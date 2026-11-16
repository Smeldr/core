package smeldr

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

// T1295: a Standalone module shares a program with ordinary modules, and
// App.Content's panic for a typed Repo says what to write.

type mixPage struct {
	Node
	Title string `smeldr:"required"`
}

func (p *mixPage) Markdown() string { return "# " + p.Title }

// mixApp is a program with a Standalone module of pages (with AI docs) and,
// when other is set, one more module.
func mixApp(t *testing.T, other ...func(*App)) (*App, http.Handler, *MemoryRepo[*mixPage]) {
	t.Helper()
	pages := NewMemoryRepo[*mixPage]()
	app := New(MustConfig(Config{BaseURL: "http://localhost", Secret: []byte("t1295-standalone-mix-secret!!!!")}))
	app.Content(NewModule((*mixPage)(nil), At("/pages"), Repo(pages), Standalone(), AIIndex(AIDoc)))
	for _, o := range other {
		o(app)
	}
	return app, app.Handler(), pages
}

func publish[T any](t *testing.T, repo *MemoryRepo[T], item T) {
	t.Helper()
	if err := repo.Save(context.Background(), item); err != nil {
		t.Fatal(err)
	}
}

func serve(h http.Handler, method, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(method, path, nil))
	return w
}

func TestStandalone_BesideOtherModules(t *testing.T) {
	for _, mode := range []struct {
		name string
		opts []Option
	}{{"default", nil}, {"APIOnly", []Option{APIOnly()}}} {
		t.Run(mode.name, func(t *testing.T) {
			posts := NewMemoryRepo[*testAIPost]()
			_, h, pages := mixApp(t, func(a *App) {
				a.Content(NewModule((*testAIPost)(nil), append([]Option{At("/posts"), Repo(posts), AIIndex(AIDoc)}, mode.opts...)...))
			})
			publish(t, pages, &mixPage{Node: Node{ID: NewID(), Slug: "about", Status: Published}, Title: "About"})
			publish(t, pages, &mixPage{Node: Node{ID: NewID(), Slug: "posts", Status: Published}, Title: "A page named posts"})
			publish(t, posts, &testAIPost{Node: Node{ID: NewID(), Slug: "hello", Status: Published}, Title: "Hello"})
			publish(t, posts, &testAIPost{Node: Node{ID: NewID(), Slug: "aidoc", Status: Published}, Title: "A post named aidoc"})
			for path, want := range map[string]string{
				"/about":             "About",
				"/about/aidoc":       "# About",
				"/posts/hello":       "Hello",
				"/posts/hello/aidoc": "# Hello",
				// A module prefix wins: the posts item with slug aidoc, not the
				// AI doc of the page named posts.
				"/posts/aidoc": "A post named aidoc",
			} {
				if w := serve(h, http.MethodGet, path); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), want) {
					t.Errorf("%s: %d %q, want %q", path, w.Code, w.Body.String(), want)
				}
			}
		})
	}
}

// A prefix of two segments beside a Standalone module.
func TestStandalone_BesideTwoSegmentPrefix(t *testing.T) {
	guides := NewMemoryRepo[*testAIPost]()
	_, h, pages := mixApp(t, func(a *App) {
		a.Content(NewModule((*testAIPost)(nil), At("/docs/guides"), Repo(guides)))
	})
	publish(t, pages, &mixPage{Node: Node{ID: NewID(), Slug: "docs", Status: Published}, Title: "Docs page"})
	publish(t, guides, &testAIPost{Node: Node{ID: NewID(), Slug: "aidoc", Status: Published}, Title: "Guide named aidoc"})
	// /docs/aidoc: the AI doc of the Standalone page docs (no module serves /docs/{slug}).
	if w := serve(h, http.MethodGet, "/docs/aidoc"); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "# Docs page") {
		t.Errorf("/docs/aidoc: %d %q", w.Code, w.Body.String())
	}
	// /docs/guides/aidoc: the guides item with slug aidoc.
	if w := serve(h, http.MethodGet, "/docs/guides/aidoc"); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Guide named aidoc") {
		t.Errorf("/docs/guides/aidoc: %d %q", w.Code, w.Body.String())
	}
}

