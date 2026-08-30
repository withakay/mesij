#!/usr/bin/env node
import { spawnSync } from 'node:child_process';
import { createRequire } from 'node:module';
import { resolve } from 'node:path';
import { createHandler } from './handler.js';
import { bytesToHex, generateToken, hashToken } from './crypto.js';
import { createNodeServer } from './node-server.js';
import { NodeSqliteStore } from './adapters/node-sqlite.js';

interface ParsedArgs {
  positional: string[];
  options: Map<string, string | true>;
}

const require = createRequire(import.meta.url);
const wranglerEntryPoint = require.resolve('wrangler');

async function main(): Promise<void> {
  const parsed = parseArgs(process.argv.slice(2));
  const [command, subcommand, third] = parsed.positional;
  const database = stringOption(parsed, 'db', process.env.MESIJ_DB || './.mesij-api/mesij-api.sqlite3');
  const projectId = stringOption(parsed, 'project', process.env.MESIJ_PROJECT_ID || 'default');

  if (command === 'db' && subcommand === 'init') {
    assertInvocation(parsed, ['db', 'init'], ['db', 'project', 'json']);
    const store = new NodeSqliteStore(database);
    try {
      await store.initialize();
      output(parsed, { ok: true, database: resolve(database), project_id: projectId }, `initialized ${resolve(database)}`);
    } finally { store.close(); }
    return;
  }

  if (command === 'token' && ['create', 'list', 'revoke'].includes(subcommand ?? '')) {
    const expected = subcommand === 'revoke' ? ['token', 'revoke', third ?? ''] : ['token', subcommand!];
    const allowed = subcommand === 'create'
      ? ['db', 'project', 'label', 'json']
      : subcommand === 'revoke' ? ['db', 'project', 'reason', 'json'] : ['db', 'project', 'json'];
    assertInvocation(parsed, expected, allowed);
    if (subcommand === 'revoke' && !third) fail('token revoke requires TOKEN_ID');
    const store = new NodeSqliteStore(database);
    try {
      await store.initialize();
      if (subcommand === 'create') {
        const label = requiredOption(parsed, 'label');
        const generated = generateToken();
        const createdAt = new Date().toISOString();
        await store.createToken(projectId, generated.tokenId, await hashToken(generated.token), label, createdAt);
        output(parsed, { token: generated.token, token_id: generated.tokenId, label, created_at: createdAt }, generated.token);
      } else if (subcommand === 'list') {
        const tokens = await store.listTokens(projectId);
        output(parsed, { project_id: projectId, tokens }, tokens.map((token) => `${token.token_id}\t${token.label}\t${token.revoked_at ? 'revoked' : 'active'}`).join('\n'));
      } else {
        const tokenId = third ?? fail('token revoke requires TOKEN_ID');
        const reason = stringOption(parsed, 'reason', '');
        const revokedAt = new Date().toISOString();
        const inserted = await store.revokeToken(projectId, tokenId, revokedAt, reason);
        output(parsed, { token_id: tokenId, revoked_at: revokedAt, reason, inserted }, inserted ? `revoked ${tokenId}` : `already revoked ${tokenId}`);
      }
    } finally { store.close(); }
    return;
  }

  if (command === 'token' && ['workers-create', 'workers-revoke'].includes(subcommand ?? '')) {
    const expected = subcommand === 'workers-revoke' ? ['token', 'workers-revoke', third ?? ''] : ['token', 'workers-create'];
    const allowed = subcommand === 'workers-create'
      ? ['project', 'label', 'apply', 'remote', 'json', 'database', 'config']
      : ['project', 'reason', 'apply', 'remote', 'json', 'database', 'config'];
    assertInvocation(parsed, expected, allowed);
    if (subcommand === 'workers-revoke' && !third) fail('token workers-revoke requires TOKEN_ID');
    const operation = subcommand === 'workers-create' ? 'create' : 'revoke';
    const result = operation === 'create'
      ? await workerCreateSql(projectId, requiredOption(parsed, 'label'))
      : workerRevokeSql(projectId, third ?? fail('token workers-revoke requires TOKEN_ID'), stringOption(parsed, 'reason', ''));
    if (hasOption(parsed, 'apply')) {
      if (operation === 'revoke') {
        preflightWorkerRevoke(projectId, third ?? fail('token workers-revoke requires TOKEN_ID'), parsed);
      }
      applyWorkerSql(result.sql, parsed);
      result.public = { ...result.public, applied: true };
    }
    output(parsed, result.public, result.text);
    return;
  }

  if (command === 'serve' && subcommand === undefined) {
    assertInvocation(parsed, ['serve'], ['host', 'port', 'db', 'project']);
    const store = new NodeSqliteStore(database);
    await store.initialize();
    const host = stringOption(parsed, 'host', '127.0.0.1');
    const port = integerOption(parsed, 'port', 7337, 1, 65535);
    const handler = createHandler({
      store,
      projectId,
      source: {
        worktree: process.env.MESIJ_WORKTREE || '',
        branch: process.env.MESIJ_BRANCH || '',
        commit: process.env.MESIJ_COMMIT || '',
      },
    });
    const server = createNodeServer(handler);
    server.listen(port, host, () => console.log(`mesij API listening on http://${host}:${port}`));
    const shutdown = (): void => {
      server.close(() => { store.close(); process.exit(0); });
    };
    process.once('SIGINT', shutdown);
    process.once('SIGTERM', shutdown);
    return;
  }

  fail(`usage:
  mesij-api db init [--db PATH] [--project ID] [--json]
  mesij-api token create --label NAME [--db PATH] [--project ID] [--json]
  mesij-api token list [--db PATH] [--project ID] [--json]
  mesij-api token revoke TOKEN_ID [--reason TEXT] [--db PATH] [--project ID] [--json]
  mesij-api token workers-create --label NAME [--project ID] [--apply] [--remote] [--json]
  mesij-api token workers-revoke TOKEN_ID [--reason TEXT] [--apply] [--remote] [--json]
  mesij-api serve [--host HOST] [--port PORT] [--db PATH] [--project ID]`);
}

