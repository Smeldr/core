#!/usr/bin/env bash
# Runs README.md's "Start in three minutes" on the release binaries, step by
# step, and stops at the first answer that differs from the one the README
# shows (T1207). CI runs it on macOS; it also runs in Git Bash on Windows and
# on Linux.
#
# What it does not do: no AI agent reads the README's prompts. Steps 3 to 5
# make the tool calls an agent would make, with the agent's own token.
#
# This script and the README are kept in step by hand. A change to the
# README's wording does not fail this script unless the server's answers
# change too.
#
# Where it departs from the README, and why (every other command is the
# README's: curl -LO, curl -sL | grep | the check, mv, chmod, the server's
# environment, init, token create, grant, transition):
#   - the hash check is the one the README gives the platform: shasum -a 256
#     -c on macOS (README line 45), sha256sum -c elsewhere (lines 30-31);
#   - PORT can be set (the README's default, 8080, otherwise);
#   - the server runs in the background with its log in a file, and the
#     bootstrap token is read from that log instead of copied by hand;
#   - init --name ci stands for --name <you>;
#   - grant and transition get --rev, which cli asks for at a terminal;
#   - the agent's prompts are the tool calls behind them (mcp(), with the
#     agent's token, over curl to /mcp/message);
#   - a report-only probe of the hash tools, and the /docs pages' check
#     line, run beside the README's steps and never fail the job.
#
# Environment:
#   OS_ARCH       the release file suffix: darwin-arm64, darwin-amd64,
#                 linux-amd64, linux-arm64 or windows-amd64 (required)
#   CORE_VERSION  a core tag such as v1.193.2, or latest (default)
#   CLI_VERSION   a cli tag such as v0.38.1, or latest (default)
#   PORT          the server's port (default 8080, as in the README)
#   WORK          the directory to run in (default: a new temporary one)
set -euo pipefail

OS_ARCH=${OS_ARCH:?set OS_ARCH, for example darwin-arm64}
CORE_VERSION=${CORE_VERSION:-latest}
CLI_VERSION=${CLI_VERSION:-latest}
PORT=${PORT:-8080}
URL=http://localhost:$PORT
WORK=${WORK:-$(mktemp -d)}
ext=""
case $OS_ARCH in windows-*) ext=.exe ;; esac

STEP=0
fail() {
	echo "::error::step $STEP: $*"
	exit 1
}
note() { echo "== step $STEP: $*"; }
# mask keeps a secret out of the GitHub Actions log (no-op elsewhere).
mask() { if [ -n "${GITHUB_ACTIONS:-}" ]; then echo "::add-mask::$1"; fi; }

# The first Python that runs (on Windows, python3 can be a store stub).
PY=""
for c in python3 python; do
	if command -v "$c" >/dev/null && "$c" -c pass 2>/dev/null; then PY=$c && break; fi
done
[ -n "$PY" ] || fail "python3 is needed to read the answers"
# json EXPR: evaluates the Python expression EXPR on the JSON on stdin (as d).
json() { "$PY" -c 'import json,sys; d=json.load(sys.stdin); v=eval(sys.argv[1]); print(v if isinstance(v,str) else json.dumps(v))' "$1"; }
# jfind KEY: the first value of KEY anywhere in the JSON on stdin, or nothing.
jfind() {
	"$PY" -c '
import json, sys
def find(x, k):
    if isinstance(x, dict):
        if k in x:
            return x[k]
        x = list(x.values())
    if isinstance(x, list):
        for v in x:
            r = find(v, k)
            if r is not None:
                return r
    return None
r = find(json.load(sys.stdin), sys.argv[1])
print("" if r is None else r)' "$1"
}

# The check the README gives the reader of this platform (T1253), chosen by
# OS_ARCH and never by what is installed: on macOS the README's macOS bullet
# (README.md line 45) says shasum -a 256 -c; everywhere else its bash form
# (lines 30-31 and 80-81) says sha256sum -c. If that tool is missing, the
# check fails, because the README is then wrong for the platform.
case $OS_ARCH in
darwin-*) sumcheck() { shasum -a 256 -c; } ; SUMCHECK="shasum -a 256 -c" ;;
*) sumcheck() { sha256sum -c; } ; SUMCHECK="sha256sum -c" ;;
esac

