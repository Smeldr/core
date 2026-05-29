# Forge — feature list

Complete list of what Forge generates and includes automatically.
Updated with every amendment that adds or changes a feature.
Last updated: v1.30.0 (A111) + smeldr.dev/mcp v1.12.0 + smeldr.dev/oauth v0.1.3 + smeldr.dev/social v0.7.3 + smeldr.dev/agent v0.5.0 + smeldr.dev/cli v0.9.2 + smeldr.dev/media v1.3.0.

## Module stability

| Package | Version | Stability |
|---------|---------|-----------|
| `smeldr.dev/core` | v1.30.0 | Stable |
| `smeldr.dev/mcp` | v1.12.0 | Stable |
| `smeldr.dev/oauth` | v0.1.3 | Beta |
| `smeldr.dev/core/pgx` | — | Beta |
| `smeldr.dev/media` | v1.3.0 | Beta |
| `smeldr.dev/cli` | v0.9.2 | Beta |
| `smeldr.dev/social` | v0.7.0 | Experimental |
| `smeldr.dev/agent` | v0.5.0 | Experimental |

**Stable** — API will not break without a deprecation notice.  
**Beta** — Functional and tested; API may change in minor releases.  
**Experimental** — Working implementation; API is actively evolving.

Stability label changes require architect sign-off.  
Graduation criteria: Experimental → Beta requires API unchanged across three
consecutive minor releases with integration tests present. Beta → Stable requires
architect approval and a major version bump for any future breaking changes.
Labels are reviewed at every module minor or major version bump.

---

## Routes and feeds — Stable

- List route — `GET /{prefix}` returns all Published items as JSON or HTML
- Detail route — `GET /{prefix}/{slug}` returns a single Published item
- RSS feed — `GET /{prefix}/feed.xml` plus aggregate `GET /feed.xml`
- Sitemap entries — `GET /{prefix}/sitemap.xml` merged into `GET /sitemap.xml`; regenerated on every publish, update, and delete — never stale, no cron
- SEO meta tags — `<title>`, `<meta description>`, canonical URL in every `<head>`
- Open Graph tags — `og:title`, `og:image`, `og:type`, `article:published_time` and more
- Twitter Cards — `twitter:card`, `twitter:title`, `twitter:image`
- AI index formats — `/llms.txt` compact index, `/llms-full.txt` Markdown corpus, per-item `/aidoc` token-efficient endpoint
- Content negotiation — one URL returns HTML to browsers, JSON to APIs, and AI-optimised format to agents; no extra code

## Storage — Stable

- Database table derived from struct — works with PostgreSQL, SQLite, and MySQL
- SQLRepo — production SQL repository with automatic table naming and upserts
- SeqRepository (Beta) — lazy streaming interface for large datasets (`iter.Seq2[T, error]`)

## Rendering — Stable

- Markdown rendering (`smeldr_markdown`) — including HTML passthrough for trusted blocks
- Trusted HTML (`forge_html`) — verbatim emission of pre-rendered HTML fields
- Field semantics in MCP schema — `smeldr_format` and `smeldr_description` struct tags; AI agents understand field intent without extra prompting

## Lifecycle — Stable

- Draft / Scheduled / Published / Archived enforcement — hardwired, cannot be disabled
- Scheduled publishing — automatic `Scheduled → Published` transition at `ScheduledAt`; no external cron
- 404 on everything non-Published — guests, search engines, and AI crawlers see nothing until explicitly published
- Draft preview — signed `?preview=<token>` URL grants read access to Draft or Scheduled content without login; Archived items are never previewable

## Access control — Stable

- Role-based access — Guest → Author → Editor → Admin enforced per module per operation
- Struct-tag validation — `forge:"required,min=3"` enforced identically for HTTP, API, and MCP calls
- Token management — named revocable tokens with role scoping; `ensureBootstrap` auto-creates the first admin token on first start
- ErrLastAdmin guard — cannot revoke the last admin token

## Navigation — Stable

- NavTree — first-class navigation abstraction (`NavModeDB` / `NavModeCode`)
- 4 MCP nav tools — `list_nav_items`, `create_nav_item`, `update_nav_item`, `delete_nav_item`

## MCP tools (smeldr.dev/mcp) — Stable

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

