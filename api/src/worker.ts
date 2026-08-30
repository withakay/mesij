import { D1Store } from './adapters/d1.js';
import type { D1Database } from './adapters/d1-types.js';
import { createHandler } from './handler.js';

export interface Env {
  DB: D1Database;
  MESIJ_PROJECT_ID?: string;
}

export default {
  fetch(request: Request, env: Env): Promise<Response> {
    const projectId = configuredProjectId(env.MESIJ_PROJECT_ID);
    if (!projectId) {
      return Promise.resolve(new Response(JSON.stringify({
        ok: false,
        error: 'worker project is not configured',
        code: 'configuration_error',
      }), {
        status: 503,
        headers: { 'content-type': 'application/json; charset=utf-8', 'cache-control': 'no-store' },
      }));
    }
    return createHandler({ store: new D1Store(env.DB), projectId })(request);
  },
};

function configuredProjectId(value: string | undefined): string | null {
  const projectId = value?.trim() ?? '';
  if (projectId === '' || /^(?:default|replace(?:_|-|\s)|change[_-]?me|your[_-]?project)/i.test(projectId)) return null;
  return projectId;
}
