package smeldr

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRobotsTxt_default(t *testing.T) {
	got := RobotsTxt(RobotsConfig{}, "")
	want := "User-agent: *\nDisallow:\n"
	if got != want {
		t.Errorf("RobotsTxt default\ngot:  %q\nwant: %q", got, want)
	}
}

func TestRobotsTxt_disallowPaths(t *testing.T) {
	cfg := RobotsConfig{Disallow: []string{"/admin", "/private"}}
	got := RobotsTxt(cfg, "")
	if !strings.Contains(got, "Disallow: /admin\n") {
		t.Errorf("missing Disallow: /admin in:\n%s", got)
	}
	if !strings.Contains(got, "Disallow: /private\n") {
		t.Errorf("missing Disallow: /private in:\n%s", got)
	}
	// Should not have bare "Disallow:\n" when paths are supplied.
	if strings.Contains(got, "Disallow:\n") {
		t.Errorf("unexpected bare Disallow: line in:\n%s", got)
	}
}

func TestRobotsTxt_askFirst(t *testing.T) {
	cfg := RobotsConfig{AIScraper: AskFirst}
	got := RobotsTxt(cfg, "")

	// User-agent: * block must be permissive (empty Disallow).
	if !strings.HasPrefix(got, "User-agent: *\nDisallow:\n") {
		t.Errorf("User-agent: * block not permissive; output:\n%s", got)
	}

	// Each AskFirst bot must appear as its own block.
	for _, bot := range askFirstBots {
		block := "User-agent: " + bot + "\nDisallow: /\n"
		if !strings.Contains(got, block) {
			t.Errorf("missing block for %q in:\n%s", bot, got)
		}
	}

	// Disallow-only bots (e.g. Bytespider) must NOT appear.
	for _, bot := range []string{"Bytespider", "omgili", "FacebookBot"} {
		if strings.Contains(got, "User-agent: "+bot) {
			t.Errorf("bot %q should not appear in AskFirst output:\n%s", bot, got)
		}
	}
}

func TestRobotsTxt_disallowAI(t *testing.T) {
	cfg := RobotsConfig{AIScraper: Disallow}
	got := RobotsTxt(cfg, "")

	for _, bot := range disallowBots {
		block := "User-agent: " + bot + "\nDisallow: /\n"
		if !strings.Contains(got, block) {
			t.Errorf("missing block for %q in:\n%s", bot, got)
		}
	}
}

func TestRobotsTxt_sitemapAppended(t *testing.T) {
	cfg := RobotsConfig{Sitemaps: true}
	got := RobotsTxt(cfg, "https://example.com")
	want := "\nSitemap: https://example.com/sitemap.xml\n"
	if !strings.Contains(got, want) {
		t.Errorf("sitemap line missing; got:\n%s", got)
	}
}

func TestRobotsTxt_sitemapOmittedWithoutBaseURL(t *testing.T) {
	cfg := RobotsConfig{Sitemaps: true}
	got := RobotsTxt(cfg, "")
	if strings.Contains(got, "Sitemap:") {
		t.Errorf("Sitemap: line should be absent when baseURL empty; got:\n%s", got)
	}
}

func TestRobotsTxtHandler_contentType(t *testing.T) {
	h := RobotsTxtHandler(RobotsConfig{}, "https://example.com")
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/robots.txt", nil)
	h(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	ct := w.Header().Get("Content-Type")
	if ct != "text/plain; charset=utf-8" {
		t.Errorf("Content-Type = %q, want %q", ct, "text/plain; charset=utf-8")
	}
	cc := w.Header().Get("Cache-Control")
	if cc != "max-age=86400" {
		t.Errorf("Cache-Control = %q, want %q", cc, "max-age=86400")
	}
}

// TestApp_SEO_implementsOption is a compile-time assertion that *RobotsConfig
// satisfies SEOOption.
func TestApp_SEO_implementsOption(t *testing.T) {
	var _ SEOOption = (*RobotsConfig)(nil)
}
