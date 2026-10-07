// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// A fixture with a known size: the anchor "a" has fanout children c00..c(n-1),
// and each child has one child of its own (g00..), so ring 1 holds n items and
// ring 2 holds n.
func fanFixture(t *testing.T, store *RelationStore, n int) {
	t.Helper()
	upsertTestKind(t, store, "related_to", "item", "item")
	for i := 0; i < n; i++ {
		c, g := fmt.Sprintf("c%02d", i), fmt.Sprintf("g%02d", i)
		mustAssert(t, store, "item", "a", "item", c, "related_to")
		mustAssert(t, store, "item", c, "item", g, "related_to")
	}
}

func sameRings(t *testing.T, a, b *Reachability) {
	t.Helper()
	if len(a.Rings) != len(b.Rings) {
		t.Fatalf("%d rings vs %d", len(a.Rings), len(b.Rings))
	}
	for i := range a.Rings {
		ak, bk := ringKeys(a.Rings[i].Items), ringKeys(b.Rings[i].Items)
		if strings.Join(ak, ",") != strings.Join(bk, ",") {
			t.Errorf("ring %d: %v vs %v", i+1, ak, bk)
		}
	}
}

// Below the cap the bounded walk is the unbounded walk: a diamond, a cycle, an
// ended edge in the middle of a path, mixed classes.
func TestReachabilityBounded_BelowTheCapEqualsReachability(t *testing.T) {
	store := setupRelationStore(t)
	ctx := context.Background()
	upsertTestKind(t, store, "related_to", "item", "item")
	mustAssert(t, store, "item", "a", "item", "b", "related_to")
	mustAssert(t, store, "item", "a", "item", "c", "related_to")
	mustAssert(t, store, "item", "b", "item", "d", "related_to")
	mustAssert(t, store, "item", "c", "item", "d", "related_to")
	mustAssert(t, store, "item", "d", "item", "a", "related_to") // a cycle
	mustAssert(t, store, "item", "d", "item", "e", "related_to")
	past := time.Now().Add(-time.Hour).UTC()
	if err := store.Assert(ctx, RelationEdge{SourceType: "item", SourceID: "e", TargetType: "item", TargetID: "ended", RelationKind: "related_to", EdgeClass: "asserted", InvalidAt: &past}); err != nil {
		t.Fatalf("Assert ended edge: %v", err)
	}

	want, err := store.Reachability(ctx, "item", "a", "", "outgoing", 5)
	if err != nil {
		t.Fatalf("Reachability: %v", err)
	}
	got, err := store.ReachabilityBounded(ctx, "item", "a", "", "outgoing", 5, 0)
	if err != nil {
		t.Fatalf("ReachabilityBounded: %v", err)
	}
	if got.Cut != nil {
		t.Errorf("a walk below the cap carries a cut: %+v", got.Cut)
	}
	sameRings(t, want, got)
	for _, r := range got.Rings {
		for _, it := range r.Items {
			if it.ID == "ended" {
				t.Error("an ended edge was walked")
			}
		}
	}
}

// A cap inside ring 2: exactly maxItems items, the true leftover counted, no ring
// beyond the cut, and the kept items are the first by type and id.
func TestReachabilityBounded_CutInsideARing(t *testing.T) {
	store := setupRelationStore(t)
	fanFixture(t, store, 10) // ring 1: 10, ring 2: 10
	got, err := store.ReachabilityBounded(context.Background(), "item", "a", "", "outgoing", 4, 14)
	if err != nil {
		t.Fatalf("ReachabilityBounded: %v", err)
	}
	if got.Cut == nil || got.Cut.Depth != 2 || got.Cut.Dropped != 6 {
		t.Fatalf("cut = %+v, want depth 2 dropped 6", got.Cut)
	}
	if len(got.Rings) != 2 {
		t.Fatalf("%d rings returned, want 2 (nothing deeper than the cut is reported, not even empty rings)", len(got.Rings))
	}
	total := 0
	for _, r := range got.Rings {
		total += len(r.Items)
	}
	if total != 14 || len(got.Rings[0].Items) != 10 || len(got.Rings[1].Items) != 4 {
		t.Errorf("items %d (ring1 %d, ring2 %d), want 14 (10, 4)", total, len(got.Rings[0].Items), len(got.Rings[1].Items))
	}
	if keys := ringKeys(got.Rings[1].Items); strings.Join(keys, ",") != "item:g00,item:g01,item:g02,item:g03" {
		t.Errorf("kept = %v, want the first four by id", keys)
	}
	// The same call gives the same answer.
	again, _ := store.ReachabilityBounded(context.Background(), "item", "a", "", "outgoing", 4, 14)
	sameRings(t, got, again)
}

