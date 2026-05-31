package smeldr

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// — Test helpers ——————————————————————————————————————————————————————————

// testFeedPost is a content type used exclusively in feed tests.
type testFeedPost struct {
	Node
	Title       string `smeldr:"required"`
	Description string
	Author      string
	ImageURL    string
	Tags        []string
}

// seedFeedPost creates and saves a testFeedPost with the given status.
// Published posts have PublishedAt set to now.
func seedFeedPost(t *testing.T, repo Repository[*testFeedPost], title string, status Status) *testFeedPost {
	t.Helper()
	n := Node{ID: NewID(), Slug: GenerateSlug(title), Status: status}
	if status == Published {
		n.PublishedAt = time.Now().UTC()
	}
	p := &testFeedPost{Node: n, Title: title, Description: "Desc: " + title, Author: "Feed Author", Tags: []string{"tech", "forge"}}
	if err := repo.Save(context.Background(), p); err != nil {
		t.Fatalf("seedFeedPost: %v", err)
	}
	return p
}

// feedHeadFunc returns a Head built from the testFeedPost's fields.
func feedHeadFunc(_ Context, p *testFeedPost) Head {
	return Head{
		Title:       p.Title,
		Description: p.Description,
		Author:      p.Author,
		Tags:        p.Tags,
		Image:       Image{URL: p.ImageURL},
		Canonical:   "",
	}
}

// testFeedCtx returns a background Context for feed test helpers.
func testFeedCtx() Context { return NewTestContext(GuestUser) }

// — TestFeedOption ————————————————————————————————————————————————————————

func TestFeedOption(t *testing.T) {
	t.Run("feed_sets_config", func(t *testing.T) {
		opt := Feed(FeedConfig{Title: "My Blog", Language: "fr"})
		fo, ok := opt.(feedOption)
		if !ok {
			t.Fatalf("Feed() returned %T, want feedOption", opt)
		}
		if fo.cfg.Title != "My Blog" {
			t.Errorf("Title = %q, want %q", fo.cfg.Title, "My Blog")
		}
		if fo.cfg.Language != "fr" {
			t.Errorf("Language = %q, want %q", fo.cfg.Language, "fr")
		}
	})

	t.Run("feed_applied_to_module", func(t *testing.T) {
		repo := NewMemoryRepo[*testFeedPost]()
		m := NewModule((*testFeedPost)(nil), Repo(repo), Feed(FeedConfig{Title: "Blog"}))
		if m.feedCfg == nil {
			t.Fatal("m.feedCfg should be non-nil after Feed() option")
		}
		if m.feedCfg.Title != "Blog" {
			t.Errorf("feedCfg.Title = %q, want %q", m.feedCfg.Title, "Blog")
		}
	})

	t.Run("feed_disabled_clears_config", func(t *testing.T) {
		repo := NewMemoryRepo[*testFeedPost]()
		m := NewModule((*testFeedPost)(nil), Repo(repo), Feed(FeedConfig{Title: "Blog"}), DisableFeed())
		if m.feedCfg != nil {
			t.Error("m.feedCfg should be nil after DisableFeed() option")
		}
	})

	t.Run("feed_disabled_option_type", func(t *testing.T) {
		opt := DisableFeed()
		if _, ok := opt.(feedDisabledOption); !ok {
			t.Fatalf("DisableFeed() returned %T, want feedDisabledOption", opt)
		}
	})

	t.Run("default_feedcfg_nil_without_option", func(t *testing.T) {
		repo := NewMemoryRepo[*testFeedPost]()
		m := NewModule((*testFeedPost)(nil), Repo(repo))
		if m.feedCfg != nil {
			t.Error("m.feedCfg should be nil when no Feed() option given")
		}
	})
}

// — TestFeedEndpoint ——————————————————————————————————————————————————————

