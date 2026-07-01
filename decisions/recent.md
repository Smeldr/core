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

---

## A170 — docs(cleanup): archive decisions A151–A157, migrate CLAUDE.md, backfill A167–A169 (2026-06-23)

**Context:** decisions/recent.md approached the 20KB limit. A167/A168/A169 were committed without DECISIONS.md entries. copilot-instructions.md was still the source of corepilot instructions rather than CLAUDE.md.

**Decision:** Archive A151–A157 (T104 substrate + SSRF + PageMeta) to `decisions/phase9-archive.md`. Backfill DECISIONS.md index rows and recent.md bodies for A167/A168/A169. Replace CLAUDE.md with full migrated content from `.github/copilot-instructions.md` with corrections: title (`Copilot Instructions` → `Agent Instructions`), Go version (1.22 → 1.26.4), skill file reference (`.claude/skills/smeldr.md` → canonical common path), coverage gate added to non-negotiable rules, new signal protocol section added. Delete `.github/copilot-instructions.md`. Delete `NEXT.md` and `plans/core-next-plan.md`. Docs-only; no version bump, no tag, no GitHub release.

---

## A173 — Fix FilterTags type assertion in serveblocks.go (T108)

**Date:** 2026-06-29
**Status:** Agreed
**Level:** 1

FilterTags is defined as Type: "array" in schemas.go. The previous assertion `.(string)` always silently failed, making the "not yet supported" slog.Warn dead code. Fixed to `.([]any)` with a `len(tags) > 0` guard. Correctness only — FilterTags SQL filtering remains out of scope for this fix.

---

## A174 — T23 Step 1: Custom State Flows schema and default flow seed

**Date:** 2026-06-29
**Status:** Agreed
**Level:** 2

Add migrateStateFlows() to migrate.go: creates smeldr_state_flows, smeldr_states, smeldr_transitions, smeldr_transition_triggers using CREATE TABLE IF NOT EXISTS. Seeds the default flow (draft/scheduled/published/archived) via INSERT OR IGNORE with an id-lookup for idempotency. Called from New() alongside migrateLegacyTableNames(). No exported Go symbols in this step. Prerequisite for T23 Steps 2–9 (RegisterFlow, transition validation, MCP tools, suppresses_signals, async triggers).

---

## A171 — Wire 6 relation MCP tools in smeldr/mcp (mcp v1.23.0, 2026-06-25)

**Context:** RelationStore.MCPAssertRelation and five sibling methods were added to core in A162/A163 (core v1.42.5–v1.42.6) but were never wired as MCP tools in the smeldr/mcp package. Content Relations docs (content-relations-mcp.md) carried a NOTE warning against publication until this wiring shipped.

**Decision:** New `relation_tools.go` in smeldr/mcp. `Server` gains a `relationStore *smeldr.RelationStore` field set in `New()` via `app.RelationStore()`. Gate: all six tools are registered only when `relationStore != nil` (i.e. `app.Relations(store)` was called) — no new `ServerOption` required. Tool dispatch added to `handleToolsCall` before module-scoped authorisation; tool definitions added to `handleToolsList`. Roles: Author (assert_relation, propose_relation, get_relations, list_relation_kinds), Editor (preview_impact), Admin (upsert_relation_kind). Output uses `relationEdgeMap`/`relationKindMap` helpers for snake_case keys, since `RelationEdge` and `RelationKindDef` have `db:` tags only. go.mod: `smeldr.dev/core` v1.42.9 → v1.43.1. 16 tests in `relation_tools_test.go`. mcp v1.22.1 → v1.23.0.

---

## A175 — T23 Step 2: RegisterFlow and state-flow types (v1.44.0, 2026-06-29)

**Date:** 2026-06-29
**Status:** Agreed
**Level:** 2