# release_base REPO VERSION: the README's download URL, latest/download, or
# download/vX.Y.Z for a fixed version as the README says.
release_base() {
	if [ "$2" = latest ]; then
		echo "https://github.com/Smeldr/$1/releases/latest/download"
	else
		echo "https://github.com/Smeldr/$1/releases/download/$2"
	fi
}

# download REPO FILE VERSION: the README's two lines, as written: curl -LO the
# file, then curl -sL SHA256SUMS | grep FILE | the platform's check. A failed
# download is caught by the check, as it would be for the reader.
download() {
	local file=$2 base out
	base=$(release_base "$1" "$3")
	curl -LO "$base/$file" || fail "curl -LO $base/$file"
	out=$(curl -sL "$base/SHA256SUMS" | grep "$file" | sumcheck 2>&1) || fail "curl -sL $base/SHA256SUMS | grep $file | $SUMCHECK printed: $out"
	[ "$out" = "$file: OK" ] || fail "the check printed \"$out\", want \"$file: OK\""
	echo "$out"
}

# notice TEXT: a line on the run's summary page on GitHub Actions, a plain
# line elsewhere. Report only: nothing that calls it fails the job.
notice() { if [ -n "${GITHUB_ACTIONS:-}" ]; then echo "::notice::$*"; else echo "notice: $*"; fi; }

# probe_hash_tools: what sha256sum and shasum are on this machine (T1253).
probe_hash_tools() {
	local t p
	for t in sha256sum shasum; do
		if p=$(command -v "$t"); then
			notice "$OS_ARCH: $t is $(ls -l "$p" 2>&1 | tr -s ' ' | cut -d' ' -f9-); --version: $("$t" --version 2>&1 | head -1)"
		else
			notice "$OS_ARCH: $t is missing"
		fi
	done
}

# docs_check REPO FILE VERSION SUMS: the /docs pages' check, in its own
# directory: curl -LO the file, curl -L -o SUMS, sha256sum --ignore-missing
# -c SUMS. Reported, never failing (T1253 asks what it does on macOS).
docs_check() {
	local base out code
	base=$(release_base "$1" "$3")
	mkdir -p docs-check && (
		cd docs-check
		curl -sLO "$base/$2"
		curl -sL -o "$4" "$base/SHA256SUMS"
		out=$(sha256sum --ignore-missing -c "$4" 2>&1) && code=0 || code=$?
		notice "$OS_ARCH: docs' line 'sha256sum --ignore-missing -c $4' exited $code: $(echo "$out" | tr '\n' ' ')"
	) || true
}

# mcp TOKEN TOOL ARGS: one tools/call, printing the JSON-RPC response.
mcp() {
	local body
	body=$(printf '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"%s","arguments":%s}}' "$2" "$3")
	curl -fsS "$URL/mcp/message" -H "Authorization: Bearer $1" -H 'Content-Type: application/json' \
		-H 'Accept: application/json, text/event-stream' --data "$body" | sed -n 's/^data: //p; /^{/p' | head -1
}
# result: the tool's own answer (the text of result.content[0]) from a response.
result() { json 'd["result"]["content"][0]["text"]'; }
# ok RESPONSE WHAT: fails unless RESPONSE is a result, not an error.
ok() {
	[ "$(echo "$1" | json '"error" in d or d.get("result",{}).get("isError",False)')" = false ] || fail "$2: $1"
}

cd "$WORK"
echo "working in $WORK"
SERVER_PID=""
BOOT=""
# On exit: stop the server, and keep a copy of its log without the bootstrap
# token (server-redacted.log), which is the one CI uploads.
finish() {
	if [ -n "$SERVER_PID" ]; then kill "$SERVER_PID" 2>/dev/null || true; fi
	if [ -f server.log ]; then
		if [ -n "$BOOT" ]; then grep -v -F "$BOOT" server.log >server-redacted.log || true; else grep -v '^[[:space:]]' server.log >server-redacted.log || true; fi
	fi
}
trap finish EXIT

STEP=0
note "hash tools on this machine (report only)"
probe_hash_tools

STEP=1
note "download the server and start it"
download core "smeldr-server-$OS_ARCH$ext" "$CORE_VERSION"
mv "smeldr-server-$OS_ARCH$ext" "smeldr-server$ext" && chmod +x "smeldr-server$ext"
SECRET=$(openssl rand -hex 32) BASE_URL=$URL PORT=$PORT \
	ENABLE_TOKENS=1 ENABLE_GOVERNANCE=1 ENABLE_RELATIONS=1 ENABLE_GOVERNANCE_RECORDS=1 \
	"./smeldr-server$ext" >server.log 2>&1 &
