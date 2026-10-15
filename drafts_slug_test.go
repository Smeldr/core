// AGPL-3.0-or-later

package smeldr

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Drafts do not reserve a URL (core-drafts-do-not-reserve-a-url): drafts of
// web content may share a slug, a slug counter counts published items only,
// uniqueness is checked on publish and schedule against published items, and
// a slug that names more than one readable item is ambiguous.

func newWebModule(repo Repository[*testPost]) *Module[*testPost] {
	return newTestModule(repo, At("/testposts"), MCP(MCPRead, MCPWrite))
}

func wantSlugTaken(t *testing.T, err error, url, free string) {
	t.Helper()
	if !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "the URL "+url+" is already published") ||
		!strings.Contains(err.Error(), "for example "+free) {
		t.Fatalf("err = %v; want the guided conflict naming %s and %s", err, url, free)
	}
}

func TestDraftSlugs_CompiledCreateShares(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	m := newWebModule(repo)
	seedPost(t, repo, "About", Draft)
	for _, fields := range []map[string]any{{"title": "Other", "slug": "about"}, {"title": "About"}} {
		out, err := m.MCPCreate(hookCtx(), fields)
		if err != nil {
			t.Fatal(err)
		}
		if s := out.(*testPost).Slug; s != "about" {
			t.Errorf("slug = %q; want about, shared with the hidden draft", s)
		}
	}
	seedPost(t, repo, "Page", Published)
	out, err := m.MCPCreate(hookCtx(), map[string]any{"title": "Page"})
	if err != nil {
		t.Fatal(err)
	}
	if s := out.(*testPost).Slug; s != "page-2" {
		t.Errorf("slug = %q; want page-2 (the counter counts the published page)", s)
	}
}

func TestDraftSlugs_CompiledPublishChecks(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	m := newWebModule(repo)
	pub := seedPost(t, repo, "About", Published)
	draft := seedPost(t, repo, "About", Draft)

	wantSlugTaken(t, m.MCPPublish(hookCtx(), draft.ID, ""), "/testposts/about", "/testposts/about-2")
	wantSlugTaken(t, m.MCPSchedule(hookCtx(), draft.ID, time.Now().Add(time.Hour), ""), "/testposts/about", "/testposts/about-2")
	_, err := m.MCPCreate(hookCtx(), map[string]any{"title": "X", "slug": "about", "status": "published"})
	wantSlugTaken(t, err, "/testposts/about", "/testposts/about-2")
	if err := serveWrite(m.createHandler, http.MethodPost, "/testposts", "", map[string]any{"Title": "X", "Slug": "about", "Status": "published"}); !errors.As(err, new(errHTTPStatus)) || err.(errHTTPStatus).code != http.StatusConflict {
		t.Errorf("HTTP create as published = %v; want 409", err)
	}

	// Against a draft holder only, publishing succeeds.
	if err := m.MCPArchive(hookCtx(), pub.ID, ""); err != nil {
		t.Fatal(err)
	}
	if err := m.MCPPublish(hookCtx(), draft.ID, ""); err != nil {
		t.Fatalf("publish against a draft holder: %v", err)
	}
	other := seedPost(t, repo, "About", Draft)
	if err := serveWrite(m.updateHandler, http.MethodPut, "/testposts/"+other.ID, other.ID,
		map[string]any{"Title": "About", "Status": "published"}); !errors.As(err, new(errHTTPStatus)) || err.(errHTTPStatus).code != http.StatusConflict {
		t.Errorf("HTTP update into published = %v; want 409", err)
	}
}

func TestDraftSlugs_CompiledSlugChange(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	m := newWebModule(repo)
	seedPost(t, repo, "About", Published)
	draft := seedPost(t, repo, "About", Draft)
	if _, err := m.MCPUpdate(hookCtx(), draft.ID, map[string]any{"slug": "about-2"}); err != nil {
		t.Fatalf("a draft's slug change: %v", err)
	}
	if err := m.MCPPublish(hookCtx(), draft.ID, ""); err != nil {
		t.Fatalf("publish after the slug change: %v", err)
	}
	stored, _ := repo.FindByID(context.Background(), draft.ID)
	if stored.Slug != "about-2" {
		t.Errorf("slug = %q; want about-2", stored.Slug)
	}
	var ve *ValidationError
	if _, err := m.MCPUpdate(hookCtx(), draft.ID, map[string]any{"slug": "about-3"}); !errors.As(err, &ve) {
		t.Errorf("a published item's slug change = %v; want refused", err)
	}
}

