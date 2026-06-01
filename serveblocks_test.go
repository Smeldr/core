package smeldr

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// ── Harness ──────────────────────────────────────────────────────────────────

type blockFixture struct {
	t     *testing.T
	db    *sql.DB
	repo  *SQLRepo[*DynamicNode]
	edges *ContentEdgeStore
	dir   string
}

// defaultBlockTemplates is a fixture template set mirroring the template data
// contract — minimal <type_name>.html files, not the real site-dev templates.
func defaultBlockTemplates() map[string]string {
	return map[string]string{
		"hero":            `<section class="hero" id="{{.AnchorID}}"><h1>{{.Headline}}</h1>{{.Subtext}}</section>`,
		"content_block":   `<article id="{{.AnchorID}}" data-id="{{.ID}}"><h2>{{.Title}}</h2>{{.Body}}</article>`,
		"content_grid":    `<div class="grid {{.Layout}}">{{range .Items}}{{.}}{{end}}</div>`,
		"faq_item":        `<div class="faq-item"><dt>{{.Question}}</dt><dd>{{.Answer}}</dd></div>`,
		"faq":             `<dl class="faq {{.Layout}}">{{range .Items}}{{.}}{{end}}</dl>`,
		"footer":          `<footer>{{.Body}}</footer>`,
		"gallery":         `<div class="gallery {{.Layout}}">{{range .Items}}{{.}}{{end}}</div>`,
		"image":           `<figure><img src="{{.MediaURL}}" alt="{{.AltText}}"><figcaption>{{.Caption}}</figcaption></figure>`,
		"team":            `<div class="team {{.Layout}}">{{range .Items}}{{.}}{{end}}</div>`,
		"contact_card":    `<div class="card">{{.Name}}{{.Body}}</div>`,
		"html_block":      `<div class="raw">{{.HTML}}</div>`,
		"link_collection": `<ul class="{{.Layout}}">{{range .Items}}{{.}}{{end}}</ul>`,
		"link_item":       `<a href="{{.URL}}">{{.Title}}</a>`,
	}
}

func newBlockFixture(t *testing.T, templates map[string]string) *blockFixture {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Skipf("sqlite unavailable: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if err := CreateBlockTables(db); err != nil {
		t.Fatalf("CreateBlockTables: %v", err)
	}
	dir := t.TempDir()
	for name, body := range templates {
		if err := os.WriteFile(filepath.Join(dir, name+".html"), []byte(body), 0o600); err != nil {
			t.Fatalf("write template %s: %v", name, err)
		}
	}
	return &blockFixture{t: t, db: db, repo: NewDynamicContentRepo(db), edges: NewContentEdgeStore(db), dir: dir}
}

// rendererOn builds a BlockRenderer whose App uses the given DB (which may wrap
// f.db, e.g. for query counting).
func (f *blockFixture) rendererOn(db DB) *BlockRenderer {
	f.t.Helper()
	app := New(Config{BaseURL: "http://localhost", Secret: []byte("test-secret-32-bytes-xxxxxxxxxxxx"), DB: db})
	r, err := app.ServeBlocks(f.dir)
	if err != nil {
		f.t.Fatalf("ServeBlocks: %v", err)
	}
	return r
}

func (f *blockFixture) renderer() *BlockRenderer { return f.rendererOn(f.db) }

func (f *blockFixture) put(id, typeName string, status Status, fields string) {
	f.t.Helper()
	if fields == "" {
		fields = "{}"
	}
	n := &DynamicNode{Node: Node{ID: id, Status: status}, TypeName: typeName, Fields: json.RawMessage(fields)}
	if err := f.repo.Save(context.Background(), n); err != nil {
		f.t.Fatalf("put %s: %v", id, err)
	}
}

func (f *blockFixture) link(parentID, parentType, childID, childType, role string) {
	f.t.Helper()
	if _, err := f.edges.AddChild(context.Background(), ContentEdge{
		ParentID: parentID, ParentType: parentType, ChildID: childID, ChildType: childType, EdgeRole: role,
	}); err != nil {
		f.t.Fatalf("link %s->%s: %v", parentID, childID, err)
	}
}

