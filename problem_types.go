package smeldr

import (
	"errors"
	"net/http"
	"strconv"
)

// ProblemType is the stable id of a refusal (D171): a short dotted name,
// area then reason (for example "ratify.automated_class"), that never changes
// when the English message does. A UI keys its own text on it; core's message
// stays the API's own words. The closed list is [ProblemTypes], and a retired
// id is never reused.
type ProblemType string

// ProblemTypeInfo is one entry of the closed list of refusal types.
type ProblemTypeInfo struct {
	// ID is the stable id.
	ID ProblemType `json:"id"`
	// Status is the HTTP status the refusal answers with.
	Status int `json:"status"`
	// Title is a short English summary, the RFC 9457 title.
	Title string `json:"title"`
	// Rule names the Decision or Task the refusal enforces, when it has one.
	Rule string `json:"rule,omitempty"`
	// Audience is who meets it: "person" (shown in a UI), "developer" (a
	// program calling the API wrongly) or "operator" (running a migration).
	Audience string `json:"audience"`
	// Retired is the version a retired type was retired in; empty while it
	// is in use. A retired type stays in the list and its id is never reused.
	Retired string `json:"retired,omitempty"`
	// ReplacedBy is the type that replaced a retired one, if any.
	ReplacedBy ProblemType `json:"replaced_by,omitempty"`
	// Class says where a UI shows it (D174): "refusal" (a rule says no),
	// "correction" (fix the input, at the field), "conflict" (the record
	// moved; read it again) or "failure" (outside the caller's reach).
	Class string `json:"class"`
	// Values are the names of the values the type carries, a closed set like
	// the ids: a name is never renamed or removed, and one may be added. A
	// refusal sends those of them its message states, so any one may be
	// absent. Each value says no more than the message does (see
	// [ProblemValuesOf]).
	Values []string `json:"values,omitempty"`
}

// The classes of [ProblemTypeInfo.Class].
const (
	problemClassRefusal    = "refusal"
	problemClassCorrection = "correction"
	problemClassConflict   = "conflict"
	problemClassFailure    = "failure"
)

// problemTypeBase is the URI prefix of a problem+json type (RFC 9457). Code
// never dereferences it.
const problemTypeBase = "https://smeldr.dev/problems/"

// URI is the RFC 9457 type of t: the id under problemTypeBase, or
// "about:blank" when t is empty (an untyped error).
func (t ProblemType) URI() string {
	if t == "" {
		return "about:blank"
	}
	return problemTypeBase + string(t)
}

