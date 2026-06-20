package smeldr

import (
	"context"
	"testing"
)

func TestMCPUpsertRelationKind_OK(t *testing.T) {
	store := setupMCPRelations(t)

	def := RelationKindDef{
		TypeName: "tagged",
		Label:    "Tagged",
		Mode:     "asserted",
	}
	got, err := store.MCPUpsertRelationKind(context.Background(), def)
	if err != nil {
		t.Fatalf("MCPUpsertRelationKind: %v", err)
	}
	if got.TypeName != "tagged" {
		t.Errorf("want TypeName=tagged, got %q", got.TypeName)
	}
	if got.ID == "" {
		t.Error("want non-empty ID")
	}
	if got.CreatedAt.IsZero() {
		t.Error("want non-zero CreatedAt")
	}
}

func TestMCPUpsertRelationKind_InvalidMode(t *testing.T) {
	store := setupMCPRelations(t)

	_, err := store.MCPUpsertRelationKind(context.Background(), RelationKindDef{
		TypeName: "bad",
		Label:    "Bad",
		Mode:     "unknown",
	})
	if err == nil {
		t.Error("want error for invalid mode, got nil")
	}
}

func TestMCPUpsertRelationKind_Upsert(t *testing.T) {
	store := setupMCPRelations(t)

	ctx := context.Background()
	first := RelationKindDef{TypeName: "tagged", Label: "Tagged", Mode: "asserted"}
	if _, err := store.MCPUpsertRelationKind(ctx, first); err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	second := RelationKindDef{TypeName: "tagged", Label: "Tagged v2", Mode: "inferable"}
	got, err := store.MCPUpsertRelationKind(ctx, second)
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if got.Label != "Tagged v2" {
		t.Errorf("want Label=Tagged v2 after update, got %q", got.Label)
	}
	if got.Mode != "inferable" {
		t.Errorf("want Mode=inferable after update, got %q", got.Mode)
	}

	// Registry must not contain duplicates.
	kinds := store.MCPListRelationKinds()
	if len(kinds) != 1 {
		t.Errorf("want exactly 1 kind after two upserts of same type_name, got %d", len(kinds))
	}
}

func TestMCPListRelationKinds_Empty(t *testing.T) {
	store := setupMCPRelations(t)

	kinds := store.MCPListRelationKinds()
	if kinds == nil {
		t.Error("want non-nil empty slice, got nil")
	}
	if len(kinds) != 0 {
		t.Errorf("want 0 kinds, got %d", len(kinds))
	}
}

func TestMCPListRelationKinds_ReturnsSorted(t *testing.T) {
	store := setupMCPRelations(t)

	ctx := context.Background()
	for _, name := range []string{"zzz_kind", "aaa_kind", "mmm_kind"} {
		if _, err := store.MCPUpsertRelationKind(ctx, RelationKindDef{
			TypeName: name,
			Label:    name,
			Mode:     "asserted",
		}); err != nil {
			t.Fatalf("MCPUpsertRelationKind %q: %v", name, err)
		}
	}

	kinds := store.MCPListRelationKinds()
	if len(kinds) != 3 {
		t.Fatalf("want 3 kinds, got %d", len(kinds))
	}
	for i, want := range []string{"aaa_kind", "mmm_kind", "zzz_kind"} {
		if kinds[i].TypeName != want {
			t.Errorf("kinds[%d]: want %q, got %q", i, want, kinds[i].TypeName)
		}
	}
}
