# Next task for corepilot

## Decision 28 — forge-cli: operator CLI

### Plan approval and go-ahead

The plan is approved. Proceed with implementation.

**Token management:** Use option (a) — token commands call the MCP JSON-RPC
endpoint (`FORGE_MCP_URL`, defaulting to `{FORGE_URL}/mcp/message`). No
Amendment to forge core required.

**Read-modify-write note:** For `publish`, `unpublish`, `archive`, and `update`,
the CLI does a GET-then-PUT round-trip. Test this pattern early and stop to raise
an Amendment if any fields are lost or mishandled through the round-trip —
particularly `PublishedAt`, `ScheduledAt`, and array fields like `Tags`.

**All other plan details are approved as stated.**

---

### What and why

Forge needs a distributable operator CLI (`forge-cli`) that lets operators manage
content and tokens from the terminal. Today, content lifecycle operations require
a running AI client via MCP. If MCP or Claude is unavailable, the operator has no
fallback. The CLI closes this gap. MCP remains the primary authoring channel; CLI
is the fallback and operator tool.

### Decision to record

Record Decision 28 in `DECISIONS.md` (index row) and append the full decision
body to `decisions/phase2.md`.

Decision 28 summary for the index:

> forge-cli: operator CLI as a separate submodule (`forge-cli/`). HTTP client
> against the running Forge instance. Subcommand structure: `forge-cli <type>
> <verb> [slug]`. Covers content CRUD, lifecycle, token management, and
> diagnostics. Zero new dependencies. AGPL licensed.

### Module to create

Create `forge-cli/` as a new submodule in forge-repo with its own `go.mod`.
Follow the same pattern as `forge-mcp/`. Starts at `v0.1.0`.

### Command structure

Type is a subcommand. Every command and subcommand responds to `--help`.

**Content commands** — `<type>` is e.g. `post`, `docpage`:

```
forge-cli <type> create --from <file>              # Author+
forge-cli <type> update <slug> --from <file>       # Author+
forge-cli <type> delete <slug>                     # Editor+
forge-cli <type> publish <slug>                    # Author+
forge-cli <type> unpublish <slug>                  # Author+
forge-cli <type> archive <slug>                    # Author+
forge-cli <type> list [--status draft|published|archived]  # Editor+
forge-cli <type> get <slug>                        # Editor+
```

**File format for `--from`** (YAML frontmatter + markdown body):

```
---
title: My Post
slug: my-post
tags: [go, forge]
---
Markdown body here...
```

Frontmatter parser: stdlib only, no YAML library. Key: value per line.
`[a, b, c]` → `[]string`. No multi-line values, no nested structures.
Body after `---` separator becomes the `body` field.

**Token commands** — call MCP JSON-RPC endpoint:

```
forge-cli token create <name> <role> <ttl>   # Admin
forge-cli token list                          # Admin
forge-cli token revoke <id>                   # Admin
```

**Diagnostics:**

```
forge-cli status   # calls /_health, prints version and connectivity
```

### Configuration

```
FORGE_URL      — base URL of the running Forge instance
FORGE_TOKEN    — bearer token with appropriate role
FORGE_MCP_URL  — MCP endpoint (default: {FORGE_URL}/mcp/message)
```

Loaded from environment or `.forge-cli.env` file in the working directory.

### Constraints

- Zero new dependencies — stdlib only (`net/http`, `flag`, `encoding/json`)
- No direct database access — always HTTP
- AGPL licensed
- All commands must have `--help` output with usage, flags, and examples
- Gets its own `forge-cli/CHANGELOG.md`
- `go.work` gains `use ./forge-cli`

### After implementation

- Delete this file
- Update `context/corepilot.md` in forge-cms/forge-architect

### v2 — deferred, do not implement now

- `forge-cli init` — guided bootstrap flow
- `forge-cli schema list` — schema introspection
