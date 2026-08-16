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
---

## A258 — DrainEvalQueue records provenance (T211, D51)

**Renumbered from the originally-drafted A260 at actual merge time —
`origin/main`'s live `DECISIONS.md` index was re-checked immediately
before this squash and was still A257 highest: T211 is the first of the
T239/T243/T211 trio to actually land, not the last as originally
assumed when the plan was written. A258 is correct as of this merge.**

### What was wrong

`App.DrainEvalQueue` applies its status transition via a raw `UPDATE`,
never through `SQLRepo.Save`/`Module.notifyAfter` — confirmed against
source: an eval-queue-driven transition produced zero `ProvenanceRecord`,
while the row's status genuinely changed. D51 named this precisely: a
scheduled Workspace condition's arrival had no record, and that absence
was the one thing making three separate design questions look like
three problems. This is that one absent write.

The authority half (never crossing a role-gated boundary itself) was
already closed — A241/D42. This Amendment is the observability half
only.

### Actor identity: `ActorKind: "job"`, not a `Run`

`ProvenanceRecord.ActorID`'s own doc anticipates a non-human actor as a
plain string, not a foreign key. `Run` (D38) is a heavier, structurally
different object — claim/lease/worktree semantics for M3's git-
worktree-based coding episodes. `DrainEvalQueue` is a stateless periodic
sweep with no claim, no lease, no worktree — forcing it through `Run`'s
shape would be a category mismatch, not reuse.

Decision: `ActorKind: "job"`, `ActorID: "drain-eval-queue"` — a fixed,
descriptive string naming the mechanism. **Generalises directly to
`SweepStructural` (T223)** as the same pattern: any stateless periodic
background mechanism gets `ActorKind: "job"` with a fixed `ActorID`
naming itself. Answers T220's question for the drain; T223's own plan
may cite this directly.

**This settles the actor question only.** A sweep's *run record* — that
a pass happened, when, what it walked, what it skipped — is a different
object, not addressed here and not foreclosed by this Amendment. An
actor is a string naming who acted; a run record is an entity queried,
ordered by time, with staleness derived from it, and by D51's own
reasoning about identity it sits considerably closer to `Run`'s own
shape than the drain's actor ever did. Whether that record is a `Run`,
something smaller, or something else again remains open — T223's own
plan settles it, not this one.

### Scope: provenance only, three effects argued out

- **Provenance — IN.** D51's own named gap.
- **Signal dispatch — OUT.** The drain's only current target is D40's
  `pending-re-evaluation → ratified/superseded` (`Decision`). Firing
  `AfterPublish`-class signals for an automated transition would
  activate every human-publish subscriber (webhooks, audit) with no
  operator decision that background automation should trigger them — a
  materially bigger change than "record that this happened."
- **Cache invalidation / rebuild triggers — OUT**, same reasoning:
  orchestration types are `APIOnly`-class content with no public HTML
  surface to invalidate or rebuild for; reachable only via the signal
  path just excluded.

### Failure handling: reuses `recordProvenance`'s existing fail-open discipline

`recordProvenance` (unexported, same package) already implements exactly
the rule this task needed: append, and on error, log at Warn and
swallow. Calling it from `DrainEvalQueue` inherits that discipline for
free. The queue-row deletion rule (A241, "not re-queued") stays
unconditional — a provenance write failure does not block or alter it.

`Surface: "trigger"` — the enum value existed but had no producer before
this Amendment; `DrainEvalQueue` is exactly the `TransitionTrigger`-
driven mechanism it was named for.

### Tests

4 new: a real drain run with `App.Provenance` wired produces exactly one
correctly-populated record; no `App.Provenance` wired is a no-op
(explicit regression pin on the existing fail-open contract); a failing
provenance store still lets the queue row delete and `triggered`
increment (A241 unweakened); the role-gated branch (Signal recorded, no
UPDATE) writes zero provenance records.

### Versioning

New behaviour (a write that did not happen before now happens), no
exported symbol changes (`recordProvenance` stays unexported). Coverage:
96.3% package-wide; `DrainEvalQueue` itself 100%. Patch bump, matching
A241's own classification for a prior `DrainEvalQueue` behaviour change
of the same shape. Level 2 amendment (crosses into `docs/ARCHITECTURE.md`
and touches an already-shipped function's real behaviour, even without a
signature change).

