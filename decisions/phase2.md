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

---

## Amendment A66 — TokenStore: implementation

**Status:** Agreed
**Date:** 2026-04-05

**Implements:** Decision 25

**What changed:**

- `auth.go`: Added `TokenRecord` struct, `TokenStore` struct and
  `NewTokenStore(db, secret)` constructor, `probeTable`, `Create`,
  `List`, `Revoke` methods. `VerifyBearerToken` signature extended from
  2-arg to 3-arg `(r, secret, store *TokenStore)` — when store is nil,
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

## Amendment A67 — `forge_html`: trusted raw HTML passthrough

**Status:** Agreed
**Date:** 2026-04-05

**Context:**
Go's `html/template` escapes all string output by default. There is no way for a
module template to render pre-rendered HTML (e.g. a video embed iframe, a
third-party widget) without a trusted passthrough function. The gap was identified
during planning of the forge-cms.dev demo page, which needs to embed an iframe
alongside Markdown content (`forge_markdown` handles the Markdown; `forge_html`
handles the iframe).

**What changed:**
- `templatehelpers.go`: `forgeHTML(s string) template.HTML` — one-line function
  returning `template.HTML(s)`; registered as `"forge_html"` in `TemplateFuncMap`;
  godoc warns that the caller is responsible for trust.
- `templatehelpers_test.go`: `TestForgeHTML` (3 sub-tests: passthrough, empty,
  not_escaped); `TestTemplateFuncMap_keys` expected count updated from 8 to 9.

**Consequences:**
- `TemplateFuncMap` grows from 8 to 9 entries.
- No exported Go symbol is added — `forgeHTML` is package-internal; only the map
  key `"forge_html"` is visible to templates.
- No interface, file, or behaviour change beyond the new function.
- Root package bumps to `v1.7.0`.
