package smeldr

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

// capabilityFixture is readSurfaceFixture (Tasks "seen" and "hidden"; "reader"
// reads only "seen"; "global" holds admin) plus write grants for the tests.
type capabilityFixture struct {
	readSurfaceFixture
}

func newCapabilityFixture(t *testing.T) capabilityFixture {
	t.Helper()
	f := capabilityFixture{newReadSurfaceFixture(t)}
	setupRelationsTable(t, f.db)
	ctx := context.Background()
	for _, def := range []RoleDefinition{
		{Name: "static-updater", Operations: []string{"read", "update"}, ScopeMode: ScopeStatic},
		{Name: "dynamic-updater", Operations: []string{"update"}, ScopeMode: ScopeDynamic, ScopeRelationKind: "contains", ScopeDirection: "outgoing"},
		{Name: "global-editor", Operations: []string{"create", "read", "update", "publish", "archive", "delete", "manage"}, ScopeMode: ScopeGlobal},
		{Name: "global-writer", Operations: []string{"update"}, ScopeMode: ScopeGlobal},
	} {
		if err := f.rs.DefineRole(ctx, def); err != nil {
			t.Fatalf("DefineRole %s: %v", def.Name, err)
		}
	}
	grant := func(g RoleGrant) {
		if _, err := f.rs.Grant(ctx, g); err != nil {
			t.Fatalf("Grant %s: %v", g.RoleName, err)
		}
	}
	grant(RoleGrant{TokenID: "static", RoleName: "static-updater", ScopeStatic: []string{"Task:" + f.seen.ID}})
	grant(RoleGrant{TokenID: "dynamic", RoleName: "scoped-reader", ScopeStatic: []string{"Task:" + f.seen.ID}})
	grant(RoleGrant{TokenID: "dynamic", RoleName: "dynamic-updater", ScopeAnchorID: "A"})
	grant(RoleGrant{TokenID: "editor", RoleName: "global-editor"})
	grant(RoleGrant{TokenID: "writer", RoleName: "global-writer"})
	past := time.Now().UTC().Add(-time.Hour)
	grant(RoleGrant{TokenID: "expired", RoleName: "global-editor", ExpiresAt: &past})
	if _, err := f.db.ExecContext(ctx,
		`INSERT INTO smeldr_relations (id, source_type, source_id, target_type, target_id, relation_kind, edge_class, attributes, created_at, updated_at)
		 VALUES (?, 'Domain', 'A', 'Task', ?, 'contains', 'asserted', '{}', datetime('now'), datetime('now'))`,
		NewID(), f.seen.ID); err != nil {
		t.Fatalf("insert edge: %v", err)
	}
	return f
}

func (f capabilityFixture) as(token string) Context { return NewTestContext(User{ID: token}) }

func (f capabilityFixture) item(task *Task) AuthTarget {
	return AuthTarget{TypeName: "Task", ID: task.ID}
}

