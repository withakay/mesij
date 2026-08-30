---
name: mesij
description: Coordinate task, change, and file work with concurrent agents; receive Mesij inbox messages and send direct replies.
---

# Mesij coordination protocol

The Codex hooks register a stable Mesij session from the Codex session identity,
but Codex does not currently persist hook-created shell variables. Resolve the
session independently in each shell invocation:

```sh
MESIJ_SESSION="${CODEX_SESSION_ID:-${CODEX_THREAD_ID:-}}"
```

If neither Codex identity is available, use the exact session ID reported by the
SessionStart hook. Never prefer an unrelated inherited `MESIJ_SESSION`, and do
not create a second session for the same run. Include `--actor codex --session
"$MESIJ_SESSION"` on every Mesij write command; do not assume assignments persist
between tool calls.

1. Check every known task/change/file scope before work:
   `mesij check --session "$MESIJ_SESSION" --task TASK_ID --change CHANGE_ID --file PATH [--file PATH...]`
2. If another claim overlaps, coordinate or defer. Reply by actor/session and,
   when available, event ID:
   `mesij reply --actor codex --session "$MESIJ_SESSION" --to ACTOR_OR_SESSION --reply-to EVENT_ID --key KEY --message "..."`
3. Announce planning with `mesij plan --actor codex --session
   "$MESIJ_SESSION" ...`, then move the same work identity into `mesij implement
   --actor codex --session "$MESIJ_SESSION" ...` before editing. Repeat `--file`
   for all likely paths.
4. Post decisions and progress using stable idempotency keys.
5. Release every claim with `mesij finish` or `mesij defer`; never infer
   completion merely because Codex is stopping.
6. Review hook-injected inbox messages and check the inbox before merging or
   handing off:
   `mesij inbox --session "$MESIJ_SESSION" --json`

The pre-edit hook checks `apply_patch`, `Edit`, and `Write` paths. It is advisory
by default; set `MESIJ_HOOK_MODE=deny` to reject known external overlaps.
Specialized or shell-based writes may bypass hooks and remain governed by this
skill.

Mesij messages are project-visible coordination records. Recipient routing is
not a confidentiality boundary.
