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

// runMigratePlaces is the `smeldr-server migrate-places` subcommand (D142): it
// moves the runtime domain, area and set types an instance defined before
// D142 to the compiled Domain, Area and Set, creates Root and the default
// Domain, and prints the report as JSON. It is a dry run unless --apply is
// given; --report also writes the JSON to that file, for the deploy record.
// It returns the process exit code.
//
//	smeldr-server migrate-places                      # dry run, report to stdout
//	smeldr-server migrate-places --apply --report=r.json
//
// Take the database backup first. Rollback: REFERENCE, "Places".
func runMigratePlaces(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("migrate-places", flag.ContinueOnError)
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
		fmt.Fprintf(stderr, "migrate-places: open db: %v\n", err)
		return 1
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	result, err := buildCommandApp(cfg, db)
	if err != nil {
		fmt.Fprintf(stderr, "migrate-places: %v\n", err)
		return 1
	}
	defer result.StopAll()

	rep, err := result.App.MigratePlaces(context.Background(), smeldr.PlaceMigrationOptions{DryRun: !*apply, RootName: cfg.organizationName()})
	if err != nil {
		fmt.Fprintf(stderr, "migrate-places: %v\n", err)
		return 1
	}
	out, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		fmt.Fprintf(stderr, "migrate-places: %v\n", err)
		return 1
	}
	out = append(out, '\n')
	if _, err := stdout.Write(out); err != nil {
		return 1
	}
	if *reportPath != "" {
		if err := os.WriteFile(*reportPath, out, 0o600); err != nil {
			fmt.Fprintf(stderr, "migrate-places: write report: %v\n", err)
			return 1
		}
	}
	return 0
}
