package smeldr

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

// — EnsureColumn (T246) ————————————————————————————————————————————————————

func TestEnsureColumn_AddsColumn(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE ensure_column_test (
			id   TEXT NOT NULL PRIMARY KEY,
			slug TEXT NOT NULL DEFAULT ''
		)`); err != nil {
		t.Fatalf("create table: %v", err)
	}

	if err := EnsureColumn(ctx, db, "ensure_column_test", "note", "TEXT NOT NULL DEFAULT ''"); err != nil {
		t.Fatalf("EnsureColumn: %v", err)
	}

	if _, err := db.ExecContext(ctx,
		`INSERT INTO ensure_column_test (id, slug, note) VALUES ('1', 'test', 'hi')`,
	); err != nil {
		t.Errorf("note column should exist after EnsureColumn, got: %v", err)
	}
}

func TestEnsureColumn_Idempotent(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE ensure_column_idempotent_test (
			id   TEXT NOT NULL PRIMARY KEY,
			note TEXT NOT NULL DEFAULT ''
		)`); err != nil {
		t.Fatalf("create table: %v", err)
	}

	if err := EnsureColumn(ctx, db, "ensure_column_idempotent_test", "note", "TEXT NOT NULL DEFAULT ''"); err != nil {
		t.Errorf("first call: %v", err)
	}
	if err := EnsureColumn(ctx, db, "ensure_column_idempotent_test", "note", "TEXT NOT NULL DEFAULT ''"); err != nil {
		t.Errorf("second call: %v", err)
	}
}

func TestEnsureColumn_NonSQLite(t *testing.T) {
	db := &queryFailDB{}
	if err := EnsureColumn(context.Background(), db, "any_table", "any_column", "TEXT"); err != nil {
		t.Fatalf("non-SQLite: expected nil, got %v", err)
	}
}

func TestEnsureColumn_AlterFails(t *testing.T) {
	// Empty SQLite DB with no target table: PRAGMA returns empty rows (no
	// error), column absent, then ALTER TABLE fails (no such table).
	db := newSQLiteDB(t)
	if err := EnsureColumn(context.Background(), db, "no_such_table", "note", "TEXT"); err == nil {
		t.Error("expected error when ALTER TABLE has no target table, got nil")
	}
}

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
	var flowID string
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
	if txCount != 6 {
		t.Errorf("transition count: want 6, got %d", txCount)
	}

	// published→draft edge exists (independent default-flow gap, fixed in A217).
	var pubToDraftCount int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM smeldr_transitions WHERE flow_id = ? AND from_state = 'published' AND to_state = 'draft'`,
		flowID,
	).Scan(&pubToDraftCount); err != nil {
		t.Fatalf("query published→draft: %v", err)
	}
	if pubToDraftCount != 1 {
		t.Errorf("published→draft transition: count = %d, want 1", pubToDraftCount)
	}

	// smeldr_transition_triggers table exists (even if empty).
	var triggerCount int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM smeldr_transition_triggers`,
	).Scan(&triggerCount); err != nil {
		t.Fatalf("smeldr_transition_triggers table missing: %v", err)
	}
}

// — migrateDuplicateStateFlowRows (T268) —————————————————————————————————————

