package smeldr

import (
	"html/template"
	"net/url"
	"strconv"
	"strings"
)

// RenderMarkdown renders a markdown body as safe HTML for embedding inside a
// page. Every character of the input is escaped, raw HTML included, so the
// result is safe for text from any author. Headings render one step, as
// <h3 class="md-h">, whatever their level, so a body never takes over the
// page's outline.
//
// The subset: ATX headings, fenced code blocks, "---" rules, tables with a
// separator row, "- " bullets and "1. " numbered lists with one nested level,
// "> " block quotes (one level), paragraphs, **strong**, *em* and _em_ (only at
// word boundaries, so snake_case and 2*3*4 stay as they are), `code`, and links
// [label](target) whose target is http, https or relative. A link with any other
// target (javascript:, data:, //host and the like) stays literal text. Images,
// setext headings, reference links and bare URLs stay literal text.
//
// The parser is bounded: its look-ahead work is linear in the input, delimiter
// counts and link nesting are capped, and input over 1 MiB is not parsed. When
// a bound is hit, or the parser fails in any way, the whole text renders as
// escaped preformatted text (<pre class="md-raw">): nothing is cut, dropped or
// interpreted.
//
// The template funcs "markdown" and "smeldr_markdown" in [TemplateFuncMap] are
// for trusted site authors and differ in two places: a line that starts with
// "<" passes through as raw HTML, and headings keep their level (<h1>..<h6>).
// Never use them for untrusted text; use RenderMarkdown.
//
//	html := smeldr.RenderMarkdown(decision.Body)
func RenderMarkdown(s string) template.HTML {
	return mdRender(s, mdBody)
}

// StripMarkdown returns the text of a markdown body without its markup, for
// previews and text/plain output. Heading, list, quote and fence markers go;
// a table row becomes its cells separated by spaces; a link or image becomes
// its label; code spans keep their content; emphasis markers go only where they
// pair as emphasis, so snake_case, 2*3*4 and a lone * survive. Each block stays
// on its own line. The result is never longer than s; when a parse bound is
// hit, s is returned unchanged.
//
//	preview := smeldr.StripMarkdown(decision.Body)
func StripMarkdown(s string) string {
	out, ok := mdStrip(s)
	if !ok || len(out) > len(s) {
		return s
	}
	return out
}

// renderMarkdown is the template-mode renderer behind the "markdown" and
// "smeldr_markdown" template funcs and markdown block fields: trusted site
// content, so a line starting with "<" passes through verbatim and headings
// keep their level. Everything else is shared with [RenderMarkdown].
//
//	{{.Content.Body | markdown}}
func renderMarkdown(s string) template.HTML {
	return mdRender(s, mdTemplate)
}

// mdMode selects the two places where the template renderer and the body
// renderer differ.
type mdMode uint8

const (
	mdTemplate mdMode = iota // trusted site content: HTML passthrough, h1..h6, no quotes
	mdBody                   // any author: everything escaped, one heading step, quotes
)

// Parse bounds (see RenderMarkdown).
const (
	mdMaxInput      = 1 << 20 // bytes; larger input is not parsed
	mdBudgetPerByte = 4       // look-ahead steps per input byte; a normal body uses about 0.2
	mdBudgetFloor   = 1 << 16 // plus this many, so short input is never near the bound
	mdMaxDelims     = 4096    // emphasis delimiter runs per inline run
	mdMaxLinkDepth  = 3       // link labels nest at most this deep
)

// mdBoundHit is the panic value that ends a parse whose bound was hit; the
// entry points recover it (and any other panic) into the raw fallback.
type mdBoundHit struct{}

// mdParser holds one parse's mode and work budget.
type mdParser struct {
	mode    mdMode
	work    int
	budget  int
	scratch []mdTok // reused for each top-level inline run
}

// spend charges n look-ahead steps and ends the parse when the budget is gone.
func (p *mdParser) spend(n int) {
	p.work += n
	if p.work > p.budget {
		panic(mdBoundHit{})
	}
}

