# Forge

**The Go web framework designed for how you actually think.**  
Built for developers. Optimized for AI. Zero compromises on readability.

[![Go Reference](https://pkg.go.dev/badge/github.com/forge-cms/forge.svg)](https://pkg.go.dev/github.com/forge-cms/forge)
**v1.11.0 — stable.** All exported symbols are stable. No breaking changes without a major version bump. See [CHANGELOG.md](CHANGELOG.md).


| | Forge | Echo | Gin | Chi |
|---|---|---|---|---|
| Zero dependencies | ✓ | ✗ | ✗ | ~ |
| Content lifecycle built-in | ✓ | ✗ | ✗ | ✗ |
| Draft-safe by default | ✓ | ✗ | ✗ | ✗ |
| SEO + structured data | ✓ | ✗ | ✗ | ✗ |
| AI indexing (llms.txt + AIDoc) | ✓ | ✗ | ✗ | ✗ |
| Cookie compliance built-in | ✓ | ✗ | ✗ | ✗ |
| Social sharing built-in | ✓ | ✗ | ✗ | ✗ |
| Role hierarchy built-in | ✓ | ✗ | ✗ | ✗ |
| AI-native endpoints (llms.txt, AIDoc) | ✓ | ✗ | ✗ | ✗ |

---

## Installation

```bash
go get github.com/forge-cms/forge
```

Requires Go 1.22+. No other dependencies.

---

## Minimal example

Define a content type, wire it, run it.

```go
package main

import (
	"log"

	"github.com/forge-cms/forge"
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
	m := forge.NewModule((*Post)(nil),
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
		return nil // fires on publish and scheduled→Published
	}),
)
```

**What you get:**

Full CRUD · Role-based auth · Draft-safe lifecycle  
Structured data (JSON-LD) · Event-driven sitemap · Content negotiation  
Open Graph · Twitter Cards · AI indexing · RSS feed  
Security headers · Graceful shutdown · Cookie compliance manifest  
Scheduled publishing · MCP integration

---

## Reference

Full API reference: [REFERENCE.md](REFERENCE.md)  
Web docs: [forge-cms.dev/docs](https://forge-cms.dev/docs)

---

## License

[AGPL v3](LICENSE) — free for individuals, open source projects, and companies
building their own sites. A commercial license will be available for organisations
running Forge as a hosted service. See [COMMERCIAL.md](COMMERCIAL.md).
