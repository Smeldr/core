// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"
)

func newListRelationsStore(t *testing.T) (*RelationStore, *sql.DB) {
	t.Helper()
	db := newSQLiteDB(t)
	if err := CreateRelationTables(db); err != nil {
		t.Fatal(err)
	}
	rs, err := NewRelationStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := RegisterOrchestrationRelationKinds(context.Background(), rs); err != nil {
		t.Fatal(err)
	}
	return rs, db
}

func assertTaskEdge(t *testing.T, rs *RelationStore, kind, srcType, src, tgtType, tgt string) RelationEdge {
	t.Helper()
	e, err := rs.MCPAssertRelation(context.Background(), srcType, src, tgtType, tgt, kind, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("assert %s %s->%s: %v", kind, src, tgt, err)
	}
	return e
}

// sortByCreatedThenID orders edges the way ListRelations does: edges created in
// the same instant by id.
func sortByCreatedThenID(edges []RelationEdge) {
	sort.Slice(edges, func(i, j int) bool {
		if !edges[i].CreatedAt.Equal(edges[j].CreatedAt) {
			return edges[i].CreatedAt.Before(edges[j].CreatedAt)
		}
		return edges[i].ID < edges[j].ID
	})
}

func pageIDs(p RelationPage) []string {
	ids := make([]string, len(p.Edges))
	for i, e := range p.Edges {
		ids[i] = e.ID
	}
	return ids
}

