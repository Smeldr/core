# Forge — Architecture

This document describes the internal structure of Forge: how the packages
are organised, how a request flows through the system, which interfaces
are stable API contracts, and the dependency rules between packages.

Read DECISIONS.md first. This document explains *how* — DECISIONS.md explains *why*.

---

## Changelog

| Date | Change |
|------|--------|
| 2026-03-01 | Initial architecture document drafted (Milestone 1 planning) |
| 2026-03-01 | Updated to reflect Milestone 1 completion: corrected request lifecycle order, added `CacheStore`, `CSRF`, `TrustedProxy`, updated `SignToken` signature, added `ListOptions.Status`, fixed `Markdownable` location to `module.go`, marked future-milestone files as planned |
| 2026-03-02 | Milestone renumbering: M2 split into App Bootstrap (M2) and SEO & Head (M3); all subsequent milestones shifted +1 |
| 2026-03-02 | Milestone 2 Step 1: `forge.go` implemented — `Config`, `MustConfig`, `New`, `App` (`Use`/`Content`/`Handle`/`Run`/`Handler`), `Registrator` interface, graceful shutdown |
| 2026-03-02 | Milestone 2 Step P1: `forge-pgx` module implemented — `Wrap(pool)` native pgx adapter satisfying `forge.DB` |
| 2026-03-03 | Milestone 3 Step 1: `head.go` implemented — `Head`, `Image`, `Breadcrumb`, `Alternate`, `Headable`, `HeadFunc`, `Excerpt`, `URL`, `Crumbs`; `Module[T].headFunc` field added (Amendment A1) |
| 2026-03-03 | Milestone 3 Step 2: `schema.go` implemented — `SchemaFor`, 8 JSON-LD rich result types (Article, Product, FAQPage, HowTo, Event, Recipe, Review, Organization), BreadcrumbList, 6 provider interfaces (FAQProvider, HowToProvider, EventProvider, RecipeProvider, ReviewProvider, OrganizationProvider) |
| 2026-03-03 | Milestone 3 Step 3: `sitemap.go` implemented — `SitemapConfig`, `ChangeFreq`, `SitemapNode`, `SitemapPrioritiser`, `SitemapEntry`, `SitemapStore`, `WriteSitemapFragment`, `SitemapEntries`, `WriteSitemapIndex`; Amendments A2 (node.go getters), A3 (Module sitemap wiring), A4 (App sitemap store + Handler guard) |
| 2026-03-03 | Milestone 3 Step 4: `robots.go` implemented — `CrawlerPolicy`, `Allow`/`Disallow`/`AskFirst`, `RobotsConfig`, `RobotsTxt`, `RobotsTxtHandler`; Amendment A5: `SEOOption`, `seoState`, `App.SEO()`, `robotsTxtRegistered` guard in `forge.go` |
| 2026-03-05 | Milestone 4 Step 1: `templatedata.go` implemented — `TemplateData[T]`, `NewTemplateData` constructor; `SiteName` sourced from `Config.BaseURL` hostname |
| 2026-03-05 | Milestone 4 Step 2: `templates.go` implemented — `templateParser` interface, `Templates`/`TemplatesOptional` options, `forgeHeadTmpl` const, `parseTemplates()`/`renderListHTML`/`renderShowHTML` on `Module[T]`, `bindErrorTemplates`; Amendments A6 (`module.go` template fields + HTML render path), A7 (`errors.go` `errorTemplateLookup`), A8 (`forge.go` `templateModules` + startup parse wiring) |
| 2026-03-05 | Milestone 4 Step 3: `templatehelpers.go` implemented — `forgeMeta`, `forgeDate`, `forgeMarkdown` (stdlib-only), `forgeExcerpt`, `forgeCSRFToken`, `forgeLLMSEntries` (stub), `TemplateFuncMap()`; Amendment A9 (`templates.go` `parseOneTemplate` now calls `.Funcs(TemplateFuncMap())`) |
| 2026-03-05 | Milestone 4 Step 4: `integration_test.go` implemented — 15 cross-component integration tests covering HTML render cycle, forge:head correctness, error pages (custom + fallback), CSRF token round-trip, App-level SEO/sitemap routing, and TemplateData field propagation |
| 2026-03-05 | Milestone 4 Step 5: `integration_full_test.go` implemented — 19 cross-milestone integration tests (M1–M4): multi-module routing, global middleware order, role-gated access (HasRole + inline middleware), AfterCreate/AfterDelete/cross-module signal isolation, content negotiation across two module types, forge_meta/forge_markdown/BreadcrumbList through render, sitemap URL in robots.txt, error template first-match and fallthrough, TemplateData siteName and request URL |
| 2026-03-06 | Milestone 5 Step 1: `social.go` implemented — `SocialFeature`, `OpenGraph`, `TwitterCard`, `Social()` option; Amendment A9 (`head.go`: `Tags []string`, `TwitterCardType`, `TwitterMeta`, `SocialOverrides`, `Head.Social` field); Amendment A10 (`templates.go` `forgeHeadTmpl` extended — full OG + Twitter block, `forge_rfc3339` added to `templatehelpers.go` and `TemplateFuncMap()`, Module[T].social field + case in `module.go`) |
| 2026-03-06 | Milestone 5 Step 2: `ai.go` implemented — `Markdownable` (A11: migrated from `module.go`), `AIDocSummary`, `AIFeature`, `LLMsTxt`/`LLMsTxtFull`/`AIDoc` constants, `AIIndex()` option, `WithoutID()` option, `LLMsEntry`, `LLMsTemplateData`, `LLMsStore`, `NewLLMsStore`, `extractNode`, `renderAIDoc`; `forgeLLMSEntries(data any)` wired in `templatehelpers.go` (A12); `LLMsStore` wiring in `forge.go` Content+Handler (A13); README one-liner added (A14); AIDoc URL uses `/{prefix}/{slug}/aidoc` — Go’s net/http.ServeMux does not support partial wildcard segments, so `/{slug}.aidoc` is not a valid pattern (A15: DECISIONS.md updated) || 2026-03-06 | Milestone 5 Step 3: `feed.go` implemented — `FeedConfig`, `Feed()` option (opt-in, Amendment A16: Decision 13 updated), `FeedDisabled()` option, `rssItem`/`rssChannel`/`rssRoot` XML structs, `FeedStore`, `NewFeedStore`, `buildRSSItem`, `capitalisePrefixTitle`, `guessMIMEType`, `writeRSSFeed`; `ModuleHandler` serves `/{prefix}/feed.xml`, `IndexHandler` serves `/feed.xml` aggregate (all Published items, reverse-chronological); `feedCfg`/`feedStore`/`regenerateFeed`/`setFeedStore` added to `module.go`; `feedStore`/`feedIndexRegistered` added to `forge.go` |
| 2026-03-06 | Milestone 5 Step 4: `integration_full_test.go` extended — G9–G12 cross-milestone groups appended: G9 (Social + SitemapConfig M3): OG/Twitter tags in forge:head, Draft → 404; G10 (AIIndex + M4 content negotiation): /llms.txt Published/Draft filter, /posts/{slug}/aidoc 200/404, Accept:text/markdown alongside AIDoc; G11 (Feed + M1 AfterPublish signal): /posts/feed.xml RSS 2.0, Draft excluded, AfterPublish fires within 500ms; G12 (Full M5 stack): Social+AIIndex+Feed+SitemapConfig+HeadFunc+Templates — OG/Twitter, /llms.txt, /aidoc, /feed.xml all verified. README.md: AI indexing and Social sharing badges updated from 🔲 Coming in Milestone 5 → ✅ Available. Milestone 5 complete. |
| 2026-03-06 | Amendment A17: `compressIfAccepted(w, r, body, contentType)` helper added to `ai.go`; gzip applied directly at AI endpoint handlers — `CompactHandler`, `FullHandler`, `renderAIDoc` (now takes `r *http.Request`); 1400-byte threshold; `Vary: Accept-Encoding` always set. Supersedes Decision 13 Amendment A clause 3. Tests: `TestCompressIfAccepted_gzip`, `TestCompressIfAccepted_smallBody`, `TestCompressIfAccepted_noAcceptEncoding`, `TestLLMsTxt_gzip`, `TestAIDoc_gzip`. |
| 2026-03-07 | Milestone 6 Step 1: `cookies.go` implemented — `CookieCategory` (`Necessary`/`Preferences`/`Analytics`/`Marketing`), `Cookie` struct, `SetCookie`, `SetCookieIfConsented`, `ReadCookie`, `ClearCookie`, `ConsentFor`, `GrantConsent`, `RevokeConsent`; `forge_consent` Necessary cookie stores consent state; Decision 5 enforcement: `SetCookie` panics on non-Necessary, `SetCookieIfConsented` panics on Necessary. |
| 2026-03-07 | Milestone 6 Step 2: `cookiemanifest.go` implemented — `cookieManifest`/`cookieManifestEntry` JSON types, `buildManifest`, `sameSiteName`, `ManifestAuth` option, `newCookieManifestHandler`; Amendment A18: `App.Cookies()`, `App.CookiesManifestAuth()`, `cookieDecls`/`cookieManifestOpts` fields added to `forge.go`; `GET /.well-known/cookies.json` mounted lazily in `App.Handler()`. |
| 2026-03-07 | Milestone 6 Step 3: `integration_full_test.go` extended — G13–G15 cross-milestone groups appended: G13 (M6 consent enforcement, Decision 5): SetCookie/ConsentFor/SetCookieIfConsented/GrantConsent/RevokeConsent; G14 (M6 + M2 handler pattern): consent lifecycle wired through an HTTP handler, ClearCookie expiry, Necessary always-true; G15 (M6 + M2 App + M1 BearerHMAC): manifest mounted/sorted/not-mounted-when-empty, authGuard 401/200. README.md: Cookies & Compliance badge updated from 🔲 Coming in Milestone 6 → ✅ Available. Milestone 6 complete. |
| 2026-03-07 | Milestone 7 Step 1: `storage.go` extended — `SQLRepo[T]` production `Repository[T]` backed by `forge.DB`; `Table()` `SQLRepoOption`; `camelToSnake()` + plural table-name derivation; `FindByID`/`FindBySlug` delegate to `QueryOne`; `FindAll` with status IN, ORDER BY, LIMIT/OFFSET; `Save` upsert (ON CONFLICT); `Delete` returns `ErrNotFound` when RowsAffected==0. 9 new `TestSQLRepo_*` tests + extended fake driver. Amendment A19. |
| 2026-03-07 | Milestone 7 Step 2: `redirects.go` implemented — `RedirectCode` (`Permanent`/`Gone`), `RedirectEntry` (+`IsPrefix`), `From` type, `Redirects()` module option, `RedirectStore` (exact map + prefix slice sorted longest-first, chain collapse max depth 10, `Get`/`Add`/`All`/`Len`), DB persistence (`Load`/`Save`/`Remove`), `handler()` fallback; `forge.go` Amendment A20: `redirectStore *RedirectStore`, `redirectFallbackReg`, `New()` init, `Content()` extracts `redirectsOption`, `Handler()` mounts `"/"` fallback, `App.Redirect()`, `App.RedirectStore()`. 13 new `TestRedirectStore_*`/`TestApp_Redirect_*` tests. |
| 2026-03-07 | Milestone 7 Step 3: `redirectmanifest.go` implemented — `redirectManifestEntry`/`redirectManifest` JSON types, `buildRedirectManifest` (delegates to `store.All()` for sorted entries), `newRedirectManifestHandler` (serialises per-request from live store, reuses `manifestAuthOption`, `Cache-Control: no-store`); `forge.go` Amendment A21: `redirectManifestReg bool`, `GET /.well-known/redirects.json` always mounted in `Handler()`. 8 new `TestRedirectManifest_*` tests. |
| 2026-03-07 | Milestone 7 Step 4: `integration_full_test.go` extended — G16–G18 cross-milestone groups appended: G16 (M7 Decision 17): 301/410/404 enforcement + forward chain collapse; G17 (M7 + M2): prefix rewrite via `Redirects(From)`, exact-beats-prefix; G18 (M7 + M6 + M1): `SQLRepo[T]` satisfies `Repository[T]` compile check, redirect manifest always mounted, entries reflect `app.Redirect()` calls, `App.RedirectManifestAuth()` (Amendment A22) 401/200. `forge.go` Amendment A22: `redirectManifestOpts []Option` field + `App.RedirectManifestAuth(auth AuthFunc)` method. README: Redirects badge → ✅ Available; SQLRepo production repository section added. Milestone 7 complete. |
| 2026-03-07 | Milestone 8 Step 1: `scheduler.go` implemented — `schedulableModule` interface, `Scheduler` struct, `newScheduler`, `Start(ctx)`, `Wait()`, `tick()` (aggregates min next across modules), `run(ctx)` adaptive timer with 60s fallback, `nextDur` helper. Amendment A23 (`node.go`): `db` struct tags added to `PublishedAt`, `ScheduledAt`, `CreatedAt`, `UpdatedAt` fixing `SQLRepo` column mapping. Amendment A24 (`context.go`): `NewBackgroundContext(siteName string) Context` for long-lived goroutine use. Amendment A25 (`module.go`): `setNodeStatus`/`setNodeTime`/`setNodeTimePtr` reflection helpers + `Module[T].processScheduled` — queries Scheduled items, publishes overdue, fires `AfterPublish`, triggers sitemap/feed debounce. Amendment A26 (`forge.go`): `schedulerModules []schedulableModule` field, `Content()` appends modules, `Run()` starts scheduler before `ListenAndServe` and stops it (via `defer`) after `srv.Shutdown`. `scheduler_test.go`: 7 tests covering overdue publish, skip-not-yet-due, AfterPublish signal, mixed items, adaptive next, start/stop lifecycle, and `NewBackgroundContext`. |
| 2026-03-07 | Milestone 8 Step 2: `integration_full_test.go` extended — G19–20 cross-milestone groups appended: G19 (M8 + M1) `TestFull_scheduler_publishesOverdue` — direct `processScheduled` call on a `MemoryRepo`-backed module, verifies past-due item → Published (Status, PublishedAt, ScheduledAt nil), future item unchanged, returned `next` == future ScheduledAt, AfterPublish signal fires once within 500ms; G20 (M8 + M2 + M3) `TestFull_scheduler_appWiring` — `App.Content()` wires module into `schedulerModules` (A26), `newScheduler` + `tick()` publishes overdue item, future item stays Scheduled, adaptive `next` returned correctly. README: Scheduled publishing badge → ✅ Available. Milestone 8 complete. |
| 2026-03-08 | Amendment A28: `resolveHead(ctx Context, item T) Head` added to `Module[T]` in `module.go` — priority: HeadFunc > Headable > zero Head. Four duplicated `headFunc` blocks replaced in `regenerateFeed`, `regenerateAI`, `aiDocHandler` (`module.go`), and `renderShowHTML` (`templates.go`). `Headable` godoc in `head.go` updated — `Module[T]` now calls `Head()` automatically in HTML rendering, sitemaps, RSS feeds, and AI endpoints without requiring an explicit `HeadFunc` option. |
| 2026-03-11 | Error handling audit and hardening (Amendments A29–A32, v1.0.1): `ERROR_HANDLING.md` created as authoritative strategy document. New sentinels `ErrBadRequest`, `ErrNotAcceptable`, `ErrRequestTooLarge`, `ErrTooManyRequests` added to `errors.go`. `errorTemplateLookup` protected with `sync.RWMutex` via `setErrorTemplateLookup`/`runErrorTemplateLookup` helpers. Direct type assertion in `respond()` replaced with `errors.As`. `writeContent` in `module.go` receives `r *http.Request`; 406 and 400/413 error paths use `WriteError`. `renderListHTML`/`renderShowHTML` in `templates.go` use `WriteError` for nil-template 406. `RateLimit` in `middleware.go` uses `WriteError` for 429. `Recoverer` stack buffer increased from 4096 to 32 KB. All `http.Error` bypass sites eliminated. Four missing test cases added to `errors_test.go`. `ARCHITECTURE.md` — "Error handling pipeline" section added. `copilot-instructions.md` — error handling rule added to non-negotiable rules. |
| 2026-03-08 | Milestone 9: v1.0.0 stabilisation complete. Coverage raised to 87.5% (target ≥85%). `benchmarks_test.go`: 17 benchmarks across M1–M8 hot paths (see BENCHMARKS.md). Godoc pass on `type App` + all `App.*` methods (A18–A26) and `SQLRepo[T]` parity. `example/blog/`, `example/docs/`, `example/api/` standalone runnable examples added (`go.work` updated). Amendment A27: `Authenticate(auth AuthFunc) func(http.Handler) http.Handler` added to `middleware.go` — populates `Context.User()` via request context; pairs with `BearerHMAC`/`CookieSession`/`AnyAuth`. `CHANGELOG.md`: Keep a Changelog format, v0.1.0–v1.0.0, API stability promise + version policy. `integration_full_test.go`: G21 (M1+M2+M3+M5+M7+M8) full v1.0.0 smoke test — scheduler promotes overdue item, aggregate sitemap + feed + AI index + redirects all verified. Known gap: `App.Content()` calls `r.Register(mux)` before `setFeedStore`/`setSitemap`, so per-module `/posts/feed.xml` and `/posts/sitemap.xml` are not registered via the App path (Amendment A28 candidate); per-module feed tested directly in G11/G12. Milestone 9 complete — v1.0.0 released. |

