# Smeldr — Decisions Archive (Phase 30)

Archived from `decisions/recent.md` on 2026-08-17. Entries A265–A267.

---

## A265 — PATCH route for typed items over REST (T242)

### Problem

Dynamic-content REST (`PATCH /_content/{type}/{id}`) and typed MCP
(`MCPUpdate`) both already had real partial-update semantics. Typed
`Module[T]` REST only had `PUT` — full-replace, so any field absent from
the body silently takes its Go zero value. Found by Peter asking whether
he could ratify a Decision with `curl`: no `PATCH` route exists for a
typed item at all, only `PUT`.

### Design — reuse MCPUpdate's own logic, don't reimplement it

`MCPUpdate` already is the exact logic a `PATCH` route needs: partial
merge onto the existing item, identity/lifecycle restored after the
merge, validated, saved, `notifyAfter`'d. The new `patchHandler` decodes
the body as a `map[string]any` (matching the dynamic-content `PATCH`
handler's own simpler pattern — no typed struct to overflow the same way
`updateHandler`'s `MaxBytesError` handling guards against) and calls the
same merge logic `MCPUpdate` calls, gated by `checkWriteOp(ctx, "update",
m.writeRole)` — the identical call `updateHandler` already makes for
`PUT`, so `PATCH` and `PUT` require the same authority. Registered in
`Register` mirroring `PUT`'s own middleware-chain wrapping exactly.

**Role tier, the plan's own open question, settled by the architect:**
same as `PUT`, not a narrower tier. `PATCH` and `PUT` are the same
write-authority question with a different body shape; nothing in this
codebase's existing role model treats a partial update as inherently
lower-risk than a full replace.

**State-change question, answered by construction, not chosen freshly:**
since the new route goes through the same identity/lifecycle-restore
logic `MCPUpdate` already has, a `Status`/`ID`/`Slug` field in the `PATCH`
body is silently discarded — identical to `MCPUpdate`'s own existing
contract. `transition_item`/`PUT` remain the only ways to change state.

**Does the dynamic-content PATCH handler's shape generalise? No —
argued, not assumed.** `newUpdateContentHandler` operates on
`DynamicNode`'s JSON-blob storage via `DynamicTypeRepo.UpdateFields` — a
fundamentally different representation from `Module[T]`'s strongly-typed
Go structs reached via reflection. Sharing one handler would need a new
interface abstracting over both storage shapes for one call site's
benefit — not worth building when `MCPUpdate` already provides the
complete, tested typed-side logic with nothing to abstract over. Each
surface keeps its own handler; the *pattern* (decode a fields map,
restore identity/lifecycle, validate, save) is shared in spirit, not in
code, because the underlying merge mechanics genuinely differ.

### Real bug caught during implementation — not part of the approved plan's own text

The plan, as approved, said "the new route calls `MCPUpdate` directly."
Reading `MCPUpdate`'s own body closely before wiring it up surfaced a
real consequence the plan hadn't examined: `MCPUpdate`'s `notifyAfter`
call hardcodes `surfaceMCP` — every caller of `MCPUpdate`, regardless of
its own transport, gets recorded as MCP-originated. `updateHandler`'s own
`PUT` call site, by contrast, already correctly passes `surfaceHTTP` at
the equivalent point (`module.go:1960`), confirming this is a real
mismatch, not a hypothetical one: calling `MCPUpdate` directly from the
new `PATCH` route would have misreported every REST-originated partial
update as MCP-originated, in a project that has previously done real,
careful work threading `Surface` accurately through 14 separate call
sites (A260).

**Fix:** extracted `MCPUpdate`'s own body into a new unexported
`updateFields(ctx, slug, fields, surface string) (any, error)`,
parameterized on the calling surface. `MCPUpdate` becomes a one-line
wrapper passing `surfaceMCP` — behaviour-preserving, confirmed by running
its own full existing test suite unmodified before writing anything new.
The new `patchHandler` calls `updateFields` directly with `surfaceHTTP`,
bypassing `MCPUpdate` entirely. Verified with a dedicated test going
through the real HTTP handler end-to-end (wiring `App.Provenance`, a real
`OnSignal` subscription, and asserting the recorded `Surface` — not by
calling `notifyAfter` directly, which would only prove the plumbing
exists, not that the new route actually uses it correctly).

### Tests

7 new (6 planned + 1 for the Surface fix found during implementation):
`TestModule_PatchHandler_PartialUpdate_PreservesAbsentFields` (the direct
regression pin — an omitted field keeps its value, unlike `PUT`),
`_RestoresIdentityAndStatus`, `_Forbidden_WithoutWriteRole`,
`_BadRequest_InvalidJSON`, `_NotFound_UnknownSlug`,
`_SurfaceIsHTTP_NotMCP` (the fix above, end-to-end),
`TestModule_Register_MountsPatchRoute` (a route that compiles but never
mounts is the exact class of gap this task itself is). All existing
`MCPUpdate`/`MCPPublish`/`resolveItem` tests pass unmodified.

### Versioning

New route, no exported symbol added, no existing signature changed
(`patchHandler`/`updateFields` both unexported, matching
`updateHandler`/`createHandler`/`deleteHandler`'s own visibility). New
consumer-visible REST capability — a route that returned 404/405 now
responds. MINOR bump. Coverage: 96.3%; `patchHandler` 100%, `MCPUpdate`
100%, `updateFields` 96.3% (the `syncSaveHook` error branch is a
pre-existing gap carried over unchanged from `MCPUpdate`'s own prior
coverage, not introduced by this extraction). `go test -race ./...`
clean. Level 2 amendment.

---

## A266 — resolveItem gains a FindByID fallback (T214)

### Problem

`mcp/tool.go`'s `identArg` returns whichever of `"id"`/`"slug"` is present
in the caller's args, but every one of its six call sites (`update`,
`publish`, `schedule`, `archive`, `delete`, `get`) named the result `slug`
and passed it into a slug-resolution path — `"id"` was an alias for the
*key name* only, never for the *identifier type*. A real `Node.ID` passed
under `"id"` resolved nothing, despite `identArg`'s own doc comment
("accepting both id and slug") reading as genuine ID support.

### Investigation — the fix belongs in core, mcp needs zero changes

Traced all six `identArg` call sites directly. Every one already passes
its resolved string into a core `Module[T]` MCP method
(`MCPUpdate`/`MCPGet`/`MCPPublish`/`MCPSchedule`/`MCPArchive`/`MCPDelete`),
and all six already route through `Module.resolveItem` (T253) as their
single resolution funnel — which tries `FindBySlug`, then, only for a
type with a registered `humanIDColumns` entry, `FindByColumn`.
`resolveItem` never tried `FindByID` — the actual gap.

**`FindByID` is already a required method on `Repository[T]`**
(`storage.go`) — every `SQLRepo[T]`/`MemoryRepo[T]` implements it, unlike
`ColumnLookupRepository`, which is an optional extension. Trying it is
therefore safe and universal for *every* `Module[T]` type, not only the
four with a `humanIDColumns` entry — this closes a strictly wider gap
than T253's own humanID-only fallback. Confirmed both `Repository[T]`
implementations return `ErrNotFound` on a miss, matching `FindBySlug`'s
own contract exactly.

**Confirmed `transition_item` is out of scope.** Checked its own mcp-side
tool directly: its schema requires `"slug"` specifically, uses
`stringArg(args, "slug")`, never calls `identArg` at all.
`App.TransitionItem`'s own separate raw-SQL resolution path (`state.go`)
never claimed `"id"` support in the first place — a different tool with
an honest, narrower contract, not this bug.

**Conclusion: extend `resolveItem`'s own fallback chain with one new
step, `FindByID`, between the existing slug lookup and the humanID
fallback — mcp needs zero changes.** `identArg`'s own doc comment becomes
true rather than needing correction, once this lands.

### Design

Ordering argued, not arbitrary: slug stays first (unchanged existing
behaviour/performance for the common case — every existing caller and
test keeps working identically), `FindByID` second (universal, required
by the interface, no type-specific gate), `humanIDColumns` fallback last
(the most specialized, only four types). A slug string colliding with a
different item's real UUID is not a realistic concern (`NewID()` is a
UUID v7).