func TestFeedEndpoint(t *testing.T) {
	repo := NewMemoryRepo[*testFeedPost]()
	seedFeedPost(t, repo, "Hello Forge", Published)

	store := NewFeedStore("example.com", "https://example.com")
	m := NewModule((*testFeedPost)(nil),
		Repo(repo),
		At("/posts"),
		Feed(FeedConfig{Title: "Blog"}),
		HeadFunc(feedHeadFunc),
	)
	m.setFeedStore(store, "https://example.com")
	m.regenerateFeed(testFeedCtx())

	mux := http.NewServeMux()
	m.Register(mux)

	cases := []struct {
		label string
		check func(t *testing.T, w *httptest.ResponseRecorder)
	}{
		{"status_200", func(t *testing.T, w *httptest.ResponseRecorder) {
			if w.Code != http.StatusOK {
				t.Errorf("status = %d, want 200", w.Code)
			}
		}},
		{"content_type_rss", func(t *testing.T, w *httptest.ResponseRecorder) {
			ct := w.Header().Get("Content-Type")
			if !strings.Contains(ct, "application/rss+xml") {
				t.Errorf("Content-Type = %q, want application/rss+xml", ct)
			}
		}},
		{"rss_root_element", func(t *testing.T, w *httptest.ResponseRecorder) {
			if !strings.Contains(w.Body.String(), `version="2.0"`) {
				t.Errorf("body missing RSS version attribute:\n%s", w.Body.String())
			}
		}},
		{"xml_declaration", func(t *testing.T, w *httptest.ResponseRecorder) {
			if !strings.HasPrefix(w.Body.String(), "<?xml") {
				t.Errorf("body does not start with XML declaration:\n%s", w.Body.String())
			}
		}},
	}

	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/posts/feed.xml", nil)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			tc.check(t, w)
		})
	}
}

// — TestFeedContainsPublishedOnly ————————————————————————————————————————

func TestFeedContainsPublishedOnly(t *testing.T) {
	repo := NewMemoryRepo[*testFeedPost]()
	pub := seedFeedPost(t, repo, "Published Post", Published)
	_ = seedFeedPost(t, repo, "Draft Post Title", Draft)

	store := NewFeedStore("example.com", "https://example.com")
	m := NewModule((*testFeedPost)(nil),
		Repo(repo),
		At("/posts"),
		Feed(FeedConfig{}),
		HeadFunc(feedHeadFunc),
	)
	m.setFeedStore(store, "https://example.com")
	m.regenerateFeed(testFeedCtx())

	r := httptest.NewRequest(http.MethodGet, "/posts/feed.xml", nil)
	w := httptest.NewRecorder()
	store.ModuleHandler("/posts").ServeHTTP(w, r)
	body := w.Body.String()

	t.Run("published_present", func(t *testing.T) {
		if !strings.Contains(body, pub.Title) {
			t.Errorf("body missing published title %q:\n%s", pub.Title, body)
		}
	})

	t.Run("draft_absent", func(t *testing.T) {
		if strings.Contains(body, "Draft Post Title") {
			t.Errorf("body contains draft item title:\n%s", body)
		}
	})
}

// — TestFeedFields ————————————————————————————————————————————————————————

func TestFeedFields(t *testing.T) {
	repo := NewMemoryRepo[*testFeedPost]()
	p := seedFeedPost(t, repo, "Field Test Post", Published)
	p.Author = "Jane Doe"
	p.Tags = []string{"go", "cms"}
	if err := repo.Save(context.Background(), p); err != nil {
		t.Fatalf("save: %v", err)
	}

	store := NewFeedStore("example.com", "https://example.com")
	m := NewModule((*testFeedPost)(nil),
		Repo(repo),
		At("/articles"),
		Feed(FeedConfig{Title: "Articles", Description: "Tech articles", Language: "en"}),
		HeadFunc(feedHeadFunc),
	)
	m.setFeedStore(store, "https://example.com")
	m.regenerateFeed(testFeedCtx())

	r := httptest.NewRequest(http.MethodGet, "/articles/feed.xml", nil)
	w := httptest.NewRecorder()
	store.ModuleHandler("/articles").ServeHTTP(w, r)
	body := w.Body.String()

	cases := []struct {
		label string
		want  string
	}{
		{"title_in_item", p.Title},
		{"author_in_item", "Jane Doe"},
		{"description_in_channel", "Tech articles"},
		{"language_in_channel", "<language>en</language>"},
		{"category_go", "<category>go</category>"},
		{"category_cms", "<category>cms</category>"},
		{"guid_element", "<guid isPermaLink=\"true\">"},
		{"link_element", "<link>https://example.com/articles/"},
		{"channel_title", "<title>Articles</title>"},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			if !strings.Contains(body, tc.want) {
				t.Errorf("body missing %q:\n%s", tc.want, body)
			}
		})
	}
}

// — TestFeedEnclosure ————————————————————————————————————————————————————

