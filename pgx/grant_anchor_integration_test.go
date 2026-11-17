//go:build integration

package pgx

import (
	"context"
	"testing"

	smeldr "smeldr.dev/core"
)

// TestPG_GrantAnchorResolved (T1213): on PostgreSQL a dynamic grant anchored
// by a Domain's address or slug stores the Domain's ID; an unknown anchor is
// refused with grant.anchor_not_found.
func TestPG_GrantAnchorResolved(t *testing.T) {
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
	if _, err := app.EnsureOrganization(ctx, "Acme"); err != nil {
		t.Fatal(err)
	}
	var domainID, address string
	if err := db.QueryRowContext(ctx, `SELECT id, address FROM smeldr_domains WHERE slug = 'general'`).Scan(&domainID, &address); err != nil {
		t.Fatal(err)
	}
	if err := smeldr.RegisterDomainReaderRole(ctx, store); err != nil {
		t.Fatal(err)
	}
	for i, anchor := range []string{address, "general", domainID} {
		token := []string{"by-address", "by-slug", "by-id"}[i]
		if _, err := store.Grant(ctx, smeldr.RoleGrant{TokenID: token, RoleName: "domain-reader", ScopeAnchorID: anchor}); err != nil {
			t.Fatalf("%s %q: %v", token, anchor, err)
		}
		gs, err := store.ListGrants(ctx, token)
		if err != nil || len(gs) != 1 || gs[0].ScopeAnchorID != domainID {
			t.Errorf("%s: grants %+v, %v; want the anchor stored as %s", token, gs, err, domainID)
		}
	}
	_, err = store.Grant(ctx, smeldr.RoleGrant{TokenID: "unknown", RoleName: "domain-reader", ScopeAnchorID: "DOM999"})
	if smeldr.ProblemTypeOf(err) != "grant.anchor_not_found" {
		t.Errorf("unknown anchor: %v (%s)", err, smeldr.ProblemTypeOf(err))
	}
	// T1248: no anchor at all is refused, and nothing is stored.
	_, err = store.Grant(ctx, smeldr.RoleGrant{TokenID: "none", RoleName: "domain-reader"})
	if smeldr.ProblemTypeOf(err) != "grant.anchor_required" {
		t.Errorf("no anchor: %v (%s)", err, smeldr.ProblemTypeOf(err))
	}
	if gs, err := store.ListGrants(ctx, "none"); err != nil || len(gs) != 0 {
		t.Errorf("no anchor: grants %+v, %v; want none", gs, err)
	}
}
