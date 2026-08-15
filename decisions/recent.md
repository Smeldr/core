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
