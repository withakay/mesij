package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mesij/internal/project"
	"mesij/internal/store"
)

const usage = `mesij is an append-only message log for agents working in Git worktrees.

Usage:
  mesij [--db PATH] [--project NAME] init
  mesij [--db PATH] session --actor NAME
  mesij [--db PATH] post --actor NAME --session ID --type TYPE [options] [MESSAGE]
  mesij [--db PATH] reply --actor NAME --session ID --to SESSION [options] [MESSAGE]
  mesij [--db PATH] start --actor NAME --session ID --task ID --file PATH [options]
  mesij [--db PATH] finish --actor NAME --session ID --task ID [--message TEXT]
  mesij [--db PATH] defer --actor NAME --session ID --task ID [--message TEXT]
  mesij [--db PATH] check [--after SEQUENCE] [--file PATH] [options]
  mesij [--db PATH] tui
  mesij [--db PATH] status [--json]

Examples:
  mesij start --actor agent-1 --task task-42 --file internal/api.go \
    --key task-42-start --message "Editing the API handler"
  mesij check --file internal/api.go
  mesij finish --actor agent-1 --task task-42 --message "Merged API changes"
  mesij check --after 12 --json

A stable --key makes retries idempotent. If omitted, mesij generates a key.
By default the database is under Git's common directory and is shared by all
linked worktrees. The project name defaults to the repository directory name.
Override these with --db/MESIJ_DB and --project/MESIJ_PROJECT.
`

type Runner struct {
	Stdout io.Writer
	Stderr io.Writer
	Dir    string
}

func (r Runner) Run(ctx context.Context, args []string) int {
	global := flag.NewFlagSet("mesij", flag.ContinueOnError)
	global.SetOutput(r.Stderr)
	dbPath := global.String("db", "", "SQLite database path")
	projectName := global.String("project", "", "project name (or MESIJ_PROJECT)")
	global.Usage = func() { fmt.Fprint(r.Stderr, usage) }
	if err := global.Parse(args); err != nil {
		return 2
	}
	remaining := global.Args()
	if len(remaining) == 0 {
		global.Usage()
		return 2
	}
	command := remaining[0]
	commandArgs := remaining[1:]
	if command == "help" || command == "--help" || command == "-h" {
		fmt.Fprint(r.Stdout, usage)
		return 0
	}

	p, err := project.Discover(r.Dir, *dbPath, *projectName)
	if err != nil {
		fmt.Fprintf(r.Stderr, "mesij: %v\n", err)
		return 1
	}

	switch command {
	case "init":
		return r.init(ctx, p, commandArgs)
	case "session":
		return r.session(ctx, p, commandArgs)
	case "post":
		return r.post(ctx, p, commandArgs, "message.posted", false)
	case "reply":
		return r.post(ctx, p, commandArgs, "message.replied", true)
	case "start":
		return r.lifecycle(ctx, p, commandArgs, "work.started")
	case "finish":
		return r.lifecycle(ctx, p, commandArgs, "work.finished")
	case "defer":
		return r.lifecycle(ctx, p, commandArgs, "work.deferred")
	case "check":
		return r.check(ctx, p, commandArgs)
	case "tui":
		return r.tui(ctx, p, commandArgs)
	case "status":
		return r.status(p, commandArgs)
	default:
		fmt.Fprintf(r.Stderr, "mesij: unknown command %q\n\n", command)
		global.Usage()
		return 2
	}
}

func (r Runner) init(ctx context.Context, p project.Context, args []string) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(r.Stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(r.Stderr, "mesij init: unexpected arguments")
		return 2
	}
	db, err := store.Open(ctx, p.Database)
	if err != nil {
		return r.fail(err)
	}
	db.Close()
	fmt.Fprintf(r.Stdout, "initialized %s\n", p.Database)
	return 0
}

type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}

