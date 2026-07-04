# Smeldr — Decisions Archive Phase 12

Archived 2026-07-04: A184–A190 from recent.md.

---

## A184 — Fix data race in state_test.go (v1.45.1)

**Status:** Agreed
**Date:** 2026-06-30
**Scope:** test-only fix — no production code changed

**Problem:** `go test -race ./...` on GitHub Actions (run #28463386642) failed with a DATA RACE in `TestFireAsyncTriggers_asyncTrigger_dispatched`. Root cause: `fireAsyncTriggers` spawns a goroutine that calls `slog.WarnContext`, which writes to the slog handler's `bytes.Buffer`. The test goroutine reads `buf.String()` after a `time.Sleep(50ms)` — but `bytes.Buffer` is not goroutine-safe and `time.Sleep` provides no happens-before edge. The race detector correctly flagged concurrent access:

```
Read at state_test.go:923 buf.String() — test goroutine
Write at state.go:364 slog.WarnContext → TextHandler → buf.Write — fireAsyncTriggers goroutine
```

**Fix:** Introduced `type safeBuf struct` in `state_test.go` — a `bytes.Buffer` wrapped with `sync.Mutex` implementing `io.Writer` and a `String()` method. Replaced `var buf bytes.Buffer` with `var buf safeBuf` in three test functions:

- `TestFireAsyncTriggers_syncTrigger_skipped`
- `TestFireAsyncTriggers_asyncTrigger_dispatched`
- `TestDynamicTypeRepo_SetStatus_fireAsyncTriggers`

`sync` added to imports. No change to production code.

**Why no production fix:** The race is in the test harness, not in `fireAsyncTriggers`. The `time.Sleep` approach is retained (it gives the goroutine time to run and log); only the shared buffer is made safe. A channel-based approach would also work but requires more structural change to the test.

Coverage: 96.0%. core v1.45.1.

---

## A185 — T23 Step 11: Signal MCP Tools in smeldr/mcp (v1.25.0, 2026-06-30)

**Date:** 2026-06-30
**Status:** Agreed
**Level:** 1

New file `signal_tools.go` in smeldr/mcp adds two MCP tools that give agents direct read/write access to the `smeldr_signals` table. Follows Option A (direct DB access), consistent with the pattern established in `state_tools.go`.

**`create_signal`:** Required params: sender, receiver, signal_type. Optional: task_ref, message, sequence (int). Inserts into smeldr_signals with status="pending". id=smeldr.NewID(); slug=signalSlug(sender, signalType, id) — e.g. "core-plan-ready-01936b4f" (base from GenerateSlug + first 8 chars of id). DB exec failure → -32603 + slog.Error. Returns {id, slug, status}.

**`list_signals`:** Required: receiver. Optional: state (default "pending"). SELECT from smeldr_signals WHERE receiver=? AND status=? ORDER BY created_at ASC. Fail-open: "no such table" error → slog.Warn + empty list (not error). Returns {signals: [], count: N}. Scan uses string for created_at/updated_at (SQLite stores TIMESTAMPTZ as text).

**tool.go wiring:** New dispatch block between state tools and dynamic content tools. Guard: `s.app.Config().DB != nil && isSignalTool(p.Name)`. Both tools: s.authorise(ctx) (Author role). Uses existing coalesceArgs/stringArg/stringArgOr/intArgOr helpers.

**Helpers:** isSignalTool(name) bool — switch on "create_signal"/"list_signals". signalSlug(sender, signalType, id) string — GenerateSlug(sender+"-"+signalType) + "-" + id[:8]. handleToolsList: appends signalToolDefs() in same DB-gated block as stateToolDefs().

**Tests:** newSignalServer(t) calls smeldr.CreateOrchestrationTables(db) after newDynamicServer. newSignalServerNoDB(t) for missing-table scenarios. 16 test functions covering happy paths, missing params (-32602), role rejection (-32001), table-missing (fail-open for list, -32603 for create), signalSlug unit tests, unknown-name fallthrough.

go.mod: smeldr.dev/core v1.45.0 → v1.45.1. Coverage: 96.0%. mcp v1.25.0.

---

## A186 — T23 Step 12: ConflictPolicy enforcement on StateFlow (v1.46.0)

**Date:** 2026-07-01
**Status:** Committed
**Version:** core v1.46.0

### Decision

Add opt-in conflict enforcement to `StateFlow`. Two new exported fields — `ActiveState string` and `ConflictPolicy ConflictPolicy` — declare the state where a uniqueness invariant applies and how to handle violations. Zero value means no enforcement.

`ConflictPolicy` is `type ConflictPolicy string` with two constants:
- `ConflictReject = "reject"` — return `ErrConflict` if any item is already in `ActiveState`
- `ConflictSupersede = "supersede"` — transition conflicting items to "superseded" and optionally assert a "supersedes" relation via `RelationStore`

Both policies are opt-in and fail-open: any DB error returns nil (transition proceeds).

### Implementation

**state.go:**
- `ConflictPolicy` type + `ConflictReject`/`ConflictSupersede` constants
- `StateFlow.ActiveState` + `StateFlow.ConflictPolicy` optional fields (zero value = no enforcement)
- `applyConflictPolicy(ctx, db, rs, typeName, toState, newItemID) error` — entry point: probe SQLite, look up flow by typeName, check toState==ActiveState, dispatch to reject or supersede logic; auto-detects static table (`camelToSnake(typeName)+"s"`) vs dynamic content table
- `conflictRejectCheck` — COUNT query; ErrConflict when count > 0; fail-open on DB error
- `conflictSupersede` — collects conflicting IDs via `conflictIDs`, UPDATE each to "superseded", optionally assert "supersedes" relation; all item-level errors are warn+continue
- `conflictIDs` — builds SELECT id query for static or dynamic table; returns []string

**migrate.go:**
- `migrateStateFlowConflictColumns(ctx, db) error` — PRAGMA-probe adds `active_state` + `conflict_policy` TEXT NOT NULL DEFAULT '' columns to `smeldr_state_flows`; called from `migrateStateFlows`; idempotent; fail-open on non-SQLite

**RegisterFlow update:**
- UPDATE persists `ActiveState` and `ConflictPolicy` after INSERT OR IGNORE + SELECT id; runs on every RegisterFlow call so re-registration updates the policy

**module.go:** `MCPPublish`, `MCPSchedule`, `MCPArchive` call `applyConflictPolicy` after `validateTransition`
**dynamic.go:** `DynamicTypeRepo.SetStatus` calls `applyConflictPolicy` after `validateTransition`

### Why fail-open?

DB errors in the conflict check should not block legitimate transitions — the conflict invariant is a best-effort guarantee, not a hard write lock. A net-split or momentary DB hiccup should not prevent content from being published.

### RelationStore wiring

Current call sites pass `rs = nil` (Module[T] does not carry a RelationStore). The `conflictSupersede` function checks `rs != nil && newItemID != ""` before asserting the relation. This is intentional: the feature is usable without relations, and RelationStore can be wired in later via a dedicated amendment.

### Consequences

- No existing behavior changes — `ActiveState` and `ConflictPolicy` default to "".
- No exported symbol removed or renamed.
- `example_test.go`: no existing Example broken.
- All DB errors are fail-open — coverage gate still 96.0%.

Coverage: 96.0%. core v1.46.0.

---

## A187 — T23 Step 13: schedule-eval trigger type + DrainEvalQueue (core v1.47.0, 2026-07-01)

**Date:** 2026-07-01
**Status:** Agreed
**Level:** 2 (new exported types, cross-module signature change, new public method, cross-repo change)

### Decision

Implement the `schedule-eval` async trigger type and background drain loop for periodic state re-evaluation. Two components: (1) a persistent eval queue (`smeldr_eval_queue`) with metadata for deferred transitions, and (2) a public `DrainEvalQueue` method that processes due rows.

### Implementation

**state.go:**

- `TransitionTrigger` exported type: `FromState, ToState, TriggerClass, TriggerType string; Config string` — declared in `StateFlow.Triggers []TransitionTrigger` and persisted by `RegisterFlow` to `smeldr_transition_triggers` (idempotent via SELECT COUNT check before INSERT).
- `fireAsyncTriggers` signature extended: added `itemID string` parameter — required for `schedule-eval` triggers to identify the affected item.
- New `schedule-eval` case in `fireAsyncTriggers` dispatch: reads `eval_field` from trigger Config (JSON), queries the affected item's row via `resolveItemTable`, reads the evaluation timestamp from the named column, INSERTs into `smeldr_eval_queue`. Fail-open: all errors log `slog.Warn` and do not block the original transition.
- `resolveItemTable(ctx, db, typeName) string` — probes `sqlite_master` for `smeldr_<snake>s` (e.g. `smeldr_decisions`), then `<snake>s`, then falls back to `smeldr_dynamic_content`. Returns `""` on probe failure.
- `isNoSuchTable(err error) bool` — checks whether a DB error contains "no such table".
- `App.DrainEvalQueue(ctx context.Context) (triggered, skipped int, err error)` — SELECT due rows from `smeldr_eval_queue WHERE eval_at <= now`. For each row: direct SQL UPDATE on the resolved table, increment `triggered` on success or `skipped` on failure. DELETE each row regardless of UPDATE outcome. Fail-open on nil DB and missing table.

**migrate.go:**

- `smeldr_eval_queue` table added to `migrateStateFlows()` stmts: `id TEXT PRIMARY KEY, type_name TEXT NOT NULL, item_id TEXT NOT NULL, to_state TEXT NOT NULL, eval_at DATETIME NOT NULL, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, UNIQUE(type_name, item_id, to_state)`.

**dynamic.go:**

- `DynamicTypeRepo.SetStatus` call to `fireAsyncTriggers` extended: passes item `id` as `itemID` parameter.

**orchestration.go:**

- `orchDecisionFlow()` wired with two `TransitionTrigger` entries:
  - `proposed → ratified`, class=`async`, type=`schedule-eval`, config `{"eval_field":"next_eval_at","to_state":"pending-re-evaluation"}`
  - `pending-re-evaluation → ratified`, same config — restarts the freshness cycle on every re-evaluation

**smeldr.dev/agent (sweep.go):**

- `NewEvalQueueScheduler(schedule, timezone string, app interface{DrainEvalQueue(ctx) (int, int, error)}) (*SweepScheduler, error)` — wraps `DrainEvalQueue` as a `SweepFunc` for the background loop. Default schedule: `"*/5 * * * *"`. Uses inline interface to avoid importing smeldr in sweep.go. agent v0.7.0.

### Consequences

- `fireAsyncTriggers` signature change: all call sites in `module.go` and `dynamic.go` updated to pass `itemID`.
- No existing behaviour changes — unknown `trigger_type` values log `slog.Warn` and skip.
- `DrainEvalQueue` is opt-in: call `agent.NewEvalQueueScheduler` or invoke manually.
- All DB errors are fail-open — eval queue is best-effort, not a hard requirement.

Coverage: 96.0%. core v1.47.0 · agent v0.7.0.

---

## A188 — T49 Step 1: Governance schema (v1.48.0)

**Date:** 2026-07-02  
**Status:** Implemented  
**Level:** 2

### Decision

New file `governance.go` introduces `migrateGovernance(ctx, db)`: creates three new tables (smeldr_roles, smeldr_role_grants, smeldr_tool_policies) with two indexes, seeds default roles and tool policies, and migrates existing token role strings to grants. Not wired into `New()` — opt-in via `App.Governance()` in Step 2.

**New type: `ScopeMode`**

Three exported constants define scope semantics for role grants:
- `ScopeGlobal` — role applies across all content (no anchor)
- `ScopeStatic` — role scoped to specific static content (e.g. by page ID)
- `ScopeDynamic` — role scoped by relation type (e.g. "can edit all Tasks related to Project X")

**New tables:**

- `smeldr_roles` (id TEXT PRIMARY KEY, name TEXT UNIQUE NOT NULL, operations TEXT NOT NULL [JSON array], scope_mode TEXT, scope_relation_kind TEXT, scope_direction TEXT, trust_level INTEGER DEFAULT 0, allow_self_approval INTEGER DEFAULT 0, created_at TIMESTAMP, updated_at TIMESTAMP)
- `smeldr_role_grants` (id TEXT PRIMARY KEY, token_id TEXT NOT NULL, role_id TEXT NOT NULL [FK smeldr_roles], scope_static JSON, scope_anchor_id TEXT, created_at TIMESTAMP; UNIQUE (token_id, role_id, scope_anchor_id) via WHERE NOT EXISTS guard to handle NULL anchor_id correctly)
- `smeldr_tool_policies` (id TEXT PRIMARY KEY, tool_name TEXT UNIQUE NOT NULL, required_op TEXT NOT NULL, created_at TIMESTAMP)

**Indexes:**

- `idx_role_grants_token` on (token_id)
- `idx_role_grants_role_anchor` on (role_id, scope_anchor_id)

**Seeding:**

- `seedDefaultRoles()` — creates three global-scope roles: author: ["create", "read", "update", "publish", "archive"]; editor: [+delete, manage]; admin: [+delete, manage, administer, review, approve, define-type, define-flow, define-relation-kind]. All use `scope_mode='global'`, `trust_level=0`, idempotent via `INSERT OR IGNORE`
- `seedToolPolicies()` — one row per built-in MCP tool; operation vocabulary: `manage` (Editor-tier operational tools), `administer` (Admin-only infrastructure); no behaviour change on day one
- `migrateTokenGrants()` — reads existing smeldr_tokens rows with non-null `role` field, inserts grant rows via `WHERE NOT EXISTS`; fail-open if smeldr_tokens missing

### Consequences

- `ScopeMode`, `ScopeGlobal`, `ScopeStatic`, `ScopeDynamic` are new exported symbols; no change to existing `App` API or request paths
- `migrateGovernance` is testable standalone and called by `App.Governance()` in Step 2
- 22 new tests in `governance_test.go`
- Zero impact on any MCP tool output, content routing, or authentication until Step 2

Coverage: 96.0%. core v1.48.0.

---

## A189 — T49 Step 2: RoleStore + App.Governance() (v1.49.0)

**Date:** 2026-07-02  
**Status:** Implemented  
**Level:** 2

### Decision

`governance.go` extended with the full authorization query layer for T49.

**New exported types:**

- `RoleDefinition` struct: `Name string`, `Operations []string`, `ScopeMode ScopeMode`, `ScopeRelationKind string`, `ScopeDirection string`, `TrustLevel int`, `AllowSelfApproval bool`
- `RoleGrant` struct: `ID string`, `TokenID string`, `RoleName string`, `ScopeStatic []string`, `ScopeAnchorID string`, `CreatedAt time.Time`
- `AuthTarget` struct: `TypeName string`, `ID string`, `Slug string` — `Slug` is display/logging only; authorization comparisons use `ID`

**New type: `RoleStore`**

`NewRoleStore(db DB) *RoleStore` — wraps the DB handle for all role operations.

Methods:
- `DefineRole(ctx, RoleDefinition) error` — upsert role by name; rejects `TrustLevel == 1`
- `Grant(ctx, RoleGrant) (string, error)` — bind token to role with optional scope; global-scope grants use `WHERE NOT EXISTS` guard
- `Revoke(ctx, grantID string) error` — delete grant row; no-op on unknown ID
- `ListGrants(ctx, tokenID string) ([]RoleGrant, error)` — returns all grants for the token
- `Authorized(ctx, tokenID, op string, target AuthTarget) (bool, error)` — evaluates scope modes: ScopeGlobal (immediate match), ScopeStatic (TypeName+":"+ID exact or TypeName+":*" wildcard), ScopeDynamic (one-hop smeldr_relations query with asserted+active edge predicate); pre-collects rows before closing cursor to avoid SQLite nested-connection deadlock

**App wiring:**

- `App.Governance(store *RoleStore) error` — validates `store.db == a.cfg.DB`; calls `migrateGovernance`; stores reference in `App.governance`
- `App.RoleStore() *RoleStore` — returns wired store or nil
- `smeldr.go`: `governance *RoleStore` field added to `App`

### Consequences

- All three scope modes queryable via `RoleStore.Authorized`; inferred relations cannot escalate scope
- Static scope uses `ID` not `Slug`
- `App.Governance` validates DB identity at wiring time
- `TrustLevel == 1` rejected at `DefineRole` time
- Zero change to request routing, MCP tools, or authentication until Step 3

Coverage: 96.0%. core v1.49.0.

---

## A190 — T49 Step 2.5: Governance mutation audit trail (2026-07-02)

**Context:** T49 Step 2 (A189) shipped `DefineRole`/`Grant`/`Revoke` — functions that mutate authority itself. Changing them with no record violates Article I ("authority never changes silently"). Step 3 (MCP tool enforcement) would make these tables load-bearing. This step closes the audit gap before enforcement goes live.

**Decision:** Opt-in governance mutation audit trail. New types:
- `GovernanceAuditRecord` — captures actor, action ("define_role" | "grant" | "revoke"), target kind/ID, before/after JSON, and timestamp
- `GovernanceAuditStore` interface (`Append`) — write-only for this step
- `sqlGovernanceAuditStore` + `NewGovernanceAuditStore(db) GovernanceAuditStore` — SQL implementation
- `CreateGovernanceAuditTable(db) error` — creates `smeldr_governance_audit` + `idx_governance_audit_actor`; opt-in, NOT called by `migrateGovernance`
- `RoleStore.WithAudit(actorTokenID string, log GovernanceAuditStore) *RoleStore` — shallow-copy pattern

**Mutation recording:**
- `DefineRole`: SELECT before-state, INSERT OR IGNORE + UPDATE, resolve role ID, `Append`
- `Grant`: INSERT WHERE NOT EXISTS, resolve grant ID, `Append` (before always `{}`)
- `Revoke`: SELECT before-state, DELETE, `Append` (after always `{}`)

**Fail-closed on Append error:** mutation method returns error — DB has no BeginTx. Fail-closed is correct: an authority change with no audit record is a silent change (Article I violation).

**Consequences:**
- 5 new exported symbols: `GovernanceAuditRecord`, `GovernanceAuditStore`, `NewGovernanceAuditStore`, `CreateGovernanceAuditTable`, `RoleStore.WithAudit`
- New unexported fields on `RoleStore`: `actorTokenID string`, `auditStore GovernanceAuditStore`
- Apps that never call `WithAudit` see zero behaviour change
- 15 new tests; coverage 96.0%

Coverage: 96.0%. core v1.50.0.

---