// A cap equal to the size of the walked component is not a cut; one less is.
func TestReachabilityBounded_CapEqualToTheComponentIsNotACut(t *testing.T) {
	store := setupRelationStore(t)
	ctx := context.Background()
	fanFixture(t, store, 3) // 6 nodes besides the anchor
	exact, err := store.ReachabilityBounded(ctx, "item", "a", "", "outgoing", 5, 6)
	if err != nil || exact.Cut != nil || len(exact.Rings) != 5 {
		t.Fatalf("exact cap = %+v, %v, want no cut and the usual five rings", exact, err)
	}
	one, _ := store.ReachabilityBounded(ctx, "item", "a", "", "outgoing", 5, 5)
	if one.Cut == nil || one.Cut.Depth != 2 || one.Cut.Dropped != 1 {
		t.Errorf("cap one short = %+v, want a cut at depth 2 dropping 1", one.Cut)
	}
}

// The cap lands exactly at the end of a ring and the next ring has more: the cut
// is in the next ring, which is reported empty with all its items dropped.
func TestReachabilityBounded_CapAtARingBoundaryWithMoreBeyond(t *testing.T) {
	store := setupRelationStore(t)
	fanFixture(t, store, 3)
	got, err := store.ReachabilityBounded(context.Background(), "item", "a", "", "outgoing", 5, 3)
	if err != nil {
		t.Fatalf("ReachabilityBounded: %v", err)
	}
	if got.Cut == nil || got.Cut.Depth != 2 || got.Cut.Dropped != 3 || len(got.Rings) != 2 || len(got.Rings[1].Items) != 0 {
		t.Errorf("result = %+v cut %+v, want ring 2 empty with 3 dropped", got.Rings, got.Cut)
	}
}

func TestReachabilityBounded_Arguments(t *testing.T) {
	store := setupRelationStore(t)
	ctx := context.Background()
	fanFixture(t, store, 2)
	if _, err := store.ReachabilityBounded(ctx, "item", "a", "", "outgoing", 2, -1); err != ErrBadRequest {
		t.Errorf("negative maxItems = %v, want ErrBadRequest", err)
	}
	if _, err := store.ReachabilityBounded(ctx, "item", "a", "", "outgoing", 0, 5); err != ErrBadRequest {
		t.Errorf("depth 0 = %v, want ErrBadRequest", err)
	}
	if _, err := store.ReachabilityBounded(ctx, "item", "a", "", "sideways", 2, 5); err != ErrBadRequest {
		t.Errorf("bad direction = %v, want ErrBadRequest", err)
	}
	// 0 is the default and a huge value is capped (the walk is complete either way here).
	for _, n := range []int{0, 1 << 30} {
		if r, err := store.ReachabilityBounded(ctx, "item", "a", "", "outgoing", 2, n); err != nil || r.Cut != nil {
			t.Errorf("maxItems %d = %+v, %v, want a complete walk", n, r, err)
		}
	}
}

// reachCountDB counts the relation queries a walk issues.
type reachCountDB struct {
	DB
	queries atomic.Int32
}

func (d *reachCountDB) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	if strings.Contains(q, "FROM smeldr_relations") {
		d.queries.Add(1)
	}
	return d.DB.QueryContext(ctx, q, args...)
}

// The cost bound: at most maxItems expansions, so at most maxItems times the
// number of directions queries, however large the graph.
func TestReachabilityBounded_QueriesStayWithinTheBound(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateRelationTables(db); err != nil {
		t.Fatalf("CreateRelationTables: %v", err)
	}
	base, err := NewRelationStore(db)
	if err != nil {
		t.Fatal(err)
	}
	fanFixture(t, base, 60) // 120 nodes
	cdb := &reachCountDB{DB: db}
	store, err := NewRelationStore(cdb)
	if err != nil {
		t.Fatal(err)
	}
	cdb.queries.Store(0)
	const maxItems = 25
	got, err := store.ReachabilityBounded(context.Background(), "item", "a", "", "both", 6, maxItems)
	if err != nil || got.Cut == nil {
		t.Fatalf("walk = %+v, %v, want a cut", got, err)
	}
	if n := cdb.queries.Load(); n > 2*maxItems+2 {
		t.Errorf("%d relation queries for a cap of %d, want at most %d", n, maxItems, 2*maxItems+2)
	}
}
