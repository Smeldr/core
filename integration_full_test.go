package forge

// integration_full_test.go — cross-milestone integration suite (M1–M5).
//
// Each test exercises behaviour that requires at least two milestone components
// working together. No test in this file duplicates coverage from
// integration_test.go (which covers single-module M4 scenarios).
//
// Groups:
//   G1  — Multi-module routing (M2)
//   G2  — Role-based access via inline middleware (M1 + M2)
//   G3  — Signal fire-through across modules (M1 + M2)
//   G4  — Content negotiation: two modules, mixed template configuration (M2 + M4)
//   G5  — forge:head + schema helpers through real render (M3 + M4)
//   G6  — SEO wiring: robots.txt + sitemap registration (M2 + M3)
//   G7  — Error template fallback across two modules (M2 + M4)
//   G8  — TemplateData end-to-end (M3 + M4)
//   G9  — Social + SitemapConfig (M5 + M3)
//   G10 — AI indexing + content negotiation (M5 + M4)
//   G11 — RSS feed + AfterPublish signal (M5 + M1)
//   G12 — Full M5 stack (M5 + M3 + M4)
//   G13 — Cookie consent enforcement (M6, Decision 5)
//   G14 — Consent lifecycle wired through a handler (M6 + M2)
//   G15 — Cookie manifest + App integration (M6 + M2 + M1)
//   G16 — Redirect enforcement: 301/410/404 + chain collapse (M7, Decision 17)
//   G17 — Prefix redirect via Redirects(From) + exact-beats-prefix (M7 + M2)
//   G18 — Full M7 stack: SQLRepo interface check + redirect manifest + ManifestAuth (M7 + M6 + M1)
//   G19 — Scheduler end-to-end: processScheduled + AfterPublish signal (M8 + M1)
//   G20 — Scheduler wired through App.Content(): schedulerModules populated, tick publishes (M8 + M2 + M3)
//   G21 — Full v1.0.0 stack: scheduler + sitemap + feed + AI index + redirects (M1+M2+M3+M5+M7+M8)
//   G22 — forge-mcp MCPModule interface + lifecycle (M10)

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// — Helpers ———————————————————————————————————————————————————————————————

// fullTestApp is the shared state for two-module integration tests.
type fullTestApp struct {
	app      *App
	handler  http.Handler
	postRepo *MemoryRepo[*testPost]
	pageRepo *MemoryRepo[*testMDPost]
	postsMod *Module[*testPost]
	pagesMod *Module[*testMDPost]
}

// newFullTestApp creates an App with two modules:
//   - posts (*testPost at /posts) with postOpts appended after Repo + At("/posts")
//   - pages (*testMDPost at /pages) with pageOpts appended after Repo + At("/pages")
//
// Templates are parsed and Handler() is called before returning.
func newFullTestApp(t *testing.T, postOpts []Option, pageOpts []Option) *fullTestApp {
	t.Helper()
	postRepo := NewMemoryRepo[*testPost]()
	pageRepo := NewMemoryRepo[*testMDPost]()

	pOpts := append([]Option{Repo(postRepo), At("/posts")}, postOpts...)
	gOpts := append([]Option{Repo(pageRepo), At("/pages")}, pageOpts...)

	m1 := NewModule((*testPost)(nil), pOpts...)
	m2 := NewModule((*testMDPost)(nil), gOpts...)

	app := New(MustConfig(Config{
		BaseURL: "https://example.com",
		Secret:  []byte("16bytessecretkey"),
	}))
	app.Content(m1)
	app.Content(m2)

	if err := m1.parseTemplates(); err != nil {
		t.Fatalf("newFullTestApp: parseTemplates posts: %v", err)
	}
	if err := m2.parseTemplates(); err != nil {
		t.Fatalf("newFullTestApp: parseTemplates pages: %v", err)
	}

	return &fullTestApp{
		app:      app,
		handler:  app.Handler(),
		postRepo: postRepo,
		pageRepo: pageRepo,
		postsMod: m1,
		pagesMod: m2,
	}
}

// fullSeedPost saves a published *testPost with the given slug and title.
func fullSeedPost(t *testing.T, repo *MemoryRepo[*testPost], slug, title string) *testPost {
	t.Helper()
	p := &testPost{Node: Node{ID: NewID(), Slug: slug, Status: Published}, Title: title}
	if err := repo.Save(context.Background(), p); err != nil {
		t.Fatalf("fullSeedPost: %v", err)
	}
	return p
}

// fullSeedPage saves a published *testMDPost with the given slug and title.
func fullSeedPage(t *testing.T, repo *MemoryRepo[*testMDPost], slug, title string) *testMDPost {
	t.Helper()
	p := &testMDPost{
		Node:  Node{ID: NewID(), Slug: slug, Status: Published},
		Title: title,
		Body:  "Hello world",
	}
	if err := repo.Save(context.Background(), p); err != nil {
		t.Fatalf("fullSeedPage: %v", err)
	}
	return p
}

// writeErrTemplate creates errors/{status}.html inside dir with the given body.
func writeErrTemplate(t *testing.T, dir string, status int, body string) {
	t.Helper()
	errDir := filepath.Join(dir, "errors")
	if err := os.MkdirAll(errDir, 0755); err != nil {
		t.Fatalf("writeErrTemplate mkdir: %v", err)
	}
	name := filepath.Join(errDir, fmt.Sprintf("%d.html", status))
	if err := os.WriteFile(name, []byte(body), 0644); err != nil {
		t.Fatalf("writeErrTemplate write: %v", err)
	}
}

// — G1: Multi-module routing (M2) ————————————————————————————————————————

// TestFull_multiModuleRouting verifies that two modules registered at different
// prefixes route independently without cross-contamination.
func TestFull_multiModuleRouting(t *testing.T) {
	postDir := intTmpDir(t, `<p>posts-list</p>`, `<p>post:{{.Content.Title}}</p>`)
	fa := newFullTestApp(t,
		[]Option{Templates(postDir)},
		nil, // pages: JSON only
	)
	fullSeedPost(t, fa.postRepo, "hello-post", "Hello Post")
	fullSeedPage(t, fa.pageRepo, "about", "About Page")

	t.Run("posts_show_html", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/posts/hello-post", nil)
		r.Header.Set("Accept", "text/html")
		w := httptest.NewRecorder()
		fa.handler.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("status = %d; body: %s", w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "Hello Post") {
			t.Errorf("posts: expected 'Hello Post', got: %s", w.Body.String())
		}
	})

	t.Run("pages_show_json", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/pages/about", nil)
		r.Header.Set("Accept", "application/json")
		w := httptest.NewRecorder()
		fa.handler.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("status = %d; body: %s", w.Code, w.Body.String())
		}
		ct := w.Header().Get("Content-Type")
		if !strings.Contains(ct, "application/json") {
			t.Errorf("Content-Type = %q; want application/json", ct)
		}
	})

	t.Run("posts_slug_does_not_hit_pages", func(t *testing.T) {
		// /posts/about should 404 — About Page is in /pages, not /posts.
		r := httptest.NewRequest("GET", "/posts/about", nil)
		r.Header.Set("Accept", "application/json")
		w := httptest.NewRecorder()
		fa.handler.ServeHTTP(w, r)
		if w.Code != 404 {
			t.Errorf("status = %d; want 404 (slug belongs to pages, not posts)", w.Code)
		}
	})
}

// TestFull_customHandleRoute verifies that App.Handle registers a route that
// coexists with module routes.
func TestFull_customHandleRoute(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	m := NewModule((*testPost)(nil), Repo(repo), At("/posts"))
	app := New(MustConfig(Config{
		BaseURL: "https://example.com",
		Secret:  []byte("16bytessecretkey"),
	}))
	app.Handle("GET /health", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("ok"))
	}))
	app.Content(m)
	handler := app.Handler()

	// Custom route works.
	r := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("/health status = %d; want 200", w.Code)
	}
	if w.Body.String() != "ok" {
		t.Errorf("/health body = %q; want ok", w.Body.String())
	}

	// Module route still works.
	r2 := httptest.NewRequest("GET", "/posts", nil)
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, r2)
	if w2.Code != 200 {
		t.Errorf("/posts status = %d; want 200", w2.Code)
	}
}

// TestFull_globalMiddlewareOrder verifies that App.Use applies middleware in
// registration order — first Use argument is the outermost wrapper.
func TestFull_globalMiddlewareOrder(t *testing.T) {
	var mu sync.Mutex
	var order []string
	record := func(name string) func(http.Handler) http.Handler {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				order = append(order, name)
				mu.Unlock()
				next.ServeHTTP(w, r)
			})
		}
	}

	repo := NewMemoryRepo[*testPost]()
	m := NewModule((*testPost)(nil), Repo(repo), At("/posts"))
	app := New(MustConfig(Config{
		BaseURL: "https://example.com",
		Secret:  []byte("16bytessecretkey"),
	}))
	app.Use(record("A"), record("B"))
	app.Content(m)
	handler := app.Handler()

	r := httptest.NewRequest("GET", "/posts", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	mu.Lock()
	got := append([]string(nil), order...)
	mu.Unlock()

	if len(got) < 2 {
		t.Fatalf("expected ≥2 middleware invocations, got %d: %v", len(got), got)
	}
	if got[0] != "A" || got[1] != "B" {
		t.Errorf("middleware order = %v; want [A B ...]", got)
	}
}

// — G2: Role-based access via inline middleware (M1 + M2) —————————————————

