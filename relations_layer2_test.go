package smeldr

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// setupLayer2 creates a DB-backed RelationStore and a minimal App wired with
// Relations(). The App has no HTTP server — only the signal bus and relation
// hook are exercised.
func setupLayer2(t *testing.T) (*RelationStore, *App) {
	t.Helper()
	db := newSQLiteDB(t)
	if err := CreateRelationTables(db); err != nil {
		t.Fatalf("CreateRelationTables: %v", err)
	}
	if err := CreateSchemaTable(db); err != nil {
		t.Fatalf("CreateSchemaTable: %v", err)
	}
	store, err := NewRelationStore(db)
	if err != nil {
		t.Fatalf("NewRelationStore: %v", err)
	}
	app := New(Config{BaseURL: "http://localhost", Secret: []byte("test-secret-key!!"), DB: db})
	app.Relations(store)
	return store, app
}

// fireCascade invokes the cascade handler directly by triggering it through the
// app's OnSignal handlers for AfterArchive with the given target ev.
func fireCascade(t *testing.T, app *App, targetType, targetNodeID, triggerSig string) {
	t.Helper()
	app.busMu.RLock()
	handlers := app.busHandlers[AfterArchive]
	app.busMu.RUnlock()
	ev := SignalEvent{
		Type:          targetType,
		NodeID:        targetNodeID,
		PreviousState: triggerSig,
		Timestamp:     time.Now(),
	}
	for _, h := range handlers {
		if err := h(context.Background(), ev); err != nil {
			t.Fatalf("cascade handler error: %v", err)
		}
	}
}

// collectCascadeEvents registers an OnSignal handler for AfterRelationCascade
// and returns a channel that receives each fired event.
func collectCascadeEvents(app *App) <-chan SignalEvent {
	ch := make(chan SignalEvent, 32)
	app.OnSignal(AfterRelationCascade, func(_ context.Context, ev SignalEvent) error {
		ch <- ev
		return nil
	})
	return ch
}

func TestCascadeHandler_FiresOnTargetArchive(t *testing.T) {
	store, app := setupLayer2(t)
	upsertTestKind(t, store, "depends_on", "article", "governance")

	ctx := context.Background()
	// article art-1 depends on governance gov-1
	if err := store.Assert(ctx, RelationEdge{
		SourceType: "article", SourceID: "art-1",
		TargetType: "governance", TargetID: "gov-1",
		RelationKind: "depends_on", EdgeClass: "asserted",
	}); err != nil {
		t.Fatalf("Assert: %v", err)
	}

	ch := collectCascadeEvents(app)

	fireCascade(t, app, "governance", "gov-1", string(AfterArchive))

	select {
	case ev := <-ch:
		if ev.NodeID != "art-1" {
			t.Errorf("want cascade for art-1, got NodeID=%q", ev.NodeID)
		}
		if ev.ActorID != "gov-1" {
			t.Errorf("want ActorID=gov-1, got %q", ev.ActorID)
		}
		if ev.PreviousState != string(AfterArchive) {
			t.Errorf("want PreviousState=%q, got %q", AfterArchive, ev.PreviousState)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout: AfterRelationCascade not fired")
	}
}

func TestCascadeHandler_VisitedSet(t *testing.T) {
	store, app := setupLayer2(t)
	upsertTestKind(t, store, "depends_on", "article", "governance")

	ctx := context.Background()
	// Two different articles depend on the same governance item.
	if err := store.Assert(ctx, RelationEdge{
		SourceType: "article", SourceID: "art-1",
		TargetType: "governance", TargetID: "gov-1",
		RelationKind: "depends_on", EdgeClass: "asserted",
	}); err != nil {
		t.Fatalf("Assert art-1: %v", err)
	}
	if err := store.Assert(ctx, RelationEdge{
		SourceType: "article", SourceID: "art-2",
		TargetType: "governance", TargetID: "gov-1",
		RelationKind: "depends_on", EdgeClass: "asserted",
	}); err != nil {
		t.Fatalf("Assert art-2: %v", err)
	}

	ch := collectCascadeEvents(app)

	fireCascade(t, app, "governance", "gov-1", string(AfterArchive))

	received := map[string]int{}
	deadline := time.After(2 * time.Second)
loop:
	for {
		select {
		case ev := <-ch:
			received[ev.NodeID]++
		case <-deadline:
			break loop
		}
	}

	if received["art-1"] != 1 {
		t.Errorf("art-1: want 1 cascade, got %d", received["art-1"])
	}
	if received["art-2"] != 1 {
		t.Errorf("art-2: want 1 cascade, got %d", received["art-2"])
	}
}

func TestCascadeHandler_CycleGuard(t *testing.T) {
	store, app := setupLayer2(t)
	upsertTestKind(t, store, "linked", "article", "article")

	ctx := context.Background()
	// A→B and B→A
	if err := store.Assert(ctx, RelationEdge{
		SourceType: "article", SourceID: "art-a",
		TargetType: "article", TargetID: "art-b",
		RelationKind: "linked", EdgeClass: "asserted",
	}); err != nil {
		t.Fatalf("Assert A→B: %v", err)
	}
	if err := store.Assert(ctx, RelationEdge{
		SourceType: "article", SourceID: "art-b",
		TargetType: "article", TargetID: "art-a",
		RelationKind: "linked", EdgeClass: "asserted",
	}); err != nil {
		t.Fatalf("Assert B→A: %v", err)
	}

	ch := collectCascadeEvents(app)

	// Archive A → B should be notified, A should NOT be re-notified.
	fireCascade(t, app, "article", "art-a", string(AfterArchive))

	received := map[string]int{}
	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev := <-ch:
			received[ev.NodeID]++
		case <-deadline:
			goto done
		}
	}
