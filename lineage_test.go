package smeldr

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"testing"
	"time"
)

// — Test helpers ——————————————————————————————————————————————————————————

// traceKeys returns "type:id" strings for a trace's nodes, sorted for
// order-independent comparison — batched queries do not guarantee row order.
func traceKeys(nodes []LineageNode) []string {
	out := make([]string, len(nodes))
	for i, n := range nodes {
		out[i] = n.Type + ":" + n.ID
	}
	sort.Strings(out)
	return out
}

func assertTraceKeys(t *testing.T, got []LineageNode, want []string) {
	t.Helper()
	gotKeys := traceKeys(got)
	wantSorted := append([]string(nil), want...)
	sort.Strings(wantSorted)
	if len(gotKeys) != len(wantSorted) {
		t.Fatalf("trace nodes = %v, want %v", gotKeys, wantSorted)
	}
	for i := range gotKeys {
		if gotKeys[i] != wantSorted[i] {
			t.Fatalf("trace nodes = %v, want %v", gotKeys, wantSorted)
		}
	}
}

func findLineageNode(t *testing.T, nodes []LineageNode, typ, id string) LineageNode {
	t.Helper()
	for _, n := range nodes {
		if n.Type == typ && n.ID == id {
			return n
		}
	}
	t.Fatalf("node %s:%s not found in trace", typ, id)
	return LineageNode{}
}

// failOnNthQueryDB wraps a working DB and fails QueryContext on the nth call
// (1-indexed), succeeding on every other call — isolates one query in a
// multi-query call sequence for error-path coverage.
type failOnNthQueryDB struct {
	DB
	n     int
	calls int
}

func (d *failOnNthQueryDB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	d.calls++
	if d.calls == d.n {
		return nil, errors.New("query error")
	}
	return d.DB.QueryContext(ctx, query, args...)
}

// scanMismatchDB wraps a real DB but ignores the caller's query, always
// running a single-column SELECT instead — used to force collectEdges' own
// row-Scan error path (a bad column shape from the driver), distinct from a
// QueryContext-level failure.
type scanMismatchDB struct {
	DB
}

func (d *scanMismatchDB) QueryContext(ctx context.Context, _ string, _ ...any) (*sql.Rows, error) {
	return d.DB.QueryContext(ctx, "SELECT 1")
}

// — Error paths ———————————————————————————————————————————————————————————

func TestTraceLineage_InvalidInput(t *testing.T) {
	store := setupRelationStore(t)
	ctx := context.Background()

	if _, err := store.TraceLineage(ctx, "", "d1", 1); err != ErrBadRequest {
		t.Errorf("empty anchorType: got %v, want ErrBadRequest", err)
	}
	if _, err := store.TraceLineage(ctx, "decision", "", 1); err != ErrBadRequest {
		t.Errorf("empty anchorID: got %v, want ErrBadRequest", err)
	}
	for _, depth := range []int{0, -1, MaxLineageDepth + 1} {
		if _, err := store.TraceLineage(ctx, "decision", "d1", depth); err != ErrBadRequest {
			t.Errorf("depth=%d: got %v, want ErrBadRequest", depth, err)
		}
	}
}

// TestTraceLineage_DBError_Supersede covers the depth-0 supersede check —
// the first DB call TraceLineage makes.
func TestTraceLineage_DBError_Supersede(t *testing.T) {
	s := mockRelationStore(&errQueryDB{})
	_, err := s.TraceLineage(context.Background(), "decision", "d1", 1)
	if err == nil {
		t.Error("want DB query error from TraceLineage, got nil")
	}
}

// TestTraceLineage_DBError_Source covers the depth-1 source-side batch
// query, isolated from the (successful) depth-0 supersede check that runs
// first — a plain errQueryDB would fail on that first call instead.
func TestTraceLineage_DBError_Source(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateRelationTables(db); err != nil {
		t.Fatalf("CreateRelationTables: %v", err)
	}
	s := mockRelationStore(&failOnNthQueryDB{DB: db, n: 2})
	_, err := s.TraceLineage(context.Background(), "decision", "d1", 1)
	if err == nil {
		t.Error("want DB query error from TraceLineage, got nil")
	}
}

// — Structural behaviour ——————————————————————————————————————————————————

func TestTraceLineage_NoEdges_EmptyTrace(t *testing.T) {
	store := setupRelationStore(t)
	ctx := context.Background()

	got, err := store.TraceLineage(ctx, "decision", "isolated", 3)
	if err != nil {
		t.Fatalf("TraceLineage: %v", err)
	}
	if len(got.Nodes) != 0 {
		t.Errorf("Nodes = %v, want empty", got.Nodes)
	}
	if got.Truncated {
		t.Error("Truncated = true, want false")
	}
}

