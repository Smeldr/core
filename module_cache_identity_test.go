package smeldr

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The module cache serves and stores only the anonymous public view
// (core-response-cache-identity, read rules step 1).

func cacheGet(m *Module[*testPost], path, slug string, user *User) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, path, nil)
	if slug != "" {
		r.SetPathValue("slug", slug)
	}
	if user != nil {
		r = withUser(r, *user)
	}
	if slug != "" {
		m.showHandler(w, r)
	} else {
		m.listHandler(w, r)
	}
	return w
}

func TestModuleCache_AuthorListNeverStoredOrServedToGuest(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	seedPost(t, repo, "Public One", Published)
	seedPost(t, repo, "Secret Draft", Draft)
	m := newTestModule(repo, Cache(5*time.Minute))
	author := authorUser()

	w := cacheGet(m, "/testposts", "", &author)
	if got := w.Header().Get("X-Cache"); got != "BYPASS" {
		t.Errorf("author X-Cache = %q; want BYPASS", got)
	}
	if !strings.Contains(w.Body.String(), "Secret Draft") {
		t.Fatalf("author list lacks the draft: %s", w.Body.String())
	}
	g1 := cacheGet(m, "/testposts", "", nil)
	if got := g1.Header().Get("X-Cache"); got != "MISS" {
		t.Errorf("first guest X-Cache = %q; want MISS (nothing stored for the author)", got)
	}
	if strings.Contains(g1.Body.String(), "Secret Draft") {
		t.Fatalf("guest got the draft: %s", g1.Body.String())
	}
	g2 := cacheGet(m, "/testposts", "", nil)
	if got := g2.Header().Get("X-Cache"); got != "HIT" || g2.Body.String() != g1.Body.String() {
		t.Errorf("second guest X-Cache = %q; want HIT with the guest body", got)
	}
}

func TestModuleCache_GuestViewNeverServedToAuthor(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	seedPost(t, repo, "Public One", Published)
	seedPost(t, repo, "Secret Draft", Draft)
	m := newTestModule(repo, Cache(5*time.Minute))
	cacheGet(m, "/testposts", "", nil) // stores the guest view
	editor := editorUser()

	w := cacheGet(m, "/testposts", "", &editor)
	if got := w.Header().Get("X-Cache"); got != "BYPASS" {
		t.Errorf("editor X-Cache = %q; want BYPASS", got)
	}
	if !strings.Contains(w.Body.String(), "Secret Draft") {
		t.Errorf("editor got the cached guest view: %s", w.Body.String())
	}
}

func TestModuleCache_DraftShowNeverStored(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	draft := seedPost(t, repo, "Secret Draft", Draft)
	m := newTestModule(repo, Cache(5*time.Minute))
	editor := editorUser()

	if w := cacheGet(m, "/testposts/"+draft.Slug, draft.Slug, &editor); w.Code != http.StatusOK || w.Header().Get("X-Cache") != "BYPASS" {
		t.Fatalf("editor show = %d %q; want 200 BYPASS", w.Code, w.Header().Get("X-Cache"))
	}
	if w := cacheGet(m, "/testposts/"+draft.Slug, draft.Slug, nil); w.Code != http.StatusNotFound {
		t.Errorf("guest show after editor = %d; want 404", w.Code)
	}
}

func TestModuleCache_GovernanceReaderBypasses(t *testing.T) {
	// A governance-authorized caller has an ID and no legacy roles.
	if cacheable(NewTestContext(User{ID: "token-1"}), httptest.NewRequest(http.MethodGet, "/x", nil)) {
		t.Error("a caller with an ID is cacheable")
	}
	if cacheable(NewTestContext(User{Roles: []Role{Guest}}), httptest.NewRequest(http.MethodGet, "/x", nil)) {
		t.Error("a caller with a role is cacheable")
	}
	if !cacheable(NewTestContext(GuestUser), httptest.NewRequest(http.MethodGet, "/x", nil)) {
		t.Error("the guest is not cacheable")
	}
	if cacheable(NewTestContext(GuestUser), httptest.NewRequest(http.MethodGet, "/x?preview=t", nil)) {
		t.Error("a preview request is cacheable")
	}
}

func TestModuleCache_PreviewNeverStored(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	draft := seedPost(t, repo, "Previewed Draft", Draft)
	const prefix = "/testposts"
	m := newTestModule(repo, Cache(5*time.Minute))
	m.secret = []byte(testSecret)
	m.prefix = prefix
	token := encodePreviewToken(prefix, draft.Slug, []byte(testSecret), time.Hour)
	path := prefix + "/" + draft.Slug + "?preview=" + token

	for i := range 2 {
		w := cacheGet(m, path, draft.Slug, nil)
		if w.Code != http.StatusOK || w.Header().Get("X-Cache") != "BYPASS" {
			t.Fatalf("preview %d = %d %q; want 200 BYPASS", i, w.Code, w.Header().Get("X-Cache"))
		}
	}
	if w := cacheGet(m, prefix+"/"+draft.Slug, draft.Slug, nil); w.Code != http.StatusNotFound {
		t.Errorf("guest without the token = %d; want 404", w.Code)
	}
	expired := encodePreviewToken(prefix, draft.Slug, []byte(testSecret), -time.Hour)
	if w := cacheGet(m, prefix+"/"+draft.Slug+"?preview="+expired, draft.Slug, nil); w.Code != http.StatusNotFound {
		t.Errorf("expired token = %d; want 404", w.Code)
	}
}

func TestModuleCache_SingleInstancePreviewNeverStored(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	draft := seedPost(t, repo, "Draft Config", Draft)
	const prefix = "/testposts"
	m := newTestModule(repo, SingleInstance(), Cache(time.Minute))
	m.secret = []byte(testSecret)
	m.prefix = prefix
	token := encodePreviewToken(prefix, draft.Slug, []byte(testSecret), time.Hour)

	w := httptest.NewRecorder()
	m.singleInstanceHandler(w, httptest.NewRequest(http.MethodGet, prefix+"?preview="+token, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("preview = %d; want 200", w.Code)
	}
	g := httptest.NewRecorder()
	m.singleInstanceHandler(g, httptest.NewRequest(http.MethodGet, prefix, nil))
	if g.Code != http.StatusNotFound {
		t.Errorf("guest after preview = %d; want 404 (the preview was stored)", g.Code)
	}
}

func TestStorableResponse(t *testing.T) {
	cases := []struct {
		header http.Header
		want   bool
	}{
		{http.Header{}, true},
		{http.Header{"Cache-Control": {"max-age=60"}}, true},
		{http.Header{"Set-Cookie": {"a=b"}}, false},
		{http.Header{"Cache-Control": {"private"}}, false},
		{http.Header{"Cache-Control": {"max-age=0, No-Store"}}, false},
	}
	for _, c := range cases {
		if got := storableResponse(c.header); got != c.want {
			t.Errorf("storableResponse(%v) = %v; want %v", c.header, got, c.want)
		}
	}
}
