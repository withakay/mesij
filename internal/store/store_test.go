package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
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
