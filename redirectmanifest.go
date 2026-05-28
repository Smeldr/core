package smeldr

import (
	"encoding/json"
	"net/http"
	"time"
)

// — JSON types ————————————————————————————————————————————————————————————

// redirectManifestEntry is the JSON representation of a single [RedirectEntry]
// in the manifest served at /.well-known/redirects.json.
type redirectManifestEntry struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Code     int    `json:"code"`
	IsPrefix bool   `json:"is_prefix"`
}

// redirectManifest is the JSON envelope served at /.well-known/redirects.json.
type redirectManifest struct {
	Site      string                  `json:"site"`
	Generated string                  `json:"generated"` // RFC 3339
	Count     int                     `json:"count"`
	Entries   []redirectManifestEntry `json:"entries"`
}

// buildRedirectManifest constructs a [redirectManifest] from the given entries.
// Entries are sorted ascending by From for deterministic output.
// Generated is set to the current UTC time in RFC 3339 format.
func buildRedirectManifest(site string, entries []RedirectEntry) redirectManifest {
	out := make([]redirectManifestEntry, len(entries))
	for i, e := range entries {
		out[i] = redirectManifestEntry{
			From:     e.From,
			To:       e.To,
			Code:     int(e.Code),
			IsPrefix: e.IsPrefix,
		}
	}
	// entries are already sorted by All() — no additional sort needed.
	return redirectManifest{
		Site:      site,
		Generated: time.Now().UTC().Format(time.RFC3339),
		Count:     len(out),
		Entries:   out,
	}
}

// — Handler ————————————————————————————————————————————————————————————————

// newRedirectManifestHandler returns an [http.Handler] that serves the live
// redirect table as JSON at /.well-known/redirects.json.
//
// Unlike the cookie manifest, redirect entries can change at runtime
// (via [App.Redirect] or [RedirectStore.Add]), so the manifest is built on
// each request from the live [RedirectStore].
//
// The endpoint always returns 200 with valid JSON — even when the store is
// empty. Optionally pass [ManifestAuth] to restrict access to authenticated
// requests.
func newRedirectManifestHandler(site string, store *RedirectStore, opts ...Option) http.Handler {
	var auth AuthFunc
	for _, opt := range opts {
		if ma, ok := opt.(manifestAuthOption); ok {
			auth = ma.auth
		}
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth != nil {
			if _, ok := auth.authenticate(r); !ok {
				WriteError(w, r, ErrUnauth)
				return
			}
		}

		entries := store.All()
		manifest := buildRedirectManifest(site, entries)
		body, _ := json.MarshalIndent(manifest, "", "  ") //nolint:errcheck — only fails on cyclic types

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		w.Write(body) //nolint:errcheck
	})
}
