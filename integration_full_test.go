package smeldr

// integration_full_test.go â€” cross-milestone integration suite (M1â€“M5).
//
// Each test exercises behaviour that requires at least two milestone components
// working together. No test in this file duplicates coverage from
// integration_test.go (which covers single-module M4 scenarios).
//
// Groups:
//   G1  â€” Multi-module routing (M2)
//   G2  â€” Role-based access via inline middleware (M1 + M2)
//   G3  â€” Signal fire-through across modules (M1 + M2)
//   G4  â€” Content negotiation: two modules, mixed template configuration (M2 + M4)
//   G5  â€” smeldr:head + schema helpers through real render (M3 + M4)
//   G6  â€” SEO wiring: robots.txt + sitemap registration (M2 + M3)
//   G7  â€” Error template fallback across two modules (M2 + M4)
//   G8  â€” TemplateData end-to-end (M3 + M4)
//   G9  â€” Social + SitemapConfig (M5 + M3)
//   G10 â€” AI indexing + content negotiation (M5 + M4)
//   G11 â€” RSS feed + AfterPublish signal (M5 + M1)
//   G12 â€” Full M5 stack (M5 + M3 + M4)
//   G13 â€” Cookie consent enforcement (M6, Decision 5)
//   G14 â€” Consent lifecycle wired through a handler (M6 + M2)
//   G15 â€” Cookie manifest + App integration (M6 + M2 + M1)
//   G16 â€” Redirect enforcement: 301/410/404 + chain collapse (M7, Decision 17)
//   G17 â€” Prefix redirect via Redirects(From) + exact-beats-prefix (M7 + M2)
//   G18 â€” Full M7 stack: SQLRepo interface check + redirect manifest + ManifestAuth (M7 + M6 + M1)
//   G19 â€” Scheduler end-to-end: processScheduled + AfterPublish signal (M8 + M1)
//   G20 â€” Scheduler wired through App.Content(): schedulerModules populated, tick publishes (M8 + M2 + M3)
//   G21 â€” Full v1.0.0 stack: scheduler + sitemap + feed + AI index + redirects (M1+M2+M3+M5+M7+M8)
//   G22 â€” forge-mcp MCPModule interface + lifecycle (M10)
//   G23 â€” CLI round-trip: GETâ†’PUT lifecycle and field preservation (Decision 28)
//   G34 â€” SingleInstance routing: GET /{prefix} serves item; MCPMeta.SingleInstance = true (T50)
//   G35 â€” Standalone routing: GET /{slug} dispatched by App; two standalone modules (T50)

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

// â€” Helpers â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”

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

// â€” G1: Multi-module routing (M2) â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”

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
		// /posts/about should 404 â€” About Page is in /pages, not /posts.
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
// registration order â€” first Use argument is the outermost wrapper.
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
		t.Fatalf("expected â‰¥2 middleware invocations, got %d: %v", len(got), got)
	}
	if got[0] != "A" || got[1] != "B" {
		t.Errorf("middleware order = %v; want [A B ...]", got)
	}
}

// â€” G2: Role-based access via inline middleware (M1 + M2) â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”

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

	// No user â†’ GuestUser â†’ denied.
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

	// Editor role â†’ allowed.
	r := httptest.NewRequest("GET", "/posts", nil)
	r = withUser(r, editorUser())
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Errorf("status = %d; want 200 (editor role)", w.Code)
	}
}

// â€” G3: Signal fire-through (M1 + M2) â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”

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

// â€” G4: Content negotiation, two modules (M2 + M4) â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”

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

// â€” G5: smeldr:head + schema through real render (M3 + M4) â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”

// TestFull_schemaForThroughTemplate verifies that smeldr_meta in a real
// template render produces a JSON-LD <script> block for Article content.
func TestFull_schemaForThroughTemplate(t *testing.T) {
	const tpl = `<!DOCTYPE html><html><head>{{smeldr_meta .Head .Content}}</head><body></body></html>`
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

// TestFull_smeldrMarkdownInTemplate verifies that smeldr_markdown converts
// Markdown syntax to HTML inside a rendered template.
func TestFull_smeldrMarkdownInTemplate(t *testing.T) {
	const tpl = `{{.Content.Body | smeldr_markdown}}`
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
// smeldr_meta to append a BreadcrumbList JSON-LD block after the primary schema.
func TestFull_breadcrumbs(t *testing.T) {
	const tpl = `{{smeldr_meta .Head .Content}}`
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
	// Two separate ld+json script blocks â€” Article + BreadcrumbList.
	count := strings.Count(body, `application/ld+json`)
	if count < 2 {
		t.Errorf("expected 2 ld+json script blocks, got %d:\n%s", count, body)
	}
}

// â€” G6: SEO wiring (M2 + M3) â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”

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

// â€” G7: Error template fallback across two modules (M2 + M4) â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”

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

// â€” G8: TemplateData end-to-end (M3 + M4) â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”

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
	// Config.BaseURL is "https://example.com" â†’ hostname is "example.com".
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

// â€” G9: Social + SitemapConfig (M5 + M3) â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”

// TestFull_social_ogTagsInHTML verifies that a module configured with
// Social(OpenGraph, TwitterCard) and SitemapConfig{} (M3) renders og:title and
// twitter:card meta tags inside smeldr:head when HeadFunc returns a non-empty Title.
func TestFull_social_ogTagsInHTML(t *testing.T) {
	const show = `<!DOCTYPE html><html><head>{{template "smeldr:head" .}}</head></html>`
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
// when Social and SitemapConfig options are active â€” lifecycle is enforced
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

// â€” G10: AI indexing + content negotiation (M5 + M4) â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”

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
// 404 for a Draft item â€” lifecycle enforcement on the AIDoc endpoint.
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

// â€” G11: RSS feed + AfterPublish signal (M5 + M1) â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”

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

// â€” G12: Full M5 stack (M5 + M3 + M4) â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”

// TestFull_fullM5_htmlHasOGTags verifies that a module with all M5 options
// (Social, AIIndex, Feed, SitemapConfig) plus M3/M4 (HeadFunc, Templates)
// still renders og:title and twitter:card correctly in smeldr:head.
func TestFull_fullM5_htmlHasOGTags(t *testing.T) {
	const show = `<!DOCTYPE html><html><head>{{template "smeldr:head" .}}</head></html>`
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

// â€” G13: Cookie consent enforcement (M6, Decision 5) â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”

// TestFull_consent_setNecessarySetsHeader verifies that SetCookie writes a
// Set-Cookie header for a Necessary cookie, and that ConsentFor(Necessary)
// is always true without any smeldr_consent cookie in the request.
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

	// ConsentFor(Necessary) must be true without any smeldr_consent cookie.
	r := httptest.NewRequest("GET", "/", nil)
	if !ConsentFor(r, Necessary) {
		t.Error("ConsentFor(Necessary) = false; want always true")
	}
}

// TestFull_consent_noConsentSkips verifies that SetCookieIfConsented returns
// false and writes no Set-Cookie header when smeldr_consent is absent.
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
		t.Error("ConsentFor(Preferences) = true without smeldr_consent; want false")
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
// expired Set-Cookie for smeldr_consent, and that ConsentFor returns false for
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
	r.AddCookie(&http.Cookie{Name: "smeldr_consent", Value: ""})
	if ConsentFor(r, Preferences) {
		t.Error("ConsentFor(Preferences) = true after RevokeConsent; want false")
	}
}

// â€” G14: Consent lifecycle wired through a handler (M6 + M2) â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”

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
// even when smeldr_consent contains garbage data.
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

	// ConsentFor(Necessary) is always true â€” even with corrupted smeldr_consent.
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{Name: "smeldr_consent", Value: "garbage-data,,,"})
	if !ConsentFor(r, Necessary) {
		t.Error("ConsentFor(Necessary) = false; want always true")
	}
}