// TestFull_roleCheck_denies verifies that an inline middleware using HasRole
// rejects unauthenticated requests with 403.
func TestFull_roleCheck_denies(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	m := NewModule((*testPost)(nil), Repo(repo), At("/posts"))
	app := New(MustConfig(Config{
		BaseURL: "https://example.com",
		Secret:  []byte("16bytessecretkey"),
	}))

	// Inline role-guard middleware built with M1's HasRole + M1's Context.
	requireEditor := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := ContextFrom(w, r)
			if !HasRole(ctx.User().Roles, Editor) {
				WriteError(w, r, ErrForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
	app.Use(requireEditor)
	app.Content(m)
	handler := app.Handler()

	// No user → GuestUser → denied.
	r := httptest.NewRequest("GET", "/posts", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Errorf("status = %d; want 403 (no role)", w.Code)
	}
}

// TestFull_roleCheck_allows verifies that a request with a matching role
// passes the inline middleware guard.
func TestFull_roleCheck_allows(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	m := NewModule((*testPost)(nil), Repo(repo), At("/posts"))
	app := New(MustConfig(Config{
		BaseURL: "https://example.com",
		Secret:  []byte("16bytessecretkey"),
	}))

	requireEditor := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := ContextFrom(w, r)
			if !HasRole(ctx.User().Roles, Editor) {
				WriteError(w, r, ErrForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
	app.Use(requireEditor)
	app.Content(m)
	handler := app.Handler()

	// Editor role → allowed.
	r := httptest.NewRequest("GET", "/posts", nil)
	r = withUser(r, editorUser())
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Errorf("status = %d; want 200 (editor role)", w.Code)
	}
}

// — G3: Signal fire-through (M1 + M2) ————————————————————————————————————

// TestFull_signalOnCreate verifies that AfterCreate fires when an item is
// created through the module's create handler.
func TestFull_signalOnCreate(t *testing.T) {
	var fired atomic.Int32
	repo := NewMemoryRepo[*testPost]()
	m := NewModule((*testPost)(nil),
		Repo(repo),
		At("/posts"),
		On(AfterCreate, func(_ Context, _ *testPost) error {
			fired.Add(1)
			return nil
		}),
	)

	body, _ := json.Marshal(map[string]string{"Title": "Signal Post"})
	w := httptest.NewRecorder()
	r := withUser(
		httptest.NewRequest("POST", "/posts", bytes.NewReader(body)),
		authorUser(),
	)
	m.createHandler(w, r)

	if w.Code != 201 {
		t.Fatalf("create: status = %d; body: %s", w.Code, w.Body.String())
	}

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if fired.Load() > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if fired.Load() == 0 {
		t.Error("AfterCreate did not fire within 500ms")
	}
}

// TestFull_signalOnDelete verifies that AfterDelete fires when an item is
// deleted through the module's delete handler.
func TestFull_signalOnDelete(t *testing.T) {
	var fired atomic.Int32
	repo := NewMemoryRepo[*testPost]()
	p := fullSeedPost(t, repo, "delete-me", "Delete Me")

	m := NewModule((*testPost)(nil),
		Repo(repo),
		At("/posts"),
		On(AfterDelete, func(_ Context, _ *testPost) error {
			fired.Add(1)
			return nil
		}),
	)

	w := httptest.NewRecorder()
	r := withUser(
		httptest.NewRequest("DELETE", "/posts/"+p.Slug, nil),
		editorUser(),
	)
	r.SetPathValue("slug", p.Slug)
	m.deleteHandler(w, r)

	if w.Code != 204 {
		t.Fatalf("delete: status = %d; body: %s", w.Code, w.Body.String())
	}

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if fired.Load() > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if fired.Load() == 0 {
		t.Error("AfterDelete did not fire within 500ms")
	}
}

// TestFull_signalCrossModuleIsolation verifies that signals registered on one
// module do not fire on another module in the same App.
func TestFull_signalCrossModuleIsolation(t *testing.T) {
	var postsFired, pagesFired atomic.Int32

	postRepo := NewMemoryRepo[*testPost]()
	pageRepo := NewMemoryRepo[*testMDPost]()

	postsMod := NewModule((*testPost)(nil),
		Repo(postRepo),
		At("/posts"),
		On(AfterCreate, func(_ Context, _ *testPost) error {
			postsFired.Add(1)
			return nil
		}),
	)
	pagesMod := NewModule((*testMDPost)(nil),
		Repo(pageRepo),
		At("/pages"),
		On(AfterCreate, func(_ Context, _ *testMDPost) error {
			pagesFired.Add(1)
			return nil
		}),
	)

	// Trigger create on posts module only.
	body, _ := json.Marshal(map[string]string{"Title": "Posts Only"})
	w := httptest.NewRecorder()
	r := withUser(
		httptest.NewRequest("POST", "/posts", bytes.NewReader(body)),
		authorUser(),
	)
	postsMod.createHandler(w, r)

	if w.Code != 201 {
		t.Fatalf("create posts: status = %d; body: %s", w.Code, w.Body.String())
	}

	// Wait for async signals.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if postsFired.Load() > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Posts AfterCreate must have fired.
	if postsFired.Load() == 0 {
		t.Error("posts AfterCreate did not fire within 500ms")
	}
	// Pages AfterCreate must NOT have fired.
	time.Sleep(20 * time.Millisecond) // small extra window to catch false fires
	if pagesFired.Load() != 0 {
		t.Errorf("pages AfterCreate fired = %d; want 0 (signal must not leak to other module)", pagesFired.Load())
	}

	// Suppress unused variable warning.
	_ = pagesMod
}

// — G4: Content negotiation, two modules (M2 + M4) ————————————————————————

// TestFull_jsonModule_noTemplates verifies that a module without templates
// returns JSON (200) when the client requests text/html -- not 406 (A35).
func TestFull_jsonModule_noTemplates(t *testing.T) {
	fa := newFullTestApp(t, nil, nil) // neither module has templates
	fullSeedPage(t, fa.pageRepo, "about", "About")

	r := httptest.NewRequest("GET", "/pages/about", nil)
	r.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	fa.handler.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Errorf("status = %d; want 200 (JSON fallback, A35)", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q; want application/json", ct)
	}
}

// TestFull_jsonModule_jsonWorks verifies that the same template-less module
// continues to serve JSON correctly.
func TestFull_jsonModule_jsonWorks(t *testing.T) {
	fa := newFullTestApp(t, nil, nil)
	fullSeedPage(t, fa.pageRepo, "home", "Home")

	r := httptest.NewRequest("GET", "/pages/home", nil)
	r.Header.Set("Accept", "application/json")
	w := httptest.NewRecorder()
	fa.handler.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Errorf("status = %d; want 200", w.Code)
	}
	if !strings.Contains(w.Header().Get("Content-Type"), "application/json") {
		t.Errorf("Content-Type = %q; want application/json", w.Header().Get("Content-Type"))
	}
}

// TestFull_htmlModule_templateFallback verifies that TemplatesOptional with
// only list.html present returns 200 for the list route and 406 for the show
// route (no show.html).
func TestFull_htmlModule_templateFallback(t *testing.T) {
	// list.html exists; show.html does not.
	dir := intTmpDir(t, `<p>list: {{len .Content}} items</p>`, "")

	repo := NewMemoryRepo[*testPost]()
	m := NewModule((*testPost)(nil), Repo(repo), At("/posts"), TemplatesOptional(dir))
	app := New(MustConfig(Config{
		BaseURL: "https://example.com",
		Secret:  []byte("16bytessecretkey"),
	}))
	app.Content(m)
	if err := m.parseTemplates(); err != nil {
		t.Fatalf("parseTemplates: %v", err)
	}
	handler := app.Handler()

	fullSeedPost(t, repo, "opt-post", "Optional Post")

	t.Run("list_200", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/posts", nil)
		r.Header.Set("Accept", "text/html")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Errorf("list status = %d; want 200; body: %s", w.Code, w.Body.String())
		}
	})

	t.Run("show_406", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/posts/opt-post", nil)
		r.Header.Set("Accept", "text/html")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 406 {
			t.Errorf("show status = %d; want 406 (no show.html)", w.Code)
		}
	})
}

// — G5: forge:head + schema through real render (M3 + M4) —————————————————

