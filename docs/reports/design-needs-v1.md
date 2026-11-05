# Design's six needs from core, answered (T1114)

Report, 5 November 2026. It answers brand's note `smeldr/brand/drafts/design-needs-for-core-v1.md` (T1113, commit 78b96f6). Core read at main 62fde15 (core v1.189.3), MCP at smeldr.dev/mcp v1.75.1. Nothing here is built; architect turns the "to build" rows into Tasks after the D157 review of design v1.

## How to read the verdicts

- **Exists as drawn:** core returns it today; the tool, call and field are named.
- **To build:** core can return it but does not yet. Size S, M or L, and the Decision the work touches or would need.
- **Cannot be built as drawn:** a ratified Decision or a model guarantee rules it out. The Decision is named.

Every answer is under the reader's credential. Core applies the read rules before it returns or counts anything, so the screen never receives what the reader may not read. Where the credential changes the shape of an answer, the row says so.

## 1. Port's search

Core: `App.Search` (`search.go:129`), MCP tool `search`. Matches plain text, case-insensitive, in each type's searchable fields: a compiled type's fields tagged `smeldr_search:"text"`, a runtime type's fields with `Search` set, or its title, description, body and summary fields. Order is newest first (`updated_at`). The MCP tool adds `awaits_action` per hit, and `state_since` and `state_reason` for a Task or Goal.

| Need | Verdict | Size | Decision |
|---|---|---|---|
| `address`, `type`, `title`, `state` per result | Exists as drawn. `SearchHit` (`search.go:81`). `title` is never empty: it falls back to the address, then the slug. If the screen needs to tell "no title" from a title, that is S. | - | - |
| total found | Exists as drawn: `total`, counted after the read rules. | - | - |
| search from one character | To build. `SearchTextMinLength` is 2 (`search.go:24`), chosen in T885 so that one letter does not scan everything. Lowering it is S, but it changes that guard, and every search is a live scan (see the index row). | S | T885's guard; needs architect's word |
| match on the address and on `external_ref` | To build. Only text fields are matched, so an address is found only where it also appears in a text field, and `external_ref` is never matched. | S to M | - |
| `external_ref` per result | To build. Not in `SearchHit`. | S | - |
| match reason (whole address, start of address, start of a word in the title, anywhere in the title, reference) | To build. Core returns the matched `field` and a `snippet`, not a reason. If core ranks by reason, the ranking comes with it. | M | - |
| the open condition about the result that awaits an act, if readable | To build. Not on the hit. Core has the data (open condition Signals with an `about` subject), and the reader's scope applies per Signal. | M | - |
| read time of the index and the index state (complete, incomplete with the types left out, not built, could not be read) | To build. Core has no search index today. Every search scans the live tables under the reader's scope, and a failed read fails the whole search (`ErrInternal`), never a partial result. Until an index exists, the honest state is "complete, live" and there is no read time. Whether an index is wanted is for architect and Peter. | L | needs its own Decision |

Credential: a record the reader may not read is not a hit and does not change `total`. A `place` filter naming a place the reader may not read answers not found, the same as a place that does not exist.

## 2. Pulse "Knots": the count per open item

Core (D150): live `App.RestCounts` (`rest_counts.go:63`, MCP `get_rest_counts`) and stored `App.StoredRestCounts` (`rest_counts_stored.go:460`, MCP `list_rest_counts`). The walk follows `grounded_in`, `addresses` and `amends` (`RestRelationKinds`, `rest_counts.go:17`), transitively, each item once, over live edges only, in both directions (`rests_on_this`, `this_rests_on`).

| Need | Verdict | Size | Decision |
|---|---|---|---|
| the relations followed, to any depth, each item once, live edges only | Exists as drawn. The same three kinds as the drawing. | - | - |
| stored count per item with the time of the sweep | Exists as drawn. `counted_at` is the end of the sweep that stored it. At most 200 items per call (`MaxStoredRestCountItems`). | - | D150 |
| a stored count never higher than the live one | Exists as drawn. Summed only over the place buckets the reader's place grants cover (D150 point 4). | - | D150 |
| the split into ratified, open and other closed | To build. Core splits into `open` and `closed` only (`RestCount`, `rest_counts.go:31`). "Open" is the state awaiting action (D109). A third bucket needs a stored column and a sweep change. | M | D150, D109 |
| a live count with a limit the caller sets (the screen uses 200), and whether it was hit | To build. The limit is a fixed 1000 (`RestCountLimit`, `rest_counts.go:22`, D35 guard 2); hitting it sets `at_least`. A caller limit at or below 1000 is S. | S | D35 guard 2 |
| the addresses in the live count | To build. The live count returns numbers only. Returning the addresses (paged, each one readable by the reader) is M. | M | D150 |
| the open items the reader may read, with type, state, open since, place and who it is addressed to | To build, as one list. Today each type is listed on its own with `awaits_action` (`list_items_by_state`, `search` with states). `state_since` exists for Task and Goal only (MCP), the place is a relation, and "addressed to" is a field of Signals only. A cross-type list with these fields is M. | M | - |

