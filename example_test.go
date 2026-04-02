package forge

// example_test.go — compile-verified README examples.
//
// Each Example function is a direct extract of a primary README code example.
// No output comments are required — the goal is compile + non-panic execution.
// All examples call app.Handler() (non-blocking) instead of app.Run().
//
// The two-step pattern (NewModule[T] + app.Content(m)) is used throughout
// because it is the idiomatic path: it preserves type safety and ensures the
// full AI/feed/sitemap wiring in App.Content is exercised.

import "time"

// examplePost is the minimal content type used by all examples in this file.
// It implements Headable (for Head auto-detection, Amendment A28) and
// Markdownable (for LLMsTxtFull text/markdown content negotiation).
type examplePost struct {
	Node
	Title string `forge:"required" json:"title"`
	Body  string `json:"body"`
}

func (p *examplePost) Head() Head {
	return Head{
		Title:       p.Title,
		Description: Excerpt(p.Body, 160),
		Canonical:   URL("/posts/", p.Slug),
	}
}

func (p *examplePost) Markdown() string { return p.Body }

// ExampleNewModule demonstrates creating a typed content module and registering
// it with an App. This is the idiomatic two-step path: NewModule[T] preserves
// full type safety and ensures all App-level wiring (sitemap, feed, AI) runs.
func ExampleNewModule() {
	secret := []byte("example-secret-key-32-bytes!!!!!")

	repo := NewMemoryRepo[*examplePost]()
	m := NewModule(&examplePost{},
		At("/posts"),
		Repo(repo),
		Auth(
			Read(Guest),
			Write(Author),
			Delete(Editor),
		),
		Cache(5*time.Minute),
		AIIndex(LLMsTxt, AIDoc),
	)

	app := New(Config{
		BaseURL: "https://example.com",
		Secret:  secret,
	})
	app.Content(m)
	_ = app.Handler()
}

// ExampleAuth demonstrates declaring role-based access for read, write, and
// delete operations on a content module.
func ExampleAuth() {
	repo := NewMemoryRepo[*examplePost]()
	m := NewModule(&examplePost{},
		At("/posts"),
		Repo(repo),
		Auth(
			Read(Guest),
			Write(Author),
			Delete(Editor),
		),
	)

	app := New(Config{
		BaseURL: "https://example.com",
		Secret:  []byte("example-secret-key-32-bytes!!!!!"),
	})
	app.Content(m)
	_ = app.Handler()
}

// ExampleAuthenticate demonstrates wiring bearer token and cookie session auth
// via AnyAuth so that both APIs and browser clients are supported. The first
// matching auth method wins on each request.
func ExampleAuthenticate() {
	const secretStr = "example-secret-key-32-bytes!!!!!"
	secretBytes := []byte(secretStr)

	app := New(Config{
		BaseURL: "https://example.com",
		Secret:  secretBytes,
	})
	app.Use(Authenticate(AnyAuth(
		BearerHMAC(secretStr),
		CookieSession("session", secretStr),
	)))
	_ = app.Handler()
}

// ExampleAIIndex demonstrates enabling AI indexing on a content module.
// LLMsTxt registers the module in /llms.txt, LLMsTxtFull produces a full
// markdown corpus at /llms-full.txt, and AIDoc adds /{slug}/aidoc endpoints.
func ExampleAIIndex() {
	repo := NewMemoryRepo[*examplePost]()
	m := NewModule(&examplePost{},
		At("/posts"),
		Repo(repo),
		AIIndex(LLMsTxt, LLMsTxtFull, AIDoc),
	)

	app := New(Config{
		BaseURL: "https://example.com",
		Secret:  []byte("example-secret-key-32-bytes!!!!!"),
	})
	app.Content(m)
	_ = app.Handler()
}

// ExampleSocial demonstrates enabling Open Graph and Twitter Card metadata on
// a content module. Head fields (Title, Description, Image) are sourced from
// the content type's Head() method automatically (Amendment A28).
func ExampleSocial() {
	repo := NewMemoryRepo[*examplePost]()
	m := NewModule(&examplePost{},
		At("/posts"),
		Repo(repo),
		Social(OpenGraph, TwitterCard),
	)

	app := New(Config{
		BaseURL: "https://example.com",
		Secret:  []byte("example-secret-key-32-bytes!!!!!"),
	})
	app.Content(m)
	_ = app.Handler()
}

// ExampleOn demonstrates registering a typed signal handler on a content
// module. The handler fires after a post is published and receives the
// full forge.Context and the typed item.
func ExampleOn() {
	repo := NewMemoryRepo[*examplePost]()
	m := NewModule(&examplePost{},
		At("/posts"),
		Repo(repo),
		On(AfterPublish, func(_ Context, p *examplePost) error {
			_ = p.Title // access typed fields
			return nil
		}),
	)

	app := New(Config{
		BaseURL: "https://example.com",
		Secret:  []byte("example-secret-key-32-bytes!!!!!"),
	})
	app.Content(m)
	_ = app.Handler()
}

// ExampleRobotsConfig demonstrates configuring robots.txt with an explicit
// disallow list, automatic sitemap inclusion, and an AI crawler policy of
// AskFirst — which disallows known AI training crawlers by name.
func ExampleRobotsConfig() {
	app := New(Config{
		BaseURL: "https://example.com",
		Secret:  []byte("example-secret-key-32-bytes!!!!!"),
	})
	app.SEO(&RobotsConfig{
		Disallow:  []string{"/admin"},
		Sitemaps:  true,
		AIScraper: AskFirst,
	})
	_ = app.Handler()
}

// ExampleOGDefaults demonstrates setting app-level Open Graph and Twitter Card
// fallback values. These are merged into every page's Head by forge:head when
// the content item does not supply its own image or Twitter creator handle.
func ExampleOGDefaults() {
	app := New(Config{
		BaseURL: "https://example.com",
		Secret:  []byte("example-secret-key-32-bytes!!!!!"),
	})
	app.SEO(&OGDefaults{
		Image:          Image{URL: "https://example.com/og-default.png", Width: 1200, Height: 630},
		TwitterSite:    "@mycompany",
		TwitterCreator: "@editor",
	})
	_ = app.Handler()
}

// ExampleAppSchema demonstrates registering app-level JSON-LD structured data.
// The block is emitted automatically by forge:head on every page.
func ExampleAppSchema() {
	app := New(Config{
		BaseURL: "https://example.com",
		Secret:  []byte("example-secret-key-32-bytes!!!!!"),
	})
	app.SEO(&AppSchema{
		Type: "Organization",
		Name: "Acme Corp",
		URL:  "https://example.com",
		Logo: "https://example.com/logo.png",
	})
	_ = app.Handler()
}
