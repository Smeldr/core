package smeldr

import (
	"encoding/json"
	"strings"
	"testing"
)

// knownOperations parses the admin role's seed literal, the one source of the
// operation words governance knows (governance.go).
func knownOperations(t testing.TB) map[string]bool {
	t.Helper()
	var ops []string
	if err := json.Unmarshal([]byte(adminRoleOperations), &ops); err != nil {
		t.Fatalf("adminRoleOperations is not a JSON array: %v", err)
	}
	set := make(map[string]bool, len(ops))
	for _, op := range ops {
		set[op] = true
	}
	return set
}

// capabilityRef names one (resource, verb) pair of the registry.
type capabilityRef struct{ resource, verb string }

// builtInToolCapability maps every built-in tool that is not a per-type tool
// to its capability. The per-type tools (create_post, get_node, ...) map by
// their verb prefix, see perTypeToolCapability. Test-only in C1: no exported
// API exposes this map (C3 decides that surface).
var builtInToolCapability = map[string]capabilityRef{
	// Dynamic content and item reads.
	"create_content":        {ResourceItem, VerbCreate},
	"get_content":           {ResourceItem, VerbGet},
	"list_content":          {ResourceItem, VerbList},
	"update_content":        {ResourceItem, VerbUpdate},
	"set_content_status":    {ResourceItem, VerbTransition},
	"schedule_content":      {ResourceItem, VerbTransition},
	"transition_item":       {ResourceItem, VerbTransition},
	"get_valid_transitions": {ResourceItem, VerbGet},
	"list_items_by_state":   {ResourceItem, VerbList},
	"get_item_standing":     {ResourceItem, VerbGet},
	"get_item_provenance":   {ResourceItem, VerbGet},
	// Composition.
	"add_section":      {ResourceItem, "compose"},
	"reorder_sections": {ResourceItem, "compose"},
	"remove_section":   {ResourceItem, "compose"},
	"add_item":         {ResourceItem, "compose"},
	"reorder_items":    {ResourceItem, "compose"},
	"remove_item":      {ResourceItem, "compose"},
	// Types and flows.
	"define_content_type":       {ResourceType, "define"},
	"redefine_content_type":     {ResourceType, "define"},
	"get_content_type_schema":   {ResourceType, VerbGet},
	"list_content_type_schemas": {ResourceType, VerbList},
	"list_type_tools":           {ResourceType, VerbList},
	"define_state_flow":         {ResourceType, "define_flow"},
	// Relations.
	"assert_relation":      {ResourceRelation, "assert"},
	"propose_relation":     {ResourceRelation, "propose"},
	"observe_relation":     {ResourceRelation, "observe"},
	"withdraw_relation":    {ResourceRelation, "withdraw"},
	"get_relations":        {ResourceRelation, VerbGet},
	"get_reachability":     {ResourceRelation, VerbGet},
	"list_relations":       {ResourceRelation, VerbList},
	"list_relation_kinds":  {ResourceRelation, VerbList},
	"upsert_relation_kind": {ResourceRelation, "define"},
	"preview_impact":       {ResourceRelation, "preview_impact"},
	// Signals.
	"create_signal": {ResourceSignal, VerbCreate},
	"list_signals":  {ResourceSignal, VerbList},
	// Access.
	"create_token":        {ResourceAccess, "create_token"},
	"list_tokens":         {ResourceAccess, VerbList},
	"revoke_token":        {ResourceAccess, "revoke_token"},
	"grant_role":          {ResourceAccess, "grant"},
	"list_grants":         {ResourceAccess, VerbList},
	"revoke_grant":        {ResourceAccess, "revoke"},
	"list_roles":          {ResourceAccess, "list_roles"},
	"lookup_token_names":  {ResourceAccess, "lookup_names"},
	"delegate_item":       {ResourceAccess, "delegate"},
	"withdraw_delegation": {ResourceAccess, "withdraw_delegation"},
	// Ops.
	"get_goal_context":      {ResourceOps, VerbGet},
	"get_sweep_run":         {ResourceOps, VerbGet},
	"get_stewardship_inbox": {ResourceOps, VerbGet},
	"get_check_status":      {ResourceOps, VerbGet},
	"list_findings":         {ResourceOps, VerbList},
	"migrate_addresses":     {ResourceOps, "migrate_addresses"},
	// Site.
	"list_nav_items":      {ResourceSite, VerbList},
	"create_nav_item":     {ResourceSite, VerbCreate},
	"update_nav_item":     {ResourceSite, VerbUpdate},
	"delete_nav_item":     {ResourceSite, VerbDelete},
	"create_redirect":     {ResourceSite, VerbCreate},
	"list_redirects":      {ResourceSite, VerbList},
	"delete_redirect":     {ResourceSite, VerbDelete},
	"create_preview_url":  {ResourceSite, "preview"},
	"create_upload_token": {ResourceSite, "upload"},
	// Page meta.
	"set_page_meta":    {ResourcePageMeta, VerbUpdate},
	"get_page_meta":    {ResourcePageMeta, VerbGet},
	"delete_page_meta": {ResourcePageMeta, VerbDelete},
	"list_page_meta":   {ResourcePageMeta, VerbList},
	// Webhooks.
	"create_webhook":          {ResourceWebhook, VerbCreate},
	"list_webhooks":           {ResourceWebhook, VerbList},
	"delete_webhook":          {ResourceWebhook, VerbDelete},
	"list_webhook_deliveries": {ResourceWebhook, VerbList},
	"retry_webhook":           {ResourceWebhook, "retry"},
}

