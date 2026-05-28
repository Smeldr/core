package smeldr

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// helpers

func okHandler(body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(body)) //nolint:errcheck
	})
}

func runMiddleware(mw func(http.Handler) http.Handler, r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	mw(okHandler("ok")).ServeHTTP(w, r)
	return w
}

// — Recoverer —————————————————————————————————————————————————————————————

func TestRecovererCatchesPanic(t *testing.T) {
	mw := Recoverer()
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("test panic")
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestRecovererPassesThrough(t *testing.T) {
	mw := Recoverer()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := runMiddleware(mw, req)
	if w.Code != http.StatusOK {
		t.Errorf("status: got %d want %d", w.Code, http.StatusOK)
	}
}

// — RequestLogger —————————————————————————————————————————————————————————

func TestRequestLoggerSetsRequestID(t *testing.T) {
	mw := RequestLogger()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()
	mw(okHandler("")).ServeHTTP(w, req)

	if w.Header().Get("X-Request-ID") == "" {
		t.Error("expected X-Request-ID to be set on response")
	}
}

func TestRequestLoggerPreservesExistingRequestID(t *testing.T) {
	mw := RequestLogger()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-ID", "my-request-id")
	w := httptest.NewRecorder()
	mw(okHandler("")).ServeHTTP(w, req)

	if got := w.Header().Get("X-Request-ID"); got != "my-request-id" {
		t.Errorf("X-Request-ID: got %q want %q", got, "my-request-id")
	}
}

// — CORS ——————————————————————————————————————————————————————————————————

func TestCORSHeaders(t *testing.T) {
	mw := CORS("https://example.com")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := runMiddleware(mw, req)

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://example.com" {
		t.Errorf("Allow-Origin: got %q want %q", got, "https://example.com")
	}
	if w.Header().Get("Access-Control-Allow-Methods") == "" {
		t.Error("expected Access-Control-Allow-Methods to be set")
	}
	if w.Header().Get("Access-Control-Allow-Headers") == "" {
		t.Error("expected Access-Control-Allow-Headers to be set")
	}
}

func TestCORSPreflight(t *testing.T) {
	called := false
	handler := CORS("https://example.com")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))

	req := httptest.NewRequest(http.MethodOptions, "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("status: got %d want %d", w.Code, http.StatusNoContent)
	}
	if called {
		t.Error("expected next handler NOT to be called on OPTIONS preflight")
	}
}

// — MaxBodySize ———————————————————————————————————————————————————————————

func TestMaxBodySizeRejects(t *testing.T) {
	mw := MaxBodySize(5) // 5 bytes max
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Try to read more than allowed — this triggers the 413.
		_, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))

	body := strings.NewReader("this is definitely more than five bytes")
	req := httptest.NewRequest(http.MethodPost, "/", body)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status: got %d want %d", w.Code, http.StatusRequestEntityTooLarge)
	}
}

func TestMaxBodySizeAllows(t *testing.T) {
	mw := MaxBodySize(100)
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("hi"))
	w := runMiddleware(mw, req)
	if w.Code != http.StatusOK {
		t.Errorf("status: got %d want %d", w.Code, http.StatusOK)
	}
}

// — SecurityHeaders ———————————————————————————————————————————————————————

func TestSecurityHeadersPresent(t *testing.T) {
	mw := SecurityHeaders()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := runMiddleware(mw, req)

	checks := map[string]string{
		"Strict-Transport-Security": "max-age=63072000; includeSubDomains",
		"X-Frame-Options":           "DENY",
		"X-Content-Type-Options":    "nosniff",
		"Referrer-Policy":           "strict-origin-when-cross-origin",
		"Content-Security-Policy":   "default-src 'self'; frame-ancestors 'none'",
	}
	for header, want := range checks {
		if got := w.Header().Get(header); got != want {
			t.Errorf("%s: got %q want %q", header, got, want)
		}
	}
}

// — RateLimit —————————————————————————————————————————————————————————————

func TestRateLimitAllows(t *testing.T) {
	mw := RateLimit(10, time.Second)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	w := runMiddleware(mw, req)
	if w.Code != http.StatusOK {
		t.Errorf("status: got %d want %d", w.Code, http.StatusOK)
	}
}

