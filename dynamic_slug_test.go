// AGPL-3.0-or-later

package smeldr

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// newSlugRepo is a DynamicTypeRepo for type "memo" with no schema, so every
// item's slug base is "item", on a database with the block tables (and the
// unique slug index) created.
func newSlugRepo(t *testing.T) (*DynamicTypeRepo, *sql.DB) {
	t.Helper()
	db := newSQLiteDB(t)
	if err := CreateBlockTables(db); err != nil {
		t.Fatalf("CreateBlockTables: %v", err)
	}
	return &DynamicTypeRepo{db: db, typeName: "memo"}, db
}

// insertSlugRow writes a memo row holding slug directly.
func insertSlugRow(t *testing.T, db DB, id, slug string) {
	t.Helper()
	now := time.Now().UTC()
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO smeldr_dynamic_content (id, slug, type_name, created_at, updated_at) VALUES ($1, $2, 'memo', $3, $3)`,
		id, slug, now); err != nil {
		t.Fatalf("insert %s: %v", slug, err)
	}
}

// saturate takes item, item-2 .. item-100 for memo.
func saturate(t *testing.T, db DB) {
	t.Helper()
	insertSlugRow(t, db, NewID(), "item")
	for i := 2; i <= slugCandidates; i++ {
		insertSlugRow(t, db, NewID(), fmt.Sprintf("item-%d", i))
	}
}

// Two creates in one UUIDv7 time window of a saturated type get distinct
// slugs, both reachable: the fallback is the id's random tail, not its
// timestamp prefix.
func TestCreateDraft_SaturatedTypeSlugsDistinct(t *testing.T) {
	repo, db := newSlugRepo(t)
	saturate(t, db)
	a, err := repo.CreateDraft(context.Background(), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := repo.CreateDraft(context.Background(), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if a.Slug == b.Slug {
		t.Fatalf("both creates got slug %q", a.Slug)
	}
	for _, n := range []*DynamicNode{a, b} {
		if want := "item-" + n.ID[len(n.ID)-8:]; n.Slug != want {
			t.Errorf("slug = %q; want %q (the id's random tail)", n.Slug, want)
		}
		got, err := repo.GetBySlug(context.Background(), n.Slug)
		if err != nil || got.ID != n.ID {
			t.Errorf("GetBySlug(%q) = %v, %v; want %s", n.Slug, got, err, n.ID)
		}
	}
}

// The numbered candidates are still used first, in order.
func TestCreateDraft_NumberedSlugsFirst(t *testing.T) {
	repo, db := newSlugRepo(t)
	insertSlugRow(t, db, NewID(), "item")
	insertSlugRow(t, db, NewID(), "item-3")
	n, err := repo.CreateDraft(context.Background(), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if n.Slug != "item-2" {
		t.Errorf("slug = %q; want item-2", n.Slug)
	}
}

// When the random tail is taken too, the slug carries the whole id; an id
// too short for a tail goes straight to the whole id.
func TestFallbackSlug_TailTaken(t *testing.T) {
	repo, db := newSlugRepo(t)
	id := NewID()
	insertSlugRow(t, db, NewID(), "item-"+id[len(id)-8:])
	if got := repo.fallbackSlug(context.Background(), "item", id); got != "item-"+id {
		t.Errorf("fallbackSlug = %q; want item-%s", got, id)
	}
	if got := repo.fallbackSlug(context.Background(), "item", "abc"); got != "item-abc" {
		t.Errorf("fallbackSlug(short id) = %q; want item-abc", got)
	}
}

// countSlugQueriesDB counts the candidate (slug IN) and single-slug probes.
type countSlugQueriesDB struct {
	DB
	in, single int
}

func (c *countSlugQueriesDB) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	switch {
	case strings.Contains(q, "slug IN ("):
		c.in++
	case strings.Contains(q, "slug = $2"):
		c.single++
	}
	return c.DB.QueryContext(ctx, q, args...)
}

// One candidate query per create, plus one probe of the fallback when the
// type is saturated.
func TestCreateDraft_SlugQueryCount(t *testing.T) {
	repo, db := newSlugRepo(t)
	insertSlugRow(t, db, NewID(), "item")
	counter := &countSlugQueriesDB{DB: db}
	repo.db = counter
	if _, err := repo.CreateDraft(context.Background(), map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if counter.in != 1 || counter.single != 0 {
		t.Errorf("unsaturated: in=%d single=%d; want 1 and 0", counter.in, counter.single)
	}
	repo.db = db
	for i := 3; i <= slugCandidates; i++ { // item and item-2 are taken already
		insertSlugRow(t, db, NewID(), fmt.Sprintf("item-%d", i))
	}
	counter = &countSlugQueriesDB{DB: db}
	repo.db = counter
	if _, err := repo.CreateDraft(context.Background(), map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if counter.in != 1 || counter.single != 1 {
		t.Errorf("saturated: in=%d single=%d; want 1 and 1", counter.in, counter.single)
	}
}

// raceInsertDB lets a competing create take the chosen slug just before this
// create's insert, and can fail every insert outright.
type raceInsertDB struct {
	DB
	t       *testing.T
	raced   bool
	failAll bool
}

func (r *raceInsertDB) QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row {
	if strings.Contains(q, "INSERT INTO") && strings.Contains(q, "smeldr_dynamic_content") {
		if !r.raced {
			r.raced = true
			insertSlugRow(r.t, r.DB, NewID(), slugArg(args))
		}
		if r.failAll {
			return r.DB.QueryRowContext(ctx, "SELECT x FROM no_such_table")
		}
	}
	return r.DB.QueryRowContext(ctx, q, args...)
}

// slugArg finds the slug among an insert's args: the value that is not an id
// and starts with "item".
func slugArg(args []any) string {
	for _, a := range args {
		if s, ok := a.(string); ok && strings.HasPrefix(s, "item") {
			return s
		}
	}
	return ""
}

// A create that loses the slug to a concurrent one between the check and the
// insert is refused by the unique index and retried under the fallback.
func TestCreateDraft_RaceRetriesWithFallback(t *testing.T) {
	repo, db := newSlugRepo(t)
	repo.db = &raceInsertDB{DB: db, t: t}
	n, err := repo.CreateDraft(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	if want := "item-" + n.ID[len(n.ID)-8:]; n.Slug != want {
		t.Errorf("slug = %q; want the fallback %q", n.Slug, want)
	}
}

// When the retry fails too, the error is returned.
func TestCreateDraft_RaceRetryFails(t *testing.T) {
	repo, db := newSlugRepo(t)
	repo.db = &raceInsertDB{DB: db, t: t, failAll: true}
	if _, err := repo.CreateDraft(context.Background(), map[string]any{}); err == nil {
		t.Fatal("CreateDraft = nil; want the retry's save error")
	}
}

func slugIndexExists(t *testing.T, db *sql.DB) bool {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_dynamic_content_type_slug'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n == 1
}

// The index refuses a second non-empty slug of one type, allows it across
// types, and allows any number of empty slugs.
func TestCreateBlockTables_UniqueSlugIndex(t *testing.T) {
	_, db := newSlugRepo(t)
	if !slugIndexExists(t, db) {
		t.Fatal("unique slug index missing")
	}
	if err := CreateBlockTables(db); err != nil {
		t.Fatalf("second CreateBlockTables: %v", err)
	}
	insertSlugRow(t, db, "a", "x")
	now := time.Now().UTC()
	if _, err := db.Exec(`INSERT INTO smeldr_dynamic_content (id, slug, type_name, created_at, updated_at) VALUES ('b', 'x', 'memo', ?, ?)`, now, now); err == nil {
		t.Error("a duplicate memo slug was accepted")
	}
	if _, err := db.Exec(`INSERT INTO smeldr_dynamic_content (id, slug, type_name, created_at, updated_at) VALUES ('c', 'x', 'other', ?, ?)`, now, now); err != nil {
		t.Errorf("the same slug in another type: %v", err)
	}
	for _, id := range []string{"d", "e"} {
		if _, err := db.Exec(`INSERT INTO smeldr_dynamic_content (id, slug, type_name, created_at, updated_at) VALUES (?, '', 'memo', ?, ?)`, id, now, now); err != nil {
			t.Errorf("empty slug %s: %v", id, err)
		}
	}
}

// With duplicates already present, CreateBlockTables logs each one, skips the
// index and leaves the rows as they are; once resolved, it creates the index.
func TestCreateBlockTables_DuplicateSlugsSkipIndex(t *testing.T) {
	db := newSQLiteDB(t)
	if _, err := db.Exec(`CREATE TABLE smeldr_dynamic_content (
		id TEXT NOT NULL PRIMARY KEY, slug TEXT NOT NULL DEFAULT '', type_name TEXT NOT NULL,
		status TEXT NOT NULL DEFAULT 'draft', fields TEXT NOT NULL DEFAULT '{}',
		created_at TIMESTAMPTZ NOT NULL, updated_at TIMESTAMPTZ NOT NULL, scheduled_at TIMESTAMPTZ,
		published_at TIMESTAMPTZ, rev INTEGER NOT NULL DEFAULT 0, last_actor TEXT NOT NULL DEFAULT '')`); err != nil {
		t.Fatal(err)
	}
	insertSlugRow(t, db, "id-1", "item-01a11850")
	insertSlugRow(t, db, "id-2", "item-01a11850")

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(prev)

	if err := CreateBlockTables(db); err != nil {
		t.Fatalf("CreateBlockTables: %v", err)
	}
	if slugIndexExists(t, db) {
		t.Error("index created over a duplicate")
	}
	log := buf.String()
	if !strings.Contains(log, "slug=item-01a11850") || !strings.Contains(log, "ids=id-1,id-2") {
		t.Errorf("warning = %q; want the slug and both ids", log)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM smeldr_dynamic_content WHERE slug='item-01a11850'`).Scan(&n); err != nil || n != 2 {
		t.Errorf("rows holding the slug = %d (%v); want both untouched", n, err)
	}

	if _, err := db.Exec(`UPDATE smeldr_dynamic_content SET slug='item-id-2' WHERE id='id-2'`); err != nil {
		t.Fatal(err)
	}
	if err := CreateBlockTables(db); err != nil {
		t.Fatalf("CreateBlockTables after resolving: %v", err)
	}
	if !slugIndexExists(t, db) {
		t.Error("index not created once the duplicate was resolved")
	}
}