// â€” G15: Cookie manifest + App integration (M6 + M2 + M1) â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”

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
// returns 404 when App.Cookies() has never been called â€” no leaking of empty manifests.
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

	// Unauthenticated request â€” 401.
	r1 := httptest.NewRequest("GET", "/.well-known/cookies.json", nil)
	w1 := httptest.NewRecorder()
	h.ServeHTTP(w1, r1)
	if w1.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated: status = %d; want 401", w1.Code)
	}

	// Authenticated Editor â€” 200.
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

// â€” G16: Redirect enforcement (M7, Decision 17) â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”

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
// a chain are collapsed: when Bâ†’C is registered before Aâ†’B, the forward
// collapse fires so A is stored directly as Aâ†’C (Decision 24).
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

// â€” G17: Prefix redirect via Redirects(From) (M7 + M2) â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”

// TestFull_prefix_redirect_rewritesPath verifies that a Redirects(From("/posts"), "/articles")
// option on app.Content() rewrites GET /posts/hello to 301 â†’ /articles/hello.
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

// â€” G18: Full M7 stack â€” SQLRepo + manifest + ManifestAuth (M7 + M6 + M1) â€”

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

	// Unauthenticated â€” 401.
	r1 := httptest.NewRequest("GET", "/.well-known/redirects.json", nil)
	w1 := httptest.NewRecorder()
	h.ServeHTTP(w1, r1)
	if w1.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated: status = %d; want 401", w1.Code)
	}

	// Authenticated Editor â€” 200.
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

// â€” G19: Scheduler end-to-end + AfterPublish signal (M8 + M1) â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”

// TestFull_scheduler_publishesOverdue verifies the end-to-end Scheduledâ†’Published
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
		t.Fatal("next should not be nil â€” pending item has a future ScheduledAt")
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

	// AfterPublish (M1 signal) must fire exactly once â€” give dispatchAfter time.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) && fired.Load() < 1 {
		time.Sleep(5 * time.Millisecond)
	}
	if got := fired.Load(); got != 1 {
		t.Errorf("AfterPublish fired %d times; want 1", got)
	}
}

// â€” G20: Scheduler wired via App.Content() (M8 + M2 + M3) â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”

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
		t.Fatal("next should not be nil â€” future item exists")
	}
	if !next.Equal(future) {
		t.Errorf("next = %v; want %v", *next, future)
	}
}

// â€” G21: Full v1.0.0 stack (M1+M2+M3+M5+M7+M8) â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”

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

	// M2: GET /posts â†’ 200 JSON, both posts present.
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

	// M3: GET /sitemap.xml â†’ 200.
	r = httptest.NewRequest("GET", "/sitemap.xml", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("GET /sitemap.xml status = %d; want 200", w.Code)
	}

	// M5 (Feed): GET /feed.xml (aggregate) â†’ 200 RSS 2.0.
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

	// M5 (AIIndex): GET /llms.txt â†’ 200, contains published slug.
	r = httptest.NewRequest("GET", "/llms.txt", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("GET /llms.txt status = %d; want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "hello-world") {
		t.Errorf("GET /llms.txt missing published post slug")
	}

	// M7 (Redirects): GET /.well-known/redirects.json â†’ 200.
	r = httptest.NewRequest("GET", "/.well-known/redirects.json", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("GET /.well-known/redirects.json status = %d; want 200", w.Code)
	}

	// M7 (prefix redirect): GET /old-posts/hello-world â†’ 301 â†’ /posts/hello-world.
	r = httptest.NewRequest("GET", "/old-posts/hello-world", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusMovedPermanently {
		t.Errorf("GET /old-posts/hello-world status = %d; want 301", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/posts/hello-world" {
		t.Errorf("Location = %q; want /posts/hello-world", loc)
	}

	// M1 (Auth): POST /posts as Guest (no token) â†’ 403 Forbidden.
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

// â€” G22: forge-mcp core â€” MCPModule interface + lifecycle (M10) â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”

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

// cliRoundTripPost is the content type used by the G23 CLI round-trip group.
// It carries a []string Tags field to verify that array values survive a
// GETâ†’PUT round-trip without conversion errors.
type cliRoundTripPost struct {
	Node
	Title string `forge:"required"`
	Body  string
	Tags  []string
}

func (p *cliRoundTripPost) Head() Head { return Head{Title: p.Title} }

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

// TestFull_G22_MCPCreatePublishLifecycle exercises the createâ†’publish lifecycle
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

	// MCPPublish: Draft â†’ Published.
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

// â€” G23: CLI round-trip â€” GETâ†’PUT lifecycle and field preservation (Decision 28) â€”

// TestFull_G23_CLIRoundTrip verifies that the HTTP API correctly handles
// the GETâ†’PUT round-trip pattern used by forge-cli for lifecycle operations.
// Specifically it checks:
//   - PublishedAt is set server-side on publish (not taken from the body)
//   - PublishedAt is preserved on a subsequent update (no re-publish)
//   - []string Tags survive the JSON round-trip without loss or type error
//   - Status transitions (publish, archive) work correctly via PUT
func TestFull_G23_CLIRoundTrip(t *testing.T) {
	repo := NewMemoryRepo[*cliRoundTripPost]()
	m := NewModule((*cliRoundTripPost)(nil),
		Repo(repo),
		At("/posts"),
		Auth(Read(Author), Write(Author), Delete(Editor)),
	)
	app := New(MustConfig(Config{
		BaseURL: "https://example.com",
		Secret:  []byte("16bytessecretkey"),
	}))
	app.Content(m)
	h := app.Handler()

	author := User{ID: "a1", Roles: []Role{Author}}
	editor := User{ID: "e1", Roles: []Role{Editor}}

	do := func(method, path string, body []byte, user User) *httptest.ResponseRecorder {
		t.Helper()
		var r *http.Request
		if body != nil {
			r = httptest.NewRequest(method, path, bytes.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
		} else {
			r = httptest.NewRequest(method, path, nil)
		}
		r.Header.Set("Accept", "application/json")
		r = withUser(r, user)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}

	decode := func(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
		t.Helper()
		var m map[string]any
		if err := json.NewDecoder(w.Body).Decode(&m); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		return m
	}

	// Step 1: Create a draft with Tags â€” POST /posts.
	createBody := `{"Title":"Round-trip Post","Body":"Hello world","Status":"draft","Tags":["go","forge"]}`
	w1 := do("POST", "/posts", []byte(createBody), author)
	if w1.Code != http.StatusCreated {
		t.Fatalf("create: status = %d; want 201; body: %s", w1.Code, w1.Body.String())
	}
	created := decode(t, w1)
	slug, _ := created["Slug"].(string)
	if slug == "" {
		t.Fatal("create: Slug not returned")
	}
	if got, _ := created["Status"].(string); got != "draft" {
		t.Errorf("create: Status = %q; want %q", got, "draft")
	}

	// Step 2: GET the item â€” verify Tags in initial draft.
	w2 := do("GET", "/posts/"+slug, nil, author)
	if w2.Code != http.StatusOK {
		t.Fatalf("GET draft: status = %d; want 200", w2.Code)
	}
	getItem := decode(t, w2)
	tags, _ := getItem["Tags"].([]any)
	if len(tags) != 2 {
		t.Errorf("GET draft: Tags len = %d; want 2", len(tags))
	}

	// Step 3: Simulate CLI `publish` â€” set Status=published and PUT back.
	// PublishedAt in the body is zero (from the GET response); the server
	// must set it to now regardless.
	getItem["Status"] = "published"
	pubBody, _ := json.Marshal(getItem)
	w3 := do("PUT", "/posts/"+slug, pubBody, author)
	if w3.Code != http.StatusOK {
		t.Fatalf("publish PUT: status = %d; want 200; body: %s", w3.Code, w3.Body.String())
	}
	published := decode(t, w3)
	if got, _ := published["Status"].(string); got != "published" {
		t.Errorf("after publish: Status = %q; want %q", got, "published")
	}
	pa, _ := published["PublishedAt"].(string)
	if pa == "" || pa == "0001-01-01T00:00:00Z" {
		t.Errorf("after publish: PublishedAt = %q; want non-zero server-set timestamp", pa)
	}

	// Step 4: Simulate CLI `update` â€” GET the published item, change Title only,
	// PUT back. Verify PublishedAt and Tags are preserved unchanged.
	w4 := do("GET", "/posts/"+slug, nil, author)
	if w4.Code != http.StatusOK {
		t.Fatalf("GET published: status = %d; want 200", w4.Code)
	}
	publishedItem := decode(t, w4)
	savedPublishedAt, _ := publishedItem["PublishedAt"].(string)
	publishedItem["Title"] = "Updated Title"
	updateBody, _ := json.Marshal(publishedItem)
	w5 := do("PUT", "/posts/"+slug, updateBody, author)
	if w5.Code != http.StatusOK {
		t.Fatalf("update PUT: status = %d; want 200; body: %s", w5.Code, w5.Body.String())
	}
	updated := decode(t, w5)
	if got, _ := updated["Title"].(string); got != "Updated Title" {
		t.Errorf("after update: Title = %q; want %q", got, "Updated Title")
	}
	if got, _ := updated["Status"].(string); got != "published" {
		t.Errorf("after update: Status = %q; want published (must not regress)", got)
	}
	if got, _ := updated["PublishedAt"].(string); got != savedPublishedAt {
		t.Errorf("after update: PublishedAt = %q; want %q (must not re-publish)", got, savedPublishedAt)
	}
	updatedTags, _ := updated["Tags"].([]any)
	if len(updatedTags) != 2 {
		t.Errorf("after update: Tags len = %d; want 2 (array must survive round-trip)", len(updatedTags))
	}

	// Step 5: Simulate CLI `archive` â€” GET then PUT with Status=archived.
	w6 := do("GET", "/posts/"+slug, nil, author)
	archiveItem := decode(t, w6)
	archiveItem["Status"] = "archived"
	archBody, _ := json.Marshal(archiveItem)
	w7 := do("PUT", "/posts/"+slug, archBody, author)
	if w7.Code != http.StatusOK {
		t.Fatalf("archive PUT: status = %d; want 200", w7.Code)
	}
	archived := decode(t, w7)
	if got, _ := archived["Status"].(string); got != "archived" {
		t.Errorf("after archive: Status = %q; want %q", got, "archived")
	}

	// Step 6: CLI `delete` â€” DELETE /posts/{slug} requires Editor role.
	w8 := do("DELETE", "/posts/"+slug, nil, editor)
	if w8.Code != http.StatusNoContent {
		t.Fatalf("delete: status = %d; want 204", w8.Code)
	}
	// Confirm it's gone.
	w9 := do("GET", "/posts/"+slug, nil, author)
	if w9.Code != http.StatusNotFound {
		t.Errorf("after delete: GET status = %d; want 404", w9.Code)
	}
}

// â€” G24: SSRF validation (M11) â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”

// TestFull_G24_SSRFValidation verifies that validateWebhookURL rejects HTTP,
// localhost, .local hostnames, and private IPs, while accepting a well-formed
// HTTPS URL with a routable public IP.
func TestFull_G24_SSRFValidation(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
		errSnip string
	}{
		{"http rejected", "http://8.8.8.8/hook", true, "HTTPS"},
		{"localhost rejected", "https://localhost/hook", true, "localhost"},
		{"dot-local rejected", "https://myserver.local/hook", true, ".local"},
		{"private 10.x rejected", "https://10.0.0.1/hook", true, "private"},
		{"private 192.168.x rejected", "https://192.168.100.1/hook", true, "private"},
		{"private 172.16.x rejected", "https://172.16.0.1/hook", true, "private"},
		{"valid public IP accepted", "https://8.8.8.8/hook", false, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateWebhookURL(tc.url)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error containing %q, got nil", tc.errSnip)
				}
				if tc.errSnip != "" && !strings.Contains(err.Error(), tc.errSnip) {
					t.Fatalf("error %q does not contain %q", err.Error(), tc.errSnip)
				}
			} else if err != nil {
				t.Fatalf("want no error, got: %v", err)
			}
		})
	}
}

