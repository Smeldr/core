package smeldr

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// dedupFixture is a relations database with a plain kind, and its store.
func dedupFixture(t *testing.T) (*sql.DB, *RelationStore) {
	t.Helper()
	db := newSQLiteDB(t)
	if err := CreateRelationTables(db); err != nil {
		t.Fatal(err)
	}
	store, err := NewRelationStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertKind(context.Background(), RelationKindDef{TypeName: "cites", Mode: "asserted", Directional: true}); err != nil {
		t.Fatal(err)
	}
	return db, store
}

func liveIndexExists(t *testing.T, db *sql.DB) bool {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = 'idx_relations_live_tuple'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n == 1
}

// insertRawEdge writes a relation row directly, as old data would have it.
func insertRawEdge(t *testing.T, db *sql.DB, id string, created time.Time) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO smeldr_relations (id, source_type, source_id, target_type, target_id, relation_kind, edge_class, attributes, created_at, updated_at)
		VALUES ($1, 'post', 'p1', 'source', 's1', 'cites', 'asserted', '{}', $2, $2)`, id, created); err != nil {
		t.Fatal(err)
	}
}

func liveCount(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM smeldr_relations WHERE invalid_at IS NULL`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// The index is made on boot when there are no duplicates, and skipped, with
// boot going on, when there are.
func TestRelationsLiveIndex(t *testing.T) {
	db, _ := dedupFixture(t)
	if !liveIndexExists(t, db) {
		t.Fatal("no duplicates: the live-tuple index must be made")
	}
	if _, err := db.Exec(`DROP INDEX idx_relations_live_tuple`); err != nil {
		t.Fatal(err)
	}
	insertRawEdge(t, db, "r1", time.Now().Add(-time.Hour))
	insertRawEdge(t, db, "r2", time.Now())
	if err := CreateRelationTables(db); err != nil {
		t.Fatalf("boot with duplicates must go on: %v", err)
	}
	if liveIndexExists(t, db) {
		t.Error("duplicates: the index must not be made")
	}
	if err := ensureLiveRelationIndex(context.Background(), identityFaultDB{db, "HAVING COUNT(*) > 1"}); err == nil {
		t.Error("a failed duplicate check: want an error")
	}
}

// raceDB hides the live row from the assert path's lookup, as a writer that
// committed between the lookup and the insert would.
type raceDB struct{ DB }

func (d raceDB) QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row {
	if strings.Contains(q, "target_id=$4 AND relation_kind=$5") {
		return d.DB.QueryRowContext(ctx, "SELECT id FROM smeldr_relations WHERE 1 = 0")
	}
	return d.DB.QueryRowContext(ctx, q, args...)
}

// A first assert that loses the race is ErrConflict naming the live row; an
// internal writer that loses it finds the relation already recorded and goes
// on, writing nothing twice.
func TestAssert_LostRace(t *testing.T) {
	db, store := dedupFixture(t)
	ctx := context.Background()
	edge := RelationEdge{SourceType: "post", SourceID: "p1", TargetType: "source", TargetID: "s1", RelationKind: "cites", EdgeClass: "asserted", Attributes: json.RawMessage("{}")}
	if err := store.Assert(ctx, edge); err != nil {
		t.Fatal(err)
	}
	var first string
	if err := db.QueryRow(`SELECT id FROM smeldr_relations`).Scan(&first); err != nil {
		t.Fatal(err)
	}
	racing := *store
	racing.db = raceDB{db}
	err := racing.Assert(ctx, edge)
	if !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), first) {
		t.Errorf("a lost first assert: %v; want ErrConflict naming %s", err, first)
	}
	got, isNew, err := writeEdgeRow(ctx, raceDB{db}, edge)
	if err != nil || isNew || got.ID != first {
		t.Errorf("an internal writer losing the race: %+v, %v, %v; want the live row, not new, no error", got.ID, isNew, err)
	}
	if n := liveCount(t, db); n != 1 {
		t.Errorf("%d live rows; want 1", n)
	}
	// Nothing explains a lost insert: an internal error.
	if err := liveRelationLost(ctx, db, RelationEdge{SourceType: "x"}, time.Now()); !errors.Is(err, ErrInternal) {
		t.Errorf("liveRelationLost with no live row: %v", err)
	}
	if _, _, err := writeEdgeRow(ctx, identityFaultDB{raceDB{db}, "SELECT rev FROM smeldr_relations"}, edge); err == nil {
		t.Error("a failed rev read after a lost race: want an error")
	}
	// After the relation ends, a new row may be asserted.
	if err := store.Withdraw(ctx, first, "gone"); err != nil {
		t.Fatal(err)
	}
	if err := store.Assert(ctx, edge); err != nil {
		t.Errorf("assert after the end: %v", err)
	}
	if n := liveCount(t, db); n != 1 {
		t.Errorf("%d live rows after the end and a new assert; want 1", n)
	}
}

