# Smeldr — Recent Decisions

Rolling working file. All new decisions are added here first.
When this file approaches ~20KB, report it at session start — the architect
will issue archiving instructions via NEXT.md.

Non-Decisions go directly to `nondecisions.md` — not here.

Archived 2026-05-25: D32 → docs.md · A87, A97–A101 → core.md
Archived 2026-05-30: A102–A115 → phase3-archive.md

---

## A116 — Block-system data foundation (T32 components 1 + 2)

**Status:** Agreed · **Date:** 2026-05-31 · **Version:** core v1.31.0

### Decision

Add the pure-core data layer for the Smeldr block system: a generic content
store for all block types and a single composition-edge table. This is the first
slice of T32 (components 1 and 2 only). MCP tools, `ServeBlocks()` rendering,
schema seeding, schema validation, and CLI are later components and are **not**
touched here. The `smeldr_content_type_schemas` table (component 7) is **not**
created here.

Implements the T55 shared-data-infra conventions: grouped idempotent table
creation (Decision 1), one composition-edge table separate from T06 relations
(Decision 2).

### New exported surface

`blocks.go` (new file):

- `DynamicNode` — generic block content type. Embeds `Node`; adds
  `TypeName string` (`db:"type_name" json:"type_name"`) and
  `Fields json.RawMessage` (`db:"fields" json:"fields"`). `Head()` minimal, for
  the future MCP/admin surface. One Go type serves all block types via the
  `type_name` discriminator; type-specific fields live in `Fields` as JSON.
- `NewDynamicContentRepo(db DB) *SQLRepo[*DynamicNode]` — `SQLRepo` bound to
  `smeldr_dynamic_content` (the type-derived name would be `dynamic_nodes`, so the
  table is set explicitly). Reuses the existing `SQLRepo` surface — no bespoke
  block store.
- `CreateBlockTables(db DB) error` — single grouped, idempotent
  (`CREATE TABLE IF NOT EXISTS`) creator for `smeldr_dynamic_content` and
  `smeldr_content_edges`, plus a `(parent_id, sort_order)` index on the edge
  table. Mirrors the `CreateSiteConfigTable` precedent.

`edges.go` (new file):

- `ContentEdge` — one composition edge (`ID`, `ParentID`, `ParentType`,
  `ChildID`, `ChildType`, `SortOrder`, `IsShared`, `EdgeRole`).
- `ContentEdgeStore` + `NewContentEdgeStore(db DB)` — hand-written `$N` SQL store
  (the `TokenStore`/`AuditStore` precedent). Methods: `AddChild` (append,
  assigns ID + SortOrder, defaults `EdgeRole` to `"section"`), `Children`
  (ordered, one query), `ChildrenOf` (batched `IN()` for many parents — the
  render engine's level-load path, no N+1), `RemoveChild` (`ErrNotFound` when
  absent), `Reorder` (single atomic `CASE` UPDATE; `ErrBadRequest` when empty).

### Consequences

- **`scheduled_at` column added to `smeldr_dynamic_content`** (absent from the
  illustrative schema in block-system.md). Required so `SQLRepo[*DynamicNode]`,
  which INSERTs every mapped `Node` field, works unchanged — and blocks become
  schedulable for free. The column list in the design doc is illustrative, not
  normative; this is an additive deviation, not a contradiction of any T55
  decision.
