package smeldr

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// ---------------------------------------------------------------------------
// RedirectStore — unit tests
// ---------------------------------------------------------------------------

func TestRedirectStore_exactMatch(t *testing.T) {
	s := NewRedirectStore()
	s.Add(RedirectEntry{From: "/old", To: "/new", Code: Permanent})

	e, ok := s.Get("/old")
	if !ok {
		t.Fatal("expected match, got none")
	}
	if e.To != "/new" || e.Code != Permanent {
		t.Errorf("unexpected entry: %+v", e)
	}
}

func TestRedirectStore_miss(t *testing.T) {
	s := NewRedirectStore()
	_, ok := s.Get("/no-such-path")
	if ok {
		t.Error("expected no match, got one")
	}
}

func TestRedirectStore_chainCollapse_301(t *testing.T) {
	s := NewRedirectStore()
	s.Add(RedirectEntry{From: "/b", To: "/c", Code: Permanent})
	s.Add(RedirectEntry{From: "/a", To: "/b", Code: Permanent})

	e, ok := s.Get("/a")
	if !ok {
		t.Fatal("expected match, got none")
	}
	if e.To != "/c" {
		t.Errorf("expected chain collapsed to /c, got %q", e.To)
	}
}

func TestRedirectStore_chainCollapse_goneIsTerminal(t *testing.T) {
	s := NewRedirectStore()
	// /b is Gone — should not be collapsed through.
	s.Add(RedirectEntry{From: "/b", To: "", Code: Gone})
	s.Add(RedirectEntry{From: "/a", To: "/b", Code: Permanent})

	e, ok := s.Get("/a")
	if !ok {
		t.Fatal("expected match, got none")
	}
	// /a should still point to /b (not collapsed to Gone's empty dest).
	if e.To != "/b" {
		t.Errorf("expected /a → /b (not collapsed), got %q", e.To)
	}
}

func TestRedirectStore_prefixMatch(t *testing.T) {
	s := NewRedirectStore()
	s.Add(RedirectEntry{From: "/posts", To: "/articles", Code: Permanent, IsPrefix: true})

	e, ok := s.Get("/posts/hello-world")
	if !ok {
		t.Fatal("expected prefix match, got none")
	}
	if !e.IsPrefix {
		t.Error("expected IsPrefix=true")
	}
}

func TestRedirectStore_exactBeatsPrefix(t *testing.T) {
	s := NewRedirectStore()
	s.Add(RedirectEntry{From: "/posts", To: "/articles", Code: Permanent, IsPrefix: true})
	s.Add(RedirectEntry{From: "/posts", To: "/blog", Code: Permanent})

	e, ok := s.Get("/posts")
	if !ok {
		t.Fatal("expected match, got none")
	}
	if e.To != "/blog" {
		t.Errorf("expected exact match /blog, got %q", e.To)
	}
}

func TestRedirectStore_prefixRewrite(t *testing.T) {
	s := NewRedirectStore()
	s.Add(RedirectEntry{From: "/posts", To: "/articles", Code: Permanent, IsPrefix: true})

	req := httptest.NewRequest(http.MethodGet, "/posts/hello-world", nil)
	w := httptest.NewRecorder()
	s.handler().ServeHTTP(w, req)

	if w.Code != http.StatusMovedPermanently {
		t.Errorf("expected 301, got %d", w.Code)
	}
	loc := w.Header().Get("Location")
	if loc != "/articles/hello-world" {
		t.Errorf("expected Location /articles/hello-world, got %q", loc)
	}
}