// mdTestPanic, when set by a test, panics inside the parser so the recovery
// path can be proved.
var mdTestPanic bool

func newMDParser(s string, mode mdMode) *mdParser {
	return &mdParser{mode: mode, budget: mdBudgetPerByte*len(s) + mdBudgetFloor}
}

func mdRender(s string, mode mdMode) (out template.HTML) {
	if len(s) > mdMaxInput {
		return mdRaw(s)
	}
	defer func() {
		if r := recover(); r != nil {
			out = mdRaw(s)
		}
	}()
	p := newMDParser(s, mode)
	blocks := p.parseBlocks(mdLines(s), 0)
	var b strings.Builder
	b.Grow(len(s) + len(s)/2) // tags add about half again to typical markdown
	p.writeBlocks(&b, blocks)
	return template.HTML(strings.TrimRight(b.String(), "\n"))
}

func mdStrip(s string) (out string, ok bool) {
	if len(s) > mdMaxInput {
		return s, false
	}
	defer func() {
		if r := recover(); r != nil {
			out, ok = s, false
		}
	}()
	p := newMDParser(s, mdBody)
	blocks := p.parseBlocks(mdLines(s), 0)
	var b strings.Builder
	b.Grow(len(s))
	p.stripBlocks(&b, blocks)
	return strings.TrimRight(b.String(), "\n"), true
}

// mdRaw is the fallback: the whole text, escaped, preformatted.
func mdRaw(s string) template.HTML {
	var b strings.Builder
	b.Grow(len(s) + 32)
	b.WriteString(`<pre class="md-raw">`)
	mdEscape(&b, s)
	b.WriteString("</pre>")
	return template.HTML(b.String())
}

func mdLines(s string) []string {
	return strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
}

// Blocks.
type mdKind uint8

const (
	mdPara mdKind = iota
	mdHeading
	mdCode
	mdRule
	mdList
	mdTable
	mdHTMLLine
	mdQuote
)

// mdBlock is one block of the tree. Only the fields of its kind are set.
type mdBlock struct {
	kind     mdKind
	level    uint8  // heading level
	ordered  bool   // list
	start    int32  // list: first number of an ordered list
	text     string // inline source (para, heading), code text, raw HTML line
	lang     string // code
	items    []mdItem
	head     []string   // table header cells (inline source)
	rows     [][]string // table body rows
	children []mdBlock  // quote
}

// mdItem is one list item: its inline source and at most one nested list.
type mdItem struct {
	text string
	sub  *mdBlock
}

// mdListMarker reports a list marker at the start of line after indent spaces:
// "- " or up to nine digits followed by ". " or ") ". It returns the marker's
// kind, its number (ordered), and the item text.
func mdListMarker(line string) (indent int, ordered bool, num int, text string, ok bool) {
	for indent < len(line) && line[indent] == ' ' {
		indent++
	}
	rest := line[indent:]
	if strings.HasPrefix(rest, "- ") {
		return indent, false, 0, rest[2:], true
	}
	d := 0
	for d < len(rest) && d < 10 && rest[d] >= '0' && rest[d] <= '9' {
		d++
	}
	if d == 0 || d > 9 || d+1 >= len(rest) || (rest[d] != '.' && rest[d] != ')') || rest[d+1] != ' ' {
		return 0, false, 0, "", false
	}
	n, _ := strconv.Atoi(rest[:d])
	return indent, true, n, rest[d+2:], true
}

// mdHeadingLevel reports an ATX heading: one to six "#" then a space.
func mdHeadingLevel(line string) (int, bool) {
	level := 0
	for level < len(line) && line[level] == '#' {
		level++
	}
	if level == 0 || level > 6 || len(line) <= level || line[level] != ' ' {
		return 0, false
	}
	return level, true
}

