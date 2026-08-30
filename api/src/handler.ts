import { constantTimeEqual, hashToken, parseToken } from './crypto.js';
import { buildCandidate } from './event-input.js';
import { MAX_EVENT_BYTES, MAX_EVENTS_PAGE_LIMIT, MAX_EVENTS_RESPONSE_BYTES } from './limits.js';
import { parseStrictJson } from './strict-json.js';
import { ApiError, IdempotencyConflictError, RequestBodyTooLargeError, type EventCandidate, type EventFilters, type EventRecord, type HandlerOptions } from './types.js';

const JSON_HEADERS = { 'content-type': 'application/json; charset=utf-8', 'cache-control': 'no-store' };

export function createHandler(options: HandlerOptions): (request: Request) => Promise<Response> {
  return async (request: Request): Promise<Response> => {
    try {
      const url = new URL(request.url);
      if (url.pathname === '/healthz') {
        requireMethod(request, 'GET');
        return json({ ok: true });
      }

      if (!url.pathname.startsWith('/v1/')) throw new ApiError(404, 'not found', 'not_found');
      await authenticate(request, options);

      if (url.pathname === '/v1/status') {
        requireMethod(request, 'GET');
        const latestSequence = await options.store.latestSequence(options.projectId);
        return json({
          ok: true,
          api_version: 'v1',
          project_id: options.projectId,
          latest_sequence: latestSequence,
          projections_supported: false,
        });
      }

      if (url.pathname === '/v1/events') {
        if (request.method === 'GET') return await listEvents(url, options);
        if (request.method === 'POST') return await postEvent(request, options);
        throw new ApiError(405, 'method not allowed', 'method_not_allowed');
      }

      throw new ApiError(404, 'not found', 'not_found');
    } catch (error) {
      if (error instanceof ApiError) return problem(error.status, error.message, error.code);
      if (error instanceof IdempotencyConflictError) {
        return problem(409, 'idempotency key was already used for different event data', 'idempotency_conflict');
      }
      console.error('mesij API request failed', error instanceof Error ? error.message : 'unknown error');
      return problem(500, 'internal server error', 'internal_error');
    }
  };
}

async function authenticate(request: Request, options: HandlerOptions): Promise<void> {
  const token = request.headers.get('x-message-api-token');
  if (!token) throw new ApiError(401, 'unauthorized', 'unauthorized');
  const parsed = parseToken(token);
  if (!parsed) throw new ApiError(401, 'unauthorized', 'unauthorized');
  const expected = await options.store.activeTokenHash(options.projectId, parsed.tokenId);
  const actual = await hashToken(token);
  if (!expected || !constantTimeEqual(expected, actual)) {
    throw new ApiError(401, 'unauthorized', 'unauthorized');
  }
}

async function postEvent(request: Request, options: HandlerOptions): Promise<Response> {
  const contentType = request.headers.get('content-type') ?? '';
  if (!/^application\/json(?:\s*;|$)/i.test(contentType)) {
    throw new ApiError(400, 'content-type must be application/json', 'invalid_json');
  }
  const declaredLength = request.headers.get('content-length');
  if (declaredLength && Number(declaredLength) > MAX_EVENT_BYTES) {
    throw new ApiError(413, 'JSON body exceeds 256 KiB', 'body_too_large');
  }
  const bytes = await readBody(request);
  let body: unknown;
  try {
    body = parseStrictJson(new TextDecoder('utf-8', { fatal: true }).decode(bytes));
  } catch (error) {
    const message = error instanceof Error ? error.message : 'invalid JSON';
    throw new ApiError(400, `invalid JSON: ${message}`, 'invalid_json');
  }
  const candidate = buildCandidate(body, options);
  if (utf8Bytes(JSON.stringify(candidateSizingRecord(candidate))) > MAX_EVENT_BYTES) {
    throw new ApiError(413, 'event exceeds 256 KiB', 'event_too_large');
  }
  const result = await options.store.appendEvent(candidate);
  return json({ ...result.event, inserted: result.inserted }, result.inserted ? 201 : 200);
}

