package smeldr

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

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

// ---------------------------------------------------------------------------
// Node.Rev — revision counter and optimistic CAS (A158)
// ---------------------------------------------------------------------------

// revNode is a test content type that embeds Node to get the Rev field.
type revNode struct {
	Node
	Body string `db:"body"`
}

// createRevNodesTable creates the schema that revNode maps to.
func createRevNodesTable(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.ExecContext(context.Background(), `
		CREATE TABLE rev_nodes (
			id           TEXT NOT NULL PRIMARY KEY,
			slug         TEXT NOT NULL DEFAULT '',
			status       TEXT NOT NULL DEFAULT 'draft',
			created_at   DATETIME NOT NULL DEFAULT '',
			updated_at   DATETIME NOT NULL DEFAULT '',
			scheduled_at DATETIME,
			published_at DATETIME,
			rev          INTEGER NOT NULL DEFAULT 0,
			body         TEXT NOT NULL DEFAULT ''
		)`)
	if err != nil {
		t.Fatalf("createRevNodesTable: %v", err)
	}
}

func TestSQLRepo_Save_RevStartsAtZero(t *testing.T) {
	db := newSQLiteDB(t)
	createRevNodesTable(t, db)
	repo := NewSQLRepo[*revNode](db, Table("rev_nodes"))
	ctx := context.Background()

	item := &revNode{Node: Node{ID: "zero-1", Slug: "zero"}}
	if err := repo.Save(ctx, item); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := repo.FindByID(ctx, "zero-1")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if got.Rev != 0 {
		t.Errorf("Rev = %d, want 0", got.Rev)
	}
}

func TestSQLRepo_Save_RevIncrements(t *testing.T) {
	db := newSQLiteDB(t)
	createRevNodesTable(t, db)
	repo := NewSQLRepo[*revNode](db, Table("rev_nodes"))
	ctx := context.Background()

	item := &revNode{Node: Node{ID: "inc-1", Slug: "inc"}}
	// First insert: stored rev = 0.
	if err := repo.Save(ctx, item); err != nil {
		t.Fatalf("first save: %v", err)
	}
	// item.Rev reflects the write-back with no re-read needed.
	if item.Rev != 0 {
		t.Errorf("item.Rev after first save = %d, want 0", item.Rev)
	}
	got, _ := repo.FindByID(ctx, "inc-1")
	if got.Rev != 0 {
		t.Errorf("after first save: Rev = %d, want 0", got.Rev)
	}

	// Second save reusing item directly (no re-read) — Save's own write-back
	// keeps item.Rev current, so this is the "matches stored" case again.
	if err := repo.Save(ctx, item); err != nil {
		t.Fatalf("second save: %v", err)
	}
	if item.Rev != 1 {
		t.Errorf("item.Rev after second save = %d, want 1", item.Rev)
	}
	got, _ = repo.FindByID(ctx, "inc-1")
	if got.Rev != 1 {
		t.Errorf("after second save: Rev = %d, want 1", got.Rev)
	}

	// Third save, same item, same reasoning: no re-read was ever needed.
	if err := repo.Save(ctx, item); err != nil {
		t.Fatalf("third save: %v", err)
	}
	if item.Rev != 2 {
		t.Errorf("item.Rev after third save = %d, want 2", item.Rev)
	}
	got, _ = repo.FindByID(ctx, "inc-1")
	if got.Rev != 2 {
		t.Errorf("after third save: Rev = %d, want 2", got.Rev)
	}
}