func TestAuthorizeCapability_Unknown(t *testing.T) {
	f := newCapabilityFixture(t)
	for _, pair := range [][2]string{{"nope", VerbGet}, {ResourceItem, "nope"}, {"", ""}} {
		if err := f.app.AuthorizeCapability(f.global, pair[0], pair[1], AuthTarget{}); !errors.Is(err, ErrBadRequest) {
			t.Errorf("%q %q: %v; want ErrBadRequest", pair[0], pair[1], err)
		}
	}
	legacy := New(Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16")})
	if err := legacy.AuthorizeCapability(NewTestContext(User{Roles: []Role{Admin}}), "nope", VerbGet, AuthTarget{}); !errors.Is(err, ErrBadRequest) {
		t.Errorf("without governance: %v; want ErrBadRequest", err)
	}
}

func TestAuthorizeCapability_LegacyFloors(t *testing.T) {
	app := New(Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16")})
	rank := map[Role]int{Guest: 0, Author: 1, Editor: 2, Admin: 3}
	for _, role := range []Role{Guest, Author, Editor, Admin} {
		ctx := NewTestContext(User{ID: "u", Roles: []Role{role}})
		for _, c := range capabilityTable {
			floor := legacyOperationFloor[c.Operation]
			err := app.AuthorizeCapability(ctx, c.Resource, c.Verb, AuthTarget{TypeName: "Task", ID: "x"})
			if want := rank[role] >= rank[floor]; (err == nil) != want {
				t.Errorf("%s on %s %s (%s): %v; want allowed %v", role, c.Resource, c.Verb, c.Operation, err, want)
			}
			if err != nil && !errors.Is(err, ErrForbidden) {
				t.Errorf("%s on %s %s: %v; want ErrForbidden", role, c.Resource, c.Verb, err)
			}
		}
	}
}

func TestLegacyOperationFloor_CoversKnownOperations(t *testing.T) {
	known := knownOperations(t)
	for op := range known {
		if _, ok := legacyOperationFloor[op]; !ok {
			t.Errorf("operation %q has no legacy floor", op)
		}
	}
	for op := range legacyOperationFloor {
		if !known[op] {
			t.Errorf("legacy floor for %q, which is not a known operation word", op)
		}
	}
}

func TestAuthorizeCapability_NoTokenID(t *testing.T) {
	f := newCapabilityFixture(t)
	guest := NewTestContext(User{Roles: []Role{Admin}})
	if err := f.app.AuthorizeCapability(guest, ResourceItem, VerbGet, AuthTarget{TypeName: "Task"}); !errors.Is(err, ErrForbidden) {
		t.Errorf("no token ID: %v; want ErrForbidden", err)
	}
}

func TestAuthorizeCapability_ReadTypeFloor(t *testing.T) {
	f := newCapabilityFixture(t)
	if err := f.app.AuthorizeCapability(f.reader, ResourceItem, VerbList, AuthTarget{TypeName: "Task"}); err != nil {
		t.Errorf("readable type: %v", err)
	}
	if err := f.app.AuthorizeCapability(f.reader, ResourceItem, VerbList, AuthTarget{TypeName: "Goal"}); !errors.Is(err, ErrForbidden) {
		t.Errorf("unreadable type: %v; want ErrForbidden", err)
	}
}

func TestAuthorizeCapability_ReadItem(t *testing.T) {
	f := newCapabilityFixture(t)
	if err := f.app.AuthorizeCapability(f.reader, ResourceItem, VerbGet, f.item(f.seen)); err != nil {
		t.Errorf("readable item: %v", err)
	}
	hidden := f.app.AuthorizeCapability(f.reader, ResourceItem, VerbGet, f.item(f.hidden))
	missing := f.app.AuthorizeCapability(f.reader, ResourceItem, VerbGet, AuthTarget{TypeName: "Task", ID: "no-such-id"})
	if !errors.Is(hidden, ErrNotFound) || !errors.Is(missing, ErrNotFound) || hidden.Error() != missing.Error() {
		t.Errorf("hidden %v, missing %v; want the same ErrNotFound", hidden, missing)
	}
	if err := f.app.AuthorizeCapability(f.reader, ResourceItem, VerbGet, AuthTarget{TypeName: "Goal", ID: "g"}); !errors.Is(err, ErrForbidden) {
		t.Errorf("item of an unreadable type: %v; want ErrForbidden", err)
	}
	if err := f.app.AuthorizeCapability(f.global, ResourceItem, VerbGet, f.item(f.hidden)); err != nil {
		t.Errorf("global reader: %v", err)
	}
}

// TestAuthorizeCapability_TypelessRead: a read with no type passes on a scope
// that reads any item only for the listed capabilities; every other one needs
// a scope that reads everything. Authorized(read, AuthTarget{}) is never asked:
// the per-item reader has no global grant, which is all it could match.
func TestAuthorizeCapability_TypelessRead(t *testing.T) {
	f := newCapabilityFixture(t)
	nobody := f.as("nobody")
	for _, c := range capabilityTable {
		if c.Operation != "read" {
			continue
		}
		key := capabilityKey{c.Resource, c.Verb}
		err := f.app.AuthorizeCapability(f.reader, c.Resource, c.Verb, AuthTarget{})
		if want := typelessReadsAny[key]; (err == nil) != want {
			t.Errorf("per-item reader, %s %s with no type: %v; want allowed %v", c.Resource, c.Verb, err, want)
		}
		if err != nil && !errors.Is(err, ErrForbidden) {
			t.Errorf("%s %s: %v; want ErrForbidden", c.Resource, c.Verb, err)
		}
		if err := f.app.AuthorizeCapability(f.global, c.Resource, c.Verb, AuthTarget{}); err != nil {
			t.Errorf("global reader, %s %s with no type: %v", c.Resource, c.Verb, err)
		}
		if err := f.app.AuthorizeCapability(nobody, c.Resource, c.Verb, AuthTarget{}); !errors.Is(err, ErrForbidden) {
			t.Errorf("reader of nothing, %s %s: %v; want ErrForbidden", c.Resource, c.Verb, err)
		}
	}
}

// readDecisions is the decision for every read capability, with its reason. A
// read capability added to the registry fails TestCapabilities_ReadDecisions
// until it is listed here and, where it applies, in typedReads or
// typelessReadsAny.
var readDecisions = map[capabilityKey]string{
	{ResourceItem, VerbGet}:                 "typed: the item's type and ID decide",
	{ResourceItem, VerbList}:                "typed: the type's floor, then filtered per item",
	{ResourceRelation, VerbGet}:             "typed: the relations of one item, filtered by both ends",
	{ResourceRelation, VerbList}:            "reads any: ListRelations keeps edges with both ends readable",
	{ResourceSignal, VerbList}:              "reads any: list_signals filters Signals by their subject",
	{ResourceSignal, VerbGet}:               "all: one Signal by slug is not filtered by subject on this capability yet",
	{ResourceType, VerbGet}:                 "all: schemas are instance infrastructure, kept global by mcp-read-rules",
	{ResourceType, VerbList}:                "all: schemas are instance infrastructure, kept global by mcp-read-rules",
	{ResourceOps, VerbGet}:                  "all: get_sweep_run returns aggregates over every item, not filtered",
	{ResourceOps, VerbList}:                 "all: findings are instance-wide",
	{ResourceAccess, "list_roles"}:          "all: role definitions are admin infrastructure",
	{ResourceAccess, "lookup_names"}:        "all: token names are admin infrastructure",
	{ResourceAccess, "delegate"}:            "all: the floor of a delegation is kept global until C3 decides",
	{ResourceAccess, "withdraw_delegation"}: "all: the floor of withdrawing is kept global until C3 decides",
	{ResourceSite, VerbList}:                "all: navigation and redirects are site infrastructure",
	{ResourceSubscription, VerbGet}:         "reads any: a subscription is read by its owner or a holder of administer, checked in the act",
	{ResourceSubscription, VerbList}:        "reads any: the list is the owner's own, checked in the act",
}

func TestCapabilities_ReadDecisions(t *testing.T) {
	seen := map[capabilityKey]bool{}
	for _, c := range capabilityTable {
		if c.Operation != "read" {
			continue
		}
		key := capabilityKey{c.Resource, c.Verb}
		seen[key] = true
		if _, ok := readDecisions[key]; !ok {
			t.Errorf("read capability %s %s has no decision: list it in readDecisions", c.Resource, c.Verb)
		}
	}
	for key := range readDecisions {
		if !seen[key] {
			t.Errorf("readDecisions lists %s %s, which is not a read capability", key.resource, key.verb)
		}
	}
	for key := range typelessReadsAny {
		if typedReads[key] || !seen[key] {
			t.Errorf("typelessReadsAny %s %s: must be a read capability and not typed", key.resource, key.verb)
		}
	}
	for key := range typedReads {
		if !seen[key] {
			t.Errorf("typedReads %s %s is not a read capability", key.resource, key.verb)
		}
	}
	if len(typelessReadsAny) != 4 || !typelessReadsAny[capabilityKey{ResourceSignal, VerbList}] || !typelessReadsAny[capabilityKey{ResourceRelation, VerbList}] ||
		!typelessReadsAny[capabilityKey{ResourceSubscription, VerbGet}] || !typelessReadsAny[capabilityKey{ResourceSubscription, VerbList}] {
		t.Errorf("typelessReadsAny = %v; want exactly signal list, relation list and subscription get and list", typelessReadsAny)
	}
}

func TestAuthorizeCapability_ScopeLoadFails(t *testing.T) {
	f := newCapabilityFixture(t)
	if _, err := f.db.Exec(`DROP TABLE smeldr_role_grants`); err != nil {
		t.Fatal(err)
	}
	for name, call := range map[string]func() error{
		"read item": func() error { return f.app.AuthorizeCapability(f.reader, ResourceItem, VerbGet, f.item(f.seen)) },
		"read type": func() error {
			return f.app.AuthorizeCapability(f.reader, ResourceItem, VerbList, AuthTarget{TypeName: "Task"})
		},
		"write item": func() error {
			return f.app.AuthorizeCapability(f.as("static"), ResourceItem, VerbUpdate, f.item(f.seen))
		},
		"write typed": func() error {
			return f.app.AuthorizeCapability(f.as("editor"), ResourceItem, VerbCreate, AuthTarget{TypeName: "Task"})
		},
	} {
		if err := call(); !errors.Is(err, ErrInternal) {
			t.Errorf("%s: %v; want ErrInternal", name, err)
		}
	}
}

// TestAuthorizeCapability_AttachedScope: an attached scope is used as is, so a
// read runs no grant query: with the grants table gone it still passes.
func TestAuthorizeCapability_AttachedScope(t *testing.T) {
	f := newCapabilityFixture(t)
	if _, err := f.db.Exec(`DROP TABLE smeldr_role_grants`); err != nil {
		t.Fatal(err)
	}
	ctx := TrustedRead(f.reader).(Context)
	if err := f.app.AuthorizeCapability(ctx, ResourceItem, VerbGet, f.item(f.hidden)); err != nil {
		t.Errorf("attached scope: %v", err)
	}
}

func TestAuthorizeCapability_WriteHiddenItem(t *testing.T) {
	f := newCapabilityFixture(t)
	// "writer" may update every Task globally but reads none: the hidden item is
	// not found before its grant is consulted.
	err := f.app.AuthorizeCapability(f.as("writer"), ResourceItem, VerbUpdate, f.item(f.hidden))
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("write on an unreadable item: %v; want ErrNotFound", err)
	}
	if err := f.app.AuthorizeCapability(f.as("writer"), ResourceItem, VerbUpdate, AuthTarget{TypeName: "Task"}); err != nil {
		t.Errorf("type-level write with a global grant: %v", err)
	}
	if err := f.app.AuthorizeCapability(f.reader, ResourceItem, VerbUpdate, f.item(f.seen)); !errors.Is(err, ErrForbidden) {
		t.Errorf("write on a readable item without the grant: %v; want ErrForbidden", err)
	}
}

func TestAuthorizeCapability_WriteGrants(t *testing.T) {
	f := newCapabilityFixture(t)
	cases := []struct {
		token string
		want  error
	}{
		{"editor", nil},
		{"static", nil},
		{"dynamic", nil},
		{"reader", ErrForbidden},
		{"expired", ErrNotFound}, // an expired grant reads nothing either
	}
	for _, c := range cases {
		err := f.app.AuthorizeCapability(f.as(c.token), ResourceItem, VerbUpdate, f.item(f.seen))
		if c.want == nil && err != nil || c.want != nil && !errors.Is(err, c.want) {
			t.Errorf("%s: %v; want %v", c.token, err, c.want)
		}
	}
	if err := f.app.AuthorizeCapability(f.as("expired"), ResourceItem, VerbCreate, AuthTarget{TypeName: "Task"}); !errors.Is(err, ErrForbidden) {
		t.Errorf("expired grant: %v; want ErrForbidden", err)
	}
	if err := f.app.AuthorizeCapability(f.as("editor"), ResourceAccess, "grant", AuthTarget{}); !errors.Is(err, ErrForbidden) {
		t.Errorf("editor granting: %v; want ErrForbidden", err)
	}
	if err := f.app.AuthorizeCapability(f.global, ResourceAccess, "grant", AuthTarget{}); err != nil {
		t.Errorf("admin granting: %v", err)
	}
}

func TestAuthorizeCapability_AuthorizedFails(t *testing.T) {
	f := newCapabilityFixture(t)
	// The scope is attached, so only Authorized's own grants query runs, and fails.
	if _, err := f.db.Exec(`DROP TABLE smeldr_role_grants`); err != nil {
		t.Fatal(err)
	}
	ctx := TrustedRead(f.as("editor")).(Context)
	if err := f.app.AuthorizeCapability(ctx, ResourceItem, VerbUpdate, f.item(f.seen)); !errors.Is(err, ErrInternal) {
		t.Errorf("Authorized failing: %v; want ErrInternal", err)
	}
}

func capabilityNames(cs []Capability) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Resource + " " + c.Verb
	}
	return out
}

