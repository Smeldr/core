# Smeldr — Decisions Archive (Phase 27)

Archived from `decisions/recent.md` on 2026-08-15. Entries D50-D56, A256-A257.

---

## D56 — `Standing` means the governed state, and the two state axes are named as two

### Scope

cloud (`internal/read`), core (the distinction it makes explicit),
architect (`constitution/vocabulary.md`, which is missing the axis)

### Decision

**A Managed Item has two independent state axes, and they are not versions
of each other:**

| Axis | Answers | Values | Every item has one? |
|---|---|---|---|
| Lifecycle (`Status`) | is this visible and served? | `draft`, `scheduled`, `published`, `archived` | yes |
| Governed state (StateFlow) | where is this in its own governed process? | per flow, e.g. `proposed`, `ratified`, `superseded` | only types with a registered flow |

They are orthogonal. A `Decision` may be `published` and `proposed` at the
same time, and both facts are true and useful: the first says an API caller
can read it, the second says nobody has ratified it.

**`Standing` means the governed state. It is empty for a type with no
registered flow — never a fallback to lifecycle.**

Counting held-open items therefore skips types with no flow, which is
correct: an item with no governed process cannot be unresolved.

### Why

`internal/read/anchor.go`'s `Standing` reads `Node.Status` while its own
doc comment claims "the current StateFlow state string". The two
vocabularies coincide only on `archived`, by accident.

The consequence was never cosmetic: Tension and held-open counting for
related `Task`/`Goal`/`Amendment` items read the wrong axis, so such items
could apparently never read as resolved through that path.

**A fallback to lifecycle was considered and rejected.** It reintroduces
exactly today's defect in a quieter form: `archived` would again mean two
different things depending on which axis produced it, and a reader could
not tell which. An empty `Standing` says "this type has no governed
process", which is a true and useful statement. A borrowed one says
something false.

**Peter's framing, which is the reason this is a decision rather than a bug
fix:** the surface should show what the surface is about. Nobody reading a
decision graph asks whether the row is published; they ask whether it is
ratified. The lifecycle axis is the CMS heritage, and it stays — it is not
legacy to be removed, it answers a real question. It simply is not what
`Standing` is for.

**The axes coexist by design, and this is the first document to say so.**
`constitution/vocabulary.md` maps the constitutional "Lifecycle / State
(Art VII)" concept to `Status` and does not mention governed states at all.
The map was missing an axis, which is how one field came to be asked to
carry both. That gap is architect's to close in `vocabulary.md`, in the
same pass as this decision.

### Consequences

- `Standing`'s implementation and doc comment agree, on the governed state.
- Held-open counting changes behaviour for flowless types: they stop being
  counted rather than being counted wrongly.
- `constitution/vocabulary.md` gains the two-axis distinction.
- A237's honesty fix (T236) made the doc comment describe the code; this
  decides which of the two the code should have been doing, which was the
  open half that fix deliberately left.

---

## D55 — A closed module may sell a domain, never a capability the model needs

### Scope

constitution reading (`cloud-strategy.md`, Article IX), commercial strategy

### Decision

**Smeldr may offer closed, separately licensed modules, provided they add a
domain rather than withhold a capability an organization needs to own and
operate its model.**

The test, applied before anything is built: **could an organization own and
operate its own operational model without this?** If yes, it may be a
closed module. If no, it belongs in core and stays AGPL.

No such module is planned. This decision preserves the option and fixes the
boundary while nobody has an interest in where it falls.

### Why

The question was whether Smeldr could sell closed add-ons the way Umbraco
sells Forms — the one part of that revenue model not already open to us
(Cloud is in progress, support becomes sellable when there is anyone to
support, certification and partner programmes need an ecosystem that does
not exist).

**It looked constitutionally blocked. On reading the clause, it is not.**
`cloud-strategy.md` says: *"Smeldr does not withhold a capability an
organization needs to own and operate its model in order to upsell Cloud."*
That is a condition of **necessity**, not of breadth. It does not say core
contains everything; it says core does not withhold what ownership
requires. A module that is not required for ownership was never inside the
prohibition.

This is a reading of the existing text, not an amendment and not a
loophole — the qualifying clause is in the sentence, and taking it
seriously is what reading it means. No constitutional change is needed, and
that matters: an amendment made speculatively, for a revenue line nobody
has planned, would be the loophole.

**Article IX supplies the ladder that makes the distinction usable.**
Capabilities strengthen the operational model; features realize
capabilities; implementations realize features. A capability is load-bearing
for the model itself. A domain is a subject area the model can be applied
to. Removing validation from core would cripple the model. Not shipping a
compliance domain does not.

### The line, stated so it can be applied rather than argued each time

- **Core, always:** anything the operational model needs to exist and be
  operated — types, authority, state, relations, provenance, the surfaces
  that reach them.
