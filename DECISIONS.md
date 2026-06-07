# Smeldr — Decision Log

This document is the permanent record of every architectural decision made for Smeldr.
Each entry captures what was decided, why, what was rejected, and what consequences follow.

**Format:** decisions are immutable once locked. New decisions are appended.
Revisions to existing decisions require a new entry that supersedes the original.

**How to use this document:**
- Before implementing a feature, check if a relevant decision exists
- Before changing an interface, check what depends on it here
- When onboarding (human or AI), read this before touching code

## decisions/ file structure

| File | Contents | New entries? |
|------|----------|-------------|
| `DECISIONS.md` | Index only — this file | No |
| `decisions/recent.md` | Rolling working file (~20KB limit) | Yes — new decisions |
| `decisions/nondecisions.md` | Non-Decisions only | Yes — Non-Decisions directly |
| `decisions/core.md` | Archive: D1–D22, A19–A65, A87, A88–A95, A97–A101 | No |
| `decisions/phase2-archive.md` | Superseded archive (was phase2.md; content now in topic files) | No |
| `decisions/phase3-archive.md` | Archive: A102–A120 | No |
| `decisions/phase4-archive.md` | Archive: A121–A125 | No |
| `decisions/auth.md` | Archive: D25, A66, D26, A83 | No |
| `decisions/content-api.md` | Archive: D27, A67, A74, A75, A77 | No |
| `decisions/docs.md` | Archive: D28, A69–A72, A76, A84–A86 | No |
| `decisions/media.md` | Archive: A73, D31, A79 | No |
| `decisions/nav.md` | Archive: D29, D30, A82 | No |
| `decisions/storage.md` | Archive: A68, A78, A80, A81 | No |
| `decisions/[topic].md` | Topic files on architect instruction | Only when instructed |

**Archiving rule:** When `recent.md` reaches ~20KB, corepilot reports it at session start:
"recent.md is Xkb — ready for archiving." The architect decides groupings and topic file
names via NEXT.md. Corepilot never archives autonomously. Non-Decisions go to
`nondecisions.md` directly and do not count toward the limit.

---

## Decision index

### Core — [decisions/core.md](decisions/core.md)

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
| A88 | `forge.go`: `App.Webhooks(store *WebhookStore)`, `App.WebhookPool() WebhookJobQueue`, `App.injectWebhookHooks()` — wires outbound webhook infrastructure into the App; pool started/stopped with server lifecycle. | Agreed | 2026-05-08 |
| A89 | `module.go`: `afterHook`/`setAfterHook`/`notifyAfter` — post-lifecycle callback slot on `Module[T]`; `notifyAfter` wraps `dispatchAfter`+`afterHook`; `MCPSchedule` dispatches `AfterSchedule`. CLI parity: `forge webhook` ships with `forge-mcp` webhook tools (A86 gap closed). | Agreed | 2026-05-08 |
| A90 | `REFERENCE.md`: replace hardcoded `1.16.0`/`1.6.1` version literals in health endpoint examples with `x.y.z` placeholder (3 occurrences). `FEATURELIST.md`: correct `delete_[type]` role from `Author+` to `Editor+` — matches `authoriseEditor()` enforcement in `forge-mcp/tool.go`. | Agreed | 2026-05-07 |
| A91 | `webhook.go`: `WHERE active = 1` → `WHERE active` (Postgres BOOLEAN parity). DDL godoc: `DEFAULT 1`→`DEFAULT TRUE`, `DATETIME`→`TIMESTAMPTZ` in `webhook.go`; `BLOB`→`BYTEA`, `DATETIME`→`TIMESTAMPTZ` (5 occurrences) in `outbound.go`. `README.md`: add token management reference link. | Agreed | 2026-05-08 |
| A92 | `auth.go`: `encodePreviewToken(prefix,slug,...)`/`decodePreviewToken` (internal, prefix-bound). `forge.go`: `Config.PreviewTokenExpiry`, `App.GeneratePreviewToken(prefix,slug)`, `App.BaseURL()`. `module.go`: `secret` field, `setSecret`, preview bypass in `showHandler`. forge-mcp: `create_preview_url` Admin tool. forge-cli: `preview` subcommand. Milestone 12 — v1.18.0. | Agreed | 2026-05-08 |
| A93 | `auth.go`: `encodeUploadToken(secret,ttl)`/`decodeUploadToken` (internal). `forge.go`: `Config.MediaUploadTokenExpiry`, `App.GenerateUploadToken()`, `App.ValidateUploadToken(token)`. forge-media: `UploadToken` header in `handleUpload`, image-only MIME whitelist for token uploads, AVIF support, hex filename prefix. forge-mcp: `create_upload_token` Author+ tool. forge-cli: media subcommands documented + AVIF. Milestone 13 — v1.19.0. | Agreed | 2026-05-09 |
| A94 | Signal bus: `SignalEvent`, `afterHookMeta`, `buildSignalEvent` (`signals.go`). `App.OnSignal`, `App.dispatchBus`, `App.wireSignalBus` replacing `injectWebhookHooks` (`forge.go`). `webhookDispatch` (`webhook.go`). `OutboundDelivery` interface (`outbound.go`). `notifyAfter` signature extended with `afterHookMeta`. Milestone 14 — v1.20.0. | Agreed | 2026-05-11 |
| A95 | `mergeFileConfig`: field-level `OGDefaults` merge — `og_image` in `forge.config` overrides Go-code `Image.URL`; all other `OGDefaults` fields retain Go-code values. Only `forge.config` key designed to take precedence over Go code. No exported symbols changed. v1.21.0. | Agreed | 2026-05-14 |

