package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	smeldr "smeldr.dev/core"
)

// runMigrateRecordShape is the `smeldr-server migrate-record-shape` subcommand
// (D112): it opens the configured database, sets the schema up the way boot
// does (so the title columns exist), runs smeldr.MigrateRecordShape and prints
// the report as JSON. It is a dry run unless --apply is given, which writes;
// --report also writes the JSON to that file, for the deploy record. It returns
// the process exit code.
//
//	smeldr-server migrate-record-shape                      # dry run, report to stdout
//	smeldr-server migrate-record-shape --apply --report=r.json
//
// Read the dry-run report first: unmapped_amendment_types lists values a
// person must map by hand, and titles lists every title the migration sets.
func runMigrateRecordShape(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("migrate-record-shape", flag.ContinueOnError)
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
		fmt.Fprintf(stderr, "migrate-record-shape: open db: %v\n", err)
		return 1
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	result, err := buildApp(cfg, db)
	if err != nil {
		fmt.Fprintf(stderr, "migrate-record-shape: %v\n", err)
		return 1
	}
	defer result.StopAll()

	rep, err := result.App.MigrateRecordShape(context.Background(), smeldr.RecordShapeMigrationOptions{DryRun: !*apply})
	if err != nil {
		fmt.Fprintf(stderr, "migrate-record-shape: %v\n", err)
		return 1
	}
	out, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		fmt.Fprintf(stderr, "migrate-record-shape: %v\n", err)
		return 1
	}
	out = append(out, '\n')
	if _, err := stdout.Write(out); err != nil {
		return 1
	}
	if *reportPath != "" {
		if err := os.WriteFile(*reportPath, out, 0o600); err != nil {
			fmt.Fprintf(stderr, "migrate-record-shape: write report: %v\n", err)
			return 1
		}
	}
	return 0
}
