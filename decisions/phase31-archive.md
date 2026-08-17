# Smeldr — Decisions Archive (Phase 31)

Archived from `decisions/recent.md` on 2026-08-17. Entries A268–A270.

---

## A268 — release.yml retries "confirm release exists" instead of failing outright (T265)

**Date:** 2026-08-17
**Status:** Proposed

**Context:** `.github/workflows/release.yml` triggers on tag push and, after
building `example/server`, checks that a GitHub Release already exists for
that tag before attaching a binary to it. The project's own documented
tag-then-release sequence (CLAUDE.md, "Tag and push sequence") pushes the tag
first — triggering this workflow immediately — then creates the Release as a
separate, later step. The check ran once, ~45-50s after the tag push (the
time the earlier build steps take), and failed outright if the Release
hadn't been created yet.

Hit for real 2026-08-17 tagging v1.70.0: an unrelated delay (fixing a
PowerShell encoding issue while extracting CHANGELOG release notes) pushed
`gh release create` past that ~50s window. The run failed with "release not
found," requiring a manual `gh run rerun --failed` once the release existed.
v1.70.1 and v1.71.0 avoided the same failure only because their release
notes were pre-extracted before tagging, keeping `gh release create` to a
few seconds after the push — an avoidable dependency on operator speed, not
something the workflow itself should require.

**Decision:** Replace the single check with a retry loop — 10 attempts, 5
seconds apart (~50s total) — before failing. Chosen to comfortably cover the
build-step latency plus an ordinary operator delay in creating the release,
without masking a genuinely absent release behind a long or silent wait.

**Developer pattern:** the tag-then-release sequence in CLAUDE.md is
unchanged — this Amendment does not ask for the release to be created before
the tag push, it only makes the workflow's own existing safety check
tolerant of that sequence's ordinary timing.

**Consequences:** CI config only. No Go code, no exported symbol, no
consumer-observable behaviour changed. No version bump, no tag — a workflow
file change is not itself something `example_test.go` or a README example
could break. Applies to `smeldr/core` only; the six standalone repos with
their own release automation are unaffected and out of scope for T265.

---

## A269 — resolveFlowID fails closed on a real DB error; CreateStateFlowTables exported (T249)

### Problem

`resolveFlowID` (`state.go`) collapsed a genuine DB error resolving a
type's state flow — SQLITE_BUSY, a cancelled context, a corrupted or
missing table — into `found=false, err=nil`, indistinguishable from "no
flow was ever registered for this type." `validateTransition` then
discarded that error entirely (`flowID, flowFound, _ :=
resolveFlowID(...)`) and returned `nil` when `!flowFound` — permitting the
transition with zero checks, including the required-role gate below it.
This directly contradicted D34's own fail-closed principle, already
correctly applied one call later for `lookupTransitionGate`'s identical
class of error (`gateErr != nil` returns `ErrInternal`). Named by the
architect during T243's own review, tracked as its own Task (T249).

### Investigation

Confirmed `transitionIsGated` (`provenance.go`), the second caller of
`resolveFlowID`, does **not** share the bug before assuming it did. Its own
contract is the opposite fail-closed direction from `validateTransition`'s
(withhold the actor on uncertainty, not reject the transition) — traced
`SubjectProvenance`'s use of `Gated` (`Gated: true` is what exposes
`ActorKind`/`ActorID`/`Surface`/`Reason`; `false` leaves them withheld).
`transitionIsGated` already returns `false` on any `resolveFlowID` error
today, and `false` is the safe, correct outcome for its own documented
contract (pinned by the existing
`TestTransitionIsGated_UnresolvableGate_FailsClosedNoActor`). No change
needed there.

### Fix

`resolveFlowID` distinguishes `sql.ErrNoRows` (legitimate "not found," the
type-specific lookup falls back to the default flow, or the default lookup
itself legitimately misses) from any other error (now propagated instead
of swallowed). `validateTransition` checks the new error and fails closed
with the same `ErrInternal` shape already used for `lookupTransitionGate`'s
sibling error path, immediately below it in the same function.

