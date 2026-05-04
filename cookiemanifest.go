package forge

import (
	"cmp"
	"encoding/json"
	"net/http"
	"slices"
	"time"
)

// — ManifestAuth option ————————————————————————————————————————————————————

// manifestAuthOption restricts the /.well-known/cookies.json endpoint to
// authenticated requests.
type manifestAuthOption struct {
	auth AuthFunc
}

// ManifestAuth returns an [Option] that restricts the /.well-known/cookies.json
// endpoint to requests that pass the given [AuthFunc].
//
// A 401 Unauthorized response is returned for unauthenticated requests.
// Omit ManifestAuth to make the endpoint publicly accessible.
func ManifestAuth(auth AuthFunc) Option {
	return manifestAuthOption{auth: auth}
}

func (manifestAuthOption) isOption() {}

// — JSON types ————————————————————————————————————————————————————————————

// cookieManifestEntry is the JSON representation of a single [Cookie]
// declaration in the compliance manifest at /.well-known/cookies.json.
type cookieManifestEntry struct {
	Name     string         `json:"name"`
	Category CookieCategory `json:"category"`
	HttpOnly bool           `json:"http_only"`
	Secure   bool           `json:"secure"`
	SameSite string         `json:"same_site"`
	MaxAge   int            `json:"max_age"`
	Purpose  string         `json:"purpose"`
}

// cookieManifest is the JSON envelope served at /.well-known/cookies.json.
type cookieManifest struct {
	Site      string                `json:"site"`
	Generated string                `json:"generated"` // RFC 3339
	Count     int                   `json:"count"`
	Cookies   []cookieManifestEntry `json:"cookies"`
}

// sameSiteName maps an [http.SameSite] value to its canonical string name.
// The zero value is treated as Strict, matching the default in [Cookie.httpCookie].
func sameSiteName(s http.SameSite) string {
	switch s {
	case http.SameSiteLaxMode:
		return "Lax"
	case http.SameSiteNoneMode:
		return "None"
	default:
		return "Strict"
	}
}

// buildManifest constructs a [cookieManifest] from decls, sorted
// alphabetically by name for deterministic JSON output.
func buildManifest(site string, decls []Cookie) cookieManifest {
	entries := make([]cookieManifestEntry, len(decls))
	for i, c := range decls {
		entries[i] = cookieManifestEntry{
			Name:     c.Name,
			Category: c.Category,
			HttpOnly: c.HttpOnly,
			Secure:   c.Secure,
			SameSite: sameSiteName(c.SameSite),
			MaxAge:   c.MaxAge,
			Purpose:  c.Purpose,
		}
	}
	slices.SortFunc(entries, func(a, b cookieManifestEntry) int {
		return cmp.Compare(a.Name, b.Name)
	})
	return cookieManifest{
		Site:      site,
		Generated: time.Now().UTC().Format(time.RFC3339),
		Count:     len(entries),
		Cookies:   entries,
	}
}

// — Handler ————————————————————————————————————————————————————————————————

// newCookieManifestHandler returns an [http.Handler] that serves the cookie
// compliance manifest as JSON.
//
// The manifest is built once at construction time — cookie declarations are
// fixed at startup and never change. Optionally pass [ManifestAuth] to
// restrict access to authenticated requests.
func newCookieManifestHandler(site string, decls []Cookie, opts ...Option) http.Handler {
	manifest := buildManifest(site, decls)
	body, _ := json.MarshalIndent(manifest, "", "  ") //nolint:errcheck — only fails on cyclic types

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
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		w.Write(body) //nolint:errcheck
	})
}
