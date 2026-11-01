//go:build integration

package pgx

import (
	"context"
	"errors"
	"testing"
	"time"

	smeldr "smeldr.dev/core"
)

// Identities on Postgres (D165, T1014): the migration of existing tokens with
// the same ids, a mint that creates an identity and one that joins it, the
// class and status read on verify in one query, the grantee check, and the
// token as the means of a provenance record.
func TestPG_Identities(t *testing.T) {
	db, _ := isolatedDB(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `CREATE TABLE smeldr_tokens (
		id TEXT PRIMARY KEY, name TEXT NOT NULL, role TEXT NOT NULL,
		expires_at TEXT NOT NULL, revoked_at TEXT, created_at TEXT NOT NULL, user_id TEXT, actor_class TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO smeldr_tokens (id, name, role, expires_at, created_at, user_id, actor_class)
		VALUES ('fp-old', 'core-old', 'editor', '2099-01-01T00:00:00Z', '2026-01-01T00:00:00Z', 'u-core', 'agent')`); err != nil {
		t.Fatal(err)
	}
	ts := smeldr.NewTokenStore(db, pgTestSecret)
	// Before EnsureIdentities a classified mint and verify work as before.
	before, _, err := ts.CreateClassified(ctx, "before", "editor", smeldr.Agent, time.Hour)
	if err != nil {
		t.Fatalf("mint without identities: %v", err)
	}
	if _, ok := smeldr.VerifyTokenString(before, []byte(pgTestSecret), ts); !ok {
		t.Fatal("verify without identities")
	}
	for i := 0; i < 2; i++ {
		if err := smeldr.EnsureIdentities(ctx, db); err != nil {
			t.Fatalf("EnsureIdentities call %d: %v", i+1, err)
		}
	}
	if err := smeldr.CreateProvenanceTable(db); err != nil {
		t.Fatal(err)
	}
	ts = smeldr.NewTokenStore(db, pgTestSecret) // a fresh store: the old one remembers no identities
	app := smeldr.New(smeldr.Config{BaseURL: "http://localhost", Secret: []byte(pgTestSecret), DB: db, TokenStore: ts})

	rep, err := app.MigrateIdentities(ctx, smeldr.IdentityMigrationOptions{})
	if err != nil {
		t.Fatalf("MigrateIdentities: %v", err)
	}
	if len(rep.Created) != 2 || rep.Created[0].ID != "u-core" || rep.Created[0].Address != "IDN1" {
		t.Fatalf("migration = %+v", rep)
	}

	human := smeldr.NewTestContext(smeldr.User{ID: "h-1", Roles: []smeldr.Role{smeldr.Admin, smeldr.Human}})
	_, botID, err := ts.CreateClassified(human, "bot", "editor", smeldr.Agent, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	bot, err := app.Identity(ctx, botID)
	if err != nil || bot.Address != "IDN3" || bot.OperatorID != "h-1" {
		t.Fatalf("minted identity = %+v, %v", bot, err)
	}
	joined, err := ts.CreateForIdentity(human, "IDN1", "core-2026-11", "editor", time.Hour, "rotation")
	if err != nil {
		t.Fatalf("CreateForIdentity: %v", err)
	}
	u, ok := smeldr.VerifyTokenString(joined, []byte(pgTestSecret), ts)
	if !ok || u.ID != "u-core" || !smeldr.IsRole(u.Roles, smeldr.Agent) || u.TokenID == "" {
		t.Fatalf("joined token verified as %+v, %v", u, ok)
	}
	names, err := ts.NamesForUserIDs(ctx, []string{"u-core"})
	if err != nil || names["u-core"] != "core-old" {
		t.Errorf("names = %v, %v; want the identity's", names, err)
	}

	if _, err := db.ExecContext(ctx, `UPDATE smeldr_identities SET status = 'suspended' WHERE id = 'u-core'`); err != nil {
		t.Fatal(err)
	}
	if _, ok := smeldr.VerifyTokenString(joined, []byte(pgTestSecret), ts); ok {
		t.Error("a suspended identity's token must not verify")
	}
	var refusal *smeldr.RefusalError
	if _, err := ts.CreateForIdentity(human, "IDN1", "x", "editor", time.Hour, ""); !errors.As(err, &refusal) {
		t.Errorf("join a suspended identity: %v; want a refusal", err)
	}

	rs := smeldr.NewRoleStore(db)
	if err := app.Governance(rs); err != nil {
		t.Fatal(err)
	}
	if _, err := rs.Grant(ctx, smeldr.RoleGrant{TokenID: botID, RoleName: "editor"}); err != nil {
		t.Errorf("grant to an identity: %v", err)
	}
	var ve *smeldr.ValidationError
	if _, err := rs.Grant(ctx, smeldr.RoleGrant{TokenID: "nobody", RoleName: "editor"}); !errors.As(err, &ve) {
		t.Errorf("grant to no identity: %v; want a ValidationError", err)
	}

	ps := smeldr.NewProvenanceStore(db)
	actor := smeldr.NewTestContext(smeldr.User{ID: botID, TokenID: "fp-bot"})
	if err := ps.Append(actor, smeldr.ProvenanceRecord{ID: smeldr.NewID(), Timestamp: time.Now(), SubjectType: "Task", SubjectID: "t1",
		Verb: "create", ActorKind: "agent", ActorID: botID}); err != nil {
		t.Fatal(err)
	}
	recs, err := ps.List(ctx, smeldr.ProvenanceFilter{SubjectID: "t1"})
	if err != nil || len(recs) != 1 || recs[0].Means != "fp-bot" {
		t.Errorf("provenance = %+v, %v; want the token as the means", recs, err)
	}
}
