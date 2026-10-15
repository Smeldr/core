//go:build integration

package pgx

import (
	"context"
	"fmt"
	"testing"
	"time"

	smeldr "smeldr.dev/core"
)

// Dynamic slugs on Postgres: the published-only unique (type_name, slug) index exists, the
// 100-candidate query works with numbered placeholders, and two creates in one
// time window of a saturated type get distinct slugs.
func TestPG_DynamicSlugs(t *testing.T) {
	db, _ := isolatedDB(t)
	ctx := context.Background()
	_, repo := dynamicApp(t, db, nil)

	var n int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM pg_indexes WHERE indexname = 'idx_dynamic_content_type_slug_published'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("unique slug index count = %d (%v); want 1", n, err)
	}

	now := time.Now().UTC()
	insert := func(id, slug string) error {
		_, err := db.ExecContext(ctx,
			`INSERT INTO smeldr_dynamic_content (id, slug, type_name, status, created_at, updated_at) VALUES ($1, $2, 'memo', 'published', $3, $3)`,
			id, slug, now)
		return err
	}
	if err := insert(smeldr.NewID(), "x"); err != nil {
		t.Fatal(err)
	}
	for i := 2; i <= 100; i++ {
		if err := insert(smeldr.NewID(), fmt.Sprintf("x-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := insert(smeldr.NewID(), "x"); err == nil {
		t.Error("a second published memo slug was accepted")
	}
	// A draft reserves no URL: it may share a published slug.
	if _, err := db.ExecContext(ctx,
		`INSERT INTO smeldr_dynamic_content (id, slug, type_name, created_at, updated_at) VALUES ($1, 'x', 'memo', $2, $2)`,
		smeldr.NewID(), now); err != nil {
		t.Errorf("a draft sharing a published slug: %v", err)
	}

	a, err := repo.CreateDraft(ctx, map[string]any{"Title": "x"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := repo.CreateDraft(ctx, map[string]any{"Title": "x"})
	if err != nil {
		t.Fatal(err)
	}
	if a.Slug == b.Slug {
		t.Fatalf("both creates got slug %q", a.Slug)
	}
	for _, node := range []*smeldr.DynamicNode{a, b} {
		if want := "x-" + node.ID[len(node.ID)-8:]; node.Slug != want {
			t.Errorf("slug = %q; want %q", node.Slug, want)
		}
	}
}

// With a published duplicate already present on Postgres, CreateBlockTables skips the
// index and keeps both rows; once resolved, the next call creates it.
func TestPG_DynamicSlugDuplicateSkipsIndex(t *testing.T) {
	db, _ := isolatedDB(t)
	ctx := context.Background()
	if err := smeldr.CreateBlockTables(db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `DROP INDEX idx_dynamic_content_type_slug_published`); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, id := range []string{"id-1", "id-2"} {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO smeldr_dynamic_content (id, slug, type_name, status, created_at, updated_at) VALUES ($1, 'dup', 'memo', 'published', $2, $2)`,
			id, now); err != nil {
			t.Fatal(err)
		}
	}
	indexCount := func() int {
		var n int
		if err := db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM pg_indexes WHERE indexname = 'idx_dynamic_content_type_slug_published'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if err := smeldr.CreateBlockTables(db); err != nil {
		t.Fatalf("CreateBlockTables with a duplicate: %v", err)
	}
	if indexCount() != 0 {
		t.Error("index created over a duplicate")
	}
	if _, err := db.ExecContext(ctx, `UPDATE smeldr_dynamic_content SET slug = 'dup-2' WHERE id = 'id-2'`); err != nil {
		t.Fatal(err)
	}
	if err := smeldr.CreateBlockTables(db); err != nil {
		t.Fatal(err)
	}
	if indexCount() != 1 {
		t.Error("index not created once resolved")
	}
}

// A database from before core-drafts-do-not-reserve-a-url has the all-status
// index: the next boot creates the published-only one, drops the old one, and
// two drafts may then share a slug.
func TestPG_DynamicSlugIndexMigrates(t *testing.T) {
	db, _ := isolatedDB(t)
	ctx := context.Background()
	if err := smeldr.CreateBlockTables(db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `DROP INDEX idx_dynamic_content_type_slug_published`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx,
		`CREATE UNIQUE INDEX idx_dynamic_content_type_slug ON smeldr_dynamic_content (type_name, slug) WHERE slug <> ''`); err != nil {
		t.Fatal(err)
	}
	if err := smeldr.CreateBlockTables(db); err != nil {
		t.Fatal(err)
	}
	count := func(name string) int {
		var n int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pg_indexes WHERE indexname = $1`, name).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if count("idx_dynamic_content_type_slug_published") != 1 || count("idx_dynamic_content_type_slug") != 0 {
		t.Fatalf("indexes after boot: published-only %d, all-status %d; want 1 and 0",
			count("idx_dynamic_content_type_slug_published"), count("idx_dynamic_content_type_slug"))
	}
	now := time.Now().UTC()
	for _, id := range []string{"d-1", "d-2"} {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO smeldr_dynamic_content (id, slug, type_name, created_at, updated_at) VALUES ($1, 'shared', 'memo', $2, $2)`,
			id, now); err != nil {
			t.Errorf("draft %s sharing a slug: %v", id, err)
		}
	}
}
