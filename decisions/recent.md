# Smeldr — Recent Decisions

Rolling working file. All new decisions are added here first.
When this file approaches ~20KB, report it at session start — the architect
will issue archiving instructions via NEXT.md.

Non-Decisions go directly to `nondecisions.md` — not here.

Archived 2026-05-25: D32 → docs.md · A87, A97–A101 → core.md
Archived 2026-05-30: A102–A115 → phase3-archive.md
Archived 2026-06-04: A116–A120 → phase3-archive.md
Archived 2026-06-05: A121–A125 → phase4-archive.md
Archived 2026-06-07: A126–A130 → phase5-archive.md
Archived 2026-06-09: A131–A135 → phase6-archive.md
Archived 2026-06-10: A136–A138 → phase7-archive.md

---

## A139 — `migrateLegacyTableNames` idempotency fix (core v1.36.2)

**Date:** 2026-06-09
**Status:** Agreed
**Level:** 2 (bug fix; behaviour change in `migrateLegacyTableNames` error path)

### Decision

`migrateLegacyTableNames` checked whether the source (`forge_*`) table existed,
but did not check whether the destination (`smeldr_*`) table already existed. If
both tables coexist — indicating a partial migration from a previous run — the
rename attempt failed with:

```
WARN smeldr: legacy table migration failed — rename forge_* tables manually
error="... there is already another table or index with this name: smeldr_audit_log"
```

The function's own docstring claimed "idempotent" — that claim was wrong.

Fix: in the `toRename`-building loop, a second `sqlite_master` query checks
whether the destination already exists. If it does, that pair is skipped with
`slog.Warn` and the loop continues. The docstring is updated to accurately
describe this behaviour.

### Files changed

- **`core/migrate.go`**: destination-existence check added; docstring updated.
- **`core/migrate_test.go`** (new): two tests:
  - `TestMigrateLegacyTableNames_destinationExists`: both `forge_tokens` and
    `smeldr_tokens` exist; function returns nil and no rename is attempted.
  - `TestMigrateLegacyTableNames_sourceOnly`: only `forge_tokens` exists;
    renamed to `smeldr_tokens` as normal.

### Version

core v1.36.2 (patch; no exported-symbol or interface change).

---

## A140 — X publisher debug logging (social v0.8.3)

**Date:** 2026-06-09
**Status:** Agreed
**Level:** 2 (new observable behaviour — slog output; no exported-symbol change)

### Decision

`twitter.go` had no `slog` calls. When a 403 or other unexpected status occurred
on media upload or tweet publish, there was no way to see the HTTP status, X's
`X-Request-Id`, or the response body without a debugger.

Fix: targeted logging in `uploadXMedia` and `publish`:
- `slog.Debug` immediately before each HTTP call: method and URL (access token
  never logged)
- `slog.Warn` on any non-2xx response: HTTP status, truncated body (≤256 chars),
  and the `X-Request-Id` response header from X

### Files changed

- **`social/twitter.go`**: `slog.Debug` + `slog.Warn` in `uploadXMedia`;
  `slog.Debug` + `slog.Warn` in `publish`. Import `"log/slog"` added.
- **`social/twitter_test.go`**: `TestPublish_logsWarnOnNonSuccess` — stub server
  returns 403; asserts WARN record with "non-2xx" in message. `slogCapture`
  helper struct implements `slog.Handler`.

### Version

social v0.8.3 (patch; no exported-symbol or interface change).

---

## A141 — X media upload: INIT/APPEND/FINALIZE chunked protocol

**Date:** 2026-06-10
**Status:** Agreed
**Level:** 1 (isolated fix in social package; no exported-symbol change)

### Decision

`uploadXMedia` in `social/twitter.go` sent a single multipart POST to the X v2
`/2/media/upload` endpoint without a `command` field. X's gateway rejects any
request missing `command` with a generic `{"title":"Forbidden","type":"about:blank"}`
403 and no `X-Request-Id` header — the request never reaches X's API handler,
making the failure undebuggable. This explains why all X media posts failed with
403 despite valid user tokens.

