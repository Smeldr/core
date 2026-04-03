package forge

import (
	"html/template"
	"strings"
	"time"
	"unicode/utf8"
)

// — Image —————————————————————————————————————————————————————————————————

// Image is a typed image reference. Width and Height are required for optimal
// Open Graph rendering and Twitter Card display. The zero value (empty URL)
// renders no image tags — safe to leave unset.
type Image struct {
	URL    string // absolute or root-relative
	Alt    string // accessibility and SEO description
	Width  int    // pixels; required for og:image:width
	Height int    // pixels; required for og:image:height
}

// — Alternate —————————————————————————————————————————————————————————————

// Alternate is an hreflang entry for internationalised pages.
// Reserved for v2 — Forge always generates an empty Alternates slice in v1.
type Alternate struct {
	Locale string // BCP 47 language tag, e.g. "en-GB"
	URL    string // absolute URL for this locale
}

// — Breadcrumb ————————————————————————————————————————————————————————————

// Breadcrumb is a single step in a breadcrumb trail. Build slices using
// the Crumb constructor and the Crumbs helper.
type Breadcrumb struct {
	Label string // human-readable label
	URL   string // root-relative or absolute URL
}

// Crumb returns a single Breadcrumb entry.
// Use with Crumbs to build Head.Breadcrumbs:
//
//	forge.Crumbs(
//	    forge.Crumb("Home",  "/"),
//	    forge.Crumb("Posts", "/posts"),
//	    forge.Crumb(p.Title, "/posts/"+p.Slug),
//	)
func Crumb(label, url string) Breadcrumb { return Breadcrumb{Label: label, URL: url} }

// Crumbs collects Breadcrumb entries for use in Head.Breadcrumbs.
func Crumbs(crumbs ...Breadcrumb) []Breadcrumb { return crumbs }

// — Rich-result type constants —————————————————————————————————————————————

// Rich result type constants for Head.Type. Each maps to a schema.org type
// used to generate JSON-LD structured data (see schema.go).
const (
	Article      = "Article"      // blog posts and news articles
	Product      = "Product"      // e-commerce product pages
	FAQPage      = "FAQPage"      // frequently asked questions
	HowTo        = "HowTo"        // step-by-step guides
	Event        = "Event"        // events with dates and locations
	Recipe       = "Recipe"       // recipes with ingredients and steps
	Review       = "Review"       // reviews with star ratings
	Organization = "Organization" // company or about pages
)

// — Head ——————————————————————————————————————————————————————————————————

// TwitterCardType is the value of the twitter:card meta property.
// Use the predefined constants [Summary], [SummaryLargeImage], [AppCard], [PlayerCard].
type TwitterCardType string

const (
	Summary           TwitterCardType = "summary"             // small card with title and description
	SummaryLargeImage TwitterCardType = "summary_large_image" // large image above the title
	AppCard           TwitterCardType = "app"                 // deep-link to a mobile app
	PlayerCard        TwitterCardType = "player"              // inline video or audio player
)

// TwitterMeta carries per-item Twitter Card overrides.
// Set on [Head.Social] to customise Twitter Card output for a specific content item.
type TwitterMeta struct {
	Card    TwitterCardType // overrides the default card type; empty uses a sensible default
	Creator string          // @handle of the content author; populates twitter:creator
}

// SocialOverrides carries per-item social sharing overrides.
// Set on [Head.Social] to customise Open Graph and Twitter Card output.
type SocialOverrides struct {
	Twitter TwitterMeta // Twitter Card overrides for this item
}

