package smeldr

import (
	"context"
	"testing"
)

func setupRelationStore(t *testing.T) *RelationStore {
	t.Helper()
	db := newSQLiteDB(t)
	if err := CreateRelationTables(db); err != nil {
		t.Fatalf("CreateRelationTables: %v", err)
	}
	store, err := NewRelationStore(db)
	if err != nil {
		t.Fatalf("NewRelationStore: %v", err)
	}
	return store
}

func TestCreateRelationTables_Idempotent(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateRelationTables(db); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if err := CreateRelationTables(db); err != nil {
		t.Fatalf("second call: %v", err)
	}
}

func TestUpsertKind_RoundTrip(t *testing.T) {
	store := setupRelationStore(t)
	ctx := context.Background()

	def := RelationKindDef{
		TypeName: "references",
		Label:    "References",
		Mode:     "asserted",
	}
	if err := store.UpsertKind(ctx, def); err != nil {
		t.Fatalf("UpsertKind: %v", err)
	}

	got, ok := store.GetKind("references")
	if !ok {
		t.Fatal("GetKind: not found after upsert")
	}
	if got.TypeName != "references" {
		t.Errorf("TypeName = %q, want %q", got.TypeName, "references")
	}
	if got.Mode != "asserted" {
		t.Errorf("Mode = %q, want asserted", got.Mode)
	}

	kinds := store.ListKinds()
	if len(kinds) != 1 {
		t.Fatalf("ListKinds: got %d, want 1", len(kinds))
	}
	if kinds[0].TypeName != "references" {
		t.Errorf("ListKinds[0].TypeName = %q, want references", kinds[0].TypeName)
	}
}

func TestUpsertKind_Update(t *testing.T) {
	store := setupRelationStore(t)
	ctx := context.Background()

	def := RelationKindDef{TypeName: "cites", Label: "Cites", Mode: "asserted"}
	if err := store.UpsertKind(ctx, def); err != nil {
		t.Fatalf("first UpsertKind: %v", err)
	}

	def.Label = "Cites (updated)"
	if err := store.UpsertKind(ctx, def); err != nil {
		t.Fatalf("second UpsertKind: %v", err)
	}

	got, _ := store.GetKind("cites")
	if got.Label != "Cites (updated)" {
		t.Errorf("Label = %q, want %q", got.Label, "Cites (updated)")
	}
}

func TestValidateRelationKindDef_InvalidMode(t *testing.T) {
	err := ValidateRelationKindDef(RelationKindDef{TypeName: "x", Mode: "bad"})
	if err == nil {
		t.Fatal("expected error for invalid mode, got nil")
	}
}

func TestValidateRelationKindDef_EmptyTypeName(t *testing.T) {
	err := ValidateRelationKindDef(RelationKindDef{TypeName: "", Mode: "asserted"})
	if err == nil {
		t.Fatal("expected error for empty type_name, got nil")
	}
}

func TestAssert_GetBySource(t *testing.T) {
	store := setupRelationStore(t)
	ctx := context.Background()

	if err := store.UpsertKind(ctx, RelationKindDef{
		TypeName: "related_to", Mode: "asserted",
	}); err != nil {
		t.Fatalf("UpsertKind: %v", err)
	}

	edge := RelationEdge{
		SourceType:   "post",
		SourceID:     "p1",
		TargetType:   "post",
		TargetID:     "p2",
		RelationKind: "related_to",
		EdgeClass:    "asserted",
	}
	if err := store.Assert(ctx, edge); err != nil {
		t.Fatalf("Assert: %v", err)
	}

	edges, err := store.GetBySource(ctx, "post", "p1", "related_to")
	if err != nil {
		t.Fatalf("GetBySource: %v", err)
	}
	if len(edges) != 1 {
		t.Fatalf("GetBySource: got %d edges, want 1", len(edges))
	}
	if edges[0].TargetID != "p2" {
		t.Errorf("TargetID = %q, want p2", edges[0].TargetID)
	}
}

