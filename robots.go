package smeldr

import (
	"fmt"
	"net/http"
	"strings"
)

// CrawlerPolicy controls how AI web crawlers are treated in the generated
// robots.txt. The zero value is [Allow].
type CrawlerPolicy string

const (
	// Allow permits all crawlers, including AI training scrapers.
	// This is the zero-value default.
	Allow CrawlerPolicy = "allow"

	// Disallow blocks all known AI training crawlers by adding individual
	// User-agent / Disallow: / entries for each identified bot.
	Disallow CrawlerPolicy = "disallow"

	// AskFirst blocks known AI training crawlers while permitting AI
	// assistants that respect the robots.txt contract. Recommended for
	// sites that wish to be indexed by AI search but not scraped for
	// training.
	AskFirst CrawlerPolicy = "ask-first"
)

// RobotsConfig configures the auto-generated robots.txt. Pass a pointer to
// [App.SEO] to register the /robots.txt endpoint:
//
//	app.SEO(&smeldr.RobotsConfig{
//	    AIScraper: smeldr.AskFirst,
//	    Sitemaps:  true,
//	})
type RobotsConfig struct {
	// Disallow lists URL paths to block for all crawlers (e.g. "/admin").
	Disallow []string

	// Sitemaps appends a Sitemap directive pointing to <baseURL>/sitemap.xml
	// when true. Requires a non-empty baseURL on [App].
	Sitemaps bool

	// AIScraper sets the AI crawler policy. Defaults to [Allow] when zero.
	AIScraper CrawlerPolicy
}

// applySEO stores c in the app-level SEO state. Satisfies [SEOOption].
func (c *RobotsConfig) applySEO(s *seoState) { s.robots = c }

// askFirstBots is the list of known AI training crawlers blocked by [AskFirst].
var askFirstBots = []string{
	"GPTBot",
	"CCBot",
	"anthropic-ai",
	"Claude-Web",
	"PerplexityBot",
}

// disallowBots is the extended list of known AI training crawlers blocked by
// [Disallow]. Superset of askFirstBots.
var disallowBots = []string{
	"GPTBot",
	"CCBot",
	"anthropic-ai",
	"Claude-Web",
	"PerplexityBot",
	"Bytespider",
	"ImagesiftBot",
	"omgili",
	"omgilibot",
	"FacebookBot",
}

// RobotsTxt generates a well-formed robots.txt string from cfg.
//
// The output always begins with a User-agent: * block. If cfg.Disallow
// contains paths, each becomes a Disallow directive; otherwise an empty
// Disallow line is emitted (allow all).
//
// When cfg.AIScraper is [AskFirst], individual User-agent / Disallow: /
// blocks are appended for each known AI training crawler, leaving the
// User-agent: * block permissive. When cfg.AIScraper is [Disallow], the
// same is done for an extended crawler list.
//
// When cfg.Sitemaps is true and baseURL is non-empty, a Sitemap directive
// is appended at the end pointing to <baseURL>/sitemap.xml.
func RobotsTxt(cfg RobotsConfig, baseURL string) string {
	var b strings.Builder

	// User-agent: * block.
	b.WriteString("User-agent: *\n")
	if len(cfg.Disallow) == 0 {
		b.WriteString("Disallow:\n")
	} else {
		for _, p := range cfg.Disallow {
			fmt.Fprintf(&b, "Disallow: %s\n", p)
		}
	}

	// Per-bot blocks for AI crawler policy.
	var bots []string
	switch cfg.AIScraper {
	case AskFirst:
		bots = askFirstBots
	case Disallow:
		bots = disallowBots
	}
	for _, bot := range bots {
		b.WriteByte('\n')
		fmt.Fprintf(&b, "User-agent: %s\nDisallow: /\n", bot)
	}

	// Sitemap directive.
	if cfg.Sitemaps && baseURL != "" {
		fmt.Fprintf(&b, "\nSitemap: %s/sitemap.xml\n", baseURL)
	}

	return b.String()
}

// RobotsTxtHandler returns an [http.HandlerFunc] that serves the robots.txt
// content generated from cfg.
//
// The content is generated once at construction time — not per request — so
// the handler is safe to share across goroutines and incurs no per-request
// allocation.
//
// Responses carry Content-Type: text/plain; charset=utf-8 and
// Cache-Control: max-age=86400 (one day).
func RobotsTxtHandler(cfg RobotsConfig, baseURL string) http.HandlerFunc {
	body := RobotsTxt(cfg, baseURL)
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "max-age=86400")
		fmt.Fprint(w, body)
	}
}
