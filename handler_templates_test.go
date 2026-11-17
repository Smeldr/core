package smeldr

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// T1317: app.Handler() parses templates, so a program that serves it with its
// own server answers HTML the same way app.Run does.

// templatedApp is an App with one published post under /posts whose module
// has the templates in dir.
func templatedApp(t *testing.T, dir string) *App {
	t.Helper()
	repo := NewMemoryRepo[*tdPost]()
	if err := repo.Save(context.Background(), &tdPost{Node: Node{ID: "p1", Slug: "hello", Status: Published}, Title: "Hello"}); err != nil {
		t.Fatal(err)
	}
	app := New(MustConfig(Config{BaseURL: "https://example.com", Secret: []byte("16bytessecretkey")}))
	app.Content(NewModule((*tdPost)(nil), Repo(repo), At("/posts"), Templates(dir)))
	return app
}

// getAccept sends GET path with accept (none when empty) and returns the answer.
func getAccept(h http.Handler, path, accept string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	if accept != "" {
		r.Header.Set("Accept", accept)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestHandler_ParsesTemplates(t *testing.T) {
	dir := t.TempDir()
	writeTplFile(t, dir, "list.html", `<h1>list</h1>`)
	writeTplFile(t, dir, "show.html", `<h1>{{.Content.Title}}</h1>`)
	h := templatedApp(t, dir).Handler()
	for _, c := range []struct{ path, accept, wantType, wantBody string }{
		{"/posts/hello", "text/html", "text/html", "<h1>Hello</h1>"},
		{"/posts/hello", "", "text/html", "<h1>Hello</h1>"},
		{"/posts/hello", "application/json", "application/json", `"Title":"Hello"`},
		{"/posts", "text/html", "text/html", "<h1>list</h1>"},
	} {
		w := getAccept(h, c.path, c.accept)
		if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), c.wantType) || !strings.Contains(w.Body.String(), c.wantBody) {
			t.Errorf("GET %s Accept %q: %d %q %q; want %s with %q", c.path, c.accept, w.Code, w.Header().Get("Content-Type"), w.Body.String(), c.wantType, c.wantBody)
		}
	}
}

func TestHandler_TemplatesWithPartials(t *testing.T) {
	dir, partials := t.TempDir(), t.TempDir()
	writeTplFile(t, dir, "list.html", `<p>list</p>`)
	writeTplFile(t, dir, "show.html", `<h1>{{.Content.Title}}</h1>{{template "footer" .}}`)
	writeTplFile(t, partials, "footer.html", `{{define "footer"}}<footer>site-footer</footer>{{end}}`)
	app := templatedApp(t, dir)
	app.Partials(partials)
	if w := getAccept(app.Handler(), "/posts/hello", "text/html"); !strings.Contains(w.Body.String(), "site-footer") {
		t.Errorf("partial not rendered through Handler: %d %q", w.Code, w.Body.String())
	}
}

// A template that does not parse: Handler logs it once at ERROR, naming the
// module's prefix and the file, and the module answers JSON; Run refuses to start.
func TestHandler_TemplateErrorLoggedOnce(t *testing.T) {
	dir := t.TempDir()
	writeTplFile(t, dir, "list.html", `<p>list</p>`)
	writeTplFile(t, dir, "show.html", `{{.Content.Title`)
	app := templatedApp(t, dir)
	var logged bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, nil)))
	h := app.Handler()
	_ = app.Handler()
	slog.SetDefault(prev)
	if n := strings.Count(logged.String(), "templates did not parse"); n != 1 {
		t.Errorf("ERROR lines = %d, want 1: %s", n, logged.String())
	}
	if !strings.Contains(logged.String(), "/posts") || !strings.Contains(logged.String(), "show.html") || !strings.Contains(logged.String(), "level=ERROR") {
		t.Errorf("the line does not name the prefix and file: %s", logged.String())
	}
	if w := getAccept(h, "/posts/hello", "text/html"); !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
		t.Errorf("a module whose templates did not parse answers %q, want JSON", w.Header().Get("Content-Type"))
	}
	if err := templatedApp(t, dir).Run("127.0.0.1:0"); err == nil || !strings.Contains(err.Error(), "show.html") {
		t.Errorf("Run with a broken template = %v, want the parse error", err)
	}
}

// A missing partials directory is one ERROR line through Handler, and the
// module still parses its own templates.
func TestHandler_MissingPartialsDir(t *testing.T) {
	dir := t.TempDir()
	writeTplFile(t, dir, "list.html", `<p>list</p>`)
	writeTplFile(t, dir, "show.html", `<h1>{{.Content.Title}}</h1>`)
	app := templatedApp(t, dir)
	app.Partials("/nonexistent-dir-that-does-not-exist")
	var logged bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, nil)))
	h := app.Handler()
	slog.SetDefault(prev)
	if !strings.Contains(logged.String(), "partials directory") {
		t.Errorf("no ERROR line for the partials directory: %s", logged.String())
	}
	if w := getAccept(h, "/posts/hello", "text/html"); !strings.Contains(w.Body.String(), "<h1>Hello</h1>") {
		t.Errorf("the module's own templates are not served: %q", w.Body.String())
	}
}

// Handler serving while the race detector watches: concurrent requests to a
// handler whose templates were parsed by Handler (modules registered first).
func TestHandler_TemplatesConcurrent(t *testing.T) {
	dir := t.TempDir()
	writeTplFile(t, dir, "list.html", `<p>list</p>`)
	writeTplFile(t, dir, "show.html", `<h1>{{.Content.Title}}</h1>`)
	h := templatedApp(t, dir).Handler()
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if w := getAccept(h, "/posts/hello", "text/html"); !strings.Contains(w.Body.String(), "Hello") {
				t.Errorf("concurrent GET: %q", w.Body.String())
			}
		}()
	}
	wg.Wait()
}
