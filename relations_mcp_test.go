package smeldr

import (
	"context"
	"encoding/json"
	"testing"
)

func setupMCPRelations(t *testing.T) *RelationStore {
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

func TestMCPAssertRelation_OK(t *testing.T) {
	store := setupMCPRelations(t)
	upsertTestKind(t, store, "tagged", "article", "tag")

	ctx := context.Background()
	edge, err := store.MCPAssertRelation(ctx, "article", "art-1", "tag", "tag-1", "tagged", nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("MCPAssertRelation: %v", err)
	}
	if edge.ID == "" {
		t.Error("want non-empty ID")
	}
	if edge.EdgeClass != "asserted" {
		t.Errorf("want EdgeClass=asserted, got %q", edge.EdgeClass)
	}
	if edge.SourceID != "art-1" || edge.TargetID != "tag-1" {
		t.Errorf("unexpected edge fields: %+v", edge)
	}
}

func TestMCPAssertRelation_UnknownKind(t *testing.T) {
	store := setupMCPRelations(t)

	_, err := store.MCPAssertRelation(context.Background(), "article", "art-1", "tag", "tag-1", "nonexistent", nil, nil, nil, nil)
	if err != ErrNotFound {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestMCPAssertRelation_WithConfidenceAndAttributes(t *testing.T) {
	store := setupMCPRelations(t)
	upsertTestKind(t, store, "tagged", "article", "tag")

	conf := 0.9
	attrs := json.RawMessage(`{"weight":1}`)
	edge, err := store.MCPAssertRelation(context.Background(), "article", "art-1", "tag", "tag-1", "tagged", &conf, nil, nil, attrs)
	if err != nil {
		t.Fatalf("MCPAssertRelation: %v", err)
	}
	if edge.Confidence == nil || *edge.Confidence != 0.9 {
		t.Errorf("want Confidence=0.9, got %v", edge.Confidence)
	}
}

func TestMCPProposeRelation_StoresInferred(t *testing.T) {
	store := setupMCPRelations(t)
	upsertTestKind(t, store, "tagged", "article", "tag")

	ctx := context.Background()
	edge, err := store.MCPProposeRelation(ctx, "article", "art-1", "tag", "tag-1", "tagged", nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("MCPProposeRelation: %v", err)
	}
	if edge.EdgeClass != "inferred" {
		t.Errorf("want EdgeClass=inferred, got %q", edge.EdgeClass)
	}

	// Verify stored in DB with inferred class.
	edges, err := store.GetBySource(ctx, "article", "art-1", "tagged")
	if err != nil {
		t.Fatalf("GetBySource: %v", err)
	}
	if len(edges) != 1 || edges[0].EdgeClass != "inferred" {
		t.Errorf("expected 1 inferred edge in DB, got: %+v", edges)
	}
}

func TestMCPGetRelations_Source(t *testing.T) {
	store := setupMCPRelations(t)
	upsertTestKind(t, store, "tagged", "article", "tag")

	ctx := context.Background()
	if err := store.Assert(ctx, RelationEdge{
		SourceType: "article", SourceID: "art-1",
		TargetType: "tag", TargetID: "tag-1",
		RelationKind: "tagged", EdgeClass: "asserted",
	}); err != nil {
		t.Fatalf("Assert: %v", err)
	}

	edges, err := store.MCPGetRelations(ctx, "article", "art-1", "source", "")
	if err != nil {
		t.Fatalf("MCPGetRelations source: %v", err)
	}
	if len(edges) != 1 || edges[0].TargetID != "tag-1" {
		t.Errorf("unexpected edges: %+v", edges)
	}
}

func TestMCPGetRelations_Target(t *testing.T) {
	store := setupMCPRelations(t)
	upsertTestKind(t, store, "tagged", "article", "tag")

	ctx := context.Background()
	if err := store.Assert(ctx, RelationEdge{
		SourceType: "article", SourceID: "art-1",
		TargetType: "tag", TargetID: "tag-1",
		RelationKind: "tagged", EdgeClass: "asserted",
	}); err != nil {
		t.Fatalf("Assert: %v", err)
	}

	edges, err := store.MCPGetRelations(ctx, "tag", "tag-1", "target", "")
	if err != nil {
		t.Fatalf("MCPGetRelations target: %v", err)
	}
	if len(edges) != 1 || edges[0].SourceID != "art-1" {
		t.Errorf("unexpected edges: %+v", edges)
	}
}

func TestMCPGetRelations_Both(t *testing.T) {
	store := setupMCPRelations(t)
	upsertTestKind(t, store, "tagged", "article", "tag")
	upsertTestKind(t, store, "refs", "tag", "article")

	ctx := context.Background()
	// art-1 --tagged--> tag-1
	if err := store.Assert(ctx, RelationEdge{
		SourceType: "article", SourceID: "art-1",
		TargetType: "tag", TargetID: "tag-1",
		RelationKind: "tagged", EdgeClass: "asserted",
	}); err != nil {
		t.Fatalf("Assert tagged: %v", err)
	}
	// tag-2 --refs--> art-1
	if err := store.Assert(ctx, RelationEdge{
		SourceType: "tag", SourceID: "tag-2",
		TargetType: "article", TargetID: "art-1",
		RelationKind: "refs", EdgeClass: "asserted",
	}); err != nil {
		t.Fatalf("Assert refs: %v", err)
	}

	edges, err := store.MCPGetRelations(ctx, "article", "art-1", "both", "")
	if err != nil {
		t.Fatalf("MCPGetRelations both: %v", err)
	}
	if len(edges) != 2 {
		t.Errorf("want 2 edges (1 source + 1 target), got %d: %+v", len(edges), edges)
	}
}

func TestMCPGetRelations_KindFilter(t *testing.T) {
	store := setupMCPRelations(t)
	upsertTestKind(t, store, "tagged", "article", "tag")
	upsertTestKind(t, store, "authored_by", "article", "author")

	ctx := context.Background()
	if err := store.Assert(ctx, RelationEdge{
		SourceType: "article", SourceID: "art-1",
		TargetType: "tag", TargetID: "tag-1",
		RelationKind: "tagged", EdgeClass: "asserted",
	}); err != nil {
		t.Fatalf("Assert tagged: %v", err)
	}
	if err := store.Assert(ctx, RelationEdge{
		SourceType: "article", SourceID: "art-1",
		TargetType: "author", TargetID: "author-1",
		RelationKind: "authored_by", EdgeClass: "asserted",
	}); err != nil {
		t.Fatalf("Assert authored_by: %v", err)
	}

	edges, err := store.MCPGetRelations(ctx, "article", "art-1", "source", "tagged")
	if err != nil {
		t.Fatalf("MCPGetRelations kind filter: %v", err)
	}
	if len(edges) != 1 || edges[0].RelationKind != "tagged" {
		t.Errorf("want 1 edge with kind=tagged, got: %+v", edges)
	}
}

func TestMCPPreviewImpact_ReturnsDependents(t *testing.T) {
	store := setupMCPRelations(t)
	upsertTestKind(t, store, "depends_on", "article", "governance")

	ctx := context.Background()
	if err := store.Assert(ctx, RelationEdge{
		SourceType: "article", SourceID: "art-1",
		TargetType: "governance", TargetID: "gov-1",
		RelationKind: "depends_on", EdgeClass: "asserted",
	}); err != nil {
		t.Fatalf("Assert: %v", err)
	}
	if err := store.Assert(ctx, RelationEdge{
		SourceType: "article", SourceID: "art-2",
		TargetType: "governance", TargetID: "gov-1",
		RelationKind: "depends_on", EdgeClass: "asserted",
	}); err != nil {
		t.Fatalf("Assert art-2: %v", err)
	}

	edges, err := store.MCPPreviewImpact(ctx, "governance", "gov-1")
	if err != nil {
		t.Fatalf("MCPPreviewImpact: %v", err)
	}
	if len(edges) != 2 {
		t.Errorf("want 2 dependents, got %d: %+v", len(edges), edges)
	}
}

func TestMCPPreviewImpact_NoDependents(t *testing.T) {
	store := setupMCPRelations(t)

	edges, err := store.MCPPreviewImpact(context.Background(), "governance", "gov-orphan")
	if err != nil {
		t.Fatalf("MCPPreviewImpact: %v", err)
	}
	if len(edges) != 0 {
		t.Errorf("want empty, got %d edges", len(edges))
	}
}

func TestMCPGetRelations_InvalidDirection(t *testing.T) {
	store := setupMCPRelations(t)

	_, err := store.MCPGetRelations(context.Background(), "article", "art-1", "sideways", "")
	if err == nil {
		t.Error("want error for invalid direction, got nil")
	}
}
