package forge

import (
	"html/template"
	"strings"
	"testing"
	"time"
)

// ——— Excerpt ——————————————————————————————————————————————————————————————

func TestExcerpt(t *testing.T) {
	tests := []struct {
		name   string
		text   string
		maxLen int
		want   string
	}{
		{"empty string", "", 160, ""},
		{"shorter than max", "Hello world", 160, "Hello world"},
		{"exact length", "Hello", 5, "Hello"},
		{"truncate at clean word end", "Hello world foo", 11, "Hello world…"},
		{"truncate mid-word falls back", "Hello world foo", 8, "Hello…"},
		{"no space falls back to hard cut", "Helloworld!", 5, "Hello…"},
		{"leading trailing whitespace stripped", "  Hello world  ", 5, "Hello…"},
		{"unicode multibyte chars", "日本語テスト長文", 4, "日本語テ…"},
		{"zero maxLen", "Hello", 0, "…"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Excerpt(tt.text, tt.maxLen)
			if got != tt.want {
				t.Errorf("Excerpt(%q, %d) = %q; want %q", tt.text, tt.maxLen, got, tt.want)
			}
		})
	}
}

func BenchmarkExcerpt(b *testing.B) {
	text := "The quick brown fox jumped over the lazy dog and then ran away into the forest where nobody could find him ever again."
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		Excerpt(text, 50)
	}
}

// ——— URL ——————————————————————————————————————————————————————————————————

func TestURL(t *testing.T) {
	tests := []struct {
		name  string
		parts []string
		want  string
	}{
		{"single segment", []string{"/posts"}, "/posts"},
		{"two segments", []string{"/posts", "my-slug"}, "/posts/my-slug"},
		{"trailing slash on first part", []string{"/posts/", "my-slug"}, "/posts/my-slug"},
		{"double slash collapsed", []string{"/posts//", "slug"}, "/posts/slug"},
		{"no leading slash added", []string{"posts", "slug"}, "/posts/slug"},
		{"trailing slash trimmed", []string{"/posts/"}, "/posts"},
		{"root slash preserved", []string{"/"}, "/"},
		{"empty parts", []string{}, "/"},
		{"three parts", []string{"/a", "b", "c"}, "/a/b/c"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := URL(tt.parts...)
			if got != tt.want {
				t.Errorf("URL(%v) = %q; want %q", tt.parts, got, tt.want)
			}
		})
	}
}

// ——— AbsURL ————————————————————————————————————————————————————————————

func TestAbsURL(t *testing.T) {
	tests := []struct {
		name string
		base string
		path string
		want string
	}{
		{"trailing slash on base", "https://example.com/", "/posts/my-slug", "https://example.com/posts/my-slug"},
		{"no trailing slash on base", "https://example.com", "/posts/my-slug", "https://example.com/posts/my-slug"},
		{"path with duplicate slashes", "https://example.com", "/posts//slug", "https://example.com/posts/slug"},
		{"empty path becomes root", "https://example.com", "", "https://example.com/"},
		{"path is just slash", "https://example.com", "/", "https://example.com/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := AbsURL(tt.base, tt.path)
			if got != tt.want {
				t.Errorf("AbsURL(%q, %q) = %q; want %q", tt.base, tt.path, got, tt.want)
			}
		})
	}
}

// ——— Crumbs ——————————————————————————————————————————————————————————————

func TestCrumbs(t *testing.T) {
	t.Run("constructs slice", func(t *testing.T) {
		got := Crumbs(
			Crumb("Home", "/"),
			Crumb("Posts", "/posts"),
			Crumb("Hello", "/posts/hello"),
		)
		if len(got) != 3 {
			t.Fatalf("len = %d; want 3", len(got))
		}
		if got[0].Label != "Home" || got[0].URL != "/" {
			t.Errorf("got[0] = %+v; want {Home /}", got[0])
		}
		if got[2].Label != "Hello" || got[2].URL != "/posts/hello" {
			t.Errorf("got[2] = %+v; want {Hello /posts/hello}", got[2])
		}
	})

	t.Run("zero Head.Breadcrumbs is nil", func(t *testing.T) {
		var h Head
		if h.Breadcrumbs != nil {
			t.Error("zero Head.Breadcrumbs should be nil")
		}
	})
}

