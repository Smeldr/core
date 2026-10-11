package smeldr

import (
	"encoding/json"
	"flag"
	"os"
	"testing"
)

var updateMarkdownGolden = flag.Bool("update-markdown-golden", false, "rewrite testdata/markdown_golden.json from the current template-mode renderer")

// markdownGoldenCorpus is the existing subset the template renderer supported
// before the rewrite (core-markdown-render-export-and-extend). Its output was
// captured from that renderer into testdata/markdown_golden.json; the template
// mode must keep producing it byte for byte.
var markdownGoldenCorpus = []string{
	"# H1", "## H2", "### H3", "#### H4", "##### H5", "###### H6",
	"## Hello **world**",
	"#NoSpace",
	"####### seven",
	"```\nfoo()\n```",
	"```go\nfmt.Println(\"hi\")\n```",
	"```\nsome code",
	"```\n<b>raw</b> & \"q\"\n```",
	"- alpha\n- beta\n- gamma",
	"- one **bold**\n- two `code`\n- three [link](https://example.com)",
	"- item\nafter the list",
	"para\n- item",
	"| A | B |\n| --- | --- |\n| 1 | 2 |\n| 3 | 4 |",
	"| Left | Center |\n| :--- | :---: |\n| **b** | `c` |",
	"| only head |",
	"| A |\n| --- |\n| <script>bad</script> |",
	"Hello **world** end",
	"**important**",
	"Use `fmt.Println` here",
	"First para.\n\nSecond para.",
	"line one\nline two",
	"---",
	"text\n---\nmore",
	"[Read more](https://example.com)",
	"[Docs](/docs)",
	"[plain](http://example.com/a?b=1&c=2)",
	"[bad](javascript:alert(1))",
	"[x](data:text/html,<h1>xss</h1>)",
	"**bold** and [link](https://example.com)",
	"Fish & chips < 5 > 3 \"quoted\" 'single'",
	"**<script>xss</script>**",
	"`<img src=x onerror=alert(1)>`",
	"<script>alert(1)</script>",
	`<div class="pull-quote">text</div>`,
	"Before.\n\n<div class=\"x\">\n<p>inner</p>\n</div>\n\nAfter.",
	"",
	"   ",
	"Windows\r\nline endings\r\n\r\nsecond",
	"# Decision\n\nContext paragraph with **bold** and `code`.\n\n## Options\n\n- first option\n- second option\n\n| Option | Cost |\n| --- | --- |\n| A | low |\n| B | high |\n\n---\n\nSee [the spec](https://example.com/spec) and [docs](/docs).",
	"unclosed **bold here",
	"unclosed `code here",
	"[unclosed link](https://example.com",
	"[no paren] here",
	"a ** b ** c",
}

type markdownGoldenCase struct {
	In  string `json:"in"`
	Out string `json:"out"`
}

// markdownGoldenChanged lists the corpus inputs whose template-mode output is
// changed on purpose by the rewrite (D97, named in the CHANGELOG). Each has its
// own row with the new expectation in markdown_test.go.
var markdownGoldenChanged = map[string]bool{
	// "**" followed or preceded by a space no longer opens or closes strong
	// (CommonMark flanking): the old pass made "<strong> b </strong>".
	"a ** b ** c": true,
}

func TestMarkdownGolden_TemplateModeUnchanged(t *testing.T) {
	const path = "testdata/markdown_golden.json"
	if *updateMarkdownGolden {
		cases := make([]markdownGoldenCase, len(markdownGoldenCorpus))
		for i, in := range markdownGoldenCorpus {
			cases[i] = markdownGoldenCase{In: in, Out: string(renderMarkdown(in))}
		}
		b, err := json.MarshalIndent(cases, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cases []markdownGoldenCase
	if err := json.Unmarshal(b, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) != len(markdownGoldenCorpus) {
		t.Fatalf("golden has %d cases, corpus %d: regenerate only from the old renderer", len(cases), len(markdownGoldenCorpus))
	}
	for _, c := range cases {
		if markdownGoldenChanged[c.In] {
			continue
		}
		if got := string(renderMarkdown(c.In)); got != c.Out {
			t.Errorf("template mode changed for %q:\n got %q\nwant %q", c.In, got, c.Out)
		}
	}
}
