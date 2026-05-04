package forge

import (
	"encoding/xml"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
)

// — FeedConfig and options ——————————————————————————————————————————————————

// FeedConfig configures the RSS 2.0 feed for a content module.
// Pass it to [Feed] to enable feed generation for the module.
//
// All fields are optional. Title defaults to the capitalised module prefix
// (e.g. "/posts" → "Posts"). Language defaults to "en".
type FeedConfig struct {
	// Title is the channel title shown in feed readers.
	// Defaults to the capitalised prefix (e.g. "Posts").
	Title string

	// Description is the channel description.
	// Defaults to the site hostname when empty.
	Description string

	// Language is the BCP 47 language code for the feed.
	// Defaults to "en".
	Language string
}

// feedOption carries the FeedConfig for a module. Implements [Option].
type feedOption struct{ cfg FeedConfig }

func (feedOption) isOption() {}

// Feed returns an Option that enables RSS 2.0 feed generation for the module.
// The feed is served at /{prefix}/feed.xml and regenerated on every publish event.
// An aggregate feed at /feed.xml merges all Published items from every
// Feed-enabled module, sorted by publish date descending.
//
//	app.Content(&Post{},
//	    forge.At("/posts"),
//	    forge.Feed(forge.FeedConfig{Title: "Blog", Description: "Latest posts"}),
//	)
func Feed(cfg FeedConfig) Option { return feedOption{cfg: cfg} }

// feedDisabledOption is an explicit opt-out marker. Implements [Option].
type feedDisabledOption struct{}

func (feedDisabledOption) isOption() {}

// DisableFeed returns an Option that explicitly opts a module out of RSS feed
// generation. This is a defensive marker for modules where a feed endpoint
// would be inappropriate (e.g. admin-only or API-only modules).
func DisableFeed() Option { return feedDisabledOption{} }

// — RSS 2.0 XML types ————————————————————————————————————————————————————

// rssGUID represents a <guid> element with an isPermaLink attribute.
type rssGUID struct {
	IsPermaLink string `xml:"isPermaLink,attr"`
	Value       string `xml:",chardata"`
}

// rssEnclosure represents an <enclosure> element for media attachments.
type rssEnclosure struct {
	URL    string `xml:"url,attr"`
	Length string `xml:"length,attr"`
	Type   string `xml:"type,attr"`
}

// rssItem represents a single <item> in an RSS 2.0 channel.
// pubTime is unexported and ignored by encoding/xml; it is used for sorting
// in IndexHandler.
type rssItem struct {
	Title       string        `xml:"title"`
	Link        string        `xml:"link"`
	Description string        `xml:"description"`
	PubDate     string        `xml:"pubDate"`
	GUID        rssGUID       `xml:"guid"`
	Enclosure   *rssEnclosure `xml:"enclosure"`
	Author      string        `xml:"author,omitempty"`
	Categories  []string      `xml:"category"`
	pubTime     time.Time     // sort key; ignored by encoding/xml
}

// rssChannel represents the <channel> element in an RSS 2.0 document.
type rssChannel struct {
	Title         string    `xml:"title"`
	Link          string    `xml:"link"`
	Description   string    `xml:"description"`
	Language      string    `xml:"language,omitempty"`
	LastBuildDate string    `xml:"lastBuildDate"`
	Items         []rssItem `xml:"item"`
}

// rssRoot is the top-level <rss> document element.
type rssRoot struct {
	XMLName xml.Name   `xml:"rss"`
	Version string     `xml:"version,attr"`
	Channel rssChannel `xml:"channel"`
}

// — Helpers ——————————————————————————————————————————————————————————————

// buildRSSItem constructs an rssItem from Head, Node, and a resolved canonical URL.
//
// canonical must be the absolute URL for this item (used for <link> and <guid>).
// An <enclosure> element is added when head.Image.URL is non-empty.
// Categories are populated from head.Tags.
func buildRSSItem(head Head, n Node, canonical string) rssItem {
	item := rssItem{
		Title:       head.Title,
		Link:        canonical,
		Description: head.Description,
		PubDate:     n.PublishedAt.Format(time.RFC1123Z),
		GUID:        rssGUID{IsPermaLink: "true", Value: canonical},
		Author:      head.Author,
		Categories:  head.Tags,
		pubTime:     n.PublishedAt,
	}
	if head.Image.URL != "" {
		item.Enclosure = &rssEnclosure{
			URL:    head.Image.URL,
			Length: "0",
			Type:   guessMIMEType(head.Image.URL),
		}
	}
	return item
}

