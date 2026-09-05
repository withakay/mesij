package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"mesij/internal/project"
	"mesij/internal/store"
)

type hookInput struct {
	SessionID     string
	HookEventName string
	ToolName      string
	ToolInput     map[string]any
}

func (input *hookInput) UnmarshalJSON(data []byte) error {
	var raw struct {
		SessionID          string          `json:"session_id"`
		SessionIDCamel     string          `json:"sessionId"`
		HookEventName      string          `json:"hook_event_name"`
		HookEventNameCamel string          `json:"hookEventName"`
		ToolName           string          `json:"tool_name"`
		ToolNameCamel      string          `json:"toolName"`
		ToolInput          json.RawMessage `json:"tool_input"`
		ToolArgs           json.RawMessage `json:"tool_args"`
		ToolArgsCamel      json.RawMessage `json:"toolArgs"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	input.SessionID = firstNonEmpty(raw.SessionID, raw.SessionIDCamel)
	input.HookEventName = firstNonEmpty(raw.HookEventName, raw.HookEventNameCamel)
	input.ToolName = firstNonEmpty(raw.ToolName, raw.ToolNameCamel)
	arguments, err := decodeHookArguments(raw.ToolInput, raw.ToolArgs, raw.ToolArgsCamel)
	if err != nil {
		return err
	}
	input.ToolInput = arguments
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func decodeHookArguments(values ...json.RawMessage) (map[string]any, error) {
	var merged map[string]any
	for _, raw := range values {
		if len(raw) == 0 || string(raw) == "null" {
			continue
		}
		var value any
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, fmt.Errorf("decode hook tool arguments: %w", err)
		}
		if encoded, ok := value.(string); ok {
			if err := json.Unmarshal([]byte(encoded), &value); err != nil {
				return nil, fmt.Errorf("decode hook tool arguments string: %w", err)
			}
		}
		object, ok := value.(map[string]any)
		if !ok {
			continue
		}
		if merged == nil {
			merged = make(map[string]any, len(object))
		}
		for key, argument := range object {
			merged[key] = argument
		}
	}
	return merged, nil
}

type hookOutput struct {
	Decision                 string              `json:"decision,omitempty"`
	Reason                   string              `json:"reason,omitempty"`
	AdditionalContext        string              `json:"additionalContext,omitempty"`
	PermissionDecision       string              `json:"permissionDecision,omitempty"`
	PermissionDecisionReason string              `json:"permissionDecisionReason,omitempty"`
	HookSpecificOutput       *hookSpecificOutput `json:"hookSpecificOutput,omitempty"`
}

type hookSpecificOutput struct {
	HookEventName            string `json:"hookEventName"`
	AdditionalContext        string `json:"additionalContext,omitempty"`
	PermissionDecision       string `json:"permissionDecision,omitempty"`
	PermissionDecisionReason string `json:"permissionDecisionReason,omitempty"`
}

func (r Runner) hook(ctx context.Context, p project.Context, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(r.Stderr, "mesij hook: expected session-start, inbox, or pre-edit")
		return 2
	}
	command := args[0]
	fs := flag.NewFlagSet("hook "+command, flag.ContinueOnError)
	fs.SetOutput(r.Stderr)
	actor := fs.String("actor", "", "stable actor name")
	mode := fs.String("mode", os.Getenv("MESIJ_HOOK_MODE"), "pre-edit mode: advisory or deny")
	format := fs.String("format", "vscode", "hook output format: vscode or copilot")
	if *mode == "" {
		*mode = "advisory"
	}
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(r.Stderr, "mesij hook: unexpected arguments")
		return 2
	}
	if *format != "vscode" && *format != "copilot" {
		fmt.Fprintln(r.Stderr, "mesij hook: --format must be vscode or copilot")
		return 2
	}
	if command == "pre-edit" && *mode != "advisory" && *mode != "deny" {
		fmt.Fprintln(r.Stderr, "mesij hook pre-edit: --mode must be advisory or deny")
		return 2
	}

	input, err := readHookInput(r.Stdin)
	if err != nil {
		fmt.Fprintf(r.Stderr, "mesij hook warning: %v\n", err)
		return 0
	}
	session := input.SessionID
	if session == "" {
		session = os.Getenv("MESIJ_SESSION")
	}
	if session == "" {
		fmt.Fprintln(r.Stderr, "mesij hook warning: hook input has no session_id")
		return 0
	}

	switch command {
	case "session-start":
		if *actor == "" {
			*actor = os.Getenv("MESIJ_ACTOR")
		}
		if *actor == "" {
			fmt.Fprintln(r.Stderr, "mesij hook session-start: --actor is required")
			return 2
		}
		return r.hookSessionStart(ctx, p, input, *actor, session, *format)
	case "inbox":
		return r.hookInbox(ctx, p, input, session, *format)
	case "pre-edit":
		return r.hookPreEdit(ctx, p, input, session, *mode, *format)
	default:
		fmt.Fprintf(r.Stderr, "mesij hook: unknown hook command %q\n", command)
		return 2
	}
}

func readHookInput(reader io.Reader) (hookInput, error) {
	if reader == nil {
		reader = os.Stdin
	}
	limited := io.LimitReader(reader, 4<<20)
	decoder := json.NewDecoder(limited)
	var input hookInput
	if err := decoder.Decode(&input); err != nil {
		return hookInput{}, fmt.Errorf("decode hook JSON: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return hookInput{}, errors.New("hook input must contain exactly one JSON object")
		}
		return hookInput{}, fmt.Errorf("decode trailing hook JSON: %w", err)
	}
	return input, nil
}

func (r Runner) hookSessionStart(ctx context.Context, p project.Context, input hookInput, actor, session, format string) int {
	db, err := store.Open(ctx, p.Database)
	if err != nil {
		return r.hookWarning(err)
	}
	defer db.Close()
	payload, _ := json.Marshal(messagePayload{Message: "Agent session opened"})
	_, _, err = db.Append(ctx, store.NewEvent{
		ProjectID: p.ID, Actor: actor, Session: session, Type: "session.started", Payload: payload,
		Worktree: p.Worktree, Branch: p.Branch, Commit: p.Commit, Host: p.Source.Host, User: p.Source.User, IP: p.Source.IP, IdempotencyKey: "session-started",
	})
	if err != nil {
		return r.hookWarning(err)
	}
	if err := appendHookEnvironment(actor, session, p); err != nil {
		fmt.Fprintf(r.Stderr, "mesij hook warning: persist environment: %v\n", err)
	}
	messages, cursor, cursorPath, err := readHookInbox(ctx, db, p, session)
	if err != nil {
		return r.hookWarning(err)
	}
	text := fmt.Sprintf("Mesij session registered as %s (%s). Reuse this session and do not create another.", actor, session)
	if formatted := formatHookMessages(messages); formatted != "" {
		text += "\n\nPending Mesij messages:\n" + formatted
	}
	output := contextHookOutput(format, hookEvent(input, "SessionStart"), text)
	if err := r.writeHookOutput(output); err != nil {
		return r.hookWarning(err)
	}
	if err := writeCursor(cursorPath, cursor); err != nil {
		return r.hookWarning(err)
	}
	return 0
}

func (r Runner) hookInbox(ctx context.Context, p project.Context, input hookInput, session, format string) int {
	db, err := store.Open(ctx, p.Database)
	if err != nil {
		return r.hookWarning(err)
	}
	defer db.Close()
	messages, cursor, cursorPath, err := readHookInbox(ctx, db, p, session)
	if err != nil {
		return r.hookWarning(err)
	}
	formatted := formatHookMessages(messages)
	if formatted == "" {
		if err := writeCursor(cursorPath, cursor); err != nil {
			return r.hookWarning(err)
		}
		return 0
	}
	eventName := hookEvent(input, "UserPromptSubmit")
	var output hookOutput
	if eventName == "Stop" || eventName == "agentStop" || eventName == "AgentStop" {
		output = hookOutput{
			Decision: "block",
			Reason:   "New Mesij messages arrived. Review and reply before stopping:\n" + formatted,
		}
	} else {
		output = contextHookOutput(format, eventName, "New Mesij messages:\n"+formatted)
	}
	if err := r.writeHookOutput(output); err != nil {
		return r.hookWarning(err)
	}
	if err := writeCursor(cursorPath, cursor); err != nil {
		return r.hookWarning(err)
	}
	return 0
}

func (r Runner) hookPreEdit(ctx context.Context, p project.Context, input hookInput, session, mode, format string) int {
	files := hookFiles(input)
	if len(files) == 0 {
		return 0
	}
	db, err := store.Open(ctx, p.Database)
	if err != nil {
		return r.hookWarning(err)
	}
	defer db.Close()
	active, err := db.Active(ctx, p.ID)
	if err != nil {
		return r.hookWarning(err)
	}
	proposed := coordinationQuery{Files: normalizeFiles(p.Root, p.Invocation, files)}
	matches := filterActive(active, proposed)
	external := make([]store.Event, 0, len(matches))
	for _, event := range matches {
		if event.Session != session {
			external = append(external, event)
		}
	}
	if len(external) == 0 {
		return 0
	}
	text := formatHookConflicts(external)
	var response hookOutput
	if format == "copilot" {
		if mode == "deny" {
			response.PermissionDecision = "deny"
			response.PermissionDecisionReason = text
		} else {
			response.AdditionalContext = text
		}
	} else {
		output := &hookSpecificOutput{HookEventName: hookEvent(input, "PreToolUse")}
		if mode == "deny" {
			output.PermissionDecision = "deny"
			output.PermissionDecisionReason = text
		} else {
			output.AdditionalContext = text
		}
		response.HookSpecificOutput = output
	}
	if err := r.writeHookOutput(response); err != nil {
		return r.hookWarning(err)
	}
	return 0
}

func readHookInbox(ctx context.Context, db *store.DB, p project.Context, session string) ([]store.Event, int64, string, error) {
	const (
		pageSize        = 100
		maxScanned      = 1000
		maxMessages     = 100
		maxContextBytes = 32 << 10
	)
	cursorPath, err := hookCursorPath(p.ID, session)
	if err != nil {
		return nil, 0, "", err
	}
	after, err := readCursor(cursorPath)
	if err != nil {
		return nil, 0, "", err
	}
	through, err := db.LatestSequence(ctx, p.ID)
	if err != nil {
		return nil, 0, "", err
	}
	all := make([]store.Event, 0, maxMessages)
	cursor := after
	scanned := 0
	contextBytes := 0
	exhausted := false
	for scanned < maxScanned {
		events, err := db.Inbox(ctx, p.ID, session, cursor, pageSize)
		if err != nil {
			return nil, 0, "", err
		}
		if len(events) == 0 {
			exhausted = true
			break
		}
		stopped := false
		for _, event := range events {
			if event.Session != session {
				lineBytes := len(hookMessageLine(event)) + 1
				if len(all) >= maxMessages || contextBytes+lineBytes > maxContextBytes {
					stopped = true
					break
				}
				all = append(all, event)
				contextBytes += lineBytes
			}
			if event.Sequence > cursor {
				cursor = event.Sequence
			}
			scanned++
			if scanned >= maxScanned {
				stopped = true
				break
			}
		}
		if stopped {
			break
		}
		if len(events) < pageSize {
			exhausted = true
			break
		}
	}
	if exhausted && through > cursor {
		cursor = through
	}
	return all, cursor, cursorPath, nil
}

func hookCursorPath(projectID, session string) (string, error) {
	base := ""
	for _, name := range []string{"CLAUDE_PLUGIN_DATA", "CODEX_PLUGIN_DATA", "PLUGIN_DATA"} {
		if value := os.Getenv(name); value != "" {
			base = value
			break
		}
	}
	if base == "" {
		state, err := os.UserCacheDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(state, "mesij", "hook-state")
	}
	sum := sha256.Sum256([]byte(projectID + "\x00" + session))
	return filepath.Join(base, "state", hex.EncodeToString(sum[:16])+".cursor"), nil
}

func readCursor(path string) (int64, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	value, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("invalid hook cursor %s", path)
	}
	return value, nil
}

func writeCursor(path string, cursor int64) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temp := path + ".tmp-" + strconv.Itoa(os.Getpid())
	if err := os.WriteFile(temp, []byte(strconv.FormatInt(cursor, 10)+"\n"), 0o600); err != nil {
		return err
	}
	return os.Rename(temp, path)
}

func appendHookEnvironment(actor, session string, p project.Context) error {
	path := os.Getenv("CLAUDE_ENV_FILE")
	if path == "" {
		return nil
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = fmt.Fprintf(file, "export MESIJ_ACTOR=%s\nexport MESIJ_SESSION=%s\nexport MESIJ_PROJECT=%s\nexport MESIJ_DB=%s\n",
		shellQuote(actor), shellQuote(session), shellQuote(p.Name), shellQuote(p.Database))
	return err
}

func hookEvent(input hookInput, fallback string) string {
	if input.HookEventName != "" {
		return input.HookEventName
	}
	return fallback
}

func hookFiles(input hookInput) []string {
	files := make([]string, 0)
	for _, key := range []string{"file_path", "filePath", "path"} {
		if value, ok := input.ToolInput[key].(string); ok && strings.TrimSpace(value) != "" {
			files = append(files, value)
		}
	}
	if command, ok := input.ToolInput["command"].(string); ok {
		re := regexp.MustCompile(`(?m)^\*\*\* (?:Add File|Update File|Delete File|Move to):\s*(.+?)\s*$`)
		for _, match := range re.FindAllStringSubmatch(command, -1) {
			if len(match) == 2 && match[1] != "" {
				files = append(files, match[1])
			}
		}
	}
	return uniqueStrings(files)
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}

func hookMessageLine(event store.Event) string {
	var payload messagePayload
	_ = json.Unmarshal(event.Payload, &payload)
	message := payload.Message
	if message == "" {
		message = string(event.Payload)
	}
	line := fmt.Sprintf("- #%d %s (%s): %s", event.Sequence, event.Actor, event.Session, message)
	return truncateUTF8(line, 4096)
}

func truncateUTF8(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	cut := maxBytes - len("…")
	for cut > 0 && !utf8.ValidString(value[:cut]) {
		cut--
	}
	if cut <= 0 {
		return "…"
	}
	return value[:cut] + "…"
}

func formatHookMessages(events []store.Event) string {
	lines := make([]string, 0, len(events))
	for _, event := range events {
		lines = append(lines, hookMessageLine(event))
	}
	return strings.Join(lines, "\n")
}

func formatHookConflicts(events []store.Event) string {
	lines := []string{"Mesij reports overlapping active work:"}
	for _, event := range events {
		payload := activePayload(event)
		lines = append(lines, fmt.Sprintf("- %s (%s), work=%s, files=%s", event.Actor, event.Session, payload.workID(), strings.Join(payload.Files, ", ")))
	}
	lines = append(lines, "Coordinate or defer before editing if the overlap is material.")
	return strings.Join(lines, "\n")
}

func contextHookOutput(format, eventName, text string) hookOutput {
	if format == "copilot" {
		return hookOutput{AdditionalContext: text}
	}
	return hookOutput{HookSpecificOutput: &hookSpecificOutput{
		HookEventName: eventName, AdditionalContext: text,
	}}
}

func (r Runner) writeHookOutput(output hookOutput) error {
	encoder := json.NewEncoder(r.Stdout)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(output); err != nil {
		return fmt.Errorf("encode hook output: %w", err)
	}
	return nil
}

func (r Runner) hookWarning(err error) int {
	fmt.Fprintf(r.Stderr, "mesij hook warning: %v\n", err)
	return 0
}