func (f *blockFixture) render(pageID string) string {
	f.t.Helper()
	html, err := f.renderer().Render(context.Background(), "page", pageID)
	if err != nil {
		f.t.Fatalf("Render: %v", err)
	}
	return string(html)
}

func mustOrder(t *testing.T, html string, markers ...string) {
	t.Helper()
	prev := -1
	for _, m := range markers {
		idx := strings.Index(html, m)
		if idx == -1 {
			t.Fatalf("marker %q not found in output:\n%s", m, html)
		}
		if idx < prev {
			t.Errorf("marker %q out of order in output:\n%s", m, html)
		}
		prev = idx
	}
}

// countingDB wraps a DB and counts query/exec calls — for the N+1 assertion.
type countingDB struct {
	DB
	n int
}

func (c *countingDB) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	c.n++
	return c.DB.QueryContext(ctx, q, args...)
}
func (c *countingDB) QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row {
	c.n++
	return c.DB.QueryRowContext(ctx, q, args...)
}
func (c *countingDB) ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error) {
	c.n++
	return c.DB.ExecContext(ctx, q, args...)
}

// ── End-user scenarios ───────────────────────────────────────────────────────

func TestServeBlocks_LandingPage(t *testing.T) {
	f := newBlockFixture(t, defaultBlockTemplates())
	page := "page1"
	f.put("hero", "hero", Published, `{"Headline":"Welcome","Subtext":"intro"}`)
	f.put("grid", "content_grid", Published, `{"Layout":"grid-3"}`)
	f.put("cb1", "content_block", Published, `{"Title":"One","Body":"b1"}`)
	f.put("cb2", "content_block", Published, `{"Title":"Two","Body":"b2"}`)
	f.put("cb3", "content_block", Published, `{"Title":"Three","Body":"b3"}`)
	f.put("faq", "faq", Published, `{"Layout":""}`)
	for i, q := range []string{"q1", "q2", "q3", "q4", "q5"} {
		id := "f" + string(rune('a'+i))
		f.put(id, "faq_item", Published, `{"Question":"`+q+`","Answer":"a"}`)
		f.link("faq", "faq", id, "faq_item", "item")
	}
	f.put("footer", "footer", Published, `{"Body":"the footer"}`)

	f.link(page, "page", "hero", "hero", "section")
	f.link(page, "page", "grid", "content_grid", "section")
	f.link("grid", "content_grid", "cb1", "content_block", "item")
	f.link("grid", "content_grid", "cb2", "content_block", "item")
	f.link("grid", "content_grid", "cb3", "content_block", "item")
	f.link(page, "page", "faq", "faq", "section")
	f.link(page, "page", "footer", "footer", "section")

	out := f.render(page)
	mustOrder(t, out, "Welcome", "One", "Two", "Three", "q1", "q5", "the footer")
	if strings.Count(out, "faq-item") != 5 {
		t.Errorf("expected 5 faq items, got %d", strings.Count(out, "faq-item"))
	}
	if !strings.Contains(out, `class="grid grid-3"`) {
		t.Errorf("grid layout token missing:\n%s", out)
	}
}

func TestServeBlocks_SharedFooterAcrossTwoPages(t *testing.T) {
	f := newBlockFixture(t, defaultBlockTemplates())
	f.put("footer", "footer", Published, `{"Body":"shared"}`)
	f.put("a", "content_block", Published, `{"Title":"A","Body":""}`)
	f.put("b", "content_block", Published, `{"Title":"B","Body":""}`)
	f.link("pageA", "page", "a", "content_block", "section")
	f.link("pageA", "page", "footer", "footer", "section")
	f.link("pageB", "page", "b", "content_block", "section")
	f.link("pageB", "page", "footer", "footer", "section")

	a := f.render("pageA")
	b := f.render("pageB")
	if !strings.Contains(a, "shared") || !strings.Contains(a, ">A<") {
		t.Errorf("pageA missing content:\n%s", a)
	}
	if !strings.Contains(b, "shared") || !strings.Contains(b, ">B<") {
		t.Errorf("pageB missing content:\n%s", b)
	}
}

