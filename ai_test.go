package smeldr

import (
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// — Test helpers ——————————————————————————————————————————————————————————

// testAIPost is a content type for AI tests that implements Markdownable and SitemapNode.
type testAIPost struct {
	Node
	Title   string `smeldr:"required"`
	Summary string
	Body    string
}

func (p *testAIPost) Markdown() string { return "# " + p.Title + "\n\n" + p.Body }
func (p *testAIPost) Head() Head       { return Head{Title: p.Title} }
func (p *testAIPost) AISummary() string {
	if p.Summary != "" {
		return p.Summary
	}
	return ""
}

// seedAIPost saves a testAIPost in repo and returns it.
func seedAIPost(t *testing.T, repo Repository[*testAIPost], title, body string, status Status) *testAIPost {
	t.Helper()
	p := &testAIPost{
		Node:  Node{ID: NewID(), Slug: GenerateSlug(title), Status: status},
		Title: title,
		Body:  body,
	}
	if err := repo.Save(context.Background(), p); err != nil {
		t.Fatalf("seedAIPost: %v", err)
	}
	return p
}

// aiHeadFunc is a HeadFunc for testAIPost that uses its Title.
func aiHeadFunc(_ Context, p *testAIPost) Head {
	return Head{
		Title:       p.Title,
		Description: "Desc: " + p.Title,
		Type:        Article,
	}
}

// testAICtx returns a background Context for use in AI test helpers.
func testAICtx() Context { return NewTestContext(GuestUser) }

// — TestAIIndexOption —————————————————————————————————————————————————————

func TestAIIndexOption(t *testing.T) {
	cases := []struct {
		name     string
		features []AIFeature
		wantLen  int
	}{
		{"llms_txt_only", []AIFeature{LLMsTxt}, 1},
		{"ai_doc_only", []AIFeature{AIDoc}, 1},
		{"llms_txt_full_only", []AIFeature{LLMsTxtFull}, 1},
		{"all_three", []AIFeature{LLMsTxt, LLMsTxtFull, AIDoc}, 3},
		{"empty", nil, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opt := AIIndex(tc.features...)
			ai, ok := opt.(aiIndexOption)
			if !ok {
				t.Fatalf("AIIndex() returned %T, want aiIndexOption", opt)
			}
			if got := len(ai.features); got != tc.wantLen {
				t.Errorf("len(features) = %d, want %d", got, tc.wantLen)
			}
		})
	}
}

// — TestWithoutIDOption ———————————————————————————————————————————————————

func TestWithoutIDOption(t *testing.T) {
	t.Run("returns_withoutIDOption", func(t *testing.T) {
		opt := WithoutID()
		if _, ok := opt.(withoutIDOption); !ok {
			t.Fatalf("WithoutID() returned %T, want withoutIDOption", opt)
		}
	})

	t.Run("sets_withoutID_on_module", func(t *testing.T) {
		repo := NewMemoryRepo[*testAIPost]()
		m := NewModule((*testAIPost)(nil),
			Repo(repo),
			AIIndex(AIDoc),
			WithoutID(),
		)
		if !m.withoutID {
			t.Error("m.withoutID should be true after WithoutID() option")
		}
	})

	t.Run("default_withoutID_is_false", func(t *testing.T) {
		repo := NewMemoryRepo[*testAIPost]()
		m := NewModule((*testAIPost)(nil), Repo(repo), AIIndex(AIDoc))
		if m.withoutID {
			t.Error("m.withoutID should be false by default")
		}
	})
}

// — TestLLMsTxtEndpoint ———————————————————————————————————————————————————

func TestLLMsTxtEndpoint(t *testing.T) {
	repo := NewMemoryRepo[*testAIPost]()
	pub := seedAIPost(t, repo, "Hello World", "body text", Published)
	_ = seedAIPost(t, repo, "Draft Post", "draft body", Draft)

	store := NewLLMsStore("example.com")
	m := NewModule((*testAIPost)(nil),
		Repo(repo),
		At("/posts"),
		AIIndex(LLMsTxt),
		HeadFunc(aiHeadFunc),
	)
	m.setAIRegistry(store, "https://example.com")
	m.regenerateAI(testAICtx())

	mux := http.NewServeMux()
	m.Register(mux)
	mux.Handle("GET /llms.txt", store.CompactHandler())

	t.Run("published_item_present", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/llms.txt", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}
		body := w.Body.String()
		if !strings.Contains(body, pub.Title) {
			t.Errorf("body missing published title %q:\n%s", pub.Title, body)
		}
		if !strings.Contains(body, "example.com") {
			t.Errorf("body missing site name:\n%s", body)
		}
	})

	t.Run("draft_item_absent", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/llms.txt", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		body := w.Body.String()
		if strings.Contains(body, "Draft Post") {
			t.Errorf("body contains draft item title:\n%s", body)
		}
	})
}

