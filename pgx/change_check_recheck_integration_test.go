//go:build integration

package pgx

import (
	"context"
	"testing"

	smeldr "smeldr.dev/core"
)

// D196, T1294 on Postgres: the round index and its migration, a quiet sweep,
// a reopen when a Decision of the Area leaves force, and a close.
func TestPG_ChangeCheckRecheck(t *testing.T) {
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
	for _, stmt := range []string{
		`INSERT INTO smeldr_areas (id, slug, status, created_at, updated_at, name, address, address_no, approval_mode)
		 VALUES ('are-b', 'billing', 'active', now(), now(), 'Billing', 'ARE900', 900, 'requires-approval')`,
		`INSERT INTO smeldr_coverage_rules (id, slug, status, created_at, updated_at, area, kind, repository, pattern, address, address_no)
		 VALUES ('cov-b', 'cov-b', 'active', now(), now(), 'ARE900', 'path', 'acme/app', 'internal/billing/**', 'COV900', 900)`,
		`INSERT INTO smeldr_decisions (id, slug, status, title, created_at, updated_at, address, address_no)
		 VALUES ('dec-b', 'dec-b', 'ratified', 'Billing rule', now(), now(), 'D900', 900)`,
		`INSERT INTO smeldr_standing (subject_type, subject_id, standing) VALUES ('Decision', 'dec-b', 'holds')`,
		`INSERT INTO smeldr_relations (id, source_type, source_id, target_type, target_id, relation_kind, edge_class, attributes, created_at, updated_at)
		 VALUES ('pl-b', 'Decision', 'dec-b', 'Area', 'are-b', 'belongs_to_area', 'asserted', '{}', now(), now())`,
	} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	if _, err := store.Grant(ctx, smeldr.RoleGrant{TokenID: "ops", RoleName: "editor"}); err != nil {
		t.Fatal(err)
	}
	ops := smeldr.NewTestContext(smeldr.User{ID: "ops", Roles: []smeldr.Role{smeldr.Editor}})
	head := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	in := smeldr.ChangeInput{Repository: "acme/app", Number: 7, HeadCommit: head, Paths: []string{"internal/billing/a.go"}}
	first, err := app.CheckChange(ops, in)
	if err != nil || len(first.Decisions) != 1 {
		t.Fatalf("first: %+v %v", first, err)
	}
	// The migration on Postgres: the old commit index back, then Ensure.
	if _, err := db.ExecContext(ctx, `CREATE UNIQUE INDEX idx_smeldr_change_checks_commit ON smeldr_change_checks (repository, number, head_commit)`); err != nil {
		t.Fatal(err)
	}
	if err := smeldr.EnsureChangeCheckTables(ctx, db); err != nil {
		t.Fatal(err)
	}
	var idx int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pg_indexes WHERE indexname = 'idx_smeldr_change_checks_commit'`).Scan(&idx); err != nil || idx != 0 {
		t.Errorf("the commit index is still there: %d %v", idx, err)
	}
	// Quiet: nothing changed, nothing written.
	for i := 0; i < 2; i++ {
		r, err := app.RecheckChangeChecks(ctx)
		if err != nil || r.Checked != 1 || r.Reopened != 0 {
			t.Fatalf("quiet sweep %d: %+v %v", i, r, err)
		}
	}
	// The Decision leaves force: the Check is checked again as a new one.
	if _, err := db.ExecContext(ctx, `UPDATE smeldr_standing SET standing = 'ceased' WHERE subject_id = 'dec-b'`); err != nil {
		t.Fatal(err)
	}
	r, err := app.RecheckChangeChecks(ctx)
	if err != nil || r.Reopened != 1 {
		t.Fatalf("sweep after the change: %+v %v", r, err)
	}
	again, err := app.CheckChange(ops, in)
	if err != nil || again.Check.ID == first.Check.ID || string(again.Check.Status) != smeldr.ChangeCheckAwaitingApproval || len(again.Decisions) != 0 {
		t.Fatalf("after reopen: %+v %v", again.Check, err)
	}
	var old string
	if err := db.QueryRowContext(ctx, `SELECT status FROM smeldr_change_checks WHERE id = $1`, first.Check.ID).Scan(&old); err != nil || old != smeldr.ChangeCheckSuperseded {
		t.Errorf("old Check: %q %v", old, err)
	}
	var rounds int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT round) FROM smeldr_change_checks WHERE number = 7 AND head_commit = $1`, head).Scan(&rounds); err != nil || rounds != 2 {
		t.Errorf("rounds: %d %v", rounds, err)
	}
	// A close ends it: the next change of the Decisions reopens nothing.
	if _, err := app.RecordClose(ops, smeldr.CloseInput{Repository: "acme/app", Number: 7, HeadCommit: head}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE smeldr_standing SET standing = 'holds' WHERE subject_id = 'dec-b'`); err != nil {
		t.Fatal(err)
	}
	if r, err := app.RecheckChangeChecks(ctx); err != nil || r.Checked != 0 {
		t.Errorf("after the close: %+v %v", r, err)
	}
	// A merge recorded twice is one row, and it replaces the close.
	for i := 0; i < 2; i++ {
		if _, err := app.RecordMerge(ops, smeldr.MergeInput{Repository: "acme/app", Number: 7, HeadCommit: head, MergeCommit: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}); err != nil {
			t.Fatal(err)
		}
	}
	var how string
	if err := db.QueryRowContext(ctx, `SELECT how FROM smeldr_change_ends WHERE repository = 'acme/app' AND number = 7`).Scan(&how); err != nil || how != "merged" {
		t.Errorf("end row: %q %v", how, err)
	}
}
