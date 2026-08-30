import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { mkdtempSync, writeFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';

const cli = fileURLToPath(new URL('../src/cli.js', import.meta.url));
const require = createRequire(import.meta.url);
const wrangler = require.resolve('wrangler');

test('CLI rejects command-specific unknown options', () => {
  for (const arguments_ of [
    ['db', 'init', '--unknown'],
    ['token', 'create', '--label', 'x', '--reason', 'nope'],
    ['token', 'list', '--apply'],
    ['serve', '--json'],
  ]) {
    const result = spawnSync(process.execPath, [cli, ...arguments_], { encoding: 'utf8' });
    assert.equal(result.status, 1, arguments_.join(' '));
    assert.match(result.stderr, /unknown option/);
  }
});

test('CLI boolean flags reject assigned values', () => {
  for (const flag of ['--apply=false', '--remote=true', '--json=0']) {
    const result = spawnSync(process.execPath, [cli, 'token', 'workers-create', '--label', 'x', flag], { encoding: 'utf8' });
    assert.equal(result.status, 1, flag);
    assert.match(result.stderr, /does not take a value/);
  }
});
test('Worker revoke apply preflights existence and remains idempotent when already revoked', () => {
  const directory = mkdtempSync(join(tmpdir(), 'mesij-wrangler-'));
  const database = `mesij-api-test-${process.pid}-${Date.now()}`;
  const config = join(directory, 'wrangler.toml');
  writeFileSync(config, `name = "mesij-api-test"\ncompatibility_date = "2026-08-30"\n[[d1_databases]]\nbinding = "DB"\ndatabase_name = "${database}"\ndatabase_id = "${database}"\n`);
  const setupSql = [
    'CREATE TABLE api_tokens (project_id TEXT NOT NULL, token_id TEXT NOT NULL, token_hash BLOB NOT NULL, label TEXT NOT NULL, created_at TEXT NOT NULL, PRIMARY KEY(project_id, token_id)) WITHOUT ROWID',
    'CREATE TABLE api_token_revocations (project_id TEXT NOT NULL, token_id TEXT NOT NULL, revoked_at TEXT NOT NULL, reason TEXT NOT NULL DEFAULT \'\', PRIMARY KEY(project_id, token_id)) WITHOUT ROWID',
    "INSERT INTO api_tokens VALUES ('project', 'revoked-token', X'0000000000000000000000000000000000000000000000000000000000000000', 'label', '2026-08-30T12:00:00.000Z')",
    "INSERT INTO api_token_revocations VALUES ('project', 'revoked-token', '2026-08-30T12:00:00.000Z', 'prior')",
  ].join('; ');
  const setup = spawnSync(process.execPath, [
    wrangler, 'd1', 'execute', database, '--config', config, '--command', setupSql, '--local', '--json',
  ], { encoding: 'utf8' });
  assert.equal(setup.status, 0, setup.stderr);

  const common = ['--project', 'project', '--apply', '--database', database, '--config', config, '--json'];
  const idempotent = spawnSync(process.execPath, [cli, 'token', 'workers-revoke', 'revoked-token', ...common], { encoding: 'utf8' });
  assert.equal(idempotent.status, 0, idempotent.stderr);
  assert.equal((JSON.parse(idempotent.stdout) as { applied: boolean }).applied, true);

  const absent = spawnSync(process.execPath, [cli, 'token', 'workers-revoke', 'absent-token', ...common], { encoding: 'utf8' });
  assert.equal(absent.status, 1, absent.stderr);
  assert.match(absent.stderr, /does not exist in project/);
});
test('generated Worker revoke SQL is idempotent without conflict replacement', () => {
  const result = spawnSync(process.execPath, [
    cli, 'token', 'workers-revoke', 'token-id', '--project', 'project', '--reason', 'rotate', '--json',
  ], { encoding: 'utf8' });
  assert.equal(result.status, 0, result.stderr);
  const output = JSON.parse(result.stdout) as { sql: string };
  assert.match(output.sql, /NOT EXISTS \(SELECT 1 FROM api_token_revocations/);
  assert.doesNotMatch(output.sql, /ON CONFLICT|OR REPLACE/);
});
