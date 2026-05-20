// Package main is a self-contained Forge devlog — a production-pattern blog
// application that demonstrates the full v1.22.2 feature set:
//
//   - SQLite persistence via forge.SQLRepo (no cgo; uses modernc.org/sqlite)
//   - Auth and RBAC: Guest read, Author write, bearer-token API auth
//   - Content lifecycle (Draft, Scheduled, Published, Archived)
//   - MCP server: AI agents can create, update, publish, and read posts
//   - Audit trail: every write is recorded in forge_audit_log
//   - HTML template rendering with forge:head
//   - Open Graph and Twitter Card social metadata
//   - RSS 2.0 feed at /posts/feed.xml and /feed.xml
//   - Sitemap at /sitemap.xml with automatic regeneration
//   - AI indexing at /llms.txt and /llms-full.txt
//   - Scheduler: automatic Scheduled→Published transition
//   - AfterPublish signal: log hook on every publish event
//
// Run with:
//
//	cd example/blog && go run .
//
// Environment variables:
//
//	DB_PATH  path to the SQLite database file (default: blog.db)
//	SECRET   HMAC signing secret (default: change-this-secret-in-production)
//
// Then visit http://localhost:8080
package main

import (
	"context"
	"database/sql"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"time"

	"forge-cms.dev/forge"
	forgemcp "forge-cms.dev/forge-mcp"
	_ "modernc.org/sqlite" // register the modernc pure-Go SQLite driver
)

// Post is the content type for a Forge devlog post.
//
// Forge: embed forge.Node — never compose it. Node provides ID, Slug, Status,
// PublishedAt, ScheduledAt, CreatedAt, and UpdatedAt. SQLRepo maps exported
// fields to SQL columns using the `db` struct tag; `db:"-"` excludes a field.
type Post struct {
	forge.Node

	Title string   `forge:"required,min=3,max=120" db:"title"`
	Body  string   `forge:"required,min=10"        db:"body"`
	Tags  []string `db:"-"` // join table in production; excluded from SQL scan
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

// createSchema ensures all required tables exist in the SQLite database.
//
// Forge: forge.CreateAuditTable creates forge_audit_log. The posts and
// forge_tokens tables are defined here so the full schema is visible in one
// place and the application starts with a known, consistent state on every run.
func createSchema(db *sql.DB) error {
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS posts (
		id           TEXT PRIMARY KEY,
		slug         TEXT NOT NULL UNIQUE,
		status       TEXT NOT NULL,
		published_at DATETIME NOT NULL,
		scheduled_at DATETIME,
		created_at   DATETIME NOT NULL,
		updated_at   DATETIME NOT NULL,
		title        TEXT NOT NULL,
		body         TEXT NOT NULL
	)`); err != nil {
		return fmt.Errorf("create posts table: %w", err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS forge_tokens (
		id         TEXT PRIMARY KEY,
		name       TEXT NOT NULL,
		role       TEXT NOT NULL,
		expires_at DATETIME NOT NULL,
		revoked_at DATETIME,
		created_at DATETIME NOT NULL
	)`); err != nil {
		return fmt.Errorf("create forge_tokens table: %w", err)
	}
	if err := forge.CreateAuditTable(db); err != nil {
		return fmt.Errorf("create audit table: %w", err)
	}
	return nil
}

