package smeldr

import (
	"context"
	"html/template"
	"net/http"
	"net/http/httptest"
	"testing"
)

// ——— PageMetaStore unit tests —————————————————————————————————————————————

func TestCreatePageMetaTable_Idempotent(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreatePageMetaTable(db); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if err := CreatePageMetaTable(db); err != nil {
		t.Fatalf("second call (idempotent): %v", err)
	}
}

func TestPageMetaStore_Set_Get(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreatePageMetaTable(db); err != nil {
		t.Fatalf("create table: %v", err)
	}
	s := NewPageMetaStore(db)
	ctx := context.Background()

	if err := s.Set(ctx, "/posts", "Posts", "All posts", "https://example.com/og.png"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := s.Get(ctx, "/posts")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Path != "/posts" {
		t.Errorf("Path = %q, want /posts", got.Path)
	}
	if got.MetaTitle != "Posts" {
		t.Errorf("MetaTitle = %q, want Posts", got.MetaTitle)
	}
	if got.Description != "All posts" {
		t.Errorf("Description = %q, want 'All posts'", got.Description)
	}
	if got.OGImage != "https://example.com/og.png" {
		t.Errorf("OGImage = %q, want https://example.com/og.png", got.OGImage)
	}
}

func TestPageMetaStore_Get_Missing(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreatePageMetaTable(db); err != nil {
		t.Fatalf("create table: %v", err)
	}
	s := NewPageMetaStore(db)
	ctx := context.Background()

	got, err := s.Get(ctx, "/unknown")
	if err != nil {
		t.Fatalf("Get for missing path returned error: %v", err)
	}
	if got.Path != "" {
		t.Errorf("missing path: got non-zero PageMeta %+v", got)
	}
}

func TestPageMetaStore_Set_Upsert(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreatePageMetaTable(db); err != nil {
		t.Fatalf("create table: %v", err)
	}
	s := NewPageMetaStore(db)
	ctx := context.Background()

	if err := s.Set(ctx, "/posts", "Old Title", "", ""); err != nil {
		t.Fatalf("first Set: %v", err)
	}
	if err := s.Set(ctx, "/posts", "New Title", "New desc", ""); err != nil {
		t.Fatalf("second Set: %v", err)
	}
	got, err := s.Get(ctx, "/posts")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.MetaTitle != "New Title" {
		t.Errorf("MetaTitle after upsert = %q, want 'New Title'", got.MetaTitle)
	}
	if got.Description != "New desc" {
		t.Errorf("Description after upsert = %q, want 'New desc'", got.Description)
	}
}

func TestPageMetaStore_Delete(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreatePageMetaTable(db); err != nil {
		t.Fatalf("create table: %v", err)
	}
	s := NewPageMetaStore(db)
	ctx := context.Background()

	if err := s.Set(ctx, "/posts", "Posts", "", ""); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := s.Delete(ctx, "/posts"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	got, err := s.Get(ctx, "/posts")
	if err != nil {
		t.Fatalf("Get after delete: %v", err)
	}
	if got.Path != "" {
		t.Errorf("expected zero PageMeta after delete, got %+v", got)
	}
}

func TestPageMetaStore_Delete_Missing(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreatePageMetaTable(db); err != nil {
		t.Fatalf("create table: %v", err)
	}
	s := NewPageMetaStore(db)
	ctx := context.Background()

	// delete of a non-existent path should be a no-op, not an error
	if err := s.Delete(ctx, "/nope"); err != nil {
		t.Fatalf("Delete of missing path: %v", err)
	}
}

func TestPageMetaStore_List(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreatePageMetaTable(db); err != nil {
		t.Fatalf("create table: %v", err)
	}
	s := NewPageMetaStore(db)
	ctx := context.Background()

	paths := []string{"/about", "/docs", "/posts"}
	for _, p := range paths {
		if err := s.Set(ctx, p, "Title "+p, "", ""); err != nil {
			t.Fatalf("Set %s: %v", p, err)
		}
	}

	all, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("List returned %d entries, want 3", len(all))
	}
	// List returns entries ordered by path — check ascending order
	for i, p := range paths {
		if all[i].Path != p {
			t.Errorf("List[%d].Path = %q, want %q", i, all[i].Path, p)
		}
	}
}

// ——— App.GetPageMeta tests ————————————————————————————————————————————————

func TestApp_GetPageMeta_NoStore(t *testing.T) {
	app := New(Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16")})
	head := app.GetPageMeta(context.Background(), "/posts")
	if head.Title != "" || head.Description != "" || head.Image.URL != "" {
		t.Errorf("expected zero Head without store, got %+v", head)
	}
}

func TestApp_GetPageMeta_Found(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreatePageMetaTable(db); err != nil {
		t.Fatalf("create table: %v", err)
	}
	s := NewPageMetaStore(db)
	ctx := context.Background()
	if err := s.Set(ctx, "/posts", "My Posts", "Latest articles", "https://example.com/og.jpg"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	app := New(Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16")})
	app.PageMeta(s)

	head := app.GetPageMeta(ctx, "/posts")
	if head.Title != "My Posts" {
		t.Errorf("Title = %q, want 'My Posts'", head.Title)
	}
	if head.Description != "Latest articles" {
		t.Errorf("Description = %q, want 'Latest articles'", head.Description)
	}
	if head.Image.URL != "https://example.com/og.jpg" {
		t.Errorf("Image.URL = %q, want https://example.com/og.jpg", head.Image.URL)
	}
}

func TestApp_GetPageMeta_Missing(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreatePageMetaTable(db); err != nil {
		t.Fatalf("create table: %v", err)
	}

	app := New(Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16")})
	app.PageMeta(NewPageMetaStore(db))

	head := app.GetPageMeta(context.Background(), "/unknown")
	if head.Title != "" {
		t.Errorf("expected zero Head for unknown path, got %+v", head)
	}
}

// ——— renderListHTML + PageMeta integration tests ——————————————————————————

func TestRenderListHTML_PageMeta_UsedWhenNoListHeadFunc(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreatePageMetaTable(db); err != nil {
		t.Fatalf("create table: %v", err)
	}
	s := NewPageMetaStore(db)
	ctx := context.Background()
	if err := s.Set(ctx, "/posts", "Posts Title", "Posts desc", ""); err != nil {
		t.Fatalf("Set: %v", err)
	}

	repo := NewMemoryRepo[*testPost]()
	m := newTestModule(repo)

	tpl, err := template.New("list").Parse(`{{.Head.Title}}`)
	if err != nil {
		t.Fatalf("template parse: %v", err)
	}
	m.tplList = tpl
	m.neg.html = true
	m.pageMetaStore = s // inject directly (as App.Handler does via push loop)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/posts", nil)
	r.Header.Set("Accept", "text/html")
	rctx := ContextFrom(w, r)
	m.renderListHTML(w, r, rctx, []*testPost{})

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if body != "Posts Title" {
		t.Errorf("body = %q, want 'Posts Title'", body)
	}
}

func TestRenderListHTML_PageMeta_ListHeadFuncPriority(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreatePageMetaTable(db); err != nil {
		t.Fatalf("create table: %v", err)
	}
	s := NewPageMetaStore(db)
	ctx := context.Background()
	if err := s.Set(ctx, "/posts", "Meta Title", "", ""); err != nil {
		t.Fatalf("Set: %v", err)
	}

	repo := NewMemoryRepo[*testPost]()
	m := newTestModule(repo)

	tpl, err := template.New("list").Parse(`{{.Head.Title}}`)
	if err != nil {
		t.Fatalf("template parse: %v", err)
	}
	m.tplList = tpl
	m.neg.html = true
	m.pageMetaStore = s
	// listHeadFunc takes priority over pageMetaStore
	m.listHeadFunc = func(_ Context, _ []*testPost) Head {
		return Head{Title: "ListHeadFunc Title"}
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/posts", nil)
	r.Header.Set("Accept", "text/html")
	rctx := ContextFrom(w, r)
	m.renderListHTML(w, r, rctx, []*testPost{})

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if body != "ListHeadFunc Title" {
		t.Errorf("body = %q, want 'ListHeadFunc Title'", body)
	}
}

// TestApp_Handler_InjectsPageMetaStore verifies that App.Handler injects the
// PageMetaStore into all registered modules via the templateModules push loop.
func TestApp_Handler_InjectsPageMetaStore(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreatePageMetaTable(db); err != nil {
		t.Fatalf("create table: %v", err)
	}
	store := NewPageMetaStore(db)

	repo := NewMemoryRepo[*testPost]()
	m := newTestModule(repo)

	app := New(Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16")})
	app.PageMeta(store)
	app.Content(m)
	app.Handler()

	if m.pageMetaStore == nil {
		t.Error("pageMetaStore should be injected into the module by App.Handler push loop")
	}
	if m.pageMetaStore != store {
		t.Error("pageMetaStore injected does not match the one passed to App.PageMeta")
	}
}

func TestRenderListHTML_PageMeta_NoEntryForPath(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreatePageMetaTable(db); err != nil {
		t.Fatalf("create table: %v", err)
	}
	// store is wired but no entry for the requested path
	s := NewPageMetaStore(db)

	repo := NewMemoryRepo[*testPost]()
	m := newTestModule(repo)

	tpl, err := template.New("list").Parse(`{{.Head.Title}}`)
	if err != nil {
		t.Fatalf("template parse: %v", err)
	}
	m.tplList = tpl
	m.neg.html = true
	m.pageMetaStore = s

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/posts", nil)
	rctx := ContextFrom(w, r)
	m.renderListHTML(w, r, rctx, []*testPost{})

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	// no entry → empty title
	if w.Body.String() != "" {
		t.Errorf("expected empty title for unmatched path, got %q", w.Body.String())
	}
}
