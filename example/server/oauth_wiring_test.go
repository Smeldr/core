package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	smeldr "smeldr.dev/core"
)

// core T1287 (D195): the release binary signs an OAuth client in with the
// pasted Smeldr token (ResolveBearer) and reads that token again at each
// refresh (TokenActive), so a revoked token ends its access.
func TestOAuthWiring_SignInStandsForTheToken(t *testing.T) {
	cfg := baseConfig()
	cfg.OAuthIssuer = "http://localhost"
	cfg.OAuthDBPath = filepath.Join(t.TempDir(), "oauth.db")
	ts := buildTestServer(t, cfg)
	raw, _, err := ts.TokenStore.CreateClassified(context.Background(), "client", "author", smeldr.Human, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	const redirect = "https://client.example.com/cb"
	resp, err := http.Post(ts.URL+"/oauth/register", "application/json",
		strings.NewReader(`{"redirect_uris": ["`+redirect+`"], "client_name": "Wiring"}`))
	if err != nil {
		t.Fatal(err)
	}
	var reg map[string]any
	json.NewDecoder(resp.Body).Decode(&reg) //nolint:errcheck
	resp.Body.Close()
	clientID, _ := reg["client_id"].(string)
	if clientID == "" {
		t.Fatalf("register: %d %v", resp.StatusCode, reg)
	}

	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	sum := sha256.Sum256([]byte(verifier))
	authorize := func(bearer string) (int, string) {
		resp, err := noFollow.PostForm(ts.URL+"/oauth/authorize", url.Values{
			"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {redirect},
			"code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "code_challenge_method": {"S256"},
			"bearer_token": {bearer}, "resource": {cfg.BaseURL + "/mcp"}, "scope": {"mcp offline_access"},
		})
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		loc, _ := url.Parse(resp.Header.Get("Location"))
		return resp.StatusCode, loc.Query().Get("code")
	}
	token := func(v url.Values) (int, map[string]any) {
		resp, err := http.PostForm(ts.URL+"/oauth/token", v)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		json.NewDecoder(resp.Body).Decode(&out) //nolint:errcheck
		return resp.StatusCode, out
	}

	if status, _ := authorize("not-a-token"); status != http.StatusUnauthorized {
		t.Errorf("an unknown token: %d, want 401", status)
	}
	status, code := authorize(raw)
	if status != http.StatusFound || code == "" {
		t.Fatalf("sign-in with a real token: %d, code %q", status, code)
	}
	status, out := token(url.Values{"grant_type": {"authorization_code"}, "code": {code}, "client_id": {clientID},
		"redirect_uri": {redirect}, "code_verifier": {verifier}, "resource": {cfg.BaseURL + "/mcp"}})
	rt, _ := out["refresh_token"].(string)
	if status != http.StatusOK || out["access_token"] == nil || rt == "" {
		t.Fatalf("code exchange: %d %v", status, out)
	}

	user, ok := smeldr.VerifyTokenString(raw, []byte(testSecret), ts.TokenStore)
	if !ok {
		t.Fatal("VerifyTokenString")
	}
	if err := ts.TokenStore.Revoke(context.Background(), user.TokenID); err != nil {
		t.Fatal(err)
	}
	if status, out := token(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {rt}, "client_id": {clientID}}); status == http.StatusOK {
		t.Errorf("refresh after the token was revoked: %d %v, want a refusal", status, out)
	}
}

// core T1286: an issuer on another origin than BASE_URL starts, with one Info
// line naming both; forms the module warns about (public http, a trailing
// slash) start too, and a password never reaches the log.
func TestOAuthIssuer_AllowedFormsStart(t *testing.T) {
	for _, c := range []struct{ issuer, want string }{
		{"https://auth.example.test", "OAUTH_ISSUER is on another origin than BASE_URL"},
		{"http://192.168.1.10:8080/", "travel in the clear"},
		{"https://u:secret@auth.example.test", "carries user info"},
	} {
		var buf bytes.Buffer
		prev := slog.Default()
		slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
		cfg := baseConfig()
		cfg.BaseURL = "http://cms.example.test"
		cfg.OAuthIssuer = c.issuer
		cfg.OAuthDBPath = filepath.Join(t.TempDir(), "oauth.db")
		buildTestServer(t, cfg)
		slog.SetDefault(prev)
		out := buf.String()
		if !strings.Contains(out, c.want) {
			t.Errorf("%s: the log lacks %q", c.issuer, c.want)
		}
		if strings.Contains(out, "secret") {
			t.Errorf("%s: the log shows the password", c.issuer)
		}
	}
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	cfg := baseConfig()
	cfg.OAuthIssuer = cfg.BaseURL
	cfg.OAuthDBPath = filepath.Join(t.TempDir(), "oauth.db")
	buildTestServer(t, cfg)
	slog.SetDefault(prev)
	if strings.Contains(buf.String(), "another origin") {
		t.Error("an issuer equal to BASE_URL logged the origin line")
	}
}