SERVER_PID=$!
for _ in $(seq 60); do
	grep -qs "smeldr-server: listening on 127.0.0.1:$PORT" server.log && break
	kill -0 "$SERVER_PID" 2>/dev/null || fail "the server exited before its ready line"
	sleep 0.5
done
grep -q "smeldr-server: listening on 127.0.0.1:$PORT" server.log || fail "no ready line within 30s"
grep -q "bootstrap admin token created (copy now, shown once):" server.log || fail "no bootstrap token line"
BOOT=$(sed -n '/bootstrap admin token created/{n;s/^[[:space:]]*//;p;q;}' server.log)
[ -n "$BOOT" ] || fail "the bootstrap token line is empty"
mask "$BOOT"
grep -v -F "$BOOT" server.log

STEP=2
note "download the CLI, init, and make the agent's token"
download cli "smeldr-cli-$OS_ARCH$ext" "$CLI_VERSION"
mv "smeldr-cli-$OS_ARCH$ext" "smeldr-cli$ext" && chmod +x "smeldr-cli$ext"
docs_check core "smeldr-server-$OS_ARCH$ext" "$CORE_VERSION" SHA256SUMS
docs_check cli "smeldr-cli-$OS_ARCH$ext" "$CLI_VERSION" cli-SHA256SUMS
cli() { "./smeldr-cli$ext" "$@"; }
out=$(SMELDR_BOOTSTRAP_TOKEN=$BOOT cli init --url "$URL" --name ci 2>&1) || fail "init: $out"
echo "$out"
for want in "OK  connected to $URL" 'OK  token "ci" created (class human, admin, 365 days)' \
	"OK  grants: admin, approver" "OK  .smeldr-cli.env written" "OK  verification passed: the new token acts as admin"; do
	echo "$out" | grep -q -F "$want" || fail "init did not print \"$want\""
done
HUMAN=$(sed -n 's/^SMELDR_TOKEN=//p' .smeldr-cli.env | tr -d '"\r')
[ -n "$HUMAN" ] || fail ".smeldr-cli.env holds no SMELDR_TOKEN"
mask "$HUMAN"
out=$(cli token create my-agent editor 30 --class agent --reason "my agent") || fail "token create: $out"
AGENT=$(echo "$out" | json 'd["token"]')
TOKEN_ID=$(echo "$out" | json 'd["token_id"]')
mask "$AGENT"
[ -n "$AGENT" ] && [ -n "$TOKEN_ID" ] || fail "token create gave no token or token_id"
# At a terminal, cli shows the identity and asks; without one it needs the
# rev it would have shown, so the script reads it as the refusal says.
out=$(cli grant list "$TOKEN_ID" </dev/null 2>&1) || fail "grant list: $out"
IREV=$(echo "$out" | jfind identity_rev)
[ -n "$IREV" ] || fail "grant list shows no identity_rev: $out"
out=$(cli grant "$TOKEN_ID" editor --reason "my agent" --rev "$IREV" </dev/null 2>&1) || fail "grant: $out"
echo "$out"

STEP=3
note "the agent proposes a Decision"
r=$(mcp "$AGENT" create_decision '{"title":"We never log session tokens","scope":"engineering","body":"Session tokens never reach a log line."}')
ok "$r" "create_decision"
d=$(echo "$r" | result)
DEC=$(echo "$d" | json 'd["address"]')
st=$(echo "$d" | json 'd.get("Status") or d.get("status")')
echo "$DEC $st"
[ "$DEC" = D1 ] || fail "the Decision's address is $DEC, want D1"
[ "$st" = proposed ] || fail "the Decision is $st, want proposed"