// â€” G25: WebhookStore encrypt/decrypt roundtrip (M11) â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”

// TestFull_G25_WebhookStoreRoundtrip verifies that:
//   - Create inserts an endpoint and returns a plaintext secret
//   - List returns the endpoint without exposing the raw secret
//   - DecryptSecret recovers the original plaintext secret
//   - Delete removes the endpoint
func TestFull_G25_WebhookStoreRoundtrip(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()
	_, err := db.ExecContext(ctx, `
		CREATE TABLE smeldr_webhook_endpoints (
			id TEXT PRIMARY KEY, events TEXT NOT NULL, target_url TEXT NOT NULL,
			secret_enc TEXT NOT NULL, active BOOLEAN NOT NULL DEFAULT 1,
			created_at DATETIME NOT NULL
		)`)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}
	appSecret := []byte("32-bytes-app-secret-for-testing!")
	store := NewWebhookStore(db, appSecret)

	// Insert an endpoint directly (bypasses URL SSRF validation).
	ep := WebhookEndpoint{
		ID:        "ep-g25",
		Events:    []string{"post.published"},
		TargetURL: "https://example.com/deliver",
	}
	evJSON, _ := json.Marshal(ep.Events)
	enc, err := store.encryptSecret([]byte("my-signing-secret"))
	if err != nil {
		t.Fatalf("encryptSecret: %v", err)
	}
	_, err = db.ExecContext(ctx,
		`INSERT INTO smeldr_webhook_endpoints (id, events, target_url, secret_enc, active, created_at)
		 VALUES ($1,$2,$3,$4,1,datetime('now'))`,
		ep.ID, string(evJSON), ep.TargetURL, enc,
	)
	if err != nil {
		t.Fatalf("insert endpoint: %v", err)
	}

	// List must return the endpoint without leaking the raw secret.
	list, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("List: want 1, got %d", len(list))
	}
	if list[0].secretEnc != "" {
		t.Error("List must not populate secretEnc â€” secret must not be exposed")
	}

	// DecryptSecret must recover the plaintext secret via EndpointsForEvent
	// (which populates secretEnc for the pool to use).
	eps, err := store.EndpointsForEvent(ctx, "post.published")
	if err != nil {
		t.Fatalf("EndpointsForEvent: %v", err)
	}
	if len(eps) != 1 {
		t.Fatalf("EndpointsForEvent: want 1, got %d", len(eps))
	}
	decrypted, err := store.DecryptSecret(eps[0])
	if err != nil {
		t.Fatalf("DecryptSecret: %v", err)
	}
	if string(decrypted) != "my-signing-secret" {
		t.Errorf("DecryptSecret: got %q, want %q", decrypted, "my-signing-secret")
	}

	// Delete must remove the endpoint.
	if err := store.Delete(ctx, ep.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	list2, _ := store.List(ctx)
	if len(list2) != 0 {
		t.Errorf("after Delete: want 0 endpoints, got %d", len(list2))
	}
}

// â€” G26: Signal â†’ enqueue (M11 + M10 + M1) â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”

// g26Post is the content type used by the G26 App-wiring integration group.
// TypeName "g26Post" â†’ event suffix = "g26post.published".
type g26Post struct {
	Node
	Title string `forge:"required,min=3"`
	Body  string `forge:"required"`
}

