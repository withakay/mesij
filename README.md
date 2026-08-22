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
- **Event-sourced:** active work is projected from `work.started`,
  `work.finished`, and `work.deferred` events; it is not mutable state.
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

# Check active claims and recent messages before deciding to edit.
mesij --project payments check \
  --session "$MESIJ_SESSION" \
  --file internal/payments --file migrations

# Claim intended work. Stable keys make retries safe.
mesij --project payments start \
  --task pay-142 \
  --file internal/payments --file migrations/0142.sql \
  --key pay-142:start \
  --message "Add idempotent capture endpoint"

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
corresponding flags. `post`, `start`, `finish`, `defer`, and `reply` require a
session.

## Commands

- `init` — initialize the project database.
- `session` — create and announce an agent session; emits shell exports.
- `check` — show active work, potential path conflicts, and messages.
- `start` — append a `work.started` claim for a task and paths.
- `finish` / `defer` — close a task claim through another immutable event.
- `post` — append an arbitrary typed message.
- `reply` — append a message addressed to a session.
- `status` — show project, worktree, branch, commit, and database identity.
- `tui` — open the human-oriented `tview` interface. Press `Tab` to change panes,
  `r` to refresh, and `q` or `Esc` to quit.

Use `--json` with session, post, lifecycle, status, or check commands for agent
integration. `check --json` returns one coordination report containing
`active_work` and `messages`. Supplying `check --session ID` includes broadcasts
and messages addressed to that session; omitting it displays the complete log.

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
