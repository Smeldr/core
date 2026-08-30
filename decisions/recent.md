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
Archived 2026-08-30: A272-A287 → phase32-archive.md
---

## A288 — nullable *time.Time scan bug: nullTimeScanner (T210)

### Problem

Confirmed real and reproducible: a `Node.ScheduledAt` round-trip
(`Save` + `FindBySlug`) through a real `SQLRepo`-backed repo failed
with `sql: Scan error on column index N, name "scheduled_at":
unsupported Scan, storing driver.Value type string into type
*time.Time`.

Root cause, verified against `storage.go`: `scanDest` (A200) special-
cases a scan destination whose address is `*time.Time` — exactly what
taking the address of a plain `time.Time` struct field gives you
(`Node.PublishedAt`, `webhook.go`'s `CreatedAt`, etc.). `Node.ScheduledAt`
is declared `*time.Time` (nullable — nil for every non-scheduled state)
— its own field address is `**time.Time`, unmatched by `scanDest`'s
existing case, falling through unwrapped to `database/sql`'s generic
pointer-to-pointer path, which cannot parse SQLite's string-formatted
timestamps.

**Scope corrected during grounding, not assumed**: `TIMESTAMPTZ` is not
an orchestration-specific mistake — it's used project-wide (`webhook.go`,
`outbound.go`, `audit.go`, `provenance.go`, `sweep_run.go`), all already
correctly handled by A200 since every one of those is a non-nullable
`time.Time` field. `Node.ScheduledAt` is the one genuinely distinct
gap, and it isn't orchestration-specific either — `Node` is embedded by
every compiled `Module[T]` type, so any content type using `SQLRepo`
and setting a non-nil `ScheduledAt` hits this.

**Checked, confirmed unaffected**: `relations.go`'s `RelationEdge.ValidAt`/
`InvalidAt` are also nullable `*time.Time` fields, but `scanEdge` already
scans them through its own hand-written `sql.NullTime` locals, never
through `scanDest`'s generic reflection path.

**A genuinely striking find during review**: `Run.AcknowledgedAt`
(`orchestration.go`) is declared `time.Time`, not `*time.Time` — its own
doc comment states this was a deliberate workaround for this *exact*
bug, discovered and empirically reproduced independently during an
earlier task, then explicitly flagged "to the architect as its own
follow-up — not fixed here." That flag sat unactioned until this task's
own independent rediscovery. Confirms this is a real, previously-known
gap, not a novel theory.

### Fix

`nullTimeScanner{dst **time.Time}` added to `storage.go`, reusing
`timeScanner`'s own parsing logic rather than duplicating it — `nil`
sets the destination to a nil pointer; any other value is parsed via a
scratch `timeScanner` and the result's address stored. `scanDest`
gains a second type-assertion branch for `**time.Time`. Fixed at the
single shared entry point every `SQLRepo[T]`'s generic scan path
already calls (`storage.go`, two call sites) — not a per-type
workaround, matching A200's own fix shape for the non-nullable case.

**Not touched, flagged forward**: `Run.AcknowledgedAt`'s own type
choice is now a stale workaround for a bug that no longer exists —
changing it is a real, separate API-shape decision (nullable-time vs. a
bool+timestamp pair) worth its own future cleanup Task, not bundled
into this one.

### Tests

9 new: `TestNullTimeScanner`'s 7 subtests directly mirror
`TestTimeScanner`'s own existing shape (`time.Time` src, RFC3339Nano
string, `[]byte`, `int64` unix, nil, unparseable string error,
unsupported type error) — the nil case differs meaningfully from
`timeScanner`'s own (sets a nil pointer, not a zero `time.Time`).
`TestSQLRepo_ScheduledAt_nonNil_roundTrips` and `_nil_roundTrips`
exercise the actual regression end-to-end through a real `SQLRepo`
(`revNode`/`rev_nodes`, an existing fixture already carrying a
`scheduled_at` column — no new fixture needed) — the non-nil test is
the literal reported failure, the nil test pins the already-working
case against regression.

### Versioning