Fix: rewrite `uploadXMedia` to perform the three mandatory steps in sequence:
1. **INIT** — `command=INIT`, `media_type`, `total_bytes`, `media_category=tweet_image`
   → returns `media_id` in `{"data":{"id":"..."}}`.
2. **APPEND** — `command=APPEND`, `media_id`, `segment_index=0`, binary image bytes
   as `media` part → expects any 2xx (typically 204 No Content).
3. **FINALIZE** — `command=FINALIZE`, `media_id` → confirms upload; returns same
   response structure as INIT.

For `tweet_image`, processing is synchronous — no `processing_info` polling needed
after FINALIZE. Existing `slog.Debug`/`slog.Warn` calls (A140) applied to each of
the three HTTP calls. `strconv` import added for `strconv.Itoa(len(imgBytes))`.
No signature or exported-symbol change.

### Files changed

- **`social/twitter.go`**: `uploadXMedia` rewritten with three-step protocol.
  `strconv` import added. Comment updated to describe chunked protocol.
- **`social/twitter_test.go`**: `TestUploadXMedia` happy-path handler updated to
  serve three distinct commands (INIT→201, APPEND→204, FINALIZE→200) using an
  `atomic.Int32` call counter; asserts all INIT/APPEND/FINALIZE field values.
  Two new error sub-cases added: "APPEND 403 returns terminal publishError" and
  "FINALIZE 403 returns terminal publishError".

### Version

social v0.8.4 (patch; no exported-symbol or interface change).

---

## A142 — X OAuth scope configurable (social v0.8.5)

**Date:** 2026-06-10
**Status:** Agreed
**Level:** 1 (isolated change in social package; no exported-symbol change)

### Decision

`xConfig` had no `Scopes` field — the OAuth 2.0 scope string was hardcoded in `authURL()` omitting `media.write`. This caused 403 on every INIT request to the X v2 media upload endpoint. `PlatformConfig.Scopes` already existed and was persisted, but was never threaded into `xConfig`.

Fix: add `Scopes []string` to `xConfig`; add `effectiveScope()` method returning default (with `media.write`) when empty or joined custom scopes; update `authURL()` to call `effectiveScope()`; thread `cfg.Scopes` in both `xConfig` construction sites; update `scope` field description in `config_mcp.go`.

### Files changed

- **`social/twitter.go`**: `Scopes []string` on `xConfig`; `effectiveScope()` method; `authURL()` uses it; `"strings"` import.
- **`social/social.go`**: `Scopes: dbCfg.Scopes` in `New()` and `reloadPlatformClient()`.
- **`social/config_mcp.go`**: `scope` description updated to cover both X and Mastodon defaults.
- **`social/twitter_test.go`**: `TestEffectiveScope` (2 cases).

### Version

social v0.8.5 (patch; no exported-symbol or interface change).

---

## A143 — Streamable HTTP SSE response mode (mcp v1.18.0)

**Date:** 2026-06-10
**Status:** Agreed
**Level:** 1 (isolated change in mcp/transport.go; no exported-symbol change)

### Decision

`messageHandler` always responded with `Content-Type: application/json`, ignoring `Accept`. MCP 2025-11-25 streamable HTTP spec requires that when a client sends `Accept: text/event-stream` on `POST /mcp`, the server responds with `Content-Type: text/event-stream` and streams the JSON-RPC response as a single SSE event. Claude.ai web requires this for OAuth-authenticated MCP connections.

Fix: after dispatching via `s.handle()`, branch on `strings.Contains(r.Header.Get("Accept"), "text/event-stream")`. SSE path: `text/event-stream` header + `data: <json>\n\n` + flush. JSON path (default): unchanged.

### Files changed

- **`mcp/transport.go`**: last three lines of `messageHandler` replaced with Accept-based branch.
- **`mcp/mcp_test.go`**: `TestMessageHandler_AcceptSSE_ReturnsEventStream` + `TestMessageHandler_NoAccept_ReturnsJSON`.

