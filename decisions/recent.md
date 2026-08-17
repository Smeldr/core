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
Archived 2026-08-13: D48-D49, A252-A255 → phase26-archive.md
Archived 2026-08-15: D50-D56, A256-A257 → phase27-archive.md
Archived 2026-08-16: A258-A260, D57-D58 → phase28-archive.md
Archived 2026-08-17: A261-A264 → phase29-archive.md
Archived 2026-08-17: A265-A267 → phase30-archive.md
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

## A272 — RegisterFlow no longer orphans a row on rename; live duplicate self-heals (T268)

### Problem

A live production incident, found 2026-08-17 during T260's own deploy
verification: `get_valid_transitions` on a real `Task` still didn't list
`resolved` (T255/A261), even though the deployed binary was confirmed
`v1.71.0` and `orchTaskFlow()` in that code correctly defines it.

Confirmed against the live database (read-only queries):
`smeldr_state_flows.type_name='Task'` had **two** rows — the old
`architect-task` (9 states, no `resolved`) and the new `agent-task` (10
states, `resolved` present and correct). `Goal`'s own flow, never renamed,
had exactly one row and `resolved` worked correctly there — isolating this
as a rename-collision, not a broader bug.

### Root cause

`RegisterFlow`'s upsert (`INSERT ... ON CONFLICT (name) DO NOTHING`) keyed
on `name`, not `type_name`. The D50-era flow rename `architect-task` →
`agent-task` (T231) changed `flow.Name` in code while `flow.TypeName`
stayed `"Task"` — on the first deploy after that rename, `RegisterFlow` saw
a genuinely new `name` value and **inserted a second row** instead of
updating the existing one. The old row was never cleaned up.
`resolveFlowID`'s own query (`SELECT id FROM smeldr_state_flows WHERE
type_name = $1 LIMIT 1`, no `ORDER BY`) picked whichever row SQLite
happened to return first — on the live instance, the stale one.

### Design — two parts, both required together

**Part 1, prevent recurrence.** `RegisterFlow`'s upsert re-keyed on
`type_name`:

```sql
INSERT INTO smeldr_state_flows(id, name, type_name, description)
VALUES ($1, $2, $3, $4)
ON CONFLICT (type_name) DO UPDATE SET
    name        = EXCLUDED.name,
    description = EXCLUDED.description
