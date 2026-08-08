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

## A238 — smeldr.dev/mcp: ErrRevConflict gets its own JSON-RPC code (M3 part 1)

### What happened

`errorFor` (`tool.go:101-116`) had explicit branches for `ValidationError`
(-32602), `ErrNotFound`, `ErrForbidden`, and `ErrConflict` (-32001). Everything
else — including `ErrRevConflict` — fell through to the generic `-32603
"internal error: ..."` catch-all. `ErrConflict` and `ErrRevConflict` are two
independently-constructed sentinel values in smeldr/core's errors.go (60, 65),
neither wrapping the other. This means `errors.Is(err, smeldr.ErrConflict)` can
never match an `ErrRevConflict`-carrying error, so both landed in the same
-32603 bucket, indistinguishable from transient errors like `SQLITE_BUSY`.

D38's fencing protocol (Run coordination for headless automation, M3) requires
listeners to reliably tell "I lost the lease race, re-read lease_holder" apart
from "something transient just happened, retry." Before this fix, both needed
the same -32603 response, distinguishable only by parsing message text.

### Why not add ErrRevConflict to the existing -32001 bucket

-32001 is already shared by `ErrNotFound`, `ErrForbidden`, and `ErrConflict`,
distinguished only by message text. Adding `ErrRevConflict` there would still
require message parsing to branch on "lost the lease race" vs. "forbidden" vs.
"not found" — exactly the string-matching fragility being fixed, just moved to
a different bucket.

### The fix

`ErrRevConflict` gets its own dedicated code: `-32002`. JSON-RPC reserves
-32000 to -32099 for implementation-defined server errors; -32001 was already
mcp's "handled application error" bucket, so -32002 is the next unused value
in that range.

```go
if errors.Is(err, smeldr.ErrRevConflict) {
    return &jsonRPCError{Code: -32002, Message: err.Error()}
}
```

Added as its own branch in `errorFor`, placed directly after the
`ErrConflict` branch for readability (order doesn't matter functionally since
the two sentinels never match each other's check).

The function's own doc comment was updated to list all five outcomes
(`ValidationError`, `ErrNotFound`, `ErrForbidden`, `ErrConflict`,
`ErrRevConflict`, plus the generic fallback). It had previously documented
none of the five branches explicitly — the `ErrConflict` branch in particular
was undocumented before this fix.

### Other sentinels checked, none needed the same treatment

Systematically read every sentinel in smeldr/core's errors.go: `ErrNotFound`,
`ErrGone`, `ErrForbidden`, `ErrUnauth`, `ErrConflict`, `ErrRevConflict`,
`ErrLastAdmin`, `ErrBadRequest`, `ErrNotAcceptable`, `ErrRequestTooLarge`,
`ErrTooManyRequests`, `ErrInternal`. Of the ones `errorFor` doesn't already
branch on (`ErrGone`, `ErrUnauth`, `ErrLastAdmin`, `ErrBadRequest`,
`ErrNotAcceptable`, `ErrRequestTooLarge`, `ErrTooManyRequests`, `ErrInternal`),
none are named or implied anywhere in D38 or its design document as something
a Run-coordinating listener's decision logic needs to distinguish. `ErrLastAdmin`
is governance/token-admin-tooling specific, unrelated to Run coordination.
No other change proposed.

### Tests

New test in `coverage_gap_test.go`, matching the existing
`TestErrorFor_NotFound`/`TestErrorFor_Forbidden` pattern exactly (bare
sentinel, since that's how `SQLRepo.Save` actually returns `ErrRevConflict`
— unwrapped, confirmed at `storage.go:774` in smeldr/core):

```go
func TestErrorFor_RevConflict(t *testing.T) {
    got := errorFor(smeldr.ErrRevConflict)
    if got.Code != -32002 {
        t.Errorf("ErrRevConflict: code = %d, want -32002", got.Code)
    }
}
```

Coverage: 96.4% (mcp package total, unchanged). No exported symbols changed.
No version-line change in mcp's own README (mcp doesn't have the same
version-line-gates-tagging convention as core), but mcp's own CHANGELOG.md
gets a new section and this is a real behavior change worth a patch tag — a
listener can now receive a JSON-RPC code (-32002) it's never seen before and
must handle. v1.29.3 → v1.29.4.

Flagged, not fixed (pre-existing, unrelated to this change, confirmed
identical on main before this work via `git stash` + `golangci-lint run`):
four pre-existing golangci-lint findings in `mcp_test.go` (unchecked
`pw.Write` calls) and `node_tools.go` (an ineffectual assignment) — none in
files this amendment touches.

Status: Ratified 2026-08-08.

---

