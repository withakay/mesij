#!/bin/sh
set -eu

command -v mesij >/dev/null 2>&1 || exit 0
input=$(cat)
session=$(printf '%s' "$input" | jq -r '.session_id // .sessionId // empty' 2>/dev/null || true)
file=$(printf '%s' "$input" | jq -r '.tool_input.file_path // .tool_input.path // empty' 2>/dev/null || true)
[ -n "$session" ] || exit 0
[ -n "$file" ] || exit 0

# The report becomes hook context. The skill tells the agent to start a work
# claim before editing; this hook is deliberately advisory rather than blocking.
mesij check --session "$session" --file "$file" --limit 20 2>/dev/null || true
