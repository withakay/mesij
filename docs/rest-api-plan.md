# REST API plan

> **Status (August 30, 2026):** [`api/`](../api/README.md) now provides a
> standalone Node.js 24+/Cloudflare Workers D1 MVP for health, authenticated
> event writes, and cursor-based event reads. It intentionally uses its own
> database. The plan below describes the later full-parity API that can share
> Mesij's service semantics and projections.

## Goal

Add an opt-in HTTP API over Mesij's existing project database without changing
the immutable event model. Clients authenticate with one program-generated token
in this exact header:

```http
x-message-api-token: mesij_v1.<token-id>.<secret>
```

Tokens are created and revoked through the local CLI. Token administration is
not exposed over HTTP in the first version.

## MVP contract

The server defaults to `127.0.0.1:7337`. All `/v1/*` routes require exactly one
non-empty `x-message-api-token` header. `/healthz` is unauthenticated and reveals
no project or database information.

| Method and path | Behavior |
| --- | --- |
| `GET /healthz` | Process liveness |
| `GET /v1/status` | Project identity and latest sequence |
| `POST /v1/events` | Strict JSON equivalent of `mesij emit` |
| `GET /v1/events` | Snapshot cursor pagination and filters |
| `GET /v1/events/stream` | NDJSON equivalent of `mesij tail --follow` |
| `GET /v1/check` | Active-work and conflict report |
| `GET /v1/agents` | Known actors and sessions |
| `GET /v1/inbox/{session}` | Session inbox projection |

`POST /v1/events` returns `201 Created` for a new event and `200 OK` for an
identical idempotent replay. Event timestamps remain server-generated; input
cannot provide or override `created_at`.

List responses use a bounded snapshot:

```json
{
  "through": 120,
  "next_after": 75,
  "has_more": true,
  "events": []
}
```

The first request captures `through`; subsequent pages reuse it. When a filtered
page is quiet, `next_after` can advance to `through` without skipping later
events.

## Token model

Bump the SQLite schema from version 3 to version 4 and add credential facts
outside the public event stream:

```sql
CREATE TABLE api_tokens (
    project_id TEXT NOT NULL,
    token_id   TEXT NOT NULL,
    token_hash BLOB NOT NULL CHECK(length(token_hash) = 32),
    label      TEXT NOT NULL,
    created_at TEXT NOT NULL,
    PRIMARY KEY (project_id, token_id)
) WITHOUT ROWID;

CREATE TABLE api_token_revocations (
    project_id TEXT NOT NULL,
    token_id   TEXT NOT NULL,
    revoked_at TEXT NOT NULL,
    reason     TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (project_id, token_id),
    FOREIGN KEY (project_id, token_id)
        REFERENCES api_tokens(project_id, token_id)
) WITHOUT ROWID;
```

Add update/delete rejection triggers. Creation and revocation are append-only;
an active token is one without a matching revocation row.

Token format:

```text
mesij_v1.<16-byte-random-id-base64url>.<32-byte-random-secret-base64url>
```

Generate both components with `crypto/rand`. The dot separator is outside the
base64url alphabet, so parsing is unambiguous. Store only `SHA-256(full token)`
and the public token ID. Since the secret is uniformly random rather than a
human password, a fast cryptographic hash is appropriate. Authentication parses
the ID for indexed lookup and compares hashes with
`subtle.ConstantTimeCompare`. Never log the header or plaintext token.

Tokens are scoped to the selected Mesij project and initially grant full read
and write access. Missing, malformed, invalid, and revoked tokens all return the
same generic `401 Unauthorized` response.

## CLI

Add:

```text
mesij token create --label NAME [--json]
mesij token list [--json]
mesij token revoke TOKEN_ID [--reason TEXT] [--json]
mesij serve [--listen 127.0.0.1:7337]
```

`token create` prints the plaintext token exactly once. JSON output contains
`token`, `token_id`, `label`, and program-set `created_at`. Listing never exposes
the token or hash. Revocation is idempotent.

## Internal structure

1. Extract event validation and coordination operations from CLI flag handlers
   into `internal/service`.
2. Have both CLI and HTTP handlers call the service directly; never invoke the
   CLI through subprocesses or stdout parsing.
3. Add `internal/httpapi` using `net/http` and `httptest`.
4. Open one `store.DB` for the server lifetime. Keep transactions short and
   release the database connection between stream polls.
5. Add a transactional coordination-report read so `through`, messages,
   projection errors, and active work come from one SQLite snapshot.

Server defaults:

- loopback binding;
- no CORS;
- 4 MiB JSON body limit, matching `emit`;
- header, idle, and maximum-header-size limits;
- graceful `SIGINT`/`SIGTERM` shutdown;
- request ID, method, path, status, and duration logging only;
- TLS required at a reverse proxy before any non-loopback deployment.

## Error mapping

| Status | Meaning |
| --- | --- |
| `400` | Invalid query or strict JSON input |
| `401` | Missing or invalid API token |
| `404` | Unknown recipient, reply target, or resource |
| `409` | Idempotency or ambiguous identity conflict |
| `405` | Unsupported method |
| `500` | Internal failure |

## Delivery sequence

1. **Schema and token CLI:** migration, generation, listing, revocation, and
   project-isolation tests.
2. **Shared service:** move CLI behavior behind a service with parity tests.
3. **Read-only HTTP:** health, status, events, check, agents, and inbox.
4. **Writes and streaming:** `POST /v1/events` and cancellable NDJSON streams.
5. **Operational hardening:** concurrency/race tests, reverse-proxy guidance,
   token rotation, backup, and recovery documentation.

The prior CLI remains usable if the server is stopped or rolled back; the added
append-only token tables can safely remain in the database.

## Required tests

- v3-to-v4 migration and project isolation;
- no plaintext token or hash leakage from list/status/log output;
- immutable token and revocation rows;
- create/list/revoke CLI behavior and idempotent revocation;
- exact `x-message-api-token` enforcement;
- strict JSON, body limits, and status mapping;
- snapshot pagination with filters and quiet pages;
- equivalent results through CLI `emit` and `POST /v1/events`;
- parallel API/CLI writers, same-key retries, stream cancellation, and
  `go test -race ./...`.
