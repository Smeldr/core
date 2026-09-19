// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"testing"
)

func TestCreateFindingTable_Idempotent(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateFindingTable(db); err != nil {
		t.Fatalf("first CreateFindingTable: %v", err)
	}
	if err := CreateFindingTable(db); err != nil {
		t.Fatalf("second CreateFindingTable (idempotent): %v", err)
	}
}

func TestFindingStore_Record_InsertsNew(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateFindingTable(db); err != nil {
		t.Fatalf("CreateFindingTable: %v", err)
	}
	store := NewFindingStore(db)
	ctx := context.Background()

	err := store.Record(ctx, Finding{
		Detector:    "structural",
		SubjectType: "RelationEdge",
		SubjectID:   "edge-1",
		Provenance:  "detected",
		Message:     "relation target no longer alive",
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}

	got, err := store.List(ctx, "structural")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d findings, want 1", len(got))
	}
	f := got[0]
	if f.ID == "" {
		t.Error("ID is empty, want a generated ID")
	}
	if f.SubjectID != "edge-1" || f.Provenance != "detected" {
		t.Errorf("unexpected finding: %+v", f)
	}
	if f.FirstSeenAt.IsZero() || f.LastSeenAt.IsZero() {
		t.Error("FirstSeenAt/LastSeenAt not set")
	}
	if !f.FirstSeenAt.Equal(f.LastSeenAt) {
		t.Error("FirstSeenAt and LastSeenAt should be equal on first insert")
	}
}

func TestFindingStore_Record_DedupsOnRepeat(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateFindingTable(db); err != nil {
		t.Fatalf("CreateFindingTable: %v", err)
	}
	store := NewFindingStore(db)
	ctx := context.Background()

	base := Finding{
		Detector:    "structural",
		SubjectType: "RelationEdge",
		SubjectID:   "edge-1",
		Provenance:  "detected",
		Message:     "first message",
	}
	if err := store.Record(ctx, base); err != nil {
		t.Fatalf("first Record: %v", err)
	}
	first, err := store.List(ctx, "structural")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	firstSeen := first[0].FirstSeenAt
	firstID := first[0].ID

	updated := base
	updated.Message = "second message"
	if err := store.Record(ctx, updated); err != nil {
		t.Fatalf("second Record: %v", err)
	}

	got, err := store.List(ctx, "structural")
	if err != nil {
		t.Fatalf("List after dedup: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d findings after repeat Record, want 1 (dedup)", len(got))
	}
	f := got[0]
	if f.ID != firstID {
		t.Errorf("ID changed across dedup: %q -> %q, want stable", firstID, f.ID)
	}
	if !f.FirstSeenAt.Equal(firstSeen) {
		t.Errorf("FirstSeenAt changed across dedup: %v -> %v, want unchanged", firstSeen, f.FirstSeenAt)
	}
	if f.Message != "second message" {
		t.Errorf("Message = %q, want updated to %q", f.Message, "second message")
	}
}

func TestFindingStore_List_FilterByDetector(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateFindingTable(db); err != nil {
		t.Fatalf("CreateFindingTable: %v", err)
	}
	store := NewFindingStore(db)
	ctx := context.Background()

	if err := store.Record(ctx, Finding{Detector: "structural", SubjectType: "RelationEdge", SubjectID: "e1", Provenance: "detected"}); err != nil {
		t.Fatalf("Record structural: %v", err)
	}
	if err := store.Record(ctx, Finding{Detector: "other", SubjectType: "RelationEdge", SubjectID: "e2", Provenance: "detected"}); err != nil {
		t.Fatalf("Record other: %v", err)
	}

	filtered, err := store.List(ctx, "structural")
	if err != nil {
		t.Fatalf("List(structural): %v", err)
	}
	if len(filtered) != 1 || filtered[0].Detector != "structural" {
		t.Errorf("List(structural) = %+v, want 1 structural finding", filtered)
	}

	all, err := store.List(ctx, "")
	if err != nil {
		t.Fatalf("List(\"\"): %v", err)
	}
	if len(all) != 2 {
		t.Errorf("List(\"\") = %d findings, want 2", len(all))
	}
}

