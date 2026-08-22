package cli

import (
	"encoding/json"
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

func TestNormalizeFilesUsesInvocationDirectory(t *testing.T) {
	got := normalizeFiles("/repo", "/repo/internal", []string{"api.go", "/repo/web/app.go"})
	if len(got) != 2 || got[0] != "internal/api.go" || got[1] != "web/app.go" {
		t.Fatalf("normalized files = %v", got)
	}
}

func TestFilterActive(t *testing.T) {
	payload, _ := json.Marshal(messagePayload{Task: "api", Files: []string{"internal/api"}})
	events := []store.Event{{Actor: "agent", Payload: payload}}
	if got := filterActive(events, []string{"internal/api/handler.go"}); len(got) != 1 {
		t.Fatalf("expected conflict, got %+v", got)
	}
	if got := filterActive(events, []string{"web/app.go"}); len(got) != 0 {
		t.Fatalf("expected no conflict, got %+v", got)
	}
}
