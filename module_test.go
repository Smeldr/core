package smeldr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// — Test helpers ——————————————————————————————————————————————————————————

// testPost is a minimal content type for Module tests.
// It embeds Node (providing ID, Slug, Status) and has a required Title field.
// It implements [Headable] (and therefore [SitemapNode]) so it can be used
// with SitemapConfig options in integration tests.
type testPost struct {
	Node
	Title string `smeldr:"required"`
	Body  string
}

func (p *testPost) Head() Head { return Head{Title: p.Title} }

// testNoHeadPost is a minimal content type that intentionally does NOT
// implement [Headable] or [SitemapNode]. Used only in A36 startup panic tests.
type testNoHeadPost struct {
	Node
	Title string `smeldr:"required"`
}

// testMDPost is a testPost that also implements [Markdownable].
type testMDPost struct {
	Node
	Title string `smeldr:"required"`
	Body  string
}

func (p *testMDPost) Markdown() string { return "# " + p.Title + "\n\n" + p.Body }

// withUser injects user into the request context so [ContextFrom] picks it up.
func withUser(r *http.Request, user User) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), userContextKey, user))
}

// authorUser returns a User with the Author role.
func authorUser() User { return User{ID: "author-1", Name: "Alice", Roles: []Role{Author}} }

// editorUser returns a User with the Editor role.
func editorUser() User { return User{ID: "editor-1", Name: "Bob", Roles: []Role{Editor}} }

// seedPost inserts a testPost into repo and returns it.
func seedPost(t *testing.T, repo Repository[*testPost], title string, status Status) *testPost {
	t.Helper()
	p := &testPost{
		Node:  Node{ID: NewID(), Slug: GenerateSlug(title), Status: status},
		Title: title,
	}
	if err := repo.Save(context.Background(), p); err != nil {
		t.Fatalf("seedPost: %v", err)
	}
	return p
}

// newTestModule creates a Module[*testPost] backed by the given repo.
func newTestModule(repo Repository[*testPost], opts ...Option) *Module[*testPost] {
	all := append([]Option{Repo(repo)}, opts...)
	return NewModule((*testPost)(nil), all...)
}

// seedMDPost inserts a testMDPost into repo and returns it.
func seedMDPost(t *testing.T, repo Repository[*testMDPost], title string, status Status) *testMDPost {
	t.Helper()
	p := &testMDPost{
		Node:  Node{ID: NewID(), Slug: GenerateSlug(title), Status: status},
		Title: title,
		Body:  "Hello world",
	}
	if err := repo.Save(context.Background(), p); err != nil {
		t.Fatalf("seedMDPost: %v", err)
	}
	return p
}

// — List handler tests ————————————————————————————————————————————————————

func TestModuleListGuestPublishedOnly(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	seedPost(t, repo, "Published Post", Published)
	seedPost(t, repo, "Draft Post", Draft)

	m := newTestModule(repo)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/testposts", nil)
	// No user injected → GuestUser.
	m.listHandler(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", w.Code)
	}
	var items []*testPost
	if err := json.Unmarshal(w.Body.Bytes(), &items); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(items) != 1 {
		t.Errorf("items count = %d; want 1 (published only)", len(items))
	}
	if items[0].Status != Published {
		t.Errorf("item status = %q; want %q", items[0].Status, Published)
	}
}

func TestModuleListAuthorSeesAll(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	seedPost(t, repo, "Published Post", Published)
	seedPost(t, repo, "Draft Post", Draft)
	seedPost(t, repo, "Scheduled Post", Scheduled)

	m := newTestModule(repo)
	w := httptest.NewRecorder()
	r := withUser(httptest.NewRequest(http.MethodGet, "/testposts", nil), authorUser())
	m.listHandler(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", w.Code)
	}
	var items []*testPost
	if err := json.Unmarshal(w.Body.Bytes(), &items); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(items) != 3 {
		t.Errorf("items count = %d; want 3 (all statuses)", len(items))
	}
}

// — Show handler tests ————————————————————————————————————————————————————

func TestModuleShowPublishedGuest(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	p := seedPost(t, repo, "My Post", Published)

	m := newTestModule(repo)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/testposts/"+p.Slug, nil)
	r.SetPathValue("slug", p.Slug)
	m.showHandler(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", w.Code)
	}
}

func TestModuleShowDraftGuest(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	p := seedPost(t, repo, "Draft Post", Draft)

	m := newTestModule(repo)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/testposts/"+p.Slug, nil)
	r.SetPathValue("slug", p.Slug)
	m.showHandler(w, r)

	// Guests must not see draft content — respond 404, not 403.
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d; want 404 (must not leak existence)", w.Code)
	}
}

func TestModuleShowDraftAuthor(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	p := seedPost(t, repo, "Draft Post", Draft)

	m := newTestModule(repo)
	w := httptest.NewRecorder()
	r := withUser(httptest.NewRequest(http.MethodGet, "/testposts/"+p.Slug, nil), authorUser())
	r.SetPathValue("slug", p.Slug)
	m.showHandler(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200 (authors see all statuses)", w.Code)
	}
}

// — Create handler tests ——————————————————————————————————————————————————

func TestModuleCreateValidation(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	m := newTestModule(repo)

	// POST with missing required Title.
	body, _ := json.Marshal(map[string]string{"Body": "Hello"})
	w := httptest.NewRecorder()
	r := withUser(
		httptest.NewRequest(http.MethodPost, "/testposts", bytes.NewReader(body)),
		authorUser(),
	)
	r.Header.Set("Content-Type", "application/json")
	m.createHandler(w, r)

	// Validation failure → 422 Unprocessable Entity.
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d; want 422 (validation error)", w.Code)
	}

	// Nothing saved.
	items, _ := repo.FindAll(context.Background(), ListOptions{})
	if len(items) != 0 {
		t.Errorf("repo count = %d; want 0 (aborted on validation failure)", len(items))
	}
}

func TestModuleCreateSuccess(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	m := newTestModule(repo)

	body, _ := json.Marshal(map[string]string{"Title": "Hello World", "Body": "Content here"})
	w := httptest.NewRecorder()
	r := withUser(
		httptest.NewRequest(http.MethodPost, "/testposts", bytes.NewReader(body)),
		authorUser(),
	)
	r.Header.Set("Content-Type", "application/json")
	m.createHandler(w, r)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d; want 201\nbody: %s", w.Code, w.Body.String())
	}

	var created testPost
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if created.ID == "" {
		t.Error("ID must be set on created item")
	}
	if created.Slug == "" {
		t.Error("Slug must be set on created item")
	}

	// Verify it was saved.
	items, _ := repo.FindAll(context.Background(), ListOptions{})
	if len(items) != 1 {
		t.Errorf("repo count = %d; want 1", len(items))
	}
}

// — Update handler tests ——————————————————————————————————————————————————

func TestModuleUpdateForbiddenGuest(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	p := seedPost(t, repo, "Existing Post", Published)

	m := newTestModule(repo)
	body, _ := json.Marshal(map[string]string{"Title": "Updated"})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/testposts/"+p.Slug, bytes.NewReader(body))
	r.SetPathValue("slug", p.Slug)
	// No user → GuestUser; default writeRole is Author.
	m.updateHandler(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d; want 403", w.Code)
	}
}

// TestModuleUpdateSetsPublishedAt verifies that transitioning an item from
// Draft to Published via updateHandler sets PublishedAt to a non-zero time.
func TestModuleUpdateSetsPublishedAt(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	p := seedPost(t, repo, "Draft Post", Draft)

	m := newTestModule(repo)

	update := map[string]any{
		"Title":  p.Title,
		"Status": string(Published),
	}
	body, _ := json.Marshal(update)
	before := time.Now().UTC()
	w := httptest.NewRecorder()
	r := withUser(
		httptest.NewRequest(http.MethodPut, "/testposts/"+p.Slug, bytes.NewReader(body)),
		editorUser(),
	)
	r.SetPathValue("slug", p.Slug)
	m.updateHandler(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200, body: %s", w.Code, w.Body.String())
	}

	saved, err := repo.FindBySlug(context.Background(), p.Slug)
	if err != nil {
		t.Fatalf("FindBySlug: %v", err)
	}
	if saved.PublishedAt.IsZero() {
		t.Error("PublishedAt is zero after Draft → Published transition")
	}
	if saved.PublishedAt.Before(before) {
		t.Errorf("PublishedAt %v is before the request time %v", saved.PublishedAt, before)
	}
}

// — Delete handler tests ——————————————————————————————————————————————————

func TestModuleDeleteForbiddenAuthor(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	p := seedPost(t, repo, "My Post", Published)

	m := newTestModule(repo) // default deleteRole = Editor
	w := httptest.NewRecorder()
	r := withUser(
		httptest.NewRequest(http.MethodDelete, "/testposts/"+p.Slug, nil),
		authorUser(), // Author < Editor → forbidden
	)
	r.SetPathValue("slug", p.Slug)
	m.deleteHandler(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d; want 403", w.Code)
	}
}

// — Content negotiation tests —————————————————————————————————————————————

func TestModuleContentNegotiationJSON(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	p := seedPost(t, repo, "My Post", Published)

	m := newTestModule(repo)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/testposts/"+p.Slug, nil)
	r.Header.Set("Accept", "application/json")
	r.SetPathValue("slug", p.Slug)
	m.showHandler(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", w.Code)
	}
	ct := w.Header().Get("Content-Type")
	if ct != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q; want application/json", ct)
	}
	vary := w.Header().Get("Vary")
	if vary == "" {
		t.Error("Vary header must be set")
	}
}