func TestTraceLineage_MixedKinds(t *testing.T) {
	store := setupRelationStore(t)
	ctx := context.Background()
	upsertTestKind(t, store, "depends_on", "task", "task")
	upsertTestKind(t, store, "derives_from", "task", "goal")

	mustAssert(t, store, "task", "t1", "task", "t2", "depends_on")
	mustAssert(t, store, "task", "t1", "goal", "g1", "derives_from")

	got, err := store.TraceLineage(ctx, "task", "t1", 1)
	if err != nil {
		t.Fatalf("TraceLineage: %v", err)
	}
	assertTraceKeys(t, got.Nodes, []string{"task:t2", "goal:g1"})
	if got.Truncated {
		t.Error("Truncated = true, want false")
	}

	t2 := findLineageNode(t, got.Nodes, "task", "t2")
	if t2.RelationKind != "depends_on" || t2.Depth != 1 {
		t.Errorf("t2 = %+v, want RelationKind=depends_on Depth=1", t2)
	}
	g1 := findLineageNode(t, got.Nodes, "goal", "g1")
	if g1.RelationKind != "derives_from" || g1.Depth != 1 {
		t.Errorf("g1 = %+v, want RelationKind=derives_from Depth=1", g1)
	}
}

func TestTraceLineage_TruncatedAtDepthLimit(t *testing.T) {
	store := setupRelationStore(t)
	ctx := context.Background()
	upsertTestKind(t, store, "depends_on", "task", "task")

	// a -> b -> c -> d, a chain of 3 hops.
	mustAssert(t, store, "task", "a", "task", "b", "depends_on")
	mustAssert(t, store, "task", "b", "task", "c", "depends_on")
	mustAssert(t, store, "task", "c", "task", "d", "depends_on")

	got, err := store.TraceLineage(ctx, "task", "a", 2)
	if err != nil {
		t.Fatalf("TraceLineage: %v", err)
	}
	assertTraceKeys(t, got.Nodes, []string{"task:b", "task:c"})
	if !got.Truncated {
		t.Error("Truncated = false, want true")
	}
}

func TestTraceLineage_NotTruncatedWhenExhausted(t *testing.T) {
	store := setupRelationStore(t)
	ctx := context.Background()
	upsertTestKind(t, store, "depends_on", "task", "task")

	// a -> b -> c, a chain of 2 hops, well within maxDepth.
	mustAssert(t, store, "task", "a", "task", "b", "depends_on")
	mustAssert(t, store, "task", "b", "task", "c", "depends_on")

	got, err := store.TraceLineage(ctx, "task", "a", 5)
	if err != nil {
		t.Fatalf("TraceLineage: %v", err)
	}
	assertTraceKeys(t, got.Nodes, []string{"task:b", "task:c"})
	if got.Truncated {
		t.Error("Truncated = true, want false")
	}
}

func TestTraceLineage_CycleVisitedOnce(t *testing.T) {
	store := setupRelationStore(t)
	ctx := context.Background()
	upsertTestKind(t, store, "depends_on", "task", "task")

	// a -> b -> a, a two-node cycle.
	mustAssert(t, store, "task", "a", "task", "b", "depends_on")
	mustAssert(t, store, "task", "b", "task", "a", "depends_on")

	got, err := store.TraceLineage(ctx, "task", "a", 5)
	if err != nil {
		t.Fatalf("TraceLineage: %v", err)
	}
	assertTraceKeys(t, got.Nodes, []string{"task:b"})
	if got.Truncated {
		t.Error("Truncated = true, want false — the cycle should exhaust the frontier, not run to maxDepth")
	}
}

func TestTraceLineage_InvalidatedEdgeFlaggedAndFollowed(t *testing.T) {
	store := setupRelationStore(t)
	ctx := context.Background()
	upsertTestKind(t, store, "depends_on", "decision", "decision")

	past := time.Now().UTC().Add(-time.Hour)
	if _, err := store.MCPAssertRelation(ctx, "decision", "a", "decision", "x", "depends_on", nil, nil, &past, nil); err != nil {
		t.Fatalf("MCPAssertRelation a->x: %v", err)
	}
	mustAssert(t, store, "decision", "x", "decision", "y", "depends_on")

	got, err := store.TraceLineage(ctx, "decision", "a", 2)
	if err != nil {
		t.Fatalf("TraceLineage: %v", err)
	}
	assertTraceKeys(t, got.Nodes, []string{"decision:x", "decision:y"})

	x := findLineageNode(t, got.Nodes, "decision", "x")
	if !x.Invalidated {
		t.Error("x.Invalidated = false, want true")
	}
	if x.Depth != 1 {
		t.Errorf("x.Depth = %d, want 1", x.Depth)
	}

	y := findLineageNode(t, got.Nodes, "decision", "y")
	if y.Invalidated {
		t.Error("y.Invalidated = true, want false — the traversal must continue past the invalidated edge")
	}
	if y.Depth != 2 {
		t.Errorf("y.Depth = %d, want 2", y.Depth)
	}
}

