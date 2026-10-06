# Smeldr — feature list

Complete list of what Smeldr generates and includes automatically.
Updated with every amendment that adds or changes a feature.
Last updated: v1.118.0 + smeldr.dev/mcp v1.46.0 + smeldr.dev/cli v0.18.0 + smeldr.dev/oauth v0.5.0 + smeldr.dev/social v0.10.5 + smeldr.dev/agent v0.9.2 + smeldr.dev/media v1.6.3 + smeldr.dev/core/pgx v0.2.0.

## Module stability

| Package | Version | Stability |
|---------|---------|-----------|
| `smeldr.dev/core` | v1.118.0 | Stable |
| `smeldr.dev/mcp` | v1.46.0 | Stable |
| `smeldr.dev/oauth` | v0.5.0 | Beta |
| `smeldr.dev/core/pgx` | v0.2.0 | Beta |
| `smeldr.dev/media` | v1.6.3 | Beta |
| `smeldr.dev/cli` | v0.18.0 | Beta |
| `smeldr.dev/social` | v0.10.5 | Experimental |
| `smeldr.dev/agent` | v0.9.2 | Experimental |

**Stable** — API will not break without a deprecation notice.  
**Beta** — Functional and tested; API may change in minor releases.  
**Experimental** — Working implementation; API is actively evolving.

Stability label changes require architect sign-off.  
Graduation criteria: Experimental → Beta requires API unchanged across three
consecutive minor releases with integration tests present. Beta → Stable requires
architect approval and a major version bump for any future breaking changes.
Labels are reviewed at every module minor or major version bump.

"API" for these criteria means the whole public contract, not only the exported Go
symbols compared tag to tag: it also covers the documented behavioural and wire
contracts (MCP tool names, parameters and response shapes; CLI commands, flags and
environment variables; HTTP parameters; database schema). A change to any of these
counts as an API change for graduation.

Last full label review: 2026-09-29, no label changes.

---

## Routes and feeds — Stable

- List route — `GET /{prefix}` returns all Published items as JSON or HTML
- Detail route — `GET /{prefix}/{slug}` returns a single Published item
- RSS feed — `GET /{prefix}/feed.xml` plus aggregate `GET /feed.xml`
- Sitemap entries — `GET /{prefix}/sitemap.xml` merged into `GET /sitemap.xml`; regenerated on every publish, update, and delete — never stale, no cron
- SEO meta tags — `<title>`, `<meta description>`, canonical URL in every `<head>`
- Per-path SEO overrides — `PageMetaStore` layer between item `Head()` and global defaults; managed via 4 Admin MCP tools; auto-applied to list pages when no `ListHeadFunc` is configured
- Open Graph tags — `og:title`, `og:image`, `og:type`, `article:published_time` and more
- Twitter Cards — `twitter:card`, `twitter:title`, `twitter:image`
- AI index formats — `/llms.txt` compact index, `/llms-full.txt` Markdown corpus, per-item `/aidoc` token-efficient endpoint
- Content negotiation — one URL returns HTML to browsers, JSON to APIs, and AI-optimised format to agents; no extra code

## Storage — Stable

- Database table derived from struct — works with PostgreSQL, SQLite, and MySQL
- SQLRepo — production SQL repository with automatic table naming and upserts
- SeqRepository (Beta) — lazy streaming interface for large datasets (`iter.Seq2[T, error]`)

## Block data foundation — Experimental