// TestFull_G26_SignalEnqueue verifies that publishing a post via the MCP
// interface causes injectWebhookHooks to enqueue a delivery job for any
// active endpoint subscribed to the relevant event. This exercises the full
// App â†’ module.go afterHook â†’ forge.go injectWebhookHooks â†’ outbound.go
// Enqueue pipeline (M11 + M1 cross-milestone).
func TestFull_G26_SignalEnqueue(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()
	createG26Tables(t, db)

	appSecret := []byte("32-bytes-g26-secret-for-testing!")
	store := NewWebhookStore(db, appSecret)
	app := New(MustConfig(Config{
		BaseURL: "https://example.com",
		Secret:  appSecret,
		DB:      db,
	}))
	app.Webhooks(store)

	repo := NewMemoryRepo[*g26Post]()
	m := NewModule((*g26Post)(nil),
		Repo(repo),
		At("/g26posts"),
		MCP(MCPRead, MCPWrite),
	)
	app.Content(m)

	// Insert a webhook endpoint that subscribes to "g26post.published".
	enc, _ := store.encryptSecret([]byte("g26-secret"))
	evJSON, _ := json.Marshal([]string{"g26post.published"})
	_, err := db.ExecContext(ctx,
		`INSERT INTO smeldr_webhook_endpoints (id, events, target_url, secret_enc, active, created_at)
		 VALUES ('ep-g26',$1,'https://8.8.8.8/hook',$2,1,datetime('now'))`,
		string(evJSON), enc,
	)
	if err != nil {
		t.Fatalf("insert endpoint: %v", err)
	}

	// Wire signal bus into the module (simulates what Run() does).
	app.wireSignalBus()

	mod := app.MCPModules()[0]
	userCtx := NewTestContext(User{ID: "u1", Roles: []Role{Author}})

	// Create and publish a post.
	item, err := mod.MCPCreate(userCtx, map[string]any{
		"title": "G26 Webhook",
		"body":  "This triggers AfterPublish signal.",
	})
	if err != nil {
		t.Fatalf("MCPCreate: %v", err)
	}
	post := item.(*g26Post)
	if err := mod.MCPPublish(userCtx, post.Slug); err != nil {
		t.Fatalf("MCPPublish: %v", err)
	}

	// Wait for the async afterHook to fire and enqueue the job.
	deadline := time.Now().Add(500 * time.Millisecond)
	var jobs []OutboundJob
	for time.Now().Before(deadline) {
		jobs, _ = app.WebhookPool().ListJobsForEndpoint(userCtx, "ep-g26")
		if len(jobs) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(jobs) == 0 {
		t.Fatal("no webhook job enqueued after MCPPublish; expected 1 job for ep-g26")
	}
	if jobs[0].Event != "g26post.published" {
		t.Errorf("job.Event = %q; want %q", jobs[0].Event, "g26post.published")
	}
}

// createG26Tables sets up all three tables needed for the G26â€“G29 webhook
// integration groups in the given DB.
func createG26Tables(t *testing.T, db DB) {
	t.Helper()
	ctx := context.Background()
	for _, ddl := range []string{
		`CREATE TABLE IF NOT EXISTS smeldr_webhook_endpoints (
			id TEXT PRIMARY KEY, events TEXT NOT NULL, target_url TEXT NOT NULL,
			secret_enc TEXT NOT NULL, active BOOLEAN NOT NULL DEFAULT 1,
			created_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS smeldr_outbound_jobs (
			id TEXT PRIMARY KEY, endpoint_id TEXT NOT NULL, target_url TEXT NOT NULL,
			secret_enc TEXT NOT NULL, payload BLOB NOT NULL, event TEXT NOT NULL,
			attempts INTEGER NOT NULL DEFAULT 0, next_retry_at DATETIME NOT NULL,
			created_at DATETIME NOT NULL, expires_at DATETIME NOT NULL,
			status TEXT NOT NULL DEFAULT 'pending'
		)`,
		`CREATE TABLE IF NOT EXISTS smeldr_delivery_logs (
			id TEXT PRIMARY KEY, job_id TEXT NOT NULL, attempted_at DATETIME NOT NULL,
			status_code INTEGER NOT NULL DEFAULT 0, duration_ms INTEGER NOT NULL DEFAULT 0,
			error TEXT NOT NULL DEFAULT ''
		)`,
	} {
		if _, err := db.ExecContext(ctx, ddl); err != nil {
			t.Fatalf("createG26Tables: %v", err)
		}
	}
}

// â€” G27: Pool retry on transient failure (M11) â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”

// TestFull_G27_RetryOnTransientFailure verifies that a job that fails on
// the first attempt is retried and eventually delivered. Two delivery log
// entries are expected: one failure, one success.
func TestFull_G27_RetryOnTransientFailure(t *testing.T) {
	pool, store := outboundTestDB(t)

	clock := newFakeClock(time.Now())
	pool.clock = clock

	var deliverCount int32
	pool.deliver = func(ctx context.Context, job OutboundJob, _ []byte) error {
		n := atomic.AddInt32(&deliverCount, 1)
		if n == 1 {
			return &webhookHTTPError{statusCode: 500}
		}
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)

	enc, _ := store.encryptSecret([]byte("g27-secret"))
	now := time.Now()
	job := OutboundJob{
		ID:          NewID(),
		EndpointID:  "ep-g27",
		TargetURL:   "https://8.8.8.8/hook",
		SecretEnc:   enc,
		Payload:     []byte(`{"event":"post.published"}`),
		Event:       "post.published",
		NextRetryAt: now,
		CreatedAt:   now,
		ExpiresAt:   now.Add(24 * time.Hour),
		Status:      "pending",
	}
	if err := pool.Enqueue(ctx, job); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	// Wait for the first delivery attempt to be recorded before advancing the
	// clock. On slow CI runners, 1s is not enough â€” use 5s to match the outer
	// retry poll and avoid advancing the clock before the first attempt lands.
	pollDeadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(pollDeadline) {
		logs, _ := pool.ListDeliveryLogs(ctx, job.ID)
		if len(logs) >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	clock.Advance(5 * time.Minute)
	time.Sleep(10 * time.Millisecond) // yield so workers can pick up the now-due job

	deadline := time.Now().Add(10 * time.Second)
	var logs []DeliveryLog
	for time.Now().Before(deadline) {
		var err error
		logs, err = pool.ListDeliveryLogs(ctx, job.ID)
		if err == nil && len(logs) >= 2 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if len(logs) < 2 {
		t.Fatalf("want â‰¥2 delivery logs (1 failure + 1 success), got %d", len(logs))
	}

	cancel()
	pool.Stop()
}

// â€” G28: Dead-letter after max attempts (M11) â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”

// TestFull_G28_DeadLetterAfterMaxAttempts verifies that a job that always
// fails transitions to "dead" status after reaching the maximum attempt count.
func TestFull_G28_DeadLetterAfterMaxAttempts(t *testing.T) {
	pool, store := outboundTestDB(t)

	clock := newFakeClock(time.Now())
	pool.clock = clock
	pool.deliver = func(_ context.Context, _ OutboundJob, _ []byte) error {
		return &webhookHTTPError{statusCode: 500}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)

	enc, _ := store.encryptSecret([]byte("g28-secret"))
	now := time.Now()
	job := OutboundJob{
		ID:          NewID(),
		EndpointID:  "ep-g28",
		TargetURL:   "https://8.8.8.8/hook",
		SecretEnc:   enc,
		Payload:     []byte(`{"event":"post.updated"}`),
		Event:       "post.updated",
		NextRetryAt: now,
		CreatedAt:   now,
		ExpiresAt:   now.Add(24 * time.Hour),
		Status:      "pending",
	}
	if err := pool.Enqueue(ctx, job); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	// Advance the clock repeatedly to push the job through all retry delays.
	deadline := time.Now().Add(5 * time.Second)
	var finalStatus string
	for time.Now().Before(deadline) {
		clock.Advance(2 * time.Hour) // skip past exponential backoff
		time.Sleep(50 * time.Millisecond)
		rows, _ := pool.db.QueryContext(ctx,
			`SELECT status FROM smeldr_outbound_jobs WHERE id = $1`, job.ID)
		func() {
			defer rows.Close()
			if rows.Next() {
				_ = rows.Scan(&finalStatus)
			}
		}()
		if finalStatus == "dead" {
			break
		}
	}

	cancel()
	pool.Stop()

	if finalStatus != "dead" {
		t.Errorf("job status = %q; want %q after max attempts", finalStatus, "dead")
	}
}

// â€” G29: Circuit breaker opens after consecutive failures (M11) â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”

// TestFull_G29_CircuitBreakerOpens verifies that after circuitThreshold
// consecutive delivery failures for a single endpoint, the circuit breaker
// opens and subsequent jobs for the same endpoint are skipped (logged as
// "circuit open") rather than attempting delivery.
func TestFull_G29_CircuitBreakerOpens(t *testing.T) {
	pool, store := outboundTestDB(t)

	clock := newFakeClock(time.Now())
	pool.clock = clock

	var deliverCalls int32
	pool.deliver = func(_ context.Context, _ OutboundJob, _ []byte) error {
		atomic.AddInt32(&deliverCalls, 1)
		return &webhookHTTPError{statusCode: 503}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)

	enc, _ := store.encryptSecret([]byte("g29-secret"))
	now := time.Now()

	// Enqueue 6 jobs â€” 5 to trip the circuit, 1 to verify it's now open.
	const total = 6
	ids := make([]string, total)
	for i := 0; i < total; i++ {
		id := NewID()
		ids[i] = id
		job := OutboundJob{
			ID:          id,
			EndpointID:  "ep-g29-cb",
			TargetURL:   "https://8.8.8.8/hook",
			SecretEnc:   enc,
			Payload:     []byte(`{"event":"post.deleted"}`),
			Event:       "post.deleted",
			NextRetryAt: now,
			CreatedAt:   now,
			ExpiresAt:   now.Add(24 * time.Hour),
			Status:      "pending",
		}
		if err := pool.Enqueue(ctx, job); err != nil {
			t.Fatalf("Enqueue job %d: %v", i, err)
		}
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		calls := atomic.LoadInt32(&deliverCalls)
		if calls >= circuitOpenThreshold {
			break
		}
		clock.Advance(10 * time.Minute)
		time.Sleep(50 * time.Millisecond)
	}

	// Wait for the 6th job to be processed (circuit should be open now).
	time.Sleep(200 * time.Millisecond)
	cancel()
	pool.Stop()

	// The 6th job should have been skipped (0 HTTP calls for it) â€” total
	// deliver calls must equal exactly circuitOpenThreshold (5), not 6.
	calls := atomic.LoadInt32(&deliverCalls)
	if calls > circuitOpenThreshold {
		t.Errorf("deliver called %d times; circuit should have opened after %d failures",
			calls, circuitOpenThreshold)
	}
}

// â€” G30: MCPSchedule â†’ AfterSchedule webhook (M11 + M10 + M8) â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”

// g30Post is the content type for the G30 cross-milestone group.
// TypeName "g30Post" â†’ event name "g30post.scheduled".
type g30Post struct {
	Node
	Title string `forge:"required,min=3"`
	Body  string `forge:"required"`
}

// TestFull_G30_MCPScheduleWebhook verifies the cross-milestone path:
// MCPSchedule (M10 MCP interface) â†’ AfterSchedule signal (A87/M11 signals.go)
// â†’ injectWebhookHooks (M11 forge.go) â†’ Enqueue. This proves that scheduling
// a post via MCP triggers the correct webhook event "g30post.scheduled".
func TestFull_G30_MCPScheduleWebhook(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()
	createG26Tables(t, db)

	appSecret := []byte("32-bytes-g30-secret-for-testing!")
	store := NewWebhookStore(db, appSecret)
	app := New(MustConfig(Config{
		BaseURL: "https://example.com",
		Secret:  appSecret,
		DB:      db,
	}))
	app.Webhooks(store)

	repo := NewMemoryRepo[*g30Post]()
	m := NewModule((*g30Post)(nil),
		Repo(repo),
		At("/g30posts"),
		MCP(MCPRead, MCPWrite),
	)
	app.Content(m)

	// Subscribe endpoint to the "scheduled" event for this type.
	enc, _ := store.encryptSecret([]byte("g30-secret"))
	evJSON, _ := json.Marshal([]string{"g30post.scheduled"})
	_, err := db.ExecContext(ctx,
		`INSERT INTO smeldr_webhook_endpoints (id, events, target_url, secret_enc, active, created_at)
		 VALUES ('ep-g30',$1,'https://8.8.8.8/hook',$2,1,datetime('now'))`,
		string(evJSON), enc,
	)
	if err != nil {
		t.Fatalf("insert endpoint: %v", err)
	}

	// Wire signal bus (equivalent of what Run() does).
	app.wireSignalBus()

	mod := app.MCPModules()[0]
	userCtx := NewTestContext(User{ID: "u1", Roles: []Role{Author}})

	// Create a draft first.
	item, err := mod.MCPCreate(userCtx, map[string]any{
		"title": "G30 Scheduled Post",
		"body":  "Scheduled via MCP to test AfterSchedule webhook.",
	})
	if err != nil {
		t.Fatalf("MCPCreate: %v", err)
	}
	post := item.(*g30Post)

	// Schedule it for 1 hour from now.
	if err := mod.MCPSchedule(userCtx, post.Slug, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("MCPSchedule: %v", err)
	}

	// Wait for the async afterHook to fire and enqueue the job.
	deadline := time.Now().Add(500 * time.Millisecond)
	var jobs []OutboundJob
	for time.Now().Before(deadline) {
		jobs, _ = app.WebhookPool().ListJobsForEndpoint(userCtx, "ep-g30")
		if len(jobs) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if len(jobs) == 0 {
		t.Fatal("no webhook job enqueued after MCPSchedule; expected job for ep-g30")
	}
	if jobs[0].Event != "g30post.scheduled" {
		t.Errorf("job.Event = %q; want %q", jobs[0].Event, "g30post.scheduled")
	}
}

// â€” G31: Draft preview token (M12) â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”

// g31Post is the content type for the G31 preview token cross-milestone group.
type g31Post struct {
	Node
	Title string `json:"title"`
}

func (p *g31Post) Head() Head { return Head{Title: p.Title} }

// TestFull_G31_PreviewToken verifies the end-to-end preview token path:
//   - GeneratePreviewToken(prefix, slug) produces a token accepted by the module
//   - A Draft item is served (200) when a valid token is present
//   - An expired token is rejected (404)
//   - A token for slug A is rejected on slug B (404)
//   - A token for prefix A is rejected on a module with prefix B (404)
//   - Published content remains accessible without a token (regression)
func TestFull_G31_PreviewToken(t *testing.T) {
	const prefix = "/g31posts"
	secret := []byte("g31-secret-32-bytes-xxxxxxxxxxx!")

	app := New(MustConfig(Config{
		BaseURL: "http://localhost",
		Secret:  secret,
	}))
	repo := NewMemoryRepo[*g31Post]()
	m := NewModule((*g31Post)(nil), At(prefix), Repo(repo))
	app.Content(m)
	handler := app.Handler()

	// Seed a Draft and a Published item.
	draft := &g31Post{Node: Node{ID: NewID(), Slug: GenerateSlug("G31 Draft"), Status: Draft}, Title: "G31 Draft"}
	if err := repo.Save(context.Background(), draft); err != nil {
		t.Fatalf("seed draft: %v", err)
	}
	pub := &g31Post{Node: Node{ID: NewID(), Slug: GenerateSlug("G31 Published"), Status: Published}, Title: "G31 Published"}
	if err := repo.Save(context.Background(), pub); err != nil {
		t.Fatalf("seed published: %v", err)
	}

	doGet := func(path string) int {
		r := httptest.NewRequest("GET", path, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w.Code
	}

	t.Run("valid token, draft â†’ 200", func(t *testing.T) {
		token := app.GeneratePreviewToken(prefix, draft.Slug)
		url := prefix + "/" + draft.Slug + "?preview=" + token
		if code := doGet(url); code != http.StatusOK {
			t.Errorf("got %d, want 200", code)
		}
	})

	t.Run("expired token (negative TTL), draft â†’ 404", func(t *testing.T) {
		// Use a negative TTL so the expiry is in the past.
		token := encodePreviewToken(prefix, draft.Slug, secret, -time.Second)
		url := prefix + "/" + draft.Slug + "?preview=" + token
		if code := doGet(url); code != http.StatusNotFound {
			t.Errorf("got %d, want 404", code)
		}
	})

	// Seed a second draft to test slug mismatch (published items are always
	// visible so cannot be used to verify rejection).
	draft2 := &g31Post{Node: Node{ID: NewID(), Slug: GenerateSlug("G31 Draft Two"), Status: Draft}, Title: "G31 Draft Two"}
	if err := repo.Save(context.Background(), draft2); err != nil {
		t.Fatalf("seed draft2: %v", err)
	}

	t.Run("token for slug A used on slug B (both drafts) â†’ 404", func(t *testing.T) {
		token := app.GeneratePreviewToken(prefix, draft.Slug)
		url := prefix + "/" + draft2.Slug + "?preview=" + token
		if code := doGet(url); code != http.StatusNotFound {
			t.Errorf("got %d, want 404", code)
		}
	})

	t.Run("token for prefix /other used on /g31posts â†’ 404", func(t *testing.T) {
		token := encodePreviewToken("/other", draft.Slug, secret, time.Hour)
		url := prefix + "/" + draft.Slug + "?preview=" + token
		if code := doGet(url); code != http.StatusNotFound {
			t.Errorf("got %d, want 404", code)
		}
	})

	t.Run("valid token, archived item â†’ 404 (archived not previewable)", func(t *testing.T) {
		archived := &g31Post{Node: Node{ID: NewID(), Slug: GenerateSlug("G31 Archived"), Status: Archived}, Title: "G31 Archived"}
		if err := repo.Save(context.Background(), archived); err != nil {
			t.Fatalf("seed archived: %v", err)
		}
		token := app.GeneratePreviewToken(prefix, archived.Slug)
		url := prefix + "/" + archived.Slug + "?preview=" + token
		if code := doGet(url); code != http.StatusNotFound {
			t.Errorf("got %d, want 404", code)
		}
	})

	t.Run("published content without token â†’ 200 (regression)", func(t *testing.T) {
		if code := doGet(prefix + "/" + pub.Slug); code != http.StatusOK {
			t.Errorf("got %d, want 200", code)
		}
	})
}

// â€” G32: Signal bus â€” OnSignal + dispatchBus cross-milestone (M14 + M10 + M11) â€”â€”â€”â€”â€”

// g32Post is the content type for G32 signal bus cross-milestone tests.
type g32Post struct {
	Node
	Title string `forge:"required,min=3"`
	Body  string `forge:"required"`
}

// TestFull_G32_OnSignalCalledOnMCPCreate verifies the cross-milestone path:
// App.OnSignal (M14) + MCPCreate (M10) â†’ AfterCreate bus handler fires with
// a correctly populated SignalEvent.
func TestFull_G32_OnSignalCalledOnMCPCreate(t *testing.T) {
	app := New(MustConfig(Config{
		BaseURL: "https://example.com",
		Secret:  []byte("test-g32-signal-bus-secret!"),
	}))
	repo := NewMemoryRepo[*g32Post]()
	m := NewModule((*g32Post)(nil),
		At("/g32posts"),
		Repo(repo),
		MCP(MCPRead, MCPWrite),
	)
	app.Content(m)

	done := make(chan SignalEvent, 1)
	app.OnSignal(AfterCreate, func(ctx context.Context, ev SignalEvent) error {
		done <- ev
		return nil
	})
	app.wireSignalBus()

	userCtx := NewTestContext(User{ID: "u1", Roles: []Role{Author}})
	_, err := app.MCPModules()[0].MCPCreate(userCtx, map[string]any{
		"title": "G32 Bus Post",
		"body":  "Cross-milestone bus integration.",
	})
	if err != nil {
		t.Fatalf("MCPCreate: %v", err)
	}

	select {
	case ev := <-done:
		if ev.Slug == "" {
			t.Error("SignalEvent.Slug is empty")
		}
		if ev.URL == "" {
			t.Error("SignalEvent.URL is empty")
		}
		if !strings.Contains(ev.URL, "/g32posts/") {
			t.Errorf("SignalEvent.URL = %q; want prefix /g32posts/", ev.URL)
		}
		if ev.ActorID != "u1" {
			t.Errorf("SignalEvent.ActorID = %q; want u1", ev.ActorID)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("OnSignal handler not called within 500 ms")
	}
}

// TestFull_G32_OnSignalAndWebhookCoexist verifies that App.OnSignal custom
// handlers and App.Webhooks webhook delivery coexist in the same signal bus â€”
// both receive the event after MCPPublish (M14 + M11 cross-milestone).
func TestFull_G32_OnSignalAndWebhookCoexist(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()
	createG26Tables(t, db)

	appSecret := []byte("32-bytes-g32-coexist-secret-key!")
	store := NewWebhookStore(db, appSecret)
	app := New(MustConfig(Config{
		BaseURL: "https://example.com",
		Secret:  appSecret,
		DB:      db,
	}))
	app.Webhooks(store)

	repo := NewMemoryRepo[*g32Post]()
	m := NewModule((*g32Post)(nil),
		At("/g32posts"),
		Repo(repo),
		MCP(MCPRead, MCPWrite),
	)
	app.Content(m)

	// Subscribe a webhook endpoint to publish events.
	enc, _ := store.encryptSecret([]byte("g32-coexist-secret"))
	evJSON, _ := json.Marshal([]string{"g32post.published"})
	if _, err := db.ExecContext(ctx,
		`INSERT INTO smeldr_webhook_endpoints (id, events, target_url, secret_enc, active, created_at)
		 VALUES ('ep-g32','`+string(evJSON)+`','https://8.8.8.8/hook','`+enc+`',1,datetime('now'))`,
	); err != nil {
		t.Fatalf("insert endpoint: %v", err)
	}

	// Register a custom bus handler alongside the webhook handler.
	busHandlerFired := make(chan struct{}, 1)
	app.OnSignal(AfterPublish, func(ctx context.Context, ev SignalEvent) error {
		busHandlerFired <- struct{}{}
		return nil
	})
	app.wireSignalBus()

	mod := app.MCPModules()[0]
	userCtx := NewTestContext(User{ID: "u1", Roles: []Role{Author}})

	item, err := mod.MCPCreate(userCtx, map[string]any{
		"title": "G32 Coexist Post",
		"body":  "Tests OnSignal + Webhooks coexistence.",
	})
	if err != nil {
		t.Fatalf("MCPCreate: %v", err)
	}
	post := item.(*g32Post)
	if err := mod.MCPPublish(userCtx, post.Slug); err != nil {
		t.Fatalf("MCPPublish: %v", err)
	}

	// Both the custom bus handler and webhook job must be triggered.
	select {
	case <-busHandlerFired:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("custom OnSignal handler not called within 500 ms")
	}

	deadline := time.Now().Add(500 * time.Millisecond)
	var jobs []OutboundJob
	for time.Now().Before(deadline) {
		jobs, _ = app.WebhookPool().ListJobsForEndpoint(userCtx, "ep-g32")
		if len(jobs) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(jobs) == 0 {
		t.Fatal("no webhook job enqueued; Webhooks handler must coexist with OnSignal handlers")
	}
}

// â€” G33 â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”

type g33Post struct {
	Node
	Title string `json:"title" forge:"required"`
}

// chanAuditStore wraps fakeAuditStore and signals a channel on each Append.
type chanAuditStore struct {
	mu       sync.Mutex
	records  []AuditRecord
	appended chan AuditRecord
}

func newChanAuditStore() *chanAuditStore {
	return &chanAuditStore{appended: make(chan AuditRecord, 10)}
}

func (s *chanAuditStore) Append(_ context.Context, r AuditRecord) error {
	s.mu.Lock()
	s.records = append(s.records, r)
	s.mu.Unlock()
	s.appended <- r
	return nil
}

func (s *chanAuditStore) List(_ context.Context, f AuditFilter) ([]AuditRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []AuditRecord
	for _, r := range s.records {
		if f.ContentType != "" && r.ContentType != f.ContentType {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

// TestFull_G33_AuditTrailLifecycle verifies App.Audit() integration with the
// signal bus (M14/A94) and the /_audit HTTP endpoint (A97).
//
// Scenarios:
//   - MCPPublish fires AfterPublish â†’ AuditRecord appended with correct fields
//   - MCPCreate does NOT produce an audit record (AfterCreate not subscribed)
//   - GET /_audit with Guest â†’ 401; with Author â†’ 403; with Editor â†’ 200
//   - GET /_audit?type=g33Post filters to matching records only
func TestFull_G33_AuditTrailLifecycle(t *testing.T) {
	secret := []byte("g33-audit-trail-integration-test")
	store := newChanAuditStore()
	app := New(MustConfig(Config{
		BaseURL: "https://example.com",
		Secret:  secret,
	}))
	app.Audit(store)

	repo := NewMemoryRepo[*g33Post]()
	m := NewModule((*g33Post)(nil),
		At("/g33posts"),
		Repo(repo),
		MCP(MCPRead, MCPWrite),
	)
	app.Content(m)
	app.wireSignalBus()

	editorCtx := NewTestContext(User{ID: "editor-1", Roles: []Role{Editor}})

	// MCPCreate: AfterCreate fires â€” must NOT be recorded by audit trail.
	item, err := m.MCPCreate(editorCtx, map[string]any{"title": "G33 Post"})
	if err != nil {
		t.Fatalf("MCPCreate: %v", err)
	}
	post := item.(*g33Post)

	// Give AfterCreate goroutine time to run, then verify nothing was appended.
	time.Sleep(150 * time.Millisecond)
	select {
	case r := <-store.appended:
		t.Errorf("unexpected audit record for AfterCreate: signal=%s", r.Signal)
	default:
		// correct â€” AfterCreate is not subscribed
	}

	// MCPPublish: AfterPublish fires â†’ AuditRecord must be appended.
	if err := m.MCPPublish(editorCtx, post.Slug); err != nil {
		t.Fatalf("MCPPublish: %v", err)
	}

	var rec AuditRecord
	select {
	case rec = <-store.appended:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("AuditStore.Append not called within 500 ms after MCPPublish")
	}
	if rec.Signal != AfterPublish {
		t.Errorf("Signal = %q, want %q", rec.Signal, AfterPublish)
	}
	if rec.ContentType != "g33Post" {
		t.Errorf("ContentType = %q, want g33Post", rec.ContentType)
	}
	if rec.Slug != post.Slug {
		t.Errorf("Slug = %q, want %q", rec.Slug, post.Slug)
	}
	if rec.ActorID != "editor-1" {
		t.Errorf("ActorID = %q, want editor-1", rec.ActorID)
	}
	if rec.ID == "" {
		t.Error("AuditRecord.ID is empty")
	}

	h := app.Handler()

	// GET /_audit â€” Guest â†’ 401.
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/_audit", nil))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("Guest: status = %d, want 401", w.Code)
	}

	// GET /_audit â€” Author â†’ 403.
	authorTok, _ := SignToken(User{ID: "a1", Roles: []Role{Author}}, string(secret), 0)
	w = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/_audit", nil)
	req.Header.Set("Authorization", "Bearer "+authorTok)
	h.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("Author: status = %d, want 403", w.Code)
	}

	// GET /_audit â€” Editor â†’ 200 with JSON array containing the AfterPublish record.
	editorTok, _ := SignToken(User{ID: "editor-1", Roles: []Role{Editor}}, string(secret), 0)
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/_audit", nil)
	req.Header.Set("Authorization", "Bearer "+editorTok)
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("Editor: status = %d, want 200", w.Code)
	}
	var records []AuditRecord
	if err := json.NewDecoder(w.Body).Decode(&records); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(records) == 0 {
		t.Error("expected at least one record, got empty")
	}

	// GET /_audit?type=g33Post filters correctly.
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/_audit?type=g33Post", nil)
	req.Header.Set("Authorization", "Bearer "+editorTok)
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("type filter: status = %d, want 200", w.Code)
	}
	var filtered []AuditRecord
	if err := json.NewDecoder(w.Body).Decode(&filtered); err != nil {
		t.Fatalf("decode filtered: %v", err)
	}
	for _, r := range filtered {
		if r.ContentType != "g33Post" {
			t.Errorf("type filter: got ContentType=%q, want g33Post", r.ContentType)
		}
	}

	// GET /_audit?type=OtherType returns empty array, not error.
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/_audit?type=OtherType", nil)
	req.Header.Set("Authorization", "Bearer "+editorTok)
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("other type filter: status = %d, want 200", w.Code)
	}
	body := strings.TrimSpace(w.Body.String())
	if !strings.HasPrefix(body, "[") {
		t.Errorf("expected JSON array for empty result, got: %q", body)
	}
}

// â€” G34: SingleInstance routing (T50) â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”

// g34Page is the content type for the G34 SingleInstance group.
type g34Page struct {
	Node
	Title string `forge:"required" db:"title"`
}

// TestFull_G34_SingleInstanceRouting verifies that a SingleInstance() module:
//   - serves GET /{prefix} with the first Published item (JSON)
//   - returns 404 for GET /{prefix} when no Published item exists
//   - has MCPMeta().SingleInstance == true
//   - does not register GET /{prefix}/{slug} (404)
func TestFull_G34_SingleInstanceRouting(t *testing.T) {
	repo := NewMemoryRepo[*g34Page]()
	m := NewModule((*g34Page)(nil),
		At("/about"),
		Repo(repo),
		SingleInstance(),
		MCP(MCPRead, MCPWrite),
	)
	app := New(MustConfig(Config{
		BaseURL: "http://localhost",
		Secret:  []byte("g34-single-instance-test-secret!"),
	}))
	app.Content(m)
	h := app.Handler()

	t.Run("MCPMeta.SingleInstance is true", func(t *testing.T) {
		meta := m.MCPMeta()
		if !meta.SingleInstance {
			t.Error("MCPMeta().SingleInstance = false; want true")
		}
	})

	t.Run("GET /about â†’ 404 when no published items", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/about", nil)
		h.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Errorf("status = %d; want 404", w.Code)
		}
	})

	// Insert a Draft â€” should still 404 for unauthenticated request.
	draft := &g34Page{
		Node:  Node{ID: NewID(), Slug: GenerateSlug("About Us"), Status: Draft},
		Title: "About Us Draft",
	}
	if err := repo.Save(context.Background(), draft); err != nil {
		t.Fatalf("save draft: %v", err)
	}

	t.Run("GET /about â†’ 404 when only Draft exists (guest)", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/about", nil)
		h.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Errorf("status = %d; want 404", w.Code)
		}
	})

	// Publish the page.
	draft.Status = Published
	if err := repo.Save(context.Background(), draft); err != nil {
		t.Fatalf("save published: %v", err)
	}

	t.Run("GET /about â†’ 200 JSON with published item", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/about", nil)
		req.Header.Set("Accept", "application/json")
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("status = %d; want 200", w.Code)
		}
		var out map[string]any
		if err := json.NewDecoder(w.Body).Decode(&out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if out["Title"] != "About Us Draft" {
			t.Errorf("Title = %v; want 'About Us Draft'", out["Title"])
		}
	})

	t.Run("GET /about/{slug} â†’ 404 (route not registered)", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/about/"+draft.Slug, nil)
		h.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Errorf("status = %d; want 404", w.Code)
		}
	})
}

