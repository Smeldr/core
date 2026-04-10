package forge

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestForgeDate_formatted(t *testing.T) {
	ts := time.Date(2026, 3, 5, 0, 0, 0, 0, time.UTC)
	got := forgeDate(ts)
	want := "5 March 2026"
	if got != want {
		t.Errorf("forgeDate = %q, want %q", got, want)
	}
}

func TestForgeDate_zero(t *testing.T) {
	got := forgeDate(time.Time{})
	if got != "" {
		t.Errorf("forgeDate(zero) = %q, want empty string", got)
	}
}

func TestForgeMeta_withSchema(t *testing.T) {
	h := Head{Title: "Test Article", Type: Article}
	got := string(forgeMeta(h, nil))
	if !strings.Contains(got, `<script type="application/ld+json">`) {
		t.Errorf("forgeMeta(Article) should contain JSON-LD script tag, got: %s", got)
	}
	if !strings.Contains(got, "Article") {
		t.Errorf("forgeMeta(Article) should contain schema type, got: %s", got)
	}
}

func TestForgeMeta_noSchema(t *testing.T) {
	got := string(forgeMeta(Head{}, nil))
	if got != "" {
		t.Errorf("forgeMeta(empty Head) = %q, want empty string", got)
	}
}

func TestForgeMarkdown_heading(t *testing.T) {
	got := string(forgeMarkdown("# Title"))
	if got != "<h1>Title</h1>" {
		t.Errorf("heading: got %q, want %q", got, "<h1>Title</h1>")
	}
}

func TestForgeMarkdown_bold(t *testing.T) {
	got := string(forgeMarkdown("**important**"))
	want := "<p><strong>important</strong></p>"
	if got != want {
		t.Errorf("bold: got %q, want %q", got, want)
	}
}

func TestForgeMarkdown_link(t *testing.T) {
	// renderMarkdown now supports [text](url) inline links.
	got := string(forgeMarkdown("[click here](https://example.com)"))
	want := `<p><a href="https://example.com">click here</a></p>`
	if got != want {
		t.Errorf("link: got %q, want %q", got, want)
	}
}

func TestForgeMarkdown_list(t *testing.T) {
	got := string(forgeMarkdown("- alpha\n- beta"))
	want := "<ul>\n<li>alpha</li>\n<li>beta</li>\n</ul>"
	if got != want {
		t.Errorf("list: got %q, want %q", got, want)
	}
}

func TestForgeMarkdown_paragraph(t *testing.T) {
	input := "first paragraph\n\nsecond paragraph"
	got := string(forgeMarkdown(input))
	if !strings.Contains(got, "<p>first paragraph</p>") {
		t.Errorf("paragraph: missing first <p>, got: %s", got)
	}
	if !strings.Contains(got, "<p>second paragraph</p>") {
		t.Errorf("paragraph: missing second <p>, got: %s", got)
	}
}

func TestForgeMarkdown_fencedCode(t *testing.T) {
	input := "intro\n\n```go\nfmt.Println(\"hello\")\n```\n\noutro"
	got := string(forgeMarkdown(input))
	if !strings.Contains(got, "<pre><code") {
		t.Errorf("fencedCode: missing <pre><code, got: %s", got)
	}
	if !strings.Contains(got, "fmt.Println") {
		t.Errorf("fencedCode: missing code body, got: %s", got)
	}
	if strings.Contains(got, "```") {
		t.Errorf("fencedCode: raw fence leaked into output, got: %s", got)
	}
	if !strings.Contains(got, "<p>intro</p>") {
		t.Errorf("fencedCode: missing intro paragraph, got: %s", got)
	}
	if !strings.Contains(got, "<p>outro</p>") {
		t.Errorf("fencedCode: missing outro paragraph, got: %s", got)
	}
}

func TestForgeMarkdown_fencedCodeHTMLEscape(t *testing.T) {
	input := "```\n<script>alert(1)</script>\n```"
	got := string(forgeMarkdown(input))
	if strings.Contains(got, "<script>") {
		t.Errorf("fencedCode: raw <script> not escaped, got: %s", got)
	}
	if !strings.Contains(got, "&lt;script&gt;") {
		t.Errorf("fencedCode: expected escaped &lt;script&gt;, got: %s", got)
	}
}

