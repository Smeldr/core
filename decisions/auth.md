# Forge Decisions — Authentication

Archived from decisions/recent.md on 2026-05-17.
Read on demand. See DECISIONS.md for the full index.

---

## Decision 25 � Token management

**Status:** Locked
**Date:** 2026-04-05

**Decision:** Forge provides a `TokenStore` that issues, lists, and revokes
named bearer tokens backed by a SQLite table (`forge_tokens`). Tokens are
stateless HMAC values � the store adds a server-side record that enables
revocation and auditing without changing the token format itself.

### Token table schema

| Field | Type | Notes |
|-------|------|-------|
| `id` | TEXT | UUID v7, primary key |
| `name` | TEXT | Free-label set by Admin (e.g. "Desiree - Author") |
| `role` | TEXT | Forge role string (e.g. "author", "editor") |
| `token_hash` | TEXT | SHA-256 of the issued token � plaintext never stored |
| `expires_at` | TEXT | ISO 8601 � mirrors the token TTL |
| `revoked_at` | TEXT | NULL until revoked |
| `created_at` | TEXT | ISO 8601 |

### Token lifecycle

1. Admin calls `create_token(name, role, ttl)` via MCP
2. Forge calls `SignToken` to produce a signed HMAC token
3. SHA-256 of the token is stored in `forge_tokens`
4. The plaintext token is returned once � never again retrievable
5. On every request, `VerifyBearerToken` checks the hash against the store
   and rejects tokens that are revoked or expired
6. Admin calls `revoke_token(id)` to set `revoked_at` � effective immediately

### MCP tools (forge-mcp, Admin role required)

| Tool | Description |
|------|-------------|
| `create_token` | Issues a new named token with a given role and TTL |
| `list_tokens` | Lists all tokens with name, role, expiry, revoked status |
| `revoke_token` | Revokes a token by ID � effective on next request |

### What this is not

- No user accounts � Forge has no user table, only tokens with roles
- No contact field � no personally identifiable data stored (GDPR)
- No update_token � revoke and re-issue is the only model
- No UI � token management is via MCP tools only

### Module boundary

- `forge/auth.go` � TokenStore, CreateToken, ListTokens, RevokeToken
- `forge-mcp/` � three new admin MCP tools wrapping the above
- `VerifyBearerToken` in `forge/auth.go` gains a TokenStore parameter;
  when nil (no store configured), behaviour is unchanged (stateless HMAC only)

**Rationale:**
Stateless HMAC tokens cannot be revoked � a stolen token is valid until
expiry. A server-side store adds revocation at the cost of one database
lookup per request, which is acceptable given Forge's target workloads.
The SHA-256 hash pattern ensures that a database breach does not expose
usable tokens. Keeping the store optional (nil = stateless mode) preserves
backward compatibility for deployments that do not need revocation.

**Rejected alternatives:**
- Session table with user accounts: Overkill for a token-first auth model.
  Forge has no login flow � tokens are issued by Admin via MCP.
- JWT with blacklist: JWT parsing is more complex than HMAC verification.
  Forge already uses HMAC tokens � no reason to change the format.
- Contact field on tokens: Would store PII. Deliberately omitted.
  Admin uses the name label as a free-text identifier.

**Consequences:**
- `forge.Config` gains optional `TokenStore` field
- `App.TokenStore()` accessor for forge-mcp
- `forge_tokens` table must exist in the database for token management to work;
  Forge logs a warning at startup if TokenStore is configured but the table
  is absent
- Stateless HMAC (current behaviour) remains the default � no breaking change

---

## Amendment A66 � TokenStore: implementation

**Status:** Agreed
**Date:** 2026-04-05

**Implements:** Decision 25

**What changed:**

- `auth.go`: Added `TokenRecord` struct, `TokenStore` struct and
  `NewTokenStore(db, secret)` constructor, `probeTable`, `Create`,
  `List`, `Revoke` methods. `VerifyBearerToken` signature extended from
  2-arg to 3-arg `(r, secret, store *TokenStore)` � when store is nil,
  behaviour is unchanged (stateless HMAC only).
