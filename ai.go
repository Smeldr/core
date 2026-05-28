package smeldr

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// — Markdownable (migrated from module.go via Amendment A11) ——————————————

// Markdownable is implemented by content types that render directly to Markdown.
// When T implements Markdownable, [Module] serves text/markdown responses without
// requiring smeldr.Templates to be configured. The Markdown body is also used in
// AIDoc output and /llms-full.txt corpus entries.
type Markdownable interface{ Markdown() string }

// — AIDocSummary ——————————————————————————————————————————————————————————

// AIDocSummary is implemented by content types that provide a concise,
// human-readable summary optimised for AI consumption. The summary is used in
// /llms.txt entries and the summary: field of AIDoc output.
//
// When a content type implements neither AIDocSummary nor Markdownable, Forge
// falls back to [Head].Description.
type AIDocSummary interface{ AISummary() string }

// — AIFeature —————————————————————————————————————————————————————————————

// AIFeature selects which AI indexing endpoints are enabled for a module.
// Pass one or more AIFeature constants to [AIIndex].
type AIFeature int

const (
	// LLMsTxt enables the /llms.txt compact content index for the module.
	// Only Published items appear. Regenerated on every publish event.
	LLMsTxt AIFeature = 1

	// LLMsTxtFull enables the /llms-full.txt full markdown corpus for the module.
	// Each Published item is rendered as a full document with a header.
	// Only Published items appear. Regenerated on every publish event.
	LLMsTxtFull AIFeature = 2

	// AIDoc enables per-item /{prefix}/{slug}.aidoc endpoints. Each endpoint
	// returns the item in token-efficient AIDoc format (text/plain).
	// Only Published items are served; non-Published items return 404.
	AIDoc AIFeature = 3
)

// aiIndexOption carries the [AIFeature] set for a module. Use [AIIndex] to
// create one.
type aiIndexOption struct{ features []AIFeature }

func (aiIndexOption) isOption() {}

// AIIndex returns an [Option] that enables AI indexing endpoints for a module.
// Pass one or more [AIFeature] constants to select which endpoints are registered.
//
//	app.Content(&BlogPost{},
//	    smeldr.At("/posts"),
//	    smeldr.AIIndex(smeldr.LLMsTxt, smeldr.LLMsTxtFull, smeldr.AIDoc),
//	)
func AIIndex(features ...AIFeature) Option { return aiIndexOption{features: features} }

// — WithoutID —————————————————————————————————————————————————————————————

// withoutIDOption suppresses the id: field from AIDoc output. Use [WithoutID]
// when publishing UUIDs to AI consumers is undesirable.
type withoutIDOption struct{}

func (withoutIDOption) isOption() {}

// WithoutID returns an [Option] that omits the id: line from AIDoc output.
// Apply alongside [AIIndex] when content UUIDs must not be exposed to AI consumers.
//
//	app.Content(&BlogPost{},
//	    smeldr.At("/posts"),
//	    smeldr.AIIndex(smeldr.AIDoc),
//	    smeldr.WithoutID(),
//	)
func WithoutID() Option { return withoutIDOption{} }

// — hasAIFeature ——————————————————————————————————————————————————————————

// hasAIFeature reports whether f is present in features.
func hasAIFeature(features []AIFeature, f AIFeature) bool {
	for _, v := range features {
		if v == f {
			return true
		}
	}
	return false
}

// — LLMsEntry —————————————————————————————————————————————————————————————

// LLMsEntry is a single compact entry in /llms.txt output.
// Fields map to the llmstxt.org compact format: - [Title](URL): Summary
type LLMsEntry struct {
	// Title is the content item's title. Required.
	Title string

	// URL is the canonical URL for this item. Required.
	URL string

	// Summary is a short plain-text description. Optional; omitted when empty.
	Summary string
}

// — LLMsTemplateData ——————————————————————————————————————————————————————

// LLMsTemplateData is the data value passed to custom llms.txt templates.
// Create templates/llms.txt in your template directory to override the
// built-in format:
//
//	# {{.SiteName}}
//
//	> {{.Description}}
//
//	## All Content
//	{{forge_llms_entries .}}
type LLMsTemplateData struct {
	// SiteName is the hostname of the site (e.g. "example.com").
	SiteName string

	// Description is a one-line site description. Empty by default;
	// set manually in custom templates.
	Description string

	// Entries contains all compact entries across all registered modules.
	Entries []LLMsEntry

	// GeneratedAt is the generation date in YYYY-MM-DD format.
	GeneratedAt string

	// ItemCount is the total number of Published items across all modules.
	ItemCount int
}