// ——— Head zero value —————————————————————————————————————————————————————

func TestHead_zeroValueSafe(t *testing.T) {
	var h Head
	// Access all fields on a zero Head — must not panic.
	_ = h.Title
	_ = h.Description
	_ = h.Author
	_ = h.Published.IsZero()
	_ = h.Modified.IsZero()
	_ = h.Image.URL
	_ = h.Image.Alt
	_ = h.Image.Width
	_ = h.Image.Height
	_ = h.Type
	_ = h.Canonical
	_ = h.Breadcrumbs
	_ = h.Alternates
	_ = h.NoIndex
}

func TestHead_timeFields(t *testing.T) {
	h := Head{}
	if !h.Published.IsZero() {
		t.Error("Head.Published should be zero by default")
	}
	if !h.Modified.IsZero() {
		t.Error("Head.Modified should be zero by default")
	}
	h.Published = time.Date(2026, 3, 3, 0, 0, 0, 0, time.UTC)
	if h.Published.IsZero() {
		t.Error("Head.Published should not be zero after being set")
	}
}

// ——— Image ———————————————————————————————————————————————————————————————

func TestImage_zeroValueSafe(t *testing.T) {
	var img Image
	if img.URL != "" || img.Alt != "" || img.Width != 0 || img.Height != 0 {
		t.Error("Image zero value should have all empty/zero fields")
	}
}

// ——— Alternate ———————————————————————————————————————————————————————————

func TestAlternate_zeroValueSafe(t *testing.T) {
	var a Alternate
	_ = a.Locale
	_ = a.URL
}

// ——— Rich-result constants ———————————————————————————————————————————————

func TestRichResultConstants(t *testing.T) {
	constants := map[string]string{
		"Article":      Article,
		"Product":      Product,
		"FAQPage":      FAQPage,
		"HowTo":        HowTo,
		"Event":        Event,
		"Recipe":       Recipe,
		"Review":       Review,
		"Organization": Organization,
	}
	for name, val := range constants {
		if val == "" {
			t.Errorf("constant %s is empty", name)
		}
		if val != name {
			t.Errorf("constant %s = %q; want %q", name, val, name)
		}
	}
}

// ——— HeadFunc option ————————————————————————————————————————————————————

func TestHeadFunc_isOption(t *testing.T) {
	// Compile-time check: HeadFunc returns an Option.
	type testContent struct{ Node }
	var _ Option = HeadFunc(func(_ Context, _ *testContent) Head {
		return Head{Title: "test"}
	})
}

func TestHeadFunc_storedOnModule(t *testing.T) {
	type Post struct{ Node }
	m := NewModule((*Post)(nil),
		Repo(NewMemoryRepo[*Post]()),
		HeadFunc(func(_ Context, p *Post) Head {
			return Head{Title: "overridden"}
		}),
	)
	if m.headFunc == nil {
		t.Error("headFunc should be set on Module after HeadFunc option")
	}
	fn, ok := m.headFunc.(func(Context, *Post) Head)
	if !ok {
		t.Fatalf("headFunc has wrong type: %T", m.headFunc)
	}
	h := fn(NewTestContext(GuestUser), &Post{})
	if h.Title != "overridden" {
		t.Errorf("headFunc returned Head.Title = %q; want %q", h.Title, "overridden")
	}
}

// ——— HeadAssets ———————————————————————————————————————————————————————————

// renderHeadAssets is a test helper that executes forgeHeadTmpl with a
// TemplateData carrying the given HeadAssets and returns the output string.
func renderHeadAssets(t *testing.T, ha *HeadAssets) string {
	t.Helper()
	tpl := template.Must(template.New("test").Funcs(TemplateFuncMap()).Parse(forgeHeadTmpl))
	data := TemplateData[string]{HeadAssets: ha}
	var buf strings.Builder
	if err := tpl.ExecuteTemplate(&buf, "forge:head", data); err != nil {
		t.Fatalf("template execution: %v", err)
	}
	return buf.String()
}

