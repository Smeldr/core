package smeldr

// Tests for emitConflictDetectedSignal (01a0dd64-2, D85): a contradicts edge
// write, via any of the three relation-write MCP tools, auto-fires two
// conflict-detected Signal rows, each naming the *other* Decision as its own
// subject.

import (
	"context"
	"testing"
)

// setupRelationStoreWithSignals is [setupRelationStore] plus
// CreateOrchestrationTables — the missing ingredient for any test that
// exercises emitConflictDetectedSignal's own raw INSERT into smeldr_signals.
// setSignalDeps is deliberately left unwired (nil, nil, nil), the same state
// a RelationStore has before App.Handler() runs — every test in this file
// either doesn't care about dispatch, or (TestInsertEdge_Contradicts_NilSignalDeps_NoPanic)
// is specifically testing that state.
func setupRelationStoreWithSignals(t *testing.T) *RelationStore {
	t.Helper()
	db := newSQLiteDB(t)
	if err := CreateRelationTables(db); err != nil {
		t.Fatalf("CreateRelationTables: %v", err)
	}
	if err := CreateOrchestrationTables(db); err != nil {
		t.Fatalf("CreateOrchestrationTables: %v", err)
	}
	store, err := NewRelationStore(db)
	if err != nil {
		t.Fatalf("NewRelationStore: %v", err)
	}
	if err := store.UpsertKind(context.Background(), RelationKindDef{
		TypeName: "contradicts", Mode: "asserted", Directional: false,
	}); err != nil {
		t.Fatalf("UpsertKind(contradicts): %v", err)
	}
	return store
}

// conflictSignalRow is a minimal projection of a conflict-detected row for
// assertions in this file.
type conflictSignalRow struct {
	signalType, receiver, subjectType, subjectID string
}

func queryConflictSignals(t *testing.T, store *RelationStore) []conflictSignalRow {
	t.Helper()
	rows, err := store.db.QueryContext(context.Background(),
		`SELECT signal_type, receiver, subject_type, subject_id FROM smeldr_signals ORDER BY subject_id`)
	if err != nil {
		t.Fatalf("query smeldr_signals: %v", err)
	}
	defer rows.Close()
	var out []conflictSignalRow
	for rows.Next() {
		var r conflictSignalRow
		if err := rows.Scan(&r.signalType, &r.receiver, &r.subjectType, &r.subjectID); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, r)
	}
	return out
}

func TestInsertEdge_Contradicts_EmitsTwoConflictDetectedSignals(t *testing.T) {
	store := setupRelationStoreWithSignals(t)
	ctx := context.Background()

	if _, err := store.MCPAssertRelation(ctx, "Decision", "d1", "Decision", "d2", "contradicts", nil, nil, nil, nil); err != nil {
		t.Fatalf("MCPAssertRelation: %v", err)
	}

	rows := queryConflictSignals(t, store)
	if len(rows) != 2 {
		t.Fatalf("len(rows) = %d, want 2", len(rows))
	}
	for _, r := range rows {
		if r.signalType != "conflict-detected" {
			t.Errorf("signal_type = %q, want conflict-detected", r.signalType)
		}
		if r.receiver != decisionRatifyOperation {
			t.Errorf("receiver = %q, want %q", r.receiver, decisionRatifyOperation)
		}
		if r.subjectType != "Decision" {
			t.Errorf("subject_type = %q, want Decision", r.subjectType)
		}
	}
	if rows[0].subjectID == rows[1].subjectID {
		t.Errorf("both rows have the same subject_id %q, want one d1 and one d2", rows[0].subjectID)
	}
	for _, r := range rows {
		if r.subjectID != "d1" && r.subjectID != "d2" {
			t.Errorf("subject_id = %q, want d1 or d2", r.subjectID)
		}
	}
}

func TestInsertEdge_Contradicts_ProposeRelation_AlsoEmits(t *testing.T) {
	store := setupRelationStoreWithSignals(t)
	ctx := context.Background()

	if _, err := store.MCPProposeRelation(ctx, "Decision", "d1", "Decision", "d2", "contradicts", nil, nil, nil, nil); err != nil {
		t.Fatalf("MCPProposeRelation: %v", err)
	}

	rows := queryConflictSignals(t, store)
	if len(rows) != 2 {
		t.Fatalf("len(rows) = %d, want 2 — hook must fire for propose_relation (inferred) too, per D85", len(rows))
	}
}

func TestInsertEdge_Contradicts_ObserveRelation_AlsoEmits(t *testing.T) {
	store := setupRelationStoreWithSignals(t)
	ctx := context.Background()

	if _, err := store.MCPObserveRelation(ctx, "Decision", "d1", "Decision", "d2", "contradicts", nil, nil, nil, nil); err != nil {
		t.Fatalf("MCPObserveRelation: %v", err)
	}

	rows := queryConflictSignals(t, store)
	if len(rows) != 2 {
		t.Fatalf("len(rows) = %d, want 2 — architect-approved: all three edge classes fire this, not just asserted/inferred", len(rows))
	}
}

func TestInsertEdge_NonContradicts_NoSignalEmitted(t *testing.T) {
	store := setupRelationStoreWithSignals(t)
	ctx := context.Background()
	if err := store.UpsertKind(ctx, RelationKindDef{
		TypeName: "supersedes", Mode: "asserted", Directional: true,
	}); err != nil {
		t.Fatalf("UpsertKind(supersedes): %v", err)
	}

	if _, err := store.MCPAssertRelation(ctx, "Decision", "d1", "Decision", "d2", "supersedes", nil, nil, nil, nil); err != nil {
		t.Fatalf("MCPAssertRelation: %v", err)
	}

	rows := queryConflictSignals(t, store)
	if len(rows) != 0 {
		t.Errorf("len(rows) = %d, want 0 — a non-contradicts edge must not emit conflict-detected", len(rows))
	}
}

func TestInsertEdge_Contradicts_NilSignalDeps_NoPanic(t *testing.T) {
	store := setupRelationStoreWithSignals(t)
	ctx := context.Background()

	// setupRelationStoreWithSignals never calls setSignalDeps — webhookStore/
	// webhookPool/eventBroadcaster are all nil, the same state as before
	// App.Handler() runs. The INSERT itself must still succeed; only the
	// dispatch half is allowed to no-op.
	if _, err := store.MCPAssertRelation(ctx, "Decision", "d1", "Decision", "d2", "contradicts", nil, nil, nil, nil); err != nil {
		t.Fatalf("MCPAssertRelation with nil signal deps: %v", err)
	}

	rows := queryConflictSignals(t, store)
	if len(rows) != 2 {
		t.Errorf("len(rows) = %d, want 2 even with nil signal deps", len(rows))
	}
}

func TestOrchDecisionFlow_UsesSharedRatifyOperationConstant(t *testing.T) {
	flow := orchDecisionFlow()
	for _, tr := range flow.Transitions {
		if tr.From == "proposed" && tr.To == "ratified" {
			if tr.RequiredOperation != decisionRatifyOperation {
				t.Errorf("RequiredOperation = %q, want decisionRatifyOperation (%q)", tr.RequiredOperation, decisionRatifyOperation)
			}
			return
		}
	}
	t.Fatal("orchDecisionFlow has no proposed→ratified transition")
}
