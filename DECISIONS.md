# Forge — Decision Log

This document is the permanent record of every architectural decision made for Forge.
Each entry captures what was decided, why, what was rejected, and what consequences follow.

**Format:** decisions are immutable once locked. New decisions are appended.
Revisions to existing decisions require a new entry that supersedes the original.

**How to use this document:**
- Before implementing a feature, check if a relevant decision exists
- Before changing an interface, check what depends on it here
- When onboarding (human or AI), read this before touching code

---

## Decision index

| # | Title | Status | Date |
|---|-------|--------|------|
| 1 | Node identity | Locked | 2025-06-01 |
| 2 | Storage model | Locked | 2025-06-01 |
| 3 | Head/SEO ownership | Locked | 2025-06-01 |
| 4 | Rendering model | Locked | 2025-06-01 |
| 5 | Cookie consent enforcement | Locked | 2025-06-01 |
| 6 | Context type | Locked | 2025-06-01 |
| 7 | AIDoc format | Locked | 2025-06-01 |
| 8 | llms.txt generation | Locked | 2025-06-01 |
| 9 | Sitemap strategy | Locked | 2025-06-01 |
| 10 | Validation API | Locked | 2025-06-01 |
| 11 | Internationalisation | Locked | 2025-06-01 |
| 12 | Image type | Locked | 2025-06-01 |
| 13 | RSS feeds | Locked | 2025-06-01 |
| 14 | Content lifecycle | Locked | 2025-06-01 |
| 15 | Role system | Locked | 2025-06-01 |
| 16 | Error handling model | Locked | 2025-06-01 |
| 17 | Redirects and content mobility | Locked | 2025-06-01 |
| 18 | Licensing strategy | Locked | 2025-06-01 |
| 19 | MCP (Model Context Protocol) support | Locked | 2025-06-01 |
| 20 | Configuration model | Locked | 2025-06-01 |
| 21 | forge.Context is an interface | Locked | 2025-06-01 |
| 22 | Storage interface and database drivers | Locked | 2025-06-01 |
| 25 | Token management | Locked | 2026-04-05 |
| A19 | `storage.go`: `SQLRepo[T]` production repository | Agreed | 2026-03-07 |
| A20 | `forge.go`: `RedirectStore`, `App.Redirect()`, fallback handler | Agreed | 2026-03-07 |
| A21 | `forge.go`: `/.well-known/redirects.json` handler | Agreed | 2026-03-07 |
| A22 | `forge.go`: `App.RedirectManifestAuth()` | Agreed | 2026-03-07 |
| A23 | `node.go`: `db` struct tags on `Node` time fields | Agreed | 2026-03-07 |
| A24 | `context.go`: `NewBackgroundContext` | Agreed | 2026-03-07 |
| A25 | `module.go`: `processScheduled` + helpers | Agreed | 2026-03-07 |
| A26 | `forge.go`: scheduler wiring | Agreed | 2026-03-07 |
| A27 | `middleware.go`: `forge.Authenticate(AuthFunc)` | Agreed | 2026-03-08 |
| A28 | Auto-detect `Headable` in `Module[T]` | Agreed | 2026-03-08 |
| A29 | `errors.go` error handling gaps | Agreed | 2026-03-11 |
| A30 | `module.go` error handling gaps | Agreed | 2026-03-11 |
| A31 | `templates.go` error handling gaps | Agreed | 2026-03-11 |
| A32 | `middleware.go` error handling gaps | Agreed | 2026-03-11 |
| A33 | `module.go` route mounting order bug (sitemap + feed) | Agreed | 2026-03-11 |
| A34 | `module.go` + `forge.go` startup rebuild for derived content | Agreed | 2026-03-11 |
| A35 | `module.go` content negotiation capability gating | Agreed | 2026-03-11 |
| A36 | `module.go` startup capability mismatch detection | Agreed | 2026-03-11 |
| A37 | `WriteError` pipeline — replace `http.Error`/`http.NotFound` bypasses | Agreed | 2026-03-12 |
| A38 | `auth.go`: `SignToken` error return implements `forge.Error` | Agreed | 2026-03-12 |
| A39 | `Module[T]`: cache sweep goroutine lifecycle and `Stop()` method | Agreed | 2026-03-12 |
| A40 | Rename `FeedDisabled()` → `DisableFeed()` and `forgeLLMSEntries` → `forgeLLMsEntries` | Agreed | 2026-03-12 |
| A41 | `Module[T]`: debounce callback must use `NewBackgroundContext`, not stashed request context | Agreed | 2026-03-12 |
| A42 | `forge.go`: `Config.Version` field and `App.Health()` endpoint | Agreed | 2026-03-12 |
| A43 | `NewSQLRepo` pointer type documentation (amends Decision 22) | Agreed | 2026-03-14 |
| A44 | `dbFields`: flatten embedded (anonymous) struct fields via `[]int` index path | Agreed | 2026-03-15 |
| A45 | `Config.Auth` field + default `BearerHMAC` wired in `New()` | Agreed | 2026-03-15 |
| A46 | `markdown.go`: minimal Markdown→HTML renderer added to `TemplateFuncMap` | Agreed | 2026-03-15 |
| A47 | `templatehelpers.go`: `forge_markdown` delegates to `renderMarkdown` | Agreed | 2026-03-15 |
| A48 | `module.go`: set `PublishedAt` on manual publish in `updateHandler` | Agreed | 2026-03-15 |
| A49 | `mcp.go`/`module.go`/`forge.go`: `MCPModule` contract — `mcpOption` carries ops; export `MCPMeta`, `MCPField`, `MCPModule`; `Module[T]` implements 10 MCP methods; `App.MCPModules()` | Agreed | 2026-03-16 |
| A50 | `auth.go`/`forge.go`/`context.go`/`forge-mcp/mcp.go`: `VerifyBearerToken`, `App.Secret()`, `NewContextWithUser`, `Server` secret auto-inherit | Agreed | 2026-03-16 |
| A51 | `templates.go`: `twitter:card` derives from `Head.Type` — `Article`/`Product` emit `summary_large_image` without requiring an explicit image | Agreed | 2026-03-17 |
| A52 | `module.go`/`forge-mcp/mcp.go`: `[]string` fields typed as `"array"` in `MCPSchema`/`inputSchema`; comma-string coercion in `MCPCreate`/`MCPUpdate` | Agreed | 2026-03-17 |
| A53 | `module.go`: `negotiate()` prefers `text/html` over `application/json` when `Accept` is absent or `*/*` and templates are configured | Agreed | 2026-03-18 |
| A56 | `head.go`: `AbsURL(base, path string) string` helper for building absolute URLs in `Head()` implementations | Agreed | 2026-03-20 |
| A57 | `storage.go`: `quoteIdent()` helper — double-quote all generated SQL identifiers to handle reserved keywords | Agreed | 2026-03-20 |
| A58 | `forge.go`: `forgeVersions()` — read `runtime/debug.ReadBuildInfo()` for `/_health` and startup log; remove `"version"` key from `Health()` response | Agreed | 2026-03-20 |
| A59 | `forge.go`: `httpsRedirect()` — exempt `/_health` from HTTPS redirect so reverse-proxy health checks receive 200 on plain HTTP | Agreed | 2026-03-20 |
| A60 | `forge.go`: `New()` calls `MustConfig()` automatically — configuration errors are always caught at startup, never at first request | Agreed | 2026-04-02 |
| A61 | `social.go`/`schema.go`/`templates.go`: `OGDefaults`, `AppSchema` SEOOptions; `forge:head` receiver changed from `Head` to `TemplateData` | Agreed | 2026-04-02 |
| A62 | `forge.go`/`templates.go`/`module.go`: `App.Partials(dir)`, `App.MustParseTemplate(path)`, `loadPartials`, `setPartials`, `parseOneTemplate` accepts partials — shared partial templates injected into all module and custom handler templates | Agreed | 2026-04-02 |
| A63 | `head.go`/`templates.go`/`templatedata.go`/`forge.go`/`module.go`: `HeadAssets`, `FaviconLink`, `ScriptTag` SEOOption — injects static assets (preconnect, stylesheets, favicons, scripts) into forge:head on every page via `app.SEO(&HeadAssets{...})` | Agreed | 2026-04-03 |
| A64 | `head.go`/`templatedata.go`: `PageHead` exported struct — embeddable head fields for custom handler data structs; `TemplateData[T]` refactored to embed `PageHead` anonymously | Agreed | 2026-04-03 |
| A65 | `module.go`/`templatedata.go`/`templates.go`: `ContextFunc` module option — per-request extra data injected into `TemplateData.Extra` for list and show renders | Agreed | 2026-04-04 |
| A66 | `auth.go`/`forge.go`/`forge-mcp`: `TokenStore` — named revocable bearer tokens, DB-backed `VerifyBearerToken`, three Admin MCP tools (`create_token`, `list_tokens`, `revoke_token`) | Agreed | 2026-04-05 |
| A67 | `templatehelpers.go`: `forgeHTML` / `forge_html` — trusted raw HTML passthrough added to `TemplateFuncMap` | Agreed | 2026-04-05 |
| D26 | `auth.go`/`errors.go`/`forge-mcp/tool.go`: last-admin guard on `TokenStore.Revoke` — `ErrLastAdmin` sentinel (409); `Revoke` refuses to revoke the last active admin token; `revoke_token` MCP tool surfaces actionable message | Agreed | 2026-04-06 |
| D27 | `mcp.go`/`module.go`/`forge-mcp/mcp.go`: field format semantics — `forge_format` and `forge_description` struct tags populate `MCPField.Format` and `MCPField.Description`; forge-mcp emits `"description"` key in JSON Schema properties with priority logic | Agreed | 2026-04-07 |
| D28 | `forge-cli/`: operator CLI — stdlib-only submodule; content CRUD + lifecycle via HTTP REST; token management via MCP JSON-RPC; YAML-subset frontmatter parser; `forge-cli/v0.1.0` | Agreed | 2026-04-07 |
| A68 | `storage.go`/`module.go`: doc comments on `Table` and `At` extended to surface irregular pluralisation pitfalls (Story → "storys") | Agreed | 2026-04-09 |
| D29 | `nav.go`/`forge.go`/`templatedata.go`/`templates.go`/`module.go`/`forge-mcp`: NavTree — first-class navigation abstraction; `NavItem`, `NavTree`, `NavModeDB`/`NavModeCode`, `App.Nav()`, `App.NavTree()`, `TemplateData.Nav`, four MCP nav tools (Editor role) | Agreed | 2026-04-11 |
| D30 | `config.go`/`forge.go`: forge.config file-based configuration — `loadConfigFile`, `mergeFileConfig`; `Config.AppSchema`, `Config.OGDefaults`; `MustConfig` loads `forge.config` (or `FORGE_CONFIG` env var path); Go-code fields always win; `secret` key panics | Agreed | 2026-04-11 |

---

> **Body text** for all decisions lives in [decisions/core.md](decisions/core.md) (Decisions 1–22 + Amendments A19–A65) and [decisions/phase2.md](decisions/phase2.md) (Decision 25 + Amendments A66–A67 + Decisions 26–28, Amendment A68).
