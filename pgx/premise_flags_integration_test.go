//go:build integration

package pgx

import (
	"context"
	"encoding/json"
	"testing"

	smeldr "smeldr.dev/core"
)

// T919 part 3 (D149) on Postgres: the condition opens once with its place,
// a confirmation keeps it closed, and a new premise state opens it again.
func TestPG_PremiseFlagged(t *testing.T) {
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
	if err := smeldr.EnsurePremiseFlagTable(ctx, db); err != nil {
		t.Fatal(err)
	}
	var domainID string
	if err := db.QueryRowContext(ctx, `SELECT id FROM smeldr_domains WHERE slug = 'general'`).Scan(&domainID); err != nil {
		t.Fatal(err)
	}
	for _, d := range []struct{ id, status string }{{"dec-d", "ratified"}, {"dec-p", "proposed"}} {
		if _, err := db.ExecContext(ctx, `INSERT INTO smeldr_decisions (id, slug, status, created_at, updated_at, address, address_no) VALUES ($1, $1, $2, now(), now(), $3, $4)`,
			d.id, d.status, "D9"+d.id[4:5], 90+len(d.status)); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO smeldr_relations (id, source_type, source_id, target_type, target_id, relation_kind, edge_class, created_at, updated_at)
			VALUES ($1, 'Decision', $2, 'Domain', $3, 'belongs_to_domain', 'asserted', now(), now())`, "pl-"+d.id, d.id, domainID); err != nil {
			t.Fatal(err)
		}
	}
	if err := smeldr.RegisterDomainApproverRole(ctx, store); err != nil {
		t.Fatal(err)
	}
	for _, g := range []smeldr.RoleGrant{{TokenID: "boss", RoleName: smeldr.RoleDomainApprover, ScopeAnchorID: domainID}, {TokenID: "boss", RoleName: "editor"}} {
		if _, err := store.Grant(ctx, g); err != nil {
			t.Fatal(err)
		}
	}
	boss := smeldr.NewTestContext(smeldr.User{ID: "boss", Roles: []smeldr.Role{smeldr.Editor, smeldr.Human}})
	if err := rels.Assert(boss, smeldr.RelationEdge{SourceType: "Decision", SourceID: "dec-d", TargetType: "Decision", TargetID: "dec-p",
		RelationKind: "grounded_in", EdgeClass: "asserted", Attributes: json.RawMessage("{}")}); err != nil {
		t.Fatal(err)
	}
	open := func() (n int, placed int) {
		t.Helper()
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM smeldr_signals WHERE signal_type = 'premise-flagged' AND status = 'open'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM smeldr_relations r JOIN smeldr_signals s ON s.id = r.source_id
			WHERE s.signal_type = 'premise-flagged' AND r.relation_kind = 'belongs_to_domain' AND r.target_id = $1`, domainID).Scan(&placed); err != nil {
			t.Fatal(err)
		}
		return n, placed
	}
	if n, placed := open(); n != 1 || placed != 1 {
		t.Fatalf("open %d, placed %d", n, placed)
	}
	if sweep, err := app.SweepPremiseFlags(ctx); err != nil || sweep.Opened != 0 || sweep.Pairs != 1 {
		t.Errorf("sweep = %+v %v", sweep, err)
	}
	if err := app.ConfirmPremise(boss, "dec-d", "dec-p", "still right"); err != nil {
		t.Fatal(err)
	}
	if sweep, err := app.SweepPremiseFlags(ctx); err != nil || sweep.Opened != 0 {
		t.Errorf("after confirm = %+v %v", sweep, err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE smeldr_decisions SET status = 'archived' WHERE id = 'dec-p'`); err != nil {
		t.Fatal(err)
	}
	if sweep, err := app.SweepPremiseFlags(ctx); err != nil || sweep.Opened != 1 {
		t.Errorf("new premise state = %+v %v", sweep, err)
	}
	if n, _ := open(); n != 1 {
		t.Errorf("open after reopening: %d", n)
	}

	// T925: the Decision moves to the Organization; its open condition follows
	// with exactly one live place edge, and is confirmed by the condition.
	var orgID, sigID, plID string
	if err := db.QueryRowContext(ctx, `SELECT id FROM smeldr_organizations LIMIT 1`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT id FROM smeldr_relations WHERE source_id = 'dec-d' AND relation_kind = 'belongs_to_domain' AND invalid_at IS NULL`).Scan(&plID); err != nil {
		t.Fatal(err)
	}
	if err := smeldr.RegisterApproverRole(ctx, store); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Grant(ctx, smeldr.RoleGrant{TokenID: "boss", RoleName: smeldr.RoleApprover}); err != nil {
		t.Fatal(err)
	}
	if err := rels.Withdraw(boss, plID, "moving it"); err != nil {
		t.Fatal(err)
	}
	if err := rels.Assert(boss, smeldr.RelationEdge{SourceType: "Decision", SourceID: "dec-d", TargetType: "Organization", TargetID: orgID,
		RelationKind: "belongs_to_organization", EdgeClass: "asserted", Attributes: json.RawMessage("{}")}); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT id FROM smeldr_signals WHERE signal_type = 'premise-flagged' AND status = 'open'`).Scan(&sigID); err != nil {
		t.Fatal(err)
	}
	var places int
	var target string
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*), MAX(target_id) FROM smeldr_relations WHERE source_id = $1 AND relation_kind LIKE 'belongs_to_%' AND invalid_at IS NULL`, sigID).Scan(&places, &target); err != nil || places != 1 || target != orgID {
		t.Errorf("condition places = %d at %s, %v", places, target, err)
	}
	if err := app.ConfirmPremiseCondition(boss, sigID, "still right"); err != nil {
		t.Fatal(err)
	}
	if n, _ := open(); n != 0 {
		t.Errorf("open after confirming by the condition: %d", n)
	}
}
