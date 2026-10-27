package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	smeldr "smeldr.dev/core"
)

// migrate-places on an instance that still keeps a runtime domain type: the
// server boots (Organization waits for the migration), the dry run writes nothing,
// --apply moves the Domain, creates Organization and writes the report file.
func TestRunMigratePlaces(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "smeldr.db")
	t.Setenv("SECRET", "test-secret-that-is-at-least-32-bytes")
	t.Setenv("DATABASE_PATH", dbPath)
	t.Setenv("BASE_URL", "https://cms.example.com")
	t.Setenv("ENABLE_DYNAMIC_CONTENT", "1")
	t.Setenv("ENABLE_RELATIONS", "1")
	t.Setenv("ORGANIZATION_NAME", "Acme")

	// Before D142: the runtime domain type with one record.
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	pre := smeldr.New(smeldr.MustConfig(smeldr.Config{BaseURL: "https://cms.example.com", Secret: []byte("test-secret-that-is-at-least-32-bytes"), DB: db}))
	pre.ServeDynamicContent()
	ctx := context.Background()
	if _, err := pre.DefineContentType(ctx, &smeldr.ContentTypeSchema{TypeName: "domain", Label: "Domain", Kind: "content",
		Fields: []byte(`[{"name":"name","type":"string","role":"title"}]`)}); err != nil {
		t.Fatal(err)
	}
	repo, err := pre.DynamicContentRepo("domain")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateDraft(ctx, map[string]any{"name": "core"}); err != nil {
		t.Fatal(err)
	}
	db.Close()

	t.Setenv("ENABLE_GOVERNANCE_RECORDS", "1")
	t.Setenv("ENABLE_RELATIONS", "1")
	var out, errOut bytes.Buffer
	if code := runMigratePlaces(nil, &out, &errOut); code != 0 {
		t.Fatalf("dry run exit %d: %s", code, errOut.String())
	}
	var rep smeldr.PlaceMigrationReport
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil || !rep.DryRun || len(rep.Moved) != 1 {
		t.Fatalf("dry run = %s, %v", out.String(), err)
	}
	out.Reset()
	reportPath := filepath.Join(dir, "r.json")
	if code := runMigratePlaces([]string{"--apply", "--report=" + reportPath}, &out, &errOut); code != 0 {
		t.Fatalf("apply exit %d: %s", code, errOut.String())
	}
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil || rep.DryRun || rep.Organization != "ORG1" || rep.Moved[0].Address != "DOM1" {
		t.Fatalf("apply = %s, %v", out.String(), err)
	}
	if b, err := os.ReadFile(reportPath); err != nil || !bytes.Contains(b, []byte(`"organization": "ORG1"`)) {
		t.Errorf("report file = %s, %v", b, err)
	}
	if code := runMigratePlaces([]string{"--nope"}, &out, &errOut); code != 2 {
		t.Errorf("bad flag: exit %d", code)
	}
	t.Setenv("TITLE_CUTOVER", "tomorrow")
	if code := runMigratePlaces(nil, &out, &errOut); code != 1 {
		t.Errorf("bad config: exit %d", code)
	}
}
