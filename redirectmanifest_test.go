package smeldr

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// ---------------------------------------------------------------------------
// redirectManifest build tests
// ---------------------------------------------------------------------------

func TestRedirectManifest_empty(t *testing.T) {
	m := buildRedirectManifest("test.com", nil)
	if m.Count != 0 {
		t.Errorf("expected Count 0, got %d", m.Count)
	}
	if m.Entries == nil {
		t.Error("expected non-nil Entries slice, got nil")
	}
	if len(m.Entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(m.Entries))
	}
}

func TestRedirectManifest_fields(t *testing.T) {
	entries := []RedirectEntry{
		{From: "/old", To: "/new", Code: Permanent, IsPrefix: false},
		{From: "/posts", To: "/articles", Code: Permanent, IsPrefix: true},
		{From: "/gone", To: "", Code: Gone, IsPrefix: false},
	}
	m := buildRedirectManifest("test.com", entries)

	if m.Count != 3 {
		t.Fatalf("expected Count 3, got %d", m.Count)
	}
	if m.Site != "test.com" {
		t.Errorf("expected Site test.com, got %q", m.Site)
	}

	// Find the prefix entry.
	var prefixEntry *redirectManifestEntry
	for i := range m.Entries {
		if m.Entries[i].From == "/posts" {
			prefixEntry = &m.Entries[i]
		}
	}
	if prefixEntry == nil {
		t.Fatal("prefix entry not found")
	}
	if !prefixEntry.IsPrefix {
		t.Error("expected IsPrefix true")
	}
	if prefixEntry.Code != int(Permanent) {
		t.Errorf("expected code %d, got %d", int(Permanent), prefixEntry.Code)
	}
}

func TestRedirectManifest_sortedByFrom(t *testing.T) {
	s := NewRedirectStore()
	s.Add(RedirectEntry{From: "/z-path", To: "/z-new", Code: Permanent})
	s.Add(RedirectEntry{From: "/a-path", To: "/a-new", Code: Permanent})
	s.Add(RedirectEntry{From: "/m-path", To: "/m-new", Code: Permanent})

	entries := s.All()
	m := buildRedirectManifest("test.com", entries)

	if len(m.Entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(m.Entries))
	}
	if m.Entries[0].From != "/a-path" || m.Entries[1].From != "/m-path" || m.Entries[2].From != "/z-path" {
		t.Errorf("unexpected order: %v", m.Entries)
	}
}

// ---------------------------------------------------------------------------
// HTTP endpoint tests
// ---------------------------------------------------------------------------

func TestRedirectManifest_endpoint_200(t *testing.T) {
	store := NewRedirectStore()
	store.Add(RedirectEntry{From: "/old", To: "/new", Code: Permanent})

	h := newRedirectManifestHandler("test.com", store)
	req := httptest.NewRequest(http.MethodGet, "/.well-known/redirects.json", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestRedirectManifest_contentType(t *testing.T) {
	store := NewRedirectStore()
	h := newRedirectManifestHandler("test.com", store)
	req := httptest.NewRequest(http.MethodGet, "/.well-known/redirects.json", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	ct := w.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("expected Content-Type application/json, got %q", ct)
	}
}

func TestRedirectManifest_alwaysMounted(t *testing.T) {
	// Empty store must still return 200 with valid JSON.
	store := NewRedirectStore()
	h := newRedirectManifestHandler("test.com", store)
	req := httptest.NewRequest(http.MethodGet, "/.well-known/redirects.json", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for empty store, got %d", w.Code)
	}

	var m redirectManifest
	if err := json.NewDecoder(w.Body).Decode(&m); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if m.Count != 0 {
		t.Errorf("expected count 0, got %d", m.Count)
	}
	if m.Entries == nil {
		t.Error("expected non-nil entries array")
	}
}

func TestRedirectManifest_manifestAuth_401(t *testing.T) {
	auth := BearerHMAC("test-secret-long-enough")
	store := NewRedirectStore()
	h := newRedirectManifestHandler("test.com", store, ManifestAuth(auth))

	req := httptest.NewRequest(http.MethodGet, "/.well-known/redirects.json", nil)
	req.Header.Set("X-Request-ID", "test-rid")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
	if got := w.Header().Get("X-Request-ID"); got != "test-rid" {
		t.Errorf("expected X-Request-ID 'test-rid', got %q", got)
	}
}

func TestRedirectManifest_manifestAuth_200(t *testing.T) {
	secret := "test-secret-long-enough"
	auth := BearerHMAC(secret)

	tok, err := SignToken(User{ID: "u1", Roles: []Role{Editor}}, secret, 0)
	if err != nil {
		t.Fatalf("SignToken: %v", err)
	}

	store := NewRedirectStore()
	h := newRedirectManifestHandler("test.com", store, ManifestAuth(auth))

	req := httptest.NewRequest(http.MethodGet, "/.well-known/redirects.json", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}
