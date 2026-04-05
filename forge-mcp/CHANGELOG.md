# Changelog — forge-mcp

All notable changes to the `forge-mcp` module are documented here.

Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Versioning: [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

## [1.1.0] — 2026-04-05

Named revocable bearer token tools — Admin role required (Amendment A66).

### Added

- `mcp.go`: `Server.tokenStore *forge.TokenStore` field; wired from
  `app.TokenStore()` in `New()`.
- `transport.go`: `VerifyBearerToken` call updated to pass `s.tokenStore`
  (3-arg signature).
- `tool.go`: `authoriseAdmin()` helper enforcing Admin role (JSON-RPC -32001
  on failure); `tokenToolDefs()` returning three MCP tool definitions
  (`create_token`, `list_tokens`, `revoke_token`) with JSON Schema; 
  `handleTokenTool()` dispatcher; `handleToolsList()` appends token tools
  when `s.tokenStore != nil`; `handleToolsCall()` pre-dispatches token
  tool names before module-level auth.

### Token tools (Admin role required)

| Tool | Description |
|------|-------------|
| `create_token` | Issues a named bearer token with a given role and TTL in days |
| `list_tokens` | Lists all tokens with name, role, expiry, and revocation status |
| `revoke_token` | Revokes a token by ID — effective on the next request |

---

## [1.0.5] — 2026-03-18

`delete_{type}` moved to Editor-level admin tools (Amendment A55).

### Changed

- `mcp.go`: `delete_{type}` moved from `mcpToolDefs` (Author-required write
  tools) to `mcpAdminReadToolDefs` (Editor-required admin tools); the total
  tool count per MCPWrite module remains 8 (5 write + 3 admin: list, get,
  delete); `mcpAdminReadToolDefs` now accepts a local `slugOnly` schema to
  avoid repeating the object definition
- `tool.go`: `delete` dispatch case now calls `authoriseEditor` before
  executing the delete; previously only Author role was required — now
  Editor or Admin is required

---

## [1.0.4] — 2026-03-18

Wrap all tool call results in MCP `CallToolResult` format.

### Fixed

- `tool.go`: `handleToolsCall` now wraps every successful result in the MCP
  `CallToolResult` envelope via a new `toolResult` helper:
  `{"content":[{"type":"text","text":"<json>"}],"isError":false}`;
  previously returned raw Go values that serialised to plain JSON objects
  or arrays, causing MCP clients (including Claude Desktop) to silently
  discard the result or display empty output

---

## [1.0.3] — 2026-03-18

Fix `list_{type}s` response format: wrap slice in `{"items": [...]}` object.

### Fixed

- `tool.go`: `list` case in `handleToolsCall` now returns
  `map[string]any{"items": items}` instead of a raw `[]any`; a bare JSON
  array result caused MCP protocol validation errors in clients that
  interpret array-valued tool results as batch responses

---

## [1.0.2] — 2026-03-18

Admin read tools for MCPWrite modules.

### Added

- `mcp.go`: `mcpAdminReadToolDefs` generates two tools per MCPWrite module:
  `list_{type}s` (all items, optional `status` filter) and `get_{type}`
  (single item by slug); both tools return items at any lifecycle status
- `tool.go`: `authoriseEditor` role check (Editor or Admin); `moduleForAdminList`
  resolves the plural typeSnake used by `list_{type}s` tool names; `list` and
  `get` cases in `handleToolsCall`; `handleToolsList` updated to include admin
  read tools alongside write tools

---

## [1.0.1] — 2026-03-17

`inputSchema` and `inputSchemaUpdate` now emit the correct JSON Schema for
`[]string` fields (Amendment A52-2).

### Fixed

- `mcp.go`: `inputSchema` and `inputSchemaUpdate` now emit
  `{"type":"array","items":{"type":"string"}}` for fields with `Type == "array"`;
  previously emitted bare `{"type":"array"}` without an `items` declaration, and
  incorrectly applied `minLength`/`maxLength`/`enum` constraints to array fields
  (Amendment A52-2)

---

## [1.0.0] — 2026-03-17

Initial release of `forge-mcp` — MCP support for Forge apps (Milestone 10).

### Added

- `mcp.go`: `Server` struct; `New(app, opts...)` constructor; `ServerOption`
  interface; `WithSecret(secret []byte)` option; `handle` JSON-RPC dispatcher;
  `handleInitialize`; JSON-RPC wire types (`jsonRPCRequest`, `jsonRPCResponse`,
  `jsonRPCError`); `mcpTool`, `mcpResource`, `allResources`, `mcpToolDefs`,
  `inputSchema`, `inputSchemaUpdate` helpers; `hasMCPOp`, `slugOf`, `snakeCase`
  utilities
- `resource.go`: `handleResourceMethod`, `handleResourcesList`,
  `handleResourcesTemplatesList`, `handleResourcesRead`, `parseResourceURI`;
  `mcpResource`, `resourceContent`, `resourceTemplate` wire types;
  Published-only lifecycle enforcement for MCP resources/read
- `tool.go`: `handleToolMethod`, `handleToolsList`, `handleToolsCall`
  dispatcher (create/update/publish/schedule/archive/delete); `toolName`,
  `parseToolName`, `moduleForType`, `authorise`, `errorFor`, `stringArg`
  helpers; Author-level role enforcement; idempotent publish; delete response
  `{"deleted":true,"slug":...}`
- `transport.go`: `ServeStdio(ctx context.Context, in io.Reader, out io.Writer)`
  for local stdio transport (Claude Desktop, Cursor, CLI tools); `Handler()`
  returning an `http.Handler` with SSE keepalive (`GET /mcp`) and authenticated
  JSON-RPC endpoint (`POST /mcp/message`) for remote SSE transport; 1 MiB
  request body limit; HMAC Bearer token authentication via `forge.VerifyBearerToken`
