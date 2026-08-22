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
	"time"

	_ "modernc.org/sqlite"
)

var ErrIdempotencyConflict = errors.New("idempotency key was already used for different event data")

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
}

type Query struct {
	ProjectID  string
	After      int64
	Limit      int
	Actor      string
	Type       string
	ForSession string
	Latest     bool
}

type DB struct{ db *sql.DB }

func Open(ctx context.Context, path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA foreign_keys=ON",
		"PRAGMA busy_timeout=5000",
	} {
		if _, err := db.ExecContext(ctx, pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("configure database: %w", err)
		}
	}
	if err := migrate(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	return &DB{db: db}, nil
}

func (d *DB) Close() error { return d.db.Close() }

func migrate(ctx context.Context, db *sql.DB) error {
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
	if _, err := db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("initialize database: %w", err)
	}
	return nil
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
	_, err = tx.ExecContext(ctx, `
INSERT INTO events
(id, project_id, actor, session_id, recipient_session, reply_to, event_type, payload, worktree, branch, commit_sha, idempotency_key, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, in.ProjectID, in.Actor, in.Session, in.Recipient, in.ReplyTo, in.Type, string(in.Payload),
		in.Worktree, in.Branch, in.Commit, in.IdempotencyKey, created)
	if err != nil {
		return Event{}, false, fmt.Errorf("append event: %w", err)
	}
	result, err := tx.ExecContext(ctx, `
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
	return got.ProjectID == want.ProjectID && got.Actor == want.Actor && got.Session == want.Session &&
		got.Recipient == want.Recipient && got.ReplyTo == want.ReplyTo && got.Type == want.Type &&
		got.Worktree == want.Worktree && string(aj) == string(bj)
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
  AND (? = '' OR actor = ?)
  AND (? = '' OR event_type = ?)
  AND (? = '' OR recipient_session = '' OR recipient_session = ? OR session_id = ?)`
	order := " ORDER BY sequence ASC LIMIT ?"
	if q.Latest {
		base = "SELECT * FROM (" + base + " ORDER BY sequence DESC LIMIT ?) ORDER BY sequence ASC"
		order = ""
	}
	rows, err := d.db.QueryContext(ctx, base+order,
		q.ProjectID, q.After, q.Actor, q.Actor, q.Type, q.Type, q.ForSession, q.ForSession, q.ForSession, q.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []Event
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

func (d *DB) Active(ctx context.Context, projectID string) ([]Event, error) {
	rows, err := d.db.QueryContext(ctx, `
WITH lifecycle AS (
    SELECT sequence, id, project_id, actor, session_id, recipient_session, reply_to, event_type,
           payload, worktree, branch, commit_sha, idempotency_key, created_at,
           row_number() OVER (
               PARTITION BY project_id, session_id, json_extract(payload, '$.task')
               ORDER BY sequence DESC
           ) AS rank
    FROM events
    WHERE project_id = ?
      AND event_type IN ('work.started', 'work.finished', 'work.deferred')
      AND json_extract(payload, '$.task') IS NOT NULL
      AND json_extract(payload, '$.task') != ''
)
SELECT sequence, id, project_id, actor, session_id, recipient_session, reply_to, event_type,
       payload, worktree, branch, commit_sha, idempotency_key, created_at
FROM lifecycle
WHERE rank = 1 AND event_type = 'work.started'
ORDER BY sequence ASC`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []Event
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
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

func NewID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
