# Next task for corepilot

## Fix: invalid JSON Schema type "datetime" in forge-mcp

### What

`forge-mcp` emits `"type":"datetime"` in generated JSON Schema for `published_at`
and `scheduled_at` fields. `"datetime"` is not a valid JSON Schema primitive type.
The correct representation is `"type":"string","format":"date-time"`.

VS Code's MCP client (and likely others) reject tool registration when the schema
is invalid, blocking all `create_*` and `update_*` tools from loading.

### Why

`module.go` in forge core returns `"datetime"` from `mcpGoTypeStr()` as an internal
type identifier for `time.Time` fields. This is correct for internal use.
The bug is in `forge-mcp/mcp.go`: `inputSchema()` and `inputSchemaUpdate()` pass
`f.Type` directly to the JSON Schema `"type"` key without translating
`"datetime"` to the valid JSON Schema representation.

### Where to fix

`forge-mcp/mcp.go` — in `inputSchema()` and `inputSchemaUpdate()`.

When building a property from `f.Type`, check if `f.Type == "datetime"`.
If so, emit `"type":"string","format":"date-time"` instead of `"type":"datetime"`.

Do not change `mcpGoTypeStr()` in `module.go` — the `"datetime"` string is an
internal identifier used by forge core and must not be changed.

### Scope

- `forge-mcp/mcp.go` — `inputSchema()` and `inputSchemaUpdate()`
- `forge-mcp/mcp_test.go` — update or add tests to assert that `published_at`
  and `scheduled_at` generate `{"type":"string","format":"date-time"}` in the
  tool schema
- `forge-mcp/CHANGELOG.md` — new entry
- `forge-mcp/go.mod` — version bump (patch)

This is a forge-mcp-only change. forge core (`module.go`) is not touched.

### Expected outcome

After the fix, a tools/list response for any content module must include
`published_at` and `scheduled_at` as:

```json
{
  "type": "string",
  "format": "date-time"
}
```

VS Code Copilot agent mode must accept the tool registration without errors.
