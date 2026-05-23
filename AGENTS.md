# Forge — AI Agent Guide

This document is for AI assistants working with Forge — either building
applications or consuming a running site via MCP.

Two roles, two sections:

- [AI coding agents](#for-ai-coding-agents) — you are helping a developer build a Forge application
- [AI consuming agents](#for-ai-consuming-agents) — you are connected to a running Forge site via MCP

---

## For AI coding agents

You are helping a developer build a Forge application.

### Start here

Read `example/blog/main.go` first. It is the minimal complete pattern:
content type, module options, app wiring, graceful shutdown. Copy and
rename it. Everything else is additive.

### Adding a content type

```go
type Post struct {
    forge.Node
    Title string `forge:"required,min=3" db:"title"`
    Body  string `forge:"required"       db:"body"`
}
```

Rules:

- Always embed `forge.Node` — never compose it
- Use `forge:"required"` and `forge:"min=N"` for validation
- Use `db:"column_name"` for `SQLRepo` column mapping
- Avoid SQLite reserved keywords as column names (`order`, `group`, etc.)
  — use `db:"sort_order"` instead

**json tags — required on every custom field**

All fields beyond `forge.Node` must have an explicit `json:"snake_case"` tag.
Without it, Go serialises the field as PascalCase, which breaks MCP read and write
operations — `update_post` with `"meta_title"` will silently return empty values.
`forge.Node` fields are exempt (handled internally).

```go
// Correct
type Post struct {
    forge.Node
    Title string `forge:"required" db:"title" json:"title"`
    Body  string `forge:"required" db:"body"  json:"body"`
}

// Wrong — MCP returns empty values for Title and Body
type Post struct {
    forge.Node
    Title string `forge:"required" db:"title"`
    Body  string `forge:"required" db:"body"`
}
```

### Field format hints

Use `forge_format` and `forge_description` to tell AI consuming agents
what a field expects. These hints appear in MCP tool descriptions at the
point of authoring.

```go
type DocPage struct {
    forge.Node
    Title string `forge:"required,min=3"`
    Body  string `forge:"required" forge_format:"markdown" forge_description:"Write content in Markdown. Supports headings, lists, and code blocks."`
    Embed string `forge_format:"html" forge_description:"Raw HTML only. Use for iframes and third-party embeds. Must be trusted content."`
}
```

Supported `forge_format` values:

| Value | Meaning |
|-------|---------|
| `markdown` | CommonMark/GFM markdown — also covers plain text |
| `html` | Trusted raw HTML — caller is responsible for sanitisation |

These tags are hints only. Forge performs no validation based on them.

### Wiring a module

This is the minimal wiring pattern from `example/blog/main.go`:

```go
repo := forge.NewMemoryRepo[*Post]()

m := forge.NewModule((*Post)(nil),
    forge.At("/posts"),
    forge.Repo(repo),
)

app := forge.New(forge.MustConfig(forge.Config{
    BaseURL: "http://localhost:8080",
    Secret:  []byte("change-this-secret-in-production"),
}))

app.Content(m)

if err := app.Run(":8080"); err != nil {
    log.Fatal(err)
}
```

### Adding MCP support

Add `forge.MCP(forge.MCPRead, forge.MCPWrite)` to the module options:

```go
forge.NewModule((*Post)(nil),
    forge.At("/posts"),
    forge.Repo(repo),
    forge.MCP(forge.MCPRead, forge.MCPWrite),
)
```

Wire the MCP server and token store in `main.go`:

```go
import forgemcp "forge-cms.dev/forge-mcp"

app := forge.New(forge.MustConfig(forge.Config{
    BaseURL:    "https://mysite.com",
    Secret:     []byte(os.Getenv("SECRET")),
    DB:         db,
    TokenStore: forge.NewTokenStore(db, os.Getenv("SECRET")),
}))

mcpSrv := forgemcp.New(app)
app.Handle("GET /mcp", mcpSrv.Handler())
app.Handle("POST /mcp/message", mcpSrv.Handler())
```

See `forge-mcp/README.md` for connection setup and token management.

### Module routing variants

Three opt-in routing variants change how a module's URLs behave:

| Option | When to use | HTML surface |
|--------|-------------|-------------|
| *(default)* | Public content with list and show pages | Full |
| `forge.SingleInstance()` | One canonical item (about page, landing page) | `GET /{prefix}` only |
| `forge.Standalone()` | Items at clean top-level URLs (`/{slug}`) | `/{slug}` and `/{prefix}` list |
| `forge.APIOnly()` | Admin-only types managed via MCP/CLI, no public web surface | None — `text/html` → 404 |

`APIOnly()` example — admin-only content type:

```go
forge.NewModule((*HomePage)(nil),
    forge.At("/home-pages"),
    forge.Repo(repo),
    forge.MCP(forge.MCPWrite),
    forge.APIOnly(),
)
// GET /home-pages Accept:application/json → 200 JSON
// GET /home-pages Accept:text/html        → 404 (not browsable)
// MCP tools: full set (create_home_page, update_home_page, etc.)
```

`APIOnly()` and `SingleInstance()` cannot be combined — `NewModule` panics at startup.

### Adding media support (forge-media)

`forge-media` is an optional submodule that adds file upload, storage, and
serving. It implements `forge.MCPModule` so AI agents can upload files via MCP.

```go
import (
    forgemedia "forge-cms.dev/forge-media"
    forgemcp   "forge-cms.dev/forge-mcp"
)

app := forge.New(forge.MustConfig(forge.Config{
    BaseURL: "https://mysite.com",
    Secret:  []byte(os.Getenv("SECRET")),
    DB:      db,
}))

// Register HTTP routes: POST /media, GET /media/{filename}, etc.
store := forgemedia.NewLocalMediaStore(app)
mediaSrv := forgemedia.Register(app, store)

// Wire into MCP so agents can upload via create_file tool.
mcpSrv := forgemcp.New(app, forgemcp.WithModule(mediaSrv))
app.Handle("GET /mcp", mcpSrv.Handler())
app.Handle("POST /mcp/message", mcpSrv.Handler())
```

`Config.MediaPath` (default `"./media"`) and `Config.MediaMaxSize` (default 5 MB)
control storage. Both are set in `forge.Config` or via `forge.config` file keys
`media_path` and `media_max_size`.

### Key rules for code generation

- Zero third-party dependencies in the `forge` core package
- `forge.Context` is an interface, not a struct
- `forge.DB` is an interface, not `*sql.DB`
- All errors must implement `forge.Error` — never raw `errors.New`
- Read `ERROR_HANDLING.md` before writing any error-handling code
- Never use `forge.SignToken` in `main()` when `TokenStore` is wired —
  stateless HMAC tokens are rejected by `VerifyBearerToken` when a store is configured

---

## For AI consuming agents

You are connected to a running Forge site via MCP. This guide applies
regardless of which MCP-compatible agent you are — Claude, Cursor, or any
other tool that supports the Model Context Protocol.

### What you can do

Two operations are available depending on how the site owner configured
the modules:

- **MCPRead** — list and read published content
- **MCPWrite** — create, update, publish, schedule, archive, delete content

Admin-role agents also have access to token management tools (see below).

### Lifecycle rules

Content follows `Draft → Scheduled → Published → Archived`. You cannot
bypass this. Publishing requires an explicit `publish` tool call after
`create`.

### Role enforcement

Write operations require `Author` role or higher. The Bearer token you
were given determines your role. If an operation returns `forbidden`,
you do not have sufficient role — do not retry.

### Available tools (MCPWrite)

For each registered content type, these tools are available:

- `create_{type}` — creates a Draft
- `update_{type}` — partial update (absent fields preserved)
- `publish_{type}` — transitions to Published
- `schedule_{type}` — schedules for future publication (RFC3339 datetime)
- `archive_{type}` — transitions to Archived
- `delete_{type}` — permanent deletion

### Field format hints

When a content type field carries a `forge_format` or `forge_description`
tag, the tool description tells you exactly what the field expects.
Follow it precisely — Markdown fields expect Markdown, HTML fields expect
raw HTML. Do not mix formats.

### Reading content

- `resources/list` — all Published items across all MCPRead modules
- `resources/read` — single item by URI (`forge://{prefix}/{slug}`)

### Resource subscriptions

When connected via SSE, you can subscribe to real-time content change
notifications:

- `resources/subscribe` — subscribe to a resource URI; you will receive
  `notifications/resources/updated` when that item is published, updated,
  or deleted.
- `resources/unsubscribe` — cancel a subscription.

Use subscriptions to keep cached content fresh without polling.
The `capabilities.resources.subscribe` flag in the `initialize` response
confirms subscriptions are available on this server.

### Token management tools (Admin role required)

These tools are available when the site has `TokenStore` configured:

| Tool | Description |
|------|-------------|
| `create_token` | Issues a new named token with a given role and TTL |
| `list_tokens` | Lists all tokens with name, role, expiry, revoked status |
| `revoke_token` | Revokes a token by ID — effective immediately |

**Critical rules for token operations:**

- Always use `list_tokens` before `revoke_token` to confirm the ID
- `revoke_token` will refuse if the token is the last active admin token —
  create a replacement first
- A revoked token cannot be restored — revocation is permanent
- Never revoke a token without explicit instruction from the site owner
- `create_token` returns the plaintext token once — copy it immediately
  and deliver it through a secure channel. It cannot be retrieved again.

### Webhook management tools (Admin role required)

These tools are available when the site has `App.Webhooks(store)` configured:

| Tool | Description |
|------|-------------|
| `create_webhook` | Registers a new outbound endpoint (HTTPS only). Returns signing secret once. |
| `list_webhooks` | Lists all registered endpoints with delivery statistics. |
| `delete_webhook` | Removes an endpoint by ID. |
| `list_webhook_deliveries` | Shows delivery log for a specific job ID. |
| `retry_webhook` | Re-queues a dead job for delivery. |

**Webhook rules:**
- `create_webhook` requires `url` (HTTPS, no private/localhost IPs) and `events` (list of event names such as `post.published`)
- The signing secret is returned once at creation — deliver it securely
- `list_webhooks` never returns secrets
- Use `list_webhooks` before `delete_webhook` to confirm the ID

### Connection setup

See `forge-mcp/README.md` for Claude Desktop, Cursor, and SSE configuration.
