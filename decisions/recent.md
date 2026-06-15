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
