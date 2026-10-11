package smeldr

import (
	"math/rand"
	"strings"
	"testing"
	"time"
)

// mdCase is one row of the subset tables: input, template-mode output and
// body-mode output.
type mdCase struct {
	name, in, tmpl, body string
}

func TestMarkdown_SubsetTable(t *testing.T) {
	cases := []mdCase{
		{"numbered list", "1. one\n2. two",
			"<ol>\n<li>one</li>\n<li>two</li>\n</ol>", "<ol>\n<li>one</li>\n<li>two</li>\n</ol>"},
		{"numbered list with start", "3) three\n4) four",
			"<ol start=\"3\">\n<li>three</li>\n<li>four</li>\n</ol>", "<ol start=\"3\">\n<li>three</li>\n<li>four</li>\n</ol>"},
		{"nested bullets", "- a\n  - a1\n  - a2\n- b",
			"<ul>\n<li>a\n<ul>\n<li>a1</li>\n<li>a2</li>\n</ul>\n</li>\n<li>b</li>\n</ul>",
			"<ul>\n<li>a\n<ul>\n<li>a1</li>\n<li>a2</li>\n</ul>\n</li>\n<li>b</li>\n</ul>"},
		{"nested numbered under bullet, deeper flattens", "- a\n  1. x\n      2. y",
			"<ul>\n<li>a\n<ol>\n<li>x</li>\n<li>y</li>\n</ol>\n</li>\n</ul>",
			"<ul>\n<li>a\n<ol>\n<li>x</li>\n<li>y</li>\n</ol>\n</li>\n</ul>"},
		{"indented continuation", "- first\n  more text\n- second",
			"<ul>\n<li>first more text</li>\n<li>second</li>\n</ul>", "<ul>\n<li>first more text</li>\n<li>second</li>\n</ul>"},
		{"nested continuation", "- a\n  - b\n    more",
			"<ul>\n<li>a\n<ul>\n<li>b more</li>\n</ul>\n</li>\n</ul>", "<ul>\n<li>a\n<ul>\n<li>b more</li>\n</ul>\n</li>\n</ul>"},
		{"bullet then numbered are two lists", "- a\n1. b",
			"<ul>\n<li>a</li>\n</ul>\n<ol>\n<li>b</li>\n</ol>", "<ul>\n<li>a</li>\n</ul>\n<ol>\n<li>b</li>\n</ol>"},
		{"numbered other than 1 does not interrupt a paragraph", "We met in\n2026. It went well.",
			"<p>We met in 2026. It went well.</p>", "<p>We met in 2026. It went well.</p>"},
		{"numbered 1 interrupts a paragraph", "Steps:\n1. go",
			"<p>Steps:</p>\n<ol>\n<li>go</li>\n</ol>", "<p>Steps:</p>\n<ol>\n<li>go</li>\n</ol>"},
		{"em star", "an *em* word", "<p>an <em>em</em> word</p>", "<p>an <em>em</em> word</p>"},
		{"em underscore", "an _em_ word", "<p>an <em>em</em> word</p>", "<p>an <em>em</em> word</p>"},
		{"strong underscore", "a __strong__ word", "<p>a <strong>strong</strong> word</p>", "<p>a <strong>strong</strong> word</p>"},
		{"snake_case stays", "use snake_case_names", "<p>use snake_case_names</p>", "<p>use snake_case_names</p>"},
		{"2*3*4 stays", "2*3*4 = 24", "<p>2*3*4 = 24</p>", "<p>2*3*4 = 24</p>"},
		{"strong around em", "**a *b* c**", "<p><strong>a <em>b</em> c</strong></p>", "<p><strong>a <em>b</em> c</strong></p>"},
		{"intraword star inside strong", "**a*b*c**", "<p><strong>a*b*c</strong></p>", "<p><strong>a*b*c</strong></p>"},
		{"unpaired star", "a * b", "<p>a * b</p>", "<p>a * b</p>"},
		{"backslash escape", `not \*em\* and \_x\_`, "<p>not *em* and _x_</p>", "<p>not *em* and _x_</p>"},
		{"code span protects markup", "`**x** [a](/b)`", "<p><code>**x** [a](/b)</code></p>", "<p><code>**x** [a](/b)</code></p>"},
		{"double backtick span", "`` a ` b ``", "<p><code> a ` b </code></p>", "<p><code> a ` b </code></p>"},
		{"link label with code and strong", "[`x` **y**](/z)",
			`<p><a href="/z"><code>x</code> <strong>y</strong></a></p>`, `<p><a href="/z"><code>x</code> <strong>y</strong></a></p>`},
		{"link with parens in target", "[w](https://e.example/a_(b))",
			`<p><a href="https://e.example/a_(b)">w</a></p>`, `<p><a href="https://e.example/a_(b)">w</a></p>`},
		{"relative forms", "[a](docs/x) [b](../up) [c](#sec) [d](?q=1)",
			`<p><a href="docs/x">a</a> <a href="../up">b</a> <a href="#sec">c</a> <a href="?q=1">d</a></p>`,
			`<p><a href="docs/x">a</a> <a href="../up">b</a> <a href="#sec">c</a> <a href="?q=1">d</a></p>`},
		{"query ampersand allowed", "[q](/s?a=1&b=2)", `<p><a href="/s?a=1&amp;b=2">q</a></p>`, `<p><a href="/s?a=1&amp;b=2">q</a></p>`},
		{"image stays literal", "![alt](/i.png)", "<p>![alt](/i.png)</p>", "<p>![alt](/i.png)</p>"},
		{"table escaped pipe and code pipe", "| a | b |\n| - | - |\n| x\\|y | `p|q` |",
			"<table>\n<thead><tr><th>a</th><th>b</th></tr></thead>\n<tbody>\n<tr><td>x|y</td><td><code>p|q</code></td></tr>\n</tbody>\n</table>",
			"<table>\n<thead><tr><th>a</th><th>b</th></tr></thead>\n<tbody>\n<tr><td>x|y</td><td><code>p|q</code></td></tr>\n</tbody>\n</table>"},
		{"ragged rows padded and cut", "| a | b |\n| - | - |\n| 1 |\n| 1 | 2 | 3 |",
			"<table>\n<thead><tr><th>a</th><th>b</th></tr></thead>\n<tbody>\n<tr><td>1</td><td></td></tr>\n<tr><td>1</td><td>2</td></tr>\n</tbody>\n</table>",
			"<table>\n<thead><tr><th>a</th><th>b</th></tr></thead>\n<tbody>\n<tr><td>1</td><td></td></tr>\n<tr><td>1</td><td>2</td></tr>\n</tbody>\n</table>"},
		{"headings by mode", "# One\n### Three", "<h1>One</h1>\n<h3>Three</h3>", "<h3 class=\"md-h\">One</h3>\n<h3 class=\"md-h\">Three</h3>"},
		{"html line by mode", "<b>x</b>", "<b>x</b>", "<p>&lt;b&gt;x&lt;/b&gt;</p>"},
		{"quote by mode", "> quoted **text**\n> more",
			"<p>&gt; quoted <strong>text</strong> &gt; more</p>",
			"<blockquote>\n<p>quoted <strong>text</strong> more</p>\n</blockquote>"},
		{"nested quote flattens", ">> deep\n> - item",
			"<p>&gt;&gt; deep &gt; - item</p>",
			"<blockquote>\n<p>deep</p>\n<ul>\n<li>item</li>\n</ul>\n</blockquote>"},
		{"NUL becomes U+FFFD, as HTMLEscapeString", "a\x00b", "<p>a�b</p>", "<p>a�b</p>"},
		{"a refused //host link was a link before", "[x](//evil.example)", "<p>[x](//evil.example)</p>", "<p>[x](//evil.example)</p>"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := string(renderMarkdown(c.in)); got != c.tmpl {
				t.Errorf("template mode:\n got %q\nwant %q", got, c.tmpl)
			}
			if got := string(RenderMarkdown(c.in)); got != c.body {
				t.Errorf("body mode:\n got %q\nwant %q", got, c.body)
			}
		})
	}
}