func TestRedirectStore_handler_301(t *testing.T) {
	s := NewRedirectStore()
	s.Add(RedirectEntry{From: "/old", To: "/new", Code: Permanent})

	req := httptest.NewRequest(http.MethodGet, "/old", nil)
	w := httptest.NewRecorder()
	s.handler().ServeHTTP(w, req)

	if w.Code != http.StatusMovedPermanently {
		t.Errorf("expected 301, got %d", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/new" {
		t.Errorf("expected Location /new, got %q", loc)
	}
}

func TestRedirectStore_handler_410(t *testing.T) {
	s := NewRedirectStore()
	s.Add(RedirectEntry{From: "/removed", To: "", Code: Gone})

	req := httptest.NewRequest(http.MethodGet, "/removed", nil)
	req.Header.Set("X-Request-ID", "test-rid")
	w := httptest.NewRecorder()
	s.handler().ServeHTTP(w, req)

	if w.Code != http.StatusGone {
		t.Errorf("expected 410, got %d", w.Code)
	}
	if got := w.Header().Get("X-Request-ID"); got != "test-rid" {
		t.Errorf("expected X-Request-ID 'test-rid', got %q", got)
	}
}

func TestRedirectStore_handler_404(t *testing.T) {
	s := NewRedirectStore()

	req := httptest.NewRequest(http.MethodGet, "/unknown", nil)
	req.Header.Set("X-Request-ID", "test-rid")
	w := httptest.NewRecorder()
	s.handler().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
	if got := w.Header().Get("X-Request-ID"); got != "test-rid" {
		t.Errorf("expected X-Request-ID 'test-rid', got %q", got)
	}
}

// ---------------------------------------------------------------------------
// App.Redirect — integration tests
// ---------------------------------------------------------------------------

func newTestApp() *App {
	return New(Config{BaseURL: "http://localhost", Secret: []byte("test-secret-key!!")})
}

func TestApp_Redirect_permanent(t *testing.T) {
	app := newTestApp()
	app.Redirect("/old-path", "/new-path", Permanent)

	req := httptest.NewRequest(http.MethodGet, "/old-path", nil)
	w := httptest.NewRecorder()
	app.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusMovedPermanently {
		t.Errorf("expected 301, got %d", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/new-path" {
		t.Errorf("expected Location /new-path, got %q", loc)
	}
}

func TestApp_Redirect_gone(t *testing.T) {
	app := newTestApp()
	app.Redirect("/removed", "", Gone)

	req := httptest.NewRequest(http.MethodGet, "/removed", nil)
	w := httptest.NewRecorder()
	app.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusGone {
		t.Errorf("expected 410, got %d", w.Code)
	}
}

func TestApp_Redirect_chain_collapsed(t *testing.T) {
	app := newTestApp()
	app.Redirect("/b", "/c", Permanent)
	app.Redirect("/a", "/b", Permanent)

	req := httptest.NewRequest(http.MethodGet, "/a", nil)
	w := httptest.NewRecorder()
	app.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusMovedPermanently {
		t.Errorf("expected 301, got %d", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/c" {
		t.Errorf("expected chain collapsed to /c, got %q", loc)
	}
}

func TestRedirectStore_Len(t *testing.T) {
	s := NewRedirectStore()
	if n := s.Len(); n != 0 {
		t.Errorf("Len = %d; want 0 on empty store", n)
	}
	s.Add(RedirectEntry{From: "/a", To: "/b", Code: Permanent})
	s.Add(RedirectEntry{From: "/c", To: "/d", Code: Permanent})
	s.Add(RedirectEntry{From: "/old/", To: "/new/", Code: Permanent, IsPrefix: true})
	if n := s.Len(); n != 3 {
		t.Errorf("Len = %d; want 3", n)
	}
}

// ---------------------------------------------------------------------------
// RedirectStore.Delete — unit tests
// ---------------------------------------------------------------------------

func TestRedirectStore_Delete_exact(t *testing.T) {
	s := NewRedirectStore()
	s.Add(RedirectEntry{From: "/a", To: "/b", Code: Permanent})
	s.Delete("/a")

	if _, ok := s.Get("/a"); ok {
		t.Error("expected entry to be removed, still present")
	}
	if n := s.Len(); n != 0 {
		t.Errorf("Len = %d after delete; want 0", n)
	}
}

func TestRedirectStore_Delete_prefix(t *testing.T) {
	s := NewRedirectStore()
	s.Add(RedirectEntry{From: "/posts", To: "/articles", Code: Permanent, IsPrefix: true})
	s.Delete("/posts")

	if _, ok := s.Get("/posts/hello"); ok {
		t.Error("expected prefix entry to be removed, still matches")
	}
	if n := s.Len(); n != 0 {
		t.Errorf("Len = %d after delete; want 0", n)
	}
}

func TestRedirectStore_Delete_noop(t *testing.T) {
	s := NewRedirectStore()
	s.Add(RedirectEntry{From: "/a", To: "/b", Code: Permanent})
	s.Delete("/nonexistent") // must not panic or affect other entries
	if n := s.Len(); n != 1 {
		t.Errorf("Len = %d; want 1 (delete of missing entry must be no-op)", n)
	}
}

func TestRedirectStore_Delete_concurrent(t *testing.T) {
	s := NewRedirectStore()
	for i := 0; i < 10; i++ {
		s.Add(RedirectEntry{From: "/a", To: "/b", Code: Permanent})
	}
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); s.Add(RedirectEntry{From: "/x", To: "/y", Code: Permanent}) }()
		go func() { defer wg.Done(); s.Delete("/x") }()
	}
	wg.Wait() // no race: verified with -race
}

