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

// runMigrateAddresses is the `smeldr-server migrate-addresses` subcommand (D110
// Phase 1): it opens the configured database, sets the schema up the way boot
// does (so every record type has its address prefix), runs
// smeldr.MigrateAddresses and prints the report as JSON. It is a dry run unless
// --apply is given, which writes; --report also writes the JSON to that file,
// for the deploy record. It returns the process exit code.
//
//	smeldr-server migrate-addresses                      # dry run, report to stdout
//	smeldr-server migrate-addresses --apply --report=r.json
//
// On SQLite the migration holds the database's one writer for its whole
// transaction (duration_ms in the report); stop the server for --apply unless a
// dry run showed that time is short.
func runMigrateAddresses(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("migrate-addresses", flag.ContinueOnError)
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
		fmt.Fprintf(stderr, "migrate-addresses: open db: %v\n", err)
		return 1
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	result, err := buildApp(cfg, db)
	if err != nil {
		fmt.Fprintf(stderr, "migrate-addresses: %v\n", err)
		return 1
	}
	defer result.StopAll()
	// Handler registers every loaded record type's address prefix.
	_ = result.App.Handler()

	rep, err := result.App.MigrateAddresses(context.Background(), smeldr.AddressMigrationOptions{DryRun: !*apply})
	if err != nil {
		fmt.Fprintf(stderr, "migrate-addresses: %v\n", err)
		return 1
	}
	out, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		fmt.Fprintf(stderr, "migrate-addresses: %v\n", err)
		return 1
	}
	out = append(out, '\n')
	if _, err := stdout.Write(out); err != nil {
		return 1
	}
	if *reportPath != "" {
		if err := os.WriteFile(*reportPath, out, 0o600); err != nil {
			fmt.Fprintf(stderr, "migrate-addresses: write report: %v\n", err)
			return 1
		}
	}
	return 0
}
