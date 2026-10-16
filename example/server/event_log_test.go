package main

import (
	"database/sql"
	"testing"
)

// ENABLE_EVENT_LOG needs ENABLE_EVENT_STREAM; with both the log table exists
// and the App counts no unlogged events (D117).
func TestEventLogToggle(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	cfg := baseConfig()
	cfg.EnableEventLog = true
	if _, err := buildApp(cfg, db); err == nil {
		t.Fatal("buildApp: want an error for ENABLE_EVENT_LOG without ENABLE_EVENT_STREAM")
	}

	cfg.EnableEventStream = true
	cfg.EventLogRetentionDays = 7
	cfg.EnableWebhooks = true
	ts := buildTestServer(t, cfg)
	var n int
	if err := ts.db.QueryRow(`SELECT COUNT(*) FROM smeldr_events`).Scan(&n); err != nil {
		t.Fatalf("smeldr_events: %v", err)
	}
	if ts.Result.App.UnloggedEvents() != 0 {
		t.Error("unlogged events at boot")
	}
}
