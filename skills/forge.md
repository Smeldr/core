# Forge CMS — developer skill

Forge is a Go content framework. This skill covers what you need to work
with forge as a developer or pilot agent.

Current versions: forge v1.24.0 · forge-mcp v1.10.3 · forge-media v1.2.0 · forge-cli v0.9.0 · forge-social v0.6.0 · forge-agent v0.4.0

---

## Core concepts

```
Node      — base struct every content type embeds (ID, Slug, Status, timestamps)
Module    — one content type, fully wired (routes, repo, MCP, signals)
Signal    — hook that fires on lifecycle changes (AfterPublish, AfterArchive, ...)
Head      — all page metadata (SEO, OG, JSON-LD, canonical)
Role      — Guest < Author < Editor < Admin
Config    — forge.config file + Go struct; Go-code values win
```

Content lifecycle: `Draft → Published/Scheduled → Archived`
Archived items are permanently invisible — cannot be reverted to Draft.

---

## Defining a content type

```go
type Story struct {
    forge.Node
    Title   string `forge:"required" json:"title"`
    Body    string `forge:"required,min=50" json:"body"`
    Image   string `forge:"" forge_description:"Hero image path." db:"image" json:"image"`
    OGImage string `forge:"" forge_description:"OG image URL." db:"og_image" json:"og_image"`
}
```

**json tag is required on every custom field** — all fields beyond `forge.Node`.
Without an explicit `json:"snake_case"` tag, Go serialises the field as PascalCase,
which breaks MCP read and write operations (MCP uses snake_case keys).
`forge.Node` fields are exempt — they are handled internally.

Wrong (MCP returns empty/missing values):
```go
type MyPage struct {
    forge.Node
    Title string `db:"title"`   // ← missing json tag — MCP cannot map "title" → Title
    Body  string `db:"body"`
}
```

Correct:
```go
type MyPage struct {
    forge.Node
    Title string `db:"title" json:"title"`
    Body  string `db:"body"  json:"body"`
}
```

---

## Wiring a module

```go
app.Content(forge.NewModule((*Story)(nil),
    forge.At("/solved"),
    forge.Table("stories"),       // override incorrect pluralisation
    forge.Repo(forge.NewSQLRepo[*Story](db)),
    forge.MCP(forge.MCPRead, forge.MCPWrite),
))
```

---

## Routing variants (v1.23.0+)

### SingleInstance — singleton page at module prefix

Use when a module holds exactly one canonical item (About, Contact, Terms):

```go
forge.NewModule((*AboutPage)(nil),
    forge.At("/about"),
    forge.Repo(repo),
    forge.SingleInstance(),
    forge.MCP(forge.MCPRead, forge.MCPWrite),
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
app.Content(forge.NewModule((*HomePage)(nil),
    forge.Repo(homePageRepo),
    forge.At("/homepage"),
    forge.SingleInstance(),
    forge.MCP(forge.MCPRead, forge.MCPWrite),
))

// Public route — custom handler reads the published record
app.Handle("GET /", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    hps, _ := homePageRepo.FindAll(r.Context(), forge.ListOptions{
        Status: []forge.Status{forge.Published},
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
forge.NewModule((*Post)(nil),
    forge.At("/posts"),
    forge.Repo(repo),
    forge.Standalone(),
)
// GET /my-post     → serves Published Post with slug "my-post"
// GET /posts       → list of Published posts (unchanged)
// GET /posts/my-post → 404 (not registered)
```

Multiple Standalone modules coexist — slug dispatch is first-match.
AIDoc served at `GET /{slug}/aidoc` when `forge.AIIndex(forge.AIDoc)` is also set.

### APIOnly — no public HTML surface (v1.24.0+)

Use when a content type is managed exclusively via MCP or CLI with no public web presence:

```go
forge.NewModule((*HomePage)(nil),
    forge.At("/home-pages"),
    forge.Repo(repo),
    forge.MCP(forge.MCPWrite),
    forge.APIOnly(),
)
// GET /home-pages Accept:application/json → 200 JSON
// GET /home-pages Accept:text/html        → 404
// MCP tools: full set (list, get, create, update, publish, etc.)
```

`APIOnly()` + `SingleInstance()` panics at startup — incompatible combination.

---

## Signal bus (v1.20.0+)

`app.OnSignal` registers a subscriber for a lifecycle signal. Handler contract:
enqueue work and return immediately — never block. Errors are logged and never
propagated to the publish caller.

```go
app.OnSignal(forge.AfterPublish, func(ctx context.Context, ev forge.SignalEvent) error {
    // ctx is detached from the request (WithoutCancel) — safe to enqueue async work
    return myQueue.Enqueue(ctx, ev)
})
```

Signal constants: `AfterPublish`, `AfterSchedule`, `AfterArchive`, `AfterDelete`

`SignalEvent` fields: `Type`, `Slug`, `Title`, `URL`, `Timestamp`, `PreviousState`, `ActorRole`, `ActorID`

`PreviousState` and `ActorRole` are transient — not reconstructable via MCP after the fact.

**Built-in audit trail (v1.22.0+):** `app.Audit(forge.NewAuditStore(db))` subscribes to
`AfterPublish`, `AfterSchedule`, `AfterArchive`, `AfterDelete` and persists each transition.
`GET /_audit` (Editor+) returns JSON records. `forge-cli audit list` prints a table.

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

`forge-cms.dev/forge-social` — social post scheduling and agent routing.