### Token management — [decisions/auth.md](decisions/auth.md)

| # | Title | Status | Date |
|---|-------|--------|------|
| [decisions/auth.md](decisions/auth.md) | Token management archive | Archive | 2026-05-17 |
| D25 | Token management | Locked | 2026-04-05 |
| A66 | `auth.go`/`forge.go`/`forge-mcp`: `TokenStore` — named revocable bearer tokens, DB-backed `VerifyBearerToken`, three Admin MCP tools (`create_token`, `list_tokens`, `revoke_token`) | Agreed | 2026-04-05 |
| D26 | `auth.go`/`errors.go`/`forge-mcp/tool.go`: last-admin guard on `TokenStore.Revoke` — `ErrLastAdmin` sentinel (409); `Revoke` refuses to revoke the last active admin token; `revoke_token` MCP tool surfaces actionable message | Agreed | 2026-04-06 |
| A83 | `auth.go` / `forge.go`: `TokenStore.ensureBootstrap` — auto-creates a bootstrap admin token (slog.Warn) when `forge_tokens` is empty at startup. `forge-cli/init.go`: new `init` subcommand bootstraps a new instance using the bootstrap token. `forge-cli` v0.3.0. | Agreed | 2026-05-04 |

### Content API — [decisions/content-api.md](decisions/content-api.md)

| # | Title | Status | Date |
|---|-------|--------|------|
| [decisions/content-api.md](decisions/content-api.md) | Content API archive | Archive | 2026-05-17 |
| D27 | `mcp.go`/`module.go`/`forge-mcp/mcp.go`: field format semantics — `forge_format` and `forge_description` struct tags populate `MCPField.Format` and `MCPField.Description`; forge-mcp emits `"description"` key in JSON Schema properties with priority logic | Agreed | 2026-04-07 |
| A67 | `templatehelpers.go`: `forgeHTML` / `forge_html` — trusted raw HTML passthrough added to `TemplateFuncMap` | Agreed | 2026-04-05 |
| A74 | `head.go`/`templates.go`/`example_test.go`: rename `FaviconLink` → `HeadLink`; rename `HeadAssets.Favicons []FaviconLink` → `HeadAssets.Links []HeadLink` — any `<link>` element, not icons only | Agreed | 2026-04-18 |
| A75 | `markdown.go`: `renderMarkdown` HTML passthrough — lines whose trimmed form starts with `<` are emitted verbatim without HTML-escaping, unblocking HTML blocks in trusted body content | Agreed | 2026-04-22 |
| A77 | `head.go`/`module.go`/`templates.go`: `ListHeadFunc` option — new `listHeadFuncOption[T]` type; `listHeadFunc any` field on `Module[T]`; `renderListHTML` resolves list head via `listHeadFunc`; fixes empty `<title>` on module list pages | Agreed | 2026-05-02 |

### Documentation — [decisions/docs.md](decisions/docs.md)

