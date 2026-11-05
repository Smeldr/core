package main

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	smeldr "smeldr.dev/core"
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
	records := func(c *ServerConfig) { c.EnableRelations, c.EnableGovernanceRecords = true, true }
	for _, tc := range []struct {
		name string
		edit func(*ServerConfig)
		want string
	}{
		{"base url missing", func(c *ServerConfig) { c.BaseURL = "" }, `BASE_URL: Config.BaseURL is required`},
		{"base url relative", func(c *ServerConfig) { c.BaseURL = "/x" }, `BASE_URL: Config.BaseURL "/x" is not a valid absolute URL`},
		{"secret short", func(c *ServerConfig) { c.Secret = "short" }, "SECRET: Config.Secret must be at least 16 bytes"},
		{"records without relations", func(c *ServerConfig) { c.EnableGovernanceRecords = true }, "ENABLE_GOVERNANCE_RECORDS requires ENABLE_RELATIONS"},
		{"orchestration without relations", func(c *ServerConfig) { c.EnableOrchestration = true }, "ENABLE_ORCHESTRATION requires ENABLE_RELATIONS"},
		{"title cutover", func(c *ServerConfig) { c.TitleCutover = "yesterday" }, "TITLE_CUTOVER: want an RFC3339 time"},
		{"signal message cutover", func(c *ServerConfig) { c.SignalMessageCutover = "soon" }, "SIGNAL_MESSAGE_CUTOVER: want an RFC3339 time"},
		{"signal event cutover", func(c *ServerConfig) { c.SignalEventCutover = "later" }, "SIGNAL_EVENT_CUTOVER: want an RFC3339 time"},
		{"plan paths cutover", func(c *ServerConfig) { c.PlanPathsCutover = "monday" }, "PLAN_PATHS_CUTOVER: want an RFC3339 time"},
		{"rev cutover", func(c *ServerConfig) { c.RevCutover = "soon" }, "REV_CUTOVER: want an RFC3339 time"},
		{"check deps", func(c *ServerConfig) { c.EnableCheck = true; records(c) }, "ENABLE_CHECK requires ENABLE_AUTHORITY"},
		{"event log deps", func(c *ServerConfig) { c.EnableEventLog = true }, "ENABLE_EVENT_LOG requires ENABLE_EVENT_STREAM"},
		{"event log retention not a number", func(c *ServerConfig) {
			c.EnableEventStream, c.EnableEventLog, c.EventLogRetentionDays = true, true, "thirty"
		}, `EVENT_LOG_RETENTION_DAYS: want a whole number of days, 1 or more, not "thirty"`},
		{"event log retention zero", func(c *ServerConfig) {
			c.EnableEventStream, c.EnableEventLog, c.EventLogRetentionDays = true, true, "0"
		}, "EVENT_LOG_RETENTION_DAYS: want a whole number of days"},
		{"event log retention negative", func(c *ServerConfig) {
			c.EnableEventStream, c.EnableEventLog, c.EventLogRetentionDays = true, true, "-5"
		}, "EVENT_LOG_RETENTION_DAYS: want a whole number of days"},
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
		Secret: "test-secret-that-is-at-least-32-bytes", BaseURL: "https://example.com",
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

// TestParseRetentionDays (T1094): empty is core's default, a whole number of
// 1 or more is days, anything else is refused; the value is not trimmed.
func TestParseRetentionDays(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  time.Duration
		ok    bool
	}{
		{"", 0, true},
		{"7", 7 * 24 * time.Hour, true},
		{"1", 24 * time.Hour, true},
		{" 7", 0, false},
		{"0", 0, false},
		{"-1", 0, false},
		{"7.5", 0, false},
		{"x", 0, false},
	} {
		got, err := parseRetentionDays("EVENT_LOG_RETENTION_DAYS", tc.value)
		if (err == nil) != tc.ok || got != tc.want {
			t.Errorf("parseRetentionDays(%q) = %v, %v; want %v, ok %v", tc.value, got, err, tc.want, tc.ok)
		}
	}
	// A malformed value is not read while the event log is off.
	cfg := ServerConfig{Secret: gitHubBase().Secret, BaseURL: "https://8.8.8.8", InstanceName: "test", EventLogRetentionDays: "thirty"}
	if _, err := validateBoot(cfg); err != nil {
		t.Errorf("event log off: %v", err)
	}
}