// seedIfEmpty inserts devlog posts when the repository is empty.
// It is a no-op on every subsequent startup — safe to call unconditionally.
//
// Forge: repo.FindAll with PerPage:1 is the correct emptiness check — it
// issues a SELECT LIMIT 1 against the posts table rather than a full scan.
func seedIfEmpty(ctx context.Context, repo forge.Repository[*Post]) error {
	existing, err := repo.FindAll(ctx, forge.ListOptions{PerPage: 1})
	if err != nil {
		return fmt.Errorf("check empty: %w", err)
	}
	if len(existing) > 0 {
		return nil
	}

	now := time.Now().UTC()
	schedIn2m := now.Add(2 * time.Minute)

	type postSpec struct {
		slug      string
		title     string
		body      string
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
afternoon and trust in production on day one.`,
			status:    forge.Published,
			published: now.Add(-14 * 24 * time.Hour),
		},
		{
			// Forge: Draft items are stored but never served on public endpoints.
			// GET /posts/mcp-support-preview returns 404 while this is a Draft.
			// Authenticated Authors see it via GET /posts/mcp-support-preview.
			slug:   "mcp-support-preview",
			title:  "Preview: Forge and the Model Context Protocol",
			body:   "A first look at MCP support in Forge v1.22 — AI agents can create, update, and publish content directly via Claude, Cursor, or any MCP-compatible assistant.",
			status: forge.Draft,
		},
		{
			// Forge: Scheduled items have a ScheduledAt timestamp in the future.
			// The in-process scheduler transitions them to Published automatically.
			// Visit http://localhost:8080/posts after 2 minutes to see this post appear.
			slug:    "zero-to-production-with-forge",
			title:   "Zero to Production With Forge",
			body:    "A step-by-step guide to building a production Forge application from scratch — deploying on a single VPS with a SQLite backend and MCP support for AI-assisted content authoring.",
			status:  forge.Scheduled,
			schedAt: &schedIn2m,
		},
	}

	for _, spec := range posts {
		node := forge.Node{
			ID:          forge.NewID(),
			Slug:        spec.slug,
			Status:      spec.status,
			PublishedAt: spec.published,
			ScheduledAt: spec.schedAt,
		}
		p := &Post{Node: node, Title: spec.title, Body: spec.body}
		if err := repo.Save(ctx, p); err != nil {
			return fmt.Errorf("seed %q: %w", spec.slug, err)
		}
	}
	return nil
}

func main() {
	// Forge: DB_PATH configures where the SQLite database file lives.
	// The default "blog.db" is fine for local development. In production,
	// set it to a persistent path (e.g. /data/blog.db on a VPS).
	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "blog.db"
	}

	// Forge: modernc.org/sqlite registers the "sqlite" driver name via the
	// blank import above. No cgo required — the driver is pure Go.
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()

	if err := createSchema(db); err != nil {
		log.Fatalf("create schema: %v", err)
	}

	// Forge: SECRET should be a 32+ byte cryptographically random value in
	// production. Generate it once at deploy time and store it in an environment
	// variable or secrets manager. The same secret signs both session cookies
	// and bearer tokens — rotate it and all tokens are instantly invalidated.
	secret := os.Getenv("SECRET")
	if secret == "" {
		secret = "change-this-secret-in-production"
		log.Println("WARN: using default SECRET — set the SECRET env var before deploying")
	}

	// Forge: TokenStore persists named bearer tokens in forge_tokens. Tokens
	// are issued via the MCP create_token tool or the forge-cli token command.
	// forge.New wires TokenStore into the authentication middleware automatically
	// when it is present in Config — no app.Use() call required.
	tokenStore := forge.NewTokenStore(db, secret)

	// Forge: SQLRepo[*Post] maps Post fields to the posts table via `db` struct
	// tags. It implements forge.Repository[*Post] — the same interface used by
	// MemoryRepo, so you can swap storage backends without changing module code.
	repo := forge.NewSQLRepo[*Post](db)

	ctx := context.Background()
	if err := seedIfEmpty(ctx, repo); err != nil {
		log.Fatalf("seed: %v", err)
	}

	// Forge: On(AfterPublish, ...) registers a signal handler that fires every
	// time a post transitions to Published — whether via API, MCP, or the
	// in-process scheduler. One hook covers all publish paths.
	m := forge.NewModule((*Post)(nil),
		forge.At("/posts"),
		forge.Repo(repo),

		// Forge: Auth(Read(Guest), Write(Author)) allows anyone to read Published
		// posts and requires a valid Author bearer token to create or update.
		// Tokens are issued via forge.TokenStore — no hardcoded credentials.
		forge.Auth(forge.Read(forge.Guest), forge.Write(forge.Author)),

		// Forge: MCP(MCPRead, MCPWrite) exposes this module as MCP resources and
		// tools. AI agents connected via the /mcp endpoint can list, read, create,
		// update, publish, schedule, archive, and delete posts.
		forge.MCP(forge.MCPRead, forge.MCPWrite),

		// Forge: SitemapConfig{} opts this module into /sitemap.xml.
		// Default behaviour: weekly changefreq, 0.5 priority. No configuration
		// needed unless you want to override those defaults.
		forge.SitemapConfig{},

		// Forge: Social(OpenGraph, TwitterCard) generates og: and twitter: meta
		// tags from the Head() return value on every HTML response.
		forge.Social(forge.OpenGraph, forge.TwitterCard),

		// Forge: Feed() enables GET /posts/feed.xml. The aggregate /feed.xml is
		// mounted automatically when any module registers a feed.
		forge.Feed(forge.FeedConfig{
			Title:       "Forge Devlog",
			Description: "Engineering notes and release announcements from the Forge team.",
		}),

		// Forge: AIIndex(LLMsTxt, LLMsTxtFull, AIDoc) registers published content
		// in /llms.txt (compact link list), /llms-full.txt (full markdown corpus),
		// and /posts/{slug}/aidoc (per-item token-efficient text for AI assistants).
		forge.AIIndex(forge.LLMsTxt, forge.LLMsTxtFull, forge.AIDoc),

		// Forge: Templates("templates") enables HTML rendering. Forge parses
		// templates/list.html and templates/show.html at startup and returns 500
		// if either file is missing or has a syntax error — fast failure, not
		// silent degradation.
		forge.Templates("templates"),

		forge.On(forge.AfterPublish, func(_ forge.Context, p *Post) error {
			log.Printf("[blog] published: %q (slug: %s)", p.Title, p.Slug)
			return nil
		}),
	)

	app := forge.New(forge.MustConfig(forge.Config{
		BaseURL: "http://localhost:8080",
		Secret:  []byte(secret),
		// Forge: DB is required for auth middleware, audit, token validation,
		// and any forge package that needs durable storage. Pass the same *sql.DB
		// that owns your schema — Forge never creates tables unless you call the
		// explicit Create* functions.
		DB:         db,
		TokenStore: tokenStore,
	}))

	app.Content(m)

	// Forge: SEO with Sitemaps: true appends /sitemap.xml to robots.txt so
	// crawlers discover it automatically.
	app.SEO(&forge.RobotsConfig{Sitemaps: true})

	// Forge: Audit records every write operation (create, update, publish,
	// archive, delete) in forge_audit_log. The /_audit endpoint (Editor+) lets
	// admins query the trail by slug or actor.
	app.Audit(forge.NewAuditStore(db))

	// Forge: forgemcp.New(app) creates the MCP server. Wire it on two routes:
	// GET /mcp for SSE (streaming) connections and POST /mcp/message for
	// stateless HTTP connections. Both paths serve the same handler — the MCP
	// transport layer picks the right mode automatically.
	mcpSrv := forgemcp.New(app)
	app.Handle("GET /mcp", mcpSrv.Handler())
	app.Handle("POST /mcp/message", mcpSrv.Handler())

	// Mount a welcome page at / so http://localhost:8080 shows an overview
	// instead of a 404.
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
	log.Println("  MCP:        http://localhost:8080/mcp  (connect Claude or Cursor here)")
	log.Println("  Audit:      http://localhost:8080/_audit  (Editor+ token required)")
	if err := app.Run(":8080"); err != nil {
		log.Fatal(err)
	}
}
