import { canonicalJson } from './strict-json.js';
import { ApiError, type EventCandidate, type HandlerOptions } from './types.js';

const ALLOWED = new Set([
  'event', 'type', 'actor', 'session', 'to', 'reply_to', 'key', 'work', 'task',
  'change', 'phase', 'message', 'files', 'data',
]);
const EVENT_ALIASES: Record<string, string> = {
  plan: 'work.planned',
  implement: 'work.implementing',
  start: 'work.started',
  finish: 'work.finished',
  defer: 'work.deferred',
  reply: 'message.replied',
  post: 'message.posted',
};
const LIFECYCLE_TYPES: Record<string, { acceptedPhase?: string; payloadPhase?: string }> = {
  'work.planned': { acceptedPhase: 'plan', payloadPhase: 'plan' },
  'work.implementing': { acceptedPhase: 'implement', payloadPhase: 'implement' },
  // Legacy work.started accepts phase=implement but omits phase from stored payloads.
  'work.started': { acceptedPhase: 'implement' },
  'work.finished': {},
  'work.deferred': {},
};

type Input = Record<string, unknown>;

function nonempty(input: Input, field: string, required = false): string {
  const value = input[field];
  if (value === undefined && !required) return '';
  if (typeof value !== 'string' || value.trim() === '') {
    throw new ApiError(400, `${field} must be a non-empty string`, 'invalid_request');
  }
  return value;
}

export function buildCandidate(value: unknown, options: HandlerOptions): EventCandidate {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) {
    throw new ApiError(400, 'top-level JSON value must be an object', 'invalid_json');
  }
  const input = value as Input;
  for (const [field, fieldValue] of Object.entries(input)) {
    if (!ALLOWED.has(field)) throw new ApiError(400, `unknown field ${JSON.stringify(field)}`, 'invalid_request');
    if (field !== 'data' && fieldValue === null) throw new ApiError(400, `field ${JSON.stringify(field)} cannot be null`, 'invalid_request');
  }

  const event = nonempty(input, 'event');
  const explicitType = nonempty(input, 'type');
  if ((event === '') === (explicitType === '')) {
    throw new ApiError(400, 'use exactly one of event or type', 'invalid_request');
  }
  let type = explicitType;
  if (event !== '') {
    type = EVENT_ALIASES[event] ?? '';
    if (type === '') throw new ApiError(400, `unknown event ${JSON.stringify(event)}`, 'invalid_request');
  }
  const lifecycle = LIFECYCLE_TYPES[type];

  const actor = nonempty(input, 'actor', true);
  const session = nonempty(input, 'session', true);
  const recipient = nonempty(input, 'to');
  const replyTo = nonempty(input, 'reply_to');
  const key = nonempty(input, 'key') || randomId(options.randomBytes);
  const work = nonempty(input, 'work');
  const task = nonempty(input, 'task');
  const change = nonempty(input, 'change');
  const requestedPhase = nonempty(input, 'phase');
  const message = input.message === undefined ? '' : stringValue(input.message, 'message');

  let files: string[] = [];
  if (input.files !== undefined) {
    if (!Array.isArray(input.files) || input.files.some((file) => typeof file !== 'string' || file.trim() === '')) {
      throw new ApiError(400, 'files must be an array of non-empty strings', 'invalid_request');
    }
    files = input.files as string[];
  }

  if (lifecycle) {
    if (input.data !== undefined || recipient !== '' || replyTo !== '') {
      throw new ApiError(400, 'lifecycle JSON contains incompatible data or routing fields', 'invalid_request');
    }
    if (requestedPhase !== '' && requestedPhase !== lifecycle.acceptedPhase) {
      throw new ApiError(400, 'lifecycle JSON contains an incompatible phase', 'invalid_request');
    }
    if (work === '' && task === '' && change === '') {
      throw new ApiError(400, 'lifecycle events require work, task, or change', 'invalid_request');
    }
  }

  if (type === 'message.replied' && recipient === '') {
    throw new ApiError(400, 'message.replied requires a recipient in to', 'invalid_request');
  }

  const payload = Object.create(null) as Record<string, unknown>;
  if (work !== '') payload.work = work;
  if (task !== '') payload.task = task;
  if (change !== '') payload.change = change;
  const phase = lifecycle ? lifecycle.payloadPhase : requestedPhase;
  if (phase) payload.phase = phase;
  if (message !== '') payload.message = message;
  if (files.length > 0) payload.files = files;
  if (input.data !== undefined) payload.data = input.data;

  const source = options.source ?? {};
  return {
    id: randomId(options.randomBytes),
    projectId: options.projectId,
    actor,
    session,
    recipient,
    replyTo,
    type,
    payloadJson: canonicalJson(payload),
    worktree: source.worktree ?? '',
    branch: source.branch ?? '',
    commit: source.commit ?? '',
    idempotencyKey: key,
    createdAt: (options.now?.() ?? new Date()).toISOString(),
  };
}

function stringValue(value: unknown, field: string): string {
  if (typeof value !== 'string') throw new ApiError(400, `${field} must be a string`, 'invalid_request');
  return value;
}

function randomId(randomBytes: HandlerOptions['randomBytes']): string {
  const bytes = randomBytes?.(16) ?? crypto.getRandomValues(new Uint8Array(16));
  return Array.from(bytes, (byte) => byte.toString(16).padStart(2, '0')).join('');
}