// perTypeToolTypes are the type parts of the per-type tools the seed lists
// (singular for one item, plural for list_).
var perTypeToolTypes = map[string]bool{
	"post": true, "posts": true,
	"story": true, "stories": true,
	"essay": true, "essays": true,
	"doc_page": true, "doc_pages": true,
	"node": true, "nodes": true,
}

// perTypeToolVerbs maps a per-type tool's verb prefix to its item verb. The
// lifecycle prefixes all become the one transition verb (D96).
var perTypeToolVerbs = map[string]string{
	"create":   VerbCreate,
	"get":      VerbGet,
	"list":     VerbList,
	"update":   VerbUpdate,
	"delete":   VerbDelete,
	"publish":  VerbTransition,
	"schedule": VerbTransition,
	"archive":  VerbTransition,
}

// perTypeToolCapability maps a per-type tool such as create_post or list_nodes
// to its item capability, and reports the verb prefix.
func perTypeToolCapability(tool string) (ref capabilityRef, prefix string, ok bool) {
	prefix, typ, found := strings.Cut(tool, "_")
	if !found || !perTypeToolTypes[typ] {
		return capabilityRef{}, "", false
	}
	verb, known := perTypeToolVerbs[prefix]
	if !known {
		return capabilityRef{}, "", false
	}
	return capabilityRef{ResourceItem, verb}, prefix, true
}

// toolCapability resolves a built-in tool to its capability.
func toolCapability(tool string) (ref capabilityRef, prefix string, ok bool) {
	if ref, ok := builtInToolCapability[tool]; ok {
		return ref, "", true
	}
	return perTypeToolCapability(tool)
}

// operationDivergence lists the built-in tools whose seeded operation differs
// from their capability's, each with its reason. The seeded row stays
// authoritative for the typed tool; the capability path uses the capability's
// operation (C2).
// A new divergence fails TestCapabilities_ToolOperationAgrees until it is
// listed here.
var operationDivergence = map[string]string{
	"create_content": "typed row kept at manage (Editor); the capability path uses item create's own operation, one gate for every type (D94, C2)",
	"update_content": "typed row kept at manage (Editor); the capability path uses item update's own operation, one gate for every type (D94, C2)",
}

// perTypeLifecycleDivergence: the per-type publish_/schedule_/archive_ tools
// are gated on publish/archive, while the one transition verb is gated on
// manage (D96). They survive in the typed profile only.
var perTypeLifecycleDivergence = map[string]bool{
	"publish":  true,
	"schedule": true,
	"archive":  true,
}

func TestCapabilities_OperationsKnown(t *testing.T) {
	known := knownOperations(t)
	for _, c := range capabilityTable {
		if !known[c.Operation] {
			t.Errorf("capability %s %s names operation %q, not a known operation word", c.Resource, c.Verb, c.Operation)
		}
	}
}

func TestCapabilities_EveryBuiltInToolMapped(t *testing.T) {
	var app *App
	for _, p := range builtInToolPolicies {
		ref, _, ok := toolCapability(p.tool)
		if !ok {
			t.Errorf("built-in tool %q maps to no capability: add it to builtInToolCapability", p.tool)
			continue
		}
		if _, ok := app.CapabilityFor(ref.resource, ref.verb); !ok {
			t.Errorf("built-in tool %q maps to %s %s, which the registry does not have", p.tool, ref.resource, ref.verb)
		}
	}
}

func TestCapabilities_ToolOperationAgrees(t *testing.T) {
	var app *App
	usedDivergence := map[string]bool{}
	for _, p := range builtInToolPolicies {
		ref, prefix, ok := toolCapability(p.tool)
		if !ok {
			continue // reported by TestCapabilities_EveryBuiltInToolMapped
		}
		c, ok := app.CapabilityFor(ref.resource, ref.verb)
		if !ok {
			continue
		}
		if c.Operation == p.op {
			continue
		}
		if _, listed := operationDivergence[p.tool]; listed {
			usedDivergence[p.tool] = true
			continue
		}
		if perTypeLifecycleDivergence[prefix] {
			continue
		}
		t.Errorf("built-in tool %q is gated on %q, its capability %s %s on %q: list it in operationDivergence with a reason, or align them",
			p.tool, p.op, c.Resource, c.Verb, c.Operation)
	}
	for tool := range operationDivergence {
		if !usedDivergence[tool] {
			t.Errorf("operationDivergence lists %q, which no longer diverges: remove it", tool)
		}
	}
}

