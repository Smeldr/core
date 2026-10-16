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

// The subcommand is a dry run by default; --apply writes, --report also writes
// the JSON to a file, and --ref-field names an undeclared reference field.
func TestRunMigrateReferences(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "smeldr.db")
	t.Setenv("SECRET", "test-secret-that-is-at-least-32-bytes")
	t.Setenv("DATABASE_PATH", dbPath)
	t.Setenv("BASE_URL", "https://cms.example.com")
	t.Setenv("ENABLE_ORCHESTRATION", "1")
	t.Setenv("ENABLE_RELATIONS", "1")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := smeldr.CreateOrchestrationTables(db); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := db.Exec(`INSERT INTO smeldr_tasks (id, slug, status, created_at, updated_at) VALUES ('t-1', 'first', 'backlog', $1, $1)`, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO smeldr_signals (id, slug, status, created_at, updated_at, sender, receiver, signal_type, task_ref)
		VALUES ('s-1', 's-1', 'pending', $1, $1, 'core', 'architect', 'notice', 'first')`, now); err != nil {
		t.Fatal(err)
	}
	db.Close()

	var out, errOut bytes.Buffer
	if code := runMigrateReferences(nil, &out, &errOut); code != 0 {
		t.Fatalf("dry run exit %d: %s", code, errOut.String())
	}
	var rep smeldr.ReferenceMigrationReport
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil || !rep.DryRun || len(rep.Created) != 1 {
		t.Fatalf("dry-run report = %s, %v", out.String(), err)
	}

	reportPath := filepath.Join(dir, "report.json")
	out.Reset()
	if code := runMigrateReferences([]string{"--apply", "--report=" + reportPath, "--ref-field=task_plan.task_ref=Task"}, &out, &errOut); code != 0 {
		t.Fatalf("apply exit %d: %s", code, errOut.String())
	}
	if b, err := os.ReadFile(reportPath); err != nil || !bytes.Equal(b, out.Bytes()) {
		t.Errorf("report file = %s, %v", b, err)
	}
	out.Reset()
	if code := runMigrateReferences(nil, &out, &errOut); code != 0 {
		t.Fatalf("second run exit %d", code)
	}
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil || len(rep.Created) != 0 || len(rep.Present) != 1 {
		t.Errorf("after apply = %s, %v", out.String(), err)
	}

	if code := runMigrateReferences([]string{"--nope"}, &out, &errOut); code != 2 {
		t.Errorf("bad flag exit %d; want 2", code)
	}
	if code := runMigrateReferences([]string{"--ref-field=noequals"}, &out, &errOut); code != 2 {
		t.Errorf("--ref-field without = exit %d; want 2", code)
	}
	errOut.Reset()
	if code := runMigrateReferences([]string{"--ref-field=nodot=Task"}, &out, &errOut); code != 1 || errOut.Len() == 0 {
		t.Errorf("--ref-field without a dot exit %d (%s); want 1", code, errOut.String())
	}
	t.Setenv("TITLE_CUTOVER", "tomorrow")
	if code := runMigrateReferences(nil, &out, &errOut); code != 1 {
		t.Errorf("bad TITLE_CUTOVER exit %d; want 1", code)
	}
}
