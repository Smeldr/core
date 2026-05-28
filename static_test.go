package smeldr

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

// newAppWithDev returns a minimal App with Dev set to the given value.
// BaseURL and Secret are set to satisfy MustConfig; no DB or modules needed.
func newAppWithDev(t *testing.T, dev bool) *App {
	t.Helper()
	app := New(MustConfig(Config{
		BaseURL: "http://localhost",
		Secret:  []byte("test-secret-at-least-16"),
		Dev:     dev,
	}))
	return app
}

func TestStatic_prod_servesEmbedded(t *testing.T) {
	memFS := fstest.MapFS{
		"hello.txt": &fstest.MapFile{Data: []byte("hello world")},
	}
	app := newAppWithDev(t, false)
	app.Static("/static/", memFS, "nonexistent")

	req := httptest.NewRequest("GET", "/static/hello.txt", nil)
	w := httptest.NewRecorder()
	app.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("prod: got status %d, want 200", w.Code)
	}
	if body := w.Body.String(); body != "hello world" {
		t.Errorf("prod: body = %q, want %q", body, "hello world")
	}
}

func TestStatic_prod_cacheHeader(t *testing.T) {
	memFS := fstest.MapFS{
		"style.css": &fstest.MapFile{Data: []byte("body{}")},
	}
	app := newAppWithDev(t, false)
	app.Static("/static/", memFS, "nonexistent")

	req := httptest.NewRequest("GET", "/static/style.css", nil)
	w := httptest.NewRecorder()
	app.Handler().ServeHTTP(w, req)

	cc := w.Header().Get("Cache-Control")
	if cc != "public, max-age=31536000, immutable" {
		t.Errorf("prod: Cache-Control = %q, want immutable header", cc)
	}
}

func TestStatic_dev_servesFromDisk(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "app.js"), []byte("console.log(1)"), 0o644); err != nil {
		t.Fatal(err)
	}

	app := newAppWithDev(t, true)
	app.Static("/static/", fstest.MapFS{}, dir)

	req := httptest.NewRequest("GET", "/static/app.js", nil)
	w := httptest.NewRecorder()
	app.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("dev: got status %d, want 200", w.Code)
	}
	if body := w.Body.String(); body != "console.log(1)" {
		t.Errorf("dev: body = %q, want %q", body, "console.log(1)")
	}
}

func TestStatic_dev_noCacheHeader(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.js"), []byte("//dev"), 0o644); err != nil {
		t.Fatal(err)
	}

	app := newAppWithDev(t, true)
	app.Static("/static/", fstest.MapFS{}, dir)

	req := httptest.NewRequest("GET", "/static/main.js", nil)
	w := httptest.NewRecorder()
	app.Handler().ServeHTTP(w, req)

	cc := w.Header().Get("Cache-Control")
	if cc == "public, max-age=31536000, immutable" {
		t.Error("dev: must not set immutable Cache-Control header")
	}
}

func TestStatic_dev_panicOnMissingDir(t *testing.T) {
	app := newAppWithDev(t, true)
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for missing devDir, got none")
		}
	}()
	app.Static("/static/", fstest.MapFS{}, "/no/such/directory/ever")
}

func TestConfig_dev_fromConfigFile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "smeldr.config")
	if err := os.WriteFile(cfgPath, []byte("dev = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	fc, err := loadConfigFile(cfgPath)
	if err != nil {
		t.Fatalf("loadConfigFile: %v", err)
	}
	if !fc.Dev {
		t.Error("expected Config.Dev = true from smeldr.config, got false")
	}
}

func TestConfig_dev_merge(t *testing.T) {
	// File config sets Dev=true; Go config has Dev=false (zero) — should merge.
	goCfg := Config{}
	fileCfg := Config{Dev: true}
	merged := mergeFileConfig(goCfg, fileCfg)
	if !merged.Dev {
		t.Error("mergeFileConfig: expected Dev=true after merge, got false")
	}

	// Go config has Dev=true; file has Dev=false — Go value must be preserved.
	goCfg2 := Config{Dev: true}
	fileCfg2 := Config{Dev: false}
	merged2 := mergeFileConfig(goCfg2, fileCfg2)
	if !merged2.Dev {
		t.Error("mergeFileConfig: Go Dev=true must not be overwritten by file Dev=false")
	}
}

func TestWithImmutableCache(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := withImmutableCache(inner)

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	cc := w.Header().Get("Cache-Control")
	if cc != "public, max-age=31536000, immutable" {
		t.Errorf("Cache-Control = %q, want immutable", cc)
	}
}

// Compile-time check: fs.FS is accepted as prod argument.
var _ fs.FS = fstest.MapFS{}
