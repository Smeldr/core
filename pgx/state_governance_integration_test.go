//go:build integration

package forgepgx

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	smeldr "smeldr.dev/core"
)

// openSmeldrDB opens a pgxpool and wraps it as a smeldr.DB using [Wrap],
// or skips the calling test if DATABASE_URL is not set.
func openSmeldrDB(t *testing.T) (smeldr.DB, func()) {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping Postgres integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	return Wrap(pool), pool.Close
}

// TestIntegration_Postgres_StateFlows boots a smeldr.App backed by a real
// Postgres 16 pool (via forgepgx.Wrap), exercises migrateStateFlows, and
// registers a custom flow via App.RegisterFlow — verifying that all
// state-flow SQL uses $N placeholders and ON CONFLICT … DO NOTHING syntax
// accepted by Postgres.
func TestIntegration_Postgres_StateFlows(t *testing.T) {
	db, cleanup := openSmeldrDB(t)
	defer cleanup()
	ctx := context.Background()

	app := smeldr.New(smeldr.Config{
		BaseURL: "http://localhost",
		Secret:  []byte("integration-test-secret-32bytes!"),
		DB:      db,
	})

	// migrateStateFlows ran inside New; verify the default flow was seeded.
	var flowName string
	if err := db.QueryRowContext(ctx,
		`SELECT name FROM smeldr_state_flows WHERE name = $1`, "default",
	).Scan(&flowName); err != nil {
		t.Fatalf("default flow missing after New: %v", err)
	}
	if flowName != "default" {
		t.Errorf("default flow name: got %q, want %q", flowName, "default")
	}

	// RegisterFlow — exercises INSERT ON CONFLICT DO NOTHING and UPDATE.
	flow := smeldr.StateFlow{
		Name:     "pg-review",
		TypeName: "article",
		States: []smeldr.State{
			{Name: "draft", Initial: true},
			{Name: "review"},
			{Name: "approved", Terminal: true},
		},
		Transitions: []smeldr.Transition{
			{From: "draft", To: "review"},
			{From: "review", To: "approved"},
		},
	}
	if err := app.RegisterFlow(flow); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}

	// RegisterFlow is idempotent — second call must not error.
	if err := app.RegisterFlow(flow); err != nil {
		t.Fatalf("RegisterFlow (idempotent): %v", err)
	}

	// Verify the flow and its states exist in Postgres.
	var stateCount int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM smeldr_states s
		 JOIN smeldr_state_flows f ON f.id = s.flow_id
		 WHERE f.name = $1`, "pg-review",
	).Scan(&stateCount); err != nil {
		t.Fatalf("count states: %v", err)
	}
	if stateCount != 3 {
		t.Errorf("state count: got %d, want 3", stateCount)
	}
}

// TestIntegration_Postgres_Governance boots a smeldr.App backed by a real
// Postgres 16 pool (via forgepgx.Wrap), exercises migrateGovernance,
// DefineRole, Grant, Authorized, RoleGranted, and ToolPolicy — verifying
// that all governance SQL uses $N placeholders and that the UPSERT and
// IS NOT DISTINCT FROM forms are accepted by Postgres.
func TestIntegration_Postgres_Governance(t *testing.T) {
	db, cleanup := openSmeldrDB(t)
	defer cleanup()
	ctx := context.Background()

	app := smeldr.New(smeldr.Config{
		BaseURL: "http://localhost",
		Secret:  []byte("integration-test-secret-32bytes!"),
		DB:      db,
	})

	store := smeldr.NewRoleStore(db)
	if err := app.Governance(store); err != nil {
		t.Fatalf("Governance: %v", err)
	}

	// DefineRole — exercises the UPSERT (INSERT … ON CONFLICT DO UPDATE).
	if err := store.DefineRole(ctx, smeldr.RoleDefinition{
		Name:       "pg-editor",
		Operations: []string{"create", "read", "update"},
		ScopeMode:  smeldr.ScopeGlobal,
	}); err != nil {
		t.Fatalf("DefineRole: %v", err)
	}

	// DefineRole is idempotent — second call must update, not error.
	if err := store.DefineRole(ctx, smeldr.RoleDefinition{
		Name:       "pg-editor",
		Operations: []string{"create", "read", "update", "delete"},
		ScopeMode:  smeldr.ScopeGlobal,
	}); err != nil {
		t.Fatalf("DefineRole (update): %v", err)
	}

	// Grant — exercises IS NOT DISTINCT FROM and SELECT re-query.
	tokenID := "pg-integ-token-" + smeldr.NewID()
	grantID, err := store.Grant(ctx, smeldr.RoleGrant{
		TokenID:  tokenID,
		RoleName: "pg-editor",
	})
	if err != nil {
		t.Fatalf("Grant: %v", err)
	}
	if grantID == "" {
		t.Error("Grant returned empty ID")
	}

	// Grant is idempotent — second call returns the same ID.
	grantID2, err := store.Grant(ctx, smeldr.RoleGrant{
		TokenID:  tokenID,
		RoleName: "pg-editor",
	})
	if err != nil {
		t.Fatalf("Grant (idempotent): %v", err)
	}
	if grantID != grantID2 {
		t.Errorf("idempotent Grant: got ID %q, want %q", grantID2, grantID)
	}

	// Authorized — exercises the JOIN query with $1 tokenID.
	ok, err := store.Authorized(ctx, tokenID, "create", smeldr.AuthTarget{})
	if err != nil {
		t.Fatalf("Authorized: %v", err)
	}
	if !ok {
		t.Error("Authorized: expected true for granted operation, got false")
	}

	// Authorized — operation not in the role's list.
	ok, err = store.Authorized(ctx, tokenID, "administer", smeldr.AuthTarget{})
	if err != nil {
		t.Fatalf("Authorized (not granted): %v", err)
	}
	if ok {
		t.Error("Authorized: expected false for ungrant operation, got true")
	}

	// RoleGranted — exercises the name-based JOIN query.
	ok, err = store.RoleGranted(ctx, tokenID, "pg-editor", smeldr.AuthTarget{})
	if err != nil {
		t.Fatalf("RoleGranted: %v", err)
	}
	if !ok {
		t.Error("RoleGranted: expected true for granted role, got false")
	}

	// RoleGranted — role not held.
	ok, err = store.RoleGranted(ctx, tokenID, "pg-superuser", smeldr.AuthTarget{})
	if err != nil {
		t.Fatalf("RoleGranted (not granted): %v", err)
	}
	if ok {
		t.Error("RoleGranted: expected false for non-granted role, got true")
	}

	// ToolPolicy — exercises the $1 tool_name lookup against seeded policies.
	op, found, err := store.ToolPolicy(ctx, "create_post")
	if err != nil {
		t.Fatalf("ToolPolicy: %v", err)
	}
	if !found {
		t.Error("ToolPolicy: expected create_post to be seeded, got not-found")
	}
	if op == "" {
		t.Error("ToolPolicy: expected non-empty required_op for create_post")
	}
}
