package smeldr

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

const redirectModuleSecret = "supersecretkey16"

// redirectApp mounts a testPost module at /posts with a fresh MemoryRepo;
// contentOpts go to App.Content, where redirect options are read.
func redirectApp(t *testing.T, contentOpts ...Option) (*App, *Module[*testPost], Repository[*testPost]) {
	t.Helper()
	repo := NewMemoryRepo[*testPost]()
	app := New(Config{BaseURL: "https://example.com", Secret: []byte(redirectModuleSecret)})
	m := newTestModule(repo, At("/posts"))
	app.Content(m, contentOpts...)
	return app, m, repo
}

// errorCode is the response's JSON error code, the part of a 404 body that
// does not vary per request (request_id does).
func errorCode(w *httptest.ResponseRecorder) string {
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	return body.Error.Code
}

func getPath(h http.Handler, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w
}

// A redirect entry at an item's own URL under a live module prefix answers
// for an archived, a deleted and a never-existing item; without an entry the
// module answers 404. Both code-registered and DB-loaded entries apply.
func TestModule_RedirectEntryOnItemURL(t *testing.T) {
	for _, source := range []string{"code", "db"} {
		for _, state := range []string{"archived", "deleted", "missing"} {
			t.Run(source+"/"+state, func(t *testing.T) {
				app, _, repo := redirectApp(t)
				slug := "never-was"
				switch state {
				case "archived":
					slug = seedPost(t, repo, "Archived Post", Archived).Slug
				case "deleted":
					p := seedPost(t, repo, "Deleted Post", Published)
					if err := repo.Delete(context.Background(), p.ID); err != nil {
						t.Fatal(err)
					}
					slug = p.Slug
				}
				gone := RedirectEntry{From: "/posts/" + slug, Code: Gone}
				moved := RedirectEntry{From: "/posts/" + slug + "-moved", To: "/elsewhere", Code: Permanent}
				switch source {
				case "code":
					app.Redirect(gone.From, "", Gone)
					app.Redirect(moved.From, moved.To, Permanent)
				case "db":
					db := newSQLiteDB(t)
					if err := CreateRoutesTable(db); err != nil {
						t.Fatal(err)
					}
					for _, e := range []RedirectEntry{gone, moved} {
						if err := NewRedirectStore().Save(context.Background(), db, e); err != nil {
							t.Fatal(err)
						}
					}
					if err := app.Redirects(db); err != nil {
						t.Fatal(err)
					}
				}
				h := app.Handler()

				if w := getPath(h, gone.From); w.Code != http.StatusGone {
					t.Errorf("Gone entry: %d, want 410", w.Code)
				}
				// The moved path never had an item; only its entry answers.
				w := getPath(h, moved.From)
				if w.Code != http.StatusMovedPermanently || w.Header().Get("Location") != "/elsewhere" {
					t.Errorf("Permanent entry: %d Location %q, want 301 /elsewhere", w.Code, w.Header().Get("Location"))
				}
				if w := getPath(h, "/posts/"+slug+"-no-entry"); w.Code != http.StatusNotFound {
					t.Errorf("no entry: %d, want 404", w.Code)
				}
			})
		}
	}
}

// A published item is served even when an entry exists for its path, and a
// valid preview of a draft wins over an entry: a stray entry never hides
// content.
func TestModule_LiveItemWinsOverRedirectEntry(t *testing.T) {
	app, m, repo := redirectApp(t)
	pub := seedPost(t, repo, "Live Post", Published)
	draft := seedPost(t, repo, "Draft Post", Draft)
	app.Redirect("/posts/"+pub.Slug, "", Gone)
	app.Redirect("/posts/"+draft.Slug, "", Gone)
	h := app.Handler()

	if w := getPath(h, "/posts/"+pub.Slug); w.Code != http.StatusOK {
		t.Errorf("published with entry: %d, want 200", w.Code)
	}
	tok := encodePreviewToken(m.prefix, draft.Slug, []byte(redirectModuleSecret), time.Hour)
	if w := getPath(h, "/posts/"+draft.Slug+"?preview="+tok); w.Code != http.StatusOK {
		t.Errorf("draft with valid preview and entry: %d, want 200", w.Code)
	}
}

