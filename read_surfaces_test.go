package smeldr

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Per-item read rules on the item surfaces (core-read-surfaces 3a): a record
// the caller may not read answers exactly like a missing one.

type readSurfaceFixture struct {
	app            *App
	db             *sql.DB
	rs             *RoleStore
	tasks          *Module[*Task]
	seen, hidden   *Task
	reader, global Context
}

func newReadSurfaceFixture(t *testing.T) readSurfaceFixture {
	t.Helper()
	ctx := context.Background()
	db := addressDB(t)
	setupTokensTable(t, db)
	app := New(Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16"), DB: db})
	RegisterOrchestrationTypes(app, db)
	rs := NewRoleStore(db)
	if err := app.Governance(rs); err != nil {
		t.Fatal(err)
	}
	_ = app.Handler()
	if err := RegisterScopedReaderRole(ctx, rs); err != nil {
		t.Fatal(err)
	}
	f := readSurfaceFixture{app: app, db: db, rs: rs, tasks: findModule[*Task](t, app)}
	seed := TrustedRead(authorCtx()).(Context)
	for _, title := range []string{"seen", "hidden"} {
		out, err := f.tasks.MCPCreate(seed, map[string]any{"Description": title, "Band": "core"})
		if err != nil {
			t.Fatal(err)
		}
		if title == "seen" {
			f.seen = asItem[*Task](out)
		} else {
			f.hidden = asItem[*Task](out)
		}
	}
	if _, err := rs.Grant(ctx, RoleGrant{TokenID: "reader", RoleName: "scoped-reader", ScopeStatic: []string{"Task:" + f.seen.ID}}); err != nil {
		t.Fatal(err)
	}
	govGrant(t, rs, "global", "admin")
	f.reader = NewTestContext(User{ID: "reader"})
	f.global = NewTestContext(User{ID: "global"})
	return f
}

func TestReadSurfaces_MCPGetHiddenEqualsMissing(t *testing.T) {
	f := newReadSurfaceFixture(t)
	if _, err := f.tasks.MCPGet(f.reader, f.seen.Slug); err != nil {
		t.Fatalf("readable: %v", err)
	}
	_, hiddenErr := f.tasks.MCPGet(f.reader, f.hidden.Slug)
	_, missingErr := f.tasks.MCPGet(f.reader, "no-such-task")
	if !errors.Is(hiddenErr, ErrNotFound) || !errors.Is(missingErr, ErrNotFound) || hiddenErr.Error() != missingErr.Error() {
		t.Errorf("hidden %v, missing %v; want the same not-found", hiddenErr, missingErr)
	}
	for _, ident := range []string{f.hidden.Address, f.hidden.ID} {
		if _, err := f.tasks.MCPGet(f.reader, ident); !errors.Is(err, ErrNotFound) {
			t.Errorf("hidden by %q: %v", ident, err)
		}
	}
	if _, err := f.tasks.MCPGet(f.global, f.hidden.Slug); err != nil {
		t.Errorf("global reader: %v", err)
	}
}

func TestReadSurfaces_WritesOnHiddenAreNotFound(t *testing.T) {
	f := newReadSurfaceFixture(t)
	cases := map[string]func() error{
		"update": func() error {
			_, err := f.tasks.MCPUpdate(f.reader, f.hidden.Slug, map[string]any{"Band": "site"})
			return err
		},
		"publish": func() error { return f.tasks.MCPPublish(f.reader, f.hidden.Slug, "") },
		"archive": func() error { return f.tasks.MCPArchive(f.reader, f.hidden.Slug, "") },
		"delete":  func() error { return f.tasks.MCPDelete(f.reader, f.hidden.Slug) },
		"transition": func() error {
			_, err := f.app.TransitionItemVia(f.reader, "mcp", "Task", f.hidden.Slug, "active", "")
			return err
		},
	}
	for name, call := range cases {
		if err := call(); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s on a hidden Task: %v; want ErrNotFound", name, err)
		}
	}
	got, err := f.tasks.MCPGet(f.global, f.hidden.Slug)
	if err != nil || asItem[*Task](got).Band != "core" || asItem[*Task](got).Status != f.hidden.Status {
		t.Errorf("hidden Task changed: %+v %v", got, err)
	}
}