// â€” G35: Standalone routing (T50) â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”â€”

// g35Post is the content type for the G35 Standalone group.
type g35Post struct {
	Node
	Title string `forge:"required" db:"title"`
}

// g35Article is a second Standalone content type for multi-module dispatch.
type g35Article struct {
	Node
	Title string `forge:"required" db:"title"`
}

// TestFull_G35_StandaloneRouting verifies that a Standalone() module:
//   - serves GET /{slug} at the top level (dispatched by App)
//   - returns 404 via redirect fallback when slug is absent
//   - list at GET /{prefix} still works normally
//   - two standalone modules coexist: each slug dispatches to correct module
func TestFull_G35_StandaloneRouting(t *testing.T) {
	postRepo := NewMemoryRepo[*g35Post]()
	articleRepo := NewMemoryRepo[*g35Article]()

	posts := NewModule((*g35Post)(nil),
		At("/posts"),
		Repo(postRepo),
		Standalone(),
	)
	articles := NewModule((*g35Article)(nil),
		At("/articles"),
		Repo(articleRepo),
		Standalone(),
	)

	app := New(MustConfig(Config{
		BaseURL: "http://localhost",
		Secret:  []byte("g35-standalone-routing-secret!!"),
	}))
	app.Content(posts)
	app.Content(articles)
	h := app.Handler()

	// Insert a published post and a published article.
	post := &g35Post{
		Node:  Node{ID: NewID(), Slug: GenerateSlug("Hello World"), Status: Published},
		Title: "Hello World",
	}
	article := &g35Article{
		Node:  Node{ID: NewID(), Slug: GenerateSlug("First Article"), Status: Published},
		Title: "First Article",
	}
	if err := postRepo.Save(context.Background(), post); err != nil {
		t.Fatalf("save post: %v", err)
	}
	if err := articleRepo.Save(context.Background(), article); err != nil {
		t.Fatalf("save article: %v", err)
	}

	t.Run("GET /hello-world â†’ 200 JSON from posts module", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/"+post.Slug, nil)
		req.Header.Set("Accept", "application/json")
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("status = %d; want 200", w.Code)
		}
		var out map[string]any
		if err := json.NewDecoder(w.Body).Decode(&out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if out["Title"] != "Hello World" {
			t.Errorf("Title = %v; want 'Hello World'", out["Title"])
		}
	})

	t.Run("GET /first-article â†’ 200 JSON from articles module", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/"+article.Slug, nil)
		req.Header.Set("Accept", "application/json")
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("status = %d; want 200", w.Code)
		}
		var out map[string]any
		if err := json.NewDecoder(w.Body).Decode(&out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if out["Title"] != "First Article" {
			t.Errorf("Title = %v; want 'First Article'", out["Title"])
		}
	})

	t.Run("GET /nonexistent â†’ 404 (no module has this slug)", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/nonexistent-slug", nil)
		h.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Errorf("status = %d; want 404", w.Code)
		}
	})

	t.Run("GET /posts â†’ list still works (module prefix unaffected)", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/posts", nil)
		req.Header.Set("Accept", "application/json")
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("status = %d; want 200", w.Code)
		}
	})

	t.Run("GET /posts/{slug} â†’ 404 (not registered for standalone module)", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/posts/"+post.Slug, nil)
		req.Header.Set("Accept", "application/json")
		h.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Errorf("status = %d; want 404", w.Code)
		}
	})

	t.Run("Draft item not served to guest via /{slug}", func(t *testing.T) {
		draft := &g35Post{
			Node:  Node{ID: NewID(), Slug: GenerateSlug("Draft Post"), Status: Draft},
			Title: "Draft Post",
		}
		if err := postRepo.Save(context.Background(), draft); err != nil {
			t.Fatalf("save draft: %v", err)
		}
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/"+draft.Slug, nil)
		h.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Errorf("draft slug: status = %d; want 404", w.Code)
		}
	})
}

