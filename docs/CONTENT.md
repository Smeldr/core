# Use Smeldr as a Go library

Smeldr is built around Decisions, but the same model also runs content: content types, publishing, feeds and AI indexing, with the same lifecycle, roles and history. This page is for building a site or an API in Go with Smeldr as a library. For the decision model, start with the [README](../README.md).

## Installation

    go get smeldr.dev/core

Requires Go 1.26+. No other dependencies.

**Databases.** State flows (transition rules and role gates, `Locked` states, `SuppressesSignals`, async triggers and `ConflictPolicy`) are enforced on SQLite and on Postgres alike. From v1.118.0 that includes Postgres through `smeldr.dev/core/pgx`, where a flow used to be stored and never consulted: upgrading can refuse transitions that passed unchecked, so read the upgrade note in [State flows](REFERENCE.md#state-flows).

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
	m := smeldr.NewModule((*Post)(nil), // nil pointer: type parameter inferred, no allocation
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
`PATCH /posts/{slug}`, `DELETE /posts/{slug}`, `GET /sitemap.xml`. Draft posts return 404 for guests.

---

## Full feature showcase

Same content type. Add options: each line unlocks a production feature.

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

Three runnable examples are in [example/](../example/):
- **example/blog**: devlog with seeded posts, RSS, AI indexing, and scheduled publishing
- **example/api**: headless JSON API with role-based auth and a redirect manifest
- **example/docs**: documentation site with AI indexing, `/llms.txt`, and AIDoc endpoints

Each runs with: `cd example/blog && go run .`

---

## More examples

A blog with Go-defined content types:

```bash
git clone https://github.com/smeldr/core
cd core/example/blog
go run .
# open http://localhost:8080
```
