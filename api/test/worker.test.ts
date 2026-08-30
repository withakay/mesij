import assert from 'node:assert/strict';
import test from 'node:test';
import type { D1Database } from '../src/adapters/d1-types.js';
import worker, { type Env } from '../src/worker.js';

const unusedDatabase = new Proxy({}, {
  get() { throw new Error('D1 must not be accessed for configuration errors or health checks'); },
}) as D1Database;

test('Worker fails closed for missing, empty, default, and placeholder project IDs', async () => {
  for (const projectId of [undefined, '', '   ', 'default', 'REPLACE_WITH_PROJECT_ID', 'change-me']) {
    const env = { DB: unusedDatabase, ...(projectId === undefined ? {} : { MESIJ_PROJECT_ID: projectId }) } as Env;
    const response = await worker.fetch(new Request('https://worker.test/healthz'), env);
    assert.equal(response.status, 503, String(projectId));
    assert.equal((await response.json() as { code: string }).code, 'configuration_error');
  }
});

test('Worker accepts a concrete project ID', async () => {
  const response = await worker.fetch(new Request('https://worker.test/healthz'), {
    DB: unusedDatabase,
    MESIJ_PROJECT_ID: 'project-production',
  });
  assert.equal(response.status, 200);
});
