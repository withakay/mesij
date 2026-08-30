import assert from 'node:assert/strict';
import test from 'node:test';
import { generateToken, hashToken } from '../src/crypto.js';
import { createHandler } from '../src/handler.js';
import { MAX_EVENT_BYTES, MAX_EVENTS_PAGE_LIMIT, MAX_EVENTS_RESPONSE_BYTES } from '../src/limits.js';
import { IdempotencyConflictError, type EventCandidate, type EventFilters, type EventRecord, type Store } from '../src/types.js';
import { sameCandidate } from '../src/adapters/sql.js';

class MemoryStore implements Store {
  events: EventRecord[] = [];
  tokens = new Map<string, Uint8Array>();

  async latestSequence(projectId: string): Promise<number> {
    return this.events.filter((event) => event.project_id === projectId).at(-1)?.sequence ?? 0;
  }

  async listEvents(projectId: string, filters: EventFilters): Promise<EventRecord[]> {
    return this.events.filter((event) => event.project_id === projectId && event.sequence > filters.after && event.sequence <= filters.through)
      .filter((event) => !filters.actor || event.actor === filters.actor)
      .filter((event) => !filters.type || event.type === filters.type)
      .filter((event) => !filters.session || event.session === filters.session)
      .slice(0, filters.limit);
  }

  async appendEvent(candidate: EventCandidate): Promise<{ event: EventRecord; inserted: boolean }> {
    const existing = this.events.find((event) => event.project_id === candidate.projectId && event.session === candidate.session && event.idempotency_key === candidate.idempotencyKey);
    if (existing) {
      if (!sameCandidate(existing, candidate)) throw new IdempotencyConflictError();
      return { event: existing, inserted: false };
    }
    const event: EventRecord = {
      sequence: this.events.length + 1,
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
    this.events.push(event);
    return { event, inserted: true };
  }

  async activeTokenHash(projectId: string, tokenId: string): Promise<Uint8Array | null> {
    return this.tokens.get(`${projectId}:${tokenId}`) ?? null;
  }
}

async function fixture(): Promise<{ handler: (request: Request) => Promise<Response>; store: MemoryStore; token: string }> {
  const store = new MemoryStore();
  const generated = generateToken((length) => new Uint8Array(length).fill(length));
  store.tokens.set(`project:${generated.tokenId}`, await hashToken(generated.token));
  let random = 0;
  const handler = createHandler({
    store,
    projectId: 'project',
    now: () => new Date('2026-08-30T12:00:00.000Z'),
    randomBytes: (length) => new Uint8Array(length).fill(++random),
  });
  return { handler, store, token: generated.token };
}

function authenticated(token: string, path: string, init: RequestInit = {}): Request {
  const headers = new Headers(init.headers);
  headers.set('x-message-api-token', token);
  return new Request(`https://example.test${path}`, { ...init, headers });
}

test('health is public while v1 requires the exact token header', async () => {
  const { handler, token } = await fixture();
  assert.equal((await handler(new Request('https://example.test/healthz'))).status, 200);
  assert.equal((await handler(new Request('https://example.test/v1/status'))).status, 401);
  assert.equal((await handler(new Request('https://example.test/v1/status', { headers: { authorization: `Bearer ${token}` } }))).status, 401);
  const response = await handler(authenticated(token, '/v1/status'));
  assert.equal(response.status, 200);
  assert.deepEqual(await response.json(), {
    ok: true, api_version: 'v1', project_id: 'project', latest_sequence: 0, projections_supported: false,
  });
});

test('POST events is strict, program-times events, and rejects duplicate fields', async () => {
  const { handler, token } = await fixture();
  const response = await handler(authenticated(token, '/v1/events', {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ event: 'plan', actor: 'agent-a', session: 'session-a', task: 'task-1', files: ['a.ts', 'b.ts'], key: 'plan-1' }),
  }));
  assert.equal(response.status, 201);
  const event = await response.json() as Record<string, unknown>;
  assert.equal(event.created_at, '2026-08-30T12:00:00.000Z');
  assert.equal(event.type, 'work.planned');
  assert.deepEqual(event.payload, { files: ['a.ts', 'b.ts'], phase: 'plan', task: 'task-1' });