// insertRawStateFlow inserts a smeldr_state_flows row directly at the SQL
// level with an explicit created_at, bypassing RegisterFlow entirely — used
// to construct the exact pre-fix duplicate shape (T268) a test needs to
// reproduce, independent of whatever RegisterFlow's own current behaviour is.
func insertRawStateFlow(t *testing.T, db *sql.DB, id, name, typeName, createdAt string) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO smeldr_state_flows(id, name, type_name, created_at) VALUES ($1, $2, $3, $4)`,
		id, name, typeName, createdAt,
	); err != nil {
		t.Fatalf("insertRawStateFlow(%s): %v", name, err)
	}
}

func TestMigrateDuplicateStateFlowRows_KeepsLatest(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()
	if err := CreateStateFlowTables(db); err != nil {
		t.Fatalf("CreateStateFlowTables: %v", err)
	}

	// Reproduces the live incident directly: two rows for type_name="Task",
	// the older one (architect-task) predating the rename to agent-task.
	oldID, newID := NewID(), NewID()
	insertRawStateFlow(t, db, oldID, "architect-task", "Task", "2026-01-01 00:00:00")
	insertRawStateFlow(t, db, newID, "agent-task", "Task", "2026-02-01 00:00:00")

	// The old row's own orphaned state/transition, to confirm cleanup reaches them.
	oldStateID := NewID()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO smeldr_states(id, flow_id, name) VALUES ($1, $2, 'draft')`, oldStateID, oldID,
	); err != nil {
		t.Fatalf("insert old state: %v", err)
	}
	oldTransitionID := NewID()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO smeldr_transitions(id, flow_id, from_state, to_state) VALUES ($1, $2, 'draft', 'done')`,
		oldTransitionID, oldID,
	); err != nil {
		t.Fatalf("insert old transition: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO smeldr_transition_triggers(id, transition_id, trigger_class, trigger_type, config) VALUES ($1, $2, 'sync', 'test', '{}')`,
		NewID(), oldTransitionID,
	); err != nil {
		t.Fatalf("insert old trigger: %v", err)
	}

	if err := migrateDuplicateStateFlowRows(ctx, db); err != nil {
		t.Fatalf("migrateDuplicateStateFlowRows: %v", err)
	}

	var count int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM smeldr_state_flows WHERE type_name = 'Task'`,
	).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("flow count = %d, want 1", count)
	}
	var survivingID string
	if err := db.QueryRowContext(ctx,
		`SELECT id FROM smeldr_state_flows WHERE type_name = 'Task'`,
	).Scan(&survivingID); err != nil {
		t.Fatalf("select id: %v", err)
	}
	if survivingID != newID {
		t.Errorf("survivor = %q, want the newer row %q", survivingID, newID)
	}

	for table, id := range map[string]string{
		"smeldr_states":              oldID,
		"smeldr_transitions":         oldID,
		"smeldr_transition_triggers": "", // checked separately below
	} {
		if table == "smeldr_transition_triggers" {
			continue
		}
		var n int
		if err := db.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM "+table+" WHERE flow_id = $1", id,
		).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if n != 0 {
			t.Errorf("%s rows remaining for orphaned flow_id = %d, want 0", table, n)
		}
	}
	var triggerCount int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM smeldr_transition_triggers WHERE transition_id = $1`, oldTransitionID,
	).Scan(&triggerCount); err != nil {
		t.Fatalf("count triggers: %v", err)
	}
	if triggerCount != 0 {
		t.Errorf("transition_triggers remaining for orphaned transition = %d, want 0", triggerCount)
	}
}

