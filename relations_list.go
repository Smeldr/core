package smeldr

import (
	"context"
	"fmt"
	"strconv"
	"time"
)

// Relation list paging bounds, the same as the item list tools'.
const (
	relationListDefaultLimit = 50
	relationListMaxLimit     = 500
)

// RelationListOptions filters and pages [RelationStore.ListRelations]. Every
// filter is optional and an exact match.
type RelationListOptions struct {
	Kind       string // relation_kind
	SourceType string // source_type
	TargetType string // target_type

	// IncludeEnded adds the relations that had ended by AsOf. The default is
	// the live graph only, with "live" defined as for [RelationStore.GetLiveBySource]:
	// invalid_at is NULL or later than AsOf.
	IncludeEnded bool

	// AsOf fixes the moment the pages describe. Zero means now (UTC); the page
	// returns the time it used, and a client passes it back on every next page
	// so that edges asserted or ended in between do not shift the pages.
	AsOf time.Time

	// Limit is the page size: 0 means 50, above 500 is clamped to 500, negative
	// is refused. Offset skips that many edges; negative is refused.
	Limit, Offset int
}

// RelationPage is one page of [RelationStore.ListRelations].
type RelationPage struct {
	Edges []RelationEdge
	Total int       // every edge matching the filters at AsOf
	AsOf  time.Time // the moment the page was cut at; pass it back for the next page
}

// ListRelations returns one page of the instance's relations, ordered by
// created_at and then id, so pages never repeat or skip. It is the read a
// client uses to hold the whole graph in memory (an org-level snapshot): page
// through to Total with the AsOf of the first page, then follow the relations
// topic of the event stream from AsOf on, adding an edge on relation.asserted
// and dropping it from the live set on relation.ended.
//
// Every page and its Total are cut at AsOf: an edge created after AsOf is not
// in the set, and an edge that ended after AsOf still counts as live (it is
// returned with its invalid_at; its relation.ended event follows on the
// stream). The one thing that can shift later pages is an administrative purge
// ([RelationStore.Delete]), which no tool exposes.
//
// source_id and target_id are returned as stored. Every edge core writes uses
// the item's Node.ID, the ID the item list tools return, so an edge joins to an
// item on (type, ID); a context packet's canonical id is a different form.
// Type names are the registered names (Decision, task_plan), never folded.
// Edges with IncludeEnded and an end on record carry it, as from
// [RelationStore.MCPGetRelations].
//
// Under per-item read rules (the scope attached to ctx, or the caller's own
// under the governance wired on the store's database) the set holds only the
// edges whose both ends the caller may read, and Total counts that set.
//
// A negative Limit or Offset is a [*ValidationError]. A missing relations table
// is an empty page; any other database failure is [ErrInternal].
func (s *RelationStore) ListRelations(ctx context.Context, opts RelationListOptions) (RelationPage, error) {
	if opts.Limit < 0 {
		return RelationPage{}, Err("limit", "must not be negative")
	}
	if opts.Offset < 0 {
		return RelationPage{}, Err("offset", "must not be negative")
	}
	limit := opts.Limit
	switch {
	case limit == 0:
		limit = relationListDefaultLimit
	case limit > relationListMaxLimit:
		limit = relationListMaxLimit
	}
	asOf := opts.AsOf.UTC()
	if opts.AsOf.IsZero() {
		asOf = time.Now().UTC()
	}
	page := RelationPage{AsOf: asOf}

	where := " WHERE created_at <= $1"
	args := []any{asOf}
	if !opts.IncludeEnded {
		where += " AND (invalid_at IS NULL OR invalid_at > $1)"
	}
	for _, f := range []struct{ column, value string }{
		{"relation_kind", opts.Kind},
		{"source_type", opts.SourceType},
		{"target_type", opts.TargetType},
	} {
		if f.value != "" {
			args = append(args, f.value)
			where += " AND " + f.column + " = $" + strconv.Itoa(len(args))
		}
	}

	scope, err := packageScope(ctx, s.db)
	if err != nil {
		return RelationPage{}, err
	}
	if !scope.All() {
		return s.listReadableRelations(ctx, scope, where, args, limit, opts, page)
	}

	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM smeldr_relations"+where, args...).Scan(&page.Total); err != nil {
		if isNoSuchTable(err) {
			return page, nil
		}
		return RelationPage{}, fmt.Errorf("%w: list relations: count: %s", ErrInternal, err)
	}
	pageArgs := append(append([]any{}, args...), limit, opts.Offset)
	rows, err := s.db.QueryContext(ctx,
		"SELECT "+relationColumns+" FROM smeldr_relations"+where+
			" ORDER BY created_at, id LIMIT $"+strconv.Itoa(len(args)+1)+" OFFSET $"+strconv.Itoa(len(args)+2),
		pageArgs...)
	if err != nil {
		return RelationPage{}, fmt.Errorf("%w: list relations: %s", ErrInternal, err)
	}
	defer rows.Close()
	edges, err := collectEdges(rows)
	if err != nil {
		return RelationPage{}, fmt.Errorf("%w: list relations: %s", ErrInternal, err)
	}
	if opts.IncludeEnded {
		if err := s.fillEdgeEnds(ctx, edges); err != nil {
			return RelationPage{}, fmt.Errorf("%w: list relations: reading how ended relations ended: %s", ErrInternal, err)
		}
	}
	page.Edges = edges
	return page, nil
}

// listReadableRelations is [RelationStore.ListRelations] for a caller that
// does not read everything: every matching row is loaded and filtered to the
// edges whose both ends the caller may read, and Total and the page are cut
// from that filtered set, so neither reveals a hidden item. Its cost grows with
// the matching rows, not the page.
func (s *RelationStore) listReadableRelations(ctx context.Context, scope *ReadScope, where string, args []any, limit int, opts RelationListOptions, page RelationPage) (RelationPage, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT "+relationColumns+" FROM smeldr_relations"+where+" ORDER BY created_at, id", args...)
	if err != nil {
		if isNoSuchTable(err) {
			return page, nil
		}
		return RelationPage{}, fmt.Errorf("%w: list relations: %s", ErrInternal, err)
	}
	edges, err := collectEdges(rows)
	rows.Close()
	if err != nil {
		return RelationPage{}, fmt.Errorf("%w: list relations: %s", ErrInternal, err)
	}
	edges = readableEdges(scope, edges)
	page.Total = len(edges)
	start := min(opts.Offset, len(edges))
	edges = edges[start:min(start+limit, len(edges))]
	if opts.IncludeEnded {
		if err := s.fillEdgeEnds(ctx, edges); err != nil {
			return RelationPage{}, fmt.Errorf("%w: list relations: reading how ended relations ended: %s", ErrInternal, err)
		}
	}
	page.Edges = edges
	return page, nil
}
