//go:build integration

package pgx

import (
	"context"
	"testing"
	"time"

	smeldr "smeldr.dev/core"
)

// D115 on Postgres: the Signal flow by kind, the open-subject index, the
// resolver, answering, and MigrateSignalKinds.
func TestPG_SignalKinds(t *testing.T) {
	db, _ := isolatedDB(t)
	ctx := context.Background()
	if err := smeldr.CreateOrchestrationTables(db); err != nil {
		t.Fatal(err)
	}
	if err := smeldr.EnsureOrchestrationSignalColumns(ctx, db); err != nil {
		t.Fatalf("EnsureOrchestrationSignalColumns: %v", err)
	}
	var idx int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pg_indexes WHERE indexname = 'idx_smeldr_signals_open_subject'`).Scan(&idx); err != nil || idx != 1 {
		t.Fatalf("open-subject index = %d, %v", idx, err)
	}
	app := smeldr.New(smeldr.Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16"), DB: db})
	smeldr.RegisterOrchestrationTypes(app, db)
	_ = app.Handler()

	// Before D115: rows written the old way.
	now := time.Now().UTC()
	for _, r := range [][3]string{{"s-ack", "review-requested", "acknowledged"}, {"s-iq", "implementation-question", "pending"}, {"s-hist", "plan-ready", "read"}} {
		if _, err := db.ExecContext(ctx, `INSERT INTO smeldr_signals (id, slug, status, created_at, updated_at, signal_type) VALUES ($1, $1, $2, $3, $3, $4)`,
			r[0], r[2], now, r[1]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO smeldr_decisions (id, slug, status, created_at, updated_at) VALUES ('d-old', 'd-old', 'proposed', $1, $1)`, now); err != nil {
		t.Fatal(err)
	}
	rep, err := app.MigrateSignalKinds(ctx, smeldr.SignalMigrationOptions{Apply: true})
	if err != nil || len(rep.Changes) != 2 || len(rep.Opened) != 1 || rep.History["plan-ready"]["read"] != 1 {
		t.Fatalf("migration = %+v, %v", rep, err)
	}
	if again, err := app.MigrateSignalKinds(ctx, smeldr.SignalMigrationOptions{Apply: true}); err != nil || len(again.Changes)+len(again.Opened) != 0 {
		t.Errorf("second run = %+v, %v", again, err)
	}
	status := func(id string) (s, answer string) {
		if err := db.QueryRowContext(ctx, `SELECT status, answer FROM smeldr_signals WHERE id = $1`, id).Scan(&s, &answer); err != nil {
			t.Fatal(err)
		}
		return
	}
	if s, a := status("s-ack"); s != "answered" || a == "" {
		t.Errorf("acknowledged request = %s %q", s, a)
	}

	// The migrated question is answered; the Decision's condition resolves
	// when it leaves proposed.
	if err := app.AnswerSignal(ctx, "s-iq", "yes", "Task:t1"); err != nil {
		t.Fatalf("AnswerSignal: %v", err)
	}
	if s, a := status("s-iq"); s != "answered" || a != "yes" {
		t.Errorf("answered question = %s %q", s, a)
	}
	if _, err := app.TransitionItemVia(ctx, "mcp", "Decision", "d-old", "archived", ""); err != nil {
		t.Fatalf("archive: %v", err)
	}
	var open int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM smeldr_signals WHERE status = 'open'`).Scan(&open); err != nil || open != 0 {
		t.Errorf("open after the Decision left proposed = %d, %v", open, err)
	}
	if _, err := app.TransitionItemVia(ctx, "mcp", "Signal", "s-hist", "acknowledged", ""); err != nil {
		t.Errorf("history acknowledge: %v", err)
	}
}
