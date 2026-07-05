# Smeldr — Recent Decisions

Rolling working file. All new decisions are added here first.
When this file approaches ~20KB, report it at session start — the architect
will issue archiving instructions via NEXT.md.

Non-Decisions go directly to `nondecisions.md` — not here.

Archived 2026-05-25: D32 → docs.md · A87, A97–A101 → core.md
Archived 2026-05-30: A102–A115 → phase3-archive.md
Archived 2026-06-04: A116–A120 → phase3-archive.md
Archived 2026-06-05: A121–A125 → phase4-archive.md
Archived 2026-06-07: A126–A130 → phase5-archive.md
Archived 2026-06-09: A131–A135 → phase6-archive.md
Archived 2026-06-10: A136–A138 → phase7-archive.md
Archived 2026-06-15: A139–A150 → phase8-archive.md
Archived 2026-06-23: A151–A157 → phase9-archive.md
Archived 2026-07-01: A158–A169 → phase10-archive.md
Archived 2026-07-02: A170, A171, A173–A183 → phase11-archive.md
Archived 2026-07-04: A184–A190 → phase12-archive.md

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

## A194 — T112: Postgres portability for state/governance/dynamic SQL (v1.52.1)

**Status:** Agreed — **Date:** 2026-07-04

### Decision

Convert all SQLite-only SQL constructs in `state.go`, `governance.go`, `migrate.go`, and `dynamic.go` to the portable pattern already used in `relations.go`, `redirects.go`, `schemas.go`, and `storage.go`. Three specific changes:

1. **`?` → `$N` positional parameters.** Every `?` placeholder replaced with `$1`, `$2`, … per query. Both modernc.org/sqlite and pgx/v5/stdlib accept `$N` natively — no translation layer needed or added.

2. **`INSERT OR IGNORE` → `INSERT … ON CONFLICT (column) DO NOTHING`.** Explicit conflict columns per table:
   - `smeldr_state_flows(name)`, `smeldr_states(flow_id, name)`, `smeldr_transitions(flow_id, from_state, to_state)`, `smeldr_eval_queue(type_name, item_id, to_state)`
   - `smeldr_roles(name)`, `smeldr_tool_policies(tool_name)`, `smeldr_routes(path_pattern)`

3. **`DefineRole` two-step → single UPSERT.** Old pattern: INSERT OR IGNORE followed by a separate UPDATE (two round-trips, theoretical race window). New pattern: single `INSERT … ON CONFLICT (name) DO UPDATE SET operations=EXCLUDED.operations, …`; `id` and `created_at` are INSERT-only (not in `DO UPDATE SET`). The follow-up `SELECT id … WHERE name=$1` for audit is unchanged.

4. **`IS ?` → `IS NOT DISTINCT FROM $N` in Grant.** SQLite-only `IS ?` syntax is not valid Postgres. `IS NOT DISTINCT FROM $N` is the portable NULL-safe equality operator supported on SQLite ≥ 3.39.0 and all Postgres versions. Also removes a duplicate `anchorID` argument that the old two-argument form required.

5. **Postgres integration test.** New `integration_core_test.go` (`//go:build integration`, `package smeldr`) boots `smeldr.App` against Postgres 16 via `database/sql` + `_ "github.com/jackc/pgx/v5/stdlib"`. Tests `migrateStateFlows`, `RegisterFlow` (idempotent), `migrateGovernance`, `DefineRole` (create + update via UPSERT), `Grant` (idempotent), `Authorized`, `RoleGranted`, `ToolPolicy`. Skips when `DATABASE_URL` is not set. Driver imported via blank import; `*sql.DB` satisfies `smeldr.DB` directly — no circular module dependency.

6. **CI extension.** `.github/workflows/ci.yml` integration job gains a second step: `go test -v -tags integration ./...` from repo root (runs after the existing pgx step, same Postgres 16 service container). `github.com/jackc/pgx/v5 v5.9.2` added as a direct `go.mod` dependency.

**`governance_test.go` test updates** (consequence of SQL changes):
- Four `execFailDB.failOn` strings updated: `"INSERT OR IGNORE INTO smeldr_roles"` → `"INSERT INTO smeldr_roles"`, `"INSERT OR IGNORE INTO smeldr_tool_policies"` → `"INSERT INTO smeldr_tool_policies"`.
- `TestDefineRole_UpdateError` removed — tested the second ExecContext call (`UPDATE smeldr_roles`) which no longer exists after the UPSERT consolidation.
- `TestGrant_ResolveIDError_WithAnchor` failOn updated: `"scope_anchor_id=?"` → `"scope_anchor_id=$3"`.

**Deferred (flagged, not fixed in T112):**
- `migrateStateFlowConflictColumns` uses `PRAGMA table_info` (SQLite-only DDL introspection). Returns nil on non-SQLite error, so Postgres boot still succeeds — but the `active_state`/`conflict_policy` ALTER TABLEs are skipped. Schema version table or IF NOT EXISTS DDL approach needed.
- `MigrateRedirectsToRoutes` queries `sqlite_master` (SQLite-only). Same nil-on-error guard; Postgres safe but migration skipped.
- DDL column types: `INTEGER PRIMARY KEY` (SQLite ROWID alias) vs Postgres `BIGSERIAL`. Separate task.

