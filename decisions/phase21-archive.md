# Smeldr — Decisions Archive (Phase 21)

Archived from `decisions/recent.md` on 2026-08-08. Entries A227-A233, D33-D37, A234-A235.

---

## A227 — `smeldr.dev/oauth`: RFC 8707 resource indicators + RFC 9207 iss identification (T183, standalone module)

### What

Scoped by T182's MCP 2026-07-28 spec design spike
(`smeldr/architect/design/mcp-2026-07-28-spike.md`, §7). Two RFCs implemented
in `smeldr.dev/oauth` (a standalone module, own go.mod/CHANGELOG/versioning —
not the `smeldr` core package):

**RFC 8707 (Resource Indicators):** new `Config.Resource` — the canonical
resource identifier this authorization server issues audience-bound tokens
for. **Required — `New` panics if empty**, same pattern as the existing
`Issuer`/`VerifyBearer` checks. `resource` is now a required parameter at
both `GET/POST /oauth/authorize` and `POST /oauth/token`; missing or
mismatched resource is rejected (`invalid_request` for missing, RFC 8707's
own `invalid_target` for a mismatch, at both endpoints).
`parseAuthorizeParams` became a method (`(s *Server) parseAuthorizeParams`) —
it needs `s.cfg.Resource` to validate against; both call sites already had a
`*Server` receiver. New `AuthCode.Resource`/`AccessToken.Resource` fields
persist the value through the full code → token flow. Refresh-token grants
re-issue for the server's single configured `Config.Resource` directly — no
client-supplied resource in that grant, since this server only ever pairs
with one resource server (a 1:1 auth-server/resource-server deployment, the
documented MCP pattern; full multi-resource-server complexity was
deliberately not built).

**RFC 9207 (Authorization Server Issuer Identification):** `iss` is now
always included in the `/oauth/authorize` redirect (unconditional, not
partial — this server declares `authorization_response_iss_parameter_
supported: true` in RFC 8414 metadata, so there is no partial-support
ambiguity to gate on).

**Found mid-implementation, not in the original plan:** the plan only
covered the `Store` interface (`store.go`) — `sqlite.go`'s bundled
`SQLiteStore` (the production `Store` implementation) needed its own schema
and INSERT/SELECT changes too. Caught immediately by the test suite (a
resource-mismatch failure on a request that should have succeeded, traced to
`GetCode`/`GetToken` silently never reading back the new column). Fix: new
`resource` column on `smeldr_oauth_codes` and `smeldr_oauth_tokens`;
existing databases migrated automatically via a new idempotent
`migrateAddResourceColumn` (`migrate.go`), following the exact same
PRAGMA-`table_info`-check-then-`ALTER TABLE` convention already established
by `migrateLegacyTableNames` (the v0.3.0 table-rename migration). Runs after
`migrateLegacyTableNames` and before `CREATE TABLE IF NOT EXISTS`, so a fresh
install never reaches it with a table missing the column.

`smeldr.dev/mcp`'s own `POST /mcp` doc-comment fix ships as a separate
amendment (A228) — different repo, different consequences (no version
bump/tag there vs. a breaking minor bump here).

### Rejected

- **Redirect-based OAuth errors for the new `resource` validation failures**
  at `/oauth/authorize` (redirecting to `redirect_uri` with `?error=
  invalid_target`, the more "standard" OAuth error-reporting shape) —
  rejected in favour of matching every other validation failure already in
  `parseAuthorizeParams`, all of which return a plain 400 via `http.Error`,
  not a redirect (defensible: `redirect_uri` isn't confirmed trustworthy
  until after the CIMD fetch that happens later in the handler). Introducing
  a redirect-based special case for only the newest check would be
  inconsistent with the file's own established pattern.
- **A configurable list of accepted resources** (multi-resource-server
  support) — `Config.Resource` is a single string. Rejected as unnecessary
  complexity: every known/planned Smeldr deployment pairs one
  `smeldr.dev/oauth` instance with exactly one `smeldr.dev/mcp` instance.
- **A new `OAUTH_RESOURCE` env var** in `example/server` — rejected in
  favour of deriving `Resource: cfg.BaseURL + "/mcp"` automatically,
  deliberately matching `smeldr.dev/mcp/transport.go`'s own
  `protectedResourceHandler` computation (`s.app.BaseURL() + "/mcp"`)
  byte-for-byte, so the two values can never drift apart. No user-facing
  configuration burden, no drift risk.

### Consequences

- **Breaking change**: `Config.Resource` required, `New` panics if empty.
  Every existing caller of `oauth.New` needs a `Resource` value added —
  including `smeldr/core`'s own `example/server/main.go` (see below).
- New exported fields: `Config.Resource`, `AuthCode.Resource`,
  `AccessToken.Resource`.
- New SQLite column (`resource`) on two tables, migrated automatically.
- `smeldr.dev/oauth` coverage: 73.4% → 75.4% (net positive from this
  change's own new tests, including `migrate_test.go` coverage for the new
  migration function — but the module's 96% gate remains unmet for reasons
  outside this task's scope; a pre-existing condition, not introduced or
  worsened here).
- `smeldr/core`'s `example/server/main.go` follow-through (adding
  `Resource: cfg.BaseURL + "/mcp"` to its `oauth.New` call) ships as part of
  this same T183 task but as its own commit in `smeldr/core` — no root core
  version bump, since no `smeldr` core-package file changed.
- `smeldr.dev/oauth` v0.3.0 → **v0.4.0** (breaking, but pre-1.0 so a MINOR
  bump per semver's 0.y.z carve-out — same precedent as v0.2.0's own
  breaking package rename).
- Docs updated: `smeldr.dev/oauth/README.md` (version line, Standards list,
  quick-start example, migration note), `CHANGELOG.md` ([0.4.0] entry),
  `common/agent/skills/smeldr.md` (oauth.Config example, Resource bullet,
  version-table bump — also corrected a stale core version shown there,
  v1.55.0 → v1.58.2, found while the file was open).
- Two unrelated, pre-existing doc-staleness items noticed in passing and
  spawned as separate follow-up tasks rather than folded into this
  amendment: `oauth/README.md`'s Storage table still shows pre-v0.3.0
  `forge_oauth_*` names; `smeldr.md`'s oauth section heading still reads
  "forge-oauth".