// — LLMsStore —————————————————————————————————————————————————————————————

// LLMsStore holds compact and full content fragments for /llms.txt and
// /llms-full.txt. Thread-safe. Analogous to [SitemapStore].
//
// Created by [App.Content] when the first module registers with [AIIndex].
// Passed to each module via setAIRegistry.
type LLMsStore struct {
	mu               sync.RWMutex
	siteName         string
	hasCompact       bool                   // true when at least one module uses LLMsTxt
	hasFull          bool                   // true when at least one module uses LLMsTxtFull
	compactFragments map[string][]LLMsEntry // keyed by module prefix
	fullFragments    map[string]string      // keyed by module prefix
}

// NewLLMsStore creates an [LLMsStore] for the given site name.
func NewLLMsStore(siteName string) *LLMsStore {
	return &LLMsStore{
		siteName:         siteName,
		compactFragments: make(map[string][]LLMsEntry),
		fullFragments:    make(map[string]string),
	}
}

// SetCompact stores compact entries for the given module prefix.
// Called by [Module].regenerateAI after every publish event.
func (s *LLMsStore) SetCompact(prefix string, entries []LLMsEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.compactFragments[prefix] = entries
}

// SetFull stores the full markdown corpus fragment for the given module prefix.
// Called by [Module].regenerateAI after every publish event.
func (s *LLMsStore) SetFull(prefix string, body string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fullFragments[prefix] = body
}

// registerCompact marks the store as having at least one module using LLMsTxt.
func (s *LLMsStore) registerCompact() {
	s.mu.Lock()
	s.hasCompact = true
	s.mu.Unlock()
}

// registerFull marks the store as having at least one module using LLMsTxtFull.
func (s *LLMsStore) registerFull() {
	s.mu.Lock()
	s.hasFull = true
	s.mu.Unlock()
}

// HasCompact reports whether any module registered with [LLMsTxt].
func (s *LLMsStore) HasCompact() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.hasCompact
}

// HasFull reports whether any module registered with [LLMsTxtFull].
func (s *LLMsStore) HasFull() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.hasFull
}

// allCompactEntries returns all compact entries across all registered modules.
func (s *LLMsStore) allCompactEntries() []LLMsEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var all []LLMsEntry
	for _, entries := range s.compactFragments {
		all = append(all, entries...)
	}
	return all
}

// CompactHandler returns an [http.Handler] that serves the /llms.txt endpoint.
// The built-in format follows the llmstxt.org convention: site name header
// followed by per-item entries as "- [Title](URL): Summary".
// Responses are gzip-compressed when the client sends Accept-Encoding: gzip
// and the body exceeds [gzipMinBytes] (Amendment A17).
func (s *LLMsStore) CompactHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entries := s.allCompactEntries()
		var buf strings.Builder
		fmt.Fprintf(&buf, "# %s\n\n", s.siteName)
		for _, e := range entries {
			if e.Summary != "" {
				fmt.Fprintf(&buf, "- [%s](%s): %s\n", e.Title, e.URL, e.Summary)
			} else {
				fmt.Fprintf(&buf, "- [%s](%s)\n", e.Title, e.URL)
			}
		}
		compressIfAccepted(w, r, []byte(buf.String()), "text/plain; charset=utf-8")
	})
}

// FullHandler returns an [http.Handler] that serves the /llms-full.txt endpoint.
// The corpus header identifies the site name, generation date, and item count.
// Each item is rendered as a full document separated by "---".
// Responses are gzip-compressed when the client sends Accept-Encoding: gzip
// and the body exceeds [gzipMinBytes] (Amendment A17).
func (s *LLMsStore) FullHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.RLock()
		fragments := make([]string, 0, len(s.fullFragments))
		for _, body := range s.fullFragments {
			fragments = append(fragments, body)
		}
		total := 0
		for _, entries := range s.compactFragments {
			total += len(entries)
		}
		s.mu.RUnlock()

		date := time.Now().Format("2006-01-02")
		var buf strings.Builder
		fmt.Fprintf(&buf, "# %s \u2014 Full Content Corpus\n", s.siteName)
		fmt.Fprintf(&buf, "> Generated by Forge on %s | Only published content | %d items\n\n", date, total)
		for _, body := range fragments {
			buf.WriteString(body)
		}
		compressIfAccepted(w, r, []byte(buf.String()), "text/plain; charset=utf-8")
	})
}