// The closed list of refusal types (T1039). An id here is a promise to every
// client keying text on it: never rename one, retire it (Retired, ReplacedBy).
const (
	problemRatifyAutomatedClass               ProblemType = "ratify.automated_class"
	problemGrantAgentGrantsApprovalRole       ProblemType = "grant.agent_grants_approval_role"
	problemGrantAutomatedHoldsRatify          ProblemType = "grant.automated_holds_ratify"
	problemGrantAutomatedApproveChangeNotArea ProblemType = "grant.automated_approve_change_not_area"
	problemGrantAutomatedNeedsHumanGrantor    ProblemType = "grant.automated_needs_human_grantor"
	problemGrantGrantorLacksAreaAuthority     ProblemType = "grant.grantor_lacks_area_authority"
	problemGrantAuthorityUnreadable           ProblemType = "grant.authority_unreadable"
	problemGrantJobNeverGrants                ProblemType = "grant.job_never_grants"
	problemGrantItemApproverScope             ProblemType = "grant.item_approver_scope"
	problemGrantItemApproverNeedsHumanGrantor ProblemType = "grant.item_approver_needs_human_grantor"
	problemGrantCheckHasNoApprovalArea        ProblemType = "grant.check_has_no_approval_area"
	problemTokenJobNeverMints                 ProblemType = "token.job_never_mints"
	problemTokenAgentMintsAutomatedOnly       ProblemType = "token.agent_mints_automated_only"
	problemTokenIdentityNotActive             ProblemType = "token.identity_not_active"
	problemTokenIdentityUnclassified          ProblemType = "token.identity_unclassified"
	problemTransitionGate                     ProblemType = "transition.gate"
	problemTransitionGateUnwired              ProblemType = "transition.gate_unwired"
	problemTransitionNoActor                  ProblemType = "transition.no_actor"
	problemPlaceAuthorityRequired             ProblemType = "place.authority_required"
	problemPlaceNoActor                       ProblemType = "place.no_actor"
	problemDecisionScopeRole                  ProblemType = "decision.scope_role"
	problemDecisionSourcesNeedPlace           ProblemType = "decision.sources_need_place"
	problemChangeCheckCoreOnly                ProblemType = "change_check.core_only"
	problemChangeCheckNotDeletable            ProblemType = "change_check.not_deletable"
	problemChangeCheckNoCaller                ProblemType = "change_check.no_caller"
	problemChangeCheckAnswerAuthority         ProblemType = "change_check.answer_authority"
	problemSignalConditionSystemOnly          ProblemType = "signal.condition_system_only"
	problemSignalAnswerNotPermitted           ProblemType = "signal.answer_not_permitted"
	problemPlanRelationsCoreOwned             ProblemType = "plan.relations_core_owned"
	problemAdministratorsUnauthenticated      ProblemType = "administrators.unauthenticated"
	problemSubscriptionNoCaller               ProblemType = "subscription.no_caller"
	problemSubscriptionNotOwner               ProblemType = "subscription.not_owner"
	problemSubscriptionWebhookNeedsAdmin      ProblemType = "subscription.webhook_needs_admin"
	problemSubscriptionSeedNeedsAdmin         ProblemType = "subscription.seed_needs_admin"
	problemCapabilityDenied                   ProblemType = "capability.denied"
	problemRoleInsufficient                   ProblemType = "role.insufficient"
	problemScopeTypeNotReadable               ProblemType = "scope.type_not_readable"
	problemWriteNotPermitted                  ProblemType = "write.not_permitted"
	problemCsrfInvalid                        ProblemType = "csrf.invalid"
	problemAuthUnauthenticated                ProblemType = "auth.unauthenticated"
	problemAuthTokenInvalid                   ProblemType = "auth.token_invalid"
	problemAuthUploadTokenInvalid             ProblemType = "auth.upload_token_invalid"
	problemAuthPreviewTokenInvalid            ProblemType = "auth.preview_token_invalid"
	problemTokenLastAdmin                     ProblemType = "token.last_admin"
	problemRevConflict                        ProblemType = "rev.conflict"
	problemRelationAlreadyExists              ProblemType = "relation.already_exists"
	problemRelationEnded                      ProblemType = "relation.ended"
	problemRelationAlreadyWithdrawn           ProblemType = "relation.already_withdrawn"
	problemRelationWithdrawFailed             ProblemType = "relation.withdraw_failed"
	problemTransitionNotPermitted             ProblemType = "transition.not_permitted"
	problemTransitionUnknownTarget            ProblemType = "transition.unknown_target"
	problemStateUnknownInitial                ProblemType = "state.unknown_initial"
	problemTransitionSingletonState           ProblemType = "transition.singleton_state"
	problemItemLockedState                    ProblemType = "item.locked_state"
	problemSlugPublishedImmutable             ProblemType = "slug.published_immutable"
	problemSlugTaken                          ProblemType = "slug.taken"
	problemSlugAmbiguous                      ProblemType = "slug.ambiguous"
	problemDecisionPlaceMissing               ProblemType = "decision.place_missing"
	problemDecisionPlaceExists                ProblemType = "decision.place_exists"
	problemDecisionReplacementState           ProblemType = "decision.replacement_state"
	problemPlacesDomainPrefixHeld             ProblemType = "places.domain_prefix_held"
	problemChangeCheckSupersedeRace           ProblemType = "change_check.supersede_race"
	problemChangeCheckUseAnswerChangeCheck    ProblemType = "change_check.use_answer_change_check"
	problemChangeCheckAreaNotTouched          ProblemType = "change_check.area_not_touched"
	problemChangeCheckAreaCommentOnly         ProblemType = "change_check.area_comment_only"
	problemChangeCheckNotAwaiting             ProblemType = "change_check.not_awaiting"
	problemChangeCheckSuperseded              ProblemType = "change_check.superseded"
	problemChangeCheckAlreadyAnswered         ProblemType = "change_check.already_answered"
	problemChangeCheckAnswerRace              ProblemType = "change_check.answer_race"
	problemPremiseNoOpenFlag                  ProblemType = "premise.no_open_flag"
	problemPremiseAlreadyConfirmed            ProblemType = "premise.already_confirmed"
	problemPremiseConditionNotOpen            ProblemType = "premise.condition_not_open"
	problemSignalRequestNotResolvable         ProblemType = "signal.request_not_resolvable"
	problemSignalConditionNotAnswerable       ProblemType = "signal.condition_not_answerable"
	problemSignalNotOpen                      ProblemType = "signal.not_open"
	problemSubscriptionDefaultFixed           ProblemType = "subscription.default_fixed"
	problemSubscriptionDefaultUndeletable     ProblemType = "subscription.default_undeletable"
	problemMigrateDuplicatePublishedSlugs     ProblemType = "migrate.duplicate_published_slugs"
	problemItemNotFound                       ProblemType = "item.not_found"
	problemRouteNotFound                      ProblemType = "route.not_found"
	problemContentTypeNotFound                ProblemType = "content_type.not_found"
	problemRelationKindUnknown                ProblemType = "relation.kind_unknown"
	problemRelationNotFound                   ProblemType = "relation.not_found"
	problemPlaceNotFound                      ProblemType = "place.not_found"
	problemIdentityNotFound                   ProblemType = "identity.not_found"
	problemRoleNotFound                       ProblemType = "role.not_found"
	problemGrantNotFound                      ProblemType = "grant.not_found"
	problemPremiseConditionNotFound           ProblemType = "premise.condition_not_found"
	problemFeatureNotEnabled                  ProblemType = "feature.not_enabled"
	problemRedirectGone                       ProblemType = "redirect.gone"
	problemRequestMalformedBody               ProblemType = "request.malformed_body"
	problemRequestInvalidParam                ProblemType = "request.invalid_param"
	problemContentTypeDefineFailed            ProblemType = "content_type.define_failed"
	problemTransitionStatusRequired           ProblemType = "transition.status_required"
	problemTransitionReasonRequired           ProblemType = "transition.reason_required"
	problemTypeNotRegistered                  ProblemType = "type.not_registered"
	problemContentTypeNotRuntime              ProblemType = "content_type.not_runtime"
	problemSlugRecordTypeFixed                ProblemType = "slug.record_type_fixed"
	problemAddonNotRun                        ProblemType = "addon.not_run"
	problemCapabilityUnknown                  ProblemType = "capability.unknown"
	problemLimitPathsPerCall                  ProblemType = "limit.paths_per_call"
	problemLimitRestCountItems                ProblemType = "limit.rest_count_items"
	problemLimitFindings                      ProblemType = "limit.findings"
	problemSubscriptionOwnerLimit             ProblemType = "subscription.owner_limit"
	problemSubscriptionItemLimit              ProblemType = "subscription.item_limit"
	problemSubscriptionRoleDefaultsNeedBand   ProblemType = "subscription.role_defaults_need_band"
	problemSubscriptionWebhookNotStream       ProblemType = "subscription.webhook_not_stream"
	problemSearchQueryInvalid                 ProblemType = "search.query_invalid"
	problemChangeCheckFilterRequired          ProblemType = "change_check.filter_required"
	problemApproversPlaceRequired             ProblemType = "approvers.place_required"
	problemEventstreamParamsConflict          ProblemType = "eventstream.params_conflict"
	problemEventstreamNoSubscriptions         ProblemType = "eventstream.no_subscriptions"
	problemEventLogNotEnabled                 ProblemType = "event_log.not_enabled"
	problemEventLogCursorInvalid              ProblemType = "event_log.cursor_invalid"
	problemFlowStandingInvalid                ProblemType = "flow.standing_invalid"
	problemReferencesSignalInTx               ProblemType = "references.signal_in_tx"
	problemMigratePrecondition                ProblemType = "migrate.precondition"
	problemContentNotAcceptable               ProblemType = "content.not_acceptable"
	problemRequestTooLarge                    ProblemType = "request.too_large"
	problemEventstreamTooManyStreams          ProblemType = "eventstream.too_many_streams"
	problemRateLimited                        ProblemType = "rate.limited"
	problemRevMissing                         ProblemType = "rev.missing"
	problemRevNotApplicable                   ProblemType = "rev.not_applicable"
	problemTitleCutover                       ProblemType = "title.cutover"
	problemSignalHistoryTypeRefused           ProblemType = "signal.history_type_refused"
	problemSignalMessageTooLong               ProblemType = "signal.message_too_long"
	problemAmendmentTypeNotInList             ProblemType = "amendment.type_not_in_list"
	problemPlanRepositoryPathsRequired        ProblemType = "plan.repository_paths_required"
	problemScheduleNeedsTime                  ProblemType = "schedule.needs_time"
	problemGrantGranteeNotIdentity            ProblemType = "grant.grantee_not_identity"
	problemReferenceAddonNotRun               ProblemType = "reference.addon_not_run"
	problemReferenceWriteOnce                 ProblemType = "reference.write_once"
	problemReferenceNotFound                  ProblemType = "reference.not_found"
	problemReferenceSubjectTypeUnknown        ProblemType = "reference.subject_type_unknown"
	problemIdentAmbiguous                     ProblemType = "ident.ambiguous"
	problemRelationPairNotPermitted           ProblemType = "relation.pair_not_permitted"
	problemRelationToItself                   ProblemType = "relation.to_itself"
	problemRelationKindUnknownAssert          ProblemType = "relation.kind_unknown_assert"
	problemRelationEdgeClassNotAsserted       ProblemType = "relation.edge_class_not_asserted"
	problemDecisionSourcesTooMany             ProblemType = "decision.sources_too_many"
	problemDecisionSourceDuplicate            ProblemType = "decision.source_duplicate"
	problemFieldImmutable                     ProblemType = "field.immutable"
	problemContentTypeRedefineIncompatible    ProblemType = "content_type.redefine_incompatible"
	problemAddressPrefixReserved              ProblemType = "address.prefix_reserved"
	problemAddressPrefixSingleLetter          ProblemType = "address.prefix_single_letter"
	problemAddressPrefixFixed                 ProblemType = "address.prefix_fixed"
	problemAddressPrefixTaken                 ProblemType = "address.prefix_taken"
	problemAddressPrefixUnavailable           ProblemType = "address.prefix_unavailable"
	problemAddressPrefixWebContent            ProblemType = "address.prefix_web_content"
	problemPlacesMigrateAddressHeld           ProblemType = "places_migrate.address_held"
	problemWebhookUrlNotAllowed               ProblemType = "webhook.url_not_allowed"
	problemConfigInvalid                      ProblemType = "config.invalid"
	problemIdentitySchemaNotReady             ProblemType = "identity.schema_not_ready"
	problemAmendsNoDatabase                   ProblemType = "amends.no_database"
	problemRoleLevelMismatch                  ProblemType = "role.level_mismatch"
	problemValidationFailed                   ProblemType = "validation.failed"
	problemFieldRequired                      ProblemType = "field.required"
	problemFieldTooLong                       ProblemType = "field.too_long"
	problemFieldTooShort                      ProblemType = "field.too_short"
	problemFieldOutOfRange                    ProblemType = "field.out_of_range"
	problemFieldTooMany                       ProblemType = "field.too_many"
	problemFieldOneLine                       ProblemType = "field.one_line"
	problemFieldInvalidFormat                 ProblemType = "field.invalid_format"
	problemFieldNotInList                     ProblemType = "field.not_in_list"
	problemFieldTypeMismatch                  ProblemType = "field.type_mismatch"
	problemFieldUnknown                       ProblemType = "field.unknown"
	problemSchemaFieldInvalid                 ProblemType = "schema.field_invalid"
)

