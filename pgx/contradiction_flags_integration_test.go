//go:build integration

package pgx

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	smeldr "smeldr.dev/core"
)

// T928 on Postgres: a contradicts edge opens two conditions, each placed with
// its own Decision and naming only it; an old row is reworded, backfilled and
// placed by the sweep, and found again by the resolver.
func TestPG_ConflictDetected(t *testing.T) {
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
	if err := smeldr.EnsureContradictionFlagTable(ctx, db); err != nil {
		t.Fatal(err)
	}
	var domainID string
	if err := db.QueryRowContext(ctx, `SELECT id FROM smeldr_domains WHERE slug = 'general'`).Scan(&domainID); err != nil {
		t.Fatal(err)
	}
	for i, id := range []string{"dec-a", "dec-b", "dec-c", "dec-d"} {
		if _, err := db.ExecContext(ctx, `INSERT INTO smeldr_decisions (id, slug, status, created_at, updated_at, address, address_no) VALUES ($1, $1, 'ratified', now(), now(), $2, $3)`,
			id, "D8"+string(rune('0'+i)), 80+i); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO smeldr_relations (id, source_type, source_id, target_type, target_id, relation_kind, edge_class, created_at, updated_at)
			VALUES ($1, 'Decision', $2, 'Domain', $3, 'belongs_to_domain', 'asserted', now(), now())`, "pl-"+id, id, domainID); err != nil {
			t.Fatal(err)
		}
	}
	if err := smeldr.RegisterApproverRole(ctx, store); err != nil {
		t.Fatal(err)
	}
	for _, g := range []smeldr.RoleGrant{{TokenID: "boss", RoleName: smeldr.RoleApprover}, {TokenID: "boss", RoleName: "editor"}} {
		if _, err := store.Grant(ctx, g); err != nil {
			t.Fatal(err)
		}
	}
	boss := smeldr.NewTestContext(smeldr.User{ID: "boss", Roles: []smeldr.Role{smeldr.Editor, smeldr.Human}})
	if err := rels.Assert(boss, smeldr.RelationEdge{SourceType: "Decision", SourceID: "dec-a", TargetType: "Decision", TargetID: "dec-b",
		RelationKind: "contradicts", EdgeClass: "asserted", Attributes: json.RawMessage("{}")}); err != nil {
		t.Fatal(err)
	}
	rows, err := db.QueryContext(ctx, `SELECT s.subject_id, s.message, (SELECT COUNT(*) FROM smeldr_relations r WHERE r.source_id = s.id AND r.target_id = $1 AND r.invalid_at IS NULL)
		FROM smeldr_signals s WHERE s.signal_type = 'conflict-detected' AND s.status = 'open'`, domainID)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for rows.Next() {
		var subject, message string
		var placed int
		if err := rows.Scan(&subject, &message, &placed); err != nil {
			t.Fatal(err)
		}
		n++
		other := "dec-b"
		if subject == "dec-b" {
			other = "dec-a"
		}
		if !strings.HasSuffix(message, " contradicts a Decision") || strings.Contains(message, other) || placed != 1 {
			t.Errorf("%s: %q placed %d", subject, message, placed)
		}
	}
	rows.Close()
	if n != 2 {
		t.Fatalf("open conditions = %d", n)
	}

	// An old row, written before T928, between dec-c and dec-d.
	if _, err := db.ExecContext(ctx, `INSERT INTO smeldr_relations (id, source_type, source_id, target_type, target_id, relation_kind, edge_class, created_at, updated_at)
		VALUES ('old-edge', 'Decision', 'dec-c', 'Decision', 'dec-d', 'contradicts', 'asserted', now(), now())`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO smeldr_signals (id, slug, status, created_at, updated_at, sender, receiver, signal_type, message, subject_type, subject_id, address, address_no)
		VALUES ('old-sig', 'old-sig', 'open', now(), now(), 'system', 'ratify', 'conflict-detected', 'Decision dec-c contradicts Decision dec-d', 'Decision', 'dec-c', 'S99001', 99001)`); err != nil {
		t.Fatal(err)
	}
	res, err := app.SweepContradictionConditions(ctx)
	if err != nil || res.Reworded != 1 {
		t.Fatalf("sweep = %+v %v", res, err)
	}
	var message string
	var placed int
	if err := db.QueryRowContext(ctx, `SELECT message, (SELECT COUNT(*) FROM smeldr_relations WHERE source_id = 'old-sig' AND invalid_at IS NULL) FROM smeldr_signals WHERE id = 'old-sig'`).Scan(&message, &placed); err != nil {
		t.Fatal(err)
	}
	if message != "D82 contradicts a Decision" || placed != 1 {
		t.Errorf("old row = %q placed %d", message, placed)
	}
	if err := rels.Withdraw(boss, "old-edge", "not a contradiction"); err != nil {
		t.Fatal(err)
	}
	var status, resolution string
	if err := db.QueryRowContext(ctx, `SELECT status, resolution FROM smeldr_signals WHERE id = 'old-sig'`).Scan(&status, &resolution); err != nil || status != "resolved" || resolution != "the contradicts relation ended (withdrawn)" {
		t.Errorf("old row after the edge ended: %s %q %v", status, resolution, err)
	}
}
