import assert from 'node:assert/strict';
import { request as httpRequest } from 'node:http';
import { once } from 'node:events';
import { mkdtempSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';
import { NodeSqliteStore } from '../src/adapters/node-sqlite.js';
import { generateToken, hashToken } from '../src/crypto.js';
import { createHandler } from '../src/handler.js';
import { createNodeServer, NODE_MAX_BODY_BYTES, NODE_MAX_HEADER_BYTES } from '../src/node-server.js';

test('Node server routes/authenticates before consuming bodies and enforces incremental limits', async () => {
  const path = join(mkdtempSync(join(tmpdir(), 'mesij-server-')), 'api.sqlite3');
  const store = new NodeSqliteStore(path);
  await store.initialize();
  const generated = generateToken((length) => new Uint8Array(length).fill(8));
  await store.createToken('p', generated.tokenId, await hashToken(generated.token), 'server', '2026-08-30T12:00:00.000Z');
  const server = createNodeServer(createHandler({ store, projectId: 'p' }));
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  const address = server.address();
  assert(address && typeof address === 'object');
  const port = address.port;

  assert.equal(server.requestTimeout, 15_000);
  assert.equal(server.headersTimeout, 10_000);
  assert.equal(server.keepAliveTimeout, 5_000);
  assert.equal(server.timeout, 30_000);
  assert.equal(server.maxRequestsPerSocket, 100);
  assert.equal(NODE_MAX_HEADER_BYTES, 16 * 1024);

  const unfinished = httpRequest({
    host: '127.0.0.1', port, method: 'POST', path: '/v1/events',
    headers: {
      'content-type': 'application/json',
      'content-length': NODE_MAX_BODY_BYTES,
      'x-message-api-token': 'not-a-token',
    },
  });
  const earlyResponse = new Promise<{ status: number; connection: string | undefined }>((resolve, reject) => {
    unfinished.on('response', (response) => {
      response.resume();
      resolve({ status: response.statusCode ?? 0, connection: response.headers.connection });
    });
    unfinished.on('error', reject);
  });
  unfinished.write('{');
  const early = await Promise.race([
    earlyResponse,
    new Promise<never>((_, reject) => setTimeout(() => reject(new Error('server buffered the unfinished body')), 1_000)),
  ]);
  assert.equal(early.status, 401);
  assert.equal(early.connection, 'close');
  unfinished.destroy();

  const oversized = await send(port, generated.token, Buffer.alloc(NODE_MAX_BODY_BYTES + 1, 0x20));
  assert.equal(oversized.status, 413);

  await new Promise<void>((resolve, reject) => server.close((error) => error ? reject(error) : resolve()));
  store.close();
});

function send(port: number, token: string, body: Buffer): Promise<{ status: number; body: string }> {
  return new Promise((resolve, reject) => {
    const request = httpRequest({
      host: '127.0.0.1', port, method: 'POST', path: '/v1/events',
      headers: {
        'content-type': 'application/json',
        'x-message-api-token': token,
      },
    }, (response) => {
      const chunks: Buffer[] = [];
      response.on('data', (chunk: Buffer) => chunks.push(chunk));
      response.on('end', () => resolve({ status: response.statusCode ?? 0, body: Buffer.concat(chunks).toString('utf8') }));
    });
    request.on('error', reject);
    request.end(body);
  });
}
