// AGPL-3.0-or-later

package smeldr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

// hookWritePath is one content write path of a Module[T]: the Before event it
// must dispatch, and a call that performs the write against seed (the item the
// test seeded; unused by the create paths). The call reports an error the way
// its surface does: the returned error on MCP, a non-2xx status on HTTP.
type hookWritePath struct {
	name   string
	sig    LifecycleEvent
	create bool
	delete bool
	call   func(t *testing.T, m *Module[*testPost], seed *testPost) error
}

// errHTTPStatus carries a non-2xx status from an HTTP write path.
type errHTTPStatus struct{ code int }

func (e errHTTPStatus) Error() string { return http.StatusText(e.code) }

// serveWrite runs h against a request and turns a non-2xx status into an error.
func serveWrite(h http.HandlerFunc, method, path, slug string, body any) error {
	var rdr *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	r := withUser(httptest.NewRequest(method, path, rdr), editorUser())
	if slug != "" {
		r.SetPathValue("slug", slug)
	}
	w := httptest.NewRecorder()
	h(w, r)
	if w.Code < 200 || w.Code > 299 {
		return errHTTPStatus{code: w.Code}
	}
	return nil
}

func hookCtx() Context { return NewTestContext(User{ID: "editor-1", Roles: []Role{Editor}}) }

// hookWritePaths lists every content write path. Each must run its Before hook.
func hookWritePaths() []hookWritePath {
	return []hookWritePath{
		{name: "HTTP POST", sig: BeforeCreate, create: true, call: func(_ *testing.T, m *Module[*testPost], _ *testPost) error {
			return serveWrite(m.createHandler, http.MethodPost, "/testposts", "", map[string]any{"Title": "New"})
		}},
		{name: "MCP create", sig: BeforeCreate, create: true, call: func(_ *testing.T, m *Module[*testPost], _ *testPost) error {
			_, err := m.MCPCreate(hookCtx(), map[string]any{"title": "New"})
			return err
		}},
		{name: "HTTP PUT", sig: BeforeUpdate, call: func(_ *testing.T, m *Module[*testPost], seed *testPost) error {
			return serveWrite(m.updateHandler, http.MethodPut, "/testposts/"+seed.Slug, seed.Slug,
				map[string]any{"Title": "Changed", "Status": string(seed.Status)})
		}},
		{name: "HTTP PATCH", sig: BeforeUpdate, call: func(_ *testing.T, m *Module[*testPost], seed *testPost) error {
			return serveWrite(m.patchHandler, http.MethodPatch, "/testposts/"+seed.Slug, seed.Slug, map[string]any{"Title": "Changed"})
		}},
		{name: "MCP update", sig: BeforeUpdate, call: func(_ *testing.T, m *Module[*testPost], seed *testPost) error {
			_, err := m.MCPUpdate(hookCtx(), seed.Slug, map[string]any{"title": "Changed"})
			return err
		}},
		{name: "HTTP DELETE", sig: BeforeDelete, delete: true, call: func(_ *testing.T, m *Module[*testPost], seed *testPost) error {
			return serveWrite(m.deleteHandler, http.MethodDelete, "/testposts/"+seed.Slug, seed.Slug, nil)
		}},
		{name: "MCP delete", sig: BeforeDelete, delete: true, call: func(_ *testing.T, m *Module[*testPost], seed *testPost) error {
			return m.MCPDelete(hookCtx(), seed.Slug)
		}},
	}
}

// TestBeforeHooks_RunOnEveryContentWrite proves each content write path, on
// HTTP and MCP alike, runs its Before hook exactly once.
func TestBeforeHooks_RunOnEveryContentWrite(t *testing.T) {
	for _, p := range hookWritePaths() {
		t.Run(p.name, func(t *testing.T) {
			repo := NewMemoryRepo[*testPost]()
			seed := seedPost(t, repo, "Original", Draft)
			fired := 0
			m := newTestModule(repo, On[*testPost](p.sig, func(_ Context, _ *testPost) error {
				fired++
				return nil
			}))
			if err := p.call(t, m, seed); err != nil {
				t.Fatalf("write failed: %v", err)
			}
			if fired != 1 {
				t.Errorf("%s fired %d times; want 1", p.sig, fired)
			}
		})
	}
}

// TestBeforeHooks_RefusalAbortsEveryContentWrite proves a Before hook's error
// aborts the write on every path, before anything is saved or deleted.
func TestBeforeHooks_RefusalAbortsEveryContentWrite(t *testing.T) {
	for _, p := range hookWritePaths() {
		t.Run(p.name, func(t *testing.T) {
			repo := NewMemoryRepo[*testPost]()
			seed := seedPost(t, repo, "Original", Draft)
			m := newTestModule(repo, On[*testPost](p.sig, func(_ Context, _ *testPost) error {
				return Err("title", "refused")
			}))
			err := p.call(t, m, seed)
			if err == nil {
				t.Fatal("write succeeded; want the hook's refusal")
			}
			var ve *ValidationError
			var hs errHTTPStatus
			switch {
			case errors.As(err, &hs):
				if hs.code != http.StatusUnprocessableEntity {
					t.Errorf("HTTP status = %d; want 422", hs.code)
				}
			case !errors.As(err, &ve):
				t.Errorf("error = %v; want the hook's ValidationError", err)
			}

			items, _ := repo.FindAll(context.Background(), ListOptions{})
			if len(items) != 1 {
				t.Fatalf("repo has %d items; want only the seed (nothing created, nothing deleted)", len(items))
			}
			got, ferr := repo.FindBySlug(context.Background(), seed.Slug)
			if ferr != nil {
				t.Fatalf("seed is gone: %v", ferr)
			}
			if got.Title != "Original" {
				t.Errorf("seed Title = %q; want it unchanged", got.Title)
			}
		})
	}
}