- DynamicNode — one generic content type for all block types, type-specific fields stored as JSON, discriminated by `type_name`; embeds `Node` for the standard lifecycle
- `NewDynamicContentRepo(db)` — `SQLRepo` bound to `smeldr_dynamic_content`
- ContentEdgeStore — composition edges (`smeldr_content_edges`); one table for page→block and collection→item, ordered by `sort_order`, batch-loaded via `ChildrenOf` (no N+1)
- `CreateBlockTables(db)` — single idempotent grouped table creator for the block schema; includes `idx_dynamic_content_type_status` (A151)
- `App.ServeBlocks(dir)` + `BlockRenderer.Render` — convention-template rendering engine (`templates/blocks/<type_name>.html`); batched load (no N+1), cycle protection, graceful degradation; renders only Published blocks
- Reference-field resolution — `{Name}ID` → `.{Name}` sub-object (`ImageID` → `.Image` on content_block/contact_card/hero); Published-only, `{{ with }}`-guarded, batched
- Block `Fields` use PascalCase keys (canonical convention; matches the block-system type tables)
- `ContentTypeSchema`, `SchemaField` — field-descriptor and schema types (A146); `SchemaField.Role` and `SchemaField.Relation` for semantic seams and relation placeholders (A151)
- `SchemaStore` with `FindByTypeName` and `All` — reads from `smeldr_content_type_schemas`
- `CreateSchemaTable(db)` — creates `smeldr_content_type_schemas` with `kind TEXT NOT NULL DEFAULT 'block'` column (A151); `MigrateSchemaKindColumn(db)` adds column to existing databases (idempotent)
- `SeedBlockTypeSchemas(db)` — seeds all 16 canonical block type schemas (A146)
- `ValidateFields(schema, fields)` — rejects unknown fields, missing required fields, type mismatches, bad URL formats, and duplicate role assignments; `ValidateBlockFields` alias retained (A151)
- `ContentTypeRegistry` + `TypeDescriptor` + `App.TypeRegistry()` — concurrency-safe name/prefix registry; dual key-space (PascalCase compiled + snake_case runtime); `Register`, `RegisterPrefix`, `Lookup`, `LookupByPrefix`, `All`; auto-populated at `App.Content()` time (A151)
- `ContentLister` interface — implemented by `Module[T]`; exposes `listPublished` as `TypeDescriptor.Fetch` for the ContentList block resolver (A152)
- ContentList block resolver — `content_list` block injects `.Items` (type-erased `[]map[string]any`) from the content-type registry at render time; `Limit`/`Page` block fields map to `ListOptions`; graceful skip for unknown type, nil Fetch, or empty ContentType; `ContentType` field stores `type_name` (e.g. `"recipe"`) not the URL prefix (A152/A154)
- `DynamicTypeRepo` — per-type CRUD repository for runtime-defined content types backed by `smeldr_dynamic_content`: `CreateDraft` (slug from title field, collision-safe; validates fields via `ValidateFields`), `GetBySlug`, `GetByID`, `List` (pagination, status filter, ordering), `UpdateFields` (PATCH semantics; validates patch via `ValidatePartialFields`), `SetStatus` (draft/published/archived; sets `published_at` on publish), `ScheduleContent` (state-flow-enforced scheduling) (A153, A202)
- `ValidatePartialFields(schema, patch)` — validates a partial `map[string]any` for the update path; unknown fields and type mismatches rejected; absent required fields not checked (A202)
- `App.DefineContentType(schema *ContentTypeSchema) (*TypeDescriptor, error)` — saves schema, registers `TypeDescriptor{Kind:"content"}`, and registers public routes at `schema.URLPrefix` when non-empty (A153/A154)
- `App.DynamicContentRepo(typeName string) (*DynamicTypeRepo, error)` — returns a typed CRUD repo for a registered runtime-defined content type (A153)
- `App.ServeDynamicContent() *App` — opt-in call that runs `MigrateURLPrefixColumn`, initialises the sitemap store, enables boot-time `loadDynamicTypes`, and registers 5 admin `/_content/{type}` endpoints (Editor+). Returns `*App` for chaining. Panics if `Config.DB` is nil. (A153/A154)
- `ContentTypeSchema.URLPrefix string` — operator-set public URL prefix; empty = admin-only; must start with `"/"` (A154)
- `MigrateURLPrefixColumn(db DB) error` — idempotent column migration for `url_prefix`; works on SQLite and Postgres (A154, A409)
- `PluralSnake(name string) string` — English plural helper (consonant+y→-ies rule) (A153)
- `ValidateSchemaDef(schema *ContentTypeSchema) error` — validates TypeName, URLPrefix format, field types, and roles (A153/A154)
- Admin content API (`/_content/{type}`) — 5 endpoints (Editor+): `POST` create draft, `GET` list all statuses, `GET /{id}` get by ID, `PATCH /{id}` update fields, `POST /{id}/status` set status; `POST /_content/types` (Admin) defines a type (A153/A154)
- Sitemap auto-rebuild — `SetStatus` triggers a background goroutine that writes `{URLPrefix}/sitemap.xml` to the in-memory sitemap store after each status change (A154)
- Still building on top: CLI block commands (c6)

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
- Struct-tag validation — `smeldr:"required,min=3"` enforced identically for HTTP, API, and MCP calls
- Token management — named revocable tokens with role scoping; `ensureBootstrap` auto-creates the first admin token on first start
- ErrLastAdmin guard — cannot revoke the last admin token
- Time-boxed grants — `RoleGrant.ExpiresAt *time.Time` (v1.99.0); an expired grant is automatically excluded from authorization checks, no manual revoke needed
- Grant provenance — `RoleGrant.Grantor` records which token created a grant (v1.101.0)
- `RoleStore.GetRole(ctx, name)` — read-only lookup of a role's full definition (operations, scope mode) (v1.100.0)
- Admin's own operation set was narrowed to remove `review`/`approve` (v1.98.0, security hardening) — Admin no longer implicitly holds every operation that exists; explicit roles are required for review/approval workflows
- The role, scope, tool-policy and audit model behind all of this is described under "Governance and delegation" below

