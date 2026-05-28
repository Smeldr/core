package smeldr

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
)

// ---------------------------------------------------------------------------
// RedirectCode
// ---------------------------------------------------------------------------

// RedirectCode is the HTTP status code issued for a redirect entry.
// Use [Permanent] (301) for URL changes that search engines should follow and
// update, and [Gone] (410) for content that has been intentionally removed.
// 410 signals de-indexing significantly faster than 404.
type RedirectCode int

const (
	// Permanent issues a 301 Moved Permanently response.
	// Use when the resource has moved to a new URL and the change is final.
	Permanent RedirectCode = http.StatusMovedPermanently

	// Gone issues a 410 Gone response.
	// Use when the resource has been intentionally removed.
	// Pass an empty string as the destination to [App.Redirect].
	Gone RedirectCode = http.StatusGone
)

// ---------------------------------------------------------------------------
// RedirectEntry
// ---------------------------------------------------------------------------

// RedirectEntry describes a single redirect rule. Obtain entries via
// [App.Redirect] or the [Redirects] module option; do not construct them
// directly in production code unless building a custom migration tool.
//
//   - From is the absolute request path that triggers the rule, e.g. "/posts/hello".
//   - To is the destination path. An empty To with Code == Gone issues 410.
//   - IsPrefix, when true, matches any path whose prefix equals From and
//     rewrites the suffix onto To at request time — a single entry covers
//     an entire renamed module prefix with zero per-request allocations
//     beyond the destination string concatenation.
type RedirectEntry struct {
	From     string       // absolute path to match
	To       string       // destination path; empty = 410 Gone
	Code     RedirectCode // Permanent (301) or Gone (410)
	IsPrefix bool         // prefix-rewrite semantics (Decision 17 amendment)
}

// ---------------------------------------------------------------------------
// From type and Redirects option
// ---------------------------------------------------------------------------

// From is the old URL prefix supplied to the [Redirects] module option.
// Wrapping in a named type makes call sites self-documenting:
//
//	smeldr.Redirects(smeldr.From("/posts"), "/articles")
type From string

// redirectsOption carries a bulk prefix redirect registered via [Redirects].
// It implements [Option] so it can be passed to [App.Content].
type redirectsOption struct {
	from From
	to   string
}

func (redirectsOption) isOption() {}

// Redirects returns a module [Option] that registers a 301 prefix redirect
// from old to to. Use it when renaming a module's URL prefix so all inbound
// links are preserved automatically:
//
//	app.Content(&BlogPost{},
//	    smeldr.At("/articles"),
//	    smeldr.Redirects(smeldr.From("/posts"), "/articles"),
//	)
func Redirects(from From, to string) Option {
	return redirectsOption{from: from, to: to}
}

// ---------------------------------------------------------------------------
// RedirectStore
// ---------------------------------------------------------------------------

// RedirectStore holds the runtime redirect table. Exact lookups are O(1) map
// reads; prefix lookups iterate a short slice sorted longest-first, ending on
// the first match. The store is safe for concurrent use.
type RedirectStore struct {
	mu     sync.RWMutex
	exact  map[string]RedirectEntry // keyed by RedirectEntry.From
	prefix []RedirectEntry          // sorted descending by len(From)
}

// NewRedirectStore returns an empty [RedirectStore] ready for use.
func NewRedirectStore() *RedirectStore {
	return &RedirectStore{exact: make(map[string]RedirectEntry)}
}

// Add registers e in the store. For exact entries, if e.To is already the
// From of an existing entry the chain is collapsed (A→B + B→C = A→C).
// The maximum collapse depth is 10; exceeding it panics with a descriptive
// message (Decision 24). Gone entries are never collapsed through — a Gone
// destination is terminal.
//
// For prefix entries (e.IsPrefix == true) the entry is appended to the prefix
// slice which is then re-sorted descending by len(From) to ensure
// longest-prefix-first lookup.
func (s *RedirectStore) Add(e RedirectEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if e.IsPrefix {
		s.prefix = append(s.prefix, e)
		slices.SortStableFunc(s.prefix, func(a, b RedirectEntry) int {
			return cmp.Compare(len(b.From), len(a.From))
		})
		return
	}

	// Chain collapse for exact entries.
	const maxDepth = 10
	depth := 0
	current := e
	for {
		next, ok := s.exact[current.To]
		if !ok {
			break
		}
		// Gone destinations are terminal — do not collapse through them.
		if next.Code == Gone {
			break
		}
		depth++
		if depth > maxDepth {
			panic(fmt.Sprintf(
				"smeldr: redirect chain collapse exceeded maximum depth %d: %s → ... → %s",
				maxDepth, e.From, current.To,
			))
		}
		current = RedirectEntry{
			From: e.From,
			To:   next.To,
			Code: next.Code,
		}
	}

	s.exact[e.From] = current
}

