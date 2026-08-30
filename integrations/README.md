# Harness integrations

Mesij integrations combine three layers:

1. **Skills** teach the coordination protocol.
2. **Hooks** register stable sessions, surface inbox messages, and check edits.
3. **Plugins/extensions** expose native Mesij tools and richer notifications.

All integrations require a current `mesij` binary on `PATH`.

## Capability matrix

| Harness | Stable session | Automatic inbox context | Native Mesij tools | Automatic pre-edit check | Deny mode | Shell-write coverage |
| --- | --- | --- | --- | --- | --- | --- |
| Claude Code | Yes | Prompt and stop hooks | No | `Write` / `Edit` | `MESIJ_HOOK_MODE=deny` | No |
| Codex | Yes | Prompt and stop hooks | No | `apply_patch` / `Edit` / `Write` | `MESIJ_HOOK_MODE=deny` | No |
| GitHub Copilot CLI | Yes | Post-tool and stop hooks | No | `edit` / `write` / `create` / `apply_patch` | `MESIJ_HOOK_MODE=deny` | No |
| OpenCode | Yes | Three-second poll | Yes | First-class edit/write/patch tools | `MESIJ_ENFORCE_CONFLICTS=1` | No |
| Pi | Yes | Three-second poll | Yes | No automatic interception | No | No |
| Herdr | Observes native session | Status-change notification | Human actions/panes | No | No | No |

Skills remain authoritative because shell-based mutation may bypass file-tool
hooks. No integration automatically finishes claims on shutdown.

## Shared Claude/Codex/Copilot hook adapter

Mesij provides a fail-open hook protocol adapter:

```sh
mesij hook session-start --actor HARNESS
mesij hook inbox
mesij hook pre-edit [--mode advisory|deny] [--format vscode|copilot]
```

Each command reads one hook JSON object from stdin and emits the structured
hook response expected by Claude Code, Codex, or GitHub Copilot CLI. The
adapter:

- uses the harness `session_id` as the stable Mesij session;
- persists Claude shell variables through `CLAUDE_ENV_FILE`;
- keeps an atomic inbox cursor outside the repository;
- injects new external messages on `UserPromptSubmit`;
- blocks `Stop`/`agentStop` once when unread messages arrive;
- extracts Claude `Write`/`Edit`, Codex `apply_patch`, and GitHub Copilot
  edit/write/create/patch paths;
- ignores the current session's own work claims;
- defaults to advisory overlap context and supports opt-in deny mode through
  `MESIJ_HOOK_MODE=deny`.

Errors are written to stderr and hooks fail open so a temporary coordination
failure does not deadlock the coding harness.

## Claude Code

`claude-code/` is a complete plugin:

- `.claude-plugin/plugin.json`
- `hooks/hooks.json`
- `skills/mesij/SKILL.md`

The hooks register the session, deliver the inbox before prompts and stopping,
and check first-class file edits. The legacy shell wrappers remain as thin
entry points to `mesij hook`. See [`claude-code/README.md`](claude-code/README.md).

## Codex

`codex/` is a complete plugin:

- `.codex-plugin/plugin.json`
- `hooks/hooks.json`
- `skills/mesij/SKILL.md`
- `skills/mesij/agents/openai.yaml`

Codex users must review and trust the plugin hooks. Standalone installations can
copy `skills/mesij` to `.agents/skills/mesij`, but the plugin is needed for
session registration and inbox delivery. See [`codex/README.md`](codex/README.md).

## GitHub Copilot CLI

`github-copilot/` is an Open Plugin Spec plugin with `plugin.json`,
`hooks/hooks.json`, and `skills/mesij/SKILL.md`. It registers stable sessions,
checks first-class edits, polls the inbox after tools, and uses `agentStop` to
surface messages that arrive before completion.

```sh
copilot plugin install withakay/mesij:integrations/github-copilot
```

## OpenCode

`opencode/` contains a project plugin and skill. Copy them to
`.opencode/plugins/mesij.ts` and `.opencode/skills/mesij/`.

The plugin exposes `mesij_inbox`, `mesij_check`, `mesij_emit`, and
`mesij_agents`; injects stable shell identity; polls inbox messages; and checks
first-class file tools. See `opencode/README.md`.

## Pi

`pi/` is an installable Pi package containing both an extension and skill:

```sh
pi install ./integrations/pi
```

The extension exposes the same four tools, provides `/mesij-inbox`, injects new
messages without forcing an idle turn, and maps Pi's durable session ID to
Mesij. See `pi/README.md`.

## Herdr

`herdr/` is a Herdr v1 plugin with popup/tab panes for the Mesij TUI, checks,
inboxes, agents, and JSONL tailing. It also maps Herdr native agent sessions to
Mesij sessions and shows notifications for unread messages when agents settle.

```sh
herdr plugin link "$PWD/integrations/herdr"
# or, with repository access:
herdr plugin install withakay/mesij/integrations/herdr
```

See [`herdr/README.md`](herdr/README.md).

## Generic integrations

Source or adapt `generic/agent-start.sh`, inject one of the skills into project
instructions, and consume Mesij directly:

```sh
mesij check --session "$MESIJ_SESSION" --after "$CURSOR" --json
mesij tail --after "$CURSOR" --follow --session "$MESIJ_SESSION"
```

Each `tail` line is one immutable event. Persist its `sequence`. For structured
request/response integrations, submit a JSON object with a `files` array through
`mesij emit`.

## Environment

- `MESIJ_ACTOR` — readable agent/harness name.
- `MESIJ_SESSION` — stable identity for one agent run.
- `MESIJ_PROJECT` — optional project selector.
- `MESIJ_DB` — optional explicit shared database.
- `MESIJ_HOME` — optional external Mesij data directory.
- `MESIJ_HOOK_MODE` — `advisory` (default) or `deny` for hook integrations.
- `MESIJ_ENFORCE_CONFLICTS` — set to `1` for OpenCode's strict first-class
  edit-tool blocking.

Direct messages are visible in the shared log. Recipient routing is a
coordination hint, not a privacy boundary. Hooks do not automatically finish
claims because session shutdown does not prove completion.
