package smeldr

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"
)

// setupLayer1Store creates a DB with both relation and schema tables ready,
// returning a configured RelationStore and SchemaStore.
func setupLayer1Store(t *testing.T) (*RelationStore, *SchemaStore) {
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
	ss := NewSchemaStore(db)
	return store, ss
}

func upsertTestKind(t *testing.T, store *RelationStore, typeName, sourceType, targetType string) {
	t.Helper()
	pairs, _ := json.Marshal([]map[string]string{
		{"source_type": sourceType, "target_type": targetType},
	})
	err := store.UpsertKind(context.Background(), RelationKindDef{
		TypeName:    typeName,
		Mode:        "asserted",
		Directional: true,
		TypePairs:   json.RawMessage(pairs),
	})
	if err != nil {
		t.Fatalf("UpsertKind %q: %v", typeName, err)
	}
}

func countEdges(t *testing.T, store *RelationStore, sourceType, sourceID string) int {
	t.Helper()
	edges, err := store.GetBySource(context.Background(), sourceType, sourceID, "")
	if err != nil {
		t.Fatalf("GetBySource: %v", err)
	}
	return len(edges)
}

func TestRecomputeAsserted_Insert(t *testing.T) {
	store, _ := setupLayer1Store(t)
	upsertTestKind(t, store, "tagged", "article", "tag")

	ctx := context.Background()
	incoming := []RelationEdge{
		{TargetType: "tag", TargetID: "tag-1", RelationKind: "tagged"},
	}
	if err := store.RecomputeAsserted(ctx, "article", "art-1", incoming); err != nil {
		t.Fatalf("RecomputeAsserted: %v", err)
	}

	edges, err := store.GetBySource(ctx, "article", "art-1", "")
	if err != nil {
		t.Fatalf("GetBySource: %v", err)
	}
	if len(edges) != 1 {
		t.Fatalf("want 1 edge, got %d", len(edges))
	}
	if edges[0].TargetID != "tag-1" || edges[0].EdgeClass != "asserted" {
		t.Errorf("unexpected edge: %+v", edges[0])
	}
}

func TestRecomputeAsserted_NoOp(t *testing.T) {
	store, _ := setupLayer1Store(t)
	upsertTestKind(t, store, "tagged", "article", "tag")

	ctx := context.Background()
	incoming := []RelationEdge{
		{TargetType: "tag", TargetID: "tag-1", RelationKind: "tagged"},
	}
	if err := store.RecomputeAsserted(ctx, "article", "art-1", incoming); err != nil {
		t.Fatalf("first RecomputeAsserted: %v", err)
	}
	if err := store.RecomputeAsserted(ctx, "article", "art-1", incoming); err != nil {
		t.Fatalf("second RecomputeAsserted: %v", err)
	}

	if n := countEdges(t, store, "article", "art-1"); n != 1 {
		t.Fatalf("want 1 edge after no-op, got %d", n)
	}
}

func TestRecomputeAsserted_Delete(t *testing.T) {
	store, _ := setupLayer1Store(t)
	upsertTestKind(t, store, "tagged", "article", "tag")

	ctx := context.Background()
	incoming := []RelationEdge{
		{TargetType: "tag", TargetID: "tag-1", RelationKind: "tagged"},
		{TargetType: "tag", TargetID: "tag-2", RelationKind: "tagged"},
	}
	if err := store.RecomputeAsserted(ctx, "article", "art-1", incoming); err != nil {
		t.Fatalf("initial RecomputeAsserted: %v", err)
	}

	// Remove tag-2 from incoming.
	reduced := []RelationEdge{
		{TargetType: "tag", TargetID: "tag-1", RelationKind: "tagged"},
	}
	if err := store.RecomputeAsserted(ctx, "article", "art-1", reduced); err != nil {
		t.Fatalf("reduced RecomputeAsserted: %v", err)
	}

	edges, err := store.GetBySource(ctx, "article", "art-1", "")
	if err != nil {
		t.Fatalf("GetBySource: %v", err)
	}
	if len(edges) != 1 {
		t.Fatalf("want 1 edge after delete, got %d", len(edges))
	}
	if edges[0].TargetID != "tag-1" {
		t.Errorf("wrong edge survived: %+v", edges[0])
	}
}

