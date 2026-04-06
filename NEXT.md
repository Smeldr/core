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

Add a `token` subcommand to the Forge binary:

```
forge token list
forge token create --name <name> --role <role> --expires <days>
forge token revoke <id>
```

- `--db` flag (or `FORGE_DB` env) specifies the database path
- Plaintext token printed once on create — never stored, never retrievable again
- Same HMAC + SHA-256 hash format as Decision 25 — no new token format
- Same `forge_tokens` table — no schema changes
- `TokenStore` methods reused as-is — no new exported symbols needed in auth.go

## Relationship to MCP tools

MCP tools (`create_token`, `list_tokens`, `revoke_token`) are unchanged.
CLI is a parallel path for humans — both operate against the same `forge_tokens` table.

## What this is not

- No HTTP endpoint
- No env-variable bootstrap
- No changes to TokenStore, forge_tokens schema, or MCP tools

## Decision record

Write Decision 26 to `decisions/phase2.md` and add the index row to `DECISIONS.md`.
Use the format established by Decision 25.

Decision 26 body:

---

## Decision 26 — Token CLI

**Status:** Locked
**Date:** 2026-04-06

**Decision:** Forge provides a `token` subcommand in the Forge binary for human
administration of tokens directly on the host. Token management via SSH does not
require a running HTTP server or a valid bearer token — only filesystem access
to the database.

### Commands

```
forge token list
forge token create --name <name> --role <role> --expires <days>
forge token revoke <id>
```

`--db` flag (or `FORGE_DB` env) specifies the database path. Defaults to the
same value as the application config.

### Workflow

1. Operator SSH'es to host
2. Runs `forge token create --name "admin" --role admin --expires 3650`
3. Forge prints the plaintext token once — operator copies it
4. Token is stored as SHA-256 hash in `forge_tokens` — same table and schema
   as Decision 25

### Relationship to MCP tools

MCP tools (`create_token`, `list_tokens`, `revoke_token`) are unchanged. CLI
is a parallel path for humans — not a replacement. Both operate against the
same `forge_tokens` table.

### Bootstrap

`ensureBootstrapToken` in `forge-site-working/seed.go` is removed. First-deploy
bootstrap is handled by the operator via `forge token create` over SSH.
This requires a site-level amendment (S-prefix).

### What this is not

- No HTTP endpoint — CLI only
- No new token format — same HMAC + SHA-256 hash as Decision 25
- No env-variable bootstrap (explicitly rejected)

### Module boundary

- `cmd/forge/` (or equivalent CLI entrypoint) — `token` subcommand
- `forge/auth.go` — `TokenStore` methods reused as-is; no new exported symbols needed
- `forge-site-working/seed.go` — `ensureBootstrapToken` removed (S-amendment)

**Rationale:**
SSH-access is already the security boundary for server administration. An operator
who can SSH to the host can already read the database, stop the process, and read
environment variables. Adding a CLI for token management does not expand the attack
surface — it formalises an operation that would otherwise require direct SQLite
manipulation. The CLI removes the dependency on a running MCP server and a valid
admin token for administrative operations.

**Rejected alternatives:**
- Env-variable bootstrap (`FORGE_BOOTSTRAP_TOKEN`): Deferred indefinitely. CLI
  covers the same use cases with clearer operator intent and no config complexity.
- HTTP admin endpoint protected by `APP_SECRET`: Adds network exposure. SSH + CLI
  is simpler and more auditable.

**Consequences:**
- Forge binary gains a `token` subcommand
- `ensureBootstrapToken` in forge-site-working removed (requires S-amendment)
- No changes to `TokenStore`, `forge_tokens` schema, or MCP tools

---

## After implementation

- Delete this file
- Update `context/corepilot.md` in forge-cms/forge-architect with version,
  amendment number, and files changed