| 2026-03-12 | Hardening sweep (Amendments A37–A41, v1.0.5): A37 — all `http.NotFound`/`http.Error` bypasses replaced with `WriteError(w, r, sentinel)`. A38 — `auth.go` `encodeToken` returns `ErrInternal` instead of raw `fmt.Errorf`. A39 — `Module[T]` goroutine lifecycle: `stopCh` field + `Stop()` method; cache sweep exits on `stopCh`; `debouncer.Stop()` added; `stoppable` interface + `App.stoppableModules`; `App.Run()` calls `Stop()` on all modules after `srv.Shutdown`. A40 — `FeedDisabled()` → `DisableFeed()`; `forgeLLMSEntries` → `forgeLLMsEntries`. A41 — debounce callback used stashed request context (cancelled before 2-second delay fires); replaced with `NewBackgroundContext(m.siteName)` at fire time; `debounceMu`/`debounceCtx` fields removed; `triggerSitemap(ctx)` → `triggerRebuild()`. |
| 2026-03-12 | Amendment A42 (`forge.go`): `Config.Version string` field added immediately after `Secret []byte`; `App.Health()` method mounts `GET /_health` returning `{"status":"ok"}` or `{"status":"ok","version":"X.Y.Z"}` (200, `application/json`). Explicit opt-in — not auto-mounted. Three tests: `TestApp_health_ok`, `TestApp_health_version`, `TestApp_health_notMounted`. |
| 2026-03-14 | Amendment A43: `NewSQLRepo` godoc and README updated — explicit pointer-type guidance added; wiring example shows `NewSQLRepo[*Post]` + `NewModule((*Post)(nil), ...)` together. |
| 2026-03-16 | Milestone 10 Step 1: `forge-mcp/mcp.go` + Amendment A49 — `forge.MCPModule` interface added to `mcp.go`; `Module[T]` implements it in `module.go`; `App.MCPModules()` added to `forge.go`; `forge-mcp` scaffold (`go.mod`, `Server`, `New`, JSON-RPC types, `handle`, `handleInitialize`, `snakeCase`, `hasMCPOp`, `slugOf`, `mcpToolDefs`, `inputSchema`, `inputSchemaUpdate`). |
| 2026-03-16 | Milestone 10 Step 2: `forge-mcp/resource.go` — read path: `handleResourceMethod`, `handleResourcesList`, `handleResourcesTemplatesList`, `handleResourcesRead`, `parseResourceURI`; `mcpResource`/`resourceContent`/`resourceTemplate` wire types; Published-only lifecycle enforcement. `handle` default case delegates to `handleResourceMethod`. |
| 2026-03-17 | Milestone 10 Step 3: `forge-mcp/tool.go` — write path: `handleToolMethod`, `handleToolsList`, `handleToolsCall` dispatcher (create/update/publish/schedule/archive/delete); `toolName`, `parseToolName`, `moduleForType`, `authorise`, `errorFor`, `stringArg` helpers; Author-level role enforcement; Flag H idempotency on publish; Flag F delete response `{"deleted":true,"slug":...}`; godoc NOTE on zero-value limitation (Flag G). `handle` default case now delegates to `handleToolMethod` before `handleResourceMethod`. |
| 2026-03-16 | Milestone 10 Step 4: `forge-mcp/transport.go` + Amendment A50 — `ServeStdio(ctx, in, out)` with goroutine-based scanner and 1 MiB `bufio.Scanner` buffer limit; `Handler()` returning ServeMux with `GET /mcp` (SSE keepalive) and `POST /mcp/message` (HTTP 401 auth boundary + `MaxBytesReader` 1 MiB body limit + JSON-RPC response); A50 additions: `forge.VerifyBearerToken(r, secret)` in `auth.go`; `App.Secret()` accessor in `forge.go`; `forge.NewContextWithUser(user)` production-safe background context constructor in `context.go`; `Server.secret []byte` + `New(app, opts...)` auto-inherit + `WithSecret` option + mismatch `log.Printf` warning in `forge-mcp/mcp.go`. 10 transport tests + 3 `TestVerifyBearerToken` sub-tests added. |
| 2026-03-17 | Amendment A51 (`templates.go`): `forgeHeadTmpl` now emits `twitter:card = summary_large_image` when `Head.Type` is `"Article"` or `"Product"`, regardless of whether `Head.Image.URL` is set; `Head.Social.Twitter.Card` explicit override takes priority. Five new sub-tests in `TestTemplates_twitterCard`. Shipped in v1.1.1. |
| 2026-03-17 | Amendment A52 (`module.go`): `mcpGoTypeStr` returns `"array"` for `reflect.Slice` kinds; new `coerceSliceFields` helper splits comma-separated string values for `[]string` fields before `MCPCreate`/`MCPUpdate` round-trip. (`forge-mcp/mcp.go`): `inputSchema` and `inputSchemaUpdate` emit `{"type":"array","items":{"type":"string"}}` for array fields and suppress `minLength`/`maxLength`/`enum` constraints. Shipped in forge v1.1.2 / forge-mcp v1.0.1. |
| 2026-03-18 | Amendment A53 (`module.go`): `negotiate()` now returns `"text/html"` when `Accept` is absent or `"*/*"` and the module has templates configured; previously returned `"application/json"` unconditionally, causing crawlers to receive JSON and miss structured data in `<head>`. API-only modules (no templates) are unaffected. Shipped in v1.1.3. |
| 2026-03-18 | Amendment A54 (`forge-mcp/mcp.go`, `forge-mcp/tool.go`): admin read tools added to every MCPWrite module — `mcpAdminReadToolDefs` generates `list_{type}s` and `get_{type}` tools; `authoriseEditor` enforces Editor or Admin role; `moduleForAdminList` resolves the plural typeSnake for list tool dispatch; `handleToolsList` and `handleToolsCall` wired. Shipped in forge-mcp v1.0.2. |
| 2026-03-18 | Amendment A55 (`forge-mcp/mcp.go`, `forge-mcp/tool.go`): `delete_{type}` moved from Author-level `mcpToolDefs` to Editor-level `mcpAdminReadToolDefs`; `mcpAdminReadToolDefs` now generates 3 tools per MCPWrite module (list, get, delete); `delete` dispatch case calls `authoriseEditor` before executing. Shipped in forge-mcp v1.0.5. |
| 2026-03-20 | Amendment A57 (`storage.go`): `quoteIdent()` helper added; applied to every generated column reference in `SQLRepo.Save`, `FindAll`, `FindByID`, `FindBySlug`, and `Delete`; prevents SQL syntax errors when `db` struct tags use reserved keywords (e.g. `db:"order"`). Shipped in v1.1.5. |
| 2026-03-20 | Amendment A58 (`forge.go`): `forgeVersions()` reads `runtime/debug.ReadBuildInfo()` at `Health()` mount time and `Run()` startup; `Health()` now includes `"forge"` and companion-module version keys in the JSON response instead of the removed `"version"` key; startup log line emitted to stderr before `ListenAndServe`. `Config.Version` retained for application use only. Shipped in v1.1.6. |
| 2026-03-20 | Amendment A59 (`forge.go`): `httpsRedirect()` exempts `/_health` from the HTTPS redirect — plain-HTTP requests to `/_health` pass through to `next` immediately, before the TLS / `X-Forwarded-Proto` check; reverse-proxy health checks no longer receive a `301`. Shipped in v1.1.7. |
| 2026-04-02 | Amendment A62 (`forge.go`, `templates.go`, `module.go`): `App.Partials(dir) *App` stores a partials directory; `loadPartials(dir)` reads `*.html` files alphabetically; `Module[T].setPartials([]string)` stores partial sources; `parseOneTemplate` now accepts `partials []string` and registers each into the template set after `forge:head`; `App.MustParseTemplate(path) *template.Template` loads a single template with FuncMap + forge:head + partials, panics on error. Shipped in v1.2.0. |
| 2026-04-03 | Amendment A63 (`head.go`, `templates.go`, `templatedata.go`, `forge.go`, `module.go`): `HeadAssets`, `FaviconLink`, `ScriptTag` new exported types in `head.go`; `HeadAssets` implements `SEOOption` via `applySEO(*seoState)`; `seoState.headAssets` field added to `forge.go`; `App.Handler()` interface assertion updated to 3-arg `setSEODefaults(*OGDefaults, *AppSchema, *HeadAssets)`; `Module[T].headAssets` field added to `module.go`; `TemplateData[T].HeadAssets *HeadAssets` field added to `templatedata.go`; `forgeHeadTmpl` extended with HeadAssets block (preconnect → stylesheets → favicons → scripts); both render paths propagate `headAssets`. Shipped in v1.3.0. |
| 2026-04-03 | Amendment A64 (`head.go`, `templatedata.go`): `PageHead` new exported struct holding `Head`, `OGDefaults`, `AppSchema`, `HeadAssets`; `TemplateData[T]` refactored to embed `PageHead` anonymously — fields promoted to top level, all template access paths unchanged; `NewTemplateData` body updated to `PageHead: PageHead{Head: head}`; custom handler structs can now embed `forge.PageHead` to gain `{{template "forge:head" .}}` support without using `TemplateData[T]`. Shipped in v1.4.0. |
| 2026-04-04 | Amendment A65 (`module.go`, `templatedata.go`, `templates.go`): `ContextFunc(fn)` new module option; `contextFuncOption` unexported type in `module.go`; `contextFunc func(Context, any) (any, error)` field on `Module[T]`; `resolveExtra` unexported method on `Module[T]` in `templates.go`; `TemplateData[T].Extra any` new field; called in `renderListHTML` and `renderShowHTML` after all other data fields are set; errors from `contextFunc` return nil and never abort the render. Shipped in v1.5.0. |
| 2026-04-05 | Amendment A66 (`auth.go`, `forge.go`, `forge-mcp/`): `TokenRecord`, `TokenStore`, `NewTokenStore(db, secret)` added to `auth.go`; `TokenStore.Create`, `List`, `Revoke`, `probeTable` methods; `VerifyBearerToken` signature extended to 3-arg `(r, secret, store *TokenStore)` — nil store preserves stateless HMAC behaviour; `Config.TokenStore *TokenStore` and `App.TokenStore()` accessor in `forge.go`; `Handler()` startup probe warns if `forge_tokens` table absent; `forge-mcp/mcp.go` wires `Server.tokenStore`; `forge-mcp/transport.go` updated sole call site; `forge-mcp/tool.go` adds `authoriseAdmin`, `tokenToolDefs`, `handleTokenTool`, pre-dispatch for token tools in `handleToolsCall`; `tools/list` exposes `create_token`/`list_tokens`/`revoke_token` when store configured (Admin role required). Shipped in v1.6.0 / forge-mcp v1.1.0. |
| 2026-04-05 | Amendment A67 (`templatehelpers.go`): `forgeHTML(s string) template.HTML` added — trusted raw HTML passthrough registered as `forge_html` in `TemplateFuncMap`; `TemplateFuncMap` godoc updated; `TestTemplateFuncMap_keys` expected count updated from 8 to 9; `TestForgeHTML` added (3 sub-tests). Shipped in v1.7.0. |
| 2026-04-06 | Decision 26 (`auth.go`, `errors.go`, `forge-mcp/tool.go`): `ErrLastAdmin` sentinel (409 `last_admin`) added to `errors.go`; `TokenStore.Revoke` gains pre-check — counts other active admin tokens before revoking; returns `ErrLastAdmin` if count is 0 and target is admin; `forge-mcp/tool.go` `revoke_token` surfaces actionable message for `ErrLastAdmin`. Shipped in forge v1.8.0, forge-mcp v1.2.0. |
| 2026-04-07 | Decision 27 (`mcp.go`, `module.go`, `forge-mcp/mcp.go`): `MCPField.Format string` and `MCPField.Description string` added to `mcp.go`; `mcpStructField` in `module.go` reads `forge_format` and `forge_description` struct tags; `fieldDescription` helper added to `forge-mcp/mcp.go`; `inputSchema` and `inputSchemaUpdate` emit `"description"` key in JSON Schema properties with three-case priority logic (both → description + " (" + format + ")"; format-only → "(format)"; neither → omitted). Shipped in forge v1.9.0, forge-mcp v1.3.0. |
| 2026-04-07 | Decision 28 (`forge-cli/`): new stdlib-only submodule `github.com/forge-cms/forge/forge-cli` (`package main`); content CRUD + lifecycle via GET-then-PUT to Forge REST API; token management via MCP JSON-RPC 2.0; YAML-subset frontmatter parser; `Config` from `FORGE_URL`/`FORGE_TOKEN`/`FORGE_MCP_URL` env vars; G23 integration test validates GET→PUT round-trip contract. Tagged `forge-cli/v0.1.0`. |

