import { constants, chmodSync, closeSync, existsSync, lstatSync, mkdirSync, openSync, readFileSync, statSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { DatabaseSync, type SQLInputValue } from 'node:sqlite';
import { fileURLToPath } from 'node:url';
import type { AdminStore, EventCandidate, EventFilters, EventRecord, TokenRecord } from '../types.js';
import { IdempotencyConflictError } from '../types.js';
import {
  EVENT_COLUMNS, EVENT_COLUMNS_E, INSERT_EVENT_SQL, INSERT_KEY_SQL, candidateBindings, mapEvent, sameCandidate,
  type EventRow,
} from './sql.js';

export interface NodeSqliteStoreOptions {
  migrationSql?: readonly string[];
}

export class NodeSqliteStore implements AdminStore {
  readonly #database: DatabaseSync;
  readonly #path: string;
  readonly #migrationSql: readonly string[] | undefined;

  constructor(path: string, options: NodeSqliteStoreOptions = {}) {
    this.#path = resolve(path);
    this.#migrationSql = options.migrationSql;
    prepareDatabasePath(this.#path);
    this.#database = new DatabaseSync(this.#path);
    this.#database.exec('PRAGMA busy_timeout=5000; PRAGMA foreign_keys=ON;');
  }

  async initialize(): Promise<void> {
    this.#assertDatabaseOwnership();
    secureDatabaseFile(this.#path);
    this.#database.exec('PRAGMA journal_mode=WAL; PRAGMA synchronous=NORMAL;');
    const migrations = this.#migrationSql ?? migrationSql();
    this.#database.exec('BEGIN IMMEDIATE');
    try {
      for (const sql of migrations) this.#database.exec(sql);
      this.#assertMetadataMarker();
      this.#database.exec('COMMIT');
    } catch (error) {
      try { this.#database.exec('ROLLBACK'); } catch { /* transaction may already be closed */ }
      throw error;
    }
  }

  async latestSequence(projectId: string): Promise<number> {
    const row = this.#database.prepare('SELECT COALESCE(MAX(sequence), 0) AS sequence FROM events WHERE project_id = ?').get(projectId) as { sequence: number | bigint };
    return Number(row.sequence);
  }

  async listEvents(projectId: string, filters: EventFilters): Promise<EventRecord[]> {
    const clauses = ['project_id = ?', 'sequence > ?', 'sequence <= ?'];
    const values: SQLInputValue[] = [projectId, filters.after, filters.through];
    if (filters.actor) {
      clauses.push('actor = ?');
      values.push(filters.actor);
    }
    if (filters.type) {
      clauses.push('event_type = ?');
      values.push(filters.type);
    }
    if (filters.session) {
      clauses.push('session_id = ?');
      values.push(filters.session);
    }
    values.push(filters.limit);
    const rows = this.#database.prepare(`SELECT ${EVENT_COLUMNS} FROM events WHERE ${clauses.join(' AND ')} ORDER BY sequence ASC LIMIT ?`).all(...values) as unknown as EventRow[];
    return rows.map(mapEvent);
  }

  async appendEvent(candidate: EventCandidate): Promise<{ event: EventRecord; inserted: boolean }> {
    const existing = this.#byKey(candidate.projectId, candidate.session, candidate.idempotencyKey);
    if (existing) return replay(existing, candidate);

    this.#database.exec('BEGIN IMMEDIATE');
    try {
      this.#database.prepare(INSERT_EVENT_SQL).run(...candidateBindings(candidate) as SQLInputValue[]);
      this.#database.prepare(INSERT_KEY_SQL).run(candidate.projectId, candidate.session, candidate.idempotencyKey, candidate.id);
      this.#database.exec('COMMIT');
    } catch (error) {
      try { this.#database.exec('ROLLBACK'); } catch { /* transaction may already be closed */ }
      const raced = this.#byKey(candidate.projectId, candidate.session, candidate.idempotencyKey);
      if (raced) return replay(raced, candidate);
      throw error;
    }
    const inserted = this.#byKey(candidate.projectId, candidate.session, candidate.idempotencyKey);
    if (!inserted) throw new Error('inserted event could not be read');
    return { event: inserted, inserted: true };
  }

  async activeTokenHash(projectId: string, tokenId: string): Promise<Uint8Array | null> {
    const row = this.#database.prepare(`
      SELECT t.token_hash AS token_hash
      FROM api_tokens t
      LEFT JOIN api_token_revocations r ON r.project_id = t.project_id AND r.token_id = t.token_id
      WHERE t.project_id = ? AND t.token_id = ? AND r.token_id IS NULL
    `).get(projectId, tokenId) as { token_hash: Uint8Array } | undefined;
    return row ? new Uint8Array(row.token_hash) : null;
  }

  async createToken(projectId: string, tokenId: string, tokenHash: Uint8Array, label: string, createdAt: string): Promise<void> {
    this.#database.prepare('INSERT INTO api_tokens(project_id, token_id, token_hash, label, created_at) VALUES (?, ?, ?, ?, ?)')
      .run(projectId, tokenId, tokenHash, label, createdAt);
  }

  async listTokens(projectId: string): Promise<TokenRecord[]> {
    const rows = this.#database.prepare(`
      SELECT t.token_id, t.label, t.created_at, r.revoked_at, r.reason
      FROM api_tokens t
      LEFT JOIN api_token_revocations r ON r.project_id = t.project_id AND r.token_id = t.token_id
      WHERE t.project_id = ? ORDER BY t.created_at ASC, t.token_id ASC
    `).all(projectId) as Array<Record<string, unknown>>;
    return rows.map((row) => ({
      token_id: String(row.token_id),
      label: String(row.label),
      created_at: String(row.created_at),
      ...(row.revoked_at ? { revoked_at: String(row.revoked_at), reason: String(row.reason ?? '') } : {}),
    }));
  }

  async revokeToken(projectId: string, tokenId: string, revokedAt: string, reason: string): Promise<boolean> {
    const exists = this.#database.prepare('SELECT 1 AS found FROM api_tokens WHERE project_id = ? AND token_id = ?').get(projectId, tokenId);
    if (!exists) throw new Error(`token ${tokenId} was not found`);
    if (this.#revocationExists(projectId, tokenId)) return false;
    try {
      const result = this.#database.prepare(`
        INSERT INTO api_token_revocations(project_id, token_id, revoked_at, reason)
        VALUES (?, ?, ?, ?)
      `).run(projectId, tokenId, revokedAt, reason);
      return Number(result.changes) === 1;
    } catch (error) {
      if (this.#revocationExists(projectId, tokenId)) return false;
      throw error;
    }
  }

  close(): void {
    this.#database.close();
  }

  #revocationExists(projectId: string, tokenId: string): boolean {
    return this.#database.prepare(`
      SELECT 1 AS found FROM api_token_revocations WHERE project_id = ? AND token_id = ?
    `).get(projectId, tokenId) !== undefined;
  }

  #assertDatabaseOwnership(): void {
    if (this.#tableExists('api_metadata')) {
      this.#assertMetadataMarker();
      return;
    }
    const version = this.#database.prepare('PRAGMA user_version').get() as { user_version: number };
    const goTables = ['agents', 'active_work', 'mentions', 'projection_errors'];
    const hasCoreEventStore = this.#tableExists('events') && this.#tableExists('idempotency_keys');
    const hasApiTokens = this.#tableExists('api_tokens') && this.#tableExists('api_token_revocations');
    const looksLikeGoMesij = Number(version.user_version) > 0 ||
      goTables.some((table) => this.#tableExists(table)) ||
      hasCoreEventStore && !hasApiTokens;
    const hasUserTables = this.#database.prepare(`
      SELECT 1 AS found FROM sqlite_schema
      WHERE type = 'table' AND name NOT LIKE 'sqlite_%' LIMIT 1
    `).get() !== undefined;
    if (looksLikeGoMesij) {
      throw new Error('refusing to initialize a Go Mesij database without the standalone API metadata marker');
    }
    if (hasUserTables) {
      throw new Error('refusing to initialize a non-empty database without the standalone API metadata marker');
    }
  }

  #assertMetadataMarker(): void {
    const marker = this.#database.prepare(`
      SELECT product, schema_revision FROM api_metadata WHERE singleton = 1
    `).get() as { product: string; schema_revision: number } | undefined;
    if (!marker || marker.product !== 'mesij-standalone-rest-api' || Number(marker.schema_revision) !== 1) {
      throw new Error('database has an invalid standalone API metadata marker');
    }
  }

  #tableExists(name: string): boolean {
    return this.#database.prepare(`
      SELECT 1 AS found FROM sqlite_schema WHERE type = 'table' AND name = ?
    `).get(name) !== undefined;
  }

  #byKey(projectId: string, session: string, key: string): EventRecord | null {
    const row = this.#database.prepare(`
      SELECT ${EVENT_COLUMNS_E} FROM events e
      JOIN idempotency_keys k ON k.event_id = e.id
      WHERE k.project_id = ? AND k.session_id = ? AND k.key = ?
    `).get(projectId, session, key) as unknown as EventRow | undefined;
    return row ? mapEvent(row) : null;
  }
}

const MIGRATION_FILES = [
  '0001_core.sql',
  '0002_api_tokens.sql',
  '0003_api_metadata.sql',
  '0004_immutable_insert_guards.sql',
  '0005_replace_guards.sql',
] as const;

function migrationSql(): string[] {
  return MIGRATION_FILES.map((migration) => {
    const url = new URL(`../../../migrations/${migration}`, import.meta.url);
    return readFileSync(fileURLToPath(url), 'utf8');
  });
}

function prepareDatabasePath(path: string): void {
  const parent = dirname(path);
  if (!existsSync(parent)) mkdirSync(parent, { recursive: true, mode: 0o700 });
  if (process.platform !== 'win32' && (statSync(parent).mode & 0o077) !== 0) {
    throw new Error(`database parent directory must have mode 0700: ${parent}`);
  }
  if (existsSync(path)) {
    if (!lstatSync(path).isFile()) throw new Error(`database path must be a regular file: ${path}`);
    return;
  }
  const descriptor = openSync(path, constants.O_CREAT | constants.O_EXCL | constants.O_RDWR, 0o600);
  closeSync(descriptor);
}

function secureDatabaseFile(path: string): void {
  if (process.platform !== 'win32') chmodSync(path, 0o600);
}

function replay(existing: EventRecord, candidate: EventCandidate): { event: EventRecord; inserted: boolean } {
  if (!sameCandidate(existing, candidate)) throw new IdempotencyConflictError();
  return { event: existing, inserted: false };
}
