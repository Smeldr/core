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

## A255 — decisions index/archive integrity sweep (T232)

### What was wrong

Two findings from T230's own investigation, same defect family:

1. `decisions/core.md` line 1274 had a mojibake character (U+FFFD, the
   Unicode replacement character) where an em dash should be, in
   `"## Decision 16 — Error handling model"` — a genuine encoding artifact,
   the `Set-Content`-without-`-Encoding utf8` class `CLAUDE.md`'s own
   environment notes warn about.
2. `A183`–`A190`'s own `DECISIONS.md` index rows all linked to
   `[recent.md]` despite being archived months earlier — the same
   completeness/pointer defect class A250/A252 each closed for their own
   cycle, older and wider here.

### Verified, not assumed — the range itself was wrong

Checked each of the eight individually against every archive file rather
than trusting the range implied by `recent.md`'s own old archive-log line
("Archived 2026-07-04: A184–A190 → phase12-archive.md"). That line was
accurate for A184–A190 — but **A183 is not part of that batch**: it was
archived earlier, into `phase11-archive.md`, and its own index row had
never been corrected either. A fix that trusted the reported range alone
would have written a second wrong link for A183 while believing it had
fixed all eight.

### Full mechanical sweep beyond the reported range — the task's own explicit instruction

Extracted all 114 rows in `DECISIONS.md`'s index and checked each one's
claimed link against whether the target file actually contains that
entry's real header. Result: no other row shares this defect — the eight
above are the complete set.

**Three different decision-header conventions have been used across this
project's history**, discovered while building the check itself: the
current `## D16`/`## A253`-style short form; an older, fully spelled-out
`## Decision 16`; and `## Amendment A215` with the full word. A sweep
pattern written against only the first two produces false positives on
the third (caught here against A215/A216/A217 before it could mislabel a
correct link as broken) — recorded here explicitly so a future sweep does
not have to rediscover it from scratch.

### The fix

`decisions/core.md`: one-character encoding fix. `DECISIONS.md`: A183's
row repointed to `phase11-archive.md`; A184–A190's seven rows repointed
to `phase12-archive.md`.

### Versioning

