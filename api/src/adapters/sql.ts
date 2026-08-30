import { canonicalJson } from '../strict-json.js';
import type { EventCandidate, EventRecord } from '../types.js';

export const EVENT_COLUMNS = `
sequence, id, project_id, actor, session_id, recipient_session, reply_to, event_type,
payload, worktree, branch, commit_sha, idempotency_key, created_at`;

export const EVENT_COLUMNS_E = `
e.sequence, e.id, e.project_id, e.actor, e.session_id, e.recipient_session, e.reply_to, e.event_type,
e.payload, e.worktree, e.branch, e.commit_sha, e.idempotency_key, e.created_at`;

export const INSERT_EVENT_SQL = `
INSERT INTO events
(id, project_id, actor, session_id, recipient_session, reply_to, event_type, payload,
 worktree, branch, commit_sha, idempotency_key, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`;

export const INSERT_KEY_SQL = `
INSERT INTO idempotency_keys(project_id, session_id, key, event_id)
VALUES (?, ?, ?, ?)`;

export function candidateBindings(candidate: EventCandidate): unknown[] {
  return [
    candidate.id, candidate.projectId, candidate.actor, candidate.session, candidate.recipient,
    candidate.replyTo, candidate.type, candidate.payloadJson, candidate.worktree, candidate.branch,
    candidate.commit, candidate.idempotencyKey, candidate.createdAt,
  ];
}

export interface EventRow {
  sequence: number | bigint;
  id: string;
  project_id: string;
  actor: string;
  session_id: string;
  recipient_session: string;
  reply_to: string;
  event_type: string;
  payload: string;
  worktree: string;
  branch: string;
  commit_sha: string;
  idempotency_key: string;
  created_at: string;
}

export function mapEvent(row: EventRow): EventRecord {
  return {
    sequence: Number(row.sequence),
    id: row.id,
    project_id: row.project_id,
    actor: row.actor,
    session: row.session_id,
    ...(row.recipient_session ? { recipient_session: row.recipient_session } : {}),
    ...(row.reply_to ? { reply_to: row.reply_to } : {}),
    type: row.event_type,
    payload: JSON.parse(row.payload) as unknown,
    worktree: row.worktree,
    ...(row.branch ? { branch: row.branch } : {}),
    ...(row.commit_sha ? { commit: row.commit_sha } : {}),
    idempotency_key: row.idempotency_key,
    created_at: row.created_at,
  };
}

export function sameCandidate(event: EventRecord, candidate: EventCandidate): boolean {
  const sameSource = event.type === 'session.started' && candidate.type === 'session.started' ||
    event.worktree === candidate.worktree &&
    (event.branch ?? '') === candidate.branch &&
    (event.commit ?? '') === candidate.commit;
  return event.project_id === candidate.projectId &&
    event.actor === candidate.actor &&
    event.session === candidate.session &&
    (event.recipient_session ?? '') === candidate.recipient &&
    (event.reply_to ?? '') === candidate.replyTo &&
    event.type === candidate.type &&
    canonicalJson(event.payload) === candidate.payloadJson &&
    sameSource;
}