---

All files are in a single package: `forge`. There are no sub-packages.
This is intentional — it eliminates circular import issues and keeps
the API surface in one place. The file names are the organisation.

### Implemented (Milestone 1 + Milestone 2)

```
github.com/forge-cms/forge/
│
├── errors.go         Error interface, sentinel errors, WriteError(), ValidationError
├── roles.go          Role type, hierarchy, HasRole(), IsRole(), built-in constants, Option interface
├── mcp.go            MCPOperation type, MCPRead/MCPWrite constants, MCP() option,
│                     MCPMeta struct, MCPField struct (incl. Format/Description — D27), MCPModule interface
│                     (Amendment A49)
├── node.go           Node, Status, lifecycle constants, NewID(), GenerateSlug(), UniqueSlug(), ValidateStruct()
│                     GetSlug(), GetPublishedAt(), GetStatus() getter methods (Amendment A2)
├── context.go        Context interface, contextImpl, ContextFrom(), NewTestContext(), User, GuestUser,
│                     NewBackgroundContext, NewContextWithUser
├── signals.go        Signal type, On[T]() option, dispatchBefore(), dispatchAfter(), debouncer,
│                     debouncer.Stop() (Amendment A39)
├── storage.go        DB interface, Query[T], QueryOne[T], Repository[T], MemoryRepo[T], ListOptions
├── auth.go           AuthFunc interface, BearerHMAC, CookieSession, BasicAuth, AnyAuth, SignToken,
│                     VerifyBearerToken(r, secret, store *TokenStore);
│                     TokenRecord, TokenStore, NewTokenStore (Amendment A66);
│                     Revoke last-admin guard — returns ErrLastAdmin (Decision 26)
├── middleware.go     RequestLogger, Recoverer, SecurityHeaders, CORS, MaxBodySize,
│                     RateLimit, TrustedProxy, InMemoryCache, CacheStore, Authenticate, CSRF, Chain
├── module.go         Module[T], NewModule, Register, Stop, At, Cache, Auth,
                      Middleware, Repo, On, SitemapConfig, AIIndex, WithoutID,
                      Feed, DisableFeed, ContextFunc options;
                      setSitemap, regenerateSitemap, setAIRegistry, regenerateAI, aiDocHandler;
                      setFeedStore, regenerateFeed; triggerRebuild();
                      aiFeatures, llmsStore, withoutID, feedCfg, feedStore,
                      contextFunc fields;
                      stoppable interface, stopCh field (Amendment A39);
                      debounce callback uses NewBackgroundContext (Amendment A41);
                      contextFuncOption, ContextFunc (Amendment A65)
│                     (Markdownable migrated to ai.go — Amendment A11)
├── forge.go          Config, MustConfig, New, App (Use/Content/Handle/Run/Handler/SEO),
│                     Registrator, SEOOption, seoState (robots/ogDefaults/appSchema), httpsRedirect,
│                     graceful shutdown via SIGINT/SIGTERM;
│                     SitemapStore wiring in Content+Handler (Amendment A4);
│                     SEO option loop, robotsTxtRegistered guard in Handler (Amendment A5);
│                     LLMsStore wiring in Content+Handler, llmsTxtRegistered +
                      llmsFullTxtRegistered guards (Amendment A13);
                      FeedStore wiring in Content+Handler, feedIndexRegistered guard (A16);
                      App.Cookies()/CookiesManifestAuth(), cookieDecls/cookieManifestOpts
                      fields, /.well-known/cookies.json lazy mount (Amendment A18);
                      redirectStore field, App.Redirect()/RedirectStore(), "/" fallback
                      mount (Amendment A20); redirectManifestReg, /.well-known/redirects.json
                      always mounted (Amendment A21); redirectManifestOpts field,
                      App.RedirectManifestAuth() (Amendment A22);
                      stoppableModules []stoppable field, Stop() wired after srv.Shutdown
                      (Amendment A39);
                      App.MCPModules() (Amendment A49);
                      App.Secret() (Amendment A50);
                      setSEODefaults push loop in Handler() (Amendment A61);
                      App.Partials() / App.MustParseTemplate(), partialsDir field,
                      setPartials push loop in Run() (Amendment A62);
                      Config.TokenStore, App.tokenStore, App.TokenStore(),
                      TokenStore startup probe in Handler() (Amendment A66)
└── head.go           Head (Title, Description, Author, Published, Modified, Image, Type,
                      Canonical, Tags, Breadcrumbs, Alternates, Social, NoIndex),
                      Image, Breadcrumb, Alternate, Headable, HeadFunc[T],
                      Excerpt, URL, AbsURL, Crumbs, Crumb, rich-result constants,
                      TwitterCardType (Summary/SummaryLargeImage/AppCard/PlayerCard),
                      TwitterMeta, SocialOverrides;
                      HeadAssets (SEOOption), FaviconLink, ScriptTag (Amendment A63);
                      PageHead (Amendment A64)
└── schema.go         SchemaFor, FAQProvider, HowToProvider, EventProvider,
                      RecipeProvider, ReviewProvider, OrganizationProvider,
                      FAQEntry, HowToStep, EventDetails, RecipeDetails,
                      ReviewDetails, OrganizationDetails;
                      AppSchema (SEOOption), renderAppSchema (Amendment A61)
└── sitemap.go        SitemapConfig, ChangeFreq, SitemapEntry, SitemapNode,
                      SitemapPrioritiser, SitemapStore, SitemapEntries[T],
                      WriteSitemapFragment, WriteSitemapIndex
└── robots.go         CrawlerPolicy (Allow/Disallow/AskFirst), RobotsConfig,
                      RobotsTxt, RobotsTxtHandler
└── templatedata.go   TemplateData[T] (embeds PageHead; Content, User, Request, SiteName, Extra),
                      PageHead (Head, OGDefaults, AppSchema, HeadAssets), NewTemplateData
                      (Amendment A61; HeadAssets field — Amendment A63; PageHead embedding — Amendment A64;
                      Extra field — Amendment A65)
└── templates.go      templateParser, Templates, TemplatesOptional, forgeHeadTmpl, parseTemplates,
                      renderListHTML, renderShowHTML, setSiteName, setSEODefaults,
                      errorTemplate, bindErrorTemplates;
                      Amendment A6 (Module[T] template fields + HTML render path),
                      Amendment A7 (errorTemplateLookup in errors.go),
                      Amendment A8 (templateModules + startup wiring in forge.go);
                      forge:head receiver changed to TemplateData, twitter:site and
                      AppSchema auto-emitted (Amendment A61);
                      loadPartials, setPartials, parseOneTemplate accepts partials slice
                      (Amendment A62);
                      HeadAssets block in forgeHeadTmpl, setSEODefaults 3-arg,
                      HeadAssets propagated in render paths (Amendment A63);
                      resolveExtra, ContextFunc extra propagated in render paths (Amendment A65)
└── templatehelpers.go forgeMeta, forgeDate, forgeRFC3339, forgeMarkdown, forgeHTML, forgeExcerpt, forgeCSRFToken,
                      forgeLLMSEntries(data any), TemplateFuncMap();
                      Amendment A9 (parseOneTemplate uses .Funcs(TemplateFuncMap()));
                      forge_rfc3339 added (M5 Step 1) for article:published_time in forge:head;
                      forgeLLMSEntries wired to real implementation (Amendment A12);
                      forgeHTML / forge_html passthrough (Amendment A67)
└── social.go         SocialFeature, OpenGraph, TwitterCard, Social() option;
                      OGDefaults (SEOOption), mergeOGDefaults (Amendment A61)
└── ai.go             Markdownable (migrated from module.go, A11), AIDocSummary,
                      AIFeature, LLMsTxt, LLMsTxtFull, AIDoc constants,
                      AIIndex() option, WithoutID() option,
                      LLMsEntry, LLMsTemplateData, LLMsStore, NewLLMsStore,
                      extractNode, renderAIDoc, hasAIFeature
└── feed.go           FeedConfig, Feed() option (opt-in, A16), DisableFeed() option,
                      FeedStore, NewFeedStore, buildRSSItem, capitalisePrefixTitle,
                      guessMIMEType, writeRSSFeed;
                      ModuleHandler → /{prefix}/feed.xml;
                      IndexHandler → /feed.xml aggregate (reverse-chronological)
└── integration_test.go 15 integration tests: HTML render cycle, forge:head, error pages,
                      CSRF round-trip, App-level SEO/sitemap, TemplateData correctness
└── integration_full_test.go 19 cross-milestone tests (M1–M4): multi-module routing,
                      global middleware order, role guards, AfterCreate/AfterDelete/isolation,
                      content negotiation, forge_meta/forge_markdown/BreadcrumbList,
                      sitemap in robots.txt, error template first-match + fallthrough,
                      TemplateData siteName + request URL

github.com/forge-cms/forge-pgx/  (separate module: ./forge-pgx/)
└── pgx.go            Wrap(*pgxpool.Pool) forge.DB — native pgx adapter

github.com/forge-cms/forge/forge-mcp/  (sub-module: ./forge-mcp/)
├── mcp.go            Server (secret []byte), New(app, opts...), ServerOption,
│                     WithSecret; handle (JSON-RPC dispatch), handleInitialize,
│                     JSON-RPC wire types (jsonRPCRequest/Response/Error),
│                     mcpTool, mcpResource, allResources, mcpToolDefs,
│                     inputSchema, inputSchemaUpdate, hasMCPOp, slugOf, snakeCase
├── resource.go       handleResourceMethod, handleResourcesList,           ✅ Milestone 10 Step 2
│                     handleResourcesTemplatesList, handleResourcesRead,
│                     parseResourceURI; mcpResource/resourceContent/resourceTemplate
├── tool.go           handleToolMethod, handleToolsList, handleToolsCall,  ✅ Milestone 10 Step 3
│                     toolName, parseToolName, moduleForType, moduleForAdminList,
│                     authorise, authoriseEditor, errorFor, stringArg, toolResult;
│                     mcpAdminReadToolDefs (Amendment A54); delete→Editor auth (Amendment A55)
├── transport.go      ServeStdio(ctx, in, out), Handler(),                 ✅ Milestone 10 Step 4
│                     sseHandler, messageHandler
└── README.md         AI-first integration guide: quick start, Claude/Cursor  ✅ Milestone 10 Step 5
                      config, SSE Bearer auth, MCPRead vs MCPWrite table

github.com/forge-cms/forge/forge-cli/  (sub-module: ./forge-cli/)
├── client.go         Config{ForgeURL,Token,MCPURL}, loadConfig, loadEnvFile,
│                     request, getItem, mergeFields, printJSON, fatal
├── frontmatter.go    parseFrontmatter, parseFrontmatterFile — YAML-subset parser
├── content.go        runContentCommand, runCreate, runUpdate, runLifecycle,
│                     runDelete, runList, runGet, findKey, findKeyIn
├── token.go          runTokenCommand, mcpCall — create/list/revoke via MCP JSON-RPC
├── status.go         runStatus — GET /_health
├── main.go           Entry point + top-level subcommand router
└── cli_test.go       Unit tests: frontmatter (9), mergeFields (2), loadEnvFile (3)
```