// mdQuoteText strips one level of "> " markers and flattens deeper ones.
func mdQuoteText(line string) (string, bool) {
	if !strings.HasPrefix(line, ">") {
		return "", false
	}
	for strings.HasPrefix(line, ">") {
		line = strings.TrimPrefix(line[1:], " ")
	}
	return line, true
}

// parseBlocks builds the block tree. It keeps the template renderer's line
// rules: a block is recognised at column 0, a blank line ends every open block,
// and a heading, rule, fence, table row or list item interrupts a paragraph.
func (p *mdParser) parseBlocks(lines []string, depth int) []mdBlock {
	blocks := make([]mdBlock, 0, len(lines)/3+1)
	var para []string
	var list *mdBlock
	var table *mdBlock

	flushPara := func() {
		if len(para) > 0 {
			blocks = append(blocks, mdBlock{kind: mdPara, text: strings.Join(para, " ")})
			para = nil
		}
	}
	flushList := func() {
		if list != nil {
			blocks = append(blocks, *list)
			list = nil
		}
	}
	flushTable := func() {
		if table != nil {
			blocks = append(blocks, *table)
			table = nil
		}
	}
	flushAll := func() { flushPara(); flushList(); flushTable() }

	for i := 0; i < len(lines); i++ {
		if mdTestPanic {
			panic("md test panic")
		}
		line := lines[i]

		if strings.HasPrefix(line, "```") {
			flushAll()
			lang := strings.TrimSpace(line[3:])
			var code []string
			i++
			for ; i < len(lines) && !strings.HasPrefix(lines[i], "```"); i++ {
				code = append(code, lines[i])
			}
			blocks = append(blocks, mdBlock{kind: mdCode, lang: lang, text: strings.Join(code, "\n")})
			continue
		}

		if level, ok := mdHeadingLevel(line); ok {
			flushAll()
			blocks = append(blocks, mdBlock{kind: mdHeading, level: uint8(level), text: line[level+1:]})
			continue
		}

		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			flushAll()
			continue
		}

		if trimmed == "---" && !strings.Contains(line, "|") {
			flushAll()
			blocks = append(blocks, mdBlock{kind: mdRule})
			continue
		}

		if p.mode == mdBody && depth == 0 {
			if _, ok := mdQuoteText(line); ok {
				flushAll()
				var inner []string
				for ; i < len(lines); i++ {
					t, ok := mdQuoteText(lines[i])
					if !ok {
						break
					}
					inner = append(inner, t)
				}
				i--
				blocks = append(blocks, mdBlock{kind: mdQuote, children: p.parseBlocks(inner, depth+1)})
				continue
			}
		}

		if strings.HasPrefix(trimmed, "|") {
			if mdIsTableSep(trimmed) {
				continue
			}
			cells := mdSplitRow(trimmed)
			if table == nil {
				flushPara()
				flushList()
				table = &mdBlock{kind: mdTable, head: cells}
			} else {
				table.rows = append(table.rows, cells)
			}
			continue
		}

		if indent, ordered, num, text, ok := mdListMarker(line); ok {
			switch {
			case list != nil && indent >= 2:
				// A nested item under the last item; deeper levels flatten into it.
				last := &list.items[len(list.items)-1]
				if last.sub == nil {
					last.sub = &mdBlock{kind: mdList, ordered: ordered, start: int32(num)}
				}
				last.sub.items = append(last.sub.items, mdItem{text: text})
				continue
			case indent == 0 && (len(para) == 0 || !ordered || num == 1):
				// A numbered line interrupts a paragraph only when it starts at 1,
				// so wrapped prose that begins "2026. " stays prose.
				flushPara()
				flushTable()
				if list != nil && list.ordered != ordered {
					flushList()
				}
				if list == nil {
					list = &mdBlock{kind: mdList, ordered: ordered, start: int32(num)}
				}
				list.items = append(list.items, mdItem{text: text})
				continue
			}
		}

		if p.mode == mdTemplate && strings.HasPrefix(trimmed, "<") {
			flushAll()
			blocks = append(blocks, mdBlock{kind: mdHTMLLine, text: line})
			continue
		}

		if list != nil && strings.HasPrefix(line, "  ") {
			// An indented line continues the last item (or its last nested item).
			last := &list.items[len(list.items)-1]
			if last.sub != nil {
				sub := &last.sub.items[len(last.sub.items)-1]
				sub.text += " " + trimmed
			} else {
				last.text += " " + trimmed
			}
			continue
		}

		flushList()
		flushTable()
		para = append(para, line)
	}
	flushList()
	flushTable()
	flushPara()
	return blocks
}