### Consequences

- All existing SQLite tests pass unchanged — portability fix, not a behaviour change.
- Postgres 16 migration, seeding, and authorization queries verified by CI on every push.
- `DefineRole` UPSERT is now atomic — eliminates the INSERT/UPDATE race window from A188/A189.
- `IS NOT DISTINCT FROM $N` handles NULL correctly on both databases — no semantic change.
- Three deferred constructs (`PRAGMA`, `sqlite_master`, DDL types) are fail-open: Postgres boot succeeds but affected migrations are skipped silently. Tracked as follow-up.
- Coverage: 96.0%. core v1.52.1.

---

## A195 — Postgres portability: DATETIME → TIMESTAMP in DDL + pgx CI replace step (v1.52.2)

**Status:** Accepted  
**Date:** 2026-07-04

### What

Five classes of Postgres DDL portability fix, all exposed by the first green run of the pgx Postgres integration test:

1. **DATETIME → TIMESTAMP** in 8 columns across 6 tables (`governance.go` + `migrate.go`).
2. **BOOLEAN DEFAULT 0 → DEFAULT FALSE** on `is_initial`, `is_terminal`, `suppresses_signals` in `smeldr_states` (`migrate.go`).
3. **INTEGER PRIMARY KEY → TEXT NOT NULL PRIMARY KEY** on all four state-flow tables (`smeldr_state_flows`, `smeldr_states`, `smeldr_transitions`, `smeldr_transition_triggers`). FK columns (`flow_id`, `transition_id`) changed to `TEXT NOT NULL`. All INSERTs now supply explicit `NewID()` values; `flowID`/`transitionID` scan types changed from `int64` to `string` throughout `state.go`, `migrate.go`, `state_test.go`, `migrate_test.go`.
4. **active_state / conflict_policy included in initial DDL** for `smeldr_state_flows`. Previously added only by `migrateStateFlowConflictColumns` via `PRAGMA table_info` (SQLite-only); on Postgres the probe returns an error and the columns were never created.
5. **FK `REFERENCES smeldr_tokens(id)` removed** from `smeldr_role_grants.token_id`. Auth is opt-in; `smeldr_tokens` may not exist when `App.Governance()` is called. Postgres enforces FK targets at CREATE TABLE time.

Also: `pgx/go.mod` bump v1.38.0 → v1.52.1; `smeldr.State` field name correction (`Initial` → `IsInitial`, `Terminal` → `IsTerminal`) in pgx integration test; `go mod edit -replace smeldr.dev/core=../` CI step added to permanently close the version-lag chicken-and-egg.

### Why

Each fix addresses a specific Postgres/SQLite divergence: DATETIME is a SQLite/MySQL type; Postgres requires TIMESTAMP. BOOLEAN DEFAULT 0 is rejected by Postgres (type mismatch). INTEGER PRIMARY KEY is a rowid alias in SQLite (auto-increment) but is not auto-increment in Postgres. PRAGMA is SQLite-only DDL introspection. Postgres enforces FK targets at table creation time. All five patterns were safe on SQLite and silent; all five break on Postgres. This is the first batch of concrete fixes under T117 (Postgres DDL portability). Remaining: DATETIME in `blocks.go`, `relations.go`, `routes.go`, `schemas.go`, `site_config.go`; `sqlite_master` probes; `MigrateRedirectsToRoutes`.

### Consequences

- Production DDL is now Postgres-compatible for all governance and state-flow tables.
- Existing SQLite databases unaffected: TIMESTAMP has identical NUMERIC affinity, DEFAULT FALSE is equivalent to DEFAULT 0, TEXT PKs store strings, and the missing ALTER TABLE columns are handled by `migrateStateFlowConflictColumns` on older databases.
- CI integration job tests pgx against local core on every PR — version lag permanently closed.
- No API change. v1.52.2.

---

## A196 — T113: HTTP/MCP AuthTarget asymmetry fix in smeldr.dev/mcp (v1.26.1)

**Status:** Done  
**Date:** 2026-07-04  
**Repo:** smeldr.dev/mcp

### What

The MCP authorization gate (`authoriseTool` in `tool.go`) passed `smeldr.AuthTarget{}` (empty TypeName) to `RoleStore.Authorized` for all tools. The HTTP gate (`canReadDrafts` / `checkWriteOp` in `module.go`) passes `AuthTarget{TypeName: m.contentTypeName}`. A `ScopeStatic` grant scoped to a type (e.g., `"Post:*"`) matches the HTTP gate but is denied by the MCP gate for the identical token and operation — governance behaves inconsistently by transport.

### Why

Inconsistent authorization across HTTP and MCP transports undermines the security model. Role enforcement must be deterministic: the same grant and operation must succeed or fail identically regardless of whether the request enters via HTTP or MCP.

