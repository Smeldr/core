package smeldr

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// InMemoryCache caches only anonymous requests and never stores a personal
// response (core-response-cache-identity, read rules step 1).

func TestInMemoryCache_CredentialsBypass(t *testing.T) {
	cases := []struct {
		name string
		set  func(r *http.Request)
		path string
	}{
		{"authorization", func(r *http.Request) { r.Header.Set("Authorization", "Bearer x") }, "/p"},
		{"cookie", func(r *http.Request) { r.Header.Set("Cookie", "session=x") }, "/p"},
		{"preview", func(*http.Request) {}, "/p?preview=t"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mw, stop := NewInMemoryCache(time.Minute)
			defer stop()
			calls := 0
			h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Write([]byte("personal")) //nolint:errcheck
			}))
			for i := range 2 {
				r := httptest.NewRequest(http.MethodGet, c.path, nil)
				c.set(r)
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				if got := w.Header().Get("X-Cache"); got != "BYPASS" {
					t.Errorf("request %d X-Cache = %q; want BYPASS", i, got)
				}
			}
			if calls != 2 {
				t.Errorf("handler ran %d times; want 2 (never served from cache)", calls)
			}
			// Nothing was stored: an anonymous request is a MISS.
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/p", nil))
			if got := w.Header().Get("X-Cache"); got != "MISS" {
				t.Errorf("anonymous after credentialed = %q; want MISS", got)
			}
		})
	}
}

func TestInMemoryCache_PersonalResponseNotStored(t *testing.T) {
	cases := map[string]func(http.Header){
		"set-cookie":       func(h http.Header) { h.Set("Set-Cookie", "csrf=1") },
		"private":          func(h http.Header) { h.Set("Cache-Control", "private") },
		"no-store":         func(h http.Header) { h.Set("Cache-Control", "no-store") },
		"no-store-in-list": func(h http.Header) { h.Set("Cache-Control", "max-age=60, no-store") },
	}
	for name, set := range cases {
		t.Run(name, func(t *testing.T) {
			mw, stop := NewInMemoryCache(time.Minute)
			defer stop()
			h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				set(w.Header())
				w.Write([]byte("mine")) //nolint:errcheck
			}))
			for i, want := range []string{"MISS", "MISS"} {
				w := httptest.NewRecorder()
				h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/me", nil))
				if got := w.Header().Get("X-Cache"); got != want {
					t.Errorf("request %d X-Cache = %q; want %q (never stored)", i, got, want)
				}
			}
		})
	}
}

func TestInMemoryCache_AnonymousStillHits(t *testing.T) {
	mw, stop := NewInMemoryCache(time.Minute)
	defer stop()
	h := mw(okHandler("public"))
	for i, want := range []string{"MISS", "HIT"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/pub", nil))
		if got := w.Header().Get("X-Cache"); got != want {
			t.Errorf("request %d X-Cache = %q; want %q", i, got, want)
		}
	}
}
