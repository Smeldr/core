// Package main is a self-contained Forge devlog — an example blog application
// that demonstrates the full v1.11.0 feature set:
//
//   - Content lifecycle (Published, Draft, Scheduled)
//   - HTML template rendering with forge:head
//   - Open Graph and Twitter Card social metadata
//   - RSS 2.0 feed at /posts/feed.xml and /feed.xml
//   - Sitemap at /sitemap.xml with automatic regeneration
//   - AI indexing at /llms.txt and /llms-full.txt
//   - Scheduler: automatic Scheduled→Published transition (check after 2 min)
//   - AfterPublish signal: log hook on every publish event
//
// Run with:
//
//	cd example/blog && go run .
//
// Then visit http://localhost:8080
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

// Post is the content type for a Forge devlog post.
// Embedding forge.Node provides ID, Slug, Status, PublishedAt, ScheduledAt,
// CreatedAt, and UpdatedAt — all automatically mapped to SQL columns via `db`
// struct tags when using forge.SQLRepo.
type Post struct {
	forge.Node

	Title string `forge:"required,min=3,max=120"`
	Body  string `forge:"required,min=10"`
	Tags  []string
}

// Head implements forge.Headable, which Forge calls when assembling HTML
// responses, sitemaps, and AI endpoints.
//
// Forge: returning a populated forge.Head enables the forge:head template
// partial to emit correct <title>, <meta description>, Open Graph, Twitter
// Card, and JSON-LD Article tags with zero additional code.
func (p *Post) Head() forge.Head {
	return forge.Head{
		Title:       p.Title + " — Forge Devlog",
		Description: forge.Excerpt(p.Body, 160),
		Author:      "The Forge Team",
		Published:   p.PublishedAt,
		Tags:        p.Tags,
		Type:        "Article",
	}
}

// Markdown implements forge.Markdownable, which powers /llms-full.txt and
// the Accept: text/markdown content-negotiation path.
//
// Forge: when AIIndex(LLMsTxtFull) is set, Forge calls Markdown() on each
// Published item and concatenates the results into /llms-full.txt so AI
// assistants can consume the entire content corpus in one request.
func (p *Post) Markdown() string {
	return fmt.Sprintf("# %s\n\n%s\n", p.Title, p.Body)
}

func main() {
	repo := forge.NewMemoryRepo[*Post]()
	seed(repo)

	// Forge: On[*Post](AfterPublish, ...) registers a signal handler that fires
	// every time a Scheduled post transitions to Published. The in-process
	// scheduler calls this automatically — no external job queue required.
	m := forge.NewModule((*Post)(nil),
		forge.At("/posts"),
		forge.Repo(repo),

		// Forge: SitemapConfig{} opts this module into /sitemap.xml.
		// No configuration needed for default behaviour (weekly, 0.5 priority).
		forge.SitemapConfig{},

		// Forge: Social(OpenGraph, TwitterCard) generates og: and twitter: meta
		// tags from the Head() return value on every HTML response.
		forge.Social(forge.OpenGraph, forge.TwitterCard),

		// Forge: Feed() enables GET /posts/feed.xml. The aggregate /feed.xml
		// is mounted automatically when any module registers a feed.
		forge.Feed(forge.FeedConfig{
			Title:       "Forge Devlog",
			Description: "Engineering notes and release announcements from the Forge team.",
		}),

		// Forge: AIIndex(LLMsTxt, LLMsTxtFull, AIDoc) registers published content in
		// /llms.txt (compact link list), /llms-full.txt (full markdown corpus), and
		// /posts/{slug}/aidoc (per-item token-efficient text for AI assistants).
		forge.AIIndex(forge.LLMsTxt, forge.LLMsTxtFull, forge.AIDoc),

		// Forge: Templates("templates") enables HTML rendering. Forge parses
		// templates/list.html (GET /posts) and templates/show.html (GET /posts/:slug)
		// at startup and fails fast if either file is missing or invalid.
		forge.Templates("templates"),

		// Forge: On fires this handler every time a post transitions to Published,
		// whether via the scheduler or a direct API call. One hook covers both paths.
		forge.On(forge.AfterPublish, func(_ forge.Context, p *Post) error {
			log.Printf("[blog] published: %q (slug: %s)", p.Title, p.Slug)
			return nil
		}),
	)

	app := forge.New(forge.MustConfig(forge.Config{
		BaseURL: "http://localhost:8080",
		// Forge: Secret is used for HMAC signing of session cookies and bearer
		// tokens. Replace with a 32+ byte cryptographically random value in
		// production (e.g. use crypto/rand to generate it at deploy time).
		Secret: []byte("change-this-secret-in-production"),
	}))

	app.Content(m)

	// Forge: SEO with Sitemaps: true appends /sitemap.xml to robots.txt so
	// crawlers discover it automatically. No manual robots.txt file required.
	app.SEO(&forge.RobotsConfig{Sitemaps: true})

	// Mount a welcome page at the root so http://localhost:8080 shows
	// an overview instead of a 404. The template is a plain html/template
	// file — it does not go through Forge's module rendering pipeline.
	indexTpl := template.Must(template.ParseFiles("templates/index.html"))
	app.Handle("GET /", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := indexTpl.ExecuteTemplate(w, "index.html", nil); err != nil {
			forge.WriteError(w, r, forge.ErrInternal)
		}
	}))

	log.Println("Forge Devlog — http://localhost:8080")
	log.Println("  Home:       http://localhost:8080/")
	log.Println("  Posts:      http://localhost:8080/posts")
	log.Println("  Feed:       http://localhost:8080/posts/feed.xml")
	log.Println("  Sitemap:    http://localhost:8080/sitemap.xml")
	log.Println("  llms.txt:   http://localhost:8080/llms.txt")
	log.Println("  llms-full:  http://localhost:8080/llms-full.txt")
	log.Println("  robots.txt: http://localhost:8080/robots.txt")
	log.Println("  aidoc:      http://localhost:8080/posts/forge-v1-release/aidoc  (example)")
	log.Println("  (One post is Scheduled — it will publish automatically in 2 minutes)")
	if err := app.Run(":8080"); err != nil {
		log.Fatal(err)
	}
}

