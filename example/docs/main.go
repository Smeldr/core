// Package main is a self-contained Forge documentation site — an example
// application demonstrating Forge's AI-first content features:
//
//   - AIDoc per-item endpoints: /docs/{slug}/aidoc (token-efficient format)
//   - /llms.txt compact content index for AI assistants
//   - /llms-full.txt full markdown corpus (opt-in, via Markdownable)
//   - AISummary() short description used in llms.txt and AIDoc summary: field
//   - Breadcrumb navigation (forge.Crumbs) for JSON-LD BreadcrumbList
//   - AI-crawler policy: AskFirst (ask before training on content)
//
// Run with:
//
//	cd example/docs && go run .
//
// Then visit http://localhost:8081/docs
// AI endpoints:
//
//	http://localhost:8081/llms.txt
//	http://localhost:8081/llms-full.txt
//	http://localhost:8081/docs/getting-started/aidoc
package main

import (
	"context"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"time"

	"forge-cms.dev/forge"
)

// Doc is the content type for a Forge documentation page.
// Embedding forge.Node provides ID, Slug, Status, and timestamp fields.
type Doc struct {
	forge.Node

	Title   string `forge:"required,min=3,max=120"`
	Body    string `forge:"required,min=10"`
	Section string `forge:"required"`
}

// Head implements forge.Headable.
//
// Forge: breadcrumbs are emitted as a JSON-LD BreadcrumbList in the page
// <head>, helping search engines understand the documentation hierarchy.
// They also render as navigation links in the HTML template.
func (d *Doc) Head() forge.Head {
	return forge.Head{
		Title:       d.Title + " — Forge Docs",
		Description: d.AISummary(),
		Author:      "The Forge Team",
		Published:   d.PublishedAt,
		Type:        "Article",
		Breadcrumbs: forge.Crumbs(
			forge.Crumb("Forge Docs", "/docs"),
			forge.Crumb(d.Section, "/docs"),
			forge.Crumb(d.Title, "/docs/"+d.Slug),
		),
	}
}

// Markdown implements forge.Markdownable.
//
// Forge: when AIIndex(LLMsTxtFull) is set, Forge concatenates every
// Published item's Markdown() output into /llms-full.txt. This lets AI
// assistants fetch the entire documentation corpus in a single request —
// useful for RAG pipelines, code assistants, and offline AI tooling.
func (d *Doc) Markdown() string {
	return fmt.Sprintf("# %s\n\n> Section: %s\n\n%s\n", d.Title, d.Section, d.Body)
}

// AISummary implements forge.AIDocSummary.
//
// Forge: the summary: field in AIDoc output comes from AISummary() when the
// content type implements this interface. It also populates the description
// entry in /llms.txt compact format:
//
//   - [Getting Started](https://example.com/docs/getting-started): Summary here
func (d *Doc) AISummary() string {
	return forge.Excerpt(d.Body, 160)
}

func main() {
	repo := forge.NewMemoryRepo[*Doc]()
	seed(repo)

	m := forge.NewModule((*Doc)(nil),
		forge.At("/docs"),
		forge.Repo(repo),

		// Forge: AIIndex(LLMsTxt, LLMsTxtFull, AIDoc) enables three AI endpoints:
		//   /llms.txt          — compact content index (llmstxt.org format)
		//   /llms-full.txt     — full markdown corpus (requires Markdownable)
		//   /docs/{slug}/aidoc — per-item token-efficient text (requires Markdownable)
		// Each endpoint is served automatically; no route registration needed.
		forge.AIIndex(forge.LLMsTxt, forge.LLMsTxtFull, forge.AIDoc),

		// Forge: SitemapConfig{} opts this module into /docs/sitemap.xml and
		// contributes an entry to the /sitemap.xml aggregate index.
		forge.SitemapConfig{},

		// Forge: Templates("templates") parses templates/list.html and
		// templates/show.html at startup. Run() fails fast if either is missing.
		forge.Templates("templates"),
	)

	app := forge.New(forge.MustConfig(forge.Config{
		BaseURL: "http://localhost:8081",
		// Forge: Secret is required for HMAC signing even when no auth is used,
		// because CookieSession middleware (if added later) depends on it.
		Secret: []byte("change-this-secret-in-production"),
	}))

	app.Content(m)

	// Forge: RobotsConfig{AIScraper: AskFirst} emits the following in robots.txt:
	//
	//   User-agent: GPTBot
	//   Disallow: /
	//
	//   User-agent: Claude-Web
	//   Disallow: /
	//
	// ...for all known AI training crawlers. AskFirst signals that the content
	// owner wants to be consulted before their content is used for training,
	// while still allowing AI assistants to *read* via /llms.txt and /llms-full.txt.
	app.SEO(&forge.RobotsConfig{
		AIScraper: forge.AskFirst,
		Sitemaps:  true,
	})

	// Welcome page — backed by templates/index.html.
	indexTpl := template.Must(template.ParseFiles("templates/index.html"))
	app.Handle("GET /", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := indexTpl.ExecuteTemplate(w, "index.html", nil); err != nil {
			forge.WriteError(w, r, forge.ErrInternal)
		}
	}))

	log.Println("Forge Docs — http://localhost:8081")
	log.Println("  Home:      http://localhost:8081/")
	log.Println("  Docs:      http://localhost:8081/docs")
	log.Println("  llms.txt:  http://localhost:8081/llms.txt")
	log.Println("  llms-full: http://localhost:8081/llms-full.txt")
	log.Println("  aidoc:     http://localhost:8081/docs/getting-started/aidoc  (example)")
	log.Println("  robots:    http://localhost:8081/robots.txt")
	if err := app.Run(":8081"); err != nil {
		log.Fatal(err)
	}
}

