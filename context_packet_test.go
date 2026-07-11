// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// — helpers ——————————————————————————————————————————————————————————————————

func setupPacketDB(t *testing.T) (DB, *RelationStore) {
	t.Helper()
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
	return db, rs
}

func insertTestAmendment(t *testing.T, db DB, amendmentNumber string) string {
	t.Helper()
	a := &Amendment{
		Node:            Node{ID: NewID(), Slug: GenerateSlug("amendment-" + amendmentNumber), Status: Published},
		AmendmentNumber: amendmentNumber,
		AmendmentType:   "feat",
	}
	repo := NewSQLRepo[*Amendment](db, Table("smeldr_amendments"))
	if err := repo.Save(context.Background(), a); err != nil {
		t.Fatalf("insertTestAmendment Save: %v", err)
	}
	return a.ID
}

// insertTestSignal inserts a minimal Signal row and returns (nodeID, slug).
func insertTestSignal(t *testing.T, db DB, signalType string) (string, string) {
	t.Helper()
	s := &Signal{
		Node:       Node{ID: NewID(), Slug: GenerateSlug("signal-" + signalType), Status: Published},
		Sender:     "core",
		Receiver:   "architect",
		SignalType: signalType,
		Message:    "test",
		Sequence:   1,
	}
	repo := NewSQLRepo[*Signal](db, Table("smeldr_signals"))
	if err := repo.Save(context.Background(), s); err != nil {
		t.Fatalf("insertTestSignal Save: %v", err)
	}
	return s.ID, s.Slug
}

func insertTestEdge(t *testing.T, rs *RelationStore, srcType, srcID, tgtType, tgtID, kind string) {
	t.Helper()
	ctx := context.Background()
	if err := rs.UpsertKind(ctx, RelationKindDef{TypeName: kind, Mode: "asserted"}); err != nil {
		t.Fatalf("insertTestEdge UpsertKind %q: %v", kind, err)
	}
	if err := rs.Assert(ctx, RelationEdge{
		ID:           NewID(),
		SourceType:   srcType,
		SourceID:     srcID,
		TargetType:   tgtType,
		TargetID:     tgtID,
		RelationKind: kind,
		EdgeClass:    "asserted",
	}); err != nil {
		t.Fatalf("insertTestEdge Assert: %v", err)
	}
}

// — error paths ——————————————————————————————————————————————————————————————