async function listEvents(url: URL, options: HandlerOptions): Promise<Response> {
  rejectUnknownQuery(url, new Set(['cursor', 'limit', 'actor', 'type', 'session']));
  const limit = integerParameter(url, 'limit', MAX_EVENTS_PAGE_LIMIT, 1, MAX_EVENTS_PAGE_LIMIT);
  const actor = stringParameter(url, 'actor');
  const type = stringParameter(url, 'type');
  const session = stringParameter(url, 'session');
  const cursorValues = url.searchParams.getAll('cursor');
  if (cursorValues.length > 1) throw new ApiError(400, 'cursor must be provided at most once', 'invalid_cursor');
  const cursor = parseCursor(cursorValues[0] ?? null);
  const latest = await options.store.latestSequence(options.projectId);
  const through = cursor?.through ?? latest;
  const after = cursor?.after ?? 0;
  if (through > latest) throw new ApiError(400, 'cursor snapshot is in the future', 'invalid_cursor');
  if (after > through) throw new ApiError(400, 'cursor is outside its snapshot', 'invalid_cursor');
  if (cursor && (cursor.actor !== actor || cursor.type !== type || cursor.session !== session)) {
    throw new ApiError(400, 'cursor does not match the requested filters', 'invalid_cursor');
  }

  const filters: EventFilters = { after, through, limit: limit + 1, actor, type, session };
  const rows = await options.store.listEvents(options.projectId, filters);
  const candidates = rows.slice(0, limit);
  const events = [] as typeof candidates;
  for (const event of candidates) {
    const probeEvents = [...events, event];
    const probe = pagePayload(through, event.sequence, true, probeEvents, actor, type, session);
    if (utf8Bytes(JSON.stringify(probe)) > MAX_EVENTS_RESPONSE_BYTES) break;
    events.push(event);
  }
  if (candidates.length > 0 && events.length === 0) {
    throw new ApiError(500, 'stored event exceeds the response budget', 'event_too_large');
  }
  const hasMore = events.length < rows.length;
  const nextAfter = hasMore ? events.at(-1)!.sequence : through;
  const payload = pagePayload(through, nextAfter, hasMore, events, actor, type, session);
  const serialized = JSON.stringify(payload);
  if (utf8Bytes(serialized) > MAX_EVENTS_RESPONSE_BYTES) {
    throw new ApiError(500, 'event list exceeds the response budget', 'response_too_large');
  }
  return jsonText(serialized);
}

function pagePayload(
  through: number,
  nextAfter: number,
  hasMore: boolean,
  events: EventRecord[],
  actor: string,
  type: string,
  session: string,
): object {
  return {
    through,
    next_after: nextAfter,
    next_cursor: hasMore ? encodeCursor({ v: 1, after: nextAfter, through, actor, type, session }) : null,
    has_more: hasMore,
    events,
  };
}

function candidateSizingRecord(candidate: EventCandidate): object {
  return {
    sequence: Number.MAX_SAFE_INTEGER,
    id: candidate.id,
    project_id: candidate.projectId,
    actor: candidate.actor,
    session: candidate.session,
    ...(candidate.recipient ? { recipient_session: candidate.recipient } : {}),
    ...(candidate.replyTo ? { reply_to: candidate.replyTo } : {}),
    type: candidate.type,
    payload: JSON.parse(candidate.payloadJson) as unknown,
    worktree: candidate.worktree,
    ...(candidate.branch ? { branch: candidate.branch } : {}),
    ...(candidate.commit ? { commit: candidate.commit } : {}),
    idempotency_key: candidate.idempotencyKey,
    created_at: candidate.createdAt,
  };
}

function utf8Bytes(value: string): number {
  return new TextEncoder().encode(value).byteLength;
}

function requireMethod(request: Request, method: string): void {
  if (request.method !== method) throw new ApiError(405, 'method not allowed', 'method_not_allowed');
}

function json(value: unknown, status = 200): Response {
  return jsonText(JSON.stringify(value), status);
}

function jsonText(value: string, status = 200): Response {
  return new Response(value, { status, headers: JSON_HEADERS });
}

function problem(status: number, message: string, code: string): Response {
  return json({ ok: false, error: message, code }, status);
}