// TestListRelations_PagingOrderAndTotal: pages of 3, 3 and 1 over 7 edges, the
// same Total on each, in created_at/id order, with no repeat.
func TestListRelations_PagingOrderAndTotal(t *testing.T) {
	rs, _ := newListRelationsStore(t)
	var asserted []RelationEdge
	for i := 0; i < 7; i++ {
		asserted = append(asserted, assertTaskEdge(t, rs, "depends_on", "Task", fmt.Sprintf("t%d", i), "Task", "t-root"))
	}
	sortByCreatedThenID(asserted)
	var want []string
	for _, e := range asserted {
		want = append(want, e.ID)
	}
	ctx := context.Background()
	first, err := rs.ListRelations(ctx, RelationListOptions{Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	got = append(got, pageIDs(first)...)
	for _, off := range []int{3, 6} {
		p, err := rs.ListRelations(ctx, RelationListOptions{Limit: 3, Offset: off, AsOf: first.AsOf})
		if err != nil {
			t.Fatal(err)
		}
		if p.Total != 7 || !p.AsOf.Equal(first.AsOf) {
			t.Errorf("offset %d: total %d as_of %v", off, p.Total, p.AsOf)
		}
		got = append(got, pageIDs(p)...)
	}
	if first.Total != 7 || strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("pages = %v (total %d), want %v", got, first.Total, want)
	}
	if p, _ := rs.ListRelations(ctx, RelationListOptions{Offset: 50}); len(p.Edges) != 0 || p.Total != 7 {
		t.Errorf("offset past the end = %d edges, total %d", len(p.Edges), p.Total)
	}
	if p, _ := rs.ListRelations(ctx, RelationListOptions{Limit: 10000}); len(p.Edges) != 7 {
		t.Errorf("a limit over 500 is clamped, not refused: %d edges", len(p.Edges))
	}
}

// TestListRelations_FiltersAndEnded: kind, source and target type filters, and
// ended edges only on request, with their end filled.
func TestListRelations_FiltersAndEnded(t *testing.T) {
	rs, _ := newListRelationsStore(t)
	ctx := context.Background()
	assertTaskEdge(t, rs, "depends_on", "Task", "a", "Task", "b")
	assertTaskEdge(t, rs, "ships_as", "Task", "a", "Amendment", "am1")
	gone := assertTaskEdge(t, rs, "depends_on", "Task", "c", "Task", "b")
	if err := rs.Withdraw(ctx, gone.ID, "no longer needed"); err != nil {
		t.Fatal(err)
	}
	count := func(o RelationListOptions) int {
		t.Helper()
		p, err := rs.ListRelations(ctx, o)
		if err != nil {
			t.Fatal(err)
		}
		if p.Total != len(p.Edges) {
			t.Fatalf("total %d, edges %d", p.Total, len(p.Edges))
		}
		return p.Total
	}
	for _, c := range []struct {
		name string
		o    RelationListOptions
		want int
	}{
		{"live", RelationListOptions{}, 2},
		{"kind", RelationListOptions{Kind: "ships_as"}, 1},
		{"source type", RelationListOptions{SourceType: "Task"}, 2},
		{"target type", RelationListOptions{TargetType: "Amendment"}, 1},
		{"combined", RelationListOptions{Kind: "depends_on", TargetType: "Amendment"}, 0},
		{"ended included", RelationListOptions{IncludeEnded: true}, 3},
	} {
		if got := count(c.o); got != c.want {
			t.Errorf("%s: %d edges, want %d", c.name, got, c.want)
		}
	}
	p, _ := rs.ListRelations(ctx, RelationListOptions{IncludeEnded: true, Kind: "depends_on", SourceType: "Task", TargetType: "Task"})
	var ended *RelationEdge
	for i := range p.Edges {
		if p.Edges[i].ID == gone.ID {
			ended = &p.Edges[i]
		}
	}
	if ended == nil || ended.InvalidAt == nil {
		t.Fatalf("the withdrawn edge is missing or not ended: %+v", ended)
	}
}

// TestListRelations_StableAtAsOf: with the first page's AsOf, an edge asserted
// and an edge withdrawn between pages change neither the set nor Total; a fresh
// read sees both changes.
func TestListRelations_StableAtAsOf(t *testing.T) {
	rs, _ := newListRelationsStore(t)
	ctx := context.Background()
	var edges []RelationEdge
	for i := 0; i < 4; i++ {
		edges = append(edges, assertTaskEdge(t, rs, "depends_on", "Task", fmt.Sprintf("s%d", i), "Task", "root"))
	}
	sortByCreatedThenID(edges)
	first, err := rs.ListRelations(ctx, RelationListOptions{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	added := assertTaskEdge(t, rs, "depends_on", "Task", "late", "Task", "root")
	if err := rs.Withdraw(ctx, edges[3].ID, "between pages"); err != nil {
		t.Fatal(err)
	}
	second, err := rs.ListRelations(ctx, RelationListOptions{Limit: 2, Offset: 2, AsOf: first.AsOf})
	if err != nil {
		t.Fatal(err)
	}
	ids := strings.Join(pageIDs(second), ",")
	if second.Total != 4 || ids != edges[2].ID+","+edges[3].ID {
		t.Errorf("page 2 at as_of = %s (total %d); want the original edges 2 and 3", ids, second.Total)
	}
	fresh, err := rs.ListRelations(ctx, RelationListOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	all := strings.Join(pageIDs(fresh), ",")
	if fresh.Total != 4 || !strings.Contains(all, added.ID) || strings.Contains(all, edges[3].ID) {
		t.Errorf("fresh read = %s (total %d); want the new edge in and the withdrawn one out", all, fresh.Total)
	}
}

// TestListRelations_IDsAreTheItemsIDs: an edge between two stored Tasks carries
// their Node.ID, the ID the item list tools return.
func TestListRelations_IDsAreTheItemsIDs(t *testing.T) {
	rs, db := newListRelationsStore(t)
	if err := CreateOrchestrationTables(db); err != nil {
		t.Fatal(err)
	}
	repo := NewSQLRepo[*Task](db, Table("smeldr_tasks"))
	ctx := context.Background()
	a := &Task{Node: Node{ID: NewID(), Slug: "list-rel-a", Status: "backlog"}}
	b := &Task{Node: Node{ID: NewID(), Slug: "list-rel-b", Status: "backlog"}}
	for _, tk := range []*Task{a, b} {
		if err := repo.Save(ctx, tk); err != nil {
			t.Fatal(err)
		}
	}
	assertTaskEdge(t, rs, "depends_on", "Task", a.ID, "Task", b.ID)
	items, err := repo.FindAll(ctx, ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]bool{}
	for _, it := range items {
		listed[it.ID] = true
	}
	p, err := rs.ListRelations(ctx, RelationListOptions{})
	if err != nil || len(p.Edges) != 1 {
		t.Fatalf("page = %+v, %v", p, err)
	}
	e := p.Edges[0]
	if e.SourceType != "Task" || !listed[e.SourceID] || !listed[e.TargetID] || e.SourceID != a.ID || e.TargetID != b.ID {
		t.Errorf("edge %+v does not join to the listed items %v", e, listed)
	}
}

type listRelFailDB struct {
	DB
	countErr, pageErr, scanErr bool
}

func (d *listRelFailDB) QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row {
	if d.countErr && strings.Contains(q, "SELECT COUNT(*) FROM smeldr_relations") {
		return d.DB.QueryRowContext(ctx, "SELECT no_such_function_listrel()")
	}
	return d.DB.QueryRowContext(ctx, q, args...)
}

func (d *listRelFailDB) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	if d.pageErr && strings.Contains(q, "ORDER BY created_at, id LIMIT") {
		return nil, errors.New("disk")
	}
	if d.scanErr && strings.Contains(q, "ORDER BY created_at, id LIMIT") {
		return d.DB.QueryContext(ctx, "SELECT 1")
	}
	return d.DB.QueryContext(ctx, q, args...)
}

// TestListRelations_ErrorPaths covers the error-path table.
func TestListRelations_ErrorPaths(t *testing.T) {
	ctx := context.Background()
	rs, db := newListRelationsStore(t)
	assertTaskEdge(t, rs, "depends_on", "Task", "x", "Task", "y")
	var ve *ValidationError
	if _, err := rs.ListRelations(ctx, RelationListOptions{Limit: -1}); !errors.As(err, &ve) {
		t.Errorf("negative limit = %v", err)
	}
	if _, err := rs.ListRelations(ctx, RelationListOptions{Offset: -1}); !errors.As(err, &ve) {
		t.Errorf("negative offset = %v", err)
	}
	for name, fdb := range map[string]*listRelFailDB{
		"count": {DB: db, countErr: true},
		"page":  {DB: db, pageErr: true},
		"scan":  {DB: db, scanErr: true},
	} {
		s := &RelationStore{db: fdb}
		if _, err := s.ListRelations(ctx, RelationListOptions{}); !errors.Is(err, ErrInternal) {
			t.Errorf("%s failure = %v, want ErrInternal", name, err)
		}
	}
	empty := &RelationStore{db: newSQLiteDB(t)}
	if p, err := empty.ListRelations(ctx, RelationListOptions{}); err != nil || p.Total != 0 || p.AsOf.IsZero() {
		t.Errorf("no relations table = %+v, %v; want an empty page with as_of", p, err)
	}
	gone := assertTaskEdge(t, rs, "depends_on", "Task", "g", "Task", "y")
	if err := rs.Withdraw(ctx, gone.ID, "end"); err != nil {
		t.Fatal(err)
	}
	failingProv := &RelationStore{db: db, provenanceStore: listRelProvFail{}}
	if _, err := failingProv.ListRelations(ctx, RelationListOptions{IncludeEnded: true}); !errors.Is(err, ErrInternal) {
		t.Errorf("a failed end lookup = %v, want ErrInternal", err)
	}
}

// listRelProvFail fails every List, so reading how an edge ended fails.
type listRelProvFail struct{}

func (listRelProvFail) Append(context.Context, ProvenanceRecord) error { return nil }
func (listRelProvFail) List(context.Context, ProvenanceFilter) ([]ProvenanceRecord, error) {
	return nil, errors.New("provenance down")
}
