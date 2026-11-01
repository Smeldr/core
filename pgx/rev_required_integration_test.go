//go:build integration

package pgx

import (
	"context"
	"errors"
	"strings"
	"testing"

	smeldr "smeldr.dev/core"
)

// TestPG_TransitionItemAt_Rev: on Postgres a transition with the rev the caller
// read moves the item and advances its rev, a stale rev is a RevConflictError
// with nothing written, and a move without a rev still advances it (T1010).
func TestPG_TransitionItemAt_Rev(t *testing.T) {
	db, _ := isolatedDB(t)
	app, _ := newPGApp(t, db)
	ctx := smeldr.TrustedRead(context.Background())
	createItemsTable(t, db, "pg_rev_items")
	if _, err := db.ExecContext(context.Background(), `ALTER TABLE pg_rev_items ADD COLUMN rev INTEGER NOT NULL DEFAULT 0`); err != nil {
		t.Fatal(err)
	}
	registerType(app, "PgRevItem")
	if err := app.RegisterFlow(smeldr.StateFlow{
		Name: "pg-rev", TypeName: "PgRevItem",
		States:      []smeldr.State{{Name: "draft", IsInitial: true}, {Name: "review"}, {Name: "done"}},
		Transitions: []smeldr.Transition{{From: "draft", To: "review"}, {From: "review", To: "done"}},
	}); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
	insertItem(t, db, "pg_rev_items", "a", "draft")
	revOf := func() int {
		var r int
		if err := db.QueryRowContext(context.Background(), `SELECT rev FROM pg_rev_items WHERE id = 'a'`).Scan(&r); err != nil {
			t.Fatal(err)
		}
		return r
	}

	res, err := app.TransitionItemAt(ctx, "mcp", "PgRevItem", "a", "review", "", 0)
	if err != nil {
		t.Fatalf("with the rev it read: %v", err)
	}
	if res["rev"] != 1 || revOf() != 1 {
		t.Errorf("rev = %v (stored %d); want 1", res["rev"], revOf())
	}
	_, err = app.TransitionItemAt(ctx, "mcp", "PgRevItem", "a", "done", "", 0)
	var rc *smeldr.RevConflictError
	if !errors.As(err, &rc) || rc.Current != 1 || !strings.Contains(err.Error(), "Read it again") {
		t.Fatalf("a stale rev: err = %v; want a RevConflictError at rev 1", err)
	}
	if s := statusOf(t, db, "pg_rev_items", "a"); s != "review" {
		t.Errorf("status = %q after the stale move; want review", s)
	}
	if _, err := app.TransitionItem(ctx, "PgRevItem", "a", "done"); err != nil {
		t.Fatal(err)
	}
	if revOf() != 2 {
		t.Errorf("a move without a rev: stored rev %d; want 2", revOf())
	}
}
