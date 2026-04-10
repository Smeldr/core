# Changelog

All notable changes to Forge are documented in this file.

Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Versioning: [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

**API stability promise:** every exported symbol in `github.com/forge-cms/forge`
at v1.0.0 is stable. No breaking changes will be made without a new major version.
The zero-dependency policy and zero-reflection-at-request-time guarantee are
treated as part of the stability promise.

**Architectural rationale:** see [DECISIONS.md](DECISIONS.md) for the reasoning
behind every design choice. Amendments that changed existing behaviour are
cross-referenced below by their Amendment ID.

---

## [Unreleased]

Changes planned for v2 and beyond are tracked in [BACKLOG.md](BACKLOG.md)
under Milestone 10 and the v2+ Roadmap section.

---

## [1.9.1] — 2026-04-10

Inline link support in `mdInline` — `[text](url)` now renders as `<a href="url">text</a>`.

### Added

- `markdown.go`: `mdApplyLinks` — iterative `[text](url)` → `<a href="url">text</a>`
  replacement with a URL allow-list (`http://`, `https://`, `/` only). Any other
  scheme (e.g. `javascript:`, `data:`) is silently rejected and the original
  literal text is emitted unchanged. Called from `mdInline` after HTML escaping
  and before bold/code pattern application.

---

## [1.9.0] — 2026-04-07

Field format semantics: `forge_format` and `forge_description` struct tags (Decision 27).

### Added

- `MCPField.Format string` — populated from the `forge_format` struct tag; machine-readable
  format hint (`"markdown"`, `"html"`). Empty string when the tag is absent (Decision 27).
- `MCPField.Description string` — populated from the `forge_description` struct tag; free-text
  authoring guidance for AI agents. Empty string when the tag is absent (Decision 27).
- `forge-mcp`: tool input schemas now emit a `"description"` key in JSON Schema properties
  using the priority logic defined in Decision 27:
  both tags → `forge_description + " (" + forge_format + ")"`;
  format only → `"(" + forge_format + ")"`; neither → key omitted.

---

## [1.8.0] — 2026-04-06

Last-admin guard on token revocation (Decision 26).

### Added

- `forge.ErrLastAdmin` — sentinel error (HTTP 409 Conflict, code `"last_admin"`).
  Returned by `TokenStore.Revoke` when the token being revoked is the last active
  (non-revoked, non-expired) admin token (Decision 26).

### Changed

- `TokenStore.Revoke` now checks whether the target token is an admin token and,
  if so, whether at least one other active admin token exists. If the target would
  be the last active admin, `Revoke` returns `ErrLastAdmin` without modifying any
  row. All other revocations are unaffected (Decision 26).

---

## [1.7.0] — 2026-04-05

Trusted raw HTML passthrough for module templates (Amendment A67).

### Added

- `forge_html` template function — wraps a `string` as `template.HTML`, bypassing
  Go's automatic HTML escaping. Registered in `TemplateFuncMap` as `"forge_html"`.
  Use only for trusted content (e.g. pre-rendered video embeds, third-party
  iframes). User-supplied strings must never be passed without prior sanitisation
  (Amendment A67).

  Template usage:
  ```
  {{.Content.Embed | forge_html}}
  ```

---

## [1.6.0] — 2026-04-05

Named revocable bearer tokens backed by a `forge_tokens` table (Amendment A66).

### Added

- `forge.TokenRecord` struct — exported record type returned by `TokenStore.List`.
  Fields: `ID`, `Name`, `Role string`; `ExpiresAt`, `RevokedAt`, `CreatedAt time.Time`.
- `forge.TokenStore` struct + `forge.NewTokenStore(db DB, secret string) *TokenStore` —
  server-side token registry. Issues tokens via `Create`, enumerates them via
  `List`, and revokes them via `Revoke`. Backed by a `forge_tokens` table.
- `TokenStore.Create(ctx, name, role string, ttl time.Duration) (string, error)` —
  calls `SignToken`, stores a SHA-256 fingerprint in `forge_tokens`, and returns the
  plaintext token once. The plaintext is never persisted (Amendment A66).
- `TokenStore.List(ctx context.Context) ([]TokenRecord, error)` — returns all token
  records ordered newest first (Amendment A66).
- `TokenStore.Revoke(ctx context.Context, id string) error` — sets `revoked_at`;
  effective immediately on next request (Amendment A66).
- `forge.Config.TokenStore *TokenStore` optional field — wire a `TokenStore` into the
  App at startup. Nil by default (stateless HMAC mode unchanged) (Amendment A66).
- `App.TokenStore() *TokenStore` accessor — used by `forge-mcp` to inherit the store
  (Amendment A66).

### Changed

- `forge.VerifyBearerToken` signature extended from 2-arg to 3-arg:
  `VerifyBearerToken(r *http.Request, secret []byte, store *TokenStore) (User, bool)`.
  When `store` is `nil`, behaviour is identical to the previous version.
  When `store` is non-nil, the token fingerprint is looked up in `forge_tokens`;
  absent or revoked tokens are rejected (Amendment A66).

---

## [1.5.0] — 2026-04-04

Per-request extra data for module templates (Amendment A65).

### Added

- `forge.ContextFunc(fn func(ctx Context, item any) (any, error)) Option` —
  new module option. The function is called once per list and show render.
  Its return value is stored in `TemplateData.Extra` and is available in
  templates as `.Extra`. Errors from the function log and set `Extra` to nil
  — the render is never aborted (Amendment A65).
- `TemplateData[T].Extra any` — new field. Zero value is `nil` when no
  `ContextFunc` is configured. Templates access it as `{{.Extra}}`:
  ```
  {{- $nav := .Extra}}
  {{template "sidebar" $nav}}
  ```
  (Amendment A65).

---

## [1.4.0] — 2026-04-03

Embeddable head struct for custom handlers (Amendment A64).

### Added

- `forge.PageHead` — exported struct holding the four framework-owned head
  fields (`Head`, `OGDefaults`, `AppSchema`, `HeadAssets`). Embed `PageHead`
  in any custom handler data struct to enable `{{template "forge:head" .}}`
  without using `TemplateData[T]` (Amendment A64).

### Changed

- `forge.TemplateData[T]` — the four previously individual fields (`Head`,
  `OGDefaults`, `AppSchema`, `HeadAssets`) are now promoted from the embedded
  anonymous `PageHead` field. Existing templates access them identically
  (`.Head`, `.OGDefaults`, etc.) — zero breaking changes. Internally, struct
  literals must be updated to use `PageHead: forge.PageHead{...}` syntax
  (Amendment A64).

---

## [1.3.0] — 2026-04-03

Static linked assets in forge:head (Amendment A63).

### Added

- `forge.HeadAssets` — new `SEOOption` applied via `app.SEO()`. Injects
  preconnect hints, stylesheets, favicon `<link>` elements, and `<script>`
  tags into `forge:head` on every page. Assets are emitted in order:
  preconnect → stylesheets → favicons → scripts (Amendment A63).
- `forge.FaviconLink` — struct declaring a single `<link>` for a favicon or
  touch icon. `Rel` is required; `Type` and `Sizes` are omitted when empty
  (Amendment A63).
- `forge.ScriptTag` — struct declaring a single `<script>` element. `Src`
  loads an external script; `Body template.JS` inlines JavaScript when `Src`
  is empty. `Async` and `Defer` are only emitted for external scripts
  (Amendment A63).

---

## [1.2.0] — 2026-04-02

Shared template partials (Amendment A62).

### Added

- `App.Partials(dir string) *App` — registers a directory of partial templates
  (any `*.html` file) to be injected into every module template set. Files must
  use `{{define "name"}}...{{end}}` syntax. Loaded in alphabetical order.
- `App.MustParseTemplate(path string) *template.Template` — parses a single
  template file with `TemplateFuncMap`, `forge:head`, and all partials from the
  configured partials directory. Intended for custom `app.Handle()` route
  handlers (e.g. a home page). Panics on error, consistent with `MustConfig`.

---

## [1.1.9] — 2026-04-02

App-level Open Graph defaults and structured data (Amendment A61).

### Added

- `forge.OGDefaults` — new `SEOOption` applied via `app.SEO()`. Sets a
  fallback `og:image`, a `twitter:site` handle, and a fallback
  `twitter:creator`. Fallbacks are merged into each page's `Head` at render
  time; `twitter:site` is always emitted when set (Amendment A61).
- `forge.AppSchema` — new `SEOOption` applied via `app.SEO()`. Declares
  app-level JSON-LD structured data (e.g. `Organization`, `WebSite`) emitted
  automatically by `forge:head` on every page (Amendment A61).

### Changed

- `forge:head` partial now receives the full `TemplateData` value instead of
  just `Head`: update `{{template "forge:head" .Head}}` to
  `{{template "forge:head" .}}` in all templates. The partial's rendered output
  is identical for existing sites with no `OGDefaults` or `AppSchema`
  configured (Amendment A61).

---

## [1.1.8] — 2026-04-02

`forge.New()` now calls `MustConfig()` automatically, so configuration errors
(empty `BaseURL`, `Secret` too short) are always caught at process start, never
at first request (Amendment A60).

### Changed

- `forge.go`: `New()` calls `MustConfig(cfg)` as its first line; apps with
  invalid configuration that previously started silently will now panic at
  startup with a descriptive message. Godoc on `New()` updated to document the
  panic behaviour (Amendment A60).

---

## [1.1.7] — 2026-03-20

`/_health` is now exempt from the HTTPS redirect middleware so that reverse-proxy
health checks (e.g. Caddy `health_uri`) receive a `200` response on plain HTTP
(Amendment A59).

### Fixed

- `forge.go`: `httpsRedirect()` now short-circuits to `next.ServeHTTP` for
  `/_health` before checking TLS / `X-Forwarded-Proto`; previously a plain-HTTP
  health check from a co-located reverse proxy received a `301` redirect instead
  of `200`, causing the proxy to report the upstream as unhealthy (Amendment A59)

---

## [1.1.6] — 2026-03-20

`/_health` now reports framework versions sourced from the binary's embedded
build info; the application-supplied `"version"` key is removed (Amendment A58).
`App.Run()` emits a startup log line with the same version data before
`ListenAndServe`.

### Changed

- `forge.go`: `App.Health()` response no longer includes the `"version"` key
  driven by `Config.Version`; instead, `forgeVersions()` reads
  `runtime/debug.ReadBuildInfo()` at mount time and injects `"forge"` (and
  any companion-module keys such as `"forge_mcp"`) into the JSON — e.g.
  `{"status":"ok","forge":"1.1.6","forge_mcp":"1.0.5"}` (Amendment A58)
- `forge.go`: `App.Run()` calls `forgeVersions()` before starting
  `ListenAndServe` and emits a startup line to stderr, e.g.
  `forge: forge 1.1.6, forge_mcp 1.0.5` (Amendment A58)
- `forge.go`: `Config.Version` godoc updated — the field is retained for
  application authors but is no longer consumed by any built-in Forge endpoint
  (Amendment A58)

---

## [1.1.5] — 2026-03-20

`SQLRepo` now double-quotes all generated SQL identifiers, fixing runtime SQL
syntax errors when `db` tag values collide with reserved keywords such as
`order`, `group`, or `index` (Amendment A57).

### Fixed

- `storage.go`: `quoteIdent()` helper added; applied to every generated column
  reference in `SQLRepo.Save`, `FindAll`, `FindByID`, `FindBySlug`, and
  `Delete`; previously unquoted identifiers caused SQL syntax errors when a
  `db` struct tag used a reserved keyword (e.g. `db:"order"`) (Amendment A57)

---

## [1.1.4] — 2026-03-20

Add `forge.AbsURL(base, path string) string` helper for building absolute URLs
in `Head()` implementations (Amendment A56).

### Added

- `head.go`: `AbsURL(base, path string) string` — trims any trailing slash from
  `base`, passes `path` through `URL()` for normalisation, and concatenates;
  intended for use in `Head()` implementations when setting `Head.Canonical`,
  `Head.Image.URL`, or any other field that requires an absolute URL
  (Amendment A56)

---

## [1.1.3] — 2026-03-18

`negotiate()` now returns `text/html` when `Accept` is absent or `*/*` and
the module has templates configured, ensuring crawlers see HTML with structured
data in `<head>` (Amendment A53).

### Fixed

- `module.go`: `negotiate()` now returns `text/html` when `Accept` is
  absent or `*/*` and the module has templates configured; previously
  returned `application/json` unconditionally for these cases, causing
  Google Search Console and other crawlers to receive JSON instead of
  HTML and never see structured data in `<head>` (Amendment A53)

---

## [1.1.2] — 2026-03-17

`[]string` fields in content types are now correctly typed as `"array"` in
`MCPSchema` and MCP tool schemas; comma-separated string values from MCP clients
are automatically coerced to slices (Amendment A52).

### Fixed

- `module.go`: `mcpGoTypeStr` now returns `"array"` for `reflect.Slice` kinds;
  previously fell through to `"string"`, causing MCP clients to advertise and send a
  plain string for `[]string` fields which `json.Unmarshal` silently discarded
  (Amendment A52-1)
- `module.go`: new `coerceSliceFields` helper splits comma-separated string values
  for `[]string` struct fields before the `Marshal→Unmarshal` round-trip in
  `MCPCreate` and `MCPUpdate`, tolerating MCP clients that serialise multi-value
  fields as comma strings (Amendment A52-3)
- `forge-mcp/mcp.go`: `inputSchema` and `inputSchemaUpdate` now emit
  `{"type":"array","items":{"type":"string"}}` for array fields instead of
  `{"type":"array"}`, and suppress `minLength`/`maxLength`/`enum` constraints that
  apply to string entries but not arrays (Amendment A52-2)

---

## [1.1.1] — 2026-03-17

`forge:head` now emits the correct `twitter:card` value for article and product
content types (Amendment A51).

### Fixed

- `templates.go`: `forgeHeadTmpl` now emits `twitter:card = summary_large_image`
  when `Head.Type` is `"Article"` or `"Product"`, even when no image is provided;
  previously only a non-empty `Head.Image.URL` triggered the large-image card,
  causing OG/Twitter scrapers to render a small summary card for article-type
  content; `Head.Social.Twitter.Card` explicit override continues to take
  priority over the derived value (Amendment A51)

---

## [1.1.0] — 2026-03-17

`forge-mcp` — MCP support shipped (Milestone 10). New exported symbols in
forge core enabling AI assistants to discover and operate on content modules
via the Model Context Protocol.

### Added

- `mcp.go`: `MCPOperation` type; `MCPRead`, `MCPWrite` constants; `MCP(...)`
  option function; `MCPMeta` struct (`Prefix`, `TypeName`, `Operations`);
  `MCPField` struct (`Name`, `JSONName`, `Type`, `Required`, `MinLength`,
  `MaxLength`, `Enum`); `MCPModule` interface (`MCPMeta()`, `MCPSchema()`,
  `MCPList()`, `MCPGet()`, `MCPCreate()`, `MCPUpdate()`, `MCPPublish()`,
  `MCPSchedule()`, `MCPArchive()`, `MCPDelete()`)
- `module.go`: `Module[T]` implements `MCPModule` — all nine operations
  delegating to the existing repo, validation, signal, and lifecycle layers
- `forge.go`: `App.MCPModules() []MCPModule` — returns modules registered
  with `MCP(...)`
- `auth.go`: `VerifyBearerToken(r *http.Request, secret []byte) (User, error)`
  — validates HMAC Bearer tokens for SSE transport (Amendment A50)
- `context.go`: `NewContextWithUser(user User) Context` — production-safe
  background context for use by transport layers (Amendment A50)
- `forge.go`: `App.Secret() []byte` — exposes the app secret for transport
  layer token verification (Amendment A50)

---

## [1.0.11] — 2026-03-15

Manually published items now get a correct `PublishedAt` timestamp.

### Fixed

- `module.go`: `updateHandler` now sets `PublishedAt` to the current UTC time
  and re-saves when the status transitions to `Published`; previously
  `PublishedAt` remained at zero for all items published via PUT; the scheduler
  path was already correct (Amendment A48)

---

## [1.0.10] — 2026-03-15

`forge_markdown` now delegates to `renderMarkdown`, gaining full table support.

### Fixed

- `templatehelpers.go`: `forgeMarkdown` replaced with a one-line delegation to
  `renderMarkdown`; the `forge_markdown` template function now renders GFM
  tables, language-tagged fenced code blocks, `<hr>`, and all other elements
  supported by `renderMarkdown`; the previous stub had no table parsing
  (Amendment A47)

---

## [1.0.9] — 2026-03-15

Minimal Markdown→HTML renderer added to `TemplateFuncMap` with zero
dependencies.

### Added

- `markdown.go`: `renderMarkdown(s string) template.HTML` — XSS-safe
  Markdown→HTML converter supporting h1–h6, fenced code blocks with
  `class="language-〈lang〉"`, unordered lists, GFM tables, `**bold**`,
  `` `inline code` ``, blank-line `<p>` paragraphs, and `---` as `<hr>`;
  all content HTML-entity-escaped before tag wrapping; zero third-party
  dependencies (Amendment A46)
- `templatehelpers.go`: `TemplateFuncMap()` gains `"markdown"` key backed by
  `renderMarkdown`; existing `"forge_markdown"` is unchanged (Amendment A46)

---

## [1.0.8] — 2026-03-15

  Default authentication wired automatically in `New()`. Silent misconfiguration
  where a developer sets `Config.Secret` and uses `SignToken` but forgets to call
  `app.Use(forge.Authenticate(...))` now produces a working app instead of 403 on
  every write request.

  ### Added

  - `forge.go`: `Config.Auth AuthFunc` field — the `AuthFunc` used to authenticate
    all requests; when nil, Forge defaults to `BearerHMAC(Config.Secret)`
    automatically (Amendment A45)
  - `forge.go`: `New()` now prepends `Authenticate(auth)` as the first middleware in
    the app stack; replaces the need to call `app.Use(forge.Authenticate(...))`
    manually for the default bearer-token use case (Amendment A45)

  ### Changed

  - `Config.Secret` godoc updated to note that it drives the default `BearerHMAC`
    auth when `Config.Auth` is nil (Amendment A45)

---

## [1.0.7] — 2026-03-15

Bug fix: SQLRepo now correctly handles content types that embed `forge.Node`
or any other anonymous (embedded) struct.

### Fixed

- `storage.go`: `dbFields` / `collectDBFields` — `dbField.index` changed from
  `int` to `[]int` (reflect field index path); new recursive helper
  `collectDBFields` flattens promoted fields from embedded structs so that
  `SQLRepo.Save` no longer passes a raw struct value as a SQL argument
  (`"unsupported type forge.Node, a struct"`). All callers updated to use
  `reflect.Value.FieldByIndex` (Amendment A44)

---

## [1.0.6] — 2026-03-12

Health endpoint and application version field.

### Added

- `forge.go`: `Config.Version string` field — when non-empty, included in the
  `GET /_health` response as `{"status":"ok","version":"X.Y.Z"}`
- `forge.go`: `App.Health()` method — mounts `GET /_health`; explicit opt-in,
  not auto-mounted; returns `200 application/json`; no authentication required
  (Amendment A42)

---

## [1.0.5] — 2026-03-12

Hardening sweep: WriteError pipeline, SignToken error type, goroutine lifecycle,
debounce context correctness, and API naming consistency. All `http.Error`/`http.NotFound`
bypasses replaced, cache sweep goroutine terminates on graceful shutdown, debounce
callback no longer uses a cancelled request context, and two API symbols renamed for
convention consistency. No breaking changes except `FeedDisabled()` →
`DisableFeed()` (Amendment A40).

### Fixed

- `redirects.go`: `http.NotFound` and `http.Error(410)` bypasses replaced with
  `WriteError(w, r, ErrNotFound)` / `WriteError(w, r, ErrGone)` (Amendment A37)
- `redirectmanifest.go`: `http.Error(401)` bypass replaced with
  `WriteError(w, r, ErrUnauth)` (Amendment A37)
- `cookiemanifest.go`: `http.Error(401)` bypass replaced with
  `WriteError(w, r, ErrUnauth)` (Amendment A37)
- `sitemap.go`: `http.NotFound` and `http.Error(500)` bypasses replaced with
  `WriteError(w, r, ErrNotFound)` / `WriteError(w, r, ErrInternal)` (Amendment A37)
- `auth.go` (`encodeToken`): unreachable `json.Marshal` error path returned raw
  `fmt.Errorf`; returns `ErrInternal` (satisfies `forge.Error`, Amendment A38)
- `module.go` (cache sweep goroutine): goroutine spawned by `NewModule` had no
  exit path and leaked across graceful shutdown and test runs; now exits via
  `stopCh` select branch (Amendment A39)
- `module.go` (debounce callback): stashed request `Context` was cancelled before
  the 2-second debounce fired; `SQLRepo` queries silently failed on every write
  event in production; callback now builds `NewBackgroundContext(m.siteName)` at
  fire time; `debounceMu`/`debounceCtx` fields removed; `triggerSitemap(ctx)`
  renamed to `triggerRebuild()` (Amendment A41)
- `example/blog/main.go`: index template error handler used `http.Error`;
  corrected to `forge.WriteError(w, r, forge.ErrInternal)`

### Added

- `Module[T].Stop()`: exported idempotent method that closes `stopCh` (halts
  cache sweep goroutine) and calls `debounce.Stop()` (Amendment A39)
- `debouncer.Stop()`: cancels any pending `time.AfterFunc` timer (Amendment A39)
- `App.Run()` calls `Stop()` on all registered modules after `srv.Shutdown`
  returns; `stoppable` interface added (Amendment A39)

### Changed

- `FeedDisabled()` renamed to `DisableFeed()` for naming convention consistency
  (`forge.Verb(Noun)` pattern); `feedDisabledOption` internal type unchanged
  (Amendment A40)
- `forgeLLMSEntries` (unexported) renamed to `forgeLLMsEntries` to match
  `LLMsStore`/`LLMsEntry` casing convention; template tag `forge_llms_entries`
  is unchanged (Amendment A40)

---

## [1.0.4] — 2026-03-11

Fenced code block rendering, content negotiation capability gating (A35),
startup capability mismatch detection (A36), and example fixes. `forge_markdown` renders ` ``` `…` ``` ` fences as `<pre><code>`.
`negotiate()` now falls back to JSON instead of 406 when a client requests
`text/html` or `text/markdown` but the module lacks templates or `Markdownable`.
Both examples gain full working links on their welcome pages. No breaking API changes.

### Fixed

- `forge_markdown` / `forgeMarkdown` did not handle fenced code blocks; content
  between ` ``` ` fences was emitted as plain paragraph text; now rendered as
  `<pre><code>` with HTML escaping applied (XSS-safe)
- `module.go` content negotiation (`negotiate()`): returned `text/html` or
  `text/markdown` even when the module lacked templates / `Markdownable`; browsers
  and `Accept: text/html` clients received 406 Not Acceptable on JSON-only modules;
  fixed by gating on `n.html` and `n.md` capability flags instead of falling back
  to unsupported formats (Amendment A35)
- `example/docs`: module had no `SitemapConfig` option; `/docs/sitemap.xml` returned
  404; `forge.SitemapConfig{}` added to the module
- `example/docs/templates/index.html`: footer linked to `/docs/sitemap.xml`
  (404); corrected to `/sitemap.xml` (aggregate index)
- `example/api`: welcome page links to `/llms.txt`, `/llms-full.txt`,
  `/resources/sitemap.xml`, `/resources/feed.xml`, and `/robots.txt` returned
  404 or 406; module now includes `SitemapConfig{}`, `Feed(FeedConfig{...})`,
  `AIIndex(LLMsTxt, LLMsTxtFull)` options and `app.SEO(&RobotsConfig{Sitemaps: true})`
- `example/api`: `Resource` lacked `Head() Head`, so it did not satisfy
  `SitemapNode`; `regenerateSitemap` exited early; `/resources/sitemap.xml`
  returned 404; `Head()` added returning `forge.Head{Title: r.Title}`
- `example/api`: `Redirects(From("/resources/go-spec"), ...)` was registered as a
  fallback at `GET /`, but `GET /resources/{slug}` matched first; fixed by adding
  an explicit `app.Handle("GET /resources/go-spec", http.RedirectHandler(..., 301))`
  so the fixed-path pattern takes mux priority over the wildcard

### Added

- `module.go` (`NewModule`): two startup panics detect capability mismatches before
  any request is served (Amendment A36):
  - `SitemapConfig{}` given but `T` does not implement `SitemapNode` (missing
    `Head() forge.Head`) → panic with actionable message; previously `regenerateSitemap`
    exited silently and `/{prefix}/sitemap.xml` was always empty
  - `AIIndex(LLMsTxtFull)` given but `T` does not implement `Markdownable` (missing
    `Markdown() string`) → panic with actionable message; previously `/llms-full.txt`
    contained empty entries silently

---

## [1.0.3] — 2026-03-11

Startup rebuild for derived content. Sitemap fragments, RSS feeds, and AI index
entries are now populated from existing repository data at server start, so apps
with seed data or pre-loaded fixtures no longer require a manual publish event
to see correct output. No breaking API changes. (Amendment A34)

### Fixed

- `Module[T]` sitemap, feed, and AI index were only populated by the debouncer
  after a create/update/publish signal; items inserted directly into the
  repository (seed data, fixtures) never triggered regeneration; `App.Handler`
  now launches a one-shot goroutine that calls `rebuildAll` on every module
  after all stores are wired up (A34)

---

## [1.0.2] — 2026-03-11

Route mounting order fix. `GET /{prefix}/sitemap.xml` and `GET /{prefix}/feed.xml`
were never mounted because the guards in `Module.Register` checked the store pointer,
which is injected *after* `Register` returns. No breaking API changes. (Amendment A33)

### Fixed

- `Module[T].Register` guarded sitemap and feed route mounting on `m.sitemapStore != nil`
  and `m.feedStore != nil` respectively; both stores are always `nil` at registration
  time because `App.Content` calls `Register` before `setSitemap`/`setFeedStore`; routes
  are now mounted when the *config* is present and the store is read lazily at request
  time (A33)

---

## [1.0.1] — 2026-03-11

Error handling pipeline hardening. All six `http.Error` bypass sites removed;
four missing sentinels added; `errorTemplateLookup` race fixed; `Recoverer`
stack buffer increased. No breaking API changes. (Amendments A29–A32)

### Added

- `ErrBadRequest` (400 `bad_request`), `ErrNotAcceptable` (406 `not_acceptable`),
  `ErrRequestTooLarge` (413 `request_too_large`), `ErrTooManyRequests`
  (429 `too_many_requests`) sentinel errors — complete the framework's own
  HTTP status vocabulary (A29)
- `setErrorTemplateLookup` / `runErrorTemplateLookup` internal helpers that
  wrap `errorTemplateLookup` in a `sync.RWMutex`, eliminating the data race
  between `App.Handler()` start-up and in-flight requests (A29)
- `ERROR_HANDLING.md` — authoritative strategy document for error handling;
  required reading before any code that calls `WriteError` or adds a sentinel

### Fixed

- `respond()` used a direct type assertion `err.(*ValidationError)` instead of
  `errors.As`; a wrapped `*ValidationError` would have silently produced a 422
  response without field details (A29)
- `writeContent` had no `*http.Request`, forcing 406 responses via `http.Error`
  (plain text, no `X-Request-ID`); now receives `r *http.Request` and calls
  `WriteError(w, r, ErrNotAcceptable)` (A30)
- JSON decode failures in `createHandler` and `updateHandler` called
  `http.Error` (plain text, no `X-Request-ID`, always 400); now calls
  `WriteError` with `ErrRequestTooLarge` (413) when `*http.MaxBytesError` is
  detected, otherwise `ErrBadRequest` (400) (A30)
- `renderListHTML` and `renderShowHTML` called `http.Error` for nil template;
  now calls `WriteError(w, r, ErrNotAcceptable)` (A31)
- `RateLimit` called `http.Error` for 429 rate-limit responses (plain text, no
  `X-Request-ID`); now calls `WriteError(w, r, ErrTooManyRequests)` (A32)
- `Recoverer` stack capture buffer was 4096 bytes; deep stacks (recursive
  templates, chained middleware) were silently truncated; increased to 32 KB (A32)

---

## [1.0.0] — 2026-03-08

v1.0.0 stabilisation: test coverage audit, benchmarks, godoc pass, and three
reference example applications.

### Added

- `go test ./... -cover` coverage raised to ≥ 85%; targeted additions for
  `App.RedirectStore`, `TrustedProxy`, `CacheStore.Sweep`, `RedirectStore.Len`,
  `stripMarkdown`, `forgeLLMSEntries`
- `benchmarks_test.go`: 17 benchmarks covering hot paths across M1–M8;
  results in [BENCHMARKS.md](BENCHMARKS.md)
- Godoc improved on `type App` and all `App.*` methods (A18–A26); `SQLRepo[T]`
  method comments brought to parity with `MemoryRepo[T]`
- `example/blog/`: standalone blog — `Post` type, `SitemapConfig`, `Social`,
  `FeedConfig`, `AIIndex`, `On[*Post](AfterPublish)`, scheduled publishing
- `example/docs/`: standalone docs site — `Doc` type, `Headable`,
  `Markdownable`, `AIDocSummary`, `AIIndex(LLMsTxt, LLMsTxtFull, AIDoc)`,
  `RobotsConfig{AIScraper: AskFirst}`
- `example/api/`: standalone JSON API — `Resource` type, `Authenticate` +
  `BearerHMAC`, `Auth(Read(Guest), Write(Editor))`, `On[T](BeforeCreate)`,
  `Redirects`, `SecurityHeaders`, `RateLimit`

### Changed (Amendment A27)

- `middleware.go`: `Authenticate(auth AuthFunc) func(http.Handler) http.Handler`
  — middleware that populates `Context.User()` from an `AuthFunc` on every
  request; enables `Module[T]` role enforcement in production. Pairs with
  `BearerHMAC`, `CookieSession`, or `AnyAuth`.

---

## [0.8.0] — 2026-03-07

Scheduled publishing: automatic `Scheduled→Published` transition with signal
dispatch, sitemap regeneration, and feed rebuild.

### Added

- `scheduler.go`: `Scheduler` type, adaptive ticker (next-due interval, 60 s
  fallback), `schedulableModule` interface
- `module.go`: `Module[T].processScheduled` — transitions Scheduled items whose
  `ScheduledAt` is past to Published, assigns `PublishedAt`, fires `AfterPublish`,
  triggers sitemap and feed regeneration (Amendment A25)
- `forge.go`: `App` starts the scheduler before `ListenAndServe` and stops it
  after `srv.Shutdown` (Amendment A26)
- `NewBackgroundContext(host string) Context` — zero-value Context for use
  outside the HTTP request cycle, e.g. in scheduler callbacks (Amendment A24)

### Changed (Amendments A23, A25)

- `node.go`: `Node` time fields (`PublishedAt`, `ScheduledAt`, `CreatedAt`,
  `UpdatedAt`) carry `db:"..."` struct tags for `SQLRepo[T]` column mapping
  (Amendment A23)

---

## [0.7.0] — 2026-03-07

Production-ready SQL repository, redirect enforcement, chain collapse, and the
`/.well-known/redirects.json` inspect endpoint.

### Added

- `storage.go`: `SQLRepo[T any]` — production `Repository[T]` backed by
  `forge.DB`; struct-tag column mapping cached in `sync.Map`; `Table()` option
  for custom table names; full CRUD + `FindAll`/`FindBySlug` (Amendment A19)
- `redirects.go`: `RedirectCode` (`Permanent`, `Temporary`, `Gone`),
  `RedirectEntry`, `From` named type, `Redirects` module option, `RedirectStore`
  with O(1) exact + prefix lookups, chain collapse, optional DB persistence,
  `App.Redirect()`, `RedirectStore.Len()` (Amendments A20, A21)
- `redirectmanifest.go`: `/.well-known/redirects.json` always mounted, live
  serialisation of `RedirectStore`, `App.RedirectManifestAuth()` (Amendment A22)
- `forge.go`: `"/"` fallback handler wired from `redirectStore.handler()`
  (Amendment A20)

---

## [0.6.0] — 2026-03-07

Cookie consent enforcement and `/.well-known/cookies.json` compliance manifest.

### Added

- `cookies.go`: `CookieCategory` (`Necessary`, `Preferences`, `Analytics`,
  `Marketing`), `Cookie`, `SetCookie`, `SetCookieIfConsented`, `ReadCookie`,
  `ClearCookie`, `GrantConsent`, `RevokeConsent`, `ConsentFor`
- `cookiemanifest.go`: `/.well-known/cookies.json` typed JSON manifest,
  `ManifestAuth` option, `App.Cookies()`, `App.CookiesManifestAuth()`
  (Amendment A18)

---

## [0.5.0] — 2026-03-06

Open Graph, Twitter Cards, AI indexing (llms.txt + AIDoc), and opt-in RSS feeds.

### Added

- `social.go`: `Social` module option, `OpenGraph`, `TwitterCard`, card-type
  constants, `SocialOverrides`; `forge:head` partial renders OG and Twitter Card
  `<meta>` tags automatically when Social is registered
- `ai.go`: `AIIndex` module option; `LLMsTxt`, `LLMsTxtFull`, `AIDoc` flags;
  `LLMsStore`; `/llms.txt` compact index; `/llms-full.txt` full markdown corpus
  (requires `Markdownable`); `/{prefix}/{slug}/aidoc` per-item endpoint
  (requires `Markdownable`); `AIDocSummary` interface; `WithoutID` option;
  gzip compression in AI handlers (Amendment A17)
- `feed.go`: `Feed` module option, `FeedConfig`, `FeedDisabled`;
  `/{prefix}/feed.xml` per-module RSS 2.0; `/feed.xml` aggregate index;
  signal-driven regeneration (Amendment A16)

### Changed

- `Markdownable` interface (`Markdown() string`) moved from `module.go` to
  `ai.go`; consumed by `/llms-full.txt` and `/{slug}/aidoc`

---

## [0.4.0] — 2026-03-05

HTML rendering, template helpers, and content negotiation.

### Added

- `templatedata.go`: `TemplateData[T any]` (`Content`, `Head`, `User`,
  `Request`, `SiteName`), `NewTemplateData`
- `templates.go`: `Templates(dir)` / `TemplatesOptional(dir)` module options;
  `forge:head` partial (title, meta description, canonical, OG, Twitter Card,
  JSON-LD); error page template `forge:error`; HTML render path for list + show
- `templatehelpers.go`: `forge_meta`, `forge_date`, `forge_rfc3339`,
  `forge_markdown`, `forge_excerpt`, `forge_csrf_token`, `forge_llms_entries`;
  `TemplateFuncMap()` export

### Changed (Amendments A6, A7, A8, P3)

- Templates parsed once at `app.Run()` / `app.Handler()` startup; missing
  template files cause fast-fail (Amendment P3)
- `forge:head` emits `BreadcrumbList` JSON-LD when `Head.Breadcrumbs` is
  non-empty (Amendment A8)
- Error pages rendered via `forge:error` when available; fallback to
  `WriteError` plain text (Amendments A6, A7)

---

## [0.3.0] — 2026-03-03

SEO metadata, JSON-LD structured data, per-module sitemaps, and robots.txt.

### Added

- `head.go`: `Head`, `Image`, `Excerpt`, `Crumb`, `Crumbs`, `Breadcrumb`,
  `Headable` interface, `HeadFunc` module option
- `schema.go`: JSON-LD types — `Article`, `Product`, `FAQPage`, `HowTo`,
  `Event`, `Recipe`, `Review`, `Organization`, `BreadcrumbList`; `SchemaOf`
  serialises to `<script type="application/ld+json">`
- `sitemap.go`: per-module `/{prefix}/sitemap.xml`, `/sitemap.xml` aggregate
  index, `SitemapConfig` option, `SitemapStore`, `SitemapPrioritiser`
  interface, debounce-driven async regeneration (Amendment P1)
- `robots.go`: auto-generated `robots.txt`, `RobotsConfig`, `AskFirst` /
  `Disallow` AI-crawler policy constants, `App.SEO()`

---

## [0.2.0] — 2026-03-02

App bootstrap, HTTP server, graceful shutdown, and the `forge-pgx` companion
module.

### Added

- `forge.go`: `Config`, `MustConfig`, `New`, `App` (`Use`, `Content`, `Handle`,
  `Run`, `Handler`), `Registrator` interface, graceful shutdown on
  `SIGINT`/`SIGTERM`
- `forge-pgx` (`github.com/forge-cms/forge-pgx`): `forgepgx.Wrap(*pgxpool.Pool)
  forge.DB` — pgx/v5 adapter; no generated code, no ORM

---

## [0.1.0] — 2026-03-01

Foundation: the minimum needed to build a real application.
Zero third-party dependencies. All types in package `forge`.

### Added

- `errors.go`: `Error` interface, `ValidationError`, `Err`, `Require`,
  `WriteError`; sentinels `ErrNotFound`, `ErrGone`, `ErrForbidden`, `ErrUnauth`,
  `ErrConflict`
- `roles.go`: `Role`, `Guest`/`Author`/`Editor`/`Admin` (levels 10/20/30/40 —
  Amendment R1), `HasRole`, `IsRole`, `NewRole`, `Read`/`Write`/`Delete` options
- `node.go`: `Node`, `Status` (`Draft`, `Scheduled`, `Published`, `Archived`),
  `NewID` (UUID v7 — Amendment S1), `GenerateSlug`, `UniqueSlug`, `RunValidation`
- `context.go`: `User` (Amendment R3), `GuestUser`, `Context` interface,
  `ContextFrom`, `NewTestContext`
- `signals.go`: `Signal`, signal constants (`BeforeCreate`, `AfterCreate`,
  `BeforeUpdate`, `AfterUpdate`, `BeforeDelete`, `AfterDelete`, `AfterPublish`),
  `On[T]` generic option (Amendment S2), debouncer
- `storage.go`: `DB` interface, `Query[T]`, `QueryOne[T]`, `Repository[T]`
  interface, `MemoryRepo[T]`, `ListOptions`
- `auth.go`: `AuthFunc` interface (Amendment S8), `BearerHMAC`, `CookieSession`,
  `BasicAuth` (production warning — Amendment S7), `AnyAuth`, `SignToken`
  (ttl-aware — Amendment S10)
- `middleware.go`: `RequestLogger`, `Recoverer`, `CORS`, `MaxBodySize`,
  `RateLimit` (with `TrustedProxy` — Amendment S12), `SecurityHeaders`,
  `InMemoryCache`, `CacheStore`, `CSRF` (Amendments S6, S11), `Chain`
- `module.go`: `Module[T any]` (Amendment M3), `NewModule`, `At`, `Cache`,
  `Auth`, `Middleware`, `Repo`, `On`; content negotiation (`application/json`,
  `text/html`, `text/markdown`); per-module LRU; lifecycle enforcement
- `mcp.go`: `MCPOperation`, `MCPRead`/`MCPWrite`, `MCP()` no-op placeholder
  (reserved for Milestone 10)

---

## Version policy

Forge uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html):

- **MAJOR** — breaking change to any exported symbol in `github.com/forge-cms/forge`
- **MINOR** — new exported symbols; backward-compatible amendments
- **PATCH** — bug fixes with no API change

v1.0.0 and all future v1.x releases maintain full backward compatibility.
A v2 will be introduced as a separate import path
(`github.com/forge-cms/forge/v2`) following Go module conventions.

See [DECISIONS.md](DECISIONS.md) for the architectural rationale behind every
design choice in this release.