New file `state.go` exports `StateFlow`, `State`, `Transition` types. `StateFlow` has Name, TypeName, Description, States []State, Transitions []Transition. `State` has Name, IsInitial, IsTerminal, SuppressesSignals. `Transition` has From, To, RequiredRole (empty means no role required; stored as SQL NULL). `App.RegisterFlow(flow StateFlow) error` validates Name/TypeName non-empty and DB non-nil, then INSERT OR IGNORE into smeldr_state_flows + SELECT id (last_insert_rowid() returns 0 on an ignored insert), then INSERT OR IGNORE for each State into smeldr_states and each Transition into smeldr_transitions. Calls `validateFlowItems` before returning. `validateFlowItems` probes sqlite_master (returns nil for non-SQLite), derives table name via camelToSnake + "s" (e.g. "AgentJob" → "agent_jobs"), checks table existence, then SELECT DISTINCT status NOT IN (placeholders) from that table — returns error naming unknown states if any exist. 12 tests in `state_test.go` cover happy path, idempotency, unknown-state error, nil DB, empty Name/TypeName, RequiredRole storage, valid-states no-error, and all exec/query error paths. Coverage: 96.0%.

---

## A176 — T23 Step 3: Transition Validation (v1.44.1, 2026-06-29)

**Date:** 2026-06-29
**Status:** Agreed
**Level:** 1

`validateTransition(ctx context.Context, db DB, typeName, fromStatus, toStatus string) error` added (unexported) to `state.go`. Algorithm: nil db → nil; sqlite_master probe fails → nil (non-SQLite, same guard as validateFlowItems); fromStatus==toStatus → nil (identity transition — preserves MCP idempotency); SELECT flow by type_name → fallback to type_name IS NULL AND name='default' → nil if neither found (no flow registered); SELECT COUNT(*) FROM smeldr_transitions WHERE flow_id=? AND from_state=? AND to_state=? → ErrConflict (409) when count=0; count query error → nil (fail open).

`DynamicTypeRepo.SetStatus` (dynamic.go): calls `validateTransition` after `GetByID` — one DB read total (GetByID was already required to read PublishedAt). `newSetStatusHandler`: hardcoded enum switch removed; empty status → 400 ErrBadRequest; ErrConflict from SetStatus → WriteError passes wrapped error via errors.Is chain → 409 response.

`Module[T]` (module.go): unexported `db DB` field + `setDB(DB)` method (same wiring pattern as `secret`/`setSecret`). `MCPPublish`, `MCPArchive`, `MCPSchedule` each call `validateTransition(ctx, m.db, m.contentTypeName, string(prevStatus), string(targetStatus))` before `setNodeStatus`. When db is nil (no DB configured) validation is silently skipped. `smeldr.go` `App.Content`: type-assertion wire `if dbs, ok := r.(interface{ setDB(DB) }); ok { dbs.setDB(a.cfg.DB) }` alongside the existing `setSecret` wire.

12 new tests in `state_test.go`: 8 `TestValidateTransition_*` unit tests (nilDB, nonSQLite, identity, customFlow_valid, customFlow_invalid, defaultFlow_valid, defaultFlow_invalid, noFlow) + 1 `TestValidateTransition_countQueryError` (mock fail-open) + 3 `TestMCPPublish/Archive/Schedule_invalidTransition` integration tests (real SQLite with restricted flow). 1 updated test in `dynamic_app_test.go`: `TestAdminSetStatus_InvalidStatus` now uses sub-tests — unknown status → 409 (was 400); empty status → 400. Coverage: 96.0%.

---

## A177 — T23 Step 4: State Flow MCP Tools (mcp v1.24.0, 2026-06-29)

**Date:** 2026-06-29
**Status:** Agreed
**Level:** 1

Add three MCP tools in new file `state_tools.go` in smeldr/mcp, all gated on `s.app.Config().DB != nil` (no new ServerOption):

