package forge

import (
	"fmt"
	"html/template"
	"strings"
)

// renderMarkdown converts a minimal subset of Markdown to safe HTML and
// returns it as [template.HTML] so Go templates do not double-escape it.
//
// All content is HTML-entity-escaped before being wrapped in tags, making
// the output safe for direct embedding in HTML pages (XSS-safe).
//
// Supported syntax:
//   - `# Heading` through `###### Heading` — h1–h6
//   - ` ```lang … ``` ` fenced code blocks — <pre><code class="language-lang">
//   - `- item` unordered list items — <ul><li>
//   - `| col | col |` tables with a `| --- | --- |` separator row
//   - `**text**` — <strong>
//   - “ `code` “ — <code>
//   - Blank-line-separated paragraphs — <p>
//   - Standalone `---` line — <hr>
//
// The function is exposed in [TemplateFuncMap] as the "markdown" key:
//
//	{{.Content.Body | markdown}}
func renderMarkdown(s string) template.HTML {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")

	var out strings.Builder

	var paraLines []string
	var listItems []string
	var codeLines []string
	var codeLang string

	type tableRow = []string
	var tableHead tableRow
	var tableBody []tableRow
	inTable := false
	inCode := false

	flushPara := func() {
		if len(paraLines) == 0 {
			return
		}
		out.WriteString("<p>")
		out.WriteString(mdInline(strings.Join(paraLines, " ")))
		out.WriteString("</p>\n")
		paraLines = paraLines[:0]
	}

	flushList := func() {
		if len(listItems) == 0 {
			return
		}
		out.WriteString("<ul>\n")
		for _, item := range listItems {
			out.WriteString("<li>")
			out.WriteString(mdInline(item))
			out.WriteString("</li>\n")
		}
		out.WriteString("</ul>\n")
		listItems = listItems[:0]
	}

	flushCode := func() {
		if codeLang != "" {
			fmt.Fprintf(&out, "<pre><code class=\"language-%s\">",
				template.HTMLEscapeString(codeLang))
		} else {
			out.WriteString("<pre><code>")
		}
		out.WriteString(template.HTMLEscapeString(strings.Join(codeLines, "\n")))
		out.WriteString("</code></pre>\n")
		codeLines = codeLines[:0]
		codeLang = ""
	}

	flushTable := func() {
		if !inTable {
			return
		}
		out.WriteString("<table>\n")
		if len(tableHead) > 0 {
			out.WriteString("<thead><tr>")
			for _, cell := range tableHead {
				out.WriteString("<th>")
				out.WriteString(mdInline(strings.TrimSpace(cell)))
				out.WriteString("</th>")
			}
			out.WriteString("</tr></thead>\n")
		}
		if len(tableBody) > 0 {
			out.WriteString("<tbody>\n")
			for _, row := range tableBody {
				out.WriteString("<tr>")
				for _, cell := range row {
					out.WriteString("<td>")
					out.WriteString(mdInline(strings.TrimSpace(cell)))
					out.WriteString("</td>")
				}
				out.WriteString("</tr>\n")
			}
			out.WriteString("</tbody>\n")
		}
		out.WriteString("</table>\n")
		tableHead = nil
		tableBody = nil
		inTable = false
	}

	for _, line := range lines {
		// Fenced code block toggle.
		if strings.HasPrefix(line, "```") {
			if inCode {
				flushCode()
				inCode = false
			} else {
				flushPara()
				flushList()
				flushTable()
				codeLang = strings.TrimSpace(line[3:])
				inCode = true
			}
			continue
		}

		// Inside a code block — buffer verbatim; no inline processing.
		if inCode {
			codeLines = append(codeLines, line)
			continue
		}

		// Heading: line starts with one or more '#' followed by a space.
		if strings.HasPrefix(line, "#") {
			level := 0
			for _, c := range line {
				if c == '#' {
					level++
				} else {
					break
				}
			}
			if level <= 6 && len(line) > level && line[level] == ' ' {
				flushPara()
				flushList()
				flushTable()
				content := mdInline(line[level+1:])
				fmt.Fprintf(&out, "<h%d>%s</h%d>\n", level, content, level)
				continue
			}
		}

		// Blank line — flush all pending blocks.
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			flushList()
			flushTable()
			flushPara()
			continue
		}

		// Horizontal rule: standalone "---" (no | chars, not a table separator).
		if trimmed == "---" && !strings.Contains(line, "|") {
			flushPara()
			flushList()
			flushTable()
			out.WriteString("<hr>\n")
			continue
		}

		// Table row: line contains at least one '|'.
		if strings.HasPrefix(trimmed, "|") {
			if isTableSep(trimmed) {
				// Separator row — skip; signals end of header.
				continue
			}
			cells := parseTableRow(trimmed)
			if !inTable {
				flushPara()
				flushList()
				tableHead = cells
				inTable = true
			} else {
				tableBody = append(tableBody, cells)
			}
			continue
		}

		// List item.
		if strings.HasPrefix(line, "- ") {
			flushPara()
			flushTable()
			listItems = append(listItems, line[2:])
			continue
		}

		// HTML passthrough: a line whose trimmed form starts with '<' is emitted
		// verbatim. Forge is self-hosted; content authors are trusted — the same
		// role system that governs all MCP write operations.
		if strings.HasPrefix(trimmed, "<") {
			flushPara()
			flushList()
			flushTable()
			out.WriteString(line)
			out.WriteString("\n")
			continue
		}

		// Regular paragraph text — flush any open list or table first.
		flushList()
		flushTable()
		paraLines = append(paraLines, line)
	}

	// Flush anything still open (unterminated fence treated as code block).
	if inCode {
		flushCode()
	}
	flushList()
	flushTable()
	flushPara()

	return template.HTML(strings.TrimRight(out.String(), "\n"))
}

