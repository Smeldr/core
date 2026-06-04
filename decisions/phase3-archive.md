# ARCHIVED — Smeldr Decisions: Phase 3

This file is archived. It contains A102–A120, covering the period
2026-05-22 through 2026-05-31. It is preserved for historical reference.
New decisions are written to `decisions/recent.md` (or `decisions/nondecisions.md`
for Non-Decisions).

---

### A102 — `module.go`: `APIOnly()` module option

**Date:** 2026-05-22
**Status:** Agreed
**File:** `module.go`

**What:**
`APIOnly() Option` marks a module as REST/MCP/CLI-only with no public HTML surface.
`GET /{prefix}` and `GET /{prefix}/{slug}` with `Accept: text/html` return 404. JSON
routes (`Accept: application/json` or absent `Accept`) are unchanged. MCP tools are
generated in full — the same as a regular module. forge-cli works via the REST JSON
API without any changes.

**Why:**
Forge registers HTML routes for all modules. For content types with no public HTML
representation — such as `HomePage` or platform config — a browser visiting the prefix
receives JSON rather than a 404. This is confusing and incorrect: the prefix should not
be a browsable URL at all. APIOnly() makes the intent explicit and enforceable.

**How:**
- `apiOnlyOption struct{}` + `APIOnly() Option` constructor
- `apiOnly bool` field on `Module[T]`
- `listHandler`, `showHandler`, `singleInstanceHandler`: early return 404 when
  `m.apiOnly` and `Accept` header contains `text/html` (explicit, non-wildcard)
- Startup panic when `APIOnly()` and `SingleInstance()` are combined — they are
  logically incompatible (`SingleInstance` serves HTML at `GET /{prefix}`)
- No change to `Register()`, `contentNegotiator`, `MCPMeta`, or `forge-mcp`

**Status resolution — 404 vs 406:**
404 chosen over 406. 404 signals "this URL has no browsable surface" — search engines
will not index it and browsers will not attempt to render it. Consistent with the
lifecycle enforcement precedent: Draft → 404 intentionally hides existence.

**Distinction from other routing variants:**
- `SingleInstance()`: one item, HTML at `GET /{prefix}`, `list_{type}s` MCP tool suppressed
- `Standalone()`:     items at `/{slug}`, HTML served
- `APIOnly()`:        no HTML anywhere; REST + MCP + CLI only, all MCP tools present

**Consequences:**
- No breaking changes. Additive option.
- `example_test.go`: `ExampleAPIOnly()` added (compile-verified).
- `integration_full_test.go`: G36 group (5 sub-tests).
- `docs/ARCHITECTURE.md` updated.

**Files changed:** `module.go`, `integration_full_test.go`, `example_test.go`,
`DECISIONS.md`, `decisions/recent.md`, `docs/ARCHITECTURE.md`, `CHANGELOG.md`,
`README.md`, `AGENTS.md`.

**Forge core → v1.24.0.**

---

### A103 — `auth.go`: `VerifyTokenString`

**Date:** 2026-05-24
**Status:** Agreed
**File:** `auth.go`

**What:**
`VerifyTokenString(token string, secret []byte, store *TokenStore) (User, bool)` —
verifies a raw bearer token string without requiring an `*http.Request`. Identical
logic to `VerifyBearerToken` except the token is provided directly rather than
extracted from an `Authorization` header. When `store` is non-nil, the DB lookup
uses `context.Background()`.

**Why:**
`forge-oauth` is a standalone MIT-licensed library with no dependency on forge core's
HTTP layer. It needs to validate Forge bearer tokens during the OAuth `/oauth/authorize`
form submission (user pastes their existing Forge token to approve the authorization).
`VerifyBearerToken` requires an `*http.Request`; constructing a synthetic request just
to pass the token through is semantically incorrect and fragile.

**Rejected alternative:**
Synthetic `*http.Request` construction in forge-oauth. Rejected: requires importing
`net/http` into a library whose design goal is minimal dependencies; semantically
incorrect (an HTTP request object is not the right representation of "a token string").

