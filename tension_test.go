package smeldr

import (
	"context"
	"testing"
)

func setupTensionDB(t *testing.T) DB {
	t.Helper()
	db := newSQLiteDB(t)
	if err := CreateOrchestrationTables(db); err != nil {
		t.Fatalf("CreateOrchestrationTables: %v", err)
	}
	if err := CreateFindingTable(db); err != nil {
		t.Fatalf("CreateFindingTable: %v", err)
	}
	return db
}

func seedRatifiedDecision(t *testing.T, db DB, id, tensionRuleID string) {
	t.Helper()
	if err := NewSQLRepo[*Decision](db, Table("smeldr_decisions")).Save(context.Background(), &Decision{
		Node:           Node{ID: id, Slug: id},
		DecisionNumber: id, Scope: "core",
		TensionRuleID: tensionRuleID,
	}); err != nil {
		t.Fatalf("seed Decision %s: %v", id, err)
	}
	if _, err := db.ExecContext(context.Background(),
		`UPDATE smeldr_decisions SET status = 'ratified' WHERE id = $1`, id,
	); err != nil {
		t.Fatalf("ratify Decision %s: %v", id, err)
	}
}

func TestRecordDeclaredTension_NilStore_NoOp(t *testing.T) {
	db := setupTensionDB(t)
	recordDeclaredTension(context.Background(), db, nil, "rule-1")
}

func TestRecordDeclaredTension_EmptyTensionRuleID_NoOp(t *testing.T) {
	db := setupTensionDB(t)
	store := NewFindingStore(db)
	recordDeclaredTension(context.Background(), db, store, "")
	findings, err := store.List(context.Background(), "declared-tension")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("expected no Finding for an empty TensionRuleID, got %d", len(findings))
	}
}

func TestRecordDeclaredTension_BelowThreshold_NoFinding(t *testing.T) {
	db := setupTensionDB(t)
	store := NewFindingStore(db)
	orig := DefaultTensionThreshold
	DefaultTensionThreshold = 3
	t.Cleanup(func() { DefaultTensionThreshold = orig })

	seedRatifiedDecision(t, db, "d1", "rule-1")
	seedRatifiedDecision(t, db, "d2", "rule-1")
	// Only 2 ratified decisions declare tension — below the threshold of 3.
	recordDeclaredTension(context.Background(), db, store, "rule-1")

	findings, err := store.List(context.Background(), "declared-tension")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("expected no Finding below threshold, got %d", len(findings))
	}
}

func TestRecordDeclaredTension_AtThreshold_RecordsFinding(t *testing.T) {
	db := setupTensionDB(t)
	store := NewFindingStore(db)
	orig := DefaultTensionThreshold
	DefaultTensionThreshold = 3
	t.Cleanup(func() { DefaultTensionThreshold = orig })

	seedRatifiedDecision(t, db, "d1", "rule-1")
	seedRatifiedDecision(t, db, "d2", "rule-1")
	seedRatifiedDecision(t, db, "d3", "rule-1")
	recordDeclaredTension(context.Background(), db, store, "rule-1")

	findings, err := store.List(context.Background(), "declared-tension")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 Finding at threshold, got %d", len(findings))
	}
	f := findings[0]
	if f.SubjectType != "Rule" || f.SubjectID != "rule-1" {
		t.Errorf("SubjectType/SubjectID = %q/%q, want Rule/rule-1", f.SubjectType, f.SubjectID)
	}
	if f.Provenance != "asserted" {
		t.Errorf("Provenance = %q, want asserted", f.Provenance)
	}
}

func TestRecordDeclaredTension_UnratifiedDecisionsDoNotCount(t *testing.T) {
	db := setupTensionDB(t)
	store := NewFindingStore(db)
	orig := DefaultTensionThreshold
	DefaultTensionThreshold = 2
	t.Cleanup(func() { DefaultTensionThreshold = orig })

	// One ratified, one still proposed — only the ratified one counts.
	seedRatifiedDecision(t, db, "d1", "rule-1")
	if err := NewSQLRepo[*Decision](db, Table("smeldr_decisions")).Save(context.Background(), &Decision{
		Node:           Node{ID: "d2", Slug: "d2"},
		DecisionNumber: "d2", Scope: "core", TensionRuleID: "rule-1",
	}); err != nil {
		t.Fatalf("seed Decision d2: %v", err)
	}
	recordDeclaredTension(context.Background(), db, store, "rule-1")

	findings, err := store.List(context.Background(), "declared-tension")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("expected no Finding — only 1 of 2 tension-declaring decisions is ratified, got %d", len(findings))
	}
}

