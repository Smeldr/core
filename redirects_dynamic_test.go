package smeldr_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	smeldr "smeldr.dev/core"
)

// A dynamic type's item URL follows the same rule as a compiled module: a
// draft or a missing slug answers from a redirect entry when one exists, and
// identically 404 when none does.
func TestDynamicItem_RedirectEntryOnItemURL(t *testing.T) {
	app := smeldr.New(smeldr.Config{
		BaseURL: "https://example.com",
		Secret:  []byte("test-secret-minimum16bytes"),
		DB:      openDynDB(t),
	})
	app.ServeDynamicContent()
	ctx := context.Background()
	schema := recipeSchema()
	if _, err := app.DefineContentType(ctx, schema); err != nil {
		t.Fatalf("DefineContentType: %v", err)
	}
	repo, err := app.DynamicContentRepo(schema.TypeName)
	if err != nil {
		t.Fatal(err)
	}
	draft, err := repo.CreateDraft(ctx, map[string]any{"Title": "Hidden Soup"})
	if err != nil {
		t.Fatal(err)
	}
	live, err := repo.CreateDraft(ctx, map[string]any{"Title": "Live Soup"})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetStatus(ctx, live.ID, smeldr.Published); err != nil {
		t.Fatal(err)
	}
	app.Redirect("/recipes/"+draft.Slug, "", smeldr.Gone)
	app.Redirect("/recipes/"+live.Slug, "", smeldr.Gone)
	app.Redirect("/recipes/ghost", "", smeldr.Gone)
	h := app.Handler()

	get := func(p string) int {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, p, nil))
		return w.Code
	}
	// The live item is served by the dynamic route, entry or not: this also
	// proves the item route is mounted, not the fallback answering alone.
	if got := get("/recipes/" + live.Slug); got != http.StatusOK {
		t.Errorf("published with entry: %d, want 200", got)
	}
	if got := get("/recipes/" + draft.Slug); got != http.StatusGone {
		t.Errorf("draft with Gone entry: %d, want 410", got)
	}
	if got := get("/recipes/ghost"); got != http.StatusGone {
		t.Errorf("missing with Gone entry: %d, want 410", got)
	}
	if got := get("/recipes/no-entry"); got != http.StatusNotFound {
		t.Errorf("missing without entry: %d, want 404", got)
	}
}
