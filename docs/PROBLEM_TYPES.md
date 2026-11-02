# Problem types

<!-- Generated from problem_types.go by: go test -run TestProblemTypesDoc -update. Do not edit by hand. -->

Every refusal and validation error Smeldr core answers with carries a stable **type** (D171, T1039): an id such as `decision.place_missing` that never changes when the English message does. A UI keys its own text on the id; core's message stays the API's own words.

- Over HTTP the JSON error envelope carries `type` (the RFC 9457 URI `https://smeldr.dev/problems/<id>`), `id`, `class`, `rule` and `values` for a typed error, and each field its own `type`, `id` and `values`; a request with `Accept: application/problem+json` gets an RFC 9457 problem+json body with the same members, where an error with no type is `about:blank`.
- In Go, `smeldr.ProblemTypeOf(err)` returns the id, `smeldr.ProblemValuesOf(err)` the values, and `smeldr.ProblemTypes()` the list below.
- **Class** says where a UI shows it (D174): `refusal` (a rule says no), `correction` (fix the input, at the field), `conflict` (the record moved: read it again) or `failure` (outside the caller's reach). A UI with no text yet for an id shows its class's shared text.
- **Values** are the named values a type carries, for a UI to fill its own text: each a string (a record as its address, a number in decimal, a list joined with ", "). A type lists every name its refusals use; one refusal sends those its message states, so a UI's text must read without any one of them. A value never says more than the message does, and never carries an item's title or text. A plain field's own name is in the field entry, not in its values.
- **Bold** marks a value every refusal of the type sends (`ProblemTypeInfo.AlwaysValues`): a UI's text may rely on it. A test holds the list to what every site writes.
- Over HTTP and MCP, values are sent only where the message shown carries the detail (T1066): a 401, 403 or 404 without a reason sends the type, id, class and rule but no values, since its message hides what could name an item the caller may not read. So values are absent as a whole, or hold every bold one. Go code reads them all with `smeldr.ProblemValuesOf`.
- An id or a value name is never renamed or reused. A value name may be added to a type. A type that goes out of use is retired: it stays in this list with the version it was retired in, and what replaced it.
- Audience: `person` is met in a UI; `developer` means a program called the API wrongly; `operator` means a migration or setup command.

| Id | Class | Status | Audience | Values | Rule | When | Retired |
|---|---|---|---|---|---|---|---|
| `ratify.automated_class` | refusal | 403 | person | **`token_class`**, `act`, `place` | D142 pt 5, T884 (places path: D145, D146) | An agent or job token tries a transition gated on ratify, or an act needing ratify over a place (placing/moving a Decision or Area) |  |
| `grant.agent_grants_approval_role` | refusal | 403 | person | **`role`**, **`operation`** | D142, D156 pt 3, T984 | An agent grantor grants a role that holds ratify or approve-change |  |
| `grant.automated_holds_ratify` | refusal | 403 | person | **`token_class`**, **`role`** | D142, T971 | A grant would give an agent/job token a role holding ratify |  |
| `grant.automated_approve_change_not_area` | refusal | 403 | person | **`token_class`**, **`role`**, **`suggested_role`** | D142, T971 | An agent/job token is granted approve-change by a role that is not an Area role anchored on an Area |  |
| `grant.automated_needs_human_grantor` | refusal | 403 | person | **`token_class`**, **`role`**, `grantor_class` | D142, T971 | An agent/job token's approval-role grant has no known grantor, or the grantor is not a human token |  |
| `grant.grantor_lacks_area_authority` | refusal | 403 | person | **`area`**, `token_class` | T971, D144 | The grantor holds no approve-change over the Area it tries to delegate (Area role, or item-change-approver for a Check's Area) |  |
| `grant.authority_unreadable` | failure | 403 | person | `grantee`, `grantor`, `token_class`, `role`, `area` | D142, T971 | A grant check fails closed because the grantee/grantor class, the anchor, or the grantor's authority/Areas cannot be read |  |
| `grant.job_never_grants` | refusal | 403 | person | **`role`** | D156, T986 | A job token tries to grant any role |  |
| `grant.item_approver_scope` | correction | 403 | person | **`role`** | D144 | An item-change-approver grant does not name exactly one ChangeCheck |  |
| `grant.item_approver_needs_human_grantor` | refusal | 403 | person | **`role`**, `grantor_class` | D144 | An item-change-approver grant has no known grantor, or the grantor is not human |  |
| `grant.check_has_no_approval_area` | refusal | 403 | person |  | D144 | Delegating approval of a Check that has no Area requiring approval |  |
| `token.job_never_mints` | refusal | 403 | person |  | D156, T986 | A job token tries to create a token |  |
| `token.agent_mints_automated_only` | refusal | 403 | person | **`token_class`** | D142, T984 | An agent token tries to mint a human or unclassified token |  |
| `token.identity_not_active` | refusal | 403 | person | **`identity`**, **`state`** | D165 | Minting a token for an identity whose status is not active (e.g. retired) |  |
| `token.identity_unclassified` | refusal | 403 | person | **`identity`** | D165 | Minting a token for an identity with no class |  |
| `transition.gate` | refusal | 403 | person | **`type`**, **`from`**, **`to`**, **`operation`** | D34, D40 | The actor does not hold the operation a transition requires (or the check itself failed, fail closed) |  |
| `transition.gate_unwired` | failure | 403 | person | **`from`**, **`to`**, **`operation`** | D142, T884 | A strict gated transition runs with no governance wired |  |
| `transition.no_actor` | refusal | 403 | person | **`from`**, **`to`**, **`operation`** | D34 | A strict gated transition is attempted with no actor in context |  |
| `place.authority_required` | refusal | 403 | person | **`act`**, **`operation`**, **`place`** | D145, D146 | Placing or moving a Decision/Area needs ratify over the place and the actor lacks it |  |
| `place.no_actor` | refusal | 403 | person | **`act`**, **`operation`**, **`place`** | D145, D146 | A place act needing authority runs with no actor known |  |
| `decision.scope_role` | refusal | 403 | person | **`scope`**, **`role`** |  | A Decision's scope maps to a required role the actor is not granted (or the grant lookup failed) |  |
| `decision.sources_need_place` | refusal | 403 | person | **`decision`** | D148 | Changing a Decision's sources when it has no place to hold ratify over |  |
| `change_check.core_only` | refusal | 403 | person |  | D143 | A caller tries to create or move a ChangeCheck directly instead of via check_change / core |  |
| `change_check.not_deletable` | refusal | 403 | person |  | D143 | A caller tries to delete a ChangeCheck |  |
| `change_check.no_caller` | refusal | 403 | person |  |  | check_change is called with no caller |  |
| `change_check.answer_authority` | refusal | 403 | person | **`area`**, `role` | D144, T884 | Answering a Check for an Area without approve-change over it (or item-change-approver on the Check), or with no actor |  |
| `signal.condition_system_only` | refusal | 403 | person | `signal_type` | D115 | A caller creates a condition-kind Signal, or tries to resolve a condition |  |
| `signal.answer_not_permitted` | refusal | 403 | person |  | D115 | Answering/withdrawing a Signal without capability while governance is on and no capability context exists |  |
| `plan.relations_core_owned` | correction | 403 | person |  | T1015 | A caller asserts a plan's belongs_to_area / checked_against relation by hand |  |
| `administrators.unauthenticated` | refusal | 403 | person |  |  | Asking who administers without being authenticated |  |
| `subscription.no_caller` | refusal | 403 | person |  | D116 | A subscription API is called with governance on and no caller/identity in context |  |
| `subscription.not_owner` | refusal | 403 | person |  | D116 | Creating or changing a subscription owned by another token without administer |  |
| `subscription.webhook_needs_admin` | refusal | 403 | person |  | D116 | A subscription asks for webhook delivery without administer |  |
| `subscription.seed_needs_admin` | refusal | 403 | person |  | D116 | Seeding role subscriptions without administer |  |
| `capability.denied` | refusal | 403 | person |  |  | AuthorizeCapability refuses: legacy floor not met, no user ID, capability not granted, or read scope does not reach the target type/item |  |
| `role.insufficient` | refusal | 403 | person |  |  | An HTTP endpoint's role floor is not met (Editor/Author/Admin), or an unauthenticated caller on an endpoint that answers 403 not 401 |  |
| `scope.type_not_readable` | refusal | 403 | person |  |  | The token's read scope does not cover the requested type |  |
| `write.not_permitted` | refusal | 403 | person |  |  | A Module create/update/delete fails its write-operation check (checkWriteOp) |  |
| `csrf.invalid` | refusal | 403 | person |  |  | CSRF cookie missing/empty, or header does not match it |  |
| `auth.unauthenticated` | refusal | 401 | person |  |  | An endpoint requires a valid bearer/session and none authenticates |  |
| `auth.token_invalid` | refusal | 401 | person |  |  | A signed session token is malformed, has a bad HMAC, bad payload or is expired |  |
| `auth.upload_token_invalid` | refusal | 401 | person |  |  | An upload token is malformed, has a bad HMAC or is expired |  |
| `auth.preview_token_invalid` | refusal | 404 | person |  |  | A preview token is malformed, has a bad HMAC or is expired (answered as not found on purpose) |  |
| `token.last_admin` | refusal | 409 | person |  |  | Revoking the last active admin token |  |
| `rev.conflict` | conflict | 409 | person | **`type`**, **`record`**, **`rev_sent`**, **`rev_current`** | T1010 | A write names a rev the record no longer has (*RevConflictError; SQLRepo returns the bare sentinel) |  |
| `relation.already_exists` | conflict | 409 | person | **`relation`** | T1019, A628 | A first assert lost the race to another writer for the same live relation |  |
| `relation.ended` | conflict | 409 | person | **`relation`**, **`ended_at`** |  | Re-asserting by id a relation that has already ended |  |
| `relation.already_withdrawn` | conflict | 409 | person | **`relation`**, **`ended_at`** | T1010 | Withdrawing a relation that already ended |  |
| `relation.withdraw_failed` | conflict | 409 | person | **`relation`** | T1010 | Withdrawing a relation whose row could not be updated (changed concurrently) |  |
| `transition.not_permitted` | refusal | 409 | person | **`type`**, **`from`**, **`to`** |  | The from-to transition is not in the type's flow |  |
| `transition.unknown_target` | correction | 409 | person | **`type`**, **`to`** |  | The target state does not exist in the type's flow |  |
| `state.unknown_initial` | correction | 409 | person | **`type`**, **`state`** |  | A create names an initial status that is not a state in the registered flow |  |
| `transition.singleton_state` | refusal | 409 | person | **`type`**, **`state`** |  | The type already has an item in a state that admits only one |  |
| `item.locked_state` | refusal | 409 | person | **`type`**, **`record`**, **`state`** |  | Editing (or re-slugging) an item in a locked state |  |
| `slug.published_immutable` | refusal | 409 | person |  |  | Changing the slug of a published item |  |
| `slug.taken` | correction | 409 | person | **`slug`**, `prefix`, `suggested_slug`, `type` |  | The URL/slug is already published (same prefix, or a linked type sharing the URL space) |  |
| `slug.ambiguous` | correction | 409 | person | **`slug`** |  | A slug names more than one readable item |  |
| `decision.place_missing` | refusal | 409 | person | **`decision`** | D142 pt 4, T883, D83 | Ratifying a Decision that has no place |  |
| `decision.place_exists` | refusal | 409 | person | **`decision`**, **`kind`**, **`place`** | D142 | Placing a Decision that already has a place |  |
| `decision.replacement_state` | refusal | 409 | person | **`decision`**, **`replacement`**, **`state`** | T1054 | Replacing a Decision needs a ratified replacement, and the replaced Decision at ratified or pending-re-evaluation |  |
| `places.domain_prefix_held` | refusal | 409 | person | **`prefix`** |  | Setup of places while the runtime domain type still holds the DOM prefix |  |
| `change_check.supersede_race` | conflict | 409 | person | **`check`** | T918 | A Check kept changing while being superseded |  |
| `change_check.use_answer_change_check` | correction | 409 | person |  | D144 | A Check's per-Area approval request is answered via answer_signal instead of answer_change_check |  |
| `change_check.area_not_touched` | correction | 409 | person | **`check`**, **`area`** | D144 | Answering for an Area the change does not touch |  |
| `change_check.area_comment_only` | refusal | 409 | person | **`area`**, **`check`**, **`approval_mode`** | D144 | Answering for an Area whose mode is comment-only |  |
| `change_check.not_awaiting` | conflict | 409 | person | **`check`**, **`state`** | D144 | Answering a Check that is not awaiting approval |  |
| `change_check.superseded` | conflict | 409 | person | **`check`** | D144 | Answering an audit for a superseded Check |  |
| `change_check.already_answered` | conflict | 409 | person | **`area`**, **`check`**, **`verdict`** | D144 | The Area already answered this Check |  |
| `change_check.answer_race` | conflict | 409 | person | **`area`**, **`check`** | D144 | Another answer for the same Area landed at the same moment |  |
| `premise.no_open_flag` | conflict | 409 | person | **`decision`** | D149 | Confirming a premise that has no open premise-flagged condition |  |
| `premise.already_confirmed` | conflict | 409 | person | **`decision`** | D149 | The premise was already confirmed for its current state |  |
| `premise.condition_not_open` | conflict | 409 | person | **`signal`**, `state` | D149 | The named premise-flagged condition is no longer open |  |
| `signal.request_not_resolvable` | refusal | 409 | person | **`signal_type`** | D115 | Resolving a request-kind Signal (it is answered or withdrawn) |  |
| `signal.condition_not_answerable` | refusal | 409 | person | **`signal_type`** | D115 | Answering or withdrawing a condition-kind Signal |  |
| `signal.not_open` | conflict | 409 | person | **`state`** | D115 | Answering/withdrawing a Signal that is not open |  |
| `subscription.default_fixed` | refusal | 409 | person |  | D116 | Changing the filter of a default subscription |  |
| `subscription.default_undeletable` | refusal | 409 | person |  | D116 | Deleting a default subscription |  |
| `migrate.duplicate_published_slugs` | refusal | 409 | operator | **`table`**, **`slugs`** |  | The published-slug index cannot be made because slugs are published twice |  |
| `item.not_found` | refusal | 404 | person |  | D110 (resolution order) | The record does not exist, or exists but is hidden from the caller's read scope (answered alike) |  |
| `route.not_found` | refusal | 404 | person |  |  | No page/route serves the path (aggregate, unknown dynamic type route, api-only module asked for HTML, unmatched redirect, missing sitemap, missing AI doc) |  |
| `content_type.not_found` | correction | 404 | person | `type` |  | The named content type is not registered, or is not a content (web) type |  |
| `relation.kind_unknown` | correction | 404 | person |  |  | A relation call names a relation kind that is not registered |  |
| `relation.not_found` | refusal | 404 | person | `relation` | T1010 | The relation (or a relation end) does not exist or cannot be read |  |
| `place.not_found` | correction | 404 | person | `place`, `area`, `decision` |  | The named Organization/Domain/Area (or a place edge's Decision/Area) does not exist or is not readable |  |
| `identity.not_found` | correction | 404 | person | **`identity`** | D165 | The named identity does not exist (lookup, or minting a token for it) |  |
| `role.not_found` | correction | 404 | person | **`role`** |  | The named role is not defined (get, or grant) |  |
| `grant.not_found` | refusal | 404 | person | **`grant`** |  | The grant ID does not exist |  |
| `premise.condition_not_found` | refusal | 404 | person | **`signal`** | D149 | No readable premise-flagged condition by that name |  |
| `feature.not_enabled` | failure | 404 | person | **`feature`** |  | Findings or provenance are not recorded on this instance |  |
| `redirect.gone` | refusal | 410 | person |  |  | A redirect entry with no target (content removed) |  |
| `request.malformed_body` | correction | 400 | person |  |  | The JSON body cannot be decoded, or does not fit the type's struct |  |
| `request.invalid_param` | correction | 400 | person |  |  | A query/argument is missing or out of range (time, level, limit, depth, anchor, direction, ids) |  |
| `content_type.define_failed` | correction | 400 | person |  |  | define_content_type over HTTP fails (cause logged, generic 400 returned) |  |
| `transition.status_required` | correction | 400 | person |  |  | The transition request body has no status |  |
| `transition.reason_required` | correction | 400 | person | **`from`**, **`to`** |  | A transition that requires a reason is sent without one |  |
| `type.not_registered` | correction | 400 | person | **`type`** |  | A transition/lookup names a type that is not registered |  |
| `content_type.not_runtime` | refusal | 400 | person | **`type`** |  | Redefining a type that was not defined at runtime |  |
| `slug.record_type_fixed` | refusal | 400 | person | **`type`** | D110 | Setting the slug of a record type (its address is its identity) |  |
| `addon.not_run` | failure | 400 | person | **`type`** | T881 | Reading/writing a Task/Goal/Run on an instance without the Orchestration add-on, or a type the instance keeps no records of |  |
| `capability.unknown` | correction | 400 | person | **`resource`**, **`verb`** |  | AuthorizeCapability is asked for a resource/verb pair that does not exist |  |
| `limit.paths_per_call` | correction | 400 | person | **`limit`** |  | More paths than a call accepts (check_change, decisions_for_paths, check_coverage) |  |
| `limit.rest_count_items` | correction | 400 | person | **`limit`** |  | More items than the stored rest-counts call accepts |  |
| `limit.findings` | correction | 400 | person | **`max`** |  | Findings limit outside 0..max |  |
| `subscription.owner_limit` | refusal | 400 | person | **`limit`** | D116 | The owner already holds the maximum number of subscriptions |  |
| `subscription.item_limit` | correction | 400 | person | **`limit`** | D116 | A subscription filter names more items than allowed |  |
| `subscription.role_defaults_need_band` | correction | 400 | person |  | D116 | Seeding role defaults without a band or Architect |  |
| `subscription.webhook_not_stream` | correction | 400 | person | **`record`** | D116 | Opening the stream on a subscription that delivers to a webhook |  |
| `search.query_invalid` | correction | 400 | person | `min`, `max`, `length`, `type` |  | Search has no text or filter, text length out of bounds, negative limit/offset, or an unsearchable type |  |
| `change_check.filter_required` | correction | 400 | person |  |  | Listing Checks without a repository, Area or Decision filter |  |
| `approvers.place_required` | correction | 400 | person |  |  | approvers_of is called without naming a place |  |
| `eventstream.params_conflict` | correction | 400 | person |  | D116 | ?subscription= and ?channel= combined |  |
| `eventstream.no_subscriptions` | failure | 400 | person |  | D116 | ?subscription= on an instance that keeps no subscriptions |  |
| `event_log.not_enabled` | failure | 400 | person |  | D117 | ?after= or an event page is asked for on an instance without the event log |  |
| `event_log.cursor_invalid` | correction | 400 | person |  | D117 | The cursor is not one this instance issued |  |
| `flow.standing_invalid` | correction | 400 | developer | **`flow`**, **`state`**, **`value`**, **`allowed`** |  | RegisterFlow with an illegal Standing tag |  |
| `references.signal_in_tx` | correction | 400 | developer |  |  | InsertSignal called inside a caller's transaction |  |
| `migrate.precondition` | correction | 400 | operator | `table`, `value` | D115, D116 | A migrate/backfill command lacks a database, table, relation store, or has bad mapping input |  |
| `content.not_acceptable` | correction | 406 | person |  |  | The Accept header asks for a representation the route cannot give (no template, no markdown) |  |
| `request.too_large` | correction | 413 | person |  |  | The body exceeds the size limit |  |
| `eventstream.too_many_streams` | refusal | 429 | person |  | D116 | The token already holds the maximum number of open streams |  |
| `rate.limited` | failure | 429 | person |  |  | The rate limiter refuses the request |  |
| `rev.missing` | correction | 422 | person | **`record`**, **`write`** | T1010 | After the rev cutover, a write to a governance type (or a governance relation, change-check answer, premise confirmation) names no rev |  |
| `rev.not_applicable` | correction | 422 | person | **`grantee`** | D165 | A grant names a rev for a grantee that is no identity |  |
| `title.cutover` | correction | 422 | person | `length`, `limit`, `type` | D112, T274 | After the title cut, a create/update lacks a title or has one over 120 chars (codes title_missing / title_too_long), or a dynamic field exceeds max_length (field_too_long) |  |
| `signal.history_type_refused` | correction | 422 | person | `signal_type`, `suggested_signal_type` | D115 | After the signal event cutover, a history-kind signal_type is sent |  |
| `signal.message_too_long` | correction | 422 | person | `length`, `limit` | D114 | After the Signal cutover, a message is over 500 characters |  |
| `amendment.type_not_in_list` | correction | 422 | person | **`value`**, **`allowed`** | D112 | amendment_type is not one of the closed list |  |
| `plan.repository_paths_required` | correction | 422 | person |  | T1015 | After cutover, a plan whose work ends in a commit is submitted without repository or paths |  |
| `schedule.needs_time` | correction | 422 | person | **`type`**, **`record`** | T1009 | Moving to scheduled without scheduled_at |  |
| `grant.grantee_not_identity` | correction | 422 | person | **`grantee`** | D165 | A grant names a token_id that is not an identity |  |
| `reference.addon_not_run` | correction | 422 | person | **`type`** | T881 | A reference field names an add-on type (Task/Goal/Run) on an instance that does not run the add-on |  |
| `reference.write_once` | refusal | 422 | person | **`record`** | D113 | Changing or clearing a write-once reference (amends, task_ref, other refs) |  |
| `reference.not_found` | correction | 422 | person | **`value`**, `type` | D113, D104 | A reference field names no record the caller can read (guided refusal for Signal task_ref; Decision/Amendment for amends; scope pattern) |  |
| `reference.subject_type_unknown` | correction | 422 | person |  |  | A Signal's subject_type names no registered content type |  |
| `ident.ambiguous` | correction | 422 | person | **`value`**, `count`, **`type`** | D110 | An identifier (old number, slug, address) matches more than one record |  |
| `relation.pair_not_permitted` | refusal | 422 | person | **`kind`**, **`source`**, **`target`** | D142 | The relation kind does not permit this source-target type pair |  |
| `relation.to_itself` | refusal | 422 | person | **`kind`**, **`record`** | T1054 | A relation names the same record as its source and its target |  |
| `relation.kind_unknown_assert` | correction | 422 | person | **`kind`** |  | Assert names an unknown relation kind |  |
| `relation.edge_class_not_asserted` | correction | 422 | person | **`allowed`** |  | Assert is sent an edge_class other than asserted |  |
| `decision.sources_too_many` | correction | 422 | person | **`count`**, **`limit`** | D148 | A Decision carries more sources than the maximum |  |
| `decision.source_duplicate` | correction | 422 | person | **`index`** | D148 | Two sources name the same repository, commit, path and anchor |  |
| `field.immutable` | refusal | 422 | person |  | D110 | An update tries to change a server-assigned or flow-controlled field (address, address_no, status, other immutable keys) |  |
| `content_type.redefine_incompatible` | refusal | 422 | person | `from_type`, `to_type` |  | A redefinition would remove/retype a field items hold, make one required, add a required field, or change url_prefix |  |
| `address.prefix_reserved` | refusal | 422 | person | **`type`**, **`prefix`** | D110 | A type asks for a prefix other than the one reserved for it |  |
| `address.prefix_single_letter` | correction | 422 | person | **`prefix`**, **`type`** | D110 | A non-core type asks for a single-letter prefix |  |
| `address.prefix_fixed` | refusal | 422 | person | **`type`**, **`prefix`** | D110 | Changing the prefix of a type that already has addresses |  |
| `address.prefix_taken` | correction | 422 | person | **`prefix`**, **`type`** | D110 | The prefix belongs to another type |  |
| `address.prefix_unavailable` | correction | 422 | person | **`type`** | D110 | No prefix can be proposed (name too short, or every candidate taken) |  |
| `address.prefix_web_content` | correction | 422 | person |  | D110 | A web-content type (url_prefix) is given an address prefix |  |
| `places_migrate.address_held` | refusal | 422 | operator | **`type`**, **`record`**, **`holder_type`**, **`holder`** |  | A legacy place's address is already held by the compiled type |  |
| `webhook.url_not_allowed` | correction | 422 | person |  |  | A webhook URL is not HTTPS, targets localhost/.local, cannot resolve, or resolves to a private IP |  |
| `config.invalid` | correction | 422 | person | `value`, `min` | T969 | ValidateConfig rejects a Config field |  |
| `identity.schema_not_ready` | failure | 422 | operator |  | D165 | Minting a classified token before the token/identity columns exist |  |
| `amends.no_database` | failure | 422 | person |  |  | amends cannot be checked because no database is configured |  |
| `role.level_mismatch` | correction | 422 | developer |  |  | A role is registered twice with different levels |  |
| `validation.failed` | correction | 422 | person |  |  | Several fields failed validation; each field carries its own type |  |
| `field.required` | correction | 422 | person |  |  | A required value is empty |  |
| `field.too_long` | correction | 422 | person | **`limit`** |  | A string exceeds its length limit |  |
| `field.too_short` | correction | 422 | person | **`min`** |  | A string is under its minimum length |  |
| `field.out_of_range` | correction | 422 | person | `min`, `max`, `value` |  | A number is outside its allowed range (negative, zero, below/above bound, inconsistent count) |  |
| `field.too_many` | correction | 422 | person | **`limit`** |  | A list has more entries than allowed |  |
| `field.one_line` | correction | 422 | person |  |  | A value must be a single line |  |
| `field.invalid_format` | correction | 422 | person | `value` |  | The value does not match the required pattern or syntax |  |
| `field.not_in_list` | correction | 422 | person | `allowed`, `value`, `schema_field` |  | The value is not one of a closed list or a registered set |  |
| `field.type_mismatch` | correction | 422 | person | **`expected`** |  | A dynamic field's JSON value has the wrong type |  |
| `field.unknown` | correction | 422 | person |  |  | A dynamic item names a field the schema does not have |  |
| `schema.field_invalid` | correction | 422 | person | `schema_field`, `value`, `field_type`, `type`, `limit` |  | A content type definition has an illegal field (reserved name, bad max_length, max_length on non-string, reference on non-string, title over the D112 limit) |  |
