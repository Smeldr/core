# Smeldr

Content-driven backend for agents, readers, and APIs.

[![Go Reference](https://pkg.go.dev/badge/smeldr.dev/core.svg)](https://pkg.go.dev/smeldr.dev/core)
[![Go Report Card](https://goreportcard.com/badge/github.com/Smeldr/core)](https://goreportcard.com/report/github.com/Smeldr/core)
[![codecov](https://codecov.io/gh/Smeldr/core/graph/badge.svg)](https://codecov.io/gh/Smeldr/core)
[![Version](https://img.shields.io/github/v/release/smeldr/core?filter=v*&color=%2341A300)](https://github.com/smeldr/core/releases/latest)

Smeldr has MCP as primary interface.
Go. Zero runtime dependencies.

Start a chat with your favorite agent, create content together and then...

...build and publish websites. Edit sections, upload images and manage access directly in chat. Hook up your workflow states to AI actions.

...schedule, change, delete posts on X, Mastodon or LinkedIn

...build apps and manage data and automate workflows

...manage multiple agents to react to signals with the built-in agent runtime

Or coordinate your entire pipeline and automated workflow, AI actions and human review included.

Directly from chat.

**v1.42.4 — stable.** Public APIs are stable within v1.
See [CHANGELOG.md](CHANGELOG.md).

## 30-second start

```bash
git clone https://github.com/smeldr/core
cd example/blog
go run .
# open http://localhost:8080
```

## What Smeldr gives you

**Content**
- **Full CRUD** — create, update, publish, archive, and delete through a single `Module[T]`
- **Draft-safe lifecycle** — drafts return 404 to guests; only Published content is visible
- **Draft preview** — share a signed `?preview=<token>` URL to let reviewers see a draft without logging in
- **Scheduled publishing** — set a future `ScheduledAt`; Smeldr transitions to Published automatically
- **Content negotiation** — one endpoint serves JSON, HTML, or Markdown based on `Accept`

**Auth & security**
- **Role-based auth** — Guest → Author → Editor → Admin enforced per-module, per-operation
- **Cookie compliance** — `/.well-known/cookies.json` declares cookie categories for GDPR tooling
- **Security headers** — CSP, HSTS, X-Frame-Options wired in one middleware call

**Discovery**
- **Structured data (JSON-LD)** — Article, Product, FAQ, and more emitted in every page `<head>`
- **Event-driven sitemap** — regenerated on publish, update, and delete; no cron job required
- **Open Graph** — og:title, og:description, og:image meta tags for social link previews
- **Twitter Cards** — twitter: meta tags with summary and summary_large_image support
- **RSS feed** — per-module feed at `/{prefix}/feed.xml` plus a global aggregate at `/feed.xml`

**AI-native**
- **AI indexing** — `/llms.txt` compact index, `/llms-full.txt` Markdown corpus, and per-item `/aidoc` endpoints
- **MCP integration** — connect AI agents to read and write content via the Model Context Protocol

**Infrastructure**
- **Graceful shutdown** — drains in-flight requests before exiting on SIGINT/SIGTERM
- **Log capture** — opt-in `app.CaptureLogs()` keeps recent errors in memory and serves them at `GET /_logs` (Admin) for live debugging — over plain HTTP, so it works even when MCP is down

---

| | Smeldr | Echo | Gin | Chi |
|---|---|---|---|---|
| Zero runtime dependencies¹ | ✓ | ✗ | ✗ | ~ |
| Content lifecycle built-in | ✓ | ✗ | ✗ | ✗ |
| Draft-safe by default | ✓ | ✗ | ✗ | ✗ |
| SEO + structured data | ✓ | ✗ | ✗ | ✗ |
| AI indexing (llms.txt + AIDoc) | ✓ | ✗ | ✗ | ✗ |
| Cookie compliance built-in | ✓ | ✗ | ✗ | ✗ |
| Social sharing built-in | ✓ | ✗ | ✗ | ✗ |
| Role hierarchy built-in | ✓ | ✗ | ✗ | ✗ |

> ¹ The test suite uses `modernc.org/sqlite` for in-process SQL integration tests. There are no runtime dependencies in the core package.

---

## Installation

```bash
go get smeldr.dev/core
```

Requires Go 1.26+. No other dependencies.

---

## Minimal example

Define a content type, wire it, run it.

```go
package main

import (
	"log"

	"smeldr.dev/core"
)

type Post struct {
	smeldr.Node
	Title string `smeldr:"required,min=3" json:"title"`
	Body  string `smeldr:"required"       json:"body"`
}

func (p *Post) Head() smeldr.Head {
	return smeldr.Head{
		Title:       p.Title,
		Description: smeldr.Excerpt(p.Body, 160),
	}
}

func (p *Post) Markdown() string { return p.Body }

func main() {
	repo := smeldr.NewMemoryRepo[*Post]()
	m := smeldr.NewModule((*Post)(nil), // nil pointer — type parameter inferred, no allocation
		smeldr.At("/posts"),
		smeldr.Repo(repo),
		smeldr.Auth(smeldr.Read(smeldr.Guest), smeldr.Write(smeldr.Author)),
	)
	app := smeldr.New(smeldr.MustConfig(smeldr.Config{
		BaseURL: "http://localhost:8080",
		Secret:  []byte("change-this-secret-in-production"),
	}))
	app.Content(m)
	if err := app.Run(":8080"); err != nil {
		log.Fatal(err)
	}
}
```

Routes: `GET /posts`, `GET /posts/{slug}`, `POST /posts`, `PUT /posts/{slug}`,
`DELETE /posts/{slug}`, `GET /sitemap.xml`. Draft posts return 404 for guests.

---

## Full feature showcase

Same content type. Add options — each line unlocks a production feature.

```go
m := smeldr.NewModule((*Post)(nil),
	smeldr.At("/posts"),
	smeldr.Repo(smeldr.NewMemoryRepo[*Post]()),
	smeldr.Auth(smeldr.Read(smeldr.Guest), smeldr.Write(smeldr.Author)),
	smeldr.SitemapConfig{ChangeFreq: smeldr.Weekly, Priority: 0.8}, // /posts/sitemap.xml
	smeldr.Social(smeldr.OpenGraph, smeldr.TwitterCard),              // og: and twitter: meta tags
	smeldr.AIIndex(smeldr.LLMsTxt, smeldr.LLMsTxtFull, smeldr.AIDoc), // /llms.txt + /posts/{slug}/aidoc
	smeldr.Feed(smeldr.FeedConfig{Title: "My Blog"}),               // /posts/feed.xml + /feed.xml
	smeldr.Templates("templates/posts"),                           // HTML at Accept: text/html
	smeldr.On(smeldr.AfterPublish, func(_ smeldr.Context, p *Post) error {
		log.Printf("published: %s", p.Slug) // fires on publish and scheduled→Published
		return nil
	}),
)
```

Three runnable examples are in [example/](example/):
- **example/blog** — devlog with seeded posts, RSS, AI indexing, and scheduled publishing
- **example/api**  — headless JSON API with role-based auth and a redirect manifest
- **example/docs** — documentation site with AI indexing, `/llms.txt`, and AIDoc endpoints

Each runs with: `cd example/blog && go run .`

---

## Reference

Full API reference: [REFERENCE.md](docs/REFERENCE.md)  
Web docs: [smeldr.dev/docs](https://smeldr.dev/docs)

For full token management reference (create, list, revoke) see [REFERENCE.md — Token management](docs/REFERENCE.md#token-management).  
For draft preview tokens see [REFERENCE.md — Draft preview](docs/REFERENCE.md#draft-preview).

---

## License

[AGPL v3](LICENSE) — free for individuals, open source projects, and companies
building their own sites. A commercial license will be available for organisations
running Smeldr as a hosted service. See [COMMERCIAL.md](COMMERCIAL.md).
