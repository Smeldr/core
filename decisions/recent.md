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