// seed inserts 6 Published documentation pages into repo across 2 sections.
// In a production app these would come from a SQL database via forge.SQLRepo[T].
func seed(repo forge.Repository[*Doc]) {
	now := time.Now().UTC()

	type docSpec struct {
		slug    string
		title   string
		section string
		body    string
		pubDays int // days ago
	}

	docs := []docSpec{
		{
			slug:    "getting-started",
			title:   "Getting Started",
			section: "Guides",
			body: `Install Forge by running go get forge-cms.dev/forge. Forge has zero
third-party dependencies, so nothing beyond the standard module tools is needed.

Create a Config with your site's base URL and a signing secret, then call
forge.New to obtain an App. Register content modules with app.Content, add
global middleware with app.Use, and start the server with app.Run.

**Minimal example:**

` + "```" + `go
app := forge.New(forge.MustConfig(forge.Config{
    BaseURL: "https://example.com",
    Secret:  []byte("...32 random bytes..."),
}))
app.Content(posts)
app.Run(":8080")
` + "```" + `

Forge derives table names, URL prefixes, and LLMs entries automatically from
your content types. The default behaviour is correct for most applications.`,
			pubDays: 90,
		},
		{
			slug:    "deploying-forge",
			title:   "Deploying Forge",
			section: "Guides",
			body: `Forge compiles to a single static binary with no runtime dependencies.
Deploy it like any Go application: copy the binary and any template files to
your server and run it behind a reverse proxy such as Caddy or nginx.

**PostgreSQL:** use the forge-pgx companion module to connect a pgx pool:

` + "```" + `go
pool, _ := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
cfg := forge.Config{DB: forgepgx.Wrap(pool), ...}
` + "```" + `

**HTTPS redirect:** set Config.HTTPS to true and Forge will automatically
redirect plain HTTP requests to HTTPS. The check honours the X-Forwarded-Proto
header so it works correctly behind a TLS-terminating reverse proxy with no
additional configuration.

**Graceful shutdown:** app.Run blocks until SIGINT or SIGTERM, then drains
active connections with a five-second deadline before returning. No signal
handling code required in your application.`,
			pubDays: 85,
		},
		{
			slug:    "modules",
			title:   "Modules and Content Types",
			section: "Reference",
			body: `A Module connects a Go struct to a set of HTTP routes, a repository, and a
collection of optional features. Define your content type, embed forge.Node,
add struct tags for validation, then call forge.NewModule to wire everything
together.

**Minimal module:**

` + "```" + `go
type Post struct {
    forge.Node
    Title string ` + "`forge:\"required,min=3\"`" + `
    Body  string ` + "`forge:\"required\"`" + `
}

m := forge.NewModule[*Post](&Post{},
    forge.At("/posts"),
    forge.Repo(forge.NewMemoryRepo[*Post]()),
)
` + "```" + `

Forge registers the following routes automatically: GET /posts (list),
GET /posts/{slug} (show), POST /posts (create), PUT /posts/{slug} (update),
DELETE /posts/{slug} (delete). Role checks, caching, middleware, sitemaps,
feeds, and AI indexing are all opt-in via additional options.

All routes serve content negotiation: send Accept: application/json for JSON,
Accept: text/html for HTML (requires Templates), Accept: text/markdown for
markdown (requires Markdownable).`,
			pubDays: 80,
		},
		{
			slug:    "content-lifecycle",
			title:   "Content Lifecycle",
			section: "Reference",
			body: `Every Forge content item moves through four lifecycle states encoded in
forge.Status: Draft, Scheduled, Published, Archived. The state is stored in
the Node.Status field and enforced by Forge on every public endpoint.

**Draft:** stored but invisible. GET /posts/{slug} returns 404. Excluded from
sitemaps, feeds, and AI indexes.

**Scheduled:** has a ScheduledAt timestamp in the future. Still invisible
publicly. The in-process scheduler transitions Scheduled items to Published
when their ScheduledAt time passes.

**Published:** publicly visible. Included in list responses, sitemaps, feeds,
and AI indexes. Node.PublishedAt is set on the first transition to Published.

**Archived:** preserved in storage but excluded from public list responses and
indexes. The item's URL continues to resolve (GET /posts/{slug} returns 200)
so existing links remain valid, but it is hidden from discovery paths.

Transitions are enforced at the framework level. Application code sets
Status; Forge enforces the rules. The AfterPublish signal fires on every
Draft→Published or Scheduled→Published transition.`,
			pubDays: 75,
		},
		{
			slug:    "seo-and-sitemaps",
			title:   "SEO and Sitemaps",
			section: "Reference",
			body: `Forge generates sitemaps, robots.txt, and HTML meta tags automatically when
you configure the relevant options. No separate SEO plugin required.

**Sitemaps:** pass SitemapConfig{} as a module option. Forge registers a
per-module fragment at /{prefix}/sitemap.xml and an aggregate index at
/sitemap.xml. Sitemaps regenerate on every publish event.

**robots.txt:** call app.SEO with a RobotsConfig. Set Sitemaps: true to
append the sitemap URL automatically. Set AIScraper to AskFirst or Disallow to
control AI training crawler access.

**HTML meta tags:** implement forge.Headable on your content type and return a
forge.Head struct. Forge renders <title>, <meta name="description">,
canonical, Open Graph, Twitter Card, and JSON-LD tags via the
{{template "forge:head" .Head}} partial. BreadcrumbList JSON-LD is generated
automatically when Breadcrumbs is non-empty.`,
			pubDays: 70,
		},
		{
			slug:    "ai-indexing",
			title:   "AI Indexing with llms.txt and AIDoc",
			section: "Reference",
			body: `Forge implements the llms.txt standard (llmstxt.org) natively. Pass
AIIndex(LLMsTxt) as a module option and Forge maintains a compact content index
at /llms.txt that AI assistants can fetch to understand your site's structure.

**Compact index (/llms.txt):** one line per Published item in the format
` + "`" + `- [Title](URL): Summary` + "`" + `. The summary comes from AIDocSummary.AISummary() if
the content type implements it, otherwise from the first 160 characters of the
plain-text body.

**Full corpus (/llms-full.txt):** pass AIIndex(LLMsTxtFull). Forge
concatenates the Markdownable.Markdown() output of every Published item.
Useful for RAG pipelines and AI assistants that want full context.

**Per-item AIDoc (/docs/{slug}/aidoc):** pass AIIndex(AIDoc). Each Published
item gets a machine-readable endpoint in token-efficient plain text format:
title, URL, summary, and full markdown body. AI models can fetch a single item
without parsing HTML.

The AskFirst AI-crawler policy (RobotsConfig{AIScraper: AskFirst}) signals
that training use requires consent, while still allowing legitimate AI reading
via the above endpoints.`,
			pubDays: 65,
		},
	}

	ctx := context.Background()
	now = now.UTC()
	for _, spec := range docs {
		pub := now.Add(-time.Duration(spec.pubDays) * 24 * time.Hour)
		node := forge.Node{
			ID:          forge.NewID(),
			Slug:        spec.slug,
			Status:      forge.Published,
			PublishedAt: pub,
		}
		doc := &Doc{Node: node, Title: spec.title, Body: spec.body, Section: spec.section}
		if err := repo.Save(ctx, doc); err != nil {
			log.Fatalf("seed %q: %v", spec.slug, err)
		}
	}
}
