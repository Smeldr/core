package smeldr

import (
	"context"
	"testing"
)

// TestNavTree_buildTree verifies that a flat list is assembled into the correct
// parent→child hierarchy with the right roots.
func TestNavTree_buildTree(t *testing.T) {
	items := []NavItem{
		{ID: "root1", Label: "Root One", SortOrder: 1},
		{ID: "root2", Label: "Root Two", SortOrder: 2},
		{ID: "child1", Label: "Child One", ParentID: "root1", SortOrder: 1},
		{ID: "child2", Label: "Child Two", ParentID: "root1", SortOrder: 2},
		{ID: "grand", Label: "Grandchild", ParentID: "child1", SortOrder: 1},
	}

	var nt NavTree
	nt.setCode(items)

	tree := nt.Tree()
	if len(tree) != 2 {
		t.Fatalf("expected 2 roots, got %d", len(tree))
	}
	if tree[0].ID != "root1" || tree[1].ID != "root2" {
		t.Errorf("unexpected root order: %v, %v", tree[0].ID, tree[1].ID)
	}
	if len(tree[0].Children) != 2 {
		t.Fatalf("expected 2 children of root1, got %d", len(tree[0].Children))
	}
	if tree[0].Children[0].ID != "child1" {
		t.Errorf("expected child1 first, got %s", tree[0].Children[0].ID)
	}
	if len(tree[0].Children[0].Children) != 1 {
		t.Fatalf("expected 1 grandchild, got %d", len(tree[0].Children[0].Children))
	}
	if tree[0].Children[0].Children[0].ID != "grand" {
		t.Errorf("unexpected grandchild: %s", tree[0].Children[0].Children[0].ID)
	}
}

// TestNavTree_sortOrder verifies that children are ordered by SortOrder ascending,
// with Label as the tiebreaker.
func TestNavTree_sortOrder(t *testing.T) {
	items := []NavItem{
		{ID: "p", Label: "Parent"},
		{ID: "c3", Label: "C", ParentID: "p", SortOrder: 3},
		{ID: "c1", Label: "A", ParentID: "p", SortOrder: 1},
		{ID: "c2a", Label: "B", ParentID: "p", SortOrder: 2},
		{ID: "c2b", Label: "A", ParentID: "p", SortOrder: 2}, // same order as c2a — label tiebreak
	}

	var nt NavTree
	nt.setCode(items)

	tree := nt.Tree()
	if len(tree) != 1 {
		t.Fatalf("expected 1 root, got %d", len(tree))
	}
	children := tree[0].Children
	if len(children) != 4 {
		t.Fatalf("expected 4 children, got %d", len(children))
	}
	want := []string{"c1", "c2b", "c2a", "c3"} // sort_order then label
	for i, id := range want {
		if children[i].ID != id {
			t.Errorf("children[%d]: want %s, got %s", i, id, children[i].ID)
		}
	}
}

// TestNavTree_hiddenGhost verifies that Hidden and Ghost flag values are
// preserved correctly through the tree.
func TestNavTree_hiddenGhost(t *testing.T) {
	items := []NavItem{
		{ID: "a", Hidden: false, Ghost: false},
		{ID: "b", Hidden: true, Ghost: false},
		{ID: "c", Hidden: false, Ghost: true},
		{ID: "d", Hidden: true, Ghost: true},
	}

	var nt NavTree
	nt.setCode(items)

	flat := nt.List()
	byID := make(map[string]NavItem, len(flat))
	for _, item := range flat {
		byID[item.ID] = item
	}

	cases := []struct {
		id     string
		hidden bool
		ghost  bool
	}{
		{"a", false, false},
		{"b", true, false},
		{"c", false, true},
		{"d", true, true},
	}
	for _, tc := range cases {
		item, ok := byID[tc.id]
		if !ok {
			t.Errorf("item %s not found in List()", tc.id)
			continue
		}
		if item.Hidden != tc.hidden {
			t.Errorf("%s: Hidden want %v, got %v", tc.id, tc.hidden, item.Hidden)
		}
		if item.Ghost != tc.ghost {
			t.Errorf("%s: Ghost want %v, got %v", tc.id, tc.ghost, item.Ghost)
		}
	}
}

