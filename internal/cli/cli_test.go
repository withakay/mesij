package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"mesij/internal/store"
)

func TestPathsOverlap(t *testing.T) {
	for _, test := range []struct {
		a, b string
		want bool
	}{
		{"internal/api.go", "internal/api.go", true},
		{"internal", "internal/api.go", true},
		{"internal/api", "internal/api.go", false},
		{"cmd/a", "cmd/b", false},
		{".", "anything", true},
	} {
		if got := pathsOverlap(test.a, test.b); got != test.want {
			t.Errorf("pathsOverlap(%q, %q) = %v, want %v", test.a, test.b, got, test.want)
		}
	}
}

func TestNormalizeFilesCanonicalizesSymlinkedRoots(t *testing.T) {
	realRoot := t.TempDir()
	linkRoot := filepath.Join(t.TempDir(), "repo")
	if err := os.Symlink(realRoot, linkRoot); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(realRoot, "internal"), 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(realRoot, "internal", "api.go")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	got := normalizeFiles(realRoot, linkRoot, []string{"internal/api.go"})
	if len(got) != 1 || got[0] != "internal/api.go" {
		t.Fatalf("normalized files = %v", got)
	}
}

func TestNormalizeFilesUsesInvocationDirectory(t *testing.T) {
	got := normalizeFiles("/repo", "/repo/internal", []string{"api.go", "/repo/web/app.go"})
	if len(got) != 2 || got[0] != "internal/api.go" || got[1] != "web/app.go" {
		t.Fatalf("normalized files = %v", got)
	}
}

func TestFilterActive(t *testing.T) {
	payload, _ := json.Marshal(messagePayload{Task: "api", Files: []string{"internal/api"}})
	events := []store.Event{{Actor: "agent", Payload: payload}}
	if got := filterActive(events, coordinationQuery{Files: []string{"internal/api/handler.go"}}); len(got) != 1 {
		t.Fatalf("expected conflict, got %+v", got)
	}
	if got := filterActive(events, coordinationQuery{Files: []string{"web/app.go"}}); len(got) != 0 {
		t.Fatalf("expected no conflict, got %+v", got)
	}
}
func TestFilterActiveByTaskChangeAndPhase(t *testing.T) {
	plan, _ := json.Marshal(messagePayload{Work: "task:42", Task: "42", Change: "api-v2", Phase: "plan"})
	implement, _ := json.Marshal(messagePayload{Work: "task:43", Task: "43", Change: "web-v2", Phase: "implement"})
	events := []store.Event{
		{Type: "work.planned", Payload: plan},
		{Type: "work.implementing", Payload: implement},
	}
	if got := filterActive(events, coordinationQuery{Task: "42"}); len(got) != 1 || got[0].Type != "work.planned" {
		t.Fatalf("task matches = %+v", got)
	}
	if got := filterActive(events, coordinationQuery{Change: "web-v2", Phase: "implement"}); len(got) != 1 || got[0].Type != "work.implementing" {
		t.Fatalf("change/phase matches = %+v", got)
	}
	if got := filterActive(events, coordinationQuery{Task: "42", Phase: "implement"}); len(got) != 0 {
		t.Fatalf("unexpected phase match = %+v", got)
	}
}
