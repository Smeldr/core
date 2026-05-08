# Forge — Roadmap

High-level roadmap for all milestones. This file tracks progress at the milestone
and step level. For sub-task detail, verification blocks, and implementation notes
see the corresponding `Milestone{N}_BACKLOG.md` file.

All architectural decisions are locked in `DECISIONS.md`.

---

## Progress

| Milestone | Description | Status |
|-----------|-------------|--------|
| M1 | Core (v0.1.0) | ✅ Done |
| M2 | App Bootstrap (v0.2.0) | ✅ Done |
| M3 | SEO & Head (v0.3.0) | ✅ Done |
| M4 | Templates & Rendering (v0.4.0) | ✅ Done |
| M5 | Social & AI (v0.5.0) | ✅ Done |
| M6 | Cookies & Compliance (v0.6.0) | ✅ Done |
| M7 | Redirects (v0.7.0) | ✅ Done |
| M8 | Scheduled publishing (v0.8.0) | ✅ Done |
| M9 | v1.0.0 stabilisation | ✅ Done |
| M10 | MCP support (v2) | ✅ Done |
| M11 | Outbound webhooks + MCP subscriptions (v1.17.0) | ✅ Done |
| M12 | Draft preview via HMAC-signed URL token (v1.18.0) | ✅ Done |

---

## Milestone 1 — Core (v0.1.0)

The minimum needed for a developer to build something real.
**Detail:** [Milestone1_BACKLOG.md](Milestone1_BACKLOG.md)

| Step | File | Status | Completed |
|------|------|--------|-----------|
| 1 | errors.go | ✅ Done | 2026-03-01 |
| 2 | roles.go | ✅ Done | 2026-03-01 |
| 3 | mcp.go | ✅ Done | 2026-03-01 |
| 4 | node.go | ✅ Done | 2026-03-01 |
| 5 | context.go | ✅ Done | 2026-03-01 |
| 6 | signals.go | ✅ Done | 2026-03-01 |
| 7 | storage.go | ✅ Done | 2026-03-01 |
| 8 | auth.go | ✅ Done | 2026-03-01 |
| 9 | middleware.go | ✅ Done | 2026-03-01 |
| 10 | module.go | ✅ Done | 2026-03-01 |
| 11 | forge.go | ⏸ Deferred — see M2 | — |
| P1 | forge-pgx | ⏸ Deferred — see M2 | — |

- [x] Step 1 — `errors.go`: forge.Error interface, sentinel errors, ValidationError, Require, WriteError
- [x] Step 2 — `roles.go`: Role type, built-in roles, level hierarchy, HasRole, IsRole, NewRole builder, Read/Write/Delete options
- [x] Step 3 — `mcp.go`: MCPOperation type, MCPRead/MCPWrite constants, MCP() no-op Option (reserved for v2)
- [x] Step 4 — `node.go`: Node struct, Status type, NewID (UUID v7), GenerateSlug, UniqueSlug, struct tag validation engine, RunValidation
- [x] Step 5 — `context.go`: User struct, GuestUser, Context interface, contextImpl, ContextFrom, NewTestContext
- [x] Step 6 — `signals.go`: Signal type and constants, On[T] generic option, dispatchBefore, dispatchAfter, debouncer
- [x] Step 7 — `storage.go`: DB interface, Query[T], QueryOne[T], Repository[T], MemoryRepo[T], ListOptions
- [x] Step 8 — `auth.go`: BearerHMAC, CookieSession (+ CSRF), BasicAuth, AnyAuth, SignToken
- [x] Step 9 — `middleware.go`: RequestLogger, Recoverer, CORS, MaxBodySize, RateLimit, SecurityHeaders, InMemoryCache, Chain
- [x] Step 10 — `module.go`: Module[T any], At/Cache/Auth/Middleware/Repo options, lifecycle, content negotiation, signals, per-module LRU
- [ ] ⏸ Step 11 — `forge.go`: Config, MustConfig, New, App (Use/Content/Handle/Run/Handler), graceful shutdown — **Deferred to Milestone 2 Step 1**
- [ ] ⏸ Step P1 — `forge-pgx` (separate module): forgepgx.Wrap(pool) thin adapter for pgx/v5 native pool — **Deferred to Milestone 2**

---

## Milestone 2 — App Bootstrap (v0.2.0)

A developer can write `forge.New(cfg)`, wire up modules, and run a real server.
**Detail:** [Milestone2_BACKLOG.md](Milestone2_BACKLOG.md)

| Step | File | Status | Completed |
|------|------|--------|-----------|
| 1 | forge.go | ✅ Done | 2026-03-02 |
| P1 | forge-pgx | ✅ Done | 2026-03-02 |