// Head carries all SEO and social metadata for a content page.
// Define it on your content type via the Headable interface.
// Forge uses the Head to populate HTML <head> tags, JSON-LD structured data,
// sitemaps, RSS feeds, and AI endpoints.
//
// All fields are optional: the zero value is safe and produces a minimal page header.
type Head struct {
	Title       string          // page title; used in <title>, og:title, and JSON-LD
	Description string          // meta description; recommended max 160 characters
	Author      string          // author name; used in <meta name="author"> and JSON-LD
	Published   time.Time       // publication date; zero value omits date tags
	Modified    time.Time       // last-modified date; zero value omits date tags
	Image       Image           // primary image; zero URL omits all image tags
	Type        string          // rich result type (Article, Product, etc.); empty omits JSON-LD
	Canonical   string          // canonical URL; empty omits the canonical tag
	Tags        []string        // content tags; used for article:tag meta and RSS categories
	Breadcrumbs []Breadcrumb    // breadcrumb trail; empty omits BreadcrumbList JSON-LD
	Alternates  []Alternate     // hreflang entries; always empty in v1
	Social      SocialOverrides // per-item social sharing overrides; zero value uses defaults
	NoIndex     bool            // true renders <meta name="robots" content="noindex">
}

// — Headable ——————————————————————————————————————————————————————————————

// Headable is implemented by content types that provide their own SEO metadata.
// Module[T] calls Head() automatically when building HTML responses, sitemaps,
// RSS feeds, and AI endpoints — no HeadFunc option required.
// HeadFunc takes priority over Headable when both are present.
type Headable interface{ Head() Head }

// — HeadAssets ————————————————————————————————————————————————————————————

// FaviconLink declares a single <link> element for a favicon or touch icon.
// Rel is required ("icon", "apple-touch-icon"). Type and Sizes are optional;
// omit them by leaving the fields empty.
type FaviconLink struct {
	Rel   string // "icon", "apple-touch-icon", etc.
	Type  string // MIME type, e.g. "image/png"; omitted when empty
	Sizes string // e.g. "32x32"; omitted when empty
	Href  string // URL to the icon file
}

// ScriptTag declares a single <script> element.
// Src loads an external script; Body inlines a JavaScript body when Src is empty.
// Body is typed as [html/template.JS] — convert a string literal with
// template.JS("…") to mark it as safe for emission inside a <script> block;
// never use this with user-supplied content.
// Async and Defer are only emitted for external scripts (Src non-empty).
type ScriptTag struct {
	Src   string      // external script URL; empty means inline
	Body  template.JS // inline JavaScript body; used when Src is empty
	Async bool        // adds async attribute (external scripts only)
	Defer bool        // adds defer attribute (external scripts only)
}

// HeadAssets is an [SEOOption] that injects static linked assets —
// preconnect hints, stylesheets, favicons, and scripts — into the
// forge:head partial on every page.
//
// Apply it via [App.SEO]:
//
//	app.SEO(&forge.HeadAssets{
//	    Preconnect:  []string{"https://fonts.googleapis.com"},
//	    Stylesheets: []string{"https://fonts.googleapis.com/css2?family=Inter&display=swap"},
//	    Favicons: []forge.FaviconLink{
//	        {Rel: "icon", Type: "image/png", Sizes: "32x32", Href: "/favicon-32.png"},
//	    },
//	    Scripts: []forge.ScriptTag{
//	        {Src: "/static/app.js", Defer: true},
//	    },
//	})
//
// Assets are emitted in order: preconnect → stylesheets → favicons → scripts.
type HeadAssets struct {
	Preconnect  []string      // <link rel="preconnect" href="…">
	Stylesheets []string      // <link rel="stylesheet" href="…">
	Favicons    []FaviconLink // <link rel="icon" …> / <link rel="apple-touch-icon" …>
	Scripts     []ScriptTag   // <script …>
}

func (h *HeadAssets) applySEO(s *seoState) { s.headAssets = h }

// — PageHead ——————————————————————————————————————————————————————————————

// PageHead holds the framework-owned fields that [forge:head] reads.
// Embed PageHead in any custom handler data struct to enable
// {{template "forge:head" .}} without using [TemplateData].
//
// Example:
//
//	type homeData struct {
//	    forge.PageHead
//	    Posts []*Post
//	}
//
//	func homeHandler(app *forge.App) http.HandlerFunc {
//	    tmpl := app.MustParseTemplate("templates/home.html")
//	    return func(w http.ResponseWriter, r *http.Request) {
//	        data := homeData{
//	            PageHead: forge.PageHead{Head: forge.Head{Title: "Home"}},
//	            Posts:    loadPosts(),
//	        }
//	        tmpl.ExecuteTemplate(w, "home.html", data)
//	    }
//	}
type PageHead struct {
	// Head carries SEO and social metadata for this page.
	Head Head

	// OGDefaults holds the app-level Open Graph and Twitter Card fallback values.
	OGDefaults *OGDefaults

	// AppSchema is a pre-rendered <script type="application/ld+json"> block
	// for app-level structured data.
	AppSchema template.HTML

	// HeadAssets holds the app-level static assets (preconnect, stylesheets,
	// favicons, scripts) set via [App.SEO] with [HeadAssets].
	HeadAssets *HeadAssets
}