// — TestLLMsTxtTemplate ———————————————————————————————————————————————————

func TestLLMsTxtTemplate(t *testing.T) {
	store := NewLLMsStore("myblog.io")
	store.registerCompact()
	store.SetCompact("/posts", []LLMsEntry{
		{Title: "First Post", URL: "https://myblog.io/posts/first-post", Summary: "An intro."},
		{Title: "No Summary Post", URL: "https://myblog.io/posts/no-summary"},
	})

	r := httptest.NewRequest(http.MethodGet, "/llms.txt", nil)
	w := httptest.NewRecorder()
	store.CompactHandler().ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()

	cases := []struct {
		label string
		want  string
	}{
		{"site_name_header", "# myblog.io"},
		{"entry_with_summary", "- [First Post](https://myblog.io/posts/first-post): An intro."},
		{"entry_without_summary", "- [No Summary Post](https://myblog.io/posts/no-summary)"},
		{"content_type", "text/plain"},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			if tc.label == "content_type" {
				if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, tc.want) {
					t.Errorf("Content-Type = %q, want %q", ct, tc.want)
				}
				return
			}
			if !strings.Contains(body, tc.want) {
				t.Errorf("body missing %q:\n%s", tc.want, body)
			}
		})
	}
}

// — TestLLMsFullTxtEndpoint ———————————————————————————————————————————————

func TestLLMsFullTxtEndpoint(t *testing.T) {
	repo := NewMemoryRepo[*testAIPost]()
	pub := seedAIPost(t, repo, "Markdown Article", "Full markdown body here.", Published)
	_ = seedAIPost(t, repo, "Hidden Draft", "draft content", Draft)

	store := NewLLMsStore("example.com")
	m := NewModule((*testAIPost)(nil),
		Repo(repo),
		At("/articles"),
		AIIndex(LLMsTxtFull),
		HeadFunc(aiHeadFunc),
	)
	m.setAIRegistry(store, "https://example.com")
	m.regenerateAI(testAICtx())

	mux := http.NewServeMux()
	mux.Handle("GET /llms-full.txt", store.FullHandler())

	t.Run("published_item_present", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/llms-full.txt", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}
		body := w.Body.String()
		if !strings.Contains(body, pub.Title) {
			t.Errorf("body missing published title %q:\n%s", pub.Title, body)
		}
		if !strings.Contains(body, pub.Body) {
			t.Errorf("body missing markdown body %q:\n%s", pub.Body, body)
		}
	})

	t.Run("draft_item_absent", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/llms-full.txt", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		body := w.Body.String()
		if strings.Contains(body, "Hidden Draft") {
			t.Errorf("body contains draft item:\n%s", body)
		}
	})
}

// — TestLLMsFullTxtFallback ———————————————————————————————————————————————

func TestLLMsFullTxtFallback(t *testing.T) {
	// Store has no module registered with LLMsTxtFull —
	// /llms-full.txt should never be mounted, so the mux returns 404.
	store := NewLLMsStore("example.com")
	store.registerCompact() // only compact registered, not full

	mux := http.NewServeMux()
	// App.Handler only mounts /llms-full.txt when HasFull() == true.
	if store.HasFull() {
		mux.Handle("GET /llms-full.txt", store.FullHandler())
	}

	r := httptest.NewRequest(http.MethodGet, "/llms-full.txt", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 when no LLMsTxtFull module registered", w.Code)
	}
}

// — TestLLMsFullTxtHeader —————————————————————————————————————————————————

func TestLLMsFullTxtHeader(t *testing.T) {
	store := NewLLMsStore("acmecorp.com")
	store.registerFull()
	store.SetFull("/posts", "## Example\nURL: https://acmecorp.com/posts/example\n\ncontent\n---\n\n")
	// prime compact so total count = 1
	store.SetCompact("/posts", []LLMsEntry{{Title: "Example", URL: "https://acmecorp.com/posts/example"}})

	r := httptest.NewRequest(http.MethodGet, "/llms-full.txt", nil)
	w := httptest.NewRecorder()
	store.FullHandler().ServeHTTP(w, r)

	body := w.Body.String()
	cases := []struct {
		label string
		want  string
	}{
		{"site_name_corpus_header", "# acmecorp.com \u2014 Full Content Corpus"},
		{"generated_by_forge", "> Generated by Forge on"},
		{"only_published_content", "Only published content"},
		{"item_count", "1 items"},
		{"content_included", "## Example"},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			if !strings.Contains(body, tc.want) {
				t.Errorf("body missing %q:\n%s", tc.want, body)
			}
		})
	}
}

