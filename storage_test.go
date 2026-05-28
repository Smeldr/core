package smeldr

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"
)

// ---------------------------------------------------------------------------
// Minimal fake database/sql driver — zero external dependencies
// ---------------------------------------------------------------------------

// fakeResult is set by each test before executing a query. Protected by
// fakeResultMu. Go test functions run sequentially by default so a single
// global is safe without t.Parallel().
var (
	fakeResultMu   sync.Mutex
	fakeResultCols []string
	fakeResultRows [][]driver.Value
)

func setFakeResult(cols []string, rows [][]driver.Value) {
	fakeResultMu.Lock()
	fakeResultCols = cols
	fakeResultRows = rows
	fakeResultMu.Unlock()
}

// fakeQueryMu protects fakeLastQuery and fakeExecRows.
var (
	fakeQueryMu   sync.Mutex
	fakeLastQuery string
	fakeExecRows  int64
)

// setFakeExecRows sets the RowsAffected value returned by the next Exec call.
func setFakeExecRows(n int64) {
	fakeQueryMu.Lock()
	fakeExecRows = n
	fakeQueryMu.Unlock()
}

// getLastQuery returns and clears the last prepared query string.
func getLastQuery() string {
	fakeQueryMu.Lock()
	defer fakeQueryMu.Unlock()
	q := fakeLastQuery
	fakeLastQuery = ""
	return q
}

// fakeExecResult is returned by forgeTestStmt.Exec.
type fakeExecResult struct{ n int64 }

func (r fakeExecResult) LastInsertId() (int64, error) { return 0, nil }
func (r fakeExecResult) RowsAffected() (int64, error) { return r.n, nil }

type forgeTestDriver struct{}

func (forgeTestDriver) Open(_ string) (driver.Conn, error) {
	return &forgeTestConn{}, nil
}

type forgeTestConn struct{}

func (forgeTestConn) Prepare(query string) (driver.Stmt, error) {
	fakeQueryMu.Lock()
	fakeLastQuery = query
	fakeQueryMu.Unlock()
	return &forgeTestStmt{}, nil
}
func (forgeTestConn) Close() error              { return nil }
func (forgeTestConn) Begin() (driver.Tx, error) { return &forgeTestTx{}, nil }

type forgeTestTx struct{}

func (forgeTestTx) Commit() error   { return nil }
func (forgeTestTx) Rollback() error { return nil }

type forgeTestStmt struct{}

func (forgeTestStmt) Close() error  { return nil }
func (forgeTestStmt) NumInput() int { return -1 }
func (forgeTestStmt) Exec(_ []driver.Value) (driver.Result, error) {
	fakeQueryMu.Lock()
	n := fakeExecRows
	fakeQueryMu.Unlock()
	return fakeExecResult{n: n}, nil
}
func (forgeTestStmt) Query(_ []driver.Value) (driver.Rows, error) {
	fakeResultMu.Lock()
	cols := append([]string(nil), fakeResultCols...)
	rows := make([][]driver.Value, len(fakeResultRows))
	for i, r := range fakeResultRows {
		rows[i] = append([]driver.Value(nil), r...)
	}
	fakeResultMu.Unlock()
	return &forgeTestRows{cols: cols, rows: rows}, nil
}

type forgeTestRows struct {
	cols []string
	rows [][]driver.Value
	pos  int
}

func (r *forgeTestRows) Columns() []string { return r.cols }
func (r *forgeTestRows) Close() error      { return nil }
func (r *forgeTestRows) Next(dest []driver.Value) error {
	if r.pos >= len(r.rows) {
		return io.EOF
	}
	copy(dest, r.rows[r.pos])
	r.pos++
	return nil
}

// Register the fake driver once for the entire test binary.
func init() {
	sql.Register("forge_test", forgeTestDriver{})
}

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("forge_test", "")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// ---------------------------------------------------------------------------
// Query[T] tests
// ---------------------------------------------------------------------------