function integerParameter(url: URL, name: string, fallback: number, minimum: number, maximum: number): number {
  const values = url.searchParams.getAll(name);
  if (values.length === 0) return fallback;
  if (values.length !== 1 || !/^(?:0|[1-9]\d*)$/.test(values[0] ?? '')) {
    throw new ApiError(400, `${name} must be one integer`, 'invalid_query');
  }
  const value = Number(values[0]);
  if (!Number.isSafeInteger(value) || value < minimum || value > maximum) {
    throw new ApiError(400, `${name} must be between ${minimum} and ${maximum}`, 'invalid_query');
  }
  return value;
}

function stringParameter(url: URL, name: string): string {
  const values = url.searchParams.getAll(name);
  if (values.length === 0) return '';
  if (values.length !== 1 || values[0]?.trim() === '') {
    throw new ApiError(400, `${name} must be one non-empty string`, 'invalid_query');
  }
  return values[0]!;
}

function rejectUnknownQuery(url: URL, allowed: Set<string>): void {
  for (const name of url.searchParams.keys()) {
    if (!allowed.has(name)) throw new ApiError(400, `unknown query parameter ${JSON.stringify(name)}`, 'invalid_query');
  }
}

interface Cursor {
  v: 1;
  after: number;
  through: number;
  actor: string;
  type: string;
  session: string;
}

function encodeCursor(cursor: Cursor): string {
  const bytes = new TextEncoder().encode(JSON.stringify(cursor));
  let binary = '';
  for (let offset = 0; offset < bytes.length; offset += 0x8000) {
    binary += String.fromCharCode(...bytes.subarray(offset, offset + 0x8000));
  }
  return btoa(binary).replaceAll('+', '-').replaceAll('/', '_').replace(/=+$/, '');
}

function parseCursor(encoded: string | null): Cursor | null {
  if (encoded === null) return null;
  try {
    const base64 = encoded.replaceAll('-', '+').replaceAll('_', '/');
    const padded = base64 + '='.repeat((4 - base64.length % 4) % 4);
    const binary = atob(padded);
    const bytes = Uint8Array.from(binary, (character) => character.charCodeAt(0));
    const value = JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(bytes)) as Partial<Cursor>;
    const keys = value && typeof value === 'object' ? Object.keys(value).sort() : [];
    if (keys.join(',') !== 'actor,after,session,through,type,v' || value.v !== 1 ||
        !Number.isSafeInteger(value.after) || !Number.isSafeInteger(value.through) ||
        (value.after ?? -1) < 0 || (value.through ?? -1) < 0 ||
        typeof value.actor !== 'string' || typeof value.type !== 'string' || typeof value.session !== 'string') {
      throw new Error('invalid');
    }
    return {
      v: 1,
      after: value.after!,
      through: value.through!,
      actor: value.actor,
      type: value.type,
      session: value.session,
    };
  } catch {
    throw new ApiError(400, 'invalid cursor', 'invalid_cursor');
  }
}

async function readBody(request: Request): Promise<Uint8Array> {
  const reader = request.body?.getReader();
  if (!reader) return new Uint8Array();
  const chunks: Uint8Array[] = [];
  let length = 0;
  try {
    while (true) {
      const result = await reader.read();
      if (result.done) break;
      length += result.value.byteLength;
      if (length > MAX_EVENT_BYTES) {
        await reader.cancel();
        throw new RequestBodyTooLargeError();
      }
      chunks.push(result.value);
    }
  } catch (error) {
    if (isBodyTooLarge(error)) {
      throw new ApiError(413, 'JSON body exceeds 256 KiB', 'body_too_large');
    }
    throw error;
  }
  const bytes = new Uint8Array(length);
  let offset = 0;
  for (const chunk of chunks) {
    bytes.set(chunk, offset);
    offset += chunk.byteLength;
  }
  return bytes;
}

function isBodyTooLarge(error: unknown): boolean {
  let current: unknown = error;
  for (let depth = 0; depth < 5 && current instanceof Error; depth++) {
    if (current instanceof RequestBodyTooLargeError) return true;
    current = current.cause;
  }
  return false;
}
