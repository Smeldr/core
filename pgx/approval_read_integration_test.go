//go:build integration

package pgx

import (
	"context"
	"testing"

	smeldr "smeldr.dev/core"
)

// TestPG_ApprovalRolesRead (T1247, D191): on PostgreSQL a domain-approver with
// no reader grant reads its Domain, and the approver reads the Organization's
// hierarchy without reading everything.
func TestPG_ApprovalRolesRead(t *testing.T) {
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
	var domainID, orgID string
	if err := db.QueryRowContext(ctx, `SELECT id FROM smeldr_domains WHERE slug = 'general'`).Scan(&domainID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT id FROM smeldr_organizations`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	for _, reg := range []func(context.Context, *smeldr.RoleStore) error{smeldr.RegisterApproverRole, smeldr.RegisterDomainApproverRole} {
		if err := reg(ctx, store); err != nil {
			t.Fatal(err)
		}
	}
	for _, g := range []smeldr.RoleGrant{
		{TokenID: "dom-appr", RoleName: smeldr.RoleDomainApprover, ScopeAnchorID: domainID},
		{TokenID: "appr", RoleName: smeldr.RoleApprover},
	} {
		if _, err := store.Grant(ctx, g); err != nil {
			t.Fatal(err)
		}
	}
	for token, want := range map[string]map[string]bool{
		"dom-appr": {"Domain:" + domainID: true, "Organization:" + orgID: false},
		"appr":     {"Domain:" + domainID: true, "Organization:" + orgID: true, "Task:no-place": false},
	} {
		scope, err := smeldr.LoadReadScope(ctx, store, smeldr.User{ID: token})
		if err != nil || scope.All() {
			t.Fatalf("%s: scope %v, all %v", token, err, scope != nil && scope.All())
		}
		for key, w := range want {
			typ, id := key[:indexColon(key)], key[indexColon(key)+1:]
			if got := scope.Readable(typ, id); got != w {
				t.Errorf("%s reads %s = %v, want %v", token, key, got, w)
			}
		}
	}
}

func indexColon(s string) int {
	for i := range s {
		if s[i] == ':' {
			return i
		}
	}
	return -1
}
