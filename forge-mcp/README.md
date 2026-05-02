# forge-mcp

> ✅ **Available** — MCP (Model Context Protocol) server for Forge apps.

`forge-mcp` wraps a `forge.App` and exposes its `MCP`-registered content modules as
MCP resources and tools, enabling Claude Desktop, Cursor, and any MCP-compatible AI
assistant to create, read, update, publish, and delete content through the Model
Context Protocol.

Schema derivation, role enforcement, and lifecycle rules are automatic — they use the
same code paths as the HTTP layer. There is no special MCP bypass.

---

## Quick start

```bash
go get forge-cms.dev/forge-mcp
```

```go
package main

import (
	"context"
	"os"

	forgemcp "forge-cms.dev/forge-mcp"

	"forge-cms.dev/forge"
)

// BlogPost is your content type — embed forge.Node and add your fields.
type BlogPost struct {
	forge.Node
	Title string `forge:"required" json:"title"`
	Body  string `forge:"required,min=50" json:"body"`
}

func main() {
	app := forge.New(forge.Config{
		BaseURL: "https://mysite.com",
		Secret:  []byte(os.Getenv("SECRET")),
	})

	posts := forge.NewModule((*BlogPost)(nil),
		forge.At("/posts"),
		forge.Repo(forge.NewMemoryRepo[*BlogPost]()),
		forge.MCP(forge.MCPWrite), // expose as MCP tools
	)
	app.Content(posts)

	srv := forgemcp.New(app)
	if err := srv.ServeStdio(context.Background(), os.Stdin, os.Stdout); err != nil {
		os.Exit(1)
	}
}
```

Build the binary:

```bash
go build -o myapp-mcp .
```

---

## Claude Desktop

Add to `~/Library/Application Support/Claude/claude_desktop_config.json`
(macOS) or `%APPDATA%\Claude\claude_desktop_config.json` (Windows):

```json
{
  "mcpServers": {
    "myapp": {
      "command": "/absolute/path/to/myapp-mcp",
      "env": {
        "SECRET": "your-hmac-secret"
      }
    }
  }
}
```

Replace `/absolute/path/to/myapp-mcp` with the path to the compiled binary.
Restart Claude Desktop to pick up the new server.

---

## Cursor

Add to `.cursor/mcp.json` in your project root (or `~/.cursor/mcp.json` globally):

```json
{
  "mcpServers": {
    "myapp": {
      "command": "/absolute/path/to/myapp-mcp",
      "env": {
        "SECRET": "your-hmac-secret"
      }
    }
  }
}
```

---

## SSE (remote / authenticated)

For remote access over HTTP, use the SSE transport:

```go
// In your main app or a dedicated MCP server binary:
mux := http.NewServeMux()
mux.Handle("/", forgemcp.New(app).Handler())
http.ListenAndServe(":9090", mux)
```

The SSE handler registers two routes:

- `GET /mcp` — establishes the event stream; the client keeps this connection open.
- `POST /mcp/message` — accepts JSON-RPC requests. Requires `Authorization: Bearer <token>`.

### Getting a token

Use `forge.SignToken` to mint a token with the same HMAC secret as your app:

```go
token, err := forge.SignToken(forge.User{
	ID:    "alice",
	Roles: []forge.Role{forge.Author},
}, []byte(os.Getenv("SECRET")), 0) // 0 = no expiry
```

### MCP client configuration (SSE)

```json
{
  "mcpServers": {
    "myapp-remote": {
      "url": "https://mysite.com/mcp",
      "headers": {
        "Authorization": "Bearer eyJ..."
      }
    }
  }
}
```

---

## MCPRead vs MCPWrite

Register each module with the operations you want:

```go
forge.MCP(forge.MCPRead)                    // read-only access
forge.MCP(forge.MCPWrite)                   // write-only access
forge.MCP(forge.MCPRead, forge.MCPWrite)    // full access
```

| MCP method                  | Requires   | Role required | What it does                                                  |
|-----------------------------|------------|---------------|---------------------------------------------------------------|
| `resources/list`            | MCPRead    | any           | List all Published items across all MCPRead modules           |
| `resources/templates/list`  | MCPRead    | any           | Return the URI template for each MCPRead module               |
| `resources/read {uri}`      | MCPRead    | any           | Fetch a single Published item by URI                          |
| `tools/call create_{type}`  | MCPWrite   | Author+       | Create a Draft item; returns the saved item as JSON           |
| `tools/call update_{type}`  | MCPWrite   | Author+       | Partially update fields by slug; non-supplied fields retained |
| `tools/call publish_{type}` | MCPWrite   | Author+       | Transition Draft → Published; idempotent                      |
| `tools/call schedule_{type}`| MCPWrite   | Author+       | Set Scheduled status and `scheduled_at` (RFC 3339)            |
| `tools/call archive_{type}` | MCPWrite   | Author+       | Set Archived status                                           |
| `tools/call delete_{type}`  | MCPWrite   | Author+       | Permanently delete an item                                    |
| `tools/call list_{type}s`   | MCPWrite   | Editor+       | List all items at any status; optional `status` filter        |
| `tools/call get_{type}`     | MCPWrite   | Editor+       | Get a single item by slug at any status                       |