  for (const body of [
    '{"event":"post","event":"plan","actor":"a","session":"s"}',
    '{"event":"post","actor":"a","session":"s","created_at":"client"}',
    '{"event":"post","actor":"a","session":"s"} true',
  ]) {
    const invalid = await handler(authenticated(token, '/v1/events', { method: 'POST', headers: { 'content-type': 'application/json' }, body }));
    assert.equal(invalid.status, 400, body);
  }
});

test('POST events enforces the portable 256 KiB request/event budget', async () => {
  const { handler, token } = await fixture();
  const body = JSON.stringify({
    event: 'post', actor: 'a', session: 's', data: 'x'.repeat(MAX_EVENT_BYTES),
  });
  const response = await handler(authenticated(token, '/v1/events', {
    method: 'POST', headers: { 'content-type': 'application/json' }, body,
  }));
  assert.equal(response.status, 413);
  assert.equal((await response.json() as { code: string }).code, 'body_too_large');

  const base = { event: 'post', actor: 'a', session: 's', data: '' };
  const baseBytes = new TextEncoder().encode(JSON.stringify(base)).byteLength;
  const nearLimitBody = JSON.stringify({ ...base, data: 'x'.repeat(MAX_EVENT_BYTES - baseBytes - 1) });
  assert(new TextEncoder().encode(nearLimitBody).byteLength <= MAX_EVENT_BYTES);
  const oversizedEvent = await handler(authenticated(token, '/v1/events', {
    method: 'POST', headers: { 'content-type': 'application/json' }, body: nearLimitBody,
  }));
  assert.equal(oversizedEvent.status, 413);
  assert.equal((await oversizedEvent.json() as { code: string }).code, 'event_too_large');
});

test('resolved event types enforce lifecycle and reply rules', async () => {
  const { handler, token } = await fixture();
  const send = (body: unknown) => handler(authenticated(token, '/v1/events', {
    method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify(body),
  }));

  assert.equal((await send({ type: 'work.planned', actor: 'a', session: 's', task: 't', data: true })).status, 400);
  assert.equal((await send({ type: 'work.finished', actor: 'a', session: 's' })).status, 400);
  assert.equal((await send({ type: 'message.replied', actor: 'a', session: 's', reply_to: 'event-id' })).status, 400);
  assert.equal((await send({ event: 'reply', actor: 'a', session: 's', to: 'recipient', key: 'reply' })).status, 201);

  const started = await send({ type: 'work.started', actor: 'a', session: 's', task: 't', phase: 'implement', key: 'start' });
  assert.equal(started.status, 201);
  assert.deepEqual((await started.json() as { payload: unknown }).payload, { task: 't' });
});

test('idempotent replay returns 200 and changed reuse returns 409', async () => {
  const { handler, token } = await fixture();
  const send = (message: string) => handler(authenticated(token, '/v1/events', {
    method: 'POST', headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ event: 'post', actor: 'a', session: 's', key: 'same', message }),
  }));
  assert.equal((await send('one')).status, 201);
  const replay = await send('one');
  assert.equal(replay.status, 200);
  assert.equal((await replay.json() as { inserted: boolean }).inserted, false);
  assert.equal((await send('two')).status, 409);
});