- `transition_item(type_name, slug, to_state)` — Editor role. Calls `DynamicTypeRepo.GetBySlug` to verify item exists, then `SetStatus` which invokes `validateTransition` internally. Returns -32001 (ErrConflict) when the transition is not permitted by the registered flow.
- `get_valid_transitions(type_name, slug)` — Author role. Queries `smeldr_state_flows` (custom flow for type, falling back to default flow) and `smeldr_transitions WHERE from_state = currentStatus`. Returns `{current_state, valid_transitions: []}`.
- `list_items_by_state(type_name, state)` — Author role. Calls `DynamicContentRepo.List` with status filter. Returns `{type_name, state, items, count}`.

`errorFor` in tool.go extended: `errors.Is(err, smeldr.ErrConflict)` → `-32001` with the error message (was falling through to -32603 "internal error"). This also correctly surfaces transition conflicts from the pre-existing `set_content_status` tool.

`go.mod`: `smeldr.dev/core` v1.43.1 → v1.44.1. 25 tests in `state_tools_test.go`. Coverage: 96.0%.

---

## A178 — T23 Step 5: define_state_flow MCP Tool (mcp v1.24.1, 2026-06-29)

**Date:** 2026-06-29
**Status:** Agreed
**Level:** 1

Add `define_state_flow(name, type_name, states, transitions)` to `state_tools.go` in smeldr/mcp. Admin role. Calls `s.app.RegisterFlow(smeldr.StateFlow{...})` with the parameters provided; idempotent (INSERT OR IGNORE). Returns `{name, type_name, state_count, transition_count}`. Gated on `s.app.Config().DB != nil` (same gate as all state tools).

`type_name` is required because `RegisterFlow` validates it as non-empty. The default flow (type_name IS NULL in smeldr_state_flows) can only be seeded at App startup; this tool registers custom flows for specific dynamic content types.

Three unexported helpers added to `state_tools.go`: `parseStates([]any) ([]smeldr.State, *jsonRPCError)`, `parseTransitions([]any) ([]smeldr.Transition, *jsonRPCError)`, `boolField(map[string]any, string) bool`. Each returns -32602 on malformed input.

`handleToolsCall` state tool role dispatch extended from `if p.Name == "transition_item"` (two-tier) to a `switch` with three cases: `define_state_flow` → `authoriseAdmin`, `transition_item` → `authoriseEditor`, `default` → `authorise`.

11 new tests in `state_tools_test.go`. `TestStateTool_ToolsList_DBSet` and `TestIsStateTool` updated to include `define_state_flow`. Coverage: 96.1%.

---

## A179 — T23 Step 6: suppressesSignals hook gate (v1.44.2, 2026-06-30)

**Date:** 2026-06-30
**Status:** Agreed
**Level:** 1

`suppressesSignals(ctx context.Context, db DB, typeName, statusName string) bool` added (unexported) to `state.go`. Algorithm mirrors `validateTransition`: nil db → false; sqlite_master probe fails → false (non-SQLite, same fail-open guard); SELECT flow by type_name → fallback to type_name IS NULL AND name='default' → false if neither found (no flow registered); SELECT suppresses_signals FROM smeldr_states WHERE flow_id=? AND name=? → scan error → false (fail-open). Returns the stored bool.

`notifyAfter` in `module.go` gains an early-return guard: `if suppressesSignals(ctx, m.db, m.contentTypeName, string(nodeStatusOf(item))) { return }` inserted before `snap := snapshotItem(item)`. When an item's current state has `suppresses_signals=true` in the registered flow, all After* signals (AfterPublish, AfterArchive, AfterSchedule, AfterUpdate, AfterCreate, AfterDelete, AfterUnpublish, AfterRelationCascade) and the afterHook are suppressed — the function returns immediately. Items without a custom flow use default flow rules; nil DB evaluates to false (signals fire).

