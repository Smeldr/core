package smeldr

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A scope load failure on a module's HTTP read and write routes is an internal
// error (500), never a not-found (binding addition D).
func TestReadSurfaces_HTTPScopeFailureIs500(t *testing.T) {
	db := setupGovernanceDB(t)
	store := NewRoleStore(&govQueryFailDB{DB: db, failOn: "FROM smeldr_role_grants"})
	repo := NewMemoryRepo[*testPost]()
	p := seedPost(t, repo, "Post", Draft)
	m := newTestModule(repo, AIIndex(AIDoc))
	m.setRoleStore(store)
	user := govTestUser("u")
	do := func(name string, h func(http.ResponseWriter, *http.Request), method, path string) {
		t.Helper()
		r := withUser(httptest.NewRequest(method, path, nil), user)
		r.SetPathValue("slug", p.Slug)
		w := httptest.NewRecorder()
		h(w, r)
		if w.Code != http.StatusInternalServerError {
			t.Errorf("%s = %d; want 500", name, w.Code)
		}
	}
	do("list", m.listHandler, http.MethodGet, "/testposts")
	do("show", m.showHandler, http.MethodGet, "/testposts/"+p.Slug)
	do("aidoc", m.aiDocHandler, http.MethodGet, "/testposts/"+p.Slug+"/aidoc")
	single := newTestModule(repo, SingleInstance())
	single.setRoleStore(store)
	do("single", single.singleInstanceHandler, http.MethodGet, "/testposts")
	for name, serve := range map[string]func(http.ResponseWriter, *http.Request, string) bool{
		"findAndServe": m.findAndServe, "findAndServeAIDoc": m.findAndServeAIDoc,
	} {
		w := httptest.NewRecorder()
		if !serve(w, withUser(httptest.NewRequest(http.MethodGet, "/x", nil), user), p.Slug) || w.Code != http.StatusInternalServerError {
			t.Errorf("%s = %d; want served with 500", name, w.Code)
		}
	}
	if _, err := m.findVisibleBySlug(NewTestContext(user), p.Slug); !errors.Is(err, ErrInternal) {
		t.Errorf("findVisibleBySlug: %v", err)
	}
	if _, err := m.findVisibleBySlug(NewTestContext(GuestUser), "missing"); err == nil {
		t.Error("findVisibleBySlug of a missing slug succeeded")
	}
}

// A hidden draft under a scoped reader: findVisibleBySlug, findAndServe and
// findAndServeAIDoc answer as missing.
func TestReadSurfaces_FindHelpersHideDrafts(t *testing.T) {
	m, _ := govModule(t)
	m.aiFeatures = []AIFeature{AIDoc}
	repo := m.repo.(*MemoryRepo[*testPost])
	p := seedPost(t, repo, "Draft", Draft)
	reader := govTestUser("reader")
	if _, err := m.findVisibleBySlug(NewTestContext(reader), p.Slug); !errors.Is(err, ErrNotFound) {
		t.Errorf("findVisibleBySlug: %v", err)
	}
	for name, serve := range map[string]func(http.ResponseWriter, *http.Request, string) bool{
		"findAndServe": m.findAndServe, "findAndServeAIDoc": m.findAndServeAIDoc,
	} {
		if serve(httptest.NewRecorder(), withUser(httptest.NewRequest(http.MethodGet, "/x", nil), reader), p.Slug) {
			t.Errorf("%s served a hidden draft", name)
		}
	}
}

