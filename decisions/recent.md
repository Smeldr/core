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

---

## A220 — Transition provenance (T149)

### What

`provenance.go` (new file): `ProvenanceRecord` (ID, Timestamp, SubjectType, SubjectID,
Verb, FromState, ToState, ActorKind, ActorID, Surface, Reason), `ProvenanceFilter`
(From, To, SubjectType, SubjectID, ActorID) exported types; `ProvenanceStore` interface
(`Append`, `List`); `NewProvenanceStore(DB)`; `CreateProvenanceTable(DB)`;
`App.Provenance(store ProvenanceStore) *App` — subscribes to the 7 completed-transition
lifecycle signals (`AfterCreate`, `AfterUpdate`, `AfterPublish`, `AfterUnpublish`,
`AfterSchedule`, `AfterArchive`, `AfterDelete`) via the existing signal bus and writes
one `ProvenanceRecord` per event through unexported `recordProvenance` — fail-open,
logs and swallows store errors, mirroring `App.Audit`'s own discipline exactly.
`provenanceVerbFor`/`currentStatusOf`/`actorKindFor` unexported helpers.

`relations.go`: `RelationStore.provenanceStore` field + `setProvenanceStore`, wired
from `App.Handler()` regardless of whether `App.Relations()` or `App.Provenance()` was
called first; `insertEdge` calls new `recordAssertProvenance`, which recovers the
calling actor via a `ctx.(Context)` type assertion — the same precedent already present
in this file (`txBeginner`) and in `dynamic.go` (`smeldrCtxAccessor`). No signature
change to `Assert`/`MCPAssertRelation`/`MCPProposeRelation`; no `smeldr.dev/mcp` change.
`CreatedByJob` takes priority over a ctx-derived human actor when both are present.

`state.go`: `Transition.RequiredReason bool` field; `validateTransition` (unexported —
no compatibility concern) gains a final `reason string` parameter and a new fail-closed
check ahead of the existing `RequiredRole` check: `required_reason` set and
`reason == ""` → `ErrBadRequest`.

`migrate.go`: `migrateTransitionReasonColumn` adds `smeldr_transitions.required_reason`
(idempotent `PRAGMA table_info` probe + conditional `ALTER TABLE`, identical shape to
`migrateStateFlowConflictColumns`).

`dynamic.go`: `DynamicTypeRepo.SetStatus` refactored into a thin wrapper over new
unexported `setStatus`; new `SetStatusWithReason(ctx, id, status, reason string) error`
is the one concrete entry point in this task's scope that can satisfy a `RequiredReason`
gate.

### Why

The design spike (`transition-provenance.md` v3, `smeldr/architect/design/`) established
that neither `AuditRecord` (4 of 8 lifecycle events, `ContentType`+`Slug`-keyed, no actor
identity) nor `RelationEdge` (`CreatedByJob` only — no human-actor field, no verb, no
timestamp-as-event-log) can answer "who did this, and why" across every governed
write path. This amendment builds the missing primitive.

### Design decisions

1. **Purely additive — `AuditRecord`/`AuditStore`/`App.Audit`/`GET /_audit` are
   untouched.** The design doc's original Decision 1 ("`ProvenanceRecord` replaces
   `AuditRecord`") was rejected during planning: `CHANGELOG.md`'s own v1.0.0 API
   stability promise ("no breaking changes without a new major version") covers
   `AuditRecord`/`AuditStore`/`NewAuditStore`/`CreateAuditTable`/`App.Audit`/
   `AuditFilter`, all exported since A97/v1.22.0. `smeldr_provenance` is a new, separate
   table. Consequence, accepted deliberately: the 4 events `App.Audit` already covers
   (`AfterPublish`, `AfterSchedule`, `AfterArchive`, `AfterDelete`) now also get a
   `ProvenanceRecord` entry, from the same call site with the same data — deliberate
   redundancy, not two competing truths, since both writes originate from one signal
   dispatch and can never disagree.
