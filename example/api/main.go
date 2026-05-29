// Package main is a self-contained Forge JSON API — a Go resource curator —
// demonstrating authentication, role-based authorisation, validation hooks,
// and legacy URL redirects with no HTML templates.
//
// The binary runs in two modes:
//
//   - Server mode (default): starts a JSON API on :8082.
//   - CLI mode: when a sub-command is provided the binary acts as a client,
//     sending authenticated requests to the running server and printing results.
//
// # CLI Usage
//
//	./api                                                  start the JSON API server (default)
//	./api html                                             start the HTML site server — browsable pages
//	./api list                                             list resources (public)
//	./api get <slug>                                       fetch one resource (public)
//	./api create <title> <url> <description>               create a resource (Editor)
//	./api update <slug> <title> <url> <description>        update a resource (Editor)
//	./api delete <slug>                                    delete a resource (Editor)
//
// Run with:
//
//	cd example/api && go run .
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"smeldr.dev/core"
)

// secret is the HMAC signing key shared between smeldr.BearerHMAC (which
// validates incoming tokens) and smeldr.SignToken (which issued them).
// In production read this from an environment variable or secrets store —
// never hard-code it.
const secret = "forge-api-example-secret-32bytes!"

// NOTE: tokens are hardcoded for demonstration only.
// In production use smeldr.SignToken to issue signed HMAC tokens.
var (
	editorToken string // smeldr.Editor role — can create and update
	authorToken string // smeldr.Author role — read-only (below Editor threshold)
)

func init() {
	var err error

	// Forge: SignToken signs a User into a bearer token verified by BearerHMAC.
	// ttl=0 means the token never expires — set a finite duration in production.
	editorToken, err = smeldr.SignToken(
		smeldr.User{ID: "editor-1", Name: "Alice", Roles: []smeldr.Role{smeldr.Editor}},
		secret, 0,
	)
	if err != nil {
		log.Fatalf("sign editor token: %v", err)
	}

	authorToken, err = smeldr.SignToken(
		smeldr.User{ID: "author-1", Name: "Bob", Roles: []smeldr.Role{smeldr.Author}},
		secret, 0,
	)
	if err != nil {
		log.Fatalf("sign author token: %v", err)
	}
}

// Resource is the content type for a curated Go community resource.
// Embedding smeldr.Node provides ID, Slug, Status, and timestamp fields.
type Resource struct {
	smeldr.Node

	Title       string `smeldr:"required,min=3,max=200"`
	URL         string `smeldr:"required"`
	Description string `smeldr:"required,min=10"`
	Tags        []string
}

// Head implements [smeldr.Headable] so Resource satisfies [smeldr.SitemapNode].
// Forge calls Head() when building the sitemap fragment at /resources/sitemap.xml.
// In HTML mode the Description field populates the <meta name="description"> tag.
func (r *Resource) Head() smeldr.Head {
	return smeldr.Head{
		Title:       r.Title,
		Description: r.Description,
	}
}

// Markdown returns a plain-text summary of the resource suitable for AI index.
// Implementing this method makes Resource satisfy [smeldr.Markdownable], which
// enables the /llms-full.txt corpus endpoint via AIIndex(LLMsTxtFull).
func (r *Resource) Markdown() string {
	return "# " + r.Title + "\n\n" + r.Description + "\n\nURL: " + r.URL
}

func main() {
	if len(os.Args) > 1 {
		if os.Args[1] == "html" {
			runServer(true)
			return
		}
		runCLI(os.Args[1:])
		return
	}
	runServer(false)
}

