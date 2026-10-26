package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"database/sql"
	"encoding/pem"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	smeldr "smeldr.dev/core"
)

const (
	testGitHubWebhookSecret = "gh-webhook-secret-do-not-log"
	testGitHubCoreSecret    = "core-webhook-secret-do-not-log"
)

// gitHubBase is a server config with every dependency of ENABLE_GITHUB on
// and the flag off. BASE_URL is an IP literal, so the core webhook's URL
// check needs no DNS.
func gitHubBase() ServerConfig {
	return ServerConfig{
		Secret:                  "test-secret-that-is-at-least-32-bytes",
		BaseURL:                 "https://8.8.8.8",
		EnableTokens:            true,
		EnableGovernance:        true,
		EnableRelations:         true,
		EnableWebhooks:          true,
		EnableGovernanceRecords: true,
		InstanceName:            "test",
	}
}

// testRSAKeyPEM returns a fresh RSA key in PKCS#1 PEM.
func testRSAKeyPEM(t *testing.T) string {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)}))
}

// gitHubFixture is a database booted once without the flag, with the
// adapter's job token, granted editor unless ungranted.
type gitHubFixture struct {
	db     *sql.DB
	userID string
	key    string
}

func newGitHubFixture(t *testing.T, class smeldr.Role, grant bool) gitHubFixture {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	res, err := buildApp(gitHubBase(), db)
	if err != nil {
		t.Fatalf("first boot: %v", err)
	}
	res.StopAll()
	ctx := context.Background()
	_, userID, err := res.TokenStore.CreateClassified(ctx, "github-adapter", "editor", class, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if grant {
		if _, err := smeldr.NewRoleStore(db).Grant(ctx, smeldr.RoleGrant{TokenID: userID, RoleName: "editor"}); err != nil {
			t.Fatal(err)
		}
	}
	return gitHubFixture{db: db, userID: userID, key: testRSAKeyPEM(t)}
}

// config is the base with the flag on and every variable set.
func (f gitHubFixture) config() ServerConfig {
	cfg := gitHubBase()
	cfg.EnableGitHub = true
	cfg.GitHub = gitHubConfig{AppID: "12345", PrivateKey: f.key, WebhookSecret: testGitHubWebhookSecret, AdapterTokenID: f.userID}
	return cfg
}

// boot runs buildApp on the fixture's database and captures the log.
func (f gitHubFixture) boot(t *testing.T, cfg ServerConfig) (ServerResult, string, error) {
	t.Helper()
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(prev)
	res, err := buildApp(cfg, f.db)
	if err == nil {
		t.Cleanup(res.StopAll)
	}
	return res, logs.String(), err
}

// endpoints counts the stored webhook endpoints: a refused boot writes none
// (T946).
func (f gitHubFixture) endpoints(t *testing.T) int {
	t.Helper()
	var n int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM smeldr_webhook_endpoints`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// assertNoSecret fails when any secret value appears in text.
func (f gitHubFixture) assertNoSecret(t *testing.T, text string) {
	t.Helper()
	for _, secret := range []string{testGitHubWebhookSecret, testGitHubCoreSecret, "PRIVATE KEY", f.key[40:80]} {
		if strings.Contains(text, secret) {
			t.Errorf("a secret appears in %q", text)
		}
	}
}

// TestGitHub_Off: without the flag nothing is read or mounted.
func TestGitHub_Off(t *testing.T) {
	ts := buildTestServer(t, gitHubBase())
	resp, err := http.Post(ts.URL+"/github/webhook", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		t.Error("the adapter is mounted without ENABLE_GITHUB")
	}
	if cfg := parseGitHubConfig(func(string) string { return "" }); cfg.AppID != "" || cfg.NamesShown != nil {
		t.Errorf("empty env = %+v", cfg)
	}
}

// TestGitHub_Mounted: with everything set the routes verify signatures, the
// core webhook is created once and reused on the next boot, and no secret is
// logged.
func TestGitHub_Mounted(t *testing.T) {
	f := newGitHubFixture(t, smeldr.Job, true)
	cfg := f.config()
	cfg.GitHub.NamesShown = []string{"smeldr/core"}
	res, logs, err := f.boot(t, cfg)
	if err != nil {
		t.Fatalf("boot: %v", err)
	}
	f.assertNoSecret(t, logs)
	if !strings.Contains(logs, "github: adapter mounted") || !strings.Contains(logs, "github: core webhook created") {
		t.Errorf("logs = %s", logs)
	}
	srv := httptest.NewServer(res.App.Handler())
	defer srv.Close()
	for _, path := range []string{"/github/webhook", "/github/core-events"} {
		resp, err := http.Post(srv.URL+path, "application/json", strings.NewReader(`{"action":"opened"}`))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s unsigned = %d, want 401", path, resp.StatusCode)
		}
	}
	// A second boot reuses the core webhook.
	if _, logs, err := f.boot(t, cfg); err != nil || strings.Contains(logs, "core webhook created") {
		t.Errorf("second boot: %v, logs %s", err, logs)
	}
	ws := smeldr.NewWebhookStore(f.db, []byte(cfg.Secret))
	if eps, err := ws.EndpointsForEvent(context.Background(), gitHubCoreEvent); err != nil || len(eps) != 1 || eps[0].TargetURL != "https://8.8.8.8/github/core-events" {
		t.Errorf("core webhooks = %+v %v", eps, err)
	}
	// The override creates nothing and the key file form works.
	keyFile := filepath.Join(t.TempDir(), "app.pem")
	if err := os.WriteFile(keyFile, []byte(f.key), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg.GitHub.PrivateKey, cfg.GitHub.PrivateKeyFile = "", keyFile
	cfg.GitHub.CoreWebhookSecret = testGitHubCoreSecret
	cfg.BaseURL = "https://8.8.4.4"
	if _, logs, err := f.boot(t, cfg); err != nil || strings.Contains(logs, "core webhook created") {
		t.Errorf("override boot: %v, logs %s", err, logs)
	}
	// A key with escaped newlines, as one env line.
	cfg = f.config()
	cfg.GitHub.PrivateKey = strings.ReplaceAll(f.key, "\n", `\n`)
	if _, _, err := f.boot(t, cfg); err != nil {
		t.Errorf("escaped key: %v", err)
	}
}

// TestGitHub_Refuses: each missing or wrong value refuses to start, names
// the variable, and shows no value.
func TestGitHub_Refuses(t *testing.T) {
	f := newGitHubFixture(t, smeldr.Job, true)
	missingFile := filepath.Join(t.TempDir(), "missing.pem")
	for _, tc := range []struct {
		name string
		edit func(*ServerConfig)
		want string
	}{
		{"requires", func(c *ServerConfig) { c.EnableWebhooks = false }, "ENABLE_GITHUB requires ENABLE_TOKENS"},
		{"missing", func(c *ServerConfig) { c.GitHub.AppID, c.GitHub.WebhookSecret = "", "" }, "not set: GITHUB_APP_ID, GITHUB_WEBHOOK_SECRET"},
		{"no key", func(c *ServerConfig) { c.GitHub.PrivateKey, c.GitHub.AdapterTokenID = "", "" }, "GITHUB_APP_PRIVATE_KEY (or GITHUB_APP_PRIVATE_KEY_FILE), GITHUB_ADAPTER_TOKEN_ID"},
		{"both keys", func(c *ServerConfig) { c.GitHub.PrivateKeyFile = "x.pem" }, "are both set"},
		{"key file", func(c *ServerConfig) { c.GitHub.PrivateKey, c.GitHub.PrivateKeyFile = "", missingFile }, "GITHUB_APP_PRIVATE_KEY_FILE cannot be read"},
		{"bad key", func(c *ServerConfig) {
			c.GitHub.PrivateKey = "-----BEGIN RSA PRIVATE KEY-----\nbm90IGEga2V5\n-----END RSA PRIVATE KEY-----\n"
		}, "GITHUB_APP_PRIVATE_KEY is not an RSA private key in PEM"},
		{"unknown token", func(c *ServerConfig) { c.GitHub.AdapterTokenID = "no-such-user" }, "no live token has that token_id"},
		{"core webhook", func(c *ServerConfig) { c.BaseURL = "http://8.8.8.8" }, "the core webhook for changecheck.transitioned cannot be created"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := f.config()
			tc.edit(&cfg)
			before := schemaOf(t, f.db)
			_, logs, err := f.boot(t, cfg)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			f.assertNoSecret(t, err.Error())
			f.assertNoSecret(t, logs)
			if n := f.endpoints(t); n != 0 {
				t.Errorf("a refused boot left %d webhook endpoints", n)
			}
			assertSchemaUnchanged(t, f.db, before)
		})
	}
}

// TestGitHub_Actor: the adapter's token must be of class job and hold
// check_change. Neither refusal changes the schema (T955): the class is read
// before any write; the grant is checked right after Governance, whose
// migration is idempotent on a database that already has a live token, so
// it adds no schema (it may rewrite its own seeded policy rows, which is
// data, not schema).
func TestGitHub_Actor(t *testing.T) {
	for _, tc := range []struct {
		name  string
		class smeldr.Role
		grant bool
		want  string
	}{
		{"human", smeldr.Human, true, "is not of class job"},
		{"no grant", smeldr.Job, false, "does not hold check_change"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newGitHubFixture(t, tc.class, tc.grant)
			before := schemaOf(t, f.db)
			if _, _, err := f.boot(t, f.config()); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want %q", err, tc.want)
			}
			if n := f.endpoints(t); n != 0 {
				t.Errorf("a refused boot left %d webhook endpoints", n)
			}
			assertSchemaUnchanged(t, f.db, before)
		})
	}
}
