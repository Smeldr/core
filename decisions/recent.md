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
Archived 2026-07-05: A191–A193 → phase13-archive.md
Archived 2026-07-05: A194–A200 → phase14-archive.md
Archived 2026-07-10: A201–A209 → phase15-archive.md
Archived 2026-07-24: A210–A214 → phase16-archive.md
Archived 2026-07-28: A215–A219 → phase17-archive.md
Archived 2026-07-29: A220-A221 → phase18-archive.md
Archived 2026-07-29: A222-A224 → phase19-archive.md
Archived 2026-07-30: A225-A226 → phase20-archive.md
Archived 2026-08-08: A227-A233, D33-D37, A234-A235 → phase21-archive.md
Archived 2026-08-09: A236-A240, D38-D39 → phase22-archive.md
Archived 2026-08-09: D40-D42, A241 → phase23-archive.md
Archived 2026-08-10: D43-D46, A242-A247 → phase24-archive.md
Archived 2026-08-11: D47, A248-A251 → phase25-archive.md
Archived 2026-08-13: D48-D49, A252-A255 → phase26-archive.md
Archived 2026-08-15: D50-D56, A256-A257 → phase27-archive.md
Archived 2026-08-16: A258-A260, D57-D58 → phase28-archive.md
---

## A261 — Task/Goal gain a "resolved" terminal state (D58, T255)

### What was wrong

Two orchestration flows had no honest way to close on "the underlying
need was met, but not by this item's own tracked work" — see D58 for
the full argument (same session, same commit). `orchTaskFlow`'s
`plan-reviewing` had exactly one legal exit; `orchGoalFlow`'s `parked`
claimed `IsTerminal: true` while holding an outbound edge back to
`open`.

### The fix

`orchestration.go`:

- `Task` gains `{Name: "resolved", IsTerminal: true}` plus three new
  transitions, each `RequiredReason: true`:
  `active→resolved`, `waiting-plan→resolved`, `plan-reviewing→resolved`.
  Deliberately excluded: `implementing`/`commit-reviewing` (a build
  already in flight means there was something to build) and `blocked`
  (a stall, not a conclusion).
- `Goal.parked` loses its `IsTerminal: true` flag (honesty fix only —
  its behaviour, including the existing `parked→open` transition, is
  unchanged). `Goal` gains `{Name: "resolved", IsTerminal: true}` plus
  three new transitions, each `RequiredReason: true`:
  `open→resolved`, `in-progress→resolved`, `parked→resolved`.

`state.go`: `IsTerminal`'s doc comment corrected per D58 — describes
"no outbound transition to a non-terminal state" rather than "no
outbound transitions," matching what the codebase (`Decision`'s own
`superseded→archived`) already relies on.

`RequiredReason` verified against `validateTransition`
(`state.go:363-446`) before use: the gate is unconditional, keyed only
on the transition's own `RequiredReason` flag and whether a caller
supplied a reason — independent of type `Kind`, so it applies
identically to `Task`/`Goal` (compiled types, routed through
`App.TransitionItemWithReason`, `state.go:797-863`) as it already does
for dynamic content. Confirmed via direct read before implementing,
not assumed; this is the first orchestration flow to use
`RequiredReason`.

### Docs

