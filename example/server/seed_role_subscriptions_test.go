package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"
)

// seed-role-subscriptions creates a role token's subscriptions once; bad
// flags are 2.
func TestRunSeedRoleSubscriptions(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SECRET", "test-secret-that-is-at-least-32-bytes")
	t.Setenv("DATABASE_PATH", filepath.Join(dir, "smeldr.db"))
	t.Setenv("BASE_URL", "https://cms.example.com")
	t.Setenv("ENABLE_ORCHESTRATION", "1")
	var out, errOut bytes.Buffer
	if code := runSeedRoleSubscriptions([]string{"--owner=core-token", "--band=core"}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	var created []map[string]any
	if err := json.Unmarshal(out.Bytes(), &created); err != nil || len(created) == 0 {
		t.Fatalf("created = %s, %v", out.String(), err)
	}
	out.Reset()
	if code := runSeedRoleSubscriptions([]string{"--owner=core-token", "--band=core"}, &out, &errOut); code != 0 {
		t.Fatalf("second run exit %d", code)
	}
	if bytes.TrimSpace(out.Bytes()) != nil && string(bytes.TrimSpace(out.Bytes())) != "null" {
		t.Errorf("second run created %s, want nothing", out.String())
	}
	if code := runSeedRoleSubscriptions([]string{"--owner=x"}, &out, &errOut); code != 2 {
		t.Errorf("no band: exit %d, want 2", code)
	}
	if code := runSeedRoleSubscriptions([]string{"--nope"}, &out, &errOut); code != 2 {
		t.Errorf("bad flag: exit %d, want 2", code)
	}
	t.Setenv("TITLE_CUTOVER", "tomorrow")
	if code := runSeedRoleSubscriptions([]string{"--owner=x", "--architect"}, &out, &errOut); code != 1 {
		t.Errorf("bad config: exit %d, want 1", code)
	}
}