// failDuplicateQueryDB fails the duplicate scan.
type failDuplicateQueryDB struct{ DB }

func (f failDuplicateQueryDB) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	if strings.Contains(q, "HAVING COUNT(*) > 1") {
		return nil, fmt.Errorf("scan failed")
	}
	return f.DB.QueryContext(ctx, q, args...)
}

func TestCreateBlockTables_DuplicateScanError(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateBlockTables(failDuplicateQueryDB{db}); err == nil {
		t.Error("CreateBlockTables = nil; want the duplicate scan's error")
	}
}

// failIndexCreateDB refuses the CREATE UNIQUE INDEX statement.
type failIndexCreateDB struct{ DB }

func (f failIndexCreateDB) ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error) {
	if strings.Contains(q, "CREATE UNIQUE INDEX") {
		return nil, fmt.Errorf("index refused")
	}
	return f.DB.ExecContext(ctx, q, args...)
}

// A CREATE INDEX that fails (a duplicate written after the scan, say) does
// not fail boot: it is logged, the index is skipped and CreateBlockTables
// returns nil.
func TestCreateBlockTables_IndexCreateErrorDoesNotFailBoot(t *testing.T) {
	db := newSQLiteDB(t)
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(prev)

	if err := CreateBlockTables(failIndexCreateDB{db}); err != nil {
		t.Fatalf("CreateBlockTables = %v; want nil when only the index is refused", err)
	}
	if slugIndexExists(t, db) {
		t.Error("index exists after a refused create")
	}
	if !strings.Contains(buf.String(), "index refused") {
		t.Errorf("warning = %q; want the index error logged", buf.String())
	}
}
