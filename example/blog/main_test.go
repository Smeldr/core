package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"smeldr.dev/core"
	forgemcp "smeldr.dev/mcp"
)

// ---------------------------------------------------------------------------
// Test infrastructure
// ---------------------------------------------------------------------------

// buildTestApp constructs a complete in-memory test server using httptest.
// It returns the app, a running test server, and pre-issued author/editor tokens.
// Use this for all tests except scheduler and audit (which need app.Run()).
func buildTestApp(t *testing.T) (*smeldr.App, *httptest.Server, string, string) {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "test-blog.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if err := createSchema(db); err != nil {
		t.Fatalf("createSchema: %v", err)
	}

	const testSecret = "test-secret-for-blog-tests"
	tokenStore := smeldr.NewTokenStore(db, testSecret)
	repo := smeldr.NewSQLRepo[*Post](db)

	m := smeldr.NewModule((*Post)(nil),
		smeldr.At("/posts"),
		smeldr.Repo(repo),
		smeldr.Auth(smeldr.Read(smeldr.Guest), smeldr.Write(smeldr.Author)),
		smeldr.MCP(smeldr.MCPRead, smeldr.MCPWrite),
		smeldr.SitemapConfig{},
		smeldr.Social(smeldr.OpenGraph, smeldr.TwitterCard),
		smeldr.Feed(smeldr.FeedConfig{Title: "Test Blog"}),
		smeldr.AIIndex(smeldr.LLMsTxt, smeldr.LLMsTxtFull, smeldr.AIDoc),
	)

	app := smeldr.New(smeldr.MustConfig(smeldr.Config{
		BaseURL:    "http://localhost",
		Secret:     []byte(testSecret),
		DB:         db,
		TokenStore: tokenStore,
	}))
	app.Content(m)

	mcpSrv := forgemcp.New(app)
	app.Handle("GET /mcp", mcpSrv.Handler())
	app.Handle("POST /mcp/message", mcpSrv.Handler())

	ctx := context.Background()
	authorToken, err := tokenStore.Create(ctx, "test-author", "author", 24*time.Hour)
	if err != nil {
		t.Fatalf("create author token: %v", err)
	}
	editorToken, err := tokenStore.Create(ctx, "test-editor", "editor", 24*time.Hour)
	if err != nil {
		t.Fatalf("create editor token: %v", err)
	}

	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)

	return app, srv, authorToken, editorToken
}

// buildFullTestServer starts a real app.Run() server on a fixed port.
// Use only for tests that require the signal bus (scheduler, audit).
// Only one TestBlogFullServer test should call this — tests within it share the server.
// Returns baseURL, authorToken, editorToken, and the slug of a pre-seeded
// scheduled post that the scheduler's initial tick should have published.
func buildFullTestServer(t *testing.T) (baseURL, authorToken, editorToken, scheduledSlug string) {
	t.Helper()

	// Use in-memory SQLite to avoid Windows file-lock issues: app.Run runs in a
	// goroutine that outlives the test, which would hold the DB file open across
	// t.TempDir() cleanup. MaxOpenConns=1 ensures all connections see the same DB.
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	db.SetMaxOpenConns(1)

	if err := createSchema(db); err != nil {
		t.Fatalf("createSchema: %v", err)
	}

	const testSecret = "full-server-test-secret"
	tokenStore := smeldr.NewTokenStore(db, testSecret)
	repo := smeldr.NewSQLRepo[*Post](db)

	// Pre-seed a scheduled post into the DB before the server starts.
	// The scheduler's initial tick (inside app.Run, before ListenAndServe)
	// will publish it, so by the time the server accepts requests the post
	// is already Published and verifiable via HTTP.
	past := time.Now().UTC().Add(-5 * time.Second)
	seeded := &Post{
		Node: smeldr.Node{
			ID:          smeldr.NewID(),
			Slug:        "pre-scheduled-test",
			Status:      smeldr.Scheduled,
			ScheduledAt: &past,
		},
		Title: "Scheduler Test Post",
		Body:  "This post was scheduled in the past and should auto-publish.",
	}
	if err := repo.Save(context.Background(), seeded); err != nil {
		t.Fatalf("seed scheduled post: %v", err)
	}
	scheduledSlug = seeded.Slug

	m := smeldr.NewModule((*Post)(nil),
		smeldr.At("/posts"),
		smeldr.Repo(repo),
		smeldr.Auth(smeldr.Read(smeldr.Guest), smeldr.Write(smeldr.Author)),
		smeldr.MCP(smeldr.MCPRead, smeldr.MCPWrite),
		smeldr.SitemapConfig{},
		smeldr.Feed(smeldr.FeedConfig{Title: "Full Test Blog"}),
		smeldr.AIIndex(smeldr.LLMsTxt, smeldr.LLMsTxtFull, smeldr.AIDoc),
	)

	app := smeldr.New(smeldr.MustConfig(smeldr.Config{
		BaseURL:    "http://localhost:19991",
		Secret:     []byte(testSecret),
		DB:         db,
		TokenStore: tokenStore,
	}))
	app.Content(m)
	app.Audit(smeldr.NewAuditStore(db))
	app.Health()

	ctx := context.Background()
	authorToken, err = tokenStore.Create(ctx, "full-author", "author", 24*time.Hour)
	if err != nil {
		t.Fatalf("create author token: %v", err)
	}
	editorToken, err = tokenStore.Create(ctx, "full-editor", "editor", 24*time.Hour)
	if err != nil {
		t.Fatalf("create editor token: %v", err)
	}

	baseURL = "http://localhost:19991"
	go func() { _ = app.Run(":19991") }()

	// Poll until the server is ready.
	client := &http.Client{Timeout: 500 * time.Millisecond}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get(baseURL + "/_health")
		if err == nil && resp.StatusCode == http.StatusOK {
			resp.Body.Close()
			return
		}
		if resp != nil {
			resp.Body.Close()
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("full server did not become ready within 5 seconds")
	return
}

// mustPost creates a post and returns its slug. Fails the test on any error.
func mustPost(t *testing.T, srvURL, token, title, body string) string {
	t.Helper()
	payload := map[string]any{"Title": title, "Body": body, "Status": "draft"}
	b, _ := json.Marshal(payload)
	req, _ := http.NewRequest(http.MethodPost, srvURL+"/posts", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /posts: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		data, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST /posts: status %d; body: %s", resp.StatusCode, data)
	}
	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode POST /posts response: %v", err)
	}
	slug, _ := result["Slug"].(string)
	if slug == "" {
		t.Fatal("POST /posts: Slug missing in response")
	}
	return slug
}

