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

## D60 — Per-member instance access is a real, separate credential; no personal-title concept anywhere in Smeldr

### Scope

Raised while scoping `cloud-workspace-instrument-zero-code` (Wave 3)'s
own authority-ladder rendering — Workspace needs to show who holds
`admin`, but cloud today holds exactly one shared bootstrap instance
token per pilot org (`pilot_orgs.instance_token_enc`, confirmed via
`scripts/seed-pilot-org`'s own documentation: "the tenant instance's
own bootstrap bearer token"). Cloud can today only answer "does someone
in this org hold admin," never "does this specific person." Discussed
directly with Peter before deciding, in two parts.

### Decision

**Part 1 — build real per-member access.** An authorized pilotorg
member may, as a deliberate, separate action (at invite time or
later), assign an individual instance-level role (`author`/`editor`/
`admin`) to another org member. Cloud mints a personal bearer token on
the target instance for that member via `create_token` + `grant_role`,
performed using the org's own already-held bootstrap token (itself
admin-capable). The new personal token is stored encrypted on that
member's own record — separate from the shared
`pilot_orgs.instance_token_enc` field, which continues to serve
whatever it already serves (org-wide/background reads not tied to a
specific viewer). Default, if nothing is assigned: the member holds no
instance access at all — org membership alone grants nothing. Later,
an authorized member may change or revoke the assignment
(`grant_role` again, or `revoke_grant`/`revoke_token`).

**Part 2 — no personal-title concept, anywhere.** Confirmed directly
against source (brand-expert's own investigation, not assumed):
Smeldr has exactly two role vocabularies, both permission-based, never
person/position-based — core's token role (`author`/`editor`/`admin`,
`auth.go`) and Cloud's own org-membership role (`owner`/`admin`/
`member`, `pilotorg/models.go`, its own code comment stating
"Deliberately a separate, narrower vocabulary" from core's). Neither
has room for a semantic job title ("CEO," "Senior," "Product
Inspector"), and this is not an oversight: `OperatorSeat`'s own
component takes a role as a raw string specifically to avoid assuming
which role vocabulary applies, and `OPEN-ITEMS.md` states directly:
"Grants point at tokens, not people... it may not invent a person from
a TokenID." No personal-title field is introduced by this decision.
Workspace's authority-ladder continues to render only role/credential,
never a person's name or job title (C36's own rule).

### Why

- Cloud already knows real per-person identity (email, at the
  `pilotorg.Member` level) — assigning that member their own personal
  instance token doesn't conflict with core's own "grants point at
  tokens, not people" principle. Core never learns or needs to learn
  who's behind a given personal token; the identity-to-token mapping
  lives entirely in Cloud's own tables, the same shape as how
  architect/core/cloud/devops/brand's own named tokens work on
  `process.smeldr.dev` today — the naming/identity association is
  external to core's own data model, not inside it.
- Reuses proven, already-shipped mechanism (`create_token`/
  `grant_role`/`revoke_grant`) — no new core capability needed, only
  new Cloud-side schema and API.
- Rejected: inventing a personal-title field. Not a neutral omission to
  fill in — it actively contradicts an established design principle in
  two separate places (`OperatorSeat`, `OPEN-ITEMS.md`).
- Rejected: continuing indefinitely with the shared-bootstrap-token
  as everyone's shared identity. Peter's own explicit instruction: not
  deferring security/authority granularity.

### Consequences

- Workspace's authority-ladder rendering can show real per-person "who
  holds `admin`" once this lands, rather than an org-wide
  approximation.
- New cloud-band work: a per-member encrypted instance-token field on
  `pilotorg.Member`, and an assign/change/revoke flow (API at minimum;
  UI surface scoped separately) using the org's existing bootstrap
  token to perform `create_token`/`grant_role`/`revoke_grant` on the
  target instance.
- This is a standing Cloud capability, not Workspace-specific — any
  future Cloud feature needing to act as a specific member, not the
  org's shared bearer, can build on the same per-member token.

---

## A294 — smeldr.dev/agent Forge→Smeldr rename (cross-repo: agent, common, core)

### Problem

