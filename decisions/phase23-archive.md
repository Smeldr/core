# Smeldr — Decisions Archive (Phase 23)

Archived from `decisions/recent.md` on 2026-08-09. Entries D40-D42, A241.

---

## D42 — Automation never crosses a role-gated transition; it stops and says so

### Scope

core

### Decision

When an automated path reaches a state transition that declares a
`RequiredRole`, it **does not perform the transition**. It leaves the item
where it is and records a `Signal` stating that a human holding the governing
role must act.

**The predicate is `RequiredRole != ""`, evaluated independently of `Strict`
and independently of whether governance is wired.** A transition that names a
role is closed to automation, full stop.

First applied in `App.DrainEvalQueue` (`state.go:748`), the timer-driven path
that transitions items whose `eval_at` has passed. It is the general rule for
every automated path, not a property of that one function.

### Why the obvious implementation is the wrong one

The natural move is to call the existing
`validateTransition(ctx, db, rs, "", typeName, from, to, "")` with an empty
`actorID` and let it decide. **It does not implement this rule**, and the
architect's own dispatch suggested trying it before core-implementer checked.

`state.go:440-445`: an empty `actorID` is treated as *pre-authorized* unless
the transition is `Strict`. A non-strict gated transition would let automation
straight through. The bug would not appear on any transition that exists
today, because D34's and D40's four gates are all `Strict: true`. It would
appear the first time someone registers a gated transition without `Strict`,
which is a reasonable thing to do and carries no warning that it opens a hole.

`Strict` answers a different question: whether an *interactive* caller, with a
real but unauthorized actor, fails closed or open when governance is not
wired. It says nothing about a caller with no actor at all. The two questions
were conflated because until now only one of them was ever asked.

The check therefore has its own narrow implementation that asks only "does this
transition name a role", never "is this absent actor authorized". Automation is
not trying to be someone. What it must not do is be no one and treat that as
sufficient.

### The Signal names the role that is actually required

The emitted `Signal` carries `receiver = <the transition's required_role>`, not
a hardcoded `"admin"`. `RequiredRole` is a free-form role name, so a fixed
`"admin"` would be a false statement about authority on any instance with a
custom role. "Which authority does this require" is also the field a human-
facing surface needs, so the record carries it rather than having it re-derived
from the transition row later.