func TestModuleContentNegotiationMarkdown(t *testing.T) {
	repo := NewMemoryRepo[*testMDPost]()
	title := "Markdown Post"
	p := seedMDPost(t, repo, title, Published)

	m := NewModule((*testMDPost)(nil), Repo(repo))
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/testmdposts/"+p.Slug, nil)
	r.Header.Set("Accept", "text/markdown")
	r.SetPathValue("slug", p.Slug)
	m.showHandler(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200\nbody: %s", w.Code, w.Body.String())
	}
	ct := w.Header().Get("Content-Type")
	if ct != "text/markdown; charset=utf-8" {
		t.Errorf("Content-Type = %q; want text/markdown; charset=utf-8", ct)
	}
	body := w.Body.String()
	if body == "" {
		t.Error("markdown body must not be empty")
	}
}

func TestModuleContentNegotiationMarkdownUnsupported(t *testing.T) {
	// testPost does NOT implement Markdownable.
	repo := NewMemoryRepo[*testPost]()
	p := seedPost(t, repo, "Plain Post", Published)

	m := newTestModule(repo)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/testposts/"+p.Slug, nil)
	r.Header.Set("Accept", "text/markdown")
	r.SetPathValue("slug", p.Slug)
	m.showHandler(w, r)

	// text/markdown unsupported (n.md == false) → JSON fallback (A35).
	if w.Code != http.StatusOK {
		t.Errorf("status = %d; want 200 (JSON fallback, A35)", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q; want application/json", ct)
	}
}

func TestModuleContentNegotiationHTML(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	p := seedPost(t, repo, "HTML Post", Published)

	m := newTestModule(repo)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/testposts/"+p.Slug, nil)
	r.Header.Set("Accept", "text/html")
	r.SetPathValue("slug", p.Slug)
	m.showHandler(w, r)

	// No templates registered → JSON fallback, not 406 (A35).
	if w.Code != http.StatusOK {
		t.Errorf("status = %d; want 200 (JSON fallback, A35)", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q; want application/json", ct)
	}
}

// TestModuleContentNegotiationWildcard verifies that Accept: */* and empty
// Accept return text/html when templates are configured, and application/json
// when they are not (Amendment A53).
func TestModuleContentNegotiationWildcard(t *testing.T) {
	cases := []struct {
		name   string
		accept string
		html   bool
		want   string
	}{
		{"empty Accept + html=true", "", true, "text/html"},
		{"wildcard Accept + html=true", "*/*", true, "text/html"},
		{"empty Accept + html=false", "", false, "application/json"},
		{"wildcard Accept + html=false", "*/*", false, "application/json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n := contentNegotiator{html: tc.html}
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.accept != "" {
				r.Header.Set("Accept", tc.accept)
			}
			if got := n.negotiate(r); got != tc.want {
				t.Errorf("negotiate() = %q, want %q", got, tc.want)
			}
		})
	}
}

// — Cache tests ———————————————————————————————————————————————————————————

func TestModuleCacheMISS(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	p := seedPost(t, repo, "Cached Post", Published)

	m := newTestModule(repo, Cache(5*time.Minute))
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/testposts/"+p.Slug, nil)
	r.SetPathValue("slug", p.Slug)
	m.showHandler(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", w.Code)
	}
	if got := w.Header().Get("X-Cache"); got != "MISS" {
		t.Errorf("X-Cache = %q; want MISS", got)
	}
}

func TestModuleCacheHIT(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	p := seedPost(t, repo, "Cached Post", Published)

	m := newTestModule(repo, Cache(5*time.Minute))

	do := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/testposts/"+p.Slug, nil)
		r.SetPathValue("slug", p.Slug)
		m.showHandler(w, r)
		return w
	}

	w1 := do()
	if w1.Header().Get("X-Cache") != "MISS" {
		t.Fatal("first request should be MISS")
	}

	w2 := do()
	if got := w2.Header().Get("X-Cache"); got != "HIT" {
		t.Errorf("X-Cache = %q; want HIT on second identical request", got)
	}
	if w2.Body.String() != w1.Body.String() {
		t.Error("cached body does not match original body")
	}
}

func TestModuleCacheInvalidatedOnCreate(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	_ = seedPost(t, repo, "Existing Post", Published)

	m := newTestModule(repo, Cache(5*time.Minute))

	// Warm the list cache.
	warmReq := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/testposts", nil)
		m.listHandler(w, r)
		return w
	}
	w1 := warmReq()
	if w1.Header().Get("X-Cache") != "MISS" {
		t.Fatal("first request should be MISS")
	}
	w2 := warmReq()
	if w2.Header().Get("X-Cache") != "HIT" {
		t.Fatal("second request should be HIT")
	}

	// Create a new post → cache must be invalidated.
	body, _ := json.Marshal(map[string]string{"Title": "New Post"})
	cw := httptest.NewRecorder()
	cr := withUser(
		httptest.NewRequest(http.MethodPost, "/testposts", bytes.NewReader(body)),
		authorUser(),
	)
	m.createHandler(cw, cr)
	if cw.Code != http.StatusCreated {
		t.Fatalf("create failed: %d", cw.Code)
	}

	// Next list request should be MISS (cache was flushed).
	w3 := warmReq()
	if got := w3.Header().Get("X-Cache"); got != "MISS" {
		t.Errorf("X-Cache = %q; want MISS after create (cache should be invalidated)", got)
	}
}

// — LifecycleEvent tests ——————————————————————————————————————————————————————————

func TestModuleSignalBeforeCreateAborts(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()

	abortErr := Err("title", "forbidden title")
	hook := On(BeforeCreate, func(ctx Context, p *testPost) error {
		return abortErr
	})

	m := newTestModule(repo, hook)
	body, _ := json.Marshal(map[string]string{"Title": "Forbidden"})
	w := httptest.NewRecorder()
	r := withUser(
		httptest.NewRequest(http.MethodPost, "/testposts", bytes.NewReader(body)),
		authorUser(),
	)
	m.createHandler(w, r)

	// BeforeCreate error → handler must not save and must return an error response.
	if w.Code == http.StatusCreated {
		t.Error("BeforeCreate error should abort the create operation")
	}

	items, _ := repo.FindAll(context.Background(), ListOptions{})
	if len(items) != 0 {
		t.Error("nothing should be saved when BeforeCreate returns an error")
	}
}

func TestModuleSignalAfterCreateFires(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()

	var fired atomic.Int32
	hook := On(AfterCreate, func(ctx Context, p *testPost) error {
		fired.Add(1)
		return nil
	})

	m := newTestModule(repo, hook)
	body, _ := json.Marshal(map[string]string{"Title": "Hello"})
	w := httptest.NewRecorder()
	r := withUser(
		httptest.NewRequest(http.MethodPost, "/testposts", bytes.NewReader(body)),
		authorUser(),
	)
	m.createHandler(w, r)

	if w.Code != http.StatusCreated {
		t.Fatalf("create failed: %d\n%s", w.Code, w.Body.String())
	}

	// AfterCreate is asynchronous — allow time for the goroutine to run.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if fired.Load() > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if fired.Load() == 0 {
		t.Error("AfterCreate hook did not fire within 500ms")
	}
}

// TestModule_plainText_markdownStripped verifies that a text/plain request
// served for a [Markdownable] item returns a body with markdown syntax removed
// (exercises the stripMarkdown helper via the content-negotiation path).
func TestModule_plainText_markdownStripped(t *testing.T) {
	repo := NewMemoryRepo[*testMDPost]()
	p := &testMDPost{
		Node:  Node{ID: NewID(), Slug: "hello-world", Status: Published},
		Title: "Hello **World**",
		Body:  "This is _italic_ and [a link](https://example.com).",
	}
	if err := repo.Save(context.Background(), p); err != nil {
		t.Fatalf("Save: %v", err)
	}
	m := NewModule((*testMDPost)(nil), Repo(repo))

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/testmdposts/hello-world", nil)
	r.Header.Set("Accept", "text/plain")
	r.SetPathValue("slug", "hello-world")
	m.showHandler(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/plain") {
		t.Errorf("Content-Type = %q; want text/plain", ct)
	}
	body := w.Body.String()
	if strings.Contains(body, "**") || strings.Contains(body, "_italic_") || strings.Contains(body, "](") {
		t.Errorf("plain-text body still contains markdown syntax: %q", body)
	}
	if !strings.Contains(body, "Hello") || !strings.Contains(body, "World") {
		t.Errorf("plain-text body missing expected words: %q", body)
	}
}

// TestModule_cacheStore_Sweep verifies that CacheStore.Sweep evicts expired
// entries from a module's LRU cache.
func TestModule_cacheStore_Sweep(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	p := seedPost(t, repo, "sweep-test", Published)
	m := NewModule((*testPost)(nil), Repo(repo), Cache(time.Millisecond))

	// Warm the module cache via a show request.
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/testposts/"+p.Slug, nil)
	r.SetPathValue("slug", p.Slug)
	m.showHandler(w, r)

	if m.cache == nil {
		t.Fatal("cache is nil after Cache option")
	}
	m.cache.mu.Lock()
	before := len(m.cache.entries)
	m.cache.mu.Unlock()
	if before == 0 {
		t.Fatal("cache should have 1 entry after warmup")
	}

	// Wait for the 1ms TTL to expire, then sweep.
	time.Sleep(5 * time.Millisecond)
	m.cache.Sweep()

	m.cache.mu.Lock()
	after := len(m.cache.entries)
	m.cache.mu.Unlock()
	if after != 0 {
		t.Errorf("after Sweep: %d entries remain; want 0", after)
	}
}

// — Benchmark ——————————————————————————————————————————————————————————————

