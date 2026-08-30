import type { EventCandidate, EventFilters, EventRecord, Store } from '../types.js';
import { IdempotencyConflictError } from '../types.js';
import type { D1Database, D1Value } from './d1-types.js';
import {
  EVENT_COLUMNS, EVENT_COLUMNS_E, INSERT_EVENT_SQL, INSERT_KEY_SQL, candidateBindings, mapEvent, sameCandidate,
  type EventRow,
} from './sql.js';

export class D1Store implements Store {
  constructor(private readonly database: D1Database) {}

  async latestSequence(projectId: string): Promise<number> {
    const row = await this.database.prepare('SELECT COALESCE(MAX(sequence), 0) AS sequence FROM events WHERE project_id = ?').bind(projectId).first<{ sequence: number }>();
    return Number(row?.sequence ?? 0);
  }

  async listEvents(projectId: string, filters: EventFilters): Promise<EventRecord[]> {
    const clauses = ['project_id = ?', 'sequence > ?', 'sequence <= ?'];
    const values: D1Value[] = [projectId, filters.after, filters.through];
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
    const result = await this.database.prepare(`SELECT ${EVENT_COLUMNS} FROM events WHERE ${clauses.join(' AND ')} ORDER BY sequence ASC LIMIT ?`)
      .bind(...values).all<EventRow>();
    if (!result.success) throw new Error(result.error ?? 'D1 event query failed');
    return (result.results ?? []).map(mapEvent);
  }

  async appendEvent(candidate: EventCandidate): Promise<{ event: EventRecord; inserted: boolean }> {
    const existing = await this.#byKey(candidate.projectId, candidate.session, candidate.idempotencyKey);
    if (existing) return replay(existing, candidate);
    try {
      const results = await this.database.batch([
        this.database.prepare(INSERT_EVENT_SQL).bind(...candidateBindings(candidate) as D1Value[]),
        this.database.prepare(INSERT_KEY_SQL).bind(candidate.projectId, candidate.session, candidate.idempotencyKey, candidate.id),
      ]);
      if (results.some((result) => !result.success)) throw new Error('D1 append batch failed');
    } catch (error) {
      const raced = await this.#byKey(candidate.projectId, candidate.session, candidate.idempotencyKey);
      if (raced) return replay(raced, candidate);
      throw error;
    }
    const inserted = await this.#byKey(candidate.projectId, candidate.session, candidate.idempotencyKey);
    if (!inserted) throw new Error('inserted event could not be read');
    return { event: inserted, inserted: true };
  }

  async activeTokenHash(projectId: string, tokenId: string): Promise<Uint8Array | null> {
    const row = await this.database.prepare(`
      SELECT t.token_hash AS token_hash
      FROM api_tokens t
      LEFT JOIN api_token_revocations r ON r.project_id = t.project_id AND r.token_id = t.token_id
      WHERE t.project_id = ? AND t.token_id = ? AND r.token_id IS NULL
    `).bind(projectId, tokenId).first<{ token_hash: ArrayBuffer | Uint8Array }>();
    if (!row) return null;
    return row.token_hash instanceof Uint8Array ? row.token_hash : new Uint8Array(row.token_hash);
  }

  async #byKey(projectId: string, session: string, key: string): Promise<EventRecord | null> {
    const row = await this.database.prepare(`
      SELECT ${EVENT_COLUMNS_E} FROM events e
      JOIN idempotency_keys k ON k.event_id = e.id
      WHERE k.project_id = ? AND k.session_id = ? AND k.key = ?
    `).bind(projectId, session, key).first<EventRow>();
    return row ? mapEvent(row) : null;
  }
}

function replay(existing: EventRecord, candidate: EventCandidate): { event: EventRecord; inserted: boolean } {
  if (!sameCandidate(existing, candidate)) throw new IdempotencyConflictError();
  return { event: existing, inserted: false };
}
