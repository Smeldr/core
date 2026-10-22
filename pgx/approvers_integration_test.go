//go:build integration

package pgx

import (
	"context"
	"errors"
	"reflect"
	"testing"

	smeldr "smeldr.dev/core"
)

// T911 (D147) on Postgres: a domain-reader sees its Domain's approvers, the
// approver's anchor left empty (it does not read the Organization), and
// another Domain is not found.
func TestPG_ApproversOf(t *testing.T) {
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
	var domainID, domainAddress string
	if err := db.QueryRowContext(ctx, `SELECT id, address FROM smeldr_domains WHERE slug = 'general'`).Scan(&domainID, &domainAddress); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO smeldr_domains (id, slug, created_at, updated_at, name, address) VALUES ('dom-other', 'other', now(), now(), 'Other', 'DOM900')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE smeldr_tokens (id TEXT PRIMARY KEY, name TEXT NOT NULL, role TEXT NOT NULL,
		expires_at TEXT NOT NULL, revoked_at TEXT, created_at TEXT NOT NULL, user_id TEXT, actor_class TEXT)`); err != nil {
		t.Fatal(err)
	}
	for _, u := range []struct{ user, class string }{{"boss", "human"}, {"dapp", "human"}, {"dapp2", "human"}, {"reader", "human"}} {
		if _, err := db.ExecContext(ctx, `INSERT INTO smeldr_tokens (id, name, role, expires_at, created_at, user_id, actor_class) VALUES ($1, $2, 'editor', '2099-01-01T00:00:00Z', '2026-01-01T00:00:00Z', $2, $3)`,
			"fp-"+u.user, u.user, u.class); err != nil {
			t.Fatal(err)
		}
	}
	for _, reg := range []func(context.Context, *smeldr.RoleStore) error{smeldr.RegisterApproverRole, smeldr.RegisterDomainApproverRole, smeldr.RegisterDomainReaderRole} {
		if err := reg(ctx, store); err != nil {
			t.Fatal(err)
		}
	}
	for _, g := range []smeldr.RoleGrant{
		{TokenID: "boss", RoleName: smeldr.RoleApprover},
		{TokenID: "dapp", RoleName: smeldr.RoleDomainApprover, ScopeAnchorID: domainID},
		{TokenID: "dapp2", RoleName: smeldr.RoleDomainApprover, ScopeAnchorID: "dom-other"},
		{TokenID: "reader", RoleName: "domain-reader", ScopeAnchorID: domainID},
	} {
		if _, err := store.Grant(ctx, g); err != nil {
			t.Fatal(err)
		}
	}
	reader := smeldr.NewTestContext(smeldr.User{ID: "reader", Roles: []smeldr.Role{smeldr.Editor}})
	got, err := app.ApproversOf(reader, domainAddress)
	if err != nil {
		t.Fatal(err)
	}
	want := []smeldr.PlaceApprover{
		{Name: "boss", Role: smeldr.RoleApprover, Class: "human"},
		{Name: "dapp", Role: smeldr.RoleDomainApprover, Class: "human", AnchorAddress: domainAddress},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
	if _, err := app.ApproversOf(reader, "DOM900"); !errors.Is(err, smeldr.ErrNotFound) {
		t.Errorf("another Domain: %v, want not found", err)
	}
}