func TestForgeHTML(t *testing.T) {
	t.Run("passthrough", func(t *testing.T) {
		input := "<strong>bold</strong>"
		got := forgeHTML(input)
		if string(got) != input {
			t.Errorf("got %q, want %q", got, input)
		}
	})
	t.Run("empty", func(t *testing.T) {
		got := forgeHTML("")
		if got != "" {
			t.Errorf("got %q, want empty string", got)
		}
	})
	t.Run("not_escaped", func(t *testing.T) {
		// forge_html is a trusted passthrough — the caller is responsible
		// for ensuring the string is safe. Verify no escaping occurs.
		input := "<script>alert(1)</script>"
		got := forgeHTML(input)
		if string(got) != input {
			t.Errorf("got %q, want %q (should not be escaped)", got, input)
		}
	})
}

func TestForgeExcerpt_pipeline(t *testing.T) {
	body := "The quick brown fox jumps over the lazy dog and then some more words follow here"
	got := string(forgeExcerpt(20, body))
	if len([]rune(got)) > 25 { // 20 + "…" + some tolerance
		t.Errorf("excerpt too long: %q", got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("excerpt should end with ellipsis, got: %q", got)
	}
}

func TestForgeCSRFToken_present(t *testing.T) {
	r2 := httptest.NewRequest("GET", "/", nil)
	r2.Header.Set("Cookie", CSRFCookieName+"=test-token-abc")

	got := string(forgeCSRFToken(r2))
	if !strings.Contains(got, `<input type="hidden"`) {
		t.Errorf("expected hidden input, got: %s", got)
	}
	if !strings.Contains(got, "test-token-abc") {
		t.Errorf("expected token value in input, got: %s", got)
	}
	if !strings.Contains(got, `name="csrf_token"`) {
		t.Errorf("expected name=csrf_token, got: %s", got)
	}
}

func TestForgeCSRFToken_absent(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	got := forgeCSRFToken(r)
	if got != "" {
		t.Errorf("absent cookie: expected empty string, got: %q", got)
	}
}

func TestTemplateFuncMap_keys(t *testing.T) {
	fm := TemplateFuncMap()
	required := []string{
		"forge_meta",
		"forge_date",
		"forge_rfc3339",
		"forge_markdown",
		"forge_html",
		"forge_excerpt",
		"forge_csrf_token",
		"forge_llms_entries",
		"markdown",
	}
	for _, key := range required {
		if _, ok := fm[key]; !ok {
			t.Errorf("TemplateFuncMap missing key %q", key)
		}
	}
	if len(fm) != len(required) {
		t.Errorf("TemplateFuncMap has %d keys, want %d", len(fm), len(required))
	}
}

// TestForgeLLMSEntries verifies that the forge_llms_entries template function
// renders LLMsTemplateData into a markdown link list and returns empty for
// unknown or nil inputs (exercises the forgeLLMsEntries helper directly).
func TestForgeLLMSEntries(t *testing.T) {
	td := LLMsTemplateData{
		Entries: []LLMsEntry{
			{Title: "Post One", URL: "/posts/one", Summary: "A summary."},
			{Title: "Post Two", URL: "/posts/two"}, // no summary
		},
	}
	got := forgeLLMsEntries(td)
	if !strings.Contains(string(got), "Post One") {
		t.Errorf("output missing 'Post One': %q", got)
	}
	if !strings.Contains(string(got), "/posts/one") {
		t.Errorf("output missing URL '/posts/one': %q", got)
	}
	if !strings.Contains(string(got), "A summary.") {
		t.Errorf("output missing summary: %q", got)
	}
	if !strings.Contains(string(got), "Post Two") {
		t.Errorf("output missing 'Post Two' (no-summary entry): %q", got)
	}
	// Pointer form should also work.
	gotPtr := forgeLLMsEntries(&td)
	if got != gotPtr {
		t.Errorf("pointer form produced different output: %q vs %q", got, gotPtr)
	}
	// Unknown type returns empty.
	if got2 := forgeLLMsEntries("not-a-td"); got2 != "" {
		t.Errorf("unknown type: got %q; want empty string", got2)
	}
	// Nil pointer returns empty.
	if got3 := forgeLLMsEntries((*LLMsTemplateData)(nil)); got3 != "" {
		t.Errorf("nil pointer: got %q; want empty string", got3)
	}
}

var benchMarkdownBody = strings.Repeat(
	"# Section Title\n\nThis is a paragraph with **bold** and *italic* text and a "+
		"[link](https://example.com). It also has `code` inline.\n\n"+
		"- Item one\n- Item two\n- Item three\n\n",
	20) // ~500 words

func BenchmarkForgeMarkdown(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = forgeMarkdown(benchMarkdownBody)
	}
}
