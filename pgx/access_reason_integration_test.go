//go:build integration

package pgx

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	smeldr "smeldr.dev/core"
)

// Reasons on minting, revoking and granting, stored and read back on Postgres.
func TestPG_AccessReasons(t *testing.T) {
	db, _ := isolatedDB(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `CREATE TABLE smeldr_tokens (
		id TEXT PRIMARY KEY, name TEXT NOT NULL, role TEXT NOT NULL,
		expires_at TEXT NOT NULL, revoked_at TEXT, created_at TEXT NOT NULL,
		user_id TEXT, actor_class TEXT)`); err != nil {
		t.Fatalf("create tokens table: %v", err)
	}
	if err := smeldr.CreateProvenanceTable(db); err != nil {
		t.Fatalf("CreateProvenanceTable: %v", err)
	}
	ts := smeldr.NewTokenStore(db, pgTestSecret)
	rs := smeldr.NewRoleStore(db)
	app := smeldr.New(smeldr.Config{BaseURL: "http://localhost", Secret: []byte(pgTestSecret), DB: db, TokenStore: ts})
	if err := app.Governance(rs); err != nil {
		t.Fatalf("Governance: %v", err)
	}
	app.Provenance(smeldr.NewProvenanceStore(db))
	app.Handler() // wires provenance into both stores, mints the bootstrap token

	admin := smeldr.NewContextWithUser(smeldr.User{ID: "admin-1", Roles: []smeldr.Role{smeldr.Admin}})
	raw, userID, err := ts.CreateClassifiedWithReason(admin, "importer", "editor", smeldr.Job, time.Hour, "nightly import")
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	h := sha256.Sum256([]byte(raw))
	fp := hex.EncodeToString(h[:])
	if err := ts.RevokeWithReason(admin, fp, "import moved"); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	recs, err := ts.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range recs {
		if r.ID == fp {
			found = true
			if r.Reason != "nightly import" || r.RevokeReason != "import moved" {
				t.Errorf("token reasons = %q, %q", r.Reason, r.RevokeReason)
			}
		}
	}
	if !found {
		t.Fatal("minted token not listed")
	}

	if err := rs.DefineRole(ctx, smeldr.RoleDefinition{Name: "importer-role", Operations: []string{"read"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := rs.Grant(admin, smeldr.RoleGrant{TokenID: userID, RoleName: "importer-role", Reason: "reads the archive"}); err != nil {
		t.Fatalf("grant: %v", err)
	}
	gs, err := rs.ListGrants(ctx, userID)
	if err != nil || len(gs) != 1 || gs[0].Reason != "reads the archive" {
		t.Errorf("ListGrants = %+v, %v; want the grant reason", gs, err)
	}
}
