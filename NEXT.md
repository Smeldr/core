# Next task for corepilot

## Task: Implement Decision 25 — Token management

### What

Implement server-side token storage and management as specified in
Decision 25 (`decisions/phase2.md`). Read the full decision before
planning anything.

### Why

Stateless HMAC tokens cannot be revoked. A stolen or compromised token
is valid until expiry. Decision 25 adds a server-side `TokenStore` that
enables named tokens with revocation, backed by a SQLite table. This is
the foundation for multi-user access management without an admin UI —
tokens are issued and revoked via MCP tools.

### Scope

Three coordinated changes:

**1. `forge/auth.go` — TokenStore**
- `TokenStore` struct backed by `forge.DB`
- `forge_tokens` table schema: `id`, `name`, `role`, `token_hash`,
  `expires_at`, `revoked_at`, `created_at`
- `token_hash` is SHA-256 of the issued token — plaintext never stored
- `TokenStore.Create(name, role string, ttl time.Duration) (string, error)`
  — calls `SignToken`, stores hash, returns plaintext token once only
- `TokenStore.List() ([]TokenRecord, error)` — returns all tokens with
  name, role, expiry, revoked status
- `TokenStore.Revoke(id string) error` — sets `revoked_at`
- `VerifyBearerToken` gains an optional `*TokenStore` parameter; when
  non-nil, it checks the hash against the store and rejects revoked or
  expired tokens; when nil, behaviour is unchanged (stateless HMAC)
- `forge.Config` gains optional `TokenStore *TokenStore` field
- `App.TokenStore() *TokenStore` accessor

**2. `forge-mcp/` — three new admin tools**
- `create_token` — name (string), role (string), ttl (string, e.g. "720h")
  → returns the plaintext token. Admin role required.
- `list_tokens` — no parameters → returns all tokens with id, name, role,
  expires_at, revoked_at. Admin role required.
- `revoke_token` — id (string) → revokes the token. Admin role required.

**3. Tests**
- Unit tests for `TokenStore` (Create, List, Revoke, hash verification)
- Integration test: create token → verify request succeeds → revoke →
  verify request fails
- MCP tool tests following existing forge-mcp test patterns

### Constraints

- `token_hash` uses `crypto/sha256` from stdlib — no new dependencies
- Forge logs a startup warning if `Config.TokenStore` is set but the
  `forge_tokens` table does not exist in the database
- No contact field on tokens — no PII stored (GDPR)
- No `update_token` — revoke and re-issue is the only model
- Stateless HMAC (current behaviour) remains the default when
  `Config.TokenStore` is nil — no breaking change
- This is a Level 2 amendment. Read `decisions/phase2.md` Decision 25
  for full rationale and consequences before writing any code.
- Add amendment entry to `DECISIONS.md` index and body to
  `decisions/phase2.md` as part of the implementation commit.
  Do this locally via git — never via GitHub MCP.

### After commit

Delete this NEXT.md and update `context/corepilot.md` with new HEAD SHA.
