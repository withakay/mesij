package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"mesij/internal/project"
	"mesij/internal/store"
)

func TestTailWritesJSONLinesAfterCursor(t *testing.T) {
	ctx := context.Background()
	database := filepath.Join(t.TempDir(), "events.sqlite3")
	p := project.Context{Name: "test", ID: "project", Root: "/repo", Invocation: "/repo", Worktree: "/repo", Database: database}
	db, err := store.Open(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		payload, _ := json.Marshal(map[string]any{"message": i})
		_, _, err := db.Append(ctx, store.NewEvent{
			ProjectID: p.ID, Actor: "agent", Session: "session", Type: "message.posted",
			Payload: payload, Worktree: p.Worktree, IdempotencyKey: string(rune('a' + i)),
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	var stdout, stderr bytes.Buffer
	runner := Runner{Stdout: &stdout, Stderr: &stderr}
	if code := runner.tail(ctx, p, []string{"--after", "1", "--limit", "10"}); code != 0 {
		t.Fatalf("tail code=%d stderr=%s", code, stderr.String())
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("JSONL lines = %q", lines)
	}
	for i, line := range lines {
		var event store.Event
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("line %d: %v", i, err)
		}
		if event.Sequence != int64(i+2) {
			t.Fatalf("line %d sequence = %d", i, event.Sequence)
		}
	}
}
func TestTailRejectsOversizedLimit(t *testing.T) {
	var stdout, stderr bytes.Buffer
	runner := Runner{Stdout: &stdout, Stderr: &stderr}
	if code := runner.tail(context.Background(), project.Context{}, []string{"--limit", "1001"}); code != 2 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
}
