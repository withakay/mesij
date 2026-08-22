#!/bin/sh
set -eu
: "${MESIJ_ACTOR:?set MESIJ_ACTOR to a human-readable agent name}"
eval "$(mesij session --actor "$MESIJ_ACTOR")"
export MESIJ_ACTOR MESIJ_SESSION
mesij check --session "$MESIJ_SESSION" --limit 30
printf '\nKeep these variables in the agent process: MESIJ_ACTOR=%s MESIJ_SESSION=%s\n' "$MESIJ_ACTOR" "$MESIJ_SESSION"