// BenchmarkModuleRequest measures the hot path for a cached GET show request.
func BenchmarkModuleRequest(b *testing.B) {
	repo := NewMemoryRepo[*testPost]()
	p := &testPost{
		Node:  Node{ID: NewID(), Slug: "bench-post", Status: Published},
		Title: "Benchmark Post",
		Body:  "Some body content for benchmarking.",
	}
	_ = repo.Save(context.Background(), p)

	m := NewModule((*testPost)(nil),
		Repo(repo),
		Cache(5*time.Minute),
	)

	// Warm the cache.
	w0 := httptest.NewRecorder()
	r0 := httptest.NewRequest(http.MethodGet, "/testposts/bench-post", nil)
	r0.SetPathValue("slug", "bench-post")
	m.showHandler(w0, r0)

	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/testposts/bench-post", nil)
		r.SetPathValue("slug", "bench-post")
		m.showHandler(w, r)
	}
}

// — A36 startup capability mismatch detection —————————————————————————————

// TestNewModule_sitemapConfig_panicsWithoutSitemapNode verifies that NewModule
// panics at startup when SitemapConfig is given but T does not implement
// SitemapNode (missing Head() smeldr.Head method). (Amendment A36)
func TestNewModule_sitemapConfig_panicsWithoutSitemapNode(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic but NewModule did not panic")
		}
		msg, _ := r.(string)
		if !strings.Contains(msg, "SitemapNode") {
			t.Errorf("panic message %q missing \"SitemapNode\"", msg)
		}
		if !strings.Contains(msg, "testNoHeadPost") {
			t.Errorf("panic message %q missing type name", msg)
		}
	}()
	repo := NewMemoryRepo[*testNoHeadPost]()
	NewModule((*testNoHeadPost)(nil), Repo(repo), SitemapConfig{})
}

// TestNewModule_aiIndexLLMsFull_panicsWithoutMarkdownable verifies that
// NewModule panics at startup when AIIndex(LLMsTxtFull) is given but T does
// not implement Markdownable (missing Markdown() string method). (Amendment A36)
func TestNewModule_aiIndexLLMsFull_panicsWithoutMarkdownable(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic but NewModule did not panic")
		}
		msg, _ := r.(string)
		if !strings.Contains(msg, "Markdownable") {
			t.Errorf("panic message %q missing \"Markdownable\"", msg)
		}
		if !strings.Contains(msg, "testPost") {
			t.Errorf("panic message %q missing type name", msg)
		}
	}()
	repo := NewMemoryRepo[*testPost]()
	NewModule((*testPost)(nil), Repo(repo), AIIndex(LLMsTxtFull))
}

// — A39 goroutine lifecycle ————————————————————————————————————————————————

// TestModule_Stop_idempotent verifies that calling Stop() twice does not panic.
// (Amendment A39)
func TestModule_Stop_idempotent(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	m := NewModule((*testPost)(nil), Repo(repo), Cache(time.Minute))
	m.Stop()
	m.Stop() // must not panic
}

// TestModule_Stop_haltsCacheSweep verifies that the cache sweep goroutine
// exits after Stop() is called. (Amendment A39)
func TestModule_Stop_haltsCacheSweep(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	m := NewModule((*testPost)(nil), Repo(repo), Cache(time.Millisecond))
	if m.cache == nil {
		t.Fatal("cache is nil — Cache option not applied")
	}
	// Close stopCh; the sweep goroutine must drain and exit.
	m.Stop()
	// stopCh must now be closed — a second read must not block.
	select {
	case <-m.stopCh:
		// expected
	default:
		t.Error("stopCh not closed after Stop()")
	}
}

// — A52 []string field typing and coercion ————————————————————————————————

// testSlicePost is a content type with a []string field for Amendment A52 tests.
type testSlicePost struct {
	Node
	Title string   `smeldr:"required"`
	Tags  []string `json:"tags"`
}

func (p *testSlicePost) Head() Head { return Head{Title: p.Title} }

// TestMCPSchema_arrayField verifies that []string struct fields are typed as
// "array" in MCPSchema output (Amendment A52-1).
func TestMCPSchema_arrayField(t *testing.T) {
	m := NewModule((*testSlicePost)(nil), Repo(NewMemoryRepo[*testSlicePost]()))
	fields := m.MCPSchema()
	for _, f := range fields {
		if f.Name == "Tags" {
			if f.Type != "array" {
				t.Errorf("Tags.Type = %q, want %q", f.Type, "array")
			}
			return
		}
	}
	t.Fatal("Tags field not found in MCPSchema output")
}

// TestMCPCreate_commaStringCoercion verifies that a comma-separated string
// value for a []string field is split before the Marshal→Unmarshal round-trip,
// so the decoded item slice is populated correctly (Amendment A52-3).
func TestMCPCreate_commaStringCoercion(t *testing.T) {
	repo := NewMemoryRepo[*testSlicePost]()
	m := NewModule((*testSlicePost)(nil), Repo(repo))
	ctx := NewTestContext(User{})
	item, err := m.MCPCreate(ctx, map[string]any{
		"title": "Hello World",
		"tags":  "mcp,test",
	})
	if err != nil {
		t.Fatalf("MCPCreate returned error: %v", err)
	}
	p, ok := item.(*testSlicePost)
	if !ok {
		t.Fatalf("unexpected type %T", item)
	}
	want := []string{"mcp", "test"}
	if len(p.Tags) != len(want) {
		t.Fatalf("Tags = %v, want %v", p.Tags, want)
	}
	for i := range want {
		if p.Tags[i] != want[i] {
			t.Errorf("Tags[%d] = %q, want %q", i, p.Tags[i], want[i])
		}
	}
}

// — Preview token bypass tests ————————————————————————————————————————————

func TestModule_previewToken(t *testing.T) {
	secret := []byte("test-secret-32-bytes-xxxxxxxxxxxx")
	const prefix = "/posts"

	newModule := func() (*Module[*testPost], Repository[*testPost]) {
		repo := NewMemoryRepo[*testPost]()
		m := NewModule((*testPost)(nil), At(prefix), Repo(repo))
		m.setSecret(secret)
		return m, repo
	}

	showReq := func(m *Module[*testPost], slug, token string) *httptest.ResponseRecorder {
		url := prefix + "/" + slug
		if token != "" {
			url += "?preview=" + token
		}
		r := httptest.NewRequest("GET", url, nil)
		r.SetPathValue("slug", slug)
		w := httptest.NewRecorder()
		m.showHandler(w, r)
		return w
	}

	t.Run("valid token, draft item returns 200", func(t *testing.T) {
		m, repo := newModule()
		draft := seedPost(t, repo, "Draft Post", Draft)
		token := encodePreviewToken(prefix, draft.Slug, secret, time.Hour)
		if w := showReq(m, draft.Slug, token); w.Code != http.StatusOK {
			t.Errorf("got %d, want 200", w.Code)
		}
	})

	t.Run("expired token, draft item returns 404", func(t *testing.T) {
		m, repo := newModule()
		draft := seedPost(t, repo, "Draft Post2", Draft)
		token := encodePreviewToken(prefix, draft.Slug, secret, -time.Second)
		if w := showReq(m, draft.Slug, token); w.Code != http.StatusNotFound {
			t.Errorf("got %d, want 404", w.Code)
		}
	})

	t.Run("valid token for wrong slug returns 404", func(t *testing.T) {
		m, repo := newModule()
		draft := seedPost(t, repo, "Draft Post3", Draft)
		token := encodePreviewToken(prefix, "other-slug", secret, time.Hour)
		if w := showReq(m, draft.Slug, token); w.Code != http.StatusNotFound {
			t.Errorf("got %d, want 404", w.Code)
		}
	})

	t.Run("valid token for wrong prefix (cross-module) returns 404", func(t *testing.T) {
		m, repo := newModule()
		draft := seedPost(t, repo, "Draft Post4", Draft)
		token := encodePreviewToken("/docs", draft.Slug, secret, time.Hour)
		if w := showReq(m, draft.Slug, token); w.Code != http.StatusNotFound {
			t.Errorf("got %d, want 404", w.Code)
		}
	})

	t.Run("no token, draft item, guest returns 404", func(t *testing.T) {
		m, repo := newModule()
		draft := seedPost(t, repo, "Draft Post5", Draft)
		if w := showReq(m, draft.Slug, ""); w.Code != http.StatusNotFound {
			t.Errorf("got %d, want 404", w.Code)
		}
	})

	t.Run("valid token, published item returns 200", func(t *testing.T) {
		m, repo := newModule()
		pub := seedPost(t, repo, "Published Post", Published)
		token := encodePreviewToken(prefix, pub.Slug, secret, time.Hour)
		if w := showReq(m, pub.Slug, token); w.Code != http.StatusOK {
			t.Errorf("got %d, want 200", w.Code)
		}
	})

	t.Run("valid token, archived item returns 404", func(t *testing.T) {
		m, repo := newModule()
		arch := seedPost(t, repo, "Archived Post", Archived)
		token := encodePreviewToken(prefix, arch.Slug, secret, time.Hour)
		if w := showReq(m, arch.Slug, token); w.Code != http.StatusNotFound {
			t.Errorf("got %d, want 404 (archived must not be previewable)", w.Code)
		}
	})
}

// — singleInstanceHandler —————————————————————————————————————————————————

