package smeldr

import (
	"context"
	"testing"
)

// TestMigrateLegacyTableNames_destinationExists verifies that when both the
// source (forge_*) and destination (smeldr_*) tables already exist — indicating
// a partial migration from a previous run — the function returns nil and skips
// the rename rather than failing with a "table already exists" error.
func TestMigrateLegacyTableNames_destinationExists(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()

	// Simulate a partial prior migration: both tables coexist.
	for _, q := range []string{
		`CREATE TABLE forge_tokens (id TEXT PRIMARY KEY)`,
		`CREATE TABLE smeldr_tokens (id TEXT PRIMARY KEY)`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("setup: %v", err)
		}
	}

	if err := migrateLegacyTableNames(ctx, db); err != nil {
		t.Fatalf("migrateLegacyTableNames returned error: %v", err)
	}

	// Both tables must still be present — no rename was attempted.
	for _, name := range []string{"forge_tokens", "smeldr_tokens"} {
		var n int
		if err := db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=$1`, name,
		).Scan(&n); err != nil || n == 0 {
			t.Errorf("table %q should still exist after skipped migration", name)
		}
	}
}

// TestMigrateStateFlows verifies that migrateStateFlows is idempotent and seeds
// the default flow with the correct states and transitions.
func TestMigrateStateFlows(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()

	// Run twice — must be idempotent.
	for i := range 2 {
		if err := migrateStateFlows(ctx, db); err != nil {
			t.Fatalf("run %d: migrateStateFlows: %v", i+1, err)
		}
	}

	// Default flow exists with type_name NULL.
	var flowID int64
	var typeName *string
	if err := db.QueryRowContext(ctx,
		`SELECT id, type_name FROM smeldr_state_flows WHERE name = 'default'`,
	).Scan(&flowID, &typeName); err != nil {
		t.Fatalf("default flow not found: %v", err)
	}
	if typeName != nil {
		t.Errorf("default flow type_name: want NULL, got %q", *typeName)
	}

	// Exactly 4 states.
	var stateCount int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM smeldr_states WHERE flow_id = ?`, flowID,
	).Scan(&stateCount); err != nil {
		t.Fatalf("count states: %v", err)
	}
	if stateCount != 4 {
		t.Errorf("state count: want 4, got %d", stateCount)
	}

	// draft is_initial=1, archived is_terminal=1.
	checkState := func(name string, wantInitial, wantTerminal bool) {
		t.Helper()
		var initial, terminal bool
		if err := db.QueryRowContext(ctx,
			`SELECT is_initial, is_terminal FROM smeldr_states WHERE flow_id = ? AND name = ?`,
			flowID, name,
		).Scan(&initial, &terminal); err != nil {
			t.Errorf("state %q not found: %v", name, err)
			return
		}
		if initial != wantInitial {
			t.Errorf("state %q is_initial: want %v, got %v", name, wantInitial, initial)
		}
		if terminal != wantTerminal {
			t.Errorf("state %q is_terminal: want %v, got %v", name, wantTerminal, terminal)
		}
	}
	checkState("draft", true, false)
	checkState("scheduled", false, false)
	checkState("published", false, false)
	checkState("archived", false, true)

	// Exactly 5 transitions.
	var txCount int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM smeldr_transitions WHERE flow_id = ?`, flowID,
	).Scan(&txCount); err != nil {
		t.Fatalf("count transitions: %v", err)
	}
	if txCount != 5 {
		t.Errorf("transition count: want 5, got %d", txCount)
	}

	// smeldr_transition_triggers table exists (even if empty).
	var triggerCount int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM smeldr_transition_triggers`,
	).Scan(&triggerCount); err != nil {
		t.Fatalf("smeldr_transition_triggers table missing: %v", err)
	}
}

// TestMigrateLegacyTableNames_sourceOnly verifies the normal migration path:
// when only the source (forge_*) table exists, it is renamed to smeldr_*.
func TestMigrateLegacyTableNames_sourceOnly(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()

	if _, err := db.ExecContext(ctx,
		`CREATE TABLE forge_tokens (id TEXT PRIMARY KEY)`,
	); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if err := migrateLegacyTableNames(ctx, db); err != nil {
		t.Fatalf("migrateLegacyTableNames returned error: %v", err)
	}

	var srcN, dstN int
	db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='forge_tokens'`,
	).Scan(&srcN)
	db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='smeldr_tokens'`,
	).Scan(&dstN)

	if srcN != 0 {
		t.Errorf("forge_tokens should not exist after migration, got %d", srcN)
	}
	if dstN != 1 {
		t.Errorf("smeldr_tokens should exist after migration, got %d", dstN)
	}
}
