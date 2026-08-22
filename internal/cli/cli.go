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
  mesij [--db PATH] plan --actor NAME --session ID [targets] [options]
  mesij [--db PATH] implement --actor NAME --session ID [targets] [options]
  mesij [--db PATH] start --actor NAME --session ID [targets] [options]
  mesij [--db PATH] finish --actor NAME --session ID [targets] [--message TEXT]
  mesij [--db PATH] defer --actor NAME --session ID [targets] [--message TEXT]
  mesij [--db PATH] check [--after SEQUENCE] [--task ID] [--change ID] [--file PATH] [options]
  mesij [--db PATH] tui
  mesij [--db PATH] status [--json]

Examples:
  mesij plan --task task-42 --change api-v2 --file internal/api.go \
    --key task-42:plan --message "Planning the API change"
  mesij check --task task-42 --change api-v2 --file internal/api.go
  mesij implement --task task-42 --change api-v2 --file internal/api.go \
    --key task-42:implement --message "Implementing the API handler"
  mesij finish --task task-42 --key task-42:finish --message "Merged API changes"
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
	case "plan":
		return r.lifecycle(ctx, p, commandArgs, "work.planned")
	case "implement":
		return r.lifecycle(ctx, p, commandArgs, "work.implementing")
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
	Work    string          `json:"work,omitempty"`
	Task    string          `json:"task,omitempty"`
	Change  string          `json:"change,omitempty"`
	Phase   string          `json:"phase,omitempty"`
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
	work := fs.String("work", "", "work claim identifier")
	task := fs.String("task", "", "related task identifier")
	change := fs.String("change", "", "related change identifier")
	phase := fs.String("phase", "", "phase such as plan or implement")
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
	if isLifecycleType(*typeName) {
		fmt.Fprintln(r.Stderr, "mesij post: lifecycle event types are reserved; use plan, implement, start, finish, or defer")
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

	payload := messagePayload{Work: *work, Task: *task, Change: *change, Phase: *phase, Message: *message, Files: normalizeFiles(p.Root, p.Invocation, files)}
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
	name := lifecycleCommand(eventType)
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(r.Stderr)
	actor := fs.String("actor", os.Getenv("MESIJ_ACTOR"), "actor name (or MESIJ_ACTOR)")
	session := fs.String("session", os.Getenv("MESIJ_SESSION"), "agent session ID (or MESIJ_SESSION)")
	work := fs.String("work", "", "stable work claim identifier")
	task := fs.String("task", "", "related task identifier")
	change := fs.String("change", "", "related change identifier")
	message := fs.String("message", "", "human-readable message")
	key := fs.String("key", "", "stable idempotency key")
	jsonOutput := fs.Bool("json", false, "write event as JSON")
	var files stringList
	fs.Var(&files, "file", "affected file or directory (repeatable)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *actor == "" || *session == "" {
		fmt.Fprintf(r.Stderr, "mesij %s: --actor and --session are required\n", name)
		return 2
	}
	if fs.NArg() > 0 {
		if *message != "" {
			fmt.Fprintf(r.Stderr, "mesij %s: use either --message or a positional message, not both\n", name)
			return 2
		}
		*message = strings.Join(fs.Args(), " ")
	}
	normalizedFiles := normalizeFiles(p.Root, p.Invocation, files)
	explicitWork := *work != ""
	defaultWork := ""
	switch {
	case *task != "":
		defaultWork = "task:" + *task
	case *change != "":
		defaultWork = "change:" + *change
	}
	if *work == "" && defaultWork == "" {
		fmt.Fprintf(r.Stderr, "mesij %s: provide --work, --task, or --change\n", name)
		return 2
	}
	if isActiveLifecycle(eventType) && *task == "" && *change == "" && len(normalizedFiles) == 0 {
		fmt.Fprintf(r.Stderr, "mesij %s: claim at least one --task, --change, or --file target\n", name)
		return 2
	}

	db, err := store.Open(ctx, p.Database)
	if err != nil {
		return r.fail(err)
	}
	defer db.Close()
	retryFound := false
	var retryPayload messagePayload
	if *key != "" {
		existing, found, err := db.FindByKey(ctx, p.ID, *session, *key)
		if err != nil {
			return r.fail(err)
		}
		if found && json.Unmarshal(existing.Payload, &retryPayload) == nil {
			retryFound = true
		}
	}
	if retryFound && !explicitWork {
		*work = retryPayload.workID()
	} else if !explicitWork {
		resolved, found, err := db.ResolveWork(ctx, p.ID, *session, *task, *change)
		if err != nil {
			if errors.Is(err, store.ErrAmbiguousWork) {
				fmt.Fprintf(r.Stderr, "mesij %s: targets map to multiple active work identities; pass --work explicitly\n", name)
				return 2
			}
			return r.fail(err)
		}
		if found {
			*work = resolved
		} else {
			*work = defaultWork
		}
	}

	payloadWork := ""
	if retryFound && !explicitWork {
		payloadWork = retryPayload.Work
	} else if explicitWork || *work != defaultWork {
		payloadWork = *work
	}
	payloadPhase := lifecyclePhase(eventType)
	if retryFound {
		payloadPhase = retryPayload.Phase
	} else if eventType == "work.started" {
		// Keep the legacy start payload stable for idempotent retries. Its phase
		// is inferred as implement by projections and displays.
		payloadPhase = ""
	}
	payloadJSON, _ := json.Marshal(messagePayload{
		Work: payloadWork, Task: *task, Change: *change, Phase: payloadPhase,
		Message: *message, Files: normalizedFiles,
	})
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
	fmt.Fprintf(r.Stdout, "%s %s for work %s as event %d, key=%s\n", state, eventType, *work, event.Sequence, event.IdempotencyKey)
	return 0
}

