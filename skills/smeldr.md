# Smeldr — developer skill

Smeldr is a Go content framework. This skill covers what you need to work
with Smeldr as a developer or pilot agent.

Current versions: smeldr.dev/core v1.53.0 · smeldr.dev/mcp v1.27.0 · smeldr.dev/oauth v0.3.0 · smeldr.dev/media v1.6.0 · smeldr.dev/cli v0.15.1 · smeldr.dev/social v0.9.2 · smeldr.dev/agent v0.7.1 · smeldr.dev/core/pgx v0.1.2

---

## Intellectual foundation

Smeldr has a constitution and vocabulary layer in `smeldr/architect/constitution/`.
Read `constitution.md`, `vocabulary.md`, and `cloud-strategy.md` when making
architecture decisions or designing new MCP or developer-facing surfaces.

**Two vocabularies — keep them on separate surfaces.**

| Internal (constitutional) | External (market / developer-facing) |
|---------------------------|---------------------------------------|
| Managed Item, Authority, Lineage, Evidence, Participant, Operational platform | content, permissions, history, AI-native content backend |

- Constitutional vocabulary belongs in `constitution/`, design docs, DECISIONS.md, and NEXT.md.
- Market vocabulary belongs in README, smeldr.dev copy, MCP tool descriptions, and public docs.
- The skill and public docs are a bridge surface — convergence is deliberate, not accidental.

**Publication gate:** no external surface adopts constitutional vocabulary until a second
domain ships. Until then, lead externally with "AI-native content backend".

**Concept-to-code mapping:** `vocabulary.md` is the authoritative binding
(e.g. Managed Item → `Node` / `DynamicNode`). The code keeps its earned names.

---

## Core concepts

```
Node      — base struct every content type embeds (ID, Slug, Status, timestamps)
Module    — one content type, fully wired (routes, repo, MCP, signals)
Signal    — hook that fires on lifecycle changes (AfterPublish, AfterArchive, ...)
Head      — all page metadata (SEO, OG, JSON-LD, canonical)
Role      — Guest < Author < Editor < Admin
Config    — smeldr.config file + Go struct; Go-code values win
```

Content lifecycle: `Draft → Published/Scheduled → Archived`
Archived items are permanently invisible — cannot be reverted to Draft.

---

## Defining a content type

```go
type Story struct {
    smeldr.Node
    Title   string `smeldr:"required" json:"title"`
    Body    string `smeldr:"required,min=50" json:"body"`
    Image   string `smeldr:"" smeldr_description:"Hero image path." db:"image" json:"image"`
    OGImage string `smeldr:"" smeldr_description:"OG image URL." db:"og_image" json:"og_image"`
}
```

**json tag is required on every custom field** — all fields beyond `smeldr.Node`.
Without an explicit `json:"snake_case"` tag, Go serialises the field as PascalCase,
which breaks MCP read and write operations (MCP uses snake_case keys).
`smeldr.Node` fields are exempt — they are handled internally.

Wrong (MCP returns empty/missing values):
```go
type MyPage struct {
    smeldr.Node
    Title string `db:"title"`   // ← missing json tag — MCP cannot map "title" → Title
    Body  string `db:"body"`
}
```

Correct:
```go
type MyPage struct {
    smeldr.Node
    Title string `db:"title" json:"title"`
    Body  string `db:"body"  json:"body"`
}
```

---

## Wiring a module

```go
app.Content(smeldr.NewModule((*Story)(nil),
    smeldr.At("/solved"),
    smeldr.Table("stories"),       // override incorrect pluralisation
    smeldr.Repo(smeldr.NewSQLRepo[*Story](db)),
    smeldr.MCP(smeldr.MCPRead, smeldr.MCPWrite),
))
```

---

## Routing variants (v1.23.0+)

### SingleInstance — singleton page at module prefix

Use when a module holds exactly one canonical item (About, Contact, Terms):

```go
smeldr.NewModule((*AboutPage)(nil),
    smeldr.At("/about"),
    smeldr.Repo(repo),
    smeldr.SingleInstance(),
    smeldr.MCP(smeldr.MCPRead, smeldr.MCPWrite),
)
// GET /about → serves first Published item
// GET /about/{slug} → 404 (not registered)
```

MCP behaviour:
- `list_{type}s` tool suppressed (`MCPMeta.SingleInstance = true`)
- `get_{type}`, `update_{type}`, `publish_{type}`, `archive_{type}`, `delete_{type}` present
- `create_preview_url` returns `/{prefix}?preview=<token>` — no slug in path (forge-mcp ≥ v1.10.2)

