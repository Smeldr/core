package smeldr

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// RoleStore.Grant stores a static pattern's item as its ID, whatever the
// caller named it by, and refuses a pattern that names nothing (core-read-scope,
// binding addition 1).
func TestGrant_StaticPatternsResolvedToID(t *testing.T) {
	ctx := context.Background()
	db := addressDB(t)
	setupTokensTable(t, db)
	app := New(Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16"), DB: db})
	RegisterOrchestrationTypes(app, db)
	rs := NewRoleStore(db)
	if err := app.Governance(rs); err != nil {
		t.Fatal(err)
	}
	_ = app.Handler()
	created, err := findModule[*Goal](t, app).MCPCreate(authorCtx(), map[string]any{"Description": "a goal"})
	if err != nil {
		t.Fatal(err)
	}
	g := created.(*Goal)
	if err := RegisterScopedReaderRole(ctx, rs); err != nil {
		t.Fatal(err)
	}
	stored := func(id string) string {
		var s string
		if err := db.QueryRowContext(ctx, `SELECT scope_static FROM smeldr_role_grants WHERE id = $1`, id).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}

	for _, name := range []string{g.Slug, "G1", "g1", g.ID} {
		id, err := rs.Grant(ctx, RoleGrant{TokenID: "tok-" + name, RoleName: "scoped-reader", ScopeStatic: []string{"Goal:" + name}})
		if err != nil {
			t.Fatalf("grant by %q: %v", name, err)
		}
		if got := stored(id); got != `["Goal:`+g.ID+`"]` {
			t.Errorf("grant by %q stored %s; want the ID form", name, got)
		}
		s, err := app.ReadScope(NewTestContext(User{ID: "tok-" + name}))
		if err != nil || !s.Readable("Goal", g.ID) {
			t.Errorf("grant by %q does not read the Goal: %v %v", name, s, err)
		}
	}

	// A wildcard is kept, and duplicates after resolution collapse.
	id, err := rs.Grant(ctx, RoleGrant{TokenID: "tok-w", RoleName: "scoped-reader", ScopeStatic: []string{"Task:*", "Goal:G1", "Goal:" + g.Slug}})
	if err != nil {
		t.Fatal(err)
	}
	if got := stored(id); got != `["Task:*","Goal:`+g.ID+`"]` {
		t.Errorf("stored %s", got)
	}

	for _, c := range []struct{ pattern, want string }{
		{"Goal:G99", "names no Goal record"},
		{"Task:" + g.ID, "names no Task record"}, // a Goal's ID under another type
		{"Nope:x", "not registered"},
		{"Goal", "must be"},
		{"Goal:", "must be"},
		{":x", "must be"},
	} {
		_, err := rs.Grant(ctx, RoleGrant{TokenID: "tok-bad", RoleName: "scoped-reader", ScopeStatic: []string{c.pattern}})
		var ve *ValidationError
		if !errors.As(err, &ve) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("pattern %q: err %v; want a validation error containing %q", c.pattern, err, c.want)
		}
	}
}

// A resolver failure that is not "not found" is an internal error, not a
// refusal of the pattern.
func TestGrant_StaticPatternResolverFailure(t *testing.T) {
	db := setupGovernanceDB(t)
	rs := NewRoleStore(db)
	ctx := context.Background()
	if err := RegisterScopedReaderRole(ctx, rs); err != nil {
		t.Fatal(err)
	}
	rs.resolveID = func(context.Context, string, string) (string, error) { return "", errors.New("db down") }
	_, err := rs.Grant(ctx, RoleGrant{TokenID: "t", RoleName: "scoped-reader", ScopeStatic: []string{"Goal:x"}})
	var ve *ValidationError
	if err == nil || errors.As(err, &ve) || !strings.Contains(err.Error(), "db down") {
		t.Errorf("err %v; want the resolver's own error, not a validation error", err)
	}
}

