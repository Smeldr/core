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
// writes the JSON report to a file. A bad TITLE_CUTOVER stops the build.
func TestRunMigrateRecordShape(t *testing.T) {
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
	if _, err := db.Exec(`INSERT INTO smeldr_goals (id, slug, status, created_at, updated_at, description) VALUES ('g-old', 'g-old', 'open', $1, $1, 'Grow the backlog view')`, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	db.Close()

	title := func() string {
		db, err := sql.Open("sqlite", dbPath)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		var v string
		if err := db.QueryRow(`SELECT title FROM smeldr_goals WHERE id = 'g-old'`).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}

	var out, errOut bytes.Buffer
	if code := runMigrateRecordShape(nil, &out, &errOut); code != 0 {
		t.Fatalf("dry run exit %d: %s", code, errOut.String())
	}
	var rep smeldr.RecordShapeMigrationReport
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil || !rep.DryRun || len(rep.Titles) != 1 {
		t.Fatalf("dry-run report = %s, %v", out.String(), err)
	}
	if got := title(); got != "" {
		t.Errorf("a dry run wrote %q", got)
	}

	reportPath := filepath.Join(dir, "report.json")
	out.Reset()
	if code := runMigrateRecordShape([]string{"--apply", "--report=" + reportPath}, &out, &errOut); code != 0 {
		t.Fatalf("apply exit %d: %s", code, errOut.String())
	}
	if got := title(); got != "Grow the backlog view" {
		t.Errorf("title after apply = %q", got)
	}
	if b, err := os.ReadFile(reportPath); err != nil || !bytes.Equal(b, out.Bytes()) {
		t.Errorf("report file = %s, %v", b, err)
	}

	// --type-map: the architect's D112 mapping (T273) as fixture data. A
	// missing file, bad JSON and an entry for no Amendment each exit 1.
	db, err = sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, a := range [][2]string{{"A493", "Level 1"}, {"A490", "Level 2"}} {
		if _, err := db.Exec(`INSERT INTO smeldr_amendments (id, slug, status, created_at, updated_at, address, amendment_type) VALUES ($1, $1, 'merged', $2, $2, $1, $3)`, a[0], now, a[1]); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	typeOf := func(id string) string {
		db, err := sql.Open("sqlite", dbPath)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		var v string
		if err := db.QueryRow(`SELECT amendment_type FROM smeldr_amendments WHERE id = $1`, id).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	badJSON := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(badJSON, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(dir, "missing.json"), badJSON, filepath.Join("testdata", "record_shape_type_map.json")} {
		errOut.Reset()
		if code := runMigrateRecordShape([]string{"--apply", "--type-map=" + path}, &out, &errOut); code != 1 || errOut.Len() == 0 {
			t.Errorf("--type-map=%s exit %d (%s); want 1 with a reason", path, code, errOut.String())
		}
	}
	if got := typeOf("A493"); got != "Level 1" {
		t.Errorf("a refused map wrote %q", got)
	}
	small := filepath.Join(dir, "types.json")
	if err := os.WriteFile(small, []byte(`{"A493": "fix", "490": "feature"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if code := runMigrateRecordShape([]string{"--apply", "--type-map=" + small}, &out, &errOut); code != 0 {
		t.Fatalf("--type-map apply exit %d: %s", code, errOut.String())
	}
	if typeOf("A493") != "fix" || typeOf("A490") != "feature" {
		t.Errorf("types after apply = %q, %q", typeOf("A493"), typeOf("A490"))
	}
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil || len(rep.AmendmentTypes) != 2 || rep.AmendmentTypes[0].Source != "type_map" {
		t.Errorf("type-map report = %s, %v", out.String(), err)
	}

	if code := runMigrateRecordShape([]string{"--nope"}, &out, &errOut); code != 2 {
		t.Errorf("bad flag exit %d; want 2", code)
	}
	t.Setenv("TITLE_CUTOVER", "tomorrow")
	if code := runMigrateRecordShape(nil, &out, &errOut); code != 1 {
		t.Errorf("bad TITLE_CUTOVER exit %d; want 1", code)
	}
}
