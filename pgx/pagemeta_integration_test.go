//go:build integration

package pgx

import (
	"context"
	"testing"

	smeldr "smeldr.dev/core"
)

// PageMetaStore on Postgres. Before core v1.119.3 Set, Get and Delete failed: they
// used ? placeholders (pgx wants $1) and INSERT OR REPLACE (SQLite only, SQLSTATE
// 42601), so the SEO overrides and the set_page_meta, get_page_meta and
// delete_page_meta tools did not work on Postgres.
func TestPG_PageMeta(t *testing.T) {
	db, _ := isolatedDB(t)
	ctx := context.Background()
	if err := smeldr.CreatePageMetaTable(db); err != nil {
		t.Fatalf("CreatePageMetaTable: %v", err)
	}
	store := smeldr.NewPageMetaStore(db)

	if got, err := store.Get(ctx, "/about"); err != nil || got.Path != "" {
		t.Fatalf("Get of a path with no row = %+v, %v, want a zero PageMeta and no error", got, err)
	}
	if err := store.Set(ctx, "/about", "Om os", "Hvem vi er ☕", "https://example.com/og.png"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := store.Get(ctx, "/about")
	want := smeldr.PageMeta{Path: "/about", MetaTitle: "Om os", Description: "Hvem vi er ☕", OGImage: "https://example.com/og.png"}
	if err != nil || got != want {
		t.Fatalf("Get = %+v, %v, want %+v", got, err, want)
	}

	// A second Set for the same path replaces the row, every column of it.
	if err := store.Set(ctx, "/about", "Nyt", "", ""); err != nil {
		t.Fatalf("Set (replace): %v", err)
	}
	got, err = store.Get(ctx, "/about")
	if err != nil || got != (smeldr.PageMeta{Path: "/about", MetaTitle: "Nyt"}) {
		t.Fatalf("Get after the replace = %+v, %v, want only the new title", got, err)
	}

	if err := store.Set(ctx, "/blog", "A", "", ""); err != nil {
		t.Fatalf("Set second path: %v", err)
	}
	all, err := store.List(ctx)
	if err != nil || len(all) != 2 || all[0].Path != "/about" || all[1].Path != "/blog" {
		t.Fatalf("List = %+v, %v, want two rows ordered by path (paths chosen so the order does not depend on the database collation)", all, err)
	}

	if err := store.Delete(ctx, "/about"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if got, err := store.Get(ctx, "/about"); err != nil || got.Path != "" {
		t.Errorf("Get after Delete = %+v, %v, want a zero PageMeta", got, err)
	}
	if err := store.Delete(ctx, "/never-there"); err != nil {
		t.Errorf("Delete of a missing path must be a no-op: %v", err)
	}

	// The wiring a page render uses.
	app := smeldr.New(smeldr.Config{BaseURL: "http://localhost", Secret: []byte(pgTestSecret), DB: db})
	app.PageMeta(store)
	if h := app.GetPageMeta(ctx, "/blog"); h.Title != "A" {
		t.Errorf("App.GetPageMeta title = %q, want A", h.Title)
	}
	if h := app.GetPageMeta(ctx, "/about"); h.Title != "" {
		t.Errorf("App.GetPageMeta after Delete = %+v, want a zero Head", h)
	}
}
