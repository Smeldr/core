package smeldr

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

// insertExpiryTestSignal inserts a smeldr_signals row with a CreatedAt
// backdated by age, for [App.ExpireSignals] test fixtures. Returns the new
// row's id (also used as its slug).
func insertExpiryTestSignal(t *testing.T, db *sql.DB, signalType, status, receiver string, age time.Duration) string {
	t.Helper()
	id := NewID()
	createdAt := time.Now().UTC().Add(-age)
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO smeldr_signals (id, slug, status, created_at, updated_at, sender, receiver, signal_type, message, task_ref, sequence)
		 VALUES (?, ?, ?, ?, ?, 'system', ?, ?, 'test message', '', 0)`,
		id, id, status, createdAt, createdAt, receiver, signalType,
	); err != nil {
		t.Fatalf("insertExpiryTestSignal: %v", err)
	}
	return id
}

func signalStatus(t *testing.T, db *sql.DB, id string) string {
	t.Helper()
	var status string
	if err := db.QueryRowContext(context.Background(),
		`SELECT status FROM smeldr_signals WHERE id = ?`, id,
	).Scan(&status); err != nil {
		t.Fatalf("signalStatus: %v", err)
	}
	return status
}

// TestExpireSignals_Retired: the sweep is retired (D115). It moves nothing,
// whatever it is given, with or without a database.
func TestExpireSignals_Retired(t *testing.T) {
	if w, e, s, err := (&App{}).ExpireSignals(context.Background(), SignalExpiryConfig{}); err != nil || w+e+s != 0 {
		t.Fatalf("nil DB: (%d,%d,%d,%v), want zeros", w, e, s, err)
	}
	db := newSQLiteDB(t)
	if err := CreateOrchestrationTables(db); err != nil {
		t.Fatalf("CreateOrchestrationTables: %v", err)
	}
	old := insertExpiryTestSignal(t, db, "notice", "pending", "core", 30*24*time.Hour)
	app := &App{cfg: Config{DB: db}}
	w, e, s, err := app.ExpireSignals(context.Background(), SignalExpiryConfig{MaxAge: time.Hour, BatchCap: -1})
	if err != nil || w+e+s != 0 {
		t.Fatalf("ExpireSignals = (%d,%d,%d,%v), want zeros", w, e, s, err)
	}
	if got := signalStatus(t, db, old); got != "pending" {
		t.Errorf("status = %q, want pending (nothing expires)", got)
	}
}
