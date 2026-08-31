package smeldr

import (
	"context"
	"encoding/json"
	"testing"
)

// — TypePairs validation at assert time (D61, item 1) ————————————————————————

func TestAssert_TypePairsViolation(t *testing.T) {
	store := setupRelationStore(t)
	ctx := context.Background()
	pairs, _ := json.Marshal([]map[string]string{{"source_type": "Goal", "target_type": "Task"}})
	if err := store.UpsertKind(ctx, RelationKindDef{
		TypeName: "contains", Mode: "asserted", Directional: true,
		TypePairs: json.RawMessage(pairs),
	}); err != nil {
		t.Fatalf("UpsertKind: %v", err)
	}

	err := store.Assert(ctx, RelationEdge{
		SourceType: "Task", SourceID: "t1",
		TargetType: "Task", TargetID: "t2",
		RelationKind: "contains", EdgeClass: "asserted",
	})
	if err == nil {
		t.Fatal("Assert: want error for Task→Task, contains only permits Goal→Task")
	}

	edges, gerr := store.GetBySource(ctx, "Task", "t1", "contains")
	if gerr != nil {
		t.Fatalf("GetBySource: %v", gerr)
	}
	if len(edges) != 0 {
		t.Errorf("want no edge persisted after a TypePairs violation, got %d", len(edges))
	}
}

func TestAssert_TypePairsAllowed(t *testing.T) {
	store := setupRelationStore(t)
	ctx := context.Background()
	pairs, _ := json.Marshal([]map[string]string{{"source_type": "Goal", "target_type": "Task"}})
	if err := store.UpsertKind(ctx, RelationKindDef{
		TypeName: "contains", Mode: "asserted", Directional: true,
		TypePairs: json.RawMessage(pairs),
	}); err != nil {
		t.Fatalf("UpsertKind: %v", err)
	}

	if err := store.Assert(ctx, RelationEdge{
		SourceType: "Goal", SourceID: "g1",
		TargetType: "Task", TargetID: "t1",
		RelationKind: "contains", EdgeClass: "asserted",
	}); err != nil {
		t.Fatalf("Assert: want success for Goal→Task, got %v", err)
	}
}

func TestAssert_TypePairsEmpty_Permissive(t *testing.T) {
	store := setupRelationStore(t)
	ctx := context.Background()
	// No TypePairs declared — the kind is unconstrained, matching
	// extractRelationEdges's own existing treatment (smeldr.go).
	if err := store.UpsertKind(ctx, RelationKindDef{
		TypeName: "related_to", Mode: "asserted", Directional: true,
	}); err != nil {
		t.Fatalf("UpsertKind: %v", err)
	}

	if err := store.Assert(ctx, RelationEdge{
		SourceType: "Anything", SourceID: "a1",
		TargetType: "AnythingElse", TargetID: "b1",
		RelationKind: "related_to", EdgeClass: "asserted",
	}); err != nil {
		t.Fatalf("Assert: want success with no TypePairs declared, got %v", err)
	}
}

func TestMCPAssertRelation_TypePairsViolation(t *testing.T) {
	store := setupRelationStore(t)
	ctx := context.Background()
	pairs, _ := json.Marshal([]map[string]string{{"source_type": "Goal", "target_type": "Task"}})
	if err := store.UpsertKind(ctx, RelationKindDef{
		TypeName: "contains", Mode: "asserted", Directional: true,
		TypePairs: json.RawMessage(pairs),
	}); err != nil {
		t.Fatalf("UpsertKind: %v", err)
	}

	_, err := store.MCPAssertRelation(ctx, "Task", "t1", "Task", "t2", "contains", nil, nil, nil, nil)
	if err == nil {
		t.Fatal("MCPAssertRelation: want error for Task→Task, contains only permits Goal→Task")
	}
}

// — Non-directional canonicalization (D61, item 3) ————————————————————————————

func TestInsertEdge_CanonicalizesNonDirectional(t *testing.T) {
	store := setupRelationStore(t)
	ctx := context.Background()
	if err := store.UpsertKind(ctx, RelationKindDef{
		TypeName: "contradicts", Mode: "asserted", Directional: false,
	}); err != nil {
		t.Fatalf("UpsertKind: %v", err)
	}

	// "Decision\x00b1" < "Decision\x00a1" is false — "a1" < "b1" — so
	// asserting b1→a1 should canonicalize to a1→b1 (lexicographically
	// smaller pair always stored as source).
	if err := store.Assert(ctx, RelationEdge{
		SourceType: "Decision", SourceID: "b1",
		TargetType: "Decision", TargetID: "a1",
		RelationKind: "contradicts", EdgeClass: "asserted",
	}); err != nil {
		t.Fatalf("Assert: %v", err)
	}

	fromA, err := store.GetBySource(ctx, "Decision", "a1", "contradicts")
	if err != nil {
		t.Fatalf("GetBySource(a1): %v", err)
	}
	if len(fromA) != 1 {
		t.Fatalf("want the canonicalized edge stored with a1 as source, got %d edges from a1", len(fromA))
	}
	if fromA[0].TargetID != "b1" {
		t.Errorf("TargetID = %q, want %q", fromA[0].TargetID, "b1")
	}

	fromB, err := store.GetBySource(ctx, "Decision", "b1", "contradicts")
	if err != nil {
		t.Fatalf("GetBySource(b1): %v", err)
	}
	if len(fromB) != 0 {
		t.Errorf("want no edge with b1 as source after canonicalization, got %d", len(fromB))
	}
}

