package forge

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// — Integration test helpers ——————————————————————————————————————————————

// intTmpDir creates a temp dir with list.html and show.html written to it.
// Pass empty string to skip writing a file (TemplatesOptional mode).
func intTmpDir(t *testing.T, listTpl, showTpl string) string {
	t.Helper()
	dir := t.TempDir()
	if listTpl != "" {
		if err := os.WriteFile(filepath.Join(dir, "list.html"), []byte(listTpl), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if showTpl != "" {
		if err := os.WriteFile(filepath.Join(dir, "show.html"), []byte(showTpl), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// intSetup creates an App + Module[*testPost] with templates parsed.
// Templates are parsed before Handler() is called so tests do not need Run().
// additionalOpts are appended to the module options after Repo and At("/posts").
func intSetup(t *testing.T, moduleOpts ...Option) (*App, http.Handler, *MemoryRepo[*testPost]) {
	t.Helper()
	repo := NewMemoryRepo[*testPost]()
	opts := append([]Option{Repo(repo), At("/posts")}, moduleOpts...)
	m := NewModule((*testPost)(nil), opts...)
	app := New(MustConfig(Config{
		BaseURL: "https://example.com",
		Secret:  []byte("16bytessecretkey"),
	}))
	app.Content(m)
	if err := m.parseTemplates(); err != nil {
		t.Fatalf("intSetup parseTemplates: %v", err)
	}
	return app, app.Handler(), repo
}

// intSeed saves a published *testPost with the given slug and title.
func intSeed(t *testing.T, repo *MemoryRepo[*testPost], slug, title string) *testPost {
	t.Helper()
	p := &testPost{
		Node:  Node{ID: NewID(), Slug: slug, Status: Published},
		Title: title,
	}
	if err := repo.Save(context.Background(), p); err != nil {
		t.Fatalf("intSeed: %v", err)
	}
	return p
}

// — 4.1 HTML render cycle —————————————————————————————————————————————————

func TestIntegration_showHTML(t *testing.T) {
	dir := intTmpDir(t,
		`<p>list</p>`,
		`<!DOCTYPE html><html><head><title>{{.Head.Title}}</title></head><body><p>{{.Content.Title}}</p></body></html>`,
	)
	_, handler, repo := intSetup(t,
		Templates(dir),
		HeadFunc(func(_ Context, p *testPost) Head {
			return Head{Title: "Show: " + p.Title}
		}),
	)
	intSeed(t, repo, "my-post", "My Post")

	r := httptest.NewRequest("GET", "/posts/my-post", nil)
	r.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	ct := w.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Show: My Post") {
		t.Errorf("expected title in body, got:\n%s", body)
	}
	if !strings.Contains(body, "My Post") {
		t.Errorf("expected post title in body, got:\n%s", body)
	}
}

func TestIntegration_listHTML(t *testing.T) {
	dir := intTmpDir(t,
		`<!DOCTYPE html><html><body><p>list: {{len .Content}} items</p></body></html>`,
		`<p>show</p>`,
	)
	_, handler, repo := intSetup(t, Templates(dir))
	intSeed(t, repo, "post-one", "Post One")
	intSeed(t, repo, "post-two", "Post Two")

	r := httptest.NewRequest("GET", "/posts", nil)
	r.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "list:") {
		t.Errorf("expected list content in body, got: %s", w.Body.String())
	}
}

func TestIntegration_json_unaffected(t *testing.T) {
	dir := intTmpDir(t, `<p>list</p>`, `<p>show</p>`)
	_, handler, repo := intSetup(t, Templates(dir))
	p := intSeed(t, repo, "json-post", "JSON Post")
	_ = p

	r := httptest.NewRequest("GET", "/posts", nil)
	r.Header.Set("Accept", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	ct := w.Header().Get("Content-Type")
	if !strings.Contains(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
}

func TestIntegration_htmlFallback_noTemplates(t *testing.T) {
	_, handler, _ := intSetup(t) // no Templates option

	r := httptest.NewRequest("GET", "/posts", nil)
	r.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	// No templates → JSON fallback 200, not 406 (A35).
	if w.Code != 200 {
		t.Errorf("status = %d, want 200 (JSON fallback, A35); body: %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
}

// — 4.2 forge:head correctness ————————————————————————————————————————————

// headTpl is a minimal show template that includes the forge:head partial.
const headTpl = `<!DOCTYPE html><html><head>{{template "forge:head" .}}</head><body>{{.Content.Title}}</body></html>`

func TestIntegration_forgeHead_noIndex(t *testing.T) {
	dir := intTmpDir(t, `<p>list</p>`, headTpl)
	_, handler, repo := intSetup(t,
		Templates(dir),
		HeadFunc(func(_ Context, _ *testPost) Head {
			return Head{Title: "Hidden", NoIndex: true}
		}),
	)
	intSeed(t, repo, "hidden-post", "Hidden")

	r := httptest.NewRequest("GET", "/posts/hidden-post", nil)
	r.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != 200 {
		t.Fatalf("status = %d; body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "noindex") {
		t.Errorf("expected noindex meta in body, got:\n%s", w.Body.String())
	}
}

func TestIntegration_forgeHead_canonical(t *testing.T) {
	dir := intTmpDir(t, `<p>list</p>`, headTpl)
	_, handler, repo := intSetup(t,
		Templates(dir),
		HeadFunc(func(_ Context, p *testPost) Head {
			return Head{Title: p.Title, Canonical: "https://example.com/posts/" + p.Slug}
		}),
	)
	intSeed(t, repo, "canon-post", "Canonical Post")

	r := httptest.NewRequest("GET", "/posts/canon-post", nil)
	r.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != 200 {
		t.Fatalf("status = %d; body: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `rel="canonical"`) {
		t.Errorf("expected canonical link in body, got:\n%s", body)
	}
	if !strings.Contains(body, "https://example.com/posts/canon-post") {
		t.Errorf("expected canonical URL in body, got:\n%s", body)
	}
}

func TestIntegration_forgeHead_jsonLD(t *testing.T) {
	const jsonLDTpl = `<!DOCTYPE html><html><head>{{template "forge:head" .}}{{forge_meta .Head .Content}}</head><body></body></html>`
	dir := intTmpDir(t, `<p>list</p>`, jsonLDTpl)
	_, handler, repo := intSetup(t,
		Templates(dir),
		HeadFunc(func(_ Context, p *testPost) Head {
			return Head{Title: p.Title, Type: Article}
		}),
	)
	intSeed(t, repo, "article-post", "My Article")

	r := httptest.NewRequest("GET", "/posts/article-post", nil)
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
	if !strings.Contains(body, "Article") {
		t.Errorf("expected Article schema type in body, got:\n%s", body)
	}
}

// — 4.3 Error pages ———————————————————————————————————————————————————————

func TestIntegration_errorPage_custom(t *testing.T) {
	// Reset errorTemplateLookup after test to avoid cross-test pollution.
	origLookup := errorTemplateLookup
	t.Cleanup(func() { setErrorTemplateLookup(origLookup) })

	dir := intTmpDir(t, `<p>list</p>`, `<p>show</p>`)
	errDir := filepath.Join(dir, "errors")
	if err := os.MkdirAll(errDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(errDir, "404.html"),
		[]byte(`<p>custom-not-found: {{.Message}}</p>`), 0644); err != nil {
		t.Fatal(err)
	}

	_, handler, _ := intSetup(t, Templates(dir))

	r := httptest.NewRequest("GET", "/posts/does-not-exist", nil)
	r.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != 404 {
		t.Fatalf("status = %d, want 404; body: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "custom-not-found") {
		t.Errorf("expected custom error page, got:\n%s", body)
	}
}

func TestIntegration_errorPage_fallback(t *testing.T) {
	origLookup := errorTemplateLookup
	t.Cleanup(func() { setErrorTemplateLookup(origLookup) })
	setErrorTemplateLookup(nil)

	// Module without templates — no error template directory.
	_, handler, _ := intSetup(t)

	r := httptest.NewRequest("GET", "/posts/does-not-exist", nil)
	r.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != 404 {
		t.Fatalf("status = %d, want 404", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "404") {
		t.Errorf("expected 404 in fallback HTML error body, got:\n%s", body)
	}
}

// — 4.4 CSRF ——————————————————————————————————————————————————————————————

func TestIntegration_csrf_tokenInForm(t *testing.T) {
	const csrfTpl = `{{forge_csrf_token .Request}}`
	dir := intTmpDir(t, `<p>list</p>`, csrfTpl)
	app, _, repo := intSetup(t, Templates(dir))

	auth := CookieSession("session", "16bytessecretkey")
	app.Use(CSRF(auth))
	handler := app.Handler()

	intSeed(t, repo, "form-post", "Form Post")

	r := httptest.NewRequest("GET", "/posts/form-post", nil)
	r.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "tok-xyz-123"})
	r.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != 200 {
		t.Fatalf("status = %d; body: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `<input type="hidden"`) {
		t.Errorf("expected hidden input in body, got:\n%s", body)
	}
	if !strings.Contains(body, "tok-xyz-123") {
		t.Errorf("expected CSRF token value in body, got:\n%s", body)
	}
}

func TestIntegration_csrf_rejectMissing(t *testing.T) {
	_, handler, _ := intSetup(t)

	auth := CookieSession("session", "16bytessecretkey")
	mux := http.NewServeMux()
	mux.Handle("/posts", CSRF(auth)(handler))

	r := httptest.NewRequest("POST", "/posts", strings.NewReader(`{"Title":"New"}`))
	r.Header.Set("Content-Type", "application/json")
	// No CSRF cookie or X-CSRF-Token header.
	w := httptest.NewRecorder()
	Chain(handler, CSRF(auth)).ServeHTTP(w, r)

	if w.Code != 403 {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

// — 4.5 App-level SEO + Sitemap ———————————————————————————————————————————

func TestIntegration_seo_robotsTxt(t *testing.T) {
	_, handler, _ := intSetup(t)
	appForSEO := New(MustConfig(Config{
		BaseURL: "https://example.com",
		Secret:  []byte("16bytessecretkey"),
	}))
	repo := NewMemoryRepo[*testPost]()
	m := NewModule((*testPost)(nil), Repo(repo), At("/posts"))
	appForSEO.Content(m)
	appForSEO.SEO(&RobotsConfig{})
	h := appForSEO.Handler()

	r := httptest.NewRequest("GET", "/robots.txt", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != 200 {
		t.Errorf("GET /robots.txt status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	_ = handler // suppress unused warning
}

func TestIntegration_sitemap_index(t *testing.T) {
	app := New(MustConfig(Config{
		BaseURL: "https://example.com",
		Secret:  []byte("16bytessecretkey"),
	}))
	repo := NewMemoryRepo[*testPost]()
	m := NewModule((*testPost)(nil), Repo(repo), At("/posts"), SitemapConfig{})
	app.Content(m)
	h := app.Handler()

	r := httptest.NewRequest("GET", "/sitemap.xml", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != 200 {
		t.Errorf("GET /sitemap.xml status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
}

// — 4.6 TemplateData correctness ——————————————————————————————————————————

func TestIntegration_templateData_user(t *testing.T) {
	const userTpl = `user:{{.User.ID}}`
	dir := intTmpDir(t, `<p>list</p>`, userTpl)
	_, handler, repo := intSetup(t, Templates(dir))
	intSeed(t, repo, "user-post", "User Post")

	r := httptest.NewRequest("GET", "/posts/user-post", nil)
	r = withUser(r, User{ID: "alice-42", Name: "Alice", Roles: []Role{Editor}})
	r.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != 200 {
		t.Fatalf("status = %d; body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "user:alice-42") {
		t.Errorf("expected user ID in rendered output, got:\n%s", w.Body.String())
	}
}

func TestIntegration_templateData_head(t *testing.T) {
	const headTitleTpl = `<title>{{.Head.Title}}</title>`
	dir := intTmpDir(t, `<p>list</p>`, headTitleTpl)
	_, handler, repo := intSetup(t,
		Templates(dir),
		HeadFunc(func(_ Context, p *testPost) Head {
			return Head{Title: "HeadFunc: " + p.Title}
		}),
	)
	intSeed(t, repo, "head-post", "Head Post")

	r := httptest.NewRequest("GET", "/posts/head-post", nil)
	r.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != 200 {
		t.Fatalf("status = %d; body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "HeadFunc: Head Post") {
		t.Errorf("expected HeadFunc title in rendered output, got:\n%s", w.Body.String())
	}
}

// — A35 content negotiation capability gating ————————————————————————————

// TestIntegration_negotiateHTMLFallback verifies that a module with no
// Templates option returns JSON (not 406) when the Accept header requests
// text/html. Before A35, negotiate() returned "text/html" regardless of
// n.html, causing writeContent to unconditionally 406.
func TestIntegration_negotiateHTMLFallback(t *testing.T) {
	// Module has no Templates option — n.html is false.
	_, handler, repo := intSetup(t)
	intSeed(t, repo, "json-post", "JSON Post")

	r := httptest.NewRequest("GET", "/posts/json-post", nil)
	r.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != 200 {
		t.Fatalf("A35: status = %d, want 200 (JSON fallback); body: %s", w.Code, w.Body.String())
	}
	ct := w.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Errorf("A35: Content-Type = %q, want application/json", ct)
	}
}

// TestIntegration_negotiateMarkdownFallback verifies that a module whose
// content type does not implement Markdownable returns JSON (not 406) when
// Accept: text/markdown is sent. Before A35, negotiate() returned
// "text/markdown" regardless of n.md, causing writeContent to 406.
func TestIntegration_negotiateMarkdownFallback(t *testing.T) {
	// testPost does not implement Markdownable — n.md is false.
	_, handler, repo := intSetup(t)
	intSeed(t, repo, "md-post", "MD Post")

	r := httptest.NewRequest("GET", "/posts/md-post", nil)
	r.Header.Set("Accept", "text/markdown")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != 200 {
		t.Fatalf("A35: status = %d, want 200 (JSON fallback); body: %s", w.Code, w.Body.String())
	}
	ct := w.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Errorf("A35: Content-Type = %q, want application/json", ct)
	}
}
