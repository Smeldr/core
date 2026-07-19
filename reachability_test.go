package smeldr

import (
	"context"
	"sort"
	"testing"
)

// ringKeys returns "type:id" strings for a ring's items, sorted for
// order-independent comparison — GetBySource/GetByTarget do not guarantee
// row order, and neither does this package's traversal.
func ringKeys(items []ReachabilityItem) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Type + ":" + it.ID
	}
	sort.Strings(out)
	return out
}

func assertRing(t *testing.T, got ReachabilityRing, wantDepth int, wantKeys []string) {
	t.Helper()
	if got.Depth != wantDepth {
		t.Errorf("ring depth = %d, want %d", got.Depth, wantDepth)
	}
	gotKeys := ringKeys(got.Items)
	sort.Strings(wantKeys)
	if len(gotKeys) != len(wantKeys) {
		t.Fatalf("ring %d items = %v, want %v", wantDepth, gotKeys, wantKeys)
	}
	for i := range gotKeys {
		if gotKeys[i] != wantKeys[i] {
			t.Errorf("ring %d items = %v, want %v", wantDepth, gotKeys, wantKeys)
			break
		}
	}
}

// — Error paths ———————————————————————————————————————————————————————————

func TestReachability_EmptyAnchor(t *testing.T) {
	store := setupRelationStore(t)
	ctx := context.Background()

	if _, err := store.Reachability(ctx, "", "1", "", "outgoing", 1); err != ErrBadRequest {
		t.Errorf("empty anchorType: got %v, want ErrBadRequest", err)
	}
	if _, err := store.Reachability(ctx, "post", "", "", "outgoing", 1); err != ErrBadRequest {
		t.Errorf("empty anchorID: got %v, want ErrBadRequest", err)
	}
}

func TestReachability_InvalidDepth(t *testing.T) {
	store := setupRelationStore(t)
	ctx := context.Background()

	for _, depth := range []int{0, -1, MaxReachabilityDepth + 1} {
		if _, err := store.Reachability(ctx, "post", "p1", "", "outgoing", depth); err != ErrBadRequest {
			t.Errorf("depth=%d: got %v, want ErrBadRequest", depth, err)
		}
	}
}

func TestReachability_InvalidDirection(t *testing.T) {
	store := setupRelationStore(t)
	ctx := context.Background()

	if _, err := store.Reachability(ctx, "post", "p1", "", "sideways", 1); err != ErrBadRequest {
		t.Errorf("got %v, want ErrBadRequest", err)
	}
}

func TestReachability_DBError(t *testing.T) {
	s := mockRelationStore(&errQueryDB{})
	_, err := s.Reachability(context.Background(), "post", "p1", "", "outgoing", 1)
	if err == nil {
		t.Error("want DB query error from Reachability, got nil")
	}
}

// TestReachability_DBError_Incoming covers reachabilityNeighbors' GetByTarget
// error branch specifically — the "outgoing" case above only exercises GetBySource.
func TestReachability_DBError_Incoming(t *testing.T) {
	s := mockRelationStore(&errQueryDB{})
	_, err := s.Reachability(context.Background(), "post", "p1", "", "incoming", 1)
	if err == nil {
		t.Error("want DB query error from Reachability (incoming), got nil")
	}
}

// — Structural behaviour ——————————————————————————————————————————————————

func TestReachability_SingleRingPresent(t *testing.T) {
	store := setupRelationStore(t)
	ctx := context.Background()
	upsertTestKind(t, store, "related_to", "post", "post")

	mustAssert(t, store, "post", "p1", "post", "p2", "related_to")

	got, err := store.Reachability(ctx, "post", "p1", "", "outgoing", 1)
	if err != nil {
		t.Fatalf("Reachability: %v", err)
	}
	if len(got.Rings) != 1 {
		t.Fatalf("len(Rings) = %d, want 1", len(got.Rings))
	}
	assertRing(t, got.Rings[0], 1, []string{"post:p2"})
}

func TestReachability_SingleRingAbsent(t *testing.T) {
	store := setupRelationStore(t)
	ctx := context.Background()

	got, err := store.Reachability(ctx, "post", "p1", "", "outgoing", 1)
	if err != nil {
		t.Fatalf("Reachability: %v", err)
	}
	if len(got.Rings) != 1 {
		t.Fatalf("len(Rings) = %d, want 1", len(got.Rings))
	}
	assertRing(t, got.Rings[0], 1, nil)
}

// TestReachability_EmptyRingsContinueToMaxDepth proves that once the frontier
// dies out, every remaining requested depth is still reported as an explicit
// empty ring — absence is reported, not truncated. This is the only legal
// shape for a "gap" in BFS: an empty ring can never be followed by a
// non-empty one, since there is nothing left to expand from.
func TestReachability_EmptyRingsContinueToMaxDepth(t *testing.T) {
	store := setupRelationStore(t)
	ctx := context.Background()
	upsertTestKind(t, store, "related_to", "post", "post")

	// p1 -> p2, but p2 has no further outgoing edges: ring 1 present, rings 2-3 absent.
	mustAssert(t, store, "post", "p1", "post", "p2", "related_to")

	got, err := store.Reachability(ctx, "post", "p1", "", "outgoing", 3)
	if err != nil {
		t.Fatalf("Reachability: %v", err)
	}
	if len(got.Rings) != 3 {
		t.Fatalf("len(Rings) = %d, want 3", len(got.Rings))
	}
	assertRing(t, got.Rings[0], 1, []string{"post:p2"})
	assertRing(t, got.Rings[1], 2, nil)
	assertRing(t, got.Rings[2], 3, nil)
}

