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

These tags are hints only. Smeldr performs no validation based on them.

### Extending a framework-provided table

If you add a field to a type backed by a table Smeldr itself creates
(e.g. adding a custom column to `SiteConfig`), a fresh install picks it up
automatically only if you also add the column to the table's own DDL — an
already-provisioned instance needs its own migration. Use
`smeldr.EnsureColumn` for that, at your own application's startup:

```go
if err := smeldr.EnsureColumn(ctx, db, "smeldr_site_configs", "custom_field", "TEXT NOT NULL DEFAULT ''"); err != nil {
    log.Fatal(err)
}
```

Idempotent — safe to call on every boot. Works on SQLite and Postgres. There is no central registry of
columns Smeldr knows about — call `EnsureColumn` for a column you added,
the same way you'd call a `Create*Table` function.

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

### Default list order

`smeldr.DefaultListOrder(field string, desc bool)` sorts a module's list
results — both the MCP `list_*` tool and the HTTP `GET {prefix}` route —
by an exported string or integer field on `T`, ascending unless `desc` is
`true`. Without it, list results return in the repository's own natural
order (arbitrary, not guaranteed):

```go
smeldr.NewModule((*Task)(nil),
    smeldr.At("/tasks"),
    smeldr.Repo(repo),
    smeldr.DefaultListOrder("Priority", false), // lower number first
)
```

Core's own `Task`/`Goal` orchestration types are wired with
`DefaultListOrder("Priority", false)` (A267, T262).

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

### Event stream (EventStream + /_events/stream, v1.73.1+)

Opt-in, in-memory push of live content-lifecycle and state-flow-transition
events — for an agent/listener process that cannot receive an inbound webhook
(behind NAT, no public IP) and wants live events without polling.

```go
app.EventStream() // mounts GET /_events/stream; independent of app.Webhooks(...)
```

`GET /_events/stream?channel=<name>` (Author role, bearer auth) holds the
connection open and writes one NDJSON line per event — same payload shape
as an outbound webhook delivery (`{"id","event","timestamp","data"}`),
covering the same event names (`"{type}.created"` … `"{type}.transitioned"`,
`"signal.created"`), except that `signal.transitioned` (v1.106.0) and every
`amendment.*` event (v1.107.0) are webhook-only and never published to the
stream. An event is also not delivered to the connection whose own token
caused it (v1.107.0, matched on `User.ID`); `?include_own=true` opts back in,
and system-originated events are never suppressed. A `{"type":"ping"}` line
arrives every 25s while idle.
At-most-once delivery — no replay on reconnect.