// In a Standalone-only program, the answers are what the old pattern gave:
// an unknown /x/aidoc 404 with the same headers and body, HEAD the same, and
// a Standalone item's AI doc wins over a redirect rule for the same path.
func TestStandalone_AnswersUnchanged(t *testing.T) {
	app, h, pages := mixApp(t)
	publish(t, pages, &mixPage{Node: Node{ID: NewID(), Slug: "about", Status: Published}, Title: "About"})
	unknown := serve(h, http.MethodGet, "/nothing/aidoc")
	if unknown.Code != http.StatusNotFound || !strings.Contains(unknown.Body.String(), "route.not_found") {
		t.Errorf("unknown: %d %s", unknown.Code, unknown.Body.String())
	}
	// What the old pattern's handler wrote for the same request.
	old := httptest.NewRecorder()
	WriteError(old, httptest.NewRequest(http.MethodGet, "/nothing/aidoc", nil), problem(problemRouteNotFound, ErrNotFound, nil))
	// The request ID is the middleware's, one per request, and was there
	// before too (the old pattern's handler ran behind it).
	noID := regexp.MustCompile(`"request_id":"[^"]*"`)
	if noID.ReplaceAllString(unknown.Body.String(), "") != noID.ReplaceAllString(old.Body.String(), "") || unknown.Header().Get("Content-Type") != old.Header().Get("Content-Type") {
		t.Errorf("unknown differs from before:\n got %s %q\nwant %s %q", unknown.Header().Get("Content-Type"), unknown.Body.String(), old.Header().Get("Content-Type"), old.Body.String())
	}
	get, head := serve(h, http.MethodGet, "/about/aidoc"), serve(h, http.MethodHead, "/about/aidoc")
	if head.Code != get.Code || head.Header().Get("Content-Type") != get.Header().Get("Content-Type") {
		t.Errorf("HEAD %d %q, GET %d %q", head.Code, head.Header().Get("Content-Type"), get.Code, get.Header().Get("Content-Type"))
	}
	app.redirectStore.Add(RedirectEntry{From: "/about/aidoc", To: "/elsewhere", Code: Permanent})
	app.redirectStore.Add(RedirectEntry{From: "/gone/aidoc", To: "/elsewhere", Code: Permanent})
	if w := serve(h, http.MethodGet, "/about/aidoc"); w.Code != http.StatusOK {
		t.Errorf("the Standalone AI doc did not win over a redirect: %d", w.Code)
	}
	if w := serve(h, http.MethodGet, "/gone/aidoc"); w.Code != http.StatusMovedPermanently {
		t.Errorf("a redirect at /x/aidoc with no Standalone item: %d", w.Code)
	}
	if w := serve(h, http.MethodPost, "/about/aidoc"); w.Code == http.StatusOK {
		t.Errorf("POST served the AI doc: %d", w.Code)
	}
}

// SingleInstance beside Standalone started before; its routes answer as before.
func TestStandalone_BesideSingleInstance(t *testing.T) {
	cfg := NewMemoryRepo[*testAIPost]()
	_, h, _ := mixApp(t, func(a *App) {
		a.Content(NewModule((*testAIPost)(nil), At("/site-config"), Repo(cfg), SingleInstance(), AIIndex(AIDoc)))
	})
	publish(t, cfg, &testAIPost{Node: Node{ID: NewID(), Slug: "site-config", Status: Published}, Title: "Site"})
	if w := serve(h, http.MethodGet, "/site-config"); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Site") {
		t.Errorf("/site-config: %d %q", w.Code, w.Body.String())
	}
}

func TestAppContent_TypedRepoPanicSaysWhatToWrite(t *testing.T) {
	app := New(MustConfig(Config{BaseURL: "http://localhost", Secret: []byte("t1295-standalone-mix-secret!!!!")}))
	defer func() {
		msg, _ := recover().(string)
		if msg != "smeldr: App.Content(&smeldr.mixPage{}, smeldr.Repo(...)) cannot use a typed Repo: write app.Content(smeldr.NewModule(&smeldr.mixPage{}, smeldr.Repo(...), smeldr.At(...)))" {
			t.Errorf("panic %q", msg)
		}
	}()
	app.Content(&mixPage{}, At("/pages"), Repo(NewMemoryRepo[*mixPage]()))
}

func TestNewModule_NoRepoPanicSaysWhatToWrite(t *testing.T) {
	defer func() {
		msg, _ := recover().(string)
		if msg != "smeldr: Module[T] requires a Repository: pass smeldr.Repo(...) to smeldr.NewModule" {
			t.Errorf("panic %q", msg)
		}
	}()
	NewModule((*mixPage)(nil), At("/pages"))
}
