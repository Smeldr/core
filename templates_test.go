package smeldr

import (
	"bytes"
	"html/template"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newTplModule creates a *Module[*tdPost] wired with the given options.
// Uses the tdPost type defined in templatedata_test.go.
func newTplModule(t *testing.T, opts ...Option) *Module[*tdPost] {
	t.Helper()
	repo := NewMemoryRepo[*tdPost]()
	return NewModule((*tdPost)(nil), append([]Option{Repo(repo), At("/test")}, opts...)...)
}

// writeTplFile writes content to a file in dir, fatally failing t on error.
func writeTplFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestTemplates_missingList(t *testing.T) {
	dir := t.TempDir()
	// Only show.html present — list.html is absent.
	writeTplFile(t, dir, "show.html", `<p>show: {{.SiteName}}</p>`)

	m := newTplModule(t, Templates(dir))
	err := m.parseTemplates()
	if err == nil {
		t.Fatal("expected error for missing list.html in required mode, got nil")
	}
	if !strings.Contains(err.Error(), "list.html") {
		t.Errorf("error should mention list.html, got: %v", err)
	}
}

func TestTemplates_missingShow(t *testing.T) {
	dir := t.TempDir()
	// Only list.html present — show.html is absent.
	writeTplFile(t, dir, "list.html", `<p>list: {{.SiteName}}</p>`)

	m := newTplModule(t, Templates(dir))
	err := m.parseTemplates()
	if err == nil {
		t.Fatal("expected error for missing show.html in required mode, got nil")
	}
	if !strings.Contains(err.Error(), "show.html") {
		t.Errorf("error should mention show.html, got: %v", err)
	}
}

func TestTemplatesOptional_missingDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nonexistent")

	m := newTplModule(t, TemplatesOptional(dir))
	if err := m.parseTemplates(); err != nil {
		t.Fatalf("optional mode should not error for absent dir, got: %v", err)
	}

	m.tplMu.RLock()
	tplList := m.tplList
	tplShow := m.tplShow
	m.tplMu.RUnlock()

	if tplList != nil {
		t.Error("expected tplList to be nil for absent optional dir")
	}
	if tplShow != nil {
		t.Error("expected tplShow to be nil for absent optional dir")
	}
}

func TestTemplates_forgeHeadRegistered(t *testing.T) {
	dir := t.TempDir()
	writeTplFile(t, dir, "list.html", `<p>list</p>`)
	writeTplFile(t, dir, "show.html", `<p>show</p>`)

	m := newTplModule(t, Templates(dir))
	if err := m.parseTemplates(); err != nil {
		t.Fatalf("parseTemplates: %v", err)
	}

	m.tplMu.RLock()
	tplList := m.tplList
	tplShow := m.tplShow
	m.tplMu.RUnlock()

	if tplList == nil {
		t.Fatal("tplList is nil after parseTemplates")
	}
	if tplList.Lookup("smeldr:head") == nil {
		t.Error("smeldr:head not registered in list template set")
	}
	if tplShow == nil {
		t.Fatal("tplShow is nil after parseTemplates")
	}
	if tplShow.Lookup("smeldr:head") == nil {
		t.Error("smeldr:head not registered in show template set")
	}
}

func TestTemplates_noIndexMeta(t *testing.T) {
	// Execute smeldrHeadTmpl directly — no module needed. FuncMap required
	// because smeldr:head uses forge_rfc3339 for article:published_time.
	tpl := template.Must(template.New("test").Funcs(TemplateFuncMap()).Parse(smeldrHeadTmpl))
	var buf bytes.Buffer
	h := Head{Title: "Test Page", NoIndex: true}
	if err := tpl.ExecuteTemplate(&buf, "smeldr:head", TemplateData[any]{PageHead: PageHead{Head: h}}); err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "noindex") {
		t.Errorf("expected noindex in smeldr:head output, got:\n%s", got)
	}
	if !strings.Contains(got, "Test Page") {
		t.Errorf("expected title in smeldr:head output, got:\n%s", got)
	}
}

