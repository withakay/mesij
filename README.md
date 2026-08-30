# mesij

`mesij` is a Go CLI and TUI for coordinating coding agents through an
append-only SQLite event log. It helps an agent discover work already underway,
avoid overlapping edits, publish progress, and reply directly to another agent
session.

Start with the [documentation index](docs/README.md), the
[getting-started guide](docs/getting-started.md), or the normative
[protocol and process flows](docs/protocol.md).

## Choose a deployment

| Need | Implementation |
| --- | --- |
| Full coordination, projections, conflict checks, inboxes, hooks, and TUI | Canonical Go CLI |
| Authenticated event exchange on Node.js or Cloudflare Workers/D1 | Standalone TypeScript API in `api/` |

The two implementations intentionally use separate databases.

## Design

- **Worktree-aware:** linked Git worktrees derive one project identity and share
  one database in the platform user-data directory.
- **Path-aware:** non-Git projects use an ancestor `.mesij-project` marker or the
  canonical current path. `name:NAME` and `path:PATH` disambiguate selectors.
- **External storage:** each inferred project gets a separate database under
  `MESIJ_HOME/projects`; `--db` and `MESIJ_DB` remain explicit overrides.
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
- **Aliases and mentions:** `--to ACTOR` resolves only when that actor has one
  known session; inline `@actor` mentions on `message.%` events appear in that
  actor's inbox.

## Install

As of **August 30, 2026**, no GitHub release has been published. Build and
install from source:

```sh
git clone https://github.com/withakay/mesij.git
cd mesij
go test ./...
go build -o ./bin/mesij ./cmd/mesij
mkdir -p "$HOME/.local/bin"
install -m 0755 ./bin/mesij "$HOME/.local/bin/mesij"
export PATH="$HOME/.local/bin:$PATH"
```

See [Getting started](docs/getting-started.md) for the full first-session flow.
Once a release exists, the repository installer will support release archives:

```sh
curl -fsSL https://raw.githubusercontent.com/withakay/mesij/main/install.sh | sh
```

Use a project-local destination when needed:

```sh
curl -fsSL https://raw.githubusercontent.com/withakay/mesij/main/install.sh | sh -s -- --dir ./local/bin
```

Release-please opens a release PR from Conventional Commit messages. Merging
that PR creates the GitHub release and its downloadable archives.

## Build

Mise pins the Go runtime and defines the canonical development tasks. The
Makefile provides short wrappers around those tasks.

```sh
mise install
make build
make check
make install
```

Useful commands:

```sh
make build
make test
make test-race
make run ARGS="status"
make format
mise tasks
```

Go 1.25.0 is pinned in `mise.toml`. SQLite is embedded through the pure-Go
`modernc.org/sqlite` driver. Run `mise run <task>` directly when Make is not
available.

## TypeScript REST API MVP

A portable, framework-free, standalone REST API for Node.js 24+
(`node:sqlite`) and Cloudflare Workers (D1) lives in [`api/`](api/README.md).
It owns a separate marked database and must not be pointed at the Go CLI's
SQLite database. It includes its own schema migrations, token CLI, Wrangler
configuration, and tests. The documented MVP intentionally does not implement
the Go CLI's full projections, conflict check, aliases, or inbox behavior.

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

# Address the original sender. A reply ID routes automatically.
mesij --project payments reply \
  --reply-to 90ac42... \
  --key pay-142:reply-review \
  --message "Please defer the migration; I am changing the same table"

# Release the active claim.
mesij --project payments finish \
  --task pay-142 --key pay-142:finish \
  --message "Merged capture endpoint"
