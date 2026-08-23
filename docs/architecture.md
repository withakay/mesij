# Architecture

## Purpose

Mesij is a best-effort coordination channel. It records what agents report so
other agents can make better decisions. It does not lock files, claim exclusive
task ownership, guarantee delivery, or wait for acknowledgments.

## Identity and storage

Each inferred project maps to one external SQLite database. Git projects use the
canonical common Git directory as their locator, so linked worktrees share the
database. Non-Git projects use a `.mesij-project` marker or canonical path.

An explicit database override always becomes the locator. The same database and
project name therefore produce the same project ID inside and outside Git.
Project selectors use `name:` and `path:` prefixes to avoid filesystem-dependent
interpretation.

Actor aliases are human-readable identities. Sessions identify one agent run.
Events record both, plus worktree, branch, and observed commit.

Aliases are routing conveniences, not authenticated identities. If an alias has
more than one known session, Mesij rejects alias routing and requires an exact
session ID. Direct routing is not a privacy or authorization boundary.

When a pre-migration database exists at `<git-common-dir>/mesij/events.sqlite3`,
discovery keeps that database and its original project-ID derivation. New Git
projects use external storage.

Earlier development builds that combined a Git worktree with an explicit
`--db` derived project IDs from the Git common directory. The current contract
derives explicit-database IDs from the database path so Git and non-Git callers
share one stream. Export those pre-release streams before upgrading if they must
be retained.

Default non-Git discovery intentionally searches ancestor directories for a
marker, like Git searches for `.git`. Use `path:PATH` to pin a directory and
bypass ancestor markers.

## Write model

SQLite is the cross-process mailbox. WAL transactions serialize independent CLI
processes. Each accepted write transaction:

1. Appends one immutable event.
2. Records its idempotency key.
3. Updates the agent registry and mention index.
4. Updates the materialized active-work projection for lifecycle events.
5. Commits all changes or rolls back all changes.

Database triggers reject event and idempotency-key updates and deletes. The
`active_work` table is disposable derived state. Schema migration version 3
builds it from existing events. Malformed legacy lifecycle rows remain immutable
events and are recorded in `projection_errors`; `check` reports their count.

## Reads and streams

Event sequences are monotonically increasing database cursors. `tail` reads a
bounded snapshot before waiting, drains full pages without sleeping, and writes
one event per JSONL line. `check --json` includes a `through` high-water cursor
so filtered consumers can checkpoint quiet intervals. `inbox` is a projection
over sender sessions, direct recipients, and actor mentions. It does not record
delivery or read receipts.

## Retention boundary

Retention is not active. Direct deletion would violate the current immutable-log
contract. A future version must use a schema migration and this sequence:

1. Select expired events through a durable sequence checkpoint.
2. Copy them to a versioned archive database outside the project tree.
3. Verify event IDs, counts, and a content hash.
4. Rebuild or update derived projections.
5. Remove the immutable-delete guard only inside the retention transaction.
6. Delete archived live rows and restore the guard.
7. Append a `retention.compacted` event with archive metadata.

Ordered archives plus the active database remain the complete event source.
Permanent archive deletion is a separate policy.