func TestCapabilities_TableInvariants(t *testing.T) {
	resources := map[string]bool{
		ResourceItem: true, ResourceType: true, ResourceRelation: true,
		ResourceSignal: true, ResourceAccess: true, ResourceOps: true,
		ResourceSite: true, ResourcePageMeta: true, ResourceWebhook: true,
		ResourceSubscription: true,
	}
	standard := map[string]bool{
		VerbGet: true, VerbList: true, VerbCreate: true,
		VerbUpdate: true, VerbDelete: true, VerbTransition: true,
	}
	rules := map[ReasonRule]bool{
		ReasonNotTaken: true, ReasonOptional: true, ReasonRequired: true, ReasonPerFlow: true,
	}
	seen := map[capabilityKey]bool{}
	for _, c := range capabilityTable {
		key := capabilityKey{c.Resource, c.Verb}
		if seen[key] {
			t.Errorf("duplicate capability %s %s", c.Resource, c.Verb)
		}
		seen[key] = true
		if !resources[c.Resource] {
			t.Errorf("capability %s %s: resource is not a Resource* constant", c.Resource, c.Verb)
		}
		if c.Verb == "" {
			t.Errorf("capability on %s has an empty verb", c.Resource)
		}
		if c.Custom == standard[c.Verb] {
			t.Errorf("capability %s %s: Custom = %v, want %v", c.Resource, c.Verb, c.Custom, !standard[c.Verb])
		}
		if !rules[c.ReasonRule] {
			t.Errorf("capability %s %s: ReasonRule %q is not a ReasonRule constant", c.Resource, c.Verb, c.ReasonRule)
		}
	}
	if len(capabilityIndex) != len(capabilityTable) {
		t.Errorf("capabilityIndex has %d entries, capabilityTable %d", len(capabilityIndex), len(capabilityTable))
	}
}

func TestCapabilities_ReturnsCopy(t *testing.T) {
	app := &App{}
	first := app.Capabilities()
	if len(first) != len(capabilityTable) {
		t.Fatalf("Capabilities() has %d entries, want %d", len(first), len(capabilityTable))
	}
	first[0].Operation = "administer"
	first[0].Verb = "mutated"
	second := app.Capabilities()
	if second[0] != capabilityTable[0] {
		t.Errorf("mutating the returned slice changed the registry: %+v", second[0])
	}
	for i := range second {
		if second[i] != capabilityTable[i] {
			t.Fatalf("Capabilities()[%d] = %+v, want table order %+v", i, second[i], capabilityTable[i])
		}
	}
	if c, _ := app.CapabilityFor(ResourceItem, VerbGet); c.Operation != "read" {
		t.Errorf("CapabilityFor(item, get) after mutation = %+v", c)
	}
}

func TestCapabilities_NilApp(t *testing.T) {
	var app *App
	if got := len(app.Capabilities()); got != len(capabilityTable) {
		t.Errorf("nil App Capabilities() has %d entries, want %d", got, len(capabilityTable))
	}
	c, ok := app.CapabilityFor(ResourceItem, VerbTransition)
	if !ok {
		t.Fatal("nil App CapabilityFor(item, transition) = false")
	}
	want := Capability{ResourceItem, VerbTransition, "manage", ReasonPerFlow, false}
	if c != want {
		t.Errorf("CapabilityFor(item, transition) = %+v, want %+v", c, want)
	}
}

func TestCapabilityFor_Unknown(t *testing.T) {
	app := &App{}
	tests := []struct {
		name, resource, verb string
	}{
		{"UnknownResource", "nope", VerbGet},
		{"UnknownVerb", ResourceItem, "nope"},
		{"Empty", "", ""},
		{"VerbOnWrongResource", ResourceSignal, VerbDelete},
		{"CaseSensitive", "Item", "Get"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, ok := app.CapabilityFor(tt.resource, tt.verb)
			if ok || c != (Capability{}) {
				t.Errorf("CapabilityFor(%q, %q) = %+v, %v; want zero, false", tt.resource, tt.verb, c, ok)
			}
		})
	}
}

func TestPerTypeToolCapability(t *testing.T) {
	tests := []struct {
		tool   string
		want   capabilityRef
		wantOK bool
	}{
		{"create_post", capabilityRef{ResourceItem, VerbCreate}, true},
		{"list_doc_pages", capabilityRef{ResourceItem, VerbList}, true},
		{"archive_node", capabilityRef{ResourceItem, VerbTransition}, true},
		{"create_widget", capabilityRef{}, false},
		{"frobnicate_post", capabilityRef{}, false},
		{"post", capabilityRef{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.tool, func(t *testing.T) {
			got, _, ok := perTypeToolCapability(tt.tool)
			if ok != tt.wantOK || got != tt.want {
				t.Errorf("perTypeToolCapability(%q) = %+v, %v; want %+v, %v", tt.tool, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func BenchmarkCapabilityFor(b *testing.B) {
	app := &App{}
	b.ReportAllocs()
	for b.Loop() {
		if _, ok := app.CapabilityFor(ResourceItem, VerbTransition); !ok {
			b.Fatal("missing item transition")
		}
	}
}
