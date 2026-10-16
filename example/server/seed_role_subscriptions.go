package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"

	smeldr "smeldr.dev/core"
)

// runSeedRoleSubscriptions is the `smeldr-server seed-role-subscriptions`
// subcommand (D116): it gives one role token its role subscriptions and its
// universal defaults, and prints the subscriptions it created as JSON. It is
// idempotent: run again, it creates nothing. It returns the process exit
// code.
//
//	smeldr-server seed-role-subscriptions --owner=<token user id> --band=core
//	smeldr-server seed-role-subscriptions --owner=<token user id> --architect
func runSeedRoleSubscriptions(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("seed-role-subscriptions", flag.ContinueOnError)
	fs.SetOutput(stderr)
	owner := fs.String("owner", "", "the role token's user ID (required)")
	band := fs.String("band", "", "the role's band: core, site, cloud, devops, brand")
	architect := fs.Bool("architect", false, "add the architect's review subscriptions")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *owner == "" || (*band == "" && !*architect) {
		fmt.Fprintln(stderr, "seed-role-subscriptions: --owner and --band or --architect are required")
		return 2
	}
	cfg := parseConfig()
	dbPath := envOr("DATABASE_PATH", "smeldr.db")
	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(10000)")
	if err != nil {
		fmt.Fprintf(stderr, "seed-role-subscriptions: open db: %v\n", err)
		return 1
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	result, err := buildApp(cfg, db)
	if err != nil {
		fmt.Fprintf(stderr, "seed-role-subscriptions: %v\n", err)
		return 1
	}
	defer result.StopAll()
	if err := smeldr.CreateSubscriptionTables(db); err != nil {
		fmt.Fprintf(stderr, "seed-role-subscriptions: %v\n", err)
		return 1
	}
	created, err := result.App.SeedRoleSubscriptionsTrusted(context.Background(), *owner, smeldr.RoleDefaults{Band: *band, Architect: *architect})
	if err != nil {
		fmt.Fprintf(stderr, "seed-role-subscriptions: %v\n", err)
		return 1
	}
	out, _ := json.MarshalIndent(created, "", "  ")
	if _, err := fmt.Fprintln(stdout, string(out)); err != nil {
		return 1
	}
	return 0
}