func TestReadSurfaces_TransitionForbiddenOnlyWhenReadable(t *testing.T) {
	f := newReadSurfaceFixture(t)
	// The reader can read "seen" but holds no transition right: forbidden or a
	// flow error, never not-found.
	_, err := f.app.TransitionItemVia(f.reader, "mcp", "Task", f.seen.Slug, "done", "")
	if errors.Is(err, ErrNotFound) {
		t.Errorf("a readable Task answered not-found: %v", err)
	}
	if _, err := f.app.TransitionItemVia(f.reader, "mcp", "Task", f.hidden.Slug, "done", ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("a hidden Task: %v; want ErrNotFound", err)
	}
}

func TestReadSurfaces_MCPListFiltered(t *testing.T) {
	f := newReadSurfaceFixture(t)
	items, err := f.tasks.MCPList(f.reader)
	if err != nil || len(items) != 1 || items[0].(*Task).ID != f.seen.ID {
		t.Errorf("reader list = %v %v; want only the readable Task", items, err)
	}
	all, err := f.tasks.MCPList(f.global)
	if err != nil || len(all) != 2 {
		t.Errorf("global list = %d %v; want 2", len(all), err)
	}
	if _, err := f.tasks.MCPList(NewTestContext(User{ID: "nobody"})); err != nil {
		t.Errorf("a reader of nothing: %v", err)
	}
}