### Caught in architect review before implementation

The plan's own test list didn't include a dedicated test for `FindByID`'s
own non-`ErrNotFound`-error branch — a genuinely new line, distinct from
`FindBySlug`'s identical-looking check one step earlier, which the
existing `TestResolveItem_NonErrNotFoundPropagates` test never reaches
(its own `errorRepo` fails at the `FindBySlug` check first). Added
`TestResolveItem_ByID_NonErrNotFoundPropagates` with a new dedicated test
double (`slugMissIDErrorRepo`, ErrNotFound from `FindBySlug`, a real error
from `FindByID`). Also confirmed, not assumed, that the two "existing
test, same branch, reached later" cases
(`TestResolveItem_NonOrchestrationType_NoFallback`,
`TestResolveItem_RepoLacksColumnLookup`) still exercise their intended
branches post-change — both test doubles already implemented `FindByID`
returning `ErrNotFound` (a required interface method since T253), so both
correctly fall through the new step unchanged.

### Tests

4 new: `TestResolveItem_ByID` (the direct regression pin — a real
`Node.ID` resolves after a slug miss), `TestResolveItem_ByID_NonOrchestrationType`
(the new fallback works for a type with *no* `humanIDColumns` entry at
all — the actual scope difference over T253's own fallback),
`TestResolveItem_ByID_NonErrNotFoundPropagates` (architect-review catch,
above), `TestModule_MCPGet_ByID` (end-to-end through the real MCP entry
point, mirroring `TestModule_MCPGet_ByHumanID`'s own T253 pattern). All 6
existing `TestResolveItem_*` tests (T253) re-run and pass unmodified.

### Versioning

`resolveItem` is unexported — no exported Go symbol added or changed.
Real consumer-observable behaviour change: every `Module[T]` MCP tool now
resolves a real `Node.ID`, where it previously silently failed with "not
found." Matches A261's own precedent (behaviour change, no exported
symbol → PATCH bump), not A262's (new exported symbol → MINOR). Coverage:
96.3%; `resolveItem` 100%. `go test -race ./...` clean. Level 2
amendment.

---

## A267 — DefaultListOrder module option; sortItems gains numeric support (T262)

### Problem

`Task.Priority`/`Goal.Priority` (int, lower = higher priority) existed
and were stored but nothing read them. `list_task`/`list_goal` (and every
other compiled-type list tool) had no sort parameter at all, so a caller
always got whatever order the underlying query happened to return.
Discovered when two Tasks' own priority numbers contradicted their
intended sequencing, with nothing surfacing the mismatch.

### Investigation — real work found in both directions, per the task's own instruction

**mcp side:** there is no separate `list_tasks` tool — `"list"` is a
generic op dispatched by type name, calling `lm.MCPList(ctx,
statuses...)` for whatever type the tool resolves to. `MCPList` is part
of the **exported `MCPModule` interface** — confirmed one external
implementer, `smeldr.dev/media`. Changing its signature to accept a
caller-supplied `orderBy` would break every implementer, the identical
class of concern D49 solved for `TransitionItem` by adding a new `App`
method instead of changing the interface. A caller-supplied parameter is
real, legitimate design space — a new optional extension interface,
matching `SeqRepository`/`ColumnLookupRepository`'s own established
pattern — but bigger than this task's own scope and the concrete problem
actually reported.

**The other direction — a real, deeper bug found, not assumed already
fixed:** `ListOptions.OrderBy`'s own doc comment already said sorting
applies only to exported *string* fields. Traced the actual
implementation: `SQLRepo`'s path builds a real SQL `ORDER BY <column>`,
correct for an `INTEGER` column — a live SQLite/Postgres-backed instance
would sort correctly once `OrderBy` is set. `MemoryRepo`'s path
(`sortItems`/`stringField`) is different: `stringField` explicitly
returns `""` for any non-string kind, so sorting a `MemoryRepo` by
`"Priority"` treated every item as equal — a silent no-op sort, not a
partial fix. **"Just wire the existing machinery through" would not have
delivered real sorting for any `MemoryRepo`-backed deployment** — a real
prerequisite fix, not scope creep. `stringField` has 7 other call sites
(`ID`/`Slug`/`Status`/`FindByColumn` lookups) that are correctly
string-only by construction — left untouched; a new, separate
`sortFieldValue` used only by `sortItems`.

### Design — no breaking change anywhere, in either repo

Rejected a caller-supplied `orderBy` parameter (above). Chosen: a
**module-level default list order**, applied identically to both the MCP
and HTTP list surfaces of a type, set once at registration:

```go
func DefaultListOrder(field string, desc bool) Option
```

Solves the actual complaint with zero interface changes anywhere —
`MCPList`'s own exported signature and behavioural contract (returns
matching items) is unchanged; it simply returns them in a more useful
order for types that opt in. `mcp` needs zero changes, matching T214's
own precedent shape.

