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
---

## D47 — A structural sweep gets a structural answer: for a compiled type, alive means the row exists

### Scope

core

### Decision

`SweepStructural`'s `TargetChecker` asks whether the thing an edge points at
still exists. For any compiled `Module` type, **alive means the row is still
there. No status is ever consulted.**

Not `superseded`, not `archived`, not `expired`, not any future terminal
state. A hard delete is the only thing that makes a target gone.

For a runtime-defined content type the existing rule is unchanged: alive means
`status = 'published'`.

### The principle, which is why this holds for types that do not exist yet

**A status is a semantic state. Existence is a structural fact. Answering a
structural question with a semantic test is a category error whatever status
is chosen.**

Stated this way deliberately, rather than as a table of the six current types.
A per-type enumeration would have to be revisited every time a seventh type or
a new terminal state is added, and each revisit is another chance to get it
wrong. The principle survives both.

It also dissolves a doubt rather than accepting it. Core-implementer proposed
the right rule but grounded it in an observation, that none of the six flows'
terminal states happen to mean "this never occurred", and was therefore least
confident about `Signal`'s `expired`, which reads closer to "no longer
relevant" than the others. Under the principle, `expired` is not a harder case
than `superseded`. It is the same case.

### What the wrong answer costs, in each direction

**Treating a terminal status as gone destroys history.** A `supersedes` edge
pointing at a superseded `Decision` is the record of the supersession itself.
Invalidating it deletes the evidence of the very act it documents. Archiving
is bookkeeping, not retraction, and a `derives_from` edge that breaks because
someone tidied up is a lost lineage.

**Answering "alive" unconditionally is equally wrong.** A checker that never
returns false makes the sweep incapable of detecting anything, which is the
state the whole exercise exists to leave. Hard delete is real and reachable
through the generic delete tool, so this rule still has a false to return.

### The hazard this closes, and the one it opens sideways

`App.SweepStructural`'s built-in checker previously queried only
`smeldr_dynamic_content` and returned false when it found no row. Every
compiled type would have answered false. **On an orchestration instance the
first run would have invalidated the entire lineage graph.** The behaviour was
documented in the function's own godoc, which said compiled types need a
custom checker; the defect was that nothing enforced it and an architect task
said "wire the detectors" without naming it.

`resolveItemTable` falls back to `smeldr_dynamic_content` whenever it finds no
dedicated table, which is correct for a genuine dynamic type and
indistinguishable from a compiled type whose table is simply absent: a
partially migrated instance, or a consumer that stopped registering a module.
Both would have reached the status lookup, found no row, and returned false.
The original hazard, reached sideways.

**Closed by requiring positive evidence rather than inferring from absence.**
Before the dynamic branch is trusted at all, the type must be confirmed as
registered in `smeldr_content_type_schemas`. When it is not, or when that
registry cannot be queried, the checker returns an **error rather than a
verdict**. `SweepStructural` counts an error as skipped and leaves the edge
untouched.

> A sweep that cannot tell whether a target exists must decline, never delete.

### Alternatives considered and rejected

- **Archived, superseded or expired means not alive.** The natural reading,
  and what the existing dynamic-content rule implies by analogy. Rejected: it
  destroys history, and it is the answer someone would arrive at by extending
  the old behaviour rather than by asking what the sweep is for.
- **A per-type table of what alive means.** Correct today and wrong on the
  day a seventh type lands.
- **Leaving the fix opt-in**, as a constructor or as `example/server` wiring.
  Rejected: an opt-in does not reach the consumer who does not know they are
  at risk, which is precisely the population this protects.

### Consequences

**Consumer-visible behaviour change on any instance with compiled types.**
Previously the default checker invalidated all their edges; now it does not.
Anyone relying on the old behaviour was relying on a bug.

**One caller-facing case survives and is documented.** An application whose
own compiled types genuinely do treat some status as equivalent to deletion,
unlike every built-in orchestration flow, must still supply its own
`TargetChecker`.

**This does not schedule anything.** The sweep still has no caller. Wiring it
is separate work with its own open questions about what a run must record.

Status: Ratified 2026-08-10.

---

## D46 — A finding and a condition are different objects, and suppressing a finding is a Decision rather than a button

### Scope

core, process

### Decision

**A finding is what a detector produces.** An observation about the model:
this edge points at something that is gone. It belongs to the detector and is
a record of what was seen and when.

**A condition is what requires a human.** Something previously settled can no
longer be safely treated as settled, and an explicit transition that requires
a role would resolve it.

They are not the same object and are governed differently:

- **Findings are persisted, deduplicated and stateful.** Dedup key: detector
  type, subject item ID, finding fingerprint. **A finding resolves when its
  detector stops firing on the subject**, not when a human says it is fixed.
- **Conditions are projections and own no state.** No seen, snoozed,
  dismissed, claimed or acknowledged. The surface cannot be made to lie
  because there is nothing on it that hides anything.

**Suppressing a finding that is still firing is not a surface action. It is
registering a `Decision`.** The act being performed is *the model detects this
and we have ruled that it stands anyway*, which is an authority-bearing
statement with an actor, a time and an audit record. It is reversible by
supersession like any other decision, and it answers "why is this not shown"
with a ratified statement rather than with a button someone pressed.

The finding continues to fire. It should. The detector is still right about
what it sees.

### What this reconciles

`analysis/fable5-operational-insights-report-2026-07-06.md` §2.1a argued that
findings must be persisted, deduplicated, stateful records, because otherwise
the same stale relation resurfaces every sweep and an Ignore action means
nothing. It proposed a flow of `open → acknowledged → delegated → resolved /
dismissed / accepted-as-is`.

`design/workspace.md` §13.5 held the opposite: the surface owns no state, and
there is deliberately no dismiss action, because one would let the surface
lie.

**Both are right, about different objects.** The analysis is describing
findings; Workspace is describing conditions. The apparent contradiction was
two documents using one word for two things.

The analysis is also internally in tension on exactly this point, which is the
tell: its §2.1d says a finding is resolved when *its detector no longer fires*,
"not the human's claim that they fixed it", while its own card actions offer
Ignore and Mark accepted. This decision keeps §2.1d and discards the buttons.

### Only one of Workspace's three provenances is a finding

Worth stating, because the word invites the assumption that all three are:

- **Detected** is a finding. A detector produced it.
- **Asserted** is not. Nothing found it; an agent outside Smeldr reasoned and
  wrote a claim in.
- **Scheduled** is not. Nothing was discovered; a date that a person set
  arrived.

So persistence, dedup and detector-verified resolution apply to one of the
three. What the other two require is open and is not settled here.

### Alternatives considered and rejected

- **A dismiss or ignore action on the surface.** The analysis's own card
  design, and the natural thing to build. Rejected: it makes the surface
  capable of hiding a condition the model still holds, which is the single
  property Workspace exists to guarantee. It also records nothing about who
  decided or why.
- **Conditions carry state, and findings are computed at read time.** The
  inverse assignment, and it fails on the analysis's own argument: a
  recomputed finding cannot be deduplicated across runs, so the same stale
  relation resurfaces every sweep.
- **No suppression at all.** Coherent, and it was the reading Workspace's
  no-state rule implied on its own. Rejected because the case is real: a
  finding can be correct and the organization can still have ruled that the
  situation stands. Refusing to model that does not remove it; it moves it
  out of the system into someone's memory.