func TestModule_singleInstanceHandler_found(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	seedPost(t, repo, "Config Page", Published)

	m := newTestModule(repo, SingleInstance())
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/testposts", nil)
	m.singleInstanceHandler(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestModule_singleInstanceHandler_empty(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	m := newTestModule(repo, SingleInstance())
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/testposts", nil)
	m.singleInstanceHandler(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (empty repo)", w.Code)
	}
}

// — updateHandler / deleteHandler notFound ————————————————————————————————

func TestModule_updateHandler_notFound(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	m := newTestModule(repo)

	w := httptest.NewRecorder()
	r := withUser(httptest.NewRequest(http.MethodPut, "/testposts/missing", strings.NewReader(`{"Title":"x"}`)), editorUser())
	r.SetPathValue("slug", "missing")
	m.updateHandler(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestModule_deleteHandler_notFound(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	m := newTestModule(repo)

	w := httptest.NewRecorder()
	r := withUser(httptest.NewRequest(http.MethodDelete, "/testposts/missing", nil), editorUser())
	r.SetPathValue("slug", "missing")
	m.deleteHandler(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

// — Middleware applied in Register ————————————————————————————————————————

func TestModule_Middleware_appliedOnRegister(t *testing.T) {
	var called atomic.Bool
	mw := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called.Store(true)
			next.ServeHTTP(w, r)
		})
	}

	repo := NewMemoryRepo[*testPost]()
	seedPost(t, repo, "Hello", Published)
	m := newTestModule(repo, Middleware(mw))

	mux := http.NewServeMux()
	m.Register(mux)

	// POST /testposts triggers the create handler (wrapped with middleware).
	body := strings.NewReader(`{"Title":"New Post"}`)
	req := withUser(httptest.NewRequest(http.MethodPost, "/testposts", body), editorUser())
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if !called.Load() {
		t.Error("middleware was not called after Register")
	}
}

// — singleInstanceHandler extended coverage ——————————————————————————————————

func TestModule_singleInstanceHandler_cacheHIT(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	seedPost(t, repo, "Config Page", Published)

	m := newTestModule(repo, SingleInstance(), Cache(time.Minute))

	// First request: MISS — populates cache.
	w1 := httptest.NewRecorder()
	r1 := httptest.NewRequest(http.MethodGet, "/testposts", nil)
	m.singleInstanceHandler(w1, r1)
	if w1.Code != http.StatusOK {
		t.Fatalf("first request status = %d, want 200", w1.Code)
	}

	// Second request: HIT — served from cache (covers the early-return branch).
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodGet, "/testposts", nil)
	m.singleInstanceHandler(w2, r2)
	if w2.Code != http.StatusOK {
		t.Fatalf("second request status = %d, want 200", w2.Code)
	}
	if w2.Header().Get("X-Cache") != "HIT" {
		t.Errorf("X-Cache = %q, want HIT", w2.Header().Get("X-Cache"))
	}
}

func TestModule_singleInstanceHandler_htmlAccept(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	seedPost(t, repo, "Config Page", Published)

	m := newTestModule(repo, SingleInstance())
	m.neg.html = true // enables text/html content negotiation

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/testposts", nil)
	r.Header.Set("Accept", "text/html")
	m.singleInstanceHandler(w, r)

	// No template set → 406 Not Acceptable, but the HTML branch was entered.
	if w.Code != http.StatusNotAcceptable {
		t.Errorf("status = %d, want 406 (no template)", w.Code)
	}
}

func TestModule_singleInstanceHandler_apiOnly_htmlRequest(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	seedPost(t, repo, "Config Page", Published)

	m := newTestModule(repo, SingleInstance())
	m.apiOnly = true

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/testposts", nil)
	r.Header.Set("Accept", "text/html")
	m.singleInstanceHandler(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for apiOnly + HTML accept", w.Code)
	}
}

func TestModule_singleInstanceHandler_previewBypass(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	draft := seedPost(t, repo, "Draft Config", Draft)

	const prefix = "/testposts"
	m := newTestModule(repo, SingleInstance())
	m.secret = []byte(testSecret)
	m.prefix = prefix

	token := encodePreviewToken(prefix, draft.Slug, []byte(testSecret), time.Hour)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, prefix+"?preview="+token, nil)
	m.singleInstanceHandler(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (preview bypass)", w.Code)
	}
}

// — createHandler extended coverage ——————————————————————————————————————————

func TestModule_createHandler_badJSON(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	m := newTestModule(repo)

	w := httptest.NewRecorder()
	r := withUser(httptest.NewRequest(http.MethodPost, "/testposts", strings.NewReader("not-json")), editorUser())
	m.createHandler(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

// — updateHandler extended coverage ——————————————————————————————————————————

func TestModule_updateHandler_badJSON(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	p := seedPost(t, repo, "My Post", Published)
	m := newTestModule(repo)

	w := httptest.NewRecorder()
	r := withUser(httptest.NewRequest(http.MethodPut, "/testposts/"+p.Slug, strings.NewReader("not-json")), editorUser())
	r.SetPathValue("slug", p.Slug)
	m.updateHandler(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestModule_updateHandler_unpublish(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	p := seedPost(t, repo, "Live Post", Published)
	m := newTestModule(repo)

	update := map[string]any{"Title": p.Title, "Status": string(Draft)}
	body, _ := json.Marshal(update)
	w := httptest.NewRecorder()
	r := withUser(httptest.NewRequest(http.MethodPut, "/testposts/"+p.Slug, bytes.NewReader(body)), editorUser())
	r.SetPathValue("slug", p.Slug)
	m.updateHandler(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	saved, _ := repo.FindBySlug(context.Background(), p.Slug)
	if nodeStatusOf(saved) != Draft {
		t.Errorf("status = %q, want Draft after unpublish", nodeStatusOf(saved))
	}
}

// — Register() extended coverage ——————————————————————————————————————————————

func TestModule_Register_singleInstance_withMiddleware(t *testing.T) {
	var called atomic.Bool
	mw := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called.Store(true)
			next.ServeHTTP(w, r)
		})
	}

	repo := NewMemoryRepo[*testPost]()
	seedPost(t, repo, "Config Page", Published)
	m := newTestModule(repo, SingleInstance(), Middleware(mw))

	mux := http.NewServeMux()
	m.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/testposts", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if !called.Load() {
		t.Error("middleware was not called for singleInstance route")
	}
}

func TestModule_Register_sitemapHandler_nilStore(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	m := NewModule((*testPost)(nil), Repo(repo), At("/posts"), SitemapConfig{Priority: 0.8})

	mux := http.NewServeMux()
	m.Register(mux)

	// sitemapStore is nil (not injected by App.Content) → handler returns 500.
	r := httptest.NewRequest(http.MethodGet, "/posts/sitemap.xml", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500 (nil sitemapStore)", w.Code)
	}
}

func TestModule_Register_feedHandler_nilStore(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	m := NewModule((*testPost)(nil), Repo(repo), At("/posts"), Feed(FeedConfig{Title: "My Feed"}))

	mux := http.NewServeMux()
	m.Register(mux)

	// feedStore is nil (not injected by App.Content) → handler returns 500.
	r := httptest.NewRequest(http.MethodGet, "/posts/feed.xml", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500 (nil feedStore)", w.Code)
	}
}

// — findAndServe / findAndServeAIDoc ——————————————————————————————————————————

func TestModule_findAndServe_previewBypass(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	draft := seedPost(t, repo, "Draft Item", Draft)

	const prefix = "/posts"
	m := newTestModule(repo)
	m.secret = []byte(testSecret)
	m.prefix = prefix

	token := encodePreviewToken(prefix, draft.Slug, []byte(testSecret), time.Hour)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/"+draft.Slug+"?preview="+token, nil)
	served := m.findAndServe(w, r, draft.Slug)

	if !served {
		t.Error("findAndServe should return true for valid preview token")
	}
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestModule_findAndServe_htmlAccept(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	pub := seedPost(t, repo, "Public Item", Published)

	m := newTestModule(repo)
	m.neg.html = true // enable HTML negotiation

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/"+pub.Slug, nil)
	r.Header.Set("Accept", "text/html")
	served := m.findAndServe(w, r, pub.Slug)

	if !served {
		t.Error("findAndServe should return true for published item")
	}
	// No template → 406.
	if w.Code != http.StatusNotAcceptable {
		t.Errorf("status = %d, want 406 (no template)", w.Code)
	}
}

func TestModule_findAndServeAIDoc_disabled(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	m := newTestModule(repo) // no AIDoc feature

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/posts/any/aidoc", nil)
	if m.findAndServeAIDoc(w, r, "any") {
		t.Error("findAndServeAIDoc should return false when AIDoc is not enabled")
	}
}

func TestModule_findAndServeAIDoc_notFound(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	m := NewModule((*testPost)(nil), Repo(repo), At("/posts"), AIIndex(AIDoc))

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/posts/missing/aidoc", nil)
	if m.findAndServeAIDoc(w, r, "missing") {
		t.Error("findAndServeAIDoc should return false when slug not found")
	}
}

func TestModule_findAndServeAIDoc_notVisible(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	draft := seedPost(t, repo, "Draft Item", Draft)
	m := NewModule((*testPost)(nil), Repo(repo), At("/posts"), AIIndex(AIDoc))

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/posts/"+draft.Slug+"/aidoc", nil)
	// Guest user — Draft is not visible.
	if m.findAndServeAIDoc(w, r, draft.Slug) {
		t.Error("findAndServeAIDoc should return false for non-visible item")
	}
}

func TestModule_findAndServeAIDoc_published(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	pub := seedPost(t, repo, "Public Item", Published)
	m := NewModule((*testPost)(nil), Repo(repo), At("/posts"), AIIndex(AIDoc))

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/posts/"+pub.Slug+"/aidoc", nil)
	served := m.findAndServeAIDoc(w, r, pub.Slug)

	if !served {
		t.Error("findAndServeAIDoc should return true for published item")
	}
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

// — typeName / autoSlugFieldPath ——————————————————————————————————————————————

func TestTypeName_pointerType(t *testing.T) {
	// typeName should deref pointer types; the loop body executes for ptr input.
	got := typeName(reflect.TypeOf((*testPost)(nil)))
	if got != "testPost" {
		t.Errorf("typeName(*testPost) = %q, want %q", got, "testPost")
	}
}

type noTitlePost struct {
	Node
	Body string `smeldr:"required"`
}

func TestAutoSlugFieldPath_requiredFallback(t *testing.T) {
	// noTitlePost has no Title/Name/Headline; falls back to the smeldr:"required" field.
	path := autoSlugFieldPath(reflect.TypeOf(noTitlePost{}))
	if path == nil {
		t.Fatal("autoSlugFieldPath should find Body as required field")
	}
}

type noSlugPost struct {
	Node
	Count int
}

func TestAutoSlugFieldPath_noSuitableField(t *testing.T) {
	// noSlugPost has no string field suitable for slug generation.
	path := autoSlugFieldPath(reflect.TypeOf(noSlugPost{}))
	if path != nil {
		t.Errorf("autoSlugFieldPath expected nil, got %v", path)
	}
}

// — autoSlug / autoSlugFieldPath pointer type coverage ————————————————————————

func TestAutoSlugFieldPath_pointerType(t *testing.T) {
	// Passing a pointer type exercises the ptr-deref loop in autoSlugFieldPath.
	path := autoSlugFieldPath(reflect.TypeOf((*testPost)(nil)))
	if path == nil {
		t.Fatal("autoSlugFieldPath(*testPost) should find Title field")
	}
}

func TestAutoSlug_pointerValue(t *testing.T) {
	// Passing a pointer value exercises the ptr-deref loop in autoSlug.
	p := &testPost{Title: "Pointer Test"}
	got := autoSlug(reflect.ValueOf(p))
	if got != "pointer-test" {
		t.Errorf("autoSlug(*testPost) = %q, want %q", got, "pointer-test")
	}
}

// — Error repository helpers —————————————————————————————————————————————————

var errRepoError = errors.New("repo error")

// errorRepo is a Repository[T] where every operation returns errRepoError.
type errorRepo[T any] struct{}

func (r errorRepo[T]) FindByID(_ context.Context, _ string) (T, error) {
	var zero T
	return zero, errRepoError
}
func (r errorRepo[T]) FindBySlug(_ context.Context, _ string) (T, error) {
	var zero T
	return zero, errRepoError
}
func (r errorRepo[T]) FindAll(_ context.Context, _ ListOptions) ([]T, error) {
	return nil, errRepoError
}
func (r errorRepo[T]) Save(_ context.Context, _ T) error        { return errRepoError }
func (r errorRepo[T]) Delete(_ context.Context, _ string) error { return errRepoError }

// savefailRepo wraps an inner repo; Save always returns errRepoError.
type savefailRepo[T any] struct{ inner Repository[T] }

func (r savefailRepo[T]) FindByID(ctx context.Context, id string) (T, error) {
	return r.inner.FindByID(ctx, id)
}
func (r savefailRepo[T]) FindBySlug(ctx context.Context, s string) (T, error) {
	return r.inner.FindBySlug(ctx, s)
}
func (r savefailRepo[T]) FindAll(ctx context.Context, o ListOptions) ([]T, error) {
	return r.inner.FindAll(ctx, o)
}
func (r savefailRepo[T]) Save(_ context.Context, _ T) error { return errRepoError }
func (r savefailRepo[T]) Delete(ctx context.Context, id string) error {
	return r.inner.Delete(ctx, id)
}

// deletefailRepo wraps an inner repo; Delete always returns errRepoError.
type deletefailRepo[T any] struct{ inner Repository[T] }

func (r deletefailRepo[T]) FindByID(ctx context.Context, id string) (T, error) {
	return r.inner.FindByID(ctx, id)
}
func (r deletefailRepo[T]) FindBySlug(ctx context.Context, s string) (T, error) {
	return r.inner.FindBySlug(ctx, s)
}
func (r deletefailRepo[T]) FindAll(ctx context.Context, o ListOptions) ([]T, error) {
	return r.inner.FindAll(ctx, o)
}
func (r deletefailRepo[T]) Save(ctx context.Context, item T) error {
	return r.inner.Save(ctx, item)
}
func (r deletefailRepo[T]) Delete(_ context.Context, _ string) error { return errRepoError }

// secondSavefailRepo: first Save call succeeds via inner, second and later fail.
type secondSavefailRepo[T any] struct {
	inner     Repository[T]
	saveCalls int
}

func (r *secondSavefailRepo[T]) FindByID(ctx context.Context, id string) (T, error) {
	return r.inner.FindByID(ctx, id)
}
func (r *secondSavefailRepo[T]) FindBySlug(ctx context.Context, s string) (T, error) {
	return r.inner.FindBySlug(ctx, s)
}
func (r *secondSavefailRepo[T]) FindAll(ctx context.Context, o ListOptions) ([]T, error) {
	return r.inner.FindAll(ctx, o)
}
func (r *secondSavefailRepo[T]) Save(ctx context.Context, item T) error {
	r.saveCalls++
	if r.saveCalls >= 2 {
		return errRepoError
	}
	return r.inner.Save(ctx, item)
}
func (r *secondSavefailRepo[T]) Delete(ctx context.Context, id string) error {
	return r.inner.Delete(ctx, id)
}

// — writeContent branch coverage —————————————————————————————————————————————

func TestWriteContent_textHTML_returns406(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	writeContent(w, r, "text/html", "any-value")
	if w.Code != http.StatusNotAcceptable {
		t.Errorf("status = %d, want 406 for text/html", w.Code)
	}
}

func TestWriteContent_textMarkdown_notMarkdownable_returns406(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	writeContent(w, r, "text/markdown", "plain-string-not-markdownable")
	if w.Code != http.StatusNotAcceptable {
		t.Errorf("status = %d, want 406 for non-Markdownable text/markdown", w.Code)
	}
}

func TestWriteContent_textPlain_notMarkdownable_jsonBody(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	writeContent(w, r, "text/plain", map[string]string{"key": "val"})
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/plain") {
		t.Errorf("Content-Type = %q, want text/plain", ct)
	}
}

// — listHandler repo error ————————————————————————————————————————————————————

func TestModule_listHandler_repoError(t *testing.T) {
	m := newTestModule(errorRepo[*testPost]{})
	w := httptest.NewRecorder()
	r := withUser(httptest.NewRequest(http.MethodGet, "/testposts", nil), editorUser())
	m.listHandler(w, r)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", w.Code)
	}
}

// — singleInstanceHandler repo error —————————————————————————————————————————

func TestModule_singleInstanceHandler_repoError(t *testing.T) {
	m := newTestModule(errorRepo[*testPost]{}, SingleInstance())
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/testposts", nil)
	m.singleInstanceHandler(w, r)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", w.Code)
	}
}

// — createHandler save error —————————————————————————————————————————————————

func TestModule_createHandler_saveError(t *testing.T) {
	// errorRepo.FindBySlug always errs → slug is unique → Save errs.
	m := newTestModule(errorRepo[*testPost]{})
	body := strings.NewReader(`{"Title":"Test Post"}`)
	w := httptest.NewRecorder()
	r := withUser(httptest.NewRequest(http.MethodPost, "/testposts", body), editorUser())
	m.createHandler(w, r)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", w.Code)
	}
}

