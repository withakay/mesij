# mesij

`mesij` is a Go CLI and TUI for coordinating coding agents through an
append-only SQLite event log. It helps an agent discover work already underway,
avoid overlapping edits, publish progress, and reply directly to another agent
session.

## Design

- **Worktree-aware:** the default database lives at
  `<git-common-dir>/mesij/events.sqlite3`, so linked Git worktrees share a log.
- **Named projects:** `--project NAME` (or `MESIJ_PROJECT`) creates an independent
  event stream in the shared database. It defaults to the repository directory.
- **Actor sessions:** every event records both a readable actor and a session ID.
- **Event-sourced:** active work is projected from `work.planned`,
  `work.implementing`/`work.started`, `work.finished`, and `work.deferred`
  events; it is not mutable state.
- **Multi-scope coordination:** claims can refer to tasks, changes, files, or any
  combination. Conflict checks understand exact task/change matches and
  overlapping file paths.
- **Immutable:** SQLite triggers reject updates and deletes.
- **Idempotent:** `(project, session, key)` is unique. Retrying identical data
  returns the original event; reusing a key with different data fails.
- **Direct replies:** `--to SESSION` marks an intended recipient, while preserving
  the event in the public project log. This is routing, not access control.

## Build

```sh
go build -o mesij ./cmd/mesij
install -m 0755 mesij ~/.local/bin/mesij
```

Requires Go 1.25 or newer. SQLite is embedded through the pure-Go
`modernc.org/sqlite` driver.

## Agent workflow

Global flags such as `--project` and `--db` go before the command.

```sh
# Open one session and retain the printed environment variables.
eval "$(mesij --project payments session --actor agent-blue)"

# Check task, change, and file scopes before deciding what to do.
mesij --project payments check \
  --session "$MESIJ_SESSION" \
  --task pay-142 --change capture-v2 \
  --file internal/payments --file migrations

# Announce planning work. Stable keys make retries safe.
mesij --project payments plan \
  --task pay-142 --change capture-v2 \
  --file internal/payments --file migrations/0142.sql \
  --key pay-142:plan \
  --message "Planning an idempotent capture endpoint"

# Move the same work claim into implementation.
mesij --project payments implement \
  --task pay-142 --change capture-v2 \
  --file internal/payments --file migrations/0142.sql \
  --key pay-142:implement \
  --message "Implementing the agreed plan"

# Publish useful progress or a decision.
mesij --project payments post \
  --type progress.updated --task pay-142 \
  --key pay-142:handler-done \
  --message "Handler is complete; migration remains"

# Address another session directly. Use an event ID with --reply-to when useful.
mesij --project payments reply \
  --to 7f03a1... --reply-to 90ac42... \
  --key pay-142:reply-review \
  --message "Please defer the migration; I am changing the same table"

# Release the active claim.
mesij --project payments finish \
  --task pay-142 --key pay-142:finish \
  --message "Merged capture endpoint"
```

`MESIJ_ACTOR`, `MESIJ_SESSION`, `MESIJ_PROJECT`, and `MESIJ_DB` can replace their
corresponding flags. `post`, `plan`, `implement`, `start`, `finish`, `defer`,
and `reply` require a session.

## Commands

- `init` — initialize the project database.
- `session` — create and announce an agent session; emits shell exports.
- `check` — show active work and conflicts by task, change, phase, or file path.
- `plan` — append a `work.planned` claim.
- `implement` — append a `work.implementing` claim for the same work identity.
- `start` — legacy/general active claim using `work.started`; treated as implement.
- `finish` / `defer` — close a work claim through another immutable event.
- `post` — append an arbitrary typed message.
- `emit` — read one strict JSON request from stdin or `--input PATH`, then write
  the resulting event as JSON.
- `reply` — append a message addressed to a session.
- `status` — show project, worktree, branch, commit, and database identity.
- `tail` — emit the event stream as JSONL; add `--follow` to keep watching.
- `tui` — open the human-oriented `tview` interface. Press `Tab` to change panes,
  `r` to refresh, and `q` or `Esc` to quit.

Lifecycle commands accept `--work`, `--task`, `--change`, and repeatable
`--file`; pass `--file` once per affected path. The work identity defaults to
`task:TASK` or `change:CHANGE`; use an explicit `--work` when neither is present.
Reusing the same identity moves one claim from plan to implement to
finished/deferred. Known task/change/file scopes
are carried forward conservatively, so an implementation event does not need to
repeat every file named during planning. If task/change identities are ambiguous,
mesij asks for an explicit `--work`.

Use `--json` with session, post, lifecycle, status, or check commands for agent
integration. For JSON input and output, use `emit`:

```sh
cat <<'JSON' | mesij --project payments emit
{
  "event": "implement",
  "actor": "agent-blue",
  "session": "session-123",
  "task": "pay-142",
  "change": "capture-v2",
  "files": [
    "internal/payments/handler.go",
    "internal/payments/service.go",
    "migrations/0142.sql"
  ],
  "key": "pay-142:implement",
  "message": "Implementing the agreed plan"
}
JSON
```

`event` accepts `plan`, `implement`, `start`, `finish`, `defer`, `post`, or
`reply`. Alternatively, provide an arbitrary `type`. Unknown JSON fields and
multiple top-level values are rejected. Successful output is the event JSON;
failures are JSON objects containing `ok`, `error`, and `exit_code`.

For long-running consumers, `tail` writes one event per line:

```sh
# Replay from the beginning without gaps, then wait for new events.
mesij --project payments tail --after 0 --follow

# Route broadcasts and direct messages for one session.
mesij --project payments tail --after 42 --follow \
  --session "$MESIJ_SESSION"
```

Without an explicit `--after`, `tail` emits the most recent window. With
`--after 0`, it starts at the first event. Filters include `--from`, `--type`,
`--session`, `--limit`, and `--poll`.

`check --json` returns one coordination report containing `active_work` and
`messages`. Active entries keep the immutable stored `payload` and may include a
derived `projection` containing scopes carried forward from prior lifecycle
events. Supplying `check --session ID` includes broadcasts and messages
addressed to that session; omitting it displays the complete log.

## Harness integrations

`integrations/` contains reusable coordination policy and adapters:

- `claude-code/` — plugin manifest, session/pre-edit hooks, and a mesij skill.
- `codex/skills/mesij/` — a portable Codex skill.
- `generic/agent-start.sh` — a shell bootstrap for other harnesses.

The skill is the primary behavioral contract: check, claim, communicate, and
release. Hooks provide reminders at lifecycle/tool boundaries, while CLI JSON
output allows richer plugins to route messages or build approvals. See
[`integrations/README.md`](integrations/README.md).

## Event envelope

Each row contains a monotonic sequence cursor, random event ID, project ID,
actor, session, optional recipient/reply target, event type, JSON payload,
worktree/branch/commit context, idempotency key, and UTC timestamp. Consumers can
poll without gaps by explicitly starting with `check --after 0`, then advancing
to the greatest returned sequence. When `--after` is omitted, `check` shows the
most recent window for humans.