func TestReadSurfaces_ScopeLoadFailureIsInternal(t *testing.T) {
	f := newReadSurfaceFixture(t)
	if _, err := f.db.Exec(`DROP TABLE smeldr_role_grants`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.tasks.MCPGet(f.reader, f.seen.Slug); !errors.Is(err, ErrInternal) {
		t.Errorf("MCPGet: %v; want ErrInternal, never not-found", err)
	}
	if _, err := f.tasks.MCPList(f.reader); !errors.Is(err, ErrInternal) {
		t.Errorf("MCPList: %v; want ErrInternal", err)
	}
	if _, err := f.app.TransitionItemVia(f.reader, "mcp", "Task", f.seen.Slug, "active", ""); !errors.Is(err, ErrInternal) {
		t.Errorf("TransitionItemVia: %v; want ErrInternal", err)
	}
}

func TestReadSurfaces_TrustedReadAndAttachedScope(t *testing.T) {
	f := newReadSurfaceFixture(t)
	if _, err := f.tasks.MCPGet(TrustedRead(f.reader).(Context), f.hidden.Slug); err != nil {
		t.Errorf("TrustedRead: %v", err)
	}
	s, err := f.app.ReadScope(f.reader)
	if err != nil {
		t.Fatal(err)
	}
	attached := WithReadScope(f.reader, s)
	if got, ok := ReadScopeFrom(attached); !ok || got != s {
		t.Error("the attached scope is not returned")
	}
	if _, ok := attached.(Context); !ok {
		t.Error("WithReadScope on a Context did not return a Context")
	}
	if _, ok := ReadScopeFrom(WithReadScope(context.Background(), s)); !ok {
		t.Error("WithReadScope on a plain context lost the scope")
	}
	if _, ok := ReadScopeFrom(context.Background()); ok {
		t.Error("a bare context has a scope")
	}
	if got := attached.(Context).User().ID; got != "reader" {
		t.Errorf("the user was lost: %q", got)
	}
}

func TestReadSurfaces_ScopedSlugNeverRevealsAHiddenHolder(t *testing.T) {
	f := newReadSurfaceFixture(t)
	out, err := f.tasks.MCPCreate(f.reader, map[string]any{"Title": "mine", "Description": "mine", "Slug": f.hidden.Slug, "Band": "core"})
	if err != nil {
		t.Fatalf("create with a taken slug: %v", err)
	}
	m := out.(map[string]any)
	slug, _ := m["slug"].(string)
	if slug == "" {
		slug, _ = m["Slug"].(string)
	}
	if slug == f.hidden.Slug || !strings.HasPrefix(slug, f.hidden.Slug+"-") {
		t.Errorf("slug = %q; want the supplied slug with an ID tail", slug)
	}
	ws, _ := m["warnings"].([]CreateWarning)
	if len(ws) != 1 || ws[0].Code != "slug_adjusted" || ws[0].Field != "slug" {
		t.Errorf("warnings = %v; want one slug_adjusted", m["warnings"])
	}
	// An automatic slug that is taken gets an ID tail, not a counter, for a
	// scoped caller; a global reader keeps the counter.
	decisions := findModule[*Decision](t, f.app)
	if _, err := decisions.MCPCreate(TrustedRead(authorCtx()).(Context), map[string]any{"Title": "Pick a vendor", "Scope": "core"}); err != nil {
		t.Fatal(err)
	}
	scoped, err := decisions.MCPCreate(f.reader, map[string]any{"Title": "Pick a vendor", "Scope": "core"})
	if err != nil {
		t.Fatal(err)
	}
	if s := asItem[*Decision](scoped).Slug; s == "pick-a-vendor-2" || !strings.HasPrefix(s, "pick-a-vendor-") {
		t.Errorf("scoped automatic slug = %q; want an ID tail", s)
	}
	global, err := decisions.MCPCreate(f.global, map[string]any{"Title": "Pick a vendor", "Scope": "core"})
	if err != nil || asItem[*Decision](global).Slug != "pick-a-vendor-2" {
		t.Errorf("global automatic slug = %v %v; want the counter", global, err)
	}
}

// The module's own HTTP routes (web content, testPost): a scoped reader sees
// published items plus the drafts it may read.
func TestReadSurfaces_HTTPDraftsPerItem(t *testing.T) {
	m, store := govModule(t)
	repo := m.repo.(*MemoryRepo[*testPost])
	pub := seedPost(t, repo, "Public", Published)
	mine := seedPost(t, repo, "Mine", Draft)
	other := seedPost(t, repo, "Other", Draft)
	if err := RegisterScopedReaderRole(context.Background(), store); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Grant(context.Background(), RoleGrant{TokenID: "reader", RoleName: "scoped-reader", ScopeStatic: []string{"testPost:" + mine.ID}}); err != nil {
		t.Fatal(err)
	}
	reader := govTestUser("reader")
	get := func(path, slug string, u *User) *httptest.ResponseRecorder {
		return cacheGet(m, path, slug, u)
	}
	w := get("/testposts", "", &reader)
	body := w.Body.String()
	if !strings.Contains(body, "Public") || !strings.Contains(body, "Mine") || strings.Contains(body, "Other") {
		t.Errorf("reader list = %s", body)
	}
	if w := get("/testposts/"+mine.Slug, mine.Slug, &reader); w.Code != http.StatusOK {
		t.Errorf("readable draft = %d", w.Code)
	}
	hidden := get("/testposts/"+other.Slug, other.Slug, &reader)
	missing := get("/testposts/nope", "nope", &reader)
	if hidden.Code != http.StatusNotFound || hidden.Code != missing.Code || withoutRequestID(hidden.Body.String()) != withoutRequestID(missing.Body.String()) {
		t.Errorf("hidden %d %q, missing %d %q; want the same 404", hidden.Code, hidden.Body.String(), missing.Code, missing.Body.String())
	}
	if w := get("/testposts/"+pub.Slug, pub.Slug, nil); w.Code != http.StatusOK {
		t.Errorf("published for a guest = %d", w.Code)
	}
	// Published web content is public through MCPGet too.
	if _, err := m.MCPGet(NewTestContext(GuestUser), pub.Slug); err != nil {
		t.Errorf("MCPGet published for a guest: %v", err)
	}
}

func TestReadSurfaces_DynamicAdminFiltered(t *testing.T) {
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
	a, _ := repo.CreateDraft(TrustedRead(ctx), map[string]any{"Title": "a"})
	b, _ := repo.CreateDraft(TrustedRead(ctx), map[string]any{"Title": "b"})
	if err := RegisterScopedReaderRole(ctx, rs); err != nil {
		t.Fatal(err)
	}
	if _, err := rs.Grant(ctx, RoleGrant{TokenID: "reader", RoleName: "scoped-reader", ScopeStatic: []string{"invoice:" + a.ID}}); err != nil {
		t.Fatal(err)
	}
	auth := authFuncFor(User{ID: "reader"})
	call := func(h http.Handler, path string, values map[string]string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		for k, v := range values {
			r.SetPathValue(k, v)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	list := call(newAdminListHandler(app, auth), "/_content/invoice?per_page=1", map[string]string{"type": "invoice"})
	var got struct {
		Items []map[string]any `json:"items"`
		Total int              `json:"total"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &got); err != nil || len(got.Items) != 1 || got.Items[0]["ID"] != a.ID || got.Total != 1 {
		t.Errorf("admin list = %s; want only a", list.Body.String())
	}
	hidden := call(newAdminGetHandler(app, auth), "/_content/invoice/"+b.ID, map[string]string{"type": "invoice", "id": b.ID})
	missing := call(newAdminGetHandler(app, auth), "/_content/invoice/nope", map[string]string{"type": "invoice", "id": "nope"})
	if hidden.Code != http.StatusNotFound || withoutRequestID(hidden.Body.String()) != withoutRequestID(missing.Body.String()) {
		t.Errorf("hidden %d %q, missing %q", hidden.Code, hidden.Body.String(), missing.Body.String())
	}
	if w := call(newAdminListHandler(app, authFuncFor(User{ID: "nobody"})), "/_content/invoice", map[string]string{"type": "invoice"}); w.Code != http.StatusForbidden {
		t.Errorf("a caller reading nothing of the type = %d; want 403", w.Code)
	}
	// A scoped creator of a dynamic record gets an ID tail, not a counter.
	c, err := repo.CreateDraft(NewTestContext(User{ID: "reader"}), map[string]any{"Title": "a"})
	if err != nil || c.Slug == a.Slug+"-2" || !strings.HasPrefix(c.Slug, a.Slug+"-") {
		t.Errorf("scoped dynamic slug = %q %v", c.Slug, err)
	}
}

// authFuncFor is an AuthFunc that always authenticates u.
type fixedAuth struct{ u User }

func (f fixedAuth) authenticate(*http.Request) (User, bool) { return f.u, true }

func authFuncFor(u User) AuthFunc { return fixedAuth{u: u} }

// withoutRequestID drops the per-request request_id from an error body, the
// one field that differs between any two responses.
func withoutRequestID(body string) string {
	if i := strings.Index(body, `"request_id":"`); i >= 0 {
		if j := strings.Index(body[i+len(`"request_id":"`):], `"`); j >= 0 {
			return body[:i] + body[i+len(`"request_id":"`)+j+1:]
		}
	}
	return body
}

// stmtDB records the text of every statement it runs.
type stmtDB struct {
	DB
	stmts []string
}

func (d *stmtDB) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	d.stmts = append(d.stmts, q)
	return d.DB.QueryContext(ctx, q, args...)
}

func (d *stmtDB) QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row {
	d.stmts = append(d.stmts, q)
	return d.DB.QueryRowContext(ctx, q, args...)
}

func (d *stmtDB) ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error) {
	d.stmts = append(d.stmts, q)
	return d.DB.ExecContext(ctx, q, args...)
}

// Requirement 1 (timing): for a scoped caller, a hidden ident and a missing
// ident of the same shape run exactly the same statements, on every resolver
// (the module's, TransitionItemVia's compiled lookup, and the dynamic one).
func TestReadSurfaces_HiddenAndMissingRunTheSameStatements(t *testing.T) {
	ctx := context.Background()
	raw := addressDB(t)
	setupTokensTable(t, raw)
	if err := CreateBlockTables(raw); err != nil {
		t.Fatal(err)
	}
	if err := CreateSchemaTable(raw); err != nil {
		t.Fatal(err)
	}
	db := &stmtDB{DB: raw}
	app := New(Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16"), DB: db})
	app.ServeDynamicContent()
	RegisterOrchestrationTypes(app, db)
	rs := NewRoleStore(db)
	if err := app.Governance(rs); err != nil {
		t.Fatal(err)
	}
	_ = app.Handler()
	tasks := findModule[*Task](t, app)
	seed := TrustedRead(authorCtx()).(Context)
	hiddenTask, err := tasks.MCPCreate(seed, map[string]any{"Description": "hidden"})
	if err != nil {
		t.Fatal(err)
	}
	ht := asItem[*Task](hiddenTask)
	if _, err := raw.Exec(`INSERT INTO smeldr_tasks (id, slug, status, created_at, updated_at, task_id) VALUES ('legacy-1', 'legacy-1', 'backlog', datetime('now'), datetime('now'), 'T-OLD1')`); err != nil {
		t.Fatal(err)
	}
	if _, err := app.DefineContentType(ctx, schemaFor("invoice", "", "")); err != nil {
		t.Fatal(err)
	}
	repo, _ := app.DynamicContentRepo("invoice")
	inv, err := repo.CreateDraft(TrustedRead(ctx), map[string]any{"Title": "hidden"})
	if err != nil {
		t.Fatal(err)
	}
	_ = RegisterScopedReaderRole(ctx, rs)
	if _, err := rs.Grant(ctx, RoleGrant{TokenID: "reader", RoleName: "scoped-reader", ScopeStatic: []string{"Goal:*"}}); err != nil {
		t.Fatal(err)
	}
	reader := NewTestContext(User{ID: "reader"})
	// Load the reader's scope once so both sides start from the same state.
	if _, err := app.ReadScope(reader); err != nil {
		t.Fatal(err)
	}
	record := func(call func() error) ([]string, error) {
		db.stmts = nil
		err := call()
		return append([]string(nil), db.stmts...), err
	}
	same := func(name string, hidden, missing func() error) {
		t.Helper()
		h, herr := record(hidden)
		m, merr := record(missing)
		if !errors.Is(herr, ErrNotFound) || !errors.Is(merr, ErrNotFound) || herr.Error() != merr.Error() {
			t.Errorf("%s: hidden %v, missing %v; want the same not-found", name, herr, merr)
		}
		if strings.Join(h, "\n") != strings.Join(m, "\n") {
			t.Errorf("%s: hidden ran\n  %s\nmissing ran\n  %s", name, strings.Join(h, "\n  "), strings.Join(m, "\n  "))
		}
	}
	get := func(ident string) func() error {
		return func() error { _, err := tasks.MCPGet(reader, ident); return err }
	}
	move := func(typeName, ident string) func() error {
		return func() error {
			_, err := app.TransitionItemVia(reader, "mcp", typeName, ident, "active", "")
			return err
		}
	}
	shapes := []struct{ name, hidden, missing string }{
		{"address", ht.Address, "T999"},
		{"slug", ht.Slug, "no-such-task"},
		{"ID", ht.ID, NewID()},
		{"legacy", "T-OLD1", "T-OLD2"},
	}
	for _, s := range shapes {
		same("MCPGet by "+s.name, get(s.hidden), get(s.missing))
		same("TransitionItemVia by "+s.name, move("Task", s.hidden), move("Task", s.missing))
	}
	for _, s := range []struct{ name, hidden, missing string }{
		{"address", inv.Address, "INV999"},
		{"slug", inv.Slug, "no-such-invoice"},
		{"ID", inv.ID, NewID()},
	} {
		same("dynamic TransitionItemVia by "+s.name, move("invoice", s.hidden), move("invoice", s.missing))
	}
}