OAuth 2.1 for remote MCP servers (smeldr.dev/oauth v0.1.2):

- `forgemcp.WithOAuth(*forgeoauth.Server)` — enables OAuth 2.1 on the MCP server; all HTTP endpoints require Bearer
- `GET /.well-known/oauth-protected-resource` — RFC 9728 protected resource metadata
- `GET /.well-known/oauth-authorization-server` — RFC 8414 authorization server metadata (served by smeldr.dev/oauth)
- PKCE S256 mandatory, CIMD stateless client validation, `offline_access` scope for refresh tokens
- Scope mapping: `mcp` → Author role, `mcp:admin` → Admin role
- SQLite-backed token store (`forgeoauth.NewSQLiteStore`)

MCP resource subscriptions (Beta):

- `resources/subscribe` and `resources/unsubscribe` JSON-RPC methods
- SSE transport assigns per-connection session ID; notifies via `notifications/resources/updated`
- Clients receive real-time push when published content changes

## Template infrastructure — Stable

- Shared partials — `App.Partials` + `MustParseTemplate`
- HeadAssets — favicons, stylesheets, preconnect, scripts injected via `smeldr:head` on every page
- ContextFunc — per-request extra data passed to module templates
- Static file serving — `App.Static` serves from embedded FS in production (immutable cache headers) and from disk in development

## SEO and structured data — Stable

- JSON-LD — Article, Product, FAQ, HowTo, Event, Recipe, Review, Organisation rich results
- OGDefaults — site-wide fallback OG image and Twitter handles
- AppSchema — site-wide Organisation/WebSite JSON-LD on every page
- Robots — `<meta name="robots">` set per lifecycle status; configurable AI crawler policy (`AskFirst`, `Allow`, `Disallow`)

## Operations — Stable

- File-based configuration — `key = value` format, fail-fast at startup; 10 keys including `og_image` (operator override for site OG image without rebuild; file value takes precedence over Go-code default)
- `/_health` endpoint — returns framework version and status; exempt from HTTPS redirect
- Zero runtime dependencies in core — pure stdlib; driver is always your choice (test suite uses `modernc.org/sqlite` for in-process SQL integration tests)
- Cookie compliance — `/.well-known/cookies.json` declares all cookies with category and consent requirements
- Redirect tracking — `App.Redirect` / `RedirectStore` registers 301 Permanent and 410 Gone entries; `/.well-known/redirects.json` serves the full redirect table for audit and CDN sync
- Security headers — CSP, HSTS, X-Frame-Options, Referrer-Policy in one middleware call
- Graceful shutdown — drains in-flight requests on SIGINT/SIGTERM
- Signal bus — `app.OnSignal(Signal, handler)` registers subscribers for `AfterPublish`, `AfterSchedule`, `AfterArchive`, `AfterDelete`; `SignalEvent` carries Type, Slug, Title, URL, Timestamp, PreviousState, ActorRole, ActorID; handlers run synchronously in the publish goroutine and must enqueue-and-return

## smeldr.dev/media — Beta

- Upload, serve, list, and delete files via HTTP and MCP
- Alt text enforced on image uploads (WCAG 1.1.1)
- `os.Root` path traversal protection (Go 1.24+)
- Configurable upload directory and max file size
- AVIF support — `image/avif` accepted and magic-byte detected alongside JPEG, PNG, WebP, GIF
- Upload token — `Authorization: UploadToken <token>` accepted on `POST /media`; image-only MIME whitelist for token uploads; Bearer-token uploads unaffected
- Hex filename prefix — stored as `<32-hex>-<sanitized>` preventing collisions without exposing upload timing

## smeldr.dev/cli — Beta

- `forge-cli init` — bootstrap a new instance from the terminal
- Content CRUD — create, update, publish, unpublish, archive, delete, list, get
- Token management — create, list, revoke (Admin role required)
- Media operations — upload, list, delete
- Webhook management — create, list, delete, view deliveries, retry
- Draft preview — `forge preview <prefix> <slug>` prints a signed preview URL (Admin role required)
- Social commands (v0.7.0): `social credential create/list`, `social post create/list/get/publish/archive/delete`, `social post queue`, `social schedule create/show/pause/resume/delete`
- Social commands (v0.8.0): `social credential get/delete`, `social platform configure` (DB-driven OAuth app config for mastodon/linkedin/x); `social credential create` now accepts `--platform x`

