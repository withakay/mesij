package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mesij/internal/store"
)

func TestInitCreatesNonGitProjectMarker(t *testing.T) {
	t.Setenv("MESIJ_HOME", t.TempDir())
	t.Setenv("MESIJ_DB", "")
	t.Setenv("MESIJ_PROJECT", "")
	dir := t.TempDir()
	runRunner(t, dir, "", "--project", "name:plain", "init")
	data, err := os.ReadFile(filepath.Join(dir, ".mesij-project"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"plain"`) {
		t.Fatalf("marker = %s", data)
	}
}

func TestAliasesMentionsInboxAndReplyRouting(t *testing.T) {
	t.Setenv("MESIJ_ACTOR", "")
	t.Setenv("MESIJ_SESSION", "")
	t.Setenv("MESIJ_DB", "")
	t.Setenv("MESIJ_PROJECT", "")
	dir := t.TempDir()
	database := filepath.Join(t.TempDir(), "events.sqlite3")
	base := []string{"--db", database, "--project", "name:test"}

	runRunner(t, dir, "", append(base, "session", "--actor", "alice", "--id", "session-a", "--json")...)
	runRunner(t, dir, "", append(base, "session", "--actor", "bob", "--id", "session-b", "--json")...)

	postedJSON := runRunner(t, dir, "", append(base, "post", "--actor", "alice", "--session", "session-a", "--message", "@bob ready", "--json")...)
	var posted struct {
		store.Event
		Inserted bool `json:"inserted"`
	}
	decodeRunnerJSON(t, postedJSON, &posted)
	if posted.Type != "message.posted" {
		t.Fatalf("posted event = %+v", posted)
	}

	inboxJSON := runRunner(t, dir, "", append(base, "inbox", "--session", "session-b", "--json")...)
	var inbox []store.Event
	decodeRunnerJSON(t, inboxJSON, &inbox)
	if len(inbox) != 1 || inbox[0].ID != posted.ID {
		t.Fatalf("bob inbox = %+v", inbox)
	}

	replyJSON := runRunner(t, dir, "", append(base, "reply", "--actor", "bob", "--session", "session-b", "--reply-to", posted.ID, "--message", "reviewed", "--json")...)
	var reply struct {
		store.Event
		Inserted bool `json:"inserted"`
	}
	decodeRunnerJSON(t, replyJSON, &reply)
	if reply.Recipient != "session-a" || reply.ReplyTo != posted.ID {
		t.Fatalf("reply = %+v", reply)
	}

	agentsJSON := runRunner(t, dir, "", append(base, "agents", "--json")...)
	var agents []store.Agent
	decodeRunnerJSON(t, agentsJSON, &agents)
	if len(agents) != 2 || agents[0].Actor == "" || agents[1].Actor == "" {
		t.Fatalf("agents = %+v", agents)
	}

	checkJSON := runRunner(t, dir, "", append(base, "check", "--json")...)
	var check struct {
		Through int64 `json:"through"`
	}
	decodeRunnerJSON(t, checkJSON, &check)
	if check.Through < reply.Sequence {
		t.Fatalf("check cursor = %d, want at least %d", check.Through, reply.Sequence)
	}
}

func TestReplyRejectsUnknownRecipient(t *testing.T) {
	t.Setenv("MESIJ_DB", "")
	t.Setenv("MESIJ_PROJECT", "")
	dir := t.TempDir()
	database := filepath.Join(t.TempDir(), "events.sqlite3")
	base := []string{"--db", database, "--project", "name:test"}
	runRunner(t, dir, "", append(base, "session", "--actor", "alice", "--id", "session-a")...)

	var stdout, stderr bytes.Buffer
	runner := Runner{Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr, Dir: dir}
	code := runner.Run(context.Background(), append(base, "reply", "--actor", "alice", "--session", "session-a", "--to", "missing", "--message", "hello"))
	if code == 0 || !strings.Contains(stderr.String(), "recipient") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestGlobalJSONAndRootHelp(t *testing.T) {
	t.Setenv("MESIJ_HOME", t.TempDir())
	t.Setenv("MESIJ_DB", "")
	t.Setenv("MESIJ_PROJECT", "")
	dir := t.TempDir()
	output := runRunner(t, dir, "", "--json", "status")
	var status map[string]any
	decodeRunnerJSON(t, output, &status)
	if status["id"] == "" || status["database"] == "" {
		t.Fatalf("status = %+v", status)
	}

	var stdout, stderr bytes.Buffer
	runner := Runner{Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr, Dir: dir}
	if code := runner.Run(context.Background(), []string{"--help"}); code != 0 {
		t.Fatalf("help exited %d: %s", code, stderr.String())
	}
}

func runRunner(t *testing.T, dir, input string, args ...string) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	runner := Runner{Stdin: strings.NewReader(input), Stdout: &stdout, Stderr: &stderr, Dir: dir}
	if code := runner.Run(context.Background(), args); code != 0 {
		t.Fatalf("mesij %v exited %d\nstdout: %s\nstderr: %s", args, code, stdout.String(), stderr.String())
	}
	return stdout.String()
}

func decodeRunnerJSON(t *testing.T, value string, target any) {
	t.Helper()
	if err := json.Unmarshal([]byte(value), target); err != nil {
		t.Fatalf("decode %q: %v", value, err)
	}
}
