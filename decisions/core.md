# Forge — Decisions: Core

Decisions 1–22 and all amendments up to A65. For Decision 25 onwards see `decisions/phase2.md`.


## Decision 1 — Node identity

**Status:** Locked  
**Decision:** Every content node has both a UUID (`ID`) and a URL-safe slug (`Slug`).
The UUID is the internal primary key. The slug is used in all URLs.
Slugs are auto-generated from the first `forge:"required"` string field unless set explicitly.

**Rationale:**
A slug-only identity is simple but fragile. Renaming a post title (and therefore its slug)
breaks all inbound links, internal references, and anything stored in external systems.
A UUID as the stable internal key means slugs can be changed freely without consequence.

A UUID-only identity is robust but produces ugly, unreadable URLs (`/posts/019242ab-...`)
that are bad for SEO, social sharing, and human memory.

The combination gives us the best of both: stable identity and readable URLs.

**Rejected alternatives:**
- *Slug only:* Renaming breaks links. No safe way to update content slugs.
- *Integer ID + slug:* Integers leak information (post count, creation order).
  UUIDs are opaque and safe to expose.

**Consequences:**
- `forge.Node` always has both `ID string` and `Slug string`
- Repository interface exposes both `FindByID` and `FindBySlug`
- Slug uniqueness must be enforced at the storage layer
- Slug collision on auto-generation appends a short suffix (e.g. `-2`)

---

## Decision 2 — Storage model

**Status:** Locked  
**Decision:** SQL-first. Forge provides `forge.Query[T]` and `forge.QueryOne[T]`
that handle struct scanning and mapping. The caller writes SQL.
No ORM. No query builder.

**Rationale:**
SQL is the most widely understood query language in existence.
It is unambiguous, composable, and directly optimisable.
AI assistants write SQL extremely well — better than most DSLs.

A query builder (`Where("published", true).OrderBy("created_at")`) looks elegant
but introduces a translation layer that fails on edge cases, produces suboptimal SQL,
and requires developers to learn two languages instead of one.

An ORM adds magic that hides performance problems and makes debugging harder.
The Go community has largely rejected ORMs in favour of `database/sql` and `sqlc`.
Forge aligns with this philosophy.

**Rejected alternatives:**
- *Simple CRUD interface only (Save/Find/Delete):* Insufficient for real filtering needs.
  A blog that cannot query "all published posts tagged 'go'" is not useful.
- *Query builder:* Elegant surface, complex implementation, leaky abstraction.
  Difficult to test. Difficult to explain to an AI assistant.
- *ORM:* Against Go philosophy. Hides complexity. Performance unpredictable.

**Consequences:**
- `forge.Query[T](db, sql, args...)` is the primary data access pattern
- `forge.QueryOne[T](db, sql, args...)` for single-item queries
- Forge maps columns to struct fields via `db` tag, then field name
- `forge.Repository[T]` interface remains for `MemoryRepo` and test doubles
- Developers are responsible for writing correct, performant SQL
- SQL injection prevention is the developer's responsibility (use parameterised queries)

---

## Decision 3 — Head/SEO ownership

**Status:** Locked  
**Decision:** Hybrid. The content type implements `Head() forge.Head` as the default.
The module can override with `forge.HeadFunc(...)` which takes precedence.

**Rationale:**
SEO metadata is fundamentally about content. A `BlogPost` knows its own title,
description, author, and type better than any external configuration.
Placing `Head()` on the content type keeps all knowledge about that content in one place.

However, there are legitimate cases where the module needs to override:
- Content types you don't own (third-party, generated)
- Head values that depend on request context (locale, A/B testing, user state)
- Site-wide title formatting (`Title + " — Site Name"`)

The hybrid model handles all cases without forcing complexity on the common path.

**Rejected alternatives:**
- *Content-type only (pull):* Cannot handle context-dependent metadata or
  content types you don't control.
- *Module-only (push):* Separates content knowledge from content type.
  `BlogPost` knows its title — it should own its head.

**Consequences:**
- Content types implementing `Head() forge.Head` get correct SEO automatically
- `forge.HeadFunc` on a module always wins over the content type's `Head()`
- Forge merges the two: module HeadFunc can call `content.Head()` and extend it
- Content types without `Head()` get a minimal head (title from slug, no structured data)
- `forge.Head` is a value type (struct), not an interface

---

## Decision 4 — Rendering model

**Status:** Locked  
**Decision:** Content negotiation via `Accept` header. Same route, same handler,
format determined by the request. JSON is the universal default.
HTML requires `forge.Templates(...)`. Markdown and plain text are always available.

**Rationale:**
A content API and a website are the same thing viewed differently.
Forcing developers to choose "am I building an API or a website" is a false constraint.
A well-built content system should serve all consumers: browsers, API clients, AI agents.

Content negotiation is a mature HTTP standard (RFC 7231). It is the correct mechanism.
Forge implementing it automatically means developers never think about it — they just
register templates if they want HTML, and everything else works.

**Rejected alternatives:**
- *HTML-first:* Marginalises API use cases. Modern sites are often headless.
- *API-first:* Requires a separate rendering layer for HTML. More code, more complexity.

**Consequences:**
- `Accept: application/json` → JSON response (always available)
- `Accept: text/html` → HTML via templates (requires `forge.Templates(...)`)
- `Accept: text/markdown` → raw markdown (requires `Markdown() string` method)
- `Accept: text/plain` → stripped plain text (always available, derived from content)
- `*/*` or missing `Accept` → JSON
- Forge sets `Vary: Accept` header automatically
- `forge.Head` metadata is embedded in HTML responses but not JSON responses
  (it is available as a separate `/_head/{slug}` endpoint for SPA use cases)

---

## Decision 5 — Cookie consent enforcement

**Status:** Locked  
**Decision:** Design-time enforcement. Cookie category determines which API is available.
`forge.Necessary` cookies use `forge.SetCookie`. All other categories
must use `forge.SetCookieIfConsented`, which silently skips if consent is absent.
There is no runtime error — the architecture makes the wrong thing impossible.

**Rationale:**
The question "what happens when you set a non-consented cookie?" arose during planning.
The correct answer is: that situation should not be reachable.

Runtime consent checks encourage developers to write `if hasConsent { setCookie(...) }` —
which is easy to forget, easy to get wrong, and impossible to audit.

Design-time enforcement via distinct API functions means:
1. A code review can confirm compliance by searching for `SetCookie` vs `SetCookieIfConsented`
2. An AI assistant can audit compliance by reading cookie declarations
3. The compiler enforces the contract — not tests, not runtime, not documentation

**Rejected alternatives:**
- *Silent skip at runtime (original proposal):* Correct behaviour but wrong mechanism.
  Relies on developers using the right function. Easy to bypass.
- *Runtime error:* Errors in cookie-setting paths are swallowed silently in practice.
  Creates noisy error handling for a non-exceptional case.
- *Queue (set when consent given):* Complex to implement. Requires Forge to hold state
  per user. The cookie category model makes this unnecessary.
- *Always set + log compliance violations:* Sets cookies without consent.
  Legally indefensible in GDPR jurisdictions.

**Consequences:**
- `forge.Necessary` cookies: use `forge.SetCookie`
- All other categories: use `forge.SetCookieIfConsented` which returns `bool`
- Consent state is stored in a `Necessary` cookie (so it is always readable)
- `forge.ConsentFor(r, forge.Preferences)` reads current consent state
- `/.well-known/cookies.json` provides a machine-readable compliance manifest
- Forge never touches third-party cookie consent — only cookies it sets itself

---

## Decision 6 — Context type

**Status:** Locked  
**Decision:** `forge.Context` is a custom interface that embeds `context.Context`
and adds Forge-specific methods: `User()`, `Locale()`, `SiteName()`,
`Request()`, `Response()`.

**Rationale:**
`context.Context` with typed keys is idiomatic Go but produces verbose, unsafe code:

```go
// stdlib approach — verbose and not type-safe at call sites
user := r.Context().Value(userKey).(forge.User)
```

`forge.Context` makes the common cases ergonomic and type-safe:

```go
// forge.Context approach
user := ctx.User()
```

Since `forge.Context` embeds `context.Context`, it is compatible with all stdlib
and third-party code that accepts `context.Context`. There is no lock-in.

`forge.Context` is the **only** non-stdlib type that appears in user-facing hook
and handler signatures. Everything else is either stdlib or the user's own types.

**Rejected alternatives:**
- *Pure `context.Context` with typed keys:* Correct but verbose. Difficult for AI
  assistants to generate correctly. Easy to make mistakes with key types.
- *`forge.Context` wrapping `*http.Request`:* Loses `context.Context` compatibility.
  Cannot be passed to functions that accept `context.Context`.

**Consequences:**
- All hooks receive `forge.Context` as first argument
- `forge.ContextFrom(r *http.Request) forge.Context` is the bridge from stdlib handlers
- `forge.Context` carries: `User`, `Locale` (default "en" until i18n v2), `SiteName`
- `forge.Context` is always non-nil — Forge guarantees this before calling user code
- Custom middleware that doesn't use Forge types uses plain `http.Handler` — no forced adoption

---

## Decision 7 — AIDoc format

**Status:** Locked  
**Decision:** AIDoc is a structured text format for serving content to AI agents.
Header delimiter: `+++aidoc+v1+++`. Body delimiter: `+++`.
Fields are `key: value` pairs, one per line. Body follows the closing delimiter.

```
+++aidoc+v1+++
type:     article
id:       019242ab-1234-7890-abcd-ef0123456789
slug:     hello-world
title:    Hello World
author:   Alice
status:   published
created:  2025-01-15T09:00:00Z
modified: 2025-03-01T14:22:00Z
tags:     [item1, item2]
summary:  One-line summary of the content.
+++
Body content here. Clean text or markdown.
```

**Rationale:**
AI agents consuming content via HTML waste tokens on navigation, ads, scripts, and markup.
JSON is verbose for long-form text (requires escaping). Markdown lacks structured metadata.

The AIDoc format is designed specifically for token efficiency and unambiguous parsing:
- The delimiter `+++aidoc+v1+++` is globally unique and immediately identifies the format
- Header fields are flat key-value — no nesting, no ambiguity
- ISO 8601 dates are unambiguous across all locales and LLM training data
- The version in the delimiter (`v1`) enables future evolution without breaking parsers
- Body content is clean text or markdown — no HTML noise

The delimiter style `+++aidoc+v1+++` was chosen over `---forge-aidoc-v1---` for brevity
while remaining unique and machine-identifiable.

**Rejected alternatives:**
- *JSON:* Verbose for long text. Requires escaping. Poor readability for humans.
- *Markdown with frontmatter:* YAML frontmatter is ambiguous and inconsistent.
  `---` delimiter conflicts with horizontal rules.
- *Plain text:* No structured metadata. Cannot be reliably parsed.
- *`---forge-aidoc-v1---`:* Longer delimiter, no functional advantage.

**Consequences:**
- Every Published content item gets `GET /{prefix}/{slug}/aidoc` automatically
- Draft/Scheduled/Archived content returns 404 on `/aidoc` endpoints
- `forge.RenderAIDoc(w, node)` is the internal rendering function
- Required fields: `type`, `id`, `slug`, `title`, `created`, `modified`
- Optional fields: `author`, `tags`, `summary` (populated if available on content type)
- Content types can implement `AIDocSummary() string` for a custom summary field
- The spec will live in `/spec/aidoc-v1.md` (created in Milestone 4 alongside the AIDoc implementation)

### Amendment B — AIDoc URL uses path segment (A15)

**Date:** 2026-03-06

The URL pattern changed from `/{prefix}/{slug}.aidoc` to `/{prefix}/{slug}/aidoc`.

Go’s `net/http.ServeMux` (Go 1.22+) requires that wildcard segments are complete
path components separated by `/`. A pattern like `{slug}.aidoc` contains a
wildcard followed by a literal suffix within the same segment — this is invalid
and causes a panic at route registration time.

`/{prefix}/{slug}/aidoc` is the Go-idiomatic equivalent: the slug is a full
segment and `aidoc` is a separate literal segment. It is unambiguous, parses
correctly, and does not conflict with any other module routes.

### Amendment A — Token optimisation (supersedes field list above)

**Date:** 2025-06-01

Three changes to reduce token count without introducing a new format or
sacrificing direct LLM readability:

**1. `status` field removed**
AIDoc endpoints only serve Published content — the status field always said
`published` and carried zero information. Removed.

**2. Compact date format**
Dates use `YYYY-MM-DD` instead of full ISO 8601 with time and timezone.
Time-of-day and timezone are rarely meaningful for AI content consumers.
Saves ~5 tokens per date field, ~10 tokens per document.

```
Before:  created:  2025-01-15T09:00:00Z    (10 tokens)
After:   created:  2025-01-15              (5 tokens)
```

**3. HTTP `Content-Encoding: gzip` on AIDoc responses**
Gzip is applied at the transport layer — not to reduce token count (the LLM
sees decompressed text) but to reduce network overhead during bulk crawling.
Long body content typically compresses 70–80%. Handled by middleware or
reverse proxy, not by `forge.RenderAIDoc` itself.
*(Superseded by Amendment A17: gzip is now applied directly by Forge’s AI endpoint handlers for compact, full, and AIDoc responses.)*

**Updated format:**

```
+++aidoc+v1+++
type:     article
id:       019242ab-1234-7890-abcd-ef0123456789
slug:     hello-world
title:    Hello World
author:   Alice
created:  2025-01-15
modified: 2025-03-01
tags:     [item1, item2]
summary:  One-line summary of the content.
+++
Body content here. Clean text or markdown.
```

**What was considered and rejected:**

- *Compact field names (`t:`, `s:`, `tl:`)* — saves ~30 tokens but introduces
  a new mini-syntax that is harder to document, debug, and explain. Not worth it.
- *Binary formats (MessagePack, CBOR)* — would require a tool-call to decode
  before the LLM can read it. More latency, not less. Defeats the purpose.
- *Separate `Accept: application/aidoc+v1+compact` variant* — two formats to
  maintain, document, and test. The three changes above achieve the same goal
  with no new surface area.

**Updated required fields:** `type`, `id`, `slug`, `title`, `created`, `modified`
(`status` removed, dates now `YYYY-MM-DD`)

---

## Decision 8 — llms.txt generation

**Status:** Locked  
**Decision:** Forge generates `/llms.txt` automatically from all registered modules.
Only Published content is included. The file regenerates on every publish/unpublish Signal.
Override by providing `templates/llms.txt` — Forge injects `{{forge_llms_entries .}}`.

**Rationale:**
`/llms.txt` is an emerging standard for helping AI systems efficiently understand
site structure without crawling every page. Generating it automatically ensures it
is always complete, always current, and never forgotten.

The template override gives developers full control for sites that need custom structure
(e.g. grouping by section, adding site-level context, restricting certain content types).

**Consequences:**
- `/llms.txt` is served automatically when any module has `forge.AIIndex(forge.LLMsTxt)`
- Format follows the llmstxt.org specification
- Only Published content appears — Forge enforces this regardless of template content
- Forge also serves `/llms-full.txt` with full content summaries (from `AIDocSummary()`)
- Template helper `{{forge_llms_entries .}}` renders all module entries

---

## Decision 9 — Sitemap strategy

**Status:** Locked  
**Decision:** Each module owns a fragment sitemap (e.g. `/posts/sitemap.xml`).
Forge merges all fragments into `/sitemap.xml` as a sitemap index.
Sitemaps regenerate via Signal on every publish/unpublish — not on-demand, not on a timer.

**Rationale:**
On-demand generation is correct but slow for large sites and hammers the database
on every Googlebot crawl. TTL-based caching is always slightly stale.

Event-driven regeneration gives us a sitemap that is always fresh (updated within
milliseconds of a publish action) without the performance cost of on-demand generation.
The sitemap is pre-computed and served as a static file.

Per-module fragment sitemaps keep each module's sitemap small and independently cacheable.
The sitemap index at `/sitemap.xml` ties them together — this is the Google-recommended
approach for large sites.

**Rejected alternatives:**
- *On-demand:* Correct but slow. Puts load on database during crawls.
- *TTL cache:* Always stale by up to TTL. Newly published content may not be indexed promptly.
- *Single sitemap:* Does not scale. Google recommends max 50,000 URLs per sitemap file.

**Consequences:**
- `forge.Signal` fires `SitemapRegenerate` after every `AfterPublish` and `AfterUnpublish`
- Sitemaps are written to a configurable directory (default: in-memory, optionally disk)
- Only Published content appears in sitemaps
- `PublishedAt` is used as `<lastmod>`
- Forge handles `ChangeFreq` and `Priority` from `forge.SitemapConfig`
- Custom `<priority>` per content type via optional `SitemapPriority() float64` method

---

## Decision 10 — Validation API

**Status:** Locked  
**Decision:** Hybrid. Struct tags handle simple constraints. `Validate() error` handles
business logic. Both run automatically before every Save. Tags run first.

```go
type BlogPost struct {
    forge.Node
    Title string `forge:"required"`
    Body  string `forge:"required,min=50"`
}

func (p *BlogPost) Validate() error {
    if p.Status == forge.Published && len(p.Tags) == 0 {
        return forge.Err("tags", "required when publishing")
    }
    return nil
}
```

**Rationale:**
Struct tags are concise for constraints that are universal to a field (`required`, `min`, `max`,
`email`, `url`). They are immediately visible at the field definition.

`Validate()` is necessary for:
- Cross-field validation (e.g. end date after start date)
- State-dependent validation (e.g. cover image required when publishing)
- Business rules that involve external state

The hybrid model gives developers the right tool for each case without forcing
everything into one mechanism.

**Rejected alternatives:**
- *Tags only:* Cannot express business logic. Cross-field rules are impossible.
- *`Validate()` only:* Verbose for simple constraints. Every content type must
  implement `required` checks manually.

**Supported tag constraints:**
```
forge:"required"           field must be non-zero
forge:"min=N"              string min length / number min value
forge:"max=N"              string max length / number max value
forge:"email"              valid email address
forge:"url"                valid URL
forge:"slug"               valid URL slug (a-z, 0-9, -)
forge:"oneof=a|b|c"        value must be one of the listed options (| separator — see Amendment R2)
```

**Consequences:**
- Tag validation runs before `Validate()` — if tags fail, `Validate()` is not called
- `forge.Err("field", "message")` returns a `*forge.ValidationError` with field context
- `forge.Require(err1, err2, ...)` collects multiple errors into one return value
- Validation errors produce HTTP 422 with a structured JSON body:
  `{"errors": [{"field": "tags", "message": "required when publishing"}]}`

---

## Decision 11 — Internationalisation

**Status:** Locked (deferred to v2)  
**Decision:** i18n is not implemented in v1. However, the architecture is designed
to accommodate it without breaking changes:
- `forge.Context` has `Locale() string` (returns `"en"` in v1)
- `forge.Head` has `Alternates []forge.Alternate` (empty in v1)
- URL structure uses prefix-agnostic patterns

**Rationale:**
Implementing i18n correctly requires decisions about URL structure (`/en/posts` vs
subdomains vs query parameters), content storage (one record per locale or separate records),
and `hreflang` tag generation. These decisions are complex and their consequences are
long-lived.

Building i18n incorrectly in v1 and having to break the API in v2 is worse than
deferring it. The current design ensures it can be added cleanly.

**Consequences for v1:**
- `ctx.Locale()` always returns `"en"`
- `head.Alternates` is always empty
- No `hreflang` tags are rendered
- URL patterns do not include locale prefix

**Planned for v2:**
- `forge.Locale` middleware that detects locale from URL, cookie, or Accept-Language
- `forge.Alternate` for hreflang tag generation
- Per-locale content variants or separate content types per locale (TBD in v2 planning)

---

## Decision 12 — Image type

**Status:** Locked  
**Decision:** `forge.Image` is a value type with four fields: `URL`, `Alt`, `Width`, `Height`.
No image processing, resizing, or optimisation in v1.

```go
type Image struct {
    URL    string // absolute or root-relative
    Alt    string // accessibility and SEO
    Width  int    // pixels, required for Open Graph
    Height int    // pixels, required for Open Graph
}
```

**Rationale:**
Open Graph requires image dimensions for optimal social sharing previews.
Twitter Cards benefit from knowing the image aspect ratio.
Without a typed `forge.Image`, developers store images as raw URL strings and
forget dimensions — producing degraded social previews.

A typed `forge.Image` struct nudges developers toward complete image metadata
without requiring any framework logic around storage or processing.

**Rejected alternatives:**
- *Raw string URL:* No dimensions. Degraded Open Graph. Missing alt text.
- *`forge.Image` with resizing middleware:* Out of scope for v1. Adds dependency on
  image processing library or external service. Deferred to v2 or a separate package.