| # | Title | Status | Date |
|---|-------|--------|------|
| [decisions/docs.md](decisions/docs.md) | Documentation archive | Archive | 2026-05-17 |
| D28 | `forge-cli/`: operator CLI — stdlib-only submodule; content CRUD + lifecycle via HTTP REST; token management via MCP JSON-RPC; YAML-subset frontmatter parser; `forge-cli/v0.1.0` | Agreed | 2026-04-07 |
| A69 | `README.md`: shortened to <150 lines; `REFERENCE.md`: new full API reference file; `example/blog/main.go` package comment updated to v1.11.0 | Agreed | 2026-04-14 |
| A70 | `README.md`: tagline, named value section (15 features), remove duplicate table row, `(*Post)(nil)` comment, real AfterPublish body, examples pointer, remove flat bullet list | Agreed | 2026-04-14 |
| A71 | `README.md`: replace tagline with plain-language framework description; add 30-second start section (clone + run) before "What Forge gives you" | Agreed | 2026-04-15 |
| A72 | `VISION.md`: insert "What Forge is" (typed state layer for AI agents); insert "The two-layer model" (Core AGPL / Cloud commercial); replace Roadmap (Phases 1–2 ✅ DONE, Phase 3 Cloud private beta, Phase 4 Cloud GA) | Agreed | 2026-04-18 |
| A76 | `go.mod` (all modules): bump minimum Go version `1.22` → `1.26.2`; rename all module paths from `github.com/forge-cms/...` to `forge-cms.dev/...`; update all imports, documentation, and `forgeVersions()` prefix logic | Agreed | 2026-04-30 |
| A84 | `REFERENCE.md`: accuracy fixes and gap-fill for v1.16.0 — corrects 5 inaccuracies (version examples, broken links, RateLimit section, `app.Content` fallback path); adds 6 missing sections (TokenStore, NavTree, OGDefaults/AppSchema, AbsURL, SeqRepository, forge-cli); adds `ErrLastAdmin` sentinel. | Agreed | 2026-05-05 |
| A85 | `.github/copilot-instructions.md`: new "Docs and content workflow" section inserted between "Standard step workflow" and "Release tagging". `FEATURELIST.md`: new file — complete feature list for v1.16.0. | Agreed | 2026-05-05 |
| A86 | `.github/copilot-instructions.md`: new "CLI and MCP tool parity" section — every MCP tool must have a CLI equivalent in the same release; notes current nav commands gap. | Agreed | 2026-05-05 |

### Media — [decisions/media.md](decisions/media.md)

| # | Title | Status | Date |
|---|-------|--------|------|
| [decisions/media.md](decisions/media.md) | Media archive | Archive | 2026-05-17 |
| A73 | `forge.go`/`config.go`: add `MediaPath` and `MediaMaxSize` fields to `Config`; parse `media_path` and `media_max_size` from `forge.config` file; add `App.Config() Config` read-only accessor for forge-media submodule access | Agreed | 2026-04-25 |
| D31 | `forge-media/`: new optional submodule — `MediaStore` interface, `LocalMediaStore`, HTTP handlers (`Register`), `forge.MCPModule` implementation, `MediaRecord.GetSlug()`; `forge-mcp`: `WithModule` server option | Agreed | 2026-04-18 |
| A79 | `forge-media/media.go`: `LocalMediaStore.Store()` and `.Delete()` use `os.Root` (Go 1.24+) instead of `filepath.Join` — path traversal prevented at OS level. Security fix. Two new tests added. | Agreed | 2026-05-04 |

### Navigation — [decisions/nav.md](decisions/nav.md)

| # | Title | Status | Date |
|---|-------|--------|------|
| [decisions/nav.md](decisions/nav.md) | Navigation archive | Archive | 2026-05-17 |
| D29 | `nav.go`/`forge.go`/`templatedata.go`/`templates.go`/`module.go`/`forge-mcp`: NavTree — first-class navigation abstraction; `NavItem`, `NavTree`, `NavModeDB`/`NavModeCode`, `App.Nav()`, `App.NavTree()`, `TemplateData.Nav`, four MCP nav tools (Editor role) | Agreed | 2026-04-11 |
| D30 | `config.go`/`forge.go`: forge.config file-based configuration — `loadConfigFile`, `mergeFileConfig`; `Config.AppSchema`, `Config.OGDefaults`; `MustConfig` loads `forge.config` (or `FORGE_CONFIG` env var path); Go-code fields always win; `secret` key panics | Agreed | 2026-04-11 |
| A82 | `forge.go` / `config.go` / `static.go`: `Config.Dev bool` + `App.Static(prefix, prod, devDir)` + forge.config `dev` key. Dev mode serves from disk; prod mode serves embedded FS with immutable Cache-Control. Replaces per-site boilerplate. | Agreed | 2026-05-04 |

