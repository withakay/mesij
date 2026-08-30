CREATE TABLE IF NOT EXISTS api_tokens (
    project_id TEXT NOT NULL,
    token_id   TEXT NOT NULL,
    token_hash BLOB NOT NULL CHECK(length(token_hash) = 32),
    label      TEXT NOT NULL CHECK(length(label) > 0),
    created_at TEXT NOT NULL,
    PRIMARY KEY(project_id, token_id)
) WITHOUT ROWID;

CREATE TABLE IF NOT EXISTS api_token_revocations (
    project_id TEXT NOT NULL,
    token_id   TEXT NOT NULL,
    revoked_at TEXT NOT NULL,
    reason     TEXT NOT NULL DEFAULT '',
    PRIMARY KEY(project_id, token_id),
    FOREIGN KEY(project_id, token_id)
        REFERENCES api_tokens(project_id, token_id)
) WITHOUT ROWID;

CREATE TRIGGER IF NOT EXISTS api_tokens_immutable_update
BEFORE UPDATE ON api_tokens BEGIN
    SELECT RAISE(ABORT, 'API tokens are immutable');
END;
CREATE TRIGGER IF NOT EXISTS api_tokens_immutable_delete
BEFORE DELETE ON api_tokens BEGIN
    SELECT RAISE(ABORT, 'API tokens are immutable');
END;
CREATE TRIGGER IF NOT EXISTS api_token_revocations_immutable_update
BEFORE UPDATE ON api_token_revocations BEGIN
    SELECT RAISE(ABORT, 'API token revocations are immutable');
END;
CREATE TRIGGER IF NOT EXISTS api_token_revocations_immutable_delete
BEFORE DELETE ON api_token_revocations BEGIN
    SELECT RAISE(ABORT, 'API token revocations are immutable');
END;
