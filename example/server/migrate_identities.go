package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"

	smeldr "smeldr.dev/core"
)

// runMigrateIdentities is the `smeldr-server migrate-identities` subcommand
// (D165, T1014): it turns every existing token's user_id into one identity
// with the same id and links the tokens to it, and prints the report as JSON,
// identities by address. It is a dry run unless --apply is given; --report
// also writes the JSON to that file, for the deploy record. It returns the
// process exit code.
//
//	smeldr-server migrate-identities                      # dry run, report to stdout
//	smeldr-server migrate-identities --apply --report=r.json
//
// Take the database backup first. Rollback: REFERENCE, "Identities".
func runMigrateIdentities(args []string, stdout, stderr io.Writer) int {
	fs := newCommandFlags("migrate-identities", stderr)
	apply := fs.Bool("apply", false, "apply the migration (default: dry run, nothing written)")
	reportPath := fs.String("report", "", "also write the JSON report to this file")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg, err := commandConfig("migrate-identities")
	if err != nil {
		fmt.Fprintf(stderr, "migrate-identities: %v\n", err)
		return 1
	}
	dbPath := envOr("DATABASE_PATH", "smeldr.db")
	db, err := sql.Open("sqlite", sqliteDSN(dbPath, maintenanceBusyTimeout))
	if err != nil {
		fmt.Fprintf(stderr, "migrate-identities: open db: %v\n", err)
		return 1
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	result, err := buildCommandApp(cfg, db)
	if err != nil {
		fmt.Fprintf(stderr, "migrate-identities: %v\n", err)
		return 1
	}
	defer result.StopAll()

	rep, err := result.App.MigrateIdentities(context.Background(), smeldr.IdentityMigrationOptions{DryRun: !*apply})
	if err != nil {
		fmt.Fprintf(stderr, "migrate-identities: %v\n", err)
		return 1
	}
	out, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		fmt.Fprintf(stderr, "migrate-identities: %v\n", err)
		return 1
	}
	out = append(out, '\n')
	if _, err := stdout.Write(out); err != nil {
		return 1
	}
	if *reportPath != "" {
		if err := os.WriteFile(*reportPath, out, 0o600); err != nil {
			fmt.Fprintf(stderr, "migrate-identities: write report: %v\n", err)
			return 1
		}
	}
	return 0
}