func TestTemplates_errorPage_custom(t *testing.T) {
	orig := errorTemplateLookup
	defer func() { setErrorTemplateLookup(orig) }()

	dir := t.TempDir()
	writeTplFile(t, dir, "list.html", `<p>list</p>`)
	writeTplFile(t, dir, "show.html", `<p>show</p>`)
	errDir := filepath.Join(dir, "errors")
	if err := os.MkdirAll(errDir, 0755); err != nil {
		t.Fatal(err)
	}
	writeTplFile(t, errDir, "404.html", `<p>custom 404: {{.Message}}</p>`)

	m := newTplModule(t, Templates(dir))
	if err := m.parseTemplates(); err != nil {
		t.Fatalf("parseTemplates: %v", err)
	}
	bindErrorTemplates([]templateParser{m})

	tpl := runErrorTemplateLookup(404)
	if tpl == nil {
		t.Fatal("expected non-nil template for status 404, got nil")
	}
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, struct {
		Status    int
		Message   string
		RequestID string
	}{404, "Not found", "req-abc"}); err != nil {
		t.Fatalf("Execute error template: %v", err)
	}
	if !strings.Contains(buf.String(), "custom 404") {
		t.Errorf("expected custom 404 content in output, got: %s", buf.String())
	}
}

func TestTemplates_twitterCard(t *testing.T) {
	tpl := template.Must(template.New("test").Funcs(TemplateFuncMap()).Parse(smeldrHeadTmpl))

	cases := []struct {
		name string
		head Head
		want string
	}{
		{
			name: "Article type → summary_large_image",
			head: Head{Title: "T", Type: Article},
			want: "summary_large_image",
		},
		{
			name: "Product type → summary_large_image",
			head: Head{Title: "T", Type: Product},
			want: "summary_large_image",
		},
		{
			name: "no type no image → summary",
			head: Head{Title: "T"},
			want: "summary",
		},
		{
			name: "explicit Card override takes priority over Article",
			head: Head{Title: "T", Type: Article, Social: SocialOverrides{Twitter: TwitterMeta{Card: Summary}}},
			want: "summary",
		},
		{
			name: "image without type → summary_large_image",
			head: Head{Title: "T", Image: Image{URL: "/img.jpg"}},
			want: "summary_large_image",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := tpl.ExecuteTemplate(&buf, "smeldr:head", TemplateData[any]{PageHead: PageHead{Head: tc.head}}); err != nil {
				t.Fatalf("ExecuteTemplate: %v", err)
			}
			want := `content="` + string(tc.want) + `"`
			if !strings.Contains(buf.String(), want) {
				t.Errorf("twitter:card: want %q in output:\n%s", want, buf.String())
			}
		})
	}
}

func TestTemplates_errorPage_fallback(t *testing.T) {
	orig := errorTemplateLookup
	defer func() { setErrorTemplateLookup(orig) }()
	setErrorTemplateLookup(nil)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/missing", nil)
	r.Header.Set("Accept", "text/html")

	WriteError(w, r, ErrNotFound)

	body := w.Body.String()
	if w.Code != 404 {
		t.Errorf("status = %d, want 404", w.Code)
	}
	if !strings.Contains(body, "404") {
		t.Errorf("expected 404 in fallback HTML body, got: %s", body)
	}
	if !strings.Contains(body, "Not found") {
		t.Errorf("expected 'Not found' in fallback HTML body, got: %s", body)
	}
}

// — A62: Shared partials ——————————————————————————————————————————————————

