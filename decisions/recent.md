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
Archived 2026-08-17: A261-A264 → phase29-archive.md
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

## A268 — release.yml retries "confirm release exists" instead of failing outright (T265)

**Date:** 2026-08-17
**Status:** Proposed

**Context:** `.github/workflows/release.yml` triggers on tag push and, after
building `example/server`, checks that a GitHub Release already exists for
that tag before attaching a binary to it. The project's own documented
tag-then-release sequence (CLAUDE.md, "Tag and push sequence") pushes the tag
first — triggering this workflow immediately — then creates the Release as a
separate, later step. The check ran once, ~45-50s after the tag push (the
time the earlier build steps take), and failed outright if the Release
hadn't been created yet.

Hit for real 2026-08-17 tagging v1.70.0: an unrelated delay (fixing a
PowerShell encoding issue while extracting CHANGELOG release notes) pushed
`gh release create` past that ~50s window. The run failed with "release not
found," requiring a manual `gh run rerun --failed` once the release existed.
v1.70.1 and v1.71.0 avoided the same failure only because their release
notes were pre-extracted before tagging, keeping `gh release create` to a
few seconds after the push — an avoidable dependency on operator speed, not
something the workflow itself should require.

**Decision:** Replace the single check with a retry loop — 10 attempts, 5
seconds apart (~50s total) — before failing. Chosen to comfortably cover the
build-step latency plus an ordinary operator delay in creating the release,
without masking a genuinely absent release behind a long or silent wait.

**Developer pattern:** the tag-then-release sequence in CLAUDE.md is
unchanged — this Amendment does not ask for the release to be created before
the tag push, it only makes the workflow's own existing safety check
tolerant of that sequence's ordinary timing.

**Consequences:** CI config only. No Go code, no exported symbol, no
consumer-observable behaviour changed. No version bump, no tag — a workflow
file change is not itself something `example_test.go` or a README example
could break. Applies to `smeldr/core` only; the six standalone repos with
their own release automation are unaffected and out of scope for T265.

---

## A269 — resolveFlowID fails closed on a real DB error; CreateStateFlowTables exported (T249)

### Problem

`resolveFlowID` (`state.go`) collapsed a genuine DB error resolving a
type's state flow — SQLITE_BUSY, a cancelled context, a corrupted or
missing table — into `found=false, err=nil`, indistinguishable from "no
flow was ever registered for this type." `validateTransition` then
discarded that error entirely (`flowID, flowFound, _ :=
resolveFlowID(...)`) and returned `nil` when `!flowFound` — permitting the
transition with zero checks, including the required-role gate below it.
This directly contradicted D34's own fail-closed principle, already
correctly applied one call later for `lookupTransitionGate`'s identical
class of error (`gateErr != nil` returns `ErrInternal`). Named by the
architect during T243's own review, tracked as its own Task (T249).

### Investigation

Confirmed `transitionIsGated` (`provenance.go`), the second caller of
`resolveFlowID`, does **not** share the bug before assuming it did. Its own
contract is the opposite fail-closed direction from `validateTransition`'s
(withhold the actor on uncertainty, not reject the transition) — traced
`SubjectProvenance`'s use of `Gated` (`Gated: true` is what exposes
`ActorKind`/`ActorID`/`Surface`/`Reason`; `false` leaves them withheld).
`transitionIsGated` already returns `false` on any `resolveFlowID` error
today, and `false` is the safe, correct outcome for its own documented
contract (pinned by the existing
`TestTransitionIsGated_UnresolvableGate_FailsClosedNoActor`). No change
needed there.

### Fix

`resolveFlowID` distinguishes `sql.ErrNoRows` (legitimate "not found," the
type-specific lookup falls back to the default flow, or the default lookup
itself legitimately misses) from any other error (now propagated instead
of swallowed). `validateTransition` checks the new error and fails closed
with the same `ErrInternal` shape already used for `lookupTransitionGate`'s
sibling error path, immediately below it in the same function.