## smeldr.dev/social — Experimental

- `smeldr.dev/social` — social post scheduling and AI agent routing
- Two scheduling models: explicit `scheduled_at` (Model 1) and slot-queue via `PublicationSchedule` (Model 2, v0.4.0+)
- Platforms: Mastodon, LinkedIn, **X (Twitter)** (v0.5.0+)
- **DB-driven platform config** (v0.5.0+): OAuth 2.0 app credentials stored AES-256-GCM encrypted in DB; `create_platform_config` MCP tool (Admin role); no environment variables required after initial setup
- **X OAuth 2.0 + PKCE** (v0.5.0+): `S256` code challenge; server-side verifier storage; 280-char body limit (terminal error, never truncated)
- OAuth credentials encrypted at rest (AES-256-GCM)
- `PublicationSchedule` — recurring weekly slots (weekday, HH:MM, IANA timezone) per credential; FIFO queue; catch-up policy on restart
- Layer 1 agent routing — `social.AddRoutes(app, forgesocial.OnPublish(...))` fires outbound HTTP on lifecycle signals; HMAC-signed payload; exponential backoff retry
- 15 MCP tools across PostModule, CredentialModule, ConfigModule, ScheduleModule
- Full CLI parity in smeldr.dev/cli v0.8.0
- **X media upload** (v0.6.0): images in `media_url` are fetched and uploaded to `api.x.com/2/media/upload` before tweeting; `media_ids` attached to tweet payload; requires `media.write` OAuth scope — existing X credentials must be re-authorised

## smeldr.dev/agent — Experimental

- `smeldr.dev/agent` — MIT-licensed agent runtime; `smeldr.dev/agent/flow` — AGPL-3.0 Forge integration adapter
- `AgentJob` — Forge content type (embeds `smeldr.Node`) with full lifecycle management: Draft → Published → Archived; auto-generated MCP tools (`create_agent_job`, `get_agent_job`, `list_agent_jobs`, `update_agent_job`, `publish_agent_job`, `archive_agent_job`, `delete_agent_job`)
- Signal-triggered jobs — any `smeldr.Signal` value as `Trigger`; `ContentTypeFilter` restricts to a named content type; full `smeldr.SignalEvent` serialised as JSON in the agent task string so the agent knows what content item fired it
- Cron-triggered jobs — 5-field cron expression as `Trigger`; scheduler rebuilds atomically on AgentJob publish/archive
- `WebhookURL` — when set, agent task prompt includes an instruction to POST output via `http_post`
- Guard: AgentJob lifecycle events never trigger other jobs (prevents self-activation loops)
- `Module.Register(*smeldr.App)` — wires MCP tools, subscribes to all 7 after-signals, starts the cron scheduler

## Audit trail — Stable

- `App.Audit(store AuditStore)` — opt-in; subscribes to `AfterPublish`, `AfterSchedule`, `AfterArchive`, `AfterDelete` via the signal bus
- `AuditRecord` — immutable entry: ID, Timestamp, Signal, ContentType, Slug, ActorID, ActorRole, PreviousState
- `NewAuditStore(db DB)` — default SQL implementation; timestamps stored as RFC3339 for SQLite compatibility
- `CreateAuditTable(db DB)` — DDL helper; creates `smeldr_audit_log` table
- `GET /_audit` — Editor-or-higher; returns JSON array; supports `from`, `to` (RFC3339), `type`, and `actor` query filters
- `forge-cli audit list [--from RFC3339] [--to RFC3339] [--type TYPE] [--actor ACTOR]` — table output, newest first (smeldr.dev/cli v0.9.1)

## Outbound webhooks — Stable

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
- `forge-cli webhook` subcommands for all operations (smeldr.dev/cli)

## Developer and AI-agent experience — Stable

- Typed MCP tools derived directly from content type — no manual schema definition
- Field semantics — AI agents understand what each field means without extra prompting
- Agents operate under the same access rules as humans — no special bypass
- Content negotiation — agents receive an AI-optimised format, not raw HTML
- Go codebase designed to be readable and extensible by AI agents
- `smeldr.Verb(Noun)` naming throughout — no abbreviations, no clever names