- Level 2 amendment (breaking change to an exported `Config` struct,
  consequences across 6 files in a standalone repo plus one core-repo file).

Level 2 amendment.

---

## A228 — `smeldr.dev/mcp`: correct `POST /mcp` doc-comment mislabeling (T183, standalone module)

### What

`transport.go`'s `Handler()`/`Register()` doc comments claimed `POST /mcp`
was a "2025-11-25 streamable HTTP" endpoint. Found false during T182's spec
investigation: `POST /mcp` is registered to the exact same handler function
(`s.messageHandler`) as `POST /mcp/message` — the 2024-11-05 SSE-transport
JSON-RPC relay. It is an alias kept for clients that expect the JSON-RPC
endpoint at `/mcp`, not a distinct implementation of the newer stateless
streamable-HTTP transport. Building real per-request distinguishing
behaviour (reading `_meta.protocolVersion`, honouring the new
`MCP-Protocol-Version`/`Mcp-Method`/`Mcp-Name` headers) would mean
implementing 2026-07-28's stateless transport rewrite — explicitly deferred,
per T182's own recommendation.

Doc comments corrected on `Handler()`, `Register()`, and `messageHandler`
itself (which didn't mention handling `POST /mcp` at all, only
`/mcp/message`). Also tightened the OAuth-authentication-boundary prose to
name `POST /mcp` alongside `/mcp/message` wherever the shared 401 check was
described, since it was previously omitted there too.

### Consequences

- Pure doc-comment change. No exported symbol, no route, no behaviour
  changed — same routes wired to the same handler as before.
- No version bump, no tag (`smeldr.dev/mcp`'s own tagging rule: "no
  version bump" = "no consumer-visible behaviour changed").
- Incidentally reproduced and confirmed (via `git stash`, comparing against
  `c5ecbaa`) a pre-existing, unrelated test failure
  (`TestStateTool_TransitionItem_InvalidTransition`) while verifying this
  change caused no regressions — spawned as a separate follow-up task
  (T184), not fixed here.
- Level 1 amendment (docs-only, no exported symbols, no cross-file
  consequences beyond the one file).

Level 1 amendment.

---

## A229 — Fix `PublishedAt` never stamped when created directly as `published` (T181)

### What

Neither `createHandler` (HTTP) nor `MCPCreate` (MCP) had any `PublishedAt`
stamping logic. Both already had status-aware branches for the "created
directly as Published" case (`createHandler`'s slug-collision check,
its `AfterPublish` hook fire), so the scenario wasn't unconsidered — the
timestamp was just never wired in. RSS `PubDate`/sitemap `LastMod` showed a
wrong (zero) date on public-facing surfaces for directly-published items.

**NEXT.md's suspected second half of the bug — checked directly, refuted,
not assumed.** The concern was that a follow-up `publish_post`/`MCPPublish`
call on the already-published item (as a manual workaround) would be
rejected by `validateTransition` on a `published→published` self-transition.
Reproduced directly against a real migrated SQLite DB: `validateTransition`
(`state.go`) has an explicit `if fromStatus == toStatus { return nil }` early
return, so the self-transition is never gated by the transition-edge lookup
at all. `MCPPublish` then unconditionally runs `setNodeTime(item,
"PublishedAt", time.Now().UTC())` on every successful call, regardless of
`prevStatus`. The follow-up call already self-heals today — no second bug
exists. Verified with a throwaway test, run, and reverted before writing the
plan (not committed).

### Fix

New helpers in `module.go`, alongside the existing `nodeStatusOf`/`nodeIDOf`
accessors and `applyDefaultStatus`:

```go
// nodePublishedAtOf returns the PublishedAt field of v via its Go field name.
func nodePublishedAtOf(v any) time.Time {
	rv := elemValue(v)
	path := goFieldPath(rv.Type(), "PublishedAt")
	if path == nil {
		return time.Time{}
	}
	return rv.FieldByIndex(path).Interface().(time.Time)
}

// stampPublishedAt sets PublishedAt to now when item's status is Published and
// PublishedAt has not already been set.
func stampPublishedAt(item any) {
	if nodeStatusOf(item) == Published && nodePublishedAtOf(item).IsZero() {
		setNodeTime(item, "PublishedAt", time.Now().UTC())
	}
}
```

Called as `stampPublishedAt(item)` immediately after `item := ptrToT[T](pv,
m.proto)` in both `createHandler` and `MCPCreate`, before `RunValidation` —
a validation failure never has visible side effects.

**Deliberately not reused at the 4 existing `PublishedAt`-stamping call
sites** (`updateHandler`, `MCPPublish`, `MCPSchedule`'s scheduler path,
`processScheduled`). Those already know they're handling a genuine state
*transition* into Published (each gated on its own `prevStatus`) and
unconditionally overwrite `PublishedAt` on every such transition, including
republish. Routing them through `stampPublishedAt`'s IsZero guard would
silently change that — a republish would stop updating `PublishedAt` if it
was already non-zero — a real regression, not a simplification.

### Consequences

- No exported Go symbols changed — both new functions are unexported.
- Consumer-observable behaviour change: directly-published items' API
  responses (HTTP and MCP) now include a non-zero `PublishedAt` where they
  previously didn't.
- New tests: `TestCreateHandler_publishedDirectlyStampsPublishedAt`,
  `TestCreateHandler_publishedWithExplicitPublishedAtPreserved`
  (`module_test.go`); `TestMCPCreate_publishedDirectlyStampsPublishedAt`,
  `TestMCPCreate_publishedWithExplicitPublishedAtPreserved` (`mcp_test.go`).
  Coverage: 96.1% (unchanged); `stampPublishedAt` 100%, `nodePublishedAtOf`
  80% (the `path == nil` defensive branch is structurally unreachable for
  any `Node`-embedding content type — every Smeldr content type embeds
  `Node`, which always has a `PublishedAt` field — same defensive
  convention as `setNodeTime`'s own identical check; not adding a
  dedicated test for a branch no real content type can trigger).
- Patch release, matching A221/A222/A225/A226's own precedent for real
  previously-broken-functionality fixes with no new exported symbols:
  v1.58.2 → v1.58.3.
- Level 1 amendment (isolated to `module.go`, no route/interface change,
  one new unexported helper pair + two one-line call sites).

Level 1 amendment.

