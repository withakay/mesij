package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(context.Background(), filepath.Join(t.TempDir(), "events.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func testEvent(key, message string) NewEvent {
	payload, _ := json.Marshal(map[string]any{"message": message})
	return NewEvent{
		ProjectID: "project", Actor: "agent", Session: "session-1", Type: "message.posted",
		Payload: payload, Worktree: "/repo", Branch: "main", Commit: "abc", IdempotencyKey: key,
	}
}

func TestAppendIsIdempotentAndImmutable(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	first, inserted, err := db.Append(ctx, testEvent("stable-key", "hello"))
	if err != nil || !inserted {
		t.Fatalf("first append: inserted=%v err=%v", inserted, err)
	}
	second, inserted, err := db.Append(ctx, testEvent("stable-key", "hello"))
	if err != nil || inserted || second.ID != first.ID || second.Sequence != first.Sequence {
		t.Fatalf("retry: event=%+v inserted=%v err=%v", second, inserted, err)
	}
	if _, _, err := db.Append(ctx, testEvent("stable-key", "different")); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("expected payload idempotency conflict, got %v", err)
	}
	otherWorktree := testEvent("stable-key", "hello")
	otherWorktree.Worktree = "/other-worktree"
	if _, _, err := db.Append(ctx, otherWorktree); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("expected worktree idempotency conflict, got %v", err)
	}
	otherBranch := testEvent("stable-key", "hello")
	otherBranch.Branch = "feature"
	if _, _, err := db.Append(ctx, otherBranch); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("expected branch idempotency conflict, got %v", err)
	}
	otherCommit := testEvent("stable-key", "hello")
	otherCommit.Commit = "def"
	if _, _, err := db.Append(ctx, otherCommit); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("expected commit idempotency conflict, got %v", err)
	}
	if _, err := db.db.ExecContext(ctx, "UPDATE events SET actor = 'other' WHERE id = ?", first.ID); err == nil {
		t.Fatal("expected update to be rejected")
	}
	if _, err := db.db.ExecContext(ctx, "DELETE FROM events WHERE id = ?", first.ID); err == nil {
		t.Fatal("expected event delete to be rejected")
	}
	if _, err := db.db.ExecContext(ctx, "DELETE FROM idempotency_keys WHERE event_id = ?", first.ID); err == nil {
		t.Fatal("expected idempotency key delete to be rejected")
	}
}

func TestSchemaVersionAndMaterializedActiveWork(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	var version int
	if err := db.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version < 2 {
		t.Fatalf("schema version = %d, want at least 2", version)
	}
	body, _ := json.Marshal(map[string]any{"task": "task-a", "files": []string{"internal/api.go"}})
	event, _, err := db.Append(ctx, NewEvent{
		ProjectID: "project", Actor: "agent", Session: "session", Type: "work.implementing",
		Payload: body, Worktree: "/repo", IdempotencyKey: "active",
	})
	if err != nil {
		t.Fatal(err)
	}
	var eventID string
	if err := db.db.QueryRowContext(ctx, `SELECT event_id FROM active_work WHERE project_id = ? AND session_id = ?`, "project", "session").Scan(&eventID); err != nil {
		t.Fatal(err)
	}
	if eventID != event.ID {
		t.Fatalf("active projection event = %q, want %q", eventID, event.ID)
	}
}

func TestSessionStartIsIdempotentAcrossWorktrees(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	body, _ := json.Marshal(map[string]any{"message": "Agent session opened"})
	first := NewEvent{
		ProjectID: "project", Actor: "agent", Session: "stable-session", Type: "session.started",
		Payload: body, Worktree: "/repo", Branch: "main", Commit: "abc", IdempotencyKey: "session-started",
	}
	event, inserted, err := db.Append(ctx, first)
	if err != nil || !inserted {
		t.Fatalf("first session append: event=%+v inserted=%v err=%v", event, inserted, err)
	}
	retry := first
	retry.Worktree, retry.Branch, retry.Commit = "/repo-worktree", "feature", "def"
	repeated, inserted, err := db.Append(ctx, retry)
	if err != nil || inserted || repeated.ID != event.ID {
		t.Fatalf("cross-worktree retry: event=%+v inserted=%v err=%v", repeated, inserted, err)
	}
}

