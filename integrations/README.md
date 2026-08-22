# Harness integrations

Mesij integration has three layers:

1. **Skill/instructions** teach the agent the coordination protocol.
2. **Hooks** surface relevant state at session start and before edits.
3. **Plugins/tools** can call the JSON CLI and provide a richer UX.

Keep the skill even when hooks are installed: a pre-edit hook can detect a path,
but only the agent understands its broader task, likely files, and when work is
actually finished.

## Claude Code

`claude-code/` is laid out as a plugin with:

- `.claude-plugin/plugin.json`
- `hooks/hooks.json`
- `hooks/session-start.sh`
- `hooks/before-edit.sh`
- `skills/mesij/SKILL.md`

The hooks require `mesij` and `jq` on `PATH`. They are advisory: session start
shows current coordination state and pre-edit checks show path overlaps. The
skill directs Claude to make and release explicit task claims.

## Codex

Copy `codex/skills/mesij` into the skills directory used by the Codex
installation, or copy its instructions into the repository's `AGENTS.md`.
Codex should open one session, retain `MESIJ_SESSION`, and follow the skill for
each task.

## Other harnesses

Source or adapt `generic/agent-start.sh`, then inject the mesij skill text into
the harness's system/project instructions. A plugin can consume:

```sh
mesij check --session "$MESIJ_SESSION" --after "$CURSOR" --json
```

Persist the greatest returned sequence as the next cursor. To integrate an edit
tool, call `check --file PATH --json` before the tool executes. Avoid silently
creating claims for every write: claims should describe a coherent task and all
likely affected paths.

## Environment

- `MESIJ_ACTOR` — readable agent/harness name.
- `MESIJ_SESSION` — unique ID for one agent run.
- `MESIJ_PROJECT` — logical project stream; optional.
- `MESIJ_DB` — shared SQLite path override; optional.

Direct messages are visible in the shared log. `recipient_session` is a routing
hint rather than a privacy boundary.