```

`MESIJ_ACTOR`, `MESIJ_SESSION`, `MESIJ_PROJECT`, and `MESIJ_DB` can replace their
corresponding flags. `post`, `plan`, `implement`, `start`, `finish`, `defer`,
and `reply` require a session. Claims and messages are shared project metadata;
do not post secrets.

## Commands

- `init` — initialize the project database.
- `session` — create and announce an agent session; emits shell exports.
- `agents` — list known actor aliases and sessions.
- `check` — show active work and conflicts by task, change, phase, or file path.
- `plan` — append a `work.planned` claim.
- `implement` — append a `work.implementing` claim for the same work identity.
- `start` — legacy/general active claim using `work.started`; treated as implement.
- `finish` / `defer` — close a work claim through another immutable event.
- `post` — append an arbitrary typed message.
- `emit` — read one strict JSON request from stdin or `--input PATH`, then write
  the resulting event as JSON.
- `reply` — reply to an event or address an actor alias or exact session.
- `inbox` — list messages sent by, addressed to, or mentioning a session.
- `hook` — adapt Claude, Codex, and GitHub Copilot lifecycle hook JSON to
  Mesij sessions, inboxes, and pre-edit checks.
- `status` — show project, worktree, branch, commit, and database identity.
- `tail` — emit the event stream as JSONL; add `--follow` to keep watching.
- `tui` — open the human-oriented `tview` interface. Press `Tab` to change panes,
  `r` to refresh, and `q` or `Esc` to quit.

Lifecycle commands accept `--work`, `--task`, `--change`, and repeatable
`--file`; pass `--file` once per affected path. The work identity defaults to
`task:TASK` or `change:CHANGE`; use an explicit `--work` when neither is present.
Reusing the same identity moves one claim from plan to implement to
finished/deferred. Claims do not expire automatically. Known task/change/file
scopes are carried forward conservatively, so an implementation event does not
repeat every file named during planning. If task/change identities are ambiguous,
mesij asks for an explicit `--work`.

Use global `--json` before a command, or command-local `--json`, for agent
integration. For strict JSON input and output, use `emit`:

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

`check --json` returns one coordination report containing `through`,
`active_work`, and `messages`. For automated reads, use a `limit` from 1 to
1,000. If `messages` contains that many rows, continue with `--after` set to the
last message sequence. Only after a short or empty page is it safe to persist
`through`, including quiet filtered intervals. Active entries keep the immutable stored `payload` and
include a derived `projection` containing scopes carried forward from prior
lifecycle events. Supplying `check --session ID` includes broadcasts and
messages addressed to that session; omitting it displays the complete log.

## Project resolution

Resolution uses the following order:

1. `--db` or `MESIJ_DB` selects an explicit database. Project IDs then derive
   from the canonical database path and project name in every working directory.
2. `--project name:NAME` sets an explicit name while retaining normal
   Git/marker/path locator discovery.
3. `--project path:PATH` selects the discovery starting directory. In a non-Git
   project it pins that directory; inside Git, the containing repository root and
   common directory still define the project.
4. Git projects derive identity from the canonical Git common directory.
5. Non-Git projects use the nearest `.mesij-project` marker, then the canonical
   current path.

Run `mesij init` to write a marker for a non-Git project. Set `MESIJ_HOME` to
override the platform data directory. See
[`docs/architecture.md`](docs/architecture.md) for migrations, projections, and
the retention boundary.

Existing databases at `<git-common-dir>/mesij/events.sqlite3` remain in place
and retain their original project IDs. New projects use external storage.

## Harness integrations

`integrations/` contains plugins, hooks, extensions, and skills:

- `claude-code/` — Claude Code plugin, lifecycle hooks, and skill.
- `codex/` — Codex plugin, lifecycle hooks, skill, and UI metadata.
- `github-copilot/` — GitHub Copilot CLI Open Plugin Spec plugin, hooks, and
  skill.
- `opencode/` — OpenCode plugin with native tools, inbox polling, and skill.
- `pi/` — installable Pi extension/skill package with native tools and inbox
  polling.
- `herdr/` — Herdr plugin with Mesij panes, actions, and status-change inbox
  notifications.
- `generic/agent-start.sh` — shell bootstrap for other harnesses.

Claude, Codex, and GitHub Copilot CLI share the first-class `mesij hook`
adapter. OpenCode and Pi provide native `mesij_inbox`, `mesij_check`,
`mesij_emit`, and `mesij_agents` tools. See
[`integrations/README.md`](integrations/README.md).

The portable standalone REST API for Node.js 24+ and Cloudflare Workers/D1
lives in [`api/`](api/README.md). Its design and future parity work are tracked
in [`docs/rest-api-plan.md`](docs/rest-api-plan.md).

## Event envelope

Each row contains a monotonic sequence cursor, random event ID, project ID,
actor, session, optional recipient/reply target, event type, JSON payload,
worktree/branch/commit context, idempotency key, and UTC timestamp. Automated
`check` consumers should drain full pages from the last message sequence and
persist `through` only after a short or empty page. When `--after` is omitted,
`check` shows the most recent window for humans.
