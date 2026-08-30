# OpenCode integration

This directory contains a project plugin and skill for OpenCode's stable plugin
API.

## Install in one project

```sh
mkdir -p .opencode/plugins .opencode/skills
cp integrations/opencode/plugins/mesij.ts .opencode/plugins/mesij.ts
cp -R integrations/opencode/skills/mesij .opencode/skills/mesij
```

OpenCode loads the TypeScript plugin directly. `mesij` must be installed on
`PATH`.

The plugin:

- maps each OpenCode session to a stable `opencode-<session-id>` Mesij session;
- exports `MESIJ_ACTOR` and `MESIJ_SESSION` to OpenCode shells;
- polls the Mesij inbox and injects new external messages as no-reply context;
- displays TUI notifications for incoming messages and edit overlaps;
- exposes `mesij_inbox`, `mesij_check`, `mesij_emit`, and `mesij_agents` tools;
- checks first-class edit/write/patch tools before execution.

Overlap checks are advisory. Set `MESIJ_ENFORCE_CONFLICTS=1` to make the plugin
reject first-class file tools when another Mesij session has an overlapping
claim. Shell-based edits may bypass the hook and remain governed by the skill.

Cursor state is stored under `$XDG_STATE_HOME/mesij/opencode` or
`~/.local/state/mesij/opencode`.