- [x] Step 1 — `forge.go`: Config, MustConfig, New, App (Use/Content/Handle/Run/Handler), Registrator, graceful shutdown
- [x] Step P1 — `forge-pgx` (separate module): forgepgx.Wrap(pool) thin adapter for pgx/v5 native pool

---

## Milestone 3 — SEO & Head (v0.3.0)

Metadata, structured data, sitemaps, and robots.txt.
**Detail:** [Milestone3_BACKLOG.md](Milestone3_BACKLOG.md)

| Step | File | Status | Completed |
|------|------|--------|-----------|
| 1 | head.go | ✅ Done | 2026-03-03 |
| 2 | schema.go | ✅ Done | 2026-03-03 |
| 3 | sitemap.go | ✅ Done | 2026-03-03 |
| 4 | robots.go | ✅ Done | 2026-03-03 |

- [x] Step 1 — `head.go`: Head and Image structs, Excerpt, URL builder, Crumbs, Headable interface, HeadFunc option
- [x] Step 2 — `schema.go`: JSON-LD structured data types (Article, Product, FAQPage, HowTo, Event, Recipe, Review, Organization, BreadcrumbList)
- [x] Step 3 — `sitemap.go`: per-module fragment sitemaps, sitemap index, SitemapConfig option, SitemapStore, debounce-driven regeneration
- [x] Step 4 — `robots.go`: auto-generated robots.txt, RobotsConfig, AskFirst AI crawler policy, sitemap URL append

---

## Milestone 4 — Templates & Rendering (v0.4.0)

HTML rendering, template helpers, content negotiation.
**Detail:** [Milestone4_BACKLOG.md](Milestone4_BACKLOG.md)

| Step | File | Status | Completed |
|------|------|--------|-----------|
| 1 | templatedata.go | ✅ Done | 2026-03-05 |
| 2 | templates.go | ✅ Done | 2026-03-05 |
| 3 | templatehelpers.go | ✅ Done | 2026-03-05 |
| 4 | integration_test.go | ✅ Done | 2026-03-05 |
| 5 | integration_full_test.go | ✅ Done | 2026-03-05 |

- [x] Step 1 — `templatedata.go`: TemplateData[T] struct, NewTemplateData constructor
- [x] Step 2 — `templates.go`: Templates/TemplatesOptional options, forge:head partial, startup parse, HTML render path, error pages
- [x] Step 3 — `templatehelpers.go`: forge_meta, forge_date, forge_markdown, forge_excerpt, forge_csrf_token, forge_llms_entries
- [x] Step 4 — `integration_test.go`: full HTML render cycle, forge:head, error pages, CSRF
- [x] Step 5 — `integration_full_test.go`: cross-milestone suite (M1–M4)

---

## Milestone 5 — Social & AI (v0.5.0)

Open Graph, Twitter Cards, llms.txt, AIDoc, RSS feeds.
**Detail:** [Milestone5_BACKLOG.md](Milestone5_BACKLOG.md)

| Step | File | Status | Completed |
|------|------|--------|-----------|
| 1 | social.go | ✅ Done | 2026-03-06 |
| 2 | ai.go | ✅ Done | 2026-03-06 |
| 3 | feed.go | ✅ Done | 2026-03-06 |
| 4 | integration_full_test.go | ✅ Done | 2026-03-06 |

- [x] Step 1 — `social.go`: Social option, OpenGraph, TwitterCard, card types, SocialOverrides
- [x] Step 2 — `ai.go`: AIIndex option, LLMsTxt, LLMsTxtFull, AIDoc format, AIDocSummary and Markdownable interfaces
- [x] Step 3 — `feed.go`: opt-in RSS 2.0 per module, FeedConfig, FeedDisabled, aggregate feed
- [x] Step 4 — `integration_full_test.go`: cross-milestone groups G9–G12 + README badge updates

---

## Milestone 6 — Cookies & Compliance (v0.6.0)

Typed cookie declarations, category-enforced consent, compliance manifest.
**Detail:** [Milestone6_BACKLOG.md](Milestone6_BACKLOG.md)

| Step | File | Status | Completed |
|------|------|--------|-----------|
| 1 | cookies.go | ✅ Done | 2026-03-07 |
| 2 | cookiemanifest.go | ✅ Done | 2026-03-07 |
| 3 | integration_full_test.go | ✅ Done | 2026-03-07 |

- [x] Step 1 — `cookies.go`: CookieCategory, consent enforcement, SetCookie, GrantConsent, RevokeConsent
- [x] Step 2 — `cookiemanifest.go`: cookieManifest JSON type, ManifestAuth option, App.Cookies()
- [x] Step 3 — `integration_full_test.go`: cross-milestone groups G13–G15 + README badge

---

## Milestone 7 — Redirects (v0.7.0)