// mdIsTableSep reports a table separator row: every non-empty cell is made of
// '-' and ':' only.
func mdIsTableSep(line string) bool {
	if !strings.Contains(line, "|") {
		return false
	}
	saw := false
	for _, cell := range strings.Split(line, "|") {
		cell = strings.TrimSpace(cell)
		if cell == "" {
			continue
		}
		for i := 0; i < len(cell); i++ {
			if cell[i] != '-' && cell[i] != ':' {
				return false
			}
		}
		saw = true
	}
	return saw
}

// mdSplitRow splits a table row into trimmed cells. The outer pipes are
// dropped; an escaped "\|" and a pipe inside a code span stay in the cell.
func mdSplitRow(line string) []string {
	s := strings.TrimPrefix(line, "|")
	if strings.HasSuffix(s, "|") && !strings.HasSuffix(s, "\\|") {
		s = s[:len(s)-1]
	}
	var cells []string
	var cur strings.Builder
	inCode := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\\' && i+1 < len(s) && s[i+1] == '|':
			cur.WriteByte('|')
			i++
		case c == '`':
			inCode = !inCode
			cur.WriteByte(c)
		case c == '|' && !inCode:
			cells = append(cells, strings.TrimSpace(cur.String()))
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	return append(cells, strings.TrimSpace(cur.String()))
}

// HTML writer.
func (p *mdParser) writeBlocks(b *strings.Builder, blocks []mdBlock) {
	for i := range blocks {
		p.writeBlock(b, &blocks[i])
	}
}

func (p *mdParser) writeBlock(b *strings.Builder, bl *mdBlock) {
	switch bl.kind {
	case mdPara:
		b.WriteString("<p>")
		p.writeInline(b, bl.text)
		b.WriteString("</p>\n")
	case mdHeading:
		if p.mode == mdBody {
			b.WriteString(`<h3 class="md-h">`)
			p.writeInline(b, bl.text)
			b.WriteString("</h3>\n")
			return
		}
		lv := byte('0' + bl.level)
		b.WriteString("<h")
		b.WriteByte(lv)
		b.WriteByte('>')
		p.writeInline(b, bl.text)
		b.WriteString("</h")
		b.WriteByte(lv)
		b.WriteString(">\n")
	case mdCode:
		if bl.lang != "" {
			b.WriteString(`<pre><code class="language-`)
			mdEscape(b, bl.lang)
			b.WriteString(`">`)
		} else {
			b.WriteString("<pre><code>")
		}
		mdEscape(b, bl.text)
		b.WriteString("</code></pre>\n")
	case mdRule:
		b.WriteString("<hr>\n")
	case mdHTMLLine:
		b.WriteString(bl.text)
		b.WriteString("\n")
	case mdQuote:
		b.WriteString("<blockquote>\n")
		p.writeBlocks(b, bl.children)
		b.WriteString("</blockquote>\n")
	case mdList:
		p.writeList(b, bl)
	case mdTable:
		p.writeTable(b, bl)
	}
}