// TestNavTree_setCode verifies that setCode builds the tree without a database.
func TestNavTree_setCode(t *testing.T) {
	items := []NavItem{
		{ID: "r", Label: "Root"},
		{ID: "c", Label: "Child", ParentID: "r"},
	}

	var nt NavTree
	nt.setCode(items)

	if nt.HasDB() {
		t.Error("expected HasDB() == false in code mode")
	}

	tree := nt.Tree()
	if len(tree) != 1 {
		t.Fatalf("expected 1 root, got %d", len(tree))
	}
	if len(tree[0].Children) != 1 {
		t.Fatalf("expected 1 child, got %d", len(tree[0].Children))
	}
}

// TestNavTree_deleteCascade_ids verifies that collectDescendantIDs returns the
// root item ID plus all descendant IDs.
func TestNavTree_deleteCascade_ids(t *testing.T) {
	items := []NavItem{
		{ID: "root"},
		{ID: "child1", ParentID: "root"},
		{ID: "child2", ParentID: "root"},
		{ID: "grand1", ParentID: "child1"},
		{ID: "grand2", ParentID: "child1"},
		{ID: "other"}, // unrelated root item
	}

	var nt NavTree
	nt.setCode(items)

	nt.mu.RLock()
	ids := nt.collectDescendantIDs("root")
	nt.mu.RUnlock()

	// Should contain root, child1, child2, grand1, grand2 — but NOT "other".
	if len(ids) != 5 {
		t.Fatalf("expected 5 ids, got %d: %v", len(ids), ids)
	}
	idSet := make(map[string]bool, len(ids))
	for _, id := range ids {
		idSet[id] = true
	}
	for _, want := range []string{"root", "child1", "child2", "grand1", "grand2"} {
		if !idSet[want] {
			t.Errorf("expected %q in descendant IDs", want)
		}
	}
	if idSet["other"] {
		t.Error("unexpected 'other' in descendant IDs")
	}
}

// TestNavTree_orphaned verifies that items whose parent does not exist are
// promoted to root level.
func TestNavTree_orphaned(t *testing.T) {
	items := []NavItem{
		{ID: "a", Label: "Root"},
		{ID: "b", Label: "Orphan", ParentID: "nonexistent"},
	}

	var nt NavTree
	nt.setCode(items)

	tree := nt.Tree()
	if len(tree) != 2 {
		t.Fatalf("expected orphan promoted to root: got %d roots", len(tree))
	}
}

// TestNavTree_get verifies the Get method returns a copy without Children and
// returns false for unknown IDs.
func TestNavTree_get(t *testing.T) {
	items := []NavItem{
		{ID: "r", Label: "Root"},
		{ID: "c", Label: "Child", ParentID: "r"},
	}

	var nt NavTree
	nt.setCode(items)

	item, ok := nt.Get("r")
	if !ok {
		t.Fatal("expected Get to return ok=true for existing item")
	}
	if item.ID != "r" {
		t.Errorf("expected ID=r, got %s", item.ID)
	}
	if item.Children != nil {
		t.Error("Get should return item with nil Children")
	}

	_, ok = nt.Get("missing")
	if ok {
		t.Error("expected Get to return ok=false for missing item")
	}
}

// TestNavTree_deepCopy verifies that Tree() returns independent copies —
// mutations to the returned slice do not affect the tree.
func TestNavTree_deepCopy(t *testing.T) {
	items := []NavItem{
		{ID: "r", Label: "Root"},
		{ID: "c", Label: "Child", ParentID: "r"},
	}

	var nt NavTree
	nt.setCode(items)

	tree1 := nt.Tree()
	tree1[0].Label = "mutated"
	if len(tree1[0].Children) > 0 {
		tree1[0].Children[0].Label = "mutated-child"
	}

	tree2 := nt.Tree()
	if tree2[0].Label == "mutated" {
		t.Error("Tree() should return independent copies — mutation affected internal state")
	}
}

