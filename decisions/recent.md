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

---

## A225 — Fix `MCPCreate`'s hardcoded `Draft` default breaking all 5 orchestration types (T180)

### What

`module.go`'s `MCPCreate` hardcoded a caller-omitted status to the literal
`Draft` constant, regardless of whether the content type has a custom
`StateFlow` registered whose actual `IsInitial` state is something else.
`validateInitialState` (A216, T148) then correctly rejected `"draft"` as
not a registered state for that flow, producing a hard `-32001 Conflict`.
This broke `create_signal`/`create_task`/`create_decision`/
`create_amendment`/`create_goal` via MCP whenever the caller omitted status
— all five orchestration types registered by `RegisterOrchestrationTypes`
have a non-`"draft"` `IsInitial` state (`"pending"`/`"backlog"`/
`"proposed"`/`"scoped"`/`"open"` respectively).

**The equivalent HTTP path was checked directly, per instruction, rather
than assumed to have "the same hardcoding" — it doesn't.** `createHandler`
has no status-defaulting logic at all: it decodes the request body into a
zero-valued struct, and if `status` is absent, the field stays at `Status`'s
Go zero value (`""`), not `"draft"` (`Draft Status = "draft"` — not the
same value). `validateInitialState` is only called `if s != ""`, so an
omitted status skips validation entirely and persists the literal empty
string. Confirmed empirically with a throwaway test. This is a **second,
silent** bug, not a copy of MCPCreate's, and not limited to orchestration
types — it affects any content type created via raw HTTP POST with no
status field. For an orchestration type specifically it is arguably worse
than MCPCreate's loud failure: `""` is not a valid state or a valid
transition `from_state` in that type's own flow, so the item is created
into a permanently stuck status with no error at all. No existing test
checked the resulting `Status` of a plain create with omitted status
(`TestModuleCreateSuccess` asserts ID/Slug/repo-count, never `Status`) —
this gap had never been exercised.

A216's own archived entry (`decisions/phase17-archive.md`) confirms this is
adjacent to, not a regression of, its own scope: A216 added
`validateInitialState` to reject an *explicitly supplied* invalid status
(`"done"`); it never addressed an *omitted* one.

### Fix

New `defaultInitialState(ctx context.Context, db DB, typeName string) string`
(`state.go`, co-located with `validateInitialState`/`suppressesSignals`):
queries `smeldr_state_flows`/`smeldr_states` for the type's own registered
`IsInitial` state, fail-open on nil DB, non-SQLite, missing flow, no
`IsInitial` state, or query error. Deliberately does **not** fall back to
the default flow the way `validateInitialState`/`suppressesSignals` do —
the built-in default flow's own seeded initial state is always `"draft"`
(`migrateStateFlows`), so a second query against it would return the same
answer for no benefit.

New `applyDefaultStatus(ctx context.Context, db DB, typeName string, pv reflect.Value, f nodeFields)`
(`module.go`, co-located with its two callers): sets `pv`'s status field to
`defaultInitialState`'s result when the caller left it empty, falling back
to the literal `Draft` constant when no custom flow is registered. Shared
by `createHandler` and `MCPCreate`, replacing `MCPCreate`'s old hardcoded
block and adding genuine defaulting to `createHandler` for the first time —
closing both gaps with one mechanism rather than porting one path's broken
logic to the other. `createHandler`'s existing
`if s := string(nodeStatusOf(item)); s != "" { validateInitialState(...) }`
check now always has a non-empty status to validate (previously only ran
when the caller explicitly supplied one) — a genuine strengthening, not
just a refactor.

### Design decisions

1. **Direction 1 (look up the registered flow) over Direction 2 (a
   module-registration default-status hook), investigated and rejected.**
   `RegisterFlow`'s `State{IsInitial: true}` is already persisted per-type
   in `smeldr_states.is_initial`. A second, module-level "default status"
   option would be a second source of truth that can drift from the
   registered flow (someone changes the flow's initial state later and
   forgets the module option) — exactly the kind of duplication this
   project's DRY principle warns against.