func TestVisibleCapabilities_Legacy(t *testing.T) {
	app := New(Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16")})
	rank := map[Role]int{Guest: 0, Author: 1, Editor: 2, Admin: 3}
	for _, role := range []Role{Guest, Author, Editor, Admin} {
		got, err := app.VisibleCapabilities(NewTestContext(User{Roles: []Role{role}}))
		if err != nil {
			t.Fatal(err)
		}
		var want []Capability
		for _, c := range capabilityTable {
			if rank[role] >= rank[legacyOperationFloor[c.Operation]] {
				want = append(want, c)
			}
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s: %v; want %v", role, capabilityNames(got), capabilityNames(want))
		}
	}
	if got, _ := app.VisibleCapabilities(NewTestContext(User{Roles: []Role{Admin}})); len(got) != len(capabilityTable) {
		t.Errorf("admin sees %d of %d", len(got), len(capabilityTable))
	}
}

func TestVisibleCapabilities_NoTokenID(t *testing.T) {
	f := newCapabilityFixture(t)
	got, err := f.app.VisibleCapabilities(NewTestContext(User{Roles: []Role{Admin}}))
	if err != nil || len(got) != 0 {
		t.Errorf("no token ID: %v %v; want none", capabilityNames(got), err)
	}
}

func TestVisibleCapabilities_PerItemReader(t *testing.T) {
	f := newCapabilityFixture(t)
	got, err := f.app.VisibleCapabilities(f.reader)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"item get", "item list", "relation get", "relation list", "signal list", "subscription get", "subscription list"}
	if !slices.Equal(capabilityNames(got), want) {
		t.Errorf("per-item reader sees %v; want %v", capabilityNames(got), want)
	}
	if got, _ := f.app.VisibleCapabilities(f.as("nobody")); len(got) != 0 {
		t.Errorf("reader of nothing sees %v", capabilityNames(got))
	}
}

