package smeldr

import (
	"context"
	"errors"
	"testing"
)

// childIDsInOrder returns the child IDs of the edges in slice order.
func childIDsInOrder(edges []ContentEdge) []string {
	ids := make([]string, len(edges))
	for i, e := range edges {
		ids[i] = e.ChildID
	}
	return ids
}

func newEdgeStore(t *testing.T) (*ContentEdgeStore, context.Context) {
	t.Helper()
	db := newSQLiteDB(t)
	if err := CreateBlockTables(db); err != nil {
		t.Fatalf("CreateBlockTables: %v", err)
	}
	return NewContentEdgeStore(db), context.Background()
}

func TestContentEdge_AddChild_AppendsInOrder(t *testing.T) {
	s, ctx := newEdgeStore(t)
	parent := NewID()

	var added []ContentEdge
	for _, child := range []string{"c1", "c2", "c3"} {
		e, err := s.AddChild(ctx, ContentEdge{
			ParentID: parent, ParentType: "page",
			ChildID: child, ChildType: "content_block",
		})
		if err != nil {
			t.Fatalf("AddChild(%s): %v", child, err)
		}
		added = append(added, e)
	}

	// SortOrder must be 0,1,2 in add order.
	for i, e := range added {
		if e.SortOrder != i {
			t.Errorf("child %s SortOrder = %d, want %d", e.ChildID, e.SortOrder, i)
		}
		if e.ID == "" {
			t.Errorf("child %s got empty ID", e.ChildID)
		}
		if e.EdgeRole != "section" {
			t.Errorf("child %s EdgeRole = %q, want section (default)", e.ChildID, e.EdgeRole)
		}
	}

	got, err := s.Children(ctx, parent)
	if err != nil {
		t.Fatalf("Children: %v", err)
	}
	if want := []string{"c1", "c2", "c3"}; !equalStrings(childIDsInOrder(got), want) {
		t.Errorf("Children order = %v, want %v", childIDsInOrder(got), want)
	}
}

func TestContentEdge_Children_EmptyParent(t *testing.T) {
	s, ctx := newEdgeStore(t)
	got, err := s.Children(ctx, NewID())
	if err != nil {
		t.Fatalf("Children: %v", err)
	}
	if got == nil {
		t.Error("Children returned nil, want empty non-nil slice")
	}
	if len(got) != 0 {
		t.Errorf("Children len = %d, want 0", len(got))
	}
}

func TestContentEdge_EdgeRoleAndShared(t *testing.T) {
	s, ctx := newEdgeStore(t)
	parent := NewID()

	e, err := s.AddChild(ctx, ContentEdge{
		ParentID: parent, ParentType: "collection",
		ChildID: "item1", ChildType: "image",
		EdgeRole: "item", IsShared: true,
	})
	if err != nil {
		t.Fatalf("AddChild: %v", err)
	}
	if e.EdgeRole != "item" {
		t.Errorf("EdgeRole = %q, want item", e.EdgeRole)
	}

	got, err := s.Children(ctx, parent)
	if err != nil {
		t.Fatalf("Children: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Children len = %d, want 1", len(got))
	}
	// is_shared INTEGER must round-trip back to bool true.
	if !got[0].IsShared {
		t.Error("IsShared = false, want true (int↔bool scan)")
	}
	if got[0].EdgeRole != "item" {
		t.Errorf("EdgeRole = %q, want item", got[0].EdgeRole)
	}
}

func TestContentEdge_ChildrenOf_Batch(t *testing.T) {
	s, ctx := newEdgeStore(t)
	pa, pb := NewID(), NewID()

	for _, c := range []string{"a1", "a2"} {
		if _, err := s.AddChild(ctx, ContentEdge{ParentID: pa, ParentType: "page", ChildID: c, ChildType: "block"}); err != nil {
			t.Fatalf("AddChild: %v", err)
		}
	}
	if _, err := s.AddChild(ctx, ContentEdge{ParentID: pb, ParentType: "page", ChildID: "b1", ChildType: "block"}); err != nil {
		t.Fatalf("AddChild: %v", err)
	}

	got, err := s.ChildrenOf(ctx, []string{pa, pb})
	if err != nil {
		t.Fatalf("ChildrenOf: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("ChildrenOf len = %d, want 3", len(got))
	}

	// Each parent's children stay ordered by sort_order within the batch.
	byParent := map[string][]string{}
	for _, e := range got {
		byParent[e.ParentID] = append(byParent[e.ParentID], e.ChildID)
	}
	if want := []string{"a1", "a2"}; !equalStrings(byParent[pa], want) {
		t.Errorf("parent A children = %v, want %v", byParent[pa], want)
	}
	if want := []string{"b1"}; !equalStrings(byParent[pb], want) {
		t.Errorf("parent B children = %v, want %v", byParent[pb], want)
	}
}

func TestContentEdge_ChildrenOf_Empty(t *testing.T) {
	s, ctx := newEdgeStore(t)
	got, err := s.ChildrenOf(ctx, nil)
	if err != nil {
		t.Fatalf("ChildrenOf(nil): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("ChildrenOf(nil) len = %d, want 0", len(got))
	}
}

func TestContentEdge_RemoveChild(t *testing.T) {
	s, ctx := newEdgeStore(t)
	parent := NewID()
	for _, c := range []string{"c1", "c2"} {
		if _, err := s.AddChild(ctx, ContentEdge{ParentID: parent, ParentType: "page", ChildID: c, ChildType: "block"}); err != nil {
			t.Fatalf("AddChild: %v", err)
		}
	}

	if err := s.RemoveChild(ctx, parent, "c1"); err != nil {
		t.Fatalf("RemoveChild: %v", err)
	}
	got, err := s.Children(ctx, parent)
	if err != nil {
		t.Fatalf("Children: %v", err)
	}
	if want := []string{"c2"}; !equalStrings(childIDsInOrder(got), want) {
		t.Errorf("after remove, children = %v, want %v", childIDsInOrder(got), want)
	}

	// Removing a non-existent edge returns ErrNotFound.
	if err := s.RemoveChild(ctx, parent, "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("RemoveChild(missing): err = %v, want ErrNotFound", err)
	}
}

func TestContentEdge_Reorder(t *testing.T) {
	s, ctx := newEdgeStore(t)
	parent := NewID()
	for _, c := range []string{"c1", "c2", "c3"} {
		if _, err := s.AddChild(ctx, ContentEdge{ParentID: parent, ParentType: "page", ChildID: c, ChildType: "block"}); err != nil {
			t.Fatalf("AddChild: %v", err)
		}
	}

	// Reverse the order.
	if err := s.Reorder(ctx, parent, []string{"c3", "c2", "c1"}); err != nil {
		t.Fatalf("Reorder: %v", err)
	}
	got, err := s.Children(ctx, parent)
	if err != nil {
		t.Fatalf("Children: %v", err)
	}
	if want := []string{"c3", "c2", "c1"}; !equalStrings(childIDsInOrder(got), want) {
		t.Errorf("after reorder, children = %v, want %v", childIDsInOrder(got), want)
	}
	for i, e := range got {
		if e.SortOrder != i {
			t.Errorf("%s SortOrder = %d, want %d", e.ChildID, e.SortOrder, i)
		}
	}
}

func TestContentEdge_Reorder_EmptyIsBadRequest(t *testing.T) {
	s, ctx := newEdgeStore(t)
	if err := s.Reorder(ctx, NewID(), nil); !errors.Is(err, ErrBadRequest) {
		t.Errorf("Reorder(empty): err = %v, want ErrBadRequest", err)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
