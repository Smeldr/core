# Forge — feature list

Complete list of what Forge generates and includes automatically.
Updated with every amendment that adds or changes a feature.
Last updated: v1.20.0 (A94) + forge-social v0.5.1 + forge-cli v0.8.0.

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
- Draft preview — signed `?preview=<token>` URL grants read access to Draft or Scheduled content without login; Archived items are never previewable

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
- `delete_[type]` — Editor+
- `list_[type]s` — Editor+
- `get_[type]` — Editor+

Admin tools (require Admin role):

- `create_preview_url` — generates a signed draft preview URL for a Draft or Scheduled item
- `create_upload_token` — generates a short-lived upload token for `POST /media` (Author+)
- `create_webhook`, `list_webhooks`, `delete_webhook` — manage outbound endpoints
- `list_webhook_deliveries`, `retry_webhook` — delivery introspection and retry
- `create_token`, `list_tokens`, `revoke_token` — token management

MCP resource subscriptions:

- `resources/subscribe` and `resources/unsubscribe` JSON-RPC methods
- SSE transport assigns per-connection session ID; notifies via `notifications/resources/updated`
- Clients receive real-time push when published content changes

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
- Signal bus — `app.OnSignal(Signal, handler)` registers subscribers for `AfterPublish`, `AfterSchedule`, `AfterArchive`, `AfterDelete`; `SignalEvent` carries Type, Slug, Title, URL, Timestamp, PreviousState, ActorRole, ActorID; handlers run synchronously in the publish goroutine and must enqueue-and-return

## forge-media (separate module)

- Upload, serve, list, and delete files via HTTP and MCP
- Alt text enforced on image uploads (WCAG 1.1.1)
- `os.Root` path traversal protection (Go 1.24+)
- Configurable upload directory and max file size
- AVIF support — `image/avif` accepted and magic-byte detected alongside JPEG, PNG, WebP, GIF
- Upload token — `Authorization: UploadToken <token>` accepted on `POST /media`; image-only MIME whitelist for token uploads; Bearer-token uploads unaffected
- Hex filename prefix — stored as `<32-hex>-<sanitized>` preventing collisions without exposing upload timing

## forge-cli (separate module)

- `forge-cli init` — bootstrap a new instance from the terminal
- Content CRUD — create, update, publish, unpublish, archive, delete, list, get
- Token management — create, list, revoke (Admin role required)
- Media operations — upload, list, delete
- Webhook management — create, list, delete, view deliveries, retry
- Draft preview — `forge preview <prefix> <slug>` prints a signed preview URL (Admin role required)
- Social commands (v0.7.0): `social credential create/list`, `social post create/list/get/publish/archive/delete`, `social post queue`, `social schedule create/show/pause/resume/delete`
- Social commands (v0.8.0): `social credential get/delete`, `social platform configure` (DB-driven OAuth app config for mastodon/linkedin/x); `social credential create` now accepts `--platform x`

## forge-social (separate module)

- `forge-cms.dev/forge-social` — social post scheduling and AI agent routing
- Two scheduling models: explicit `scheduled_at` (Model 1) and slot-queue via `PublicationSchedule` (Model 2, v0.4.0+)
- Platforms: Mastodon, LinkedIn, **X (Twitter)** (v0.5.0+)
- **DB-driven platform config** (v0.5.0+): OAuth 2.0 app credentials stored AES-256-GCM encrypted in DB; `create_platform_config` MCP tool (Admin role); no environment variables required after initial setup
- **X OAuth 2.0 + PKCE** (v0.5.0+): `S256` code challenge; server-side verifier storage; 280-char body limit (terminal error, never truncated)
- OAuth credentials encrypted at rest (AES-256-GCM)
- `PublicationSchedule` — recurring weekly slots (weekday, HH:MM, IANA timezone) per credential; FIFO queue; catch-up policy on restart
- Layer 1 agent routing — `social.AddRoutes(app, forgesocial.OnPublish(...))` fires outbound HTTP on lifecycle signals; HMAC-signed payload; exponential backoff retry
- 15 MCP tools across PostModule, CredentialModule, ConfigModule, ScheduleModule
- Full CLI parity in forge-cli v0.8.0

## Outbound webhooks

- `WebhookStore` — SQLite-backed endpoint registry with AES-256-GCM secret encryption
- SSRF-safe URL validation — HTTPS required, no private/loopback IPs permitted
- HMAC-SHA256 payload signing — signed string `"<ts>.<body>"`, header `sha256=<hex>`
- Exponential backoff — 4^attempt ±20% jitter, cap 1 hour
- Per-endpoint circuit breakers — open after 5 consecutive failures for 5 minutes
- Dead-letter after 7 attempts — job status transitions to "dead"
- `App.Webhooks(store)` — wires store and starts the background worker pool
- MCP tools for Admin agents: `create_webhook`, `list_webhooks`, `delete_webhook`,
  `list_webhook_deliveries`, `retry_webhook`
- `AfterPublish`, `AfterUpdate`, `AfterDelete`, `AfterSchedule` signals trigger jobs automatically
- `forge-cli webhook` subcommands for all operations

## Developer and AI-agent experience

- Typed MCP tools derived directly from content type — no manual schema definition
- Field semantics — AI agents understand what each field means without extra prompting
- Agents operate under the same access rules as humans — no special bypass
- Content negotiation — agents receive an AI-optimised format, not raw HTML
- Go codebase designed to be readable and extensible by AI agents
- `forge.Verb(Noun)` naming throughout — no abbreviations, no clever names
