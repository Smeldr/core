package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	_ "modernc.org/sqlite"
	smeldr "smeldr.dev/core"
)

// ---------------------------------------------------------------------------
// Test infrastructure
// ---------------------------------------------------------------------------

// testServer holds all components of a running in-process test server.
type testServer struct {
	URL        string
	TokenStore *smeldr.TokenStore // nil when EnableTokens=false
	Result     ServerResult
	db         *sql.DB
}

// buildTestServer starts an in-process test server from the given config.
// Uses :memory: SQLite with MaxOpenConns=1 to avoid Windows file-lock issues.
// Calls the real buildApp, so tests exercise the exact same wiring as main().
func buildTestServer(t *testing.T, cfg ServerConfig) *testServer {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	db.SetMaxOpenConns(1)
	result, err := buildApp(cfg, db)
	if err != nil {
		db.Close()
		t.Fatalf("buildApp: %v", err)
	}
	srv := httptest.NewServer(result.App.Handler())
	t.Cleanup(func() {
		srv.Close()
		result.StopAll()
		db.Close()
	})
	return &testServer{
		URL:        srv.URL,
		TokenStore: result.TokenStore,
		Result:     result,
		db:         db,
	}
}

// createToken creates a bearer token via the TokenStore and returns the raw token string.
func createToken(t *testing.T, ts *testServer, name, role string) string {
	t.Helper()
	if ts.TokenStore == nil {
		t.Fatal("createToken: TokenStore is nil (EnableTokens=false)")
	}
	tok, err := ts.TokenStore.Create(context.Background(), name, role, 24*time.Hour)
	if err != nil {
		t.Fatalf("create token %q (%s): %v", name, role, err)
	}
	return tok
}

// getStatus issues a GET and returns the HTTP status code.
func getStatus(t *testing.T, srvURL, token, path string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, srvURL+path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

// mcpCall issues a JSON-RPC 2.0 call to POST /mcp/message and returns the decoded response.
func mcpCall(t *testing.T, srvURL, token, method string, params map[string]any) map[string]any {
	t.Helper()
	body := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  method,
	}
	if params != nil {
		body["params"] = params
	}
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, srvURL+"/mcp/message", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("MCP %s: %v", method, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("MCP %s: decode: %v\nbody: %s", method, err, data)
	}
	return result
}

// toolsList calls tools/list and returns all tool names.
func toolsList(t *testing.T, srvURL, token string) []string {
	t.Helper()
	resp := mcpCall(t, srvURL, token, "tools/list", nil)
	result, _ := resp["result"].(map[string]any)
	toolsRaw, _ := result["tools"].([]any)
	names := make([]string, 0, len(toolsRaw))
	for _, tr := range toolsRaw {
		tm, _ := tr.(map[string]any)
		if name, _ := tm["name"].(string); name != "" {
			names = append(names, name)
		}
	}
	return names
}

// callTool invokes a named MCP tool and returns the decoded payload from the content text field.
func callTool(t *testing.T, srvURL, token, name string, args map[string]any) map[string]any {
	t.Helper()
	resp := mcpCall(t, srvURL, token, "tools/call", map[string]any{
		"name":      name,
		"arguments": args,
	})
	if errObj, ok := resp["error"]; ok {
		t.Fatalf("tools/call %s: RPC error: %v", name, errObj)
	}
	result, _ := resp["result"].(map[string]any)
	content, _ := result["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("tools/call %s: empty content in result: %v", name, result)
	}
	item, _ := content[0].(map[string]any)
	text, _ := item["text"].(string)
	var payload map[string]any
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		t.Fatalf("tools/call %s: decode text %q: %v", name, text, err)
	}
	return payload
}

const testSecret = "test-secret-32-bytes-xxxxxxxxxxxx"

// baseConfig returns a minimal ServerConfig suitable for in-process testing.
func baseConfig() ServerConfig {
	return ServerConfig{
		Secret:       testSecret,
		BaseURL:      "http://localhost",
		Port:         "8080",
		Addr:         "127.0.0.1:8080",
		EnableTokens: true,
	}
}