// guessMIMEType returns a best-effort image MIME type from the URL's file
// extension. Returns "image/jpeg" for unrecognised extensions.
func guessMIMEType(rawURL string) string {
	lower := strings.ToLower(rawURL)
	// Strip query string for extension matching.
	if i := strings.IndexByte(lower, '?'); i >= 0 {
		lower = lower[:i]
	}
	switch {
	case strings.HasSuffix(lower, ".png"):
		return "image/png"
	case strings.HasSuffix(lower, ".gif"):
		return "image/gif"
	case strings.HasSuffix(lower, ".webp"):
		return "image/webp"
	case strings.HasSuffix(lower, ".svg"):
		return "image/svg+xml"
	default:
		return "image/jpeg"
	}
}

// capitalisePrefixTitle converts a URL prefix (e.g. "/posts") to a
// human-readable title (e.g. "Posts") using ASCII-only operations.
// No external dependencies required.
func capitalisePrefixTitle(prefix string) string {
	s := strings.TrimLeft(prefix, "/")
	if s == "" {
		return prefix
	}
	b := []byte(s)
	if b[0] >= 'a' && b[0] <= 'z' {
		b[0] -= 32
	}
	return string(b)
}

// writeRSSFeed marshals feed to w as RSS 2.0 XML with the correct
// Content-Type header. Writes a 500 response on marshal failure.
func writeRSSFeed(w http.ResponseWriter, feed rssRoot) {
	data, err := xml.MarshalIndent(feed, "", "  ")
	if err != nil {
		http.Error(w, "feed unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
	w.Write([]byte(xml.Header))
	w.Write(data)
}

// — FeedStore ————————————————————————————————————————————————————————————

// FeedStore holds pre-built RSS item fragments from all Feed-enabled content
// modules. It is shared across modules via App.Content and provides per-module
// and aggregate /feed.xml HTTP handlers.
//
// All public methods are safe for concurrent use.
type FeedStore struct {
	mu        sync.RWMutex
	siteName  string
	baseURL   string
	fragments map[string][]rssItem  // keyed by module prefix
	configs   map[string]FeedConfig // keyed by module prefix
}

// NewFeedStore constructs a [FeedStore] for the given site hostname and base URL.
// Called by [App.Content] when the first Feed-enabled module is registered.
func NewFeedStore(siteName, baseURL string) *FeedStore {
	return &FeedStore{
		siteName:  siteName,
		baseURL:   baseURL,
		fragments: make(map[string][]rssItem),
		configs:   make(map[string]FeedConfig),
	}
}

// Set stores the RSS items and config for the given module prefix.
// Passing nil items registers the prefix without content (used at startup).
// Called by regenerateFeed on every publish event and by setFeedStore at startup.
func (s *FeedStore) Set(prefix string, cfg FeedConfig, items []rssItem) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fragments[prefix] = items
	s.configs[prefix] = cfg
}

// HasFeeds reports whether at least one module has registered a feed fragment.
// Used by App.Handler to decide whether to mount GET /feed.xml.
func (s *FeedStore) HasFeeds() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.fragments) > 0
}

// ModuleHandler returns an http.Handler that serves the RSS 2.0 feed for the
// given module prefix at /{prefix}/feed.xml.
//
// The channel title comes from FeedConfig.Title (falling back to the
// capitalised prefix). Language defaults to "en".
func (s *FeedStore) ModuleHandler(prefix string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.RLock()
		items := append([]rssItem(nil), s.fragments[prefix]...)
		cfg := s.configs[prefix]
		s.mu.RUnlock()

		title := cfg.Title
		if title == "" {
			title = capitalisePrefixTitle(prefix)
		}
		lang := cfg.Language
		if lang == "" {
			lang = "en"
		}
		desc := cfg.Description
		if desc == "" {
			desc = s.siteName
		}

		writeRSSFeed(w, rssRoot{
			Version: "2.0",
			Channel: rssChannel{
				Title:         title,
				Link:          strings.TrimRight(s.baseURL, "/") + prefix,
				Description:   desc,
				Language:      lang,
				LastBuildDate: time.Now().UTC().Format(time.RFC1123Z),
				Items:         items,
			},
		})
	})
}

// IndexHandler returns an http.Handler that serves a merged RSS 2.0 feed of
// all Published items from every Feed-enabled module, sorted by pubDate
// descending, at /feed.xml.
func (s *FeedStore) IndexHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.RLock()
		var all []rssItem
		for _, items := range s.fragments {
			all = append(all, items...)
		}
		s.mu.RUnlock()

		slices.SortFunc(all, func(a, b rssItem) int {
			return b.pubTime.Compare(a.pubTime)
		})

		writeRSSFeed(w, rssRoot{
			Version: "2.0",
			Channel: rssChannel{
				Title:         s.siteName,
				Link:          strings.TrimRight(s.baseURL, "/"),
				Description:   s.siteName,
				Language:      "en",
				LastBuildDate: time.Now().UTC().Format(time.RFC1123Z),
				Items:         all,
			},
		})
	})
}