### A real, unplanned scope question surfaced during implementation

The fix correctly turned "no such table: smeldr_state_flows" from a
silently-ignored condition into a real, propagated error — which broke 7
pre-existing tests across `dynamic_test.go` and `relations_sweep_test.go`
that build a `DynamicTypeRepo`/status-transition path directly against a
raw `*sql.DB`, bypassing `smeldr.New` (which always calls
`migrateStateFlows` unconditionally when `Config.DB` is set), and so never
had the `smeldr_state_flows` table at all. These tests were never
exercising state-flow validation; they relied on the swallowed error to
skip it entirely, invisibly, until this fix.

Stopped and asked the architect before touching a file outside `state.go`/
`state_test.go`, presenting two options: route the shared test-DB helper
through `smeldr.New` (more coupling to unrelated App-bootstrap machinery
for one migration side effect), or export a narrow
`CreateStateFlowTables(db DB) error`, matching `CreateBlockTables`/
`CreateSchemaTable`'s own precedent as targeted, single-purpose exported
setup calls already used by the same test helper. **Architect's decision:
the latter, scoped narrowly** — extracted the five `CREATE TABLE IF NOT
EXISTS` statements out of `migrateStateFlows` into `CreateStateFlowTables`;
`migrateStateFlows` now calls it first, then proceeds with its own
unexported default-flow seeding, so there is exactly one place the schema
is defined. `openDynDB` (`dynamic_test.go`) and
`TestAppSweepStructural_DefaultChecker` (`relations_sweep_test.go`) both
call it in their setup now, matching every other `migrateStateFlows`-using
test in the package. `migrateStateFlows`'s own doc comment corrected in the
same edit ("four" → accurately describes the five tables via
`CreateStateFlowTables`).

### Tests

`TestResolveFlowID_QueryError` (type-specific query's real error is
propagated without falling through to the default-flow query — a DB error
is not "keep trying," unlike a legitimate miss),
`TestResolveFlowID_DefaultQueryError` (type-specific query legitimately
misses, default-flow query then hits a real error — also propagated),
`TestValidateTransition_flowResolutionError` (the direct regression pin:
`validateTransition` returns `ErrInternal`, not `nil`, when `resolveFlowID`
errors). All pre-existing `TestValidateTransition_*`/
`TestTransitionIsGated_*`/`TestSubjectProvenance_*` tests re-run unmodified
and pass — both legitimate-miss paths are unaffected by this fix. The 7
tests broken by the fix's own correctness (listed above) fixed by adding
`CreateStateFlowTables(db)` to their setup, not by weakening the fix.

### Versioning

`resolveFlowID`/`validateTransition` remain unexported — real
consumer-observable behaviour change (a transition attempted during a
genuine DB error previously succeeded silently; now correctly returns
`ErrInternal`), matching A266's own precedent (behaviour change, no
exported symbol → PATCH). `CreateStateFlowTables` is a new exported symbol,
Level 2 on its own merit, folded into this same Amendment per the
architect's own instruction (it exists strictly because this fix correctly
exposed a latent test-fixture gap, not as independent work). Coverage:
96.3% package-wide; `resolveFlowID`/`validateTransition`/
`CreateStateFlowTables` all 100%. `go test -race ./...` clean. PATCH bump:
v1.71.0 → **v1.71.1**.

---

## A270 — applyConflictPolicy resolves the real smeldr_-prefixed table (T229)

### Problem

`applyConflictPolicy`'s own static-table probe (`state.go`) checked only
the bare `<snake>s` form of a type's table name (e.g. `typeName="Task"` →
`"tasks"`) — never the `smeldr_<snake>s` form every one of the six
orchestration types' real tables actually uses (`smeldr_tasks`,
`smeldr_goals`, `smeldr_decisions`, `smeldr_amendments`, `smeldr_signals`,
`smeldr_runs`, confirmed directly against `orchestration.go`). A miss
silently fell through to treating the type as dynamic content, checking
conflicts against `smeldr_dynamic_content` instead of the type's own real
table — harmless while no orchestration type used a conflict policy, wrong
the day one does. Flagged in an earlier backlog audit, tracked as its own
Task (T229).

