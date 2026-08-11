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
---

## D49 — Transition authority for compiled types lives on App, not on the MCPModule interface

### Scope

core (App.TransitionItem), mcp (transition_item dispatch)

### Decision

Flow-validated state transitions for compiled types are performed by a
new `App.TransitionItem(ctx, typeName, slug, toState)` — resolving the
item's table via `resolveItemTable`, validating via the same
`validateTransition` call the REST path (`updateHandler`) already makes,
and persisting via the file's established raw status-UPDATE pattern
(`setStatus`/`conflictSupersede`'s own precedent). The dynamic-content
branch delegates to `DynamicContentRepo` unchanged.

### Rejected alternative

A new required `MCPTransition(ctx, slug, toState)` method on the exported
`MCPModule` interface, implemented generically on `Module[T]` with typed
`repo.Save` (rev-CAS preserved). Rejected on the API stability promise:
Go interface satisfaction has no default-method escape, so a new required
method breaks every `MCPModule` implementer that is not `Module[T]`
itself. The benefit it would buy — typed rev-CAS on a status-only write —
is one the codebase's existing status-write paths already forgo
deliberately; declining it here is consistency, not new risk.

### Boundary

The raw status-UPDATE's `rev` behaviour must be stated explicitly in the
implementing Amendment's own text, whichever it is — it borders T227's
territory (rev echo semantics), and an unstated rev semantic is the kind
of gap that gets rediscovered expensively.

### Status

Ratified 2026-08-11 (Peter, via architect session). Implementation: the
compiled-state-tools cycle.

---

## A251 — App.TransitionItem extends state transitions to compiled types (smeldr/core half of D49)

### What this closes

`transition_item`/`get_valid_transitions`/`list_items_by_state` cleanly
rejected compiled types after A248 (T225's own fix) instead of
crashing — correct, but not the same as operating on one. M0 step 7's
signal protocol needs a real MCP path to move a `Signal`'s flow state;
this closes that gap.

### The mechanism

New `App.TransitionItem(ctx, typeName, slug, toState) (map[string]any,
error)` branches on `TypeRegistry().Lookup(typeName).Kind`:

- `"content"` delegates unchanged to `DynamicContentRepo` +
  `SetStatus` — zero behavioural difference for every existing
  dynamic-content caller. Not reimplemented; literally called.
- `"compiled"` resolves the table via `resolveItemTable` (D42/D47's
  own reused helper), runs the identical `validateTransition` call
  `updateHandler` (`module.go:1876`) already makes for the HTTP path
  — same signature, same arguments, same `ErrForbidden` on a `Strict`
  gate without the role — then a raw `UPDATE … SET status = …`,
  mirroring `applyConflictPolicy`'s own `conflictSupersede` precedent
  in the same file rather than inventing a new raw-update shape.

A compiled type whose backing table can't be found (partial
migration, an unregistered module) returns a distinct error instead
of silently falling into the dynamic-content branch — D47's own guard
against exactly that ambiguity, applied here too.

### Why App, not the MCPModule interface (D49)

Two designs were considered. The chosen one — a new `App` method — adds
zero exported interface surface. The rejected one — a new required
`MCPTransition` method on the exported `MCPModule` interface,
implemented generically on `Module[T]` with typed `repo.Save` (rev-CAS
preserved) — was rejected on the API stability promise: Go interface
satisfaction has no default-method escape hatch, so a new required
method breaks every `MCPModule` implementer that is not `Module[T]`
itself. See D49's own entry for the full argument.

### Rev behaviour, stated explicitly per D49's own boundary requirement

The compiled-type raw `UPDATE` does not read or advance `Node.Rev`.
Concurrency consequence, said plainly: a concurrent `Save` holding the
item's pre-transition rev still satisfies `Save`'s CAS check (the
stored rev in the DB is unchanged) and will silently overwrite this
status change with whatever status its own in-memory item carries —
no `ErrRevConflict`, no error at all. This is not a new risk this
Amendment introduces: `DynamicTypeRepo.setStatus` and
`applyConflictPolicy`'s own `conflictSupersede` path already make
this exact choice for their raw status updates. This Amendment
extends an already-accepted pattern to a new table class; it does not
invent the exposure.

### Async triggers

Fires `fireAsyncTriggers` (A240's `TransitionTrigger` pipeline) after
a successful compiled-type update — the same, and only, hook
`setStatus` fires for the dynamic path. Does **not** fire
`module.go`'s `notifyAfter`/`AfterPublish`-class signal-bus hooks for
either branch. This is an existing, deliberate split in the codebase
already: the state-flow-transition path and the lifecycle-signal path
are two different mechanisms today, confirmed by reading `setStatus`
directly before writing this method. Extending `transition_item` to
compiled types makes that existing split apply symmetrically — it
does not create a new asymmetry.

### A pre-existing gap closed as a side effect, not the main point

The unregistered-type error now wraps `ErrBadRequest` instead of a
bare error. Found while wiring the mcp side: `errorFor` had no mapping
for `ErrBadRequest` at all, even though `validateTransition`'s own
`RequiredReason` case has returned it since T149/A220 with nowhere for
a JSON-RPC caller to see anything but a generic `-32603`. Fixed in the
paired `smeldr.dev/mcp` Amendment (A252): `ErrBadRequest` now maps to
`-32602`, the same code `ValidationError` already uses, since both
mean the caller's own request was malformed.

### Tests and coverage

17 new tests (`state_transition_item_test.go`), full error-path table:
unregistered type, item not found (both branches), invalid target
state, transition not permitted, role-gated forbidden/granted, DB nil,
compiled-type table missing, transition-row query failure (D34
fail-closed), UPDATE exec failure, conflict-policy violation, and the
item-lookup SELECT itself failing. No exported symbols removed.
Coverage: 96.3% package-wide; `TransitionItem` itself 94.7% — two
branches named, not chased: `DynamicContentRepo`'s own error and
`repo.List`'s own error inside the already-passed `Kind == "content"`
check are structurally unreachable through the real dispatch path
(state tools require `Config().DB != nil` before dispatch even
reaches this method, and `Kind` cannot change between this method's
own registry check and `DynamicContentRepo`'s identical internal one).

### Versioning

MINOR bump — new exported API surface (`App.TransitionItem`), same
classification as A235/A236/A239. v1.63.3 → v1.64.0.

Status: Implements D49.

---

## A252 — transition_item/get_valid_transitions/list_items_by_state operate on compiled types (D49, smeldr.dev/mcp half)

### What shipped

`transition_item` now calls the new `smeldr.dev/core` `App.TransitionItem` (A251) instead
of cleanly rejecting every compiled type, the actual capability A251's core-side work
exists to expose. `get_valid_transitions` and `list_items_by_state` need no new core API:
both resolve a compiled type's current status/listing through its own module's already-
exported, type-erased `MCPGet`/`MCPList` — status is read from the marshaled JSON result
(`Node.Status` carries no `json` tag, so it marshals under its own field name; no
reflection required). Role-gated transitions (D34/D40) behave identically through this
path to the REST path, since both ultimately reach the same `validateTransition` call.

### errorFor gains a mapping it always should have had

`validateTransition`'s `RequiredReason` branch has returned `ErrBadRequest` since T149/A220
with no JSON-RPC code mapping anywhere in `errorFor` — every call fell through to the
generic `-32603`. Closed as a direct side effect of wiring `App.TransitionItem`'s own "type
not registered" error through the same path: `errorFor` now maps `smeldr.ErrBadRequest` →
`-32602`, the same code `ValidationError` already uses.

### Versioning

Requires `smeldr.dev/core` v1.64.0+ (`App.TransitionItem`) — go.mod bumped only after
core's tag was proxy-verified, matching A243's own precedent, not a `go.work`-local
override. MINOR bump — new consumer-visible tool capability, three previously-clean-
rejecting tools now actually operate on compiled types. v1.30.1 → v1.30.2.

Status: Implements D49 (paired with A251, smeldr/core half — see that entry for the full
design rationale, not restated here).

### Note

This entry backfills a completeness gap: `mcp/CHANGELOG.md`'s own `[1.30.2]` section and
A251's own text both already cited "A252" at the time of that release, but the index row
and this body were never written — the same class of gap A250 itself named and fixed one
cycle earlier. Found and fixed during T227's plan review (2026-08-11), before T227's own
Amendment number could be correctly assigned.

---

## D48 — A generated tool's authority requirement is derived from its structure, never enumerated by hand

### Scope

mcp (mechanism), core (seed rows + documentation)

### Decision

When `smeldr_tool_policies` has no row for a tool name, the required
operation is **derived from the tool's own verb prefix** — `get`/`list` →
`read`, `create` → `create`, `update` → `update`, `publish`/`schedule` →
`publish`, `archive` → `archive`, `delete` → `delete` — **if and only if a
real registered module backs the parsed type name** (`moduleForType`, or
`moduleForAdminList` for list-tools' plural names). Otherwise denial stands
exactly as before.

**An explicit policy row always wins over derivation.** Derivation is a
default, not an override: an operator who writes a row for a specific tool
changes that tool's requirement, and nothing re-derives it.

### What this settles

T224: `seedToolPolicies` is a hand-maintained list covering the four
built-in content types and a set of framework tools. The six orchestration
types' generated per-type tools had no rows, and `authoriseTool`'s
fail-closed not-found branch therefore denied every one of them — verified
live on `process.smeldr.dev` with a token holding a real admin grant.
`create_signal` worked and `get_signal` did not, for no reason an operator
could inspect. M0 step 7 was blocked on it.

The deeper defect is the list itself. A hand-enumerated authority surface
is correct only for what was enumerated at the time — the exact class D47
closed for the sweep's `TargetChecker`, and this same list was already
patched once this week (A242's grant tools, whose own comment warns that an
unpolicied tool is denied for everyone). A seventh orchestration type, or
any new compiled module, would silently reopen the hole.

### Rejected alternatives

- **Seed rows per generated tool, per deployment.** Correct today, wrong
  the day the next type lands, and each patch is another chance to miss
  one. Rejected on D47's own argument, not on cost.
- **Missing row means allow.** Fails Article I outright — an unknown tool
  name would acquire authority nobody granted.
- **Derivation inside core's `RoleStore.ToolPolicy`.** `parseToolName` and
  the module registry are mcp-side knowledge; core cannot confirm a real
  module backs a type name. The fallback lives in `mcp`'s `authoriseTool`
  path, where that confirmation is possible — an unknown or misspelled
  name still fails closed because no module confirms it.

### Boundary

`manage`, `administer`, `define-type`, `define-flow` and
`define-relation-kind` have no generated verb form and are **never
derived** — every tool requiring them keeps an explicit row.
`get_goal_context` and `list_type_tools` are framework tools, not generated
ones, and get explicit `read` rows in the same change.

The derivation rule is documented in governance-model.md §4 in the same
commit that ships it. A requirement an operator cannot read about anywhere
is silent authority, which is the failure mode this project keeps finding
elsewhere (T219, T223).

### Status

Ratified 2026-08-10 (Peter, via architect session). Implementation: T224's
cycle, smeldr.dev/mcp.

---

## D47 — A structural sweep gets a structural answer: for a compiled type, alive means the row exists

### Scope

core

### Decision

`SweepStructural`'s `TargetChecker` asks whether the thing an edge points at
still exists. For any compiled `Module` type, **alive means the row is still
there. No status is ever consulted.**

Not `superseded`, not `archived`, not `expired`, not any future terminal
state. A hard delete is the only thing that makes a target gone.

For a runtime-defined content type the existing rule is unchanged: alive means
`status = 'published'`.

### The principle, which is why this holds for types that do not exist yet

**A status is a semantic state. Existence is a structural fact. Answering a
structural question with a semantic test is a category error whatever status
is chosen.**

Stated this way deliberately, rather than as a table of the six current types.
A per-type enumeration would have to be revisited every time a seventh type or
a new terminal state is added, and each revisit is another chance to get it
wrong. The principle survives both.

It also dissolves a doubt rather than accepting it. Core-implementer proposed
the right rule but grounded it in an observation, that none of the six flows'
terminal states happen to mean "this never occurred", and was therefore least
confident about `Signal`'s `expired`, which reads closer to "no longer
relevant" than the others. Under the principle, `expired` is not a harder case
than `superseded`. It is the same case.

### What the wrong answer costs, in each direction

**Treating a terminal status as gone destroys history.** A `supersedes` edge
pointing at a superseded `Decision` is the record of the supersession itself.
Invalidating it deletes the evidence of the very act it documents. Archiving
is bookkeeping, not retraction, and a `derives_from` edge that breaks because
someone tidied up is a lost lineage.

**Answering "alive" unconditionally is equally wrong.** A checker that never
returns false makes the sweep incapable of detecting anything, which is the
state the whole exercise exists to leave. Hard delete is real and reachable
through the generic delete tool, so this rule still has a false to return.

### The hazard this closes, and the one it opens sideways

`App.SweepStructural`'s built-in checker previously queried only
`smeldr_dynamic_content` and returned false when it found no row. Every
compiled type would have answered false. **On an orchestration instance the
first run would have invalidated the entire lineage graph.** The behaviour was
documented in the function's own godoc, which said compiled types need a
custom checker; the defect was that nothing enforced it and an architect task
said "wire the detectors" without naming it.

`resolveItemTable` falls back to `smeldr_dynamic_content` whenever it finds no
dedicated table, which is correct for a genuine dynamic type and
indistinguishable from a compiled type whose table is simply absent: a
partially migrated instance, or a consumer that stopped registering a module.
Both would have reached the status lookup, found no row, and returned false.
The original hazard, reached sideways.

**Closed by requiring positive evidence rather than inferring from absence.**
Before the dynamic branch is trusted at all, the type must be confirmed as
registered in `smeldr_content_type_schemas`. When it is not, or when that
registry cannot be queried, the checker returns an **error rather than a
verdict**. `SweepStructural` counts an error as skipped and leaves the edge
untouched.

> A sweep that cannot tell whether a target exists must decline, never delete.

### Alternatives considered and rejected

- **Archived, superseded or expired means not alive.** The natural reading,
  and what the existing dynamic-content rule implies by analogy. Rejected: it
  destroys history, and it is the answer someone would arrive at by extending
  the old behaviour rather than by asking what the sweep is for.
- **A per-type table of what alive means.** Correct today and wrong on the
  day a seventh type lands.
- **Leaving the fix opt-in**, as a constructor or as `example/server` wiring.
  Rejected: an opt-in does not reach the consumer who does not know they are
  at risk, which is precisely the population this protects.

### Consequences

**Consumer-visible behaviour change on any instance with compiled types.**
Previously the default checker invalidated all their edges; now it does not.
Anyone relying on the old behaviour was relying on a bug.

**One caller-facing case survives and is documented.** An application whose
own compiled types genuinely do treat some status as equivalent to deletion,
unlike every built-in orchestration flow, must still supply its own
`TargetChecker`.

**This does not schedule anything.** The sweep still has no caller. Wiring it
is separate work with its own open questions about what a run must record.

Status: Ratified 2026-08-10.

---
## A250 — derive missing tool-policy rows from a generated tool's own verb (T224, smeldr.dev/mcp half of D48)

### The mechanism D48 requires

`authoriseTool`'s `ToolPolicy`-not-found branch (`tool.go`) previously
failed closed unconditionally — the exact behaviour that left every
one of the six orchestration types' own generated per-type tools
forbidden for everyone, T224's root finding. New `deriveToolPolicy`:
when no `smeldr_tool_policies` row exists, derive the required
operation from the tool's own verb prefix, using the identical
vocabulary `seedToolPolicies` itself already seeds with (`get`/`list`
→ `read`, `create` → `create`, `update` → `update`, `publish`/
`schedule` → `publish`, `archive` → `archive`, `delete` → `delete`).

Derivation only fires when a real registered module backs the parsed
type name: `moduleForAdminList` for a `list_*` name (reverses the
plural — `list_decisions` → `decision`), `moduleForType` for every
other verb. An unknown or misspelled tool name still fails closed —
nothing derives a requirement for a type name no module confirms.
An explicit `smeldr_tool_policies` row always wins over derivation;
derivation is a default, not an override.

`manage`, `administer`, `define-type`, `define-flow`, and
`define-relation-kind` have no generated-tool verb form and are never
derived, per D48's own boundary — every tool requiring one of these
keeps an explicit row, always.

### Why derivation lives in mcp, not core

`parseToolName` and the module registry (`s.modules`) are mcp-side
knowledge. Confirming "a real module backs this type name" — the
check that keeps an unrecognised name failing closed — is only
possible where that registry lives. Moving derivation into core's
`RoleStore.ToolPolicy` would give core no way to make that
confirmation, and D48 itself named this as a rejected alternative.

### Tests, made permanent not one-time

New `TestAuthoriseTool_PolicyCoverage_Enumerated`: derives the full
tool list from a real `tools/list` response — not hand-listed — and
asserts every tool resolves through either an explicit row or
derivation. This is the acceptance criterion itself, not just a
regression check: a future generated tool with no coverage is caught
here, the same way this one wasn't. New
`TestAuthoriseTool_GetSignal_NoLongerForbidden` reproduces T224's own
live symptom directly through `handleToolsCall` (a real `Signal`, a
real admin-granted token) rather than only through the enumeration
sweep.

### A real release-ordering lesson, found by the test itself

`go.mod`'s `smeldr.dev/core` pin was left at v1.63.0 when this shipped
— core v1.63.2 (A248, the `Kind` fix) and v1.63.3 (A249, the seed
rows this enumeration test depends on) were not yet tagged, matching
A243's own precedent of waiting for a proxy-verified tag rather than
a `go.work`-local override. CI caught this live: with the pin still
at v1.63.0, `TestAuthoriseTool_PolicyCoverage_Enumerated` correctly
failed on exactly `get_goal_context` and `list_type_tools` — the two
rows that only exist in v1.63.3 — because CI resolves the real
published proxy, not `go.work`'s local override that had been masking
the gap locally. The test was doing exactly what it was built for;
fixed by tagging core first, then bumping the pin against the
verified tag, matching the required sequence exactly.

### Versioning

Patch bump — no exported symbols changed (`deriveToolPolicy` is
unexported). v1.30.0 → v1.30.1.

Status: Implements D48.

---

## A249 — seedToolPolicies rows for get_goal_context/list_type_tools (T224, smeldr/core half of D48)

### What this closes

D48's boundary is explicit: `manage`, `administer`, `define-type`,
`define-flow`, and `define-relation-kind` have no generated verb
form and are never derived, and `get_goal_context`/`list_type_tools`
are framework tools, not generated ones — both get explicit `read`
rows rather than relying on the mcp-side fallback D48 itself
introduces. This is that seeding.

Verified live on `process.smeldr.dev` before this fix, as part of
T224's investigation: a token holding a real admin grant received
`-32001 forbidden` calling either tool, with no `smeldr_tool_policies`
row for either name and therefore nothing an operator could inspect
or grant against — `authoriseTool`'s not-found branch fails closed
by design (D44), and nothing had ever seeded a row for these two.

### Why "read", not something else

`AGENTS.md` already documented `get_goal_context` as requiring the
`Author` role (A199) before this fix — the seeded row makes reality
match a promise already published, not a new behavioural decision.
`list_type_tools` is a pure discovery/introspection tool (lists the
MCP tools available for a given content type) with the same shape:
read-only, no side effect, appropriate at the same tier as every
other `"read"`-gated tool in `seedToolPolicies`.

### Tests and coverage

New `TestRoleStore_ToolPolicy_OrchestrationDiscoveryTools` asserts
both tools resolve `found=true`/`op="read"` through the real
`RoleStore.ToolPolicy` path (not the seeding function directly).
No exported symbols changed. Coverage: 96.2% package-wide.

### Versioning

Patch bump — two previously-forbidden-for-everyone tools become
usable at their already-documented tier; no exported API surface
added. v1.63.2 → v1.63.3.

Status: Implements D48.

---

## A248 — Fix DynamicContentRepo's compiled-type guard (T225)

### Root cause

`App.Content()` registered every compiled module's
`TypeDescriptor` with `Kind: "content"` — the same value
assigned to genuine runtime-defined dynamic types created
via `DefineContentType`. This identity fooled
`DynamicContentRepo`'s compiled-type guard (`Kind !=
"content"` check) into believing compiled types were
runtime-defined and normal, causing them to bind to
`smeldr_dynamic_content` instead of their module's own
storage. MCP tools `transition_item`, `get_valid_transitions`,
and `list_items_by_state` crashed with `-32603 internal
error: SQL logic error: no such table: smeldr_dynamic_content`
when called on compiled orchestration types (Signal, Run,
Task, Decision, Amendment, Goal).

### How it was found

Reproduced locally, not reasoned about, per NEXT.md's own
instruction: a real in-process instance
(`RegisterOrchestrationTypes` called, no dynamic content
ever configured), a real `Signal` seeded, `get_valid_transitions`
called through the actual `handleToolsCall` entry point.
Traced backward from the missing-table crash through the SQL
error to the binding logic, then to the mislabeled `Kind`
field at the single registration site (`smeldr.go`).
Corroborating evidence found independently: the long-skipped
test `TestDynamicContentRepo_CompiledType_Rejected` had sat
skipped since A153 for the same reason — unable to inject a
`Kind: "block"` descriptor through the public API, because
the real public path had never produced one.

### The fix

`App.Content()` now assigns `Kind: "compiled"` (a new, third
value) when registering a compiled module's `TypeDescriptor`,
in place of `"content"`. Every one of `dynamic.go`'s six
`Kind != "content"` guards is a pure inequality, so any
non-`"content"` value — including the shorter `"block"` —
would have worked identically through every guard. `"block"`
was rejected anyway: `dynamic.go`'s `define_content_type` HTTP
handler and `smeldr.dev/mcp`'s matching MCP tool both echo
`desc.Kind` back in their response. Checked both, not assumed
harmless — neither can currently leak a compiled module's
`Kind`, only because `DefineContentType` unconditionally
forces `schema.Kind = "content"` regardless of what's passed
in. That safety is coincidental, not structural: nothing stops
a future diagnostic surface from enumerating
`TypeRegistry.All()` and rendering `.Kind` for every registered
type, compiled ones included, at which point `"block"` would
be an actively false answer. `registry.go`'s `TypeDescriptor.Kind`
field comment updated from `"block" | "content"` to `"block" |
"content" | "compiled"` — `"block"` itself stays a real,
distinct value (the block system's own use), unrenamed.

### Tests and coverage

`TestDynamicContentRepo_CompiledType_Rejected`
(`dynamic_app_test.go`) un-skipped: now calls
`CreateOrchestrationTables` + `RegisterOrchestrationTypes`
against a real app instance and asserts
`DynamicContentRepo("Signal")` returns the existing
`"is a compiled type; use its module directly"` error, through
the real public registration path rather than an unexported
internal one. No exported symbols changed. Coverage: 96.2%
package-wide. `go build`/`vet`/`gofmt`/`test`/`golangci-lint`
all clean.

### Explicitly out of scope, confirmed against the dispatch's own exclusions

This one-line fix alone makes `transition_item`/
`get_valid_transitions`/`list_items_by_state`
(`smeldr.dev/mcp`) return the existing clean
`"compiled type; use its module directly"` message for any
compiled type instead of crashing — through a mechanism that
already existed and was simply never reached. Whether these
three tools should be extended to actually *operate* on
compiled types, not just reject them cleanly, was argued both
directions in the plan (`resolveItemTable`, D42/D47's own
reused helper, plus the fully generic `validateTransition`
that `DynamicTypeRepo.setStatus` already calls before its one
dynamic-content-specific line, mean the authority gate would
not be bypassed) but deliberately not shipped here — a real
capability gap for M0 step 7, its own future dispatch, not
risked by bundling into this fix.

### Versioning

Patch bump — a real, consumer-observable fix to
`DynamicContentRepo`'s existing guard (three MCP tools stop
crashing on any compiled orchestration type), not new exported
API surface. `TypeDescriptor.Kind` gained a documented value;
no exported symbol removed. v1.63.1 → v1.63.2.

Status: Agreed.

---