async function workerCreateSql(projectId: string, label: string): Promise<{ sql: string; public: object; text: string }> {
  const generated = generateToken();
  const createdAt = new Date().toISOString();
  const hash = await hashToken(generated.token);
  const sql = `INSERT INTO api_tokens(project_id, token_id, token_hash, label, created_at) VALUES (${sqlString(projectId)}, ${sqlString(generated.tokenId)}, X'${bytesToHex(hash)}', ${sqlString(label)}, ${sqlString(createdAt)});`;
  return {
    sql,
    public: { token: generated.token, token_id: generated.tokenId, label, created_at: createdAt, sql, applied: false },
    text: `${generated.token}\n${sql}`,
  };
}

function workerRevokeSql(projectId: string, tokenId: string, reason: string): { sql: string; public: object; text: string } {
  const revokedAt = new Date().toISOString();
  const sql = `INSERT INTO api_token_revocations(project_id, token_id, revoked_at, reason) SELECT ${sqlString(projectId)}, ${sqlString(tokenId)}, ${sqlString(revokedAt)}, ${sqlString(reason)} WHERE EXISTS (SELECT 1 FROM api_tokens WHERE project_id = ${sqlString(projectId)} AND token_id = ${sqlString(tokenId)}) AND NOT EXISTS (SELECT 1 FROM api_token_revocations WHERE project_id = ${sqlString(projectId)} AND token_id = ${sqlString(tokenId)});`;
  return { sql, public: { token_id: tokenId, revoked_at: revokedAt, reason, sql, applied: false }, text: sql };
}

function preflightWorkerRevoke(projectId: string, tokenId: string, parsed: ParsedArgs): void {
  const sql = `SELECT COUNT(*) AS token_count FROM api_tokens WHERE project_id = ${sqlString(projectId)} AND token_id = ${sqlString(tokenId)};`;
  const result = runWrangler([...workerD1Arguments(parsed, sql), '--json'], true);
  let payload: unknown;
  try {
    payload = JSON.parse(result.stdout ?? '');
  } catch {
    fail('wrangler returned invalid JSON for token revoke preflight');
  }
  if (!Array.isArray(payload) || payload.length !== 1) {
    fail('wrangler returned invalid JSON for token revoke preflight');
  }
  const first = payload[0] as { results?: unknown[] };
  const row = first.results?.[0] as { token_count?: unknown } | undefined;
  const tokenCount = Number(row?.token_count);
  if (!Number.isSafeInteger(tokenCount) || tokenCount < 0) {
    fail('wrangler returned invalid token count for token revoke preflight');
  }
  if (tokenCount < 1) {
    fail(`token ${JSON.stringify(tokenId)} does not exist in project ${JSON.stringify(projectId)}`);
  }
}