// TestFull_schemaForThroughTemplate verifies that forge_meta in a real
// template render produces a JSON-LD <script> block for Article content.
func TestFull_schemaForThroughTemplate(t *testing.T) {
	const tpl = `<!DOCTYPE html><html><head>{{forge_meta .Head .Content}}</head><body></body></html>`
	dir := intTmpDir(t, `<p>list</p>`, tpl)
	_, handler, repo := intSetup(t,
		Templates(dir),
		HeadFunc(func(_ Context, p *testPost) Head {
			return Head{Title: p.Title, Type: Article}
		}),
	)
	intSeed(t, repo, "schema-post", "Schema Post")

	r := httptest.NewRequest("GET", "/posts/schema-post", nil)
	r.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != 200 {
		t.Fatalf("status = %d; body: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `application/ld+json`) {
		t.Errorf("expected JSON-LD script tag in body, got:\n%s", body)
	}
	if !strings.Contains(body, `"Article"`) {
		t.Errorf("expected Article schema type in body, got:\n%s", body)
	}
}

// TestFull_forgeMarkdownInTemplate verifies that forge_markdown converts
// Markdown syntax to HTML inside a rendered template.
func TestFull_forgeMarkdownInTemplate(t *testing.T) {
	const tpl = `{{.Content.Body | forge_markdown}}`
	pageDir := intTmpDir(t, `<p>list</p>`, tpl)

	pageRepo := NewMemoryRepo[*testMDPost]()
	m := NewModule((*testMDPost)(nil), Repo(pageRepo), At("/pages"), Templates(pageDir))
	app := New(MustConfig(Config{
		BaseURL: "https://example.com",
		Secret:  []byte("16bytessecretkey"),
	}))
	app.Content(m)
	if err := m.parseTemplates(); err != nil {
		t.Fatalf("parseTemplates: %v", err)
	}
	handler := app.Handler()

	p := &testMDPost{
		Node:  Node{ID: NewID(), Slug: "bold-page", Status: Published},
		Title: "Bold Page",
		Body:  "**bold text** and `code`",
	}
	if err := pageRepo.Save(context.Background(), p); err != nil {
		t.Fatal(err)
	}

	r := httptest.NewRequest("GET", "/pages/bold-page", nil)
	r.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != 200 {
		t.Fatalf("status = %d; body: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "<strong>bold text</strong>") {
		t.Errorf("expected <strong>bold text</strong> in body, got:\n%s", body)
	}
	if !strings.Contains(body, "<code>code</code>") {
		t.Errorf("expected <code>code</code> in body, got:\n%s", body)
	}
}

// TestFull_breadcrumbs verifies that a non-empty Head.Breadcrumbs causes
// forge_meta to append a BreadcrumbList JSON-LD block after the primary schema.
func TestFull_breadcrumbs(t *testing.T) {
	const tpl = `{{forge_meta .Head .Content}}`
	dir := intTmpDir(t, `<p>list</p>`, tpl)
	_, handler, repo := intSetup(t,
		Templates(dir),
		HeadFunc(func(_ Context, p *testPost) Head {
			return Head{
				Title: p.Title,
				Type:  Article,
				Breadcrumbs: Crumbs(
					Crumb("Home", "https://example.com"),
					Crumb("Posts", "https://example.com/posts"),
				),
			}
		}),
	)
	intSeed(t, repo, "crumb-post", "Crumb Post")

	r := httptest.NewRequest("GET", "/posts/crumb-post", nil)
	r.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != 200 {
		t.Fatalf("status = %d; body: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "BreadcrumbList") {
		t.Errorf("expected BreadcrumbList in body, got:\n%s", body)
	}
	// Two separate ld+json script blocks — Article + BreadcrumbList.
	count := strings.Count(body, `application/ld+json`)
	if count < 2 {
		t.Errorf("expected 2 ld+json script blocks, got %d:\n%s", count, body)
	}
}

// — G6: SEO wiring (M2 + M3) ——————————————————————————————————————————————

// TestFull_sitemapAppendsInRobots verifies that App.SEO with Sitemaps: true
// produces a robots.txt containing a Sitemap directive.
func TestFull_sitemapAppendsInRobots(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	m := NewModule((*testPost)(nil), Repo(repo), At("/posts"), SitemapConfig{})
	app := New(MustConfig(Config{
		BaseURL: "https://example.com",
		Secret:  []byte("16bytessecretkey"),
	}))
	app.Content(m)
	app.SEO(&RobotsConfig{Sitemaps: true})
	handler := app.Handler()

	r := httptest.NewRequest("GET", "/robots.txt", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != 200 {
		t.Fatalf("GET /robots.txt status = %d; body: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "Sitemap:") {
		t.Errorf("expected 'Sitemap:' directive in robots.txt, got:\n%s", body)
	}
	if !strings.Contains(body, "https://example.com/sitemap.xml") {
		t.Errorf("expected sitemap URL in robots.txt, got:\n%s", body)
	}
}

// — G7: Error template fallback across two modules (M2 + M4) ——————————————

// TestFull_errorTemplate_firstMatch verifies that when both modules have an
// errors/404.html, the first registered module's template is used.
func TestFull_errorTemplate_firstMatch(t *testing.T) {
	origLookup := errorTemplateLookup
	t.Cleanup(func() { setErrorTemplateLookup(origLookup) })

	postDir := intTmpDir(t, `<p>list</p>`, `<p>show</p>`)
	pageDir := intTmpDir(t, `<p>list</p>`, `<p>show</p>`)
	writeErrTemplate(t, postDir, 404, `<p>posts-first-match</p>`)
	writeErrTemplate(t, pageDir, 404, `<p>pages-second</p>`)

	fa := newFullTestApp(t,
		[]Option{Templates(postDir)},
		[]Option{Templates(pageDir)},
	)

	r := httptest.NewRequest("GET", "/posts/no-such", nil)
	r.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	fa.handler.ServeHTTP(w, r)

	if w.Code != 404 {
		t.Fatalf("status = %d; want 404", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "posts-first-match") {
		t.Errorf("expected first module's error template, got:\n%s", body)
	}
	if strings.Contains(body, "pages-second") {
		t.Errorf("unexpected second module's template in first-match scenario:\n%s", body)
	}
}

// TestFull_errorTemplate_fallsThrough verifies that when the first module has
// no errors/ directory, the second module's error template is used.
func TestFull_errorTemplate_fallsThrough(t *testing.T) {
	origLookup := errorTemplateLookup
	t.Cleanup(func() { setErrorTemplateLookup(origLookup) })

	postDir := intTmpDir(t, `<p>list</p>`, `<p>show</p>`)
	// postDir intentionally has no errors/ subdirectory.
	pageDir := intTmpDir(t, `<p>list</p>`, `<p>show</p>`)
	writeErrTemplate(t, pageDir, 404, `<p>pages-fallthrough-404</p>`)

	fa := newFullTestApp(t,
		[]Option{Templates(postDir)},
		[]Option{Templates(pageDir)},
	)

	r := httptest.NewRequest("GET", "/posts/no-such", nil)
	r.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	fa.handler.ServeHTTP(w, r)

	if w.Code != 404 {
		t.Fatalf("status = %d; want 404", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "pages-fallthrough-404") {
		t.Errorf("expected second module's error template (fallthrough), got:\n%s", body)
	}
}

// — G8: TemplateData end-to-end (M3 + M4) ————————————————————————————————

// TestFull_templateData_siteName verifies that Config.BaseURL hostname is
// propagated to {{.SiteName}} inside a rendered HTML template.
func TestFull_templateData_siteName(t *testing.T) {
	const tpl = `site:{{.SiteName}}`
	dir := intTmpDir(t, `<p>list</p>`, tpl)
	_, handler, repo := intSetup(t, Templates(dir))
	intSeed(t, repo, "sn-post", "SiteName Post")

	r := httptest.NewRequest("GET", "/posts/sn-post", nil)
	r.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != 200 {
		t.Fatalf("status = %d; body: %s", w.Code, w.Body.String())
	}
	// Config.BaseURL is "https://example.com" → hostname is "example.com".
	if !strings.Contains(w.Body.String(), "site:example.com") {
		t.Errorf("expected site:example.com in body, got: %s", w.Body.String())
	}
}

// TestFull_templateData_requestURL verifies that the live *http.Request is
// accessible in the template and {{.Request.URL.Path}} matches the request.
func TestFull_templateData_requestURL(t *testing.T) {
	const tpl = `path:{{.Request.URL.Path}}`
	dir := intTmpDir(t, `list:{{.Request.URL.Path}}`, tpl)
	_, handler, repo := intSetup(t, Templates(dir))
	intSeed(t, repo, "url-post", "URL Post")

	r := httptest.NewRequest("GET", "/posts/url-post", nil)
	r.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != 200 {
		t.Fatalf("status = %d; body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "path:/posts/url-post") {
		t.Errorf("expected path:/posts/url-post in body, got: %s", w.Body.String())
	}
}

// — G9: Social + SitemapConfig (M5 + M3) ——————————————————————————————————

// TestFull_social_ogTagsInHTML verifies that a module configured with
// Social(OpenGraph, TwitterCard) and SitemapConfig{} (M3) renders og:title and
// twitter:card meta tags inside forge:head when HeadFunc returns a non-empty Title.
func TestFull_social_ogTagsInHTML(t *testing.T) {
	const show = `<!DOCTYPE html><html><head>{{template "forge:head" .}}</head></html>`
	dir := intTmpDir(t, `<p>list</p>`, show)
	_, handler, repo := intSetup(t,
		Social(OpenGraph, TwitterCard),
		SitemapConfig{},
		HeadFunc(func(_ Context, p *testPost) Head {
			return Head{Title: p.Title, Description: "A test post"}
		}),
		Templates(dir),
	)
	intSeed(t, repo, "og-post", "OG World")

	r := httptest.NewRequest("GET", "/posts/og-post", nil)
	r.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `property="og:title"`) {
		t.Errorf("og:title missing from body:\n%s", body)
	}
	if !strings.Contains(body, `name="twitter:card"`) {
		t.Errorf("twitter:card missing from body:\n%s", body)
	}
}

// TestFull_social_draftReturns404 verifies that a Draft post returns 404
// when Social and SitemapConfig options are active — lifecycle is enforced
// regardless of which M5 options are present.
func TestFull_social_draftReturns404(t *testing.T) {
	dir := intTmpDir(t, `<p>list</p>`, `<p>{{.Content.Title}}</p>`)
	_, handler, repo := intSetup(t,
		Social(OpenGraph, TwitterCard),
		SitemapConfig{},
		Templates(dir),
	)
	// Seed a draft post directly (intSeed always seeds Published).
	draft := &testPost{
		Node:  Node{ID: NewID(), Slug: "og-draft", Status: Draft},
		Title: "Draft OG Post",
	}
	if err := repo.Save(context.Background(), draft); err != nil {
		t.Fatalf("seed draft: %v", err)
	}

	r := httptest.NewRequest("GET", "/posts/og-draft", nil)
	r.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d (want 404); body: %s", w.Code, w.Body.String())
	}
}

// — G10: AI indexing + content negotiation (M5 + M4) ————————————————————

// TestFull_ai_llmsTxt_publishedPresent verifies that /llms.txt lists Published
// items and excludes Draft items when AIIndex(LLMsTxt) is configured.
func TestFull_ai_llmsTxt_publishedPresent(t *testing.T) {
	repo := NewMemoryRepo[*testAIPost]()
	pub := seedAIPost(t, repo, "AI World", "body text", Published)
	_ = seedAIPost(t, repo, "AI Draft", "draft body", Draft)

	store := NewLLMsStore("example.com")
	m := NewModule((*testAIPost)(nil),
		Repo(repo),
		At("/posts"),
		AIIndex(LLMsTxt),
		HeadFunc(aiHeadFunc),
	)
	m.setAIRegistry(store, "https://example.com")
	m.regenerateAI(testAICtx())

	mux := http.NewServeMux()
	m.Register(mux)
	mux.Handle("GET /llms.txt", store.CompactHandler())

	r := httptest.NewRequest("GET", "/llms.txt", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, pub.Title) {
		t.Errorf("published title %q missing from /llms.txt:\n%s", pub.Title, body)
	}
	if strings.Contains(body, "AI Draft") {
		t.Errorf("/llms.txt should not contain Draft item:\n%s", body)
	}
}

// TestFull_ai_aiDoc_published verifies that /posts/{slug}/aidoc returns 200
// for a Published item and contains the AIDoc v1 header.
func TestFull_ai_aiDoc_published(t *testing.T) {
	repo := NewMemoryRepo[*testAIPost]()
	pub := seedAIPost(t, repo, "AIDoc Published", "body text", Published)

	store := NewLLMsStore("example.com")
	m := NewModule((*testAIPost)(nil),
		Repo(repo),
		At("/posts"),
		AIIndex(AIDoc),
		HeadFunc(aiHeadFunc),
	)
	m.setAIRegistry(store, "https://example.com")

	mux := http.NewServeMux()
	m.Register(mux)

	r := httptest.NewRequest("GET", "/posts/"+pub.Slug+"/aidoc", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "+++aidoc+v1+++") {
		t.Errorf("body missing AIDoc header:\n%s", w.Body.String())
	}
}

// TestFull_ai_aiDoc_draftReturns404 verifies that /posts/{slug}/aidoc returns
// 404 for a Draft item — lifecycle enforcement on the AIDoc endpoint.
func TestFull_ai_aiDoc_draftReturns404(t *testing.T) {
	repo := NewMemoryRepo[*testAIPost]()
	draft := seedAIPost(t, repo, "AIDoc Draft", "body text", Draft)

	store := NewLLMsStore("example.com")
	m := NewModule((*testAIPost)(nil),
		Repo(repo),
		At("/posts"),
		AIIndex(AIDoc),
		HeadFunc(aiHeadFunc),
	)
	m.setAIRegistry(store, "https://example.com")

	mux := http.NewServeMux()
	m.Register(mux)

	r := httptest.NewRequest("GET", "/posts/"+draft.Slug+"/aidoc", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d (want 404); body: %s", w.Code, w.Body.String())
	}
}

// TestFull_ai_markdownContentNeg verifies that Accept: text/markdown returns
// the Markdown() body alongside the AIDoc option being active (M4 + M5).
func TestFull_ai_markdownContentNeg(t *testing.T) {
	repo := NewMemoryRepo[*testAIPost]()
	pub := seedAIPost(t, repo, "Markdown Negotiation", "markdown body text", Published)

	store := NewLLMsStore("example.com")
	m := NewModule((*testAIPost)(nil),
		Repo(repo),
		At("/posts"),
		AIIndex(AIDoc),
		HeadFunc(aiHeadFunc),
	)
	m.setAIRegistry(store, "https://example.com")

	mux := http.NewServeMux()
	m.Register(mux)

	r := httptest.NewRequest("GET", "/posts/"+pub.Slug, nil)
	r.Header.Set("Accept", "text/markdown")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body: %s", w.Code, w.Body.String())
	}
	ct := w.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/markdown") {
		t.Errorf("Content-Type = %q; want text/markdown", ct)
	}
	if !strings.Contains(w.Body.String(), pub.Title) {
		t.Errorf("markdown body missing post title %q:\n%s", pub.Title, w.Body.String())
	}
}

// — G11: RSS feed + AfterPublish signal (M5 + M1) ————————————————————————

// TestFull_feed_publishedInFeed verifies that /posts/feed.xml returns a valid
// RSS 2.0 document containing Published items.
func TestFull_feed_publishedInFeed(t *testing.T) {
	repo := NewMemoryRepo[*testFeedPost]()
	pub := seedFeedPost(t, repo, "Feed World", Published)

	store := NewFeedStore("example.com", "https://example.com")
	m := NewModule((*testFeedPost)(nil),
		Repo(repo),
		At("/posts"),
		Feed(FeedConfig{Title: "Integration Blog"}),
		HeadFunc(feedHeadFunc),
	)
	m.setFeedStore(store, "https://example.com")
	m.regenerateFeed(testFeedCtx())

	mux := http.NewServeMux()
	m.Register(mux)

	r := httptest.NewRequest("GET", "/posts/feed.xml", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `version="2.0"`) {
		t.Errorf("body missing RSS version attribute:\n%s", body)
	}
	if !strings.Contains(body, pub.Title) {
		t.Errorf("published title %q missing from feed:\n%s", pub.Title, body)
	}
}

// TestFull_feed_draftAbsent verifies that Draft items are excluded from the
// RSS feed at /posts/feed.xml.
func TestFull_feed_draftAbsent(t *testing.T) {
	repo := NewMemoryRepo[*testFeedPost]()
	_ = seedFeedPost(t, repo, "Feed Published", Published)
	_ = seedFeedPost(t, repo, "Feed Draft Title", Draft)

	store := NewFeedStore("example.com", "https://example.com")
	m := NewModule((*testFeedPost)(nil),
		Repo(repo),
		At("/posts"),
		Feed(FeedConfig{Title: "Blog"}),
		HeadFunc(feedHeadFunc),
	)
	m.setFeedStore(store, "https://example.com")
	m.regenerateFeed(testFeedCtx())

	mux := http.NewServeMux()
	m.Register(mux)

	r := httptest.NewRequest("GET", "/posts/feed.xml", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)

	if strings.Contains(w.Body.String(), "Feed Draft Title") {
		t.Errorf("feed should not contain Draft item:\n%s", w.Body.String())
	}
}

// TestFull_feed_afterPublishSignalFires verifies that the AfterPublish signal
// fires when a Draft item is transitioned to Published while Feed is active
// (M5 + M1 cross-milestone).
func TestFull_feed_afterPublishSignalFires(t *testing.T) {
	var fired atomic.Int32
	repo := NewMemoryRepo[*testFeedPost]()

	// Seed a Draft post that will be updated to Published.
	draft := &testFeedPost{
		Node:  Node{ID: NewID(), Slug: "signal-feed", Status: Draft},
		Title: "Signal Feed Post",
	}
	if err := repo.Save(context.Background(), draft); err != nil {
		t.Fatalf("seed draft: %v", err)
	}

	store := NewFeedStore("example.com", "https://example.com")
	m := NewModule((*testFeedPost)(nil),
		Repo(repo),
		At("/posts"),
		Feed(FeedConfig{Title: "Blog"}),
		HeadFunc(feedHeadFunc),
		On(AfterPublish, func(_ Context, _ *testFeedPost) error {
			fired.Add(1)
			return nil
		}),
	)
	m.setFeedStore(store, "https://example.com")

	body, _ := json.Marshal(map[string]any{"Title": "Signal Feed Post", "Status": "published"})
	w := httptest.NewRecorder()
	r := withUser(
		httptest.NewRequest("PUT", "/posts/signal-feed", bytes.NewReader(body)),
		editorUser(),
	)
	r.SetPathValue("slug", "signal-feed")
	m.updateHandler(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("update: status = %d; body: %s", w.Code, w.Body.String())
	}

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if fired.Load() > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if fired.Load() == 0 {
		t.Error("AfterPublish did not fire within 500ms")
	}
}

// — G12: Full M5 stack (M5 + M3 + M4) ———————————————————————————————————

// TestFull_fullM5_htmlHasOGTags verifies that a module with all M5 options
// (Social, AIIndex, Feed, SitemapConfig) plus M3/M4 (HeadFunc, Templates)
// still renders og:title and twitter:card correctly in forge:head.
func TestFull_fullM5_htmlHasOGTags(t *testing.T) {
	const show = `<!DOCTYPE html><html><head>{{template "forge:head" .}}</head></html>`
	dir := intTmpDir(t, `<p>list</p>`, show)

	aiStore := NewLLMsStore("example.com")
	feedSt := NewFeedStore("example.com", "https://example.com")
	repo := NewMemoryRepo[*testAIPost]()

	m := NewModule((*testAIPost)(nil),
		Repo(repo),
		At("/posts"),
		Social(OpenGraph, TwitterCard),
		AIIndex(LLMsTxt, AIDoc),
		Feed(FeedConfig{Title: "Full M5 Blog"}),
		SitemapConfig{},
		HeadFunc(aiHeadFunc),
		Templates(dir),
	)
	m.setAIRegistry(aiStore, "https://example.com")
	m.setFeedStore(feedSt, "https://example.com")

	pub := seedAIPost(t, repo, "Full M5 Post", "body text", Published)
	m.regenerateAI(testAICtx())
	m.regenerateFeed(testAICtx())

	if err := m.parseTemplates(); err != nil {
		t.Fatalf("parseTemplates: %v", err)
	}

	mux := http.NewServeMux()
	m.Register(mux)
	mux.Handle("GET /llms.txt", aiStore.CompactHandler())

	r := httptest.NewRequest("GET", "/posts/"+pub.Slug, nil)
	r.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `property="og:title"`) {
		t.Errorf("og:title missing from full M5 HTML:\n%s", body)
	}
	if !strings.Contains(body, `name="twitter:card"`) {
		t.Errorf("twitter:card missing from full M5 HTML:\n%s", body)
	}
}

// TestFull_fullM5_llmsTxt verifies that /llms.txt is populated with Published
// items when the full M5 option set is active.
func TestFull_fullM5_llmsTxt(t *testing.T) {
	aiStore := NewLLMsStore("example.com")
	feedSt := NewFeedStore("example.com", "https://example.com")
	repo := NewMemoryRepo[*testAIPost]()

	m := NewModule((*testAIPost)(nil),
		Repo(repo),
		At("/posts"),
		Social(OpenGraph, TwitterCard),
		AIIndex(LLMsTxt, AIDoc),
		Feed(FeedConfig{Title: "Full M5 Blog"}),
		HeadFunc(aiHeadFunc),
	)
	m.setAIRegistry(aiStore, "https://example.com")
	m.setFeedStore(feedSt, "https://example.com")

	pub := seedAIPost(t, repo, "LLMs Entry", "body text", Published)
	m.regenerateAI(testAICtx())

	mux := http.NewServeMux()
	m.Register(mux)
	mux.Handle("GET /llms.txt", aiStore.CompactHandler())

	r := httptest.NewRequest("GET", "/llms.txt", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), pub.Title) {
		t.Errorf("published title %q missing from /llms.txt:\n%s", pub.Title, w.Body.String())
	}
}

// TestFull_fullM5_aiDoc verifies that /posts/{slug}/aidoc returns a valid
// AIDoc response when the full M5 option set is active.
func TestFull_fullM5_aiDoc(t *testing.T) {
	aiStore := NewLLMsStore("example.com")
	feedSt := NewFeedStore("example.com", "https://example.com")
	repo := NewMemoryRepo[*testAIPost]()

	m := NewModule((*testAIPost)(nil),
		Repo(repo),
		At("/posts"),
		Social(OpenGraph, TwitterCard),
		AIIndex(LLMsTxt, AIDoc),
		Feed(FeedConfig{Title: "Full M5 Blog"}),
		HeadFunc(aiHeadFunc),
	)
	m.setAIRegistry(aiStore, "https://example.com")
	m.setFeedStore(feedSt, "https://example.com")

	pub := seedAIPost(t, repo, "AIDoc Full M5", "body text", Published)

	mux := http.NewServeMux()
	m.Register(mux)

	r := httptest.NewRequest("GET", "/posts/"+pub.Slug+"/aidoc", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "+++aidoc+v1+++") {
		t.Errorf("body missing AIDoc header:\n%s", w.Body.String())
	}
}

// TestFull_fullM5_feed verifies that /posts/feed.xml returns a valid RSS 2.0
// document with Published items when the full M5 option set is active.
func TestFull_fullM5_feed(t *testing.T) {
	aiStore := NewLLMsStore("example.com")
	feedSt := NewFeedStore("example.com", "https://example.com")
	repo := NewMemoryRepo[*testAIPost]()

	m := NewModule((*testAIPost)(nil),
		Repo(repo),
		At("/posts"),
		Social(OpenGraph, TwitterCard),
		AIIndex(LLMsTxt, AIDoc),
		Feed(FeedConfig{Title: "Full M5 Blog"}),
		HeadFunc(aiHeadFunc),
	)
	m.setAIRegistry(aiStore, "https://example.com")
	m.setFeedStore(feedSt, "https://example.com")

	pub := seedAIPost(t, repo, "Feed Full M5", "body text", Published)
	m.regenerateFeed(testAICtx())

	mux := http.NewServeMux()
	m.Register(mux)

	r := httptest.NewRequest("GET", "/posts/feed.xml", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `version="2.0"`) {
		t.Errorf("body missing RSS version:\n%s", body)
	}
	if !strings.Contains(body, pub.Title) {
		t.Errorf("published title %q missing from feed:\n%s", pub.Title, body)
	}
}

// — G13: Cookie consent enforcement (M6, Decision 5) ————————————————————

// TestFull_consent_setNecessarySetsHeader verifies that SetCookie writes a
// Set-Cookie header for a Necessary cookie, and that ConsentFor(Necessary)
// is always true without any forge_consent cookie in the request.
func TestFull_consent_setNecessarySetsHeader(t *testing.T) {
	c := Cookie{
		Name:     "session",
		Category: Necessary,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	}
	w := httptest.NewRecorder()
	SetCookie(w, c, "abc123")

	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected 1 Set-Cookie header, got %d", len(cookies))
	}
	if cookies[0].Name != "session" || cookies[0].Value != "abc123" {
		t.Errorf("cookie = %+v; want name=session value=abc123", cookies[0])
	}

	// ConsentFor(Necessary) must be true without any forge_consent cookie.
	r := httptest.NewRequest("GET", "/", nil)
	if !ConsentFor(r, Necessary) {
		t.Error("ConsentFor(Necessary) = false; want always true")
	}
}

// TestFull_consent_noConsentSkips verifies that SetCookieIfConsented returns
// false and writes no Set-Cookie header when forge_consent is absent.
func TestFull_consent_noConsentSkips(t *testing.T) {
	c := Cookie{Name: "theme", Category: Preferences}
	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()

	set := SetCookieIfConsented(w, r, c, "dark")
	if set {
		t.Error("SetCookieIfConsented returned true without consent")
	}
	if got := w.Result().Cookies(); len(got) > 0 {
		t.Errorf("expected no Set-Cookie header; got %v", got)
	}
	if ConsentFor(r, Preferences) {
		t.Error("ConsentFor(Preferences) = true without forge_consent; want false")
	}
}

// TestFull_consent_grantAllowsSet verifies that GrantConsent + SetCookieIfConsented
// works end-to-end: consent written by GrantConsent is carried into a subsequent
// request and allows the non-Necessary cookie to be set.
func TestFull_consent_grantAllowsSet(t *testing.T) {
	// Step 1: grant consent for Preferences and Analytics.
	grantW := httptest.NewRecorder()
	GrantConsent(grantW, Preferences, Analytics)

	consentCookies := grantW.Result().Cookies()
	if len(consentCookies) == 0 {
		t.Fatal("GrantConsent wrote no Set-Cookie header")
	}

	// Step 2: build a request that carries the consent cookie.
	r := httptest.NewRequest("GET", "/", nil)
	for _, ck := range consentCookies {
		r.AddCookie(ck)
	}

	// Step 3: SetCookieIfConsented succeeds for Preferences.
	setW := httptest.NewRecorder()
	c := Cookie{Name: "theme", Category: Preferences}
	if !SetCookieIfConsented(setW, r, c, "dark") {
		t.Error("SetCookieIfConsented returned false despite granted Preferences consent")
	}

	// Marketing was not granted.
	if ConsentFor(r, Marketing) {
		t.Error("ConsentFor(Marketing) = true; Marketing was not granted")
	}
}

// TestFull_consent_revokeConsentFalse verifies that RevokeConsent writes an
// expired Set-Cookie for forge_consent, and that ConsentFor returns false for
// non-Necessary categories on a request carrying the revoked cookie.
func TestFull_consent_revokeConsentFalse(t *testing.T) {
	revW := httptest.NewRecorder()
	RevokeConsent(revW)

	revCookies := revW.Result().Cookies()
	if len(revCookies) == 0 {
		t.Fatal("RevokeConsent wrote no Set-Cookie header")
	}
	if revCookies[0].MaxAge != -1 {
		t.Errorf("revoke cookie MaxAge = %d; want -1", revCookies[0].MaxAge)
	}

	// A request with the revoked (empty value) cookie loses all non-Necessary consent.
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{Name: "forge_consent", Value: ""})
	if ConsentFor(r, Preferences) {
		t.Error("ConsentFor(Preferences) = true after RevokeConsent; want false")
	}
}

