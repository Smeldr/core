# Forge CMS — developer skill

Forge is a Go content framework. This skill covers what you need to work
with forge as a developer or pilot agent.

Current versions: forge v1.20.0 · forge-mcp v1.9.2 · forge-media v1.2.0 · forge-cli v0.7.0

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
    Image   string `forge:"" forge_description:"Hero image path." db:"image"`
    OGImage string `forge:"" forge_description:"OG image URL." db:"og_image" json:"og_image"`
}
```

**json tag is required** for multi-word snake_case fields (e.g. `og_image`).
Without it, `json.Unmarshal` cannot map MCP's snake_case key to the Go field.

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
| `create_preview_url` | Admin | Draft preview URL (prefix + slug) |
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
    Mastodon: forgesocial.MastodonConfig{...},
})
social.Register(app)
defer social.Stop()

mcpSrv := forgemcp.New(app,
    forgemcp.WithModule(social.PostModule()),
    forgemcp.WithModule(social.CredentialModule()),
    forgemcp.WithModule(social.ScheduleModule()),  // slot-queue
)
```

**Two scheduling models:**
- Model 1: explicit `scheduled_at` timestamp per post
- Model 2: slot-queue — `PublicationSchedule` defines recurring weekly slots; posts with `status: queued` (no `scheduled_at`) are published FIFO when a slot fires

**Catch-up:** if the server was offline when a slot fired, one post per missed slot is published on the next tick, capped at `len(slots)` per tick.

**Schedule status:** `active` (slots fire) or `paused` (queue preserved, slots skipped).

MCP tools: `create_scheduled_post`, `list_scheduled_posts`, `publish_scheduled_post`,
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

# Social (forge-cli v0.7.0+)
forge-cli social credential create --platform mastodon --name "My account"
forge-cli social credential list
forge-cli social post create --platform mastodon --credential <id> --body "..."
forge-cli social post queue --credential <id> --body "..."
forge-cli social post list --status queued
forge-cli social post publish <slug>
forge-cli social post archive <slug>
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

## Skill update checklist (run before tagging any forge/forge-mcp/forge-media/forge-cli release)

- [ ] MCP tools or CLI commands changed? → update both sections and verify
      CLI/MCP parity: every MCP tool must have a CLI equivalent and vice versa
- [ ] New Config keys? → update forge.config section
- [ ] New failure modes confirmed in this release? → update gotchas
- [ ] Bump version line at top of this file
- [ ] Copy updated file to Forge-site-working/.claude/skills/forge.md
      and to forge-architect/.claude/skills/forge.md