**Consequences:**
- No breaking changes. Additive exported function.
- forge-oauth can validate Forge tokens without importing `smeldr.dev/core` at all
  if using the callback pattern.
- No new test file — existing `auth_test.go` infrastructure sufficient.

**Files changed:** `auth.go`, `DECISIONS.md`, `decisions/recent.md`, `CHANGELOG.md`.

**Forge core → v1.25.0.**

---

## A104 — Health endpoint and startup log key rename (T59 Phase 0C)

**Date:** 2026-05-26
**Status:** Accepted
**Milestone:** T59 Phase 0C — module path rename

**Context:**
`forgeVersions()` in `forge.go` derives short keys from module paths by stripping
the base prefix and replacing hyphens with underscores. With the old paths this
produced `"forge"` for `forge-cms.dev/forge` and `"forge_mcp"` for
`forge-cms.dev/forge-mcp`. The `/_health` endpoint and startup log used the
hardcoded key `"forge"` to identify the core module version.

After renaming to `smeldr.dev/*` paths, `smeldr.dev/core` produces the key
`"core"` and `smeldr.dev/mcp` produces `"mcp"`.

**Decision:**
- `const base` updated from `"forge-cms.dev/"` to `"smeldr.dev/"`.
- `/_health` JSON response key changes: `"forge"` → `"core"`, `"forge_mcp"` → `"mcp"`.
  Example: `{"status":"ok","core":"1.25.0","mcp":"1.11.2"}`.
- Startup log prefix changes from `"forge: "` to `"smeldr: "`.
- All four hardcoded `versions["forge"]` / `k != "forge"` lookups updated to `"core"`.
- Godoc for `Health()` and `Run()` updated to reflect new keys.
- `forge_test.go` assertion updated from `"forge":` to `"core":`.

**This is a breaking change** for any health monitor parsing the `/_health` JSON
keys `"forge"` or `"forge_mcp"`. Clients must update to use `"core"` and `"mcp"`.
Accepted in Phase 0C — the rename is the right time to accept this change.
Phase 2 cutover note: update UC2 health-check configuration if it reads these keys.

**Consequences:**
- `/_health` response format changes (breaking for monitors using old keys).
- Startup log line changes from `"forge: forge 1.25.0"` to `"smeldr: core 1.25.0"`.
- No other exported API affected.

**Files changed:** `forge.go`, `forge_test.go`, `DECISIONS.md`, `decisions/recent.md`.

---

## A105 — T59 Phase 2.4: all smeldr.dev/* modules tagged and published

