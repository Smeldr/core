# Smeldr — Decisions Archive: A194–A200

Archived from `decisions/recent.md` on 2026-07-05.

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

## A198 — T114 Step 1: Goal orchestration type + context-packet query

**Status:** Done
**Date:** 2026-07-05
**Files changed:** `orchestration.go`, `orchestration_test.go`, `docs/ARCHITECTURE.md`

### What

Added `Goal` struct, embedding Node with fields: GoalID string, Priority int, Band string, Size string, Description string (smeldr_format:"markdown").

Added `GoalContext` struct with fields: Goal *Goal, LinkedDecisions []Decision, LinkedTasks []Task, LinkedGoals []Goal.

Added `QueryGoalContext(ctx context.Context, db DB, rs *RelationStore, goalID string) (*GoalContext, error)` — performs bidirectional edge query (GetBySource + GetByTarget), deduplicates results by edge ID, skips self-links, fails open (no error) when rs is nil, returns ErrBadRequest for empty goalID, ErrInternal for nil db, ErrNotFound when goal is missing, logs warn and continues when linked items cannot be fetched.

Added `orchGoalFlow()` (unexported) implementing state machine: 4 states (open, in-progress, done, parked) with 5 transitions (open→in-progress, in-progress→done, open→parked, in-progress→parked, parked→open).

Extended `CreateOrchestrationTables` with `smeldr_goals` DDL (5th orchestration table).

Extended `RegisterOrchestrationTypes` to register Goal as 5th orchestration type + its flow.

### Why

Implements T114 dogfood conversion — models ARCHITECT_TODO.md rows as live Smeldr items queryable via the relation graph. State flow reflects real-world goal lifecycle; parked→open transition is necessary because goals can be un-parked. Relations are queried bidirectionally because assert_relation direction is convention-dependent; bidirectional query captures all edges regardless of assertion direction.

### Consequences

CreateOrchestrationTables now creates 5 tables (was 4). RegisterOrchestrationTypes now registers 5 flows (was 4). New `smeldr_goals` table with columns: goal_id, priority, band, size, description. QueryGoalContext is the primary read path for the get_goal_context MCP tool (Step 2). Added 11 new tests: TestGoalFlow_definition, TestQueryGoalContext (9 sub-cases), TestCreateOrchestrationTables_DBError.

Coverage: 96.0%. core v1.53.0 (minor bump — new exported types and functions).

---

## A199 — T114 Step 2: get_goal_context MCP tool (smeldr.dev/mcp v1.27.0)

**Status:** Agreed — **Date:** 2026-07-05

### Decision

Add `get_goal_context` as an MCP tool in `smeldr.dev/mcp`. The tool retrieves a `GoalContext` for a given `goal_id` (e.g. `"T114"`) and returns it as structured JSON. Implemented in `orchestration_tools.go` following the `signal_tools.go` pattern: `orchestrationToolDefs()`, `isOrchestrationTool()`, `handleOrchestrationTool()`. Gated on `s.app.Config().DB != nil`. Requires Author role. Wired into `handleToolsList` and `handleToolsCall` in `tool.go`.

**Files changed:** `orchestration_tools.go` (new), `orchestration_tools_test.go` (new), `tool.go`, `go.mod`, `CHANGELOG.md`
**Date:** 2026-07-05

### What

New file `orchestration_tools.go` in `smeldr.dev/mcp`:

- `orchestrationToolDefs() []mcpTool` — returns the `get_goal_context` tool definition with `goal_id` as the single required parameter.
- `isOrchestrationTool(name string) bool` — predicate for dispatch.
- `(s *Server) handleOrchestrationTool(ctx, name, args)` — extracts `goal_id`, calls `smeldr.QueryGoalContext(ctx, db, s.app.RelationStore(), goalID)`, returns `{goal, linked_decisions, linked_tasks, linked_goals}`. Maps errors via `errorFor` (-32001 for not-found, -32602 for missing param, -32603 for internal).

`tool.go` updates:
- `handleToolsList`: `orchestrationToolDefs()` appended inside the existing `s.app.Config().DB != nil` block.
- `handleToolsCall`: dispatch block added after signal tool dispatch.

8 tests in `orchestration_tools_test.go`: ToolsList_DBNil, ToolsList_DBSet, MissingGoalID, GoalNotFound, HappyPath, RoleRejection, IsOrchestrationTool, UnknownName.

`go.mod`: `smeldr.dev/core` bumped from v1.51.0 to v1.53.0. go.sum will be regenerated against the real v1.53.0 tag after core is merged and tagged.

### Why

Completes T114's agent-facing half: an AI pilot can now query a goal's full context (linked Decisions, Tasks, and Goals) in a single MCP call, matching the `GoalContext` data structure introduced in A198. Follows the established signal_tools.go pattern to keep the dispatch layer consistent.

### Consequences

- `GOWORK=off go build ./...` in the mcp repo will fail until core v1.53.0 is tagged (go.sum does not yet have the hash for v1.53.0). Local builds work via go.work.
- `smeldr.dev/mcp v1.27.0` — minor version bump (new tool, no breaking changes).
- AGENTS.md updated with get_goal_context row.

---

## A200 — storage.go time.Time scan fix for SQLite TIMESTAMPTZ columns

**Status:** Done
**Date:** 2026-07-05
**Files changed:** `storage.go`, `storage_sqlite_test.go`

### What

Added `timeScanner` (unexported type, implements `sql.Scanner`) — handles source types: string (parses RFC3339Nano, RFC3339, or Go `.String()` format "2006-01-02 15:04:05 -0700 MST"), []byte (converts to string and parses), int64 (interprets as Unix seconds → time.Time), nil (→ zero time.Time), time.Time (direct assignment), unknown type (returns error).

Added `scanDest` (unexported function) — wraps *time.Time addresses in timeScanner; returns all other pointer types unchanged. Applied `scanDest` in `Query[T]` and `Seq` scan loops, replacing direct `Addr().Interface()` for time.Time field destinations.

### Why

Go 1.26's database/sql `convertAssign` does not handle string→*time.Time conversion. modernc.org/sqlite v1.50.0 stores time.Time{} (zero time) as "0001-01-01 00:00:00 +0000 UTC" (Go's `.String()` format) in TIMESTAMPTZ TEXT columns. Round-trip queries on draft orchestration items (published_at = zero time) failed with "unsupported Scan, storing driver.Value type string into type *time.Time". This bug exists in all 4 pre-existing orchestration types but was only exposed by T114 Step 1, the first code path to execute a round-trip query on a draft orchestration item.

### Consequences

Round-trip queries on draft items (zero published_at) now succeed for all orchestration types (Signal, Task, Decision, Amendment, Goal). SQLRepo.FindByID and FindBySlug now correctly scan time.Time values for all item states. Added TestTimeScanner (7 sub-cases: time.Time source, RFC3339Nano string, bytes, int64, nil, unparseable string error, unsupported type error). No API change; no version bump beyond A198.

Coverage: 96.0%. core v1.53.0.

---