- `forge.go`: `Config.TokenStore *TokenStore` field; `App.tokenStore`
  private field; `App.TokenStore() *TokenStore` accessor; startup probe
  in `Handler()` that logs a warning if the table is absent.
- `forge-mcp/mcp.go`: `Server.tokenStore *forge.TokenStore` field; wired
  from `app.TokenStore()` in `New()`.
- `forge-mcp/transport.go`: sole `VerifyBearerToken` call updated to pass
  `s.tokenStore`.
- `forge-mcp/tool.go`: `authoriseAdmin()` helper; `tokenToolDefs()` (3
  tool definitions with JSON Schema); `handleTokenTool()` dispatcher;
  `handleToolsList()` and `handleToolsCall()` updated to expose and
  dispatch token tools when `s.tokenStore != nil`.

**Consequences:**
- MCP `tools/list` returns three additional tool entries when a TokenStore
  is configured; token tools require Admin role.
- Token tool names (`create_token`, `list_tokens`, `revoke_token`) are
  pre-dispatched before module-level auth to avoid name collisions.
- `forge-mcp` version bumps to `v1.1.0`; root package bumps to `v1.6.0`.

---

## Decision 26 � Last-admin guard on token revocation

**Status:** Locked
**Date:** 2026-04-06

**Decision:** `TokenStore.Revoke` refuses to revoke a token if it is the last
active (non-revoked, non-expired) token with the `admin` role. The check is a
two-step SQL lookup executed inside `Revoke` before the UPDATE. First the role
of the target token is fetched; if it is not `admin` the guard is skipped. If it
is `admin`, a COUNT of other active admin tokens is performed. If that count is 0,
`Revoke` returns the new sentinel error `ErrLastAdmin` without modifying any row.

### Guard logic

```go
// 1. Fetch role of target token � skip guard for non-admin:
SELECT role FROM forge_tokens WHERE id = $1

// 2. Only if role = "admin": count other active admins:
SELECT COUNT(*) FROM forge_tokens
WHERE role = 'admin'
  AND revoked_at IS NULL
  AND expires_at > $1
  AND id != $2
```

If COUNT = 0, `Revoke` returns `ErrLastAdmin`.

### New exported symbol

`ErrLastAdmin` � sentinel `forge.Error`, HTTP status 409 Conflict,
code `"last_admin"`, public message `"Cannot revoke the last active admin token"`.
Consistent with `ErrConflict` and other package sentinels.

### Scope

- `auth.go`: `Revoke` gains the two-step pre-check
- `errors.go`: `ErrLastAdmin` exported sentinel
- `forge-mcp/tool.go`: `handleTokenTool` returns a specific, actionable message
  for `ErrLastAdmin` on `revoke_token`
- forge core bumps to `v1.8.0` (new exported symbol `ErrLastAdmin`)
- forge-mcp bumps to `v1.2.0` (behavioural change in error surface)

### What this does not cover

- Natural token expiry � not an operator action; not guarded
- `Create` and `List` � unchanged
- MCP tool signatures � unchanged
- `forge_tokens` schema � unchanged

**Rationale:**
A single `revoke_token` call can permanently lock out all MCP-based administrative
access. Recovery requires direct database access � bypassing all Forge abstractions.
The guard makes this impossible without first creating a replacement admin token.
The check is in core, not in the MCP layer, so it protects against any caller
regardless of interface.

The guard is intentionally narrow: only the `admin` role is protected, only active
(non-revoked, non-expired) tokens are counted, and natural expiry is excluded
because it is not a discrete operator action. The two-query implementation is
preferred over a single-query approach so that non-admin tokens are never blocked
when no admin tokens exist � a correctness guarantee that the spec's single-query
wording did not provide.

**Rejected alternatives:**
- Guard in forge-mcp only: Does not protect against future non-MCP callers. The
  invariant belongs in the store, not the transport.
