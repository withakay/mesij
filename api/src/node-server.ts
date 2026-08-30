import { createServer, type IncomingMessage, type Server, type ServerResponse } from 'node:http';
import { MAX_EVENT_BYTES } from './limits.js';
import { RequestBodyTooLargeError } from './types.js';

export const NODE_MAX_BODY_BYTES = MAX_EVENT_BYTES;
export const NODE_MAX_HEADER_BYTES = 16 * 1024;

export function createNodeServer(handler: (request: Request) => Promise<Response>): Server {
  const server = createServer({
    maxHeaderSize: NODE_MAX_HEADER_BYTES,
    requestTimeout: 15_000,
    headersTimeout: 10_000,
    keepAliveTimeout: 5_000,
  }, (request, response) => void serveRequest(handler, request, response));
  server.maxRequestsPerSocket = 100;
  server.setTimeout(30_000, (socket) => socket.destroy());
  return server;
}

async function serveRequest(
  handler: (request: Request) => Promise<Response>,
  incoming: IncomingMessage,
  outgoing: ServerResponse,
): Promise<void> {
  try {
    const headers = new Headers();
    for (let index = 0; index < incoming.rawHeaders.length; index += 2) {
      headers.append(incoming.rawHeaders[index]!, incoming.rawHeaders[index + 1]!);
    }
    const method = incoming.method ?? 'GET';
    const hasBody = method !== 'GET' && method !== 'HEAD';
    const init: RequestInit & { duplex?: 'half' } = {
      method,
      headers,
      ...(hasBody ? { body: limitedRequestBody(incoming, NODE_MAX_BODY_BYTES), duplex: 'half' as const } : {}),
    };
    const host = headers.get('host') ?? 'localhost';
    const request = new Request(`http://${host}${incoming.url ?? '/'}`, init);
    const response = await handler(request);
    const requestBodyUnread = !incoming.complete;
    const responseHeaders = Object.fromEntries(response.headers.entries());
    if (requestBodyUnread) {
      outgoing.shouldKeepAlive = false;
      responseHeaders.connection = 'close';
    }
    outgoing.writeHead(response.status, responseHeaders);
    const responseBody = Buffer.from(await response.arrayBuffer());
    outgoing.end(responseBody, () => {
      if (requestBodyUnread) incoming.destroy();
    });
  } catch (error) {
    const tooLarge = isBodyTooLarge(error);
    outgoing.shouldKeepAlive = false;
    outgoing.writeHead(tooLarge ? 413 : 500, { 'content-type': 'application/json; charset=utf-8', connection: 'close' });
    outgoing.end(JSON.stringify({
      ok: false,
      error: tooLarge ? 'JSON body exceeds 256 KiB' : 'internal server error',
      code: tooLarge ? 'body_too_large' : 'internal_error',
    }), () => incoming.destroy());
  }
}

function limitedRequestBody(incoming: IncomingMessage, maximum: number): ReadableStream<Uint8Array> {
  const iterator = incoming[Symbol.asyncIterator]();
  let length = 0;
  return new ReadableStream<Uint8Array>({
    async pull(controller) {
      try {
        const result = await iterator.next();
        if (result.done) {
          controller.close();
          return;
        }
        const chunk = Buffer.isBuffer(result.value) ? result.value : Buffer.from(result.value as Uint8Array);
        length += chunk.byteLength;
        if (length > maximum) {
          const error = new RequestBodyTooLargeError();
          incoming.pause();
          controller.error(error);
          return;
        }
        controller.enqueue(new Uint8Array(chunk.buffer, chunk.byteOffset, chunk.byteLength));
      } catch (error) {
        controller.error(error);
      }
    },
    async cancel() {
      await iterator.return?.();
      incoming.destroy();
    },
  });
}

function isBodyTooLarge(error: unknown): boolean {
  let current: unknown = error;
  for (let depth = 0; depth < 5 && current instanceof Error; depth++) {
    if (current instanceof RequestBodyTooLargeError) return true;
    current = current.cause;
  }
  return false;
}