// problemTypeList is every refusal type, in the catalogue's order.
var problemTypeList = []ProblemTypeInfo{
	{ID: problemRatifyAutomatedClass, Status: 403, Title: "An agent or job token tries a transition gated on ratify, or an act needing ratify over a place (placing/moving a Decision or Area)", Rule: "D142 pt 5, T884 (places path: D145, D146)", Audience: "person", Class: problemClassRefusal, Values: []string{"token_class", "act", "place"}},
	{ID: problemGrantAgentGrantsApprovalRole, Status: 403, Title: "An agent grantor grants a role that holds ratify or approve-change", Rule: "D142, D156 pt 3, T984", Audience: "person", Class: problemClassRefusal, Values: []string{"role", "operation"}},
	{ID: problemGrantAutomatedHoldsRatify, Status: 403, Title: "A grant would give an agent/job token a role holding ratify", Rule: "D142, T971", Audience: "person", Class: problemClassRefusal, Values: []string{"token_class", "role"}},
	{ID: problemGrantAutomatedApproveChangeNotArea, Status: 403, Title: "An agent/job token is granted approve-change by a role that is not an Area role anchored on an Area", Rule: "D142, T971", Audience: "person", Class: problemClassRefusal, Values: []string{"token_class", "role", "suggested_role"}},
	{ID: problemGrantAutomatedNeedsHumanGrantor, Status: 403, Title: "An agent/job token's approval-role grant has no known grantor, or the grantor is not a human token", Rule: "D142, T971", Audience: "person", Class: problemClassRefusal, Values: []string{"token_class", "role", "grantor_class"}},
	{ID: problemGrantGrantorLacksAreaAuthority, Status: 403, Title: "The grantor holds no approve-change over the Area it tries to delegate (Area role, or item-change-approver for a Check's Area)", Rule: "T971, D144", Audience: "person", Class: problemClassRefusal, Values: []string{"area", "token_class"}},
	{ID: problemGrantAuthorityUnreadable, Status: 403, Title: "A grant check fails closed because the grantee/grantor class, the anchor, or the grantor's authority/Areas cannot be read", Rule: "D142, T971", Audience: "person", Class: problemClassFailure, Values: []string{"grantee", "grantor", "token_class", "role", "area"}},
	{ID: problemGrantJobNeverGrants, Status: 403, Title: "A job token tries to grant any role", Rule: "D156, T986", Audience: "person", Class: problemClassRefusal, Values: []string{"role"}},
	{ID: problemGrantItemApproverScope, Status: 403, Title: "An item-change-approver grant does not name exactly one ChangeCheck", Rule: "D144", Audience: "person", Class: problemClassCorrection, Values: []string{"role"}},
	{ID: problemGrantItemApproverNeedsHumanGrantor, Status: 403, Title: "An item-change-approver grant has no known grantor, or the grantor is not human", Rule: "D144", Audience: "person", Class: problemClassRefusal, Values: []string{"role", "grantor_class"}},
	{ID: problemGrantCheckHasNoApprovalArea, Status: 403, Title: "Delegating approval of a Check that has no Area requiring approval", Rule: "D144", Audience: "person", Class: problemClassRefusal},
	{ID: problemTokenJobNeverMints, Status: 403, Title: "A job token tries to create a token", Rule: "D156, T986", Audience: "person", Class: problemClassRefusal},
	{ID: problemTokenAgentMintsAutomatedOnly, Status: 403, Title: "An agent token tries to mint a human or unclassified token", Rule: "D142, T984", Audience: "person", Class: problemClassRefusal, Values: []string{"token_class"}},
	{ID: problemTokenIdentityNotActive, Status: 403, Title: "Minting a token for an identity whose status is not active (e.g. retired)", Rule: "D165", Audience: "person", Class: problemClassRefusal, Values: []string{"identity", "state"}},
	{ID: problemTokenIdentityUnclassified, Status: 403, Title: "Minting a token for an identity with no class", Rule: "D165", Audience: "person", Class: problemClassRefusal, Values: []string{"identity"}},
	{ID: problemTransitionGate, Status: 403, Title: "The actor does not hold the operation a transition requires (or the check itself failed, fail closed)", Rule: "D34, D40", Audience: "person", Class: problemClassRefusal, Values: []string{"type", "from", "to", "operation"}},
	{ID: problemTransitionGateUnwired, Status: 403, Title: "A strict gated transition runs with no governance wired", Rule: "D142, T884", Audience: "person", Class: problemClassFailure, Values: []string{"from", "to", "operation"}},
	{ID: problemTransitionNoActor, Status: 403, Title: "A strict gated transition is attempted with no actor in context", Rule: "D34", Audience: "person", Class: problemClassRefusal, Values: []string{"from", "to", "operation"}},
	{ID: problemPlaceAuthorityRequired, Status: 403, Title: "Placing or moving a Decision/Area needs ratify over the place and the actor lacks it", Rule: "D145, D146", Audience: "person", Class: problemClassRefusal, Values: []string{"act", "operation", "place"}},
	{ID: problemPlaceNoActor, Status: 403, Title: "A place act needing authority runs with no actor known", Rule: "D145, D146", Audience: "person", Class: problemClassRefusal, Values: []string{"act", "operation", "place"}},
	{ID: problemDecisionScopeRole, Status: 403, Title: "A Decision's scope maps to a required role the actor is not granted (or the grant lookup failed)", Audience: "person", Class: problemClassRefusal, Values: []string{"scope", "role"}},
	{ID: problemDecisionSourcesNeedPlace, Status: 403, Title: "Changing a Decision's sources when it has no place to hold ratify over", Rule: "D148", Audience: "person", Class: problemClassRefusal, Values: []string{"decision"}},
	{ID: problemChangeCheckCoreOnly, Status: 403, Title: "A caller tries to create or move a ChangeCheck directly instead of via check_change / core", Rule: "D143", Audience: "person", Class: problemClassRefusal},
	{ID: problemChangeCheckNotDeletable, Status: 403, Title: "A caller tries to delete a ChangeCheck", Rule: "D143", Audience: "person", Class: problemClassRefusal},
	{ID: problemChangeCheckNoCaller, Status: 403, Title: "check_change is called with no caller", Audience: "person", Class: problemClassRefusal},
	{ID: problemChangeCheckAnswerAuthority, Status: 403, Title: "Answering a Check for an Area without approve-change over it (or item-change-approver on the Check), or with no actor", Rule: "D144, T884", Audience: "person", Class: problemClassRefusal, Values: []string{"area", "role"}},
	{ID: problemSignalConditionSystemOnly, Status: 403, Title: "A caller creates a condition-kind Signal, or tries to resolve a condition", Rule: "D115", Audience: "person", Class: problemClassRefusal, Values: []string{"signal_type"}},
	{ID: problemSignalAnswerNotPermitted, Status: 403, Title: "Answering/withdrawing a Signal without capability while governance is on and no capability context exists", Rule: "D115", Audience: "person", Class: problemClassRefusal},
	{ID: problemPlanRelationsCoreOwned, Status: 403, Title: "A caller asserts a plan's belongs_to_area / checked_against relation by hand", Rule: "T1015", Audience: "person", Class: problemClassCorrection},
	{ID: problemAdministratorsUnauthenticated, Status: 403, Title: "Asking who administers without being authenticated", Audience: "person", Class: problemClassRefusal},
	{ID: problemSubscriptionNoCaller, Status: 403, Title: "A subscription API is called with governance on and no caller/identity in context", Rule: "D116", Audience: "person", Class: problemClassRefusal},
	{ID: problemSubscriptionNotOwner, Status: 403, Title: "Creating or changing a subscription owned by another token without administer", Rule: "D116", Audience: "person", Class: problemClassRefusal},
	{ID: problemSubscriptionWebhookNeedsAdmin, Status: 403, Title: "A subscription asks for webhook delivery without administer", Rule: "D116", Audience: "person", Class: problemClassRefusal},
	{ID: problemSubscriptionSeedNeedsAdmin, Status: 403, Title: "Seeding role subscriptions without administer", Rule: "D116", Audience: "person", Class: problemClassRefusal},
	{ID: problemCapabilityDenied, Status: 403, Title: "AuthorizeCapability refuses: legacy floor not met, no user ID, capability not granted, or read scope does not reach the target type/item", Audience: "person", Class: problemClassRefusal},
	{ID: problemRoleInsufficient, Status: 403, Title: "An HTTP endpoint's role floor is not met (Editor/Author/Admin), or an unauthenticated caller on an endpoint that answers 403 not 401", Audience: "person", Class: problemClassRefusal},
	{ID: problemScopeTypeNotReadable, Status: 403, Title: "The token's read scope does not cover the requested type", Audience: "person", Class: problemClassRefusal},
	{ID: problemWriteNotPermitted, Status: 403, Title: "A Module create/update/delete fails its write-operation check (checkWriteOp)", Audience: "person", Class: problemClassRefusal},
	{ID: problemCsrfInvalid, Status: 403, Title: "CSRF cookie missing/empty, or header does not match it", Audience: "person", Class: problemClassRefusal},
	{ID: problemAuthUnauthenticated, Status: 401, Title: "An endpoint requires a valid bearer/session and none authenticates", Audience: "person", Class: problemClassRefusal},
	{ID: problemAuthTokenInvalid, Status: 401, Title: "A signed session token is malformed, has a bad HMAC, bad payload or is expired", Audience: "person", Class: problemClassRefusal},
	{ID: problemAuthUploadTokenInvalid, Status: 401, Title: "An upload token is malformed, has a bad HMAC or is expired", Audience: "person", Class: problemClassRefusal},
	{ID: problemAuthPreviewTokenInvalid, Status: 404, Title: "A preview token is malformed, has a bad HMAC or is expired (answered as not found on purpose)", Audience: "person", Class: problemClassRefusal},
	{ID: problemTokenLastAdmin, Status: 409, Title: "Revoking the last active admin token", Audience: "person", Class: problemClassRefusal},
	{ID: problemRevConflict, Status: 409, Title: "A write names a rev the record no longer has (*RevConflictError; SQLRepo returns the bare sentinel)", Rule: "T1010", Audience: "person", Class: problemClassConflict, Values: []string{"type", "record", "rev_sent", "rev_current"}},
	{ID: problemRelationAlreadyExists, Status: 409, Title: "A first assert lost the race to another writer for the same live relation", Rule: "T1019, A628", Audience: "person", Class: problemClassConflict, Values: []string{"relation"}},
	{ID: problemRelationEnded, Status: 409, Title: "Re-asserting by id a relation that has already ended", Audience: "person", Class: problemClassConflict, Values: []string{"relation", "ended_at"}},
	{ID: problemRelationAlreadyWithdrawn, Status: 409, Title: "Withdrawing a relation that already ended", Rule: "T1010", Audience: "person", Class: problemClassConflict, Values: []string{"relation", "ended_at"}},
	{ID: problemRelationWithdrawFailed, Status: 409, Title: "Withdrawing a relation whose row could not be updated (changed concurrently)", Rule: "T1010", Audience: "person", Class: problemClassConflict, Values: []string{"relation"}},
	{ID: problemTransitionNotPermitted, Status: 409, Title: "The from-to transition is not in the type's flow", Audience: "person", Class: problemClassRefusal, Values: []string{"type", "from", "to"}},
	{ID: problemTransitionUnknownTarget, Status: 409, Title: "The target state does not exist in the type's flow", Audience: "person", Class: problemClassCorrection, Values: []string{"type", "to"}},
	{ID: problemStateUnknownInitial, Status: 409, Title: "A create names an initial status that is not a state in the registered flow", Audience: "person", Class: problemClassCorrection, Values: []string{"type", "state"}},
	{ID: problemTransitionSingletonState, Status: 409, Title: "The type already has an item in a state that admits only one", Audience: "person", Class: problemClassRefusal, Values: []string{"type", "state"}},
	{ID: problemItemLockedState, Status: 409, Title: "Editing (or re-slugging) an item in a locked state", Audience: "person", Class: problemClassRefusal, Values: []string{"type", "record", "state"}},
	{ID: problemSlugPublishedImmutable, Status: 409, Title: "Changing the slug of a published item", Audience: "person", Class: problemClassRefusal},
	{ID: problemSlugTaken, Status: 409, Title: "The URL/slug is already published (same prefix, or a linked type sharing the URL space)", Audience: "person", Class: problemClassCorrection, Values: []string{"slug", "prefix", "suggested_slug", "type"}},
	{ID: problemSlugAmbiguous, Status: 409, Title: "A slug names more than one readable item", Audience: "person", Class: problemClassCorrection, Values: []string{"slug"}},
	{ID: problemDecisionPlaceMissing, Status: 409, Title: "Ratifying a Decision that has no place", Rule: "D142 pt 4, T883, D83", Audience: "person", Class: problemClassRefusal, Values: []string{"decision"}},
	{ID: problemDecisionPlaceExists, Status: 409, Title: "Placing a Decision that already has a place", Rule: "D142", Audience: "person", Class: problemClassRefusal, Values: []string{"decision", "kind", "place"}},
	{ID: problemDecisionReplacementState, Status: 409, Title: "Replacing a Decision needs a ratified replacement, and the replaced Decision at ratified or pending-re-evaluation", Rule: "T1054", Audience: "person", Class: problemClassRefusal, Values: []string{"decision", "replacement", "state"}},
	{ID: problemPlacesDomainPrefixHeld, Status: 409, Title: "Setup of places while the runtime domain type still holds the DOM prefix", Audience: "person", Class: problemClassRefusal, Values: []string{"prefix"}},
	{ID: problemChangeCheckSupersedeRace, Status: 409, Title: "A Check kept changing while being superseded", Rule: "T918", Audience: "person", Class: problemClassConflict, Values: []string{"check"}},
	{ID: problemChangeCheckUseAnswerChangeCheck, Status: 409, Title: "A Check's per-Area approval request is answered via answer_signal instead of answer_change_check", Rule: "D144", Audience: "person", Class: problemClassCorrection},
	{ID: problemChangeCheckAreaNotTouched, Status: 409, Title: "Answering for an Area the change does not touch", Rule: "D144", Audience: "person", Class: problemClassCorrection, Values: []string{"check", "area"}},
	{ID: problemChangeCheckAreaCommentOnly, Status: 409, Title: "Answering for an Area whose mode is comment-only", Rule: "D144", Audience: "person", Class: problemClassRefusal, Values: []string{"area", "check", "approval_mode"}},
	{ID: problemChangeCheckNotAwaiting, Status: 409, Title: "Answering a Check that is not awaiting approval", Rule: "D144", Audience: "person", Class: problemClassConflict, Values: []string{"check", "state"}},
	{ID: problemChangeCheckSuperseded, Status: 409, Title: "Answering an audit for a superseded Check", Rule: "D144", Audience: "person", Class: problemClassConflict, Values: []string{"check"}},
	{ID: problemChangeCheckAlreadyAnswered, Status: 409, Title: "The Area already answered this Check", Rule: "D144", Audience: "person", Class: problemClassConflict, Values: []string{"area", "check", "verdict"}},
	{ID: problemChangeCheckAnswerRace, Status: 409, Title: "Another answer for the same Area landed at the same moment", Rule: "D144", Audience: "person", Class: problemClassConflict, Values: []string{"area", "check"}},
	{ID: problemPremiseNoOpenFlag, Status: 409, Title: "Confirming a premise that has no open premise-flagged condition", Rule: "D149", Audience: "person", Class: problemClassConflict, Values: []string{"decision"}},
	{ID: problemPremiseAlreadyConfirmed, Status: 409, Title: "The premise was already confirmed for its current state", Rule: "D149", Audience: "person", Class: problemClassConflict, Values: []string{"decision"}},
	{ID: problemPremiseConditionNotOpen, Status: 409, Title: "The named premise-flagged condition is no longer open", Rule: "D149", Audience: "person", Class: problemClassConflict, Values: []string{"signal", "state"}},
	{ID: problemSignalRequestNotResolvable, Status: 409, Title: "Resolving a request-kind Signal (it is answered or withdrawn)", Rule: "D115", Audience: "person", Class: problemClassRefusal, Values: []string{"signal_type"}},
	{ID: problemSignalConditionNotAnswerable, Status: 409, Title: "Answering or withdrawing a condition-kind Signal", Rule: "D115", Audience: "person", Class: problemClassRefusal, Values: []string{"signal_type"}},
	{ID: problemSignalNotOpen, Status: 409, Title: "Answering/withdrawing a Signal that is not open", Rule: "D115", Audience: "person", Class: problemClassConflict, Values: []string{"state"}},
	{ID: problemSubscriptionDefaultFixed, Status: 409, Title: "Changing the filter of a default subscription", Rule: "D116", Audience: "person", Class: problemClassRefusal},
	{ID: problemSubscriptionDefaultUndeletable, Status: 409, Title: "Deleting a default subscription", Rule: "D116", Audience: "person", Class: problemClassRefusal},
	{ID: problemMigrateDuplicatePublishedSlugs, Status: 409, Title: "The published-slug index cannot be made because slugs are published twice", Audience: "operator", Class: problemClassRefusal, Values: []string{"table", "slugs"}},
	{ID: problemItemNotFound, Status: 404, Title: "The record does not exist, or exists but is hidden from the caller's read scope (answered alike)", Rule: "D110 (resolution order)", Audience: "person", Class: problemClassRefusal},
	{ID: problemRouteNotFound, Status: 404, Title: "No page/route serves the path (aggregate, unknown dynamic type route, api-only module asked for HTML, unmatched redirect, missing sitemap, missing AI doc)", Audience: "person", Class: problemClassRefusal},
	{ID: problemContentTypeNotFound, Status: 404, Title: "The named content type is not registered, or is not a content (web) type", Audience: "person", Class: problemClassCorrection, Values: []string{"type"}},
	{ID: problemRelationKindUnknown, Status: 404, Title: "A relation call names a relation kind that is not registered", Audience: "person", Class: problemClassCorrection},
	{ID: problemRelationNotFound, Status: 404, Title: "The relation (or a relation end) does not exist or cannot be read", Rule: "T1010", Audience: "person", Class: problemClassRefusal, Values: []string{"relation"}},
	{ID: problemPlaceNotFound, Status: 404, Title: "The named Organization/Domain/Area (or a place edge's Decision/Area) does not exist or is not readable", Audience: "person", Class: problemClassCorrection, Values: []string{"place", "area", "decision"}},
	{ID: problemIdentityNotFound, Status: 404, Title: "The named identity does not exist (lookup, or minting a token for it)", Rule: "D165", Audience: "person", Class: problemClassCorrection, Values: []string{"identity"}},
	{ID: problemRoleNotFound, Status: 404, Title: "The named role is not defined (get, or grant)", Audience: "person", Class: problemClassCorrection, Values: []string{"role"}},
	{ID: problemGrantNotFound, Status: 404, Title: "The grant ID does not exist", Audience: "person", Class: problemClassRefusal, Values: []string{"grant"}},
	{ID: problemPremiseConditionNotFound, Status: 404, Title: "No readable premise-flagged condition by that name", Rule: "D149", Audience: "person", Class: problemClassRefusal, Values: []string{"signal"}},
	{ID: problemFeatureNotEnabled, Status: 404, Title: "Findings or provenance are not recorded on this instance", Audience: "person", Class: problemClassFailure, Values: []string{"feature"}},
	{ID: problemRedirectGone, Status: 410, Title: "A redirect entry with no target (content removed)", Audience: "person", Class: problemClassRefusal},
	{ID: problemRequestMalformedBody, Status: 400, Title: "The JSON body cannot be decoded, or does not fit the type's struct", Audience: "person", Class: problemClassCorrection},
	{ID: problemRequestInvalidParam, Status: 400, Title: "A query/argument is missing or out of range (time, level, limit, depth, anchor, direction, ids)", Audience: "person", Class: problemClassCorrection},
	{ID: problemContentTypeDefineFailed, Status: 400, Title: "define_content_type over HTTP fails (cause logged, generic 400 returned)", Audience: "person", Class: problemClassCorrection},
	{ID: problemTransitionStatusRequired, Status: 400, Title: "The transition request body has no status", Audience: "person", Class: problemClassCorrection},
	{ID: problemTransitionReasonRequired, Status: 400, Title: "A transition that requires a reason is sent without one", Audience: "person", Class: problemClassCorrection, Values: []string{"from", "to"}},
	{ID: problemTypeNotRegistered, Status: 400, Title: "A transition/lookup names a type that is not registered", Audience: "person", Class: problemClassCorrection, Values: []string{"type"}},
	{ID: problemContentTypeNotRuntime, Status: 400, Title: "Redefining a type that was not defined at runtime", Audience: "person", Class: problemClassRefusal, Values: []string{"type"}},
	{ID: problemSlugRecordTypeFixed, Status: 400, Title: "Setting the slug of a record type (its address is its identity)", Rule: "D110", Audience: "person", Class: problemClassRefusal, Values: []string{"type"}},
	{ID: problemAddonNotRun, Status: 400, Title: "Reading/writing a Task/Goal/Run on an instance without the Orchestration add-on, or a type the instance keeps no records of", Rule: "T881", Audience: "person", Class: problemClassFailure, Values: []string{"type"}},
	{ID: problemCapabilityUnknown, Status: 400, Title: "AuthorizeCapability is asked for a resource/verb pair that does not exist", Audience: "person", Class: problemClassCorrection, Values: []string{"resource", "verb"}},
	{ID: problemLimitPathsPerCall, Status: 400, Title: "More paths than a call accepts (check_change, decisions_for_paths, check_coverage)", Audience: "person", Class: problemClassCorrection, Values: []string{"limit"}},
	{ID: problemLimitRestCountItems, Status: 400, Title: "More items than the stored rest-counts call accepts", Audience: "person", Class: problemClassCorrection, Values: []string{"limit"}},
	{ID: problemLimitFindings, Status: 400, Title: "Findings limit outside 0..max", Audience: "person", Class: problemClassCorrection, Values: []string{"max"}},
	{ID: problemSubscriptionOwnerLimit, Status: 400, Title: "The owner already holds the maximum number of subscriptions", Rule: "D116", Audience: "person", Class: problemClassRefusal, Values: []string{"limit"}},
	{ID: problemSubscriptionItemLimit, Status: 400, Title: "A subscription filter names more items than allowed", Rule: "D116", Audience: "person", Class: problemClassCorrection, Values: []string{"limit"}},
	{ID: problemSubscriptionRoleDefaultsNeedBand, Status: 400, Title: "Seeding role defaults without a band or Architect", Rule: "D116", Audience: "person", Class: problemClassCorrection},
	{ID: problemSubscriptionWebhookNotStream, Status: 400, Title: "Opening the stream on a subscription that delivers to a webhook", Rule: "D116", Audience: "person", Class: problemClassCorrection, Values: []string{"record"}},
	{ID: problemSearchQueryInvalid, Status: 400, Title: "Search has no text or filter, text length out of bounds, negative limit/offset, or an unsearchable type", Audience: "person", Class: problemClassCorrection, Values: []string{"min", "max", "length", "type"}},
	{ID: problemChangeCheckFilterRequired, Status: 400, Title: "Listing Checks without a repository, Area or Decision filter", Audience: "person", Class: problemClassCorrection},
	{ID: problemApproversPlaceRequired, Status: 400, Title: "approvers_of is called without naming a place", Audience: "person", Class: problemClassCorrection},
	{ID: problemEventstreamParamsConflict, Status: 400, Title: "?subscription= and ?channel= combined", Rule: "D116", Audience: "person", Class: problemClassCorrection},
	{ID: problemEventstreamNoSubscriptions, Status: 400, Title: "?subscription= on an instance that keeps no subscriptions", Rule: "D116", Audience: "person", Class: problemClassFailure},
	{ID: problemEventLogNotEnabled, Status: 400, Title: "?after= or an event page is asked for on an instance without the event log", Rule: "D117", Audience: "person", Class: problemClassFailure},
	{ID: problemEventLogCursorInvalid, Status: 400, Title: "The cursor is not one this instance issued", Rule: "D117", Audience: "person", Class: problemClassCorrection},
	{ID: problemFlowStandingInvalid, Status: 400, Title: "RegisterFlow with an illegal Standing tag", Audience: "developer", Class: problemClassCorrection, Values: []string{"flow", "state", "value", "allowed"}},
	{ID: problemReferencesSignalInTx, Status: 400, Title: "InsertSignal called inside a caller's transaction", Audience: "developer", Class: problemClassCorrection},
	{ID: problemMigratePrecondition, Status: 400, Title: "A migrate/backfill command lacks a database, table, relation store, or has bad mapping input", Rule: "D115, D116", Audience: "operator", Class: problemClassCorrection, Values: []string{"table", "value"}},
	{ID: problemContentNotAcceptable, Status: 406, Title: "The Accept header asks for a representation the route cannot give (no template, no markdown)", Audience: "person", Class: problemClassCorrection},
	{ID: problemRequestTooLarge, Status: 413, Title: "The body exceeds the size limit", Audience: "person", Class: problemClassCorrection},
	{ID: problemEventstreamTooManyStreams, Status: 429, Title: "The token already holds the maximum number of open streams", Rule: "D116", Audience: "person", Class: problemClassRefusal},
	{ID: problemRateLimited, Status: 429, Title: "The rate limiter refuses the request", Audience: "person", Class: problemClassFailure},
	{ID: problemRevMissing, Status: 422, Title: "After the rev cutover, a write to a governance type (or a governance relation, change-check answer, premise confirmation) names no rev", Rule: "T1010", Audience: "person", Class: problemClassCorrection, Values: []string{"record", "write"}},
	{ID: problemRevNotApplicable, Status: 422, Title: "A grant names a rev for a grantee that is no identity", Rule: "D165", Audience: "person", Class: problemClassCorrection, Values: []string{"grantee"}},
	{ID: problemTitleCutover, Status: 422, Title: "After the title cut, a create/update lacks a title or has one over 120 chars (codes title_missing / title_too_long), or a dynamic field exceeds max_length (field_too_long)", Rule: "D112, T274", Audience: "person", Class: problemClassCorrection, Values: []string{"length", "limit", "type"}},
	{ID: problemSignalHistoryTypeRefused, Status: 422, Title: "After the signal event cutover, a history-kind signal_type is sent", Rule: "D115", Audience: "person", Class: problemClassCorrection, Values: []string{"signal_type", "suggested_signal_type"}},
	{ID: problemSignalMessageTooLong, Status: 422, Title: "After the Signal cutover, a message is over 500 characters", Rule: "D114", Audience: "person", Class: problemClassCorrection, Values: []string{"length", "limit"}},
	{ID: problemAmendmentTypeNotInList, Status: 422, Title: "amendment_type is not one of the closed list", Rule: "D112", Audience: "person", Class: problemClassCorrection, Values: []string{"value", "allowed"}},
	{ID: problemPlanRepositoryPathsRequired, Status: 422, Title: "After cutover, a plan whose work ends in a commit is submitted without repository or paths", Rule: "T1015", Audience: "person", Class: problemClassCorrection},
	{ID: problemScheduleNeedsTime, Status: 422, Title: "Moving to scheduled without scheduled_at", Rule: "T1009", Audience: "person", Class: problemClassCorrection, Values: []string{"type", "record"}},
	{ID: problemGrantGranteeNotIdentity, Status: 422, Title: "A grant names a token_id that is not an identity", Rule: "D165", Audience: "person", Class: problemClassCorrection, Values: []string{"grantee"}},
	{ID: problemReferenceAddonNotRun, Status: 422, Title: "A reference field names an add-on type (Task/Goal/Run) on an instance that does not run the add-on", Rule: "T881", Audience: "person", Class: problemClassCorrection, Values: []string{"type"}},
	{ID: problemReferenceWriteOnce, Status: 422, Title: "Changing or clearing a write-once reference (amends, task_ref, other refs)", Rule: "D113", Audience: "person", Class: problemClassRefusal, Values: []string{"record"}},
	{ID: problemReferenceNotFound, Status: 422, Title: "A reference field names no record the caller can read (guided refusal for Signal task_ref; Decision/Amendment for amends; scope pattern)", Rule: "D113, D104", Audience: "person", Class: problemClassCorrection, Values: []string{"value", "type"}},
	{ID: problemReferenceSubjectTypeUnknown, Status: 422, Title: "A Signal's subject_type names no registered content type", Audience: "person", Class: problemClassCorrection},
	{ID: problemIdentAmbiguous, Status: 422, Title: "An identifier (old number, slug, address) matches more than one record", Rule: "D110", Audience: "person", Class: problemClassCorrection, Values: []string{"value", "count", "type"}},
	{ID: problemRelationPairNotPermitted, Status: 422, Title: "The relation kind does not permit this source-target type pair", Rule: "D142", Audience: "person", Class: problemClassRefusal, Values: []string{"kind", "source", "target"}},
	{ID: problemRelationToItself, Status: 422, Title: "A relation names the same record as its source and its target", Rule: "T1054", Audience: "person", Class: problemClassRefusal, Values: []string{"kind", "record"}},
	{ID: problemRelationKindUnknownAssert, Status: 422, Title: "Assert names an unknown relation kind", Audience: "person", Class: problemClassCorrection, Values: []string{"kind"}},
	{ID: problemRelationEdgeClassNotAsserted, Status: 422, Title: "Assert is sent an edge_class other than asserted", Audience: "person", Class: problemClassCorrection, Values: []string{"allowed"}},
	{ID: problemDecisionSourcesTooMany, Status: 422, Title: "A Decision carries more sources than the maximum", Rule: "D148", Audience: "person", Class: problemClassCorrection, Values: []string{"count", "limit"}},
	{ID: problemDecisionSourceDuplicate, Status: 422, Title: "Two sources name the same repository, commit, path and anchor", Rule: "D148", Audience: "person", Class: problemClassCorrection, Values: []string{"index"}},
	{ID: problemFieldImmutable, Status: 422, Title: "An update tries to change a server-assigned or flow-controlled field (address, address_no, status, other immutable keys)", Rule: "D110", Audience: "person", Class: problemClassRefusal},
	{ID: problemContentTypeRedefineIncompatible, Status: 422, Title: "A redefinition would remove/retype a field items hold, make one required, add a required field, or change url_prefix", Audience: "person", Class: problemClassRefusal, Values: []string{"from_type", "to_type"}},
	{ID: problemAddressPrefixReserved, Status: 422, Title: "A type asks for a prefix other than the one reserved for it", Rule: "D110", Audience: "person", Class: problemClassRefusal, Values: []string{"type", "prefix"}},
	{ID: problemAddressPrefixSingleLetter, Status: 422, Title: "A non-core type asks for a single-letter prefix", Rule: "D110", Audience: "person", Class: problemClassCorrection, Values: []string{"prefix", "type"}},
	{ID: problemAddressPrefixFixed, Status: 422, Title: "Changing the prefix of a type that already has addresses", Rule: "D110", Audience: "person", Class: problemClassRefusal, Values: []string{"type", "prefix"}},
	{ID: problemAddressPrefixTaken, Status: 422, Title: "The prefix belongs to another type", Rule: "D110", Audience: "person", Class: problemClassCorrection, Values: []string{"prefix", "type"}},
	{ID: problemAddressPrefixUnavailable, Status: 422, Title: "No prefix can be proposed (name too short, or every candidate taken)", Rule: "D110", Audience: "person", Class: problemClassCorrection, Values: []string{"type"}},
	{ID: problemAddressPrefixWebContent, Status: 422, Title: "A web-content type (url_prefix) is given an address prefix", Rule: "D110", Audience: "person", Class: problemClassCorrection},
	{ID: problemPlacesMigrateAddressHeld, Status: 422, Title: "A legacy place's address is already held by the compiled type", Audience: "operator", Class: problemClassRefusal, Values: []string{"type", "record", "holder_type", "holder"}},
	{ID: problemWebhookUrlNotAllowed, Status: 422, Title: "A webhook URL is not HTTPS, targets localhost/.local, cannot resolve, or resolves to a private IP", Audience: "person", Class: problemClassCorrection},
	{ID: problemConfigInvalid, Status: 422, Title: "ValidateConfig rejects a Config field", Rule: "T969", Audience: "person", Class: problemClassCorrection, Values: []string{"value", "min"}},
	{ID: problemIdentitySchemaNotReady, Status: 422, Title: "Minting a classified token before the token/identity columns exist", Rule: "D165", Audience: "operator", Class: problemClassFailure},
	{ID: problemAmendsNoDatabase, Status: 422, Title: "amends cannot be checked because no database is configured", Audience: "person", Class: problemClassFailure},
	{ID: problemRoleLevelMismatch, Status: 422, Title: "A role is registered twice with different levels", Audience: "developer", Class: problemClassCorrection},
	{ID: problemValidationFailed, Status: 422, Title: "Several fields failed validation; each field carries its own type", Audience: "person", Class: problemClassCorrection},
	{ID: problemFieldRequired, Status: 422, Title: "A required value is empty", Audience: "person", Class: problemClassCorrection},
	{ID: problemFieldTooLong, Status: 422, Title: "A string exceeds its length limit", Audience: "person", Class: problemClassCorrection, Values: []string{"limit"}},
	{ID: problemFieldTooShort, Status: 422, Title: "A string is under its minimum length", Audience: "person", Class: problemClassCorrection, Values: []string{"min"}},
	{ID: problemFieldOutOfRange, Status: 422, Title: "A number is outside its allowed range (negative, zero, below/above bound, inconsistent count)", Audience: "person", Class: problemClassCorrection, Values: []string{"min", "max", "value"}},
	{ID: problemFieldTooMany, Status: 422, Title: "A list has more entries than allowed", Audience: "person", Class: problemClassCorrection, Values: []string{"limit"}},
	{ID: problemFieldOneLine, Status: 422, Title: "A value must be a single line", Audience: "person", Class: problemClassCorrection},
	{ID: problemFieldInvalidFormat, Status: 422, Title: "The value does not match the required pattern or syntax", Audience: "person", Class: problemClassCorrection, Values: []string{"value"}},
	{ID: problemFieldNotInList, Status: 422, Title: "The value is not one of a closed list or a registered set", Audience: "person", Class: problemClassCorrection, Values: []string{"allowed", "value", "schema_field"}},
	{ID: problemFieldTypeMismatch, Status: 422, Title: "A dynamic field's JSON value has the wrong type", Audience: "person", Class: problemClassCorrection, Values: []string{"expected"}},
	{ID: problemFieldUnknown, Status: 422, Title: "A dynamic item names a field the schema does not have", Audience: "person", Class: problemClassCorrection},
	{ID: problemSchemaFieldInvalid, Status: 422, Title: "A content type definition has an illegal field (reserved name, bad max_length, max_length on non-string, reference on non-string, title over the D112 limit)", Audience: "person", Class: problemClassCorrection, Values: []string{"schema_field", "value", "field_type", "type", "limit"}},
}

