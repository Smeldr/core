//go:build integration

package pgx

import (
	"context"
	"testing"

	smeldr "smeldr.dev/core"
)

// T918 part 1 on Postgres: a covered change makes one Check, a redelivery
// returns it, and the next commit supersedes it.
func TestPG_ChangeCheck(t *testing.T) {
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
	if err := smeldr.EnsureChangeCheckTables(ctx, db); err != nil {
		t.Fatalf("EnsureChangeCheckTables on a fresh database: %v", err)
	}
	var domainID string
	if err := db.QueryRowContext(ctx, `SELECT id FROM smeldr_domains WHERE slug = 'general'`).Scan(&domainID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO smeldr_areas (id, slug, status, created_at, updated_at, name, address, address_no, approval_mode)
		VALUES ('are-b', 'billing', 'active', now(), now(), 'Billing', 'ARE900', 900, 'requires-approval')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO smeldr_coverage_rules (id, slug, status, created_at, updated_at, area, kind, repository, pattern, address, address_no)
		VALUES ('cov-b', 'cov-b', 'active', now(), now(), 'ARE900', 'path', 'acme/app', 'internal/billing/**', 'COV900', 900)`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Grant(ctx, smeldr.RoleGrant{TokenID: "ops", RoleName: "editor"}); err != nil {
		t.Fatal(err)
	}
	ops := smeldr.NewTestContext(smeldr.User{ID: "ops", Roles: []smeldr.Role{smeldr.Editor}})
	in := smeldr.ChangeInput{Repository: "acme/app", Number: 5, HeadCommit: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Paths: []string{"internal/billing/a.go"}, FilesTotal: 4000}
	first, err := app.CheckChange(ops, in)
	if err != nil {
		t.Fatal(err)
	}
	if string(first.Check.Status) != smeldr.ChangeCheckAwaitingApproval || !first.Check.CheckedPartly || len(first.Areas) != 1 {
		t.Errorf("first: %+v", first)
	}
	again, err := app.CheckChange(ops, in)
	if err != nil || again.Check.ID != first.Check.ID {
		t.Errorf("redelivery: %v, %v", again.Check, err)
	}
	in.HeadCommit = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if _, err := app.CheckChange(ops, in); err != nil {
		t.Fatal(err)
	}
	old, err := app.ChangeCheckOf(ops, first.Check.Address)
	if err != nil || string(old.Check.Status) != smeldr.ChangeCheckSuperseded {
		t.Errorf("first after the next commit: %+v, %v", old.Check, err)
	}
}