2. **`defaultInitialState` does not fall back to the default flow.**
   Verified the built-in default flow's own `IsInitial` state actually is
   `"draft"` (`migrate.go`'s `migrateStateFlows`) before relying on this —
   the Go-level fallback to the literal `Draft` constant in
   `applyDefaultStatus` produces the identical answer without a second
   query, for the overwhelmingly common case (no custom flow registered).
3. **Dedicated `TestApplyDefaultStatus_*` unit tests, planned but not
   written — a deliberate deviation, not an oversight.** Coverage
   confirmed 100% on both new functions from the integration tests alone
   (`TestMCPCreate_omittedStatus_customInitialState`/`_defaultsToDraft`,
   `TestCreateHandler_omittedStatus_customInitialState`/`_defaultsToDraft`,
   plus the existing `TestMCPCreate_invalidInitialState`/
   `TestCreateHandler_invalidInitialState` exercising the already-set-status
   early return). Adding isolated unit tests on top would have duplicated
   the same branches without exercising anything the integration tests
   don't already prove end to end.

### Consequences

- No exported Go symbols changed — `defaultInitialState` and
  `applyDefaultStatus` are both unexported.
- Real behaviour fix, cross-file (`state.go` + `module.go`), affecting two
  HTTP/MCP entry points.
- `example/server`'s `TestServerToggles/on/orchestration`/
  `on/orchestrationWithRelations` (failing on `main` before this fix,
  confirmed via a clean checkout during T178) now pass. The `on/provenance`
  test's `"status": "open"` workaround (added during T178 to sidestep this
  exact bug) removed — proves the fix from a third angle.
- New tests: `TestDefaultInitialState_nilDB`, `_nonSQLite`, `_noFlow`,
  `_noInitialState`, `_customInitialState`, `_stateQueryError`
  (`state_test.go`); `TestMCPCreate_omittedStatus_customInitialState`,
  `_defaultsToDraft`, `TestCreateHandler_omittedStatus_customInitialState`,
  `_defaultsToDraft` (`state_test.go`, alongside the existing A216
  integration tests). Coverage: 96.1% (unchanged), 100% on both new
  functions.
- Patch release, matching A221/A222's own precedent for real
  previously-broken-functionality fixes with no new exported symbols:
  v1.58.0 → v1.58.1.
- Level 2 amendment (cross-file, real behaviour fix affecting two
  entry points).

Level 2 amendment.

---

## A226 — Fix `updateHandler`'s double-`Save` on publish-via-PUT (T179)

### What