## A239 — smeldr/core: the Run orchestration module (D38, M3 part 1)

### The type

New sixth orchestration content type in `orchestration.go`: `Run` — one row per
mechanical episode of headless automated work, from the moment a listener claims
it to the moment it merges or is abandoned.

Fields: `TaskID`, `Repo`, `Machine`, `Branch`, `WorktreePath`, `BaseSHA`,
`LeaseHolder`, `Outcome` (type `RunOutcome`), `Cleanup` (type
`RunCleanupState`), `AcknowledgedAt` (type `time.Time`).

Two new exported string-enum types, following the existing `ConflictPolicy`
type's own precedent (a plain `string`-based type with named constants):

- `RunOutcome` — values `RunMerged` ("merged"), `RunNeedsResync`
  ("needs-resync"), `RunStuck` ("stuck"), `RunFailed` ("failed"),
  `RunOrphaned` ("orphaned"). Empty string means the Run is still in-flight;
  every named value is terminal, including `RunNeedsResync`, whose continuation
  is always a new Run branching from this one, never a resumption of this row.

- `RunCleanupState` — values `RunCleanupPending` ("pending"), `RunCleanupDone`
  ("done").

### Deliberately no StateFlow

Unlike the other five orchestration types, `Run` registers no `StateFlow`. Its
authoritative state lives entirely in `LeaseHolder`/`Outcome`, guarded by
`SQLRepo.Save`'s rev-based compare-and-swap, never in the inherited
`Node.Status` field via `validateTransition`.