func TestTraceLineage_FollowsSupersedeToReplacement_SameDepth(t *testing.T) {
	store := setupRelationStore(t)
	ctx := context.Background()
	upsertTestKind(t, store, "depends_on", "decision", "decision")
	upsertTestKind(t, store, "supersedes", "decision", "decision")

	// z depends_on x (the stale decision); x2 supersedes x; x2 itself
	// depends_on w, proving x2 keeps being walked upstream one depth further.
	mustAssert(t, store, "decision", "z", "decision", "x", "depends_on")
	mustAssert(t, store, "decision", "x2", "decision", "x", "supersedes")
	mustAssert(t, store, "decision", "x2", "decision", "w", "depends_on")

	got, err := store.TraceLineage(ctx, "decision", "z", 2)
	if err != nil {
		t.Fatalf("TraceLineage: %v", err)
	}
	assertTraceKeys(t, got.Nodes, []string{"decision:x", "decision:x2", "decision:w"})

	x := findLineageNode(t, got.Nodes, "decision", "x")
	if !x.Superseded {
		t.Error("x.Superseded = false, want true")
	}
	if x.Depth != 1 {
		t.Errorf("x.Depth = %d, want 1", x.Depth)
	}

	x2 := findLineageNode(t, got.Nodes, "decision", "x2")
	if x2.RelationKind != "supersedes" {
		t.Errorf("x2.RelationKind = %q, want supersedes", x2.RelationKind)
	}
	if x2.Depth != 1 {
		t.Errorf("x2.Depth = %d, want 1 — a supersede replacement is a same-depth lateral step", x2.Depth)
	}

	w := findLineageNode(t, got.Nodes, "decision", "w")
	if w.Depth != 2 {
		t.Errorf("w.Depth = %d, want 2 — x2's own upstream walk starts one depth past where x2 was found", w.Depth)
	}
}

func TestTraceLineage_AnchorSupersededCheckedFirst(t *testing.T) {
	store := setupRelationStore(t)
	ctx := context.Background()
	upsertTestKind(t, store, "depends_on", "decision", "decision")
	upsertTestKind(t, store, "supersedes", "decision", "decision")

	mustAssert(t, store, "decision", "x2", "decision", "x", "supersedes")
	mustAssert(t, store, "decision", "x2", "decision", "y", "depends_on")

	got, err := store.TraceLineage(ctx, "decision", "x", 2)
	if err != nil {
		t.Fatalf("TraceLineage: %v", err)
	}
	assertTraceKeys(t, got.Nodes, []string{"decision:x2", "decision:y"})

	x2 := findLineageNode(t, got.Nodes, "decision", "x2")
	if x2.Depth != 0 {
		t.Errorf("x2.Depth = %d, want 0 — checked before any upstream walk", x2.Depth)
	}
	y := findLineageNode(t, got.Nodes, "decision", "y")
	if y.Depth != 1 {
		t.Errorf("y.Depth = %d, want 1", y.Depth)
	}

	for _, n := range got.Nodes {
		if n.ID == "x" {
			t.Error("anchor itself must never appear as a LineageNode")
		}
	}
}

func TestTraceLineage_SameDepthTieBreak(t *testing.T) {
	store := setupRelationStore(t)
	ctx := context.Background()
	upsertTestKind(t, store, "depends_on", "decision", "decision")

	if _, err := store.MCPObserveRelation(ctx, "decision", "a", "decision", "b", "depends_on", nil, nil, nil, nil); err != nil {
		t.Fatalf("MCPObserveRelation: %v", err)
	}
	if _, err := store.MCPAssertRelation(ctx, "decision", "a", "decision", "b", "depends_on", nil, nil, nil, nil); err != nil {
		t.Fatalf("MCPAssertRelation: %v", err)
	}

	got, err := store.TraceLineage(ctx, "decision", "a", 1)
	if err != nil {
		t.Fatalf("TraceLineage: %v", err)
	}
	assertTraceKeys(t, got.Nodes, []string{"decision:b"})

	b := findLineageNode(t, got.Nodes, "decision", "b")
	if b.EdgeClass != "asserted" {
		t.Errorf("b.EdgeClass = %q, want asserted (higher trust than observed)", b.EdgeClass)
	}
}

