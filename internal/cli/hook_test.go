package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"mesij/internal/project"
	"mesij/internal/store"
)

func hookTestProject(t *testing.T) project.Context {
	t.Helper()
	return project.Context{
		Name: "test", ID: "hook-project", Root: "/repo", Invocation: "/repo",
		Worktree: "/repo", Database: filepath.Join(t.TempDir(), "events.sqlite3"), Branch: "main",
	}
}

func runHook(t *testing.T, p project.Context, input string, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	runner := Runner{Stdin: strings.NewReader(input), Stdout: &stdout, Stderr: &stderr}
	code := runner.hook(context.Background(), p, args)
	return code, stdout.String(), stderr.String()
}

func TestReadHookInputAcceptsSnakeAndCopilotCamelCase(t *testing.T) {
	for _, test := range []struct {
		name  string
		input string
		event string
		tool  string
		path  string
	}{
		{name: "snake", input: `{"session_id":"s","hook_event_name":"PreToolUse","tool_name":"Edit","tool_args":{"file_path":"snake.go"}}`, event: "PreToolUse", tool: "Edit", path: "snake.go"},
		{name: "camel", input: `{"sessionId":"s","hookEventName":"preToolUse","toolName":"create","toolArgs":"{\"filePath\":\"camel.go\"}"}`, event: "preToolUse", tool: "create", path: "camel.go"},
	} {
		t.Run(test.name, func(t *testing.T) {
			input, err := readHookInput(strings.NewReader(test.input))
			if err != nil {
				t.Fatal(err)
			}
			if input.SessionID != "s" || input.HookEventName != test.event || input.ToolName != test.tool {
				t.Fatalf("input=%+v", input)
			}
			if files := hookFiles(input); len(files) != 1 || files[0] != test.path {
				t.Fatalf("files=%v", files)
			}
		})
	}
}

func TestHookSessionAndIncrementalInbox(t *testing.T) {
	p := hookTestProject(t)
	state := t.TempDir()
	t.Setenv("PLUGIN_DATA", state)
	envFile := filepath.Join(t.TempDir(), "claude-env")
	t.Setenv("CLAUDE_ENV_FILE", envFile)

	code, output, stderr := runHook(t, p, `{"session_id":"session-a","hook_event_name":"SessionStart"}`, "session-start", "--actor", "claude-code")
	if code != 0 || stderr != "" {
		t.Fatalf("session hook code=%d stderr=%s", code, stderr)
	}
	var start hookOutput
	if err := json.Unmarshal([]byte(output), &start); err != nil {
		t.Fatal(err)
	}
	if start.HookSpecificOutput == nil || !strings.Contains(start.HookSpecificOutput.AdditionalContext, "session-a") {
		t.Fatalf("session output = %+v", start)
	}
	env, err := os.ReadFile(envFile)
	if err != nil || !strings.Contains(string(env), "MESIJ_SESSION='session-a'") {
		t.Fatalf("environment = %q err=%v", env, err)
	}

	db, err := store.Open(context.Background(), p.Database)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(messagePayload{Message: "Please review the API"})
	_, _, err = db.Append(context.Background(), store.NewEvent{
		ProjectID: p.ID, Actor: "codex", Session: "session-b", Recipient: "session-a",
		Type: "message.posted", Payload: payload, Worktree: p.Worktree, IdempotencyKey: "message-1",
	})
	db.Close()
	if err != nil {
		t.Fatal(err)
	}

	input := `{"session_id":"session-a","hook_event_name":"UserPromptSubmit"}`
	code, output, stderr = runHook(t, p, input, "inbox")
	if code != 0 || stderr != "" || !strings.Contains(output, "Please review the API") {
		t.Fatalf("inbox code=%d stdout=%s stderr=%s", code, output, stderr)
	}
	code, output, stderr = runHook(t, p, input, "inbox")
	if code != 0 || output != "" || stderr != "" {
		t.Fatalf("second inbox code=%d stdout=%s stderr=%s", code, output, stderr)
	}
}

