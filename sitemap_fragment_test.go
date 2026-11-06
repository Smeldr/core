package smeldr

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// sitemapFragmentApp is an app with no sitemap module, for App.SitemapFragment
// tests (T1168).
func sitemapFragmentApp() *App {
	return New(MustConfig(Config{BaseURL: "https://example.com/", Secret: []byte("16bytessecretkey")}))
}

// sitemapGet fetches path from h.
func sitemapGet(h http.Handler, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w
}

// wantPanic runs f and fails unless it panics with a message containing want.
func wantPanic(t *testing.T, want string, f func()) {
	t.Helper()
	defer func() {
		t.Helper()
		r := recover()
		if r == nil {
			t.Fatalf("no panic; want one containing %q", want)
		}
		if msg, _ := r.(string); !strings.Contains(msg, want) {
			t.Fatalf("panic = %v; want it to contain %q", r, want)
		}
	}()
	f()
}

func noEntries(context.Context) ([]SitemapEntry, error) { return nil, nil }

// TestSitemapFragment_ServesEntries: a relative Loc is joined to BaseURL, an
// absolute one is written as given, and the fragment is application/xml.
func TestSitemapFragment_ServesEntries(t *testing.T) {
	app := sitemapFragmentApp()
	app.SitemapFragment("/pages/sitemap.xml", func(context.Context) ([]SitemapEntry, error) {
		return []SitemapEntry{
			{Loc: "/", ChangeFreq: Weekly, Priority: 1, LastMod: time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)},
			{Loc: "https://other.example/x"},
		}, nil
	})
	w := sitemapGet(app.Handler(), "/pages/sitemap.xml")
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/xml") {
		t.Fatalf("status %d, type %q: %s", w.Code, w.Header().Get("Content-Type"), w.Body.String())
	}
	body := w.Body.String()
	for _, want := range []string{"<loc>https://example.com/</loc>", "<loc>https://other.example/x</loc>", "<lastmod>2026-11-06</lastmod>"} {
		if !strings.Contains(body, want) {
			t.Errorf("fragment lacks %s:\n%s", want, body)
		}
	}
}

// TestSitemapFragment_IndexWithoutModules: with no sitemap module, the index
// exists and lists the application fragment.
func TestSitemapFragment_IndexWithoutModules(t *testing.T) {
	app := sitemapFragmentApp()
	app.SitemapFragment("/pages/sitemap.xml", noEntries)
	w := sitemapGet(app.Handler(), "/sitemap.xml")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "<loc>https://example.com/pages/sitemap.xml</loc>") {
		t.Errorf("index %d:\n%s", w.Code, w.Body.String())
	}
}

// TestSitemapFragment_IndexWithModule: one index lists the module fragment
// and the application fragment, sorted; each serves its own entries.
func TestSitemapFragment_IndexWithModule(t *testing.T) {
	app := sitemapFragmentApp()
	app.Content(NewModule((*testPost)(nil), Repo(NewMemoryRepo[*testPost]()), At("/posts"), SitemapConfig{}))
	app.SitemapFragment("/docs/topics/sitemap.xml", func(context.Context) ([]SitemapEntry, error) {
		return []SitemapEntry{{Loc: "/docs/topics/storage"}}, nil
	})
	h := app.Handler()
	app.sitemapStore.Set("/posts/sitemap.xml", []byte("<urlset/>")) // the module's fragment, also written by its startup rebuild
	idx := sitemapGet(h, "/sitemap.xml").Body.String()
	a := strings.Index(idx, "/docs/topics/sitemap.xml")
	b := strings.Index(idx, "/posts/sitemap.xml")
	if a < 0 || b < 0 || a > b || strings.Count(idx, "<sitemapindex") != 1 {
		t.Errorf("index should list both, sorted, in one document:\n%s", idx)
	}
	if got := sitemapGet(h, "/docs/topics/sitemap.xml").Body.String(); !strings.Contains(got, "https://example.com/docs/topics/storage") {
		t.Errorf("application fragment:\n%s", got)
	}
	if w := sitemapGet(h, "/posts/sitemap.xml"); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "urlset") {
		t.Errorf("module fragment = %d %q", w.Code, w.Body.String())
	}
}

// TestSitemapFragment_FollowsData: entries are computed per request, so a
// change in the data shows at the next request.
func TestSitemapFragment_FollowsData(t *testing.T) {
	app := sitemapFragmentApp()
	topics := []string{"storage"}
	app.SitemapFragment("/docs/topics/sitemap.xml", func(context.Context) ([]SitemapEntry, error) {
		out := make([]SitemapEntry, 0, len(topics))
		for _, s := range topics {
			out = append(out, SitemapEntry{Loc: "/docs/topics/" + s})
		}
		return out, nil
	})
	h := app.Handler()
	if body := sitemapGet(h, "/docs/topics/sitemap.xml").Body.String(); strings.Contains(body, "/docs/topics/auth") {
		t.Fatalf("auth listed before it exists:\n%s", body)
	}
	topics = append(topics, "auth")
	if body := sitemapGet(h, "/docs/topics/sitemap.xml").Body.String(); !strings.Contains(body, "/docs/topics/auth") {
		t.Errorf("auth not listed after it appeared:\n%s", body)
	}
}

// TestSitemapFragment_Empty: no entries is a valid empty urlset.
func TestSitemapFragment_Empty(t *testing.T) {
	app := sitemapFragmentApp()
	app.SitemapFragment("/pages/sitemap.xml", noEntries)
	w := sitemapGet(app.Handler(), "/pages/sitemap.xml")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "urlset") || strings.Contains(w.Body.String(), "<url>") {
		t.Errorf("empty fragment %d:\n%s", w.Code, w.Body.String())
	}
}

