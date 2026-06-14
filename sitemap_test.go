package smeldr

import (
	"bytes"
	"encoding/xml"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// — Test helpers ——————————————————————————————————————————————————————————

// sitemapPost is a minimal content type for sitemap tests.
// It embeds Node (satisfying SitemapNode via A2 getters) and implements
// Headable.
type sitemapPost struct {
	Node
	Title   string
	Canon   string // used for Head().Canonical when non-empty
	Heading Head
}

func (p *sitemapPost) Head() Head {
	h := p.Heading
	if p.Canon != "" {
		h.Canonical = p.Canon
	}
	return h
}

// sitemapPostWithPriority adds SitemapPrioritiser to sitemapPost.
type sitemapPostWithPriority struct {
	sitemapPost
	Prio float64
}

func (p *sitemapPostWithPriority) SitemapPriority() float64 { return p.Prio }

// publishedAt is a helper to build a published post.
func publishedPost(slug string, publishedAt time.Time) *sitemapPost {
	return &sitemapPost{
		Node: Node{
			ID:          NewID(),
			Slug:        slug,
			Status:      Published,
			PublishedAt: publishedAt,
		},
	}
}

// parseSitemapURLSet parses the XML body from a WriteSitemapFragment call.
type parsedURLSet struct {
	XMLName xml.Name    `xml:"urlset"`
	URLs    []parsedURL `xml:"url"`
}

type parsedURL struct {
	Loc        string `xml:"loc"`
	LastMod    string `xml:"lastmod"`
	ChangeFreq string `xml:"changefreq"`
}

type parsedIndex struct {
	XMLName  xml.Name           `xml:"sitemapindex"`
	Sitemaps []parsedSitemapRef `xml:"sitemap"`
}

type parsedSitemapRef struct {
	Loc string `xml:"loc"`
}

// — WriteSitemapFragment tests ————————————————————————————————————————————

func TestWriteSitemapFragment(t *testing.T) {
	pub := time.Date(2026, 3, 3, 12, 0, 0, 0, time.UTC)
	entries := []SitemapEntry{
		{Loc: "https://example.com/posts/hello", LastMod: pub, ChangeFreq: Weekly, Priority: 0.8},
		{Loc: "https://example.com/posts/world", LastMod: pub, ChangeFreq: Daily, Priority: 0.5},
	}
	var buf bytes.Buffer
	if err := WriteSitemapFragment(&buf, entries); err != nil {
		t.Fatalf("WriteSitemapFragment: %v", err)
	}
	body := buf.String()
	if !strings.Contains(body, `xmlns="http://www.sitemaps.org/schemas/sitemap/0.9"`) {
		t.Errorf("missing sitemaps namespace in output:\n%s", body)
	}
	var us parsedURLSet
	if err := xml.Unmarshal(buf.Bytes(), &us); err != nil {
		t.Fatalf("XML parse error: %v\nbody:\n%s", err, body)
	}
	if len(us.URLs) != 2 {
		t.Fatalf("got %d URLs, want 2", len(us.URLs))
	}
	if us.URLs[0].Loc != "https://example.com/posts/hello" {
		t.Errorf("URL[0].Loc = %q", us.URLs[0].Loc)
	}
	if us.URLs[0].LastMod != "2026-03-03" {
		t.Errorf("URL[0].LastMod = %q, want 2026-03-03", us.URLs[0].LastMod)
	}
	if us.URLs[1].ChangeFreq != "daily" {
		t.Errorf("URL[1].ChangeFreq = %q, want daily", us.URLs[1].ChangeFreq)
	}
}

func TestWriteSitemapFragment_empty(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteSitemapFragment(&buf, nil); err != nil {
		t.Fatalf("WriteSitemapFragment(nil): %v", err)
	}
	var us parsedURLSet
	if err := xml.Unmarshal(buf.Bytes(), &us); err != nil {
		t.Fatalf("XML parse error: %v\nbody:\n%s", err, buf.String())
	}
	if len(us.URLs) != 0 {
		t.Errorf("expected 0 URLs, got %d", len(us.URLs))
	}
}

func TestWriteSitemapFragment_zeroLastMod(t *testing.T) {
	entries := []SitemapEntry{
		{Loc: "https://example.com/foo", ChangeFreq: Weekly, Priority: 0.5},
	}
	var buf bytes.Buffer
	if err := WriteSitemapFragment(&buf, entries); err != nil {
		t.Fatalf("WriteSitemapFragment: %v", err)
	}
	if strings.Contains(buf.String(), "<lastmod>") {
		t.Errorf("zero LastMod should be omitted, but <lastmod> found in:\n%s", buf.String())
	}
}

// — SitemapEntries tests ——————————————————————————————————————————————————

func TestSitemapEntries_filtersUnpublished(t *testing.T) {
	pub := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	items := []*sitemapPost{
		publishedPost("hello", pub),
		{Node: Node{Slug: "draft-one", Status: Draft}},
		{Node: Node{Slug: "scheduled-one", Status: Scheduled}},
		publishedPost("world", pub),
	}
	entries := SitemapEntries(items, "https://example.com", SitemapConfig{})
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2 (only Published items)", len(entries))
	}
}