## Governance and delegation — Experimental

### Baseline model — RoleStore

Opt-in: an app that never calls `App.Governance(store)` sees no governance behaviour at all.

- `App.Governance(store *RoleStore) error` — wires the store: creates the governance tables, seeds default roles and tool policies, migrates existing token role strings into grants, and always wires the audit trail alongside it (D44 — there is no governance-enabled instance without an audit log). `store` must share the app's own `Config.DB`. Accessors `App.RoleStore()` and `App.GovernanceAuditStore()`
- Tables — `smeldr_roles`, `smeldr_role_grants`, `smeldr_tool_policies`, `smeldr_governance_audit`
- `RoleDefinition` — `Name`, `Operations` (full-word operation list), `ScopeMode`, `ScopeRelationKind`, `ScopeDirection`, `TrustLevel` (0 direct, 2 plan required; 1 is rejected as not yet defined), `AllowSelfApproval` (meaningful only at trust level 2)
- `RoleStore.DefineRole` — upsert by name; a redefinition updates operations, scope, trust level and self-approval, and keeps the row's ID
- Seeded roles — `author` (create, read, update, publish, archive), `editor` (author plus delete, manage), `admin` (editor plus administer, define-type, define-flow, define-relation-kind; no review/approve since v1.98.0)
- `RoleGrant` and `RoleStore.Grant`/`Revoke`/`ListGrants` — binds a token to a role with concrete scope data. `Grant` is idempotent on token, role, anchor and static scope list, so repeating it returns the existing grant ID
- Three scope modes (`ScopeMode`) — `ScopeGlobal` (every item), `ScopeStatic` (explicit `type:id` or `type:*` patterns), `ScopeDynamic` (items one hop from an anchor item via a named relation kind and direction; only asserted, non-invalidated edges count)
- `RoleStore.Authorized(ctx, tokenID, op, AuthTarget)` — whether the token holds any unexpired grant whose role includes the operation and whose scope covers the target. Fails closed on a query error; a transient dynamic-scope error does not abort the remaining grants and surfaces only if none authorizes. Operations with no target (e.g. `administer`) need a global grant
- `RoleStore.RoleGranted(ctx, tokenID, roleName, target)` — the same scope logic keyed by exact role name instead of operation word, for named-role gates on custom transitions
- `AuthTarget` — `TypeName`, `ID` (used for matching), `Slug` (display and logging only)
- Tool policies — `smeldr_tool_policies` maps each built-in MCP tool to the operation word that gates it, seeded idempotently on every boot; `RoleStore.ToolPolicy(ctx, toolName)` is the seam smeldr.dev/mcp uses to resolve a tool's required operation before calling `Authorized`
- Governance audit: `RoleStore.WithAudit(actorTokenID, log)` returns a store that records every `DefineRole`, `Grant` and `Revoke` as a `GovernanceAuditRecord` (actor, action, target, before and after JSON) via a `GovernanceAuditStore`; `NewGovernanceAuditStore`/`CreateGovernanceAuditTable` give the SQL-backed one. `DefineRole`, `Grant` and `Revoke` are atomic with their audit record when the audit store is the SQL one and the DB supports transactions (`Grant`/`Revoke` since A233, `DefineRole` since v1.105.2); otherwise (a non-SQL audit store or no transaction support) an audit error means the change may already have applied
- Stewardship reads — `RoleStore.StewardedRuleTypes` and `RoleStore.StewardshipInbox` return the rule types a token stewards and the Decisions, Rules and authority stubs touching them