// seed inserts 8 devlog posts into repo: 6 Published, 1 Draft, 1 Scheduled.
// In a production app these would come from a SQL database via forge.SQLRepo[T].
func seed(repo forge.Repository[*Post]) {
	now := time.Now().UTC()
	schedIn2m := now.Add(2 * time.Minute)

	type postSpec struct {
		slug      string
		title     string
		body      string
		tags      []string
		status    forge.Status
		published time.Time
		schedAt   *time.Time
	}

	posts := []postSpec{
		{
			slug:  "why-forge-has-zero-dependencies",
			title: "Why Forge Has Zero Dependencies",
			body: `When we started building Forge, the first architectural decision we locked was
simple: zero third-party dependencies in the core package. Every Go developer
has seen it — a promising framework that pulls in a dependency graph the size of
a small city, pins you to one logging library, one router, one database layer.

Go's standard library is remarkably complete. net/http gives you a
production-ready HTTP server. html/template handles safe rendering.
encoding/xml covers RSS. database/sql abstracts every relational database.
We asked ourselves what third-party code would add that justified the cost —
version conflicts, supply chain risk, upgrade treadmills — and answered:
nothing we actually needed.

The constraint paid dividends immediately. Forge builds in under two seconds.
go test ./... runs in under a second with no network fetches. You can vendor it
with a single go mod vendor and never worry about left-pad moments. Zero
dependencies is not a marketing claim; it is a design principle that makes
every downstream project more maintainable.`,
			tags:      []string{"architecture", "design", "go"},
			status:    forge.Published,
			published: now.Add(-84 * 24 * time.Hour),
		},
		{
			slug:  "scheduled-publishing-without-a-job-queue",
			title: "How We Handle Scheduled Publishing Without a Job Queue",
			body: `Most CMS platforms solve scheduled publishing with a cron job or a dedicated
job queue: Redis, Sidekiq, Celery, BullMQ. These work, but they introduce
infrastructure you have to operate, monitor, and keep in sync with your
application state. We wanted something you could run on a single binary with no
external dependencies.

Forge uses an adaptive in-process ticker. When the scheduler starts it looks at
the nearest ScheduledAt timestamp across all modules and sets its tick interval
to half the remaining time, down to a minimum of one second. If nothing is
scheduled the interval falls back to 60 seconds. This means a post scheduled
one minute in the future fires within a second of its target time, while a
post scheduled a week away barely touches the CPU.

On each tick the scheduler calls processScheduled on each module, queries the
repository for items in Scheduled status with ScheduledAt in the past,
transitions them to Published, sets PublishedAt, and fires AfterPublish.
The whole operation is synchronous and runs well under a millisecond for
typical content volumes. No queue, no worker, no operations burden.`,
			tags:      []string{"scheduler", "architecture", "concurrency"},
			status:    forge.Published,
			published: now.Add(-70 * 24 * time.Hour),
		},
		{
			slug:  "building-llms-txt-support-in-go",
			title: "Building llms.txt Support Into a Go Web Framework",
			body: `The llms.txt standard gives AI assistants a machine-readable map of a
website's content. The format is deliberately simple: a markdown file at
/llms.txt with a list of links and one-line descriptions. Forge implements it
natively — just pass AIIndex(LLMsTxt) as a module option and Forge handles
the rest.

Internally, Forge maintains a LLMsStore that accumulates entries as modules
register themselves. Each entry carries a title, URL, and summary. Forge derives
the summary from the content type's AISummary() method if the type implements
AIDocSummary, or falls back to the first 160 characters of the plain-text
excerpt. The /llms.txt handler renders the store in the standard compact format.

We also support /llms-full.txt, an opt-in endpoint that concatenates the full
markdown representation of every Published item. This gives AI assistants a
single request that yields the entire content corpus — useful for RAG pipelines
and for AI assistants that want to answer questions about your documentation
without repeated fetches.`,
			tags:      []string{"ai", "llms.txt", "seo"},
			status:    forge.Published,
			published: now.Add(-56 * 24 * time.Hour),
		},
		{
			slug:  "content-lifecycle-in-forge",
			title: "Content Lifecycle in Forge: Draft, Scheduled, Published, Archived",
			body: `Every piece of content in Forge moves through a defined lifecycle: Draft →
Scheduled → Published → Archived. Forge enforces this at the framework level,
not the application level. A Draft item returns 404 on public endpoints. A
Scheduled item is invisible until its ScheduledAt time passes. An Archived item
is preserved in storage but excluded from all public lists.

This matters because lifecycle enforcement is the kind of logic that is trivial
to implement once, catastrophic to forget. We have all seen staging content
accidentally published, deleted posts returning 200s, draft articles indexed by
Google. Forge makes the correct behaviour the only available behaviour —
developers cannot forget to check status because the check does not exist in
application code; it exists in the framework.

The lifecycle also integrates with signals. AfterPublish fires whenever an item
transitions to Published, whether that happens via an API call or via the
scheduled publisher. This gives you one place to hook logging, cache warming,
webhook dispatch, and notification delivery — regardless of how the publish
happened.`,
			tags:      []string{"content", "lifecycle", "design"},
			status:    forge.Published,
			published: now.Add(-42 * 24 * time.Hour),
		},
		{
			slug:  "why-we-chose-net-http-servemux",
			title: "Why We Chose Go's net/http ServeMux Over a Third-Party Router",
			body: `Go 1.22 shipped a significantly improved http.ServeMux: method-specific routes
(GET /posts/{slug}), named path parameters via r.PathValue, and precedence
rules that make tighter patterns win over looser ones. For a content framework
that generates a fixed set of routes per module — list, show, create, update,
delete, feed, sitemap, aidoc — the standard mux covers everything we need.

Third-party routers offer features like regex constraints, middleware per-route,
and automatic OPTIONS handling. We do not need any of these. Forge handles
middleware at the module and app level, not the route level. Regex constraints
on slugs would complicate the URL scheme without benefit.

Using the standard mux also means zero extra import, zero compatibility risk
with future Go versions, and instant familiarity for any Go developer who reads
the framework source. When the answer is already in the standard library,
adding a dependency is not a trade-off — it is a mistake.`,
			tags:      []string{"http", "routing", "go", "design"},
			status:    forge.Published,
			published: now.Add(-28 * 24 * time.Hour),
		},
		{
			slug:  "forge-v1-release",
			title: "Forge v1.0.0: Production-Ready, Zero Dependencies",
			body: `Today we are tagging Forge v1.0.0. The API is stable. The test suite covers
87% of production code paths. Benchmarks confirm that the hot paths — token
validation, redirect lookup, HTML template rendering — all run in single-digit
microseconds with zero or near-zero allocations on the critical path.

Milestone 9 added systematic benchmark coverage across all eight milestone
layers, a godoc pass ensuring every exported symbol has a doc comment, three
example applications (blog, docs, API), and a CHANGELOG tracing every decision
from v0.1.0 to v1.0.0. The result is a framework you can read end-to-end in an
afternoon and trust in production on day one.

Forge 2.0 will add MCP (Model Context Protocol) support — a native integration
with Claude, Copilot, and Cursor that exposes your content as readable MCP
resources and your write operations as MCP tools. The syntax is already
reserved in mcp.go.`,
			tags:      []string{"release", "v1", "announcement"},
			status:    forge.Published,
			published: now.Add(-14 * 24 * time.Hour),
		},
		{
			// Forge: Draft items are stored but never served on public endpoints.
			// They are excluded from /sitemap.xml, /llms.txt, and RSS feeds.
			// GET /posts/mcp-support-preview returns 404 while this is Draft.
			slug:   "mcp-support-preview",
			title:  "Preview: Forge and the Model Context Protocol",
			body:   "A first look at MCP support coming in Forge v2.0 — draft content, not yet ready for publication.",
			tags:   []string{"mcp", "ai", "v2"},
			status: forge.Draft,
		},
		{
			// Forge: Scheduled items have a ScheduledAt timestamp in the future.
			// The in-process scheduler transitions them to Published automatically.
			// Visit http://localhost:8080/posts after 2 minutes to see this appear.
			slug:    "zero-to-production-with-forge",
			title:   "Zero to Production With Forge",
			body:    "A step-by-step guide to building a production Forge application from scratch — deploying on a single VPS with a PostgreSQL backend via forge-pgx.",
			tags:    []string{"tutorial", "deployment", "production"},
			status:  forge.Scheduled,
			schedAt: &schedIn2m,
		},
	}

	ctx := context.Background()
	for _, spec := range posts {
		node := forge.Node{
			ID:          forge.NewID(),
			Slug:        spec.slug,
			Status:      spec.status,
			PublishedAt: spec.published,
			ScheduledAt: spec.schedAt,
		}
		post := &Post{Node: node, Title: spec.title, Body: spec.body, Tags: spec.tags}
		if err := repo.Save(ctx, post); err != nil {
			log.Fatalf("seed %q: %v", spec.slug, err)
		}
	}
}