func TestVisibleCapabilities_GlobalEditor(t *testing.T) {
	f := newCapabilityFixture(t)
	got, err := f.app.VisibleCapabilities(f.as("editor"))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range capabilityTable {
		want := c.Operation != "administer" && legacyOperationFloor[c.Operation] != Admin
		if slices.Contains(got, c) != want {
			t.Errorf("editor %s %s (%s): visible %v; want %v", c.Resource, c.Verb, c.Operation, !want, want)
		}
	}
	all, err := f.app.VisibleCapabilities(f.global)
	if err != nil || len(all) != len(capabilityTable) {
		t.Errorf("admin sees %d of %d: %v", len(all), len(capabilityTable), err)
	}
}

func TestVisibleCapabilities_NoRead(t *testing.T) {
	f := newCapabilityFixture(t)
	got, err := f.app.VisibleCapabilities(f.as("writer"))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range got {
		if c.Operation != "update" {
			t.Errorf("update-only grant sees %s %s (%s)", c.Resource, c.Verb, c.Operation)
		}
	}
	if len(got) == 0 {
		t.Error("update-only grant sees nothing")
	}
}

func TestVisibleCapabilities_Errors(t *testing.T) {
	t.Run("scope", func(t *testing.T) {
		f := newCapabilityFixture(t)
		if _, err := f.db.Exec(`DROP TABLE smeldr_role_grants`); err != nil {
			t.Fatal(err)
		}
		if got, err := f.app.VisibleCapabilities(f.reader); !errors.Is(err, ErrInternal) || got != nil {
			t.Errorf("%v %v; want ErrInternal and no list", got, err)
		}
	})
	t.Run("grants", func(t *testing.T) {
		f := newCapabilityFixture(t)
		if _, err := f.db.Exec(`DROP TABLE smeldr_role_grants`); err != nil {
			t.Fatal(err)
		}
		ctx := TrustedRead(f.reader).(Context)
		if got, err := f.app.VisibleCapabilities(ctx); !errors.Is(err, ErrInternal) || got != nil {
			t.Errorf("%v %v; want ErrInternal and no list", got, err)
		}
	})
}