### Delegation and stewardship additions (since v1.89.3)

- Item-scoped delegation — `RoleStore.GetGrant`, `RoleStore.ListRoles` (v1.101.0); built-in
  `item-approver`/`item-reviewer` roles via `RegisterItemApproverRole`/`RegisterItemReviewerRole`
  (v1.101.0), scoped to a single item rather than global
- `delegate_item` MCP tool (smeldr.dev/mcp v1.41.0) — a token delegates a role/operation
  subset it already holds to another token, scoped to one item, expiring after 1-90 days
  (default 14); the caller must itself hold every operation the delegated role carries, and
  only a `ScopeStatic` role qualifies
- `withdraw_delegation` MCP tool (v1.42.0) — the delegator revokes their own delegation
  before it expires
- `list_roles` MCP tool (v1.42.0) — lists every role defined on the instance: name,
  operations, scope shape
- Domain-scoped Decision stewardship — `RegisterDecisionDomainAdminRole`,
  `RegisterDecisionStewardRole` (v1.94.0) define roles that hold ratify/supersede authority
  over Decisions within one Domain, rather than instance-wide
- `lookup_token_names` MCP tool (v1.43.0) + `TokenStore.NamesForUserIDs(ctx, userIDs)`
  (v1.104.0) — batch-resolve actor IDs seen in `last_actor`/`RoleGrant.Grantor`/relation
  `created_by` back to their human-readable token names
- Transition provenance - with `App.Provenance` wired, every successful state change through
  `App.TransitionItemVia` (and `TransitionItem`/`TransitionItemWithReason`, which delegate to it),
  `DynamicTypeRepo.SetStatus`/`SetStatusWithReason`/`ScheduleContent` and the
  `POST /_content/{type}/{id}/status` endpoint writes a `ProvenanceRecord` (verb transition, from and
  to state, actor, actor kind, surface, reason) (v1.109.0). `TransitionItemVia` names the entry point;
  the older methods record an empty surface (the `transition_item` MCP tool passes `mcp` from
  smeldr.dev/mcp v1.44.0). Synchronous and fail-open, nothing recorded for a rejected
  transition, earlier transitions are not backfilled. Since v1.110.0 the same record is written for
  the item a ConflictSupersede transition supersedes (actor of the triggering caller, reason naming
  the winner; no event fires for it) and for each Signal expired by `App.ExpireSignals` (job
  `signal-expiry-sweep`, surface trigger). `CreateProvenanceTable` also indexes
  (subject_type, subject_id)
- Explicit standing (D100) - a flow `State` may declare `Standing: StandingHolds` (only legal tag;
  `App.RegisterFlow` rejects others); each item stores standing in `smeldr_standing` (`holds`,
  `ceased`, `none`, `not recorded`), written only by state-change code: entering a holding state
  stores `holds`, leaving one for an untagged state stores `ceased`, untagged to untagged changes
  nothing; no row means `none`; every standing change is `standing-began`/`standing-ended` provenance
  with the transition's actor; read via `ItemStanding`/`CountStanding` and the `standing` field of
  the context packet; `MigrateStanding` gives pre-existing items standing once per type from the
  flow graph without guessing; `App.CheckStandingDrift` reports (never repairs) stored standing not
  matching its state's tag; Smeldr's tags: Decision `ratified` and `pending-re-evaluation`,
  Amendment `merged`; Signal, Task and Goal have none (v1.112.0); for a page of items use `TypeHasStanding` then `ItemStandings` (v1.114.0): one query per 400 ids, `none` for an item with no row, empty map for a type without standing
- Actor provenance — every orchestration item (`Signal`, `Task`, `Decision`, `Amendment`,
  `Goal`, `Run`) and `DynamicNode` gained a `LastActor` field (v1.95.0), stamped at creation
  time too, not only on transition (v1.97.0); `RelationEdge` gained `CreatedBy` (v1.97.0)
  and MCP responses for `get_relations`/`assert_relation`/`propose_relation`/
  `observe_relation` now include it (smeldr.dev/mcp v1.42.1)

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

Cross-cutting tool behaviour (smeldr.dev/mcp v1.39.0-v1.40.0):