// problemTypeIndex finds an entry by id.
var problemTypeIndex = func() map[ProblemType]ProblemTypeInfo {
	m := make(map[ProblemType]ProblemTypeInfo, len(problemTypeList))
	for _, p := range problemTypeList {
		m[p.ID] = p
	}
	return m
}()

// ProblemTypes returns the closed list of refusal types, retired ones
// included, as a copy.
func ProblemTypes() []ProblemTypeInfo {
	out := append([]ProblemTypeInfo(nil), problemTypeList...)
	for i := range out {
		out[i].Values = append([]string(nil), out[i].Values...)
	}
	return out
}

// problemTyped is an error that carries its refusal type.
type problemTyped interface{ problemType() ProblemType }

// ProblemTypeOf returns the refusal type err carries, or "" when it carries
// none. It looks through wrapped errors, and gives the two sentinels whose
// meaning is a single type, [ErrRevConflict] and [ErrLastAdmin], their type.
//
//	if smeldr.ProblemTypeOf(err) == "decision.place_missing" { ... }
func ProblemTypeOf(err error) ProblemType {
	if err == nil {
		return ""
	}
	var pt problemTyped
	if errors.As(err, &pt) {
		if t := pt.problemType(); t != "" {
			return t
		}
	}
	var rc *RevConflictError
	var lr *liveRelationExists
	switch {
	case errors.As(err, &rc), errors.Is(err, ErrRevConflict):
		return problemRevConflict
	case errors.As(err, &lr):
		return problemRelationAlreadyExists
	case errors.Is(err, ErrLastAdmin):
		return problemTokenLastAdmin
	}
	return ""
}

