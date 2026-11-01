package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	smeldr "smeldr.dev/core"
)

// migrate-identities: the dry run writes nothing, --apply turns the bootstrap
// token's user_id into an unclassified identity and writes the report file,
// and a second run creates nothing.
func TestRunMigrateIdentities(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SECRET", "test-secret-that-is-at-least-32-bytes")
	t.Setenv("DATABASE_PATH", filepath.Join(dir, "smeldr.db"))
	t.Setenv("BASE_URL", "https://cms.example.com")
	t.Setenv("ENABLE_TOKENS", "1")

	var out, errOut bytes.Buffer
	if code := runMigrateIdentities(nil, &out, &errOut); code != 0 {
		t.Fatalf("dry run exit %d: %s", code, errOut.String())
	}
	var rep smeldr.IdentityMigrationReport
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil || !rep.DryRun || len(rep.Created) != 1 || rep.Created[0].Class != "unclassified" {
		t.Fatalf("dry run = %s, %v", out.String(), err)
	}
	out.Reset()
	reportPath := filepath.Join(dir, "r.json")
	if code := runMigrateIdentities([]string{"--apply", "--report=" + reportPath}, &out, &errOut); code != 0 {
		t.Fatalf("apply exit %d: %s", code, errOut.String())
	}
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil || rep.DryRun || rep.Created[0].Address != "IDN1" {
		t.Fatalf("apply = %s, %v", out.String(), err)
	}
	if b, err := os.ReadFile(reportPath); err != nil || !bytes.Contains(b, []byte(`"address": "IDN1"`)) {
		t.Errorf("report file = %s, %v", b, err)
	}
	out.Reset()
	if code := runMigrateIdentities([]string{"--apply"}, &out, &errOut); code != 0 {
		t.Fatalf("second apply exit %d: %s", code, errOut.String())
	}
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil || len(rep.Created) != 0 {
		t.Errorf("second apply = %s, %v; want nothing created", out.String(), err)
	}
	if code := runMigrateIdentities([]string{"--nope"}, &out, &errOut); code != 2 {
		t.Errorf("bad flag: exit %d", code)
	}
	t.Setenv("TITLE_CUTOVER", "tomorrow")
	if code := runMigrateIdentities(nil, &out, &errOut); code != 1 {
		t.Errorf("bad config: exit %d", code)
	}
	t.Setenv("TITLE_CUTOVER", "")
	t.Setenv("ENABLE_TOKENS", "")
	t.Setenv("DATABASE_PATH", filepath.Join(dir, "fresh.db"))
	if code := runMigrateIdentities(nil, &out, &errOut); code != 1 {
		t.Errorf("no identities table: exit %d", code)
	}
}
