package smeldr

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
)

// migrateStateFlows creates the four state-flow tables and seeds the default
// flow (draft→scheduled→published→archived). All operations are idempotent:
// tables use CREATE TABLE IF NOT EXISTS; inserts use ON CONFLICT DO NOTHING.
// Called once at startup from [New] when [Config.DB] is non-nil.
func migrateStateFlows(ctx context.Context, db DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS smeldr_state_flows (
			id              TEXT NOT NULL PRIMARY KEY,
			name            TEXT NOT NULL UNIQUE,
			type_name       TEXT,
			description     TEXT,
			active_state    TEXT NOT NULL DEFAULT '',
			conflict_policy TEXT NOT NULL DEFAULT '',
			created_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS smeldr_states (
			id                 TEXT NOT NULL PRIMARY KEY,
			flow_id            TEXT NOT NULL REFERENCES smeldr_state_flows(id),
			name               TEXT    NOT NULL,
			is_initial         BOOLEAN NOT NULL DEFAULT FALSE,
			is_terminal        BOOLEAN NOT NULL DEFAULT FALSE,
			suppresses_signals BOOLEAN NOT NULL DEFAULT FALSE,
			UNIQUE(flow_id, name)
		)`,
		`CREATE TABLE IF NOT EXISTS smeldr_transitions (
			id              TEXT NOT NULL PRIMARY KEY,
			flow_id         TEXT NOT NULL REFERENCES smeldr_state_flows(id),
			from_state      TEXT    NOT NULL,
			to_state        TEXT    NOT NULL,
			required_role   TEXT,
			required_reason BOOLEAN NOT NULL DEFAULT FALSE,
			strict          BOOLEAN NOT NULL DEFAULT FALSE,
			UNIQUE(flow_id, from_state, to_state)
		)`,
		`CREATE TABLE IF NOT EXISTS smeldr_transition_triggers (
			id             TEXT NOT NULL PRIMARY KEY,
			transition_id  TEXT NOT NULL REFERENCES smeldr_transitions(id),
			trigger_class  TEXT    NOT NULL CHECK(trigger_class IN ('sync', 'async')),
			trigger_type   TEXT    NOT NULL,
			config         TEXT    NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS smeldr_eval_queue (
			id         TEXT    PRIMARY KEY,
			type_name  TEXT    NOT NULL,
			item_id    TEXT    NOT NULL,
			to_state   TEXT    NOT NULL,
			eval_at    TIMESTAMP NOT NULL,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(type_name, item_id, to_state)
		)`,
	}
	for _, s := range stmts {
		if _, err := db.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("smeldr: migrateStateFlows: %w", err)
		}
	}

	// Seed the default flow — mirrors the compile-time enum.
	if _, err := db.ExecContext(ctx,
		`INSERT INTO smeldr_state_flows(id, name, type_name) VALUES ($1, 'default', NULL) ON CONFLICT (name) DO NOTHING`,
		NewID()); err != nil {
		return fmt.Errorf("smeldr: migrateStateFlows: seed flow: %w", err)
	}
	var flowID string
	if err := db.QueryRowContext(ctx,
		`SELECT id FROM smeldr_state_flows WHERE name = 'default'`).Scan(&flowID); err != nil {
		return fmt.Errorf("smeldr: migrateStateFlows: seed flow id: %w", err)
	}

	states := []struct {
		name     string
		initial  bool
		terminal bool
	}{
		{"draft", true, false},
		{"scheduled", false, false},
		{"published", false, false},
		{"archived", false, true},
	}
	for _, s := range states {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO smeldr_states(id, flow_id, name, is_initial, is_terminal, suppresses_signals) VALUES ($1, $2, $3, $4, $5, FALSE) ON CONFLICT (flow_id, name) DO NOTHING`,
			NewID(), flowID, s.name, s.initial, s.terminal,
		); err != nil {
			return fmt.Errorf("smeldr: migrateStateFlows: seed state %s: %w", s.name, err)
		}
	}

	transitions := [][2]string{
		{"draft", "scheduled"},
		{"draft", "published"},
		{"scheduled", "published"},
		{"published", "archived"},
		{"draft", "archived"},
		{"published", "draft"},
	}
	for _, t := range transitions {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO smeldr_transitions(id, flow_id, from_state, to_state) VALUES ($1, $2, $3, $4) ON CONFLICT (flow_id, from_state, to_state) DO NOTHING`,
			NewID(), flowID, t[0], t[1],
		); err != nil {
			return fmt.Errorf("smeldr: migrateStateFlows: seed transition %s→%s: %w", t[0], t[1], err)
		}
	}
	if err := migrateStateFlowConflictColumns(ctx, db); err != nil {
		return err
	}
	if err := migrateTransitionReasonColumn(ctx, db); err != nil {
		return err
	}
	return migrateTransitionStrictColumn(ctx, db)
}

// EnsureColumn adds column to table if it does not already exist, using
// columnDDL as the column's full type/constraint clause — for example,
// "TEXT NOT NULL DEFAULT" followed by an empty-string literal, or
// "INTEGER NOT NULL DEFAULT 0". Idempotent — safe to call on every application
// startup. A no-op on non-SQLite databases (PRAGMA table_info is
// SQLite-specific, matching every existing column migration in this
// package); other database engines migrate by their own tooling.
//
// Additive only — never drops a column, so downgrading to an older binary
// after EnsureColumn has run leaves one unused column present, never lost
// data or a broken older schema: older code that doesn't declare the
// field simply never reads or writes it.
//
// Call EnsureColumn for a column your own type declares, at your own
// application's startup, the same way Create*Table is already called —
// there is no central registry of "columns the framework knows about."
// An application extending a framework-provided table (T246's own
// motivating incident: SiteConfig extended downstream, hand-patched
// twice) calls EnsureColumn itself for its own added columns; ownership
// follows whoever declared the field, not a shared migration list (T246).
func EnsureColumn(ctx context.Context, db DB, table, column, columnDDL string) error {
	rows, err := db.QueryContext(ctx, "PRAGMA table_info("+quoteIdent(table)+")")
	if err != nil {
		return nil // non-SQLite — assume schema is current
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, pk int
		var name, colType string
		var dflt *string
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil {
			continue
		}
		if name == column {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx,
		"ALTER TABLE "+quoteIdent(table)+" ADD COLUMN "+quoteIdent(column)+" "+columnDDL,
	); err != nil {
		return fmt.Errorf("smeldr: EnsureColumn: %s.%s: %w", table, column, err)
	}
	return nil
}

// migrateTransitionReasonColumn adds the required_reason column to
// smeldr_transitions on pre-existing SQLite databases that predate this
// column (T149). Fresh installs on any DB engine already have the column via
// the CREATE TABLE statement above; this only upgrades an existing SQLite
// database created before A220. Idempotent — safe to call on every boot.
// A no-op on non-SQLite databases (PRAGMA not supported), same precedent
// as migrateStateFlowConflictColumns.
func migrateTransitionReasonColumn(ctx context.Context, db DB) error {
	return EnsureColumn(ctx, db, "smeldr_transitions", "required_reason", "BOOLEAN NOT NULL DEFAULT FALSE")
}

// migrateTransitionStrictColumn adds the strict column to smeldr_transitions
// on pre-existing SQLite databases that predate this column (D34, T206).
// Fresh installs on any DB engine already have the column via the CREATE
// TABLE statement above; this only upgrades an existing SQLite database
// created before this column existed. Idempotent — safe to call on every
// boot. A no-op on non-SQLite databases (PRAGMA not supported), same
// precedent as migrateTransitionReasonColumn.
func migrateTransitionStrictColumn(ctx context.Context, db DB) error {
	return EnsureColumn(ctx, db, "smeldr_transitions", "strict", "BOOLEAN NOT NULL DEFAULT FALSE")
}

// migrateStateFlowConflictColumns adds the active_state and conflict_policy
// columns to smeldr_state_flows when they are absent. Idempotent — safe to
// call on every boot. A no-op on non-SQLite databases (PRAGMA not supported).
func migrateStateFlowConflictColumns(ctx context.Context, db DB) error {
	if err := EnsureColumn(ctx, db, "smeldr_state_flows", "active_state", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	return EnsureColumn(ctx, db, "smeldr_state_flows", "conflict_policy", "TEXT NOT NULL DEFAULT ''")
}

// migrateLegacyTableNames renames any forge_* tables that still exist in the
// database to their smeldr_* equivalents. It is called from [New] once at
// startup when [Config.DB] is non-nil.
//
// The function only operates on SQLite databases (identified by the presence
// of sqlite_master). For other databases, the caller must migrate manually.
// All renames are wrapped in a single transaction when the DB supports BeginTx.
//
// Idempotency: if both the source (forge_*) and destination (smeldr_*) tables
// already exist — indicating a partial migration from a previous run — that pair
// is skipped with a warning and the remaining pairs are still processed.
// Re-running after a full or partial migration is safe.
func migrateLegacyTableNames(ctx context.Context, db DB) error {
	pairs := [][2]string{
		{"forge_audit_log", "smeldr_audit_log"},
		{"forge_delivery_logs", "smeldr_delivery_logs"},
		{"forge_nav", "smeldr_nav"},
		{"forge_outbound_jobs", "smeldr_outbound_jobs"},
		{"forge_redirects", "smeldr_redirects"},
		{"forge_tokens", "smeldr_tokens"},
		{"forge_webhook_endpoints", "smeldr_webhook_endpoints"},
	}

	// Probe sqlite_master. Returns silently when db is not SQLite.
	var dummy int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master`).Scan(&dummy); err != nil {
		return nil // not SQLite — skip silently
	}

	// Determine which legacy tables still exist and need renaming.
	var toRename [][2]string
	for _, pair := range pairs {
		var srcN int
		if err := db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=$1`, pair[0],
		).Scan(&srcN); err != nil || srcN == 0 {
			continue // source doesn't exist — nothing to rename
		}
		var dstN int
		if err := db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=$1`, pair[1],
		).Scan(&dstN); err == nil && dstN > 0 {
			// Destination already exists — partial migration from a previous run.
			// Skip this pair rather than failing the rename.
			slog.Warn("smeldr: legacy table migration skipped — destination already exists",
				"src", pair[0], "dst", pair[1])
			continue
		}
		toRename = append(toRename, pair)
	}
	if len(toRename) == 0 {
		return nil
	}

	// Execute renames in a transaction when the DB supports it.
	type transactor interface {
		BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error)
	}

	execDB := db
	var commit func() error = func() error { return nil }
	var rollback func() error = func() error { return nil }

	if tr, ok := db.(transactor); ok {
		tx, err := tr.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("smeldr: migrate legacy tables: begin: %w", err)
		}
		execDB = tx
		commit = tx.Commit
		rollback = tx.Rollback
	}

	for _, pair := range toRename {
		slog.Info("smeldr: renaming legacy table", "from", pair[0], "to", pair[1])
		if _, err := execDB.ExecContext(ctx, `ALTER TABLE `+pair[0]+` RENAME TO `+pair[1]); err != nil {
			_ = rollback()
			return fmt.Errorf("smeldr: migrate legacy tables: %s → %s: %w", pair[0], pair[1], err)
		}
	}
	return commit()
}