func TestSQLRepo_Save_RevConflict(t *testing.T) {
	db := newSQLiteDB(t)
	createRevNodesTable(t, db)
	repo := NewSQLRepo[*revNode](db, Table("rev_nodes"))
	ctx := context.Background()

	item := &revNode{Node: Node{ID: "cas-1", Slug: "cas"}}
	// First insert: stored rev = 0.
	if err := repo.Save(ctx, item); err != nil {
		t.Fatalf("first save: %v", err)
	}
	// A second holder reads the item at rev 0 and never advances its own
	// copy — this manufactures a genuinely stale write, independent of
	// what item.Rev becomes after the next Save.
	stale := &revNode{Node: Node{ID: "cas-1", Slug: "cas", Rev: item.Rev}}

	// Second save with item.Rev = 0: WHERE rev=0 matches stored rev=0 → update, stored rev→1.
	if err := repo.Save(ctx, item); err != nil {
		t.Fatalf("second save: %v", err)
	}
	// Save writes the database-computed rev back onto item — no re-read needed.
	if item.Rev != 1 {
		t.Fatalf("item.Rev after second save = %d, want 1 (write-back)", item.Rev)
	}

	// stale.Rev is still 0: WHERE rev=0 fails (stored rev=1) → ErrRevConflict.
	if err := repo.Save(ctx, stale); !errors.Is(err, ErrRevConflict) {
		t.Errorf("expected ErrRevConflict, got %v", err)
	}
}

// TestSQLRepo_Save_WritesBackTimestamps proves UpdatedAt/CreatedAt are
// written onto the caller's own struct with no re-read needed — the part
// of Save's write-back contract beyond Rev.
func TestSQLRepo_Save_WritesBackTimestamps(t *testing.T) {
	db := newSQLiteDB(t)
	createRevNodesTable(t, db)
	repo := NewSQLRepo[*revNode](db, Table("rev_nodes"))
	ctx := context.Background()

	item := &revNode{Node: Node{ID: "ts-1", Slug: "ts"}}
	if err := repo.Save(ctx, item); err != nil {
		t.Fatalf("first save: %v", err)
	}
	if item.UpdatedAt.IsZero() {
		t.Error("item.UpdatedAt is zero after first save, want non-zero (write-back)")
	}
	if item.CreatedAt.IsZero() {
		t.Error("item.CreatedAt is zero after first save, want non-zero (write-back)")
	}
	firstCreatedAt := item.CreatedAt

	// A second save must not change CreatedAt (only set when previously zero).
	if err := repo.Save(ctx, item); err != nil {
		t.Fatalf("second save: %v", err)
	}
	if !item.CreatedAt.Equal(firstCreatedAt) {
		t.Errorf("item.CreatedAt changed on second save: got %v, want %v", item.CreatedAt, firstCreatedAt)
	}
}

// TestSQLRepo_ScheduledAt_nonNil_roundTrips is the actual regression this
// task exists for (T210): a Save+FindBySlug round-trip on a Node.ScheduledAt
// field, a nullable *time.Time, previously failed with "sql: Scan error on
// column index N, name \"scheduled_at\": unsupported Scan, storing
// driver.Value type string into type *time.Time" — scanDest's *time.Time
// case does not match a nullable field's own **time.Time address. Fixed via
// nullTimeScanner.
func TestSQLRepo_ScheduledAt_nonNil_roundTrips(t *testing.T) {
	db := newSQLiteDB(t)
	createRevNodesTable(t, db)
	repo := NewSQLRepo[*revNode](db, Table("rev_nodes"))
	ctx := context.Background()

	at := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	item := &revNode{Node: Node{ID: "sched-1", Slug: "sched", ScheduledAt: &at}}
	if err := repo.Save(ctx, item); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := repo.FindBySlug(ctx, "sched")
	if err != nil {
		t.Fatalf("FindBySlug: %v", err)
	}
	if got.ScheduledAt == nil {
		t.Fatal("got.ScheduledAt is nil, want non-nil")
	}
	if !got.ScheduledAt.Equal(at) {
		t.Errorf("got.ScheduledAt = %v, want %v", got.ScheduledAt, at)
	}
}

// TestSQLRepo_ScheduledAt_nil_roundTrips pins the already-working nil case —
// the fix must not regress it.
func TestSQLRepo_ScheduledAt_nil_roundTrips(t *testing.T) {
	db := newSQLiteDB(t)
	createRevNodesTable(t, db)
	repo := NewSQLRepo[*revNode](db, Table("rev_nodes"))
	ctx := context.Background()

	item := &revNode{Node: Node{ID: "unsched-1", Slug: "unsched"}}
	if err := repo.Save(ctx, item); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := repo.FindBySlug(ctx, "unsched")
	if err != nil {
		t.Fatalf("FindBySlug: %v", err)
	}
	if got.ScheduledAt != nil {
		t.Errorf("got.ScheduledAt = %v, want nil", got.ScheduledAt)
	}
}