### Consequences

**A `Decision` acquires a second job.** Alongside recording what an
organization decided, it becomes the mechanism by which a detected finding is
overruled. That is a widening of what `Decision` means and it is deliberate.

**The mechanism by which a suppressing `Decision` is linked to the finding it
overrules is not designed here.** A relation kind is the obvious shape, and
D45 already added two for the Workspace surface, but which kind and in which
direction is open.

**Workspace's condition filter gains a case it does not have today**: a
condition whose finding is still firing but which a ratified `Decision` has
settled must stop appearing. The criterion is unchanged, an explicit
authorized transition resolved it, but the transition is on the `Decision`
rather than on the subject.

**This decision does not settle whether `Insight` (T126) is the persisted
finding type**, nor whether the six existing orchestration modules are the
right pattern for it. It settles what the object is, not what it is called or
where it lives.

### How this was reached, recorded because the question mattered more than the answer

Architect framed the blocking question as "are conditions persisted items",
and spent a day citing the July analysis from a summary without reading it.
Peter asked what a finding actually is. **The distinction dissolved the
conflict rather than resolving it**, and the suppression mechanism followed
from it directly rather than being negotiated.

Status: Ratified 2026-08-10.

---

## D45 — Two relation kinds complete the Workspace model: `investigates` and `contradicts`

### Scope

core

### Decision

`RegisterOrchestrationRelationKinds` gains two kinds beside D36's four:

| TypeName | Pair | Mode | Directional | Weighted |
|---|---|---|---|---|
| `investigates` | Task to Decision | `asserted` | true | false |
| `contradicts` | Decision to Decision | `inferable` | **false** | **true** |

`investigates` carries delegation: Workspace may originate model work without
owning the work. Creating an investigation is not assigning a person, and the
`Task` is real model state rather than surface state.

`contradicts` is the vehicle for semantic contradiction. **Declaring it does
not make Smeldr detect anything.** It gives reasoning performed elsewhere a
declared shape to arrive in.

### Why `contradicts` is the more consequential of the two

`design/workspace.md` §13.9e establishes that a Workspace condition has one of
three provenances: detected, asserted, or scheduled. Detected and scheduled
both have live mechanisms. **Asserted had no vehicle at all**, which made an
entire third of the model theoretical.

An agent reasons outside Smeldr, concludes that two `Decision`s are
incompatible, and writes that conclusion in as an edge. `edge_class` records
whether a human asserted it or an agent inferred it; `confidence` records how
strongly. Workspace then projects the consequence of a claim the model
already holds.

The governing line, which this decision must not be read as weakening:

> Reasoning may enter the model as a claim. Workspace may expose its
> consequences. Workspace may never perform the reasoning.

### The three field choices, each defended separately

**`Mode: "inferable"`, and it is the first.** D36's four kinds are all
`asserted`, because each records something someone established. `inferable`
says this is a kind an agent may reasonably infer rather than one that must be
stated. That is exactly the boundary where reasoning enters, and marking it in
the model rather than only in a design document is the point.

`Mode` is not `edge_class`. `Mode` describes what the kind permits;
`edge_class` records how one particular edge actually arrived. A236 established
that `edge_class` is set by which of `MCPAssertRelation`/`MCPProposeRelation`/
`MCPObserveRelation` is called, independent of the kind's `Mode`. Both are
needed and neither implies the other.

**`Directional: false`, also the first.** Contradiction is symmetric: if A
contradicts B then B contradicts A. A direction would be a fact the model does
not have, and would make traversal order meaningful when it is not.

**`Weighted: true`, and stated honestly as documentation.** Verified against
source rather than assumed: `Weighted` is written by `UpsertKind`
(`relations.go:260`) and read back (`:575`) and **consulted nowhere**. Like
`TypePairs` per A236, it documents rather than enforces. `Confidence` is a
free `*float64` on every edge regardless. Setting it true records that
confidence is meaningful for this kind, and nobody should later assume it
gates anything.

### Alternatives considered and rejected

- **Do not declare `contradicts`; keep contradiction out of the model
  entirely.** Defensible on the ground that Smeldr must never reason. Rejected
  because it confuses detecting with receiving: refusing the shape does not
  make the project more honest, it makes an entire provenance unreachable
  while the design document continues to claim it.
- **Make `contradicts` directional.** Rejected: it would encode a direction
  the model has no basis for, and invite a reader to infer that the source
  somehow contradicts *more*.
- **Reuse `supersedes` for contradiction.** Rejected, and this is the one
  worth stating at length. Supersession is a decision someone made: one thing
  now stands in place of another. Contradiction is a claim about two things
  that both still stand. Collapsing them would destroy precisely the
  distinction Workspace exists to surface, since a superseded `Decision` needs
  no human and a contradicted pair is the case that does.

### Consequences

**Declaring a kind is not a capability.** After this ships, an instance can
hold a contradiction claim. Nothing in Smeldr produces one, and nothing
should. The first writer will be an agent outside the framework.

**Nothing writes either kind yet.** `investigates` is written when Workspace's
delegation action is built; `contradicts` when an external agent asserts one.
Both are declared ahead of their writers deliberately, so the surface can be
designed against a real model rather than a promised one.

**`RegisterOrchestrationRelationKinds` and its documentation say "four" in
several places.** Six now.

**`TypePairs` remains documentation-only** (A236), so both kinds are writable
outside their declared pairs. That is the existing behaviour of every kind and
is not changed here.

Status: Ratified 2026-08-09.

---

## D44 — An authority mutation always leaves a record, and the record is not optional

### Scope

core

### Decision

Every change to **who may do what** is recorded in `smeldr_governance_audit`:
granting a role, revoking a grant, defining or redefining a role. Actor,
action, target, before, after, time.

**The record is not separately enabled.** Wiring governance wires audit.
`App.Governance` creates the audit table and holds the store, the same way it
already holds the `RoleStore`. There is no option to turn it off and no way to
reach a governance-enabled instance that has no audit trail.

The actor recorded is the JWT's `User.ID`, per D43, which is the same value
`Authorized` and `RoleGranted` check against.

### Why this is not an implementation detail

D43 removed the implicit bridge between `smeldr_tokens.role` and governance
grants, on the ground that granting must be an act with an actor, a target and
a time rather than a string match at boot. **That argument depends entirely on
the record existing.** An opt-in audit trail means an operator can wire
governance without it, and granting is once again an act nobody can point at
afterward.

Making the record optional makes the decision optional. Article I says
authority never changes silently; a change that leaves no trace is silent
whatever ceremony surrounded it at the time.

### The boundary against provenance, stated so the two are not conflated

This decision covers mutations of the **authority model**. It does not cover
acts *performed under* that authority.

A state transition is recorded through `ProvenanceRecord` (`Verb`,
`FromState`, `ToState`, `ActorKind`, `ActorID`, `Surface`, `Reason`), which is
a different mechanism answering a different question. Governance audit answers
"who was given the right to do this". Provenance answers "who then did it".

Both are required. Neither substitutes for the other, and a gap in one must
never be argued away by pointing at the other.

### Alternatives considered and rejected

