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

---

## Amendment A215 — Wire ContextPacketHandler into example/server (T147 Part 1)

**Date:** 2026-07-12
**Status:** Done
**Repo:** smeldr/core
**Pilot:** corepilot
**Level:** 1 (changes entirely within `example/server`; no exported core symbols)

### What was decided

Wire `App.ContextPacketHandler` into `example/server/main.go` so a locally-run instance can serve `GET /packet/{type}/{slug}[?depth=]`. Introduce `INSTANCE_NAME` env var for `PacketSource.Name`. Gate the handler on both `ENABLE_RELATIONS` and `ENABLE_ORCHESTRATION`.

### Rationale

`ContextPacketHandler` (A214) was implemented in core v1.55.0 but not wired into any running binary. Without wiring, the endpoint is unreachable from the dogfood instance. The handler requires a `*RelationStore` — meaningless without relation data — and orchestration types as anchor candidates, so both flags are required. A source name is needed for provenance in packet responses; `INSTANCE_NAME` follows the existing `ENABLE_*`/`BASE_URL` env var pattern.

### Consequences

- `GET /packet/{type}/{slug}[?depth=]` is now reachable on the dogfood instance when both flags are set
- `ServerConfig.InstanceName string` added (25th field); `INSTANCE_NAME` env var (default: `"smeldr-dogfood"`)
- Two separate `EnableRelations` blocks consolidated into one: `var rs *smeldr.RelationStore` hoisted; `CreateRelationTables` moved into the merged block (no behaviour change — DDL is idempotent)
- `TestServerToggles`: 7 → 9 sub-cases (`on/contextPacket`, `off/contextPacketWithoutRelations`)
- No exported core symbols changed; no version bump required

---

## Amendment A216 — T148: Orchestration create-time state validation

**Date:** 2026-07-15
**Status:** Done
**Repo:** smeldr/core
**Pilot:** corepilot
**Level:** 1 (no exported Go symbols changed)

### What was decided

Two related state-flow enforcement gaps were closed:

**Gap 1 — Create-time:** `createHandler` (HTTP POST) and `MCPCreate` (MCP) now call `validateInitialState` after `RunValidation`, before `repo.Save`. This rejects any `status` string that is not a registered state in the type's own flow (or the default flow). Previously, any string valid in *any* registered flow was accepted silently — root cause of the 14 "done"-status Amendments written by T147's data migration.

**Gap 2 — Transition-time:** `validateTransition` gains a target-state pre-check that queries `smeldr_states` immediately after resolving the flow ID. If the target state does not exist in the flow, it returns a specific `ErrConflict` ("not a valid target state") before reaching the transition-edge lookup. This is more descriptive than the previous "transition not permitted" message, which did not distinguish "edge missing" from "state doesn't exist in this flow".

**New unexported function:**
`validateInitialState(ctx context.Context, db DB, typeName, statusName string) error`

Fail-open in all structural error cases (nil DB, non-SQLite, missing flow, query error). Returns `ErrConflict` only when the state exists in no registered flow for the type.

**10 new tests in `state_test.go`:**
- `TestValidateInitialState_nilDB`, `_emptyStatus`, `_nonSQLite`, `_noFlow`, `_validState`, `_invalidState`, `_stateQueryError` (7 unit)
- `TestValidateTransition_unknownTargetState` (1 unit, validates new pre-check message)
- `TestMCPCreate_invalidInitialState`, `TestCreateHandler_invalidInitialState` (2 module integration)

### Why

An AI agent calling `create_amendment` with `status="done"` received no error and the item was stored with an invalid state — discovered only by re-querying. Given Smeldr's "deterministic, enforced state" guarantee, a silent success on invalid input is a product correctness gap. See T148 in ARCHITECT_TODO.md.

### Consequences

- `createHandler` and `MCPCreate` now reject invalid initial states with HTTP 409 / MCP ErrConflict
- `validateTransition` returns a more specific error when the target state does not exist in the flow
- No exported Go symbols added or changed
- No version bump required (fail-open behaviour for existing correct callers is unchanged)
- Level 1 amendment

