package main

import (
	"bytes"
	"database/sql"
	"log/slog"
	"strings"
	"testing"
)

// hasAgentJobsTable reports whether db has the agent_jobs table.
func hasAgentJobsTable(t *testing.T, db *sql.DB) bool {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'agent_jobs'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n == 1
}

// bootAgents runs buildApp on db with the agent job system on and returns
// what it logged.
func bootAgents(t *testing.T, db *sql.DB, cfg ServerConfig) (string, error) {
	t.Helper()
	cfg.EnableAgents = true
	cfg.AgentMCPURL = "http://127.0.0.1:1/mcp/message"
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(prev)
	res, err := buildApp(cfg, db)
	if err == nil {
		t.Cleanup(res.StopAll)
	}
	return logs.String(), err
}

// TestAgents_FreshDatabaseCreatesTable: with ENABLE_AGENTS on a fresh
// database, the boot creates agent_jobs before the agent module loads its
// jobs, so the scheduler starts without an error (T1129). A second boot on
// the same database works too, and a refused boot creates nothing.
func TestAgents_FreshDatabaseCreatesTable(t *testing.T) {
	cfg := ServerConfig{Secret: gitHubBase().Secret, BaseURL: "https://8.8.8.8", InstanceName: "test"}

	refused := freshDB(t)
	bad := cfg
	bad.BaseURL = ""
	if _, err := bootAgents(t, refused, bad); err == nil {
		t.Fatal("a config without BASE_URL booted")
	}
	if hasAgentJobsTable(t, refused) {
		t.Error("a refused boot created agent_jobs")
	}

	db := freshDB(t)
	for _, boot := range []string{"fresh database", "second boot"} {
		logs, err := bootAgents(t, db, cfg)
		if err != nil {
			t.Fatalf("%s: %v", boot, err)
		}
		if !hasAgentJobsTable(t, db) {
			t.Fatalf("%s: no agent_jobs table", boot)
		}
		for _, bad := range []string{"scheduler init failed", "no such table"} {
			if strings.Contains(logs, bad) {
				t.Errorf("%s: logged %q:\n%s", boot, bad, logs)
			}
		}
	}
}
