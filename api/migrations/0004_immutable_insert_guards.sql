CREATE TRIGGER IF NOT EXISTS events_immutable_insert
BEFORE INSERT ON events
WHEN EXISTS (
    SELECT 1 FROM events
    WHERE id = NEW.id OR (NEW.sequence > 0 AND sequence = NEW.sequence)
) BEGIN
    SELECT RAISE(ABORT, 'events are immutable');
END;

CREATE TRIGGER IF NOT EXISTS api_tokens_immutable_insert
BEFORE INSERT ON api_tokens
WHEN EXISTS (
    SELECT 1 FROM api_tokens
    WHERE project_id = NEW.project_id AND token_id = NEW.token_id
) BEGIN
    SELECT RAISE(ABORT, 'API tokens are immutable');
END;