// TestBeforeHooks_MutationIsSavedOnMCP proves a Before hook that changes the
// item has that change saved on MCP, as on HTTP.
func TestBeforeHooks_MutationIsSavedOnMCP(t *testing.T) {
	setBody := func(_ Context, p *testPost) error {
		p.Body = "set by hook"
		return nil
	}

	t.Run("create", func(t *testing.T) {
		repo := NewMemoryRepo[*testPost]()
		m := newTestModule(repo, On[*testPost](BeforeCreate, setBody))
		out, err := m.MCPCreate(hookCtx(), map[string]any{"title": "New"})
		if err != nil {
			t.Fatalf("MCPCreate: %v", err)
		}
		got, err := repo.FindBySlug(context.Background(), out.(*testPost).Slug)
		if err != nil {
			t.Fatalf("FindBySlug: %v", err)
		}
		if got.Body != "set by hook" {
			t.Errorf("Body = %q; want the hook's value", got.Body)
		}
	})

	t.Run("update", func(t *testing.T) {
		repo := NewMemoryRepo[*testPost]()
		seed := seedPost(t, repo, "Original", Draft)
		m := newTestModule(repo, On[*testPost](BeforeUpdate, setBody))
		if _, err := m.MCPUpdate(hookCtx(), seed.Slug, map[string]any{"title": "Changed"}); err != nil {
			t.Fatalf("MCPUpdate: %v", err)
		}
		got, err := repo.FindBySlug(context.Background(), seed.Slug)
		if err != nil {
			t.Fatalf("FindBySlug: %v", err)
		}
		if got.Body != "set by hook" || got.Title != "Changed" {
			t.Errorf("got Title %q Body %q; want Changed / set by hook", got.Title, got.Body)
		}
	})
}

// TestBeforeSave_PublicHookRunsBeforeInternalCheck proves beforeSave's order:
// the public hook first, then the type's own internal pre-save check, on HTTP
// and MCP.
func TestBeforeSave_PublicHookRunsBeforeInternalCheck(t *testing.T) {
	paths := []hookWritePath{}
	for _, p := range hookWritePaths() {
		if !p.delete {
			paths = append(paths, p)
		}
	}
	for _, p := range paths {
		t.Run(p.name, func(t *testing.T) {
			repo := NewMemoryRepo[*testPost]()
			seed := seedPost(t, repo, "Original", Draft)
			var order []string
			m := newTestModule(repo,
				On[*testPost](p.sig, func(_ Context, _ *testPost) error {
					order = append(order, "public")
					return nil
				}),
				saveHooksOption{before: func(_ Context, _ DB, _, _ any) error {
					order = append(order, "internal")
					return nil
				}},
			)
			if err := p.call(t, m, seed); err != nil {
				t.Fatalf("write failed: %v", err)
			}
			if want := []string{"public", "internal"}; !reflect.DeepEqual(order, want) {
				t.Errorf("order = %v; want %v", order, want)
			}
		})
	}
}

// TestBeforeHooks_PanicIsErrorOnMCP proves a panicking Before hook on MCP is
// recovered into errSignalPanic and nothing is saved.
func TestBeforeHooks_PanicIsErrorOnMCP(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	m := newTestModule(repo, On[*testPost](BeforeCreate, func(_ Context, _ *testPost) error {
		panic("boom")
	}))
	if _, err := m.MCPCreate(hookCtx(), map[string]any{"title": "New"}); !errors.Is(err, errSignalPanic) {
		t.Fatalf("MCPCreate error = %v; want errSignalPanic", err)
	}
	if items, _ := repo.FindAll(context.Background(), ListOptions{}); len(items) != 0 {
		t.Errorf("repo has %d items; want none", len(items))
	}
}

// TestBeforeUpdate_NotRunOnLifecycleTransitions pins the boundary written into
// BeforeUpdate's godoc: a status-only transition through the MCP lifecycle
// tools is gated by the state flow and does not run BeforeUpdate. Changing
// that is a deliberate contract change, and this test has to change with it.
// (transition_item's compiled branch is a raw status UPDATE that never reaches
// the module, so it cannot run a module hook at all.)
func TestBeforeUpdate_NotRunOnLifecycleTransitions(t *testing.T) {
	calls := map[string]func(m *Module[*testPost], slug string) error{
		"publish": func(m *Module[*testPost], slug string) error { return m.MCPPublish(hookCtx(), slug, "") },
		"schedule": func(m *Module[*testPost], slug string) error {
			return m.MCPSchedule(hookCtx(), slug, time.Now().Add(time.Hour), "")
		},
		"archive": func(m *Module[*testPost], slug string) error { return m.MCPArchive(hookCtx(), slug, "") },
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			repo := NewMemoryRepo[*testPost]()
			seed := seedPost(t, repo, "Original", Draft)
			fired := 0
			m := newTestModule(repo, On[*testPost](BeforeUpdate, func(_ Context, _ *testPost) error {
				fired++
				return errors.New("must not run")
			}))
			if err := call(m, seed.Slug); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if fired != 0 {
				t.Errorf("BeforeUpdate fired %d times on %s; want 0", fired, name)
			}
		})
	}
}