D38 itself names the wrong, natural-looking precedent to avoid:
`smeldr/agent`'s `AgentJob` type registers a real `StateFlow` and routes its
whole lifecycle through `Status` — copying that shape for Run would look
natural and be wrong, since Run's coordination fundamentally needs the one
atomic compare-and-swap the framework has (`SQLRepo.Save`'s rev-CAS), which
`DynamicTypeRepo`/`validateTransition`-based state changes don't provide at all.

### Every Run stays Draft forever — named explicitly, not left as an implicit accident

Because no code path ever calls anything that would publish a Run, every Run
row's `Node.Status` field stays at `Draft` for the row's entire life. Verified
this doesn't hide Run rows from a listener's own reads: `MCPList`
(`module.go:2154`) applies no draft-visibility filtering of its own — it
returns everything the underlying repo's `FindAll` returns unless the caller
explicitly requests a status subset.

This property is stated directly in `Run`'s own godoc comment, not left as an
implicit, undocumented side effect.

### Extending the existing registration functions, not adding a parallel pair

`CreateOrchestrationTables` and `RegisterOrchestrationTypes` were extended in
place — a new `CREATE TABLE IF NOT EXISTS smeldr_runs` statement joined the
existing statement list; a new `app.Content(NewModule[*Run](...))` call joined
the existing five. The `flows` slice was NOT touched — no `orchRunFlow()`
function exists, matching the no-StateFlow decision directly.

Both functions' doc comments were updated from "five" to "six" throughout. This
was a deliberate choice against building a parallel `CreateRunTable`/
`RegisterRunType` pair: D38 itself frames Run as "a sixth typed orchestration
module," not a separate concept, and duplicating the wiring shape for one type
would be unnecessary parallel structure.

`Run` is registered with `MCP(MCPRead, MCPWrite)`, matching its five siblings.
This is not just a consistency choice: D38's own claim/renewal/land-time-holder-check
writes are all designed to travel the MCP `update` tool's path
(`mcp/tool.go`'s `handleToolsCall` calls `MCPUpdate`), so write access is a
real requirement for the future M3 listener to be buildable at all, not a style
preference.

### AcknowledgedAt: caught and fixed before building, not assumed safe

The field gating deletion of a preserved stuck/failed/orphaned Run's worktree
was originally planned as `AcknowledgedAt *time.Time` (a pointer, nil meaning
"not yet acknowledged"). Verified directly before writing any code, not
assumed: this would have been the first `*time.Time` custom field on any
SQLRepo-backed content type in the codebase. `Node.ScheduledAt` and
`RelationEdge`'s own timestamp fields looked like they might be evidence it
would work — checked directly, addressed below, and turned out to be a real
finding of their own, not settled evidence either way.

Traced the actual mechanism (`storage.go`): `scanDest` only special-cases a
scan destination whose Go type is `*time.Time` — which is exactly what you get
when you take the address of a field whose OWN declared type is plain
`time.Time` (i.e. `&someTimeTimeField` is `*time.Time`). A field declared as
`*time.Time` itself has an address of `**time.Time` (pointer to the pointer
field), which does not match `scanDest`'s type assertion and falls through to
the raw, unwrapped scan destination — meaning it would receive SQLite's
string-formatted TIMESTAMPTZ value with no parsing logic able to handle it,
since the custom `timeScanner` wrapper (needed because "Go 1.26's database/sql
convertAssign does not handle string→*time.Time natively," per its own
comment) is never engaged for a `**time.Time` destination.

Fixed to `AcknowledgedAt time.Time` (not a pointer), with the zero value
meaning "not yet acknowledged" — matching `Decision.NextEvalAt`'s own
already-proven, already-tested nullable-timestamp convention. This is a
better, more consistent choice than the original pointer design, not merely a
workaround forced by a bug.

**Second finding, caught during architect review of the first draft of this
comment, not before**: the first draft cited `Node.ScheduledAt` alongside
`NextEvalAt` as a second working precedent for the zero-value convention.
`Node.ScheduledAt` is itself `*time.Time` (`node.go:70`) — not a `time.Time`
precedent at all, and the sentence contradicted itself as written. Resolving
which of the two contradicting claims was true meant checking directly rather
than picking one and moving on: does `Node.ScheduledAt` genuinely work as a
`*time.Time` through this same generic path (meaning the `scanDest` reasoning
above is incomplete), or was the citation simply wrong?

Wrote a throwaway test (not committed) exercising `Node.ScheduledAt` through
a real `NewSQLRepo`-backed table, matching the exact `Save`/`FindByID` path
`AcknowledgedAt` would use. A nil `ScheduledAt` round-trips fine — `database/sql`'s
built-in `**T` handling sets the pointer to nil on a nil driver value with no
string-parsing needed. A **non-nil** `ScheduledAt` reproduces `scanDest`'s
exact failure: `sql: Scan error ... unsupported Scan, storing driver.Value
type string into type *time.Time`. Checked whether every existing test that
looked like it exercised this was actually testing the SQL-backed path: it
wasn't — `TestFull_scheduler_publishesOverdue`, `TestFull_scheduler_appWiring`,
and every other test asserting a non-nil `ScheduledAt` use `NewMemoryRepo`,
which never touches `scanDest` or SQLite scanning at all. The only assertions
against a real `FindByID` result check the **nil** case (the field cleared
after publishing) — never a genuine non-nil round-trip against `SQLRepo`.

**This is very likely a real, previously-unnoticed latent bug in
`Node.ScheduledAt`** — not fixed here, per the architect's own instruction:
flagged as its own follow-up task, out of this Amendment's scope. The citation
itself is corrected to name only `Decision.NextEvalAt` as the real precedent.

### The rev-echo contract: documented, not tested — stated plainly rather than faked

D38's own text: any lease-touching write that omits `rev` from its update
payload silently degrades to last-write-wins, indistinguishable from correct
behaviour under any single-threaded test — and this is true of any test
written for this module too.

This cannot be tested at the layer this task builds, and that's stated directly
rather than manufacturing a test that looks like coverage and isn't: the failure
mode lives entirely in how a future caller (the M3 listener, not built by this
task) constructs its MCP update payload — whether it includes `rev` or not.
Nothing in `orchestration.go` decides that; this task builds no such caller.

The contract itself is documented directly on `Run`'s own godoc comment so the
eventual listener implementer sees it at the type definition, not only in this
Decision's own text.

What IS tested, a different and real claim this task's own code does make:
`TestRun_SaveRevConflict` proves `Run` genuinely participates in
`SQLRepo.Save`'s real compare-and-swap — not a test of future listener
discipline, but proof the type is correctly wired onto the one atomic
primitive the whole design depends on. Mirrors the existing
`TestSQLRepo_Save_RevConflict` test's exact pattern: save an item (stored rev
0→1), save again with a stale rev value (0), assert `ErrRevConflict`.

### Tests

- `TestOrchestrationTypes_embedNode` — new `Run` sub-test.
- `TestCreateOrchestrationTables` — `smeldr_runs` added to the table-existence
  check.
- `TestRegisterOrchestrationTypes_flows` — the flow-count assertion stays at 5
  (not 6), PLUS a new, load-bearing assertion: a direct query confirming zero
  `smeldr_state_flows` rows exist for `type_name='Run'`. This second check
  matters because an unchanged count of 5 alone couldn't distinguish "Run
  correctly has no flow" from "Run's module registration was silently forgotten
  entirely."
- New `TestRun_SaveRevConflict` (above).

### AGENTS.md also corrected — a real, pre-existing staleness found in passing

AGENTS.md's orchestration-types section said "Four built-in types" and its
table listed only Signal/Task/Decision/Amendment — `Goal` (added earlier by
Amendment A198) had never been added to that table at all, a pre-existing
staleness unrelated to this task but directly in the same section being edited.

Corrected to six types, with both Goal and Run added to the table, and a new
paragraph added warning that Run.status is inert and AI assistants should read
LeaseHolder/Outcome instead.

### Companion mcp-repo change

A238 (this same work cycle, `smeldr.dev/mcp`, no core-package files touched)
is the prerequisite: `ErrRevConflict` gets its own JSON-RPC code, -32002,
needed before D38's fencing protocol can work at all. No docs/ARCHITECTURE.md
entry for A238, matching A227/A228's own precedent (standalone-module-only
changes don't get entries in core's own architecture doc).