### Shipped (Milestones 7–8)

```
├── storage.go (extend) SQLRepo[T] — production Repository[T] backed by forge.DB;
│                     Table() SQLRepoOption; auto-derived table names (snake_case plural);
│                     FindByID/FindBySlug/FindAll/Save/Delete; reuses dbFields cache;
│                     $N SQL placeholders (Amendment A19)                      ✅ Milestone 7
├── redirects.go      RedirectCode (MovedPermanently/Gone), RedirectEntry (+IsPrefix),
│                     From type, Redirects() option, RedirectStore (exact + prefix
│                     lookup, chain collapse, DB persistence via Load/Save/Remove),
│                     App.Redirect(), "/" fallback wiring (Amendment A20)      ✅ Milestone 7
├── redirectmanifest.go  buildRedirectManifest, newRedirectManifestHandler;
│                     GET /.well-known/redirects.json (always mounted, live JSON);
│                     reuses ManifestAuth option (Amendment A21)               ✅ Milestone 7
└── scheduler.go      Adaptive ticker, scheduled publishing loop               ✅ Milestone 8
```

---

## Request lifecycle

A request arriving at a Forge app passes through these layers in order.
**Read (GET) and write (POST/PUT/DELETE) paths diverge after context creation.**

```
HTTP Request
    │
    ▼
┌─────────────────────────────────┐
│  Global middleware chain        │  RequestLogger, Recoverer, SecurityHeaders,
│  (app.Use order)                │  CORS, MaxBodySize, RateLimit, CSRF
└────────────────┬────────────────┘
                 │
    ▼
┌─────────────────────────────────┐
│  net/http ServeMux router       │  Go 1.22 pattern matching, path parameters
└────────────────┬────────────────┘
                 │
    ▼
┌─────────────────────────────────┐
│  forge.Context creation         │  ContextFrom(w, r)
│                                 │  Sets X-Request-ID (UUID v7 if absent)
│                                 │  Extracts User resolved by auth middleware
└────────────────┬────────────────┘
                 │
    ▼ GET / read only
┌─────────────────────────────────┐
│  Cache check                    │  forge.Cache(ttl) per-module LRU
│                                 │  HIT → write X-Cache: HIT, return immediately
│                                 │  MISS → continue (X-Cache: MISS set on response)
└────────────────┬────────────────┘
                 │
    ▼
┌─────────────────────────────────┐
│  Role check                     │  ctx.User().HasRole(required)
│                                 │  Insufficient role → 403
└────────────────┬────────────────┘
                 │
    ▼
┌─────────────────────────────────┐
│  Storage fetch                  │  repo.FindBySlug / repo.FindAll
│                                 │  Not found → 404
└────────────────┬────────────────┘
                 │
    ▼ GET / read only
┌─────────────────────────────────┐
│  Lifecycle enforcement          │  non-Published + Guest → 404
│                                 │  (404 intentional — do not leak draft existence)
└────────────────┬────────────────┘
                 │
    ▼ POST / PUT / DELETE only
┌─────────────────────────────────┐
│  Input decode + validation      │  json.Decode → auto-ID/Slug → RunValidation
│                                 │  Validation failure → 422
└────────────────┬────────────────┘
                 │
    ▼
┌─────────────────────────────────┐
│  BeforeX signals                │  Synchronous. Can abort with error → 500.
│                                 │  BeforeCreate / BeforeUpdate / BeforeDelete
└────────────────┬────────────────┘
                 │
    ▼
┌─────────────────────────────────┐
│  Storage operation              │  repo.Save / repo.Delete
└────────────────┬────────────────┘
                 │
    ▼
┌─────────────────────────────────┐
│  AfterX signals                 │  Asynchronous (goroutine). Cannot abort.
│                                 │  AfterCreate/Update/Delete/Publish/Unpublish/Archive
└────────────────┬────────────────┘
                 │
    ▼
┌─────────────────────────────────┐
│  Content negotiation            │  application/json → JSON (default for API-only modules)
│                                 │  text/html       → HTML when templates configured
│                                 │  text/markdown   → Markdown() or JSON fallback
│                                 │  text/plain      → stripped text
│                                 │  Vary: Accept always set
│                                 │  Empty/"*/*" Accept: HTML preferred when n.html (A53)
└────────────────┬────────────────┘
                 │
    ▼
HTTP Response  (X-Request-ID always set)
```