- **Cloud:** running those capabilities continuously across the whole model,
  which is what `cloud-strategy.md` already says commercial value comes
  from.
- **Eligible for a closed module:** a bounded domain built *on* a fully
  capable core, whose absence leaves the core no less capable.

### Rejected alternatives

**Amend the constitution to permit withholding capabilities.** Rejected:
it would break Article XII's ownership principle, and the honest reading
above makes it unnecessary. An amendment is not cheaper than reading the
sentence.

**Rule the whole add-on model out.** Rejected: it forecloses a proven
revenue line on a misreading, and the misreading is ours.

### Consequences

- The option is preserved without any code, licence or constitutional
  change today.
- Any future closed module must state which domain it adds and answer the
  ownership test in writing before work starts.
- Recorded so the reasoning is not rebuilt from scratch each time the
  question resurfaces, which was this question's own history.
- Untouched and separate: whether consulting revenue competes with Cloud
  for Peter's hours is a business-shape question, not a licensing one.

---

## D54 — A cycle spanning two repos is two Tasks joined by `depends_on`

### Scope

process (Task shape), all implementing agents

### Decision

**A cycle that spans two repositories is two Tasks joined by a
`depends_on` edge, one per repo — never one Task covering both.**

Each runs its own full flow: its own section in the plan file, its own
commit review, its own `done`. The upstream Task reaching `done` is what
releases the downstream one, which is the release ordering that already
exists in reality (upstream tag → proxy verify → downstream pin bump →
downstream tag).

**Either party opens the downstream Task the moment the plan establishes a
second repo is in scope** — not at dispatch. Dispatch cannot know: T235 was
dispatched as `XS` and turned out to be one core method plus two mcp
schemas plus a release sequence. The plan gate is where scope becomes
known, and requiring architect to predict it would reintroduce the guess
that failed.

### Why

Three times, a two-repo cycle shipped with the second repo's Amendment
missing: A250 (found weeks later, during T230), A252 (caught at review),
and A257 (caught at close-out). The shape is identical every time — it is
always the *second* repo's Amendment, never the first's.

A `Task` has one state, and in a two-repo cycle that state is consumed by
the first repo's work: `commit-reviewing` means "the core half is ready".
The second repo's half cannot even be pinned until the first is tagged and
proxy-verified, so by construction it arrives *after* the state that would
have checked it. There is no point in the flow where anyone is asked "and
the other repo?"

This is not carelessness, and core-implementer named the constraint itself
during T235 rather than working around it silently.

### Rejected alternatives

**Extra states on the Task flow** (`commit-reviewing-upstream` →
`commit-reviewing-downstream`). Rejected: it bakes a two-repo assumption
into a flow shared by every band, and an n-repo cycle would need n states.
The `depends_on` edge expresses ordering without special-casing the count.

**A `repos: [core, mcp]` field on the Task.** Rejected, and this is the
sharpest of the three: **a field is not a gate.** The defect is precisely
that nothing checked. A declared-but-unconsulted field repeats `TypePairs`
(A236), still listed as an open loose end two months later. We have the
receipt for this mistake already.

**Requiring an Amendment per repo at close, and nothing else.** Not
rejected — but it constrains what `done` requires, not what a Task is. It
composes with this decision rather than replacing it, and alone it is a
rule with no checkpoint attached, which is the situation that produced the
three misses.

### Consequences

- Detection remains possible later and belongs with the sweep: "a released
  tag with no Amendment citing it" is a checkable condition of exactly the
  kind D46/D51 describe.
