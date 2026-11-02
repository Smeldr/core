//go:build integration

package pgx

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	smeldr "smeldr.dev/core"
)

// One live row per relation on Postgres (T1019): the partial unique index made
// on boot refuses a second live row, an assert of a relation that exists
// reuses its row, and the dedup migration keeps the oldest and remakes the
// index.
func TestPG_RelationLiveUnique(t *testing.T) {
	db, _ := isolatedDB(t)
	ctx := context.Background()
	if err := smeldr.CreateRelationTables(db); err != nil {
		t.Fatal(err)
	}
	if err := smeldr.CreateProvenanceTable(db); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pg_indexes WHERE indexname = 'idx_relations_live_tuple'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("the live-tuple index on Postgres: %d, %v", n, err)
	}
	insert := func(id string, at time.Time) error {
		_, err := db.ExecContext(ctx, `INSERT INTO smeldr_relations (id, source_type, source_id, target_type, target_id, relation_kind, edge_class, attributes, created_at, updated_at)
			VALUES ($1, 'post', 'p1', 'source', 's1', 'cites', 'asserted', '{}', $2, $2)`, id, at)
		return err
	}
	if err := insert("first", time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := insert("second", time.Now()); err == nil {
		t.Fatal("a second live row of the relation: want a unique violation")
	}

	rs, err := smeldr.NewRelationStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := rs.UpsertKind(ctx, smeldr.RelationKindDef{TypeName: "cites", Mode: "asserted", Directional: true}); err != nil {
		t.Fatal(err)
	}
	edge := smeldr.RelationEdge{SourceType: "post", SourceID: "p1", TargetType: "source", TargetID: "s1", RelationKind: "cites", EdgeClass: "asserted", Attributes: json.RawMessage("{}")}
	if err := rs.Assert(ctx, edge); err != nil {
		t.Fatalf("assert of an existing relation reuses its row: %v", err)
	}
	edge.ID = smeldr.NewID()
	if err := rs.Assert(ctx, edge); !errors.Is(err, smeldr.ErrConflict) {
		t.Errorf("a new row for a live relation: %v; want ErrConflict", err)
	}

	app := smeldr.New(smeldr.Config{BaseURL: "http://localhost", Secret: []byte(pgTestSecret), DB: db})
	app.Relations(rs)
	if _, err := db.ExecContext(ctx, `DROP INDEX idx_relations_live_tuple`); err != nil {
		t.Fatal(err)
	}
	if err := insert("second", time.Now()); err != nil {
		t.Fatal(err)
	}
	rep, err := app.MigrateRelationDuplicates(ctx, smeldr.RelationDedupOptions{})
	if err != nil {
		t.Fatalf("MigrateRelationDuplicates: %v", err)
	}
	if len(rep.Duplicates) != 1 || rep.Duplicates[0].Kept != "first" || rep.RowsEnded != 1 || !rep.IndexMade {
		t.Errorf("migration on Postgres = %+v", rep)
	}
}
