//go:build integration

package forgepgx

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"smeldr.dev/core"
)

// TestWrap_integration exercises Wrap against a real PostgreSQL instance.
// Run with:
//
//	DATABASE_URL=postgres://user:pass@localhost/testdb \
//	  go test -v -tags integration ./pgx/...
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

	// QueryContext via smeldr.Query[T] — confirms the full stack works end-to-end.
	type testRow struct {
		ID   string
		Name string
	}
	rows, err := smeldr.Query[testRow](ctx, db, `SELECT id, name FROM forgepgx_test`)
	if err != nil {
		t.Fatalf("smeldr.Query: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("smeldr.Query: got %d rows, want 1", len(rows))
	}
	if rows[0].Name != "hello" {
		t.Fatalf("smeldr.Query result: got %q, want %q", rows[0].Name, "hello")
	}
}

// ---------------------------------------------------------------------------
// Repository parity suite
//
// pgxParityItem mirrors parityItem from forge/storage_test.go. It cannot be
// imported directly because pgx is a separate Go module and internal
// packages are not accessible across module boundaries (Approach A).
// ---------------------------------------------------------------------------

// pgxParityItem is the content type used by the pgx parity suite.
type pgxParityItem struct {
	ID     string `db:"id"`
	Slug   string `db:"slug"`
	Title  string `db:"title"`
	Status string `db:"status"`
}

// runForgePgxRepoParity defines the behavioural contract for Repository[T]
// and runs it against the provided repo. Mirrors runRepoParity in
// forge/storage_test.go — both must stay in sync with the parity contract.
func runForgePgxRepoParity(t *testing.T, repo smeldr.Repository[pgxParityItem]) {
	t.Helper()
	ctx := context.Background()

	t.Run("FindByID_notFound", func(t *testing.T) {
		_, err := repo.FindByID(ctx, "missing")
		if !isErrNotFound(err) {
			t.Errorf("FindByID missing: got %v, want ErrNotFound", err)
		}
	})

	t.Run("FindBySlug_notFound", func(t *testing.T) {
		_, err := repo.FindBySlug(ctx, "missing")
		if !isErrNotFound(err) {
			t.Errorf("FindBySlug missing: got %v, want ErrNotFound", err)
		}
	})

	t.Run("FindAll_empty", func(t *testing.T) {
		items, err := repo.FindAll(ctx, smeldr.ListOptions{})
		if err != nil {
			t.Fatalf("FindAll empty: %v", err)
		}
		if len(items) != 0 {
			t.Errorf("FindAll empty: got %d items, want 0", len(items))
		}
	})

	alpha := pgxParityItem{ID: "1", Slug: "alpha", Title: "Alpha", Status: "published"}
	beta := pgxParityItem{ID: "2", Slug: "beta", Title: "Beta", Status: "draft"}
	gamma := pgxParityItem{ID: "3", Slug: "gamma", Title: "Gamma", Status: "published"}

	for _, it := range []pgxParityItem{alpha, beta, gamma} {
		if err := repo.Save(ctx, it); err != nil {
			t.Fatalf("Save %s: %v", it.ID, err)
		}
	}

	t.Run("Save_FindByID", func(t *testing.T) {
		got, err := repo.FindByID(ctx, "1")
		if err != nil {
			t.Fatalf("FindByID: %v", err)
		}
		if got != alpha {
			t.Errorf("FindByID: got %+v, want %+v", got, alpha)
		}
	})

	t.Run("Save_FindBySlug", func(t *testing.T) {
		got, err := repo.FindBySlug(ctx, "beta")
		if err != nil {
			t.Fatalf("FindBySlug: %v", err)
		}
		if got != beta {
			t.Errorf("FindBySlug: got %+v, want %+v", got, beta)
		}
	})

	t.Run("FindAll_all", func(t *testing.T) {
		items, err := repo.FindAll(ctx, smeldr.ListOptions{})
		if err != nil {
			t.Fatalf("FindAll: %v", err)
		}
		if len(items) != 3 {
			t.Errorf("FindAll: got %d items, want 3", len(items))
		}
	})

	t.Run("FindAll_statusFilter", func(t *testing.T) {
		items, err := repo.FindAll(ctx, smeldr.ListOptions{Status: []smeldr.Status{smeldr.Published}})
		if err != nil {
			t.Fatalf("FindAll status: %v", err)
		}
		if len(items) != 2 {
			t.Fatalf("FindAll status: got %d items, want 2", len(items))
		}
		for _, it := range items {
			if it.Status != string(smeldr.Published) {
				t.Errorf("unexpected status %q", it.Status)
			}
		}
	})

	t.Run("Save_update", func(t *testing.T) {
		updated := pgxParityItem{ID: "1", Slug: "alpha", Title: "Alpha Updated", Status: "published"}
		if err := repo.Save(ctx, updated); err != nil {
			t.Fatalf("Save update: %v", err)
		}
		got, err := repo.FindByID(ctx, "1")
		if err != nil {
			t.Fatalf("FindByID after update: %v", err)
		}
		if got.Title != "Alpha Updated" {
			t.Errorf("after update Title = %q, want %q", got.Title, "Alpha Updated")
		}
	})

	t.Run("Delete", func(t *testing.T) {
		if err := repo.Delete(ctx, "2"); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		_, err := repo.FindByID(ctx, "2")
		if !isErrNotFound(err) {
			t.Errorf("FindByID deleted: got %v, want ErrNotFound", err)
		}
	})

	t.Run("Delete_notFound", func(t *testing.T) {
		err := repo.Delete(ctx, "2") // already deleted above
		if !isErrNotFound(err) {
			t.Errorf("Delete missing: got %v, want ErrNotFound", err)
		}
	})

	t.Run("FindAll_pagination", func(t *testing.T) {
		items, err := repo.FindAll(ctx, smeldr.ListOptions{PerPage: 1, Page: 1})
		if err != nil {
			t.Fatalf("FindAll page 1: %v", err)
		}
		if len(items) != 1 {
			t.Errorf("FindAll page 1: got %d items, want 1", len(items))
		}
	})
}

// isErrNotFound reports whether err is (or wraps) smeldr.ErrNotFound.
func isErrNotFound(err error) bool {
	if err == nil {
		return false
	}
	var fe smeldr.Error
	if errors.As(err, &fe) {
		return fe.Code() == smeldr.ErrNotFound.Code()
	}
	return errors.Is(err, smeldr.ErrNotFound)
}

// TestRepoParity_pgx runs the full Repository parity suite against a real
// PostgreSQL instance via the pgx adapter. Requires DATABASE_URL and the
// integration build tag.
func TestRepoParity_pgx(t *testing.T) {
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

	_, err = db.ExecContext(ctx, `CREATE TABLE parity_items (
		id     TEXT NOT NULL PRIMARY KEY,
		slug   TEXT NOT NULL,
		title  TEXT NOT NULL,
		status TEXT NOT NULL
	)`)
	if err != nil {
		t.Fatalf("create parity_items: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DROP TABLE parity_items`)
	})

	repo := smeldr.NewSQLRepo[pgxParityItem](db, smeldr.Table("parity_items"))
	runForgePgxRepoParity(t, repo)
}
