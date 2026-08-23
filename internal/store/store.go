package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"modernc.org/sqlite"
)

var ErrIdempotencyConflict = errors.New("idempotency key was already used for different event data")
var ErrAmbiguousWork = errors.New("task and change refer to different work identities")
var ErrRecipientNotFound = errors.New("recipient agent or session was not found")
var ErrAmbiguousRecipient = errors.New("actor alias matches multiple sessions; use an exact session ID")
var ErrReplyTargetNotFound = errors.New("reply target was not found")
var ErrInvalidLifecycle = errors.New("lifecycle event has no work identity")

type Event struct {
	Sequence       int64           `json:"sequence"`
	ID             string          `json:"id"`
	ProjectID      string          `json:"project_id"`
	Actor          string          `json:"actor"`
	Session        string          `json:"session"`
	Recipient      string          `json:"recipient_session,omitempty"`
	ReplyTo        string          `json:"reply_to,omitempty"`
	Type           string          `json:"type"`
	Payload        json.RawMessage `json:"payload"`
	Projection     json.RawMessage `json:"projection,omitempty"`
	Worktree       string          `json:"worktree"`
	Branch         string          `json:"branch,omitempty"`
	Commit         string          `json:"commit,omitempty"`
	IdempotencyKey string          `json:"idempotency_key"`
	CreatedAt      time.Time       `json:"created_at"`
}

type NewEvent struct {
	ProjectID      string
	Actor          string
	Session        string
	Recipient      string
	ReplyTo        string
	Type           string
	Payload        json.RawMessage
	Worktree       string
	Branch         string
	Commit         string
	IdempotencyKey string
	Mentions       []string
}

type Query struct {
	ProjectID  string
	After      int64
	Through    int64
	Limit      int
	Actor      string
	Type       string
	ForSession string
	Latest     bool
}

