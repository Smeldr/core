# Setup, not a page block: the places of Domains and Areas (DOM2 Payments,
# ARE1 Billing over internal/billing/** in acme/app) and three ratified
# Decisions, D1 in ORG1, D2 in DOM2, D3 in ARE1, which connect-an-agent and
# which-decisions-apply take as given. A refused call, or an address other than
# the one the pages use, fails the setup and names the call, so a page is never
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
setup_rel() {
  setup_call assert_relation "{\"source_type\":\"$1\",\"source_id\":\"$2\",\"relation_kind\":\"$3\",\"target_type\":\"$4\",\"target_id\":\"$5\"}"
}
setup_call create_domain '{"name":"Payments"}' DOM2 || return 1
setup_rel Domain DOM2 belongs_to_organization Organization ORG1 || return 1
setup_call create_area '{"name":"Billing","approval_mode":"requires-approval"}' ARE1 || return 1
setup_rel Area ARE1 belongs_to_domain Domain DOM2 || return 1
setup_call create_coverage_rule '{"area":"ARE1","repository":"acme/app","pattern":"internal/billing/**"}' || return 1
i=0
for x in "Log no session tokens|belongs_to_organization|Organization|ORG1" \
  "Payments use idempotency keys|belongs_to_domain|Domain|DOM2" \
  "Invoices are immutable|belongs_to_area|Area|ARE1"; do
  i=$((i + 1))
  IFS='|' read -r t k tt tid <<< "$x"
  setup_call create_decision "{\"title\":\"$t\",\"scope\":\"engineering\",\"body\":\"$t.\"}" "D$i" || return 1
  setup_rel Decision "D$i" "$k" "$tt" "$tid" || return 1
  ./smeldr-cli transition Decision "D$i" --to ratified --rev 0 > /dev/null || { echo "setup: ratifying D$i failed"; return 1; }
done
rm -f setup-req.json
./smeldr-cli search --type Decision