// problemValues are the named values a refusal carries (D174), keyed by the
// names its type declares. A site writes the keys as literals, so a test can
// hold each site to its type's declared set.
type problemValues map[string]string

// problemValued is an error that carries named values.
type problemValued interface{ problemValues() problemValues }

// ProblemValuesOf returns the named values err's refusal carries, as a copy,
// or nil when it carries none. Each name is one its type declares
// ([ProblemTypeInfo.Values]); each value is a string (a record as its
// address, a number in decimal, a list joined with ", ") and never says more
// than the message does. A UI fills its own text from them.
//
//	smeldr.ProblemValuesOf(err)["decision"] // "D118"
func ProblemValuesOf(err error) map[string]string {
	if err == nil {
		return nil
	}
	var pv problemValued
	if errors.As(err, &pv) {
		if v := pv.problemValues(); len(v) > 0 {
			return copyValues(v)
		}
	}
	var rc *RevConflictError
	var lr *liveRelationExists
	switch {
	case errors.As(err, &rc):
		return copyValues(problemValues{"type": rc.Type, "record": rc.ID,
			"rev_sent": strconv.Itoa(rc.Sent), "rev_current": strconv.Itoa(rc.Current)})
	case errors.As(err, &lr):
		return copyValues(problemValues{"relation": lr.ID})
	}
	return nil
}

