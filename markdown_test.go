package forge

import (
	"html/template"
	"testing"
)

func TestRenderMarkdown(t *testing.T) {
	t.Run("headings h1 through h6", func(t *testing.T) {
		cases := []struct {
			input string
			want  string
		}{
			{"# H1", "<h1>H1</h1>"},
			{"## H2", "<h2>H2</h2>"},
			{"### H3", "<h3>H3</h3>"},
			{"#### H4", "<h4>H4</h4>"},
			{"##### H5", "<h5>H5</h5>"},
			{"###### H6", "<h6>H6</h6>"},
		}
		for _, tc := range cases {
			got := string(renderMarkdown(tc.input))
			if got != tc.want {
				t.Errorf("renderMarkdown(%q) = %q; want %q", tc.input, got, tc.want)
			}
		}
	})

	t.Run("fenced code block no lang", func(t *testing.T) {
		input := "```\nfoo()\n```"
		want := "<pre><code>foo()</code></pre>"
		got := string(renderMarkdown(input))
		if got != want {
			t.Errorf("got %q; want %q", got, want)
		}
	})

	t.Run("fenced code block with lang", func(t *testing.T) {
		input := "```go\nfmt.Println(\"hi\")\n```"
		want := "<pre><code class=\"language-go\">fmt.Println(&#34;hi&#34;)</code></pre>"
		got := string(renderMarkdown(input))
		if got != want {
			t.Errorf("got %q; want %q", got, want)
		}
	})

	t.Run("fenced code block lang with XSS attempt", func(t *testing.T) {
		input := "```<script>\nalert(1)\n```"
		got := string(renderMarkdown(input))
		if containsUnescaped(got, "<script>") {
			t.Errorf("unescaped <script> in output: %q", got)
		}
	})

	t.Run("unordered list", func(t *testing.T) {
		input := "- alpha\n- beta\n- gamma"
		want := "<ul>\n<li>alpha</li>\n<li>beta</li>\n<li>gamma</li>\n</ul>"
		got := string(renderMarkdown(input))
		if got != want {
			t.Errorf("got %q; want %q", got, want)
		}
	})

	t.Run("table with header and body", func(t *testing.T) {
		input := "| A | B |\n| --- | --- |\n| 1 | 2 |\n| 3 | 4 |"
		got := string(renderMarkdown(input))
		wantFragments := []string{
			"<table>", "<thead>", "<th>A</th>", "<th>B</th>", "</thead>",
			"<tbody>", "<td>1</td>", "<td>2</td>", "<td>3</td>", "<td>4</td>", "</tbody>", "</table>",
		}
		for _, frag := range wantFragments {
			if !contains(got, frag) {
				t.Errorf("missing %q in output %q", frag, got)
			}
		}
	})

	t.Run("bold", func(t *testing.T) {
		got := string(renderMarkdown("Hello **world** end"))
		want := "<p>Hello <strong>world</strong> end</p>"
		if got != want {
			t.Errorf("got %q; want %q", got, want)
		}
	})

	t.Run("inline code", func(t *testing.T) {
		got := string(renderMarkdown("Use `fmt.Println` here"))
		want := "<p>Use <code>fmt.Println</code> here</p>"
		if got != want {
			t.Errorf("got %q; want %q", got, want)
		}
	})

	t.Run("paragraphs separated by blank line", func(t *testing.T) {
		input := "First para.\n\nSecond para."
		want := "<p>First para.</p>\n<p>Second para.</p>"
		got := string(renderMarkdown(input))
		if got != want {
			t.Errorf("got %q; want %q", got, want)
		}
	})

	t.Run("multi-line paragraph joined with space", func(t *testing.T) {
		input := "line one\nline two"
		want := "<p>line one line two</p>"
		got := string(renderMarkdown(input))
		if got != want {
			t.Errorf("got %q; want %q", got, want)
		}
	})

	t.Run("horizontal rule", func(t *testing.T) {
		got := string(renderMarkdown("---"))
		want := "<hr>"
		if got != want {
			t.Errorf("got %q; want %q", got, want)
		}
	})

	t.Run("XSS in paragraph content", func(t *testing.T) {
		got := string(renderMarkdown("<script>alert(1)</script>"))
		if containsUnescaped(got, "<script>") {
			t.Errorf("unescaped <script> tag in output: %q", got)
		}
	})

	t.Run("XSS in bold content", func(t *testing.T) {
		got := string(renderMarkdown("**<script>xss</script>**"))
		if containsUnescaped(got, "<script>") {
			t.Errorf("unescaped <script> in bold output: %q", got)
		}
	})

	t.Run("XSS in inline code", func(t *testing.T) {
		got := string(renderMarkdown("`<img src=x onerror=alert(1)>`"))
		if containsUnescaped(got, "<img") {
			t.Errorf("unescaped <img> in code output: %q", got)
		}
	})

	t.Run("XSS in table cell", func(t *testing.T) {
		input := "| A |\n| --- |\n| <script>bad</script> |"
		got := string(renderMarkdown(input))
		if containsUnescaped(got, "<script>") {
			t.Errorf("unescaped <script> in table cell output: %q", got)
		}
	})

	t.Run("returns template.HTML type", func(t *testing.T) {
		var _ template.HTML = renderMarkdown("hello")
	})

	t.Run("TemplateFuncMap contains markdown key", func(t *testing.T) {
		fm := TemplateFuncMap()
		if _, ok := fm["markdown"]; !ok {
			t.Error("TemplateFuncMap missing 'markdown' key")
		}
	})

	t.Run("empty input", func(t *testing.T) {
		got := string(renderMarkdown(""))
		if got != "" {
			t.Errorf("empty input: got %q; want \"\"", got)
		}
	})

	t.Run("heading with inline bold", func(t *testing.T) {
		got := string(renderMarkdown("## Hello **world**"))
		want := "<h2>Hello <strong>world</strong></h2>"
		if got != want {
			t.Errorf("got %q; want %q", got, want)
		}
	})

	t.Run("list items with inline code", func(t *testing.T) {
		input := "- use `fmt.Println`\n- use `os.Exit`"
		got := string(renderMarkdown(input))
		if !contains(got, "<code>fmt.Println</code>") || !contains(got, "<code>os.Exit</code>") {
			t.Errorf("missing inline code in list: %q", got)
		}
	})

	t.Run("hr between paragraphs", func(t *testing.T) {
		input := "before\n\n---\n\nafter"
		got := string(renderMarkdown(input))
		want := "<p>before</p>\n<hr>\n<p>after</p>"
		if got != want {
			t.Errorf("got %q; want %q", got, want)
		}
	})

	t.Run("inline link https", func(t *testing.T) {
		got := string(renderMarkdown("[Read more](https://example.com)"))
		want := `<p><a href="https://example.com">Read more</a></p>`
		if got != want {
			t.Errorf("got %q; want %q", got, want)
		}
	})

	t.Run("inline link relative", func(t *testing.T) {
		got := string(renderMarkdown("[Docs](/docs)"))
		want := `<p><a href="/docs">Docs</a></p>`
		if got != want {
			t.Errorf("got %q; want %q", got, want)
		}
	})

	t.Run("inline link rejected scheme javascript", func(t *testing.T) {
		got := string(renderMarkdown("[bad](javascript:alert(1))"))
		if containsStr(got, `href="javascript:`) {
			t.Errorf("javascript: scheme must not appear in href: %q", got)
		}
		if !containsStr(got, "[bad]") {
			t.Errorf("rejected link text should be emitted as literal: %q", got)
		}
	})

	t.Run("inline link rejected scheme data", func(t *testing.T) {
		got := string(renderMarkdown("[x](data:text/html,<h1>xss</h1>)"))
		if containsStr(got, `href="data:`) {
			t.Errorf("data: scheme must not appear in href: %q", got)
		}
	})

	t.Run("inline link next to bold", func(t *testing.T) {
		got := string(renderMarkdown("**bold** and [link](https://example.com)"))
		if !containsStr(got, "<strong>bold</strong>") {
			t.Errorf("expected <strong> in output: %q", got)
		}
		if !containsStr(got, `<a href="https://example.com">link</a>`) {
			t.Errorf("expected <a> in output: %q", got)
		}
	})
}

func TestIsTableSep(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{"| --- | --- |", true},
		{"| :--- | ---: |", true},
		{"| --- |", true},
		{"|---|---|", true},
		{"| foo | bar |", false},
		{"---", false},
	}
	for _, tc := range cases {
		got := isTableSep(tc.input)
		if got != tc.want {
			t.Errorf("isTableSep(%q) = %v; want %v", tc.input, got, tc.want)
		}
	}
}

func TestParseTableRow(t *testing.T) {
	cells := parseTableRow("| A | B | C |")
	if len(cells) != 3 {
		t.Fatalf("expected 3 cells, got %d: %v", len(cells), cells)
	}
}

// contains reports whether sub is a substring of s.
func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || containsStr(s, sub))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// containsUnescaped reports whether s contains the literal (unescaped) tag.
func containsUnescaped(s, tag string) bool {
	return containsStr(s, tag)
}
