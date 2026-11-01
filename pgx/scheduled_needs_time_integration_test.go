//go:build integration

package pgx

import (
	"context"
	"errors"
	"strings"
	"testing"

	smeldr "smeldr.dev/core"
)

// TestPG_ScheduledNeedsTime: a raw move to scheduled (transition_item on a
// compiled type) is refused on Postgres when the item has no scheduled_at, or
// its table has no such column (T1009), and allowed once it has a time.
func TestPG_ScheduledNeedsTime(t *testing.T) {
	db, _ := isolatedDB(t)
	app, _ := newPGApp(t, db)
	ctx := smeldr.TrustedRead(context.Background())
	flow := func(typeName string) smeldr.StateFlow {
		return smeldr.StateFlow{
			Name: "pg-sched-" + strings.ToLower(typeName), TypeName: typeName,
			States:      []smeldr.State{{Name: "draft", IsInitial: true}, {Name: "scheduled"}, {Name: "published"}},
			Transitions: []smeldr.Transition{{From: "draft", To: "scheduled"}, {From: "scheduled", To: "published"}},
		}
	}
	wantRefused := func(err error) {
		t.Helper()
		var ve *smeldr.ValidationError
		if !errors.As(err, &ve) || !strings.Contains(err.Error(), "without a scheduled_at") {
			t.Fatalf("err = %v; want the ValidationError on ScheduledAt", err)
		}
	}

	createItemsTable(t, db, "pg_timed_items")
	if _, err := db.ExecContext(context.Background(), `ALTER TABLE pg_timed_items ADD COLUMN scheduled_at TIMESTAMPTZ`); err != nil {
		t.Fatal(err)
	}
	registerType(app, "PgTimedItem")
	if err := app.RegisterFlow(flow("PgTimedItem")); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
	insertItem(t, db, "pg_timed_items", "a", "draft")
	_, err := app.TransitionItem(ctx, "PgTimedItem", "a", "scheduled")
	wantRefused(err)
	if s := statusOf(t, db, "pg_timed_items", "a"); s != "draft" {
		t.Errorf("status = %q after the refusal, want draft", s)
	}
	if _, err := db.ExecContext(context.Background(), `UPDATE pg_timed_items SET scheduled_at = now() + interval '1 hour' WHERE id = 'a'`); err != nil {
		t.Fatal(err)
	}
	if _, err := app.TransitionItem(ctx, "PgTimedItem", "a", "scheduled"); err != nil {
		t.Fatalf("with a time: %v", err)
	}

	createItemsTable(t, db, "pg_untimed_items")
	registerType(app, "PgUntimedItem")
	if err := app.RegisterFlow(flow("PgUntimedItem")); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
	insertItem(t, db, "pg_untimed_items", "b", "draft")
	_, err = app.TransitionItem(ctx, "PgUntimedItem", "b", "scheduled")
	wantRefused(err)
}