func TestMigrateNodeRevColumn_AddsColumn(t *testing.T) {
	db := newSQLiteDB(t)
	_, err := db.ExecContext(context.Background(), `
		CREATE TABLE migrate_rev_test (
			id   TEXT NOT NULL PRIMARY KEY,
			slug TEXT NOT NULL DEFAULT ''
		)`)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}

	if err := MigrateNodeRevColumn(db, "migrate_rev_test"); err != nil {
		t.Fatalf("MigrateNodeRevColumn: %v", err)
	}

	// Column must exist — insert a row that uses it.
	_, err = db.ExecContext(context.Background(),
		`INSERT INTO migrate_rev_test (id, slug, rev) VALUES ('1', 'test', 0)`)
	if err != nil {
		t.Errorf("rev column should exist after migration, got: %v", err)
	}
}

func TestMigrateNodeRevColumn_Idempotent(t *testing.T) {
	db := newSQLiteDB(t)
	_, _ = db.ExecContext(context.Background(), `
		CREATE TABLE idempotent_rev_test (
			id  TEXT NOT NULL PRIMARY KEY,
			rev INTEGER NOT NULL DEFAULT 0
		)`)

	if err := MigrateNodeRevColumn(db, "idempotent_rev_test"); err != nil {
		t.Errorf("first call: %v", err)
	}
	if err := MigrateNodeRevColumn(db, "idempotent_rev_test"); err != nil {
		t.Errorf("second call: %v", err)
	}
}

// TestTimeScanner covers the SQL-scanner wrapper for time.Time destinations.
func TestTimeScanner(t *testing.T) {
	ref := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)

	t.Run("time.Time_src", func(t *testing.T) {
		var dst time.Time
		ts := timeScanner{dst: &dst}
		if err := ts.Scan(ref); err != nil {
			t.Fatalf("Scan: %v", err)
		}
		if !dst.Equal(ref) {
			t.Errorf("dst = %v, want %v", dst, ref)
		}
	})

	t.Run("RFC3339Nano_string", func(t *testing.T) {
		var dst time.Time
		ts := timeScanner{dst: &dst}
		if err := ts.Scan(ref.Format(time.RFC3339Nano)); err != nil {
			t.Fatalf("Scan: %v", err)
		}
		if !dst.Equal(ref) {
			t.Errorf("dst = %v, want %v", dst, ref)
		}
	})

	t.Run("bytes_src", func(t *testing.T) {
		var dst time.Time
		ts := timeScanner{dst: &dst}
		if err := ts.Scan([]byte(ref.Format(time.RFC3339Nano))); err != nil {
			t.Fatalf("Scan: %v", err)
		}
		if !dst.Equal(ref) {
			t.Errorf("dst = %v, want %v", dst, ref)
		}
	})

	t.Run("int64_unix_src", func(t *testing.T) {
		var dst time.Time
		ts := timeScanner{dst: &dst}
		unix := ref.Unix()
		if err := ts.Scan(unix); err != nil {
			t.Fatalf("Scan: %v", err)
		}
		if !dst.Equal(ref) {
			t.Errorf("dst = %v, want %v", dst, ref)
		}
	})

	t.Run("nil_src_zero_time", func(t *testing.T) {
		var dst time.Time
		ts := timeScanner{dst: &dst}
		if err := ts.Scan(nil); err != nil {
			t.Fatalf("Scan: %v", err)
		}
		if !dst.IsZero() {
			t.Errorf("dst = %v, want zero", dst)
		}
	})

	t.Run("unparseable_string_error", func(t *testing.T) {
		var dst time.Time
		ts := timeScanner{dst: &dst}
		err := ts.Scan("not-a-date")
		if err == nil {
			t.Error("Scan: expected error, got nil")
		}
	})

	t.Run("unsupported_type_error", func(t *testing.T) {
		var dst time.Time
		ts := timeScanner{dst: &dst}
		err := ts.Scan(3.14)
		if err == nil {
			t.Error("Scan: expected error for float64 src, got nil")
		}
	})
}

