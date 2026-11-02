package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	smeldr "smeldr.dev/core"
)

// commandRuns is every subcommand in commandEnvs, with the arguments of a
// run that writes nothing a second run would see differently.
var commandRuns = map[string]struct {
	run  func([]string, io.Writer, io.Writer) int
	args []string
}{
	"migrate-identities":      {runMigrateIdentities, nil},
	"migrate-relation-dedup":  {runMigrateRelationDedup, nil},
	"repair-swept-edges":      {runRepairSweptEdges, nil},
	"migrate-places":          {runMigratePlaces, nil},
	"migrate-references":      {runMigrateReferences, nil},
	"migrate-record-shape":    {runMigrateRecordShape, nil},
	"migrate-addresses":       {runMigrateAddresses, nil},
	"migrate-signal-kinds":    {runMigrateSignalKinds, nil},
	"seed-role-subscriptions": {runSeedRoleSubscriptions, []string{"--owner=founder-1", "--band=core"}},
}

// serverFlags is a server's environment with every feature the subcommands
// touch, as process.smeldr.dev runs.
var serverFlags = []string{"ENABLE_TOKENS", "ENABLE_GOVERNANCE", "ENABLE_RELATIONS", "ENABLE_DYNAMIC_CONTENT",
	"ENABLE_ORCHESTRATION", "ENABLE_GOVERNANCE_RECORDS", "ENABLE_AUTHORITY", "ENABLE_PROVENANCE",
	"ENABLE_EVENT_STREAM", "ENABLE_SUBSCRIPTIONS", "ENABLE_WEBHOOKS"}

// clearServerEnv unsets every variable a subcommand could read.
func clearServerEnv(t *testing.T) {
	t.Helper()
	for _, v := range append([]string{"SECRET", "BASE_URL", "TITLE_CUTOVER", "SIGNAL_MESSAGE_CUTOVER", "SIGNAL_EVENT_CUTOVER",
		"PLAN_PATHS_CUTOVER", "REV_CUTOVER"}, serverFlags...) {
		t.Setenv(v, "")
	}
}

// setServerEnv sets the full server environment.
func setServerEnv(t *testing.T) {
	t.Helper()
	t.Setenv("SECRET", "test-secret-that-is-at-least-32-bytes")
	t.Setenv("BASE_URL", "https://cms.example.com")
	for _, v := range serverFlags {
		t.Setenv(v, "1")
	}
}