// — HeadFunc option ———————————————————————————————————————————————————————

// headFuncOption stores a module-level head override function.
type headFuncOption[T any] struct{ fn func(Context, T) Head }

func (headFuncOption[T]) isOption() {}

// HeadFunc returns an Option that overrides a content type's Head method at
// the module level. The function receives the current request context and the
// content item; its return value takes precedence over the content type's own
// Head() implementation.
//
//	app.Content(&BlogPost{},
//	    forge.At("/posts"),
//	    forge.HeadFunc(func(ctx forge.Context, p *BlogPost) forge.Head {
//	        return forge.Head{Title: p.Title + " — " + ctx.SiteName()}
//	    }),
//	)
func HeadFunc[T any](fn func(Context, T) Head) Option { return headFuncOption[T]{fn: fn} }

// — Excerpt ———————————————————————————————————————————————————————————————

// Excerpt returns a plain-text summary truncated at the last word boundary
// within maxLen characters. A Unicode ellipsis ("…") is appended when the
// text is truncated. Use it to populate Head.Description.
//
//	forge.Excerpt(p.Body, 160)
func Excerpt(text string, maxLen int) string {
	text = strings.TrimSpace(text)
	if utf8.RuneCountInString(text) <= maxLen {
		return text
	}
	// Find the byte offset immediately after the maxLen-th rune.
	bytePos := 0
	for i := 0; i < maxLen; i++ {
		_, size := utf8.DecodeRuneInString(text[bytePos:])
		bytePos += size
	}
	truncated := text[:bytePos]
	// Only truncate further when we're mid-word (next byte is not a space).
	if bytePos < len(text) && text[bytePos] != ' ' {
		if idx := strings.LastIndex(truncated, " "); idx > 0 {
			truncated = truncated[:idx]
		}
	}
	return truncated + "…"
}

// — URL ————————————————————————————————————————————————————————————————————

// URL joins path segments into a root-relative URL. It collapses duplicate
// slashes, ensures a leading slash, and trims any trailing slash (the root "/"
// is preserved).
//
//	forge.URL("/posts/", p.Slug)  →  "/posts/my-slug"
func URL(parts ...string) string {
	joined := strings.Join(parts, "/")
	// Collapse consecutive slashes.
	for strings.Contains(joined, "//") {
		joined = strings.ReplaceAll(joined, "//", "/")
	}
	if !strings.HasPrefix(joined, "/") {
		joined = "/" + joined
	}
	joined = strings.TrimRight(joined, "/")
	if joined == "" {
		return "/"
	}
	return joined
}

// AbsURL joins a base URL and a path into an absolute URL.
// It trims any trailing slash from base before joining, so both of the
// following produce the same result:
//
//	forge.AbsURL("https://example.com",  "/posts/my-slug")  →  "https://example.com/posts/my-slug"
//	forge.AbsURL("https://example.com/", "/posts/my-slug")  →  "https://example.com/posts/my-slug"
//
// The path argument is passed through [URL] first, so duplicate slashes are
// collapsed and a leading slash is guaranteed.
// Use AbsURL in Head() implementations when setting Head.Canonical, Head.Image.URL,
// or any other field that requires an absolute URL.
//
//	func (p *Post) Head() forge.Head {
//	    return forge.Head{
//	        Canonical: forge.AbsURL(siteBaseURL, forge.URL("/posts", p.Slug)),
//	    }
//	}
func AbsURL(base, path string) string {
	base = strings.TrimRight(base, "/")
	return base + URL(path)
}
