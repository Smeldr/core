package smeldr

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// — TestBuildManifest ————————————————————————————————————————————————————

func TestCookieManifest_empty(t *testing.T) {
	m := buildManifest("example.com", nil)
	if m.Count != 0 {
		t.Errorf("Count = %d; want 0", m.Count)
	}
	if len(m.Cookies) != 0 {
		t.Errorf("Cookies len = %d; want 0", len(m.Cookies))
	}
	if m.Site != "example.com" {
		t.Errorf("Site = %q; want %q", m.Site, "example.com")
	}
	if m.Generated == "" {
		t.Error("Generated must be non-empty")
	}
}

func TestCookieManifest_fields(t *testing.T) {
	decls := []Cookie{
		{
			Name:     "session",
			Category: Necessary,
			HttpOnly: true,
			Secure:   true,
			SameSite: http.SameSiteStrictMode,
			MaxAge:   3600,
			Purpose:  "Keeps you logged in.",
		},
	}
	m := buildManifest("site.io", decls)
	if m.Count != 1 {
		t.Fatalf("Count = %d; want 1", m.Count)
	}
	e := m.Cookies[0]
	if e.Name != "session" {
		t.Errorf("Name = %q; want session", e.Name)
	}
	if e.Category != Necessary {
		t.Errorf("Category = %q; want necessary", e.Category)
	}
	if !e.HttpOnly {
		t.Error("HttpOnly should be true")
	}
	if !e.Secure {
		t.Error("Secure should be true")
	}
	if e.SameSite != "Strict" {
		t.Errorf("SameSite = %q; want Strict", e.SameSite)
	}
	if e.MaxAge != 3600 {
		t.Errorf("MaxAge = %d; want 3600", e.MaxAge)
	}
	if e.Purpose != "Keeps you logged in." {
		t.Errorf("Purpose = %q", e.Purpose)
	}
}

func TestCookieManifest_sortedByName(t *testing.T) {
	decls := []Cookie{
		{Name: "zebra", Category: Analytics},
		{Name: "alpha", Category: Necessary},
		{Name: "mango", Category: Preferences},
	}
	m := buildManifest("x.com", decls)
	names := make([]string, len(m.Cookies))
	for i, e := range m.Cookies {
		names[i] = e.Name
	}
	if names[0] != "alpha" || names[1] != "mango" || names[2] != "zebra" {
		t.Errorf("cookies not sorted by name: %v", names)
	}
}

// — TestSameSiteName ——————————————————————————————————————————————————————

func TestSameSiteName(t *testing.T) {
	cases := []struct {
		in   http.SameSite
		want string
	}{
		{http.SameSiteStrictMode, "Strict"},
		{http.SameSiteLaxMode, "Lax"},
		{http.SameSiteNoneMode, "None"},
		{0, "Strict"}, // zero value defaults to Strict
	}
	for _, tc := range cases {
		got := sameSiteName(tc.in)
		if got != tc.want {
			t.Errorf("sameSiteName(%v) = %q; want %q", tc.in, got, tc.want)
		}
	}
}

// — TestManifestHandler ———————————————————————————————————————————————————

func TestCookieManifest_endpoint_200(t *testing.T) {
	decls := []Cookie{
		{Name: "sess", Category: Necessary, Purpose: "Session."},
	}
	h := newCookieManifestHandler("test.com", decls)
	r := httptest.NewRequest(http.MethodGet, "/.well-known/cookies.json", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", w.Code)
	}
}