// ---------------------------------------------------------------------------
// TestServerToggles
// ---------------------------------------------------------------------------

// TestServerToggles verifies each ENABLE_* toggle:
// off-states prove absence via 404 or missing MCP tool;
// on-states prove presence via routed HTTP response and MCP round-trips.
func TestServerToggles(t *testing.T) {
	t.Run("off/noTokens", func(t *testing.T) {
		cfg := baseConfig()
		cfg.EnableTokens = false
		ts := buildTestServer(t, cfg)

		if ts.TokenStore != nil {
			t.Error("TokenStore should be nil when EnableTokens=false")
		}
		// Health is always available regardless of token config.
		if got := getStatus(t, ts.URL, "", "/_health"); got != http.StatusOK {
			t.Errorf("/_health: got %d, want 200", got)
		}
		// MCP requires a Bearer token when a secret is configured.
		// Without a token store there is no way to issue tokens, so
		// unauthenticated MCP calls must return 401.
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/mcp/message",
			bytes.NewReader([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("unauthenticated MCP call: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("unauthenticated MCP: got %d, want 401", resp.StatusCode)
		}
	})

	t.Run("off/noDynamicContent", func(t *testing.T) {
		cfg := baseConfig()
		// EnableDynamicContent deliberately left false.
		ts := buildTestServer(t, cfg)
		token := createToken(t, ts, "author", "author")
		tools := toolsList(t, ts.URL, token)
		if slices.Contains(tools, "define_content_type") {
			t.Error("define_content_type present in tools/list when ENABLE_DYNAMIC_CONTENT=false")
		}
	})

	t.Run("off/noOrchestration", func(t *testing.T) {
		cfg := baseConfig()
		// EnableOrchestration deliberately left false.
		ts := buildTestServer(t, cfg)
		token := createToken(t, ts, "author", "author")

		// /goals route must not be registered.
		if got := getStatus(t, ts.URL, token, "/goals"); got != http.StatusNotFound {
			t.Errorf("GET /goals: got %d, want 404 when ENABLE_ORCHESTRATION=false", got)
		}
		// create_goal must not appear — the Goal module is not registered.
		// Note: get_goal_context is always listed when a DB is configured (it is
		// gated on db != nil, not on ENABLE_ORCHESTRATION), so it is not checked here.
		tools := toolsList(t, ts.URL, token)
		if slices.Contains(tools, "create_goal") {
			t.Error("create_goal present in tools/list when ENABLE_ORCHESTRATION=false")
		}
	})

	t.Run("on/orchestration", func(t *testing.T) {
		cfg := baseConfig()
		cfg.EnableOrchestration = true
		ts := buildTestServer(t, cfg)
		token := createToken(t, ts, "author", "author")

		// /goals route must be registered (200 empty list or 403 — not 404).
		got := getStatus(t, ts.URL, token, "/goals")
		if got == http.StatusNotFound {
			t.Errorf("GET /goals: got 404; route should exist when ENABLE_ORCHESTRATION=true")
		}
		// Both create_goal and get_goal_context must appear in tools/list.
		tools := toolsList(t, ts.URL, token)
		if !slices.Contains(tools, "create_goal") {
			t.Error("create_goal missing from tools/list when ENABLE_ORCHESTRATION=true")
		}
		if !slices.Contains(tools, "get_goal_context") {
			t.Error("get_goal_context missing from tools/list when ENABLE_ORCHESTRATION=true")
		}

		// create_goal round trip.
		goal := callTool(t, ts.URL, token, "create_goal", map[string]any{
			"goal_id":     "T-test",
			"description": "Test goal for orchestration toggle verification",
			"priority":    float64(1),
			"band":        "P1",
			"size":        "S",
		})
		goalID, _ := goal["ID"].(string)
		if goalID == "" {
			t.Fatalf("create_goal: ID missing in response: %v", goal)
		}

		// get_goal_context using the GoalID string.
		ctx := callTool(t, ts.URL, token, "get_goal_context", map[string]any{
			"goal_id": "T-test",
		})
		inner, _ := ctx["goal"].(map[string]any)
		if inner == nil {
			t.Fatalf("get_goal_context: 'goal' key missing in response: %v", ctx)
		}
		if got, _ := inner["goal_id"].(string); got != "T-test" {
			t.Errorf("get_goal_context: goal.goal_id = %q, want %q", got, "T-test")
		}
	})

	t.Run("on/orchestrationWithRelations", func(t *testing.T) {
		// This is the literal dogfood loop: create a Goal and a Decision,
		// assert a relation between them, confirm get_goal_context traverses it.
		cfg := baseConfig()
		cfg.EnableOrchestration = true
		cfg.EnableRelations = true
		ts := buildTestServer(t, cfg)
		adminToken := createToken(t, ts, "admin", "admin")
		authorToken := createToken(t, ts, "author", "author")

		// Register a relation kind (Admin-only).
		callTool(t, ts.URL, adminToken, "upsert_relation_kind", map[string]any{
			"type_name": "implements",
			"mode":      "asserted",
		})

		// Create a Goal.
		goal := callTool(t, ts.URL, authorToken, "create_goal", map[string]any{
			"goal_id":     "T-rel-test",
			"description": "Goal for relation dogfood test",
			"priority":    float64(1),
			"band":        "P1",
			"size":        "S",
		})
		goalNodeID, _ := goal["ID"].(string)
		if goalNodeID == "" {
			t.Fatalf("create_goal: ID missing: %v", goal)
		}

		// Create a Decision.
		dec := callTool(t, ts.URL, authorToken, "create_decision", map[string]any{
			"decision_number": "D-test",
			"scope":           "test",
			"body":            "Test decision for relation dogfood",
		})
		decNodeID, _ := dec["ID"].(string)
		if decNodeID == "" {
			t.Fatalf("create_decision: ID missing: %v", dec)
		}

		// Assert relation: Goal implements Decision.
		// source_type and target_type must match the strings used by QueryGoalContext.
		callTool(t, ts.URL, authorToken, "assert_relation", map[string]any{
			"source_type":   "Goal",
			"source_id":     goalNodeID,
			"target_type":   "Decision",
			"target_id":     decNodeID,
			"relation_kind": "implements",
		})

		// get_goal_context must include the linked Decision.
		gctx := callTool(t, ts.URL, authorToken, "get_goal_context", map[string]any{
			"goal_id": "T-rel-test",
		})
		linkedDecs, _ := gctx["linked_decisions"].([]any)
		if len(linkedDecs) == 0 {
			t.Errorf("get_goal_context: linked_decisions is empty, want the asserted Decision")
		}
	})

	t.Run("on/redirects", func(t *testing.T) {
		cfg := baseConfig()
		cfg.EnableRedirects = true
		ts := buildTestServer(t, cfg)
		token := createToken(t, ts, "author", "author")
		// Redirect management is MCP-only; confirm create_redirect is in tools/list.
		tools := toolsList(t, ts.URL, token)
		if !slices.Contains(tools, "create_redirect") {
			t.Error("create_redirect missing from tools/list when ENABLE_REDIRECTS=true")
		}
	})

	t.Run("on/pageMeta", func(t *testing.T) {
		cfg := baseConfig()
		cfg.EnablePageMeta = true
		ts := buildTestServer(t, cfg)
		token := createToken(t, ts, "author", "author")
		// Page meta management is MCP-only; confirm set_page_meta is in tools/list.
		tools := toolsList(t, ts.URL, token)
		if !slices.Contains(tools, "set_page_meta") {
			t.Error("set_page_meta missing from tools/list when ENABLE_PAGE_META=true")
		}
	})
}