func TestInsertEdge_DirectionalKind_NotCanonicalized(t *testing.T) {
	store := setupRelationStore(t)
	ctx := context.Background()
	if err := store.UpsertKind(ctx, RelationKindDef{
		TypeName: "supersedes", Mode: "asserted", Directional: true,
	}); err != nil {
		t.Fatalf("UpsertKind: %v", err)
	}

	// b1→a1 on a directional kind must stay b1→a1, even though a1 < b1
	// lexicographically — canonicalization only applies to Directional: false.
	if err := store.Assert(ctx, RelationEdge{
		SourceType: "Decision", SourceID: "b1",
		TargetType: "Decision", TargetID: "a1",
		RelationKind: "supersedes", EdgeClass: "asserted",
	}); err != nil {
		t.Fatalf("Assert: %v", err)
	}

	fromB, err := store.GetBySource(ctx, "Decision", "b1", "supersedes")
	if err != nil {
		t.Fatalf("GetBySource(b1): %v", err)
	}
	if len(fromB) != 1 || fromB[0].TargetID != "a1" {
		t.Errorf("want b1→a1 unchanged, got %+v", fromB)
	}
}

// — Dedup on (source, target, relation_kind) (D61, item 3) ———————————————————

func TestInsertEdge_DedupSameTuple(t *testing.T) {
	store := setupRelationStore(t)
	ctx := context.Background()
	if err := store.UpsertKind(ctx, RelationKindDef{
		TypeName: "derives_from", Mode: "asserted", Directional: true,
	}); err != nil {
		t.Fatalf("UpsertKind: %v", err)
	}

	edge := RelationEdge{
		SourceType: "Task", SourceID: "t1",
		TargetType: "Goal", TargetID: "g1",
		RelationKind: "derives_from", EdgeClass: "asserted",
	}
	if err := store.Assert(ctx, edge); err != nil {
		t.Fatalf("first Assert: %v", err)
	}
	if err := store.Assert(ctx, edge); err != nil {
		t.Fatalf("second Assert (same tuple): %v", err)
	}

	got, err := store.GetBySource(ctx, "Task", "t1", "derives_from")
	if err != nil {
		t.Fatalf("GetBySource: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("want exactly 1 row after asserting the same tuple twice, got %d", len(got))
	}
}

// TestInsertEdge_Dedup_DifferentEdgeClass_NotCollapsed proves the dedup key
// includes edge_class (architect review, A297): a system-observed edge and a
// later human-asserted edge for the identical (source, target, kind) tuple
// must remain two distinct rows, not collapse onto one with the last write
// silently downgrading (or upgrading) the earlier one's trust tier.
func TestInsertEdge_Dedup_DifferentEdgeClass_NotCollapsed(t *testing.T) {
	store := setupRelationStore(t)
	ctx := context.Background()
	if err := store.UpsertKind(ctx, RelationKindDef{
		TypeName: "derives_from", Mode: "asserted", Directional: true,
	}); err != nil {
		t.Fatalf("UpsertKind: %v", err)
	}

	if _, err := store.MCPObserveRelation(ctx, "Task", "t1", "Goal", "g1", "derives_from", nil, nil, nil, nil); err != nil {
		t.Fatalf("MCPObserveRelation: %v", err)
	}
	if _, err := store.MCPAssertRelation(ctx, "Task", "t1", "Goal", "g1", "derives_from", nil, nil, nil, nil); err != nil {
		t.Fatalf("MCPAssertRelation: %v", err)
	}

	got, err := store.GetBySource(ctx, "Task", "t1", "derives_from")
	if err != nil {
		t.Fatalf("GetBySource: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 distinct rows (observed + asserted), got %d: %+v", len(got), got)
	}
	classes := map[string]bool{}
	for _, e := range got {
		classes[e.EdgeClass] = true
	}
	if !classes["observed"] || !classes["asserted"] {
		t.Errorf("want both edge_class=observed and edge_class=asserted present, got %+v", classes)
	}
}

func TestInsertEdge_ExplicitID_BypassesDedupLookup(t *testing.T) {
	store := setupRelationStore(t)
	ctx := context.Background()
	if err := store.UpsertKind(ctx, RelationKindDef{
		TypeName: "derives_from", Mode: "asserted", Directional: true,
	}); err != nil {
		t.Fatalf("UpsertKind: %v", err)
	}

	base := RelationEdge{
		SourceType: "Task", SourceID: "t1",
		TargetType: "Goal", TargetID: "g1",
		RelationKind: "derives_from", EdgeClass: "asserted",
	}
	e1 := base
	e1.ID = NewID()
	if err := store.Assert(ctx, e1); err != nil {
		t.Fatalf("first Assert (explicit id): %v", err)
	}
	e2 := base
	e2.ID = NewID()
	if err := store.Assert(ctx, e2); err != nil {
		t.Fatalf("second Assert (different explicit id): %v", err)
	}

	got, err := store.GetBySource(ctx, "Task", "t1", "derives_from")
	if err != nil {
		t.Fatalf("GetBySource: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("want 2 rows — explicit edge.ID must bypass the dedup lookup entirely, got %d", len(got))
	}
}

func TestInsertEdge_DedupLookupError(t *testing.T) {
	store := setupRelationStore(t)
	ctx := context.Background()
	if err := store.UpsertKind(ctx, RelationKindDef{
		TypeName: "derives_from", Mode: "asserted", Directional: true,
	}); err != nil {
		t.Fatalf("UpsertKind: %v", err)
	}
	wrapped := &govQueryRowFailDB{DB: store.db, failOn: "SELECT id FROM smeldr_relations WHERE"}
	failing := &RelationStore{db: wrapped, registry: store.registry}

	err := failing.Assert(ctx, RelationEdge{
		SourceType: "Task", SourceID: "t1",
		TargetType: "Goal", TargetID: "g1",
		RelationKind: "derives_from", EdgeClass: "asserted",
	})
	if err == nil {
		t.Fatal("Assert: want error propagated from a failed dedup lookup query")
	}
}
