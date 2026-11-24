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

// T1324: a move writes only from the state it validated. The hook moves the
// Task to blocked between transitionItem's read of its state (active) and its
// write (waiting-plan): the move is refused with transition.state_moved, the
// Task stays blocked, and the refused move leaves no rev bump, provenance or
// event.
func TestTransitionItem_StateMovedMeanwhile(t *testing.T) {
	app, db, _, store := setupProvenanceTransitionApp(t)
	app.EventStream()
	ch, err := app.eventBroadcaster.subscribe("w-race", eventStreamChannelAll)
	if err != nil {
		t.Fatal(err)
	}
	defer app.eventBroadcaster.unsubscribe(ch)
	repo := NewSQLRepo[*Task](db, Table("smeldr_tasks"))
	task := &Task{Node: Node{ID: "task-race-1", Slug: "task-race-1", Status: "active"}, Title: "Race", Band: "core"}
	if err := repo.Save(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	readRev := func() int {
		var rev int
		if err := db.QueryRow(`SELECT rev FROM smeldr_tasks WHERE id = 'task-race-1'`).Scan(&rev); err != nil {
			t.Fatal(err)
		}
		return rev
	}
	transitionBeforeWrite = func(typeName, id string) {
		if _, err := db.Exec(`UPDATE smeldr_tasks SET status = 'blocked', rev = rev + 1 WHERE id = $1`, id); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(func() { transitionBeforeWrite = nil })
	ctx := TrustedRead(NewTestContext(User{ID: "u-1", Roles: []Role{Admin}}))
	_, err = app.TransitionItemVia(ctx, "mcp", "Task", "task-race-1", "waiting-plan", "planning")
	transitionBeforeWrite = nil
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("move after the state moved: %v, want ErrConflict", err)
	}
	if got := ProblemTypeOf(err); got != problemTransitionStateMoved {
		t.Fatalf("problem type of %v = %q, want %s", err, got, problemTransitionStateMoved)
	}
	if v := ProblemValuesOf(err); v["from"] != "active" || v["to"] != "waiting-plan" {
		t.Errorf("values %v, want from active, to waiting-plan", v)
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM smeldr_tasks WHERE id = 'task-race-1'`).Scan(&status); err != nil || status != "blocked" {
		t.Errorf("status %q (%v), want blocked", status, err)
	}
	if rev := readRev(); rev != 1 {
		t.Errorf("rev %d, want 1 (the hook's bump only)", rev)
	}
	if got := store.Appended(); len(got) != 0 {
		t.Errorf("provenance for the refused move: %+v", got)
	}
	for _, e := range drainEvents(ch) {
		t.Errorf("an event for the refused move: %s", e)
	}
	// With nothing in between, the same move from the state it reads goes
	// through.
	if _, err := app.TransitionItemVia(ctx, "mcp", "Task", "task-race-1", "active", "unblocked"); err != nil {
		t.Fatalf("blocked to active: %v", err)
	}
}

// moveAtBeginDB runs move on the plain handle when a transaction is begun:
// after the conflict plan read its losers, before the winner's write.
type moveAtBeginDB struct {
	DB
	sqlDB *sql.DB
	move  func(*sql.DB)
}

func (m *moveAtBeginDB) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	if m.move != nil {
		m.move(m.sqlDB)
		m.move = nil
	}
	return m.sqlDB.BeginTx(ctx, opts)
}

// T1324: a loser the conflict plan read in the active state, moved by another
// call before the winner's write, stays where that call put it; the winner
// still moves, and no Error says the type has two active items.
func TestConflictSupersede_LoserMovedMeanwhile(t *testing.T) {
	app, db, _ := edgeTestApp(t, "read", signalSupersedeStates, signalSupersedeTransitions)
	insertSignal(t, db, "old", "old-slug", "read")
	insertSignal(t, db, "winner", "winner-slug", "pending")
	app.cfg.DB = &moveAtBeginDB{DB: db, sqlDB: db, move: func(d *sql.DB) {
		if _, err := d.Exec(`UPDATE smeldr_signals SET status = 'acknowledged' WHERE id = 'old'`); err != nil {
			t.Error(err)
		}
	}}
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(prev)
	if _, err := app.TransitionItemVia(TrustedRead(NewTestContext(User{ID: "u-w", Roles: []Role{Editor}})), "mcp", "Signal", "winner-slug", "read", ""); err != nil {
		t.Fatalf("winner: %v", err)
	}
	if s := signalStatusOf(t, db, "old"); s != "acknowledged" {
		t.Errorf("old = %q, want acknowledged (left where the other call put it)", s)
	}
	if s := signalStatusOf(t, db, "winner"); s != "read" {
		t.Errorf("winner = %q, want read", s)
	}
	if strings.Contains(logs.String(), "not every active item was superseded") {
		t.Errorf("an Error for a loser that left the active state: %s", logs.String())
	}
}

// T1330: a runtime-defined type's move is written only from the state it was
// checked from. The hook archives the item between the check (draft to
// published) and the write: refused with transition.state_moved, the item
// stays archived, and nothing is recorded or announced for the refused move.
func TestDynamicSetStatus_StateMovedMeanwhile(t *testing.T) {
	app, db, _, store := setupProvenanceTransitionApp(t)
	typeName, slug := defineProvenanceDynamicType(t, app, db, "race_note")
	app.EventStream()
	ch, err := app.eventBroadcaster.subscribe("w-dyn", eventStreamChannelAll)
	if err != nil {
		t.Fatal(err)
	}
	defer app.eventBroadcaster.unsubscribe(ch)
	transitionBeforeWrite = func(_, id string) {
		if _, err := db.Exec(`UPDATE smeldr_dynamic_content SET status = 'archived' WHERE id = $1`, id); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(func() { transitionBeforeWrite = nil })
	ctx := TrustedRead(NewTestContext(User{ID: "u-1", Roles: []Role{Admin}}))
	_, err = app.TransitionItemVia(ctx, "mcp", typeName, slug, "published", "")
	transitionBeforeWrite = nil
	if ProblemTypeOf(err) != problemTransitionStateMoved || !errors.Is(err, ErrConflict) {
		t.Fatalf("move after the item moved: %v, want transition.state_moved", err)
	}
	if v := ProblemValuesOf(err); v["from"] != "draft" || v["to"] != "published" || v["type"] != typeName {
		t.Errorf("values %v", v)
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM smeldr_dynamic_content WHERE slug = $1`, slug).Scan(&status); err != nil || status != "archived" {
		t.Errorf("status %q (%v), want archived", status, err)
	}
	if got := store.Appended(); len(got) != 0 {
		t.Errorf("provenance for the refused move: %+v", got)
	}
	for _, e := range drainEvents(ch) {
		t.Errorf("an event for the refused move: %s", e)
	}
}

// T1330: ScheduleContent is refused the same way, before any loser moves
// and before scheduled_at is set or the scheduler is woken.
func TestDynamicScheduleContent_StateMovedMeanwhile(t *testing.T) {
	app, db, _, store := setupProvenanceTransitionApp(t)
	typeName, slug := defineProvenanceDynamicType(t, app, db, "race_sched")
	repo, err := app.DynamicContentRepo(typeName)
	if err != nil {
		t.Fatal(err)
	}
	woke := false
	repo.scheduleWake = func() { woke = true }
	var id string
	if err := db.QueryRow(`SELECT id FROM smeldr_dynamic_content WHERE slug = $1`, slug).Scan(&id); err != nil {
		t.Fatal(err)
	}
	transitionBeforeWrite = func(_, id string) {
		if _, err := db.Exec(`UPDATE smeldr_dynamic_content SET status = 'archived' WHERE id = $1`, id); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(func() { transitionBeforeWrite = nil })
	err = repo.ScheduleContent(TrustedRead(NewTestContext(User{ID: "u-1", Roles: []Role{Admin}})), id, time.Now().Add(time.Hour))
	transitionBeforeWrite = nil
	if ProblemTypeOf(err) != problemTransitionStateMoved {
		t.Fatalf("schedule after the item moved: %v, want transition.state_moved", err)
	}
	var status string
	var at sql.NullTime
	if err := db.QueryRow(`SELECT status, scheduled_at FROM smeldr_dynamic_content WHERE id = $1`, id).Scan(&status, &at); err != nil {
		t.Fatal(err)
	}
	if status != "archived" || at.Valid {
		t.Errorf("status %q, scheduled_at %v; want archived and none", status, at)
	}
	if woke {
		t.Error("the scheduler was woken for a refused move")
	}
	if got := store.Appended(); len(got) != 0 {
		t.Errorf("provenance for the refused move: %+v", got)
	}
}

// T1330: a drained move whose item another call moved after the gate's read
// is skipped and logged, not written: the item stays where the other call put
// it, its rev is not advanced, nothing is recorded, and the queue row is
// deleted (the queued move was decided for a state the item has left).
func TestDrainEvalQueue_ItemMovedMeanwhile(t *testing.T) {
	db := newMigratedDB(t)
	if err := CreateOrchestrationTables(db); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	repo := NewSQLRepo[*Task](db, Table("smeldr_tasks"))
	if err := repo.Save(ctx, &Task{Node: Node{ID: "task-drain-1", Slug: "task-drain-1", Status: "active"}, Title: "Drain", Band: "core"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO smeldr_eval_queue (id, type_name, item_id, to_state, eval_at) VALUES ('q-1', 'Task', 'task-drain-1', 'waiting-plan', datetime('now', '-1 second'))`); err != nil {
		t.Fatal(err)
	}
	store := &fakeProvenanceStore{}
	app := &App{cfg: Config{DB: db}, provenanceStore: store, findingStore: NewFindingStore(db)}
	if err := CreateFindingTable(db); err != nil {
		t.Fatal(err)
	}
	transitionBeforeWrite = func(_, id string) {
		if _, err := db.Exec(`UPDATE smeldr_tasks SET status = 'blocked', rev = rev + 1 WHERE id = $1`, id); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(func() { transitionBeforeWrite = nil })
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(prev)
	walked, triggered, skipped, err := app.DrainEvalQueue(ctx)
	transitionBeforeWrite = nil
	if err != nil || walked != 1 || triggered != 0 || skipped != 1 {
		t.Fatalf("drain: (%d, %d, %d), %v; want (1, 0, 1)", walked, triggered, skipped, err)
	}
	var status string
	var rev, queued, findings int
	if err := db.QueryRow(`SELECT status, rev FROM smeldr_tasks WHERE id = 'task-drain-1'`).Scan(&status, &rev); err != nil {
		t.Fatal(err)
	}
	if status != "blocked" || rev != 1 {
		t.Errorf("status %q rev %d, want blocked and 1 (the hook's bump only)", status, rev)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM smeldr_eval_queue`).Scan(&queued); err != nil || queued != 0 {
		t.Errorf("queue rows %d (%v), want 0", queued, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM smeldr_findings`).Scan(&findings); err != nil || findings != 0 {
		t.Errorf("findings %d (%v), want 0", findings, err)
	}
	if got := store.Appended(); len(got) != 0 {
		t.Errorf("provenance for the skipped move: %+v", got)
	}
	for _, want := range []string{"the item moved before the drain wrote", "queued_from=active", "now=blocked", "to_state=waiting-plan"} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("log lacks %q: %s", want, logs.String())
		}
	}
}