- Pagination — `list_tasks`/`list_amendments`/`list_decisions`/`list_goals`/`list_runs`/`list_signals` take `limit`/`offset`, default 50 capped at 500 (previously unbounded); response includes `total` (the real pre-`limit` count) alongside `items`/`signals`
- Unknown-parameter rejection — every `create_*`/`update_*` tool now returns `-32602` for a key not in its own declared schema, instead of silently succeeding with the field left empty
- `tools/list` is filtered to the calling token's own role — a caller no longer sees a tool it could never actually call (curation only; `tools/call` enforcement is unchanged)
- `mcp.WithSchemaTools(db)` — wires `get_content_type_schema`/`list_content_type_schemas` alone, without the rest of `WithBlocks`'s tool surface

Block system tools (Experimental, T32 — enabled with `mcp.WithBlocks()`):

- `create_node`, `update_node`, `get_node`, `list_nodes`, `publish_node`, `archive_node` — Author+; generic block lifecycle, addressed by ID. `publish_node`/`archive_node` actually run a block type's own registered state flow when one is defined via `define_state_flow` (v1.36.2 — previously silently bypassed it)
- `add_section`, `reorder_sections`, `remove_section` — Editor+; compose page sections
- `add_item`, `reorder_items`, `remove_item` — Editor+; compose collection items

Admin tools (require Admin role):

- `create_preview_url` — generates a signed draft preview URL for a Draft or Scheduled item
- `create_upload_token` — generates a short-lived upload token for `POST /media` (Author+)
- `create_webhook`, `list_webhooks`, `delete_webhook` — manage outbound endpoints
- `list_webhook_deliveries`, `retry_webhook` — delivery introspection and retry
- `create_token`, `list_tokens`, `revoke_token` — token management

OAuth 2.1 for remote MCP servers (smeldr.dev/oauth v0.1.2):

- `mcp.WithOAuth(*oauth.Server)` — enables OAuth 2.1 on the MCP server; all HTTP endpoints require Bearer
- `GET /.well-known/oauth-protected-resource` — RFC 9728 protected resource metadata
- `GET /.well-known/oauth-authorization-server` — RFC 8414 authorization server metadata (served by smeldr.dev/oauth)
- PKCE S256 mandatory, CIMD stateless client validation, `offline_access` scope for refresh tokens
- Scope mapping: `mcp` → Author role, `mcp:admin` → Admin role
- SQLite-backed token store (`oauth.NewSQLiteStore`)

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
- DB-backed redirect management — `App.Redirects(db)` activates `CreateRedirectsTable` (auto-ensure), loads saved entries, and enables runtime management via MCP tools (`create_redirect`, `list_redirects`, `delete_redirect`, Editor role) and CLI (`smeldr-cli redirect list/create/delete`); changes take effect immediately without restart
- Content statistics endpoint — `App.StatsHandler()` mounts `GET /_stats` (Admin role); returns per-content-type item counts per status (`draft`/`published`/`scheduled`/`archived`). External modules contribute via `StatsExtProvider` interface registered with `App.RegisterStatsProvider`
- Log capture endpoint — `App.CaptureLogs(opts...)` installs a teeing `slog.Handler` (preserves stderr) into a bounded in-memory ring; `GET /_logs` (Admin) serves recent records over plain HTTP — works when MCP is down. Options `WithLogCapacity` (default 500), `WithLogLevel` (default WARN); envelope `{capacity,count,dropped,entries}` newest-first; query `level`/`limit`/`since`; route absent → 404 when not enabled. Live-debugging only (in-memory, lost on restart)
- Security headers — CSP, HSTS, X-Frame-Options, Referrer-Policy in one middleware call
- Graceful shutdown — drains in-flight requests on SIGINT/SIGTERM
- Signal bus — `app.OnSignal(Signal, handler)` registers subscribers for `AfterPublish`, `AfterSchedule`, `AfterArchive`, `AfterDelete`; `SignalEvent` carries Type, Slug, Title, URL, Timestamp, PreviousState, ActorRole, ActorID; handlers run synchronously in the publish goroutine and must enqueue-and-return
- Config-driven feature-toggle layer — `example/server/main.go` is a deployable reference binary with no hard-coded Go content types; all optional subsystems (governance, relations, dynamic content, blocks, media, social, tokens, webhooks, redirects, page meta, agents, OAuth) are gated by `ENABLE_*` environment variables; binary compiles and runs with only `SECRET` set; basis for T114 dogfood instance and T118 downloadable binary (A197)

## smeldr.dev/media — Beta

