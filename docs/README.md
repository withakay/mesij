# Mesij documentation

Documentation status as of **August 30, 2026**.

| Audience | Document | Status |
| --- | --- | --- |
| New users | [Getting started](getting-started.md) | Practical guide |
| Implementers and integration authors | [Coordination protocol](protocol.md) | Normative draft |
| Operators | [Operations](operations.md) | Practical guide |
| Contributors | [Architecture](architecture.md) | Internal design |
| Harness users | [Harness integrations](../integrations/README.md) | Installation and capability guide |
| Node/Workers users | [Standalone REST API](../api/README.md) | Implemented MVP |
| API contributors | [Full-parity Go REST API plan](rest-api-plan.md) | Future target |

## Which implementation should I use?

| Need | Choose |
| --- | --- |
| Full task/change/file coordination, projections, conflicts, inboxes, hooks, and TUI | Canonical Go CLI |
| Simple authenticated event exchange on Node.js or Cloudflare Workers/D1 | Standalone TypeScript API |
| Native coding-harness integration | A plugin/extension under `integrations/` plus the Go CLI |

The Go and standalone API databases are intentionally separate. Do not point the
standalone API at a canonical Go Mesij database.
