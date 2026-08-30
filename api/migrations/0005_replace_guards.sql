CREATE TRIGGER IF NOT EXISTS idempotency_keys_immutable_insert
BEFORE INSERT ON idempotency_keys
WHEN EXISTS (
    SELECT 1 FROM idempotency_keys
    WHERE (project_id = NEW.project_id AND session_id = NEW.session_id AND key = NEW.key)
       OR event_id = NEW.event_id
) BEGIN
    SELECT RAISE(ABORT, 'idempotency keys are immutable');
END;

CREATE TRIGGER IF NOT EXISTS api_token_revocations_immutable_insert
BEFORE INSERT ON api_token_revocations
WHEN EXISTS (
    SELECT 1 FROM api_token_revocations
    WHERE project_id = NEW.project_id AND token_id = NEW.token_id
) BEGIN
    SELECT RAISE(ABORT, 'API token revocations are immutable');
END;