STEP=4
note "the agent places it, tries to ratify it and is refused; the person ratifies"
r=$(mcp "$AGENT" assert_relation '{"source_type":"Decision","source_id":"D1","relation_kind":"belongs_to_organization","target_type":"Organization","target_id":"ORG1"}')
ok "$r" "placing D1 in ORG1"
r=$(mcp "$AGENT" transition_item '{"type_name":"Decision","slug":"D1","to_state":"ratified","rev":0}')
echo "$r"
code=$(echo "$r" | json 'd.get("error",{}).get("code")')
msg=$(echo "$r" | json 'd.get("error",{}).get("message","")')
[ "$code" = -32001 ] || fail "the agent's ratify answered code $code, want -32001: $r"
want="forbidden: an agent token never ratifies, supersedes or amends a Decision; a human approver does"
[ "$msg" = "$want" ] || fail "the agent's ratify said \"$msg\", want \"$want\""
r=$(mcp "$HUMAN" get_decision '{"slug":"D1"}')
ok "$r" "get_decision D1 before ratifying"
DREV=$(echo "$r" | result | json 'str(d.get("Rev", d.get("rev")))')
out=$(cli transition Decision D1 --to ratified --reason "We never log session tokens" --rev "$DREV" </dev/null 2>&1) || fail "the person's ratify: $out"
echo "$out"
r=$(mcp "$HUMAN" get_decision '{"slug":"D1"}')
ok "$r" "get_decision D1"
d=$(echo "$r" | result)
st=$(echo "$d" | json 'd.get("Status") or d.get("status")')
sd=$(echo "$d" | json 'd.get("standing")')
aw=$(echo "$d" | json 'd.get("awaits_action")')
[ "$st" = ratified ] && [ "$sd" = holds ] && [ "$aw" = false ] || fail "D1 is $st, standing $sd, awaits_action $aw; want ratified, holds, false"

STEP=5
note "the agent asks what applies to a file"
paths='{"repository":"acme/app","paths":["internal/billing/handler.go"]}'
r=$(mcp "$AGENT" decisions_for_paths "$paths")
ok "$r" "decisions_for_paths"
d=$(echo "$r" | result)
echo "$d"
[ "$(echo "$d" | json 'd["uncovered"]')" = '["internal/billing/handler.go"]' ] || fail "the path is not uncovered on a fresh server: $d"
[ "$(echo "$d" | json 'len(d["areas"]) + len(d["decisions"])')" = 0 ] || fail "a fresh server names Areas or Decisions: $d"
r=$(mcp "$HUMAN" create_domain '{"name":"Payments"}')
ok "$r" "create_domain"
DOM=$(echo "$r" | result | json 'd["address"]')
# The README's four calls: a Domain, an Area, the Area into the Domain, and a
# coverage rule.
r=$(mcp "$HUMAN" create_area '{"name":"Billing"}')
ok "$r" "create_area"
ARE=$(echo "$r" | result | json 'd["address"]')
r=$(mcp "$HUMAN" assert_relation "{\"source_type\":\"Area\",\"source_id\":\"$ARE\",\"relation_kind\":\"belongs_to_domain\",\"target_type\":\"Domain\",\"target_id\":\"$DOM\"}")
ok "$r" "placing $ARE"
r=$(mcp "$HUMAN" create_coverage_rule "{\"area\":\"$ARE\",\"repository\":\"acme/app\",\"pattern\":\"internal/billing/**\"}")
ok "$r" "create_coverage_rule"
r=$(mcp "$AGENT" decisions_for_paths "$paths")
ok "$r" "decisions_for_paths after the coverage rule"
d=$(echo "$r" | result)
echo "$d"
# The README's answer, address for address (README.md, step 5).
[ "$DOM" = DOM2 ] || fail "the Domain's address is $DOM, the README shows DOM2"
[ "$ARE" = ARE1 ] || fail "the Area's address is $ARE, the README shows ARE1"
got=$(echo "$d" | json '[[a["address"],a["name"],a["approval_mode"],a["domain"],a["paths"],a["rules"]] for a in d["areas"]]')
want='[["ARE1", "Billing", "comment-only", "DOM2", ["internal/billing/handler.go"], ["COV1"]]]'
[ "$got" = "$want" ] || fail "areas are $got, want $want"
got=$(echo "$d" | json '[[x["address"],x["title"],x["state"],x["standing"],x["place"],x["place_address"]] for x in d["decisions"]]')
want='[["D1", "We never log session tokens", "ratified", "holds", "organization", "ORG1"]]'
[ "$got" = "$want" ] || fail "decisions are $got, want $want"
[ "$(echo "$d" | json 'd["uncovered"]')" = '[]' ] || fail "the path is still uncovered: $d"

STEP=6
echo "== all README steps gave the answers it shows ($OS_ARCH, core $CORE_VERSION, cli $CLI_VERSION)"
