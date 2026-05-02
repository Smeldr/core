//go:build integration

package forgepgx

import (
	"context"
	"os"
	"testing"

	forge "forge-cms.dev/forge"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestWrap_integration exercises Wrap against a real PostgreSQL instance.
// Run with:
//
//	DATABASE_URL=postgres://user:pass@localhost/testdb \
//	  go test -v -tags integration ./forge-pgx/...
func TestWrap_integration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}

	ctx := context.Background()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	defer pool.Close()

	db := Wrap(pool)

	// Create a temporary table.
	_, err = db.ExecContext(ctx,
		`CREATE TEMP TABLE forgepgx_test (id TEXT PRIMARY KEY, name TEXT NOT NULL)`)
	if err != nil {
		t.Fatalf("ExecContext CREATE: %v", err)
	}

	// Insert a row.
	_, err = db.ExecContext(ctx,
		`INSERT INTO forgepgx_test (id, name) VALUES ($1, $2)`, "1", "hello")
	if err != nil {
		t.Fatalf("ExecContext INSERT: %v", err)
	}

	// QueryRowContext — single row.
	var name string
	r := db.QueryRowContext(ctx, `SELECT name FROM forgepgx_test WHERE id = $1`, "1")
	if err := r.Scan(&name); err != nil {
		t.Fatalf("QueryRowContext Scan: %v", err)
	}
	if name != "hello" {
		t.Fatalf("QueryRowContext: got %q, want %q", name, "hello")
	}

	// QueryContext via forge.Query[T] — confirms the full stack works end-to-end.
	type testRow struct {
		ID   string
		Name string
	}
	rows, err := forge.Query[testRow](ctx, db, `SELECT id, name FROM forgepgx_test`)
	if err != nil {
		t.Fatalf("forge.Query: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("forge.Query: got %d rows, want 1", len(rows))
	}
	if rows[0].Name != "hello" {
		t.Fatalf("forge.Query result: got %q, want %q", rows[0].Name, "hello")
	}
}