### Storage — [decisions/storage.md](decisions/storage.md)

| # | Title | Status | Date |
|---|-------|--------|------|
| [decisions/storage.md](decisions/storage.md) | Storage archive | Archive | 2026-05-17 |
| A68 | `storage.go`/`module.go`: doc comments on `Table` and `At` extended to surface irregular pluralisation pitfalls (Story → "storys") | Agreed | 2026-04-09 |
| A78 | `node.go`: `ValidateStruct` unexported to `validateStruct`; `RunValidation` is now the sole public entry point for struct-tag validation. Breaking change: removes exported symbol. | Agreed | 2026-05-04 |
| A80 | `storage.go`: `SeqRepository[T]` optional interface + `Seq` methods on `MemoryRepo[T]` and `SQLRepo[T]` — lazy `iter.Seq2[T, error]` streaming without full result-set load. Additive; `Repository[T]` unchanged. | Agreed | 2026-05-04 |
| A81 | `go.mod`: `modernc.org/sqlite` added as test-only dependency; enables `TestRepoParity_SQLRepo` against real in-memory SQLite. Exception to zero-dep rule: CGO-free, test-only, single file, documented precedent. | Agreed | 2026-05-04 |

### Phase 3 Archive — [decisions/phase3-archive.md](decisions/phase3-archive.md)

