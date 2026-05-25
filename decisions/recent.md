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
- forge-oauth can validate Forge tokens without importing `forge-cms.dev/forge` at all
  if using the callback pattern.
- No new test file — existing `auth_test.go` infrastructure sufficient.

**Files changed:** `auth.go`, `DECISIONS.md`, `decisions/recent.md`, `CHANGELOG.md`.

**Forge core → v1.25.0.**

---