func TestBoolToInt(t *testing.T) {
	if boolToInt(true) != 1 {
		t.Error("boolToInt(true) should be 1")
	}
	if boolToInt(false) != 0 {
		t.Error("boolToInt(false) should be 0")
	}
}

func TestNavTree_CodeMode_writeErrors(t *testing.T) {
	var nt NavTree // no db set — code mode
	ctx := context.Background()

	if _, err := nt.Create(ctx, NavItem{Label: "x"}); err == nil {
		t.Error("Create in code mode should return error")
	}
	if _, err := nt.Update(ctx, NavItem{ID: "x"}); err == nil {
		t.Error("Update in code mode should return error")
	}
	if err := nt.Delete(ctx, "x"); err == nil {
		t.Error("Delete in code mode should return error")
	}
}

func TestNavTree_DB_migrate(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()
	var nt NavTree
	if err := nt.migrate(ctx, db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if !nt.HasDB() {
		t.Error("HasDB() should be true after migrate")
	}
}

func TestNavTree_DB_load_empty(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()
	var nt NavTree
	if err := nt.migrate(ctx, db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := nt.load(ctx, db); err != nil {
		t.Fatalf("load: %v", err)
	}
	if nt.Tree() != nil {
		t.Error("Tree() should be nil on empty table")
	}
}

func TestNavTree_DB_Create(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()
	var nt NavTree
	if err := nt.migrate(ctx, db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	item, err := nt.Create(ctx, NavItem{Label: "Home", Path: "/", Hidden: true, Ghost: false})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if item.ID == "" {
		t.Error("Create should assign an ID")
	}

	tree := nt.Tree()
	if len(tree) != 1 {
		t.Fatalf("Tree() len: got %d, want 1", len(tree))
	}
	if tree[0].Label != "Home" {
		t.Errorf("Label: got %q, want \"Home\"", tree[0].Label)
	}
	if !tree[0].Hidden {
		t.Error("Hidden should be true")
	}
}

func TestNavTree_DB_Update(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()
	var nt NavTree
	if err := nt.migrate(ctx, db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	created, err := nt.Create(ctx, NavItem{Label: "Old"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	created.Label = "New"
	updated, err := nt.Update(ctx, created)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Label != "New" {
		t.Errorf("Label after update: got %q, want \"New\"", updated.Label)
	}

	tree := nt.Tree()
	if len(tree) != 1 || tree[0].Label != "New" {
		t.Errorf("Tree after update: unexpected state")
	}
}

func TestNavTree_DB_Update_notFound(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()
	var nt NavTree
	if err := nt.migrate(ctx, db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	_, err := nt.Update(ctx, NavItem{ID: "nonexistent", Label: "x"})
	if err != ErrNotFound {
		t.Errorf("Update nonexistent: got %v, want ErrNotFound", err)
	}
}

func TestNavTree_DB_Delete(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()
	var nt NavTree
	if err := nt.migrate(ctx, db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	item, err := nt.Create(ctx, NavItem{Label: "Temp"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := nt.Delete(ctx, item.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if nt.Tree() != nil {
		t.Error("Tree() should be nil after deleting the only item")
	}
}

func TestNavTree_DB_Delete_notFound(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()
	var nt NavTree
	if err := nt.migrate(ctx, db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	if err := nt.Delete(ctx, "nonexistent"); err != ErrNotFound {
		t.Errorf("Delete nonexistent: got %v, want ErrNotFound", err)
	}
}

func TestNavTree_DB_Delete_cascade(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()
	var nt NavTree
	if err := nt.migrate(ctx, db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	parent, err := nt.Create(ctx, NavItem{Label: "Parent"})
	if err != nil {
		t.Fatalf("Create parent: %v", err)
	}
	_, err = nt.Create(ctx, NavItem{Label: "Child", ParentID: parent.ID})
	if err != nil {
		t.Fatalf("Create child: %v", err)
	}

	if err := nt.Delete(ctx, parent.ID); err != nil {
		t.Fatalf("Delete parent: %v", err)
	}
	if nt.Tree() != nil {
		t.Error("Tree() should be nil after cascade delete of parent+child")
	}
}