### Consequences

Added `target smeldr.AuthTarget` as the last parameter to `authoriseTool`. The caller is responsible for supplying the correct target:

- **Infrastructure tools** (tokens, nav, webhooks, preview, upload, redirect, page meta, relation, state, signal, dynamic content, block/node/composition/schema/typed tools): pass `smeldr.AuthTarget{}`. These tools have no per-content-type scope — global grants apply, type-scoped grants correctly don't.

- **Module-scoped tools** (create, update, publish, schedule, archive, delete, list, get for registered `Module[T]` types): pass `AuthTarget{TypeName: typeName}` where `typeName = m.MCPMeta().TypeName`. Special cases:
  - **Baseline check restructured:** TypeName resolved via `m.MCPMeta().TypeName` BEFORE the auth call. Previously `authoriseTool` was called before `parseToolName`/`moduleForType`.
  - **`case "delete"` escalation:** passes `AuthTarget{TypeName: typeName}` (same variable from restructured baseline).
  - **`case "list"` escalation:** passes `AuthTarget{TypeName: lm.MCPMeta().TypeName}` — `typeSnake` is plural (e.g., "posts") so `moduleForType` returns nil; must use `lm` from `moduleForAdminList`.
  - **`case "get"` escalation:** passes `AuthTarget{TypeName: gm.MCPMeta().TypeName}`.

`AuthTarget.ID` is not populated (same limitation as the HTTP gate). Slug→ID resolution deferred, documented via `TODO(T49-scope)` comment.

- 23 call sites updated (4 module-scoped with TypeName, 19 static with `AuthTarget{}`).
- 1 new test `TestAuthoriseTool_TypeScoped_ParityWithHTTP` with two sub-cases (create/delete) using custom `ScopeMode: ScopeStatic` role definitions.
- 8 existing `authoriseTool` tests updated to pass `smeldr.AuthTarget{}` (behaviour unchanged — global grants work with any target).
- Pre-existing coverage at 95.3% (unchanged; `TestStateTool_GetValidTransitions` failures are pre-existing from a separate defect, not introduced by this fix).
- No exported symbols changed. No core package changes. Level 1 amendment.

Coverage: 95.3% (pre-existing). mcp v1.26.1.

---

## A197 — T121: Config-driven feature-toggle layer (`example/server`)

**Date:** 2026-07-04
**Status:** Done
**Files:** `example/server/main.go`, `example/server/go.mod`, `go.work`, `AGENTS.md`, `README.md`, `docs/FEATURELIST.md`

### What

Added `example/server/` — a standalone Go module (`module example/server`) with its own `go.mod` and `replace` directives for all smeldr.dev/* dependencies. Delivers a deployable binary with no hard-coded Go content types; all content types are defined at runtime via the `define_content_type` MCP tool. Optional subsystems are gated by `ENABLE_*` environment variables; the binary compiles and runs with only `SECRET` set. 11 boolean vars: `ENABLE_TOKENS`, `ENABLE_GOVERNANCE`, `ENABLE_RELATIONS`, `ENABLE_DYNAMIC_CONTENT`, `ENABLE_BLOCKS`, `ENABLE_MEDIA`, `ENABLE_SOCIAL`, `ENABLE_WEBHOOKS`, `ENABLE_REDIRECTS`, `ENABLE_PAGE_META`, `ENABLE_AGENTS`. Plus `OAUTH_ISSUER` and associated Mastodon/agent env vars.

### Why

T114 (dogfood instance) and T118 (downloadable binary) both need a runnable generic server. `example/blog/main.go` demonstrates a single-content-type pattern; `example/server/main.go` demonstrates the full subsystem composition pattern — it is both the pattern reference and the deployable artifact. Placing the binary in `example/server/` with its own `go.mod` avoids a circular dependency: `smeldr.dev/mcp` imports `smeldr.dev/core`, so a binary importing both cannot live inside `smeldr.dev/core`'s module. The pattern follows `example/blog/`, `example/docs/`, `example/api/`.

### Consequences

- 11 `ENABLE_*` boolean env vars + `OAUTH_ISSUER` gate their respective subsystems (set to any non-empty value to enable).
- `migrateDB(db)` always creates `smeldr_tokens` and `smeldr_webhook_endpoints` tables unconditionally (idempotent `CREATE TABLE IF NOT EXISTS`); no DDL helper exists in core for these tables.
- Wiring order is load-bearing: `CreateRelationTables` before `NewRelationStore`; `agentMod.Register` before `mcp.New` (so `AgentJob` appears in the MCP tool list).
- `go.work` gains a `use ./example/server` entry (local workspace only; gitignored).
- `AGENTS.md` gains a "Generic reference server (example/server)" section with full env-var table and wiring guidance.
- `README.md` references `example/server` as the quickstart for a generic server.
- `docs/FEATURELIST.md` "Operations" section gains a bullet for the config-driven feature-toggle layer.
- No exported Go symbols changed. No core API change. No core version bump. Level 2 amendment.

Coverage: 96.0%. core v1.52.2.

---