Production `SQLRepo[T]`, automatic redirect tracking, 410 Gone, chain collapse.
**Detail:** [Milestone7_BACKLOG.md](Milestone7_BACKLOG.md)

| Step | File | Status | Completed |
|------|------|--------|-----------|
| 1 | storage.go | ✅ Done | 2026-03-07 |
| 2 | redirects.go | ✅ Done | 2026-03-07 |
| 3 | redirectmanifest.go | ✅ Done | 2026-03-07 |
| 4 | integration_full_test.go | ✅ Done | 2026-03-07 |

- [x] Step 1 — `storage.go`: SQLRepo[T] production repository
- [x] Step 2 — `redirects.go`: RedirectCode, RedirectEntry, RedirectStore, App.Redirect()
- [x] Step 3 — `redirectmanifest.go`: /.well-known/redirects.json endpoint
- [x] Step 4 — `integration_full_test.go`: cross-milestone groups G16–G18 + README badge updates

---

## Milestone 8 — Scheduled publishing (v0.8.0)

Adaptive ticker, Scheduled→Published transition, AfterPublish signal.
**Detail:** [Milestone8_BACKLOG.md](Milestone8_BACKLOG.md)

| Step | File | Status | Completed |
|------|------|--------|-----------|
| 1 | scheduler.go | ✅ Done | 2026-03-07 |
| 2 | integration_full_test.go | ✅ Done | 2026-03-07 |

- [x] Step 1 — `scheduler.go`: adaptive ticker, Scheduled→Published, AfterPublish signal
- [x] Step 2 — `integration_full_test.go`: cross-milestone groups G19–G20 + README badge update

---

## Milestone 9 — v1.0.0 stabilisation

Test coverage, benchmarks, godoc audit, example apps.
**Detail:** [Milestone9_BACKLOG.md](Milestone9_BACKLOG.md)

| Step | File | Status | Completed |
|------|------|--------|-----------|
| 1 | coverage audit | ✅ Done | 2026-03-08 |
| 2 | benchmarks_test.go | ✅ Done | 2026-03-08 |
| 3 | forge.go + storage.go (godoc) | ✅ Done | 2026-03-08 |
| 4 | example/blog/ | ✅ Done | 2026-03-08 |
| 5 | example/docs/ | ✅ Done | 2026-03-08 |
| 6 | example/api/ | ✅ Done | 2026-03-08 |
| 7 | CHANGELOG.md + integration_full_test.go G21 | ✅ Done | 2026-03-08 |
| 8 | example_test.go | ✅ Done | 2026-03-08 |

---

## Milestone 10 — MCP support (v2)

Implementation of Decision 19. Syntax reserved in v1 via mcp.go.
**Detail:** [Milestone10_BACKLOG.md](Milestone10_BACKLOG.md)

| Step | File | Status | Completed |
|------|------|--------|-----------|
| 1 | forge-mcp/mcp.go | ✅ Done | 2026-03-16 |
| 2 | forge-mcp/resource.go | ✅ Done | 2026-03-16 |
| 3 | forge-mcp/tool.go | ✅ Done | 2026-03-17 |
| 4 | forge-mcp/transport.go | ✅ Done | 2026-03-16 |
| 5 | forge-mcp/README.md | ✅ Done | 2026-03-17 |

- [x] Step 1 — `forge-mcp` module scaffold: MCPServer, resource schema auto-generation
- [x] Step 2 — MCPRead: expose Published content as readable MCP resources
- [x] Step 3 — MCPWrite: expose Create/Update/Delete/Publish as MCP tools
- [x] Step 4 — Transport: stdio (local AI tools) + SSE (remote, authenticated)
- [x] Step 5 — Documentation: connecting Claude/Cursor/Copilot to a Forge app

---

## Milestone 11 — Outbound webhooks + MCP subscriptions (v1.17.0)

Signal-driven HTTP delivery to external endpoints; encrypted secrets; retries;
circuit breaker; five Admin MCP tools and five forge-cli subcommands; MCP
resource subscriptions for real-time AI agent notification.
**Detail:** see git history — `Milestone11_BACKLOG.md` removed at close.

| Step | File | Status | Completed |
|------|------|--------|----------|
| A87 | signals.go | ✅ Done | 2026-05-06 |
| 1 | webhook.go | ✅ Done | 2026-05-06 |
| 2 | outbound.go | ✅ Done | 2026-05-06 |
| A89 | module.go | ✅ Done | 2026-05-06 |
| A88 | forge.go | ✅ Done | 2026-05-06 |
| 3 | forge-mcp/webhook_tools.go | ✅ Done | 2026-05-06 |
| 4 | forge-cli/webhook.go | ✅ Done | 2026-05-06 |
| 5 | forge-mcp/subscription.go | ✅ Done | 2026-05-06 |
| 6 | integration_full_test.go | ✅ Done | 2026-05-08 |

