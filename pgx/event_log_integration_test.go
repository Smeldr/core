//go:build integration

package pgx

import (
	"context"
	"fmt"
	"testing"
	"time"

	smeldr "smeldr.dev/core"
)

// D117 on Postgres: the event log table, its order, the cursor and the read
// of a page.
func TestPG_EventLog(t *testing.T) {
	db, _ := isolatedDB(t)
	ctx := context.Background()
	if err := smeldr.CreateOrchestrationTables(db); err != nil {
		t.Fatal(err)
	}
	if err := smeldr.CreateEventLogTable(db); err != nil {
		t.Fatal(err)
	}
	if err := smeldr.CreateEventLogTable(db); err != nil {
		t.Fatalf("second create: %v", err)
	}
	app := smeldr.New(smeldr.Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16"), DB: db})
	smeldr.RegisterOrchestrationTypes(app, db)
	app.EventStream().EventLog(smeldr.EventLogOptions{})
	_ = app.Handler()

	for i := range 5 {
		if err := smeldr.InsertSignal(ctx, db, &smeldr.Signal{Sender: "core", Receiver: "architect", SignalType: "question", Message: fmt.Sprint("q", i)}); err != nil {
			t.Fatal(err)
		}
	}
	tasks := smeldr.NewSQLRepo[*smeldr.Task](db, smeldr.Table("smeldr_tasks"))
	if err := tasks.Save(ctx, &smeldr.Task{Node: smeldr.Node{ID: smeldr.NewID(), Slug: "t-log", Status: "backlog"}, Title: "log me", Band: "core"}); err != nil {
		t.Fatal(err)
	}
	for _, to := range []string{"active", "waiting-plan", "plan-reviewing"} {
		if _, err := app.TransitionItem(ctx, "Task", "t-log", to); err != nil {
			t.Fatalf("transition to %s: %v", to, err)
		}
	}
	var page smeldr.EventPage
	deadline := time.Now().Add(5 * time.Second)
	for {
		p, err := app.EventsAfter(ctx, "", 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(p.Events) >= 3 || time.Now().After(deadline) {
			page = p
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(page.Events) < 3 {
		t.Fatalf("logged %d events, want the 3 transitions", len(page.Events))
	}
	for i := 1; i < len(page.Events); i++ {
		if page.Events[i].Seq <= page.Events[i-1].Seq {
			t.Fatalf("order: %d after %d", page.Events[i].Seq, page.Events[i-1].Seq)
		}
	}
	if e := page.Events[0]; e.SubjectType != "Task" || e.Event != "task.transitioned" || e.CreatedAt.IsZero() {
		t.Errorf("first event = %+v", e)
	}
	// D116: the meta a subscription filter matches on is logged, so a replay
	// filters the same way.
	if e := page.Events[0]; e.FromState != "backlog" || e.ToState != "active" || e.Fields["band"] != "core" {
		t.Errorf("logged filter meta = from %q to %q fields %v", e.FromState, e.ToState, e.Fields)
	}
	rest, err := app.EventsAfter(ctx, page.Events[0].ID, 0)
	if err != nil || len(rest.Events) != len(page.Events)-1 {
		t.Errorf("after the first = %d events, %v", len(rest.Events), err)
	}
	if gone, _ := app.EventsAfter(ctx, "gone", 0); !gone.Skipped {
		t.Error("an unknown cursor was not skipped")
	}
}
