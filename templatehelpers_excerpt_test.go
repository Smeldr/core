package smeldr

import (
	"encoding/json"
	"html/template"
	"strings"
	"testing"
)

func execExcerptTemplate(t *testing.T, src, body string) string {
	t.Helper()
	tpl, err := template.New("x").Funcs(TemplateFuncMap()).Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	if err := tpl.Execute(&b, map[string]string{"Body": body}); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// smeldr_excerpt is escaped by html/template in element text.
func TestSmeldrExcerpt_EscapedInHTML(t *testing.T) {
	out := execExcerptTemplate(t, `<p>{{.Body | smeldr_excerpt 200}}</p>`,
		"<script>alert(1)</script> and <img src=x onerror=alert(1)> & more")
	for _, bad := range []string{"<script", "<img"} {
		if strings.Contains(out, bad) {
			t.Errorf("unescaped %q in %q", bad, out)
		}
	}
	for _, want := range []string{"&lt;script&gt;", "&lt;img", "&amp; more"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %q", want, out)
		}
	}
}

// In a JSON-LD string, the excerpt is escaped as a JS string: the JSON parses
// and the description round-trips, with no way to end the script element.
func TestSmeldrExcerpt_EscapedInJSONLD(t *testing.T) {
	body := `He said "hi" </script><script>alert(1)</script>`
	out := execExcerptTemplate(t, `<script type="application/ld+json">{"description": "{{.Body | smeldr_excerpt 200}}"}</script>`, body)
	inner := strings.TrimSuffix(strings.TrimPrefix(out, `<script type="application/ld+json">`), `</script>`)
	if strings.Contains(inner, "</script") || strings.Contains(inner, "<script") {
		t.Fatalf("script tag inside the JSON-LD string: %q", out)
	}
	var v struct {
		Description string `json:"description"`
	}
	if err := json.Unmarshal([]byte(inner), &v); err != nil {
		t.Fatalf("JSON-LD does not parse: %v in %q", err, inner)
	}
	if v.Description != body {
		t.Errorf("description = %q, want %q", v.Description, body)
	}
}

func TestSmeldrExcerpt_StripsMarkdownToOneLine(t *testing.T) {
	got := string(smeldrExcerpt(200, "# Title\n\n**bold** and [a link](/x) and snake_case\n\n- item"))
	if want := "Title bold and a link and snake_case item"; got != want {
		t.Errorf("excerpt = %q, want %q", got, want)
	}
	if strings.ContainsAny(got, "\n\r") {
		t.Errorf("excerpt has a line break: %q", got)
	}
}

func TestSmeldrExcerpt_FuncMapType(t *testing.T) {
	if _, ok := TemplateFuncMap()["smeldr_excerpt"].(func(int, string) string); !ok {
		t.Errorf("smeldr_excerpt is %T, want func(int, string) string", TemplateFuncMap()["smeldr_excerpt"])
	}
}