// — G14: Consent lifecycle wired through a handler (M6 + M2) ————————————

// TestFull_consent_moduleHandlerSetsPreferences verifies the full consent
// lifecycle wired through an HTTP handler (M2 pattern): without consent the
// handler returns 204 and sets no cookie; after GrantConsent the handler
// returns 200 and sets the Preferences cookie.
func TestFull_consent_moduleHandlerSetsPreferences(t *testing.T) {
	themeCookie := Cookie{Name: "theme", Category: Preferences}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /set-preference", func(w http.ResponseWriter, r *http.Request) {
		if SetCookieIfConsented(w, r, themeCookie, "dark") {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusNoContent)
		}
	})

	// Without consent: handler returns 204.
	r1 := httptest.NewRequest("GET", "/set-preference", nil)
	w1 := httptest.NewRecorder()
	mux.ServeHTTP(w1, r1)
	if w1.Code != http.StatusNoContent {
		t.Errorf("without consent: status = %d; want 204", w1.Code)
	}

	// Grant Preferences consent on a separate response.
	grantW := httptest.NewRecorder()
	GrantConsent(grantW, Preferences)

	// With consent cookie: handler returns 200 and writes the theme cookie.
	r2 := httptest.NewRequest("GET", "/set-preference", nil)
	for _, ck := range grantW.Result().Cookies() {
		r2.AddCookie(ck)
	}
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, r2)
	if w2.Code != http.StatusOK {
		t.Errorf("with consent: status = %d; want 200", w2.Code)
	}
	setCookies := w2.Result().Cookies()
	if len(setCookies) == 0 || setCookies[0].Name != "theme" {
		t.Errorf("theme cookie not set: %v", setCookies)
	}
}