No exported symbol changed (`scanDest`/`timeScanner`/`nullTimeScanner`
all unexported). Coverage: 96.3% package-wide, unchanged. `go build`/
`go vet`/`go test -race -count=1 ./...` clean; `golangci-lint` clean.
PATCH bump — a real, consumer-observable bug fix (any caller using
`SQLRepo` with a non-nil `Node.ScheduledAt` previously got a hard Scan
error; now works), no new exported symbol, matching A226/A281/A283/
A286/A287's own precedent: v1.76.2 → **v1.76.3**.

---

## A289 — SweepRunRecord actor concept (Wave 4b, cloud-machine-implementation-plan-v1.md)

### Problem

`SweepRunRecord` (`sweep_run.go`) had no actor concept — its fields
were `ID`/`Detector`/`RanAt`/`Interval`/`Walked`/`Flagged`/`Skipped`/
`Err`, nothing naming who or what triggered the run. Every other
background-job provenance record in core already carries `ActorKind`/
`ActorID` (`ProvenanceRecord`; `App.DrainEvalQueue`'s own write,
`state.go:1104-1114`) — `SweepRunRecord` predates that convention and
was never brought in line with it. `state.go`'s own comment on the
`DrainEvalQueue` write explicitly forecast this: "generalises to
SweepStructural (T223) as the same pattern."

**Sequencing constraint, real and non-reversible**: this ships before
devops turns on `ENABLE_STRUCTURAL_SWEEP` on the live
`process.smeldr.dev` deployment — a `SweepRunRecord`'s own fields
can't be backfilled after the fact once real sweep runs start
accumulating without actor data.

### Fix

`SweepRunRecord` gains `ActorKind string` / `ActorID string`, matching
`ProvenanceRecord`'s existing vocabulary (`"human"`/`"job"`/`"agent"`;
empty only if truly unattributable) rather than a new vocabulary for
sweep runs specifically. `example/server`'s `sweepFn` closure
populates `ActorKind: "job"`, `ActorID: "sweep-structural"` — a fixed,
non-enumerable mechanism identifier, named after `App.SweepStructural`
kebab-cased, exactly mirroring `drain-eval-queue`'s own derivation from
`App.DrainEvalQueue`.

