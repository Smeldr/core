package smeldr

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
)

// NavMode controls how the application's navigation tree is populated.
// The zero value means no navigation tree is active.
type NavMode int

const (
	// NavModeDB populates the navigation tree from the smeldr_nav database
	// table. Requires [Config.DB] to be non-nil; panics at startup if DB
	// is nil when this mode is selected.
	NavModeDB NavMode = iota + 1

	// NavModeCode populates the navigation tree from items supplied via
	// [App.Nav]. No database access is performed.
	NavModeCode
)

// NavItem represents a single entry in the navigation tree. Items are stored
// in the smeldr_nav table ([NavModeDB]) or supplied via [App.Nav] ([NavModeCode]).
//
// The Hidden and Ghost flags determine where the item appears:
//
//	Hidden=false Ghost=false — shown in navigation; in breadcrumb; clickable
//	Hidden=true  Ghost=false — hidden from navigation; in breadcrumb; clickable
//	Hidden=false Ghost=true  — shown in navigation; in breadcrumb; not clickable
//	Hidden=true  Ghost=true  — hidden from navigation; in breadcrumb; not clickable
type NavItem struct {
	// ID is the unique identifier. Generated automatically as a UUIDv7 on
	// create when empty.
	ID string

	// Label is the display text rendered in navigations and breadcrumbs.
	Label string

	// Path is the URL prefix for this item, e.g. "/learn". An empty Path
	// marks the item as a ghost — it has no backing route and is
	// non-clickable everywhere.
	Path string

	// ParentID is the ID of the parent NavItem. Empty for top-level items.
	ParentID string

	// Module is the Forge module table name this item maps to, e.g.
	// "posts". Empty for custom or ghost items not backed by a content module.
	Module string

	// Hidden excludes this item from rendered navigation while keeping it
	// accessible in breadcrumbs. A hidden item is still clickable.
	Hidden bool

	// Ghost marks this item as non-clickable everywhere. Ghost items appear
	// in navigation (unless also Hidden) but have no backing route. Use
	// ghost items as structural grouping nodes.
	Ghost bool

	// SortOrder controls the display order within a parent level. Lower
	// values appear first. Items with equal SortOrder are sorted
	// alphabetically by Label.
	SortOrder int

	// Children holds the item's direct children in SortOrder order.
	// Children is populated in memory during tree construction and is
	// never persisted to the database.
	Children []*NavItem
}

// NavTree holds the in-memory navigation tree and provides thread-safe access
// for both reading (templates) and writing (MCP tools).
//
// Obtain a NavTree from [App.NavTree] after calling [App.Handler].
type NavTree struct {
	mu    sync.RWMutex
	flat  map[string]*NavItem // all items keyed by ID
	roots []*NavItem          // top-level items ordered by SortOrder then Label
	db    DB                  // nil in NavModeCode; set by migrate in NavModeDB
}

// HasDB reports whether the NavTree is backed by a database (NavModeDB).
// Only when HasDB is true can Create, Update, and Delete be called.
func (n *NavTree) HasDB() bool {
	return n.db != nil
}