func (p *mdParser) writeList(b *strings.Builder, bl *mdBlock) {
	tag := "ul"
	if bl.ordered {
		tag = "ol"
	}
	b.WriteByte('<')
	b.WriteString(tag)
	if bl.ordered && bl.start != 1 {
		b.WriteString(` start="`)
		b.WriteString(strconv.Itoa(int(bl.start)))
		b.WriteByte('"')
	}
	b.WriteString(">\n")
	for i := range bl.items {
		it := &bl.items[i]
		b.WriteString("<li>")
		p.writeInline(b, it.text)
		if it.sub != nil {
			b.WriteString("\n")
			p.writeList(b, it.sub)
		}
		b.WriteString("</li>\n")
	}
	b.WriteString("</")
	b.WriteString(tag)
	b.WriteString(">\n")
}

func (p *mdParser) writeTable(b *strings.Builder, bl *mdBlock) {
	b.WriteString("<table>\n")
	b.WriteString("<thead><tr>")
	for _, cell := range bl.head {
		b.WriteString("<th>")
		p.writeInline(b, cell)
		b.WriteString("</th>")
	}
	b.WriteString("</tr></thead>\n")
	if len(bl.rows) > 0 {
		b.WriteString("<tbody>\n")
		for _, row := range bl.rows {
			b.WriteString("<tr>")
			// A ragged row is padded or cut to the header's width.
			for k := range bl.head {
				b.WriteString("<td>")
				if k < len(row) {
					p.writeInline(b, row[k])
				}
				b.WriteString("</td>")
			}
			b.WriteString("</tr>\n")
		}
		b.WriteString("</tbody>\n")
	}
	b.WriteString("</table>\n")
}

// Plain-text writer.
func (p *mdParser) stripBlocks(b *strings.Builder, blocks []mdBlock) {
	for i := range blocks {
		bl := &blocks[i]
		switch bl.kind {
		case mdPara, mdHeading:
			p.stripInline(b, bl.text)
			b.WriteByte('\n')
		case mdCode:
			if bl.text != "" {
				b.WriteString(bl.text)
				b.WriteByte('\n')
			}
		case mdQuote:
			p.stripBlocks(b, bl.children)
		case mdList:
			p.stripList(b, bl)
		case mdTable:
			p.stripRow(b, bl.head)
			for _, row := range bl.rows {
				p.stripRow(b, row)
			}
		}
	}
}

func (p *mdParser) stripList(b *strings.Builder, bl *mdBlock) {
	for i := range bl.items {
		p.stripInline(b, bl.items[i].text)
		b.WriteByte('\n')
		if bl.items[i].sub != nil {
			p.stripList(b, bl.items[i].sub)
		}
	}
}

func (p *mdParser) stripRow(b *strings.Builder, cells []string) {
	for k, cell := range cells {
		if k > 0 {
			b.WriteByte(' ')
		}
		p.stripInline(b, cell)
	}
	b.WriteByte('\n')
}

// Inline.
type mdTokKind uint8

const (
	mdText mdTokKind = iota
	mdCodeSpan
	mdLink
	mdImage
	mdDelim
	mdStrong
	mdEm
)

// mdTok is one inline token. Text, code and an image's source are slices of
// the inline source; a link carries its parsed label, a pair its children.
type mdTok struct {
	kind     mdTokKind
	v        string  // text, code content, link href, or an image's source
	children []mdTok // link label, strong, em
	ch       byte    // delimiter
	n        int     // delimiter run length left; an image's alt length
	canOpen  bool
	canClose bool
}

func mdIsSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v'
}

func mdIsPunct(c byte) bool {
	return (c >= '!' && c <= '/') || (c >= ':' && c <= '@') || (c >= '[' && c <= '`') || (c >= '{' && c <= '~')
}

// mdPlain reports inline text with no byte that can start markup, which is
// written as it is, without tokens.
func mdPlain(s string) bool { return !strings.ContainsAny(s, "\\`[!*_") }

func (p *mdParser) writeInline(b *strings.Builder, s string) {
	if mdPlain(s) {
		mdEscape(b, s)
		return
	}
	toks := p.tokenize(p.scratch[:0], s, 0)
	mdWriteToks(b, toks)
	p.scratch = toks[:0]
}