`CreateSweepRunTable`'s DDL gains both columns
(`TEXT NOT NULL DEFAULT ''`); a pre-existing `smeldr_sweep_runs` table
from a deployment that already called `CreateSweepRunTable` (shipped
since `sweep-structural-wired-on-schedule`, 2026-08-20) is upgraded via
two `EnsureColumn` calls (T246 pattern, same shape as
`CreateSiteConfigTable`'s own `scheduled_at`/`rev` migration calls) —
a pre-migration row correctly reads back with both fields empty
("unattributable"), not a fabricated actor. `Append`/`Last`/`List`/
`scanSweepRunRecord` all updated to read/write both columns.

### Tests

`TestSweepRunStore_AppendAndLast` and `TestSweepRunStore_ListOrderAndLimit`
extended to set and assert `ActorKind`/`ActorID`. New
`TestCreateSweepRunTable_MigratesActorColumns` creates a pre-A289-shape
table by hand, inserts a row before migrating, calls
`CreateSweepRunTable` again, and asserts: the pre-migration row reads
back with both fields empty, and a post-migration `Append` correctly
writes and reads back real values.

### Versioning

New exported struct fields on an existing type (`SweepRunRecord`), no
new top-level exported symbol, no breaking change — matches A275's own
precedent for consumer-visible-but-additive changes to an existing
exported type. PATCH bump: v1.76.3 → **v1.76.4**. Tag/release pending
Peter's own fresh explicit go-ahead, separate from commit approval.

---

## A290 — ContextPacket actor concept: CreatedAt/UpdatedAt on PacketAnchor/PacketItem

### Problem

Requested by cloud-implementer, blocking `cloud-multi-tenant-instrument-
reads` (Wave 0 of `design/cloud-machine-implementation-plan-v1.md`).
`PacketAnchor` and `PacketItem` (`context_packet.go`) carried no
timestamps — fields were `Type`/`ID`/`Slug`/`Status`/`Rev`/`URL`/
`Fields`. `internal/read` (smeldr/cloud) needs both for `AgeDays`/
elapsed-time framing once its own fetch layer is rewired to read from
`ContextPacket` instead of local SQL. cloud-implementer already ruled
out the alternative (reading a compiled type's own typed
`GET /{prefix}/{slug}`) because that route's auth gate varies per-
tenant depending on whether governance/RoleStore is wired, unlike
`ContextPacketHandler`'s uniform `HasRole(Editor)` check.

### Fix

`PacketAnchor`/`PacketItem` gain `CreatedAt time.Time` / `UpdatedAt
time.Time` (`json:"created_at"`/`json:"updated_at"`), positioned
between `Rev` and `URL` — keeping the existing identity → lifecycle →
payload grouping intact. Both are a pure read-through from the
underlying content's own `Node.CreatedAt`/`Node.UpdatedAt`
(`node.go:72-76`), already present on every orchestration type — no
new data, no new storage. `BuildContextPacket`'s two struct literals
(the anchor build and the linked-item build inside the traversal loop)
both populate the fields from `anchorNode`/`nd` respectively, both
already in scope at each call site.

### Tests

`TestBuildContextPacket_goalAnchor` extended to assert the anchor's
`CreatedAt`/`UpdatedAt` are non-zero. `TestBuildContextPacket_
decisionAnchor` (already exercising one linked item) extended to
assert non-zero timestamps on both the anchor and `Items[0]`,
confirming both structs populate correctly, not just the anchor.

### Versioning

New exported struct fields on two existing types, no new top-level
exported symbol, no breaking change — same shape as A289, additive to
the same already-open `[1.76.4] — Unreleased` batch, not a separate
version number. Tag/release for the combined A289+A290 batch pending
Peter's own fresh explicit go-ahead, separate from commit approval.

---

## A291 — ContextPacket relation metadata: CreatedAt/Label/ReverseLabel on PacketRelation

### Problem

Requested by cloud-implementer, third additive gap in the same series
as A289/A290 — found while mapping every field `internal/read/
thread.go` reads off a `RelationEdge` against `PacketRelation`'s
current shape (`SourceType`/`SourceID`/`TargetType`/`TargetID`/`Kind`
only). Confirmed directly against source:

- `thread.go:196` reads `edge.CreatedAt.Unix()` to date relation-driven
  thread rows — `PacketRelation` carried no `CreatedAt`.
- `thread.go:174-175` reads `rs.GetKind(edge.RelationKind).Label` to
  resolve the display word — `PacketRelation.Kind` alone is only the
  raw `relation_kind` type_name, not the resolved label.

Once this lands and releases, cloud-implementer's `ContextPacket` call
becomes fully sufficient for Trace's rewrite — anchor, items, and
relations resolved in one call, no further core-side gaps expected for
Trace specifically.

### Fix

`PacketRelation` gains `CreatedAt time.Time`, `Label string`,
`ReverseLabel string` — positioned after `Kind`, keeping the existing
"edge identity → resolved meaning" grouping. `CreatedAt` is a direct
read-through from `RelationEdge.CreatedAt`, already in scope as the
loop variable at the one construction site
(`context_packet.go:363-379`). `Label`/`ReverseLabel` resolve via
`rs.GetKind(edge.RelationKind)` — `rs *RelationStore` is already a
`BuildContextPacket` parameter. Fail-open on an unregistered kind:
both stay `""`, matching `thread.go`'s own `eventFromEdge` precedent
for the identical situation — no error, no item dropped.

`EdgeClass` confirmed NOT needed — cloud-implementer verified it is
only read off `Reachability`'s own ring items in Pulse's `computeTension`
path, out of scope per the architect's prior decision on this same
Task's "second gap" (`Reachability` has no remote path, split into its
own follow-up Task).

### Tests