func TestDraftSlugs_CompiledAmbiguity(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	m := newWebModule(repo)
	a := seedPost(t, repo, "About", Draft)
	b := seedPost(t, repo, "About", Draft)
	if _, err := m.MCPGet(hookCtx(), "about"); !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "use its ID") {
		t.Fatalf("two readable drafts = %v; want ambiguous", err)
	}
	if got, err := m.MCPGet(hookCtx(), b.ID); err != nil || got.(*testPost).ID != b.ID {
		t.Fatalf("by ID = %v, %v; want b", got, err)
	}
	// A scope that reads only a: the hidden b is not counted.
	only := &ReadScope{items: map[string]map[string]bool{m.contentTypeName: {a.ID: true}}}
	if got, err := m.resolveItemFor(hookCtx(), "about", only); err != nil || got.ID != a.ID {
		t.Errorf("hidden plus readable = %v, %v; want a", got, err)
	}
	none := &ReadScope{items: map[string]map[string]bool{}}
	if _, err := m.resolveItemFor(hookCtx(), "about", none); !errors.Is(err, ErrNotFound) {
		t.Errorf("both hidden = %v; want not found", err)
	}
	// HTTP writes resolve the same way.
	if err := serveWrite(m.deleteHandler, http.MethodDelete, "/testposts/about", "about", nil); !errors.As(err, new(errHTTPStatus)) || err.(errHTTPStatus).code != http.StatusConflict {
		t.Errorf("HTTP delete by a shared slug = %v; want 409", err)
	}
}

func TestDraftSlugs_CompiledPublicAndPreview(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	m := newTestModule(repo, At("/testposts"))
	m.secret = []byte(testSecret)
	d1 := seedPost(t, repo, "About", Draft)
	pub := seedPost(t, repo, "About", Published)
	d2 := seedPost(t, repo, "About", Draft)

	get := func(query string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/testposts/about"+query, nil)
		r.Header.Set("Accept", "application/json")
		r.SetPathValue("slug", "about")
		w := httptest.NewRecorder()
		m.showHandler(w, r)
		return w
	}
	idOf := func(w *httptest.ResponseRecorder) string {
		var p testPost
		_ = json.Unmarshal(w.Body.Bytes(), &p)
		return p.ID
	}
	if w := get(""); w.Code != http.StatusOK || idOf(w) != pub.ID {
		t.Errorf("public GET = %d %s; want the published page", w.Code, idOf(w))
	}
	tok := encodePreviewTokenForID("/testposts", "about", d2.ID, []byte(testSecret), time.Hour)
	if w := get("?preview=" + tok); w.Code != http.StatusOK || idOf(w) != d2.ID {
		t.Errorf("preview by ID = %d %s; want d2", w.Code, idOf(w))
	}
	// A slug-only token with two drafts sharing the slug opens neither.
	legacy := encodePreviewToken("/testposts", "about", []byte(testSecret), time.Hour)
	if w := get("?preview=" + legacy); idOf(w) != pub.ID {
		t.Errorf("ambiguous slug-only preview = %s; want the published page", idOf(w))
	}
	// With one draft it opens it, as before.
	if err := repo.Delete(context.Background(), d2.ID); err != nil {
		t.Fatal(err)
	}
	if w := get("?preview=" + legacy); idOf(w) != d1.ID {
		t.Errorf("slug-only preview, one draft = %s; want d1", idOf(w))
	}
	// A token for another item, or bound to a published one, opens nothing.
	if w := get("?preview=" + encodePreviewTokenForID("/testposts", "about", pub.ID, []byte(testSecret), time.Hour)); idOf(w) != pub.ID {
		t.Errorf("an ID token for a published item served %s", idOf(w))
	}
	if w := get("?preview=" + encodePreviewTokenForID("/testposts", "about", "missing", []byte(testSecret), time.Hour)); idOf(w) != pub.ID {
		t.Errorf("an ID token for a missing item served %s", idOf(w))
	}
}

