package main

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

// findingsTable reports whether smeldr_findings exists.
func findingsTable(t *testing.T, db *sql.DB) bool {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'smeldr_findings'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n == 1
}

// TestFindings_Wired (T948): the finding store is wired, and its table made,
// exactly when a detector that records findings can run.
func TestFindings_Wired(t *testing.T) {
	base := ServerConfig{Secret: "test-secret-that-is-at-least-32-bytes", BaseURL: "https://example.com", InstanceName: "test"}
	for _, tc := range []struct {
		name string
		edit func(*ServerConfig)
		want bool
	}{
		{"governance records", func(c *ServerConfig) { c.EnableRelations, c.EnableGovernanceRecords = true, true }, true},
		{"orchestration", func(c *ServerConfig) { c.EnableRelations, c.EnableOrchestration = true, true }, true},
		{"structural sweep", func(c *ServerConfig) { c.EnableRelations, c.EnableStructuralSweep = true, true }, true},
		{"neither", func(*ServerConfig) {}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := base
			tc.edit(&cfg)
			ts := buildTestServer(t, cfg)
			if got := ts.Result.App.FindingStore() != nil; got != tc.want {
				t.Errorf("store wired = %v, want %v", got, tc.want)
			}
			if got := findingsTable(t, ts.db); got != tc.want {
				t.Errorf("table made = %v, want %v", got, tc.want)
			}
		})
	}
	// The drift check still needs governance records: the finding condition
	// does not loosen the flag's own requirement.
	cfg := base
	cfg.EnableStandingDriftCheck = true
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()
	if _, err := buildApp(cfg, db); err == nil || !strings.Contains(err.Error(), "ENABLE_STANDING_DRIFT_CHECK requires") {
		t.Errorf("drift check without governance records: %v", err)
	}
	if findingsTable(t, db) {
		t.Error("a refused boot made the finding table")
	}
}

// TestFindings_DetectorRecords (T948): the standing drift check records a
// finding, and a second boot keeps it.
func TestFindings_DetectorRecords(t *testing.T) {
	cfg := ServerConfig{Secret: "test-secret-that-is-at-least-32-bytes", BaseURL: "https://example.com", InstanceName: "test", EnableRelations: true, EnableGovernanceRecords: true}
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()
	res, err := buildApp(cfg, db)
	if err != nil {
		t.Fatal(err)
	}
	defer res.StopAll()
	// A ratified Decision with no stored standing: it holds by its state but
	// its standing says otherwise.
	if _, err := db.Exec(`INSERT INTO smeldr_decisions (id, slug, status, created_at, updated_at) VALUES ('d1', 'd1', 'ratified', datetime('now'), datetime('now'))`); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, drifted, err := res.App.CheckStandingDrift(ctx); err != nil || drifted == 0 {
		t.Fatalf("drift check: %d drifted, %v", drifted, err)
	}
	count := func() int {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM smeldr_findings WHERE detector = 'standing-drift' AND subject_id = 'd1'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count(); n != 1 {
		t.Fatalf("findings = %d, want 1", n)
	}
	again, err := buildApp(cfg, db)
	if err != nil {
		t.Fatalf("second boot: %v", err)
	}
	defer again.StopAll()
	if n := count(); n != 1 {
		t.Errorf("findings after a second boot = %d, want 1", n)
	}
	if list, err := again.App.FindingStore().List(ctx, "standing-drift"); err != nil || len(list) != 1 {
		t.Errorf("FindingStore.List = %v %v", list, err)
	}
}
