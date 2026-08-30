---
name: mesij
description: Coordinate task, change, and file work with concurrent agents; receive Mesij inbox messages and send direct replies.
---

# Mesij coordination protocol

The Claude Code plugin registers a stable session and injects `MESIJ_ACTOR` and
`MESIJ_SESSION` into subsequent Bash commands. Do **not** create another session.
If those variables are unexpectedly absent, pass the session ID from the
SessionStart context explicitly to every Mesij command.

1. Check all known scopes before starting:
   `mesij check --session "$MESIJ_SESSION" --task TASK_ID --change CHANGE_ID --file PATH [--file PATH...]`
2. If another claim overlaps, coordinate or defer. Reply by actor/session and,
   when available, event ID:
   `mesij reply --to ACTOR_OR_SESSION --reply-to EVENT_ID --key KEY --message "..."`
3. Announce planning before changing code:
   `mesij plan --task TASK_ID --change CHANGE_ID --file PATH [--file PATH...] --key "TASK_ID:plan" --message "intent"`
4. Move the same work identity into implementation before editing:
   `mesij implement --task TASK_ID --change CHANGE_ID --file PATH [--file PATH...] --key "TASK_ID:implement" --message "approach"`
5. Post useful decisions or progress with stable retry keys.
6. Release every claim with `finish` or `defer`; never infer completion merely
   because the session is stopping.
7. Review injected inbox messages, reply when needed, and run
   `mesij inbox --session "$MESIJ_SESSION" --json` before merging or handoff.

The pre-edit hook is advisory by default and only observes Claude's first-class
`Write` and `Edit` tools; Bash-based file mutation remains governed by this
skill. Set `MESIJ_HOOK_MODE=deny` to block first-class edits with known external
overlaps.

Mesij messages are project-visible coordination records. Recipient routing is
not a confidentiality boundary.