**Date:** 2026-05-27
**Status:** Agreed
**Milestone:** T59 Phase 2.4 — first Go-resolvable versions on smeldr.dev/* paths

**What:**
All 8 Go modules renamed in T59 Phase 0C have been tagged and published on the
smeldr.dev vanity domain. This is the first moment any module is resolvable via
`go get smeldr.dev/<module>@latest` from the public Go module proxy.

**Tags published:**

| Module | New tag | Notes |
|--------|---------|-------|
| smeldr.dev/core | v1.25.1 | patch bump; module path rename only |
| smeldr.dev/oauth | v0.1.2 | patch bump; module path rename only |
| smeldr.dev/mcp | v1.11.3 | patch bump; require blocks updated to real versions |
| smeldr.dev/media | v1.2.1 | patch bump; require blocks updated |
| smeldr.dev/social | v0.6.1 | patch bump; require blocks updated |
| smeldr.dev/agent | v0.4.2 | patch bump; require blocks updated |
| smeldr.dev/cli | v0.9.1 | patch bump; module path rename only (stdlib-only, no smeldr.dev/* deps) |
| smeldr.dev/pgx | forge-pgx/v0.1.0 | first tag; replace directive removed; v0.0.0 → v1.25.1 |

**Verification:** `go get smeldr.dev/{core,oauth,mcp,media,social,agent,cli}@latest`
resolves correctly from the Go module proxy. All 7 modules confirmed via `GONOSUMDB=smeldr.dev GOWORK=off go get` from a clean temp directory.

**Known issue — smeldr.dev/pgx not resolvable via go get:**
The vanity meta tag maps `smeldr.dev/pgx` → `github.com/smeldr/core`, but the root
`go.mod` of that repo declares `module smeldr.dev/core`. Go's module resolution
requires either (a) the root go.mod to match the import path, or (b) the import path
to be a sub-path of the parent module (e.g. `smeldr.dev/core/pgx`). The `forge-pgx/v0.1.0`
tag is in place; resolution will work once sitepilot corrects the vanity configuration.
Architect decision required: change module path to `smeldr.dev/core/forge-pgx` and
update the vanity redirect, OR create a separate `github.com/smeldr/pgx` repo.

**Also fixed:** 4 stale module paths in `common/agent/skills/forge.md`
(`smeldr.dev/forge-oauth` → `smeldr.dev/oauth`, `smeldr.dev/forge-social` →
`smeldr.dev/social`, `smeldr.dev/forge` → `smeldr.dev/core`).

**Files changed:** go.mod + go.sum in mcp, media, social, agent, forge-pgx; all repos
merged T59-phase-0c → main; `common/agent/skills/forge.md`.

---

## A106 — T59 doc rename: forge-cms.dev → smeldr.dev across all core documentation

**Date:** 2026-05-28
**Status:** Agreed
**Milestone:** T59 — documentation rename (docs-only)

**What:**
Renamed all `forge-cms.dev/*` module path references to `smeldr.dev/*`, all
`github.com/forge-cms/*` repository references to `github.com/smeldr/*`, and all
prose module names (`forge-mcp`, `forge-media`, `forge-cli`, `forge-social`,
`forge-agent`, `forge-oauth`) to their canonical `smeldr.dev/*` forms across 10
documentation files in the core repo.

Also corrected stale sub-module path references (`smeldr.dev/core-mcp` →
`smeldr.dev/mcp`, `smeldr.dev/core-media` → `smeldr.dev/media`,
`smeldr.dev/core-agent` → `smeldr.dev/agent`, `smeldr.dev/core-agent/flow` →
`smeldr.dev/agent/flow`) left over from Phase 0C.

**Scope:** Docs only. No Go source files changed. Binary command names (`forge-cli`,
`forge-cli init`, etc.) and config file names (`.forge-cli.env`) are unchanged — they
refer to the CLI binary, not the Go module path.

**Excluded:** `CHANGELOG.md`, `DECISIONS.md`, `decisions/` — contain historical
records that must not be altered.

**Files changed:** `README.md`, `AGENTS.md`, `docs/VISION.md`, `docs/SECURITY.md`,
`docs/FEATURELIST.md`, `.github/copilot-instructions.md`, `docs/ARCHITECTURE.md`,
`docs/REFERENCE.md`, `skills/forge.md`, `skills/README.md`.

---

## A107 — T62: package forge → smeldr rename

**Date:** 2026-05-28
**Status:** Agreed
**Milestone:** T62 — package rename

**What:**
- `package forge` → `package smeldr` in all 75 root-package Go files
- 9 template function string literals renamed: `forge:head` → `smeldr:head`,
  `forge_markdown` → `smeldr_markdown`, `forge_date` → `smeldr_date`,
  `forge_meta` → `smeldr_meta`, `forge_html` → `smeldr_html`,
  `forge_excerpt` → `smeldr_excerpt`, `forge_csrf_token` → `smeldr_csrf_token`,
  `forge_rfc3339` → `smeldr_rfc3339`, `forge_llms_entries` → `smeldr_llms_entries`
- 2 struct tag keys: `forge_format` → `smeldr_format`, `forge_description` → `smeldr_description`
- 2 cookie names: `forge_csrf` → `smeldr_csrf`, `forge_consent` → `smeldr_consent`
- Internal identifiers renamed: `forgeHeadTmpl` → `smeldrHeadTmpl`, `forgeDate` → `smeldrDate` etc.
- `forge-pgx` updated: `forge.X` → `smeldr.X`, import alias removed
- All standalone modules updated: `forge.` → `smeldr.` throughout (smeldr.dev/mcp, media, social, agent, oauth, cli)
- All code examples in core documentation updated

**Breaking changes:**
- Any template using `{{template "forge:head" .}}` must update to `{{template "smeldr:head" .}}`
- Any template using `{{forge_date .}}`, `{{forge_markdown .}}` etc. must update to `smeldr_*`
- Any code using `forge_format`/`forge_description` struct tags must update to `smeldr_*`
- Sessions using `forge_csrf`/`forge_consent` cookies are invalidated on upgrade
- Callers using the `forge.` package prefix must use `smeldr.`

**Files changed:** All 75 root-package `.go` files, `templatehelpers.go`, `templates.go`,
`module.go`, `mcp.go`, `auth.go`, `cookies.go`, `middleware.go`, `templatedata.go`,
`forge-pgx/pgx.go`, `forge-pgx/pgx_integration_test.go`, `forge-pgx/pgx_test.go`,
`example/` (Go + HTML), all test files referencing renamed string literals,
all standalone modules (mcp, media, social, agent, oauth, cli),
all core markdown documentation, `common/agent/skills/forge.md`.

**Forge core → v1.26.0** (minor bump — breaking changes for callers).

---

## A108 — T64+T65: smeldr.config, SMELDR_CONFIG, smeldr: error prefix, skill rename

**Date:** 2026-05-28
**Status:** Agreed
**Milestone:** T64 + T65

**What:**
- `forge.config` → `smeldr.config` — runtime config filename renamed. Operators must rename
  their config file on disk.
- `FORGE_CONFIG` → `SMELDR_CONFIG` — env var path override renamed. Operators using this
  must update.
- `FORGE_CONFIG`/`forge.config` references updated in `config.go`, `forge.go`,
  `config_test.go`, `static_test.go`, and all doc files (README.md, AGENTS.md,
  docs/REFERENCE.md, docs/FEATURELIST.md, docs/ARCHITECTURE.md,
  .github/copilot-instructions.md).
- `skills/forge.md` → `skills/smeldr.md` in core repo and
  `agent/skills/forge.md` → `agent/skills/smeldr.md` in common repo.
  `.github/copilot-instructions.md` updated to reference new skill file path.
- Error string prefix `"forge: "` → `"smeldr: "` throughout all Go source files
  (~48 occurrences in 14 files). DB table names (`forge_tokens`, `forge_nav`,
  `forge_audit_log`) unchanged — live schema names are out of scope.

**Breaking changes:**
- `forge.config` → `smeldr.config` (operators must rename file on disk)
- `FORGE_CONFIG` → `SMELDR_CONFIG` (operators must update env var)

**No exported Go API changes. No DB schema changes.**

**Files changed (core):** `config.go`, `forge.go`, `config_test.go`, `static_test.go`,
`audit.go`, `auth.go`, `errors.go`, `middleware.go`, `module.go`, `nav.go`, `node.go`,
`outbound.go`, `redirects.go`, `static.go`, `templates.go`, `webhook.go`,
`skills/forge.md` (→ `skills/smeldr.md`), `.github/copilot-instructions.md`,
`AGENTS.md`, `docs/REFERENCE.md`, `docs/ARCHITECTURE.md`, `CHANGELOG.md`,
`decisions/recent.md`, `DECISIONS.md`.

**Files changed (common):** `agent/skills/forge.md` (→ `agent/skills/smeldr.md`).

**Forge core → v1.27.0** (minor bump — `forge.config` and `FORGE_CONFIG` rename are
breaking for operators).

---

## A109 — T66: forge_* → smeldr_* DB table rename, auto-migrate at startup

**Date:** 2026-05-28
**Status:** Agreed
**Milestone:** T66

**What:**
All 7 internal DB tables renamed from `forge_` to `smeldr_` prefix. Auto-migration via
`migrateLegacyTableNames(ctx, db)` called from `New()` handles existing SQLite databases
transparently on first startup with v1.28.0. PostgreSQL operators must run the 7
`ALTER TABLE` renames manually before deploying.

Tables renamed:
- `forge_audit_log` → `smeldr_audit_log`
- `forge_delivery_logs` → `smeldr_delivery_logs`
- `forge_nav` → `smeldr_nav`
- `forge_outbound_jobs` → `smeldr_outbound_jobs`
- `forge_redirects` → `smeldr_redirects`
- `forge_tokens` → `smeldr_tokens`
- `forge_webhook_endpoints` → `smeldr_webhook_endpoints`

**Migration function (`migrate.go`):** Probes `sqlite_master` to detect SQLite; silently
skips for other databases. Wraps all renames in a single transaction via `BeginTx` when
available. Idempotent — checks old table existence before each rename. Logs each rename
via `slog.Info`.

**Breaking changes (upgrade):**
- Existing SQLite databases: auto-migrated at first startup with v1.28.0.
- PostgreSQL operators: must run 7 `ALTER TABLE ... RENAME TO ...` statements manually.

**No exported Go API changes. `migrate.go` is package-internal.**

**Files changed (core):** `migrate.go` (new), `forge.go`, `audit.go`, `auth.go`, `nav.go`,
`outbound.go`, `redirects.go`, `webhook.go`, `auth_test.go`, `outbound_test.go`,
`webhook_test.go`, `integration_full_test.go`, `example/blog/main.go`,
`docs/REFERENCE.md`, `docs/FEATURELIST.md`, `docs/ARCHITECTURE.md`, `README.md`,
`CHANGELOG.md`, `decisions/recent.md`, `DECISIONS.md`.

**Forge core → v1.28.0** (minor bump — DB schema migration required for upgrade).

---

## A110 — T63: SiteConfig singleton — global site configuration via MCP

**Date:** 2026-05-28
**Status:** Agreed
**Milestone:** T63

**What:**
`SiteConfig` struct added to core — a built-in singleton content type for site-wide
defaults configurable via MCP after first deploy. Replaces hardcoded values in `main.go`
for operators who provision via agent.

Fields: `site_name`, `title_separator`, `og_image`, `x_handle`, `head_script`.

Factory: `NewSiteConfigModule(db DB) *Module[SiteConfig]` — returns a fully configured
`SingleInstance` module backed by `NewSQLRepo` on `smeldr_site_configs`.

DDL helper: `CreateSiteConfigTable(db DB) error` — creates the `smeldr_site_configs`
table if it does not exist. Follows the `CreateAuditTable` pattern.

**Why:** Cloud provisioning requires MCP-only setup after first deploy. Hardcoded
`main.go` config requires a rebuild for every site-name or analytics script change.

**No breaking changes** — purely additive. Existing applications are unaffected.
v1.29.0.

---

## A111 — T74+T67: HeadAssets.RawHead + smeldr:"required" tag rename

**Date:** 2026-05-29

**What (T74):**
`HeadAssets.RawHead template.HTML` field added to the `HeadAssets` struct.
The `smeldr:head` template partial injects it verbatim into `<head>` after all
other HeadAssets output (preconnect → stylesheets → links → scripts → RawHead).
Zero value is a no-op — fully backward compatible.

Use case: analytics snippets (GoatCounter, Plausible), preload hints, or any
custom `<head>` HTML that does not fit the structured fields.

**What (T67):**
Validation and auto-slug struct tag key renamed from `forge:"required"` to
`smeldr:"required"` throughout core. Three call sites changed:
`autoSlugFieldPath` (module.go), MCP field builder (module.go), and
`parseConstraints` (node.go). All test files, example files, and doc comments
in core updated.

**Breaking change (T67):** any content type still using `forge:"required"` will
no longer have those fields validated until the tag is updated to `smeldr:"required"`.
Operators with custom content types must update struct tags. Repos with known
`forge:"required"` usage (site-dev, site, agent) have separate follow-up tasks.

**Why (T74):** HeadAssets covers most common static-asset injection patterns but
could not accept arbitrary head HTML. RawHead closes the gap without adding a
one-off escape hatch for every new use case.

**Why (T67):** Completes the smeldr brand rename. The `forge` tag key was the last
visible remnant of the old brand in the core API.

**Files changed (core):** `head.go`, `templates.go`, `module.go`, `node.go`,
`ai_test.go`, `doc.go`, `example_test.go`, `node_test.go`, `module_test.go`,
`feed_test.go`, `mcp_test.go`, `integration_full_test.go`,
`example/docs/main.go`, `example/api/main.go`, `CHANGELOG.md`, `docs/REFERENCE.md`,
`docs/FEATURELIST.md`, `README.md`, `DECISIONS.md`, `decisions/recent.md`.

**Forge core → v1.30.0** (minor bump — breaking tag rename + additive RawHead field).

---

## A112 — T71: X OAuth base URL twitter.com → x.com

**Date:** 2026-05-29

**What:** `xAuthBase` in `social/twitter.go` changed from `"https://twitter.com"`
to `"https://x.com"`.

**Why:** X's OAuth authorization endpoint moved to x.com. Using twitter.com causes
a login loop due to session cookie domain mismatch between twitter.com and x.com.

**Files changed (social):** `twitter.go`.

**smeldr.dev/social → v0.7.4** (patch).

---

## A113 — T57 oauth: POST /oauth/revoke per RFC 7009

**Date:** 2026-05-29

**What:** `revokeHandler` added to `forgeoauth.Server`. `POST /oauth/revoke`
registered in `Handler()`. New file `revoke.go`.

**Behaviour:** Always responds `200 OK` regardless of whether the token is found
or already revoked (RFC 7009 §2.2 requirement). Only refresh tokens are handled —
`DeleteRefreshToken` is called on the store. Access tokens expire naturally via
`ExpiresAt`. `token_type_hint` is accepted but not enforced.

**Why:** RFC 7009 compliance. Clients (Claude Desktop, custom apps) need a
standard way to invalidate refresh tokens on logout or credential rotation.

**Files changed (oauth):** `revoke.go` (new), `server.go`.

**smeldr.dev/oauth → v0.1.4** (patch).

---

## A114 — T57 cli: smeldr-cli oauth revoke subcommand

**Date:** 2026-05-29

**What:** New `oauth.go` in forge-cli with `runOAuthCommand` / `runOAuthRevoke`.
`main.go` switch gets `"oauth"` case; `printUsage` updated with OAuth subcommands.

**Usage:** `smeldr-cli oauth revoke <token>` — POSTs
`application/x-www-form-urlencoded` to `FORGE_URL/oauth/revoke` via
`http.PostForm`. Server always responds 200; CLI prints "Token revoked." on
success.

**Why:** CLI parity with the new `/oauth/revoke` endpoint (A113).

**Files changed (cli):** `oauth.go` (new), `main.go`.

**smeldr.dev/cli → v0.9.3** (patch).

---

## A115 — T58: forgemcp.Server.Register(app)

**Date:** 2026-05-29

**What:** New `Register(app *smeldr.App)` method on `forgemcp.Server`.
Calls `s.Handler()` once to get the forgemcp mux, then registers each route
pattern on the forge app's mux via `app.Handle()`.

Routes registered unconditionally: `GET /mcp`, `POST /mcp`, `POST /mcp/message`,
`GET /.well-known/oauth-protected-resource`.

Routes registered when `WithOAuth` is configured: `GET/.well-known/oauth-authorization-server`,
`GET /oauth/authorize`, `POST /oauth/authorize`, `POST /oauth/token`,
`POST /oauth/revoke`.

**Why:** Operators currently call `app.Handle(...)` five times for each OAuth
endpoint. `Register` reduces this to one call and removes the risk of missing
a route. `Handler()` is unchanged for non-forge embeddings.

**Also:** `mcp_test.go` struct tags updated `forge:"required"` → `smeldr:"required"`
(T67 follow-up). `go.mod` bumped: `smeldr.dev/core v1.26.0` → `v1.30.0`,
`smeldr.dev/oauth v0.1.2` → `v0.1.4`.

**Files changed (mcp):** `transport.go`, `mcp_test.go`, `go.mod`, `go.sum`.

**smeldr.dev/mcp → v1.13.0** (minor — new exported method).

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