func (p *mdParser) stripInline(b *strings.Builder, s string) {
	if mdPlain(s) {
		b.WriteString(s)
		return
	}
	toks := p.tokenize(p.scratch[:0], s, 0)
	mdStripToks(b, toks)
	p.scratch = toks[:0]
}

// tokenize splits an inline run into text, code spans, links, images and
// emphasis delimiter runs. Code spans are taken first, so nothing inside one is
// interpreted. Every look-ahead scan is charged to the work budget. Tokens are
// appended to dst, which a top-level run reuses from the parser's scratch.
func (p *mdParser) tokenize(dst []mdTok, s string, depth int) []mdTok {
	toks := dst
	// Where the last closer of each kind is: a scan that starts after it can
	// never succeed, so it is not run (the common hostile shapes cost nothing).
	lastTick := strings.LastIndexByte(s, '`')
	lastBracket := strings.LastIndexByte(s, ']')
	lastParen := strings.LastIndexByte(s, ')')
	// Text tokens are slices of s: from is where the pending text starts.
	from := 0
	flushTo := func(i int) {
		if i > from {
			toks = append(toks, mdTok{kind: mdText, v: s[from:i]})
		}
	}
	delims := 0
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '\\' && i+1 < len(s) && mdIsPunct(s[i+1]):
			flushTo(i)
			from = i + 1 // the escaped character starts the next text token
			i += 2
			continue
		case c == '`':
			n := mdRun(s, i, '`')
			if i+n > lastTick {
				i += n
				continue
			}
			if end := p.findRun(s, '`', n, i+n); end >= 0 {
				flushTo(i)
				toks = append(toks, mdTok{kind: mdCodeSpan, v: s[i+n : end]})
				i = end + n
				from = i
				continue
			}
			i += n
			continue
		case c == '[' || (c == '!' && i+1 < len(s) && s[i+1] == '['):
			start := i
			if c == '!' {
				start++
			}
			if depth < mdMaxLinkDepth {
				if start >= lastBracket || start >= lastParen {
					i++
					continue
				}
				if label, target, end, ok := p.matchLink(s, start, lastParen); ok {
					switch href, safe := mdSafeHref(target); {
					case c == '!':
						flushTo(i)
						toks = append(toks, mdTok{kind: mdImage, v: s[i:end], n: len(label)})
					case safe:
						flushTo(i)
						toks = append(toks, mdTok{kind: mdLink, v: href, children: p.tokenize(nil, label, depth+1)})
					default:
						// A refused link stays literal text, pending with the text around it.
						i = end
						continue
					}
					i = end
					from = i
					continue
				}
			}
			i++
			continue
		case c == '*' || c == '_':
			n := mdRun(s, i, c)
			var prev, next byte = ' ', ' '
			if i > 0 {
				prev = s[i-1]
			}
			if i+n < len(s) {
				next = s[i+n]
			}
			left := !mdIsSpace(next) && (!mdIsPunct(next) || mdIsSpace(prev) || mdIsPunct(prev))
			right := !mdIsSpace(prev) && (!mdIsPunct(prev) || mdIsSpace(next) || mdIsPunct(next))
			// Both delimiters follow "_"'s word-boundary rule, so an intraword run
			// (snake_case, 2*3*4) never opens or closes emphasis.
			canOpen := left && (!right || mdIsPunct(prev))
			canClose := right && (!left || mdIsPunct(next))
			if !canOpen && !canClose {
				i += n // can never pair: plain text
				continue
			}
			flushTo(i)
			toks = append(toks, mdTok{kind: mdDelim, ch: c, n: n, canOpen: canOpen, canClose: canClose})
			delims++
			if delims > mdMaxDelims {
				panic(mdBoundHit{})
			}
			i += n
			from = i
			continue
		}
		i++
	}
	flushTo(len(s))
	if delims == 0 {
		return toks
	}
	return p.resolve(toks)
}