// The golden row that changed on purpose, with its new output.
func TestMarkdown_GoldenChangedRows(t *testing.T) {
	if got := string(renderMarkdown("a ** b ** c")); got != "<p>a ** b ** c</p>" {
		t.Errorf("spaced ** = %q", got)
	}
}

// mdUnsafe reports the first tag in s that body mode never writes, or an
// attribute other than the ones it writes (class on h3 and pre, start on ol,
// href on a, with an allowed target). Escaped text can hold anything.
func mdUnsafe(s string) string {
	allowed := map[string]bool{"p": true, "h3": true, "ul": true, "ol": true, "li": true, "pre": true, "code": true,
		"table": true, "thead": true, "tbody": true, "tr": true, "th": true, "td": true, "strong": true, "em": true,
		"a": true, "hr": true, "blockquote": true}
	for i := strings.IndexByte(s, '<'); i >= 0; {
		end := strings.IndexByte(s[i:], '>')
		if end < 0 {
			return s[i:]
		}
		tag := strings.TrimPrefix(s[i+1:i+end], "/")
		name, attrs, _ := strings.Cut(tag, " ")
		if !allowed[name] {
			return "<" + tag + ">"
		}
		switch {
		case attrs == "":
		case name == "a" && strings.HasPrefix(attrs, `href="`) && strings.HasSuffix(attrs, `"`) && strings.Count(attrs, `"`) == 2:
			if _, ok := mdSafeHref(strings.TrimSuffix(strings.TrimPrefix(attrs, `href="`), `"`)); !ok {
				return "<" + tag + ">"
			}
		case (name == "h3" && attrs == `class="md-h"`) || (name == "pre" && attrs == `class="md-raw"`) ||
			(name == "ol" && strings.HasPrefix(attrs, `start="`)) || (name == "code" && strings.HasPrefix(attrs, `class="language-`) && strings.Count(attrs, `"`) == 2):
		default:
			return "<" + tag + ">"
		}
		next := strings.IndexByte(s[i+end:], '<')
		if next < 0 {
			break
		}
		i += end + next
	}
	return ""
}

