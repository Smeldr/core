package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	smeldr "smeldr.dev/core"
)

// runMigrateReferences is the `smeldr-server migrate-references` subcommand
// (D113): it opens the configured database, sets the schema up the way boot
// does, runs smeldr.MigrateReferences and prints the report as JSON. It is a
// dry run unless --apply is given, which writes; --report also writes the JSON
// to that file, for the deploy record. It returns the process exit code.
//
//	smeldr-server migrate-references                            # dry run, report to stdout
//	smeldr-server migrate-references --ref-field=task_plan.task_ref=Task
//	smeldr-server migrate-references --apply --report=r.json --ref-field=task_plan.task_ref=Task
//
// --ref-field (repeatable) names a runtime-defined reference field its schema
// does not declare yet, as type.field=Type. Read the dry-run report first:
// unresolved lists every value that names no record, left as it is. Run it
// after migrate-signal-kinds.
func runMigrateReferences(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("migrate-references", flag.ContinueOnError)
	fs.SetOutput(stderr)
	apply := fs.Bool("apply", false, "apply the migration (default: dry run, nothing written)")
	reportPath := fs.String("report", "", "also write the JSON report to this file")
	refFields := map[string]string{}
	fs.Func("ref-field", "a reference field its schema does not declare yet, as type.field=Type (repeatable)", func(v string) error {
		k, t, ok := strings.Cut(v, "=")
		if !ok {
			return fmt.Errorf("want type.field=Type, got %q", v)
		}
		refFields[k] = t
		return nil
	})
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg := parseConfig()
	dbPath := envOr("DATABASE_PATH", "smeldr.db")
	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(10000)")
	if err != nil {
		fmt.Fprintf(stderr, "migrate-references: open db: %v\n", err)
		return 1
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	result, err := buildCommandApp(cfg, db)
	if err != nil {
		fmt.Fprintf(stderr, "migrate-references: %v\n", err)
		return 1
	}
	defer result.StopAll()

	rep, err := result.App.MigrateReferences(context.Background(), smeldr.ReferenceMigrationOptions{DryRun: !*apply, RefFields: refFields})
	if err != nil {
		fmt.Fprintf(stderr, "migrate-references: %v\n", err)
		return 1
	}
	out, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		fmt.Fprintf(stderr, "migrate-references: %v\n", err)
		return 1
	}
	out = append(out, '\n')
	if _, err := stdout.Write(out); err != nil {
		return 1
	}
	if *reportPath != "" {
		if err := os.WriteFile(*reportPath, out, 0o600); err != nil {
			fmt.Fprintf(stderr, "migrate-references: write report: %v\n", err)
			return 1
		}
	}
	return 0
}
