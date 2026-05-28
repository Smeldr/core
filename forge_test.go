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