func TestHeadAssets_stylesheets(t *testing.T) {
	ha := &HeadAssets{
		Stylesheets: []string{
			"https://fonts.googleapis.com/css2?family=Inter&display=swap",
			"/static/app.css",
		},
	}
	got := renderHeadAssets(t, ha)
	if !strings.Contains(got, `<link rel="stylesheet" href="https://fonts.googleapis.com/css2?family=Inter&amp;display=swap">`) {
		t.Errorf("missing Google Fonts stylesheet link in output:\n%s", got)
	}
	if !strings.Contains(got, `<link rel="stylesheet" href="/static/app.css">`) {
		t.Errorf("missing /static/app.css stylesheet link in output:\n%s", got)
	}
}

func TestHeadAssets_favicons_full(t *testing.T) {
	ha := &HeadAssets{
		Favicons: []FaviconLink{
			{Rel: "icon", Type: "image/png", Sizes: "32x32", Href: "/favicon-32.png"},
		},
	}
	got := renderHeadAssets(t, ha)
	want := `<link rel="icon" type="image/png" sizes="32x32" href="/favicon-32.png">`
	if !strings.Contains(got, want) {
		t.Errorf("missing full favicon link in output:\n%s", got)
	}
}

func TestHeadAssets_favicons_minimal(t *testing.T) {
	ha := &HeadAssets{
		Favicons: []FaviconLink{
			{Rel: "apple-touch-icon", Href: "/apple-touch-icon.png"},
		},
	}
	got := renderHeadAssets(t, ha)
	if !strings.Contains(got, `<link rel="apple-touch-icon" href="/apple-touch-icon.png">`) {
		t.Errorf("missing minimal favicon link in output:\n%s", got)
	}
	if strings.Contains(got, "type=") || strings.Contains(got, "sizes=") {
		t.Errorf("type and sizes attributes should be omitted when empty:\n%s", got)
	}
}

func TestHeadAssets_scripts_external(t *testing.T) {
	ha := &HeadAssets{
		Scripts: []ScriptTag{
			{Src: "/static/app.js", Defer: true},
			{Src: "/static/analytics.js", Async: true},
		},
	}
	got := renderHeadAssets(t, ha)
	if !strings.Contains(got, `<script src="/static/app.js" defer></script>`) {
		t.Errorf("missing deferred script in output:\n%s", got)
	}
	if !strings.Contains(got, `<script src="/static/analytics.js" async></script>`) {
		t.Errorf("missing async script in output:\n%s", got)
	}
}

func TestHeadAssets_scripts_inline(t *testing.T) {
	ha := &HeadAssets{
		Stylesheets: []string{"/static/app.css"},
		Scripts:     []ScriptTag{{Body: template.JS("console.log('hi')")}},
	}
	got := renderHeadAssets(t, ha)

	ssIdx := strings.Index(got, `<link rel="stylesheet"`)
	scriptIdx := strings.Index(got, `<script>console.log`)
	if ssIdx < 0 {
		t.Fatalf("stylesheet link not found in output:\n%s", got)
	}
	if scriptIdx < 0 {
		t.Fatalf("inline script not found in output:\n%s", got)
	}
	if scriptIdx < ssIdx {
		t.Errorf("inline script appears before stylesheet (index %d < %d); want scripts after stylesheets:\n%s",
			scriptIdx, ssIdx, got)
	}
}

func TestHeadAssets_nil_isNoop(t *testing.T) {
	baseline := renderHeadAssets(t, nil)
	withNil := renderHeadAssets(t, nil)
	if baseline != withNil {
		t.Error("nil HeadAssets should produce identical output on every call")
	}
	if strings.Contains(baseline, "<link") || strings.Contains(baseline, "<script") {
		t.Errorf("nil HeadAssets emitted unexpected link/script tags:\n%s", baseline)
	}
}