`sender = "system"`, `signal_type = "authorization-required"`, `status =
"pending"` (`orchSignalFlow`'s initial state). Addressing the role rather than
an individual matches D38's `Run` reclaim convention: the model knows roles and
token bearers, never people.

### Alternatives considered and rejected

- **Automation is exempt, because authority was granted when the trigger was
  configured.** There is no authorization event to point at. `RegisterFlow` is
  Go code run at startup by whoever wrote `main.go`, not an audited act by an
  identified actor. `define_state_flow` is admin-gated and would be such an
  act, but nothing records who called it, so the drain has nothing to consult.
  This option asserts a chain of authority that does not exist on disk.
- **The trigger carries an actor, and automation acts as whoever configured
  it.** Closer, since `define_state_flow` does have a real actor to capture.
  Rejected on Article XI rather than on mechanics: configuring a trigger is a
  one-time act of setting up a rule, and a timer firing months later "as" that
  admin manufactures a fresh authorization event out of a stale one. The
  mechanics are also bad (no actor column on `TransitionTrigger` or
  `smeldr_eval_queue`, and an unanswered question about what happens to a
  queued row when the configuring admin's grant is revoked), but the
  constitutional objection stands on its own.
- **Mirror `validateTransition`'s non-strict carve-out, so the drain also
  skips the check when governance is unwired.** Rejected. Declaring a
  `RequiredRole` is the operator's stated intent about that transition;
  not having wired governance yet does not withdraw the declaration.

### Consequences

**A visible asymmetry on instances that declare roles without wiring
governance.** Where a flow sets `RequiredRole` and `App.Governance()` was never
called, `rs == nil` and an interactive caller passes on the non-strict path
(`state.go:433-439`), while automation now blocks and emits a `Signal`
addressed to a role that has no definition anywhere. Interactive passes,
automated stops, on the identical transition. This is stated rather than fixed:
a `Signal` nobody can act on is a visible defect in that instance's own setup,
which is the loud failure this decision is for.

**Nothing in production changes today.** The only transition the drain
currently reaches is entry into `pending-re-evaluation`, which D40 deliberately
leaves ungated. The rule exists so that the first gated target ever configured
fails loudly instead of silently succeeding.

**Blocked items are not deduplicated.** A blocked item's queue row is deleted
like any other, matching `DrainEvalQueue`'s existing "failed transitions are
not re-queued" rule. If the same eval arms again later, a second `Signal` is
raised rather than being matched against the unresolved first one.
Re-arm/dedup policy is orthogonal to who may authorize a crossing and is not
settled here.

**T211's observability half stays open.** The ungated path still applies its
transition through a raw `UPDATE` with no `ProvenanceRecord`, no `Signal`
dispatch and no cache invalidation. Closing it needs either an `App`-level
type-name-to-module registry or type-erased dispatch, both of which land in M5
territory. This decision covers who may cross a boundary, not what gets
recorded when one is crossed legitimately.

Status: Ratified 2026-08-09.

---

## D41 — A conclusion that forecloses an alternative is registered as a Decision, or it will be rediscovered

### Scope

process

### Decision

When work reaches a conclusion that rules out an alternative someone would
otherwise plausibly take, **the conclusion is registered as a `Decision`**. Not
the analysis it came from, not the conversation, just the conclusion and what it
forecloses.

This applies to conclusions from external sparring (GPT, Claude Design, Fable 5
reports) exactly as it applies to conclusions reached internally. The origin does
not change whether it is binding.

### The trigger, stated so it can be recognised in the moment rather than in hindsight

"Register the important things" is unusable, because importance is judged
afterwards. Four signals, any one of which is enough:

1. **You can name the alternative you rejected.** Every Decision from D33 to D40
   has the form *X, not Y*. If there is no Y, nothing was decided, something was
   described. This is the primary test.
2. **You catch yourself citing it as settled.** The moment you write "as we
   established" or "per the earlier conclusion" about something with no
   D-number, that thing is load-bearing and undocumented.
3. **It cost real effort to reach.** Multiple rounds, an external report, or a
   sparring pass that broke an earlier attempt. Rediscovering it will cost the
   same again.
4. **Someone arriving without it would plausibly do the opposite.** Not "would
   be confused", would actively go the other way.

### What not to register, so the register stays readable

Implementation choices the code already documents. Anything a reader would
derive from source in five minutes. Anything an existing Decision already
covers. Restating a conclusion in new words is worse than not restating it,
because the two versions then drift.

### Why this exists

**A worked example, 2026-08-09, and the reason this rule is not theoretical.**
An analysis on 2026-07-06 concluded that detection primitives stay free under
AGPL, that findings must be persisted deduplicated items rather than a
computation, and that a v1 surface should be a digest proving the sweep ran
rather than a standing inbox that looks dead when empty. Those conclusions were
recorded in `analysis/fable5-operational-insights-report-2026-07-06.md` and
inside a very long `T111` row in `ARCHITECT_TODO.md`. **Neither location is read
at session start.**

Five weeks later a full day of architect and external sparring re-derived most
of it independently, and got one part wrong that the earlier work had already
corrected: conflating semantic contradiction between decisions, which requires
reasoning Smeldr deliberately does not do, with mechanical competing authority,
which is deterministic and already implemented. The project had previously fixed
that exact conflation in marketing copy.

The information was on disk the whole time, in the same repository, and was not
found. `DECISIONS.md`'s index **is** read at session start, every time. A
D-number would have surfaced it in the first ten minutes.

### Consequences

The decisions register grows faster, deliberately. That is the cost, and it is
lower than the cost it replaces: `decisions/recent.md` has an archiving
mechanism that already works, and the index line is what does the finding, not
the body.

Architect holds decisions-write authority (D37), so architect registers these
without a separate dispatch.

**A retroactive pass over `analysis/` is warranted and is not part of this
decision.** Twenty-one documents exist there; an unknown number contain
conclusions that never became Decisions. Finding them is real work and deserves
its own session rather than being folded into whatever is running.

**The durable fix is not this rule.** It is M0 steps 4-9, where decisions and
tasks live in the running instance and an agent queries them instead of reading
markdown. This rule reduces the bleeding while that remains untouched. The irony
of the project's own coordination problem being the thing the product exists to
solve is noted rather than resolved.

Status: Ratified 2026-08-09.

---

## D40 — Ratification and supersession after re-evaluation are authority-bearing and gated the same as their direct counterparts

### Scope

core

### Decision

`orchDecisionFlow()`'s `pending-re-evaluation → ratified` and
`pending-re-evaluation → superseded` transitions get `RequiredRole: "admin"`
and `Strict: true`, matching `proposed → ratified` and `ratified → superseded`
exactly (D34, A234).

**The proposition being ruled on is about authority, not about any surface:**
ratifying a `Decision` is an authority-bearing act regardless of which state it
is entered from. D34 already established that for the direct path. Entering the
same state after a re-evaluation is the same act through a different door, and
nothing about having been reviewed first supplies the authority that the direct
path requires.

Article XI is the governing principle: *thinking does not establish authority*.
Re-evaluation is thinking. A model that lets `pending-re-evaluation → ratified`
proceed ungated is asserting in practice that having re-evaluated something is
itself sufficient to make it authoritative, which the constitution denies.

### How this was found, recorded because the order matters

Found while framing the Workspace surface
(`smeldr/architect/design/workspace.md` §13.7), by asking when the model
genuinely requires a human and checking the answer against source rather than
assuming it. `Decision`'s flow has seven transitions and exactly two were gated.

**Decided as governance, not as design.** The order is governance, then model,
then surface, never the reverse. Workspace's own filter excludes ungated
transitions, so before this decision the entire re-evaluation cycle would have
been invisible to it. That is a consequence of the ruling and was explicitly
not an argument for it: a surface wanting something to display is not a reason
to change who may do what.

### Consequences

Consumer-visible behaviour change on any governance-enabled instance, including
the live one at `process.smeldr.dev`. A `Decision` sitting in
`pending-re-evaluation` can no longer be returned to `ratified` or moved to
`superseded` without an `admin` grant. With `Strict: true`, a nil `RoleStore`
or a missing actor rejects rather than allowing the transition through, the
same fail-closed shape D34 established.

**Entry into `pending-re-evaluation` is deliberately left ungated.** Marking a
`Decision` as needing review is not an authority-bearing act; it is the model
observing that a premise moved. The `schedule-eval` trigger's own config
(`{"eval_field":"next_eval_at","to_state":"pending-re-evaluation"}`) targets
exactly that state, so the automated path in is unaffected by this decision.

**A limit worth stating rather than leaving to be discovered.** `DrainEvalQueue`
(`state.go:748`) applies its transitions with a raw SQL `UPDATE` and calls
neither `validateTransition` nor `RoleGranted` (tracked as T211). Today that
bypass reaches only `pending-re-evaluation`, which this decision leaves ungated,
so nothing is currently circumvented. But the bypass is structural rather than
scoped: any future trigger configured with a gated target state would route
around the gate entirely. This decision does not close that, and T211 becomes
more consequential now that more transitions are gated, not less.

### Alternatives considered and rejected

- **Leave it ungated and loosen Workspace's filter instead**, so the
  re-evaluation cycle becomes visible without a governance change. Rejected on
  the ordering principle above, and because it would have meant a surface
  quietly redefining what counts as an authority boundary.
- **Gate only `pending-re-evaluation → ratified`, leaving supersession open.**
  Rejected: `superseded` is equally authority-bearing, D34 already gates
  `ratified → superseded`, and an asymmetry here would recreate exactly the
  two-doors-one-lock problem this decision exists to close.

Status: Ratified 2026-08-09.

---

## A241 — D40's gate, and D42/T211's authority half

### Part 1 — D40

`orchDecisionFlow()`'s `pending-re-evaluation → ratified` and
`pending-re-evaluation → superseded` transitions gain `RequiredRole: "admin"`,
`Strict: true` — matching D34's own `proposed → ratified`/`ratified →
superseded` exactly.

`decisionRatifyModule` (`module_test.go`), the fixture A234's own tests
already use for a real `Module[*Decision]` with governance + `orchDecisionFlow`
wired against a real DB, took a hardcoded `"proposed"` seed status. Generalized
to `decisionModuleAtStatus(t, status string)`; its 3 existing callers now pass
`"proposed"` explicitly — confirmed by inspection, not assumed, that none of
them exercised any other status, so this is a pure generalization with no
behaviour change to the existing three tests.

4 new tests, reusing the generalized fixture seeded at
`"pending-re-evaluation"`, mirroring the existing D34 ratify tests' own
shape — a real `updateHandler` call, real `httptest.Recorder`, real
`RoleStore` grant/no-grant, not a struct-field assertion:
`TestModule_updateHandler_decisionReEvalRatify_forbidden`/`_authorized`,
`TestModule_updateHandler_decisionReEvalSupersede_forbidden`/`_authorized`.
`TestDecisionFlow_definition` extended (not just added to) to also find and
assert `RequiredRole`/`Strict` on both new transitions.

### Part 2 — D42, T211's authority half

Implements D42 in full — read D42's own body for the argued design decision
(the `Strict`-carve-out gap found in the naive `validateTransition(ctx, db,
rs, "", ...)` approach, the two rejected alternatives, and the stated
unwired-governance consequence); not restated here per D41's own rule against
restating a conclusion in new words.

`App.DrainEvalQueue` (`state.go`) now declines to apply any transition whose
target flow declares a `RequiredRole`. New `drainAuthorizationGate(ctx, db DB,
table, typeName, itemID, toState string) (fromState, requiredRole string, err
error)`: reads the item's live status directly — `smeldr_eval_queue` stores
only `to_state`, confirmed directly (the INSERT site, `state.go`) has no
`from_state` column, so the gate question cannot be answered without this
read — then the same two-step flow-id lookup `validateTransition` already
uses (type-specific, falling back to the seeded `"default"` flow), then reads
`required_role` for the specific `from→to` transition row. Returns the role
name itself, not a bool, so the caller never has to re-derive "which role" from
a separate lookup.

New `recordAuthorizationRequiredSignal(ctx, db DB, typeName, itemID, fromState,
toState, requiredRole string) error`: raw `INSERT INTO smeldr_signals` (`$N`
placeholders, this codebase's own convention — not `smeldr.dev/mcp`'s `?`
style), `sender="system"`, `receiver=requiredRole` (never a hardcoded role —
`RequiredRole` is free-form, and a fixed value would be a false statement about
authority on any instance with a custom role name), `signal_type=
"authorization-required"`, `status='pending'` (`orchSignalFlow`'s own initial
state).

`DrainEvalQueue`'s loop gains one branch: gate-check error → existing skip+log
treatment (a plain failure is not an authorization verdict, so it is not
conflated with one); role required → `recordAuthorizationRequiredSignal`
instead of the `UPDATE`, `skipped++`; not gated → the existing raw `UPDATE`,
byte-for-byte unchanged. The queue row is deleted unconditionally in every
case, matching the pre-existing "failed transitions are not re-queued" rule —
blocked transitions are not re-queued either.

**Two limits named here, deliberately not built here — architect review:**

1. **The gate is authorization, not validation.** `drainAuthorizationGate`
   returns "not gated" both when a transition is genuinely open and when the
   transition row simply doesn't exist (`sql.ErrNoRows`) — `validateTransition`
   treats that same missing-row case as `ErrConflict` (not permitted at all).
   Correct for the question this gate asks (no declared `required_role` means
   nothing to enforce), but it means the drain still applies a transition the
   flow never declared as legal in the first place — a different gap than the
   authority question this task answers. Declared-transition validity on the
   drain path belongs with T211's still-open observability half, not here.
2. **A `recordAuthorizationRequiredSignal` failure is the one way the loud
   failure goes quiet.** If the `Signal` insert itself fails, the item is not
   transitioned, no `Signal` exists, and the queue row is still deleted (a
   `slog.WarnContext` log line is the only trace). This is a real gap in D42's
   own argument — the whole point of a `Signal` over a log line is that it
   persists and is queryable — but keeping the queue row on this one failure
   mode would mean special-casing `DrainEvalQueue`'s existing, pre-existing
   "failed transitions are not re-queued" rule for a single branch, which is
   not this task's rule to revisit. Named so a future reader does not assume
   the `Signal` path is unconditionally reliable.

### Tests and coverage

15 new tests. 6 direct `drainAuthorizationGate` unit tests, one per branch:
status-read error, same-state short-circuit, no flow registered anywhere for
the type (genuinely exercised via `newSQLiteDB` rather than `newMigratedDB` —
`migrateStateFlows` always seeds a `"default"` flow, so a `newMigratedDB`-based
attempt at this case silently falls through to that default flow instead, a
real trap caught while writing the test, not assumed safe), flow found but no
matching transition row, transition found but ungated, transition found and
gated, and a transition-row query error. `recordAuthorizationRequiredSignal`:
one success test asserting the inserted row's exact fields, one INSERT-error
test.

`DrainEvalQueue`-level tests prove the wiring, not the mechanism, through the
real `App.DrainEvalQueue` entry point exclusively — per explicit architect
review criterion for this task: a gated transition blocks the item (status
unchanged), records exactly one `authorization-required` `Signal` addressed to
the declared role, and still deletes the queue row; a `recordAuthorizationRequiredSignal`
failure logs and continues rather than panicking or double-counting. Two
pre-existing `DrainEvalQueue` branches — the ungated path's own `UPDATE`
failure and the queue-row `DELETE` failure — lost their only prior test
coverage as a side effect of the new gate-check now running first (the
existing `TestDrainEvalQueue_transitionFail` fixture now fails at the
gate-check stage instead of at the `UPDATE`, same observable outcome, moved
cause — its comment updated to say so rather than leaving a stale claim in
place); given fresh, direct coverage instead via a new `nthExecFailDB` test
wrapper (mirroring the existing `nthQueryRowFailDB` pattern).

No exported symbols changed anywhere. Coverage: 96.2% package-wide;
`drainAuthorizationGate`, `recordAuthorizationRequiredSignal`, and
`DrainEvalQueue` all 100%. `go build`/`vet`/`gofmt`/`test`/`golangci-lint` all
clean.

### Coverage and versioning

Patch bump — consumer-observable behaviour change on any governance-enabled
instance (`PUT /decisions/{slug}` re-evaluation-door ratify/supersede now
requires `admin`; any instance with a `RequiredRole`-gated `DrainEvalQueue`
target now blocks automation on it instead of silently applying it), no new
exported symbols. v1.61.1 → v1.61.2.

Status: Implements D40, D42.

---
