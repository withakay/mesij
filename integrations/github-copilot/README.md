# GitHub Copilot CLI integration

This directory is an Open Plugin Spec plugin for GitHub Copilot CLI. It bundles
Mesij hooks and a coordination skill.

GitHub Copilot CLI 1.0.74 or newer is required for Open Plugin Spec v1 support.
A current `mesij` binary must be installed on `PATH`.

## Try from this checkout

```sh
copilot --plugin-dir integrations/github-copilot plugin list
```

## Install from GitHub

```sh
copilot plugin install withakay/mesij:integrations/github-copilot
copilot plugin list
```

Review plugin hooks before trusting them. Installed plugin hooks execute with the
same local permissions as Copilot CLI.

## Behavior

The plugin:

- maps Copilot's stable session ID directly to a Mesij session;
- injects the exact session ID and initial coordination context on
  `SessionStart`;
- checks first-class `edit`, `write`, `create`, and `apply_patch` tools for external file
  overlaps;
- polls the Mesij inbox after tools;
- blocks `agentStop` once when unread external messages arrive, prompting Copilot
  to review and reply;
- contributes the `mesij` skill.

Overlap checks are advisory by default. Set `MESIJ_HOOK_MODE=deny` to deny known
external overlaps. Shell-based mutations may bypass first-class file hooks.

The shared `mesij hook` adapter keeps a bounded, atomic inbox cursor outside the
repository. Hook failures are fail-open at the Mesij adapter level; Copilot CLI
itself treats crashing command `preToolUse` hooks as deny, so the adapter always
returns success and writes diagnostics to stderr.