`TestBuildContextPacket_decisionAnchor` extended: asserts
`Relations[0].CreatedAt` is non-zero, and that `Label`/`ReverseLabel`
are both empty — this test's own `insertTestEdge` registers a kind
with no `Label`, so it correctly exercises the fail-open path. New
`TestBuildContextPacket_relationLabelResolution` registers a kind with
both `Label`/`ReverseLabel` set and asserts the packet's
`PacketRelation` carries the resolved words exactly — proves the
non-fail-open path is also correct, not just the default.

### Versioning

New exported struct fields on an existing type, no new top-level
exported symbol, no breaking change — same shape as A289/A290. v1.76.4
is already tagged and released (Peter's own fresh go-ahead, earlier
this session) — this change lands in a fresh `[1.76.5] — Unreleased`
section. PATCH bump: v1.76.4 → **v1.76.5**. Tag/release pending
Peter's own fresh explicit go-ahead, separate from commit approval.

---

## D59 — Goals are places: a new `contains` relation kind gives Smeldr a first-class, user-defined taxonomy

### Scope

Raised by cloud-implementer's plan for `t204-navigator-instrument`
(Wave 2 of `design/cloud-machine-implementation-plan-v1.md`). Navigator's
own closed design (Turn 56/79/80) draws a containment hierarchy — domain
to area to entries, e.g. "Halden Works" (1,284) containing "Pricing"
(412) containing "Decisions" (54) — that does not correspond to
anything in Smeldr's real data model. `orchestration.go` defines five
flat content types (`Decision`/`Task`/`Goal`/`Amendment`/`Signal`), each
with at most a flat category string (`Decision.Scope`, `Task.Band`), no
nesting, no domain/area concept anywhere in core. Discussed directly
with Peter before deciding — not resolved unilaterally.

### Decision

A Goal is a place. Any content item can relate to a Goal via a new
RelationStore kind — working name `contains` (reverse label `part of`
or similar; exact label left to the implementing Task) — and the tree
Navigator walks is that relation, not a schema-level hierarchy. The
taxonomy itself is user/org-defined: a Goal is already a freely created
item with no fixed enum, so "Frontend," "Finance," or "Pricing" are
just Goals someone created, not values baked into core's schema. This
generalizes past Navigator: it is the answer to a real coordination
need — with many agents and humans taking decisions across domains,
being able to ask "what already exists in this area" before adding
something new is a first-class discovery primitive, not a UI-only
concern.

### Why

Three readings were on the table; two were rejected:

- **Type as the only hierarchy level** (the five content types as flat
  top-level "domains"). Cheapest, but doesn't match the design's own
  worked examples (3+ levels) and makes realistic entry counts
  implausible at this repo's actual scale.
- **Reuse the existing `Scope`/`Band` fields directly.** Real data, zero
  schema change — but each type's category vocabulary is its own,
  unrelated to the others. A cross-type tree built from five
  disconnected ad hoc vocabularies would be an artifact of the UI, not
  a hierarchy the model actually asserts.

A shared flat `Topic` field (one new field, present on all five types)
was also considered and rejected: cheaper than a relation kind, but
caps grouping at exactly one level, gives the domain itself no
addressable identity of its own, and free-text values risk vocabulary
drift fracturing the taxonomy ("frontend" vs "Frontend" vs
"front-end") with no correction mechanism.

The `contains`-relation reading wins because:

1. It produces a real containment structure of arbitrary depth, not a
   fixed-depth flattening or a single-level tag.
2. Goals already function informally this way in this repo's own live
   usage — Tasks and Decisions already reference the Goal they serve.
3. The query this decision exists to serve — "what already exists in
   domain X" — is not new engineering. `RelationStore`'s reachability
   walk already exists and is proven in production today (Pulse's
   `computeTension`); this decision reuses that primitive with a new
   kind, it does not invent new query machinery.
4. An initially sparse tree that fills in as `contains` edges get
   asserted is consistent with the rest of the system's own posture on
   absence — stated honestly, never fabricated to look complete (the
   same posture behind Pulse's "not computed" states and Navigator's
   own "surrendered count" handling).