- **Audit as an opt-in `ServerOption` on the MCP server.** Proposed by
  core-implementer while planning T216, and reasonable on its own terms: it
  matches how every other optional subsystem in this codebase is wired, and a
  library normally lets its consumer choose. Rejected because the thing being
  made optional is not a subsystem, it is the evidence that a decision
  happened.
- **Audit wired in `example/server` only.** Would have made the live instance
  correct and left every other deployment to remember. The failure mode this
  whole task exists to fix was something correct in principle that nobody had
  actually wired.

### Consequences

**Enabling governance now creates a table that did not exist before on that
instance.** `CreateGovernanceAuditTable` is idempotent and documented as safe
on every startup, so this is a no-op on second boot, but it is a
consumer-visible change and belongs in the CHANGELOG.

**A deployment cannot have governance without audit.** That is the point, and
it is also the cost: a consumer who wanted role gating and no audit table no
longer has that combination.

**`RoleStore.WithAudit` keeps its per-request shape.** It bakes a fixed actor
into the store it returns, so the actor is attached per call rather than at
startup. This decision does not change that and must not be read as
permission to wire a single shared actor once.

Status: Ratified 2026-08-09.

---

## D43 — Creating a token confers no governance role; granting is a separate, explicit act

### Scope

core, mcp

### Decision

`smeldr_tokens.role` and `smeldr_role_grants` are **two authority models, and
nothing silently bridges them.** Creating a bearer token with `role: "admin"`
gives it the legacy `Role`-hierarchy standing that predates governance. It
confers no governance grant. An administrator grants a governance role as its
own act.

Three consequences follow directly:

- **`migrateTokenGrants` is removed** (`governance.go:283`), not repaired.
- **`RoleStore.Grant`, `ListGrants` and a revoke counterpart get an MCP
  surface**, admin-gated under the `administer` operation word, the same as
  `create_token`.
- **The bootstrap path stays exactly as A237 built it** (`smeldr.go:1328`), as
  the single deliberate exception. An instance whose first token holds no
  grant cannot be administered by anyone, which is not a governance model, it
  is a locked door.

### The defect this settles, stated so it is recognisable rather than abstract

A token has two identities:

- `smeldr_tokens.id` is the SHA-256 fingerprint of the raw token string
  (`auth.go:452-453`).
- `User.ID` is a `NewID()` embedded in the JWT at signing time
  (`auth.go:447`).

`createToken`'s own doc comment states the distinction. `VerifyBearerToken`
returns the decoded JWT, so `ctx.User().ID` is the second one; the fingerprint
is only used for the revocation lookup (`auth.go:300-313`). `authoriseTool`
authorizes on `ctx.User().ID` (`mcp/tool.go:89`), and `RoleGranted` and
`Authorized` query `smeldr_role_grants.token_id`.

`migrateTokenGrants` writes the **fingerprint** into that column
(`governance.go:284`, `:329`). The two values can never match. Every grant
that migration has produced is unreachable.

**The diagnostic, so this is never re-derived:** a fingerprint is 64
hexadecimal characters and a `User.ID` is not. Read
`smeldr_role_grants.token_id` and you can see which kind it holds.

**Why it went unnoticed.** A237 fixed the bootstrap token by granting against
the JWT `User.ID` directly, and its own comment names itself "a break-glass
safety net, not a substitute for provisioning a real operator's own token".
Provisioning a real operator's own token was the first thing to exercise the
broken path, on `process.smeldr.dev`, five weeks later.

### Why this is a decision about authority and not a bug fix

The migration could be made to work. Storing `user_id` as a column on
`smeldr_tokens` would do it, and it is less work than this.

The objection is Article I: **authority never changes silently.** A migration
that converts a column into a governance grant, at boot, for every token, with
no actor and no audit record, is that silence. It grants authority because a
string matched a role name. Nobody decided anything, and nothing records that
anybody did.

Removing the bridge makes granting an act with an actor, a target and a time.
That is what `RoleStore.Grant` already is; it simply had no way to be called.

### Alternatives considered and rejected

- **Store `user_id` as a new column on `smeldr_tokens` and key grants on it.**
  Cheapest, and it preserves today's implicit behaviour, which is precisely
  the objection. It would make the silence work correctly rather than end it.
- **Have `TokenStore.Create` write the grant directly**, as `ensureBootstrap`
  now does. No schema change. Same objection, plus `TokenStore` holds no
  `RoleStore` reference, so it would need one purely to keep the bridge.

Both were rejected on the same ground, not on cost. Peter's decision,
2026-08-09, on an explicit cost comparison: the expensive option, done
properly.

### Consequences

**Provisioning becomes two acts, deliberately.** Create the token, then grant
its role. Every agent token in M0 step 9 goes through both. This is the cost,
and it is the point.

**Existing grant rows keyed on a fingerprint are inert, not dangerous.** They
authorize nothing, because nothing ever looks them up by that value. Whether
to delete them is an implementation question, not a decision.

**A real gap this opens, named rather than left to be found.**
`TokenStore.Revoke`'s last-admin guard counts rows where
`smeldr_tokens.role = 'admin'` (`auth.go:532`). After this change, the last
token *declared* admin is no longer the same thing as the last token *granted*
the admin role. The guard will keep protecting the declaration while the
grant is what actually matters. It is not closed here, and it should not be
closed by quietly making the guard read grants instead, since that changes
what a documented safety guarantee means.

**M0 steps 4 and 9 are blocked until this ships** (T216). The bootstrap token
is the only working credential on `process.smeldr.dev` in the meantime and
must not be revoked.

Status: Ratified 2026-08-09.

---

## A242 — D43 + D44/T216, smeldr/core half

### Verified before touching anything, not assumed

D43 states `RoleGranted`/`Authorized` resolve the actor from `User.ID`,
never `smeldr_tokens.id`. Grepped every production call site to confirm
this holds everywhere, not just where D43 itself had already checked:

```
module.go:1369        m.roleStore.Authorized(ctx, ctx.User().ID, "read", ...)
module.go:1397        m.roleStore.Authorized(ctx, ctx.User().ID, op, ...)
orchestration.go:222  rs.RoleGranted(ctx, actorID, role, AuthTarget{})
state.go:451          rs.RoleGranted(ctx, actorID, requiredRole.String, AuthTarget{})
mcp/tool.go:89        rs.Authorized(ctx, ctx.User().ID, requiredOp, target)
```

All five resolve from `ctx.User().ID` or a JWT-sourced `actorID`. D43 had
checked two of these before this task; this closes the other three. Removal
breaks no working path, because no working path was ever reading the rows
`migrateTokenGrants` produced.

### Part 1 — remove the bridge

`migrateTokenGrants` removed (`governance.go`), along with its call site in
`migrateGovernance` and 8 dedicated tests. The bootstrap grant in
`App.Handler` (A237) is untouched — it grants against the JWT `User.ID`
directly, D43's named exception, already correct.

### The inert-row question — membership, not shape

What happens to existing `smeldr_role_grants` rows keyed on a fingerprint?
Once `list_grants` ships (`smeldr.dev/mcp`, A243), calling it would return
these mixed in with real grants, with no way to tell them apart short of
knowing the 64-hex-character diagnostic. Left alone, that's a real cost of
doing nothing, not a hypothetical one.