func TestBuildContextPacket_errors(t *testing.T) {
	ctx := context.Background()
	const base = "http://localhost"
	const src = "test"

	t.Run("nil_db", func(t *testing.T) {
		_, err := BuildContextPacket(ctx, nil, nil, base, src, "goal", "x", 1)
		if !errors.Is(err, ErrInternal) {
			t.Errorf("nil db: got %v, want ErrInternal", err)
		}
	})

	t.Run("unknown_type", func(t *testing.T) {
		db := newSQLiteDB(t)
		_, err := BuildContextPacket(ctx, db, nil, base, src, "monkey", "x", 1)
		if !errors.Is(err, ErrBadRequest) {
			t.Errorf("unknown type: got %v, want ErrBadRequest", err)
		}
	})

	t.Run("empty_slug", func(t *testing.T) {
		db := newSQLiteDB(t)
		_, err := BuildContextPacket(ctx, db, nil, base, src, "goal", "", 1)
		if !errors.Is(err, ErrBadRequest) {
			t.Errorf("empty slug: got %v, want ErrBadRequest", err)
		}
	})

	t.Run("depth_zero", func(t *testing.T) {
		db := newSQLiteDB(t)
		_, err := BuildContextPacket(ctx, db, nil, base, src, "goal", "some-slug", 0)
		if !errors.Is(err, ErrBadRequest) {
			t.Errorf("depth 0: got %v, want ErrBadRequest", err)
		}
	})

	t.Run("depth_three", func(t *testing.T) {
		db := newSQLiteDB(t)
		_, err := BuildContextPacket(ctx, db, nil, base, src, "goal", "some-slug", 3)
		if !errors.Is(err, ErrBadRequest) {
			t.Errorf("depth 3: got %v, want ErrBadRequest", err)
		}
	})

	t.Run("anchor_db_error", func(t *testing.T) {
		// Anchor DB error: query to smeldr_goals is intercepted and fails.
		// We pass failDB as the db arg so the anchor fetch fails; rs is nil
		// so no RelationStore setup is needed.
		base2 := newSQLiteDB(t)
		if err := CreateOrchestrationTables(base2); err != nil {
			t.Fatalf("CreateOrchestrationTables: %v", err)
		}
		failDB := &govQueryFailDB{DB: base2, failOn: "smeldr_goals"}
		_, err := BuildContextPacket(ctx, failDB, nil, base, src, "goal", "any-slug", 1)
		if err == nil {
			t.Error("expected error from anchor DB failure")
		}
		if errors.Is(err, ErrInternal) || errors.Is(err, ErrBadRequest) {
			t.Errorf("expected propagated DB error, not sentinel: %v", err)
		}
	})

	t.Run("anchor_not_found", func(t *testing.T) {
		db, _ := setupPacketDB(t)
		_, err := BuildContextPacket(ctx, db, nil, base, src, "goal", "no-such-slug", 1)
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("missing anchor: got %v, want ErrNotFound", err)
		}
	})

	t.Run("getbysource_error", func(t *testing.T) {
		db, _ := setupPacketDB(t)
		goalNodeID := insertTestGoal(t, db, "T900", "P0", "S")
		slug := mustSlugForID(t, db, "smeldr_goals", goalNodeID)
		// failDB wraps the same underlying DB so relation tables already exist.
		failDB := &govQueryFailDB{DB: db, failOn: "WHERE source_type"}
		rs, err := NewRelationStore(failDB)
		if err != nil {
			t.Fatalf("NewRelationStore: %v", err)
		}
		_, err = BuildContextPacket(ctx, db, rs, base, src, "goal", slug, 1)
		if err == nil {
			t.Error("expected error from GetBySource failure")
		}
	})

	t.Run("getbytarget_error", func(t *testing.T) {
		db, _ := setupPacketDB(t)
		goalNodeID := insertTestGoal(t, db, "T901", "P0", "S")
		slug := mustSlugForID(t, db, "smeldr_goals", goalNodeID)
		// failDB wraps the same underlying DB so relation tables already exist.
		failDB := &govQueryFailDB{DB: db, failOn: "WHERE target_type"}
		rs, err := NewRelationStore(failDB)
		if err != nil {
			t.Fatalf("NewRelationStore: %v", err)
		}
		_, err = BuildContextPacket(ctx, db, rs, base, src, "goal", slug, 1)
		if err == nil {
			t.Error("expected error from GetByTarget failure")
		}
	})
}