func TestServeBlocks_GalleryCarousel(t *testing.T) {
	f := newBlockFixture(t, defaultBlockTemplates())
	f.put("gal", "gallery", Published, `{"Layout":"carousel"}`)
	for _, id := range []string{"i1", "i2", "i3"} {
		f.put(id, "image", Published, `{"MediaURL":"/m/`+id+`.jpg","AltText":"`+id+`"}`)
		f.link("gal", "gallery", id, "image", "item")
	}
	f.link("p", "page", "gal", "gallery", "section")
	out := f.render("p")
	if !strings.Contains(out, `class="gallery carousel"`) {
		t.Errorf("carousel layout missing:\n%s", out)
	}
	mustOrder(t, out, "/m/i1.jpg", "/m/i2.jpg", "/m/i3.jpg")
}

func TestServeBlocks_TeamGrid2(t *testing.T) {
	f := newBlockFixture(t, defaultBlockTemplates())
	f.put("team", "team", Published, `{"Layout":"grid-2"}`)
	f.put("m1", "contact_card", Published, `{"Name":"Ada","Body":"bio"}`)
	f.put("m2", "contact_card", Published, `{"Name":"Linus","Body":"bio"}`)
	f.link("p", "page", "team", "team", "section")
	f.link("team", "team", "m1", "contact_card", "item")
	f.link("team", "team", "m2", "contact_card", "item")
	out := f.render("p")
	mustOrder(t, out, "Ada", "Linus")
	if !strings.Contains(out, `class="team grid-2"`) {
		t.Errorf("team layout missing:\n%s", out)
	}
}

// ── Edge cases ───────────────────────────────────────────────────────────────

func TestServeBlocks_EmptyPage(t *testing.T) {
	f := newBlockFixture(t, defaultBlockTemplates())
	out := f.render("nonexistent-page")
	if out != "" {
		t.Errorf("empty page should render empty, got %q", out)
	}
}

func TestServeBlocks_EmptyCollection(t *testing.T) {
	f := newBlockFixture(t, defaultBlockTemplates())
	f.put("grid", "content_grid", Published, `{"Layout":"grid-3"}`)
	f.link("p", "page", "grid", "content_grid", "section")
	out := f.render("p")
	if !strings.Contains(out, `class="grid grid-3"`) {
		t.Errorf("collection shell should render:\n%s", out)
	}
}

func TestServeBlocks_DraftBlockSkipped(t *testing.T) {
	f := newBlockFixture(t, defaultBlockTemplates())
	f.put("pub", "content_block", Published, `{"Title":"Published","Body":""}`)
	f.put("draft", "content_block", Draft, `{"Title":"Draft","Body":""}`)
	f.link("p", "page", "pub", "content_block", "section")
	f.link("p", "page", "draft", "content_block", "section")
	out := f.render("p")
	if !strings.Contains(out, "Published") {
		t.Error("published block should render")
	}
	if strings.Contains(out, "Draft") {
		t.Errorf("draft block must be skipped:\n%s", out)
	}
}

func TestServeBlocks_ArchivedBlockSkipped(t *testing.T) {
	f := newBlockFixture(t, defaultBlockTemplates())
	f.put("pub", "content_block", Published, `{"Title":"Live","Body":""}`)
	f.put("arch", "content_block", Archived, `{"Title":"Archived","Body":""}`)
	f.link("p", "page", "pub", "content_block", "section")
	f.link("p", "page", "arch", "content_block", "section")
	out := f.render("p")
	if strings.Contains(out, "Archived") {
		t.Errorf("archived block must be skipped:\n%s", out)
	}
}

func TestServeBlocks_DanglingEdge(t *testing.T) {
	f := newBlockFixture(t, defaultBlockTemplates())
	f.put("real", "content_block", Published, `{"Title":"Real","Body":""}`)
	f.link("p", "page", "real", "content_block", "section")
	f.link("p", "page", "ghost", "content_block", "section") // ghost block never created
	out := f.render("p")
	if !strings.Contains(out, "Real") {
		t.Error("real block should render despite dangling sibling")
	}
}

func TestServeBlocks_MissingTemplate(t *testing.T) {
	// Template set omits "hero".
	tpls := defaultBlockTemplates()
	delete(tpls, "hero")
	f := newBlockFixture(t, tpls)
	f.put("hero", "hero", Published, `{"Headline":"NoTemplate"}`)
	f.put("cb", "content_block", Published, `{"Title":"Has","Body":""}`)
	f.link("p", "page", "hero", "hero", "section")
	f.link("p", "page", "cb", "content_block", "section")
	out := f.render("p")
	if strings.Contains(out, "NoTemplate") {
		t.Errorf("block with missing template must be skipped:\n%s", out)
	}
	if !strings.Contains(out, "Has") {
		t.Error("sibling with a template should still render")
	}
}