func TestSitemapEntries_canonicalLoc(t *testing.T) {
	pub := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	item := &sitemapPost{
		Node:  Node{Slug: "my-post", Status: Published, PublishedAt: pub},
		Canon: "https://example.com/custom/path",
	}
	entries := SitemapEntries([]*sitemapPost{item}, "https://example.com", SitemapConfig{})
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	if entries[0].Loc != "https://example.com/custom/path" {
		t.Errorf("Loc = %q, want canonical URL", entries[0].Loc)
	}
}

func TestSitemapEntries_slugFallback(t *testing.T) {
	pub := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	item := publishedPost("my-post", pub) // Canon is empty
	entries := SitemapEntries([]*sitemapPost{item}, "https://example.com", SitemapConfig{})
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	want := "https://example.com/my-post"
	if entries[0].Loc != want {
		t.Errorf("Loc = %q, want %q", entries[0].Loc, want)
	}
}

func TestSitemapEntries_customPriority(t *testing.T) {
	pub := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	item := &sitemapPostWithPriority{
		sitemapPost: sitemapPost{Node: Node{Slug: "featured", Status: Published, PublishedAt: pub}},
		Prio:        0.9,
	}
	entries := SitemapEntries([]*sitemapPostWithPriority{item}, "https://example.com", SitemapConfig{Priority: 0.5})
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	if entries[0].Priority != 0.9 {
		t.Errorf("Priority = %v, want 0.9 (SitemapPrioritiser should win)", entries[0].Priority)
	}
}

// — WriteSitemapIndex tests ————————————————————————————————————————————————

func TestWriteSitemapIndex(t *testing.T) {
	urls := []string{
		"https://example.com/posts/sitemap.xml",
		"https://example.com/pages/sitemap.xml",
	}
	var buf bytes.Buffer
	if err := WriteSitemapIndex(&buf, urls, time.Time{}); err != nil {
		t.Fatalf("WriteSitemapIndex: %v", err)
	}
	var idx parsedIndex
	if err := xml.Unmarshal(buf.Bytes(), &idx); err != nil {
		t.Fatalf("XML parse error: %v\nbody:\n%s", err, buf.String())
	}
	if len(idx.Sitemaps) != 2 {
		t.Fatalf("got %d sitemaps, want 2", len(idx.Sitemaps))
	}
	if idx.Sitemaps[0].Loc != "https://example.com/posts/sitemap.xml" {
		t.Errorf("Sitemaps[0].Loc = %q", idx.Sitemaps[0].Loc)
	}
	if idx.Sitemaps[1].Loc != "https://example.com/pages/sitemap.xml" {
		t.Errorf("Sitemaps[1].Loc = %q", idx.Sitemaps[1].Loc)
	}
}

// — SitemapStore tests ————————————————————————————————————————————————————

func TestSitemapStore_SetGet(t *testing.T) {
	s := NewSitemapStore()
	data := []byte("<urlset/>")
	s.Set("/posts/sitemap.xml", data)
	got, ok := s.Get("/posts/sitemap.xml")
	if !ok {
		t.Fatal("Get: not found after Set")
	}
	if string(got) != string(data) {
		t.Errorf("Get = %q, want %q", got, data)
	}
	// Mutating original must not affect stored copy.
	data[0] = 'X'
	got2, _ := s.Get("/posts/sitemap.xml")
	if got2[0] == 'X' {
		t.Error("Set should store a copy, not a reference")
	}
}