### Version

mcp v1.18.0 (minor; new observable behaviour).

---

## A144 — media StatsExtProvider (media v1.5.0)

**Date:** 2026-06-10
**Status:** Agreed
**Level:** 2 (new exported-interface implementation; new file)

### Decision

`smeldr.dev/media` deferred implementing `StatsExtProvider` when the interface was introduced in core v1.35.0 (A126). The `forge_media` table has `mime_type` and `size_bytes` columns — everything needed.

Fix: add `stats.go` to media package implementing `StatsKey() string` (returns `"media"`) and `ProvideStats(ctx) (map[string]any, error)` on `*Server`. Two queries: `COUNT(*) + COALESCE(SUM(size_bytes), 0)` for `file_count`/`total_bytes`; `GROUP BY mime_type` for `by_type` breakdown.

### Files changed

- **`media/stats.go`**: NEW — `StatsExtProvider` implementation on `*Server`.
- **`media/stats_test.go`**: NEW — `TestProvideStats` + `TestProvideStats_empty`.

### Version

media v1.5.0 (minor; new exported interface implementation).

---

## A145 — T94: content-type instances as block-section parents (core v1.37.0 · mcp v1.19.0)

**Date:** 2026-06-10
**Status:** Agreed
**Level:** 2 (new exported interface + option; no breaking changes)

### Decision

The block system (T32) only accepted `DynamicNode` IDs as `parent_id` in `add_section`/`add_item`. Content-type instances (Post, Essay, custom types) could not host block sections without being stored as DynamicNodes — defeating the purpose of the native content type.