func TestRenderMarkdown_XSS(t *testing.T) {
	for _, in := range []string{
		"<script>alert(1)</script>",
		"text <script>alert(1)</script> inline",
		"| a |\n| - |\n| <script>alert(1)</script> |",
		"<img src=x onerror=alert(1)>",
		"<div class=\"x\"",
		"```\"><script>alert(1)</script>\ncode\n```",
		"> <script>alert(1)</script>",
		"> [x](javascript:alert(1))",
		"- <img src=x onerror=alert(1)>",
		"# <script>alert(1)</script>",
		"**<script>alert(1)</script>**",
		"`<script>alert(1)</script>`",
		"[x](javascript:alert(1))",
		"[x](JaVaScRiPt:alert(1))",
		"[x](java\tscript:alert(1))",
		"[x](\x01javascript:alert(1))",
		"[x](&#106;avascript:alert(1))",
		"[x](&#x6A;avascript:alert(1))",
		"[x](&colon;javascript:alert(1))",
		"[x](data:text/html,<script>alert(1)</script>)",
		"[x](vbscript:msgbox(1))",
		"[x](//evil.example)",
		"[x](/\\evil.example)",
		"[x](%01javascript:alert(1))",
		"[x]()",
		"[x](https:evil)",
		"[x](mailto:a@b.example)",
		"[\"><script>alert(1)</script>](/ok)",
		"[x](/ok\"onmouseover=\"alert(1))",
		"![x](javascript:alert(1))",
	} {
		got := string(RenderMarkdown(in))
		if bad := mdUnsafe(got); bad != "" {
			t.Errorf("RenderMarkdown(%q) has unescaped %q: %q", in, bad, got)
		}
		if strings.Contains(got, "<a ") && !strings.Contains(got, `href="/ok`) {
			t.Errorf("RenderMarkdown(%q) made a link: %q", in, got)
		}
	}
}

