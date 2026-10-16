package smeldr

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

// signalMigrationFixture holds one Signal of each case the migration knows,
// and two Decisions in proposed, one of which already has its condition.
func signalMigrationFixture(t *testing.T) (*App, *sql.DB, map[string]string) {
	t.Helper()
	app, db := signalTestApp(t)
	app.signalConditions = false // the fixture is the state before D115
	ids := map[string]string{}
	for _, c := range []struct{ name, signalType, status string }{
		{"cond-pending", "authorization-required", "pending"},
		{"cond-read", "conflict-detected", "read"},
		{"cond-expired", "conflict-detected", "expired"},
		{"req-pending", "review-requested", "pending"},
		{"req-ack", "review-requested", "acknowledged"},
		{"req-expired", "review-requested", "expired"},
		{"iq-read", "implementation-question", "read"},
		{"iq-ack", "implementation-question", "acknowledged"},
		{"hist-pending", "plan-ready", "pending"},
		{"hist-ack", "plan-ready", "acknowledged"},
		{"hist-notice", "notice", "expired"},
	} {
		ids[c.name] = insertExpiryTestSignal(t, db, c.signalType, c.status, "core", time.Hour)
	}
	insertDecision(t, db, "d-open", "d-open", "proposed")
	insertDecision(t, db, "d-has", "d-has", "proposed")
	insertDecision(t, db, "d-done", "d-done", "ratified")
	if err := InsertSignal(withSignalSystem(context.Background()), db, ratificationSignal("d-has")); err != nil {
		t.Fatal(err)
	}
	return app, db, ids
}

func TestMigrateSignalKinds(t *testing.T) {
	app, db, ids := signalMigrationFixture(t)
	ctx := context.Background()
	before := countSignals(t, db, "ratification-required", "open")

	dry, err := app.MigrateSignalKinds(ctx, SignalMigrationOptions{})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if dry.Applied || len(dry.Changes) != 8 || len(dry.Opened) != 1 {
		t.Fatalf("dry run = %+v, want 8 changes and 1 opened", dry)
	}
	if signalStatusOf(t, db, ids["req-ack"]) != "acknowledged" || countSignals(t, db, "ratification-required", "open") != before {
		t.Fatal("a dry run wrote")
	}

	rep, err := app.MigrateSignalKinds(ctx, SignalMigrationOptions{Apply: true})
	if err != nil || !rep.Applied || len(rep.Changes) != 8 || len(rep.Opened) != 1 {
		t.Fatalf("apply = %+v, %v", rep, err)
	}
	want := map[string][3]string{
		"cond-pending": {"open", "authorization-required", ""},
		"cond-read":    {"open", "conflict-detected", ""},
		"cond-expired": {"open", "conflict-detected", ""},
		"req-pending":  {"open", "review-requested", ""},
		"req-ack":      {"answered", "review-requested", signalMigratedAcknowledged},
		"req-expired":  {"withdrawn", "review-requested", signalMigratedExpired},
		"iq-read":      {"open", "question", ""},
		"iq-ack":       {"answered", "question", signalMigratedAcknowledged},
		"hist-pending": {"pending", "plan-ready", ""},
		"hist-ack":     {"acknowledged", "plan-ready", ""},
		"hist-notice":  {"expired", "notice", ""},
	}
	for name, w := range want {
		status, signalType, _, answer, _ := signalRow(t, db, ids[name])
		if status != w[0] || signalType != w[1] || answer != w[2] {
			t.Errorf("%s = %s %s %q, want %v", name, status, signalType, answer, w)
		}
	}
	if rep.History["plan-ready"]["pending"] != 1 || rep.History["plan-ready"]["acknowledged"] != 1 || rep.History["notice"]["expired"] != 1 {
		t.Errorf("history = %v", rep.History)
	}
	if rep.Opened[0].Subject != "Decision:d-open" || openSignalsOf(t, db, "ratification-required", "Decision", "d-open") != 1 {
		t.Errorf("opened = %+v", rep.Opened)
	}
	var noted bool
	for _, c := range rep.Changes {
		if c.ID == ids["cond-expired"] && c.Note != "" {
			noted = true
		}
	}
	if !noted {
		t.Error("the expired condition is not noted")
	}

	again, err := app.MigrateSignalKinds(ctx, SignalMigrationOptions{Apply: true})
	if err != nil || len(again.Changes) != 0 || len(again.Opened) != 0 {
		t.Errorf("second run = %+v, %v; want nothing to do", again, err)
	}
}

func TestMigrateSignalKinds_Edges(t *testing.T) {
	ctx := context.Background()
	if _, err := MigrateSignalKinds(ctx, nil, SignalMigrationOptions{}); !errors.Is(err, ErrBadRequest) {
		t.Errorf("nil db: %v", err)
	}
	if rep, err := MigrateSignalKinds(ctx, newSQLiteDB(t), SignalMigrationOptions{Apply: true}); err != nil || len(rep.Changes) != 0 {
		t.Errorf("no table: %+v, %v", rep, err)
	}
	// Signals without Decisions: nothing to open.
	db := newSQLiteDB(t)
	if _, err := db.Exec(`CREATE TABLE smeldr_signals AS SELECT 1 AS x WHERE 0`); err != nil {
		t.Fatal(err)
	}
	if _, err := MigrateSignalKinds(ctx, db, SignalMigrationOptions{}); !errors.Is(err, ErrInternal) {
		t.Errorf("a table without the columns: %v, want ErrInternal", err)
	}
	for _, step := range []string{"SELECT id, COALESCE(address", "UPDATE smeldr_signals SET signal_type", "SELECT d.id FROM smeldr_decisions", "INSERT INTO smeldr_signals"} {
		t.Run(step, func(t *testing.T) {
			_, fdb, _ := signalMigrationFixture(t)
			if _, err := MigrateSignalKinds(ctx, failAddressDB{fdb, step}, SignalMigrationOptions{}); !errors.Is(err, ErrInternal) {
				t.Errorf("err = %v, want ErrInternal when %q fails", err, step)
			}
		})
	}
}
