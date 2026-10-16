package smeldr

import (
	"fmt"
	"log/slog"
	"slices"
)

// ReasonRule says whether a [Capability] takes a reason, and who decides.
type ReasonRule string

const (
	// ReasonNotTaken: the act takes no reason.
	ReasonNotTaken ReasonRule = "none"
	// ReasonOptional: a reason may be given, and it is recorded.
	ReasonOptional ReasonRule = "optional"
	// ReasonRequired: the act is refused without a reason.
	ReasonRequired ReasonRule = "required"
	// ReasonPerFlow: the type's state flow decides per transition (D96).
	ReasonPerFlow ReasonRule = "flow"
)

// The resources a [Capability] acts on (D94). Resources are the nouns every
// surface shares: the MCP tools, the REST routes, the stream endpoint and the
// CLI name the same resource the same way.
const (
	// ResourceItem is any content item, compiled or dynamic, addressed by
	// its type as an argument. Adding a type never adds a capability.
	ResourceItem = "item"
	// ResourceType is a content type's schema and state flow.
	ResourceType = "type"
	// ResourceRelation is a relation edge between two items, and its kinds.
	ResourceRelation = "relation"
	// ResourceSignal is a protocol Signal.
	ResourceSignal = "signal"
	// ResourceAccess is tokens, role grants, roles and delegations.
	ResourceAccess = "access"
	// ResourceOps is operational state: sweeps, checks, findings, the
	// stewardship inbox and goal context.
	ResourceOps = "ops"
	// ResourceSite is the editorial site surface: navigation, redirects,
	// preview links and upload tokens.
	ResourceSite = "site"
	// ResourcePageMeta is per-page SEO metadata (instance infrastructure).
	ResourcePageMeta = "page_meta"
	// ResourceWebhook is an outbound webhook and its deliveries.
	ResourceWebhook = "webhook"
)

// The standard verbs (D94, D96). Every other verb on a [Capability] is a
// custom verb, and its Custom field is true.
const (
	// VerbGet reads one.
	VerbGet = "get"
	// VerbList reads many.
	VerbList = "list"
	// VerbCreate creates one.
	VerbCreate = "create"
	// VerbUpdate changes one.
	VerbUpdate = "update"
	// VerbDelete removes one.
	VerbDelete = "delete"
	// VerbTransition moves an item to a state its type's flow allows: the one
	// lifecycle verb on every surface (D96).
	VerbTransition = "transition"
)

// Capability is one act on one resource: the unit that the MCP, REST and CLI
// surfaces each project (D94). Operation is the governance operation word
// that gates the act (D43), one per (Resource, Verb).
type Capability struct {
	// Resource is one of the Resource* constants.
	Resource string
	// Verb is a standard verb (the Verb* constants) or a custom one.
	Verb string
	// Operation is the governance operation word that gates the act.
	Operation string
	// ReasonRule says whether the act takes a reason.
	ReasonRule ReasonRule
	// Custom is true when Verb is not one of the standard verbs.
	Custom bool
}

// capabilityKey indexes [capabilityTable] by (resource, verb).
type capabilityKey struct{ resource, verb string }

