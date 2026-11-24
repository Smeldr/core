package smeldr

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strings"
	"testing"
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
