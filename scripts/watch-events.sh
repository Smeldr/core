#!/bin/bash
# Holds a streamed connection to process.smeldr.dev's GET /_events/stream
# open and emits one line per real event (heartbeat pings filtered out).
# Intended as a Monitor `command` for core-implementer's own session.
#
# Contains NO secret. The bearer token is read fresh, at run time, from
# this project's own Claude Code MCP config (.claude.json) — the same
# credential already used for this session's process.smeldr.dev MCP
# calls, not a new or duplicated one. Nothing here echoes, logs, or
# writes the token anywhere; it lives only in curl's own process memory
# for the life of the connection.
#
# Copied from smeldr/architect/scripts/watch-events.sh (commit 885142f),
# PROJECT_KEY changed to this project's own absolute path as it appears
# in .claude.json (forward-slash form — confirmed against the actual
# config, not assumed from the backslash form the OS itself reports).

set -uo pipefail

CONFIG_FILE="C:\\Users\\peter\\.claude.json"
PROJECT_KEY='C:/Users/peter/Documents/Code/Smeldr/core'

# A long-held streamed connection drops periodically (curl exit 56,
# CURLE_RECV_ERROR, roughly every 10-20 minutes in real use) — this is
# normal for this kind of connection, not a server problem. Loop and
# reconnect instead of letting one drop silently end the Monitor.
while true; do
  TOKEN=$(powershell.exe -NoProfile -Command "
    \$ErrorActionPreference = 'Stop'
    \$cfg = Get-Content -Raw '$CONFIG_FILE' | ConvertFrom-Json
    \$node = \$cfg.projects.'$PROJECT_KEY'
    if (-not \$node) { \$node = \$cfg.'$PROJECT_KEY' }
    \$auth = \$node.mcpServers.process.headers.Authorization
    if (-not \$auth) { exit 1 }
    \$auth -replace '^Bearer ', ''
  " 2>/dev/null | tr -d '\r\n')

  if [ -z "${TOKEN:-}" ]; then
    echo "ERROR: could not extract process.smeldr.dev token for $PROJECT_KEY from $CONFIG_FILE" >&2
    sleep 5
    continue
  fi

  curl -sN -H "Authorization: Bearer $TOKEN" "https://process.smeldr.dev/_events/stream?channel=core" \
    | grep --line-buffered -v '"type":"ping"'

  sleep 2
done
