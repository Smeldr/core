// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"testing"
)

// Error paths of core-drafts-do-not-reserve-a-url (drafts_slug_test.go).

// plainRepo hides MemoryRepo's slugMatcher, as a user-written repository
// would, and can fail FindBySlug.
type plainRepo struct {
	Repository[*testPost]
	failSlug bool
}

func (p plainRepo) FindBySlug(ctx context.Context, slug string) (*testPost, error) {
	if p.failSlug {
		return nil, errRepoError
	}
	return p.Repository.FindBySlug(ctx, slug)
}

func TestDraftSlugs_UserRepositoryFallsBack(t *testing.T) {
	mem := NewMemoryRepo[*testPost]()
	m := newWebModule(plainRepo{Repository: mem})
	pub := seedPost(t, mem, "About", Published)
	if got, err := m.MCPGet(hookCtx(), "about"); err != nil || got.(*testPost).ID != pub.ID {
		t.Errorf("one holder through FindBySlug = %v, %v", got, err)
	}
	if hs, err := m.slugHolders(context.Background(), "none"); err != nil || len(hs) != 0 {
		t.Errorf("no holder = %v, %v; want none", hs, err)
	}
	draft := seedPost(t, mem, "Other", Draft)
	draft.Slug = "about"
	if err := mem.Save(context.Background(), draft); err != nil {
		t.Fatal(err)
	}
	wantSlugTaken(t, m.MCPPublish(hookCtx(), draft.ID, ""), "/testposts/about", "/testposts/about-2")

	failing := newWebModule(plainRepo{Repository: mem, failSlug: true})
	if _, err := failing.slugHolders(context.Background(), "about"); !errors.Is(err, errRepoError) {
		t.Errorf("slugHolders = %v; want the repository error", err)
	}
	if err := failing.publishedSlugConflict(context.Background(), draft); !errors.Is(err, errRepoError) {
		t.Errorf("publishedSlugConflict = %v; want the repository error", err)
	}
	if _, _, err := failing.visibleSlugHolder(context.Background(), "about", nil); !errors.Is(err, errRepoError) {
		t.Errorf("visibleSlugHolder = %v; want the repository error", err)
	}
	if _, err := failing.findVisibleBySlug(hookCtx(), "about"); !errors.Is(err, errRepoError) {
		t.Errorf("findVisibleBySlug = %v; want the repository error", err)
	}
}

// suggestFailRepo answers the first slug lookup and fails the next, so the
// free-slug suggestion's lookup fails.
type suggestFailRepo struct {
	Repository[*testPost]
	calls *int
}

func (s suggestFailRepo) FindBySlug(ctx context.Context, slug string) (*testPost, error) {
	*s.calls++
	if *s.calls > 1 {
		return nil, errRepoError
	}
	return s.Repository.FindBySlug(ctx, slug)
}

func TestDraftSlugs_SuggestionLookupFails(t *testing.T) {
	mem := NewMemoryRepo[*testPost]()
	calls := 0
	m := newWebModule(suggestFailRepo{Repository: mem, calls: &calls})
	seedPost(t, mem, "About", Published)
	draft := &testPost{Node: Node{ID: NewID(), Slug: "about", Status: Draft}, Title: "x"}
	if err := m.publishedSlugConflict(context.Background(), draft); !errors.Is(err, errRepoError) {
		t.Errorf("err = %v; want the suggestion's lookup error", err)
	}
}

func TestDraftSlugs_HTTPWriteByID(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	m := newWebModule(repo)
	a := seedPost(t, repo, "About", Draft)
	seedPost(t, repo, "About", Draft)
	if err := serveWrite(m.updateHandler, http.MethodPut, "/testposts/"+a.ID, a.ID, map[string]any{"Title": "Changed"}); err != nil {
		t.Fatalf("PUT by ID: %v", err)
	}
	stored, _ := repo.FindByID(context.Background(), a.ID)
	if stored.Title != "Changed" || stored.Slug != "about" {
		t.Errorf("stored = %q %q; want Changed, slug kept", stored.Title, stored.Slug)
	}
	if err := serveWrite(m.deleteHandler, http.MethodDelete, "/testposts/missing", "missing", nil); !errors.As(err, new(errHTTPStatus)) || err.(errHTTPStatus).code != http.StatusNotFound {
		t.Errorf("missing = %v; want 404", err)
	}
	if _, err := m.findVisibleBySlug(NewTestContext(GuestUser), a.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("hidden by ID = %v; want not found", err)
	}
}

