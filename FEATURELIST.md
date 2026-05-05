# Forge — feature list

Complete list of what Forge generates and includes automatically.
Updated with every amendment that adds or changes a feature.
Last updated: v1.16.0 (A84).

---

## Routes and feeds

- List route — `GET /{prefix}` returns all Published items as JSON or HTML
- Detail route — `GET /{prefix}/{slug}` returns a single Published item
- RSS feed — `GET /{prefix}/feed.xml` plus aggregate `GET /feed.xml`
- Sitemap entries — `GET /{prefix}/sitemap.xml` merged into `GET /sitemap.xml`; regenerated on every publish, update, and delete — never stale, no cron
- SEO meta tags — `<title>`, `<meta description>`, canonical URL in every `<head>`
- Open Graph tags — `og:title`, `og:image`, `og:type`, `article:published_time` and more
- Twitter Cards — `twitter:card`, `twitter:title`, `twitter:image`
- AI index formats — `/llms.txt` compact index, `/llms-full.txt` Markdown corpus, per-item `/aidoc` token-efficient endpoint
- Content negotiation — one URL returns HTML to browsers, JSON to APIs, and AI-optimised format to agents; no extra code

## Storage

- Database table derived from struct — works with PostgreSQL, SQLite, and MySQL
- SQLRepo — production SQL repository with automatic table naming and upserts
- SeqRepository — lazy streaming interface for large datasets (`iter.Seq2[T, error]`)

## Rendering

- Markdown rendering (`forge_markdown`) — including HTML passthrough for trusted blocks
- Trusted HTML (`forge_html`) — verbatim emission of pre-rendered HTML fields
- Field semantics in MCP schema — `forge_format` and `forge_description` struct tags; AI agents understand field intent without extra prompting

## Lifecycle

- Draft / Scheduled / Published / Archived enforcement — hardwired, cannot be disabled
- Scheduled publishing — automatic `Scheduled → Published` transition at `ScheduledAt`; no external cron
- 404 on everything non-Published — guests, search engines, and AI crawlers see nothing until explicitly published

## Access control

- Role-based access — Guest → Author → Editor → Admin enforced per module per operation
- Struct-tag validation — `forge:"required,min=3"` enforced identically for HTTP, API, and MCP calls
- Token management — named revocable tokens with role scoping; `ensureBootstrap` auto-creates the first admin token on first start
- ErrLastAdmin guard — cannot revoke the last admin token

## Navigation

- NavTree — first-class navigation abstraction (`NavModeDB` / `NavModeCode`)
- 4 MCP nav tools — `list_nav_items`, `create_nav_item`, `update_nav_item`, `delete_nav_item`

## MCP tools (forge-mcp)

Per content type — automatically derived, no manual definition:

- `create_[type]` — Author+
- `update_[type]` — Author+
- `publish_[type]` — Author+
- `schedule_[type]` — Author+
- `archive_[type]` — Author+
- `delete_[type]` — Author+
- `list_[type]s` — Editor+
- `get_[type]` — Editor+

## Template infrastructure

- Shared partials — `App.Partials` + `MustParseTemplate`
- HeadAssets — favicons, stylesheets, preconnect, scripts injected via `forge:head` on every page
- ContextFunc — per-request extra data passed to module templates
- Static file serving — `App.Static` serves from embedded FS in production (immutable cache headers) and from disk in development

## SEO and structured data

- JSON-LD — Article, Product, FAQ, HowTo, Event, Recipe, Review, Organisation rich results
- OGDefaults — site-wide fallback OG image and Twitter handles
- AppSchema — site-wide Organisation/WebSite JSON-LD on every page
- Robots — `<meta name="robots">` set per lifecycle status; configurable AI crawler policy (`AskFirst`, `Allow`, `Disallow`)

## Operations

- File-based configuration — `key = value` format, fail-fast at startup; 10 keys
- `/_health` endpoint — returns framework version and status; exempt from HTTPS redirect
- Zero third-party dependencies in core — pure stdlib; driver is always your choice
- Cookie compliance — `/.well-known/cookies.json` declares all cookies with category and consent requirements
- Redirect tracking — automatic 301 on slug rename, 410 Gone on archive/delete; `/.well-known/redirects.json` for audit
- Security headers — CSP, HSTS, X-Frame-Options, Referrer-Policy in one middleware call
- Graceful shutdown — drains in-flight requests on SIGINT/SIGTERM

## forge-media (separate module)

- Upload, serve, list, and delete files via HTTP and MCP
- Alt text enforced on image uploads (WCAG 1.1.1)
- `os.Root` path traversal protection (Go 1.24+)
- Configurable upload directory and max file size

## forge-cli (separate module)

- `forge-cli init` — bootstrap a new instance from the terminal
- Content CRUD — create, update, publish, unpublish, archive, delete, list, get
- Token management — create, list, revoke (Admin role required)
- Media operations — upload, list, delete

## Developer and AI-agent experience

- Typed MCP tools derived directly from content type — no manual schema definition
- Field semantics — AI agents understand what each field means without extra prompting
- Agents operate under the same access rules as humans — no special bypass
- Content negotiation — agents receive an AI-optimised format, not raw HTML
- Go codebase designed to be readable and extensible by AI agents
- `forge.Verb(Noun)` naming throughout — no abbreviations, no clever names