func TestRecomputeAsserted_EmptyIncoming(t *testing.T) {
	store, _ := setupLayer1Store(t)
	upsertTestKind(t, store, "tagged", "article", "tag")

	ctx := context.Background()
	if err := store.RecomputeAsserted(ctx, "article", "art-1", []RelationEdge{
		{TargetType: "tag", TargetID: "tag-1", RelationKind: "tagged"},
	}); err != nil {
		t.Fatalf("initial RecomputeAsserted: %v", err)
	}

	if err := store.RecomputeAsserted(ctx, "article", "art-1", nil); err != nil {
		t.Fatalf("empty RecomputeAsserted: %v", err)
	}
	if n := countEdges(t, store, "article", "art-1"); n != 0 {
		t.Fatalf("want 0 edges after empty incoming, got %d", n)
	}
}

func TestBulkRecompute_Basic(t *testing.T) {
	store, _ := setupLayer1Store(t)
	upsertTestKind(t, store, "tagged", "article", "tag")

	ctx := context.Background()
	items := []RelationSource{
		{
			SourceType: "article",
			SourceID:   "art-1",
			Incoming:   []RelationEdge{{TargetType: "tag", TargetID: "tag-a", RelationKind: "tagged"}},
		},
		{
			SourceType: "article",
			SourceID:   "art-2",
			Incoming:   []RelationEdge{{TargetType: "tag", TargetID: "tag-b", RelationKind: "tagged"}},
		},
	}
	if err := store.BulkRecompute(ctx, items); err != nil {
		t.Fatalf("BulkRecompute: %v", err)
	}

	if n := countEdges(t, store, "article", "art-1"); n != 1 {
		t.Fatalf("art-1: want 1 edge, got %d", n)
	}
	if n := countEdges(t, store, "article", "art-2"); n != 1 {
		t.Fatalf("art-2: want 1 edge, got %d", n)
	}
}

func TestNewRelationStore_LoadsExistingRows(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateRelationTables(db); err != nil {
		t.Fatalf("CreateRelationTables: %v", err)
	}
	// Seed a kind before constructing the store so loadRegistry scans rows.
	s1, err := NewRelationStore(db)
	if err != nil {
		t.Fatalf("first NewRelationStore: %v", err)
	}
	if err := s1.UpsertKind(context.Background(), RelationKindDef{TypeName: "authored_by", Mode: "asserted"}); err != nil {
		t.Fatalf("UpsertKind: %v", err)
	}

	// Second store construction hits loadRegistry with a non-empty table,
	// exercising scanRelationKind.
	s2, err := NewRelationStore(db)
	if err != nil {
		t.Fatalf("second NewRelationStore: %v", err)
	}
	if _, ok := s2.GetKind("authored_by"); !ok {
		t.Error("kind not loaded from DB on second NewRelationStore")
	}
}

func TestAppRelationsHook_CompiledType_NoOp(t *testing.T) {
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

	ctx := context.Background()
	// Compiled type — no schema in smeldr_content_type_schemas → ErrNotFound path.
	if err := app.syncSaveHook(ctx, "post", "post-1", struct{}{}); err != nil {
		t.Fatalf("hook on unknown type must be no-op, got: %v", err)
	}
}

func TestAppRelationsHook_DynamicType_CreatesEdge(t *testing.T) {
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

	ctx := context.Background()
	upsertTestKind(t, store, "references", "article", "source")

	ss := NewSchemaStore(db)
	fields, _ := json.Marshal([]SchemaField{
		{Name: "references", Type: "string", Relation: "edge"},
	})
	if err := ss.Save(ctx, &ContentTypeSchema{
		TypeName: "article", Kind: "content", Fields: json.RawMessage(fields),
	}); err != nil {
		t.Fatalf("ss.Save: %v", err)
	}

	itemFields, _ := json.Marshal(map[string]any{"references": "src-99"})
	dn := &DynamicNode{Node: Node{ID: "art-200"}, TypeName: "article", Fields: json.RawMessage(itemFields)}

	if err := app.syncSaveHook(ctx, "article", "art-200", dn); err != nil {
		t.Fatalf("hook: %v", err)
	}

	edges, err := store.GetBySource(ctx, "article", "art-200", "")
	if err != nil {
		t.Fatalf("GetBySource: %v", err)
	}
	if len(edges) != 1 || edges[0].TargetID != "src-99" {
		t.Errorf("unexpected edges: %+v", edges)
	}
}

