# Setup, not a page block: the further Decisions which-decisions-apply's
# examples show (D5 to D9, proposed) and the pins file its third block reads.
# It runs after Getting started and Set up the example places, which leave D1
# to D4 and define mcp (T1272). A refused call, or an address other than the
# one the page uses, fails the setup and names the call, so the page is never
# blamed for the setup's fault.
n=100
# setup_call TOOL ARGS [ADDRESS]: one tool call; fails on a JSON-RPC error or a
# result marked isError, and, given ADDRESS, unless the answer creates it.
setup_call() {
  n=$((n + 1))
  printf '{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":"%s","arguments":%s}}' "$n" "$1" "$2" > setup-req.json
  setup_answer=$(mcp setup-req.json)
  case "$setup_answer" in
    *'"error":'* | *'"isError":true'*)
      echo "setup: $1 was refused: $setup_answer"
      return 1 ;;
  esac
  if [ -n "${3:-}" ]; then
    case "$setup_answer" in
      *"\\\"address\\\":\\\"$3\\\""*) ;;
      *)
        echo "setup: $1 did not create $3: $setup_answer"
        return 1 ;;
    esac
  fi
}
i=4
for t in 'Round invoice totals to cents' 'General rule' 'Unplaced' 'Tokens follow the token spec' 'Pinned later'; do
  i=$((i + 1))
  setup_call create_decision "{\"title\":\"$t\",\"scope\":\"engineering\",\"body\":\"$t.\"}" "D$i" || return 1
done
rm -f setup-req.json
printf '[{"repository":"acme/app","commit":"0123456789abcdef0123456789abcdef01234567","path":"cmd/server/main.go","role":"implementation"}]' > pins.json
echo '(setup: D5 to D9 proposed, pins.json written)'