- [x] A87 — `signals.go`: AfterSchedule Signal constant
- [x] Step 1 — `webhook.go`: WebhookStore, SSRF validation, payload building, AES-256-GCM secret encryption
- [x] Step 2 — `outbound.go`: workerPool, retry, circuit breaker, HMAC signing, HTTP delivery
- [x] A89 — `module.go`: contentTypeName field, afterHook, notifyAfter helper, AfterSchedule dispatch
- [x] A88 — `forge.go`: App.Webhooks(), App.WebhookPool(), injectWebhookHooks, Run wiring
- [x] Step 3 — `forge-mcp/webhook_tools.go`: 5 Admin webhook MCP tools
- [x] Step 4 — `forge-cli/webhook.go`: 5 webhook subcommands (CLI parity)
- [x] Step 5 — `forge-mcp/subscription.go`: MCP resource subscriptions + transport handlers
- [x] Step 6 — `integration_full_test.go`: cross-milestone groups G24–G30

---

## Milestone 12 — Draft preview (v1.18.0)

Stateless HMAC-SHA256 signed URL token granting read access to Draft and
Scheduled content without login. Prefix-bound — tokens cannot replay across
modules. Archived items are never previewable.
**Detail:** see git history — no separate backlog file.

| Step | File | Status | Completed |
|------|------|--------|-----------|
| A92 | auth.go, forge.go, module.go | ✅ Done | 2026-05-08 |
| 1 | forge-mcp/preview_tools.go | ✅ Done | 2026-05-08 |
| 2 | forge-cli/preview.go | ✅ Done | 2026-05-08 |

- [x] A92 — `auth.go`: `encodePreviewToken` / `decodePreviewToken` (prefix+slug+expiry, HMAC-SHA256)
- [x] A92 — `forge.go`: `Config.PreviewTokenExpiry`, `App.GeneratePreviewToken(prefix, slug)`, `App.BaseURL()`
- [x] A92 — `module.go`: `secret` field, `setSecret`, preview bypass in `showHandler` (Draft/Scheduled only — Archived → 404)
- [x] Step 1 — `forge-mcp/preview_tools.go`: `create_preview_url` Admin MCP tool; always in `tools/list`
- [x] Step 2 — `forge-cli/preview.go`: `forge-cli preview <prefix> <slug>` subcommand

---

## Phase 2 — Production foundation

See [VISION.md](VISION.md) for context. Steps are ordered by dependency
and practical value.

- **Health endpoint + error reporter interface** — `GET /_health` ✅ Done (Amendment A42); `forge.ErrorReporter` interface still pending
- **Shared template partials** — add partial directory support or `{{template "include"}}` mechanism
- **`forge:head` public helper** — expose as `forge.HeadPartial(head Head) template.HTML` or equivalent
- **`forge.New` MustConfig enforcement** — make `New` call `MustConfig` internally
- **`forge.AppSchema{}`** — app-level structured data via `app.SEO()`
- **`forge.OGDefaults{}`** — app-level fallback for `og:image`, `twitter:site`, `twitter:creator`
- **AI endpoint performance hardening** — pre-computation, caching, rate limiting. See NOTES.md [N1].

---

## v2+ Roadmap (not yet planned)

These topics require a new Tier 1 decision round before planning begins.

- **i18n** — locale-aware URLs, hreflang tags, per-locale content
- **Forge AI** — content assistant built on MCP + AIDoc + llms.txt
- **Admin UI** — `forge-studio` as a separate package
- **Search** — SQLite FTS5 integration, `forge.Searchable` interface
- **Webhooks** — outbound HTTP on content events ✅ Done (Milestone 11)
- **Multi-tenancy** — multiple sites from one instance
- **GraphQL** — auto-generated schema from content types
- **Edge/CDN** — surrogate keys, automatic CDN purge
- **Image resizing** — `forge-images` as a separate package
- **Forge Cloud** — managed hosting, dual-license introduction
- **Database migrations** — `forge migrate` CLI or migration interface
- **Publish-time validation** — `forge:"required_when=published"` tag or `OnPublish` interface
- **Token revocation** — per-token revocation list or short default TTL + refresh

---

## Known issues (unfiled)

- [ ] **Health endpoint HTTPS redirect** — `forge.Config{HTTPS: true}` causes `GET /_health` to return 301. (Relates to Amendment A42)
- [ ] **SQLite reserved keyword guard** — `SQLRepo` generates unquoted column names; reserved keywords cause SQL syntax errors at runtime.
- [ ] **forge-admin missing** — no web UI for content management; MVP forge-admin is Tier 4 on the critical path to Forge Cloud.
- [ ] **forge:head emits relative og:url** — fixed in forge-site via forge.AbsURL() (A56 + S34). Proper fix is forge:head using AbsURL internally.