### Coverage and versioning

No exported symbols removed anywhere. Coverage: 96.2% (core package total,
unchanged from baseline). MINOR version bump — new exported API surface (Run,
RunOutcome, RunCleanupState, and their constants), same classification as
Amendments A235/A236's own new-type additions. v1.60.1 → v1.61.0.

Status: Ratified 2026-08-08.

---

## D39 — Module paths: `smeldr.dev/x` is for published modules only, private repos use `github.com/Smeldr/x`

### Scope

cross-repo

### Decision

A repo's Go module path is decided by whether the module is *published for
others to import*, not by who owns it or how it feels:

- **`smeldr.dev/<name>`** — public modules intended to be fetched as a
  dependency. Requires a matching vanity entry in `smeldr/site-dev`'s own
  handler list, and is not optional: the path resolves only because
  `smeldr.dev` serves a `go-import` meta tag for it. Current entries are
  `core`, `pgx`, `social`, `agent`, `cli` (dedicated handlers) plus `mcp`,
  `media`, `oauth` (via `vanityGoGetMiddleware`, which avoids colliding with
  those paths' existing site routes).
- **`github.com/Smeldr/<name>`** — private repos. Resolves directly with no
  vanity entry, no `smeldr/site-dev` change, and no deploy of the site to add
  a new one. Proven in real use: `smeldr/cloud`'s `go.mod` depends on
  `github.com/Smeldr/mail` at a pseudo-version and fetches it successfully.

The next private repo, `smeldr/runner` (the M3 listener, not yet created), uses
`github.com/Smeldr/runner` under this decision.

### Why, beyond consistency

Two reasons that are not preference:

1. **A vanity path is infrastructure, not a name.** `smeldr.dev/<name>` is
   inert unless someone adds it to `site-dev`'s list and redeploys the site.
   Choosing a vanity path for a private repo silently takes on that dependency
   and produces a module path that cannot be resolved until the work is done.
2. **`smeldr.dev` is the public framework site.** Serving a `go-import` meta
   tag for a private repo advertises that the repo exists and where it lives,
   to anyone who asks, and then fails on authentication. Publishing a pointer
   to something deliberately private is the wrong default.

### `smeldr/cloud` is inconsistent with this rule and stays that way

`smeldr/cloud` declares `module smeldr.dev/cloud` and has **no** vanity entry in
`site-dev`'s list. Verified directly against `site-dev/main.go:622-627` and its
`vanityGoGetMiddleware`. `go get smeldr.dev/cloud` cannot resolve.

That is not a latent bug, and this decision explicitly does not create follow-up
work from it. `smeldr/cloud` is Smeldr Cloud itself, the commercial product: a
leaf application, deliberately private, with no reason any module should ever
import it. A module path that cannot resolve costs nothing when nothing will
ever ask it to resolve.

What the case actually demonstrates is the rule's own point from the other
direction. `cloud` took the vanity path, never did the `site-dev` work that
would make it real, and nothing broke, because the vanity path was never
buying it anything in the first place. The choice was pure cost with no
benefit, and the only reason that stayed invisible is that the cost was also
zero. For a private repo that *is* imported, the same choice would not have
been free: `smeldr/mail` would have needed a public meta tag on `smeldr.dev`
before `smeldr/cloud` could depend on it at all, and it correctly took the
plain path instead.

Changing `cloud`'s module path would touch every import in the repo to gain
nothing. Left as is, deliberately. Recorded here so the inconsistency reads as
a settled judgment rather than an oversight nobody noticed.

### Alternatives considered and rejected

- **Vanity paths for everything, adding private repos to the site's list.**
  Rejected on reason 2 above, plus it makes creating a private repo require a
  public site deploy.
- **Plain GitHub paths for everything, retiring the vanity scheme.** Rejected:
  the vanity paths are a real published promise on eight modules, `smeldr.dev/core`
  is in every consumer's `go.mod` and in public documentation, and breaking that
  to gain internal uniformity is a bad trade.

Status: Ratified 2026-08-08.

---
