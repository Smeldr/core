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
---

## D52 — `investigates` targets a condition's subject, which is often an edge

### Scope

core (relation kind declaration), process (Workspace delegation)

### Decision

`investigates` is declared `Task` → `Decision` **and** `Task` →
`RelationEdge`. D45 declared only the first.

### Why

D51 rules that a condition is identified by its qualified subject
`(subject_type, subject_id)`, and that subject is an edge for two of the
three provenances:

| Condition | Subject a `Task` would investigate |
|---|---|
| Detected | the `RelationEdge` carrying `invalid_at` |
| Asserted (contradiction) | the `contradicts` edge |
| Scheduled | the item — today only `Decision`, the one type with a gated re-evaluation state |

So `Task` → `Decision` is correct for exactly one of three cases. For a
contradiction it is worse than incomplete: pointing the investigation at
one of the two `Decision`s **chooses a subject**, which is precisely what
the symmetry of `contradicts` forbids and what D45 itself established
when it made the kind non-directional.

The system already treats edges as subjects — `recordAssertProvenance`
writes `SubjectType: "RelationEdge"` (`relations.go:359-364`) — so this
declares something the model can already express rather than inventing a
shape.

### Both pairs, not a superset

Declared as the two pairs that are real today, not `Task` → anything.
Other orchestration types have no gated state that produces a scheduled
condition, so a pair naming them would be speculation. When a second type
gains one, this gets another pair and that is the right cost.

### Consequence stated honestly

`TypePairs` is documentation-only and never enforced at write time
(A236), so nothing was rejecting the wrong edge and nothing will accept
the right one differently. **The value here is that a declaration which is
documentation gets read as truth, and a wrong one is worse than an absent
one.** Whether declarations should be enforced at all is T233's question,
untouched here.

### Status

Ratified 2026-08-11 (Peter, via architect session). Amends D45, which
otherwise stands as ratified — recorded as its own Decision rather than as
a correction buried in D51's body, so the change carries its own lineage.

---

## D51 — A condition is a projection identified by its subject; a finding is a persisted, detector-owned record

### Scope

