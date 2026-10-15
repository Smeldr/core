package smeldr

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
