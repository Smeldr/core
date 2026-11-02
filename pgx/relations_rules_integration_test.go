//go:build integration

package pgx

import (
	"context"
	"errors"
	"testing"

	smeldr "smeldr.dev/core"
)

// TestPG_RelationRules: on Postgres the relation door refuses a relation from
// a record to itself, and a supersedes relation between Decisions unless the
// replacement is ratified and the replaced one ratified or pending
// re-evaluation (T1054).
func TestPG_RelationRules(t *testing.T) {
	db, _ := isolatedDB(t)
	ctx := context.Background()
	if err := smeldr.CreateOrchestrationTables(db); err != nil {
		t.Fatal(err)
	}
	if err := smeldr.CreateRelationTables(db); err != nil {
		t.Fatal(err)
	}
	rs, err := smeldr.NewRelationStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := smeldr.RegisterOrchestrationRelationKinds(ctx, rs); err != nil {
		t.Fatal(err)
	}
	for _, d := range []struct{ id, status string }{
		{"d-new", "ratified"}, {"d-old", "ratified"}, {"d-draft", "proposed"}, {"d-gone", "superseded"},
	} {
		if _, err := db.ExecContext(ctx, `INSERT INTO smeldr_decisions (id, slug, status, created_at, updated_at, address) VALUES ($1, $1, $2, now(), now(), $3)`,
			d.id, d.status, "D-"+d.id); err != nil {
			t.Fatal(err)
		}
	}
	assert := func(src, tgt, kind string) error {
		_, err := rs.MCPAssertRelation(smeldr.TrustedRead(ctx), "Decision", src, "Decision", tgt, kind, nil, nil, nil, nil)
		return err
	}

	err = assert("d-old", "d-old", "grounded_in")
	if smeldr.ProblemTypeOf(err) != "relation.to_itself" || smeldr.ProblemValuesOf(err)["record"] != "D-d-old" {
		t.Errorf("a relation to itself: %v; want relation.to_itself naming D-d-old", err)
	}
	err = assert("d-draft", "d-old", "supersedes")
	if smeldr.ProblemTypeOf(err) != "decision.replacement_state" || !errors.Is(err, smeldr.ErrConflict) || smeldr.ProblemValuesOf(err)["state"] != "proposed" {
		t.Errorf("a proposed replacement: %v; want decision.replacement_state", err)
	}
	err = assert("d-new", "d-gone", "supersedes")
	if smeldr.ProblemTypeOf(err) != "decision.replacement_state" || smeldr.ProblemValuesOf(err)["state"] != "superseded" {
		t.Errorf("replacing a superseded Decision: %v; want decision.replacement_state", err)
	}
	if err := assert("d-new", "d-old", "supersedes"); err != nil {
		t.Errorf("a ratified replacement of a ratified Decision: %v", err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM smeldr_relations WHERE relation_kind IN ('supersedes', 'grounded_in')`).Scan(&n); err != nil || n != 1 {
		t.Errorf("relation rows = %d %v; want the one legal edge", n, err)
	}
}
