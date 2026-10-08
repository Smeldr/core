// AGPL-3.0-or-later

package smeldr

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// actorWritePath is one compiled-type write path, driven as a given user with
// a body that may carry last_actor. It returns the slug of the written item.
type actorWritePath struct {
	name   string
	create bool
	call   func(t *testing.T, m *Module[*testActorPost], seed *testActorPost, user User, body map[string]any) string
}

// serveActor runs h as user against body and fails the test on a non-2xx.
func serveActor(t *testing.T, h http.HandlerFunc, method, slug string, user User, body map[string]any) {
	t.Helper()
	b, _ := json.Marshal(body)
	r := withUser(httptest.NewRequest(method, "/testactorposts/"+slug, bytes.NewReader(b)), user)
	if slug != "" {
		r.SetPathValue("slug", slug)
	}
	w := httptest.NewRecorder()
	h(w, r)
	if w.Code < 200 || w.Code > 299 {
		t.Fatalf("%s: status %d: %s", method, w.Code, w.Body.String())
	}
}

func actorWritePaths() []actorWritePath {
	return []actorWritePath{
		{name: "HTTP POST", create: true, call: func(t *testing.T, m *Module[*testActorPost], _ *testActorPost, user User, body map[string]any) string {
			body["Title"] = "Created"
			b, _ := json.Marshal(body)
			r := withUser(httptest.NewRequest(http.MethodPost, "/testactorposts", bytes.NewReader(b)), user)
			w := httptest.NewRecorder()
			m.createHandler(w, r)
			if w.Code != http.StatusCreated && w.Code != http.StatusOK {
				t.Fatalf("POST: status %d: %s", w.Code, w.Body.String())
			}
			var out testActorPost
			if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
				t.Fatalf("POST body: %v", err)
			}
			return out.Slug
		}},
		{name: "MCP create", create: true, call: func(t *testing.T, m *Module[*testActorPost], _ *testActorPost, user User, body map[string]any) string {
			body["title"] = "Created"
			item, err := m.MCPCreate(NewTestContext(user), body)
			if err != nil {
				t.Fatalf("MCPCreate: %v", err)
			}
			return item.(*testActorPost).Slug
		}},
		{name: "HTTP PUT", call: func(t *testing.T, m *Module[*testActorPost], seed *testActorPost, user User, body map[string]any) string {
			body["Title"] = "Changed"
			body["Status"] = string(seed.Status)
			serveActor(t, m.updateHandler, http.MethodPut, seed.Slug, user, body)
			return seed.Slug
		}},
		{name: "HTTP PATCH", call: func(t *testing.T, m *Module[*testActorPost], seed *testActorPost, user User, body map[string]any) string {
			body["Title"] = "Changed"
			serveActor(t, m.patchHandler, http.MethodPatch, seed.Slug, user, body)
			return seed.Slug
		}},
		{name: "MCP update", call: func(t *testing.T, m *Module[*testActorPost], seed *testActorPost, user User, body map[string]any) string {
			body["title"] = "Changed"
			if _, err := m.MCPUpdate(NewTestContext(user), seed.Slug, body); err != nil {
				t.Fatalf("MCPUpdate: %v", err)
			}
			return seed.Slug
		}},
	}
}

// newActorModule is a Module over testActorPost with one item written by
// alice.
func newActorModule(t *testing.T) (*Module[*testActorPost], Repository[*testActorPost], *testActorPost) {
	t.Helper()
	repo := NewMemoryRepo[*testActorPost]()
	seed := &testActorPost{Node: Node{ID: NewID(), Slug: "seeded", Status: Draft}, Title: "Seeded", LastActor: "alice"}
	if err := repo.Save(context.Background(), seed); err != nil {
		t.Fatal(err)
	}
	return NewModule((*testActorPost)(nil), Repo(repo)), repo, seed
}

func storedActor(t *testing.T, repo Repository[*testActorPost], slug string) string {
	t.Helper()
	got, err := repo.FindBySlug(context.Background(), slug)
	if err != nil {
		t.Fatalf("FindBySlug(%q): %v", slug, err)
	}
	return got.LastActor
}

// Every create and content update records its writer as LastActor (Option A,
// "most recent writer"), whatever the body says, and a body that omits the
// field does not erase it.
func TestCompiledLastActor_WriterOnEveryPath(t *testing.T) {
	bob := User{ID: "bob", Roles: []Role{Editor}}
	bodies := []struct {
		name string
		body func() map[string]any
	}{
		{"omits last_actor", func() map[string]any { return map[string]any{} }},
		{"names another actor", func() map[string]any { return map[string]any{"last_actor": "carol"} }},
		{"sends it empty", func() map[string]any { return map[string]any{"last_actor": ""} }},
	}
	for _, p := range actorWritePaths() {
		for _, b := range bodies {
			t.Run(p.name+"/"+b.name, func(t *testing.T) {
				m, repo, seed := newActorModule(t)
				slug := p.call(t, m, seed, bob, b.body())
				if got := storedActor(t, repo, slug); got != "bob" {
					t.Errorf("LastActor = %q; want bob, the writer", got)
				}
			})
		}
	}
}

// A system write (no user ID) never takes the body's value: an update keeps
// the stored actor, a create stays empty.
func TestCompiledLastActor_SystemWriteKeepsStored(t *testing.T) {
	system := User{}
	for _, p := range actorWritePaths() {
		if p.name == "HTTP POST" || p.name == "HTTP PUT" || p.name == "HTTP PATCH" {
			continue // the HTTP write routes refuse a caller without a role
		}
		t.Run(p.name, func(t *testing.T) {
			m, repo, seed := newActorModule(t)
			slug := p.call(t, m, seed, system, map[string]any{"last_actor": "carol"})
			want := "alice"
			if p.create {
				want = ""
			}
			if got := storedActor(t, repo, slug); got != want {
				t.Errorf("LastActor = %q; want %q", got, want)
			}
		})
	}
}

func TestLastActorOf(t *testing.T) {
	var nilPost *testActorPost
	cases := []struct {
		name string
		item any
		want string
	}{
		{"nil", nil, ""},
		{"nil pointer", nilPost, ""},
		{"value", testActorPost{LastActor: "a"}, "a"},
		{"pointer", &testActorPost{LastActor: "b"}, "b"},
		{"no field", &testPost{}, ""},
		{"not a struct", "x", ""},
		{"non-string field", &struct{ LastActor int }{LastActor: 1}, ""},
	}
	for _, c := range cases {
		if got := lastActorOf(c.item); got != c.want {
			t.Errorf("%s: lastActorOf = %q; want %q", c.name, got, c.want)
		}
	}
}