type scanTarget struct {
	ID    string `db:"id"`
	Title string `db:"title"`
}

func TestQueryScansRows(t *testing.T) {
	db := newTestDB(t)
	setFakeResult(
		[]string{"id", "title"},
		[][]driver.Value{
			{"abc", "Hello"},
			{"def", "World"},
		},
	)

	rows, err := Query[scanTarget](context.Background(), db, "SELECT id, title FROM t")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	if rows[0].ID != "abc" || rows[0].Title != "Hello" {
		t.Errorf("row 0: got %+v", rows[0])
	}
	if rows[1].ID != "def" || rows[1].Title != "World" {
		t.Errorf("row 1: got %+v", rows[1])
	}
}

func TestQueryReturnsEmptySliceNotNil(t *testing.T) {
	db := newTestDB(t)
	setFakeResult([]string{"id", "title"}, nil)

	rows, err := Query[scanTarget](context.Background(), db, "SELECT id, title FROM t")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rows == nil {
		t.Fatal("expected empty slice, got nil")
	}
	if len(rows) != 0 {
		t.Errorf("expected 0 rows, got %d", len(rows))
	}
}

func TestQueryPointerType(t *testing.T) {
	db := newTestDB(t)
	setFakeResult(
		[]string{"id", "title"},
		[][]driver.Value{{"xyz", "Pointer"}},
	)

	rows, err := Query[*scanTarget](context.Background(), db, "SELECT id, title FROM t")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0] == nil || rows[0].ID != "xyz" {
		t.Errorf("unexpected row: %+v", rows[0])
	}
}

func TestQueryDiscardsUnknownColumns(t *testing.T) {
	db := newTestDB(t)
	setFakeResult(
		[]string{"id", "unknown_col", "title"},
		[][]driver.Value{{"abc", "ignored", "Hi"}},
	)

	rows, err := Query[scanTarget](context.Background(), db, "SELECT * FROM t")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rows[0].Title != "Hi" {
		t.Errorf("expected Title %q, got %q", "Hi", rows[0].Title)
	}
}

// ---------------------------------------------------------------------------
// QueryOne[T] tests
// ---------------------------------------------------------------------------

