#!/bin/sh
set -eu

command -v mesij >/dev/null 2>&1 || exit 0
input=$(cat)
session=$(printf '%s' "$input" | jq -r '.session_id // .sessionId // empty' 2>/dev/null || true)
[ -n "$session" ] || session="claude-$(date +%s)-$$"
actor=${MESIJ_ACTOR:-claude-code}

mesij session --actor "$actor" --id "$session" --json >/dev/null 2>&1 || true
printf 'mesij session: %s\n' "$session"
mesij check --session "$session" --limit 30 2>/dev/null || true
