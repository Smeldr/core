package main

import (
	"database/sql"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// schemaOf lists every schema object in db: a refused boot leaves it as it
// was (T955).
func schemaOf(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query(`SELECT type || ' ' || name FROM sqlite_master ORDER BY type, name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// assertSchemaUnchanged fails when db's schema differs from before.
func assertSchemaUnchanged(t *testing.T, db *sql.DB, before []string) {
	t.Helper()
	if after := schemaOf(t, db); strings.Join(after, "\n") != strings.Join(before, "\n") {
		t.Errorf("a refused boot changed the schema:\nbefore %v\nafter  %v", before, after)
	}
}

// freshDB is a new, empty sqlite database file.
func freshDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "fresh.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	return db
}

// TestBoot_RefusalWritesNoSchema (T955): every configuration refusal comes
// before the first write, so a refused boot on a fresh database leaves it
// empty, and the error names the variable, never a secret.
func TestBoot_RefusalWritesNoSchema(t *testing.T) {
	key := testRSAKeyPEM(t)
	github := func(c *ServerConfig) {
		c.EnableGitHub = true
		c.GitHub = gitHubConfig{AppID: "12345", PrivateKey: key, WebhookSecret: testGitHubWebhookSecret, AdapterTokenID: "adapter"}
	}
	records := func(c *ServerConfig) { c.EnableGovernanceRecords = true }
	for _, tc := range []struct {
		name string
		edit func(*ServerConfig)
		want string
	}{
		{"title cutover", func(c *ServerConfig) { c.TitleCutover = "yesterday" }, "TITLE_CUTOVER: want an RFC3339 time"},
		{"signal message cutover", func(c *ServerConfig) { c.SignalMessageCutover = "soon" }, "SIGNAL_MESSAGE_CUTOVER: want an RFC3339 time"},
		{"signal event cutover", func(c *ServerConfig) { c.SignalEventCutover = "later" }, "SIGNAL_EVENT_CUTOVER: want an RFC3339 time"},
		{"check deps", func(c *ServerConfig) { c.EnableCheck = true; records(c) }, "ENABLE_CHECK requires ENABLE_AUTHORITY"},
		{"event log deps", func(c *ServerConfig) { c.EnableEventLog = true }, "ENABLE_EVENT_LOG requires ENABLE_EVENT_STREAM"},
		{"sweep deps", func(c *ServerConfig) { c.EnableStructuralSweep = true }, "ENABLE_STRUCTURAL_SWEEP requires ENABLE_RELATIONS"},
		{"eval queue deps", func(c *ServerConfig) { c.EnableEvalQueueDrain = true }, "ENABLE_EVAL_QUEUE_DRAIN requires ENABLE_GOVERNANCE_RECORDS"},
		{"drift deps", func(c *ServerConfig) { c.EnableStandingDriftCheck = true }, "ENABLE_STANDING_DRIFT_CHECK requires ENABLE_GOVERNANCE_RECORDS"},
		{"media backend", func(c *ServerConfig) { c.EnableMedia, c.MediaBackend = true, "s3" }, `unsupported MEDIA_STORE_BACKEND "s3"`},
		{"sweep schedule", func(c *ServerConfig) {
			c.EnableRelations, c.EnableStructuralSweep, c.StructuralSweepSchedule = true, true, "every hour"
		}, "structural sweep scheduler:"},
		{"eval queue schedule", func(c *ServerConfig) {
			records(c)
			c.EnableEvalQueueDrain, c.EvalQueueDrainSchedule = true, "61 * * * *"
		}, "eval queue drain scheduler:"},
		{"drift schedule", func(c *ServerConfig) {
			records(c)
			c.EnableStandingDriftCheck, c.StandingDriftSchedule = true, "daily"
		}, "standing drift check scheduler:"},
		{"github deps", func(c *ServerConfig) { github(c); c.EnableWebhooks = false }, "ENABLE_GITHUB requires ENABLE_TOKENS"},
		{"github missing", func(c *ServerConfig) { github(c); c.GitHub.AppID, c.GitHub.WebhookSecret = "", "" }, "not set: GITHUB_APP_ID, GITHUB_WEBHOOK_SECRET"},
		{"github both keys", func(c *ServerConfig) { github(c); c.GitHub.PrivateKeyFile = "x.pem" }, "are both set"},
		{"github key file", func(c *ServerConfig) {
			github(c)
			c.GitHub.PrivateKey, c.GitHub.PrivateKeyFile = "", filepath.Join(t.TempDir(), "missing.pem")
		}, "GITHUB_APP_PRIVATE_KEY_FILE cannot be read"},
		{"github bad key", func(c *ServerConfig) {
			github(c)
			c.GitHub.PrivateKey = "-----BEGIN RSA PRIVATE KEY-----\nbm90IGEga2V5\n-----END RSA PRIVATE KEY-----\n"
		}, "GITHUB_APP_PRIVATE_KEY is not an RSA private key in PEM"},
		{"github core webhook url", func(c *ServerConfig) { github(c); c.BaseURL = "http://8.8.8.8" }, "the core webhook for changecheck.transitioned cannot be created"},
		{"github fresh database", github, "GITHUB_ADAPTER_TOKEN_ID: the tokens cannot be read"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// GitHub's cases start from every dependency on; the rest from
			// nothing on.
			cfg := ServerConfig{Secret: gitHubBase().Secret, BaseURL: "https://8.8.8.8", InstanceName: "test"}
			if strings.HasPrefix(tc.name, "github") {
				cfg = gitHubBase()
			}
			tc.edit(&cfg)
			db := freshDB(t)
			before := schemaOf(t, db)
			res, err := buildApp(cfg, db)
			if err == nil {
				res.StopAll()
				t.Fatalf("booted, want %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			for _, secret := range []string{testGitHubWebhookSecret, key[40:80]} {
				if strings.Contains(err.Error(), secret) {
					t.Errorf("a secret appears in %q", err)
				}
			}
			assertSchemaUnchanged(t, db, before)
		})
	}
}

// TestBoot_ScheduleCheckLeavesNoGoroutine (T955): validateBoot builds each
// scheduler to parse its schedule and stops it; nothing keeps running.
func TestBoot_ScheduleCheckLeavesNoGoroutine(t *testing.T) {
	cfg := ServerConfig{
		EnableRelations: true, EnableStructuralSweep: true,
		EnableGovernanceRecords: true, EnableEvalQueueDrain: true, EnableStandingDriftCheck: true,
	}
	if _, err := validateBoot(cfg); err != nil {
		t.Fatal(err)
	}
	before := runtime.NumGoroutine()
	for i := 0; i < 20; i++ {
		if _, err := validateBoot(cfg); err != nil {
			t.Fatal(err)
		}
	}
	// Stopped schedulers' goroutines may take a moment to return.
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > before+2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > before+2 {
		t.Errorf("goroutines: %d before 60 schedule checks, %d after", before, n)
	}
}
