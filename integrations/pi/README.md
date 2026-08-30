# Pi integration

This Pi package bundles a Mesij extension and skill.

```sh
pi install ./integrations/pi
# Project-local installation:
pi install -l ./integrations/pi
```

`mesij` must be installed on `PATH`. Project extensions are executable code and
should be reviewed before trust is granted.

The extension:

- maps Pi's durable session ID to `pi-<session-id>`;
- exports `MESIJ_ACTOR` and `MESIJ_SESSION` for Pi shell calls;
- polls and injects new external inbox messages without forcing an idle turn;
- provides `/mesij-inbox`;
- exposes `mesij_inbox`, `mesij_check`, `mesij_emit`, and `mesij_agents` tools;
- persists inbox cursors under `$XDG_STATE_HOME/mesij/pi` or
  `~/.local/state/mesij/pi`.

The extension intentionally does not finish active claims during session
shutdown. A stopped session does not prove that work was completed.
