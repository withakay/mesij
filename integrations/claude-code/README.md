# Claude Code integration

This directory is a Claude Code plugin containing lifecycle hooks and the Mesij
skill. A current `mesij` binary with the `hook`, `inbox`, and `agents` commands
must be on `PATH`.

## Validate and load from a checkout

```sh
claude plugin validate integrations/claude-code
claude --plugin-dir integrations/claude-code
```

Inside Claude Code, inspect the plugin and hooks before trusting them. Restart
Claude after changing plugin files.

## Behavior

- `SessionStart` registers Claude's native session and persists Mesij shell
  variables through `CLAUDE_ENV_FILE`.
- `UserPromptSubmit` injects unread external inbox messages.
- `PreToolUse` checks `Write` and `Edit` paths.
- `Stop` blocks once when unread messages arrive.
- `MESIJ_HOOK_MODE=deny` turns advisory external overlaps into denials.

Bash-based file changes can bypass first-class file hooks. The skill remains the
source of coordination behavior.

## Update and remove

A `--plugin-dir` load uses files directly from the checkout: pull updates and
restart Claude. Remove the `--plugin-dir` argument to stop loading it. Permanent
installation requires publishing or configuring a Claude plugin marketplace;
this repository does not currently publish one.