### A real, unplanned scope question surfaced during implementation

The fix correctly turned "no such table: smeldr_state_flows" from a
silently-ignored condition into a real, propagated error — which broke 7
pre-existing tests across `dynamic_test.go` and `relations_sweep_test.go`
that build a `DynamicTypeRepo`/status-transition path directly against a
raw `*sql.DB`, bypassing `smeldr.New` (which always calls
`migrateStateFlows` unconditionally when `Config.DB` is set), and so never
had the `smeldr_state_flows` table at all. These tests were never
exercising state-flow validation; they relied on the swallowed error to
skip it entirely, invisibly, until this fix.

Stopped and asked the architect before touching a file outside `state.go`/
`state_test.go`, presenting two options: route the shared test-DB helper
through `smeldr.New` (more coupling to unrelated App-bootstrap machinery
for one migration side effect), or export a narrow
`CreateStateFlowTables(db DB) error`, matching `CreateBlockTables`/
`CreateSchemaTable`'s own precedent as targeted, single-purpose exported
setup calls already used by the same test helper. **Architect's decision:
the latter, scoped narrowly** — extracted the five `CREATE TABLE IF NOT
EXISTS` statements out of `migrateStateFlows` into `CreateStateFlowTables`;
`migrateStateFlows` now calls it first, then proceeds with its own
unexported default-flow seeding, so there is exactly one place the schema
is defined. `openDynDB` (`dynamic_test.go`) and
`TestAppSweepStructural_DefaultChecker` (`relations_sweep_test.go`) both
call it in their setup now, matching every other `migrateStateFlows`-using
test in the package. `migrateStateFlows`'s own doc comment corrected in the
same edit ("four" → accurately describes the five tables via
`CreateStateFlowTables`).

### Tests

`TestResolveFlowID_QueryError` (type-specific query's real error is
propagated without falling through to the default-flow query — a DB error
is not "keep trying," unlike a legitimate miss),
`TestResolveFlowID_DefaultQueryError` (type-specific query legitimately
misses, default-flow query then hits a real error — also propagated),
`TestValidateTransition_flowResolutionError` (the direct regression pin:
`validateTransition` returns `ErrInternal`, not `nil`, when `resolveFlowID`
errors). All pre-existing `TestValidateTransition_*`/
`TestTransitionIsGated_*`/`TestSubjectProvenance_*` tests re-run unmodified
and pass — both legitimate-miss paths are unaffected by this fix. The 7
tests broken by the fix's own correctness (listed above) fixed by adding
`CreateStateFlowTables(db)` to their setup, not by weakening the fix.

### Versioning

`resolveFlowID`/`validateTransition` remain unexported — real
consumer-observable behaviour change (a transition attempted during a
genuine DB error previously succeeded silently; now correctly returns
`ErrInternal`), matching A266's own precedent (behaviour change, no
exported symbol → PATCH). `CreateStateFlowTables` is a new exported symbol,
Level 2 on its own merit, folded into this same Amendment per the
architect's own instruction (it exists strictly because this fix correctly
exposed a latent test-fixture gap, not as independent work). Coverage:
96.3% package-wide; `resolveFlowID`/`validateTransition`/
`CreateStateFlowTables` all 100%. `go test -race ./...` clean. PATCH bump:
v1.71.0 → **v1.71.1**.

---

## A270 — applyConflictPolicy resolves the real smeldr_-prefixed table (T229)

### Problem

`applyConflictPolicy`'s own static-table probe (`state.go`) checked only
the bare `<snake>s` form of a type's table name (e.g. `typeName="Task"` →
`"tasks"`) — never the `smeldr_<snake>s` form every one of the six
orchestration types' real tables actually uses (`smeldr_tasks`,
`smeldr_goals`, `smeldr_decisions`, `smeldr_amendments`, `smeldr_signals`,
`smeldr_runs`, confirmed directly against `orchestration.go`). A miss
silently fell through to treating the type as dynamic content, checking
conflicts against `smeldr_dynamic_content` instead of the type's own real
table — harmless while no orchestration type used a conflict policy, wrong
the day one does. Flagged in an earlier backlog audit, tracked as its own
Task (T229).

