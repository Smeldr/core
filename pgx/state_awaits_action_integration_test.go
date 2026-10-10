//go:build integration

package pgx

import (
	"context"
	"testing"

	smeldr "smeldr.dev/core"
)

// TestPG_StateAwaitsAction: D109's flag on Postgres. The column exists, the
// orchestration flows store it, a re-registration flips it on an existing row,
// and ValidTransitions reports it for each target.
func TestPG_StateAwaitsAction(t *testing.T) {
	db, _ := isolatedDB(t)
	app, _ := newPGApp(t, db)
	ctx := context.Background()
	if err := smeldr.CreateOrchestrationTables(db); err != nil {
		t.Fatalf("CreateOrchestrationTables: %v", err)
	}
	if err := smeldr.EnsureStateAwaitsActionColumn(ctx, db); err != nil {
		t.Fatalf("EnsureStateAwaitsActionColumn: %v", err)
	}
	smeldr.RegisterOrchestrationTypes(app, db)

	open, found, err := smeldr.StatesAwaitingAction(ctx, db, "Task")
	if err != nil || !found || len(open) != 7 || !open["blocked"] || open["done"] {
		t.Errorf("Task open states = %v, %v, %v; want D109's seven", open, found, err)
	}
	if dec, _, _ := smeldr.StatesAwaitingAction(ctx, db, "Decision"); dec["ratified"] || !dec["pending-re-evaluation"] {
		t.Errorf("Decision open states = %v", dec)
	}

	flow := func(open bool) smeldr.StateFlow {
		return smeldr.StateFlow{Name: "pg-aw", TypeName: "PgAw",
			States:      []smeldr.State{{Name: "todo", IsInitial: true, AwaitsAction: open}, {Name: "done", IsTerminal: true}},
			Transitions: []smeldr.Transition{{From: "todo", To: "done"}, {From: "done", To: "todo"}}}
	}
	if err := app.RegisterFlow(flow(false)); err != nil {
		t.Fatal(err)
	}
	if err := app.RegisterFlow(flow(true)); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := smeldr.StatesAwaitingAction(ctx, db, "PgAw"); !got["todo"] {
		t.Errorf("re-registration did not set awaits_action on the existing row: %v", got)
	}
	opts, err := app.ValidTransitions(ctx, "PgAw", "done")
	if err != nil || len(opts) != 1 || opts[0].ToState != "todo" || !opts[0].ToAwaitsAction {
		t.Errorf("ValidTransitions = %+v, %v; want done -> todo awaiting action", opts, err)
	}
}
