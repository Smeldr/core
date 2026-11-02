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

// migrate-relation-dedup: on an instance with two live rows of one relation,
// the dry run writes nothing, --apply keeps the oldest and writes the report,
// and a second run finds nothing.
func TestRunMigrateRelationDedup(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "smeldr.db")
	t.Setenv("SECRET", "test-secret-that-is-at-least-32-bytes")
	t.Setenv("DATABASE_PATH", dbPath)
	t.Setenv("BASE_URL", "https://cms.example.com")
	t.Setenv("ENABLE_RELATIONS", "1")

	var out, errOut bytes.Buffer
	if code := runMigrateRelationDedup(nil, &out, &errOut); code != 0 {
		t.Fatalf("first run (creates the tables) exit %d: %s", code, errOut.String())
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DROP INDEX idx_relations_live_tuple`); err != nil {
		t.Fatal(err)
	}
	for _, r := range []struct{ id, at string }{{"old", "2026-01-01T00:00:00Z"}, {"new", "2026-02-01T00:00:00Z"}} {
		if _, err := db.Exec(`INSERT INTO smeldr_relations (id, source_type, source_id, target_type, target_id, relation_kind, edge_class, attributes, created_at, updated_at)
			VALUES ($1, 'post', 'p1', 'source', 's1', 'cites', 'asserted', '{}', $2, $2)`, r.id, r.at); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	out.Reset()
	if code := runMigrateRelationDedup(nil, &out, &errOut); code != 0 {
		t.Fatalf("dry run exit %d: %s", code, errOut.String())
	}
	var rep smeldr.RelationDedupReport
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil || !rep.DryRun || len(rep.Duplicates) != 1 || rep.Duplicates[0].Kept != "old" {
		t.Fatalf("dry run = %s, %v", out.String(), err)
	}
	out.Reset()
	reportPath := filepath.Join(dir, "r.json")
	if code := runMigrateRelationDedup([]string{"--apply", "--report=" + reportPath}, &out, &errOut); code != 0 {
		t.Fatalf("apply exit %d: %s", code, errOut.String())
	}
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil || rep.DryRun || rep.RowsEnded != 1 || !rep.IndexMade {
		t.Fatalf("apply = %s, %v", out.String(), err)
	}
	if b, err := os.ReadFile(reportPath); err != nil || !bytes.Contains(b, []byte(`"kept": "old"`)) {
		t.Errorf("report file = %s, %v", b, err)
	}
	out.Reset()
	if code := runMigrateRelationDedup([]string{"--apply"}, &out, &errOut); code != 0 {
		t.Fatalf("second apply exit %d: %s", code, errOut.String())
	}
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil || len(rep.Duplicates) != 0 {
		t.Errorf("second apply = %s, %v", out.String(), err)
	}
	if code := runMigrateRelationDedup([]string{"--nope"}, &out, &errOut); code != 2 {
		t.Errorf("bad flag: exit %d", code)
	}
	t.Setenv("TITLE_CUTOVER", "tomorrow")
	if code := runMigrateRelationDedup(nil, &out, &errOut); code != 1 {
		t.Errorf("bad config: exit %d", code)
	}
	t.Setenv("TITLE_CUTOVER", "")
	t.Setenv("ENABLE_RELATIONS", "")
	if code := runMigrateRelationDedup(nil, &out, &errOut); code != 1 {
		t.Errorf("no relations: exit %d", code)
	}
}