func TestMdSafeHref(t *testing.T) {
	for target, want := range map[string]bool{
		"http://a.example": true, "https://a.example/p?q=1#f": true, "HTTPS://A.example": true,
		"/x": true, "x/y": true, "../u": true, "#a": true, "?q": true, "/s?a=1&b=2": true, "/%20space": true,
		"": false, "http://": false, "https:x": false, "//h.example": false, "/\\h": false, "a b": false,
		"javascript:x": false, "JAVASCRIPT:x": false, "data:x": false, "vbscript:x": false, "mailto:x": false,
		"ftp://h.example": false, "\x7fx": false, "%0ajavascript:x": false, "%1fx": false, "%7fx": false,
		"&#106;x": false, "&amp;x": false, "x:y": false, "%zz": false,
	} {
		if _, got := mdSafeHref(target); got != want {
			t.Errorf("mdSafeHref(%q) = %v, want %v", target, got, want)
		}
	}
}

// Every bound falls back to escaped preformatted text holding the whole input.
func TestMarkdown_BoundsFallBackToRaw(t *testing.T) {
	isRaw := func(t *testing.T, name, in string, out string) {
		t.Helper()
		if !strings.HasPrefix(out, `<pre class="md-raw">`) || !strings.HasSuffix(out, "</pre>") {
			t.Fatalf("%s: not the raw fallback: %.80q", name, out)
		}
		if len(out) < len(in) {
			t.Errorf("%s: fallback lost text", name)
		}
	}
	big := strings.Repeat("a", mdMaxInput+1)
	isRaw(t, "size", big, string(RenderMarkdown(big)))
	isRaw(t, "size template", big, string(renderMarkdown(big)))

	delims := strings.Repeat("a *", mdMaxDelims+2)
	isRaw(t, "delimiter cap", delims, string(RenderMarkdown(delims)))

	brackets := strings.Repeat("[", 200000) + "](x)" // every opener scans to the end
	isRaw(t, "work budget", brackets, string(RenderMarkdown(brackets)))

	mdTestPanic = true
	out := string(RenderMarkdown("<b>x</b> & y"))
	mdTestPanic = false
	isRaw(t, "panic", "<b>x</b> & y", out)
	if out != `<pre class="md-raw">&lt;b&gt;x&lt;/b&gt; &amp; y</pre>` {
		t.Errorf("raw fallback = %q", out)
	}

	if got := StripMarkdown(brackets); got != brackets {
		t.Error("StripMarkdown must return the input unchanged on a bound")
	}
	if got := StripMarkdown(big); got != big {
		t.Error("StripMarkdown must return oversized input unchanged")
	}
	mdTestPanic = true
	got := StripMarkdown("**x**")
	mdTestPanic = false
	if got != "**x**" {
		t.Errorf("StripMarkdown after a panic = %q", got)
	}
}

// The work budget is linear: a parse of n bytes never spends more than the
// budget's per-byte allowance on input that does not hit a bound.
func TestMarkdown_WorkIsLinear(t *testing.T) {
	doc := strings.Repeat("A [link](/x) with `code`, **strong** and *em*.\n- item [a](/b)\n\n", 2000)
	p := newMDParser(doc, mdBody)
	blocks := p.parseBlocks(mdLines(doc), 0)
	var b strings.Builder
	p.writeBlocks(&b, blocks)
	if p.work > len(doc) {
		t.Errorf("work %d for %d bytes, want at most %d", p.work, len(doc), len(doc))
	}
	if strings.HasPrefix(b.String(), "<pre") {
		t.Error("a normal long body must not hit a bound")
	}
}