// — updateHandler error branches —————————————————————————————————————————————

func TestModule_updateHandler_validationError(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	p := seedPost(t, repo, "Test Post", Draft)
	m := newTestModule(repo)

	update := map[string]any{"Title": ""}
	body, _ := json.Marshal(update)
	w := httptest.NewRecorder()
	r := withUser(httptest.NewRequest(http.MethodPut, "/testposts/"+p.Slug, bytes.NewReader(body)), editorUser())
	r.SetPathValue("slug", p.Slug)
	m.updateHandler(w, r)

	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", w.Code)
	}
}

func TestModule_updateHandler_beforeUpdateError(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	p := seedPost(t, repo, "Test Post", Draft)
	m := newTestModule(repo, On[*testPost](BeforeUpdate, func(_ Context, _ *testPost) error {
		return errors.New("hook error")
	}))

	update := map[string]any{"Title": p.Title}
	body, _ := json.Marshal(update)
	w := httptest.NewRecorder()
	r := withUser(httptest.NewRequest(http.MethodPut, "/testposts/"+p.Slug, bytes.NewReader(body)), editorUser())
	r.SetPathValue("slug", p.Slug)
	m.updateHandler(w, r)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", w.Code)
	}
}

func TestModule_updateHandler_saveError(t *testing.T) {
	mem := NewMemoryRepo[*testPost]()
	p := seedPost(t, mem, "Test Post", Draft)
	m := newTestModule(savefailRepo[*testPost]{inner: mem})

	update := map[string]any{"Title": p.Title}
	body, _ := json.Marshal(update)
	w := httptest.NewRecorder()
	r := withUser(httptest.NewRequest(http.MethodPut, "/testposts/"+p.Slug, bytes.NewReader(body)), editorUser())
	r.SetPathValue("slug", p.Slug)
	m.updateHandler(w, r)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", w.Code)
	}
}

func TestModule_updateHandler_secondSaveError(t *testing.T) {
	mem := NewMemoryRepo[*testPost]()
	p := seedPost(t, mem, "Test Post", Draft)
	repo := &secondSavefailRepo[*testPost]{inner: mem}
	m := newTestModule(repo)

	// Draft → Published triggers a second Save to record PublishedAt.
	update := map[string]any{"Title": p.Title, "Status": string(Published)}
	body, _ := json.Marshal(update)
	w := httptest.NewRecorder()
	r := withUser(httptest.NewRequest(http.MethodPut, "/testposts/"+p.Slug, bytes.NewReader(body)), editorUser())
	r.SetPathValue("slug", p.Slug)
	m.updateHandler(w, r)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", w.Code)
	}
}

// — deleteHandler error branches ——————————————————————————————————————————————

