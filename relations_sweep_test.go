package smeldr

import (
	"context"
	"errors"
	"testing"
)

// setupSweepStore creates a RelationStore on a fresh SQLite DB with a "linked"
// asserted kind (article → page).
func setupSweepStore(t *testing.T) *RelationStore {
	t.Helper()
	store := setupMCPRelations(t)
	upsertTestKind(t, store, "linked", "article", "page")
	return store
}

// assertEdge inserts an asserted edge from article/srcID to page/tgtID via "linked".
func assertEdge(t *testing.T, store *RelationStore, srcID, tgtID string) RelationEdge {
	t.Helper()
	ctx := context.Background()
	edge, err := store.MCPAssertRelation(ctx, "article", srcID, "page", tgtID, "linked", nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("MCPAssertRelation %s→%s: %v", srcID, tgtID, err)
	}
	return edge
}

// aliveChecker always reports the target as alive.
func aliveChecker(_ context.Context, _, _ string) (bool, error) { return true, nil }

// staleChecker always reports the target as not alive.
func staleChecker(_ context.Context, _, _ string) (bool, error) { return false, nil }

// errChecker always returns an error.
func errChecker(_ context.Context, _, _ string) (bool, error) {
	return false, errors.New("check error")
}

// noopOnStale is an onStale callback that does nothing.
func noopOnStale(_ context.Context, _ RelationEdge) {}

// — Tests ————————————————————————————————————————————————————————————————————

func TestSweepStructural_NoRelations(t *testing.T) {
	store := setupSweepStore(t)
	f, sk, err := store.SweepStructural(context.Background(), aliveChecker, noopOnStale)
	if err != nil || f != 0 || sk != 0 {
		t.Errorf("want (0,0,nil), got (%d,%d,%v)", f, sk, err)
	}
}

func TestSweepStructural_AliveTarget(t *testing.T) {
	store := setupSweepStore(t)
	edge := assertEdge(t, store, "art-1", "page-1")

	f, sk, err := store.SweepStructural(context.Background(), aliveChecker, noopOnStale)
	if err != nil || f != 0 || sk != 0 {
		t.Errorf("want (0,0,nil), got (%d,%d,%v)", f, sk, err)
	}
	// invalid_at must remain unset.
	edges, _ := store.GetBySource(context.Background(), "article", "art-1", "linked")
	if len(edges) != 1 || edges[0].ID != edge.ID || edges[0].InvalidAt != nil {
		t.Errorf("expected untouched edge, got %+v", edges)
	}
}

func TestSweepStructural_StaleTarget(t *testing.T) {
	store := setupSweepStore(t)
	assertEdge(t, store, "art-1", "page-1")

	onStaleCalls := 0
	onStale := func(_ context.Context, _ RelationEdge) { onStaleCalls++ }

	f, sk, err := store.SweepStructural(context.Background(), staleChecker, onStale)
	if err != nil {
		t.Fatalf("SweepStructural: %v", err)
	}
	if f != 1 || sk != 0 {
		t.Errorf("want (1,0), got (%d,%d)", f, sk)
	}
	if onStaleCalls != 1 {
		t.Errorf("want onStale called 1×, got %d", onStaleCalls)
	}
	// Row must now have invalid_at set.
	edges, _ := store.GetBySource(context.Background(), "article", "art-1", "linked")
	if len(edges) != 1 || edges[0].InvalidAt == nil {
		t.Errorf("expected edge with InvalidAt set, got %+v", edges)
	}
}

func TestSweepStructural_TargetDedup(t *testing.T) {
	store := setupSweepStore(t)
	// 3 different sources → same target page-1.
	assertEdge(t, store, "art-1", "page-1")
	assertEdge(t, store, "art-2", "page-1")
	assertEdge(t, store, "art-3", "page-1")

	checkCalls := 0
	check := func(_ context.Context, _, _ string) (bool, error) {
		checkCalls++
		return false, nil
	}
	onStaleCalls := 0
	onStale := func(_ context.Context, _ RelationEdge) { onStaleCalls++ }

	f, sk, err := store.SweepStructural(context.Background(), check, onStale)
	if err != nil {
		t.Fatalf("SweepStructural: %v", err)
	}
	if f != 3 || sk != 0 {
		t.Errorf("want (3,0), got (%d,%d)", f, sk)
	}
	if checkCalls != 1 {
		t.Errorf("want check called 1× (dedup), got %d", checkCalls)
	}
	if onStaleCalls != 3 {
		t.Errorf("want onStale called 3×, got %d", onStaleCalls)
	}
}

