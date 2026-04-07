# Next task for corepilot

## Decision 28 — forge-cli: operator CLI

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
Follow the same pattern as `forge-mcp/`.

### Implementation

Before writing any code, present a plan covering:
1. Which existing Forge HTTP endpoints cover each CLI command — and which gaps
   exist (if any gaps exist, stop and raise an Amendment before proceeding)
2. Module structure and file layout
3. How `--from <file>` frontmatter parsing will work

Wait for Peter's approval of the plan before writing any code.

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

**Token commands:**

```
forge-cli token create <name> <role> <ttl>   # Admin
forge-cli token list                         # Admin
forge-cli token revoke <id>                  # Admin
```

**Diagnostics:**

```
forge-cli status   # calls /_health, prints version and connectivity
```

### Configuration

```
FORGE_URL    — base URL of the running Forge instance
FORGE_TOKEN  — bearer token with appropriate role
```

Loaded from environment or `.forge-cli.env` file in the working directory.

### Constraints

- Zero new dependencies — stdlib only (`net/http`, `flag`, `encoding/json`)
- No direct database access — always HTTP
- AGPL licensed
- All commands must have `--help` output with usage, flags, and examples

### What this is not

- No interactive prompts for long-form content — file-based input only
- No direct database access
- No replacement for forge-admin

### Note on superseded task

This file previously contained a README/MCP section update task. That task
is superseded by this one. Corepilot may include the README update as part
of this work if convenient, or defer it.

### v2 — deferred, do not implement now

- `forge-cli init` — guided bootstrap flow
- `forge-cli schema list` — schema introspection