`docs/ARCHITECTURE.md`'s `orchestration.go` package-structure entry
gains a paragraph. `docs/FEATURELIST.md`'s `Goal` content-type
description was already wrong independent of this task (listed
`deferred`, a `Task` state, as one of `Goal`'s own outcomes, and
omitted `parked→open`'s own return edge) — corrected in the same
commit since it documents the exact flow this task touches.

### Tests

7 new: `TestTaskFlow_ResolvedReachableFromThreeStates`,
`TestTaskFlow_ResolvedIsTerminal`,
`TestGoalFlow_ParkedNoLongerTerminal` (explicit regression pin, not
just an updated count),
`TestGoalFlow_ResolvedReachableFromThreeStates`,
`TestGoalFlow_ResolvedIsTerminal`. Two existing tests updated for the
real new counts: `TestTaskFlow_definition` (9→10 states, 9→12
transitions), `TestGoalFlow_definition` (`wantStates`/`wantTerminals`/
transition count). `TestRegisterOrchestrationTypes_flows` re-read and
confirmed unaffected — it counts registered flows only, not states or
transitions per flow.

### Versioning

No new exported Go symbol (`orchTaskFlow`/`orchGoalFlow` stay
unexported; `State`/`Transition`/`StateFlow` types unchanged) but real
consumer-observable behaviour change via `transition_item`/
`get_valid_transitions`. Patch bump, matching A247's/A253's own
precedent for a behaviour fix with no new exported symbol. Coverage:
96.3%. `go test -race ./...` clean. Level 2 amendment.

---

## A262 — Task's own human identifier gains a lookup path (T253)

### What was wrong

`get_task`/`update_task`/`transition_item`/`publish`/`archive`/
`delete_task` all resolve their caller-supplied identifier via
`Repository.FindBySlug` — matching `Node.Slug` only. A Task's own
human-facing identifier (`TaskID`, e.g. `"T203"` — the identifier used
in nearly every conversation about this project's own work) had no
lookup path anywhere; the same gap applies to `Goal.GoalID`,
`Decision.DecisionNumber`, `Amendment.AmendmentNumber`. A caller given
"check on T253" had no direct way to look it up — only `list_tasks()`
and a manual scan, which this project's own sessions had to do
repeatedly.

### The fix: an optional `Repository[T]` extension, not a breaking change

New `ColumnLookupRepository[T]` interface (`storage.go`):
`FindByColumn(ctx, column, value) (T, error)`. Declared as an
**optional extension**, type-asserted by callers — the exact shape
`SeqRepository[T]` already established — rather than widening the
exported `Repository[T]` interface itself, which would break any
custom repo implementer that predates this task (the same class of
break D49 already ruled out for `MCPModule`, and D53 governs
explicitly). Implemented by both `SQLRepo[T]` (raw parameterized SQL,
`column` never caller-supplied) and `MemoryRepo[T]` (reflection via
new unexported `fieldNameForColumn`, which resolves a db column name
to its Go field name via the same struct-tag-driven `dbFields` cache
`SQLRepo.columnForField` already uses in reverse — no per-type
translation table needed in `storage.go` at all).

**Which types get a fallback: an explicit map, not reflection-based
auto-detection.** New `humanIDColumns` (`orchestration.go`): `Task` →
`task_id`, `Goal` → `goal_id`, `Decision` → `decision_number`,
`Amendment` → `amendment_number`. `Signal` has no entry — no canonical
sequential identifier exists on it. Explicit, matching
`anchorTypeTable`'s own shape (`context_packet.go`) — a future field
merely named `...Number` must never silently become a lookup key
nobody intended.

**Two independent call sites, not one shared abstraction.** New
`Module.resolveItem` (`module.go`, unexported): tries `FindBySlug`
first (the common case, costs nothing extra), falls back to
`FindByColumn` only on a slug miss for a type with a `humanIDColumns`
entry. All six MCP methods (`MCPGet`/`MCPUpdate`/`MCPPublish`/
`MCPSchedule`/`MCPArchive`/`MCPDelete`) now call it instead of
`m.repo.FindBySlug` directly. `App.TransitionItem`/
`TransitionItemWithReason`'s own compiled-type path (`state.go`) never
went through `Module[T]` at all — a raw `SELECT ... WHERE slug = $1`
against `resolveItemTable`'s result — so it gets the identical
fallback expressed as a second raw-SQL attempt on `sql.ErrNoRows`,
using the same `humanIDColumns` map. The two paths were already
structurally independent before this task; a shared abstraction across
them would be more machinery than a four-entry map justifies.

**Deliberately not touched.** HTTP handlers
(`aiDocHandler`/`showHandler`/`updateHandler`/`deleteHandler`/
`findAndServe`/`findAndServeAIDoc`) resolve `r.PathValue("slug")` from
a URL path — a URL path segment is a slug by REST convention, and a
caller who already has a URL already has the slug; there is no "typed
a human ID into a browser bar" scenario this needs to solve.
`createHandler`'s own `FindBySlug` call is a slug-collision probe
while generating a new slug, unrelated to identifier resolution.

### Two real bugs caught while wiring this up, not part of the original ask

**`MCPUpdate` was about to overwrite a Task's real slug with the
caller's ident.** Its identity-restore step did
`pv.Elem().FieldByIndex(f.slug).SetString(slug)` using the raw
function parameter — correct when `slug` genuinely was the slug, but
once `resolveItem` can also return a match via `TaskID`, that same
parameter can hold `"T203"` instead. Fixed to restore the item's own
real slug via the already-existing `nodeSlugOf(existing)` helper,
never the caller-supplied ident.

**`MCPPublish`'s `checkSlugCollision` had the identical shape** — it
checked the raw ident for a collision rather than the item's own real
slug, which would have checked the wrong string entirely when
resolved by human ID. Fixed the same way, via `nodeSlugOf(item)`.

**`TransitionItem`'s own response echoed the raw ident back as
`"slug"`.** Fixed by fetching the real `slug` column in the same
`SELECT` (already reading `id`/`status`) and returning that instead of
the function parameter — same class of bug as `MCPUpdate`'s, same
fix shape.

### Verified before implementing, not assumed

**`get_valid_transitions`/`list_items_by_state` (mcp) coverage,
checked directly against `smeldr.dev/mcp`'s own source, not inferred
from precedent alone.** `itemCurrentStatus`
(`mcp/state_tools.go:283-310`) calls `m.MCPGet(ctx, slug)` for compiled
types — covered transitively by the `MCPGet` fix, confirmed by reading
the actual call, not assumed from A248/A249's own precedent.
`itemsByState` (`mcp/state_tools.go:317+`) never resolves a single
identifier at all — it lists every item in a given `state`, no slug/ID
resolution happens there, so it was never a candidate.

### Tests

New: `TestSQLRepo_FindByColumn_query`, `TestMemoryRepo_FindByColumn`
(+ `_notFound`, `_unknownColumn`), `TestResolveItem_BySlug`,
`TestResolveItem_ByHumanID`, `TestResolveItem_NoMatch`,
`TestResolveItem_NonOrchestrationType_NoFallback`,
`TestResolveItem_NonErrNotFoundPropagates`,
`TestResolveItem_RepoLacksColumnLookup` (the fail-closed branch: a
type with a `humanIDColumns` entry whose repo doesn't implement
`ColumnLookupRepository`), `TestModule_MCPGet_ByHumanID`,
`TestModule_MCPUpdate_ByHumanID_PreservesRealSlug` (the regression pin
for the bug above), `TestModule_MCPPublish_ByHumanID`,
`TestApp_TransitionItem_Compiled_ByHumanID` (+
`_StillResolvesBySlug`). One existing test's own mock fixture
(`TestApp_TransitionItem_SelectQueryFails`) updated for the new SQL
query text (`"id, status FROM"` → `"id, slug, status FROM"`) — the
query itself changed to also fetch the real slug, not a behaviour
regression.

### Versioning

New exported symbol (`ColumnLookupRepository[T]`). No existing
exported signature changes. MINOR bump. Coverage: 96.3%;
`resolveItem` 100%. `go test -race ./...` clean. Level 2 amendment.

---

## A263 — Webhook event coverage for orchestration Signals (T231)

### Problem

`App.TransitionItemWithReason` (`state.go:801`) called `fireAsyncTriggers`
and nothing else — no webhook fired on any orchestration-type (`Task`,
`Decision`, `Amendment`, `Goal`, `Signal`) state transition, so no agent or
architect could learn a state changed except by asking. The same gap
existed in `recordAuthorizationRequiredSignal` (`state.go:966`), the
D42-class Signal insert recorded when an automated transition hits a
role-gated boundary — a raw `INSERT` with no dispatch.

**Priority raised 2026-08-14** — the desktop doorbell was assumed to cover
interactive delivery, leaving this task for headless automation only. That
premise did not survive the day: the doorbell's content did not reach brand
at all, twice; both pings coincided with the receiving session hanging,
five and ten minutes; and most directly, the doorbell tells the architect
nothing — it is a thing architect sends, not a thing architect receives.
T211's approval sitting unseen in the plan file for two days, with neither
side aware, was the measured cost this closes.

### Mechanism, argued not assumed

The task's own text warned against assuming the existing `App.OnSignal` bus
was the right delivery path just because webhooks already hang off it.
Investigated at source before designing:

`App.OnSignal`/`dispatchBus` (`smeldr.go:1101-1133`) is keyed on
`LifecycleEvent`, a fixed, closed vocabulary — `AfterCreate`/`AfterUpdate`/
`AfterPublish`/`AfterUnpublish`/`AfterArchive`/`AfterSchedule`/`AfterDelete`
(`signals.go:19-56`). `webhookDispatch`'s own `signalToEventSuffix`
(`webhook.go:253-272`) is a `switch` over exactly those seven constants.
Orchestration compiled types run on `StateFlow`-declared, per-type,
arbitrary-named states (`backlog`, `waiting-plan`, `plan-reviewing`,
`commit-reviewing`, `resolved`, …) with no relationship to Draft/Published/
Archived at all — there is no `AfterX` constant a `waiting-plan →
plan-reviewing` transition could map to, and states are declared per-flow
via `define_state_flow` (itself an MCP tool), not a fixed enum that a core
release could keep pace with.

Second, independent mismatch: `webhookDispatch`'s payload builder,
`buildWebhookPayload` (`webhook.go:290`), requires a typed Go item
(`extractNode(item)`). `TransitionItemWithReason` never loads one — it
operates entirely at the raw-SQL layer (`resolveItemTable` + `id`/`slug`/
`status` columns by name) because it handles both compiled and
dynamic-content types generically without importing every registered Go
struct. `recordAuthorizationRequiredSignal` is the same — a raw `INSERT`,
no Go value in hand.

**Conclusion: a dedicated dispatch beside `fireAsyncTriggers`**, not a bus
emit — both mismatches are structural (closed vocabulary; typed-item
requirement), verified against source rather than assumed from "webhooks
already work that way."

### Scope boundary against A258/D42, checked not relitigated

`DrainEvalQueue`'s own successful automated-transition branch
(`state.go`) applies a raw `UPDATE` directly — it does not call
`TransitionItemWithReason` — and A258 already ruled that path
provenance-only: firing publish-class signals for an automated transition
would activate every human-publish subscriber with no operator decision
behind it. This Amendment does not touch that branch or reopen that
ruling. What it does wire is `recordAuthorizationRequiredSignal`'s own
Signal-creation path — different in kind, since a Signal exists
specifically to be seen and acted on by a human, so leaving it silent on
the delivery side undermines D42's own purpose. Confirmed by grep: it is
the only `INSERT INTO smeldr_signals` outside the normal `Module[T]`
create path.

### What stays out of scope, named not silently dropped

`DynamicTypeRepo.setStatus` (`dynamic.go:228`) has the identical gap —
`fireAsyncTriggers` only, no webhook — for runtime-defined content types'
own custom state flows. `DynamicTypeRepo` holds no `*WebhookStore`/pool
reference today (only `db`, `rs`, `typeName`), and wiring that through is
separate, real work this task's own title ("webhook event coverage for
orchestration Signals") did not ask for.

### Implementation

`webhook.go`: the endpoint-lookup + enqueue tail extracted out of
`webhookDispatch` into a new shared `enqueueWebhookEvent(ctx, store, pool,
eventName, payload []byte)` — no behaviour change to `webhookDispatch`
itself, pure extraction, its own existing tests pass unchanged. New
`transitionWebhookData` struct (`Type`, `ID`, `Slug`, `FromState`,
`ToState`, `Reason`) and `dispatchTransitionWebhook(ctx, store, pool,
eventName string, data transitionWebhookData)` — nil-safe no-op when
`store`/`pool` are nil, matching `App.Webhooks` being opt-in.

`state.go`: `TransitionItemWithReason` calls `dispatchTransitionWebhook`
immediately after `fireAsyncTriggers`, event name `strings.ToLower(typeName)
+ ".transitioned"` (e.g. `task.transitioned`), carrying `from_state`/
`to_state`/`reason`. `recordAuthorizationRequiredSignal` gains two new
unexported params, `store *WebhookStore, pool *workerPool` (its one call
site, in `DrainEvalQueue`, already holds `a.webhookStore`/`a.webhookPool`)
— fires `"signal.created"` right after its own successful `INSERT`, the
exact event name a human-created Signal already produces via the normal
create path, so a D42-triggered Signal is indistinguishable from a
human-created one to a webhook subscriber.

`orchestration.go`: folded in per D50 same cycle — `orchTaskFlow`'s
`Name: "architect-task"` → `"agent-task"`, generic behaviour under a
role-specific name was the layering smell.

### Docs

`docs/REFERENCE.md`: new "Orchestration transition events (T231)" section
documenting the `"{type}.transitioned"` convention and `signal.created`
reuse; `agent-task (9 states)` rename (state count unchanged by D58).
`docs/FEATURELIST.md`: `agent-task flow` rename. `docs/ARCHITECTURE.md`:
`state.go` package-map entry extended. `AGENTS.md`: webhook-rules bullet
added naming the new event convention for AI assistants wiring
`create_webhook`.

### Tests

New in `webhook_test.go`: `TestEnqueueWebhookEvent_success`,
`_lookupError`, `_enqueueError`; `TestDispatchTransitionWebhook_nilStore`,
`_nilPool`, `_success`. New in `state_transition_item_test.go`:
`TestApp_TransitionItemWithReason_FiresTransitionWebhook`,
`_NoWebhookStore`, `TestDrainEvalQueue_AuthorizationRequiredSignal_FiresWebhook`
(all through the real entry points, not the dispatch functions directly).
Two existing tests (`TestRecordAuthorizationRequiredSignal_Success`,
`_InsertError`) updated for the new signature. One existing test
(`TestTaskFlow_definition`) updated for the renamed flow name.

### Versioning

No exported Go symbol added or changed — every new identifier
(`enqueueWebhookEvent`, `transitionWebhookData`, `dispatchTransitionWebhook`)
is unexported, and `recordAuthorizationRequiredSignal`'s signature change
is internal (its one call site updated in the same commit). New webhook
event types becoming deliverable is a real, consumer-observable capability
for anyone with `App.Webhooks` wired, opt-in (an operator must subscribe
an endpoint to the new event names). MINOR bump. Coverage: 96.2%.
`go test -race ./...` clean. Level 2 amendment.

---

## A264 — EnsureColumn, a general schema-migration path (T246)

### Problem

No schema migration mechanism existed. Every `Create*Table` function
(`CreateAuditTable`, `CreateBlockTables`, `CreateSiteConfigTable`, …) uses
`CREATE TABLE IF NOT EXISTS`, which does nothing to a table that already
exists in an older shape — confirmed directly in source, `blocks.go:68`'s
own comment already said so. Cited incident: `CreateSiteConfigTable` (core
v1.58.4) shipped missing columns `SQLRepo.Save` requires, hand-patched
twice in two downstream repos with no framework fix produced either time.
Named as a real risk from M6 onward — a pilot instance upgrading to a core
release with a changed table has no path that doesn't involve someone
opening a SQL client on their own box.

### The mechanism already existed, four times, unnamed

Grepped every `Create*Table`/`migrate*` function before designing
anything. Four independently implement the identical idiom by hand:
`MigrateNodeRevColumn` (`storage.go`, A158, exported, parameterized on
table, hardcoded to column `rev`), `migrateTransitionReasonColumn` (A220),
`migrateTransitionStrictColumn` (A234), `migrateStateFlowConflictColumns`
(A186) — all four: `PRAGMA table_info(table)`, scan for the column,
`ALTER TABLE … ADD COLUMN` if absent, no-op on non-SQLite (`PRAGMA`
unsupported — an existing, already-accepted boundary, unchanged here).
Additive only, none ever drops a column.

**Directly answers the "argue against the no-build-pipeline principle"
requirement.** The conventional answer — an ordered migration-file list
with a version table — is foreign to this codebase: no `migrations/`
directory, no `schema_version` table, no codegen step exists anywhere in
the project today. Importing one now would be new machinery for a problem
this codebase has already been solving correctly by hand, four times.
**Rejected in favour of naming and centralizing the idiom that already
works**, not replacing it — the task's own explicit instruction not to
"bundle a rewrite of `Create*Table`'s existing behaviour."

### The mechanism

New exported `EnsureColumn(ctx context.Context, db DB, table, column,
columnDDL string) error` (`migrate.go`). Idempotent, additive-only,
SQLite-only (matching every function it generalizes). The four existing
functions became thin wrappers over it — behaviour-preserving; every one
of their own existing tests passes unmodified, confirmed before writing
anything new.

### Answering the task's four required questions

1. **Mechanism** — `EnsureColumn`, argued above against the ordered-list
   alternative with direct evidence (four working hand-written precedents
   already in the codebase), not a general appeal to principle.
2. **Ownership** — whoever declares the field calls `EnsureColumn` for it,
   at their own startup, the same way `Create*Table` already works today.
   No central registry. An application extending a framework-provided
   table (the `SiteConfig` incident's own shape) calls `EnsureColumn`
   itself instead of hand-writing `ALTER TABLE` twice in two repos.
3. **Downgrade** — explicitly unsupported, stated in `EnsureColumn`'s own
   doc comment: additive-only, so a downgrade leaves one unused column
   present, never lost data, never a broken older schema.
4. **Startup detection** — `EnsureColumn` runs and fixes eagerly wherever
   it is called, always at application startup (matching every existing
   call site's own established timing), never deferred to first request.
   A real `ALTER` failure surfaces as a real Go `error` from the startup
   call chain. No separate detect-only mode: an additive `ALTER COLUMN`
   is safe and idempotent, so eager auto-fix strictly dominates
   eager-detect-then-manual-fix for this class of change. A framework
   mechanism cannot detect a third party's own undeclared schema
   requirement it was never told about — that remains the caller's own
   responsibility per the ownership answer, the same as every other
   opt-in Smeldr subsystem.

### Live bug found during investigation, not assumed — the flagship fix

Re-read `CreateSiteConfigTable`'s current DDL directly rather than
trusting the task's own "already caused hand-patching twice" framing as
historical only. Its `CREATE TABLE` text declared no `scheduled_at` and no
`rev` column; `SiteConfig` embeds `Node`, which declares both
unconditionally (`ScheduledAt *time.Time db:"scheduled_at"`, `Rev int
db:"rev"`) — `SQLRepo.Save`'s `dbFields` reflection expects both
regardless.

**Reproduced live, not hypothetical:** a throwaway test —
`CreateSiteConfigTable(db)` then `Save` on a *freshly created* table —
failed immediately: `no such column: scheduled_at`. Broader than the
task's own framing ("an instance that already has the table"): a
brand-new instance was broken too, not only an upgrading one.
`site_config_test.go`'s two existing tests never called `.Save()`, which
is why this shipped unnoticed since v1.58.4.

Direct fix-shape precedent found in `docs/ARCHITECTURE.md`'s own history:
**A221** hit the identical bug class (`required_reason` added only via
the SQLite-only migration, never in the `CREATE TABLE` text, so a fresh
Postgres install never got it — caught by CI's pgx integration job). Same
fix here: declare both columns in the `CREATE TABLE` text directly (fixes
fresh installs) *and* call `EnsureColumn` for both right after (fixes
pre-existing installs that already ran the broken DDL).

### What stays out of scope, named not silently dropped

`DynamicTypeRepo.setStatus`'s own identical gap for runtime-defined
content types is not addressed — `DynamicTypeRepo` holds no
`*WebhookStore`-style reference to a migration mechanism today, and
wiring one through is separate work. Non-SQLite column migration remains
out of scope, matching the existing boundary every generalized function
already had. No other `Create*Table` function's own possible
missing-column bugs were investigated beyond the one the task's own text
names as the confirmed incident.

### Tests

4 new in `migrate_test.go` (`TestEnsureColumn_AddsColumn`, `_Idempotent`,
`_NonSQLite`, `_AlterFails`); 5 new in `site_config_test.go`
(`TestCreateSiteConfigTable_SaveSucceeds` — the regression pin for the
live bug — `_MigratesPreexistingTable`, `_CreateFails`,
`_ScheduledAtMigrationFails`, `_RevMigrationFails`, the last two using a
fixture recreating the pre-fix table shape since `EnsureColumn`'s own
`ALTER` branch is only reached when a column is genuinely missing).
Existing tests for all four retrofitted functions pass unmodified.

### Versioning

New exported symbol (`EnsureColumn`); `CreateSiteConfigTable` behaviour
fixed (a real bug, not a new capability, but consumer-observable —
`SiteConfig` now actually works). No existing exported signature changes.
MINOR bump, matching A262's own precedent (new exported symbol → MINOR).
Coverage: 96.3%; `CreateSiteConfigTable` 100%, `EnsureColumn` 88.2% (the
`rows.Scan`/`rows.Err` iteration-error branches are the same
structurally-hard-to-trigger class already accepted elsewhere in this
package, now consolidated into one place instead of duplicated
uncovered across four). `go test -race ./...` clean. Level 2 amendment.

---

## A265 — PATCH route for typed items over REST (T242)

### Problem

Dynamic-content REST (`PATCH /_content/{type}/{id}`) and typed MCP
(`MCPUpdate`) both already had real partial-update semantics. Typed
`Module[T]` REST only had `PUT` — full-replace, so any field absent from
the body silently takes its Go zero value. Found by Peter asking whether
he could ratify a Decision with `curl`: no `PATCH` route exists for a
typed item at all, only `PUT`.

### Design — reuse MCPUpdate's own logic, don't reimplement it

`MCPUpdate` already is the exact logic a `PATCH` route needs: partial
merge onto the existing item, identity/lifecycle restored after the
merge, validated, saved, `notifyAfter`'d. The new `patchHandler` decodes
the body as a `map[string]any` (matching the dynamic-content `PATCH`
handler's own simpler pattern — no typed struct to overflow the same way
`updateHandler`'s `MaxBytesError` handling guards against) and calls the
same merge logic `MCPUpdate` calls, gated by `checkWriteOp(ctx, "update",
m.writeRole)` — the identical call `updateHandler` already makes for
`PUT`, so `PATCH` and `PUT` require the same authority. Registered in
`Register` mirroring `PUT`'s own middleware-chain wrapping exactly.

**Role tier, the plan's own open question, settled by the architect:**
same as `PUT`, not a narrower tier. `PATCH` and `PUT` are the same
write-authority question with a different body shape; nothing in this
codebase's existing role model treats a partial update as inherently
lower-risk than a full replace.

**State-change question, answered by construction, not chosen freshly:**
since the new route goes through the same identity/lifecycle-restore
logic `MCPUpdate` already has, a `Status`/`ID`/`Slug` field in the `PATCH`
body is silently discarded — identical to `MCPUpdate`'s own existing
contract. `transition_item`/`PUT` remain the only ways to change state.

**Does the dynamic-content PATCH handler's shape generalise? No —
argued, not assumed.** `newUpdateContentHandler` operates on
`DynamicNode`'s JSON-blob storage via `DynamicTypeRepo.UpdateFields` — a
fundamentally different representation from `Module[T]`'s strongly-typed
Go structs reached via reflection. Sharing one handler would need a new
interface abstracting over both storage shapes for one call site's
benefit — not worth building when `MCPUpdate` already provides the
complete, tested typed-side logic with nothing to abstract over. Each
surface keeps its own handler; the *pattern* (decode a fields map,
restore identity/lifecycle, validate, save) is shared in spirit, not in
code, because the underlying merge mechanics genuinely differ.

### Real bug caught during implementation — not part of the approved plan's own text

The plan, as approved, said "the new route calls `MCPUpdate` directly."
Reading `MCPUpdate`'s own body closely before wiring it up surfaced a
real consequence the plan hadn't examined: `MCPUpdate`'s `notifyAfter`
call hardcodes `surfaceMCP` — every caller of `MCPUpdate`, regardless of
its own transport, gets recorded as MCP-originated. `updateHandler`'s own
`PUT` call site, by contrast, already correctly passes `surfaceHTTP` at
the equivalent point (`module.go:1960`), confirming this is a real
mismatch, not a hypothetical one: calling `MCPUpdate` directly from the
new `PATCH` route would have misreported every REST-originated partial
update as MCP-originated, in a project that has previously done real,
careful work threading `Surface` accurately through 14 separate call
sites (A260).

**Fix:** extracted `MCPUpdate`'s own body into a new unexported
`updateFields(ctx, slug, fields, surface string) (any, error)`,
parameterized on the calling surface. `MCPUpdate` becomes a one-line
wrapper passing `surfaceMCP` — behaviour-preserving, confirmed by running
its own full existing test suite unmodified before writing anything new.
The new `patchHandler` calls `updateFields` directly with `surfaceHTTP`,
bypassing `MCPUpdate` entirely. Verified with a dedicated test going
through the real HTTP handler end-to-end (wiring `App.Provenance`, a real
`OnSignal` subscription, and asserting the recorded `Surface` — not by
calling `notifyAfter` directly, which would only prove the plumbing
exists, not that the new route actually uses it correctly).

### Tests

7 new (6 planned + 1 for the Surface fix found during implementation):
`TestModule_PatchHandler_PartialUpdate_PreservesAbsentFields` (the direct
regression pin — an omitted field keeps its value, unlike `PUT`),
`_RestoresIdentityAndStatus`, `_Forbidden_WithoutWriteRole`,
`_BadRequest_InvalidJSON`, `_NotFound_UnknownSlug`,
`_SurfaceIsHTTP_NotMCP` (the fix above, end-to-end),
`TestModule_Register_MountsPatchRoute` (a route that compiles but never
mounts is the exact class of gap this task itself is). All existing
`MCPUpdate`/`MCPPublish`/`resolveItem` tests pass unmodified.

### Versioning

New route, no exported symbol added, no existing signature changed
(`patchHandler`/`updateFields` both unexported, matching
`updateHandler`/`createHandler`/`deleteHandler`'s own visibility). New
consumer-visible REST capability — a route that returned 404/405 now
responds. MINOR bump. Coverage: 96.3%; `patchHandler` 100%, `MCPUpdate`
100%, `updateFields` 96.3% (the `syncSaveHook` error branch is a
pre-existing gap carried over unchanged from `MCPUpdate`'s own prior
coverage, not introduced by this extraction). `go test -race ./...`
clean. Level 2 amendment.

---

## A266 — resolveItem gains a FindByID fallback (T214)

### Problem

`mcp/tool.go`'s `identArg` returns whichever of `"id"`/`"slug"` is present
in the caller's args, but every one of its six call sites (`update`,
`publish`, `schedule`, `archive`, `delete`, `get`) named the result `slug`
and passed it into a slug-resolution path — `"id"` was an alias for the
*key name* only, never for the *identifier type*. A real `Node.ID` passed
under `"id"` resolved nothing, despite `identArg`'s own doc comment
("accepting both id and slug") reading as genuine ID support.

### Investigation — the fix belongs in core, mcp needs zero changes

Traced all six `identArg` call sites directly. Every one already passes
its resolved string into a core `Module[T]` MCP method
(`MCPUpdate`/`MCPGet`/`MCPPublish`/`MCPSchedule`/`MCPArchive`/`MCPDelete`),
and all six already route through `Module.resolveItem` (T253) as their
single resolution funnel — which tries `FindBySlug`, then, only for a
type with a registered `humanIDColumns` entry, `FindByColumn`.
`resolveItem` never tried `FindByID` — the actual gap.

**`FindByID` is already a required method on `Repository[T]`**
(`storage.go`) — every `SQLRepo[T]`/`MemoryRepo[T]` implements it, unlike
`ColumnLookupRepository`, which is an optional extension. Trying it is
therefore safe and universal for *every* `Module[T]` type, not only the
four with a `humanIDColumns` entry — this closes a strictly wider gap
than T253's own humanID-only fallback. Confirmed both `Repository[T]`
implementations return `ErrNotFound` on a miss, matching `FindBySlug`'s
own contract exactly.

**Confirmed `transition_item` is out of scope.** Checked its own mcp-side
tool directly: its schema requires `"slug"` specifically, uses
`stringArg(args, "slug")`, never calls `identArg` at all.
`App.TransitionItem`'s own separate raw-SQL resolution path (`state.go`)
never claimed `"id"` support in the first place — a different tool with
an honest, narrower contract, not this bug.

**Conclusion: extend `resolveItem`'s own fallback chain with one new
step, `FindByID`, between the existing slug lookup and the humanID
fallback — mcp needs zero changes.** `identArg`'s own doc comment becomes
true rather than needing correction, once this lands.

### Design

Ordering argued, not arbitrary: slug stays first (unchanged existing
behaviour/performance for the common case — every existing caller and
test keeps working identically), `FindByID` second (universal, required
by the interface, no type-specific gate), `humanIDColumns` fallback last
(the most specialized, only four types). A slug string colliding with a
different item's real UUID is not a realistic concern (`NewID()` is a
UUID v7).

### Caught in architect review before implementation

The plan's own test list didn't include a dedicated test for `FindByID`'s
own non-`ErrNotFound`-error branch — a genuinely new line, distinct from
`FindBySlug`'s identical-looking check one step earlier, which the
existing `TestResolveItem_NonErrNotFoundPropagates` test never reaches
(its own `errorRepo` fails at the `FindBySlug` check first). Added
`TestResolveItem_ByID_NonErrNotFoundPropagates` with a new dedicated test
double (`slugMissIDErrorRepo`, ErrNotFound from `FindBySlug`, a real error
from `FindByID`). Also confirmed, not assumed, that the two "existing
test, same branch, reached later" cases
(`TestResolveItem_NonOrchestrationType_NoFallback`,
`TestResolveItem_RepoLacksColumnLookup`) still exercise their intended
branches post-change — both test doubles already implemented `FindByID`
returning `ErrNotFound` (a required interface method since T253), so both
correctly fall through the new step unchanged.

### Tests

4 new: `TestResolveItem_ByID` (the direct regression pin — a real
`Node.ID` resolves after a slug miss), `TestResolveItem_ByID_NonOrchestrationType`
(the new fallback works for a type with *no* `humanIDColumns` entry at
all — the actual scope difference over T253's own fallback),
`TestResolveItem_ByID_NonErrNotFoundPropagates` (architect-review catch,
above), `TestModule_MCPGet_ByID` (end-to-end through the real MCP entry
point, mirroring `TestModule_MCPGet_ByHumanID`'s own T253 pattern). All 6
existing `TestResolveItem_*` tests (T253) re-run and pass unmodified.

### Versioning

`resolveItem` is unexported — no exported Go symbol added or changed.
Real consumer-observable behaviour change: every `Module[T]` MCP tool now
resolves a real `Node.ID`, where it previously silently failed with "not
found." Matches A261's own precedent (behaviour change, no exported
symbol → PATCH bump), not A262's (new exported symbol → MINOR). Coverage:
96.3%; `resolveItem` 100%. `go test -race ./...` clean. Level 2
amendment.

---

## A267 — DefaultListOrder module option; sortItems gains numeric support (T262)

### Problem

`Task.Priority`/`Goal.Priority` (int, lower = higher priority) existed
and were stored but nothing read them. `list_task`/`list_goal` (and every
other compiled-type list tool) had no sort parameter at all, so a caller
always got whatever order the underlying query happened to return.
Discovered when two Tasks' own priority numbers contradicted their
intended sequencing, with nothing surfacing the mismatch.

### Investigation — real work found in both directions, per the task's own instruction

**mcp side:** there is no separate `list_tasks` tool — `"list"` is a
generic op dispatched by type name, calling `lm.MCPList(ctx,
statuses...)` for whatever type the tool resolves to. `MCPList` is part
of the **exported `MCPModule` interface** — confirmed one external
implementer, `smeldr.dev/media`. Changing its signature to accept a
caller-supplied `orderBy` would break every implementer, the identical
class of concern D49 solved for `TransitionItem` by adding a new `App`
method instead of changing the interface. A caller-supplied parameter is
real, legitimate design space — a new optional extension interface,
matching `SeqRepository`/`ColumnLookupRepository`'s own established
pattern — but bigger than this task's own scope and the concrete problem
actually reported.

**The other direction — a real, deeper bug found, not assumed already
fixed:** `ListOptions.OrderBy`'s own doc comment already said sorting
applies only to exported *string* fields. Traced the actual
implementation: `SQLRepo`'s path builds a real SQL `ORDER BY <column>`,
correct for an `INTEGER` column — a live SQLite/Postgres-backed instance
would sort correctly once `OrderBy` is set. `MemoryRepo`'s path
(`sortItems`/`stringField`) is different: `stringField` explicitly
returns `""` for any non-string kind, so sorting a `MemoryRepo` by
`"Priority"` treated every item as equal — a silent no-op sort, not a
partial fix. **"Just wire the existing machinery through" would not have
delivered real sorting for any `MemoryRepo`-backed deployment** — a real
prerequisite fix, not scope creep. `stringField` has 7 other call sites
(`ID`/`Slug`/`Status`/`FindByColumn` lookups) that are correctly
string-only by construction — left untouched; a new, separate
`sortFieldValue` used only by `sortItems`.

### Design — no breaking change anywhere, in either repo

Rejected a caller-supplied `orderBy` parameter (above). Chosen: a
**module-level default list order**, applied identically to both the MCP
and HTTP list surfaces of a type, set once at registration:

```go
func DefaultListOrder(field string, desc bool) Option
```

Solves the actual complaint with zero interface changes anywhere —
`MCPList`'s own exported signature and behavioural contract (returns
matching items) is unchanged; it simply returns them in a more useful
order for types that opt in. `mcp` needs zero changes, matching T214's
own precedent shape.

New unexported `Module[T]` fields (`defaultOrderBy`, `defaultOrderDesc`)
set via the existing option-parsing switch. New `withDefaultOrder`
helper, shared by `MCPList` and `listHandler`, so both surfaces agree on
order for the same type — avoiding the asymmetry of "MCP shows priority
order, HTTP doesn't" for identical underlying data.

`RegisterOrchestrationTypes`'s `Task`/`Goal` module construction each
gain `DefaultListOrder("Priority", false)`. `Signal`, `Decision`,
`Amendment`, `Run` are untouched — none have a `Priority` field.

### `storage.go` — numeric sort support, additive

New `sortFieldValue[T any](v T, name string) (s string, i int64, isInt bool)`
— a string for a string field, an int64 for any signed-integer-width
field, a zero value with `isInt=false` for any other kind (unknown field,
non-comparable type) — matching `sortItems`' own existing fail-quiet-to-
equal behaviour. `sortPair[T]` gains `intKey`/`isInt` alongside its
existing `key`; `sortItems` compares whichever key is populated. Every
existing string-field sort is unaffected — same comparison path, same
result, confirmed by re-running the existing tests unmodified, not
assumed.

### Tests

8 new: `TestSortItems_IntField` (ascending/descending, the direct
regression pin), `TestSortFieldValue_UnknownField`,
`_NonComparableKind` (fail-quiet cases), `TestModule_MCPList_DefaultOrder`,
`_NoDefaultOrder_Unchanged` (every other registered type's own
regression pin), `TestModule_listHandler_DefaultOrder` (MCP/HTTP
consistency, not asserted from `MCPList` alone),
`TestRegisterOrchestrationTypes_TaskGoalDefaultOrder` (scope check — the
wiring is real, not just designed). All existing `OrderBy`/sort tests
(string path) re-run and pass unmodified.

### Versioning

New exported symbol: `DefaultListOrder`. `sortFieldValue` unexported.
Real consumer-observable behaviour change: `get_task`/`get_goal` (MCP)
and `GET /tasks`/`GET /goals` (HTTP) now return items priority-ordered by
default. MINOR bump, matching A262's own precedent (new exported symbol
→ MINOR). Coverage: 96.3%; `MCPList`/`listHandler`/`withDefaultOrder`
100%; `sortFieldValue` 86.7%/`sortItems` 93.8% (uncovered branches are
the nil-pointer guard, non-struct `T`, and `Int8`/`Int16`/`Int32` kinds —
no content type in this codebase uses those kinds or ever passes a nil
pointer through this path; the same structurally-defensive-only class
already accepted elsewhere this session, named not chased). `go test
-race ./...` clean. Level 2 amendment.

---