// mdRun returns the length of the run of c starting at s[i].
func mdRun(s string, i int, c byte) int {
	n := 0
	for i+n < len(s) && s[i+n] == c {
		n++
	}
	return n
}

// findRun finds the next run of exactly n copies of c at or after from.
func (p *mdParser) findRun(s string, c byte, n, from int) int {
	for i := from; i < len(s); {
		p.spend(1)
		if s[i] != c {
			i++
			continue
		}
		k := mdRun(s, i, c)
		if k == n {
			return i
		}
		i += k
	}
	return -1
}

// matchLink recognises [label](target) at s[i] == '['. The label may hold code
// spans and balanced brackets; the target ends at the first unbalanced ')'.
func (p *mdParser) matchLink(s string, i, lastParen int) (label, target string, end int, ok bool) {
	depth := 0
	k := i
	for ; k < len(s); k++ {
		p.spend(1)
		switch s[k] {
		case '\\':
			k++
		case '`':
			n := mdRun(s, k, '`')
			if close := p.findRun(s, '`', n, k+n); close >= 0 {
				k = close + n - 1
			} else {
				k += n - 1
			}
		case '[':
			depth++
		case ']':
			depth--
		}
		if depth == 0 {
			break
		}
	}
	if k >= len(s) || k+1 >= len(s) || s[k+1] != '(' || k+2 > lastParen {
		return "", "", 0, false
	}
	parens := 0
	q := k + 2
	for ; q < len(s); q++ {
		p.spend(1)
		if s[q] == '(' {
			parens++
		} else if s[q] == ')' {
			if parens == 0 {
				break
			}
			parens--
		}
	}
	if q >= len(s) {
		return "", "", 0, false
	}
	return s[i+1 : k], s[k+2 : q], q + 1, true
}

// mdSafeHref allows an http or https URL with a host, or a relative target with
// neither scheme nor host. Everything else (javascript:, data:, vbscript:,
// mailto:, //host, a backslash, whitespace, a quote, angle bracket or backtick,
// a control character, an escaped control character, an HTML character
// reference) is refused. The target is returned as written.
func mdSafeHref(target string) (string, bool) {
	if target == "" {
		return "", false
	}
	for i := 0; i < len(target); i++ {
		c := target[i]
		if c <= ' ' || c == 0x7f || c == '\\' || c == '"' || c == '\'' || c == '<' || c == '>' || c == '`' {
			return "", false
		}
		if c == '&' && mdEntityAt(target, i) {
			return "", false
		}
		if c == '%' && i+2 < len(target) && (target[i+1] == '0' || target[i+1] == '1' || target[i+1] == '7') {
			if v, err := strconv.ParseUint(target[i+1:i+3], 16, 8); err == nil && (v < 0x20 || v == 0x7f) {
				return "", false
			}
		}
	}
	if strings.IndexByte(target, '%') >= 0 {
		if _, err := url.Parse(target); err != nil {
			return "", false // a malformed escape
		}
	}
	// The scheme, if any, is what comes before a ':' that precedes the first
	// '/', '?' or '#'. Compared without case, so JaVaScRiPt: is javascript:.
	end := strings.IndexAny(target, ":/?#")
	if end < 0 || target[end] != ':' {
		// No scheme: relative, unless it names a host ("//host").
		return target, !strings.HasPrefix(target, "//")
	}
	scheme := target[:end]
	if !strings.EqualFold(scheme, "http") && !strings.EqualFold(scheme, "https") {
		return "", false
	}
	rest := target[end+1:]
	if !strings.HasPrefix(rest, "//") {
		return "", false // "https:x" has no host
	}
	host := rest[2:]
	if h := strings.IndexAny(host, "/?#"); h >= 0 {
		host = host[:h]
	}
	return target, host != ""
}

