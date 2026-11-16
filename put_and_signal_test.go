package smeldr

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// T1300: findings from docs' Go-program runs.

// putPost sends a PUT of body to the item's slug as an Editor.
func putPost(t *testing.T, m *Module[*testPost], slug string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(body)
	w := httptest.NewRecorder()
	r := withUser(httptest.NewRequest(http.MethodPut, "/testposts/"+slug, bytes.NewReader(raw)), editorUser())
	r.SetPathValue("slug", slug)
	m.updateHandler(w, r)
	return w
}

// A PUT without PublishedAt keeps the stored time, also on an item that stays
// published; a PUT that sends one keeps the sent one.
func TestPut_PublishedAtKept(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	p := seedPost(t, repo, "Published Post", Published)
	stored := time.Date(2026, 11, 16, 8, 43, 52, 0, time.UTC)
	p.PublishedAt = stored
	if err := repo.Save(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	m := newTestModule(repo)

	w := putPost(t, m, p.Slug, map[string]any{"Title": "Edited title", "Body": p.Body, "Status": string(Published)})
	if w.Code != http.StatusOK {
		t.Fatalf("PUT = %d %s", w.Code, w.Body.String())
	}
	saved, _ := repo.FindBySlug(context.Background(), p.Slug)
	if !saved.PublishedAt.Equal(stored) || saved.Title != "Edited title" {
		t.Errorf("after a PUT without PublishedAt: %v %q; want %v and the new title", saved.PublishedAt, saved.Title, stored)
	}
	var answer testPost
	if err := json.Unmarshal(w.Body.Bytes(), &answer); err != nil || !answer.PublishedAt.Equal(stored) {
		t.Errorf("answer PublishedAt = %v %v; want %v", answer.PublishedAt, err, stored)
	}

	sent := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	if w := putPost(t, m, p.Slug, map[string]any{"Title": "Edited title", "Body": p.Body, "Status": string(Published), "PublishedAt": sent}); w.Code != http.StatusOK {
		t.Fatalf("PUT with PublishedAt = %d %s", w.Code, w.Body.String())
	}
	saved, _ = repo.FindBySlug(context.Background(), p.Slug)
	if !saved.PublishedAt.Equal(sent) {
		t.Errorf("a sent PublishedAt = %v; want %v", saved.PublishedAt, sent)
	}
}

// A PUT whose Slug differs from the stored one is 200: the other fields are
// applied and the slug stays. This fixes today's answer (a PATCH refuses).
func TestPut_SlugIgnored(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	p := seedPost(t, repo, "Slug Post", Draft)
	m := newTestModule(repo)
	w := putPost(t, m, p.Slug, map[string]any{"Slug": "another-slug", "Title": "New title", "Body": p.Body, "Status": string(Draft)})
	if w.Code != http.StatusOK {
		t.Fatalf("PUT = %d %s", w.Code, w.Body.String())
	}
	saved, err := repo.FindByID(context.Background(), p.ID)
	if err != nil || saved.Slug != p.Slug || saved.Title != "New title" {
		t.Errorf("after a PUT with another slug: %q %q %v; want the stored slug and the new title", saved.Slug, saved.Title, err)
	}
}

// An OnSignal handler registered after the bus was wired with no handler at
// all runs for a later write: the hook is set even when nothing listens.
func TestOnSignal_AfterWiring(t *testing.T) {
	app, mod := newBusApp(t, "test-secret-bus-after-run")
	app.wireSignalBus() // as Handler() and Run do, with nothing registered yet

	var mu sync.Mutex
	calls := 0
	done := make(chan struct{}, 1)
	app.OnSignal(AfterCreate, func(context.Context, SignalEvent) error {
		mu.Lock()
		calls++
		mu.Unlock()
		done <- struct{}{}
		return nil
	})
	if _, err := mod.MCPCreate(NewTestContext(User{ID: "u1", Roles: []Role{Author}}), map[string]any{"title": "After run"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("a handler registered after wiring never ran")
	}
}

// The scheduler's publish reaches a handler with ActorRole guest, no
// ActorID and Surface trigger: no actor, a system path (documented, T1300).
func TestOnSignal_SchedulerEvent(t *testing.T) {
	app := New(MustConfig(Config{BaseURL: "http://localhost:8080", Secret: []byte("test-secret-bus-scheduler")}))
	repo := NewMemoryRepo[*sbPost]()
	mod := NewModule((*sbPost)(nil), At("/sb"), Repo(repo))
	app.Content(mod)
	got := make(chan SignalEvent, 1)
	app.OnSignal(AfterPublish, func(_ context.Context, ev SignalEvent) error {
		got <- ev
		return nil
	})
	app.wireSignalBus()
	past := time.Now().UTC().Add(-time.Minute)
	p := &sbPost{Node: Node{ID: NewID(), Slug: "scheduled-one", Status: Scheduled, ScheduledAt: &past}, Title: "Scheduled one"}
	if err := repo.Save(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if _, _, err := mod.processScheduled(app.backgroundContext(), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-got:
		if ev.ActorRole != "guest" || ev.ActorID != "" || ev.Surface != "trigger" || ev.PreviousState != "scheduled" {
			t.Errorf("scheduler event: role %q, id %q, surface %q, prev %q", ev.ActorRole, ev.ActorID, ev.Surface, ev.PreviousState)
		}
	case <-time.After(time.Second):
		t.Fatal("no AfterPublish from the scheduler")
	}
}

// BenchmarkSignalBus_NoHandlers is the cost a write pays when nothing listens,
// now that the hook is always set (T1300).
func BenchmarkSignalBus_NoHandlers(b *testing.B) {
	app := New(Config{BaseURL: "https://example.com", Secret: []byte("bench-secret-signal-bus-xxxxxxx")})
	ctx := NewTestContext(User{ID: "u1", Roles: []Role{Author}})
	item := &testPost{Node: Node{ID: "id", Slug: "s"}, Title: "t"}
	meta := afterHookMeta{TypeName: "testPost", Prefix: "/testposts", Surface: surfaceHTTP}
	b.ReportAllocs()
	for b.Loop() {
		app.signalBus(ctx, AfterUpdate, meta, item)
	}
}