test('GET events uses a bounded opaque cursor and filters', async () => {
  const { handler, token } = await fixture();
  for (let index = 1; index <= 4; index++) {
    const response = await handler(authenticated(token, '/v1/events', {
      method: 'POST', headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ type: index % 2 ? 'odd' : 'even', actor: index < 4 ? 'a' : 'b', session: 's', key: `k${index}` }),
    }));
    assert.equal(response.status, 201);
  }
  const first = await handler(authenticated(token, '/v1/events?limit=1&actor=a'));
  const page1 = await first.json() as { through: number; has_more: boolean; next_cursor: string; events: EventRecord[] };
  assert.equal(page1.through, 4);
  assert.equal(page1.has_more, true);
  assert.equal(page1.events[0]?.sequence, 1);

  const second = await handler(authenticated(token, `/v1/events?limit=1&actor=a&cursor=${encodeURIComponent(page1.next_cursor)}`));
  const page2 = await second.json() as { through: number; events: EventRecord[] };
  assert.equal(page2.through, 4);
  assert.equal(page2.events[0]?.sequence, 2);
  const changedFilters = await handler(authenticated(token, `/v1/events?limit=1&actor=b&cursor=${encodeURIComponent(page1.next_cursor)}`));
  assert.equal(changedFilters.status, 400);
  const future = encodeCursorForTest({ v: 1, after: 0, through: 5, actor: '', type: '', session: '' });
  assert.equal((await handler(authenticated(token, `/v1/events?cursor=${future}`))).status, 400);
  assert.equal((await handler(authenticated(token, `/v1/events?cursor=${future}&cursor=${future}`))).status, 400);
  assert.equal((await handler(authenticated(token, '/v1/events?unknown=x'))).status, 400);
});

test('GET event cursors round-trip UTF-8 filters', async () => {
  const { handler, token } = await fixture();
  const actor = 'équipe 🚀';
  const type = '消息.发布';
  const session = 'sessión-猫';
  for (let index = 1; index <= 2; index++) {
    const response = await handler(authenticated(token, '/v1/events', {
      method: 'POST', headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ type, actor, session, key: `unicode-${index}` }),
    }));
    assert.equal(response.status, 201);
  }

  const filters = new URLSearchParams({ limit: '1', actor, type, session });
  const first = await handler(authenticated(token, `/v1/events?${filters}`));
  assert.equal(first.status, 200);
  const page1 = await first.json() as { next_cursor: string; events: EventRecord[] };
  assert.equal(page1.events[0]?.actor, actor);

  filters.set('cursor', page1.next_cursor);
  const second = await handler(authenticated(token, `/v1/events?${filters}`));
  assert.equal(second.status, 200);
  const page2 = await second.json() as { events: EventRecord[] };
  assert.equal(page2.events[0]?.sequence, 2);
});

test('GET events caps limit and paginates within the cumulative JSON response budget', async () => {
  const { handler, store, token } = await fixture();
  for (let sequence = 1; sequence <= 24; sequence++) {
    store.events.push({
      sequence,
      id: `large-${sequence}`,
      project_id: 'project',
      actor: 'large',
      session: 'session',
      type: 'message.posted',
      payload: { data: 'x'.repeat(240 * 1024) },
      worktree: '',
      idempotency_key: `large-${sequence}`,
      created_at: '2026-08-30T12:00:00.000Z',
    });
  }

  assert.equal((await handler(authenticated(token, `/v1/events?limit=${MAX_EVENTS_PAGE_LIMIT + 1}`))).status, 400);

  const sequences: number[] = [];
  let cursor = '';
  let pages = 0;
  do {
    const path = `/v1/events?limit=${MAX_EVENTS_PAGE_LIMIT}${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ''}`;
    const response = await handler(authenticated(token, path));
    assert.equal(response.status, 200);
    const text = await response.text();
    assert(new TextEncoder().encode(text).byteLength <= MAX_EVENTS_RESPONSE_BYTES);
    const page = JSON.parse(text) as { through: number; has_more: boolean; next_cursor: string | null; events: EventRecord[] };
    assert.equal(page.through, 24);
    sequences.push(...page.events.map((event) => event.sequence));
    cursor = page.next_cursor ?? '';
    pages++;
    if (!page.has_more) break;
  } while (pages < 10);

  assert(pages > 1);
  assert.deepEqual(sequences, Array.from({ length: 24 }, (_, index) => index + 1));
});

function encodeCursorForTest(value: object): string {
  return Buffer.from(JSON.stringify(value)).toString('base64url');
}
