---
name: mesij
description: Coordinate task, change, and file work with concurrent agents using the Mesij tools and inbox.
---

# Mesij coordination protocol

The OpenCode plugin already registers a stable Mesij session. Never run
`mesij session` again. Prefer the plugin tools over shell commands.

1. Call `mesij_check` with every known task, change, phase, and file before work.
2. If another claim overlaps, coordinate or defer. Use `mesij_emit` with
   `event: "reply"`, an actor/session in `to`, and `reply_to` when available.
3. Announce planning using `mesij_emit` with `event: "plan"` and a stable key.
4. Move the same work identity into `event: "implement"` before editing. Include
   all likely files.
5. Post useful decisions and progress with stable idempotency keys.
6. Release every active claim with `event: "finish"` or `event: "defer"`.
7. Use `mesij_inbox` before merging or handing off, and respond to plugin-injected
   inbox messages promptly.

The plugin's edit hook is advisory unless `MESIJ_ENFORCE_CONFLICTS=1`. Shell
writes can bypass it, so this skill remains authoritative. Mesij recipient
routing is public project metadata, not confidentiality.