// The migration keeps the oldest live row of each relation, ends the rest as
// deduplicated, then makes the index; a dry run writes nothing.
func TestMigrateRelationDuplicates(t *testing.T) {
	app, db, _ := placesApp(t)
	_ = app.Handler() // wires provenance into the relation store
	ctx := context.Background()
	if _, err := db.Exec(`DROP INDEX idx_relations_live_tuple`); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	insertRawEdge(t, db, "r-new", now)
	insertRawEdge(t, db, "r-old", now.Add(-2*time.Hour))
	insertRawEdge(t, db, "r-mid", now.Add(-time.Hour))

	dry, err := app.MigrateRelationDuplicates(ctx, RelationDedupOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if !dry.DryRun || len(dry.Duplicates) != 1 || dry.Duplicates[0].Kept != "r-old" || dry.RowsEnded != 2 ||
		dry.Duplicates[0].Source != "post:p1" || dry.Duplicates[0].Kind != "cites" || dry.IndexMade {
		t.Fatalf("dry run = %+v", dry)
	}
	if n := liveCount(t, db); n != 3 || liveIndexExists(t, db) {
		t.Fatalf("a dry run wrote: %d live, index %v", n, liveIndexExists(t, db))
	}

	rep, err := app.MigrateRelationDuplicates(ctx, RelationDedupOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.RowsEnded != 2 || !rep.IndexMade || !liveIndexExists(t, db) {
		t.Errorf("apply = %+v", rep)
	}
	var live string
	if err := db.QueryRow(`SELECT id FROM smeldr_relations WHERE invalid_at IS NULL`).Scan(&live); err != nil || live != "r-old" {
		t.Errorf("the kept row = %s, %v; want r-old", live, err)
	}
	recs, err := NewProvenanceStore(db).List(ctx, ProvenanceFilter{SubjectID: "r-new"})
	if err != nil || len(recs) != 1 || recs[0].ToState != EdgeEndDeduplicated || recs[0].ActorID != relationDedupJob || !strings.Contains(recs[0].Reason, "r-old") {
		t.Errorf("the end's provenance = %+v, %v", recs, err)
	}
	again, err := app.MigrateRelationDuplicates(ctx, RelationDedupOptions{})
	if err != nil || len(again.Duplicates) != 0 || !again.IndexMade {
		t.Errorf("a second run = %+v, %v", again, err)
	}

	bare := New(MustConfig(Config{BaseURL: "https://example.com", Secret: []byte(transitionItemTestSecret), DB: newSQLiteDB(t)}))
	if _, err := bare.MigrateRelationDuplicates(ctx, RelationDedupOptions{}); !errors.Is(err, ErrBadRequest) {
		t.Errorf("no relations: %v; want ErrBadRequest", err)
	}
}

// Database failures in the migration are errors.
func TestMigrateRelationDuplicates_Failures(t *testing.T) {
	for _, match := range []string{"HAVING COUNT(*) > 1", "ORDER BY created_at, id", "UPDATE smeldr_relations SET invalid_at"} {
		app, db, rs := placesApp(t)
		if _, err := db.Exec(`DROP INDEX idx_relations_live_tuple`); err != nil {
			t.Fatal(err)
		}
		insertRawEdge(t, db, "a", time.Now().Add(-time.Hour))
		insertRawEdge(t, db, "b", time.Now())
		rs.db = identityFaultDB{db, match}
		if _, err := app.MigrateRelationDuplicates(context.Background(), RelationDedupOptions{}); err == nil {
			t.Errorf("%s: want an error", match)
		}
	}
}

// The rest of the failure paths, and a record named by its address.
func TestRelationDedup_MoreFailures(t *testing.T) {
	ctx := context.Background()
	db, _ := dedupFixture(t)
	if err := ensureLiveRelationIndex(ctx, identityFaultDB{db, "CREATE UNIQUE INDEX IF NOT EXISTS idx_relations_live_tuple"}); err == nil {
		t.Error("a failed index create: want an error")
	}
	edge := RelationEdge{SourceType: "post", SourceID: "p9", TargetType: "source", TargetID: "s9", RelationKind: "cites", EdgeClass: "asserted", Attributes: json.RawMessage("{}")}
	if _, _, err := writeEdgeRow(ctx, identityFaultDB{db, "ON CONFLICT DO NOTHING"}, edge); err == nil {
		t.Error("a failed insert: want an error")
	}

	app, adb, rs := placesApp(t)
	if _, err := adb.Exec(`INSERT INTO smeldr_areas (id, slug, created_at, updated_at, name, address) VALUES ('are-9', 'are-9', datetime('now'), datetime('now'), 'Billing', 'ARE9')`); err != nil {
		t.Fatal(err)
	}
	if got := app.recordLabel(TrustedRead(ctx), "Area", "are-9"); got != "ARE9" {
		t.Errorf("recordLabel = %q; want the address", got)
	}
	if _, err := adb.Exec(`DROP INDEX idx_relations_live_tuple`); err != nil {
		t.Fatal(err)
	}
	insertRawEdge(t, adb, "x1", time.Now().Add(-time.Hour))
	insertRawEdge(t, adb, "x2", time.Now())
	rs.db = identityFaultDB{adb, "CREATE UNIQUE INDEX IF NOT EXISTS idx_relations_live_tuple"}
	if _, err := app.MigrateRelationDuplicates(ctx, RelationDedupOptions{}); err == nil {
		t.Error("a failed index create after the ends: want an error")
	}
}

// oneRowDB returns one row for a relation's live rows, as if the others had
// ended between the duplicate search and the read.
type oneRowDB struct{ DB }

func (d oneRowDB) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	if strings.Contains(q, "ORDER BY created_at, id") {
		q += " LIMIT 1"
	}
	return d.DB.QueryContext(ctx, q, args...)
}

// A relation whose extra rows ended meanwhile is left alone.
func TestMigrateRelationDuplicates_EndedMeanwhile(t *testing.T) {
	app, db, rs := placesApp(t)
	if _, err := db.Exec(`DROP INDEX idx_relations_live_tuple`); err != nil {
		t.Fatal(err)
	}
	insertRawEdge(t, db, "a", time.Now().Add(-time.Hour))
	insertRawEdge(t, db, "b", time.Now())
	rs.db = oneRowDB{db}
	rep, err := app.MigrateRelationDuplicates(context.Background(), RelationDedupOptions{DryRun: true})
	if err != nil || len(rep.Duplicates) != 0 || rep.RowsEnded != 0 {
		t.Errorf("ended meanwhile = %+v, %v; want nothing", rep, err)
	}
}
