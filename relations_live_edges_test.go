// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// endEdge sets invalid_at on the edge src -> tgt of kind, the way SweepStructural
// ends a relation that no longer holds.
func endEdge(t *testing.T, store *RelationStore, srcID, tgtID, kind string, at time.Time) {
	t.Helper()
	res, err := store.db.ExecContext(context.Background(),
		`UPDATE smeldr_relations SET invalid_at = $1 WHERE source_id = $2 AND target_id = $3 AND relation_kind = $4`,
		at, srcID, tgtID, kind)
	if err != nil {
		t.Fatalf("end edge %s->%s: %v", srcID, tgtID, err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		t.Fatalf("end edge %s->%s touched %d rows, want 1", srcID, tgtID, n)
	}
}

func past() time.Time   { return time.Now().UTC().Add(-time.Hour) }
func future() time.Time { return time.Now().UTC().Add(time.Hour) }

func edgeIDs(edges []RelationEdge) []string {
	out := make([]string, 0, len(edges))
	for _, e := range edges {
		out = append(out, e.SourceID+">"+e.TargetID)
	}
	return out
}

// TestGetLive_VersusAll: the same data through both pairs of getters, with and
// without a kind filter. An edge whose invalid_at lies in the future is live.
func TestGetLive_VersusAll(t *testing.T) {
	store := setupRelationStore(t)
	ctx := context.Background()
	upsertTestKind(t, store, "related_to", "post", "post")
	upsertTestKind(t, store, "cites", "post", "post")

	mustAssert(t, store, "post", "a", "post", "live", "related_to")
	mustAssert(t, store, "post", "a", "post", "ended", "related_to")
	mustAssert(t, store, "post", "a", "post", "later", "related_to")
	mustAssert(t, store, "post", "a", "post", "other-kind", "cites")
	endEdge(t, store, "a", "ended", "related_to", past())
	endEdge(t, store, "a", "later", "related_to", future())

	all, err := store.GetBySource(ctx, "post", "a", "")
	if err != nil || len(all) != 4 {
		t.Fatalf("GetBySource = %v, %v, want all 4 edges including the ended one", edgeIDs(all), err)
	}
	live, err := store.GetLiveBySource(ctx, "post", "a", "")
	if err != nil || len(live) != 3 {
		t.Fatalf("GetLiveBySource = %v, %v, want 3 (the ended edge is gone)", edgeIDs(live), err)
	}
	for _, e := range live {
		if e.TargetID == "ended" {
			t.Error("GetLiveBySource returned the ended edge")
		}
	}
	liveKind, err := store.GetLiveBySource(ctx, "post", "a", "related_to")
	if err != nil || len(liveKind) != 2 {
		t.Errorf("GetLiveBySource(kind) = %v, %v, want live and later", edgeIDs(liveKind), err)
	}

	allT, _ := store.GetByTarget(ctx, "post", "ended", "")
	liveT, err := store.GetLiveByTarget(ctx, "post", "ended", "")
	if len(allT) != 1 || len(liveT) != 0 || err != nil {
		t.Errorf("target side: all=%d live=%d err=%v, want 1 and 0", len(allT), len(liveT), err)
	}
	liveTKind, err := store.GetLiveByTarget(ctx, "post", "live", "related_to")
	if err != nil || len(liveTKind) != 1 {
		t.Errorf("GetLiveByTarget(kind) = %v, %v, want 1", edgeIDs(liveTKind), err)
	}
}

// TestGetLive_QueryError: both live getters return the database error.
func TestGetLive_QueryError(t *testing.T) {
	store := setupRelationStore(t)
	store.db = &queryFailDB{}
	if _, err := store.GetLiveBySource(context.Background(), "post", "a", ""); err == nil {
		t.Error("GetLiveBySource must return the query error")
	}
	if _, err := store.GetLiveByTarget(context.Background(), "post", "a", ""); err == nil {
		t.Error("GetLiveByTarget must return the query error")
	}
}

// TestReachability_EndedEdgeInTheMiddle is the bug: a path a -> b -> c where b -> c
// has ended. Reachability used to count c.
func TestReachability_EndedEdgeInTheMiddle(t *testing.T) {
	store := setupRelationStore(t)
	ctx := context.Background()
	upsertTestKind(t, store, "related_to", "post", "post")
	mustAssert(t, store, "post", "a", "post", "b", "related_to")
	mustAssert(t, store, "post", "b", "post", "c", "related_to")
	endEdge(t, store, "b", "c", "related_to", past())

	got, err := store.Reachability(ctx, "post", "a", "", "outgoing", 2)
	if err != nil {
		t.Fatalf("Reachability: %v", err)
	}
	assertRing(t, got.Rings[0], 1, []string{"post:b"})
	if len(got.Rings) > 1 && len(got.Rings[1].Items) != 0 {
		t.Errorf("ring 2 = %v, want empty: the b -> c relation has ended", ringKeys(got.Rings[1].Items))
	}

	// An ended first edge removes b as well.
	endEdge(t, store, "a", "b", "related_to", past())
	got, err = store.Reachability(ctx, "post", "a", "", "outgoing", 2)
	if err != nil {
		t.Fatalf("Reachability: %v", err)
	}
	for _, ring := range got.Rings {
		if len(ring.Items) != 0 {
			t.Errorf("ring %d = %v, want nothing reachable", ring.Depth, ringKeys(ring.Items))
		}
	}
}

func TestReachability_EndedEdge_AllDirections(t *testing.T) {
	store := setupRelationStore(t)
	ctx := context.Background()
	upsertTestKind(t, store, "related_to", "post", "post")
	mustAssert(t, store, "post", "in", "post", "mid", "related_to")
	mustAssert(t, store, "post", "mid", "post", "out", "related_to")
	mustAssert(t, store, "post", "later", "post", "mid", "related_to")
	endEdge(t, store, "in", "mid", "related_to", past())
	endEdge(t, store, "later", "mid", "related_to", future()) // still live

	incoming, err := store.Reachability(ctx, "post", "mid", "", "incoming", 1)
	if err != nil {
		t.Fatalf("incoming: %v", err)
	}
	assertRing(t, incoming.Rings[0], 1, []string{"post:later"})
	both, err := store.Reachability(ctx, "post", "mid", "", "both", 1)
	if err != nil {
		t.Fatalf("both: %v", err)
	}
	assertRing(t, both.Rings[0], 1, []string{"post:later", "post:out"})
}

// TestMCPPreviewImpact_OmitsEndedEdges: the preview must match what the cascade
// does, and the cascade does not notify through a relation that has ended.
func TestMCPPreviewImpact_OmitsEndedEdges(t *testing.T) {
	store := setupMCPRelations(t)
	upsertTestKind(t, store, "depends_on", "article", "governance")
	mustAssert(t, store, "article", "art-live", "governance", "gov-1", "depends_on")
	mustAssert(t, store, "article", "art-ended", "governance", "gov-1", "depends_on")
	endEdge(t, store, "art-ended", "gov-1", "depends_on", past())

	edges, err := store.MCPPreviewImpact(context.Background(), "governance", "gov-1")
	if err != nil {
		t.Fatalf("MCPPreviewImpact: %v", err)
	}
	if len(edges) != 1 || edges[0].SourceID != "art-live" {
		t.Errorf("preview = %v, want only art-live", edgeIDs(edges))
	}
}

// TestBuildCascadeHandler_SkipsEndedEdges: only the live dependent gets the
// AfterRelationCascade signal.
func TestBuildCascadeHandler_SkipsEndedEdges(t *testing.T) {
	store := setupMCPRelations(t)
	ctx := context.Background()
	upsertTestKind(t, store, "depends_on", "article", "tag")
	mustAssert(t, store, "article", "art-live", "tag", "tag-1", "depends_on")
	mustAssert(t, store, "article", "art-ended", "tag", "tag-1", "depends_on")
	endEdge(t, store, "art-ended", "tag-1", "depends_on", past())

	app := &App{}
	var liveHits, endedHits atomic.Int32
	app.OnSignal(AfterRelationCascade, func(_ context.Context, ev SignalEvent) error {
		switch ev.NodeID {
		case "art-live":
			liveHits.Add(1)
		case "art-ended":
			endedHits.Add(1)
		}
		return nil
	})
	if err := buildCascadeHandler(store, app)(ctx, SignalEvent{Type: "tag", NodeID: "tag-1"}); err != nil {
		t.Fatalf("cascade handler: %v", err)
	}
	time.Sleep(700 * time.Millisecond) // the 500 ms debouncer
	if liveHits.Load() != 1 {
		t.Errorf("the live dependent was notified %d times, want 1", liveHits.Load())
	}
	if endedHits.Load() != 0 {
		t.Errorf("the dependent behind an ended edge was notified %d times, want 0", endedHits.Load())
	}
}

// TestQueryGoalContext_OmitsEndedEdges: a goal's linked items are what it is linked
// to now.
func TestQueryGoalContext_OmitsEndedEdges(t *testing.T) {
	ctx := context.Background()
	db := newSQLiteDB(t)
	if err := CreateOrchestrationTables(db); err != nil {
		t.Fatalf("CreateOrchestrationTables: %v", err)
	}
	if err := CreateRelationTables(db); err != nil {
		t.Fatalf("CreateRelationTables: %v", err)
	}
	rs, err := NewRelationStore(db)
	if err != nil {
		t.Fatalf("NewRelationStore: %v", err)
	}
	goalNodeID := insertTestGoal(t, db, "T114", "P0", "M")
	keptID := insertTestDecision(t, db, "A198")
	droppedID := insertTestDecision(t, db, "A199")
	insertTestEdge(t, rs, "Goal", goalNodeID, "Decision", keptID, "implements")
	insertTestEdge(t, rs, "Goal", goalNodeID, "Decision", droppedID, "implements")
	endEdge(t, rs, droppedID, goalNodeID, "implements", past())

	gc, err := QueryGoalContext(ctx, db, rs, "T114")
	if err != nil {
		t.Fatalf("QueryGoalContext: %v", err)
	}
	if len(gc.LinkedDecisions) != 1 || gc.LinkedDecisions[0].DecisionNumber != "A198" {
		t.Errorf("LinkedDecisions = %+v, want only A198 (the A199 link has ended)", gc.LinkedDecisions)
	}
}

// TestBuildContextPacket_OmitsEndedRelations: an ended relation is not shown to the
// agent as a link, and the item behind it is not pulled in.
func TestBuildContextPacket_OmitsEndedRelations(t *testing.T) {
	db, rs := setupPacketDB(t)
	ctx := context.Background()
	decID := insertTestDecision(t, db, "D99")
	slug := mustSlugForID(t, db, "smeldr_decisions", decID)
	keptGoal := insertTestGoal(t, db, "T101", "P0", "S")
	droppedGoal := insertTestGoal(t, db, "T102", "P0", "S")
	insertTestEdge(t, rs, "Decision", decID, "Goal", keptGoal, "links")
	insertTestEdge(t, rs, "Decision", decID, "Goal", droppedGoal, "links")
	endEdge(t, rs, decID, droppedGoal, "links", past())

	pkt, err := BuildContextPacket(ctx, db, rs, "http://localhost", "test", "decision", slug, 1)
	if err != nil {
		t.Fatalf("BuildContextPacket: %v", err)
	}
	if len(pkt.Relations) != 1 || len(pkt.Items) != 1 {
		t.Fatalf("relations=%d items=%d, want 1 and 1 (the ended link is not shown)", len(pkt.Relations), len(pkt.Items))
	}
	if pkt.Items[0].ID != "T101" {
		t.Errorf("item = %q, want T101", pkt.Items[0].ID)
	}
}

// TestMCPGetRelations_StillReturnsEndedEdges pins the decision that the operator
// query is the history view: it returns every edge, ended ones included, each
// carrying its InvalidAt.
func TestMCPGetRelations_StillReturnsEndedEdges(t *testing.T) {
	store := setupMCPRelations(t)
	upsertTestKind(t, store, "depends_on", "article", "tag")
	mustAssert(t, store, "article", "art-1", "tag", "tag-1", "depends_on")
	endEdge(t, store, "art-1", "tag-1", "depends_on", past())

	for _, dir := range []string{"source", "target", "both"} {
		id, typ := "art-1", "article"
		if dir == "target" {
			id, typ = "tag-1", "tag"
		}
		edges, err := store.MCPGetRelations(context.Background(), typ, id, dir, "")
		if err != nil {
			t.Fatalf("MCPGetRelations %s: %v", dir, err)
		}
		if dir == "both" {
			continue // art-1 has no incoming edge: the source side alone returns it
		}
		if len(edges) != 1 || edges[0].InvalidAt == nil {
			t.Errorf("%s: %+v, want the ended edge with its InvalidAt set", dir, edges)
		}
	}
}
