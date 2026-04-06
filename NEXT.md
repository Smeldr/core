# Next — Decision 26: Token CLI

## What and why

Forge needs a human-operated path for token administration that does not depend
on a running HTTP server or a valid bearer token. The trigger is operational
safety: an AI agent with MCP access can make mistakes (tokens deleted without
authorisation). An operator with SSH access to the host must be able to list,
create, and revoke tokens directly.

SSH access is already the security boundary for server administration. A CLI
that operates directly against the SQLite database does not expand the attack
surface — it formalises an operation that would otherwise require direct
SQLite manipulation.

## Scope

Add a `token` subcommand to a new `cmd/forge` CLI binary:

```
forge token list
forge token create --name <n> --role <role> --expires <days>
forge token revoke <id>
```

- `FORGE_DB` env specifies the database path (required for all commands)
- `FORGE_SECRET` env specifies the HMAC secret (required for `create` only)
- Plaintext token printed once on create — never stored, never retrievable again
- Same HMAC + SHA-256 hash format as Decision 25 — no new token format
- Same `forge_tokens` table — no schema changes
- `TokenStore` methods reused as-is — no new exported symbols in auth.go

## Module structure

`cmd/forge/` is a **separate module** with its own `go.mod`:

- Module path: `github.com/forge-cms/forge/cmd/forge`
- Depends on: `github.com/forge-cms/forge` (via `replace ../..`)
- SQLite driver: `modernc.org/sqlite` (pure Go, no CGO) — dependency lives
  here only, not in the root module
- Added to `go.work`

This keeps the root module's zero-dependency rule intact.

## Table initialisation

The CLI runs `CREATE TABLE IF NOT EXISTS forge_tokens (...)` directly via
`*sql.DB` before calling `NewTokenStore`. The schema is publicly documented
in Decision 25. No new exported symbols are needed in auth.go — `probeTable`
remains unexported.

## Secret handling

- `FORGE_SECRET` is read from the environment, never from a flag (secrets
  must not appear in shell history)
- `create` requires `FORGE_SECRET` — exits with a clear error if absent
- `list` and `revoke` do not require `FORGE_SECRET`

## Relationship to MCP tools

MCP tools (`create_token`, `list_tokens`, `revoke_token`) are unchanged.
CLI is a parallel path for humans — both operate against the same
`forge_tokens` table.

## What this is not

- No HTTP endpoint
- No env-variable bootstrap
- No changes to TokenStore, forge_tokens schema, or MCP tools

## Decision record

Write Decision 26 to `decisions/phase2.md` and add the index row to
`DECISIONS.md`. Use the format established by Decision 25.

Decision 26 body:

---

## Decision 26 — Token CLI

**Status:** Locked
**Date:** 2026-04-06

**Decision:** Forge provides a `token` subcommand in a dedicated CLI binary
(`cmd/forge`) for human administration of tokens directly on the host. Token
management via SSH does not require a running HTTP server or a valid bearer
token — only filesystem access to the database and the HMAC secret.

### Commands

```
forge token list
forge token create --name <n> --role <role> --expires <days>
forge token revoke <id>
```

Configuration via environment variables:

| Variable | Required for |
|----------|-------------|
| `FORGE_DB` | All commands |
| `FORGE_SECRET` | `create` only |

### Workflow

1. Operator SSH'es to host
2. Runs `forge token create --name "admin" --role admin --expires 3650`
3. Forge prints the plaintext token once — operator copies it
4. Token is stored as SHA-256 hash in `forge_tokens` — same table and schema
   as Decision 25

### Module structure

`cmd/forge/` is a separate module (`github.com/forge-cms/forge/cmd/forge`)
with its own `go.mod`. It depends on the root forge module via `replace ../..`
and on `modernc.org/sqlite` for the SQLite driver. The root module's
zero-dependency rule is preserved — the SQLite dependency lives in the CLI
module only.

### Table initialisation

The CLI runs `CREATE TABLE IF NOT EXISTS forge_tokens (...)` directly before
calling `NewTokenStore`. `probeTable` remains unexported — no new exported
symbols in auth.go.

### Secret handling

`FORGE_SECRET` is read from the environment only — never from a flag.
`list` and `revoke` do not require it.

### Relationship to MCP tools

MCP tools (`create_token`, `list_tokens`, `revoke_token`) are unchanged.
CLI is a parallel path for humans — not a replacement. Both operate against
the same `forge_tokens` table.

### Bootstrap

`ensureBootstrapToken` in `forge-site-working/seed.go` is removed.
First-deploy bootstrap is handled by the operator via `forge token create`
over SSH. This requires a site-level amendment (S-prefix).

### What this is not

- No HTTP endpoint — CLI only
- No new token format — same HMAC + SHA-256 hash as Decision 25
- No env-variable bootstrap (explicitly rejected)

**Rationale:**
SSH-access is already the security boundary for server administration. An
operator who can SSH to the host can already read the database, stop the
process, and read environment variables. Adding a CLI for token management
does not expand the attack surface — it formalises an operation that would
otherwise require direct SQLite manipulation. The CLI removes the dependency
on a running MCP server and a valid admin token for administrative operations.

Secrets are passed via environment variables only. Flags are intentionally
omitted to prevent secrets appearing in shell history, process lists, or
log output.

**Rejected alternatives:**
- Env-variable bootstrap (`FORGE_BOOTSTRAP_TOKEN`): Deferred indefinitely.
  CLI covers the same use cases with clearer operator intent.
- HTTP admin endpoint protected by `APP_SECRET`: Adds network exposure.
  SSH + CLI is simpler and more auditable.
- Single root module with SQLite dependency: Would pollute the core
  library's dependency graph. Separate module preserves the zero-dependency
  rule.

**Consequences:**
- New module: `cmd/forge/` with own `go.mod` and `go.work` entry
- `ensureBootstrapToken` in forge-site-working removed (requires S-amendment)
- No changes to `TokenStore`, `forge_tokens` schema, or MCP tools
- No version bump to root module (library code unchanged)

---

## After implementation

- Delete this file
- Update `context/corepilot.md` in forge-cms/forge-architect with version,
  amendment number, and files changed
