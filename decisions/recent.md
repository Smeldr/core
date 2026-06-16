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