done:
	if received["art-b"] != 1 {
		t.Errorf("art-b: want 1 cascade, got %d", received["art-b"])
	}
	if received["art-a"] != 0 {
		t.Errorf("art-a: want 0 cascades (cycle guard), got %d", received["art-a"])
	}
}

func TestCascadeHandler_DepthOne(t *testing.T) {
	store, app := setupLayer2(t)
	upsertTestKind(t, store, "depends_on", "article", "article")

	ctx := context.Background()
	// Chain: A depends on B, B depends on C.
	if err := store.Assert(ctx, RelationEdge{
		SourceType: "article", SourceID: "art-a",
		TargetType: "article", TargetID: "art-b",
		RelationKind: "depends_on", EdgeClass: "asserted",
	}); err != nil {
		t.Fatalf("Assert A→B: %v", err)
	}
	if err := store.Assert(ctx, RelationEdge{
		SourceType: "article", SourceID: "art-b",
		TargetType: "article", TargetID: "art-c",
		RelationKind: "depends_on", EdgeClass: "asserted",
	}); err != nil {
		t.Fatalf("Assert B→C: %v", err)
	}

	ch := collectCascadeEvents(app)

	// Archive C → only B should be notified (one hop), not A.
	fireCascade(t, app, "article", "art-c", string(AfterArchive))

	received := map[string]int{}
	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev := <-ch:
			received[ev.NodeID]++
		case <-deadline:
			goto done2
		}
	}
done2:
	if received["art-b"] != 1 {
		t.Errorf("art-b: want 1 cascade, got %d", received["art-b"])
	}
	if received["art-a"] != 0 {
		t.Errorf("art-a: want 0 cascades (depth=1 guard), got %d", received["art-a"])
	}
}

func TestCascadeHandler_Idempotency(t *testing.T) {
	store, app := setupLayer2(t)
	upsertTestKind(t, store, "depends_on", "article", "governance")

	ctx := context.Background()
	if err := store.Assert(ctx, RelationEdge{
		SourceType: "article", SourceID: "art-1",
		TargetType: "governance", TargetID: "gov-1",
		RelationKind: "depends_on", EdgeClass: "asserted",
	}); err != nil {
		t.Fatalf("Assert: %v", err)
	}

	// Retrieve the edge to confirm it's in DB.
	edges, err := store.GetBySource(ctx, "article", "art-1", "")
	if err != nil || len(edges) == 0 {
		t.Fatalf("GetBySource: %v edges=%v", err, edges)
	}

	var count atomic.Int32
	app.OnSignal(AfterRelationCascade, func(_ context.Context, _ SignalEvent) error {
		count.Add(1)
		return nil
	})

	// Call the handler twice with the same edge+trigger. The visited/seen sets
	// prevent duplicate cascade signals within each individual run.
	app.busMu.RLock()
	handlers := app.busHandlers[AfterArchive]
	app.busMu.RUnlock()
	ev := SignalEvent{Type: "governance", NodeID: "gov-1", PreviousState: string(AfterArchive)}
	for _, h := range handlers {
		_ = h(ctx, ev)
		_ = h(ctx, ev) // second call — new run, but debouncer coalesces
	}

	time.Sleep(1200 * time.Millisecond) // wait for debouncer(s) to fire

	if n := count.Load(); n > 1 {
		t.Errorf("want at most 1 cascade signal (debouncer coalesces), got %d", n)
	}
}

func TestCascadeHandler_NoRelations(t *testing.T) {
	_, app := setupLayer2(t)

	var mu sync.Mutex
	fired := false
	app.OnSignal(AfterRelationCascade, func(_ context.Context, _ SignalEvent) error {
		mu.Lock()
		fired = true
		mu.Unlock()
		return nil
	})

	// Item with no incoming edges → cascade handler is a no-op.
	fireCascade(t, app, "governance", "gov-orphan", string(AfterArchive))

	time.Sleep(700 * time.Millisecond)

	mu.Lock()
	f := fired
	mu.Unlock()
	if f {
		t.Error("cascade signal fired for item with no relations")
	}
}

func TestSignalEvent_NodeIDPopulated(t *testing.T) {
	// Verify buildSignalEvent sets NodeID from Node.ID.
	n := &testPost{Node: Node{ID: "uuid-abc", Slug: "my-post"}, Title: "T"}
	meta := afterHookMeta{TypeName: "testPost", Prefix: "/posts", PrevState: "draft"}
	ctx := NewTestContext(GuestUser)
	ev := buildSignalEvent(ctx, AfterPublish, meta, n, "http://example.com")
	if ev.NodeID != "uuid-abc" {
		t.Errorf("want NodeID=uuid-abc, got %q", ev.NodeID)
	}
	if ev.Slug != "my-post" {
		t.Errorf("want Slug=my-post, got %q", ev.Slug)
	}
}
