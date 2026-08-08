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
---

## A236 — Register the four orchestration relation kinds (D36, M0 step 2)

### What

Implements D36 in full: `RegisterOrchestrationRelationKinds(ctx
context.Context, store *RelationStore) error` (`orchestration.go`, new)
— a public registration function for all four orchestration-layer
relation kinds, allowing M0-step-3's permanent instance and other
callers to wire them up without duplicating their inline definitions.

Registers four relation kinds via `RelationStore.UpsertKind`, each with
Mode=asserted, Directional=true, Weighted=false:

- `derives_from`: Task → Goal (architect derives a Task from a Goal)
- `depends_on`: Task → Task (implementer's mandatory pre-check)
- `ships_as`: Task → Amendment (Amendment names the Task it shipped for)
- `supersedes`: Decision → Decision (role-gated Decision supersede)

(A fifth candidate, `implements` [Amendment → Decision], remains
unregistered per D36 — never used in a worked example.)

### D36 already settled this — a citation miss, not a re-litigated decision

NEXT.md's own framing presented Mode/Directional/Weighted/TypePairs as
an open design question, to be argued independently from the design
docs (`decision-authority-and-lineage.md` §11,
`process-types-and-workflow.md`'s relation table). The plan did that
independent analysis, tracing each kind to a deliberate act by a named
actor at an identifiable workflow moment — and converged on values
identical to D36's.

D36 (commit `7db0eb6`) had, in fact, already settled this exact
question before this task began. This was not a re-litigated decision
or a code change — it was a citation miss, caught and corrected before
commit, not silently smoothed over. D36 is the controlling decision;
this amendment implements its verdict. `docs/ARCHITECTURE.md` and this
entry cite D36 directly rather than presenting the reasoning as if
discovered in a vacuum.

### Implementation notes

- **Mode is validated but not enforced at write time.**
  `RelationKindDef.Mode` is checked by `ValidateRelationKindDef` when a
  kind is registered, but the actual `edge_class` written to an edge is
  chosen entirely by which write call is used: `MCPAssertRelation`
  (`"asserted"`), `MCPProposeRelation` (`"inferred"`), or
  `MCPObserveRelation` (`"observed"`) — independent of the registered
  kind's `Mode`. `Mode` is a documented default character for the kind
  today, not yet a live enforcement gate.
- **`TypePairs` is documentation-only.** Read only by
  `ValidateRelationKindDef`'s JSON-shape check, never by `insertEdge` or
  any write path — safe to populate descriptively (e.g.
  `[{"source_type":"Task","target_type":"Goal"}]`).
- **Wired into the existing orchestration gate.** Called from
  `example/server/main.go`'s existing `if cfg.EnableRelations &&
  cfg.EnableOrchestration` block (previously only called
  `ContextPacketHandler`), using `context.Background()` — the function
  has no other `ctx` variable in scope. Idempotent by design, relying on
  `UpsertKind`'s own idempotency.

2 new tests in `orchestration_test.go`:

- `TestRegisterOrchestrationRelationKinds_RoundTrip` — asserts all four
  kinds' `Mode`/`Directional`/`Weighted`/`TypePairs` via `ListKinds()`,
  then calls registration again to prove idempotency.
- `TestRegisterOrchestrationRelationKinds_UpsertError` — covers the
  single linear error-return path on `UpsertKind` failure, reusing the
  existing `mockRelationStore`/`errExecDB` fixtures from
  `relations_errors_test.go`.

Coverage: 96.2% overall; `RegisterOrchestrationRelationKinds` itself
100%. No exported symbols changed elsewhere. MINOR version bump
(new exported API surface): v1.59.0 → v1.60.0.

Status: Ratified 2026-08-07.

---

## A237 — Fix D34 enforcement gap: bootstrap admin token couldn't ratify Decisions (M0 step 3)

### What

The bootstrap flow creates a token with `role="admin"` (set by
`TokenStore.ensureBootstrap`, called from `App.Handler()`), which
satisfies the generic MCP tool access gate. But `RoleStore.RoleGranted`,
called by `validateTransition` when checking a `Decision` with
`Strict: true`, queries only `smeldr_role_grants` — it never reads
`smeldr_tokens.role`.

On a governance-enabled instance with an empty `smeldr_tokens` table,
the bootstrap token could do everything EXCEPT ratify or supersede a
`Decision` — exactly the operation a governance-enabled instance exists
to support.

### Checked before implementing, not assumed

Traced the full authorization path from source, not assumed from naming:

- `validateTransition` (`state.go`) calls
  `rs.RoleGranted(ctx, actorID, requiredRole.String, AuthTarget{})`
- `RoleGranted` (`governance.go`) queries
  `smeldr_role_grants` JOIN `smeldr_roles` only
- Never reads `smeldr_tokens.role` at all

Additional complication found: the `actorID` passed to `RoleGranted` is
the random `User.ID` embedded in the JWT's claims at token-creation time.
This ID is NOT the same as `smeldr_tokens.id` (the SHA-256 fingerprint of
the raw signed token). The two are computed independently and never
cross-referenced. The `actorID` is what `RoleStore.Grant` requires in its
`TokenID` field.

Confirmed by grepping the whole package: no production code anywhere
called `RoleStore.Grant` before this fix — a real, latent gap, not
hypothetical.

### Fix

New unexported helper in `auth.go`:

```go
func (ts *TokenStore) createToken(ctx context.Context, name, role string,
	ttl time.Duration) (raw, userID string, err error)
```

This factors `Create`'s existing logic (build a `User`, sign it, hash and
insert the fingerprint into `smeldr_tokens`) into a shared implementation
that also returns the JWT's own `User.ID`.

`Create`'s exported signature and behaviour are completely unchanged — it
now delegates to `createToken` and discards the `userID`:

```go
func (ts *TokenStore) Create(ctx context.Context, name, role string,
	ttl time.Duration) (string, error) {
	raw, _, err := ts.createToken(ctx, name, role, ttl)
	return raw, err
}
```

`ensureBootstrap`'s signature changes (unexported, non-breaking) from
returning nothing to returning the created token's identity:

```go
func (ts *TokenStore) ensureBootstrap(ctx context.Context) (
	userID string, created bool)
```

`App.Handler()` in `smeldr.go` extends the bootstrap block: when a token
was freshly created AND `App.Governance` is wired, it grants the bootstrap
token the `admin` role:

```go
if created && a.governance != nil {
	if _, err := a.governance.Grant(context.Background(), RoleGrant{
		TokenID: userID, RoleName: "admin",
	}); err != nil {
		slog.Warn("smeldr: failed to grant admin role to bootstrap token",
			"err", err)
	}
}
```

This is fail-open (logs a warning, does not panic or block startup) —
matching every other branch in that same bootstrap block.

The grant is sufficient with no other `RoleGrant` fields needed:
`seedDefaultRoles` already seeds the `admin` role with
`scope_mode='global'` at role-definition time, and `RoleGranted`'s
`ScopeGlobal` case returns `true` unconditionally on any matching grant
row. A bare `RoleGrant{TokenID: userID, RoleName: "admin"}` is a complete,
unconditional global admin grant.

### Why not a script or a documented manual step

NEXT.md offered two alternatives, both rejected in the approved plan:

**A one-time devops setup script:** an extra artifact to write, ship, and
remember to run, for something the App can already safely do at boot.

**A documented manual step:** exactly the kind of "someone has to
remember to do this" step that gets missed — the original task framing
was already worried about this. It is not acceptable for a
governance-enabled instance to have a permanently unreachable operation
at bootstrap.

Both alternatives lose to the idempotent-at-boot shape `ensureBootstrap`
and `seedDefaultRoles` already established as this project's precedent
for exactly this kind of bootstrap concern.

### Tests and coverage

5 tests changed/added in `auth_test.go` and `coverage_test.go`:

- `TestTokenStore_ensureBootstrap_empty` extended to assert
  `created == true` and a non-empty `userID`
- `TestTokenStore_ensureBootstrap_nonEmpty` extended to assert
  `created == false` and empty `userID`
- `TestTokenStore_ensureBootstrap_createFails` extended to assert
  `created == false` and empty `userID`
- New `TestApp_Handler_bootstrapGrantsAdmin` — end-to-end: after calling
  `App.Handler()` with governance wired and an empty `smeldr_tokens`
  table, queries `smeldr_role_grants` for the admin-role row, then calls
  `RoleStore.RoleGranted` on that token ID and asserts `true` — proves the
  fix through the real API, not just that "Grant was called"
- New `TestApp_Handler_bootstrapGrantFails` — governance wired against a
  DB wrapper that fails the `INSERT INTO smeldr_role_grants` statement;
  asserts `Handler()` does not panic (fail-open)

Named, accepted coverage gap: `createToken`'s `SignToken`-error branch is
at 91.7% function coverage (one uncovered line). This is a structurally
unreachable branch (the same pre-existing `json.Marshal`-on-a-simple-struct
failure that `encodeToken`'s own doc comment already calls "unreachable in
practice"), now visible under the new `createToken` function name instead
of being invisible inside `Create`'s own body. Not a regression —
package-wide coverage held flat at 96.2% before and after this change
(`ensureBootstrap` and `Create` both 100%).

### Context: design decisions made alongside this fix

No new binary needed for the permanent self-hosting instance —
`example/server`'s existing config-driven binary already supports the
required `ENABLE_TOKENS`+`ENABLE_GOVERNANCE`+`ENABLE_RELATIONS`+
`ENABLE_ORCHESTRATION` combination as-is. SQLite chosen as the backend for
that instance's own deployment — matches the existing proven backup
pattern and avoids being the first live test of a documented (Amendment
A235) SQLite-only portability gap in the relation/lineage code that
instance depends on.

**Important distinction:** this bootstrap grant is a break-glass safety
net so ratify/supersede is never permanently unreachable from a
governance-enabled instance's very first boot. It is explicitly NOT a
substitute for provisioning an operator's own real, deliberately-granted
token — a separate, later step in the self-hosting rollout plan.

No new exported symbols. Patch bump (consumer-observable: a fresh boot
with `ENABLE_GOVERNANCE` set and an empty `smeldr_tokens` table now also
grants the bootstrap token an admin `RoleStore` role, where it previously
silently could not ratify/supersede a `Decision`). v1.60.0 → v1.60.1.

Status: Ratified 2026-08-07.

---

## D38 — Run coordination for headless automation: a sixth typed orchestration module, leases over CAS, branches carry state

### Scope

core

### Decision

Headless automation (M3) coordinates through a new sixth typed
orchestration module, `Run`: one row per mechanical episode, from claim to
merge-or-abandon. Five choices are load-bearing.

**1. `Run` is a typed module, never a `DynamicTypeRepo` type.** Verified
directly against core at `40310e8`: `SQLRepo.Save` (`storage.go:700-778`)
carries the only atomic test-and-set in the framework, an `ON CONFLICT ...
WHERE {table}.rev = $revPH` guard that returns `ErrRevConflict` on
`RowsAffected == 0`. `DynamicTypeRepo.UpdateFields`/`setStatus`
(`dynamic.go:175-261`) have no such guard, and `setStatus` validates and
writes as two separate statements, so two concurrent transitions from the
same state can both pass validation and both apply. `conflictRejectCheck`
(`state.go:651-674`) looks like it might already prevent this and does not:
it is a `SELECT COUNT(*)` outside any transaction, counted globally per
type name, with a literal `// fail-open` comment on its error path.
Advisory, not a lock. The claim mechanism therefore reuses `Save`'s CAS
directly rather than layering on `Task`'s own transitions, because
state-changing writes are exactly the writes that lack atomicity, and the
one atomic write does not change status at all.

**2. Every lease-touching write echoes the `rev` it last read.** Claims,
renewals, the land-time holder check and reclaims, without exception.
`Node.Rev` has no `json` tag (`node.go:83`), mcp's update handler passes
caller arguments through unfiltered (`mcp/tool.go:414`), and `MCPUpdate`
restores only `ID`/`Slug`/`Status` after decoding (`module.go:2297-2339`).
A write that omits `rev` therefore has `MCPUpdate` seed the row's current
value into its own guard, satisfying the CAS by construction and degrading
the whole design to last-write-wins, silently, and indistinguishably from
correct behaviour under any single-threaded test. "Claims are CAS writes"
is the version that fails silently. "Claims are CAS writes that echo the
`rev` they read" is the version that does not.

**3. `Run` registers no `StateFlow`.** Its authoritative state lives in
`lease_holder` and `outcome`, guarded by the writes above, never in
`Status` through `validateTransition`. The nearest existing precedent
argues the opposite way and is a trap: `smeldr/agent` (`e589dcf`) registers
a real `StateFlow` for `AgentJob` (`flow/module.go:91-109`) and routes its
lifecycle through `Status`. Copying that shape here would look natural and
be wrong.

**4. The branch carries state, the worktree is a disposable cache.** Every
step ends with all work committed and pushed to the remote before the
process exits; a dirty exit is a step failure. Branch and directory names
derive from the `Run` row's own ID, never from timestamps or a
next-available scan, and are never reused. A cleanup that fails therefore
degrades to a disk-space problem rather than a correctness one, and no Run
depends on the previous Run's cleanup having succeeded.

**5. Landing is serialized and gated, never automatically rebased.**
Immediately before merge the listener fetches; if the branch's merge-base
with `main` is not `main`'s current tip, the Run does not merge. It
terminals as `needs-resync`, emits a `Signal`, and stops. Every sequential
human-facing number is allocated inside the land step, strictly after that
gate passes, never carried on a branch beforehand. This generalizes the
S-number rule this project already follows, and it is what makes the
M1/M2 trial's actual failure impossible by construction rather than merely
caught sooner.

### Alternatives considered and rejected

- **The `Task` state machine as the coordination point**, with a Task
  transition serving as the claim. Rejected on the source evidence in
  point 1: the transition path is precisely the path with no CAS.
- **The existing heartbeat `Signal` doubling as the lease.** Rejected, and
  the restart-on-failure design guarantees the divergence rather than
  merely permitting it: a listener crashes mid-Run, systemd restarts it in
  seconds, the new process heartbeats immediately with no memory of the Run
  it was running. A lease defined as "the holder's heartbeat is fresh"
  would call that Run validly held. Heartbeat answers "is listener L up"
  and never authorizes anything; the lease answers "does L validly hold Run
  R". Their timers stay two mechanisms that happen to share an interval,
  never merged later as an optimization.
- **Automatic rebase, or a merge queue now.** Rejected for M3. The M1/M2
  collision was semantic: two branches each internally consistent, cleanly
  rebase-able, both wrong about the same number. Rebase resolves text, not
  meaning. A merge queue is the eventual destination once throughput
  demands it, and it requires trusting unattended re-verification after an
  automated rebase, which is a trust decision reserved for Peter later, not
  an engineering one to assume now.
- **A shared "who is currently active on this repo" registry** that human
  and automated sessions both voluntarily check. Rejected: a voluntarily
  checked registry is a second source of truth that drifts, and then lies
  with authority it has not earned. The enforcement point stays singular,
  the land-time freshness gate, applied uniformly to both paths.
- **Treating any `ErrRevConflict` as "lost, stand down".** Rejected as a
  source of false fencing. The error only means the row changed since the
  last read, which an innocent write also causes. On conflict the correct
  response is to re-read: `lease_holder` empty means retry, `lease_holder`
  equal to self means the write already succeeded and its response was
  lost, a different holder or a terminal `outcome` means genuinely lost.

### Consequences

One prerequisite in `smeldr/mcp` is required before the fencing protocol
can work at all: `ErrConflict` and `ErrRevConflict` are two independently
constructed sentinels (`errors.go:59-66`, neither wrapping the other), so
`errorFor`'s `errors.Is(err, smeldr.ErrConflict)` branch never matches
`ErrRevConflict` and it falls into the generic `-32603` bucket
(`mcp/tool.go:101-116`), the same bucket a transient `SQLITE_BUSY` reaches.
Over MCP a listener currently cannot tell "lost the race" from "transient,
retry" except by matching error text.

Exclusivity of one Run per repo rests on topology for M3 (one listener per
repo, architect creating one Run at a time), not on a mechanism. Named
rather than attributed to the CAS, which guards a given row and says
nothing about a second row for the same repo. The mechanical version, a
persistent per-repo lock row claimed through the same CAS pattern, is not
built for M3 and becomes necessary the moment a second concurrent Run per
repo is real.

Lease TTL starts at 15-20 minutes, three to four missed heartbeats, as a
ratified estimate rather than a derived number. Expiry evaluation compares
a server-stamped `updated_at` against the evaluator's own clock, so the
TTL must stay orders of magnitude above plausible skew: minutes, never
seconds, whatever later tuning from real data recommends. A non-terminal
Run row is written only by its lease holder or by a reclaim that terminals
it, and by exactly one process, the listener, never its child. All other
commentary about an in-flight Run goes into a `Signal` referencing it,
because `Save` stamps `UpdatedAt` on every successful write by anyone, so
a human annotating a suspected-dead Run directly on the row would silently
extend the dead holder's lease.

Full design, five review passes with real defects found on each:
`smeldr/architect/design/m3-headless-worktree-isolation.md`.

Status: Ratified 2026-08-08.

---
