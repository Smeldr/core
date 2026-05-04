package forge

// sitemap.go provides event-driven XML sitemap generation for Forge modules.
// Each module with a [SitemapConfig] option owns a fragment sitemap
// (e.g. /posts/sitemap.xml). Forge merges all fragments into /sitemap.xml
// as a sitemap index. See Decision 9.

import (
	"encoding/xml"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
)

// — ChangeFreq ————————————————————————————————————————————————————————————

// ChangeFreq is the value of a sitemap <changefreq> element, indicating how
// frequently the page content is likely to change.
type ChangeFreq string

const (
	// Always signals the URL changes with every access. Use for live data.
	Always ChangeFreq = "always"

	// Hourly signals the URL is updated approximately once per hour.
	Hourly ChangeFreq = "hourly"

	// Daily signals the URL is updated approximately once per day.
	Daily ChangeFreq = "daily"

	// Weekly signals the URL is updated approximately once per week.
	// This is the default when [SitemapConfig.ChangeFreq] is empty.
	Weekly ChangeFreq = "weekly"

	// Monthly signals the URL is updated approximately once per month.
	Monthly ChangeFreq = "monthly"

	// Yearly signals the URL is updated approximately once per year.
	Yearly ChangeFreq = "yearly"

	// Never signals the URL is permanently archived and will not change.
	Never ChangeFreq = "never"
)

// — SitemapConfig —————————————————————————————————————————————————————————

// SitemapConfig configures the per-module sitemap fragment. Pass it to
// App.Content as an option alongside [At], [Cache], and similar options.
//
//	app.Content(posts, forge.SitemapConfig{ChangeFreq: forge.Weekly, Priority: 0.8})
//
// ChangeFreq defaults to [Weekly] when zero. Priority defaults to 0.5 when
// zero or negative.
type SitemapConfig struct {
	// ChangeFreq is the expected update frequency for URLs in this module.
	// Defaults to [Weekly] when empty.
	ChangeFreq ChangeFreq

	// Priority is the relative importance of URLs in this module, in the range
	// 0.0–1.0. Defaults to 0.5 when zero or negative.
	Priority float64
}

// isOption marks SitemapConfig as a [Module] option.
func (SitemapConfig) isOption() {}

// — SitemapPrioritiser and SitemapNode ————————————————————————————————————

// SitemapPrioritiser may be implemented by content types to provide a
// per-item priority override in the sitemap. When not implemented,
// [SitemapConfig.Priority] is used (defaulting to 0.5).
type SitemapPrioritiser interface {
	SitemapPriority() float64
}

// SitemapNode is the type constraint for [SitemapEntries]. It is satisfied by
// any pointer to a struct that embeds [Node] and implements [Headable].
// All Forge content types that embed Node satisfy this constraint automatically
// after Amendment A2.
type SitemapNode interface {
	Headable
	GetSlug() string
	GetPublishedAt() time.Time
	GetStatus() Status
}

// — SitemapEntry ——————————————————————————————————————————————————————————

// SitemapEntry is a single URL entry in a sitemap fragment. A zero LastMod is
// omitted from the XML output.
type SitemapEntry struct {
	// Loc is the canonical URL of the page.
	Loc string

	// LastMod is the date-time the content was last modified. It is formatted
	// as a date-only string (YYYY-MM-DD) in the output. Zero value is omitted.
	LastMod time.Time

	// ChangeFreq is the expected update frequency. Defaults to [Weekly].
	ChangeFreq ChangeFreq

	// Priority is the relative importance, 0.0–1.0. Defaults to 0.5.
	Priority float64
}

// — Internal XML types ————————————————————————————————————————————————————

const sitemapNS = "http://www.sitemaps.org/schemas/sitemap/0.9"

type xmlURLSet struct {
	XMLName xml.Name `xml:"urlset"`
	XMLNS   string   `xml:"xmlns,attr"`
	URLs    []xmlURL `xml:"url"`
}

type xmlURL struct {
	Loc        string  `xml:"loc"`
	LastMod    string  `xml:"lastmod,omitempty"`
	ChangeFreq string  `xml:"changefreq,omitempty"`
	Priority   float64 `xml:"priority,omitempty"`
}

type xmlSitemapIndex struct {
	XMLName  xml.Name        `xml:"sitemapindex"`
	XMLNS    string          `xml:"xmlns,attr"`
	Sitemaps []xmlSitemapRef `xml:"sitemap"`
}

type xmlSitemapRef struct {
	Loc     string `xml:"loc"`
	LastMod string `xml:"lastmod,omitempty"`
}

// — WriteSitemapFragment ——————————————————————————————————————————————————

// WriteSitemapFragment writes a complete XML sitemap fragment to w. It
// streams the document via [xml.NewEncoder] — the full document is never
// held in memory. Returns the first write or encode error.
//
// Entries with a zero [SitemapEntry.LastMod] omit the <lastmod> element.
// An empty entries slice produces a valid empty <urlset/>.
func WriteSitemapFragment(w io.Writer, entries []SitemapEntry) error {
	if _, err := io.WriteString(w, xml.Header); err != nil {
		return err
	}
	us := xmlURLSet{XMLNS: sitemapNS}
	for _, e := range entries {
		u := xmlURL{
			Loc:        e.Loc,
			ChangeFreq: string(e.ChangeFreq),
			Priority:   e.Priority,
		}
		if !e.LastMod.IsZero() {
			u.LastMod = e.LastMod.UTC().Format("2006-01-02")
		}
		us.URLs = append(us.URLs, u)
	}
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	if err := enc.Encode(us); err != nil {
		return err
	}
	return enc.Flush()
}