**Channel subscription (v1.81.0+, A302; extended v1.89.2+, 01a0a683):**
`?channel=<name>` subscribes to one channel only — a `Task`/`Goal`'s own
`Band`, a `Decision`'s own `Scope`, or a `Signal`'s own `Receiver`, matched
verbatim (free-form string, not a fixed enum). `?channel=all`, or the
parameter omitted entirely, subscribes to everything — the default, so a
listener written before this feature existed keeps working unmodified.
Channel routing applies to every event those four types produce — both
`*.transitioned` (A302; not `signal.transitioned`, see above) and `*.created`/`*.updated` (01a0a683, which closed
a gap where the latter had been an unconditional broadcast to every
subscriber regardless of channel). Every generic content-module lifecycle
event has no such field and is always delivered to every subscriber
regardless of its requested channel (`Amendment` events are no longer
streamed at all, v1.107.0). No new role is
required for `?channel=all` — same access an Author-role token already
had. Event-*type* filtering (as opposed to channel) is still client-side
only. Route is absent (404) unless `EventStream` was called.
There is no MCP tool for this by design, same reasoning as `/_logs` — the
whole point is working when an agent has only this one HTTP connection to
rely on. Each token may hold at most 4 concurrent connections — a 5th
attempt gets 429, not a hang; a well-behaved listener holding its one normal
connection never approaches this. A raw-SQL creation path with no typed Go
item in hand (e.g. a bespoke MCP tool in another module writing directly
to its own table) won't fire the normal lifecycle hooks — call
`App.NotifySignalCreated(ctx, id, slug)` explicitly for a `Signal` created
that way (mirrors core's own `recordAuthorizationRequiredSignal`).

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
| `ENABLE_ORCHESTRATION` | boolean | wire orchestration types (Signal, Task, Decision, Amendment, Goal, Run); set `ENABLE_RELATIONS` for full `get_goal_context` traversal |
| `INSTANCE_NAME` | optional | source name embedded in `GET /packet/{type}/{slug}` responses (default: `smeldr-dogfood`); requires both `ENABLE_RELATIONS` and `ENABLE_ORCHESTRATION` |
| `ENABLE_REDIRECTS` | boolean | wire database-backed redirect management |
| `ENABLE_PAGE_META` | boolean | wire per-path SEO override store |
| `ENABLE_MEDIA` | boolean | wire local media upload and management |
| `ENABLE_SOCIAL` | boolean | wire Mastodon social publishing |
| `ENABLE_WEBHOOKS` | boolean | wire outbound webhook delivery |
| `ENABLE_EVENT_STREAM` | boolean | wire `GET /_events/stream` (opt-in agent event push); independent of `ENABLE_ORCHESTRATION` |
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

**Actor classification (`Job`/`Agent`/`Human` tags, A224, A418, D105):** three `Role`
constants classify *who* is acting, not *what* they may do. They are outside
the permission hierarchy and never grant or change a permission. Mint a
classified token with `TokenStore.CreateClassified(ctx, name, role, class, ttl)`
(`class` is `smeldr.Agent`, `smeldr.Job` or `smeldr.Human`), or with the
`create_token` tool's optional `actor_class` (mcp v1.48.0) or
`smeldr-cli token create ... --class` (cli v0.19.0); the token then carries
`[editor, agent]` and provenance records `ActorKind` `"agent"`, `"job"` or
`"human"`. **An actor with no tag records `"unclassified"`** (since core
v1.121.0): a token from `Create`/`CreateWithID`, a session or Basic-auth user.
It never claims to be a person. The Admin who mints a token attests its class:
. When several tags are
present the order is job, agent, human. **Before v1.121.0 an untagged actor
recorded `"human"` and no token could carry a tag; older provenance rows keep
that value and mean unclassified, not a verified person.** An issued token
cannot be classified afterwards: issue a new one and revoke the old one.
`list_tokens` shows each token's stored `ActorClass`; call
`smeldr.EnsureTokenActorClassColumn` at boot (a classified mint on a table
without the column is refused).

### Available tools (MCPWrite)

For each registered content type, these tools are available:

- `create_{type}` — creates a Draft
- `update_{type}` — partial update (absent fields preserved)
- `publish_{type}` — transitions to Published
- `schedule_{type}` — schedules for future publication (RFC3339 datetime)
- `archive_{type}` — transitions to Archived
- `delete_{type}` — permanent deletion

**Tool discovery:** if you found one verb for a type (e.g. `update_essay`) but
are unsure which other tools exist, call `list_type_tools({type_name: "essay"})`.
It returns the complete list of tool names for that type. Always available;
requires Author role.

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

### Token management tools

These tools are available when the site has `TokenStore` configured. `create_token`/
`list_tokens`/`revoke_token` require Admin role — they expose or change a token's role,
expiry, and revoked status. `lookup_token_names` requires only Author role — it reveals
nothing those three protect, only a name for an ID already visible elsewhere (see below).

| Tool | Role | Description |
|------|------|-------------|
| `create_token` | Admin | Issues a new named token with a given role and TTL; optional `actor_class` (`agent`, `job` or `human`, mcp v1.48.0, never changes permissions). Returns `token_id` alongside the raw token — pass it directly to `grant_role`. |
| `list_tokens` | Admin | Lists all tokens with name, role, expiry, revoked status, and `user_id` (the JWT identity this token was minted for — `null` for a token created before this field existed) |
| `revoke_token` | Admin | Revokes a token by ID — effective immediately |
| `lookup_token_names` | Author | Batch-resolves a list of `user_id` values (e.g. from `last_actor`, `RoleGrant.Grantor`, or a relation edge's `created_by`) back to each token's own `Name`. An ID with no matching token is simply absent from the result, never guessed. |

**Critical rules for token operations:**

- Always use `list_tokens` before `revoke_token` to confirm the ID
- `revoke_token` will refuse if the token is the last active admin token —
  create a replacement first
- A revoked token cannot be restored — revocation is permanent
- Never revoke a token without explicit instruction from the site owner
- `create_token` returns the plaintext token once — copy it immediately
  and deliver it through a secure channel. It cannot be retrieved again.
- **Creating a token grants it no governance role.** A freshly created
  token can authenticate but is authorized for nothing until an admin
  calls `grant_role` for it — see the next section. This is deliberate
  (D43): granting authority is always its own explicit act, never a
  byproduct of token creation. `create_token`'s response already includes
  the `token_id` `grant_role` needs, so this is normally a direct
  two-call sequence with nothing to decode in between.

### Governance grant management tools (Admin role required)

These tools are available when the site has governance wired via
`App.Governance(store)`. They are the only way to grant, list, or revoke
a governance role — a token's legacy `role` field (set at `create_token`
time) has no bearing on what a token is actually authorized to do once
governance is wired.

| Tool | Description |
|------|-------------|
| `grant_role` | Grants a role to a token. Requires `token_id` (the token's JWT user ID, not its `list_tokens` fingerprint) and `role`. Optional `scope_static` (array of `"type:id"`/`"type:*"` patterns) and `scope_anchor_id` for scoped roles. |
| `list_grants` | Lists governance grants. Omit `token_id` to list every grant on the instance; pass it to filter to one token. |
| `revoke_grant` | Revokes a grant by its own `id` (from `grant_role` or `list_grants`) — not by `token_id`. |

**Critical rules for grant operations:**

- `token_id` for `grant_role`/`list_grants` is the token's JWT user ID —
  **not** the SHA-256 fingerprint `list_tokens` returns. `create_token`'s
  response includes this value directly as `token_id` — use it as-is,
  there is no need to decode the raw token to recover it.
- `revoke_grant` takes the grant's own ID, never a `token_id` — you grant
  a *role*, and you revoke a *grant*.
- Every grant and revoke is recorded in an audit trail automatically —
  there is no way to opt out (D44).

**Rule-type stewardship** — granting standing authority over a rule-type
domain (e.g. "who owns `design-system`") uses these same tools with no new
mechanism: `grant_role` with `scope_static: ["RuleType:<name>"]` on a role
holding the `"steward"` operation. `RoleStore.StewardedRuleTypes`/
`StewardshipInbox` (Go API, no MCP tool yet) answer "what does this token
steward" and "what currently touches authority I steward" — see
[docs/REFERENCE.md](docs/REFERENCE.md#rule-type-stewardship-d63d64-decision-governance-modelmd-5).

**Per-Domain Decision authority** — granting one person ratify/supersede
authority over Decisions in a specific Domain (D71/D72's Domain/Area model)
also uses these same tools with no new mechanism: `grant_role` with
`role: "decision-domain-admin"` (auto-defined when `ENABLE_GOVERNANCE`,
`ENABLE_RELATIONS`, and `ENABLE_ORCHESTRATION` are all set) and
`scope_anchor_id` set to the target Domain item's own ID — real delegation,
narrower than the flat `admin` role, matching D68's own policy. See
[docs/REFERENCE.md](docs/REFERENCE.md#decision-domain-authority-d68d71d72-decide-decision-scope-role-policy).

**Decision review/ratify authority, instance-wide** — `grant_role` with
`role: "decision-steward"` (auto-defined when `ENABLE_GOVERNANCE` is set:
`Operations: ["review", "approve"]`, `ScopeMode: global`, `TrustLevel: 0`)
grants review/ratify authority over every Decision on the instance, not
scoped to one Domain. `admin`'s own `Operations` bundle no longer carries
`review`/`approve` (they are reserved for the Plan governance loop, not a
generic admin-tier grant, per `design/governance-model.md` §4, as of
01a0e3f9-2) — an `admin`-holding token that also needs to ratify/review
Decisions must hold `decision-steward` via an explicit grant.
See [docs/REFERENCE.md](docs/REFERENCE.md#decision-steward-role-grants-and-delegate-v1).

**Delegating a role to someone else, time-boxed** — `delegate_item` (Author+
role, `smeldr.dev/mcp`) lets any token hand off a *subset* of its own
authority to another token, scoped to one item, for a limited time (default
14 days, capped at 90) — unlike `grant_role`, which is Admin-only and hands
out standing authority with no expiry. Takes `token_id` (the delegate),
`role` (an existing role name), `operation` (the operation this delegation
is for — must be one of `role`'s own operations), `type`+`id` (the target
item), and an optional `expires_in_days`. Before granting, the tool checks
that the caller (the delegator) is itself authorized for *every* operation
`role` carries, against that same target — not just the named `operation` —
so a token can never hand out more authority than it actually holds. `role`
must also be a static-scope role (e.g. the built-in `item-approver`/
`item-reviewer`) — a global- or dynamic-scope role is refused, because its
own grants never consult the scoped `type:id` pair `delegate_item` writes,
so delegating one "for this item" would actually authorize the recipient
everywhere the role reaches.

`withdraw_delegation` (Author+, `smeldr.dev/mcp`) lets the member who
created a delegation undo it before it expires — `revoke_grant` stays
Admin-only, so this is the delegator's own equivalent. Takes `grant_id`
(from `delegate_item`'s own response or `list_grants`); refuses a grant
that isn't a time-boxed delegation (no `ExpiresAt`) or that this caller
didn't create.

`list_roles` (Author+, `smeldr.dev/mcp`) lists every role defined on the
instance — name, operations, and scope shape — read-only, useful for
checking what a role actually authorizes before granting or delegating it.

See [docs/REFERENCE.md](docs/REFERENCE.md#time-boxed-grants-expiresat-grants-and-delegate-v1-3132).

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
- Orchestration state transitions (`Task`/`Decision`/`Amendment`/`Goal`/`Signal`, via
  the `transition_item` tool) fire a separate `{type}.transitioned` event
  (e.g. `task.transitioned`) — subscribe to it, not `created`/`updated`/`published`,
  to observe orchestration state changes (a `Signal`'s own `signal.transitioned` and every `amendment.*` event reach
  webhooks only, not the event stream, since v1.106.0 and v1.107.0); a `Signal` created automatically when a
  role-gated automated transition is blocked fires `signal.created`, same as a
  human-created Signal

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
| `define_state_flow` | Admin | Register or update a state flow. Params: `name`, `type_name` (required), `states` (array of `{name, is_initial?, is_terminal?, suppresses_signals?, locked?, standing?}`; `standing` is `"holds"` or omitted), `transitions` (array of `{from, to, required_role?}`), `active_state` (optional string), `conflict_policy` (optional: `"reject"` or `"supersede"`). Idempotent — safe to re-run. Returns `{name, type_name, state_count, transition_count}`. (A186) |
| `transition_item` | Editor | Move an item — dynamic content or a compiled type (e.g. `Signal`, `Task`, `Decision`) — to a new state. Params: `type_name`, `slug`, `to_state`, optional `reason`. Validated against the registered flow; returns -32001 if the transition is not permitted, or -32602 if the target transition requires a reason and none was given. (D49, A256/T235) |
| `get_valid_transitions` | Author | List legal target states for the item's current state — dynamic or compiled. Params: `type_name`, `slug`. Falls back to the default flow when no custom flow is registered. Returns `{current_state, valid_transitions: []}`. (D49) |
| `list_items_by_state` | Author | List all items of a content type — dynamic or compiled — in the given state. Params: `type_name`, `state`. Returns `{type_name, state, items, count}`. (D49) |
| `state_since`, `state_reason` keys | Author | Since mcp v1.47.0 (A415) each Task and Goal item of `get_task`/`list_tasks`/`get_goal`/`list_goals` and `list_items_by_state` also carries `state_since` (RFC3339 UTC, second resolution) and, when given, `state_reason`: when it entered its current state and why, from provenance (`App.ItemsStateSince`, core v1.120.0). Absent `state_since` means unknown, not "has not moved". The actor is never on this surface. |
| `get_item_standing` | Editor | Read one item's standing (D100): `holds`, `ceased` or `none`, or no `standing` key when the type has none. Params: `type_name`, `slug`. A failed read of the standing is an error. Since mcp v1.46.0 (A403); the typed `get_<type>`/`list_<type>`, `get_content`, `list_content`, `list_items_by_state` and `transition_item` results also carry a separate `standing` key for a type that has standing (an absent key means no standing for the type or a failed lookup). |
| `create_signal` | Author | Insert a protocol signal into smeldr_signals with status "pending". Params: `sender`, `receiver`, `signal_type` (required); `task_ref`, `message`, `sequence`, `subject_type`, `subject_id` (optional — the general way any signal pattern points at a real item, D86). Returns `{id, slug, status}`. Requires smeldr_signals table (call `CreateOrchestrationTables` first). (A185, A352) |
| `list_signals` | Author | List signals from smeldr_signals by receiver and status. Params: `receiver` (required), `state` (optional, default "pending"). Returns `{signals, count}` ordered by created_at ascending. Fail-open when smeldr_signals table is absent — returns empty list. (A185) |
| `get_goal_context` | Author | Retrieve a goal and all items linked to it via the relation graph (Decisions, Tasks, other Goals). Params: `goal_id` (required, e.g. `"T114"`). Returns `{goal, linked_decisions, linked_tasks, linked_goals}`. Returns -32001 when goal does not exist. Requires smeldr_goals table (call `CreateOrchestrationTables` first). (A199) |
| `get_sweep_run` | Author | Read the most recent structural sweep run recorded for a detector, via `SweepRunStore.Last`. Params: `detector` (required, e.g. `"structural"`). Returns `{detector, wired: false}` when no run has been recorded, or `{detector, wired: true, ran_at, walked, flagged}` when one exists. `SweepRunStore` itself has no HTTP/MCP surface in core (A279) — this tool is smeldr.dev/mcp's own remote-read layer on top of it. (A298) |
| `get_stewardship_inbox` | Author | Retrieve the calling token's own rule-type stewardship inbox, via `RoleStore.StewardshipInbox`. No params — always the caller's own token (`ctx.User().ID`). Returns `{rule_types, decisions, rules, stubs}`, all empty (not an error) when the token holds no stewardship grants. See [docs/REFERENCE.md](docs/REFERENCE.md#rule-type-stewardship-d63d64-decision-governance-modelmd-5) for how a stewardship grant itself is created. (D63/D64 §5, A310, smeldr.dev/mcp) |
| `get_check_status` | Author | Read the most recently recorded Check result for a subject, via `CheckStore.Last`. Params: `subject_type`, `subject_id` (both required, e.g. `"Decision"`/the Decision's own ID). Returns `{found: false, subject_type, subject_id}` when Check has never run for this subject, or `{found: true, subject_type, subject_id, rule_type, ran_at, match_type, match_id, match_name, sentence}` when it has. See [docs/REFERENCE.md](docs/REFERENCE.md#authority-check-decision-governance-modelmd-4) for Check itself. (A312, smeldr.dev/mcp) |

**State flow rules:**
- `define_state_flow` calls `App.RegisterFlow`, which upserts the flow row keyed on `type_name` (not `name`) — re-running with the same `type_name` is always safe, including when `name` has changed: the existing row is updated in place, not duplicated (T268 fixed a real production incident where a rename orphaned the old row instead). `active_state`, `conflict_policy`, and `description` are updated on every call. Existing state rows are kept by identity (keyed on `flow_id`+`name`, never duplicated), but since v1.113.0 (A399) every flag on them (`is_initial`/`is_terminal`/`suppresses_signals`/`locked`/`standing`) updates to the registered value on every call, logged at Warn for `locked` and `suppresses_signals` and at Info for the rest; so a call that leaves out a flag, `active_state` or `conflict_policy` resets it; always send the full definition. Since mcp v1.45.0 (A400) `define_state_flow` carries `locked`, `standing`, `active_state` and `conflict_policy` and refuses a `type_name` that is a Go-defined type (its flow is set in code). Existing transition rows are preserved by identity (keyed on `flow_id`+`from_state`+`to_state`, never duplicated), but a transition's own `required_role`/`required_reason`/`strict` values now update on every re-registration, matching the last call's values — re-running `define_state_flow` with a changed role/reason/strict gate actually takes effect (previously silently frozen at first insert; fixed after a real production incident where a role-gate change never took effect on any pre-existing database)
- `conflict_policy`: `"reject"` returns `ErrConflict` (-32603) when another item is already in `active_state`; `"supersede"` transitions conflicting items to "superseded" before proceeding; both policies fail-open on DB error (transition is not blocked)
- `type_name` is required for `define_state_flow` (the default flow is seeded at startup, not via MCP)
- `transition_item` calls `App.TransitionItem` (D49), which resolves whichever table stores the item: a runtime-defined type delegates unchanged to `DynamicContentRepo`/`SetStatus`; a compiled type (e.g. an orchestration type) performs a raw status update against its own table. Both branches run `validateTransition` — the same validation every status-change path in the HTTP layer uses.
- `slug` on `transition_item`, `get_valid_transitions`, and every `Module[T]`-generated tool (`get_{type}`/`update_{type}`/`publish_{type}`/`schedule_{type}`/`archive_{type}`/`delete_{type}`) also accepts a type's own human-facing identifier, when it has one, as a fallback after a slug lookup misses: `Task.TaskID` ("T203"), `Goal.GoalID`, `Decision.DecisionNumber`, `Amendment.AmendmentNumber` — so `get_task("T203")` works exactly like `get_task("the-real-slug")` (A262, T253). `Signal` has no such identifier and stays slug-only. The response always reports the item's own real slug, never the identifier you passed in.
- The six `Module[T]`-generated tools above (not `transition_item`/`get_valid_transitions`, which use a separate resolution path in `state.go`, unchanged here) also accept a real `Node.ID` — tried after the slug lookup, before the human-facing identifier — so `get_task("<the-real-uuid>")` also works, for every type including `Signal` (`Module.resolveItem`, A266/T214). Previously a real ID silently resolved nothing despite `identArg`'s own doc comment implying it was supported.
- For a compiled type, `App.TransitionItem`'s raw status update does **not** advance `Node.Rev` — the same choice `SetStatus`/`applyConflictPolicy`'s own supersede path already make for their raw status updates. A concurrent `Save` holding the item's pre-transition rev still satisfies `Save`'s CAS check and will silently overwrite the status change with whatever status its own in-memory item carries.
- `transition_item` does **not** fire a compiled type's `AfterPublish`/`AfterArchive`-class signal-bus hooks (`module.go`'s `notifyAfter`) — only the `TransitionTrigger` pipeline (`fireAsyncTriggers`, A240) fires, exactly as it already does for dynamic content today. These are two different, pre-existing mechanisms in this codebase; `transition_item` extends the state-flow-transition path symmetrically, it does not introduce a new asymmetry.
- `transition_item`'s response includes `last_actor` (D78, A347) — the calling actor's own ID (empty when the caller has no `smeldr.Context` identity), also persisted onto the transitioned row and exposed via `get_task`/`list_tasks`/etc. and `get_content`/`list_content` for the six orchestration types and every dynamic content type. A compiled type's table that predates this column (any table outside the six orchestration types) is unaffected — the write fails open, same as before this Amendment.
- `required_role` on a transition is enforced when `App.Governance` is wired: the actor's token must hold a grant to that exact role name; fail-closed on error → -32001; when governance is not wired the field is stored but not enforced
- `required_reason` on a transition (`Transition.RequiredReason`, T149/A220) is enforced unconditionally (fail-closed, no governance dependency): a transition with the flag set returns `ErrBadRequest` when no reason is supplied. **`transition_item` cannot currently satisfy this gate**, for either a dynamic or a compiled type — `App.TransitionItem` always passes an empty reason. Only `DynamicTypeRepo.SetStatusWithReason` (Go API, not yet exposed via MCP) can supply one. Do not register a `RequiredReason: true` transition on a type you intend to drive via `transition_item` until the MCP tool gains a `reason` parameter.
- `get_valid_transitions` and `list_items_by_state` resolve a compiled type's current status via its module's own `MCPGet`/`MCPList` — no `smeldr_dynamic_content` involvement, no new core API
- `get_valid_transitions` queries `smeldr_state_flows` directly for the custom flow registered for `type_name`, falling back to the default flow if none is registered
- The default flow (draft → scheduled/published/archived, scheduled → published, published → archived) is always present when a DB is configured

### Orchestration content types (A183)

Six built-in types for the architect/pilot protocol and headless
automation (M3, D38). Call `RegisterOrchestrationTypes(app, db)` at
startup — after `CreateOrchestrationTables(db)` — to activate them. All
six types are registered with `MCP(MCPRead, MCPWrite)`, so MCP tools are
generated automatically.

| Type | Table | Initial state | Purpose |
|------|-------|---------------|---------|
| `Signal` | `smeldr_signals` | `pending` | Protocol message between a pilot and the architect |
| `Task` | `smeldr_tasks` | `backlog` | Work item in the task state machine |
| `Decision` | `smeldr_decisions` | `proposed` | Architectural decision with a re-evaluation cycle |
| `Amendment` | `smeldr_amendments` | `scoped` | Committed changeset linking a Task to its implementation |
| `Goal` | `smeldr_goals` | `open` | Work goal, linked to Decisions and Tasks via the relation graph |
| `Run` | `smeldr_runs` | n/a — no state flow | One mechanical episode of headless automated work (D38, M3) |

Each type embeds `Node` and receives the standard auto-generated MCP tools (`create_signal`, `get_signal`, `list_signals`, `update_signal`, `publish_signal`, `archive_signal`, `delete_signal`, and the equivalent for `task`, `decision`, `amendment`, `goal`, `run`).

**`Run` has no state flow — its `status` field is inert (D38).** Unlike
the other five types, `Run`'s real state lives in its own `lease_holder`
and `outcome` fields, not `status`. `publish_run`/`archive_run`/
`schedule_run` exist (the standard tool set every `MCP(MCPWrite)` type
gets) but do not gate anything — no code path validates a `Run` transition
because no flow is registered for it. Every `Run` row stays `Draft` for
its entire life; this does not hide it from `list_runs`/`get_run`, since
`Run` is read the same way every other type is. Do not build logic that
gates on `Run.status` — read `lease_holder`/`outcome` instead.

**`Decision` ratify/supersede requires the `admin` role (D34, A234).** `Decision`'s `proposed → ratified` and `ratified → superseded` transitions are gated: the caller's token must hold a grant for the `admin` role (via `App.Governance`), or the transition is rejected — including when governance isn't wired or no actor is present in context, both of which used to silently allow the transition through. No MCP tool can currently move a `Decision` to `ratified` or `superseded` at all: `publish_decision`/`archive_decision` only ever target the built-in `Published`/`Archived` states, neither of which exists in `Decision`'s own flow. Today this transition is only reachable via a direct HTTP `PUT /decisions/{slug}` request with a changed `status` field.

**`Decision` gained `rule_type`/`reversibility` fields (A304, decision-governance-model §3).** Set them via the standard `update_decision` MCP tool like any other field — no new tool. **`scope` already covers "affected surface"**: do not also set a `surface` field, there isn't one; `scope` (existing) is what §3 calls "affected surface." `rule_type` is a free-text name compared via `RuleTypeRank` (Go API only, no MCP tool yet — an organization first declares its own ordering with `SetRuleTypeOrder`). `reversibility` should be one of `reversible`/`conditionally-reversible`/`irreversible`/`reversibility-disputed` (`smeldr.Reversibility`'s own constants) — nothing validates this today, and nothing currently reads, ranks, or enforces either field: this is a pure data model, not yet wired to any Check/gate.

**`Amendment` gained a `body` field (A305, D67).** `create_amendment`/`update_amendment` now accept `body` (markdown) alongside the existing `summary` (still a one-liner). D67 makes recording a new Amendment via `create_amendment` the primary, authoritative act going forward — `smeldr/core`'s own git-file record (`DECISIONS.md`/`decisions/recent.md`) is frozen at A304/A305 and accepts no new entries. Write the same depth of rationale in `body` that a `decisions/recent.md` entry used to hold — this is now the only place it lives. A new Amendment record should be driven through its own flow to `merged` in the same action it's created (the work it represents is already shipped by the time it's recorded), not left at the initial `scoped` state.

**`update_decision`/`update_amendment` (and the generic `update_<type>` MCP tools generally) now reject a content edit once the item is in a locked state (Amendment decision-content-mutable-after-ratification).** A `Decision` in `ratified`, `pending-re-evaluation`, `superseded`, or `archived` — every state except `proposed` — rejects `update_decision` with an error wrapping `ErrConflict` (HTTP 409), even for an `admin`-role caller: this is a state gate with no override, not an authorization gate. Same for an `Amendment` in `merged` or `rejected` (`committed` still accepts edits, matching the create-then-drive-through-in-one-action pattern D67 already uses). **This is expected behaviour, not a bug** — retrying the same edit will never succeed. The correct action once a `Decision`/`Amendment` is locked is to create a *new* item that supersedes it (`DECISIONS.md`'s own documented model: "revisions to an existing decision require a new entry that supersedes the original"), never to keep retrying `update_decision`/`update_amendment` on the locked one. `publish_decision`/`archive_decision`/`transition_item` (moving the item to a *different* state) are unaffected — only field-content edits on an item that stays in the same locked state are blocked.

**`LifecycleEvent` (renamed from `Signal` in A183)**

The Go type `smeldr.Signal` was renamed to `smeldr.LifecycleEvent` to free the `Signal` name for the orchestration content type above. All constant names are unchanged (`AfterCreate`, `AfterPublish`, etc.). If you have code that references `smeldr.Signal` as a type (not a constant), update it to `smeldr.LifecycleEvent`.

**`Signal`'s `expired` state is reachable by a real scheduled mechanism, not only by hand (A374).** `App.ExpireSignals` (Go API only, no MCP tool) moves `pending`/`read` Signals older than a configurable age (default 14 days) to `expired` — never deleted, Signals are Trace history. Three `signal_type` values are always excluded regardless of caller config, since they represent a standing condition that closes only by being answered: `authorization-required`, `review-requested`, `conflict-detected`. If you build your own dynamic content types that create Signals with a `signal_type` meaning "a real open condition, not yet resolved," pass it in `SignalExpiryConfig.ExcludeTypes` — it's added to, never a replacement for, the mandatory three.

**`Task` gained a second, shorter door to `done`: `active` → `done` with a required reason (D88).** The original `commit-reviewing` → `done` path (full plan/commit cycle) is unchanged and still the normal route. The new `active` → `done` transition is for work that concludes without ever going through a build at all — a review, a sign-off, a copy pass, an investigation, or a design discussion — and closes the Task directly from `active`. It requires a reason, same as `resolved` (D58), but means something different: `resolved` means the underlying need was met by work *outside* this Task's own tracked work; `active` → `done` means this Task's own work *is* what closed it, just not through a build. Only reachable from `active` — `backlog` → `done` stays illegal, a Task must be claimed first, matching every other terminal-state door in this flow. No new MCP tool: use `transition_item` with `to: "done"` and a `reason`, same as any other gated transition.

**`Task` gained three return/block transitions (A391).** `commit-reviewing` → `implementing` (reason required) lets a reviewer return a Task to the implementer after a failed review; before this, `commit-reviewing` could only reach `done`. `implementing` → `blocked` (reason required) lets the implementer stop mid-build to ask instead of guess: the reason states what is being asked. `blocked` → `implementing` resumes the build; the existing `blocked` → `active` is unchanged. `blocked` does not remember where it was entered from, so a block raised mid-build should resume to `implementing`, not `active` (which would re-enter planning). None of the three requires an operation, matching every other Task transition: who may return a Task is governed by the process, not the flow. No new MCP tool: use `transition_item` with the target state and a `reason`.

**State changes now write provenance (A392).** When `App.Provenance` is wired, every successful state change made through `transition_item` (`App.TransitionItem`/`TransitionItemWithReason`, and the new `App.TransitionItemVia(ctx, surface, ...)` they delegate to), `DynamicTypeRepo.SetStatus`/`ScheduleContent` and `POST /_content/{type}/{id}/status` writes a `ProvenanceRecord` (verb `transition`, from and to state, actor and kind, surface, reason). A rejected transition records nothing, a failed write is logged and never fails the transition. **Not backfilled**: earlier transitions have no record. The older methods record an empty surface; call `TransitionItemVia` with `"mcp"`, `"http"`, `"cli"` or `"trigger"` to name it. **Nothing is recorded unless provenance is enabled**: the example server wires it only when `ENABLE_PROVENANCE` is set, so check that before expecting history. `CreateProvenanceTable` also adds an index on `(subject_type, subject_id)`; a table you created from your own DDL needs it added by hand. `POST /_content/{type}/{id}/status` records the authenticated caller as actor since v1.109.1 (it recorded none in v1.109.0). **v1.109.1 is a security fix on that endpoint (A393):** it used to pass no actor to the transition gate, so any Editor could perform a non-Strict `RequiredOperation` transition on a runtime-defined type without holding the operation, and a `Strict` one could never succeed (500). It now enforces the operation like every other transition path (403 without it, 400 when a reason is required). A client that relied on the old 200 for a gated transition now needs the grant. **Since v1.110.0 two more state changes are recorded (A395):** an item moved to `superseded` as the side effect of another item's transition under `ConflictSupersede` (actor of the triggering caller, that transition's surface, reason `superseded by <Type> <id>`; `last_actor` is stamped too), and every Signal expired by `App.ExpireSignals` (actor kind `job`, actor `signal-expiry-sweep`, surface `trigger`). No event fires for a superseded item, so a client that relies on the event stream sees it change only when it re-reads. **Since v1.111.0 (A396)** that supersede also asserts a `supersedes` relation edge winner to loser (by item ID, triggering actor as `created_by`), but only when `App.Relations` is wired and a `supersedes` relation kind permitting `Type -> Type` is registered; the orchestration kind permits Decision to Decision only, so a customer-defined type needs its own kind (an `UpsertKind` with empty or matching `TypePairs`, keeping the Decision pair). Without it the item is still superseded and recorded, without an edge, and one Info line per type says so.

**Standing is explicit (D100, v1.112.0).** A flow `State` may declare `Standing: smeldr.StandingHolds` ("an item in this state is in force"; any other non-empty value is rejected by `App.RegisterFlow`). Each item of a type that has such a state stores its own standing in `smeldr_standing`: `holds`, `ceased`, `none` (never held; also an item with no row) or `not recorded`. The code that changes the item's state is the only writer: entering a state that holds stores `holds`, leaving one for an untagged state stores `ceased`, moving between untagged states changes nothing. Every change is also a `standing-began`/`standing-ended` provenance event with the transition's actor (when `App.Provenance` is wired), so ratifying a Decision now reads as two entries. Smeldr's own tags: Decision `ratified` and `pending-re-evaluation`, Amendment `merged`; Signal, Task and Goal have no standing. Read it with `ItemStanding(ctx, db, typeName, id)` / `CountStanding`, or the `standing` field of a context packet; it is separate from the governed state name. Existing items are not backfilled except by `MigrateStanding`, which classifies them once per type from the flow graph as registered when it ran (an item in `proposed` is `none`, one in `superseded` is `ceased`, one in `archived` is `not recorded`) and never guesses. Retagging a flow never recomputes stored standings. `App.CheckStandingDrift` only reports a stored standing that no longer matches the state's tag. `RegisterFlow` updates the standing tag of an existing state row; the other state flags still do not update an existing row. `define_state_flow` (mcp) does not yet pass `standing` through; that comes in a later mcp release. For a page of items call `smeldr.TypeHasStanding` once and `smeldr.ItemStandings` once (v1.114.0, one query per 400 ids; an item with no stored row is `none`, an empty map means the type has no standing) instead of `ItemStanding` per item.

**ConflictSupersede order and atomicity (A398, v1.112.1).** Under `ConflictSupersede` the entering item's own write now comes first and the items it supersedes after it (before, the losers were superseded first, so a failed write of the entering item left the type with no active item). `App.TransitionItemVia` and runtime-defined types run both in one transaction when the database handle supports `BeginTx` (`core/pgx` from adapter v0.3.0; a Postgres failed write fails the whole transition, and the `last_actor` fail-open runs under a savepoint since v1.119.1); the `Module` lifecycle methods (`MCPPublish`, `MCPSchedule`, `MCPArchive`) cannot, because their own write goes through the module's repository, so a failed second write there leaves two items in the active state (logged at Error), never none. Provenance, standing and the `supersedes` edge of a superseded item are recorded after the commit, only for items that really moved. The policy is still not enforced on `Module`'s HTTP PUT status change or the scheduler's publish, and two concurrent winners can both pass the check. **Concurrent winners (A405, v1.115.1).** Transitions of one type into its `ActiveState` are serialised per type within the process (the policy reads, the winner write and the losers writes happen under one lock), so two simultaneous winners can no longer both become active; the lock is in-process only, and the policy is enforced on SQLite only for now. **Enforced paths (A406, v1.116.0).** The policy also applies on a `Module` HTTP PUT that changes status (a rejected PUT is 409, nothing saved), on the scheduler publish (a rejected item stays `scheduled`, retried every tick, one Warn per item), on `DrainEvalQueue` (skipped and logged) and on `DynamicTypeRepo.ScheduleContent`; every status writer in core applies it or cannot reach the active state. **Databases (A407, v1.116.1).** The state machine is enforced on SQLite only: on any other database (including Postgres through `core/pgx`) a registered flow is stored but never consulted, so a transition's `RequiredOperation` gate and `Strict` are NOT checked (an authorization gap), `State.Locked` locks nothing, async triggers do not fire and `ConflictPolicy` is not applied. `RegisterFlow` logs a Warn once per process saying so, and a context that has ended is now an error (`ErrInternal`) where it used to silently skip the gate; see REFERENCE, State flows.

**State flows on Postgres (v1.118.0, D103).** The state machine (transition rules and role gates, `RequiredReason`, `Strict`, `Locked`, `SuppressesSignals`, async triggers, `ConflictPolicy`) is enforced on every supported database. Before v1.118.0 it was enforced on SQLite only: on Postgres through `core/pgx` a flow was stored and never consulted, so a transition's role gate was not checked. From v1.118.0 it is, with no opt-out: a transition that passed unchecked can now be refused (403, 409 or 400), `Locked` content is read-only, triggers fire and `ConflictPolicy` applies. `EnsureColumn` and the boot migrations now work on Postgres. Since v1.119.0 `ConflictReject` and `ConflictSupersede` are also exclusive across several application processes on one Postgres database through `core/pgx` (an advisory lock the adapter provides through `AcquireLock`; measured: two processes moving two items into the active state at the same moment both won before, exactly one wins now). A handle without `AcquireLock`, SQLite and a wrapper that hides the method keep the in-process lock only.

**Dynamic content on Postgres (v1.119.2).** Runtime-defined content types now work on Postgres through `core/pgx` (reads, updates, status changes, the `Locked` check, the conflict policy): a `json.RawMessage` field of any struct scanned through `Query` or `SQLRepo` is scanned from a string or bytes, and `SeedBlockTypeSchemas` no longer uses SQLite-only SQL. Before, every read of a dynamic item failed with "unsupported Scan, storing driver.Value type string into type *json.RawMessage". `PageMetaStore` (SEO overrides) works on Postgres since v1.119.3.

### Reference types and the structural sweep (A372)

A dynamic content type an application registers via `define_content_type`
can be designated a **reference type** (Go API only, no MCP tool —
`RelationStore.RegisterReferenceType(ctx, typeName)`) when it holds
lookup/reference data rather than editorial content — data other items
point at rather than content with its own publication lifecycle. `domain`
and `area` (D71/D72) are registered as reference types by core itself, at
the same point it registers their relation kinds.

This changes only how the background structural sweep (`App.SweepStructural`)
decides whether an edge's target still counts as "alive": for an ordinary
dynamic content type, a relation edge to a `draft` (never-published) item is
invalidated, since unpublished editorial content isn't real yet. For a
reference type, a `draft`/`published`/`scheduled` row all count as alive —
only `archived` counts as gone. If you create a Domain or Area item via `create_domain`/`create_area` and
never call `publish_domain`/`publish_area` on it, `belongs_to_domain`/
`belongs_to_area` edges pointing at it survive the sweep; this was not true
before A372, and a Decision's Domain-scoped governance access could silently
disappear the first time the sweep ran against a never-published Domain.

### Authority mechanism (A303)

Two built-in types for the decision-governance-model's Authority
mechanism — the first subtype of the conceptual Authority supertype
(Decision/Rule/Principle/Standard/Precedent) plus a cheap pointer type for
un-modeled source material. Call `RegisterAuthorityTypes(app, db)` at
startup — after `CreateAuthorityTables(db)` — to activate them. Both types
are registered with `MCP(MCPRead, MCPWrite)`, so MCP tools are generated
automatically, same as every orchestration type above.

| Type | Table | Initial state | Purpose |
|------|-------|---------------|---------|
| `Rule` | `smeldr_rules` | `draft` | A fully modeled, governing rule |
| `AuthorityStub` | `smeldr_authority_stubs` | `stub` | A cheap pointer into a source document, not yet fully modeled |

Each type embeds `Node` and receives the standard auto-generated MCP tools (`create_rule`, `get_rule`, `list_rules`, `update_rule`, `publish_rule`, `archive_rule`, `delete_rule`, and the equivalent for `authority_stub`).

**`Rule` does not use `publish_rule`/`archive_rule` to reach its real states.** `Rule`'s own flow is `draft` → `active` → `retired` — none of those are the built-in `Published`/`Archived` states, so use `transition_item` (type `Rule`) to move a Rule through its real lifecycle, the same pattern `Decision` already requires for `ratified`/`superseded`.

**Converting a stub to a Rule is a manual, two-step act, not a tool.** Create the full `Rule` via `create_rule`, then `assert_relation` a `materializes` edge from the `AuthorityStub` to the new `Rule`, then `transition_item` the stub to `converted`. No MCP tool automates this sequence yet — deliberately, per the design's own anti-big-bang instruction (automate only once a real second/third conversion shows what's actually repeated).

### Check mechanism (A312, decision-governance-model.md §4)

Check is an enforced *precondition* on a `Decision`'s `proposed → ratified`
transition — advisory and recording, not a gate: it never blocks
ratification, even when it finds an existing Rule or AuthorityStub sharing
the Decision's `RuleType`. Enable it once, on the server: `app.Check(smeldr.NewCheckStore(db))` (after `CreateCheckTable(db)`, alongside
`CreateAuthorityTables(db)`). No MCP tool exposes Check directly — it runs
automatically, transparent to every caller, on both the HTTP `PUT` and
`transition_item` paths that can move a `Decision` to `ratified`. See
[REFERENCE.md](docs/REFERENCE.md)'s "Authority Check" section for the full
`CheckRecord`/`CheckStore` API.

## Connection setup

See the smeldr.dev/mcp README for Claude Desktop, Cursor, and SSE configuration.