func TestReachability_MultiHopChain(t *testing.T) {
	store := setupRelationStore(t)
	ctx := context.Background()
	upsertTestKind(t, store, "related_to", "post", "post")

	// p1 -> p2 -> p3 -> p4
	mustAssert(t, store, "post", "p1", "post", "p2", "related_to")
	mustAssert(t, store, "post", "p2", "post", "p3", "related_to")
	mustAssert(t, store, "post", "p3", "post", "p4", "related_to")

	got, err := store.Reachability(ctx, "post", "p1", "", "outgoing", 3)
	if err != nil {
		t.Fatalf("Reachability: %v", err)
	}
	if len(got.Rings) != 3 {
		t.Fatalf("len(Rings) = %d, want 3", len(got.Rings))
	}
	assertRing(t, got.Rings[0], 1, []string{"post:p2"})
	assertRing(t, got.Rings[1], 2, []string{"post:p3"})
	assertRing(t, got.Rings[2], 3, []string{"post:p4"})
}

func TestReachability_KindFilter(t *testing.T) {
	store := setupRelationStore(t)
	ctx := context.Background()
	upsertTestKind(t, store, "related_to", "post", "post")
	upsertTestKind(t, store, "cites", "post", "post")

	mustAssert(t, store, "post", "p1", "post", "p2", "related_to")
	mustAssert(t, store, "post", "p1", "post", "p3", "cites")

	got, err := store.Reachability(ctx, "post", "p1", "related_to", "outgoing", 1)
	if err != nil {
		t.Fatalf("Reachability: %v", err)
	}
	assertRing(t, got.Rings[0], 1, []string{"post:p2"})
}

func TestReachability_DirectionIncoming(t *testing.T) {
	store := setupRelationStore(t)
	ctx := context.Background()
	upsertTestKind(t, store, "related_to", "post", "post")

	// p2 -> p1: an incoming-direction query anchored at p1 should find p2.
	mustAssert(t, store, "post", "p2", "post", "p1", "related_to")

	got, err := store.Reachability(ctx, "post", "p1", "", "incoming", 1)
	if err != nil {
		t.Fatalf("Reachability: %v", err)
	}
	assertRing(t, got.Rings[0], 1, []string{"post:p2"})

	// The same edge is invisible to an outgoing-direction query from p1.
	got, err = store.Reachability(ctx, "post", "p1", "", "outgoing", 1)
	if err != nil {
		t.Fatalf("Reachability: %v", err)
	}
	assertRing(t, got.Rings[0], 1, nil)
}

func TestReachability_DirectionBoth(t *testing.T) {
	store := setupRelationStore(t)
	ctx := context.Background()
	upsertTestKind(t, store, "related_to", "post", "post")

	mustAssert(t, store, "post", "p1", "post", "p2", "related_to") // outgoing
	mustAssert(t, store, "post", "p3", "post", "p1", "related_to") // incoming

	got, err := store.Reachability(ctx, "post", "p1", "", "both", 1)
	if err != nil {
		t.Fatalf("Reachability: %v", err)
	}
	assertRing(t, got.Rings[0], 1, []string{"post:p2", "post:p3"})
}

func TestReachability_CycleSafety(t *testing.T) {
	store := setupRelationStore(t)
	ctx := context.Background()
	upsertTestKind(t, store, "related_to", "post", "post")

	// p1 -> p2 -> p1: a cycle. p1 (the anchor) must never reappear in a ring.
	mustAssert(t, store, "post", "p1", "post", "p2", "related_to")
	mustAssert(t, store, "post", "p2", "post", "p1", "related_to")

	got, err := store.Reachability(ctx, "post", "p1", "", "outgoing", 3)
	if err != nil {
		t.Fatalf("Reachability: %v", err)
	}
	assertRing(t, got.Rings[0], 1, []string{"post:p2"})
	assertRing(t, got.Rings[1], 2, nil)
	assertRing(t, got.Rings[2], 3, nil)
}

// TestReachability_CrossType proves the primitive is not restricted to the 5
// orchestration anchor types BuildContextPacket hardcodes — any type name works.
func TestReachability_CrossType(t *testing.T) {
	store := setupRelationStore(t)
	ctx := context.Background()
	upsertTestKind(t, store, "illustrates", "widget", "gadget")

	mustAssert(t, store, "widget", "w1", "gadget", "g1", "illustrates")

	got, err := store.Reachability(ctx, "widget", "w1", "", "outgoing", 1)
	if err != nil {
		t.Fatalf("Reachability: %v", err)
	}
	assertRing(t, got.Rings[0], 1, []string{"gadget:g1"})
}

// mustAssert registers relKind (if not already known to the test) is assumed
// pre-registered by the caller via upsertTestKind, then asserts one edge.
func mustAssert(t *testing.T, store *RelationStore, srcType, srcID, tgtType, tgtID, relKind string) {
	t.Helper()
	if err := store.Assert(context.Background(), RelationEdge{
		SourceType: srcType, SourceID: srcID,
		TargetType: tgtType, TargetID: tgtID,
		RelationKind: relKind, EdgeClass: "asserted",
	}); err != nil {
		t.Fatalf("Assert %s %s->%s: %v", relKind, srcID, tgtID, err)
	}
}
