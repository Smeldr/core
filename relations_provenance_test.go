package smeldr

import (
	"context"
	"testing"
)

// TestInsertEdge_RecordsProvenance_WithActor confirms the type-assertion
// approach (design doc §5.2) recovers the actor when ctx is a real
// smeldr.Context — the shape every MCP tool handler call and the
// applyConflictPolicy internal caller actually use.
func TestInsertEdge_RecordsProvenance_WithActor(t *testing.T) {
	store := setupRelationStore(t)
	upsertTestKind(t, store, "related_to", "post", "post")

	prov := &fakeProvenanceStore{}
	store.setProvenanceStore(prov)

	ctx := NewTestContext(User{ID: "u1", Roles: []Role{Author}})
	if err := store.Assert(ctx, RelationEdge{
		SourceType: "post", SourceID: "p1",
		TargetType: "post", TargetID: "p2",
		RelationKind: "related_to", EdgeClass: "asserted",
	}); err != nil {
		t.Fatalf("Assert: %v", err)
	}

	if len(prov.appended) != 1 {
		t.Fatalf("got %d provenance records, want 1", len(prov.appended))
	}
	r := prov.appended[0]
	if r.SubjectType != "RelationEdge" {
		t.Errorf("SubjectType = %q, want RelationEdge", r.SubjectType)
	}
	if r.Verb != "assert" {
		t.Errorf("Verb = %q, want assert", r.Verb)
	}
	if r.ActorID != "u1" || r.ActorKind != "human" {
		t.Errorf("actor: got %s/%s, want u1/human", r.ActorID, r.ActorKind)
	}
	if r.SubjectID == "" {
		t.Error("SubjectID (edge ID) is empty")
	}
}

// TestInsertEdge_RecordsProvenance_NoActor confirms the type assertion fails
// gracefully (no panic, empty actor fields, edge insert still succeeds) when
// ctx is a bare context.Context — the case for e.g. applyConflictPolicy's
// supersede path when reached without a real request-scoped Context, or any
// other system-initiated caller.
func TestInsertEdge_RecordsProvenance_NoActor(t *testing.T) {
	store := setupRelationStore(t)
	upsertTestKind(t, store, "related_to", "post", "post")

	prov := &fakeProvenanceStore{}
	store.setProvenanceStore(prov)

	if err := store.Assert(context.Background(), RelationEdge{
		SourceType: "post", SourceID: "p1",
		TargetType: "post", TargetID: "p2",
		RelationKind: "related_to", EdgeClass: "asserted",
	}); err != nil {
		t.Fatalf("Assert: %v", err)
	}

	if len(prov.appended) != 1 {
		t.Fatalf("got %d provenance records, want 1", len(prov.appended))
	}
	r := prov.appended[0]
	if r.ActorID != "" || r.ActorKind != "" {
		t.Errorf("actor: got %s/%s, want empty/empty (no smeldr.Context)", r.ActorID, r.ActorKind)
	}
}

// TestInsertEdge_RecordsProvenance_CreatedByJob confirms Decision 2's
// absorption: an edge with CreatedByJob set records ActorKind="job" with the
// job identifier as ActorID, taking priority over any ctx-derived actor.
func TestInsertEdge_RecordsProvenance_CreatedByJob(t *testing.T) {
	store := setupRelationStore(t)
	upsertTestKind(t, store, "supersedes", "post", "post")

	prov := &fakeProvenanceStore{}
	store.setProvenanceStore(prov)

	jobID := "sweep-job-1"
	ctx := NewTestContext(User{ID: "u1"}) // a human actor also present in ctx
	if err := store.Assert(ctx, RelationEdge{
		SourceType: "post", SourceID: "p1",
		TargetType: "post", TargetID: "p2",
		RelationKind: "supersedes", EdgeClass: "asserted",
		CreatedByJob: &jobID,
	}); err != nil {
		t.Fatalf("Assert: %v", err)
	}

	if len(prov.appended) != 1 {
		t.Fatalf("got %d provenance records, want 1", len(prov.appended))
	}
	r := prov.appended[0]
	if r.ActorKind != "job" || r.ActorID != jobID {
		t.Errorf("actor: got %s/%s, want job/%s (CreatedByJob takes priority)", r.ActorKind, r.ActorID, jobID)
	}
}

// TestInsertEdge_NilProvenanceStore_NoOp confirms relation assertion works
// unchanged when App.Provenance was never wired — the normal, fully-supported
// default state.
func TestInsertEdge_NilProvenanceStore_NoOp(t *testing.T) {
	store := setupRelationStore(t)
	upsertTestKind(t, store, "related_to", "post", "post")
	// store.provenanceStore is nil by default — no setProvenanceStore call.

	if err := store.Assert(context.Background(), RelationEdge{
		SourceType: "post", SourceID: "p1",
		TargetType: "post", TargetID: "p2",
		RelationKind: "related_to", EdgeClass: "asserted",
	}); err != nil {
		t.Fatalf("Assert: %v", err)
	}
}

func TestSetProvenanceStore(t *testing.T) {
	store := setupRelationStore(t)
	prov := &fakeProvenanceStore{}
	store.setProvenanceStore(prov)
	if store.provenanceStore != prov {
		t.Error("setProvenanceStore did not set the field")
	}
}