| # | Title | Status | Date |
|---|-------|--------|------|
| A102 | `module.go`: `APIOnly()` module option — marks a module as REST/MCP/CLI-only with no public HTML surface. `GET /{prefix}` and `GET /{prefix}/{slug}` with `Accept: text/html` return 404. JSON routes and all MCP tools unchanged. `APIOnly()` + `SingleInstance()` panics at startup. v1.24.0. | Agreed | 2026-05-22 |
| A103 | `auth.go`: `VerifyTokenString(token string, secret []byte, store *TokenStore) (User, bool)` — verifies a raw bearer token without `*http.Request`. Identical to `VerifyBearerToken` but takes the token string directly; DB lookup uses `context.Background()`. Enables forge-oauth (and other downstream libraries) to validate Forge tokens without importing the HTTP layer. v1.25.0. | Agreed | 2026-05-24 |
| A104 | `forge.go`: `/_health` JSON key and startup log rename. `"forge"` → `"core"`, `"forge_mcp"` → `"mcp"` in `/_health` response; startup log prefix `"forge: "` → `"smeldr: "`. Follows module path rename (T59 Phase 0C). Breaking change for health monitors. | Agreed | 2026-05-26 |
| A105 | T59 Phase 2.4: all smeldr.dev/* modules tagged and published. First Go-resolvable versions on smeldr.dev/* paths. 7/8 confirmed via go get; smeldr.dev/pgx blocked pending vanity config fix (architect decision required). | Agreed | 2026-05-27 |
| A106 | T59 doc rename: forge-cms.dev → smeldr.dev across all core documentation. Renamed all forge-cms.dev/* → smeldr.dev/*, github.com/forge-cms/* → github.com/smeldr/*, and prose module names (forge-mcp → smeldr.dev/mcp, etc.) across 10 doc files. Docs only — no code changes. | Agreed | 2026-05-28 |
| A107 | T62 package rename: `package forge` → `package smeldr` in all 75 root-package Go files. 9 template function names renamed (forge:head → smeldr:head, forge_markdown → smeldr_markdown, etc.), 2 struct tag keys (forge_format → smeldr_format, forge_description → smeldr_description), 2 cookie names (forge_csrf → smeldr_csrf, forge_consent → smeldr_consent). All standalone modules (mcp, media, social, agent, oauth, cli) updated. Breaking change — v1.26.0. | Agreed | 2026-05-28 |
| A108 | T64+T65: `forge.config` → `smeldr.config`, `FORGE_CONFIG` → `SMELDR_CONFIG` (breaking for operators). Error prefix `"forge: "` → `"smeldr: "` in ~48 strings across 14 files. `skills/forge.md` → `skills/smeldr.md` (core + common repos). No exported API or schema changes. v1.27.0. | Agreed | 2026-05-28 |
| A109 | T66: `forge_*` → `smeldr_*` DB table rename (7 tables); `migrateLegacyTableNames` auto-migration at `New()` for SQLite. PostgreSQL operators must migrate manually. v1.28.0. | Agreed | 2026-05-28 |
| A110 | T63: `SiteConfig` singleton — global site-configuration content type in core. `SiteConfig` struct with 5 fields (`site_name`, `title_separator`, `og_image`, `x_handle`, `head_script`); `NewSiteConfigModule(db)` factory; `CreateSiteConfigTable(db)` DDL helper. Configurable via MCP after first deploy — no rebuild required. v1.29.0. | Agreed | 2026-05-28 |
| A111 | T74+T67: `HeadAssets.RawHead template.HTML` — verbatim HTML injected into `<head>` after all other HeadAssets output; zero value is no-op (T74). Validation/auto-slug struct tag key renamed `forge:"required"` → `smeldr:"required"` — breaking for operators with custom content types (T67). v1.30.0. | Agreed | 2026-05-29 |
| A112 | T71: `xAuthBase` changed `"https://twitter.com"` → `"https://x.com"` in social/twitter.go — fixes X OAuth login loop caused by session cookie domain mismatch. social v0.7.4. | Agreed | 2026-05-29 |
| A113 | T57 oauth: `POST /oauth/revoke` per RFC 7009 — `revokeHandler` added; always 200 OK; revokes refresh tokens via `DeleteRefreshToken`; access tokens expire naturally. oauth v0.1.4. | Agreed | 2026-05-29 |
| A114 | T57 cli: `smeldr-cli oauth revoke <token>` — POSTs to `FORGE_URL/oauth/revoke`; CLI parity with A113. cli v0.9.3. | Agreed | 2026-05-29 |
| A115 | T58: `forgemcp.Server.Register(app *smeldr.App)` — mounts all MCP+OAuth routes on forge App in one call; delegates to `s.Handler()` mux. `Handler()` unchanged. go.mod: core v1.30.0, oauth v0.1.4. mcp v1.13.0. | Agreed | 2026-05-29 |

### Recent — [decisions/recent.md](decisions/recent.md)

| # | Title | Status | Date |
|---|-------|--------|------|
| A132 | T100 Step 4: social package rename `forgesocial` → `social` (smeldr.dev/social v0.8.0). Package decl in all 25 files (21 internal + 4 external `forgesocial_test`→`social_test`), ~120 error/panic/log-string prefixes `forgesocial:`→`social:`, drop alias + `forgesocial.X`→`social.X`, stale cross-refs `forge-mcp`/`forgemcp.X`→`mcp` (A130), social_test.go + router_test.go local var `social`→`svc` (package-name collision), README + CHANGELOG. Preserved: `forge_social_*` DB tables (65 refs, 8 tables — T102), `X-Forge-Signature` (T86/T87), standalone brand words (T101). Gate (forgesocial\|forge-social\|forgemcp\|forge-mcp in *.go) = ZERO. Breaking-MINOR. | Agreed | 2026-06-07 |
| A131 | T100 Step 3: media package rename `forgemedia` → `media` (smeldr.dev/media v1.4.0). Package decl in all 8 files (external `forgemedia_test`→`media_test`), error/panic prefixes `forgemedia:`/`forgemedia.New:`→`media:`/`media.New:` (16), drop alias + `forgemedia.X`→`media.X`, package-doc Forge→Smeldr, stale cross-refs `forge-mcp`/`forgemcp.X`→`mcp` (Step 2), canary fixture renamed, CHANGELOG. Preserved: `forge_media` DB table (10 refs), standalone brand words (T101). v1.3.0→v1.4.0 (T95 → v1.5.0). No exported-symbol/behaviour change. Grep gate literally zero. Independent step. | Agreed | 2026-06-06 |
| A130 | T100 Step 2: mcp package rename `forgemcp` → `mcp` + oauth v0.2.0 adoption (smeldr.dev/mcp v1.17.0). Package decl in all 16 files, godoc selectors, package-doc Forge→Smeldr; dropped `forgeoauth` alias + `forgeoauth.X`→`oauth.X` + dep bump v0.1.5→v0.2.0; `WithOAuth` param `oauth`→`srv` (shadow); stale godoc `forge_format`/`forge_description`→`smeldr_*` (T62/A107); `forge-media`→media, `forge-operator`→operator; README + CHANGELOG. Preserved: `WithForgeFallback`, `forge://` parse-compat, `forge-cli`/`forgemedia.Register` (Step 5/3), standalone brand words. No exported-symbol/behaviour change. Breaking-minor. | Agreed | 2026-06-06 |
| A129 | T100 Step 1: oauth package rename `forgeoauth` → `oauth` (smeldr.dev/oauth v0.2.0). Package decl in 9 production + 2 test files, godoc selectors, error/panic/slog prefixes (`forgeoauth:`/`forge-oauth:` → `oauth:`), README v0.2.0 + `forge-cms.dev` → `smeldr.dev` paths, CHANGELOG. Preserved: `forge_oauth_*` DB tables, `forgemcp` refs (Step 2), test fixtures. No exported-symbol or behaviour change. Breaking-minor; gates Step 2 (mcp). | Agreed | 2026-06-06 |
| A87 | `signals.go`: `AfterSchedule Signal = "after_schedule"` — fires after Scheduled transition, alongside AfterUpdate. Enables `post.scheduled` webhook events and per-signal MCP subscription routing. | Agreed | 2026-05-06 |
| A126 | T04: `ContentTypeStats`, `SiteStats`, `StatsExtProvider`, `App.Stats()`, `App.StatsHandler()`, `App.RegisterStatsProvider()` + `GET /_stats` (Admin). Per-type counts per status; external provider interface for media/other modules. Go 1.26.4 bump (GO-2026-5039, GO-2026-5037). core v1.35.0. | Agreed | 2026-06-04 |
| A128 | T79: in-memory log capture + `GET /_logs` (Admin). `App.CaptureLogs(opts...)`, `LogEntry`, `WithLogCapacity` (default 500), `WithLogLevel` (default WARN); teeing `slog.Handler` (Enabled OR rule so the stderr threshold is never narrowed; `WithAttrs`/`WithGroup` fidelity) over a bounded overwrite-oldest ring (monotonic `seq` + `dropped`). Built-in `*slog.defaultHandler` is substituted with a stderr text handler to avoid a fatal slog/log re-entrancy cycle (`slog.SetDefault` repoints the log package). `GET /_logs` is plain HTTP + bearer (works when MCP is down), envelope `{capacity,count,dropped,entries}` newest-first, query `level`/`limit`/`since`; route absent → 404 when `CaptureLogs` was not called. Ephemeral live-debugging facility (NOT log storage; stderr stays the durable path); HTTP/CLI-only, no MCP tool. core v1.36.0. | Agreed | 2026-06-05 |
| A127 | `smeldr.dev/cli`: `nav` command group (T18). Four Editor-role CLI commands reach full parity with nav MCP tools: `nav list` (table: ID, LABEL, PATH, PARENT, HIDDEN, GHOST, SORT; `--json`), `nav create --label <label> [--path] [--parent-id] [--module] [--hidden] [--ghost] [--sort-order]`, `nav update <id> [same flags]`, `nav delete <id>` (cascades). Closes the last confirmed CLI/MCP gap. cli v0.13.0. | Agreed | 2026-06-04 |
| A125 | T30: `CreateRedirectsTable`, `App.Redirects(db)`, `App.RedirectDB()`, `RedirectStore.Delete` + MCP `create_redirect`/`list_redirects`/`delete_redirect` (Editor+) + CLI `redirect list/create/delete`. Auto-ensure table, no DDL; changes live immediately. core v1.34.0 · mcp v1.16.0 · cli v0.12.0. | Agreed | 2026-06-04 |
| A124 | T53: `NewRateLimiter` + `NewInMemoryCache` — additive constructors returning `(middleware, stopFn)`. Stop function closes an internal channel and blocks until the goroutine confirms exit (`sync.OnceFunc`, idempotent). Existing `RateLimit`/`InMemoryCache` delegate and discard stop — no API breakage. Fixes goroutine leak in tests. core v1.33.0 (minor — new exported symbols). | Agreed | 2026-06-03 |
| A123 | T86 wire-level dual-compat sweep. mcp v1.15.0: resource URIs generated as `smeldr://`; both `smeldr://` and legacy `forge://` accepted on read/subscribe. core v1.32.0: `httpDeliver` dual-emits `X-Smeldr-*` + `X-Forge-*` webhook headers (same values). cli v0.11.0: `SMELDR_URL/TOKEN/MCP_URL` preferred with `FORGE_*` fallback; `init` writes `.smeldr-cli.env`. All changes additive/non-breaking. Legacy `forge://` accept and `X-Forge-*` emit deferred to T87. | Agreed | 2026-06-03 |
| A122 | T88+T89 doc accuracy follow-up: `forge:"required"` → `smeldr:"required"` in live code examples. `README.md` minimal example (2 lines). `core/skills/` full-sync from `common/agent/skills/` — fixes stale struct tags, footer paths, and adds missing sections (SiteConfig, RawHead, block MCP tools, oauth). Enforcement: copilot-instructions M-number doc-gate updated to unconditional `Copy-Item` sync command. Level 1, docs-only, no version bump. | Agreed | 2026-06-02 |
| A121 | T85 core-repo brand sweep: "Forge" → "Smeldr" in all living doc prose and headers across 17 files. Scope: README.md, copilot-instructions.md, CHANGELOG.md (header only), DECISIONS.md (header/intro), ARCHITECTURE.md, REFERENCE.md, FEATURELIST.md, VISION.md (incl. forge-admin → smeldr-admin, Forge Cloud → Smeldr Cloud), SECURITY.md, skills/smeldr.md (+ version line resync), BENCHMARKS.md, CLA.md, Milestone_BACKLOG_TEMPLATE.md, NOTES.md, ERROR_HANDLING.md, example/README.md, example/api/README.md. Preserve: X-Forge-* headers, forge:// URI, FORGE_* env vars, forge-cli binary, historical CHANGELOG/decisions narrative, code identifiers. No version bump. | Agreed | 2026-06-01 |
| A120 | `serveblocks.go`: reference-field resolution (T82). A `{Name}ID` field resolves to a `.{Name}` sub-object = the referenced Published block's `buildData` (`ImageID` → `.Image` with `.MediaURL`/`.AltText`/`.Caption`). `blockFieldFormats.refs` + `refs:["ImageID"]` on content_block/contact_card/hero; `refIDsOf`; one batched `IN()` ref-load pass in `loadTree`; resolve loop in `renderBlock`. `{{ with }}`-guarded (absent/unpublished/dangling → no key), Published-only, one level, no N+1 (counting-DB test). 8 tests. Extends A118; held core v1.31.0. | Agreed | 2026-05-31 |
| A119 | `smeldr.dev/cli`: `block` command group (T32 component 6) mirroring the 12 block MCP tools — `block node create/update/get/list/publish/archive` (Author), `block section`/`block item` `add/reorder/remove` (Editor). One `block` parent verb (architect A4). T77 table output for `node list` (pure `renderTable`/`nodeListTable`; `--json` escape). PascalCase-preserving `--field K=V` + `--fields <json>`. Pure HTTP client via `mcpCall` — no core/mcp import, no go.work. `cliVersion` 0.9.0→0.10.0 (const had lagged shipped tags). `logs` half deferred (A1). 12 tests (pure + httptest mock-MCP). Held with T32. cli v0.10.0. | Agreed | 2026-05-31 |
| A118 | `serveblocks.go` (new): `App.ServeBlocks(dir) (*BlockRenderer, error)` + `BlockRenderer.Render(ctx, pageType, pageID)` — T32 component 4 rendering engine. Assembles a page from blocks + composition edges into HTML via `templates/blocks/<type_name>.html`. Batched load (no N+1), cycle protection (visited-set + maxDepth 16), graceful degradation (unpublished/missing/dangling/malformed/missing-template/exec-error all skip+log, never page-wide). Built-in `blockFieldRegistry` (interim until c7 schema). PascalCase block-`Fields` key convention documented (AGENTS.md). ContentList deferred to c4b. 24 tests. Core engine only — route wiring is convergence. Part of held core v1.31.0. | Agreed | 2026-05-31 |
| A117 | `smeldr.dev/mcp`: block-system generic MCP tools (T32 component 3). `WithBlocks()` server option (constructs `DynamicNode` repo + `ContentEdgeStore` from `Config.DB`). Generic node tier (`node_tools.go`, Author+): `create_node`/`update_node`/`get_node`/`list_nodes`/`publish_node`/`archive_node` — addressed by ID. Composition tier (`edge_tools.go`, Editor+): `add_section`/`reorder_sections`/`remove_section`/`add_item`/`reorder_items`/`remove_item` — distinct names, shared helper, `parent_type`/`child_type` derived. Intercepted before module dispatch; blocks not browsable resources. AGENTS/REFERENCE/FEATURELIST/skill updated. Built vs local core via go.work; held for coordinated core v1.31.0 + mcp v1.14.0 release. | Agreed | 2026-05-31 |
| A116 | `blocks.go`/`edges.go` (new): block-system data foundation (T32 components 1+2). `DynamicNode` (generic block type, `Fields json.RawMessage`), `NewDynamicContentRepo(db)`, `CreateBlockTables(db)` (grouped idempotent creator for `smeldr_dynamic_content` + `smeldr_content_edges` + index; `scheduled_at` added for `SQLRepo` reuse). `ContentEdge`, `ContentEdgeStore`, `NewContentEdgeStore(db)` with `AddChild`/`Children`/`ChildrenOf`(batch)/`RemoveChild`/`Reorder`(atomic CASE); `is_shared` INTEGER↔bool scan. One edge table for page→block + collection→item (T55 D1+D2). Data layer only — MCP/render/seed later. v1.31.0. | Agreed | 2026-05-31 |
| A97 | Audit trail (T21) — `App.Audit(AuditStore)` subscribes to `AfterPublish`, `AfterSchedule`, `AfterArchive`, `AfterDelete` via signal bus; persists `AuditRecord` to SQL. `NewAuditStore(DB)`, `CreateAuditTable(DB)`. GET `/_audit` (Editor+). `forge audit list` CLI. New exported types: `AuditRecord`, `AuditFilter`, `AuditStore`. v1.22.0. | Agreed | 2026-05-16 |
| A98 | Fix data race in `notifyAfter` (`module.go`) — `snapshotItem` takes a shallow reflect copy of `item` before goroutines are spawned; both `dispatchAfter` and `afterHook` goroutine receive the snapshot. Resolves races on G26, G30, G32, G33 detected by `-race`. No exported symbols changed. v1.22.1. | Agreed | 2026-05-19 |
| A99 | Go toolchain upgrade policy — patch: follow within one sprint (govulncheck trigger); minor: within 1–2 months or before Go drops support; go.mod `go` directive tracks latest patch; `toolchain` directive used when patch bump needed but min version stays stable. | Agreed | 2026-05-19 |
| A100 | Go 1.26.3 toolchain bump — `go.mod` `go` directive `go 1.26.2` → `go 1.26.3`. Closes GO-2026-4982, GO-2026-4980, GO-2026-4971, GO-2026-4918. CI auto-picks version via `go-version-file: go.mod`. v1.22.2. | Agreed | 2026-05-19 |
| A101 | `SingleInstance()` and `Standalone()` module routing options. `SingleInstance`: serves first Published item at `GET /{prefix}`; slug URLs not registered. `Standalone`: App dispatches `GET /{slug}` top-level across all Standalone modules; `GET /{prefix}/{slug}` not registered; list at `GET /{prefix}` retained. `MCPMeta.SingleInstance bool` added; `forge-mcp` suppresses `list_{type}s` for SingleInstance modules. v1.23.0. | Agreed | 2026-05-23 |
| D32 | decisions/ file system restructure — flat role-separated system with rolling working file (`recent.md`), Non-Decisions file (`nondecisions.md`), phase2.md archived as `phase2-archive.md`. Archiving is architect-directed at ~20KB. | Active | 2026-05-17 |

### Non-Decisions — [decisions/nondecisions.md](decisions/nondecisions.md)

| # | Title | Status | Date |
|---|-------|--------|------|
| A96 | Non-Decision: sitemap ping (T39) — Forge will not provide opt-in sitemap ping. Google deprecated their endpoint in 2023; IndexNow requires API key + verification file (app-level setup). Developer pattern: `App.OnSignal(AfterPublish, ...)`. REFERENCE.md: new "Search engine indexing" section. | Agreed | 2026-05-16 |

---

> **Body text:** D1–D22, A19–A65, A87, A88–A95, A97–A101 → [`decisions/core.md`](decisions/core.md) · D25, A66, D26, A83 → [`decisions/auth.md`](decisions/auth.md) · D27, A67, A74, A75, A77 → [`decisions/content-api.md`](decisions/content-api.md) · D28, D32, A69–A72, A76, A84–A86 → [`decisions/docs.md`](decisions/docs.md) · A73, D31, A79 → [`decisions/media.md`](decisions/media.md) · D29, D30, A82 → [`decisions/nav.md`](decisions/nav.md) · A68, A78, A80, A81 → [`decisions/storage.md`](decisions/storage.md) · A102–A120 → [`decisions/phase3-archive.md`](decisions/phase3-archive.md) · A121–A125 → [`decisions/phase4-archive.md`](decisions/phase4-archive.md) · A126–A132 → [`decisions/recent.md`](decisions/recent.md) · A96 → [`decisions/nondecisions.md`](decisions/nondecisions.md) · phase2-archive.md — superseded; use topic files above
