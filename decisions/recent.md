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

## A274 — GET /_events/stream: NDJSON push transport for agent orchestration events (T269)

### Problem

Every implementing agent session tonight runs on Peter's own machine,
behind NAT with no public IP. Smeldr's own outbound webhook (T231/A263)
is an HTTP POST *from* the server *to* a URL Smeldr provides — nothing
can POST into a machine with no public address to POST into. The
self-hosting design's own "N listeners" webhook-to-local-listener model
(`design/self-hosting-the-architect-process.md` §7) only reaches a
listener co-located on the same public box as `process.smeldr.dev`
itself — never a listener on a home machine. Full rationale and the
rejected alternatives (Claude Code Routines, a hosted webhook relay,
WebSocket) are in `design/agent-event-signaling.md`, an addendum to §7
approved 2026-08-17; this Amendment implements it, not re-argues it.

### Fix

New `App.EventStream()` (opt-in, matches `App.CaptureLogs`/`App.Webhooks`)
installs an unexported in-memory `eventBroadcaster` (`eventstream.go`) — a
set of subscriber channels, non-blocking `broadcast` (a full subscriber
buffer is dropped, logged at `Warn`, never blocks the caller or its
siblings) — and mounts `GET /_events/stream`: Author role, bearer auth
(same `AuthFunc` contract as `GET /_logs`/`GET /_audit`), each connection
held open and receiving one NDJSON line per event (`http.Flusher`,
`"\n"`-terminated, flushed immediately), a `{"type":"ping"}` heartbeat
every 25s (`eventStreamHeartbeat`, an injectable `var` for tests, not a
`const`) to survive idle-timing reverse proxies. Delivery is at-most-once
by design — no replay/backfill in this first cut (the design doc's own
lean, confirmed not re-argued); every subscriber receives every event,
filtered client-side only (also the design doc's own lean).

Two broadcast call sites, reusing the exact payload-building code already
used for webhook delivery rather than a second implementation:

1. **`App.dispatchBus`** (content lifecycle — `AfterCreate`…`AfterSchedule`)
   gains a broadcast check using `buildWebhookPayload`, inserted *before*
   the existing `OnSignal`-handler dispatch and its own early-return —
   deliberately unconditional on whether any `OnSignal` handler (including
   `App.Webhooks`) is registered.
2. **`dispatchTransitionWebhook`** (state-flow transitions, T231's
   `"signal.created"`) gains a `broadcaster *eventBroadcaster` parameter;
   its nil-guard changed from "return unless store+pool both set" to
   "return unless store+pool **or** broadcaster is set" — payload building
   happens once, then each configured sink (webhook enqueue, stream
   broadcast) fires independently. `recordAuthorizationRequiredSignal`
   (`state.go`) threads the same new parameter through to its own call.

### Decoupling decision — the one judgment call this Amendment adds

The design doc's own Shape §1 names both broadcast call sites but doesn't
settle whether the stream should work when `App.Webhooks()` was never
called. Decided: **yes, independently** — the stream is in-memory only,
with no relationship to whether an external webhook target exists; making
it silently require `App.Webhooks()` would defeat much of its own point
(an agent that wants live events without standing up webhook
infrastructure). Two tests exist specifically to *prove* this rather than
only cover current behaviour:
`TestDispatchBus_BroadcastsToEventStreamOnAfterCreate` and
`TestDispatchTransitionWebhook_nilStoreNilPoolBroadcasterSet`, both with
`EventStream()` configured and `Webhooks()` never called.

**Role: Author.** Not pre-decided in the design doc. Chosen by analogy —
`list_signals`, `get_valid_transitions`, `list_items_by_state` (the MCP
tools reading the identical event classes this stream carries) are all
Author-role; gating the push path higher than its own pull-based
equivalent would make it harder to use than the polling it exists to
replace. Kept deliberately separate from webhook *configuration*'s Admin
bar — "read a live feed of events that already happened" and "point
delivery at an arbitrary external URL" are different questions.

### Real, unplanned finding — `App.wireSignalBus` was itself gating the stream shut

`App.wireSignalBus`'s own early-return (`if !hasBus &&
len(a.signalListeners) == 0 { return }`) meant the afterHook closure that
calls `dispatchBus` was never wired at all when zero `OnSignal` handlers
existed — `EventStream()` alone, with no `OnSignal`/`Webhooks()` call,
would have wired the broadcaster but never actually fired it, silently
contradicting the decoupling decision above. Caught by the plan's own
test for that decision failing on first run, not found by inspection.
Fixed by adding `a.eventBroadcaster == nil` as a third condition to the
same early-return — the closure now wires whenever any of the three
sinks (bus handlers, legacy listeners, event stream) is configured.

### Tests

19 new tests (`eventstream_test.go`, plus additions to `signals_test.go`,
`smeldr_test.go`, `webhook_test.go`, `state_test.go`) — a named
`-race`-covered concurrent-subscriber test
(`TestEventBroadcaster_ConcurrentSubscribeBroadcastUnsubscribe`), a
non-blocking-drop test under a deliberately overflowed buffer, separate
write-error tests for the event-payload and heartbeat write call sites
(two distinct `select` cases), route-absent-without-`EventStream()` and
double-`Handler()`-registration-is-idempotent pins, and the two
decoupling-proof tests named above. Five existing call sites (
`dispatchTransitionWebhook`/`recordAuthorizationRequiredSignal`
signature changes) updated as mechanical regression pins, not new
coverage. `newEventStreamHandler`'s one uncovered branch (a subscriber
channel closed while still subscribed, distinct from the handler's own
deferred `unsubscribe`) is genuinely unreachable via the public API —
named, not chased, matching this session's own precedent for
structurally-unreachable branches (A219/A273).

### Versioning

New exported symbol (`App.EventStream`) + new consumer-visible route.
Coverage: 96.3% package-wide; every new function in `eventstream.go`
100% except `newEventStreamHandler` (97.0%, the one named-unreachable
branch above). `go test -race ./...` clean. `golangci-lint` zero
findings. MINOR bump, matching A265/A271's own precedent for "new
capability, no breaking change". v1.72.2 → **v1.73.0**.

---

## A275 — GET /_events/stream: example/server wiring and a per-token connection cap (T271)

### Problem

Two gaps found reviewing A274/T269 the same day, both closed in this
Amendment. First: `App.EventStream()` existed in the library, but
nothing in `example/server/main.go` ever called it — the endpoint was
unreachable from the one binary this project actually deploys. Second:
neither `eventBroadcaster` nor `newEventStreamHandler` capped the number
of concurrent subscriber connections a single token could hold open. A
valid Author-role token stuck in a reconnect loop (a listener bug, not
malice) or a compromised token could hold unbounded concurrent
long-lived connections, each with its own goroutine and 32-slot buffer.

### Fix — example/server wiring

New `EnableEventStream bool` / `ENABLE_EVENT_STREAM` env var, matching
`EnableWebhooks`'s exact existing pattern (`main.go`). Deliberately
**not** tied to `EnableOrchestration` — verified directly against
`dispatchBus`'s source before writing the plan: its broadcast fires for
every registered `Module[T]`'s content-lifecycle events, not only the
five orchestration types, so coupling gating to orchestration would
wrongly block a CMS-only deployment from ever using the stream for its
own content events.

### Fix — per-token connection cap

Mechanism argued, not guessed. Rejected `RateLimit`/`NewRateLimiter`
(`middleware.go`) — that bounds *request rate* over a time window, the
right tool for bursty short requests; this is a *concurrency* question
(one connection held open for its entire lifetime), a different
mechanism for a different quantity. Rejected a pure global cap — it
can't distinguish "several well-behaved listeners, one connection each"
(the design's own steady-state, `design/agent-event-signaling.md`'s "N
listeners, not one") from "one runaway token holding dozens"; set low
enough to matter, it blocks legitimate multi-listener growth, set high
enough not to, it stops bounding the actual risk.

New `eventStreamMaxSubscribersPerToken = 4` (not 1): the steady-state is
exactly one connection per token/listener, so capping at the literal
steady-state would reject the ordinary case of a listener reconnecting
before its old connection finishes tearing down — a false-positive on
normal operation. 4 gives headroom for that overlap while still
rejecting a genuine runaway within a handful of iterations.

Identity key: `User.ID`, verified directly against `auth.go`'s
`decodeToken` — the `id` claim baked into the token at signing time,
already the exact identity `ActorID`/provenance recording uses elsewhere
in this package, not a second notion of "who is this."
`eventBroadcaster` gains a `byToken map[string]int` alongside the
existing `subs` map; `subscribe` changes from `subscribe() chan []byte`
to `subscribe(tokenID string) (chan []byte, error)`, returning the
already-existing `ErrTooManyRequests` sentinel (429) once a token is at
its cap. The check happens in `newEventStreamHandler` *before* the `200`
header is written — headers cannot be un-written once sent, so a
rejection has to happen before the status line commits.

### Tests

16 new tests: `TestEventBroadcaster_SubscribePerTokenCapRejectsPastLimit`,
`_SubscribeDifferentTokensIndependentCaps`, `_UnsubscribeFreesTokenSlot`,
`_UnsubscribeCleansByTokenEntry` at the broadcaster-unit level;
`TestEventStreamHandler_TooManyConnectionsPerToken429`,
`_DifferentTokensNotCapped`, `_CapReleasedOnDisconnect` proving the same
through the real HTTP handler end-to-end, not just the broadcaster in
isolation; two `example/server` toggle tests
(`off/noEventStream`/`on/eventStream`) extending the existing
`TestServerToggles` table. Five existing call sites
(`eventBroadcaster.subscribe`'s changed signature, in
`signals_test.go`/`state_test.go`/`webhook_test.go`) updated as
mechanical regression pins, not new coverage.

### Versioning

No new exported symbol (`subscribe`'s changed signature is unexported),
but a real consumer-observable behaviour change on `GET /_events/stream`
(a 429 case that didn't exist before), which CLAUDE.md's own versioning
rule treats as tag-worthy regardless of exported-symbol status — matching
A266/A269/A270/A272's own precedent for this exact class. v1.73.0 itself
has not been tagged/released yet; this still gets its own version number
rather than folding backward into it, matching the established
precedent of one Amendment, one version, even within the same unreleased
session. Coverage: 96.3% package-wide; `subscribe`/`unsubscribe`/
`tokenCount`/`newEventBroadcaster` all 100%, `newEventStreamHandler`
97.2% (the one pre-existing, genuinely-unreachable branch named in A274,
unchanged). `go test -race ./...` clean. `golangci-lint` zero findings.
PATCH bump: v1.73.0 → **v1.73.1**.

---

## A276 — GET /_events/stream: write-timeout was killing every connection

### Problem

`GET /_events/stream` (A274) never actually delivered events in
production. Devops confirmed this live via three independent tests,
including one made from the box's own SSH session straight to loopback
`127.0.0.1:8081`, bypassing Caddy and the network entirely — this rules
out proxy, TLS-inspecting antivirus, and network causes, isolating the
failure to the application's own write-timeout handling.

Root cause, confirmed directly in source: `smeldr.go`'s
`defaultWriteTimeout = 10 * time.Second` is wired into the shared
`http.Server.WriteTimeout`. Per Go's own documentation, `WriteTimeout` is
"the maximum duration before timing out writes of the response. It is
reset whenever a new request's header is read" — for one held-open
streaming response there is no second request to reset it, and an
intermediate `Flush()` does not push it back either. The stream's initial
`200` header and first flush happen immediately on connect, well inside
the 10s window; the connection then sits idle until the *first* heartbeat
fires at `eventStreamHeartbeat` (25s). By then the fixed 10s deadline has
already passed — that first heartbeat `Write` is the one that hits the
expired deadline and the stdlib force-closes the connection. No event
payload could ever arrive before that either, for the identical reason.

### Fix

`newEventStreamHandler` now constructs `rc := http.NewResponseController(w)`
(stdlib, Go 1.20+) once, and calls `rc.SetWriteDeadline(time.Now().Add(2 *
eventStreamHeartbeat))` immediately before every write — both the
event-delivery branch and the heartbeat branch — refreshing the deadline
to roughly 50s in the future at the default 25s heartbeat interval. Go's
`net.Conn` deadlines are checked lazily, only at the moment an I/O call is
attempted, not enforced by a background timer — so the deadline in effect
at write-time is always the freshly-set one, including for the very first
heartbeat that used to trigger the bug.

### Alternatives rejected

- **A second `http.Server`/listener with `WriteTimeout: 0`, dedicated to
  this route.** Every other opt-in feature in this codebase
  (`CaptureLogs`, `Webhooks`, `Audit`, `PageMeta`, A275's own connection
  cap) mounts its route on the app's single existing `*http.ServeMux`,
  sharing one `http.Server`/one graceful-shutdown path. A second listener
  for one route breaks that pattern, doubles the surface `App.Run`'s
  shutdown logic has to reason about, and removes write-timeout
  protection from the *entire* route rather than scoping the removal to
  the one thing that actually needs it.
- **Disable the deadline entirely** (`SetWriteDeadline(time.Time{})` once,
  at connection start) — simpler, but removes all protection against a
  genuinely wedged write (a TCP connection alive at the OS level, peer not
  reading, no FIN ever received) for the connection's whole lifetime.
  `r.Context().Done()` already catches a *clean* client disconnect
  independently of any write; a half-dead peer that never sends FIN/RST is
  a different, real failure mode a fully-unbounded deadline leaves
  unguarded — the same class of "unbounded resource held by one bad
  actor" A275 already argued for and fixed on the connection-*count* axis.
  Leaving connection *duration* unbounded in the same Amendment family
  would have been inconsistent.

`SetWriteDeadline`'s own error is deliberately non-fatal: a
`ResponseWriter` that doesn't support write deadlines (returns
`http.ErrNotSupported` — true only for a custom/test `ResponseWriter`,
never a real HTTP/1.1+ connection) is logged at `Warn` and the write is
still attempted rather than aborting the connection over it.

### Tests

`TestEventStreamHandler_SurvivesWriteTimeout` uses
`httptest.NewUnstartedServer` rather than `httptest.NewServer` specifically
so `Config.WriteTimeout` can be set to a real, short value (100ms) before
`.Start()` — reproducing the exact stdlib mechanism, not a proxy for it.
Verified to fail against the pre-fix code (closes the connection after 3
of 5 expected lines, `unexpected EOF`) before confirming it passes with
the fix — a genuine regression test, not just new coverage.
`TestEventStreamHandler_SetWriteDeadlineUnsupported_StillWrites` covers
the graceful-degradation branch with a new `deadlineUnsupportedWriter`
test double (implements `http.Flusher` but not the unexported shape
`ResponseController.SetWriteDeadline` looks for).

### Versioning

No new exported symbol; a real consumer-observable behaviour change (the
feature now actually delivers events past 10s, which it structurally
could not before). Coverage: 96.3% package-wide; `newEventStreamHandler`
97.6% (up from 97.2% — the new graceful-degradation branch is now
covered). `go test -race ./...` clean. `golangci-lint` zero findings.
PATCH bump: v1.73.1 → **v1.73.2**.

---

## A277 — App.NotifySignalCreated: exported hook for raw-SQL Signal creation paths

### Problem

`smeldr.dev/mcp`'s `create_signal` tool performs a direct `db.ExecContext`
SQL INSERT into the `smeldr_signals` table instead of using
`Module[Signal].MCPCreate`. This bypasses core's entire typed lifecycle
machinery — `notifyAfter` hooks, `wireSignalBus`, `dispatchBus` — with the
result that creating a Signal via `create_signal` produces no webhook
delivery and no `GET /_events/stream` broadcast, even when those systems
are configured and working. This is the identical shape of gap
`recordAuthorizationRequiredSignal` (an internal core function in
`state.go`) already had: a raw-SQL item insert with no typed Go item in
hand, so it cannot reach the normal `buildWebhookPayload` codepath. The
fix applied there was to call `dispatchTransitionWebhook` directly after
the INSERT succeeds (A263). However, `mcp` is a separate Go module that
sees only core's exported API surface — it has no access to that
unexported function. A277 solves this by exporting a thin wrapper.

### Fix

New exported method `App.NotifySignalCreated(ctx context.Context, id,
slug string)` in `webhook.go`, immediately after `dispatchTransitionWebhook`:

```go
dispatchTransitionWebhook(ctx, a.webhookStore, a.webhookPool, a.eventBroadcaster, "signal.created", transitionWebhookData{
	Type: "signal", ID: id, Slug: slug, ToState: "pending",
})
```

Mirrors `recordAuthorizationRequiredSignal`'s own internal call shape
exactly. Nil-safe — a no-op if neither `App.Webhooks()` nor
`App.EventStream()` has been configured, inherited directly from
`dispatchTransitionWebhook`'s own existing nil-guard.

### Deliberately narrow scope — one caller, not a generic escape hatch

The method exists for exactly one known real caller: `mcp`'s
`create_signal`. It is not a generic "fire an arbitrary event from
outside core" mechanism. If a second bespoke raw-SQL creation path (e.g.
a CLI direct-insert command, or another tool in another module) needs the
same treatment later, that is a real second data point worth a new
Amendment to generalize from — not speculative framework-building now.
Mirrors A271's own per-token-cap-number reasoning: one specific decision
based on one real known requirement.

### Tests

Three new tests in `webhook_test.go`:

- `TestApp_NotifySignalCreated_BroadcastsToEventStream` — `App.EventStream()`
  configured, no `App.Webhooks()` — asserts the event-stream broadcaster
  receives a `"signal.created"` payload with the correct id/slug, proving
  the same decoupling T269's own tests verified for the general path.
- `TestApp_NotifySignalCreated_EnqueuesWebhook` — `App.Webhooks()`
  configured with a real endpoint subscribed to `"signal.created"`
  (`App.EventStream()` deliberately not called in this test — proves the
  webhook sink alone is sufficient, not that both sinks are required
  together) — asserts a delivery job is enqueued.
- `TestApp_NotifySignalCreated_NilSafeWhenNeitherConfigured` — neither
  configured — no panic.

### Versioning

Coverage: 96.3% package-wide; `NotifySignalCreated` itself 100%. `go test
-race ./...` clean. `golangci-lint` zero findings. MINOR bump (new
exported symbol): v1.73.2 → **v1.74.0**.

**This Amendment covers the `core`-side hook export only.** The `mcp`-side
call site (a new invocation of `App.NotifySignalCreated` in
`signal_tools.go`'s own `create_signal` handler) plus the required
dependency-pin bump from `mcp`'s current stale v1.65.0 up to this new
v1.74.0 is tracked separately as **Amendment A278** in the `mcp` repo —
blocked on v1.74.0 actually being tagged/released first, not part of this
entry's own scope.

---

## A278 — smeldr.dev/mcp: create_signal wired to App.NotifySignalCreated

### Problem

The `mcp`-side half of the two-repo fix begun in A277. `create_signal`'s
own `handleSignalTool` case did the raw `db.ExecContext` INSERT described
in A277 and returned — no call to the newly-exported
`App.NotifySignalCreated`, since that method didn't exist in any
`mcp`-resolvable `core` version until this session's own v1.74.0 release.

### Fix

One new line in `signal_tools.go`, immediately after the INSERT succeeds
and before the `toolResult` return:

```go
s.app.NotifySignalCreated(ctx, id, slug)
```

Required a `smeldr.dev/core` dependency pin bump — `mcp/go.mod` was
pinned at `v1.65.0`, 8+ minor versions behind the now-current `v1.74.0`
(same staleness class T217, still unclaimed in the backlog, exists to
catch systematically). Confirmed real before starting: `go mod download
-json smeldr.dev/core@v1.74.0`'s own `Origin.Hash` matched the exact
committed `core` SHA (`e916984`) — the version genuinely resolves on the
real proxy, not assumed from the tag having been pushed.

### Verifying the pin jump itself, not just the new line

None of the intervening `core` versions crossed a major (still v1.x,
covered by the API stability promise), but a jump this large was checked
directly rather than trusted on that promise alone, per the standing
standalone-module pre-tag checklist:

- `go mod tidy` — no diff
- `go build ./...` / `go vet ./...` — clean
- `go test ./...` — green, `mcp`'s own **full** suite, not only the
  signal-tool tests
- `go test -race ./...` — green

**Two pre-existing `golangci-lint` findings, confirmed not mine.**
`mcp_test.go`/`node_tools.go` each carry one finding (`errcheck` on an
unchecked `pw.Write`, `ineffassign` on a dead `n++`). Confirmed via `git
stash` that both already exist on `mcp`'s own `main`, in files this
Amendment never touches — reported for the record, not fixed (out of
this Amendment's own scope; fixing an unrelated file crosses the same
file-boundary rule that turns a fix into a separate Amendment).

### Tests

`TestHandleSignalTool_CreateSignal_NotifiesApp` — proves the wiring
end-to-end, not just that the call is present in source. `mcp` has no
access to `core`'s own unexported `eventBroadcaster` to inspect directly
(the same constraint A277 itself exists to work around), so the test
connects a real `GET /_events/stream` client (`httptest.NewServer(app.
Handler())`, a signed `Author`-role bearer token) and asserts the
`"signal.created"` NDJSON line actually arrives after calling
`create_signal` through the real MCP tool handler.

### Versioning

`create_signal`'s own request/response shape is unchanged — this adds a
side effect (a notification that should have always fired), not a new
parameter or response field. Coverage: 96.0% package-wide (mcp's own
established target), `handleSignalTool` 93.5%. `go test -race ./...`
clean. PATCH bump (mcp): v1.31.0 → **v1.31.1**.

---

## A279 — SweepRunRecord/SweepRunStore: a scheduled detector run leaves a record

### Problem

`App.SweepStructural` (a structural sweep, detecting unused/broken Relations)
or `App.DrainEvalQueue` (an eval-queue drain, processing pending orchestration)
previously logged one Debug-level line on a clean run and persisted nothing —
"the sweep ran and found nothing" was indistinguishable from "no sweep has ever
run" to any external observer. A scheduled detector needs to record *when* and
*what* it examined, whether or not it found anything, so a downstream caller
(e.g. a staleness-derivation task) can establish whether the sweep has run
recently enough to trust its absence of findings.

### Design — pattern-matched, not invented

`SweepRunRecord` (fields: `ID`, `Detector` string — e.g. `"structural"`,
`"eval-queue"` — `RanAt` time.Time, `Interval` string — the detector's own cron
schedule, `Walked`/`Flagged`/`Skipped` int, `Err` string) and `SweepRunStore`
interface (`Append`, `Last`, `List` methods) are modeled **directly on**
the existing `AuditRecord`/`AuditStore` pattern — the same immutable-record-plus-
store shape, established, tested, and proven in production. Both are new types,
not extensions of `Run` or `Finding` (both D38 and D51 settled these boundaries
explicitly: `Run` is a heavier, user-facing claim/lease/worktree object for M3
headless coding sessions; `Finding` is a detector-owned, subject-keyed record of
one detected condition; neither is the shape of a "one row per execution,
ordered by time, existing whether or not anything was found"). 

### Fix — signature change, explicitly confirmed

**Breaking signature change, confirmed in plan review, not taken unilaterally:**
`RelationStore.SweepStructural` and `App.SweepStructural` (both `relations.go`,
`smeldr.go`) and `App.DrainEvalQueue` (`state.go`) all gained a new leading
`walked int` return value — the total items examined that run. Without this, the
combination `flagged=0, skipped=0` cannot be told apart from "nothing to check"
versus "checked everything, found nothing," a real ambiguity that `walked` alone
resolves.

**No HTTP or MCP surface by design.** `Last`/`List` are Go-level only, leaving
a public read surface (e.g. an `/_sweep-runs` route or `list_sweep_runs` MCP
tool) to a future task if one turns out to need it — same reasoning as the
existing `/_logs` route having no MCP tool (the design's own lean, not a gap).

**Deliberately no built-in wiring of `SweepRunStore.Append` into
`agent.NewSweepScheduler`'s own signature.** The base `smeldr.dev/agent` package
is MIT-licensed and carries zero dependency on `smeldr.dev/core` by design
(verified directly — `NewEvalQueueScheduler` already takes an anonymous
interface rather than a concrete `*smeldr.App` type specifically for this reason;
only the separate `smeldr.dev/agent/flow` AGPL sub-package imports core). Adding
a `SweepRunStore` parameter to `NewSweepScheduler` would have broken that
boundary. Recording a run belongs in the **caller's own wrapping closure**
instead, with an example documented as a godoc usage comment on `SweepRunStore`
itself.

### Tests

10 new tests: `TestCreateSweepRunTable_Idempotent` (called twice, second is a
no-op); `TestSweepRunStore_AppendAndLast` (round-trip — Append then Last
retrieves the same record); `TestSweepRunStore_AppendError`/`_LastError`/
`_ListError` (DB-double-injected failures on each method); `TestSweepRunStore_LastNotFound`
(Last on a detector with no run recorded returns `found=false, err=nil`);
`TestSweepRunStore_ListOrderAndLimit` (three runs for one detector plus one for
a different detector — proves newest-first ordering, `limit` truncation, and
detector isolation all in one fixture); `TestSweepRunStore_ListEmpty`;
`TestSweepRunStore_LastScanError`/`_ListScanError` (a DB double swaps in a
wrong-column-count row so `Scan` itself fails, proving the error is wrapped
and returned, not swallowed). All pre-existing `SweepStructural` and
`DrainEvalQueue` tests updated for the new leading-return
arity, each asserting the correct `walked` count for its own scenario (clean run,
flagged case, skipped case, error case) with the new return value wired to the
assertion. Coverage: 96.3% package-wide; `sweep_run.go`'s own functions 100%;
`RelationStore.SweepStructural` 87.1% (four fatal-DB-error branches — initial
query error, initial scan error, `rows.Err()` post-iteration, per-edge UPDATE
error — remain uncovered, the same structurally-hard-to-trigger-with-a-real-driver
class already accepted elsewhere this session; named, not chased).

### Versioning

New exported symbols (`SweepRunRecord`, `SweepRunStore`, `NewSweepRunStore`,
`CreateSweepRunTable`) + breaking signature change (`walked int` return added).
No exported symbols removed. Coverage: 96.3% package-wide; `sweep_run.go` 100%.
`go test -race ./...` clean. `golangci-lint` zero findings. MINOR bump:
v1.74.0 → **v1.75.0**.

---

## A280 — smeldr.dev/agent: SweepFunc/NewEvalQueueScheduler widen for walked

### Problem

The `smeldr.dev/agent` package implements `smeldr.dev/core`'s scheduled detector
contracts. A279 widened the signature that `smeldr.dev/core` exports for
`App.SweepStructural` and `App.DrainEvalQueue` to include a leading `walked int`
return. The agent-side contract functions must match.

### Fix

**`SweepFunc` type widened:**
```go
// Old signature
type SweepFunc func(ctx context.Context) (flagged, skipped int, err error)

// New signature
type SweepFunc func(ctx context.Context) (walked, flagged, skipped int, err error)
```

**`NewEvalQueueScheduler`'s anonymous interface parameter widened to match:**
The scheduler's own internal `DrainEvalQueue` check changed from
```go
DrainEvalQueue(ctx context.Context) (triggered, skipped int, err error)
```
to
```go
DrainEvalQueue(ctx context.Context) (walked, triggered, skipped int, err error)
```

**Log-level branch improvement:** The scheduler's own `NewSweepScheduler`'s
`gocron.NewTask` closure previously checked `if flagged == 0 && skipped == 0`
to decide between Debug and Info logging. Now, any sweep where `walked > 0`
triggers Info-level logging even when `flagged` and `skipped` are both zero —
a `walked > 0` result is itself informative (it proves the sweep actually
examined something). The log line now includes the `walked` count for visibility.

### Tests

All existing scheduler tests in `sweep_test.go` updated for the new 4-return
arity — mechanical regression pins. One new test, `TestSweepScheduler_WalkedNonZeroLogsInfo`,
confirms the new log-level behavior by capturing `slog` output into a buffer
and asserting an Info-level line (not Debug) for `walked=3, flagged=0, skipped=0`.
Synchronization happens via the scheduler's own documented `Stop()` "waits for
in-flight runs" guarantee rather than a sleep, eliminating a race between the
test's own buffer read and the scheduler's still-in-flight log write.
`go test -race ./...` clean on both `smeldr.dev/agent` and `smeldr.dev/agent/flow`.

### Versioning

No exported symbols removed. `SweepFunc` and `NewEvalQueueScheduler`'s changed
signatures are exported, constituting a breaking change for any direct caller of
the scheduler constructor — but this package is pre-1.0 (v0.7.1) and the breaking
change is intentional, matching A279's own design. Coverage unchanged at this
package's own pre-existing baseline (41.4% `smeldr.dev/agent`, 56.2%
`smeldr.dev/agent/flow` — no 96% gate applies to this repo, per T105's own
backlog note). `go test -race ./...` clean. `golangci-lint` zero
findings. MINOR bump (pre-1.0, breaking change to exported function type):
v0.7.1 → **v0.8.0**.

---

## A281 — RoleStore.Grant/Revoke and TokenStore.Revoke now write ProvenanceRecord too

### Problem

Task T202 identified a gap: `RoleStore.Grant` and `RoleStore.Revoke` wrote to
`GovernanceAuditStore` only (full before/after diff record), while
`TokenStore.Revoke` wrote to neither `GovernanceAuditStore` nor
`ProvenanceStore` — just a bare `UPDATE smeldr_tokens SET revoked_at = $1
WHERE id = $2`. A UI design mockup for orchestration features assumed admin
objects (grants, revokes, token revocations) flowed through the unified
`SubjectProvenance` history that every other subject type (`RelationEdge`,
`Decision`, etc.) already uses, bridging the same actor-attribution and
state-transition visibility that D43/D44 establish for content — but the admin
side was missing that bridge entirely.

### Design

**Verified against prior decisions:** D44's boundary ("governance audit answers
who was given the right to act, ProvenanceRecord answers who then acted; a gap
in one is never argued away by pointing at the other") forbids *substituting*
one record for the other, not forbidding *both* from describing the same event.
`GovernanceAuditStore` remains the sole authoritative full-diff record,
unchanged by this Amendment. `ProvenanceRecord` adds a second, narrower entry
compatible with the same `SubjectProvenance` read mechanism. T243's
actor-gating model (where `SubjectProvenance` hides actor identity behind a
role-gated `StateFlow` transition) degrades safely to "always fully visible"
for these subjects, because neither a `RoleGrant` nor a `Token` has (or should
have) a registered `StateFlow` for `transitionIsGated` to check. This matches,
not contradicts, `GovernanceAuditStore`'s own already-transparent posture for
admin actions — an admin action is supposed to be fully attributed to whoever
can already see it, unlike a content transition where actor identity is
deliberately hidden from less-privileged viewers.

**Implementation approach:**
`RoleStore` (`governance.go`) and `TokenStore` (`auth.go`) each gained an
unexported `provenanceStore ProvenanceStore` field and a `setProvenanceStore(store
ProvenanceStore)` setter method — the exact pattern `RelationStore`'s own
existing wiring already follows. These are wired at `App.Handler()` time
(in `smeldr.go`) when both `App.Provenance(...)` and `App.Governance(...)`
are configured (for `RoleStore`), or both `App.Provenance(...)` and
`Config.TokenStore` are configured (for `TokenStore`).

`RoleStore.Grant` and `RoleStore.Revoke` each call a new unexported helper
`recordGrantProvenance(ctx, grantID, verb string)` after their existing DB
write succeeds. `TokenStore.Revoke` calls `recordProvenance` directly (inline,
no separate helper) after its own `UPDATE` succeeds. Both reuse the exact
actor-recovery pattern `RelationStore.recordAssertProvenance` already
established: a `ctx.(Context)` type assertion recovers `actorID`/`roles` when
the caller passed a concrete `smeldr.Context` (every real caller does);
`actorKindFor(actorID, roles)` derives `ActorKind`. Deliberately NOT using
`RoleStore.actorTokenID` (the field `WithAudit` sets) for `ProvenanceRecord.ActorID`
— that field is a token fingerprint for `GovernanceAuditRecord`'s own actor slot,
a different identity space than `ProvenanceRecord.ActorID`'s user/job/agent-identifier
contract.

New `SubjectType` values: `"RoleGrant"` (used by both `Grant` and `Revoke`) and
`"Token"` (used by `TokenStore.Revoke`) — the first non-`Node`, non-`RelationEdge`
subject types `ProvenanceRecord` has ever carried. `Verb` values reused rather
than added: `"assert"` for `Grant`, `"invalidate"` for both `Revoke` calls —
mirroring `RelationEdge`'s own existing assert/invalidate pair for a structurally
identical create/remove shape, rather than growing the `Verb` enum with new
`"grant"`/`"revoke"` values for one more subject kind.

Fail-open throughout, matching every other `recordProvenance` call site in the
package: `recordProvenance` itself already logs-and-swallows an `Append` failure,
so a provenance write failure never fails the grant/revoke/token-revoke operation
itself.

### Tests

13 new tests total, covering success paths, nil-store no-op, append-failure graceful
fail-open, plain-context empty-actor cases, and real end-to-end `App.Handler()` wiring:

In `governance_test.go`:
1. `TestRoleStore_Grant_RecordsProvenance`
2. `TestRoleStore_Grant_NilProvenanceStore_NoOp`
3. `TestRoleStore_Grant_ProvenanceAppendFails_GrantStillSucceeds`
4. `TestRoleStore_Grant_PlainContext_ActorEmpty`
5. `TestRoleStore_Revoke_RecordsProvenance`
6. `TestRoleStore_Revoke_NilProvenanceStore_NoOp`
7. `TestRoleStore_Revoke_ProvenanceAppendFails_RevokeStillSucceeds`
8. `TestAppHandler_WiresProvenanceIntoGovernance` (end-to-end test driving the real
   `App.Handler()` wiring, not calling `setProvenanceStore` directly)

In `auth_test.go`:
9. `TestTokenStore_Revoke_RecordsProvenance`
10. `TestTokenStore_Revoke_NilProvenanceStore_NoOp`
11. `TestTokenStore_Revoke_ProvenanceAppendFails_RevokeStillSucceeds`
12. `TestTokenStore_Revoke_PlainContext_ActorEmpty`
13. `TestAppHandler_WiresProvenanceIntoTokenStore` (end-to-end test driving the real
    `App.Handler()` wiring)

### Versioning

No new exported Go symbols were added or removed (`provenanceStore` field and
`setProvenanceStore` method are both unexported; `recordGrantProvenance` is
unexported). No HTTP or MCP surface was added. Real consumer-observable behaviour
change: `SubjectProvenance` can now return two new `SubjectType` values
(`"RoleGrant"` and `"Token"`) that it never produced before — matching how
Amendment A275's own 429-connection-cap change was also classified as PATCH for
the same reason: no new exported symbol, but tag-worthy consumer-observable
behaviour on a read path. PATCH bump: v1.75.0 → **v1.75.1**.

Coverage: 96.3% package-wide (`go test -race -count=1 -coverprofile=coverage.out
./... ; go tool cover -func=coverage.out`). `setProvenanceStore` on both
`RoleStore` and `TokenStore`, and `recordGrantProvenance`, are all 100% covered.
`Grant`/`Revoke` themselves remain unchanged from their pre-existing baseline
coverage (96.4%/95.1% respectively) — the new code paths inside them are fully
covered; the small gap is pre-existing and unrelated to this change. `go test
-race ./...` clean. `golangci-lint run ./...` zero findings.

---

## A282 — clean-clone build check + pin-currency check (T217)

### Problem

No periodic check existed for (a) building fresh with no `go.work`/sibling
repos, or (b) whether declared `smeldr.dev/*` pins are current — both
distinct, both real, traced to three incidents in one week: an untagged
release, a near-miss stale `mcp` pin blocking a deploy (T266), and
`example/server` unbuildable from a clean clone for a month (A245),
invisible because every real build in this project went through
`go.work`'s local override.

Confirmed directly: `.github/workflows/ci.yml`'s existing `test` job runs
`go test`/`go vet` at repo root only — it never enters `example/api`,
`example/blog`, `example/docs`, or `example/server`, each its own Go module.
`release.yml` builds `example/server` only, only on a pushed tag, with
`GOWORK: off` already set — the one place this project already did a
clean-clone-style build, confirming the pattern to reuse rather than invent.

### Fix — two checks

**Check 1, `ci.yml`'s new `examples` matrix job**, same triggers as the
existing `test` job (`push: [main]`, `pull_request: [main]`), so a broken
example is caught on the commit that breaks it:

```yaml
examples:
  name: Build examples (clean clone)
  runs-on: ubuntu-latest
  strategy:
    matrix:
      dir: [example/api, example/blog, example/docs, example/server]
  steps:
    - uses: actions/checkout@v4
    - uses: actions/setup-go@v5
      with:
        go-version-file: ${{ matrix.dir }}/go.mod
    - working-directory: ${{ matrix.dir }}
      env: { GOWORK: 'off' }
      run: go build ./...
    - working-directory: ${{ matrix.dir }}
      env: { GOWORK: 'off' }
      run: go vet ./...
```

A matrix job, not a loop in one job, so which example broke is visible at a
glance in the Actions UI. Deliberately validates every example against
core's current checked-out HEAD (every example replaces `smeldr.dev/core =>
../..`, confirmed below) — not against any published core version. Argued,
not assumed: this is strictly the right and sufficient scope for what this
check exists to catch (A245's own incident — a core-repo commit that breaks
an example, caught on that same commit, before any tag ships), and strictly
stronger than a published-pin build would be for that purpose. A
published-pin build would be a different, real check (does the *last
released* core version still build against the *last released* pin) that
belongs to pin-currency's own territory, not this one's.

**Check 2, new `.github/workflows/pin-currency.yml`**, cron-triggered
(`0 6 * * 1`, every Monday) plus `workflow_dispatch` for on-demand runs —
staleness accrues from an external release, not a commit to this repo, so a
push-triggered check alone can go stale for weeks against a fully green
`main`. New `scripts/checkpins` — its own Go module (`golang.org/x/mod/modfile`
beyond stdlib; a CI-tooling-only import, never part of the `smeldr` package's
own zero-dependency module graph), run via `go run . <dir>...`:

1. Parse each `<dir>/go.mod`'s `require`/`replace` directives.
2. For each `smeldr.dev/*` requirement that is **direct** (not `// indirect`)
   and **not** covered by an active `replace`, run `go list -m -versions
   <module>` against the real proxy.
3. Compare the highest listed version to the pinned one; collect every
   mismatch across every directory given.
4. Exit non-zero, printing every stale pin, if any were found; exit 0
   (silent) otherwise.

`checkModule(dir, data, latest)` is the testable core — pure parsing plus
comparison, `latest` injected as a `func(module string) (string, error)` so
unit tests never make a live network call; `goListLatestVersion` (the real,
`exec.Command`-backed implementation) and `main` (CLI plumbing, `os.Exit`)
are deliberately untested — the real proxy call is an integration concern
the scheduled workflow itself exercises after merge, not something to fake
convincingly in a unit test.

**Indirect requires are skipped by design**, discovered mid-implementation
to matter for a real reason, not decided in the abstract: T217's own three
motivating incidents are all about a *direct* dependency missing a
capability a caller actually reaches (a tool, an exported symbol) — an
indirect pin's staleness is a different, lower-signal risk class, and `go
mod tidy` already manages indirect versions on its own without a human
needing to track them individually.

### Two plan-time assumptions corrected by actually running the tool, not by inspection

Running `checkpins` for real against all four examples during grounding
disagreed with the plan's own written assumptions on two of the (then)
"four known-stale pins," in both directions:

- **`example/server`'s `smeldr.dev/agent v0.7.1` is not actually stale.**
  The plan assumed the real current version was `v0.8.0`, based on this
  same session's own earlier work (T223) landing that version on `main` —
  but `main` and *tagged* are different facts, and `v0.8.0` was pushed
  without a tag (no fresh explicit release approval yet, per this project's
  own standing "stop after push, no tag/release without a fresh explicit
  yes" discipline). `go list -m -versions smeldr.dev/agent` against the
  real proxy still correctly reports `v0.7.1` as latest. Bumping the pin to
  `v0.8.0` here would have pinned a version that does not exist as an
  installable tag — the tool's own correct, proxy-grounded behavior caught
  this before it became a real mistake, not after.
- **`example/blog`'s `smeldr.dev/oauth v0.2.0` (indirect) was found stale**
  (real current: `v0.4.0`) — a pin entirely outside the plan's own original
  two-pin scope, surfaced only by actually running the comparison against
  every requirement in the file rather than the two the plan had
  specifically named in advance. Resolved as a side effect of the design
  decision above (indirect requires skipped) rather than bumped by hand —
  `go mod tidy` is what already owns this class of drift.

### Pin bump shipped in this same task

`example/blog`'s `smeldr.dev/mcp v1.30.0 → v1.31.1`, via `go get
smeldr.dev/mcp@v1.31.1 && go mod tidy` (never hand-edited version strings,
matching this project's own standing discipline) — verified with
`GOWORK=off go build ./...`/`go vet ./...`/`go test ./...` all clean in
`example/blog` afterward, and a full re-run of `checkpins` against all four
examples confirming zero stale pins remain. `example/server`'s `agent` pin
is **not** touched, per the correction above.

### Tests

9 new tests in `scripts/checkpins/main_test.go`:
`TestCheckModule_SkipsReplaced`, `TestCheckModule_ReportsStalePin`,
`TestCheckModule_CurrentPinNotReported`, `TestCheckModule_SkipsIndirect`,
`TestCheckModule_IgnoresNonSmeldrModules`,
`TestCheckModule_MultiplePinsSomeStale`, `TestCheckModule_LatestLookupError`,
`TestCheckModule_MalformedGoMod`, `TestCheckDir_ReadError`. `checkModule`
itself 100% covered; `main`/`goListLatestVersion` untested by design (see
above). This is a standalone tooling module with its own `go.mod`, not part
of the `smeldr` package's own 96% coverage gate.

`ci.yml`'s new `examples` job verified by actually running each example's
`go build ./...`/`go vet ./...` locally with `GOWORK=off` before proposing
this commit — the same commands the workflow itself runs.
`pin-currency.yml` cannot be verified before merge (GitHub Actions does not
run an unmerged workflow file's own `schedule`/`workflow_dispatch` triggers
against a branch) — flagged as a real limitation, not glossed over: the
first real proof this works end-to-end is a manual `gh workflow run` after
this lands on `main`.

### Versioning

No `smeldr` package code touched — `smeldr.dev/core` itself is not
released by this change. Level 1 amendment: config/tooling-only, no
version bump, no tag, matching A245/A246's own precedent for exactly this
class of change.

---

## A283 — BuildContextPacket's Published-only gate removed, GET /packet/{type}/{slug} now requires Editor role (T159)

### Problem

`context_packet.go:233`/`:335` both required `Status == Published` before
including an orchestration item in a context packet. `BuildContextPacket`
exists only for the five orchestration anchor types (`goal`, `decision`,
`amendment`, `task`, `signal`); none of their registered `StateFlow`
states is ever the literal string `"published"` — `applyDefaultStatus`
sets a new item's `Status` to its flow's own initial state (`"backlog"`
for `Task`, `"open"` for `Goal`, `"proposed"` for `Decision`, `"scoped"`
for `Amendment`, `"pending"` for `Signal`), and every later transition
writes the same column with the flow's own state names, never lifecycle
vocabulary. The gate has had a 0% pass rate for its entire domain,
unconditionally, since it shipped (A214, T145, 2026-07-12) — over a
month. Every existing test passed regardless, because the shared
`insertTest*` fixtures hardcode `Status: Published` directly, bypassing
the real creation path and never exercising the actual bug.

### Fix, part 1 — remove both gates

No narrowing, no swapping `Published` for a different constant — D56
already settled that "governed state" (what these five types have) and
"lifecycle" (a CMS-content-visibility question) are two independent axes,
and there is no lifecycle-Status concept that means anything for a `Task`
or `Decision`. Matches D47's own precedent directly: for a compiled type,
alive means the row exists, no status consulted — applied here to packet
visibility instead of structural-sweep liveness. An archived `Decision`
or a `done` `Task` is still real, legitimate operational history.

`Run`'s absence from `anchorTypeTable` is a related but distinct gap, left
out of scope: `BuildContextPacket`'s own doc comment already scopes itself
to the five orchestration anchor types, and D38 is explicit that `Run`
deliberately carries no `StateFlow` at all.

### Fix, part 2 — held twice, then the access-model layer

**First hold (2026-08-16).** Removing the gate as designed would have
shipped a real, live exposure: `GET /packet/{type}/{slug}` was designed
unauthenticated for an isolated demo instance (A214's own text: "intended
for isolated demo instance with public read access") that has never been
deployed — but the handler is mounted on the real, live instance today
whenever `ENABLE_RELATIONS && ENABLE_ORCHESTRATION` are both set, which
they are. Removing the status gate would have made every orchestration
item on the real instance publicly readable with a guessable,
human-readable slug (`t255-orchestration-flows-...`, not an opaque ID) —
a live enumeration risk, not theoretical. Held for Peter's own access-model
decision rather than either implementer deciding alone overnight on a live
instance carrying real data.

**Peter's decision (2026-08-16):** require login. Proposed default,
confirmed in this task: gate behind a valid token, `Editor` role minimum —
matches every MCP tool's own baseline for reading the same class of data
(`get_task`/`get_decision`/etc.), not `Guest` (equivalent to no gate) and
not `Admin`-only (this is a read of already-permitted-in-principle
operational context, not an authority-bearing act).

**Second hold (2026-08-20).** The plan sat at `plan-reviewing` for four
days with Peter's answer never actually turned into a design — the text
on file was still the pre-hold, no-auth version. Held again rather than
approved as written, with three specific points named for the replan:
the `Editor`-role gate itself, the mechanism (raw handler, no existing
`readRole`/`checkWriteOp` to reuse), and whether `example/server`'s
wiring needed its own flag now that "on" means "on the real instance."

### Fix, part 3 — the actual design

**Auth mechanism.** `ContextPacketHandler` is a raw
`http.HandlerFunc` registration, structurally identical to three existing
raw handlers that already gate on role: `newAuditHandler`, `newLogsHandler`,
`newEventStreamHandler`. All three resolve `auth := a.cfg.Auth; if auth ==
nil { auth = BearerHMAC(string(a.cfg.Secret)) }` once at registration time,
then inside the handler: `user, ok := auth.authenticate(r)` →
`ErrUnauth` if not ok; `user.HasRole(Editor)` → `ErrForbidden` if not.
Applied the identical five-line pattern directly to `ContextPacketHandler`
rather than extracting a shared helper — this would be the fourth use of
the identical shape (a real argument for extraction), but the first three
shipped it inline three separate times without factoring it out, so
inlining a fourth time follows this codebase's own established convention
rather than introducing a new one unprompted mid-fix. `AuthFunc.authenticate`
is intentionally unexported (prevents accidental direct calls, only
callable from inside the `smeldr` package) — matches `ContextPacketHandler`'s
own location in the same package. Extraction is a real, reasonable future
refactor once a fifth raw handler needs the same check, not this one.

`user.HasRole(Editor)` is hierarchical (`roles.go`: "Admin satisfies a
check for Editor") — `Editor` and `Admin` tokens both pass, `Author` and
`Guest` both 403.

**`example/server` wiring.** New `EnableContextPacket bool` /
`ENABLE_CONTEXT_PACKET` field, checked in addition to (not instead of) the
existing `EnableRelations`/`EnableOrchestration` prerequisites:
`cfg.EnableRelations && cfg.EnableOrchestration && cfg.EnableContextPacket`.
Every other optional HTTP surface in this binary — `EnableWebhooks`,
`EnableEventStream`, `EnableProvenance`, `EnableMedia`, etc. — already gets
its own explicit flag, one feature one flag, even where a feature has its
own structural prerequisites. Bundling `ContextPacketHandler` under the
side effect of two unrelated flags being true together was the one place
this binary's own config surface broke its own established convention —
worth fixing now that the risk conversation is already open, not just once
auth exists. Authentication lowers the risk of the route existing; it does
not change whether an operator who only wanted relation-assertion MCP
tools should also, silently, get a new HTTP GET surface mounted. The new
flag defaults off, so upgrading with the old two-flag combination already
set does not silently re-enable the route.

### Tests

Two existing tests inverted (not just re-asserted): `TestBuildContextPacket_draftAnchor`
(a `Draft` anchor now resolves, was `ErrNotFound`) and
`TestBuildContextPacket_draftLinkedItemExcluded` → renamed
`_draftLinkedItemIncluded` (a `Draft` linked item now appears in `Items`,
was silently skipped). Two new tests build items with real flow-state
values directly (`"backlog"`, `"open"`, `"proposed"`, `"active"`) instead
of changing the shared `insertTest*` helpers' hardcoded `Published` —
`TestBuildContextPacket_realisticFlowStatus_taskAnchor` reproduces the
literal reported bug (a `Task` anchor with `Status: "backlog"`);
`_linkedItems` covers a `Goal` anchor with a linked `Decision` and `Task`,
neither `Published`. Four new handler-level auth tests: `_Unauthorized`
(no token, 401), `_ForbiddenForAuthor` (403, below `Editor`), `_OKForAdmin`
(200, proving the hierarchical check), `_200_realisticFlowStatus`
(end-to-end through the real HTTP handler with a `"backlog"` Task anchor —
reproduces T159's own reported symptom and proves it now returns 200, not
404). The three pre-existing handler tests (`_200`, `_400_invalidDepth`,
`_404_unknownSlug`) each gained a real `Editor`-role bearer token, now
required to reach what they were already testing. `example/server` gains
`off/contextPacketWithoutOwnFlag`, proving the route stays unmounted with
`EnableContextPacket` false even when the other two prerequisites are true;
the existing `on/contextPacket` test updated to set all three flags and
carry a token, plus a new no-token sub-assertion (401) proving the new
flag does not relax auth.

The shared `insertTest*` fixtures' hardcoded `Status: Published` — the
reason this bug went undetected for over a month — is flagged, not fixed:
these helpers are shared across many unrelated tests well beyond this
bug's own scope, and auditing every caller for an unintended assertion
dependency is a separate, larger piece of work.

### Versioning

No exported Go symbol changed — `BuildContextPacket`'s signature and every
exported type are unchanged; `ContextPacketHandler`'s signature is
unchanged (the auth resolution happens inside, using existing `Config`
fields already read by other handlers). Real, significant
consumer-observable behaviour change on both counts (gate removed, auth
added) on the same route. Coverage: 96.3% package-wide (`ContextPacketHandler`
92.6% — one pre-existing, unrelated gap: a valid custom `?depth=2` value
at the HTTP-handler level was never covered before this change either,
named not chased). `go test -race ./...` clean. `golangci-lint` zero
findings. PATCH bump, matching A247/A253/A261/A281's own precedent
(behaviour fix, no new exported symbol, still versioned): v1.75.1 →
**v1.75.2**.

---

## A284 — MCPModule.MCPPublish/MCPSchedule/MCPArchive gain a reason parameter (T237)

### Problem

A220 added `Transition.RequiredReason bool` and threaded a `reason string`
parameter into `validateTransition`'s own signature, but A256's own
investigation (2026-08-13) found every real call site in `module.go` —
`updateHandler`, `MCPPublish`, `MCPSchedule`, `MCPArchive` — passed a
hardcoded `""`. A `RequiredReason`-gated transition has been structurally
unreachable through any REST or MCP entry point since A220 shipped:
declaring the gate on a `StateFlow.Transitions` entry had no observable
effect anywhere except `App.TransitionItemWithReason`, A256's own new
method for the `transition_item` MCP tool. A256 deliberately narrowed its
own scope to that one method rather than the four `module.go` call sites,
tracking the gap as T237 — "a wire-format question, not a
parameter-threading one," since `MCPPublish`/`MCPSchedule`/`MCPArchive`
are methods on the exported `MCPModule` interface (changing their
signature is a breaking change to every implementer), while
`updateHandler` needed a design decision about where an HTTP `PUT` caller
supplies a reason without colliding with the request body's own decoded
fields.

### Design

`MCPModule`'s three methods (`mcp.go`) each gain a trailing
`reason string` parameter:

```go
MCPPublish(ctx Context, slug, reason string) error
MCPSchedule(ctx Context, slug string, at time.Time, reason string) error
MCPArchive(ctx Context, slug, reason string) error
```

`Module[T]`'s three implementations (`module.go`) thread `reason` into
their existing `validateTransition(...)` call, replacing the hardcoded
`""` argument — no new logic, the gate itself (`RequiredReason` check
inside `validateTransition`) already existed and already worked correctly
whenever it was actually reached; the whole gap was that no caller ever
supplied a real value.

`updateHandler` reads a new `Smeldr-Reason` request header rather than a
body field, guarded by the same `if prevStatus != newStatus` condition
already wrapping its `validateTransition` call (so a same-status content
edit never reads the header at all — matches `updateHandler`'s own
existing pattern of only paying the `validateTransition` cost on an actual
transition). A reserved JSON body key (e.g. `"reason"` or `"Reason"`) was
considered and rejected: `updateHandler` decodes the request body directly
into the caller's own struct type via `json.Unmarshal`, so any content
type that happens to declare its own `Reason` field (plausible — it is not
an unusual word) would collide with a reserved key silently, not with a
compile error. An out-of-band header cannot collide with a decoded field
by construction. `serveblocks.go`'s own `buildData`/`Status` handling was
checked as the closest existing precedent for a reserved-body-key
collision risk in this codebase and confirmed as a live, not merely
theoretical, concern.

### Breaking change (D53)

This is a breaking change to the exported `MCPModule` interface — any
independent implementer (not just `Module[T]`) must update its own method
signatures to keep compiling against the new `smeldr.dev/core` version. No
compatibility twin (e.g. an `MCPPublishWithReason` sibling method
preserving the old three-argument signature) is added, per D53: breaking
changes are taken inside v1 as long as no external importer exists yet,
checked against the module proxy's own importers index (T217/A282's
pin-currency mechanism), not assumed. `smeldr.dev/mcp`, `smeldr/media`,
and `smeldr/social` are the three known implementers, all `smeldr.dev/*`
ourselves — updating each of their own `MCPModule` implementations to the
new three-parameter shape is out of this task's core-only scope, tracked
as separate future Tasks per D54 (two-repo Amendment cycles split: core
ships and tags first, downstream repos get their own Task once core is
tagged and proxy-verified). Until that lands, `smeldr.dev/mcp`'s own
`publish_{type}`/`schedule_{type}`/`archive_{type}` tool dispatch code
cannot compile against this core version at all — pinning to v1.76.0 in
that repo is itself part of the deferred downstream Task, not a
consequence discovered after the fact.

### Tests

6 new tests in `state_test.go`. `requiredReasonPostFlow` registers a
`StateFlow` for `testPost` whose three draft-originating transitions
(`draft→published`, `draft→scheduled`, `draft→archived`) all require a
reason — reused by all six new tests rather than three separate fixtures.
`TestModule_MCPPublish_ThreadsReason`/`_MCPSchedule_ThreadsReason`/
`_MCPArchive_ThreadsReason` each call the method once with `""` (asserts
`ErrBadRequest` — proves the gate is actually live for this flow before
testing satisfaction) and once with a real reason string (asserts `nil`
error — proves the parameter reaches `validateTransition`'s own last
argument, not merely that it compiles).
`TestModule_updateHandler_ReasonHeader_ThreadsToValidateTransition` builds
a real `App` via `New(MustConfig(Config{DB: sqlDB, ...}))` + `app.Content(m)`
and drives a real `httptest` `PUT` request with `Smeldr-Reason` set,
asserting 200.
`TestModule_updateHandler_RequiredReason_MissingHeader_StillRejected` is
the same request with the header omitted, asserting 400 — this test
caught a real bug during implementation, not merely confirmed the design:
`App.Content` unconditionally calls `dbs.setDB(a.cfg.DB)` on any module
implementing `setDB`, which silently overwrote an `m.setDB(sqlDB)` call
made *before* `app.Content(m)` with `nil` (since `Config.DB` was left
unset), making `validateTransition` unable to find the registered flow at
all and passing both the reason-present and reason-missing requests
through as 200s. Fixed by passing `DB: sqlDB` in the `Config` literal
itself, letting `App.Content`'s existing wiring do what it already does
for every other `Module[T]` test elsewhere in this file, rather than
racing it. `TestModule_updateHandler_NoReasonHeader_SameStatusUnaffected`
pins the common case (a same-status content edit) never reaching the gate
at all, regardless of the header's absence.

### Versioning

No exported symbol removed; three exported interface methods changed
signature (breaking). Coverage: 96.3% package-wide. `go build ./...`,
`go vet ./...` (catches the 25 call-site compile errors across
`state_test.go`, `module_test.go`, `slug_collision_test.go`, `mcp_test.go`,
`integration_full_test.go` that `go build` alone does not, since it never
compiles `_test.go` files), `go test -race -count=1 ./...`, `gofmt -l .`,
and `golangci-lint run ./...` all clean. MINOR bump per D53's own addendum
(a breaking change inside v1 takes the largest signal version numbers can
carry within v1 — PATCH would be actively misleading, and the version
number alone cannot state `BREAKING`, which is why the CHANGELOG entry and
this Amendment both state it in the text a person reads before upgrading):
v1.75.2 → **v1.76.0**.

---

## A285 — App.SweepStructural wired on a real cron schedule (sweep-structural-wired-on-schedule)

### Problem

Zero detectors ran on the live `process.smeldr.dev` instance. `App.SweepStructural`
and `App.DrainEvalQueue` both existed, tested, and (T223/A279) left a
`SweepRunRecord` when run — but nothing called either on a schedule.
Deliberately left unwired twice before: A240 named four blockers for
`DrainEvalQueue` (provenance, Signal dispatch, cache invalidation,
authority-check); T223/A279 gave the same caution for `SweepStructural`
("no observability, no silent automated authority"). This task's own
dispatch: verify each of A240's four blockers directly before wiring
anything, rather than assuming they're closed or still open either way.

### The two detectors are not in the same state

`App.SweepStructural` (`smeldr.go:1025`) already has exactly the shape
`agent.SweepFunc` expects. Its own `onStale` callback already fires
`AfterRelationCascade` via `a.emitSignal` → `a.dispatchBus` — the same
central dispatch path webhook delivery and the event stream already use.
All four of A240's concerns, applied to this detector, are closed:
observability (T223's `SweepRunRecord`), Signal dispatch (already wired
through `dispatchBus`), and authority — this detector mutates a relation
edge's `invalid_at`, never a content item's own governed `Status`, so
`validateTransition`/`RoleGranted` were never the relevant gate here.

`App.DrainEvalQueue` (`state.go:1014`) is different: T211/A258 closed two
of A240's four (`drainAuthorizationGate` — a role-gated transition is
never applied automatically, an `authorization-required` Signal is
recorded instead; `recordProvenance` on every successful automated
transition). The other two remain open by explicit, current-source
design, quoting `state.go:1088-1103` directly: *"signal dispatch, cache
invalidation and rebuild triggers are deliberately out of scope... firing
`AfterPublish`-class signals for an automated transition would activate
every human-publish subscriber with no operator decision that background
automation should trigger them."* Wiring `DrainEvalQueue` on a schedule
today would mean a ratified `Decision` can flip to `pending-re-evaluation`
with zero Signal dispatch and zero cache invalidation — exactly the class
of silent effect A240 refused to ship. **Deliberately not wired this
cycle** — reversing T211's own scope decision is a real design call with
its own consequences, left for its own future Task rather than bundled
into this one as a side effect.

### Design

`example/server/main.go`: new `EnableStructuralSweep`/
`ENABLE_STRUCTURAL_SWEEP` toggle (requires `EnableRelations` explicitly —
fails loudly via a config error, matching the file's own existing
`EnableRelations && EnableOrchestration` compound-gate precedent, rather
than silently no-op through `App.SweepStructural`'s own `(0,0,0,nil)`
short-circuit for a nil `RelationStore`) and `StructuralSweepSchedule`/
`STRUCTURAL_SWEEP_SCHEDULE` (5-field cron, default `"0 * * * *"` —
hourly). `smeldr.dev/agent` bumped from v0.7.1 to v0.8.0 in
`example/server/go.mod` (v0.7.1 predates A280's own `walked`-widening,
so `agent.NewSweepScheduler`'s current signature doesn't exist there).
`smeldr.CreateSweepRunTable`/`smeldr.NewSweepRunStore` wired at startup;
a wrapping closure calls `app.SweepStructural(ctx)` then records a
`SweepRunRecord` — the exact pattern `sweep_run.go`'s own doc comment
already specifies, since the dependency-free `smeldr.dev/agent` package
can't record one itself without importing `smeldr.dev/core`. `buildApp`
falls back to the `"0 * * * *"` default itself when
`cfg.StructuralSweepSchedule` is empty (not just relying on
`parseConfig`'s own `envOr` default) — matches `agent.NewEvalQueueScheduler`'s
own established default-fallback pattern, protecting any caller
constructing `ServerConfig` directly rather than only through `parseConfig`;
caught by a real test failure during implementation
(`TestServerToggles/on/structuralSweep` failed with a `gocron` crontab
parse error against `baseConfig()`'s own zero-value schedule field before
this fix), not assumed.

### Tests

3 new subtests in `example/server/main_test.go`'s `TestServerToggles`:
`off/noStructuralSweep` (table not created when the flag is off),
`on/structuralSweepRequiresRelations` (calls `buildApp` directly, asserts
an error when `EnableRelations` is false), `on/structuralSweep` (table
created when both flags are set). Testing depth matches this file's own
established level for background-goroutine/infrastructure toggles
(structural — is it wired — not a deep behavioural check of a fired
scheduled run, since the fastest real cron granularity is one minute,
impractical for a unit test; the scheduler mechanism itself is already
tested in `smeldr.dev/agent`'s own repo).

### Versioning

No `smeldr` package file touched — `example/server`-only (config,
wiring, its own `go.mod` pin, its own tests). No version bump, no tag,
matching A245/A246/A282's own precedent for example-directory-only
changes. Level 1 amendment.

---

## A286 — provenance.go List's dead increment (SA4006)

Caught blocking a green `main` CI while closing out the T237 tag/release
cascade (core v1.76.0 → mcp/social/media → `example/*` pin bumps) —
`staticcheck`'s SA4006 flagged `provenance.go`'s `List` function: after
the last of five optional filter branches (`f.ActorID != ""`), `n` is
incremented one final time but never read again — dead code, no
behavioural effect either way, since nothing downstream consults `n`'s
post-increment value.

Same bug class independently found and fixed the same session in
`smeldr.dev/mcp`'s `node_tools.go` (`listNodes`, identical shape: a
placeholder counter's final increment after the last conditional branch
using it). Neither is a regression — both predate this session by weeks
(`provenance.go`'s version since `9db0eaf`, `feat(provenance): add
SubjectProvenance read mechanism`, Amendment A260).

Fix: removed the dead `n++`. `gofmt`/`go vet`/`go build`/`go test
./...`/`golangci-lint run ./...` all clean; coverage unchanged at 96.3%.
No exported symbol changed, no behaviour change. PATCH bump — a version
number's only purpose left to serve here is giving the fix a tag to hang
a green CI check on: v1.76.0 → **v1.76.1**.

---

## A287 — remove legacy X-Forge-* webhook headers (T87)

### Problem

`httpDeliver` (`outbound.go`) dual-emitted the preferred `X-Smeldr-*`
webhook headers alongside the legacy `X-Forge-*` set, a deprecation
window opened at the Forge→Smeldr rename (T86) and originally deferred
behind a major version bump. D53 (2026-08-14) established that a
breaking change is taken inside v1 as soon as no external importer
exists to protect — checked against the real module proxy's importers
index, not assumed. Peter's own explicit go-ahead extended that
reasoning past core's exported Go API to this class of wire-protocol
compatibility on 2026-08-15 ("let's delete them now,"
`backlog-audit-2026-08-14.md#T87`), closing the live Task
`t87-remove-forge-compat-shims`.

### Fix

`httpDeliver`'s four `X-Forge-*` `req.Header.Set` calls removed; only
`X-Smeldr-Signature`/`X-Smeldr-Timestamp`/`X-Smeldr-Event`/
`X-Smeldr-Delivery` are set now. `signPayload`'s own doc comment
(previously citing both header names) updated to reference only
`X-Smeldr-Signature`. `outbound_test.go`'s `X-Forge-*` assertions and
its `X-Smeldr-Signature == X-Forge-Signature` equality check removed —
the `X-Smeldr-*` assertions are now the test's only header checks.
`docs/REFERENCE.md` and `docs/SECURITY.md` updated to describe only the
current, single header set; `docs/ARCHITECTURE.md`'s own dated
changelog entry from A223 (2026-07-28) — which documented the
dual-emission as a then-current, deliberate preservation — is left
untouched, being a historical record of that commit's own state, not
live documentation.

### Companion Amendments

This is the `smeldr.dev/core` half of a three-repo task (D54: two-or-more
-repo Amendment cycles are still one Task, not necessarily separate
ones, when Peter's own go-ahead already covers all three at once).
`smeldr.dev/mcp` removes the legacy `forge://` MCP resource URI scheme
(own CHANGELOG v1.32.1). `smeldr.dev/cli` removes the legacy `FORGE_*`
env var fallbacks and the `.forge-cli.env` file fallback (own
CHANGELOG, version TBD — checked before tagging, not assumed here).

### Versioning

No exported symbol changed. Coverage unaffected (existing tests
exercise the same code paths, minus the removed assertions). `go build`/
`go vet`/`go test ./...` clean. PATCH bump, matching A226/A281/A283/
A286's own precedent for a real, consumer-observable behaviour change
with no new exported symbol: v1.76.1 → **v1.76.2**.

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
