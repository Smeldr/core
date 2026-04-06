# Next — README: MCP section update

## What and why

The MCP integration section in README.md predates token management (Decision 25)
and does not mention TokenStore, named revocable tokens, or the safety guard
introduced in Decision 26. A developer reading the README gets an incomplete
picture of how MCP authentication works in Forge today.

## Scope

In `README.md`, find the section `## MCP integration (forge-mcp)` and replace
the body with the following:

---

## MCP integration (forge-mcp)

✅ **Available**

`forge-mcp` is a separate module that wraps a `forge.App` and exposes its
content modules to AI assistants via the [Model Context Protocol](https://modelcontextprotocol.io).
Any MCP-compatible agent — Claude Desktop, Cursor, or others — can connect
to a running Forge site and create, update, and publish content directly.

Schema derivation, lifecycle enforcement, and role checks are all automatic —
no configuration beyond `forge.MCP(...)` on your existing modules.

```go
import forgemcp "github.com/forge-cms/forge-mcp"

app := forge.New(forge.MustConfig(forge.Config{
    BaseURL:    "https://mysite.com",
    Secret:     []byte(os.Getenv("SECRET")),
    DB:         db,
    TokenStore: forge.NewTokenStore(db, os.Getenv("SECRET")),
}))

app.Content(
    forge.NewModule((*Post)(nil), forge.At("/posts"), forge.MCP(forge.MCPWrite)),
)

mcpSrv := forgemcp.New(app)
app.Handle("GET /mcp", mcpSrv.Handler())
app.Handle("POST /mcp/message", mcpSrv.Handler())
```

Forge uses named, revocable bearer tokens for MCP authentication. Tokens
are stored as SHA-256 hashes — a database breach does not expose usable
credentials. `revoke_token` refuses to revoke the last active admin token,
so a single MCP call cannot lock you out of your own site.

For full setup instructions — token management, stdio proxy, Claude Desktop
and SSE configuration — see [forge-cms.dev/docs/mcp](https://forge-cms.dev/docs/mcp)
and `forge-mcp/README.md`.

---

## After implementation

- Delete this file
- Update `context/corepilot.md` in forge-cms/forge-architect — note as
  docs-only change, no version bump
