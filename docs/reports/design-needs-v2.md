# Design version 1, core's assessment (D180 step 3, T1178)

Report, 11 November 2026. It answers T1178: core's own reading of the architect's build assessment (`smeldr/architect` `design/design-v1-build-assessment.md`, sections 2, 5, 7 and 9a) against the code, D181 and the locked package. It is the second version of the T1114 report (`design-needs-v1.md`), which stays as the record of what core answered on 5 November.

Read at:
- core main `9236ede` (v1.191.0, plus T1172's workflow change, which touches no read path), with T1176 (bearer tokens checked against the store on every route) in review on its branch;
- mcp v1.75.2;
- the locked package, `smeldr/brand` `cloud/design-truth-package/v1-2026-11-09/` at `1a8a096`, read with `git show`.

The package holds the states the architect named for step 1 (Port at 1280, W4 to W7, R5, S3). This report builds on none of the files brand's T1175 branch adds beside the export.

How the facts were found: by reading the code, file and line named. A fact about what a tool returns is from its source. No cost claim in section 3 rests on a measurement: each says what the query is and whether its work grows with the model, from the code. No query plan is quoted.

Every answer holds under the reader's credential: what a reader with a given scope receives, not what an admin token receives.

## 1. T1114's six points against the locked package

The package took in all six of core's answers of 5 November (`export/Open before v1.md`, "What each unconfirmed point needs from core" and "Closed for version 1"). Each point, as it stands now:

| Point | Holds? | What changed in the package |
|---|---|---|
| 1. Port's search | Holds. | The package asks from one character; Peter answered P3 (9a): one character on address and reference, two for names. The index line is "Complete, live · N entries"; the other index states are drawn for a future index only. The ranking reasons are kept "if core returns them". |
| 2. Pulse's rest counts | Holds, with one name corrected. | The package asks for the split, a caller's limit of 200 and the addresses, as core answered. **Correction to my 5 November answer:** the field that says a count was cut is `at_least` on the wire (`rest_counts.go:36`); `capped` is only the stored column. |
| 3. The premise mark | Holds. | `grounded_in` only (D153) and the premise only to a reader who may read it (D149), both taken in. |
| 4. Who may approve | Holds. | The empty-anchor holder is drawn ("for a place above this one, which you cannot read"). "Whether the reader holds it" is still asked. The holder's name is the identity's name when the holder has one, else its newest live token's name, and a holder with no live token is left out (`approvers.go:46-47`, `254-262`). |
| 5. Sources | Holds. | "An entry without commit" is gone. `sources` exists on Decision only (`orchestration.go:361`); the package draws it on Decisions only. |
| 6. Screen words | Holds. | `audit-required` dropped for `merge-unapproved`; `relates_to` read as `refers_to`; `unclassified` added; a missing `reverse_label` reads "Linked items ({label})". |

## 2. The model map's "exists" rows (assessment section 5)

Confirmed rows are listed briefly; corrected and partly-true rows say what is true. Rows marked "to build" are not re-checked, except where the fact turned out to exist already.

### 2.1 Corrections

| Row (section) | What is true |
|---|---|
| Admin holders, "class person only" (5.1) | `administrators` (D154, `administrators.go:35-79`) returns `{name, class}` for every holder of a live global grant whose role holds `administer`, **of any class**. There is no person filter. The class word is `human`, not "person". The filter "persons only" is Cloud's to apply, or a parameter core adds (S). |
| A Signal row's address (5.2) | `get_signal` returns the address; **`list_signals` does not** (`mcp/signal_tools.go:277-288`). |
| A Signal's class, condition or request (5.2) | **Not a field, and no surface returns it.** It is derived in Go from `signal_type` (`SignalTypeOf`, `signal_kinds.go:37-48`). J must return it. |
| The other Area's answer: who, when, why (5.2, 5.5) | Stored in `smeldr_change_check_areas` (`answered_by`, `answered_at`, `reason`, `change_check.go:142-144`) and written (`change_check_approval.go:377`, `change_check_audit.go:187`), but **never read back**: the only read selects `approval_mode, verdict` (`change_check.go:646`). A read change (S). `answered_by` is an actor ID; the name and class need the directory (section 5). |
| "{addr} and {addr} already contradict, entered {time} by {who}" (5.2) | **No such refusal.** Asserting a live relation again updates the row and succeeds (`mcp/relation_tools.go:23-25`), and the re-assert **overwrites** `created_by`. The only "already exists" refusal, `relation_already_exists` (409), is a race between two writers, with the row ID as its only value (`problem_types.go:292`, `497-498`). See section 7. |
| Standing with its start or end (5.5) | `get_item_standing` returns `holds`, `ceased` or `none` only (`mcp/standing_tools.go:155`). `smeldr_standing.updated_at` is written (`standing.go:123`) and never returned; the times are in provenance as `standing-began` and `standing-ended`. A read change (S). |
| The thread's other end's address (5.5) | `get_relations` returns raw IDs (`source_id`, `target_id`), **not addresses** (`mcp/relation_tools.go:478-507`), every row live and ended, in creation order, with **no live-only filter and no paging**. The stream's relation events do carry `source_address` and `target_address` (`webhook.go:527-548`). |
| "Merged without approval" from the Check's state (5.5) | There is **no merged state** on a Check (`change_check.go:59-72`). `record_merge` opens a `merge-unapproved` condition **only for an `audit-after-merge` Area**, whose verdict becomes `awaiting-audit`; the Check's state does not change (`change_check_audit.go:18-30`). A `requires-approval` Area merged without approval leaves no mark. The package's W5 example (CHK8) is in an `audit-after-merge` Area, so W5 can be built. |
| Places: "title" (5.4) | Organization, Domain and Area have `name` and `description`, not `title` (`places.go:27-65`). The Organization's flow has `active` only; Domain and Area can be archived (`places.go:119`). |
| "Show what is scanned": the sources, when each ran, scanned and found (5.2) | `get_sweep_run` takes one detector (required) and returns its **latest run only**: `detector, wired, ran_at, walked, flagged` (`mcp/sweep_run_tools.go:20-68`). There is no list of detectors and no history over MCP or HTTP; `skipped` and `err` are stored and not returned (`sweep_run.go:10-26`). A list of the detectors with their latest runs is S. |
| "for {Area}" on a rule that matches nothing (5.2) | `coverage-matches-nothing` has **no Area edge**; its subject is the CoverageRule, whose `area` field names the Area (`coverage.go:881-888`). `merge-unapproved` and the approval request do have a `belongs_to_area` edge. |
| "≥ n" from `capped` (5.3) | The wire name is `at_least` (see section 1, point 2). |
| The Delegate write (5.2) | `delegate_item` takes `token_id` (a token's user ID, not an identity) and `expires_in_days` (days, not a date) (`mcp/delegate_tools.go:38`). Delegation is per token today. See section 5. |

### 2.2 Confirmed

The live line: the stream with `?after=`, `skipped`, `caught_up` and `ping` (`eventstream.go:469-705`); `?after=` needs the event log on (`event_log_not_enabled` otherwise). Port: address, type, title, state per hit (`search.go:81-94`), and an unreadable record is no hit and changes no total. Receiver as a routing name or an operation. A Signal's subject through `about` and `subject_type`/`subject_id`. Coverage rule fields `area, repository, pattern, match_count, checked_at` (`coverage.go:23-44`). The Check's `repository, number, head_commit, files_total, files_checked, checked_partly, setting`, and `checked_against` returned as `decisions` filtered by what the reader may read (`change_check.go:632-665`). `approvers_of`: name, role, class, anchor, ordered by level then name; empty list against not found (`approvers.go:17-135`). The writes `answer_change_check`, `confirm_premise`, `answer_signal`, `assert_relation`. Problems: `id, class, rule, values` on both doors (`errors.go:466-482`, `mcp/tool.go:403-425`); a bare 401, 403 or 404 carries no `values`. Stored rest counts both ways, summed over the readable places, at most 200 items a call (`rest_counts_stored.go:52-63`, `460-515`); `counted_at` is the sweep's end; the sweep runs only with `ENABLE_STRUCTURAL_SWEEP`, on `STRUCTURAL_SWEEP_SCHEDULE` (default hourly). `in_set` as a relation (no reverse label). D109's `awaits_action` per state (`state.go:167`). Approval mode, defaulting to `comment-only` (`places.go:64`, `322-333`). `external_ref` on every addressed record, and matched when an address, slug or ID is resolved (`address_resolve.go:92`). Provenance's entry fields `timestamp, verb, from_state, to_state, gated, actor_kind, actor_id, surface, reason`; `gated` is computed at read time from the current flow. A Check's Areas with their approval mode as stored when the check ran (better than marked: it exists today). An unreadable address answers exactly like a missing one on MCP `get_*`, the record routes and dynamic types (`module.go:1980-1998`, `2312-2366`; `dynamic.go:1376`).

### 2.3 Facts the map does not list

- **There is no HTTP search route**, no HTTP route for `administrators`, and none for identities.
- **A title** exists on Decision, Task and Goal only. An Amendment's is `summary`, a place's is `name` (`record_title.go:33-36`). A compiled record's Node fields have no JSON tags (`Status`, `ID`).
- **`/packet`** serves six types only (Goal, Decision, Amendment, Task, Signal, Run), at most 25 items a type (`context_packet.go`).

## 3. The reads A to N

Today, **no read applies the reader's scope in SQL.** The scope is loaded once per request into maps (`read_scope.go:16-46`, `112-171`), and every list loads its rows and filters them in Go: `list_<type>` reads the whole table with no `LIMIT` (`storage.go:892-921`) and pages in memory; `list_items_by_state` does not page at all; `list_content`, `list_signals` and `list_relations` page in SQL for an unscoped reader and load everything for a scoped one; `search` runs one `LIKE` per type with no `LIMIT`; provenance loads an item's whole history. The scope's own anchor queries filter `WHERE target_id = $1` without `target_type` (`read_scope.go:196`, `201`), and both relation indexes lead with the type column. So D181 point 3 (item 3 below) is what every read in this section waits on, and the reads' verdicts assume it is built.

Indexes today that the reads can use: relations by `(source_type, source_id, relation_kind)` and `(target_type, target_id, relation_kind)`; provenance by `(subject_type, subject_id)`; open Signals by `(subject_type, subject_id)`. Missing: a state index on any record table or on `smeldr_dynamic_content`; `smeldr_signals (receiver, status)`; `smeldr_tokens (user_id)`; provenance with its time.

| Read | Verdict | Shape, scope, cost |
|---|---|---|
| A me | Agree, with one shape point. | Its work is the caller's own grants, never the model. "The places I read" is not always a list of places: a static grant names items, and a type grant names types. A returns the scope in its own terms (everything; these types; these places; these items as a count), and says which places I may change who decides in. Needs the identity read of the caller (section 5). |
| B places | Agree. | The tree is small (places, not items). Its counts are E's and need the stored placement. "Unplaced" must be a value the query can filter on: the stored placement has an explicit "none". |
| C a place | Agree. | One place, its coverage rules (by `area`), its approvers (L) and K's open count. Its cost is that place's rules and grants. |
| D records | Agree. | Needs the stored placement and the scope as a predicate, an index `(type, status, updated_at, id)` per table and on `smeldr_dynamic_content` with `type_name` first, paging by key, and row columns without the body. For a runtime-defined type, the dynamic table carries the same placement columns. |
| E counts | **Agree on shape; the cost rule cannot be met as written.** | A `COUNT` under a predicate reads every counted row, even from an index alone, so its work grows with what it counts, which is the model. Two ways to meet the rule: stored counters per (place, type, state), kept in the write's own transaction; or an index-only count with a stated ceiling, above which it answers "at least n", as the rest counts do. I propose the second for version 1: it needs no new write path and matches a shape the screens already have. It needs the architect's word, and is a Decision if it is a rule for every count. |
| F a record | Agree. | All parts exist or are S: standing's time (section 2.1), the Check's per-Area answers (2.1), the place path, the create and ratify entries from provenance (a query by subject and verb with `LIMIT`, not the whole history), the open conditions (index exists), the acts (needs item 2). The relation counts by kind are a `GROUP BY` over that item's edges: bounded by the item, not the model, except for a place, where they are the place's members; a place's counts come from E. |
| G relations | Agree. | Missing today: live only, paging (by `(created_at, id)`, so the index needs those columns after the kind), the other end's address, row and place, and the scope on the other end as a predicate. The other end's type varies per row, so its row is read per type in a batch. |
| H history | Agree. | Needs the index `(subject_type, subject_id, timestamp, id)` with the limit in the query, filters by verb and actor, and names and classes through the directory. **H can carry "Relation added" entries**: two limited queries (provenance and the item's relation rows by `created_at` and `invalid_at`) merged by time in one read. Core does it, so the paging is one cursor. |
| I open items | **Agree on shape; as a live query across types it does not meet the rule.** | Sorting every open item across all types by a stored rest count is a join and a sort over every open item: no single index serves it. What meets it: a stored projection, one row per open item (type, id, place, state since, addressee, the two rest totals), written in the same transaction as the item's transition and checked by the structural sweep, indexed per sort order. "Addressed to" for a non-Signal item becomes core's rule, written into that row. This is a new table and a new derived fact: I propose a Decision for it. |
| J what waits for me | Agree, with two additions. | "What I can act on" must be a predicate, not a per-Signal check in Go: receiver equal to one of my routing names, or an operation I hold over the subject's place (which is a place predicate once the placement is stored). Needs the index `(status, receiver)`. Two facts J needs that core does not store: **the identity that created a Signal** (no column today; only provenance's create entry has the actor, `orchestration.go:200-256`), and the class, derived in Go (section 2.1). |
| K pull requests of a place | Agree. | Checks reach an Area through `smeldr_change_check_areas`, whose key is `(check_id, area_id)`; K needs an index `(area_id, check_id)`. The newest non-superseded check per pull request can use the existing `(repository, number, status)` index. The counts by state and by change time are E's rule. |
| L approvers, administrators | Agree. | Add "whether the reader holds it" (S), and, for administrators, a class parameter if the Frame wants persons only. Its cost is grants, not items. |
| M rest counts | Agree. | The split is a stored column and a sweep change (M); the caller's limit, at or below the fixed 1000, is S; the addresses, paged and each readable, M. |
| N search | **The narrow form meets the rule; the text form cannot without an index.** | One character on address and `external_ref` is an index lookup: a prefix match on the unique address index, plus an index on `external_ref` (none today). On PostgreSQL a prefix `LIKE` uses an index only with a pattern operator class or the C collation; the DDL says so. The text search is a scan of every searchable column; its work grows with the model whatever the limit. Under P3 (9a) that stays until an index has its own Decision. |

## 4. Sizes and order for the backend list, items 2 to 16

My sizes. "Decision" names what the item touches, and "new" marks where one is needed.

**Foundation:**

| # | Item | Size | Decisions | Notes |
|---|---|---|---|---|
| 2 | Authorization in the application layer: one registry of operations, the gate moved out of the MCP module and the record routes, item-scoped grants on both doors, the parity test | L | D181, D105 | Split: 2a the registry and the move, 2b the parity test over the matrix. 2b depends on 2a. |
| 3 | The scope in the query | L, split | D181 | 3a **stored placement** on every compiled table and on `smeldr_dynamic_content`, written with the placement relation, with an explicit "none", checked by the structural sweep (M to L). 3b **the scope as a predicate**: `read_scope.go` returns a predicate instead of maps, and every list and count uses it; the anchor queries gain the type column (L). 3c **"in this state since" stored** on every type, written with the transition, backfilled from provenance (M). 3d **the indexes and row columns** (M). 3a, then 3b; 3c and 3d beside them. |
| 4 | The seeded 10,000-item model and the query-plan test | M | D181 | On SQLite in the suite, and on PostgreSQL in pgxcheck: the plan test means something only on the database that serves. |
| 5 | A limit per identity, with page and body ceilings | M | D181, D165 | Keyed on the identity, so needs T1176's stored user. |

**Read side** (each with its query-plan test, after 3):

| # | Item | Size | Decisions | Notes |
|---|---|---|---|---|
| 6 | D records and E counts | M | D181; **new** if the counting rule (section 3, E) is general | |
| 7 | B places and C a place | M | D181 | |
| 8 | F, G, H | F M, G M, H M | D181, D165, D100, D148 | F includes standing's time and the Check's per-Area answers (S each). H includes relation entries. |
| 9 | J what waits for me | L | D181, D115, D142 | Plus **the Signal's creator** (a column written at create, S, with a backfill from provenance). |
| 10 | I open items | L | D181, D109, D150; **new** for the projection | The projection table (section 3, I). |
| 11 | K pull requests of a place | M | D181, D143 | |
| 12 | A me | S to M | D181, D154, D165 | After the identity read (section 5). |
| 13 | The additions: L's "holds it" (S); M's split (M), limit (S) and addresses (M); N's narrow form (M); the premise read with its confirmation (S to M); plus `list_signals`'s address and a Signal's class on every Signal read (S); the sweep runs list (S) | S each unless named | D147, D150, D35, D149 | Can go before the foundation: none needs the stored placement. |

**Write side:**

| # | Item | Size | Decisions | Notes |
|---|---|---|---|---|
| 14 | Decline, supersede, retire, place-several as single commands | M | D181 | Place-several reports each record. |
| 15 | The idempotency key on creates | M | D181 | |
| 16 | MCP's internal error without the Go error text | S | T1039 | Today some MCP handlers return `"internal error: " + err.Error()` (for example `mcp/signal_tools.go:275`). |

**From section 5, the identity directory:** 17a a public batch read of names and classes by actor ID, used by H, J, F and the lists (S); 17b list the persons of the organization, MCP and HTTP (S to M, plus the rule in section 5); 17c the caller's own identity for A (S).

**Proposed Tasks, in order:** 13 (now, independent); 3a; 2a; 3b with 3d; 3c; 4; 2b; 17a to 17c; 5; 6; 7; 8; 9; 10 (after its Decision); 11; 12; 14; 15; 16 (any time).

## 5. The identity directory (D165, G31)

**Built at main:**
- The table `smeldr_identities`: `id, name, class, status, attested_by, operator_id, purpose, contact, rev`, with an address (`identity.go:72-104`). `name` is the display name. `smeldr_tokens.identity_id` links a token to its identity.
- `App.Identity(ctx, ref)`: one identity by ID or address, to any authenticated caller, with `contact` blanked for a non-admin (`identity.go:150-166`).
- Names and classes from the identity already reach `approvers_of`, `administrators` and `list_tokens`, and authorization takes the class from the identity.
- With T1176 (in review): on every HTTP route, a bearer token's user carries the identity's class and its token ID, as on MCP.

**Not built:**
- **No list.** No function, MCP tool or route lists identities. So there is no persons list for Delegate.
- **No organization link.** An identity has no organization, and an instance has one Organization (`places.go:20-25`, `92`). For the persons list, I propose the rule "the persons of the organization are the instance's active `human` identities": no new column for version 1.
- **No batch read** of names and classes. `identitiesOf` exists but is unexported (`identity.go:438-470`). `lookup_token_names` returns names without class, and only for IDs with a token row.
- **Provenance** carries `actor_kind` and `actor_id` only.
- **A Signal's creator** is not stored (section 3, J).
- **No seat read.** Nothing returns the caller's own name, organization and class.
- **Delegation is per token**, not per identity (`delegate_item` takes `token_id`). D165 gives one identity several tokens over time. A delegation to a person should name the identity, and the grant should follow it across token renewal. That changes `delegate_item`'s input, which is a D97 contract change (M, with a deprecation period).

## 6. Who may end a placement ("Undo")

From the code (`withdraw_relation`, `relation_history.go:219-313`, `places.go:344-505`):
- **Ending a placement needs a reason and the edge's `rev`.** A reason is required for every withdrawal. A stale `rev` is refused on a placement edge.
- **For a proposed Decision, any actor holding `archive` who can read the edge may end it:** Author, Editor or Admin, of any class. That is the same authority that placed it: placing a proposed Decision is not gated either.
- **For a Decision that is not proposed** (ratified, or past it), only a **person** holding `ratify` over the place it leaves may end it. An Organization, Domain or Area approver counts. An agent or a job is always refused, with `place.authority_required` (403, act `unplace`).
- **The placer is not consulted.** Having placed it gives no right of its own.
- **The Area's approval mode plays no part.** It is read only by change checks.
- **A ratified Decision can be left without any place.** "Needs a place" is checked only when it is ratified and when its sources change.

So "Undo" right after placing a Decision in W2 (still proposed) is open to whoever could place it. If it was ratified in between, Undo needs a person with ratify over that place, and is refused to everyone else.

## 7. What cannot be built as drawn

- **"{addr} and {addr} already contradict, entered {time} by {who}"** (the assert refusal in W9). As the model stands, a second `contradicts` between the same pair is not refused: the assert updates the existing row and writes the new caller as `created_by`, so "who entered it" is lost. It can be built (S to M): refuse a re-assert of a live edge with a problem that carries the existing edge's address pair, `created_at` and creator, and stop overwriting `created_by`. That changes what `assert_relation` does today for every kind, so it is a contract change for agents that re-assert on purpose. **Peter's decision**, through the architect, or the drawing shows only that the relation already exists.
- **"Merged without approval" for a `requires-approval` Area.** Not drawn in the package (W5 is an `audit-after-merge` Area), so nothing to change. Stated so that no later state assumes it: core records an unapproved merge only for `audit-after-merge`.
- **E and I as live reads under the cost rule** (section 3). They can be built as drawn, but only with the counting ceiling and the projection named in section 3; the drawing itself does not change.
- From the architect's list and confirmed: Pulse "Arriving" and the "partial read" of a whole page have no source; Port's index states have no source until an index exists.

Nothing else in the four instruments, the Frame or Port contradicts a guarantee of the model.
