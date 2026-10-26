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

// The subcommand is a dry run by default; --apply writes, and --report also
// writes the JSON report to a file.
func TestRunMigrateWebhookSubscriptions(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "smeldr.db")
	t.Setenv("DATABASE_PATH", dbPath)
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := migrateDB(db); err != nil {
		t.Fatal(err)
	}
	if err := smeldr.CreateWebhookTables(db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO smeldr_webhook_endpoints (id, events, target_url, secret_enc, created_at) VALUES ('e1', '["task.transitioned"]', 'https://x.example', 's', CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	count := func() int {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM smeldr_subscriptions`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	var out, errOut bytes.Buffer
	if code := runMigrateWebhookSubscriptions(nil, &out, &errOut); code != 0 {
		t.Fatalf("dry run exit %d: %s", code, errOut.String())
	}
	var rep smeldr.WebhookMigrationReport
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil || rep.Applied || len(rep.Created) != 1 {
		t.Fatalf("dry-run report = %s, %v", out.String(), err)
	}
	if n := count(); n != 0 {
		t.Errorf("a dry run wrote %d", n)
	}
	reportPath := filepath.Join(dir, "report.json")
	out.Reset()
	if code := runMigrateWebhookSubscriptions([]string{"--apply", "--report=" + reportPath}, &out, &errOut); code != 0 {
		t.Fatalf("apply exit %d: %s", code, errOut.String())
	}
	if n := count(); n != 1 {
		t.Errorf("subscriptions after apply = %d, want 1", n)
	}
	if b, err := os.ReadFile(reportPath); err != nil || !bytes.Equal(b, out.Bytes()) {
		t.Errorf("report file = %s, %v", b, err)
	}
	if code := runMigrateWebhookSubscriptions([]string{"--nope"}, &out, &errOut); code != 2 {
		t.Errorf("bad flag exit %d; want 2", code)
	}
	if code := runMigrateWebhookSubscriptions([]string{"--report=" + filepath.Join(dir, "no", "such", "dir", "r.json")}, &out, &errOut); code != 1 {
		t.Errorf("unwritable report exit %d; want 1", code)
	}
	db.Close()
}