`smeldr.dev/agent`'s README (and, confirmed by a full-repo grep, most of
its source tree) was a genuinely unfinished Forge→Smeldr rename — the
module itself was correctly `smeldr.dev/agent` in `go.mod`, but almost
everything else still said "forge": env var names, the connector binary's
own directory, the electricity-advisor example's scheduler binary/systemd
unit/config path, the MCP client's own protocol-visible identity string,
log message prefixes, and the README/CHANGELOG prose throughout.

### Fix

Renamed across three repos:

- **`smeldr.dev/agent`**: `FORGE_MCP_URL`/`FORGE_TOKEN` → `SMELDR_MCP_URL`/
  `SMELDR_TOKEN`; `cmd/agent-forge` → `cmd/agent-smeldr`; `example/
  electricity-advisor`'s scheduler binary `forge-agent-scheduler` →
  `smeldr-agent-scheduler`, its systemd unit file renamed to match (unit
  `Description`/`EnvironmentFile`/`ExecStart` updated), `/etc/forge-agent/`
  → `/etc/smeldr-agent/`; the MCP client's own `mcpsdk.Implementation.Name`
  sent during the `initialize` handshake, `"forge-agent"` →
  `"smeldr-agent"`; every `flow` package `slog` message prefix; README and
  `CHANGELOG.md` title/prose. `cmd/agent-github`'s own `GITHUB_REPO`
  example value updated to a real, current repo (`smeldr/core`).
- **`common/agent/skills/smeldr.md`** (canonical skill file): its own
  `## forge-agent (separate module)` section (header, binary path/env-var
  references, `forge-mcp` mentions, the `flow` sub-section and its
  `FORGE_TOKEN` example, the "forge signal"/"any forge signal string
  value" phrasing in the `AgentJob` fields table and Signal triggers
  section), plus the top `Current versions:` line's own `smeldr.dev/agent`
  number (only this one module — the other six numbers on that same line
  belong to the separate, still-open `docs-version-lines-stale-post-
  cascade` Task).