func TestRoleStore_GrantedOperations(t *testing.T) {
	f := newCapabilityFixture(t)
	ctx := context.Background()
	ops, err := f.rs.grantedOperations(ctx, "dynamic")
	if err != nil || !ops["read"] || !ops["update"] || ops["delete"] {
		t.Errorf("dynamic: %v %v; want read and update", ops, err)
	}
	if ops, err := f.rs.grantedOperations(ctx, "expired"); err != nil || len(ops) != 0 {
		t.Errorf("expired: %v %v; want none", ops, err)
	}
	if _, err := f.db.ExecContext(ctx, `UPDATE smeldr_roles SET operations = 'not json' WHERE name = 'global-writer'`); err != nil {
		t.Fatal(err)
	}
	if ops, err := f.rs.grantedOperations(ctx, "writer"); err != nil || len(ops) != 0 {
		t.Errorf("malformed operations: %v %v; want skipped", ops, err)
	}
	for _, hook := range []queryHookDB{
		{match: "smeldr_role_grants", replace: "SELECT 1"},
		{match: "smeldr_role_grants", fail: true},
	} {
		hook.DB = f.db
		if _, err := (&RoleStore{db: &hook}).grantedOperations(ctx, "editor"); err == nil {
			t.Errorf("hook %+v: want an error", hook.match)
		}
	}
}