func TestQueryOneNotFound(t *testing.T) {
	db := newTestDB(t)
	setFakeResult([]string{"id", "title"}, nil)

	_, err := QueryOne[scanTarget](context.Background(), db, "SELECT id, title FROM t WHERE id = $1", "nope")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestQueryOneReturnsFirst(t *testing.T) {
	db := newTestDB(t)
	setFakeResult(
		[]string{"id", "title"},
		[][]driver.Value{
			{"first", "First"},
			{"second", "Second"},
		},
	)

	row, err := QueryOne[scanTarget](context.Background(), db, "SELECT id, title FROM t")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if row.ID != "first" {
		t.Errorf("expected first row, got ID=%q", row.ID)
	}
}

// ---------------------------------------------------------------------------
// ListOptions tests
// ---------------------------------------------------------------------------

func TestListOptionsOffset(t *testing.T) {
	tests := []struct {
		name    string
		opts    ListOptions
		wantOff int
	}{
		{"page 1 per 10", ListOptions{Page: 1, PerPage: 10}, 0},
		{"page 2 per 10", ListOptions{Page: 2, PerPage: 10}, 10},
		{"page 3 per 5", ListOptions{Page: 3, PerPage: 5}, 10},
		{"page 0 treated as 1", ListOptions{Page: 0, PerPage: 10}, 0},
		{"negative page treated as 1", ListOptions{Page: -1, PerPage: 10}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.opts.Offset(); got != tt.wantOff {
				t.Errorf("Offset() = %d, want %d", got, tt.wantOff)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// MemoryRepo tests
// ---------------------------------------------------------------------------

type repoItem struct {
	ID    string
	Slug  string
	Title string
}

func TestMemoryRepoSaveAndFindByID(t *testing.T) {
	r := NewMemoryRepo[repoItem]()
	ctx := context.Background()

	item := repoItem{ID: "1", Slug: "hello", Title: "Hello"}
	if err := r.Save(ctx, item); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := r.FindByID(ctx, "1")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if got != item {
		t.Errorf("got %+v, want %+v", got, item)
	}
}

func TestMemoryRepoFindBySlug(t *testing.T) {
	r := NewMemoryRepo[repoItem]()
	ctx := context.Background()

	item := repoItem{ID: "2", Slug: "world", Title: "World"}
	_ = r.Save(ctx, item)

	got, err := r.FindBySlug(ctx, "world")
	if err != nil {
		t.Fatalf("FindBySlug: %v", err)
	}
	if got.ID != "2" {
		t.Errorf("expected ID %q, got %q", "2", got.ID)
	}

	_, err = r.FindBySlug(ctx, "no-such-slug")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestMemoryRepoFindAll(t *testing.T) {
	r := NewMemoryRepo[repoItem]()
	ctx := context.Background()

	for i, title := range []string{"Banana", "Apple", "Cherry"} {
		_ = r.Save(ctx, repoItem{ID: string(rune('a' + i)), Slug: title, Title: title})
	}

	t.Run("all items no pagination", func(t *testing.T) {
		all, err := r.FindAll(ctx, ListOptions{})
		if err != nil {
			t.Fatalf("FindAll: %v", err)
		}
		if len(all) != 3 {
			t.Errorf("expected 3 items, got %d", len(all))
		}
	})

	t.Run("pagination page 1", func(t *testing.T) {
		page, err := r.FindAll(ctx, ListOptions{Page: 1, PerPage: 2})
		if err != nil {
			t.Fatalf("FindAll: %v", err)
		}
		if len(page) != 2 {
			t.Errorf("expected 2 items, got %d", len(page))
		}
	})

	t.Run("pagination page 2", func(t *testing.T) {
		page, err := r.FindAll(ctx, ListOptions{Page: 2, PerPage: 2})
		if err != nil {
			t.Fatalf("FindAll: %v", err)
		}
		if len(page) != 1 {
			t.Errorf("expected 1 item, got %d", len(page))
		}
	})

	t.Run("page beyond end", func(t *testing.T) {
		page, err := r.FindAll(ctx, ListOptions{Page: 10, PerPage: 2})
		if err != nil {
			t.Fatalf("FindAll: %v", err)
		}
		if len(page) != 0 {
			t.Errorf("expected 0 items, got %d", len(page))
		}
	})

	t.Run("order by title ascending", func(t *testing.T) {
		all, err := r.FindAll(ctx, ListOptions{OrderBy: "Title"})
		if err != nil {
			t.Fatalf("FindAll: %v", err)
		}
		if all[0].Title != "Apple" || all[1].Title != "Banana" || all[2].Title != "Cherry" {
			t.Errorf("unexpected order: %v", all)
		}
	})

	t.Run("order by title descending", func(t *testing.T) {
		all, err := r.FindAll(ctx, ListOptions{OrderBy: "Title", Desc: true})
		if err != nil {
			t.Fatalf("FindAll: %v", err)
		}
		if all[0].Title != "Cherry" || all[1].Title != "Banana" || all[2].Title != "Apple" {
			t.Errorf("unexpected order: %v", all)
		}
	})
}

func TestMemoryRepoDelete(t *testing.T) {
	r := NewMemoryRepo[repoItem]()
	ctx := context.Background()

	_ = r.Save(ctx, repoItem{ID: "del", Slug: "delete-me", Title: "Delete"})

	if err := r.Delete(ctx, "del"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err := r.FindByID(ctx, "del")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}

	// Second delete returns ErrNotFound.
	if err := r.Delete(ctx, "del"); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound on second delete, got %v", err)
	}
}

func TestMemoryRepoDeleteNotFound(t *testing.T) {
	r := NewMemoryRepo[repoItem]()
	ctx := context.Background()

	err := r.Delete(ctx, "ghost")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestMemoryRepoSaveUpdates(t *testing.T) {
	r := NewMemoryRepo[repoItem]()
	ctx := context.Background()

	_ = r.Save(ctx, repoItem{ID: "u1", Slug: "original", Title: "Original"})
	_ = r.Save(ctx, repoItem{ID: "u1", Slug: "updated", Title: "Updated"})

	got, _ := r.FindByID(ctx, "u1")
	if got.Title != "Updated" {
		t.Errorf("expected Updated, got %q", got.Title)
	}

	// Should still be one item (upsert, not duplicate insert).
	all, _ := r.FindAll(ctx, ListOptions{})
	if len(all) != 1 {
		t.Errorf("expected 1 item after upsert, got %d", len(all))
	}
}

// ---------------------------------------------------------------------------
// Benchmarks
// ---------------------------------------------------------------------------

func BenchmarkQueryScanCached(b *testing.B) {
	db, _ := sql.Open("forge_test", "")
	defer db.Close()

	setFakeResult(
		[]string{"id", "title"},
		[][]driver.Value{{"bench-id", "Bench Title"}},
	)

	ctx := context.Background()
	// Warm up the reflection cache.
	_, _ = Query[scanTarget](ctx, db, "SELECT id, title FROM t")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		setFakeResult(
			[]string{"id", "title"},
			[][]driver.Value{{"bench-id", "Bench Title"}},
		)
		_, _ = Query[scanTarget](ctx, db, "SELECT id, title FROM t")
	}
}

// ---------------------------------------------------------------------------
// SQLRepo[T] test types
// ---------------------------------------------------------------------------

// BlogPost and PageContent are test-only types used to exercise SQLRepo[T]
// table-name derivation and query generation. They live here in the test
// binary only and are not exported from the forge package.
type BlogPost struct {
	ID    string `db:"id"`
	Title string `db:"title"`
}

type PageContent struct {
	ID string `db:"id"`
}

// OrderedItem uses the SQLite reserved keyword "order" as a column name to
// verify that quoteIdent is applied to all generated SQL identifiers.
type OrderedItem struct {
	ID    string `db:"id"`
	Order int    `db:"order"`
}

// ---------------------------------------------------------------------------
// SQLRepo[T] tests
// ---------------------------------------------------------------------------

func TestSQLRepo_tableName_auto(t *testing.T) {
	r := NewSQLRepo[BlogPost](nil)
	if r.table != "blog_posts" {
		t.Errorf("expected blog_posts, got %q", r.table)
	}
	r2 := NewSQLRepo[PageContent](nil)
	if r2.table != "page_contents" {
		t.Errorf("expected page_contents, got %q", r2.table)
	}
}

func TestSQLRepo_tableName_override(t *testing.T) {
	r := NewSQLRepo[BlogPost](nil, Table("posts"))
	if r.table != "posts" {
		t.Errorf("expected posts, got %q", r.table)
	}
}

func TestSQLRepo_FindByID_query(t *testing.T) {
	db := newTestDB(t)
	setFakeResult(
		[]string{"id", "title"},
		[][]driver.Value{{"1", "Hello"}},
	)
	r := NewSQLRepo[BlogPost](db)
	item, err := r.FindByID(context.Background(), "1")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if item.ID != "1" || item.Title != "Hello" {
		t.Errorf("unexpected item: %+v", item)
	}
	want := `SELECT * FROM blog_posts WHERE "id" = $1`
	if q := getLastQuery(); q != want {
		t.Errorf("query = %q, want %q", q, want)
	}
}

func TestSQLRepo_FindBySlug_query(t *testing.T) {
	db := newTestDB(t)
	setFakeResult(
		[]string{"id", "title"},
		[][]driver.Value{{"2", "World"}},
	)
	r := NewSQLRepo[BlogPost](db)
	_, err := r.FindBySlug(context.Background(), "world")
	if err != nil {
		t.Fatalf("FindBySlug: %v", err)
	}
	want := `SELECT * FROM blog_posts WHERE "slug" = $1`
	if q := getLastQuery(); q != want {
		t.Errorf("query = %q, want %q", q, want)
	}
}

func TestSQLRepo_Save_insert(t *testing.T) {
	db := newTestDB(t)
	setFakeExecRows(1)
	r := NewSQLRepo[BlogPost](db)
	err := r.Save(context.Background(), BlogPost{ID: "1", Title: "Hello"})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	want := `INSERT INTO blog_posts ("id", "title") VALUES ($1, $2) ON CONFLICT ("id") DO UPDATE SET "title"=EXCLUDED."title"`
	if q := getLastQuery(); q != want {
		t.Errorf("query = %q, want %q", q, want)
	}
}

func TestSQLRepo_Delete_query(t *testing.T) {
	db := newTestDB(t)
	setFakeExecRows(1)
	r := NewSQLRepo[BlogPost](db)
	err := r.Delete(context.Background(), "1")
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	want := `DELETE FROM blog_posts WHERE "id" = $1`
	if q := getLastQuery(); q != want {
		t.Errorf("query = %q, want %q", q, want)
	}
}

func TestSQLRepo_Delete_notFound(t *testing.T) {
	db := newTestDB(t)
	setFakeExecRows(0)
	r := NewSQLRepo[BlogPost](db)
	err := r.Delete(context.Background(), "ghost")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestSQLRepo_FindAll_noFilter(t *testing.T) {
	db := newTestDB(t)
	setFakeResult(
		[]string{"id", "title"},
		[][]driver.Value{{"1", "Hello"}, {"2", "World"}},
	)
	r := NewSQLRepo[BlogPost](db)
	items, err := r.FindAll(context.Background(), ListOptions{})
	if err != nil {
		t.Fatalf("FindAll: %v", err)
	}
	if len(items) != 2 {
		t.Errorf("expected 2 items, got %d", len(items))
	}
	want := "SELECT * FROM blog_posts"
	if q := getLastQuery(); q != want {
		t.Errorf("query = %q, want %q", q, want)
	}
}

func TestSQLRepo_FindAll_statusFilter(t *testing.T) {
	db := newTestDB(t)
	setFakeResult([]string{"id", "title"}, nil)
	r := NewSQLRepo[BlogPost](db)
	_, err := r.FindAll(context.Background(), ListOptions{Status: []Status{Published}})
	if err != nil {
		t.Fatalf("FindAll: %v", err)
	}
	want := `SELECT * FROM blog_posts WHERE "status" IN ($1)`
	if q := getLastQuery(); q != want {
		t.Errorf("query = %q, want %q", q, want)
	}
}

// TestSQLRepo_ReservedKeyword_quotes verifies that column names that collide
// with SQL reserved keywords (e.g. "order") are double-quoted in every
// generated SQL statement, preventing syntax errors at runtime.
func TestSQLRepo_ReservedKeyword_quotes(t *testing.T) {
	db := newTestDB(t)
	setFakeExecRows(1)
	r := NewSQLRepo[OrderedItem](db)
	err := r.Save(context.Background(), OrderedItem{ID: "1", Order: 5})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	want := `INSERT INTO ordered_items ("id", "order") VALUES ($1, $2) ON CONFLICT ("id") DO UPDATE SET "order"=EXCLUDED."order"`
	if q := getLastQuery(); q != want {
		t.Errorf("Save query = %q, want %q", q, want)
	}
}

// ---------------------------------------------------------------------------
// Seq tests — MemoryRepo and SQLRepo implement SeqRepository[T]
// ---------------------------------------------------------------------------

// seqItem carries a Status field so we can exercise the status filter.
type seqItem struct {
	ID     string `db:"id"`
	Title  string `db:"title"`
	Status string `db:"status"`
}

// compile-time interface checks
var _ SeqRepository[repoItem] = (*MemoryRepo[repoItem])(nil)
var _ SeqRepository[BlogPost] = (*SQLRepo[BlogPost])(nil)

func TestMemoryRepo_Seq_basic(t *testing.T) {
	ctx := context.Background()
	r := NewMemoryRepo[repoItem]()
	items := []repoItem{
		{ID: "1", Slug: "a", Title: "A"},
		{ID: "2", Slug: "b", Title: "B"},
		{ID: "3", Slug: "c", Title: "C"},
	}
	for _, it := range items {
		if err := r.Save(ctx, it); err != nil {
			t.Fatal(err)
		}
	}

	var got []repoItem
	for item, err := range r.Seq(ctx, ListOptions{}) {
		if err != nil {
			t.Fatalf("Seq yielded error: %v", err)
		}
		got = append(got, item)
	}
	if len(got) != 3 {
		t.Fatalf("got %d items, want 3", len(got))
	}
	for i, want := range items {
		if got[i] != want {
			t.Errorf("item[%d] = %+v, want %+v", i, got[i], want)
		}
	}
}

func TestMemoryRepo_Seq_statusFilter(t *testing.T) {
	ctx := context.Background()
	r := NewMemoryRepo[seqItem]()
	_ = r.Save(ctx, seqItem{ID: "1", Status: "published"})
	_ = r.Save(ctx, seqItem{ID: "2", Status: "draft"})
	_ = r.Save(ctx, seqItem{ID: "3", Status: "published"})

	var got []seqItem
	for item, err := range r.Seq(ctx, ListOptions{Status: []Status{Published}}) {
		if err != nil {
			t.Fatalf("Seq yielded error: %v", err)
		}
		got = append(got, item)
	}
	if len(got) != 2 {
		t.Fatalf("got %d items, want 2", len(got))
	}
	for _, it := range got {
		if it.Status != string(Published) {
			t.Errorf("unexpected status %q in result", it.Status)
		}
	}
}

func TestMemoryRepo_Seq_yieldStop(t *testing.T) {
	ctx := context.Background()
	r := NewMemoryRepo[repoItem]()
	for i := range 5 {
		id := fmt.Sprintf("%d", i+1)
		_ = r.Save(ctx, repoItem{ID: id, Slug: id})
	}

	count := 0
	for _, err := range r.Seq(ctx, ListOptions{}) {
		if err != nil {
			t.Fatal(err)
		}
		count++
		if count == 2 {
			break
		}
	}
	if count != 2 {
		t.Errorf("yielded %d items, want 2 (early stop)", count)
	}
}

func TestSQLRepo_Seq_basic(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	setFakeResult(
		[]string{"id", "title"},
		[][]driver.Value{
			{"1", "First"},
			{"2", "Second"},
		},
	)
	r := NewSQLRepo[BlogPost](db)

	var got []BlogPost
	for item, err := range r.Seq(ctx, ListOptions{}) {
		if err != nil {
			t.Fatalf("Seq yielded error: %v", err)
		}
		got = append(got, item)
	}
	if len(got) != 2 {
		t.Fatalf("got %d rows, want 2", len(got))
	}
	if got[0].ID != "1" || got[0].Title != "First" {
		t.Errorf("row[0] = %+v, want {ID:1 Title:First}", got[0])
	}
	if got[1].ID != "2" || got[1].Title != "Second" {
		t.Errorf("row[1] = %+v, want {ID:2 Title:Second}", got[1])
	}
}

func TestSQLRepo_Seq_statusFilter_query(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	setFakeResult([]string{"id", "title", "status"}, nil)
	r := NewSQLRepo[seqItem](db)

	for _, err := range r.Seq(ctx, ListOptions{Status: []Status{Published}}) {
		if err != nil {
			t.Fatalf("Seq error: %v", err)
		}
	}
	want := `SELECT * FROM seq_items WHERE "status" IN ($1)`
	if q := getLastQuery(); q != want {
		t.Errorf("query = %q, want %q", q, want)
	}
}

// ---------------------------------------------------------------------------
// Repository parity suite
//
// runRepoParity defines the behavioural contract shared by all Repository[T]
// implementations. Both MemoryRepo and SQLRepo must satisfy these guarantees.
//
// For SQLRepo the fake driver only handles controlled query/exec results, so
// full parity testing of SQLRepo requires a real DB (see the integration build
// tag tests). This suite therefore runs against MemoryRepo, which is the
// reference implementation that SQLRepo must match.
// ---------------------------------------------------------------------------

// parityItem is the content type used by the parity suite. It carries all
// fields that parity tests exercise: ID, Slug, Title, Status.
type parityItem struct {
	ID     string `db:"id"`
	Slug   string `db:"slug"`
	Title  string `db:"title"`
	Status string `db:"status"`
}

func runRepoParity(t *testing.T, repo Repository[parityItem]) {
	t.Helper()
	ctx := context.Background()

	t.Run("FindByID_notFound", func(t *testing.T) {
		_, err := repo.FindByID(ctx, "missing")
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("FindByID missing: got %v, want ErrNotFound", err)
		}
	})

	t.Run("FindBySlug_notFound", func(t *testing.T) {
		_, err := repo.FindBySlug(ctx, "missing")
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("FindBySlug missing: got %v, want ErrNotFound", err)
		}
	})

	t.Run("FindAll_empty", func(t *testing.T) {
		items, err := repo.FindAll(ctx, ListOptions{})
		if err != nil {
			t.Fatalf("FindAll empty: %v", err)
		}
		if len(items) != 0 {
			t.Errorf("FindAll empty: got %d items, want 0", len(items))
		}
	})

	alpha := parityItem{ID: "1", Slug: "alpha", Title: "Alpha", Status: "published"}
	beta := parityItem{ID: "2", Slug: "beta", Title: "Beta", Status: "draft"}
	gamma := parityItem{ID: "3", Slug: "gamma", Title: "Gamma", Status: "published"}

	for _, it := range []parityItem{alpha, beta, gamma} {
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
		items, err := repo.FindAll(ctx, ListOptions{})
		if err != nil {
			t.Fatalf("FindAll: %v", err)
		}
		if len(items) != 3 {
			t.Errorf("FindAll: got %d items, want 3", len(items))
		}
	})

	t.Run("FindAll_statusFilter", func(t *testing.T) {
		items, err := repo.FindAll(ctx, ListOptions{Status: []Status{Published}})
		if err != nil {
			t.Fatalf("FindAll status: %v", err)
		}
		if len(items) != 2 {
			t.Fatalf("FindAll status: got %d items, want 2", len(items))
		}
		for _, it := range items {
			if it.Status != string(Published) {
				t.Errorf("unexpected status %q", it.Status)
			}
		}
	})

	t.Run("Save_update", func(t *testing.T) {
		updated := parityItem{ID: "1", Slug: "alpha", Title: "Alpha Updated", Status: "published"}
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
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("FindByID deleted: got %v, want ErrNotFound", err)
		}
	})

	t.Run("Delete_notFound", func(t *testing.T) {
		err := repo.Delete(ctx, "2") // already deleted above
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("Delete missing: got %v, want ErrNotFound", err)
		}
	})

	t.Run("FindAll_pagination", func(t *testing.T) {
		items, err := repo.FindAll(ctx, ListOptions{PerPage: 1, Page: 1})
		if err != nil {
			t.Fatalf("FindAll page 1: %v", err)
		}
		if len(items) != 1 {
			t.Errorf("FindAll page 1: got %d items, want 1", len(items))
		}
	})
}

// TestRepoParity_MemoryRepo runs the parity contract against MemoryRepo,
// which is the reference implementation that all Repository backends must match.
func TestRepoParity_MemoryRepo(t *testing.T) {
	runRepoParity(t, NewMemoryRepo[parityItem]())
}