Status: Implements D51's own named gap — no new Decision, D51 already
settled the scope.

---

## A259 — Go toolchain bump to 1.26.6 across seven repos (T239, red main)

**Renumbered from the originally-drafted A258 at actual merge time —
T211 (this same session's other branch) landed first and claimed A258.
Re-checked `origin/main`'s live `DECISIONS.md` immediately before this
squash: highest is now A258 (T211). A259 is correct as of this merge.**

### What was wrong

`main`'s CI went red with no causing commit — `govulncheck ./...` failed
on `b95d886` (a docs-only archiving commit) reporting five Go
**standard-library** vulnerabilities (GO-2026-6218, GO-2026-6091,
GO-2026-6090, GO-2026-6089, GO-2026-5972), every one "Fixed in:
...@go1.26.6". Time-triggered, not commit-triggered: the vulnerability
database updated and every build started failing regardless of content.

### The fix: `toolchain go1.26.6`, not a `go` line bump

Two ways to move the toolchain say different things. `go 1.26.6` raises
the module's own declared minimum language version — every consumer
would need 1.26.6+ to build against us, forever, for a change that added
no language feature and fixed no bug in our own code. `toolchain
go1.26.6` pins the actual build toolchain (`GOTOOLCHAIN=auto`, default
since Go 1.21) without changing the declared minimum — `go build`/
`govulncheck`/etc. use 1.26.6 regardless of what's locally installed.

The five vulnerabilities are all stdlib, all patched in a Go *patch*
release — nothing about what the language or a caller needs changed,
only which binary compiles the code. This is exactly the case the
`toolchain` directive exists for. Bumping `go` itself would overstate
the change and read as a false claim in the git history the day a real
1.27 language-level bump happens.

### Scope: all seven owned repos, not just the two CI catches

`core` and `mcp` are the only two repos running `govulncheck` in CI
(both resolve their Go version via `actions/setup-go@v5`'s
`go-version-file: go.mod`, confirmed by reading each `ci.yml`), so those
two were the ones going red. `cli`, `media`, `oauth`, `agent`, `social`
carry the identical five stdlib holes with no CI step to catch them —
verified `go 1.26.5`/no `toolchain` line in all seven via direct
inspection before changing anything. Not having a `govulncheck` step
means less *visibility*, not less exposure, so all seven get the same
one-line fix for the same reason. `cloud`, `mail`, `site-dev` are
architect's own dispatch to the cloud/site implementers — not this
Amendment's scope.

### Durable note, so this doesn't require re-deriving from a diff next time

Recorded in `common/agent/skills/smeldr.md`'s "Common gotchas" section
(read by every implementing agent at session start) rather than a
per-repo README, since this recurs across every repo on the affected Go
version, not just whichever one someone happens to be reading.

### Versioning

Docs/config only — no source change, no exported symbol, no
consumer-observable behaviour change (the toolchain compiling the code
is not part of any module's API surface). No version bump, no tag,
matching A245/A246's own precedent for a build-tooling-only fix. Level 1
amendment.

Status: Implements no new Decision — a build-tooling fix, not an
architectural one.

---

## A260 — SubjectProvenance, the provenance read mechanism (T243)

**Renumbered from the originally-drafted A259 at actual merge time —
T239 landed first and claimed A259 for itself. Re-checked `origin/main`'s
live `DECISIONS.md` immediately before this squash: highest is now A259
(T239). A260 is correct as of this merge.**

### What was wrong

The audit trail was write-only: `ProvenanceRecord` written on seven
lifecycle events, readable through no surface at all — no HTTP route, no
MCP tool, no cloud surface, `ProvenanceFilter` had zero production
callers. Found 2026-08-14 immediately after Peter ratified D54 over MCP
with his own credential, and nobody could confirm the record named him
rather than the architect.

### The mechanism: `SubjectProvenance`, no new HTTP/MCP surface

`architect/design/provenance-visibility-brief.md` §4.7 is explicit: "no
new surface" — no standalone audit route, no Workspace view, no browsing
endpoint. This settles the task's own open question ("MCP tool, HTTP
route, or both?") in a direction narrower than either option: a plain Go
function, `SubjectProvenance(ctx, db, store, subjectType, subjectID)
([]ProvenanceEntry, error)`, composed directly by callers holding their
own `DB` handle — the same pattern `smeldr.dev/cloud`'s own
`internal/read.BuildTraceReading` already uses to read core data (`db
smeldr.DB` parameter, no round trip). `NewProvenanceStore(db DB)` being
already exported (`provenance.go:76`) confirms the composition is real —
cloud can construct a store from its own `smeldr.DB` today.

Item-scoped only — `SubjectProvenance` never sets `ProvenanceFilter`'s
`ActorID`, satisfying brief §4.1 ("actor is never a query key")
structurally rather than by caller discipline.

### The gating decision: §4.3, reusing validateTransition's own logic

`ProvenanceEntry` carries `Gated bool`; `ActorKind`/`ActorID`/`Surface`/
`Reason` are populated only when `Gated` — an ungated entry carries only
`Verb`/`FromState`/`ToState`/`Timestamp`, matching the brief's own
framing ("a word and a date, with nothing to open"). Gated means the
transition that produced the record required `RequiredRole` with
`Strict: true` — the exact predicate `validateTransition` (`state.go`)
already evaluates. Extracted `resolveFlowID`/`lookupTransitionGate` from
`validateTransition` into shared, unexported helpers (DRY — reused, not
reimplemented) rather than duplicating the `smeldr_transitions` query;
`validateTransition`'s own full existing test suite passed unmodified
after the extraction, confirmed before writing anything new, proving the
refactor is behaviour-preserving.

**Fail-closed direction stated explicitly, and it points opposite to
`validateTransition`'s own rule.** `validateTransition` fails closed
toward *rejecting the transition* (D34: a DB error must never silently
permit an unauthorized act). `transitionIsGated` fails closed toward
*withholding the actor* — a gate that cannot be resolved must never be
treated as safe to reveal. Both are the conservative choice for their
own question; a future reader should not assume they point the same way
just because both are called "fail-closed."

### Surface and Reason populated (added scope, architect's own instruction)

`App.Provenance`'s subscription built every `ProvenanceRecord` with
`Surface`/`Reason` unset. `Surface` is now real: `SignalEvent` and
`afterHookMeta` each gained a `Surface` field, threaded through a new
`surface` parameter on `notifyAfter` (unexported, no external signature
change) — all 14 production call sites in `module.go` traced individually
and given the correct one of three new constants (`surfaceHTTP`/
`surfaceMCP`/`surfaceTrigger`). `"cli"` has no producer — nothing in
`module.go` can distinguish a `smeldr-cli` HTTP request from any other,
named explicitly rather than silently omitted.

`Reason` plumbing is complete but honestly empty everywhere today: none
of the 14 call sites has a real reason value available (`updateHandler`/
`MCPPublish`/`MCPSchedule`/`MCPArchive` all pass `""` to
`validateTransition`, unchanged until T237 lands). Wiring the field now
means T237, when it ships, only changes its own four `reason` arguments
— this task's plumbing needs no further changes. Matches the brief's own
framing directly: "most acts carry none... an empty reason [should be]
unremarkable, not a hole."

### Real, tangential finding — flagged, not fixed

`DynamicTypeRepo.SetStatus`/`SetStatusWithReason` (`dynamic.go`) call
only `fireAsyncTriggers`, never `notifyAfter`/`dispatchBus` — dynamic-
content status transitions produce **no `ProvenanceRecord` at all**,
independent of this task (T243 is read-side only). `SubjectProvenance`
against a dynamic-content item correctly returns an empty or partial
history; not a defect in this task, worth naming so an empty result
isn't later mistaken for the mechanism being broken.

### Tests

12 new tests: `transitionIsGated`'s full branch table (strict+role,
role-without-strict, nil DB, same-state, no flow, undeclared edge,
unresolvable-gate-fails-closed), `SubjectProvenance`'s gated/ungated/
create-and-update-never-gated/no-actor-in-filter/store-error cases, plus
two Surface-propagation tests — one at the `dispatchBus` level (mirrors
`TestAppProvenance_JobDrivenTransition_ActorKindJob`'s own established
pattern) and one through the real `notifyAfter → afterHookMeta →
buildSignalEvent` path, not a hand-built `SignalEvent` literal.

### Versioning

New exported symbols (`ProvenanceEntry`, `SubjectProvenance`) — MINOR
bump. No existing signature changed (`notifyAfter` is unexported).
Coverage: 96.3% package-wide; `SubjectProvenance` 100%, `transitionIsGated`
88.9% (one structurally-unreachable branch — `resolveFlowID`'s own `err`
return is always `nil` by construction, named not chased, same class as
A251's own precedent). Level 2 amendment.

Status: Implements provenance-visibility-brief.md's engineering half —
cloud's own rendering into Trace's witness certificate is a separate,
later task (T245).

---

## D57 — Task is the ship-code pipeline; Goal is the generic backlog item

### Scope

architect (which type it creates for new work), core (both flows already
exist — this is a usage decision, not a schema change)

### Decision

**Task's own state flow (`backlog → active → waiting-plan →
plan-reviewing → implementing → commit-reviewing → done`) is deliberately
shaped for the ship-code dispatch cycle, not the general work-item type.**
The original design doc named this explicitly: "Task's own state flow
already mirrors the dispatch cycle exactly." `Goal`'s flow (`open →
in-progress → done`/`parked`) already is the general type: no
plan-review, no commit-review, no phase that presupposes a repo or a
diff.

Going forward: architect creates a `Task` only for work that ends in a
plan-reviewed, commit-reviewed change to a repo. Everything else — a
design discussion, a decision-in-progress, an investigation, anything
that concludes without that cycle — is a `Goal`.

### Why

Found 2026-08-15 reviewing `T244` (a six-turn discussion round with
brand, concluded, output written, already unblocking two other pieces of
work) sitting stuck at `backlog` with no honest path to `done`.
`get_valid_transitions` showed only `backlog → active` from where it
sat; reaching `done` from there would mean walking through
`waiting-plan`/`plan-reviewing`/`implementing`/`commit-reviewing` —
states that would each misrepresent something that never happened, since
no plan was written, no code was implemented, no commit was reviewed.

**The stuck state was not a flow defect. It was the wrong type.**
`Goal`'s flow already has the right shape, and was already documented
for exactly this in `design/self-hosting-the-architect-process.md`'s
original type mapping ("`ARCHITECT_TODO.md` rows not yet dispatched →
`Goal` items, `open`. Broad backlog, not yet actively worked" versus
`Task`'s own explicit dispatch-cycle mirroring). The distinction existed
from the start and drifted out of practice, not out of the model.

### Consequences

- Architect creates `Task` only for ship-code work; `Goal` for
  discussion/decision/investigation work.
- `T244` stays a historical `Task`, not retyped — the record is closed
  with a note rather than migrated, since nothing depends on its
  identity changing and retyping risks breaking the references other
  Tasks (`T243`, `T245`) already made to it by number.
- `T238` is unaffected by this decision — it is a genuine `Task` (an
  expected commit that turned out unnecessary), a different edge case:
  the flow's missing "verified, no work needed" exit, not a type
  mismatch. Tracked separately.
- Rejected: renaming `Task`'s own states (`implementing`/
  `commit-reviewing`) to generic terms. They are accurate for what
  `Task` is actually for; the fix was choosing the right type, not
  blurring the right one's own vocabulary.

---

## D58 — A governed flow closing on work done outside its own tracked history gets an honest terminal state for that, distinct from success

### Scope

core (`orchestration.go`'s five governed flows), `state.go` (`IsTerminal`'s
own documented meaning)

### Decision

**When a governed flow's item can close because the underlying need was
met — but not by that item's own tracked work — the flow needs a
terminal state naming exactly that, distinct from the state meaning
"this item's own work produced the outcome."** Two flows lacked one:

- `Task`: `plan-reviewing` had exactly one legal exit, `implementing` —
  no way to close a plan whose own conclusion was "already done
  elsewhere, nothing to build" (found via T238, which sat stuck at
  `plan-reviewing` with no honest path forward).
- `Goal`: `parked` was marked `IsTerminal: true` but has an outbound
  edge back to `open` — by `state.go`'s own definition that makes it
  not a sink at all, so `Goal` had no real one-way close distinct from
  a temporary, resumable pause (found via T146, never actioned).

Both are the same shape, settled together: a new `resolved` terminal
state in each flow, `RequiredReason: true` on every transition into it
(the whole point of the state is an explanation of what resolved it
elsewhere; a transition carrying none would be the same problem one
level down). `Task.resolved` is reachable from `active`/`waiting-plan`/
`plan-reviewing` — every state that precedes a real build actually
starting, not only the one state where the gap was first found, since
the same discovery can surface at any of the three. `Goal.resolved` is
reachable from `open`/`in-progress`/`parked`. `Goal.parked` loses its
`IsTerminal` flag as a pure honesty fix — it does not change what
`parked` already does, only what it claims about itself.

### Why the other three governed flows needed no change

Checked individually against their own registered `Transitions`, not
assumed symmetric with `Task`/`Goal` (`Run` has no `StateFlow`, D38,
out of scope):

- **`Signal`**: `acknowledged`/`expired` both have zero outbound edges
  in either direction. A `Signal` is a system-emitted notification
  (D42's class), not tracked work that could be "resolved elsewhere."
- **`Amendment`**: `merged`/`rejected` both have zero outbound edges.
  `rejected` already covers "this item's own tracked work did not
  produce a shipped outcome," `merged` specifically means code landed.
- **`Decision`**: `superseded` already *is* this shape — a newer
  `Decision` replacing an older one is "resolved by other means,"
  simply under an existing name. No new state needed.

### A related, smaller, separate finding — flagged, not fixed

`Decision.superseded` is marked `IsTerminal: true` but has an outbound
edge to `archived` (`orchestration.go`) — structurally the same shape
as `Goal.parked`'s own bug, just far more benign: `archived` is also
terminal, so the edge never leads back to live work, only between two
closed states (further bookkeeping on an already-settled `Decision`,
never a reopening). `state.go`'s own doc comment on `IsTerminal` read
literally ("no outbound transitions are permitted from a terminal
state") already forbade this, undetected, because nothing has ever
validated it — confirmed: no code anywhere consults `IsTerminal`
besides upserting the column at registration.

**Resolved as a documentation fix, not a code fix.** The doc comment
described a stricter invariant than the codebase has ever actually
relied on — `Decision`'s own flow already depended on the looser
reading the day it was written. Corrected `state.go`'s comment to say
what is actually enforced and actually true: a terminal state may not
transition to a *non-terminal* one (a reopening), but may transition to
another terminal state (further closed-state bookkeeping). Inventing a
fix for `Decision.superseded → archived` was considered and rejected —
nothing is hitting it, and building one speculatively is exactly the
kind of unscoped work this decision's own `Goal`/`parked` finding
argues against.

### Rejected alternatives

- **Fixing only the one literally-cited state per flow** (`Task`'s
  `plan-reviewing`, `Goal`'s `parked`) rather than every state that
  precedes the point of no return. Would relocate the same bug to
  whichever of the other states the same discovery next surfaces at,
  not close the shape.
- **A single shared `resolved`-reachability mechanism instead of
  per-flow transitions.** No such mechanism exists in `StateFlow`
  today (transitions are declared per flow, per state) and building
  one would be new infrastructure for a two-flow problem.
- **Fixing `Decision.superseded → archived`'s structure to match
  `IsTerminal`'s literal doc comment**, rather than correcting the
  comment. Rejected above — no live defect, and Decision's own flow
  has relied on the looser reading since it was written.

### Consequences

- `orchTaskFlow`/`orchGoalFlow` (both unexported, `orchestration.go`)
  gain a `resolved` state and its transitions — no exported Go symbol
  changes, but real consumer-observable behaviour: `transition_item`/
  `get_valid_transitions` (mcp) now show a new valid state for `Task`
  and `Goal`.
- `state.go`'s `IsTerminal` doc comment corrected to describe actual,
  relied-upon behaviour.
- Any future governed flow author should read this decision before
  assuming `IsTerminal` alone is enough — it says nothing about
  *reachability into* the terminal state, which is each flow's own
  transitions to design honestly.

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
