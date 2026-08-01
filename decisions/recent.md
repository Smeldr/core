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