// TestAppSweepStructural_RecordsFinding is the sweep-to-finding bridge's
// own integration proof: a newly-flagged stale edge produces exactly one
// Finding row when a FindingStore is configured.
func TestAppSweepStructural_RecordsFinding(t *testing.T) {
	db, rs, app := setupTargetCheckerDB(t)
	if err := CreateFindingTable(db); err != nil {
		t.Fatalf("CreateFindingTable: %v", err)
	}
	app.Findings(NewFindingStore(db))

	seedTask(t, db, "task-1")
	seedDecision(t, db, "decision-1", "proposed")
	assertTaskDependsOnDecision(t, rs, "task-1", "decision-1")
	if _, err := db.ExecContext(context.Background(),
		"DELETE FROM smeldr_decisions WHERE id=$1", "decision-1"); err != nil {
		t.Fatalf("delete Decision: %v", err)
	}

	if _, f, _, err := app.SweepStructural(context.Background()); err != nil {
		t.Fatalf("SweepStructural: %v", err)
	} else if f != 1 {
		t.Fatalf("want 1 flagged edge, got %d", f)
	}

	findings, err := app.FindingStore().List(context.Background(), "structural")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1", len(findings))
	}
	fnd := findings[0]
	if fnd.SubjectType != "RelationEdge" || fnd.Provenance != "detected" {
		t.Errorf("unexpected finding: %+v", fnd)
	}

	// Sweeping again with the same edge already flagged must not create a
	// duplicate finding — the edge was already invalidated by the first
	// sweep, so onStale does not fire a second time (staled dedup inside
	// RelationStore.SweepStructural itself), and the finding row stays one.
	if _, f2, _, err := app.SweepStructural(context.Background()); err != nil {
		t.Fatalf("second SweepStructural: %v", err)
	} else if f2 != 0 {
		t.Errorf("second sweep: want 0 newly-flagged (already invalid), got %d", f2)
	}
	findings2, err := app.FindingStore().List(context.Background(), "structural")
	if err != nil {
		t.Fatalf("List after second sweep: %v", err)
	}
	if len(findings2) != 1 {
		t.Errorf("got %d findings after second sweep, want still 1 (no duplicate)", len(findings2))
	}
}

// TestAppSweepStructural_NoFindingStore_Unaffected is the regression
// guard: with no FindingStore configured, SweepStructural's own
// behaviour is unchanged from before this feature existed.
func TestAppSweepStructural_NoFindingStore_Unaffected(t *testing.T) {
	db, rs, app := setupTargetCheckerDB(t)
	seedTask(t, db, "task-1")
	seedDecision(t, db, "decision-1", "proposed")
	assertTaskDependsOnDecision(t, rs, "task-1", "decision-1")
	if _, err := db.ExecContext(context.Background(),
		"DELETE FROM smeldr_decisions WHERE id=$1", "decision-1"); err != nil {
		t.Fatalf("delete Decision: %v", err)
	}

	if _, f, _, err := app.SweepStructural(context.Background()); err != nil {
		t.Fatalf("SweepStructural: %v", err)
	} else if f != 1 {
		t.Errorf("want 1 flagged edge, got %d", f)
	}
	if app.FindingStore() != nil {
		t.Error("FindingStore() should be nil - Findings() was never called")
	}
}

func TestApp_Findings_GetterSetter(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateFindingTable(db); err != nil {
		t.Fatalf("CreateFindingTable: %v", err)
	}
	app := &App{}
	if app.FindingStore() != nil {
		t.Error("FindingStore() should be nil before Findings() is called")
	}
	store := NewFindingStore(db)
	if app.Findings(store) != app {
		t.Error("Findings should return the same *App for chaining")
	}
	if app.FindingStore() != store {
		t.Error("FindingStore() should return the store set by Findings()")
	}
}