// capabilityTable is the registry, in a fixed order. Its operation words are
// today's (governance.go); a later Task adds its own rows here. Read-only
// after init.
var capabilityTable = []Capability{
	{ResourceItem, VerbGet, "read", ReasonNotTaken, false},
	{ResourceItem, VerbList, "read", ReasonNotTaken, false},
	{ResourceItem, VerbCreate, "create", ReasonNotTaken, false},
	{ResourceItem, VerbUpdate, "update", ReasonOptional, false},
	{ResourceItem, VerbDelete, "delete", ReasonNotTaken, false},
	{ResourceItem, VerbTransition, "manage", ReasonPerFlow, false},
	{ResourceItem, "compose", "manage", ReasonNotTaken, true},

	{ResourceType, VerbGet, "read", ReasonNotTaken, false},
	{ResourceType, VerbList, "read", ReasonNotTaken, false},
	{ResourceType, "define", "define-type", ReasonNotTaken, true},
	{ResourceType, "define_flow", "define-flow", ReasonNotTaken, true},

	{ResourceRelation, VerbGet, "read", ReasonNotTaken, false},
	{ResourceRelation, VerbList, "read", ReasonNotTaken, false},
	{ResourceRelation, "assert", "create", ReasonNotTaken, true},
	{ResourceRelation, "propose", "create", ReasonNotTaken, true},
	{ResourceRelation, "observe", "create", ReasonNotTaken, true},
	{ResourceRelation, "withdraw", "archive", ReasonRequired, true},
	{ResourceRelation, "define", "define-relation-kind", ReasonNotTaken, true},
	{ResourceRelation, "preview_impact", "manage", ReasonNotTaken, true},

	{ResourceSignal, VerbGet, "read", ReasonNotTaken, false},
	{ResourceSignal, VerbList, "read", ReasonNotTaken, false},
	{ResourceSignal, VerbCreate, "create", ReasonNotTaken, false},
	{ResourceSignal, "answer", "update", ReasonRequired, true},
	{ResourceSignal, "withdraw", "archive", ReasonRequired, true},

	{ResourceAccess, VerbList, "administer", ReasonNotTaken, false},
	{ResourceAccess, "list_roles", "read", ReasonNotTaken, true},
	{ResourceAccess, "lookup_names", "read", ReasonNotTaken, true},
	{ResourceAccess, "create_token", "administer", ReasonOptional, true},
	{ResourceAccess, "revoke_token", "administer", ReasonOptional, true},
	{ResourceAccess, "grant", "administer", ReasonOptional, true},
	{ResourceAccess, "revoke", "administer", ReasonOptional, true},
	// delegate and withdraw_delegation are floors: the per-item check runs
	// inside the act, against the actual target.
	{ResourceAccess, "delegate", "read", ReasonNotTaken, true},
	{ResourceAccess, "withdraw_delegation", "read", ReasonNotTaken, true},

	{ResourceOps, VerbGet, "read", ReasonNotTaken, false},
	{ResourceOps, VerbList, "read", ReasonNotTaken, false},
	{ResourceOps, "migrate_addresses", "administer", ReasonNotTaken, true},

	{ResourceSite, VerbList, "read", ReasonNotTaken, false},
	{ResourceSite, VerbCreate, "manage", ReasonNotTaken, false},
	{ResourceSite, VerbUpdate, "manage", ReasonNotTaken, false},
	{ResourceSite, VerbDelete, "manage", ReasonNotTaken, false},
	{ResourceSite, "preview", "publish", ReasonNotTaken, true},
	{ResourceSite, "upload", "create", ReasonNotTaken, true},

	{ResourcePageMeta, VerbGet, "administer", ReasonNotTaken, false},
	{ResourcePageMeta, VerbList, "administer", ReasonNotTaken, false},
	{ResourcePageMeta, VerbUpdate, "administer", ReasonNotTaken, false},
	{ResourcePageMeta, VerbDelete, "administer", ReasonNotTaken, false},

	{ResourceWebhook, VerbList, "administer", ReasonNotTaken, false},
	{ResourceWebhook, VerbCreate, "administer", ReasonNotTaken, false},
	{ResourceWebhook, VerbDelete, "administer", ReasonNotTaken, false},
	{ResourceWebhook, "retry", "administer", ReasonNotTaken, true},
}

// capabilityIndex is capabilityTable keyed by (resource, verb), built once at
// init so [App.CapabilityFor] is one map lookup with no allocation.
var capabilityIndex = func() map[capabilityKey]Capability {
	m := make(map[capabilityKey]Capability, len(capabilityTable))
	for _, c := range capabilityTable {
		m[capabilityKey{c.Resource, c.Verb}] = c
	}
	return m
}()

// Capabilities returns every capability the instance knows, in a fixed order
// (D94). The slice is a copy: changing it does not change the registry. Safe
// on a nil *App.
func (a *App) Capabilities() []Capability {
	out := make([]Capability, len(capabilityTable))
	copy(out, capabilityTable)
	return out
}

