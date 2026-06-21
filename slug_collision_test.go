package smeldr

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestSlugCollision_BlocksPublishInAggregate verifies that MCPPublish returns
// an error when another module in the same aggregate route already has a
// published item with the same slug.
func TestSlugCollision_BlocksPublishInAggregate(t *testing.T) {
	repo1 := NewMemoryRepo[*testPost]()
	repo2 := NewMemoryRepo[*testPost]()
	m1 := newTestModule(repo1, At("/posts"))
	m2 := newTestModule(repo2, At("/articles"))

	app := newTestApp()
	app.Content(m1)
	app.Content(m2)
	app.Route("/all", Aggregate(Serves[*testPost](m1), Serves[*testPost](m2)).List())

	ctx := context.Background()

	// Publish an item in m2 with slug "hello".
	p2 := &testPost{Node: Node{ID: NewID(), Slug: "hello", Status: Published}, Title: "Hello from articles"}
	if err := repo2.Save(ctx, p2); err != nil {
		t.Fatalf("repo2.Save: %v", err)
	}

	// Attempt to publish an item with the same slug "hello" in m1 via MCPPublish.
	p1 := &testPost{Node: Node{ID: NewID(), Slug: "hello", Status: Draft}, Title: "Hello from posts"}
	if err := repo1.Save(ctx, p1); err != nil {
		t.Fatalf("repo1.Save: %v", err)
	}

	sctx := newTestContext(t)
	err := m1.MCPPublish(sctx, "hello")
	if err == nil {
		t.Fatal("expected slug collision error, got nil")
	}
	if !strings.Contains(err.Error(), "hello") {
		t.Errorf("error should mention slug %q: %v", "hello", err)
	}

	// Item should still be Draft.
	item, _ := repo1.FindBySlug(ctx, "hello")
	if nodeStatusOf(item) != Draft {
		t.Errorf("item status = %v; want Draft", nodeStatusOf(item))
	}
}

// TestSlugCollision_AllowsPublishWhenNoConflict verifies that MCPPublish
// succeeds when no other aggregate module has a published item with this slug.
func TestSlugCollision_AllowsPublishWhenNoConflict(t *testing.T) {
	repo1 := NewMemoryRepo[*testPost]()
	repo2 := NewMemoryRepo[*testPost]()
	m1 := newTestModule(repo1, At("/posts"))
	m2 := newTestModule(repo2, At("/articles"))

	app := newTestApp()
	app.Content(m1)
	app.Content(m2)
	app.Route("/all", Aggregate(Serves[*testPost](m1), Serves[*testPost](m2)).List())

	ctx := context.Background()

	// m2 has a different slug.
	p2 := &testPost{Node: Node{ID: NewID(), Slug: "other", Status: Published}, Title: "Other"}
	if err := repo2.Save(ctx, p2); err != nil {
		t.Fatalf("repo2.Save: %v", err)
	}

	// m1 publishes "unique" — no conflict.
	p1 := &testPost{Node: Node{ID: NewID(), Slug: "unique", Status: Draft}, Title: "Unique"}
	if err := repo1.Save(ctx, p1); err != nil {
		t.Fatalf("repo1.Save: %v", err)
	}

	sctx := newTestContext(t)
	if err := m1.MCPPublish(sctx, "unique"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	item, _ := repo1.FindBySlug(ctx, "unique")
	if nodeStatusOf(item) != Published {
		t.Errorf("item status = %v; want Published", nodeStatusOf(item))
	}
}

// TestSlugCollision_NoCheckForNonAggregateModule verifies that a module that
// is NOT in an aggregate route has no slugCheckers and publishes freely.
func TestSlugCollision_NoCheckForNonAggregateModule(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	m := newTestModule(repo, At("/posts"))

	app := newTestApp()
	app.Content(m)
	// No app.Route — module not in an aggregate.

	if len(m.slugCheckers) != 0 {
		t.Errorf("slugCheckers = %d; want 0 for non-aggregate module", len(m.slugCheckers))
	}

	ctx := context.Background()
	p := &testPost{Node: Node{ID: NewID(), Slug: "free", Status: Draft}, Title: "Free"}
	if err := repo.Save(ctx, p); err != nil {
		t.Fatalf("Save: %v", err)
	}

	sctx := newTestContext(t)
	if err := m.MCPPublish(sctx, "free"); err != nil {
		t.Fatalf("unexpected error for non-aggregate module: %v", err)
	}
}

// TestSlugCollision_HTTPCreate_Blocked verifies that createHandler returns 409
// when creating directly as Published with a conflicting slug.
func TestSlugCollision_HTTPCreate_Blocked(t *testing.T) {
	repo1 := NewMemoryRepo[*testPost]()
	repo2 := NewMemoryRepo[*testPost]()
	m1 := newTestModule(repo1, At("/posts"))
	m2 := newTestModule(repo2, At("/articles"))

	app := newTestApp()
	app.Content(m1)
	app.Content(m2)
	app.Route("/all", Aggregate(Serves[*testPost](m1), Serves[*testPost](m2)).List())

	ctx := context.Background()
	p2 := &testPost{Node: Node{ID: NewID(), Slug: "clash", Status: Published}, Title: "Clash"}
	if err := repo2.Save(ctx, p2); err != nil {
		t.Fatalf("repo2.Save: %v", err)
	}

	body := `{"slug":"clash","status":"published","Title":"Clash from posts"}`
	req := httptest.NewRequest(http.MethodPost, "/posts", strings.NewReader(body))
	req = withUser(req, authorUser())
	w := httptest.NewRecorder()
	m1.createHandler(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("status = %d; want 409", w.Code)
	}
}

// newTestContext returns a minimal Context for MCP tests.
func newTestContext(t *testing.T) Context {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = withUser(req, authorUser())
	w := httptest.NewRecorder()
	return ContextFrom(w, req)
}
