export interface EventRecord {
  sequence: number;
  id: string;
  project_id: string;
  actor: string;
  session: string;
  recipient_session?: string;
  reply_to?: string;
  type: string;
  payload: unknown;
  worktree: string;
  branch?: string;
  commit?: string;
  idempotency_key: string;
  created_at: string;
}

export interface EventCandidate {
  id: string;
  projectId: string;
  actor: string;
  session: string;
  recipient: string;
  replyTo: string;
  type: string;
  payloadJson: string;
  worktree: string;
  branch: string;
  commit: string;
  idempotencyKey: string;
  createdAt: string;
}

export interface EventFilters {
  after: number;
  through: number;
  limit: number;
  actor: string;
  type: string;
  session: string;
}

export interface TokenRecord {
  token_id: string;
  label: string;
  created_at: string;
  revoked_at?: string;
  reason?: string;
}

export interface Store {
  latestSequence(projectId: string): Promise<number>;
  listEvents(projectId: string, filters: EventFilters): Promise<EventRecord[]>;
  appendEvent(candidate: EventCandidate): Promise<{ event: EventRecord; inserted: boolean }>;
  activeTokenHash(projectId: string, tokenId: string): Promise<Uint8Array | null>;
}

export interface AdminStore extends Store {
  initialize(): Promise<void>;
  createToken(projectId: string, tokenId: string, tokenHash: Uint8Array, label: string, createdAt: string): Promise<void>;
  listTokens(projectId: string): Promise<TokenRecord[]>;
  revokeToken(projectId: string, tokenId: string, revokedAt: string, reason: string): Promise<boolean>;
  close(): void;
}

export interface HandlerOptions {
  store: Store;
  projectId: string;
  source?: {
    worktree?: string;
    branch?: string;
    commit?: string;
  };
  now?: () => Date;
  randomBytes?: (length: number) => Uint8Array;
}

export class ApiError extends Error {
  constructor(
    public readonly status: number,
    message: string,
    public readonly code: string,
  ) {
    super(message);
  }
}

export class IdempotencyConflictError extends Error {}
export class RequestBodyTooLargeError extends Error {}
