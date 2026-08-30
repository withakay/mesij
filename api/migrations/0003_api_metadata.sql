CREATE TABLE IF NOT EXISTS api_metadata (
    singleton      INTEGER PRIMARY KEY CHECK(singleton = 1),
    product         TEXT NOT NULL CHECK(product = 'mesij-standalone-rest-api'),
    schema_revision INTEGER NOT NULL CHECK(schema_revision = 1)
);

INSERT OR IGNORE INTO api_metadata(singleton, product, schema_revision)
VALUES (1, 'mesij-standalone-rest-api', 1);

CREATE TRIGGER IF NOT EXISTS api_metadata_immutable_insert
BEFORE INSERT ON api_metadata
WHEN EXISTS (
    SELECT 1 FROM api_metadata
    WHERE singleton <> NEW.singleton
       OR product <> NEW.product
       OR schema_revision <> NEW.schema_revision
) BEGIN
    SELECT RAISE(ABORT, 'API metadata is immutable');
END;
CREATE TRIGGER IF NOT EXISTS api_metadata_immutable_update
BEFORE UPDATE ON api_metadata BEGIN
    SELECT RAISE(ABORT, 'API metadata is immutable');
END;
CREATE TRIGGER IF NOT EXISTS api_metadata_immutable_delete
BEFORE DELETE ON api_metadata BEGIN
    SELECT RAISE(ABORT, 'API metadata is immutable');
END;