// g36HomePage is an admin-only content type with no public HTML surface.
type g36HomePage struct {
	Node
	Title   string `json:"title"    forge:"required" db:"title"`
	HeroURL string `json:"hero_url" db:"hero_url"`
}

// TestFull_G36_APIOnly verifies that an APIOnly() module (T51/A102):
//   - GET /{prefix} with Accept: text/html → 404
//   - GET /{prefix} with Accept: application/json → 200 with items
//   - GET /{prefix}/{slug} with Accept: text/html → 404
//   - GET /{prefix}/{slug} with Accept: application/json → 200
//   - Preview token bypass for Draft item via JSON → 200
func TestFull_G36_APIOnly(t *testing.T) {
	secret := []byte("g36-api-only-test-secret-32byte!")
	repo := NewMemoryRepo[*g36HomePage]()
	m := NewModule((*g36HomePage)(nil),
		At("/home-pages"),
		Repo(repo),
		MCP(MCPWrite),
		APIOnly(),
	)

	app := New(MustConfig(Config{
		BaseURL: "http://localhost",
		Secret:  secret,
	}))
	app.Content(m)
	h := app.Handler()

	// Seed a Published item and a Draft item.
	published := &g36HomePage{
		Node:    Node{ID: NewID(), Slug: "home", Status: Published},
		Title:   "Home Page",
		HeroURL: "https://example.com/hero.jpg",
	}
	draft := &g36HomePage{
		Node:    Node{ID: NewID(), Slug: "home-draft", Status: Draft},
		Title:   "Home Page Draft",
		HeroURL: "https://example.com/hero-draft.jpg",
	}
	if err := repo.Save(context.Background(), published); err != nil {
		t.Fatalf("save published: %v", err)
	}
	if err := repo.Save(context.Background(), draft); err != nil {
		t.Fatalf("save draft: %v", err)
	}

	t.Run("GET /home-pages Accept:text/html → 404", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/home-pages", nil)
		req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
		h.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Errorf("status = %d; want 404", w.Code)
		}
	})

	t.Run("GET /home-pages Accept:application/json → 200 with items", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/home-pages", nil)
		req.Header.Set("Accept", "application/json")
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("status = %d; want 200", w.Code)
		}
		var items []map[string]any
		if err := json.NewDecoder(w.Body).Decode(&items); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(items) == 0 {
			t.Error("expected at least one item in response")
		}
	})

	t.Run("GET /home-pages/home Accept:text/html → 404", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/home-pages/home", nil)
		req.Header.Set("Accept", "text/html")
		h.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Errorf("status = %d; want 404", w.Code)
		}
	})

	t.Run("GET /home-pages/home Accept:application/json → 200", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/home-pages/home", nil)
		req.Header.Set("Accept", "application/json")
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("status = %d; want 200", w.Code)
		}
		var out map[string]any
		if err := json.NewDecoder(w.Body).Decode(&out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if out["title"] != "Home Page" {
			t.Errorf("title = %v; want 'Home Page'", out["title"])
		}
	})

	t.Run("Preview token bypass for Draft item via JSON → 200", func(t *testing.T) {
		token := app.GeneratePreviewToken("/home-pages", draft.Slug)
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/home-pages/"+draft.Slug+"?preview="+token, nil)
		req.Header.Set("Accept", "application/json")
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("preview status = %d; want 200", w.Code)
		}
		var out map[string]any
		if err := json.NewDecoder(w.Body).Decode(&out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if out["title"] != "Home Page Draft" {
			t.Errorf("title = %v; want 'Home Page Draft'", out["title"])
		}
	})
}