// A record type's create under a failing scope load is an internal error.
func TestReadSurfaces_CreateScopeFailure(t *testing.T) {
	db := addressDB(t)
	setupTokensTable(t, db)
	app := New(Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16"), DB: db})
	RegisterOrchestrationTypes(app, db)
	if err := app.Governance(NewRoleStore(db)); err != nil {
		t.Fatal(err)
	}
	_ = app.Handler()
	tasks := findModule[*Task](t, app)
	tasks.setRoleStore(NewRoleStore(&govQueryFailDB{DB: db, failOn: "FROM smeldr_role_grants"}))
	if _, err := tasks.MCPCreate(NewTestContext(User{ID: "u"}), map[string]any{"Description": "x"}); !errors.Is(err, ErrInternal) {
		t.Errorf("MCPCreate: %v; want ErrInternal", err)
	}
	w := httptest.NewRecorder()
	r := withUser(httptest.NewRequest(http.MethodPost, "/tasks", nil), User{ID: "u", Roles: []Role{Admin}})
	tasks.createHandler(w, r)
	if w.Code == http.StatusCreated {
		t.Errorf("createHandler = %d; want a failure", w.Code)
	}
}

func TestIDSlug_FullIDWhenTailTaken(t *testing.T) {
	id := "01a14030-adde-7965-9bc9-07b9d88ccb6f"
	taken := func(s string) bool { return s == "x-d88ccb6f" }
	if got := idSlug("x", id, taken); got != "x-"+id {
		t.Errorf("idSlug = %q; want the whole ID", got)
	}
	if got := idSlug("x", "short", func(string) bool { return false }); got != "x-short" {
		t.Errorf("idSlug short = %q", got)
	}
}

func TestPageMaps(t *testing.T) {
	items := []map[string]any{{"ID": "a"}, {"ID": "b"}, {"ID": "c"}}
	if got := pageMaps(items, ListOptions{}); len(got) != 3 {
		t.Errorf("no paging = %d", len(got))
	}
	if got := pageMaps(items, ListOptions{Page: 2, PerPage: 2}); len(got) != 1 || got[0]["ID"] != "c" {
		t.Errorf("page 2 = %v", got)
	}
	if got := pageMaps(items, ListOptions{Page: 5, PerPage: 2}); len(got) != 0 {
		t.Errorf("past the end = %v", got)
	}
}

func TestAdminReadScope_Branches(t *testing.T) {
	ctx := context.Background()
	legacy := New(Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16")})
	if _, err := legacy.adminReadScope(ctx, User{Roles: []Role{Author}}, true, "x"); !errors.Is(err, ErrForbidden) {
		t.Errorf("legacy Author: %v; want forbidden", err)
	}
	if s, err := legacy.adminReadScope(ctx, User{Roles: []Role{Editor}}, true, "x"); err != nil || !s.All() {
		t.Errorf("legacy Editor: %v %v", s, err)
	}
	db := setupGovernanceDB(t)
	gov := New(Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16"), DB: db})
	if err := gov.Governance(NewRoleStore(db)); err != nil {
		t.Fatal(err)
	}
	if _, err := gov.adminReadScope(ctx, User{ID: "u"}, false, "x"); !errors.Is(err, ErrForbidden) {
		t.Errorf("unauthenticated: %v", err)
	}
	gov.governance = NewRoleStore(&govQueryFailDB{DB: db, failOn: "FROM smeldr_role_grants"})
	if _, err := gov.adminReadScope(ctx, User{ID: "u"}, true, "x"); !errors.Is(err, ErrInternal) {
		t.Errorf("load failure: %v; want ErrInternal", err)
	}
}

// A dynamic record create under a failing scope load is an internal error, and
// a reader of everything keeps the numbered slugs.
func TestDynamicNewSlug_Branches(t *testing.T) {
	ctx := context.Background()
	app, db := dynamicAddressApp(t)
	setupTokensTable(t, db)
	if err := migrateGovernance(ctx, db); err != nil {
		t.Fatal(err)
	}
	rs := NewRoleStore(db)
	if err := app.Governance(rs); err != nil {
		t.Fatal(err)
	}
	if _, err := app.DefineContentType(ctx, schemaFor("invoice", "", "")); err != nil {
		t.Fatal(err)
	}
	repo, _ := app.DynamicContentRepo("invoice")
	a, _ := repo.CreateDraft(TrustedRead(ctx), map[string]any{"Title": "same"})
	b, _ := repo.CreateDraft(TrustedRead(ctx), map[string]any{"Title": "same"})
	if b.Slug != a.Slug+"-2" {
		t.Errorf("trusted second slug = %q after %q; want the counter", b.Slug, a.Slug)
	}
	failing := repo.WithGovernance(NewRoleStore(&govQueryFailDB{DB: db, failOn: "FROM smeldr_role_grants"}))
	if _, err := failing.CreateDraft(NewTestContext(User{ID: "u"}), map[string]any{"Title": "z"}); !errors.Is(err, ErrInternal) {
		t.Errorf("failing scope: %v; want ErrInternal", err)
	}
}

// The legacy "matches N records" error counts records the caller may not read,
// so a scoped caller gets not-found; a global reader keeps the count.
func TestReadSurfaces_LegacyAmbiguityHidden(t *testing.T) {
	f := newReadSurfaceFixture(t)
	for _, id := range []string{"amb-1", "amb-2"} {
		if _, err := f.db.Exec(`INSERT INTO smeldr_tasks (id, slug, status, created_at, updated_at, task_id) VALUES ($1, $1, 'backlog', datetime('now'), datetime('now'), 'T-OLD')`, id); err != nil {
			t.Fatal(err)
		}
	}
	var ve *ValidationError
	if _, err := f.tasks.MCPGet(f.global, "T-OLD"); !errors.As(err, &ve) {
		t.Errorf("global: %v; want the ambiguity error", err)
	}
	if _, err := f.tasks.MCPGet(f.reader, "T-OLD"); !errors.Is(err, ErrNotFound) {
		t.Errorf("scoped MCPGet: %v; want ErrNotFound", err)
	}
	if _, err := f.app.TransitionItemVia(f.reader, "mcp", "Task", "T-OLD", "active", ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("scoped transition: %v; want ErrNotFound", err)
	}
	if _, err := f.app.TransitionItemVia(f.global, "mcp", "Task", "T-OLD", "active", ""); !errors.As(err, &ve) {
		t.Errorf("global transition: %v; want the ambiguity error", err)
	}
}

// The /_content routes answer a scope load failure with 500.
func TestReadSurfaces_DynamicAdminScopeFailure(t *testing.T) {
	ctx := context.Background()
	app, db := dynamicAddressApp(t)
	setupTokensTable(t, db)
	if err := migrateGovernance(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := app.Governance(NewRoleStore(db)); err != nil {
		t.Fatal(err)
	}
	if _, err := app.DefineContentType(ctx, schemaFor("invoice", "", "")); err != nil {
		t.Fatal(err)
	}
	app.governance = NewRoleStore(&govQueryFailDB{DB: db, failOn: "FROM smeldr_role_grants"})
	auth := authFuncFor(User{ID: "u"})
	for name, h := range map[string]http.Handler{"list": newAdminListHandler(app, auth), "get": newAdminGetHandler(app, auth)} {
		r := httptest.NewRequest(http.MethodGet, "/_content/invoice/x", nil)
		r.SetPathValue("type", "invoice")
		r.SetPathValue("id", "x")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusInternalServerError {
			t.Errorf("%s = %d; want 500", name, w.Code)
		}
	}
}