---

## Error handling pipeline

See `ERROR_HANDLING.md` for the full strategy. This section summarises the
architectural contracts that are enforced across all files.

### The single pipeline rule

Every error-to-HTTP translation goes through `WriteError(w, r, err)`. No file
may call `http.Error`, write a raw status, or format an error response by hand.
This includes middleware and helpers that have access to `http.ResponseWriter`
and `*http.Request`.

### Error type dispatch (inside `WriteError`)

```
err
 ├── errors.As(*ValidationError)  →  422 + fields array
 ├── errors.As(forge.Error) 4xx   →  status from error, public message
 ├── errors.As(forge.Error) 5xx   →  logged + generic 500
 └── anything else                →  logged + generic 500
```

`errors.As` is required at every inspection point — never direct type assertions.

### Sentinel registry

All sentinels live in `errors.go`. Call sites reference the package-level variable.
`newSentinel` must never be called outside `errors.go`.

| Variable | Status | Code |
|----------|--------|------|
| `ErrBadRequest` | 400 | `bad_request` |
| `ErrUnauth` | 401 | `unauthorized` |
| `ErrForbidden` | 403 | `forbidden` |
| `ErrNotFound` | 404 | `not_found` |
| `ErrNotAcceptable` | 406 | `not_acceptable` |
| `ErrConflict` | 409 | `conflict` |
| `ErrGone` | 410 | `gone` |
| `ErrRequestTooLarge` | 413 | `request_too_large` |
| `ErrTooManyRequests` | 429 | `too_many_requests` |

