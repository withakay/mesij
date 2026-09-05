# Operations

This guide covers the canonical Go CLI database. The standalone TypeScript API
has separate operational guidance in [`api/README.md`](../api/README.md).

## Locate the active project and database

```sh
mesij status
mesij status --json
```

Default data roots:

| Platform | Default root |
| --- | --- |
| Linux with `XDG_DATA_HOME` | `$XDG_DATA_HOME/mesij` |
| Linux otherwise | `~/.local/share/mesij` |
| macOS | user configuration directory plus `/mesij` |
| Windows | user configuration directory plus `\mesij` |

Override with `MESIJ_HOME`. Each new project database is stored below
`MESIJ_HOME/projects` and named from the sanitized project name and project ID.
An explicit `--db`/`MESIJ_DB` path overrides this layout.

Legacy Git databases at `<git-common-dir>/mesij/events.sqlite3` stay in place.

## Permissions

Mesij creates database directories with owner-only permissions. Keep explicit
shared database paths restricted to the intended local users. The event log can
contain file names, work plans, messages, and source context. Do not put secrets
in events.

## Concurrency

SQLite WAL mode coordinates independent CLI processes and linked worktrees.
Writes use short transactions and retry transient busy errors. Operational
recommendations:

- keep databases on a local filesystem with reliable locking;
- avoid network filesystems unless their SQLite locking behavior is known;
- do not manually modify tables while agents are active;
- let hook and CLI commands finish rather than killing them mid-transaction.

## Backup

Use SQLite's online backup command when `sqlite3` is available:

```sh
db=$(mesij status --json | jq -r .database)
backup="backups/mesij-$(date +%Y%m%d-%H%M%S).sqlite3"
mkdir -p backups
sqlite3 "$db" ".backup '$backup'"
sqlite3 "$backup" 'PRAGMA integrity_check;'
```

Without `sqlite3`, stop all Mesij writers before copying the database. Copying
only the main file while WAL writes are active can produce an incomplete
backup.

The backup command above verifies the exact file it created.

## Schema upgrades

The canonical store migrates automatically when opened. The current schema is
version 4. When upgrading from a schema before version 2, Mesij rebuilds agents,
mentions, active work, and projection errors by replaying events. Version 4 adds
the `host`, `user_name`, and `ip` columns; existing rows keep empty values.

Malformed historical lifecycle events remain immutable. Mesij records their IDs
in `projection_errors`; `check` reports the project count.

Before upgrading production/shared databases:

1. stop active writers;
2. create and verify a backup;
3. run `mesij init`, `mesij check`, `mesij agents`, or another command that
   opens the store and applies migrations;
4. run normal tests or a smoke workflow;
5. restart harnesses.

## Stale active claims

Claims have no lease or automatic timeout. If an agent disappears, another
session should coordinate where possible, then explicitly close or supersede the
work using the original work/task/change identity.

```sh
mesij agents
mesij check
mesij defer --actor ORIGINAL_ACTOR --session ORIGINAL_SESSION \
  --work WORK_ID --key "WORK_ID:recovery-defer" \
  --message "Deferring stale claim after operator review"
```

Lifecycle projection identity includes the session. Closing an abandoned claim
therefore requires deliberately reusing its original session ID and actor from
`mesij agents --json`. Using another actor would update the session registry and
change alias/mention behavior. Mesij identities are self-asserted, so operators
should record why they are doing this. Do not delete `active_work` rows manually;
they are rebuildable projections of immutable facts.

## Database growth and retention

Retention is not implemented. Events and idempotency facts are append-only, so
database size grows over time. Do not delete rows directly. The archival
boundary and required future compaction sequence are described in
[`architecture.md`](architecture.md).

## Cursor recovery

If a consumer loses its cursor:

- use `tail --after 0 --follow` to start at the first event, drain every full
  page, and then wait; stop it after catch-up if continuous following is not
  wanted;
- use `check --after 0 --limit 1000 --json` for a bounded coordination report;
- when a page contains the requested limit (which must be at most 1,000),
  continue from the last message sequence;
- persist `through` only after a short or empty page;
- understand that inbox cursors are observation progress, not read receipts.

Harness hook cursors are stored outside the repository. Resetting one can cause
messages to be injected again; it does not change the event log.

## Common errors

### `actor alias matches multiple sessions`

Use an exact session ID from `mesij agents --json`. Mesij does not guess among
multiple sessions sharing an actor alias.

### `idempotency key was already used for different event data`

Retry with exactly the original command/input or choose a new stable key for the
new semantic event. Source context is part of equivalence except for
`session.started`.

### `targets map to multiple active work identities`

Pass explicit `--work` and retain that identity through plan, implement, and
finish/defer.

### Nested non-Git directories resolve as separate projects

Run `mesij init` at the intended non-Git project root to create a
`.mesij-project` marker, or select the root explicitly with
`--project path:PATH`.

### Hook warning without blocked work

Hook adapters fail open on operational errors. Check `mesij status`, confirm the
binary is on `PATH`, and run `mesij inbox`/`mesij check` manually.

## Uninstall and data removal

Removing the binary or harness plugin does not remove data. Before deleting a
project database:

1. locate it with `mesij status --json`;
2. stop all writers and hooks;
3. archive and verify it;
4. remove the exact database, WAL, and SHM files only if permanent deletion is
   intended.

Do not use broad cleanup commands against Git directories or `MESIJ_HOME`.