// TestSitemapFragment_EntriesError: an error from entries is a 500 that does
// not show the cause; the index still lists the fragment.
func TestSitemapFragment_EntriesError(t *testing.T) {
	app := sitemapFragmentApp()
	app.SitemapFragment("/pages/sitemap.xml", func(context.Context) ([]SitemapEntry, error) {
		return nil, errors.New("db down: secret detail")
	})
	h := app.Handler()
	w := sitemapGet(h, "/pages/sitemap.xml")
	if w.Code != http.StatusInternalServerError || strings.Contains(w.Body.String(), "secret detail") {
		t.Errorf("entries error = %d %s; want a 500 without the cause", w.Code, w.Body.String())
	}
	if !strings.Contains(sitemapGet(h, "/sitemap.xml").Body.String(), "/pages/sitemap.xml") {
		t.Error("the index dropped the failing fragment")
	}
}

// failingResponseWriter fails every body write, for the fragment's write
// error path.
type failingResponseWriter struct{ h http.Header }

func (f *failingResponseWriter) Header() http.Header {
	if f.h == nil {
		f.h = http.Header{}
	}
	return f.h
}
func (f *failingResponseWriter) Write([]byte) (int, error) { return 0, errors.New("client gone") }
func (f *failingResponseWriter) WriteHeader(int)           {}

// TestSitemapFragment_WriteError: a write that fails is logged and does not
// panic.
func TestSitemapFragment_WriteError(t *testing.T) {
	store := NewSitemapStore()
	store.setSource("/p.xml", sitemapSource{entries: noEntries, baseURL: "https://example.com"})
	store.Handler().ServeHTTP(&failingResponseWriter{}, httptest.NewRequest(http.MethodGet, "/p.xml", nil))
}

// TestSitemapFragment_BadPath: setup misuse panics with the reason.
func TestSitemapFragment_BadPath(t *testing.T) {
	for _, tc := range []struct {
		name, path, want string
		entries          func(context.Context) ([]SitemapEntry, error)
	}{
		{"empty", "", "must start with /", noEntries},
		{"relative", "pages/sitemap.xml", "must start with /", noEntries},
		{"not xml", "/pages/sitemap", "must end in .xml", noEntries},
		{"the index", "/sitemap.xml", "is the index", noEntries},
		{"nil entries", "/pages/sitemap.xml", "is nil", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := sitemapFragmentApp()
			wantPanic(t, tc.want, func() { app.SitemapFragment(tc.path, tc.entries) })
		})
	}
	t.Run("added twice", func(t *testing.T) {
		app := sitemapFragmentApp()
		app.SitemapFragment("/pages/sitemap.xml", noEntries)
		wantPanic(t, "already an application fragment", func() { app.SitemapFragment("/pages/sitemap.xml", noEntries) })
	})
}

// TestSitemapFragment_ClashesWithModule: a path a module's fragment uses is
// refused at App.Handler when the module came first.
func TestSitemapFragment_ClashesWithModule(t *testing.T) {
	app := sitemapFragmentApp()
	app.Content(NewModule((*testPost)(nil), Repo(NewMemoryRepo[*testPost]()), At("/posts"), SitemapConfig{}))
	app.SitemapFragment("/posts/sitemap.xml", noEntries)
	wantPanic(t, "also a module's sitemap fragment", func() { app.Handler() })
}

// TestSitemapFragment_ClashesWithModuleRegisteredLater: the same refusal, the
// same way, when the module is registered after the application fragment.
func TestSitemapFragment_ClashesWithModuleRegisteredLater(t *testing.T) {
	app := sitemapFragmentApp()
	app.SitemapFragment("/posts/sitemap.xml", noEntries)
	app.Content(NewModule((*testPost)(nil), Repo(NewMemoryRepo[*testPost]()), At("/posts"), SitemapConfig{}))
	wantPanic(t, "also a module's sitemap fragment", func() { app.Handler() })
}

// TestSitemapFragment_DynamicTypeCannotTakePath: a runtime-defined type whose
// fragment path is an application fragment cannot replace it: the store keeps
// the application's entries (the write is dropped with a warning).
func TestSitemapFragment_DynamicTypeCannotTakePath(t *testing.T) {
	app := sitemapFragmentApp()
	app.SitemapFragment("/notes/sitemap.xml", func(context.Context) ([]SitemapEntry, error) {
		return []SitemapEntry{{Loc: "/mine"}}, nil
	})
	h := app.Handler()
	app.sitemapStore.Set("/notes/sitemap.xml", []byte("<urlset>theirs</urlset>"))
	if _, stored := app.sitemapStore.Get("/notes/sitemap.xml"); stored {
		t.Error("a module write was stored over an application fragment")
	}
	if body := sitemapGet(h, "/notes/sitemap.xml").Body.String(); !strings.Contains(body, "https://example.com/mine") {
		t.Errorf("fragment = %s; want the application's entries", body)
	}
	if n := strings.Count(sitemapGet(h, "/sitemap.xml").Body.String(), "/notes/sitemap.xml"); n != 1 {
		t.Errorf("index lists the path %d times, want 1", n)
	}
}

// TestSitemapFragment_HandlerTwice: a second App.Handler mounts nothing twice.
func TestSitemapFragment_HandlerTwice(t *testing.T) {
	app := sitemapFragmentApp()
	app.SitemapFragment("/pages/sitemap.xml", noEntries)
	app.Handler()
	if w := sitemapGet(app.Handler(), "/pages/sitemap.xml"); w.Code != http.StatusOK {
		t.Errorf("after a second Handler: %d", w.Code)
	}
}
