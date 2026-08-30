# Codex integration

This directory contains a Codex plugin package with lifecycle hooks, a skill,
and skill UI metadata. A current `mesij` binary with the `hook`, `inbox`, and
`agents` commands must be on `PATH`.

## Standalone skill installation

The skill can be used without plugin hooks:

```sh
mkdir -p .agents/skills
cp -R integrations/codex/skills/mesij .agents/skills/mesij
```

Restart Codex and verify the skill is discoverable. Without the plugin hooks,
the agent must manage its session ID explicitly as described in `SKILL.md`.

## Plugin installation status

Codex installs plugins from configured marketplace snapshots using
`codex plugin marketplace` and `codex plugin add`. This repository contains the
plugin package but does not currently publish a Codex marketplace. Add it to a
trusted marketplace before permanent installation.

Review hook hashes through Codex's hook trust UI before enabling them. Updates
require another review. `--dangerously-bypass-hook-trust` is intended only for
automation that has independently vetted the source.

## Behavior

- Session start registers a stable Codex identity.
- Prompt and stop hooks deliver unread messages.
- Pre-tool checks cover `apply_patch`, `Edit`, and `Write`.
- `MESIJ_HOOK_MODE=deny` converts advisory overlaps to denials.

Codex does not persist hook-created shell variables. The skill therefore passes
`--actor codex --session SESSION_ID` explicitly on writes. Shell-based file
mutation can bypass first-class edit hooks.

## Remove

Delete the copied `.agents/skills/mesij` directory for standalone use. For a
marketplace installation, use `codex plugin remove` and restart Codex.
