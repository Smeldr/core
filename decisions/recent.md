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