### Investigation

The already-correct reference implementation for this exact resolution
already existed in the same file: `resolveItemTable`, used by
`App.TransitionItem` and elsewhere, probes in order `smeldr_<snake>s`,
then `<snake>s`, then falls back to `smeldr_dynamic_content`. Existing
tests never caught the bug because `applyConflictPolicy`'s own test
fixture (`registerConflictFlow`) creates a table literally named
`conflict_types` — the bare form, matching the bug's own assumption on
both sides, never exercising the `smeldr_`-prefixed path at all.

### Fix

Replaced the inline probe with a direct call to `resolveItemTable` — DRY
reuse of the already-correct function rather than a second bespoke fix,
matching CLAUDE.md's own "check whether the logic already exists
elsewhere" directive:

```go
staticTable := resolveItemTable(ctx, db, typeName)
isDynamic := staticTable == "smeldr_dynamic_content"
```

`camelToSnake` remains used elsewhere (`state.go`, inside
`resolveItemTable` itself, `storage.go`) — not left dangling by the
removal of its third call site.

### Tests

New `TestApplyConflictPolicy_smeldrPrefixedTable`: creates a real
`smeldr_tasks` table (not the fixture's bare `conflict_types`), registers
a `ConflictReject` flow for `TypeName: "Task"`, inserts one row already in
the active state, and asserts `applyConflictPolicy("Task", ...)` returns
`ErrConflict` — proving the fix finds the real table instead of silently
checking `smeldr_dynamic_content`. All 18 pre-existing
`TestApplyConflictPolicy_*` tests re-run unmodified and pass — the
bare-form `conflict_types` fixture still resolves correctly under
`resolveItemTable`'s own fallback order (probe `smeldr_conflict_types`
first, miss, fall through to `conflict_types`, hit).

### Versioning

No exported symbol changed (`applyConflictPolicy`/`resolveItemTable` both
unexported). Real consumer-observable behaviour change: a conflict-policy
check against an orchestration type now correctly enforces against that
type's own real table instead of silently checking the wrong one. PATCH
bump, matching A266/A269's own precedent. Coverage: 96.3% package-wide;
`applyConflictPolicy` itself 95.5%. `go test -race ./...` clean. `golangci-
lint` zero findings. v1.71.1 → **v1.71.2**.

---

## A271 — RelationKindDef.ReverseLabel (T160)

### Problem

`RelationKindDef` had one `Label` field and no counterpart for the same
relation viewed from the target's own side (e.g. `"supersedes"` vs.
`"superseded by"`) — cloud maintained its own local workaround map rather
than a real field on the shared type.

### Investigation

Traced the full blast radius before proposing anything: the DDL
(`CreateRelationTables`), the shared `relationKindColumns` const used by
both `SELECT` and `INSERT`, `scanRelationKind`, `UpsertKind`'s
`INSERT ... ON CONFLICT DO UPDATE`, `ValidateRelationKindDef` (`Label`
itself is never validated — empty is valid), and
`RegisterOrchestrationRelationKinds` (`orchestration.go`), the one place in
this repo that actually defines relation-kind labels for the four seeded
kinds (`derives_from`, `depends_on`, `ships_as`, `supersedes`).