// TestRecordDeclaredTension_CountQueryError_LoggedNotPanicked pins the
// fail-open posture on the count query itself: a DB missing
// smeldr_decisions (e.g. App.Findings wired without App.Orchestration)
// must be logged and swallowed, never panicking or propagating — same
// fail-open discipline the whole decision-governance §6/§4 wiring uses
// throughout (RunAuthorityCheck's own callers included).
func TestRecordDeclaredTension_CountQueryError_LoggedNotPanicked(t *testing.T) {
	db := newSQLiteDB(t) // no CreateOrchestrationTables — smeldr_decisions absent
	if err := CreateFindingTable(db); err != nil {
		t.Fatalf("CreateFindingTable: %v", err)
	}
	store := NewFindingStore(db)
	recordDeclaredTension(context.Background(), db, store, "rule-1")
}

// TestRecordDeclaredTension_RecordError_LoggedNotPanicked pins the
// fail-open posture on FindingStore.Record itself: a store backed by a DB
// missing smeldr_findings must be logged and swallowed, matching
// SweepStructural's own onStale callback precedent (smeldr.go).
func TestRecordDeclaredTension_RecordError_LoggedNotPanicked(t *testing.T) {
	db := setupTensionDB(t)
	orig := DefaultTensionThreshold
	DefaultTensionThreshold = 1
	t.Cleanup(func() { DefaultTensionThreshold = orig })
	seedRatifiedDecision(t, db, "d1", "rule-1")

	// A FindingStore over a DB whose smeldr_findings table was dropped
	// after construction — Record's own INSERT fails.
	store := NewFindingStore(db)
	if _, err := db.ExecContext(context.Background(), `DROP TABLE smeldr_findings`); err != nil {
		t.Fatalf("drop smeldr_findings: %v", err)
	}
	recordDeclaredTension(context.Background(), db, store, "rule-1")
}

func TestRunDeclaredTensionAggregation_WrongTransition_NoOp(t *testing.T) {
	db := setupTensionDB(t)
	store := NewFindingStore(db)
	runDeclaredTensionAggregation(context.Background(), db, store,
		&Decision{Node: Node{ID: "d1"}, TensionRuleID: "rule-1"}, "ratified", "superseded")
	findings, err := store.List(context.Background(), "declared-tension")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(findings) != 0 {
		t.Error("expected no aggregation for a non-ratification transition")
	}
}

func TestRunDeclaredTensionAggregation_NonDecisionItem_NoOp(t *testing.T) {
	db := setupTensionDB(t)
	store := NewFindingStore(db)
	runDeclaredTensionAggregation(context.Background(), db, store,
		&Task{Node: Node{ID: "t1"}}, "proposed", "ratified")
	findings, err := store.List(context.Background(), "declared-tension")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(findings) != 0 {
		t.Error("expected no aggregation for a non-Decision item")
	}
}

func TestRunDeclaredTensionAggregation_Ratifies(t *testing.T) {
	db := setupTensionDB(t)
	store := NewFindingStore(db)
	orig := DefaultTensionThreshold
	DefaultTensionThreshold = 1
	t.Cleanup(func() { DefaultTensionThreshold = orig })

	seedRatifiedDecision(t, db, "d1", "rule-1")
	runDeclaredTensionAggregation(context.Background(), db, store,
		&Decision{Node: Node{ID: "d1"}, TensionRuleID: "rule-1"}, "proposed", "ratified")

	findings, err := store.List(context.Background(), "declared-tension")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 Finding, got %d", len(findings))
	}
}

func TestRunDeclaredTensionAggregationByID_WrongType_NoOp(t *testing.T) {
	db := setupTensionDB(t)
	store := NewFindingStore(db)
	runDeclaredTensionAggregationByID(context.Background(), db, store, "Task", "t1", "proposed", "ratified")
	findings, err := store.List(context.Background(), "declared-tension")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(findings) != 0 {
		t.Error("expected no aggregation for a non-Decision type")
	}
}

func TestRunDeclaredTensionAggregationByID_LookupError_NoOp(t *testing.T) {
	db := setupTensionDB(t)
	store := NewFindingStore(db)
	// itemID does not exist in smeldr_decisions — QueryRowContext.Scan
	// fails, logged and swallowed (fail-open), not panicking.
	runDeclaredTensionAggregationByID(context.Background(), db, store, "Decision", "no-such-id", "proposed", "ratified")
}

func TestRunDeclaredTensionAggregationByID_Ratifies(t *testing.T) {
	db := setupTensionDB(t)
	store := NewFindingStore(db)
	orig := DefaultTensionThreshold
	DefaultTensionThreshold = 1
	t.Cleanup(func() { DefaultTensionThreshold = orig })

	seedRatifiedDecision(t, db, "d1", "rule-1")
	runDeclaredTensionAggregationByID(context.Background(), db, store, "Decision", "d1", "proposed", "ratified")

	findings, err := store.List(context.Background(), "declared-tension")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 Finding, got %d", len(findings))
	}
}
