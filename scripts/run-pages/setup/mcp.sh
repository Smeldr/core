# Setup, not a page block: loads the operator's token and defines mcp as
# Getting started does, for a sequence that runs only Getting started's first
# blocks.
. ./.smeldr-cli.env
mcp() {
  curl -s http://localhost:8080/mcp/message \
    -H "Authorization: Bearer $SMELDR_TOKEN" \
    -H 'Content-Type: application/json' \
    -H 'Accept: application/json, text/event-stream' \
    --data @"$1"
}
echo '(setup: token loaded, mcp defined as on Getting started)'
