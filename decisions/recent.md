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
Archived 2026-06-15: A139–A150 → phase8-archive.md

---

## A165 — T06 step 7: Layer 3a structural sweep (v1.42.8, 2026-06-21)

**Context:** T06 needs a DB-only engine to find relations whose targets are no longer live (published). Layer 3a — no LLM, no AgentJob rows.

**Decision:** Add `TargetChecker` func type: `func(ctx context.Context, targetType, targetID string) (alive bool, err error)`. Add `RelationStore.SweepStructural(ctx, check TargetChecker, onStale func(ctx, edge RelationEdge)) (flagged, skipped int, err error)` — iterates all active relations (`invalid_at IS NULL OR invalid_at > now` AND `valid_at IS NULL OR valid_at <= now`), deduplicates targets via map to minimise checker calls, calls caller-supplied `TargetChecker` once per unique target, sets `invalid_at = now` on stale edges, fires `onStale` callback per flagged edge. Add `App.SweepStructural(ctx) (flagged, skipped int, err error)` — convenience wrapper with nil-store guard. Default `TargetChecker` queries `smeldr_dynamic_content` for `status = 'published'`; default `onStale` emits `AfterRelationCascade` signal.

**What it does NOT do:** no LLM calls, no AgentJob rows, no MCP calls inside the sweep, no transitive traversal (one hop only), does not know which content tables exist (TargetChecker's responsibility).

**Deferred:** cron scheduling lives in `smeldr/agent` (Layer 3b — separate step).

---

## A163 — T06 step 6: MCP relation kind tools (v1.42.6, 2026-06-20)

**Context:** T06 agents need to manage the relation kind registry through MCP — not just query and assert edges, but also register and inspect kinds dynamically.

**Decision:** Add two thin wrapper methods to `RelationStore`: `MCPUpsertRelationKind` and `MCPListRelationKinds`. Both delegate to existing `UpsertKind`/`ListKinds`. `MCPUpsertRelationKind` returns the stored `RelationKindDef` (with generated ID and timestamps) so callers don't need a follow-up `GetKind`. `MCPListRelationKinds` always returns a non-nil slice to simplify forge-mcp serialisation.

**Completeness:** These two tools complete the MCP surface for T06. Agents can now register kinds and assert/query/preview relations entirely through MCP without touching application code.

**Deferred:** `delete_relation_kind` (requires cascade handling on existing edges), `get_relation_kind` (agents can use list + client-side filter).

---

## A162 — T06 step 5: MCP relation tools (v1.42.5, 2026-06-20)

**Context:** T06 relation graph needs MCP-accessible tools so agents and operators can assert, query, and preview edges without direct DB access.

**Decision:** Add four methods to `RelationStore`: `MCPAssertRelation`, `MCPProposeRelation`, `MCPGetRelations`, `MCPPreviewImpact`. Wired via `App.RelationStore()` — forge-mcp checks non-nil to gate registration. No new interface required; pattern mirrors `RedirectDB()`.

**insertEdge refactor:** Shared unexported `insertEdge(ctx, edge) (RelationEdge, error)` extracted from `Assert`. Returns the populated edge with generated ID and timestamps. Both `Assert` and the MCP methods use it; `Assert` behaviour is unchanged.

**propose_relation:** Stores `edge_class="inferred"` by calling `insertEdge` directly, bypassing `Assert`'s edge_class guard. The inferred edge is NOT automatically asserted — pending human or agent review.

**Auth:** No role-check in RelationStore layer. Authorization is enforced by forge-mcp at tool-invoke time, identical to all other tools.

**preview_impact:** Read-only dry-run. Calls `GetByTarget(ctx, type, id, "")` and returns source-side dependents. No signals are fired.

**Scope deferred:** `upsert_relation_kind` DDL tool, Layer 3 sweep trigger, bulk import.

---

## A161 — T06 step 4: Layer 2 reactive cascade signal (v1.42.4, 2026-06-20)

**Context:** T06 Layer 2 requires that content items depending on a target are notified when that target changes status — without the dependent being re-saved.

**Decision:** Add `AfterRelationCascade Signal = "relation.cascade"`. `App.Relations()` subscribes `buildCascadeHandler` to `AfterPublish`, `AfterArchive`, `AfterDelete`, and `AfterUnpublish`. The handler calls `GetByTarget` to find source-side dependents, applies visited-set and idempotency guards, and fires `AfterRelationCascade` once per unique source item via a per-source debouncer (500 ms).

**SignalEvent.NodeID:** Added `NodeID string` field to `SignalEvent`, populated from `Node.ID` in `buildSignalEvent`. Required for `GetByTarget(ctx, targetType, targetID)` to resolve the target by UUID rather than slug. Non-breaking addition — previously absent, now populated for all signals.

**Semantic field reuse in AfterRelationCascade:** `PreviousState` carries the trigger signal name (e.g. `"after_archive"`); `ActorID` carries the changed target item's NodeID. Documented semantic mismatch — acceptable to avoid adding new fields to SignalEvent.

**Debounce pattern:** One `*debouncer` per `"sourceType:sourceID"` key in a `sync.Map`. Debouncer fires after 500 ms of silence, then deletes itself from the Map to bound memory. Mirrors the sitemap debouncer pattern in module.go.

**emitSignal:** Unexported `App.emitSignal(ctx, sig, ev)` wraps `dispatchBus` for use inside signal handler goroutines. The cascade debouncer fires in a timer goroutine — `emitSignal` is safe to call there because it does not re-enter the bus goroutine.

**Guards:** (1) visited-set per run deduplicates by source item; (2) idempotency-set per run deduplicates by edge ID; (3) depth = 1 enforced by subscribing only to status-change signals, not to AfterRelationCascade itself.

**Scope deferred:** Layer 3 (structural sweep), MCP tools, inferred edges, multi-hop cascade.

---

## A160 — T06 step 3: Layer 1 save-path relation recompute (v1.42.3, 2026-06-20)

**Context:** T06 Layer 1 requires that asserted relations are kept in sync with content field values on every save, without the operator calling `Assert` explicitly.

**Decision:** Add `RecomputeAsserted` to `RelationStore` — a differential algorithm (SELECT current → diff → delete stale + insert new). Common case costs exactly 1 SELECT and 0 writes. Wire it into `createHandler`, `updateHandler`, `MCPCreate`, and `MCPUpdate` via a new `SyncSaveHook` type on `App`. The hook is synchronous: errors abort the request.

**Schema resolution:** The hook closure calls `SchemaStore.FindByTypeName` to read the item's content-type schema. Returns nil immediately for compiled types (no schema registered → ErrNotFound). For dynamic types, finds all `SchemaField` entries with `Relation: "edge"`, unmarshals the item's `DynamicNode.Fields` JSON, and extracts target IDs.

**target_type resolution:** `extractRelationEdges` looks up the registered `RelationKindDef` by field name (the field name IS the relation kind). `TypePairs[0].TargetType` gives the target type. Skips if kind not registered or TypePairs empty.

**Hook wiring:** `App.Content()` collects modules implementing `setSyncSaveHook`. `App.Handler()` wires the hook into all collected modules after startup — mirrors the `afterHook`/`setAfterHook` pattern.

**BulkRecompute:** Phase 1 = all SELECTs + diff computation. Phase 2 = all deletes + inserts. Intended for post-import scenarios; not called from the save path.

**Scope deferred:** Layer 2 (AfterPublish signal, cascade guards), Layer 3 (structural sweep), MCP tools, `DynamicTypeRepo` direct paths (status-only saves skip hook).

---

## A159 — T06 step 2: relation schema + stores (v1.42.2, 2026-06-20)

**Context:** T06 content-relations needs a persistent, queryable edge store and a runtime registry of relation kinds before any layer can assert, derive, or validate edges.

**Decision:** Add `smeldr_relation_kinds` (kind registry) and `smeldr_relations` (edge store) as separate tables. `RelationEdge` does not embed `Node` — relations are graph edges, not content items.

**Registry:** `RelationKindRegistry` is an in-memory map hydrated from the DB at `NewRelationStore` time. `UpsertKind` updates the DB and the registry atomically (Lock). `GetKind` and `ListKinds` are registry-only (RLock, no DB round-trip).

**CAS on upsert:** `UpsertKind` uses `INSERT ... ON CONFLICT (type_name) DO UPDATE SET` (house style — not `INSERT OR REPLACE`, which is SQLite-only). `id` and `created_at` are preserved on conflict.

**Assert-only for now:** `Assert` rejects `edge_class != "asserted"` — inferred edges (`ProposeRelation`) are deferred to Layer 2.

**Scope deferred:** Layer 1 (save-path edge recompute from `SchemaField.Relation`), Layer 2 (AfterPublish signal subscriptions, cascade guards), Layer 3 (structural sweep), MCP tools (`assert_relation`, `get_relations`, `preview_impact`), `BulkRecompute`.

---

## A158 — Node.Rev optimistic-concurrency token (v1.42.1, 2026-06-20)

**Context:** T06 (content relations) requires a collision-free `(edge, node-state-version)` key and a way to resolve the concurrent-append `SortOrder` race in `edges.go`. A per-node revision counter solves both.

**Decision:** Add `Rev int \`db:"rev"\`` to `smeldr.Node`. The storage layer owns Rev — it is always `0` on first insert and incremented on every subsequent save. Callers must not set Rev manually.

**CAS in SQLRepo:** `INSERT … ON CONFLICT DO UPDATE SET … WHERE table.rev = $N`. If `RowsAffected = 0` the caller receives `ErrRevConflict` (HTTP 409) and must reload before retrying.

**MemoryRepo:** increment-only via reflection on the pointer receiver; no CAS (test repo does not simulate concurrent writers).

**Migration:** `MigrateNodeRevColumn(db, table)` — PRAGMA probe + `ALTER TABLE … ADD COLUMN rev INTEGER NOT NULL DEFAULT 0`. Operators must call it once per existing Node-embedding table at startup.

**Rejected:** a global revision table (cross-table coordination overhead); embedding Rev only in a sub-interface (forces type assertions in storage layer).

---

## A151 — T104 Increment 1: schema generalisation + content-type registry (core v1.39.0)

**Date:** 2026-06-15
**Status:** Agreed
**Level:** 2 (new exported symbols; no breaking changes)

### Decision

T104 Increment 1 generalises the schema layer and introduces the content-type
registry. Five deliverables shipped together:

**1. `kind` column on `smeldr_content_type_schemas`**
New `TEXT NOT NULL DEFAULT 'block'` column discriminates block types from
content types. Added to `CreateSchemaTable` DDL. `MigrateSchemaKindColumn(db)` adds
the column to existing databases via `PRAGMA table_info` probe (idempotent, safe on
every boot). `ContentTypeSchema` struct gains `Kind string \`db:"kind"\``.

**2. `SchemaField.Role` and `SchemaField.Relation`**
Two new fields in the schema field JSON:
- `Role string` — semantic seam: `"title"` / `"description"` / `"og_image"` /
  `"body"` / `"summary"`. At most one field per schema may carry each role.
  Read by T72 (head/SEO), T96 (summary cards), and the render path.
- `Relation string` — forward-compat placeholder marking a field as a future
  T06 edge-backed relation. Non-empty value = "this field will become a T06 edge".
  Distinct from a render reference (`{Name}ID` in Fields).

**3. `ValidateFields` (renamed from `ValidateBlockFields`)**
`ValidateBlockFields` → `ValidateFields` with a back-compat alias:
- Type checking: `string`, `integer` (whole numbers), `boolean`, `array`, `object`.
- URL format: strings with `Format: "url"` that start with `"/"` are accepted as
  relative paths; other non-empty strings validated with `url.Parse`.
- Duplicate role rejection: returns an error when two schema fields carry the same
  non-empty `Role`.
- JSON `null` passes type checking (required check catches it separately).

**4. `ContentTypeRegistry` + `TypeDescriptor` + `App.TypeRegistry()`**
`registry.go` (new file):
- `TypeDescriptor` — extensible type envelope: `Name` (PascalCase for compiled /
  snake_case for runtime), `Prefix` (URL prefix), `Schema` (`*ContentTypeSchema`;
  nil for compiled modules in this increment), `Kind` (`"block"` / `"content"`).
- `ContentTypeRegistry` — concurrency-safe registry (RWMutex); dual key-space;
  prefix uniqueness enforced globally.
- Methods: `Register(d)` (panics on dup name or prefix), `RegisterPrefix(prefix, name)`
  (idempotent for same-name, panics on prefix claimed by different type),
  `Lookup(name)`, `LookupByPrefix(prefix)`, `All()`.
- `App.TypeRegistry()` — returns the app's `*ContentTypeRegistry`.
- `App.Content()` auto-registers compiled modules at `Content()` time with
  soft-dup guard: first call wins, subsequent calls for the same TypeName call
  `RegisterPrefix` instead of `Register`.

**5. `idx_dynamic_content_type_status` index**
`CREATE INDEX IF NOT EXISTS idx_dynamic_content_type_status ON smeldr_dynamic_content
(type_name, status, published_at DESC)` added to `CreateBlockTables`. Supports
efficient type+status queries for ContentList resolver (T96) and future dynamic
content tools.

### Tests (21 new tests)

`schemas_test.go`: `TestMigrateSchemaKindColumn` (3 cases: fresh DB, pre-T104 DB,
idempotent re-run), `SeedBlockTypeSchemas_KindDefaultsToBlock`,
`SchemaField_RoleAndRelationRoundtrip`, `ValidateFields` type checks (5 cases),
URL format (2 cases), `DuplicateRoleRejected`, `NullValueSkipsTypeCheck`,
`ValidateBlockFields_Alias`.

`registry_test.go` (via `schemas_test.go` helpers): `RegisterAndLookup`,
`LookupByPrefix`, `LookupMissing`, `DuplicateNamePanics`, `DuplicatePrefixPanics`,
`All` (via `testRegistry` helper using `smeldr.New(...).TypeRegistry()`).

### Design frame

See `architect/design/content-type-model.md` — this increment ships the descriptor
envelope and the registry linchpin (steps 1 + 2 of the sequencing plan). T96
(ContentList resolver) is the first consumer.

### Version

core v1.39.0 (minor — new exported symbols: `MigrateSchemaKindColumn`, `ValidateFields`,
`ContentTypeRegistry`, `TypeDescriptor`, `App.TypeRegistry()`, `LookupByPrefix`,
`RegisterPrefix`, `ValidateBlockFields` alias). Branch: feat/t104-registry (fbb8442).

---

## A152 — T96: ContentList block resolver (core v1.40.0)

**Date:** 2026-06-15
**Status:** Agreed
**Level:** 2 (new exported symbols; no breaking changes)

### Decision

T96 wires the `content_list` block type to the `ContentTypeRegistry` so that a CMS
block can dynamically inject Published content items at render time.

**1. `ContentLister` interface**
New unexported-method interface in `registry.go`:
```go
type ContentLister interface {
    listPublished(ctx context.Context, opts ListOptions) ([]map[string]any, error)
}
```
`Module[T]` implements `ContentLister` via a new `listPublished` method added to
`module.go`. Type erasure is via JSON marshal→unmarshal to `map[string]any`; the
variable is named `row` (not `m`) to avoid shadowing the `m` receiver.

**2. `TypeDescriptor.Fetch`**
New field on `TypeDescriptor` (registry.go):
```go
Fetch func(ctx context.Context, opts ListOptions) ([]map[string]any, error)
```
Wired at `App.Content()` time: after registering the module's `TypeDescriptor`,
if the module implements `ContentLister`, `desc.Fetch = cl.listPublished` is set.

**3. BlockRenderer registry + ContentList resolver (serveblocks.go)**
- `BlockRenderer` gains `registry *ContentTypeRegistry` field.
- `App.ServeBlocks` passes `registry: a.typeRegistry` in the struct literal.
- `renderBlock` signature extended with `ctx context.Context` as first parameter;
  all internal callers updated (`Render` loop + recursive call).
- After reference-field resolution, a `content_list` special case:
  reads `ContentType` from block data → `LookupByPrefix` → if `Fetch != nil` →
  calls `Fetch(ctx, contentListOpts(data))` → sets `data["Items"]`.
  Graceful skips (no items, no error): unknown type, nil Fetch, empty ContentType,
  Fetch error (logged).
- Normal child-edge Items path runs only when ContentList resolution was not
  performed (`resolvedContentList` flag).
- `contentListOpts(data map[string]any) ListOptions` helper: `Limit` → `PerPage`,
  `Page` → `Page`; `SortField` "published_at"/"created_at"/"title" → `OrderBy`;
  `SortDir` "desc" → `Desc=true`; JSON float64 → int conversion.
- `slog.Warn` on each silent fall-through: empty ContentType, unknown ContentType
  (LookupByPrefix nil), nil Fetch (dynamic type not yet supported), FilterTags
  non-empty (field present but not yet supported). Fetch error also logs Warn.

### Tests (9 new tests in serveblocks_test.go)

`TestContentList_Resolves` — happy path: content_list block with ContentType="posts",
Fetch returns 3 items, Items appear in order.
`TestContentList_LimitPassedToFetch` — Limit=5 in block data → ListOptions.PerPage=5.
`TestContentList_UnknownContentType` — no descriptor in registry → no items.
`TestContentList_NilFetch` — descriptor registered, Fetch=nil → no items.
`TestContentList_EmptyContentType` — empty ContentType field → Fetch not called.
`TestContentListOpts` — Limit+Page round-trip.
`TestContentListOpts_Defaults` — empty data → zero opts.
`TestContentListOpts_SortField` — published_at/created_at/title → OrderBy; unknown ignored.
`TestContentListOpts_SortDir` — desc→Desc=true; asc→Desc=false.

### Version

core v1.40.0 (minor — new exported symbols: `ContentLister`, `TypeDescriptor.Fetch`).
Branch: feat/t96-contentlist.

---

## A153 — T104 Inc 2: dynamic content substrate (core v1.41.0)

**Date:** 2026-06-16
**Status:** Agreed
**Level:** 2 (new exported symbols; no breaking changes)

### Decision

T104 increment 2 adds the full dynamic content substrate: per-type CRUD repositories
backed by `smeldr_dynamic_content`, a `ServeDynamicContent()` app method that registers
public and admin HTTP routes, and the `DefineContentType` + `DynamicContentRepo` app
methods for operator use.

**1. Exported helpers (`dynamic.go`)**
- `PluralSnake(name string) string` (exported from unexported `pluralSnake`) — English
  plural for snake_case type names; consonant+y → -ies rule; all others get plain -s.
- `ValidateSchemaDef(schema *ContentTypeSchema) error` (exported from `validateSchemaDef`)
  — validates TypeName (required), field types (6 known: string/integer/boolean/array/object/number),
  and Role values (title/description/og_image/body/summary).

**2. `DynamicTypeRepo` (`dynamic.go`)**
Per-type repository backed by `smeldr_dynamic_content`:
- `CreateDraft(ctx, schema, fields)` — derives slug from title field; collision-safe via
  `uniqueSlug` (counter suffix up to 100); validates required fields via schema.
- `GetBySlug(ctx, slug)` → `map[string]any` or `ErrNotFound`
- `GetByID(ctx, id)` → `map[string]any` or `ErrNotFound`
- `List(ctx, opts ListOptions)` → `[]map[string]any` (status filter, pagination, ordering)
- `UpdateFields(ctx, id, fields)` — PATCH semantics (merge, not replace); re-validates required
- `SetStatus(ctx, id, status)` — draft → published → archived; updates `published_at` on publish

**3. App methods**
- `App.DefineContentType(schema *ContentTypeSchema) error` — saves schema to
  `smeldr_content_type_schemas`, registers `TypeDescriptor{Kind:"content"}` in the type
  registry, claims URL prefix `"/" + PluralSnake(schema.TypeName)`. Returns error on
  duplicate, nil DB, or invalid schema.
- `App.DynamicContentRepo(typeName string) (*DynamicTypeRepo, error)` — returns a typed
  repo for a registered content type. Rejects compiled (`Kind != "content"`) types.
- `loadDynamicTypes(ctx, db, app)` — loads all `kind="content"` schemas from DB on boot,
  calls `DefineContentType` for each; idempotent (skips already-registered types and prefixes).

**4. `App.ServeDynamicContent()` (`smeldr.go`)**
Panics if `Config.DB` is nil. Calls `loadDynamicTypes` then registers:
- **Public:** `GET /{slug}` (always; checks registry), `GET /{seg1}/{seg2}` (only when enabled)
- **Admin** (`/_content/*`, Editor+ auth, only when `dynamicContentEnabled && !dynamicContentReg`):
  - `POST /_content/{prefix}` — create draft
  - `GET /_content/{prefix}` — list items (pagination via `?page=` / `?per_page=`)
  - `GET /_content/{prefix}/{id}` — get item by ID
  - `PATCH /_content/{prefix}/{id}` — update fields
  - `POST /_content/{prefix}/{id}/status` — set status
- All handlers reject unknown prefixes (404) and compiled types (404).

### Tests

- `dynamic_test.go` (new, 40+ tests) — `DynamicTypeRepo` CRUD, `PluralSnake` (12 cases),
  `ValidateSchemaDef` (13 cases), `SchemaStore.AllByKind`, `MigrateSchemaKindColumn`
- `dynamic_app_test.go` (new, 50+ tests) — `DefineContentType`, `DynamicContentRepo`,
  `loadDynamicTypes`, `ServeDynamicContent` panic, public HTTP routing, all 6 admin endpoints
- `schemas_test.go` — 2 new tests: `ValidateBlockFields` invalid schema/fields JSON
- `dynamic_app_test.go` also covers `TypeRegistry.All()` and `RegisterPrefix` idempotent path

Coverage: 96.0% (gate ≥96.0% ✓)

### Version

core v1.41.0 (minor — new exported symbols: `PluralSnake`, `ValidateSchemaDef`,
`DynamicTypeRepo`, `App.DefineContentType`, `App.DynamicContentRepo`, `App.ServeDynamicContent`).
Branch: feat/t104-dynamic.

---

## A154 — T104 Inc 2 patch: operator-controlled URL routing (core v1.41.1)

**Root cause of A153 bug:** `ServeDynamicContent()` registered `GET /{seg1}/{seg2}` as
a catch-all wildcard that conflicted with literal 2-segment paths (e.g. `GET /static/`)
in Go 1.22's `ServeMux`. The deeper issue: auto-deriving the public URL from the type name
(`PluralSnake`) is wrong — URL structure is the operator's decision.

### Changes

**`schemas.go`**
- `ContentTypeSchema.URLPrefix string` (db: `url_prefix`) — public URL prefix; empty = admin-only, no public routes.
- `MigrateURLPrefixColumn(db DB) error` — idempotent `PRAGMA table_info` probe; adds `url_prefix TEXT NOT NULL DEFAULT ''` when missing.
- `CreateSchemaTable` DDL: `url_prefix` column added.
- `SchemaStore.Save`: upserts include `url_prefix`.
- `ValidateSchemaDef`: rejects non-empty URLPrefix that does not start with `"/"`.

**`dynamic.go`**
- `DefineContentType`: derives `prefix = schema.URLPrefix` (may be empty); when non-empty, registers `GET prefix` + `GET prefix/{slug}` on the mux. Empty prefix = admin-only type.
- `loadDynamicTypes`: same prefix logic; warns + skips on URLPrefix collision (not type-name collision).
- Admin handlers: all 5 changed from `{prefix}` (URL prefix) to `{type}` (type_name) path variable; `LookupByPrefix` → `Lookup`.
- `newDefineTypeHandler`: decodes `url_prefix` from JSON body, passes to schema.
- `rebuildDynamicSitemap(ctx, desc)`: called in goroutine after `SetStatus`; writes XML fragment for all Published items to `sitemapStore`; no-op if `desc.Prefix == ""` or `sitemapStore == nil`.

**`smeldr.go`**
- Removed `GET /{seg1}/{seg2}` catch-all wildcard (root cause of mux conflict).
- `GET /{slug}` handler no longer dispatches through `LookupByPrefix`; falls through to standalone modules + redirect store.
- Admin routes: `/_content/{prefix}` → `/_content/{type}`.
- `ServeDynamicContent()`: calls `MigrateURLPrefixColumn`; initialises `sitemapStore` if nil.

**`serveblocks.go`**
- `ContentList` resolver: `LookupByPrefix(ct)` → `Lookup(ct)`; block data `ContentType` field now holds `type_name` (not URL prefix).

### Breaking change (from A153)

Operators using the A153 content block API must update `ContentType` in `content_list` block data from URL prefix (e.g. `"posts"`) to type_name (e.g. `"post"`). The A153 public routing (`GET /pluralname`) is replaced by explicit `URLPrefix` on the schema.

### Tests

17 new tests across `dynamic_app_test.go`, `dynamic_test.go`, `serveblocks_test.go`, `schemas_test.go`:
- `TestDefineContentType_WithURLPrefix`, `TestDefineContentType_NoURLPrefix`
- `TestLoadDynamicTypes_URLPrefix`, `TestLoadDynamicTypes_URLPrefixCollision`, `TestLoadDynamicTypes_SlugRoute`, `TestLoadDynamicTypes_DBError`
- `TestServeDynamicContent_NoPanic_WithStaticRoute`
- `TestAdminRoutes_TypeName`
- `TestRebuildDynamicSitemap`, `TestRebuildDynamicSitemap_NoPrefix`
- `TestContentList_UsesTypeName` (serveblocks_test.go)
- `TestValidateSchemaDef_URLPrefix_BadFormat`, `TestValidateSchemaDef_URLPrefix_Valid`
- `TestMigrateURLPrefixColumn_AddsColumn`, `TestMigrateURLPrefixColumn_NonSQLite`
- `TestDynamicContentRepo_NilDB_Registered`
- Fixed: `TestContentList_Resolves`, `TestContentList_LimitPassedToFetch`, `TestContentList_NilFetch`, `TestContentList_EmptyContentType` — ContentType field updated to type_name

Coverage: 96.0% (gate ≥96.0% ✓)

### Version

core v1.41.1 (patch — no new exported symbols; `ContentTypeSchema.URLPrefix` field added;
`MigrateURLPrefixColumn` exported). Branch: fix/t104-dynamic-routing.

---

## A155 — security: SSRF fix in outbound webhook transport (core v1.41.2)

**Finding:** code review identified two issues in `outbound.go`: (1) `outboundClient`
blocked redirects but allowed direct connections to private IP ranges — an operator with
Editor access could register a webhook at `http://169.254.169.254/` and probe the internal
network; (2) a comment falsely claimed "SSRF validation performed at endpoint creation time"
when no such validation existed.

### Changes

**`outbound.go`**
- `ssrfSafeDialContext()` — new unexported func returning `DialContext`. Resolves hostname
  via `net.DefaultResolver.LookupIPAddr` before connecting; rejects if any resolved IP is
  loopback, RFC1918, link-local, unspecified, CGNAT (100.64.0.0/10), or IPv6 unique-local
  (fc00::/7). Check is at dial time, not URL-parse time — defeats DNS rebinding attacks.
  Wired into `outboundClient` via `&http.Transport{DialContext: ssrfSafeDialContext()}`.
- `workerPool.Enqueue`: rejects `target_url` values whose scheme is not `"https"`. Returns
  `"smeldr: webhook: target_url must use https scheme"`.
- `outboundClient` comment corrected: removed false "SSRF validation at endpoint creation
  time" claim; replaced with accurate description of redirect-blocking and dialer-based
  IP-range rejection.

### Tests

2 new tests in `outbound_test.go`:
- `TestSSRFProtection` — 8 blocked addresses: 127.0.0.1, 10.0.0.1, 192.168.1.1,
  169.254.169.254, ::1, fe80::1, 100.64.0.1, fc00::1
- `TestEnqueueHTTPSValidation` — http scheme rejected, https accepted

Existing `TestHTTPDeliver_Success` / `TestHTTPDeliver_Non2xx` updated to temporarily swap
`outboundClient` with a plain client (test servers run on 127.0.0.1).

Coverage: 96.0% (gate ≥96.0% ✓)

### Version

core v1.41.2 (patch — no exported symbols changed). Branch: fix/ssrf-outbound-transport.

---

## A157 — T72 PageMeta — per-path SEO override layer (core v1.42.0)

**Date:** 2026-06-19
**Status:** Agreed
**Level:** 2 (new exported symbols; no breaking changes)

### Decision

T72 adds `PageMetaStore` — a per-path SEO override layer inserted between each content item's own `Head()` implementation and the global `SiteConfig.og_image`/`OGDefaults` fallback.

An operator uses `App.PageMeta(store)` at wiring time and `App.GetPageMeta(ctx, path)` in custom handlers. The framework uses the store automatically in `renderListHTML` when no `ListHeadFunc` is configured.

### Changes

**`pagemeta.go`** (new file):
- `PageMeta` struct — `Path`, `MetaTitle`, `Description`, `OGImage` string fields
- `PageMetaStore` — backed by `smeldr.DB`
- `NewPageMetaStore(db DB) *PageMetaStore`
- `CreatePageMetaTable(db DB) error` — `smeldr_page_meta` DDL (idempotent)
- `Set(ctx, path, title, description, ogImage)` — INSERT OR REPLACE upsert
- `Get(ctx, path)` — returns zero `PageMeta` (nil error) when no row exists; caller checks `meta.Path != ""`
- `Delete(ctx, path)` — no-op when path absent
- `List(ctx)` — all entries ordered by path

**`smeldr.go`**:
- `App.PageMeta(store *PageMetaStore) *App` — wires the store; returns `*App` for chaining
- `App.GetPageMeta(ctx context.Context, path string) Head` — returns a populated `Head` (Title, Description, Image.URL); zero `Head` when store nil or no entry
- `App.Handler()` push loop: injects `pageMetaStore` into all `templateModules` via `setPageMetaStore`

**`module.go`**:
- `pageMetaStore *PageMetaStore` field on `Module[T]`

**`templates.go`**:
- `setPageMetaStore(s *PageMetaStore)` on `Module[T]`
- `renderListHTML`: when `listHeadFunc` is nil AND `pageMetaStore` is non-nil, looks up `r.URL.Path`; sets `data.Head` if `meta.Path != ""`; `listHeadFunc` takes priority

### Tests

14 new tests in `pagemeta_test.go`:
- `TestCreatePageMetaTable_Idempotent`
- `TestPageMetaStore_Set_Get`, `TestPageMetaStore_Get_Missing`, `TestPageMetaStore_Set_Upsert`
- `TestPageMetaStore_Delete`, `TestPageMetaStore_Delete_Missing`, `TestPageMetaStore_List`
- `TestApp_GetPageMeta_NoStore`, `TestApp_GetPageMeta_Found`, `TestApp_GetPageMeta_Missing`
- `TestRenderListHTML_PageMeta_UsedWhenNoListHeadFunc`, `TestRenderListHTML_PageMeta_ListHeadFuncPriority`
- `TestApp_Handler_InjectsPageMetaStore`
- `TestRenderListHTML_PageMeta_NoEntryForPath`

Coverage: 96.0% (gate ≥96.0% ✓)

### Version

core v1.42.0 (minor — new exported symbols: `PageMeta`, `PageMetaStore`, `NewPageMetaStore`, `CreatePageMetaTable`, `App.PageMeta`, `App.GetPageMeta`). Branch: feat/t72-page-meta.

---
