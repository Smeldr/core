# Smeldr — Decisions Archive: Phase 13 (A191–A193)

Archived 2026-07-05 from decisions/recent.md.
Topic: T49 governance wiring — Steps 3 and 4 (core half and MCP half).

---

## A191 — T49 Step 3: Wire Authorized into module role gate + ToolPolicy (v1.51.0)

**Date:** 2026-07-02
**Status:** Agreed
**Files:** `governance.go`, `module.go`, `smeldr.go`, `governance_test.go`, `module_test.go`

**Context:**
T49 Step 3 wires the governance `RoleStore` into `Module[T]`'s role-check call sites (Path A from governance-model.md §7) and exposes `RoleStore.ToolPolicy` as the seam between core and `smeldr.dev/mcp` for per-tool authorization enforcement.

**Decision:**

Three-branch fail-closed architecture (§5.5): when a role-gate method is called, check the state of `Module.roleStore`:

1. `roleStore == nil` — governance not wired; use legacy role-based check (`ctx.User().HasRole(legacyRole)`)
2. `roleStore != nil && ctx.User().ID == ""` — unauthenticated request; deny immediately (no legacy fallback)
3. `roleStore != nil && ctx.User().ID != ""` — authenticated; call `RoleStore.Authorized(ctx, ID, op, target)` with fail-closed error semantics (error → false, never fall back to legacy)

Applied to four gate-points:

- `Module[T].canReadDrafts(ctx Context) bool` — gates draft visibility in list filters and single-item reads
- `Module[T].checkWriteOp(ctx Context, op string, legacyRole Role) bool` — gates create/update/delete operations
- `Module[T].isVisible(ctx Context, item any) bool` — converted from standalone `isVisible(item any, user User) bool` to a method; Published items are always visible; Draft visibility delegates to `canReadDrafts`
- All call sites updated: 4 `isVisible` checks, 2 list-handler status-filter branches, 3 write/delete enforcement points

**New exported method:**

- `RoleStore.ToolPolicy(ctx, toolName) (requiredOp string, found bool, err error)` — exact-match lookup in `smeldr_tool_policies` table
  - `found=false, nil` when no row matches (avoids leaking `ErrNoRows` to caller)
  - `found=true, requiredOp, nil` when match found
  - `found=false, "", err` on DB error (fail-closed: caller must deny on error, not assume "no policy")
  - Seam between core and `smeldr.dev/mcp`: the MCP server calls `ToolPolicy` to resolve tool permissions before invoking the actual tool

**Module injection and wiring:**

- `Module[T]` gains `roleStore *RoleStore` field and `setRoleStore(*RoleStore)` method
- `App` gains `governanceModules []interface{ setRoleStore(*RoleStore) }` slice; modules register themselves at `App.Content()` time (same pattern as `navTree` registration)
- `App.Handler()` injects the `RoleStore` (if wired via `App.Governance()`) into all registered modules before returning the HTTP handler

**Deferred:**

- Prefix-pattern fallback for runtime-defined content types (T104) deferred to Step 8
- Item-level scope (slug→ID resolution for static/dynamic grants) deferred; `AuthTarget` is used at type-level only; documented via TODO comments

**Consequences:**
- 1 new exported method: `RoleStore.ToolPolicy`
- 4 new unexported members on `Module[T]`: `roleStore` field, `setRoleStore`, `canReadDrafts`, `checkWriteOp`; `isVisible` converted from package-level func to method
- New unexported App field: `governanceModules`
- Apps without governance wired see zero behaviour change (nil `roleStore` → legacy checks pass through)
- Apps with governance wired: unauthenticated requests (`ID == ""`) are denied; Authorized errors → deny; no legacy fallback after governance is wired
- 20 new tests; coverage 96.0%

Coverage: 96.0%. core v1.51.0.

---

## A192 — T49 Step 4 (MCP half): unified authoriseTool in smeldr.dev/mcp (v1.26.0)

**Status:** Done  
**Date:** 2026-07-03  
**Repo:** smeldr.dev/mcp

**Problem:** `mcp/tool.go` had three hardcoded role-check helpers (`authorise`, `authoriseEditor`,
`authoriseAdmin`) scattered across ~22 call sites in `handleToolsCall`. These had no knowledge of
the governance model introduced in A188–A191.

**Decision:** Introduce `authoriseTool(ctx smeldr.Context, toolName string, legacyRole smeldr.Role,
rs *smeldr.RoleStore) *jsonRPCError` as the single authorisation seam in `smeldr.dev/mcp`.
Three-branch pattern (§5.5):

1. `rs == nil` — legacy path: `smeldr.HasRole(ctx.User().Roles, legacyRole)`. Exact current
   behaviour for deployments without governance.
2. `rs` wired, `ctx.User().ID == ""` — deny (unauthenticated request).
3. `rs` wired, token ID present — `rs.ToolPolicy(ctx, toolName)` then
   `rs.Authorized(ctx, ctx.User().ID, requiredOp, smeldr.AuthTarget{})`. Both fail closed on
   error. `ToolPolicy` not-found also denies (unrecognised tool = no known requirement).

`rs` is passed as a parameter rather than fetched inside the function so test code can inject a
custom `*smeldr.RoleStore` backed by a failing DB without modifying App internals.
`handleToolsCall` fetches `rs := s.app.RoleStore()` once at the top of the function.

`found=false` from `ToolPolicy` → deny. Rationale: an unrecognised tool has no known
authorisation requirement; granting access would mean governance has no coverage of it. The T104
Step 8 prefix-pattern fallback for runtime-generated tool names is explicitly deferred.

