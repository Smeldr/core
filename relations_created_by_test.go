package smeldr

// Tests for RelationEdge.CreatedBy (01a0e3bc-8): the human/agent counterpart
// to CreatedByJob, filled from ctx's own authenticated user across all three
// relation-write MCP tools, and via the EnsureRelationCreatedByColumn
// migration for pre-existing installs.

import (
	"context"
	"testing"
)

func TestInsertEdge_Assert_SetsCreatedByFromActor(t *testing.T) {
	store := setupRelationStore(t)
	ctx := context.Background()
	if err := store.UpsertKind(ctx, RelationKindDef{
		TypeName: "relates_to", Mode: "asserted", Directional: false,
	}); err != nil {
		t.Fatalf("UpsertKind: %v", err)
	}

	actorCtx := NewTestContext(User{ID: "editor-9"})
	edge, err := store.MCPAssertRelation(actorCtx, "Decision", "d1", "Decision", "d2", "relates_to", nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("MCPAssertRelation: %v", err)
	}
	if edge.CreatedBy == nil || *edge.CreatedBy != "editor-9" {
		t.Errorf("CreatedBy = %v, want %q", edge.CreatedBy, "editor-9")
	}
}

func TestInsertEdge_Propose_SetsCreatedByFromActor(t *testing.T) {
	store := setupRelationStore(t)
	ctx := context.Background()
	if err := store.UpsertKind(ctx, RelationKindDef{
		TypeName: "relates_to", Mode: "asserted", Directional: false,
	}); err != nil {
		t.Fatalf("UpsertKind: %v", err)
	}

	actorCtx := NewTestContext(User{ID: "agent-3"})
	edge, err := store.MCPProposeRelation(actorCtx, "Decision", "d1", "Decision", "d2", "relates_to", nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("MCPProposeRelation: %v", err)
	}
	if edge.CreatedBy == nil || *edge.CreatedBy != "agent-3" {
		t.Errorf("CreatedBy = %v, want %q", edge.CreatedBy, "agent-3")
	}
}

func TestInsertEdge_Observe_SetsCreatedByFromActor(t *testing.T) {
	store := setupRelationStore(t)
	ctx := context.Background()
	if err := store.UpsertKind(ctx, RelationKindDef{
		TypeName: "relates_to", Mode: "asserted", Directional: false,
	}); err != nil {
		t.Fatalf("UpsertKind: %v", err)
	}

	actorCtx := NewTestContext(User{ID: "webhook-actor"})
	edge, err := store.MCPObserveRelation(actorCtx, "Decision", "d1", "Decision", "d2", "relates_to", nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("MCPObserveRelation: %v", err)
	}
	if edge.CreatedBy == nil || *edge.CreatedBy != "webhook-actor" {
		t.Errorf("CreatedBy = %v, want %q", edge.CreatedBy, "webhook-actor")
	}
}

// TestInsertEdge_PlainContext_CreatedByNil proves a system-initiated call
// (a plain context.Context, no smeldr.Context wrapping it) leaves CreatedBy
// nil — same "no caller identity available" convention LastActor already
// uses on the transition path.
func TestInsertEdge_PlainContext_CreatedByNil(t *testing.T) {
	store := setupRelationStore(t)
	ctx := context.Background()
	if err := store.UpsertKind(ctx, RelationKindDef{
		TypeName: "relates_to", Mode: "asserted", Directional: false,
	}); err != nil {
		t.Fatalf("UpsertKind: %v", err)
	}

	edge, err := store.MCPAssertRelation(ctx, "Decision", "d1", "Decision", "d2", "relates_to", nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("MCPAssertRelation: %v", err)
	}
	if edge.CreatedBy != nil {
		t.Errorf("CreatedBy = %v, want nil", *edge.CreatedBy)
	}
}

// TestInsertEdge_Reassert_CreatedByOverwrittenByMostRecentActor proves a
// re-assert of the same (source, target, kind, edge_class) tuple by a
// different actor updates CreatedBy — matching CreatedByJob's own existing
// conflict behavior on this table (architect-approved, 01a0e3bc-8 plan
// review): the most recent asserter is who a credential surface should
// point at.
func TestInsertEdge_Reassert_CreatedByOverwrittenByMostRecentActor(t *testing.T) {
	store := setupRelationStore(t)
	ctx := context.Background()
	if err := store.UpsertKind(ctx, RelationKindDef{
		TypeName: "relates_to", Mode: "asserted", Directional: false,
	}); err != nil {
		t.Fatalf("UpsertKind: %v", err)
	}

	firstCtx := NewTestContext(User{ID: "first-actor"})
	if _, err := store.MCPAssertRelation(firstCtx, "Decision", "d1", "Decision", "d2", "relates_to", nil, nil, nil, nil); err != nil {
		t.Fatalf("first MCPAssertRelation: %v", err)
	}

	secondCtx := NewTestContext(User{ID: "second-actor"})
	edge, err := store.MCPAssertRelation(secondCtx, "Decision", "d1", "Decision", "d2", "relates_to", nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("second MCPAssertRelation: %v", err)
	}
	if edge.CreatedBy == nil || *edge.CreatedBy != "second-actor" {
		t.Errorf("CreatedBy = %v, want %q (overwritten by the re-assert)", edge.CreatedBy, "second-actor")
	}
}

// — EnsureRelationCreatedByColumn ————————————————————————————————————————————

func TestEnsureRelationCreatedByColumn_AddsColumn(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()
	// Simulate a pre-existing install: create the table without created_by,
	// mirroring the shape smeldr_relations had before this column existed.
	if _, err := db.ExecContext(ctx, `
CREATE TABLE smeldr_relations (
    id              TEXT NOT NULL PRIMARY KEY,
    source_type     TEXT NOT NULL,
    source_id       TEXT NOT NULL,
    target_type     TEXT NOT NULL,
    target_id       TEXT NOT NULL,
    relation_kind   TEXT NOT NULL,
    edge_class      TEXT NOT NULL,
    confidence      REAL,
    valid_at        TIMESTAMPTZ,
    invalid_at      TIMESTAMPTZ,
    created_by_job  TEXT,
    attributes      TEXT NOT NULL DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL,
    updated_at      TIMESTAMPTZ NOT NULL,
    last_confirmed_at TIMESTAMPTZ
)`); err != nil {
		t.Fatalf("create pre-existing table: %v", err)
	}

	if err := EnsureRelationCreatedByColumn(ctx, db); err != nil {
		t.Fatalf("EnsureRelationCreatedByColumn: %v", err)
	}

	if _, err := db.ExecContext(ctx,
		`INSERT INTO smeldr_relations
			(id, source_type, source_id, target_type, target_id, relation_kind, edge_class,
			 created_by, created_at, updated_at)
		VALUES ('r1', 'A', '1', 'B', '2', 'k', 'asserted', 'actor-1', '2026-01-01', '2026-01-01')`,
	); err != nil {
		t.Errorf("created_by column should exist after migration, got: %v", err)
	}
}

func TestEnsureRelationCreatedByColumn_Idempotent(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()
	if err := CreateRelationTables(db); err != nil {
		t.Fatalf("CreateRelationTables: %v", err)
	}
	if err := EnsureRelationCreatedByColumn(ctx, db); err != nil {
		t.Errorf("first call: %v", err)
	}
	if err := EnsureRelationCreatedByColumn(ctx, db); err != nil {
		t.Errorf("second call: %v", err)
	}
}