func TestExtractRelationEdges_NonDynamicNode(t *testing.T) {
	store, _ := setupLayer1Store(t)
	// Non-DynamicNode item → nil slice (no-op).
	out := extractRelationEdges("post", "p1", []SchemaField{{Name: "x", Relation: "edge"}}, struct{}{}, store)
	if out != nil {
		t.Errorf("expected nil for non-DynamicNode, got %v", out)
	}
}

func TestExtractRelationEdges_EmptyFields(t *testing.T) {
	store, _ := setupLayer1Store(t)
	dn := &DynamicNode{Node: Node{ID: "n1"}, Fields: nil}
	out := extractRelationEdges("t", "n1", []SchemaField{{Name: "x", Relation: "edge"}}, dn, store)
	if out != nil {
		t.Errorf("expected nil for empty Fields, got %v", out)
	}
}

func TestExtractRelationEdges_KindNotRegistered(t *testing.T) {
	store, _ := setupLayer1Store(t)
	f, _ := json.Marshal(map[string]any{"unregistered_kind": "target-1"})
	dn := &DynamicNode{Node: Node{ID: "n1"}, Fields: json.RawMessage(f)}
	out := extractRelationEdges("t", "n1", []SchemaField{{Name: "unregistered_kind", Relation: "edge"}}, dn, store)
	if len(out) != 0 {
		t.Errorf("expected empty slice for unregistered kind, got %v", out)
	}
}

func TestExtractRelationEdges_EmptyTypePairs(t *testing.T) {
	store, _ := setupLayer1Store(t)
	// Kind registered but TypePairs is empty array.
	if err := store.UpsertKind(context.Background(), RelationKindDef{
		TypeName: "no_pairs", Mode: "asserted", TypePairs: json.RawMessage("[]"),
	}); err != nil {
		t.Fatalf("UpsertKind: %v", err)
	}
	f, _ := json.Marshal(map[string]any{"no_pairs": "target-1"})
	dn := &DynamicNode{Node: Node{ID: "n1"}, Fields: json.RawMessage(f)}
	out := extractRelationEdges("t", "n1", []SchemaField{{Name: "no_pairs", Relation: "edge"}}, dn, store)
	if len(out) != 0 {
		t.Errorf("expected empty slice for empty TypePairs, got %v", out)
	}
}

func TestAssert_OptionalFields_RoundTrip(t *testing.T) {
	store := setupRelationStore(t)
	if err := store.UpsertKind(context.Background(), RelationKindDef{TypeName: "co_authored", Mode: "asserted"}); err != nil {
		t.Fatalf("UpsertKind: %v", err)
	}

	ctx := context.Background()
	conf := 0.9
	now := time.Now().UTC().Truncate(time.Second)
	job := "import-job-1"
	edge := RelationEdge{
		SourceType:   "doc",
		SourceID:     "d1",
		TargetType:   "author",
		TargetID:     "a1",
		RelationKind: "co_authored",
		EdgeClass:    "asserted",
		Confidence:   &conf,
		ValidAt:      &now,
		CreatedByJob: &job,
	}
	if err := store.Assert(ctx, edge); err != nil {
		t.Fatalf("Assert: %v", err)
	}

	edges, err := store.GetBySource(ctx, "doc", "d1", "")
	if err != nil {
		t.Fatalf("GetBySource: %v", err)
	}
	if len(edges) != 1 {
		t.Fatalf("want 1 edge, got %d", len(edges))
	}
	got := edges[0]
	if got.Confidence == nil || *got.Confidence != conf {
		t.Errorf("Confidence: want %v, got %v", conf, got.Confidence)
	}
	if got.ValidAt == nil || !got.ValidAt.Equal(now) {
		t.Errorf("ValidAt: want %v, got %v", now, got.ValidAt)
	}
	if got.CreatedByJob == nil || *got.CreatedByJob != job {
		t.Errorf("CreatedByJob: want %q, got %v", job, got.CreatedByJob)
	}
}

