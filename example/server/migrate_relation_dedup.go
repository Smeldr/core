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

// runMigrateRelationDedup is the `smeldr-server migrate-relation-dedup`
// subcommand (T1019): it ends every second live row of a relation, keeping the
// oldest, then makes the live-tuple unique index, and prints the report as
// JSON. It is a dry run unless --apply is given; --report also writes the JSON
// to that file, for the deploy record. It returns the process exit code.
//
//	smeldr-server migrate-relation-dedup                      # dry run, report to stdout
//	smeldr-server migrate-relation-dedup --apply --report=r.json
//
// Take the database backup first. Rollback: REFERENCE, "One live row per relation".
func runMigrateRelationDedup(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("migrate-relation-dedup", flag.ContinueOnError)
	fs.SetOutput(stderr)
	apply := fs.Bool("apply", false, "apply the migration (default: dry run, nothing written)")
	reportPath := fs.String("report", "", "also write the JSON report to this file")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg := parseConfig()
	dbPath := envOr("DATABASE_PATH", "smeldr.db")
	db, err := sql.Open("sqlite", sqliteDSN(dbPath, maintenanceBusyTimeout))
	if err != nil {
		fmt.Fprintf(stderr, "migrate-relation-dedup: open db: %v\n", err)
		return 1
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	result, err := buildCommandApp(cfg, db)
	if err != nil {
		fmt.Fprintf(stderr, "migrate-relation-dedup: %v\n", err)
		return 1
	}
	defer result.StopAll()

	rep, err := result.App.MigrateRelationDuplicates(context.Background(), smeldr.RelationDedupOptions{DryRun: !*apply})
	if err != nil {
		fmt.Fprintf(stderr, "migrate-relation-dedup: %v\n", err)
		return 1
	}
	out, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		fmt.Fprintf(stderr, "migrate-relation-dedup: %v\n", err)
		return 1
	}
	out = append(out, '\n')
	if _, err := stdout.Write(out); err != nil {
		return 1
	}
	if *reportPath != "" {
		if err := os.WriteFile(*reportPath, out, 0o600); err != nil {
			fmt.Fprintf(stderr, "migrate-relation-dedup: write report: %v\n", err)
			return 1
		}
	}
	return 0
}