// CapabilityFor returns the capability for verb on resource, and false when
// the instance has no such capability. Safe on a nil *App.
func (a *App) CapabilityFor(resource, verb string) (Capability, bool) {
	c, ok := capabilityIndex[capabilityKey{resource, verb}]
	return c, ok
}

// legacyOperationFloor is the legacy role each operation word needs when no
// governance is wired: the floors seedToolPolicies' comment documents.
var legacyOperationFloor = map[string]Role{
	"read":                 Author,
	"create":               Author,
	"update":               Author,
	"publish":              Author,
	"archive":              Author,
	"delete":               Editor,
	"manage":               Editor,
	"administer":           Admin,
	"define-type":          Admin,
	"define-flow":          Admin,
	"define-relation-kind": Admin,
}

// typelessReadsAny lists the read capabilities that, called with no type,
// pass when the caller reads at least one item of any type, because their
// surface filters per item. Every other type-less read needs a scope that
// reads everything ([ReadScope.All]): the read rules fail toward less access.
var typelessReadsAny = map[capabilityKey]bool{
	{ResourceSignal, VerbList}:   true, // list_signals filters Signals by their subject
	{ResourceRelation, VerbList}: true, // ListRelations keeps edges with both ends readable
}

// typedReads lists the read capabilities called with an item type (and often
// an ID), so a caller who reads at least one item of some type can use them:
// the type's own read rules then decide per call.
var typedReads = map[capabilityKey]bool{
	{ResourceItem, VerbGet}:     true,
	{ResourceItem, VerbList}:    true,
	{ResourceRelation, VerbGet}: true, // the relations of one item
}

// AuthorizeCapability reports whether the caller in ctx may perform verb on
// resource at target: nil when it may. It applies the same rules on every
// surface (D94, D96):
//
//   - an unknown (resource, verb) is an [ErrBadRequest] error;
//   - without governance, the caller's legacy role must meet the operation's
//     floor (read, create, update, publish and archive need Author; delete and
//     manage Editor; administer and define-* Admin), else [ErrForbidden];
//   - under governance a caller with no token ID is [ErrForbidden];
//   - a read goes through the caller's read scope (the one attached to ctx,
//     else the caller's own): with a type and no ID it needs the type to be
//     readable ([ErrForbidden] otherwise); with an ID, an item the caller may
//     not read is [ErrNotFound], exactly as a missing one; with no type it needs
//     a scope that reads everything, except signal list and relation list,
//     whose surfaces filter per item. Only item get, item list and relation get
//     use the target's type; any other read ignores it and needs the type-less
//     rule;
//   - any other operation on an item the caller may not read is [ErrNotFound];
//     otherwise [RoleStore.Authorized] decides, and false is [ErrForbidden].
//
// A failure to load the scope or the grants is an [ErrInternal] error, never a
// pass. The state flow's own per-transition gate stays inside
// [App.TransitionItemVia].
func (a *App) AuthorizeCapability(ctx Context, resource, verb string, target AuthTarget) error {
	c, ok := capabilityIndex[capabilityKey{resource, verb}]
	if !ok {
		return fmt.Errorf("%w: unknown capability %q %q", ErrBadRequest, resource, verb)
	}
	user := ctx.User()
	if a.governance == nil {
		if floor, ok := legacyOperationFloor[c.Operation]; ok && user.HasRole(floor) {
			return nil
		}
		return ErrForbidden
	}
	if user.ID == "" {
		return ErrForbidden
	}
	needsScope := c.Operation == "read" || (target.TypeName != "" && target.ID != "")
	var scope *ReadScope
	if needsScope {
		s, err := scopeFor(ctx, a.governance)
		if err != nil {
			return err
		}
		scope = s
	}
	if c.Operation == "read" {
		return authorizeRead(scope, c, target)
	}
	if target.TypeName != "" && target.ID != "" && !scope.Readable(target.TypeName, target.ID) {
		return ErrNotFound
	}
	ok, err := a.governance.Authorized(ctx, user.ID, c.Operation, target)
	if err != nil {
		slog.ErrorContext(ctx, "smeldr: capability authorization failed", "resource", c.Resource, "verb", c.Verb, "token", user.ID, "error", err)
		return fmt.Errorf("%w: authorize %s %s: %s", ErrInternal, c.Resource, c.Verb, err)
	}
	if !ok {
		return ErrForbidden
	}
	return nil
}