- Upload, serve, list, and delete files via HTTP and MCP
- Alt text enforced on image uploads (WCAG 1.1.1)
- `os.Root` path traversal protection (Go 1.24+)
- Configurable upload directory and max file size
- AVIF support — `image/avif` accepted and magic-byte detected alongside JPEG, PNG, WebP, GIF
- Upload token — `Authorization: UploadToken <token>` accepted on `POST /media`; image-only MIME whitelist for token uploads; Bearer-token uploads unaffected
- Hex filename prefix — stored as `<32-hex>-<sanitized>` preventing collisions without exposing upload timing

## smeldr.dev/cli — Beta

- `smeldr-cli init` — bootstrap a new instance from the terminal
- Content CRUD — create, update, publish, unpublish, archive, delete, list, get
- Token management — create, list, revoke (Admin role required)
- Media operations — upload, list, delete
- Webhook management — create, list, delete, view deliveries, retry
- Draft preview — `forge preview <prefix> <slug>` prints a signed preview URL (Admin role required)
- Social commands (v0.7.0): `social credential create/list`, `social post create/list/get/publish/archive/delete`, `social post queue`, `social schedule create/show/pause/resume/delete`
- Social commands (v0.8.0): `social credential get/delete`, `social platform configure` (DB-driven OAuth app config for mastodon/linkedin/x); `social credential create` now accepts `--platform x`
- Block commands (v0.10.0, T32): `block node create/update/get/list/publish/archive`, `block section`/`block item` `add/reorder/remove` — full CLI/MCP parity with the block tools; `node list` table output (`--json` escape); PascalCase `--field`

## smeldr.dev/social — Experimental

- `smeldr.dev/social` — social post scheduling and AI agent routing
- Two scheduling models: explicit `scheduled_at` (Model 1) and slot-queue via `PublicationSchedule` (Model 2, v0.4.0+)
- Platforms: Mastodon, LinkedIn, **X (Twitter)** (v0.5.0+)
- **DB-driven platform config** (v0.5.0+): OAuth 2.0 app credentials stored AES-256-GCM encrypted in DB; `create_platform_config` MCP tool (Admin role); no environment variables required after initial setup
- **X OAuth 2.0 + PKCE** (v0.5.0+): `S256` code challenge; server-side verifier storage; 280-char body limit (terminal error, never truncated)
- OAuth credentials encrypted at rest (AES-256-GCM)
- `PublicationSchedule` — recurring weekly slots (weekday, HH:MM, IANA timezone) per credential; FIFO queue; catch-up policy on restart
- Layer 1 agent routing — `social.AddRoutes(app, social.OnPublish(...))` fires outbound HTTP on lifecycle signals; HMAC-signed payload; exponential backoff retry
- 15 MCP tools across PostModule, CredentialModule, ConfigModule, ScheduleModule
- Full CLI parity in smeldr.dev/cli v0.8.0
- **X media upload** (v0.6.0): images in `media_url` are fetched and uploaded to `api.x.com/2/media/upload` before tweeting; `media_ids` attached to tweet payload; requires `media.write` OAuth scope — existing X credentials must be re-authorised

## smeldr.dev/agent — Experimental

- `smeldr.dev/agent` — MIT-licensed agent runtime; `smeldr.dev/agent/flow` — AGPL-3.0 Smeldr integration adapter
- `AgentJob` — Smeldr content type (embeds `smeldr.Node`) with full lifecycle management: Draft → Published → Archived; auto-generated MCP tools (`create_agent_job`, `get_agent_job`, `list_agent_jobs`, `update_agent_job`, `publish_agent_job`, `archive_agent_job`, `delete_agent_job`)
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
- `smeldr-cli audit list [--from RFC3339] [--to RFC3339] [--type TYPE] [--actor ACTOR]` — table output, newest first (smeldr.dev/cli v0.9.1)

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
- `smeldr-cli webhook` subcommands for all operations (smeldr.dev/cli)

## Developer and AI-agent experience — Stable

- Typed MCP tools derived directly from content type — no manual schema definition
- Field semantics — AI agents understand what each field means without extra prompting
- Agents operate under the same access rules as humans — no special bypass
- Content negotiation — agents receive an AI-optimised format, not raw HTML
- Go codebase designed to be readable and extensible by AI agents
- `smeldr.Verb(Noun)` naming throughout — no abbreviations, no clever names

## State flows — Stable