func TestMigrateDuplicateStateFlowRows_NoDuplicates_NoOp(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()
	if err := CreateStateFlowTables(db); err != nil {
		t.Fatalf("CreateStateFlowTables: %v", err)
	}
	insertRawStateFlow(t, db, NewID(), "goal-lifecycle", "Goal", "2026-01-01 00:00:00")

	if err := migrateDuplicateStateFlowRows(ctx, db); err != nil {
		t.Fatalf("migrateDuplicateStateFlowRows: %v", err)
	}

	var count int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM smeldr_state_flows WHERE type_name = 'Goal'`,
	).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("flow count = %d, want 1 (untouched)", count)
	}
}

func TestMigrateDuplicateStateFlowRows_MultipleGroups(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()
	if err := CreateStateFlowTables(db); err != nil {
		t.Fatalf("CreateStateFlowTables: %v", err)
	}
	// Two independent duplicate groups, each handled on its own.
	insertRawStateFlow(t, db, "task-old", "architect-task", "Task", "2026-01-01 00:00:00")
	insertRawStateFlow(t, db, "task-new", "agent-task", "Task", "2026-02-01 00:00:00")
	insertRawStateFlow(t, db, "decision-old", "decision-v1", "Decision", "2026-01-01 00:00:00")
	insertRawStateFlow(t, db, "decision-new", "decision-v2", "Decision", "2026-02-01 00:00:00")

	if err := migrateDuplicateStateFlowRows(ctx, db); err != nil {
		t.Fatalf("migrateDuplicateStateFlowRows: %v", err)
	}

	for typeName, wantID := range map[string]string{"Task": "task-new", "Decision": "decision-new"} {
		var count int
		if err := db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM smeldr_state_flows WHERE type_name = $1`, typeName,
		).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", typeName, err)
		}
		if count != 1 {
			t.Errorf("%s: flow count = %d, want 1", typeName, count)
		}
		var gotID string
		if err := db.QueryRowContext(ctx,
			`SELECT id FROM smeldr_state_flows WHERE type_name = $1`, typeName,
		).Scan(&gotID); err != nil {
			t.Fatalf("select id %s: %v", typeName, err)
		}
		if gotID != wantID {
			t.Errorf("%s: survivor = %q, want %q", typeName, gotID, wantID)
		}
	}
}

func TestMigrateDuplicateStateFlowRows_FindGroupsError(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateStateFlowTables(db); err != nil {
		t.Fatalf("CreateStateFlowTables: %v", err)
	}
	wrapped := &alwaysFailQueryContextDB{DB: db}
	if err := migrateDuplicateStateFlowRows(context.Background(), wrapped); err == nil {
		t.Error("expected error when finding duplicate groups fails, got nil")
	}
}

// dupFlowQueryFailAfter wraps a real DB, failing QueryContext once its call
// count exceeds failAfter — lets a test choose exactly which stage of
// migrateDuplicateStateFlowRows fails (finding groups succeeds, listing a
// group's own row ids fails).
type dupFlowQueryFailAfter struct {
	DB
	failAfter int
	count     int
}

func (d *dupFlowQueryFailAfter) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	d.count++
	if d.count > d.failAfter {
		return nil, errors.New("simulated query error")
	}
	return d.DB.QueryContext(ctx, query, args...)
}

func TestMigrateDuplicateStateFlowRows_ListRowsError(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()
	if err := CreateStateFlowTables(db); err != nil {
		t.Fatalf("CreateStateFlowTables: %v", err)
	}
	insertRawStateFlow(t, db, "task-old", "architect-task", "Task", "2026-01-01 00:00:00")
	insertRawStateFlow(t, db, "task-new", "agent-task", "Task", "2026-02-01 00:00:00")

	wrapped := &dupFlowQueryFailAfter{DB: db, failAfter: 1}
	if err := migrateDuplicateStateFlowRows(ctx, wrapped); err == nil {
		t.Error("expected error when listing a duplicate group's own row ids fails, got nil")
	}
}

// dupFlowExecFailAt wraps a real DB, failing the Nth ExecContext call — lets
// a test target exactly one of deleteOrphanedStateFlow's four sequential
// deletes.
type dupFlowExecFailAt struct {
	DB
	failAt int
	count  int
}

func (d *dupFlowExecFailAt) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	d.count++
	if d.count == d.failAt {
		return nil, errors.New("simulated exec error")
	}
	return d.DB.ExecContext(ctx, query, args...)
}

func TestMigrateDuplicateStateFlowRows_DeleteError(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()
	if err := CreateStateFlowTables(db); err != nil {
		t.Fatalf("CreateStateFlowTables: %v", err)
	}
	insertRawStateFlow(t, db, "task-old", "architect-task", "Task", "2026-01-01 00:00:00")
	insertRawStateFlow(t, db, "task-new", "agent-task", "Task", "2026-02-01 00:00:00")

	wrapped := &dupFlowExecFailAt{DB: db, failAt: 1}
	if err := migrateDuplicateStateFlowRows(ctx, wrapped); err == nil {
		t.Error("expected error when deleteOrphanedStateFlow's own delete fails, got nil")
	}
}

