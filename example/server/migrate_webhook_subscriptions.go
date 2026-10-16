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

// runMigrateWebhookSubscriptions is the `smeldr-server
// migrate-webhook-subscriptions` subcommand (D116): it gives every webhook
// endpoint that has no subscription its own, owned by legacy-admin and
// delivering the event names the endpoint was created for, and prints the
// report as JSON. It is a dry run unless --apply is given; --report also
// writes the JSON to that file, for the deploy record. It returns the process
// exit code.
//
//	smeldr-server migrate-webhook-subscriptions                      # dry run
//	smeldr-server migrate-webhook-subscriptions --apply --report=r.json
func runMigrateWebhookSubscriptions(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("migrate-webhook-subscriptions", flag.ContinueOnError)
	fs.SetOutput(stderr)
	apply := fs.Bool("apply", false, "apply the migration (default: dry run, nothing written)")
	reportPath := fs.String("report", "", "also write the JSON report to this file")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	dbPath := envOr("DATABASE_PATH", "smeldr.db")
	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(10000)")
	if err != nil {
		fmt.Fprintf(stderr, "migrate-webhook-subscriptions: open db: %v\n", err)
		return 1
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err := smeldr.CreateSubscriptionTables(db); err != nil {
		fmt.Fprintf(stderr, "migrate-webhook-subscriptions: %v\n", err)
		return 1
	}
	rep, err := smeldr.MigrateWebhookSubscriptions(context.Background(), db, smeldr.WebhookMigrationOptions{Apply: *apply})
	if err != nil {
		fmt.Fprintf(stderr, "migrate-webhook-subscriptions: %v\n", err)
		return 1
	}
	out, _ := json.MarshalIndent(rep, "", "  ")
	out = append(out, '\n')
	if _, err := stdout.Write(out); err != nil {
		return 1
	}
	if *reportPath != "" {
		if err := os.WriteFile(*reportPath, out, 0o600); err != nil {
			fmt.Fprintf(stderr, "migrate-webhook-subscriptions: write report: %v\n", err)
			return 1
		}
	}
	return 0
}