9 new tests in `state_test.go`. 7 unit tests for `suppressesSignals`: `TestSuppressesSignals_nilDB`, `TestSuppressesSignals_nonSQLite`, `TestSuppressesSignals_noFlow`, `TestSuppressesSignals_falseWhenNotSet`, `TestSuppressesSignals_trueWhenSet`, `TestSuppressesSignals_defaultFlowFallback`, `TestSuppressesSignals_scanError`. 2 integration tests for `notifyAfter` suppression: `TestNotifyAfter_suppressedState_hooksSkipped` (registers custom flow with suppresses_signals=true, fires signal, verifies afterHook was not called), `TestNotifyAfter_unsuppressedState_hooksFire` (same flow, state=false for draft, verifies hook fires). Coverage: 96.0%.

---

## A180 — T23 Step 7: fireAsyncTriggers dispatch in state.go + SetStatus wire (dynamic.go) (v1.44.3, 2026-06-30)

**Date:** 2026-06-30
**Status:** Agreed
**Level:** 1

`fireAsyncTriggers(ctx context.Context, db DB, typeName, fromState, toState string)` added (unexported) to `state.go`. Algorithm: nil db → return; sqlite_master probe fails → return (non-SQLite, fail-open guard); one JOIN query on `smeldr_transition_triggers`, `smeldr_transitions`, `smeldr_state_flows` for `trigger_class='async'` AND matching from_state/to_state AND `(f.type_name=? OR (f.type_name IS NULL AND f.name='default'))`; scan error → return (fail-open, slog.Warn); rows.Err → return (fail-open, slog.Warn); for each matched trigger row, dispatch goroutine with panic recovery (slog.Error on panic); unknown trigger_type → slog.Warn (concrete handlers deferred to Steps 10+). All error paths are fail-open — the transition in SetStatus always succeeds.

Design note: one JOIN query (not two sequential queries like validateTransition) because the result is a collection, not a scalar. The OR in the WHERE clause resolves custom-flow vs default-flow in the same round-trip.

`DynamicTypeRepo.SetStatus` (dynamic.go) tail changed from `return err` to: check `err != nil` → return; call `fireAsyncTriggers(ctx, r.db, r.typeName, string(node.Status), string(status))`; return nil. `node.Status` is the FROM state captured by `GetByID` at the top of `SetStatus`.

10 new tests in `state_test.go`: `TestFireAsyncTriggers_nilDB`, `TestFireAsyncTriggers_nonSQLite`, `TestFireAsyncTriggers_noTriggers`, `TestFireAsyncTriggers_syncTrigger_skipped`, `TestFireAsyncTriggers_asyncTrigger_dispatched`, `TestFireAsyncTriggers_queryError`, `TestFireAsyncTriggers_scanError` (driver mock: 1-column rows, scan expects 2 → scan error path), `TestFireAsyncTriggers_rowsError` (driver mock: Next() returns non-EOF error → rows.Err() path), `TestSetStatus_firesAsyncTrigger`. Coverage: 96.0%.

---

## A181 — T23 Step 8: AgentJob state flow registration in smeldr/agent (v0.6.1, 2026-06-30)

**Date:** 2026-06-30
**Status:** Agreed
**Level:** 1

`Module.Register` in `flow/module.go` (`smeldr.dev/agent`) gains an `app.RegisterFlow` call at the start of the function, before `app.Content(m.mod)`. The flow registered is `"agent-job"` for `TypeName: "AgentJob"` with four states — `draft` (initial), `published`, `paused` (`SuppressesSignals: true`), `archived` (terminal) — and five transitions: draft→published, published↔paused, published→archived, paused→archived. `SuppressesSignals: true` on `paused` means that After* hooks are suppressed while an AgentJob is paused, preserving its position without restarting it.

`RegisterFlow` is fail-open on nil DB and non-SQLite (returns nil silently). A genuine error (e.g., duplicate name conflict) is logged via `slog.Error("smeldr-agent: RegisterFlow failed", "error", err)` and does not block startup. No new tests required — `RegisterFlow` is fully tested in smeldr.dev/core; existing `flow/` tests remain green with the new call (nil-DB path is no-op).

