# Herdr integration

This directory is a first-class Herdr plugin for viewing Mesij coordination from
Herdr workspaces and receiving notifications when focused agents have unread
messages.

Requirements:

- Herdr 0.7.4 or newer;
- Node.js 18 or newer;
- a current `mesij` binary on `PATH`;
- the relevant Mesij harness plugin/hook so Herdr's native agent session maps to
  the session registered in Mesij.

## Local development

From the Mesij repository root:

```sh
herdr plugin link "$PWD/integrations/herdr"
herdr plugin action list --plugin withakay.mesij
```

Unlink without deleting source files:

```sh
herdr plugin unlink withakay.mesij
```

## Install from GitHub

If Git credentials can clone the Mesij repository:

```sh
herdr plugin install withakay/mesij/integrations/herdr
herdr plugin action list --plugin withakay.mesij
```

Reinstall the same source to update a managed plugin. Remove it with:

```sh
herdr plugin uninstall withakay.mesij
```

## Actions

| Action | Behavior |
| --- | --- |
| `withakay.mesij.tui` | Open the Mesij TUI in a large popup |
| `withakay.mesij.check` | Show active work, conflicts, and recent messages |
| `withakay.mesij.inbox` | Show the focused agent's mapped Mesij inbox |
| `withakay.mesij.agents` | List registered Mesij actors and sessions |
| `withakay.mesij.tail` | Follow JSONL events in a Herdr tab |

Invoke an action directly:

```sh
herdr plugin action invoke withakay.mesij.tui
```

Actions use the focused pane/workspace directory so Mesij resolves the same
project as the agent.

## Notifications

The plugin listens for `pane.agent_status_changed`. When an agent becomes idle,
done, or blocked, it:

1. reads the pane's native Herdr agent-session reference;
2. maps OpenCode IDs to `opencode-<native-id>` and reads Pi's native ID from
   the reported session-file header before mapping it to `pi-<native-id>`; other
   supported harness IDs are used directly;
3. checks that Mesij session's inbox from the pane's working directory;
4. shows a Herdr notification for new external messages;
5. persists a per-project/session cursor under `HERDR_PLUGIN_STATE_DIR`.

Notifications are best effort and never alter or acknowledge Mesij events. If a
pane has no native session reference, the inbox action explains that the agent's
Herdr integration and Mesij harness integration are both required.

## Trust and limitations

Herdr plugins run as the current user and are not sandboxed. Review
`herdr-plugin.toml` and `scripts/` before installation.

The plugin is a human/runtime view over Mesij; it does not replace the Claude,
Codex, Copilot, OpenCode, or Pi integrations that register sessions and teach
agents the coordination protocol. Shell panes without a detected native agent
session can still use the TUI, checks, agents list, and event stream.