2. **Relation-assertion actor recovery via type assertion, not a signature change.**
   The design doc's open question offered two options (breaking `Assert`/etc. params,
   or parallel `AssertWithActor`-style methods). A third option, not listed in the
   design doc: since `smeldr.Context` embeds `context.Context` directly (`context.go`),
   a `smeldr.Context` value passed into a `context.Context`-typed parameter retains its
   full concrete method set at runtime, recoverable via `ctx.(Context)`. Zero signature
   change, zero `smeldr.dev/mcp` coordination, and precedent already exists twice in the
   codebase for exactly this pattern. Confirmed independently by the architect against
   `context.go` and `mcp/relation_tools.go` before approval.
3. **`ActorKind` default is `"human"` when an actor ID is present, empty otherwise.**
   `"agent"` is deliberately reserved, unused, for a future concept that does not yet
   exist in the codebase — not a placeholder guess.
4. **`RequiredReason` enforcement is fail-closed**, matching `RequiredRole`'s existing
   discipline in the same function, and distinct from provenance recording itself
   (fail-open, matching `validateTransition`'s structural-error zone and
   `applyConflictPolicy`'s precedent).
5. **`SetStatusWithReason` added as a new method, not a `SetStatus` signature change.**
   None of the 6 pre-existing `validateTransition` call sites (`module.go` ×4,
   `dynamic.go`'s `ScheduleContent`) can supply a non-empty reason today — changing
   `MCPPublish`/`MCPSchedule`/`MCPArchive`/`updateHandler`'s exported signatures to
   thread a reason through would be a materially larger, riskier breaking change than
   this task's scope, for a gate (`RequiredReason`) that no shipped flow currently sets
   to `true`.

### Known gap, flagged rather than silently left

`MCPPublish`/`MCPSchedule`/`MCPArchive`/`updateHandler`, and the `smeldr.dev/mcp`
`transition_item` tool, still have no way to supply a reason — a `RequiredReason: true`
transition is only reachable end-to-end through `DynamicTypeRepo.SetStatusWithReason`
today. Full MCP-surface reachability (a `reason` argument on `transition_item`) is
deferred to a future task, not silently dropped.

### Consequences

- New exported symbols: `ProvenanceRecord`, `ProvenanceFilter`, `ProvenanceStore`,
  `NewProvenanceStore`, `CreateProvenanceTable`, `App.Provenance`,
  `Transition.RequiredReason`, `DynamicTypeRepo.SetStatusWithReason`. No existing
  exported symbol changed, removed, or deprecated.
- New table: `smeldr_provenance`. `smeldr_transitions` gains one column
  (`required_reason`, additive, idempotent migration).
- 30+ new tests across `provenance_test.go` (new), `relations_provenance_test.go`
  (new), plus additions to `state_test.go` and `dynamic_test.go`. Package coverage:
  96.1%.
- Level 2 amendment (new exported symbols, new table, behaviour change in
  `insertEdge`/`validateTransition`, cross-file consequences).

Level 2 amendment.

---

## A221 — T149 hotfix: `required_reason` missing from fresh-install CREATE TABLE

### What

`migrate.go`'s `smeldr_transitions` `CREATE TABLE IF NOT EXISTS` statement now
includes `required_reason BOOLEAN NOT NULL DEFAULT FALSE` directly, matching the
existing precedent where `active_state`/`conflict_policy` are declared inline on
`smeldr_state_flows`'s own CREATE TABLE, not only added via a follow-on migration.
`migrateTransitionReasonColumn`'s godoc is updated to state its actual role: it
upgrades a pre-existing SQLite database created before A220; it does nothing for a
fresh install on any engine, since the column is now present from CREATE TABLE.

### Why

A220 (T149, merged as `aa67139`) added `required_reason` only via
`migrateTransitionReasonColumn`, an idempotent `PRAGMA table_info` probe + `ALTER
TABLE`, gated to silently no-op on any DB where the PRAGMA query itself errors —
the same fail-open pattern `migrateStateFlowConflictColumns` already established
for upgrading existing SQLite databases. That pattern is correct for an *upgrade*
path, but `smeldr_transitions`'s CREATE TABLE statement itself was never updated to
include the column — unlike `active_state`/`conflict_policy`, which are declared in
both places. Consequence: a fresh Postgres install (`smeldr.New` against a real,
empty Postgres database — exactly what the pgx integration test suite does) got a
`smeldr_transitions` table with no `required_reason` column at all, and
`migrateTransitionReasonColumn` silently treated the resulting PRAGMA query error as
"non-SQLite, schema assumed current" — masking the real gap instead of catching it.

Caught by CI's `Test (pgx integration)` job on the A220 push (`TestIntegration_
Postgres_StateFlows` failed: `column "required_reason" of relation
"smeldr_transitions" does not exist`), not locally — the standard `go test ./...`
run from `core`'s own module never builds or exercises the separate `core/pgx`
submodule, and its integration-tagged tests require a live Postgres instance CI
wires in via `services:` + a temporary `go mod edit -replace`.

### Consequences

- No exported Go symbols changed.
- Fresh installs on any DB engine (SQLite or Postgres) now get `required_reason`
  from the CREATE TABLE statement itself; `migrateTransitionReasonColumn` continues
  to serve pre-existing SQLite databases only, as originally intended.
- No test changes needed — `TestMigrateTransitionReasonColumn_idempotent`'s own
  existing comment (`newMigratedDB(t) // migrateStateFlows already adds the
  column`) already assumed this exact behaviour; the bug was only in the CREATE
  TABLE SQL, not in any test's expectations.
- Patch release: v1.57.0 → v1.57.1. No API change.
- Level 1 amendment.

---

## A222 — Webhook/outbound TIMESTAMPTZ scan bug

### What

`webhook.go`'s `List` and `EndpointsForEvent`, and `outbound.go`'s `fetchDueJobs`,
`ListJobsForEndpoint`, `ListDeliveryLogs`, and `DeliveryStats` all scanned columns
documented and created as `TIMESTAMPTZ` (`created_at`, `next_retry_at`,
`expires_at`, `attempted_at`) directly into `time.Time`/`*time.Time` destinations
via plain `rows.Scan`/`row.Scan` calls. `modernc.org/sqlite`'s driver only
auto-converts the exact column-type names `DATE`, `DATETIME`, and `TIMESTAMP` to
`time.Time` on scan — `TIMESTAMPTZ` is not on that list, and the scan fails hard:

```
sql: Scan error on column index N, name "...": unsupported Scan, storing
driver.Value type string into type *time.Time
```

`fetchDueJobs` is the core polling function the worker pool's background loop
calls every cycle to find due webhook jobs. Against the actually-documented,
required production DDL, outbound webhook delivery was completely non-functional
— not a partial degradation.

### Why undetected

Every test table across `webhook_test.go` (5 occurrences), `outbound_test.go` (1
shared helper), and `integration_full_test.go`'s G26–G29 cross-milestone groups
(the most production-realistic tests in the suite — G26 literally calls
`WebhookPool().ListJobsForEndpoint` after a real `MCPPublish`) declared these
columns as `DATETIME` instead of the documented `TIMESTAMPTZ`. Confirmed:
`modernc.org/sqlite`'s driver keys off the column's declared type name
(decltype), not the stored value's actual string format — so this was a pure
test/production DDL divergence masking a real, severe bug through 96%+ package
coverage the whole time.

### Fix

1. **Non-nullable fields** (`List`, `EndpointsForEvent`, `fetchDueJobs`,
   `ListJobsForEndpoint`, `ListDeliveryLogs`): wrapped each `*time.Time`
   scan destination in `scanDest()` — `storage.go`'s existing `timeScanner`
   (A200), already built for exactly this class of problem and already tested
   (`storage_sqlite_test.go`, 7 cases). Reused, not duplicated.
2. **Nullable field** (`DeliveryStats`'s `MAX(dl.attempted_at) *time.Time`
   return — `scanDest`'s type assertion matches `*time.Time`, not `**time.Time`,
   so it doesn't apply directly to a nullable-pointer return): scanned into a
   `*string` first, matching `TokenStore.List`'s existing nullable-field shape
   for its `RevokedAt`. **Deviation from the plan, with concrete evidence, not
   silently decided:** the plan (and the architect's approval) called for
   mirroring `TokenStore`'s exact parse — a bare `time.Parse(time.RFC3339, ...)`.
   Implemented as specified, then verified with a new real round-trip test
   (`TestWorkerPool_DeliveryStats_realAttempt`) rather than assumed — the test
   failed. A throwaway debug test revealed why: `TokenStore.Create` explicitly
   pre-formats its own writes with `.Format(time.RFC3339)` before insert, making
   its own reader/writer contract self-consistent by construction.
   `outbound.go`'s writes are never pre-formatted — they insert a raw Go
   `time.Time` value, and the driver's own stringification produced
   `"2026-07-28 17:58:07.7222132 +0000 UTC"` (Go's default `time.Time.String()`
   shape), which a bare RFC3339 parse cannot read (no `T` separator) but which
   `timeScanner`'s existing broader layout list already handles. Final fix:
   `timeScanner{dst: &t}.Scan(*maxAtStr)`, calling the same already-tested
   unexported method directly on the scanned string — not a new, generalized
   `nullableTimeScanner` type (the architect's specific "don't build one for a
   single use site" guidance is still honored; this reuses the one helper that
   already exists rather than adding a second).
3. **Root cause of the malformed string, fixed at the source, not papered
   over on the read side:** `realClock.Now()` (`outbound.go`, the production
   `Clock` implementation) and `webhookDispatch`'s local `now := time.Now()`
   (`webhook.go`) both returned local, monotonic-clock-bearing `time.Time`
   values — every other timestamp-generation site in this codebase already
   calls `.UTC()` (`WebhookStore.Create`, `provenance.go`'s `recordProvenance`,
   `TokenStore.Create`). `realClock.Now()` now returns `time.Now().UTC()` —
   one source-of-truth fix, rather than patching every individual call site
   that reads `p.clock.Now()`.
4. **Test DDL and fixture corrections**, required, not optional (otherwise this
   exact bug class regresses invisibly again): all `DATETIME` column
   declarations in `webhook_test.go`, `outbound_test.go`, and
   `integration_full_test.go`'s G26–G29 table helpers corrected to
   `TIMESTAMPTZ`. Eight `datetime('now')` SQL-literal inserts (across
   `webhook_test.go` and `integration_full_test.go`) replaced with a real
   parameterized `time.Now().UTC()` value, matching how production code
   actually writes these columns (it never uses SQLite's `datetime()` SQL
   function). Six `newFakeClock(time.Now())` construction sites (across
   `outbound_test.go` and `integration_full_test.go`'s G27–G29) corrected to
   `.UTC()` for the same monotonic-clock reason as `realClock.Now()`.
5. **New regression test**: `TestWorkerPool_DeliveryStats_realAttempt` exercises
   the actual `Enqueue` → `fetchDueJobs` → `processJob` → `DeliveryStats` round
   trip end-to-end — the only way to have caught the RFC3339-parse gap before
   shipping, and the guard against this exact bug recurring in `DeliveryStats`
   specifically.

### Consequences

- No exported Go symbols changed. `Clock` interface signature unchanged (only
  `realClock`'s returned value changed, from local+monotonic to UTC).
- Fixes a previously-undetected, severe production bug: outbound webhook
  delivery was completely non-functional against any SQLite database following
  the documented `TIMESTAMPTZ` DDL.
- Test suite now genuinely exercises the documented production schema across
  all three affected test files, closing the exact gap that let this ship
  undetected through 96%+ coverage.
- New test: `TestWorkerPool_DeliveryStats_realAttempt`. Coverage: 96.1%.
- Patch release: v1.57.1 → v1.57.2. No API change.
- Level 2 amendment (cross-file, production-critical behaviour fix).

---