func TestAgentsMentionsAndReplyRouting(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	start := func(actor, session, key string) {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"message": "started"})
		if _, _, err := db.Append(ctx, NewEvent{
			ProjectID: "project", Actor: actor, Session: session, Type: "session.started",
			Payload: body, Worktree: "/repo", IdempotencyKey: key,
		}); err != nil {
			t.Fatal(err)
		}
	}
	start("alice", "session-a", "start-a")
	start("bob", "session-b", "start-b")

	resolved, err := db.ResolveRecipient(ctx, "project", "bob")
	if err != nil || resolved != "session-b" {
		t.Fatalf("resolved recipient = %q, err=%v", resolved, err)
	}
	message := testEvent("message", "hello @bob")
	message.Actor, message.Session, message.Mentions = "alice", "session-a", []string{"bob"}
	posted, _, err := db.Append(ctx, message)
	if err != nil {
		t.Fatal(err)
	}
	inbox, err := db.Inbox(ctx, "project", "session-b", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(inbox) != 1 || inbox[0].ID != posted.ID {
		t.Fatalf("bob inbox = %+v", inbox)
	}
	replySession, err := db.ReplyRecipient(ctx, "project", posted.ID)
	if err != nil || replySession != "session-a" {
		t.Fatalf("reply recipient = %q, err=%v", replySession, err)
	}
	if _, err := db.ReplyRecipient(ctx, "project", "missing"); !errors.Is(err, ErrReplyTargetNotFound) {
		t.Fatalf("missing reply target error = %v", err)
	}
	start("bob", "session-b-2", "start-b-2")
	if _, err := db.ResolveRecipient(ctx, "project", "bob"); !errors.Is(err, ErrAmbiguousRecipient) {
		t.Fatalf("duplicate alias error = %v", err)
	}
}

func TestConcurrentOpenAndAppend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.sqlite3")
	const workers = 24
	var wait sync.WaitGroup
	errors := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wait.Add(1)
		go func(i int) {
			defer wait.Done()
			ctx := context.Background()
			db, err := Open(ctx, path)
			if err != nil {
				errors <- err
				return
			}
			defer db.Close()
			event := testEvent(fmt.Sprintf("concurrent-%d", i), "message")
			event.Session = fmt.Sprintf("session-%d", i)
			if _, _, err := db.Append(ctx, event); err != nil {
				errors <- err
			}
		}(i)
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		t.Errorf("concurrent operation: %v", err)
	}
}

