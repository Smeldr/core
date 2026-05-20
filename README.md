# Forge

A Go framework for content-driven applications. Zero runtime dependencies. AI-native by default.

[![Go Reference](https://pkg.go.dev/badge/forge-cms.dev/forge.svg)](https://pkg.go.dev/forge-cms.dev/forge)
**v1.22.2 — stable.** All exported symbols are stable. No breaking changes without a major version bump. See [CHANGELOG.md](CHANGELOG.md).

## 30-second start

```bash
git clone https://github.com/forge-cms/forge
cd example/blog
go run .
# open http://localhost:8080
```

## What Forge gives you

**Content**
- **Full CRUD** — create, update, publish, archive, and delete through a single `Module[T]`
- **Draft-safe lifecycle** — drafts return 404 to guests; only Published content is visible
- **Draft preview** — share a signed `?preview=<token>` URL to let reviewers see a draft without logging in
- **Scheduled publishing** — set a future `ScheduledAt`; Forge transitions to Published automatically
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

---

| | Forge | Echo | Gin | Chi |
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
go get forge-cms.dev/forge
```

Requires Go 1.26+. No other dependencies.

---

## Minimal example

Define a content type, wire it, run it.

```go
package main

import (
	"log"

	"forge-cms.dev/forge"
)

type Post struct {
	forge.Node
	Title string `forge:"required,min=3" json:"title"`
	Body  string `forge:"required"       json:"body"`
}

func (p *Post) Head() forge.Head {
	return forge.Head{
		Title:       p.Title,
		Description: forge.Excerpt(p.Body, 160),
	}
}

func (p *Post) Markdown() string { return p.Body }

func main() {
	repo := forge.NewMemoryRepo[*Post]()
	m := forge.NewModule((*Post)(nil), // nil pointer — type parameter inferred, no allocation
		forge.At("/posts"),
		forge.Repo(repo),
		forge.Auth(forge.Read(forge.Guest), forge.Write(forge.Author)),
	)
	app := forge.New(forge.MustConfig(forge.Config{
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
m := forge.NewModule((*Post)(nil),
	forge.At("/posts"),
	forge.Repo(forge.NewMemoryRepo[*Post]()),
	forge.Auth(forge.Read(forge.Guest), forge.Write(forge.Author)),
	forge.SitemapConfig{ChangeFreq: forge.Weekly, Priority: 0.8}, // /posts/sitemap.xml
	forge.Social(forge.OpenGraph, forge.TwitterCard),              // og: and twitter: meta tags
	forge.AIIndex(forge.LLMsTxt, forge.LLMsTxtFull, forge.AIDoc), // /llms.txt + /posts/{slug}/aidoc
	forge.Feed(forge.FeedConfig{Title: "My Blog"}),               // /posts/feed.xml + /feed.xml
	forge.Templates("templates/posts"),                           // HTML at Accept: text/html
	forge.On(forge.AfterPublish, func(_ forge.Context, p *Post) error {
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
Web docs: [forge-cms.dev/docs](https://forge-cms.dev/docs)

For full token management reference (create, list, revoke) see [REFERENCE.md — Token management](docs/REFERENCE.md#token-management).  
For draft preview tokens see [REFERENCE.md — Draft preview](docs/REFERENCE.md#draft-preview).

---

## License

[AGPL v3](LICENSE) — free for individuals, open source projects, and companies
building their own sites. A commercial license will be available for organisations
running Forge as a hosted service. See [COMMERCIAL.md](COMMERCIAL.md).