// — TestAIDocEndpoint —————————————————————————————————————————————————————

func TestAIDocEndpoint(t *testing.T) {
	repo := NewMemoryRepo[*testAIPost]()
	pub := seedAIPost(t, repo, "My First Post", "Markdown **body** content.", Published)

	m := NewModule((*testAIPost)(nil),
		Repo(repo),
		At("/posts"),
		AIIndex(AIDoc),
		HeadFunc(aiHeadFunc),
	)
	mux := http.NewServeMux()
	m.Register(mux)

	r := httptest.NewRequest(http.MethodGet, "/posts/"+pub.Slug+"/aidoc", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()

	cases := []struct {
		label string
		want  string
	}{
		{"aidoc_header", "+++aidoc+v1+++"},
		{"type_field", "type:     Article"},
		{"id_field", "id:       " + pub.ID},
		{"slug_field", "slug:     " + pub.Slug},
		{"title_field", "title:    My First Post"},
		{"summary_from_aisummary", "summary:"},
		{"body_separator", "+++"},
		{"markdown_body", "Markdown **body** content."},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			if !strings.Contains(body, tc.want) {
				t.Errorf("body missing %q:\n%s", tc.want, body)
			}
		})
	}
}

// — TestAIDocNotFound —————————————————————————————————————————————————————

func TestAIDocNotFound(t *testing.T) {
	cases := []struct {
		name   string
		status Status
	}{
		{"draft", Draft},
		{"archived", Archived},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := NewMemoryRepo[*testAIPost]()
			p := seedAIPost(t, repo, "Hidden "+tc.name, "body", tc.status)

			m := NewModule((*testAIPost)(nil),
				Repo(repo),
				At("/posts"),
				AIIndex(AIDoc),
			)
			mux := http.NewServeMux()
			m.Register(mux)

			r := httptest.NewRequest(http.MethodGet, "/posts/"+p.Slug+"/aidoc", nil)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			if w.Code != http.StatusNotFound {
				t.Errorf("status = %d, want 404 for %s item", w.Code, tc.name)
			}
		})
	}
}

// — TestAIDocWithoutID ————————————————————————————————————————————————————

