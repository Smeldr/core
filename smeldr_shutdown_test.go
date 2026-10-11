package smeldr

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

// countingStop is a stoppable that counts its Stop calls.
type countingStop struct{ n atomic.Int32 }

func (c *countingStop) Stop() { c.n.Add(1) }

const shutdownTestSecret = "supersecretkey16"

func newShutdownTestApp(t *testing.T) *App {
	t.Helper()
	return New(Config{BaseURL: "https://example.com", Secret: []byte(shutdownTestSecret)})
}

// startServe runs app.serve on a fresh loopback listener and returns its base
// URL, a cancel func that starts shutdown, and the channel serve's result
// arrives on.
func startServe(t *testing.T, app *App, timeout time.Duration) (string, context.CancelFunc, <-chan error) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- app.serve(ctx, ln, timeout) }()
	return "http://" + ln.Addr().String(), cancel, done
}

// waitServe waits up to within for serve to return and reports how long it
// took after start and its error.
func waitServe(t *testing.T, done <-chan error, start time.Time, within time.Duration) (time.Duration, error) {
	t.Helper()
	select {
	case err := <-done:
		return time.Since(start), err
	case <-time.After(within):
		t.Fatalf("serve did not return within %v", within)
		return 0, nil
	}
}

// openStream opens a GET request to url and returns the response once its
// headers have arrived.
func openStream(t *testing.T, url, token string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	return resp
}

// An open event stream no longer holds shutdown for its full timeout: serve
// returns nil quickly, the client sees its connection end, and the modules
// are stopped.
func TestServe_StreamEndsAndShutdownIsQuick(t *testing.T) {
	app := newShutdownTestApp(t).EventStream()
	mod := &countingStop{}
	app.stoppableModules = append(app.stoppableModules, mod)
	base, cancel, done := startServe(t, app, 5*time.Second)

	tok, err := SignToken(User{ID: "u1", Roles: []Role{Author}}, shutdownTestSecret, 0)
	if err != nil {
		t.Fatalf("SignToken: %v", err)
	}
	resp := openStream(t, base+"/_events/stream", tok)

	read := make(chan error, 1)
	go func() {
		_, err := io.Copy(io.Discard, resp.Body)
		read <- err
	}()

	start := time.Now()
	cancel()
	took, err := waitServe(t, done, start, 4*time.Second)
	if err != nil {
		t.Errorf("serve = %v, want nil", err)
	}
	if took >= shutdownStreamGrace {
		t.Errorf("shutdown took %v, want under the %v grace (the stream is released at once)", took, shutdownStreamGrace)
	}
	select {
	case <-read:
	case <-time.After(2 * time.Second):
		t.Error("stream client still connected after shutdown")
	}
	if got := mod.n.Load(); got != 1 {
		t.Errorf("Stop called %d times, want 1", got)
	}
}

// A long-lived handler that returns only on r.Context().Done(), as MCP SSE
// does, ends after the grace, and serve returns nil.
func TestServe_LongLivedHandlerEndsAfterGrace(t *testing.T) {
	app := newShutdownTestApp(t)
	ended := make(chan struct{})
	app.Handle("GET /sse", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(ended)
	}))
	base, cancel, done := startServe(t, app, 5*time.Second)
	openStream(t, base+"/sse", "")

	start := time.Now()
	cancel()
	took, err := waitServe(t, done, start, 4*time.Second)
	if err != nil {
		t.Errorf("serve = %v, want nil", err)
	}
	if took > shutdownStreamGrace+time.Second {
		t.Errorf("shutdown took %v, want about the %v grace", took, shutdownStreamGrace)
	}
	select {
	case <-ended:
	default:
		t.Error("handler did not return")
	}
}

// A handler that ignores cancellation makes Shutdown time out; the modules
// are still stopped and the deadline error is returned. The 100 ms timeout is
// below the grace, so the base context is cancelled at half the timeout.
func TestServe_StopsModulesWhenShutdownErrors(t *testing.T) {
	app := newShutdownTestApp(t)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	app.Handle("GET /stuck", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-release
	}))
	mod := &countingStop{}
	app.stoppableModules = append(app.stoppableModules, mod)
	base, cancel, done := startServe(t, app, 100*time.Millisecond)
	openStream(t, base+"/stuck", "")

	start := time.Now()
	cancel()
	_, err := waitServe(t, done, start, 2*time.Second)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("serve = %v, want context.DeadlineExceeded", err)
	}
	if got := mod.n.Load(); got != 1 {
		t.Errorf("Stop called %d times, want 1 even though Shutdown failed", got)
	}
}

// With no open requests, shutdown is clean: nil, and the modules stopped.
func TestServe_StopsModulesOnCleanShutdown(t *testing.T) {
	app := newShutdownTestApp(t)
	mod := &countingStop{}
	app.stoppableModules = append(app.stoppableModules, mod)
	_, cancel, done := startServe(t, app, 5*time.Second)

	cancel()
	if _, err := waitServe(t, done, time.Now(), 2*time.Second); err != nil {
		t.Errorf("serve = %v, want nil", err)
	}
	if got := mod.n.Load(); got != 1 {
		t.Errorf("Stop called %d times, want 1", got)
	}
}

// A listener that is already closed makes Serve fail at once; serve returns
// that error without starting a shutdown.
func TestServe_ServeErrorReturned(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	ln.Close()
	if err := newShutdownTestApp(t).serve(context.Background(), ln, time.Second); err == nil {
		t.Error("serve on a closed listener = nil, want an error")
	}
}

// Run returns the listen error for an address already in use.
func TestRun_ListenError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()
	if err := newShutdownTestApp(t).Run(ln.Addr().String()); err == nil {
		t.Error("Run on a busy address = nil, want an error")
	}
}