func TestGetBySource_AllKinds(t *testing.T) {
	store := setupRelationStore(t)
	ctx := context.Background()

	for _, kind := range []string{"related_to", "cites"} {
		if err := store.UpsertKind(ctx, RelationKindDef{TypeName: kind, Mode: "asserted"}); err != nil {
			t.Fatalf("UpsertKind %s: %v", kind, err)
		}
	}

	for _, kind := range []string{"related_to", "cites"} {
		edge := RelationEdge{
			SourceType: "post", SourceID: "p1",
			TargetType: "post", TargetID: "p2",
			RelationKind: kind, EdgeClass: "asserted",
		}
		if err := store.Assert(ctx, edge); err != nil {
			t.Fatalf("Assert %s: %v", kind, err)
		}
	}

	edges, err := store.GetBySource(ctx, "post", "p1", "")
	if err != nil {
		t.Fatalf("GetBySource (all kinds): %v", err)
	}
	if len(edges) != 2 {
		t.Errorf("GetBySource all kinds: got %d, want 2", len(edges))
	}
}

func TestGetByTarget(t *testing.T) {
	store := setupRelationStore(t)
	ctx := context.Background()

	if err := store.UpsertKind(ctx, RelationKindDef{TypeName: "related_to", Mode: "asserted"}); err != nil {
		t.Fatalf("UpsertKind: %v", err)
	}

	edge := RelationEdge{
		SourceType: "post", SourceID: "p1",
		TargetType: "post", TargetID: "p2",
		RelationKind: "related_to", EdgeClass: "asserted",
	}
	if err := store.Assert(ctx, edge); err != nil {
		t.Fatalf("Assert: %v", err)
	}

	edges, err := store.GetByTarget(ctx, "post", "p2", "related_to")
	if err != nil {
		t.Fatalf("GetByTarget: %v", err)
	}
	if len(edges) != 1 {
		t.Fatalf("GetByTarget: got %d edges, want 1", len(edges))
	}
	if edges[0].SourceID != "p1" {
		t.Errorf("SourceID = %q, want p1", edges[0].SourceID)
	}
}

func TestDelete_RemovesEdge(t *testing.T) {
	store := setupRelationStore(t)
	ctx := context.Background()

	if err := store.UpsertKind(ctx, RelationKindDef{TypeName: "related_to", Mode: "asserted"}); err != nil {
		t.Fatalf("UpsertKind: %v", err)
	}

	edge := RelationEdge{
		ID:         "edge-del-1",
		SourceType: "post", SourceID: "p1",
		TargetType: "post", TargetID: "p2",
		RelationKind: "related_to", EdgeClass: "asserted",
	}
	if err := store.Assert(ctx, edge); err != nil {
		t.Fatalf("Assert: %v", err)
	}

	if err := store.Delete(ctx, "edge-del-1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	edges, err := store.GetBySource(ctx, "post", "p1", "related_to")
	if err != nil {
		t.Fatalf("GetBySource after delete: %v", err)
	}
	if len(edges) != 0 {
		t.Errorf("expected 0 edges after delete, got %d", len(edges))
	}
}

func TestAssert_UnknownKind(t *testing.T) {
	store := setupRelationStore(t)
	ctx := context.Background()

	edge := RelationEdge{
		SourceType: "post", SourceID: "p1",
		TargetType: "post", TargetID: "p2",
		RelationKind: "nonexistent", EdgeClass: "asserted",
	}
	err := store.Assert(ctx, edge)
	if err == nil {
		t.Fatal("expected error for unknown relation kind, got nil")
	}
}

func TestAssert_WrongEdgeClass(t *testing.T) {
	store := setupRelationStore(t)
	ctx := context.Background()

	if err := store.UpsertKind(ctx, RelationKindDef{TypeName: "related_to", Mode: "inferable"}); err != nil {
		t.Fatalf("UpsertKind: %v", err)
	}

	edge := RelationEdge{
		SourceType: "post", SourceID: "p1",
		TargetType: "post", TargetID: "p2",
		RelationKind: "related_to", EdgeClass: "inferred",
	}
	err := store.Assert(ctx, edge)
	if err == nil {
		t.Fatal("expected error for wrong edge_class, got nil")
	}
}

func TestApp_Relations_Wiring(t *testing.T) {
	app := newTestApp()
	if app.RelationStore() != nil {
		t.Fatal("RelationStore should be nil before wiring")
	}

	db := newSQLiteDB(t)
	if err := CreateRelationTables(db); err != nil {
		t.Fatalf("CreateRelationTables: %v", err)
	}
	store, err := NewRelationStore(db)
	if err != nil {
		t.Fatalf("NewRelationStore: %v", err)
	}

	app.Relations(store)
	if app.RelationStore() != store {
		t.Fatal("RelationStore should return wired store")
	}
}