Fix: introduce `ContentParentProvider` interface and `BlockHost()` option. Modules opt in via `app.Content(m, smeldr.BlockHost())`. The MCP `resolveParentType()` helper tries the DynamicNode repo first; if not found, iterates registered `ContentParentProvider` instances; returns -32602 if no provider claims the ID. Body and sections coexist as independent data paths (body in the module's own table; sections in `smeldr_content_edges`).

Ride-along T98: mojibake em-dashes in `integration_full_test.go` G-index legend and section headers replaced with plain hyphens.

### Files changed (core)

- **`block_host.go`**: NEW — `ContentParentProvider` + `blockHostProvider` interfaces.
- **`block_host_test.go`**: NEW — 6 unit tests.
- **`forge.go`**: `blockParents []ContentParentProvider` on App; `blockHostProvider` check in `Content()`.
- **`module.go`**: `BlockHost() Option` + `blockHost bool` field + `blockHostEnabled()`/`BlockParentTypeName()`/`HasBlockParent()` methods on `Module[T]`.
- **`stats.go`**: `RegisterBlockParent()` + `BlockParents()` on App.
- **`integration_full_test.go`**: T98 mojibake fix.
- **`CHANGELOG.md`**: `[1.37.0]` entry.
- **`DECISIONS.md`**: A145 index row.

### Files changed (mcp)

- **`edge_tools.go`**: `resolveParentType()` helper + updated `addEdge()` + updated tool description.
- **`edge_tools_test.go`**: 3 new tests (ContentInstance parent, unknown parent, DynamicNode unaffected).
- **`mcp.go`**: `blockParents []smeldr.ContentParentProvider` on Server; `WithBlocks()` populates from `app.BlockParents()`.
- **`CHANGELOG.md`**: `[1.19.0]` entry.

### Version

core v1.37.0 (minor; new exported symbols: `ContentParentProvider`, `BlockHost`, `RegisterBlockParent`, `BlockParents`) · mcp v1.19.0 (minor; `add_section`/`add_item` accept content-instance parents).

---

## A146 — T97: Schema-defined block types (core v1.38.0 · mcp v1.20.0)

**Date:** 2026-06-10
**Status:** Agreed
**Level:** 1 (new exported symbols, new MCP tools; additive — no breaking changes)

### Decision

Block types in the T32 system lacked a machine-readable field specification. Content authors using `create_node` had no way to discover what fields a type expected, and the MCP layer could not validate calls or generate typed shorthand tools. T97 introduces a schema layer addressing both gaps without breaking any existing callers.

### Core: `schemas.go` (new)

**Types**: `SchemaField` (`Name`, `Type`, `Required`, `Format`, `Description` — JSON Schema type names); `ContentTypeSchema` (`ID`, `TypeName`, `Label`, `Fields json.RawMessage`, `CreatedAt`, `UpdatedAt`).

**`SchemaStore`** — `FindByTypeName(ctx, typeName)` (returns `ErrNotFound` when absent) and `All(ctx)` (all schemas ordered by `type_name`).

**`CreateSchemaTable(db)`** — `CREATE TABLE IF NOT EXISTS smeldr_content_type_schemas`. Idempotent. Called separately from `CreateBlockTables` by the operator at startup.

**`SeedBlockTypeSchemas(db)`** — `INSERT OR IGNORE` seeds 16 canonical block types: `content_block`, `image`, `link_item`, `html_block`, `quote`, `contact_card`, `faq_item`, `content_grid`, `gallery`, `link_collection`, `html_grid`, `faq`, `team`, `hero`, `footer`, `content_list`. Idempotent. Fields column stored as BLOB (`json.RawMessage`, not `string`) for SQLite driver scan compatibility.

**`ValidateBlockFields(schema, fields)`** — rejects unknown fields (`"unknown field %q for type %q"`) and missing required fields (`"required field %q missing for type %q"`). `ErrNotFound` on schema lookup → pass-through (backwards compat). Nil/empty fields treated as `{}`.

### MCP: `schema_tools.go` (new)

**`get_content_type_schema(type_name)`** — returns `{type_name, label, fields: []SchemaField}` for one type; -32602 when not found.
**`list_content_type_schemas()`** — returns `{items: [{type_name, label}]}` for all 16 types. Both tools require Author role.

### MCP: `typed_tools.go` (new)

**`generateTypedTools(schemas)`** — generates one `create_<type_name>` tool per schema at startup. Named, typed parameters derived from field definitions; required fields mapped to JSON Schema `"required"` array.

**`handleTypedTool(ctx, name, args)`** — `args` IS the fields dict (not nested under `"fields"`); marshals, validates against schema, saves `DynamicNode`. `typeName = strings.TrimPrefix(name, "create_")`. Intercepted before `parseToolName + moduleForType` to prevent "unknown tool" fallthrough.

### MCP: `mcp.go` + `node_tools.go` + `tool.go`

`Server` gains `schemaStore`, `typedTools`, `typedToolSet`. `WithBlocks()` extended to construct `SchemaStore` and generate typed tools at startup; schema table missing → graceful degradation (typed tools nil, no error).

`create_node` and `update_node` validate fields against schema when `schemaStore != nil`; `ErrNotFound` → pass-through (backwards compat for unregistered types).

### Tests

`schemas_test.go` (core, 10 tests): idempotency, 16-type seed verification, `FindByTypeName`/`NotFound`, `ValidateBlockFields` accept/reject/empty. `schema_tools_test.go` (mcp, 9 tests): schema discovery tools, typed tool presence at startup, create via typed tool, missing-required rejection, `create_node` schema validation pass/fail/passthrough.

### Design decisions

**Supplement, not replace**: `create_node` unchanged and never removed. Schema validation is additive — previously-valid calls continue to work. Typed tools are the preferred path but not the only one.

**Seed-only for T97**: No MCP tool to write/modify schemas in this release. Schema writability is T55/T49 territory.

### Version

core v1.38.0 (minor — new exported symbols: `SchemaField`, `ContentTypeSchema`, `SchemaStore`, `NewSchemaStore`, `CreateSchemaTable`, `SeedBlockTypeSchemas`, `ValidateBlockFields`) · mcp v1.20.0 (minor — new tools: `get_content_type_schema`, `list_content_type_schemas`, `create_<type_name>` × 16).
