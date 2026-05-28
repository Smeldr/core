# Forge — Recent Decisions

Rolling working file. All new decisions are added here first.
When this file approaches ~20KB, report it at session start — the architect
will issue archiving instructions via NEXT.md.

Non-Decisions go directly to `nondecisions.md` — not here.

Archived 2026-05-25: D32 → docs.md · A87, A97–A101 → core.md

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
