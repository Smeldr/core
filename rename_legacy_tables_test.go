// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"errors"
	"testing"
)

// RenameLegacyTables renames a module's own pairs: a table that exists is
// renamed, an absent one is skipped, and a call again changes nothing.
func TestRenameLegacyTables_ModulePairs(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()
	if _, err := db.Exec(`CREATE TABLE forge_media (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO forge_media (id) VALUES ('m1')`); err != nil {
		t.Fatal(err)
	}
	pairs := [][2]string{{"forge_media", "smeldr_media"}, {"forge_absent", "smeldr_absent"}}
	for i := 0; i < 2; i++ {
		if err := RenameLegacyTables(ctx, db, pairs); err != nil {
			t.Fatalf("call %d: %v", i+1, err)
		}
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM smeldr_media`).Scan(&n); err != nil || n != 1 {
		t.Errorf("smeldr_media rows = %d (%v); want the renamed row", n, err)
	}
	if ok, _ := tableExists(ctx, db, "forge_media"); ok {
		t.Error("forge_media still exists")
	}
}

// A name that is not a plain lower-case identifier is refused before anything
// is renamed.
func TestRenameLegacyTables_RefusesOddNames(t *testing.T) {
	db := newSQLiteDB(t)
	if _, err := db.Exec(`CREATE TABLE forge_x (id TEXT)`); err != nil {
		t.Fatal(err)
	}
	for _, pairs := range [][][2]string{
		{{"forge_x", `smeldr_x"; DROP TABLE forge_x; --`}},
		{{"Forge_X", "smeldr_x"}},
		{{"forge_x", ""}},
	} {
		if err := RenameLegacyTables(context.Background(), db, pairs); !isRenameValidation(err) {
			t.Errorf("%v: err = %v; want a ValidationError", pairs, err)
		}
	}
	if ok, _ := tableExists(context.Background(), db, "forge_x"); !ok {
		t.Error("forge_x was touched")
	}
}

func isRenameValidation(err error) bool {
	var ve *ValidationError
	return errors.As(err, &ve)
}
