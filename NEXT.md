# Next — Decision 26: Last-admin guard on token revocation

## What and why

`TokenStore.Revoke` currently executes an unconditional `UPDATE` with no check
on whether the token being revoked is the last active admin token. A single
`revoke_token` MCP call can permanently lock out all administrative access —
requiring direct database intervention to recover. This has happened in
production.

The fix is a guard inside `Revoke` itself: if the token being revoked is the
last active (non-revoked, non-expired) admin token, `Revoke` refuses and
returns a new sentinel error `ErrLastAdmin`. The guard is in core, not in the
MCP layer — it protects against any caller, human or AI.

## Scope

### auth.go — Revoke guard

Before executing the `UPDATE`, `Revoke` runs:

```sql
SELECT COUNT(*) FROM forge_tokens
WHERE role = 'admin'
  AND revoked_at IS NULL
  AND expires_at > <now>
  AND id != <id>
```

If the count is 0, `Revoke` returns `ErrLastAdmin` without modifying any row.

`now` must use the same RFC3339 UTC format as stored in `expires_at`.

### errors.go — new sentinel

Add `ErrLastAdmin` as an exported sentinel consistent with existing sentinels
(`ErrUnauth`, `ErrNotFound`, etc.). Use the established `Err(...)` constructor
pattern. HTTP status: 409 Conflict is the closest fit — the operation is valid
in isolation but conflicts with a safety constraint.

### forge-mcp/tool.go — surface ErrLastAdmin

`handleTokenTool` currently returns a generic error string for `revoke_token`
failures. When the error is `ErrLastAdmin`, return a specific, readable message
to the MCP caller — e.g.:
"Cannot revoke token: it is the last active admin token. Create a replacement
admin token before revoking this one."

## What does not change

- `Create` and `List` — unchanged
- `VerifyBearerToken` — unchanged
- MCP tool signatures — unchanged
- `forge_tokens` schema — unchanged
- Token expiry behaviour — natural expiry is not an operator action and is not
  guarded. The guard applies only to explicit `Revoke` calls.

## Version

- forge core: v1.8.0 (new exported symbol `ErrLastAdmin`)
- forge-mcp: version bump only if forge-mcp changes are needed; assess during
  implementation

## Decision record

Write Decision 26 to `decisions/phase2.md` and add the index row to
`DECISIONS.md`. Use the format established by Decision 25.

Decision 26 body:

---

## Decision 26 — Last-admin guard on token revocation

**Status:** Locked
**Date:** 2026-04-06

**Decision:** `TokenStore.Revoke` refuses to revoke a token if it is the last
active (non-revoked, non-expired) token with the `admin` role. The check is a
single SQL COUNT query executed inside `Revoke` before the UPDATE. If the guard
triggers, `Revoke` returns the new sentinel error `ErrLastAdmin` without
modifying any row.

### Guard query

```sql
SELECT COUNT(*) FROM forge_tokens
WHERE role = 'admin'
  AND revoked_at IS NULL
  AND expires_at > <now>
  AND id != <id>
```

If COUNT = 0, `Revoke` returns `ErrLastAdmin`.

### New exported symbol

`ErrLastAdmin` — sentinel `forge.Error` consistent with `ErrUnauth`,
`ErrNotFound` etc. HTTP status 409 Conflict. forge-mcp surfaces it as a
readable message directing the operator to create a replacement token first.

### Scope

- `auth.go`: `Revoke` gains the pre-check query
- `errors.go`: `ErrLastAdmin` exported sentinel
- `forge-mcp/tool.go`: `handleTokenTool` returns a specific message for
  `ErrLastAdmin` on `revoke_token`
- forge core bumps to `v1.8.0` (new exported symbol)

### What this does not cover

- Natural token expiry — not an operator action; not guarded
- `Create` and `List` — unchanged
- MCP tool signatures — unchanged
- `forge_tokens` schema — unchanged

**Rationale:**
A single `revoke_token` call can permanently lock out all MCP-based
administrative access. Recovery requires direct database access — bypassing
all Forge abstractions. The guard makes this impossible without first creating
a replacement admin token. The check is in core, not in the MCP layer, so it
protects against any caller regardless of interface.

The guard is intentionally narrow: only the `admin` role is protected, only
active (non-revoked, non-expired) tokens are counted, and natural expiry is
excluded because it is not a discrete operator action.

**Rejected alternatives:**
- Guard in forge-mcp only: Does not protect against future non-MCP callers.
  The invariant belongs in the store, not the transport.
- Warn instead of refuse: A warning can be ignored by any caller. A hard
  refusal cannot.
- Guard all roles: Only admin tokens gate administrative access. Over-broad.

**Consequences:**
- `Revoke` is no longer unconditional — callers must handle `ErrLastAdmin`
- forge-mcp surfaces a clear, actionable error message for this case
- No schema changes, no breaking changes to existing call sites that do not
  hit the guard

---

## After implementation

- Delete this file
- Update `context/corepilot.md` in forge-cms/forge-architect with version,
  amendment number, and files changed