### Investigation

The already-correct reference implementation for this exact resolution
already existed in the same file: `resolveItemTable`, used by
`App.TransitionItem` and elsewhere, probes in order `smeldr_<snake>s`,
then `<snake>s`, then falls back to `smeldr_dynamic_content`. Existing
tests never caught the bug because `applyConflictPolicy`'s own test
fixture (`registerConflictFlow`) creates a table literally named
`conflict_types` — the bare form, matching the bug's own assumption on
both sides, never exercising the `smeldr_`-prefixed path at all.

### Fix

Replaced the inline probe with a direct call to `resolveItemTable` — DRY
reuse of the already-correct function rather than a second bespoke fix,
matching CLAUDE.md's own "check whether the logic already exists
elsewhere" directive:

```go
staticTable := resolveItemTable(ctx, db, typeName)
isDynamic := staticTable == "smeldr_dynamic_content"
```

`camelToSnake` remains used elsewhere (`state.go`, inside
`resolveItemTable` itself, `storage.go`) — not left dangling by the
removal of its third call site.

### Tests

New `TestApplyConflictPolicy_smeldrPrefixedTable`: creates a real
`smeldr_tasks` table (not the fixture's bare `conflict_types`), registers
a `ConflictReject` flow for `TypeName: "Task"`, inserts one row already in
the active state, and asserts `applyConflictPolicy("Task", ...)` returns
`ErrConflict` — proving the fix finds the real table instead of silently
checking `smeldr_dynamic_content`. All 18 pre-existing
`TestApplyConflictPolicy_*` tests re-run unmodified and pass — the
bare-form `conflict_types` fixture still resolves correctly under
`resolveItemTable`'s own fallback order (probe `smeldr_conflict_types`
first, miss, fall through to `conflict_types`, hit).

### Versioning

No exported symbol changed (`applyConflictPolicy`/`resolveItemTable` both
unexported). Real consumer-observable behaviour change: a conflict-policy
check against an orchestration type now correctly enforces against that
type's own real table instead of silently checking the wrong one. PATCH
bump, matching A266/A269's own precedent. Coverage: 96.3% package-wide;
`applyConflictPolicy` itself 95.5%. `go test -race ./...` clean. `golangci-
lint` zero findings. v1.71.1 → **v1.71.2**.

---

## A271 — RelationKindDef.ReverseLabel (T160)

### Problem

`RelationKindDef` had one `Label` field and no counterpart for the same
relation viewed from the target's own side (e.g. `"supersedes"` vs.
`"superseded by"`) — cloud maintained its own local workaround map rather
than a real field on the shared type.

### Investigation

Traced the full blast radius before proposing anything: the DDL
(`CreateRelationTables`), the shared `relationKindColumns` const used by
both `SELECT` and `INSERT`, `scanRelationKind`, `UpsertKind`'s
`INSERT ... ON CONFLICT DO UPDATE`, `ValidateRelationKindDef` (`Label`
itself is never validated — empty is valid), and
`RegisterOrchestrationRelationKinds` (`orchestration.go`), the one place in
this repo that actually defines relation-kind labels for the four seeded
kinds (`derives_from`, `depends_on`, `ships_as`, `supersedes`).