**Pattern: SingleInstance + custom public handler**
When the public URL differs from the module prefix (e.g. homepage at `/`, module at `/homepage`):

```go
// Module at /homepage — admin + MCP surface only
app.Content(smeldr.NewModule((*HomePage)(nil),
    smeldr.Repo(homePageRepo),
    smeldr.At("/homepage"),
    smeldr.SingleInstance(),
    smeldr.MCP(smeldr.MCPRead, smeldr.MCPWrite),
))

// Public route — custom handler reads the published record
app.Handle("GET /", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    hps, _ := homePageRepo.FindAll(r.Context(), smeldr.ListOptions{
        Status: []smeldr.Status{smeldr.Published},
    })
    hp := homePageDefaults()
    for _, p := range hps {
        if p.Slug == "home" { hp = p; break }
    }
    // render hp ...
}))
```

When to use: exactly one record; no list page; managed via MCP/CLI; public URL may differ.
When NOT to use: multiple records; module prefix IS the public URL; per-item slug URLs needed.

### Standalone — top-level /{slug} routing

Use when items should appear at `/{slug}` rather than `/{prefix}/{slug}`:

```go
smeldr.NewModule((*Post)(nil),
    smeldr.At("/posts"),
    smeldr.Repo(repo),
    smeldr.Standalone(),
)
// GET /my-post     → serves Published Post with slug "my-post"
// GET /posts       → list of Published posts (unchanged)
// GET /posts/my-post → 404 (not registered)
```

Multiple Standalone modules coexist — slug dispatch is first-match.
AIDoc served at `GET /{slug}/aidoc` when `smeldr.AIIndex(smeldr.AIDoc)` is also set.

### APIOnly — no public HTML surface (v1.24.0+)

Use when a content type is managed exclusively via MCP or CLI with no public web presence:

```go
smeldr.NewModule((*HomePage)(nil),
    smeldr.At("/home-pages"),
    smeldr.Repo(repo),
    smeldr.MCP(smeldr.MCPWrite),
    smeldr.APIOnly(),
)
// GET /home-pages Accept:application/json → 200 JSON
// GET /home-pages Accept:text/html        → 404
// MCP tools: full set (list, get, create, update, publish, etc.)
```

`APIOnly()` + `SingleInstance()` panics at startup — incompatible combination.

### SiteConfig — built-in singleton (v1.29.0+)

`SiteConfig` is a built-in singleton content type for site-wide defaults configurable
via MCP after first deploy. Fields: `site_name`, `title_separator`, `og_image`,
`x_handle`, `head_script`. Registration:

```go
smeldr.CreateSiteConfigTable(db)          // once at startup
app.Content(smeldr.NewSiteConfigModule(db)) // registers MCP tools
```

Configure via `create_site_config` MCP tool (Admin role). No code redeployment needed.

### HeadAssets.RawHead — verbatim head HTML (v1.30.0+)

`HeadAssets` accepts a `RawHead template.HTML` field injected verbatim into `<head>`
after all other HeadAssets output (preconnect → stylesheets → links → scripts → RawHead).
Use for analytics snippets, preload hints, or custom head HTML that does not fit the
structured fields. Zero value is a no-op.

```go
app.SEO(&smeldr.HeadAssets{
    Scripts: []smeldr.ScriptTag{{Src: "/static/app.js", Defer: true}},
    RawHead: template.HTML(`<link rel="preload" href="/fonts/inter.woff2" as="font" crossorigin>`),
})
```

---

## Signal bus (v1.20.0+)

`app.OnSignal` registers a subscriber for a lifecycle signal. Handler contract:
enqueue work and return immediately — never block. Errors are logged and never
propagated to the publish caller.

```go
app.OnSignal(smeldr.AfterPublish, func(ctx context.Context, ev smeldr.SignalEvent) error {
    // ctx is detached from the request (WithoutCancel) — safe to enqueue async work
    return myQueue.Enqueue(ctx, ev)
})
```

Signal constants: `AfterPublish`, `AfterSchedule`, `AfterArchive`, `AfterDelete`

`SignalEvent` fields: `Type`, `Slug`, `Title`, `URL`, `Timestamp`, `PreviousState`, `ActorRole`, `ActorID`

`PreviousState` and `ActorRole` are transient — not reconstructable via MCP after the fact.