// TestFull_consent_clearCookieExpiresHeader verifies that ClearCookie writes a
// Set-Cookie header with MaxAge -1, and that ConsentFor(Necessary) remains true
// even when forge_consent contains garbage data.
func TestFull_consent_clearCookieExpiresHeader(t *testing.T) {
	c := Cookie{Name: "prefs", Category: Preferences}
	w := httptest.NewRecorder()
	ClearCookie(w, c)

	cookies := w.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("ClearCookie wrote no Set-Cookie header")
	}
	if cookies[0].MaxAge != -1 {
		t.Errorf("MaxAge = %d; want -1", cookies[0].MaxAge)
	}
	if cookies[0].Expires.After(time.Now().UTC()) {
		t.Errorf("Expires = %v is in the future; want past", cookies[0].Expires)
	}

	// ConsentFor(Necessary) is always true — even with corrupted forge_consent.
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{Name: "forge_consent", Value: "garbage-data,,,"})
	if !ConsentFor(r, Necessary) {
		t.Error("ConsentFor(Necessary) = false; want always true")
	}
}

// — G15: Cookie manifest + App integration (M6 + M2 + M1) ———————————————

// TestFull_manifest_mountedWhenDeclared verifies that /.well-known/cookies.json
// is mounted by App.Handler() when App.Cookies() has been called, returns 200,
// Content-Type application/json, and valid JSON with the correct site and count.
func TestFull_manifest_mountedWhenDeclared(t *testing.T) {
	app := New(MustConfig(Config{
		BaseURL: "https://example.com",
		Secret:  []byte("secret-key-for-test-use"),
	}))
	app.Cookies(
		Cookie{Name: "session", Category: Necessary, Purpose: "Auth session"},
		Cookie{Name: "theme", Category: Preferences, Purpose: "UI theme"},
	)
	h := app.Handler()

	r := httptest.NewRequest("GET", "/.well-known/cookies.json", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body: %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q; want application/json", ct)
	}
	var manifest map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &manifest); err != nil {
		t.Fatalf("invalid JSON: %v\nbody: %s", err, w.Body.String())
	}
	if manifest["site"] != "example.com" {
		t.Errorf("site = %v; want example.com", manifest["site"])
	}
	if count, _ := manifest["count"].(float64); int(count) != 2 {
		t.Errorf("count = %v; want 2", manifest["count"])
	}
}