// runServer starts the API on :8082.
// When withHTML is true, smeldr.Templates is added to the module so every
// resource is rendered as a browsable HTML page at /resources/{slug}.
// Without it the server runs as a headless JSON API (the default).
func runServer(withHTML bool) {
	repo := smeldr.NewMemoryRepo[*Resource]()
	seed(repo)

	// Forge: BearerHMAC validates Authorization: Bearer <token> on every request.
	// It returns an AuthFunc — pair it with smeldr.Authenticate to populate
	// Context.User() so module role checks (smeldr.Auth) evaluate correctly.
	auth := smeldr.BearerHMAC(secret)

	// Build module options. smeldr.Templates is appended when running in HTML mode,
	// turning the JSON API into a rendered website with no other code change.
	opts := []smeldr.Option{
		smeldr.At("/resources"),
		smeldr.Repo(repo),

		// Forge: Auth(Read(Guest), Write(Editor)) sets the minimum role for each
		// operation class. Guest means no token required to read. Editor means
		// a valid Editor-or-higher token is required to create or update.
		smeldr.Auth(
			smeldr.Read(smeldr.Guest),   // GET /resources and GET /resources/{slug} — public
			smeldr.Write(smeldr.Editor), // POST /resources and PUT /resources/{slug} — Editor+
		),

		// Forge: BeforeCreate fires synchronously before the item is saved.
		// Returning a non-nil error aborts the create and sends a 422 response.
		// smeldr.Err creates a smeldr.ValidationError carrying a field-level message.
		smeldr.On(smeldr.BeforeCreate, func(_ smeldr.Context, r *Resource) error {
			if !strings.HasPrefix(r.URL, "http://") && !strings.HasPrefix(r.URL, "https://") {
				return smeldr.Err("url", "must start with http:// or https://")
			}
			return nil
		}),

		// Forge: SitemapConfig{} registers a sitemap fragment at /resources/sitemap.xml.
		// The app-level sitemap index at /sitemap.xml aggregates all module fragments.
		smeldr.SitemapConfig{},

		// Forge: Feed(FeedConfig{}) registers an RSS 2.0 feed at /resources/feed.xml.
		smeldr.Feed(smeldr.FeedConfig{Title: "Forge API — Resources", Description: "Curated Go community resources"}),

		// Forge: AIIndex registers AI discovery endpoints.
		//   /llms.txt          — compact content index (llmstxt.org format)
		//   /llms-full.txt     — full markdown corpus (requires Markdownable)
		smeldr.AIIndex(smeldr.LLMsTxt, smeldr.LLMsTxtFull),
	}
	if withHTML {
		// Forge: Templates("templates") enables HTML rendering. Forge parses
		// templates/list.html (GET /resources) and templates/show.html
		// (GET /resources/{slug}) at startup. The JSON API routes continue to
		// work — clients sending Accept: application/json still get JSON.
		opts = append(opts, smeldr.Templates("templates"))
	}
	m := smeldr.NewModule((*Resource)(nil), opts...)

	app := smeldr.New(smeldr.MustConfig(smeldr.Config{
		BaseURL: "http://localhost:8082",
		Secret:  []byte(secret),
	}))

	// Forge: Authenticate(auth) runs BearerHMAC on every inbound request and
	// stores the User in the request context so Context.User() returns it.
	// Without this middleware, ctx.User() always returns GuestUser and the
	// write-role check in smeldr.Auth would never pass.
	//
	// SecurityHeaders() adds HSTS, X-Frame-Options, X-Content-Type-Options,
	// Referrer-Policy, and Content-Security-Policy to every response.
	//
	// RateLimit(100, time.Second) caps each IP at 100 requests per second.
	// Pass smeldr.TrustedProxy() as a third argument when running behind nginx
	// or Caddy so the real client IP is read from X-Forwarded-For.
	app.Use(
		smeldr.Authenticate(auth),
		smeldr.SecurityHeaders(),
		smeldr.RateLimit(100, time.Second),
	)

	// Forge: Redirects(From("/resources/go-spec"), "/resources/go-language-spec")
	// registers the 301 rule in the RedirectStore so it appears in the manifest
	// at /.well-known/redirects.json. The explicit Handle below registers the same
	// redirect as a fixed mux route so it takes priority over GET /resources/{slug}.
	app.Content(m,
		smeldr.Redirects(
			smeldr.From("/resources/go-spec"),
			"/resources/go-language-spec",
		),
	)

	// Explicit route so the redirect fires before GET /resources/{slug}.
	// Fixed-path patterns beat wildcard patterns in Go 1.22's mux.
	app.Handle("GET /resources/go-spec", http.RedirectHandler("/resources/go-language-spec", http.StatusMovedPermanently))

	// Forge: SEO(&RobotsConfig{Sitemaps: true}) registers GET /robots.txt and
	// appends the sitemap index URL to its Sitemap: directives.
	app.SEO(&smeldr.RobotsConfig{Sitemaps: true})

	// Welcome page — real links for all GET endpoints; CLI commands for write ops.
	app.Handle("GET /", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>Forge API Example</title>
<style>
body{font-family:system-ui,sans-serif;max-width:760px;margin:40px auto;padding:0 24px;color:#1a1a1a}
h1{font-size:1.8rem;margin-bottom:.25rem}p{color:#555;line-height:1.6}
h2{margin:2rem 0 .5rem}ul{padding-left:1.2rem}li{margin:.4rem 0}
code{background:#f4f4f4;padding:2px 6px;border-radius:4px;font-size:.9em}
pre{background:#f4f4f4;padding:12px 16px;border-radius:6px;overflow-x:auto;font-size:.85em;line-height:1.7}
a{color:#2563eb}
footer{margin-top:3rem;padding-top:1rem;border-top:1px solid #e5e7eb;font-size:.85rem;color:#888}
footer a{color:#2563eb}
</style>
</head>
<body>
<h1>Forge API Example</h1>
<p>A headless JSON API built with Forge. Demonstrates authentication, role-based authorisation, validation hooks, and legacy URL redirects.</p>
<h2>Read endpoints (public)</h2>
<ul>
  <li><a href="/resources"><code>GET /resources</code></a> &#8212; list published resources</li>
  <li><a href="/resources/go-language-spec"><code>GET /resources/go-language-spec</code></a> &#8212; single resource</li>
  <li><a href="/resources/go-spec"><code>GET /resources/go-spec</code></a> &#8212; legacy redirect &#8594; <code>/resources/go-language-spec</code></li>
</ul>
<h2>HTML site mode</h2>
<p>Restart the server with <code>./api html</code> to enable server-rendered HTML pages. The same resources are available as browsable pages:</p>
<pre>./api html</pre>
<ul>
  <li><a href="/resources"><code>GET /resources</code></a> &#8212; rendered list page (in HTML mode)</li>
  <li><a href="/resources/go-language-spec"><code>GET /resources/go-language-spec</code></a> &#8212; rendered detail page (in HTML mode)</li>
</ul>
<h2>Write endpoints &#8212; CLI</h2>
<p>Write operations require an Editor token. Use the built-in CLI from the same binary:</p>
<pre>./api list
./api get go-language-spec
./api create "My Resource" "https://example.com" "A great Go resource."
./api update my-resource "Updated Title" "https://example.com" "Better description."
./api delete my-resource</pre>
<h2>AI &amp; Discovery</h2>
<ul>
  <li><a href="/llms.txt"><code>GET /llms.txt</code></a> &#8212; compact AI index</li>
  <li><a href="/llms-full.txt"><code>GET /llms-full.txt</code></a> &#8212; full AI corpus</li>
  <li><a href="/resources/sitemap.xml"><code>GET /resources/sitemap.xml</code></a> &#8212; sitemap fragment</li>
  <li><a href="/resources/feed.xml"><code>GET /resources/feed.xml</code></a> &#8212; RSS feed</li>
  <li><a href="/.well-known/redirects.json"><code>GET /.well-known/redirects.json</code></a> &#8212; redirect manifest</li>
</ul>
<footer>Built with <a href="https://smeldr.dev/core">Smeldr</a> &#183; <a href="/robots.txt">robots.txt</a></footer>
</body></html>`)
	}))

	if withHTML {
		log.Println("Forge API (HTML mode) -- http://localhost:8082")
		log.Println("")
		log.Println("  HTML pages:")
		log.Println("    http://localhost:8082/resources")
		log.Println("    http://localhost:8082/resources/go-language-spec")
		log.Println("")
		log.Println("  JSON API still works (send Accept: application/json)")
		log.Println("")
		log.Println("  Editor token (Alice):", editorToken)
	} else {
		log.Println("Forge API -- http://localhost:8082")
		log.Println("")
		log.Println("  Editor token (Alice):", editorToken)
		log.Println("  Author token (Bob):  ", authorToken)
		log.Println("")
		log.Println("  ./api list                                     GET /resources  (public)")
		log.Println("  ./api get go-language-spec                     GET /resources/go-language-spec  (public)")
		log.Println("  ./api create \"My Resource\" \"https://…\" \"Desc\"  POST /resources  (Editor)")
		log.Println("  ./api update <slug> <title> <url> <desc>       PUT /resources/<slug>  (Editor)")
		log.Println("  ./api delete <slug>                            DELETE /resources/<slug>  (Editor)")
	}

	if err := app.Run(":8082"); err != nil {
		log.Fatal(err)
	}
}

// seed inserts 8 Published resources, 1 Draft, and 1 Scheduled into repo.
// In production these come from a SQL database via smeldr.SQLRepo[T].
func seed(repo smeldr.Repository[*Resource]) {
	now := time.Now().UTC()
	ctx := context.Background()

	type spec struct {
		slug   string
		title  string
		url    string
		desc   string
		tags   []string
		status smeldr.Status
		days   int        // days ago (for PublishedAt)
		sched  *time.Time // non-nil for Scheduled
	}

	schedTime := now.Add(48 * time.Hour) // scheduled 2 days in the future

	resources := []spec{
		{
			slug:   "go-language-spec",
			title:  "The Go Language Specification",
			url:    "https://go.dev/ref/spec",
			desc:   "The official specification for the Go programming language. The authoritative reference for parsers, compilers, and anyone building Go tooling.",
			tags:   []string{"spec", "language", "reference"},
			status: smeldr.Published,
			days:   90,
		},
		{
			slug:   "effective-go",
			title:  "Effective Go",
			url:    "https://go.dev/doc/effective_go",
			desc:   "A guide to writing clear, idiomatic Go code. Covers formatting, names, control flow, data structures, methods, interfaces, and concurrency.",
			tags:   []string{"guide", "style", "idioms"},
			status: smeldr.Published,
			days:   85,
		},
		{
			slug:   "the-go-blog",
			title:  "The Go Blog",
			url:    "https://go.dev/blog",
			desc:   "Official blog from the Go team at Google. Covers language features, toolchain releases, community news, and deep technical articles.",
			tags:   []string{"blog", "official", "news"},
			status: smeldr.Published,
			days:   80,
		},
		{
			slug:   "pkg-go-dev",
			title:  "pkg.go.dev",
			url:    "https://pkg.go.dev",
			desc:   "The official Go package index and documentation host. Search for any public Go module, browse docs, and explore the import graph.",
			tags:   []string{"packages", "docs", "modules"},
			status: smeldr.Published,
			days:   75,
		},
		{
			slug:   "gopls",
			title:  "gopls — The Go Language Server",
			url:    "https://pkg.go.dev/golang.org/x/tools/gopls",
			desc:   "The official language server for Go, consumed by VS Code, Neovim, Emacs, and other editors. Provides completion, diagnostics, and refactoring.",
			tags:   []string{"tooling", "lsp", "editor"},
			status: smeldr.Published,
			days:   70,
		},
		{
			slug:   "go-playground",
			title:  "The Go Playground",
			url:    "https://go.dev/play",
			desc:   "Run and share Go code snippets in the browser with no installation required. Useful for reproducing bugs, sharing examples, and learning.",
			tags:   []string{"playground", "tools", "learning"},
			status: smeldr.Published,
			days:   65,
		},
		{
			slug:   "concurrency-is-not-parallelism",
			title:  "Concurrency is not Parallelism — Rob Pike",
			url:    "https://go.dev/talks/2012/waza.slide",
			desc:   "Rob Pike's landmark 2012 talk distinguishing concurrency (structuring programs) from parallelism (executing computations simultaneously). Essential Go philosophy.",
			tags:   []string{"concurrency", "talk", "rob-pike"},
			status: smeldr.Published,
			days:   60,
		},
		{
			slug:   "go-memory-model",
			title:  "The Go Memory Model",
			url:    "https://go.dev/ref/mem",
			desc:   "Specifies the conditions under which reads of a variable in one goroutine can be guaranteed to observe values written by a different goroutine.",
			tags:   []string{"concurrency", "memory", "reference"},
			status: smeldr.Published,
			days:   55,
		},
		{
			// Draft — not publicly visible; demonstrates the Draft lifecycle state.
			slug:   "go-tour",
			title:  "A Tour of Go",
			url:    "https://go.dev/tour",
			desc:   "An interactive introduction to Go in your browser. Covers the basics of the language with hands-on exercises — the recommended starting point for beginners.",
			tags:   []string{"learning", "interactive", "beginner"},
			status: smeldr.Draft,
			days:   0,
		},
		{
			// Scheduled — will become Published at sched time; demonstrates the Scheduled lifecycle state.
			slug:   "go-proverbs",
			title:  "Go Proverbs — Rob Pike",
			url:    "https://go-proverbs.github.io",
			desc:   "Rob Pike's collection of Go proverbs from GopherFest 2015. Pithy design principles that capture the philosophy of idiomatic Go.",
			tags:   []string{"proverbs", "philosophy", "rob-pike"},
			status: smeldr.Scheduled,
			days:   0,
			sched:  &schedTime,
		},
	}

	for _, r := range resources {
		var pub time.Time
		if r.days > 0 {
			pub = now.Add(-time.Duration(r.days) * 24 * time.Hour)
		}
		node := smeldr.Node{
			ID:          smeldr.NewID(),
			Slug:        r.slug,
			Status:      r.status,
			PublishedAt: pub,
			ScheduledAt: r.sched,
		}
		res := &Resource{
			Node:        node,
			Title:       r.title,
			URL:         r.url,
			Description: r.desc,
			Tags:        r.tags,
		}
		if err := repo.Save(ctx, res); err != nil {
			log.Fatalf("seed %q: %v", r.slug, err)
		}
	}
}

// runCLI dispatches CLI sub-commands to the running server at http://localhost:8082.
// The Editor token (signed in init) is used for write operations automatically —
// no --token flag or copy-pasting from the server log required.
func runCLI(args []string) {
	if len(args) == 0 {
		printUsage()
		return
	}
	switch args[0] {
	case "list":
		apiRequest(http.MethodGet, "/resources", "", nil)
	case "get":
		if len(args) < 2 {
			die("usage: api get <slug>")
		}
		apiRequest(http.MethodGet, "/resources/"+args[1], "", nil)
	case "create":
		if len(args) < 4 {
			die("usage: api create <title> <url> <description>")
		}
		apiRequest(http.MethodPost, "/resources", editorToken, map[string]any{
			"title":       args[1],
			"url":         args[2],
			"description": args[3],
			"status":      "published",
		})
	case "update":
		if len(args) < 5 {
			die("usage: api update <slug> <title> <url> <description>")
		}
		apiRequest(http.MethodPut, "/resources/"+args[1], editorToken, map[string]any{
			"title":       args[2],
			"url":         args[3],
			"description": args[4],
		})
	case "delete":
		if len(args) < 2 {
			die("usage: api delete <slug>")
		}
		apiRequest(http.MethodDelete, "/resources/"+args[1], editorToken, nil)
	case "html":
		die("'html' is a server flag, not a CLI command.\nUsage: api html    (start server with HTML templates)")
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", args[0])
		printUsage()
		os.Exit(1)
	}
}

// apiRequest sends an HTTP request to the local server and prints the JSON
// response to stdout. It exits 1 on network failure or a 4xx/5xx response.
func apiRequest(method, path, token string, body any) {
	const base = "http://localhost:8082"

	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			fmt.Fprintln(os.Stderr, "encode:", err)
			os.Exit(1)
		}
		r = bytes.NewReader(b)
	}

	req, err := http.NewRequest(method, base+path, r)
	if err != nil {
		fmt.Fprintln(os.Stderr, "build request:", err)
		os.Exit(1)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, "http:", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	out, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		fmt.Fprintf(os.Stderr, "error %d: %s\n", resp.StatusCode, strings.TrimSpace(string(out)))
		os.Exit(1)
	}

	if len(out) == 0 {
		fmt.Printf("%s %s %d\n", method, path, resp.StatusCode)
		return
	}

	var pretty bytes.Buffer
	if json.Indent(&pretty, out, "", "  ") == nil {
		fmt.Println(pretty.String())
	} else {
		fmt.Println(string(out))
	}
}

func printUsage() {
	fmt.Fprintln(os.Stderr, `Usage: api [command]

  (no command)                              start the JSON API server on :8082
  html                                      start the server with HTML templates
  list                                      GET /resources  (public)
  get <slug>                                GET /resources/<slug>  (public)
  create <title> <url> <desc>               POST /resources  (Editor)
  update <slug> <title> <url> <desc>        PUT /resources/<slug>  (Editor)
  delete <slug>                             DELETE /resources/<slug>  (Editor)`)
}

func die(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	os.Exit(1)
}