---

## Amendment A217 — T150: updateHandler state governance + published→draft default transition

**Date:** 2026-07-15
**Status:** Done
**Repo:** smeldr/core
**Pilot:** corepilot
**Level:** 2 (route behaviour change; two files changed: migrate.go + module.go)

### What was decided

Two related governance gaps closed in one commit:

**Fix 1 — `module.go` (`updateHandler`):** After `prevStatus` and `newStatus` are resolved, if they differ, the handler now calls `validateTransition(ctx, m.db, m.roleStore, ctx.User().ID, m.contentTypeName, string(prevStatus), string(newStatus))`. If an error is returned, `WriteError` is called and the handler returns. Fail-open semantics are preserved: nil DB, non-SQLite, missing flow, and structural query errors all return nil (continue); `ErrConflict` (unknown target state or missing edge) returns HTTP 409; `ErrForbidden` (RequiredRole denied) returns HTTP 403. No new imports required.

**Fix 2 — `migrate.go` (`migrateStateFlows`):** Added `{"published", "draft"}` to the default flow's `transitions` slice as the sixth entry. The `ON CONFLICT (flow_id, from_state, to_state) DO NOTHING` insert makes this additive and idempotent for existing instances.

**Updated and new tests (5 total):**
- `TestModule_updateHandler_unpublish` — updated to use `newSQLiteDB(t)` + `migrateStateFlows` instead of running with `m.db == nil`
- `TestUpdateHandler_validateTransition_invalidTarget` — PUT with `Status: "done"` (not in default flow) returns 409 Conflict
- `TestUpdateHandler_validateTransition_sameStatus` — PUT with unchanged status skips validateTransition, succeeds without DB
- `TestMigrateStateFlows` — transition count assertion updated 5→6; inline `published→draft` count assertion added (no separate function — folded into the existing test)

### Why

`updateHandler` (HTTP PUT /{prefix}/{slug}) decoded the request body into a fresh item and preserved only ID and Slug from the existing record — leaving Status free to be overwritten by any authenticated caller with update rights. This bypassed both `RequiredRole` governance (T49) and state-flow correctness in a single PUT request. The `published→draft` gap is independent: the unpublish path was silently reachable in production only when `m.db == nil` (fail-open), meaning `TestModule_updateHandler_unpublish` passed because it ran without a database.

### Consequences

- PUT /{prefix}/{slug} now enforces state governance when the status changes
- Fail-open semantics (nil DB, no flow, structural errors) preserved — existing callers on no-DB setups are unaffected
- `published→draft` transition now present in the default flow — unpublish path is explicitly supported
- `TestModule_updateHandler_unpublish` now exercises `validateTransition` against a real DB — the test would have caught this gap on its own
- No exported Go symbols added or changed
- No version bump required (fail-open for existing correct callers unchanged)
- Level 2 amendment

---

## A218 — Agent role rename: pilot → core-implementer (housekeeping + startup test)

### What

