//go:build integration

package pgx

import (
	"context"
	"errors"
	"testing"
	"time"

	smeldr "smeldr.dev/core"
)

// Capability authorization on Postgres: a static reader, a dynamic writer and
// a token with no grant get the same answers as on SQLite.
func TestPG_CapabilityAuthorization(t *testing.T) {
	db, _ := isolatedDB(t)
	ctx := context.Background()
	if err := smeldr.CreateRelationTables(db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS smeldr_tokens (id TEXT NOT NULL PRIMARY KEY, role TEXT NOT NULL DEFAULT '')`); err != nil {
		t.Fatal(err)
	}
	app := smeldr.New(smeldr.Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16"), DB: db})
	rs := smeldr.NewRoleStore(db)
	if err := app.Governance(rs); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO smeldr_relations (id, source_type, source_id, target_type, target_id, relation_kind, edge_class, attributes, created_at, updated_at)
		 VALUES ($1, 'Domain', 'A', 'Goal', 'g1', 'contains', 'asserted', '{}', $2, $2)`,
		smeldr.NewID(), now); err != nil {
		t.Fatal(err)
	}
	for _, def := range []smeldr.RoleDefinition{
		{Name: "goal-reader", Operations: []string{"read"}, ScopeMode: smeldr.ScopeStatic},
		{Name: "domain-updater", Operations: []string{"update"}, ScopeMode: smeldr.ScopeDynamic, ScopeRelationKind: "contains", ScopeDirection: "outgoing"},
	} {
		if err := rs.DefineRole(ctx, def); err != nil {
			t.Fatal(err)
		}
	}
	// No App resolver for Goal here (the type is not registered), so the
	// wildcard form is used.
	for _, g := range []smeldr.RoleGrant{
		{TokenID: "tok", RoleName: "goal-reader", ScopeStatic: []string{"Goal:*"}},
		{TokenID: "tok", RoleName: "domain-updater", ScopeAnchorID: "A"},
	} {
		if _, err := rs.Grant(ctx, g); err != nil {
			t.Fatal(err)
		}
	}
	tok := smeldr.NewTestContext(smeldr.User{ID: "tok"})
	cases := []struct {
		name           string
		ctx            smeldr.Context
		resource, verb string
		target         smeldr.AuthTarget
		want           error
	}{
		{"read a readable item", tok, smeldr.ResourceItem, smeldr.VerbGet, smeldr.AuthTarget{TypeName: "Goal", ID: "g1"}, nil},
		{"read an unreadable type", tok, smeldr.ResourceItem, smeldr.VerbList, smeldr.AuthTarget{TypeName: "Task"}, smeldr.ErrForbidden},
		{"update through the dynamic grant", tok, smeldr.ResourceItem, smeldr.VerbUpdate, smeldr.AuthTarget{TypeName: "Goal", ID: "g1"}, nil},
		{"update off the anchor", tok, smeldr.ResourceItem, smeldr.VerbUpdate, smeldr.AuthTarget{TypeName: "Goal", ID: "g2"}, smeldr.ErrForbidden},
		{"no grant", smeldr.NewTestContext(smeldr.User{ID: "none"}), smeldr.ResourceItem, smeldr.VerbGet, smeldr.AuthTarget{TypeName: "Goal", ID: "g1"}, smeldr.ErrForbidden},
	}
	for _, c := range cases {
		err := app.AuthorizeCapability(c.ctx, c.resource, c.verb, c.target)
		if c.want == nil && err != nil || c.want != nil && !errors.Is(err, c.want) {
			t.Errorf("%s: %v; want %v", c.name, err, c.want)
		}
	}
	visible, err := app.VisibleCapabilities(tok)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, c := range visible {
		names = append(names, c.Resource+" "+c.Verb)
	}
	// item search (T885), item decisions_for_paths (T886) and item
	// approvers_of (T911) are visible to any token that holds read.
	want := []string{"item get", "item list", "item update", "item search", "item decisions_for_paths", "item answer_change_check", "item change_checks", "item approvers_of", "item rest_counts", "item confirm_premise", "relation get", "relation list", "signal list", "signal answer", "subscription get", "subscription list", "subscription update"}
	if len(names) != len(want) {
		t.Fatalf("visible %v; want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("visible %v; want %v", names, want)
			break
		}
	}
}