```

matching `UpsertKind`'s own already-correct, already-established pattern
for `smeldr_relation_kinds` (`type_name TEXT NOT NULL UNIQUE`, `ON CONFLICT
(type_name)`) — the architect named this precedent directly during T160's
own review, before T268 was even dispatched. The flow-ID read and the
`active_state`/`conflict_policy` `UPDATE` immediately after both switched
off of `name` too (`WHERE type_name = $1` and `WHERE id = $3` using the
already-fetched `flowID`, respectively) — removes every remaining reliance
on `name` as an identity lookup in this function.

New `CREATE UNIQUE INDEX IF NOT EXISTS idx_state_flows_type_name ON
smeldr_state_flows(type_name)` — required for `ON CONFLICT (type_name)` to
be valid SQLite at all. `RegisterFlow` always requires a non-empty
`TypeName` (rejects `""` on entry); the default flow (`type_name IS NULL,
name='default'`) is seeded by a completely separate raw `INSERT` inside
`migrateStateFlows`, never through `RegisterFlow` — confirmed, not
assumed, so this index never has to reconcile with a NULL `type_name` row
from `RegisterFlow`'s own path, and standard SQL treats NULLs as mutually
non-conflicting in a UNIQUE index regardless.

**A real behavioural upgrade, flagged explicitly rather than left implicit
in the diff**: the old `ON CONFLICT (name) DO NOTHING` never updated
`description` on a re-registration (only `active_state`/`conflict_policy`
were kept current via a separate `UPDATE`). `DO UPDATE SET description =
EXCLUDED.description` now keeps `description` current too, matching the
same "code's own current definition wins" philosophy already applied to
the other two fields — a strict improvement, not a risk, since nothing in
the codebase depends on `description` staying frozen after first insert.

**Part 2, heal existing duplicates — including the live one, and any other
install carrying the same pattern, not just this one database.** A
duplicate `type_name` is *always* a bug state, never a legitimate design:
`resolveFlowID` already assumes exactly one row per type via its own
unqualified `LIMIT 1`, so a second row was already silently unreachable
through the correct code path even before this fix — just still occupying
space and, as this incident showed, at risk of being the one that wins an
undefined-order race.

New unexported `migrateDuplicateStateFlowRows(ctx, db) error`
(`migrate.go`), called from `migrateStateFlows` right after
`CreateStateFlowTables(db)` succeeds and **before** the new unique index —
ordering is load-bearing: the index creation would otherwise fail against
any install still carrying the duplicate, not merely a stylistic choice.

1. Finds every `type_name` with more than one row.
2. For each, keeps the row with the **latest `created_at`**, removes the
   rest. Argued, not guessed: `RegisterFlow`'s own `INSERT` never sets
   `created_at` explicitly, relying on the DDL's own `DEFAULT
   CURRENT_TIMESTAMP` — so "latest `created_at`" means precisely "the row
   the most recent successful `RegisterFlow` call actually created," which
   for a rename is exactly the new, correct definition. Checked against
   the actual live incident before trusting the rule in the abstract:
   `agent-task` (the correct survivor) really was created after
   `architect-task` by construction, since the rename happened later — the
   rule is also deliberately general rather than special-cased to that one
   fact, so it holds for any future duplicate regardless of whether a
   rename happens to add states too.
3. Deletes each orphan's own `smeldr_transition_triggers` (via a subquery
   on `smeldr_transitions.flow_id`), then `smeldr_transitions`, then
   `smeldr_states`, then the `smeldr_state_flows` row itself, in that
   order — no `ON DELETE CASCADE` exists in this schema and SQLite doesn't
   enforce foreign keys by default here, so orphaned child rows would
   otherwise survive silently.
4. Logs each removal at `slog.Warn` (`type_name`, `removed_id`,
   `kept_id`) — a real defect existed and was silently corrected, which an
   operator should notice, matching D34's own fail-loud lineage rather
   than staying quiet about a self-healing data change.

**Deliberately not done**: a one-off manual `DELETE` against the live
instance — T268's own dispatch explicitly ruled this out (the same
collision would recur on the next rename anywhere in the codebase without
the underlying fix); the migration above is general enough to self-heal
this instance (and any other affected install) on its next restart, which
T260's own redeploy will perform anyway.

### Tests

`TestRegisterFlow_rename_updatesInPlace` (the direct regression pin — same
`TypeName`, new `Name`, asserts exactly one row survives, the old row's own
states are gone, `description` updated too), `TestMigrateDuplicateStateFlowRows_KeepsLatest`
(reproduces the live incident's exact shape at the SQL level, confirms the
newer row survives and every one of the orphan's own child rows —
triggers, transitions, states — are gone), `_NoDuplicates_NoOp`,
`_MultipleGroups` (two independent duplicate groups handled
independently), `_FindGroupsError`, `_ListRowsError`,
`TestDeleteOrphanedStateFlow_ExecErrors` (table-driven, all four sequential
deletes), `TestMigrateStateFlows_DuplicateCleanupError`,
`_CreateIndexError`, `_HealsExistingDuplicate` (through the real
`migrateStateFlows` entry point, not the unexported function directly —
confirms sequencing, not just the function in isolation, and that the
unique index is genuinely enforced afterward). All 19 pre-existing
`TestRegisterFlow_*` tests re-run unmodified and pass — checked before
implementing, not after, that their fault-injection fakes are content-blind
to the SQL text changes (call-count-based, not query-string-based).

### Versioning

`RegisterFlow`/`resolveFlowID`/`migrateStateFlows`/
`migrateDuplicateStateFlowRows` all unexported or unchanged exported
signatures — no new exported symbol. Real consumer-observable behaviour
change (a rename no longer orphans a row; `description` now updates on
re-registration). PATCH bump, matching A266/A269/A270's own precedent.
Coverage: 96.2% package-wide; `RegisterFlow` 97.2%,
`deleteOrphanedStateFlow` 100%. `migrateDuplicateStateFlowRows`'s remaining
gap (`Scan`/`.Err()` post-iteration checks on `groups`/`idRows`) is the
same structurally-hard-to-trigger-with-a-real-driver class already accepted
elsewhere this session (A264, A267, T249) — named, not chased.
`migrateStateFlows`'s own 75.0% is unchanged from T249's own already-
accepted baseline; both of this Amendment's own new lines inside it are
covered. `go test -race ./...` clean. `golangci-lint` zero findings.
v1.72.0 → **v1.72.1**.

---
