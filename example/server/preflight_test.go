//go:build preflight

package main

// Preflight test — spawns the compiled binary as a real OS process.
// This test is NOT run by `go test ./...`; it requires an explicit build tag:
//
//	cd example/server && go test -tags preflight -v -run TestPreflight .
//
// Use this before tagging a new example/server release to confirm that
// the binary starts correctly, serves /_health, and shuts down cleanly.

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// freePort finds an available TCP port on localhost and returns it.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("freePort: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port
}

// waitReady polls /_health on baseURL until it returns 200 or timeout elapses.
func waitReady(baseURL string, timeout time.Duration) bool {
	client := &http.Client{Timeout: 500 * time.Millisecond}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := client.Get(baseURL + "/_health")
		if err == nil && resp.StatusCode == http.StatusOK {
			resp.Body.Close()
			return true
		}
		if resp != nil {
			resp.Body.Close()
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// TestPreflight builds the smeldr-server binary, starts it as a real OS process,
// confirms it comes up healthy, probes a representative route, then kills it.
func TestPreflight(t *testing.T) {
	// Build the binary.
	binName := "smeldr-server"
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	bin := filepath.Join(t.TempDir(), binName)
	out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput()
	if err != nil {
		t.Fatalf("build smeldr-server: %v\n%s", err, out)
	}

	port := freePort(t)
	dbPath := filepath.Join(t.TempDir(), "preflight.db")
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)

	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(),
		"SECRET=preflight-secret-32-bytes-xxxx",
		fmt.Sprintf("BASE_URL=http://127.0.0.1:%d", port),
		fmt.Sprintf("PORT=%d", port),
		fmt.Sprintf("ADDR=127.0.0.1:%d", port),
		"ENABLE_TOKENS=1",
		"ENABLE_ORCHESTRATION=1",
		"ENABLE_RELATIONS=1",
		fmt.Sprintf("DATABASE_PATH=%s", dbPath),
	)

	if err := cmd.Start(); err != nil {
		t.Fatalf("start binary: %v", err)
	}
	t.Cleanup(func() {
		cmd.Process.Kill() //nolint:errcheck
		cmd.Wait()         //nolint:errcheck
	})

	// Wait for the server to become ready (up to 5 seconds).
	if !waitReady(baseURL, 5*time.Second) {
		t.Fatal("preflight server did not become ready within 5 seconds")
	}

	// /goals route must be present with ENABLE_ORCHESTRATION=1.
	// Without a bearer token the server returns 401, not 404 — any non-404 status
	// confirms the route is registered.
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(baseURL + "/goals")
	if err != nil {
		t.Fatalf("GET /goals: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Errorf("GET /goals: got 404; route should exist with ENABLE_ORCHESTRATION=1")
	}
}