```go
social := forgesocial.New(db, forgesocial.Config{
    Secret: cfg.Secret,
    // Mastodon/LinkedIn env-var config still accepted but deprecated.
    // Preferred: use create_platform_config MCP tool (Admin) to store
    // credentials in the DB; no env vars required.
})
social.Register(app)
defer social.Stop()

mcpSrv := forgemcp.New(app,
    forgemcp.WithModule(social.PostModule()),
    forgemcp.WithModule(social.CredentialModule()),
    forgemcp.WithModule(social.ConfigModule()),     // create_platform_config
    forgemcp.WithModule(social.ScheduleModule()),   // slot-queue
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

**Platform config (v0.5.0+):** call `create_platform_config` (Admin) with `platform`, `client_id`, `client_secret`, `redirect_url`, and (for Mastodon) `instance_url`. Credentials are stored AES-256-GCM encrypted in the DB. X requires a registered app in the Twitter developer portal with OAuth 2.0 enabled.

**X OAuth 2.0 + PKCE:** call `create_social_credential` with `platform=x` → returns a `redirect_url` containing the PKCE challenge. The code verifier is stored server-side — agents never see it. Operator completes the flow in a browser; callback saves the token automatically.

MCP tools: `create_platform_config`, `create_scheduled_post`, `list_scheduled_posts`, `publish_scheduled_post`,
`archive_scheduled_post`, `delete_scheduled_post`, `create_social_credential`,
`list_social_credentials`, `get_social_credential`, `delete_social_credential`,
`create_publication_schedule`, `get_publication_schedule`, `update_publication_schedule`,
`list_publication_schedules`, `delete_publication_schedule`

---

## forge-cli key commands

```bash
# Content
forge-cli posts create --from post.md
forge-cli posts update my-slug --from updated.md
forge-cli posts publish my-slug
forge-cli posts archive my-slug
forge-cli posts list --status draft

# Preview (Admin)
forge-cli preview /posts my-draft-slug

# Media
forge-cli media upload hero.jpg --description "Hero image"
forge-cli media list --type image
forge-cli media delete <id>

# Tokens (Admin)
forge-cli token create ci-deploy author 30
forge-cli token list
forge-cli token revoke <id>

# Webhooks
forge-cli webhook create --url https://example.com/hook --events post.published
forge-cli webhook list
forge-cli webhook delete <endpoint-id>

# Audit trail (Editor role, v1.22.0+)
forge-cli audit list
forge-cli audit list --type Post
forge-cli audit list --from 2026-01-01T00:00:00Z --to 2026-12-31T23:59:59Z
forge-cli audit list --actor <actor-id>

# Social (forge-cli v0.8.0+)
forge-cli social credential create --platform mastodon|linkedin|x [--instance-url <url>]
forge-cli social credential list
forge-cli social credential get <id>
forge-cli social credential delete <id>
forge-cli social post create --platform mastodon|linkedin|x --credential <id> --body "..."
forge-cli social post queue --credential <id> --body "..."
forge-cli social post list --status queued
forge-cli social post publish <slug>
forge-cli social post archive <slug>
forge-cli social platform configure --platform mastodon|linkedin|x --client-id <id> --client-secret <secret> --redirect-url <url> [--instance-url <url>] [--success-url <url>]
forge-cli social schedule create --credential <id> --slot "monday 09:00 Europe/Copenhagen"
forge-cli social schedule show --credential <id>
forge-cli social schedule pause --credential <id>
forge-cli social schedule resume --credential <id>
forge-cli social schedule delete --credential <id>
```

Config: `FORGE_URL`, `FORGE_TOKEN`, `FORGE_MCP_URL` (or `.forge-cli.env`)

---

## Common gotchas

- **go.mod line 1** must be `module forge-cms.dev/forge-mcp` — not a github.com path
- **Verify go.mod deps before tagging** — `grep forge-cms.dev go.mod`; run `go mod tidy`
- **Module proxy caches permanently** — bad tag requires a new patch tag, no fix
- **forge.Table()** — use when type name pluralises incorrectly (Story → storys)
- **Windows MIME** — add `mime.AddExtensionType(".webp", "image/webp")` in main()
- **Docker volume** — forge_media volume at /app/media; COPY in Dockerfile seeds it on first run
- **Archived ≠ Draft** — preview tokens bypass Draft/Scheduled only, never Archived
- **Timezone database** — `time.LoadLocation` fails on servers without OS tzdata (Alpine, scratch). forge-social embeds `time/tzdata` since v0.4.1 — ensure you are on v0.4.1 or later
- **X body limit** — X posts are capped at 280 characters. Exceeding the limit returns a terminal `publishError` — the post will never be retried. Truncate before calling `create_scheduled_post` with `platform=x`
- **X media** — `media_url` is ignored for platform `x` in v0.5.0. Attach images only for Mastodon and LinkedIn

---

## forge.config keys

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

`forge-cms.dev/forge-agent` v0.3.0 — minimal Go agent runtime with native MCP support.
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

`forge-cms.dev/forge-agent/flow` — AGPL sub-package. Wires `AgentJob` as a Forge
content type. Requires `forge-cms.dev/forge` as a dependency.

```go
import forgeagent "forge-cms.dev/forge-agent/flow"

forgeagent.CreateTable(db) // run once at startup
agentMod := forgeagent.New(db, forgeagent.Config{
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
`C:\Users\peter\Documents\Code\forge-common\agent\skills\forge.md`

When updating: edit here first, then update the version line and notify pilots.
Pilots read this file directly — no copies to distribute.