// migrate creates the smeldr_nav table if it does not already exist, and stores
// the db reference for subsequent CRUD operations. Called once at [App.Handler]
// startup when [Config.NavMode] is [NavModeDB].
func (n *NavTree) migrate(ctx context.Context, db DB) error {
	n.db = db
	_, err := db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS smeldr_nav (
	id         TEXT PRIMARY KEY,
	label      TEXT NOT NULL DEFAULT '',
	path       TEXT NOT NULL DEFAULT '',
	parent_id  TEXT NOT NULL DEFAULT '',
	module     TEXT NOT NULL DEFAULT '',
	hidden     INTEGER NOT NULL DEFAULT 0,
	ghost      INTEGER NOT NULL DEFAULT 0,
	sort_order INTEGER NOT NULL DEFAULT 0
)`)
	if err != nil {
		return fmt.Errorf("smeldr: migrate smeldr_nav: %w", err)
	}
	return nil
}

// load reads all rows from smeldr_nav and rebuilds the in-memory tree.
// Called once at [App.Handler] startup after [migrate], and after any write.
func (n *NavTree) load(ctx context.Context, db DB) error {
	rows, err := db.QueryContext(ctx,
		`SELECT id, label, path, parent_id, module, hidden, ghost, sort_order
		 FROM smeldr_nav
		 ORDER BY sort_order ASC, label ASC`)
	if err != nil {
		return fmt.Errorf("smeldr: load smeldr_nav: %w", err)
	}
	defer rows.Close()
	var items []NavItem
	for rows.Next() {
		var item NavItem
		var hidden, ghost int
		if err := rows.Scan(
			&item.ID, &item.Label, &item.Path, &item.ParentID,
			&item.Module, &hidden, &ghost, &item.SortOrder,
		); err != nil {
			return fmt.Errorf("smeldr: scan smeldr_nav row: %w", err)
		}
		item.Hidden = hidden != 0
		item.Ghost = ghost != 0
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("smeldr: iterate smeldr_nav: %w", err)
	}
	n.mu.Lock()
	n.buildTree(items)
	n.mu.Unlock()
	return nil
}

// buildTree rebuilds flat and roots from a flat list of items. Orphaned items
// (whose parent does not exist in the list) are promoted to root level.
// Must be called with n.mu held for writing.
func (n *NavTree) buildTree(items []NavItem) {
	n.flat = make(map[string]*NavItem, len(items))
	for i := range items {
		cp := items[i]
		cp.Children = nil
		n.flat[cp.ID] = &cp
	}
	var roots []*NavItem
	for _, item := range n.flat {
		if item.ParentID == "" {
			roots = append(roots, item)
		} else if parent, ok := n.flat[item.ParentID]; ok {
			parent.Children = append(parent.Children, item)
		} else {
			// Orphaned item — promote to root.
			roots = append(roots, item)
		}
	}
	sortNavItems(roots)
	for _, item := range n.flat {
		sortNavItems(item.Children)
	}
	n.roots = roots
}

// sortNavItems sorts a []*NavItem slice by SortOrder ascending, then Label ascending.
func sortNavItems(items []*NavItem) {
	slices.SortFunc(items, func(a, b *NavItem) int {
		if a.SortOrder != b.SortOrder {
			return cmp.Compare(a.SortOrder, b.SortOrder)
		}
		return cmp.Compare(a.Label, b.Label)
	})
}

// setCode populates the tree from items supplied in code ([NavModeCode]).
// The items slice is copied; the caller's slice is not retained.
func (n *NavTree) setCode(items []NavItem) {
	n.mu.Lock()
	n.buildTree(items)
	n.mu.Unlock()
}

// Tree returns a deep copy of the root navigation items with their Children
// populated. The returned slice is safe for concurrent use and modification.
// Returns nil when the tree is empty.
func (n *NavTree) Tree() []NavItem {
	n.mu.RLock()
	defer n.mu.RUnlock()
	if len(n.roots) == 0 {
		return nil
	}
	return deepCopyRoots(n.roots)
}

// List returns a flat slice of all navigation items ordered by SortOrder then
// Label. Children is always nil in the returned items.
// Returns nil when the tree is empty.
func (n *NavTree) List() []NavItem {
	n.mu.RLock()
	defer n.mu.RUnlock()
	if len(n.flat) == 0 {
		return nil
	}
	out := make([]NavItem, 0, len(n.flat))
	for _, item := range n.flat {
		cp := *item
		cp.Children = nil
		out = append(out, cp)
	}
	slices.SortFunc(out, func(a, b NavItem) int {
		if a.SortOrder != b.SortOrder {
			return cmp.Compare(a.SortOrder, b.SortOrder)
		}
		return cmp.Compare(a.Label, b.Label)
	})
	return out
}

// Get returns a copy of the NavItem with the given ID. Returns (NavItem{}, false)
// when no item with that ID exists.
func (n *NavTree) Get(id string) (NavItem, bool) {
	n.mu.RLock()
	defer n.mu.RUnlock()
	item, ok := n.flat[id]
	if !ok {
		return NavItem{}, false
	}
	cp := *item
	cp.Children = nil
	return cp, true
}

// Create inserts a new NavItem into the database, rebuilds the in-memory tree,
// and returns the inserted item with its ID populated.
// Returns an error when the NavTree is in code mode ([NavModeCode]).
func (n *NavTree) Create(ctx context.Context, item NavItem) (NavItem, error) {
	if n.db == nil {
		return NavItem{}, fmt.Errorf("smeldr: NavTree.Create requires NavModeDB")
	}
	if item.ID == "" {
		item.ID = NewID()
	}
	_, err := n.db.ExecContext(ctx,
		`INSERT INTO smeldr_nav (id, label, path, parent_id, module, hidden, ghost, sort_order)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		item.ID, item.Label, item.Path, item.ParentID,
		item.Module, boolToInt(item.Hidden), boolToInt(item.Ghost), item.SortOrder,
	)
	if err != nil {
		return NavItem{}, fmt.Errorf("smeldr: NavTree.Create: %w", err)
	}
	if err := n.load(ctx, n.db); err != nil {
		return NavItem{}, err
	}
	return item, nil
}