func TestSweepStructural_CheckerError(t *testing.T) {
	store := setupSweepStore(t)
	edge := assertEdge(t, store, "art-1", "page-1")

	f, sk, err := store.SweepStructural(context.Background(), errChecker, noopOnStale)
	if err != nil || f != 0 || sk != 1 {
		t.Errorf("want (0,1,nil), got (%d,%d,%v)", f, sk, err)
	}
	// Edge must be untouched (not invalidated).
	edges, _ := store.GetBySource(context.Background(), "article", "art-1", "linked")
	if len(edges) != 1 || edges[0].ID != edge.ID {
		t.Errorf("expected edge untouched, got %+v", edges)
	}
}

func TestSweepStructural_AlreadyInvalid(t *testing.T) {
	store := setupSweepStore(t)
	edge := assertEdge(t, store, "art-1", "page-1")

	// Manually invalidate the edge first.
	if err := store.Delete(context.Background(), edge.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	checkCalls := 0
	check := func(_ context.Context, _, _ string) (bool, error) {
		checkCalls++
		return false, nil
	}

	f, sk, err := store.SweepStructural(context.Background(), check, noopOnStale)
	if err != nil || f != 0 || sk != 0 {
		t.Errorf("want (0,0,nil), got (%d,%d,%v)", f, sk, err)
	}
	if checkCalls != 0 {
		t.Errorf("want check not called for already-invalid edge, got %d", checkCalls)
	}
}

func TestSweepStructural_MixedTargets(t *testing.T) {
	store := setupSweepStore(t)
	// page-alive: alive, page-stale: stale, page-err: checker error.
	assertEdge(t, store, "art-alive", "page-alive")
	assertEdge(t, store, "art-stale", "page-stale")
	assertEdge(t, store, "art-err", "page-err")

	check := func(_ context.Context, _, targetID string) (bool, error) {
		switch targetID {
		case "page-alive":
			return true, nil
		case "page-stale":
			return false, nil
		default:
			return false, errors.New("check error")
		}
	}
	onStaleCalls := 0
	onStale := func(_ context.Context, _ RelationEdge) { onStaleCalls++ }

	f, sk, err := store.SweepStructural(context.Background(), check, onStale)
	if err != nil {
		t.Fatalf("SweepStructural: %v", err)
	}
	if f != 1 || sk != 1 {
		t.Errorf("want (1,1), got (%d,%d)", f, sk)
	}
	if onStaleCalls != 1 {
		t.Errorf("want onStale 1×, got %d", onStaleCalls)
	}
}

func TestAppSweepStructural_NilStore(t *testing.T) {
	app := &App{}
	f, sk, err := app.SweepStructural(context.Background())
	if err != nil || f != 0 || sk != 0 {
		t.Errorf("want (0,0,nil) for nil store, got (%d,%d,%v)", f, sk, err)
	}
}

func TestAppSweepStructural_DefaultChecker(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateBlockTables(db); err != nil {
		t.Fatalf("CreateBlockTables: %v", err)
	}
	if err := CreateRelationTables(db); err != nil {
		t.Fatalf("CreateRelationTables: %v", err)
	}
	rs, err := NewRelationStore(db)
	if err != nil {
		t.Fatalf("NewRelationStore: %v", err)
	}

	// Register kind and assert an edge from article to a dynamic content target.
	upsertTestKind(t, rs, "linked", "article", "article")
	ctx := context.Background()
	_, err = rs.MCPAssertRelation(ctx, "article", "src-1", "article", "tgt-published", "linked", nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("MCPAssertRelation: %v", err)
	}

	// Insert a published dynamic content row for tgt-published.
	repo := &DynamicTypeRepo{db: db, typeName: "article"}
	node, err := repo.CreateDraft(ctx, map[string]any{"title": "target"})
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	// Override the generated ID to match the relation target.
	if _, err := db.ExecContext(ctx, "UPDATE smeldr_dynamic_content SET id=$1 WHERE id=$2",
		"tgt-published", node.ID); err != nil {
		t.Fatalf("UPDATE id: %v", err)
	}
	if err := repo.SetStatus(ctx, "tgt-published", Published); err != nil {
		t.Fatalf("SetStatus published: %v", err)
	}

	app := &App{cfg: Config{DB: db}}
	app.Relations(rs)

	// Published target → not stale → flagged=0.
	f, sk, err := app.SweepStructural(ctx)
	if err != nil {
		t.Fatalf("SweepStructural: %v", err)
	}
	if f != 0 || sk != 0 {
		t.Errorf("want (0,0) for published target, got (%d,%d)", f, sk)
	}

	// Now set status to draft → target no longer published → flagged=1.
	if err := repo.SetStatus(ctx, "tgt-published", Draft); err != nil {
		t.Fatalf("SetStatus draft: %v", err)
	}
	f, sk, err = app.SweepStructural(ctx)
	if err != nil {
		t.Fatalf("SweepStructural after draft: %v", err)
	}
	if f != 1 || sk != 0 {
		t.Errorf("want (1,0) for draft target, got (%d,%d)", f, sk)
	}
}