// TestNullTimeScanner covers the SQL-scanner wrapper for nullable *time.Time
// destinations (T210) — the same value shapes TestTimeScanner covers, plus
// the nil-means-nil-pointer case timeScanner itself does not have (its own
// nil case sets a zero time.Time, not a nil pointer).
func TestNullTimeScanner(t *testing.T) {
	ref := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)

	t.Run("time.Time_src", func(t *testing.T) {
		var dst *time.Time
		ns := nullTimeScanner{dst: &dst}
		if err := ns.Scan(ref); err != nil {
			t.Fatalf("Scan: %v", err)
		}
		if dst == nil || !dst.Equal(ref) {
			t.Errorf("dst = %v, want %v", dst, ref)
		}
	})

	t.Run("RFC3339Nano_string", func(t *testing.T) {
		var dst *time.Time
		ns := nullTimeScanner{dst: &dst}
		if err := ns.Scan(ref.Format(time.RFC3339Nano)); err != nil {
			t.Fatalf("Scan: %v", err)
		}
		if dst == nil || !dst.Equal(ref) {
			t.Errorf("dst = %v, want %v", dst, ref)
		}
	})

	t.Run("bytes_src", func(t *testing.T) {
		var dst *time.Time
		ns := nullTimeScanner{dst: &dst}
		if err := ns.Scan([]byte(ref.Format(time.RFC3339Nano))); err != nil {
			t.Fatalf("Scan: %v", err)
		}
		if dst == nil || !dst.Equal(ref) {
			t.Errorf("dst = %v, want %v", dst, ref)
		}
	})

	t.Run("int64_unix_src", func(t *testing.T) {
		var dst *time.Time
		ns := nullTimeScanner{dst: &dst}
		if err := ns.Scan(ref.Unix()); err != nil {
			t.Fatalf("Scan: %v", err)
		}
		if dst == nil || !dst.Equal(ref) {
			t.Errorf("dst = %v, want %v", dst, ref)
		}
	})

	t.Run("nil_src_nil_pointer", func(t *testing.T) {
		dst := &ref // deliberately non-nil beforehand, to prove Scan(nil) resets it
		ns := nullTimeScanner{dst: &dst}
		if err := ns.Scan(nil); err != nil {
			t.Fatalf("Scan: %v", err)
		}
		if dst != nil {
			t.Errorf("dst = %v, want nil", dst)
		}
	})

	t.Run("unparseable_string_error", func(t *testing.T) {
		var dst *time.Time
		ns := nullTimeScanner{dst: &dst}
		err := ns.Scan("not-a-date")
		if err == nil {
			t.Error("Scan: expected error, got nil")
		}
	})

	t.Run("unsupported_type_error", func(t *testing.T) {
		var dst *time.Time
		ns := nullTimeScanner{dst: &dst}
		err := ns.Scan(3.14)
		if err == nil {
			t.Error("Scan: expected error for float64 src, got nil")
		}
	})
}

func TestMemoryRepo_Save_RevIncrement(t *testing.T) {
	repo := NewMemoryRepo[*revNode]()
	ctx := context.Background()

	item := &revNode{Node: Node{ID: "mem-1", Slug: "mem"}}

	// First save (insert): Rev stays 0.
	if err := repo.Save(ctx, item); err != nil {
		t.Fatalf("first save: %v", err)
	}
	if item.Rev != 0 {
		t.Errorf("after first save: item.Rev = %d, want 0", item.Rev)
	}

	// Second save (update): Rev incremented to 1 via pointer.
	if err := repo.Save(ctx, item); err != nil {
		t.Fatalf("second save: %v", err)
	}
	if item.Rev != 1 {
		t.Errorf("after second save: item.Rev = %d, want 1", item.Rev)
	}

	// Third save (update): Rev incremented to 2.
	if err := repo.Save(ctx, item); err != nil {
		t.Fatalf("third save: %v", err)
	}
	if item.Rev != 2 {
		t.Errorf("after third save: item.Rev = %d, want 2", item.Rev)
	}
}
