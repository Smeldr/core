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
