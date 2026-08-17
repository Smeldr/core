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
Archived 2026-08-17: A268-A270 → phase31-archive.md
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

## A273 — smeldrVersions reports the real build version under a replace directive (T219)

### Problem

`smeldrVersions()` (`smeldr.go`) reads `debug.ReadBuildInfo()` and, for
each dependency, uses `dep.Version` unconditionally — the nominal
`go.mod` require-line version, never checking `dep.Replace`. For any
binary built under a local `replace` directive (`example/server`'s own
`replace smeldr.dev/core => ../..`), this reports a version string
completely disconnected from what was actually compiled in — `/_health`
can silently lie about the running version. This is precisely the nuance
flagged to devops during T266's own close-out tonight: a `go.mod` pin
bump alone does not guarantee what a `replace`-built binary actually runs.

### Investigation

Built a minimal, throwaway reproduction to verify Go's actual runtime
behaviour rather than trusting memory of the `debug` package's API:

```
module example.com/main
require example.com/pkgA v1.2.3
replace example.com/pkgA => ./pkgA
```

`debug.ReadBuildInfo()` output:
```
Dep: Path="example.com/pkgA" Version="v1.2.3"
  Replace: Path="./pkgA" Version="(devel)"
```

Confirmed precisely: `dep.Version` holds the stale, nominal require-line
version regardless of what's actually linked; `dep.Replace` (non-nil for
any active replace) carries the real build source, and for a local
filesystem replace with no version pin, `dep.Replace.Version` is Go's own
standard `"(devel)"` marker — never empty, correcting an initial
assumption before it shipped.

Confirmed the only two callers of `smeldrVersions()` (`Health()`'s
`/_health` JSON, `Run()`'s startup log line) neither parse the returned
string as a real semver — both simply interpolate it, so passing through
`"(devel)"` is safe for both.

**No existing tests for `smeldrVersions()`/`Health()` at all** — a
separate, pre-existing gap this investigation surfaced. Worse: `core`
itself has zero dependencies and no `replace` directive of its own, so
`debug.ReadBuildInfo()` run inside a core package test can never naturally
exercise the replace-handling branch this fix adds.

### Fix

`parseSmeldrVersions` prefers `dep.Replace.Version` when a replace is
active:

```go
for _, dep := range info.Deps {
	version := dep.Version
	if dep.Replace != nil {
		version = dep.Replace.Version
	}
	add(dep.Path, version)
}
```

`"(devel)"` is passed through verbatim rather than translated to
different wording — it's Go's own already-recognized signal (the same
binary's own `Main.Version` uses it too for any locally-built binary), and
inventing different terminology for the same underlying state risks
reading as a *different* state to anyone who knows Go's own convention.

### Testability

Extracted `parseSmeldrVersions(info *debug.BuildInfo) map[string]string`
from `smeldrVersions()`'s own body — `smeldrVersions()` is now a thin
wrapper (`debug.ReadBuildInfo()` then delegate). A plain `*debug.BuildInfo`
needs no mocking to construct by hand, unlocking real coverage of the
replace-handling logic that core's own real build info can never
naturally exercise.

### Tests

`TestParseSmeldrVersions_NoReplace` (regression pin for existing
behaviour, non-`smeldr.dev/` dependency ignored), `_ReplaceWithVersion`
(a versioned/forked replace uses the replace's own version),
`_ReplaceLocalPath_ReportsDevel` (the direct T219 regression pin —
reports `"(devel)"`, not the stale nominal version), `_EmptyVersionSkipped`.
`smeldrVersions()`'s own `if !ok { return nil }` branch is genuinely
unreachable from a real running test binary — named, not chased, matching
this session's own precedent for structurally-unreachable branches.

### Versioning

`smeldrVersions`/`parseSmeldrVersions` both unexported — no new exported
symbol. Real consumer-observable behaviour change: `/_health`'s `"core"`
(and any companion module) field now reports honestly for a replace-built
binary instead of a stale version. PATCH bump, matching
A266/A269/A270/A272's own precedent. Coverage: 96.2% package-wide;
`parseSmeldrVersions` 100%. `go test -race ./...` clean. `golangci-lint`
zero findings. v1.72.1 → **v1.72.2**.

---
