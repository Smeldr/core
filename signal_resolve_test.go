package smeldr

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// openSignalsOf counts the open Signals of one type about one subject.
func openSignalsOf(t *testing.T, db *sql.DB, signalType, subjectType, subjectID string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM smeldr_signals WHERE status = 'open' AND signal_type = ? AND subject_type = ? AND subject_id = ?`,
		signalType, subjectType, subjectID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// An authorization-required condition is opened once per blocked transition
// and resolved when its subject leaves from_state.
func TestResolve_AuthorizationRequired(t *testing.T) {
	app, db := signalTestApp(t)
	ctx := context.Background()
	out, err := findModule[*Task](t, app).MCPCreate(authorCtx(), map[string]any{"title": "a task"})
	if err != nil {
		t.Fatal(err)
	}
	task := asItem[*Task](out)
	for range 2 {
		if err := recordAuthorizationRequiredSignal(ctx, db, nil, nil, nil, "Task", task.ID, "backlog", "active", "approve"); err != nil {
			t.Fatalf("record: %v", err)
		}
	}
	if n := openSignalsOf(t, db, "authorization-required", "Task", task.ID); n != 1 {
		t.Fatalf("open = %d, want 1 (one per blocked transition)", n)
	}
	if _, err := app.TransitionItemVia(ctx, "mcp", "Task", task.ID, "active", ""); err != nil {
		t.Fatalf("transition: %v", err)
	}
	if n := openSignalsOf(t, db, "authorization-required", "Task", task.ID); n != 0 {
		t.Errorf("open after the move = %d, want 0", n)
	}
	var resolution, actor string
	_ = db.QueryRow(`SELECT resolution, last_actor FROM smeldr_signals WHERE signal_type = 'authorization-required'`).Scan(&resolution, &actor)
	if resolution != "Task "+task.ID+" moved backlog to active" || actor != signalResolverActor {
		t.Errorf("resolution %q actor %q", resolution, actor)
	}
	// A condition on another from_state is left open.
	if err := recordAuthorizationRequiredSignal(ctx, db, nil, nil, nil, "Task", task.ID, "done", "active", "approve"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.TransitionItemVia(ctx, "mcp", "Task", task.ID, "waiting-plan", ""); err != nil {
		t.Fatalf("transition: %v", err)
	}
	if n := openSignalsOf(t, db, "authorization-required", "Task", task.ID); n != 1 {
		t.Errorf("open = %d, want the other from_state's condition left open", n)
	}
}

// ratification-required is opened when a Decision is created in proposed or
// enters it, one at a time, and resolved when the Decision leaves proposed.
func TestResolve_RatificationRequired(t *testing.T) {
	app, db := signalTestApp(t)
	ctx := context.Background()
	out, err := findModule[*Decision](t, app).MCPCreate(authorCtx(), map[string]any{"title": "a decision"})
	if err != nil {
		t.Fatal(err)
	}
	d := asItem[*Decision](out)
	if n := openSignalsOf(t, db, "ratification-required", "Decision", d.ID); n != 1 {
		t.Fatalf("open after create = %d, want 1", n)
	}
	app.signalItemCreated(ctx, "Decision", d.ID, "proposed")
	if n := openSignalsOf(t, db, "ratification-required", "Decision", d.ID); n != 1 {
		t.Errorf("open after a second create notice = %d, want 1", n)
	}
	if _, err := app.TransitionItemVia(ctx, "mcp", "Decision", d.ID, "archived", ""); err != nil {
		t.Fatalf("archive: %v", err)
	}
	if n := openSignalsOf(t, db, "ratification-required", "Decision", d.ID); n != 0 {
		t.Errorf("open after leaving proposed = %d, want 0", n)
	}
	// Entering proposed again opens a new one, once.
	for range 2 {
		app.afterStatusChange(ctx, statusTransition{typeName: "Decision", id: d.ID, from: "pending-re-evaluation", to: "proposed"})
	}
	if n := openSignalsOf(t, db, "ratification-required", "Decision", d.ID); n != 1 {
		t.Errorf("open after re-entry = %d, want 1", n)
	}
	// Created in another state: no condition.
	app.signalItemCreated(ctx, "Decision", "d-other", "ratified")
	app.signalItemCreated(ctx, "Task", "t-1", "proposed")
	if n := countSignals(t, db, "ratification-required", "open"); n != 1 {
		t.Errorf("open total = %d, want 1", n)
	}
}

// A contradiction is resolved when its edge ends, and when a side leaves
// ratified. Asserting the edge again while it is open adds no rows.
func TestResolve_ConflictDetected(t *testing.T) {
	store := setupRelationStoreWithSignals(t)
	ctx := context.Background()
	edge, err := store.MCPAssertRelation(ctx, "Decision", "d1", "Decision", "d2", "contradicts", nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.MCPAssertRelation(ctx, "Decision", "d1", "Decision", "d2", "contradicts", nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if n := countSignals(t, store.db.(*sql.DB), "conflict-detected", "open"); n != 2 {
		t.Fatalf("open = %d, want 2", n)
	}
	if err := store.Withdraw(ctx, edge.ID, "not a contradiction"); err != nil {
		t.Fatalf("Withdraw: %v", err)
	}
	if n := countSignals(t, store.db.(*sql.DB), "conflict-detected", "resolved"); n != 2 {
		t.Errorf("resolved after withdraw = %d, want 2", n)
	}

	app, db := signalTestApp(t)
	r := app.signalResolver()
	r.emitContradiction(ctx, RelationEdge{ID: "e1", SourceType: "Decision", SourceID: "d1", TargetType: "Decision", TargetID: "d2", RelationKind: "contradicts"})
	app.afterStatusChange(ctx, statusTransition{typeName: "Decision", id: "d2", from: "ratified", to: "pending-re-evaluation"})
	if n := countSignals(t, db, "conflict-detected", "resolved"); n != 2 {
		t.Errorf("resolved after a side left ratified = %d, want 2", n)
	}
}

// The taken-URL condition: one per item, resolved by publishing or by a new
// slug.
func TestResolve_SlugCondition(t *testing.T) {
	app, db := signalTestApp(t)
	ctx := context.Background()
	for range 2 {
		if !app.signalSlugBlocked(ctx, "Post", "p1", "about") {
			t.Fatal("blocked not recorded")
		}
	}
	if n := openSignalsOf(t, db, "conflict-detected", "Post", "p1"); n != 1 {
		t.Fatalf("open = %d, want 1", n)
	}
	app.afterStatusChange(ctx, statusTransition{typeName: "Post", id: "p1", from: "scheduled", to: "published"})
	if n := openSignalsOf(t, db, "conflict-detected", "Post", "p1"); n != 0 {
		t.Errorf("open after publish = %d, want 0", n)
	}
	app.signalSlugBlocked(ctx, "Post", "p1", "about")
	app.signalSlugChanged(ctx, "Post", "p1")
	if n := openSignalsOf(t, db, "conflict-detected", "Post", "p1"); n != 0 {
		t.Errorf("open after a slug change = %d, want 0", n)
	}
	off := &App{cfg: Config{DB: db}}
	if off.signalSlugBlocked(ctx, "Post", "p2", "x") {
		t.Error("an App without Signals recorded a condition")
	}
	off.signalSlugChanged(ctx, "Post", "p2")
	off.signalItemCreated(ctx, "Decision", "d", "proposed")
	off.resolveSignalsFor(ctx, statusTransition{typeName: "Post", id: "p2", from: "a", to: "b"})
}

// recordingSink is a [signalSink] that records what a module told it.
type recordingSink struct {
	created, changed []string
	blocked          int
	recordBlock      bool
}

func (s *recordingSink) SignalCutovers() SignalCutovers { return SignalCutovers{} }
func (s *recordingSink) signalItemCreated(_ context.Context, typeName, id, status string) {
	s.created = append(s.created, typeName+"/"+status)
}
func (s *recordingSink) signalSlugBlocked(context.Context, string, string, string) bool {
	s.blocked++
	return s.recordBlock
}
func (s *recordingSink) signalSlugChanged(_ context.Context, typeName, id string) {
	s.changed = append(s.changed, id)
}

// A module tells its sink about creates, blocked schedules and slug changes;
// a recorded block is not also logged.
func TestModuleSignalSink(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	m := newWebModule(repo)
	sink := &recordingSink{recordBlock: true}
	m.setSignalSink(sink)
	seedPost(t, repo, "About", Published)
	sched := seedPost(t, repo, "About", Scheduled)
	past := time.Now().Add(-time.Minute)
	sched.ScheduledAt = &past
	if err := repo.Save(context.Background(), sched); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(prev)
	if n, _, err := m.processScheduled(hookCtx(), time.Now()); err != nil || n != 0 {
		t.Fatalf("processScheduled = %d, %v", n, err)
	}
	if sink.blocked != 1 || strings.Contains(buf.String(), "its URL is taken") {
		t.Errorf("blocked = %d, log %q; want the sink to record it and no log", sink.blocked, buf.String())
	}
	if _, err := m.MCPUpdate(hookCtx(), sched.ID, map[string]any{"slug": "about-2"}); err != nil {
		t.Fatalf("MCPUpdate: %v", err)
	}
	if len(sink.changed) != 1 || sink.changed[0] != sched.ID {
		t.Errorf("changed = %v, want the scheduled item", sink.changed)
	}
	if _, err := m.MCPCreate(hookCtx(), map[string]any{"title": "New"}); err != nil {
		t.Fatal(err)
	}
	if len(sink.created) != 1 {
		t.Errorf("created = %v, want one notice", sink.created)
	}
}

// failingSignalsDB fails every statement on smeldr_signals.
type failingSignalsDB struct{ DB }

func (d failingSignalsDB) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	if strings.Contains(q, "smeldr_signals") {
		return nil, errors.New("disk")
	}
	return d.DB.QueryContext(ctx, q, args...)
}

func (d failingSignalsDB) ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error) {
	if strings.Contains(q, "smeldr_signals") {
		return nil, errors.New("disk")
	}
	return d.DB.ExecContext(ctx, q, args...)
}

// The resolver fails open: a store failure is logged and never fails or
// panics the change that caused it.
func TestResolve_FailsOpen(t *testing.T) {
	app, db := signalTestApp(t)
	ctx := context.Background()
	app.cfg.DB = failingSignalsDB{db}
	app.resolveSignalsFor(ctx, statusTransition{typeName: "Decision", id: "d", from: "ratified", to: "proposed"})
	app.resolveSignalsFor(ctx, statusTransition{typeName: "Signal", id: "s", from: "open", to: "answered", reason: "a"})
	app.signalItemCreated(ctx, "Decision", "d", "proposed")
	if app.signalSlugBlocked(ctx, "Post", "p", "x") {
		t.Error("a failed lookup recorded the block")
	}
	app.signalSlugChanged(ctx, "Post", "p")
	app.emitRatificationRequired(ctx, "d")
	r := app.signalResolver()
	r.resolve(ctx, []openSignal{{id: "x"}}, "r")
	r.resolveContradiction(ctx, "a", "b", "r")
	r.resolveContradictionsOf(ctx, "a", "r")
	if err := recordAuthorizationRequiredSignal(ctx, app.cfg.DB, nil, nil, nil, "Task", "t", "a", "b", "approve"); err == nil {
		t.Error("recordAuthorizationRequiredSignal: want the lookup failure")
	}
	// A missing table is quiet for a contradiction check.
	signalResolver{db: newSQLiteDB(t)}.resolveContradiction(ctx, "a", "b", "r")
	// The taken-URL emit fails after a good lookup.
	app.cfg.DB = db
	if _, err := db.Exec(`CREATE TRIGGER sig_refuse BEFORE INSERT ON smeldr_signals BEGIN SELECT RAISE(ABORT, 'no'); END`); err != nil {
		t.Fatal(err)
	}
	if app.signalSlugBlocked(ctx, "Post", "p", "x") {
		t.Error("a failed insert recorded the block")
	}
}

func TestOpenSignalsOlderThan(t *testing.T) {
	app, db := signalTestApp(t)
	ctx := context.Background()
	old := insertExpiryTestSignal(t, db, "question", "open", "core", 20*24*time.Hour)
	older := insertExpiryTestSignal(t, db, "review-requested", "open", "core", 30*24*time.Hour)
	insertExpiryTestSignal(t, db, "question", "open", "core", time.Hour)
	insertExpiryTestSignal(t, db, "notice", "pending", "core", 30*24*time.Hour)
	got, err := app.OpenSignalsOlderThan(ctx, 0, 0)
	if err != nil || len(got) != 2 || got[0].ID != older || got[1].ID != old {
		t.Fatalf("OpenSignalsOlderThan = %+v, %v; want the two old open ones, oldest first", got, err)
	}
	if got, _ := app.OpenSignalsOlderThan(ctx, time.Minute, 1); len(got) != 1 || got[0].ID != older {
		t.Errorf("limit 1 = %+v", got)
	}
	if got, _ := app.OpenSignalsOlderThan(ctx, time.Minute, 1000); len(got) != 3 {
		t.Errorf("a limit over 500 = %d signals, want 3", len(got))
	}
	if got, err := app.OpenSignalsOlderThan(WithReadScope(ctx, &ReadScope{}), 0, 0); err != nil || len(got) != 0 {
		t.Errorf("a scope that reads nothing = %+v, %v", got, err)
	}
	if got, err := (&App{}).OpenSignalsOlderThan(ctx, 0, 0); got != nil || err != nil {
		t.Errorf("no DB = %v, %v", got, err)
	}
	if got, err := (&App{cfg: Config{DB: newSQLiteDB(t)}}).OpenSignalsOlderThan(ctx, 0, 0); got != nil || err != nil {
		t.Errorf("no table = %v, %v", got, err)
	}
	app.cfg.DB = failingSignalsDB{db}
	if _, err := app.OpenSignalsOlderThan(ctx, 0, 0); !errors.Is(err, ErrInternal) {
		t.Errorf("store failure: %v, want ErrInternal", err)
	}
}

// The resolver finds a contradiction's rows by their message, and rows written
// before D115 carry this exact text: the wording must not change.
func TestContradictsMessage_Wording(t *testing.T) {
	if got := contradictsMessage("D12"); got != "D12 contradicts a Decision" {
		t.Errorf("contradictsMessage = %q; it names only its own Decision (T928)", got)
	}
}