func TestServeBlocks_MalformedFieldsJSON(t *testing.T) {
	f := newBlockFixture(t, defaultBlockTemplates())
	f.put("bad", "content_block", Published, `{not valid json`)
	f.put("good", "content_block", Published, `{"Title":"Good","Body":""}`)
	f.link("p", "page", "bad", "content_block", "section")
	f.link("p", "page", "good", "content_block", "section")
	out := f.render("p") // must not panic
	if !strings.Contains(out, "Good") {
		t.Error("good block should still render after a malformed sibling")
	}
}

func TestServeBlocks_EmptyFields(t *testing.T) {
	f := newBlockFixture(t, defaultBlockTemplates())
	f.put("cb", "content_block", Published, `{}`)
	f.link("p", "page", "cb", "content_block", "section")
	out := f.render("p")
	if !strings.Contains(out, "<article") {
		t.Errorf("empty-fields block should still render its shell:\n%s", out)
	}
}

func TestServeBlocks_CycleProtection(t *testing.T) {
	f := newBlockFixture(t, defaultBlockTemplates())
	// A (grid) → item B (grid) → item A : a cycle via shared collection blocks.
	f.put("A", "content_grid", Published, `{"Layout":"a"}`)
	f.put("B", "content_grid", Published, `{"Layout":"b"}`)
	f.link("p", "page", "A", "content_grid", "section")
	f.link("A", "content_grid", "B", "content_grid", "item")
	f.link("B", "content_grid", "A", "content_grid", "item")

	done := make(chan string, 1)
	go func() { done <- f.render("p") }()
	select {
	case out := <-done:
		if !strings.Contains(out, `class="grid a"`) {
			t.Errorf("expected A to render once:\n%s", out)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Render did not terminate — cycle protection failed")
	}
}

func TestServeBlocks_ReorderedSections(t *testing.T) {
	f := newBlockFixture(t, defaultBlockTemplates())
	f.put("x", "content_block", Published, `{"Title":"X","Body":""}`)
	f.put("y", "content_block", Published, `{"Title":"Y","Body":""}`)
	f.link("p", "page", "x", "content_block", "section")
	f.link("p", "page", "y", "content_block", "section")
	mustOrder(t, f.render("p"), ">X<", ">Y<")

	if err := f.edges.Reorder(context.Background(), "p", []string{"y", "x"}); err != nil {
		t.Fatalf("Reorder: %v", err)
	}
	mustOrder(t, f.render("p"), ">Y<", ">X<")
}

func TestServeBlocks_NestedOrderPreserved(t *testing.T) {
	f := newBlockFixture(t, defaultBlockTemplates())
	f.put("grid", "content_grid", Published, `{"Layout":"g"}`)
	f.link("p", "page", "grid", "content_grid", "section")
	for _, id := range []string{"n1", "n2", "n3", "n4"} {
		f.put(id, "content_block", Published, `{"Title":"`+id+`","Body":""}`)
		f.link("grid", "content_grid", id, "content_block", "item")
	}
	mustOrder(t, f.render("p"), ">n1<", ">n2<", ">n3<", ">n4<")
}

func TestServeBlocks_BatchedLoad_NoNPlus1(t *testing.T) {
	f := newBlockFixture(t, defaultBlockTemplates())
	f.put("hero", "hero", Published, `{"Headline":"H"}`)
	f.link("p", "page", "hero", "hero", "section")
	// 3 collections × 6 items.
	for _, c := range []string{"c1", "c2", "c3"} {
		f.put(c, "content_grid", Published, `{"Layout":"g"}`)
		f.link("p", "page", c, "content_grid", "section")
		for i := 0; i < 6; i++ {
			id := c + "-i" + string(rune('0'+i))
			f.put(id, "content_block", Published, `{"Title":"`+id+`","Body":""}`)
			f.link(c, "content_grid", id, "content_block", "item")
		}
	}

	cdb := &countingDB{DB: f.db}
	r := f.rendererOn(cdb)
	cdb.n = 0 // reset after construction (CreateBlockTables ran)
	if _, err := r.Render(context.Background(), "page", "p"); err != nil {
		t.Fatalf("Render: %v", err)
	}
	// 1 (sections) + 2 per level × 2 levels = 5. Allow a little slack; the naive
	// N+1 path would be 20+ for 19 blocks.
	if cdb.n > 7 {
		t.Errorf("expected a bounded query count (~5), got %d — N+1 regression", cdb.n)
	}
}

func TestServeBlocks_MarkdownFieldRendersHTML(t *testing.T) {
	f := newBlockFixture(t, defaultBlockTemplates())
	f.put("cb", "content_block", Published, `{"Title":"T","Body":"**bold** text"}`)
	f.link("p", "page", "cb", "content_block", "section")
	out := f.render("p")
	if !strings.Contains(out, "<strong>bold</strong>") {
		t.Errorf("markdown Body should render to HTML, got:\n%s", out)
	}
	if strings.Contains(out, "&lt;strong&gt;") {
		t.Errorf("markdown HTML must not be escaped:\n%s", out)
	}
}

func TestServeBlocks_HTMLBlockRaw(t *testing.T) {
	f := newBlockFixture(t, defaultBlockTemplates())
	f.put("h", "html_block", Published, `{"HTML":"<iframe src=\"x\"></iframe>"}`)
	f.link("p", "page", "h", "html_block", "section")
	out := f.render("p")
	if !strings.Contains(out, "<iframe src=\"x\"></iframe>") {
		t.Errorf("html_block HTML must render raw:\n%s", out)
	}
}

func TestServeBlocks_AnchorIDPresentAndAbsent(t *testing.T) {
	f := newBlockFixture(t, defaultBlockTemplates())
	f.put("withAnchor", "content_block", Published, `{"Title":"A","Body":"","AnchorID":"sec-a"}`)
	f.put("noAnchor", "content_block", Published, `{"Title":"B","Body":""}`)
	f.link("p", "page", "withAnchor", "content_block", "section")
	f.link("p", "page", "noAnchor", "content_block", "section")
	out := f.render("p")
	if !strings.Contains(out, `id="sec-a"`) {
		t.Errorf("AnchorID should be emitted:\n%s", out)
	}
	if !strings.Contains(out, `id=""`) {
		t.Errorf("absent AnchorID should default to empty, no error:\n%s", out)
	}
}

// ── Defensive additions ──────────────────────────────────────────────────────

func TestServeBlocks_PlainStringFieldEscaped(t *testing.T) {
	f := newBlockFixture(t, defaultBlockTemplates())
	// Title is a plain (non-markdown) field — must be auto-escaped.
	f.put("cb", "content_block", Published, `{"Title":"<script>alert(1)</script>","Body":""}`)
	f.link("p", "page", "cb", "content_block", "section")
	out := f.render("p")
	if strings.Contains(out, "<script>alert(1)</script>") {
		t.Errorf("plain field must be escaped (XSS):\n%s", out)
	}
	if !strings.Contains(out, "&lt;script&gt;") {
		t.Errorf("expected escaped output:\n%s", out)
	}
}

func TestServeBlocks_LinkSubstructBinds(t *testing.T) {
	f := newBlockFixture(t, map[string]string{
		"link_item": `<a href="{{.Link.URL}}" target="{{.Link.Target}}">{{.Link.Title}}</a>`,
	})
	f.put("li", "link_item", Published, `{"Link":{"URL":"https://x.test","Title":"Go","Target":"_blank","IsCTA":true}}`)
	f.link("p", "page", "li", "link_item", "section")
	out := f.render("p")
	if !strings.Contains(out, `href="https://x.test"`) || !strings.Contains(out, `target="_blank"`) || !strings.Contains(out, ">Go<") {
		t.Errorf("Link sub-struct fields should bind:\n%s", out)
	}
}

func TestServeBlocks_DraftItemInCollectionSkipped(t *testing.T) {
	f := newBlockFixture(t, defaultBlockTemplates())
	f.put("grid", "content_grid", Published, `{"Layout":"g"}`)
	f.put("pub", "content_block", Published, `{"Title":"PubItem","Body":""}`)
	f.put("draft", "content_block", Draft, `{"Title":"DraftItem","Body":""}`)
	f.link("p", "page", "grid", "content_grid", "section")
	f.link("grid", "content_grid", "pub", "content_block", "item")
	f.link("grid", "content_grid", "draft", "content_block", "item")
	out := f.render("p")
	if !strings.Contains(out, "PubItem") {
		t.Error("published item should render")
	}
	if strings.Contains(out, "DraftItem") {
		t.Errorf("draft item in collection must be skipped:\n%s", out)
	}
}

func TestServeBlocks_TemplateExecutionErrorIsLocal(t *testing.T) {
	tpls := defaultBlockTemplates()
	tpls["content_block"] = `{{.Nope.Deep}}` // accessing a field on an absent key errors at execution
	f := newBlockFixture(t, tpls)
	f.put("boom", "content_block", Published, `{"Title":"x"}`)
	f.put("footer", "footer", Published, `{"Body":"safe footer"}`)
	f.link("p", "page", "boom", "content_block", "section")
	f.link("p", "page", "footer", "footer", "section")
	out := f.render("p") // must not error/panic
	if !strings.Contains(out, "safe footer") {
		t.Errorf("a block with an execution error must not take down siblings:\n%s", out)
	}
}

func TestServeBlocks_NoDBConfigured(t *testing.T) {
	app := New(Config{BaseURL: "http://localhost", Secret: []byte("test-secret-32-bytes-xxxxxxxxxxxx")})
	if _, err := app.ServeBlocks(t.TempDir()); err == nil {
		t.Fatal("ServeBlocks without a DB should return an error")
	}
}

// ── Reference-field resolution (T82) ─────────────────────────────────────────

// refTemplates are fixtures whose image-bearing types use the {{ with .Image }}
// guard per the pinned contract.
func refTemplates() map[string]string {
	return map[string]string{
		"content_block": `<article>{{.Title}}{{.Body}}{{ with .Image }}<img src="{{ .MediaURL }}" alt="{{ .AltText }}"><figcaption>{{ .Caption }}</figcaption>{{ end }}</article>`,
		"hero":          `<section class="hero">{{.Headline}}{{ with .Image }}<img src="{{ .MediaURL }}">{{ end }}</section>`,
		"contact_card":  `<div class="card">{{.Name}}{{ with .Image }}<img src="{{ .MediaURL }}">{{ end }}</div>`,
	}
}

func TestServeBlocks_RefResolves(t *testing.T) {
	f := newBlockFixture(t, refTemplates())
	f.put("img1", "image", Published, `{"MediaURL":"/m/hero.jpg","AltText":"a hero"}`)
	f.put("cb", "content_block", Published, `{"Title":"T","Body":"","ImageID":"img1"}`)
	f.link("p", "page", "cb", "content_block", "section")
	out := f.render("p")
	if !strings.Contains(out, `src="/m/hero.jpg"`) {
		t.Errorf("referenced image MediaURL should render in <img src>:\n%s", out)
	}
	if !strings.Contains(out, `alt="a hero"`) {
		t.Errorf("referenced image AltText should render:\n%s", out)
	}
}

func TestServeBlocks_RefAbsent(t *testing.T) {
	f := newBlockFixture(t, refTemplates())
	f.put("cb", "content_block", Published, `{"Title":"NoImage","Body":""}`)
	f.link("p", "page", "cb", "content_block", "section")
	out := f.render("p")
	if !strings.Contains(out, "NoImage") {
		t.Error("block should render")
	}
	if strings.Contains(out, "<img") {
		t.Errorf("absent ImageID must produce no <img> (guarded):\n%s", out)
	}
}

func TestServeBlocks_RefUnpublished(t *testing.T) {
	f := newBlockFixture(t, refTemplates())
	f.put("img1", "image", Draft, `{"MediaURL":"/m/draft.jpg"}`)
	f.put("cb", "content_block", Published, `{"Title":"Live","Body":"","ImageID":"img1"}`)
	f.link("p", "page", "cb", "content_block", "section")
	out := f.render("p")
	if !strings.Contains(out, "Live") {
		t.Error("block should still render")
	}
	if strings.Contains(out, "<img") {
		t.Errorf("a Draft referenced image must not resolve:\n%s", out)
	}
}

func TestServeBlocks_RefDangling(t *testing.T) {
	f := newBlockFixture(t, refTemplates())
	f.put("cb", "content_block", Published, `{"Title":"Here","Body":"","ImageID":"ghost"}`)
	f.link("p", "page", "cb", "content_block", "section")
	out := f.render("p") // must not crash
	if !strings.Contains(out, "Here") {
		t.Error("block should render despite dangling ref")
	}
	if strings.Contains(out, "<img") {
		t.Errorf("dangling ImageID must not resolve:\n%s", out)
	}
}

func TestServeBlocks_RefShared(t *testing.T) {
	f := newBlockFixture(t, refTemplates())
	f.put("img1", "image", Published, `{"MediaURL":"/m/shared.jpg","AltText":"s"}`)
	f.put("cb1", "content_block", Published, `{"Title":"One","Body":"","ImageID":"img1"}`)
	f.put("cb2", "content_block", Published, `{"Title":"Two","Body":"","ImageID":"img1"}`)
	f.link("p", "page", "cb1", "content_block", "section")
	f.link("p", "page", "cb2", "content_block", "section")
	out := f.render("p")
	if strings.Count(out, `src="/m/shared.jpg"`) != 2 {
		t.Errorf("a shared image should resolve in both parents (want 2 <img>):\n%s", out)
	}
}

func TestServeBlocks_RefCaptionMarkdown(t *testing.T) {
	f := newBlockFixture(t, refTemplates())
	f.put("img1", "image", Published, `{"MediaURL":"/m/x.jpg","Caption":"**bold** cap"}`)
	f.put("cb", "content_block", Published, `{"Title":"T","Body":"","ImageID":"img1"}`)
	f.link("p", "page", "cb", "content_block", "section")
	out := f.render("p")
	if !strings.Contains(out, "<strong>bold</strong>") {
		t.Errorf(".Image.Caption should be markdown-rendered (proves .Image = full buildData):\n%s", out)
	}
}

func TestServeBlocks_RefHeroAndContactCard(t *testing.T) {
	f := newBlockFixture(t, refTemplates())
	f.put("img1", "image", Published, `{"MediaURL":"/m/h.jpg"}`)
	f.put("img2", "image", Published, `{"MediaURL":"/m/c.jpg"}`)
	f.put("hero", "hero", Published, `{"Headline":"H","ImageID":"img1"}`)
	f.put("card", "contact_card", Published, `{"Name":"Ada","Body":"","ImageID":"img2"}`)
	f.link("p", "page", "hero", "hero", "section")
	f.link("p", "page", "card", "contact_card", "section")
	out := f.render("p")
	if !strings.Contains(out, `src="/m/h.jpg"`) {
		t.Errorf("hero ImageID should resolve:\n%s", out)
	}
	if !strings.Contains(out, `src="/m/c.jpg"`) {
		t.Errorf("contact_card ImageID should resolve:\n%s", out)
	}
}

func TestServeBlocks_RefBatched_NoNPlus1(t *testing.T) {
	f := newBlockFixture(t, refTemplates())
	// 8 content_blocks, each referencing its own image.
	for i := 0; i < 8; i++ {
		s := string(rune('a' + i))
		img := "img-" + s
		cb := "cb-" + s
		f.put(img, "image", Published, `{"MediaURL":"/m/`+s+`.jpg"}`)
		f.put(cb, "content_block", Published, `{"Title":"`+s+`","Body":"","ImageID":"`+img+`"}`)
		f.link("p", "page", cb, "content_block", "section")
	}

	cdb := &countingDB{DB: f.db}
	r := f.rendererOn(cdb)
	cdb.n = 0
	out, err := r.Render(context.Background(), "page", "p")
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Count(string(out), "<img") != 8 {
		t.Errorf("expected 8 resolved images, got %d", strings.Count(string(out), "<img"))
	}
	// 1 (sections) + 2 (blocks + their item edges) + 1 (one batched ref load) ≈ 4.
	// The naive per-block path would be 8+ extra. Bound generously.
	if cdb.n > 6 {
		t.Errorf("expected bounded query count (~4) for 8 refs, got %d — N+1 regression", cdb.n)
	}
}