Confirmed `MCPUpsertRelationKind`/`MCPListRelationKinds` pass
`RelationKindDef` straight through with no separate mcp-repo param struct
— a new field reaches mcp automatically, no `smeldr.dev/mcp` change
needed (matching T214's own precedent shape).

### Design

`ReverseLabel string` (`db:"reverse_label"`), identical treatment to
`Label` in every respect: optional, unvalidated, zero-value = "not
provided," no requirement that it be set even when `Directional` is true.
Matching `Label`'s own already-optional character keeps this additive
rather than introducing an inconsistency between two sibling fields.

**Whether to populate the four seeded kinds — presented for review rather
than decided alone.** The Task's own example (`supersedes` →
`"superseded by"`) is concrete and directly actionable; the other three
kinds' correct reverse phrasing isn't stated anywhere in the Task or the
design docs, and inventing business terminology unprompted is exactly the
kind of unilateral call this session's own plan-first correction (T229)
exists to prevent. **Architect confirmed**: seed `supersedes` →
`"Superseded By"` only; leave the other three at the zero-value ("no
reverse label established yet" is the honest state until someone actually
decides the wording) — not a question for Peter to adjudicate three
placeholder strings, squarely an architect-level call.

### Migration

`CreateRelationTables` declares `reverse_label TEXT NOT NULL DEFAULT ''`
directly in its `CREATE TABLE` text (fresh installs) plus a paired
`EnsureColumn(ctx, db, "smeldr_relation_kinds", "reverse_label", "TEXT NOT
NULL DEFAULT ''")` call (pre-existing installs) — the same
declare-and-migrate-together shape established twice already this session
(A221's own precedent, reused by A264/T246). `CreateRelationTables` is the
single production entry point for these tables (confirmed via a full
call-site search: every test and `example/server/main.go` route through
it), so the migration call lives inside it once, not duplicated per
caller.

**Real, unplanned bug found and fixed along the way, in a shared test
double, not this Task's own code:** `failOnNthExecDB` (`coverage_test.go`,
used broadly for `CreateRelationTables`'s own `ExecContext`-failure test
suite) had a `QueryContext` stub returning `(nil, nil)` — a contract no
real driver ever honors (`database/sql` always returns either a valid
`*Rows` or a non-nil error). Harmless until now, because nothing in
`CreateRelationTables`'s call graph ever called `QueryContext` before
`EnsureColumn`'s own `PRAGMA table_info` probe. Caused a real nil-pointer
panic in `TestCreateRelationTables_ExecError2` (confirmed by actually
running the test, not assumed from reading the code — `sql.Rows.Close()`/
`.Next()` on a nil receiver panic). Fixed by making the stub return a real,
valid empty result set (`sql.OpenDB(&guardRowConn{noRow: true})`, the exact
pattern its own sibling `QueryRowContext` method already used one line
below) instead of `(nil, nil)`. This shifted `CreateRelationTables`'s own
`ExecContext` call count from 5 to 6 against this fake (it always reports
the column missing, so `EnsureColumn`'s `ALTER` always fires against it) —
added `TestCreateRelationTables_ExecError6` to keep every real statement's
own failure path covered, since the fake's own position-6 statement
(`idx_relations_governance_temporal`) would otherwise have gone untested
after the shift.

### Tests

`TestUpsertKind_ReverseLabel_RoundTrip` (Upsert → GetKind/ListKinds → a
fresh `NewRelationStore` re-hydration from the database, not only the
in-memory registry write), `TestUpsertKind_ReverseLabel_ZeroValue`
(unset persists as `""`), `TestCreateRelationTables_ExecError6` (see
above), `TestRegisterOrchestrationRelationKinds_RoundTrip` extended with
`ReverseLabel` assertions for all four kinds, `TestMCPUpsertRelationKind_OK`
extended to confirm `ReverseLabel` passes through the mcp surface
unchanged.

### Versioning

New exported field (`RelationKindDef.ReverseLabel`). MINOR bump, matching
A262/A267's own precedent (new exported symbol → MINOR). Coverage: 96.3%
package-wide; `CreateRelationTables`/`UpsertKind`/
`RegisterOrchestrationRelationKinds`/`MCPUpsertRelationKind` all 100%.
`go test -race ./...` clean. `golangci-lint` zero findings. v1.71.2 →
**v1.72.0**.

---