func TestSitemapStore_Handler_notFound(t *testing.T) {
	s := NewSitemapStore()
	req := httptest.NewRequest(http.MethodGet, "/missing/sitemap.xml", nil)
	req.Header.Set("X-Request-ID", "test-rid")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
	if got := w.Header().Get("X-Request-ID"); got != "test-rid" {
		t.Errorf("expected X-Request-ID 'test-rid', got %q", got)
	}
}

func TestSitemapStore_Handler_found(t *testing.T) {
	s := NewSitemapStore()
	body := []byte("<?xml version=\"1.0\"?><urlset/>")
	s.Set("/posts/sitemap.xml", body)
	req := httptest.NewRequest(http.MethodGet, "/posts/sitemap.xml", nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/xml") {
		t.Errorf("Content-Type = %q, want application/xml", ct)
	}
	if got := w.Body.String(); got != string(body) {
		t.Errorf("body = %q, want %q", got, body)
	}
}

func TestSitemapStore_IndexHandler(t *testing.T) {
	s := NewSitemapStore()
	s.Set("/posts/sitemap.xml", []byte("<urlset/>"))
	s.Set("/pages/sitemap.xml", []byte("<urlset/>"))
	req := httptest.NewRequest(http.MethodGet, "/sitemap.xml", nil)
	w := httptest.NewRecorder()
	s.IndexHandler("https://example.com").ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	var idx parsedIndex
	if err := xml.Unmarshal([]byte(body), &idx); err != nil {
		t.Fatalf("XML parse error: %v\nbody:\n%s", err, body)
	}
	if len(idx.Sitemaps) != 2 {
		t.Fatalf("got %d sitemaps, want 2", len(idx.Sitemaps))
	}
	// Paths are sorted; /pages comes before /posts.
	if idx.Sitemaps[0].Loc != "https://example.com/pages/sitemap.xml" {
		t.Errorf("Sitemaps[0].Loc = %q", idx.Sitemaps[0].Loc)
	}
	if idx.Sitemaps[1].Loc != "https://example.com/posts/sitemap.xml" {
		t.Errorf("Sitemaps[1].Loc = %q", idx.Sitemaps[1].Loc)
	}
}

// — WriteSitemap error paths ——————————————————————————————————————————————

// failWriter always fails the first Write call.
type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("write error") }

// afterFirstWriter succeeds the first Write, fails all subsequent ones.
type afterFirstWriter struct{ wrote bool }

func (w *afterFirstWriter) Write(p []byte) (int, error) {
	if w.wrote {
		return 0, errors.New("write error")
	}
	w.wrote = true
	return len(p), nil
}

func TestWriteSitemapFragment_headerWriteError(t *testing.T) {
	err := WriteSitemapFragment(failWriter{}, []SitemapEntry{{Loc: "https://x.com/"}})
	if err == nil {
		t.Error("expected error from header write failure")
	}
}

func TestWriteSitemapFragment_encodeError(t *testing.T) {
	err := WriteSitemapFragment(&afterFirstWriter{}, []SitemapEntry{{Loc: "https://x.com/"}})
	if err == nil {
		t.Error("expected error from encode failure")
	}
}

func TestWriteSitemapIndex_headerWriteError(t *testing.T) {
	err := WriteSitemapIndex(failWriter{}, []string{"https://x.com/sitemap1.xml"}, time.Time{})
	if err == nil {
		t.Error("expected error from header write failure")
	}
}

func TestWriteSitemapIndex_nonZeroLastMod(t *testing.T) {
	var buf bytes.Buffer
	lastMod := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	if err := WriteSitemapIndex(&buf, []string{"https://x.com/sitemap1.xml"}, lastMod); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "2025-06-01") {
		t.Errorf("expected lastMod date in output, got: %s", buf.String())
	}
}

func TestWriteSitemapIndex_encodeError(t *testing.T) {
	err := WriteSitemapIndex(&afterFirstWriter{}, []string{"https://x.com/sitemap1.xml"}, time.Time{})
	if err == nil {
		t.Error("expected error from encode failure")
	}
}

// — Benchmarks ————————————————————————————————————————————————————————————

func BenchmarkWriteSitemapFragment(b *testing.B) {
	pub := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	entries := make([]SitemapEntry, 100)
	for i := range entries {
		entries[i] = SitemapEntry{
			Loc:        "https://example.com/posts/post-" + string(rune('0'+i%10)),
			LastMod:    pub,
			ChangeFreq: Weekly,
			Priority:   0.7,
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		var buf bytes.Buffer
		_ = WriteSitemapFragment(&buf, entries)
	}
}