func TestModule_deleteHandler_beforeDeleteError(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	p := seedPost(t, repo, "Test Post", Published)
	m := newTestModule(repo, On[*testPost](BeforeDelete, func(_ Context, _ *testPost) error {
		return errors.New("hook error")
	}))

	w := httptest.NewRecorder()
	r := withUser(httptest.NewRequest(http.MethodDelete, "/testposts/"+p.Slug, nil), editorUser())
	r.SetPathValue("slug", p.Slug)
	m.deleteHandler(w, r)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", w.Code)
	}
}

func TestModule_deleteHandler_deleteError(t *testing.T) {
	mem := NewMemoryRepo[*testPost]()
	p := seedPost(t, mem, "Test Post", Published)
	m := newTestModule(deletefailRepo[*testPost]{inner: mem})

	w := httptest.NewRecorder()
	r := withUser(httptest.NewRequest(http.MethodDelete, "/testposts/"+p.Slug, nil), editorUser())
	r.SetPathValue("slug", p.Slug)
	m.deleteHandler(w, r)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", w.Code)
	}
}

// — MCPList error —————————————————————————————————————————————————————————————

func TestMCPList_repoError(t *testing.T) {
	m := newTestModule(errorRepo[*testPost]{})
	ctx := NewTestContext(editorUser())
	if _, err := m.MCPList(ctx); err == nil {
		t.Error("MCPList should return error when FindAll fails")
	}
}

// — MCPCreate error branches ——————————————————————————————————————————————————

func TestMCPCreate_marshalError(t *testing.T) {
	m := newTestModule(NewMemoryRepo[*testPost]())
	ctx := NewTestContext(editorUser())
	if _, err := m.MCPCreate(ctx, map[string]any{"Title": make(chan int)}); err == nil {
		t.Error("MCPCreate should return error for un-marshallable fields")
	}
}

func TestMCPCreate_unmarshalError(t *testing.T) {
	m := newTestModule(NewMemoryRepo[*testPost]())
	ctx := NewTestContext(editorUser())
	// JSON array cannot unmarshal into a string field.
	if _, err := m.MCPCreate(ctx, map[string]any{"Title": []int{1, 2, 3}}); err == nil {
		t.Error("MCPCreate should return error when unmarshal fails")
	}
}

func TestMCPCreate_validationError(t *testing.T) {
	m := newTestModule(NewMemoryRepo[*testPost]())
	ctx := NewTestContext(editorUser())
	if _, err := m.MCPCreate(ctx, map[string]any{}); err == nil {
		t.Error("MCPCreate should return error when validation fails")
	}
}

func TestMCPCreate_saveError(t *testing.T) {
	// errorRepo.FindBySlug errs → slug is unique → Save errs.
	m := newTestModule(errorRepo[*testPost]{})
	ctx := NewTestContext(editorUser())
	if _, err := m.MCPCreate(ctx, map[string]any{"Title": "Test"}); err == nil {
		t.Error("MCPCreate should return error when Save fails")
	}
}

// — MCPUpdate error branches ——————————————————————————————————————————————————

func TestMCPUpdate_findBySlugError(t *testing.T) {
	m := newTestModule(errorRepo[*testPost]{})
	ctx := NewTestContext(editorUser())
	if _, err := m.MCPUpdate(ctx, "any-slug", map[string]any{}); err == nil {
		t.Error("MCPUpdate should return error when FindBySlug fails")
	}
}

func TestMCPUpdate_marshalError(t *testing.T) {
	mem := NewMemoryRepo[*testPost]()
	p := seedPost(t, mem, "Test", Draft)
	m := newTestModule(mem)
	ctx := NewTestContext(editorUser())
	if _, err := m.MCPUpdate(ctx, p.Slug, map[string]any{"Title": make(chan int)}); err == nil {
		t.Error("MCPUpdate should return error for un-marshallable fields")
	}
}

func TestMCPUpdate_unmarshalError(t *testing.T) {
	mem := NewMemoryRepo[*testPost]()
	p := seedPost(t, mem, "Test", Draft)
	m := newTestModule(mem)
	ctx := NewTestContext(editorUser())
	if _, err := m.MCPUpdate(ctx, p.Slug, map[string]any{"Title": []int{1, 2, 3}}); err == nil {
		t.Error("MCPUpdate should return error when unmarshal fails")
	}
}

func TestMCPUpdate_validationError(t *testing.T) {
	mem := NewMemoryRepo[*testPost]()
	p := seedPost(t, mem, "Test Post", Draft)
	m := newTestModule(mem)
	ctx := NewTestContext(editorUser())
	if _, err := m.MCPUpdate(ctx, p.Slug, map[string]any{"Title": ""}); err == nil {
		t.Error("MCPUpdate should return error when validation fails")
	}
}

func TestMCPUpdate_saveError(t *testing.T) {
	mem := NewMemoryRepo[*testPost]()
	p := seedPost(t, mem, "Test", Draft)
	m := newTestModule(savefailRepo[*testPost]{inner: mem})
	ctx := NewTestContext(editorUser())
	if _, err := m.MCPUpdate(ctx, p.Slug, map[string]any{"Title": "New Title"}); err == nil {
		t.Error("MCPUpdate should return error when Save fails")
	}
}

// — MCPPublish error branches —————————————————————————————————————————————————

func TestMCPPublish_findBySlugError(t *testing.T) {
	m := newTestModule(errorRepo[*testPost]{})
	ctx := NewTestContext(editorUser())
	if err := m.MCPPublish(ctx, "any-slug"); err == nil {
		t.Error("MCPPublish should return error when FindBySlug fails")
	}
}

func TestMCPPublish_saveError(t *testing.T) {
	mem := NewMemoryRepo[*testPost]()
	p := seedPost(t, mem, "Test", Draft)
	m := newTestModule(savefailRepo[*testPost]{inner: mem})
	ctx := NewTestContext(editorUser())
	if err := m.MCPPublish(ctx, p.Slug); err == nil {
		t.Error("MCPPublish should return error when Save fails")
	}
}

// — MCPSchedule error branches ————————————————————————————————————————————————

func TestMCPSchedule_findBySlugError(t *testing.T) {
	m := newTestModule(errorRepo[*testPost]{})
	ctx := NewTestContext(editorUser())
	if err := m.MCPSchedule(ctx, "any-slug", time.Now().Add(time.Hour)); err == nil {
		t.Error("MCPSchedule should return error when FindBySlug fails")
	}
}

func TestMCPSchedule_saveError(t *testing.T) {
	mem := NewMemoryRepo[*testPost]()
	p := seedPost(t, mem, "Test", Draft)
	m := newTestModule(savefailRepo[*testPost]{inner: mem})
	ctx := NewTestContext(editorUser())
	if err := m.MCPSchedule(ctx, p.Slug, time.Now().Add(time.Hour)); err == nil {
		t.Error("MCPSchedule should return error when Save fails")
	}
}

// — MCPArchive error branches —————————————————————————————————————————————————

func TestMCPArchive_findBySlugError(t *testing.T) {
	m := newTestModule(errorRepo[*testPost]{})
	ctx := NewTestContext(editorUser())
	if err := m.MCPArchive(ctx, "any-slug"); err == nil {
		t.Error("MCPArchive should return error when FindBySlug fails")
	}
}

func TestMCPArchive_saveError(t *testing.T) {
	mem := NewMemoryRepo[*testPost]()
	p := seedPost(t, mem, "Test", Draft)
	m := newTestModule(savefailRepo[*testPost]{inner: mem})
	ctx := NewTestContext(editorUser())
	if err := m.MCPArchive(ctx, p.Slug); err == nil {
		t.Error("MCPArchive should return error when Save fails")
	}
}

// — MCPDelete error branches ——————————————————————————————————————————————————

func TestMCPDelete_findBySlugError(t *testing.T) {
	m := newTestModule(errorRepo[*testPost]{})
	ctx := NewTestContext(editorUser())
	if err := m.MCPDelete(ctx, "any-slug"); err == nil {
		t.Error("MCPDelete should return error when FindBySlug fails")
	}
}

func TestMCPDelete_deleteError(t *testing.T) {
	mem := NewMemoryRepo[*testPost]()
	p := seedPost(t, mem, "Test", Draft)
	m := newTestModule(deletefailRepo[*testPost]{inner: mem})
	ctx := NewTestContext(editorUser())
	if err := m.MCPDelete(ctx, p.Slug); err == nil {
		t.Error("MCPDelete should return error when Delete fails")
	}
}

// — snapshotItem non-pointer ——————————————————————————————————————————————

func TestSnapshotItem_nonPointer(t *testing.T) {
	val := snapshotItem("hello")
	if val != "hello" {
		t.Errorf("snapshotItem non-pointer: got %v want hello", val)
	}
}

func TestSnapshotItem_nilPointer(t *testing.T) {
	var p *testPost
	val := snapshotItem(p)
	if val != p {
		t.Errorf("snapshotItem nil pointer: should return unchanged")
	}
}

// — HasBlockParent non-ErrNotFound ————————————————————————————————————————

func TestHasBlockParent_nonErrNotFoundError(t *testing.T) {
	m := newTestModule(errorRepo[*testPost]{})
	ctx := NewTestContext(User{})
	ok, err := m.HasBlockParent(ctx, "any-id")
	if ok {
		t.Error("HasBlockParent should return false on repo error")
	}
	if err == nil {
		t.Error("HasBlockParent should return non-nil error on repo error")
	}
}

// — collectStats countByStatus error ——————————————————————————————————————

// errorStatusCounterRepo implements Repository[T] and statusCounter,
// with countByStatus always returning an error.
type errorStatusCounterRepo[T any] struct{}