core (the `Finding` type), process (Workspace's model contract)

### Decision

**1. Conditions remain projections and own no state.** This confirms D46
and closes T221's original framing ("are Workspace conditions persisted
items"), which predates D46 and asks a question D46 already answered.

**2. A condition's identity is its qualified subject
`(subject_type, subject_id)`** — the durable object whose state
constitutes the condition. Detected: the `RelationEdge` carrying
`invalid_at`. Asserted: the `contradicts` edge. Scheduled: the item
itself. **No record is created for a condition, in any provenance.**

**3. Findings are persisted as a thin, detector-owned type, named
`Finding`.** Written only by detectors: no `create_*`/`update_*` MCP
tools, no human-driven state flow. This answers D46's own open question.

### Why 2, derived rather than enumerated

An identity exists so that two references to one thing are recognisable
as the same. That yields five uses, and each was tested against the
scheduled provenance — the only one with no obvious object:

| Use | Requires | Scheduled condition |
|---|---|---|
| Stable rendering across refresh | something immutable to key on | the item's ID |
| Model linkage (a suppressing `Decision`, an `investigates` `Task`) | an edge target | the item; suppression applies only to findings (D46) |
| Addressing (a URL per row) | an address | the item's own address |
| Detector dedup | a fingerprint across runs | vacuous — no detector runs, and D40's two gated exits are **one** condition per the `(condition)` ruling, so one item is one scheduled condition |
| History ("was this present last week") | an arrival timestamp | missing — but that is T211's absent write, not an identity |

All five are covered and none requires a new object. The three
convergences recorded in T221 were therefore one absent write seen from
three angles, and the answer is to make `DrainEvalQueue` write
provenance, not to persist conditions.

### Why 3

D46 removed most of the shape the July analysis drew.
`acknowledged`, `dismissed` and `accepted-as-is` are human claims about
resolution, and D46 rules that a finding resolves when its **detector
stops firing**, while suppression is a `Decision`. `delegated` became
model work via `investigates`. What survives as grounds for persistence
is real but narrow: dedup across sweep runs, a stable target for a
suppressing `Decision`, and history.

**A write surface would invite exactly the behaviour D46 forbids**, so
the type has none — the tools would be the invitation. Nearest precedent
is `Run` (D38): a type whose authoritative state lives outside
`Node.Status`.

### Named `Finding`, and the trail to the prior art

`analysis/fable5-operational-insights-report-2026-07-06.md` and **T126
call this `Insight`**. Recorded explicitly so the prior art stays
findable — D41 exists because this project loses its own earlier work.

Renamed for two reasons. D46 (one month later) established **finding** as
the precise term for what a detector produces, so `Insight` would name the
concept with a word the model no longer uses for it. And `Insight` is
already a **Cloud capability** in `cloud-strategy.md` ("surface what the
model implies but does not state") — a core type of the same name puts one
word on a free AGPL primitive and a sold continuous service at once,
across the two-vocabulary boundary. Article X: precise and intentionally
small.

### Rejected alternatives

- **Conditions as persisted items** (T221's original framing). Puts state
  in the surface's own subject matter and re-creates the "the surface can
  be made to lie" failure §13.5 exists to prevent.
- **A seventh orchestration type with full CRUDAP for findings.** The
  write surface contradicts D46's resolution rule.
- **A per-condition record for the scheduled provenance.** Dissolved by
  deriving what identity is for.
- **Deferring the identity question to empirical observation.** Architect
  proposed this; Peter rejected it as hedging. The question was answerable
  by reasoning and has been answered — deferring would have left T211 and
  T223 blocked on a non-question.

### Boundary

- Identity must be **qualified**. Item IDs and edge IDs are both
  `NewID()` UUIDs in conceptually different namespaces. The pattern
  already exists: `smeldr_relations`' `source_type`/`source_id`, and
  `recordAssertProvenance`'s `SubjectType: "RelationEdge"`.
- **Not settled here:** §13.9c's volume question — what a Workspace with
  3, 12 or 47 simultaneous conditions looks like. Presentation, not
  model, and it blocks nothing.
- `investigates`' declared pair is wrong for contradictions under this
  ruling. Recorded, not fixed here: it becomes **D52**, so the amendment
  to D45 carries its own lineage.

### Unblocks

T211 (arrival provenance is now known to be the whole scheduled gap),
T223 (detectors can be wired knowing what they must record), T126 (the
type's shape and name are settled).

### Status

Ratified 2026-08-11 (Peter, via architect session).

---

## D50 — The Task flow is the protocol; Signals are not a chat channel

### Scope

process (the live-instance coordination protocol), core (one flow rename)

### Decision

The ship-code pipeline runs on `Task` state transitions alone.
`waiting-plan → plan-reviewing` *is* plan-ready,
`plan-reviewing → implementing` *is* approved-start,
`implementing → commit-reviewing` *is* commit-ready. Each transition
already carries actor, timestamp, audit, and a `Reason` field for the
one-line message; plans and answers stay in plan files, as always.

**Retired for the live channel:** protocol-verb `Signal` records, the
`sequence` counter, the `read` step, and dispatch signals — a dispatch is
the `Task`'s own creation.

**The `Signal` type is reserved for system-emitted notifications** —
D42's automation-stopped-at-a-gate class — not agent conversation.

**The flow renames `architect-task` → `agent-task`.** It governs any
agent's work through the pipeline; the old name claimed the process was
architect's own, which is also why the flow living in core looked like a
layering smell. A generic agent work pipeline earns its place in the
framework; only the name said otherwise.

**Delivery:** interactive sessions use session-start queries plus the
desktop doorbell (transport, never record, per AGENT_PROTOCOL.md's own
rules). Headless and product-grade delivery is T231's event coverage plus
M3's relay — deferred with M3, since the doorbell does not exist for
unattended sessions and no customer has Peter's desktop app. File-based
(unmigrated) roles are unchanged.

### Why

The Signal-record protocol reified the file channel's own compensations.
Explicit messages, sequence counters and read-receipts existed because
files cannot push and hold no state; copying that shape onto a system
that already has state, ordering, audit and events produced double
bookkeeping — the same phase recorded in `Task.Status` and again in a
`signal_type` chain, with no rule for which record wins when they
disagree. That is Article IV's silent ambiguity, built in on day one of
the channel.

### Rejected alternatives

- **Keeping both records.** Drift by design; every future reader must
  reconcile two accounts of one fact.
- **Building the relay now for interactive use.** The doorbell suffices
  for active sessions; the relay serves headless automation and belongs
  to M3's own track.
- **Keeping the `architect-task` name.** Generic behaviour under a
  role-specific name is how the layering question arose at all.

### Status

Ratified 2026-08-11 (Peter, via architect session). Takes effect between
cycles: T230 finishes under the old protocol; the next dispatched task
runs on Task transitions alone.

---

## A257 — transition_item/set_content_status gain reason (T235, smeldr.dev/mcp half)

### What shipped

`transition_item` and `set_content_status` (`smeldr.dev/mcp`) both gain an
optional `reason` string parameter, threaded to `smeldr.dev/core`'s new
`App.TransitionItemWithReason`/`DynamicTypeRepo.SetStatusWithReason`
(A256) respectively — satisfying a `Transition.RequiredReason` gate,
which neither tool could reach before (both always passed an empty
reason to the underlying core call). Omitting `reason` behaves exactly as
before either tool shipped it.

`set_content_status` was checked and fixed alongside `transition_item`
per the dispatching task's own instruction to look for siblings with the
same gap rather than closing one call site and leaving another open — it
had the identical defect.

### Versioning

Requires `smeldr.dev/core` v1.65.0+ (`App.TransitionItemWithReason`,
`DynamicTypeRepo.SetStatusWithReason`) — go.mod bumped only after core's
tag was proxy-verified, not `go.work`-local, matching every prior
core+mcp cycle. New consumer-visible tool capability on both tools.
MINOR bump v1.30.2 → v1.31.0.

Status: Implements no new Decision — mcp-side half of the same fix A256
implements on the core side. See A256 for the full root-cause
investigation (both dispatch assumptions corrected, the `SetStatus`/
`SetStatusWithReason` precedent found and reused) — not restated here.

### Note

This entry backfills a completeness gap found at close-out review: A256
covered the core half, but the mcp half's own Amendment was never
written, and mcp's own `[1.31.0]` `CHANGELOG.md` entry cited no A-number
where every other entry there does. Third occurrence of this exact shape
in this session (after A250, A252) — flagged by the architect as
structural, not incidental: when a cycle spans two repos, the second
repo's Amendment is the one that goes missing, because a `Task`'s single
state has no point in the flow where the second repo's work gets
checked. Not solved here — the architect owns the fix, tracked
separately from this backfill.

---

## A256 — App.TransitionItemWithReason (T235, smeldr/core half)

### The gap, verified — and two of the dispatch's own assumptions corrected

`transition_item` (mcp) has no `reason` parameter. The dispatch assumed
`App.TransitionItem`'s signature "already takes a reason internally per
A251" and that the REST path (`updateHandler`) has a working reason
convention to mirror. Neither holds. Read `App.TransitionItem`'s full body:
every `validateTransition` call inside it passes a hard-coded `""`. Grepped
every `validateTransition(` call site in the repo: `updateHandler`,
`MCPPublish`, `MCPSchedule`, `MCPArchive`, and `newSetStatusHandler` all
pass `""` too. **No live entry point anywhere — REST or MCP, dynamic or
compiled — has ever threaded a real reason through to `validateTransition`.**
A `Transition` with `RequiredReason` set has been unreachable from every
direction since the field was added (T149/A220), not a `transition_item`-
specific gap.

### The precedent, found instead

`DynamicTypeRepo.SetStatus`/`SetStatusWithReason` already solved exactly
this problem once, for a different caller — a second exported method
added specifically to carry a reason without changing the original's
signature, preserving the API stability promise. `SetStatusWithReason` is
itself unwired: no production caller anywhere, tests only. This is the
shape to reuse, not invent a second convention.

### The fix

New `App.TransitionItemWithReason(ctx, typeName, slug, toState, reason
string) (map[string]any, error)` (`state.go`), mirroring `SetStatus`/
`SetStatusWithReason`'s exact shape. `TransitionItem` becomes a thin
wrapper — `return a.TransitionItemWithReason(ctx, typeName, slug, toState,
"")` — unchanged signature, unchanged behaviour, every existing caller and
test untouched (confirmed: all 15 pre-existing `TransitionItem` tests pass
unmodified). `TransitionItemWithReason` carries the real logic: the
dynamic branch calls `SetStatusWithReason` instead of `SetStatus`; the
compiled branch passes `reason` into `validateTransition` instead of the
hard-coded `""`.

**Architect's own note, recorded per instruction**: this is the *second*
`X`/`XWithReason` twin in core. Two is a pattern; a third would be
accretion — the honest moment to revisit this shape is when a third
reason-bearing operation appears, not now, when following the existing
precedent is plainly better than inventing a third convention.

### Out of scope, spun into its own task

The four `module.go` call sites (`updateHandler`, `MCPPublish`,
`MCPSchedule`, `MCPArchive`) still pass `""` — after this Amendment the
REST path still cannot satisfy a `RequiredReason` gate. That needs a
wire-format answer (where does a reason arrive in a `PUT` body), a
different question from threading an already-existing parameter.
Deliberately not widened into here — tracked as T237.

### Tests

`TestApp_TransitionItemWithReason_Compiled_RequiredReason` and
`_Dynamic_RequiredReason`: both prove `TransitionItem` (empty reason via
the wrapper) still fails a `RequiredReason` gate, and
`TransitionItemWithReason` with a real reason succeeds — on both the
compiled and dynamic branches. All 15 pre-existing `TransitionItem` tests
pass unchanged, proving the wrapper introduces zero regression.

### Versioning

New exported symbol (`App.TransitionItemWithReason`) — MINOR bump. No
behaviour change for any existing caller. Coverage: 96.3% package-wide;
`TransitionItem` 100%, `TransitionItemWithReason` 97.4% (one uncovered
branch, `DynamicContentRepo`'s own error path inside the already-passed
`Kind == "content"` check — the identical, already-named,
structurally-unreachable branch A251 itself documented; not new, not
chased).

Status: mcp half (tool schema changes for `transition_item` and
`set_content_status`) sequenced after this tag is proxy-verified, per
established practice.

---
