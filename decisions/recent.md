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
