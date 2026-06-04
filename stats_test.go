package smeldr

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// ContentTypeStats — JSON serialisation
// ---------------------------------------------------------------------------

func TestContentTypeStats_JSON(t *testing.T) {
	s := ContentTypeStats{
		TypeName: "Post",
		Prefix:   "/posts",
		Counts:   map[Status]int{Draft: 2, Published: 5},
	}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(b)
	for _, want := range []string{`"type":"Post"`, `"prefix":"/posts"`, `"counts"`} {
		if !strings.Contains(got, want) {
			t.Errorf("JSON missing %q: %s", want, got)
		}
	}
}

func TestSiteStats_ExternalOmitEmpty(t *testing.T) {
	s := SiteStats{
		Content:     []ContentTypeStats{},
		GeneratedAt: "2026-01-01T00:00:00Z",
	}
	b, _ := json.Marshal(s)
	if strings.Contains(string(b), `"external"`) {
		t.Errorf("external should be omitted when nil: %s", b)
	}
}

// ---------------------------------------------------------------------------
// MemoryRepo.countByStatus
// ---------------------------------------------------------------------------

type statsPost struct {
	Node
	Title string `db:"title"`
}

func (p *statsPost) Head() Head { return Head{} }

func TestMemoryRepo_countByStatus(t *testing.T) {
	repo := NewMemoryRepo[*statsPost]()
	ctx := context.Background()

	items := []*statsPost{
		{Node: Node{ID: "1", Slug: "a", Status: Draft}},
		{Node: Node{ID: "2", Slug: "b", Status: Published}},
		{Node: Node{ID: "3", Slug: "c", Status: Published}},
		{Node: Node{ID: "4", Slug: "d", Status: Archived}},
	}
	for _, it := range items {
		if err := repo.Save(ctx, it); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	sc, ok := any(repo).(statusCounter)
	if !ok {
		t.Fatal("MemoryRepo does not implement statusCounter")
	}
	counts, err := sc.countByStatus(ctx)
	if err != nil {
		t.Fatalf("countByStatus: %v", err)
	}
	if counts[Draft] != 1 {
		t.Errorf("Draft = %d; want 1", counts[Draft])
	}
	if counts[Published] != 2 {
		t.Errorf("Published = %d; want 2", counts[Published])
	}
	if counts[Archived] != 1 {
		t.Errorf("Archived = %d; want 1", counts[Archived])
	}
}

// ---------------------------------------------------------------------------
// SQLRepo.countByStatus
// ---------------------------------------------------------------------------

func TestSQLRepo_countByStatus(t *testing.T) {
	db := newSQLiteDB(t)
	_, err := db.ExecContext(context.Background(), `
		CREATE TABLE stats_posts (
			id TEXT PRIMARY KEY, slug TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'draft',
			created_at DATETIME NOT NULL DEFAULT '',
			updated_at DATETIME NOT NULL DEFAULT '',
			published_at DATETIME,
			scheduled_at DATETIME,
			title TEXT NOT NULL DEFAULT ''
		)`)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}
	repo := NewSQLRepo[*statsPost](db, Table("stats_posts"))
	ctx := context.Background()

	statuses := []Status{Draft, Draft, Published, Published, Published, Archived}
	for i, s := range statuses {
		it := &statsPost{Node: Node{
			ID: "id" + string(rune('0'+i)), Slug: string(rune('a' + i)), Status: s,
		}}
		if err := repo.Save(ctx, it); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	sc, ok := any(repo).(statusCounter)
	if !ok {
		t.Fatal("SQLRepo does not implement statusCounter")
	}
	counts, err := sc.countByStatus(ctx)
	if err != nil {
		t.Fatalf("countByStatus: %v", err)
	}
	if counts[Draft] != 2 {
		t.Errorf("Draft = %d; want 2", counts[Draft])
	}
	if counts[Published] != 3 {
		t.Errorf("Published = %d; want 3", counts[Published])
	}
	if counts[Archived] != 1 {
		t.Errorf("Archived = %d; want 1", counts[Archived])
	}
}

// ---------------------------------------------------------------------------
// App.Stats — aggregation
// ---------------------------------------------------------------------------

func TestApp_Stats_TwoModules(t *testing.T) {
	app := newTestApp()
	ctx := context.Background()

	repo1 := NewMemoryRepo[*statsPost]()
	_ = repo1.Save(ctx, &statsPost{Node: Node{ID: "1", Slug: "a", Status: Published}})
	_ = repo1.Save(ctx, &statsPost{Node: Node{ID: "2", Slug: "b", Status: Draft}})
	m := NewModule((*statsPost)(nil), At("/posts"), Repo(repo1))
	app.Content(m)

	stats, err := app.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if len(stats.Content) != 1 {
		t.Fatalf("len(Content) = %d; want 1", len(stats.Content))
	}
	c := stats.Content[0]
	if c.TypeName != "statsPost" {
		t.Errorf("TypeName = %q; want statsPost", c.TypeName)
	}
	if c.Counts[Published] != 1 || c.Counts[Draft] != 1 {
		t.Errorf("Counts = %v; want published=1 draft=1", c.Counts)
	}
	if stats.GeneratedAt == "" {
		t.Error("GeneratedAt must not be empty")
	}
	if stats.External != nil {
		t.Error("External should be nil when no providers registered")
	}
}

func TestApp_Stats_Empty(t *testing.T) {
	app := newTestApp()
	stats, err := app.Stats(context.Background())
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.Content == nil {
		t.Error("Content must be non-nil (empty slice)")
	}
	if len(stats.Content) != 0 {
		t.Errorf("Content = %d; want 0 modules", len(stats.Content))
	}
}

func TestApp_Stats_ExternalProvider(t *testing.T) {
	app := newTestApp()
	app.RegisterStatsProvider(&fakeStatsProvider{key: "media", data: map[string]any{"files": 7}})

	stats, err := app.Stats(context.Background())
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.External == nil {
		t.Fatal("External must not be nil when provider registered")
	}
	media, ok := stats.External["media"].(map[string]any)
	if !ok || media["files"] != 7 {
		t.Errorf("External[media] = %v; want {files:7}", stats.External["media"])
	}
}

func TestApp_Stats_ProviderErrorDegrades(t *testing.T) {
	app := newTestApp()
	app.RegisterStatsProvider(&fakeStatsProvider{key: "media", err: errors.New("boom")})
	app.RegisterStatsProvider(&fakeStatsProvider{key: "other", data: map[string]any{"x": 1}})

	stats, err := app.Stats(context.Background())
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	// media failed — must not appear; other succeeded.
	if _, has := stats.External["media"]; has {
		t.Error("failing provider should be omitted from External")
	}
	if stats.External["other"] == nil {
		t.Error("successful provider must appear in External")
	}
}

// ---------------------------------------------------------------------------
// GET /_stats — HTTP handler
// ---------------------------------------------------------------------------

func TestStatsHandler_Admin200(t *testing.T) {
	app := newTestApp()
	repo := NewMemoryRepo[*statsPost]()
	_ = repo.Save(context.Background(), &statsPost{Node: Node{ID: "1", Slug: "a", Status: Published}})
	app.Content(NewModule((*statsPost)(nil), At("/posts"), Repo(repo)))
	app.StatsHandler()

	token, _ := SignToken(User{Roles: []Role{Admin}}, "test-secret-key!!", 0)
	req := httptest.NewRequest(http.MethodGet, "/_stats", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	app.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d; want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q; want application/json", ct)
	}
	var result SiteStats
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(result.Content) != 1 {
		t.Errorf("content length = %d; want 1", len(result.Content))
	}
	if result.GeneratedAt == "" {
		t.Error("generated_at must not be empty")
	}
}

func TestStatsHandler_Editor403(t *testing.T) {
	app := newTestApp()
	app.StatsHandler()

	token, _ := SignToken(User{Roles: []Role{Editor}}, "test-secret-key!!", 0)
	req := httptest.NewRequest(http.MethodGet, "/_stats", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	app.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d; want 403", w.Code)
	}
}

func TestStatsHandler_NoToken401(t *testing.T) {
	app := newTestApp()
	app.StatsHandler()

	req := httptest.NewRequest(http.MethodGet, "/_stats", nil)
	w := httptest.NewRecorder()
	app.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d; want 401", w.Code)
	}
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

type fakeStatsProvider struct {
	key  string
	data map[string]any
	err  error
}

func (f *fakeStatsProvider) StatsKey() string { return f.key }
func (f *fakeStatsProvider) ProvideStats(_ context.Context) (map[string]any, error) {
	return f.data, f.err
}