`go.mod`: `smeldr.dev/core v1.26.0 → v1.44.3`. `go` directive: `1.26.3 → 1.26.4` (required by core v1.44.3).

---

## A182 — T23 Step 9: ScheduledPost delivery flow registration in smeldr/social (v0.9.1, 2026-06-30)

**Date:** 2026-06-30
**Status:** Agreed
**Level:** 1

`Social.Register` in `social.go` (`smeldr.dev/social`) gains an `app.RegisterFlow` call at the start of the function, before `app.Handle(...)` route registrations. `log/slog` import added (was absent). The flow registered is `"scheduled-post"` for `TypeName: "ScheduledPost"` with seven states — `draft` (initial), `scheduled`, `queued`, `delivered` (terminal), `partial`, `failed`, `archived` (terminal) — and ten transitions: draft→scheduled, scheduled→queued, queued→delivered/partial/failed, partial→queued (retry), failed→queued (retry), delivered/partial/failed→archived.

`RegisterFlow` is fail-open on nil DB and non-SQLite (returns nil silently). A genuine error is logged via `slog.Error("smeldr-social: RegisterFlow failed", "error", err)` and does not block startup. No new tests required — `RegisterFlow` is fully tested in smeldr.dev/core; all existing social tests remain green.

`go.mod`: `smeldr.dev/core v1.26.0 → v1.44.3`. `go` directive: `1.26.3 → 1.26.4` (required by core v1.44.3).

---

## A183 - T23 Step 10: LifecycleEvent rename + orchestration types (core v1.45.0, 2026-06-30)

**Date:** 2026-06-30
**Status:** Agreed
**Level:** 2 (exported type rename, new exported types and functions, cross-repo change)

`type Signal string` in `signals.go` renamed to `type LifecycleEvent string`. The eleven constants (BeforeCreate, AfterCreate, BeforeUpdate, AfterUpdate, BeforeDelete, AfterDelete, AfterPublish, AfterUnpublish, AfterArchive, AfterSchedule, SitemapRegenerate, AfterRelationCascade) keep their names - only the type annotation changes. All core function signatures updated: `On[T]`, `OnSignal`, `AddSignalListener`, `notifyAfter`, `setAfterHook`, `dispatchBus`, `emitSignal`, `signalToEventSuffix`, `buildEventName`, `buildWebhookPayload`, `webhookDispatch`. `AuditRecord.Signal` field type changed to `LifecycleEvent`. Frees the name `Signal` for orchestration use.

New file `orchestration.go` (package smeldr) adds four content types: `Signal` (protocol message: sender, receiver, signal_type, message, task_ref, sequence), `Task` (work item: task_id, priority, band, size, description, note_ref), `Decision` (ratified decision: decision_number, scope, body, next_eval_at, eval_note), `Amendment` (changeset: amendment_number, amendment_type, version, commit_hash, pilot, summary). All embed `Node`.

`CreateOrchestrationTables(db DB) error` creates tables smeldr_signals, smeldr_tasks, smeldr_decisions, smeldr_amendments. `RegisterOrchestrationTypes(app *App, db DB)` registers all four types with custom state flows and MCP(MCPRead, MCPWrite); fail-open on nil DB. Four private flow helpers: orchSignalFlow (4 states), orchTaskFlow (9 states), orchDecisionFlow (5 states), orchAmendmentFlow (6 states).

Dependents: `smeldr/agent v0.6.2` (flow/module.go, flow/agent_job.go, flow/agent_job_test.go: smeldr.Signal -> smeldr.LifecycleEvent; core dep v1.44.3 -> v1.45.0). `smeldr/social v0.9.2` (route.go, router.go, export_test.go: same rename; core dep -> v1.45.0). `smeldr/mcp v1.24.2` (mcp.go: same rename; core dep v1.44.1 -> v1.45.0).

All tests pass. Coverage: core 96.0%.

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