- This gives the same three-level shape as the relations convention —
  convention now, detector when the sweep is wired (T223), framework gate
  last if it earns it (T233's family). The consistency is deliberate: it is
  the same question each time, which is *where* an invariant should live.
- Cost, stated plainly: more Tasks, and for a small change the pairing
  reads as overhead. The counter is in the evidence — all three missed
  Amendments were small changes. Small is where this fails, because small
  is where "it is just a pin bump" wins the argument.

---

## D53 — Breaking changes stay inside v1 until the first external importer; v2 is reserved for a stability statement, not a feature milestone

### Scope

core (and every `smeldr.dev/*` module that follows its versioning), release
process, site-dev (the vanity handler, as a prerequisite recorded below)

### Decision

Two halves, one decision, because the second is what makes the first
honest rather than merely convenient.

**1. Breaking changes to core's exported API are taken inside v1, without a
major bump, for as long as no external importer exists.** Downstream repos
move their pin and fix the call sites that actually changed. No
compatibility twin is added to preserve a signature for a caller that does
not exist.

**2. "No external importer" is a checked fact, not an assumption.** The
check is the module proxy's own importers index
(`pkg.go.dev/smeldr.dev/core?tab=importedby`), run at release time and
folded into the clean-clone and pin-currency check that T217 exists to
build. The moment a module outside `smeldr.dev/*` appears there, this
decision's first half expires and a breaking change requires v2.

**3. v2 is not earned by accumulating features.** It is reserved for a
deliberate statement that the API is settled and we stand behind it, spent
when there is someone to say it to — the first external importer, or public
launch, whichever comes first. Finishing the consolidation is what makes
the statement true; having an audience is what makes it worth making.

### Why

**The evidence, gathered rather than assumed.** The proxy's importers index
lists exactly five importers of `smeldr.dev/core`: `agent/flow`,
`core/pgx`, `mcp`, `media`, `social`. Every one is ours. GitHub's clone
statistics are ambiguous and were not used.

**The limit of that evidence, stated rather than rounded away:** the index
only sees modules that are themselves published to the proxy. A closed,
internal consumer would not appear. There is no positive sign of one, and
the check above is the standing way we would find out.

**What v2 actually costs, and it is not the rename.** Rewriting 150 import
references across our own repos is mechanical. The permanent cost is that
`smeldr.dev/core/v2` becomes what every future document, README, code
example and article says, forever, and that two live major versions must be
reasoned about from then on. That is a real price, and today it would buy
protection for a population of zero.

**The uncomfortable observation this decision answers.** Core is at v1.65
and behaves like v0: sixty-five minor releases, and we are about to take a
breaking change inside v1 because the alternative was six compatibility
twins protecting nobody. The version number does not currently carry the
meaning it claims. This decision does not fix that by renumbering; it names
the condition under which the number starts being true, and the
consolidation is the work of getting there.

**Mechanical prerequisite, recorded so it is not discovered late.** A v2
tag alone does nothing. `smeldr.dev` serves its `go-import` meta tag from
`site-dev`'s `main.go` (`vanityHandler`/`vanityHTML`), which declares the
module path as `smeldr.dev/{mod}`. For `go get smeldr.dev/core/v2` to
resolve at all, that tag must declare `smeldr.dev/core/v2` as its own
module path. That is a code change in a different repo, owned by a
different agent, requiring a deploy of smeldr.dev **before** any v2 tag is
usable by anyone.

### Rejected alternatives

**Go to v2 now.** Pays the permanent path cost immediately, and worse,
spends the stability statement while there is no audience to hear it. The
statement can only be made once.

**Return to v0**, which would be semver-honest about the current state.
Checked and unavailable: the proxy already holds v1 tags, and version
resolution prefers the highest, so a v0 tag would be inert. Recorded so the
next person does not re-derive it.

**Avoid the break and keep adding compatibility twins.** This is what
triggered the whole question. `SetStatus`/`SetStatusWithReason` and
`TransitionItem`/`TransitionItemWithReason` already exist; a planned third
reason-bearing operation would have added three more plus a parallel
interface, six twins in total, every one of them preserving a signature for
a caller that does not exist. The architect's own note at A256 said two is
a pattern and a third would be accretion; this decision is that note
honoured rather than overruled.

### Consequences

- The consolidation plan's single breaking pass
  (`architect/design/consolidation-plan.md`, wave 2) is authorised by this
  decision, and should be taken as one pass rather than one break per
  feature — the window is open now and closes without warning.
- T217's release check gains the importer lookup as a third item beside the
  clean-clone build and the pin-currency check. The versioning policy and
  the release check become the same mechanism.
- Any Amendment taking a breaking change under this decision must say so
  explicitly and cite D53, so the v1 series' own history shows where the
  breaks are.

### Addendum, 2026-08-14 — what version number a breaking change gets

The original decision settled that breaking changes are taken inside v1.
It did not say what version number one gets, and the first such change
(widening `MCPModule`, T237) surfaced the gap before it shipped.

**A breaking change inside v1 takes a MINOR bump, with `BREAKING` stated
plainly in the CHANGELOG entry and in the Amendment.**

Within v1, MINOR is the largest signal available. PATCH would be actively
misleading. MAJOR is already rejected above, because it forces a `/v2`
module path permanently to protect a population that does not exist.

**The version number cannot carry the truth inside v1.** That was the price
accepted when v2 was declined. So the truth goes where it can be carried:
the text a person actually reads before upgrading. This decision's own
existing requirement — that any breaking change cite D53 explicitly —
already puts the trail in the decisions record regardless of what the
number says.

**The honest limitation, stated rather than smoothed over:** a machine
reading only version numbers gets no warning. That is the accepted cost.
It is tolerable only because every consumer is ours and their pins move
deliberately, so no machine makes that decision unattended. **The day the
importer check finds an external module, this addendum expires with the
decision's first half** — from then on a breaking change requires v2, and
the question of what number it takes inside v1 stops existing.

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

