---
name: mesij
description: Coordinate task, change, and file work with concurrent agents using the Mesij tools and inbox.
---

# Mesij coordination protocol

The Pi extension already registers a stable Mesij session. Never create another
session. Prefer `mesij_check`, `mesij_emit`, `mesij_inbox`, and `mesij_agents`.

1. Check all known task/change/file scopes before work.
2. Coordinate or defer when another active claim overlaps.
3. Announce planning, then move the same work identity into implementation.
4. Include every likely file and use stable idempotency keys.
5. Reply to incoming messages using actor/session routing and `reply_to` when
   available.
6. Finish or defer every active claim explicitly; session shutdown is not
   completion.
7. Run `mesij_inbox` or `/mesij-inbox` before merging and handoff.

Mesij messages are project-visible coordination records. Recipient routing is
not a confidentiality boundary.
