package smeldr

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// ——————————————————————————————————————————————————————————————
// MustConfig
// ——————————————————————————————————————————————————————————————

func TestMustConfig_valid(t *testing.T) {
	cfg := Config{
		BaseURL: "https://example.com",
		Secret:  []byte("supersecretkey16"),
	}
	got := MustConfig(cfg)
	if got.BaseURL != cfg.BaseURL {
		t.Fatalf("BaseURL modified: got %q", got.BaseURL)
	}
	if string(got.Secret) != string(cfg.Secret) {
		t.Fatal("Secret modified")
	}
}

func TestMustConfig_emptyBaseURL(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for empty BaseURL")
		}
		msg := fmt.Sprint(r)
		if !strings.Contains(msg, "BaseURL") {
			t.Fatalf("panic message does not mention BaseURL: %s", msg)
		}
	}()
	MustConfig(Config{
		BaseURL: "",
		Secret:  []byte("supersecretkey16"),
	})
}

func TestMustConfig_invalidBaseURL(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for invalid BaseURL")
		}
		msg := fmt.Sprint(r)
		if !strings.Contains(msg, "not a valid absolute URL") {
			t.Fatalf("unexpected panic message: %s", msg)
		}
	}()
	MustConfig(Config{
		BaseURL: "not-a-url",
		Secret:  []byte("supersecretkey16"),
	})
}

func TestMustConfig_relativeURL(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for relative URL")
		}
	}()
	MustConfig(Config{
		BaseURL: "/relative-path",
		Secret:  []byte("supersecretkey16"),
	})
}

func TestMustConfig_shortSecret(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for short Secret")
		}
		msg := fmt.Sprint(r)
		if !strings.Contains(msg, "Secret") {
			t.Fatalf("panic message does not mention Secret: %s", msg)
		}
	}()
	MustConfig(Config{
		BaseURL: "https://example.com",
		Secret:  []byte("tooshort"),
	})
}

// ——————————————————————————————————————————————————————————————
// New — defaults and preservation
// ——————————————————————————————————————————————————————————————

func TestNew_defaults(t *testing.T) {
	app := New(Config{
		BaseURL: "https://example.com",
		Secret:  []byte("supersecretkey16"),
	})
	if app.cfg.ReadTimeout != defaultReadTimeout {
		t.Errorf("ReadTimeout: got %v, want %v", app.cfg.ReadTimeout, defaultReadTimeout)
	}
	if app.cfg.WriteTimeout != defaultWriteTimeout {
		t.Errorf("WriteTimeout: got %v, want %v", app.cfg.WriteTimeout, defaultWriteTimeout)
	}
	if app.cfg.IdleTimeout != defaultIdleTimeout {
		t.Errorf("IdleTimeout: got %v, want %v", app.cfg.IdleTimeout, defaultIdleTimeout)
	}
}

func TestNew_preservesTimeouts(t *testing.T) {
	app := New(Config{
		BaseURL:      "https://example.com",
		Secret:       []byte("supersecretkey16"),
		ReadTimeout:  1 * time.Second,
		WriteTimeout: 2 * time.Second,
		IdleTimeout:  3 * time.Second,
	})
	if app.cfg.ReadTimeout != time.Second {
		t.Errorf("ReadTimeout overwritten: got %v", app.cfg.ReadTimeout)
	}
	if app.cfg.WriteTimeout != 2*time.Second {
		t.Errorf("WriteTimeout overwritten: got %v", app.cfg.WriteTimeout)
	}
	if app.cfg.IdleTimeout != 3*time.Second {
		t.Errorf("IdleTimeout overwritten: got %v", app.cfg.IdleTimeout)
	}
}

// ——————————————————————————————————————————————————————————————
// Use — middleware ordering
// ——————————————————————————————————————————————————————————————

func TestApp_Use_order(t *testing.T) {
	app := New(Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16")})

	var order []string

	first := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "first")
			next.ServeHTTP(w, r)
		})
	}
	second := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "second")
			next.ServeHTTP(w, r)
		})
	}

	app.Use(first)
	app.Use(second)
	app.Handle("GET /ping", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/ping", nil)
	w := httptest.NewRecorder()
	app.Handler().ServeHTTP(w, req)

	if len(order) != 2 || order[0] != "first" || order[1] != "second" {
		t.Fatalf("unexpected middleware order: %v", order)
	}
}

