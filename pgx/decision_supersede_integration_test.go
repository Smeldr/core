//go:build integration

package pgx

import (
	"context"
	"testing"

	smeldr "smeldr.dev/core"
)

// T1187 (D189) on Postgres: supersede in both forms commits as one write (the
// new Decision's address, insert, place and relation included), a failed
// write leaves nothing, and the decision-replacement detector finds a
// superseded Decision with no replacement and clears it once repaired.
func TestPG_SupersedeDecision(t *testing.T) {
	db, _ := isolatedDB(t)
	ctx := context.Background()
	app, store := newPGApp(t, db)
	for _, create := range []func(smeldr.DB) error{smeldr.CreateGovernanceTables, smeldr.CreateRelationTables, smeldr.CreateFindingTable} {
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
	app.Findings(smeldr.NewFindingStore(db))
	_ = app.Handler()
	if _, err := app.EnsureOrganization(ctx, "Acme"); err != nil {
		t.Fatal(err)
	}
	var domainID string
	if err := db.QueryRowContext(ctx, `SELECT id FROM smeldr_domains WHERE slug = 'general'`).Scan(&domainID); err != nil {
		t.Fatal(err)
	}
	for i, d := range []struct{ id, status string }{{"dec-old", "ratified"}, {"dec-new", "ratified"}, {"dec-two", "ratified"}, {"dec-lost", "superseded"}} {
		if _, err := db.ExecContext(ctx, `INSERT INTO smeldr_decisions (id, slug, status, scope, created_at, updated_at, address, address_no) VALUES ($1, $1, $2, 'core', now(), now(), $3, $4)`,
			d.id, d.status, "D90"+string(rune('0'+i)), 900+i); err != nil {
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
	status := func(id string) string {
		t.Helper()
		var s string
		if err := db.QueryRowContext(ctx, `SELECT status FROM smeldr_decisions WHERE id = $1`, id).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}

	// Form 1.
	if _, err := app.SupersedeDecision(boss, smeldr.SupersedeRequest{Decision: "dec-old", Replacement: "dec-new", Reason: "clearer"}); err != nil {
		t.Fatal(err)
	}
	if status("dec-old") != "superseded" {
		t.Errorf("dec-old is %s", status("dec-old"))
	}

	// Form 2, after a failed attempt that must leave nothing behind.
	var counter, count int
	db.QueryRowContext(ctx, `SELECT last_no FROM smeldr_address_counters WHERE type_name = 'Decision'`).Scan(&counter)
	db.QueryRowContext(ctx, `SELECT COUNT(*) FROM smeldr_decisions`).Scan(&count)
	if _, err := db.ExecContext(ctx, `CREATE FUNCTION refuse_supersedes() RETURNS trigger AS $$ BEGIN IF NEW.relation_kind = 'supersedes' THEN RAISE EXCEPTION 'refused by test'; END IF; RETURN NEW; END $$ LANGUAGE plpgsql`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TRIGGER refuse_supersedes BEFORE INSERT ON smeldr_relations FOR EACH ROW EXECUTE FUNCTION refuse_supersedes()`); err != nil {
		t.Fatal(err)
	}
	if _, err := app.SupersedeDecision(boss, smeldr.SupersedeRequest{Decision: "dec-two", Reason: "r", New: &smeldr.NewReplacement{Title: "Never", Body: "b"}}); err == nil {
		t.Fatal("the write passed the trigger")
	}
	var counterAfter, countAfter int
	db.QueryRowContext(ctx, `SELECT last_no FROM smeldr_address_counters WHERE type_name = 'Decision'`).Scan(&counterAfter)
	db.QueryRowContext(ctx, `SELECT COUNT(*) FROM smeldr_decisions`).Scan(&countAfter)
	if counterAfter != counter || countAfter != count || status("dec-two") != "ratified" {
		t.Errorf("a failed write left: counter %d to %d, Decisions %d to %d, dec-two %s", counter, counterAfter, count, countAfter, status("dec-two"))
	}
	if _, err := db.ExecContext(ctx, `DROP TRIGGER refuse_supersedes ON smeldr_relations`); err != nil {
		t.Fatal(err)
	}
	res, err := app.SupersedeDecision(boss, smeldr.SupersedeRequest{Decision: "dec-two", Reason: "replaced", New: &smeldr.NewReplacement{Title: "The new rule", Body: "Every write names its place."}})
	if err != nil {
		t.Fatal(err)
	}
	var newStatus, placeTarget string
	if err := db.QueryRowContext(ctx, `SELECT d.status, r.target_id FROM smeldr_decisions d JOIN smeldr_relations r ON r.source_id = d.id AND r.relation_kind = 'belongs_to_domain'
		WHERE d.address = $1`, res.Replacement).Scan(&newStatus, &placeTarget); err != nil {
		t.Fatalf("the new Decision %s: %v", res.Replacement, err)
	}
	if newStatus != "ratified" || placeTarget != domainID || status("dec-two") != "superseded" || !res.Created {
		t.Errorf("new %s in %s, dec-two %s, %+v", newStatus, placeTarget, status("dec-two"), res)
	}

	// The detector and the repair.
	if _, _, _, err := app.SweepStructural(ctx); err != nil {
		t.Fatal(err)
	}
	var findings int
	db.QueryRowContext(ctx, `SELECT COUNT(*) FROM smeldr_findings WHERE detector = 'decision-replacement' AND subject_id = 'dec-lost'`).Scan(&findings)
	if findings != 1 {
		t.Errorf("findings for dec-lost = %d, want 1", findings)
	}
	if _, err := app.RepairSuperseded(boss, smeldr.RepairRequest{Decision: "dec-lost", Replacement: "dec-new", Reason: "it was replaced by dec-new"}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := app.SweepStructural(ctx); err != nil {
		t.Fatal(err)
	}
	db.QueryRowContext(ctx, `SELECT COUNT(*) FROM smeldr_findings WHERE detector = 'decision-replacement'`).Scan(&findings)
	if findings != 0 {
		t.Errorf("%d decision-replacement findings after the repair", findings)
	}
}