func (r errorStatusCounterRepo[T]) FindByID(_ context.Context, _ string) (T, error) {
	var zero T
	return zero, errRepoError
}
func (r errorStatusCounterRepo[T]) FindBySlug(_ context.Context, _ string) (T, error) {
	var zero T
	return zero, errRepoError
}
func (r errorStatusCounterRepo[T]) FindAll(_ context.Context, _ ListOptions) ([]T, error) {
	return nil, errRepoError
}
func (r errorStatusCounterRepo[T]) Save(_ context.Context, _ T) error        { return errRepoError }
func (r errorStatusCounterRepo[T]) Delete(_ context.Context, _ string) error { return errRepoError }
func (r errorStatusCounterRepo[T]) countByStatus(_ context.Context) (map[Status]int, error) {
	return nil, errRepoError
}

func TestCollectStats_countByStatusError(t *testing.T) {
	m := newTestModule(errorStatusCounterRepo[*testPost]{})
	ctx := NewTestContext(User{})
	s := m.collectStats(ctx)
	if len(s.Counts) != 0 {
		t.Errorf("collectStats on error: Counts should be empty, got %v", s.Counts)
	}
}

// — notifyAfter panic recovery ————————————————————————————————————————————

func TestNotifyAfter_panicRecovery(t *testing.T) {
	m := newTestModule(NewMemoryRepo[*testPost]())
	panicked := make(chan struct{})
	m.setAfterHook(func(_ Context, _ LifecycleEvent, _ afterHookMeta, _ any) {
		close(panicked)
		panic("test panic from afterHook")
	})
	ctx := NewTestContext(User{})
	p := &testPost{Node: Node{ID: "1", Slug: "slug"}}
	m.notifyAfter(ctx, AfterCreate, "", p)
	// Wait for the goroutine to panic and recover.
	select {
	case <-panicked:
	case <-time.After(2 * time.Second):
		t.Fatal("afterHook goroutine never ran")
	}
	time.Sleep(10 * time.Millisecond) // let recover+log complete
}

// — regenerateFeed error paths ————————————————————————————————————————————

func TestRegenerateFeed_findAllError(t *testing.T) {
	m := newTestModule(errorRepo[*testPost]{}, Feed(FeedConfig{Title: "Test"}))
	m.setFeedStore(NewFeedStore("site", "https://example.com"), "https://example.com")
	ctx := NewTestContext(User{})
	m.regenerateFeed(ctx) // should return silently on FindAll error
}

func TestRegenerateFeed_singleInstance_canonical(t *testing.T) {
	mem := NewMemoryRepo[*testPost]()
	p := &testPost{Node: Node{ID: "1", Slug: "slug", Status: Published}}
	_ = mem.Save(context.Background(), p)

	m := newTestModule(mem, Feed(FeedConfig{Title: "Test"}), SingleInstance())
	m.setFeedStore(NewFeedStore("site", "https://example.com"), "https://example.com")
	ctx := NewTestContext(User{})
	m.regenerateFeed(ctx)
}

func TestRegenerateFeed_standalone_canonical(t *testing.T) {
	mem := NewMemoryRepo[*testPost]()
	p := &testPost{Node: Node{ID: "1", Slug: "my-slug", Status: Published}}
	_ = mem.Save(context.Background(), p)

	m := newTestModule(mem, Feed(FeedConfig{Title: "Test"}), Standalone())
	m.setFeedStore(NewFeedStore("site", "https://example.com"), "https://example.com")
	ctx := NewTestContext(User{})
	m.regenerateFeed(ctx)
}

// — aiDocHandler FindBySlug error ——————————————————————————————————————————

func TestAiDocHandler_findBySlugError(t *testing.T) {
	m := newTestModule(errorRepo[*testPost]{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/testposts/missing.aidoc", nil)
	r.SetPathValue("slug", "missing")
	m.aiDocHandler(w, r)
	if w.Code != http.StatusInternalServerError && w.Code != http.StatusNotFound {
		t.Errorf("aiDocHandler repo error: got %d, want 500 or 404", w.Code)
	}
}

// — regenerateSitemap error paths —————————————————————————————————————————

func TestRegenerateSitemap_findAllError(t *testing.T) {
	// sitemapPost implements SitemapNode so SitemapConfig is accepted.
	errRepo := errorRepo[*sitemapPost]{}
	m := NewModule((*sitemapPost)(nil), Repo(errRepo), SitemapConfig{})
	m.setSitemap(NewSitemapStore(), "https://example.com")
	ctx := NewTestContext(User{})
	m.regenerateSitemap(ctx) // should return silently on FindAll error
}

func TestRegenerateSitemap_singleInstance_canonical(t *testing.T) {
	mem := NewMemoryRepo[*sitemapPost]()
	p := &sitemapPost{Node: Node{ID: "1", Slug: "slug", Status: Published}}
	_ = mem.Save(context.Background(), p)

	m := NewModule((*sitemapPost)(nil), Repo(mem), SitemapConfig{}, SingleInstance())
	m.setSitemap(NewSitemapStore(), "https://example.com")
	ctx := NewTestContext(User{})
	m.regenerateSitemap(ctx)
}

func TestRegenerateSitemap_standalone_canonical(t *testing.T) {
	mem := NewMemoryRepo[*sitemapPost]()
	p := &sitemapPost{Node: Node{ID: "1", Slug: "my-slug", Status: Published}}
	_ = mem.Save(context.Background(), p)

	m := NewModule((*sitemapPost)(nil), Repo(mem), SitemapConfig{}, Standalone())
	m.setSitemap(NewSitemapStore(), "https://example.com")
	ctx := NewTestContext(User{})
	m.regenerateSitemap(ctx)
}

// — regenerateAI error paths ——————————————————————————————————————————————

func TestRegenerateAI_findAllError(t *testing.T) {
	m := newTestModule(errorRepo[*testPost]{}, AIIndex(LLMsTxt))
	m.setAIRegistry(NewLLMsStore("site"), "https://example.com")
	ctx := NewTestContext(User{})
	m.regenerateAI(ctx) // should return silently on FindAll error
}

func TestRegenerateAI_skipEmptyTitle(t *testing.T) {
	mem := NewMemoryRepo[*testPost]()
	// testPost with empty Title — should be skipped in LLMsTxt compact entries.
	p := &testPost{Node: Node{ID: "1", Slug: "s", Status: Published}, Title: ""}
	_ = mem.Save(context.Background(), p)

	m := newTestModule(mem, AIIndex(LLMsTxt))
	m.setAIRegistry(NewLLMsStore("site"), "https://example.com")
	ctx := NewTestContext(User{})
	m.regenerateAI(ctx)
}

func TestRegenerateAI_singleInstance_compact(t *testing.T) {
	mem := NewMemoryRepo[*testPost]()
	p := &testPost{Node: Node{ID: "1", Slug: "s", Status: Published}, Title: "Home"}
	_ = mem.Save(context.Background(), p)

	m := newTestModule(mem, AIIndex(LLMsTxt), SingleInstance())
	m.setAIRegistry(NewLLMsStore("site"), "https://example.com")
	ctx := NewTestContext(User{})
	m.regenerateAI(ctx)
}

func TestRegenerateAI_standalone_compact(t *testing.T) {
	mem := NewMemoryRepo[*testPost]()
	p := &testPost{Node: Node{ID: "1", Slug: "my-slug", Status: Published}, Title: "Article"}
	_ = mem.Save(context.Background(), p)

	m := newTestModule(mem, AIIndex(LLMsTxt), Standalone())
	m.setAIRegistry(NewLLMsStore("site"), "https://example.com")
	ctx := NewTestContext(User{})
	m.regenerateAI(ctx)
}

func TestRegenerateAI_full_withPublishedAt(t *testing.T) {
	mem := NewMemoryRepo[*testMDPost]()
	pub := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	p := &testMDPost{
		Node:  Node{ID: "1", Slug: "s", Status: Published, PublishedAt: pub},
		Title: "My Doc",
		Body:  "content",
	}
	_ = mem.Save(context.Background(), p)

	m := NewModule((*testMDPost)(nil), Repo(mem), AIIndex(LLMsTxtFull))
	m.setAIRegistry(NewLLMsStore("site"), "https://example.com")
	ctx := NewTestContext(User{})
	m.regenerateAI(ctx)
}

func TestRegenerateAI_singleInstance_full(t *testing.T) {
	mem := NewMemoryRepo[*testMDPost]()
	p := &testMDPost{
		Node:  Node{ID: "1", Slug: "s", Status: Published},
		Title: "Home",
		Body:  "content",
	}
	_ = mem.Save(context.Background(), p)

	m := NewModule((*testMDPost)(nil), Repo(mem), AIIndex(LLMsTxtFull), SingleInstance())
	m.setAIRegistry(NewLLMsStore("site"), "https://example.com")
	ctx := NewTestContext(User{})
	m.regenerateAI(ctx)
}

func TestRegenerateAI_standalone_full(t *testing.T) {
	mem := NewMemoryRepo[*testMDPost]()
	p := &testMDPost{
		Node:  Node{ID: "1", Slug: "my-slug", Status: Published},
		Title: "Article",
		Body:  "content",
	}
	_ = mem.Save(context.Background(), p)

	m := NewModule((*testMDPost)(nil), Repo(mem), AIIndex(LLMsTxtFull), Standalone())
	m.setAIRegistry(NewLLMsStore("site"), "https://example.com")
	ctx := NewTestContext(User{})
	m.regenerateAI(ctx)
}

// — Register with sitemapStore/feedStore nil (safety guard paths) —————————

func TestRegister_sitemapStore_nil_returns500(t *testing.T) {
	mem := NewMemoryRepo[*sitemapPost]()
	m := NewModule((*sitemapPost)(nil), Repo(mem), SitemapConfig{})
	// Register without calling setSitemap → sitemapStore is nil.
	mux := http.NewServeMux()
	m.Register(mux)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/sitemapposts/sitemap.xml", nil)
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("sitemap with nil store: got %d want 500", w.Code)
	}
}

func TestRegister_feedStore_nil_returns500(t *testing.T) {
	mem := NewMemoryRepo[*testPost]()
	m := newTestModule(mem, Feed(FeedConfig{Title: "Test"}))
	// Register without calling setFeedStore → feedStore is nil.
	mux := http.NewServeMux()
	m.Register(mux)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/testposts/feed.xml", nil)
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("feed with nil store: got %d want 500", w.Code)
	}
}

