//go:build integration

package pgx

import (
	"context"
	"errors"
	"testing"

	smeldr "smeldr.dev/core"
)

// T906 (D145) on Postgres: a ratified Decision is moved by an approver, and
// an Author without ratify is refused on both halves of the move.
func TestPG_PlaceAuthority(t *testing.T) {
	db, _ := isolatedDB(t)
	ctx := context.Background()
	app, store := newPGApp(t, db)
	for _, create := range []func(smeldr.DB) error{smeldr.CreateGovernanceTables, smeldr.CreateRelationTables} {
		if err := create(db); err != nil {
			t.Fatal(err)
		}
	}
	smeldr.RegisterGovernanceTypes(app, db)
	rels, err := smeldr.NewRelationStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := smeldr.RegisterGovernanceRelationKinds(ctx, rels); err != nil {
		t.Fatal(err)
	}
	app.Relations(rels)
	_ = app.Handler()
	org, err := app.EnsureOrganization(ctx, "Acme")
	if err != nil {
		t.Fatal(err)
	}
	var domainID string
	if err := db.QueryRowContext(ctx, `SELECT id FROM smeldr_domains WHERE slug = 'general'`).Scan(&domainID); err != nil {
		t.Fatal(err)
	}
	if err := smeldr.RegisterApproverRole(ctx, store); err != nil {
		t.Fatal(err)
	}
	for _, g := range []smeldr.RoleGrant{
		{TokenID: "approver", RoleName: smeldr.RoleApprover},
		{TokenID: "approver", RoleName: "editor"},
		{TokenID: "author", RoleName: "editor"},
	} {
		if _, err := store.Grant(ctx, g); err != nil {
			t.Fatal(err)
		}
	}
	approver := smeldr.NewTestContext(smeldr.User{ID: "approver", Roles: []smeldr.Role{smeldr.Editor, smeldr.Human}})
	author := smeldr.NewTestContext(smeldr.User{ID: "author", Roles: []smeldr.Role{smeldr.Editor}})

	decisions := smeldr.NewSQLRepo[*smeldr.Decision](db, smeldr.Table("smeldr_decisions"))
	if err := decisions.Save(ctx, &smeldr.Decision{Node: smeldr.Node{ID: "d1", Slug: "d1", Status: "proposed"}, Title: "Money in cents"}); err != nil {
		t.Fatal(err)
	}
	// Proposed: an Author places it.
	edge, err := rels.MCPAssertRelation(author, "Decision", "d1", "Domain", domainID, "belongs_to_domain", nil, nil, nil, []byte("{}"))
	if err != nil {
		t.Fatalf("Author places a proposed Decision: %v", err)
	}
	if _, err := app.TransitionItem(smeldr.TrustedRead(approver), "Decision", "d1", "ratified"); err != nil {
		t.Fatalf("ratify: %v", err)
	}
	// Ratified: the Author may not move it out, the approver may.
	if err := rels.Withdraw(author, edge.ID, "move"); !errors.Is(err, smeldr.ErrForbidden) {
		t.Errorf("Author withdraws a ratified Decision's place: %v, want forbidden", err)
	}
	if err := rels.Withdraw(approver, edge.ID, "move to the Organization"); err != nil {
		t.Fatalf("approver withdraws: %v", err)
	}
	if _, err := rels.MCPAssertRelation(author, "Decision", "d1", "Organization", org.ID, "belongs_to_organization", nil, nil, nil, []byte("{}")); !errors.Is(err, smeldr.ErrForbidden) {
		t.Errorf("Author places a ratified Decision: %v, want forbidden", err)
	}
	if _, err := rels.MCPAssertRelation(approver, "Decision", "d1", "Organization", org.ID, "belongs_to_organization", nil, nil, nil, []byte("{}")); err != nil {
		t.Errorf("approver places it on the Organization: %v", err)
	}
}