// ——————————————————————————————————————————————————————————————
// Handle
// ——————————————————————————————————————————————————————————————

func TestApp_Handle(t *testing.T) {
	app := New(Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16")})
	app.Handle("GET /hello", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "world")
	}))

	req := httptest.NewRequest("GET", "/hello", nil)
	w := httptest.NewRecorder()
	app.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200", w.Code)
	}
	if body := w.Body.String(); body != "world" {
		t.Fatalf("got body %q, want %q", body, "world")
	}
}

// ——————————————————————————————————————————————————————————————
// Content — Registrator path (typed module)
// ——————————————————————————————————————————————————————————————

func TestApp_Content_list(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	m := NewModule((*testPost)(nil), Repo(repo))

	app := New(Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16")})
	app.Content(m)

	req := httptest.NewRequest("GET", "/testposts", nil)
	w := httptest.NewRecorder()
	app.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("list: got status %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("list: unexpected Content-Type %q", ct)
	}
}

func TestApp_Content_create(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	m := NewModule((*testPost)(nil), Repo(repo))

	app := New(Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16")})
	app.Content(m)

	// Create a published post (Author role required by default).
	body := strings.NewReader(`{"Title":"Hello","Status":"published"}`)
	createReq := httptest.NewRequest("POST", "/testposts", body)
	createReq.Header.Set("Content-Type", "application/json")
	createReq = withUser(createReq, User{ID: "author-1", Roles: []Role{Author}})
	cw := httptest.NewRecorder()
	app.Handler().ServeHTTP(cw, createReq)

	if cw.Code != http.StatusCreated {
		t.Fatalf("create: got status %d, want 201; body: %s", cw.Code, cw.Body.String())
	}

	// Confirm the item is retrievable via the list endpoint.
	listReq := httptest.NewRequest("GET", "/testposts", nil)
	lw := httptest.NewRecorder()
	app.Handler().ServeHTTP(lw, listReq)

	var items []map[string]any
	if err := json.NewDecoder(lw.Body).Decode(&items); err != nil {
		t.Fatalf("list decode: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item after create, got %d", len(items))
	}
}

// ——————————————————————————————————————————————————————————————
// Handler — middleware applied
// ——————————————————————————————————————————————————————————————

func TestApp_Handler_middlewareChain(t *testing.T) {
	app := New(Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16")})
	app.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Test-MW", "applied")
			next.ServeHTTP(w, r)
		})
	})
	app.Handle("GET /ping", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/ping", nil)
	w := httptest.NewRecorder()
	app.Handler().ServeHTTP(w, req)

	if got := w.Header().Get("X-Test-MW"); got != "applied" {
		t.Fatalf("middleware header missing; got %q", got)
	}
}

// ——————————————————————————————————————————————————————————————
// Handler — HTTPS redirect
// ——————————————————————————————————————————————————————————————

func TestApp_Handler_httpsRedirect(t *testing.T) {
	app := New(Config{
		BaseURL: "https://example.com",
		Secret:  []byte("supersecretkey16"),
		HTTPS:   true,
	})
	app.Handle("GET /ping", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Plain HTTP request (no TLS, no X-Forwarded-Proto).
	req := httptest.NewRequest("GET", "http://example.com/ping", nil)
	w := httptest.NewRecorder()
	app.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusMovedPermanently {
		t.Fatalf("expected 301, got %d", w.Code)
	}
	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, "https://") {
		t.Fatalf("redirect location does not start with https://: %q", loc)
	}
}

func TestApp_Handler_httpsRedirect_xForwardedProto(t *testing.T) {
	app := New(Config{
		BaseURL: "https://example.com",
		Secret:  []byte("supersecretkey16"),
		HTTPS:   true,
	})
	app.Handle("GET /ping", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	// Request already marked as HTTPS by reverse proxy.
	req := httptest.NewRequest("GET", "http://example.com/ping", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()
	app.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected pass-through (204), got %d", w.Code)
	}
}

func TestApp_Handler_httpsRedirect_disabled(t *testing.T) {
	app := New(Config{
		BaseURL: "https://example.com",
		Secret:  []byte("supersecretkey16"),
		HTTPS:   false, // explicitly off
	})
	app.Handle("GET /ping", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "http://example.com/ping", nil)
	w := httptest.NewRecorder()
	app.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 when HTTPS=false, got %d", w.Code)
	}
}

