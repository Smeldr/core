package smeldr

import (
	"context"
	"testing"
)

func TestCreateRoutesTable_Idempotent(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateRoutesTable(db); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if err := CreateRoutesTable(db); err != nil {
		t.Fatalf("second call (idempotency): %v", err)
	}
}

func TestMigrateRedirectsToRoutes_Idempotent(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateRoutesTable(db); err != nil {
		t.Fatalf("CreateRoutesTable: %v", err)
	}
	// No smeldr_redirects table — must be a no-op.
	if err := MigrateRedirectsToRoutes(db); err != nil {
		t.Fatalf("MigrateRedirectsToRoutes (no source table): %v", err)
	}
	// Second call also no-op.
	if err := MigrateRedirectsToRoutes(db); err != nil {
		t.Fatalf("MigrateRedirectsToRoutes (second call): %v", err)
	}
}

func TestMigrateRedirectsToRoutes_CopiesAndDrops(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateRoutesTable(db); err != nil {
		t.Fatalf("CreateRoutesTable: %v", err)
	}

	// Create the legacy table and seed two rows.
	if _, err := db.ExecContext(context.Background(), `
CREATE TABLE smeldr_redirects (
    from_path TEXT NOT NULL PRIMARY KEY,
    to_path   TEXT NOT NULL DEFAULT '',
    code      INTEGER NOT NULL DEFAULT 301,
    is_prefix INTEGER NOT NULL DEFAULT 0
)`); err != nil {
		t.Fatalf("create smeldr_redirects: %v", err)
	}
	if _, err := db.ExecContext(context.Background(),
		"INSERT INTO smeldr_redirects (from_path, to_path, code, is_prefix) VALUES ('/old', '/new', 301, 0), ('/posts', '/articles', 301, 1)",
	); err != nil {
		t.Fatalf("seed smeldr_redirects: %v", err)
	}

	if err := MigrateRedirectsToRoutes(db); err != nil {
		t.Fatalf("MigrateRedirectsToRoutes: %v", err)
	}

	// smeldr_redirects must be gone.
	rows, err := db.QueryContext(context.Background(),
		"SELECT name FROM sqlite_master WHERE type='table' AND name='smeldr_redirects'")
	if err != nil {
		t.Fatalf("check table existence: %v", err)
	}
	if rows.Next() {
		rows.Close()
		t.Error("smeldr_redirects still exists after migration")
	}
	rows.Close()

	// Two redirect rows must be in smeldr_routes.
	type routeRow struct {
		PathPattern string `db:"path_pattern"`
		RouteType   string `db:"route_type"`
		RedirectTo  string `db:"redirect_to"`
		StatusCode  int    `db:"status_code"`
		IsPrefix    bool   `db:"is_prefix"`
	}
	results, err := Query[routeRow](context.Background(), db,
		"SELECT path_pattern, route_type, redirect_to, status_code, is_prefix FROM smeldr_routes WHERE route_type='redirect' ORDER BY path_pattern")
	if err != nil {
		t.Fatalf("query smeldr_routes: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("want 2 rows, got %d", len(results))
	}

	r0 := results[0]
	if r0.PathPattern != "/old" || r0.RouteType != "redirect" || r0.RedirectTo != "/new" || r0.StatusCode != 301 || r0.IsPrefix {
		t.Errorf("row 0 unexpected: %+v", r0)
	}
	r1 := results[1]
	if r1.PathPattern != "/posts" || r1.RedirectTo != "/articles" || !r1.IsPrefix {
		t.Errorf("row 1 unexpected: %+v", r1)
	}
}

func TestMigrateRedirectsToRoutes_Idempotent_AfterMigration(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateRoutesTable(db); err != nil {
		t.Fatalf("CreateRoutesTable: %v", err)
	}
	// Seed and migrate once.
	if _, err := db.ExecContext(context.Background(), `
CREATE TABLE smeldr_redirects (
    from_path TEXT NOT NULL PRIMARY KEY,
    to_path   TEXT NOT NULL DEFAULT '',
    code      INTEGER NOT NULL DEFAULT 301,
    is_prefix INTEGER NOT NULL DEFAULT 0
)`); err != nil {
		t.Fatalf("create smeldr_redirects: %v", err)
	}
	if _, err := db.ExecContext(context.Background(),
		"INSERT INTO smeldr_redirects VALUES ('/a', '/b', 301, 0)"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := MigrateRedirectsToRoutes(db); err != nil {
		t.Fatalf("first migration: %v", err)
	}
	// Second call must be no-op (source table already dropped).
	if err := MigrateRedirectsToRoutes(db); err != nil {
		t.Fatalf("second migration: %v", err)
	}
}
