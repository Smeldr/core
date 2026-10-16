package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	smeldr "smeldr.dev/core"
)

// The subcommand is a dry run by default; --apply writes, and --report also
// writes the JSON report to a file. A bad Signal cutover stops the build.
func TestRunMigrateSignalKinds(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "smeldr.db")
	t.Setenv("SECRET", "test-secret-that-is-at-least-32-bytes")
	t.Setenv("DATABASE_PATH", dbPath)
	t.Setenv("BASE_URL", "https://cms.example.com")
	t.Setenv("ENABLE_ORCHESTRATION", "1")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := smeldr.CreateOrchestrationTables(db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO smeldr_signals (id, slug, status, created_at, updated_at, signal_type) VALUES ('s-old', 's-old', 'pending', $1, $1, 'review-requested')`, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	db.Close()

	status := func() string {
		db, err := sql.Open("sqlite", dbPath)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		var v string
		if err := db.QueryRow(`SELECT status FROM smeldr_signals WHERE id = 's-old'`).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}

	var out, errOut bytes.Buffer
	if code := runMigrateSignalKinds(nil, &out, &errOut); code != 0 {
		t.Fatalf("dry run exit %d: %s", code, errOut.String())
	}
	var rep smeldr.SignalMigrationReport
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil || rep.Applied || len(rep.Changes) != 1 {
		t.Fatalf("dry-run report = %s, %v", out.String(), err)
	}
	if got := status(); got != "pending" {
		t.Errorf("a dry run wrote %q", got)
	}

	reportPath := filepath.Join(dir, "report.json")
	out.Reset()
	if code := runMigrateSignalKinds([]string{"--apply", "--report=" + reportPath}, &out, &errOut); code != 0 {
		t.Fatalf("apply exit %d: %s", code, errOut.String())
	}
	if got := status(); got != "open" {
		t.Errorf("status after apply = %q, want open", got)
	}
	if b, err := os.ReadFile(reportPath); err != nil || !bytes.Equal(b, out.Bytes()) {
		t.Errorf("report file = %s, %v", b, err)
	}

	if code := runMigrateSignalKinds([]string{"--nope"}, &out, &errOut); code != 2 {
		t.Errorf("bad flag exit %d; want 2", code)
	}
	t.Setenv("SIGNAL_EVENT_CUTOVER", "tomorrow")
	if code := runMigrateSignalKinds(nil, &out, &errOut); code != 1 {
		t.Errorf("bad SIGNAL_EVENT_CUTOVER exit %d; want 1", code)
	}
	t.Setenv("SIGNAL_EVENT_CUTOVER", "")
	t.Setenv("SIGNAL_MESSAGE_CUTOVER", "tomorrow")
	if code := runMigrateSignalKinds(nil, &out, &errOut); code != 1 {
		t.Errorf("bad SIGNAL_MESSAGE_CUTOVER exit %d; want 1", code)
	}
}
