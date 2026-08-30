import assert from 'node:assert/strict';
import { DatabaseSync } from 'node:sqlite';
import { chmodSync, mkdtempSync, mkdirSync, readFileSync, statSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import test from 'node:test';
import { NodeSqliteStore } from '../src/adapters/node-sqlite.js';
import { generateToken, hashToken } from '../src/crypto.js';
import { IdempotencyConflictError } from '../src/types.js';

test('Node init writes a standalone marker and refuses a Go Mesij database without it', async () => {
  const freshPath = join(mkdtempSync(join(tmpdir(), 'mesij-api-')), 'fresh.sqlite3');
  const fresh = new NodeSqliteStore(freshPath);
  await fresh.initialize();
  await fresh.initialize();
  fresh.close();
  if (process.platform !== 'win32') {
    assert.equal(statSync(freshPath).mode & 0o777, 0o600);
    assert.equal(statSync(dirname(freshPath)).mode & 0o777, 0o700);
  }
  const marked = new DatabaseSync(freshPath);
  const metadata = marked.prepare('SELECT product, schema_revision FROM api_metadata WHERE singleton = 1').get() as { product: string; schema_revision: number };
  assert.equal(metadata.product, 'mesij-standalone-rest-api');
  assert.equal(metadata.schema_revision, 1);
  marked.close();

  const goPath = join(mkdtempSync(join(tmpdir(), 'mesij-go-')), 'go.sqlite3');
  const go = new DatabaseSync(goPath);
  go.exec(`
    CREATE TABLE events(sequence INTEGER PRIMARY KEY, id TEXT);
    CREATE TABLE idempotency_keys(project_id TEXT, session_id TEXT, key TEXT, event_id TEXT);
  `);
  go.close();
  const refused = new NodeSqliteStore(goPath);
  await assert.rejects(refused.initialize(), /refusing to initialize a Go Mesij database/);
  refused.close();
  const unchanged = new DatabaseSync(goPath);
  assert.equal(unchanged.prepare("SELECT 1 FROM sqlite_schema WHERE type='table' AND name='api_metadata'").get(), undefined);
  unchanged.close();
});

test('fresh Node initialization is atomic, resumable, and refuses insecure parents', async () => {
  const parent = mkdtempSync(join(tmpdir(), 'mesij-atomic-'));
  const path = join(parent, 'api.sqlite3');
  const broken = new NodeSqliteStore(path, {
    migrationSql: ['CREATE TABLE should_rollback(value TEXT);', 'THIS IS NOT VALID SQL;'],
  });
  await assert.rejects(broken.initialize());
  broken.close();
  const afterFailure = new DatabaseSync(path);
  assert.equal(afterFailure.prepare("SELECT 1 FROM sqlite_schema WHERE type='table' AND name='should_rollback'").get(), undefined);
  afterFailure.close();

  const resumed = new NodeSqliteStore(path);
  await resumed.initialize();
  resumed.close();

  if (process.platform !== 'win32') {
    const insecure = join(parent, 'insecure');
    mkdirSync(insecure, { mode: 0o755 });
    chmodSync(insecure, 0o755);
    assert.throws(() => new NodeSqliteStore(join(insecure, 'api.sqlite3')), /mode 0700/);
  }
});

test('Node sqlite initializes, stores only hashes, and revokes idempotently', async () => {
  const path = join(mkdtempSync(join(tmpdir(), 'mesij-api-')), 'api.sqlite3');
  const store = new NodeSqliteStore(path);
  await store.initialize();
  const generated = generateToken((length) => new Uint8Array(length).fill(7));
  const hash = await hashToken(generated.token);
  await store.createToken('p', generated.tokenId, hash, 'test token', '2026-08-30T12:00:00.000Z');
  assert.deepEqual(await store.activeTokenHash('p', generated.tokenId), hash);
  const listed = await store.listTokens('p');
  assert.deepEqual(listed, [{ token_id: generated.tokenId, label: 'test token', created_at: '2026-08-30T12:00:00.000Z' }]);
  assert.equal(JSON.stringify(listed).includes(generated.token), false);
  assert.equal(readFileSync(path).includes(Buffer.from(generated.token)), false);

  assert.equal(await store.revokeToken('p', generated.tokenId, '2026-08-30T12:01:00.000Z', 'rotate'), true);
  assert.equal(await store.revokeToken('p', generated.tokenId, '2026-08-30T12:02:00.000Z', 'again'), false);
  assert.deepEqual((await store.listTokens('p'))[0], {
    token_id: generated.tokenId,
    label: 'test token',
    created_at: '2026-08-30T12:00:00.000Z',
    revoked_at: '2026-08-30T12:01:00.000Z',
    reason: 'rotate',
  });
  assert.equal(await store.activeTokenHash('p', generated.tokenId), null);
  store.close();
});

test('event and token facts are immutable and event retries are idempotent', async () => {
  const path = join(mkdtempSync(join(tmpdir(), 'mesij-api-')), 'api.sqlite3');
  const store = new NodeSqliteStore(path);
  await store.initialize();
  const candidate = {
    id: 'id', projectId: 'p', actor: 'a', session: 's', recipient: '', replyTo: '', type: 'message.posted',
    payloadJson: '{}', worktree: '', branch: '', commit: '', idempotencyKey: 'key', createdAt: '2026-08-30T12:00:00.000Z',
  };
  assert.equal((await store.appendEvent(candidate)).inserted, true);
  assert.equal((await store.appendEvent({ ...candidate, id: 'retry-id', createdAt: '2026-08-30T12:01:00.000Z' })).inserted, false);
  await assert.rejects(store.appendEvent({ ...candidate, id: 'changed-id', payloadJson: '{"changed":true}' }), IdempotencyConflictError);
  await store.createToken('p', 'token-id', new Uint8Array(32), 'immutable', '2026-08-30T12:00:00.000Z');
  await store.revokeToken('p', 'token-id', '2026-08-30T12:01:00.000Z', 'original');
  store.close();
  const raw = new DatabaseSync(path);
  assert.throws(() => raw.exec("INSERT INTO events SELECT * FROM events WHERE id='id'"), /immutable/);
  assert.throws(() => raw.exec("UPDATE events SET actor='b' WHERE id='id'"), /immutable/);
  assert.throws(() => raw.exec("DELETE FROM events WHERE id='id'"), /immutable/);
  assert.throws(() => raw.exec("INSERT OR REPLACE INTO idempotency_keys(project_id, session_id, key, event_id) VALUES ('p', 's', 'key', 'id')"), /immutable/);
  assert.throws(() => raw.exec("INSERT INTO api_tokens SELECT * FROM api_tokens WHERE token_id='token-id'"), /immutable/);
  assert.throws(() => raw.exec("UPDATE api_tokens SET label='changed' WHERE token_id='token-id'"), /immutable/);
  assert.throws(() => raw.exec("INSERT OR REPLACE INTO api_token_revocations(project_id, token_id, revoked_at, reason) VALUES ('p', 'token-id', '2026-08-30T12:02:00.000Z', 'changed')"), /immutable/);
  assert.throws(() => raw.exec('UPDATE api_metadata SET schema_revision=2 WHERE singleton=1'), /immutable/);
  assert.throws(() => raw.exec('DELETE FROM api_metadata WHERE singleton=1'), /immutable/);
  raw.close();
});
