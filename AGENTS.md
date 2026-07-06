# Smeldr — AI Agent Guide

This document is for AI assistants working with Smeldr — either building
applications or consuming a running site via MCP.

Two roles, two sections:

- [AI coding agents](#for-ai-coding-agents) — you are helping a developer build a Smeldr application
- [AI consuming agents](#for-ai-consuming-agents) — you are connected to a running Smeldr site via MCP

---

## For AI coding agents

You are helping a developer build a Smeldr application.

### Start here

Read `example/blog/main.go` first. It is the minimal complete pattern:
content type, module options, app wiring, graceful shutdown. Copy and
rename it. Everything else is additive.

### Adding a content type

```go
type Post struct {
    smeldr.Node
    Title string `smeldr:"required,min=3" db:"title"`
    Body  string `smeldr:"required"       db:"body"`
}
```

Rules:

- Always embed `smeldr.Node` — never compose it
- Use `smeldr:"required"` and `smeldr:"min=N"` for validation
- Use `db:"column_name"` for `SQLRepo` column mapping
- Avoid SQLite reserved keywords as column names (`order`, `group`, etc.)
  — use `db:"sort_order"` instead

**json tags — required on every custom field**

All fields beyond `smeldr.Node` must have an explicit `json:"snake_case"` tag.
Without it, Go serialises the field as PascalCase, which breaks MCP read and write
operations — `update_post` with `"meta_title"` will silently return empty values.
`smeldr.Node` fields are exempt (handled internally).

```go
// Correct
type Post struct {
    smeldr.Node
    Title string `smeldr:"required" db:"title" json:"title"`
    Body  string `smeldr:"required" db:"body"  json:"body"`
}

// Wrong — MCP returns empty values for Title and Body
type Post struct {
    smeldr.Node
    Title string `smeldr:"required" db:"title"`
    Body  string `smeldr:"required" db:"body"`
}
```

### Field format hints

Use `smeldr_format` and `smeldr_description` to tell AI consuming agents
what a field expects. These hints appear in MCP tool descriptions at the
point of authoring.

```go
type DocPage struct {
    smeldr.Node
    Title string `smeldr:"required,min=3"`
    Body  string `smeldr:"required" smeldr_format:"markdown" smeldr_description:"Write content in Markdown. Supports headings, lists, and code blocks."`
    Embed string `smeldr_format:"html" smeldr_description:"Raw HTML only. Use for iframes and third-party embeds. Must be trusted content."`
}
```

Supported `smeldr_format` values:

| Value | Meaning |
|-------|---------|
| `markdown` | CommonMark/GFM markdown — also covers plain text |
| `html` | Trusted raw HTML — caller is responsible for sanitisation |

These tags are hints only. Forge performs no validation based on them.

### Wiring a module

This is the minimal wiring pattern from `example/blog/main.go`:

```go
repo := smeldr.NewMemoryRepo[*Post]()

m := smeldr.NewModule((*Post)(nil),
    smeldr.At("/posts"),
    smeldr.Repo(repo),
)

app := smeldr.New(smeldr.MustConfig(smeldr.Config{
    BaseURL: "http://localhost:8080",
    Secret:  []byte("change-this-secret-in-production"),
}))

app.Content(m)

if err := app.Run(":8080"); err != nil {
    log.Fatal(err)
}
```

### Adding MCP support

Add `smeldr.MCP(smeldr.MCPRead, smeldr.MCPWrite)` to the module options:

```go
smeldr.NewModule((*Post)(nil),
    smeldr.At("/posts"),
    smeldr.Repo(repo),
    smeldr.MCP(smeldr.MCPRead, smeldr.MCPWrite),
)
```

Wire the MCP server and token store in `main.go`:

```go
import mcp "smeldr.dev/mcp"

app := smeldr.New(smeldr.MustConfig(smeldr.Config{
    BaseURL:    "https://mysite.com",
    Secret:     []byte(os.Getenv("SECRET")),
    DB:         db,
    TokenStore: smeldr.NewTokenStore(db, os.Getenv("SECRET")),
}))

mcpSrv := mcp.New(app)
app.Handle("GET /mcp", mcpSrv.Handler())
app.Handle("POST /mcp/message", mcpSrv.Handler())
```

See the smeldr.dev/mcp README for connection setup and token management.

### Module routing variants

Three opt-in routing variants change how a module's URLs behave:

| Option | When to use | HTML surface |
|--------|-------------|-------------|
| *(default)* | Public content with list and show pages | Full |
| `smeldr.SingleInstance()` | One canonical item (about page, landing page) | `GET /{prefix}` only |
| `smeldr.Standalone()` | Items at clean top-level URLs (`/{slug}`) | `/{slug}` and `/{prefix}` list |
| `smeldr.APIOnly()` | Admin-only types managed via MCP/CLI, no public web surface | None — `text/html` → 404 |

`APIOnly()` example — admin-only content type:

```go
smeldr.NewModule((*HomePage)(nil),
    smeldr.At("/home-pages"),
    smeldr.Repo(repo),
    smeldr.MCP(smeldr.MCPWrite),
    smeldr.APIOnly(),
)
// GET /home-pages Accept:application/json → 200 JSON
// GET /home-pages Accept:text/html        → 404 (not browsable)
// MCP tools: full set (create_home_page, update_home_page, etc.)
```

`APIOnly()` and `SingleInstance()` cannot be combined — `NewModule` panics at startup.

### Adding media support (smeldr.dev/media)

`smeldr.dev/media` is an optional module that adds file upload, storage, and
serving. It implements `smeldr.MCPModule` so AI agents can upload files via MCP.

```go
import (
    media "smeldr.dev/media"
    mcp   "smeldr.dev/mcp"
)

app := smeldr.New(smeldr.MustConfig(smeldr.Config{
    BaseURL: "https://mysite.com",
    Secret:  []byte(os.Getenv("SECRET")),
    DB:      db,
}))

// Register HTTP routes: POST /media, GET /media/{filename}, etc.
store := media.NewLocalMediaStore(app)
mediaSrv := media.Register(app, store)

// Wire into MCP so agents can upload via create_file tool.
mcpSrv := mcp.New(app, mcp.WithModule(mediaSrv))
app.Handle("GET /mcp", mcpSrv.Handler())
app.Handle("POST /mcp/message", mcpSrv.Handler())
```

`Config.MediaPath` (default `"./media"`) and `Config.MediaMaxSize` (default 5 MB)
control storage. Both are set in `smeldr.Config` or via `smeldr.config` file keys
`media_path` and `media_max_size`.

### Block system rendering (ServeBlocks)

`app.ServeBlocks(dir)` renders pages composed of blocks (generic content nodes)
and composition edges, using one convention template per block type at
`templates/blocks/<type_name>.html`:

```go
r, err := app.ServeBlocks("templates/blocks")
html, err := r.Render(ctx, "page", pageID) // ordered, Published section blocks → HTML
```

`ServeBlocks` ensures the block tables (`smeldr.CreateBlockTables`). Rendering
batch-loads (no N+1), is cycle-safe, and degrades gracefully — a bad or
unpublished block is skipped, never failing the page. Each template receives the
block's `Fields` promoted to top level plus `.ID` / `.Slug` / `.Status` /
`.AnchorID`; collections also get `.Layout` and `.Items` (pre-rendered).

**Block `Fields` keys are PascalCase — this is a hard rule.** Templates access
`.Title`, `.Body`, `.Headline`, so blocks must be stored with PascalCase field
keys (`{"Title": "...", "Body": "..."}`), matching the block-system type tables.
A block created with snake_case keys (e.g. `{"title": ...}`) will render blank —
the template accessor `.Title` cannot find key `title`. When creating blocks via
the `create_node` MCP tool, use PascalCase field names.

**Reference fields** (`ImageID` → `.Image`): a block field named `{Name}ID` that
holds another block's ID is resolved by ServeBlocks into a `.{Name}` sub-object
carrying that block's data. `content_block`, `contact_card`, and `hero` carry
`ImageID` referencing a published Image block; templates render it guarded:

```html
{{ with .Image }}<img src="{{ .MediaURL }}" alt="{{ .AltText }}">{{ end }}
```

Resolution is Published-only and guarded — an absent/unpublished/dangling reference
simply renders nothing. To make a block show an image: create the Image block, then
set the parent's `ImageID` to the Image block's ID.

### Log capture (CaptureLogs + /_logs)

Opt-in, in-memory capture of recent log records for live debugging. Records still
reach the existing handler (stderr) AND, at/above the capture level, are stored in a
bounded ring served at `GET /_logs` (Admin, plain HTTP + bearer — works when MCP is
down). Not log storage: in-memory only, lost on restart.

```go
import "log/slog"

// Call AFTER any app-side slog.SetDefault of your own.
app.CaptureLogs() // ring of 500, WARN and above (defaults)
// or: app.CaptureLogs(smeldr.WithLogCapacity(1000), smeldr.WithLogLevel(slog.LevelInfo))
```

`GET /_logs` (Admin) returns `{capacity, count, dropped, entries}` (entries
newest-first). Query params: `level` (min, inclusive), `limit` (most recent N),
`since` (RFC3339). Route is absent (404) unless `CaptureLogs` was called. There is no
MCP tool for logs by design — the path must not depend on MCP. Use `smeldr-cli logs`.

### Generic reference server (example/server)

`example/server/main.go` is a deployable binary with no custom Go content types.
All content types are defined at runtime via the `define_content_type` MCP tool.
Optional subsystems are gated by environment variables — the binary compiles and
runs with only `SECRET` set; every other feature is opt-in.

**Run it:**

```bash
cd example/server
SECRET=changeme go run .
```

**Environment variables:**

| Variable | Required? | Description |
|----------|-----------|-------------|
| `SECRET` | yes | HMAC signing secret (min 32 bytes in production) |
| `BASE_URL` | optional | canonical origin, e.g. `https://cms.example.com` |
| `DATABASE_PATH` | optional | path to SQLite database (default: `smeldr.db`) |
| `PORT` | optional | HTTP listen port (default: `8080`) |
| `ADDR` | optional | full listen address (default: `127.0.0.1:PORT`) |
| `ENABLE_TOKENS` | boolean | wire database-backed named token management |
| `ENABLE_GOVERNANCE` | boolean | wire role-based access control |
| `ENABLE_RELATIONS` | boolean | wire the relation graph store |
| `ENABLE_DYNAMIC_CONTENT` | boolean | wire the runtime content type system |
| `ENABLE_BLOCKS` | boolean | wire the block/composition system MCP tools |
| `ENABLE_ORCHESTRATION` | boolean | wire orchestration types (Signal, Task, Decision, Amendment, Goal); set `ENABLE_RELATIONS` for full `get_goal_context` traversal |
| `ENABLE_REDIRECTS` | boolean | wire database-backed redirect management |
| `ENABLE_PAGE_META` | boolean | wire per-path SEO override store |
| `ENABLE_MEDIA` | boolean | wire local media upload and management |
| `ENABLE_SOCIAL` | boolean | wire Mastodon social publishing |
| `ENABLE_WEBHOOKS` | boolean | wire outbound webhook delivery |
| `ENABLE_AGENTS` | boolean | wire the agent job system |
| `AGENT_MCP_URL` | when ENABLE_AGENTS | agent MCP endpoint (default: `http://127.0.0.1:PORT/mcp/message`) |
| `AGENT_MCP_TOKEN` | when ENABLE_AGENTS | bearer token for agent MCP calls |
| `OAUTH_ISSUER` | optional | enable OAuth 2.1; set to canonical issuer URL |
| `OAUTH_DB_PATH` | when OAUTH_ISSUER | path to OAuth SQLite database (default: `./oauth.db`) |

Boolean vars gate their subsystem: set the var to any non-empty value to enable it
(`ENABLE_TOKENS=1`, `ENABLE_GOVERNANCE=true`, etc.).

**Testing:**

```bash
# In-process unit tests (runs as part of go test ./...):
cd example/server && go test ./...

# Preflight test — builds and spawns the real binary, confirms /_health + /goals:
cd example/server && go test -tags preflight -v -run TestPreflight .
```

When writing your own `main.go`, use `example/server/main.go` as the reference for
correct wiring order — some calls have load-bearing ordering constraints (e.g.
`CreateRelationTables` before `NewRelationStore`; `agentMod.Register` before
`mcp.New` so `AgentJob` appears in the MCP tool list).

### Key rules for code generation

- Zero third-party dependencies in the `smeldr` core package
- `smeldr.Context` is an interface, not a struct
- `smeldr.DB` is an interface, not `*sql.DB`
- All errors must implement `smeldr.Error` — never raw `errors.New`
- Read `ERROR_HANDLING.md` before writing any error-handling code
- Never use `smeldr.SignToken` in `main()` when `TokenStore` is wired —
  stateless HMAC tokens are rejected by `VerifyBearerToken` when a store is configured

---

## For AI consuming agents

You are connected to a running Smeldr site via MCP. This guide applies
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

### Block tools (when the server is started with `WithBlocks`)

The block system stores all block types as generic nodes and composes them into
pages and collections. Blocks are addressed by **ID** — they have no slug and are
not browsable resources (use `get_node` / `list_nodes`, not `resources/read`).

Generic node lifecycle (Author role):

- `create_node(type_name, fields)` — creates a Draft block. `type_name` is the
  block type (e.g. `"content_block"`, `"hero"`, `"faq_item"`); `fields` is a JSON
  object of type-specific data. Returns the new block's `id`.
- `update_node(id, fields)` — merges `fields` onto the stored block (absent keys
  preserved; `type_name` cannot change).
- `get_node(id)`, `list_nodes(type_name?, status?)` — read blocks at any status.
- `publish_node(id)` (idempotent), `archive_node(id)`.

Composition (Editor role) — assemble blocks into pages and collections:

- `add_section(parent_id, child_id)` / `reorder_sections(parent_id, ordered_child_ids)` / `remove_section(parent_id, child_id)` — page sections.
- `add_item(parent_id, child_id)` / `reorder_items(parent_id, ordered_child_ids)` / `remove_item(parent_id, child_id)` — collection items.

`add_section` / `add_item` derive the parent and child types automatically — pass
only the IDs. Create a block with `create_node` before composing it.

### Field format hints

When a content type field carries a `smeldr_format` or `smeldr_description`
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

### Redirect management tools (Editor role required)

These tools are available when the site has `App.Redirects(db)` called at startup:

| Tool | Description |
|------|-------------|
| `create_redirect` | Creates or updates a redirect rule. `from` (must start with `/`), `to`, `code` (301/302/410, default 301), `is_prefix` (bool). Changes take effect immediately. |
| `list_redirects` | Lists all registered redirect rules (code-registered and database-saved). |
| `delete_redirect` | Deletes a redirect rule by `from` path. Changes take effect immediately. |

**Redirect rules:**
- `from` must start with `/`
- `code` 410 (Gone) requires `to` to be empty
- `is_prefix: true` makes `from` a path prefix — the unmatched suffix is appended to `to` at request time
- Changes are in-memory-immediate: no server restart required

### Page meta management tools (Admin role required)

These tools are available when the MCP server is started with `mcp.WithPageMeta(db)`:

| Tool | Description |
|------|-------------|
| `set_page_meta` | Upserts SEO overrides (title, description, og:image) for a URL path. `path` required (must start with `/`). Changes apply to the next request. |
| `get_page_meta` | Returns stored SEO overrides for a URL path. Returns empty fields when no override is stored. |
| `delete_page_meta` | Removes stored SEO overrides for a URL path. The path falls back to the content type's own `Head()` and global defaults. |
| `list_page_meta` | Lists all stored SEO overrides, ordered by path. |

**Page meta rules:**
- `path` must start with `/`
- `meta_title`, `meta_description`, and `og_image` are all optional; omit to clear that field
- `ListHeadFunc` takes priority over stored overrides on list pages
- Override is a no-op if `mcp.WithPageMeta(db)` was not called

### State flow tools

These tools are available when `App.Config().DB` is non-nil (any app with a database):

| Tool | Role | Description |
|------|------|-------------|
| `define_state_flow` | Admin | Register or update a state flow. Params: `name`, `type_name` (required), `states` (array of `{name, is_initial?, is_terminal?, suppresses_signals?}`), `transitions` (array of `{from, to, required_role?}`), `active_state` (optional string), `conflict_policy` (optional: `"reject"` or `"supersede"`). Idempotent — safe to re-run. Returns `{name, type_name, state_count, transition_count}`. (A186) |
| `transition_item` | Editor | Move a dynamic content item to a new state. Params: `type_name`, `slug`, `to_state`. Validated against the registered flow; returns -32001 if the transition is not permitted. |
| `get_valid_transitions` | Author | List legal target states for the item's current state. Params: `type_name`, `slug`. Falls back to the default flow when no custom flow is registered. Returns `{current_state, valid_transitions: []}`. |
| `list_items_by_state` | Author | List all items of a dynamic content type in the given state. Params: `type_name`, `state`. Returns `{type_name, state, items, count}`. |
| `create_signal` | Author | Insert a protocol signal into smeldr_signals with status "pending". Params: `sender`, `receiver`, `signal_type` (required); `task_ref`, `message`, `sequence` (optional). Returns `{id, slug, status}`. Requires smeldr_signals table (call `CreateOrchestrationTables` first). (A185) |
| `list_signals` | Author | List signals from smeldr_signals by receiver and status. Params: `receiver` (required), `state` (optional, default "pending"). Returns `{signals, count}` ordered by created_at ascending. Fail-open when smeldr_signals table is absent — returns empty list. (A185) |
| `get_goal_context` | Author | Retrieve a goal and all items linked to it via the relation graph (Decisions, Tasks, other Goals). Params: `goal_id` (required, e.g. `"T114"`). Returns `{goal, linked_decisions, linked_tasks, linked_goals}`. Returns -32001 when goal does not exist. Requires smeldr_goals table (call `CreateOrchestrationTables` first). (A199) |

**State flow rules:**
- `define_state_flow` calls `App.RegisterFlow` which uses INSERT OR IGNORE — re-running with the same name/type_name is safe; existing state and transition rows are preserved; `active_state` and `conflict_policy` are updated on every call
- `conflict_policy`: `"reject"` returns `ErrConflict` (-32603) when another item is already in `active_state`; `"supersede"` transitions conflicting items to "superseded" before proceeding; both policies fail-open on DB error (transition is not blocked)
- `type_name` is required for `define_state_flow` (the default flow is seeded at startup, not via MCP)
- `transition_item` calls `DynamicTypeRepo.SetStatus` which runs `validateTransition` internally — the same validation used by all status-change paths in the HTTP layer
- `required_role` on a transition is enforced when `App.Governance` is wired: the actor's token must hold a grant to that exact role name; fail-closed on error → -32001; when governance is not wired the field is stored but not enforced
- `get_valid_transitions` queries `smeldr_state_flows` directly for the custom flow registered for `type_name`, falling back to the default flow if none is registered
- The default flow (draft → scheduled/published/archived, scheduled → published, published → archived) is always present when a DB is configured

### Orchestration content types (A183)

Four built-in types for the architect/pilot protocol. Call `RegisterOrchestrationTypes(app, db)` at startup — after `CreateOrchestrationTables(db)` — to activate them. All four types are registered with `MCP(MCPRead, MCPWrite)`, so MCP tools are generated automatically.

| Type | Table | Initial state | Purpose |
|------|-------|---------------|---------|
| `Signal` | `smeldr_signals` | `pending` | Protocol message between a pilot and the architect |
| `Task` | `smeldr_tasks` | `backlog` | Work item in the task state machine |
| `Decision` | `smeldr_decisions` | `proposed` | Architectural decision with a re-evaluation cycle |
| `Amendment` | `smeldr_amendments` | `scoped` | Committed changeset linking a Task to its implementation |

Each type embeds `Node` and receives the standard auto-generated MCP tools (`create_signal`, `get_signal`, `list_signals`, `update_signal`, `publish_signal`, `archive_signal`, `delete_signal`, and the equivalent for `task`, `decision`, `amendment`).

**`LifecycleEvent` (renamed from `Signal` in A183)**

The Go type `smeldr.Signal` was renamed to `smeldr.LifecycleEvent` to free the `Signal` name for the orchestration content type above. All constant names are unchanged (`AfterCreate`, `AfterPublish`, etc.). If you have code that references `smeldr.Signal` as a type (not a constant), update it to `smeldr.LifecycleEvent`.
## Connection setup

See the smeldr.dev/mcp README for Claude Desktop, Cursor, and SSE configuration.