func TestDraftSlugs_SchedulerWarnsOnce(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	m := newWebModule(repo)
	seedPost(t, repo, "About", Published)
	sched := seedPost(t, repo, "About", Scheduled)
	past := time.Now().Add(-time.Minute)
	sched.ScheduledAt = &past
	if err := repo.Save(context.Background(), sched); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(prev)
	defer scheduledBlockedLogged.Delete("slug/" + m.contentTypeName + "/" + sched.ID)

	for range 2 {
		if n, _, err := m.processScheduled(hookCtx(), time.Now()); err != nil || n != 0 {
			t.Fatalf("processScheduled = %d, %v; want 0 published", n, err)
		}
	}
	if c := strings.Count(buf.String(), "its URL is taken"); c != 1 {
		t.Errorf("warnings = %d; want 1 (once per item)", c)
	}
	stored, _ := repo.FindByID(context.Background(), sched.ID)
	if stored.Status != Scheduled {
		t.Errorf("status = %s; want it to stay scheduled", stored.Status)
	}
	stored.Slug = "about-2"
	if err := repo.Save(context.Background(), stored); err != nil {
		t.Fatal(err)
	}
	if n, _, err := m.processScheduled(hookCtx(), time.Now()); err != nil || n != 1 {
		t.Errorf("after the slug change = %d, %v; want 1 published", n, err)
	}
}

func TestDraftSlugs_RecordTypesKeepTheirRule(t *testing.T) {
	m := NewModule((*Decision)(nil), Repo(NewMemoryRepo[*Decision]()), At("/decisions"))
	if m.draftSlugsShared() {
		t.Error("a core record type shares draft slugs")
	}
	r := NewModule((*Rule)(nil), Repo(NewMemoryRepo[*Rule]()), At("/rules"), reservedSlugs())
	if r.draftSlugsShared() {
		t.Error("a reservedSlugs module shares draft slugs")
	}
	if err := r.publishedSlugConflict(context.Background(), &Rule{Node: Node{ID: "x", Slug: "s"}}); err != nil {
		t.Errorf("record publish check = %v; want none", err)
	}
}

// — compiled, by table (App.TransitionItemVia) —

func TestDraftSlugs_TransitionItemVia(t *testing.T) {
	app, db, repo := newTransitionHookApp(t, nil, At("/testposts"))
	ctx := TrustedRead(hookCtx())
	seedPost(t, repo, "About", Published)
	d1 := seedPost(t, repo, "About", Draft)
	d2 := seedPost(t, repo, "About", Draft)

	if _, err := app.TransitionItem(ctx, "testPost", "about", string(Published)); !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "use its ID") {
		t.Fatalf("by a shared slug = %v; want ambiguous", err)
	}
	_, err := app.TransitionItem(ctx, "testPost", d1.ID, string(Published))
	wantSlugTaken(t, err, "/testposts/about", "/testposts/about-2")
	_, err = app.TransitionItem(ctx, "testPost", d1.ID, string(Scheduled))
	wantSlugTaken(t, err, "/testposts/about", "/testposts/about-2")
	if _, err := db.Exec(`UPDATE test_posts SET slug = 'about-2' WHERE id = ?`, d2.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := app.TransitionItem(ctx, "testPost", "about-2", string(Published)); err != nil {
		t.Fatalf("a free slug: %v", err)
	}
	if err := tablePublishedSlugConflict(context.Background(), db, "no_such_table", "/x", "s", "id"); !errors.Is(err, ErrInternal) {
		t.Errorf("lookup failure = %v; want ErrInternal", err)
	}
	if err := tablePublishedSlugConflict(context.Background(), db, "test_posts", "/x", "", "id"); err != nil {
		t.Errorf("empty slug = %v; want nil", err)
	}
	if _, _, _, _, err := webContentSlugHolder(context.Background(), db, "no_such_table", "s", nil); !errors.Is(err, ErrInternal) {
		t.Errorf("holder lookup failure = %v; want ErrInternal", err)
	}
}

// — dynamic —