// mdEntityAt reports an HTML character reference at s[i] == '&': "&#" or a
// name of letters and digits closed by ';'. A query string's "&b=2" is not one.
func mdEntityAt(s string, i int) bool {
	if i+1 < len(s) && s[i+1] == '#' {
		return true
	}
	k := i + 1
	for k < len(s) && ((s[k] >= 'a' && s[k] <= 'z') || (s[k] >= 'A' && s[k] <= 'Z') || (s[k] >= '0' && s[k] <= '9')) {
		k++
	}
	return k > i+1 && k < len(s) && s[k] == ';'
}

// resolve pairs emphasis delimiters (a simplified CommonMark pass, as in orch's
// renderer) into strong and em. An unpaired delimiter stays literal text. The
// backward scan for an opener is charged to the work budget.
func (p *mdParser) resolve(toks []mdTok) []mdTok {
	for ci := 0; ci < len(toks); ci++ {
		closer := &toks[ci]
		if closer.kind != mdDelim || !closer.canClose {
			continue
		}
		for closer.n > 0 {
			oi := ci - 1
			for ; oi >= 0; oi-- {
				p.spend(1)
				o := &toks[oi]
				if o.kind == mdDelim && o.ch == closer.ch && o.canOpen && o.n > 0 {
					break
				}
			}
			if oi < 0 {
				break
			}
			opener := &toks[oi]
			use := 1
			kind := mdEm
			if opener.n >= 2 && closer.n >= 2 {
				use, kind = 2, mdStrong
			}
			opener.n -= use
			closer.n -= use
			// Replace toks[oi+1:ci] by one pair token, in place.
			inner := append([]mdTok(nil), toks[oi+1:ci]...)
			toks[oi+1] = mdTok{kind: kind, children: inner}
			n := copy(toks[oi+2:], toks[ci:])
			toks = toks[:oi+2+n]
			ci = oi + 2
			closer = &toks[ci]
		}
	}
	return toks
}

func mdWriteToks(b *strings.Builder, toks []mdTok) {
	for i := range toks {
		t := &toks[i]
		switch t.kind {
		case mdText:
			mdEscape(b, t.v)
		case mdCodeSpan:
			b.WriteString("<code>")
			mdEscape(b, t.v)
			b.WriteString("</code>")
		case mdLink:
			b.WriteString(`<a href="`)
			mdEscape(b, t.v)
			b.WriteString(`">`)
			mdWriteToks(b, t.children)
			b.WriteString("</a>")
		case mdImage:
			mdEscape(b, t.v)
		case mdStrong:
			b.WriteString("<strong>")
			mdWriteToks(b, t.children)
			b.WriteString("</strong>")
		case mdEm:
			b.WriteString("<em>")
			mdWriteToks(b, t.children)
			b.WriteString("</em>")
		case mdDelim:
			for k := 0; k < t.n; k++ {
				b.WriteByte(t.ch)
			}
		}
	}
}

func mdStripToks(b *strings.Builder, toks []mdTok) {
	for i := range toks {
		t := &toks[i]
		switch t.kind {
		case mdText, mdCodeSpan:
			b.WriteString(t.v)
		case mdImage:
			b.WriteString(t.v[2 : 2+t.n]) // the alt text: "![" + alt + "](...)"
		case mdLink, mdStrong, mdEm:
			mdStripToks(b, t.children)
		case mdDelim:
			for k := 0; k < t.n; k++ {
				b.WriteByte(t.ch)
			}
		}
	}
}

// mdEscape writes s with the characters html/template's HTMLEscapeString
// escapes, escaped the same way, without an intermediate string.
func mdEscape(b *strings.Builder, s string) {
	last := 0
	for i := 0; i < len(s); i++ {
		var rep string
		switch s[i] {
		case '<':
			rep = "&lt;"
		case '>':
			rep = "&gt;"
		case '&':
			rep = "&amp;"
		case '\'':
			rep = "&#39;"
		case '"':
			rep = "&#34;"
		case 0:
			rep = "�"
		default:
			continue
		}
		b.WriteString(s[last:i])
		b.WriteString(rep)
		last = i + 1
	}
	b.WriteString(s[last:])
}
