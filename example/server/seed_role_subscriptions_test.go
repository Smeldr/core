package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	smeldr "smeldr.dev/core"
)

// seedEnv points the subcommand at a fresh database in dir.
func seedEnv(t *testing.T, dir string) string {
	t.Helper()
	dbPath := filepath.Join(dir, "smeldr.db")
	t.Setenv("SECRET", "test-secret-that-is-at-least-32-bytes")
	t.Setenv("DATABASE_PATH", dbPath)
	t.Setenv("BASE_URL", "https://cms.example.com")
	t.Setenv("ENABLE_ORCHESTRATION", "1")
	t.Setenv("ENABLE_RELATIONS", "1")
	return dbPath
}

// runSeed runs the subcommand and decodes its report.
func runSeed(t *testing.T, args ...string) (smeldr.RoleSeedReport, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	if code := runSeedRoleSubscriptions(args, &out, &errOut); code != 0 {
		t.Fatalf("%v: exit %d: %s", args, code, errOut.String())
	}
	var rep smeldr.RoleSeedReport
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatalf("report = %s, %v", out.String(), err)
	}
	return rep, errOut.String()
}

// names lists the names of subs.
func names(subs []smeldr.Subscription) string {
	var n []string
	for _, s := range subs {
		n = append(n, s.Name)
	}
	return strings.Join(n, "|")
}

// Without task_plan defined, the plan subscription is left out and named in
// the report and on stderr; bad flags are 2, a bad config is 1.
func TestRunSeedRoleSubscriptions(t *testing.T) {
	seedEnv(t, t.TempDir())
	rep, stderr := runSeed(t, "--owner=core-token", "--band=core")
	if len(rep.Created) != 2 || len(rep.LeftOut) != 1 || rep.LeftOut[0].Type != "task_plan" {
		t.Fatalf("report = %+v", rep)
	}
	if !strings.Contains(stderr, `left out "Plan reviews in band core": type task_plan is not defined here`) {
		t.Errorf("stderr = %q", stderr)
	}
	var out, errOut bytes.Buffer
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

// With task_plan defined as a runtime type (as on process.smeldr.dev), the
// plan subscriptions are created; a rerun creates nothing (T859).
func TestRunSeedRoleSubscriptions_RuntimeTaskPlan(t *testing.T) {
	dbPath := seedEnv(t, t.TempDir())
	t.Setenv("ENABLE_DYNAMIC_CONTENT", "1")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := smeldr.CreateSchemaTable(db); err != nil {
		t.Fatal(err)
	}
	if err := smeldr.NewSchemaStore(db).Save(context.Background(), &smeldr.ContentTypeSchema{TypeName: "task_plan", Label: "Task plan", Kind: "content"}); err != nil {
		t.Fatal(err)
	}
	db.Close()

	rep, _ := runSeed(t, "--owner=core-token", "--band=core")
	if got := names(rep.Created); got != "Tasks in band core|Plan reviews in band core|Signals to core" || len(rep.LeftOut) != 0 {
		t.Errorf("band seed = %s, left out %+v", got, rep.LeftOut)
	}
	rep, _ = runSeed(t, "--owner=arch-token", "--architect")
	if len(rep.Created) != 6 || !strings.Contains(names(rep.Created), "Plans submitted") {
		t.Errorf("architect seed = %s", names(rep.Created))
	}
	rep, stderr := runSeed(t, "--owner=core-token", "--band=core")
	if len(rep.Created) != 0 || len(rep.LeftOut) != 0 || stderr != "" {
		t.Errorf("rerun = %+v, stderr %q; want nothing", rep, stderr)
	}
}

// buildCommandApp gives a subcommand the running server's registry, runtime
// types included; buildApp alone does not.
func TestBuildCommandApp_LoadsRuntimeTypes(t *testing.T) {
	dbPath := seedEnv(t, t.TempDir())
	t.Setenv("ENABLE_DYNAMIC_CONTENT", "1")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := smeldr.CreateSchemaTable(db); err != nil {
		t.Fatal(err)
	}
	if err := smeldr.NewSchemaStore(db).Save(context.Background(), &smeldr.ContentTypeSchema{TypeName: "task_plan", Label: "Task plan", Kind: "content"}); err != nil {
		t.Fatal(err)
	}
	plain, err := buildApp(parseConfig(), db)
	if err != nil {
		t.Fatal(err)
	}
	defer plain.StopAll()
	if plain.App.TypeRegistry().Lookup("task_plan") != nil {
		t.Error("buildApp alone loaded task_plan; the early Handler call order changed, recheck buildCommandApp")
	}
	cmd, err := buildCommandApp(parseConfig(), db)
	if err != nil {
		t.Fatal(err)
	}
	defer cmd.StopAll()
	if cmd.App.TypeRegistry().Lookup("task_plan") == nil {
		t.Error("buildCommandApp did not load task_plan")
	}
	t.Setenv("TITLE_CUTOVER", "tomorrow")
	if _, err := buildCommandApp(parseConfig(), db); err == nil {
		t.Error("a bad config passed")
	}
}
