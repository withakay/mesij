---
name: mesij
description: Coordinate task, change, and file work with other agents, and send progress or direct replies through mesij.
---

# Mesij coordination protocol

Use `mesij` whenever this repository may have concurrent agents.

1. Open a session once. If `MESIJ_SESSION` is absent, run:
   `eval "$(mesij session --actor "${MESIJ_ACTOR:-claude-code}")"`
2. Before starting, inspect likely conflicts by every known scope:
   `mesij check --session "$MESIJ_SESSION" --task TASK_ID --change CHANGE_ID --file PATH [--file PATH...]`
3. If another active task/change/file overlaps, prefer deferring or send its session a direct reply:
   `mesij reply --to SESSION --reply-to EVENT_ID --message "..."`
4. Announce planning before changing code:
   `mesij plan --task TASK_ID --change CHANGE_ID --file PATH --key "TASK_ID:plan" --message "intent"`
5. Move the same work identity into implementation before editing. Repeat
   `--file` for every likely path, or use `mesij emit` with a JSON `files` array:
   `mesij implement --task TASK_ID --change CHANGE_ID --file PATH [--file PATH...] --key "TASK_ID:implement" --message "approach"`
6. Post decisions or useful progress with a stable retry key:
   `mesij post --type progress.updated --task TASK_ID --key KEY --message "..."`
7. Release the claim when done or intentionally postponed:
   `mesij finish --task TASK_ID --key "TASK_ID:finish" --message "result"`
   or `mesij defer --task TASK_ID --key "TASK_ID:defer" --message "reason"`
8. Check messages again before merging.

`MESIJ_ACTOR`, `MESIJ_SESSION`, and optionally `MESIJ_PROJECT` should remain set
for the session. Events are public coordination records; `--to` identifies the
intended session but does not make a message secret.
