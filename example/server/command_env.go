package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"strings"
)

// commandNeed is one environment variable a maintenance subcommand needs, and
// why: the tables or types it reads are made or loaded only with it.
type commandNeed struct {
	variable string
	why      string
	set      func(ServerConfig) bool
}

// commandEnv is what one maintenance subcommand needs from the environment
// (T1041). DATABASE_PATH (default smeldr.db) is always read.
type commandEnv struct {
	needs []commandNeed
	// signs reports whether the subcommand signs anything that outlives it.
	// Such a subcommand requires SECRET; every other one may run without it
	// and gets a random secret for its own process.
	signs bool
	// loadedTypes reports whether the subcommand reads records of every type
	// the instance loads: the add-on types (Task, Goal, Run) are loaded only
	// with ENABLE_ORCHESTRATION, and the runtime types (task_plan and the
	// rest) only with ENABLE_DYNAMIC_CONTENT. With such records or types in
	// the database and the flag off, it refuses instead of quietly doing less
	// (checkLoadedTypes).
	loadedTypes bool
}

var (
	needTokens = commandNeed{"ENABLE_TOKENS", "the identities table and the tokens' identity column are made with tokens",
		func(c ServerConfig) bool { return c.EnableTokens }}
	needRelations = commandNeed{"ENABLE_RELATIONS", "the relation store is wired with relations",
		func(c ServerConfig) bool { return c.EnableRelations }}
	needRecords = commandNeed{"ENABLE_GOVERNANCE_RECORDS (or ENABLE_ORCHESTRATION)", "the governance record types are loaded with them",
		func(c ServerConfig) bool { return c.governanceRecords() }}
)

// commandEnvs is every maintenance subcommand that boots the App, and what
// it needs. A new subcommand adds its entry here; the test checks each one
// runs with exactly this environment.
var commandEnvs = map[string]commandEnv{
	"migrate-identities":      {needs: []commandNeed{needTokens}},
	"migrate-relation-dedup":  {needs: []commandNeed{needRelations}},
	"repair-swept-edges":      {needs: []commandNeed{needRelations}},
	"migrate-places":          {needs: []commandNeed{needRelations, needRecords}},
	"migrate-references":      {needs: []commandNeed{needRelations, needRecords}, loadedTypes: true},
	"migrate-record-shape":    {needs: []commandNeed{needRelations, needRecords}, loadedTypes: true},
	"migrate-addresses":       {needs: []commandNeed{needRelations, needRecords}, loadedTypes: true},
	"migrate-signal-kinds":    {needs: []commandNeed{needRelations, needRecords}},
	"seed-role-subscriptions": {needs: []commandNeed{needRelations, needRecords}, loadedTypes: true},
}

// commandBaseURL is the BASE_URL a subcommand uses when none is set: it
// serves nothing and links nothing.
const commandBaseURL = "http://localhost"

// commandConfig reads the configuration for the maintenance subcommand name.
// SECRET and BASE_URL are optional unless the subcommand signs: without them
// it gets a random secret for its own process and commandBaseURL. Every
// variable it needs and lacks is named in one error, before the database is
// opened.
func commandConfig(name string) (ServerConfig, error) {
	env, ok := commandEnvs[name]
	if !ok {
		return ServerConfig{}, fmt.Errorf("%s: no environment declared for this subcommand", name)
	}
	cfg := readConfig()
	var missing []string
	if env.signs && cfg.Secret == "" {
		missing = append(missing, "SECRET (it signs what it writes)")
	}
	for _, n := range env.needs {
		if !n.set(cfg) {
			missing = append(missing, n.variable+" ("+n.why+")")
		}
	}
	if len(missing) > 0 {
		return ServerConfig{}, fmt.Errorf("needs %s", strings.Join(missing, "; "))
	}
	if cfg.Secret == "" {
		secret, err := processSecret()
		if err != nil {
			return ServerConfig{}, err
		}
		cfg.Secret = secret
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = commandBaseURL
	}
	return cfg, nil
}

// processSecret is a secret for one subcommand process only, never written
// anywhere: anything signed with it cannot be verified afterwards.
func processSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("make a process secret: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// checkLoadedTypes refuses a subcommand that reads records of every loaded
// type when the database holds records of a type the environment does not
// load: Task, Goal or Run rows without ENABLE_ORCHESTRATION, or runtime
// types without ENABLE_DYNAMIC_CONTENT. The subcommand would quietly skip
// them. A database without those tables holds nothing to skip.
func checkLoadedTypes(ctx context.Context, db *sql.DB, name string, cfg ServerConfig) error {
	if !commandEnvs[name].loadedTypes {
		return nil
	}
	var missing []string
	checks := []struct {
		on    bool
		query string
		need  string
	}{
		{cfg.EnableOrchestration, `SELECT COUNT(*) FROM smeldr_dynamic_content WHERE type_name IN ('Task', 'Goal', 'Run')`,
			"ENABLE_ORCHESTRATION (the database holds %d Task, Goal or Run records, whose types are loaded only with it)"},
		{cfg.EnableDynamicContent, `SELECT COUNT(*) FROM smeldr_content_type_schemas WHERE kind = 'content'`,
			"ENABLE_DYNAMIC_CONTENT (the database defines %d runtime types, loaded only with it)"},
	}
	for _, c := range checks {
		if c.on {
			continue
		}
		var n int
		if err := db.QueryRowContext(ctx, c.query).Scan(&n); err != nil {
			if strings.Contains(err.Error(), "no such table") {
				continue
			}
			return fmt.Errorf("count records by type: %w", err)
		}
		if n > 0 {
			missing = append(missing, fmt.Sprintf(c.need, n))
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("needs %s", strings.Join(missing, "; "))
	}
	return nil
}

// commandUsage is the usage line for a subcommand's -h: its flags, then the
// environment it reads.
func commandUsage(name string) string {
	env := commandEnvs[name]
	vars := []string{"DATABASE_PATH (default smeldr.db)"}
	for _, n := range env.needs {
		vars = append(vars, n.variable)
	}
	secret := "SECRET and BASE_URL are optional"
	if env.signs {
		secret = "SECRET is required; BASE_URL is optional"
	}
	return fmt.Sprintf("Environment: %s; %s.", strings.Join(vars, ", "), secret)
}

// newCommandFlags is the flag set of the subcommand name: its -h prints the
// flags, then the environment the subcommand reads.
func newCommandFlags(name string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintf(stderr, "Usage of %s:\n", name)
		fs.PrintDefaults()
		fmt.Fprintln(stderr, commandUsage(name))
	}
	return fs
}