### `errorTemplateLookup` — one-shot initialisation

`errorTemplateLookup` is guarded by `sync.Once`. It is set exactly once by
`App.Handler()`. Subsequent calls to `App.Handler()` are no-ops for this
variable. Reads in `respond()` are safe with no additional locking.

### X-Request-ID contract

- Set by `ContextFrom` on every request (UUID v7 if absent from inbound header)
- `RequestLogger` must be the outermost middleware to ensure it is set before any handler runs
- `WriteError` reads from the response header first, then falls back to the request header
- Appears in: response header, JSON error body, every `slog.Error` call

---

## Stable interfaces (public API contracts)

These interfaces are the extension points for users of Forge.
They must not change in v1.x without a deprecation cycle.

### Implemented (Milestone 1)

```go
// Markdownable — implement to enable text/markdown content negotiation.
// Declared in module.go.
type Markdownable interface {
    Markdown() string
}

// Validatable — implement to run custom validation after struct-tag validation
type Validatable interface {
    Validate() error
}

// AuthFunc — implement to provide a custom authentication scheme.
// Forge provides BearerHMAC, CookieSession, BasicAuth, and AnyAuth.
type AuthFunc interface {
    authenticate(*http.Request) (User, bool)
}

// Repository[T] — implement to provide a custom storage backend
type Repository[T any] interface {
    FindByID(ctx context.Context, id string) (T, error)
    FindBySlug(ctx context.Context, slug string) (T, error)
    FindAll(ctx context.Context, opts ListOptions) ([]T, error)
    Save(ctx context.Context, node T) error
    Delete(ctx context.Context, id string) error
}

// Context — the request context passed to all hooks and handlers.
// Implemented as an interface (not a struct) to enable testing without HTTP.
type Context interface {
    context.Context
    User() User
    Locale() string
    SiteName() string
    RequestID() string
    Request() *http.Request
    Response() http.ResponseWriter
}

// Error — all Forge errors implement this
type Error interface {
    error
    HTTPStatus() int
    Code() string
    Public() string
}

// DB — satisfied by *sql.DB, *sql.Tx, and pgx adapters
type DB interface {
    QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
    ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
    QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Registrator — implemented by *Module[T]; pass to App.Content for type-safe registration
type Registrator interface {
    Register(mux *http.ServeMux)
}
```

