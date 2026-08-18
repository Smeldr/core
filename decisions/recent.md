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