- `App.RegisterFlow(StateFlow) error` — registers or updates a custom state machine for a content type; idempotent; persists to `smeldr_state_flows`/`smeldr_flow_states`/`smeldr_flow_transitions`
- `StateFlow` struct — `Name`, `TypeName`, `States`, `Transitions`, `ActiveState`, `ConflictPolicy`
- `State` struct — `Name`, `IsInitial`, `IsTerminal`, `SuppressesSignals`
- `Transition` struct — `From`, `To`, `RequiredRole`
- `ConflictPolicy` type — opt-in uniqueness enforcement at a designated `ActiveState` (A186)
  - `ConflictReject` (`"reject"`) — returns `ErrConflict` (409) when another item is already in `ActiveState`
  - `ConflictSupersede` (`"supersede"`) — transitions the other items in `ActiveState` to `"superseded"` when an item enters it, the entering item's own write first (v1.112.1; atomic for `TransitionItemVia` and dynamic types). Since v1.111.0 it also asserts a `supersedes` edge winner -> loser (by ID, triggering actor as `created_by`) when `App.Relations` is wired and a `supersedes` kind permitting the type's pair is registered; transitions into the active state are serialised per type within the process (v1.115.1)
  - Both policies fail-open: DB errors return nil and never block a transition
- RegisterFlow now updates all state flags on existing state rows (v1.113.0), clearing `is_initial` on rows no longer initial; previously rows stayed frozen at insert. (A399)
- State flow enforced automatically in `MCPPublish`, `MCPSchedule`, `MCPArchive`, and `DynamicTypeRepo.SetStatus`; since v1.116.0 the conflict policy also applies on HTTP PUT status changes, the scheduler publish, `DrainEvalQueue` and `DynamicTypeRepo.ScheduleContent`
- State flows are enforced on SQLite and Postgres alike since v1.118.0 (D103, A409): a Postgres deployment through `core/pgx` used to store a flow and never consult it. `ConflictReject` is exclusive within one process, not across processes
- `EnsureColumn` and the boot migrations work on SQLite and Postgres since v1.118.0 (A409)
- `define_state_flow` MCP tool — registers a flow including `active_state` and `conflict_policy` params (Admin role)
- `TransitionTrigger` struct — `FromState`, `ToState`, `TriggerClass`, `TriggerType`, `Config`; declared in `StateFlow.Triggers`
- `StateFlow.Triggers []TransitionTrigger` — async trigger handlers persisted to `smeldr_transition_triggers` by `RegisterFlow`; idempotent
- `schedule-eval` trigger type — on transition, reads `eval_field` from item row and inserts into `smeldr_eval_queue` for later drain
- `smeldr_eval_queue` table — queues timed state transitions; `UNIQUE(type_name, item_id, to_state)` prevents duplicate entries
- `App.DrainEvalQueue(ctx) (triggered, skipped int, err error)` — drains due rows: UPDATE item status, DELETE queue row; fail-open on nil DB and missing table (A187); each triggered transition also records a `Finding` when a `FindingStore` is configured (v1.91.1)
- `orchDecisionFlow` wired with two `schedule-eval` triggers on `proposed→ratified` and `pending-re-evaluation→ratified`
- `App.ExpireSignals(ctx, SignalExpiryConfig)` (v1.103.0) — scheduled detector moving stale `pending`/`read` Signals to `expired` (never deleted) after `MaxAge` (default 14 days); three `signal_type` values (`authorization-required`, `review-requested`, `conflict-detected`) are always excluded, since they represent a standing condition that must be answered, not aged out; `ExcludeTypes` adds further exclusions without replacing the mandatory three

## Orchestration types — Experimental

