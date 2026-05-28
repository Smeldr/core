package smeldr

import (
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"
)

// — Template helpers ———————————————————————————————————————————————————————

// smeldrMeta returns the JSON-LD <script> block for head and content as safe
// HTML. When the Head has no Type or the content type does not implement the
// matching schema provider interface, smeldrMeta returns an empty string.
//
// Template usage:
//
//	{{smeldr_meta .Head .Content}}
func smeldrMeta(head Head, content any) template.HTML {
	return template.HTML(SchemaFor(head, content))
}

// smeldrRFC3339 formats t as an RFC 3339 / ISO 8601 timestamp
// ("2006-01-02T15:04:05Z07:00"). Returns an empty string when t is the zero
// value. Used by smeldr:head for article:published_time and feed item pubDate.
//
// Template usage:
//
//	{{smeldr_rfc3339 .Head.Published}}
func smeldrRFC3339(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

// smeldrDate formats t using the "2 January 2006" layout. Returns an empty
// string when t is the zero value.
//
// Template usage:
//
//	{{.Content.PublishedAt | smeldr_date}}
func smeldrDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2 January 2006")
}

// smeldrMarkdown converts Markdown to safe HTML and returns it as [template.HTML]
// so the template engine does not double-escape it. Delegates to [renderMarkdown].
//
// Supported syntax:
//   - `# Heading` through `###### Heading` → <h1>–<h6>
//   - ` ```lang … ``` ` fenced code blocks → <pre><code class="language-lang">
//   - `- item` → <ul><li>
//   - `| col |` tables with a `| --- |` separator row → <table>
//   - `**text**` → <strong>
//   - " `code` " → <code>
//   - Blank-line-separated paragraphs → <p>
//   - Standalone `---` → <hr>
//
// All content is HTML-entity-escaped before tag wrapping (XSS-safe).
//
// Template usage:
//
//	{{.Content.Body | smeldr_markdown}}
func smeldrMarkdown(s string) template.HTML {
	return renderMarkdown(s)
}

// smeldrHTML wraps s as [template.HTML], bypassing Go's automatic HTML escaping.
// Use only for trusted content — user-supplied strings must never be passed to
// smeldr_html without prior sanitisation.
//
// Template usage:
//
//	{{.Content.Embed | smeldr_html}}
func smeldrHTML(s string) template.HTML {
	return template.HTML(s)
}

// smeldrExcerpt returns a plain-text excerpt of s truncated at the last word
// boundary within maxLen runes, with a Unicode ellipsis appended when truncated.
// Wraps [Excerpt].
//
// In templates, maxLen is passed as an explicit argument and s arrives via the
// pipeline:
//
//	{{.Content.Body | smeldr_excerpt 120}}
func smeldrExcerpt(maxLen int, s string) template.HTML {
	return template.HTML(Excerpt(s, maxLen))
}

// smeldrCSRFToken reads the CSRF cookie from r and returns an HTML hidden input
// field containing the token. Returns an empty string when the cookie is absent.
//
// Template usage:
//
//	{{smeldr_csrf_token .Request}}
func smeldrCSRFToken(r *http.Request) template.HTML {
	cookie, err := r.Cookie(CSRFCookieName)
	if err != nil {
		return ""
	}
	return template.HTML(fmt.Sprintf(
		`<input type="hidden" name="csrf_token" value="%s">`,
		template.HTMLEscapeString(cookie.Value),
	))
}

// smeldrLLMsEntries formats the entries from data for use in custom llms.txt
// templates. data must be a [LLMsTemplateData] value or pointer; returns an
// empty string for any other type.
//
// Each entry is formatted using the llmstxt.org compact convention:
//
//   - [Title](URL): Summary
//
// Template usage:
//
//	{{smeldr_llms_entries .}}
func smeldrLLMsEntries(data any) template.HTML {
	var td LLMsTemplateData
	switch v := data.(type) {
	case LLMsTemplateData:
		td = v
	case *LLMsTemplateData:
		if v == nil {
			return ""
		}
		td = *v
	default:
		return ""
	}
	var buf strings.Builder
	for _, e := range td.Entries {
		if e.Summary != "" {
			fmt.Fprintf(&buf, "- [%s](%s): %s\n", e.Title, e.URL, e.Summary)
		} else {
			fmt.Fprintf(&buf, "- [%s](%s)\n", e.Title, e.URL)
		}
	}
	return template.HTML(buf.String())
}

// — TemplateFuncMap ————————————————————————————————————————————————————————

// TemplateFuncMap returns a [template.FuncMap] containing all Forge template
// helper functions. Pass it to [template.Template.Funcs] before parsing:
//
//	tpl := template.New("page").Funcs(smeldr.TemplateFuncMap())
//
// Available functions:
//
//	smeldr_meta         — JSON-LD <script> block: {{smeldr_meta .Head .Content}}
//	smeldr_date         — formatted date string: {{.PublishedAt | smeldr_date}}
//	smeldr_markdown     — Markdown → HTML: {{.Body | smeldr_markdown}}
//	smeldr_html         — trusted raw HTML passthrough: {{.Content.Embed | smeldr_html}}
//	smeldr_excerpt      — truncated excerpt: {{.Body | smeldr_excerpt 160}}
//	smeldr_csrf_token   — hidden CSRF input: {{smeldr_csrf_token .Request}}
//	smeldr_rfc3339      — RFC 3339 timestamp: {{smeldr_rfc3339 .Head.Published}}
//	smeldr_llms_entries — AI doc entry links (LLMsTemplateData): {{smeldr_llms_entries .}}
//	markdown           — full Markdown → HTML (tables, hr, language class): {{.Body | markdown}}
func TemplateFuncMap() template.FuncMap {
	return template.FuncMap{
		"smeldr_meta":         smeldrMeta,
		"smeldr_date":         smeldrDate,
		"smeldr_rfc3339":      smeldrRFC3339,
		"smeldr_markdown":     smeldrMarkdown,
		"smeldr_html":         smeldrHTML,
		"smeldr_excerpt":      smeldrExcerpt,
		"smeldr_csrf_token":   smeldrCSRFToken,
		"smeldr_llms_entries": smeldrLLMsEntries,
		"markdown":           renderMarkdown,
	}
}