func TestMarkdown_HostileInputIsFast(t *testing.T) {
	inputs := map[string]string{
		"brackets":    strings.Repeat("[", 200000),
		"budget":      strings.Repeat("[", 200000) + "](x)",
		"backticks":   strings.Repeat("`", 200000),
		"alt ticks":   strings.Repeat("` ``", 50000),
		"stars":       strings.Repeat("*", 200000),
		"underscores": strings.Repeat("_", 200000),
		"open links":  strings.Repeat("[](", 70000),
		"open strong": strings.Repeat("**a ", 50000),
		"nested":      strings.Repeat("[[[[", 50000),
		"lists":       strings.Repeat("- x\n", 100000),
		"nested list": "- a\n" + strings.Repeat("  - b\n", 100000),
		"quotes":      strings.Repeat("> ", 100000),
		"table":       "| a |\n| - |\n" + strings.Repeat("| x |\n", 50000),
		"long line":   strings.Repeat("word ", 200000),
	}
	for name, in := range inputs {
		start := time.Now()
		_ = RenderMarkdown(in)
		_ = renderMarkdown(in)
		_ = StripMarkdown(in)
		// Generous: -race with -cover is up to ten times slower; a quadratic
		// parse of these inputs takes tens of seconds even without them.
		if d := time.Since(start); d > 5*time.Second {
			t.Errorf("%s: took %v", name, d)
		}
	}
}

func TestStripMarkdown_Corpus(t *testing.T) {
	cases := map[string]string{
		"# Title":                             "Title",
		"## Hello **world**":                  "Hello world",
		"- a\n- b":                            "a\nb",
		"1. one\n2. two":                      "one\ntwo",
		"- a\n  - nested":                     "a\nnested",
		"> quoted":                            "quoted",
		"see [the docs](/docs) now":           "see the docs now",
		"refused [x](javascript:alert(1))":    "refused [x](javascript:alert(1))",
		"![alt text](/i.png)":                 "alt text",
		"`code` and **bold** and _em_":        "code and bold and em",
		"snake_case and 2*3*4 and a * b":      "snake_case and 2*3*4 and a * b",
		"| a | b |\n| --- | --- |\n| 1 | 2 |": "a b\n1 2",
		"```go\nx := 1\n```":                  "x := 1",
		"---":                                 "",
		"one\ntwo":                            "one two",
		"para\n\n# Head":                      "para\nHead",
		"<b>x</b>":                            "<b>x</b>",
		`\*literal\*`:                         "*literal*",
		"This is _italic_ and [a link](https://example.com).": "This is italic and a link.",
	}
	for in, want := range cases {
		if got := StripMarkdown(in); got != want {
			t.Errorf("StripMarkdown(%q) = %q, want %q", in, got, want)
		}
	}
}

// StripMarkdown never returns a longer string, on the corpus or on random input.
func TestStripMarkdown_NeverGrows(t *testing.T) {
	alphabet := "ab *_`[]()!#>|-1.\n\\ &<"
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 5000; i++ {
		n := rng.Intn(80)
		var b strings.Builder
		for k := 0; k < n; k++ {
			b.WriteByte(alphabet[rng.Intn(len(alphabet))])
		}
		s := b.String()
		if got := StripMarkdown(s); len(got) > len(s) {
			t.Fatalf("StripMarkdown(%q) = %q grew", s, got)
		}
		_ = RenderMarkdown(s)
		_ = renderMarkdown(s)
	}
	for _, s := range markdownGoldenCorpus {
		if got := StripMarkdown(s); len(got) > len(s) {
			t.Errorf("StripMarkdown(%q) = %q grew", s, got)
		}
	}
}

var benchDecisionBody = strings.Repeat(
	"## Context\n\nA paragraph with **bold**, *em*, `code` and a [link](https://example.com/spec).\n\n"+
		"1. First point\n2. Second point\n   - a nested note\n\n"+
		"| Option | Cost |\n| --- | --- |\n| A | low |\n| B | high |\n\n> A quoted line.\n\n",
	130) // about 50 KB

func BenchmarkRenderMarkdown_Long(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = RenderMarkdown(benchDecisionBody)
	}
}

func BenchmarkStripMarkdown_Long(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = StripMarkdown(benchDecisionBody)
	}
}