Credential: an item the reader may not read is neither counted nor walked through, so a count never reveals a hidden item. An item the reader may not read is left out of a stored-count answer.

## 3. The premise mark

Core: the condition `premise-flagged` (D149, as amended by D153), opened and resolved by the system (`premise_flags.go`). One condition per pair of resting Decision and premise. Its subject is the resting Decision, it is placed in that Decision's place, and it is resolved when the premise is in force again, the `grounded_in` edge ends, the resting Decision leaves force, or a ratify holder confirms it with `confirm_premise`.

| Need | Verdict | Size | Decision |
|---|---|---|---|
| the open `premise-flagged` Signals the reader may read | Exists as drawn: `list_signals` with that type; each Signal is read under its place. | - | D149 |
| `address`, `kind`, `state`, `subject` (the item that carries the mark), the Signal's place | Exists as drawn: Signal `address`, `signal_type`, `status`, `subject_type` and `subject_id` (the resting Decision), the `belongs_to_*` edge. | - | D149 |
| one Signal per link with the problem, none further away | Exists as drawn: one per (Decision, premise) pair, only for a direct `grounded_in` edge. | - | D149 |
| the premise relation `addresses` | **Cannot be built as drawn.** D153 makes `grounded_in` the only premise relation. An `addresses` edge never opens the mark, and conditions opened from one before D153 resolve. The screen should show `grounded_in` ("rests on") only. | - | D153 |
| the premise's address on the Signal for every reader | **Cannot be built as drawn.** The Signal names only the resting Decision (`premiseMessage`, `premise_flags.go:66`), so a reader who may not read the premise does not learn it. This is D149's read guarantee. | - | D149 |
| the premise (address, relation, state, whether it has a place) for a reader who may read it | To build. The pair is stored (`smeldr_premise_flags`), but no read returns it. A read that adds the premise when the reader may read it is S to M. | S to M | D149 |
| the answers (who, when, reason) | To build, mapped. A condition has no answers; it is resolved. Who resolved it and why are in its resolution text and provenance. A `confirm_premise` stores who confirmed, when and why on the pair, and no read returns that today. Returning it with the premise read above is part of the same work. | S | D149 |

## 4. Who may approve changes in a place

Core: `App.ApproversOf` (`approvers.go:51`, D147), MCP `approvers_of`. The place is an Organization, Domain or Area, named by address, slug or ID. It returns the approver holders (anchored on the Organization), the domain-approver holders of the place's Domain and, for an Area, its area-approver holders. Only live grants and holders with a live token are listed.

| Need | Verdict | Size | Decision |
|---|---|---|---|
| per holder: name, class (person, agent, job), role, where it is anchored | Exists as drawn: `PlaceApprover` (`approvers.go:17`) has `name`, `class` (`human`, `agent`, `job`, or `unclassified` for an old token), `role` (`approver`, `domain-approver`, `area-approver`) and `anchor_address`. | - | D147 |
| live grants only | Exists as drawn. | - | D147 |
| "empty" and "hidden from you" as two answers | Exists as drawn. A place the reader may not read, or that does not exist, is an error (not found, problem type `place.not_found`). A readable place with no holders is an empty list `[]`. The screen shows "Who decides here is not shown to you yet." on the error and "No one approves changes here yet." on `[]`. | - | D147 |
| who may approve changes, and who may ratify | Exists, derived from the role. `approver` holds ratify and approve-change; `domain-approver` holds ratify and approve-change; `area-approver` holds approve-change only (`orchestration.go:430-462`). So the ratify list is the first two roles. If the screen wants the operations spelled out per holder, that is S. | - (S to spell out) | D147 |
| whether the reader holds it | To build. Not in `PlaceApprover`. | S | D147 |

Credential: a holder is listed even when the reader may not read the place its grant is anchored on. Then `anchor_address` is empty. The drawing needs a form for that case, for example "anchored above this place".

## 5. The sources of a Decision