// — Governance integration tests ——————————————————————————————————————————

// govTestUser returns a User with the given ID and Author role, suitable for
// governance tests that grant by tokenID matching ctx.User().ID.
func govTestUser(id string) User { return User{ID: id, Name: "Alice", Roles: []Role{Author}} }

// govModule sets up a Module backed by MemoryRepo with a live RoleStore
// wired via setRoleStore. Returns the module and the store.
func govModule(t *testing.T) (*Module[*testPost], *RoleStore) {
	t.Helper()
	db := setupGovernanceDB(t)
	store := NewRoleStore(db)
	m := newTestModule(NewMemoryRepo[*testPost]())
	m.setRoleStore(store)
	return m, store
}

// govGrant grants roleName to tokenID in store, failing the test on error.
func govGrant(t *testing.T, store *RoleStore, tokenID, roleName string) {
	t.Helper()
	if _, err := store.Grant(context.Background(), RoleGrant{TokenID: tokenID, RoleName: roleName}); err != nil {
		t.Fatalf("govGrant(%q, %q): %v", tokenID, roleName, err)
	}
}

// canReadDrafts — Branch 1: no RoleStore → legacy HasRole(Author) check.
func TestModule_canReadDrafts_NoStore_AuthorAllowed(t *testing.T) {
	m := newTestModule(NewMemoryRepo[*testPost]())
	ctx := NewTestContext(authorUser())
	if !m.canReadDrafts(ctx) {
		t.Error("expected canReadDrafts=true for Author without governance")
	}
}

func TestModule_canReadDrafts_NoStore_GuestDenied(t *testing.T) {
	m := newTestModule(NewMemoryRepo[*testPost]())
	ctx := NewTestContext(GuestUser)
	if m.canReadDrafts(ctx) {
		t.Error("expected canReadDrafts=false for Guest without governance")
	}
}

// canReadDrafts — Branch 2: governance wired, no actor ID → deny.
func TestModule_canReadDrafts_StoreWired_NoActorID(t *testing.T) {
	m, _ := govModule(t)
	ctx := NewTestContext(GuestUser) // ID == ""
	if m.canReadDrafts(ctx) {
		t.Error("expected canReadDrafts=false for unauthenticated user when governance is wired")
	}
}

// canReadDrafts — Branch 3a: governance wired, user has grant → allow.
func TestModule_canReadDrafts_StoreWired_Authorized(t *testing.T) {
	m, store := govModule(t)
	const uid = "tok-author-read"
	govGrant(t, store, uid, "author") // author has "read"
	ctx := NewTestContext(govTestUser(uid))
	if !m.canReadDrafts(ctx) {
		t.Error("expected canReadDrafts=true for user with read grant when governance is wired")
	}
}

// canReadDrafts — Branch 3b: governance wired, user has no grant → deny.
func TestModule_canReadDrafts_StoreWired_Denied(t *testing.T) {
	m, _ := govModule(t)
	ctx := NewTestContext(govTestUser("no-grants-user"))
	if m.canReadDrafts(ctx) {
		t.Error("expected canReadDrafts=false for user with no grants when governance is wired")
	}
}

// canReadDrafts — Branch 3c: governance wired, DB error → fail-closed.
func TestModule_canReadDrafts_StoreWired_ErrorFailClosed(t *testing.T) {
	db := setupGovernanceDB(t)
	// Authorized uses QueryContext for grants — wrap that to simulate a DB error.
	wrapped := &govQueryFailDB{DB: db, failOn: "FROM smeldr_role_grants"}
	store := NewRoleStore(wrapped)
	m := newTestModule(NewMemoryRepo[*testPost]())
	m.setRoleStore(store)
	const uid = "tok-fail"
	// Grant exists in real DB but wrapped DB errors on the lookup.
	if _, err := NewRoleStore(db).Grant(context.Background(), RoleGrant{TokenID: uid, RoleName: "author"}); err != nil {
		t.Fatalf("Grant: %v", err)
	}
	ctx := NewTestContext(govTestUser(uid))
	if m.canReadDrafts(ctx) {
		t.Error("expected canReadDrafts=false (fail-closed) when Authorized returns error")
	}
}

// checkWriteOp — Branch 2: governance wired, no actor ID → deny.
func TestModule_checkWriteOp_StoreWired_NoActorID(t *testing.T) {
	m, _ := govModule(t)
	ctx := NewTestContext(GuestUser) // ID == ""
	if m.checkWriteOp(ctx, "create", Author) {
		t.Error("expected checkWriteOp=false for unauthenticated user when governance is wired")
	}
}

// checkWriteOp — Branch 3: governance wired, user has grant → allow.
func TestModule_checkWriteOp_StoreWired_Authorized(t *testing.T) {
	m, store := govModule(t)
	const uid = "tok-author-write"
	govGrant(t, store, uid, "author") // author has "create"
	ctx := NewTestContext(govTestUser(uid))
	if !m.checkWriteOp(ctx, "create", Author) {
		t.Error("expected checkWriteOp=true for user with create grant when governance is wired")
	}
}

// checkWriteOp — Branch 3: governance wired, user has no grant → deny.
func TestModule_checkWriteOp_StoreWired_Denied(t *testing.T) {
	m, _ := govModule(t)
	ctx := NewTestContext(govTestUser("no-grants-user"))
	if m.checkWriteOp(ctx, "create", Author) {
		t.Error("expected checkWriteOp=false for user with no grants when governance is wired")
	}
}

// checkWriteOp — DB error path → fail-closed.
func TestModule_checkWriteOp_StoreWired_ErrorFailClosed(t *testing.T) {
	db := setupGovernanceDB(t)
	// Authorized uses QueryContext for grants — wrap that to simulate a DB error.
	wrapped := &govQueryFailDB{DB: db, failOn: "FROM smeldr_role_grants"}
	store := NewRoleStore(wrapped)
	m := newTestModule(NewMemoryRepo[*testPost]())
	m.setRoleStore(store)
	const uid = "tok-fail-write"
	if _, err := NewRoleStore(db).Grant(context.Background(), RoleGrant{TokenID: uid, RoleName: "author"}); err != nil {
		t.Fatalf("Grant: %v", err)
	}
	ctx := NewTestContext(govTestUser(uid))
	if m.checkWriteOp(ctx, "create", Author) {
		t.Error("expected checkWriteOp=false (fail-closed) when Authorized returns error")
	}
}

// isVisible — Published items are always visible regardless of governance.
func TestModule_isVisible_Published_AlwaysVisible(t *testing.T) {
	m, _ := govModule(t)
	// GuestUser has no grants — but published items must still be visible.
	ctx := NewTestContext(GuestUser)
	item := &testPost{Node: Node{Status: Published}}
	if !m.isVisible(ctx, item) {
		t.Error("expected isVisible=true for published item regardless of governance")
	}
}

// createHandler — governance wired, unauthenticated → 403.
func TestModule_createHandler_GovernanceWired_NoActorID(t *testing.T) {
	m, _ := govModule(t)
	body, _ := json.Marshal(map[string]string{"Title": "Hello"})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/testposts", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	// No user injected → GuestUser (ID=="").
	m.createHandler(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d; want 403 when governance wired and actor ID is empty", w.Code)
	}
}

// createHandler — governance wired, authorized user → 201.
func TestModule_createHandler_GovernanceWired_Authorized(t *testing.T) {
	m, store := govModule(t)
	const uid = "tok-create-ok"
	govGrant(t, store, uid, "author")
	body, _ := json.Marshal(map[string]string{"Title": "Hello"})
	w := httptest.NewRecorder()
	r := withUser(
		httptest.NewRequest(http.MethodPost, "/testposts", bytes.NewReader(body)),
		govTestUser(uid),
	)
	r.Header.Set("Content-Type", "application/json")
	m.createHandler(w, r)
	if w.Code != http.StatusCreated {
		t.Errorf("status = %d; want 201 when governance wired and user has create grant", w.Code)
	}
}

// createHandler — governance wired, no grant → 403.
func TestModule_createHandler_GovernanceWired_Denied(t *testing.T) {
	m, _ := govModule(t)
	body, _ := json.Marshal(map[string]string{"Title": "Hello"})
	w := httptest.NewRecorder()
	r := withUser(
		httptest.NewRequest(http.MethodPost, "/testposts", bytes.NewReader(body)),
		govTestUser("no-grants-user"),
	)
	r.Header.Set("Content-Type", "application/json")
	m.createHandler(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d; want 403 when governance wired and user has no grant", w.Code)
	}
}

// — Register with module-level middleware ——————————————————————————————————

func TestRegister_withMiddleware_wrapsHandlers(t *testing.T) {
	mem := NewMemoryRepo[*testPost]()
	called := false
	mw := func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			h.ServeHTTP(w, r)
		})
	}
	m := newTestModule(mem, Middleware(mw))
	mux := http.NewServeMux()
	m.Register(mux)

	w := httptest.NewRecorder()
	r := withUser(httptest.NewRequest(http.MethodPost, "/testposts", strings.NewReader(`{"Title":"T"}`)), editorUser())
	mux.ServeHTTP(w, r)
	if !called {
		t.Error("middleware should have been called")
	}
}