func TestReadScope_ReadsAny(t *testing.T) {
	var none *ReadScope
	empty := &ReadScope{items: map[string]map[string]bool{"Task": {}}, anyOfTyp: map[string]bool{}}
	wild := &ReadScope{anyOfTyp: map[string]bool{"Task": true}}
	one := &ReadScope{items: map[string]map[string]bool{"Task": {"t": true}}}
	for name, c := range map[string]struct {
		s    *ReadScope
		want bool
	}{"nil": {none, false}, "empty": {empty, false}, "wildcard": {wild, true}, "one item": {one, true}, "all": {AllReadScope(), true}} {
		if got := c.s.readsAny(); got != c.want {
			t.Errorf("%s: readsAny = %v; want %v", name, got, c.want)
		}
	}
}

func BenchmarkAuthorizeCapability_AttachedRead(b *testing.B) {
	app := &App{governance: &RoleStore{}}
	scope := &ReadScope{items: map[string]map[string]bool{"Task": {"t": true}}}
	ctx := WithReadScope(NewTestContext(User{ID: "u"}), scope).(Context)
	target := AuthTarget{TypeName: "Task", ID: "t"}
	b.ReportAllocs()
	for b.Loop() {
		if err := app.AuthorizeCapability(ctx, ResourceItem, VerbGet, target); err != nil {
			b.Fatal(err)
		}
	}
}

// TestAuthorizeCapability_TypeIgnoredOutsideTypedReads: a read capability that
// is not a typed read ignores the target's type, so passing one cannot open a
// surface that does not filter by it (get_sweep_run's aggregates).
func TestAuthorizeCapability_TypeIgnoredOutsideTypedReads(t *testing.T) {
	f := newCapabilityFixture(t)
	if err := f.app.AuthorizeCapability(f.reader, ResourceOps, VerbGet, AuthTarget{TypeName: "Task"}); !errors.Is(err, ErrForbidden) {
		t.Errorf("ops get with a readable type for a reader of one Task: %v; want ErrForbidden", err)
	}
	if err := f.app.AuthorizeCapability(f.reader, ResourceOps, VerbGet, f.item(f.seen)); !errors.Is(err, ErrForbidden) {
		t.Errorf("ops get with a readable item: %v; want ErrForbidden", err)
	}
	if err := f.app.AuthorizeCapability(f.reader, ResourceSignal, VerbList, AuthTarget{TypeName: "Goal"}); err != nil {
		t.Errorf("signal list with an unreadable type for a reader of any item: %v; want the type-less rule to pass", err)
	}
	if err := f.app.AuthorizeCapability(f.global, ResourceOps, VerbGet, AuthTarget{TypeName: "Goal"}); err != nil {
		t.Errorf("ops get for a reader of everything: %v", err)
	}
}