// writePartialFile writes a {{define}} partial into a partials sub-directory.
func writePartialFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestPartials_availableInListTemplate(t *testing.T) {
	dir := t.TempDir()
	partialsDir := filepath.Join(dir, "partials")
	writePartialFile(t, partialsDir, "nav.html", `{{define "nav"}}<nav>site-nav</nav>{{end}}`)
	writeTplFile(t, dir, "list.html", `{{template "nav" .}}<p>list</p>`)
	writeTplFile(t, dir, "show.html", `<p>show</p>`)

	partials, err := loadPartials(partialsDir)
	if err != nil {
		t.Fatalf("loadPartials: %v", err)
	}
	m := newTplModule(t, Templates(dir))
	m.setPartials(partials)
	if err := m.parseTemplates(); err != nil {
		t.Fatalf("parseTemplates: %v", err)
	}

	data := NewTemplateData[[](*tdPost)](NewTestContext(GuestUser), nil, Head{}, "test")
	var buf bytes.Buffer
	if err := m.tplList.Execute(&buf, data); err != nil {
		t.Fatalf("Execute list: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "site-nav") {
		t.Errorf("expected nav partial in list output, got:\n%s", got)
	}
}

func TestPartials_availableInShowTemplate(t *testing.T) {
	dir := t.TempDir()
	partialsDir := filepath.Join(dir, "partials")
	writePartialFile(t, partialsDir, "footer.html", `{{define "footer"}}<footer>site-footer</footer>{{end}}`)
	writeTplFile(t, dir, "list.html", `<p>list</p>`)
	writeTplFile(t, dir, "show.html", `{{template "footer" .}}<p>show</p>`)

	partials, err := loadPartials(partialsDir)
	if err != nil {
		t.Fatalf("loadPartials: %v", err)
	}
	m := newTplModule(t, Templates(dir))
	m.setPartials(partials)
	if err := m.parseTemplates(); err != nil {
		t.Fatalf("parseTemplates: %v", err)
	}

	p := &tdPost{Node: Node{ID: NewID(), Slug: "s", Status: Published}, Title: "T"}
	data := NewTemplateData(NewTestContext(GuestUser), p, Head{}, "test")
	var buf bytes.Buffer
	if err := m.tplShow.Execute(&buf, data); err != nil {
		t.Fatalf("Execute show: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "site-footer") {
		t.Errorf("expected footer partial in show output, got:\n%s", got)
	}
}

func TestPartials_missingDirErrors(t *testing.T) {
	app := New(MustConfig(Config{
		BaseURL: "https://example.com",
		Secret:  []byte("16bytessecretkey"),
	}))
	app.Partials("/nonexistent-dir-that-does-not-exist")

	dir := t.TempDir()
	writeTplFile(t, dir, "list.html", `<p>list</p>`)
	writeTplFile(t, dir, "show.html", `<p>show</p>`)

	repo := NewMemoryRepo[*tdPost]()
	m := NewModule((*tdPost)(nil), Repo(repo), At("/test"), Templates(dir))
	app.Content(m)

	err := app.Run(":0")
	if err == nil {
		t.Fatal("expected error for missing partials dir, got nil")
	}
	if !strings.Contains(err.Error(), "partials directory") {
		t.Errorf("expected 'partials directory' in error, got: %v", err)
	}
}

func TestPartials_noPartialsIsNoop(t *testing.T) {
	dir := t.TempDir()
	writeTplFile(t, dir, "list.html", `<p>list</p>`)
	writeTplFile(t, dir, "show.html", `<p>show</p>`)

	m := newTplModule(t, Templates(dir))
	// No setPartials call — m.partials is nil.
	if err := m.parseTemplates(); err != nil {
		t.Fatalf("parseTemplates without partials: %v", err)
	}
	if m.tplList == nil || m.tplShow == nil {
		t.Fatal("expected both tplList and tplShow to be non-nil without partials")
	}
}

func TestPartials_mustParseTemplate_includesPartials(t *testing.T) {
	dir := t.TempDir()
	partialsDir := filepath.Join(dir, "partials")
	writePartialFile(t, partialsDir, "nav.html", `{{define "nav"}}<nav>home-nav</nav>{{end}}`)

	homePath := filepath.Join(dir, "home.html")
	if err := os.WriteFile(homePath, []byte(`{{template "nav" .}}<main>home</main>`), 0644); err != nil {
		t.Fatal(err)
	}

	app := New(MustConfig(Config{
		BaseURL: "https://example.com",
		Secret:  []byte("16bytessecretkey"),
	}))
	app.Partials(partialsDir)

	tpl := app.MustParseTemplate(homePath)
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, nil); err != nil {
		t.Fatalf("Execute home: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "home-nav") {
		t.Errorf("expected nav partial in home output, got:\n%s", got)
	}
}

func TestPartials_loadPartials_sortedAlphabetically(t *testing.T) {
	dir := t.TempDir()
	// Write in reverse order to confirm sorting.
	writePartialFile(t, dir, "z.html", `{{define "z"}}Z{{end}}`)
	writePartialFile(t, dir, "a.html", `{{define "a"}}A{{end}}`)
	writePartialFile(t, dir, "m.html", `{{define "m"}}M{{end}}`)

	srcs, err := loadPartials(dir)
	if err != nil {
		t.Fatalf("loadPartials: %v", err)
	}
	if len(srcs) != 3 {
		t.Fatalf("expected 3 partials, got %d", len(srcs))
	}
	// First src must be a.html (alphabetical).
	if !strings.Contains(srcs[0], `define "a"`) {
		t.Errorf("expected first partial to be a.html, got: %s", srcs[0])
	}
}

// — ContextFunc tests ———————————————————————————————————————————————————————

func TestContextFunc_list(t *testing.T) {
	dir := t.TempDir()
	writeTplFile(t, dir, "list.html", `{{.Extra}}`)
	writeTplFile(t, dir, "show.html", ``)

	called := false
	m := newTplModule(t,
		Templates(dir),
		ContextFunc(func(_ Context, item any) (any, error) {
			called = true
			return "nav-data", nil
		}),
	)
	if err := m.parseTemplates(); err != nil {
		t.Fatalf("parseTemplates: %v", err)
	}
	m.setSiteName("example.com")
	ctx := NewTestContext(GuestUser)

	w := httptest.NewRecorder()
	m.renderListHTML(w, ctx.Request(), ctx, nil)

	if !called {
		t.Error("ContextFunc was not called for list render")
	}
	if body := w.Body.String(); body != "nav-data" {
		t.Errorf("expected Extra = \"nav-data\", got %q", body)
	}
}

func TestContextFunc_show(t *testing.T) {
	dir := t.TempDir()
	writeTplFile(t, dir, "list.html", ``)
	writeTplFile(t, dir, "show.html", `{{.Extra}}`)

	post := &tdPost{}
	post.Slug = "test-post"
	post.Status = Published

	called := false
	m := newTplModule(t,
		Templates(dir),
		ContextFunc(func(_ Context, item any) (any, error) {
			called = true
			return "sidebar-data", nil
		}),
	)
	if err := m.parseTemplates(); err != nil {
		t.Fatalf("parseTemplates: %v", err)
	}
	m.setSiteName("example.com")
	ctx := NewTestContext(GuestUser)

	w := httptest.NewRecorder()
	m.renderShowHTML(w, ctx.Request(), ctx, post)

	if !called {
		t.Error("ContextFunc was not called for show render")
	}
	if body := w.Body.String(); body != "sidebar-data" {
		t.Errorf("expected Extra = \"sidebar-data\", got %q", body)
	}
}

func TestContextFunc_nil(t *testing.T) {
	dir := t.TempDir()
	writeTplFile(t, dir, "list.html", `{{if .Extra}}EXTRA{{else}}NONE{{end}}`)
	writeTplFile(t, dir, "show.html", ``)

	// No ContextFunc option — Extra must be nil.
	m := newTplModule(t, Templates(dir))
	if err := m.parseTemplates(); err != nil {
		t.Fatalf("parseTemplates: %v", err)
	}
	m.setSiteName("example.com")
	ctx := NewTestContext(GuestUser)

	w := httptest.NewRecorder()
	m.renderListHTML(w, ctx.Request(), ctx, nil)

	if body := w.Body.String(); body != "NONE" {
		t.Errorf("expected Extra to be nil (NONE), got %q", body)
	}
}

func TestContextFunc_error(t *testing.T) {
	dir := t.TempDir()
	writeTplFile(t, dir, "list.html", `{{if .Extra}}EXTRA{{else}}NONE{{end}}`)
	writeTplFile(t, dir, "show.html", ``)

	m := newTplModule(t,
		Templates(dir),
		ContextFunc(func(_ Context, _ any) (any, error) {
			return nil, &testContextFuncErr{}
		}),
	)
	if err := m.parseTemplates(); err != nil {
		t.Fatalf("parseTemplates: %v", err)
	}
	m.setSiteName("example.com")
	ctx := NewTestContext(GuestUser)

	w := httptest.NewRecorder()
	m.renderListHTML(w, ctx.Request(), ctx, nil)

	// Render must complete (no 500); Extra must be nil.
	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if body := w.Body.String(); body != "NONE" {
		t.Errorf("expected Extra to be nil (NONE) on ContextFunc error, got %q", body)
	}
}

// testContextFuncErr satisfies the smeldr.Error interface for tests.
type testContextFuncErr struct{}

func (e *testContextFuncErr) Error() string   { return "context func error" }
func (e *testContextFuncErr) Code() string    { return "context_func_error" }
func (e *testContextFuncErr) HTTPStatus() int { return 500 }

// ——— ListHeadFunc ————————————————————————————————————————————————————————

func TestListHeadFunc_titlePopulated(t *testing.T) {
	dir := t.TempDir()
	writeTplFile(t, dir, "list.html", `{{template "smeldr:head" .}}`)
	writeTplFile(t, dir, "show.html", ``)

	m := newTplModule(t,
		Templates(dir),
		ListHeadFunc(func(_ Context, _ []*tdPost) Head {
			return Head{Title: "All Posts"}
		}),
	)
	if err := m.parseTemplates(); err != nil {
		t.Fatalf("parseTemplates: %v", err)
	}
	m.setSiteName("example.com")
	ctx := NewTestContext(GuestUser)

	w := httptest.NewRecorder()
	m.renderListHTML(w, ctx.Request(), ctx, nil)

	body := w.Body.String()
	want := "<title>All Posts</title>"
	if !strings.Contains(body, want) {
		t.Errorf("list page title not set; want %q in output:\n%s", want, body)
	}
}

func TestListHeadFunc_absent_emptyTitle(t *testing.T) {
	dir := t.TempDir()
	writeTplFile(t, dir, "list.html", `{{template "smeldr:head" .}}`)
	writeTplFile(t, dir, "show.html", ``)

	// No ListHeadFunc — Head is zero; title element should be empty.
	m := newTplModule(t, Templates(dir))
	if err := m.parseTemplates(); err != nil {
		t.Fatalf("parseTemplates: %v", err)
	}
	m.setSiteName("example.com")
	ctx := NewTestContext(GuestUser)

	w := httptest.NewRecorder()
	m.renderListHTML(w, ctx.Request(), ctx, nil)

	body := w.Body.String()
	if !strings.Contains(body, "<title></title>") {
		t.Errorf("expected empty <title></title> when no ListHeadFunc; got:\n%s", body)
	}
}

func TestHeadFunc_showUnaffectedByListHeadFunc(t *testing.T) {
	dir := t.TempDir()
	writeTplFile(t, dir, "list.html", ``)
	writeTplFile(t, dir, "show.html", `{{template "smeldr:head" .}}`)

	post := &tdPost{}
	post.Slug = "hello"
	post.Status = Published

	m := newTplModule(t,
		Templates(dir),
		HeadFunc(func(_ Context, p *tdPost) Head {
			return Head{Title: "Show: " + p.Slug}
		}),
		ListHeadFunc(func(_ Context, _ []*tdPost) Head {
			return Head{Title: "list title"}
		}),
	)
	if err := m.parseTemplates(); err != nil {
		t.Fatalf("parseTemplates: %v", err)
	}
	m.setSiteName("example.com")
	ctx := NewTestContext(GuestUser)

	w := httptest.NewRecorder()
	m.renderShowHTML(w, ctx.Request(), ctx, post)

	body := w.Body.String()
	want := "<title>Show: hello</title>"
	if !strings.Contains(body, want) {
		t.Errorf("HeadFunc on show broken by ListHeadFunc; want %q in:\n%s", want, body)
	}
}