func TestSyncSaveHook_FiredOnCreateHandler(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	m := NewModule((*testPost)(nil), Repo(repo), At("/posts"))

	var hookCalled bool
	m.setSyncSaveHook(func(_ context.Context, typeName, id string, _ any) error {
		hookCalled = true
		if typeName != "testPost" {
			t.Errorf("hook typeName = %q, want testPost", typeName)
		}
		return nil
	})

	body, _ := json.Marshal(map[string]string{"Title": "Hook Post"})
	w := httptest.NewRecorder()
	r := withUser(
		httptest.NewRequest("POST", "/posts", bytes.NewReader(body)),
		authorUser(),
	)
	m.createHandler(w, r)

	if w.Code != 201 {
		t.Fatalf("createHandler status = %d; body: %s", w.Code, w.Body.String())
	}
	if !hookCalled {
		t.Error("syncSaveHook was not called by createHandler")
	}
}

func TestSyncSaveHook_FiredOnUpdateHandler(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	m := NewModule((*testPost)(nil), Repo(repo), At("/posts"))

	// Seed an item.
	ctx := context.Background()
	seed := &testPost{Node: Node{ID: NewID(), Slug: "hook-post"}, Title: "Original"}
	if err := repo.Save(ctx, seed); err != nil {
		t.Fatalf("seed save: %v", err)
	}

	var hookCalled bool
	m.setSyncSaveHook(func(_ context.Context, _, _ string, _ any) error {
		hookCalled = true
		return nil
	})

	body, _ := json.Marshal(map[string]string{"Title": "Updated"})
	w := httptest.NewRecorder()
	r := withUser(
		httptest.NewRequest("PUT", "/posts/hook-post", bytes.NewReader(body)),
		authorUser(),
	)
	r.SetPathValue("slug", "hook-post")
	m.updateHandler(w, r)

	if w.Code != 200 {
		t.Fatalf("updateHandler status = %d; body: %s", w.Code, w.Body.String())
	}
	if !hookCalled {
		t.Error("syncSaveHook was not called by updateHandler")
	}
}

func TestSyncSaveHook_FiredOnMCPCreate(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	m := NewModule((*testPost)(nil), Repo(repo), At("/posts"))

	var hookCalled bool
	m.setSyncSaveHook(func(_ context.Context, _, _ string, _ any) error {
		hookCalled = true
		return nil
	})

	ctx := NewBackgroundContext("localhost")
	_, err := m.MCPCreate(ctx, map[string]any{"Title": "MCP Post"})
	if err != nil {
		t.Fatalf("MCPCreate: %v", err)
	}
	if !hookCalled {
		t.Error("syncSaveHook was not called by MCPCreate")
	}
}

func TestSyncSaveHook_FiredOnMCPUpdate(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	m := NewModule((*testPost)(nil), Repo(repo), At("/posts"))

	ctx := NewBackgroundContext("localhost")
	_, err := m.MCPCreate(ctx, map[string]any{"Title": "Original MCP"})
	if err != nil {
		t.Fatalf("MCPCreate seed: %v", err)
	}

	var hookCalled bool
	m.setSyncSaveHook(func(_ context.Context, _, _ string, _ any) error {
		hookCalled = true
		return nil
	})

	items, _ := repo.FindAll(ctx, ListOptions{})
	if len(items) == 0 {
		t.Fatal("no items in repo")
	}
	_, err = m.MCPUpdate(ctx, items[0].Slug, map[string]any{"Title": "Updated MCP"})
	if err != nil {
		t.Fatalf("MCPUpdate: %v", err)
	}
	if !hookCalled {
		t.Error("syncSaveHook was not called by MCPUpdate")
	}
}

func TestGetByTarget_AllKinds(t *testing.T) {
	store := setupRelationStore(t)
	ctx := context.Background()
	if err := store.UpsertKind(ctx, RelationKindDef{TypeName: "cited_by", Mode: "asserted"}); err != nil {
		t.Fatalf("UpsertKind: %v", err)
	}
	if err := store.Assert(ctx, RelationEdge{
		SourceType: "article", SourceID: "a1",
		TargetType: "ref", TargetID: "r1",
		RelationKind: "cited_by", EdgeClass: "asserted",
	}); err != nil {
		t.Fatalf("Assert: %v", err)
	}

	// kind="" covers the all-kinds query path in GetByTarget.
	edges, err := store.GetByTarget(ctx, "ref", "r1", "")
	if err != nil {
		t.Fatalf("GetByTarget (all kinds): %v", err)
	}
	if len(edges) != 1 {
		t.Errorf("want 1 edge, got %d", len(edges))
	}
}

