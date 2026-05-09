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
| A69 | `README.md`: shortened to <150 lines; `REFERENCE.md`: new full API reference file; `example/blog/main.go` package comment updated to v1.11.0 | Agreed | 2026-04-14 |
| A70 | `README.md`: tagline, named value section (15 features), remove duplicate table row, `(*Post)(nil)` comment, real AfterPublish body, examples pointer, remove flat bullet list | Agreed | 2026-04-14 |
| A71 | `README.md`: replace tagline with plain-language framework description; add 30-second start section (clone + run) before "What Forge gives you" | Agreed | 2026-04-15 |
| A72 | `VISION.md`: insert "What Forge is" (typed state layer for AI agents); insert "The two-layer model" (Core AGPL / Cloud commercial); replace Roadmap (Phases 1–2 ✅ DONE, Phase 3 Cloud private beta, Phase 4 Cloud GA) | Agreed | 2026-04-18 |
| A73 | `forge.go`/`config.go`: add `MediaPath` and `MediaMaxSize` fields to `Config`; parse `media_path` and `media_max_size` from `forge.config` file; add `App.Config() Config` read-only accessor for forge-media submodule access | Agreed | 2026-04-25 |
| A74 | `head.go`/`templates.go`/`example_test.go`: rename `FaviconLink` → `HeadLink`; rename `HeadAssets.Favicons []FaviconLink` → `HeadAssets.Links []HeadLink` — any `<link>` element, not icons only | Agreed | 2026-04-18 |
| D31 | `forge-media/`: new optional submodule — `MediaStore` interface, `LocalMediaStore`, HTTP handlers (`Register`), `forge.MCPModule` implementation, `MediaRecord.GetSlug()`; `forge-mcp`: `WithModule` server option | Agreed | 2026-04-18 |
| D29 | `nav.go`/`forge.go`/`templatedata.go`/`templates.go`/`module.go`/`forge-mcp`: NavTree — first-class navigation abstraction; `NavItem`, `NavTree`, `NavModeDB`/`NavModeCode`, `App.Nav()`, `App.NavTree()`, `TemplateData.Nav`, four MCP nav tools (Editor role) | Agreed | 2026-04-11 |
| D30 | `config.go`/`forge.go`: forge.config file-based configuration — `loadConfigFile`, `mergeFileConfig`; `Config.AppSchema`, `Config.OGDefaults`; `MustConfig` loads `forge.config` (or `FORGE_CONFIG` env var path); Go-code fields always win; `secret` key panics | Agreed | 2026-04-11 |
| A75 | `markdown.go`: `renderMarkdown` HTML passthrough — lines whose trimmed form starts with `<` are emitted verbatim without HTML-escaping, unblocking HTML blocks in trusted body content | Agreed | 2026-04-22 |
| A76 | `go.mod` (all modules): bump minimum Go version `1.22` → `1.26.2`; rename all module paths from `github.com/forge-cms/...` to `forge-cms.dev/...`; update all imports, documentation, and `forgeVersions()` prefix logic | Agreed | 2026-04-30 |
| A77 | `head.go`/`module.go`/`templates.go`: `ListHeadFunc` option — new `listHeadFuncOption[T]` type; `listHeadFunc any` field on `Module[T]`; `renderListHTML` resolves list head via `listHeadFunc`; fixes empty `<title>` on module list pages | Agreed | 2026-05-02 |
| A78 | `node.go`: `ValidateStruct` unexported to `validateStruct`; `RunValidation` is now the sole public entry point for struct-tag validation. Breaking change: removes exported symbol. | Agreed | 2026-05-04 |
| A79 | `forge-media/media.go`: `LocalMediaStore.Store()` and `.Delete()` use `os.Root` (Go 1.24+) instead of `filepath.Join` — path traversal prevented at OS level. Security fix. Two new tests added. | Agreed | 2026-05-04 |
| A80 | `storage.go`: `SeqRepository[T]` optional interface + `Seq` methods on `MemoryRepo[T]` and `SQLRepo[T]` — lazy `iter.Seq2[T, error]` streaming without full result-set load. Additive; `Repository[T]` unchanged. | Agreed | 2026-05-04 |
| A81 | `go.mod`: `modernc.org/sqlite` added as test-only dependency; enables `TestRepoParity_SQLRepo` against real in-memory SQLite. Exception to zero-dep rule: CGO-free, test-only, single file, documented precedent. | Agreed | 2026-05-04 |
| A82 | `forge.go` / `config.go` / `static.go`: `Config.Dev bool` + `App.Static(prefix, prod, devDir)` + forge.config `dev` key. Dev mode serves from disk; prod mode serves embedded FS with immutable Cache-Control. Replaces per-site boilerplate. | Agreed | 2026-05-04 |
| A83 | `auth.go` / `forge.go`: `TokenStore.ensureBootstrap` — auto-creates a bootstrap admin token (slog.Warn) when `forge_tokens` is empty at startup. `forge-cli/init.go`: new `init` subcommand bootstraps a new instance using the bootstrap token. `forge-cli` v0.3.0. | Agreed | 2026-05-04 |
| A84 | `REFERENCE.md`: accuracy fixes and gap-fill for v1.16.0 — corrects 5 inaccuracies (version examples, broken links, RateLimit section, `app.Content` fallback path); adds 6 missing sections (TokenStore, NavTree, OGDefaults/AppSchema, AbsURL, SeqRepository, forge-cli); adds `ErrLastAdmin` sentinel. | Agreed | 2026-05-05 |
| A85 | `.github/copilot-instructions.md`: new "Docs and content workflow" section inserted between "Standard step workflow" and "Release tagging". `FEATURELIST.md`: new file — complete feature list for v1.16.0. | Agreed | 2026-05-05 |
| A86 | `.github/copilot-instructions.md`: new "CLI and MCP tool parity" section — every MCP tool must have a CLI equivalent in the same release; notes current nav commands gap. | Agreed | 2026-05-05 |
| A87 | `signals.go`: `AfterSchedule Signal = "after_schedule"` — fires after Scheduled transition, alongside AfterUpdate. Enables `post.scheduled` webhook events and per-signal MCP subscription routing. | Agreed | 2026-05-06 |
| A88 | `forge.go`: `App.Webhooks(store *WebhookStore)`, `App.WebhookPool() WebhookJobQueue`, `App.injectWebhookHooks()` — wires outbound webhook infrastructure into the App; pool started/stopped with server lifecycle. | Agreed | 2026-05-08 |
| A89 | `module.go`: `afterHook`/`setAfterHook`/`notifyAfter` — post-lifecycle callback slot on `Module[T]`; `notifyAfter` wraps `dispatchAfter`+`afterHook`; `MCPSchedule` dispatches `AfterSchedule`. CLI parity: `forge webhook` ships with `forge-mcp` webhook tools (A86 gap closed). | Agreed | 2026-05-08 |
| A90 | `REFERENCE.md`: replace hardcoded `1.16.0`/`1.6.1` version literals in health endpoint examples with `x.y.z` placeholder (3 occurrences). `FEATURELIST.md`: correct `delete_[type]` role from `Author+` to `Editor+` — matches `authoriseEditor()` enforcement in `forge-mcp/tool.go`. | Agreed | 2026-05-07 |
| A91 | `webhook.go`: `WHERE active = 1` → `WHERE active` (Postgres BOOLEAN parity). DDL godoc: `DEFAULT 1`→`DEFAULT TRUE`, `DATETIME`→`TIMESTAMPTZ` in `webhook.go`; `BLOB`→`BYTEA`, `DATETIME`→`TIMESTAMPTZ` (5 occurrences) in `outbound.go`. `README.md`: add token management reference link. | Agreed | 2026-05-08 |
| A92 | `auth.go`: `encodePreviewToken(prefix,slug,...)`/`decodePreviewToken` (internal, prefix-bound). `forge.go`: `Config.PreviewTokenExpiry`, `App.GeneratePreviewToken(prefix,slug)`, `App.BaseURL()`. `module.go`: `secret` field, `setSecret`, preview bypass in `showHandler`. forge-mcp: `create_preview_url` Admin tool. forge-cli: `preview` subcommand. Milestone 12 — v1.18.0. | Agreed | 2026-05-08 |
| A93 | `auth.go`: `encodeUploadToken(secret,ttl)`/`decodeUploadToken` (internal). `forge.go`: `Config.MediaUploadTokenExpiry`, `App.GenerateUploadToken()`, `App.ValidateUploadToken(token)`. forge-media: `UploadToken` header in `handleUpload`, image-only MIME whitelist for token uploads, AVIF support, hex filename prefix. forge-mcp: `create_upload_token` Author+ tool. forge-cli: media subcommands documented + AVIF. Milestone 13 — v1.19.0. | Agreed | 2026-05-09 |

---

> **Body text** for all decisions lives in [decisions/core.md](decisions/core.md) (Decisions 1–22 + Amendments A19–A65) and [decisions/phase2.md](decisions/phase2.md) (Decision 25 + Amendments A66–A67 + Decisions 26–28, Amendment A68–A76).