---

## A230 — `smeldr.dev/mcp`: fix stale `published→draft` assertion in `TestStateTool_TransitionItem_InvalidTransition` (T184, standalone module)

### What

The test asserted `published → draft` is rejected by the default flow
("not in the default flow"). It isn't, hasn't been since core's Amendment
A217 (`d74cab7`, 2026-07-15, "enforce validateTransition in updateHandler +
published→draft") deliberately added `{"published", "draft"}` to the default
flow's seeded transitions (`migrate.go`) as an intentional "unpublish"
feature — confirmed via `git log -S` on the exact literal.

**Severity, checked rather than taken at face value.** `smeldr.dev/mcp`'s
own `go.mod` pins `smeldr.dev/core v1.54.0` (2026-07-05 — predates A217).
Ran the failing test two ways to isolate this: with the local `go.work`
(gitignored, dev-only convenience file pointing at core's live working
tree) it fails, because the workspace silently overrides `go.mod`'s pinned
version, building against current core (v1.58.2+, which has A217). With
`GOWORK=off` (forcing resolution from `go.sum`'s actual pinned `v1.54.0`)
it passes, because that version genuinely predates the addition. **Real CI
(fresh clone, no gitignored workspace) was green the whole time** — this
was a local-development-only false failure, not a live CI break, contrary
to how it was initially framed. Still worth fixing: the assertion is
factually stale regardless of which core version resolves it, and it will
become a genuine CI break the moment anyone bumps `smeldr.dev/mcp`'s core
dependency (a plausible near-future event, not hypothetical).

### Fix

Swapped the attempted invalid target from `"draft"` (now valid) to
`"scheduled"` — not in the default flow's transition list in either
version, and directionally safer than reaching for `archived` (one hop
closer to plausibly gaining its own outgoing transitions someday, e.g. a
hypothetical "restore from archive" feature). Added a comment flagging this
as a "currently invalid, not guaranteed forever" assumption, so the next
core amendment that touches transitions updates this test deliberately
instead of going stale again — the same failure mode this amendment itself
fixes.

### Flagged, not fixed here

`smeldr.dev/mcp/go.mod` pinning `smeldr.dev/core v1.54.0` while actual core
is at v1.58.3 is a real, growing gap — worth its own dedicated task to bump
and re-verify `smeldr.dev/mcp`'s full test suite against current core
(A217 through A230 span multiple behaviour changes never tested against
for real). Not attempted here — a dependency bump plus full regression pass
is a different shape of work than fixing one stale assertion.

### Consequences

- Test-only change, no exported symbol, no route, no behaviour changed.
- No version bump, no tag (matches A228's precedent: "no version bump" =
  "no consumer-visible behaviour changed").
- Level 1 amendment (single test file, no cross-file consequences).

Level 1 amendment.

---

## A231 — Thread `EdgeClass`/`Confidence` through `Reachability`'s output (T194)

### What

`ReachabilityItem` (`reachability.go:15-22`) was `{Type, ID}` only. The BFS
(`reachabilityNeighbors`) already read each `RelationEdge`'s full struct while
walking `GetBySource`/`GetByTarget`, then discarded everything but the
neighbor's type/id when building `reachabilityNode`, and `Reachability`'s main
loop discarded it a second time when building `ReachabilityItem`. The real,
structured asserted/observed/inferred distinction (T193/A232) existed in the
database but never survived into anything that reads `Reachability`'s output.

**Checked, not assumed: no `smeldr.dev/mcp` `Reachability` exposure exists at
all.** Grepped the whole mcp repo for `reachab`/`Reachability`, case-
insensitive — zero hits. T193's own NEXT.md estimated "~1-2 days across both
repos" for this task; there is no second repo to touch. This is core-only.

### The real gap this surfaced

Adding two fields looked like a small change until accounting for what they
mean: a node can be reached by **multiple edges at the same hop distance**
(e.g. two different relation kinds both linking the same source to the same
target). The BFS's `seenNodes` dedup previously kept whichever edge was
processed first — order depended on `frontier`'s slice order and each
`GetBySource`/`GetByTarget` call's row order, neither guaranteed stable. That
was invisible while nothing downstream read per-edge metadata. It stops being
invisible the moment `EdgeClass`/`Confidence` ride along, since now *which*
edge "won" the tie determines what a caller sees — live, previously-silent
nondeterminism.

### Fix

New unexported `reachabilityCandidate` (pairs a discovered node with the
edge's `EdgeClass`/`Confidence`), `edgeClassRank` (asserted=2, observed=1,
inferred/unknown=0), and `betterCandidate` (higher rank wins; within the same
rank, higher `Confidence` wins, nil treated as lowest). `reachabilityNeighbors`
returns `[]reachabilityCandidate` instead of bare `[]reachabilityNode`.
`Reachability`'s per-depth loop now collects all of a depth's candidate edges
per new node into a map before finalizing the ring, applying `betterCandidate`
on each new candidate, rather than committing on first discovery. Existing
behaviour (one node per ring, shortest-distance-wins, ring order not
guaranteed — confirmed via `reachability_test.go`'s own `ringKeys()` helper,
which sorts before comparing) is unaffected; only which edge's metadata gets
attached when there's a genuine choice.

### Consequences

- `ReachabilityItem` gains `EdgeClass string`/`Confidence *float64` —
  additive, backward-compatible JSON (`confidence` uses `omitempty`,
  matching `RelationEdge`'s own convention).
- New tests: `TestReachability_ItemsCarryEdgeClassAndConfidence` (base case),
  `TestReachability_TieBreak_PrefersAssertedOverInferred`,
  `TestReachability_TieBreak_PrefersObservedOverInferred`,
  `TestReachability_TieBreak_PrefersHigherConfidenceWithinSameClass`,
  `TestReachability_TieBreak_NilConfidenceTreatedAsLowest`. All existing
  `TestReachability_*` tests needed no changes (they compare via `ringKeys()`,
  type:id only).
- No exported symbols removed. Coverage: 96.1%; `edgeClassRank`/`Reachability`/
  `reachabilityNeighbors` 100%, `betterCandidate` 87.5% (the uncovered branch
  is an order-dependent tie already exercised symmetrically by the confidence
  tests above — not a defensive-dead-code case, just a small residual gap
  below the function's full branch count; total coverage already clears the
  96% gate).
- Patch release (consumer-observable: new JSON fields on `Reachability`'s
  existing response shape). Bundled with A232 in the same commit —
  v1.58.3 → v1.58.4.
- Level 2 amendment (exported struct fields added, real behaviour addition to
  an existing method's output).

Level 2 amendment.

---

## A232 — Implement `EdgeClass`'s `"observed"` value (T196)

### What

Per T193's own resolved recommendation (`design/edge-class-observed-spike.md`)
— both judgment calls already confirmed by the architect, not re-litigated
here.

New `MCPObserveRelation` (`relations.go`), mirroring `MCPAssertRelation`/
`MCPProposeRelation` exactly in shape, storing `edge_class="observed"`:
records an edge a system directly witnessed (e.g. via an inbound integration),
as opposed to a human's direct claim or an agent's inference.

`smeldr.dev/mcp` gains a matching `observe_relation` tool (`relation_tools.go`),
mirroring `propose_relation`'s tool definition and dispatch case, Author role
(same as its siblings). `get_relations`' `edge_class` filter enum gains
`"observed"` — otherwise the new value would be unfilterable via the one tool
built specifically to filter by edge class. Doc comments updated from "six"
to "seven" tool definitions/relation management tools throughout
`relation_tools.go`.

### Deliberately not changed — confirmed no-ops, not oversights

- `governance.go`'s dynamic-scope check stays `edge_class='asserted'`-only —
  a system-witnessed fact is not automatically the same trust tier as a
  deliberate human grant, per the spike's own resolved reasoning.
- `RecomputeAsserted`/`BulkRecompute`'s diff scope stays `asserted`-only —
  observed facts are not subject to Layer 1's reference-field-driven
  reconciliation.
- No new `RelationKindDef.Mode` value — kinds already in `asserted` mode
  permit both `asserted` and `observed` edges (both are non-probabilistic
  facts, just different origins).

### Consequences

- New exported method: `RelationStore.MCPObserveRelation`. No exported
  symbols removed or changed elsewhere.
- `smeldr.dev/mcp`: new `observe_relation` tool — consumer-observable, its
  own patch version bump/tag in that repo (unlike A228's doc-only case, this
  is a real new capability).
- New tests: `TestMCPObserveRelation_OK`, `_UnknownKind`, `_StoresObserved`,
  `_WithConfidenceAndAttributes` (`relations_mcp_test.go`, core);
  `TestRelationTools_ObserveRelation`, `_MissingArgs`, `_UnknownKind`
  (`relation_tools_test.go`, mcp), plus extended
  `TestRelationTools_GetRelations_EdgeClassFilter` to prove the new enum
  value filters correctly, and updated the three tests that hardcode the
  tool-name list (`_PresentWithStore`, `_IsRelationTool`,
  `_RelationToolDefs_AllPresent`) to include `observe_relation`.
- Coverage: 96.1% (core, unchanged), 96.4% (mcp).
- Patch release (consumer-observable: new write path). Bundled with A231 in
  the same core commit — v1.58.3 → v1.58.4.
- Level 2 amendment (new exported method, real new write path, cross-repo
  consequence).

Level 2 amendment.

---

## A233 — Fix `RoleStore.Grant`/`Revoke`'s non-atomic audit-write gap (T200)

### What

`Grant` and `Revoke` (`governance.go`) each ran their state-changing SQL
(`INSERT`/`DELETE`) and their `GovernanceAuditStore.Append` call as two
separate, non-transactional writes. If the audit write failed after the
state change had already succeeded, the function returned an error — but
the grant/revoke had already taken effect. A caller reading "Revoke failed"
would reasonably assume the grant still exists; it didn't. Found during
T199's spike, independently verified by the architect against source
(`governance.go:768-786` at the time).

`design/governance-model.md` §9's own Article I commitment states the audit
trail on `Grant`/`Revoke` exists specifically because "changing them in
place with no history violates... 'authority never changes silently.'"
This failure mode was exactly that — a silent authority change, the precise
thing the mechanism was built to prevent.

### Checked before implementing, not assumed

NEXT.md's own suggested fix was to mirror `relations.go`'s `applyRelationDiff`
— a `txBeginner` interface-check that begins a transaction on `s.db` when
supported. Read `GovernanceAuditStore`'s actual definition before applying
this mechanically: it is an **exported, pluggable interface**
("Applications may supply their own implementation, e.g. writing to a
remote log service"). The bundled `sqlGovernanceAuditStore` holds its own
separately-bound `db DB` field, set once at `NewGovernanceAuditStore(db)`
construction — entirely independent of `RoleStore.db`. `applyRelationDiff`'s
pattern works because *all* its writes go through the same `s.db`; here, a
transaction on `RoleStore.db` alone would not pull `auditStore.Append`'s
write into it — `Append` still calls `ExecContext` on its own stored
reference, oblivious to any transaction the caller happens to be running.
A mechanical copy of the suggested pattern would have compiled, looked
right, and not actually fixed the bug. The architect independently verified
this exact claim against source before approving the plan.

True cross-object atomicity also isn't universally guaranteeable — a custom
`GovernanceAuditStore` writing to a remote log service has no shared
transaction with a local SQL `DB` to join, by construction. The realistic,
implemented goal: make the common case (the bundled SQL-backed store,
sharing a transactional DB) genuinely atomic, and leave custom
implementations exactly as robust as they were before — not silently
claimed improved.

### Fix

New unexported optional interface, alongside `GovernanceAuditStore`:

```go
type txGovernanceAuditAppender interface {
	appendTx(ctx context.Context, exec DB, r GovernanceAuditRecord) error
}
```

`sqlGovernanceAuditStore` implements it; both `Append` and `appendTx` now
delegate to one shared `appendAuditRecord(ctx, exec DB, r) error` helper — no
duplicated SQL, no risk of the two paths drifting apart.

`Grant`/`Revoke` each type-assert `s.auditStore.(txGovernanceAuditAppender)`.
When it succeeds *and* `s.db` satisfies the existing `txBeginner` interface,
a transaction is opened; the state-changing write, any ID-resolution reads,
and the audit append all go through that transaction (`exec`), and a
deferred `tx.Rollback()` (matching `applyRelationDiff`'s own established
idiom) undoes everything if any step — including the audit append itself —
fails. When either condition doesn't hold (a custom, non-SQL audit store, or
a `DB` without transaction support), both functions fall back to the exact
sequential behaviour they had before: still fail-closed (an audit failure
still returns an error), just without the rollback guarantee — an honest,
documented limitation, not a silently-claimed fix that doesn't apply.

**Incidental second bug closed as a side effect, not the primary target:**
`Grant`'s existing shape is insert-then-resolve-the-real-ID-via-SELECT (to
handle its own idempotent upsert case). Before this fix, if that resolve
`SELECT` failed after a successful `INSERT`, the grant was permanently
orphaned in the database with no way to learn its ID. The same transaction
wrap rolls that back too, for free.

`DefineRole` has the identical two-separate-writes shape (and the same
`failAppendAuditStore`-covered test) but is explicitly out of NEXT.md's
scope ("Fix `RoleStore.Revoke`/`Grant`'s..."). Not touched — flagged as a
same-shape follow-up candidate.

### Test changes

Existing tests using `failAppendAuditStore` (`TestWithAudit_Grant_AppendError`,
`TestWithAudit_Revoke_AppendError`) needed **no changes** — that fixture only
implements `Append`, not `appendTx`, so the interface check fails for it and
these tests correctly keep exercising the (unchanged) fallback path, still
expecting an error with no rollback guarantee. Verified this precisely
rather than assumed: `setupGovernanceDB` returns a real `*sql.DB` (which does
have a real `BeginTx`), so it's specifically the audit-store type — not the
DB — that determines which path a given test exercises.

New tests use a **real SQL failure, not a mock** — a genuine
`sqlGovernanceAuditStore` pointed at a database where
`CreateGovernanceAuditTable` was deliberately never called, so its own
`INSERT INTO smeldr_governance_audit` genuinely fails with "no such table"
— mirroring `govQueryRowFailDB`'s existing route-to-nonexistent-target
trick, rather than inventing a new mocking approach:

- `TestGrant_AuditAppendFailure_RollsBackInsert` / `TestRevoke_AuditAppendFailure_RollsBackDelete`
  — prove the actual fix: after a failed `Grant`/`Revoke`, the grant table's
  row count (and, for Revoke, the specific grant's continued existence) is
  unchanged from before the call.
- `TestGrant_BeginTxError` / `TestRevoke_BeginTxError` — a small `txBeginner`-
  satisfying wrapper whose `BeginTx` always errors (same wrap-and-fail shape
  as `govQueryRowFailDB`), proving the error propagates and nothing changes
  when a transaction can't even be opened.

**One accepted, named coverage gap:** `commit()` failing after an
already-successful sequence of statement executions within the same open
transaction is a driver/connection-level failure mode with no deterministic
way to trigger against SQLite in-memory without a custom driver mock, which
this codebase doesn't have. Not silently left uncovered — named here and in
the code's own test-plan reasoning, matching this session's established
precedent (e.g. A229's `nodePublishedAtOf` gap) for defensive branches that
aren't realistically reachable in tests.

### Consequences

- No exported Go symbols changed — `txGovernanceAuditAppender`,
  `appendAuditRecord`, and `appendTx` are all unexported.
- Real behaviour fix: `Grant`/`Revoke` (and therefore `smeldr.dev/mcp`'s
  `grant_role`/`revoke_grant` tools) no longer silently mutate governance
  state while reporting failure, in the common (bundled audit store,
  transactional DB) case.
- Closes A190's own documented "non-atomic — mutation may have already
  taken effect" caveat for that case; the caveat still applies, honestly,
  for custom non-SQL `GovernanceAuditStore` implementations.
- New tests: `TestGrant_AuditAppendFailure_RollsBackInsert`,
  `TestRevoke_AuditAppendFailure_RollsBackDelete`, `TestGrant_BeginTxError`,
  `TestRevoke_BeginTxError`. Coverage: 96.1% (unchanged); `Grant` 96.3%,
  `Revoke` 95.0% (the named `commit()` gap), `appendAuditRecord`/`appendTx`
  100%.
- Patch release, matching precedent for real behaviour fixes with no new
  exported symbols: v1.58.4 → v1.58.5.
- Level 2 amendment (real behaviour fix to a governance-critical, MCP-exposed
  operation, matching A226's own precedent for classifying this shape of fix
  Level 2 despite no exported-symbol change).

Level 2 amendment.

---

## D33 — Self-hosting roadmap: pipeline proven before vision demonstrated, milestone-phased

### Scope

cross-cutting

### Decision

The migration of the architect/implementer coordination process onto a live
Smeldr instance ships in six sequenced milestones (M0–M5, full detail:
`smeldr/architect/design/self-hosting-roadmap.md`), not as one undertaking.
M0 proves the dispatch pipeline (Goal/Task/Signal, live for all five agents)
without authority enforcement, lineage tracing, headless automation, or
historical migration — deliberately. M1 (authority) and M2 (lineage) are the
critical path to M5 (supersede-triggered re-evaluation), the capstone that
actually demonstrates decisions building on decisions. M3 (headless
automation) and M4 (historical migration) run independently, off that
critical path. M0's own completion is named "the pipeline working," never
"the vision demonstrated" — the two claims are never conflated in any report
about this work.

### Alternatives considered and rejected

- **Building everything simultaneously ("all in").** Rejected on two
  grounds: it multiplies fault surface (a broken first pilot Task would be
  indistinguishable from a broken headless listener if both are new at
  once), and two of the deferred pieces (the supersede-trigger's
  job-granularity and loop-prevention questions; headless automation's
  worktree-isolation mechanics) have no finished design yet — building them
  is not actually available regardless of ambition, only designing them is.
- **Treating M0's own "successful turn" as sufficient proof of the
  self-hosting vision.** Rejected explicitly — a Task moving through its
  lifecycle with no Decision, no authority check, and no lineage in play
  proves the plumbing, not the promise that decisions build on decisions,
  visibly and traceably.

### Consequences

Future dispatch of self-hosting work is scoped per milestone, not as one
large task. Any report of M0's completion must state plainly that
authority/lineage/headless/migration remain unbuilt.

Status: Ratified 2026-08-07.

---

## D34 — Decision authority: role-to-transition mapping, not a rank axis; epistemic origin stays separate from authority

### Scope

core

### Decision

Authority over who may ratify or supersede a `Decision` is expressed through
the role/transition mechanism already shipped in core (`RoleStore` grants +
`required_role` on a state-flow transition) — not a new rank integer on
`Decision`, and not by repurposing `RelationEdge.EdgeClass`
(asserted/inferred/observed). `EdgeClass` stays pure epistemic origin (how an
edge came to exist); authority (who may bind it) is a separate axis,
expressed through which role is required on the ratify/supersede transition
and which actors hold that role. An unratified agent proposal is
distinguished from a ratified one via `Decision`'s own `proposed → ratified`
lifecycle state, not a new field.

For `Decision`-class-specific authority (e.g. an implementation-scoped
decision needing a different ratifying role than a cross-cutting one), the
mechanism is a thin, `Decision`-specific authorization wrapper that maps
`Decision.Scope` to a required role name in application code, then grants
that role with `ScopeGlobal` — not `RoleGranted`'s `ScopeDynamic` path, which
would require a pre-existing asserted relation edge from every `Decision` to
a scope-anchor before it could be ratified, an ordering dependency not worth
taking on for this case.

### Alternatives considered and rejected

- **A rank integer on `Decision`** (e.g. Peter=100, architect=80,
  implementer=40, supersede-if-rank≥target). Rejected: duplicates state
  already living in role/grant records (a rank change after the fact would
  desync from history), and forces a total order onto what is really a
  partial order per operation.
- **Reusing `EdgeClass` as a proxy for authority.** Rejected: conflates
  epistemic origin with binding authority — an agent can deliberately
  assert a low-authority `Decision` (asserted-but-overridable is a normal,
  real state), which the `EdgeClass` framing would misrepresent as
  "inferred."
- **Routing `Decision`-class-specific authority through `RoleGranted`'s
  `ScopeDynamic`** (relation-edge-based scoping) as the default. Rejected:
  requires every `Decision` to carry an asserted edge to its scope-anchor
  before ratification can even be attempted, an ordering dependency with no
  clear owner. Left available for a narrower future case, not the default.

### Consequences

`smeldr_transitions.required_role`/`required_reason` on `Decision`'s own
`proposed → ratified` and `ratified → superseded` transitions is real, scoped
follow-up work — currently unset, verified directly against
`orchDecisionFlow()`. A fail-open gap in `validateTransition` (three
branches: query-error, `RoleStore` not wired, no actor in context) needs a
paired fix (a new `strict` column plus a resolved posture on the query-error
branch) before this authority model is actually enforced, not just designed.
Full detail: `smeldr/architect/design/decision-authority-and-lineage.md`.

Status: Ratified 2026-08-07.

---

## D35 — Decision lineage: bounded, cycle-safe query-time traversal, not a maintained transitive closure or a new relation kind

### Scope

core

### Decision

Tracing what a `Decision`'s premise ultimately rests on (walking
`depends_on`/`derives_from`/`supersedes` edges upstream, potentially through
superseded history) is implemented as a read-time traversal
(`trace_lineage(item_id)`), computed on demand — not as a relation kind that
"carries" transitivity, and not as an eagerly-maintained transitive-closure
table. Three guards are required, not optional: a visited-set for cycle
detection; an explicit "truncated" signal when a depth limit is hit, never a
silent cutoff; and a decided, non-default behaviour at an invalidated edge —
the traversal follows it rather than stopping, and flags it as invalidated
in the returned trace, since the invalidated edge is usually where the
actual answer lives.

### Alternatives considered and rejected

- **A maintained transitive-closure table**, updated reactively on every
  edge write. Rejected: reintroduces the exact cascade-amplification risk
  `AfterRelationCascade`'s own depth=1 limit was built to avoid, for a
  question (lineage) that is asked rarely, at query time, not on every
  write — the wrong side of the write/read cost asymmetry.
- **A new relation kind meant to "carry" transitive lineage directly.**
  Rejected: edges don't carry transitivity, traversal computes it; a
  relation kind can't substitute for a real bounded walk.

### Consequences

`trace_lineage` is real, unbuilt work, reusing `ContentEdgeStore`'s existing
batched `IN (...)` read pattern (`edges.go`) as its technique, not its code
(that store serves a different type, `ContentEdge`, not `RelationEdge`). Not
yet decided, flagged as its own open question: whether a trace crossing into
a superseded `Decision`'s own `supersedes` chain should continue to the
replacement or stop — a distinct decision from "follow invalidated edges,"
not implied by it. Full detail:
`smeldr/architect/design/decision-authority-and-lineage.md`.

Status: Ratified 2026-08-07.

---

## D36 — Relation kinds for the orchestration graph: derives_from, depends_on, ships_as, supersedes

### Scope

core

### Decision

Four relation kinds are registered for the five orchestration types (Goal,
Task, Decision, Amendment, Signal), all `Mode=asserted`, `Directional=true`,
`Weighted=false`:

- **`derives_from`** (Task → Goal) — load-bearing: a Goal's closure is
  computed by confirming every derived Task is `done`.
- **`depends_on`** (Task → Task) — the edge an implementer checks (via
  `get_goal_context`) before proceeding past `active`; direction is
  dependent-points-at-dependency, so `GetByTarget` correctly finds
  dependents if cascade notification is ever wired to this relation kind.
- **`ships_as`** (Task → Amendment) — the only link back to the Task an
  Amendment shipped for; `Amendment` carries no `TaskRef`-equivalent field
  of its own, verified directly against `orchestration.go`.
- **`supersedes`** (Decision → Decision) — complements `Decision`'s own
  `superseded` state with *which* decision did the superseding; the state
  alone can't carry that.

`implements` (Amendment → Decision) remains an open, unconfirmed candidate —
named once, in passing, never used in a worked example. Not registered by
this decision.

`Signal` is explicitly excluded from the relation graph — its own `TaskRef`
field already covers its link to `Task`; a relation kind would be redundant.

### Alternatives considered and rejected

- **Registering `implements` alongside the other four.** Rejected for now:
  no real worked example has ever needed it; registering an unused kind
  adds surface area without a concrete use to validate it against.

### Consequences

Registration (`upsert_relation_kind`, four calls) is real, scoped,
currently-unbuilt setup work — a prerequisite for any `depends_on`/
`derives_from`/`ships_as`/`supersedes` edge being assertable at all. Full
detail: `smeldr/architect/design/process-types-and-workflow.md` §1,
`decision-authority-and-lineage.md` §11.

Status: Ratified 2026-08-07.

---

## D37 — Architect may propose, register, and commit new Decision entries directly to smeldr/core

### Scope

cross-cutting

### Decision

Architect may write new Decision entries directly to `decisions/recent.md`
and `DECISIONS.md`'s index table, using local file tools, and commit and
push that change directly to `smeldr/core` — a narrow, explicit exception to
the standing rule that architect never runs git write actions on pilot
repos. Peter reviews and ratifies before each commit. D-number assignment
follows the same discipline as A-number assignment: read `DECISIONS.md`'s
actual index table directly, the correct next number is the last row + 1,
never pre-assigned or guessed. The `decisions/recent.md` archiving mechanic
(moving old entries to a phase-archive file once the ~20KB threshold is hit)
stays core-implementer's job, not delegated further, per the documented
2026-07-30 incident where a less-careful actor corrupted that exact
line-surgery mechanic.

### Alternatives considered and rejected

- **Keeping the old rule** (all `decisions/` writes route via `NEXT.md` to
  core-implementer). Reconsidered because its actual justification was a
  remote-push tooling limitation (`push_files` truncation on large files),
  not an authority principle — and because it created exactly the failure
  mode this investigation surfaced: real decisions (D33–D36 above)
  accumulating in architect's own `design/` documents without ever being
  registered, dependent on someone remembering to formalize them later.
  Found directly: D-numbering had gone dormant since D32 (2026-05-17), with
  genuinely decision-shaped content flowing through Amendment numbering
  instead for nearly three months.

### Consequences

`CLAUDE.md` (`smeldr/architect`) updated 2026-08-07 with the full rule,
including the git-commit boundary and the D-number-assignment discipline.
Does not extend to any other pilot-repo git write action (merges, tags,
releases, PRs, any file outside `decisions/`/`DECISIONS.md`) — those stay
exclusively Peter's or the relevant implementer's.

Status: Ratified 2026-08-07.

---

## A234 — Decision authority enforcement: strict transitions + Decision-specific role gate (T206)

### What

Closes D34's authorization gap across four pieces in `smeldr/core`:

1. New `Transition.Strict bool` field in `state.go`, stored as `smeldr_transitions.strict` column (additive). New idempotent migration in `migrate.go`, structurally identical to the existing `migrateTransitionReasonColumn`.

2. `validateTransition` now fail-closed on strict transitions: when `Strict: true`, a nil `RoleStore` or empty `actorID` returns `ErrForbidden` instead of silently allowing. When `Strict: false` (every pre-existing transition), both branches are unchanged. Separately and unconditionally, `smeldr_transitions` row lookup errors (not `sql.ErrNoRows`) now return `ErrInternal` instead of nil — prevents a strict transition from silently passing under transient DB errors.

3. `orchDecisionFlow()` in `orchestration.go`: the two governance transitions (`proposed → ratified` and `ratified → superseded`) now carry `RequiredRole: "admin"` and `Strict: true`. Previously bare `{From, To}` pairs with no role or strictness.

4. New unexported `decisionScopeRoles map[string]string` (starts empty) and `authorizeDecisionScope` function in `orchestration.go`. Reads a `Decision`'s `Scope` field, maps it to a required role via `decisionScopeRoles`, and checks via `RoleStore.RoleGranted` with zero-value `AuthTarget` (only matches `ScopeGlobal` grants, not `ScopeDynamic`). Wired into `updateHandler` immediately after `validateTransition`, on status-change PUT requests.

### Implementation detail caught in testing

`authorizeDecisionScope` reads the existing item's `Scope` (pre-decode), not the newly-decoded item, to prevent scope-zeroing attacks: if a PUT request omits `scope` from its JSON body, a decode-time check would zero that field before checking authorization, silently bypassing the gate. Worse, a caller could pair a status change with a favorable scope in the same request. Test `TestModule_updateHandler_decisionRatify_scopeForbidden` explicitly verified this: it asserts the check still rejects an unauthorized actor even when the request body contains no scope field. This caught and prevented the bug before ship.

### Why strict defaults to false; why row-lookup error fix is unconditional

Existing transitions (thousands in the wild) ship with `Strict: false`, preserving their prior behaviour. When `strict: true`, two previously-unconditional allow-paths now reject; when false, they are byte-for-byte unchanged.

The row-lookup error fix is unconditional because shipping `Strict: true` without it would not actually close the authority gap: a transient DB error (e.g. SQLITE_BUSY) would still silently allow the transition before the strictness check ran. This is a real bug independent of the Strict feature.

### Known gaps — out of scope

- `decisionScopeRoles` ships empty: no scope-to-role policy has been decided yet (D34 is explicit this is a separate, still-open question). This layer is real and tested but currently a no-op. Plain `RequiredRole: "admin"` (piece 3) is what enforces anything today.
- `RequiredReason` was deliberately left unset on both transitions: `updateHandler` hardcodes reason to empty string with no passthrough. Setting `RequiredReason: true` would make both transitions permanently unreachable through the only path that reaches them — a self-defeating trap. Flagged as a separate follow-up (updateHandler has no reason-passthrough for any content type, not just Decision).
- No MCP tool can currently move a `Decision` to `ratified` or `superseded`: `transition_item` only operates on dynamic content; `MCPUpdate` explicitly discards caller-supplied status changes. Only direct HTTP `PUT /decisions/{slug}` reaches this code. Separate named gap.

### Consequences

19 new tests (4 `migrateTransitionStrictColumn`, 4 `validateTransition` strict-branch, 1 `RegisterFlow` strict-persistence, 7 `authorizeDecisionScope`, 3 `updateHandler` e2e) plus 2 existing tests updated (`TestValidateTransition_transitionQueryError`'s assertion for the fail-closed `[B]` change; `TestDecisionFlow_definition` extended to assert the new `RequiredRole`/`Strict` fields). Overall coverage 96.1%. Branch coverage: 100% on `validateTransition`, `authorizeDecisionScope`, `orchDecisionFlow`; 89.5% on `migrateTransitionStrictColumn` (one defensive rows.Scan error-continue branch, matching precedented `migrateTransitionReasonColumn` shape). Patch version bump v1.58.5 → v1.58.6: a `PUT /decisions/{slug}` status change to `ratified` or `superseded` now requires the `admin` role where it was previously unenforced (a request that used to succeed can now return 403). No exported symbols removed; `Transition.Strict` is additive. Level 2 amendment.

Status: Shipped 2026-08-07.

---

## A235 — Build `trace_lineage`: bounded, cycle-safe upstream lineage traversal (D35, M2)

### What

Implements D35 in full: `RelationStore.TraceLineage(ctx, anchorType, anchorID
string, maxDepth int) (*LineageTrace, error)` (`lineage.go`, new) — a
bounded, read-time upstream traversal over `depends_on`/`derives_from`
edges, reporting every item found as a `LineageNode` (`Type`, `ID`, `Depth`,
`RelationKind`, `EdgeClass`, `Confidence`, `Invalidated`, `Superseded`).

Reuses `reachability.go`'s (A219/A231) established BFS shape directly —
`reachabilityNode`, `edgeClassRank`, `betterCandidate` — rather than
`edges.go`'s `ChildrenOf`, the reuse target NEXT.md/D35 originally named:
`ChildrenOf` is a flat, single-level batched fetch over a structurally
unrelated type (`ContentEdge`), with no BFS, no visited-set, no depth
tracking, while `reachability.go` already solves guard 1 (cycle detection
via a visited-set) in this exact package, over this exact `RelationEdge`
type. What NEXT.md's pointer got right is narrower than "the technique":
one batched `IN (...)` query per BFS level instead of one query per
frontier node — which `reachability.go` itself does not do
(`reachabilityNeighbors` queries per node). New `batchEdgesBySource`/
`batchEdgesByTarget`/`batchEdgesByTypeAndIDs` (unexported) apply that
batching, grouped by node type per query rather than a composite
`(type, id)` row-value `IN (...)`, since row-value support is not verified
portable across this package's SQLite and pgx backends. Flagged and
approved by the architect before implementation
(`plans/core-m2-next-plan.md` §2).

Three guards, per D35:

1. Cycle detection via a shared visited-set across the whole traversal.
2. `LineageTrace.Truncated` — an explicit signal when the walk still had
   unexplored frontier at `maxDepth`, computed via a one-time,
   non-recording peek (`hasFurtherLineage`) rather than assumed from
   "frontier non-empty at the boundary" (a false-positive bug caught during
   implementation — see below).
3. Invalidated edges (`InvalidAt` set) are followed, not stopped at —
   flagged via `LineageNode.Invalidated`.

Resolves D35's own open question (does a trace crossing into a superseded
`Decision` continue to the replacement, or stop): **follows it**, argued
independently in the plan (`core-m2-next-plan.md` §3, approved) on the same
reasoning D35 guard 3 already accepted for invalidated edges — stopping
would hide exactly what the query was asked to find. A superseded node's
replacement is recorded at the *same* `Depth` as the superseded node (a
lateral step, not an extra upstream hop) — a node's supersede history is
revision metadata about its identity, not a new premise in the reasoning
chain.

### Two real bugs found and fixed during implementation, not part of the approved plan's original design

1. **`Truncated` false positive.** The initial design set
   `Truncated = true` whenever the frontier was non-empty at `maxDepth` —
   but a non-empty frontier only means nodes were found, not that *they*
   have further edges. A test with two leaf nodes found at `maxDepth=1`,
   neither with further edges, caught this returning `Truncated=true` when
   the graph was actually fully exhausted. Fixed with `hasFurtherLineage`:
   a one-time peek query at the depth boundary, checking whether the final
   frontier has *any* unvisited depends_on/derives_from target or incoming
   supersedes edge, without recording anything into the trace.
2. **Supersede chains only resolved one hop.** The initial
   `followSupersedes` found a node's *immediate* replacement but never
   checked whether that replacement had itself since been superseded — a
   Decision revised twice (X → X2 → X3) would trace to X2, not the actual
   current X3, directly contradicting the plan's own stated purpose ("the
   agent wants the current replacement"). Fixed: `followSupersedes` now
   loops, chasing each newly-found replacement for further supersession
   until none remain. This removed the only depth-bound that would
   otherwise have applied to that walk (chain length is not counted
   against `maxDepth`, by design — see above) — so a pathologically long
   revision history could otherwise run the loop unbounded. Capped at
   `MaxLineageDepth` hops, reusing the same ceiling as the main walk;
   hitting the cap sets `Truncated`, the same signal used for the
   hop-depth limit. `TestTraceLineage_MultiHopSupersedeChain` and
   `TestTraceLineage_SupersedeChainCappedAtMaxLineageDepth` prove the fix
   and the cap respectively.

Both were caught by coverage-driven testing during implementation, fixed in
this same commit, and reviewed as part of the diff — not re-litigated as a
new planning round (matches A231's own precedent for a mid-implementation
tie-break discovery).

### Deliberately out of scope, per the approved plan

- No anchor-existence check — matches `Reachability`'s own precedent
  (reports graph structure only), and confirmed load-bearing beyond
  consistency: `resolveItemTable` (the only existing type-to-table lookup)
  is SQLite-only (queries `sqlite_master`), so using it here would be a
  real portability bug against pgx, not just an unnecessary coupling
  (architect's own finding, `core-m2-next-plan.md` §7).
- No MCP tool — `smeldr.dev/mcp` is a separate repo outside this
  task/worktree; `trace_lineage` ships core-only, matching `Reachability`'s
  own T194/T196 split (core first, MCP wiring dispatched separately, only
  when asked for).
- No `state.go`/`orchestration.go` changes — the M1 collision risk NEXT.md
  named does not materialize; `lineage.go` is genuinely new, standalone.

### Consequences

- New file `lineage.go`: exported `LineageNode`, `LineageTrace` types;
  `MaxLineageDepth = 10` constant; `RelationStore.TraceLineage` method. No
  exported symbols changed elsewhere.
- 20 new tests (`lineage_test.go`), 100% coverage on every new function
  (`TraceLineage`, `hasFurtherLineage`, `followSupersedes`,
  `batchEdgesBySource`, `batchEdgesByTarget`, `batchEdgesByTypeAndIDs`).
- Package coverage: 96.2%.
- Level 2 amendment (new exported symbols, new file, real new read path).

Level 2 amendment.