// authorizeRead is [App.AuthorizeCapability]'s read rule under governance.
// Only the typed reads ([typedReads]) take the target's type into account; any
// other read capability ignores it and applies the type-less rule, so passing a
// type cannot open a read whose surface does not filter by that type.
func authorizeRead(scope *ReadScope, c Capability, target AuthTarget) error {
	key := capabilityKey{c.Resource, c.Verb}
	switch {
	case target.TypeName == "" || !typedReads[key]:
		if scope.All() || (typelessReadsAny[key] && scope.readsAny()) {
			return nil
		}
		return ErrForbidden
	case !scope.TypeReadable(target.TypeName):
		return ErrForbidden
	case target.ID != "" && !scope.Readable(target.TypeName, target.ID):
		return ErrNotFound
	}
	return nil
}

// VisibleCapabilities returns the capabilities the caller in ctx may use on at
// least one target, in registry order: the set a tool list filters by.
//
// Without governance it is every capability whose operation's legacy floor the
// caller's role meets. Under governance a caller with no token ID sees none; a
// read capability is visible when the caller's scope reads everything, or
// reads at least one item and the capability is called with an item type (item
// get and list, relation get) or filters per item (signal list, relation
// list); any other capability is visible when one of the caller's unexpired
// grants holds its operation, in any scope mode. A failure to load the scope or
// the grants is an [ErrInternal] error and no list: never a full or partial one.
func (a *App) VisibleCapabilities(ctx Context) ([]Capability, error) {
	user := ctx.User()
	out := make([]Capability, 0, len(capabilityTable))
	if a.governance == nil {
		for _, c := range capabilityTable {
			if floor, ok := legacyOperationFloor[c.Operation]; ok && user.HasRole(floor) {
				out = append(out, c)
			}
		}
		return out, nil
	}
	if user.ID == "" {
		return out, nil
	}
	scope, err := scopeFor(ctx, a.governance)
	if err != nil {
		return nil, err
	}
	ops, err := a.governance.grantedOperations(ctx, user.ID)
	if err != nil {
		slog.ErrorContext(ctx, "smeldr: visible capabilities could not be loaded", "token", user.ID, "error", err)
		return nil, fmt.Errorf("%w: visible capabilities: %s", ErrInternal, err)
	}
	for _, c := range capabilityTable {
		if c.Operation == "read" {
			key := capabilityKey{c.Resource, c.Verb}
			if scope.All() || ((typedReads[key] || typelessReadsAny[key]) && scope.readsAny()) {
				out = append(out, c)
			}
			continue
		}
		if ops[c.Operation] {
			out = append(out, c)
		}
	}
	return out, nil
}

// capabilityWarnings lists the warning codes each capability's act can return
// next to its result (the D110 CreateWarning channel): a write that succeeds
// but should be corrected. Read-only after init.
var capabilityWarnings = map[capabilityKey][]string{
	{ResourceItem, VerbCreate}: {"slug_adjusted", "deprecated_field", "title_missing", "title_too_long", "field_too_long"},
	{ResourceItem, VerbUpdate}: {"deprecated_field", "title_missing", "title_too_long", "field_too_long"},
	{ResourceType, "define"}:   {"title_field_missing"},
}

// CapabilityWarnings returns the warning codes the act of verb on resource can
// return with a successful result, in a fixed order; nil when it returns
// none or the capability is unknown. The slice is a copy. Safe on a nil *App.
func (a *App) CapabilityWarnings(resource, verb string) []string {
	return slices.Clone(capabilityWarnings[capabilityKey{resource, verb}])
}
