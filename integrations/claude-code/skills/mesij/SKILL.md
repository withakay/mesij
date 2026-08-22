---
name: mesij
description: Coordinate work with other agents before editing files, and send progress or direct replies through mesij.
---

# Mesij coordination protocol

Use `mesij` whenever this repository may have concurrent agents.

1. Open a session once. If `MESIJ_SESSION` is absent, run:
   `eval "$(mesij session --actor "${MESIJ_ACTOR:-claude-code}")"`
2. Before starting a task, inspect likely conflicts:
   `mesij check --session "$MESIJ_SESSION" --file PATH [--file PATH...]`
3. If another active task overlaps, prefer deferring or send its session a direct reply:
   `mesij reply --to SESSION --reply-to EVENT_ID --message "..."`
4. Claim the work before editing:
   `mesij start --task TASK_ID --file PATH [--file PATH...] --key "TASK_ID:start" --message "intent"`
5. Post decisions or useful progress with a stable retry key:
   `mesij post --type progress.updated --task TASK_ID --key KEY --message "..."`
6. Release the claim when done or intentionally postponed:
   `mesij finish --task TASK_ID --key "TASK_ID:finish" --message "result"`
   or `mesij defer --task TASK_ID --key "TASK_ID:defer" --message "reason"`
7. Check messages again before merging.

`MESIJ_ACTOR`, `MESIJ_SESSION`, and optionally `MESIJ_PROJECT` should remain set
for the session. Events are public coordination records; `--to` identifies the
intended session but does not make a message secret.
