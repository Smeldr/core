package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	smeldr "smeldr.dev/core"
)

// The subcommand is a dry run by default; --apply writes, and --report also
// writes the JSON report to a file.
func TestRunMigrateAddresses(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "smeldr.db")
	t.Setenv("SECRET", "test-secret-that-is-at-least-32-bytes")
	t.Setenv("DATABASE_PATH", dbPath)
	t.Setenv("BASE_URL", "https://cms.example.com")
	t.Setenv("ENABLE_ORCHESTRATION", "1")

	// A database from before addresses: one Decision with a legacy number.
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := smeldr.CreateOrchestrationTables(db); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, q := range []string{
		`DELETE FROM smeldr_address_counters`,
		`INSERT INTO smeldr_decisions (id, slug, status, created_at, updated_at, decision_number) VALUES ('d-old', 'd-old', 'ratified', $1, $1, 'D4')`,
	} {
		if _, err := db.Exec(q, now); err != nil && !strings.Contains(q, "DELETE") {
			t.Fatal(err)
		}
	}
	db.Close()

	address := func() string {
		db, err := sql.Open("sqlite", dbPath)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		var a string
		if err := db.QueryRow(`SELECT address FROM smeldr_decisions WHERE id = 'd-old'`).Scan(&a); err != nil {
			t.Fatal(err)
		}
		return a
	}

	var out, errOut bytes.Buffer
	if code := runMigrateAddresses(nil, &out, &errOut); code != 0 {
		t.Fatalf("dry run exit %d: %s", code, errOut.String())
	}
	var rep smeldr.AddressMigrationReport
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil || !rep.DryRun {
		t.Fatalf("dry run report: %v %s", err, out.String())
	}
	if a := address(); a != "" {
		t.Errorf("after the dry run the address is %q, want nothing written", a)
	}

	reportPath := filepath.Join(dir, "report.json")
	out.Reset()
	if code := runMigrateAddresses([]string{"--apply", "--report=" + reportPath}, &out, &errOut); code != 0 {
		t.Fatalf("apply exit %d: %s", code, errOut.String())
	}
	if a := address(); a != "D4" {
		t.Errorf("after --apply the address is %q, want D4 kept", a)
	}
	file, err := os.ReadFile(reportPath)
	if err != nil || !bytes.Equal(file, out.Bytes()) {
		t.Errorf("report file = %q (%v), want the same JSON as stdout", file, err)
	}
	if code := runMigrateAddresses([]string{"--bogus"}, &out, &errOut); code != 2 {
		t.Errorf("an unknown flag: exit %d, want 2", code)
	}
}