### Consequences

- Navigator's tree-rendering work is now split: everything independent
  of the place question (host contract extension, rack reorder,
  fold/floor UI, hand-off wiring, empty-org and frozen-read handling)
  proceeds now; real tree rendering against actual `contains` edges
  waits on the relation kind existing.
- A follow-up Task registers the `contains` kind (RelationKindDef,
  `Label`/`ReverseLabel`) and settles what this decision deliberately
  leaves open: **who asserts a `contains` edge, and when** — at
  creation time, via a later curation pass, or both. Not decided here.
- This is now a standing Smeldr capability, not a Navigator
  implementation detail — other instruments or a future search surface
  can query the same relation kind once it exists.

---

## A292 — Register `contains` relation kind, settle D59's two open questions (D59 follow-up)

### Problem

D59 settled that a Goal is a place, but deliberately left the `contains`
kind unregistered and two real questions open: who asserts a `contains`
edge and when, and whether `RelationStore.Reachability`/`ContextPacket`
are sufficient for cloud's own "what already exists in domain X" query.
cloud-implementer is blocked on this for the second half of
`t204-navigator-instrument` (real tree rendering).

### Fix

`RegisterOrchestrationRelationKinds` gains a fifth kind: `contains`
(`Label: "Contains"`, `ReverseLabel: "Part Of"`, `Directional: true`,
`Mode: "asserted"`), with five `TypePairs` entries —
Goal→{Goal,Task,Decision,Amendment,Signal} — matching D59's own "any
content item can relate to a Goal" language, including Goal→Goal for
nested domains ("Halden Works" containing "Pricing").

### Who asserts, and when

**Convention now, no new core machinery.** Verified directly: `Task`
carries no field referencing a Goal at all — its relation to a Goal
today is asserted purely via `derives_from`, through a separate
`assert_relation` call, never bundled into creation. No existing
relation kind, including `derives_from`/`ships_as`, is ever
auto-asserted by a `createHandler` as a side effect of creating an
item — "architect asserts `derives_from` at dispatch" is a *process*
convention (`AGENT_PROTOCOL.md`), not a core-level mechanism. `contains`
follows the identical model: the creating actor asserts the edge via
the existing `assert_relation` tool, same discipline, no new code.
Building creation-time auto-assertion here would be new, unrequested
machinery that also preempts T233's own holistic review of assert-time
enforcement across every relation kind.

Deferred, explicitly out of scope: a coverage-detection follow-up
(flagging items with zero `contains` edges) is a different problem
from `SweepStructural`'s existing staleness detection (D47) — it would
need its own new mechanism, naturally tied to T126's still-unbuilt
`Finding` type (D51). Not building it now.

### Reachability / ContextPacket sufficiency

**Confirmed insufficient — the same gap already known for Pulse, not a
new one.** `RelationStore.Reachability` has zero remote/cross-tenant
exposure (every call site is in `reachability_test.go`, no REST, no
MCP). `ContextPacket`'s depth cap (2) and `packetPerTypeCap` (25) would
silently undercount a real domain's contents for the same reason they
can't substitute for Pulse's Tension. This gap had been referenced
inline three times across this session (Pulse's `computeTension`,
A291's own commit note, and this Task) without ever becoming its own
Task — flagged to the architect rather than creating a fourth inline
reference or a duplicate Task for what is the identical root cause. The
architect has since created it as its own backlog item.

### Tests

`TestRegisterOrchestrationRelationKinds_RoundTrip` extended with a
`"contains"` entry (Label, ReverseLabel, exact five-pair `TypePairs`
JSON) — same assertions every other kind already gets, kind count now 5.

### Versioning

New relation-kind data registered by an existing function; no exported
Go symbol/signature changed, no breaking change — same shape as
A289/A290/A291's own precedent. v1.76.5 is already tagged and
released — this lands in a fresh `[1.76.6] — Unreleased` section. PATCH
bump: v1.76.5 → **v1.76.6**. Tag/release pending Peter's own fresh
explicit go-ahead, given directly in chat, never relayed.

---
