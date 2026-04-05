# Forge — Decisions: Phase 2

Decision 25 onwards.

## Decision 25 — Token management

**Status:** Locked
**Date:** 2026-04-05

**Decision:** Forge provides a `TokenStore` that issues, lists, and revokes
named bearer tokens backed by a SQLite table (`forge_tokens`). Tokens are
stateless HMAC values — the store adds a server-side record that enables
revocation and auditing without changing the token format itself.

### Token table schema

| Field | Type | Notes |
|-------|------|-------|
| `id` | TEXT | UUID v7, primary key |
| `name` | TEXT | Free-label set by Admin (e.g. "Desiree - Author") |
| `role` | TEXT | Forge role string (e.g. "author", "editor") |
| `token_hash` | TEXT | SHA-256 of the issued token — plaintext never stored |
| `expires_at` | TEXT | ISO 8601 — mirrors the token TTL |
| `revoked_at` | TEXT | NULL until revoked |
| `created_at` | TEXT | ISO 8601 |

### Token lifecycle

1. Admin calls `create_token(name, role, ttl)` via MCP
2. Forge calls `SignToken` to produce a signed HMAC token
3. SHA-256 of the token is stored in `forge_tokens`
4. The plaintext token is returned once — never again retrievable
5. On every request, `VerifyBearerToken` checks the hash against the store
   and rejects tokens that are revoked or expired
6. Admin calls `revoke_token(id)` to set `revoked_at` — effective immediately

### MCP tools (forge-mcp, Admin role required)

| Tool | Description |
|------|-------------|
| `create_token` | Issues a new named token with a given role and TTL |
| `list_tokens` | Lists all tokens with name, role, expiry, revoked status |
| `revoke_token` | Revokes a token by ID — effective on next request |

### What this is not

- No user accounts — Forge has no user table, only tokens with roles
- No contact field — no personally identifiable data stored (GDPR)
- No update_token — revoke and re-issue is the only model
- No UI — token management is via MCP tools only

### Module boundary

- `forge/auth.go` — TokenStore, CreateToken, ListTokens, RevokeToken
- `forge-mcp/` — three new admin MCP tools wrapping the above
- `VerifyBearerToken` in `forge/auth.go` gains a TokenStore parameter;
  when nil (no store configured), behaviour is unchanged (stateless HMAC only)

**Rationale:**
Stateless HMAC tokens cannot be revoked — a stolen token is valid until
expiry. A server-side store adds revocation at the cost of one database
lookup per request, which is acceptable given Forge's target workloads.
The SHA-256 hash pattern ensures that a database breach does not expose
usable tokens. Keeping the store optional (nil = stateless mode) preserves
backward compatibility for deployments that do not need revocation.

**Rejected alternatives:**
- Session table with user accounts: Overkill for a token-first auth model.
  Forge has no login flow — tokens are issued by Admin via MCP.
- JWT with blacklist: JWT parsing is more complex than HMAC verification.
  Forge already uses HMAC tokens — no reason to change the format.
- Contact field on tokens: Would store PII. Deliberately omitted.
  Admin uses the name label as a free-text identifier.

**Consequences:**
- `forge.Config` gains optional `TokenStore` field
- `App.TokenStore()` accessor for forge-mcp
- `forge_tokens` table must exist in the database for token management to work;
  Forge logs a warning at startup if TokenStore is configured but the table
  is absent
- Stateless HMAC (current behaviour) remains the default — no breaking change