// The answer depends on the entry, never on the hidden item: a draft and a
// slug that never existed answer identically under the same kind of entry,
// and identically without one.
func TestModule_RedirectAnswerDoesNotLeakExistence(t *testing.T) {
	app, _, repo := redirectApp(t)
	draft := seedPost(t, repo, "Hidden Draft", Draft)
	app.Redirect("/posts/"+draft.Slug, "/target", Permanent)
	app.Redirect("/posts/ghost", "/target", Permanent)
	h := app.Handler()

	a, b := getPath(h, "/posts/"+draft.Slug), getPath(h, "/posts/ghost")
	if a.Code != b.Code || a.Header().Get("Location") != b.Header().Get("Location") || a.Body.String() != b.Body.String() {
		t.Errorf("with entry: draft %d %q %q, missing %d %q %q, want identical",
			a.Code, a.Header().Get("Location"), a.Body.String(), b.Code, b.Header().Get("Location"), b.Body.String())
	}

	app2, _, repo2 := redirectApp(t)
	d2 := seedPost(t, repo2, "Hidden Draft", Draft)
	h2 := app2.Handler()
	a, b = getPath(h2, "/posts/"+d2.Slug), getPath(h2, "/posts/ghost")
	if a.Code != http.StatusNotFound || a.Code != b.Code || errorCode(a) != errorCode(b) || errorCode(a) != "not_found" {
		t.Errorf("without entry: draft %d %q, missing %d %q, want identical 404 not_found", a.Code, errorCode(a), b.Code, errorCode(b))
	}
}

// A prefix entry over a still-mounted prefix covers that module's missing and
// archived items, carrying the rest of the path.
func TestModule_PrefixEntryCoversMountedModule(t *testing.T) {
	app, _, repo := redirectApp(t, Redirects(From("/posts"), "/articles"))
	arch := seedPost(t, repo, "Old Post", Archived)
	pub := seedPost(t, repo, "Kept Post", Published)
	h := app.Handler()

	for _, slug := range []string{arch.Slug, "never-was"} {
		w := getPath(h, "/posts/"+slug)
		if w.Code != http.StatusMovedPermanently || w.Header().Get("Location") != "/articles/"+slug {
			t.Errorf("%s: %d Location %q, want 301 /articles/%s", slug, w.Code, w.Header().Get("Location"), slug)
		}
	}
	if w := getPath(h, "/posts/"+pub.Slug); w.Code != http.StatusOK {
		t.Errorf("published under a prefix rule: %d, want 200", w.Code)
	}
}

// A SingleInstance module with nothing published consults the store too.
func TestModule_SingleInstanceRedirectEntry(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	app := New(Config{BaseURL: "https://example.com", Secret: []byte(redirectModuleSecret)})
	app.Content(newTestModule(repo, At("/about"), SingleInstance()))
	seedPost(t, repo, "About", Archived)
	app.Redirect("/about", "", Gone)
	if w := getPath(app.Handler(), "/about"); w.Code != http.StatusGone {
		t.Errorf("single instance archived with entry: %d, want 410", w.Code)
	}
}

// A Module used without an App has no store and answers 404 as before; a nil
// store matches nothing.
func TestModule_NotFoundWithoutApp(t *testing.T) {
	m := newTestModule(NewMemoryRepo[*testPost](), At("/posts"))
	mux := http.NewServeMux()
	m.Register(mux)
	if w := getPath(mux, "/posts/never-was"); w.Code != http.StatusNotFound {
		t.Errorf("module without App: %d, want 404", w.Code)
	}
	var s *RedirectStore
	if s.serveIfMatched(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil)) {
		t.Error("nil store matched")
	}
}