The first draft of the fix deleted rows whose `token_id` was 64 hex
characters — D43's own diagnostic, made executable. Wrong predicate, caught
on review: that diagnostic exists so a human reading a table can tell the
two apart by eye. As a `DELETE` predicate it deletes authorization data on
the *shape* of a string, and would take a legitimate grant with it if any
deployment ever produced a `User.ID` of that shape. OAuth-issued tokens do
not go through `NewID()`.

The exact predicate was already sitting in the bug itself:
`migrateTokenGrants` wrote `smeldr_tokens.id` into `token_id`, so an inert
row is precisely one whose `token_id` **appears in** `smeldr_tokens.id` — a
real grant's `token_id` can never appear there without a SHA-256 preimage.
New `pruneInertTokenGrants(ctx, db) (removed int, err error)`:

```go
func pruneInertTokenGrants(ctx context.Context, db DB) (removed int, err error) {
	result, err := db.ExecContext(ctx,
		`DELETE FROM smeldr_role_grants WHERE token_id IN (SELECT id FROM smeldr_tokens)`,
	)
	...
}
```

Fail-open (missing `smeldr_tokens` errors, caller logs and continues — the
same treatment the removed migration had for the same reason), logged via
`slog.Info` on every run including zero removed, so a fresh instance's own
startup log shows the check ran rather than merely that it found nothing.
Wired into `migrateGovernance` in place of the removed call.

**Stated plainly, because the name alone doesn't say it — this is
enforcement, not migration.** `pruneInertTokenGrants` is not one-time
cleanup that eventually has nothing left to do. It runs on every
`migrateGovernance` call — every app startup, forever — and deletes any
`smeldr_role_grants` row whose `token_id` appears in `smeldr_tokens.id`,
full stop. It doesn't know or care that today's matching rows came from a
removed migration; it enforces D43's own rule (granting is a separate,
explicit act, never a byproduct of a token's role field) continuously,
which is stronger than a cleanup pass and is the right shape for that rule.
The consequence worth naming: anyone who ever keys a grant on
`smeldr_tokens.id` deliberately will watch it vanish at their next restart,
with a log line that gives no hint their own choice is what triggered it.

`TestPruneInertTokenGrants_RemovesOnlyMatchingRows` proves the fix is
membership, not shape directly: it seeds a real grant whose `token_id` is
*also* 64 hex characters but never appears in `smeldr_tokens.id`, and
asserts it survives pruning while the genuinely inert row is removed.

### Part 3 — audit is not optional (D44)

`App.Governance` now unconditionally calls `CreateGovernanceAuditTable` and
constructs a `GovernanceAuditStore`, held on a new unexported
`App.governanceAudit` field alongside the existing `App.governance`. New
`App.GovernanceAuditStore()` accessor beside `App.RoleStore()`. No option,
no opt-out — D44's own argument is that this is what makes it impossible to
reach a governance-enabled instance with no audit trail.

