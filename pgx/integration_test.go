//go:build integration

package pgx

import (
	"fmt"
	"os"
	"testing"
)

// TestMain refuses a tagged run without a database (T890): every integration
// test would skip, and a run of skips looks green without testing Postgres.
// Run the suite with scripts/pgxcheck, which also prints the line to quote.
func TestMain(m *testing.M) {
	if os.Getenv("DATABASE_URL") == "" {
		fmt.Fprintln(os.Stderr, "pgx integration tests need DATABASE_URL: a run without a database is not a run (see scripts/pgxcheck)")
		os.Exit(1)
	}
	os.Exit(m.Run())
}