// TestTraceLineage_MultiHopSupersedeChain proves followSupersedes resolves a
// chain of successive supersessions to the actual current replacement (x3),
// not just one hop in (x2) — the bug caught by TestTraceLineage_MixedKinds'
// coverage probe during implementation.
func TestTraceLineage_MultiHopSupersedeChain(t *testing.T) {
	store := setupRelationStore(t)
	ctx := context.Background()
	upsertTestKind(t, store, "depends_on", "decision", "decision")
	upsertTestKind(t, store, "supersedes", "decision", "decision")

	mustAssert(t, store, "decision", "z", "decision", "x", "depends_on")
	mustAssert(t, store, "decision", "x2", "decision", "x", "supersedes")
	mustAssert(t, store, "decision", "x3", "decision", "x2", "supersedes")
	mustAssert(t, store, "decision", "x3", "decision", "w", "depends_on")

	got, err := store.TraceLineage(ctx, "decision", "z", 2)
	if err != nil {
		t.Fatalf("TraceLineage: %v", err)
	}
	assertTraceKeys(t, got.Nodes, []string{"decision:x", "decision:x2", "decision:x3", "decision:w"})

	x := findLineageNode(t, got.Nodes, "decision", "x")
	if !x.Superseded {
		t.Error("x.Superseded = false, want true")
	}
	x2 := findLineageNode(t, got.Nodes, "decision", "x2")
	if !x2.Superseded {
		t.Error("x2.Superseded = false, want true — x2 was itself later superseded by x3")
	}
	if x2.Depth != 1 {
		t.Errorf("x2.Depth = %d, want 1 — same lateral step as x", x2.Depth)
	}
	x3 := findLineageNode(t, got.Nodes, "decision", "x3")
	if x3.Superseded {
		t.Error("x3.Superseded = true, want false — x3 is the current replacement, nothing supersedes it")
	}
	if x3.Depth != 1 {
		t.Errorf("x3.Depth = %d, want 1", x3.Depth)
	}
	w := findLineageNode(t, got.Nodes, "decision", "w")
	if w.Depth != 2 {
		t.Errorf("w.Depth = %d, want 2 — x3's own upstream walk starts one depth past where x3 was found", w.Depth)
	}
}

// TestTraceLineage_SupersedeChainCappedAtMaxLineageDepth proves a
// pathologically long revision history does not let followSupersedes' chain
// resolution run unbounded — it stops at MaxLineageDepth hops and reports
// Truncated, the same signal used for the main hop-depth limit.
func TestTraceLineage_SupersedeChainCappedAtMaxLineageDepth(t *testing.T) {
	store := setupRelationStore(t)
	ctx := context.Background()
	upsertTestKind(t, store, "depends_on", "decision", "decision")
	upsertTestKind(t, store, "supersedes", "decision", "decision")

	mustAssert(t, store, "decision", "z", "decision", "x0", "depends_on")

	chainLen := MaxLineageDepth + 3
	for i := 0; i < chainLen; i++ {
		older := fmt.Sprintf("x%d", i)
		newer := fmt.Sprintf("x%d", i+1)
		mustAssert(t, store, "decision", newer, "decision", older, "supersedes")
	}

	got, err := store.TraceLineage(ctx, "decision", "z", 1)
	if err != nil {
		t.Fatalf("TraceLineage: %v", err)
	}
	if !got.Truncated {
		t.Error("Truncated = false, want true — the supersede chain exceeds MaxLineageDepth")
	}

	finalID := fmt.Sprintf("x%d", chainLen)
	for _, n := range got.Nodes {
		if n.ID == finalID {
			t.Errorf("trace reached the final replacement %s despite the chain exceeding the cap", finalID)
		}
	}
}