// mustSlugForID queries slug from a named table by node id.
func mustSlugForID(t *testing.T, db DB, table, id string) string {
	t.Helper()
	rows, err := db.QueryContext(context.Background(),
		"SELECT slug FROM "+table+" WHERE id = $1", id)
	if err != nil {
		t.Fatalf("mustSlugForID query: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatalf("mustSlugForID: no row for id %s in %s", id, table)
	}
	var slug string
	if err := rows.Scan(&slug); err != nil {
		t.Fatalf("mustSlugForID scan: %v", err)
	}
	return slug
}

// — happy paths: anchor types —————————————————————————————————————————————————

func TestBuildContextPacket_goalAnchor(t *testing.T) {
	db, rs := setupPacketDB(t)
	ctx := context.Background()

	goalID := insertTestGoal(t, db, "T100", "P1", "M")
	slug := mustSlugForID(t, db, "smeldr_goals", goalID)

	pkt, err := BuildContextPacket(ctx, db, rs, "http://localhost", "test", "goal", slug, 1)
	if err != nil {
		t.Fatalf("BuildContextPacket: %v", err)
	}
	if pkt.Anchor.Type != "goal" {
		t.Errorf("anchor type = %q, want %q", pkt.Anchor.Type, "goal")
	}
	if pkt.Anchor.ID != "T100" {
		t.Errorf("anchor ID = %q, want %q", pkt.Anchor.ID, "T100")
	}
	if pkt.Anchor.URL != "http://localhost/goals/"+slug {
		t.Errorf("anchor URL = %q", pkt.Anchor.URL)
	}
	if pkt.PacketVersion != "1.0" {
		t.Errorf("packet_version = %q, want %q", pkt.PacketVersion, "1.0")
	}
	if pkt.Source.Name != "test" {
		t.Errorf("source.name = %q", pkt.Source.Name)
	}
	if pkt.Boundary.Method != "relations" {
		t.Errorf("boundary.method = %q", pkt.Boundary.Method)
	}
	if pkt.Boundary.Depth != 1 {
		t.Errorf("boundary.depth = %d, want 1", pkt.Boundary.Depth)
	}
	if len(pkt.Items) != 0 {
		t.Errorf("items len = %d, want 0", len(pkt.Items))
	}
	if len(pkt.Relations) != 0 {
		t.Errorf("relations len = %d, want 0", len(pkt.Relations))
	}
}

func TestBuildContextPacket_decisionAnchor(t *testing.T) {
	db, rs := setupPacketDB(t)
	ctx := context.Background()

	decID := insertTestDecision(t, db, "D99")
	slug := mustSlugForID(t, db, "smeldr_decisions", decID)
	goalID := insertTestGoal(t, db, "T101", "P0", "S")

	insertTestEdge(t, rs, "Decision", decID, "Goal", goalID, "links")

	pkt, err := BuildContextPacket(ctx, db, rs, "http://localhost", "test", "decision", slug, 1)
	if err != nil {
		t.Fatalf("BuildContextPacket: %v", err)
	}
	if pkt.Anchor.Type != "decision" {
		t.Errorf("anchor type = %q", pkt.Anchor.Type)
	}
	if pkt.Anchor.ID != "D99" {
		t.Errorf("anchor ID = %q", pkt.Anchor.ID)
	}
	if len(pkt.Items) != 1 {
		t.Fatalf("items len = %d, want 1", len(pkt.Items))
	}
	if pkt.Items[0].Type != "goal" {
		t.Errorf("items[0].type = %q, want goal", pkt.Items[0].Type)
	}
	if len(pkt.Relations) != 1 {
		t.Errorf("relations len = %d, want 1", len(pkt.Relations))
	}
}

func TestBuildContextPacket_amendmentAnchor(t *testing.T) {
	db, rs := setupPacketDB(t)
	ctx := context.Background()

	amdID := insertTestAmendment(t, db, "A200")
	slug := mustSlugForID(t, db, "smeldr_amendments", amdID)
	taskID := insertTestTask(t, db, "T102")

	insertTestEdge(t, rs, "Amendment", amdID, "Task", taskID, "implements")

	pkt, err := BuildContextPacket(ctx, db, rs, "http://localhost", "test", "amendment", slug, 1)
	if err != nil {
		t.Fatalf("BuildContextPacket: %v", err)
	}
	if pkt.Anchor.Type != "amendment" {
		t.Errorf("anchor type = %q", pkt.Anchor.Type)
	}
	if pkt.Anchor.ID != "A200" {
		t.Errorf("anchor ID = %q", pkt.Anchor.ID)
	}
	if len(pkt.Items) != 1 {
		t.Fatalf("items len = %d, want 1", len(pkt.Items))
	}
	if pkt.Items[0].Type != "task" {
		t.Errorf("items[0].type = %q, want task", pkt.Items[0].Type)
	}
}

func TestBuildContextPacket_taskAnchor(t *testing.T) {
	db, rs := setupPacketDB(t)
	ctx := context.Background()

	taskID := insertTestTask(t, db, "T103")
	slug := mustSlugForID(t, db, "smeldr_tasks", taskID)
	goalID := insertTestGoal(t, db, "T104", "P1", "L")
	_, sigSlug := insertTestSignal(t, db, "plan-ready")
	sigID := mustIDForSlug(t, db, "smeldr_signals", sigSlug)

	insertTestEdge(t, rs, "Task", taskID, "Goal", goalID, "tracks")
	insertTestEdge(t, rs, "Signal", sigID, "Task", taskID, "references")

	pkt, err := BuildContextPacket(ctx, db, rs, "http://localhost", "test", "task", slug, 1)
	if err != nil {
		t.Fatalf("BuildContextPacket: %v", err)
	}
	if pkt.Anchor.Type != "task" {
		t.Errorf("anchor type = %q", pkt.Anchor.Type)
	}
	if len(pkt.Items) != 2 {
		t.Errorf("items len = %d, want 2", len(pkt.Items))
	}
}

func TestBuildContextPacket_signalAnchor(t *testing.T) {
	db, rs := setupPacketDB(t)
	ctx := context.Background()

	_, slug := insertTestSignal(t, db, "commit-ready")

	pkt, err := BuildContextPacket(ctx, db, rs, "http://localhost", "test", "signal", slug, 1)
	if err != nil {
		t.Fatalf("BuildContextPacket: %v", err)
	}
	if pkt.Anchor.Type != "signal" {
		t.Errorf("anchor type = %q", pkt.Anchor.Type)
	}
	if pkt.Anchor.Slug != slug {
		t.Errorf("anchor slug = %q, want %q", pkt.Anchor.Slug, slug)
	}
	if len(pkt.Items) != 0 {
		t.Errorf("items len = %d, want 0", len(pkt.Items))
	}
}

// mustIDForSlug queries the node ID from a table by slug.
func mustIDForSlug(t *testing.T, db DB, table, slug string) string {
	t.Helper()
	rows, err := db.QueryContext(context.Background(),
		"SELECT id FROM "+table+" WHERE slug = $1", slug)
	if err != nil {
		t.Fatalf("mustIDForSlug query: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatalf("mustIDForSlug: no row for slug %s in %s", slug, table)
	}
	var id string
	if err := rows.Scan(&id); err != nil {
		t.Fatalf("mustIDForSlug scan: %v", err)
	}
	return id
}

// — nil relation store ————————————————————————————————————————————————————————

func TestBuildContextPacket_nilRelationStore(t *testing.T) {
	db, _ := setupPacketDB(t)
	ctx := context.Background()

	goalID := insertTestGoal(t, db, "T110", "P2", "S")
	slug := mustSlugForID(t, db, "smeldr_goals", goalID)

	pkt, err := BuildContextPacket(ctx, db, nil, "http://localhost", "test", "goal", slug, 2)
	if err != nil {
		t.Fatalf("BuildContextPacket nil rs: %v", err)
	}
	if len(pkt.Items) != 0 {
		t.Errorf("items len = %d, want 0 (no rs)", len(pkt.Items))
	}
	if pkt.Boundary.Depth != 2 {
		t.Errorf("boundary.depth = %d, want 2", pkt.Boundary.Depth)
	}
}

// — ghost link ————————————————————————————————————————————————————————————————

func TestBuildContextPacket_ghostLink(t *testing.T) {
	db, rs := setupPacketDB(t)
	ctx := context.Background()

	goalID := insertTestGoal(t, db, "T111", "P0", "S")
	slug := mustSlugForID(t, db, "smeldr_goals", goalID)

	// Edge to a non-existent task node — should be silently skipped.
	insertTestEdge(t, rs, "Goal", goalID, "Task", NewID(), "tracks")

	pkt, err := BuildContextPacket(ctx, db, rs, "http://localhost", "test", "goal", slug, 1)
	if err != nil {
		t.Fatalf("BuildContextPacket: %v", err)
	}
	if len(pkt.Items) != 0 {
		t.Errorf("items len = %d, want 0 (ghost skipped)", len(pkt.Items))
	}
}

// — depth=2 traversal —————————————————————————————————————————————————————————

func TestBuildContextPacket_depth2(t *testing.T) {
	db, rs := setupPacketDB(t)
	ctx := context.Background()

	// Goal → Task → Decision (depth 2 must pull in Decision)
	goalID := insertTestGoal(t, db, "T120", "P0", "M")
	taskID := insertTestTask(t, db, "T121")
	decID := insertTestDecision(t, db, "D120")
	goalSlug := mustSlugForID(t, db, "smeldr_goals", goalID)

	insertTestEdge(t, rs, "Goal", goalID, "Task", taskID, "tracks")
	insertTestEdge(t, rs, "Task", taskID, "Decision", decID, "references")

	pkt, err := BuildContextPacket(ctx, db, rs, "http://localhost", "test", "goal", goalSlug, 2)
	if err != nil {
		t.Fatalf("BuildContextPacket depth=2: %v", err)
	}
	if len(pkt.Items) != 2 {
		t.Errorf("items len = %d, want 2 (task + decision)", len(pkt.Items))
	}
}

// — per-type cap ——————————————————————————————————————————————————————————————

func TestBuildContextPacket_perTypeCap(t *testing.T) {
	db, rs := setupPacketDB(t)
	ctx := context.Background()

	anchorID := insertTestGoal(t, db, "T130", "P0", "XL")
	anchorSlug := mustSlugForID(t, db, "smeldr_goals", anchorID)

	// Insert 26 tasks — cap is 25.
	for i := range 26 {
		taskID := insertTestTask(t, db, taskIDForIndex(i))
		insertTestEdge(t, rs, "Goal", anchorID, "Task", taskID, "tracks")
	}

	pkt, err := BuildContextPacket(ctx, db, rs, "http://localhost", "test", "goal", anchorSlug, 1)
	if err != nil {
		t.Fatalf("BuildContextPacket: %v", err)
	}
	taskItems := 0
	for _, it := range pkt.Items {
		if it.Type == "task" {
			taskItems++
		}
	}
	if taskItems != 25 {
		t.Errorf("task items = %d, want 25 (cap)", taskItems)
	}
	om := pkt.Boundary.Omitted
	if om == nil {
		t.Fatal("boundary.omitted is nil, want non-nil")
	}
	if om["task"] == nil {
		t.Fatal("boundary.omitted[task] is nil")
	}
	if om["task"].Omitted != 1 {
		t.Errorf("omitted[task].omitted = %d, want 1", om["task"].Omitted)
	}
	if om["task"].Included != 25 {
		t.Errorf("omitted[task].included = %d, want 25", om["task"].Included)
	}
}

func taskIDForIndex(i int) string {
	const digits = "0123456789"
	return "TC" + string(digits[i/10%10]) + string(digits[i%10])
}

// — relations require both endpoints ——————————————————————————————————————————

func TestBuildContextPacket_relationsRequireBothEndpoints(t *testing.T) {
	db, rs := setupPacketDB(t)
	ctx := context.Background()

	// Goal → Decision (depth 1, both pulled in)
	goalID := insertTestGoal(t, db, "T140", "P0", "S")
	decID := insertTestDecision(t, db, "D140")
	ghostID := NewID() // not in DB
	goalSlug := mustSlugForID(t, db, "smeldr_goals", goalID)

	insertTestEdge(t, rs, "Goal", goalID, "Decision", decID, "uses")
	// Edge to a ghost node — endpoint not resolvable, relation must be omitted.
	insertTestEdge(t, rs, "Goal", goalID, "Task", ghostID, "tracks")

	pkt, err := BuildContextPacket(ctx, db, rs, "http://localhost", "test", "goal", goalSlug, 1)
	if err != nil {
		t.Fatalf("BuildContextPacket: %v", err)
	}
	// Decision edge should appear; ghost edge should not.
	if len(pkt.Relations) != 1 {
		t.Errorf("relations len = %d, want 1", len(pkt.Relations))
	}
	if pkt.Relations[0].Kind != "uses" {
		t.Errorf("relation kind = %q, want uses", pkt.Relations[0].Kind)
	}
}

// — diamond dedup —————————————————————————————————————————————————————————————

func TestBuildContextPacket_diamondDedup(t *testing.T) {
	db, rs := setupPacketDB(t)
	ctx := context.Background()

	// Goal → Task, Goal → Decision, Task → Decision
	// Decision is reachable via two paths — must appear exactly once in Items.
	goalID := insertTestGoal(t, db, "T150", "P0", "S")
	taskID := insertTestTask(t, db, "T151")
	decID := insertTestDecision(t, db, "D150")
	goalSlug := mustSlugForID(t, db, "smeldr_goals", goalID)

	insertTestEdge(t, rs, "Goal", goalID, "Task", taskID, "tracks")
	insertTestEdge(t, rs, "Goal", goalID, "Decision", decID, "uses")
	insertTestEdge(t, rs, "Task", taskID, "Decision", decID, "references")

	pkt, err := BuildContextPacket(ctx, db, rs, "http://localhost", "test", "goal", goalSlug, 2)
	if err != nil {
		t.Fatalf("BuildContextPacket diamond: %v", err)
	}

	seen := map[string]int{}
	for _, it := range pkt.Items {
		seen[it.Type+":"+it.ID]++
	}
	for key, count := range seen {
		if count > 1 {
			t.Errorf("item %s appears %d times (want 1)", key, count)
		}
	}
	if len(pkt.Items) != 2 {
		t.Errorf("items len = %d, want 2 (task + decision, each once)", len(pkt.Items))
	}
}

// — Published-only lifecycle contract ————————————————————————————————————————

func TestBuildContextPacket_draftAnchor(t *testing.T) {
	db, rs := setupPacketDB(t)
	ctx := context.Background()

	// Draft anchor must return ErrNotFound — same convention as every other
	// public-facing read path in this codebase.
	g := &Goal{
		Node:   Node{ID: NewID(), Slug: GenerateSlug("goal-draft"), Status: Draft},
		GoalID: "TDRAFT",
		Band:   "P0",
		Size:   "S",
	}
	repo := NewSQLRepo[*Goal](db, Table("smeldr_goals"))
	if err := repo.Save(ctx, g); err != nil {
		t.Fatalf("Save draft goal: %v", err)
	}

	_, err := BuildContextPacket(ctx, db, rs, "http://localhost", "test", "goal", g.Slug, 1)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("draft anchor: got %v, want ErrNotFound", err)
	}
}

func TestBuildContextPacket_draftLinkedItemExcluded(t *testing.T) {
	db, rs := setupPacketDB(t)
	ctx := context.Background()

	// Published anchor with one Draft linked task — task must not appear in Items.
	anchorID := insertTestGoal(t, db, "TPUB1", "P0", "S")
	anchorSlug := mustSlugForID(t, db, "smeldr_goals", anchorID)

	draftTask := &Task{
		Node:   Node{ID: NewID(), Slug: GenerateSlug("task-draftlink"), Status: Draft},
		TaskID: "TDRAFTLINK",
	}
	taskRepo := NewSQLRepo[*Task](db, Table("smeldr_tasks"))
	if err := taskRepo.Save(ctx, draftTask); err != nil {
		t.Fatalf("Save draft task: %v", err)
	}
	insertTestEdge(t, rs, "Goal", anchorID, "Task", draftTask.ID, "tracks")

	pkt, err := BuildContextPacket(ctx, db, rs, "http://localhost", "test", "goal", anchorSlug, 1)
	if err != nil {
		t.Fatalf("BuildContextPacket: %v", err)
	}
	if len(pkt.Items) != 0 {
		t.Errorf("items len = %d, want 0 (draft item silently excluded)", len(pkt.Items))
	}
	// Draft item omission is silent — Boundary.Omitted must be nil.
	if pkt.Boundary.Omitted != nil {
		t.Errorf("boundary.omitted = %v, want nil (draft exclusion is not a cap)", pkt.Boundary.Omitted)
	}
}

// — HTTP handler ——————————————————————————————————————————————————————————————

func TestContextPacketHandler_200(t *testing.T) {
	db, rs := setupPacketDB(t)
	goalID := insertTestGoal(t, db, "T160", "P0", "S")
	slug := mustSlugForID(t, db, "smeldr_goals", goalID)

	app := New(Config{
		BaseURL: "http://localhost",
		Secret:  []byte("testsecret16chars"),
		DB:      db,
	})
	app.ContextPacketHandler(rs, "unit-test")

	req := httptest.NewRequest(http.MethodGet, "/packet/goal/"+slug, nil)
	w := httptest.NewRecorder()
	app.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var pkt ContextPacket
	if err := json.NewDecoder(w.Body).Decode(&pkt); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if pkt.PacketVersion != "1.0" {
		t.Errorf("packet_version = %q, want 1.0", pkt.PacketVersion)
	}
	if pkt.Anchor.Type != "goal" {
		t.Errorf("anchor.type = %q, want goal", pkt.Anchor.Type)
	}
	if pkt.Source.Name != "unit-test" {
		t.Errorf("source.name = %q, want unit-test", pkt.Source.Name)
	}
}

func TestContextPacketHandler_400_invalidDepth(t *testing.T) {
	db, rs := setupPacketDB(t)
	goalID := insertTestGoal(t, db, "T161", "P0", "S")
	slug := mustSlugForID(t, db, "smeldr_goals", goalID)

	app := New(Config{
		BaseURL: "http://localhost",
		Secret:  []byte("testsecret16chars"),
		DB:      db,
	})
	app.ContextPacketHandler(rs, "unit-test")

	req := httptest.NewRequest(http.MethodGet, "/packet/goal/"+slug+"?depth=5", nil)
	w := httptest.NewRecorder()
	app.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestContextPacketHandler_404_unknownSlug(t *testing.T) {
	db, rs := setupPacketDB(t)

	app := New(Config{
		BaseURL: "http://localhost",
		Secret:  []byte("testsecret16chars"),
		DB:      db,
	})
	app.ContextPacketHandler(rs, "unit-test")

	req := httptest.NewRequest(http.MethodGet, "/packet/goal/no-such-slug", nil)
	w := httptest.NewRecorder()
	app.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}
