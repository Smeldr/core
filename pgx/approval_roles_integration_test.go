//go:build integration

package pgx

import (
	"context"
	"errors"
	"testing"
	"time"

	smeldr "smeldr.dev/core"
)

// T884 on Postgres: the approval-role migration of an instance that still has
// the old roles, a grant to it that survives, and the agent rule, with the
// class read from the tokens table and a legacy table without the columns.
func TestPG_ApprovalRoles(t *testing.T) {
	db, _ := isolatedDB(t)
	ctx := context.Background()
	// An instance from before T884: decision-steward with a grant, and an open
	// ratification condition addressed to "approve".
	_, store := newPGApp(t, db)
	if err := store.DefineRole(ctx, smeldr.RoleDefinition{Name: "decision-steward", Operations: []string{"review", "approve"}, ScopeMode: smeldr.ScopeGlobal}); err != nil {
		t.Fatalf("define old role: %v", err)
	}
	if _, err := store.Grant(ctx, smeldr.RoleGrant{TokenID: "peter", RoleName: "decision-steward"}); err != nil {
		t.Fatalf("old grant: %v", err)
	}
	if err := smeldr.CreateGovernanceTables(db); err != nil {
		t.Fatalf("CreateGovernanceTables: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO smeldr_signals (id, slug, status, created_at, updated_at, receiver, required_role)
		VALUES ('s1', 's1', 'open', now(), now(), 'approve', 'approve')`); err != nil {
		t.Fatalf("seed signal: %v", err)
	}

	// The next boot migrates; a second one changes nothing.
	for i := 0; i < 2; i++ {
		_, store = newPGApp(t, db)
	}
	if err := smeldr.RegisterApproverRole(ctx, store); err != nil {
		t.Fatal(err)
	}
	if ok, err := store.Authorized(ctx, "peter", smeldr.OperationRatify, smeldr.AuthTarget{TypeName: "Decision", ID: "d1"}); err != nil || !ok {
		t.Errorf("peter ratify after the migration = %v (%v), want true", ok, err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM smeldr_roles WHERE name = 'decision-steward'`).Scan(&n); err != nil || n != 0 {
		t.Errorf("old role rows = %d (%v), want 0", n, err)
	}
	var receiver string
	if err := db.QueryRowContext(ctx, `SELECT receiver FROM smeldr_signals WHERE id = 's1'`).Scan(&receiver); err != nil || receiver != smeldr.OperationRatify {
		t.Errorf("signal receiver = %q (%v), want ratify", receiver, err)
	}

	// No tokens table: every grantee is unclassified.
	if _, err := store.Grant(ctx, smeldr.RoleGrant{TokenID: "nobody", RoleName: smeldr.RoleApprover}); err != nil {
		t.Errorf("grant without a tokens table: %v", err)
	}
	// A legacy tokens table without the class columns: unclassified too.
	if _, err := db.ExecContext(ctx, `CREATE TABLE smeldr_tokens (id TEXT PRIMARY KEY, name TEXT NOT NULL, role TEXT NOT NULL,
		expires_at TEXT NOT NULL, revoked_at TEXT, created_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Grant(ctx, smeldr.RoleGrant{TokenID: "legacy", RoleName: smeldr.RoleApprover}); err != nil {
		t.Errorf("grant with a legacy tokens table: %v", err)
	}
	// With the columns, an agent token is refused ratify.
	if err := smeldr.EnsureTokenUserIDColumn(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := smeldr.EnsureTokenActorClassColumn(ctx, db); err != nil {
		t.Fatal(err)
	}
	ts := smeldr.NewTokenStore(db, pgTestSecret)
	raw, _, err := ts.CreateClassified(ctx, "bot", "editor", smeldr.Agent, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	bot, ok := smeldr.VerifyTokenString(raw, []byte(pgTestSecret), ts)
	if !ok {
		t.Fatal("bot token does not verify")
	}
	if _, err := store.Grant(ctx, smeldr.RoleGrant{TokenID: bot.ID, RoleName: smeldr.RoleApprover}); !errors.Is(err, smeldr.ErrForbidden) {
		t.Errorf("approver to an agent token: %v, want ErrForbidden", err)
	}
}
