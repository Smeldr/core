#!/usr/bin/env bash
# ci-green.sh <owner/repo> <sha>: exit 0 only when the newest ci.yml run from a
# push on <sha> completed with success (core T1297). The release workflow runs
# it before it builds anything, so a release is never cut on a commit whose CI
# is red, still running or missing. It reads ci.yml only, no other workflow.
#
# The same file is in Smeldr/core and Smeldr/cli: change both together.
# It runs from the tagged commit, so it gates only a tag on a commit that
# carries it; a tag on an older commit runs that commit's workflow, ungated.
#
# Needs gh with a token that can read the repository's Actions runs
# (GH_TOKEN; in a workflow, the workflow's own token with actions: read).
#
# Bounds, in the environment, so a test can use small ones:
#   WAIT_MINUTES     how long to wait while the run is queued or running (45)
#   MISSING_MINUTES  how long to wait for a run to appear at all (10)
#   POLL_SECONDS     time between two reads (30)
#
# A failed read is retried within the same bounds; it is never taken as green.
set -u

repo=${1:?usage: ci-green.sh <owner/repo> <sha>}
sha=${2:?usage: ci-green.sh <owner/repo> <sha>}
wait_minutes=${WAIT_MINUTES:-45}
missing_minutes=${MISSING_MINUTES:-10}
poll_seconds=${POLL_SECONDS:-30}

start=$(date +%s)
last="no answer from the GitHub API yet"
while :; do
  elapsed=$(( $(date +%s) - start ))
  if run=$(gh api "repos/$repo/actions/workflows/ci.yml/runs?head_sha=$sha&event=push&per_page=1" \
      --jq '.workflow_runs[0] // empty | "\(.status) \(.conclusion // "none") \(.html_url)"' 2>&1); then
    if [ -z "$run" ]; then
      last="no run"
      if [ "$elapsed" -ge $(( missing_minutes * 60 )) ]; then
        echo "::error::No ci.yml run from a push on $sha in $repo after $missing_minutes minutes. A release is tagged on a commit that is on main, after its CI has run. The release stays a draft."
        exit 1
      fi
    else
      read -r status conclusion url <<<"$run"
      if [ "$status" = completed ]; then
        if [ "$conclusion" = success ]; then
          echo "CI is green on $sha: $url"
          exit 0
        fi
        echo "::error::CI on $sha is $conclusion: $url. The release stays a draft; fix CI and tag a new commit."
        exit 1
      fi
      last="$status: $url"
      if [ "$elapsed" -ge $(( wait_minutes * 60 )) ]; then
        echo "::error::CI on $sha is still $status after $wait_minutes minutes: $url. The release stays a draft; re-run this workflow when CI is green."
        exit 1
      fi
    fi
  else
    last="the GitHub API answered: $run"
    if [ "$elapsed" -ge $(( wait_minutes * 60 )) ]; then
      echo "::error::Could not read CI for $sha in $repo for $wait_minutes minutes ($last). The release stays a draft."
      exit 1
    fi
  fi
  echo "Waiting for CI on $sha ($last), ${elapsed}s so far."
  sleep "$poll_seconds"
done