func TestCookieManifest_contentType(t *testing.T) {
	h := newCookieManifestHandler("test.com", nil)
	r := httptest.NewRequest(http.MethodGet, "/.well-known/cookies.json", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	ct := w.Header().Get("Content-Type")
	if !strings.Contains(ct, "application/json") {
		t.Errorf("Content-Type = %q; want application/json", ct)
	}
}

func TestCookieManifest_validJSON(t *testing.T) {
	decls := []Cookie{
		{Name: "prefs", Category: Preferences, Purpose: "Remembers language."},
	}
	h := newCookieManifestHandler("test.com", decls)
	r := httptest.NewRequest(http.MethodGet, "/.well-known/cookies.json", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	var m cookieManifest
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("response is not valid JSON: %v\nbody: %s", err, w.Body.String())
	}
	if m.Count != 1 {
		t.Errorf("JSON count = %d; want 1", m.Count)
	}
	if m.Cookies[0].Name != "prefs" {
		t.Errorf("JSON cookie name = %q; want prefs", m.Cookies[0].Name)
	}
}

func TestCookieManifest_noDecls_notMounted(t *testing.T) {
	// When no Cookies() declarations are made, App.Handler() must not mount
	// the endpoint — the mux returns 404.
	app := New(MustConfig(Config{
		BaseURL: "https://example.com",
		Secret:  []byte("secret-key-for-test-use"),
	}))
	// Do NOT call app.Cookies(...)
	h := app.Handler()

	r := httptest.NewRequest(http.MethodGet, "/.well-known/cookies.json", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d; want 404 when no cookie declarations registered", w.Code)
	}
}

func TestCookieManifest_endpoint_mounted(t *testing.T) {
	app := New(MustConfig(Config{
		BaseURL: "https://example.com",
		Secret:  []byte("secret-key-for-test-use"),
	}))
	app.Cookies(Cookie{Name: "sess", Category: Necessary, Purpose: "Auth."})
	h := app.Handler()

	r := httptest.NewRequest(http.MethodGet, "/.well-known/cookies.json", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", w.Code)
	}
	var m cookieManifest
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if m.Site != "example.com" {
		t.Errorf("site = %q; want example.com", m.Site)
	}
}

// — TestManifestAuth ——————————————————————————————————————————————————————

func TestCookieManifest_manifestAuth_401(t *testing.T) {
	auth := BearerHMAC("test-secret-long-enough")
	h := newCookieManifestHandler("test.com", nil, ManifestAuth(auth))

	r := httptest.NewRequest(http.MethodGet, "/.well-known/cookies.json", nil)
	r.Header.Set("X-Request-ID", "test-rid")
	// No Authorization header.
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d; want 401 for unauthenticated request", w.Code)
	}
	if got := w.Header().Get("X-Request-ID"); got != "test-rid" {
		t.Errorf("expected X-Request-ID 'test-rid', got %q", got)
	}
}

func TestCookieManifest_manifestAuth_200(t *testing.T) {
	secret := "test-secret-long-enough"
	auth := BearerHMAC(secret)

	tok, err := SignToken(User{ID: "u1", Roles: []Role{Editor}}, secret, 0)
	if err != nil {
		t.Fatalf("SignToken: %v", err)
	}

	h := newCookieManifestHandler("test.com", nil, ManifestAuth(auth))

	r := httptest.NewRequest(http.MethodGet, "/.well-known/cookies.json", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d; want 200 for authenticated request", w.Code)
	}
}

// — TestApp_Cookies_deduplication ————————————————————————————————————————

func TestApp_Cookies_deduplication(t *testing.T) {
	app := New(MustConfig(Config{
		BaseURL: "https://example.com",
		Secret:  []byte("secret-key-for-test-use"),
	}))
	c1 := Cookie{Name: "sess", Category: Necessary, Purpose: "First."}
	c2 := Cookie{Name: "sess", Category: Necessary, Purpose: "Second (duplicate — should be ignored)."}
	app.Cookies(c1, c2)

	if len(app.cookieDecls) != 1 {
		t.Errorf("expected 1 declaration after dedup, got %d", len(app.cookieDecls))
	}
	if app.cookieDecls[0].Purpose != "First." {
		t.Errorf("first declaration should win, got Purpose = %q", app.cookieDecls[0].Purpose)
	}
}