function applyWorkerSql(sql: string, parsed: ParsedArgs): void {
  const capture = hasOption(parsed, 'json');
  const result = runWrangler(workerD1Arguments(parsed, sql), capture);
  if (capture) {
    if (result.stdout) process.stderr.write(result.stdout);
    if (result.stderr) process.stderr.write(result.stderr);
  }
}

function workerD1Arguments(parsed: ParsedArgs, sql: string): string[] {
  const database = stringOption(parsed, 'database', 'mesij-api');
  const config = stringOption(parsed, 'config', 'wrangler.toml');
  const arguments_ = ['d1', 'execute', database, '--config', config, '--command', sql];
  if (hasOption(parsed, 'remote')) arguments_.push('--remote');
  else arguments_.push('--local');
  return arguments_;
}

function runWrangler(arguments_: string[], capture: boolean): ReturnType<typeof spawnSync> & { stdout?: string; stderr?: string } {
  const result = capture
    ? spawnSync(process.execPath, [wranglerEntryPoint, ...arguments_], { encoding: 'utf8' })
    : spawnSync(process.execPath, [wranglerEntryPoint, ...arguments_], { stdio: 'inherit' });
  if (result.error) throw result.error;
  if (result.status !== 0) {
    if (capture) {
      if (result.stdout) process.stderr.write(result.stdout);
      if (result.stderr) process.stderr.write(result.stderr);
    }
    fail(`wrangler exited with status ${result.status ?? 'unknown'}`);
  }
  return result as ReturnType<typeof spawnSync> & { stdout?: string; stderr?: string };
}

function sqlString(value: string): string {
  return `'${value.replaceAll("'", "''")}'`;
}

function parseArgs(arguments_: string[]): ParsedArgs {
  const positional: string[] = [];
  const options = new Map<string, string | true>();
  for (let index = 0; index < arguments_.length; index++) {
    const argument = arguments_[index]!;
    if (!argument.startsWith('--')) {
      positional.push(argument);
      continue;
    }
    const equals = argument.indexOf('=');
    if (equals !== -1) {
      const name = argument.slice(2, equals);
      if (BOOLEAN_OPTIONS.has(name)) fail(`--${name} does not take a value`);
      if (options.has(name)) fail(`duplicate option --${name}`);
      options.set(name, argument.slice(equals + 1));
      continue;
    }
    const name = argument.slice(2);
    if (options.has(name)) fail(`duplicate option --${name}`);
    const next = arguments_[index + 1];
    if (BOOLEAN_OPTIONS.has(name)) {
      options.set(name, true);
    } else if (next !== undefined && !next.startsWith('--')) {
      options.set(name, next);
      index++;
    } else {
      options.set(name, true);
    }
  }
  return { positional, options };
}

const BOOLEAN_OPTIONS = new Set(['json', 'apply', 'remote']);

function assertInvocation(parsed: ParsedArgs, positional: string[], allowedOptions: string[]): void {
  if (parsed.positional.length !== positional.length || parsed.positional.some((value, index) => value !== positional[index])) {
    fail(`unexpected positional arguments: ${parsed.positional.join(' ')}`);
  }
  const allowed = new Set(allowedOptions);
  for (const name of parsed.options.keys()) {
    if (!allowed.has(name)) fail(`unknown option --${name}`);
  }
}

function stringOption(parsed: ParsedArgs, name: string, fallback: string): string {
  const value = parsed.options.get(name);
  if (value === undefined) return fallback;
  if (value === true || value === '') fail(`--${name} requires a value`);
  return value;
}

function requiredOption(parsed: ParsedArgs, name: string): string {
  return stringOption(parsed, name, '') || fail(`--${name} is required`);
}

function integerOption(parsed: ParsedArgs, name: string, fallback: number, minimum: number, maximum: number): number {
  const raw = stringOption(parsed, name, String(fallback));
  const value = Number(raw);
  if (!Number.isInteger(value) || value < minimum || value > maximum) fail(`--${name} must be between ${minimum} and ${maximum}`);
  return value;
}

function hasOption(parsed: ParsedArgs, name: string): boolean {
  return parsed.options.has(name);
}

function output(parsed: ParsedArgs, value: unknown, text: string): void {
  console.log(hasOption(parsed, 'json') ? JSON.stringify(value) : text);
}

function fail(message: string): never {
  throw new Error(message);
}

main().catch((error: unknown) => {
  console.error(`mesij-api: ${error instanceof Error ? error.message : String(error)}`);
  process.exitCode = 1;
});
