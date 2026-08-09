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