`core/CLAUDE.md`: every "Corepilot"/"corepilot" reference (12 lines) renamed to
"core-implementer" — both the bare role name (e.g. "core-implementer owns all writes to
`decisions/`") and path references to the session-context file, which moves in lockstep
in `smeldr/architect` from `context/corepilot.md` to `context/core-implementer.md`
(git mv, plus a fix to two stale rows in that file's version table — see Consequences).
`smeldr/common/agent/skills/smeldr.md` role wording generalized ("developer or pilot
agent" → "developer or agent"; "Pilots read this file" → "Agents read this file") since
that file is shared across every agent, not just this one.

**Scope addition (architect-approved, same session):** `CLAUDE.md`'s commit-approval
language reconciled with its own Signal protocol section, which already defines
`commit-ready` → `commit-approved` as the approval mechanism. Four passages that still
described a chat-only "yes" flow were rewritten to route through the signal channel:
the Step 6 pre-commit gate close, the "Rules for steps" close, the
"Never push without explicit permission" section (retitled "Push follows commit
approval"), and the verification-commands bullet in "### 3. Implement the step"
(this fourth instance was caught by the architect on commit-feedback review and
folded into this same commit). The section resolved a direct self-contradiction: it said
"'Commit approved' is not push permission — always wait for 'push it'", while the
Branching section said the opposite ("'Commit approved' means: squash to main now.
Push follows immediately"). The Branching section's rule is correct and is now the
only statement of it; the contradicting sentence was removed.

Scope is role-naming and stale-reference correctness only. `DECISIONS.md`, `decisions/`,
and CHANGELOG historical text are untouched — lineage stays as written under the old name.
Other agents' own "pilot" naming (sitepilot, etc.) is out of scope; each agent renames
itself at its own next session.

### Why

The "pilot" naming was retired project-wide (naming note 2026-07-15, executed in
`smeldr/architect` files 2026-07-18). This session doubled as a protocol startup test —
full session-start → plan → signal approval → implementation → close cycle exercised
under the new name.

### Consequences

- No exported Go symbol touched, no runtime behaviour changed — pure instruction/doc text
- Session-start protocol in `CLAUDE.md` now points at the renamed context file
  (`context/core-implementer.md`); the file itself is renamed in the same task
  (`smeldr/architect`, committed separately in that repo)
- While correcting the file's known media-version typo (v1.0.0 → v1.6.0, called out in
  the task itself), a startup-test verification pass against actual git tags found two
  more rows wrong in the same table: cli listed as v0.19.0 (actual v0.15.2) and oauth
  listed as v0.4.0 (actual v0.3.0) — both corrected in the same `smeldr/architect` commit
- `smeldr/common/agent/skills/smeldr.md` version line and stale path references to the
  renamed context file corrected in the same task, committed in `smeldr/common`
- Classified Level 1 (not Level 0): the rename changes operative documentation across
  three repos, and "when did the role names change" is a fact lineage should be able to
  answer
- `CLAUDE.md`'s commit-approval language now consistently routes through the
  `commit-ready`/`commit-approved` signal pair instead of a chat-only "yes"; the
  push-permission self-contradiction between the docs-workflow and Branching sections
  is resolved in favor of the Branching section's rule

Level 1 amendment.

---

## A219 — Reachability as a general platform primitive (T153)

### What

`reachability.go` (new file): `RelationStore.Reachability(ctx, anchorType, anchorID,
kind, direction string, maxDepth int) (*Reachability, error)` — a general-purpose
bounded breadth-first traversal of the relation graph outward from any anchor
(type, id), reporting which items are found at each hop distance from 1 to `maxDepth`.
Exported types: `ReachabilityItem` (Type, ID), `ReachabilityRing` (Depth, Items — a ring
with zero items is a genuine reportable absence, not an error or an omission),
`Reachability` (AnchorType, AnchorID, Kind, Direction, Rings). `MaxReachabilityDepth = 10`
is a safety-ceiling constant, confirmed by the architect against real Pulse mockup
readings (3-4 rings in practice; 10 gives generous headroom). Unexported helpers
`reachabilityNode` and `reachabilityNeighbors` reuse `RelationStore.GetBySource`/
`GetByTarget` — no new SQL, no new tables.

### Why

Found independently twice: once checking Pulse's "Reach" reading (concentric-rings
metaphor — the boundary of an entered scope, each absence extending inward one ring per
closure it holds open) against source, and again during the Design Package v1
implementability review, which named "reachability" as one of six items in a closed
derivation-grammar set (count, presence, absence, reachability, closure, elapsed time)
that must be a deterministic platform capability, not a per-instrument interpretation.
Two independent findings on the same gap. Verified against source before designing
against it: `governance.go`'s `ScopeDynamic`/`relationExists` resolves exactly one hop
(a boolean access check); `context_packet.go`'s `BuildContextPacket` does bounded
depth-1–2 traversal, but hardcoded to 5 orchestration anchor types and shaped for a
one-shot JSON export, not a general, repeatable, arbitrary-type graph-distance read.
Neither is "walk the graph outward from an anchor, N hops, report structure or absence
at each ring" as a reusable primitive — that gap is what this amendment closes.

### Design decisions

1. **Standalone primitive, not a `ScopeDynamic` extension.** `ScopeDynamic` needs a
   boolean; Pulse's Reach needs ring-structured presence/absence data — a boolean is a
   trivial derivative of ring data, not the reverse. Extending `governance.go`'s
   fail-closed, security-critical `Authorized` path to carry a richer return shape it
   doesn't need is scope creep into a sensitive file with its own review needs.
   `governance.go` is untouched by this amendment. (A later amendment could have
   `relationExists` call `Reachability(..., maxDepth=1)` to delete its own duplicated
   one-hop SQL — deliberately not done here.)
2. **New file, not an addition to `relations.go`.** Same precedent as
   `context_packet.go`: a derived, read-only computation built on `*RelationStore`,
   kept separate from the CRUD/store fundamentals.
3. **Reuses `BuildContextPacket`'s proven frontier-expansion BFS shape**, generalized:
   no hardcoded type table, arbitrary anchor type string, `seenNodes` dedup map
   (standard BFS visited-once semantics — cycles and diamonds never cause a node to be
   revisited or appear in more than one ring). Confirmed against `design/
   content-relations.md` (T06's original spike): "no off-the-shelf bounded-traversal
   pattern to copy" for SQLite, recursive CTEs explicitly rejected in favor of
   iterative bounded traversal — this amendment follows that established guidance, not
   a new pattern.
4. **Every requested depth returns a ring, even after the frontier is exhausted.**
   A ring with zero items is data, not an omission — matches "each absence extends
   inward one ring" from the product framing this primitive exists to serve.
5. **Go primitive only — no HTTP endpoint, no MCP tool.** Pulse (the only named
   consumer) is Cloud-side and does not exist yet; it owns its own data-fetching layer
   per `observation-system-host-contract.md`. Wiring a consumer-facing surface now would
   be guessing at a shape this task has no mandate to decide.

### Explicitly out of scope, by design

- **Tension's dependency on this primitive** — the design packet's one available line
  ("Tension is structural, never aged: an absence's depth is the number of closures it
  holds open") does not by itself determine whether Tension needs graph-depth
  traversal or a local count. This amendment does not presume an answer; the primitive
  is general enough to serve either outcome if Tension later needs it.
- **The full six-derivation closed set** (count, presence, absence, reachability,
  closure, elapsed time) — count/presence/absence are already free today (plain SQL);
  closure and elapsed time are unanalyzed. Formalizing a unified closed-set registry to
  match the frontend's `DerivationName` union is materially larger than this task and
  is tracked separately as `T156`.
- **`ScopeDynamic` behaviour change** — see design decision 1.

### Consequences

- New exported symbols: `ReachabilityItem`, `ReachabilityRing`, `Reachability`,
  `RelationStore.Reachability`, `MaxReachabilityDepth`. No existing exported symbol
  changed or removed.
- No new database tables, no schema migration — reads only, via the existing
  `smeldr_relations` table through `GetBySource`/`GetByTarget`.
- Per-hop query pattern is non-batched (two queries per frontier node per ring), the
  same pattern `BuildContextPacket` already uses in production. A batched `IN()`-based
  frontier query (same shape as `edges.go`'s `ContentEdgeStore.ChildrenOf`) was
  considered and deliberately deferred — no existing batched-by-(type,id) query to
  build on, and the real access pattern (dogfood-scale operational data) does not yet
  justify the added complexity.
- 15 tests in `reachability_test.go`: 4 error paths (empty anchor, invalid depth,
  invalid direction, DB error — both the `GetBySource` and `GetByTarget` branches),
  single-ring present/absent, multi-hop chain, empty-rings-continue-to-max-depth,
  kind filtering, all three direction values, cycle safety, and a cross-type traversal
  (proves the "general platform primitive" claim against `BuildContextPacket`'s
  hardcoded 5-type limitation). 100% coverage on both new functions. Package coverage:
  96.1%.
- Level 2 amendment (new exported symbols, new platform capability).

Level 2 amendment.

---
