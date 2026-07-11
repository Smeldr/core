# Changelog

All notable changes to Smeldr are documented in this file.

Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Versioning: [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

**API stability promise:** every exported symbol in `smeldr.dev/core`
at v1.0.0 is stable. No breaking changes will be made without a new major version.
The zero-dependency policy and zero-reflection-at-request-time guarantee are
treated as part of the stability promise.

**Architectural rationale:** see [DECISIONS.md](DECISIONS.md) for the reasoning
behind every design choice. Amendments that changed existing behaviour are
cross-referenced below by their Amendment ID.

---

## [Unreleased]

Changes planned for v2 and beyond are tracked in [BACKLOG.md](BACKLOG.md)
under Milestone 10 and the v2+ Roadmap section.

---

## [1.55.0] — 2026-07-11

### Added
- `context_packet.go`: `ContextPacket`, `PacketSource`, `PacketAnchor`, `PacketBoundary`, `PacketOmission`, `PacketItem`, `PacketRelation` — v1 bounded operational context JSON envelope types. (A214)
- `BuildContextPacket(ctx, DB, *RelationStore, baseURL, sourceName, anchorType, anchorSlug string, depth int) (*ContextPacket, error)` — breadth-first traversal over all 5 orchestration anchor types (goal, decision, amendment, task, signal), depth 1–2, per-type cap 25 items, seenEdge + seenNode dedup (diamond-safe). (A214)
- `App.ContextPacketHandler(rs *RelationStore, sourceName string)` — mounts `GET /packet/{type}/{slug}[?depth=]` unauthenticated HTTP endpoint returning a `ContextPacket` as JSON. (A214)
- Published-only lifecycle contract: Draft/Archived anchor returns `ErrNotFound`; Draft/Archived linked items are silently excluded and not counted in `Boundary.Omitted`. (A214)

---

## [1.54.2] — 2026-07-10

### Fixed
- `module.go`: `Module[T].setDB` now calls `MigrateNodeRevColumn` automatically when the underlying repository is a `*SQLRepo[T]`. Closes the latent gap that caused the 2026-07-07 production emergency patch — no operator action required; idempotent on every boot. Custom repositories (not `*SQLRepo[T]`) are unaffected. (A212)

---

## [1.54.1] — 2026-07-06

### Fixed
- `orchestration.go`: `RegisterOrchestrationTypes` now passes explicit `Table("smeldr_...")` options to all 5 orchestration `NewSQLRepo` calls. Previously, `tableName[T]()` derivation produced e.g. `"goals"` instead of `"smeldr_goals"`, making every `create_goal`, `create_signal`, `create_task`, `create_decision`, and `create_amendment` MCP call fail with "no such table" against a real SQLite database. (A205)
- `smeldr.go`: `App.Relations()` now calls `CreateSchemaTable(a.cfg.DB)` before wiring the `syncSaveHook`. Previously, `ENABLE_RELATIONS=true` without `ENABLE_DYNAMIC_CONTENT` caused "no such table: smeldr_content_type_schemas" on every content save. Fix is idempotent and nil-DB-guarded. (A206)

### Added
- `example/server`: `ServerConfig`, `ServerResult`, `parseConfig`, and `buildApp(cfg, db) (ServerResult, error)` extracted from monolithic `main()`. All subsystem failures return errors. (A204)
- `example/server/main_test.go`: `TestServerToggles` with 7 sub-cases (each `ENABLE_*` toggle). (A204)
- `example/server/preflight_test.go`: `TestPreflight` (`//go:build preflight`): builds binary, spawns OS process, polls `/_health`, probes `/goals`. Run: `go test -tags preflight -v -run TestPreflight .` (A204)

---

## [1.54.0] — 2026-07-05

### Added
- `schemas.go`: `ValidateFields(schema *ContentTypeSchema, fields map[string]any) *ValidationError` — validates complete field set for create operations; rejects unknown fields, missing required fields, and type mismatches
- `schemas.go`: `ValidatePartialFields(schema *ContentTypeSchema, patch map[string]any) *ValidationError` — validates partial field set for update operations; rejects unknown fields and type mismatches; permits absent required fields
- `dynamic.go`: `DynamicTypeRepo.ScheduleContent(ctx context.Context, id string, scheduledAt time.Time) error` — transitions content to Scheduled status with state-flow enforcement; updates `scheduled_at` and fires async triggers
- `dynamic.go`: field validation integrated into `DynamicTypeRepo.CreateDraft` (via `ValidateFields`) and `DynamicTypeRepo.UpdateFields` (via `ValidatePartialFields`)
- `dynamic.go`: `llmsStore` compact fragment wiring for dynamic types with URLPrefix; `rebuildDynamicAIIndex` regenerates `/llms.txt` after dynamic content changes

---

## [1.53.0] — 2026-07-05

### Added
- `orchestration.go`: `Goal` struct (GoalID, Priority, Band, Size, Description fields, embeds Node). `GoalContext` struct (Goal + LinkedDecisions + LinkedTasks + LinkedGoals). `QueryGoalContext(ctx, DB, *RelationStore, goalID)` — bidirectional edge traversal, deduplication, fail-open on nil RelationStore. `orchGoalFlow` — 4 states, 5 transitions, parked→open allowed. `CreateOrchestrationTables` extended with `smeldr_goals` DDL. `RegisterOrchestrationTypes` extended to 5th type + flow. (A198, T114 Step 1)
- `storage.go`: `timeScanner` (sql.Scanner for time.Time, handles string/[]byte/int64/nil/time.Time sources). `scanDest` wrapper applied in `Query[T]` and `Seq` scan loops — fixes round-trip queries on draft items where published_at is stored as a Go string by SQLite. (A200)

---

## [1.52.2] — 2026-07-04

### Fixed
- `governance.go`, `migrate.go` DDL: replace `DATETIME` with `TIMESTAMP` in 8 columns across 6 tables (`smeldr_roles` created_at/updated_at, `smeldr_role_grants` created_at, `smeldr_tool_policies` created_at, `smeldr_governance_audit` created_at, `smeldr_state_flows` created_at, `smeldr_eval_queue` eval_at/created_at). Postgres has no `DATETIME` type; SQLite accepts `TIMESTAMP` with identical NUMERIC affinity.
- `migrate.go` DDL: `BOOLEAN NOT NULL DEFAULT 0` → `DEFAULT FALSE` on `is_initial`, `is_terminal`, `suppresses_signals` in `smeldr_states`. Postgres rejects integer literals as boolean defaults.
- `migrate.go` DDL: `INTEGER PRIMARY KEY` → `TEXT NOT NULL PRIMARY KEY` on all four state-flow tables (`smeldr_state_flows`, `smeldr_states`, `smeldr_transitions`, `smeldr_transition_triggers`); FK columns updated to `TEXT NOT NULL`. SQLite's `INTEGER PRIMARY KEY` is a rowid alias (auto-increment); Postgres `INTEGER PRIMARY KEY` is not. All INSERTs now supply explicit `NewID()` values. `flowID`/`transitionID` scan types changed from `int64` to `string` throughout `state.go`, `migrate.go`, `state_test.go`, `migrate_test.go`.
- `migrate.go` DDL: `active_state` and `conflict_policy` columns included in the initial `CREATE TABLE` for `smeldr_state_flows`. Previously added only by `migrateStateFlowConflictColumns` via `PRAGMA table_info` (SQLite-only); on Postgres the PRAGMA probe fails and the columns were never created.
- `governance.go` DDL: removed `REFERENCES smeldr_tokens(id)` FK from `smeldr_role_grants.token_id`. Auth is opt-in; `smeldr_tokens` may not exist when `App.Governance()` is called. Postgres enforces FK targets at `CREATE TABLE` time; SQLite ignores them by default.
- `pgx/go.mod`: bump `smeldr.dev/core` dependency from v1.38.0 to v1.52.1 — module was created against a stale core version.
- `pgx/state_governance_integration_test.go`: fix `smeldr.State` field names `Initial` → `IsInitial`, `Terminal` → `IsTerminal`.

### Internal
- `.github/workflows/ci.yml`: add `go mod edit -replace smeldr.dev/core=../` step before integration tests in the pgx job, ensuring CI always tests pgx against the local core code of the same commit. Permanently eliminates the version-lag chicken-and-egg problem.

---

## [1.52.1] — 2026-07-04

### Changed
- **Postgres portability** — All `?` SQL placeholders converted to `$N` positional parameters across `state.go`, `governance.go`, `migrate.go`, and `dynamic.go`, matching the portable convention used in `relations.go`, `redirects.go`, `schemas.go`, and `storage.go`.
- All `INSERT OR IGNORE` statements replaced with `INSERT … ON CONFLICT (column) DO NOTHING` with explicit conflict columns, and `DefineRole`'s two-step INSERT/UPDATE sequence collapsed to a single UPSERT (`INSERT … ON CONFLICT (name) DO UPDATE SET …`) to eliminate race window; `id` and `created_at` are INSERT-only (excluded from `DO UPDATE SET`).
- `Grant`'s `IS ?` NULL comparison (SQLite-only) replaced with `IS NOT DISTINCT FROM $N` — portable across SQLite ≥ 3.39.0 and all Postgres versions; also removes duplicate `anchorID` argument.

### Added
- **Postgres integration tests** — New `integration_core_test.go` (`//go:build integration`, `package smeldr`) boots `smeldr.App` against real Postgres 16 via `database/sql` + `pgx/v5/stdlib`, covering `migrateStateFlows`, `RegisterFlow`, `migrateGovernance`, `DefineRole`, `Grant`, `Authorized`, `RoleGranted`, and `ToolPolicy`; skips when `DATABASE_URL` is unset.
- CI `.github/workflows/ci.yml` integration job extended with new step running `go test -v -tags integration ./...` from repo root alongside the existing pgx step.
- Direct dependency: `github.com/jackc/pgx/v5 v5.9.2` (stdlib driver for integration tests).

### Fixed
- `governance_test.go` `execFailDB` matchers updated from `"INSERT OR IGNORE INTO smeldr_roles/smeldr_tool_policies"` to `"INSERT INTO smeldr_roles/smeldr_tool_policies"` to match new SQL; `TestDefineRole_UpdateError` removed (second ExecContext path no longer exists after UPSERT consolidation); `TestGrant_ResolveIDError_WithAnchor` failOn updated `"scope_anchor_id=?"` → `"scope_anchor_id=$3"`.

---

## [1.52.0] — 2026-07-03

### Added
- **Governance role-based transitions + DynamicTypeRepo authorization** (`governance.go`, `state.go`, `module.go`, `dynamic.go`, T49 Step 4 core, A193):
  - `RoleStore.RoleGranted(ctx context.Context, tokenID, roleName string, target AuthTarget) (bool, error)` — Path B named-role lookup (vs `Authorized`'s Path A operation-word lookup); evaluates same three scope modes (`ScopeGlobal`, `ScopeStatic`, `ScopeDynamic`); pre-collects rows before closing cursor (SQLite single-statement constraint); fail-closed §5.5 on any DB error
  - `validateTransition(ctx, db DB, rs *RoleStore, actorID, typeName, from, to string) error` — signature extended with `*RoleStore` and `actorID`; adds fail-closed authorization zone: `required_role` NULL/empty → nil (no gate); `rs == nil` → nil (governance not wired); `actorID == ""` → nil (system path); `RoleGranted` error → `ErrForbidden`; `!ok` → `ErrForbidden`; structural zone unchanged
  - `Module[T].MCPPublish/MCPSchedule/MCPArchive` — pass `m.roleStore, ctx.User().ID` to `validateTransition`
  - `DynamicTypeRepo.WithGovernance(rs *RoleStore) *DynamicTypeRepo` — returns shallow copy with `rs` field set; wires governance into `DynamicTypeRepo.SetStatus`
  - `DynamicTypeRepo.SetStatus` — extracts actorID via local `smeldrCtxAccessor` interface; passes to `validateTransition`
  - 20 new tests (`RoleGranted`: 12 paths including global/static/wildcard/dynamic/error/malformed; `validateTransition required_role`: 5 paths; `DynamicTypeRepo.WithGovernance`: 3 paths); coverage 96.0%

---

## [1.51.0] — 2026-07-02

### Added
- **Governance module role gate + ToolPolicy** (`governance.go`, `module.go`, `smeldr.go`, T49 Step 3, A191):
  - `RoleStore.ToolPolicy(ctx context.Context, toolName string) (requiredOp string, found bool, err error)` — exact-match lookup in `smeldr_tool_policies`; returns `found=false, err=nil` when no row (avoids `ErrNoRows` leak); returns `found=true, requiredOp, nil` on hit; returns `found=false, "", err` on DB error
  - Seam between core and `smeldr.dev/mcp`: the MCP server calls `ToolPolicy` to resolve each tool's required operation before calling `RoleStore.Authorized`
  - Prefix-pattern fallback for runtime-defined content types (T104) deferred to Step 8
  - `Module[T].roleStore *RoleStore` field injected from `App.Handler()` via `setRoleStore(*RoleStore)`
  - `Module[T].canReadDrafts(ctx Context) bool` — 3-branch: `roleStore == nil` → legacy `ctx.User().HasRole(Author)`; `roleStore != nil && ctx.User().ID == ""` → deny; `roleStore != nil && ID != ""` → `RoleStore.Authorized(ctx, ID, "read", AuthTarget{TypeName})` (fail-closed §5.5 on error)
  - `Module[T].checkWriteOp(ctx Context, op string, legacyRole Role) bool` — same 3-branch pattern
  - `Module[T].isVisible(ctx Context, item any) bool` — converted from standalone function to Module method; Published items always visible; Draft delegates to `canReadDrafts`
  - All 4 `isVisible` call sites, 2 list-handler status-filter sites, and 3 write/delete gate sites updated to use new helpers
  - `App.governanceModules []interface{ setRoleStore(*RoleStore) }` — modules register at `App.Content()` time; `App.Handler()` injects the `RoleStore` into all registered modules
  - 20 new tests (3 `TestRoleStore_ToolPolicy_*` + 17 `TestModule_canReadDrafts_*` / `TestModule_checkWriteOp_*` / `TestModule_isVisible_*` / `TestModule_createHandler_GovernanceWired_*`); coverage 96.0%

---

## [1.50.0] — 2026-07-02

### Added
- **Governance mutation audit trail** (`governance.go`, T49 Step 2.5, A190):
  - `GovernanceAuditRecord` struct: `ID`, `ActorTokenID`, `Action` ("define_role" | "grant" | "revoke"), `TargetKind` ("role" | "grant"), `TargetID`, `Before` (JSON), `After` (JSON), `CreatedAt`
  - `GovernanceAuditStore` interface: `Append(ctx, GovernanceAuditRecord) error`
  - `NewGovernanceAuditStore(db DB) GovernanceAuditStore` — SQL-backed implementation
  - `CreateGovernanceAuditTable(db DB) error` — creates `smeldr_governance_audit` table + `idx_governance_audit_actor` index; opt-in (not called by `App.Governance`)
  - `RoleStore.WithAudit(actorTokenID string, log GovernanceAuditStore) *RoleStore` — returns shallow copy with audit wired to the given actor and store
  - `DefineRole`, `Grant`, `Revoke` capture before-state JSON, run the mutation, then call `log.Append` with a `GovernanceAuditRecord`; fail-closed on Append error
  - **Non-atomic semantics:** the underlying DB has no transaction primitive; if `Append` fails after the mutation succeeds, the error return means "the mutation may have already taken effect — verify current state before retrying." `DefineRole` and `Grant` are idempotent on retry; `Revoke` is idempotent by nature.

---

## [1.49.0] — 2026-07-02

### Added
- **Role-based authorization** (`governance.go`, T49 Step 2, A189):
  - `RoleDefinition` struct: `Name`, `Operations`, `ScopeMode`, `ScopeRelationKind`, `ScopeDirection`, `TrustLevel`, `AllowSelfApproval`
  - `RoleGrant` struct: `ID`, `TokenID`, `RoleName`, `ScopeStatic`, `ScopeAnchorID`, `CreatedAt`
  - `AuthTarget` struct: `TypeName`, `ID`, `Slug` (`Slug` is display/logging only — not used in authorization comparisons)
  - `RoleStore` struct and `NewRoleStore(db DB)` constructor
  - `RoleStore.DefineRole(ctx, RoleDefinition)` — upsert role by name; rejects `TrustLevel == 1` (semantics undefined until future spike)
  - `RoleStore.Grant(ctx, RoleGrant)` — bind token to role with scope data (static list or dynamic anchor); `WHERE NOT EXISTS` guard prevents NULL-anchor duplicates
  - `RoleStore.Revoke(ctx, grantID)` — delete grant by ID (idempotent)
  - `RoleStore.ListGrants(ctx, tokenID)` — list all grants for a token (empty `tokenID` = all grants)
  - `RoleStore.Authorized(ctx, tokenID, op, AuthTarget) (bool, error)` — evaluates three scope modes: `ScopeGlobal` (always matches), `ScopeStatic` (`TypeName+":"+ID` and `TypeName+":*"` wildcards), `ScopeDynamic` (one-hop `smeldr_relations` query filtered to `edge_class='asserted'` and `invalid_at IS NULL OR invalid_at > now`); pre-collects all grant rows before closing cursor (avoids SQLite nested-connection deadlock)
  - `App.Governance(store *RoleStore) error` — wires store; validates `store.db == cfg.DB`; runs `migrateGovernance`
  - `App.RoleStore() *RoleStore` — accessor for the wired store
  - `App.governance *RoleStore` field added to `smeldr.go`

---

## [1.48.0] — 2026-07-02

### Added
- **Governance schema** (`governance.go`, T49 Step 1, A188):
  - `ScopeMode` type with `ScopeGlobal`, `ScopeStatic`, `ScopeDynamic` constants
  - `smeldr_roles` table: named role templates with full-word operations JSON array, scope mode, trust level, and self-approval flag
  - `smeldr_role_grants` table: binds a token to a role with concrete scope data (static list or dynamic anchor); unique guard via `WHERE NOT EXISTS` (SQLite `NULL`-in-`UNIQUE` makes `INSERT OR IGNORE` unsafe for global-scope grants)
  - `smeldr_tool_policies` table: maps each MCP tool name to a required operation word; zero behaviour change on day one
  - Default roles seeded: `author` (`["create","read","update","publish","archive"]`), `editor` (+`delete`, `manage`), `admin` (+`delete`, `manage`, `administer`, `review`, `approve`, `define-type`, `define-flow`, `define-relation-kind`); all `scope_mode='global'`, `trust_level=0`
  - Operation vocabulary: `manage` (Editor-tier operational tools: composition, transitions, nav CRUD, redirect CRUD, dynamic content), `administer` (Admin-only infra: tokens, webhooks, page-meta); `approve`/`review` reserved for the Plan governance loop (§6)
  - `migrateTokenGrants`: migrates existing `smeldr_tokens.role` values into `smeldr_role_grants` (global scope); fail-open when `smeldr_tokens` is absent
  - `migrateGovernance` is **not** called from `New()` — opt-in via `App.Governance()` (T49 Step 2)

---

## [1.47.0] — 2026-07-01

### Added
- **State flow transitions** — new `TransitionTrigger` type to model state machine transitions:
  - Struct fields: `FromState`, `ToState`, `TriggerClass`, `TriggerType`, `Config`
  - `StateFlow.Triggers []TransitionTrigger` persisted via `App.RegisterFlow` to `smeldr_transition_triggers` table (T23 Step 13, A187)
- **Async evaluation queue** — new `smeldr_eval_queue` table to schedule deferred state evaluations:
  - Columns: `id`, `type_name`, `item_id`, `to_state`, `eval_at`, `created_at`
  - Unique constraint on `type_name + item_id + to_state`
- **`App.DrainEvalQueue(ctx context.Context) (triggered, skipped int, err error)`** — drain and apply due evaluations:
  - Reads from `smeldr_eval_queue` where `eval_at <= now`
  - Applies direct UPDATE to target item row; deletes queue entry regardless of success
  - Fail-open on nil DB and missing table
- **`schedule-eval` trigger type** — new async transition handler in state flows:
  - Reads `eval_field` from item row and INSERTs into `smeldr_eval_queue` with computed `to_state`
  - Enables deferred state transitions (e.g., schedule re-evaluation at a future time)
- **Orchestration decision flow wired** — `orchDecisionFlow()` now includes two `schedule-eval` triggers:
  - `proposed → ratified` and `pending-re-evaluation → ratified` with `eval_field: next_eval_at`

### Changed
- **`fireAsyncTriggers` signature** — added `itemID string` parameter to support queue insertion

### Internal
- **`resolveItemTable(ctx, db, typeName) string`** — probes sqlite_master for `smeldr_<snake>s` table, falls back to `<snake>s`, then `smeldr_dynamic_content`
- **`isNoSuchTable(err) bool`** — helper to distinguish table-not-found errors in async handlers

---

## [1.46.0] — 2026-07-01

### Added
- ConflictPolicy enforcement on StateFlow (T23 Step 12, A186):
  - `ConflictPolicy` type with `ConflictReject` and `ConflictSupersede` constants
  - `StateFlow.ActiveState` and `StateFlow.ConflictPolicy` optional fields
  - `migrateStateFlowConflictColumns`: adds `active_state`/`conflict_policy` columns on boot (idempotent)
  - `RegisterFlow`: persists `ActiveState` and `ConflictPolicy` via UPDATE after INSERT OR IGNORE
  - `applyConflictPolicy` (unexported): ConflictReject returns ErrConflict; ConflictSupersede transitions
    conflicting items to "superseded" + optional "supersedes" relation; all DB errors fail-open
  - Wired into `Module[T]` (MCPPublish/MCPSchedule/MCPArchive) and `DynamicTypeRepo.SetStatus`

---

## [1.45.1] — 2026-06-30

### Fixed
- Race condition in `state_test.go` under `go test -race`: `TestFireAsyncTriggers_asyncTrigger_dispatched`
  and `TestDynamicTypeRepo_SetStatus_fireAsyncTriggers` used a bare `bytes.Buffer` as the slog
  handler target while `fireAsyncTriggers` wrote to it concurrently from a spawned goroutine.
  Replaced with a mutex-protected `safeBuf` wrapper in three affected test functions. No
  production code changed. (A184)

---

## [1.45.0] — 2026-06-30

### Added
- Orchestration content types in `orchestration.go` (T23 Step 10, A183):
  - `Signal` — protocol message (sender, receiver, signal_type, message, task_ref, sequence)
  - `Task` — work item (task_id, priority, band, size, description, note_ref)
  - `Decision` — ratified architectural decision (decision_number, scope, body, next_eval_at, eval_note)
  - `Amendment` — committed changeset (amendment_number, amendment_type, version, commit_hash, pilot, summary)
- `CreateOrchestrationTables(db DB) error` — creates `smeldr_signals`, `smeldr_tasks`, `smeldr_decisions`, `smeldr_amendments` tables.
- `RegisterOrchestrationTypes(app *App, db DB)` — registers all four orchestration content types with custom state flows and MCP read+write tools. Fail-open on nil DB. Flows: signal-protocol (4 states, 4 transitions), architect-task (9 states, 9 transitions), governance-decision (5 states, 7 transitions), amendment-lifecycle (6 states, 6 transitions).

### Changed
- `type Signal string` renamed to `type LifecycleEvent string` in `signals.go` (A183). Signal constants retain their names (BeforeCreate, AfterCreate, BeforeUpdate, AfterUpdate, BeforeDelete, AfterDelete, AfterPublish, AfterUnpublish, AfterArchive, AfterSchedule, SitemapRegenerate, AfterRelationCascade); only the type annotation changes. Updated signatures: `On[T]`, `OnSignal`, `AddSignalListener`, `dispatchBus`, `emitSignal`, `notifyAfter`, `setAfterHook`, `signalToEventSuffix`, `buildEventName`, `buildWebhookPayload`, `webhookDispatch`. `AuditRecord.Signal` field type changed to `LifecycleEvent`.

---

## [1.44.3] — 2026-06-30

### Internal
- `fireAsyncTriggers(ctx context.Context, db DB, typeName, fromState, toState string)` (unexported, `state.go`):
  queries `smeldr_transition_triggers` (one JOIN with `smeldr_transitions` + `smeldr_state_flows`) for
  `trigger_class='async'` rows matching the transition. Dispatches each trigger in a goroutine with
  panic recovery. Unknown `trigger_type` values log `slog.Warn` — concrete handlers come in Steps 10+.
  Fail-open on all error paths (query error, scan error, rows.Err). (T23 Step 7, A180)
- `DynamicTypeRepo.SetStatus` (`dynamic.go`): calls `fireAsyncTriggers` after a successful `ExecContext`.
  FROM state is `node.Status` captured by `GetByID` before the update. (T23 Step 7, A180)

---

## [1.44.2] — 2026-06-30

### Changed
- `notifyAfter` now suppresses all After* signals (AfterPublish, AfterArchive, AfterSchedule,
  AfterUpdate, AfterCreate, AfterDelete, AfterUnpublish, AfterRelationCascade) and the
  afterHook when an item's current state has `suppresses_signals=true` in its registered
  state flow. Identity transitions and items without a custom flow fall back to default flow
  rules; nil DB or non-SQLite queries return false (allow signals). (T23 Step 6, A179)

### Internal
- `suppressesSignals(ctx context.Context, db DB, typeName, statusName string) bool` (unexported, `state.go`):
  checks `smeldr_states.suppresses_signals` for the item's current status in its registered
  flow. Same pattern as `validateTransition`: nil DB → false; sqlite_master probe fails →
  false (non-SQLite, fail-open); custom flow lookup → default flow fallback → false if no
  flow found; scan error → false (fail-open). (T23 Step 6, A179)
- Early-return guard in `notifyAfter` (`module.go`): `if suppressesSignals(ctx, m.db, m.contentTypeName, string(nodeStatusOf(item))) { return }`
  inserted before signal snapshot and dispatch. (T23 Step 6, A179)

---

## [1.44.1] — 2026-06-29

### Changed
- `DynamicTypeRepo.SetStatus` now validates the requested transition against the registered
  state flow before writing. Custom flow states (e.g. `"paused"`, `"queued"`) are accepted
  when permitted by the flow; disallowed transitions return `ErrConflict` (409). Types
  without a custom flow fall back to the built-in default flow. (T23 Step 3, A176)
- `newSetStatusHandler` (`POST /_content/{type}/{id}/status`): hardcoded
  `Draft/Published/Archived/Scheduled` enum guard removed. An empty `status` field still
  returns 400; disallowed transitions now return 409 instead of 400. (T23 Step 3, A176)
- `MCPPublish`, `MCPArchive`, `MCPSchedule`: each now calls `validateTransition` before
  updating item status. Returns `ErrConflict` when the transition is not permitted by the
  registered flow. Identity transitions (already in the target state) are always allowed
  to preserve MCP idempotency. (T23 Step 3, A176)

### Internal
- `validateTransition` (unexported, `state.go`): checks `(flow_id, from_state, to_state)`
  in `smeldr_transitions`; falls back to default flow; nil DB and non-SQLite return nil
  (no-op). (T23 Step 3, A176)
- `Module[T]` gains unexported `db DB` field and `setDB(DB)` method; wired by
  `App.Content` via the same type-assertion pattern as `setSecret`. (T23 Step 3, A176)

---

## [1.44.0] — 2026-06-29

### Added
- `StateFlow`, `State`, `Transition` types and `App.RegisterFlow(flow StateFlow) error`
  method for custom state-machine registration. Flows are idempotently upserted at startup
  via INSERT OR IGNORE. (T23 Step 2, A175)
- `validateFlowItems` (unexported) SQLite-only validation: checks that all existing items
  of a given type are in states defined by their flow; returns detailed error listing unknown
  states for operator remediation before startup. (T23 Step 2, A175)

---

## [1.43.3] — 2026-06-29

### Added
- State-flow schema: `smeldr_state_flows`, `smeldr_states`, `smeldr_transitions`,
  `smeldr_transition_triggers` tables created automatically at startup via
  `migrateStateFlows()`. The default flow (draft → scheduled → published → archived)
  is seeded idempotently. Prerequisite for T23 Steps 2–9. (T23 Step 1, A174)

---

## [1.43.2] — 2026-06-29

### Fixed
- `serveblocks`: corrected `FilterTags` type assertion from `.(string)` to `.([]any)`.
  `FilterTags` is defined as `Type: "array"` in schemas.go; the previous assertion always
  silently failed, making the "not yet supported" warning dead code. (T108, A173)

---

## [1.43.1] — 2026-06-21

### Added
- Publish-time slug collision check for aggregate routes: before transitioning an item to Published (via HTTP create, HTTP update, or `MCPPublish`), all registered slug checkers query sibling modules in the same aggregate route. A collision returns `ErrConflict` (HTTP 409) with a message naming the colliding type and slug. (A169)
- `(*App).Route` now wires `slugCheckable` cross-module checkers alongside the existing `cacheInvalidatable` cross-module invalidators for each aggregate spec pair. (A169)
- `insertDynamicRoutes` — internal helper that writes `route_type='content'` list and item rows to `smeldr_routes` when a dynamic content type has a non-empty `url_prefix`. Called from `DefineContentType` and `loadDynamicTypes` (idempotent via `INSERT OR IGNORE`). (A169)
- `loadDynamicTypes` now backfills `smeldr_routes` for all types registered before this amendment; idempotent on repeated restarts. (A169)

### Fixed
- Cache invalidation cycle: cross-module aggregate wiring now calls `flushOwnCache()` instead of `invalidateCache()` on the target module, preventing infinite recursion when two modules invalidate each other. (A169)
- Aggregate handler slug and sort key corrected from `"slug"`/`"published_at"` to `"Slug"`/`"PublishedAt"` (PascalCase), matching the keys returned by both `Module[T].ListPublished` and `DynamicTypeRepo.List`. (A169)

---

## [1.43.0] — 2026-06-21

### Added
- `type Listable interface { ListPublished(ctx context.Context, opts ListOptions) ([]map[string]any, error) }` — exported interface for modules that expose published content to aggregate routes and ContentList resolvers; replaces the unexported `ContentLister`. (A168)
- `func Serves[T any](m *Module[T]) *ServesSpec` — single-type route spec builder; returns a `*ServesSpec` that can produce `List()` or `Show()` `RouteSpec` values. (A168)
- `func Aggregate(specs ...*ServesSpec) *AggregateSpec` — multi-type route spec builder; combines two or more `*ServesSpec` values. (A168)
- `(*AggregateSpec).List() RouteSpec` / `(*AggregateSpec).Show() RouteSpec` — produce aggregate-list and aggregate-item specs respectively. (A168)
- `(*App).Route(pattern string, spec RouteSpec)` — registers a route in the in-memory `routeReg`; for aggregate specs also wires a parallel-fetch JSON handler on the mux and cross-links `cacheInvalidators` between all participating modules. (A168)
- `aggregate.go` — internal parallel aggregate handler: `ListPublished` called concurrently on all specs, results merged and sorted by `published_at` descending (list view); slug-matched across all specs (show view). (A168)

### Changed
- `(*Module[T]).listPublished` renamed to `ListPublished` (exported) — satisfies the new `Listable` interface. (A168)
- `App.Content` — now auto-populates `routeReg` with `list` and `item` entries for any module registered with `At(prefix)`. (A168)

### Notes
Slug collision at route-definition time is deferred; see DECISIONS.md.

---

## [1.42.9] — 2026-06-21

### Added
- `func CreateRoutesTable(db DB) error` — creates `smeldr_routes` (unified table for content, redirect, and gone routes) with `id`, `path_pattern` (UNIQUE), `route_type`, `view`, `type_names`, `redirect_to`, `status_code`, `is_prefix`, `created_at`, `updated_at`. Replaces `CreateRedirectsTable`. (A167)
- `func MigrateRedirectsToRoutes(db DB) error` — idempotent migration: copies all rows from `smeldr_redirects` → `smeldr_routes` (route_type='redirect') and drops the source table; no-op if `smeldr_redirects` does not exist. (A167)

### Changed
- `(*RedirectStore).Load` — reads from `smeldr_routes WHERE route_type='redirect'` instead of `smeldr_redirects`. (A167)
- `(*RedirectStore).Save` — upserts into `smeldr_routes` with `route_type='redirect'` and RFC-3339 timestamps. (A167)
- `(*RedirectStore).Remove` — deletes from `smeldr_routes WHERE path_pattern=$1 AND route_type='redirect'`. (A167)
- `(*App).Redirects(db DB)` — now calls `CreateRoutesTable` + `MigrateRedirectsToRoutes` instead of `CreateRedirectsTable`. (A167)

---

## [1.42.8] — 2026-06-21

### Added
- `type TargetChecker func(ctx context.Context, targetType, targetID string) (alive bool, err error)` — exported function type for checking whether a relation target is still live; used by `RelationStore.SweepStructural`. (A165)
- `(*RelationStore).SweepStructural(ctx context.Context, check TargetChecker, onStale func(ctx context.Context, edge RelationEdge)) (flagged int, skipped int, err error)` — iterates all active relations (`invalid_at IS NULL OR invalid_at > now` AND `valid_at IS NULL OR valid_at <= now`), deduplicates targets via map so each unique target is checked once, marks stale edges by setting `invalid_at = now`, and calls `onStale` per edge. Returns `(flagged, skipped, error)`. (A165)
- `(*App).SweepStructural(ctx context.Context) (flagged, skipped int, err error)` — convenience wrapper; returns `(0, 0, nil)` if no `RelationStore` is configured. Default `TargetChecker` queries `smeldr_dynamic_content` by id and checks `status = 'published'`; default `onStale` fires `AfterRelationCascade` via `emitSignal`. (A165)

### Notes
`SweepStructural` is a one-hop structural validity check: it does not make LLM calls, does not create `AgentJob` rows, does not call MCP tools, and does not traverse relations transitively. Cron scheduling lives in `smeldr/agent` (Layer 3b — separate step).

---

## [1.42.7] — 2026-06-20

### Tests
- Added comprehensive error-path coverage for `relations.go` and `dynamic.go`: 3 new test files (`relations_errors_test.go`, `dynamic_errors_test.go`, and additions to `coverage_test.go`) covering 30+ error scenarios including database failures, invalid input, and edge cases in relation management and dynamic type operations. Also covered success paths for module listing and field collection. Statement coverage improved from 95.3% to 96.2%, meeting gate requirement. (A164)

---

## [1.42.6] — 2026-06-20

### Added
- `(*RelationStore).MCPUpsertRelationKind(ctx context.Context, def RelationKindDef) (RelationKindDef, error)` — registers or updates a relation kind; validates via `ValidateRelationKindDef`, persists, and returns the stored definition with generated ID and timestamps. Returns an error on invalid `mode` or empty `type_name`. (A163)
- `(*RelationStore).MCPListRelationKinds() []RelationKindDef` — returns all registered relation kinds sorted by `type_name`; always returns a non-nil slice. (A163)

### Notes
These two tools complete the MCP surface for T06. Agents can now register kinds and assert/query/preview relations entirely through MCP without touching application code.

Gate: `App.RelationStore() != nil` — forge-mcp checks this before registering both tools.

---

## [1.42.5] — 2026-06-20

### Added
- `(*RelationStore).MCPAssertRelation(ctx, sourceType, sourceID, targetType, targetID, relationKind string, confidence *float64, validAt, invalidAt *time.Time, attributes json.RawMessage) (RelationEdge, error)` — manually asserts a typed edge; returns `ErrNotFound` if the kind is not registered. (A162)
- `(*RelationStore).MCPProposeRelation(...)` — identical signature to `MCPAssertRelation` but stores `edge_class: "inferred"` for human or agent review. (A162)
- `(*RelationStore).MCPGetRelations(ctx, typeName, id, direction, kind string) ([]RelationEdge, error)` — queries the relation graph; `direction` is `"source"`, `"target"`, or `"both"`; `kind=""` returns all kinds. (A162)
- `(*RelationStore).MCPPreviewImpact(ctx, typeName, id string) ([]RelationEdge, error)` — dry-run: returns source-side dependents without firing any cascade signals. (A162)

### Changed
- `(*RelationStore).Assert` — now delegates to unexported `insertEdge`; behaviour unchanged. (A162)

### Wiring
`App.RelationStore()` is the gate for relation MCP tools. forge-mcp checks non-nil before registering the four tools.

---

## [1.42.4] — 2026-06-20

### Added
- `AfterRelationCascade Signal = "relation.cascade"` — fires when a content item's relation premise changes (target published, archived, deleted, or unpublished). Fired once per source-side dependent, debounced 500 ms per source item. (A161)
- `SignalEvent.NodeID string` — stable UUID of the content item. Populated for all signals; previously absent. (A161)
- `App.emitSignal` (unexported) — fires registered OnSignal handlers directly from inside a signal handler goroutine. Safe for use in cascade handlers. (A161)

### Changed
- `App.Relations()` — now subscribes to `AfterPublish`, `AfterArchive`, `AfterDelete`, and `AfterUnpublish` to drive Layer 2 cascade propagation. (A161)

---

## [1.42.3] — 2026-06-20

### Added
- `(*RelationStore).RecomputeAsserted(ctx, sourceType, sourceID string, incoming []RelationEdge) error` — Layer 1 differential save-path edge recompute: SELECT current asserted edges, compute diff, delete stale, insert new. Common case (no field changes) costs exactly one SELECT and zero writes. (A160)
- `(*RelationStore).BulkRecompute(ctx, items []RelationSource) error` — batched post-import variant: all SELECTs first, then all deletes + inserts. (A160)
- `RelationSource` struct — carries `SourceType`, `SourceID string`, and `Incoming []RelationEdge` for `BulkRecompute`. (A160)
- `SyncSaveHook` type — `func(ctx context.Context, typeName, id string, item any) error`. (A160)
- `App.Relations()` now builds a `SyncSaveHook` closure: reads the item's registered schema, finds `Relation: "edge"` fields, extracts target IDs, calls `RecomputeAsserted`. No-op for compiled types (no schema entry). (A160)

### Changed
- `Module[T].createHandler`, `.updateHandler`, `.MCPCreate`, `.MCPUpdate` — call `SyncSaveHook` synchronously after `repo.Save`; error aborts the request. (A160)

---

## [1.42.2] — 2026-06-20

### Added
- `RelationKindDef` — Go struct for named relation-kind registrations (`type_name`, `mode`, `directional`, `weighted`, `type_pairs`, `attributes`).
- `RelationEdge` — Go struct for typed adjacency edges; does not embed `Node` (graph edges are not content items).
- `RelationKindRegistry` — in-memory thread-safe registry, hydrated from `smeldr_relation_kinds` at startup.
- `RelationStore` — wraps `DB`; holds the registry; created with `NewRelationStore(db DB)`.
- `CreateRelationTables(db DB) error` — idempotent DDL creator for `smeldr_relation_kinds`, `smeldr_relations`, and three indexes (`idx_relations_source`, `idx_relations_target`, `idx_relations_governance_temporal`).
- `NewRelationStore(db DB) (*RelationStore, error)` — creates the store and hydrates the registry.
- `ValidateRelationKindDef(def RelationKindDef) error` — validates `type_name` non-empty, `mode` ∈ {derived, asserted, inferable}, `type_pairs` valid JSON.
- `(*RelationStore).UpsertKind(ctx, def) error` — upsert by `type_name`; updates registry atomically on success.
- `(*RelationStore).GetKind(typeName string) (RelationKindDef, bool)` — registry read, no DB round-trip.
- `(*RelationStore).ListKinds() []RelationKindDef` — sorted by `type_name`.
- `(*RelationStore).Assert(ctx, edge RelationEdge) error` — inserts or updates an asserted edge; rejects unknown kinds and non-asserted `edge_class`.
- `(*RelationStore).GetBySource(ctx, sourceType, sourceID, kind string) ([]RelationEdge, error)` — `kind=""` returns all kinds.
- `(*RelationStore).GetByTarget(ctx, targetType, targetID, kind string) ([]RelationEdge, error)`.
- `(*RelationStore).Delete(ctx, id string) error` — hard delete by relation ID.
- `App.Relations(store *RelationStore) *App` — wires the store into the application.
- `App.RelationStore() *RelationStore` — returns the wired store (nil if not wired).

---

## [1.42.1] — 2026-06-20

### Breaking / Migration required

Call `MigrateNodeRevColumn(db, table)` for each existing content table before the first request. Without migration, the first `Save` fails with `"table X has no column named rev"`. Tables that need migration: your app's content tables (e.g. `posts`, `stories`) AND `smeldr_dynamic_content` if `ServeBlocks` is wired.

### Added
- `Node.Rev int` field (`db:"rev"`) — optimistic-concurrency revision counter. `0` on first insert; incremented by the storage layer on every subsequent save.
- `ErrRevConflict` sentinel error (HTTP 409, code `rev_conflict`) — returned by `SQLRepo.Save` when the caller's `Rev` does not match the stored revision.
- `MigrateNodeRevColumn(db DB, table string) error` — idempotent migration helper; probes via `PRAGMA table_info` and adds `rev INTEGER NOT NULL DEFAULT 0` if the column is absent.

### Changed
- `SQLRepo.Save` — CAS (compare-and-swap) guard: `WHERE table.rev = $N` appended to the `ON CONFLICT DO UPDATE` clause; `RowsAffected = 0` returns `ErrRevConflict`.
- `MemoryRepo.Save` — increments `Rev` via reflection on the pointer receiver on update (no CAS; increment-only).
- `blocks.go`, `stats_test.go`, `example/blog/main.go` — `rev INTEGER NOT NULL DEFAULT 0` added to all Node-embedding `CREATE TABLE` DDLs.

---

## [1.42.0] — 2026-06-19

### Added

- `PageMeta` struct with fields `Path`, `MetaTitle`, `Description`, `OGImage` — holds per-path SEO overrides. (T72/A157)
- `PageMetaStore` — DB-backed store for per-path SEO overrides. (T72/A157)
- `NewPageMetaStore(db DB) *PageMetaStore` — constructor. (T72/A157)
- `CreatePageMetaTable(db DB) error` — creates the `smeldr_page_meta` table (idempotent; `IF NOT EXISTS`). (T72/A157)
- `PageMetaStore.Set(ctx, path, title, description, ogImage string) error` — upserts overrides for a path via `INSERT OR REPLACE`. (T72/A157)
- `PageMetaStore.Get(ctx, path string) (PageMeta, error)` — returns stored overrides; returns zero `PageMeta` and nil error when no row exists. (T72/A157)
- `PageMetaStore.Delete(ctx, path string) error` — removes overrides for a path; no-op when path absent. (T72/A157)
- `PageMetaStore.List(ctx) ([]PageMeta, error)` — lists all stored overrides, ordered by path. (T72/A157)
- `App.PageMeta(store *PageMetaStore) *App` — wires the store into the app for use by template modules and `GetPageMeta`. Returns `*App` for chaining. (T72/A157)
- `App.GetPageMeta(ctx context.Context, path string) Head` — returns a `Head` populated from the store for the given path; returns zero `Head` when the store is nil or no override exists. (T72/A157)

### Changed

- `renderListHTML`: when no `ListHeadFunc` is configured and a `PageMetaStore` is wired, the list page head is automatically populated from the store for the request path. `ListHeadFunc` takes priority when set. (T72/A157)
- `App.Handler()` push loop now injects the `PageMetaStore` into all template modules via `setPageMetaStore`. (T72/A157)

---

## [1.41.1] — 2026-06-16

### Added

- `ContentTypeSchema.URLPrefix string` — operator-set public URL prefix for a
  content type. Empty string means admin-only (no public GET routes registered).
  Must start with `"/"` when non-empty. Set via `DefineContentType` schema or via
  the `POST /_content/types` JSON body (`url_prefix` field). (T104/A154)
- `MigrateURLPrefixColumn(db DB) error` — idempotent migration that adds the
  `url_prefix` column to `smeldr_content_type_schemas` when it is missing. Uses
  `PRAGMA table_info`; returns nil for non-SQLite databases. Called automatically
  by `ServeDynamicContent`. (T104/A154)

### Changed

- `DefineContentType`: public routes (`GET {prefix}`, `GET {prefix}/{slug}`) are
  now registered only when `schema.URLPrefix` is non-empty. Previously the prefix
  was derived automatically via `PluralSnake(TypeName)`. Types with no URLPrefix
  are admin-only. (T104/A154)
- Admin routes changed from `/_content/{prefix}` to `/_content/{type}` — the
  path variable now holds the `type_name`, not the URL prefix. Callers must update
  URLs accordingly. (T104/A154)
- `ContentList` block field `ContentType` now holds the `type_name` (e.g.
  `"recipe"`) instead of the URL prefix (e.g. `"recipes"`). Update existing block
  data when upgrading from v1.41.0. (T104/A154)
- `SetStatus` now triggers a background sitemap rebuild for types with a URLPrefix.
  The fragment is written to `sitemapStore` and served at
  `GET {prefix}/sitemap.xml`. (T104/A154)

### Fixed

- `GET /{seg1}/{seg2}` catch-all wildcard removed from `ServeDynamicContent`.
  The wildcard conflicted with literal 2-segment routes (e.g. `GET /static/`) in
  Go 1.22's `ServeMux`. Public item routes are now registered per type at
  `GET {URLPrefix}/{slug}`. (T104/A154)

## [1.41.2] — 2026-06-17

### Added

- `ssrfSafeDialContext()` — unexported func returning an `http.DialContext` that
  resolves hostnames via DNS before connecting and rejects connections to
  restricted IP ranges: loopback, RFC1918 (10/8, 172.16/12, 192.168/16),
  link-local (169.254/16, fe80::/10), unspecified, CGNAT (100.64.0.0/10),
  IPv6 unique-local (fc00::/7). Check performed at dial time to prevent DNS
  rebinding attacks. Wired into `outboundClient` via
  `&http.Transport{DialContext: ssrfSafeDialContext()}`. (A155)

### Changed

- `workerPool.Enqueue` now rejects `target_url` values whose scheme is not
  `https`. Returns `"smeldr: webhook: target_url must use https scheme"`. (A155)

### Fixed

- Comment on `outboundClient` previously claimed "SSRF validation performed at
  endpoint creation time" — no such validation existed. Replaced with accurate
  description of the client's redirect-blocking and SSRF-safe dialer. (A155)

---

## [1.41.0] — 2026-06-16

### Added

- `App.ServeDynamicContent() *App` — registers public and admin HTTP routes for
  runtime-defined content types. Panics if `Config.DB` is nil. Loads all
  `kind="content"` schemas from the database on first call (idempotent). Public
  routes: `GET /{slug}` (single item by slug, published only) and
  `GET /{seg1}/{seg2}` (reserved for future sub-type routing). Admin routes under
  `/_content/{prefix}`: `POST` (create draft), `GET` (list with pagination),
  `GET /{id}` (get by ID), `PATCH /{id}` (update fields), `POST /{id}/status`
  (set status). All admin routes require Editor role. (T104/A153)
- `App.DefineContentType(schema *ContentTypeSchema) error` — saves a content-type
  schema to `smeldr_content_type_schemas`, registers a `TypeDescriptor` (Kind:
  `"content"`) in the type registry, and claims the URL prefix
  `"/" + PluralSnake(schema.TypeName)`. Returns an error on nil DB, duplicate
  type name, or invalid schema. (T104/A153)
- `App.DynamicContentRepo(typeName string) (*DynamicTypeRepo, error)` — returns a
  `DynamicTypeRepo` for a registered runtime-defined content type. Rejects
  compiled (`Kind != "content"`) types with an error. (T104/A153)
- `DynamicTypeRepo` — per-type CRUD repository backed by `smeldr_dynamic_content`:
  `CreateDraft` (slug derived from title field, collision-safe), `GetBySlug`,
  `GetByID`, `List` (pagination, status filter, ordering), `UpdateFields` (PATCH
  semantics — merge, not replace; re-validates required fields), `SetStatus`
  (draft → published → archived; sets `published_at` on publish). (T104/A153)
- `PluralSnake(name string) string` — English-plural helper for snake_case type
  names. Consonant+y endings use the -ies rule; all others get plain -s.
  `"recipe"` → `"recipes"`, `"story"` → `"stories"`. (T104/A153)
- `ValidateSchemaDef(schema *ContentTypeSchema) error` — validates a
  `ContentTypeSchema` before writing to the database: requires non-empty
  `TypeName`, known field types (`string`/`integer`/`boolean`/`array`/`object`/
  `number`), and recognised `Role` values (`title`/`description`/`og_image`/
  `body`/`summary`). (T104/A153)

---

## [1.40.0] — 2026-06-15

### Added

- `ContentLister` interface — implemented by `Module[T]`; exposes `listPublished`
  as `TypeDescriptor.Fetch` for the ContentList block resolver (T96/A152).
- `TypeDescriptor.Fetch` field — `func(ctx, ListOptions) ([]map[string]any, error)`;
  wired at `App.Content()` time for any module implementing `ContentLister`.
- ContentList block resolver — `content_list` blocks now inject `.Items`
  (`[]map[string]any`) via the content-type registry at render time. Block fields
  `Limit`→`PerPage`, `Page`→`Page`, `SortField` ("published_at"/"created_at"/"title")
  →`OrderBy`, `SortDir` "desc"→`Desc`. `slog.Warn` on each skip path: empty
  `ContentType`, unknown type, nil `Fetch`, `FilterTags` present (not yet
  supported), fetch error. (T96/A152)

---

## [1.39.0] — 2026-06-15

### Added

- `kind TEXT NOT NULL DEFAULT 'block'` column on `smeldr_content_type_schemas`
  (added to `CreateSchemaTable` DDL; `MigrateSchemaKindColumn(db)` migrates
  existing databases — idempotent, safe on every boot) (T104/A151).
- `SchemaField.Role` — semantic seam: `"title"` / `"description"` / `"og_image"` /
  `"body"` / `"summary"`; at most one field per schema per role (T104/A151).
- `SchemaField.Relation` — forward-compat placeholder for future T06 edge-backed
  relations (T104/A151).
- `ValidateFields(schema, fields)` — extends `ValidateBlockFields` with type
  checking, URL format validation, and duplicate-role rejection; `ValidateBlockFields`
  retained as alias (T104/A151).
- `ContentTypeRegistry` + `TypeDescriptor` + `App.TypeRegistry()` — concurrency-safe
  name/prefix registry; dual key-space; auto-populated at `App.Content()` time
  (`registry.go`) (T104/A151).
- `idx_dynamic_content_type_status` index on `smeldr_dynamic_content` for efficient
  type+status queries (added to `CreateBlockTables`) (T104/A151).

---

## [1.38.0] — 2026-06-10

### Added

- `SchemaField`, `ContentTypeSchema` — field-descriptor and schema types for
  the block-type schema system (T32 Phase B, A146).
- `SchemaStore` with `FindByTypeName` and `All` — reads from
  `smeldr_content_type_schemas`.
- `CreateSchemaTable(db)` — creates `smeldr_content_type_schemas` (idempotent).
- `SeedBlockTypeSchemas(db)` — seeds all 16 canonical block type schemas using
  `INSERT OR IGNORE` (idempotent, preserves operator customisation).
- `ValidateBlockFields(schema, fields)` — rejects unknown fields and missing
  required fields; unschematised types are not validated (backwards compatible).

---

## [1.37.0] — 2026-06-10

### Added

- `ContentParentProvider` interface and `BlockHost()` module option (T94/A145):
  content-type instances (Post, Story, Essay, or any developer-defined type) can
  now host block sections and items. Pass `smeldr.BlockHost()` to `App.Content`
  to opt a module into the block-parent registry. The MCP tools `add_section` and
  `add_item` resolve the parent via the registry when the `parent_id` is not a
  `DynamicNode`. `ServeBlocks` / `BlockRenderer.Render` are unchanged — the edge
  table already accepts any parent ID. Body and sections coexist on the same
  instance (independent data paths).
- `App.RegisterBlockParent(p ContentParentProvider)` and `App.BlockParents()` for
  manual registration of external providers (e.g. from companion packages).

### Fixed

- `integration_full_test.go`: mojibake em-dashes in G-index legend and
  section-header comments replaced with plain hyphens (T98).

---

## [1.36.2] — 2026-06-09

### Fixed

- `migrateLegacyTableNames`: idempotency fix — a partial migration that leaves
  both source (`forge_*`) and destination (`smeldr_*`) tables in place no longer
  fails with "table already exists". A second `sqlite_master` check per pair skips
  the rename when the destination already exists (A139).

---

## [1.36.1] — 2026-06-08

### Fixed

- `processScheduled`: a `Save` failure for one scheduled item no longer halts
  processing of all remaining items in the same scheduler tick. The failing item
  is skipped with a `slog.Warn` log and the loop continues (Amendment A137).
- `scheduler.go`: errors returned from `processScheduled` are now captured and
  logged; previously they were silently discarded (Amendment A137).

---

## [1.36.0] — 2026-06-05

### Added

- `App.CaptureLogs(opts ...LogCaptureOption) *App` — opt-in, in-memory log capture.
  Installs a teeing `slog.Handler`: records still reach the existing handler
  (stderr) and records at/above the capture level are stored in a bounded ring
  (Amendment A128, T79).
- `LogEntry` — exported type for a captured record (`Time`, `Level`, `Msg`,
  `Attrs`, `Seq`); JSON wire shape for `/_logs` (Amendment A128, T79).
- `WithLogCapacity(n int)` (default 500) and `WithLogLevel(level slog.Level)`
  (default WARN) — `LogCaptureOption` configurers (Amendment A128, T79).
- `GET /_logs` — Admin role, plain HTTP + bearer (works when MCP is down). Mounted
  only when `CaptureLogs` was called (route absent → 404 otherwise). Envelope
  `{capacity, count, dropped, entries}` newest-first; query params `level`, `limit`,
  `since` (Amendment A128, T79).

### Notes

- Live-debugging facility only — in-memory, lost on restart; stderr stays the
  durable path. HTTP/CLI-only by design (no MCP tool): the feature must not depend
  on the subsystem it helps debug.
- When no custom handler is configured, `CaptureLogs` forwards to a text handler on
  stderr instead of wrapping slog's built-in handler, avoiding a fatal slog/log
  re-entrancy loop. Call `CaptureLogs` after any app-side `slog.SetDefault`.

---

## [1.35.0] — 2026-06-04

### Added

- `ContentTypeStats`, `SiteStats` — exported types for the `/_stats` endpoint
  and `App.Stats` (Amendment A126, T04).
- `StatsExtProvider` interface — external modules (e.g. smeldr.dev/media) implement
  this to contribute additional statistics without creating an import cycle. Register
  with `App.RegisterStatsProvider` (Amendment A126, T04).
- `App.Stats(ctx context.Context) (SiteStats, error)` — aggregates item counts per
  status across all registered content modules plus any external providers (Amendment
  A126, T04).
- `App.StatsHandler()` — mounts `GET /_stats` (Admin role required). Returns JSON with
  content counts and external stats; `generated_at` is RFC3339 UTC (Amendment A126, T04).
- `App.RegisterStatsProvider(p StatsExtProvider)` — registers an external stats
  contributor (Amendment A126, T04).

### Changed

- `go.mod` toolchain bumped `go 1.26.3` → `go 1.26.4` to close
  GO-2026-5039 (net/textproto) and GO-2026-5037 (crypto/x509) (Amendment A126).

---

## [1.34.0] — 2026-06-04

### Added

- `CreateRedirectsTable(db DB) error` — idempotent DDL helper; creates the
  `smeldr_redirects` table if it does not exist. Called automatically by
  `App.Redirects`; also exported for migration tools and tests (Amendment A125, T30).
- `App.Redirects(db DB) error` — activates database-backed redirect management:
  creates the table, loads saved entries into the in-memory store, and enables
  the MCP and CLI redirect tools. No manual DDL or `Load` call required
  (Amendment A125, T30).
- `App.RedirectDB() DB` — returns the DB passed to `App.Redirects`, or nil if not
  activated. Used internally by `smeldr.dev/mcp` to gate and service redirect tools
  (Amendment A125, T30).
- `RedirectStore.Delete(from string)` — removes an entry from the in-memory store.
  Pair with `RedirectStore.Remove(ctx, db, from)` for full DB + in-memory deletion
  (Amendment A125, T30).

---

## [1.33.0] — 2026-06-03

### Added

- `NewRateLimiter(n int, d time.Duration, opts ...Option) (Middleware, func())` — same
  as `RateLimit` but also returns a stop function that terminates the background sweep
  goroutine. The stop function is idempotent. Use it in tests via `t.Cleanup(stop)` to
  prevent goroutine leaks (Amendment A124, T53).
- `NewInMemoryCache(ttl time.Duration, opts ...Option) (func(http.Handler) http.Handler, func())` —
  same as `InMemoryCache` but returns a stop function for the background sweep goroutine
  (Amendment A124, T53).

---

## [1.32.0] — 2026-06-03

### Changed (additive, non-breaking)

- **Webhook headers dual-emit (Amendment A123, T86):** `httpDeliver` now sets both
  the new `X-Smeldr-Signature`, `X-Smeldr-Timestamp`, `X-Smeldr-Event`,
  `X-Smeldr-Delivery` headers (preferred) and the legacy `X-Forge-*` equivalents
  (same values) on every delivery. Existing receivers verifying `X-Forge-Signature`
  continue to work unchanged. The legacy `X-Forge-*` set will be removed in T87
  after a deprecation window.

---

## [1.31.0] — 2026-05-31

### Added
- Block-system data foundation (Amendment A116, T32 components 1+2). Data layer only — MCP tools, rendering, and schema seeding are later components.
  - `DynamicNode` — one generic content type for all block types; type-specific fields stored as JSON in `Fields json.RawMessage`, discriminated by `TypeName`. Embeds `Node` for the standard lifecycle.
  - `NewDynamicContentRepo(db DB) *SQLRepo[*DynamicNode]` — repository bound to the `smeldr_dynamic_content` table.
  - `ContentEdge`, `ContentEdgeStore`, `NewContentEdgeStore(db DB)` — composition edges (`smeldr_content_edges`); one table for page→block and collection→item. `AddChild`, `Children`, `ChildrenOf` (batched), `RemoveChild`, `Reorder`.
  - `CreateBlockTables(db DB) error` — single idempotent grouped creator for the block tables and the `(parent_id, sort_order)` index.
- Block rendering engine (Amendment A118, T32 component 4).
  - `App.ServeBlocks(dir string) (*BlockRenderer, error)` — parses convention templates under `dir` (one `<type_name>.html` per block type); returns a renderer bound to the App's DB.
  - `BlockRenderer.Render(ctx Context, pageType, pageID string) (template.HTML, error)` — loads and renders the full block tree for one page or collection; batched per-level load (no N+1); cycle protection via visited-set and `maxDepth 16`; graceful degradation — unpublished, missing, dangling, malformed blocks, missing template, or exec error all skip and log rather than 500.
- Reference-field resolution in ServeBlocks (Amendment A120, T32/T82). For built-in block types (`content_block`, `contact_card`, `hero`): `ImageID` fields are batch-loaded via a single `IN()` query (Published-only, no N+1) and injected as `.Image` sub-objects carrying `.MediaURL`, `.AltText`, and `.Caption`. `{{ with .Image }}` guards are honoured. Raw `ImageID` value is preserved alongside `.Image`.

---

## [1.30.0] — 2026-05-29

### Added
- `HeadAssets.RawHead template.HTML` — verbatim HTML injected into `<head>` after all other `HeadAssets` output (preconnect, stylesheets, links, scripts). Zero value is a no-op; fully backward compatible (Amendment A111, T74).

### Changed (breaking)
- Validation and auto-slug struct tag key renamed from `forge:"required"` to `smeldr:"required"` (Amendment A111, T67). Any content type using `forge:"required"` must update the tag; fields without the updated tag will no longer trigger validation or auto-slug derivation.

---

## [1.29.0] — 2026-05-28

### Added
- `SiteConfig` struct — singleton content type for site-wide defaults configurable via MCP (Amendment A110).
- `NewSiteConfigModule(db DB) *Module[SiteConfig]` — factory; returns a `SingleInstance` module pre-wired with `NewSQLRepo` on `smeldr_site_configs`.
- `CreateSiteConfigTable(db DB) error` — DDL helper; creates `smeldr_site_configs` if it does not exist.

---

## [1.28.0] — 2026-05-28

DB table rename: `forge_*` → `smeldr_*`, auto-migration at startup (Amendment A109).

### Changed (breaking for upgrades)

- **DB tables renamed** — all 7 internal tables renamed from `forge_` to `smeldr_` prefix.
  Existing SQLite databases are migrated automatically at first startup with v1.28.0
  via `migrateLegacyTableNames` called from `New()`. PostgreSQL operators must run the
  7 `ALTER TABLE` renames manually before deploying v1.28.0.
  `forge_audit_log → smeldr_audit_log`,
  `forge_delivery_logs → smeldr_delivery_logs`,
  `forge_nav → smeldr_nav`,
  `forge_outbound_jobs → smeldr_outbound_jobs`,
  `forge_redirects → smeldr_redirects`,
  `forge_tokens → smeldr_tokens`,
  `forge_webhook_endpoints → smeldr_webhook_endpoints`.

### Added

- `migrateLegacyTableNames` — internal function called from `New()` when `Config.DB` is
  non-nil and the database is SQLite. Wraps all renames in a transaction; logs each rename
  via `slog.Info`. Idempotent.

---

## [1.27.0] — 2026-05-28

Post-T62 cleanup: `smeldr.config`, `SMELDR_CONFIG`, `smeldr:` error prefix, skill file rename (Amendment A108).

### Changed (breaking)

- **`forge.config` renamed to `smeldr.config`** — operators must rename their runtime config file on
  disk from `forge.config` to `smeldr.config`.
- **`FORGE_CONFIG` env var renamed to `SMELDR_CONFIG`** — operators using the environment variable
  path override must update from `FORGE_CONFIG` to `SMELDR_CONFIG`.

### Changed (internal)

- Error string prefix `"forge: "` renamed to `"smeldr: "` throughout all Go source files (~48
  occurrences in 14 files: `audit.go`, `auth.go`, `config.go`, `errors.go`, `forge.go`,
  `middleware.go`, `module.go`, `nav.go`, `node.go`, `outbound.go`, `redirects.go`, `static.go`,
  `templates.go`, `webhook.go`). DB table names (`forge_tokens`, `forge_nav`, `forge_audit_log`)
  are unchanged.
- Skill file renamed `skills/forge.md` → `skills/smeldr.md` in core repo and
  `agent/skills/forge.md` → `agent/skills/smeldr.md` in common repo.

---

## [1.26.0] — 2026-05-28

Package rename: `package forge` → `package smeldr` (Amendment A107).

### Changed (breaking)

- **Package name:** `package forge` renamed to `package smeldr` in all 75 root-package Go files.
  Callers must update the usage prefix: `forge.App` → `smeldr.App` etc.
- **Template functions** (9 renamed): `forge:head` → `smeldr:head`, `forge_markdown` → `smeldr_markdown`,
  `forge_date` → `smeldr_date`, `forge_meta` → `smeldr_meta`, `forge_html` → `smeldr_html`,
  `forge_excerpt` → `smeldr_excerpt`, `forge_csrf_token` → `smeldr_csrf_token`,
  `forge_rfc3339` → `smeldr_rfc3339`, `forge_llms_entries` → `smeldr_llms_entries`.
  Templates using the old names must be updated.
- **Struct tag keys**: `forge_format:"..."` → `smeldr_format:"..."`,
  `forge_description:"..."` → `smeldr_description:"..."`.
- **Cookie names**: `forge_csrf` → `smeldr_csrf`, `forge_consent` → `smeldr_consent`.
  Existing sessions are invalidated on upgrade.

---

## [1.25.0] — 2026-05-24

`VerifyTokenString` — verify a Forge bearer token without an HTTP request (Amendment A103).

### Added

- `forge.VerifyTokenString(token string, secret []byte, store *TokenStore) (User, bool)` —
  verifies a raw bearer token string directly, without requiring an `*http.Request`.
  Identical verification logic to `VerifyBearerToken` (HMAC decode + optional
  `forge_tokens` fingerprint check). When `store` is non-nil, the DB lookup uses
  `context.Background()`.

  Intended for downstream libraries (forge-oauth, forge-agent, forge-admin) that hold
  a token string and need to validate it without constructing a synthetic HTTP request.

---

## [1.24.0] — 2026-05-22

`APIOnly()` module option — REST/MCP/CLI-only with no public HTML surface (Amendment A102).

### Added

- `forge.APIOnly() Option` — marks a module as having no public HTML surface.
  `GET /{prefix}` and `GET /{prefix}/{slug}` with `Accept: text/html` return 404.
  JSON routes, MCP tools, and forge-cli are unchanged.
- `NewModule` panics when `APIOnly()` and `SingleInstance()` are combined (logically
  incompatible: `SingleInstance` serves HTML; `APIOnly` forbids HTML).
- `ExampleAPIOnly` compile-verified example in `example_test.go`.
- Integration test group G36 (`TestFull_G36_APIOnly`): 5 sub-tests covering HTML block,
  JSON pass-through, and preview token bypass via JSON.

---

## [1.23.0] — 2026-05-23

`SingleInstance()` and `Standalone()` module routing options (Amendment A100).

### Added

- `forge.SingleInstance() Option` — marks a module as having at most one canonical
  item. `GET /{prefix}` serves the first Published item directly (no slug in URL).
  `GET /{prefix}/{slug}` is not registered (404). Useful for About, Contact, and
  other singleton pages.
- `forge.Standalone() Option` — removes the per-item slug URL from the module prefix
  and instead dispatches `GET /{slug}` at the top level via `App`. Useful when item
  slugs should appear as first-class top-level URLs (e.g. `/my-post` rather than
  `/posts/my-post`). The list endpoint `GET /{prefix}` remains.
- `MCPMeta.SingleInstance bool` field — `true` when the module uses `SingleInstance()`.
  Used by `forge-mcp` to suppress the `list_{type}s` admin tool for single-instance
  modules.
- `standaloneDispatcher` internal interface — implemented by `Module[T]`; used by
  `App` to route top-level slug requests to the correct Standalone module.
- `App.standaloneModules` / `App.standaloneReg` — internal fields supporting
  Standalone dispatch registration in `App.Handler()`.
- Integration test groups G34 (SingleInstance) and G35 (Standalone, two modules).

### Changed

- `forge-mcp`: `mcpAdminReadToolDefs` suppresses `list_{type}s` when
  `MCPMeta.SingleInstance` is true.

---

## [1.22.2] — 2026-05-19

Go 1.26.3 toolchain upgrade (A99 policy — govulncheck fix).

### Changed

- `go.mod`: `go` directive bumped from `go 1.26.2` to `go 1.26.3`.
  Resolves four stdlib CVEs reported by govulncheck: GO-2026-4982
  (XSS in `html/template`), GO-2026-4980 (XSS in `html/template`),
  GO-2026-4971 (panic in `net` on Windows), GO-2026-4918 (infinite
  loop in HTTP/2 transport). All fixed in Go 1.26.3.

---

## [1.22.1] — 2026-05-19

Fix data race in `notifyAfter` (Amendment A98).

### Fixed

- `module.go`: `notifyAfter` now takes a shallow snapshot of `item` before
  spawning any goroutine. Previously the original pointer was passed to both
  `dispatchAfter` and the `afterHook` goroutine, allowing a concurrent
  lifecycle transition (`setNodeStatus`/`setNodeTime`) to race with the
  signal handlers reading the same `Node` fields via reflection. The new
  `snapshotItem` helper (unexported) uses `reflect.New` + `Elem().Set` to copy
  the pointed-to struct; both goroutines receive the snapshot and no longer
  share mutable state with the caller. Races G26, G30, G32, and G33 are resolved.

---

## [1.22.0] — 2026-05-16

Built-in opt-in audit trail (Amendment A97).

### Added

- `audit.go` (new): `AuditRecord`, `AuditFilter`, `AuditStore` interface,
  `NewAuditStore(DB)` (SQL-backed implementation), `CreateAuditTable(DB)`.
- `App.Audit(AuditStore) *App` — wire an audit store to subscribe to
  `AfterPublish`, `AfterSchedule`, `AfterArchive`, and `AfterDelete` signals.
  `App.Handler()` lazily mounts `GET /_audit` (Editor role required) when
  an audit store is configured.

---

## [1.21.0] — 2026-05-14

`og_image` operator override (forge.config).

### Changed

- `mergeFileConfig`: `og_image` in `forge.config` now overrides `OGDefaults.Image.URL`
  set in Go code, so operators can update the site OG image with a config change
  and container restart — no rebuild required. All other `OGDefaults` fields
  (`TwitterSite`, `TwitterCreator`, width, height) retain Go-code values.
  When `og_image` is absent from the file, Go-code `Image.URL` is the fallback
  (existing behaviour preserved).

---

## [1.20.0] — 2026-05-11

Signal bus (Milestone 14, Amendment A94).

### Added
- `SignalEvent` struct — structured event type delivered to signal bus subscribers.
  Fields: `Type`, `Slug`, `Title`, `URL`, `Timestamp`, `PreviousState`, `ActorRole`, `ActorID`.
- `App.OnSignal(sig Signal, h func(context.Context, SignalEvent) error) *App` —
  registers a handler that fires after every matching lifecycle event. Multiple
  handlers per signal are supported; registration order is preserved.
- `OutboundDelivery` interface — minimal `{ Enqueue(ctx, OutboundJob) error }` used
  by any engine that needs retry-backed outbound HTTP without coupling to `WebhookStore`.

### Changed
- `App.Webhooks` now registers webhook delivery as `OnSignal` handlers, making it one
  subscriber among many. Behaviour is unchanged; the change is internal only.
- `injectWebhookHooks` replaced by `wireSignalBus` (unexported). The signal bus is
  wired at `App.Run()` time as before.

---

## [1.19.0] — 2026-05-09

Media upload token (Milestone 13, Amendment A93).

### Added

- `Config.MediaUploadTokenExpiry time.Duration` — upload token lifetime.
  Defaults to 15 minutes when zero.
- `App.GenerateUploadToken() string` — returns a short-lived HMAC-SHA256 signed
  token. Pass it in the `Authorization: UploadToken <token>` header to POST /media
  without a full bearer token. No slug or prefix binding — authorises any upload
  within the TTL.
- `App.ValidateUploadToken(token string) error` — validates an upload token inline.
  Returns `ErrUnauth` on any failure. Intended for use by forge-media's upload handler.
- `encodeUploadToken` / `decodeUploadToken` (unexported) — HMAC-SHA256 token
  encoding and constant-time validation in `auth.go`.

---

## [1.18.0] — 2026-05-08

Draft preview via HMAC-signed URL token (Milestone 12, Amendment A92).

### Added

- `Config.PreviewTokenExpiry time.Duration` — configures preview token lifetime.
  Defaults to 12 hours when zero.
- `App.GeneratePreviewToken(prefix, slug string) string` — returns a signed
  preview token binding the module prefix and content slug. Pass it to the
  module's show endpoint via `?preview=<token>` to serve Draft or Scheduled
  content to unauthenticated viewers.
- `App.BaseURL() string` — returns `Config.BaseURL` without a trailing slash.
  Intended for companion packages (forge-mcp, forge-cli) that build absolute URLs.
- `encodePreviewToken` / `decodePreviewToken` (unexported) — HMAC-SHA256 token
  encoding and constant-time validation in `auth.go`. Token payload encodes
  prefix, slug, and expiry so a token for `/posts/foo` cannot be replayed on
  `/docs/foo` or any other slug.

### Changed

- `Module[T].showHandler`: valid `?preview=<token>` now bypasses the
  Published-only visibility guard for **Draft and Scheduled** items only.
  Archived items are never previewable — a valid token for an Archived item
  falls through silently to 404.
- `Module[T]` gains an unexported `secret []byte` field injected by
  `App.Content` via the new `setSecret([]byte)` method.

---

## [1.17.0] — 2026-05-08

Outbound webhooks, MCP resource subscriptions, and `AfterSchedule` signal (Milestone 11).

### Added

- `WebhookEndpoint` — struct representing a registered outbound webhook (ID,
  events, target URL, active flag, created timestamp).
- `WebhookStore` — SQLite-backed store for managing webhook endpoints. Methods:
  `Create`, `List`, `Delete`, `EndpointsForEvent`, `DecryptSecret`.
  URL validation (HTTPS-only, no private/loopback IPs) is enforced on `Create`.
- `validateWebhookURL` (unexported) — SSRF-safe URL validator shared by store
  and MCP tools.
- `OutboundJob` — struct representing a delivery task (ID, endpoint ID, target
  URL, encrypted secret, payload, event, attempt count, retry schedule, status).
- `DeliveryLog` — struct representing a single delivery attempt record.
- `WebhookJobQueue` interface — `Enqueue`, `ListJobsForEndpoint`,
  `ListDeliveryLogs` methods consumed by the rest of the app.
- `WorkerPool` (unexported `workerPool`) — background goroutine pool that
  delivers webhook payloads with HMAC-SHA256 signing, exponential backoff
  (4^attempt ±20% jitter, max 1 hour), per-endpoint circuit breakers
  (5 consecutive failures → open for 5 minutes), and dead-letter after
  7 attempts.
- `App.Webhooks(store *WebhookStore)` — wires outbound webhooks into the app.
  Registers the store and creates a `workerPool` backed by `Config.DB`.
- `App.WebhookPool() WebhookJobQueue` — returns the active pool for testing
  and admin introspection.
- `App.injectWebhookHooks` (unexported) — registers an `afterHook` on every
  module that dispatches `AfterPublish`, `AfterUpdate`, `AfterDelete`, and
  `AfterSchedule` signals as webhook delivery jobs.
- `AfterSchedule Signal` — new signal constant in `signals.go` emitted after a
  node is successfully scheduled via `MCPSchedule` (Amendment A87).
- `Module.afterHook`, `Module.setAfterHook`, `Module.notifyAfter` (unexported)
  — post-lifecycle callback slot for webhook wiring (Amendment A89).
- `forge-mcp`: `create_webhook`, `list_webhooks`, `delete_webhook`,
  `list_webhook_deliveries`, `retry_webhook` MCP tools (Admin role required).
- `forge-mcp`: MCP resource subscriptions — `resources/subscribe` and
  `resources/unsubscribe` JSON-RPC methods. The SSE transport now assigns a
  session ID and notifies subscribed clients via `notifications/resources/updated`
  events when content changes.
- `forge-cli`: `forge webhook` command with subcommands: `create`, `list`,
  `delete`, `deliveries`, `retry`.

### Changed

- `Module.MCPSchedule` (Amendment A87) — now dispatches `AfterSchedule` signal
  after a successful schedule operation.
- `forge-mcp` `handleInitialize` — capabilities response now includes
  `"resources": {"subscribe": true, "listChanged": true}`.
- `forge-mcp` SSE transport — now emits `event: endpoint` with a session ID
  before entering the notification loop.

---

## [1.16.0] — 2026-05-04

Static file serving, automatic bootstrap token, and repo cleanup (A82, A83).

### Added

- `Config.Dev bool` — dev mode flag. When true, `App.Static` serves files
  from disk. When false (production), serves from an embedded `fs.FS` with
  immutable `Cache-Control` headers.
- `App.Static(prefix, prod, devDir)` — mounts a static file tree. In dev mode
  reads from `devDir`; in production reads from `prod` (embedded FS).
- `forge.config` key `dev` (bool) — sets `Config.Dev` from config file.
- `TokenStore.ensureBootstrap` (unexported) — auto-creates a `bootstrap-admin`
  token (admin role, 10-year TTL) when `forge_tokens` is empty at first startup.
  The raw token is emitted via `slog.Warn` (shown once, never persisted).
- `App.Handler` now calls `ensureBootstrap` after a successful `probeTable`.

### Changed

- Stale `forge-cli/`, `forge-mcp/`, and `forge-media/` subdirs removed from
  this repo. These modules now live in standalone repos. `forge-pgx/` remains
  as a subdir module.

### Fixed

- `App.Static` used `"/static/"` as ServeMux pattern, conflicting with
  Go 1.22+ method-qualified routes. Fixed to `"GET " + prefix`.

Amendments: A82, A83.

---

## [1.15.0] — 2026-05-04

Go 1.26.2 modernisation sprint, SeqRepository streaming, SQLite test parity (A78, A80, A81).

### Added

- `SeqRepository[T]` optional interface with `Seq`, `SeqByStatus`, and
  `SeqAll` methods — returns lazy `iter.Seq2[T, error]` for streaming without
  full result-set load (Amendment A80).
- `MemoryRepo[T]` and `SQLRepo[T]` both implement `SeqRepository[T]`.
- `storage_sqlite_test.go` — `TestRepoParity_SQLRepo` runs all 11 parity
  sub-tests against a real in-memory SQLite DB using `modernc.org/sqlite`
  (test-only, CGO-free, Amendment A81).
- `forge-pgx`: repository parity test suite against real PostgreSQL.

### Changed

- `validateStruct` unexported (was `ValidateStruct`); not part of the public
  API (Amendment A78).
- `sort` package replaced with `slices` across all core files.
- `modernc.org/sqlite` added as a test-only direct dependency (Amendment A81).
- `go.work` added to `.gitignore` (was accidentally tracked).

### Documentation

- `ErrRequestTooLarge` godoc clarified.
- `App.Content` fallback comment added.
- `REFERENCE.md` — Rate Limiting section added.
- Decisions A78 and A79 documented in `DECISIONS.md` and `decisions/core.md`.

---

## [1.14.1] — 2026-05-02

`ListHeadFunc` option — populate list page `<title>` and meta tags (Amendment A77).

### Added

- `forge.ListHeadFunc[T any](fn func(forge.Context, []T) forge.Head) forge.Option` —
  new module option that sets the `<title>` and meta tags for a module's list page.
  The function receives the current request context and the slice of published items.
- `listHeadFuncOption[T]` unexported generic type (same pattern as `HeadFunc`).
- `listHeadFunc any` field on `Module[T]`.

### Fixed

- Module list pages (e.g. `/posts`) always rendered with an empty `<title>`.
  `renderListHTML` now resolves the list head via `listHeadFunc` when set,
  with `mergeOGDefaults` applied for consistency with show-page behaviour.

---

## [1.14.0] — 2026-04-30

Go 1.26.2 and module path migration to `forge-cms.dev` (Amendment A76).

### Changed

- All `go.mod` files: `go` directive bumped from `1.22` to `1.26.2` across
  `forge`, `forge-mcp`, `forge-media`, `forge-cli`, and `forge-pgx`.
- Module paths renamed across all modules and all import sites:
  - `github.com/forge-cms/forge` → `smeldr.dev/core`
  - `github.com/forge-cms/forge-mcp` → `smeldr.dev/core-mcp`
  - `github.com/forge-cms/forge-media` → `smeldr.dev/core-media`
  - `github.com/forge-cms/forge-cli` → `smeldr.dev/cli`
  - `github.com/forge-cms/forge-pgx` → `smeldr.dev/core-pgx`
- `forge.go`: `forgeVersions()` prefix logic updated — uses `forge-cms.dev/`
  as the base prefix; sub-modules are no longer sub-paths of the root module.
- All internal imports, documentation, and README examples updated.

Closes #1, Closes #2.

---

## [1.13.1] — 2026-04-22

HTML passthrough in `renderMarkdown` — lines starting with `<` are emitted verbatim.

### Changed

- `markdown.go`: `renderMarkdown` now emits any line whose trimmed form starts with
  `<` verbatim, without HTML-escaping. Forge is self-hosted; content authors are
  trusted (same role system that governs MCP write operations). This unblocks HTML
  blocks such as `<div class="pull-quote">` and `</div>` in body content. Inline
  markdown in non-HTML lines (bold, code, links, tables) is still escaped.

---

## [1.13.0] — 2026-04-18

HeadLink — rename FaviconLink → HeadLink, HeadAssets.Favicons → HeadAssets.Links (Amendment A74).

### Changed

- `head.go`: `FaviconLink` renamed to `HeadLink`; godoc updated to describe any HTML
  `<link>` element, not only favicons. Breaking change — update all `FaviconLink` references.
- `head.go`: `HeadAssets.Favicons []FaviconLink` renamed to `HeadAssets.Links []HeadLink`.
  Breaking change — update all `.Favicons` field references.
- `templates.go`: template range updated from `.HeadAssets.Favicons` to `.HeadAssets.Links`.
  Generated HTML is identical.

---

## [1.12.0] — 2026-04-18

Media Library — optional `forge-media` submodule (Decision 31).

### Added

- `forge.go`: `Config.MediaPath string` — file system path for uploaded media.
  Defaults to `"./media"` when zero. Read by `forge-media`; ignored by forge core.
- `forge.go`: `Config.MediaMaxSize int64` — maximum upload size in bytes.
  Defaults to 5 242 880 (5 MB) when zero. Read by `forge-media`; ignored by forge core.
- `forge.go`: `App.Config() Config` — returns a copy of the application
  configuration. Allows companion packages (`forge-media`) to read `BaseURL`,
  `MediaPath`, and `MediaMaxSize` without the host application repeating those
  values at the call site (Amendment A73).

Submodules: forge-media v1.0.0 released, forge-mcp v1.5.0 released.

---

## [1.11.0] — 2026-04-11

forge.config — file-based configuration (Decision 30).

### Added

- `config.go`: `loadConfigFile(path string) (Config, error)` — parses a
  plain `key = value` file. Comments (`#`), blank lines, and unknown keys are
  silently ignored. Values may contain `=`; only the first is the separator.
- `config.go`: `mergeFileConfig(goCfg, fileCfg Config) Config` — merges file
  values into a Go Config; Go-code fields always take precedence.
- `forge.go`: `Config.AppSchema *AppSchema` — app-level JSON-LD structured
  data set via file (`org_name`, `org_type`) or directly in Go code. Explicit
  `app.SEO()` call takes precedence.
- `forge.go`: `Config.OGDefaults *OGDefaults` — app-level Open Graph and
  Twitter Card fallbacks set via file (`og_image`, `twitter_site`) or Go code.
  Root-relative `og_image` values are resolved against `BaseURL` at startup.
- `forge.go`: `MustConfig` now loads `forge.config` in the working directory
  (or the path in `FORGE_CONFIG` env var) before validating the config. Go-code
  fields win. `secret` as a file key panics immediately.
- Supported keys: `base_url`, `https`, `nav_mode`, `org_name`, `org_type`,
  `twitter_site`, `og_image`. Unknown keys are silently ignored.
- Error messages include line number, invalid value, and expected values —
  readable by both humans and AI agents.

---

## [1.10.0] — 2026-04-11

NavTree — first-class navigation abstraction (Decision 29).

### Added

- `nav.go`: `NavMode` type with `NavModeDB` (database-backed) and `NavModeCode`
  (code-supplied) constants. Zero value disables navigation.
- `nav.go`: `NavItem` struct — nine fields: `ID`, `Label`, `Path`, `ParentID`,
  `Module`, `Hidden`, `Ghost`, `SortOrder`, `Children`. Hidden/Ghost flag matrix
  governs visibility in navigation, breadcrumbs, and clickability.
- `nav.go`: `NavTree` struct — thread-safe in-memory tree with flat `map[string]*NavItem`
  and roots slice. Methods: `Tree()` (deep copy of roots with Children), `List()`
  (flat list), `Get(id)`, `HasDB()`, `Create`, `Update`, `Delete` (recursive
  descendant removal), `setCode`, `migrate`, `load`.
- `forge.go`: `Config.NavMode NavMode` field — selects DB or code navigation mode.
- `forge.go`: `App.navTree *NavTree`, `App.navCodeItems []NavItem`,
  `App.navTreeModules` — nav wiring fields.
- `forge.go`: `App.Nav(items ...NavItem)` — registers code-mode nav items.
- `forge.go`: `App.NavTree() *NavTree` — accessor for forge-mcp and custom handlers.
- `forge.go`: `Content()` — detects `setNavTree(*NavTree)` interface, appends
  module to `navTreeModules` for deferred wiring.
- `forge.go`: `Handler()` — initialises NavTree after TokenStore probe: migrates
  and loads (NavModeDB) or builds from code items (NavModeCode); then calls
  `setNavTree` on all registered modules.
- `templatedata.go`: `TemplateData[T].Nav []NavItem` field — populated in HTML
  renders when a nav tree is configured. Templates access it as `{{.Nav}}`.
- `templates.go`: `Module[T].setNavTree(*NavTree)` — setter called by
  `App.Handler()`.
- `templates.go`: `renderListHTML` and `renderShowHTML` — inject `data.Nav` from
  `m.navTree.Tree()` when `m.navTree != nil`.
- `module.go`: `Module[T].navTree *NavTree` field.

Submodules: forge-mcp v1.4.0 released.

---

## [1.9.1] — 2026-04-10

Inline link support in `mdInline` — `[text](url)` now renders as `<a href="url">text</a>`.

### Added

- `markdown.go`: `mdApplyLinks` — iterative `[text](url)` → `<a href="url">text</a>`
  replacement with a URL allow-list (`http://`, `https://`, `/` only). Any other
  scheme (e.g. `javascript:`, `data:`) is silently rejected and the original
  literal text is emitted unchanged. Called from `mdInline` after HTML escaping
  and before bold/code pattern application.

---

## [1.9.0] — 2026-04-07

Field format semantics: `forge_format` and `forge_description` struct tags (Decision 27).

### Added

- `MCPField.Format string` — populated from the `forge_format` struct tag; machine-readable
  format hint (`"markdown"`, `"html"`). Empty string when the tag is absent (Decision 27).
- `MCPField.Description string` — populated from the `forge_description` struct tag; free-text
  authoring guidance for AI agents. Empty string when the tag is absent (Decision 27).
- `forge-mcp`: tool input schemas now emit a `"description"` key in JSON Schema properties
  using the priority logic defined in Decision 27:
  both tags → `forge_description + " (" + forge_format + ")"`;
  format only → `"(" + forge_format + ")"`; neither → key omitted.

---

## [1.8.0] — 2026-04-06

Last-admin guard on token revocation (Decision 26).

### Added

- `forge.ErrLastAdmin` — sentinel error (HTTP 409 Conflict, code `"last_admin"`).
  Returned by `TokenStore.Revoke` when the token being revoked is the last active
  (non-revoked, non-expired) admin token (Decision 26).

### Changed

- `TokenStore.Revoke` now checks whether the target token is an admin token and,
  if so, whether at least one other active admin token exists. If the target would
  be the last active admin, `Revoke` returns `ErrLastAdmin` without modifying any
  row. All other revocations are unaffected (Decision 26).

---

## [1.7.0] — 2026-04-05

Trusted raw HTML passthrough for module templates (Amendment A67).

### Added

- `forge_html` template function — wraps a `string` as `template.HTML`, bypassing
  Go's automatic HTML escaping. Registered in `TemplateFuncMap` as `"forge_html"`.
  Use only for trusted content (e.g. pre-rendered video embeds, third-party
  iframes). User-supplied strings must never be passed without prior sanitisation
  (Amendment A67).

  Template usage:
  ```
  {{.Content.Embed | forge_html}}
  ```

---

## [1.6.0] — 2026-04-05

Named revocable bearer tokens backed by a `forge_tokens` table (Amendment A66).

### Added

- `forge.TokenRecord` struct — exported record type returned by `TokenStore.List`.
  Fields: `ID`, `Name`, `Role string`; `ExpiresAt`, `RevokedAt`, `CreatedAt time.Time`.
- `forge.TokenStore` struct + `forge.NewTokenStore(db DB, secret string) *TokenStore` —
  server-side token registry. Issues tokens via `Create`, enumerates them via
  `List`, and revokes them via `Revoke`. Backed by a `forge_tokens` table.
- `TokenStore.Create(ctx, name, role string, ttl time.Duration) (string, error)` —
  calls `SignToken`, stores a SHA-256 fingerprint in `forge_tokens`, and returns the
  plaintext token once. The plaintext is never persisted (Amendment A66).
- `TokenStore.List(ctx context.Context) ([]TokenRecord, error)` — returns all token
  records ordered newest first (Amendment A66).
- `TokenStore.Revoke(ctx context.Context, id string) error` — sets `revoked_at`;
  effective immediately on next request (Amendment A66).
- `forge.Config.TokenStore *TokenStore` optional field — wire a `TokenStore` into the
  App at startup. Nil by default (stateless HMAC mode unchanged) (Amendment A66).
- `App.TokenStore() *TokenStore` accessor — used by `forge-mcp` to inherit the store
  (Amendment A66).

### Changed

- `forge.VerifyBearerToken` signature extended from 2-arg to 3-arg:
  `VerifyBearerToken(r *http.Request, secret []byte, store *TokenStore) (User, bool)`.
  When `store` is `nil`, behaviour is identical to the previous version.
  When `store` is non-nil, the token fingerprint is looked up in `forge_tokens`;
  absent or revoked tokens are rejected (Amendment A66).

---

## [1.5.0] — 2026-04-04

Per-request extra data for module templates (Amendment A65).

### Added

- `forge.ContextFunc(fn func(ctx Context, item any) (any, error)) Option` —
  new module option. The function is called once per list and show render.
  Its return value is stored in `TemplateData.Extra` and is available in
  templates as `.Extra`. Errors from the function log and set `Extra` to nil
  — the render is never aborted (Amendment A65).
- `TemplateData[T].Extra any` — new field. Zero value is `nil` when no
  `ContextFunc` is configured. Templates access it as `{{.Extra}}`:
  ```
  {{- $nav := .Extra}}
  {{template "sidebar" $nav}}
  ```
  (Amendment A65).

---

## [1.4.0] — 2026-04-03

Embeddable head struct for custom handlers (Amendment A64).

### Added

- `forge.PageHead` — exported struct holding the four framework-owned head
  fields (`Head`, `OGDefaults`, `AppSchema`, `HeadAssets`). Embed `PageHead`
  in any custom handler data struct to enable `{{template "forge:head" .}}`
  without using `TemplateData[T]` (Amendment A64).

### Changed

- `forge.TemplateData[T]` — the four previously individual fields (`Head`,
  `OGDefaults`, `AppSchema`, `HeadAssets`) are now promoted from the embedded
  anonymous `PageHead` field. Existing templates access them identically
  (`.Head`, `.OGDefaults`, etc.) — zero breaking changes. Internally, struct
  literals must be updated to use `PageHead: forge.PageHead{...}` syntax
  (Amendment A64).

---

## [1.3.0] — 2026-04-03

Static linked assets in forge:head (Amendment A63).

### Added

- `forge.HeadAssets` — new `SEOOption` applied via `app.SEO()`. Injects
  preconnect hints, stylesheets, favicon `<link>` elements, and `<script>`
  tags into `forge:head` on every page. Assets are emitted in order:
  preconnect → stylesheets → favicons → scripts (Amendment A63).
- `forge.FaviconLink` — struct declaring a single `<link>` for a favicon or
  touch icon. `Rel` is required; `Type` and `Sizes` are omitted when empty
  (Amendment A63).
- `forge.ScriptTag` — struct declaring a single `<script>` element. `Src`
  loads an external script; `Body template.JS` inlines JavaScript when `Src`
  is empty. `Async` and `Defer` are only emitted for external scripts
  (Amendment A63).

---

## [1.2.0] — 2026-04-02

Shared template partials (Amendment A62).

### Added

- `App.Partials(dir string) *App` — registers a directory of partial templates
  (any `*.html` file) to be injected into every module template set. Files must
  use `{{define "name"}}...{{end}}` syntax. Loaded in alphabetical order.
- `App.MustParseTemplate(path string) *template.Template` — parses a single
  template file with `TemplateFuncMap`, `forge:head`, and all partials from the
  configured partials directory. Intended for custom `app.Handle()` route
  handlers (e.g. a home page). Panics on error, consistent with `MustConfig`.

---

## [1.1.9] — 2026-04-02

App-level Open Graph defaults and structured data (Amendment A61).

### Added

- `forge.OGDefaults` — new `SEOOption` applied via `app.SEO()`. Sets a
  fallback `og:image`, a `twitter:site` handle, and a fallback
  `twitter:creator`. Fallbacks are merged into each page's `Head` at render
  time; `twitter:site` is always emitted when set (Amendment A61).
- `forge.AppSchema` — new `SEOOption` applied via `app.SEO()`. Declares
  app-level JSON-LD structured data (e.g. `Organization`, `WebSite`) emitted
  automatically by `forge:head` on every page (Amendment A61).

### Changed

- `forge:head` partial now receives the full `TemplateData` value instead of
  just `Head`: update `{{template "forge:head" .Head}}` to
  `{{template "forge:head" .}}` in all templates. The partial's rendered output
  is identical for existing sites with no `OGDefaults` or `AppSchema`
  configured (Amendment A61).

---

## [1.1.8] — 2026-04-02

`forge.New()` now calls `MustConfig()` automatically, so configuration errors
(empty `BaseURL`, `Secret` too short) are always caught at process start, never
at first request (Amendment A60).

### Changed

- `forge.go`: `New()` calls `MustConfig(cfg)` as its first line; apps with
  invalid configuration that previously started silently will now panic at
  startup with a descriptive message. Godoc on `New()` updated to document the
  panic behaviour (Amendment A60).

---

## [1.1.7] — 2026-03-20

`/_health` is now exempt from the HTTPS redirect middleware so that reverse-proxy
health checks (e.g. Caddy `health_uri`) receive a `200` response on plain HTTP
(Amendment A59).

### Fixed

- `forge.go`: `httpsRedirect()` now short-circuits to `next.ServeHTTP` for
  `/_health` before checking TLS / `X-Forwarded-Proto`; previously a plain-HTTP
  health check from a co-located reverse proxy received a `301` redirect instead
  of `200`, causing the proxy to report the upstream as unhealthy (Amendment A59)

---

## [1.1.6] — 2026-03-20

`/_health` now reports framework versions sourced from the binary's embedded
build info; the application-supplied `"version"` key is removed (Amendment A58).
`App.Run()` emits a startup log line with the same version data before
`ListenAndServe`.

### Changed

- `forge.go`: `App.Health()` response no longer includes the `"version"` key
  driven by `Config.Version`; instead, `forgeVersions()` reads
  `runtime/debug.ReadBuildInfo()` at mount time and injects `"forge"` (and
  any companion-module keys such as `"forge_mcp"`) into the JSON — e.g.
  `{"status":"ok","forge":"1.1.6","forge_mcp":"1.0.5"}` (Amendment A58)
- `forge.go`: `App.Run()` calls `forgeVersions()` before starting
  `ListenAndServe` and emits a startup line to stderr, e.g.
  `forge: forge 1.1.6, forge_mcp 1.0.5` (Amendment A58)
- `forge.go`: `Config.Version` godoc updated — the field is retained for
  application authors but is no longer consumed by any built-in Forge endpoint
  (Amendment A58)

---

## [1.1.5] — 2026-03-20

`SQLRepo` now double-quotes all generated SQL identifiers, fixing runtime SQL
syntax errors when `db` tag values collide with reserved keywords such as
`order`, `group`, or `index` (Amendment A57).

### Fixed

- `storage.go`: `quoteIdent()` helper added; applied to every generated column
  reference in `SQLRepo.Save`, `FindAll`, `FindByID`, `FindBySlug`, and
  `Delete`; previously unquoted identifiers caused SQL syntax errors when a
  `db` struct tag used a reserved keyword (e.g. `db:"order"`) (Amendment A57)

---

## [1.1.4] — 2026-03-20

Add `forge.AbsURL(base, path string) string` helper for building absolute URLs
in `Head()` implementations (Amendment A56).

### Added

- `head.go`: `AbsURL(base, path string) string` — trims any trailing slash from
  `base`, passes `path` through `URL()` for normalisation, and concatenates;
  intended for use in `Head()` implementations when setting `Head.Canonical`,
  `Head.Image.URL`, or any other field that requires an absolute URL
  (Amendment A56)

---

## [1.1.3] — 2026-03-18

`negotiate()` now returns `text/html` when `Accept` is absent or `*/*` and
the module has templates configured, ensuring crawlers see HTML with structured
data in `<head>` (Amendment A53).

### Fixed

- `module.go`: `negotiate()` now returns `text/html` when `Accept` is
  absent or `*/*` and the module has templates configured; previously
  returned `application/json` unconditionally for these cases, causing
  Google Search Console and other crawlers to receive JSON instead of
  HTML and never see structured data in `<head>` (Amendment A53)

---

## [1.1.2] — 2026-03-17

`[]string` fields in content types are now correctly typed as `"array"` in
`MCPSchema` and MCP tool schemas; comma-separated string values from MCP clients
are automatically coerced to slices (Amendment A52).

### Fixed

- `module.go`: `mcpGoTypeStr` now returns `"array"` for `reflect.Slice` kinds;
  previously fell through to `"string"`, causing MCP clients to advertise and send a
  plain string for `[]string` fields which `json.Unmarshal` silently discarded
  (Amendment A52-1)
- `module.go`: new `coerceSliceFields` helper splits comma-separated string values
  for `[]string` struct fields before the `Marshal→Unmarshal` round-trip in
  `MCPCreate` and `MCPUpdate`, tolerating MCP clients that serialise multi-value
  fields as comma strings (Amendment A52-3)
- `forge-mcp/mcp.go`: `inputSchema` and `inputSchemaUpdate` now emit
  `{"type":"array","items":{"type":"string"}}` for array fields instead of
  `{"type":"array"}`, and suppress `minLength`/`maxLength`/`enum` constraints that
  apply to string entries but not arrays (Amendment A52-2)

---

## [1.1.1] — 2026-03-17

`forge:head` now emits the correct `twitter:card` value for article and product
content types (Amendment A51).

### Fixed

- `templates.go`: `forgeHeadTmpl` now emits `twitter:card = summary_large_image`
  when `Head.Type` is `"Article"` or `"Product"`, even when no image is provided;
  previously only a non-empty `Head.Image.URL` triggered the large-image card,
  causing OG/Twitter scrapers to render a small summary card for article-type
  content; `Head.Social.Twitter.Card` explicit override continues to take
  priority over the derived value (Amendment A51)

---

## [1.1.0] — 2026-03-17

`forge-mcp` — MCP support shipped (Milestone 10). New exported symbols in
forge core enabling AI assistants to discover and operate on content modules
via the Model Context Protocol.

### Added

- `mcp.go`: `MCPOperation` type; `MCPRead`, `MCPWrite` constants; `MCP(...)`
  option function; `MCPMeta` struct (`Prefix`, `TypeName`, `Operations`);
  `MCPField` struct (`Name`, `JSONName`, `Type`, `Required`, `MinLength`,
  `MaxLength`, `Enum`); `MCPModule` interface (`MCPMeta()`, `MCPSchema()`,
  `MCPList()`, `MCPGet()`, `MCPCreate()`, `MCPUpdate()`, `MCPPublish()`,
  `MCPSchedule()`, `MCPArchive()`, `MCPDelete()`)
- `module.go`: `Module[T]` implements `MCPModule` — all nine operations
  delegating to the existing repo, validation, signal, and lifecycle layers
- `forge.go`: `App.MCPModules() []MCPModule` — returns modules registered
  with `MCP(...)`
- `auth.go`: `VerifyBearerToken(r *http.Request, secret []byte) (User, error)`
  — validates HMAC Bearer tokens for SSE transport (Amendment A50)
- `context.go`: `NewContextWithUser(user User) Context` — production-safe
  background context for use by transport layers (Amendment A50)
- `forge.go`: `App.Secret() []byte` — exposes the app secret for transport
  layer token verification (Amendment A50)

---

## [1.0.11] — 2026-03-15

Manually published items now get a correct `PublishedAt` timestamp.

### Fixed

- `module.go`: `updateHandler` now sets `PublishedAt` to the current UTC time
  and re-saves when the status transitions to `Published`; previously
  `PublishedAt` remained at zero for all items published via PUT; the scheduler
  path was already correct (Amendment A48)

---

## [1.0.10] — 2026-03-15

`forge_markdown` now delegates to `renderMarkdown`, gaining full table support.

### Fixed

- `templatehelpers.go`: `forgeMarkdown` replaced with a one-line delegation to
  `renderMarkdown`; the `forge_markdown` template function now renders GFM
  tables, language-tagged fenced code blocks, `<hr>`, and all other elements
  supported by `renderMarkdown`; the previous stub had no table parsing
  (Amendment A47)

---

## [1.0.9] — 2026-03-15

Minimal Markdown→HTML renderer added to `TemplateFuncMap` with zero
dependencies.

### Added

- `markdown.go`: `renderMarkdown(s string) template.HTML` — XSS-safe
  Markdown→HTML converter supporting h1–h6, fenced code blocks with
  `class="language-〈lang〉"`, unordered lists, GFM tables, `**bold**`,
  `` `inline code` ``, blank-line `<p>` paragraphs, and `---` as `<hr>`;
  all content HTML-entity-escaped before tag wrapping; zero third-party
  dependencies (Amendment A46)
- `templatehelpers.go`: `TemplateFuncMap()` gains `"markdown"` key backed by
  `renderMarkdown`; existing `"forge_markdown"` is unchanged (Amendment A46)

---

## [1.0.8] — 2026-03-15

  Default authentication wired automatically in `New()`. Silent misconfiguration
  where a developer sets `Config.Secret` and uses `SignToken` but forgets to call
  `app.Use(forge.Authenticate(...))` now produces a working app instead of 403 on
  every write request.

  ### Added

  - `forge.go`: `Config.Auth AuthFunc` field — the `AuthFunc` used to authenticate
    all requests; when nil, Forge defaults to `BearerHMAC(Config.Secret)`
    automatically (Amendment A45)
  - `forge.go`: `New()` now prepends `Authenticate(auth)` as the first middleware in
    the app stack; replaces the need to call `app.Use(forge.Authenticate(...))`
    manually for the default bearer-token use case (Amendment A45)

  ### Changed

  - `Config.Secret` godoc updated to note that it drives the default `BearerHMAC`
    auth when `Config.Auth` is nil (Amendment A45)

---

## [1.0.7] — 2026-03-15

Bug fix: SQLRepo now correctly handles content types that embed `forge.Node`
or any other anonymous (embedded) struct.

### Fixed

- `storage.go`: `dbFields` / `collectDBFields` — `dbField.index` changed from
  `int` to `[]int` (reflect field index path); new recursive helper
  `collectDBFields` flattens promoted fields from embedded structs so that
  `SQLRepo.Save` no longer passes a raw struct value as a SQL argument
  (`"unsupported type forge.Node, a struct"`). All callers updated to use
  `reflect.Value.FieldByIndex` (Amendment A44)

---

## [1.0.6] — 2026-03-12

Health endpoint and application version field.

### Added

- `forge.go`: `Config.Version string` field — when non-empty, included in the
  `GET /_health` response as `{"status":"ok","version":"X.Y.Z"}`
- `forge.go`: `App.Health()` method — mounts `GET /_health`; explicit opt-in,
  not auto-mounted; returns `200 application/json`; no authentication required
  (Amendment A42)

---

## [1.0.5] — 2026-03-12

Hardening sweep: WriteError pipeline, SignToken error type, goroutine lifecycle,
debounce context correctness, and API naming consistency. All `http.Error`/`http.NotFound`
bypasses replaced, cache sweep goroutine terminates on graceful shutdown, debounce
callback no longer uses a cancelled request context, and two API symbols renamed for
convention consistency. No breaking changes except `FeedDisabled()` →
`DisableFeed()` (Amendment A40).

### Fixed

- `redirects.go`: `http.NotFound` and `http.Error(410)` bypasses replaced with
  `WriteError(w, r, ErrNotFound)` / `WriteError(w, r, ErrGone)` (Amendment A37)
- `redirectmanifest.go`: `http.Error(401)` bypass replaced with
  `WriteError(w, r, ErrUnauth)` (Amendment A37)
- `cookiemanifest.go`: `http.Error(401)` bypass replaced with
  `WriteError(w, r, ErrUnauth)` (Amendment A37)
- `sitemap.go`: `http.NotFound` and `http.Error(500)` bypasses replaced with
  `WriteError(w, r, ErrNotFound)` / `WriteError(w, r, ErrInternal)` (Amendment A37)
- `auth.go` (`encodeToken`): unreachable `json.Marshal` error path returned raw
  `fmt.Errorf`; returns `ErrInternal` (satisfies `forge.Error`, Amendment A38)
- `module.go` (cache sweep goroutine): goroutine spawned by `NewModule` had no
  exit path and leaked across graceful shutdown and test runs; now exits via
  `stopCh` select branch (Amendment A39)
- `module.go` (debounce callback): stashed request `Context` was cancelled before
  the 2-second debounce fired; `SQLRepo` queries silently failed on every write
  event in production; callback now builds `NewBackgroundContext(m.siteName)` at
  fire time; `debounceMu`/`debounceCtx` fields removed; `triggerSitemap(ctx)`
  renamed to `triggerRebuild()` (Amendment A41)
- `example/blog/main.go`: index template error handler used `http.Error`;
  corrected to `forge.WriteError(w, r, forge.ErrInternal)`

### Added

- `Module[T].Stop()`: exported idempotent method that closes `stopCh` (halts
  cache sweep goroutine) and calls `debounce.Stop()` (Amendment A39)
- `debouncer.Stop()`: cancels any pending `time.AfterFunc` timer (Amendment A39)
- `App.Run()` calls `Stop()` on all registered modules after `srv.Shutdown`
  returns; `stoppable` interface added (Amendment A39)

### Changed

- `FeedDisabled()` renamed to `DisableFeed()` for naming convention consistency
  (`forge.Verb(Noun)` pattern); `feedDisabledOption` internal type unchanged
  (Amendment A40)
- `forgeLLMSEntries` (unexported) renamed to `forgeLLMsEntries` to match
  `LLMsStore`/`LLMsEntry` casing convention; template tag `forge_llms_entries`
  is unchanged (Amendment A40)

---

## [1.0.4] — 2026-03-11

Fenced code block rendering, content negotiation capability gating (A35),
startup capability mismatch detection (A36), and example fixes. `forge_markdown` renders ` ``` `…` ``` ` fences as `<pre><code>`.
`negotiate()` now falls back to JSON instead of 406 when a client requests
`text/html` or `text/markdown` but the module lacks templates or `Markdownable`.
Both examples gain full working links on their welcome pages. No breaking API changes.

### Fixed

- `forge_markdown` / `forgeMarkdown` did not handle fenced code blocks; content
  between ` ``` ` fences was emitted as plain paragraph text; now rendered as
  `<pre><code>` with HTML escaping applied (XSS-safe)
- `module.go` content negotiation (`negotiate()`): returned `text/html` or
  `text/markdown` even when the module lacked templates / `Markdownable`; browsers
  and `Accept: text/html` clients received 406 Not Acceptable on JSON-only modules;
  fixed by gating on `n.html` and `n.md` capability flags instead of falling back
  to unsupported formats (Amendment A35)
- `example/docs`: module had no `SitemapConfig` option; `/docs/sitemap.xml` returned
  404; `forge.SitemapConfig{}` added to the module
- `example/docs/templates/index.html`: footer linked to `/docs/sitemap.xml`
  (404); corrected to `/sitemap.xml` (aggregate index)
- `example/api`: welcome page links to `/llms.txt`, `/llms-full.txt`,
  `/resources/sitemap.xml`, `/resources/feed.xml`, and `/robots.txt` returned
  404 or 406; module now includes `SitemapConfig{}`, `Feed(FeedConfig{...})`,
  `AIIndex(LLMsTxt, LLMsTxtFull)` options and `app.SEO(&RobotsConfig{Sitemaps: true})`
- `example/api`: `Resource` lacked `Head() Head`, so it did not satisfy
  `SitemapNode`; `regenerateSitemap` exited early; `/resources/sitemap.xml`
  returned 404; `Head()` added returning `forge.Head{Title: r.Title}`
- `example/api`: `Redirects(From("/resources/go-spec"), ...)` was registered as a
  fallback at `GET /`, but `GET /resources/{slug}` matched first; fixed by adding
  an explicit `app.Handle("GET /resources/go-spec", http.RedirectHandler(..., 301))`
  so the fixed-path pattern takes mux priority over the wildcard

### Added

- `module.go` (`NewModule`): two startup panics detect capability mismatches before
  any request is served (Amendment A36):
  - `SitemapConfig{}` given but `T` does not implement `SitemapNode` (missing
    `Head() forge.Head`) → panic with actionable message; previously `regenerateSitemap`
    exited silently and `/{prefix}/sitemap.xml` was always empty
  - `AIIndex(LLMsTxtFull)` given but `T` does not implement `Markdownable` (missing
    `Markdown() string`) → panic with actionable message; previously `/llms-full.txt`
    contained empty entries silently

---

## [1.0.3] — 2026-03-11

Startup rebuild for derived content. Sitemap fragments, RSS feeds, and AI index
entries are now populated from existing repository data at server start, so apps
with seed data or pre-loaded fixtures no longer require a manual publish event
to see correct output. No breaking API changes. (Amendment A34)

### Fixed

- `Module[T]` sitemap, feed, and AI index were only populated by the debouncer
  after a create/update/publish signal; items inserted directly into the
  repository (seed data, fixtures) never triggered regeneration; `App.Handler`
  now launches a one-shot goroutine that calls `rebuildAll` on every module
  after all stores are wired up (A34)

---

## [1.0.2] — 2026-03-11

Route mounting order fix. `GET /{prefix}/sitemap.xml` and `GET /{prefix}/feed.xml`
were never mounted because the guards in `Module.Register` checked the store pointer,
which is injected *after* `Register` returns. No breaking API changes. (Amendment A33)

### Fixed

- `Module[T].Register` guarded sitemap and feed route mounting on `m.sitemapStore != nil`
  and `m.feedStore != nil` respectively; both stores are always `nil` at registration
  time because `App.Content` calls `Register` before `setSitemap`/`setFeedStore`; routes
  are now mounted when the *config* is present and the store is read lazily at request
  time (A33)

---

## [1.0.1] — 2026-03-11

Error handling pipeline hardening. All six `http.Error` bypass sites removed;
four missing sentinels added; `errorTemplateLookup` race fixed; `Recoverer`
stack buffer increased. No breaking API changes. (Amendments A29–A32)

### Added

- `ErrBadRequest` (400 `bad_request`), `ErrNotAcceptable` (406 `not_acceptable`),
  `ErrRequestTooLarge` (413 `request_too_large`), `ErrTooManyRequests`
  (429 `too_many_requests`) sentinel errors — complete the framework's own
  HTTP status vocabulary (A29)
- `setErrorTemplateLookup` / `runErrorTemplateLookup` internal helpers that
  wrap `errorTemplateLookup` in a `sync.RWMutex`, eliminating the data race
  between `App.Handler()` start-up and in-flight requests (A29)
- `ERROR_HANDLING.md` — authoritative strategy document for error handling;
  required reading before any code that calls `WriteError` or adds a sentinel

### Fixed

- `respond()` used a direct type assertion `err.(*ValidationError)` instead of
  `errors.As`; a wrapped `*ValidationError` would have silently produced a 422
  response without field details (A29)
- `writeContent` had no `*http.Request`, forcing 406 responses via `http.Error`
  (plain text, no `X-Request-ID`); now receives `r *http.Request` and calls
  `WriteError(w, r, ErrNotAcceptable)` (A30)
- JSON decode failures in `createHandler` and `updateHandler` called
  `http.Error` (plain text, no `X-Request-ID`, always 400); now calls
  `WriteError` with `ErrRequestTooLarge` (413) when `*http.MaxBytesError` is
  detected, otherwise `ErrBadRequest` (400) (A30)
- `renderListHTML` and `renderShowHTML` called `http.Error` for nil template;
  now calls `WriteError(w, r, ErrNotAcceptable)` (A31)
- `RateLimit` called `http.Error` for 429 rate-limit responses (plain text, no
  `X-Request-ID`); now calls `WriteError(w, r, ErrTooManyRequests)` (A32)
- `Recoverer` stack capture buffer was 4096 bytes; deep stacks (recursive
  templates, chained middleware) were silently truncated; increased to 32 KB (A32)

---

## [1.0.0] — 2026-03-08

v1.0.0 stabilisation: test coverage audit, benchmarks, godoc pass, and three
reference example applications.

### Added

- `go test ./... -cover` coverage raised to ≥ 85%; targeted additions for
  `App.RedirectStore`, `TrustedProxy`, `CacheStore.Sweep`, `RedirectStore.Len`,
  `stripMarkdown`, `forgeLLMSEntries`
- `benchmarks_test.go`: 17 benchmarks covering hot paths across M1–M8;
  results in [BENCHMARKS.md](BENCHMARKS.md)
- Godoc improved on `type App` and all `App.*` methods (A18–A26); `SQLRepo[T]`
  method comments brought to parity with `MemoryRepo[T]`
- `example/blog/`: standalone blog — `Post` type, `SitemapConfig`, `Social`,
  `FeedConfig`, `AIIndex`, `On[*Post](AfterPublish)`, scheduled publishing
- `example/docs/`: standalone docs site — `Doc` type, `Headable`,
  `Markdownable`, `AIDocSummary`, `AIIndex(LLMsTxt, LLMsTxtFull, AIDoc)`,
  `RobotsConfig{AIScraper: AskFirst}`
- `example/api/`: standalone JSON API — `Resource` type, `Authenticate` +
  `BearerHMAC`, `Auth(Read(Guest), Write(Editor))`, `On[T](BeforeCreate)`,
  `Redirects`, `SecurityHeaders`, `RateLimit`

### Changed (Amendment A27)

- `middleware.go`: `Authenticate(auth AuthFunc) func(http.Handler) http.Handler`
  — middleware that populates `Context.User()` from an `AuthFunc` on every
  request; enables `Module[T]` role enforcement in production. Pairs with
  `BearerHMAC`, `CookieSession`, or `AnyAuth`.

---

## [0.8.0] — 2026-03-07

Scheduled publishing: automatic `Scheduled→Published` transition with signal
dispatch, sitemap regeneration, and feed rebuild.

### Added

- `scheduler.go`: `Scheduler` type, adaptive ticker (next-due interval, 60 s
  fallback), `schedulableModule` interface
- `module.go`: `Module[T].processScheduled` — transitions Scheduled items whose
  `ScheduledAt` is past to Published, assigns `PublishedAt`, fires `AfterPublish`,
  triggers sitemap and feed regeneration (Amendment A25)
- `forge.go`: `App` starts the scheduler before `ListenAndServe` and stops it
  after `srv.Shutdown` (Amendment A26)
- `NewBackgroundContext(host string) Context` — zero-value Context for use
  outside the HTTP request cycle, e.g. in scheduler callbacks (Amendment A24)

### Changed (Amendments A23, A25)

- `node.go`: `Node` time fields (`PublishedAt`, `ScheduledAt`, `CreatedAt`,
  `UpdatedAt`) carry `db:"..."` struct tags for `SQLRepo[T]` column mapping
  (Amendment A23)

---

## [0.7.0] — 2026-03-07

Production-ready SQL repository, redirect enforcement, chain collapse, and the
`/.well-known/redirects.json` inspect endpoint.

### Added

- `storage.go`: `SQLRepo[T any]` — production `Repository[T]` backed by
  `forge.DB`; struct-tag column mapping cached in `sync.Map`; `Table()` option
  for custom table names; full CRUD + `FindAll`/`FindBySlug` (Amendment A19)
- `redirects.go`: `RedirectCode` (`Permanent`, `Temporary`, `Gone`),
  `RedirectEntry`, `From` named type, `Redirects` module option, `RedirectStore`
  with O(1) exact + prefix lookups, chain collapse, optional DB persistence,
  `App.Redirect()`, `RedirectStore.Len()` (Amendments A20, A21)
- `redirectmanifest.go`: `/.well-known/redirects.json` always mounted, live
  serialisation of `RedirectStore`, `App.RedirectManifestAuth()` (Amendment A22)
- `forge.go`: `"/"` fallback handler wired from `redirectStore.handler()`
  (Amendment A20)

---

## [0.6.0] — 2026-03-07

Cookie consent enforcement and `/.well-known/cookies.json` compliance manifest.

### Added

- `cookies.go`: `CookieCategory` (`Necessary`, `Preferences`, `Analytics`,
  `Marketing`), `Cookie`, `SetCookie`, `SetCookieIfConsented`, `ReadCookie`,
  `ClearCookie`, `GrantConsent`, `RevokeConsent`, `ConsentFor`
- `cookiemanifest.go`: `/.well-known/cookies.json` typed JSON manifest,
  `ManifestAuth` option, `App.Cookies()`, `App.CookiesManifestAuth()`
  (Amendment A18)

---

## [0.5.0] — 2026-03-06

Open Graph, Twitter Cards, AI indexing (llms.txt + AIDoc), and opt-in RSS feeds.

### Added

- `social.go`: `Social` module option, `OpenGraph`, `TwitterCard`, card-type
  constants, `SocialOverrides`; `forge:head` partial renders OG and Twitter Card
  `<meta>` tags automatically when Social is registered
- `ai.go`: `AIIndex` module option; `LLMsTxt`, `LLMsTxtFull`, `AIDoc` flags;
  `LLMsStore`; `/llms.txt` compact index; `/llms-full.txt` full markdown corpus
  (requires `Markdownable`); `/{prefix}/{slug}/aidoc` per-item endpoint
  (requires `Markdownable`); `AIDocSummary` interface; `WithoutID` option;
  gzip compression in AI handlers (Amendment A17)
- `feed.go`: `Feed` module option, `FeedConfig`, `FeedDisabled`;
  `/{prefix}/feed.xml` per-module RSS 2.0; `/feed.xml` aggregate index;
  signal-driven regeneration (Amendment A16)

### Changed

- `Markdownable` interface (`Markdown() string`) moved from `module.go` to
  `ai.go`; consumed by `/llms-full.txt` and `/{slug}/aidoc`

---

## [0.4.0] — 2026-03-05

HTML rendering, template helpers, and content negotiation.

### Added

- `templatedata.go`: `TemplateData[T any]` (`Content`, `Head`, `User`,
  `Request`, `SiteName`), `NewTemplateData`
- `templates.go`: `Templates(dir)` / `TemplatesOptional(dir)` module options;
  `forge:head` partial (title, meta description, canonical, OG, Twitter Card,
  JSON-LD); error page template `forge:error`; HTML render path for list + show
- `templatehelpers.go`: `forge_meta`, `forge_date`, `forge_rfc3339`,
  `forge_markdown`, `forge_excerpt`, `forge_csrf_token`, `forge_llms_entries`;
  `TemplateFuncMap()` export

### Changed (Amendments A6, A7, A8, P3)

- Templates parsed once at `app.Run()` / `app.Handler()` startup; missing
  template files cause fast-fail (Amendment P3)
- `forge:head` emits `BreadcrumbList` JSON-LD when `Head.Breadcrumbs` is
  non-empty (Amendment A8)
- Error pages rendered via `forge:error` when available; fallback to
  `WriteError` plain text (Amendments A6, A7)

---

## [0.3.0] — 2026-03-03

SEO metadata, JSON-LD structured data, per-module sitemaps, and robots.txt.

### Added

- `head.go`: `Head`, `Image`, `Excerpt`, `Crumb`, `Crumbs`, `Breadcrumb`,
  `Headable` interface, `HeadFunc` module option
- `schema.go`: JSON-LD types — `Article`, `Product`, `FAQPage`, `HowTo`,
  `Event`, `Recipe`, `Review`, `Organization`, `BreadcrumbList`; `SchemaOf`
  serialises to `<script type="application/ld+json">`
- `sitemap.go`: per-module `/{prefix}/sitemap.xml`, `/sitemap.xml` aggregate
  index, `SitemapConfig` option, `SitemapStore`, `SitemapPrioritiser`
  interface, debounce-driven async regeneration (Amendment P1)
- `robots.go`: auto-generated `robots.txt`, `RobotsConfig`, `AskFirst` /
  `Disallow` AI-crawler policy constants, `App.SEO()`

---

## [0.2.0] — 2026-03-02

App bootstrap, HTTP server, graceful shutdown, and the `forge-pgx` companion
module.

### Added

- `forge.go`: `Config`, `MustConfig`, `New`, `App` (`Use`, `Content`, `Handle`,
  `Run`, `Handler`), `Registrator` interface, graceful shutdown on
  `SIGINT`/`SIGTERM`
- `forge-pgx` (`smeldr.dev/core-pgx`): `forgepgx.Wrap(*pgxpool.Pool)
  forge.DB` — pgx/v5 adapter; no generated code, no ORM

---

## [0.1.0] — 2026-03-01

Foundation: the minimum needed to build a real application.
Zero third-party dependencies. All types in package `forge`.

### Added

- `errors.go`: `Error` interface, `ValidationError`, `Err`, `Require`,
  `WriteError`; sentinels `ErrNotFound`, `ErrGone`, `ErrForbidden`, `ErrUnauth`,
  `ErrConflict`
- `roles.go`: `Role`, `Guest`/`Author`/`Editor`/`Admin` (levels 10/20/30/40 —
  Amendment R1), `HasRole`, `IsRole`, `NewRole`, `Read`/`Write`/`Delete` options
- `node.go`: `Node`, `Status` (`Draft`, `Scheduled`, `Published`, `Archived`),
  `NewID` (UUID v7 — Amendment S1), `GenerateSlug`, `UniqueSlug`, `RunValidation`
- `context.go`: `User` (Amendment R3), `GuestUser`, `Context` interface,
  `ContextFrom`, `NewTestContext`
- `signals.go`: `Signal`, signal constants (`BeforeCreate`, `AfterCreate`,
  `BeforeUpdate`, `AfterUpdate`, `BeforeDelete`, `AfterDelete`, `AfterPublish`),
  `On[T]` generic option (Amendment S2), debouncer
- `storage.go`: `DB` interface, `Query[T]`, `QueryOne[T]`, `Repository[T]`
  interface, `MemoryRepo[T]`, `ListOptions`
- `auth.go`: `AuthFunc` interface (Amendment S8), `BearerHMAC`, `CookieSession`,
  `BasicAuth` (production warning — Amendment S7), `AnyAuth`, `SignToken`
  (ttl-aware — Amendment S10)
- `middleware.go`: `RequestLogger`, `Recoverer`, `CORS`, `MaxBodySize`,
  `RateLimit` (with `TrustedProxy` — Amendment S12), `SecurityHeaders`,
  `InMemoryCache`, `CacheStore`, `CSRF` (Amendments S6, S11), `Chain`
- `module.go`: `Module[T any]` (Amendment M3), `NewModule`, `At`, `Cache`,
  `Auth`, `Middleware`, `Repo`, `On`; content negotiation (`application/json`,
  `text/html`, `text/markdown`); per-module LRU; lifecycle enforcement
- `mcp.go`: `MCPOperation`, `MCPRead`/`MCPWrite`, `MCP()` no-op placeholder
  (reserved for Milestone 10)

---

## Version policy

Forge uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html):

- **MAJOR** — breaking change to any exported symbol in `smeldr.dev/core`
- **MINOR** — new exported symbols; backward-compatible amendments
- **PATCH** — bug fixes with no API change

v1.0.0 and all future v1.x releases maintain full backward compatibility.
A v2 will be introduced as a separate import path
(`smeldr.dev/core/v2`) following Go module conventions.

See [DECISIONS.md](DECISIONS.md) for the architectural rationale behind every
design choice in this release.
