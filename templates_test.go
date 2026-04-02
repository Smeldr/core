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
	if err := tpl.ExecuteTemplate(&buf, "forge:head", TemplateData[any]{Head: h}); err != nil {
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
			if err := tpl.ExecuteTemplate(&buf, "forge:head", TemplateData[any]{Head: tc.head}); err != nil {
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