// mustPublish transitions a post to Published via PUT /posts/{slug}.
func mustPublish(t *testing.T, srvURL, token, slug string) {
	t.Helper()
	// GET the item first, then set Status=published and PUT it back.
	req, _ := http.NewRequest(http.MethodGet, srvURL+"/posts/"+slug, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /posts/%s: %v", slug, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /posts/%s: status %d", slug, resp.StatusCode)
	}
	var item map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&item); err != nil {
		t.Fatalf("decode GET /posts/%s: %v", slug, err)
	}

	item["Status"] = "published"
	b, _ := json.Marshal(item)
	putReq, _ := http.NewRequest(http.MethodPut, srvURL+"/posts/"+slug, bytes.NewReader(b))
	putReq.Header.Set("Content-Type", "application/json")
	putReq.Header.Set("Authorization", "Bearer "+token)
	putResp, err := http.DefaultClient.Do(putReq)
	if err != nil {
		t.Fatalf("PUT /posts/%s: %v", slug, err)
	}
	defer putResp.Body.Close()
	if putResp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(putResp.Body)
		t.Fatalf("PUT /posts/%s (publish): status %d; body: %s", slug, putResp.StatusCode, data)
	}
}

// getJSON issues a GET and decodes the JSON response.
func getJSON(t *testing.T, srvURL, token, path string) map[string]any {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, srvURL+path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET %s: status %d; body: %s", path, resp.StatusCode, data)
	}
	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode GET %s: %v", path, err)
	}
	return result
}

// ---------------------------------------------------------------------------
// TestBlog — tests that use httptest.NewServer (no scheduler, no audit)
// ---------------------------------------------------------------------------