// ——————————————————————————————————————————————————————————————
// Run — graceful shutdown
// ——————————————————————————————————————————————————————————————

func TestApp_Run_gracefulShutdown(t *testing.T) {
	// Pick a free port by briefly listening, then releasing it.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("could not find free port: %v", err)
	}
	addr := l.Addr().String()
	l.Close()

	app := New(Config{
		BaseURL:      "https://example.com",
		Secret:       []byte("supersecretkey16"),
		ReadTimeout:  time.Second,
		WriteTimeout: time.Second,
		IdleTimeout:  time.Second,
	})
	app.Handle("GET /ping", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	done := make(chan error, 1)
	go func() {
		done <- app.Run(addr)
	}()

	// Poll until the server accepts connections (max 2 s).
	ready := false
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://" + addr + "/ping")
		if err == nil {
			io.Copy(io.Discard, resp.Body) //nolint:errcheck
			resp.Body.Close()
			ready = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !ready {
		t.Fatal("server did not become ready within 2 s")
	}

	// Send SIGINT to self.
	proc, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatalf("FindProcess: %v", err)
	}
	if err := proc.Signal(os.Interrupt); err != nil {
		t.Skipf("cannot send signal on this platform (%v); skipping shutdown assertion", err)
	}

	// Expect clean shutdown within 2 s.
	select {
	case runErr := <-done:
		if runErr != nil {
			t.Fatalf("Run returned error: %v", runErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not shut down within 2 s after SIGINT")
	}
}

// TestApp_RedirectStore verifies that New always initialises the redirect store
// and that App.RedirectStore returns it (Amendment A20).
func TestApp_RedirectStore(t *testing.T) {
	app := New(Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16")})
	if store := app.RedirectStore(); store == nil {
		t.Error("RedirectStore() = nil; want non-nil *RedirectStore")
	}
	// Store should be the same pointer on repeated calls.
	if s1, s2 := app.RedirectStore(), app.RedirectStore(); s1 != s2 {
		t.Error("RedirectStore() returned different pointers on consecutive calls")
	}
}

// ——————————————————————————————————————————————————————————————
// Health endpoint (Amendments A42, A58)
// ——————————————————————————————————————————————————————————————

// TestApp_health_ok verifies that GET /_health returns 200 application/json
// with a body containing "status":"ok" (A42) and a "forge" version key
// sourced from build info (A58).
func TestApp_health_ok(t *testing.T) {
	app := New(Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16")})
	app.Health()

	req := httptest.NewRequest("GET", "/_health", nil)
	w := httptest.NewRecorder()
	app.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type: got %q, want %q", ct, "application/json")
	}
	body := w.Body.String()
	if !strings.Contains(body, `"status":"ok"`) {
		t.Fatalf("body missing status:ok, got %q", body)
	}
}

// TestApp_health_forgeVersion verifies that GET /_health includes the
// "core" key populated from binary build info (Amendment A58, updated A104).
func TestApp_health_forgeVersion(t *testing.T) {
	app := New(Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16")})
	app.Health()

	req := httptest.NewRequest("GET", "/_health", nil)
	w := httptest.NewRecorder()
	app.Handler().ServeHTTP(w, req)

	body := w.Body.String()
	// In test binaries smeldr.dev/core is the main module; build info is always present.
	// The version may be "(devel)" in local builds but the key must exist.
	if !strings.Contains(body, `"core":`) {
		t.Fatalf("body missing core version key, got %q", body)
	}
}

// TestApp_health_configVersion_notExposed verifies that Config.Version is NOT
// included in the /_health JSON response (Amendment A58: app-level versioning
// is no longer forge core's responsibility).
func TestApp_health_configVersion_notExposed(t *testing.T) {
	app := New(Config{
		BaseURL: "https://example.com",
		Secret:  []byte("supersecretkey16"),
		Version: "1.2.3",
	})
	app.Health()

	req := httptest.NewRequest("GET", "/_health", nil)
	w := httptest.NewRecorder()
	app.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200", w.Code)
	}
	body := w.Body.String()
	if strings.Contains(body, `"version"`) {
		t.Fatalf("expected no version key in health response, got: %s", body)
	}
}

// TestApp_health_notMounted verifies that GET /_health returns 404 when
// app.Health() has not been called.
func TestApp_health_notMounted(t *testing.T) {
	app := New(Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16")})
	// Health() is intentionally NOT called here.

	req := httptest.NewRequest("GET", "/_health", nil)
	w := httptest.NewRecorder()
	app.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("got status %d, want 404 when Health() not mounted", w.Code)
	}
}

// TestApp_health_httpsExempt verifies that GET /_health returns 200 on a
// plain-HTTP request even when Config.HTTPS is true (Amendment A59).
func TestApp_health_httpsExempt(t *testing.T) {
	app := New(Config{
		BaseURL: "https://example.com",
		Secret:  []byte("supersecretkey16"),
		HTTPS:   true,
	})
	app.Health()

	req := httptest.NewRequest("GET", "/_health", nil)
	// No TLS, no X-Forwarded-Proto: plain HTTP.
	w := httptest.NewRecorder()
	app.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200 — /_health must be exempt from HTTPS redirect", w.Code)
	}
}

// ——————————————————————————————————————————————————————————————
// Benchmark
// ——————————————————————————————————————————————————————————————

// ——————————————————————————————————————————————————————————————
// MustConfig — SMELDR_CONFIG env var path
// ——————————————————————————————————————————————————————————————

func TestMustConfig_envVarPath(t *testing.T) {
	// Write a minimal smeldr.config to a temp file.
	f, err := os.CreateTemp("", "smeldr-*.config")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	defer os.Remove(f.Name())
	f.WriteString("# test config\n") //nolint:errcheck
	f.Close()

	t.Setenv("SMELDR_CONFIG", f.Name())

	// MustConfig should read the env-var path without panicking.
	cfg := MustConfig(Config{
		BaseURL: "https://example.com",
		Secret:  []byte("supersecretkey16"),
	})
	if cfg.BaseURL != "https://example.com" {
		t.Errorf("BaseURL = %q, want %q", cfg.BaseURL, "https://example.com")
	}
}

// ——————————————————————————————————————————————————————————————
// Handler — NavModeCode path + setNavTree
// ——————————————————————————————————————————————————————————————

func TestApp_Handler_navModeCode(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	m := NewModule((*testPost)(nil), Repo(repo))

	app := New(Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16")})
	app.Nav(NavItem{Label: "Home", Path: "/"})
	app.Content(m) // adds m to navTreeModules

	// Handler() triggers the NavModeCode branch and calls m.setNavTree.
	h := app.Handler()
	if app.navTree == nil {
		t.Fatal("navTree should be non-nil after Handler() with Nav items")
	}
	if m.navTree == nil {
		t.Fatal("module navTree should be set via setNavTree after Handler()")
	}

	// Verify the tree is usable.
	req := httptest.NewRequest("GET", "/testposts", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("list status = %d, want 200", w.Code)
	}
}

// ——————————————————————————————————————————————————————————————
// Handler — OGDefaults root-relative image URL resolution
// ——————————————————————————————————————————————————————————————

func TestApp_Handler_OGDefaults_rootRelative(t *testing.T) {
	app := New(Config{
		BaseURL: "https://example.com",
		Secret:  []byte("supersecretkey16"),
		OGDefaults: &OGDefaults{
			TwitterSite: "@example",
			Image:       Image{URL: "/static/og.png", Width: 1200, Height: 630},
		},
	})
	app.Handler()

	og := app.seo.ogDefaults
	if og == nil {
		t.Fatal("seo.ogDefaults should be non-nil after Handler()")
	}
	want := "https://example.com/static/og.png"
	if og.Image.URL != want {
		t.Errorf("og.Image.URL = %q, want %q", og.Image.URL, want)
	}
}

func TestApp_Handler_OGDefaults_absoluteURL(t *testing.T) {
	app := New(Config{
		BaseURL: "https://example.com",
		Secret:  []byte("supersecretkey16"),
		OGDefaults: &OGDefaults{
			Image: Image{URL: "https://cdn.example.com/og.png"},
		},
	})
	app.Handler()

	og := app.seo.ogDefaults
	if og == nil {
		t.Fatal("seo.ogDefaults should be non-nil after Handler()")
	}
	// Absolute URL should be preserved unchanged.
	if og.Image.URL != "https://cdn.example.com/og.png" {
		t.Errorf("og.Image.URL = %q, want unchanged absolute URL", og.Image.URL)
	}
}

// ——————————————————————————————————————————————————————————————
// Handler — audit handler registration
// ——————————————————————————————————————————————————————————————

type mockAuditStore struct{}

func (mockAuditStore) Append(_ context.Context, _ AuditRecord) error { return nil }
func (mockAuditStore) List(_ context.Context, _ AuditFilter) ([]AuditRecord, error) {
	return nil, nil
}

func TestApp_Handler_auditHandler(t *testing.T) {
	app := New(Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16")})
	app.Audit(mockAuditStore{})
	h := app.Handler()

	// /_audit is protected by auth; unauthenticated request gets 401.
	req := httptest.NewRequest("GET", "/_audit", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code == http.StatusNotFound {
		t.Error("/_audit route not registered (got 404)")
	}
}

// ——————————————————————————————————————————————————————————————
// Handler — logs handler registration
// ——————————————————————————————————————————————————————————————

func TestApp_Handler_logsHandler(t *testing.T) {
	app := New(Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16")})
	app.CaptureLogs()
	h := app.Handler()

	// /_logs is protected by auth; unauthenticated request gets 401.
	req := httptest.NewRequest("GET", "/_logs", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code == http.StatusNotFound {
		t.Error("/_logs route not registered (got 404)")
	}
}

// ——————————————————————————————————————————————————————————————
// Handler — cookie manifest registration
// ——————————————————————————————————————————————————————————————

func TestApp_Handler_cookiesManifest(t *testing.T) {
	app := New(Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16")})
	app.Cookies(Cookie{Name: "session", Category: Necessary, Purpose: "Keeps you logged in."})
	h := app.Handler()

	req := httptest.NewRequest("GET", "/.well-known/cookies.json", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("cookies manifest status = %d, want 200", w.Code)
	}
	var got map[string]any
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode cookies.json: %v", err)
	}
	if got["count"] == nil {
		t.Error("cookies manifest should have a count field")
	}
}

// ——————————————————————————————————————————————————————————————
// Run — with registered module (scheduler path)
// ——————————————————————————————————————————————————————————————

func TestApp_Run_withModule(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("could not find free port: %v", err)
	}
	addr := l.Addr().String()
	l.Close()

	repo := NewMemoryRepo[*testPost]()
	m := NewModule((*testPost)(nil), Repo(repo))

	app := New(Config{
		BaseURL:      "https://example.com",
		Secret:       []byte("supersecretkey16"),
		ReadTimeout:  time.Second,
		WriteTimeout: time.Second,
		IdleTimeout:  time.Second,
	})
	app.Content(m) // adds m to schedulerModules

	done := make(chan error, 1)
	go func() {
		done <- app.Run(addr)
	}()

	// Poll until ready (max 2 s).
	deadline := time.Now().Add(2 * time.Second)
	ready := false
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://" + addr + "/testposts")
		if err == nil {
			io.Copy(io.Discard, resp.Body) //nolint:errcheck
			resp.Body.Close()
			ready = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !ready {
		t.Fatal("server did not become ready within 2 s")
	}

	proc, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatalf("FindProcess: %v", err)
	}
	if err := proc.Signal(os.Interrupt); err != nil {
		t.Skipf("cannot send signal on this platform (%v); skipping", err)
	}

	select {
	case runErr := <-done:
		if runErr != nil {
			t.Fatalf("Run returned error: %v", runErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not shut down within 2 s after SIGINT")
	}
}

// ——————————————————————————————————————————————————————————————
// Run — scheduler + webhook pool, fail-fast (covers start/stop defers)
// ——————————————————————————————————————————————————————————————

func TestApp_Run_schedulerAndWebhookPool_failFast(t *testing.T) {
	db := newSQLiteDB(t)
	store := NewWebhookStore(db, []byte("supersecretkey16"))

	repo := NewMemoryRepo[*testPost]()
	m := NewModule((*testPost)(nil), Repo(repo))

	app := New(Config{
		BaseURL: "https://example.com",
		Secret:  []byte("supersecretkey16"),
		DB:      db,
	})
	app.Content(m)      // adds m to schedulerModules
	app.Webhooks(store) // sets webhookPool

	// Bind a listener first so that app.Run using the same addr fails immediately
	// with "address already in use". This causes Run() to return quickly while
	// still exercising the webhookPool.Start/Stop and schedCancel/sched.Wait
	// deferred cleanup paths.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("could not bind listener: %v", err)
	}
	defer l.Close()
	addr := l.Addr().String()

	runErr := app.Run(addr)
	if runErr == nil {
		t.Error("expected error when address is already in use")
	}
}

// ——————————————————————————————————————————————————————————————
// WebhookPool
// ——————————————————————————————————————————————————————————————

func TestApp_WebhookPool_nil(t *testing.T) {
	app := New(Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16")})
	// No Webhooks() called → pool is nil.
	if app.WebhookPool() != nil {
		t.Error("WebhookPool() should return nil when no webhook store is configured")
	}
}

func TestApp_WebhookPool_nonNil(t *testing.T) {
	db := newSQLiteDB(t)
	store := NewWebhookStore(db, []byte("supersecretkey16"))
	app := New(Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16"), DB: db})
	app.Webhooks(store)

	p1 := app.WebhookPool()
	if p1 == nil {
		t.Fatal("WebhookPool() = nil; want non-nil pool after Webhooks()")
	}

	// Second call returns the same pool (covers the non-nil return branch).
	p2 := app.WebhookPool()
	if p1 != p2 {
		t.Error("WebhookPool() should return the same pool on repeated calls")
	}
}

// ——————————————————————————————————————————————————————————————
// Benchmark
// ——————————————————————————————————————————————————————————————

func BenchmarkApp_Handler(b *testing.B) {
	repo := NewMemoryRepo[*testPost]()
	// Pre-populate with a few items.
	for i := range 10 {
		p := &testPost{Title: fmt.Sprintf("Post %d", i)}
		p.Node.ID = NewID()
		p.Node.Slug = GenerateSlug(p.Title)
		p.Node.Status = Published
		if err := repo.Save(context.Background(), p); err != nil {
			b.Fatalf("seed: %v", err)
		}
	}

	m := NewModule((*testPost)(nil), Repo(repo))
	app := New(Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16")})
	app.Content(m)
	h := app.Handler()

	req := httptest.NewRequest("GET", "/testposts", nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
	}
}

// — App.Cookies dedup ——————————————————————————————————————————————————————

func TestApp_Cookies_dedup(t *testing.T) {
	app := New(Config{BaseURL: "https://example.com", Secret: []byte("testsecret16chars")})
	session := Cookie{Name: "session", Category: Necessary}
	prefs := Cookie{Name: "prefs", Category: Preferences}

	app.Cookies(session)
	app.Cookies(session, prefs) // "session" already registered — must not duplicate

	count := 0
	for _, d := range app.cookieDecls {
		if d.Name == "session" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("Cookies dedup: session appears %d times, want 1", count)
	}
}

// — App.Redirects error path ——————————————————————————————————————————————

func TestApp_Redirects_createTableError(t *testing.T) {
	app := New(Config{BaseURL: "https://example.com", Secret: []byte("testsecret16chars")})
	if err := app.Redirects(&errExecDB{}); err == nil {
		t.Error("expected error when CreateRedirectsTable fails")
	}
}

// — App.Handler TokenStore probe error ————————————————————————————————————

func TestApp_Handler_tokenStoreProbeError(t *testing.T) {
	// errExecDB.QueryRowContext returns a no-row Row → Scan returns sql.ErrNoRows
	// → probeTable returns error → WARN is printed, ensureBootstrap is skipped.
	ts := NewTokenStore(&errExecDB{}, testSecret)
	app := New(Config{
		BaseURL:    "https://example.com",
		Secret:     []byte("testsecret16chars"),
		TokenStore: ts,
	})
	_ = app.Handler() // should not panic
}

// — App.Handler AppSchema / OGDefaults auto-apply ————————————————————————

func TestApp_Handler_appSchemaAndOGDefaults(t *testing.T) {
	schema := &AppSchema{Type: "Organization", Name: "Test Org", URL: "https://example.com"}
	og := &OGDefaults{Image: Image{URL: "/og.png"}}
	app := New(Config{
		BaseURL:    "https://example.com",
		Secret:     []byte("testsecret16chars"),
		AppSchema:  schema,
		OGDefaults: og,
	})
	_ = app.Handler()
}

func TestApp_Handler_ogDefaults_absoluteImage(t *testing.T) {
	// OGDefaults with absolute image URL — takes the else branch (no root-relative resolution).
	og := &OGDefaults{Image: Image{URL: "https://cdn.example.com/og.png"}}
	app := New(Config{
		BaseURL:    "https://example.com",
		Secret:     []byte("testsecret16chars"),
		OGDefaults: og,
	})
	_ = app.Handler()
}

// — App.Handler LLMs store registration ——————————————————————————————————

func TestApp_Handler_llmsTxtRegistration(t *testing.T) {
	mem := NewMemoryRepo[*testPost]()
	m := newTestModule(mem, AIIndex(LLMsTxt))
	app := New(Config{BaseURL: "https://example.com", Secret: []byte("testsecret16chars")})
	app.Content(m)
	_ = app.Handler() // should register GET /llms.txt route
}

func TestApp_Handler_llmsFullTxtRegistration(t *testing.T) {
	mem := NewMemoryRepo[*testMDPost]()
	m := NewModule((*testMDPost)(nil), Repo(mem), AIIndex(LLMsTxtFull))
	app := New(Config{BaseURL: "https://example.com", Secret: []byte("testsecret16chars")})
	app.Content(m)
	_ = app.Handler() // should register GET /llms-full.txt route
}

// — App.Handler standalone dispatch registration ——————————————————————————

func TestApp_Handler_standaloneDispatch(t *testing.T) {
	mem := NewMemoryRepo[*testPost]()
	p := &testPost{Node: Node{ID: "1", Slug: "my-slug", Status: Published}}
	_ = mem.Save(context.Background(), p)

	m := newTestModule(mem, Standalone())
	app := New(Config{BaseURL: "https://example.com", Secret: []byte("testsecret16chars")})
	app.Content(m)
	h := app.Handler()

	// GET /{slug} — should dispatch to the standalone module.
	req := httptest.NewRequest(http.MethodGet, "/my-slug", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	// The slug exists — either 200 OK or 200 JSON response.
	if w.Code != http.StatusOK {
		t.Errorf("GET /{slug} standalone: got %d want 200", w.Code)
	}
}

func TestApp_Handler_standaloneAIDocNotFound(t *testing.T) {
	mem := NewMemoryRepo[*testPost]()
	m := newTestModule(mem, Standalone())
	app := New(Config{BaseURL: "https://example.com", Secret: []byte("testsecret16chars")})
	app.Content(m)
	h := app.Handler()

	// GET /{slug}/aidoc for unknown slug — should 404.
	req := httptest.NewRequest(http.MethodGet, "/unknown-slug/aidoc", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("GET /{slug}/aidoc not found: got %d want 404", w.Code)
	}
}

// — App.Handler NavModeCode registration —————————————————————————————————

func TestApp_Handler_navCodeItems(t *testing.T) {
	app := New(Config{BaseURL: "https://example.com", Secret: []byte("testsecret16chars")})
	app.Nav(NavItem{ID: "home", Label: "Home", Path: "/"})
	_ = app.Handler() // should build navTree from code items
	if app.navTree == nil {
		t.Error("navTree should be set after Handler() with NavModeCode items")
	}
}

// — App.Run error paths ———————————————————————————————————————————————————

func TestApp_Run_loadPartialsError(t *testing.T) {
	app := New(Config{BaseURL: "https://example.com", Secret: []byte("testsecret16chars")})
	app.Partials("non-existent-partials-dir-xyz")
	err := app.Run(":0")
	if err == nil {
		t.Error("expected error from loadPartials with non-existent dir")
	}
}

func TestApp_Run_parseTemplatesError(t *testing.T) {
	mem := NewMemoryRepo[*testPost]()
	// Templates("non-existent-dir") with required=true → parseTemplates fails.
	m := newTestModule(mem, Templates("non-existent-dir-xyz"))
	app := New(Config{BaseURL: "https://example.com", Secret: []byte("testsecret16chars")})
	app.Content(m)
	err := app.Run(":0")
	if err == nil {
		t.Error("expected error from parseTemplates with non-existent template dir")
	}
}

// — App.MustParseTemplate panic paths ————————————————————————————————————

func TestMustParseTemplate_parseError_panics(t *testing.T) {
	app := New(Config{BaseURL: "https://example.com", Secret: []byte("testsecret16chars")})
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic from MustParseTemplate with non-existent file")
		}
	}()
	app.MustParseTemplate("non-existent-file-xyz.html")
}

func TestMustParseTemplate_loadPartialsError_panics(t *testing.T) {
	app := New(Config{BaseURL: "https://example.com", Secret: []byte("testsecret16chars")})
	app.Partials("non-existent-partials-dir-xyz")
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic from MustParseTemplate when loadPartials fails")
		}
	}()
	app.MustParseTemplate("any.html")
}
