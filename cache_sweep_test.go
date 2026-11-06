package smeldr

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// withFastCacheSweep makes the cache sweep goroutines tick every
// millisecond for the test (T1160).
func withFastCacheSweep(t *testing.T) {
	t.Helper()
	old := cacheSweepInterval
	cacheSweepInterval = time.Millisecond
	t.Cleanup(func() { cacheSweepInterval = old })
}

// TestModule_backgroundSweepRemovesExpired: a Cache module's background
// goroutine sweeps expired entries on its ticker, every run (T1160).
func TestModule_backgroundSweepRemovesExpired(t *testing.T) {
	withFastCacheSweep(t)
	repo := NewMemoryRepo[*testPost]()
	p := seedPost(t, repo, "bg-sweep", Published)
	m := NewModule((*testPost)(nil), Repo(repo), Cache(time.Millisecond))
	t.Cleanup(m.Stop)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/testposts/"+p.Slug, nil)
	r.SetPathValue("slug", p.Slug)
	m.showHandler(w, r)

	deadline := time.Now().Add(5 * time.Second)
	for {
		m.cache.mu.Lock()
		n := len(m.cache.entries)
		m.cache.mu.Unlock()
		if n == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("background sweep left %d entries", n)
		}
		time.Sleep(time.Millisecond)
	}
}

// TestNewInMemoryCache_backgroundSweepRuns: the middleware's sweep
// goroutine ticks and stops cleanly. The expired entry it removes is not
// reachable from outside, so the test lets several ticks pass before stop
// (T1160).
func TestNewInMemoryCache_backgroundSweepRuns(t *testing.T) {
	withFastCacheSweep(t)
	mw, stop := NewInMemoryCache(time.Millisecond)
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil))
	time.Sleep(20 * time.Millisecond)
	stop()
}
