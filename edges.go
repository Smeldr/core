package smeldr

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// ContentEdge is one composition edge: a record that a parent node contains a
// child node at a given position. It is the single shape used for both
// page→block and collection→item composition — "edge" is the graph term for a
// connection between two nodes.
//
// Edges are addressed by ID and are kept deliberately separate from semantic
// relations: an edge is always asserted and ordered (SortOrder is the point),
// whereas a relation may be inferred, unordered, and weighted.
type ContentEdge struct {
	// ID is the UUID v7 primary key. Assigned by [ContentEdgeStore.AddChild]
	// when empty.
	ID string

	// ParentID and ParentType identify the containing node.
	ParentID   string
	ParentType string

	// ChildID and ChildType identify the contained node.
	ChildID   string
	ChildType string

	// SortOrder is the child's zero-based position within the parent.
	// Maintained by [ContentEdgeStore.AddChild] (append) and
	// [ContentEdgeStore.Reorder].
	SortOrder int

	// IsShared marks a child that is referenced by more than one parent.
	// Editing such a child affects every page that contains it.
	IsShared bool

	// EdgeRole distinguishes the kind of containment — "section" (a block on a
	// page), "item" (an item in a collection), or a future region such as
	// "header"/"footer". Defaults to "section" when empty.
	EdgeRole string
}

// ContentEdgeStore provides CRUD over the smeldr_content_edges table. Create
// the table with [CreateBlockTables] before use.
//
// The store reads a parent's full ordered child list in one query
// ([ContentEdgeStore.Children]) and many parents' children in one batched query
// ([ContentEdgeStore.ChildrenOf]) — no per-child lookups — so the render engine
// can assemble a page tree without an N+1 query pattern.
type ContentEdgeStore struct {
	db DB
}

// NewContentEdgeStore returns a [ContentEdgeStore] backed by db.
func NewContentEdgeStore(db DB) *ContentEdgeStore {
	return &ContentEdgeStore{db: db}
}

// edgeColumns is the fixed SELECT column list, matching the scan order in
// scanEdges.
const edgeColumns = `id, parent_id, parent_type, child_id, child_type, sort_order, is_shared, edge_role`

// AddChild appends e as the last child of its parent and returns the stored
// edge. The ID is assigned (UUID v7) when empty, EdgeRole defaults to
// "section", and SortOrder is set to one past the parent's current maximum —
// any SortOrder set by the caller is overwritten.
//
// AddChild computes the next position with a read-then-insert. Under concurrent
// appends to the same parent on a backend that allows them (e.g. pgx/Postgres
// with multiple editors), two edges can receive the same SortOrder; a
// subsequent [ContentEdgeStore.Reorder] corrects it. On single-writer SQLite
// this cannot occur. If concurrent multi-editor composition is ever required,
// revisit this with a transaction or a sequence.
func (s *ContentEdgeStore) AddChild(ctx context.Context, e ContentEdge) (ContentEdge, error) {
	if e.ID == "" {
		e.ID = NewID()
	}
	if e.EdgeRole == "" {
		e.EdgeRole = "section"
	}

	var next int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(sort_order), -1) + 1 FROM smeldr_content_edges WHERE parent_id = $1`,
		e.ParentID,
	).Scan(&next); err != nil {
		return ContentEdge{}, err
	}
	e.SortOrder = next

	shared := 0
	if e.IsShared {
		shared = 1
	}

	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO smeldr_content_edges (`+edgeColumns+`)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		e.ID, e.ParentID, e.ParentType, e.ChildID, e.ChildType, e.SortOrder, shared, e.EdgeRole,
	); err != nil {
		return ContentEdge{}, err
	}
	return e, nil
}

// Children returns all edges whose parent is parentID, ordered by SortOrder.
// Returns an empty slice (never nil, never an error) when the parent has no
// children.
func (s *ContentEdgeStore) Children(ctx context.Context, parentID string) ([]ContentEdge, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+edgeColumns+` FROM smeldr_content_edges WHERE parent_id = $1 ORDER BY sort_order`,
		parentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEdges(rows)
}

// ChildrenOf returns the edges of every parent in parentIDs in one query,
// ordered by parent then SortOrder. It is the batched read the render engine
// uses to load an entire page tree level without a per-parent round trip.
// Returns an empty slice when parentIDs is empty.
func (s *ContentEdgeStore) ChildrenOf(ctx context.Context, parentIDs []string) ([]ContentEdge, error) {
	if len(parentIDs) == 0 {
		return []ContentEdge{}, nil
	}

	placeholders := make([]string, len(parentIDs))
	args := make([]any, len(parentIDs))
	for i, id := range parentIDs {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = id
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT `+edgeColumns+` FROM smeldr_content_edges
		 WHERE parent_id IN (`+strings.Join(placeholders, ", ")+`)
		 ORDER BY parent_id, sort_order`,
		args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEdges(rows)
}

// RemoveChild deletes the edge linking parentID to childID. Returns
// [ErrNotFound] when no such edge exists.
func (s *ContentEdgeStore) RemoveChild(ctx context.Context, parentID, childID string) error {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM smeldr_content_edges WHERE parent_id = $1 AND child_id = $2`,
		parentID, childID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// Reorder sets the SortOrder of the named children of parentID to match the
// order of orderedChildIDs (the first ID becomes position 0, and so on). It
// runs as a single atomic UPDATE. IDs not currently children of the parent are
// ignored; children of the parent not named in orderedChildIDs keep their
// existing SortOrder. Returns [ErrBadRequest] when orderedChildIDs is empty.
func (s *ContentEdgeStore) Reorder(ctx context.Context, parentID string, orderedChildIDs []string) error {
	if len(orderedChildIDs) == 0 {
		return ErrBadRequest
	}

	var caseExpr strings.Builder
	caseExpr.WriteString("CASE child_id")
	inPlaceholders := make([]string, len(orderedChildIDs))
	args := make([]any, 0, len(orderedChildIDs)+1)
	args = append(args, parentID) // $1
	for i, id := range orderedChildIDs {
		ph := fmt.Sprintf("$%d", i+2) // $2, $3, …
		fmt.Fprintf(&caseExpr, " WHEN %s THEN %d", ph, i)
		inPlaceholders[i] = ph
		args = append(args, id)
	}
	caseExpr.WriteString(" END")

	_, err := s.db.ExecContext(ctx,
		`UPDATE smeldr_content_edges SET sort_order = `+caseExpr.String()+
			` WHERE parent_id = $1 AND child_id IN (`+strings.Join(inPlaceholders, ", ")+`)`,
		args...)
	return err
}

// scanEdges reads all rows into a slice of [ContentEdge], translating the
// is_shared INTEGER column into a bool. The SELECT column order must match
// [edgeColumns].
func scanEdges(rows *sql.Rows) ([]ContentEdge, error) {
	edges := make([]ContentEdge, 0)
	for rows.Next() {
		var (
			e      ContentEdge
			shared int64
		)
		if err := rows.Scan(
			&e.ID, &e.ParentID, &e.ParentType, &e.ChildID, &e.ChildType,
			&e.SortOrder, &shared, &e.EdgeRole,
		); err != nil {
			return nil, err
		}
		e.IsShared = shared != 0
		edges = append(edges, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return edges, nil
}
