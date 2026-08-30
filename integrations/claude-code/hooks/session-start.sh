#!/bin/sh
set -eu
exec mesij hook session-start --actor "${MESIJ_ACTOR:-claude-code}"
