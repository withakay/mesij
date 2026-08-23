#!/bin/sh
set -eu

command -v mesij >/dev/null 2>&1 || exit 0
input=$(cat)
session=$(printf '%s' "$input" | jq -r '.session_id // .sessionId // empty' 2>/dev/null || true)
[ -n "$session" ] || session="claude-$(date +%s)-$$"
actor=${MESIJ_ACTOR:-claude-code}

if ! error=$(mesij session --actor "$actor" --id "$session" --json 2>&1 >/dev/null); then
  printf 'mesij warning: session registration failed: %s\n' "$error"
  exit 0
fi
printf 'Mesij session is %s. Reuse this exact ID for MESIJ_SESSION; do not create another session.\n' "$session"
if ! mesij check --session "$session" --limit 30; then
  printf 'mesij warning: initial coordination check failed\n'
fi