func TestBlog(t *testing.T) {
	_, srv, authorToken, _ := buildTestApp(t)
	srvURL := srv.URL

	t.Run("storage/createAndRetrieve", func(t *testing.T) {
		slug := mustPost(t, srvURL, authorToken, "Hello Storage", "Body that is long enough to pass validation.")
		item := getJSON(t, srvURL, authorToken, "/posts/"+slug)
		if got, _ := item["Title"].(string); got != "Hello Storage" {
			t.Errorf("Title = %q; want %q", got, "Hello Storage")
		}
	})

	t.Run("storage/draftIsHidden", func(t *testing.T) {
		slug := mustPost(t, srvURL, authorToken, "Hidden Draft", "This draft should not be visible to guests.")
		// Guest (no token) should get 404.
		req, _ := http.NewRequest(http.MethodGet, srvURL+"/posts/"+slug, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET /posts/%s: %v", slug, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("guest GET draft: status %d; want 404", resp.StatusCode)
		}
	})

	t.Run("auth/unauthenticatedWriteReturns403", func(t *testing.T) {
		// Forge: anonymous requests are treated as Guest role (level 10).
		// Write requires Author (level 20), so the response is 403 Forbidden —
		// not 401 Unauthorized, which would indicate an unknown identity.
		payload := map[string]any{"Title": "Unauth Post", "Body": "Should be rejected by auth middleware."}
		b, _ := json.Marshal(payload)
		resp, err := http.Post(srvURL+"/posts", "application/json", bytes.NewReader(b))
		if err != nil {
			t.Fatalf("POST /posts: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("unauthenticated POST: status %d; want 403", resp.StatusCode)
		}
	})

	t.Run("auth/authorTokenCanCreate", func(t *testing.T) {
		slug := mustPost(t, srvURL, authorToken, "Auth Post", "Created with a valid author bearer token.")
		if slug == "" {
			t.Error("expected non-empty slug")
		}
	})

	t.Run("auth/guestCanReadPublished", func(t *testing.T) {
		slug := mustPost(t, srvURL, authorToken, "Public Read", "This post will be published and readable by guests.")
		mustPublish(t, srvURL, authorToken, slug)
		// Guest (no token) should see 200.
		resp, err := http.Get(srvURL + "/posts/" + slug)
		if err != nil {
			t.Fatalf("GET /posts/%s: %v", slug, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("guest GET published: status %d; want 200", resp.StatusCode)
		}
	})

	t.Run("lifecycle/draftToPublished", func(t *testing.T) {
		slug := mustPost(t, srvURL, authorToken, "Lifecycle Post", "Testing the draft to published transition path.")
		// Confirm draft (author sees it).
		item := getJSON(t, srvURL, authorToken, "/posts/"+slug)
		if got, _ := item["Status"].(string); got != "draft" {
			t.Errorf("initial Status = %q; want draft", got)
		}
		// Publish and confirm.
		mustPublish(t, srvURL, authorToken, slug)
		item = getJSON(t, srvURL, authorToken, "/posts/"+slug)
		if got, _ := item["Status"].(string); got != "published" {
			t.Errorf("after publish Status = %q; want published", got)
		}
	})

	t.Run("lifecycle/publishedAppearsInList", func(t *testing.T) {
		slug := mustPost(t, srvURL, authorToken, "List Post", "This post should appear in the public list after publishing.")
		mustPublish(t, srvURL, authorToken, slug)
		// GET /posts (guest) should return a JSON array containing the slug.
		req, _ := http.NewRequest(http.MethodGet, srvURL+"/posts", nil)
		req.Header.Set("Accept", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET /posts: %v", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if !strings.Contains(string(body), slug) {
			t.Errorf("GET /posts: slug %q not found in response", slug)
		}
	})

	t.Run("feeds/feedXML", func(t *testing.T) {
		resp, err := http.Get(srvURL + "/posts/feed.xml")
		if err != nil {
			t.Fatalf("GET /posts/feed.xml: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("feed.xml: status %d; want 200", resp.StatusCode)
		}
		ct := resp.Header.Get("Content-Type")
		if !strings.Contains(ct, "xml") {
			t.Errorf("feed.xml Content-Type = %q; want xml", ct)
		}
	})

	t.Run("feeds/sitemap", func(t *testing.T) {
		resp, err := http.Get(srvURL + "/sitemap.xml")
		if err != nil {
			t.Fatalf("GET /sitemap.xml: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("sitemap.xml: status %d; want 200", resp.StatusCode)
		}
	})

	t.Run("ai/llmsTxt", func(t *testing.T) {
		// Publish a post with a known title before checking llms.txt.
		slug := mustPost(t, srvURL, authorToken, "LLMs Txt Test Post", "Content for the AI index test; must be long enough.")
		mustPublish(t, srvURL, authorToken, slug)

		// Forge: the AI index is rebuilt by a 2-second debouncer after each
		// publish. Poll /llms.txt until the slug appears or the deadline passes.
		deadline := time.Now().Add(5 * time.Second)
		var body string
		for time.Now().Before(deadline) {
			resp, err := http.Get(srvURL + "/llms.txt")
			if err != nil {
				t.Fatalf("GET /llms.txt: %v", err)
			}
			raw, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("llms.txt: status %d; want 200", resp.StatusCode)
			}
			body = string(raw)
			if strings.Contains(body, slug) {
				return
			}
			time.Sleep(250 * time.Millisecond)
		}
		t.Errorf("llms.txt: slug %q not found after 5s; body: %s", slug, body)
	})

	t.Run("ai/llmsFullTxt", func(t *testing.T) {
		resp, err := http.Get(srvURL + "/llms-full.txt")
		if err != nil {
			t.Fatalf("GET /llms-full.txt: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("llms-full.txt: status %d; want 200", resp.StatusCode)
		}
	})

	t.Run("mcp/endpointReachable", func(t *testing.T) {
		resp, err := http.Get(srvURL + "/mcp")
		if err != nil {
			t.Fatalf("GET /mcp: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET /mcp: status %d; want 200", resp.StatusCode)
		}
	})
}

// ---------------------------------------------------------------------------
// TestBlogSignal — AfterPublish signal fires in httptest.NewServer context
// ---------------------------------------------------------------------------

func TestBlogSignal(t *testing.T) {
	// Use a dedicated app instance with a channel captured at module build time.
	// This ensures the channel reference is stable and not shared with other tests.
	dbPath := filepath.Join(t.TempDir(), "signal-blog.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if err := createSchema(db); err != nil {
		t.Fatalf("createSchema: %v", err)
	}

	const testSecret = "signal-test-secret"
	tokenStore := smeldr.NewTokenStore(db, testSecret)
	repo := smeldr.NewSQLRepo[*Post](db)

	published := make(chan string, 1) // receives slug when AfterPublish fires

	m := smeldr.NewModule((*Post)(nil),
		smeldr.At("/posts"),
		smeldr.Repo(repo),
		smeldr.Auth(smeldr.Read(smeldr.Guest), smeldr.Write(smeldr.Author)),
		smeldr.On(smeldr.AfterPublish, func(_ smeldr.Context, p *Post) error {
			published <- p.Slug
			return nil
		}),
	)

	app := smeldr.New(smeldr.MustConfig(smeldr.Config{
		BaseURL:    "http://localhost",
		Secret:     []byte(testSecret),
		DB:         db,
		TokenStore: tokenStore,
	}))
	app.Content(m)

	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)

	ctx := context.Background()
	authorToken, err := tokenStore.Create(ctx, "signal-author", "author", 24*time.Hour)
	if err != nil {
		t.Fatalf("create author token: %v", err)
	}

	slug := mustPost(t, srv.URL, authorToken, "Signal Post", "This post triggers an AfterPublish signal.")
	mustPublish(t, srv.URL, authorToken, slug)

	select {
	case got := <-published:
		if got != slug {
			t.Errorf("AfterPublish: got slug %q; want %q", got, slug)
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("AfterPublish: signal not received within 500ms")
	}
}

// ---------------------------------------------------------------------------
// TestBlogFullServer — scheduler and audit tests (require app.Run())
// ---------------------------------------------------------------------------

func TestBlogFullServer(t *testing.T) {
	// Forge: buildFullTestServer pre-seeds a scheduled post into the DB before
	// the server starts. The scheduler's initial tick (inside app.Run) publishes
	// it before ListenAndServe begins accepting requests, so the post is already
	// Published by the time this test function receives control.
	baseURL, authorToken, editorToken, scheduledSlug := buildFullTestServer(t)

	t.Run("scheduler/publishesOverduePost", func(t *testing.T) {
		// Poll to confirm the pre-seeded scheduled post is now Published.
		// The scheduler's initial tick ran before the server accepted requests,
		// so 3 seconds is more than enough headroom.
		deadline := time.Now().Add(3 * time.Second)
		client := &http.Client{}
		for time.Now().Before(deadline) {
			getReq, _ := http.NewRequest(http.MethodGet, baseURL+"/posts/"+scheduledSlug, nil)
			getReq.Header.Set("Authorization", "Bearer "+authorToken)
			getResp, err := client.Do(getReq)
			if err == nil {
				var item map[string]any
				json.NewDecoder(getResp.Body).Decode(&item)
				getResp.Body.Close()
				if status, _ := item["Status"].(string); status == "published" {
					return // success
				}
			}
			time.Sleep(200 * time.Millisecond)
		}
		t.Error("scheduler: post was not published within 3 seconds")
	})

	t.Run("audit/recordedOnPublish", func(t *testing.T) {
		slug := mustPost(t, baseURL, authorToken, "Audit Test Post", "This post will generate an audit record when published.")
		mustPublish(t, baseURL, authorToken, slug)

		// Wait briefly for async audit write.
		time.Sleep(100 * time.Millisecond)

		// GET /_audit requires Editor+ token.
		req, _ := http.NewRequest(http.MethodGet, baseURL+"/_audit?slug="+slug, nil)
		req.Header.Set("Authorization", "Bearer "+editorToken)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET /_audit: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			data, _ := io.ReadAll(resp.Body)
			t.Fatalf("GET /_audit: status %d; body: %s", resp.StatusCode, data)
		}
		body, _ := io.ReadAll(resp.Body)
		if !strings.Contains(string(body), slug) {
			t.Errorf("audit log: slug %q not found in audit response", slug)
		}
	})
}