func TestRateLimitRejects(t *testing.T) {
	// Allow only 1 request per minute.
	mw := RateLimit(1, time.Minute)
	handler := mw(okHandler("ok"))
	addr := "10.0.0.1:9999"

	// First request should succeed.
	req1 := httptest.NewRequest(http.MethodGet, "/", nil)
	req1.RemoteAddr = addr
	w1 := httptest.NewRecorder()
	handler.ServeHTTP(w1, req1)
	if w1.Code != http.StatusOK {
		t.Fatalf("first request: got %d want 200", w1.Code)
	}

	// Second request should be rejected immediately.
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.RemoteAddr = addr
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req2)
	if w2.Code != http.StatusTooManyRequests {
		t.Errorf("second request: got %d want 429", w2.Code)
	}
	if w2.Header().Get("Retry-After") == "" {
		t.Error("expected Retry-After header on 429 response")
	}
}

// — InMemoryCache —————————————————————————————————————————————————————————

func TestInMemoryCacheMISS(t *testing.T) {
	mw := InMemoryCache(time.Minute)
	req := httptest.NewRequest(http.MethodGet, "/page", nil)
	w := runMiddleware(mw, req)
	if got := w.Header().Get("X-Cache"); got != "MISS" {
		t.Errorf("X-Cache: got %q want MISS", got)
	}
}

func TestInMemoryCacheHIT(t *testing.T) {
	mw := InMemoryCache(time.Minute)
	handler := mw(okHandler("cached-body"))

	req1 := httptest.NewRequest(http.MethodGet, "/cached", nil)
	w1 := httptest.NewRecorder()
	handler.ServeHTTP(w1, req1)
	if w1.Header().Get("X-Cache") != "MISS" {
		t.Fatalf("first request: expected MISS, got %q", w1.Header().Get("X-Cache"))
	}

	req2 := httptest.NewRequest(http.MethodGet, "/cached", nil)
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req2)
	if got := w2.Header().Get("X-Cache"); got != "HIT" {
		t.Errorf("second request: X-Cache got %q want HIT", got)
	}
	if got := w2.Body.String(); got != "cached-body" {
		t.Errorf("body: got %q want %q", got, "cached-body")
	}
}

func TestInMemoryCacheEviction(t *testing.T) {
	// Max 1 entry — inserting a second key must evict the first.
	mw := InMemoryCache(time.Minute, CacheMaxEntries(1))
	handler := mw(okHandler("body"))

	// Populate key A.
	reqA := httptest.NewRequest(http.MethodGet, "/a", nil)
	handler.ServeHTTP(httptest.NewRecorder(), reqA)

	// Populate key B — evicts A.
	reqB := httptest.NewRequest(http.MethodGet, "/b", nil)
	handler.ServeHTTP(httptest.NewRecorder(), reqB)

	// Key A must now be a MISS again.
	reqA2 := httptest.NewRequest(http.MethodGet, "/a", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, reqA2)
	if got := w.Header().Get("X-Cache"); got != "MISS" {
		t.Errorf("after eviction, /a: X-Cache got %q want MISS", got)
	}
}

func TestInMemoryCacheTTLExpiry(t *testing.T) {
	// TTL of 1 nanosecond — entry expires immediately.
	mw := InMemoryCache(time.Nanosecond)
	handler := mw(okHandler("body"))

	req1 := httptest.NewRequest(http.MethodGet, "/ttl", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req1)

	// Sleep to let the 1ns TTL elapse.
	time.Sleep(time.Millisecond)

	req2 := httptest.NewRequest(http.MethodGet, "/ttl", nil)
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req2)
	if got := w2.Header().Get("X-Cache"); got != "MISS" {
		t.Errorf("after TTL expiry: X-Cache got %q want MISS", got)
	}
}