Docs only, no code, no exported symbols. Direct commit to `main`, matching
the decisions-file-family precedent this session already established
(A250/A252's own fix shape) — no branch. No version bump.

Status: Documentation-accuracy fix, no new Decision.

---

## A254 — ERROR_HANDLING.md documentation review (T230)

### What was stale

`ERROR_HANDLING.md` had not been reviewed since the project's first month.
Four findings from the dispatch, all independently verified against current
source rather than patched blindly (the dispatch's own explicit instruction,
naming D47/D48's hand-list defect class as what a blind patch would repeat):

1. **"The single pipeline rule" no longer true as written.** The doc knows
   only `WriteError` (HTTP). `smeldr.dev/mcp`'s `errorFor` (`tool.go`) is a
   second, independent error-rendering pipeline for JSON-RPC, unmentioned.
   Verified its actual body, not just its existence: it distinguishes
   exactly five conditions (`*ValidationError`/`ErrBadRequest` → `-32602`,
   `ErrNotFound`/`ErrForbidden`/`ErrConflict` → `-32001`, `ErrRevConflict` →
   `-32002`); every other sentinel falls through to the generic `-32603`
   bucket, indistinguishable there from a genuine internal error.
2. **9 of 12 sentinels listed.** Counted `errors.go`'s actual var block
   against the doc's Tier 1 code example — missing exactly `ErrRevConflict`,
   `ErrLastAdmin`, `ErrInternal`. Confirmed, no discrepancy from the
   dispatch's own count.
3. **Line 114's wrap example taught the retired `"forge:"` prefix.** Grepped
   the whole repository, not just this file, for the same defect class —
   the other 16 files matching `forge:` are either decision archives
   (correctly preserved historical text) or unrelated uses of the same
   substring (a struct-tag rename note, a URI scheme, test DB credentials).
   Nothing else repeats this specific pattern.
4. **D16 citation and the "Known gaps" table.** D16 is not in `DECISIONS.md`
   itself (index-only) — verified it lives in `decisions/core.md`, archived,
   under the pre-abbreviation header `## Decision 16 — Error handling model`
   (a first grep for `^## D16` missed it for exactly that formatting
   reason). The "Known gaps (fixed in v1.0.1)" table reads as current,
   neutral documentation with no framing that it is pure history — every
   row already ✅ Fixed, from the project's first month.

**Two more findings beyond the hand-list**, from re-reading the whole
document rather than only the listed items: Tier 3's own description ("any
error that is not a `smeldr.Error`...") is imprecise — a `smeldr.Error`
whose own `HTTPStatus() >= 500` gets the identical generic-500 treatment as
a fully untyped error, not exempted by satisfying the interface. Sentinel
construction re-verified accurate as written — no change, checked not
assumed.

### The fix

All in `ERROR_HANDLING.md`, docs-only: retitled the pipeline section to
scope it explicitly to HTTP and added the `errorFor` breakdown verbatim
(five sentinels named, the rest noted as falling to `-32603`); added the
three missing sentinels to the Tier 1 example; replaced the `forge:` wrap
example with `smeldr:`; fixed the Decision 16 citation to point at
`decisions/core.md` directly; marked the "Known gaps" table as historical
record in one sentence; added one clarifying sentence to Tier 3 on
`smeldr.Error`-with-5xx handling.

### Not fixed here, named instead

A real, tangential finding surfaced while chasing the D16 citation:
`decisions/core.md`'s own Decision 16 header (line 1274) has a mojibake
character where an em dash should be — a genuine encoding artifact, out of
this task's own file scope. Folded together with a wider, older version of
the same completeness gap A250/A252 each closed for their own cycle,
found while archiving `decisions/recent.md` in this task's own step 0:
`A183`–`A190`'s index rows still point at `[recent.md]` despite being
archived to `phase12-archive.md` back on 2026-07-04. Both become their own
follow-up (T232, architect registers and dispatches later) rather than
expanding this docs-only cycle's scope. A third, separate observation from
the architect's own plan review — `errorFor`'s coverage gap for
`ErrUnauth` specifically (falls to `-32603` like every unmapped sentinel,
but unauthenticated requests are common enough over MCP that the gap may
be worth its own decision) — is deferred to a future decision, not
this cycle.

### Versioning

No exported symbols, no behaviour change — pure documentation accuracy.
No version bump, no `CHANGELOG.md` entry (`ERROR_HANDLING.md` is not part
of `smeldr.dev/core`'s own consumer-observable surface). Level 1 amendment.

Status: Implements no new Decision — a documentation-accuracy fix against
Decision 16's own existing contract.

---

## D49 — Transition authority for compiled types lives on App, not on the MCPModule interface

### Scope

core (App.TransitionItem), mcp (transition_item dispatch)

### Decision

Flow-validated state transitions for compiled types are performed by a
new `App.TransitionItem(ctx, typeName, slug, toState)` — resolving the
item's table via `resolveItemTable`, validating via the same
`validateTransition` call the REST path (`updateHandler`) already makes,
and persisting via the file's established raw status-UPDATE pattern
(`setStatus`/`conflictSupersede`'s own precedent). The dynamic-content
branch delegates to `DynamicContentRepo` unchanged.

### Rejected alternative

A new required `MCPTransition(ctx, slug, toState)` method on the exported
`MCPModule` interface, implemented generically on `Module[T]` with typed
`repo.Save` (rev-CAS preserved). Rejected on the API stability promise:
Go interface satisfaction has no default-method escape, so a new required
method breaks every `MCPModule` implementer that is not `Module[T]`
itself. The benefit it would buy — typed rev-CAS on a status-only write —
is one the codebase's existing status-write paths already forgo
deliberately; declining it here is consistency, not new risk.

### Boundary

The raw status-UPDATE's `rev` behaviour must be stated explicitly in the
implementing Amendment's own text, whichever it is — it borders T227's
territory (rev echo semantics), and an unstated rev semantic is the kind
of gap that gets rediscovered expensively.

### Status

Ratified 2026-08-11 (Peter, via architect session). Implementation: the
compiled-state-tools cycle.

---

## A253 — SQLRepo.Save writes back the caller's own struct (T227)

### The bug

`Save` (`storage.go`) built a separate addressable copy (`cp`/`rv`) for the
DB write. `UpdatedAt`/`CreatedAt` were written into that copy; the SQL
incremented `rev` in the database (`SET rev = table.rev + 1`); the CAS
guard was checked via `RowsAffected()`. None of it — not the incremented
`rev`, not even `UpdatedAt` — was ever written back into the caller's own
struct. A successful `Save` call left `item` completely untouched.

Confirmed live, not hypothetical: `PUT /signals/{slug}` on
`process.smeldr.dev` during M0 step 6 responded `"Rev": 0`; a re-read
immediately after showed `"Rev": 1`. `smeldr/runner`'s own `client_test.go`
(M3 runner build) hit the identical thing from `update_run`.

**Prior art already in this codebase, now closed rather than worked
around again.** A226 fixed a *symptom* of this exact root cause —
`updateHandler` called `Save` twice on a publish-via-PUT, and the second
call's CAS failed because the first call's rev increment was never echoed
back — by removing the second `Save` entirely, not by fixing `Save`'s own
contract. D38 (`orchestration.go`, `Run`'s own doc comment) named this
precise gap as future work three weeks ago, deliberately left unbuilt: "a
write whose payload omits rev has MCPUpdate silently seed the row's
current value instead... degrading claims/renewals to last-write-wins,
indistinguishably from correct behaviour under any single-threaded test
(D38 §3)." That entry is explicit the SQLRepo-level echo did not exist
yet. This task builds it.

### Write-back to the passed pointer, not a changed return shape

`NewSQLRepo`'s own doc comment already documents the convention that `T`
is a pointer type — matching the proto passed to `NewModule`. When that
convention is followed, `item`'s underlying struct is already addressable
the moment `Save` dereferences it, before today's code ever copied it
away into `cp`. Writing into that same value directly needs no new
capability, only for `Save` to stop copying away from the value that
matters. A changed return shape would have been a breaking signature
change to an exported method used directly by every module handler in
`smeldr/core` itself, by every standalone repo that embeds
`smeldr.dev/core`, and by any external caller using `SQLRepo` directly for
a custom type — for zero benefit the write-back approach doesn't already
give. When `item` is not the conventional addressable shape (a value-type
`T`, already discouraged by `NewSQLRepo`'s own doc comment), write-back is
a guarded no-op via `src.CanSet()`: the write still succeeds, only the
echo is skipped, identical to prior silent behaviour for that
already-discouraged case. No new panic risk.

### Scope: rev, and the identical UpdatedAt/CreatedAt defect

The task named `rev` as the observed symptom, but the exact same
copy-only defect affects `UpdatedAt` and `CreatedAt` for the identical
reason — they are written into `rv`, never into `src`. Fixing only `rev`
while leaving the other two half-fixed in the same function, in the same
commit, would have repeated A226's own shape: symptom fixed, contract
untouched, inside the very task that exists to end it. All three are
fixed together. `UpdatedAt`/`CreatedAt` write-back needed no round trip —
their correct value is already known in Go before the query runs (`now`,
computed once) — so it is a plain field copy from `rv` to `src`, gated
only on `src.CanSet()`, factored into a new unexported
`writeBackTimestamps(src, rv reflect.Value, updatedAtPath, createdAtPath
[]int)`.

### The rev echo itself: RETURNING, one round trip, not a guess

`rev` is different from the timestamps: its correct new value is owned by
the database (`SET rev = table.rev + 1`), unknowable in Go ahead of time.
Fix: append `RETURNING rev` to the same statement and use
`QueryRowContext` instead of `ExecContext` — the database computes and
returns the authoritative value in the *same* round trip, whether the
branch taken was a fresh INSERT (no conflict — the `rev` returned is
whatever was inserted, per `Node.Rev`'s own "on first insert Rev = 0"
contract) or the CAS-guarded `DO UPDATE` (`rev` returned is `old+1`).
This is the direct alternative to the "guaranteed extra read roundtrip"
the task itself named as today's cost — `TestSQLRepo_Save_RevIncrements`
literally did this extra read before this fix and no longer needs to.

CAS-conflict detection moves from `RowsAffected() == 0` to
`errors.Is(err, sql.ErrNoRows)` on the `Scan` — `QueryRowContext` returns
no row when the `WHERE`-guarded `DO UPDATE` didn't fire, exactly
mirroring `RowsAffected() == 0` before. Still maps to the existing
`ErrRevConflict` sentinel — no new sentinel, per `ERROR_HANDLING.md`'s own
criteria (an existing named 4xx condition, not a new one).

First use of `RETURNING` anywhere in `smeldr/core`. Safe on both backends:
SQLite (`modernc.org/sqlite v1.50.0`, RETURNING supported since SQLite
3.35) and PostgreSQL (native, long-standing). `smeldr.DB`'s
`QueryRowContext` is already the method used for every other single-row
read in the codebase (`auth.go`) — no new interface surface. Verification
relies on the SQLite-backed tests; `pgx/pgx_integration_test.go` is gated
behind `-tags integration` plus a live `DATABASE_URL`, unavailable in this
environment — the architect asked that the pgx-integration CI job be
watched explicitly on push rather than concluding green from the SQLite
suite alone (A213's own lesson).

### Two tests that passed because of the bug, not in spite of it

`TestSQLRepo_Save_RevConflict` and `TestRun_SaveRevConflict` both relied
on the caller's own `Rev` field staying stale across `Save` calls to
manufacture their conflict scenario: a second `Save` on the same `item`
"happened" to still carry the pre-increment rev, matching the stored rev
by coincidence of the bug rather than by test design. Once `Save` writes
back, that second call correctly advances `item.Rev`, and the tests'
own third call would then succeed instead of conflicting — their premise
broke the moment the fix landed, confirmed directly (`go test` failed
exactly these two, nothing else, before the rewrite). Both rewritten to
hold a deliberately-separate, deliberately-never-advanced stale copy
instead of relying on the caller's own struct to go stale by omission —
each now also directly asserts the write-back (`item.Rev == 1` after the
second save), proving the fix, not just the unbroken conflict path.

### Doc comments

`Node.Rev` (`node.go`) gained one sentence: the first goroutine's own
struct now reflects the real post-save `Rev` with no re-read required.
`Run`'s own doc comment (`orchestration.go`) gained one sentence
distinguishing this task's fix (a lower layer — whether `Save` tells its
immediate Go caller the truth at all) from D38 §3's own, still entirely
open concern (a future M3 listener that omits `rev` from its own MCP
update payload). Neither the D38 §3 claim nor its scope was narrowed or
removed.

### Tests

5 new: `TestSQLRepo_Save_WritesBackTimestamps`, `TestSQLRepo_Save_
update_returningRev`, `TestSQLRepo_Save_RevConflict_fakeDriver`,
`TestSQLRepo_Save_ValueType_NoPanic` (a value-type `T` with a `rev`
column does not panic on write-back — the `CanSet()`-guarded skip path,
previously untested since no existing fixture combined "has rev" with
"used as a value type"), `TestSQLRepo_Save_execError_noRev` (reuses the
existing `errExecDB` fixture from `auth_test.go` rather than a new one).
`TestSQLRepo_Save_RevIncrements` extended with direct `item.Rev`
assertions after each `Save` call, proving no re-read is needed through
the whole sequence, alongside its existing `FindByID` DB-truth checks.

### Versioning

No exported symbols changed — `Save(ctx, item T) error`'s signature is
identical. Level 2 despite that: a real behaviour change to a
widely-used exported method with consequences in more than one file (see
above). Coverage: 96.3% package-wide; `Save` itself 100% (the one
previously-missing branch, the plain-`Exec` error path for a type with no
`rev` column, closed with `TestSQLRepo_Save_execError_noRev`). Patch bump
— matches A226/A233/A241's own precedent for a real, consumer-observable
behaviour fix with no new API surface. v1.64.0 → v1.64.1.

Status: Implements no new Decision — a direct bugfix to an existing,
long-standing contract gap A226 and D38 §3 had each already named.

---

## A252 — transition_item/get_valid_transitions/list_items_by_state operate on compiled types (D49, smeldr.dev/mcp half)

### What shipped

`transition_item` now calls the new `smeldr.dev/core` `App.TransitionItem` (A251) instead
of cleanly rejecting every compiled type, the actual capability A251's core-side work
exists to expose. `get_valid_transitions` and `list_items_by_state` need no new core API:
both resolve a compiled type's current status/listing through its own module's already-
exported, type-erased `MCPGet`/`MCPList` — status is read from the marshaled JSON result
(`Node.Status` carries no `json` tag, so it marshals under its own field name; no
reflection required). Role-gated transitions (D34/D40) behave identically through this
path to the REST path, since both ultimately reach the same `validateTransition` call.

### errorFor gains a mapping it always should have had

`validateTransition`'s `RequiredReason` branch has returned `ErrBadRequest` since T149/A220
with no JSON-RPC code mapping anywhere in `errorFor` — every call fell through to the
generic `-32603`. Closed as a direct side effect of wiring `App.TransitionItem`'s own "type
not registered" error through the same path: `errorFor` now maps `smeldr.ErrBadRequest` →
`-32602`, the same code `ValidationError` already uses.

### Versioning

Requires `smeldr.dev/core` v1.64.0+ (`App.TransitionItem`) — go.mod bumped only after
core's tag was proxy-verified, matching A243's own precedent, not a `go.work`-local
override. MINOR bump — new consumer-visible tool capability, three previously-clean-
rejecting tools now actually operate on compiled types. v1.30.1 → v1.30.2.

Status: Implements D49 (paired with A251, smeldr/core half — see that entry for the full
design rationale, not restated here).

### Note

This entry backfills a completeness gap: `mcp/CHANGELOG.md`'s own `[1.30.2]` section and
A251's own text both already cited "A252" at the time of that release, but the index row
and this body were never written — the same class of gap A250 itself named and fixed one
cycle earlier. Found and fixed during T227's plan review (2026-08-11), before T227's own
Amendment number could be correctly assigned.

---

## D48 — A generated tool's authority requirement is derived from its structure, never enumerated by hand

### Scope

mcp (mechanism), core (seed rows + documentation)

### Decision

When `smeldr_tool_policies` has no row for a tool name, the required
operation is **derived from the tool's own verb prefix** — `get`/`list` →
`read`, `create` → `create`, `update` → `update`, `publish`/`schedule` →
`publish`, `archive` → `archive`, `delete` → `delete` — **if and only if a
real registered module backs the parsed type name** (`moduleForType`, or
`moduleForAdminList` for list-tools' plural names). Otherwise denial stands
exactly as before.

**An explicit policy row always wins over derivation.** Derivation is a
default, not an override: an operator who writes a row for a specific tool
changes that tool's requirement, and nothing re-derives it.

### What this settles

T224: `seedToolPolicies` is a hand-maintained list covering the four
built-in content types and a set of framework tools. The six orchestration
types' generated per-type tools had no rows, and `authoriseTool`'s
fail-closed not-found branch therefore denied every one of them — verified
live on `process.smeldr.dev` with a token holding a real admin grant.
`create_signal` worked and `get_signal` did not, for no reason an operator
could inspect. M0 step 7 was blocked on it.

The deeper defect is the list itself. A hand-enumerated authority surface
is correct only for what was enumerated at the time — the exact class D47
closed for the sweep's `TargetChecker`, and this same list was already
patched once this week (A242's grant tools, whose own comment warns that an
unpolicied tool is denied for everyone). A seventh orchestration type, or
any new compiled module, would silently reopen the hole.

### Rejected alternatives

- **Seed rows per generated tool, per deployment.** Correct today, wrong
  the day the next type lands, and each patch is another chance to miss
  one. Rejected on D47's own argument, not on cost.
- **Missing row means allow.** Fails Article I outright — an unknown tool
  name would acquire authority nobody granted.
- **Derivation inside core's `RoleStore.ToolPolicy`.** `parseToolName` and
  the module registry are mcp-side knowledge; core cannot confirm a real
  module backs a type name. The fallback lives in `mcp`'s `authoriseTool`
  path, where that confirmation is possible — an unknown or misspelled
  name still fails closed because no module confirms it.

### Boundary

`manage`, `administer`, `define-type`, `define-flow` and
`define-relation-kind` have no generated verb form and are **never
derived** — every tool requiring them keeps an explicit row.
`get_goal_context` and `list_type_tools` are framework tools, not generated
ones, and get explicit `read` rows in the same change.

The derivation rule is documented in governance-model.md §4 in the same
commit that ships it. A requirement an operator cannot read about anywhere
is silent authority, which is the failure mode this project keeps finding
elsewhere (T219, T223).

### Status

Ratified 2026-08-10 (Peter, via architect session). Implementation: T224's
cycle, smeldr.dev/mcp.

---

