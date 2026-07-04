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

---

## A184 — Fix data race in state_test.go (v1.45.1)

**Status:** Agreed
**Date:** 2026-06-30
**Scope:** test-only fix — no production code changed

**Problem:** `go test -race ./...` on GitHub Actions (run #28463386642) failed with a DATA RACE in
`TestFireAsyncTriggers_asyncTrigger_dispatched`. Root cause: `fireAsyncTriggers` spawns a goroutine
that calls `slog.WarnContext`, which writes to the slog handler's `bytes.Buffer`. The test goroutine
reads `buf.String()` after a `time.Sleep(50ms)` — but `bytes.Buffer` is not goroutine-safe and
`time.Sleep` provides no happens-before edge. The race detector correctly flagged concurrent access:

```
Read at state_test.go:923 buf.String() — test goroutine
Write at state.go:364 slog.WarnContext → TextHandler → buf.Write — fireAsyncTriggers goroutine
```

**Fix:** Introduced `type safeBuf struct` in `state_test.go` — a `bytes.Buffer` wrapped with
`sync.Mutex` implementing `io.Writer` and a `String()` method. Replaced `var buf bytes.Buffer`
with `var buf safeBuf` in three test functions:

- `TestFireAsyncTriggers_syncTrigger_skipped`
- `TestFireAsyncTriggers_asyncTrigger_dispatched`
- `TestDynamicTypeRepo_SetStatus_fireAsyncTriggers`

`sync` added to imports. No change to production code.

**Why no production fix:** The race is in the test harness, not in `fireAsyncTriggers`. The
`time.Sleep` approach is retained (it gives the goroutine time to run and log); only the shared
buffer is made safe. A channel-based approach would also work but requires more structural change
to the test.

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
- `smeldr_role_grants` (id TEXT PRIMARY KEY, token_id TEXT NOT NULL [FK smeldr_tokens], role_id TEXT NOT NULL [FK smeldr_roles], scope_static JSON, scope_anchor_id TEXT, created_at TIMESTAMP; UNIQUE (token_id, role_id, scope_anchor_id) via WHERE NOT EXISTS guard to handle NULL anchor_id correctly)
- `smeldr_tool_policies` (id TEXT PRIMARY KEY, tool_name TEXT UNIQUE NOT NULL, required_op TEXT NOT NULL, created_at TIMESTAMP)

**Indexes:**

- `idx_role_grants_token` on (token_id)
- `idx_role_grants_role_anchor` on (role_id, scope_anchor_id)

**Seeding:**

- `seedDefaultRoles()` — creates three global-scope roles:
  - author: ["create", "read", "update", "publish", "archive"]
  - editor: [+delete, manage]
  - admin: [+delete, manage, administer, review, approve, define-type, define-flow, define-relation-kind]
  All use `scope_mode='global'`, `trust_level=0`, idempotent via `INSERT OR IGNORE`
- `seedToolPolicies()` — one row per built-in MCP tool; operation vocabulary: `manage` (Editor-tier operational tools — composition, `transition_item`, `preview_impact`, nav CRUD, redirect CRUD, dynamic-content Editor tools), `administer` (Admin-only infrastructure — tokens, webhooks, page-meta); `approve`/`review` reserved for the Plan governance loop (§6) and must NOT gate generic Admin infrastructure; no behaviour change on day one (tool enforcement added in Step 2)
- `migrateTokenGrants()` — reads existing smeldr_tokens rows with non-null `role` field, looks up matching role by name, and inserts grant rows via `WHERE NOT EXISTS` (not `INSERT OR IGNORE`, since SQLite allows multiple NULLs in UNIQUE columns); fail-open if smeldr_tokens missing (logs warning, continues)

### Consequences

- `ScopeMode`, `ScopeGlobal`, `ScopeStatic`, `ScopeDynamic` are new exported symbols; no change to existing `App` API or request paths
- `migrateGovernance` is testable standalone and called by `App.Governance()` in Step 2
- 22 new tests in `governance_test.go` cover schema creation, default role seeding, tool policy seeding, token grant migration, edge cases (missing smeldr_tokens table, null role), and all DB error paths (fail-open)
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
- `DefineRole(ctx, RoleDefinition) error` — upsert role by name (INSERT OR IGNORE + UPDATE, two statements — preserves `created_at`); rejects `TrustLevel == 1` (semantics deferred to a future spike — only 0 and 2 are accepted); `json.Marshal([]string)` is infallible and the error path is unreachable
- `Grant(ctx, RoleGrant) (string, error)` — bind token to role with optional scope anchor or static patterns; global-scope grants (nil anchor) use `WHERE NOT EXISTS` guard (SQLite allows multiple NULLs in UNIQUE columns, so `INSERT OR IGNORE` would silently duplicate)
- `Revoke(ctx, grantID string) error` — delete grant row; no-op on unknown ID
- `ListGrants(ctx, tokenID string) ([]RoleGrant, error)` — returns all grants for the token; empty tokenID returns all grants across all tokens
- `Authorized(ctx, tokenID, op string, target AuthTarget) (bool, error)` — evaluates scope modes:
  - `ScopeGlobal` — matches immediately if op is in the role's operations
  - `ScopeStatic` — matches `target.TypeName+":"+target.ID` exactly, or `target.TypeName+":*"` wildcard; uses `ID`, not `Slug` (slug is not stable identity after T30/A125 renames)
  - `ScopeDynamic` — one-hop `smeldr_relations` query with predicate `edge_class='asserted' AND (invalid_at IS NULL OR invalid_at > now)` — prevents inferred (unconfirmed) relations from granting scope (privilege-escalation fix from design review §5.3)
  - `pendingErr` pattern: dynamic-scope query errors accumulate and are only surfaced if no grant matched
  - Pre-collects all grant rows into `[]authorizedGrant` before closing cursor, then calls `relationExists` — avoids SQLite nested-connection deadlock (only one active statement per connection)

**App wiring:**

- `App.Governance(store *RoleStore) error` — validates `store.db == a.cfg.DB` (mismatch guard: strict bar given authorization blast radius); calls `migrateGovernance`; stores reference in `App.governance`
- `App.RoleStore() *RoleStore` — returns wired store or nil
- `smeldr.go`: `governance *RoleStore` field added to `App`

### Consequences

- All three scope modes queryable via `RoleStore.Authorized`; inferred relations cannot escalate scope (design review fix §5.3)
- Static scope uses `ID` not `Slug`; `AuthTarget.Slug` stays for display/logging only (design review fix §5.2)
- `App.Governance` validates DB identity at wiring time (design review fix §9)
- `TrustLevel == 1` rejected at `DefineRole` time (design review fix §6)
- Zero change to request routing, MCP tools, or authentication until Step 3

Coverage: 96.0%. core v1.49.0.

---

## A190 — T49 Step 2.5: Governance mutation audit trail (2026-07-02)

**Context:** T49 Step 2 (A189) shipped `DefineRole`/`Grant`/`Revoke` — functions that mutate authority itself (which roles exist, who holds them, over what scope). Changing them with no record violates Article I ("authority never changes silently"). Step 3 (MCP tool enforcement) would make these tables load-bearing. This step closes the audit gap before enforcement goes live.

**Decision:** Opt-in governance mutation audit trail. New types:
- `GovernanceAuditRecord` — captures actor, action ("define_role" | "grant" | "revoke"), target kind/ID, before/after JSON, and timestamp
- `GovernanceAuditStore` interface (`Append`) — write-only for this step; query/list comes in Step 7
- `sqlGovernanceAuditStore` + `NewGovernanceAuditStore(db) GovernanceAuditStore` — SQL implementation
- `CreateGovernanceAuditTable(db) error` — creates `smeldr_governance_audit` + `idx_governance_audit_actor`; opt-in, NOT called by `migrateGovernance` (same discipline as `smeldr_audit_log`)
- `RoleStore.WithAudit(actorTokenID string, log GovernanceAuditStore) *RoleStore` — shallow-copy pattern; existing call sites (`NewRoleStore(db)` + direct method calls) are unchanged

**Mutation recording:**
- `DefineRole`: SELECT before-state (existing role or `{}`), INSERT OR IGNORE + UPDATE, resolve role ID, `Append`
- `Grant`: INSERT WHERE NOT EXISTS, resolve grant ID, `Append` (before always `{}`)
- `Revoke`: SELECT before-state, DELETE, `Append` (after always `{}`)

**Fail-closed on Append error:** If `Append` fails, the mutation method returns an error — but the DB has no transaction primitive (`DB` interface: QueryContext/ExecContext/QueryRowContext only, no BeginTx). The mutation already took effect. Fail-closed is still correct over fail-open: an authority change with no audit record is a silent change (Article I violation). Callers that receive an error should verify current state before assuming rollback; `DefineRole` and `Grant` are idempotent on retry, `Revoke` is idempotent by nature.

**Rejected alternatives:**
- Extending DB interface with BeginTx for real atomicity: out of scope for this step; would affect every DB consumer in the codebase
- Fail-open (log + continue): would mean the authority structure changed silently when Append fails — exact problem this step exists to close

**Consequences:**
- 5 new exported symbols: `GovernanceAuditRecord`, `GovernanceAuditStore`, `NewGovernanceAuditStore`, `CreateGovernanceAuditTable`, `RoleStore.WithAudit`
- New unexported fields on `RoleStore`: `actorTokenID string`, `auditStore GovernanceAuditStore`
- Apps that never call `WithAudit` see zero behaviour change
- `set_tool_policy` audit is out of scope for this step (Step 7/8)
- 15 new tests; coverage 96.0%

Coverage: 96.0%. core v1.50.0.

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