// TestTraceLineage_FollowSupersedes_ReplacementAlreadyVisited covers
// followSupersedes' visited-dedup branch: x2 is reached both via a direct
// depends_on edge from the anchor AND as the replacement for a separately
// depends_on-reached x that x2 also supersedes. x2 must appear exactly
// once, and x's Superseded flag must still be set even though x2 itself
// was already visited by the time the supersedes edge is processed.
func TestTraceLineage_FollowSupersedes_ReplacementAlreadyVisited(t *testing.T) {
	store := setupRelationStore(t)
	ctx := context.Background()
	upsertTestKind(t, store, "depends_on", "decision", "decision")
	upsertTestKind(t, store, "supersedes", "decision", "decision")

	mustAssert(t, store, "decision", "z", "decision", "x", "depends_on")
	mustAssert(t, store, "decision", "z", "decision", "x2", "depends_on")
	mustAssert(t, store, "decision", "x2", "decision", "x", "supersedes")

	got, err := store.TraceLineage(ctx, "decision", "z", 1)
	if err != nil {
		t.Fatalf("TraceLineage: %v", err)
	}
	assertTraceKeys(t, got.Nodes, []string{"decision:x", "decision:x2"})

	x := findLineageNode(t, got.Nodes, "decision", "x")
	if !x.Superseded {
		t.Error("x.Superseded = false, want true")
	}
	x2 := findLineageNode(t, got.Nodes, "decision", "x2")
	if x2.RelationKind != "depends_on" {
		t.Errorf("x2.RelationKind = %q, want depends_on — reached directly, not via the supersedes edge", x2.RelationKind)
	}
}

// TestBatchEdgesByTypeAndIDs_EmptyKinds covers the empty-kinds guard
// directly — every real call site passes a fixed non-empty literal, so
// this exercises the guard itself: without it, an empty kinds slice would
// generate invalid SQL ("relation_kind IN ()").
func TestBatchEdgesByTypeAndIDs_EmptyKinds(t *testing.T) {
	store := setupRelationStore(t)
	got, err := store.batchEdgesByTypeAndIDs(context.Background(), true, []reachabilityNode{{"decision", "a"}}, nil)
	if err != nil {
		t.Fatalf("batchEdgesByTypeAndIDs: %v", err)
	}
	if got != nil {
		t.Errorf("got = %v, want nil", got)
	}
}

// — Batched-query error paths, isolated by call position ————————————————————

// TestTraceLineage_DBError_SupersedeInLoop covers followSupersedes' error
// branch when called from inside the main depth loop (depth >= 1), distinct
// from TestTraceLineage_DBError_Supersede which hits the depth-0 call before
// the loop even starts.
func TestTraceLineage_DBError_SupersedeInLoop(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateRelationTables(db); err != nil {
		t.Fatalf("CreateRelationTables: %v", err)
	}
	store := mockRelationStore(db)
	upsertTestKind(t, store, "depends_on", "decision", "decision")
	mustAssert(t, store, "decision", "a", "decision", "x", "depends_on")

	s2 := mockRelationStore(&failOnNthQueryDB{DB: db, n: 3})
	_, err := s2.TraceLineage(context.Background(), "decision", "a", 2)
	if err == nil {
		t.Error("want DB query error from TraceLineage, got nil")
	}
}

// TestTraceLineage_DBError_HasFurtherLineage_Source covers hasFurtherLineage's
// own source-side query failing during the depth-boundary truncation peek.
func TestTraceLineage_DBError_HasFurtherLineage_Source(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateRelationTables(db); err != nil {
		t.Fatalf("CreateRelationTables: %v", err)
	}
	store := mockRelationStore(db)
	upsertTestKind(t, store, "depends_on", "decision", "decision")
	mustAssert(t, store, "decision", "a", "decision", "x", "depends_on")

	s2 := mockRelationStore(&failOnNthQueryDB{DB: db, n: 4})
	_, err := s2.TraceLineage(context.Background(), "decision", "a", 1)
	if err == nil {
		t.Error("want DB query error from TraceLineage, got nil")
	}
}

// TestTraceLineage_DBError_HasFurtherLineage_Target covers hasFurtherLineage's
// own target-side (supersede) query failing during the same peek, isolated
// from its source-side query which must succeed first.
func TestTraceLineage_DBError_HasFurtherLineage_Target(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateRelationTables(db); err != nil {
		t.Fatalf("CreateRelationTables: %v", err)
	}
	store := mockRelationStore(db)
	upsertTestKind(t, store, "depends_on", "decision", "decision")
	mustAssert(t, store, "decision", "a", "decision", "x", "depends_on")

	s2 := mockRelationStore(&failOnNthQueryDB{DB: db, n: 5})
	_, err := s2.TraceLineage(context.Background(), "decision", "a", 1)
	if err == nil {
		t.Error("want DB query error from TraceLineage, got nil")
	}
}

// TestTraceLineage_ScanError covers collectEdges' own row-Scan error path
// inside batchEdgesByTypeAndIDs, distinct from a QueryContext-level failure.
func TestTraceLineage_ScanError(t *testing.T) {
	s := mockRelationStore(&scanMismatchDB{DB: newSQLiteDB(t)})
	_, err := s.TraceLineage(context.Background(), "decision", "d1", 1)
	if err == nil {
		t.Error("want scan error from TraceLineage, got nil")
	}
}