- Warn instead of refuse: A warning can be ignored by any caller. A hard refusal cannot.
- Guard all roles: Only admin tokens gate administrative access. Over-broad.
- Single-query guard (COUNT of other admins regardless of target role): Would
  incorrectly block revoking non-admin tokens when no admin tokens exist.

**Consequences:**
- `Revoke` is no longer unconditional � callers must handle `ErrLastAdmin`
- forge-mcp surfaces a clear, actionable error message for this case
- No schema changes, no breaking changes to existing call sites that do not hit the guard


## Amendment A83 — auth.go / forge.go / forge-cli/init.go: TokenStore bootstrap + forge-cli init

**Date:** 2026-05-04
**Status:** Agreed
**Level:** 1 (additive — new unexported method, new forge-cli subcommand, no breaking change)
**Files:** `auth.go`, `forge.go`, `auth_test.go`, `forge-cli/init.go` (new), `forge-cli/main.go`, `forge-cli/CHANGELOG.md`

### Problem

A new operator deploying Forge with a TokenStore had no recovery path if they
missed the initial bootstrap token. The token is shown only once (correct by
design — only the fingerprint is stored). If the operator missed it, they were
forced to manipulate the database directly. Additionally, site-level code
(`ensureBootstrapToken` in `seed.go`) was the only discovery mechanism for this
pattern; a new developer would not know it existed.

### Decision

**Part 1 — `TokenStore.ensureBootstrap` (forge core):**

New unexported method `ensureBootstrap(ctx context.Context)` on `*TokenStore`.
Called by `App.Handler()` immediately after a successful `probeTable()`.
Behaviour:

- Queries `SELECT COUNT(*) FROM forge_tokens`.
- If count > 0 or scan fails: no-op (table already has tokens, or not
  accessible — probeTable already warned).
- If count == 0: calls `ts.Create(ctx, "bootstrap-admin", "admin", 10yr)` and
  emits the raw token via `slog.Warn` — impossible to miss in any log output:
  ```
  WARN  forge: forge_tokens is empty — bootstrap admin token created (copy now, shown once):
        <raw token>
  ```

**Part 2 — `forge-cli init` (forge-cli v0.3.0):**

New subcommand that completes the setup flow in one command:

```
forge-cli init [--url URL] [--bootstrap-token TOKEN] [--name NAME] [--days N] [--force]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--url` | `http://localhost:8080` | Base URL of the Forge instance |
| `--bootstrap-token` | (required) | Pasted from startup log |
| `--name` | `operator` | Name for the created admin token |
| `--days` | `365` | Token TTL in days |
| `--force` | false | Overwrite existing `.forge-cli.env` |

Flow:
1. `GET /_health` unauthenticated — fail fast if unreachable.
2. `create_token` via MCP with the bootstrap token — admin role, given name, TTL.
3. Extract `"token"` from the JSON result.
4. Fail if `.forge-cli.env` exists and `--force` not set.
5. Write `.forge-cli.env`: `FORGE_URL=...` + `FORGE_TOKEN=...`.
6. `GET /_health` with the new token — warn on failure (env file preserved).
7. Print success lines.

`FORGE_MCP_URL` is not written — `client.go` defaults to `FORGE_URL/mcp/message`.
Role is hardcoded to `admin` — `init` is a bootstrap tool only.

### Consequences

1. `ensureBootstrap` is unexported — no public API change.
2. `forge_tokens` emptiness is checked on every `App.Handler()` call, but the
   query is a single `SELECT COUNT(*)` — negligible overhead, and only when
   `TokenStore` is configured.
3. Sites that call `ensureBootstrapToken` in their own code will see a
   duplicate token on the first run. Site-level code should be removed — this
   is a follow-up task for `forge-site-working`.
4. `forge-cli` bumped to v0.3.0 (new subcommand, no breaking change).
5. `cliVersion` corrected from `"0.1.0"` to `"0.3.0"` (was never updated
   through v0.2.0 / A76 module rename).
6. 2 new tests in `auth_test.go`: `TestTokenStore_ensureBootstrap_empty` and
   `TestTokenStore_ensureBootstrap_nonEmpty`.

---