// ---------------------------------------------------------------------------
// CreateRoutesTable + App.Redirects + App.RedirectDB — DB integration tests
// ---------------------------------------------------------------------------

func TestCreateRoutesTable_idempotent(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateRoutesTable(db); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if err := CreateRoutesTable(db); err != nil {
		t.Fatalf("second call (idempotency): %v", err)
	}
}

func TestApp_Redirects_wires_db(t *testing.T) {
	db := newSQLiteDB(t)
	app := newTestApp()
	if err := app.Redirects(db); err != nil {
		t.Fatalf("Redirects: %v", err)
	}
	if app.RedirectDB() == nil {
		t.Error("RedirectDB() returned nil after Redirects(db)")
	}
}

func TestApp_Redirects_loads_existing(t *testing.T) {
	db := newSQLiteDB(t)
	// Seed the DB directly before wiring.
	if err := CreateRoutesTable(db); err != nil {
		t.Fatalf("CreateRoutesTable: %v", err)
	}
	store := NewRedirectStore()
	entry := RedirectEntry{From: "/old", To: "/new", Code: Permanent}
	if err := store.Save(context.Background(), db, entry); err != nil {
		t.Fatalf("Save: %v", err)
	}

	app := newTestApp()
	if err := app.Redirects(db); err != nil {
		t.Fatalf("Redirects: %v", err)
	}
	e, ok := app.RedirectStore().Get("/old")
	if !ok {
		t.Fatal("expected /old to be loaded, not found")
	}
	if e.To != "/new" {
		t.Errorf("To = %q; want /new", e.To)
	}
}

func TestRedirectStore_Save_Delete_roundtrip(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateRoutesTable(db); err != nil {
		t.Fatalf("CreateRoutesTable: %v", err)
	}
	s := NewRedirectStore()
	ctx := context.Background()

	entry := RedirectEntry{From: "/old", To: "/new", Code: Permanent}
	if err := s.Save(ctx, db, entry); err != nil {
		t.Fatalf("Save: %v", err)
	}
	s.Add(entry)

	// Verify in-memory.
	if _, ok := s.Get("/old"); !ok {
		t.Fatal("expected /old in store after Add")
	}

	// Delete from DB and in-memory.
	if err := s.Remove(ctx, db, "/old"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	s.Delete("/old")

	// In-memory should be gone.
	if _, ok := s.Get("/old"); ok {
		t.Error("/old still present in store after Delete")
	}

	// DB should be gone: fresh Load should not bring it back.
	s2 := NewRedirectStore()
	if err := s2.Load(ctx, db); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := s2.Get("/old"); ok {
		t.Error("/old still in DB after Remove")
	}
}

func TestRedirectStore_Save_IsPrefix_bool_roundtrip(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateRoutesTable(db); err != nil {
		t.Fatalf("CreateRoutesTable: %v", err)
	}
	s := NewRedirectStore()
	ctx := context.Background()

	entry := RedirectEntry{From: "/posts", To: "/articles", Code: Permanent, IsPrefix: true}
	if err := s.Save(ctx, db, entry); err != nil {
		t.Fatalf("Save: %v", err)
	}

	s2 := NewRedirectStore()
	if err := s2.Load(ctx, db); err != nil {
		t.Fatalf("Load: %v", err)
	}
	e, ok := s2.Get("/posts/hello")
	if !ok {
		t.Fatal("expected prefix match after Load, got none")
	}
	if !e.IsPrefix {
		t.Error("IsPrefix should be true after DB round-trip")
	}
}