// serverDatabase boots a server with the full environment on a new file, as
// the server would have before a subcommand runs, and returns its path.
func serverDatabase(t *testing.T) string {
	t.Helper()
	setServerEnv(t)
	path := filepath.Join(t.TempDir(), "server.db")
	db, err := sql.Open("sqlite", sqliteDSN(path, time.Second))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	res, err := buildCommandApp(parseConfig(), db)
	if err != nil {
		t.Fatal(err)
	}
	res.StopAll()
	// A Task and a runtime type, as on process.smeldr.dev.
	if _, err := db.Exec(`INSERT INTO smeldr_dynamic_content (id, slug, type_name, status, fields, created_at, updated_at)
		VALUES ('t1', 't1', 'Task', 'backlog', '{}', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	if err := smeldr.NewSchemaStore(db).Save(context.Background(), &smeldr.ContentTypeSchema{TypeName: "task_plan", Label: "Task plan", Kind: "content"}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

// copyFile copies the database at src to a new file and returns its path.
func copyFile(t *testing.T, src string) string {
	t.Helper()
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "copy.db")
	if err := os.WriteFile(dst, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return dst
}

// report runs the subcommand and returns its JSON report without the
// timing, which differs between two runs.
func report(t *testing.T, name string, dbPath string) (map[string]any, string, int) {
	t.Helper()
	t.Setenv("DATABASE_PATH", dbPath)
	c := commandRuns[name]
	var out, errOut bytes.Buffer
	code := c.run(c.args, &out, &errOut)
	if code != 0 {
		return nil, errOut.String(), code
	}
	var rep map[string]any
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatalf("%s: report %q: %v", name, out.String(), err)
	}
	return withoutRunValues(rep).(map[string]any), errOut.String(), code
}

// withoutRunValues drops what differs between two runs of the same report:
// the timing, and the ids and times of records a run creates.
func withoutRunValues(v any) any {
	switch v := v.(type) {
	case map[string]any:
		for _, k := range []string{"duration_ms", "id", "created_at", "updated_at"} {
			delete(v, k)
		}
		for k, e := range v {
			v[k] = withoutRunValues(e)
		}
	case []any:
		for i, e := range v {
			v[i] = withoutRunValues(e)
		}
	}
	return v
}

// Each subcommand runs with only DATABASE_PATH and the flags commandEnvs
// declares for it, without SECRET or BASE_URL, and gives the same report as
// a run with the server's full environment (T1041).
func TestCommandEnv_DeclaredEnvironmentIsEnough(t *testing.T) {
	if len(commandRuns) != len(commandEnvs) {
		t.Fatalf("commandRuns has %d subcommands, commandEnvs %d", len(commandRuns), len(commandEnvs))
	}
	server := serverDatabase(t)
	for name, env := range commandEnvs {
		t.Run(name, func(t *testing.T) {
			setServerEnv(t)
			full, stderr, code := report(t, name, copyFile(t, server))
			if code != 0 {
				t.Fatalf("full environment: exit %d: %s", code, stderr)
			}
			clearServerEnv(t)
			for _, n := range env.needs {
				t.Setenv(strings.Fields(n.variable)[0], "1")
			}
			if env.loadedTypes {
				// The database holds a Task and a runtime type (serverDatabase).
				t.Setenv("ENABLE_ORCHESTRATION", "1")
				t.Setenv("ENABLE_DYNAMIC_CONTENT", "1")
			}
			got, stderr, code := report(t, name, copyFile(t, server))
			if code != 0 {
				t.Fatalf("declared environment: exit %d: %s", code, stderr)
			}
			if !reflect.DeepEqual(got, full) {
				t.Errorf("declared environment report = %v\nfull environment report = %v", got, full)
			}
		})
	}
}

// Each declared variable left out is refused before the database is opened,
// naming it.
func TestCommandEnv_MissingVariableRefused(t *testing.T) {
	for name, env := range commandEnvs {
		for i, left := range env.needs {
			t.Run(name+"/"+left.variable, func(t *testing.T) {
				clearServerEnv(t)
				for j, n := range env.needs {
					if j != i {
						t.Setenv(strings.Fields(n.variable)[0], "1")
					}
				}
				dbPath := filepath.Join(t.TempDir(), "absent.db")
				_, stderr, code := report(t, name, dbPath)
				if code != 1 || !strings.Contains(stderr, name+": needs "+left.variable) {
					t.Errorf("exit %d, %q; want 1 naming %s", code, stderr, left.variable)
				}
				if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
					t.Errorf("the database was opened: %v", err)
				}
			})
		}
	}
}

// Every entry says whether it signs, and a subcommand that signs requires
// SECRET: it never runs on a throwaway secret.
func TestCommandEnv_Signs(t *testing.T) {
	for name, env := range commandEnvs {
		if env.signs {
			t.Errorf("%s signs; no subcommand did when T1041 was written, so check SECRET is meant", name)
		}
	}
	clearServerEnv(t)
	commandEnvs["test-signs"] = commandEnv{signs: true}
	defer delete(commandEnvs, "test-signs")
	if _, err := commandConfig("test-signs"); err == nil || !strings.Contains(err.Error(), "SECRET") {
		t.Errorf("signing without SECRET: %v; want refused naming SECRET", err)
	}
	if !strings.Contains(commandUsage("test-signs"), "SECRET is required") {
		t.Errorf("usage = %q", commandUsage("test-signs"))
	}
	t.Setenv("SECRET", "test-secret-that-is-at-least-32-bytes")
	if cfg, err := commandConfig("test-signs"); err != nil || cfg.Secret != "test-secret-that-is-at-least-32-bytes" {
		t.Errorf("signing with SECRET: %v, %q", err, cfg.Secret)
	}
}

// Without SECRET and BASE_URL a subcommand gets a random secret of its own
// and the local base URL; with them, it uses them as they are.
func TestCommandConfig_SecretAndBaseURL(t *testing.T) {
	clearServerEnv(t)
	t.Setenv("ENABLE_TOKENS", "1")
	a, err := commandConfig("migrate-identities")
	if err != nil {
		t.Fatal(err)
	}
	b, err := commandConfig("migrate-identities")
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Secret) != 64 || a.Secret == b.Secret {
		t.Errorf("process secrets %q, %q; want two different 64-character secrets", a.Secret, b.Secret)
	}
	if a.BaseURL != commandBaseURL {
		t.Errorf("BaseURL = %q", a.BaseURL)
	}
	t.Setenv("SECRET", "test-secret-that-is-at-least-32-bytes")
	t.Setenv("BASE_URL", "https://cms.example.com")
	c, err := commandConfig("migrate-identities")
	if err != nil || c.Secret != "test-secret-that-is-at-least-32-bytes" || c.BaseURL != "https://cms.example.com" {
		t.Errorf("explicit = %q, %q, %v", c.Secret, c.BaseURL, err)
	}
	if _, err := commandConfig("no-such-subcommand"); err == nil {
		t.Error("an undeclared subcommand: want an error")
	}
}

// A subcommand that reads every loaded type refuses when the database holds a
// Task without ENABLE_ORCHESTRATION, or a runtime type without
// ENABLE_DYNAMIC_CONTENT, naming both, instead of quietly skipping them.
func TestCommandEnv_LoadedTypesNeedTheirFlags(t *testing.T) {
	server := serverDatabase(t)
	for name, env := range commandEnvs {
		if !env.loadedTypes {
			continue
		}
		t.Run(name, func(t *testing.T) {
			clearServerEnv(t)
			for _, n := range env.needs {
				t.Setenv(strings.Fields(n.variable)[0], "1")
			}
			_, stderr, code := report(t, name, copyFile(t, server))
			if code != 1 || !strings.Contains(stderr, "needs ENABLE_ORCHESTRATION (the database holds 1 Task") ||
				!strings.Contains(stderr, "; ENABLE_DYNAMIC_CONTENT (the database defines 1 runtime types") {
				t.Errorf("exit %d, %q; want refused naming ENABLE_ORCHESTRATION and ENABLE_DYNAMIC_CONTENT", code, stderr)
			}
			t.Setenv("ENABLE_ORCHESTRATION", "1")
			t.Setenv("ENABLE_DYNAMIC_CONTENT", "1")
			if _, stderr, code := report(t, name, copyFile(t, server)); code != 0 {
				t.Errorf("with both flags: exit %d, %s", code, stderr)
			}
		})
	}
	// No dynamic content table at all: nothing to skip.
	empty, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "empty.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer empty.Close()
	if err := checkLoadedTypes(t.Context(), empty, "migrate-addresses", ServerConfig{}); err != nil {
		t.Errorf("no table: %v", err)
	}
}

// -h prints the flags and then the environment the subcommand reads.
func TestCommandEnv_Usage(t *testing.T) {
	for name, c := range commandRuns {
		var out, errOut bytes.Buffer
		if code := c.run([]string{"-h"}, &out, &errOut); code != 2 {
			t.Errorf("%s -h: exit %d", name, code)
		}
		if !strings.Contains(errOut.String(), "Environment: DATABASE_PATH (default smeldr.db)") ||
			!strings.Contains(errOut.String(), "SECRET and BASE_URL are optional") {
			t.Errorf("%s -h = %q", name, errOut.String())
		}
	}
}