func TestHookPreEditReportsExternalOverlap(t *testing.T) {
	p := hookTestProject(t)
	db, err := store.Open(context.Background(), p.Database)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range []store.NewEvent{
		{ProjectID: p.ID, Actor: "self", Session: "session-a", Type: "work.implementing", Payload: json.RawMessage(`{"work":"self","files":["internal/api.go"]}`), Worktree: p.Worktree, IdempotencyKey: "self"},
		{ProjectID: p.ID, Actor: "other", Session: "session-b", Type: "work.implementing", Payload: json.RawMessage(`{"work":"other","files":["internal/api.go"]}`), Worktree: p.Worktree, IdempotencyKey: "other"},
	} {
		if _, _, err := db.Append(context.Background(), event); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	input := `{"session_id":"session-a","hook_event_name":"PreToolUse","tool_name":"Edit","tool_input":{"file_path":"internal/api.go"}}`
	code, output, stderr := runHook(t, p, input, "pre-edit", "--mode", "deny")
	if code != 0 || stderr != "" {
		t.Fatalf("pre-edit code=%d stderr=%s", code, stderr)
	}
	var response hookOutput
	if err := json.Unmarshal([]byte(output), &response); err != nil {
		t.Fatal(err)
	}
	if response.HookSpecificOutput == nil || response.HookSpecificOutput.PermissionDecision != "deny" {
		t.Fatalf("response = %+v", response)
	}
	text := response.HookSpecificOutput.PermissionDecisionReason
	if !strings.Contains(text, "other") || strings.Contains(text, "work=self") {
		t.Fatalf("conflict text = %q", text)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func TestHookDoesNotAdvanceInboxCursorWhenDeliveryFails(t *testing.T) {
	p := hookTestProject(t)
	t.Setenv("PLUGIN_DATA", t.TempDir())
	_, _, _ = runHook(t, p, `{"session_id":"session-a"}`, "session-start", "--actor", "claude-code")

	db, err := store.Open(context.Background(), p.Database)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(messagePayload{Message: "Do not lose this"})
	_, _, err = db.Append(context.Background(), store.NewEvent{
		ProjectID: p.ID, Actor: "other", Session: "session-b", Recipient: "session-a",
		Type: "message.posted", Payload: payload, Worktree: p.Worktree, IdempotencyKey: "message",
	})
	db.Close()
	if err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	runner := Runner{Stdin: strings.NewReader(`{"session_id":"session-a","hook_event_name":"UserPromptSubmit"}`), Stdout: failingWriter{}, Stderr: &stderr}
	if code := runner.hook(context.Background(), p, []string{"inbox"}); code != 0 || !strings.Contains(stderr.String(), "write failed") {
		t.Fatalf("failed delivery code=%d stderr=%s", code, stderr.String())
	}
	code, output, stderrText := runHook(t, p, `{"session_id":"session-a","hook_event_name":"UserPromptSubmit"}`, "inbox")
	if code != 0 || stderrText != "" || !strings.Contains(output, "Do not lose this") {
		t.Fatalf("retry code=%d stdout=%s stderr=%s", code, output, stderrText)
	}
}
func TestHookInboxIsBoundedAndLeavesRemainderUnread(t *testing.T) {
	p := hookTestProject(t)
	t.Setenv("PLUGIN_DATA", t.TempDir())
	_, _, _ = runHook(t, p, `{"session_id":"session-a"}`, "session-start", "--actor", "claude-code")
	db, err := store.Open(context.Background(), p.Database)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 105; i++ {
		payload, _ := json.Marshal(messagePayload{Message: fmt.Sprintf("bulk-%03d", i)})
		_, _, err := db.Append(context.Background(), store.NewEvent{
			ProjectID: p.ID, Actor: "other", Session: "session-b", Recipient: "session-a",
			Type: "message.posted", Payload: payload, Worktree: p.Worktree,
			IdempotencyKey: fmt.Sprintf("bulk-%03d", i),
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	input := `{"session_id":"session-a","hook_event_name":"UserPromptSubmit"}`
	_, first, _ := runHook(t, p, input, "inbox")
	_, second, _ := runHook(t, p, input, "inbox")
	if got := strings.Count(first, "bulk-"); got != 100 {
		t.Fatalf("first batch contains %d messages", got)
	}
	if got := strings.Count(second, "bulk-"); got != 5 {
		t.Fatalf("second batch contains %d messages", got)
	}
}
func TestTruncateUTF8BoundsOversizedHookMessages(t *testing.T) {
	value := strings.Repeat("é", 5000)
	got := truncateUTF8(value, 4096)
	if len(got) > 4096 || !utf8.ValidString(got) || !strings.HasSuffix(got, "…") {
		t.Fatalf("truncated value bytes=%d valid=%v", len(got), utf8.ValidString(got))
	}
}
func TestHookCopilotFormatUsesTopLevelFields(t *testing.T) {
	p := hookTestProject(t)
	t.Setenv("PLUGIN_DATA", t.TempDir())
	code, output, stderr := runHook(t, p, `{"sessionId":"copilot-session","hookEventName":"sessionStart"}`, "session-start", "--actor", "github-copilot", "--format", "copilot")
	if code != 0 || stderr != "" {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	var response hookOutput
	if err := json.Unmarshal([]byte(output), &response); err != nil {
		t.Fatal(err)
	}
	if response.AdditionalContext == "" || response.HookSpecificOutput != nil {
		t.Fatalf("response=%+v", response)
	}
}

func TestHookCopilotCamelCaseToolArgsReportsOverlap(t *testing.T) {
	p := hookTestProject(t)
	db, err := store.Open(context.Background(), p.Database)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = db.Append(context.Background(), store.NewEvent{
		ProjectID: p.ID, Actor: "other", Session: "other-session", Type: "work.implementing",
		Payload:  json.RawMessage(`{"work":"other","files":["internal/copilot.go"]}`),
		Worktree: p.Worktree, IdempotencyKey: "other-copilot",
	})
	db.Close()
	if err != nil {
		t.Fatal(err)
	}

	input := `{"sessionId":"copilot-session","hookEventName":"preToolUse","toolName":"create","toolArgs":"{\"path\":\"internal/copilot.go\"}"}`
	code, output, stderr := runHook(t, p, input, "pre-edit", "--mode", "deny", "--format", "copilot")
	if code != 0 || stderr != "" {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	var response hookOutput
	if err := json.Unmarshal([]byte(output), &response); err != nil {
		t.Fatal(err)
	}
	if response.PermissionDecision != "deny" || !strings.Contains(response.PermissionDecisionReason, "other") {
		t.Fatalf("response=%+v", response)
	}
}

func TestHookInboxBlocksStopAndAgentStop(t *testing.T) {
	for _, eventName := range []string{"Stop", "agentStop"} {
		t.Run(eventName, func(t *testing.T) {
			p := hookTestProject(t)
			t.Setenv("PLUGIN_DATA", t.TempDir())
			_, _, _ = runHook(t, p, `{"sessionId":"copilot-session"}`, "session-start", "--actor", "github-copilot", "--format", "copilot")
			db, err := store.Open(context.Background(), p.Database)
			if err != nil {
				t.Fatal(err)
			}
			payload, _ := json.Marshal(messagePayload{Message: "review before stopping"})
			_, _, err = db.Append(context.Background(), store.NewEvent{
				ProjectID: p.ID, Actor: "other", Session: "other-session", Recipient: "copilot-session",
				Type: "message.posted", Payload: payload, Worktree: p.Worktree, IdempotencyKey: "stop-message",
			})
			db.Close()
			if err != nil {
				t.Fatal(err)
			}

			code, output, stderr := runHook(t, p, fmt.Sprintf(`{"sessionId":"copilot-session","hookEventName":%q}`, eventName), "inbox", "--format", "copilot")
			if code != 0 || stderr != "" {
				t.Fatalf("code=%d stderr=%s", code, stderr)
			}
			var response hookOutput
			if err := json.Unmarshal([]byte(output), &response); err != nil {
				t.Fatal(err)
			}
			if response.Decision != "block" || !strings.Contains(response.Reason, "review before stopping") {
				t.Fatalf("response=%+v", response)
			}
		})
	}
}