// Get returns the [RedirectEntry] matching path, or (RedirectEntry{}, false)
// when no rule applies. Exact entries are checked first; if no exact match is
// found the prefix slice is scanned longest-first.
func (s *RedirectStore) Get(path string) (RedirectEntry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if e, ok := s.exact[path]; ok {
		return e, true
	}
	for _, e := range s.prefix {
		if strings.HasPrefix(path, e.From) {
			return e, true
		}
	}
	return RedirectEntry{}, false
}

// All returns a deterministically sorted slice of all registered entries
// (exact + prefix), sorted ascending by From. Intended for manifest
// serialisation.
func (s *RedirectStore) All() []RedirectEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]RedirectEntry, 0, len(s.exact)+len(s.prefix))
	for _, e := range s.exact {
		out = append(out, e)
	}
	out = append(out, s.prefix...)
	slices.SortFunc(out, func(a, b RedirectEntry) int { return cmp.Compare(a.From, b.From) })
	return out
}

// Len returns the total number of registered entries (exact + prefix).
func (s *RedirectStore) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.exact) + len(s.prefix)
}

// ---------------------------------------------------------------------------
// DB persistence
// ---------------------------------------------------------------------------

// dbRedirectRow is the scan target for rows from the forge_redirects table.
type dbRedirectRow struct {
	From     string `db:"from_path"`
	To       string `db:"to_path"`
	Code     int    `db:"code"`
	IsPrefix bool   `db:"is_prefix"`
}

// Load reads all rows from the forge_redirects table and registers them via
// [RedirectStore.Add]. Chain collapse and validation rules are applied during
// load. The forge_redirects table must exist — see the README for the schema.
func (s *RedirectStore) Load(ctx context.Context, db DB) error {
	rows, err := Query[dbRedirectRow](ctx, db,
		"SELECT from_path, to_path, code, is_prefix FROM forge_redirects")
	if err != nil {
		return err
	}
	for _, row := range rows {
		s.Add(RedirectEntry{
			From:     row.From,
			To:       row.To,
			Code:     RedirectCode(row.Code),
			IsPrefix: row.IsPrefix,
		})
	}
	return nil
}

// Save upserts e into the forge_redirects table. The forge_redirects table
// must exist — see the README for the schema.
func (s *RedirectStore) Save(ctx context.Context, db DB, e RedirectEntry) error {
	_, err := db.ExecContext(ctx,
		"INSERT INTO forge_redirects (from_path, to_path, code, is_prefix) "+
			"VALUES ($1, $2, $3, $4) "+
			"ON CONFLICT (from_path) DO UPDATE SET to_path=$2, code=$3, is_prefix=$4",
		e.From, e.To, int(e.Code), e.IsPrefix,
	)
	return err
}

// Remove deletes the entry with the given from path from the forge_redirects
// table. The forge_redirects table must exist — see the README for the schema.
func (s *RedirectStore) Remove(ctx context.Context, db DB, from string) error {
	_, err := db.ExecContext(ctx,
		"DELETE FROM forge_redirects WHERE from_path = $1", from)
	return err
}

// ---------------------------------------------------------------------------
// HTTP handler (fallback)
// ---------------------------------------------------------------------------

// handler returns an [http.Handler] that serves the redirect store. It is
// registered at "/" by [App.Handler] and is only reached for requests that
// match no other route (Decision 24 — zero overhead on successful requests).
//
// Behaviour:
//   - Exact or prefix match with non-empty To → HTTP redirect at e.Code.
//   - Match with empty To (Gone) → 410 Gone.
//   - No match → 404 Not Found.
func (s *RedirectStore) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e, ok := s.Get(r.URL.Path)
		if !ok {
			WriteError(w, r, ErrNotFound)
			return
		}
		if e.To == "" {
			WriteError(w, r, ErrGone)
			return
		}
		dest := e.To
		if e.IsPrefix {
			dest = e.To + strings.TrimPrefix(r.URL.Path, e.From)
		}
		http.Redirect(w, r, dest, int(e.Code))
	})
}
