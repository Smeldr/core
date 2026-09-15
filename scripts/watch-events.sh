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
# for the life of each connection.
#
# Reconnects automatically. The streamed connection is long-lived and
# will eventually be dropped by the server or an intermediate proxy —
# observed repeatedly in real use, curl exit 56 (CURLE_RECV_ERROR),
# roughly every 10-20 minutes. That is normal for any long-poll/SSE
# connection, not a bug in this script's own request, but the original
# version of this script had no retry loop: it made one curl connection
# and exited for good the moment that connection dropped, silently
# ending the Monitor and requiring a manual re-arm every time — this is
# the fix for that, not a new mechanism, a missing loop around the same
# one request.
#
# Exponential backoff on fast-repeating failures (2026-09-15, live
# incident, ported from smeldr/architect's own copy, commits 63c593c and
# 1d7ac06): a fixed 2s retry between attempts is fine for the normal
# ~10-20min drop above, but a sustained "too_many_requests" from the
# server fails the connection immediately every time, and a fixed 2s
# retry against that just re-hammers the server every 2 seconds instead
# of backing off — caught live, this project's own copy of this script
# doing exactly that. Below: if the prior connection stayed up at least
# 30s (a real, healthy connection that then dropped normally), the
# backoff resets to 2s: this is not about punishing every reconnect,
# only ones that are failing fast and repeatedly. If it failed in under
# 30s, the wait doubles next time (2/4/8/16/32/60s, capped at 60s)
# instead of retrying at the same fixed cadence into whatever is still
# rejecting the connection.
#
# Jitter (2026-09-15, same incident): backoff alone still retries in
# lockstep across the ~5 concurrent clients this project runs
# (architect/core/site/cloud/devops, each its own copy) — a shared
# failure (server restart, a rate-limit window) drops all five near-
# simultaneously, and without jitter they all wait the same 2s, then all
# retry together, then all wait 4s, still together. "Equal jitter" (half
# the backoff is guaranteed, the other half randomized) spreads retries
# across time instead of every client hammering the server in the same
# instant, while still growing the floor as backoff itself grows.
#
# Copied from smeldr/architect/scripts/watch-events.sh, PROJECT_KEY
# changed to this project's own absolute path as it appears in
# .claude.json (forward-slash form — confirmed against the actual
# config, not assumed from the backslash form the OS itself reports).

set -uo pipefail

CONFIG_FILE="C:\\Users\\peter\\.claude.json"
PROJECT_KEY='C:/Users/peter/Documents/Code/Smeldr/core'

backoff=2
max_backoff=60

fetch_token() {
  powershell.exe -NoProfile -Command "
    \$ErrorActionPreference = 'Stop'
    \$cfg = Get-Content -Raw '$CONFIG_FILE' | ConvertFrom-Json
    \$node = \$cfg.projects.'$PROJECT_KEY'
    if (-not \$node) { \$node = \$cfg.'$PROJECT_KEY' }
    \$auth = \$node.mcpServers.process.headers.Authorization
    if (-not \$auth) { exit 1 }
    \$auth -replace '^Bearer ', ''
  " 2>/dev/null | tr -d '\r\n'
}

while true; do
  TOKEN=$(fetch_token)
  if [ -z "${TOKEN:-}" ]; then
    echo "ERROR: could not extract process.smeldr.dev token for $PROJECT_KEY from $CONFIG_FILE" >&2
    sleep 5
    continue
  fi

  connected_at=$(date +%s)
  curl -sN -H "Authorization: Bearer $TOKEN" "https://process.smeldr.dev/_events/stream?channel=core" \
    | grep --line-buffered -v '"type":"ping"'

  # curl (or grep) exited — the connection dropped for whatever reason.
  # Reconnect after a pause rather than hammering the server, and rather
  # than silently ending the Monitor for good. See the exponential-
  # backoff comment above: reset on a real, sustained connection,
  # double on a fast repeating failure.
  duration=$(( $(date +%s) - connected_at ))
  if [ "$duration" -ge 30 ]; then
    backoff=2
  else
    backoff=$((backoff * 2))
    [ "$backoff" -gt "$max_backoff" ] && backoff=$max_backoff
  fi
  # Equal jitter: half the wait is the guaranteed floor, the other half
  # is randomized (bash's builtin $RANDOM, 0-32767, no external dep) —
  # see the jitter comment above for why this needs to exist at all.
  half=$((backoff / 2))
  jitter=$((RANDOM % (half + 1)))
  sleep "$((half + jitter))"
done