type messagePayload struct {
	Task    string          `json:"task,omitempty"`
	Message string          `json:"message,omitempty"`
	Files   []string        `json:"files,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (r Runner) session(ctx context.Context, p project.Context, args []string) int {
	fs := flag.NewFlagSet("session", flag.ContinueOnError)
	fs.SetOutput(r.Stderr)
	actor := fs.String("actor", os.Getenv("MESIJ_ACTOR"), "actor name (or MESIJ_ACTOR)")
	sessionID := fs.String("id", "", "session ID; generated when omitted")
	jsonOutput := fs.Bool("json", false, "write event as JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *actor == "" || fs.NArg() != 0 {
		fmt.Fprintln(r.Stderr, "mesij session: --actor is required")
		return 2
	}
	if *sessionID == "" {
		var err error
		*sessionID, err = store.NewID()
		if err != nil {
			return r.fail(err)
		}
	}
	payload, _ := json.Marshal(messagePayload{Message: "Agent session opened"})
	db, err := store.Open(ctx, p.Database)
	if err != nil {
		return r.fail(err)
	}
	defer db.Close()
	event, _, err := db.Append(ctx, store.NewEvent{
		ProjectID: p.ID, Actor: *actor, Session: *sessionID, Type: "session.started", Payload: payload,
		Worktree: p.Worktree, Branch: p.Branch, Commit: p.Commit, IdempotencyKey: "session-started",
	})
	if err != nil {
		return r.fail(err)
	}
	if *jsonOutput {
		return r.writeJSON(event)
	}
	fmt.Fprintf(r.Stdout, "export MESIJ_ACTOR=%s\nexport MESIJ_SESSION=%s\nexport MESIJ_PROJECT=%s\nexport MESIJ_DB=%s\n",
		shellQuote(*actor), shellQuote(*sessionID), shellQuote(p.Name), shellQuote(p.Database))
	return 0
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func (r Runner) post(ctx context.Context, p project.Context, args []string, defaultType string, requireRecipient bool) int {
	fs := flag.NewFlagSet("post", flag.ContinueOnError)
	fs.SetOutput(r.Stderr)
	actor := fs.String("actor", os.Getenv("MESIJ_ACTOR"), "actor name (or MESIJ_ACTOR)")
	session := fs.String("session", os.Getenv("MESIJ_SESSION"), "agent session ID (or MESIJ_SESSION)")
	typeName := fs.String("type", defaultType, "event type")
	to := fs.String("to", "", "recipient session ID")
	replyTo := fs.String("reply-to", "", "event ID being answered")
	task := fs.String("task", "", "related task or work identifier")
	message := fs.String("message", "", "human-readable message")
	key := fs.String("key", "", "stable idempotency key")
	data := fs.String("data", "", "additional JSON value")
	jsonOutput := fs.Bool("json", false, "write event as JSON")
	var files stringList
	fs.Var(&files, "file", "related file (repeatable)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *actor == "" || *session == "" {
		fmt.Fprintln(r.Stderr, "mesij post: --actor/MESIJ_ACTOR and --session/MESIJ_SESSION are required")
		return 2
	}
	if requireRecipient && *to == "" {
		fmt.Fprintln(r.Stderr, "mesij reply: --to recipient session is required")
		return 2
	}
	if *typeName == "work.started" || *typeName == "work.finished" || *typeName == "work.deferred" {
		fmt.Fprintln(r.Stderr, "mesij post: lifecycle event types are reserved; use start, finish, or defer")
		return 2
	}
	if *typeName == "" {
		fmt.Fprintln(r.Stderr, "mesij post: --type cannot be empty")
		return 2
	}
	if fs.NArg() > 0 {
		if *message != "" {
			fmt.Fprintln(r.Stderr, "mesij post: use either --message or a positional message, not both")
			return 2
		}
		*message = strings.Join(fs.Args(), " ")
	}

	payload := messagePayload{Task: *task, Message: *message, Files: normalizeFiles(p.Root, p.Invocation, files)}
	if *data != "" {
		if !json.Valid([]byte(*data)) {
			fmt.Fprintln(r.Stderr, "mesij post: --data must be valid JSON")
			return 2
		}
		payload.Data = json.RawMessage(*data)
	}
	payloadJSON, _ := json.Marshal(payload)

	db, err := store.Open(ctx, p.Database)
	if err != nil {
		return r.fail(err)
	}
	defer db.Close()
	event, inserted, err := db.Append(ctx, store.NewEvent{
		ProjectID: p.ID, Actor: *actor, Session: *session, Recipient: *to, ReplyTo: *replyTo,
		Type: *typeName, Payload: payloadJSON,
		Worktree: p.Worktree, Branch: p.Branch, Commit: p.Commit, IdempotencyKey: *key,
	})
	if err != nil {
		return r.fail(err)
	}
	if *jsonOutput {
		out := struct {
			store.Event
			Inserted bool `json:"inserted"`
		}{event, inserted}
		return r.writeJSON(out)
	}
	state := "posted"
	if !inserted {
		state = "already posted"
	}
	fmt.Fprintf(r.Stdout, "%s event %d (%s), key=%s\n", state, event.Sequence, event.Type, event.IdempotencyKey)
	return 0
}

func (r Runner) lifecycle(ctx context.Context, p project.Context, args []string, eventType string) int {
	name := strings.TrimPrefix(eventType, "work.")
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(r.Stderr)
	actor := fs.String("actor", os.Getenv("MESIJ_ACTOR"), "actor name (or MESIJ_ACTOR)")
	session := fs.String("session", os.Getenv("MESIJ_SESSION"), "agent session ID (or MESIJ_SESSION)")
	task := fs.String("task", "", "stable task or work identifier")
	message := fs.String("message", "", "human-readable message")
	key := fs.String("key", "", "stable idempotency key")
	jsonOutput := fs.Bool("json", false, "write event as JSON")
	var files stringList
	fs.Var(&files, "file", "affected file or directory (repeatable)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *actor == "" || *session == "" || *task == "" {
		fmt.Fprintf(r.Stderr, "mesij %s: --actor, --session, and --task are required\n", name)
		return 2
	}
	if fs.NArg() > 0 {
		if *message != "" {
			fmt.Fprintf(r.Stderr, "mesij %s: use either --message or a positional message, not both\n", name)
			return 2
		}
		*message = strings.Join(fs.Args(), " ")
	}
	if eventType == "work.started" && len(files) == 0 {
		fmt.Fprintln(r.Stderr, "mesij start: at least one --file is required to make conflicts visible")
		return 2
	}
	payloadJSON, _ := json.Marshal(messagePayload{Task: *task, Message: *message, Files: normalizeFiles(p.Root, p.Invocation, files)})
	db, err := store.Open(ctx, p.Database)
	if err != nil {
		return r.fail(err)
	}
	defer db.Close()
	event, inserted, err := db.Append(ctx, store.NewEvent{
		ProjectID: p.ID, Actor: *actor, Session: *session, Type: eventType, Payload: payloadJSON,
		Worktree: p.Worktree, Branch: p.Branch, Commit: p.Commit, IdempotencyKey: *key,
	})
	if err != nil {
		return r.fail(err)
	}
	if *jsonOutput {
		return r.writeJSON(struct {
			store.Event
			Inserted bool `json:"inserted"`
		}{event, inserted})
	}
	state := "posted"
	if !inserted {
		state = "already posted"
	}
	fmt.Fprintf(r.Stdout, "%s %s for task %s as event %d, key=%s\n", state, eventType, *task, event.Sequence, event.IdempotencyKey)
	return 0
}

func normalizeFiles(root, invocation string, files []string) []string {
	out := make([]string, 0, len(files))
	for _, name := range files {
		clean := filepath.Clean(name)
		if !filepath.IsAbs(clean) {
			clean = filepath.Join(invocation, clean)
		}
		if filepath.IsAbs(clean) {
			if rel, err := filepath.Rel(root, clean); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				clean = rel
			}
		}
		out = append(out, filepath.ToSlash(clean))
	}
	return out
}

func (r Runner) check(ctx context.Context, p project.Context, args []string) int {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(r.Stderr)
	after := fs.Int64("after", 0, "only messages after this sequence")
	limit := fs.Int("limit", 100, "maximum messages (up to 1000)")
	actor := fs.String("from", "", "filter messages by actor")
	typeName := fs.String("type", "", "filter messages by event type")
	forSession := fs.String("session", os.Getenv("MESIJ_SESSION"), "include broadcasts and direct messages for this session")
	jsonOutput := fs.Bool("json", false, "write one JSON coordination report")
	var files stringList
	fs.Var(&files, "file", "proposed file or directory; report overlapping active work (repeatable)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	afterSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "after" {
			afterSet = true
		}
	})
	if fs.NArg() != 0 || *after < 0 || *limit < 1 {
		fmt.Fprintln(r.Stderr, "mesij check: invalid arguments")
		return 2
	}
	db, err := store.Open(ctx, p.Database)
	if err != nil {
		return r.fail(err)
	}
	defer db.Close()
	active, err := db.Active(ctx, p.ID)
	if err != nil {
		return r.fail(err)
	}
	requested := normalizeFiles(p.Root, p.Invocation, files)
	relevant := filterActive(active, requested)
	events, err := db.List(ctx, store.Query{ProjectID: p.ID, After: *after, Limit: *limit, Actor: *actor, Type: *typeName, ForSession: *forSession, Latest: !afterSet})
	if err != nil {
		return r.fail(err)
	}
	if *jsonOutput {
		return r.writeJSON(struct {
			Proposed []string      `json:"proposed_files,omitempty"`
			Active   []store.Event `json:"active_work"`
			Messages []store.Event `json:"messages"`
		}{requested, relevant, events})
	}

	if len(requested) > 0 {
		fmt.Fprintf(r.Stdout, "Potential conflicts for %s:\n", strings.Join(requested, ", "))
	} else {
		fmt.Fprintln(r.Stdout, "Active work:")
	}
	if len(relevant) == 0 {
		fmt.Fprintln(r.Stdout, "  none")
	}
	for _, event := range relevant {
		var payload messagePayload
		_ = json.Unmarshal(event.Payload, &payload)
		fmt.Fprintf(r.Stdout, "  ! %s (%s) / %s on %s", event.Actor, event.Session, payload.Task, strings.Join(payload.Files, ", "))
		if payload.Message != "" {
			fmt.Fprintf(r.Stdout, " — %s", payload.Message)
		}
		fmt.Fprintln(r.Stdout)
	}

	fmt.Fprintln(r.Stdout, "\nMessages:")
	if len(events) == 0 {
		fmt.Fprintln(r.Stdout, "  none")
	}
	for _, event := range events {
		writeEvent(r.Stdout, event)
	}
	return 0
}

func filterActive(events []store.Event, proposed []string) []store.Event {
	if len(proposed) == 0 {
		return events
	}
	var matches []store.Event
	for _, event := range events {
		var payload messagePayload
		if json.Unmarshal(event.Payload, &payload) != nil {
			continue
		}
		matched := false
		for _, a := range payload.Files {
			for _, b := range proposed {
				if pathsOverlap(a, b) {
					matched = true
					break
				}
			}
		}
		if matched {
			matches = append(matches, event)
		}
	}
	return matches
}

func pathsOverlap(a, b string) bool {
	a = strings.TrimSuffix(filepath.ToSlash(filepath.Clean(a)), "/")
	b = strings.TrimSuffix(filepath.ToSlash(filepath.Clean(b)), "/")
	if a == "." || b == "." || a == b {
		return true
	}
	return strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

func writeEvent(w io.Writer, event store.Event) {
	var payload messagePayload
	_ = json.Unmarshal(event.Payload, &payload)
	branch := event.Branch
	if branch == "" {
		branch = "detached"
	}
	fmt.Fprintf(w, "%d  %s  %s (%s)  %s  [%s]", event.Sequence, event.CreatedAt.Local().Format(time.RFC3339), event.Actor, event.Session, event.Type, branch)
	if event.Recipient != "" {
		fmt.Fprintf(w, "  → %s", event.Recipient)
	}
	if event.ReplyTo != "" {
		fmt.Fprintf(w, "  reply:%s", event.ReplyTo)
	}
	fmt.Fprintln(w)
	if payload.Task != "" {
		fmt.Fprintf(w, "    task: %s\n", payload.Task)
	}
	if payload.Message != "" {
		fmt.Fprintf(w, "    %s\n", payload.Message)
	}
	if len(payload.Files) > 0 {
		fmt.Fprintf(w, "    files: %s\n", strings.Join(payload.Files, ", "))
	}
	if len(payload.Data) > 0 {
		fmt.Fprintf(w, "    data: %s\n", payload.Data)
	}
}

func (r Runner) status(p project.Context, args []string) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(r.Stderr)
	jsonOutput := fs.Bool("json", false, "write JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(r.Stderr, "mesij status: unexpected arguments")
		return 2
	}
	if *jsonOutput {
		return r.writeJSON(p)
	}
	fmt.Fprintf(r.Stdout, "project:  %s (%s)\nworktree: %s\nbranch:   %s\ncommit:   %s\ndatabase: %s\n", p.Name, p.ID, p.Worktree, p.Branch, p.Commit, p.Database)
	return 0
}

func (r Runner) writeJSON(v any) int {
	encoder := json.NewEncoder(r.Stdout)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(v); err != nil {
		return r.fail(err)
	}
	return 0
}

func (r Runner) fail(err error) int {
	if errors.Is(err, store.ErrIdempotencyConflict) {
		fmt.Fprintf(r.Stderr, "mesij: %v; choose a new --key or retry with identical data\n", err)
	} else {
		fmt.Fprintf(r.Stderr, "mesij: %v\n", err)
	}
	return 1
}