func TestMigrationRebuildsActiveWorkFromLegacyEvents(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.sqlite3")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = legacy.ExecContext(ctx, `
CREATE TABLE events (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT, id TEXT NOT NULL UNIQUE, project_id TEXT NOT NULL,
    actor TEXT NOT NULL, session_id TEXT NOT NULL, recipient_session TEXT NOT NULL DEFAULT '',
    reply_to TEXT NOT NULL DEFAULT '', event_type TEXT NOT NULL, payload TEXT NOT NULL,
    worktree TEXT NOT NULL, branch TEXT NOT NULL, commit_sha TEXT NOT NULL,
    idempotency_key TEXT NOT NULL, created_at TEXT NOT NULL
);
INSERT INTO events(id, project_id, actor, session_id, event_type, payload, worktree, branch, commit_sha, idempotency_key, created_at)
VALUES ('legacy-event', 'project', 'agent', 'session', 'work.implementing',
        '{"task":"legacy-task"}', '/repo', 'main', 'abc', 'legacy-key', '2026-01-01T00:00:00Z');
INSERT INTO events(id, project_id, actor, session_id, event_type, payload, worktree, branch, commit_sha, idempotency_key, created_at)
VALUES ('malformed-event', 'project', 'agent', 'session', 'work.implementing',
        '{}', '/repo', 'main', 'abc', 'malformed-key', '2026-01-01T00:00:01Z');`)
	if err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	active, err := db.Active(ctx, "project")
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 || active[0].ID != "legacy-event" {
		t.Fatalf("active work = %+v", active)
	}
	projectionErrors, err := db.ProjectionErrorCount(ctx, "project")
	if err != nil || projectionErrors != 1 {
		t.Fatalf("projection errors = %d, err=%v", projectionErrors, err)
	}
}

func TestActiveThroughUsesCursorBoundary(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	appendWork := func(session, key string) Event {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"task": key})
		event, _, err := db.Append(ctx, NewEvent{
			ProjectID: "project", Actor: "agent", Session: session, Type: "work.implementing",
			Payload: body, Worktree: "/repo", IdempotencyKey: key,
		})
		if err != nil {
			t.Fatal(err)
		}
		return event
	}
	first := appendWork("session-1", "first")
	appendWork("session-2", "second")
	active, err := db.ActiveThrough(ctx, "project", first.Sequence)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 || active[0].ID != first.ID {
		t.Fatalf("bounded active work = %+v", active)
	}
}

func TestConcurrentSameKeyReturnsOneEvent(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "events.sqlite3")
	first, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	type result struct {
		event    Event
		inserted bool
		err      error
	}
	results := make(chan result, 2)
	start := make(chan struct{})
	for _, db := range []*DB{first, second} {
		go func(db *DB) {
			<-start
			event, inserted, err := db.Append(ctx, testEvent("same-key", "same-message"))
			results <- result{event, inserted, err}
		}(db)
	}
	close(start)
	a, b := <-results, <-results
	if a.err != nil || b.err != nil || a.event.ID != b.event.ID || a.inserted == b.inserted {
		t.Fatalf("concurrent results: a=%+v b=%+v", a, b)
	}
}

func TestProjectionsAreIsolatedByProject(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	for _, project := range []string{"project-a", "project-b"} {
		body, _ := json.Marshal(map[string]any{"message": "started"})
		if _, _, err := db.Append(ctx, NewEvent{
			ProjectID: project, Actor: "agent", Session: "session", Type: "session.started",
			Payload: body, Worktree: "/repo", IdempotencyKey: "start",
		}); err != nil {
			t.Fatal(err)
		}
	}
	work, _ := json.Marshal(map[string]any{"task": "task-a"})
	if _, _, err := db.Append(ctx, NewEvent{
		ProjectID: "project-a", Actor: "agent", Session: "session", Type: "work.implementing",
		Payload: work, Worktree: "/repo", IdempotencyKey: "work",
	}); err != nil {
		t.Fatal(err)
	}
	message := testEvent("mention", "hello")
	message.ProjectID, message.Mentions = "project-a", []string{"agent"}
	if _, _, err := db.Append(ctx, message); err != nil {
		t.Fatal(err)
	}
	active, err := db.Active(ctx, "project-b")
	if err != nil || len(active) != 0 {
		t.Fatalf("project-b active work = %+v, err=%v", active, err)
	}
	inbox, err := db.Inbox(ctx, "project-b", "session", 0, 100)
	if err != nil || len(inbox) != 0 {
		t.Fatalf("project-b inbox = %+v, err=%v", inbox, err)
	}
}

func TestActiveWorkIsDerivedFromLifecycle(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	appendLifecycle := func(session, actor, task, kind, key string) {
		t.Helper()
		payload, _ := json.Marshal(map[string]any{"task": task, "files": []string{"internal/api.go"}})
		_, _, err := db.Append(ctx, NewEvent{ProjectID: "project", Actor: actor, Session: session, Type: kind,
			Payload: payload, Worktree: "/repo", Branch: "main", IdempotencyKey: key})
		if err != nil {
			t.Fatal(err)
		}
	}
	appendLifecycle("s1", "a1", "task-a", "work.started", "1")
	appendLifecycle("s2", "a2", "task-b", "work.started", "2")
	appendLifecycle("s1", "a1", "task-a", "work.finished", "3")

	active, err := db.Active(ctx, "project")
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 || active[0].Session != "s2" {
		t.Fatalf("active work = %+v", active)
	}
}

func TestListRoutesBroadcastsAndDirectMessages(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	for i, recipient := range []string{"", "session-a", "session-b"} {
		in := testEvent(string(rune('a'+i)), "message")
		in.Session = "sender"
		in.Recipient = recipient
		if _, _, err := db.Append(ctx, in); err != nil {
			t.Fatal(err)
		}
	}
	events, err := db.List(ctx, Query{ProjectID: "project", ForSession: "session-a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Recipient != "" || events[1].Recipient != "session-a" {
		t.Fatalf("routed events = %+v", events)
	}
}

func TestListLatestReturnsNewestWindowInCursorOrder(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	for i := 0; i < 5; i++ {
		in := testEvent(string(rune('a'+i)), "message")
		if _, _, err := db.Append(ctx, in); err != nil {
			t.Fatal(err)
		}
	}
	events, err := db.List(ctx, Query{ProjectID: "project", Limit: 2, Latest: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Sequence != 4 || events[1].Sequence != 5 {
		t.Fatalf("latest events = %+v", events)
	}
}

func TestInsertOrReplaceCannotMutateEvent(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	event, _, err := db.Append(ctx, testEvent("key", "message"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.db.ExecContext(ctx, `
INSERT OR REPLACE INTO events
SELECT sequence, id, project_id, 'evil', session_id, recipient_session, reply_to, event_type,
       payload, worktree, branch, commit_sha, idempotency_key, created_at
FROM events WHERE id = ?`, event.ID)
	if err == nil {
		t.Fatal("expected INSERT OR REPLACE to be rejected")
	}
	got, err := db.byKey(ctx, event.ProjectID, event.Session, event.IdempotencyKey)
	if err != nil {
		t.Fatal(err)
	}
	if got.Actor != event.Actor {
		t.Fatalf("actor changed to %q", got.Actor)
	}
}

func TestPlanImplementFinishProjectionPreservesScopes(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	appendEvent := func(kind, key string, payload map[string]any) {
		t.Helper()
		body, _ := json.Marshal(payload)
		_, _, err := db.Append(ctx, NewEvent{
			ProjectID: "project", Actor: "agent", Session: "session", Type: kind,
			Payload: body, Worktree: "/repo", IdempotencyKey: key,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	appendEvent("work.planned", "plan", map[string]any{
		"task": "task-42", "change": "api-v2", "phase": "plan", "files": []string{"internal/api.go"},
	})
	active, err := db.Active(ctx, "project")
	if err != nil || len(active) != 1 || active[0].Type != "work.planned" {
		t.Fatalf("planned active work = %+v, err=%v", active, err)
	}
	// The implementing event need not repeat scopes already established by the
	// plan; the event-sourced projection carries them forward conservatively.
	appendEvent("work.implementing", "implement", map[string]any{
		"task": "task-42", "phase": "implement",
	})
	active, err = db.Active(ctx, "project")
	if err != nil || len(active) != 1 || active[0].Type != "work.implementing" {
		t.Fatalf("implementing active work = %+v, err=%v", active, err)
	}
	var projected projectionPayload
	if err := json.Unmarshal(active[0].Projection, &projected); err != nil {
		t.Fatal(err)
	}
	if projected.Change != "api-v2" || len(projected.Files) != 1 || projected.Files[0] != "internal/api.go" {
		t.Fatalf("projected scopes = %+v", projected)
	}
	appendEvent("work.finished", "finish", map[string]any{"task": "task-42"})
	active, err = db.Active(ctx, "project")
	if err != nil || len(active) != 0 {
		t.Fatalf("finished active work = %+v, err=%v", active, err)
	}
}

func TestResolveWorkCarriesIdentityAcrossChangingScopes(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	appendPayload := func(kind, key string, payload map[string]any) {
		t.Helper()
		body, _ := json.Marshal(payload)
		_, _, err := db.Append(ctx, NewEvent{
			ProjectID: "project", Actor: "agent", Session: "session", Type: kind,
			Payload: body, Worktree: "/repo", IdempotencyKey: key,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	appendPayload("work.planned", "plan", map[string]any{"change": "change-c"})
	appendPayload("work.implementing", "implement", map[string]any{
		"work": "change:change-c", "task": "task-t", "change": "change-c",
	})
	work, found, err := db.ResolveWork(ctx, "project", "session", "task-t", "")
	if err != nil || !found || work != "change:change-c" {
		t.Fatalf("resolved work = %q found=%v err=%v", work, found, err)
	}
	appendPayload("work.finished", "finish", map[string]any{
		"work": "change:change-c", "task": "task-t",
	})
	work, found, err = db.ResolveWork(ctx, "project", "session", "task-t", "")
	if err != nil || !found || work != "change:change-c" {
		t.Fatalf("resolved closed work = %q found=%v err=%v", work, found, err)
	}
}

func TestResolveWorkRejectsAmbiguousActiveClaims(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	for i, work := range []string{"work-one", "work-two"} {
		body, _ := json.Marshal(map[string]any{"work": work, "task": "task-t"})
		_, _, err := db.Append(ctx, NewEvent{
			ProjectID: "project", Actor: "agent", Session: "session", Type: "work.implementing",
			Payload: body, Worktree: "/repo", IdempotencyKey: fmt.Sprintf("key-%d", i),
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := db.ResolveWork(ctx, "project", "session", "task-t", ""); !errors.Is(err, ErrAmbiguousWork) {
		t.Fatalf("expected ambiguous work, got %v", err)
	}
}
func TestListHonorsSnapshotHighWater(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	for i := 0; i < 4; i++ {
		in := testEvent(fmt.Sprintf("snapshot-%d", i), "message")
		if _, _, err := db.Append(ctx, in); err != nil {
			t.Fatal(err)
		}
	}
	events, err := db.List(ctx, Query{ProjectID: "project", After: 1, Through: 3, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Sequence != 2 || events[1].Sequence != 3 {
		t.Fatalf("snapshot events = %+v", events)
	}
}

func TestSourceContextRoundTripsAndIsNotComparedOnRetry(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	first := NewEvent{ProjectID: "project", Actor: "agent", Session: "session", Type: "message.posted",
		Payload: json.RawMessage(`{"message":"hi"}`), Worktree: "/repo", Branch: "main", Commit: "abc",
		Host: "laptop", User: "jack", IP: "100.64.0.9", IdempotencyKey: "source-key"}
	event, inserted, err := db.Append(ctx, first)
	if err != nil || !inserted {
		t.Fatalf("append: inserted=%v err=%v", inserted, err)
	}
	if event.Host != "laptop" || event.User != "jack" || event.IP != "100.64.0.9" {
		t.Fatalf("source context not stored: %+v", event)
	}
	listed, err := db.List(ctx, Query{ProjectID: "project", Limit: 10})
	if err != nil || len(listed) != 1 || listed[0].Host != "laptop" || listed[0].IP != "100.64.0.9" {
		t.Fatalf("list = %+v err=%v", listed, err)
	}
	retry := first
	retry.Host, retry.IP = "laptop.lan", "192.168.1.2"
	again, inserted, err := db.Append(ctx, retry)
	if err != nil || inserted || again.ID != event.ID {
		t.Fatalf("retry with changed host/ip must return original: inserted=%v err=%v", inserted, err)
	}
}