**Built-in audit trail (v1.22.0+):** `app.Audit(smeldr.NewAuditStore(db))` subscribes to
`AfterPublish`, `AfterSchedule`, `AfterArchive`, `AfterDelete` and persists each transition.
`GET /_audit` (Editor+) returns JSON records. `smeldr-cli audit list` prints a table.

---

## Log capture (v1.36.0+)

Opt-in, in-memory capture of recent log records for live debugging — works over
plain HTTP even when MCP is down. `app.CaptureLogs(...)` installs a teeing
`slog.Handler`: records still reach the existing handler (stderr) AND records
at/above the capture level are stored in a bounded ring.

```go
app.CaptureLogs() // ring of 500, WARN+ (defaults)
// app.CaptureLogs(smeldr.WithLogCapacity(1000), smeldr.WithLogLevel(slog.LevelInfo))
```

`GET /_logs` (Admin, bearer) returns `{capacity, count, dropped, entries}` with
entries newest-first. Query params: `level` (min level, inclusive), `limit` (most
recent N), `since` (RFC3339). Route is absent (404) unless `CaptureLogs` was called.

- **Not log storage** — in-memory, lost on restart; stderr stays the durable path.
- **Ordering** — call `CaptureLogs` AFTER any app-side `slog.SetDefault`.
- **Zero-config** — when no custom handler is set, capture forwards to a stderr text
  handler (wrapping slog's built-in handler would deadlock via the log package).
- **No MCP tool** by design — use `smeldr-cli logs` (calls `/_logs` over HTTP).

---

## Custom state flows (v1.44.0+)

Call `app.RegisterFlow(smeldr.StateFlow{...})` at startup (after `smeldr.New`) to
register a data-driven state machine for a content type. Idempotent — safe to call
on every restart.

```go
err := app.RegisterFlow(smeldr.StateFlow{
    Name:     "agent-job",
    TypeName: "AgentJob",  // Go type name — maps to "agent_jobs" table
    States: []smeldr.State{
        {Name: "draft",     IsInitial: true},
        {Name: "published"},
        {Name: "paused",    SuppressesSignals: true},
        {Name: "archived",  IsTerminal: true},
    },
    Transitions: []smeldr.Transition{
        {From: "draft",     To: "published"},
        {From: "published", To: "paused"},
        {From: "paused",    To: "published"},
        {From: "published", To: "archived", RequiredRole: "Editor"},
    },
})
```

- `IsInitial` — entry point for new items (exactly one per flow)
- `IsTerminal` — no outbound transitions (e.g. "archived")
- `SuppressesSignals` — After* hooks do not fire for items in this state
- `RequiredRole` — stored in `smeldr_transitions` for future per-transition role enforcement (not yet enforced; planned for a later T23 step)
- Requires `Config.DB`. On unknown existing item states, returns error — treat like a failed migration.

**ConflictPolicy (v1.46.0+, A186):** Opt-in uniqueness enforcement at a designated state.

```go
err := app.RegisterFlow(smeldr.StateFlow{
    Name:           "article-flow",
    TypeName:       "Article",
    ActiveState:    "published",
    ConflictPolicy: smeldr.ConflictReject, // or smeldr.ConflictSupersede
    States: []smeldr.State{
        {Name: "draft", IsInitial: true},
        {Name: "published"},
        {Name: "archived", IsTerminal: true},
    },
    Transitions: []smeldr.Transition{
        {From: "draft", To: "published"},
        {From: "published", To: "archived"},
    },
})
```

- `ConflictReject` — `MCPPublish`/`SetStatus` returns `ErrConflict` (409) when another item is already in `ActiveState`
- `ConflictSupersede` — transitions all conflicting items to `"superseded"` before the new item enters `ActiveState`
- Zero value = no enforcement. Both policies fail-open: DB errors never block a transition.

**Decision freshness (v1.47.0+, A187):** Wire scheduled state transitions via `TransitionTrigger`.

```go
err := app.RegisterFlow(smeldr.StateFlow{
    Name:     "governance-decision",
    TypeName: "Decision",
    States: []smeldr.State{
        {Name: "proposed", IsInitial: true},
        {Name: "ratified"},
        {Name: "pending-re-evaluation"},
    },
    Transitions: []smeldr.Transition{
        {From: "proposed", To: "ratified"},
        {From: "ratified", To: "pending-re-evaluation"},
        {From: "pending-re-evaluation", To: "ratified"},
    },
    Triggers: []smeldr.TransitionTrigger{
        {
            FromState:    "proposed",
            ToState:      "ratified",
            TriggerClass: "async",
            TriggerType:  "schedule-eval",
            Config:       `{"eval_field":"next_eval_at","to_state":"pending-re-evaluation"}`,
        },
    },
})
```

- `schedule-eval` reads `eval_field` from the item row and inserts a timed entry in `smeldr_eval_queue`
- `App.DrainEvalQueue(ctx)` transitions due items; call via `agent.NewEvalQueueScheduler("", "UTC", app)` for automatic drain
- Fail-open: nil DB, missing table, and empty `eval_field` are all silently skipped

---

## MCP tool catalog

Tools are named from the type in lower_snake_case.
`BlogPost` → `blog_post`, `MCPDoc` → `mcp_doc`.

| Tool | Role | Description |
|------|------|-------------|
| `create_{type}` | Author+ | Create Draft |
| `update_{type}` | Author+ | Partial field update by slug |
| `publish_{type}` | Author+ | Draft → Published |
| `schedule_{type}` | Author+ | Draft → Scheduled (requires scheduled_at RFC 3339) |
| `archive_{type}` | Author+ | Any → Archived |
| `delete_{type}` | Editor+ | Permanent delete |
| `list_{type}s` | Editor+ | All items, optional status filter |
| `get_{type}` | Editor+ | Single item at any status |
| `create_upload_token` | Author+ | forge-media: generate upload token |
| `create_preview_url` | Editor+ | Draft preview URL (prefix + slug) |
| `create_token` | Admin | Mint bearer token |
| `list_tokens` / `revoke_token` | Admin | Token management |
| `create_webhook` / `list_webhooks` / `delete_webhook` | Admin | Webhook endpoints |
| `list_webhook_deliveries` / `retry_webhook` | Admin | Delivery introspection and retry |
| `create_redirect` / `list_redirects` / `delete_redirect` | Editor+ | Redirect rule management (requires `app.Redirects(db)`) |
| `set_page_meta` / `get_page_meta` / `delete_page_meta` / `list_page_meta` | Admin | Per-path SEO overrides (requires `mcp.WithPageMeta(db)`) |
| `assert_relation` / `propose_relation` / `get_relations` | Author+ | Relation graph: assert/propose edges, query by source/target/both. Gate: `app.Relations(store)` called. |
| `preview_impact` | Editor+ | Return dependents of a target item without firing signals. Gate: `app.Relations(store)` called. |
| `upsert_relation_kind` | Admin | Register or update a relation kind. Gate: `app.Relations(store)` called. |
| `list_relation_kinds` | Author+ | List all registered relation kinds. Gate: `app.Relations(store)` called. |
| `define_state_flow` | Admin | Register or update a state flow for a dynamic content type. Calls `App.RegisterFlow`; idempotent. `type_name` required. Returns `{name, type_name, state_count, transition_count}`. Gate: `App.Config().DB != nil`. |
| `transition_item` | Editor | Move a dynamic content item to a new state; validates against registered flow (ErrConflict → -32001). Gate: `App.Config().DB != nil`. |
| `get_valid_transitions` | Author | List legal target states for the item's current state; falls back to default flow. Gate: `App.Config().DB != nil`. |
| `list_items_by_state` | Author | List items of a dynamic content type in a given state. Gate: `App.Config().DB != nil`. |
| `create_signal` | Author | Insert a signal into smeldr_signals with status=pending. Args: `sender`, `receiver`, `signal_type` (required); `task_ref`, `message`, `sequence` (optional). Requires `CreateOrchestrationTables`. Gate: `App.Config().DB != nil`. |
| `list_signals` | Author | List signals by receiver+state (default "pending"). Fail-open on missing table. Gate: `App.Config().DB != nil`. |
| `get_goal_context` | Author | Retrieve a goal and all linked items (Decisions, Tasks, Goals) via the relation graph. Args: `goal_id` (required, e.g. `"T114"`). Returns `{goal, linked_decisions, linked_tasks, linked_goals}`. -32001 on not found. Gate: `App.Config().DB != nil`. smeldr.dev/mcp v1.27.0. |

Block system (T32, enabled with `mcp.WithBlocks()`; blocks addressed by ID, not slug):

| Tool | Role | Description |
|------|------|-------------|
| `create_node` | Author+ | Create a Draft block. Args `type_name`, `fields` (JSON object) |
| `update_node` | Author+ | Merge `fields` onto a block by `id` (absent keys preserved) |
| `get_node` / `list_nodes` | Author+ | Read block(s); `list_nodes` filters by `type_name` / `status` |
| `publish_node` / `archive_node` | Author+ | Lifecycle by `id` (publish idempotent) |
| `add_section` / `reorder_sections` / `remove_section` | Editor+ | Compose page sections |
| `add_item` / `reorder_items` / `remove_item` | Editor+ | Compose collection items |

`add_section` / `add_item` take only `parent_id` + `child_id` (types derived).
Reorder tools take `parent_id` + `ordered_child_ids`.

**Block rendering (developer API):** `app.ServeBlocks("templates/blocks")` returns a
`*BlockRenderer`; `Render(ctx, "page", pageID)` assembles a page's Published blocks
into HTML via one `templates/blocks/<type_name>.html` per type (batched, cycle-safe,
degrades gracefully). **Block `Fields` keys are PascalCase** (`{"Title":...,"Body":...}`,
matching the block-system type tables) — templates access `.Title`/`.Body`, so
snake_case keys render blank. Use PascalCase when creating blocks via `create_node`.
**Reference fields:** a `{Name}ID` field resolves to a `.{Name}` sub-object — `ImageID`
→ `.Image` (`.Image.MediaURL`/`.AltText`/`.Caption`) on content_block/contact_card/hero;
templates guard with `{{ with .Image }}…{{ .MediaURL }}…{{ end }}`. Published-only.

---

## forge-oauth — OAuth 2.1 for remote MCP (v0.1.0+)

ChatGPT Plus and Claude.ai require OAuth 2.1 to connect to remote MCP servers.
`forge-oauth` is a standalone MIT library (`smeldr.dev/oauth`).

```go
import oauth "smeldr.dev/oauth"

store, _ := oauth.NewSQLiteStore("./oauth.db")
oauthSrv := oauth.New(oauth.Config{
    Issuer: "https://cms.example.com",
    VerifyBearer: func(token string) bool {
        _, ok := smeldr.VerifyTokenString(token, app.Secret(), app.TokenStore())
        return ok
    },
}, store)
mcpSrv := mcp.New(app, mcp.WithOAuth(oauthSrv))
```

- `smeldr.VerifyTokenString(token, secret, store)` — validates a raw bearer token without `*http.Request` (v1.25.0+)
- `mcp.WithOAuth(*oauth.Server)` — enables OAuth; all HTTP endpoints require Bearer (v1.11.0+)
- `mcp.WithForgeFallback()` — accepts forge bearer tokens as fallback when OAuth enabled; use alongside `WithOAuth` to keep Claude Desktop/smeldr-cli working (v1.11.1+). `ErrTokenNotFound` → try forge bearer · `ErrTokenExpired` → always 401
- `GET /.well-known/oauth-protected-resource` — RFC 9728; triggers OAuth flow in AI clients on 401
- Scope mapping: `mcp` → Author role · `mcp:admin` → Admin role
- `offline_access` scope → refresh token issued (required for ChatGPT)
- CIMD: stateless client validation — no client registration database

---

## forge-media — upload token flow

**Never use `create_file` (base64) for real images** — 85 KB WebP ≈ 113 KB
base64 — too many tokens. Use the upload token flow instead.

```
Step 1: call create_upload_token MCP tool
        → returns { token, upload_url, expires_in }

Step 2: POST file directly to upload_url
        Authorization: UploadToken <token>
        Content-Type: multipart/form-data
        Field name: "file"

        curl -X POST https://example.com/media \
          -H "Authorization: UploadToken <token>" \
          -F "file=@hero.webp"

        → 201 { "url": "/media/abc123-hero.webp", ... }

Step 3: update content with returned URL
        update_story slug="my-story" image="/media/abc123-hero.webp"
```

Token expires in 15 min (default). MIME whitelist: jpeg, png, webp, gif, avif.
Filename gets a hex prefix — prevents overwrite of existing files.

---

## forge-social (separate module)

`smeldr.dev/social` — social post scheduling and agent routing.

```go
socialSrv := social.New(db, social.Config{
    Secret: cfg.Secret,
    // Mastodon/LinkedIn env-var config still accepted but deprecated.
    // Preferred: use create_platform_config MCP tool (Admin) to store
    // credentials in the DB; no env vars required.
})
socialSrv.Register(app)
defer socialSrv.Stop()

mcpSrv := mcp.New(app,
    mcp.WithModule(socialSrv.PostModule()),
    mcp.WithModule(socialSrv.CredentialModule()),
    mcp.WithModule(socialSrv.ConfigModule()),     // create_platform_config
    mcp.WithModule(socialSrv.ScheduleModule()),   // slot-queue
)
```

**Platforms:** `mastodon` | `linkedin` | `x` (Twitter, v0.5.0+)

**Two scheduling models:**
- Model 1: explicit `scheduled_at` timestamp per post
- Model 2: slot-queue — `PublicationSchedule` defines recurring weekly slots; posts with `status: queued` (no `scheduled_at`) are published FIFO when a slot fires

**Catch-up:** if the server was offline when a slot fired, one post per missed slot is published on the next tick, capped at `len(slots)` per tick.

**Schedule status:** `active` (slots fire) or `paused` (queue preserved, slots skipped).

**Slot format (MCP):** `slots` is a JSON array string — use `"timezone"`, not `"tz"`:
```json
[{"weekday": 1, "time": "09:00", "timezone": "Europe/Copenhagen"},
 {"weekday": 4, "time": "09:00", "timezone": "Europe/Copenhagen"}]
```
weekday: 0=Sunday, 1=Monday … 6=Saturday. Empty timezone silently defaults to UTC.

**Platform config (v0.5.0+):** call `create_platform_config` (Admin) with `platform`, `client_id`, `client_secret`, `redirect_url`, and (for Mastodon) `instance_url`. Optional: `scope` (space-separated OAuth 2.0 scopes; X default: `"tweet.read users.read tweet.write offline.access media.write"`; Mastodon default: `"write:statuses write:media"`). Credentials are stored AES-256-GCM encrypted in the DB. X requires a registered app in the Twitter developer portal with OAuth 2.0 enabled.

**X OAuth 2.0 + PKCE:** call `create_social_credential` with `platform=x` → returns a `redirect_url` containing the PKCE challenge. The code verifier is stored server-side — agents never see it. Operator completes the flow in a browser; callback saves the token automatically.

MCP tools: `create_platform_config`, `create_scheduled_post`, `list_scheduled_posts`, `publish_scheduled_post`,
`archive_scheduled_post`, `delete_scheduled_post`, `create_social_credential`,
`list_social_credentials`, `get_social_credential`, `delete_social_credential`,
`create_publication_schedule`, `get_publication_schedule`, `update_publication_schedule`,
`list_publication_schedules`, `delete_publication_schedule`

---

## smeldr-cli key commands

```bash
# Content
smeldr-cli posts create --from post.md
smeldr-cli posts update my-slug --from updated.md
smeldr-cli posts publish my-slug
smeldr-cli posts archive my-slug
smeldr-cli posts list --status draft

# Preview (Admin)
smeldr-cli preview /posts my-draft-slug

# Media
smeldr-cli media upload hero.jpg --description "Hero image"
smeldr-cli media list --type image
smeldr-cli media delete <id>

# Tokens (Admin)
smeldr-cli token create ci-deploy author 30
smeldr-cli token list
smeldr-cli token revoke <id>

# Webhooks
smeldr-cli webhook create --url https://example.com/hook --events post.published
smeldr-cli webhook list
smeldr-cli webhook delete <endpoint-id>

# Redirect management (Editor role, v1.34.0+) — requires app.Redirects(db)
smeldr-cli redirect list                                      # aligned table
smeldr-cli redirect list --json                              # raw JSON
smeldr-cli redirect create --from /old --to /new             # 301
smeldr-cli redirect create --from /gone --code 410           # 410 Gone
smeldr-cli redirect create --from /posts --to /articles --prefix  # prefix rewrite
smeldr-cli redirect delete /old-path

# Navigation tree (Editor role, v0.13.0+) — requires app.Nav(...) with DB mode
smeldr-cli nav list                                          # aligned table (ID, LABEL, PATH, PARENT, HIDDEN, GHOST, SORT)
smeldr-cli nav list --json                                   # raw JSON
smeldr-cli nav create --label "Learn" --path /learn          # create top-level item
smeldr-cli nav create --label "Intro" --path /learn/intro --parent-id <id>  # nested
smeldr-cli nav update <id> --label "New Label" --sort-order 2
smeldr-cli nav delete <id>                                   # cascades to descendants

# Audit trail (Editor role, v1.22.0+)
smeldr-cli audit list
smeldr-cli audit list --type Post
smeldr-cli audit list --from 2026-01-01T00:00:00Z --to 2026-12-31T23:59:59Z
smeldr-cli audit list --actor <actor-id>

# Block system (T32, smeldr-cli v0.10.0+) — Fields keys are case-sensitive PascalCase
smeldr-cli block node create --type hero --field Headline="Welcome"   # Author
smeldr-cli block node list --type hero --status published             # aligned table; --json for raw
smeldr-cli block node publish <id>
smeldr-cli block section add <page_id> <block_id>                     # Editor
smeldr-cli block section reorder <page_id> <id1,id2,id3>
smeldr-cli block item add <collection_id> <block_id>

# Social (smeldr-cli v0.8.0+)
smeldr-cli social credential create --platform mastodon|linkedin|x [--instance-url <url>]
smeldr-cli social credential list
smeldr-cli social credential get <id>
smeldr-cli social credential delete <id>
smeldr-cli social post create --platform mastodon|linkedin|x --credential <id> --body "..."
smeldr-cli social post queue --credential <id> --body "..."
smeldr-cli social post list --status queued
smeldr-cli social post publish <slug>
smeldr-cli social post archive <slug>
smeldr-cli social platform configure --platform mastodon|linkedin|x --client-id <id> --client-secret <secret> --redirect-url <url> [--instance-url <url>] [--success-url <url>]
smeldr-cli social schedule create --credential <id> --slot "monday 09:00 Europe/Copenhagen"
smeldr-cli social schedule show --credential <id>
smeldr-cli social schedule pause --credential <id>
smeldr-cli social schedule resume --credential <id>
smeldr-cli social schedule delete --credential <id>
```

Config: `SMELDR_URL`, `SMELDR_TOKEN`, `SMELDR_MCP_URL` (or `.smeldr-cli.env`; legacy `FORGE_*` env vars still read as fallbacks — T86/T87)

---

## Common gotchas

- **go.mod line 1** must be `module smeldr.dev/mcp` — not a github.com path
- **Verify go.mod deps before tagging** — `grep smeldr.dev go.mod`; run `go mod tidy`
- **Module proxy caches permanently** — bad tag requires a new patch tag, no fix
- **smeldr.Table()** — use when type name pluralises incorrectly (Story → storys)
- **Windows MIME** — add `mime.AddExtensionType(".webp", "image/webp")` in main()
- **Docker volume** — forge_media volume at /app/media; COPY in Dockerfile seeds it on first run
- **Archived ≠ Draft** — preview tokens bypass Draft/Scheduled only, never Archived
- **Timezone database** — `time.LoadLocation` fails on servers without OS tzdata (Alpine, scratch). forge-social embeds `time/tzdata` since v0.4.1 — ensure you are on v0.4.1 or later
- **X body limit** — X posts are capped at 280 characters. Exceeding the limit returns a terminal `publishError` — the post will never be retried. Truncate before calling `create_scheduled_post` with `platform=x`
- **X media** — `media_url` is ignored for platform `x` in v0.5.0. Attach images only for Mastodon and LinkedIn

---

## smeldr.config keys

```
base_url                      string
secret                        string  (panics if set here — use env instead)
dev                           bool    (disk-based static serving)
media_path                    string  (default ./media/)
media_max_size                int     (bytes, default 5242880)
preview_token_expiry          duration
media_upload_token_expiry     duration
```

---

## forge-agent (separate module)

`smeldr.dev/agent` v0.3.0 — minimal Go agent runtime with native MCP support.
MIT license. Three dependencies: `anthropic-sdk-go` + `modelcontextprotocol/go-sdk` + `gocron/v2`.

```go
cfg := agent.Config{
    MCPURL:        "http://localhost:8080/mcp",
    MCPToken:      "bearer-token",
    SystemPrompt:  "You are a helpful assistant.",
    Model:         "claude-sonnet-4-6", // default
    MaxTurns:      10,                  // default
    StreamableHTTP: false,              // false = SSE (forge-mcp), true = Streamable HTTP (GitHub MCP)
}
a := agent.New(cfg)
result, err := a.Run(ctx, "List all published posts and summarize the site.")
```

**Transport selection:**
- `StreamableHTTP: false` — SSE transport (`/mcp` + `/mcp/message`). Use for forge-mcp.
- `StreamableHTTP: true` — Streamable HTTP (MCP 2025-11-25 spec). Use for GitHub MCP
  (`https://api.githubcopilot.com/mcp/`) and other modern MCP servers.

**Built-in tools** (always available alongside MCP tools):

| Tool | Input | Behaviour |
|------|-------|-----------|
| `http_get` | `url` | GET request, returns body (32 KB cap) |
| `http_post` | `url`, `body`, `content_type` | POST body; `content_type` defaults to `text/plain` |

`http_post` examples: ntfy.sh notifications (`text/plain`), Discord webhooks (`application/json`).

**Scheduler (v0.2.0+):** `agent.NewScheduler([]agent.Job{...})` — cron-driven agent jobs.
Each `Job` has `Schedule` (5-field cron), `Timezone` (IANA), `Task` (prompt), `Config`.
Timezone validated at startup. Overlapping runs skipped. Missed jobs not caught up.
Add `import _ "time/tzdata"` to binaries on Alpine/scratch containers.

**Example binaries in repo:**
- `cmd/agent-forge` — `FORGE_MCP_URL` + `FORGE_TOKEN` (SSE)
- `cmd/agent-github` — `GITHUB_MCP_URL` + `GITHUB_TOKEN` + `GITHUB_REPO` (Streamable HTTP)
- `example/electricity-advisor/` — `ANTHROPIC_API_KEY` + `DISCORD_WEBHOOK_URL` (UC2: electricity prices → Discord)

### forge-agent/flow — Forge integration (v0.3.0)

`smeldr.dev/agent/flow` — AGPL sub-package. Wires `AgentJob` as a Forge
content type. Requires `smeldr.dev/core` as a dependency.

```go
import agentflow "smeldr.dev/agent/flow"

agentflow.CreateTable(db) // run once at startup
agentMod := agentflow.New(db, agentflow.Config{
    MCPURL:   "http://localhost:8080/mcp",
    MCPToken: os.Getenv("FORGE_TOKEN"),
})
agentMod.Register(app) // registers MCP tools + signal bus
defer agentMod.Stop()
```

**AgentJob fields:**

| Field | Description |
|-------|-------------|
| `Name` | Human-readable identifier. Slug source. Required. |
| `Trigger` | Cron expression (`"45 13 * * *"`) or forge signal (`"after_publish"`). Required. |
| `ContentTypeFilter` | Restrict signal trigger to content type (e.g. `"Post"`). Empty = all. |
| `SystemPrompt` | System instruction for every run. Required. |
| `Model` | Anthropic model ID. Defaults to `"claude-sonnet-4-6"`. |
| `MaxTurns` | Max tool-use loops. Defaults to 10. |
| `WebhookURL` | If set, agent task includes instruction to POST output here via `http_post`. |

**Status lifecycle:** Draft (does not run) → Published (active) → Archived (stopped).

**Auto-generated MCP tools (Admin role):**
`create_agent_job`, `get_agent_job`, `list_agent_jobs`, `update_agent_job`,
`publish_agent_job`, `archive_agent_job`, `delete_agent_job`

**Signal triggers:** set `Trigger` to any forge signal string value:
`after_publish`, `after_create`, `after_update`, `after_unpublish`,
`after_archive`, `after_schedule`, `after_delete`.

**Guard:** AgentJob lifecycle events never trigger other jobs — prevents self-activation loops.

---

## Skill update process

The canonical source for this file is:
`C:\Users\peter\Documents\Code\Smeldr\common\agent\skills\smeldr.md`

When updating: edit here first, then sync to `core/skills/smeldr.md` via the
doc-gate Copy-Item step before any M-number commit.
Pilots read this file directly — no copies to distribute.

---

## Haiku delegation — plan marking

Every corepilot plan for a task that contains mechanical deliverables should include
a Haiku-scan step. For each deliverable in the plan, mark it `[Haiku]` if it is
template-eligible, or leave it unmarked (implicitly Sonnet).

Eligible categories are defined in `context/corepilot.md` under
"Haiku delegation — mekaniske leverancer".

This makes delegation decisions explicit and reviewable by the architect.

**Example plan entry:**

| Deliverable | Owner |
|-------------|-------|
| coverage_test.go — 41 test stubs from plan table | [Haiku] |
| codecov.yml — new file with exact content specified | [Haiku] |
| decisions/recent.md — A150 entry (fixed format) | [Haiku] |
| context/corepilot.md — coverage gate addition | Sonnet |
| Plan writing | Sonnet |
