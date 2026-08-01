# Smeldr — Decisions Archive (Phase 20)

Archived from `decisions/recent.md` on 2026-07-30. Entries A225-A226.

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