Confirmed `MCPUpsertRelationKind`/`MCPListRelationKinds` pass
`RelationKindDef` straight through with no separate mcp-repo param struct
— a new field reaches mcp automatically, no `smeldr.dev/mcp` change
needed (matching T214's own precedent shape).

### Design

`ReverseLabel string` (`db:"reverse_label"`), identical treatment to
`Label` in every respect: optional, unvalidated, zero-value = "not
provided," no requirement that it be set even when `Directional` is true.
Matching `Label`'s own already-optional character keeps this additive
rather than introducing an inconsistency between two sibling fields.

**Whether to populate the four seeded kinds — presented for review rather
than decided alone.** The Task's own example (`supersedes` →
`"superseded by"`) is concrete and directly actionable; the other three
kinds' correct reverse phrasing isn't stated anywhere in the Task or the
design docs, and inventing business terminology unprompted is exactly the
kind of unilateral call this session's own plan-first correction (T229)
exists to prevent. **Architect confirmed**: seed `supersedes` →
`"Superseded By"` only; leave the other three at the zero-value ("no
reverse label established yet" is the honest state until someone actually
decides the wording) — not a question for Peter to adjudicate three
placeholder strings, squarely an architect-level call.

### Migration

`CreateRelationTables` declares `reverse_label TEXT NOT NULL DEFAULT ''`
directly in its `CREATE TABLE` text (fresh installs) plus a paired
`EnsureColumn(ctx, db, "smeldr_relation_kinds", "reverse_label", "TEXT NOT
NULL DEFAULT ''")` call (pre-existing installs) — the same
declare-and-migrate-together shape established twice already this session
(A221's own precedent, reused by A264/T246). `CreateRelationTables` is the
single production entry point for these tables (confirmed via a full
call-site search: every test and `example/server/main.go` route through
it), so the migration call lives inside it once, not duplicated per
caller.

**Real, unplanned bug found and fixed along the way, in a shared test
double, not this Task's own code:** `failOnNthExecDB` (`coverage_test.go`,
used broadly for `CreateRelationTables`'s own `ExecContext`-failure test
suite) had a `QueryContext` stub returning `(nil, nil)` — a contract no
real driver ever honors (`database/sql` always returns either a valid
`*Rows` or a non-nil error). Harmless until now, because nothing in
`CreateRelationTables`'s call graph ever called `QueryContext` before
`EnsureColumn`'s own `PRAGMA table_info` probe. Caused a real nil-pointer
panic in `TestCreateRelationTables_ExecError2` (confirmed by actually
running the test, not assumed from reading the code — `sql.Rows.Close()`/
`.Next()` on a nil receiver panic). Fixed by making the stub return a real,
valid empty result set (`sql.OpenDB(&guardRowConn{noRow: true})`, the exact
pattern its own sibling `QueryRowContext` method already used one line
below) instead of `(nil, nil)`. This shifted `CreateRelationTables`'s own
`ExecContext` call count from 5 to 6 against this fake (it always reports
the column missing, so `EnsureColumn`'s `ALTER` always fires against it) —
added `TestCreateRelationTables_ExecError6` to keep every real statement's
own failure path covered, since the fake's own position-6 statement
(`idx_relations_governance_temporal`) would otherwise have gone untested
after the shift.

### Tests

`TestUpsertKind_ReverseLabel_RoundTrip` (Upsert → GetKind/ListKinds → a
fresh `NewRelationStore` re-hydration from the database, not only the
in-memory registry write), `TestUpsertKind_ReverseLabel_ZeroValue`
(unset persists as `""`), `TestCreateRelationTables_ExecError6` (see
above), `TestRegisterOrchestrationRelationKinds_RoundTrip` extended with
`ReverseLabel` assertions for all four kinds, `TestMCPUpsertRelationKind_OK`
extended to confirm `ReverseLabel` passes through the mcp surface
unchanged.

### Versioning

New exported field (`RelationKindDef.ReverseLabel`). MINOR bump, matching
A262/A267's own precedent (new exported symbol → MINOR). Coverage: 96.3%
package-wide; `CreateRelationTables`/`UpsertKind`/
`RegisterOrchestrationRelationKinds`/`MCPUpsertRelationKind` all 100%.
`go test -race ./...` clean. `golangci-lint` zero findings. v1.71.2 →
**v1.72.0**.

---
