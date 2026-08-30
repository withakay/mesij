---
name: mesij
description: Coordinate task, change, and file work with concurrent agents; receive Mesij inbox messages and send direct replies.
---

# Mesij coordination protocol

The GitHub Copilot CLI plugin registers a stable Mesij session at session start.
The initial hook context reports its exact session ID. Retain that value and do
not create another session. Copilot hook-created shell variables do not persist,
so include `--actor github-copilot --session SESSION_ID` on every Mesij write.

1. Check all known task/change/file scopes before work:
   `mesij check --session SESSION_ID --task TASK_ID --change CHANGE_ID --file PATH [--file PATH...]`
2. Coordinate or defer when another claim overlaps. Reply by actor/session and,
   when available, event ID:

   ```sh
   mesij reply --actor github-copilot --session SESSION_ID \
     --to ACTOR_OR_SESSION --reply-to EVENT_ID --key KEY --message "..."
   ```

3. Announce planning, then implementation, using the same work identity and
   stable retry keys:
   `mesij plan --actor github-copilot --session SESSION_ID --task TASK_ID --file PATH --key "TASK_ID:plan"`
   `mesij implement --actor github-copilot --session SESSION_ID --task TASK_ID --file PATH --key "TASK_ID:implement"`
4. Include every likely file. Post useful decisions and progress.
5. Finish or defer every active claim explicitly. An agent turn or session ending
   does not prove work completion.
6. Review hook-injected inbox messages and run
   `mesij inbox --session SESSION_ID --json` before merge or handoff.

The pre-tool hook checks Copilot's first-class edit/write/patch tools. It is
advisory by default; set `MESIJ_HOOK_MODE=deny` to deny known external overlaps.
Shell-based edits can bypass that check and remain governed by this skill.

Mesij messages are project-visible coordination records. Recipient routing is
not a confidentiality boundary.
