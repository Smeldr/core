package smeldr

import (
	"context"
	"fmt"
	"time"
)

// CreateRoutesTable creates the smeldr_routes table and its path-pattern index
// if they do not already exist. Idempotent.
//
// Called automatically by [App.Redirects]; also available for migration tools
// and tests.
func CreateRoutesTable(db DB) error {
	_, err := db.ExecContext(context.Background(), `
CREATE TABLE IF NOT EXISTS smeldr_routes (
    id           TEXT NOT NULL PRIMARY KEY,
    path_pattern TEXT NOT NULL UNIQUE,
    route_type   TEXT NOT NULL,
    view         TEXT NOT NULL DEFAULT '',
    type_names   TEXT NOT NULL DEFAULT '[]',
    redirect_to  TEXT,
    status_code  INTEGER,
    is_prefix    INTEGER NOT NULL DEFAULT 0,
    created_at   DATETIME NOT NULL,
    updated_at   DATETIME NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_routes_path ON smeldr_routes (path_pattern)`)
	return err
}

// MigrateRedirectsToRoutes copies all rows from smeldr_redirects into
// smeldr_routes with route_type='redirect', then drops smeldr_redirects.
//
// Idempotent: returns nil immediately if smeldr_redirects does not exist.
// Requires [CreateRoutesTable] to have been called first.
func MigrateRedirectsToRoutes(db DB) error {
	ctx := context.Background()

	// Check whether smeldr_redirects still exists.
	rows, err := db.QueryContext(ctx,
		"SELECT name FROM sqlite_master WHERE type='table' AND name='smeldr_redirects'")
	if err != nil {
		return fmt.Errorf("smeldr: migrate redirects: check table: %w", err)
	}
	exists := rows.Next()
	rows.Close()
	if !exists {
		return nil
	}

	// Read all existing redirect rows.
	srcRows, err := Query[struct {
		From     string `db:"from_path"`
		To       string `db:"to_path"`
		Code     int    `db:"code"`
		IsPrefix bool   `db:"is_prefix"`
	}](ctx, db, "SELECT from_path, to_path, code, is_prefix FROM smeldr_redirects")
	if err != nil {
		return fmt.Errorf("smeldr: migrate redirects: read source: %w", err)
	}

	now := time.Now().UTC()
	for _, r := range srcRows {
		_, err := db.ExecContext(ctx,
			"INSERT OR IGNORE INTO smeldr_routes "+
				"(id, path_pattern, route_type, redirect_to, status_code, is_prefix, created_at, updated_at) "+
				"VALUES ($1, $2, 'redirect', $3, $4, $5, $6, $7)",
			NewID(), r.From, r.To, r.Code, r.IsPrefix, now, now,
		)
		if err != nil {
			return fmt.Errorf("smeldr: migrate redirects: insert %q: %w", r.From, err)
		}
	}

	_, err = db.ExecContext(ctx, "DROP TABLE smeldr_redirects")
	if err != nil {
		return fmt.Errorf("smeldr: migrate redirects: drop table: %w", err)
	}
	return nil
}