// TestDeleteOrphanedStateFlow_ExecErrors covers all four of
// deleteOrphanedStateFlow's own sequential deletes failing in turn
// (transition_triggers, transitions, states, the flow row itself).
func TestDeleteOrphanedStateFlow_ExecErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		failAt int
	}{
		{"transition_triggers delete fails", 1},
		{"transitions delete fails", 2},
		{"states delete fails", 3},
		{"flow row delete fails", 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := newSQLiteDB(t)
			if err := CreateStateFlowTables(db); err != nil {
				t.Fatalf("CreateStateFlowTables: %v", err)
			}
			flowID := NewID()
			insertRawStateFlow(t, db, flowID, "test-flow", "TestType", "2026-01-01 00:00:00")
			wrapped := &dupFlowExecFailAt{DB: db, failAt: tc.failAt}
			if err := deleteOrphanedStateFlow(context.Background(), wrapped, flowID); err == nil {
				t.Errorf("%s: expected error, got nil", tc.name)
			}
		})
	}
}

// TestMigrateStateFlows_DuplicateCleanupError pins migrateStateFlows's own
// new wrap of migrateDuplicateStateFlowRows's error — CreateStateFlowTables
// makes only ExecContext calls, so alwaysFailQueryContextDB lets it succeed
// and fails on migrateDuplicateStateFlowRows's own first QueryContext call.
func TestMigrateStateFlows_DuplicateCleanupError(t *testing.T) {
	db := newSQLiteDB(t)
	wrapped := &alwaysFailQueryContextDB{DB: db}
	if err := migrateStateFlows(context.Background(), wrapped); err == nil {
		t.Error("expected error when migrateDuplicateStateFlowRows fails, got nil")
	}
}

// TestMigrateStateFlows_CreateIndexError pins migrateStateFlows's own new
// CREATE UNIQUE INDEX statement (T268): CreateStateFlowTables makes 5
// ExecContext calls, migrateDuplicateStateFlowRows makes none on a fresh
// (duplicate-free) database, so the index creation is the 6th.
func TestMigrateStateFlows_CreateIndexError(t *testing.T) {
	db := newSQLiteDB(t)
	wrapped := &dupFlowExecFailAt{DB: db, failAt: 6}
	if err := migrateStateFlows(context.Background(), wrapped); err == nil {
		t.Error("expected error when CREATE UNIQUE INDEX fails, got nil")
	}
}

// TestMigrateStateFlows_HealsExistingDuplicate confirms migrateStateFlows
// itself — not just migrateDuplicateStateFlowRows in isolation — runs the
// cleanup before the new unique index, so a pre-existing duplicate (any
// install a pre-T268 binary already wrote one to, including the live
// incident) self-heals on the next boot instead of the index creation
// failing against still-present duplicate data.
func TestMigrateStateFlows_HealsExistingDuplicate(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()
	if err := CreateStateFlowTables(db); err != nil {
		t.Fatalf("CreateStateFlowTables: %v", err)
	}
	insertRawStateFlow(t, db, "task-old", "architect-task", "Task", "2026-01-01 00:00:00")
	insertRawStateFlow(t, db, "task-new", "agent-task", "Task", "2026-02-01 00:00:00")

	if err := migrateStateFlows(ctx, db); err != nil {
		t.Fatalf("migrateStateFlows: %v", err)
	}

	var count int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM smeldr_state_flows WHERE type_name = 'Task'`,
	).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("flow count = %d, want 1 (healed)", count)
	}

	// The unique index must exist and actually be enforced now.
	if _, err := db.ExecContext(ctx,
		`INSERT INTO smeldr_state_flows(id, name, type_name) VALUES ($1, 'another-task-flow', 'Task')`,
		NewID(),
	); err == nil {
		t.Error("expected UNIQUE constraint violation inserting a second row for type_name='Task', got nil")
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