### Key exported functions and types (Milestone 1 + Milestone 2 Step 1)

```go
// App bootstrap (forge.go)
type Config struct {
    BaseURL      string        // required: canonical site URL, e.g. "https://example.com"
    Secret       []byte        // required: min 16 bytes; used for HMAC tokens and cookies
    Version      string        // optional: included in GET /_health response when non-empty
    DB           DB            // optional: *sql.DB or forgepgx.Wrap(pool)
    HTTPS        bool          // optional: enable HTTP→HTTPS redirect
    ReadTimeout  time.Duration // optional: default 5 s
    WriteTimeout time.Duration // optional: default 10 s
    IdleTimeout  time.Duration // optional: default 120 s
}
func MustConfig(cfg Config) Config           // validates Config; panics with descriptive msg
func New(cfg Config) *App                    // creates App; applies default timeouts

func (a *App) Health()                                              // mount GET /_health (opt-in)
func (a *App) Use(mws ...func(http.Handler) http.Handler)  // append global middleware
func (a *App) Handle(pattern string, h http.Handler)       // register raw handler
func (a *App) Content(v any, opts ...Option)               // register *Module[T] or untyped module
func (a *App) Handler() http.Handler                       // compose all routes + middleware
func (a *App) Run(addr string) error                       // listen; graceful shutdown on SIGINT/SIGTERM

// SignToken — ttl=0 means no expiry; ttl>0 embeds exp claim, rejected after expiry
func SignToken(user User, secret string, ttl time.Duration) (string, error)

// Authenticate — sets Context.User() for every request; pairs with Auth(Read/Write) on modules
func Authenticate(auth AuthFunc) func(http.Handler) http.Handler

// CSRF — double-submit cookie protection; wrap CookieSession-authenticated routes only
func CSRF(auth AuthFunc) func(http.Handler) http.Handler

// RateLimit — pass TrustedProxy() when running behind nginx/Caddy/CloudFlare
func RateLimit(n int, d time.Duration, opts ...Option) func(http.Handler) http.Handler
func TrustedProxy() Option

// CacheStore — exported LRU cache backing forge.Cache() and forge.InMemoryCache()
type CacheStore struct{ /* unexported */ }
func NewCacheStore(ttl time.Duration, max int) *CacheStore
func (c *CacheStore) Flush()  // invalidate all entries (called on write operations)
func (c *CacheStore) Sweep()  // remove expired entries (called by background ticker)

// ListOptions — Status filter is applied inside the repository layer
type ListOptions struct {
    Page    int
    PerPage int
    OrderBy string
    Desc    bool
    Status  []Status // nil/empty = all statuses; non-empty = exact match filter
}
```

### Shipped (Milestones 3–5)