Core: `Decision.Sources` (`orchestration.go:361`, `decision_sources.go`, D148).

| Need | Verdict | Size | Decision |
|---|---|---|---|
| `sources` on every Decision, in one shape, an empty list when none | Exists as drawn. Written as `[]`, never `null` (`decision_sources.go:103`). At most 20 entries. | - | D148 |
| entry `{repository, commit, path, anchor (optional), role, hash (not shown)}` | Exists as drawn. `role` is `source`, `evidence` or `implementation`. `hash` is optional (`sha256:` and 64 hex). | - | D148 |
| an entry without `commit`, printed without a link | **Cannot be built as drawn.** D148 requires `commit` to be a full 40-character SHA, never a branch, so every entry has one. The screen may drop that case. | - | D148 |

## 6. Screen words for data names

The names core uses today. "Not in Design's list" means the screen will print it raw. That is allowed, but it is listed so Design can decide.

| Group | Core's names | Against Design's list |
|---|---|---|
| Check state (`ChangeCheck`) | `checking`, `commented`, `awaiting-approval`, `approved`, `not-approved`, `superseded` | `awaiting-approval` and `not-approved` match. `checking`, `commented`, `approved` and `superseded` are not in the list. |
| Area verdict | `commented`, `awaiting-approval`, `approved`, `not-approved`, `awaiting-audit` | `awaiting-approval`, `not-approved` and `awaiting-audit` match. |
| Signal kind | conditions: `ratification-required`, `authorization-required`, `conflict-detected`, `downstream-blocked`, `reference-stale`, `premise-flagged`, `coverage-matches-nothing`, `merge-unapproved`; requests: `review-requested`, `question` (`signal_kinds.go:37`). Older history types (`notice`, `plan-ready` and the rest) are still readable. | **`audit-required` does not exist in core and never shipped.** The audit condition is `merge-unapproved`. The screen should drop `audit-required`. Not in the list: `authorization-required`, `downstream-blocked` and `reference-stale` (the last two have no emitter yet). |
| Approval mode | `comment-only`, `requires-approval`, `audit-after-merge`. An Area with no mode is read as `comment-only` (`places.go:328`). | Matches, including the missing-mode rule. |
| Relation | Every kind has `label`. Most also have `reverse_label`. From `list_relation_kinds`: `grounded_in` "Grounded In" / "Grounds", `refers_to` "Refers To" / "Referred To By", `about` "About" / "Subject Of". `contradicts` (not directional), `depends_on` and `in_set` have no `reverse_label`. | **`relates_to` is not a core relation kind.** The nearest are `refers_to` and `about`. Design should name which one "Related items" reads, or core adds `relates_to` (S, a seeded kind). The screen needs a fallback for a missing `reverse_label`. |
| Actor class | `human`, `agent`, `job`, and `unclassified` for an identity migrated from an old token (`identity.go:30`) | `unclassified` is not in the list. |
| Source role | `source`, `evidence`, `implementation` | Matches. |

## To build, all points

| # | Item | Size | Decision |
|---|---|---|---|
| 1 | Search from one character | S | T885's guard |
| 1 | Match on address and `external_ref` | S to M | - |
| 1 | `external_ref` per hit | S | - |
| 1 | Match reason per hit (and ranking by it) | M | - |
| 1 | Open condition per hit | M | - |
| 1 | A search index, with read time and index state | L | its own Decision |
| 2 | Split ratified / open / other closed | M | D150, D109 |
| 2 | Caller-set limit on the live count | S | D35 guard 2 |
| 2 | Addresses in the live count | M | D150 |
| 2 | One list of open items with open since, place, addressed to | M | - |
| 3 | Premise read for a reader who may read it, with the confirmation | S to M | D149 |
| 4 | Whether the reader holds it, per holder | S | D147 |
| 4 | Operations spelled out per holder (optional; derivable from role) | S | D147 |
| 6 | `relates_to` as a kind, if Design keeps the name | S | - |

## What the drawing should change

- Search: show the index line as "complete, live" with no read time until an index exists, or leave it out (point 1).
- Premise mark: `grounded_in` only (D153), and the premise shown only to a reader who may read it (D149) (point 3).
- Approvers: a form for a holder whose anchor the reader cannot see (point 4).
- Sources: drop "an entry without commit" (D148) (point 5).
- Names: drop `audit-required` for `merge-unapproved`; choose `refers_to` or `about` for `relates_to`, or ask core for the kind; add `unclassified` and the check states that are not in the list; give a fallback for a missing `reverse_label` (point 6).