// copyValues copies v, nil when it is empty.
func copyValues(v problemValues) map[string]string {
	if len(v) == 0 {
		return nil
	}
	out := make(map[string]string, len(v))
	for k, x := range v {
		out[k] = x
	}
	return out
}

// typedError gives err a refusal type and its values without changing it:
// Error, errors.Is and errors.As see err as before.
type typedError struct {
	t   ProblemType
	v   problemValues
	err error
}

func (e *typedError) Error() string                { return e.err.Error() }
func (e *typedError) Unwrap() error                { return e.err }
func (e *typedError) problemType() ProblemType     { return e.t }
func (e *typedError) problemValues() problemValues { return e.v }

// problem gives err, a 4xx sentinel or an error wrapping one, the refusal
// type t and its values v (nil for none). Every core refusal site names its
// type this way (or with refuse or fieldErr), so the type is fixed at the
// site and never derived from text.
func problem(t ProblemType, err error, v problemValues) error {
	return &typedError{t: t, v: v, err: err}
}

// refuse is [Refused] with a refusal type and its values.
func refuse(t ProblemType, reason string, v problemValues) *RefusalError {
	return &RefusalError{Reason: reason, typ: t, values: v}
}

// fieldErr is [Err] with a refusal type and its values for the field.
func fieldErr(t ProblemType, field, message string, v problemValues) *ValidationError {
	return &ValidationError{fields: []fieldError{{field: field, message: message, typ: t, values: v}}}
}

// problemTitle is the RFC 9457 title for t answering with status: the list's
// title, or the status text for an untyped error.
func problemTitle(t ProblemType, status int) string {
	if p, ok := problemTypeIndex[t]; ok {
		return p.Title
	}
	return http.StatusText(status)
}

// problemRule is the rule t enforces, or "".
func problemRule(t ProblemType) string {
	return problemTypeIndex[t].Rule
}

// problemClass is t's class, or "" for an untyped error.
func problemClass(t ProblemType) string {
	return problemTypeIndex[t].Class
}