func TestAIDocWithoutID(t *testing.T) {
	repo := NewMemoryRepo[*testAIPost]()
	pub := seedAIPost(t, repo, "Public Post", "some body text", Published)

	m := NewModule((*testAIPost)(nil),
		Repo(repo),
		At("/posts"),
		AIIndex(AIDoc),
		WithoutID(),
		HeadFunc(aiHeadFunc),
	)
	mux := http.NewServeMux()
	m.Register(mux)

	r := httptest.NewRequest(http.MethodGet, "/posts/"+pub.Slug+"/aidoc", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()

	t.Run("id_suppressed", func(t *testing.T) {
		if strings.Contains(body, "id:") {
			t.Errorf("body contains id: field when WithoutID() is set:\n%s", body)
		}
	})

	t.Run("slug_present", func(t *testing.T) {
		if !strings.Contains(body, "slug:     "+pub.Slug) {
			t.Errorf("body missing slug field:\n%s", body)
		}
	})

	t.Run("id_value_absent", func(t *testing.T) {
		if strings.Contains(body, pub.ID) {
			t.Errorf("body contains ID value %q when WithoutID() is set:\n%s", pub.ID, body)
		}
	})
}

// — TestCompressIfAccepted (Amendment A17) ——————————————————————————————

// bigBody returns a slice of at least n bytes for threshold testing.
func bigBody(n int) []byte { return []byte(strings.Repeat("x", n)) }

// decompressGzip decompresses a gzip-encoded byte slice and returns the plain bytes.
func decompressGzip(t *testing.T, b []byte) []byte {
	t.Helper()
	gr, err := gzip.NewReader(strings.NewReader(string(b)))
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	out, err := io.ReadAll(gr)
	if err != nil {
		t.Fatalf("io.ReadAll gzip: %v", err)
	}
	return out
}

// TestCompressIfAccepted_gzip verifies that a body ≥ gzipMinBytes with
// Accept-Encoding: gzip receives Content-Encoding: gzip and a valid compressed body.
func TestCompressIfAccepted_gzip(t *testing.T) {
	body := bigBody(gzipMinBytes)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/llms.txt", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	compressIfAccepted(w, r, body, "text/plain; charset=utf-8")

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if w.Header().Get("Content-Encoding") != "gzip" {
		t.Errorf("Content-Encoding = %q; want gzip", w.Header().Get("Content-Encoding"))
	}
	if w.Header().Get("Content-Length") == "" {
		t.Errorf("Content-Length must be set on compressed response")
	}
	if w.Header().Get("Vary") != "Accept-Encoding" {
		t.Errorf("Vary = %q; want Accept-Encoding", w.Header().Get("Vary"))
	}
	decompressed := decompressGzip(t, w.Body.Bytes())
	if string(decompressed) != string(body) {
		t.Errorf("decompressed body mismatch: got %d bytes, want %d", len(decompressed), len(body))
	}
}

// TestCompressIfAccepted_smallBody verifies that a body < gzipMinBytes is NOT
// compressed even when the client accepts gzip.
func TestCompressIfAccepted_smallBody(t *testing.T) {
	body := bigBody(gzipMinBytes - 1)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/llms.txt", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	compressIfAccepted(w, r, body, "text/plain; charset=utf-8")

	if w.Header().Get("Content-Encoding") != "" {
		t.Errorf("expected no Content-Encoding for small body, got %q", w.Header().Get("Content-Encoding"))
	}
	if w.Header().Get("Vary") != "Accept-Encoding" {
		t.Errorf("Vary = %q; want Accept-Encoding", w.Header().Get("Vary"))
	}
	if w.Body.String() != string(body) {
		t.Errorf("plain body mismatch for small body path")
	}
}

// TestCompressIfAccepted_noAcceptEncoding verifies that a large body is NOT
// compressed when the client does not include Accept-Encoding: gzip.
func TestCompressIfAccepted_noAcceptEncoding(t *testing.T) {
	body := bigBody(gzipMinBytes)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/llms.txt", nil)
	// No Accept-Encoding header set.
	compressIfAccepted(w, r, body, "text/plain; charset=utf-8")

	if w.Header().Get("Content-Encoding") != "" {
		t.Errorf("expected no Content-Encoding without Accept-Encoding header, got %q", w.Header().Get("Content-Encoding"))
	}
	if w.Body.String() != string(body) {
		t.Errorf("plain body mismatch for no-accept-encoding path")
	}
}

// TestLLMsTxt_gzip verifies that CompactHandler returns a gzip-compressed
// response when the client sends Accept-Encoding: gzip and the body is large
// enough to trigger compression.
func TestLLMsTxt_gzip(t *testing.T) {
	repo := NewMemoryRepo[*testAIPost]()
	// Seed one entry with a very long summary so the response exceeds gzipMinBytes.
	longSummary := strings.Repeat("word ", 300) // ~1500 chars
	p := &testAIPost{
		Node:    Node{ID: NewID(), Slug: "gzip-article", Status: Published},
		Title:   "Gzip Article",
		Summary: longSummary,
	}
	if err := repo.Save(context.Background(), p); err != nil {
		t.Fatalf("save: %v", err)
	}

	store := NewLLMsStore("example.com")
	m := NewModule((*testAIPost)(nil),
		Repo(repo),
		At("/posts"),
		AIIndex(LLMsTxt),
		HeadFunc(aiHeadFunc),
	)
	m.setAIRegistry(store, "https://example.com")
	m.regenerateAI(testAICtx())

	mux := http.NewServeMux()
	mux.Handle("GET /llms.txt", store.CompactHandler())

	r := httptest.NewRequest(http.MethodGet, "/llms.txt", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body: %s", w.Code, w.Body.String())
	}
	if w.Header().Get("Content-Encoding") != "gzip" {
		t.Errorf("Content-Encoding = %q; want gzip", w.Header().Get("Content-Encoding"))
	}
	if w.Header().Get("Content-Length") == "" {
		t.Errorf("Content-Length must be set on compressed response")
	}
	decompressed := string(decompressGzip(t, w.Body.Bytes()))
	if !strings.Contains(decompressed, "Gzip Article") {
		t.Errorf("decompressed body missing entry title:\n%s", decompressed[:min(200, len(decompressed))])
	}
}

// — renderAIDoc coverage paths ————————————————————————————————————————————

// testMarkdownablePost implements Markdownable but NOT AIDocSummary.
type testMarkdownablePost struct {
	Node
	Title string
	Body  string
}

func (p *testMarkdownablePost) Markdown() string { return p.Body }
func (p *testMarkdownablePost) Head() Head       { return Head{Title: p.Title} }

func TestRenderAIDoc_nonMarkdownable(t *testing.T) {
	// Non-Markdownable item → JSON marshalled body.
	item := &testPost{
		Node:  Node{ID: "id1", Slug: "slug1", Status: Published},
		Title: "Test",
	}
	head := Head{Title: "Test", Type: "article"}
	n := Node{ID: "id1", Slug: "slug1"}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	renderAIDoc(w, r, head, n, item, false)

	body := w.Body.String()
	if !strings.Contains(body, "+++aidoc+v1+++") {
		t.Errorf("body missing aidoc header:\n%s", body)
	}
	// Non-Markdownable → JSON in body.
	if !strings.Contains(body, `"Title"`) {
		t.Errorf("body should contain JSON for non-Markdownable item:\n%s", body)
	}
}

func TestRenderAIDoc_markdownableNoSummary(t *testing.T) {
	// Markdownable without AIDocSummary + empty description → Excerpt fallback.
	item := &testMarkdownablePost{
		Node:  Node{ID: "id2", Slug: "slug2", Status: Published},
		Title: "Markdown Post",
		Body:  "Some markdown content that is long enough for excerpt.",
	}
	// Empty description so the Markdownable excerpt branch is hit.
	head := Head{Title: "Markdown Post"}
	n := Node{ID: "id2", Slug: "slug2"}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	renderAIDoc(w, r, head, n, item, false)

	body := w.Body.String()
	if !strings.Contains(body, "+++aidoc+v1+++") {
		t.Errorf("body missing aidoc header:\n%s", body)
	}
}

func TestRenderAIDoc_authorCreatedTags(t *testing.T) {
	// Covers head.Author, n.CreatedAt, and head.Tags branches.
	item := &testAIPost{
		Node:  Node{ID: "id3", Slug: "slug3", Status: Published},
		Title: "Tagged Post",
		Body:  "body",
	}
	createdAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	n := Node{ID: "id3", Slug: "slug3", CreatedAt: createdAt}
	head := Head{
		Title:  "Tagged Post",
		Author: "Alice",
		Tags:   []string{"go", "testing"},
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	renderAIDoc(w, r, head, n, item, true) // withoutID=true

	body := w.Body.String()
	if !strings.Contains(body, "author:   Alice") {
		t.Errorf("body missing author field:\n%s", body)
	}
	if !strings.Contains(body, "created:  2026-01-01") {
		t.Errorf("body missing created field:\n%s", body)
	}
	if !strings.Contains(body, "tags:     [go, testing]") {
		t.Errorf("body missing tags field:\n%s", body)
	}
	// withoutID=true → no id: line.
	if strings.Contains(body, "id:") {
		t.Errorf("body should not contain id: when withoutID=true:\n%s", body)
	}
}

// TestAIDoc_gzip verifies that the /aidoc endpoint returns a gzip-compressed
// response when Accept-Encoding: gzip is sent and the document body is large.
func TestAIDoc_gzip(t *testing.T) {
	repo := NewMemoryRepo[*testAIPost]()
	// Build a post whose Markdown() body exceeds gzipMinBytes.
	longBody := strings.Repeat("content word ", 120) // ~1560 chars
	pub := seedAIPost(t, repo, "BigDoc Post", longBody, Published)

	store := NewLLMsStore("example.com")
	m := NewModule((*testAIPost)(nil),
		Repo(repo),
		At("/posts"),
		AIIndex(AIDoc),
		HeadFunc(aiHeadFunc),
	)
	m.setAIRegistry(store, "https://example.com")

	mux := http.NewServeMux()
	m.Register(mux)

	r := httptest.NewRequest(http.MethodGet, "/posts/"+pub.Slug+"/aidoc", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body: %s", w.Code, w.Body.String())
	}
	if w.Header().Get("Content-Encoding") != "gzip" {
		t.Errorf("Content-Encoding = %q; want gzip", w.Header().Get("Content-Encoding"))
	}
	if w.Header().Get("Content-Length") == "" {
		t.Errorf("Content-Length must be set on compressed response")
	}
	decompressed := string(decompressGzip(t, w.Body.Bytes()))
	if !strings.Contains(decompressed, "+++aidoc+v1+++") {
		t.Errorf("decompressed body missing AIDoc header:\n%s", decompressed[:min(200, len(decompressed))])
	}
}