// — SitemapEntries ————————————————————————————————————————————————————————

// SitemapEntries builds a slice of [SitemapEntry] values from items, applying
// the rules in cfg. Only [Published] items are included.
//
// Loc is taken from [Head.Canonical]; if empty it falls back to
// strings.TrimRight(baseURL, "/") + "/" + item.GetSlug().
//
// ChangeFreq defaults to [Weekly] when cfg.ChangeFreq is empty.
// Priority is taken from [SitemapPrioritiser] if implemented, then from
// cfg.Priority if positive, otherwise defaults to 0.5.
func SitemapEntries[T SitemapNode](items []T, baseURL string, cfg SitemapConfig) []SitemapEntry {
	result := make([]SitemapEntry, 0, len(items))
	freq := cfg.ChangeFreq
	if freq == "" {
		freq = Weekly
	}
	base := strings.TrimRight(baseURL, "/")
	for _, item := range items {
		if item.GetStatus() != Published {
			continue
		}
		loc := item.Head().Canonical
		if loc == "" {
			loc = base + "/" + item.GetSlug()
		}
		priority := cfg.Priority
		if sp, ok := any(item).(SitemapPrioritiser); ok {
			priority = sp.SitemapPriority()
		} else if priority <= 0 {
			priority = 0.5
		}
		result = append(result, SitemapEntry{
			Loc:        loc,
			LastMod:    item.GetPublishedAt(),
			ChangeFreq: freq,
			Priority:   priority,
		})
	}
	return result
}

// — WriteSitemapIndex —————————————————————————————————————————————————————

// WriteSitemapIndex writes a sitemap index document to w. Each URL in
// fragmentURLs becomes one <sitemap> entry. lastMod is written as a
// date-only string and omitted when zero. An empty fragmentURLs slice
// produces a valid empty <sitemapindex/>.
func WriteSitemapIndex(w io.Writer, fragmentURLs []string, lastMod time.Time) error {
	if _, err := io.WriteString(w, xml.Header); err != nil {
		return err
	}
	idx := xmlSitemapIndex{XMLNS: sitemapNS}
	lastModStr := ""
	if !lastMod.IsZero() {
		lastModStr = lastMod.UTC().Format("2006-01-02")
	}
	for _, u := range fragmentURLs {
		idx.Sitemaps = append(idx.Sitemaps, xmlSitemapRef{Loc: u, LastMod: lastModStr})
	}
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	if err := enc.Encode(idx); err != nil {
		return err
	}
	return enc.Flush()
}

// — SitemapStore ——————————————————————————————————————————————————————————

// SitemapStore holds the latest generated sitemap fragments in memory. Forge
// populates it automatically via the debouncer on every publish/unpublish
// event. It is safe for concurrent use by multiple goroutines.
type SitemapStore struct {
	mu        sync.RWMutex
	fragments map[string][]byte
}

// NewSitemapStore returns an initialised, empty [SitemapStore].
func NewSitemapStore() *SitemapStore {
	return &SitemapStore{fragments: make(map[string][]byte)}
}

// Set stores a copy of data keyed by path (e.g. "/posts/sitemap.xml").
// Subsequent calls replace the previous value for the same path.
func (s *SitemapStore) Set(path string, data []byte) {
	cp := make([]byte, len(data))
	copy(cp, data)
	s.mu.Lock()
	s.fragments[path] = cp
	s.mu.Unlock()
}

// Get returns the stored bytes for path and whether the path exists.
func (s *SitemapStore) Get(path string) ([]byte, bool) {
	s.mu.RLock()
	v, ok := s.fragments[path]
	s.mu.RUnlock()
	return v, ok
}

// Paths returns a sorted slice of all stored fragment paths. Used by
// [SitemapStore.IndexHandler] to enumerate fragments when building the index.
func (s *SitemapStore) Paths() []string {
	s.mu.RLock()
	paths := make([]string, 0, len(s.fragments))
	for p := range s.fragments {
		paths = append(paths, p)
	}
	s.mu.RUnlock()
	slices.Sort(paths)
	return paths
}

// Handler returns an [http.Handler] that serves stored fragment bytes by
// request path. Responds with 404 when the path has no stored fragment.
// Content-Type is set to application/xml; charset=utf-8.
func (s *SitemapStore) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, ok := s.Get(r.URL.Path)
		if !ok {
			WriteError(w, r, ErrNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write(data) //nolint:errcheck
	})
}

// IndexHandler returns an [http.Handler] that generates the sitemap index
// on each request from all currently stored fragment paths. baseURL is
// prepended to each path to form the full fragment URL
// (e.g. "https://example.com/posts/sitemap.xml").
func (s *SitemapStore) IndexHandler(baseURL string) http.Handler {
	base := strings.TrimRight(baseURL, "/")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths := s.Paths()
		urls := make([]string, len(paths))
		for i, p := range paths {
			urls[i] = base + p
		}
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		if err := WriteSitemapIndex(w, urls, time.Time{}); err != nil {
			WriteError(w, r, ErrInternal)
		}
	})
}
