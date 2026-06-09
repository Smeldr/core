package smeldr

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
)

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