- **`smeldr.dev/core`**: `skills/smeldr.md` synced from `common` via the
  mandated `Copy-Item` step — this also caught up an unrelated, pre-
  existing sync gap (the file was missing the entire "Event stream
  (v1.73.1+)" section already present in `common`'s own canonical copy),
  a real consequence of running the sync unconditionally rather than a
  scope leak.

**Deliberately not touched, both flagged for a separate decision rather
than silently fixed or silently bundled in:**
1. The `flow` subpackage's own Go package identifier, `package
   forgeagent` (`flow/agent_job.go`, `flow/module.go`,
   `flow/agent_job_test.go`) — a materially larger breaking change than
   this rename's own scope (breaks any *unaliased* external `import
   "smeldr.dev/agent/flow"`; core's own `example/server/main.go` already
   aliases it as `agentflow` and is unaffected). The original finding's
   own "confirmed by direct grep" list never named the package
   identifier.
2. `flow/LICENSE`'s copyright holder, `Copyright (C) 2026 forge-cms` — a
   legal/copyright attribution question, not a branding one, referred to
   Peter directly rather than decided unilaterally as part of a
   branding cleanup. **Resolved same session**: Peter's own explicit
   decision, relayed via the architect — `Copyright (C) 2026 Peter Ravn
   Thers`, matching this project's established commercial-identity model
   (rights held individually, no company). Fixed in a follow-up commit on
   the same feature branch rather than a separate Task, per the
   architect's own instruction once confirmed.

Preserved deliberately (historical narrative, not a miss): `README.md`'s
"forge-social hit this in v0.4.0" sentence (a past incident, named as it
actually was at the time, matching A121's own established exception);
`docs/REFERENCE.md`'s and `docs/ARCHITECTURE.md`'s own mentions of legacy
`FORGE_*` env var fallback support, which describe `smeldr.dev/cli`'s own
real, currently-shipped backward-compat feature (T86/T87) — an unrelated
module, would have been actively wrong to "fix" here.

### Versioning

`smeldr.dev/agent`: breaking rename of deployment artifacts (env vars,
binary names, systemd unit, config path, MCP client identity) — no
exported Go symbol changed (deliberately, per the flagged package-
identifier question above), but real consumer-observable breaking
behaviour for anyone with an existing deployment. MINOR bump under pre-1.0
semantics, matching this repo's own `v0.7.1 → v0.8.0` precedent for a
comparably-scoped breaking change (A280): **v0.8.0 → v0.9.0**. Migration
note in `CHANGELOG.md` names every old→new artifact explicitly.

`smeldr.dev/core`: docs-only (`skills/smeldr.md`), no version bump.

### Tests

`go build`/`go vet`/`go test ./...`/`go test -race ./...` all green in the
agent repo (both `smeldr.dev/agent` and `smeldr.dev/agent/flow`); `go mod
tidy` no real diff (the reported `go.mod` diff is a pre-existing CRLF/LF
Windows-checkout artifact, verified via `git show HEAD:go.mod` carrying no
CRLF at all — not touched). `gofmt -l .` flags four files, all verified via
the same git-blob check to be the identical pre-existing CRLF artifact,
none actually dirty in git's own stored content, none touched.

---

## A293 — Reachability remote-exposure: GET /reachability/{type}/{id}

### Problem

`RelationStore.Reachability` had zero remote/cross-tenant exposure — every
call site was inside `reachability_test.go`, no REST, no MCP surface.
D59's own stated motivation for the `contains` relation kind (A292) —
cloud's "what already exists in domain X" query — needs this once
`contains` edges exist. `ContextPacket` cannot substitute: its depth cap
(2) and `packetPerTypeCap` (25) would silently undercount a real domain's
contents, the identical reason it can't substitute for Pulse's Tension
either. Referenced inline three times this session before the architect
created this as its own Task.

### Fix

**New route: `GET /reachability/{type}/{id}`** (`?kind=&direction=&depth=`),
registered by `App.ReachabilityHandler(rs *RelationStore)` — same shape as
`ContextPacketHandler` minus the `sourceName` param (Reachability never
builds URLs). No depth cap tighter than `MaxReachabilityDepth` (10) is
imposed at the HTTP layer — this route exists specifically to do what
`ContextPacket`'s own caps can't.

**Role: Author**, not `ContextPacket`'s `Editor` — Reachability exposes
graph structure only (type/id/edge_class/confidence, no item content),
matching `MCPGetRelations`'s own existing Author+ bar for a structurally
identical read.

**Core-side MCP wrapper included, mcp-repo tool registration NOT
included.** New `RelationStore.MCPReachability` — a bare passthrough,
matching `MCPPreviewImpact`'s own established shape — so `smeldr.dev/mcp`
can wire an actual tool in its own future Task without a core-side
prerequisite. Actually registering the tool is that repo's own separate
work, matching the established two-repo split for every prior
`MCPXxx`-then-tool addition.

`example/server` gates the route behind a new `ENABLE_REACHABILITY` flag,
requiring `ENABLE_RELATIONS` only — deliberately not `ENABLE_ORCHESTRATION`,
since `Reachability` operates on the generic relation graph, not the five
orchestration anchor types `ContextPacket` is scoped to.

**Flagged, not fixed**: `reachability.go`'s own doc comment claims to
mirror `MCPGetRelations`'s direction vocabulary, but the two functions
actually accept different literal strings (`"incoming"`/`"outgoing"`/
`"both"` vs. `"source"`/`"target"`/`"both"`) — a genuine, pre-existing
inconsistency, not introduced here, out of this Task's own scope.

### Tests

`TestMCPReachability_ThinWrapper` proves the passthrough. Five handler
tests: 200 with real graph data, 400 invalid direction, 400 invalid depth
string, 401 no token, 403 sub-Author role. Three `example/server`
`TestServerToggles` subtests (on, off-without-relations, off-without-own-
flag), matching `contextPacket`'s own established shape exactly.

### Versioning

New exported symbols (`App.ReachabilityHandler`, `RelationStore.
MCPReachability`) and a new route — MINOR bump, matching A274/A279's own
precedent for new-exported-API changes, not PATCH. v1.76.6 already
tagged — lands in a fresh `[1.77.0] — Unreleased` section. MINOR bump:
v1.76.6 → **v1.77.0**. Tag/release pending Peter's own fresh explicit
go-ahead, given directly in chat, never relayed.

---

## A295 — Public positioning accelerated to ledger-first: GitHub description, topics, README opening reordered

Peter's own decision (Turn 11, `smeldr/common/reviews/smeldr-value-
positioning-grok/discussion.md`): "Lad os accelerere det skarpere sprog.
Men endelig ikke over claim." — accelerate past the gradual `.dev`
warm-up pace deliberately, named on purpose per brand's own Turn 8
condition for proceeding, under a standing constraint: never claim more
than what's actually built and dogfooded today.

### What changed

**README.md opening** (replaces the old "AI-native content backend in Go"
one-liner and the blog-first lead): a new three-sentence paragraph leading
with the `Decision`/relation-sweep ledger capability, followed by one real
`Decision` shown as it's actually stored (this repo's own D59, quoted from
`DECISIONS.md`, not fabricated) and a corrected `ENABLE_ORCHESTRATION`
wiring snippet. The existing five "start a chat...then" bullets (blog,
social scheduling, apps, agent runtime, decision freshness cycle) are kept,
not deleted — moved after the ledger example rather than leading, per
brand's Turn 8 concern that the content use case stay visible. The
Echo/Gin/Chi comparison table is removed outright, not replaced with an
unverified competitor table — this repo cannot verify comparison claims
about Mem0/Zep/Letta or session-audit tools firsthand.

**GitHub repo description and topics** (`gh repo edit`, run after Peter's
own direct chat confirmation of the exact final text — a separate,
explicit public-content-change gate from this commit): description
reworded to lead with the ledger framing; `content-management` topic
dropped; `governance`, `audit-trail`, `self-hosted` added. Live and
verified via `gh repo view` before this commit.

### Corrected error

Grok's own Turn 8 relay suggested leading the README with "the generic
`ENABLE_GOVERNANCE` server." Verified directly against
`example/server/main.go`: `ENABLE_GOVERNANCE` wires `RoleStore` (RBAC) only.
The `Decision`/`Task`/`Amendment`/`Goal` ledger types are wired by the
separate `ENABLE_ORCHESTRATION` flag. The shipped copy uses the correct flag.

### Grounding (every claim checked against real code, not asserted)

- "enforced lifecycle" — `StateFlow`/`RequiredRole`/`Strict` transition
  gates (`orchestration.go`, `module.go`).
- "a Decision moves proposed → ratified only when the right role approves
  it" — `orchestration.go:688`, `RequiredRole: "admin", Strict: true`,
  covered by `TestDefineOrchestrationDecisionFlow`.
- "a background sweep flags connections that no longer hold" —
  `RelationStore.SweepStructural` (`relations.go:724`), exercised by
  `relations_sweep_test.go`.
- `audit-trail` topic — `ENABLE_PROVENANCE` wires transition-provenance
  recording (`App.Provenance`), verified in `example/server/main.go`.
- `governance` topic — both `ENABLE_GOVERNANCE` (RBAC) and the Decision
  ratification gate are real, shipped code.
- `multi-agent` topic deliberately **not** added — the closest real hook
  (`ENABLE_AGENTS`, one agent-job system) describes this org's own live
  dogfood usage pattern, not a literal shipped multi-agent capability in
  core's own package. Flagged for architect/brand to override if disagreed.

### Process note

Brand's own status signal (`brand-status-01a052c5`, sent to architect
after the Task moved to `implementing`) caught a real house-style
violation in the first draft: em dashes in public-facing copy, the same
non-negotiable rule fixed across 11 live posts on 2026-08-28. Fixed before
commit — the repo description and README opening paragraph now use colons
instead of em dashes. Brand raised no objection to keeping D59 as the
example, and no other substantive changes. Brand also flagged, as a
process note rather than a blocker, that Turn 11's own promise ("brand
reviews the actual draft copy before it ships") wasn't triggered by a
signal before this Task moved to `implementing` — noted for next time, not
re-blocking this Task since brand's actual review (via the plan file) has
now happened and raised only the em-dash issue.

### Consequences

No exported Go symbol changed, no route or middleware behavior changed, no
`Example` function in `example_test.go` broken (reorder and new
illustrative snippets only, none compiled). No version bump — docs and
public-metadata positioning only. Level 2 amendment (README structural
reorder, cross-references DECISIONS.md/recent.md, external repo metadata).

---