`example/blog`'s `TestBlogSignal` and `TestBlogFullServer/audit/
recordedOnPublish` both failed deterministically — every run, not
intermittently — with a 409 `rev_conflict` on the PUT request that
publishes a draft post.

`module.go`'s `updateHandler` calls `m.repo.Save(ctx, item)` **twice** when
a request transitions an item from non-`Published` to `Published`: once
unconditionally, then again after `setNodeTime(item, "PublishedAt", ...)`.
`SQLRepo.Save` (`storage.go`) is a compare-and-swap: `UPDATE ... WHERE
rev = $N` using the in-memory `item.Rev` value, then increments the stored
`rev` by 1 (`SET rev = table.rev + 1`) — it never writes the new,
incremented value back into the caller's `item` struct. After Save #1
succeeds, the database row's `rev` is `N+1`, but `item.Rev` in Go is still
`N`. Save #2 issues `UPDATE ... WHERE rev = $N` again — against a row now
at `N+1` — matches zero rows, and returns `ErrRevConflict`. This is
structurally guaranteed to fail every time a PUT transitions an item to
`Published`; `updateHandler` returns the 409 before
`m.notifyAfter(ctx, AfterPublish, ...)` is ever reached, so no PUT-driven
publish fired `AfterPublish` at all, silently, in addition to returning
the wrong status code.

**Historical origin, found via `git log -S`:** the double-`Save` pattern
was introduced deliberately by Amendment A48 (`c4fdce2`, Milestone 9,
2026-03-15) — "Second save committed before AfterPublish signal dispatch
so handlers see the correct timestamp." At the time this was correct: no
optimistic-concurrency mechanism existed yet. Amendment A158 (`Node.Rev`,
2026-06-20 — three months later) added the `rev`-based CAS to `SQLRepo.Save`
for a completely unrelated reason (preventing lost updates under concurrent
writers) and silently broke A48's pattern — two unrelated amendments never
re-verified against each other, caught only because `example/blog`'s own
integration-style tests exercise a real SQL-backed publish-via-PUT.

**Verified empirically before proposing the fix:** implemented the change
directly first, ran `TestBlogSignal`/`TestBlogFullServer` — both passed.
Ran the full `example/blog` suite — green. Ran core's own `go test ./...`
— one pre-existing test failed: `TestModule_updateHandler_secondSaveError`,
which exists specifically to exercise the second-`Save`-fails error branch
this fix removes. Reverted the change afterward, per protocol, before
writing the plan.

**Other `setNodeTime(item, "PublishedAt", ...)` call sites — checked, not
affected.** `processScheduled` (scheduler) and `MCPPublish` both set status
+ `PublishedAt` **before** their single `Save` call — never had this bug.
`dynamic.go`'s `DynamicTypeRepo.setStatus` (the runtime/dynamic-content
equivalent) uses one direct hand-written `UPDATE` with no `rev`-CAS
mechanism at all — also unaffected. `updateHandler` was the only broken
site — and its own bug contradicted A48's own stated intent ("mirrors the
scheduler's behaviour"), which `processScheduled` already does correctly.

### Fix

Set `PublishedAt` before the (now single) `Save` call, matching
`processScheduled`/`MCPPublish`'s own existing, correct shape:

```go
if prevStatus != Published && newStatus == Published {
    setNodeTime(item, "PublishedAt", time.Now().UTC())
}

if err := m.repo.Save(ctx, item); err != nil {
    WriteError(w, r, err)
    return
}
```

### Test changes

**Removed, not repurposed:** `TestModule_updateHandler_secondSaveError`
and its sole fixture, `secondSavefailRepo` (confirmed via grep it had no
other caller) — the second-`Save`-fails scenario they exercised becomes
structurally impossible, not merely untested. `TestModule_updateHandler_
saveError` (the merged single `Save`'s error path) continues to cover
what remains.

**Added:** `TestModule_updateHandler_publishSetsPublishedAtOneSave` — a
real regression test against a real `SQLRepo`-backed repo (not
`MemoryRepo`, which can't exercise the rev-CAS at all, matching
`example/blog`'s own failure mode), proving the actual fix rather than
just the absence of an error: response status 200, `PublishedAt` non-zero,
and an `AfterPublish` `On(...)` hook actually fires.

`example/blog`'s `TestBlogSignal`/`TestBlogFullServer/audit/
recordedOnPublish` needed no changes — they already correctly expressed
the expected behaviour; they simply go from red to green.

### Consequences

- No exported Go symbols changed.
- Real behaviour fix: PUT-driven publish transitions now succeed and fire
  `AfterPublish` (previously always failed with 409, silently skipping the
  signal).
- `example/blog`'s two previously-failing tests now pass.
- New test: `TestModule_updateHandler_publishSetsPublishedAtOneSave`.
  Removed: `TestModule_updateHandler_secondSaveError`, `secondSavefailRepo`.
  Coverage: 96.1% (unchanged).
- Patch release, matching A221/A222/A225's own precedent for real
  previously-broken-functionality fixes with no new exported symbols:
  v1.58.1 → v1.58.2.
- Level 2 amendment (route behaviour change, `module.go`).

Level 2 amendment.

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