// TestOpenDatabase_RefusedCreatesNoFile: a boot refused for its
// configuration exits before the database is opened, so no file is created
// at DATABASE_PATH (T1117).
func TestOpenDatabase_RefusedCreatesNoFile(t *testing.T) {
	badConfig := filepath.Join(t.TempDir(), "smeldr.config")
	if err := os.WriteFile(badConfig, []byte("https = maybe\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	key := testRSAKeyPEM(t)
	for _, tc := range []struct {
		name string
		edit func(*ServerConfig)
		env  map[string]string
		want string
	}{
		{"base url missing", func(c *ServerConfig) { c.BaseURL = "" }, nil, "BASE_URL: Config.BaseURL is required"},
		{"cutover", func(c *ServerConfig) { c.TitleCutover = "yesterday" }, nil, "TITLE_CUTOVER: want an RFC3339 time"},
		{"config file", func(*ServerConfig) {}, map[string]string{"SMELDR_CONFIG": badConfig}, "SMELDR_CONFIG"},
		{"github, no database yet", func(c *ServerConfig) {
			*c = gitHubBase()
			c.EnableGitHub = true
			c.GitHub = gitHubConfig{AppID: "12345", PrivateKey: key, WebhookSecret: testGitHubWebhookSecret, AdapterTokenID: "adapter"}
		}, nil, errNoAdapterToken.Error()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			cfg := ServerConfig{Secret: gitHubBase().Secret, BaseURL: "https://8.8.8.8", InstanceName: "test"}
			tc.edit(&cfg)
			path := filepath.Join(t.TempDir(), "smeldr.db")
			db, err := openDatabase(cfg, path)
			if err == nil {
				db.Close()
				t.Fatal("openDatabase opened a database for a refused config")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want %q", err, tc.want)
			}
			if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
				t.Errorf("a refused boot created %s (stat: %v)", path, statErr)
			}
		})
	}
}

// TestOpenDatabase_NoAdapterTokenMessage: the refusal openDatabase gives
// without a database is checkGitHubToken's own for an unknown token_id.
func TestOpenDatabase_NoAdapterTokenMessage(t *testing.T) {
	f := newGitHubFixture(t, smeldr.Job, true)
	cfg := f.config()
	cfg.GitHub.AdapterTokenID = "no-such-user"
	_, _, err := f.boot(t, cfg)
	if err == nil || err.Error() != errNoAdapterToken.Error() {
		t.Fatalf("checkGitHubToken: %v, want %q", err, errNoAdapterToken)
	}
}

// TestOpenDatabase_Opens: a valid config opens the database, creates the
// file and allows one connection; with ENABLE_GITHUB an existing file is
// opened.
func TestOpenDatabase_Opens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "smeldr.db")
	db, err := openDatabase(ServerConfig{Secret: gitHubBase().Secret, BaseURL: "https://8.8.8.8"}, path)
	if err != nil {
		t.Fatal(err)
	}
	if got := db.Stats().MaxOpenConnections; got != 1 {
		t.Errorf("MaxOpenConnections = %d, want 1", got)
	}
	db.Close()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("no database file: %v", err)
	}
	db, err = openDatabase(gitHubBase(), path)
	if err != nil {
		t.Fatalf("github with an existing file: %v", err)
	}
	db.Close()
}

// TestOpenDatabase_OpenFails: a path the driver cannot open is an error and
// no database.
func TestOpenDatabase_OpenFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "no-such-dir", "smeldr.db")
	db, err := openDatabase(ServerConfig{Secret: gitHubBase().Secret, BaseURL: "https://8.8.8.8"}, path)
	if err == nil {
		db.Close()
		t.Fatal("openDatabase opened a database in a missing directory")
	}
	if !strings.Contains(err.Error(), "ping db") && !strings.Contains(err.Error(), "open db") {
		t.Errorf("err = %v, want open db or ping db", err)
	}
}