// Update replaces the stored NavItem (matched by ID) and rebuilds the
// in-memory tree. Returns [ErrNotFound] when no item with item.ID exists.
// Returns an error when the NavTree is in code mode ([NavModeCode]).
func (n *NavTree) Update(ctx context.Context, item NavItem) (NavItem, error) {
	if n.db == nil {
		return NavItem{}, fmt.Errorf("smeldr: NavTree.Update requires NavModeDB")
	}
	res, err := n.db.ExecContext(ctx,
		`UPDATE smeldr_nav
		 SET label=$1, path=$2, parent_id=$3, module=$4,
		     hidden=$5, ghost=$6, sort_order=$7
		 WHERE id=$8`,
		item.Label, item.Path, item.ParentID, item.Module,
		boolToInt(item.Hidden), boolToInt(item.Ghost), item.SortOrder,
		item.ID,
	)
	if err != nil {
		return NavItem{}, fmt.Errorf("smeldr: NavTree.Update: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return NavItem{}, ErrNotFound
	}
	if err := n.load(ctx, n.db); err != nil {
		return NavItem{}, err
	}
	return item, nil
}

// Delete permanently removes the NavItem with the given id and all of its
// descendants from the database, then rebuilds the in-memory tree.
// Returns [ErrNotFound] when no item with that id exists.
// Returns an error when the NavTree is in code mode ([NavModeCode]).
func (n *NavTree) Delete(ctx context.Context, id string) error {
	if n.db == nil {
		return fmt.Errorf("smeldr: NavTree.Delete requires NavModeDB")
	}

	// Collect all IDs to delete (item + all descendants) from the in-memory
	// tree before touching the database.
	n.mu.RLock()
	ids := n.collectDescendantIDs(id)
	n.mu.RUnlock()

	if len(ids) == 0 {
		return ErrNotFound
	}

	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, did := range ids {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = did
	}
	_, err := n.db.ExecContext(ctx,
		`DELETE FROM smeldr_nav WHERE id IN (`+strings.Join(placeholders, ",")+`)`,
		args...,
	)
	if err != nil {
		return fmt.Errorf("smeldr: NavTree.Delete: %w", err)
	}
	return n.load(ctx, n.db)
}

// collectDescendantIDs returns the ID of the item with the given id and the
// IDs of all its descendants. Returns nil when no item with that id exists.
// Must be called with n.mu held for reading.
func (n *NavTree) collectDescendantIDs(id string) []string {
	item, ok := n.flat[id]
	if !ok {
		return nil
	}
	ids := []string{id}
	for _, child := range item.Children {
		ids = append(ids, n.collectDescendantIDs(child.ID)...)
	}
	return ids
}

// deepCopyRoots returns deep copies of the given root NavItem slice.
func deepCopyRoots(roots []*NavItem) []NavItem {
	out := make([]NavItem, len(roots))
	for i, r := range roots {
		out[i] = deepCopyItem(r)
	}
	return out
}

// deepCopyItem returns a deep copy of item and all its descendants.
func deepCopyItem(item *NavItem) NavItem {
	cp := *item
	if len(item.Children) == 0 {
		cp.Children = nil
		return cp
	}
	cp.Children = make([]*NavItem, len(item.Children))
	for i, child := range item.Children {
		childCp := deepCopyItem(child)
		cp.Children[i] = &childCp
	}
	return cp
}

// boolToInt converts a bool to an integer for SQLite storage (0 or 1).
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