type Agent struct {
	Actor      string    `json:"actor"`
	Session    string    `json:"session"`
	StartedAt  time.Time `json:"started_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
}

type DB struct{ db *sql.DB }

const schemaVersion = 3

func Open(ctx context.Context, path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, "PRAGMA busy_timeout=5000"); err != nil {
		db.Close()
		return nil, fmt.Errorf("configure database timeout: %w", err)
	}
	for _, pragma := range []string{"PRAGMA journal_mode=WAL", "PRAGMA synchronous=NORMAL", "PRAGMA foreign_keys=ON"} {
		if err := retryBusy(ctx, func() error {
			_, err := db.ExecContext(ctx, pragma)
			return err
		}); err != nil {
			db.Close()
			return nil, fmt.Errorf("configure database: %w", err)
		}
	}
	if err := retryBusy(ctx, func() error { return migrate(ctx, db) }); err != nil {
		db.Close()
		return nil, err
	}
	return &DB{db: db}, nil
}

func (d *DB) Close() error { return d.db.Close() }

func migrate(ctx context.Context, db *sql.DB) error {
	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if version >= schemaVersion {
		return nil
	}
	const schema = `
CREATE TABLE IF NOT EXISTS events (
    sequence          INTEGER PRIMARY KEY AUTOINCREMENT,
    id                TEXT NOT NULL UNIQUE,
    project_id        TEXT NOT NULL,
    actor             TEXT NOT NULL CHECK(length(actor) > 0),
    session_id        TEXT NOT NULL CHECK(length(session_id) > 0),
    recipient_session TEXT NOT NULL DEFAULT '',
    reply_to          TEXT NOT NULL DEFAULT '',
    event_type        TEXT NOT NULL CHECK(length(event_type) > 0),
    payload           TEXT NOT NULL CHECK(json_valid(payload)),
    worktree          TEXT NOT NULL,
    branch            TEXT NOT NULL,
    commit_sha        TEXT NOT NULL,
    idempotency_key   TEXT NOT NULL CHECK(length(idempotency_key) > 0),
    created_at        TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS idempotency_keys (
    project_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    key        TEXT NOT NULL,
    event_id   TEXT NOT NULL UNIQUE REFERENCES events(id),
    PRIMARY KEY(project_id, session_id, key)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS events_project_sequence ON events(project_id, sequence);
CREATE INDEX IF NOT EXISTS events_project_type_sequence ON events(project_id, event_type, sequence);
CREATE INDEX IF NOT EXISTS events_project_actor_sequence ON events(project_id, actor, sequence);
CREATE INDEX IF NOT EXISTS events_project_session_sequence ON events(project_id, session_id, sequence);
CREATE INDEX IF NOT EXISTS events_project_recipient_sequence ON events(project_id, recipient_session, sequence);

CREATE TABLE IF NOT EXISTS agents (
    project_id   TEXT NOT NULL,
    session_id   TEXT NOT NULL,
    actor        TEXT NOT NULL,
    started_at   TEXT NOT NULL,
    last_seen_at TEXT NOT NULL,
    PRIMARY KEY(project_id, session_id)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS agents_project_actor_seen ON agents(project_id, actor, last_seen_at DESC);

CREATE TABLE IF NOT EXISTS mentions (
    event_id TEXT NOT NULL REFERENCES events(id),
    actor    TEXT NOT NULL,
    PRIMARY KEY(event_id, actor)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS mentions_actor_event ON mentions(actor, event_id);

CREATE TABLE IF NOT EXISTS active_work (
    project_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    work_id    TEXT NOT NULL,
    event_id   TEXT NOT NULL UNIQUE REFERENCES events(id),
    projection TEXT NOT NULL CHECK(json_valid(projection)),
    PRIMARY KEY(project_id, session_id, work_id)
) WITHOUT ROWID;

CREATE TABLE IF NOT EXISTS projection_errors (
    event_id   TEXT PRIMARY KEY REFERENCES events(id),
    project_id TEXT NOT NULL,
    reason     TEXT NOT NULL
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS projection_errors_project ON projection_errors(project_id, event_id);

CREATE TRIGGER IF NOT EXISTS idempotency_keys_immutable_update
BEFORE UPDATE ON idempotency_keys BEGIN
    SELECT RAISE(ABORT, 'idempotency keys are immutable');
END;
CREATE TRIGGER IF NOT EXISTS idempotency_keys_immutable_delete
BEFORE DELETE ON idempotency_keys BEGIN
    SELECT RAISE(ABORT, 'idempotency keys are immutable');
END;

CREATE TRIGGER IF NOT EXISTS events_immutable_insert
BEFORE INSERT ON events
WHEN EXISTS (
    SELECT 1 FROM events
    WHERE id = NEW.id OR (NEW.sequence > 0 AND sequence = NEW.sequence)
) BEGIN
    SELECT RAISE(ABORT, 'events are immutable');
END;
CREATE TRIGGER IF NOT EXISTS events_immutable_update
BEFORE UPDATE ON events BEGIN
    SELECT RAISE(ABORT, 'events are immutable');
END;
CREATE TRIGGER IF NOT EXISTS events_immutable_delete
BEFORE DELETE ON events BEGIN
    SELECT RAISE(ABORT, 'events are immutable');
END;
`
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin schema migration: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("initialize database: %w", err)
	}
	if version < 2 {
		if err := rebuildActiveWork(ctx, tx); err != nil {
			return fmt.Errorf("rebuild active work: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version=%d", schemaVersion)); err != nil {
		return fmt.Errorf("set schema version: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit schema migration: %w", err)
	}
	return nil
}

func retryBusy(ctx context.Context, operation func() error) error {
	delay := 10 * time.Millisecond
	for attempt := 0; ; attempt++ {
		err := operation()
		if err == nil || !isBusy(err) || attempt == 8 {
			return err
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		if delay < 320*time.Millisecond {
			delay *= 2
		}
	}
}

func isBusy(err error) bool {
	var sqliteErr *sqlite.Error
	return errors.As(err, &sqliteErr) && sqliteErr.Code()&0xff == 5
}

func (d *DB) Append(ctx context.Context, in NewEvent) (event Event, inserted bool, err error) {
	if in.ProjectID == "" || in.Actor == "" || in.Session == "" || in.Type == "" {
		return Event{}, false, errors.New("project, actor, session, and event type are required")
	}
	if !json.Valid(in.Payload) {
		return Event{}, false, errors.New("payload must be valid JSON")
	}
	if in.IdempotencyKey == "" {
		in.IdempotencyKey, err = NewID()
		if err != nil {
			return Event{}, false, err
		}
	}
	id, err := NewID()
	if err != nil {
		return Event{}, false, err
	}
	created := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return Event{}, false, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
INSERT INTO events
(id, project_id, actor, session_id, recipient_session, reply_to, event_type, payload, worktree, branch, commit_sha, idempotency_key, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, in.ProjectID, in.Actor, in.Session, in.Recipient, in.ReplyTo, in.Type, string(in.Payload),
		in.Worktree, in.Branch, in.Commit, in.IdempotencyKey, created)
	if err != nil {
		return Event{}, false, fmt.Errorf("append event: %w", err)
	}
	sequence, err := result.LastInsertId()
	if err != nil {
		return Event{}, false, fmt.Errorf("read event sequence: %w", err)
	}
	result, err = tx.ExecContext(ctx, `
INSERT INTO idempotency_keys(project_id, session_id, key, event_id)
VALUES (?, ?, ?, ?)
ON CONFLICT(project_id, session_id, key) DO NOTHING`, in.ProjectID, in.Session, in.IdempotencyKey, id)
	if err != nil {
		return Event{}, false, fmt.Errorf("record idempotency key: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return Event{}, false, err
	}
	if rows == 0 {
		if err := tx.Rollback(); err != nil {
			return Event{}, false, err
		}
		event, err = d.byKey(ctx, in.ProjectID, in.Session, in.IdempotencyKey)
		if err != nil {
			return Event{}, false, err
		}
		if !sameEvent(event, in) {
			return Event{}, false, ErrIdempotencyConflict
		}
		return event, false, nil
	}
	event = Event{
		Sequence: sequence, ID: id, ProjectID: in.ProjectID, Actor: in.Actor, Session: in.Session,
		Recipient: in.Recipient, ReplyTo: in.ReplyTo, Type: in.Type, Payload: in.Payload,
		Worktree: in.Worktree, Branch: in.Branch, Commit: in.Commit, IdempotencyKey: in.IdempotencyKey,
	}
	event.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	if err := projectEvent(ctx, tx, event, in.Mentions); err != nil {
		return Event{}, false, fmt.Errorf("project event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Event{}, false, err
	}
	event, err = d.byKey(ctx, in.ProjectID, in.Session, in.IdempotencyKey)
	if err != nil {
		return Event{}, false, err
	}
	return event, true, nil
}

func sameEvent(got Event, want NewEvent) bool {
	var a, b any
	if json.Unmarshal(got.Payload, &a) != nil || json.Unmarshal(want.Payload, &b) != nil {
		return false
	}
	aj, _ := json.Marshal(a)
	bj, _ := json.Marshal(b)
	sameSource := got.Worktree == want.Worktree && got.Branch == want.Branch && got.Commit == want.Commit
	if got.Type == "session.started" && want.Type == "session.started" {
		sameSource = true
	}
	return got.ProjectID == want.ProjectID && got.Actor == want.Actor && got.Session == want.Session &&
		got.Recipient == want.Recipient && got.ReplyTo == want.ReplyTo && got.Type == want.Type &&
		sameSource && string(aj) == string(bj)
}

func (d *DB) byKey(ctx context.Context, projectID, session, key string) (Event, error) {
	row := d.db.QueryRowContext(ctx, `
SELECT e.sequence, e.id, e.project_id, e.actor, e.session_id, e.recipient_session, e.reply_to, e.event_type,
       e.payload, e.worktree, e.branch, e.commit_sha, e.idempotency_key, e.created_at
FROM events e
JOIN idempotency_keys k ON k.event_id = e.id
WHERE k.project_id = ? AND k.session_id = ? AND k.key = ?`, projectID, session, key)
	return scanEvent(row)
}

func (d *DB) FindByKey(ctx context.Context, projectID, session, key string) (Event, bool, error) {
	event, err := d.byKey(ctx, projectID, session, key)
	if errors.Is(err, sql.ErrNoRows) {
		return Event{}, false, nil
	}
	return event, err == nil, err
}

func (d *DB) List(ctx context.Context, q Query) ([]Event, error) {
	if q.Limit <= 0 {
		q.Limit = 100
	}
	if q.Limit > 1000 {
		q.Limit = 1000
	}
	base := `
SELECT sequence, id, project_id, actor, session_id, recipient_session, reply_to, event_type,
       payload, worktree, branch, commit_sha, idempotency_key, created_at
FROM events
WHERE project_id = ? AND sequence > ?
  AND (? = 0 OR sequence <= ?)
  AND (? = '' OR actor = ?)
  AND (? = '' OR event_type = ?)
  AND (? = '' OR recipient_session = '' OR recipient_session = ? OR session_id = ?)`
	order := " ORDER BY sequence ASC LIMIT ?"
	if q.Latest {
		base = "SELECT * FROM (" + base + " ORDER BY sequence DESC LIMIT ?) ORDER BY sequence ASC"
		order = ""
	}
	rows, err := d.db.QueryContext(ctx, base+order,
		q.ProjectID, q.After, q.Through, q.Through, q.Actor, q.Actor, q.Type, q.Type, q.ForSession, q.ForSession, q.ForSession, q.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := make([]Event, 0)
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

func (d *DB) LatestSequence(ctx context.Context, projectID string) (int64, error) {
	var sequence int64
	err := d.db.QueryRowContext(ctx, "SELECT COALESCE(MAX(sequence), 0) FROM events WHERE project_id = ?", projectID).Scan(&sequence)
	return sequence, err
}

func (d *DB) ProjectionErrorCount(ctx context.Context, projectID string) (int, error) {
	var count int
	err := d.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM projection_errors WHERE project_id = ?`, projectID).Scan(&count)
	return count, err
}

// ResolveRecipient accepts an exact session ID or the most recently seen
// session for an actor alias.
func (d *DB) ResolveRecipient(ctx context.Context, projectID, recipient string) (string, error) {
	var session string
	err := d.db.QueryRowContext(ctx, `SELECT session_id FROM agents WHERE project_id = ? AND session_id = ?`, projectID, recipient).Scan(&session)
	if err == nil {
		return session, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("resolve recipient session: %w", err)
	}
	rows, err := d.db.QueryContext(ctx, `
SELECT session_id FROM agents WHERE project_id = ? AND actor = ? ORDER BY last_seen_at DESC LIMIT 2`, projectID, recipient)
	if err != nil {
		return "", fmt.Errorf("resolve recipient alias: %w", err)
	}
	defer rows.Close()
	var sessions []string
	for rows.Next() {
		if err := rows.Scan(&session); err != nil {
			return "", err
		}
		sessions = append(sessions, session)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if len(sessions) > 1 {
		return "", ErrAmbiguousRecipient
	}
	if len(sessions) == 1 {
		return sessions[0], nil
	}
	return "", ErrRecipientNotFound
}

func (d *DB) ListAgents(ctx context.Context, projectID string) ([]Agent, error) {
	rows, err := d.db.QueryContext(ctx, `
SELECT actor, session_id, started_at, last_seen_at
FROM agents WHERE project_id = ? ORDER BY last_seen_at DESC`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}
	defer rows.Close()
	agents := make([]Agent, 0)
	for rows.Next() {
		var agent Agent
		var started, seen string
		if err := rows.Scan(&agent.Actor, &agent.Session, &started, &seen); err != nil {
			return nil, err
		}
		var err error
		agent.StartedAt, err = time.Parse(time.RFC3339Nano, started)
		if err != nil {
			return nil, err
		}
		agent.LastSeenAt, err = time.Parse(time.RFC3339Nano, seen)
		if err != nil {
			return nil, err
		}
		agents = append(agents, agent)
	}
	return agents, rows.Err()
}

func (d *DB) ReplyRecipient(ctx context.Context, projectID, eventID string) (string, error) {
	var session string
	err := d.db.QueryRowContext(ctx, `SELECT session_id FROM events WHERE project_id = ? AND id = ?`, projectID, eventID).Scan(&session)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrReplyTargetNotFound
	}
	if err != nil {
		return "", fmt.Errorf("resolve reply target: %w", err)
	}
	return session, nil
}

func (d *DB) Inbox(ctx context.Context, projectID, session string, after int64, limit int) ([]Event, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	var actor string
	if err := d.db.QueryRowContext(ctx, `SELECT actor FROM agents WHERE project_id = ? AND session_id = ?`, projectID, session).Scan(&actor); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrRecipientNotFound
		}
		return nil, fmt.Errorf("resolve inbox session: %w", err)
	}
	rows, err := d.db.QueryContext(ctx, `
SELECT DISTINCT e.sequence, e.id, e.project_id, e.actor, e.session_id, e.recipient_session, e.reply_to, e.event_type,
       e.payload, e.worktree, e.branch, e.commit_sha, e.idempotency_key, e.created_at
FROM events e
LEFT JOIN mentions m ON m.event_id = e.id
WHERE e.project_id = ? AND e.sequence > ? AND e.event_type LIKE 'message.%'
  AND (e.session_id = ? OR e.recipient_session = ? OR m.actor = ?)
ORDER BY e.sequence ASC LIMIT ?`, projectID, after, session, session, actor, limit)
	if err != nil {
		return nil, fmt.Errorf("query inbox: %w", err)
	}
	defer rows.Close()
	events := make([]Event, 0)
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func rebuildActiveWork(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM active_work; DELETE FROM mentions; DELETE FROM agents; DELETE FROM projection_errors;`); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `
SELECT sequence, id, project_id, actor, session_id, recipient_session, reply_to, event_type,
       payload, worktree, branch, commit_sha, idempotency_key, created_at
FROM events ORDER BY sequence ASC`)
	if err != nil {
		return err
	}
	var events []Event
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			rows.Close()
			return err
		}
		events = append(events, event)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, event := range events {
		var payload struct {
			Mentions []string `json:"mentions"`
		}
		_ = json.Unmarshal(event.Payload, &payload)
		if err := projectEvent(ctx, tx, event, payload.Mentions); err != nil {
			if errors.Is(err, ErrInvalidLifecycle) {
				if _, insertErr := tx.ExecContext(ctx, `INSERT INTO projection_errors(event_id, project_id, reason) VALUES (?, ?, ?)`, event.ID, event.ProjectID, err.Error()); insertErr != nil {
					return insertErr
				}
				continue
			}
			return err
		}
	}
	return nil
}

func projectEvent(ctx context.Context, tx *sql.Tx, event Event, mentions []string) error {
	created := event.CreatedAt.UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `
INSERT INTO agents(project_id, session_id, actor, started_at, last_seen_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(project_id, session_id) DO UPDATE SET actor = excluded.actor, last_seen_at = excluded.last_seen_at`,
		event.ProjectID, event.Session, event.Actor, created, created); err != nil {
		return err
	}
	seen := make(map[string]bool)
	for _, mention := range mentions {
		mention = strings.TrimPrefix(strings.TrimSpace(mention), "@")
		if mention == "" || seen[mention] {
			continue
		}
		seen[mention] = true
		if _, err := tx.ExecContext(ctx, `INSERT INTO mentions(event_id, actor) VALUES (?, ?)`, event.ID, mention); err != nil {
			return err
		}
	}
	if isLifecycleEvent(event.Type) {
		return applyActiveProjection(ctx, tx, event)
	}
	return nil
}

func applyActiveProjection(ctx context.Context, tx *sql.Tx, event Event) error {
	var current projectionPayload
	if err := json.Unmarshal(event.Payload, &current); err != nil {
		return fmt.Errorf("decode lifecycle payload: %w", err)
	}
	if current.Phase == "" {
		switch event.Type {
		case "work.planned":
			current.Phase = "plan"
		case "work.implementing", "work.started":
			current.Phase = "implement"
		}
	}
	work := current.workID()
	if work == "" {
		return ErrInvalidLifecycle
	}
	if event.Type == "work.finished" || event.Type == "work.deferred" {
		_, err := tx.ExecContext(ctx, `DELETE FROM active_work WHERE project_id = ? AND session_id = ? AND work_id = ?`, event.ProjectID, event.Session, work)
		return err
	}
	var priorJSON string
	err := tx.QueryRowContext(ctx, `SELECT projection FROM active_work WHERE project_id = ? AND session_id = ? AND work_id = ?`, event.ProjectID, event.Session, work).Scan(&priorJSON)
	if err == nil {
		var prior projectionPayload
		if json.Unmarshal([]byte(priorJSON), &prior) == nil {
			current = mergeProjection(prior, current)
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	projection, err := json.Marshal(current)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO active_work(project_id, session_id, work_id, event_id, projection)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(project_id, session_id, work_id)
DO UPDATE SET event_id = excluded.event_id, projection = excluded.projection`,
		event.ProjectID, event.Session, work, event.ID, string(projection))
	return err
}

func isLifecycleEvent(eventType string) bool {
	switch eventType {
	case "work.planned", "work.implementing", "work.started", "work.finished", "work.deferred":
		return true
	default:
		return false
	}
}

func (d *DB) ResolveWork(ctx context.Context, projectID, session, task, change string) (string, bool, error) {
	if task == "" && change == "" {
		return "", false, nil
	}
	active, err := d.Active(ctx, projectID)
	if err != nil {
		return "", false, err
	}
	candidates := make(map[string]bool)
	for _, event := range active {
		if event.Session != session {
			continue
		}
		raw := event.Projection
		if len(raw) == 0 {
			raw = event.Payload
		}
		var payload projectionPayload
		if json.Unmarshal(raw, &payload) != nil {
			continue
		}
		if (task != "" && payload.Task == task) || (change != "" && payload.Change == change) {
			candidates[payload.workID()] = true
		}
	}
	if len(candidates) > 1 {
		return "", false, ErrAmbiguousWork
	}
	for work := range candidates {
		return work, true, nil
	}

	rows, err := d.db.QueryContext(ctx, `
SELECT payload
FROM events
WHERE project_id = ? AND session_id = ?
  AND event_type IN ('work.planned', 'work.implementing', 'work.started', 'work.finished', 'work.deferred')
ORDER BY sequence DESC`, projectID, session)
	if err != nil {
		return "", false, err
	}
	defer rows.Close()
	var taskWork, changeWork string
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return "", false, err
		}
		var payload projectionPayload
		if json.Unmarshal([]byte(raw), &payload) != nil {
			continue
		}
		work := payload.workID()
		if work == "" {
			continue
		}
		if taskWork == "" && task != "" && payload.Task == task {
			taskWork = work
		}
		if changeWork == "" && change != "" && payload.Change == change {
			changeWork = work
		}
		if (task == "" || taskWork != "") && (change == "" || changeWork != "") {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return "", false, err
	}
	if taskWork != "" && changeWork != "" && taskWork != changeWork {
		return "", false, ErrAmbiguousWork
	}
	if taskWork != "" {
		return taskWork, true, nil
	}
	if changeWork != "" {
		return changeWork, true, nil
	}
	return "", false, nil
}

func (d *DB) Active(ctx context.Context, projectID string) ([]Event, error) {
	return d.ActiveThrough(ctx, projectID, 0)
}

func (d *DB) ActiveThrough(ctx context.Context, projectID string, through int64) ([]Event, error) {
	rows, err := d.db.QueryContext(ctx, `
SELECT e.sequence, e.id, e.project_id, e.actor, e.session_id, e.recipient_session, e.reply_to, e.event_type,
       e.payload, e.worktree, e.branch, e.commit_sha, e.idempotency_key, e.created_at, a.projection
FROM active_work a
JOIN events e ON e.id = a.event_id
WHERE a.project_id = ? AND (? = 0 OR e.sequence <= ?)
ORDER BY e.sequence ASC`, projectID, through, through)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := make([]Event, 0)
	for rows.Next() {
		event, err := scanProjectedEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return events, nil
}

type projectionPayload struct {
	Work    string          `json:"work,omitempty"`
	Task    string          `json:"task,omitempty"`
	Change  string          `json:"change,omitempty"`
	Phase   string          `json:"phase,omitempty"`
	Message string          `json:"message,omitempty"`
	Files   []string        `json:"files,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (p projectionPayload) workID() string {
	if p.Work != "" {
		return p.Work
	}
	if p.Task != "" {
		return "task:" + p.Task
	}
	if p.Change != "" {
		return "change:" + p.Change
	}
	return ""
}

func mergeProjection(prior, current projectionPayload) projectionPayload {
	if current.Work == "" {
		current.Work = prior.Work
	}
	if current.Task == "" {
		current.Task = prior.Task
	}
	if current.Change == "" {
		current.Change = prior.Change
	}
	if current.Phase == "" {
		current.Phase = prior.Phase
	}
	seen := make(map[string]bool, len(prior.Files)+len(current.Files))
	files := make([]string, 0, len(prior.Files)+len(current.Files))
	for _, file := range append(prior.Files, current.Files...) {
		if !seen[file] {
			seen[file] = true
			files = append(files, file)
		}
	}
	current.Files = files
	return current
}

type scanner interface{ Scan(dest ...any) error }

func scanEvent(s scanner) (Event, error) {
	var e Event
	var payload, created string
	err := s.Scan(&e.Sequence, &e.ID, &e.ProjectID, &e.Actor, &e.Session, &e.Recipient, &e.ReplyTo,
		&e.Type, &payload, &e.Worktree, &e.Branch, &e.Commit, &e.IdempotencyKey, &created)
	if err != nil {
		return Event{}, err
	}
	e.Payload = json.RawMessage(payload)
	e.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	return e, err
}

func scanProjectedEvent(s scanner) (Event, error) {
	var event Event
	var payload, projection, created string
	err := s.Scan(&event.Sequence, &event.ID, &event.ProjectID, &event.Actor, &event.Session, &event.Recipient, &event.ReplyTo,
		&event.Type, &payload, &event.Worktree, &event.Branch, &event.Commit, &event.IdempotencyKey, &created, &projection)
	if err != nil {
		return Event{}, err
	}
	event.Payload = json.RawMessage(payload)
	event.Projection = json.RawMessage(projection)
	event.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	return event, err
}

func NewID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
