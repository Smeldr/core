package forge

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
	if tplList.Lookup("forge:head") == nil {
		t.Error("forge:head not registered in list template set")
	}
	if tplShow == nil {
		t.Fatal("tplShow is nil after parseTemplates")
	}
	if tplShow.Lookup("forge:head") == nil {
		t.Error("forge:head not registered in show template set")
	}
}

func TestTemplates_noIndexMeta(t *testing.T) {
	// Execute forgeHeadTmpl directly — no module needed. FuncMap required
	// because forge:head uses forge_rfc3339 for article:published_time.
	tpl := template.Must(template.New("test").Funcs(TemplateFuncMap()).Parse(forgeHeadTmpl))
	var buf bytes.Buffer
	h := Head{Title: "Test Page", NoIndex: true}
	if err := tpl.ExecuteTemplate(&buf, "forge:head", TemplateData[any]{PageHead: PageHead{Head: h}}); err != nil {
		t.Fatalf("ExecuteTemplate: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "noindex") {
		t.Errorf("expected noindex in forge:head output, got:\n%s", got)
	}
	if !strings.Contains(got, "Test Page") {
		t.Errorf("expected title in forge:head output, got:\n%s", got)
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
	tpl := template.Must(template.New("test").Funcs(TemplateFuncMap()).Parse(forgeHeadTmpl))

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
			if err := tpl.ExecuteTemplate(&buf, "forge:head", TemplateData[any]{PageHead: PageHead{Head: tc.head}}); err != nil {
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