func newWebDynamicRepo(t *testing.T) (*DynamicTypeRepo, *sql.DB) {
	t.Helper()
	db := newSQLiteDB(t)
	for _, f := range []func(DB) error{CreateBlockTables, CreateSchemaTable, CreateStateFlowTables} {
		if err := f(db); err != nil {
			t.Fatal(err)
		}
	}
	fields, _ := json.Marshal([]SchemaField{{Name: "Title", Type: "string", Required: true, Role: "title"}})
	return NewDynamicTypeRepo(db, "recipe", &ContentTypeSchema{TypeName: "recipe", Kind: "content", URLPrefix: "/recipes", Fields: fields}), db
}

func TestDraftSlugs_Dynamic(t *testing.T) {
	repo, _ := newWebDynamicRepo(t)
	ctx := TrustedRead(context.Background())
	a, err := repo.CreateDraft(ctx, map[string]any{"Title": "Pasta"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := repo.CreateDraft(ctx, map[string]any{"Title": "Pasta"})
	if err != nil {
		t.Fatal(err)
	}
	if a.Slug != "pasta" || b.Slug != "pasta" {
		t.Fatalf("slugs %q %q; want both pasta", a.Slug, b.Slug)
	}
	if _, err := repo.Resolve(ctx, "pasta"); !errors.Is(err, ErrConflict) {
		t.Errorf("Resolve by a shared slug = %v; want ambiguous", err)
	}
	if got, err := repo.resolveFor(ctx, "pasta", func(n *DynamicNode) bool { return n.ID == b.ID }); err != nil || got.ID != b.ID {
		t.Errorf("hidden plus readable = %v, %v; want b", got, err)
	}
	if _, err := repo.resolveFor(ctx, "pasta", func(*DynamicNode) bool { return false }); !errors.Is(err, ErrNotFound) {
		t.Errorf("both hidden = %v; want not found", err)
	}
	if err := repo.SetStatus(ctx, a.ID, Published); err != nil {
		t.Fatal(err)
	}
	wantSlugTaken(t, repo.SetStatus(ctx, b.ID, Published), "/recipes/pasta", "/recipes/pasta-2")
	wantSlugTaken(t, repo.ScheduleContent(ctx, b.ID, time.Now().Add(time.Hour)), "/recipes/pasta", "/recipes/pasta-2")
	if got, err := repo.GetBySlug(ctx, "pasta"); err != nil || got.ID != a.ID {
		t.Errorf("GetBySlug = %v, %v; want the published one first", got, err)
	}

	// The publisher chooses another slug, then publishes.
	if err := repo.SetSlugVia(ctx, "mcp", b.ID, "pasta-2"); err != nil {
		t.Fatalf("SetSlugVia: %v", err)
	}
	if err := repo.SetStatus(ctx, b.ID, Published); err != nil {
		t.Fatalf("publish after the slug change: %v", err)
	}
	if err := repo.SetSlugVia(ctx, "mcp", b.ID, "pasta-3"); !errors.Is(err, ErrConflict) {
		t.Errorf("a published node's slug change = %v; want ErrConflict", err)
	}
	var ve *ValidationError
	if err := repo.SetSlugVia(ctx, "mcp", b.ID, "Not A Slug"); !errors.As(err, &ve) {
		t.Errorf("an invalid slug = %v; want a ValidationError", err)
	}
	if err := repo.SetSlugVia(ctx, "mcp", "missing", "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("a missing node = %v; want ErrNotFound", err)
	}
	c, _ := repo.CreateDraft(ctx, map[string]any{"Title": "Pasta"})
	if c.Slug != "pasta-3" {
		t.Errorf("a new draft = %q; want pasta-3 (both published slugs counted)", c.Slug)
	}
}

func TestDraftSlugs_DynamicRecordType(t *testing.T) {
	db := newSQLiteDB(t)
	for _, f := range []func(DB) error{CreateBlockTables, CreateSchemaTable, CreateStateFlowTables} {
		if err := f(db); err != nil {
			t.Fatal(err)
		}
	}
	repo := NewDynamicTypeRepo(db, "memo", &ContentTypeSchema{TypeName: "memo", Kind: "content", AddressPrefix: "ME"})
	if repo.webContent() {
		t.Fatal("a record type counts as web content")
	}
	if err := repo.SetSlugVia(context.Background(), "", "x", "y"); !errors.Is(err, ErrBadRequest) {
		t.Errorf("record SetSlugVia = %v; want ErrBadRequest", err)
	}
	if err := repo.publishedSlugConflict(context.Background(), &DynamicNode{Node: Node{ID: "x", Slug: "y"}}); err != nil {
		t.Errorf("record publish check = %v; want nil", err)
	}
}

// — EnsurePublishedSlugIndex —

func TestEnsurePublishedSlugIndex(t *testing.T) {
	db := newSQLiteDB(t)
	if _, err := db.Exec(`CREATE TABLE posts (id TEXT PRIMARY KEY, slug TEXT NOT NULL, status TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := EnsurePublishedSlugIndex(ctx, db, ""); !errors.Is(err, ErrBadRequest) {
		t.Errorf("no table = %v; want ErrBadRequest", err)
	}
	if err := EnsurePublishedSlugIndex(ctx, db, "missing"); !errors.Is(err, ErrInternal) {
		t.Errorf("missing table = %v; want ErrInternal", err)
	}
	for _, r := range [][3]string{{"1", "a", "published"}, {"2", "a", "published"}, {"3", "b", "draft"}, {"4", "b", "draft"}} {
		if _, err := db.Exec(`INSERT INTO posts VALUES (?, ?, ?)`, r[0], r[1], r[2]); err != nil {
			t.Fatal(err)
		}
	}
	if err := EnsurePublishedSlugIndex(ctx, db, "posts"); !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "a") {
		t.Fatalf("published duplicates = %v; want ErrConflict naming a", err)
	}
	if _, err := db.Exec(`UPDATE posts SET slug = 'a-2' WHERE id = '2'`); err != nil {
		t.Fatal(err)
	}
	for range 2 { // idempotent
		if err := EnsurePublishedSlugIndex(ctx, db, "posts"); err != nil {
			t.Fatalf("EnsurePublishedSlugIndex: %v", err)
		}
	}
	if _, err := db.Exec(`INSERT INTO posts VALUES ('5', 'a', 'published')`); err == nil {
		t.Error("a second published a was accepted")
	}
	if _, err := db.Exec(`INSERT INTO posts VALUES ('6', 'a', 'draft')`); err != nil {
		t.Errorf("a draft sharing a: %v", err)
	}
}

// failExecDB fails one statement matching sub.
type failExecDB struct {
	DB
	sub string
}

func (f failExecDB) ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error) {
	if strings.Contains(q, f.sub) {
		return nil, errors.New("refused")
	}
	return f.DB.ExecContext(ctx, q, args...)
}

func TestEnsurePublishedSlugIndex_CreateFails(t *testing.T) {
	db := newSQLiteDB(t)
	if _, err := db.Exec(`CREATE TABLE posts (id TEXT PRIMARY KEY, slug TEXT NOT NULL, status TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if err := EnsurePublishedSlugIndex(context.Background(), failExecDB{db, "CREATE UNIQUE INDEX"}, "posts"); !errors.Is(err, ErrInternal) {
		t.Errorf("a refused CREATE = %v; want ErrInternal", err)
	}
}

// The boot log names the outcome of the index swap once (architect binding).
func TestDynamicSlugIndex_BootLog(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(prev)

	db := newSQLiteDB(t)
	if err := CreateBlockTables(db); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "published-only slug index in place") {
		t.Errorf("log = %q; want the in-place line", buf.String())
	}
	buf.Reset()
	if err := CreateBlockTables(failExecDB{db, "DROP INDEX IF EXISTS idx_dynamic_content_type_slug"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "could not be dropped") {
		t.Errorf("log = %q; want the drop warning", buf.String())
	}
	buf.Reset()
	db2 := newSQLiteDB(t)
	if err := CreateBlockTables(db2); err != nil {
		t.Fatal(err)
	}
	if _, err := db2.Exec(`DROP INDEX idx_dynamic_content_type_slug_published`); err != nil {
		t.Fatal(err)
	}
	insertSlugRow(t, db2, "p1", "dup")
	insertSlugRow(t, db2, "p2", "dup")
	buf.Reset()
	if err := CreateBlockTables(db2); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "published-only slug index skipped") || !strings.Contains(buf.String(), "published_duplicates=1") {
		t.Errorf("log = %q; want the skipped line with one duplicate", buf.String())
	}
}
