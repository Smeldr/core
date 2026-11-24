//go:build integration

package pgx

import (
	"context"
	"strings"
	"sync"
	"testing"

	smeldr "smeldr.dev/core"
)

// T1324 on Postgres: two head commits of one pull request checked at the
// same moment leave one live Check, and the other superseded with the edge
// from it. Thirty rounds on real parallel connections; the order that made
// two live Checks is proved by core's deterministic test, this one runs the
// same SQL under load.
func TestPG_CheckChange_ConcurrentHeads(t *testing.T) {
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
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO smeldr_areas (id, slug, status, created_at, updated_at, name, address, address_no, approval_mode)
		VALUES ('are-r', 'race', 'active', now(), now(), 'Race', 'ARE910', 910, 'comment-only')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO smeldr_coverage_rules (id, slug, status, created_at, updated_at, area, kind, repository, pattern, address, address_no)
		VALUES ('cov-r', 'cov-r', 'active', now(), now(), 'ARE910', 'path', 'acme/race', 'internal/**', 'COV910', 910)`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Grant(ctx, smeldr.RoleGrant{TokenID: "ops", RoleName: "editor"}); err != nil {
		t.Fatal(err)
	}
	ops := smeldr.NewTestContext(smeldr.User{ID: "ops", Roles: []smeldr.Role{smeldr.Editor}})
	heads := []string{strings.Repeat("a", 40), strings.Repeat("b", 40)}
	for number := 100; number < 130; number++ {
		var wg sync.WaitGroup
		errs := make([]error, len(heads))
		for i, head := range heads {
			wg.Add(1)
			go func(i int, head string) {
				defer wg.Done()
				_, errs[i] = app.CheckChange(ops, smeldr.ChangeInput{Repository: "acme/race", Number: number, HeadCommit: head, Paths: []string{"internal/a.go"}})
			}(i, head)
		}
		wg.Wait()
		for i, err := range errs {
			if err != nil {
				t.Fatalf("#%d head %s: %v", number, heads[i][:4], err)
			}
		}
		var live, superseded int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FILTER (WHERE status <> 'superseded'), COUNT(*) FILTER (WHERE status = 'superseded')
			FROM smeldr_change_checks WHERE repository = 'acme/race' AND number = $1`, number).Scan(&live, &superseded); err != nil {
			t.Fatal(err)
		}
		if live != 1 || superseded != 1 {
			t.Fatalf("#%d: live %d, superseded %d; want 1 and 1", number, live, superseded)
		}
		var edges int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM smeldr_relations r
			JOIN smeldr_change_checks n ON n.id = r.source_id AND n.status <> 'superseded'
			JOIN smeldr_change_checks o ON o.id = r.target_id AND o.status = 'superseded'
			WHERE r.relation_kind = 'supersedes' AND r.invalid_at IS NULL AND n.number = $1 AND o.number = $1 AND n.repository = 'acme/race'`, number).Scan(&edges); err != nil {
			t.Fatal(err)
		}
		if edges != 1 {
			t.Fatalf("#%d: %d supersedes edges from the live Check, want 1", number, edges)
		}
	}
}
