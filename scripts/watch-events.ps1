# watch-events.ps1
#
# Single-process replacement for watch-events.sh's bash+curl design.
# Intended as a Monitor `command` for core-implementer's own session.
#
# Root cause this exists to remove: watch-events.sh wraps curl.exe as a
# separate child process of a bash.exe wrapper. On this Windows environment,
# stopping the Monitor (TaskStop) does not reliably kill that child - tested
# live, confirmed: even with an explicit trap in the bash script, curl.exe
# survived a stop. 42 orphaned bash.exe/curl.exe processes were found still
# running, hours after being "stopped," each still holding a real connection
# and counting against eventStreamMaxSubscribersPerToken (core's own
# investigation, core-implementer-investigation-finding-a29741a1) - the
# direct cause of a real, live too_many_requests incident on 2026-09-15.
#
# This script does the token fetch, the streaming HTTP request, reconnect,
# backoff, and jitter all inside one native PowerShell process using
# System.Net.Http.HttpClient - no curl, no separate child process for the
# connection itself. There is nothing left to orphan independently of the
# process a Monitor actually tracks: killing this one process is killing the
# whole thing, not killing a wrapper while a grandchild survives.
#
# Contains NO secret. The bearer token is read fresh, at run time, from this
# project's own Claude Code MCP config (.claude.json) - same credential
# already used for this session's process.smeldr.dev MCP calls.
#
# Exponential backoff + equal jitter, same shape and same reasoning as
# watch-events.sh's own 2026-09-15 fix: reset to a 2s baseline when the prior
# connection stayed up >= 30s (a real, healthy connection that dropped
# normally); otherwise double, capped at 60s. Equal jitter (half the wait
# guaranteed, half randomized) so concurrent clients don't retry in lockstep
# after a shared failure.
#
# Ported from smeldr/architect/scripts/watch-events.ps1 (commit 792c2b6),
# ProjectKey/Channel changed to this project's own. Supersedes
# watch-events.sh as the recommended Monitor-arming command (01a0a652) -
# watch-events.sh is kept in the repo as reference/fallback only.

$ErrorActionPreference = 'Stop'

# Windows PowerShell 5.1 (.NET Framework) does not load System.Net.Http into
# the default runspace the way PowerShell 7/.NET Core does - confirmed live,
# the first run of this script failed with "Unable to find type
# [System.Net.Http.HttpClient]" until this explicit load was added.
Add-Type -AssemblyName System.Net.Http

$ConfigFile = 'C:\Users\peter\.claude.json'
$ProjectKey = 'C:/Users/peter/Documents/Code/Smeldr/core'
$Channel = 'core'
$Url = "https://process.smeldr.dev/_events/stream?channel=$Channel"

function Get-ProcessToken {
    $cfg = Get-Content -Raw $ConfigFile | ConvertFrom-Json
    $node = $cfg.projects.$ProjectKey
    if (-not $node) { $node = $cfg.$ProjectKey }
    $auth = $node.mcpServers.process.headers.Authorization
    if (-not $auth) { throw "no process.smeldr.dev token found for $ProjectKey in $ConfigFile" }
    return $auth -replace '^Bearer ', ''
}

$backoff = 2
$maxBackoff = 60

while ($true) {
    $token = $null
    try {
        $token = Get-ProcessToken
    } catch {
        [Console]::Error.WriteLine("ERROR: could not extract process.smeldr.dev token: $_")
        Start-Sleep -Seconds 5
        continue
    }

    $connectedAt = Get-Date
    $client = [System.Net.Http.HttpClient]::new()
    $client.Timeout = [System.Threading.Timeout]::InfiniteTimeSpan
    $client.DefaultRequestHeaders.Authorization = `
        [System.Net.Http.Headers.AuthenticationHeaderValue]::new('Bearer', $token)

    try {
        $response = $client.GetAsync($Url, [System.Net.Http.HttpCompletionOption]::ResponseHeadersRead).GetAwaiter().GetResult()
        if (-not $response.IsSuccessStatusCode) {
            [Console]::Error.WriteLine("ERROR: HTTP $([int]$response.StatusCode) from $Url")
        } else {
            $stream = $response.Content.ReadAsStreamAsync().GetAwaiter().GetResult()
            $reader = [System.IO.StreamReader]::new($stream)
            try {
                while (-not $reader.EndOfStream) {
                    $line = $reader.ReadLine()
                    if ($null -eq $line) { break }
                    if ($line -notmatch '"type":"ping"') {
                        [Console]::Out.WriteLine($line)
                        [Console]::Out.Flush()
                    }
                }
            } finally {
                $reader.Dispose()
            }
        }
    } catch {
        [Console]::Error.WriteLine("ERROR: connection dropped: $_")
    } finally {
        $client.Dispose()
    }

    # Connection ended - the server or an intermediate proxy dropped it, or
    # it never connected at all. Reconnect after a pause rather than
    # hammering the server: reset to the 2s baseline on a real, sustained
    # connection (>= 30s), double on a fast repeating failure (2/4/8/16/32/
    # 60s, capped), then equal jitter (half guaranteed, half randomized) so
    # this doesn't retry in lockstep with the other roles' own clients.
    $duration = ((Get-Date) - $connectedAt).TotalSeconds
    if ($duration -ge 30) {
        $backoff = 2
    } else {
        $backoff = [Math]::Min($backoff * 2, $maxBackoff)
    }
    $half = [Math]::Floor($backoff / 2)
    $jitter = Get-Random -Minimum 0 -Maximum ($half + 1)
    Start-Sleep -Seconds ($half + $jitter)
}
