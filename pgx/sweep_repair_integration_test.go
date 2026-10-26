//go:build integration

package pgx

import (
	"context"
	"testing"
	"time"

	smeldr "smeldr.dev/core"
)

// T932 on Postgres: an edge to a runtime type with its own flow survives the
// structural sweep in a state that is not published, and an edge the old
// sweep ended is restored once by RepairSweptEdges, and not when a later
// row of the relation was ended for another reason.
func TestPG_SweepKeepsOwnFlowTypes(t *testing.T) {
	db, _ := isolatedDB(t)
	ctx := context.Background()
	app, _ := dynamicApp(t, db, &smeldr.StateFlow{Name: "memo-flow", States: []smeldr.State{
		{Name: "draft", IsInitial: true}, {Name: "approved"}, {Name: "archived", IsTerminal: true},
	}})
	for _, create := range []func(smeldr.DB) error{smeldr.CreateRelationTables, smeldr.CreateProvenanceTable, smeldr.CreateOrchestrationTables} {
		if err := create(db); err != nil {
			t.Fatal(err)
		}
	}
	rels, err := smeldr.NewRelationStore(db)
	if err != nil {
		t.Fatal(err)
	}
	app.Relations(rels)
	app.Provenance(smeldr.NewProvenanceStore(db))
	_ = app.Handler()
	if _, err := db.ExecContext(ctx, `INSERT INTO smeldr_dynamic_content (id, slug, type_name, status, fields, created_at, updated_at) VALUES ('memo-1', 'memo-1', 'memo', 'approved', '{"Title":"x"}', now(), now())`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO smeldr_relations (id, source_type, source_id, target_type, target_id, relation_kind, edge_class, created_at, updated_at)
		VALUES ('e-keep', 'Task', 't-1', 'memo', 'memo-1', 'refers_to', 'asserted', now(), now())`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO smeldr_tasks (id, slug, status, created_at, updated_at) VALUES ('t-1', 't-1', 'backlog', now(), now())`); err != nil {
		t.Fatal(err)
	}
	if _, flagged, _, err := app.SweepStructural(ctx); err != nil || flagged != 0 {
		t.Fatalf("sweep flagged %d, %v; want the edge to an approved memo kept", flagged, err)
	}
	// End it the way the old sweep did, then repair.
	// Times from Go, not the database's now(): the two clocks may differ, and
	// the repair compares against Go's.
	if _, err := db.ExecContext(ctx, `UPDATE smeldr_relations SET invalid_at = $1 WHERE id = 'e-keep'`, time.Now().UTC().Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO smeldr_provenance (id, timestamp, subject_type, subject_id, verb, from_state, to_state, actor_kind, actor_id, surface, reason)
		VALUES ('pv-1', now(), 'RelationEdge', 'e-keep', 'invalidate', 'live', 'swept', 'job', 'sweep-structural', '', 'source or target no longer alive')`); err != nil {
		t.Fatal(err)
	}
	dry, err := app.RepairSweptEdges(ctx, false)
	if err != nil || dry.Candidates != 1 || dry.Restored != 1 {
		t.Fatalf("dry run = %+v %v", dry, err)
	}
	run, err := app.RepairSweptEdges(ctx, true)
	if err != nil || run.Restored != 1 {
		t.Fatalf("apply = %+v %v", run, err)
	}
	var live int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM smeldr_relations WHERE target_id = 'memo-1' AND invalid_at IS NULL`).Scan(&live); err != nil || live != 1 {
		t.Errorf("live after repair = %d %v", live, err)
	}
	if again, err := app.RepairSweptEdges(ctx, true); err != nil || again.Restored != 0 || again.SkippedLive != 1 {
		t.Errorf("second apply = %+v %v", again, err)
	}
	// The restored row ended for another reason (a withdraw): the swept row is
	// superseded and stays ended.
	if _, err := db.ExecContext(ctx, `UPDATE smeldr_relations SET invalid_at = $1 WHERE target_id = 'memo-1' AND invalid_at IS NULL`, time.Now().UTC().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if run, err := app.RepairSweptEdges(ctx, true); err != nil || run.Restored != 0 || run.SkippedSuperseded != 1 {
		t.Errorf("superseded = %+v %v", run, err)
	}
}