```go
// Headable — implement to control SEO, social, and AI metadata  (head.go) ✅ Milestone 3
type Headable interface {
    Head() Head
}

// AIDocSummary — optional; custom AIDoc summary field           (ai.go)   ✅ Milestone 5
// NOTE: the method is AISummary(), not AIDocSummary()
type AIDocSummary interface {
    AISummary() string
}

// SitemapPrioritiser — optional; per-item sitemap priority (sitemap.go)  ✅ Milestone 3
type SitemapPrioritiser interface {
    SitemapPriority() float64
}
```

---

## Internal dependency rules

To prevent circular imports and keep the package coherent, these rules apply.
All files listed below are implemented.

```
errors.go       — no internal dependencies (foundation layer)
roles.go        — no internal dependencies (foundation layer)
mcp.go          — no internal dependencies
node.go         — depends on: errors
context.go      — depends on: roles, node
auth.go         — depends on: errors, roles, context, node
signals.go      — depends on: context, errors
storage.go      — depends on: node, errors
middleware.go   — depends on: errors, context, auth, node
module.go       — depends on: node, context, signals, storage, errors, middleware

── shipped (Milestones 2–8) ─────────────────────────────────────────────────
head.go         — no internal dependencies                              ✅ Milestone 3
forge.go        — depends on: all of the above                          ✅ Milestone 2
templates.go    — depends on: head, context, node                       ✅ Milestone 4
cookies.go      — depends on: errors (none — stdlib net/http only)      ✅ Milestone 6
├── cookiemanifest.go — depends on: cookies, forge.go (Amendment A18)  ✅ Milestone 6
redirects.go    — depends on: errors, storage (forge.DB), forge.go (A20)       ✅ Milestone 7
├── redirectmanifest.go — depends on: redirects, cookiemanifest (manifestAuthOption), forge.go (A21) ✅ Milestone 7
sitemap.go      — depends on: node, signals                             ✅ Milestone 3
rss.go          — depends on: node, signals, head                       ✅ Milestone 5
ai.go           — depends on: node, head                                ✅ Milestone 5
social.go       — depends on: head                                      ✅ Milestone 5
scheduler.go    — depends on: node, signals, storage                    ✅ Milestone 8
```

The dependency graph has no cycles. `errors.go` and `roles.go` are the only
true foundation files — everything else can depend on them freely.

---

## forge.Node embedding

Every content type embeds `forge.Node`. Embedding (not composition) is required
because Forge uses reflection to access Node fields directly:

```go
// forge reads these fields by name via reflection — do not rename them
type Node struct {
    ID          string
    Slug        string
    Status      Status
    PublishedAt time.Time
    ScheduledAt *time.Time
    CreatedAt   time.Time
    UpdatedAt   time.Time
}
```

The reflection access is cached on first use via `sync.Map` — field lookup
is O(1) after the first request for any given type.

---

## Signal dispatch

Signals are dispatched synchronously (BeforeX) or asynchronously (AfterX).

```
BeforeCreate / BeforeUpdate / BeforeDelete
    → run in request goroutine
    → return error → operation aborted, error returned to client
    → panic → recovered, logged, 500 returned

AfterCreate / AfterUpdate / AfterDelete / AfterPublish / AfterUnpublish / AfterArchive
    → run in new goroutine (go dispatch(...))
    → errors logged, never returned to client
    → panic recovered and logged

SitemapRegenerate
    → fired by AfterPublish, AfterUnpublish, AfterArchive, AfterDelete
    → debounced 2 seconds — coalesces bursts of changes
    → runs sitemap + feed regeneration
```

---

## Scheduler *(Milestone 8)*

The scheduled publishing loop runs as a goroutine started by `app.Run()`.

```
On startup:
    query storage for the next scheduled item (MIN(scheduled_at) WHERE status = 'scheduled')
    if found: set timer to time.Until(scheduled_at)
    if not found: set fallback ticker to 60 seconds

On tick:
    query all items WHERE status = 'scheduled' AND scheduled_at <= now
    for each: set status = published, set published_at = now
              fire AfterPublish signal (async)
    recalculate next scheduled item → reset timer

On shutdown:
    wait for in-progress tick to complete (max 5 seconds)
    then exit
```

---

## Content negotiation

A single endpoint responds differently based on the `Accept` header:

```
Accept: application/json     → JSON response (default for API clients)
Accept: text/html            → rendered template
Accept: text/markdown        → calls Markdown() if implemented, else 406
Accept: text/plain           → stripped plaintext version
```

The `Accept` header check uses pre-compiled content-type matching per module,
not string comparison on every request.

---

## Redirect table

The redirect table is a flat key-value store keyed by `FromPath`.
It lives alongside the content — in the same database, same transaction.

Redirect lookups happen only on requests that would otherwise produce a 404.
The resolution order:

```
1. Try to find a published node with this slug in this module
2. If not found: check redirect table for this path
3. If found in redirect table: serve 301 or 410
4. If not found anywhere: serve 404
```

This means redirect lookup adds zero overhead to successful requests.

---

## Cache

The LRU cache is per-module, not global. Each `forge.Cache(ttl)` call
creates an independent cache for that module.

```
Cache key:   "{method}:{path}:{accept-header}"
Cache value: serialised HTTP response (status + headers + body)
Max entries: 1000 per module (configurable)
Eviction:    LRU when max entries reached
TTL:         hard expiry per entry
Invalidation: AfterCreate / AfterUpdate / AfterDelete signals clear the module cache
```

`X-Cache: HIT` and `X-Cache: MISS` headers are always set.

---

## Storage and the forge.DB interface

Forge defines a minimal `forge.DB` interface internally:

```go
type DB interface {
    QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
    ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
    QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}
```

This interface is satisfied by:
- `*sql.DB` (standard library) — zero additional dependency
- `*sql.Tx` — transactions work automatically
- `forgepgx.Wrap(pool)` — native pgx pool adapter (~2.5× faster for PostgreSQL)
- Any custom type that implements the three methods

`forge.Query[T]` and `forge.QueryOne[T]` accept `forge.DB`, not `*sql.DB`.
This means switching drivers requires changing exactly one value in `forge.Config`.

The `forge-pgx` adapter lives at `github.com/forge-cms/forge-pgx` — a separate
module. It imports both `forge` and `pgx/v5`. Forge core never imports pgx.

---

## Template data shape

```go
// show handler
TemplateData[T] {
    Content  T             // the single content item
    Head     forge.Head    // from item.Head() merged with module HeadFunc
    User     forge.User    // current user — zero value if Guest
    Request  *http.Request
}

// list handler
TemplateData[[]T] {
    Content  []T           // slice of items
    Head     forge.Head    // from module HeadFunc
    User     forge.User
    Request  *http.Request
}
```

---

## Testing

Every public interface has a test double:

```go
// In-memory repository — no database needed
repo := forge.NewMemoryRepo[*BlogPost]()

// Test context — no HTTP needed
ctx := forge.NewTestContext(forge.User{
    ID:    "test-user",
    Roles: []forge.Role{forge.Editor},
})

// Token for test requests — ttl=0 means no expiry
tok, _ := forge.SignToken(user, "test-secret", 0)

// Module integration test via httptest — no app.Run() required
repo := forge.NewMemoryRepo[*Post]()
m := forge.NewModule((*Post)(nil), forge.Repo(repo))
mux := http.NewServeMux()
m.Register(mux)
w := httptest.NewRecorder()
r := httptest.NewRequest(http.MethodGet, "/posts", nil)
mux.ServeHTTP(w, r)
```

Use `net/http/httptest` with `m.Register(mux)` for module integration tests.
Use `forge.NewTestContext()` with direct signal handler calls for unit tests.
`forge.App` / `app.Handler()` will be available from Milestone 2.