func TestBulkRecompute_NoOpSecondCall(t *testing.T) {
	store, _ := setupLayer1Store(t)
	upsertTestKind(t, store, "tagged", "article", "tag")

	ctx := context.Background()
	items := []RelationSource{{
		SourceType: "article",
		SourceID:   "art-noop",
		Incoming:   []RelationEdge{{TargetType: "tag", TargetID: "tag-x", RelationKind: "tagged"}},
	}}
	if err := store.BulkRecompute(ctx, items); err != nil {
		t.Fatalf("first BulkRecompute: %v", err)
	}
	// Second call: diff is empty → diffs slice stays empty → apply loop skipped.
	if err := store.BulkRecompute(ctx, items); err != nil {
		t.Fatalf("second BulkRecompute (no-op): %v", err)
	}
	if n := countEdges(t, store, "article", "art-noop"); n != 1 {
		t.Fatalf("want 1 edge after no-op BulkRecompute, got %d", n)
	}
}

func TestRecomputeAsserted_EdgeWithPresetID(t *testing.T) {
	store, _ := setupLayer1Store(t)
	upsertTestKind(t, store, "tagged", "article", "tag")

	ctx := context.Background()
	// Edge with pre-set ID and non-zero CreatedAt covers those branches in applyRelationDiff.
	now := time.Now().UTC()
	incoming := []RelationEdge{{
		ID:           "preset-id-1",
		TargetType:   "tag",
		TargetID:     "tag-preset",
		RelationKind: "tagged",
		CreatedAt:    now,
	}}
	if err := store.RecomputeAsserted(ctx, "article", "art-preset", incoming); err != nil {
		t.Fatalf("RecomputeAsserted: %v", err)
	}

	edges, err := store.GetBySource(ctx, "article", "art-preset", "")
	if err != nil {
		t.Fatalf("GetBySource: %v", err)
	}
	if len(edges) != 1 || edges[0].ID != "preset-id-1" {
		t.Errorf("unexpected edges: %+v", edges)
	}
}

func TestLayer1_Integration(t *testing.T) {
	store, ss := setupLayer1Store(t)
	ctx := context.Background()

	const typeName = "article"
	const kindName = "references"
	const targetType = "source"

	// Register relation kind.
	upsertTestKind(t, store, kindName, typeName, targetType)

	// Register schema with a Relation="edge" field named after the kind.
	fields, _ := json.Marshal([]SchemaField{
		{Name: "title", Type: "string"},
		{Name: kindName, Type: "string", Relation: "edge"},
	})
	if err := ss.Save(ctx, &ContentTypeSchema{
		TypeName: typeName,
		Kind:     "content",
		Fields:   json.RawMessage(fields),
	}); err != nil {
		t.Fatalf("ss.Save: %v", err)
	}

	// Build the hook closure (mirrors App.Relations logic).
	hook := func(ctx context.Context, tn, id string, item any) error {
		schema, err := ss.FindByTypeName(ctx, tn)
		if err != nil {
			return err
		}
		f, err := schema.ParseFields()
		if err != nil {
			return err
		}
		incoming := extractRelationEdges(tn, id, f, item, store)
		return store.RecomputeAsserted(ctx, tn, id, incoming)
	}

	// Simulate saving a DynamicNode whose Fields include the relation field.
	itemFields, _ := json.Marshal(map[string]any{
		"title":  "My Article",
		kindName: "source-id-42",
	})
	dn := &DynamicNode{
		Node:     Node{ID: "art-100"},
		TypeName: typeName,
		Fields:   json.RawMessage(itemFields),
	}

	// Fire the hook (replaces explicit Assert call).
	if err := hook(ctx, typeName, dn.ID, dn); err != nil {
		t.Fatalf("hook: %v", err)
	}

	// Verify the edge was created without an explicit Assert call.
	edges, err := store.GetBySource(ctx, typeName, "art-100", "")
	if err != nil {
		t.Fatalf("GetBySource: %v", err)
	}
	if len(edges) != 1 {
		t.Fatalf("want 1 edge, got %d", len(edges))
	}
	e := edges[0]
	if e.TargetID != "source-id-42" || e.TargetType != targetType || e.RelationKind != kindName {
		t.Errorf("unexpected edge: %+v", e)
	}
	if e.EdgeClass != "asserted" {
		t.Errorf("want edge_class=asserted, got %q", e.EdgeClass)
	}
}