func TestFeedEnclosure(t *testing.T) {
	repo := NewMemoryRepo[*testFeedPost]()
	p := seedFeedPost(t, repo, "Enclosure Post", Published)
	p.ImageURL = "https://example.com/img/hero.jpg"
	if err := repo.Save(context.Background(), p); err != nil {
		t.Fatalf("save: %v", err)
	}

	store := NewFeedStore("example.com", "https://example.com")
	m := NewModule((*testFeedPost)(nil),
		Repo(repo),
		At("/posts"),
		Feed(FeedConfig{}),
		HeadFunc(feedHeadFunc),
	)
	m.setFeedStore(store, "https://example.com")
	m.regenerateFeed(testFeedCtx())

	r := httptest.NewRequest(http.MethodGet, "/posts/feed.xml", nil)
	w := httptest.NewRecorder()
	store.ModuleHandler("/posts").ServeHTTP(w, r)
	body := w.Body.String()

	cases := []struct {
		label string
		want  string
	}{
		{"enclosure_element", "<enclosure"},
		{"enclosure_url", `url="https://example.com/img/hero.jpg"`},
		{"enclosure_mime_jpeg", `type="image/jpeg"`},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			if !strings.Contains(body, tc.want) {
				t.Errorf("body missing %q:\n%s", tc.want, body)
			}
		})
	}
}

// — TestFeedIndexEndpoint ————————————————————————————————————————————————

func TestFeedIndexEndpoint(t *testing.T) {
	repoA := NewMemoryRepo[*testFeedPost]()
	repoB := NewMemoryRepo[*testFeedPost]()
	pa := seedFeedPost(t, repoA, "Post From Posts", Published)
	pb := seedFeedPost(t, repoB, "Post From News", Published)
	_ = seedFeedPost(t, repoA, "Draft In Posts", Draft)

	store := NewFeedStore("example.com", "https://example.com")

	mA := NewModule((*testFeedPost)(nil),
		Repo(repoA),
		At("/posts"),
		Feed(FeedConfig{Title: "Posts"}),
		HeadFunc(feedHeadFunc),
	)
	mA.setFeedStore(store, "https://example.com")
	mA.regenerateFeed(testFeedCtx())

	mB := NewModule((*testFeedPost)(nil),
		Repo(repoB),
		At("/news"),
		Feed(FeedConfig{Title: "News"}),
		HeadFunc(feedHeadFunc),
	)
	mB.setFeedStore(store, "https://example.com")
	mB.regenerateFeed(testFeedCtx())

	r := httptest.NewRequest(http.MethodGet, "/feed.xml", nil)
	w := httptest.NewRecorder()
	store.IndexHandler().ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()

	cases := []struct {
		label string
		want  string
	}{
		{"post_from_posts", pa.Title},
		{"post_from_news", pb.Title},
		{"draft_absent", ""},
		{"rss_version", `version="2.0"`},
		{"content_type", "application/rss+xml"},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			if tc.label == "draft_absent" {
				if strings.Contains(body, "Draft In Posts") {
					t.Errorf("index feed contains draft item:\n%s", body)
				}
				return
			}
			if tc.label == "content_type" {
				ct := w.Header().Get("Content-Type")
				if !strings.Contains(ct, tc.want) {
					t.Errorf("Content-Type = %q, want %q", ct, tc.want)
				}
				return
			}
			if !strings.Contains(body, tc.want) {
				t.Errorf("body missing %q:\n%s", tc.want, body)
			}
		})
	}
}

// — TestFeedDefaultTitle —————————————————————————————————————————————————

func TestFeedDefaultTitle(t *testing.T) {
	cases := []struct {
		prefix string
		want   string
	}{
		{"/posts", "Posts"},
		{"/articles", "Articles"},
		{"/news", "News"},
	}
	for _, tc := range cases {
		t.Run(tc.prefix, func(t *testing.T) {
			got := capitalisePrefixTitle(tc.prefix)
			if got != tc.want {
				t.Errorf("capitalisePrefixTitle(%q) = %q, want %q", tc.prefix, got, tc.want)
			}
		})
	}

	// Verify the default appears in served channel when FeedConfig.Title is empty.
	t.Run("empty_title_uses_prefix", func(t *testing.T) {
		repo := NewMemoryRepo[*testFeedPost]()
		seedFeedPost(t, repo, "Some Post", Published)
		store := NewFeedStore("example.com", "https://example.com")
		m := NewModule((*testFeedPost)(nil),
			Repo(repo),
			At("/posts"),
			Feed(FeedConfig{}),
			HeadFunc(feedHeadFunc),
		)
		m.setFeedStore(store, "https://example.com")
		m.regenerateFeed(testFeedCtx())

		r := httptest.NewRequest(http.MethodGet, "/posts/feed.xml", nil)
		w := httptest.NewRecorder()
		store.ModuleHandler("/posts").ServeHTTP(w, r)

		if !strings.Contains(w.Body.String(), "<title>Posts</title>") {
			t.Errorf("channel title should default to 'Posts':\n%s", w.Body.String())
		}
	})
}