**Removed:** `authorise`, `authoriseEditor`, `authoriseAdmin` — dead after all 22 call sites replaced.

**Consequences:**
- No behavioural change for any deployment without governance (`rs == nil` path is identical to
  the removed methods).
- All 22 call sites in `handleToolsCall` updated uniformly.
- 8 new tests (`tool_gov_test.go`) covering all paths of `authoriseTool`.
- `smeldr.dev/core` dep bumped from v1.45.1 to v1.51.0.

Coverage: 96.0%. mcp v1.26.0.

---

## A193 — T49 Step 4 (core half): RoleGranted name-based lookup + required_role resolution in validateTransition (v1.52.0)

**Status:** Done  
**Date:** 2026-07-03  
**Repo:** smeldr/core

**Problem:** `validateTransition` in `state.go` fetched transition existence but could not enforce `required_role` — the field was stored in `smeldr_transitions` but never checked. `DynamicTypeRepo.SetStatus` had no way to wire governance. `Module[T]` call sites (`MCPPublish`, `MCPSchedule`, `MCPArchive`) did not pass actor identity to `validateTransition`. `App.DrainEvalQueue` (T23 Step 13, A187) transitions items via the same machinery without a human actor behind the call — a system-initiated, timer-driven path that must remain exempted from `required_role` enforcement while permitting enforcement for request-initiated operations.

**Decision:** Introduce `RoleStore.RoleGranted(ctx context.Context, tokenID, roleName string, target AuthTarget) (bool, error)` as the name-based counterpart to `Authorized` (Path B named-role lookup, §7 governance-model.md). One JOIN query on `smeldr_role_grants g JOIN smeldr_roles r ON r.id = g.role_id WHERE g.token_id = ? AND r.name = ?`. Pre-collects rows before closing cursor (SQLite single-statement constraint). Evaluates three scope modes (global/static/dynamic) identically to `Authorized`: `ScopeGlobal` always matches; `ScopeStatic` matches `TypeName+":"+ID` exact and `TypeName+":*"` wildcard; `ScopeDynamic` checks one-hop relation via `relationExists`. Returns `(false, err)` on any DB error (fail-closed §5.5). Added to `governance.go`.

Extend `validateTransition` signature: `(ctx context.Context, db DB, rs *RoleStore, actorID, typeName, from, to string) error`. Two-zone pattern (§5.5):

**Zone 1: Structural check (fail-open).** Query `smeldr_transitions` for existence. Nil DB, non-SQLite, no custom flow registered, or query error → `nil` (unchanged from today). This is the "does this transition even exist" question — governance is irrelevant here.

**Zone 2: Authorization check (fail-closed).** Entered only when `required_role` is non-NULL and non-empty:
- `rs == nil` → `nil` (governance not wired; `required_role` value ignored)
- `actorID == ""` → `nil` (system-initiated path, pre-authorized — skip check)
- `rs != nil` && `actorID != ""` → call `rs.RoleGranted(ctx, actorID, required_role, AuthTarget{TypeName: typeName, ID: item_id})`. If error → `ErrForbidden` (fail-closed). If `!ok` → `ErrForbidden`. If `ok` → `nil`.

The `actorID == ""` guard distinguishes system-initiated paths (`DrainEvalQueue`, background jobs, test code with plain `context.Context`) from request-initiated ones (`Module[T].MCPPublish` etc.). System paths were never actor-initiated; their authorization decision already happened when the trigger was registered. Request paths require a live actor identity.

Updated `validateTransition` location: `state.go`.

Wire governance into `DynamicTypeRepo`: add `rs *RoleStore` field + `WithGovernance(rs *RoleStore) *DynamicTypeRepo` shallow-copy method (returns new `DynamicTypeRepo` with `rs` set). `SetStatus` extracts `actorID` via local `smeldrCtxAccessor` interface (`User() User`). Plain `context.Context` callers get `actorID=""` (system path, skip check). Updated in `dynamic.go`.

Update three `Module[T]` call sites in `module.go`:
- `MCPPublish`: pass `m.roleStore, ctx.User().ID` to `validateTransition`
- `MCPSchedule`: pass `m.roleStore, ctx.User().ID` to `validateTransition`
- `MCPArchive`: pass `m.roleStore, ctx.User().ID` to `validateTransition`

**Consequences:**
- `ErrForbidden` maps to `-32001` in `smeldr.dev/mcp` (confirmed via `errors.Is` unwrap in `errorFor()`).
- `DrainEvalQueue` is architecturally exempt: does direct SQL UPDATEs, never calls `validateTransition` (no change to current flow).
- `actorID == ""` guard covers all system-initiated paths — no special handling needed for `DrainEvalQueue` or test code using plain `context.Context`. No cross-file changes required.
- No behavioural change for any deployment without `App.Governance` wired (`rs == nil` path → skip check).
- Structural zone (does transition exist?) stays fail-open — identical to today.
- Authorization zone (can this actor trigger it?) is fail-closed — distinct from structural check, documented with code comments.
- `RoleGranted` query shares the same scope-evaluation logic as `Authorized` — no duplicate reasoning.
- `smeldr.dev/mcp` changes in A192 already handle `ErrForbidden` via `errors.Is(err, smeldr.ErrForbidden)`.
- 20 new tests: 12 for `RoleGranted` (global/static/wildcard/dynamic pass, miss, empty-target, pending-error, malformed-JSON, query-error), 5 for `validateTransition` required_role paths (nil-RS, empty-actor, granted, not-granted, grant-check-error), 3 for `DynamicTypeRepo.WithGovernance`+`SetStatus` (plain-ctx, authorized, forbidden).
- Coverage: 96.0%. core v1.52.0.

---
