package forge

import (
	"net/http"
	"net/http/httptest"
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
