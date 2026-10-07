//go:build integration

package pgx

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	smeldr "smeldr.dev/core"
)

// Relation history on Postgres: one row per life, every end recorded with its
// cause, and get_relations reading the causes back.
func TestPG_RelationHistory(t *testing.T) {
	db, _ := isolatedDB(t)
	ctx := context.Background()
	if err := smeldr.CreateRelationTables(db); err != nil {
		t.Fatalf("CreateRelationTables: %v", err)
	}
	if err := smeldr.CreateProvenanceTable(db); err != nil {
		t.Fatalf("CreateProvenanceTable: %v", err)
	}
	rs, err := smeldr.NewRelationStore(db)
	if err != nil {
		t.Fatalf("NewRelationStore: %v", err)
	}
	if err := rs.UpsertKind(ctx, smeldr.RelationKindDef{TypeName: "tagged", Mode: "asserted", Directional: true}); err != nil {
		t.Fatalf("UpsertKind: %v", err)
	}
	prov := smeldr.NewProvenanceStore(db)
	app := smeldr.New(smeldr.Config{BaseURL: "http://localhost", Secret: []byte(pgTestSecret), DB: db})
	app.Relations(rs)
	app.Provenance(prov)
	app.Handler() // wires the provenance store into the relation store

	alice := smeldr.NewContextWithUser(smeldr.User{ID: "alice", Roles: []smeldr.Role{smeldr.Editor}})
	edge := smeldr.RelationEdge{SourceType: "article", SourceID: "a1", TargetType: "tag", TargetID: "t1", RelationKind: "tagged", EdgeClass: "asserted"}

	// Assert, withdraw, assert again: two rows, two lives.
	if err := rs.Assert(alice, edge); err != nil {
		t.Fatalf("assert: %v", err)
	}
	first, _ := rs.GetLiveBySource(ctx, "article", "a1", "")
	if err := rs.Withdraw(alice, first[0].ID, "wrong tag"); err != nil {
		t.Fatalf("withdraw: %v", err)
	}
	if err := rs.Withdraw(alice, first[0].ID, "again"); !errors.Is(err, smeldr.ErrConflict) {
		t.Errorf("withdrawing an ended row = %v; want ErrConflict", err)
	}
	time.Sleep(5 * time.Millisecond)
	if err := rs.Assert(alice, edge); err != nil {
		t.Fatalf("re-assert: %v", err)
	}
	rows, _ := rs.GetBySource(ctx, "article", "a1", "")
	if len(rows) != 2 || rows[0].ID != first[0].ID || rows[0].InvalidAt == nil || rows[1].InvalidAt != nil {
		t.Fatalf("rows = %+v; want the ended first life and a new live one", rows)
	}

	// The sweep ends the second life, with its job as the actor.
	dead := func(_ context.Context, typ, _ string) (bool, error) { return typ != "tag", nil }
	if _, flagged, _, err := rs.SweepStructural(ctx, dead, func(context.Context, smeldr.RelationEdge) {}); err != nil || flagged != 1 {
		t.Fatalf("sweep = %d, %v", flagged, err)
	}

	got, err := rs.MCPGetRelations(alice, "article", "a1", "source", "")
	if err != nil || len(got) != 2 {
		t.Fatalf("get_relations = %+v, %v", got, err)
	}
	if got[0].Ended == nil || got[0].Ended.Cause != smeldr.EdgeEndWithdrawn || got[0].Ended.ActorID != "alice" || got[0].Ended.Reason != "wrong tag" {
		t.Errorf("first life ended = %+v", got[0].Ended)
	}
	if got[1].Ended == nil || got[1].Ended.Cause != smeldr.EdgeEndSwept || got[1].Ended.ActorID != "sweep-structural" {
		t.Errorf("second life ended = %+v", got[1].Ended)
	}

	// Two provenance pairs: assert and invalidate per life.
	for i, row := range rows {
		recs, err := prov.List(ctx, smeldr.ProvenanceFilter{SubjectType: "RelationEdge", SubjectID: row.ID})
		if err != nil {
			t.Fatal(err)
		}
		var vs []string
		for _, r := range recs {
			vs = append(vs, r.Verb)
		}
		sort.Strings(vs)
		if len(vs) != 2 || vs[0] != "assert" || vs[1] != "invalidate" {
			t.Errorf("life %d records = %v; want one assert and one invalidate", i+1, vs)
		}
	}

	// Recompute ends instead of deleting.
	if err := rs.RecomputeAsserted(alice, "article", "a2", []smeldr.RelationEdge{{TargetType: "tag", TargetID: "t2", RelationKind: "tagged"}}); err != nil {
		t.Fatal(err)
	}
	if err := rs.RecomputeAsserted(alice, "article", "a2", nil); err != nil {
		t.Fatal(err)
	}
	a2, _ := rs.MCPGetRelations(alice, "article", "a2", "source", "")
	if len(a2) != 1 || a2[0].Ended == nil || a2[0].Ended.Cause != smeldr.EdgeEndRecomputed || a2[0].Ended.ActorID != "alice" {
		t.Errorf("recomputed = %+v", a2)
	}
}
