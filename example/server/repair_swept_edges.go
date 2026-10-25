package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
)

// runRepairSweptEdges is the `smeldr-server repair-swept-edges` subcommand
// (T932): it opens the configured database, sets the schema up the way boot
// does, runs smeldr.RepairSweptEdges and prints the report as JSON. It is a
// dry run unless --apply is given, which writes; --report also writes the
// JSON to that file, for the deploy record. It returns the process exit code.
//
//	smeldr-server repair-swept-edges                      # dry run, report to stdout
//	smeldr-server repair-swept-edges --apply --report=r.json
//
// Run it only on a release whose structural sweep keeps edges to a type with
// its own state flow, or the next sweep ends the restored edges again. A
// restore sends no event and opens no condition.
func runRepairSweptEdges(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("repair-swept-edges", flag.ContinueOnError)
	fs.SetOutput(stderr)
	apply := fs.Bool("apply", false, "restore the edges (default: dry run, nothing written)")
	reportPath := fs.String("report", "", "also write the JSON report to this file")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg := parseConfig()
	dbPath := envOr("DATABASE_PATH", "smeldr.db")
	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(10000)")
	if err != nil {
		fmt.Fprintf(stderr, "repair-swept-edges: open db: %v\n", err)
		return 1
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	result, err := buildCommandApp(cfg, db)
	if err != nil {
		fmt.Fprintf(stderr, "repair-swept-edges: %v\n", err)
		return 1
	}
	defer result.StopAll()

	rep, err := result.App.RepairSweptEdges(context.Background(), *apply)
	if err != nil {
		fmt.Fprintf(stderr, "repair-swept-edges: %v\n", err)
		return 1
	}
	out, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		fmt.Fprintf(stderr, "repair-swept-edges: %v\n", err)
		return 1
	}
	out = append(out, '\n')
	if _, err := stdout.Write(out); err != nil {
		return 1
	}
	if *reportPath != "" {
		if err := os.WriteFile(*reportPath, out, 0o600); err != nil {
			fmt.Fprintf(stderr, "repair-swept-edges: write report: %v\n", err)
			return 1
		}
	}
	return 0
}