- `Signal` content type — protocol message between pilots and the architect (signal-protocol flow: pending -> read -> acknowledged/expired). `create_signal`'s `receiver` is optional (smeldr.dev/mcp v1.38.0) — omitting it broadcasts to every subscriber; `subject_type`/`subject_id` (v1.40.1) let any Signal point at a specific item; `list_signals` gained `sender` and `limit` filters (v1.37.0), querying signals sent as well as received
- `Task` content type — work item in the architect/pilot state machine (agent-task flow: backlog -> active -> ... -> done/deferred/resolved). `backlog` also exits directly to `deferred`/`resolved` (v1.95.1 — a Task can turn out unnecessary before anyone claims it). `active` also exits directly to `done` with a required reason (v1.105.0) — for work that concludes without a plan/commit cycle at all (a review, sign-off, investigation, or design discussion), distinct from `resolved` (the need was met by work outside this Task). `commit-reviewing` can return to `implementing` with a required reason (v1.108.0), a reviewer sends the Task back to the implementer after a failed review; `implementing` can go to `blocked` with a required reason, the implementer stops mid-build to ask rather than guess, and `blocked` resumes to `implementing` (a block raised mid-build resumes there, not in `active`, which would re-enter planning); the existing `blocked` to `active` exit is unchanged
- `Decision` content type — ratified architectural decision with re-evaluation cycle (governance-decision flow: proposed -> ratified -> ... -> superseded/archived). Gained a short display `Title` field independent of the Markdown `Body` (v1.93.0), and `TensionRuleID`/`TensionReason` fields recording declared tension against a Rule (v1.91.0)
- `Amendment` content type — committed changeset linking a Task to its code implementation (amendment-lifecycle flow: scoped -> in-progress -> commit-ready -> committed -> merged/rejected)
- `Goal` content type — work goal with priority, band, and size; linked to Decisions and Tasks via the relation graph (goal-lifecycle flow: open -> in-progress -> done/resolved, open <-> parked, parked -> resolved)
- `Run` content type (D38, M3) — one mechanical episode of headless automated work, from the moment a listener claims it to the moment it merges or is abandoned. Fields: `TaskID`, `Repo`, `Machine`, `Branch`, `WorktreePath`, `BaseSHA`, `LeaseHolder`, `Outcome`, `Cleanup`, `AcknowledgedAt`, `LastActor`. Unlike the other five types it registers no state flow: its authoritative state is `LeaseHolder` plus `Outcome`, guarded by `SQLRepo.Save`'s rev-CAS, and `Node.Status` stays Draft for the whole life of the row. `RunOutcome` values `merged`, `needs-resync`, `stuck`, `failed`, `orphaned` (empty while in flight); `RunCleanupState` values `pending`/`done`. Lease expiry is computed by the caller (`UpdatedAt` plus a TTL), not stored. Every lease-touching write must echo the `Rev` it last read, or the CAS degrades to last-write-wins
- `CreateOrchestrationTables(db DB) error` — creates all six DB tables (`smeldr_signals`, `smeldr_tasks`, `smeldr_decisions`, `smeldr_amendments`, `smeldr_goals`, `smeldr_runs`)
- `RegisterOrchestrationTypes(app *App, db DB)` — registers all six types with MCP(MCPRead, MCPWrite); five of them (all but `Run`) also get state flows; fail-open on nil DB
- All six types embed `Node` and receive full MCP tool generation (create, get, list, update, publish, archive, delete)
- `QueryGoalContext(ctx, db, rs, goalID)` — assembles `GoalContext` (goal + linked Decisions, Tasks, Goals) via bidirectional relation-graph query; fail-open on nil RelationStore
- `Severity` type + `SeverityOf(ctx, db, rs, anchorType, anchorID, ruleType, kind, direction, maxDepth)` (v1.91.0) — computes an ordinal blast-radius severity for a Rule-anchored graph walk, surfaced once a threshold is crossed
- New relation kinds `belongs_to_domain`, `belongs_to_area` (Decision → Domain/Area), and `in_set` (v1.92.0) — model domain/area/set membership on the relation graph
- `RelationStore.RegisterReferenceType(ctx, typeName)` (v1.102.0) — marks a dynamic content type as reference/lookup data, so `App.SweepStructural` treats any non-archived row (not only `published`) as a live target, since reference data (e.g. Domain, Area) is typically never explicitly published
- Automatic `conflict-detected` Signal (v1.96.0) — fires whenever a `contradicts` relation edge is asserted/proposed/observed between two Decisions, notifying each Decision's steward
- `Finding`/`FindingStore` (v1.90.0) — a structured, deduplicated record of a detected structural or governance condition (`NewFindingStore`, `CreateFindingTable`, `FindingStore.Record`, `App.Findings`); wired into `App.SweepStructural`'s stale-edge detection and `App.DrainEvalQueue`'s scheduled re-evaluations
- `get_goal_context` MCP tool (smeldr.dev/mcp v1.27.0) — agent-callable query returning `{goal, linked_decisions, linked_tasks, linked_goals}` for a given GoalID
