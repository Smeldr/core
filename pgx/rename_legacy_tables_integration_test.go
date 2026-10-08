//go:build integration

package pgx

import (
	"context"
	"testing"

	smeldr "smeldr.dev/core"
)

// RenameLegacyTables on Postgres: a module's legacy table is renamed with its
// rows, an absent pair is skipped, and a second call is a no-op.
func TestPG_RenameLegacyTables(t *testing.T) {
	db, _ := isolatedDB(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `CREATE TABLE forge_media (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO forge_media (id) VALUES ('m1')`); err != nil {
		t.Fatal(err)
	}
	pairs := [][2]string{{"forge_media", "smeldr_media"}, {"forge_absent", "smeldr_absent"}}
	for i := 0; i < 2; i++ {
		if err := smeldr.RenameLegacyTables(ctx, db, pairs); err != nil {
			t.Fatalf("call %d: %v", i+1, err)
		}
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM smeldr_media`).Scan(&n); err != nil || n != 1 {
		t.Errorf("smeldr_media rows = %d (%v); want 1", n, err)
	}
}
