package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	smeldr "smeldr.dev/core"
)

// The subcommand is a dry run by default; --apply writes and --report also
// writes the JSON report. Here the database has nothing to restore.
func TestRunRepairSweptEdges(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "smeldr.db")
	t.Setenv("SECRET", "test-secret-that-is-at-least-32-bytes")
	t.Setenv("DATABASE_PATH", dbPath)
	t.Setenv("BASE_URL", "https://cms.example.com")
	t.Setenv("ENABLE_ORCHESTRATION", "1")
	t.Setenv("ENABLE_RELATIONS", "1")
	t.Setenv("ENABLE_PROVENANCE", "1")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, create := range []func(smeldr.DB) error{smeldr.CreateOrchestrationTables, smeldr.CreateRelationTables, smeldr.CreateProvenanceTable} {
		if err := create(db); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	var out, errOut bytes.Buffer
	if code := runRepairSweptEdges(nil, &out, &errOut); code != 0 {
		t.Fatalf("dry run exit %d: %s", code, errOut.String())
	}
	var rep smeldr.SweepRepair
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil || rep.Applied || rep.Candidates != 0 {
		t.Fatalf("dry-run report = %s, %v", out.String(), err)
	}
	report := filepath.Join(dir, "r.json")
	out.Reset()
	if code := runRepairSweptEdges([]string{"--apply", "--report", report}, &out, &errOut); code != 0 {
		t.Fatalf("apply exit %d: %s", code, errOut.String())
	}
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil || !rep.Applied {
		t.Fatalf("apply report = %s, %v", out.String(), err)
	}
	if b, err := os.ReadFile(report); err != nil || !bytes.Equal(b, out.Bytes()) {
		t.Errorf("report file = %q, %v", b, err)
	}
	if code := runRepairSweptEdges([]string{"--no-such-flag"}, &out, &errOut); code != 2 {
		t.Errorf("a bad flag exit %d", code)
	}
	if code := runRepairSweptEdges([]string{"--report", filepath.Join(dir, "missing", "r.json")}, &out, &errOut); code != 1 {
		t.Errorf("an unwritable report exit %d", code)
	}
}