// — extractNode ———————————————————————————————————————————————————————————

// extractNode extracts the embedded [Node] from any content item.
// Returns a zero Node when the item does not embed smeldr.Node.
func extractNode(item any) Node {
	rv := elemValue(item)
	nf := rv.FieldByName("Node")
	if !nf.IsValid() {
		return Node{}
	}
	n, _ := nf.Interface().(Node)
	return n
}

// — gzip compression helper (Amendment A17) ————————————————————————————

// gzipMinBytes is the minimum body size below which gzip compression is skipped.
// Compressing small responses wastes CPU — the overhead outweighs the saving.
// 1024 bytes aligns with the industry consensus threshold used by NGINX, Cloudflare,
// Spring Boot, and Akamai for text/plain and text/markdown content.
const gzipMinBytes = 1024

// compressIfAccepted writes body to w, applying gzip compression when the
// client sends Accept-Encoding: gzip and len(body) >= [gzipMinBytes].
// Always sets Content-Type, Content-Length, and Vary: Accept-Encoding.
func compressIfAccepted(w http.ResponseWriter, r *http.Request, body []byte, contentType string) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Vary", "Accept-Encoding")
	if len(body) >= gzipMinBytes && strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		gz.Write(body) //nolint:errcheck
		gz.Close()     //nolint:errcheck
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Content-Length", strconv.Itoa(buf.Len()))
		w.WriteHeader(http.StatusOK)
		w.Write(buf.Bytes()) //nolint:errcheck
		return
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	w.Write(body) //nolint:errcheck
}

// — renderAIDoc ———————————————————————————————————————————————————————————

// renderAIDoc writes an AIDoc formatted response to w.
//
// The AIDoc format is designed for token efficiency:
//   - Only Published content is served (enforced by the caller)
//   - id: is omitted when withoutID is true
//   - Dates use YYYY-MM-DD — timezone-free, compact
//   - Body is Markdown when item implements [Markdownable], otherwise JSON
//
// Responses are gzip-compressed when the client sends Accept-Encoding: gzip
// and the body exceeds [gzipMinBytes] (Amendment A17).
//
// Summary priority: [AIDocSummary].AISummary() (when non-empty) → [Head].Description → [Excerpt]([Markdownable].Markdown(), 120).
func renderAIDoc(w http.ResponseWriter, r *http.Request, head Head, n Node, item any, withoutID bool) {
	// Compute body.
	var body string
	if md, ok := item.(Markdownable); ok {
		body = md.Markdown()
	} else {
		b, _ := json.Marshal(item)
		body = string(b)
	}

	// Compute summary — priority: AIDocSummary (non-empty) → Head.Description → Excerpt(Markdown, 120).
	summary := head.Description
	if as, ok := item.(AIDocSummary); ok {
		if s := as.AISummary(); s != "" {
			summary = s
		}
	} else if md, ok := item.(Markdownable); ok && summary == "" {
		summary = Excerpt(md.Markdown(), 120)
	}

	contentType := head.Type
	if contentType == "" {
		contentType = "article"
	}

	var buf strings.Builder
	buf.WriteString("+++aidoc+v1+++\n")
	fmt.Fprintf(&buf, "type:     %s\n", contentType)
	if !withoutID {
		fmt.Fprintf(&buf, "id:       %s\n", n.ID)
	}
	fmt.Fprintf(&buf, "slug:     %s\n", n.Slug)
	if head.Title != "" {
		fmt.Fprintf(&buf, "title:    %s\n", head.Title)
	}
	if head.Author != "" {
		fmt.Fprintf(&buf, "author:   %s\n", head.Author)
	}
	if !n.CreatedAt.IsZero() {
		fmt.Fprintf(&buf, "created:  %s\n", n.CreatedAt.Format("2006-01-02"))
	}
	if !n.UpdatedAt.IsZero() {
		fmt.Fprintf(&buf, "modified: %s\n", n.UpdatedAt.Format("2006-01-02"))
	}
	if len(head.Tags) > 0 {
		fmt.Fprintf(&buf, "tags:     [%s]\n", strings.Join(head.Tags, ", "))
	}
	if summary != "" {
		fmt.Fprintf(&buf, "summary:  %s\n", summary)
	}
	buf.WriteString("+++\n")
	buf.WriteString(body)

	compressIfAccepted(w, r, []byte(buf.String()), "text/plain; charset=utf-8")
}