**Consequences:**
- `forge.Image` zero value (empty URL) renders no image tags — safe to leave empty
- Forge renders `og:image:width` and `og:image:height` only when dimensions are non-zero
- `Alt` is recommended but not required (some images are decorative)
- Storage: `forge.Image` marshals to/from JSON as a nested object
- Database: store as JSON column or four separate columns (developer's choice)

---

## Decision 13 — RSS feeds

**Status:** Locked  
**Decision:** RSS feeds are generated automatically for any content module whose
content type has a `GetPublishedAt() time.Time` method. No configuration required.
The feed is served at `/{prefix}/feed.xml`.

**Rationale:**
RSS feeds are valuable for content discoverability and are expected by feed readers,
podcast apps, and aggregators. They are also useful for AI content indexing.

The auto-generation approach means developers never forget to add feeds, and feeds
are always correct — they use the same data as the sitemap and content API.

The `GetPublishedAt()` method is already present on `forge.Node` via the lifecycle
system (Decision 14). No additional interface is needed.

**Consequences:**
- Every module with `forge.AIIndex` or `forge.SEO` gets a feed automatically
- Opt out with `forge.Feed(forge.Disabled)` if needed
- Feed includes: title, description (from `Validate()` error or `Head().Description`),
  published date, author, categories (from tags)
- Only Published items appear in the feed
- Feed regenerates on the same Signal as the sitemap (AfterPublish, AfterUnpublish)
- Feed title defaults to module prefix (e.g. "Posts") — override with `forge.Feed(forge.FeedConfig{Title: "..."})`

---

### Amendment A16 — RSS opt-in (not auto-generated)

**Date:** 2026-03-06  
**Status:** Agreed  
**Amends:** Decision 13

**Change:** Decision 13 stated RSS feeds are auto-generated for every content module (opt-out with `forge.FeedDisabled()`). The agreed implementation is **opt-in**: a module must explicitly call `forge.Feed(forge.FeedConfig{...})` to get a feed.

**Rationale:**
- Explicit over implicit: admin modules, API-only modules, and single-record config modules should not silently sprout public `/feed.xml` endpoints.
- Consistent with `AIIndex` and `SitemapConfig` — both require explicit opt-in.
- `FeedDisabled()` is retained as a defensive explicit opt-out marker, useful when default behaviour changes in future or when subclassing patterns require it.

**Call-site impact:**
```go
// Before (Decision 13 intent — never implemented):
// Every module auto-gets /{prefix}/feed.xml

// After (implemented):
app.Content(&Post{},
    forge.At("/posts"),
    forge.Feed(forge.FeedConfig{Title: "Blog", Description: "Latest posts"}),
)
```

**Consequences of amendment:**
- Decision 13 "auto-generate" sentence is superseded by this amendment
- `FeedDisabled()` option exists but is a no-op when `Feed(...)` was never called
- `/feed.xml` (aggregate index) is only registered when at least one module calls `Feed(...)`
- No README examples are broken (feed was not yet documented as implemented)

---

### Amendment A17 — gzip applied directly in AI endpoint handlers

**Date:** 2026-03-06  
**Status:** Agreed  
**Amends:** Decision 13 (Amendment A, clause 3)

**Change:** Decision 13 Amendment A stated gzip on AIDoc responses would be “handled by middleware or reverse proxy, not by forge.RenderAIDoc itself.” That clause is superseded. Gzip compression is now applied directly by Forge’s AI endpoint handlers via the unexported `compressIfAccepted` helper in `ai.go`.

**Endpoints affected:** `/llms.txt` (`CompactHandler`), `/llms-full.txt` (`FullHandler`), `/{prefix}/{slug}/aidoc` (`aiDocHandler` → `renderAIDoc`).

**Behaviour:**
- When `Accept-Encoding: gzip` is present **and** the response body is ≥ 1024 bytes, the response is gzip-compressed.
- `Content-Encoding: gzip`, `Content-Length`, and `Vary: Accept-Encoding` headers are set on all three endpoints (Content-Length is set on plain responses too).
- Below 1024 bytes the plain body is returned — compression overhead would exceed the saving on small responses.

**Rationale:**
- `llms-full.txt` is a full Markdown corpus that can reach hundreds of KB on large sites; gzip saves 70–80% on the wire, meaningfully reducing crawl bandwidth.
- Requiring operators to wrap Forge AI handlers with a custom gzip middleware creates unnecessary friction and is inconsistent with the “production-ready by default” principle.
- The 1024-byte threshold aligns with the industry consensus used by NGINX, Cloudflare, Spring Boot, and Akamai for text/plain and text/markdown content (2026 defaults).
- The helper is scoped to AI endpoints only — HTML/JSON/RSS responses are not affected.

**Consequences of amendment:**
- `renderAIDoc` now takes `r *http.Request` as its second parameter (unexported function, no external API change).
- `compressIfAccepted` compresses into a `bytes.Buffer` first so `Content-Length` can be set before `WriteHeader`; `Content-Length` is also set on the plain (non-compressed) path for consistent HTTP hygiene.
- `gzipMinBytes = 1024` is an unexported package-level constant, accessible to tests in the same package.
- No change to the public `Option` API or any exported symbol.
- **Brotli is deferred:** Go's standard library has no `compress/brotli` package; adding a third-party dependency violates Decision 3. Revisit if stdlib adds brotli support or if a `forge-brotli` opt-in extension module is introduced.

---

### Amendment A18 — App.Cookies() and /.well-known/cookies.json wired into forge.go

**Date:** 2026-03-07  
**Status:** Agreed  
**Amends:** Decision 5 (Cookie consent enforcement)

**Change:** The compliance manifest (`/.well-known/cookies.json`) and the `App.Cookies()` / `App.CookiesManifestAuth()` entry points are implemented in `cookiemanifest.go` but require three additions to `forge.go`:
- `cookieDecls []Cookie` and `cookieManifestOpts []Option` fields on `App`
- `App.Cookies(decls ...Cookie)` method (append with name-based deduplication)
- `App.CookiesManifestAuth(auth AuthFunc)` method (sets manifest auth guard)
- `App.Handler()`: mounts `GET /.well-known/cookies.json` when `len(a.cookieDecls) > 0`

This crosses the file boundary from `cookiemanifest.go` into `forge.go`. It was pre-specified in `Milestone6_BACKLOG.md` §2.1 and §2.5 and agreed as part of the Milestone 6 plan.

**Consequences:**
- `App` gains two new exported methods (`Cookies`, `CookiesManifestAuth`) and three unexported fields.
- `/.well-known/cookies.json` is mounted lazily in `Handler()`, consistent with the sitemap/robots/llms-txt/feed pattern already established.
- When no declarations are registered, the endpoint is not mounted and returns 404.
- No change to `Option` interface, `Module`, or any content-serving path.

---

## Decision 14 — Content lifecycle

**Status:** Locked  
**Decision:** Lifecycle is built into `forge.Node` for all content types.
It cannot be opted out of. Four states: `Draft`, `Published`, `Scheduled`, `Archived`.
Forge enforces lifecycle rules automatically for all public endpoints, sitemaps, feeds,
and AI endpoints — regardless of developer configuration.

```go
const (
    Draft     Status = "draft"
    Published Status = "published"
    Scheduled Status = "scheduled"
    Archived  Status = "archived"
)
```

**Rationale:**
The question arose during planning: "should lifecycle be opt-in?"

The answer is no — and the reason is architectural safety. If lifecycle is opt-in
(via an interface), a content type that forgets to implement it has no protection.
Draft posts could leak to public endpoints, sitemaps, and AI crawlers.

Making lifecycle a compile-time impossibility to bypass is the only way to guarantee
the invariant: **non-Published content is never publicly visible**.

The cost is that all content types carry lifecycle fields even if they don't need them
(e.g. a `SiteConfig` type). This is a small, acceptable cost for an absolute guarantee.

**Scheduled publishing:**
Forge runs an internal ticker (default: every 60 seconds) that queries for
`status = 'scheduled' AND scheduled_at <= NOW()` and transitions matching items to
`Published`. This fires the `AfterPublish` Signal, which triggers sitemap and feed regeneration.

**Rejected alternatives:**
- *`forge.Publishable` interface (opt-in):* Correct behaviour but wrong mechanism.
  A content type that forgets to implement it has no protection.
- *Separate `forge.DraftContent` vs `forge.PublishedContent` types:* Creates a
  type-system split that makes generic handling impossible.

**Consequences:**
- `forge.Node.Status` is always present and always enforced
- Public GET endpoints return 404 for non-Published content (not 403 — do not leak existence)
- Editor+ can access non-Published content via the same endpoints when authenticated
- Author can access own Draft/Scheduled/Archived content when authenticated
- Sitemap, feed, AIDoc, and llms.txt never include non-Published content
- `<meta name="robots" content="noindex, nofollow">` is set for non-Published content

---

## Decision 15 — Role system

**Status:** Locked  
**Decision:** Hierarchical role system with four built-in roles and support for custom roles.
Higher roles inherit all permissions of lower roles.

```
Admin   (level 40)  →  full access including app configuration
Editor  (level 30)  →  create, update, delete any content — sees all drafts
Author  (level 20)  →  create, update own content — sees own drafts
Guest   (level 10)  →  read Published content (unauthenticated)
```

> **Note:** Levels use a spacing of 10 — see Amendment R1. Absolute values are not
> part of the public API; only relative ordering is guaranteed.

Custom roles are inserted into the hierarchy:
```go
forge.Role("moderator").Below(forge.Editor).Above(forge.Author)
```

**Rationale:**
Content management systems have well-understood role hierarchies.
An admin can do everything an editor can do. An editor can do everything an author can do.
This is the model every developer expects, and modelling it explicitly as a hierarchy
eliminates the need to list every role when specifying a permission.

`forge.Write(forge.Author)` meaning "Author, Editor, and Admin" is immediately obvious.
`forge.Write(forge.Role("author"), forge.Role("editor"), forge.Role("admin"))` is not.

RBAC (Role-Based Access Control with explicit permissions) was rejected because it
adds complexity that serves enterprise use cases Forge does not target.
It can always be layered on top via custom middleware for projects that need it.

**Rejected alternatives:**
- *String-only roles (no hierarchy):* Type-unsafe. Easy to typo. No inheritance.
  Every permission check must list all applicable roles.
- *RBAC with explicit permissions:* Powerful but complex. Wrong level of abstraction
  for Forge's target audience. Difficult to explain to AI assistants.

**Consequences:**
- `user.HasRole(forge.Editor)` returns true for Editor, Admin
- `user.Is(forge.Editor)` returns true only for exactly Editor
- `forge.Read(role)`, `forge.Write(role)`, `forge.Delete(role)` accept a minimum role level
- Guest is the implicit role for unauthenticated requests — never needs to be declared
- `forge.Admin` has access to `app.Config` endpoints (future: admin UI)
- Custom roles inserted into the hierarchy are fully composable with built-in roles
- Role is stored as a string in tokens and sessions for forward compatibility

---

## Appendix — Decisions not taken (Tier 3 roadmap)

The following topics were discussed and explicitly deferred to v2 or later:

**Admin UI** — A web-based admin interface for content management.
Planned as a separate package (`forge-admin`), not in core.
Blocked by: stable core API, role system (done), template system (done).

**Webhooks** — Outbound HTTP calls on content events.
Useful for search indexing, CDN invalidation, notification systems.
Will be implemented as a Signal handler in core, with a convenience wrapper.

**Search** — Full-text search over content.
SQLite FTS5 integration is the likely v1 path. Planned as an optional module.

**Multi-tenancy** — Multiple sites from one Forge instance.
Complex enough to require its own design phase. Not blocking v1.

**GraphQL** — Auto-generated GraphQL schema from content types.
Requires reflection or code generation. Likely a separate package.

**Edge/CDN integration** — Surrogate key support, automatic CDN purge on publish.
Signal-based approach makes this straightforward to add. Not blocking v1.

**Image resizing** — On-the-fly or pre-computed image variants.
Separate package. Core provides `forge.Image` type as the integration point.

---

## Addenda — Security & Performance review (2025-06-01)

The following amendments were added after a dedicated security and performance review.
Each is an amendment to an existing decision or a new sub-decision.

---

### Amendment S1 — UUID v7 (amends Decision 1)

**Decision:** Forge uses UUID v7 (time-ordered random) for all generated IDs, not UUID v4.

**Rationale:**
UUID v7 is time-ordered, which means database B-tree indexes stay compact and sequential
inserts do not cause page splits. UUID v4 is fully random — good for security but causes
index fragmentation at scale. UUID v7 provides the same security guarantees as v4
(122 random bits) while being naturally sortable by creation time.
This eliminates the need for a separate `created_at` index in many query patterns.

**Consequences:**
- `forge.NewID()` generates UUID v7 using stdlib `crypto/rand` for the random component
- The time component of UUID v7 must not be used as a security boundary
- Slug auto-generation remains unchanged

---

### Amendment S6 — CSRF protection (new, relates to Decision 6)

**Decision:** `forge.CookieSession` automatically enables CSRF protection.
Bearer token routes are exempt. Cookie-based write routes (POST, PUT, DELETE)
require a valid CSRF token.

**Mechanism:**
- Forge generates a CSRF token and stores it in a `Necessary` cookie (`forge_csrf`)
- The client must echo the token in either `X-CSRF-Token` header or `_csrf` form field
- Forge validates the token on all non-safe methods (POST, PUT, PATCH, DELETE)
- The CSRF token rotates on every successful authentication

**Consequences:**
- `forge.CookieSession` middleware automatically handles CSRF — no additional config
- `forge.BearerHMAC` routes skip CSRF validation entirely
- HTML templates get `{{forge_csrf_token}}` helper for form embedding
- AJAX clients read the token from the `forge_csrf` cookie and send it as `X-CSRF-Token`
- Opt out (strongly discouraged) with `forge.CookieSession(..., forge.WithoutCSRF)`

---

### Amendment S7 — BasicAuth production warning (amends Decision 15)

**Decision:** `forge.BasicAuth` logs a structured warning at startup when
`app.Config.Env` is not `forge.Development`.

**Warning output:**
```
WARN  forge: BasicAuth is enabled in a non-development environment.
      BasicAuth sends credentials on every request and has no session management.
      Consider forge.BearerHMAC or forge.CookieSession for production use.
```

**Consequences:**
- Warning fires once at `app.Run()`, not on every request
- Warning cannot be silenced without setting `Env: forge.Development`
- Does not prevent the application from starting

---

### Amendment S8 — AIDoc ID field is configurable (amends Decision 7)

**Decision:** The `id` field in AIDoc responses is included by default but
can be suppressed per-module with `forge.AIDoc(forge.WithoutID)`.

**Rationale:**
For most content types, exposing the UUID in AIDoc is harmless and useful
for AI agents that want to reference specific items. However, operators may
choose to omit it to reduce information exposure.

**Consequences:**
- Default: `id` field is present in all AIDoc responses
- `forge.AIIndex(forge.AIDoc(forge.WithoutID))` suppresses the `id` field
- All other AIDoc fields are always present and cannot be suppressed

---

### Amendment S9 — Cookie manifest access control (amends Cookie compliance)

**Decision:** `/.well-known/cookies.json` is public by default (intentional — compliance transparency).
Operators can restrict access with `forge.ManifestAuth(minRole)`.

```go
// Default — public
app.Cookies(SessionCookie, PreferenceCookie)

// Restricted
app.Cookies(SessionCookie, PreferenceCookie,
    forge.ManifestAuth(forge.Editor),
)
```

**Rationale:**
The manifest is designed for compliance auditing and should generally be public.
The option to restrict it exists for operators with specific security requirements.

**Consequences:**
- Default behaviour is unchanged — manifest is always public unless `ManifestAuth` is set
- When restricted, unauthenticated requests receive 401 (not 404 — do not hide the endpoint)

---

### Amendment S2 — Generic `On[T]` replaces exported `SignalHandler` (amends Decision 8)

**Decision:** `forge.On` is a generic function `On[T any](signal Signal, h func(Context, T) error) Option`.
The exported `SignalHandler` named type is removed. Internal dispatch uses an unexported
`signalHandler` type `func(Context, any) error`.

**Call-site syntax:**
```go
forge.On(forge.BeforeCreate, func(ctx forge.Context, p *BlogPost) error {
    p.Author = ctx.User().Name
    return nil
})
```

**Mechanism:** `On[T]` captures the typed handler in a closure at registration time:
```go
func On[T any](signal Signal, h func(Context, T) error) Option {
    return signalOption{signal: signal, handler: func(ctx Context, payload any) error {
        return h(ctx, payload.(T))
    }}
}
```
The type assertion `payload.(T)` appears exactly once, written by the framework, never by developers.

**Consequences for developer/AI experience:**
1. **Call-site syntax** — fully typed; no visible `any`, no assertion, matches README verbatim
2. **README** — no changes required; README already assumed this form
3. **AI generation accuracy** — AI assistants write `func(ctx forge.Context, p *BlogPost) error`
   directly; correct without consulting docs
4. **Consistency** — `On[T]` follows the same generic helper pattern as `Query[T]`/`QueryOne[T]`
   (Step 7); one pattern, applied everywhere

**Trade-off:** Internal dispatch stores `[]signalHandler` (erased type); this is invisible to
developers and confined entirely to signals.go.

---

### Amendment S3 — `Repository[T any]` and `MemoryRepo[T any]` use unconstrained type parameter (amends ARCHITECTURE.md)

**Decision:** `Repository[T any]` and `MemoryRepo[T any]` use an unconstrained type parameter
`[T any]`, not `[T forge.Node]`. `ARCHITECTURE.md` incorrectly specified `[T forge.Node]` —
Go generics do not support struct types as type constraints; only interfaces may appear there.
This is consistent with `Query[T any]`, `QueryOne[T any]`, and `On[T any]`.

**Call-site syntax:**
```go
type ArticleRepo = forge.MemoryRepo[Article]
```

**Consequences for developer/AI experience:**
1. **Call-site syntax** — identical; no impact on how the type is used
2. **ARCHITECTURE.md** — corrected in the same step; `Repository[T Node]` → `Repository[T any]`
3. **README.md** — corrected in the same step
4. **AI generation accuracy** — `[T any]` is the idiomatic Go pattern; AI assistants generate
   it correctly without consulting docs
5. **Consistency** — matches every other generic helper in the package

**Rule:** All generic helpers in the `forge` package use `[T any]`. Type safety is enforced by
the caller's concrete type argument, not by a package-level constraint.

---

### Amendment S8 — `AuthFunc` is an interface, not a named function type (amends Decision 15)

**Decision:** `forge.AuthFunc` is declared as an interface with one unexported method:

```go
type AuthFunc interface{ authenticate(*http.Request) (User, bool) }
```

The backlog originally specified `type AuthFunc func(r *http.Request) (User, bool)` (a named
function type). This is changed to an interface because two downstream steps require
capability detection on `AuthFunc` values without package-level globals:

- **Step 9 (middleware):** must detect whether a given `AuthFunc` enables CSRF validation
  (`csrfAware` interface with `csrfEnabled() bool`).
- **Step 11 (`app.Run`):** must detect whether a given `AuthFunc` should emit a production
  warning (`productionWarner` interface with `warnIfProduction(io.Writer)`).

With a named function type, both requirements demand a parallel registry (a `sync.Map` or
global slice keyed by function pointer) — fragile, not thread-safe at init time, and
impossible to test in isolation. With an interface, each concrete `AuthFunc` struct
implements whichever capability interfaces apply; detection is a simple type assertion.

**Call-site syntax** — identical before and after this amendment:
```go
app.Auth(forge.BearerHMAC(secret))
app.Auth(forge.CookieSession("forge_session", secret))
app.Auth(forge.BearerHMAC(secret), forge.CookieSession("forge_session", secret))
```

Developers never call `.authenticate()` directly — they only pass `AuthFunc` values to
factory functions and to `app.Auth(...)`.

**Consequences for developer/AI experience:**
1. **Call-site syntax** — unchanged; no visible difference at the point of use
2. **README** — no changes required; all factory-function examples remain valid
3. **AI generation accuracy** — AI assistants only write factory calls, never the interface
   method directly; correct code generated without consulting docs
4. **Consistency** — `AuthFunc` joins `Option` (roles.go) and `Signal` (signals.go) as an
   unexported-method interface; one pattern applied across all extension points
5. **Step 9/11 detection** — type assertions against `productionWarner` / `csrfAware`;
   clean, idiomatic, zero globals

**Rule:** `forge.AuthFunc` is an interface. Custom authentication schemes implement it by
declaring a struct and an unexported `authenticate(*http.Request) (User, bool)` method.

---

### Amendment P1 — Asynchronous sitemap regeneration (amends Decision 9)

**Decision:** Sitemap regeneration runs asynchronously in a dedicated goroutine.
A 2-second debounce coalesces burst publishes into a single rebuild.

**Mechanism:**
```
AfterPublish signal fires
    → resets debounce timer to T+2s
    → at T+2s, sitemap goroutine rebuilds all affected fragments
    → writes to in-memory store (optionally to disk)
    → updates /sitemap.xml index
```

**Consequences:**
- Publish requests return immediately — never blocked by sitemap I/O
- A burst of 50 simultaneous publishes produces one sitemap rebuild, not 50
- Maximum sitemap staleness after a publish: ~2 seconds
- If the app shuts down during a rebuild, the rebuild is lost (acceptable — next startup rebuilds)
- RSS feed regeneration uses the same goroutine and debounce

---

### Amendment M1 — Storage injection via forge.Repo[T any] Option (amends Decision 2)

**Decision:** `Module[T any]` receives its `Repository[T]` via `forge.Repo[T any](r Repository[T]) Option`.
This option is never written by application developers. `App.Content` (Step 11) calls it
internally after auto-creating a SQL-backed repository from `Config.DB` and type metadata.
Tests supply it directly using `forge.NewMemoryRepo[T]()`.

**Rationale:**
The README shows `app.Content(&BlogPost{}, forge.At("/posts"), ...)` with no visible repo argument.
A hidden injection mechanism (e.g., a method on `Module`) would require `Module[T]` to carry
a pointer that is only valid after `App.Content` completes registration — a partial construction
pattern that violates the invariant that all options are resolved at `NewModule` time.
The `Option` pattern resolves this cleanly: `App.Content` builds a `Repository[T]` from the DB
and calls `forge.Repo(repo)` as the last option before constructing the module. Call sites
that omit a `forge.Repo(...)` (e.g., in unit tests run without an App) get a clear panic at
construction time: `"forge: Module[T] requires a Repository; use forge.Repo(...)"`. This is a
fail-fast contract rather than a nil-dereference at first request.

**Consequences:**
- `forge.Repo[T any](r Repository[T]) Option` added to `module.go`
- `App.Content` (Step 11) always supplies `forge.Repo(repo)` — it is never a user concern
- Module construction panics if no `forge.Repo(...)` is provided (dev-time safety)
- Power users who need a custom repo (read-through cache, audit repo, etc.) can supply it

---

### Amendment M2 — Export CacheStore from middleware.go (amends Amendment P2)

**Decision:** The unexported `lruCache` type in `middleware.go` is promoted to an exported
`CacheStore` struct with an exported API: `NewCacheStore(ttl time.Duration, max int) *CacheStore`,
`Get(key string) (*cacheEntry, bool)`, `Set(key string, e *cacheEntry)`, `Flush()`, `Sweep()`.
`InMemoryCache` middleware is updated to use `*CacheStore` internally (no external behaviour
change). `Module[T]` holds a `*CacheStore` for module-level cache management with
signal-triggered invalidation via `Flush()`.

**Rationale:**
`forge.Cache(ttl)` on a module differs fundamentally from `forge.InMemoryCache(ttl)` middleware:
the module cache must be invalidated on write signals (AfterCreate/Update/Delete). The
middleware cache has no `Flush` method and no signal hooks. Sharing the implementation but
exposing a controlled public surface (`CacheStore`) avoids duplication and keeps both uses
aligned. Since `lruCache` was never exported, promoting it is backward-compatible.

**Exported API added to middleware.go:**
```go
type CacheStore struct { /* unexported fields */ }
func NewCacheStore(ttl time.Duration, max int) *CacheStore
func (c *CacheStore) Get(key string) (status int, header http.Header, body []byte, ok bool)
func (c *CacheStore) Set(key string, status int, header http.Header, body []byte)
func (c *CacheStore) Flush()
func (c *CacheStore) Sweep()
```

**`InMemoryCache` middleware is unchanged at the call site** — it creates its own `*CacheStore`
internally. `CacheMaxEntries(n)` option continues to work as before.

**Consequences:**
- `middleware.go` gains `CacheStore` exported type + `NewCacheStore` constructor
- `middleware_test.go` may reference `CacheStore` directly (optional)
- `module.go` uses `*CacheStore` for all module-level caching
- `forge.Cache(ttl)` option enables module caching; `forge.Middleware(forge.InMemoryCache(ttl))`
  is middleware-scoped caching — distinct concepts, clear in godoc

---

### Amendment M3 — Module[T any] type parameter (amends Step 10 spec)

**Decision:** `Module[T any]` uses the unconstrained `[T any]` type parameter, not
`[T forge.Node]`. The backlog spec was written before Amendment S3 locked all generic helpers
to `[T any]`. `Node` struct fields (`ID`, `Slug`, `Status`) are accessed at runtime via
reflection using the same `sync.Map`-keyed cache pattern established in `storage.go`.

**Field access pattern:**
```go
// Reflection helpers (unexported, module.go)
func nodeStatus(v any) Status { /* reflect field "Status" → Status */ }
func nodeSlug(v any) string   { /* reflect field "Slug" → string */ }
func nodeID(v any) string     { /* reflect field "ID" → string */ }
```

**Rationale:** Identical to Amendment S3 — a `forge.Node` type constraint creates a hidden
coupling between the generic type system and one concrete struct, excluding future content
types that embed `Node` via pointer or composition patterns not yet anticipated.

**Consequences:**
- `Module[T any]` — not `Module[T forge.Node]`
- Reflection helpers read `Status`, `Slug`, `ID` by name; reflect.Type cached in `sync.Map`
- `NewModule[T any](proto T, opts ...Option) *Module[T]` captures `reflect.TypeOf(proto)` once
- The Step 10 backlog spec text is updated to reflect `[T any]`

---

### Amendment M4 — MemoryRepo supports embedded struct fields (amends Step 7)

**Decision:** `stringField` in `storage.go` is updated to handle embedded struct field
promotion via `reflect.Type.FieldByName` with a `sync.Map`-backed path cache
(`goFieldPathCache`). The existing `goFields` map (flat field → index) is preserved
for internal use; `stringField` now uses the path-aware `goFieldPath` function.

**Rationale:**
`MemoryRepo` uses `stringField(v, "ID")` and `stringField(v, "Slug")` to locate
fields for keying and lookup. Content types always embed `forge.Node` rather than
declaring `ID`, `Slug`, `Status` as direct fields. The original `goFields` function
only scanned top-level fields via `t.NumField()`, missing promoted fields from
embedded structs. As a result, `Save` keyed all items by `""` (empty string),
causing every save to overwrite the same entry and `FindBySlug` to always return
`ErrNotFound`.

The new `goFieldPath(t, name)` function uses `t.FieldByName(name)` which correctly
traverses embedded structs. The returned `[]int` index path is cached per
`(reflect.Type, fieldName)` pair to avoid repeated reflection work.

**Impact on existing code:** Zero. The `repoItem` type used in `storage_test.go`
has flat fields — `FieldByName` returns the same single-element path `[i]` as
before, and all existing storage tests continue to pass.

**Consequences:**
- `goFieldPathCache sync.Map` added to `storage.go`
- `goFieldPathKey` unexported struct added as the cache key
- `goFieldPath(t reflect.Type, name string) []int` added to `storage.go`
- `stringField` updated to use `FieldByIndex(goFieldPath(...))` instead of
  `goFields` map with `Field(idx)`
- `goFields` is retained for potential future use (not removed)

---

### Amendment P2 — Cache eviction policy (amends Middleware)

**Decision:** `forge.InMemoryCache` implements LRU eviction with a configurable
maximum entry count (default: 1000 entries).

**Mechanism:**
- Entries are evicted in LRU order when `maxEntries` is reached
- TTL expiry check runs on every read (lazy expiry) plus a background sweep every 60 seconds
- Cache size is bounded: `maxEntries × avgResponseSize` is the approximate memory bound

```go
// Default — 1000 entries, LRU eviction
forge.InMemoryCache(5*time.Minute)

// Custom max entries
forge.InMemoryCache(5*time.Minute, forge.CacheMaxEntries(500))
```

**Consequences:**
- Memory usage is bounded — no unbounded growth from query parameter explosion
- LRU implementation uses a doubly-linked list + map (stdlib-only, ~40 lines)
- Cache keys include the full URL including query parameters
- `X-Cache: HIT` / `X-Cache: MISS` headers are always set

---

### Amendment P3 — Template parsing at startup (amends Decision 4)

**Decision:** Templates are parsed at `app.Run()`, not lazily on first request.
A missing or invalid template causes an immediate, descriptive startup failure.

**Rationale:**
Lazy parsing means a template error surfaces only when the relevant route is first hit —
potentially in production, under load, observed by real users.
Eager parsing at startup provides a fast feedback loop: the application either starts
correctly or fails with a clear error message.

**Startup behaviour:**
```
app.Run() →
    parse all registered templates →
    if any template fails: log error + exit(1) →
    otherwise: start HTTP server
```

**Consequences:**
- Template errors are caught before any traffic is served
- `forge.Templates("templates/posts")` validates that both `list.html` and `show.html` exist
- Missing template directory → startup failure with path in error message
- `forge.TemplatesOptional("templates/posts")` exists for cases where HTML is truly optional
- Hot-reload in development: `forge.TemplatesWatch("templates/posts")` re-parses on file change

---

### Amendment R1 — Role levels use spacing of 10 (amends Decision 15)

**Decision:** Built-in role levels are assigned in multiples of 10 (Guest=10, Author=20,
Editor=30, Admin=40) rather than consecutive integers (1, 2, 3, 4).

**Rationale:**
With consecutive levels, registering a custom role between two adjacent built-ins
(e.g. between Author=2 and Editor=3) is mathematically impossible — there is no integer
strictly between 2 and 3. The fluent builder API in Decision 15 (`Above(Author).Below(Editor)`)
would silently produce an incorrect level (the last call wins, resulting in the
lower bound rather than a midpoint).

Spaced levels (10, 20, 30, 40) leave nine slots between every pair of adjacent
built-in roles, making the intent of the builder API correct and testable.

**Consequences:**
- `levelOf(Guest)=10`, `levelOf(Author)=20`, `levelOf(Editor)=30`, `levelOf(Admin)=40`
- Custom roles inserted with `Above(Author).Below(Editor)` receive level 29 (Editor−1),
  which is correctly > 20 (Author) and < 30 (Editor)
- The absolute numeric values of levels are **not part of the public API**;
  only relative ordering is guaranteed
- `TestRoleLevel` asserts the concrete values 10/20/30/40 and must be updated if
  built-in levels are ever renumbered (which requires a new amendment)

---

### Amendment R3 — `forge.User` is defined in `context.go` (amends Decision 21)

**Decision:** The `forge.User` struct is defined in `context.go` (Layer 1), not in
`auth.go` (Layer 3).

**Rationale:**
`forge.Context.User()` returns `forge.User`. `context.go` is in Layer 1 (depends on
roles only). `auth.go` is in Layer 3 (depends on context, node, signals, storage).

Defining `forge.User` in `auth.go` would create a forward reference: context.go (Layer 1)
would need to reference a type from auth.go (Layer 3), violating the dependency layer rules
in ARCHITECTURE.md.

Moving the declaration to `context.go` resolves this cleanly:
- `forge.User` only depends on `forge.Role` (Layer 0) — it fits in Layer 1
- `auth.go` builds on top of the User type without moving it
- The User struct is a pure data type with no behaviour; behaviour (token signing,
  password hashing, session management) belongs in auth.go

**Consequences:**
- `forge.User struct { ID, Name string; Roles []Role }` declared in `context.go`
- `forge.GuestUser` zero-value var also in `context.go`
- `auth.go` uses `forge.User` as its primary identity type without re-declaring it
- Tests that construct users import nothing beyond the `forge` package (no auth dependency)

---

### Amendment R2 — `oneof` tag uses `|` as value separator (amends Decision 10)

**Decision:** The `oneof=` tag constraint uses `|` (pipe) as the separator between
allowed values, not `,` (comma) as shown in the Decision 10 example.

**Rationale:**
The `forge:"..."` tag parser splits the entire tag value on `,` to find individual
constraints. A tag such as `forge:"oneof=draft,published,archived"` would be parsed as
three separate constraints — `oneof=draft`, `published`, and `archived` — the last two
being unrecognised keys that trigger a panic.

Using `|` as the within-`oneof` separator avoids this ambiguity entirely:
```
forge:"required,oneof=draft|published|archived"
```

**Consequences:**
- Decision 10 example `forge:"oneof=a,b,c"` becomes `forge:"oneof=a|b|c"`
- The parsing rule is: split the tag on `,`; for any part starting with `oneof=`,
  split the remainder on `|` to get the allowed values
- `|` is not a valid value in any Forge-managed string field, so no escaping is needed
- Documentation and examples must use `|` consistently

---

## Decision 16 — Error handling model

**Status:** Locked
**Date:** 2025-06-01

**Decision:** Forge uses a typed error hierarchy. All Forge errors implement
`forge.Error` — an interface that carries an HTTP status code, a machine-readable
code, and a public-safe message. Internal error details are never exposed to clients.
Every request gets a `X-Request-ID` (UUID v7) header for end-to-end traceability.

### Error interface

```go
type Error interface {
    error
    Code()       string  // machine-readable: "not_found", "validation_failed"
    HTTPStatus() int     // correct HTTP status code
    Public()     string  // safe to show to the client
}
```

### Sentinel errors

```go
var (
    ErrNotFound   = forge.NewError(404, "not_found",   "Not found")
    ErrGone       = forge.NewError(410, "gone",        "This content has been removed")
    ErrForbidden  = forge.NewError(403, "forbidden",   "Forbidden")
    ErrUnauth     = forge.NewError(401, "unauthorized","Unauthorized")
    ErrConflict   = forge.NewError(409, "conflict",    "Conflict")
)
```

### Validation errors

```go
forge.Err("title", "required")                 // single field error → 422
forge.Require(forge.Err(...), forge.Err(...))  // multiple field errors → 422
```

### Error response format (follows Accept header — Decision 4)

JSON (`Accept: application/json`):
```json
{
  "error": {
    "code":       "validation_failed",
    "message":    "Validation failed",
    "request_id": "019242ab-1234-7890-abcd-ef0123456789",
    "fields": [
      { "field": "title", "message": "required" },
      { "field": "body",  "message": "minimum 50 characters" }
    ]
  }
}
```

HTML (`Accept: text/html`): rendered via `templates/errors/{status}.html` if present,
otherwise Forge renders a minimal built-in error page.

### Internal error handling

- Unknown errors (`fmt.Errorf(...)` from hooks or services) → `500 Internal Server Error`
- Internal error details are logged with `slog.Error` including `request_id`
- Client receives only: `{ "error": { "code": "internal_error", "message": "Internal server error", "request_id": "..." } }`
- Panics are caught by `forge.Recoverer()` middleware, logged, and returned as 500

### Error chain in hooks

Forge inspects errors returned from hooks using `errors.As`:
```
forge.Error with HTTPStatus 4xx  →  returned directly to client
forge.Error with HTTPStatus 5xx  →  logged + generic 500 to client
forge.ValidationError            →  422 with field details
any other error                  →  logged + generic 500 to client
```

### Request ID

- UUID v7 generated for every request
- Set as `X-Request-ID` response header always
- If request arrives with `X-Request-ID` header, Forge uses and echoes that value
  (useful for tracing across services)
- Available in `forge.Context` via `ctx.RequestID()`
- Included in all error responses and all structured log entries

**Rationale:**
A single error interface with HTTP status embedded eliminates the switch statements
that litter most Go web codebases (`if errors.Is(err, ErrNotFound) { w.WriteHeader(404) }`).
The handler just calls `forge.WriteError(w, r, err)` and the right thing happens.

Request IDs are the minimum viable observability primitive. They cost nothing and
make the difference between "we got a 500" and "we got a 500, here is every log line
for that exact request".

**Consequences:**
- `forge.WriteError(w, r, err)` is the one function all handlers call on error
- Error templates live in `templates/errors/404.html`, `templates/errors/500.html` etc.
- `forge.Context.RequestID()` is available in all hooks and custom handlers
- `slog` structured logging always includes `request_id` field

---

## Decision 17 — Redirects and content mobility

**Status:** Locked
**Date:** 2025-06-01

**Decision:** Forge automatically maintains a redirect table for all content modules.
When a node's slug or prefix changes, Forge records the previous path and serves
the appropriate redirect automatically. Archived and deleted content always returns
`410 Gone` — never `404`.

### Automatic behaviours

| Event | Previous path response |
|-------|----------------------|
| Slug renamed | `301 Moved Permanently` → new slug |
| Prefix changed | `301 Moved Permanently` → new prefix + slug |
| Node archived | `410 Gone` |
| Node deleted | `410 Gone` |
| Node scheduled | `404 Not Found` (does not exist yet — no redirect) |
| Node drafted (unpublished) | `404 Not Found` (does not leak existence) |

### Redirect table

The redirect table is stored alongside content. Each entry:

```go
type RedirectEntry struct {
    FromPath   string    // e.g. "/posts/helo-world"
    ToPath     string    // e.g. "/posts/hello-world" — empty string means 410
    StatusCode int       // 301 or 410
    NodeID     string    // UUID of the node — stable across renames
    CreatedAt  time.Time
}
```

The table is keyed by `FromPath`. On every request that results in a 404,
Forge checks the redirect table before returning. If a match is found:
- `ToPath` non-empty → redirect with `StatusCode`
- `ToPath` empty → `410 Gone`

### Request resolution order

```
Request arrives at /posts/old-slug
  1. Find published node with slug "old-slug" in module "/posts"
  2. Not found → check redirect table for "/posts/old-slug"
  3. Redirect found → serve 301 or 410
  4. No redirect found → serve 404
```

### API

```go
// Default — automatic, no configuration needed
app.Content(&BlogPost{},
    forge.At("/posts"),
)

// Explicit bulk redirect when changing a module's prefix
app.Content(&BlogPost{},
    forge.At("/articles"),                     // new prefix
    forge.Redirects(forge.From("/posts")),     // 301 all old /posts/* URLs
)

// Manual one-off redirect
app.Redirect("/old-path", "/new-path", forge.Permanent)
app.Redirect("/removed", "",            forge.Gone)
```

### 410 vs 404 — rationale

`410 Gone` tells search engines that content was *intentionally* removed.
Google removes `410` pages from its index significantly faster than `404` pages.
For a CMS, archived and deleted content should always be `410` — the content
existed, was indexed, and has been deliberately retired.

`404` is reserved for paths that never existed or content that is not yet published.
Leaking that a draft exists (by returning `410` instead of `404`) would be a
security issue — Forge always returns `404` for draft and scheduled content.

**Rationale:**
Redirect management is one of the most neglected aspects of CMS development.
Developers rename slugs during editing, reorganise content into new sections,
and archive old posts — and silently break every inbound link and SEO ranking
in the process. Making redirect tracking automatic and default means it is
never forgotten.

The UUID as stable internal identity makes this possible: even if a post is renamed
three times, Forge can trace the chain back and redirect any historical URL to the
current canonical URL.

**Consequences:**
- Redirect table is populated automatically by Forge on every slug/prefix change
- Redirect table entries are included in content exports and migrations
- `forge.Context` has no special redirect API — it is fully automatic
- Redirect chains are collapsed: A→B→C becomes A→C (avoids redirect chains)
- Maximum redirect chain length before collapse: 1 (Forge always points to current URL)
- Redirect table can be inspected at `GET /.well-known/redirects.json` (Editor+)

---

## Decision 18 — Licensing strategy

**Status:** Locked
**Date:** 2025-06-01

**Decision:** MIT license at launch. Dual-license model introduced when Forge Cloud
is ready for commercial offering. The project lives under the `forge-cms` GitHub
organisation from day one — not a personal namespace.

### Phase 1 — MIT (now)
All usage permitted without restriction. Maximum adoption, zero friction.
No legal review required for enterprise evaluation.

### Phase 2 — Dual license (when Forge Cloud launches)
```
MIT         →  open source projects, personal use, startups
Forge Pro   →  commercial hosted use, enterprise support, SLA
```
The MIT-licensed core remains unchanged. Forge Pro is a commercial license
for organisations running Forge as a hosted service for others.

**Rationale:**
A restrictive license (AGPL, BSL) at launch would reduce adoption before
there is anything to protect. The community and trust built under MIT
becomes the moat — not the license. The dual-license model is introduced
only when a commercial product exists to sell.

**Consequences:**
- `go.mod` module path: `github.com/forge-cms/forge`
- All documentation references `forge-cms` organisation
- `LICENSE` file is MIT from commit 1
- A `COMMERCIAL.md` file is added at launch explaining future dual-license intent
  so it is never a surprise to contributors or users
- Contributors sign a CLA (Contributor License Agreement) from day one —
  this is required to relicense later without contacting every contributor

### On the CLA

A CLA is a legal agreement where contributors grant the project owner the right
to relicense their contributions. Without it, changing from MIT to a dual-license
model requires consent from every contributor — which becomes impossible at scale.

Tools: `cla-assistant.io` integrates with GitHub PRs and is free for open source.

---

## Decision 19 — MCP (Model Context Protocol) support

**Status:** Locked (v1 syntax reservation, v2 implementation)
**Date:** 2025-06-01

**Decision:** Forge will support MCP in v2. The `forge.MCP(...)` option is
reserved in v1 syntax to prevent API breaks when implementation lands.
Using `forge.MCP(...)` in v1 is a no-op — it compiles but does nothing.

### Syntax reserved in v1

```go
app.Content(&BlogPost{},
    forge.At("/posts"),
    forge.MCP(forge.MCPRead),                      // read-only MCP resource
    // forge.MCP(forge.MCPRead, forge.MCPWrite),   // read + write via MCP
)
```

### What MCP enables (v2)

MCP (Model Context Protocol) is an open standard for AI assistants to
connect to external systems in a structured way. A Forge app with MCP
support exposes content as typed resources and operations as typed tools —
allowing AI assistants to interact with the CMS directly:

```
"Publish all draft posts older than 7 days"
"Create a new blog post with this title and body"
"What is the SEO status of my last 10 posts?"
"Which redirects are missing a destination?"
```

### Architecture

```
forge.Node + struct tags  →  MCP resource schema (auto-generated)
forge.Module operations   →  MCP tools (Create, Update, Delete, Publish)
forge.Auth / forge.Roles  →  MCP authentication (same role system)
forge.Validation          →  MCP tool input validation (same rules)
```

The MCP layer is a thin translation layer over Forge's existing
semantics — not a new system. Struct tags already define the schema.
Lifecycle rules already define what operations are allowed.
Auth already defines who can do what.

### Security constraints (v2 planning notes)

- MCP endpoints require authentication — no anonymous MCP access
- `forge.MCPRead` respects lifecycle — Draft content not exposed to Guest
- `forge.MCPWrite` requires minimum `forge.Author` role
- Rate limiting applies to MCP endpoints (same as HTTP endpoints)
- MCP transport: stdio (local tools) and SSE (remote, authenticated)

### Relation to Forge AI (monetisation)

MCP is the technical foundation for the "Forge AI" product described
in Decision 18's monetisation roadmap. Forge Cloud + MCP enables a
content assistant that understands your content model, your SEO rules,
your lifecycle states, and your role constraints — because it reads them
directly from your running Forge app.

**Rationale:**
MCP syntax reserved in v1 because:
1. Cost is zero — `forge.MCP(...)` is a no-op compile-time placeholder
2. Prevents breaking change when v2 implementation lands
3. Signals intent to early adopters and contributors
4. Forces the architectural question: what does a Forge MCP resource look like?
   Answer: exactly what `forge.Head` and `forge.Node` already define.

**Consequences:**
- `forge.MCPRead` and `forge.MCPWrite` are exported constants in v1 (unused)
- `forge.MCP(options...)` is an exported function that returns a `forge.Option` (no-op)
- v2 Milestone 10 implements the full MCP server
- `forge-mcp` may become a separate package to keep core dependency-free

---

## Decision 20 — Configuration model

**Status:** Locked
**Date:** 2025-06-01

**Decision:** Three-layer configuration. Explicit `forge.Config{}` always wins.
Five environment variables are read automatically as fallback.
No YAML/TOML files. No global singleton. No hot-reload.
Config is validated at `app.Run()` with precise, actionable error messages.

### Layer 1 — forge.Config (explicit, always wins)

```go
app := forge.New(forge.Config{
    BaseURL: "https://mysite.com",      // required
    Secret:  []byte(os.Getenv("SECRET")), // required
    Env:     forge.Production,           // default: forge.Development
})
```

| Field | Required | Default | Notes |
|-------|----------|---------|-------|
| `BaseURL` | Yes* | — | Falls back to `FORGE_BASE_URL`, then `http://localhost:{PORT}` |
| `Secret` | Yes* | — | Falls back to `FORGE_SECRET`. Warning logged if weak or missing |
| `Env` | No | `forge.Development` | Falls back to `FORGE_ENV` |
| `Logger` | No | `slog.Default()` | Custom `slog.Logger` |
| `LogLevel` | No | `slog.LevelInfo` | Falls back to `FORGE_LOG_LEVEL` |

*Required in production. In development, Forge provides safe defaults.

### Layer 2 — Environment variables (fallback, auto-read)

Forge reads these automatically. Explicit Config fields always take precedence.

```
FORGE_ENV        → Config.Env        (development | production | test)
FORGE_BASE_URL   → Config.BaseURL    (https://mysite.com)
FORGE_SECRET     → Config.Secret     (min 32 bytes recommended)
FORGE_LOG_LEVEL  → Config.LogLevel   (debug | info | warn | error)
PORT             → used by app.Run() if no addr provided
```

**FORGE_SECRET behaviour:**
- Not set in production → startup warning: *"FORGE_SECRET is not set. Sessions and tokens are insecure."*
- Set but under 32 bytes → startup warning: *"FORGE_SECRET is short. Use at least 32 random bytes."*
- Never a fatal error — developer's responsibility to act on the warning

### Layer 3 — .env files (not Forge's responsibility)

Forge does not parse `.env` files. Zero-dependencies means zero `.env` parsers.
Developers use whatever they already use: `direnv`, `docker --env-file`,
`godotenv` in their own `main.go`, shell exports, or deployment platform secrets.

This is a deliberate non-feature. The question "does .env win over environment
variable?" is a source of subtle bugs Forge should not introduce.

### Startup validation — forge.MustConfig

`forge.New()` calls `forge.MustConfig(cfg)` internally. It runs at startup,
never at request time. Failures are fatal with precise, actionable messages:

```
FATAL forge: Config.BaseURL is required in production.
             Set it via forge.Config{BaseURL: "https://yoursite.com"}
             or the FORGE_BASE_URL environment variable.

WARN  forge: FORGE_SECRET is not set.
             Sessions and tokens will use an insecure default secret.
             Set FORGE_SECRET to at least 32 random bytes in production.

WARN  forge: BasicAuth is enabled in a non-development environment.
             Consider forge.BearerHMAC or forge.CookieSession instead.
```

### app.Run() addr resolution

```go
app.Run(":8080")          // explicit — always used
app.Run("")               // empty → uses PORT env var → falls back to :8080
app.Run()                 // no arg → same as Run("")
```

### What is explicitly NOT supported

- YAML or TOML config files — requires parser, introduces ambiguity
- Global config singleton (`forge.SetGlobalConfig`) — untestable, order-dependent
- Hot-reload of config — introduces race conditions
- Merging config from multiple sources beyond the two layers above

**Rationale:**
Configuration is where "helpful" frameworks become magic frameworks.
Every layer of indirection — YAML files, global singletons, hot-reload —
adds a class of bugs that are hard to reproduce and harder to explain to
an AI assistant. Two layers (explicit + env vars) cover 99% of real use
cases. The third layer (.env files) is a solved problem Forge should not re-solve.

**Consequences:**
- `forge.Config` has exactly the fields in the table above — no more
- `forge.Development`, `forge.Production`, `forge.Test` are the three env constants
- `forge.MustConfig` is exported for testing — lets tests validate config directly
- All five env vars are documented in README under "Configuration"
- `forge.Env` type is a string constant — safe to store in config files by the user

---

## Decision 21 — forge.Context is an interface

**Status:** Locked
**Date:** 2025-06-01

**Decision:** `forge.Context` is a Go interface, not a concrete struct.
The internal implementation is `contextImpl` (unexported).
A `forge.NewTestContext(user forge.User) forge.Context` constructor
is provided for unit testing without HTTP.

```go
type Context interface {
    context.Context
    User() User
    Locale() string
    SiteName() string
    RequestID() string
    Request() *http.Request
    Response() http.ResponseWriter
}
```

**Rationale:**
A struct would require constructing a full `*http.Request` in every unit test
that exercises a hook or handler. An interface allows test code to pass a
`forge.NewTestContext(user)` with no HTTP machinery involved.

The cost of an interface (one level of indirection per method call) is
negligible at request granularity. The benefit (testable hooks without a
running server) is significant.

**Rejected alternatives:**
- *Concrete struct with test helpers:* Forces tests to construct `*http.Request`
  even when the request is irrelevant to what is being tested.
- *context.Context with value keys:* Loses type safety. `ctx.Value(userKey)`
  returns `interface{}`. Breaks "one right way" principle.

**Consequences:**
- `forge.Context` is an interface in `context.go`
- Internal implementation is `contextImpl` — unexported
- `forge.ContextFrom(r *http.Request) forge.Context` — production constructor
- `forge.NewTestContext(user forge.User) forge.Context` — test constructor
- All hooks and handlers receive `forge.Context` — never `*contextImpl`
- ARCHITECTURE.md documents this in the Stable interfaces section

---

## Decision 22 — Storage interface and database drivers

**Status:** Locked
**Date:** 2025-06-01

**Decision:** Forge defines a minimal `forge.DB` interface internally.
The default and recommended implementation uses `pgx` via the official
`pgx/v5/stdlib` compatibility shim — which provides `*sql.DB` semantics
with pgx's native performance. A `forge-pgx` sibling package provides
a native `pgxpool.Pool` adapter for maximum throughput.
SQLite and MySQL work via standard `database/sql` drivers with no changes.

### The forge.DB interface

```go
// forge.DB is satisfied by *sql.DB, *sql.Tx, and any pgx adapter.
// Users never reference this type directly — they pass a *sql.DB or
// a wrapped pgxpool.Pool to forge.Config{DB: ...}.
type DB interface {
    QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
    ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
    QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}
```

### Usage

**Recommended default — pgx via stdlib shim (one dependency, near-native speed)**

```go
import (
    "github.com/jackc/pgx/v5/stdlib"
)

db := stdlib.OpenDB(connConfig) // returns *sql.DB backed by pgx
app := forge.New(forge.Config{DB: db})
```

**Maximum performance — native pgx pool (separate forge-pgx package)**

```go
import (
    forgepgx "github.com/forge-cms/forge-pgx"
    "github.com/jackc/pgx/v5/pgxpool"
)

pool, _ := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
app := forge.New(forge.Config{DB: forgepgx.Wrap(pool)})
```

**Zero dependency — standard database/sql with any driver**

```go
import (
    "database/sql"
    _ "github.com/mattn/go-sqlite3"   // SQLite
    // _ "github.com/go-sql-driver/mysql" // MySQL
    // _ "github.com/lib/pq"             // PostgreSQL (slower than pgx)
)

db, _ := sql.Open("sqlite3", "./mysite.db")
app := forge.New(forge.Config{DB: db})
```

### Performance comparison (PostgreSQL)

| Approach | Relative throughput | Dependencies |
|----------|--------------------:|-------------|
| `database/sql` + `lib/pq` | 1× (baseline) | 1 (lib/pq) |
| `pgx/v5/stdlib` shim | ~1.8× | 1 (pgx) |
| `forge-pgx` native pool | ~2.5× | 1 (pgx) |
| `database/sql` + SQLite | n/a (different use case) | 1 (driver) |

Forge core has zero dependencies. `pgx` is a user dependency — Forge does
not import it. `forge-pgx` is a separate module (`github.com/forge-cms/forge-pgx`)
that imports both `forge` and `pgx`.

### Why not bundle pgx in core

Forge's zero-dependency guarantee applies to the core module. Bundling pgx
would force every Forge user — including those using SQLite or MySQL — to
download and compile pgx. The adapter pattern keeps core clean while making
the fast path a one-import upgrade.

### forge-pgx adapter (approximately 25 lines)

```go
// forge-pgx/pgx.go
package forgepgx

import (
    "context"
    "database/sql"

    "github.com/jackc/pgx/v5/pgxpool"
)

type poolAdapter struct{ p *pgxpool.Pool }

func Wrap(p *pgxpool.Pool) forge.DB { return &poolAdapter{p} }

func (a *poolAdapter) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
    // pgx rows → sql.Rows via pgx/v5/stdlib translation layer
    return stdlib.OpenDBFromPool(a.p).QueryContext(ctx, q, args...)
}
// ExecContext and QueryRowContext follow the same pattern
```

**Rationale:**
The `forge.DB` interface is the correct abstraction level. It matches exactly
what `database/sql` already exposes, meaning zero friction for existing Go
developers. It enables driver substitution without any changes to user code
beyond swapping the value passed to `forge.Config{DB: ...}`.
Performance is not sacrificed by default — the recommended path uses pgx.
Zero-dependency is preserved for the core module.

**Consequences:**
- `forge.Config` gets a `DB forge.DB` field
- `forge.DB` interface is exported (users may implement it for custom backends)
- `forge-pgx` created as a sibling module at `github.com/forge-cms/forge-pgx`
- README explains all three tiers clearly with code examples
- BACKLOG updated: `forge-pgx` added as a parallel deliverable to Milestone 1
- `forge.Query[T]` and `forge.QueryOne[T]` accept `forge.DB`, not `*sql.DB`

---

### Amendment S10 — Token expiry in SignToken (amends auth.go)

**Decision:** `SignToken(user User, secret string, ttl time.Duration) (string, error)` gains
a `ttl` parameter. When `ttl > 0` an `exp` (Unix seconds) field is embedded in the token
payload. `decodeToken` rejects tokens whose `exp` is non-zero and in the past with
`ErrUnauth`. `ttl = 0` means no expiry (default for tests and long-lived service tokens).

**Rationale:**
Tokens with no expiry are a common attack vector — a stolen token is valid forever until
the signing secret is rotated (which invalidates all users). An explicit TTL limits the
blast radius of a token leak to the configured window.

**Call-site syntax:**
```go
// 24-hour session token (typical web app)
tok, err := forge.SignToken(user, secret, 24*time.Hour)

// No expiry (service-to-service, long-lived CLI tokens)
tok, err := forge.SignToken(user, secret, 0)
```

**Consequences:**
- `tokenPayload` gains `Exp int64 \`json:"exp,omitempty"\`` field (backward-compatible — old tokens without `exp` decode fine with `Exp = 0` → no expiry)
- `encodeToken` gains `ttl time.Duration` parameter
- `decodeToken` validates `Exp` before returning the user
- All existing `SignToken` call sites in tests updated to pass `0`

---

### Amendment S11 — CSRF middleware (amends middleware.go / auth.go)

**Decision:** `forge.CSRF(auth AuthFunc) func(http.Handler) http.Handler` enforces
the double-submit cookie pattern for cookie-session authentication.

**Mechanism:**
1. When the `forge_csrf` cookie is absent, CSRF issues a new random token cookie (`HttpOnly: false`, `Secure: true`, `SameSite: Strict`).
2. Safe methods (GET, HEAD, OPTIONS) are passed through without validation.
3. Unsafe methods must supply the cookie value as the `X-CSRF-Token` request header, compared with `crypto/subtle.ConstantTimeCompare`.
4. If the CSRF middleware is constructed with an `AuthFunc` that is not `csrfAware` (e.g. `BearerHMAC`), it is a passthrough no-op.

**Applied as:**
```go
// Global, applied to all routes
app.Use(forge.CSRF(myAuth))

// Per-module only
forge.NewModule(&Post{}, forge.Middleware(forge.CSRF(myAuth)), forge.Repo(repo))
```

**Consequences:**
- `CSRF(auth AuthFunc)` added to `middleware.go`
- Requires `crypto/subtle` and `strings` imports in `middleware.go`
- `forge_csrf` cookie value is a UUID v7 (`NewID()`) random token

---

### Amendment S12 — RateLimit trusted proxy support (amends middleware.go)

**Decision:** `RateLimit(n int, d time.Duration, opts ...Option)` gains an optional
`forge.TrustedProxy()` option. When set, the real client IP is read from
`X-Real-IP` (nginx standard) or the leftmost entry in `X-Forwarded-For`, falling
back to `r.RemoteAddr`.

**Rationale:**
In any standard deployment behind a reverse proxy, `r.RemoteAddr` is the proxy's
IP address, meaning all clients share one rate-limit bucket.

**Call-site syntax:**
```go
// Direct exposure (development, raw VPS)
app.Use(forge.RateLimit(100, time.Minute))

// Behind nginx / Caddy / load balancer
app.Use(forge.RateLimit(100, time.Minute, forge.TrustedProxy()))
```

**Consequences:**
- `TrustedProxy() Option` + `trustedProxyOption` added to `middleware.go`
- `realClientIP(r *http.Request) string` unexported helper added
- `RateLimit` signature changed to variadic opts (backward-compatible)

---

### Amendment M5 — ListOptions.Status filter (amends storage.go)

**Decision:** `ListOptions` gains a `Status []Status` field. `MemoryRepo.FindAll`
applies the filter server-side (in the repository), not in application memory.

**Rationale:**
The previous implementation in `listHandler` fetched all items with `FindAll(ctx, ListOptions{})` then filtered in Go memory. For a 100k-post repository this allocates the full collection on every unauthenticated list request. Pushing the filter into the repository is the correct abstraction — real DB implementations can apply a `WHERE status = ?` clause.

**Consequences:**
- `Status []Status` added to `ListOptions` (zero value = return all statuses — backward-compatible)
- `statusMatch[T any](item T, statuses []Status) bool` unexported helper in `storage.go`
- `MemoryRepo.FindAll` filters via `statusMatch` before collecting items
- `listHandler` passes `Status: []Status{Published}` for guest users; `nil` for authors
- In-Go filter loop after `FindAll` removed from `listHandler`

---

## Decision 23 — SQLRepo SQL placeholder style

**Status:** Locked  
**Date:** 2026-03-07

**Decision:** `SQLRepo[T]` uses `$N`-style positional placeholders (e.g. `$1`, `$2`) for all
generated SQL. This is the PostgreSQL/pgx native format and is also accepted by
`modernc.org/sqlite` (pure-Go SQLite) and `lib/pq`.

**Rationale:**
`?`-style placeholders (MySQL, standard `database/sql`) are not supported by pgx
without wrapping. Since `pgx/v5` is the recommended driver (Decision 22) and the
primary supported database is PostgreSQL, `$N` is the correct default. SQLite
users who pass a `*sql.DB` backed by `modernc.org/sqlite` get `$N` support
automatically — no placeholder translation layer needed.

**Consequences:**
- All `SQLRepo[T]` generated queries use `$N` positional parameters
- MySQL is not supported by `SQLRepo[T]` out of the box — a `forge-mysql` sibling
  package can provide a `MySQLRepo[T]` with `?` placeholders in a future milestone
- `MemoryRepo[T]` is unaffected

---

## Decision 24 — Redirect lookup on the 404 path; chain collapse depth limit

**Status:** Locked  
**Date:** 2026-03-07

**Decision:** `RedirectStore.handler()` is mounted at `"/"` in `App.Handler()` as the
ServeMux fallback. It is only reached when no other pattern matches, meaning:
**redirect lookup adds zero overhead to successful requests**.

Chain collapse maximum depth is **10**. If collapsing a chain would exceed 10 hops,
`RedirectStore.Add` panics with a descriptive message. This prevents infinite
loops and misconfiguration from silently degrading into a redirect spiral.

**Rationale:**
A chain longer than 10 hops is almost certainly a configuration error, not a
legitimate content migration. Panicking at startup (when `app.Redirect()` is called
in `main.go`) surfaces the problem immediately rather than at request time.

**Consequences:**
- `RedirectStore.handler()` is `a.mux.Handle("/", ...)` — always registered in `Handler()`
- Empty store: `handler()` calls `http.NotFound` — identical to default ServeMux 404
- `Add()` collapses chains on every insert; max depth 10 = panic guard
- `Get()` is always O(1) for exact matches; O(prefix count) for prefix fallback

---

### Amendment A19 — SQLRepo[T] added to storage.go (Milestone 7, Step 1)

**Date:** 2026-03-07  
**Status:** Agreed  
**Amends:** Decision 22 (Storage interface and database drivers)

**Change:** `SQLRepo[T]` is added to `storage.go` alongside `MemoryRepo[T]`. Both
implement `Repository[T]`. No new file — one step = one logical unit.

**New in storage.go:**
- `type SQLRepoOption interface{ isSQLRepoOption() }` — marker interface for SQL repo options
- `func Table(name string) SQLRepoOption` — overrides auto-derived table name
- `type SQLRepo[T any] struct` with fields `db DB`, `table string`
- `func NewSQLRepo[T any](db DB, opts ...SQLRepoOption) *SQLRepo[T]`
- `(r *SQLRepo[T]) FindByID`, `FindBySlug`, `FindAll`, `Save`, `Delete` — all satisfy `Repository[T]`
- Auto-derived table name: snake_case plural of type name (`BlogPost` → `blog_posts`)
- All SQL uses `$N` placeholders (Decision 23)
- Reuses existing `dbFields` cache — no duplication

**Consequences:**
- `MemoryRepo[T]` is unchanged
- `SQLRepo[T]` requires a table whose columns match the struct's `db` tags
- README documents recommended table schema pattern
- `forge-pgx` integration tests deferred to a future milestone

---

### Amendment A20 — forge.go: RedirectStore, App.Redirect(), fallback handler (Milestone 7, Step 2)

**Date:** 2026-03-07  
**Status:** Agreed  
**Amends:** Decision 17 (Redirects and content mobility)

**Change:** Three additions to `forge.go`, pre-approved as part of the Milestone 7 plan.

**New in forge.go:**
- `redirectStore *RedirectStore` field on `App` struct
- `New()` initialises `redirectStore: NewRedirectStore()`
- `func (a *App) Redirect(from, to string, code RedirectCode)` — manual one-off redirect
- `App.Content()`: extracts `redirectsOption`; registers prefix `RedirectEntry` in store
- `App.Handler()`: `a.mux.Handle("/", a.redirectStore.handler())` — unconditional fallback

**Decision 17 amendment — IsPrefix field:**  
`RedirectEntry` gains `IsPrefix bool`. When `true`, the handler performs a
runtime path rewrite: `/old-prefix/X` → `entry.To + "/X"`. This is a single
in-memory entry — no DB expansion, zero per-request allocation beyond string concat.

**Consequences:**
- All existing `App.Redirect()` callers unaffected (exact redirects, `IsPrefix=false`)
- `Redirects(From, to)` option registers a prefix entry via `App.Content()`
- Fallback handler is always registered; empty store = standard 404 behaviour

---

### Amendment A21 — forge.go: /.well-known/redirects.json (Milestone 7, Step 3)

**Date:** 2026-03-07  
**Status:** Agreed  
**Amends:** Decision 17 (Redirects and content mobility)

**Change:** `/.well-known/redirects.json` is always mounted in `App.Handler()`,
unlike `/.well-known/cookies.json` which only mounts when declarations exist.
Redirect entries change at runtime so the manifest serialises on each request.

**New in forge.go:**
- `redirectManifestReg bool` field on `App` struct
- `App.Handler()`: mounts `GET /.well-known/redirects.json` unconditionally via
  `newRedirectManifestHandler(hostname, a.redirectStore)`
- Reuses `manifestAuthOption` from `cookiemanifest.go` — no new option type

**Consequences:**
- Empty store returns `{"count": 0, "entries": []}` — never 404
- Live serialisation: manifest always reflects the current store state
- `ManifestAuth` is optional; endpoint is public by default

---

### Amendment A22 — forge.go: App.RedirectManifestAuth() (Milestone 7, Step 4)

**Date:** 2026-03-07  
**Status:** Agreed  
**Amends:** Amendment A21 (forge.go: /.well-known/redirects.json)

**Change:** `/.well-known/redirects.json` needs an app-level auth guard method,
mirroring `App.CookiesManifestAuth()` (Amendment A18). Without this method, the
only way to set auth is via `ManifestAuth` inside `newRedirectManifestHandler`,
which is not accessible from outside the package.

**New in forge.go:**
- `redirectManifestOpts []Option` field on `App` struct
- `func (a *App) RedirectManifestAuth(auth AuthFunc)` — appends `ManifestAuth(auth)` to `redirectManifestOpts`
- `App.Handler()`: passes `a.redirectManifestOpts...` to `newRedirectManifestHandler`

**Call-site syntax:**
```go
app.RedirectManifestAuth(forge.BearerHMAC(secret, forge.Editor))
```

**Consequences:**
- Mirrors `CookiesManifestAuth` exactly — no new patterns introduced
- No existing callers broken (opts are additive; nil slice = public endpoint)
- README does not document this method yet — will be added in M7 final docs pass


---

### Amendment A23 — node.go: `db` struct tags on `Node` time fields (Milestone 8, Step 1)

**Date:** 2026-03-07
**Status:** Agreed
**Amends:** Decision 14 (Content lifecycle) — `Node` struct

**Change:** `dbFields` lowercases Go field names without inserting underscores, so
`ScheduledAt` maps to `scheduledat` (no underscore). SQL columns use `snake_case`
(`scheduled_at`), causing a mismatch that silently drops those columns in
`SQLRepo.FindAll/Save`. Explicit `db` struct tags fix this for all four time fields.

**New tags on `Node`:**
`go
PublishedAt time.Time  `db:"published_at"`
ScheduledAt *time.Time `db:"scheduled_at"`
CreatedAt   time.Time  `db:"created_at"`
UpdatedAt   time.Time  `db:"updated_at"`
`

**Consequences:**
- `SQLRepo` now maps to the correct column names automatically
- `MemoryRepo` is unaffected (does not use `db` tags)
- No existing tests rely on the broken column names

---

### Amendment A24 — context.go: `NewBackgroundContext` (Milestone 8, Step 1)

**Date:** 2026-03-07
**Status:** Agreed
**Amends:** Decision 21 (Context interface)

**Change:** The scheduler goroutine needs a `Context` for repository calls and
signal dispatch but has no HTTP request. `NewTestContext` is test-only and
uses a request-scoped context that may be cancelled. A new constructor backed
by `context.Background()` is needed.

**New in context.go:** `func NewBackgroundContext(siteName string) Context`
- Creates a synthetic `GET /` `*http.Request` (same pattern as `NewTestContext`)
- Wraps with `context.Background()` — never times out
- `user: GuestUser`, `locale: "en"`, `requestID: NewID()`
- `siteName` set from parameter

**Consequences:**
- Scheduler can make repository calls and fire signals without an HTTP request
- `NewTestContext` is unchanged — test code is unaffected

---

### Amendment A25 — module.go: `processScheduled` + helpers (Milestone 8, Step 1)

**Date:** 2026-03-07
**Status:** Agreed
**Amends:** Decision 14 (Content lifecycle)

**Change:** `Module[T]` must implement the `schedulableModule` interface so the
`Scheduler` can drive the Scheduled to Published transition.

**New in module.go:**
- `setNodeStatus(item any, s Status)` — sets Status field via reflection
- `setNodeTime(item any, field string, t time.Time)` — sets time.Time field
- `setNodeTimePtr(item any, field string, t *time.Time)` — sets *time.Time field
- `func (m *Module[T]) processScheduled(ctx Context, now time.Time) (int, *time.Time, error)`

**Consequences:**
- `Module[T]` now satisfies `schedulableModule` at compile time
- No change to `Module[T]`'s public API
- No new imports required in `module.go`

---

### Amendment A26 — forge.go: scheduler wiring (Milestone 8, Step 1)

**Date:** 2026-03-07
**Status:** Agreed
**Amends:** Decision 2 (App bootstrap)

**Change:** `App` must collect modules and start/stop the `Scheduler` in `Run()`.

**New in forge.go:**
- `schedulerModules []schedulableModule` field on `App` struct
- `App.Content()`: appends module to `schedulerModules` when it satisfies `schedulableModule`
- `App.Run()`: starts scheduler before `ListenAndServe`; `defer` stops it after `srv.Shutdown`

**Consequences:**
- Scheduler goroutine starts before HTTP server and stops cleanly after graceful shutdown
- `App` with no content modules: no goroutine spawned
- `App.Run()` return paths unchanged — defer handles all cleanup paths

---

### Amendment A27 — middleware.go: `forge.Authenticate(AuthFunc)` (Milestone 9, Step 6)

**Date:** 2026-03-08
**Status:** Agreed
**Amends:** Decision 15 (Role system), middleware.go, ARCHITECTURE.md

**The gap:** Decision 15 defines `forge.Auth(forge.Read(r), forge.Write(r))` as module options and `BearerHMAC`/`CookieSession`/`BasicAuth` for issuing `AuthFunc` values. However, `AuthFunc` does not implement `Option` and `userContextKey` is unexported — application code outside the `forge` package had no way to inject a `User` into the request context. Module role checks at `ctx.User().HasRole(m.writeRole)` always evaluated against `GuestUser` in production, making `forge.Auth` useless without internal access.

**Change:** Add one exported function to `middleware.go`:

```go
func Authenticate(auth AuthFunc) func(http.Handler) http.Handler
```

The implementation calls `auth.authenticate(r)` and, on success, attaches the returned `User` to the request context via `context.WithValue(r.Context(), userContextKey, user)`. Unauthenticated requests pass through, so `ContextFrom` falls back to `GuestUser` — correct for public read endpoints.

**Call-site:**

```go
app.Use(forge.Authenticate(forge.BearerHMAC(secret)))

m := forge.NewModule[*Resource](&Resource{},
    forge.Auth(
        forge.Read(forge.Guest),    // GET list + show — no token required
        forge.Write(forge.Editor),  // POST/PUT/DELETE — Editor role required
    ),
)
```

**Consequences:**
- Explicit two-step wiring: `Authenticate` for the request user layer, `Auth(Read/Write)` for the module threshold policy. Separation of concerns is intentional.
- Identical signature pattern to `CSRF(auth AuthFunc)` — no new API shape.
- Purely additive: no breaking change to any existing symbol.
- File boundary: one function added to `middleware.go` only.

**Rejected alternatives:**
- `forge.Auth(forge.BearerHMAC(secret), forge.Read(Guest), ...)` — mixes authentication (request layer) with authorisation (module threshold layer).
- Exporting `userContextKey` — breaks encapsulation of `Context`'s internal request state.

---

### Amendment A28 — Auto-detect `Headable` in `Module[T]` (amends Decision 3 + Decision 14)

**Status:** Agreed  
**Amends:** Decision 3 (Head/SEO ownership), Decision 14 (Content lifecycle) — `module.go`, `head.go`

**The gap:** `Headable` was documented as "implemented by content types that provide their own SEO metadata" but `Module[T]` never called it. The only way to wire SEO metadata was via the explicit `HeadFunc` option — a closure the developer must write by hand. A content type that correctly implemented `Head() forge.Head` still received a zero `Head` in sitemaps, feeds, AI endpoints, and HTML rendering unless `HeadFunc` was also supplied. This made the interface decorative and broke the zero-config production-ready promise.

**Change:** Add `resolveHead(ctx Context, item T) Head` to `Module[T]` in `module.go`:

```go
// resolveHead returns the Head for item using the highest-priority source available:
//  1. HeadFunc option — explicit module-level override (context-aware)
//  2. Headable interface on T — type-level default (no context)
//  3. Zero Head
func (m *Module[T]) resolveHead(ctx Context, item T) Head {
    if m.headFunc != nil {
        if fn, ok := m.headFunc.(func(Context, T) Head); ok {
            return fn(ctx, item)
        }
    }
    if h, ok := any(item).(Headable); ok {
        return h.Head()
    }
    return Head{}
}
```

Replace the four duplicated `headFunc` resolution blocks in `regenerateFeed`, `regenerateAI`, `aiDocHandler`, and `renderShowHTML` with `m.resolveHead(ctx, item)`.

Update the `Headable` godoc in `head.go` to document that `Module[T]` calls it automatically.

**Call-site before:**
```go
forge.NewModule[*Article](&Article{},
    forge.At("/articles"),
    forge.HeadFunc(func(_ forge.Context, a *Article) forge.Head {
        return forge.Head{Title: a.Title, Description: a.Excerpt}
    }),
    forge.AIIndex(forge.LLMsTxt, forge.AIDoc),
)
```

**Call-site after:**
```go
// Article implements forge.Headable
func (a *Article) Head() forge.Head {
    return forge.Head{Title: a.Title, Description: a.Excerpt}
}

forge.NewModule[*Article](&Article{},
    forge.At("/articles"),
    forge.AIIndex(forge.LLMsTxt, forge.AIDoc),
)
```

`HeadFunc` remains supported and takes priority over `Headable` when both are present — no breaking change.

**Consequences:**
- `Headable` delivers its documented promise without an explicit `HeadFunc` option
- `HeadFunc` is still the correct choice for context-aware or database-enriched metadata
- The `any(item).(Headable)` assertion fires only in regeneration and show handlers — not on the list hot path
- README hero examples and tweet-length demos are now accurate
- Existing code with `HeadFunc` is unaffected — priority order ensures no behaviour change

**Rejected alternatives:**
- `forge.DefaultHead()` option — requires an extra call-site token; still leaves `Headable` decorative
- Reflection on struct field names — fragile, no compile-time contract, inconsistent with codebase patterns
- Exporting a head-resolution function — adds surface area with no benefit over an interface

---

## Amendment A29 — `errors.go` error handling gaps

**Status:** Agreed  
**Date:** 2026-03-11  
**Amends:** Decision 16

**Problem:** A post-v1.0.0 audit of the error handling pipeline found four gaps in `errors.go`:

1. `respond()` uses a direct type assertion `err.(*ValidationError)` instead of `errors.As`. A wrapped `*ValidationError` passed to `respond` from any future non-`WriteError` call path would silently produce a response without field details.
2. `errorTemplateLookup` is an unprotected package-level `var func(int) *template.Template`. The Go race detector flags concurrent reads (in-flight requests) against the single write (in `App.Handler()`). It should be protected by `sync.Once` since `App.Handler()` is a one-shot call in the expected lifecycle.
3. Four sentinels actively produced by the framework have no named constant: 400 (`ErrBadRequest`), 406 (`ErrNotAcceptable`), 413 (`ErrRequestTooLarge`), 429 (`ErrTooManyRequests`). Handlers producing those status codes currently `newSentinel(...)` inline, violating the rule that `newSentinel` must only be used in `errors.go`.
4. `errors_test.go` is missing: wrapped-sentinel unwrapping, 5xx `forge.Error` suppression, wrapped `*ValidationError` field propagation, and the response-header `X-Request-ID` priority path.

**Decision:** Fix all four in `errors.go` / `errors_test.go` as Amendment A29:
- Replace direct type assertion in `respond` with `errors.As`
- Protect `errorTemplateLookup` with `sync.Once` (write path in `App.Handler`, read path in `respond`)
- Add `ErrBadRequest`, `ErrNotAcceptable`, `ErrRequestTooLarge`, `ErrTooManyRequests` sentinels
- Extend `errors_test.go` with the four missing cases

**Consequences:**
- No public API change — new sentinels are additive
- `example_test.go` unaffected
- `respond` is now safe to call from paths other than `WriteError` without risking silent field omission
- `errorTemplateLookup` write is guarded; second call to `App.Handler()` is silently a no-op (consistent with existing behaviour — `App.Handler()` is documented as one-shot)

---

## Amendment A30 — `module.go` error handling gaps

**Status:** Agreed  
**Date:** 2026-03-11  
**Amends:** Decision 16

**Problem:** Three `http.Error` calls in `module.go` bypass `WriteError`:

1. `writeContent(w, ct, v)` calls `http.Error(w, "HTML templates not registered", 406)` and `http.Error(w, "text/markdown not supported", 406)`. Because `writeContent` takes no `*http.Request`, it cannot call `WriteError`. This forces content-negotiation failures to emit plain text with no `X-Request-ID`.
2. `createHandler` and `updateHandler` call `http.Error(w, "invalid JSON body", http.StatusBadRequest)` on `json.Decode` failure. This has a correctness bug: when `MaxBodySize` middleware is in use and the client exceeds the limit, `json.Decode` returns `*http.MaxBytesError` (Go 1.19+). The current code maps this silently to 400 instead of 413.

**Decision:**
- Add `r *http.Request` parameter to `writeContent` and `writeContentCached`; update all call sites within `module.go`
- Replace both `http.Error(w, "HTML templates not registered", ...)` calls with `WriteError(w, r, ErrNotAcceptable)`
- Replace both `http.Error(w, "text/markdown not supported", ...)` calls with `WriteError(w, r, ErrNotAcceptable)`
- Replace both JSON decode error paths with `errors.As(*http.MaxBytesError)` → `ErrRequestTooLarge` (413), else `ErrBadRequest` (400), both via `WriteError`

**Consequences:**
- `writeContent` and `writeContentCached` gain a `r *http.Request` parameter — internal functions only, no public API change
- Content-negotiation 406 responses now carry `X-Request-ID` and JSON/HTML format
- Clients that exceed `MaxBodySize` receive 413 instead of 400
- `example_test.go` unaffected

---

## Amendment A31 — `templates.go` error handling gaps

**Status:** Agreed  
**Date:** 2026-03-11  
**Amends:** Decision 16

**Problem:** `renderListHTML` and `renderShowHTML` in `templates.go` call `http.Error(w, "HTML templates not registered", http.StatusNotAcceptable)` when `tplList` / `tplShow` is nil. Both functions have `r *http.Request` available, so `WriteError` can be called directly.

**Decision:** Replace both `http.Error` calls with `WriteError(w, r, ErrNotAcceptable)`.

**Consequences:**
- 406 responses from the HTML render path now carry `X-Request-ID` and correct format
- No public API change
- `example_test.go` unaffected

---

## Amendment A32 — `middleware.go` error handling gaps

**Status:** Agreed  
**Date:** 2026-03-11  
**Amends:** Decision 16

**Problem:** Two error paths in `middleware.go` bypass `WriteError`:

1. `RateLimit` calls `http.Error(w, "Too Many Requests", http.StatusTooManyRequests)`. JSON API clients receive `text/plain` with no `X-Request-ID`.
2. `Recoverer` allocates a 4096-byte buffer for `runtime.Stack`. Deep call stacks (e.g. recursive template rendering, deeply chained middleware) truncate silently. The conventional Go allocation for stack captures is 32 KB (`32 * 1024`).

**Decision:**
- Replace `http.Error` in `RateLimit` with `WriteError(w, r, ErrTooManyRequests)`
- Increase `Recoverer` stack buffer from 4096 to `32 * 1024` bytes

**Consequences:**
- 429 responses now carry `X-Request-ID` and are JSON or HTML per `Accept`
- Panic stack traces are no longer silently truncated for deep stacks
- No public API change
- `example_test.go` unaffected


---

## Amendment A33 -- `module.go` route mounting order bug (sitemap + feed)

**Status:** Agreed
**Date:** 2026-03-11
**Amends:** Decision 16 (module registration), Decision 9 (sitemap)

**Problem:** `Module[T].Register(mux)` is called by `App.Content()` before `setSitemap` and `setFeedStore` are called. Both methods inject the shared `*SitemapStore` and `*FeedStore` into the module. As a result, the route-mounting guards inside `Register`:

```go
if m.sitemapCfg != nil && m.sitemapStore != nil { /* never true -- store not yet set */ }
if m.feedCfg   != nil && m.feedStore   != nil { /* never true -- store not yet set */ }
```

always evaluate to `false`. Neither `GET /{prefix}/sitemap.xml` nor `GET /{prefix}/feed.xml` is ever mounted, regardless of module configuration.

**Decision:** Change both guards to check only the config (always known at `NewModule` time), and read the store lazily inside the handler via a closure over `m`. A nil-store guard in each handler is a safety net for the theoretical startup race window; in practice the stores are always set before the server begins accepting connections.

**Consequences:**
- `GET /{prefix}/sitemap.xml` and `GET /{prefix}/feed.xml` are now correctly mounted for any module that opts in via `SitemapConfig{}` or `Feed(FeedConfig{...})`
- `/posts/feed.xml` in the example blog now returns RSS instead of 404
- No public API change; all module options unchanged
- `example_test.go` unaffected
- AI routes (`/{prefix}/{slug}/aidoc`) are not affected -- `aiFeatures` is set at `NewModule` time, not via a post-Register injection


---

## Amendment A34 -- `module.go` + `forge.go` startup rebuild for derived content

**Status:** Agreed
**Date:** 2026-03-11
**Amends:** Decision 9 (sitemap), Decision 16 (module lifecycle)

**Problem:** Sitemap fragments, RSS feeds, and AI index entries are generated
by the module debouncer, which fires only when a content item transitions through
the create/update/publish signal pipeline. Items inserted directly into a
Repository (seed data, pre-loaded fixtures, SQLRepo data from a previous run)
are never signalled, so sitemaps and feeds are empty until the first real publish
event after server start. This makes the example apps and any real app with
existing data appear broken on first run.

**Decision:** Add startup regeneration:

1. `module.go`: define internal `rebuilder` interface with `rebuildAll(ctx Context)`
2. `module.go`: implement `rebuildAll` on `Module[T]` -- calls
   `regenerateSitemap`, `regenerateAI`, `regenerateFeed` in sequence
3. `forge.go`: collect `rebuilder` modules in `App.Content` alongside existing
   interface checks; add `rebuilderModules []rebuilder` + `rebuildDone bool` to
   `App`
4. `forge.go`: in `App.Handler()`, after all stores are set, launch a single
   goroutine that calls `rebuildAll` on each module (guarded by `rebuildDone`)

The goroutine is used so that `App.Handler()` is not blocked by repository
queries at startup. The `rebuildDone` guard ensures the goroutine is launched
exactly once even if `Handler()` is called multiple times.

**Consequences:**
- Sitemap fragments, feeds, and AI index are populated from existing repository
  data before the server accepts its first request (in practice -- the goroutine
  races with the first request, but sitemaps are populated within milliseconds)
- No public API change; `rebuildAll` / `rebuilder` are unexported
- Any app that seeds data before `app.Run()` now gets correct sitemap + feed
  output on first page load without needing a manual publish event
- `example_test.go` unaffected
- `go test ./...` green with no changes to test files
---

## Amendment A35 -- `module.go` content negotiation capability gating

**Status:** Agreed
**Date:** 2026-03-11
**Amends:** Decision 9 (content negotiation, Milestone 4)

**Problem:** `contentNegotiator.negotiate()` returned `"text/html"` whenever the
request `Accept` header contained `text/html`, regardless of whether the module
had an HTML template registered (`n.html`). `writeContent` then unconditionally
returned 406 for the `"text/html"` case. The result: a browser visiting any
module without `forge.Templates(...)` -- including every JSON-only API module --
received `406 Not Acceptable` instead of JSON.

The same flaw existed for `"text/markdown"`: `negotiate()` returned it even when
`n.md == false` (content type does not implement `Markdownable`), causing a 406
rather than a JSON fallback.

**Decision:** Gate each content-type branch in `negotiate()` on the corresponding
capability flag:

```go
if n.html && strings.Contains(a, "text/html") {
    return "text/html"
}
if n.md && strings.Contains(a, "text/markdown") {
    return "text/markdown"
}
```

`text/plain` is not gated -- it always works (falls back to stripped markdown or JSON).

**Consequences:**
- A browser visiting a JSON-only module now receives JSON (200) instead of 406
- Modules with `forge.Templates(...)` are unaffected -- `n.html == true`
- Modules with `Markdownable` are unaffected -- `n.md == true`
- The `text/html` branch in `writeContent` is retained as a safety net (only
  reachable if `n.html == true` but template rendering somehow fails)
- No public API change; `contentNegotiator` and `negotiate()` are unexported
- Two new integration tests: `TestIntegration_negotiateHTMLFallback` and
  `TestIntegration_negotiateMarkdownFallback`
- `example_test.go` unaffected
- `go test ./...` green

## Amendment A36 — `module.go` startup capability mismatch detection

**Status:** Agreed
**Date:** 2026-03-11
**Amends:** Decision 9 (`module.go` option parsing and startup validation)

**Problem:** Two option combinations silently produced empty outputs at runtime
with no error or warning:

1. `SitemapConfig{}` given but `T` does not implement `SitemapNode` (missing
   `Head() forge.Head`). `regenerateSitemap` performs a type assertion on each
   item and exits the loop on the first failure — `/{prefix}/sitemap.xml` is
   always served empty. The `example/api` bug required live testing to discover.

2. `AIIndex(LLMsTxtFull)` given but `T` does not implement `Markdownable`
   (missing `Markdown() string`). `regenerateAI` skips the full-corpus path —
   `/llms-full.txt` contains no body text. Silent, same root cause.

Both are unambiguous programming errors: the developer requested a feature that
requires an interface their type does not satisfy. The correct remedy is always
to add the missing method — there is no valid use-case for a silently empty
sitemap or AI corpus.

**Decision:** Add two `panic` checks in `NewModule`, immediately after the
existing `!repoFound` panic, consistent with the `getNodeFields` / `repoFound`
pattern (programmer errors → panic at startup, never at request time):

```go
// A36: SitemapConfig requires T to implement SitemapNode.
if m.sitemapCfg != nil {
    if _, ok := any(proto).(SitemapNode); !ok {
        panic(fmt.Sprintf(
            "forge: %s has SitemapConfig but does not implement SitemapNode "+
                "(add a Head() forge.Head method); sitemap would be silently empty",
            typeName,
        ))
    }
}
// A36: AIIndex(LLMsTxtFull) requires T to implement Markdownable.
if hasAIFeature(m.aiFeatures, LLMsTxtFull) && !m.neg.md {
    panic(fmt.Sprintf(
        "forge: %s has AIIndex(LLMsTxtFull) but does not implement Markdownable "+
            "(add a Markdown() string method); /llms-full.txt would be silently empty",
        typeName,
    ))
}
```

The `m.neg.md` flag is already set before option parsing by the existing
`Markdownable` detection, so no extra type assertion is needed for the second check.

**Consequences:**

1. **Call-site syntax** — unchanged; no public API change. The new panics are
   unreachable for correctly-written code.
2. **README / examples** — all documented examples already satisfy the required
   interfaces; no README change needed.
3. **AI generation accuracy** — improved; the panic messages state exactly which
   method to add, making correct code trivially recoverable.
4. **Consistency** — matches `getNodeFields` and `!repoFound` patterns exactly.
5. **Existing tests** — three test types used `SitemapConfig{}` without `Head()`:
   `*testPost` (module_test.go) and `*testAIPost` (ai_test.go). Fixed by adding
   `func (p *testPost) Head() Head` and `func (p *testAIPost) Head() Head`.
   A new `testNoHeadPost` type (no `Head()`) serves as the intentional-failure
   fixture for the A36 panic test.
6. **New tests** — `TestNewModule_sitemapConfig_panicsWithoutSitemapNode` and
   `TestNewModule_aiIndexLLMsFull_panicsWithoutMarkdownable` added to `module_test.go`.
7. **No breaking change** — correctly-written code is unaffected.

## Amendment A37 — `WriteError` pipeline: replace `http.Error`/`http.NotFound` bypasses

**Status:** Agreed  
**Date:** 2026-03-12  
**Amends:** Decision 16 (`errors.go` error handling model) and `ERROR_HANDLING.md` single-pipeline rule

**Problem:** Five call sites in framework code bypassed `WriteError`, violating the single-pipeline rule from `ERROR_HANDLING.md`:

1. `redirects.go` `handler()`: `http.NotFound(w, r)` (no-match path) and `http.Error(w, "Gone", 410)` (Gone path)
2. `redirectmanifest.go` `newRedirectManifestHandler`: `http.Error(w, "Unauthorized", 401)` (auth gate)
3. `cookiemanifest.go` `newCookieManifestHandler`: `http.Error(w, "Unauthorized", 401)` (auth gate)
4. `sitemap.go` `SitemapStore.Handler()`: `http.NotFound(w, r)` (unknown path)
5. `sitemap.go` `SitemapStore.IndexHandler()`: `http.Error(w, "sitemap index error", 500)` (XML encode failure)

All five bypass `WriteError`, so these responses:
- Carry no `X-Request-ID` header (breaks distributed tracing)
- Ignore the `Accept` header (always plain text, even for JSON clients)
- Produce no structured log entry

**Decision:** Replace all five call sites with `WriteError(w, r, ...)` using the matching sentinel:
- `http.NotFound` → `WriteError(w, r, ErrNotFound)`
- `http.Error(..., 401)` → `WriteError(w, r, ErrUnauth)`
- `http.Error(..., 410)` → `WriteError(w, r, ErrGone)`
- `http.Error(..., 500)` → `WriteError(w, r, ErrInternal)` (underlying XML encode error is extremely rare—ResponseWriter wrapping an in-memory buffer—and is logged via `slog.Error` inside `WriteError`)

All are in handler closures that already receive `*http.Request`, so no signature change is needed.

**Consequences:**

1. **Call-site syntax** — unchanged; no public API change.
2. **Response shape** — 404, 410, 401, and 500 responses from these handlers now include `X-Request-ID` and respect `Accept: application/json`.
3. **Logging** — 500 responses from `IndexHandler` are now logged via `slog.Error` in `WriteError`.
4. **Tests** — existing test assertions on status code are unaffected. New assertions added for `X-Request-ID` presence.
5. **No breaking change** — clients that parse only the status code see no difference.

## Amendment A38 — `auth.go`: `SignToken` error return implements `forge.Error`

**Status:** Agreed  
**Date:** 2026-03-12  
**Amends:** Decision 16 (`errors.go` error handling model)

**Problem:** `encodeToken` (called by the public `SignToken`) returned a raw `fmt.Errorf` value on `json.Marshal` failure. This is the only non-`forge.Error` error return from a public API function, violating Decision 16.

**Decision:** Replace `fmt.Errorf("forge: encodeToken marshal: %w", err)` with `ErrInternal`. The `json.Marshal` call serialises `tokenPayload{string, string, []string, int64}` — none of those types can fail JSON serialisation, so this path is unreachable in practice. Returning `ErrInternal` is the correct defensive choice: it satisfies `forge.Error`, is already imported, and requires no new types.

A compile-time assertion `var _ Error = ErrInternal` is added to `auth_test.go` to document the contract explicitly.

**Consequences:**

1. **Call-site syntax** — `SignToken` signature is unchanged; only the error type improves.
2. **Error inspectability** — callers using `errors.As(err, new(forge.Error))` now correctly identify the error.
3. **No breaking change** — the error path was already unreachable; no caller in production can observe the difference.

---

## Amendment A39 — `Module[T]`: cache sweep goroutine lifecycle and `Stop()` method

**Status:** Agreed  
**Date:** 2026-03-12  
**Amends:** Decision 1 (zero-dependency, production-ready defaults)

**Problem:** When `Cache(ttl)` is used with a module, `NewModule` spawns a `time.Ticker` goroutine that calls `CacheStore.Sweep()` every 60 seconds. The goroutine had no exit path — it ran until the process terminated, leaking across test runs and preventing clean graceful shutdown.

**Decision:**

1. Add `stopCh chan struct{}` to `Module[T]`. Initialised unconditionally in `NewModule` via `make(chan struct{})` so `Stop()` is always safe to call regardless of options.
2. The cache sweep goroutine uses `select { case <-ticker.C: ... case <-m.stopCh: return }` instead of `for range ticker.C`.
3. Add exported `Stop()` method on `Module[T]`. Idempotent — closing an already-closed channel is guarded by a non-blocking select. Also calls `debounce.Stop()` to cancel any pending `time.AfterFunc` timer.
4. Add `Stop()` method to `debouncer` in `signals.go` (cancels pending `time.Timer` under the mutex).
5. Add `stoppable` interface (unexported, matching the `rebuilder`/`schedulableModule` pattern) and `stoppableModules []stoppable` field on `App`.
6. `App.Content` appends every registered module that implements `stoppable` to `stoppableModules`.
7. `App.Run` calls `sp.Stop()` for every stoppable module after `srv.Shutdown` returns.

**Consequences:**

1. **No API change** — `Module[T]` gains one exported method (`Stop()`). No existing call sites break.
2. **Test isolation** — modules created in unit tests no longer leak goroutines between test cases.
3. **Graceful shutdown** — the cache sweep ticker and any pending debounce timer are cancelled within the 5-second shutdown window.
4. **Debouncer** — `debouncer.Stop()` is safe to call even before `Trigger()` has been called (`d.timer == nil` guard).

---

## Amendment A40 — Rename `FeedDisabled()` → `DisableFeed()` and `forgeLLMSEntries` → `forgeLLMsEntries`

**Status:** Agreed  
**Date:** 2026-03-12  
**Amends:** Decision 1 (API readability) and Amendment A16 (`feed.go` initial naming)

**Problem:** Two symbol names violated the `forge.Verb(Noun)` / `forge.Noun` naming convention:

1. `FeedDisabled()` reads as an adjective predicate rather than a command. Option constructors follow the imperative verb pattern (`Feed`, `Cache`, `Auth`, `On`, `Redirect`). The consistent name is `DisableFeed()` — verb first, noun second.
2. `forgeLLMSEntries` used the all-caps acronym `LLMS`. Go convention (and the rest of the codebase: `LLMsStore`, `LLMsEntry`, `LLMsTxt`) uses mixed-case `LLMs`. The correct unexported name is `forgeLLMsEntries`.

**Decision:**

1. Rename exported `FeedDisabled() Option` → `DisableFeed() Option` in `feed.go`. Godoc updated. `feedDisabledOption` struct and `feedDisabledOption` case in `module.go` are internal and unchanged.
2. Rename unexported `forgeLLMSEntries` → `forgeLLMsEntries` in `templatehelpers.go`. The map key `"forge_llms_entries"` is unchanged — no template call sites break.
3. All call sites updated: `feed_test.go`, `templatehelpers_test.go`, `module.go` comment.

**Consequences:**

1. **Breaking change for `FeedDisabled()`** — any application code calling `FeedDisabled()` must be updated to `DisableFeed()`. Since Forge is pre-v1.1.0 and this is a cosmetic rename of a rarely-used option, the breakage is acceptable and preferable to locking in the wrong name.
2. **No template break** — `{{forge_llms_entries .}}` in user templates is unaffected; the Go function name is unexported.
3. **AI generation accuracy** — `DisableFeed()` is immediately parseable as an imperative option; `forgeLLMsEntries` matches the casing of all other `LLMs*` symbols.

---

## Amendment A41 — `Module[T]`: debounce callback must use `NewBackgroundContext`, not stashed request context

**Status:** Agreed
**Date:** 2026-03-12
**Amends:** Amendment A24 (`NewBackgroundContext`) — missed application; Decision 9 (event-driven regeneration)

**Problem:** `triggerSitemap(ctx Context)` stashed the triggering HTTP request's `forge.Context` in `m.debounceCtx` (protected by `debounceMu`) and the debounce callback read it back 2 seconds later. `forge.Context` embeds `context.Context`, and the request's underlying context is cancelled as soon as the HTTP handler returns — typically well within the 2-second debounce window. When `SQLRepo.FindAll` or `SQLRepo.Save` received this cancelled context, they returned a context error. All three regeneration paths (`regenerateSitemap`, `regenerateAI`, `regenerateFeed`) and `dispatchAfter(SitemapRegenerate)` silently swallowed the error via `if err != nil { return }`, so every write event in production caused a silent no-op rebuild. `MemoryRepo` ignores its context argument, which is why tests never caught this.

Amendment A24 added `NewBackgroundContext` precisely to solve this class of problem — it was not applied here when the debouncer was first implemented.

**Decision:**

1. Remove `debounceMu sync.Mutex` and `debounceCtx Context` fields from `Module[T]`.
2. The debounce callback builds its own `Context` at fire time via `NewBackgroundContext(m.siteName)` — `m.siteName` is a plain string field, safe to read from a goroutine after module construction.
3. Rename `triggerSitemap(ctx Context)` to `triggerRebuild()` — no ctx parameter needed; the callback is fully self-contained.
4. Update all four call sites (create, update, delete handlers and `processScheduled`).

**Consequences:**

1. **Correctness** — `SQLRepo` users: `regenerateSitemap/AI/Feed` now execute with a live `context.Background()`-backed context; database queries succeed.
2. **Signal handlers** — `dispatchAfter(SitemapRegenerate)` receives a non-cancelled context; any repo calls inside signal handlers also succeed.
3. **Simpler struct** — `debounceMu` and `debounceCtx` removed; no lock needed for the debounce path.
4. **`siteName` at fire time** — may be empty string if module is used without `App.Content`; `NewBackgroundContext("")` is valid and safe.
5. **No exported API change** — `triggerRebuild` is unexported; `Module[T]`'s public surface is unchanged.

---

## Amendment A42 — `forge.go`: `Config.Version` field and `App.Health()` endpoint

**Status:** Agreed
**Date:** 2026-03-12
**Amends:** Decision 2 (App bootstrap)

**Problem:** Forge apps running in Kubernetes, Docker, or behind a load balancer need a dedicated liveness/readiness endpoint. Developers currently use `app.Handle("GET /healthz", ...)` by hand — a repetitive, error-prone pattern with no standard response shape. The version string has no first-class home in the framework; developers hard-code it in separate handler closures.

**Decision:**

1. Add `Version string` field to `Config`, immediately after `Secret []byte`.
   Godoc: "Version is the application version string. When non-empty, it is
   included in the GET /_health response."
2. Add `func (a *App) Health()` to `forge.go`. Mounts `GET /_health` on the App mux.
   - `Content-Type: application/json`, status 200 always.
   - Body: `{"status":"ok"}` when `Config.Version` is empty.
   - Body: `{"status":"ok","version":"X.Y.Z"}` when `Config.Version` is set
     (version string JSON-quoted via `fmt.Fprintf`).
   - `Health()` is explicit opt-in — not mounted automatically by `New` or `Run`.
     Callers who prefer a custom path continue to use `app.Handle`.
3. Three tests added to `forge_test.go`:
   - `TestApp_health_ok` — no version, body is `{"status":"ok"}`
   - `TestApp_health_version` — version `"1.2.3"`, body is `{"status":"ok","version":"1.2.3"}`
   - `TestApp_health_notMounted` — `Health()` not called, `/_health` returns 404

**Call-site syntax:**
```go
app := forge.New(forge.MustConfig(forge.Config{
    BaseURL: os.Getenv("BASE_URL"),
    Secret:  []byte(os.Getenv("SECRET")),
    Version: "1.2.3",
}))
app.Health()
```

**Consequences:**

1. **Call-site syntax** — `Config.Version` is a zero-value string; all existing `Config` literals are backward-compatible.
2. **No forced mount** — the endpoint is not registered unless `Health()` is called. Apps that already use `app.Handle("GET /_health", ...)` are unaffected.
3. **Response shape** — fixed JSON schema; serialisation uses `fmt.Fprintf` with `%q` for the version field to ensure correct JSON string escaping.
4. **No middleware** — `/_health` bypasses rate limiting and authentication by design. Liveness probes must not be auth-gated.
5. **Consistency** — matches `Cookies()`, `Redirect()`, `RedirectManifestAuth()` as an explicit opt-in method on `App`.
6. **No breaking change** — existing code is unaffected.

---

## Amendment A43 — NewSQLRepo pointer type documentation (amends Decision 22)

**Date:** 2026-03-14  
**Status:** Agreed

**Change:** Added explicit pointer-type guidance to `NewSQLRepo` godoc and
README wiring example. T must be a pointer type and must match the proto
passed to `NewModule`. Value types compile but produce a type mismatch at
`forge.Repo(repo)`.

**Consequences:** Documentation only — no API or behaviour change.

---

## Amendment A44 — `dbFields`: flatten embedded (anonymous) struct fields via `[]int` index path

**Date:** 2026-03-15  
**Status:** Agreed

**Change:** `dbField.index` changed from `int` to `[]int` (a `reflect`
field index path). `dbFields` now delegates to a new recursive helper
`collectDBFields` that traverses anonymous (embedded) struct fields and
collects their promoted fields with the full index path. All callers
updated to use `reflect.Value.FieldByIndex` instead of `reflect.Value.Field`:

- `Query[T]`: `colIdx` map changed to `map[string][]int`; scan target
  resolved via `elem.FieldByIndex(idx)`.
- `SQLRepo.columnForField`: resolved via `r.elemType.FieldByIndex(f.index).Name`.
- `SQLRepo.Save`: argument value resolved via `rv.FieldByIndex(f.index).Interface()`.

**Consequences:** Content types that embed `forge.Node` (or any anonymous
struct) now have their promoted fields correctly mapped to SQL columns.
Before this fix, `SQLRepo.Save` passed the embedded struct value itself as
a SQL argument, producing `"unsupported type forge.Node, a struct"` at
runtime. No API surface change; the fix is internal to the reflection layer.

---

## Amendment A45 — `Config.Auth` field + default `BearerHMAC` wired in `New()`

**Date:** 2026-03-15  
**Status:** Agreed

**Change:** Added `Auth AuthFunc` field to `Config`. `New()` now prepends
`Authenticate(auth)` as the first item in `app.middleware`. When `Auth` is
nil, `auth` defaults to `BearerHMAC(string(cfg.Secret))`. Developers can
override by setting `Config.Auth` to `CookieSession`, `AnyAuth`, or any
custom `AuthFunc` before calling `New()`. `Config.Secret` godoc updated to
mention the default auth behaviour.

**Consequences:**
- Silent misconfiguration eliminated: a developer who sets `Config.Secret`
  and calls `SignToken` but never calls `app.Use(forge.Authenticate(...))` no
  longer gets unexplained 403 responses on every write request.
- Existing apps that already call `app.Use(forge.Authenticate(...))` will now
  have `Authenticate` in the stack twice. They should remove their explicit
  call or set `Config.Auth` to their preferred `AuthFunc`. The double-wrapping
  is safe (inner wins for role population) but redundant.
- Auth can be disabled by setting `Config.Auth` to a no-op `AuthFunc` that
  always returns `GuestUser` — no API change is required for that pattern.

---

## Amendment A46 — `markdown.go`: minimal Markdown→HTML renderer added to `TemplateFuncMap`

**Date:** 2026-03-15  
**Status:** Agreed

**Change:** New file `markdown.go` implements `renderMarkdown(s string)
template.HTML` — a zero-dependency, XSS-safe Markdown→HTML converter
exposed in `TemplateFuncMap` as the `"markdown"` key. Supported elements:
h1–h6, fenced code blocks with `class="language-〈lang〉"`, unordered lists,
GFM tables (header + separator + body rows), `**bold**`, `` `inline code` ``,
blank-line-separated `<p>` paragraphs, and standalone `---` as `<hr>`. All
content is HTML-entity-escaped before being wrapped in tags. The existing
`forge_markdown` / `forgeMarkdown` function in `templatehelpers.go` is
unchanged for backward compatibility.

**Consequences:** Templates can now use `{{.Content.Body | markdown}}` for
a richer Markdown→HTML render (tables, language-tagged code blocks, hr)
without any third-party dependency. `TemplateFuncMap` gains one new key
(`"markdown"`); the key count increases from 7 to 8.

---

## Amendment A47 — `templatehelpers.go`: `forge_markdown` delegates to `renderMarkdown`

**Date:** 2026-03-15  
**Status:** Agreed

**Change:** `forgeMarkdown` in `templatehelpers.go` was a separate stub
implementation without table support. The function body is replaced with a
one-line delegation to `renderMarkdown` from `markdown.go`. Both `forge_markdown`
and `markdown` template keys now use the identical full renderer.

**Reason:** A46 added `renderMarkdown` and exposed it as the `"markdown"` key,
but left `forge_markdown` / `forgeMarkdown` unchanged. Any template using
`forge_markdown` continued to produce incorrect output for tables — the
original stub had no table parsing at all.

**Consequences:** `forge_markdown` gains full parity with `markdown`: GFM
table rendering, language-tagged fenced code blocks (`class="language-X"`),
`<hr>` from `---`, and XSS-safe HTML-entity escaping. Features that existed
only in `forgeMarkdown` — `*italic*` and `[link](url)` — are dropped; they
were not part of the documented API and were not present in `renderMarkdown`.
The `applyInline` helper and regex vars (`reMdLink`, `reMdBold`, `reMdItalic`,
`reMdCode`, `reMdHeading`) in `templatehelpers.go` become dead code; they
compile cleanly and are left in place to avoid a cross-file change.

---

## Amendment A48 — `module.go`: set `PublishedAt` on manual publish in `updateHandler`

**Date:** 2026-03-15  
**Status:** Agreed

**Change:** `updateHandler` now sets `PublishedAt` to `time.Now().UTC()` and
calls `m.repo.Save` a second time when the status transitions from any
non-Published value to `Published`. This mirrors the already-correct behaviour
in `processScheduled` (the scheduler path). The second save is committed before
`AfterPublish` signal handlers are dispatched, so handlers see the correct
timestamp.

**Reason:** Items published manually via PUT had `PublishedAt` permanently
stuck at the zero time ("0001-01-01"). The scheduler set it correctly via
`setNodeTime(item, "PublishedAt", now)` but `updateHandler` had no equivalent
step. Any template, feed entry, or AI index entry that rendered
`PublishedAt` showed the wrong date for all manually-published content.

**Consequences:** One additional `repo.Save` call per Draft→Published (or
Scheduled→Published) transition triggered via PUT. For `MemoryRepo` this is
negligible; for `SQLRepo` it is one extra `INSERT OR REPLACE` per manual
publish event, which is acceptable given publish frequency. The response body
returned by `updateHandler` reflects the updated `PublishedAt` value because
`item` is mutated in place by `setNodeTime` before `writeJSON` is called.

---

### Amendment A49 — `mcp.go`/`module.go`/`forge.go`: `MCPModule` contract

**Date:** 2026-03-16
**Status:** Agreed

**Change:** Three coordinated changes across forge core upgrade the v1 MCP stubs
into a real, testable interface that `forge-mcp` (a separate Go module) can
consume without accessing `Module[T]` internals directly.

**`mcp.go`:**
- `mcpOption` gains a field: `type mcpOption struct{ ops []MCPOperation }`.
  `MCP(ops ...MCPOperation) Option` now stores them: `return mcpOption{ops: ops}`.
- `MCPMeta` struct added (exported): `Prefix string`, `TypeName string`,
  `Operations []MCPOperation`.
- `MCPField` struct added (exported): `Name`, `JSONName`, `Type`
  (`"string" | "number" | "boolean" | "datetime"`), `Required bool`,
  `MinLength int`, `MaxLength int`, `Enum []string`. Derived automatically from
  Go struct fields and `forge:` struct tags.
- `MCPModule` interface added (exported): 10 methods — `MCPMeta() MCPMeta`,
  `MCPSchema() []MCPField`, `MCPList`, `MCPGet`, `MCPCreate`, `MCPUpdate`,
  `MCPPublish`, `MCPSchedule`, `MCPArchive`, `MCPDelete`. This interface is the
  sole boundary `forge-mcp` crosses into `forge` core.
- Godoc on `MCPRead`, `MCPWrite`, and `MCP()` updated: "no-op in v1" language
  removed; references to `MCPModule` added.
- `"time"` import added to `mcp.go` (required for `MCPSchedule`'s `time.Time`
  parameter in the `MCPModule` interface).

**`module.go`:**
- `Module[T]` struct gains `mcpOps []MCPOperation` field. The option switch in
  `NewModule` gains `case mcpOption: m.mcpOps = v.ops`. This is the sole
  persistent MCP state on the struct.
- All 10 `MCPModule` methods implemented on `*Module[T]`. Mutating methods share
  the same `repo`, `signals`, `RunValidation`, `invalidateCache`,
  `triggerRebuild`, and `dispatchAfter` calls as the existing HTTP handlers—no
  new I/O paths, no new lifecycle rules.
- Four private helpers added: `typeName(reflect.Type) string`,
  `snakeCase(string) string`, `mcpGoTypeStr(reflect.Type) string`,
  `mcpJSONName(reflect.StructField) string`, `mcpParseForgeTag(string)`,
  `mcpStructField(reflect.StructField) MCPField`.
- `snakeCase` rule: consecutive uppercase letters form one word—
  `MCPPost → mcp_post`, `BlogID → blog_id`.
- Compile-time assertion: `var _ MCPModule = (*Module[struct{ Node }])(nil)`.

**`forge.go`:**
- `App` struct gains `mcpModules []MCPModule` field.
- `App.Content()` type-asserts each `Registrator` against `MCPModule` and
  appends it when `len(mm.MCPMeta().Operations) > 0`. Mirrors the existing
  `schedulerModules`/`rebuilderModules`/`stoppableModules` pattern exactly.
- `App.MCPModules() []MCPModule` accessor added. Returns the live internal slice.
  `forge-mcp` calls this once in `New(app)` to build its registry.

**Call-site syntax** — unchanged before and after:

```go
app.Content(&BlogPost{},
    forge.At("/posts"),
    forge.MCP(forge.MCPRead, forge.MCPWrite),
)
```

**Consequences:**
1. `forge.MCP(...)` options are no longer a no-op at runtime. `App.MCPModules()`
   returns a non-empty slice for apps that use `forge.MCP(...)`.
2. Three new exported types — `MCPMeta`, `MCPField`, `MCPModule` — are the
   stable API surface for `forge-mcp`. No existing exported symbol is removed
   or renamed.
3. `forge` never imports `forge-mcp`. The import direction is one-way.
4. `MCPModule` methods do not gate on `ctx.User().Role()`. Role decisions are
   the caller's responsibility; `forge-mcp` constructs a `forge.Context` with
   the appropriate role before every call.
5. `MCPUpdate` preserves `Node.ID`, `Node.Slug`, and `Node.Status` after the
   JSON merge; status transitions go through the dedicated lifecycle methods.
6. `MCPSchema` includes the embedded `forge.Node` fields Slug, Status,
   PublishedAt, and ScheduledAt; it omits ID, CreatedAt, and UpdatedAt.

---

## Amendment A50 — `auth.go`/`forge.go`/`context.go`/`forge-mcp/mcp.go`: `VerifyBearerToken`, `App.Secret()`, `NewContextWithUser`, `Server` secret auto-inherit

**Date:** 2026-03-16
**Status:** Agreed

**Change:** Four co-dependent additions required to implement `forge-mcp/transport.go`
without leaking test-scoped helpers into production code and without silent
misconfiguration of SSE bearer-token authentication.

**Part 1 — `forge/auth.go`: `VerifyBearerToken`**

Add a new exported free function:

```go
// VerifyBearerToken extracts and verifies the HMAC-signed bearer token from r's
// Authorization header. It returns the authenticated User and true on success,
// or GuestUser and false if the header is absent, malformed, or the signature
// is invalid. secret must be the same value used to sign the token with
// [SignToken]. This is the public counterpart to the unexported authenticate
// method on [BearerHMAC] and is intended for use outside the forge package
// (e.g. forge-mcp SSE transport) where [AuthFunc] is not directly callable.
func VerifyBearerToken(r *http.Request, secret []byte) (User, bool) {
    hdr := r.Header.Get("Authorization")
    if !strings.HasPrefix(hdr, "Bearer ") {
        return GuestUser, false
    }
    token := strings.TrimPrefix(hdr, "Bearer ")
    user, err := decodeToken(token, string(secret))
    if err != nil {
        return GuestUser, false
    }
    return user, true
}
```

The signature takes `*http.Request` and `secret []byte` (not two strings) to
match HTTP handler call sites directly and to accept the `[]byte` secret stored
in `Config.Secret` without a conversion at the call site.

**Part 2 — `forge/forge.go`: `App.Secret()`**

Add one exported accessor method:

```go
// Secret returns the HMAC signing secret from the application configuration.
// It is intended for use by forge-mcp and other companion packages that must
// verify tokens minted with [SignToken] but cannot access [Config] directly.
func (a *App) Secret() []byte { return a.cfg.Secret }
```

**Part 3 — `forge/context.go`: `NewContextWithUser`**

Add a new exported constructor:

```go
// NewContextWithUser returns a [Context] for use in background goroutines or
// non-HTTP transports (e.g. stdio MCP) that require a real User identity.
// Unlike [NewTestContext], this function may appear in production code.
// Unlike [NewBackgroundContext], the User is caller-supplied rather than
// hardcoded to [GuestUser].
func NewContextWithUser(user User) Context {
    rec := httptest.NewRecorder()
    req := httptest.NewRequest(http.MethodGet, "/", nil)
    req = req.WithContext(context.Background())
    return &contextImpl{
        Context:   context.Background(),
        user:      user,
        locale:    "en",
        siteName:  "",
        requestID: NewID(),
        req:       req,
        w:         rec,
    }
}
```

`net/http/httptest` was already imported by `NewTestContext` in `context.go` —
no new dependency is introduced.

**Part 4 — `forge-mcp/mcp.go`: `Server` secret auto-inherit**

Replace the `auth forge.AuthFunc` placeholder field with `secret []byte` and
update `New` to auto-inherit the app's secret. Add a `ServerOption` type and
`WithSecret` option:

```go
type ServerOption func(*Server)

func WithSecret(secret []byte) ServerOption {
    return func(s *Server) { s.secret = secret }
}

type Server struct {
    modules []forge.MCPModule
    secret  []byte
}

func New(app *forge.App, opts ...ServerOption) *Server {
    s := &Server{
        modules: app.MCPModules(),
        secret:  app.Secret(),
    }
    for _, o := range opts {
        o(s)
    }
    if len(s.secret) > 0 && !bytes.Equal(s.secret, app.Secret()) {
        log.Printf("forge-mcp: WithSecret value differs from app Config.Secret — " +
            "tokens minted by forge.SignToken will fail SSE verification")
    }
    return s
}
```

**Consequences:**

1. `forge.VerifyBearerToken` gives `forge-mcp/transport.go` a clean, callable
   authentication path without exposing the unexported `authenticate` method
   on `AuthFunc` or importing test infrastructure.
2. `App.Secret()` eliminates a class of silent misconfiguration (the A45
   analogy): the SSE transport automatically uses the same secret as the rest
   of the application without any extra developer action.
3. `forge.NewContextWithUser` provides a production-safe background context
   constructor calling `NewContextWithUser(forge.User{ID:"stdio", Roles:
   []forge.Role{forge.Admin}})`. `NewTestContext` continues to exist unchanged
   for test code; the new function is the non-test equivalent.
4. `Server.auth forge.AuthFunc` is removed. The field was unexported and only
   ever used as a placeholder — no call site outside `forge-mcp` referenced it.
5. `New(app *forge.App, opts ...ServerOption)` is backward-compatible at
   existing call sites where `New(app)` is called with no options. The
   variadic `opts` parameter adds no obligation.
6. The `bytes` and `log` standard library packages are added to `forge-mcp/mcp.go`
   imports (needed for `bytes.Equal` and `log.Printf` in the mismatch warning).

## Amendment A51 — `templates.go`: `twitter:card` derives from `Head.Type`

**Status:** Agreed  
**Date:** 2026-03-17  
**Scope:** `templates.go` (one-line conditional change), `templates_test.go` (five new sub-tests)

**Problem:**  
`forgeHeadTmpl` only emits `twitter:card = "summary_large_image"` when
`Head.Image.URL` is non-empty. A `Head{Type: "Article"}` with no explicit
image emits `"summary"`, which suppresses the large-image card layout on
Twitter/X for content that is clearly article-like. OG audits on
forge-cms.dev surfaced this as a real-world regression.

**Decision:**  
Extend the `twitter:card` conditional to emit `"summary_large_image"` when
`Head.Type` is `"Article"` or `"Product"`, regardless of whether an image is
present. Priority order (highest to lowest):

1. `Head.Social.Twitter.Card` explicit override — unchanged
2. `Head.Type == "Article"` or `"Product"` — new, derives card from content type
3. `Head.Image.URL` non-empty — existing behaviour preserved
4. Default `"summary"`

**Change:**

```go
// Before
{{- else if .Image.URL}}
<meta name="twitter:card" content="summary_large_image">

// After
{{- else if or (eq .Type "Article") (eq .Type "Product") .Image.URL}}
<meta name="twitter:card" content="summary_large_image">
```

**Consequences:**

1. Article and product pages automatically emit the correct Twitter Card type
   without requiring developers to set `Head.Social.Twitter.Card` explicitly
   or provide a placeholder image.
2. No exported API change — `Head.Type` is already part of `Head` and is set
   by the existing `Article`, `Product` constants in `head.go`.
3. Existing pages that set `Head.Image.URL` are unaffected — the `or`
   short-circuits and the image path still triggers `summary_large_image`.
4. Pages that explicitly set `Head.Social.Twitter.Card = Summary` continue to
   emit `"summary"` even when `Head.Type` is `"Article"` — the override
   remains the highest-priority branch.
5. No README examples break. `example_test.go` requires no changes.
6. Shipped as patch v1.1.1 — no breaking change.

## Amendment A52 — `module.go`/`forge-mcp/mcp.go`: `[]string` fields in MCPSchema and comma-string coercion

**Status:** Agreed
**Date:** 2026-03-17
**Scope:** `module.go` (`mcpGoTypeStr`, `coerceSliceFields`, `MCPCreate`, `MCPUpdate`),
`forge-mcp/mcp.go` (`inputSchema`, `inputSchemaUpdate`),
`module_test.go`, `forge-mcp/mcp_test.go`

**Problem (three related bugs):**

1. `mcpGoTypeStr` in `module.go` has no `case reflect.Slice` branch — slice types
   fall through to `default: return "string"`, so a `[]string` field on a content
   type is advertised to MCP clients as `{"type":"string"}`. Claude Desktop and
   other clients therefore send a plain string value, which `json.Unmarshal` into
   a `[]string` field rejects silently (the field stays nil, no error returned).

2. `inputSchema` and `inputSchemaUpdate` in `forge-mcp/mcp.go` unconditionally emit
   `{"type": f.Type}` with `minLength`/`maxLength`/`enum` constraints regardless of
   the field type. An array field needs `{"type":"array","items":{"type":"string"}}`;
   the constraints are not meaningful for array entries.

3. Even after Bug 1 is fixed, some MCP client versions (observed in Claude Desktop)
   serialise multi-value fields as comma-separated strings (`"tags":"mcp,test"`)
   rather than JSON arrays (`["mcp","test"]`). Without normalisation,
   `json.Unmarshal` into `[]string` silently discards the value.

**Decision:**

Fix all three bugs as a single patch:

1. **`mcpGoTypeStr`** — add `case reflect.Slice: return "array"` before `default`.

2. **`inputSchema` / `inputSchemaUpdate`** — when `f.Type == "array"`, emit
   `{"type":"array","items":{"type":"string"}}` and skip `minLength`/`maxLength`/`enum`
   constraints (which apply to string entries, not arrays).

3. **`coerceSliceFields`** — new unexported helper in `module.go`. Before the
   `json.Marshal(fields)` → `json.Unmarshal(data, pv)` round-trip in `MCPCreate`
   and `MCPUpdate`, walk every struct field of the target type; for any `[]string`
   (or `[]*string`) field whose corresponding `fields` map entry is a plain `string`,
   split on `","`, trim spaces, and replace the entry with `[]any`. The subsequent
   `json.Marshal` then encodes a proper JSON array, and `json.Unmarshal` succeeds.
   `MCPCreate` is also reordered to call `m.newItemPtr()` before the marshal step so
   the element type is available for the coercion walk.

**Consequences:**

1. Content types with `[]string` fields (e.g. `Tags []string`) now produce correct
   `MCPField.Type == "array"` from `MCPSchema()` and `{"type":"array","items":{"type":"string"}}`
   in the MCP tools/list schema, so conforming MCP clients send proper JSON arrays.
2. Clients that send comma-separated strings are transparently normalised; no change
   required at the application layer.
3. No exported API change — `MCPCreate`, `MCPUpdate`, and `MCPSchema` signatures are
   unchanged. `coerceSliceFields` is unexported.
4. No README examples break. `example_test.go` requires no changes.
5. Shipped as patch v1.1.2 — no breaking change.

## Amendment A53 � `module.go`: `negotiate()` prefers `text/html` for absent or wildcard Accept

**Status:** Agreed
**Date:** 2026-03-18
**Scope:** `module.go` (`contentNegotiator.negotiate`), `module_test.go` (four new sub-tests)

**Problem:**
`negotiate()` collapsed three cases into one branch:
```go
if a == "" || a == "*/*" || strings.Contains(a, "application/json") {
    return "application/json"
}
```
When `Accept` is absent or `"*/*"`, the server returned JSON even when templates
were configured. Google Search Console, Googlebot, and many link-preview scrapers
omit `Accept` entirely or send `"*/*"`. These callers received JSON and never saw
the `<head>` structured data (OG tags, JSON-LD, Twitter Card) emitted by
`forge:head`. Rich search results were unavailable for forge-managed content.

**Decision:**
Split the wildcard case from the explicit JSON case:

1. `Accept == ""` or `Accept == "*/*"` ? prefer `"text/html"` if `n.html == true`
   (templates configured), otherwise `"application/json"`.
2. `strings.Contains(a, "application/json")` ? always `"application/json"` (explicit
   request from API clients; unchanged).

This matches RFC 9110 �12.5.1: when `"*/*"` is present the server selects its
preferred representation. For content modules with templates, HTML is the richer
and more useful response.

**Consequences:**

1. Crawlers and link-preview tools that omit `Accept` or send `"*/*"` now receive
   HTML, see `forge:head` structured data, and are eligible for rich search results.
2. API clients that explicitly request `application/json` are unaffected.
3. Modules without templates (`n.html == false`) continue to return `application/json`
   for all wildcard requests � no regression for API-only modules.
4. No exported API change � `contentNegotiator` is unexported.
5. No README examples break. `example_test.go` requires no changes.
6. Shipped as patch v1.1.3 � no breaking change.


## Amendment A56 — `head.go`: `AbsURL(base, path string) string` helper

**Status:** Agreed
**Date:** 2026-03-20
**Scope:** `head.go`, `head_test.go`

**Problem:**
`forge.URL()` returns root-relative paths (e.g. `"/devlog/my-slug"`). `forge:head`
uses `Head.Canonical` verbatim for `og:url` and `<link rel="canonical">`. Any
content type that builds `Canonical` with `forge.URL()` therefore produces an
invalid absolute URL (e.g. `og:url="/devlog/my-slug"` instead of
`og:url="https://example.com/devlog/my-slug"`). Developers must currently prepend
`cfg.BaseURL` manually, which is error-prone and inconsistent (trailing slash
handling varies by site).

**Decision:**
Add `AbsURL(base, path string) string` to `head.go`, immediately after `URL()`.
The function trims any trailing slash from `base`, passes `path` through `URL()`
(which collapses duplicate slashes and guarantees a leading slash), and
concatenates:

```go
func AbsURL(base, path string) string {
    base = strings.TrimRight(base, "/")
    return base + URL(path)
}
```

No change to `forge:head`, `URL()`, or any other existing API. `forge:head`
integration (automatic BaseURL prepend) is deferred to A57.

**Consequences:**

1. Developers can build correct absolute URLs for `Head.Canonical`,
   `Head.Image.URL`, and any other absolute-URL field using
   `forge.AbsURL(cfg.BaseURL, forge.URL(...))` without manual string
   concatenation or trailing-slash guards.
2. `forge-site` S19 workaround (manual string prepend) can be replaced with
   `forge.AbsURL()` in a follow-up amendment.
3. No breaking change. No exported API removed or modified. `strings` is already
   imported in `head.go` — no new imports.
4. Shipped as patch v1.1.4 — no breaking change.

## Amendment A57 — `storage.go`: `quoteIdent()` — double-quote all generated SQL identifiers

**Status:** Agreed
**Date:** 2026-03-20
**Scope:** `storage.go` (`quoteIdent` helper, `SQLRepo.FindByID`, `FindBySlug`, `FindAll`, `Save`, `Delete`), `storage_test.go` (updated expected queries, new `TestSQLRepo_ReservedKeyword_quotes`)

**Problem:**
`SQLRepo` generated unquoted column names in all SQL statements. SQLite and
PostgreSQL treat unquoted identifiers as case-insensitive bare tokens; any
identifier that collides with a SQL reserved keyword (`order`, `group`, `index`,
`check`, `references`, etc.) causes a syntax error or silent misbehaviour at
runtime. A content type with `Order int \`db:"order"\`` would panic under SQLite
and raise a syntax error under PostgreSQL with no indication of the root cause.

**Decision:**
Add `quoteIdent(name string) string` immediately before the `SQLRepoOption`
type declaration in `storage.go`. Apply it to every generated column reference:

- `Save`: `cols[i]`, `setParts` entries (`col=EXCLUDED.col`), and the `ON CONFLICT (id)` key
- `FindAll`: the `WHERE "status" IN (...)` column and the `ORDER BY "col"` column
- `FindByID`: the `WHERE "id" = $1` predicate
- `FindBySlug`: the `WHERE "slug" = $1` predicate
- `Delete`: the `WHERE "id" = $1` predicate

ANSI SQL double-quoting is the correct mechanism for identifier quoting and is
supported by both SQLite (≥ 3.x) and PostgreSQL.

**Consequences:**

1. Reserved keywords (`order`, `group`, `index`, `references`, etc.) work as
   `db` tag values without any workaround.
2. No breaking change — quoting a valid identifier is semantically identical to
   the unquoted form. Existing schemas and queries are unaffected.
3. Existing unit tests updated to assert the now-quoted query strings.
   New `TestSQLRepo_ReservedKeyword_quotes` asserts the `"order"` column is
   quoted in the generated `INSERT … ON CONFLICT … DO UPDATE SET` statement.
4. Shipped as patch v1.1.5 — no breaking change.

---

## Amendment A58 — `forge.go`: `forgeVersions()` — framework version reporting in `/_health` and startup log

**Status:** Agreed
**Date:** 2026-03-20
**Scope:** `forge.go` (`forgeVersions()`, `App.Health()`, `App.Run()`), `forge_test.go` (updated health tests, new `TestApp_health_forgeVersion`, `TestApp_health_configVersion_notExposed`)

**Problem:**
The `/_health` endpoint reported an application-supplied `Config.Version` string
that the framework had no knowledge of, placing operational responsibility for
version management on every application author. There was no way for observability
tooling, monitoring dashboards, or support engineers to discover which version of
Forge (or its companion modules) was actually running inside a binary.

**Decision:**
Add `forgeVersions() map[string]string` to `forge.go`. It calls
`runtime/debug.ReadBuildInfo()` (available since Go 1.12) to discover the
versions of all modules whose path begins with `github.com/forge-cms/forge`:

- `github.com/forge-cms/forge` → key `"forge"`
- Sub-modules (e.g. `github.com/forge-cms/forge/forge-mcp`) → key derived from
  the sub-path with hyphens replaced by underscores (`"forge_mcp"`)

The leading `"v"` is stripped from version strings so the JSON values are clean
(e.g. `"1.1.6"` not `"v1.1.6"`). Both `info.Main` and `info.Deps` are scanned
so the function works whether forge is the main module (dev/test binaries, where
the version is `"(devel)"`) or a versioned dependency.

`App.Health()` calls `forgeVersions()` once at mount time and closes over the
result. The `"version"` key (previously driven by `Config.Version`) is removed;
the new keys are `"forge"` and any detected companion modules:

```json
{"status":"ok","forge":"1.1.6","forge_mcp":"1.0.5"}
```

`App.Run()` calls `forgeVersions()` once before starting `ListenAndServe` and
emits a startup log line to stderr:

```
forge: forge (devel)        // development build
forge: forge 1.1.6, forge_mcp 1.0.5  // production build
```

`Config.Version` is retained in the `Config` struct — its godoc is updated to
clarify it is for application use only and is no longer consumed by `Health()`.

**Consequences:**

1. Observability tooling and health-check consumers can now discover the exact
   Forge version from the health endpoint without any application configuration.
2. The `"version"` key is removed from the `/_health` response — callers that
   relied on it must read `Config.Version` themselves and add it to a custom
   response if needed. This is a **breaking change to the Health() JSON shape**;
   however, because `Config.Version` was seldom set in practice and the new
   behaviour is strictly more informative, this is shipped as a patch (v1.1.6)
   rather than a minor version bump.
3. In development builds (go test, local go run), the version will be `"(devel)"`
   — this is the correct representation from `runtime/debug` and is intentional.
4. `TestApp_health_ok` and `TestApp_health_version` updated; new tests
   `TestApp_health_forgeVersion` and `TestApp_health_configVersion_notExposed`
   added.
5. No new exported symbols. No changes to any interface. No dependency added;
   `runtime/debug` is part of the Go standard library.
6. Shipped as patch v1.1.6.

---

## Amendment A59 — `forge.go`: `httpsRedirect()` — exempt `/_health` from HTTPS redirect

**Status:** Agreed
**Date:** 2026-03-20
**Scope:** `forge.go` (`httpsRedirect()`), `forge_test.go` (new `TestApp_health_httpsExempt`)

**Problem:**
When `Config.HTTPS` is true, the `httpsRedirect()` middleware unconditionally
redirects all plain-HTTP requests with a 301 to their HTTPS equivalent. Reverse
proxies that perform health checks over plain HTTP (e.g. Caddy `health_uri`,
internal Kubernetes liveness probes) receive a 301 instead of 200, causing
the proxy to report the upstream as unhealthy and potentially taking the site
down.

**Decision:**
Add a path check inside `httpsRedirect()` before the TLS / `X-Forwarded-Proto`
check: if `r.URL.Path == "/_health"`, call `next.ServeHTTP(w, r)` and return
immediately. The check is placed first so the hot path for the health endpoint
skips the TLS check entirely.

**Consequences:**

1. Reverse-proxy health checks over plain HTTP (Caddy `health_uri`, etc.) now
   receive `200 application/json` regardless of `Config.HTTPS`.
2. All other plain-HTTP requests are still redirected to HTTPS as before.
3. Security note: `/_health` is exempt from HTTPS redirect but not exempt from
   DDoS risk. The risk is considered acceptable: the endpoint performs no
   database queries, no file I/O, and no computation — it returns a static JSON
   response of ~50 bytes. A reverse proxy (e.g. Caddy) sits in front and
   handles connection limiting independently of Forge. An attacker targeting
   `/_health` gains no meaningful advantage over targeting any other public endpoint.
4. New test `TestApp_health_httpsExempt` confirms `/_health` returns 200 on a
   plain-HTTP request when `Config.HTTPS` is true.

---

## Amendment A60 — `forge.go`: `New()` enforces `MustConfig()` automatically

**Status:** Agreed
**Date:** 2026-04-02
**Scope:** `forge.go` (`New()`)

**Problem:**
`forge.New()` accepted a `Config` without validation. An app with an empty
`BaseURL` or a `Secret` shorter than 16 bytes would start successfully and only
fail at the first request that needed those values (e.g. a CSRF check or a
cookie signature). This delayed error discovery until runtime, often in
production.

**Decision:**
Add `cfg = MustConfig(cfg)` as the first line of `New()`. The existing
`MustConfig` function already performs all required checks and panics with a
descriptive message on failure. Callers that previously passed a raw `Config`
directly to `New` now get an immediate panic at process start if the config is
invalid.

**Consequences:**

1. Breaking change: apps that passed an invalid `Config` to `New()` directly
   (without calling `MustConfig`) and somehow started will now panic at startup.
   This is intentional — a misconfigured app must not silently start.
2. The godoc on `New()` is updated to document the panic behaviour and remove
   the recommendation to call `MustConfig` manually.
3. Calling `forge.New(forge.MustConfig(cfg))` remains correct and is still the
   idiomatic form documented in examples; the double call is a no-op because
   `MustConfig` is pure and returns the validated `Config` unchanged.
5. No exported symbols added or changed. No interfaces modified.
6. Shipped as patch v1.1.7.

---

## Amendment A61 — `OGDefaults`, `AppSchema`, and `forge:head` receiver change

**Status:** Agreed
**Date:** 2026-04-02
**Scope:** `social.go`, `schema.go`, `forge.go` (`seoState`, `Handler()`),
`module.go`, `templates.go`, `templatedata.go`

**Problem:**
Forge sites had no way to declare app-level Open Graph fallbacks (`og:image`,
`twitter:creator`) or a `twitter:site` handle once at the app level. Each
content type had to repeat these values, or they were absent entirely. There
was also no mechanism for site-wide JSON-LD structured data (e.g. an
Organization block) without duplicating it in every content type.

**Decision:**

1. **`OGDefaults`** (`social.go`) — new `SEOOption` applied via `app.SEO()`.
   Fields: `Image Image`, `TwitterSite string`, `TwitterCreator string`.
   `mergeOGDefaults(head, d)` applies fallbacks at render time: `Head.Image`
   is replaced when empty; `Head.Social.Twitter.Creator` is replaced when empty.
   `TwitterSite` is app-level only (no per-item override).
2. **`AppSchema`** (`schema.go`) — new `SEOOption` applied via `app.SEO()`.
   Fields: `Type`, `Name`, `URL`, `Logo`. `renderAppSchema` produces a
   pre-rendered `template.HTML` JSON-LD block at `Handler()` time.
3. **`forge:head` receiver change** — the partial now receives the full
   `TemplateData` value (`{{template "forge:head" .}}`) instead of just `Head`
   (`{{template "forge:head" .Head}}`). All field references inside the partial
   are prefixed with `.Head.`. New tags added: `twitter:site` (from
   `.OGDefaults.TwitterSite`) and `{{.AppSchema}}` (auto-emitted).
4. **`TemplateData[T]`** gains two fields: `OGDefaults *OGDefaults` and
   `AppSchema template.HTML`.
5. **`seoState`** gains `ogDefaults *OGDefaults` and `appSchema *AppSchema`.
   `App.Handler()` pushes both to all template modules via `setSEODefaults`
   using the same inline interface pattern as `setSiteName`.

**Consequences:**

1. **Breaking call-site change:** `{{template "forge:head" .Head}}` must be
   updated to `{{template "forge:head" .}}` in all developer templates. The
   three built-in example apps and all `README.md` examples are updated in this
   commit. The `example_test.go` compile tests remain green.
2. `twitter:site` is now emitted when `OGDefaults.TwitterSite` is set —
   previously this tag was never emitted by Forge at all.
3. `AppSchema` JSON-LD is auto-emitted every page when configured; no per-
   template change required.
4. `OGDefaults` and `AppSchema` are zero-safe: nil values produce no output.
5. Go generics / `html/template` compatibility confirmed: `TemplateData[T]` is
   monomorphized at compile time; reflection sees concrete field names at
   runtime regardless of `T`.

---

## Amendment A62 — Shared template partials

**Status:** Agreed  
**Date:** 2026-04-02  
**Scope:** `templates.go`, `forge.go`, `module.go`

**Problem:**
Every Forge site duplicated nav and footer HTML across all module templates
(`list.html`, `show.html`) and any custom handler templates (e.g. `home.html`).
There was no mechanism to define a shared partial once and include it across
all templates.

**Decision:**

1. **`App.Partials(dir string) *App`** (`forge.go`) — stores a partials
   directory path; returns `*App` for chaining. Any `*.html` file in `dir` is
   treated as a shared partial and registered into every module template set.
   Files must use `{{define "name"}}...{{end}}` syntax (same convention as
   `forge:head`). Files are loaded and registered in alphabetical order for
   determinism. Opt-in: sites with no `Partials()` call are unaffected.
2. **`loadPartials(dir string) ([]string, error)`** (`templates.go`) — reads
   all `*.html` files from `dir` alphabetically. Returns `(nil, nil)` when
   `dir` is empty. Returns an error if the directory does not exist.
3. **`Module[T].setPartials([]string)`** (`templates.go`) — stores partial
   sources on the module. Called by `App.Run()` before `parseTemplates()` via
   the same inline interface pattern as `setSiteName` and `setSEODefaults`.
4. **`parseOneTemplate`** now accepts a `partials []string` argument and
   registers each source into the template set after `forge:head`.
5. **`App.MustParseTemplate(path string) *template.Template`** (`forge.go`) —
   loads a single template file, registers `TemplateFuncMap`, `forge:head`, and
   all partials from `a.partialsDir`. Panics on error (consistent with
   `MustConfig`). For custom `app.Handle()` route handlers that need shared
   partials (e.g. a home page template).

**Consequences:**

1. Module templates: partials auto-injected at `parseTemplates()` time with no
   call-site change to `list.html` / `show.html`.
2. Custom handler templates: developer calls `app.MustParseTemplate(path)` once
   in `main()` before `app.Run()`.
3. `parseOneTemplate` signature change is internal — no exported API touched.
4. `loadPartials` / `setPartials` / `partialsDir` are all internal; the only
   exported symbols added are `App.Partials` and `App.MustParseTemplate`.
5. Missing partials directory returns an error from `App.Run()` (not a panic)
   so the failure message is clear; `MustParseTemplate` panics for consistency
   with the `Must` naming convention.

---

## Amendment A63 — HeadAssets: static linked assets via forge:head

**Status:** Agreed  
**Date:** 2026-04-03  
**Scope:** `head.go`, `templates.go`, `templatedata.go`, `forge.go`, `module.go`

**Problem:**
Forge provided no built-in way to inject site-wide static assets — preconnect
hints, stylesheets, favicon `<link>` elements, and `<script>` tags — into
`forge:head`. Developers had to hard-code these in every list/show template or
create a shared partial that duplicated the logic. Neither approach let the
framework manage the `<head>` consistently.

**Decision:**

1. **`HeadAssets`** (`head.go`) — new `SEOOption` struct. Applied via
   `app.SEO(&forge.HeadAssets{...})`. Fields:
   - `Preconnect []string` — emitted as `<link rel="preconnect" href="…">`
   - `Stylesheets []string` — emitted as `<link rel="stylesheet" href="…">`
   - `Favicons []FaviconLink` — emitted as typed `<link>` elements
   - `Scripts []ScriptTag` — emitted as `<script>` elements (external or inline)
2. **`FaviconLink`** (`head.go`) — struct with `Rel`, `Type`, `Sizes`, `Href`.
   `Type` and `Sizes` are omitted from the emitted `<link>` when empty.
3. **`ScriptTag`** (`head.go`) — struct with `Src`, `Body template.JS`, `Async`,
   `Defer`. `Async`/`Defer` are only emitted for external scripts (`Src`
   non-empty). `Body` is typed `template.JS` (an alias for `string` from
   `html/template`) so Go's context-aware escaping does not quote inline bodies.
4. **Emission order** in `forgeHeadTmpl` (`templates.go`): preconnect →
   stylesheets → favicons → scripts. Scripts are always last to give
   stylesheets time to load.
5. **Propagation chain** — mirrors `OGDefaults` and `AppSchema`:
   - `seoState.headAssets *HeadAssets` added to `forge.go`
   - `App.Handler()` interface assertion updated to 3-arg `setSEODefaults`:
     `interface{ setSEODefaults(*OGDefaults, *AppSchema, *HeadAssets) }`
   - `Module[T].headAssets *HeadAssets` field added to `module.go`
   - `Module[T].setSEODefaults(d, a, ha)` body updated in `templates.go`
   - `TemplateData[T].HeadAssets *HeadAssets` field added to `templatedata.go`
   - Both render paths (`renderListHTML`, `renderShowHTML`) set `data.HeadAssets`
6. **Critical sync constraint**: the `forge.go` interface assertion and the
   `module.go` `setSEODefaults` method signature must always match. A mismatch
   produces no compile error but silently breaks `HeadAssets` propagation at
   runtime.

**Consequences:**

1. `HeadAssets`, `FaviconLink`, `ScriptTag` are new exported types in `head.go`.
   The `html/template` import is added to `head.go`.
2. `setSEODefaults` signature change (2-arg → 3-arg) is internal — the method
   is unexported and called only via the inline interface assertion in
   `App.Handler()`.
3. `TemplateData[T]` gains one new field (`HeadAssets`). Existing templates that
   do not use `HeadAssets` are unaffected.
4. Nil `HeadAssets` (no `app.SEO(&HeadAssets{...})` call) produces no output —
   the template block is guarded by `{{- if .HeadAssets}}`.
5. Inline script bodies must be wrapped with `template.JS(...)` at the call
   site. This is explicit and secure: it reminds developers never to pass
   user-supplied content as raw JavaScript.

---

## Amendment A64 — PageHead: embeddable head struct for custom handlers

**Status:** Agreed  
**Date:** 2026-04-03  
**Scope:** `head.go`, `templatedata.go`

**Problem:**
`forge:head` works in module templates (list.html, show.html) because they
receive `TemplateData[T]`, which carries `.Head`, `.OGDefaults`, `.AppSchema`,
and `.HeadAssets`. Custom handlers — like a home page — define their own data
structs and could not use `{{template "forge:head" .}}` because those fields
were absent from any struct they defined.

**Decision:**

1. **`PageHead`** (`head.go`) — new exported struct holding exactly the four
   fields that `forge:head` reads: `Head Head`, `OGDefaults *OGDefaults`,
   `AppSchema template.HTML`, `HeadAssets *HeadAssets`.
2. **`TemplateData[T]`** (`templatedata.go`) — refactored to embed `PageHead`
   as an **anonymous (unnamed) field**. Go's `html/template` engine promotes
   all fields of the embedded struct to the top level, so existing template
   access paths (`.Head`, `.OGDefaults`, `.AppSchema`, `.HeadAssets`) are
   preserved without modification.
3. **`NewTemplateData`** constructor — signature unchanged. The body now writes
   `PageHead: PageHead{Head: head}` instead of `Head: head`.
4. **Zero breaking changes** — field access paths in templates, the
   `NewTemplateData` signature, and the `templates.go` render paths (which use
   field assignment, not struct-literal syntax) are all unaffected.

**Consequences:**

1. `PageHead` is a new exported type in `head.go`. Any developer building a
   custom handler struct can embed it to gain `forge:head` support:
   ```go
   type homeData struct {
       forge.PageHead
       Posts []*Post
   }
   ```
2. Struct literal initialization of `TemplateData[T]` with `Head:`,
   `OGDefaults:`, `AppSchema:`, or `HeadAssets:` keys directly is no longer
   valid — callers must use `PageHead: forge.PageHead{Head: ...}`. This affects
   only internal test files (updated in the same commit); public API callers use
   `NewTemplateData` which is unchanged.
3. `templatedata.go` drops its `"html/template"` import (now unused); the import
   lives in `head.go` where `PageHead.AppSchema template.HTML` is declared.
4. `forgeHeadTmpl` is unchanged — it already accesses `.Head`, `.OGDefaults`,
   `.AppSchema`, `.HeadAssets` at the top level, which anonymous embedding
   satisfies.

---

## Amendment A65 — ContextFunc: per-request extra data for module templates

**Status:** Agreed  
**Date:** 2026-04-04  
**Scope:** `module.go`, `templatedata.go`, `templates.go`

**Problem:**
Module show and list templates receive only `TemplateData[T]`, which contains
the single content item being rendered (or a slice for list). There is no
framework-supported way to pass additional data — for example, all published
docs for a sidebar — into a template without writing a custom handler that
bypasses the module's routing, authentication, caching, and lifecycle layers.

**Decision:**

1. **`ContextFunc(fn)`** (`module.go`) — new module `Option`. Accepts a
   function `func(Context, any) (any, error)`. The function is called once
   per list or show render, after all module wiring has run. Its return value
   is stored in `TemplateData.Extra`. The function receives the current
   `Context` and the item being rendered (T for show, []T for list).
2. **`TemplateData.Extra any`** (`templatedata.go`) — new field. Zero value is
   `nil` when no `ContextFunc` is configured. Templates access it as `{{.Extra}}`
   and may assign it to a named variable:
   ```
   {{- $nav := .Extra}}
   {{template "sidebar" $nav}}
   ```
3. **`resolveExtra`** (`templates.go`) — unexported method on `Module[T]`.
   Called by `renderListHTML` and `renderShowHTML` after all other `data`
   fields are set. If `contextFunc` is nil it returns nil immediately (zero
   overhead for all sites that do not use the option). If the function returns
   an error, `resolveExtra` logs and returns nil — the render is never aborted.
4. **`NewTemplateData` signature unchanged.** `Extra` is set by the render
   path via direct field assignment, consistent with how `OGDefaults`,
   `AppSchema`, and `HeadAssets` are set after construction.

**Rationale for `any` item parameter:**
`ContextFunc` uses `any` rather than `T` because a single function type must
work for both list (`[]T`) and show (`T`) call sites. A `T`-typed overload
would require two distinct options, two fields, and two call sites — more
surface area for one pattern that is already clear at the call site.

**Consequences:**

1. Sites without `ContextFunc` are unaffected; `resolveExtra` is a nil-check
   short-circuit with no allocation.
2. `ContextFunc` errors must never abort the render — log and return nil.
   This is consistent with how signal errors are handled: they are logged but
   do not surface to the HTTP client.
3. `resolveExtra` lives in `templates.go`, not `module.go`, because it is
   purely a render concern. This follows the file-organisation principle that
   logic belongs in the file where it is consumed.
4. No change to `forgeHeadTmpl`, `NewTemplateData` signature, or any existing
   template field paths.

---