New unexported `Module[T]` fields (`defaultOrderBy`, `defaultOrderDesc`)
set via the existing option-parsing switch. New `withDefaultOrder`
helper, shared by `MCPList` and `listHandler`, so both surfaces agree on
order for the same type — avoiding the asymmetry of "MCP shows priority
order, HTTP doesn't" for identical underlying data.

`RegisterOrchestrationTypes`'s `Task`/`Goal` module construction each
gain `DefaultListOrder("Priority", false)`. `Signal`, `Decision`,
`Amendment`, `Run` are untouched — none have a `Priority` field.

### `storage.go` — numeric sort support, additive

New `sortFieldValue[T any](v T, name string) (s string, i int64, isInt bool)`
— a string for a string field, an int64 for any signed-integer-width
field, a zero value with `isInt=false` for any other kind (unknown field,
non-comparable type) — matching `sortItems`' own existing fail-quiet-to-
equal behaviour. `sortPair[T]` gains `intKey`/`isInt` alongside its
existing `key`; `sortItems` compares whichever key is populated. Every
existing string-field sort is unaffected — same comparison path, same
result, confirmed by re-running the existing tests unmodified, not
assumed.

### Tests

8 new: `TestSortItems_IntField` (ascending/descending, the direct
regression pin), `TestSortFieldValue_UnknownField`,
`_NonComparableKind` (fail-quiet cases), `TestModule_MCPList_DefaultOrder`,
`_NoDefaultOrder_Unchanged` (every other registered type's own
regression pin), `TestModule_listHandler_DefaultOrder` (MCP/HTTP
consistency, not asserted from `MCPList` alone),
`TestRegisterOrchestrationTypes_TaskGoalDefaultOrder` (scope check — the
wiring is real, not just designed). All existing `OrderBy`/sort tests
(string path) re-run and pass unmodified.

### Versioning

New exported symbol: `DefaultListOrder`. `sortFieldValue` unexported.
Real consumer-observable behaviour change: `get_task`/`get_goal` (MCP)
and `GET /tasks`/`GET /goals` (HTTP) now return items priority-ordered by
default. MINOR bump, matching A262's own precedent (new exported symbol
→ MINOR). Coverage: 96.3%; `MCPList`/`listHandler`/`withDefaultOrder`
100%; `sortFieldValue` 86.7%/`sortItems` 93.8% (uncovered branches are
the nil-pointer guard, non-struct `T`, and `Int8`/`Int16`/`Int32` kinds —
no content type in this codebase uses those kinds or ever passes a nil
pointer through this path; the same structurally-defensive-only class
already accepted elsewhere this session, named not chased). `go test
-race ./...` clean. Level 2 amendment.

---