**Tool naming:** type names are converted to `lower_snake_case`, with consecutive
uppercase letters treated as one word. `BlogPost` → `blog_post`,
`MCPDocument` → `mcp_document`. Full examples:
`create_blog_post`, `update_blog_post`, `publish_blog_post`.

**Resource URI format:** `forge://{prefix}/{slug}` — for example,
`forge://posts/hello-world` for a post at `/posts/hello-world`.

**Field names in `create_*` and `update_*` arguments:** use the JSON field name
(lowercase, respecting any `json:` tag). For `Title string` with no json tag,
the argument key is `"title"`. For `Tags string \`json:"tags"\``, the key is `"tags"`.

---

## Admin read tools

Each MCPWrite module also exposes two admin read tools that require **Editor or Admin** role.
Unlike `resources/read`, these tools return content at any lifecycle status — Draft, Scheduled,
Published, or Archived.

| Tool | Arguments | Returns |
|------|-----------|--------|
| `list_{type}s` | `status?: "draft"\|"scheduled"\|"published"\|"archived"` | `[]T` — all items, or filtered by status |
| `get_{type}` | `slug: string` | `T` — single item |

```go
// Example: list all posts regardless of status
// Tool name: list_blog_posts (type BlogPost → snake blog_post + s)

// Example: get a single post by slug
// Tool name: get_blog_post
```

**Tool naming:** follows the same snake_case rule as write tools. `BlogPost` → `blog_post`,
so the list tool is `list_blog_posts` and the get tool is `get_blog_post`.

**Return value:** the full struct including all `forge.Node` fields (`Status`, `PublishedAt`,
`ScheduledAt`, `CreatedAt`, `UpdatedAt`, `Slug`, `ID`).

---

## Lifecycle enforcement

**MCPRead exposes only `Published` items.** Draft, Scheduled, and Archived items
are invisible to `resources/list` and return MCP error -32001 on `resources/read`.
This enforcement is unconditional — it applies regardless of the caller's role and
cannot be disabled.

MCPWrite tools operate on items at any lifecycle stage. The full transition table:

```
Draft      → Published  (publish_{type})
Draft      → Scheduled  (schedule_{type}, requires scheduled_at RFC 3339)
*, *       → Archived   (archive_{type})
Published  → Published  (publish_{type} is idempotent — no error, no duplicate signal)
```

The Forge scheduler automatically transitions `Scheduled → Published` at the
configured time without any MCP call.

---

## Role enforcement

### stdio transport (ServeStdio)

`ServeStdio` runs with `forge.Admin` privileges. It is designed for **local,
trusted processes only** — any process that can write to stdin has full content
access. Do not expose the MCP binary's stdin to untrusted processes.

### SSE transport (Handler)

`POST /mcp/message` enforces authentication on every request:

- No `Authorization: Bearer <token>` header → HTTP 401
- Valid token, but role < `forge.Author` → MCP error -32001 (not HTTP 401;
  authentication succeeded, authorisation failed)
- All `tools/call` operations require `forge.Author` role or above

Tokens are minted and verified using the same HMAC mechanism as the Forge REST
API (`forge.BearerHMAC`). A token from `forge.SignToken(user, secret, 0)` is
valid for both the REST API and the SSE MCP transport — there is no separate
token format.

---

## Zero dependencies

`forge-mcp` has zero external dependencies. All MCP transport and JSON-RPC 2.0
protocol handling is implemented using the Go standard library only.

---

## Secret rotation (WithSecret)

`forgemcp.New(app)` automatically inherits the HMAC secret from `app.Config.Secret`.
You only need `WithSecret` during a secret rotation window — when you need to
accept tokens signed with a different secret than the one currently in `Config.Secret`:

```go
srv := forgemcp.New(app, forgemcp.WithSecret(oldSecret))
```

If the provided secret differs from `app.Config.Secret`, a warning is logged at
startup. Remove `WithSecret` once rotation is complete.