func TestInMemoryCacheSkipsNonGET(t *testing.T) {
	callCount := 0
	mw := InMemoryCache(time.Minute)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.WriteHeader(http.StatusOK)
	}))

	// POST requests should always reach the handler.
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodPost, "/post", nil)
		handler.ServeHTTP(httptest.NewRecorder(), req)
	}
	if callCount != 3 {
		t.Errorf("expected handler called 3 times for POST; got %d", callCount)
	}
}

// — Chain —————————————————————————————————————————————————————————————————

func TestChain(t *testing.T) {
	order := []string{}

	mw1 := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "mw1-before")
			next.ServeHTTP(w, r)
			order = append(order, "mw1-after")
		})
	}
	mw2 := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "mw2-before")
			next.ServeHTTP(w, r)
			order = append(order, "mw2-after")
		})
	}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		order = append(order, "handler")
	})

	h := Chain(inner, mw1, mw2)
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	want := []string{"mw1-before", "mw2-before", "handler", "mw2-after", "mw1-after"}
	if len(order) != len(want) {
		t.Fatalf("order: got %v want %v", order, want)
	}
	for i, v := range want {
		if order[i] != v {
			t.Errorf("order[%d]: got %q want %q", i, order[i], v)
		}
	}
}

// — TrustedProxy ——————————————————————————————————————————————————————————

// TestTrustedProxy_bucketsByForwardedIP verifies that RateLimit with TrustedProxy
// uses X-Forwarded-For as the per-IP key rather than RemoteAddr.
func TestTrustedProxy_bucketsByForwardedIP(t *testing.T) {
	// Allow exactly 1 request per minute per IP.
	mw := RateLimit(1, time.Minute, TrustedProxy())
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := mw(inner)

	do := func(xff string) int {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-Forwarded-For", xff)
		req.RemoteAddr = "10.1.1.1:0" // same proxy address for every request
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w.Code
	}

	if code := do("192.0.2.1"); code != http.StatusOK {
		t.Errorf("first 192.0.2.1: got %d, want 200", code)
	}
	// Same forwarded IP exhausts its bucket.
	if code := do("192.0.2.1"); code != http.StatusTooManyRequests {
		t.Errorf("second 192.0.2.1: got %d, want 429", code)
	}
	// Different forwarded IP has an independent bucket — must not be blocked.
	if code := do("192.0.2.2"); code != http.StatusOK {
		t.Errorf("first 192.0.2.2 (different IP): got %d, want 200", code)
	}
}

// TestTrustedProxy_xRealIP verifies that X-Real-IP is preferred over
// X-Forwarded-For when both are present.
func TestTrustedProxy_xRealIP(t *testing.T) {
	mw := RateLimit(1, time.Minute, TrustedProxy())
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := mw(inner)

	do := func(xri, xff string) int {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		if xri != "" {
			req.Header.Set("X-Real-IP", xri)
		}
		if xff != "" {
			req.Header.Set("X-Forwarded-For", xff)
		}
		req.RemoteAddr = "10.1.1.1:0"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w.Code
	}

	// First request: X-Real-IP=198.51.100.1, X-Forwarded-For=198.51.100.99.
	if code := do("198.51.100.1", "198.51.100.99"); code != http.StatusOK {
		t.Errorf("first with X-Real-IP: got %d, want 200", code)
	}
	// Second request with same X-Real-IP must be rate-limited (X-Real-IP is used).
	if code := do("198.51.100.1", "198.51.100.99"); code != http.StatusTooManyRequests {
		t.Errorf("second with same X-Real-IP: got %d, want 429", code)
	}
	// Request with the X-Forwarded-For IP (different from X-Real-IP) is not limited.
	if code := do("", "198.51.100.99"); code != http.StatusOK {
		t.Errorf("request with X-Forwarded-For only: got %d, want 200", code)
	}
}

// — Benchmarks ————————————————————————————————————————————————————————————

func BenchmarkInMemoryCacheHIT(b *testing.B) {
	mw := InMemoryCache(time.Minute)
	handler := mw(okHandler("benchmark-body"))

	// Warm the cache.
	req := httptest.NewRequest(http.MethodGet, "/bench", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest(http.MethodGet, "/bench", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Header().Get("X-Cache") != "HIT" {
			b.Fatal("expected cache HIT")
		}
	}
}