// A dynamic record type's item is resolved by its address too.
func TestGrant_StaticPatternDynamicType(t *testing.T) {
	ctx := context.Background()
	app, db := dynamicAddressApp(t)
	setupTokensTable(t, db)
	if err := migrateGovernance(ctx, db); err != nil {
		t.Fatal(err)
	}
	rs := NewRoleStore(db)
	if err := app.Governance(rs); err != nil {
		t.Fatal(err)
	}
	if _, err := app.DefineContentType(ctx, schemaFor("invoice", "", "")); err != nil {
		t.Fatal(err)
	}
	repo, _ := app.DynamicContentRepo("invoice")
	inv, err := repo.CreateDraft(ctx, map[string]any{"Title": "first"})
	if err != nil {
		t.Fatal(err)
	}
	if err := RegisterScopedReaderRole(ctx, rs); err != nil {
		t.Fatal(err)
	}
	if _, err := rs.Grant(ctx, RoleGrant{TokenID: "tok", RoleName: "scoped-reader", ScopeStatic: []string{"invoice:" + inv.Address}}); err != nil {
		t.Fatalf("grant by address: %v", err)
	}
	s, err := app.ReadScope(NewTestContext(User{ID: "tok"}))
	if err != nil || !s.Readable("invoice", inv.ID) {
		t.Errorf("grant by address does not read the item: %v %v", s, err)
	}
	if _, err := rs.Grant(ctx, RoleGrant{TokenID: "tok", RoleName: "scoped-reader", ScopeStatic: []string{"invoice:INV99"}}); err == nil {
		t.Error("an unknown address was accepted")
	}
}

// A stewardship pattern names a synthetic type, not a record: on an App with
// governance, Grant keeps "RuleType:<name>" as given (a regression from A494,
// which refused it as an unregistered type), resolves the records next to it,
// and the stewardship inbox matches it.
func TestGrant_SyntheticRuleTypeKeptAsGiven(t *testing.T) {
	ctx := context.Background()
	db := addressDB(t)
	setupTokensTable(t, db)
	if err := CreateAuthorityTables(db); err != nil {
		t.Fatal(err)
	}
	app := New(Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16"), DB: db})
	RegisterOrchestrationTypes(app, db)
	rs := NewRoleStore(db)
	if err := app.Governance(rs); err != nil {
		t.Fatal(err)
	}
	_ = app.Handler()
	if err := rs.DefineRole(ctx, RoleDefinition{Name: "rule-steward", Operations: []string{"steward", "read"}, ScopeMode: ScopeStatic}); err != nil {
		t.Fatal(err)
	}
	created, err := findModule[*Goal](t, app).MCPCreate(TrustedRead(authorCtx()).(Context), map[string]any{"Description": "a goal"})
	if err != nil {
		t.Fatal(err)
	}
	g := created.(*Goal)
	decision := &Decision{Node: Node{ID: NewID(), Slug: "d-rule"}, DecisionNumber: "D900", Scope: "core", RuleType: "design-system"}
	if err := NewSQLRepo[*Decision](db, Table("smeldr_decisions")).Save(ctx, decision); err != nil {
		t.Fatal(err)
	}

	id, err := rs.Grant(ctx, RoleGrant{TokenID: "steward", RoleName: "rule-steward", ScopeStatic: []string{"RuleType:design-system", "Goal:" + g.Slug}})
	if err != nil {
		t.Fatalf("Grant: %v", err)
	}
	var stored string
	if err := db.QueryRowContext(ctx, `SELECT scope_static FROM smeldr_role_grants WHERE id = $1`, id).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if want := `["RuleType:design-system","Goal:` + g.ID + `"]`; stored != want {
		t.Errorf("stored %s; want %s", stored, want)
	}
	if types, err := rs.StewardedRuleTypes(ctx, "steward"); err != nil || len(types) != 1 || types[0] != "design-system" {
		t.Errorf("StewardedRuleTypes = %v %v; want [design-system]", types, err)
	}
	inbox, err := rs.StewardshipInbox(ctx, "steward")
	if err != nil || len(inbox.Decisions) != 1 || inbox.Decisions[0].ID != decision.ID {
		t.Errorf("StewardshipInbox = %+v %v; want the design-system Decision", inbox, err)
	}
	if ok, err := rs.Authorized(ctx, "steward", "steward", AuthTarget{TypeName: "RuleType", ID: "design-system"}); err != nil || !ok {
		t.Errorf("Authorized on the stewarded domain = %v %v; want true", ok, err)
	}
	if _, err := rs.Grant(ctx, RoleGrant{TokenID: "steward-2", RoleName: "rule-steward", ScopeStatic: []string{"RuleType:"}}); err == nil {
		t.Error("RuleType: with no name was accepted")
	}
}