**A real design point, found while planning, that does not follow from
`RoleStore.WithAudit`'s own signature and would have been easy to build
wrong:** `WithAudit(actorTokenID string, log GovernanceAuditStore)
*RoleStore` bakes one fixed actor into the store it returns. It cannot be
wired once at startup — every MCP call comes from a different token. Every
caller (`smeldr.dev/mcp`'s new grant tools, A243) must derive a **fresh**
audit-wrapped store per request: `rs.WithAudit(ctx.User().ID, auditStore)`
immediately before each `Grant`/`Revoke` call, never a single shared
instance held across requests. Recorded here so it is not rediscovered, and
not left only in a plan file that gets deleted.

### Part 2 — tool policy rows

Three new rows in `seedToolPolicies`, matching `create_token`/`list_tokens`/
`revoke_token`'s own `"administer"` gate exactly: `grant_role`,
`list_grants`, `revoke_grant`. Must ship in the same release as
`smeldr.dev/mcp`'s own tool implementation (A243) — `authoriseTool`
(`mcp/tool.go`) treats a tool with no `smeldr_tool_policies` row as denied
for everyone, the identical code path a real DB error takes.

### Tests and coverage

17 new/extended tests: `pruneInertTokenGrants` — exec error (including the
real missing-`smeldr_tokens` case), `RowsAffected` error, the
membership-vs-shape regression above, idempotency; `App.Governance`
extended to assert `GovernanceAuditStore()` is non-nil on success, plus a
new test for `CreateGovernanceAuditTable` itself failing (neither
`RoleStore()` nor `GovernanceAuditStore()` gets wired on that path);
`seedToolPolicies`'s existing spot-check table extended with the three new
rows. No new error-path table needed beyond `pruneInertTokenGrants`'s own —
every other touched function's error paths were already tested before this
task.

No exported symbols removed. New exported `App.GovernanceAuditStore()`.
Coverage: 96.3% package-wide; `pruneInertTokenGrants`, `Governance`,
`GovernanceAuditStore`, `migrateGovernance` all 100%. `go build`/`vet`/
`gofmt`/`test`/`golangci-lint` all clean. `example/server` unaffected by
design — D44's whole point is that it needs no audit wiring of its own.

### Coverage and versioning

MINOR bump — new exported API surface (`App.GovernanceAuditStore()`), same
classification as A235/A236's own new-symbol additions. v1.61.2 → v1.62.0.

Status: Implements D43, D44.

---

## A243 — D43 + D44/T216, smeldr.dev/mcp half

### Scope

The mcp-side companion to A242: the only way to grant, list, or revoke a
governance role, now that A242 removed the implicit token-role→grant
bridge. Without this, A242 alone leaves governance grants reachable only
by calling `RoleStore.Grant`/`ListGrants`/`Revoke` directly in Go — no
agent or operator using the MCP surface could grant a role at all.

### Three new tools, mirroring the token-tool shape exactly

`grantToolDefs()` (`grant_tools.go`, new file) and `handleGrantTool`
follow `tokenToolDefs()`/`handleTokenTool`'s established pattern
(`tool.go`) rather than inventing a new one:

- **`grant_role`** — required `token_id`, `role`; optional `scope_static`
  (`[]string`) and `scope_anchor_id`. Calls
  `rs.WithAudit(ctx.User().ID, s.app.GovernanceAuditStore()).Grant(ctx,
  RoleGrant{...})` — unconditionally, per A242's Part 3: D44 guarantees
  `GovernanceAuditStore()` is non-nil whenever `RoleStore()` is non-nil, so
  there is no conditional-audit branch to write or test.
- **`list_grants`** — optional `token_id`; verified, not assumed, that
  `RoleStore.ListGrants` (`governance.go:872`) treats an empty `tokenID`
  as "no filter" before relying on it.
- **`revoke_grant`** — required `id`, the grant's own primary key, **not**
  a `token_id`. Deliberate asymmetry, not an inconsistency: you grant a
  *role*, and you revoke a *grant* by its own identity — forcing CRUD-style
  symmetry (`revoke_grant(token_id, role)`) would make the tool names agree
  with each other and disagree with `RoleStore.Revoke`'s actual signature.

Registration and dispatch in `tool.go` gate on `s.app.RoleStore() != nil` —
the same signal already used for token tools' `s.tokenStore != nil` gate,
not a second feature flag for the same fact. All three require Admin via
the ordinary `authoriseTool`/`ToolPolicy` path against A242's new
`"administer"` rows (`grant_role`/`list_grants`/`revoke_grant`), which is
why this and A242 had to ship in the same release — `authoriseTool` denies
an unpolicied tool for everyone, the same code path as a real DB error.

### `token_id` means the JWT `User.ID`, not the `list_tokens` fingerprint

Stated explicitly in both tools' descriptions and in `AGENTS.md`, because
nothing about the argument name makes this obvious and D43 built the
distinction deliberately: `RoleGrant.TokenID` (and every `RoleStore` method
that takes a `tokenID` parameter) has always meant the JWT `User.ID`
embedded at signing time, never `smeldr_tokens.id` (the SHA-256
fingerprint `list_tokens`/`create_token`'s `id` field shows). `TokenStore`
exposes no method that returns a freshly created token's `User.ID` — by
design, per D43, creating a token is not the moment a role gets decided.
The operator's only path to it is `smeldr.VerifyTokenString` (already
exported, already used by `mcp.New`'s own SSE bearer-verification example)
against the raw token they just received. `AGENTS.md`'s new grant-tools
section states this plainly rather than leaving it to be discovered by a
failed `grant_role` call against a fingerprint.

### `go.mod`, sequenced after the release, not alongside it

`go.mod`'s `smeldr.dev/core` requirement bumped `v1.58.4` → `v1.62.0` only
after A242 was tagged, released, and proxy-verified
(`GOPROXY=https://proxy.golang.org go list -m smeldr.dev/core@v1.62.0`) —
not against a `go.work`-local override. `GOFLAGS=-mod=mod GOWORK=off go mod
tidy` and `GOWORK=off go build ./...` both confirmed clean against the real
published module before this commit, so `main` here never carries a
`go.mod` that only resolves inside this workspace.

### The end-to-end proof this needed, not a unit-test substitute

`TestGrantTools_EndToEnd_MintedGrantRatifiesDecision` walks the full
operator path in one test, refusing every shortcut a smaller test would
have taken: `create_token` (MCP) mints a real bearer token; the token is
decoded via `VerifyTokenString` to recover its `User.ID` (the step above,
proven necessary rather than assumed); `grant_role` (MCP) grants `admin` to
that ID from a **separate** admin actor, not a self-grant; the newly
granted token's own raw JWT — not the granter's — is used as the
`Authorization` header on a real `PUT /decisions/{slug}` request against
`smeldr.RegisterOrchestrationTypes`'s real `app.Handler()`, exercising
`orchDecisionFlow`'s `RequiredRole: "admin", Strict: true` gate exactly as
a production request would. Asserts `200` and `Status == "ratified"`. This
is the proof that a grant minted purely through MCP's own tool surface is
real, load-bearing authority elsewhere in the system — not merely an
MCP-side bookkeeping row — closing the loop A242 opened.

`TestGrantTools_AuditRecordsWritten` is the audit-side analogue: calls
`grant_role` then `revoke_grant` through the real `handleToolsCall`
dispatch path, then queries `smeldr_governance_audit` directly by
`actor_token_id`/`action`/`target_id` — never calling `WithAudit`/`Append`
directly — proving D44's mandatory trail is actually reached from the MCP
surface, not just from `RoleStore`'s own already-tested Go API.

### Tests and coverage

16 new tests in `grant_tools_test.go`: presence/absence in `tools/list`;
`grant_role` success (with and without scope), missing `token_id`/`role`,
non-array/non-string `scope_static`, unknown role (`-32001`, `ErrNotFound`
via `errorFor`); `list_grants` unfiltered and filtered; `revoke_grant`
success and missing `id`; the `handleGrantTool` unknown-name default
branch; a forbidden case for an editor-only actor across all three tools;
the audit and end-to-end proofs above. Package coverage: 96.3%. `go
build`/`vet`/`gofmt`/`test` all clean; `golangci-lint` reports only the
same four pre-existing, unrelated findings A238 already flagged
(`mcp_test.go`, `node_tools.go`) — none in any file this task touched.

No exported `smeldr.dev/core` symbols changed by this half. New exported
`smeldr.dev/mcp` behaviour: three new MCP tools. `AGENTS.md` (smeldr/core)
gains a governance grant tools section plus a note on the token-management
section that creating a token grants no role.

### Coverage and versioning

MINOR bump — new tool surface, same classification as A227/A238's own
new-tool additions. `smeldr.dev/mcp` v1.29.4 → v1.30.0. No `smeldr/core`
version change (A242 already shipped its own).

Status: Implements D43, D44.

---

## A244 — D43/T216 addition: expose the identity `grant_role` needs from `create_token`

### The gap, found on review of A243, not inferred

D43 splits token provisioning into two acts — create a token, then grant it
a role — deliberately, so granting authority is never a byproduct. A243
shipped the second act (`grant_role`) but not a way to reach it from the
first: `create_token` returned `{token, message}`, `list_tokens` returns
only the SHA-256 fingerprint, and `grant_role` requires the JWT `User.ID`.
The only way to bridge them was decoding the raw JWT by hand.

That is not the two-act sequence D43 describes. It is a two-act sequence
with a manual, undocumented step in the middle — and it is the exact
operation the self-hosting roadmap's M0 step 4 and step 9 consist of. D43
required the second act to be explicit; it did not say the explicit act
should be unreachable from the surface built for it.

### The fix is smaller than the gap

`createToken` (`auth.go`) already computes and returns the JWT `User.ID`;
`TokenStore.Create` already discards it, unchanged, same signature, same
callers. New method exposes what already exists:

```go
func (ts *TokenStore) CreateWithID(ctx context.Context, name, role string, ttl time.Duration) (raw, userID string, err error) {
	return ts.createToken(ctx, name, role, ttl)
}
```

A thin wrapper, no new logic, no new failure mode — the same shape `Create`
already is over `createToken`. `smeldr.dev/mcp`'s `create_token` tool
switches to it and adds the field to its response:

```go
raw, tokenID, err := s.tokenStore.CreateWithID(ctx, tokenName, role, ttl)
...
return toolResult(map[string]any{
	"token":    raw,
	"token_id": tokenID,
	"message":  "Store this token securely — it cannot be retrieved again.",
}), nil
```

### `token_id`, not `user_id`

Named to match `grant_role`'s own argument, not the Go-level `User.ID` this
actually is — call-site continuity over Go-level precision. A caller
reading `create_token`'s response and a caller writing `grant_role`'s
argument should be looking at the same field name; `user_id` would have
preserved exactly the friction this fix exists to remove, just moved one
field over. `AGENTS.md`'s grant-tools section (shipped by A243) is
corrected in the same commit — it previously stated decoding the raw token
was the only way to learn this value, which A243 was still technically
correct about, and A244 makes false.

### The test that was standing in for the gap gets rewritten, not duplicated

`TestGrantTools_EndToEnd_MintedGrantRatifiesDecision` (A243) used
`smeldr.VerifyTokenString` to recover the second actor's ID — that call was
never testing a real operator workflow, it was standing in for the missing
field. Once `token_id` is real, the test's happy path reads it directly off
`create_token`'s response instead. Adding a second test beside the
unchanged original would have left a passing test permanently documenting
a workaround no operator should take — the kind of thing that quietly
calcifies into the "supported" way to do something. A narrower
`TestHandleTokenTool_CreateWithID_TokenIDMatchesJWT` keeps exactly one
`VerifyTokenString` call, as a consistency check that the returned
`token_id` matches what the JWT actually carries — not as the documented
path.

### The rest of the operator chain, checked rather than assumed to hold

Verifying this one hop invited the question of whether the other three
hold. They do, checked directly: `RoleGrant.ID` is empty on input to
`Grant` and populated by `ListGrants`, and `revoke_grant` takes exactly
that value — `list_grants`→`revoke_grant` needs no bridge. `RoleGrant.TokenID`
is the same `User.ID` that `Authorized`/`RoleGranted` query by —
`grant_role`'s `token_id` argument reaches authorization correctly. The
operator surface closes end to end as of this Amendment, not assumed to.

### Tests and coverage

2 new core tests: `TestTokenStore_CreateWithID` (non-empty `userID`,
distinct from the token's own fingerprint), `TestTokenStore_CreateWithID_execError`
(surfaces the same error `Create` does). mcp: the end-to-end test rewritten
in place, one new consistency test added. `AGENTS.md` corrected. No
exported symbols removed; new exported `TokenStore.CreateWithID`. Coverage:
core 96.2%, mcp 96.3%. `go build`/`vet`/`gofmt`/`test` clean both repos;
`golangci-lint` reports only the same four pre-existing, unrelated findings
already flagged by A238 — none in any file this addition touched.

### Coverage and versioning

MINOR bump both repos — new exported API surface. `smeldr/core` v1.62.0 →
v1.63.0. `smeldr.dev/mcp` rides the same release as A243 (still unreleased,
pending Peter's clearance) rather than its own separate bump.

Status: Implements D43.

---

## A245 — `example/server` gets the sibling examples' `replace smeldr.dev/core` directive

### Found by asking the tag to stand on its own

Building the `v1.63.0` release artifact required a fresh worktree checked
out exactly at the tag, built with `GOWORK=off` — no gitignored `go.work`
present to quietly substitute local sibling checkouts for whatever each
module's own `go.mod` actually declares. `example/server` failed to
compile: `smeldr.CreateProvenanceTable`, `app.Provenance`,
`smeldr.NewProvenanceStore`, `smeldr.RegisterOrchestrationRelationKinds`,
`app.ContextPacketHandler` all undefined.

### Not a stale pin — a missing directive

`example/server/go.mod` pins `smeldr.dev/core v1.54.0`. Checked when each
missing symbol shipped, not guessed: `ContextPacketHandler` is A214
(v1.55.0, 2026-07-11), `Provenance`/`ProvenanceStore` is A220 (v1.57.0,
2026-07-27), `RegisterOrchestrationRelationKinds` is A236 (v1.60.0). The
pin has been behind `main.go` for roughly a month.

Checked the three sibling example modules rather than assuming
`example/server` was uniquely broken: `example/blog`, `example/api`, and
`example/docs` all carry `replace smeldr.dev/core => ../..`.
`example/server` carries no replace directive at all — it is the only one
of the four that doesn't follow the repo's own established pattern, and
the only one that failed.

### The consequence is user-facing, not just a release-tooling inconvenience

README's own "Generic server" section documents `cd example/server && go
run .` as the way to try it after cloning. `go.work` is gitignored — a
fresh clone has no local override, resolves `smeldr.dev/core` to the
pinned v1.54.0, and hits exactly the five errors above. This has been true
for about a month; nobody hit it because nobody in this project builds
`example/server` without the workspace in the ordinary course of things.

### The fix is the sibling pattern, not a version bump

`replace smeldr.dev/core => ../..`, added via `go mod edit -replace`
(`example/server/go.mod`); `go mod tidy` (`GOWORK=off`) removed four
now-unnecessary `go.sum` lines for the old pinned version and left the
`require` block's version string untouched, matching Go's own convention
that a `replace` target's declared version becomes advisory once
replaced.

A version bump to `v1.63.0` was rejected — flagged in the plan, agreed
before implementation: a pin goes stale again at the next release and
reproduces this exact failure in about a month, on whatever module happens
to ship next. A relative `replace` never goes stale; it always builds
against whatever is actually checked out, which is what the other three
examples already rely on and why they didn't break.

The other five pinned dependencies (`mcp`, `agent`, `media`, `oauth`,
`social`) are separate repos and cannot be relatively replaced from a
`smeldr/core`-only clone, so they keep real version pins. Checked, not
swept in silently: `GOWORK=off go build ./...`, `go vet ./...`, and `go
test ./...` all pass with only the core replace added — no further
undefined symbols, so none of the other five are currently behind.

### `smeldr.dev/core`'s own content is unchanged

`example/server` is its own module (`module example/server`) — its
`go.mod` is not part of the `smeldr.dev/core` package. Verified, not
assumed: `git diff` against the `v1.63.0` tag touches only files under
`example/server/`. No `smeldr.dev/core` file changed, so no version bump
and no new tag — a release here would describe a change to a module whose
published content did not move.

### Separately flagged, not folded in

`example/blog/go.mod` also carries `replace smeldr.dev/mcp =>
../../../mcp` — a path that reaches outside this repo. A clone of
`smeldr/core` alone cannot satisfy it, which means README's primary
quickstart path may have the same class of problem for a different
reason. Not verified against a real clean clone here; tracked as its own,
separate follow-up rather than guessed at and swept into this fix.

### Tests and coverage

No new tests — a dependency-resolution fix, not a behavior change;
`example/server`'s own existing test suite (`go test ./...`, `GOWORK=off`)
passing is the proof. No `smeldr.dev/core` coverage change (no core files
touched).

### Versioning

No version bump, no tag — `smeldr.dev/core`'s own published content is
unchanged. Level 1 amendment (single-file config fix, no cross-file
consequences within the `smeldr.dev/core` package itself).

Status: Implements the repo's own existing convention (A197's original
per-example `replace` directives), restoring `example/server` to it.

---

## A246 — T217: the other three examples don't build from a clean clone either

### Found by actually doing it, not by pattern-matching A245

A245 fixed `example/server`'s missing `smeldr.dev/core` replace. The
architect verified that fix the same way it was proposed to be
verified — a shallow clone into a tempdir, no sibling repos, `GOWORK=off`,
`go build ./...` in each example — and found a second, unrelated defect
in the process: `example/blog`, `example/api`, and `example/docs` don't
build from a clean clone either, for a different reason.

### The defect

Each of the three declares a `go` line below what the `smeldr.dev/core`
module they `replace` in actually requires:

| module | declared `go` | core requires |
|---|---|---|
| `example/blog` | `1.26.3` | `1.26.5` |
| `example/api` | `1.26.2` | `1.26.5` |
| `example/docs` | `1.26.2` | `1.26.5` |
| `example/server` | `1.26.5` | `1.26.5` (already correct) |

Go's automatic toolchain switching keys off the **main** module's own
`go` directive — never a `replace` target's. A main module declaring
`1.26.3` never triggers a switch to a newer toolchain even though the
code it's about to build (`smeldr.dev/core`, replaced in from `../..`)
requires one, and the build fails outright:

```
go: module ../.. requires go >= 1.26.5 (running go 1.26.3)
```

`example/server` declaring `1.26.5` was luck, not design — A245 fixed
its missing replace directive without anyone checking whether its `go`
line happened to already be correct, which it did, incidentally.

### A dispatch error caught by checking the source, not the description

NEXT.md stated all three examples declare `go 1.26.3`. Checked each
`go.mod` directly rather than trusting the summary: only `example/blog`
does. `example/api` and `example/docs` declare `1.26.2`. The architect
had checked `blog` only and generalized from one file to three. Reading
core's own root `go.mod` for the real target value, rather than copying
`1.26.5` out of the task description either, is what surfaced the
discrepancy — the same "check the source, not the summary" habit A245
and this task's own verification method both depend on.

### Fix: bump the `go` line, nothing else

One line changed per file — `example/blog/go.mod`, `example/api/go.mod`,
`example/docs/go.mod` all now declare `go 1.26.5`, matching core's own
root `go.mod`.

### `example/blog`'s second, load-bearing defect: a replace path outside the repo

`example/blog/go.mod` also carried `replace smeldr.dev/mcp =>
../../../mcp` — three directories up from `example/blog/`, outside
`smeldr/core` entirely. Absent in any clone of this repo alone; this is
the failure that appears immediately after the `go` line is fixed, not a
separate, later concern.

Checked whether this was a stale, unused import before proposing
anything — it isn't: `example/blog/main.go` imports `smeldr.dev/mcp` and
calls `mcp.New(app)`, wiring a real MCP server into two routes. Dropping
the dependency entirely would change what the example demonstrates,
which was explicitly not this task's call to make.

### The rule, stated once rather than left implicit

`example/server` already embodies the right shape for this without ever
stating it as a rule: a dependency that lives **in this repo**
(`smeldr.dev/core`) gets a relative `replace`; a dependency that lives in
a **separate repo** (`mcp`, `agent`, `media`, `oauth`, `social`) gets a
real version pin, resolved via the proxy, because a relative path to a
sibling repo cannot exist in an isolated clone of `smeldr/core` alone.
`example/blog` was the one place still relatively-replacing an
out-of-repo module. Fixed to match: `replace smeldr.dev/mcp =>
../../../mcp` removed, `require smeldr.dev/mcp v1.30.0` (the newest
tagged release; nothing in `example/blog/main.go` requires anything
older) added via `go mod edit -require` + `GOWORK=off go mod tidy`.

**Named explicitly, not presented as closing the question**: a version
pin is only as safe as whatever actually re-checks it stays current, and
no such check runs automatically today. This is the identical failure
mode A245 itself was born from — `example/server`'s `smeldr.dev/core`
pin went stale for a month, invisible precisely because nothing built it
outside `go.work`. `example/blog`'s new `smeldr.dev/mcp` pin carries the
same latent risk until a repeatable isolation build exists. This task's
own manual clean-clone run (below) is a point-in-time check, not that
missing automation — T217 tracks the larger pattern; whether the answer
is a clean-clone CI job, a release-time check, or something else is
explicitly not decided here.

### Verification, from real isolation — the part the architect said would be checked

Cloned into a tempdir placed **entirely outside** the `Smeldr/` directory
tree — not a subdirectory of it. A clone inside the tree could still
resolve `example/blog`'s old `../../../mcp` path by relative traversal
and would have proven nothing about whether the fix actually removed
that dependency; placing the clone outside the tree removes every
sibling repo (`mcp`, `agent`, `media`, `oauth`, `social`) and any
`go.work` file from being reachable at all, by relative path or by Go's
own upward directory search.

`GOWORK=off go build ./...` in all four example directories inside that
clone. All four exit 0. Error text was checked as well as exit codes —
not just "green" — so that `example/blog`'s previously-known failure
mode couldn't mask a new, different regression behind the same exit
code.

### README, trivially true, fixed in the same commit

The 30-second quickstart did:

```bash
git clone https://github.com/smeldr/core
cd example/blog
```

`git clone` with no explicit target directory clones into `./core` — the
next line needs to be inside that directory. As written, the documented
command failed before it ever reached the `go.mod` question this task
is about. Added `cd core` between the two lines.

### Scope grew mid-task: the deploy that already happened

`process.smeldr.dev` was rebuilt from A245's fix and deployed. Its own
`/_health` reported `mcp: 1.28.0`. `grant_role`, `list_grants`,
`revoke_grant`, and `create_token`'s `token_id` field all shipped in
`smeldr.dev/mcp` v1.30.0 (A243, A244). A245 added a relative `replace`
for `smeldr.dev/core` only — `example/server`'s `smeldr.dev/mcp` pin was
never touched, so the artifact that shipped carried core v1.63.0 against
mcp v1.28.0. The tool-policy rows for the three grant tools are in the
live database, because those come from core. The tools themselves are
not, because those come from mcp. M0 step 4 — the reason T216 exists —
had not actually reached the instance.

**The specific reasoning that let this through was shared, not solely
the architect's.** A245's own claim that the other five pinned
dependencies "were checked, not assumed, to still be current" rested on
`GOWORK=off go build/vet/test ./...` passing. That check proves
`main.go` doesn't call anything a pinned version lacks. It says nothing
about whether the pinned version is actually the current release — MCP
tools are additive, so a stale tool pin loses tools *silently*: no
compile error, no test failure, nothing observable until an operator
calls a tool that was never in the binary. "Does it build" and "is the
pin current" are two different questions, answered by two different
checks, and only the first one had been run.

### Checked each of the five out-of-repo pins against what's actually published

Not assumed from whether the build passes — checked directly (`go list
-m -versions`, `GOWORK=off`, against the real proxy) and reported for
all five, including the ones that turned out fine:

| module | pinned | latest published | action |
|---|---|---|---|
| `smeldr.dev/mcp` | v1.28.0 | v1.30.0 | bumped |
| `smeldr.dev/social` | v0.9.2 | v0.10.1 | **not bumped — see below** |
| `smeldr.dev/agent` | v0.7.1 | v0.7.1 | already current |
| `smeldr.dev/media` | v1.6.0 | v1.6.0 | already current |
| `smeldr.dev/oauth` | v0.4.0 | v0.4.0 | already current |

**`mcp` v1.28.0 → v1.30.0, bumped.** Read every intervening `CHANGELOG.md`
entry (v1.29.0 through v1.30.0) before bumping, not just the version
number: `list_type_tools` tool (additive), `id`-as-slug-alias widening
(additive — old `slug`-only calls still work), `list_type_tools`
reordered to the front of `tools/list` (a real output-order change, but
`example/server` doesn't parse its own `tools/list` output — no
consumer impact here), `observe_relation` tool (additive),
`ErrRevConflict` gaining its own `-32002` code instead of falling into
generic `-32603` (a real behavior change to an existing error path — a
client branching on `-32603` to catch a rev conflict would now miss it;
`example/server` itself does not do this), `grant_role`/`list_grants`/
`revoke_grant`/`token_id` (additive, and the entire point of this
bump). None of these remove or alter behavior `example/server` itself
depends on — bumped.

**`social` v0.9.2 → v0.10.1, not bumped — flagged instead, per the
explicit instruction to stop rather than carry a behavior change
silently.** v0.10.0 is additive (five new REST endpoints). v0.10.1 is
not: `MCPCreate`'s explicit `status` field was previously silently
ignored on create; v0.10.1 makes it take effect. A caller that
previously set `status` and had it silently dropped will now see it
actually applied — a real behavior change to existing input handling,
not new surface. Unrelated to T216's urgency (social is opt-in via
`ENABLE_SOCIAL`, no connection to the grant-tools gap), so left at
v0.9.2 pending a separate decision rather than bundled into this fix.

**Recorded here explicitly, not left implicit: `example/server`'s
`smeldr.dev/social` pin is now *knowingly* behind, with the reason
written down.** A stale pin nobody wrote a reason for is exactly how
`smeldr.dev/mcp` reached v1.28.0 in the first place — this Amendment's
own opening finding. The difference between "knowingly behind, reason
on record" and "accidentally behind, discovered three months from now"
is entirely this paragraph existing.

**`agent`, `media`, `oauth`: checked, already current, no action.**
Reported so "no change" reads as verified rather than unmentioned.

### A side effect noticed on review, not deliberately made: `example/server`'s `core` require line

`go mod tidy` (run as part of the `mcp` bump above, and separately when
A245 first added the `core` replace) rewrote `example/server/go.mod`'s
`smeldr.dev/core` require line from `v1.54.0` to `v1.63.0`. Not a
deliberate edit — Go's own convention for a replaced module's nominal
require version, applied automatically. Flagged on review because it
is consequential, not because it was wrong: Go's build info reports the
*require* version for a replaced module, not the replaced content's
actual version, so `/_health`'s `"core"` field — previously a lie
(`"1.54.0"` while the binary actually ran whatever `../..` contained) —
now happens to read `"1.63.0"`, matching the real content for this
build specifically.

**This is a real, welcome side effect for T219 (the same
decoration-not-measurement problem `/_health`'s version fields have
generally), and it does not close T219.** The require line will drift
stale again at the next `smeldr.dev/core` release exactly the way it
already did once, because nothing re-derives it from the replaced
module's actual content — only `go mod tidy` running incidentally (as
here) keeps it honest, and nothing forces that to happen on every
release. What changed today is that the string is *currently* true
rather than *currently* false, not that it is now guaranteed true.

### The artifact was rebuilt, because the one already on disk was wrong

The `v1.63.0`/`smeldr-server` build A245 produced (and devops has, as of
this Amendment, not yet deployed further than the one instance above)
carried the stale mcp pin. Rebuilt after the mcp bump, from the commit
that carries both fixes; verified end to end against a real running
instance (not just `go build`) that `tools/list` now includes
`grant_role`/`list_grants`/`revoke_grant`, and `create_token`'s response
includes `token_id`. Commit, SHA-256, and static-link confirmation
reported in the `committed` signal, same shape as A245's own report.

### What this says about T217's own design, stated because T217 is still open

The clean-clone `GOWORK=off go build ./...` check this Amendment (and
A245) both rely on catches "does not compile." It would not have caught
this — the stale `mcp` pin builds fine, every symbol it needs already
existed in v1.28.0. **Pin currency is a second, different check from
build success, and they do not overlap**: a build check proves the code
compiles against whatever is pinned; a currency check proves the pin
still points at the current release. Neither substitutes for the other,
and nothing in this repo runs the second one automatically yet. T217
now covers both — this Amendment's own mid-task discovery is the
evidence for why the scope needed both, not resolved proof that either
is solved.

### Tests and coverage

No new tests — dependency-resolution and `go.mod` fixes, not a behavior
change to any tested code path. Verification is the isolation build
itself, described above, not a Go test. No `smeldr.dev/core` coverage
change — no core `.go` files touched.

### Versioning

No version bump, no tag — same reasoning as A245: nothing in the
`smeldr.dev/core` package's own content changed; these are `example/*`
module and documentation fixes. Level 1 amendment.

Status: Fixes a defect A245's own verification uncovered. Names, does
not close, the isolation-build gap both amendments depend on.

---

## A247 — a `TargetChecker` that knows the orchestration types

Implements D47 (`decisions/recent.md`) — read that entry for the
category-error principle, the sideways `resolveItemTable`-fallback
hazard, and the three rejected alternatives; not restated here.

### The implementation, small, reusing what D42 already built

`resolveItemTable(ctx, db, typeName) string` (`state.go:721`, built for
D42's `drainAuthorizationGate`) already does exactly the type-name to
table resolution this needed — reused rather than duplicated, checked
before writing anything new. New unexported `defaultTargetChecker(db
DB) TargetChecker` (`smeldr.go`) replaces the inline closure
`App.SweepStructural` used to build: for any table `resolveItemTable`
resolves to something other than `smeldr_dynamic_content`, alive means
`SELECT COUNT(*) ... WHERE id=$1` finds a row; otherwise, after the
registry check above, the original `status = 'published'` lookup,
byte-for-byte unchanged.

### Where this belongs — `App`'s own default, not an opt-in

Replaces `App.SweepStructural`'s built-in default, per D47's own
rejection of an opt-in constructor or `example/server`-only wiring.
One consequence worth stating precisely at the implementation level:
`resolveItemTable`'s own fallback design means the corrected default
covers *any* compiled `Module`-registered type automatically, not just
the six built-in orchestration ones — no hardcoded type list, so a
consumer with their own compiled types gets the fix without doing
anything. `SweepStructural`'s existing caller-facing guidance narrows
rather than disappears: still true for an application whose own "alive"
definition genuinely differs from "row exists" (D47's own documented
surviving case) — that consumer still supplies its own `TargetChecker`
via `RelationStore.SweepStructural` directly.

### Tests and coverage

9 new tests, all driven through `App.SweepStructural`, never the
checker function directly — the explicit check criterion, and the
right one: a checker correct in isolation but never reached by the
real entry point is the same defect class as the one this fixes.
Compiled-target alive and hard-deleted; the two named checks
(superseded and archived `Decision` both keep their relations,
identical assertions, stated as the same rule rather than two separate
rules that happen to agree); the compiled-table query-error path;
both ways the registry lookup itself can fail (an unrecognized type
with no table anywhere, including a decoy dynamic-content row under a
different real type to prove the checker isn't just matching by
coincidental ID; the `smeldr_content_type_schemas` table missing
entirely, reached by constructing `App.relationStore` directly since
`App.Relations()` always creates that table as a side effect); a
genuine dynamic-content deletion, proving the registry gate didn't
change what "not found" already meant for a type that legitimately
belongs there. One existing test (`TestAppSweepStructural_DefaultChecker`)
updated, not left to fail silently: it exercised a dynamic content type
("article") that was never registered in `smeldr_content_type_schemas`,
which this Amendment's own new registry check would now (correctly)
reject — fixed by registering the type properly rather than weakening
the check to pass the old test.

No exported symbols changed. Coverage: 96.3% package-wide;
`defaultTargetChecker` 90.9% — two DB-failure leaves in the unchanged
dynamic-content branch (`QueryContext`/`Scan` errors) left uncovered,
the same class of failure already exercised for the compiled-table
branch; named here rather than left silent. `go build`/`vet`/`gofmt`/
`test`/`golangci-lint` all clean.

### Explicitly out of scope, confirmed against the dispatch's own exclusions

No scheduler, no cron, no call site invoking `SweepStructural` on a
timer — this builds the checker, not the trigger. `SweepStructural`'s
return values unchanged (the missing relations-examined count is real,
named as its own separate task, not carried here).

### Versioning

Patch bump — a real, consumer-observable fix to `App.SweepStructural`'s
existing default behaviour, not new exported API surface.

Status: Implements D47.

---
