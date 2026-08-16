# Smeldr — Architecture

This document describes the internal structure of Smeldr: how the packages
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
| 2026-03-02 | Milestone 2 Step P1: `forge-pgx` module implemented — `Wrap(pool)` native pgx adapter satisfying `smeldr.DB` |
| 2026-03-03 | Milestone 3 Step 1: `head.go` implemented — `Head`, `Image`, `Breadcrumb`, `Alternate`, `Headable`, `HeadFunc`, `Excerpt`, `URL`, `Crumbs`; `Module[T].headFunc` field added (Amendment A1) |
| 2026-03-03 | Milestone 3 Step 2: `schema.go` implemented — `SchemaFor`, 8 JSON-LD rich result types (Article, Product, FAQPage, HowTo, Event, Recipe, Review, Organization), BreadcrumbList, 6 provider interfaces (FAQProvider, HowToProvider, EventProvider, RecipeProvider, ReviewProvider, OrganizationProvider) |
| 2026-03-03 | Milestone 3 Step 3: `sitemap.go` implemented — `SitemapConfig`, `ChangeFreq`, `SitemapNode`, `SitemapPrioritiser`, `SitemapEntry`, `SitemapStore`, `WriteSitemapFragment`, `SitemapEntries`, `WriteSitemapIndex`; Amendments A2 (node.go getters), A3 (Module sitemap wiring), A4 (App sitemap store + Handler guard) |
| 2026-03-03 | Milestone 3 Step 4: `robots.go` implemented — `CrawlerPolicy`, `Allow`/`Disallow`/`AskFirst`, `RobotsConfig`, `RobotsTxt`, `RobotsTxtHandler`; Amendment A5: `SEOOption`, `seoState`, `App.SEO()`, `robotsTxtRegistered` guard in `forge.go` |
| 2026-03-05 | Milestone 4 Step 1: `templatedata.go` implemented — `TemplateData[T]`, `NewTemplateData` constructor; `SiteName` sourced from `Config.BaseURL` hostname |
| 2026-03-05 | Milestone 4 Step 2: `templates.go` implemented — `templateParser` interface, `Templates`/`TemplatesOptional` options, `forgeHeadTmpl` const, `parseTemplates()`/`renderListHTML`/`renderShowHTML` on `Module[T]`, `bindErrorTemplates`; Amendments A6 (`module.go` template fields + HTML render path), A7 (`errors.go` `errorTemplateLookup`), A8 (`forge.go` `templateModules` + startup parse wiring) |
| 2026-03-05 | Milestone 4 Step 3: `templatehelpers.go` implemented — `forgeMeta`, `forgeDate`, `forgeMarkdown` (stdlib-only), `forgeExcerpt`, `forgeCSRFToken`, `forgeLLMSEntries` (stub), `TemplateFuncMap()`; Amendment A9 (`templates.go` `parseOneTemplate` now calls `.Funcs(TemplateFuncMap())`) |
| 2026-03-05 | Milestone 4 Step 4: `integration_test.go` implemented — 15 cross-component integration tests covering HTML render cycle, smeldr:head correctness, error pages (custom + fallback), CSRF token round-trip, App-level SEO/sitemap routing, and TemplateData field propagation |
| 2026-03-05 | Milestone 4 Step 5: `integration_full_test.go` implemented — 19 cross-milestone integration tests (M1–M4): multi-module routing, global middleware order, role-gated access (HasRole + inline middleware), AfterCreate/AfterDelete/cross-module signal isolation, content negotiation across two module types, smeldr_meta/smeldr_markdown/BreadcrumbList through render, sitemap URL in robots.txt, error template first-match and fallthrough, TemplateData siteName and request URL |
| 2026-03-06 | Milestone 5 Step 1: `social.go` implemented — `SocialFeature`, `OpenGraph`, `TwitterCard`, `Social()` option; Amendment A9 (`head.go`: `Tags []string`, `TwitterCardType`, `TwitterMeta`, `SocialOverrides`, `Head.Social` field); Amendment A10 (`templates.go` `forgeHeadTmpl` extended — full OG + Twitter block, `smeldr_rfc3339` added to `templatehelpers.go` and `TemplateFuncMap()`, Module[T].social field + case in `module.go`) |
| 2026-03-06 | Milestone 5 Step 2: `ai.go` implemented — `Markdownable` (A11: migrated from `module.go`), `AIDocSummary`, `AIFeature`, `LLMsTxt`/`LLMsTxtFull`/`AIDoc` constants, `AIIndex()` option, `WithoutID()` option, `LLMsEntry`, `LLMsTemplateData`, `LLMsStore`, `NewLLMsStore`, `extractNode`, `renderAIDoc`; `forgeLLMSEntries(data any)` wired in `templatehelpers.go` (A12); `LLMsStore` wiring in `forge.go` Content+Handler (A13); README one-liner added (A14); AIDoc URL uses `/{prefix}/{slug}/aidoc` — Go’s net/http.ServeMux does not support partial wildcard segments, so `/{slug}.aidoc` is not a valid pattern (A15: DECISIONS.md updated) || 2026-03-06 | Milestone 5 Step 3: `feed.go` implemented — `FeedConfig`, `Feed()` option (opt-in, Amendment A16: Decision 13 updated), `FeedDisabled()` option, `rssItem`/`rssChannel`/`rssRoot` XML structs, `FeedStore`, `NewFeedStore`, `buildRSSItem`, `capitalisePrefixTitle`, `guessMIMEType`, `writeRSSFeed`; `ModuleHandler` serves `/{prefix}/feed.xml`, `IndexHandler` serves `/feed.xml` aggregate (all Published items, reverse-chronological); `feedCfg`/`feedStore`/`regenerateFeed`/`setFeedStore` added to `module.go`; `feedStore`/`feedIndexRegistered` added to `forge.go` |
| 2026-03-06 | Milestone 5 Step 4: `integration_full_test.go` extended — G9–G12 cross-milestone groups appended: G9 (Social + SitemapConfig M3): OG/Twitter tags in smeldr:head, Draft → 404; G10 (AIIndex + M4 content negotiation): /llms.txt Published/Draft filter, /posts/{slug}/aidoc 200/404, Accept:text/markdown alongside AIDoc; G11 (Feed + M1 AfterPublish signal): /posts/feed.xml RSS 2.0, Draft excluded, AfterPublish fires within 500ms; G12 (Full M5 stack): Social+AIIndex+Feed+SitemapConfig+HeadFunc+Templates — OG/Twitter, /llms.txt, /aidoc, /feed.xml all verified. README.md: AI indexing and Social sharing badges updated from 🔲 Coming in Milestone 5 → ✅ Available. Milestone 5 complete. |
| 2026-03-06 | Amendment A17: `compressIfAccepted(w, r, body, contentType)` helper added to `ai.go`; gzip applied directly at AI endpoint handlers — `CompactHandler`, `FullHandler`, `renderAIDoc` (now takes `r *http.Request`); 1400-byte threshold; `Vary: Accept-Encoding` always set. Supersedes Decision 13 Amendment A clause 3. Tests: `TestCompressIfAccepted_gzip`, `TestCompressIfAccepted_smallBody`, `TestCompressIfAccepted_noAcceptEncoding`, `TestLLMsTxt_gzip`, `TestAIDoc_gzip`. |
| 2026-03-07 | Milestone 6 Step 1: `cookies.go` implemented — `CookieCategory` (`Necessary`/`Preferences`/`Analytics`/`Marketing`), `Cookie` struct, `SetCookie`, `SetCookieIfConsented`, `ReadCookie`, `ClearCookie`, `ConsentFor`, `GrantConsent`, `RevokeConsent`; `smeldr_consent` Necessary cookie stores consent state; Decision 5 enforcement: `SetCookie` panics on non-Necessary, `SetCookieIfConsented` panics on Necessary. |
| 2026-03-07 | Milestone 6 Step 2: `cookiemanifest.go` implemented — `cookieManifest`/`cookieManifestEntry` JSON types, `buildManifest`, `sameSiteName`, `ManifestAuth` option, `newCookieManifestHandler`; Amendment A18: `App.Cookies()`, `App.CookiesManifestAuth()`, `cookieDecls`/`cookieManifestOpts` fields added to `forge.go`; `GET /.well-known/cookies.json` mounted lazily in `App.Handler()`. |
| 2026-03-07 | Milestone 6 Step 3: `integration_full_test.go` extended — G13–G15 cross-milestone groups appended: G13 (M6 consent enforcement, Decision 5): SetCookie/ConsentFor/SetCookieIfConsented/GrantConsent/RevokeConsent; G14 (M6 + M2 handler pattern): consent lifecycle wired through an HTTP handler, ClearCookie expiry, Necessary always-true; G15 (M6 + M2 App + M1 BearerHMAC): manifest mounted/sorted/not-mounted-when-empty, authGuard 401/200. README.md: Cookies & Compliance badge updated from 🔲 Coming in Milestone 6 → ✅ Available. Milestone 6 complete. |
| 2026-03-07 | Milestone 7 Step 1: `storage.go` extended — `SQLRepo[T]` production `Repository[T]` backed by `smeldr.DB`; `Table()` `SQLRepoOption`; `camelToSnake()` + plural table-name derivation; `FindByID`/`FindBySlug` delegate to `QueryOne`; `FindAll` with status IN, ORDER BY, LIMIT/OFFSET; `Save` upsert (ON CONFLICT); `Delete` returns `ErrNotFound` when RowsAffected==0. 9 new `TestSQLRepo_*` tests + extended fake driver. Amendment A19. |
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
| 2026-03-16 | Milestone 10 Step 1: `smeldr.dev/mcp/mcp.go` + Amendment A49 — `smeldr.MCPModule` interface added to `mcp.go`; `Module[T]` implements it in `module.go`; `App.MCPModules()` added to `forge.go`; `smeldr.dev/mcp` scaffold (`go.mod`, `Server`, `New`, JSON-RPC types, `handle`, `handleInitialize`, `snakeCase`, `hasMCPOp`, `slugOf`, `mcpToolDefs`, `inputSchema`, `inputSchemaUpdate`). |
| 2026-03-16 | Milestone 10 Step 2: `smeldr.dev/mcp/resource.go` — read path: `handleResourceMethod`, `handleResourcesList`, `handleResourcesTemplatesList`, `handleResourcesRead`, `parseResourceURI`; `mcpResource`/`resourceContent`/`resourceTemplate` wire types; Published-only lifecycle enforcement. `handle` default case delegates to `handleResourceMethod`. |
| 2026-03-17 | Milestone 10 Step 3: `smeldr.dev/mcp/tool.go` — write path: `handleToolMethod`, `handleToolsList`, `handleToolsCall` dispatcher (create/update/publish/schedule/archive/delete); `toolName`, `parseToolName`, `moduleForType`, `authorise`, `errorFor`, `stringArg` helpers; Author-level role enforcement; Flag H idempotency on publish; Flag F delete response `{"deleted":true,"slug":...}`; godoc NOTE on zero-value limitation (Flag G). `handle` default case now delegates to `handleToolMethod` before `handleResourceMethod`. |
| 2026-03-16 | Milestone 10 Step 4: `smeldr.dev/mcp/transport.go` + Amendment A50 — `ServeStdio(ctx, in, out)` with goroutine-based scanner and 1 MiB `bufio.Scanner` buffer limit; `Handler()` returning ServeMux with `GET /mcp` (SSE keepalive) and `POST /mcp/message` (HTTP 401 auth boundary + `MaxBytesReader` 1 MiB body limit + JSON-RPC response); A50 additions: `smeldr.VerifyBearerToken(r, secret)` in `auth.go`; `App.Secret()` accessor in `forge.go`; `smeldr.NewContextWithUser(user)` production-safe background context constructor in `context.go`; `Server.secret []byte` + `New(app, opts...)` auto-inherit + `WithSecret` option + mismatch `log.Printf` warning in `smeldr.dev/mcp/mcp.go`. 10 transport tests + 3 `TestVerifyBearerToken` sub-tests added. |
| 2026-03-17 | Amendment A51 (`templates.go`): `forgeHeadTmpl` now emits `twitter:card = summary_large_image` when `Head.Type` is `"Article"` or `"Product"`, regardless of whether `Head.Image.URL` is set; `Head.Social.Twitter.Card` explicit override takes priority. Five new sub-tests in `TestTemplates_twitterCard`. Shipped in v1.1.1. |
| 2026-03-17 | Amendment A52 (`module.go`): `mcpGoTypeStr` returns `"array"` for `reflect.Slice` kinds; new `coerceSliceFields` helper splits comma-separated string values for `[]string` fields before `MCPCreate`/`MCPUpdate` round-trip. (`smeldr.dev/mcp/mcp.go`): `inputSchema` and `inputSchemaUpdate` emit `{"type":"array","items":{"type":"string"}}` for array fields and suppress `minLength`/`maxLength`/`enum` constraints. Shipped in forge v1.1.2 / smeldr.dev/mcp v1.0.1. |
| 2026-03-18 | Amendment A53 (`module.go`): `negotiate()` now returns `"text/html"` when `Accept` is absent or `"*/*"` and the module has templates configured; previously returned `"application/json"` unconditionally, causing crawlers to receive JSON and miss structured data in `<head>`. API-only modules (no templates) are unaffected. Shipped in v1.1.3. |
| 2026-03-18 | Amendment A54 (`smeldr.dev/mcp/mcp.go`, `smeldr.dev/mcp/tool.go`): admin read tools added to every MCPWrite module — `mcpAdminReadToolDefs` generates `list_{type}s` and `get_{type}` tools; `authoriseEditor` enforces Editor or Admin role; `moduleForAdminList` resolves the plural typeSnake for list tool dispatch; `handleToolsList` and `handleToolsCall` wired. Shipped in smeldr.dev/mcp v1.0.2. |
| 2026-03-18 | Amendment A55 (`smeldr.dev/mcp/mcp.go`, `smeldr.dev/mcp/tool.go`): `delete_{type}` moved from Author-level `mcpToolDefs` to Editor-level `mcpAdminReadToolDefs`; `mcpAdminReadToolDefs` now generates 3 tools per MCPWrite module (list, get, delete); `delete` dispatch case calls `authoriseEditor` before executing. Shipped in smeldr.dev/mcp v1.0.5. |
| 2026-03-20 | Amendment A57 (`storage.go`): `quoteIdent()` helper added; applied to every generated column reference in `SQLRepo.Save`, `FindAll`, `FindByID`, `FindBySlug`, and `Delete`; prevents SQL syntax errors when `db` struct tags use reserved keywords (e.g. `db:"order"`). Shipped in v1.1.5. |
| 2026-03-20 | Amendment A58 (`forge.go`): `forgeVersions()` reads `runtime/debug.ReadBuildInfo()` at `Health()` mount time and `Run()` startup; `Health()` now includes `"forge"` and companion-module version keys in the JSON response instead of the removed `"version"` key; startup log line emitted to stderr before `ListenAndServe`. `Config.Version` retained for application use only. Shipped in v1.1.6. |
| 2026-03-20 | Amendment A59 (`forge.go`): `httpsRedirect()` exempts `/_health` from the HTTPS redirect — plain-HTTP requests to `/_health` pass through to `next` immediately, before the TLS / `X-Forwarded-Proto` check; reverse-proxy health checks no longer receive a `301`. Shipped in v1.1.7. |
| 2026-04-02 | Amendment A62 (`forge.go`, `templates.go`, `module.go`): `App.Partials(dir) *App` stores a partials directory; `loadPartials(dir)` reads `*.html` files alphabetically; `Module[T].setPartials([]string)` stores partial sources; `parseOneTemplate` now accepts `partials []string` and registers each into the template set after `smeldr:head`; `App.MustParseTemplate(path) *template.Template` loads a single template with FuncMap + smeldr:head + partials, panics on error. Shipped in v1.2.0. |
| 2026-04-03 | Amendment A63 (`head.go`, `templates.go`, `templatedata.go`, `forge.go`, `module.go`): `HeadAssets`, `HeadLink`, `ScriptTag` new exported types in `head.go`; `HeadAssets` implements `SEOOption` via `applySEO(*seoState)`; `seoState.headAssets` field added to `forge.go`; `App.Handler()` interface assertion updated to 3-arg `setSEODefaults(*OGDefaults, *AppSchema, *HeadAssets)`; `Module[T].headAssets` field added to `module.go`; `TemplateData[T].HeadAssets *HeadAssets` field added to `templatedata.go`; `forgeHeadTmpl` extended with HeadAssets block (preconnect → stylesheets → favicons → scripts); both render paths propagate `headAssets`. Shipped in v1.3.0. |
| 2026-04-03 | Amendment A64 (`head.go`, `templatedata.go`): `PageHead` new exported struct holding `Head`, `OGDefaults`, `AppSchema`, `HeadAssets`; `TemplateData[T]` refactored to embed `PageHead` anonymously — fields promoted to top level, all template access paths unchanged; `NewTemplateData` body updated to `PageHead: PageHead{Head: head}`; custom handler structs can now embed `smeldr.PageHead` to gain `{{template "smeldr:head" .}}` support without using `TemplateData[T]`. Shipped in v1.4.0. |
| 2026-04-04 | Amendment A65 (`module.go`, `templatedata.go`, `templates.go`): `ContextFunc(fn)` new module option; `contextFuncOption` unexported type in `module.go`; `contextFunc func(Context, any) (any, error)` field on `Module[T]`; `resolveExtra` unexported method on `Module[T]` in `templates.go`; `TemplateData[T].Extra any` new field; called in `renderListHTML` and `renderShowHTML` after all other data fields are set; errors from `contextFunc` return nil and never abort the render. Shipped in v1.5.0. |
| 2026-04-05 | Amendment A66 (`auth.go`, `forge.go`, `smeldr.dev/mcp/`): `TokenRecord`, `TokenStore`, `NewTokenStore(db, secret)` added to `auth.go`; `TokenStore.Create`, `List`, `Revoke`, `probeTable` methods; `VerifyBearerToken` signature extended to 3-arg `(r, secret, store *TokenStore)` — nil store preserves stateless HMAC behaviour; `Config.TokenStore *TokenStore` and `App.TokenStore()` accessor in `forge.go`; `Handler()` startup probe warns if `forge_tokens` table absent; `smeldr.dev/mcp/mcp.go` wires `Server.tokenStore`; `smeldr.dev/mcp/transport.go` updated sole call site; `smeldr.dev/mcp/tool.go` adds `authoriseAdmin`, `tokenToolDefs`, `handleTokenTool`, pre-dispatch for token tools in `handleToolsCall`; `tools/list` exposes `create_token`/`list_tokens`/`revoke_token` when store configured (Admin role required). Shipped in v1.6.0 / smeldr.dev/mcp v1.1.0. |
| 2026-04-05 | Amendment A67 (`templatehelpers.go`): `forgeHTML(s string) template.HTML` added — trusted raw HTML passthrough registered as `forge_html` in `TemplateFuncMap`; `TemplateFuncMap` godoc updated; `TestTemplateFuncMap_keys` expected count updated from 8 to 9; `TestForgeHTML` added (3 sub-tests). Shipped in v1.7.0. |
| 2026-04-06 | Decision 26 (`auth.go`, `errors.go`, `smeldr.dev/mcp/tool.go`): `ErrLastAdmin` sentinel (409 `last_admin`) added to `errors.go`; `TokenStore.Revoke` gains pre-check — counts other active admin tokens before revoking; returns `ErrLastAdmin` if count is 0 and target is admin; `smeldr.dev/mcp/tool.go` `revoke_token` surfaces actionable message for `ErrLastAdmin`. Shipped in forge v1.8.0, smeldr.dev/mcp v1.2.0. |
| 2026-04-07 | Decision 27 (`mcp.go`, `module.go`, `smeldr.dev/mcp/mcp.go`): `MCPField.Format string` and `MCPField.Description string` added to `mcp.go`; `mcpStructField` in `module.go` reads `smeldr_format` and `smeldr_description` struct tags; `fieldDescription` helper added to `smeldr.dev/mcp/mcp.go`; `inputSchema` and `inputSchemaUpdate` emit `"description"` key in JSON Schema properties with three-case priority logic (both → description + " (" + format + ")"; format-only → "(format)"; neither → omitted). Shipped in forge v1.9.0, smeldr.dev/mcp v1.3.0. |
| 2026-04-07 | Decision 28 (`smeldr.dev/cli/`): new stdlib-only submodule `smeldr.dev/cli` (`package main`); content CRUD + lifecycle via GET-then-PUT to Forge REST API; token management via MCP JSON-RPC 2.0; YAML-subset frontmatter parser; `Config` from `FORGE_URL`/`FORGE_TOKEN`/`FORGE_MCP_URL` env vars; G23 integration test validates GET→PUT round-trip contract. Tagged `smeldr.dev/cli/v0.1.0`. |
| 2026-04-10 | Fix (`smeldr.dev/mcp/mcp.go`): `inputSchema` and `inputSchemaUpdate` emit `{"type":"string","format":"date-time"}` for `f.Type == "datetime"` fields (`published_at`, `scheduled_at`). Previously emitted invalid `"type":"datetime"`, blocking tool registration in strict MCP clients (VS Code Copilot). Shipped in smeldr.dev/mcp v1.3.1. |
| 2026-04-11 | Decision 29 (`nav.go`, `forge.go`, `templatedata.go`, `templates.go`, `module.go`, `smeldr.dev/mcp/`): NavTree first-class navigation abstraction; NavMode, NavItem, NavTree; App.Nav(), App.NavTree(); TemplateData[T].Nav field; smeldr.dev/mcp nav tools (list/create/update/delete). Shipped in forge v1.10.0 / smeldr.dev/mcp v1.4.0. |
| 2026-04-11 | Decision 30 (`config.go`, `forge.go`): `loadConfigFile`, `mergeFileConfig`; `Config.AppSchema`, `Config.OGDefaults`; `MustConfig` auto-loads `smeldr.config`. Shipped in forge v1.11.0. |
| 2026-04-18 | Decision 31 (`forge.go`, `smeldr.dev/media/`, `smeldr.dev/mcp/`, `smeldr.dev/cli/`): `Config.MediaPath string`, `Config.MediaMaxSize int64`, `App.Config() Config` accessor added to `forge.go` (Amendment A73); new optional submodule `smeldr.dev/media/` — `MediaStore` interface, `LocalMediaStore`, `MediaRecord`, `MediaType`, `CreateMediaTable`, HTTP server (`Server`, `New`, `Register`, `HTTPHandler`), smeldr.MCPModule implementation (`MCPMeta`, `MCPSchema`, `MCPCreate`, `MCPDelete`, `MCPList`, `MCPGet`); `smeldr.dev/mcp`: `WithModule(m smeldr.MCPModule) ServerOption` added (v1.5.0); `smeldr.dev/cli`: media upload, list, delete commands. Shipped in forge v1.12.0, smeldr.dev/media v1.0.0, smeldr.dev/mcp v1.5.0. |
| 2026-05-02 | Amendment A77 (`head.go`, `module.go`, `templates.go`): `listHeadFuncOption[T]` unexported generic type; `ListHeadFunc[T any](fn func(Context, []T) Head) Option` exported option; `listHeadFunc any` field on `Module[T]`; `renderListHTML` resolves list head via type assertion after building TemplateData; `mergeOGDefaults` applied to list head for consistency with show-page behaviour. Fixes empty `<title>` on module list pages. Shipped in v1.14.1. |
| 2026-05-08 | Milestone 11 (v1.17.0): A87 (`signals.go`): `AfterSchedule Signal = "after_schedule"`. A89 (`module.go`): `afterHook` callback field, `setAfterHook`, `notifyAfter`; `MCPSchedule` dispatches `AfterSchedule`. A88 (`forge.go`): `App.Webhooks(*WebhookStore)`, `App.WebhookPool() WebhookJobQueue`, `App.injectWebhookHooks()`; pool started/stopped with server lifecycle. Step 1 (`webhook.go`): `WebhookEndpoint`, `WebhookStore` (AES-256-GCM secret encryption, SSRF validation), `WebhookJobQueue` interface, `Titled` interface, `OutboundJob`, `DeliveryLog`, payload-building helpers, `buildWebhookPayload`, `signalToEventSuffix`. Step 2 (`outbound.go`): `workerPool` with exponential backoff (4^attempt ±20% jitter, max 1h), per-endpoint circuit breaker (threshold 5, open 5min), dead-letter at 7 attempts, HMAC-SHA256 signing, injectable `deliver` func for testing, `fakeClock` test helper. `smeldr.dev/mcp`: `webhookStore` field, 5 Admin MCP tools (`create_webhook`, `list_webhooks`, `delete_webhook`, `list_webhook_deliveries`, `retry_webhook`), `subscriptionRegistry` (fan-out SSE push), `resources/subscribe` + `resources/unsubscribe` JSON-RPC methods, session-ID-based SSE transport, `capabilities.resources.subscribe=true`. `smeldr.dev/cli` v0.4.0: `forge webhook` subcommands (create, list, delete, deliveries, retry). `integration_full_test.go`: G24–G30 cross-milestone groups. |
| 2026-05-08 | Milestone 12 (v1.18.0) / A92: `auth.go`: `encodePreviewToken(prefix, slug string, secret []byte, ttl time.Duration) string` + `decodePreviewToken(token string, secret []byte) (prefix, slug string, err error)` (internal; reuse `tokenHMAC`; constant-time comparison). `forge.go`: `Config.PreviewTokenExpiry time.Duration`, `App.GeneratePreviewToken(prefix, slug string) string`, `App.BaseURL() string`. `module.go`: `secret []byte` field + `setSecret([]byte)` (wired by `App.Content`) + preview bypass block in `showHandler` (checks prefix + slug; falls through silently on failure). `smeldr.dev/mcp/preview_tools.go`: `create_preview_url` Admin tool; `Server.app *smeldr.App` field added to `mcp.go`. `smeldr.dev/cli` v0.5.0: `preview.go` + `smeldr.dev/cli preview <prefix> <slug>`. `integration_full_test.go`: G31 cross-milestone group. |
| 2026-05-11 | Milestone 14 (v1.20.0) / A94: Signal bus. `signals.go`: `SignalEvent` exported struct, `afterHookMeta` unexported struct, `buildSignalEvent` unexported func. `forge.go`: `App.OnSignal(sig Signal, h func(context.Context, SignalEvent) error) *App` (exported), `App.dispatchBus` (unexported), `App.wireSignalBus` (unexported, replaces `injectWebhookHooks`); `App` gains `busMu sync.RWMutex` and `busHandlers map[Signal][]func(context.Context, SignalEvent) error`; `App.Webhooks` refactored to register `webhookDispatch` as `OnSignal` handlers. `webhook.go`: `webhookDispatch` unexported func. `outbound.go`: `OutboundDelivery` exported interface `{ Enqueue(ctx, OutboundJob) error }`. `module.go`: `afterHook` field type, `setAfterHook`, `notifyAfter` signatures extended with `afterHookMeta`; all call sites updated with prevState. `integration_full_test.go`: G32 cross-milestone group. |
| 2026-05-16 | A96 (Non-Decision, docs-only): sitemap ping. REFERENCE.md: "Search engine indexing" section with `App.OnSignal(AfterPublish, ...)` developer pattern. No code changes. |
| 2026-05-16 | A97 (v1.22.0): Built-in opt-in audit trail. `audit.go` (new): `AuditRecord`, `AuditFilter`, `AuditStore` interface, `NewAuditStore(DB)`, `CreateAuditTable(DB)`, `newAuditHandler` (unexported). `forge.go`: `App.Audit(AuditStore) *App`; `App` gains `auditStore AuditStore` and `auditHandlerReg bool` fields; `App.Handler()` lazily mounts `GET /_audit` when `auditStore != nil`. `audit_test.go` (new): 13 unit tests. `integration_full_test.go`: G33 cross-milestone group. `smeldr.dev/cli` v0.9.0: `forge audit list` subcommand. |
| 2026-05-19 | A98 (v1.22.1): Fix data race in `notifyAfter`. `module.go`: `snapshotItem(item any) any` (new unexported func) — shallow reflect copy of the pointed-to struct; `notifyAfter` calls `snapshotItem` once and passes the snapshot to both `dispatchAfter` and the `afterHook` goroutine. Eliminates concurrent read/write on `Node` fields (races G26, G30, G32, G33). No exported symbols changed. |
| 2026-05-19 | A100 (v1.22.2): Go 1.26.3 toolchain bump. `go.mod`: `go 1.26.2` → `go 1.26.3`. Closes GO-2026-4982, GO-2026-4980, GO-2026-4971, GO-2026-4918. No exported symbols changed. |
| 2026-05-22 | A102 (v1.24.0): `APIOnly()` module option — no public HTML surface. `GET /{prefix}` and `GET /{prefix}/{slug}` with `Accept: text/html` return 404. JSON routes and all MCP tools unchanged. `APIOnly()` + `SingleInstance()` panics at startup. `apiOnly bool` field on `Module[T]`; guard added to `listHandler`, `showHandler`, `singleInstanceHandler`. `integration_full_test.go`: G36. `example_test.go`: `ExampleAPIOnly`. |
| 2026-05-23 | A101 (v1.23.0): `SingleInstance()` and `Standalone()` module routing options. `mcp.go`: `MCPMeta.SingleInstance bool` field. `module.go`: `singleInstance bool` + `standalone bool` fields on `Module[T]`; `singleInstanceOption`/`standaloneOption` types; `SingleInstance()`/`Standalone()` exported constructors; `singleInstanceHandler`; `standaloneEnabled()`/`findAndServe()`/`findAndServeAIDoc()` dispatch helpers; `Register()` routing branches; URL generation 3-way branch in `regenerateSitemap`/`regenerateFeed`/`regenerateAI`. `forge.go`: `standaloneDispatcher` internal interface; `App.standaloneModules []standaloneDispatcher` + `App.standaloneReg bool`; `App.Content()` detects standalone modules; `App.Handler()` registers `GET /{slug}` + `GET /{slug}/aidoc` dispatch when standalone modules present. `smeldr.dev/mcp/mcp.go`: `mcpAdminReadToolDefs` suppresses `list_{type}s` when `MCPMeta.SingleInstance` is true. `integration_full_test.go`: G34 (SingleInstance) + G35 (Standalone, two modules). |
| 2026-05-31 | A120 (v1.31.0, T82): `serveblocks.go` reference-field resolution. `blockFieldFormats.refs []string`; `refs:["ImageID"]` on content_block/contact_card/hero; `refIDsOf`; a single batched `IN()` ref-load pass appended to `loadTree` (Published-only); resolve loop in `renderBlock` setting `data[".{Name}"]` = referenced block's `buildData` (`ImageID` → `.Image`). `{{ with }}`-guarded, Published-only, one level, no N+1. Extends A118. 8 tests in `serveblocks_test.go`. |
| 2026-05-31 | A118 (v1.31.0, T32 component 4): `serveblocks.go` (new) — `App.ServeBlocks(dir) (*BlockRenderer, error)` + `BlockRenderer.Render(ctx, pageType, pageID) (template.HTML, error)`. Convention-template rendering engine (`templates/blocks/<type_name>.html`): batched per-level load via `ContentEdgeStore.ChildrenOf` + `Query[*DynamicNode]` IN() (no N+1); cycle protection (visited-set + `maxDepth` 16); graceful degradation (skip+`slog.Warn` for unpublished/missing/dangling/malformed/missing-template/exec-error). Built-in `blockFieldRegistry` (markdown/raw-HTML fields per type_name; interim until c7). Reuses `renderMarkdown`, `TemplateFuncMap`. PascalCase block-`Fields` key convention (AGENTS.md). ContentList deferred (c4b). `serveblocks_test.go`: 24 tests. Held core v1.31.0. |
| 2026-05-31 | A116 (v1.31.0, T32 components 1+2): Block-system data foundation. `blocks.go` (new): `DynamicNode` (embeds `Node`; `TypeName`, `Fields json.RawMessage`) + `Head()`; `NewDynamicContentRepo(db) *SQLRepo[*DynamicNode]` (binds `smeldr_dynamic_content`); `CreateBlockTables(db)` — one idempotent grouped creator for `smeldr_dynamic_content` + `smeldr_content_edges` + `(parent_id, sort_order)` index (T55 Decision 1; `scheduled_at` added so `SQLRepo` reuse works). `edges.go` (new): `ContentEdge`, `ContentEdgeStore`, `NewContentEdgeStore(db)`; `AddChild`/`Children`/`ChildrenOf` (batch `IN()`)/`RemoveChild`/`Reorder` (atomic `CASE`); `is_shared` INTEGER↔bool scan; one edge table for page→block and collection→item (T55 Decision 2). `blocks_test.go` + `edges_test.go` (new): 12 tests against in-memory SQLite. Data layer only — MCP, rendering, seeding are later components. |
| 2026-06-20 | A159 (v1.42.2, T06 step 2): Relation schema + stores. `relations.go` (new): `RelationKindDef`, `RelationEdge` (not embedding Node — graph edge, not content), `RelationKindRegistry` (in-memory, `sync.RWMutex`), `RelationStore`; `CreateRelationTables(db)` — `smeldr_relation_kinds` + `smeldr_relations` + 3 indexes (source, target, governance temporal); `NewRelationStore(db)` (hydrates registry from DB); `ValidateRelationKindDef`; `UpsertKind` (ON CONFLICT type_name DO UPDATE, updates registry atomically); `GetKind`/`ListKinds` (registry only, no DB round-trip); `Assert` (asserted edges only, CAS on id); `GetBySource`/`GetByTarget` (kind="" returns all); `Delete`. `smeldr.go`: `App.Relations(store)` + `App.RelationStore()`. 11 tests in `relations_test.go`. Coverage: 96.1%. |
| 2026-06-20 | A158 (v1.42.1, T06 prerequisite): `Node.Rev` optimistic-concurrency token. `node.go`: `Rev int \`db:"rev"\`` added to `Node` — 0 on first insert, incremented by storage on every subsequent save. `errors.go`: `ErrRevConflict` sentinel (HTTP 409, code `rev_conflict`). `storage.go`: `MigrateNodeRevColumn(db DB, table string) error` — idempotent PRAGMA-probe + `ALTER TABLE … ADD COLUMN rev INTEGER NOT NULL DEFAULT 0`; `SQLRepo.Save`: CAS `WHERE table.rev = $N` + `RowsAffected=0 → ErrRevConflict`; `MemoryRepo.Save`: `incrementRevField` helper increments via reflection on pointer T (no CAS). `blocks.go`, `stats_test.go`, `example/blog/main.go`: `rev INTEGER NOT NULL DEFAULT 0` added to all Node-embedding DDLs. 6 new tests in `storage_sqlite_test.go`. Coverage: 96.0%. |
| 2026-06-19 | A157 (v1.42.0, T72): PageMeta per-path SEO override layer. `pagemeta.go` (new): `PageMeta` struct (`Path`, `MetaTitle`, `Description`, `OGImage`); `PageMetaStore` (backed by `smeldr.DB`); `NewPageMetaStore(db)`; `CreatePageMetaTable(db)` — `smeldr_page_meta` DDL (idempotent, `IF NOT EXISTS`); `Set` (`INSERT OR REPLACE`), `Get` (zero `PageMeta` + nil error on miss), `Delete` (no-op on miss), `List` (ordered by path). `smeldr.go`: `App.PageMeta(store *PageMetaStore) *App`; `App.GetPageMeta(ctx, path) Head`; `Handler()` push loop: `setPageMetaStore(store)` for all `templateModules`. `templates.go`: `setPageMetaStore` + `renderListHTML` auto-populates `data.Head` from store when `listHeadFunc` is nil; `listHeadFunc` takes priority. 14 new tests in `pagemeta_test.go`. Coverage: 96.0%. |
| 2026-06-16 | A153 (v1.41.0, T104 Inc 2): Dynamic content substrate. `dynamic.go` (new): `DynamicTypeRepo` (per-type CRUD: `CreateDraft`/`GetBySlug`/`GetByID`/`List`/`UpdateFields`/`SetStatus`); `titleSlug`/`uniqueSlug`/`nodeToMap`/`writeDynamicJSON` unexported helpers; `PluralSnake(name) string` (exported); `App.DefineContentType(schema) error` (saves schema, registers `TypeDescriptor{Kind:"content"}`, claims URL prefix); `App.DynamicContentRepo(typeName) (*DynamicTypeRepo, error)` (rejects compiled types); `loadDynamicTypes(ctx, db, app)` (boot-time DB load, idempotent); `App.ServeDynamicContent() *App` (panics without `Config.DB`; registers public `GET /{slug}` + `GET /{seg1}/{seg2}` and 5 admin `/_content/*` routes). `schemas.go`: `ValidateSchemaDef` (exported from `validateSchemaDef`). `registry.go`: `All()` covered by new tests; `RegisterPrefix` idempotent path covered. 90+ new tests in `dynamic_test.go` + `dynamic_app_test.go`. Coverage: 96.0%. |
| 2026-06-08 | A137 (v1.36.1): `processScheduled` save-error handling. `return err` on `repo.Save` failure replaced with `slog.Warn + continue` — a single failing item no longer halts remaining scheduled items in the same tick. `scheduler.go`: capture + log errors from `processScheduled` (was silently ignored). `scheduler_test.go`: `TestProcessScheduled_continuesAfterSaveError` + `failOnSaveRepo[T]` helper. |
| 2026-06-05 | A128 (v1.36.0, T79): In-memory log capture + `GET /_logs`. `logcapture.go` (new): `LogEntry` (exported wire type); `logRing` (bounded overwrite-oldest ring, `sync.Mutex`, monotonic `seq` + `dropped`, `snapshot` newest-first); `teeHandler` (`slog.Handler`; `Enabled = inner.Enabled \|\| level>=min` so stderr is never narrowed; `WithAttrs`/`WithGroup` carry attrs+groups to both paths); `App.CaptureLogs(opts ...LogCaptureOption) *App`; `LogCaptureOption`, `WithLogCapacity` (default 500), `WithLogLevel` (default WARN); `newLogTee` + `bridgesToLog` (substitutes a stderr text handler for the built-in `*slog.defaultHandler` to avoid a fatal slog/log re-entrancy loop, since `slog.SetDefault` repoints the log package); `logsResponse` + `newLogsHandler` (unexported; Admin role; query `level`/`limit`/`since`; envelope `{capacity,count,dropped,entries}`). `forge.go`: `App` gains `logRing *logRing` + `logsHandlerReg bool`; `CaptureLogs` stores the ring; `App.Handler()` lazily mounts `GET /_logs` when `logRing != nil` (absent → 404). `logcapture_test.go` (new): 14 unit tests. `integration_full_test.go`: G37 cross-feature group (+ M1 auth). HTTP/CLI-only by design (no MCP tool); ephemeral live-debugging, not log storage. |
| 2026-07-01 | A186 (v1.46.0, T23 Step 12): `ConflictPolicy` type + `ConflictReject`/`ConflictSupersede` constants (`state.go`); `StateFlow.ActiveState` + `StateFlow.ConflictPolicy` optional fields (zero value = no enforcement); `migrateStateFlowConflictColumns` (new, `migrate.go`) — PRAGMA-probe adds `active_state`/`conflict_policy` TEXT NOT NULL DEFAULT '' columns to `smeldr_state_flows`, idempotent, fail-open on non-SQLite, called from `migrateStateFlows`; `RegisterFlow` UPDATE persists both fields after INSERT OR IGNORE + SELECT id; `applyConflictPolicy` (new, unexported) — SQLite-only; probes sqlite_master; looks up flow by typeName; if toState==ActiveState dispatches to `conflictRejectCheck` (COUNT; ErrConflict if >0) or `conflictSupersede` (collects IDs via `conflictIDs`, UPDATE to "superseded", optional `rs.Assert("supersedes")`); auto-detects static vs `smeldr_dynamic_content` table; all DB errors fail-open. Wired into `Module[T]` `MCPPublish`/`MCPSchedule`/`MCPArchive` and `DynamicTypeRepo.SetStatus` after `validateTransition`. 30+ new tests in `state_test.go`. Coverage: 96.0%. |
| 2026-06-30 | A185 (mcp v1.25.0, T23 Step 11): `signal_tools.go` (new in smeldr/mcp) — `signalToolDefs()`, `isSignalTool()`, `handleSignalTool()`. `create_signal`: INSERT into smeldr_signals (status=pending, slug=signalSlug(sender,signalType,id)), Author role, -32603 on exec error. `list_signals`: SELECT by receiver+state (default pending), fail-open on "no such table", Author role. Both gated on DB != nil. `tool.go` dispatch block added between state tools and dynamic content tools. 16 tests in signal_tools_test.go; coverage 96.0%. core dep v1.45.0 → v1.45.1. |
| 2026-06-30 | A184 (v1.45.1): Fix data race in `state_test.go` under `go test -race`. `TestFireAsyncTriggers_asyncTrigger_dispatched` and `TestDynamicTypeRepo_SetStatus_fireAsyncTriggers` used a bare `bytes.Buffer` as the slog handler target; `fireAsyncTriggers` writes to it concurrently from a spawned goroutine. Replaced with `safeBuf` (mutex-protected wrapper implementing `io.Writer`) in three test functions. No production code changed. Coverage: 96.0%. |
| 2026-06-30 | A183 (v1.45.0, T23 Step 10): `type Signal string` renamed to `type LifecycleEvent string` in `signals.go` — frees `Signal` as a content-type name. All constant names unchanged (AfterCreate, AfterPublish, etc.). All function signatures updated across `signals.go`, `audit.go`, `module.go`, `smeldr.go`, `webhook.go`, and 5 test files. `orchestration.go` (new): `Signal`, `Task`, `Decision`, `Amendment` content types (all embed `Node`); `CreateOrchestrationTables(db DB) error` creates 4 SQLite tables (`smeldr_signals`, `smeldr_tasks`, `smeldr_decisions`, `smeldr_amendments`); `RegisterOrchestrationTypes(app *App, db DB)` registers all 4 types with `MCP(MCPRead, MCPWrite)` and 4 custom state flows (fail-open on nil DB); 4 unexported flow builders (`orchSignalFlow` 4 states, `orchTaskFlow` 9 states, `orchDecisionFlow` 5 states, `orchAmendmentFlow` 6 states). 8 tests in `orchestration_test.go`. `smeldr.dev/agent` v0.6.2, `smeldr.dev/social` v0.9.2, `smeldr.dev/mcp` v1.24.2: `smeldr.Signal` → `smeldr.LifecycleEvent` in all consumer files. Coverage: 96.0%. |
| 2026-06-29 | A176 (v1.44.1, T23 Step 3): `validateTransition` (unexported) added to `state.go` — checks (flow_id, from_state, to_state) in `smeldr_transitions`; falls back to default flow when no custom flow registered; returns `ErrConflict` (409) on disallowed transition; identity transitions (from==to) always pass; nil DB and non-SQLite return nil. `dynamic.go` `DynamicTypeRepo.SetStatus`: calls `validateTransition` after `GetByID`, one DB read; `newSetStatusHandler`: enum switch removed — empty status → 400, disallowed transition → 409 via `errors.Is(ErrConflict)`. `module.go` `Module[T]`: `db DB` (unexported) + `setDB(DB)` — same wiring pattern as `setSecret`; `MCPPublish`/`MCPArchive`/`MCPSchedule` each call `validateTransition` before `setNodeStatus`. `smeldr.go` `App.Content`: type-assertion wire for `setDB`. 12 new tests in `state_test.go`; 1 updated test in `dynamic_app_test.go`. Coverage: 96.0%. |
| 2026-06-29 | A175 (v1.44.0, T23 Step 2): `state.go` (new) — `StateFlow`, `State`, `Transition` exported types defining a data-driven state machine for a content type; `App.RegisterFlow(StateFlow) error` — idempotent upsert of flow/states/transitions via INSERT OR IGNORE + SELECT id pattern (consistent with `migrateStateFlows`); `validateFlowItems` (unexported) — SQLite-only unknown-state validation: queries sqlite_master for table existence, then `SELECT DISTINCT status NOT IN (...)` against the type's table, returns error listing unknown states or nil when DB is not SQLite or table does not yet exist. 12 tests in `state_test.go`. |
| 2026-07-02 | A191 (v1.51.0, T49 Step 3): `governance.go` — `RoleStore.ToolPolicy(ctx, toolName) (requiredOp, found, err)` exact-match lookup in `smeldr_tool_policies`; seam between core and smeldr.dev/mcp. `module.go` — `roleStore *RoleStore` field + `setRoleStore(*RoleStore)` wired from App.Handler(); `canReadDrafts(ctx)` 3-branch (nil store → legacy HasRole(Author); store+no ID → deny; store+ID → Authorized); `checkWriteOp(ctx, op, legacyRole)` same 3-branch; `isVisible` converted from standalone func to `(m *Module[T]) isVisible(ctx, item)` method; all role-check call sites updated; §5.5 fail-closed on Authorized error. `smeldr.go` — `governanceModules []interface{setRoleStore(*RoleStore)}` field on App; `App.Content` registers modules; `App.Handler` injects RoleStore into all modules. |
| 2026-07-02 | A190 (v1.50.0, T49 Step 2.5): `governance.go` — `GovernanceAuditRecord` exported struct (ID, ActorTokenID, Action, TargetKind, TargetID, Before, After JSON strings, CreatedAt); `GovernanceAuditStore` interface (`Append(ctx, GovernanceAuditRecord) error`); `sqlGovernanceAuditStore` + `NewGovernanceAuditStore(db) GovernanceAuditStore`; `CreateGovernanceAuditTable(db) error` — creates `smeldr_governance_audit` + `idx_governance_audit_actor` (opt-in, NOT in migrateGovernance); `RoleStore.WithAudit(actorTokenID string, log GovernanceAuditStore) *RoleStore` — shallow copy with audit wired; `DefineRole`/`Grant`/`Revoke` query before-state, run mutation, then call `auditStore.Append`; fail-closed on Append error (non-atomic — mutation already took effect; callers should verify state on error). 15 new tests; coverage 96.0%. |
| 2026-07-02 | A189 (v1.49.0, T49 Step 2): `governance.go` extended — `RoleDefinition`, `RoleGrant`, `AuthTarget` exported structs; `RoleStore` + `NewRoleStore(db)`: `DefineRole` (upsert; rejects trust_level=1), `Grant` (WHERE NOT EXISTS for NULL anchor), `Revoke`, `ListGrants`, `Authorized` (pre-collects rows before processing to avoid SQLite nested-connection deadlock; dynamic scope filters `edge_class='asserted' AND (invalid_at IS NULL OR invalid_at > now)`; static scope matches `TypeName+":"+ID` — not slug); `App.Governance(store)` validates `store.db == cfg.DB` then runs `migrateGovernance`; `App.RoleStore()` accessor. `smeldr.go`: `governance *RoleStore` field on `App`. 55 tests; coverage 96.0%. |
| 2026-07-02 | A188 (v1.48.0, T49 Step 1): `governance.go` (new) — `ScopeMode` type (`ScopeGlobal`/`ScopeStatic`/`ScopeDynamic` constants); `migrateGovernance(ctx, db)` — creates `smeldr_roles`, `smeldr_role_grants`, `smeldr_tool_policies` tables + two indexes (`idx_role_grants_token`, `idx_role_grants_role_anchor`); `seedDefaultRoles` (author/editor/admin, full-word operations JSON arrays, scope_mode='global', trust_level=0); `seedToolPolicies` (one row per built-in MCP tool → required_op word, zero behaviour change); `migrateTokenGrants` (SELECT smeldr_tokens, lookup role, INSERT via WHERE NOT EXISTS guard — SQLite NULL-in-UNIQUE makes INSERT OR IGNORE unreliable for global-scope grants); fail-open in `migrateGovernance` when smeldr_tokens absent. NOT wired into `New()` — opt-in via `App.Governance()` (T49 Step 2). |
| 2026-07-01 | A187 (v1.47.0, T23 Step 13): `state.go` — `TransitionTrigger` exported type (`FromState`, `ToState`, `TriggerClass`, `TriggerType`, `Config`); `StateFlow.Triggers []TransitionTrigger` field persisted by `RegisterFlow` (idempotent SELECT COUNT guard); `fireAsyncTriggers` extended with `itemID string` parameter; `schedule-eval` trigger handler reads `eval_field` from item row, INSERTs into `smeldr_eval_queue` (fail-open); `resolveItemTable(ctx, db, typeName) string` (sqlite_master probe: `smeldr_<snake>s` → `<snake>s` → `smeldr_dynamic_content`); `isNoSuchTable(err) bool`; `App.DrainEvalQueue(ctx) (triggered, skipped int, err error)` — SELECT due rows, direct UPDATE, DELETE each row regardless; fail-open on nil DB and missing table. `migrate.go`: `smeldr_eval_queue` table with `UNIQUE(type_name, item_id, to_state)` added to `migrateStateFlows()`. `dynamic.go`: `fireAsyncTriggers` call updated with item `id`. `orchestration.go`: `orchDecisionFlow()` wired with two `TransitionTrigger` entries (`proposed→ratified` and `pending-re-evaluation→ratified`). `smeldr.dev/agent/sweep.go`: `NewEvalQueueScheduler(schedule, timezone string, app interface{DrainEvalQueue}) (*SweepScheduler, error)`. |
| 2026-07-04 | A197 (T121): `example/server/` added — standalone Go module (`module example/server`) with own `go.mod` and `replace` directives for all smeldr.dev/* dependencies. Deployable reference binary with no hard-coded Go content types; all content types defined at runtime via `define_content_type` MCP tool. 11 `ENABLE_*` env vars gate optional subsystems (governance, relations, dynamic content, blocks, media, social, webhooks, redirects, page meta, agents); `OAUTH_ISSUER` enables OAuth 2.1. `migrateDB(db)` inlines `smeldr_tokens` and `smeldr_webhook_endpoints` DDL unconditionally (idempotent; no DDL helpers exist in core for these tables). Wiring order load-bearing: `CreateRelationTables` before `NewRelationStore`; `agentMod.Register` before `mcp.New`. `go.work` gains `use ./example/server` (gitignored). No exported Go symbols changed in core. |
| 2026-07-05 | A202 (v1.54.0, T122 T104 Phase B): `schemas.go` — `ValidateFields(schema *ContentTypeSchema, fields map[string]any) *ValidationError` (create path: rejects unknown fields, missing required fields, type mismatches); `ValidatePartialFields(schema *ContentTypeSchema, patch map[string]any) *ValidationError` (update path: rejects unknown fields and type mismatches; absent required fields not checked). `dynamic.go` — `DynamicTypeRepo.ScheduleContent(ctx, id, scheduledAt) error` (transitions to Scheduled status with state-flow enforcement via `validateTransition`; updates `scheduled_at`; fires async triggers); `CreateDraft` and `UpdateFields` now call `ValidateFields`/`ValidatePartialFields` respectively; `App.DynamicContentRepo(typeName)` now calls `repo.WithGovernance(a.governance)` when governance is wired (required-role enforcement for `SetStatus`/`ScheduleContent` flows through App accessor); `loadDynamicTypes` wires `llmsStore` compact fragment for types with URLPrefix; `rebuildDynamicAIIndex` regenerates `/llms.txt` compact fragment after dynamic content changes. Coverage: 96.1%. |
| 2026-07-06 | A204/A205/A206 (T125): `example/server/` refactored — `ServerConfig` (24 env-var fields), `ServerResult` (App, MCP server, TokenStore, StopAll), `parseConfig`, and `buildApp(cfg, db) (ServerResult, error)` extracted from monolithic `main()`. `buildApp` returns errors for all subsystem failures (no `log.Fatalf` inside it). `main_test.go` added (package main, `TestServerToggles` 7 sub-cases: each ENABLE_* toggle verified in-process). `preflight_test.go` added (`//go:build preflight`; builds binary, spawns OS process, polls `/_health`, probes `/goals`). `go.mod` bumped: core v1.52.2→v1.54.0, mcp v1.26.1→v1.28.0. Bug fixes: A205 — `orchestration.go` `RegisterOrchestrationTypes` now passes explicit `Table("smeldr_...")` options to all 5 orchestration `NewSQLRepo` calls (derivation produced e.g. "goals" not "smeldr_goals"); A206 — `smeldr.go` `App.Relations()` now calls `CreateSchemaTable(a.cfg.DB)` (idempotent, nil-guarded) so `smeldr_content_type_schemas` exists before `syncSaveHook` fires, fixing "no such table" errors when `ENABLE_RELATIONS=true, ENABLE_DYNAMIC_CONTENT=false`. Coverage: 96.0%. |
| 2026-07-12 | A215 (T147 Part 1): `example/server/` — `App.ContextPacketHandler` wired when both `ENABLE_RELATIONS` and `ENABLE_ORCHESTRATION` are set. `ServerConfig.InstanceName string` added (25th field); `INSTANCE_NAME` env var (default: `smeldr-dogfood`). Two `EnableRelations` blocks consolidated into one (hoisted `var rs *smeldr.RelationStore`; `CreateRelationTables` moved into the combined block). `TestServerToggles`: 7→9 sub-cases (`on/contextPacket`, `off/contextPacketWithoutRelations`). No exported core symbols changed. Level 1 amendment. |
| 2026-07-19 | A219 (T153): Reachability as a general platform primitive. `reachability.go` (new) — `ReachabilityItem`, `ReachabilityRing`, `Reachability` exported types; `MaxReachabilityDepth = 10` constant; `RelationStore.Reachability(ctx, anchorType, anchorID, kind, direction string, maxDepth int) (*Reachability, error)` — bounded breadth-first traversal over the relation graph from any anchor type/id (not hardcoded to `context_packet.go`'s 5 orchestration types), one ring per hop 1..maxDepth, a ring with zero items is a genuine reportable absence, not an error or omission; `reachabilityNode`/`reachabilityNeighbors` unexported helpers reuse `RelationStore.GetBySource`/`GetByTarget`. Built as its own primitive rather than extending `governance.go`'s `ScopeDynamic`/`relationExists` — shape mismatch (ring data vs. boolean) and risk isolation from the fail-closed authorization path (design rationale in `decisions/recent.md`). 15 new tests in `reachability_test.go`, 100% coverage on both new functions. Coverage: 96.1%. Level 2 amendment. |
| 2026-07-27 | A220 (T149): Transition provenance. `provenance.go` (new) — `ProvenanceRecord`, `ProvenanceFilter` exported types; `ProvenanceStore` interface; `NewProvenanceStore(DB)`; `CreateProvenanceTable(DB)`; `App.Provenance(store ProvenanceStore) *App` subscribes to the 7 completed-transition signals (`AfterCreate`, `AfterUpdate`, `AfterPublish`, `AfterUnpublish`, `AfterSchedule`, `AfterArchive`, `AfterDelete`) via the existing signal bus and writes one `ProvenanceRecord` per event through `recordProvenance` (fail-open — logs and swallows store errors, mirroring `App.Audit`'s own discipline); `provenanceVerbFor`/`currentStatusOf`/`actorKindFor` unexported helpers. Purely additive: `AuditRecord`/`AuditStore`/`App.Audit`/`GET /_audit` are untouched — `smeldr_provenance` is a new, separate table, not a replacement (design doc's original "`ProvenanceRecord` replaces `AuditRecord`" framing was rejected in planning as a violation of the `CHANGELOG.md` v1.0.0 API stability promise; the 4 events `App.Audit` already covers now also get a `ProvenanceRecord` entry from the same call site — accepted as deliberate redundancy, not competing truths). `relations.go` — `RelationStore.provenanceStore` field + `setProvenanceStore`, wired from `App.Handler()` regardless of `App.Relations()`/`App.Provenance()` call order; `insertEdge` calls new `recordAssertProvenance`, which recovers the calling actor via a `ctx.(Context)` type assertion (same precedent as `relations.go`'s existing `txBeginner` assertion and `dynamic.go`'s `smeldrCtxAccessor`) — no signature change to `Assert`/`MCPAssertRelation`/`MCPProposeRelation`, no `smeldr.dev/mcp` changes required; `CreatedByJob` takes priority over a ctx-derived human actor. `state.go` — `Transition.RequiredReason bool` field; `validateTransition` gains a final `reason string` parameter (unexported function, no compatibility concern) and a new fail-closed check ahead of the existing `RequiredRole` check: `required_reason` set and `reason == ""` → `ErrBadRequest`. `migrate.go` — `migrateTransitionReasonColumn` adds `smeldr_transitions.required_reason` (idempotent `PRAGMA table_info` probe, same shape as `migrateStateFlowConflictColumns`). `dynamic.go` — `DynamicTypeRepo.SetStatus` refactored into a thin wrapper over new unexported `setStatus`; new `SetStatusWithReason(ctx, id, status, reason string) error` is the one concrete entry point in this task's scope that can satisfy a `RequiredReason` gate (added rather than changing `SetStatus`'s signature, preserving the stability promise). Known gap, flagged at commit time: `MCPPublish`/`MCPSchedule`/`MCPArchive`/`updateHandler` and the `smeldr.dev/mcp` `transition_item` tool still cannot supply a reason — full end-to-end `RequiredReason` reachability from MCP is deferred, not silently dropped. 30+ new tests across `provenance_test.go` (new), `relations_provenance_test.go` (new), `state_test.go`, `dynamic_test.go`; coverage 96.1%. Level 2 amendment. |
| 2026-07-27 | A221 (T149 hotfix): `migrate.go`'s `smeldr_transitions` CREATE TABLE statement now declares `required_reason BOOLEAN NOT NULL DEFAULT FALSE` directly, matching the `active_state`/`conflict_policy` precedent on `smeldr_state_flows`. A220 added the column only via the SQLite-only `migrateTransitionReasonColumn` ALTER-TABLE migration, so a fresh Postgres install never got it — caught by CI's `Test (pgx integration)` job (`TestIntegration_Postgres_StateFlows`: "column required_reason does not exist"), not locally, since core's own `go test ./...` never builds the separate `core/pgx` submodule. `migrateTransitionReasonColumn`'s godoc corrected to describe its actual role: upgrading pre-existing SQLite databases only. No exported symbols, no test changes (existing test comments already assumed the fixed behaviour). v1.57.0 → v1.57.1. Level 1 amendment. |
| 2026-07-28 | A222 (webhook/outbound TIMESTAMPTZ scan fix, Level 2): `modernc.org/sqlite`'s driver only auto-converts `DATE`/`DATETIME`/`TIMESTAMP` columns to `time.Time` on scan, not `TIMESTAMPTZ` (exact decltype match). `webhook.go`'s `List`/`EndpointsForEvent` and `outbound.go`'s `fetchDueJobs`/`ListJobsForEndpoint`/`ListDeliveryLogs` scanned `TIMESTAMPTZ`-documented columns (`created_at`, `next_retry_at`, `expires_at`, `attempted_at`) directly into `time.Time`, hard-erroring on any DB following the documented DDL — `fetchDueJobs` is the core worker-pool delivery-loop poll, so production outbound webhook delivery was non-functional against any compliant SQLite database. Masked by `webhook_test.go`/`outbound_test.go`/`integration_full_test.go`'s G26–G29 groups all declaring test tables as `DATETIME` instead of `TIMESTAMPTZ` — confirmed the driver keys off the declared column type name, not the stored value's format. Fix: all 5 non-nullable scan sites wrapped in `scanDest()` (A200's existing `timeScanner`, reused not duplicated); `DeliveryStats`'s nullable `MAX(dl.attempted_at) *time.Time` return reuses `timeScanner{dst:&t}.Scan(...)` directly on the scanned string (a plain `time.Parse(time.RFC3339, ...)` — the architect's first-suggested, `TokenStore`-mirroring shape — was tried and empirically failed: outbound.go's writes are never pre-formatted to RFC3339 the way `TokenStore.Create` explicitly does, so the driver's real stored string, `"2026-07-28 17:58:07.7222132 +0000 UTC"`, only matches `timeScanner`'s existing broader layout list, confirmed via a throwaway debug test before committing to this shape). Root cause of the raw string's shape: `realClock.Now()` (outbound.go) and `webhookDispatch`'s `now := time.Now()` (webhook.go) returned local, monotonic-clock-bearing time instead of `.UTC()` like every other timestamp-generation site in this codebase — fixed at the source (`realClock.Now()` now returns `time.Now().UTC()`) rather than patched at every call site. Test DDL corrected to `TIMESTAMPTZ` in all 3 files; `datetime('now')` SQL-literal inserts (8 occurrences across `webhook_test.go`/`integration_full_test.go`) replaced with real parameterized `time.Now().UTC()` values matching how production code actually writes these columns; `newFakeClock(time.Now())` call sites (6, across `outbound_test.go`/`integration_full_test.go`'s G27–G29) corrected to `.UTC()` for the same reason. New regression test `TestWorkerPool_DeliveryStats_realAttempt` (exercises the real Enqueue→processJob→DeliveryStats round trip — proved the fix rather than assuming it). No exported symbols changed. Coverage: 96.1%. v1.57.1 → v1.57.2. |
| 2026-07-28 | A223 (T176, Level 2): Finish the Forge→Smeldr rename — `core/pgx` submodule + remaining prose sweep, found during an awesome-go submission readiness review. `pgx/pgx.go`+3 test files: `package forgepgx` → `package pgx` (matches the module's own import path, `smeldr.dev/core/pgx`, and every other renamed companion package's convention — T61/v0.1.0 renamed the directory and module path but missed the package declaration itself); `forgepgx.Wrap` → `pgx.Wrap`; `TestWrap_compilesAsForgeDB` → `TestWrap_compilesAsSmeldrDB`; `runForgePgxRepoParity` → `runPgxRepoParity`; temp table `forgepgx_test` → `smeldr_pgx_test` (not a persisted-migration concern — a `CREATE TEMP TABLE` scoped to one test's connection lifetime). `pgx/v0.2.0` tagged, breaking-minor (package-qualifier change), matching A129's own classification for the oauth rename; `pgx/CHANGELOG.md` added (didn't exist before). Six real bugs found during the sweep, fixed in the same commit: `doc.go`'s godoc `[Signal]` cross-reference silently resolved to the wrong type after A183 renamed `Signal`→`LifecycleEvent` and reused `Signal` for an unrelated orchestration content type — fixed to `[LifecycleEvent]`; `ai.go`'s godoc example showed a dead template function name (`forge_llms_entries`, real name `smeldr_llms_entries`) — copy-paste produced a template parse error; the actual runtime-generated `/llms-full.txt` footer string ("Generated by Forge on...") changed to "Generated by Smeldr on..." (real product output, not a comment — `ai_test.go` assertion updated in lockstep); `example/docs/main.go`'s embedded struct-tag code sample used the dead `forge:"required"` tag key instead of the real `smeldr:"required"` (A107) — silently produced zero validation if copy-pasted, no error; `smeldr_test.go`'s doc comment claimed a `"forge"` `/_health` version key that doesn't exist (real key: `"core"`) — `TestApp_health_forgeVersion` renamed `TestApp_health_coreVersion` to match; four `WARN  forge:` stderr log-prefix strings (`auth.go`'s `basicAuthWarn`, `smeldr.go`'s TokenStore/NavTree warnings) corrected to `smeldr:`, matching the `panic("smeldr: ...")` two lines away in the same function. Remaining sweep: `doc.go`/`storage.go`/`benchmarks_test.go` prose; `example/docs/main.go`'s ~30-occurrence prose pass; full sweep of `smeldr.go` (27 occurrences, heaviest single file — includes `forgeVersions`→`smeldrVersions`, `"forge:head"`→`"smeldr:head"` reserved-name doc), `module.go` (`mcpParseForgeTag`→`mcpParseSmeldrTag`), `mcp.go`, `context.go`, `errors.go`, `head.go`, `nav.go`, `node.go`, `outbound.go` (prose only), `relations.go`, `sitemap.go`, `social.go`, `templatedata.go`, `templatehelpers.go`, `schema.go`, `markdown.go`, and ~15 test files (mostly `TestForgeXxx`→`TestSmeldrXxx` identifier renames, zero external impact — all unexported/test-only). Preserved, not touched: `migrate.go`'s `migrateLegacyTableNames` and its 7 literal `forge_*`→`smeldr_*` table-rename pairs (real production-migration compatibility, not branding); the `X-Forge-Signature`/`X-Forge-Timestamp`/`X-Forge-Event`/`X-Forge-Delivery` HTTP headers in `outbound.go`/`outbound_test.go` (T86/T87 deprecation window, explicitly documented as intentional); `mcp.WithForgeFallback()` (external `smeldr.dev/mcp` module's own preserved API, called by `example/server/main.go`); `example/server/seed_goals/main.go`'s historical/planning seed-data narrative. No exported Go symbols changed in `core` itself; two consumer-observable changes (the `/llms-full.txt` footer text, the log-prefix fixes) justify a patch bump. Coverage: 96.1%. v1.57.2 → v1.57.3. |
| 2026-07-29 | A224 (T178, Level 2): Wire `App.Provenance()` into a real instance; fix `ActorKind` for human/job/agent. `example/server` gains `ENABLE_PROVENANCE`/`ServerConfig.EnableProvenance` (same convention as `ENABLE_RELATIONS`). New exported `Job`/`Agent` `Role` constants (`roles.go`), deliberately unregistered in the permission hierarchy — classification tags via `IsRole`, not `HasRole`. `SignalEvent.ActorRoles []Role` (full role set, captured synchronously in `buildSignalEvent`); `actorKindFor(actorID, roles []Role)` resolves "job"/"agent"/"human"; `App.Provenance()`'s handler reads `ev.ActorRoles` directly — an originally-proposed `ctx.(Context)` recovery design was implemented, tested, and empirically confirmed broken first (`dispatchBus`'s `context.WithoutCancel`/`WithTimeout` wrapping strips `smeldr.Context`'s method set) before this redesign. `relations.go`'s `recordAssertProvenance` shares the same role-aware helper; `CreatedByJob`'s override unchanged. Independent fix: `App.Handler()` now also calls `wireSignalBus()` (previously only `App.Run()` did) — closes a real, previously-silent gap for any caller embedding `http.Handler` directly instead of calling `Run()`; deliberately re-entrant, not run-once, since `Content()` can add modules after an early `Handler()` call. One incidental pre-existing bug found and flagged separately (T180): `MCPCreate` hardcodes status `"draft"`, conflicting with every orchestration type's own non-`"draft"` `IsInitial` state. Coverage: 96.1%. v1.57.3 → v1.58.0. |
| 2026-07-29 | A225 (T180, Level 2): Fix `MCPCreate`'s hardcoded `Draft` default breaking all 5 orchestration types via MCP (the A224 incidental finding, dispatched as its own task). Root cause: `MCPCreate` forced an omitted status to the literal `Draft` constant, then `validateInitialState` (A216) correctly rejected it as not a registered state in the type's own flow (`Signal`→`"pending"`, `Task`→`"backlog"`, `Decision`→`"proposed"`, `Amendment`→`"scoped"`, `Goal`→`"open"`). Checked the equivalent HTTP path (`createHandler`) directly rather than assuming it had "the same hardcoding" — it didn't: it has no status-defaulting at all, so an omitted status silently persisted the literal empty string `""`, worse than MCPCreate's loud failure since `""` isn't a valid state or transition `from_state` in a custom flow either, leaving the item permanently stuck with no error. New `defaultInitialState(ctx, db, typeName) string` (`state.go`) looks up the type's own registered `IsInitial` state (`smeldr_states.is_initial`), fail-open; new `applyDefaultStatus` (`module.go`) shared by both `createHandler` and `MCPCreate`, falling back to `Draft` only when no custom flow is registered. Rejected an alternative (a module-registration default-status hook) as a second, driftable source of truth duplicating data already in `smeldr_states`. No exported symbols changed. `example/server`'s previously-failing `TestServerToggles/on/orchestration`/`on/orchestrationWithRelations` now pass; the A224-introduced `on/provenance` test's `"status": "open"` workaround removed as a third proof angle. Coverage: 96.1%. v1.58.0 → v1.58.1. |
| 2026-07-29 | A226 (T179, Level 2): Fix `updateHandler`'s double-`Save` on publish-via-PUT, deterministically breaking `example/blog`'s `TestBlogSignal`/`TestBlogFullServer/audit/recordedOnPublish` with a 409 `rev_conflict`. Root cause: on a non-Published→Published transition, `updateHandler` called `m.repo.Save` once, then set `PublishedAt` and called `m.repo.Save` a second time on the same in-memory `item`. `SQLRepo.Save`'s rev-based CAS (`WHERE rev = $N`, Amendment A158) increments the stored `rev` without writing the new value back into the caller's struct — Save #1 succeeds and the DB's `rev` advances, but Save #2 still carries the old, now-stale `rev`, so its own `WHERE rev = $N` matches nothing and returns `ErrRevConflict`, every time, not intermittently — `updateHandler` returned the 409 before `AfterPublish` was ever dispatched. Traced via `git log -S`: Amendment A48 (2026-03-15) introduced the double-`Save` correctly, before any CAS existed; A158 (2026-06-20) silently broke it three months later for an unrelated reason, never re-verified against A48's own code path. Checked the other two `setNodeTime(..., "PublishedAt", ...)` call sites (`processScheduled`, `MCPPublish`) — both already set the field before their one `Save` call, never affected; `dynamic.go`'s `setStatus` uses a single hand-written `UPDATE` with no rev-CAS at all, also unaffected — `updateHandler` was the only broken site. Fix: merged into one `Save` call, matching `processScheduled`/`MCPPublish`'s own shape (and literally what A48's own code comment already claimed to do). Removed `TestModule_updateHandler_secondSaveError` and its sole fixture `secondSavefailRepo` — the second-`Save`-fails scenario they tested becomes structurally impossible, not merely untested; `TestModule_updateHandler_saveError` continues to cover the merged `Save`'s error path. New `TestModule_updateHandler_publishSetsPublishedAtOneSave` proves the fix against a real `SQLRepo`-backed repo (not `MemoryRepo`, which can't exercise the rev-CAS at all) — asserts 200, `PublishedAt` set, and `AfterPublish` actually fires. No exported symbols changed. Coverage: 96.1%. v1.58.1 → v1.58.2. |
| 2026-07-30 | A229 (T181, Level 1): Fix `PublishedAt` never stamped when an item is created directly with status `"published"` via `createHandler` (HTTP) or `MCPCreate` (MCP) — both already had status-aware branches for this case (slug-collision check, `AfterPublish` hook) but never wired `PublishedAt` in. RSS `PubDate`/sitemap `LastMod` showed a wrong (zero) date on public-facing surfaces for directly-published items. Also empirically checked, and refuted, a suspected second half of the bug: NEXT.md guessed a follow-up `publish_post`/`MCPPublish` call on the already-published item would be rejected by `validateTransition` (`published→published`); reproduced directly and confirmed `validateTransition`'s `fromStatus == toStatus` early return means it's never gated, and `MCPPublish` already unconditionally restamps `PublishedAt` on every successful call — no second bug exists. Fix: new `nodePublishedAtOf(v) time.Time` + `stampPublishedAt(item)` (`module.go`), called right after item construction in both `createHandler` and `MCPCreate`, before `RunValidation`. Deliberately IsZero-guarded (preserves a caller-supplied `PublishedAt`, e.g. a data-import path) and deliberately *not* reused at the 4 existing transition-time `PublishedAt` call sites (`updateHandler`, `MCPPublish`, `MCPSchedule`'s scheduler path, `processScheduled`) — those unconditionally overwrite `PublishedAt` on every transition into Published, including republish, and the IsZero guard would silently change that. No exported symbols changed. Coverage: 96.1%. Patch bump (consumer-observable: directly-published items' API responses now include a non-zero `PublishedAt`). v1.58.2 → v1.58.3. |
| 2026-08-02 | A231 (T194, Level 2): Thread `EdgeClass`/`Confidence` through `Reachability`'s output — `ReachabilityItem` gains `EdgeClass string`/`Confidence *float64`, previously discarded during traversal (`reachabilityNode`/`ReachabilityItem{Type, ID}` only). Checked directly, not assumed: `smeldr.dev/mcp` has no `Reachability` exposure at all (grepped the whole repo) — this is a core-only change, not "across both repos" per `T193`'s own estimate. Real design gap surfaced while implementing: when a node is reachable via more than one edge at the same hop distance, the BFS's node-dedup previously kept whichever edge was processed first, with no defined order — invisible while nothing read per-edge metadata, live nondeterminism the moment `EdgeClass`/`Confidence` started riding along. Fix: new `reachabilityCandidate`/`edgeClassRank`/`betterCandidate` (unexported) implement a deterministic precedence — `asserted` > `observed` > `inferred`, then higher `Confidence` (nil treated as lowest) — applied by collecting all of a depth's candidate edges per new node before finalizing the ring, rather than committing on first discovery. Existing behaviour (one node per ring, shortest-distance-wins, ring order not guaranteed) unaffected; only which edge's metadata is attached when there's a choice. 4 new tests in `reachability_test.go` proving the base case and all three tie-break tiers. No exported symbols removed, two new struct fields added (backward-compatible JSON, `confidence` uses `omitempty`). Coverage: 96.1%. Patch bump (consumer-observable: new JSON fields on `Reachability`'s existing response shape). |
| 2026-08-02 | A232 (T196, Level 2): Implement `EdgeClass`'s `"observed"` value, per `T193`'s own resolved recommendation (`design/edge-class-observed-spike.md`) — no re-litigation of either judgment call. New `MCPObserveRelation` (`relations.go`), mirroring `MCPAssertRelation`/`MCPProposeRelation` exactly, storing `edge_class="observed"`. Deliberately no `governance.go` change (dynamic-scope check stays asserted-only) and no `RecomputeAsserted`/`BulkRecompute` change (diff scope stays asserted-only) — both confirmed no-ops, not oversights, matching the spike's own resolved judgment calls. `smeldr.dev/mcp` gains a matching `observe_relation` tool (`relation_tools.go`) mirroring `propose_relation`, plus `"observed"` added to `get_relations`' `edge_class` filter enum — a separate, mcp-repo-only consequence of this same amendment, not requiring its own core-repo doc entry. 3 new tests in `relations_mcp_test.go`. No exported symbols changed. Coverage: 96.1%. Patch bump (consumer-observable: new write path). |
| 2026-08-07 | A235 (D35, M2, Level 2): `lineage.go` (new) — `LineageNode`, `LineageTrace` exported types; `MaxLineageDepth = 10` constant; `RelationStore.TraceLineage(ctx, anchorType, anchorID string, maxDepth int) (*LineageTrace, error)` — bounded, read-time upstream traversal over `depends_on`/`derives_from` edges. Reuses `reachability.go`'s BFS shape (`reachabilityNode`, `edgeClassRank`, `betterCandidate`) rather than `edges.go`'s `ChildrenOf` — a closer, previously-unnoticed precedent in the same package over the same `RelationEdge` type, flagged and approved before implementation — while adding the real batched `IN (...)`-per-depth-level query `reachability.go` itself lacks (`batchEdgesBySource`/`batchEdgesByTarget`/`batchEdgesByTypeAndIDs`, grouped by node type to stay portable across SQLite/pgx, not a composite row-value `IN (...)`). Resolves D35's own open question: a visited node that was itself superseded is followed to its replacement, recorded at the same `Depth` (a lateral step, not an extra upstream hop — supersede history is revision metadata about identity, not a new premise), on the same reasoning D35 guard 3 already accepted for invalidated edges (followed, not stopped at, flagged via `LineageNode.Invalidated`). Two real bugs found and fixed during implementation: `Truncated` was a false positive whenever the frontier was merely non-empty at `maxDepth`, regardless of whether those nodes had further edges — fixed with `hasFurtherLineage`, a one-time non-recording peek at the depth boundary; `followSupersedes` originally resolved only one hop of a supersede chain, missing a Decision's actual current replacement after more than one revision — fixed by looping the chain walk until no new replacement is found, then capping it at `MaxLineageDepth` hops (reusing the main walk's ceiling) since chain length was otherwise the only unbounded part of the traversal. No anchor-existence check (matches `Reachability`'s own precedent; `resolveItemTable`, the only existing type-to-table lookup, is confirmed SQLite-only, so using it here would be a real pgx portability bug, not just an unnecessary coupling); no MCP tool (`smeldr.dev/mcp` is a separate repo, out of this task's scope, matching `Reachability`'s own T194/T196 core-first split). 20 new tests in `lineage_test.go`, 100% coverage on every new function. Coverage: 96.2%. Level 2 amendment. |
| 2026-08-03 | A233 (T200, Level 2): Fix `RoleStore.Grant`/`Revoke`'s non-atomic audit-write gap, found during `T199`'s spike. `NEXT.md`'s own suggested fix (mirror `applyRelationDiff`'s `txBeginner` pattern directly onto `RoleStore.db`) was checked before implementing and found insufficient: `GovernanceAuditStore` is an exported, pluggable interface, and the bundled `sqlGovernanceAuditStore` holds its own separately-bound `db DB` field — wrapping only `RoleStore.db` in a transaction would not have pulled `auditStore.Append`'s write into it. Real fix: new unexported `txGovernanceAuditAppender` optional interface (`appendTx(ctx, exec DB, r) error`), implemented by `sqlGovernanceAuditStore` alongside its existing `Append` (both now delegate to a shared `appendAuditRecord` helper — no duplication). `Grant`/`Revoke` type-assert for it; when present *and* the DB supports `txBeginner`, the state-changing write and the audit append share one transaction — true atomicity for the common/default case, closing A190's own documented "non-atomic" caveat. Falls back to the prior sequential behaviour, honestly unimproved, for a custom non-SQL `GovernanceAuditStore` or a non-transactional DB — not silently claimed fixed for those. Incidental second bug closed as a side effect: `Grant`'s grantID-resolve `SELECT` failing after a successful `INSERT` previously orphaned the row with no way to learn its ID; now rolled back like everything else in the same transaction. `DefineRole` has the identical shape but is out of this amendment's scope (matches `NEXT.md`'s own title) — flagged as a same-shape follow-up. 4 new tests use a real (not mocked) SQL failure — a genuine `sqlGovernanceAuditStore` pointed at a DB missing the audit table — to exercise the actual rollback; existing `failAppendAuditStore`-based tests stay unmodified since that fixture doesn't implement `appendTx` and correctly continues to exercise the fallback path. One accepted, named coverage gap: `commit()` failing after already-successful statement execution isn't deterministically testable against SQLite in-memory without a custom driver mock. No exported symbols changed. Coverage: 96.1% (`Grant` 96.3%, `Revoke` 95.0%, new helpers 100%). Patch bump (consumer-observable: `grant_role`/`revoke_grant` no longer silently mutate state while reporting failure, in the common case). |
| 2026-08-07 | A234 (T206, Level 2, D34): Decision authority enforcement. `validateTransition`'s `required_role` gate was real and working but unset on `Decision`'s `proposed→ratified`/`ratified→superseded` transitions, and had three fail-open escape hatches that made even a set `required_role` unreliable — not a missing capability, an unset column plus an unresolved fail-open branch. New `Transition.Strict bool` (`state.go`) + `smeldr_transitions.strict` column (additive, `migrateTransitionStrictColumn` mirrors `migrateTransitionReasonColumn`'s idempotent PRAGMA-probe shape) — when true, a nil `RoleStore` or an empty `actorID` now reject with `ErrForbidden` instead of allowing through; false (every pre-existing transition) is unchanged. Separately, `validateTransition`'s transitions-row query erroring for a reason other than `sql.ErrNoRows` now returns `ErrInternal` instead of `nil` — global, not conditional on `Strict` — since that branch runs before `Strict` is ever consulted; shipping the column alone would not have closed the gap for a `strict=TRUE` transition under a transient DB error. `orchDecisionFlow()`'s two governance transitions (`orchestration.go`) now carry `RequiredRole: "admin"` (the highest of the three built-in seeded roles, zero new provisioning burden) and `Strict: true`. `RequiredReason` deliberately left unset on both: `updateHandler`, the only call site that can reach a `Decision` status change today, hardcodes `reason=""` with no passthrough — `RequiredReason: true` here would have made both transitions permanently unreachable through the only path that reaches them, not more auditable; flagged as a separate, not-yet-scoped follow-up rather than worked around. New `decisionScopeRoles map[string]string` (unexported, starts empty) + `authorizeDecisionScope` (`orchestration.go`) implement D34 §6's scope-to-role wrapper as a second, per-instance authorization layer alongside the generic `RequiredRole` gate — `RequiredRole` is one value shared by every `Decision` row regardless of its own `Scope` field, which a future per-scope policy needs and `RequiredRole` alone cannot express. Wired into `updateHandler` (`module.go`) directly after the existing `validateTransition` call, checked against `existing` (the item's current, pre-update state), not the newly decoded `item` — `updateHandler` decodes a full-replace PUT body, so checking `item` would let a caller's own `Scope` value (changed or simply omitted and zeroed) steer which role the check requires; a dedicated test (`TestModule_updateHandler_decisionRatify_scopeForbidden`) caught this during implementation before it shipped. `decisionScopeRoles` ships empty — no scope-to-role policy has been decided yet (D34 §12 is explicit that grant-provisioning is a separate, still-open question) — so this call site is a no-op for every `Decision` today; the `RequiredRole: "admin"` gate above is what actually enforces authority now. Found and confirmed, not fixed here (named follow-ups): no MCP tool can change a `Decision`'s status at all today — `MCPUpdate` restores the existing status after decode, and `Decision` is a real `NewModule[*Decision]` type, not T32 dynamic content, so `transition_item`/`DynamicTypeRepo.SetStatus` doesn't reach it either; `RegisterFlow`'s transition upsert is `ON CONFLICT DO NOTHING`, so re-registering an already-registered flow with a changed `RequiredRole`/`RequiredReason`/`Strict` silently does not update the stored row (pre-existing limitation, `Strict` merely inherits it). No exported symbols removed or changed signature; `Transition.Strict` is additive. Coverage: 96.1% (`validateTransition`, `authorizeDecisionScope`, `orchDecisionFlow` 100%; `migrateTransitionStrictColumn` 89.5%, matching `migrateTransitionReasonColumn`'s own precedented shape). Patch bump (consumer-observable: `PUT /decisions/{slug}` ratify/supersede now requires the `admin` role by default, where it was previously unenforced). v1.58.5 → v1.58.6. |
| 2026-08-07 | A236 (D36, M0 step 2, Level 2): `orchestration.go` — new `RegisterOrchestrationRelationKinds(ctx context.Context, store *RelationStore) error`, registering the four relation kinds D36 already ratified: `derives_from` (Task→Goal), `depends_on` (Task→Task), `ships_as` (Task→Amendment), `supersedes` (Decision→Decision), all `Mode="asserted"`/`Directional=true`/`Weighted=false`. Process note, not a code consequence: this task's own NEXT.md framed those four field values as an open design question still to be argued from the design docs; independent analysis (each kind traced to a deliberate act by a named actor at an identifiable workflow moment — never system inference or an agent proposal pending review) converged on values identical to D36's, but D36 (commit `7db0eb6`, already on `main` before this task started) had in fact already settled them — a citation miss caught before commit, not a re-litigated decision; this entry and the `decisions/recent.md` body cite D36 as controlling. Confirmed directly (not assumed): `RelationKindDef.Mode` is validated but not otherwise enforced anywhere in the codebase today — `edge_class` on a written edge is chosen entirely by which of `MCPAssertRelation`/`MCPProposeRelation`/`MCPObserveRelation` is called, independent of the registered kind's `Mode` — so today this is a documented default character for each kind, not yet a live gate; `TypePairs` is read only by `ValidateRelationKindDef`'s JSON-shape check, never by `insertEdge` or any write path, safe to populate descriptively. The fifth candidate kind named in the design docs and explicitly excluded by D36, `implements` (Amendment→Decision), stays unregistered. Wired into `example/server/main.go`'s existing `if cfg.EnableRelations && cfg.EnableOrchestration` gate (previously only `app.ContextPacketHandler`), called with `context.Background()` (the function has no other `ctx` in scope) before `ContextPacketHandler`. Exported specifically so the permanent M0-step-3 instance (`self-hosting-build-plan.md` step 3) calls the same function rather than duplicating the four literal definitions. 2 new tests in `orchestration_test.go`: round-trip (all four kinds' `Mode`/`Directional`/`Weighted`/`TypePairs` asserted against `ListKinds()`, plus a second call proving idempotency) and one `UpsertKind`-failure path (reusing the existing `mockRelationStore`/`errExecDB` fixtures from `relations_errors_test.go`) — a single, linear error-return path reused across the four-kind loop, not four distinct branches, so no error-path table was needed. No exported symbols changed elsewhere. Coverage: 96.2% (`RegisterOrchestrationRelationKinds` 100%). MINOR bump (new exported API surface, same classification as A235/`TraceLineage`). v1.59.0 → v1.60.0. |
| 2026-08-07 | A237 (M0 step 3, Level 2): closes a real, previously-latent gap in D34's own enforcement story, found while planning the permanent self-hosting instance. `TokenStore.ensureBootstrap` (A83) already auto-creates a `bootstrap-admin` token with `role="admin"` when `smeldr_tokens` is empty, but that field only satisfies the generic Role hierarchy (Guest<Author<Editor<Admin, decoded from the JWT) — `validateTransition`'s D34 Strict gate calls `RoleGranted`, which reads `smeldr_role_grants` only and never consults `smeldr_tokens.role`. Traced further, not assumed: `RoleStore.Grant`'s `TokenID` must equal the JWT's own `User.ID` (embedded at signing time), a value distinct from `smeldr_tokens.id` (the SHA-256 fingerprint used as the token's primary key) — the two were computed independently and never cross-referenced anywhere in the codebase; `TokenStore.Create`'s existing signature never surfaced the JWT's `User.ID` at all, and no production code anywhere called `RoleStore.Grant`. Without this fix, the auto-created bootstrap token could do everything except ratify or supersede a `Decision` — exactly the operation the new instance exists to support. Fix: new unexported `createToken` (`auth.go`) factors `Create`'s existing logic into a shared helper returning `(raw, userID string, err error)`; `Create`'s exported signature and behaviour are unchanged (delegates, discards `userID`); `ensureBootstrap`'s signature changes (unexported, non-breaking) from no return value to `(userID string, created bool)`. `App.Handler()` (`smeldr.go`) extends the existing A83 bootstrap block: when a token was freshly created and `App.Governance` is wired, grants it the `admin` role via `RoleStore.Grant(ctx, RoleGrant{TokenID: userID, RoleName: "admin"})` — fail-open with a `slog.Warn` on error, matching every other branch in that block. Confirmed sufficient with no other `RoleGrant` fields: `seedDefaultRoles` already seeds `admin` with `scope_mode='global'`, and `RoleGranted`'s `ScopeGlobal` case matches unconditionally. This bootstrap grant is a break-glass safety net, not a substitute for provisioning an operator's own token (`self-hosting-build-plan.md` step 4, separate and later). Rejected alternatives, argued in the plan: a one-time setup script (an artifact to remember to run) and a documented manual step (exactly the kind of thing NEXT.md's own item 4 warned gets missed) — both lose to the idempotent-at-boot shape `ensureBootstrap`/`seedDefaultRoles` already established as this project's precedent. 5 tests changed/added: `TestTokenStore_ensureBootstrap_empty`/`_nonEmpty`/`_createFails` extended to assert the new return values; new `TestApp_Handler_bootstrapGrantsAdmin` (end-to-end: after `Handler()`, `RoleGranted` on the created token returns `true`) and `TestApp_Handler_bootstrapGrantFails` (a `RoleGrant` INSERT failure doesn't panic, fail-open). One named, accepted coverage gap: `createToken`'s `SignToken`-error branch (91.7% function coverage) is structurally unreachable — the same pre-existing `json.Marshal`-on-a-string/[]string/int64-struct branch `encodeToken`'s own doc comment already calls "unreachable in practice," now visible under a new function name rather than inlined inside `Create`; not a regression, package coverage unchanged at 96.2%. No new binary and no new env var needed — `example/server` already supports the required `ENABLE_TOKENS`+`ENABLE_GOVERNANCE`+`ENABLE_RELATIONS`+`ENABLE_ORCHESTRATION` combination as-is (argued in the plan against a new binary or a build-flag/mode-switch, both rejected as unnecessary given zero code changes were required to reuse the existing config-driven binary). SQLite chosen over pgx/Postgres for this instance's DB backend — matches the box's existing proven backup pattern, avoids being the first live test of `resolveItemTable`'s documented (A235) SQLite-only gap in the relation/lineage code this instance depends on. No exported symbols changed (`Create`'s signature/behaviour unchanged; `ensureBootstrap`'s signature change is unexported-only). Coverage: 96.2% (`ensureBootstrap` 100%, `createToken` 91.7% — the one named, accepted gap above; `Create` 100%). Patch bump (consumer-observable: a fresh boot with `ENABLE_GOVERNANCE` set and an empty `smeldr_tokens` table now also grants the bootstrap token the `admin` RoleStore role, where it previously silently could not ratify/supersede a `Decision`). v1.60.0 → v1.60.1. |
| 2026-08-08 | A239 (D38, M3 part 1, Level 2): `orchestration.go` — new sixth orchestration type, `Run`, one row per mechanical episode of headless automated work (claim → merge-or-abandon). `RunOutcome` (string enum: `merged`/`needs-resync`/`stuck`/`failed`/`orphaned`, empty while in-flight, every named value terminal) and `RunCleanupState` (`pending`/`done`) follow `ConflictPolicy`'s own named-string-type precedent. `Run` deliberately registers no `StateFlow` — its authoritative state lives in `LeaseHolder`/`Outcome`, guarded by `SQLRepo.Save`'s rev-CAS, never in the inherited `Node.Status`/`validateTransition` (D38 explicitly names `smeldr/agent`'s `AgentJob` as the wrong, natural-looking precedent to copy — that type routes its lifecycle through a real `StateFlow`; `Run` does not). Every `Run` row stays `Draft` for its entire life (nothing ever calls `publish_run`) — confirmed this doesn't hide anything from a listener's reads, since `MCPList` applies no draft-visibility filtering of its own. `CreateOrchestrationTables`/`RegisterOrchestrationTypes` extended in place (six tables/types now, five flows still — no `orchRunFlow`) rather than a parallel `Run`-only pair, matching D38's own "a sixth typed orchestration module" framing; both doc comments updated. `Run` gets `MCP(MCPRead, MCPWrite)` like its five siblings — not just for consistency: D38's own claim/renewal/land-time-holder-check writes all travel the MCP `update` path (`mcp/tool.go`'s `handleToolsCall` → `MCPUpdate`), so write access is a real requirement for the not-yet-built M3 listener, not a style preference. `AcknowledgedAt` is `time.Time` (zero value = not yet acknowledged), not `*time.Time` as first drafted in the plan — verified directly before building, not assumed: `storage.go`'s `scanDest` only special-cases a scan-destination whose type is `*time.Time`, which is what a field of Go type `time.Time` naturally addresses to; a field declared `*time.Time` addresses to `**time.Time`, unhandled by `scanDest`, and would fail to parse SQLite's string-formatted `TIMESTAMPTZ` values on read. Fixed to match `Decision.NextEvalAt`'s own established zero-value convention instead. **Caught a self-contradicting citation, and a real second finding, during architect review**: the plan's own comment cited `Node.ScheduledAt` alongside `NextEvalAt` as a second "already-proven" precedent — but `Node.ScheduledAt` is itself `*time.Time`, not a `time.Time` precedent at all. Investigated directly rather than just deleting the bad citation: reproduced `scanDest`'s exact failure mode empirically against a real `SQLRepo`/SQLite (a throwaway test, deleted, not committed) — a nil `ScheduledAt` round-trips fine (`database/sql`'s nil-source path needs no string parsing), but a non-nil one fails with a real `Scan` error. Every existing test that round-trips a non-nil `ScheduledAt` uses `NewMemoryRepo`, never `NewSQLRepo` — the bug has no test coverage in the one repo implementation that would actually hit it. **Flagged as a likely real, previously-unnoticed latent bug in `Node.ScheduledAt`, not fixed here** — out of this task's scope, named as its own follow-up. Rev-echo (the discipline every lease-touching write must follow, D38 §3) is documented directly on `Run`'s own godoc as a contract the future listener must uphold — explicitly not something this task builds, enforces, or can test: the failure mode lives entirely in how a future caller constructs its update payload, and this task builds no such caller. What is tested instead: `TestRun_SaveRevConflict` proves `Run` genuinely participates in `SQLRepo.Save`'s real CAS (mirrors `TestSQLRepo_Save_RevConflict`'s exact pattern) — a real claim this task's own code makes, not a substitute for the untestable one. `TestRegisterOrchestrationTypes_flows` gained a load-bearing assertion beyond the unchanged flow count: a direct check that zero `smeldr_state_flows` rows exist for `type_name='Run'`, since an unchanged count alone couldn't distinguish "Run correctly has no flow" from "Run's module registration was silently forgotten." `AGENTS.md`'s orchestration-types section also corrected from "Four built-in types" (a pre-existing staleness — `Goal`, added by A198, was never added to that table either) to six, with both `Goal` and `Run` added and a new paragraph warning against gating on `Run.status`. Companion mcp-repo prerequisite (A238, `smeldr.dev/mcp`, no core-package files touched, no entry here per A227/A228's own precedent): `ErrRevConflict` gets its own JSON-RPC code, `-32002`, distinct from `ErrConflict`'s `-32001` — argued in the plan why sharing the existing `-32001` bucket wouldn't actually let a listener branch on code alone. No exported symbols removed anywhere. Coverage: 96.2% (unchanged). MINOR bump (new exported API surface: `Run`, `RunOutcome`, `RunCleanupState`, and their constants — same classification as A235/A236). v1.60.1 → v1.61.0. |
| 2026-08-11 | A251 (D49, Level 2, smeldr/core half): new `App.TransitionItem(ctx, typeName, slug, toState) (map[string]any, error)` (`state.go`) — extends flow-validated state transitions to compiled types, closing the gap `transition_item`/`get_valid_transitions`/`list_items_by_state` left after A248 (cleanly rejecting a compiled type is not the same as operating on one; M0 step 7's signal protocol needs the latter). Branches on `TypeRegistry().Lookup(typeName).Kind`: `"content"` delegates unchanged to `DynamicContentRepo`/`SetStatus` (zero behavioural difference for existing dynamic-content callers); `"compiled"` resolves the table via `resolveItemTable` (D42/D47's own reused helper), runs the identical `validateTransition` call `updateHandler` and `SetStatus` already make (same D34/D40 authority gate, not bypassed — the same call already enforcing it), then a raw `UPDATE … SET status = …`, mirroring `applyConflictPolicy`'s own `conflictSupersede` precedent rather than inventing a new raw-update shape. Chose this (D49) over a new required `MCPTransition` method on the exported `MCPModule` interface specifically to avoid a breaking change for any implementer that isn't `Module[T]` itself — Go interface satisfaction has no default-method escape hatch, so a new required interface method breaks every independent implementer, where a new `App` method breaks nothing. **Rev behaviour stated explicitly, per D49's own boundary requirement**: the compiled-type raw `UPDATE` does not read or advance `Node.Rev` — a concurrent `Save` holding the item's pre-transition rev still satisfies `Save`'s CAS check (the stored rev is unchanged) and silently overwrites the status change with whatever status its own in-memory item carries. Not a new risk: `SetStatus` and `applyConflictPolicy`'s own supersede path already make this exact choice for their raw status updates; this extends it to a new table class rather than introducing it. Fires `fireAsyncTriggers` (A240) after a successful compiled-type update, mirroring `setStatus`'s own only hook — does not fire `notifyAfter`/`AfterPublish`-class signal-bus hooks for either branch, matching existing dynamic-content behaviour symmetrically rather than creating a new asymmetry. A compiled type whose backing table can't be found (partial migration, an unregistered module) returns a distinct error instead of silently falling into the dynamic-content branch — D47's own guard against exactly that ambiguity, applied here too. The unregistered-type error now wraps `ErrBadRequest` (new `errorFor` mapping in the paired `smeldr.dev/mcp` Amendment, A252) rather than a bare error, closing a real pre-existing gap: `validateTransition`'s `RequiredReason` case already returned `ErrBadRequest` with no JSON-RPC mapping for it anywhere. 17 new tests (`state_transition_item_test.go`) covering the full error-path table: unregistered type, not found (both branches), invalid target state, transition not permitted, role-gated forbidden/granted, DB nil, compiled-type table missing, transition-row query failure (D34 fail-closed), UPDATE exec failure, conflict-policy violation, and the item-lookup SELECT failing. No exported symbols removed. Coverage: 96.3% package-wide; `TransitionItem` 94.7% — the `DynamicContentRepo`/`repo.List` error branches inside the already-passed `Kind == "content"` check are structurally unreachable through the real dispatch path (state tools require `Config().DB != nil` before dispatch, and `Kind` cannot change between the two checks), named rather than chased. MINOR bump (new exported API surface, same classification as A235/A236/A239). v1.63.3 → v1.64.0. |
| 2026-08-13 | A256 (T235, Level 2, smeldr/core half): new `App.TransitionItemWithReason(ctx, typeName, slug, toState, reason string) (map[string]any, error)` (`state.go`), mirroring `DynamicTypeRepo.SetStatus`/`SetStatusWithReason`'s exact shape — a second method rather than a signature change, so `App.TransitionItem` (now a thin wrapper passing `""`) keeps its existing signature and behaviour for every current caller. Corrected two of the dispatching task's own assumptions during investigation: `App.TransitionItem` did not already carry a reason internally, and no REST path (`updateHandler`/`MCPPublish`/`MCPSchedule`/`MCPArchive`/`newSetStatusHandler`) has ever threaded one either — every `validateTransition` call site in the repo passed a hard-coded `""`, meaning `Transition.RequiredReason` (A220) has been unreachable from any entry point since it was added, not a `transition_item`-specific gap. Dynamic branch now calls `SetStatusWithReason` instead of `SetStatus`; compiled branch passes the real `reason` into `validateTransition`. The four `module.go` REST call sites still pass `""` — deliberately out of scope, tracked as T237 (a wire-format question, not a parameter-threading one). New exported symbol. Coverage: 96.3%; `TransitionItem` 100%, `TransitionItemWithReason` 97.4% (one already-named structurally-unreachable branch, A251's own precedent, not new). MINOR bump v1.64.1 → v1.65.0. |
| 2026-08-11 | A253 (T227, Level 2): `SQLRepo.Save` (`storage.go`) built a separate addressable copy for the DB write and never wrote the incremented `rev` — or even `UpdatedAt`/`CreatedAt` — back onto the caller's own struct; a successful `Save` left `item` untouched. Confirmed live on `process.smeldr.dev` during M0 step 6 (`PUT /signals/{slug}` responded `"Rev": 0`, a re-read showed `"Rev": 1`) and diagnosed as the root cause A226 had already found a symptom of (`updateHandler`'s double-`Save` on publish) without fixing the contract itself. Fix: `Save` now appends `RETURNING "rev"` and uses `QueryRowContext` instead of `ExecContext` — the database-computed post-write `rev` comes back in the same round trip, no extra query; `sql.ErrNoRows` on the scan replaces `RowsAffected() == 0` for `ErrRevConflict` detection, same sentinel. `UpdatedAt`/`CreatedAt` (already known in Go before the query runs) write back unconditionally on success via new unexported `writeBackTimestamps`. Write-back is guarded by `src.CanSet()` — a value-type `T` (already discouraged by `NewSQLRepo`'s own doc comment) degrades to a no-op, not a panic. First use of `RETURNING` anywhere in `smeldr/core`; safe on both SQLite (`modernc.org/sqlite`, RETURNING since 3.35) and PostgreSQL (native, long-standing). No signature change — `Save(ctx, item T) error` unchanged; Level 2 because of the real behaviour change with consequences in more than one file: two existing tests, `TestSQLRepo_Save_RevConflict` and `TestRun_SaveRevConflict`, previously passed *because of* the bug (relying on the caller's `Rev` staying stale to manufacture their own conflict scenario) and were rewritten with a deliberately-held-stale separate copy — which also now directly asserts the write-back. `Node.Rev`'s and `Run`'s (D38 §3) own doc comments each gained one clarifying sentence; D38 §3's own listener-payload-construction concern stays separate, open, and unclosed by this task. 5 new tests, 100% coverage on `Save`. Coverage: 96.3% package-wide. Patch bump (consumer-observable: `Save` callers — including every `Module[T]` handler and any direct `SQLRepo` user — now see the real post-write `Rev`/timestamps on their own struct with no re-read). v1.64.0 → v1.64.1. |
| 2026-08-10 | A249 (D48, T224, Level 1, smeldr/core half): `seedToolPolicies` (`governance.go`) gains two rows, `get_goal_context` → `"read"` and `list_type_tools` → `"read"`. Both are framework discovery tools, not module-generated ones (no compiled `Module` backs either type name), so D48's mcp-side verb-derived fallback (the companion `smeldr.dev/mcp` half of this same Amendment pair) can never rescue a missing row for them — `authoriseTool`'s not-found branch fails closed regardless, exactly as it did for every one of the six orchestration types' own generated tools before D48. Verified live on `process.smeldr.dev` before this fix: a token holding a real admin grant got `-32001 forbidden` calling either tool, with no policy row to inspect and no way for an operator to grant access short of writing a raw SQL insert. `AGENTS.md` already documented `get_goal_context` as `Author`-gated (A199) — this fix makes the seeded policy match what was already published, not a new behavioural promise. New `TestRoleStore_ToolPolicy_OrchestrationDiscoveryTools` asserts both tools resolve to `found=true`/`op="read"` through the real `RoleStore.ToolPolicy` path. No exported symbols changed. Coverage: 96.2%. Patch bump (consumer-observable: two previously-forbidden-for-everyone tools become usable at the documented Author tier). v1.63.2 → v1.63.3. |
| 2026-08-10 | A248 (T225, Level 2): Fix `DynamicContentRepo`'s "compiled type" guard, which existed since A153 but never once fired for a real compiled `Module` — `App.Content()` (`smeldr.go`) registered every compiled module's `TypeDescriptor` with `Kind: "content"`, the same value `DefineContentType` uses for genuine runtime-defined types, so `DynamicContentRepo`'s `Kind != "content"` check (`dynamic.go:421`) always passed and built a `DynamicTypeRepo` bound to `smeldr_dynamic_content` for a type that was never stored there. Reproduced locally, not reasoned about: a real in-process instance with `RegisterOrchestrationTypes` called and no dynamic content ever configured, `get_valid_transitions` on a real `Signal` through `handleToolsCall`, returned `-32603 internal error: SQL logic error: no such table: smeldr_dynamic_content` — the exact symptom reported against `process.smeldr.dev`. Traced past the symptom to the mislabeling, not just the missing table: `TestDynamicContentRepo_CompiledType_Rejected` (`dynamic_app_test.go`) had sat skipped for this exact reason since A153, unable to inject a `Kind: "block"` descriptor through the public API — the real public path had never produced one. Fix: `Kind: "content"` → `Kind: "compiled"` at the one `App.Content()` registration site. `TypeDescriptor.Kind`'s field comment (`registry.go`) updated from `"block" | "content"` to `"block" | "content" | "compiled"`; `"block"` is untouched, joined by a distinct third value rather than reused for a case it never accurately described. Value argued, not assumed: every one of `dynamic.go`'s `Kind != "content"` guards is a pure inequality — any non-`"content"` value satisfies all of them identically, so `"block"` would have worked too, but `dynamic.go`'s `define_content_type` HTTP handler and `smeldr.dev/mcp`'s matching MCP tool both echo `desc.Kind` back in their response; checked both and confirmed neither can leak a compiled module's `Kind` today only because `DefineContentType` unconditionally forces `schema.Kind = "content"` regardless of input — coincidental safety, not structural, and the kind of small permanent falsehood this project keeps finding and removing elsewhere. `TestDynamicContentRepo_CompiledType_Rejected` un-skipped: now registers real orchestration types and asserts `DynamicContentRepo("Signal")` returns the existing `"is a compiled type; use its module directly"` error instead of skipping. This one-line fix alone makes `transition_item`/`get_valid_transitions`/`list_items_by_state` (`smeldr.dev/mcp`) return that same clean error for any compiled type instead of crashing, through a mechanism that already existed and was simply never reached — closing T225 without touching `smeldr.dev/mcp`. Whether these three tools should be extended to actually operate on compiled types (`resolveItemTable`, D42/D47's own reused helper, plus the fully generic `validateTransition` that `DynamicTypeRepo.setStatus` already calls before its one dynamic-content-specific line) is a separate, argued-but-deliberately-unshipped question — real capability gap for M0 step 7, own future dispatch, not risked by bundling into this fix. No exported symbols removed; `TypeDescriptor.Kind`'s documented value set gained a member. Coverage: 96.2%. Patch bump (consumer-observable: three MCP tools stop crashing on any compiled orchestration type). v1.63.1 → v1.63.2. |
| 2026-08-10 | A247 (Level 1): `App.SweepStructural`'s built-in default `TargetChecker` queried only `smeldr_dynamic_content`, `status = 'published'`; it never knew about any compiled `Module` type's own table, so wiring it unchanged on an instance with compiled types (e.g. all six orchestration types) would conclude every relation target is gone and invalidate the entire lineage graph on first run — the sweep never running was the only reason the graph stayed intact. New unexported `defaultTargetChecker(db DB) TargetChecker` (`smeldr.go`): for any type `resolveItemTable` resolves to its own compiled table, alive means the row exists, full stop — no status, terminal or otherwise, means "gone." Argued per type, not assumed: none of the six built-in flows' states (including every terminal one — `Decision`'s `superseded`/`archived`, `Amendment`'s `rejected`, etc.) represent "this never happened," only "reached this point"; a `Decision` that reached `superseded` is still the record of a real decision, and `archived` is bookkeeping, not retraction — no different from `superseded`. `Run` needs no special case: never consulting `Status` for a compiled type is exactly why it's also safe for `Run`, which never leaves Draft (D38, A239). `Signal`'s `expired` state initially looked like the least certain case; on review this dissolved rather than survived — `SweepStructural` asks a structural question (does the target still exist), a status answers a semantic one, and answering the former with the latter is a category error regardless of which status is picked. Second, sideways hazard closed in the same fix: `resolveItemTable`'s own fallback to `smeldr_dynamic_content` when it finds no dedicated table is correct for a genuine dynamic-content type but indistinguishable, from a bare "no row" result alone, from a compiled type whose table is simply absent (partial migration, an unregistered module) — both would otherwise reach the same status lookup and return false. Guarded directly: before trusting the dynamic-content branch, `targetType` must be confirmed present in `smeldr_content_type_schemas` (`kind='content'`); when it isn't, or that table can't be queried, `defaultTargetChecker` returns an error, which `RelationStore.SweepStructural` already treats as skipped, not stale — a sweep that cannot tell must decline, never delete. Replaces `App.SweepStructural`'s own default rather than shipping as an opt-in constructor or `example/server`-only wiring — an opt-in tool doesn't reach a consumer who doesn't know they're at risk, which is the exact failure this fixes; `resolveItemTable`'s own fallback design means the corrected default covers any compiled `Module` type automatically, not just the six built-in orchestration ones. 9 new tests (`smeldr_target_checker_test.go`, `relations_sweep_test.go`) — compiled-target alive/deleted, the two named checks (superseded and archived `Decision` keep their relations), the compiled-table query-error path, both ways the registry lookup can fail (unregistered type, registry table itself missing), and a genuine dynamic-content deletion regression — every one driven through `App.SweepStructural`, never the checker directly, per the explicit check criterion: a checker correct in isolation but never reached by the real entry point is the same defect class as the one this fixes. No scheduler, no cron, no call site invoking the sweep on a timer — out of scope, confirmed against the dispatch's own exclusions. `SweepStructural`'s return values unchanged. Coverage: 96.3% package-wide; `defaultTargetChecker` 90.9% — two DB-failure leaves in the unchanged dynamic-content branch (`QueryContext`/`Scan` errors) left uncovered, the same class already exercised for the compiled-table branch, named rather than silently left. No exported symbols changed. Patch bump — a real, consumer-observable fix to `App.SweepStructural`'s existing default behaviour, not new API surface. |
| 2026-08-10 | A246 (T217, Level 1): `example/blog`, `example/api`, `example/docs` bumped `go 1.26.3`/`1.26.2`/`1.26.2` → `go 1.26.5`, matching core's own root `go.mod` — Go's toolchain auto-switch keys off the *main* module's own `go` line, not any `replace` target's, so a main module declaring below what its replaced target requires never triggers a switch and fails outright (`module ../.. requires go >= 1.26.5`) — `example/server`'s own `go 1.26.5` was luck, not design, per A245. `example/blog/go.mod` additionally drops `replace smeldr.dev/mcp => ../../../mcp` (a path outside this repo, absent in any clone of `smeldr/core` alone) in favor of a real pin, `smeldr.dev/mcp v1.30.0` — confirmed load-bearing first, not a stale import: `example/blog/main.go` calls `mcp.New(app)`. Establishes the pattern `example/server` already embodies as an explicit rule: an in-repo dependency (`smeldr.dev/core`) gets a relative `replace`; an out-of-repo dependency gets a real version pin. **Named, not closed: a pin is only as safe as the isolation check that verifies it, and no such check runs automatically yet** — A245's own core pin went stale for a month specifically because nothing built it outside `go.work`; `smeldr.dev/mcp`'s new pin in `blog` carries the identical risk until a repeatable isolation build exists (this task's own manual verification is not that). Verified by real isolation, not `go.work`: shallow clone into a tempdir outside the `Smeldr/` directory tree entirely (no sibling `mcp`/`agent`/`media`/`oauth`/`social` checkouts reachable by relative path, no `go.work` findable by upward search), `GOWORK=off go build ./...` in all four example directories — `server`/`blog`/`api`/`docs` all exit 0. README's `git clone … / cd example/blog` also fixed — clone with no explicit target directory lands in `./core`, so the next line needs `cd core` first; as written, the documented command failed before ever reaching the `go.mod` question. **Scope grew mid-task on a production finding**: `process.smeldr.dev`'s own `/_health` reported `mcp: 1.28.0` after A245's deploy — A245's `replace` covered `smeldr.dev/core` only, so the live artifact shipped core v1.63.0 against mcp v1.28.0, meaning `grant_role`/`list_grants`/`revoke_grant`/`token_id` (A243/A244, shipped in mcp v1.30.0) never reached the instance, silently: a stale MCP-tool pin loses tools with no build error, a distinct failure class from A245's compile-time one that A245's own `GOWORK=off go build/vet/test ./...` check doesn't catch. Checked (`go list -m -versions`, real proxy), not assumed, all five of `example/server`'s out-of-repo pins: `mcp` v1.28.0→v1.30.0 bumped (every intervening `CHANGELOG.md` entry read; none alter behavior this consumer depends on); `social` v0.9.2, latest v0.10.1, **not bumped** — v0.10.1 makes a previously-silently-ignored `status` field on create actually take effect, a real behavior change, not additive surface, flagged for a separate decision rather than carried silently; `agent`/`media`/`oauth` confirmed already current. Rebuilt the release artifact from the fixed commit; verified end to end against a local running instance (not just `go build`) that `tools/list` includes the three grant tools and `create_token`'s response includes `token_id`. Not deployed — devops takes the rebuilt artifact from here. `smeldr.dev/social` left at v0.9.2 (latest v0.10.1) — noted as knowingly, not accidentally, behind, with the reason on record: an unrecorded stale pin is exactly the failure this Amendment's own opening finding traces back to. Side effect flagged on review: `go mod tidy` rewrote `example/server`'s `smeldr.dev/core` require line `v1.54.0`→`v1.63.0`, which makes `/_health`'s `"core"` field currently true for this build (T219's decoration-not-measurement symptom) without closing T219 — nothing re-derives that line automatically at the next release. No `smeldr.dev/core` package content changed. No version bump, no tag. |
| 2026-08-10 | A245 (Level 1): `example/server/go.mod` gains `replace smeldr.dev/core => ../..`, matching `example/blog`/`example/api`/`example/docs`, which all already carry it — `example/server` was the only one of the four that didn't. Found while building a `v1.63.0` release artifact from a clean tag checkout (`GOWORK=off`, no gitignored `go.work` to paper over it): the module's pinned `smeldr.dev/core v1.54.0` predates `App.ContextPacketHandler` (A214, v1.55.0), `App.Provenance`/`ProvenanceStore` (A220, v1.57.0), and `RegisterOrchestrationRelationKinds` (A236, v1.60.0), all used by `main.go` — a fresh clone following README's own `cd example/server && go run .` instruction has been unable to build for roughly a month, invisible only because every actual build in this project goes through `go.work`'s local override. Fix is the sibling modules' own pattern, not a version bump: a version pin goes stale again at the next release, a relative replace never does. The five other pinned dependencies (`mcp`, `agent`, `media`, `oauth`, `social`) are separate repos, can't be relatively replaced from a core-only clone, and were checked, not assumed, to still be current — `GOWORK=off go build/vet/test ./...` all pass with only the core replace added, no further undefined symbols. No `smeldr.dev/core` package content changed — `example/server` is its own module (`module example/server`); confirmed no core files touched. No version bump, no tag: nothing in the core module itself changed. Separately flagged, not fixed here: `example/blog/go.mod` also carries `replace smeldr.dev/mcp => ../../../mcp`, a path outside this repo — a `smeldr/core`-only clone may not be able to satisfy README's primary quickstart either, unverified against a real clean clone, tracked as its own follow-up. |
| 2026-08-09 | A244 (D43, T216 addition, Level 2): closes a real gap the architect found in A242/A243's own operator surface — `create_token` and `grant_role` are the two acts D43 splits token provisioning into, but nothing returned the identity (`token_id`, the JWT `User.ID`) the second act needs, so an operator had to hand-decode the raw JWT between them. New `TokenStore.CreateWithID(ctx, name, role, ttl) (raw, userID string, err error)` (`auth.go`) — a thin wrapper exposing `createToken`'s already-computed `userID`, which `Create` (unchanged, same signature, same callers) still discards. `smeldr.dev/mcp`'s `create_token` tool switches to `CreateWithID` and adds `token_id` to its response, ready to pass directly to `grant_role`'s own `token_id` argument — same field name deliberately, so a caller never has to work out that the two are the same identity. `TestGrantTools_EndToEnd_MintedGrantRatifiesDecision` (mcp) rewritten to read `token_id` off `create_token`'s response instead of calling `VerifyTokenString` — the decode step was standing in for the missing field, and leaving it in the "happy path" test would have left a passing test documenting a workaround instead of the fixed workflow; a narrower `TestHandleTokenTool_CreateWithID_TokenIDMatchesJWT` keeps one `VerifyTokenString` cross-check as a consistency guarantee. `AGENTS.md`'s grant-tools section (added by A243) corrected — the "not returned by create_token, decode it yourself" language it shipped with is superseded. 2 new core tests (`TestTokenStore_CreateWithID`, `_execError`); mcp coverage 96.3%, core coverage 96.2%. No exported symbols removed; new exported `TokenStore.CreateWithID`. MINOR bump (new exported API surface). |
| 2026-08-09 | A242 (D43, D44, T216, Level 2, smeldr/core half): implements D43's removal of the implicit `smeldr_tokens.role` → governance-grant bridge and D44's mandatory audit trail. **Verified, not assumed:** grepped every production `RoleGranted`/`Authorized` call site (`module.go:1369`, `module.go:1397`, `orchestration.go:222`, `state.go:451`) plus `mcp/tool.go`'s `authoriseTool` — all five resolve the actor from `ctx.User().ID` or a JWT-sourced `actorID`, none query `smeldr_tokens.id` (the fingerprint `migrateTokenGrants` wrote into `token_id`); D43 itself had checked only two of these before this task, so this closes a real, previously-partial verification, not a re-confirmation. `migrateTokenGrants` removed (`governance.go`) along with its call site in `migrateGovernance` and 8 dedicated tests. New `pruneInertTokenGrants(ctx, db) (removed int, err error)` replaces it: deletes `smeldr_role_grants` rows by membership (`token_id` appears in `smeldr_tokens.id`), not by shape — a 64-hex-character format check (D43's own diagnostic for a human reading a table) was the first draft, rejected on review because a real grant's `token_id` could coincidentally match that shape without ever matching a fingerprint by membership, and a format-based DELETE would remove it anyway; fail-open, idempotent, logged via `slog.Info` on every run including zero removed. `App.Governance` (`governance.go`) now also calls `CreateGovernanceAuditTable` and constructs a `GovernanceAuditStore` unconditionally — no opt-in, per D44's own "the record is not optional." New `App.GovernanceAuditStore()` accessor beside `App.RoleStore()`; `smeldr.go` gains an `App.governanceAudit` field. **Real design point found while planning, not obvious from `RoleStore.WithAudit`'s own signature:** it bakes one fixed actor into the store it returns, so it cannot be wired once at startup — every caller must derive a fresh `WithAudit(ctx.User().ID, ...)` copy per request/actor (consumed by `smeldr.dev/mcp`'s new grant tools, A243). `seedToolPolicies` gains `grant_role`/`list_grants`/`revoke_grant`, all `"administer"` — must ship in the same release as `smeldr.dev/mcp`'s tool implementation, since `authoriseTool` denies a tool with no policy row for everyone, the same code path a real DB error takes. 17 new/extended tests (4 `pruneInertTokenGrants` unit tests including a membership-vs-shape regression proving a coincidentally-64-hex-character real grant survives; `App.Governance` extended for the mandatory-audit assertion plus a new `CreateGovernanceAuditTable`-failure test; `seedToolPolicies` spot-checks extended). No exported symbols removed; new exported `App.GovernanceAuditStore()`. Coverage: 96.3% (`pruneInertTokenGrants`/`Governance`/`GovernanceAuditStore`/`migrateGovernance` all 100%). MINOR bump (new exported API surface). |
| 2026-08-09 | A241 (D40, D42, Level 2): two related fixes. Part 1 (D40) — `orchDecisionFlow()`'s `pending-re-evaluation→ratified`/`→superseded` transitions gain `RequiredRole: "admin"`/`Strict: true`, matching D34's own two direct-path transitions exactly: ratifying/superseding is the same authority-bearing act through either door, and having been re-evaluated first supplies no authority the direct path doesn't already require. `decisionRatifyModule` (`module_test.go`) generalized to `decisionModuleAtStatus(t, status string)` (its 3 existing callers pass `"proposed"` explicitly, no behaviour change — confirmed by inspection) and reused to seed `"pending-re-evaluation"` for 4 new end-to-end tests (`updateHandler`, real `RoleStore`, real 403/200) proving both new transitions actually reject without `admin`, not just that the struct fields are set. Part 2 (D42, T211's authority half) — `App.DrainEvalQueue` (`state.go`) now declines to cross any `RequiredRole`-gated transition itself: new `drainAuthorizationGate(ctx, db, table, typeName, itemID, toState string) (fromState, requiredRole string, err error)` reads the item's live status (`smeldr_eval_queue` stores only `to_state`, confirmed no `from_state` column exists) and looks up whether the declared transition names a role, independent of `Strict` or whether governance is wired — deliberately not a call to `validateTransition` with an empty `actorID`, which was checked directly and found to pass on a non-strict gate (`state.go:440-445`), the exact gap D42's own body documents as the wrong obvious implementation. When gated, new `recordAuthorizationRequiredSignal(ctx, db, typeName, itemID, fromState, toState, requiredRole string) error` inserts a `Signal` (`sender="system"`, `receiver=<the declared role, never hardcoded>`, `signal_type="authorization-required"`) instead of applying the transition; the queue row is still deleted regardless (matches the existing "failed transitions are not re-queued" rule), and the ungated path (today, always `pending-re-evaluation`) is byte-for-byte unchanged. 15 new tests: `TestDecisionFlow_definition` extended (not just added to) for both new gated transitions; 6 direct `drainAuthorizationGate` unit tests covering every branch (status-read error, same-state short-circuit, no-flow-at-all — genuinely exercised by using `newSQLiteDB` instead of `newMigratedDB`, since `migrateStateFlows` always seeds a `"default"` flow that would otherwise mask this path — no-transition-row, ungated, gated, transition-query error); `recordAuthorizationRequiredSignal` success + insert-error; `DrainEvalQueue`-level end-to-end tests proving the wiring (gated transition blocks + signals + preserves item status; signal-record failure still logs and continues; two pre-existing branches — UPDATE failure and DELETE failure on the ungated path — that lost their only test coverage as a side effect of the new gate-check running first, given fresh direct coverage with a new `nthExecFailDB` wrapper). All `DrainEvalQueue`-level assertions drive the real `App.DrainEvalQueue` entry point, never the two new helpers directly, per explicit architect review criterion (mechanism vs. wiring). No exported symbols changed. Coverage: 96.2% (`drainAuthorizationGate`/`recordAuthorizationRequiredSignal`/`DrainEvalQueue` all 100%). Patch bump (consumer-observable: `PUT /decisions/{slug}` re-evaluation-door ratify/supersede now requires `admin`; any instance with a `RequiredRole`-gated `DrainEvalQueue` target now blocks automation on it instead of applying it). v1.61.1 → v1.61.2. |
| 2026-08-08 | A240 (Level 2): the async trigger pipeline (`TransitionTrigger`/`fireAsyncTriggers`, A187) never fired for any typed orchestration module. Confirmed by grep: `fireAsyncTriggers` had exactly two call sites, both in `dynamic.go` (`DynamicTypeRepo.setStatus`/`ScheduleContent`) — `module.go` had zero. Traced every real transition path a typed module (`Signal`/`Task`/`Decision`/`Amendment`/`Goal`/`Run`) has, not just `updateHandler`: `updateHandler`, `MCPPublish`, `MCPSchedule`, `MCPArchive`, and `processScheduled` (which skips `validateTransition` entirely, being system-initiated) all reach a real `Status` change via `m.repo.Save` and none called `fireAsyncTriggers`. Confirmed unintended, not a deliberate boundary: `orchDecisionFlow()`'s own `schedule-eval` `TransitionTrigger` on `proposed→ratified` (registered by A187, 2026-07-01) had never fired for a single ratified `Decision` since it shipped. Fix: one `fireAsyncTriggers(ctx, m.db, m.contentTypeName, fromState, toState, itemID)` call added at each of the five sites, immediately after the successful `Save`, mirroring `dynamic.go`'s own two working call sites exactly — no new function, no new exported symbol. `updateHandler`'s call is guarded by `prevStatus != newStatus` (matches its own existing `validateTransition` guard, avoids a wasted query on every plain content edit); the three `MCP*` sites and `processScheduled` call it unconditionally (matches `dynamic.go`'s own unguarded shape). `MCPUpdate` deliberately excluded — its own doc comment already states `Node.Status` is always restored after the merge (`module.go:2333`), so `fromState == toState` is structurally guaranteed inside it; there is nothing to fix there, confirmed from stated intent, not inferred from behaviour. Scope explicitly narrowed on architect review from the original investigation's three findings to this one: wiring `agent.NewEvalQueueScheduler` into `example/server` (the second finding — the drain mechanism is documented and tested in `smeldr.dev/agent` but never called there) was **held**, not shipped — the architect traced that shipping it alongside this fix would mean the live `process.smeldr.dev` instance starts automatically flipping ratified `Decision`s to `pending-re-evaluation` with zero `Provenance`, zero `Signal` dispatch, zero cache invalidation (this task's own third finding: `DrainEvalQueue` applies its transition via a raw SQL `UPDATE`, never through `Save`/`notifyAfter`), **and** zero `validateTransition`/`RoleGranted` call (a fourth finding, from the architect's own independent review, not this task's original investigation: `DrainEvalQueue`'s body calls neither). Both facets tracked together as **T211**, not dispatched. Until T211 lands, this Amendment's own effect is deliberately incomplete and stated as such: registered async triggers now correctly insert rows into `smeldr_eval_queue` for typed modules, but nothing in `example/server` drains that queue — rows will visibly accumulate, which is the intended, safer intermediate state, not an oversight. 8 tests: `TestModule_updateHandler_firesAsyncTrigger`, `TestModule_updateHandler_noTriggerOnSameStatus` (registers a trigger ON THE SAME-STATUS transition itself, not a different one, so the guard — not merely "nothing matched" — is what the test actually proves; independently verified by temporarily removing the guard and confirming the test fails), `TestModule_MCPPublish_firesAsyncTrigger`, `TestModule_MCPPublish_firesAsyncTriggerEvenOnSameStatus` (the other half of the asymmetry pin — a real republish self-transition with a trigger registered on `published→published`), `TestModule_MCPSchedule_firesAsyncTrigger`, `TestModule_MCPArchive_firesAsyncTrigger`, `TestModule_processScheduled_firesAsyncTrigger`, plus the pre-existing `TestSetStatus_firesAsyncTrigger` (dynamic-content path, unaffected, still green). Every new test drives a real typed-module transition path (HTTP `PUT`, or a real `MCP*`/`processScheduled` call) — none call `fireAsyncTriggers` directly, per the architect's own explicit review criterion. New shared test helper `setupTriggerFlowTransition` (parameterized `from`/`to`, alongside the existing fixed-shape `setupTriggerFlow`). No new error-path table — five call sites to an existing, already-tested, void function, no new branching logic. No exported symbols changed. Coverage: 96.2% (unchanged). Patch bump (consumer-observable: a registered `TransitionTrigger` on a typed module's flow now actually fires). v1.61.0 → v1.61.1. |

---

All files are in a single package: `smeldr`. There are no sub-packages.
This is intentional — it eliminates circular import issues and keeps
the API surface in one place. The file names are the organisation.

### Implemented (Milestone 1 + Milestone 2)

```
smeldr.dev/
│
├── errors.go         Error interface, sentinel errors, WriteError(), ValidationError
├── roles.go          Role type, hierarchy, HasRole(), IsRole(), built-in constants, Option interface;
│                     Job/Agent constants — classification tags, deliberately unregistered in the
│                     hierarchy (level 0, HasRole never matches; IsRole does) (Amendment A224, T178)
├── mcp.go            MCPOperation type, MCPRead/MCPWrite constants, MCP() option,
│                     MCPMeta struct (Prefix, TypeName, Operations, SingleInstance), MCPField struct
│                     (incl. Format/Description — D27), MCPModule interface
│                     (Amendment A49)
├── node.go           Node (incl. Rev int — optimistic-concurrency token, Amendment A158), Status,
│                     lifecycle constants, NewID(), GenerateSlug(), UniqueSlug(), ValidateStruct()
│                     GetSlug(), GetPublishedAt(), GetStatus() getter methods (Amendment A2)
├── context.go        Context interface, contextImpl, ContextFrom(), NewTestContext(), User, GuestUser,
│                     NewBackgroundContext, NewContextWithUser
├── signals.go        LifecycleEvent type (renamed from Signal, A183), On[T]() option,
│                     dispatchBefore(), dispatchAfter(), debouncer, debouncer.Stop() (Amendment A39);
│                     SignalEvent{Type, Slug, Title, URL, Timestamp, PreviousState, ActorRole, ActorID,
│                     ActorRoles}, afterHookMeta (unexported), buildSignalEvent (unexported) (Amendment A94);
│                     ActorRoles []Role captured synchronously in buildSignalEvent (same moment as
│                     ActorID/ActorRole) — added because dispatchBus's context.WithoutCancel/WithTimeout
│                     wrapping strips smeldr.Context's method set from the ctx an OnSignal handler
│                     receives, so a handler cannot recover roles via ctx.(Context) itself (Amendment
│                     A224, T178)
├── orchestration.go  Signal, Task, Decision, Amendment, Goal, Run content types embedding Node;
│                     GoalContext struct (Goal + LinkedDecisions + LinkedTasks + LinkedGoals);
│                     QueryGoalContext(ctx, DB, *RelationStore, goalID) (*GoalContext, error);
│                     CreateOrchestrationTables(DB) error — creates 6 tables incl. smeldr_runs;
│                     RegisterOrchestrationTypes(*App, DB) — fail-open, registers 6 types + 5 flows
│                     (Run gets no flow, see below);
│                     orchSignalFlow, orchTaskFlow, orchDecisionFlow, orchAmendmentFlow, orchGoalFlow (unexported)
│                     (Amendment A183, T23 Step 10; Goal type: A198, T114 Step 1)
│                     RunOutcome/RunCleanupState (string-enum types, constants) + Run struct —
│                     sixth orchestration type, D38/M3: registers no StateFlow (state lives in
│                     LeaseHolder/Outcome, guarded by SQLRepo.Save's rev-CAS, never Node.Status);
│                     AcknowledgedAt is time.Time (zero = unset), not *time.Time — the generic
│                     SQLRepo scan path (scanDest) only special-cases a field whose own type is
│                     time.Time (its address is naturally *time.Time); a *time.Time field would
│                     scan into **time.Time, unhandled, verified directly before building rather
│                     than assumed (Amendment A239, M3 part 1)
│                     orchDecisionFlow's proposed→ratified and ratified→superseded transitions
│                     now carry RequiredRole="admin", Strict=true (D34: ratify/supersede
│                     authority was a real, working mechanism left unset on these two rows, not
│                     a missing capability); decisionScopeRoles map[string]string (unexported,
│                     starts empty) + authorizeDecisionScope(ctx, rs, actorID, item any,
│                     scopeRoles) error — a second, per-instance authorization layer alongside
│                     RequiredRole: RequiredRole is one value shared by every Decision regardless
│                     of that item's own Scope field, so a future scope-to-role policy (e.g.
│                     "core" decisions need a different ratifying role than "cross-cutting" ones)
│                     cannot be expressed by RequiredRole alone; a no-op today for every type
│                     including Decision until decisionScopeRoles is populated — the generic
│                     RequiredRole="admin" gate above is what actually enforces authority now
│                     (Amendment A234, T206)
│                     orchDecisionFlow's pending-re-evaluation→ratified and
│                     pending-re-evaluation→superseded transitions also carry
│                     RequiredRole="admin", Strict=true (D40): ratifying/superseding is the same
│                     authority-bearing act regardless of which door it is entered through, and
│                     having been re-evaluated first does not supply the direct path's authority
│                     (Amendment A241, D40)
│                     RegisterOrchestrationRelationKinds(ctx, *RelationStore) error — registers
│                     derives_from (Task→Goal), depends_on (Task→Task), ships_as (Task→Amendment),
│                     supersedes (Decision→Decision), all Mode="asserted"/Directional=true/
│                     Weighted=false, per D36; idempotent via UpsertKind (Amendment A236, M0 step 2)
│                     orchTaskFlow/orchGoalFlow both gain "resolved" (IsTerminal), a fourth/third
│                     outcome distinct from "done": the underlying need was met, but not by this
│                     item's own tracked work (D58). Task: reachable from active/waiting-plan/
│                     plan-reviewing, all RequiredReason=true. Goal: "parked" loses its (dishonest —
│                     it has an outbound edge to "open") IsTerminal flag, and "resolved" is
│                     reachable from open/in-progress/parked, all RequiredReason=true
│                     (Amendment A261, T255)
├── context_packet.go ContextPacket, PacketSource, PacketAnchor, PacketBoundary, PacketOmission,
│                     PacketItem, PacketRelation exported types;
│                     BuildContextPacket(ctx, DB, *RelationStore, baseURL, sourceName, anchorType,
│                     anchorSlug string, depth int) (*ContextPacket, error) — breadth-first
│                     traversal over all 5 orchestration anchor types, depth 1–2, per-type cap 25;
│                     App.ContextPacketHandler(rs *RelationStore, sourceName string) — mounts
│                     GET /packet/{type}/{slug}[?depth=] unauthenticated HTTP endpoint;
│                     anchorTypeEntry, anchorTypeTable, packetFetchItem, packetFieldsFromItem,
│                     packetItemURL (unexported) (Amendment A214, T145)
├── reachability.go   ReachabilityItem, ReachabilityRing, Reachability exported types;
│                     MaxReachabilityDepth = 10 constant;
│                     RelationStore.Reachability(ctx, anchorType, anchorID, kind, direction string,
│                     maxDepth int) (*Reachability, error) — general-purpose bounded BFS over the
│                     relation graph from any anchor type/id, one ring per hop 1..maxDepth, ring with
│                     zero items = genuine reportable absence, not hardcoded to any fixed type set
│                     (contrast context_packet.go's 5-type anchorTypeTable); reachabilityNode,
│                     reachabilityNeighbors (unexported) (Amendment A219, T153)
│                     ReachabilityItem.EdgeClass/Confidence — the edge that reached each item now
│                     survives the traversal instead of being dropped; when a node is reachable via
│                     more than one edge at the same hop distance, the most-trusted edge's metadata
│                     wins (asserted > observed > inferred, then higher Confidence, nil lowest) —
│                     reachabilityCandidate/edgeClassRank/betterCandidate (unexported) implement the
│                     deterministic tie-break; previously the first edge processed silently won, with
│                     no defined order (Amendment A231, T194)
├── lineage.go        LineageNode, LineageTrace exported types; MaxLineageDepth = 10 constant;
│                     RelationStore.TraceLineage(ctx, anchorType, anchorID string, maxDepth int)
│                     (*LineageTrace, error) — bounded, read-time upstream traversal over
│                     depends_on/derives_from edges (D35); reuses reachability.go's BFS shape
│                     (reachabilityNode, edgeClassRank, betterCandidate) rather than edges.go's
│                     ChildrenOf, adding a real batched IN(...)-per-depth-level query that
│                     reachability.go itself lacks (batchEdgesBySource/batchEdgesByTarget/
│                     batchEdgesByTypeAndIDs, unexported, grouped by node type for SQLite/pgx
│                     portability); a node superseded during the walk is followed to its
│                     replacement (followSupersedes, unexported) at the same Depth, chasing a full
│                     supersede chain rather than one hop, capped at MaxLineageDepth hops;
│                     LineageTrace.Truncated set by hasFurtherLineage (unexported), a one-time
│                     non-recording peek at the depth boundary rather than assumed from a
│                     non-empty frontier (Amendment A235, D35/M2)
├── storage.go        DB interface, Query[T], QueryOne[T], Repository[T], MemoryRepo[T], ListOptions;
│                     timeScanner (unexported) — sql.Scanner for time.Time fields, handles SQLite
│                     string format; scanDest (unexported) — wraps *time.Time destinations (A200)
│                     ColumnLookupRepository[T] — optional Repository[T] extension (SeqRepository's
│                     own precedent), FindByColumn(ctx, column, value) resolves an item by a
│                     non-slug/id column; implemented by both SQLRepo (raw SQL) and MemoryRepo
│                     (reflection via fieldNameForColumn, unexported, reuses dbFields — no per-type
│                     table needed there). Module.resolveItem (module.go) and App.TransitionItem
│                     (state.go) both fall back to it, via a new orchestration.go humanIDColumns
│                     map, when a slug lookup misses on a type with a human-facing identifier (e.g.
│                     Task.TaskID) — so get_task("T203") and transition_item resolve the same way
│                     slug already did (Amendment A262, T253);
│                     MigrateNodeRevColumn(db, table) now a thin wrapper over migrate.go's new
│                     EnsureColumn(ctx, db, table, column, columnDDL) error — generalizes four
│                     independently hand-duplicated PRAGMA-probe-then-ALTER functions found across
│                     this package into one; additive-only column migration for any table, called
│                     by the application (or the framework) at its own startup, no central registry
│                     of known columns. migrateTransitionReasonColumn/migrateTransitionStrictColumn/
│                     migrateStateFlowConflictColumns (migrate.go) all now thin wrappers too,
│                     behaviour-preserving. Live bug found and fixed: CreateSiteConfigTable's
│                     (site_config.go) own CREATE TABLE text was missing scheduled_at/rev — Node
│                     declares both, so SQLRepo.Save failed against any table it created, fresh
│                     install or not; fixed by declaring both columns directly plus an EnsureColumn
│                     call for pre-existing installs that already ran the old DDL, matching A221's
│                     own precedent shape (Amendment A264, T246)
├── state.go          StateFlow, State, Transition — data-driven state machine types;
│                     ConflictPolicy type (ConflictReject, ConflictSupersede constants);
│                     StateFlow.ActiveState + StateFlow.ConflictPolicy optional fields;
│                     Transition.RequiredRole string — optional role name gate;
│                     Transition.RequiredReason bool — optional per-transition reason gate (T149);
│                     Transition.Strict bool — optional per-transition fail-closed gate (D34, T206):
│                     when true, a nil RoleStore or an empty actorID reject with ErrForbidden
│                     instead of allowing through; false (zero value) keeps every pre-existing
│                     transition's lenient behaviour unchanged; only meaningful alongside a
│                     non-empty RequiredRole;
│                     App.RegisterFlow(StateFlow) error — idempotent upsert (INSERT OR
│                     IGNORE + SELECT id + UPDATE conflict fields); validateFlowItems
│                     (unexported) — SQLite-only unknown-state check; validateTransition
│                     (ctx, db, rs *RoleStore, actorID, typeName, from, to, reason) —
│                     fail-open zone (structural: nil DB, non-SQLite, no flow) plus one
│                     fail-CLOSED structural branch (D34): the smeldr_transitions row query
│                     erroring for a reason other than sql.ErrNoRows now returns ErrInternal
│                     instead of silently allowing through — this branch runs before Strict is
│                     ever consulted, for every transition, so a transient DB error can no
│                     longer bypass a role gate regardless of that transition's own Strict
│                     value; fail-closed gates: required_reason (unconditional — ErrBadRequest
│                     when required_reason=true and reason==""), then required_role (rs wired,
│                     actorID non-empty → RoleGranted; error or !ok → ErrForbidden; rs==nil or
│                     actorID=="" → ErrForbidden when Strict, otherwise skip check and allow,
│                     matching every transition's behaviour before D34); applyConflictPolicy
│                     (unexported) — ConflictReject/ConflictSupersede enforcement after
│                     validateTransition; conflictRejectCheck, conflictSupersede,
│                     conflictIDs (unexported helpers); all DB errors fail-open
│                     (Amendments A175, A176, A186, T23, A193, A220, A234);
│                     defaultInitialState(ctx, db, typeName) string — returns typeName's own
│                     registered IsInitial state (queries smeldr_states directly, does not fall
│                     back to the default flow — its own initial state is always "draft", so a
│                     second query would be redundant); "" (fail-open) when none registered.
│                     Used by module.go's applyDefaultStatus to default an omitted create-time
│                     status to the type's real initial state instead of the literal Draft
│                     constant (Amendment A225, T180);
│                     TransitionItemWithReason now calls dispatchTransitionWebhook (webhook.go)
│                     right after fireAsyncTriggers — a dedicated delivery path beside it, not a
│                     bus emit, because App.OnSignal's LifecycleEvent vocabulary is fixed to
│                     Draft/Published/Archived semantics and buildWebhookPayload requires a typed
│                     Go item, neither of which fits a StateFlow-driven transition on an
│                     arbitrary named state or the D42-class Signal recordAuthorizationRequiredSignal
│                     inserts by raw SQL; event name convention "{type}.transitioned", nil-safe
│                     when App.Webhooks was never wired; recordAuthorizationRequiredSignal gained
│                     store/pool params and fires "signal.created" — the same event name a
│                     human-created Signal already produces — so a D42-triggered Signal is
│                     indistinguishable from a human-created one to a webhook subscriber
│                     (Amendment A263, T231)
├── audit.go          AuditRecord, AuditFilter, AuditStore interface, NewAuditStore(DB), CreateAuditTable(DB),
│                     newAuditHandler (unexported); GET /_audit mounted by App.Handler() (Amendment A97)
├── provenance.go     ProvenanceRecord, ProvenanceFilter, ProvenanceStore interface, NewProvenanceStore(DB),
│                     CreateProvenanceTable(DB); App.Provenance(store) — subscribes to AfterCreate/
│                     AfterUpdate/AfterPublish/AfterUnpublish/AfterSchedule/AfterArchive/AfterDelete via
│                     the signal bus, writes one ProvenanceRecord per event (recordProvenance, fail-open);
│                     provenanceVerbFor/currentStatusOf/actorKindFor (unexported); purely additive —
│                     AuditRecord/App.Audit unchanged, no shared table (Amendment A220, T149);
│                     actorKindFor(actorID, roles []Role) resolves "job"/"agent" via IsRole against
│                     SignalEvent.ActorRoles (not ctx-recovery — see signals.go entry above); wired into
│                     a real running instance via example/server's ENABLE_PROVENANCE (Amendment A224, T178);
│                     ProvenanceEntry, SubjectProvenance(ctx, db, store, subjectType, subjectID) — the read
│                     mechanism (T243): item-scoped only (never actor-keyed, provenance-visibility-brief.md
│                     §4.1), applies §4.3's gating decision per record via transitionIsGated (unexported;
│                     reuses state.go's resolveFlowID/lookupTransitionGate — a RequiredRole+Strict
│                     transition exposes ActorKind/ActorID/Surface/Reason, an ungated one carries only
│                     Verb/FromState/ToState/Timestamp). No HTTP route, no MCP tool — brief §4.7 names
│                     cloud's Trace witness certificate as the only surface, composed directly the same
│                     way cloud/internal/read.BuildTraceReading already reads core data
├── blocks.go          DynamicNode (embeds Node; TypeName, Fields json.RawMessage) + Head(),
│                     NewDynamicContentRepo(db) *SQLRepo[*DynamicNode] (binds smeldr_dynamic_content),
│                     CreateBlockTables(db) — grouped idempotent creator: smeldr_dynamic_content +
│                     smeldr_content_edges + (parent_id, sort_order) index (Amendment A116, T32)
├── relations.go       RelationKindDef, RelationEdge (not Node-embedding), RelationKindRegistry,
│                     RelationStore; CreateRelationTables(db), NewRelationStore(db),
│                     ValidateRelationKindDef, UpsertKind, GetKind, ListKinds, Assert,
│                     GetBySource, GetByTarget, Delete; App.Relations/RelationStore (Amendment A159, T06);
│                     RelationStore.provenanceStore field + setProvenanceStore (unexported), wired at
│                     App.Handler() time when both App.Relations and App.Provenance are configured;
│                     insertEdge calls recordAssertProvenance (unexported) — recovers the actor via a
│                     type assertion on ctx (ctx.(Context)), no signature change to Assert/
│                     MCPAssertRelation/MCPProposeRelation, no smeldr.dev/mcp changes (Amendment A220, T149);
│                     recordAssertProvenance also passes the ctx-derived Roles into actorKindFor (this
│                     call site is genuinely synchronous, unaffected by the dispatchBus ctx-wrapping
│                     constraint that ruled out the same approach in provenance.go); CreatedByJob still
│                     overrides a ctx-derived actor unconditionally (Amendment A224, T178)
│                     MCPObserveRelation — mirrors MCPAssertRelation/MCPProposeRelation, stores
│                     edge_class="observed" (a system directly witnessed a fact, e.g. via a webhook/
│                     integration, as opposed to a human's direct claim or an agent's inference).
│                     Deliberately excluded from governance.go's asserted-only dynamic-scope check and
│                     from RecomputeAsserted/BulkRecompute's asserted-only diff scope — a system-
│                     witnessed fact is not automatically the same trust tier as a deliberate human
│                     grant (design/edge-class-observed-spike.md, T193) (Amendment A232, T196)
├── edges.go           ContentEdge, ContentEdgeStore, NewContentEdgeStore(db); AddChild/Children/
│                     ChildrenOf (batch IN())/RemoveChild/Reorder (atomic CASE); scanEdges, edgeColumns;
│                     one composition-edge table for page→block + collection→item (Amendment A116, T32)
├── serveblocks.go     App.ServeBlocks(dir) (*BlockRenderer, error), BlockRenderer + Render; batched
│                     loadTree/loadBlocks, recursive renderBlock (visited-set + maxDepth), buildData
│                     (contract: Node fields + promoted Fields + markdown/raw-HTML), blockFieldRegistry
│                     (interim until c7); graceful degradation throughout (Amendment A118, T32 c4);
│                     reference-field resolution — refs registry + refIDsOf + batched ref-load,
│                     {Name}ID → .{Name} = referenced block buildData (Amendment A120, T82)
├── governance.go     ScopeMode type (ScopeGlobal/ScopeStatic/ScopeDynamic constants);
│                     migrateGovernance(ctx, db) — smeldr_roles + smeldr_role_grants + smeldr_tool_policies
│                     tables + indexes; seedDefaultRoles (author/editor/admin); seedToolPolicies
│                     (built-in MCP tool → required_op mapping); migrateTokenGrants (token role →
│                     global-scope grant, WHERE NOT EXISTS guard for NULL-in-UNIQUE);
│                     fail-open on missing smeldr_tokens (T49 Step 1, A188);
│                     RoleDefinition, RoleGrant, AuthTarget exported structs;
│                     RoleStore + NewRoleStore(db): DefineRole (rejects trust_level=1),
│                     Grant (WHERE NOT EXISTS for NULL anchor), Revoke, ListGrants, Authorized
│                     (pre-collects rows → avoids SQLite nested-connection deadlock;
│                     dynamic scope: edge_class='asserted' + active-edge predicate;
│                     static scope: TypeName+":"+ID — not slug);
│                     App.Governance(store) validates store.db == cfg.DB, runs migrateGovernance;
│                     App.RoleStore() accessor; App.governance field in smeldr.go (T49 Step 2, A189);
│                     GovernanceAuditRecord, GovernanceAuditStore (write-only interface);
│                     CreateGovernanceAuditTable(db) — smeldr_governance_audit + idx_governance_audit_actor;
│                     NewGovernanceAuditStore(db);
│                     RoleStore.WithAudit(actorTokenID, log) returns shallow copy with audit wired —
│                     bakes a single fixed actor into the returned store, so callers must derive a
│                     fresh copy per request/actor, never wire one shared instance at startup;
│                     DefineRole/Grant/Revoke record before/after JSON to GovernanceAuditStore when wired;
│                     fail-closed on Append error; (T49 Step 2.5, A190);
│                     migrateTokenGrants removed (D43): keyed grants on smeldr_tokens.id (the SHA-256
│                     fingerprint), but Authorized/RoleGranted (and mcp's authoriseTool) resolve the
│                     actor from the JWT's User.ID exclusively — verified directly across all five
│                     production call sites (module.go x2, orchestration.go, state.go, mcp/tool.go),
│                     not just the two D43 itself had checked — so every grant that migration ever
│                     produced was structurally unreachable. New pruneInertTokenGrants(ctx, db)
│                     (removed int, err error) replaces it in migrateGovernance — enforcement, not
│                     migration: runs on every call, forever, deleting any smeldr_role_grants row
│                     whose token_id appears in smeldr_tokens.id, by membership (not by shape — a
│                     64-hex-character format check would delete a real grant if any deployment ever
│                     produced a User.ID of that shape) — fail-open, logged via slog.Info on every
│                     run including zero removed;
│                     App.Governance now also calls CreateGovernanceAuditTable and constructs the
│                     GovernanceAuditStore unconditionally — audit is no longer opt-in (D44: an
│                     authority mutation always leaves a record, and the record is not optional);
│                     new App.GovernanceAuditStore() accessor beside App.RoleStore(); smeldr.go gains
│                     an App.governanceAudit field alongside App.governance;
│                     seedToolPolicies gains grant_role/list_grants/revoke_grant, all "administer"
│                     (T216, D43/D44);
│                     Grant/Revoke's state-changing write and audit append now share one transaction
│                     when possible (txGovernanceAuditAppender optional interface, appendTx +
│                     appendAuditRecord — implemented by the bundled sqlGovernanceAuditStore) — a failed
│                     audit append rolls back the grant/revoke instead of leaving it applied with no
│                     record, closing A190's own "non-atomic" caveat above. Falls back to the prior
│                     sequential behaviour for a custom, non-SQL GovernanceAuditStore or a DB without
│                     transaction support — honestly unimproved for that case, not silently claimed
│                     fixed. DefineRole not touched — same-shape follow-up, out of this amendment's
│                     scope; (T200, A233);
│                     RoleStore.ToolPolicy(ctx, toolName) (requiredOp string, found bool, err error) —
│                     exact-match lookup in smeldr_tool_policies; found=false when no row (ErrNoRows);
│                     seam between core and smeldr.dev/mcp: MCP server calls ToolPolicy then Authorized;
│                     prefix-pattern fallback for runtime-defined content types deferred to T104 Step 8;
│                     (T49 Step 3, A191);
│                     RoleStore.RoleGranted(ctx, tokenID, roleName, target) (bool, error) — Path B:
│                     name-based role lookup (vs Authorized's Path A operation-word lookup);
│                     same three scope modes (global/static/dynamic) and fail-closed §5.5 semantics;
│                     used by validateTransition to gate Transition.RequiredRole;
│                     DynamicTypeRepo.rs *RoleStore field + WithGovernance(rs *RoleStore) *DynamicTypeRepo
│                     shallow-copy method — wires governance into DynamicTypeRepo.SetStatus;
│                     SetStatus extracts actorID via local smeldrCtxAccessor interface (User() User);
│                     plain context.Context callers get actorID="" → skip check;
│                     (T49 Step 4, A193)
├── auth.go           AuthFunc interface, BearerHMAC, CookieSession, BasicAuth, AnyAuth, SignToken,
│                     VerifyBearerToken(r, secret, store *TokenStore);
│                     TokenRecord, TokenStore, NewTokenStore (Amendment A66);
│                     Revoke last-admin guard — returns ErrLastAdmin (Decision 26);
│                     createToken (unexported, new) — shared by Create and ensureBootstrap,
│                     returns the JWT's own User.ID alongside the raw token so a caller can
│                     reference the token's identity elsewhere without redundantly decoding it;
│                     ensureBootstrap now returns (userID string, created bool) instead of
│                     nothing, so App.Handler can grant the bootstrap token an admin RoleStore
│                     role in the same boot sequence (Amendment A237, M0 step 3);
│                     CreateWithID(ctx, name, role, ttl) (raw, userID string, err error) — thin
│                     wrapper exposing createToken's already-computed User.ID to callers outside
│                     this package; Create's own signature is unchanged (Amendment A244, T216)
├── middleware.go     RequestLogger, Recoverer, SecurityHeaders, CORS, MaxBodySize,
│                     RateLimit, TrustedProxy, InMemoryCache, CacheStore, Authenticate, CSRF, Chain
├── module.go         Module[T], NewModule, Register, Stop, At, Cache, Auth,
                      Middleware, Repo, On, SitemapConfig, AIIndex, WithoutID,
                      Feed, DisableFeed, ContextFunc, SingleInstance, Standalone,
                      APIOnly options;
                      setSitemap, regenerateSitemap, setAIRegistry, regenerateAI, aiDocHandler;
                      setFeedStore, regenerateFeed; triggerRebuild();
                      singleInstanceHandler; standaloneEnabled/findAndServe/findAndServeAIDoc
                      (standaloneDispatcher helpers);
                      aiFeatures, llmsStore, withoutID, feedCfg, feedStore,
                      contextFunc, singleInstance, standalone, apiOnly fields;
                      stoppable interface, stopCh field (Amendment A39);
                      debounce callback uses NewBackgroundContext (Amendment A41);
                      contextFuncOption, ContextFunc (Amendment A65);
                      db DB field + setDB(DB) wired from App.Content (Amendment A176);
                      roleStore *RoleStore field + setRoleStore(*RoleStore) wired from App.Handler;
                      canReadDrafts(ctx) — 3-branch: nil store→legacy HasRole(Author),
                        store+no-ID→deny, store+ID→Authorized (fail-closed §5.5 on error);
                      checkWriteOp(ctx, op, legacyRole) — same 3-branch pattern;
                      isVisible(ctx, item) Module method (was standalone func) — Published always
                        visible, Draft delegates to canReadDrafts; all 4 call sites updated;
                      all write/delete gates use checkWriteOp; list-handler status filter uses
                        canReadDrafts; (T49 Step 3, A191)
│                     (Markdownable migrated to ai.go — Amendment A11)
│                     applyDefaultStatus(ctx, db, typeName, pv, nodeFields) — shared by
                        createHandler and MCPCreate: defaults an omitted create-time status to
                        typeName's own registered initial state (state.go's defaultInitialState),
                        falling back to the literal Draft constant only when no custom flow is
                        registered. Fixes two gaps: MCPCreate previously hardcoded Draft
                        unconditionally, breaking every orchestration type whose real initial
                        state isn't "draft" (-32001 Conflict via validateInitialState);
                        createHandler had no defaulting at all, silently persisting the literal
                        empty string "" — worse for a custom-flow type, since "" is not a valid
                        from_state for any of its registered transitions either, leaving the item
                        permanently stuck with no error (Amendment A225, T180)
│                     nodePublishedAtOf(v) time.Time + stampPublishedAt(item) — the latter sets
                        PublishedAt to now when item's resolved status is Published and
                        PublishedAt is still zero, called from createHandler and MCPCreate right
                        after item construction (before RunValidation); never overwrites a
                        caller-supplied PublishedAt (e.g. a data-import path preserving a
                        historical publish date). Fixes: neither create path previously stamped
                        PublishedAt at all when an item was created directly with status
                        "published", leaving RSS PubDate/sitemap LastMod wrong (zero) on
                        public-facing surfaces. Deliberately not reused at the 4 existing
                        transition-time PublishedAt call sites (updateHandler, MCPPublish,
                        MCPSchedule's scheduler path, processScheduled) — those unconditionally
                        overwrite PublishedAt on every transition into Published, including
                        republish, and routing them through stampPublishedAt's IsZero guard would
                        silently stop that on a second publish (Amendment A229, T181)
│                     updateHandler's publish-transition path (non-Published→Published) sets
                        PublishedAt before its single Save call, not via a second Save — the
                        second Save always failed against SQLRepo.Save's rev-based CAS
                        (Amendment A158) once the first Save had already advanced the stored rev
                        (Amendment A226, T179)
│                     fireAsyncTriggers (state.go) now called from all five real typed-module
                        transition sites — updateHandler (guarded by prevStatus != newStatus),
                        MCPPublish/MCPSchedule/MCPArchive (unconditional, matching dynamic.go's own
                        setStatus/ScheduleContent), and processScheduled (system-initiated, no
                        validateTransition call but a registered trigger doesn't care who
                        initiated the transition). Previously fireAsyncTriggers had exactly two
                        call sites, both in dynamic.go — no registered TransitionTrigger had ever
                        fired for any typed orchestration module (Signal/Task/Decision/Amendment/
                        Goal/Run), including Decision's own schedule-eval trigger on
                        proposed→ratified, registered since A187 and never once fired.
                        MCPUpdate deliberately excluded — its own doc comment already states
                        Node.Status is always restored after the merge, so fromState==toState is
                        structurally guaranteed inside it; there is no transition there to fire a
                        trigger for. DrainEvalQueue (the consumer that would apply a queued
                        schedule-eval outcome) is not wired into example/server in this task —
                        rows now correctly accumulate in smeldr_eval_queue, visibly, but nothing
                        drains them yet; see T211 (Amendment A240)
│                     drainAuthorizationGate(ctx, DB, table, typeName, itemID, toState string)
                        (fromState, requiredRole string, err error) + recordAuthorizationRequiredSignal
                        (ctx, DB, typeName, itemID, fromState, toState, requiredRole string) error —
                        T211's authority half: DrainEvalQueue now checks whether the item's
                        current status→to_state transition is role-gated before applying it;
                        automation never crosses a RequiredRole-gated boundary itself, regardless
                        of Strict or whether governance is wired — declaring RequiredRole is the
                        operator's stated intent, not withdrawn by an unwired RoleStore. A gated
                        transition is not applied; a Signal (signal_type="authorization-required",
                        sender="system", receiver=the exact declared role) is recorded instead and
                        the queue row is still deleted (blocked transitions are not re-queued,
                        matching the existing failed-transition rule). A successful (ungated or
                        authorized) transition now records one ProvenanceRecord (T211's
                        observability half, D51) — ActorKind="job", ActorID="drain-eval-queue",
                        Surface="trigger", via the same fail-open recordProvenance every other
                        subscriber uses; a failed provenance write never blocks the queue-row
                        deletion (A241's own not-re-queued rule, unweakened). Deliberately still
                        unrestored: Save/notifyAfter's other three effects (Signal dispatch, cache
                        invalidation, rebuild triggers) — argued out, not bundled in, since the
                        drain's only current target (D40's Decision re-evaluation) would activate
                        every human-publish subscriber with no operator decision behind it;
                        MCPUpdate's own body extracted into new updateFields(ctx, slug, fields,
                        surface string) (any, error) — identical merge/identity-restore logic,
                        parameterized on the calling surface; MCPUpdate is now a one-line wrapper
                        passing surfaceMCP (behaviour-preserving, its own existing tests pass
                        unmodified). New patchHandler + PATCH {prefix}/{slug} route in Register
                        (T242) — partial update over REST for typed Module[T] items, the typed
                        counterpart to dynamic content's own PATCH route; calls updateFields with
                        surfaceHTTP, not MCPUpdate directly — calling MCPUpdate itself would have
                        misreported every PATCH-over-REST update as MCP-originated, since it
                        hardcodes surfaceMCP; caught during implementation, not in the original
                        plan (Amendment A265, T242); resolveItem gains a FindByID fallback between
                        the slug lookup and the humanIDColumns fallback (T214) — FindByID is a
                        required Repository[T] method (unlike ColumnLookupRepository, which is
                        optional), so this closes the gap for every Module[T] type, not only the
                        four with a humanIDColumns entry; a real Node.ID passed via mcp's identArg
                        "id" key previously resolved nothing despite identArg's own doc comment
                        claiming id support (Amendment A266, T214)
├── forge.go          Config, MustConfig, New, App (Use/Content/Handle/Run/Handler/SEO),
│                     Registrator, SEOOption, seoState (robots/ogDefaults/appSchema), httpsRedirect,
│                     standaloneDispatcher internal interface (A101),
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
                      TokenStore startup probe in Handler() (Amendment A66);
                      App.Audit(AuditStore) *App, auditStore/auditHandlerReg fields,
                      GET /_audit lazy mount in Handler() (Amendment A97);
                      standaloneModules/standaloneReg fields, GET /{slug} + GET /{slug}/aidoc
                      dispatch in Handler() (Amendment A101);
                      App.PageMeta(*PageMetaStore) *App, pageMetaStore field,
                      App.GetPageMeta(ctx, path) Head,
                      setPageMetaStore push loop in Handler() (Amendment A157);
                      governanceModules []interface{setRoleStore(*RoleStore)} field;
                      App.Content registers modules via interface assertion;
                      App.Handler injects governance.RoleStore into all modules after
                        navTree/syncHook injection loops (A191, T49 Step 3)
└── head.go           Head (Title, Description, Author, Published, Modified, Image, Type,
                      Canonical, Tags, Breadcrumbs, Alternates, Social, NoIndex),
                      Image, Breadcrumb, Alternate, Headable, HeadFunc[T], ListHeadFunc[T],
                      Excerpt, URL, AbsURL, Crumbs, Crumb, rich-result constants,
                      TwitterCardType (Summary/SummaryLargeImage/AppCard/PlayerCard),
                      TwitterMeta, SocialOverrides;
                      HeadAssets (SEOOption), HeadLink, ScriptTag (Amendment A63);
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
                      smeldr:head receiver changed to TemplateData, twitter:site and
                      AppSchema auto-emitted (Amendment A61);
                      loadPartials, setPartials, parseOneTemplate accepts partials slice
                      (Amendment A62);
                      HeadAssets block in forgeHeadTmpl, setSEODefaults 3-arg,
                      HeadAssets propagated in render paths (Amendment A63);
                      resolveExtra, ContextFunc extra propagated in render paths (Amendment A65);
                      setPageMetaStore, renderListHTML PageMeta fallback when no ListHeadFunc
                      (Amendment A157)
└── pagemeta.go       PageMeta, PageMetaStore, NewPageMetaStore, CreatePageMetaTable
                      (smeldr_page_meta DDL); Set/Get/Delete/List store methods
                      (Amendment A157)
└── templatehelpers.go forgeMeta, forgeDate, forgeRFC3339, forgeMarkdown, forgeHTML, forgeExcerpt, forgeCSRFToken,
                      forgeLLMSEntries(data any), TemplateFuncMap();
                      Amendment A9 (parseOneTemplate uses .Funcs(TemplateFuncMap()));
                      smeldr_rfc3339 added (M5 Step 1) for article:published_time in smeldr:head;
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
└── integration_test.go 15 integration tests: HTML render cycle, smeldr:head, error pages,
                      CSRF round-trip, App-level SEO/sitemap, TemplateData correctness
└── integration_full_test.go cross-milestone test groups G1–G35 (M1–M4 + milestones 5–14 + A97 + A101):
                      multi-module routing, global middleware order, role guards,
                      AfterCreate/AfterDelete/isolation, content negotiation,
                      smeldr_meta/smeldr_markdown/BreadcrumbList, sitemap in robots.txt,
                      error template first-match + fallthrough, TemplateData siteName + request URL;
                      G33: audit trail lifecycle (AfterCreate excluded, AfterPublish recorded,
                      GET /_audit auth enforcement, content-type filter);
                      G34: SingleInstance routing (GET /prefix serves first Published item,
                      MCPMeta.SingleInstance=true, slug URL 404);
                      G35: Standalone routing (two modules, GET /{slug} dispatched by App,
                      draft not served to guest, list endpoint unaffected)

smeldr.dev/core/pgx  (separate module: ./pgx/)
└── pgx.go            Wrap(*pgxpool.Pool) smeldr.DB — native pgx adapter

smeldr.dev/mcp/  (separate repo: github.com/smeldr/mcp)
├── mcp.go            Server (secret []byte), New(app, opts...), ServerOption,
│                     WithSecret, WithModule(m smeldr.MCPModule) (D31);
│                     handle (JSON-RPC dispatch), handleInitialize,
│                     JSON-RPC wire types (jsonRPCRequest/Response/Error),
│                     mcpTool, mcpResource, allResources, mcpToolDefs,
│                     inputSchema, inputSchemaUpdate, hasMCPOp, slugOf, snakeCase
├── resource.go       handleResourceMethod, handleResourcesList,           ✅ Milestone 10 Step 2
│                     handleResourcesTemplatesList, handleResourcesRead,
│                     parseResourceURI; mcpResource/resourceContent/resourceTemplate
├── tool.go           handleToolMethod, handleToolsList, handleToolsCall,  ✅ Milestone 10 Step 3
│                     toolName, parseToolName, moduleForType, moduleForAdminList,
│                     authorise, authoriseEditor, errorFor, stringArg, toolResult;
│                     mcpAdminReadToolDefs (Amendment A54); delete→Editor auth (Amendment A55);
│                     list_{type}s suppressed for SingleInstance modules (Amendment A101)
├── transport.go      ServeStdio(ctx, in, out), Handler(),                 ✅ Milestone 10 Step 4
│                     sseHandler, messageHandler
└── README.md         AI-first integration guide: quick start, Claude/Cursor  ✅ Milestone 10 Step 5
                      config, SSE Bearer auth, MCPRead vs MCPWrite table

smeldr.dev/cli/  (separate repo: github.com/smeldr/cli)
├── client.go         Config{ForgeURL,Token,MCPURL}, loadConfig, loadEnvFile,
│                     request, getItem, mergeFields, printJSON, fatal
├── frontmatter.go    parseFrontmatter, parseFrontmatterFile — YAML-subset parser
├── content.go        runContentCommand, runCreate, runUpdate, runLifecycle,
│                     runDelete, runList, runGet, findKey, findKeyIn
├── token.go          runTokenCommand, mcpCall — create/list/revoke via MCP JSON-RPC
├── status.go         runStatus — GET /_health
├── media.go          runMediaCommand, runMediaUpload, runMediaList, runMediaDelete;
│                     buildMultipart/multipartRequest helpers; printMediaUsage
├── main.go           Entry point + top-level subcommand router
└── cli_test.go       Unit tests: frontmatter (9), mergeFields (2), loadEnvFile (3)
```

smeldr.dev/media/  (separate repo: github.com/smeldr/media)
```
├── media.go          MediaType constants (Image/Document/Video/Other), MediaRecord struct,
│                     MediaStore interface, LocalMediaStore + NewLocalMediaStore;
│                     CreateMediaTable, insertMedia, listMedia, getMediaByID,
│                     deleteMediaRecord; detectMIME, sniffMIME, detectMediaType,
│                     generateFilename, sanitizeFilename; writeJSON helper
├── server.go         Server struct, New(app, store) *Server,
│                     Register(app, store) *Server convenience constructor;
│                     HTTPHandler() http.Handler;
│                     handleUpload (POST /media — Author+; WCAG 1.1.1 description check),
│                     handleServe (GET /media/{filename} — public),
│                     handleList (GET /media — Editor+; ?type= filter),
│                     handleDelete (DELETE /media/{id} — Editor+)
├── mcp.go            Server implements smeldr.MCPModule;
│                     MCPMeta (TypeName="File", Prefix="/media"),
│                     MCPSchema (filename/data/description/media_type fields),
│                     MCPCreate (base64 decode → MIME detect → store → insert),
│                     MCPDelete; MCPList; MCPGet;
│                     MCPUpdate/MCPPublish/MCPSchedule/MCPArchive → ErrBadRequest
├── os_helpers.go     ensureDir, writeFile, removeFile, encodeJSON (test seams)
└── example_test.go   ExampleRegister — compile-verified minimal wiring pattern
```

### Shipped (Milestones 7–8)

```
├── storage.go (extend) SQLRepo[T] — production Repository[T] backed by smeldr.DB;
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

A request arriving at a Smeldr app passes through these layers in order.
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
│  smeldr.Context creation         │  ContextFrom(w, r)
│                                 │  Sets X-Request-ID (UUID v7 if absent)
│                                 │  Extracts User resolved by auth middleware
└────────────────┬────────────────┘
                 │
    ▼ GET / read only
┌─────────────────────────────────┐
│  Cache check                    │  smeldr.Cache(ttl) per-module LRU
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
 ├── errors.As(smeldr.Error) 4xx   →  status from error, public message
 ├── errors.As(smeldr.Error) 5xx   →  logged + generic 500
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

### `wireSignalBus` — called from `App.Handler()`, re-entrant not run-once (Amendment A224, T178)

`App.Handler()` now calls `a.wireSignalBus()` on every call, not only inside
`App.Run()`. Found while testing `App.Provenance()`'s wiring in
`example/server`: any caller that embeds `App.Handler()`'s `http.Handler` in
its own `http.Server` (or an `httptest.NewServer`-based in-process test
server) instead of calling the blocking `App.Run()` previously never got
`OnSignal` subscribers (`App.Webhooks`, `App.Audit`, `App.Provenance`, custom
`OnSignal` handlers) wired at all — a real gap, not hypothetical, since
`example/server`'s own `main()` happens to call `Run()`, but its test harness
(`buildTestServer`, `main_test.go`) does not and never had.

Deliberately **re-entrant, not guarded to run once**: `App.Content()` can add
modules to `hookableModules` between an early `Handler()` call and a later
one (this is exactly `example/server`'s own `buildApp` shape — an early
`app.Handler()` call precedes `RegisterOrchestrationTypes`'s `Content()`
calls in the same function), so every call re-wires every module currently
in `hookableModules`. Cheap and harmless to repeat — no HTTP route
registration happens in `wireSignalBus`, unlike most of `Handler()`'s other
one-time setups, so there is no double-registration panic risk to guard
against with a `!a.xRegistered`-style flag.

### X-Request-ID contract

- Set by `ContextFrom` on every request (UUID v7 if absent from inbound header)
- `RequestLogger` must be the outermost middleware to ensure it is set before any handler runs
- `WriteError` reads from the response header first, then falls back to the request header
- Appears in: response header, JSON error body, every `slog.Error` call

---

## Stable interfaces (public API contracts)

These interfaces are the extension points for users of Smeldr.
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
// Smeldr provides BearerHMAC, CookieSession, BasicAuth, and AnyAuth.
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

// Error — all Smeldr errors implement this
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

// CacheStore — exported LRU cache backing smeldr.Cache() and smeldr.InMemoryCache()
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

### Shipped (A94, A97)

```go
// AuditStore — implement to provide a custom audit persistence backend    (audit.go) ✅ A97
type AuditStore interface {
    Append(ctx context.Context, r AuditRecord) error
    List(ctx context.Context, f AuditFilter) ([]AuditRecord, error)
}
// Use NewAuditStore(db DB) for the built-in SQL implementation.
// Use CreateAuditTable(db DB) to create the smeldr_audit_log table.
// Wire via App.Audit(store AuditStore) — subscribes to AfterPublish, AfterSchedule,
// AfterArchive, AfterDelete. Mounts GET /_audit (Editor role required).

// OutboundDelivery — implement to provide a custom delivery backend       (outbound.go) ✅ A94
type OutboundDelivery interface {
    Enqueue(ctx context.Context, job OutboundJob) error
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
redirects.go    — depends on: errors, storage (smeldr.DB), forge.go (A20)       ✅ Milestone 7
├── redirectmanifest.go — depends on: redirects, cookiemanifest (manifestAuthOption), forge.go (A21) ✅ Milestone 7
sitemap.go      — depends on: node, signals                             ✅ Milestone 3
rss.go          — depends on: node, signals, head                       ✅ Milestone 5
ai.go           — depends on: node, head                                ✅ Milestone 5
social.go       — depends on: head                                      ✅ Milestone 5
scheduler.go    — depends on: node, signals, storage                    ✅ Milestone 8
webhook.go      — depends on: errors, auth, node, signals                ✅ Milestone 11
outbound.go     — depends on: errors, auth, signals                      ✅ Milestone 11
audit.go        — depends on: errors, auth, roles, storage               ✅ A97 (v1.22.0)
```

The dependency graph has no cycles. `errors.go` and `roles.go` are the only
true foundation files — everything else can depend on them freely.

---

## smeldr.Node embedding

Every content type embeds `smeldr.Node`. Embedding (not composition) is required
because Smeldr uses reflection to access Node fields directly:

```go
// smeldr reads these fields by name via reflection — do not rename them
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
Accept: text/html            → rendered template (or 404 for APIOnly modules)
Accept: text/markdown        → calls Markdown() if implemented, else 406
Accept: text/plain           → stripped plaintext version
```

The `Accept` header check uses pre-compiled content-type matching per module,
not string comparison on every request.

### Routing variants

| Option | GET /{prefix} | GET /{prefix}/{slug} | MCP tools |
|--------|--------------|----------------------|-----------|
| *(default)* | list (HTML or JSON) | show (HTML or JSON) | full set |
| `SingleInstance()` | first Published item (HTML or JSON) | not registered (404) | `list_{type}s` suppressed |
| `Standalone()` | list (HTML or JSON) | not registered; `GET /{slug}` dispatched by App | full set |
| `APIOnly()` | JSON only; `text/html` → 404 | JSON only; `text/html` → 404 | full set |

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

The LRU cache is per-module, not global. Each `smeldr.Cache(ttl)` call
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

## Storage and the smeldr.DB interface

Smeldr defines a minimal `smeldr.DB` interface internally:

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

`smeldr.Query[T]` and `smeldr.QueryOne[T]` accept `smeldr.DB`, not `*sql.DB`.
This means switching drivers requires changing exactly one value in `smeldr.Config`.

The `pgx` adapter lives at `smeldr.dev/core/pgx` — a separate
module. It imports both `smeldr` and `pgx/v5`. Smeldr core never imports pgx.

---

## Template data shape

```go
// show handler
TemplateData[T] {
    Content  T             // the single content item
    Head     smeldr.Head    // from item.Head() merged with module HeadFunc
    User     smeldr.User    // current user — zero value if Guest
    Request  *http.Request
}

// list handler
TemplateData[[]T] {
    Content  []T           // slice of items
    Head     smeldr.Head    // from module HeadFunc
    User     smeldr.User
    Request  *http.Request
}
```

---

## Testing

Every public interface has a test double:

```go
// In-memory repository — no database needed
repo := smeldr.NewMemoryRepo[*BlogPost]()

// Test context — no HTTP needed
ctx := smeldr.NewTestContext(smeldr.User{
    ID:    "test-user",
    Roles: []smeldr.Role{smeldr.Editor},
})

// Token for test requests — ttl=0 means no expiry
tok, _ := smeldr.SignToken(user, "test-secret", 0)

// Module integration test via httptest — no app.Run() required
repo := smeldr.NewMemoryRepo[*Post]()
m := smeldr.NewModule((*Post)(nil), smeldr.Repo(repo))
mux := http.NewServeMux()
m.Register(mux)
w := httptest.NewRecorder()
r := httptest.NewRequest(http.MethodGet, "/posts", nil)
mux.ServeHTTP(w, r)
```

Use `net/http/httptest` with `m.Register(mux)` for module integration tests.
Use `smeldr.NewTestContext()` with direct signal handler calls for unit tests.
`smeldr.App` / `app.Handler()` will be available from Milestone 2.

---

## External modules

These modules are maintained separately and consume the Smeldr core API
via the published interfaces documented above.

| Module | Role |
|--------|------|
| `smeldr.dev/mcp` | MCP server — exposes Smeldr content over JSON-RPC 2.0 / SSE |
| `smeldr.dev/media` | Media storage and serving; implements `smeldr.MCPModule` |
| `smeldr.dev/cli` | CLI admin tool — content CRUD, tokens, webhooks, audit |
| `smeldr.dev/social` | Social publishing scheduler (Twitter/X, LinkedIn, Mastodon) |
| `smeldr.dev/agent` | MIT-licensed agent runtime; `smeldr.dev/agent/flow` (AGPL-3.0) is the Smeldr integration adapter — subscribes to `App.OnSignal` and dispatches agent jobs in response to content lifecycle signals |

None of these modules are imported by Smeldr core. All integration is outbound:
Smeldr core defines the interfaces; external modules implement or consume them.