// failSlugQueryDB fails every query whose text holds sub.
type failSlugQueryDB struct {
	DB
	sub string
}

func (f failSlugQueryDB) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	if strings.Contains(q, f.sub) {
		return nil, errors.New("query refused")
	}
	return f.DB.QueryContext(ctx, q, args...)
}

func (f failSlugQueryDB) QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row {
	if strings.Contains(q, f.sub) {
		return f.DB.QueryRowContext(ctx, "SELECT x FROM no_such_table")
	}
	return f.DB.QueryRowContext(ctx, q, args...)
}

func TestDraftSlugs_DynamicErrorPaths(t *testing.T) {
	repo, db := newWebDynamicRepo(t)
	ctx := TrustedRead(context.Background())
	a, err := repo.CreateDraft(ctx, map[string]any{"Title": "Pasta"})
	if err != nil {
		t.Fatal(err)
	}
	broken := NewDynamicTypeRepo(failSlugQueryDB{db, "status = 'published' AND id <> $3"}, "recipe", repo.schema)
	if err := broken.publishedSlugConflict(ctx, a); !errors.Is(err, ErrInternal) {
		t.Errorf("publishedSlugConflict = %v; want ErrInternal", err)
	}
	if err := repo.publishedSlugConflict(ctx, &DynamicNode{}); err != nil {
		t.Errorf("empty slug = %v; want nil", err)
	}
	noHolders := NewDynamicTypeRepo(failSlugQueryDB{db, "ORDER BY CASE WHEN status"}, "recipe", repo.schema)
	if _, err := noHolders.Resolve(ctx, "pasta"); err == nil {
		t.Error("Resolve with a failing holder query = nil; want the error")
	}
	failUpdate := NewDynamicTypeRepo(failExecDB{db, "SET slug = $1"}, "recipe", repo.schema)
	if err := failUpdate.SetSlugVia(ctx, "", a.ID, "pasta-9"); err == nil {
		t.Error("SetSlugVia with a failing UPDATE = nil; want the error")
	}
}

func TestDraftSlugs_TableHelpersErrors(t *testing.T) {
	db := newSQLiteDB(t)
	if _, err := db.Exec(`CREATE TABLE t (id TEXT, slug TEXT, status TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO t VALUES ('1', 'a', 'published'), ('2', 'a-2', 'published')`); err != nil {
		t.Fatal(err)
	}
	calls := 0
	if err := tablePublishedSlugConflict(context.Background(), countFailDB{DB: db, after: 1, calls: &calls}, "t", "/x", "a", "9"); !errors.Is(err, ErrInternal) {
		t.Errorf("suggestion lookup failure = %v; want ErrInternal", err)
	}
	if err := tablePublishedSlugConflict(context.Background(), db, "t", "/x", "a", "9"); !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "/x/a-3") {
		t.Errorf("conflict = %v; want the suggestion /x/a-3", err)
	}
	if _, _, _, hit, err := webContentSlugHolder(context.Background(), db, "t", "a", func(string) bool { return false }); err != nil || hit {
		t.Errorf("hidden holder = %v, %v; want a miss", hit, err)
	}
}

// countFailDB answers QueryRowContext normally for the first `after` calls,
// then fails it.
type countFailDB struct {
	DB
	after int
	calls *int
}

func (c countFailDB) QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row {
	*c.calls++
	if *c.calls > c.after {
		return c.DB.QueryRowContext(ctx, "SELECT x FROM no_such_table")
	}
	return c.DB.QueryRowContext(ctx, q, args...)
}

func TestEnsurePublishedSlugIndex_ScanFails(t *testing.T) {
	db := newSQLiteDB(t)
	if _, err := db.Exec(`CREATE TABLE posts (id TEXT PRIMARY KEY, slug TEXT NOT NULL, status TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if err := EnsurePublishedSlugIndex(context.Background(), failSlugQueryDB{db, "HAVING COUNT(*) > 1"}, "posts"); !errors.Is(err, ErrInternal) {
		t.Errorf("a failing scan = %v; want ErrInternal", err)
	}
}