// TestFull_manifest_sortedByName verifies that the manifest entries are sorted
// alphabetically, regardless of declaration order in App.Cookies().
func TestFull_manifest_sortedByName(t *testing.T) {
	app := New(MustConfig(Config{
		BaseURL: "https://example.com",
		Secret:  []byte("secret-key-for-test-use"),
	}))
	app.Cookies(
		Cookie{Name: "zebra", Category: Analytics, Purpose: "z"},
		Cookie{Name: "alpha", Category: Preferences, Purpose: "a"},
		Cookie{Name: "mango", Category: Marketing, Purpose: "m"},
	)
	h := app.Handler()

	r := httptest.NewRequest("GET", "/.well-known/cookies.json", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	var manifest struct {
		Cookies []struct {
			Name string `json:"name"`
		} `json:"cookies"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &manifest); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	want := []string{"alpha", "mango", "zebra"}
	for i, c := range manifest.Cookies {
		if c.Name != want[i] {
			t.Errorf("cookies[%d].name = %q; want %q", i, c.Name, want[i])
		}
	}
}

// TestFull_manifest_notMountedWhenNoDecls verifies that /.well-known/cookies.json
// returns 404 when App.Cookies() has never been called — no leaking of empty manifests.
func TestFull_manifest_notMountedWhenNoDecls(t *testing.T) {
	app := New(MustConfig(Config{
		BaseURL: "https://example.com",
		Secret:  []byte("secret-key-for-test-use"),
	}))
	h := app.Handler()

	r := httptest.NewRequest("GET", "/.well-known/cookies.json", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d; want 404 when no cookies declared", w.Code)
	}
}

// TestFull_manifest_authGuard verifies that App.CookiesManifestAuth (M1 BearerHMAC)
// blocks unauthenticated requests with 401 and passes valid Editor tokens through.
func TestFull_manifest_authGuard(t *testing.T) {
	const secret = "test-secret-long-enough"
	app := New(MustConfig(Config{
		BaseURL: "https://example.com",
		Secret:  []byte("secret-key-for-test-use"),
	}))
	app.Cookies(Cookie{Name: "session", Category: Necessary, Purpose: "Auth"})
	app.CookiesManifestAuth(BearerHMAC(secret))
	h := app.Handler()

	// Unauthenticated request — 401.
	r1 := httptest.NewRequest("GET", "/.well-known/cookies.json", nil)
	w1 := httptest.NewRecorder()
	h.ServeHTTP(w1, r1)
	if w1.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated: status = %d; want 401", w1.Code)
	}

	// Authenticated Editor — 200.
	tok, err := SignToken(User{ID: "u1", Roles: []Role{Editor}}, secret, 0)
	if err != nil {
		t.Fatalf("SignToken: %v", err)
	}
	r2 := httptest.NewRequest("GET", "/.well-known/cookies.json", nil)
	r2.Header.Set("Authorization", "Bearer "+tok)
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, r2)
	if w2.Code != http.StatusOK {
		t.Errorf("authenticated Editor: status = %d; want 200", w2.Code)
	}
}

// — G16: Redirect enforcement (M7, Decision 17) ——————————————————————————

// TestFull_redirect_permanent verifies that app.Redirect("/old", "/new", Permanent)
// issues a 301 with the correct Location header.
func TestFull_redirect_permanent(t *testing.T) {
	app := New(MustConfig(Config{
		BaseURL: "https://example.com",
		Secret:  []byte("16bytessecretkey"),
	}))
	app.Redirect("/old-path", "/new-path", Permanent)
	h := app.Handler()

	r := httptest.NewRequest("GET", "/old-path", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusMovedPermanently {
		t.Fatalf("status = %d; want 301", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/new-path" {
		t.Errorf("Location = %q; want /new-path", loc)
	}
}

// TestFull_redirect_gone verifies that app.Redirect("/removed", "", Gone)
// issues a 410 Gone response.
func TestFull_redirect_gone(t *testing.T) {
	app := New(MustConfig(Config{
		BaseURL: "https://example.com",
		Secret:  []byte("16bytessecretkey"),
	}))
	app.Redirect("/removed", "", Gone)
	h := app.Handler()

	r := httptest.NewRequest("GET", "/removed", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusGone {
		t.Fatalf("status = %d; want 410", w.Code)
	}
}

// TestFull_redirect_unknownPath verifies that an unregistered path returns 404
// from the fallback handler.
func TestFull_redirect_unknownPath(t *testing.T) {
	app := New(MustConfig(Config{
		BaseURL: "https://example.com",
		Secret:  []byte("16bytessecretkey"),
	}))
	h := app.Handler()

	r := httptest.NewRequest("GET", "/no-such-page", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want 404", w.Code)
	}
}

// TestFull_redirect_chainCollapsed verifies that two Redirect calls that form
// a chain are collapsed: when B→C is registered before A→B, the forward
// collapse fires so A is stored directly as A→C (Decision 24).
func TestFull_redirect_chainCollapsed(t *testing.T) {
	app := New(MustConfig(Config{
		BaseURL: "https://example.com",
		Secret:  []byte("16bytessecretkey"),
	}))
	// Register the destination leg first so the forward collapse can fire.
	app.Redirect("/b", "/c", Permanent)
	app.Redirect("/a", "/b", Permanent)
	h := app.Handler()

	r := httptest.NewRequest("GET", "/a", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusMovedPermanently {
		t.Fatalf("status = %d; want 301", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/c" {
		t.Errorf("Location = %q; want /c (chain collapsed)", loc)
	}
}

// — G17: Prefix redirect via Redirects(From) (M7 + M2) ——————————————————

// TestFull_prefix_redirect_rewritesPath verifies that a Redirects(From("/posts"), "/articles")
// option on app.Content() rewrites GET /posts/hello to 301 → /articles/hello.
func TestFull_prefix_redirect_rewritesPath(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	m := NewModule((*testPost)(nil), Repo(repo), At("/articles"))
	app := New(MustConfig(Config{
		BaseURL: "https://example.com",
		Secret:  []byte("16bytessecretkey"),
	}))
	app.Content(m, Redirects(From("/posts"), "/articles"))
	h := app.Handler()

	r := httptest.NewRequest("GET", "/posts/hello", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusMovedPermanently {
		t.Fatalf("status = %d; want 301", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/articles/hello" {
		t.Errorf("Location = %q; want /articles/hello", loc)
	}
}

// TestFull_prefix_redirect_exactBeatsPrefix verifies that an exact redirect
// entry takes priority over a prefix entry for the same base path.
func TestFull_prefix_redirect_exactBeatsPrefix(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	m := NewModule((*testPost)(nil), Repo(repo), At("/articles"))
	app := New(MustConfig(Config{
		BaseURL: "https://example.com",
		Secret:  []byte("16bytessecretkey"),
	}))
	app.Content(m, Redirects(From("/posts"), "/articles"))
	// Exact entry for /posts/about overrides the prefix rewrite.
	app.Redirect("/posts/about", "/about", Permanent)
	h := app.Handler()

	r := httptest.NewRequest("GET", "/posts/about", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusMovedPermanently {
		t.Fatalf("status = %d; want 301", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/about" {
		t.Errorf("Location = %q; want /about (exact beats prefix)", loc)
	}
}

// — G18: Full M7 stack — SQLRepo + manifest + ManifestAuth (M7 + M6 + M1) —

// TestFull_sqlrepo_satisfiesInterface verifies at compile time that
// *SQLRepo[*testPost] satisfies Repository[*testPost], and that NewSQLRepo
// returns a usable value at runtime.
func TestFull_sqlrepo_satisfiesInterface(t *testing.T) {
	db := newTestDB(t)
	var _ Repository[*testPost] = NewSQLRepo[*testPost](db)
}

// TestFull_manifest_redirect_alwaysMounted verifies that GET
// /.well-known/redirects.json is mounted unconditionally and returns valid JSON
// even when the redirect store is empty.
func TestFull_manifest_redirect_alwaysMounted(t *testing.T) {
	app := New(MustConfig(Config{
		BaseURL: "https://example.com",
		Secret:  []byte("16bytessecretkey"),
	}))
	h := app.Handler()

	r := httptest.NewRequest("GET", "/.well-known/redirects.json", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", w.Code)
	}
	var m map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("invalid JSON: %v\nbody: %s", err, w.Body.String())
	}
	if count, _ := m["count"].(float64); int(count) != 0 {
		t.Errorf("count = %v; want 0", m["count"])
	}
}

// TestFull_manifest_redirect_reflectsEntries verifies that redirects added via
// App.Redirect() appear in /.well-known/redirects.json.
func TestFull_manifest_redirect_reflectsEntries(t *testing.T) {
	app := New(MustConfig(Config{
		BaseURL: "https://example.com",
		Secret:  []byte("16bytessecretkey"),
	}))
	app.Redirect("/old", "/new", Permanent)
	app.Redirect("/gone", "", Gone)
	h := app.Handler()

	r := httptest.NewRequest("GET", "/.well-known/redirects.json", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", w.Code)
	}
	var m map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if count, _ := m["count"].(float64); int(count) != 2 {
		t.Errorf("count = %v; want 2", m["count"])
	}
}

// TestFull_manifest_redirect_authGuard verifies that App.RedirectManifestAuth
// (Amendment A22) blocks unauthenticated requests with 401 and passes valid
// Editor tokens through.
func TestFull_manifest_redirect_authGuard(t *testing.T) {
	const secret = "test-secret-long-enough"
	app := New(MustConfig(Config{
		BaseURL: "https://example.com",
		Secret:  []byte("secret-key-for-test-use"),
	}))
	app.Redirect("/old", "/new", Permanent)
	app.RedirectManifestAuth(BearerHMAC(secret))
	h := app.Handler()

	// Unauthenticated — 401.
	r1 := httptest.NewRequest("GET", "/.well-known/redirects.json", nil)
	w1 := httptest.NewRecorder()
	h.ServeHTTP(w1, r1)
	if w1.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated: status = %d; want 401", w1.Code)
	}

	// Authenticated Editor — 200.
	tok, err := SignToken(User{ID: "u1", Roles: []Role{Editor}}, secret, 0)
	if err != nil {
		t.Fatalf("SignToken: %v", err)
	}
	r2 := httptest.NewRequest("GET", "/.well-known/redirects.json", nil)
	r2.Header.Set("Authorization", "Bearer "+tok)
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, r2)
	if w2.Code != http.StatusOK {
		t.Errorf("authenticated Editor: status = %d; want 200", w2.Code)
	}
}

// — G19: Scheduler end-to-end + AfterPublish signal (M8 + M1) ——————————————

// TestFull_scheduler_publishesOverdue verifies the end-to-end Scheduled→Published
// transition: a past-due item is published, a future item is left Scheduled,
// and the AfterPublish signal (M1) fires exactly once.
func TestFull_scheduler_publishesOverdue(t *testing.T) {
	var fired atomic.Int32
	repo := NewMemoryRepo[*testPost]()
	bgCtx := NewBackgroundContext("example.com")

	m := NewModule((*testPost)(nil),
		Repo(repo),
		At("/posts"),
		On(AfterPublish, func(_ Context, _ *testPost) error {
			fired.Add(1)
			return nil
		}),
	)

	past := time.Now().UTC().Add(-2 * time.Minute)
	future := time.Now().UTC().Add(30 * time.Minute)

	overdue := &testPost{Node: Node{ID: NewID(), Slug: "overdue", Status: Scheduled, ScheduledAt: &past}}
	pending := &testPost{Node: Node{ID: NewID(), Slug: "pending", Status: Scheduled, ScheduledAt: &future}}

	if err := repo.Save(context.Background(), overdue); err != nil {
		t.Fatalf("seed overdue: %v", err)
	}
	if err := repo.Save(context.Background(), pending); err != nil {
		t.Fatalf("seed pending: %v", err)
	}

	now := time.Now().UTC()
	published, next, err := m.processScheduled(bgCtx, now)
	if err != nil {
		t.Fatalf("processScheduled: %v", err)
	}
	if published != 1 {
		t.Errorf("published = %d; want 1", published)
	}
	if next == nil {
		t.Fatal("next should not be nil — pending item has a future ScheduledAt")
	}

	// Overdue item must be Published with ScheduledAt cleared.
	got, err := repo.FindByID(context.Background(), overdue.ID)
	if err != nil {
		t.Fatalf("FindByID overdue: %v", err)
	}
	if got.Status != Published {
		t.Errorf("overdue status = %v; want Published", got.Status)
	}
	if got.ScheduledAt != nil {
		t.Errorf("overdue ScheduledAt = %v; want nil", got.ScheduledAt)
	}
	if got.PublishedAt.IsZero() {
		t.Error("overdue PublishedAt should be set")
	}

	// Pending item must remain Scheduled.
	gotPending, err := repo.FindByID(context.Background(), pending.ID)
	if err != nil {
		t.Fatalf("FindByID pending: %v", err)
	}
	if gotPending.Status != Scheduled {
		t.Errorf("pending status = %v; want Scheduled", gotPending.Status)
	}

	// AfterPublish (M1 signal) must fire exactly once — give dispatchAfter time.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) && fired.Load() < 1 {
		time.Sleep(5 * time.Millisecond)
	}
	if got := fired.Load(); got != 1 {
		t.Errorf("AfterPublish fired %d times; want 1", got)
	}
}

// — G20: Scheduler wired via App.Content() (M8 + M2 + M3) —————————————————

// TestFull_scheduler_appWiring verifies that App.Content() registers modules
// into schedulerModules (Amendment A26), that a Scheduler built from those
// modules processes overdue items, and that the soonest future ScheduledAt
// is returned for the adaptive timer.
func TestFull_scheduler_appWiring(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()

	app := New(MustConfig(Config{
		BaseURL: "https://example.com",
		Secret:  []byte("16bytessecretkey"),
	}))

	m := NewModule((*testPost)(nil),
		Repo(repo),
		At("/posts"),
		SitemapConfig{},
	)
	app.Content(m)

	// Amendment A26: schedulerModules must contain the registered module.
	if len(app.schedulerModules) != 1 {
		t.Fatalf("schedulerModules len = %d; want 1", len(app.schedulerModules))
	}

	// Seed overdue + future items.
	past := time.Now().UTC().Add(-1 * time.Minute)
	future := time.Now().UTC().Add(20 * time.Minute)
	p1 := &testPost{Node: Node{ID: NewID(), Slug: "sched-past", Status: Scheduled, ScheduledAt: &past}}
	p2 := &testPost{Node: Node{ID: NewID(), Slug: "sched-future", Status: Scheduled, ScheduledAt: &future}}
	if err := repo.Save(context.Background(), p1); err != nil {
		t.Fatalf("seed p1: %v", err)
	}
	if err := repo.Save(context.Background(), p2); err != nil {
		t.Fatalf("seed p2: %v", err)
	}

	// Build a Scheduler directly from the wired modules to test the A26 integration.
	bgCtx := NewBackgroundContext("example.com")
	sched := newScheduler(app.schedulerModules, bgCtx)
	next := sched.tick()

	// Overdue item must be Published.
	got, err := repo.FindByID(context.Background(), p1.ID)
	if err != nil {
		t.Fatalf("FindByID p1: %v", err)
	}
	if got.Status != Published {
		t.Errorf("p1 status = %v; want Published", got.Status)
	}

	// Future item must remain Scheduled.
	gotFuture, err := repo.FindByID(context.Background(), p2.ID)
	if err != nil {
		t.Fatalf("FindByID p2: %v", err)
	}
	if gotFuture.Status != Scheduled {
		t.Errorf("p2 status = %v; want Scheduled", gotFuture.Status)
	}

	// Adaptive timer: next must point to the future item's ScheduledAt.
	if next == nil {
		t.Fatal("next should not be nil — future item exists")
	}
	if !next.Equal(future) {
		t.Errorf("next = %v; want %v", *next, future)
	}
}

// — G21: Full v1.0.0 stack (M1+M2+M3+M5+M7+M8) ——————————————————————————

// TestFull_G21_V1FullStack wires a single App with every cross-milestone
// feature that shipped in v1.0.0: Auth (M1), App routing (M2), SitemapConfig
// (M3), Feed + AIIndex (M5), Redirects (M7), and the scheduler (M8). It
// verifies that all endpoints respond correctly and that an overdue Scheduled
// item is promoted to Published by the scheduler and then served via the
// module list endpoint.
//
// Note on App.Content() ordering: per-module routes (/{prefix}/feed.xml,
// /{prefix}/sitemap.xml) require the module stores to be set before Register
// is called. App.Content currently calls Register first, so aggregate routes
// (/feed.xml, /sitemap.xml) are tested here instead. This is tracked as a
// known gap; the per-module feed route is exercised directly in G11/G12.
func TestFull_G21_V1FullStack(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()

	// One already-Published post.
	pub := &testPost{Node: Node{
		ID:     NewID(),
		Slug:   "hello-world",
		Status: Published,
	}}
	if err := repo.Save(context.Background(), pub); err != nil {
		t.Fatalf("seed published: %v", err)
	}

	// One overdue Scheduled post (ScheduledAt in the past).
	past := time.Now().UTC().Add(-2 * time.Minute)
	overduePost := &testPost{Node: Node{
		ID:          NewID(),
		Slug:        "scheduled-post",
		Status:      Scheduled,
		ScheduledAt: &past,
	}}
	if err := repo.Save(context.Background(), overduePost); err != nil {
		t.Fatalf("seed scheduled: %v", err)
	}

	m := NewModule((*testPost)(nil),
		Repo(repo),
		At("/posts"),
		Auth(Read(Guest), Write(Author)),
		SitemapConfig{},
		Feed(FeedConfig{Title: "G21 Blog"}),
		AIIndex(LLMsTxt),
		HeadFunc(func(_ Context, p *testPost) Head {
			return Head{Title: p.Slug, Description: p.Slug + " description"}
		}),
	)

	app := New(MustConfig(Config{
		BaseURL: "https://example.com",
		Secret:  []byte("16bytessecretkey"),
	}))
	app.Content(m, Redirects(From("/old-posts"), "/posts"))

	// M8: run the scheduler before building the handler so that regeneration
	// results are visible at route-registration time.
	bgCtx := NewBackgroundContext("example.com")
	newScheduler(app.schedulerModules, bgCtx).tick()

	// Populate the feed and AI stores so that App.Handler registers the
	// aggregate /feed.xml and /llms.txt routes.
	m.regenerateFeed(bgCtx)
	m.regenerateAI(bgCtx)

	h := app.Handler()

	// M2: GET /posts → 200 JSON, both posts present.
	r := httptest.NewRequest("GET", "/posts", nil)
	r.Header.Set("Accept", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /posts status = %d; want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "hello-world") {
		t.Errorf("GET /posts missing published post slug")
	}
	if !strings.Contains(body, "scheduled-post") {
		t.Errorf("GET /posts missing scheduler-promoted post slug (M8+M2 cross-check)")
	}

	// M3: GET /sitemap.xml → 200.
	r = httptest.NewRequest("GET", "/sitemap.xml", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("GET /sitemap.xml status = %d; want 200", w.Code)
	}

	// M5 (Feed): GET /feed.xml (aggregate) → 200 RSS 2.0.
	// Note: per-module /posts/feed.xml requires store injection before Register;
	// tested directly in G11/G12. Aggregate feed verified here.
	r = httptest.NewRequest("GET", "/feed.xml", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("GET /feed.xml status = %d; want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), `version="2.0"`) {
		t.Errorf("GET /feed.xml missing RSS version attribute")
	}

	// M5 (AIIndex): GET /llms.txt → 200, contains published slug.
	r = httptest.NewRequest("GET", "/llms.txt", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("GET /llms.txt status = %d; want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "hello-world") {
		t.Errorf("GET /llms.txt missing published post slug")
	}

	// M7 (Redirects): GET /.well-known/redirects.json → 200.
	r = httptest.NewRequest("GET", "/.well-known/redirects.json", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("GET /.well-known/redirects.json status = %d; want 200", w.Code)
	}

	// M7 (prefix redirect): GET /old-posts/hello-world → 301 → /posts/hello-world.
	r = httptest.NewRequest("GET", "/old-posts/hello-world", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusMovedPermanently {
		t.Errorf("GET /old-posts/hello-world status = %d; want 301", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/posts/hello-world" {
		t.Errorf("Location = %q; want /posts/hello-world", loc)
	}

	// M1 (Auth): POST /posts as Guest (no token) → 403 Forbidden.
	// 403 is correct: the request is authenticated as Guest (role level 10)
	// but Write requires Author (level 20). 401 would indicate unknown identity.
	r = httptest.NewRequest("POST", "/posts", nil)
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("POST /posts as Guest status = %d; want 403", w.Code)
	}
}

// — G22: forge-mcp core — MCPModule interface + lifecycle (M10) ——————————————

// testMCPPost is the canonical MCP test content type for the G22 group.
// Required fields with min constraints exercise MCPCreate validation and
// MCPSchema field derivation.
type testMCPPost struct {
	Node
	Title  string `forge:"required,min=3"`
	Body   string `forge:"required,min=10"`
	Rating int
	Tags   string `json:"tags"`
}

// findField looks up an MCPField by Go field name in a schema slice.
// Tests use this helper rather than positional indexing because field
// order is unspecified.
func findField(schema []MCPField, name string) (MCPField, bool) {
	for _, f := range schema {
		if f.Name == name {
			return f, true
		}
	}
	return MCPField{}, false
}

// TestFull_G22_MCPModuleInterface verifies that App.MCPModules() returns the
// registered modules and that MCPMeta() and MCPSchema() report correct
// metadata on the forge core side of Amendment A49.
func TestFull_G22_MCPModuleInterface(t *testing.T) {
	repo1 := NewMemoryRepo[*testMCPPost]()
	m1 := NewModule((*testMCPPost)(nil),
		Repo(repo1),
		At("/posts"),
		MCP(MCPRead),
	)
	repo2 := NewMemoryRepo[*testMCPPost]()
	m2 := NewModule((*testMCPPost)(nil),
		Repo(repo2),
		At("/articles"),
		MCP(MCPWrite),
	)

	app := New(MustConfig(Config{
		BaseURL: "https://example.com",
		Secret:  []byte("16bytessecretkey"),
	}))
	app.Content(m1)
	app.Content(m2)

	mods := app.MCPModules()
	if len(mods) != 2 {
		t.Fatalf("MCPModules() len = %d; want 2", len(mods))
	}

	// m1: MCPRead at /posts
	if mods[0].MCPMeta().Prefix != "/posts" {
		t.Errorf("mods[0].Prefix = %q; want /posts", mods[0].MCPMeta().Prefix)
	}
	if len(mods[0].MCPMeta().Operations) != 1 || mods[0].MCPMeta().Operations[0] != MCPRead {
		t.Errorf("mods[0].Operations = %v; want [MCPRead]", mods[0].MCPMeta().Operations)
	}

	// m2: MCPWrite at /articles
	if mods[1].MCPMeta().Prefix != "/articles" {
		t.Errorf("mods[1].Prefix = %q; want /articles", mods[1].MCPMeta().Prefix)
	}
	if len(mods[1].MCPMeta().Operations) != 1 || mods[1].MCPMeta().Operations[0] != MCPWrite {
		t.Errorf("mods[1].Operations = %v; want [MCPWrite]", mods[1].MCPMeta().Operations)
	}

	// Title must be a required field in the schema.
	schema := mods[0].MCPSchema()
	titleField, ok := findField(schema, "Title")
	if !ok {
		t.Fatal("MCPSchema() missing Title field")
	}
	if !titleField.Required {
		t.Errorf("Title.Required = false; want true")
	}
	if titleField.JSONName == "" {
		t.Error("Title.JSONName must not be empty")
	}
}

// TestFull_G22_MCPCreatePublishLifecycle exercises the create→publish lifecycle
// through the MCPModule interface: Draft status on create, Published after
// MCPPublish, AfterPublish signal fires, MCPList status filtering is correct.
func TestFull_G22_MCPCreatePublishLifecycle(t *testing.T) {
	var afterPublishCount int64

	repo := NewMemoryRepo[*testMCPPost]()
	m := NewModule((*testMCPPost)(nil),
		Repo(repo),
		At("/posts"),
		MCP(MCPRead, MCPWrite),
		On(AfterPublish, func(_ Context, _ *testMCPPost) error {
			atomic.AddInt64(&afterPublishCount, 1)
			return nil
		}),
	)

	app := New(MustConfig(Config{
		BaseURL: "https://example.com",
		Secret:  []byte("16bytessecretkey"),
	}))
	app.Content(m)

	mods := app.MCPModules()
	if len(mods) != 1 {
		t.Fatalf("MCPModules() len = %d; want 1", len(mods))
	}
	mod := mods[0]

	ctx := NewTestContext(User{ID: "u1", Roles: []Role{Author}})

	// MCPCreate with JSONName keys ("title"/"body", not "Title"/"Body").
	item, err := mod.MCPCreate(ctx, map[string]any{
		"title": "Hello MCP World",
		"body":  "This body is long enough to pass validation.",
	})
	if err != nil {
		t.Fatalf("MCPCreate: %v", err)
	}

	// Verify Draft status and non-empty slug via type assertion.
	got, ok := item.(*testMCPPost)
	if !ok {
		t.Fatalf("MCPCreate returned %T; want *testMCPPost", item)
	}
	if got.Status != Draft {
		t.Errorf("created item Status = %v; want Draft", got.Status)
	}
	if got.Slug == "" {
		t.Fatal("created item Slug must not be empty")
	}
	slug := got.Slug

	// MCPPublish: Draft → Published.
	if err := mod.MCPPublish(ctx, slug); err != nil {
		t.Fatalf("MCPPublish: %v", err)
	}

	// Wait for async AfterPublish signal (same pattern as G19/G20).
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) && atomic.LoadInt64(&afterPublishCount) < 1 {
		time.Sleep(5 * time.Millisecond)
	}
	if n := atomic.LoadInt64(&afterPublishCount); n != 1 {
		t.Errorf("AfterPublish fired %d times; want 1", n)
	}

	// Published list must contain the item.
	published, err := mod.MCPList(ctx, Published)
	if err != nil {
		t.Fatalf("MCPList(Published): %v", err)
	}
	if len(published) != 1 {
		t.Errorf("MCPList(Published) = %d items; want 1", len(published))
	}

	// Draft list must be empty.
	drafts, err := mod.MCPList(ctx, Draft)
	if err != nil {
		t.Fatalf("MCPList(Draft): %v", err)
	}
	if len(drafts) != 0 {
		t.Errorf("MCPList(Draft) = %d items; want 0", len(drafts))
	}
}
