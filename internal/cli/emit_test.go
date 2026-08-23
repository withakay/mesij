package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"mesij/internal/project"
	"mesij/internal/store"
)

func TestEmitAcceptsJSONAndMultipleFiles(t *testing.T) {
	database := filepath.Join(t.TempDir(), "events.sqlite3")
	p := project.Context{
		Name: "test", ID: "project", Root: "/repo", Invocation: "/repo",
		Worktree: "/repo", Database: database, Branch: "main",
	}
	input := `{
		"event":"plan",
		"actor":"agent-a",
		"session":"session-a",
		"task":"task-1",
		"change":"change-1",
		"files":["internal/a.go","internal/b.go"],
		"key":"task-1:plan",
		"message":"Plan both files"
	}`
	var stdout, stderr bytes.Buffer
	runner := Runner{Stdin: bytes.NewBufferString(input), Stdout: &stdout, Stderr: &stderr}
	if code := runner.emit(context.Background(), p, nil); code != 0 {
		t.Fatalf("emit code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	var response struct {
		store.Event
		Inserted bool `json:"inserted"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.Inserted || response.Type != "work.planned" {
		t.Fatalf("response = %+v", response)
	}
	var payload messagePayload
	if err := json.Unmarshal(response.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Files) != 2 || payload.Files[0] != "internal/a.go" || payload.Files[1] != "internal/b.go" {
		t.Fatalf("files = %v", payload.Files)
	}
}

func TestEmitReturnsJSONErrors(t *testing.T) {
	var stdout bytes.Buffer
	runner := Runner{Stdin: bytes.NewBufferString(`{"event":"plan","unknown":true}`), Stdout: &stdout, Stderr: &bytes.Buffer{}}
	code := runner.emit(context.Background(), project.Context{}, nil)
	if code != 2 {
		t.Fatalf("code = %d", code)
	}
	var response struct {
		OK       bool   `json:"ok"`
		Error    string `json:"error"`
		ExitCode int    `json:"exit_code"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.OK || response.ExitCode != 2 || response.Error == "" {
		t.Fatalf("response = %+v", response)
	}
}
func TestEmitRejectsNullAndEmptyFiles(t *testing.T) {
	for _, input := range []string{
		`{"event":"plan","actor":"a","session":"s","task":"t","files":[null]}`,
		`{"event":"plan","actor":"a","session":"s","task":"t","files":[""]}`,
	} {
		var stdout bytes.Buffer
		runner := Runner{Stdin: bytes.NewBufferString(input), Stdout: &stdout, Stderr: &bytes.Buffer{}}
		if code := runner.emit(context.Background(), project.Context{}, nil); code != 2 {
			t.Fatalf("input %s: code=%d", input, code)
		}
		var response map[string]any
		if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if response["ok"] != false {
			t.Fatalf("input %s: response=%v", input, response)
		}
	}
}

func TestEmitRunSupportsNonGitProject(t *testing.T) {
	t.Setenv("MESIJ_HOME", t.TempDir())
	t.Setenv("MESIJ_DB", "")
	t.Setenv("MESIJ_PROJECT", "")
	var stdout, stderr bytes.Buffer
	runner := Runner{
		Stdin:  bytes.NewBufferString(`{"event":"post","actor":"a","session":"s"}`),
		Stdout: &stdout, Stderr: &stderr, Dir: t.TempDir(),
	}
	if code := runner.Run(context.Background(), []string{"emit"}); code != 0 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	var response store.Event
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Type != "message.posted" || response.ProjectID == "" {
		t.Fatalf("response=%+v", response)
	}
}
func TestEmitRejectsNoncanonicalAndDuplicateFields(t *testing.T) {
	for _, input := range []string{
		`{"Event":"post","actor":"a","session":"s"}`,
		`{"event":"post","event":"finish","actor":"a","session":"s"}`,
	} {
		var stdout bytes.Buffer
		runner := Runner{Stdin: bytes.NewBufferString(input), Stdout: &stdout, Stderr: &bytes.Buffer{}}
		if code := runner.emit(context.Background(), project.Context{}, nil); code != 2 {
			t.Fatalf("input %s: code=%d", input, code)
		}
		var response map[string]any
		if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if response["ok"] != false {
			t.Fatalf("input %s: response=%v", input, response)
		}
	}
}

func TestCommandHintSkipsGlobalFlagValues(t *testing.T) {
	for _, test := range []struct {
		args []string
		want string
	}{
		{[]string{"--project", "emit", "status"}, "status"},
		{[]string{"--project=emit", "status"}, "status"},
		{[]string{"--bad", "emit"}, "emit"},
		{[]string{"--db", "/tmp/x", "emit"}, "emit"},
	} {
		if got := commandHint(test.args); got != test.want {
			t.Errorf("commandHint(%v)=%q want %q", test.args, got, test.want)
		}
	}
}