// isTableSep reports whether line is a Markdown table separator row —
// i.e. the line starts with '|', and every cell (split by '|', non-empty)
// contains only '-', ':', and spaces.
func isTableSep(line string) bool {
	if !strings.Contains(line, "|") {
		return false
	}
	parts := strings.Split(line, "|")
	saw := false
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		for _, c := range p {
			if c != '-' && c != ':' {
				return false
			}
		}
		saw = true
	}
	return saw
}

// parseTableRow splits a Markdown table row into cell strings.
// Leading and trailing '|' characters are stripped before splitting.
func parseTableRow(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "|")
	line = strings.TrimSuffix(line, "|")
	return strings.Split(line, "|")
}

// mdInline applies inline Markdown patterns to text that has already been
// HTML-entity-escaped. The escape step ensures that raw < > & in the source
// are neutralised before any markup tags are inserted.
//
// Supported patterns: [text](url) → <a href="url">, **bold** → <strong>,
// `code` → <code>. Link URL allow-list: http://, https://, and / only.
func mdInline(s string) string {
	s = template.HTMLEscapeString(s)
	s = mdApplyLinks(s)
	s = mdApplyBold(s)
	s = mdApplyCode(s)
	return s
}

// mdApplyLinks replaces [text](url) with <a href="url">text</a>.
// Called after HTML escaping, so the text portion is already safe.
// URL allow-list: only http://, https://, and / (relative paths) are
// accepted. Any other scheme is silently rejected and the original
// [text](url) literal is emitted unchanged.
func mdApplyLinks(s string) string {
	var b strings.Builder
	for {
		// Find opening '['
		open := strings.Index(s, "[")
		if open == -1 {
			b.WriteString(s)
			break
		}
		// Find '](' immediately after the text
		mid := strings.Index(s[open:], "](")
		if mid == -1 {
			b.WriteString(s)
			break
		}
		mid += open // absolute index of ']('
		// Find closing ')' after the URL
		close := strings.Index(s[mid+2:], ")")
		if close == -1 {
			b.WriteString(s)
			break
		}
		close += mid + 2 // absolute index of ')'

		text := s[open+1 : mid]
		rawURL := s[mid+2 : close]

		// Allow-list: only safe URL schemes.
		if !strings.HasPrefix(rawURL, "http://") &&
			!strings.HasPrefix(rawURL, "https://") &&
			!strings.HasPrefix(rawURL, "/") {
			// Reject: emit everything up to and including the closing ')' as-is.
			b.WriteString(s[:close+1])
			s = s[close+1:]
			continue
		}

		b.WriteString(s[:open])
		b.WriteString("<a href=\"")
		b.WriteString(rawURL)
		b.WriteString("\">")
		b.WriteString(text)
		b.WriteString("</a>")
		s = s[close+1:]
	}
	return b.String()
}

// mdApplyBold replaces **text** with <strong>text</strong>.
// Called after HTML escaping so '**' cannot be injected via entity tricks.
func mdApplyBold(s string) string {
	var b strings.Builder
	for {
		open := strings.Index(s, "**")
		if open == -1 {
			b.WriteString(s)
			break
		}
		close := strings.Index(s[open+2:], "**")
		if close == -1 {
			b.WriteString(s)
			break
		}
		b.WriteString(s[:open])
		b.WriteString("<strong>")
		b.WriteString(s[open+2 : open+2+close])
		b.WriteString("</strong>")
		s = s[open+2+close+2:]
	}
	return b.String()
}

// mdApplyCode replaces `code` with <code>code</code>.
// Called after HTML escaping; backtick is not an HTML-special character.
func mdApplyCode(s string) string {
	var b strings.Builder
	for {
		open := strings.Index(s, "`")
		if open == -1 {
			b.WriteString(s)
			break
		}
		close := strings.Index(s[open+1:], "`")
		if close == -1 {
			b.WriteString(s)
			break
		}
		b.WriteString(s[:open])
		b.WriteString("<code>")
		b.WriteString(s[open+1 : open+1+close])
		b.WriteString("</code>")
		s = s[open+1+close+1:]
	}
	return b.String()
}
