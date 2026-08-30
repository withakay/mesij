CREATE TABLE IF NOT EXISTS events (
    sequence          INTEGER PRIMARY KEY AUTOINCREMENT,
    id                TEXT NOT NULL UNIQUE,
    project_id        TEXT NOT NULL,
    actor             TEXT NOT NULL CHECK(length(actor) > 0),
    session_id        TEXT NOT NULL CHECK(length(session_id) > 0),
    recipient_session TEXT NOT NULL DEFAULT '',
    reply_to          TEXT NOT NULL DEFAULT '',
    event_type        TEXT NOT NULL CHECK(length(event_type) > 0),
    payload           TEXT NOT NULL CHECK(json_valid(payload)),
    worktree          TEXT NOT NULL DEFAULT '',
    branch            TEXT NOT NULL DEFAULT '',
    commit_sha        TEXT NOT NULL DEFAULT '',
    idempotency_key   TEXT NOT NULL CHECK(length(idempotency_key) > 0),
    created_at        TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS idempotency_keys (
    project_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    key        TEXT NOT NULL,
    event_id   TEXT NOT NULL UNIQUE REFERENCES events(id),
    PRIMARY KEY(project_id, session_id, key)
) WITHOUT ROWID;

CREATE INDEX IF NOT EXISTS events_project_sequence ON events(project_id, sequence);
CREATE INDEX IF NOT EXISTS events_project_type_sequence ON events(project_id, event_type, sequence);
CREATE INDEX IF NOT EXISTS events_project_actor_sequence ON events(project_id, actor, sequence);
CREATE INDEX IF NOT EXISTS events_project_session_sequence ON events(project_id, session_id, sequence);
CREATE INDEX IF NOT EXISTS events_project_recipient_sequence ON events(project_id, recipient_session, sequence);

CREATE TRIGGER IF NOT EXISTS idempotency_keys_immutable_update
BEFORE UPDATE ON idempotency_keys BEGIN
    SELECT RAISE(ABORT, 'idempotency keys are immutable');
END;
CREATE TRIGGER IF NOT EXISTS idempotency_keys_immutable_delete
BEFORE DELETE ON idempotency_keys BEGIN
    SELECT RAISE(ABORT, 'idempotency keys are immutable');
END;
CREATE TRIGGER IF NOT EXISTS events_immutable_update
BEFORE UPDATE ON events BEGIN
    SELECT RAISE(ABORT, 'events are immutable');
END;
CREATE TRIGGER IF NOT EXISTS events_immutable_delete
BEFORE DELETE ON events BEGIN
    SELECT RAISE(ABORT, 'events are immutable');
END;