func lifecycleCommand(eventType string) string {
	switch eventType {
	case "work.planned":
		return "plan"
	case "work.implementing":
		return "implement"
	case "work.started":
		return "start"
	case "work.finished":
		return "finish"
	case "work.deferred":
		return "defer"
	default:
		return strings.TrimPrefix(eventType, "work.")
	}
}

func lifecyclePhase(eventType string) string {
	switch eventType {
	case "work.planned":
		return "plan"
	case "work.implementing", "work.started":
		return "implement"
	default:
		return ""
	}
}

func isLifecycleType(eventType string) bool {
	switch eventType {
	case "work.planned", "work.implementing", "work.started", "work.finished", "work.deferred":
		return true
	default:
		return false
	}
}

func isActiveLifecycle(eventType string) bool {
	return eventType == "work.planned" || eventType == "work.implementing" || eventType == "work.started"
}

func lifecycleDisplayPhase(event store.Event, payload messagePayload) string {
	if payload.Phase != "" {
		return payload.Phase
	}
	return lifecyclePhase(event.Type)
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
	proposedTask := fs.String("task", "", "proposed task identifier")
	proposedChange := fs.String("change", "", "proposed change identifier")
	proposedPhase := fs.String("phase", "", "filter active work by phase: plan or implement")
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
	if *proposedPhase != "" && *proposedPhase != "plan" && *proposedPhase != "implement" {
		fmt.Fprintln(r.Stderr, "mesij check: --phase must be plan or implement")
		return 2
	}
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
	proposed := coordinationQuery{
		Task: *proposedTask, Change: *proposedChange, Phase: *proposedPhase,
		Files: normalizeFiles(p.Root, p.Invocation, files),
	}
	relevant := filterActive(active, proposed)
	events, err := db.List(ctx, store.Query{ProjectID: p.ID, After: *after, Limit: *limit, Actor: *actor, Type: *typeName, ForSession: *forSession, Latest: !afterSet})
	if err != nil {
		return r.fail(err)
	}
	if *jsonOutput {
		return r.writeJSON(struct {
			Proposed      coordinationQuery `json:"proposed"`
			ProposedFiles []string          `json:"proposed_files"`
			Active        []store.Event     `json:"active_work"`
			Messages      []store.Event     `json:"messages"`
		}{proposed, proposed.Files, relevant, events})
	}

	if proposed.hasTargets() {
		fmt.Fprintf(r.Stdout, "Potential conflicts for %s:\n", proposed.String())
	} else {
		fmt.Fprintln(r.Stdout, "Active work:")
	}
	if len(relevant) == 0 {
		fmt.Fprintln(r.Stdout, "  none")
	}
	for _, event := range relevant {
		payload := activePayload(event)
		phase := lifecycleDisplayPhase(event, payload)
		fmt.Fprintf(r.Stdout, "  ! %s (%s) / %s phase=%s", event.Actor, event.Session, payload.workID(), phase)
		if payload.Task != "" {
			fmt.Fprintf(r.Stdout, " task=%s", payload.Task)
		}
		if payload.Change != "" {
			fmt.Fprintf(r.Stdout, " change=%s", payload.Change)
		}
		if len(payload.Files) > 0 {
			fmt.Fprintf(r.Stdout, " files=%s", strings.Join(payload.Files, ", "))
		}
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

type coordinationQuery struct {
	Task   string   `json:"task,omitempty"`
	Change string   `json:"change,omitempty"`
	Phase  string   `json:"phase,omitempty"`
	Files  []string `json:"files,omitempty"`
}

func (q coordinationQuery) hasTargets() bool {
	return q.Task != "" || q.Change != "" || q.Phase != "" || len(q.Files) > 0
}

func (q coordinationQuery) String() string {
	var parts []string
	if q.Task != "" {
		parts = append(parts, "task="+q.Task)
	}
	if q.Change != "" {
		parts = append(parts, "change="+q.Change)
	}
	if q.Phase != "" {
		parts = append(parts, "phase="+q.Phase)
	}
	if len(q.Files) > 0 {
		parts = append(parts, "files="+strings.Join(q.Files, ","))
	}
	return strings.Join(parts, " ")
}

func (p messagePayload) workID() string {
	if p.Work != "" {
		return p.Work
	}
	if p.Task != "" {
		return "task:" + p.Task
	}
	if p.Change != "" {
		return "change:" + p.Change
	}
	return "unknown"
}

func activePayload(event store.Event) messagePayload {
	var payload messagePayload
	raw := event.Projection
	if len(raw) == 0 {
		raw = event.Payload
	}
	_ = json.Unmarshal(raw, &payload)
	return payload
}

func filterActive(events []store.Event, proposed coordinationQuery) []store.Event {
	if !proposed.hasTargets() {
		return events
	}
	matches := make([]store.Event, 0)
	for _, event := range events {
		payload := activePayload(event)
		phase := lifecycleDisplayPhase(event, payload)
		if proposed.Phase != "" && proposed.Phase != phase {
			continue
		}
		targeted := proposed.Task != "" || proposed.Change != "" || len(proposed.Files) > 0
		matched := !targeted
		if proposed.Task != "" && proposed.Task == payload.Task {
			matched = true
		}
		if proposed.Change != "" && proposed.Change == payload.Change {
			matched = true
		}
		for _, activeFile := range payload.Files {
			for _, proposedFile := range proposed.Files {
				if pathsOverlap(activeFile, proposedFile) {
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
	if payload.Work != "" {
		fmt.Fprintf(w, "    work: %s\n", payload.Work)
	}
	if payload.Task != "" {
		fmt.Fprintf(w, "    task: %s\n", payload.Task)
	}
	if payload.Change != "" {
		fmt.Fprintf(w, "    change: %s\n", payload.Change)
	}
	if phase := lifecycleDisplayPhase(event, payload); phase != "" {
		fmt.Fprintf(w, "    phase: %s\n", phase)
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
