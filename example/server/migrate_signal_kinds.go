package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	smeldr "smeldr.dev/core"
)

// runMigrateSignalKinds is the `smeldr-server migrate-signal-kinds` subcommand
// (D115): it opens the configured database, sets the schema up the way boot
// does (so the Signal columns exist), runs smeldr.MigrateSignalKinds and prints
// the report as JSON. It is a dry run unless --apply is given, which writes;
// --report also writes the JSON to that file, for the deploy record. It returns
// the process exit code.
//
//	smeldr-server migrate-signal-kinds                      # dry run, report to stdout
//	smeldr-server migrate-signal-kinds --apply --report=r.json
//
// Read the dry-run report first: changes lists every Signal whose type or
// state moves, opened every ratification-required it creates, and history
// the Signals it leaves as they are.
func runMigrateSignalKinds(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("migrate-signal-kinds", flag.ContinueOnError)
	fs.SetOutput(stderr)
	apply := fs.Bool("apply", false, "apply the migration (default: dry run, nothing written)")
	reportPath := fs.String("report", "", "also write the JSON report to this file")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg := parseConfig()
	dbPath := envOr("DATABASE_PATH", "smeldr.db")
	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(10000)")
	if err != nil {
		fmt.Fprintf(stderr, "migrate-signal-kinds: open db: %v\n", err)
		return 1
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	result, err := buildCommandApp(cfg, db)
	if err != nil {
		fmt.Fprintf(stderr, "migrate-signal-kinds: %v\n", err)
		return 1
	}
	defer result.StopAll()

	rep, err := result.App.MigrateSignalKinds(context.Background(), smeldr.SignalMigrationOptions{Apply: *apply})
	if err != nil {
		fmt.Fprintf(stderr, "migrate-signal-kinds: %v\n", err)
		return 1
	}
	out, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		fmt.Fprintf(stderr, "migrate-signal-kinds: %v\n", err)
		return 1
	}
	out = append(out, '\n')
	if _, err := stdout.Write(out); err != nil {
		return 1
	}
	if *reportPath != "" {
		if err := os.WriteFile(*reportPath, out, 0o600); err != nil {
			fmt.Fprintf(stderr, "migrate-signal-kinds: write report: %v\n", err)
			return 1
		}
	}
	return 0
}

// parseCutover reads a cutover variable: empty is the zero time (warnings
// only), anything else must be an RFC3339 time.
func parseCutover(name, value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s: want an RFC3339 time: %w", name, err)
	}
	return t, nil
}
