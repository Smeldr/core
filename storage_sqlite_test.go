package smeldr

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

// newSQLiteDB opens an in-memory SQLite database and registers cleanup.
// MaxOpenConns is set to 1 because SQLite :memory: databases are per-connection
// — allowing multiple connections would give each its own empty database.
// The test is skipped if SQLite is unavailable.
func newSQLiteDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Skipf("sqlite unavailable: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	return db
}

// createParityItemsTable creates the schema that parityItem maps to.
// parityItem derives its table name as "parity_items" (camelToSnake + "s").
func createParityItemsTable(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.ExecContext(context.Background(), `
		CREATE TABLE parity_items (
			id     TEXT NOT NULL PRIMARY KEY,
			slug   TEXT NOT NULL,
			title  TEXT NOT NULL,
			status TEXT NOT NULL
		)`)
	if err != nil {
		t.Fatalf("createParityItemsTable: %v", err)
	}
}

// TestRepoParity_SQLRepo runs the full parity contract (11 sub-tests) against a
// real in-memory SQLite database. This verifies that SQLRepo[T] correctly
// executes INSERT ... ON CONFLICT, DELETE with RowsAffected, SELECT with status
// filter, and LIMIT/OFFSET pagination against an actual SQL engine — not just
// the fake driver used by the other SQLRepo tests.
func TestRepoParity_SQLRepo(t *testing.T) {
	sqldb := newSQLiteDB(t)
	createParityItemsTable(t, sqldb)
	repo := NewSQLRepo[parityItem](sqldb)
	runRepoParity(t, repo)
}

func TestSQLRepo_FindAll_OrderBy_knownField(t *testing.T) {
	sqldb := newSQLiteDB(t)
	createParityItemsTable(t, sqldb)
	repo := NewSQLRepo[parityItem](sqldb)
	ctx := context.Background()

	items := []parityItem{
		{ID: "1", Slug: "b", Title: "Banana", Status: "draft"},
		{ID: "2", Slug: "a", Title: "Apple", Status: "draft"},
	}
	for _, it := range items {
		if err := repo.Save(ctx, it); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	results, err := repo.FindAll(ctx, ListOptions{OrderBy: "Title"})
	if err != nil {
		t.Fatalf("FindAll OrderBy Title: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Title != "Apple" {
		t.Errorf("results[0].Title = %q, want \"Apple\"", results[0].Title)
	}
}

func TestSQLRepo_FindAll_OrderBy_unknownField(t *testing.T) {
	sqldb := newSQLiteDB(t)
	createParityItemsTable(t, sqldb)
	repo := NewSQLRepo[parityItem](sqldb)
	ctx := context.Background()

	// Unknown field: columnForField returns false, no ORDER BY is added.
	results, err := repo.FindAll(ctx, ListOptions{OrderBy: "NonExistentField"})
	if err != nil {
		t.Fatalf("FindAll unknown OrderBy: %v", err)
	}
	_ = results // result count not important; we just verify no error
}