- **Blocks are addressed by ID; `slug` may be empty.** The table keeps the
  standard `slug TEXT NOT NULL DEFAULT ''` Node column but with no UNIQUE
  constraint. No auto-slug derivation in this layer (it has no admin/MCP reader);
  that belongs in the later handler/MCP component. Matches T55 Decision 2 ("blocks
  have no meaningful slug").
- **`is_shared` is stored as INTEGER and scanned via an `int64` temp into `bool`**
  in `scanEdges` — `database/sql` will not scan INTEGER straight into `*bool`.
  This is why edges use a hand-written store, not `SQLRepo` reflection.
- **`AddChild` computes the next `SortOrder` with a read-then-insert.** On a
  multi-editor pgx/Postgres backend, concurrent appends to the same parent can
  collide on `SortOrder`; a later `Reorder` corrects it. Not possible on
  single-writer SQLite. Documented in the method godoc as a forward breadcrumb;
  not engineered around in this slice.
- No new sentinels — reuses `ErrNotFound` and `ErrBadRequest`.
- No existing exported symbol changed; no Example function affected. Fully
  additive — minor version bump (v1.31.0).
- `docs/ARCHITECTURE.md` (file map + changelog), `docs/REFERENCE.md`
  ("Block data foundation" section), and `docs/FEATURELIST.md` updated.
  `AGENTS.md` deliberately not updated — blocks are not reachable by an AI agent
  until the MCP wiring component; agent-facing docs follow then.

### Tests

`blocks_test.go` + `edges_test.go` — 12 tests against in-memory modernc SQLite
(the A81 precedent): `CreateBlockTables` idempotency and table/index existence
(and absence of `content_type_schemas`); DynamicNode create/update/delete and
JSON `Fields` round-trip; edge append order, ordered `Children`, batched
`ChildrenOf`, `is_shared` int↔bool round-trip, `RemoveChild` + `ErrNotFound`,
`Reorder` correctness + `ErrBadRequest`.

---

## A117 — Block-system generic MCP tools (T32 component 3)

**Status:** Agreed · **Date:** 2026-05-31 · **Version:** smeldr.dev/mcp v1.14.0
(provisional — tagged only at the coordinated core v1.31.0 + mcp release)

### Decision

Expose the A116 block foundation through MCP so an AI operator can create blocks
and compose them into pages and collections. Code lives in `smeldr.dev/mcp`; this
amendment records the surface in the core decision log (as A115 did for the mcp
`Register` method). Built against local core via `go.work` — core v1.31.0 is not
tagged yet, and mcp's go.mod is not bumped in this slice.

### New surface (smeldr.dev/mcp)

- `WithBlocks() ServerOption` (`mcp.go`) — opt-in that enables the block tools by
  constructing a `DynamicNode` repo and a `ContentEdgeStore` from the App's
  `Config.DB`. Tools are exposed only when set and the App has a DB. Two new
  `Server` fields (`blockRepo`, `edgeStore`).
- **Generic node tier** (`node_tools.go`, Decision 4 `*_node`), addressed by **ID**
  (blocks have no slug): `create_node(type_name, fields)`, `update_node(id, fields)`
  (shallow JSON merge — absent keys preserved, `type_name` immutable),
  `get_node(id)`, `list_nodes(type_name?, status?)` (via `smeldr.Query`),
  `publish_node(id)` (idempotent), `archive_node(id)`.
- **Composition tier** (`edge_tools.go`, Decision 4 explicit verbs): `add_section`,
  `reorder_sections`, `remove_section`, `add_item`, `reorder_items`, `remove_item`.
  Sections (`edge_role` `"section"`) and items (`"item"`) are deliberately distinct
  tool names for operator clarity, sharing one helper set parameterised by role.
  `add_*` derives `parent_type`/`child_type` from the stored blocks' `type_name`,
  so the operator never passes (or mismatches) types.
- Dispatch interception before the module-scoped path (like preview/upload/webhook
  tools), so a content type cannot shadow a node/composition tool.

### Consequences

- **Roles:** all six node tools require **Author+**; all six composition tools
  require **Editor+**. Node reads (`get_node`/`list_nodes`) are Author+, not
  Editor+ as first proposed — blocks are shared building components
  (`is_shared` is first-class), not private authored drafts, so the Editor-gate
  rationale for `get_{type}`/`list_{type}s` does not apply, and a create-but-cannot-
  read gap is avoided. Clean division: Authors produce/manage blocks; Editors
  compose them. (Architect override of the plan's D-4.)
- **Blocks are not browsable resources** — no `resources/list`/`resources/read`
  entry; the read surface is `get_node`/`list_nodes` only.
- AGENTS.md "For AI consuming agents", `docs/REFERENCE.md`, `docs/FEATURELIST.md`,
  and the common skill updated for the new tool surface (the A116 AGENTS deferral
  ends here — blocks are now agent-reachable). `smeldr.dev/mcp` README + CHANGELOG
  updated.
- Held from public tag: core v1.31.0 + mcp v1.14.0 ship together at a separate
  explicit release trigger.

### Tests

`node_tools_test.go` + `edge_tools_test.go` (mcp repo) — 9 tests against an
in-memory SQLite app with `WithBlocks`: create/get/update-merge, publish
(idempotent)/archive, `list_nodes` type/no filter, Author gate on node tools,
tools surface only with `WithBlocks`; add/reorder/remove sections, add_item role +
type derivation, Editor gate on composition, missing-child error.

---

## A118 — ServeBlocks rendering engine (T32 component 4)

**Status:** Agreed · **Date:** 2026-05-31 · **Version:** core v1.31.0 (same held
T32 release as A116; ships at the coordinated core v1.31.0 + mcp v1.14.0 tag)

### Decision

Add `App.ServeBlocks` — the engine that assembles a page from blocks
(`smeldr_dynamic_content`) and composition edges (`smeldr_content_edges`) and
renders it to HTML via convention templates (`templates/blocks/<type_name>.html`).
Core engine only — route wiring and the real site-dev templates are the sitepilot
convergence step; ContentList is deferred (component 4b).

### New exported surface (`serveblocks.go`)

- `func (a *App) ServeBlocks(dir string) (*BlockRenderer, error)` — app subsystem:
  ensures the block tables (`CreateBlockTables`), parses each `<type_name>.html` in
  dir with `TemplateFuncMap()`, returns a renderer. Errors if `Config.DB` is nil or
  a template fails to parse.
- `type BlockRenderer struct{…}` with
  `Render(ctx, pageType, pageID string) (template.HTML, error)` — assembles the
  page's ordered, Published section blocks (and each collection's items) into HTML.

### Engine properties

- **Batched load, no N+1.** Per depth level: one `IN()` query for the level's
  blocks (Published-only) and one `ContentEdgeStore.ChildrenOf` for their item
  edges; assemble in memory. A Hero + 3 collections × 6 items renders in ~5 queries,
  not ~23. Verified by a counting-DB test.
- **Cycle protection (mandatory).** Bounded load + a visited-set on the render DFS
  path + `maxDepth` (16) — a shared-block cycle A→B→A terminates, never loops.
- **Graceful degradation — every failure is local, never page-wide:** unpublished /
  missing / dangling block → skipped; missing template → skip + `slog.Warn`;
  malformed `Fields` JSON → skip + `slog.Warn`; template execution error → skip +
  `slog.Warn`; empty page → empty output; empty collection → shell only. The
  status rule applies at every level (a draft item in a published collection is
  skipped).

### Template data contract

Each block template receives a `map[string]any`: `ID`/`Slug`/`Status` from Node;
the decoded `Fields` promoted to top level; `AnchorID` always present; Markdown
fields pre-rendered to `template.HTML` (via `renderMarkdown`), raw-HTML fields
passed through, plain fields auto-escaped by `html/template`. Collections also get
`Layout` and `Items` (`[]template.HTML`, each item pre-rendered).

### Consequences

- **PascalCase is the canonical block-`Fields` key convention.** Templates access
  `.Title` / `.Body`, so Fields are stored with PascalCase keys matching the
  block-system.md type tables. A block stored with snake_case keys (the usual MCP
  convention) would not bind and would render blank. `create_node` (A117) does not
  enforce casing — documented in AGENTS.md; the future c7 schema field names and
  `create_node` field hints must use PascalCase.
- **Built-in `blockFieldRegistry`** (type_name → markdown / raw-HTML fields)
  derived from block-system.md is the interim source of field-format metadata until
  the c7 `content_type_schemas` table replaces it.
- **ContentList deferred to component 4b** — it is the only block that queries the
  App module registry (posts/stories), not `smeldr_dynamic_content`; kept out so the
  c4 engine stays storage-only, behind a future narrow lookup.
- Additive; no exported symbol changed. Part of the held core v1.31.0.

### Tests

`serveblocks_test.go` — 24 tests with fixture templates: 4 end-user scenarios
(landing page order, shared footer across two pages, gallery carousel, team grid),
15 edge cases (empty page/collection, draft+archived skip, dangling edge, missing
template, malformed/empty Fields, **cycle protection**, reorder, nested order,
**batched-load query count**, markdown→HTML, raw HTML, AnchorID present/absent), and
5 defensive (plain-field XSS escaping, Link sub-struct binding, draft item in
collection skipped, local template-exec error, no-DB constructor error).

---

## A119 — CLI block commands + T77 table output (T32 component 6)

**Status:** Agreed · **Date:** 2026-05-31 · **Version:** smeldr.dev/cli v0.10.0
(held — tagged with the coordinated T32 release)

### Decision

Add `smeldr-cli block` commands mirroring the 12 block MCP tools (A117), satisfying
the N10 CLI/MCP-parity rule in the same coordinated release, plus the T77 UX lift
(human-readable table output). Recorded in the core decision log as cli amendments
are (e.g. A114).

### New surface (smeldr.dev/cli)

- `block` top-level command group (architect A4 override — one domain verb, like
  `social`/`media`/`token`, not three top-level verbs):
  - `block node create|update|get|list|publish|archive` → `create_node` … (Author).
  - `block section add|reorder|remove`, `block item add|reorder|remove` →
    `add_section`/`reorder_sections`/`remove_section`/`add_item`/… (Editor).
- **T77 table output:** `block node list` prints an aligned table (ID, type_name,
  status, slug) via a pure `renderTable`/`nodeListTable`; `--json` for raw output.
- **PascalCase fields:** `--field K=V` accumulates into a map preserving key case
  (a `fieldFlag` flag.Value), `--fields <json>` passes an object verbatim — block
  Fields keys are case-sensitive PascalCase (A118).

### Consequences

- Pure HTTP client: reuses `mcpCall` (JSON-RPC `tools/call`), `loadConfig`,
  `printJSON`, `fatal`. **No core/mcp import, no go.work** — sends tool names as
  strings. The held-core build dependency does not reach the cli.
- `cliVersion` resynced `0.9.0` → `0.10.0`. The const had lagged the shipped tags
  (cli shipped through v0.9.3); cli CHANGELOG likewise lacks 0.9.0–0.9.3 — a
  pre-existing gap, flagged in the CHANGELOG, not reconstructed here (architect A3).
- `logs` (the T77 error-display half) deferred to a separate slice (architect A1) —
  noted in corepilot.md.
- Env-var rename FORGE_* → SMELDR_* is out of scope (T78); `FORGE_*` stays.
- cli README + help text + CHANGELOG + common skill updated.

### Tests

`block_test.go` (cli) — 12 tests: pure `renderTable` alignment, `nodeListTable`
rows/columns + empty, `buildFields` PascalCase precedence; and an httptest mock-MCP
harness asserting each command sends the correct JSON-RPC tool name + arguments
(create/update/publish/list with table-vs-`--json`, section add/reorder, item
add/reorder).

---

## A120 — ServeBlocks reference-field resolution (ImageID → .Image) (T82)

**Status:** Agreed · **Date:** 2026-05-31 · **Version:** core v1.31.0 (held T32
release; extends A118 ServeBlocks)

### Decision

ServeBlocks resolves declared **reference fields**: a `{Name}ID` field on a block
resolves to a `.{Name}` sub-object in the parent's template data holding the
referenced block's `buildData` output. `ImageID` → `.Image`. This makes
image-bearing blocks (ContentBlock, ContactCard, Hero) show real images in the
first T32 release instead of passing the raw block id through.

### Mechanism (`serveblocks.go`)

- `blockFieldFormats` gains a `refs []string` field; `blockFieldRegistry` declares
  `refs: ["ImageID"]` on `content_block`, `contact_card`, `hero`. Target key =
  `TrimSuffix(name, "ID")` → `Image`. Interim metadata, replaced by the c7 schema.
- `refIDsOf(block)` decodes Fields and returns the declared reference ids.
- `loadTree`: after the composition-tree level loop, ONE collection pass gathers
  all reference ids across the loaded tree and `loadBlocks` them in a single `IN()`
  (Published-only) into the `blocks` map. Referenced blocks are not in `childEdges`
  (never rendered standalone) — only used for resolution. One pass suffices: the
  only ref field is `ImageID` and Image declares no refs (documented bound).
- `renderBlock`: after `buildData`, for each declared ref the referenced Published
  block's `buildData` is set as `data[".{Name}"]` (e.g. `data["Image"]`).

### Contract (pinned — two pilots depend on it)

`.Image` is the **full `buildData`** of the referenced block: `.Image.MediaURL`,
`.Image.AltText`, `.Image.Title`, `.Image.Caption` (Caption markdown-rendered).
Templates use `{{ with .Image }}<img src="{{ .MediaURL }}" alt="{{ .AltText }}">{{ end }}`.
**Guarded:** an absent / unpublished / dangling reference produces no `.Image` key,
so the `{{ with }}` renders nothing — no error, no vanished block. **Published-only**
(same rule as composed blocks). **Batched** — one extra `IN()` per page regardless
of how many blocks carry a reference (counting-DB test asserts the bound).
Resolution is **one level** (`.Image` is plain `buildData`, no nested ref
resolution — Image has no refs). Raw `ImageID` is left in the data (harmless).

### Tests

`serveblocks_test.go` — 8 ref tests: resolves (MediaURL+AltText in `<img>`), absent
(guarded, no img), unpublished (Draft image skipped), dangling (no crash), shared
image across two parents, `.Image.Caption` markdown (proves full buildData), hero +
contact_card coverage, and batched query-count (8 refs within a bounded count).

---

## A121 — T85: core-repo brand sweep ("Forge" → "Smeldr" in doc prose + headers)

**Date:** 2026-06-01  
**Status:** Agreed  
**Branch:** `docs/T85-brand-sweep`

### What

Pure doc-only brand sweep across 17 files in the core repo. Every instance of
"Forge" as the framework brand name in living doc prose and headers is renamed to
"Smeldr". No code changes, no wire-level identifiers, no version bump.

### Scope

Files touched: `README.md`, `.github/copilot-instructions.md`, `CHANGELOG.md`
(header + `decisions/recent.md` header), `DECISIONS.md` (header + intro),
`docs/ARCHITECTURE.md`, `docs/REFERENCE.md`, `docs/FEATURELIST.md`,
`docs/VISION.md`, `docs/SECURITY.md`, `skills/smeldr.md`, `BENCHMARKS.md`,
`CLA.md`, `Milestone_BACKLOG_TEMPLATE.md`, `NOTES.md`, `ERROR_HANDLING.md`,
`example/README.md`, `example/api/README.md`.

Additional: `docs/VISION.md` — `forge-admin` → `smeldr-admin` and
`Forge Cloud` → `Smeldr Cloud` throughout.

`skills/smeldr.md` — version line resynced to current versions alongside the
header brand fix (file was stale at v1.25.1 from before the housekeeping sprint).

Two stale filesystem paths in `copilot-instructions.md` corrected:
`common/agent/skills/forge.md` → `smeldr.md` (file was renamed in the
housekeeping sprint but the instructions were not updated).

### Preserve (not touched)

`X-Forge-*` webhook headers, `forge://` MCP resource URI scheme, `FORGE_*`
env vars, `forge-cli` binary name, historical CHANGELOG entry bodies,
`decisions/*.md` archive files, DECISIONS.md dated index rows, `migrate.go`
`forge_*` rename sources, `BenchmarkForgeMarkdown` Go identifier,
`FORGE_SECRET` env var references, test-local identifiers.

### Why

Every prior renaming task (T59/T62/T64/T65/T66/A106) renamed the code, module
paths, config files, and error prefixes — but never the framework *brand name*
in doc prose. This sweep makes the docs consistent with the published brand.
The direct trigger was the "Forge v1.31.0" tag name during the T32 release dance.

---

## A122 — T88+T89: fix stale `forge:` struct tag examples + core/skills sync

**Date:** 2026-06-02  
**Status:** Agreed  
**Level:** 1 (docs-only, no version bump)

### What

Two classes of correctness bugs closed in one commit:

**T88 — stale `forge:"required"` in live code examples.**
`A111` (v1.30.0) renamed the struct tag key from `forge:"required"` to
`smeldr:"required"`. Any developer or AI assistant copying the README minimal
example would produce a non-functional content type (validation and auto-slug
silently do nothing with the old key). One file, two lines:
- `README.md` lines 101–102: `forge:"required"` → `smeldr:"required"`

**T89 — `core/skills/` public mirror synced from `common/agent/skills/`.**
`core/skills/` is the deliberate public distribution copy of the canonical
pilot skills in `common/agent/skills/` (private repo). It had drifted to
`forge v1.25.1` (missing SiteConfig, RawHead, block MCP catalog, oauth
section, and stale struct tags). Root cause: the doc-gate reminder was
passive ("copy updated...") — easy to forget. This amendment:

1. Fixed the canonical (`common/agent/skills/smeldr.md`): stale footer path
   `forge-common/agent/skills/forge.md` → `Smeldr/common/agent/skills/smeldr.md`.
2. Fixed `common/agent/skills/smeldr-design.md`: stale Destination footer.
3. Synced `core/skills/smeldr.md` and `core/skills/smeldr-design.md` from
   common via `Copy-Item *.md -Force`. Sync brings correct struct tags,
   current sections (SiteConfig, RawHead, block tools, oauth), and correct
   footer paths.
4. Replaced the passive doc-gate reminder with an **unconditional Copy-Item
   command** in copilot-instructions M-number pre-commit gate.
   `smeldr-design-assistant.md` and `smeldr-operator.md` are core-only
   (Claude.ai project instructions; no common canonical) and are not
   overwritten by the Copy-Item *.md command.

### Preserve

Historical `forge:"required"` references in `CHANGELOG.md:47` (migration
note), `DECISIONS.md:198` (archive row), `docs/REFERENCE.md:186–187`
(breaking-change migration guidance), and `decisions/*.md` archives.

---
