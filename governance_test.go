package smeldr

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
)

// execFailDB wraps a real DB and fails ExecContext when the query contains a
// target string. Used to exercise error paths in governance seed functions.
type execFailDB struct {
	DB
	failOn string
}

func (d *execFailDB) ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error) {
	if strings.Contains(q, d.failOn) {
		return nil, errors.New("simulated exec fail")
	}
	return d.DB.ExecContext(ctx, q, args...)
}

// govQueryFailDB wraps a real DB and makes QueryContext fail when the query
// contains a target string.
type govQueryFailDB struct {
	DB
	failOn string
}

func (d *govQueryFailDB) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	if strings.Contains(q, d.failOn) {
		return nil, errors.New("simulated query fail")
	}
	return d.DB.QueryContext(ctx, q, args...)
}

// govQueryRowFailDB wraps a real DB and makes QueryRowContext return an error
// row when the query contains a target string.
type govQueryRowFailDB struct {
	DB
	failOn string
}

func (d *govQueryRowFailDB) QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row {
	if strings.Contains(q, d.failOn) {
		sdb, _ := sql.Open("sqlite", ":memory:")
		return sdb.QueryRowContext(ctx, "SELECT 1 FROM no_table_xyz")
	}
	return d.DB.QueryRowContext(ctx, q, args...)
}

// setupTokensTable creates smeldr_tokens with the minimal columns needed for
// governance migration tests.
func setupTokensTable(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(),
		`CREATE TABLE IF NOT EXISTS smeldr_tokens (
			id   TEXT NOT NULL PRIMARY KEY,
			role TEXT NOT NULL DEFAULT ''
		)`,
	); err != nil {
		t.Fatalf("setup smeldr_tokens: %v", err)
	}
}

// TestMigrateGovernance_TablesCreated verifies all three governance tables and
// their indexes are created after a single call to migrateGovernance.
func TestMigrateGovernance_TablesCreated(t *testing.T) {
	db := newSQLiteDB(t)
	setupTokensTable(t, db)
	ctx := context.Background()

	if err := migrateGovernance(ctx, db); err != nil {
		t.Fatalf("migrateGovernance: %v", err)
	}

	tables := []string{"smeldr_roles", "smeldr_role_grants", "smeldr_tool_policies"}
	for _, tbl := range tables {
		var n int
		if err := db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, tbl,
		).Scan(&n); err != nil || n == 0 {
			t.Errorf("table %q not found after migrateGovernance", tbl)
		}
	}

	indexes := []string{"idx_role_grants_token", "idx_role_grants_role_anchor"}
	for _, idx := range indexes {
		var n int
		if err := db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?`, idx,
		).Scan(&n); err != nil || n == 0 {
			t.Errorf("index %q not found after migrateGovernance", idx)
		}
	}
}

// TestMigrateGovernance_Idempotent verifies that calling migrateGovernance twice
// returns no error and produces no duplicate rows.
func TestMigrateGovernance_Idempotent(t *testing.T) {
	db := newSQLiteDB(t)
	setupTokensTable(t, db)
	ctx := context.Background()

	for i := range 2 {
		if err := migrateGovernance(ctx, db); err != nil {
			t.Fatalf("run %d: migrateGovernance: %v", i+1, err)
		}
	}

	var roleCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM smeldr_roles`).Scan(&roleCount); err != nil {
		t.Fatalf("count roles: %v", err)
	}
	if roleCount != 3 {
		t.Errorf("role count after two runs: want 3, got %d", roleCount)
	}
}

// TestMigrateGovernance_DefaultRolesSeed verifies author, editor, and admin roles
// are seeded with the correct operations arrays.
func TestMigrateGovernance_DefaultRolesSeed(t *testing.T) {
	db := newSQLiteDB(t)
	setupTokensTable(t, db)
	ctx := context.Background()

	if err := migrateGovernance(ctx, db); err != nil {
		t.Fatalf("migrateGovernance: %v", err)
	}

	cases := []struct {
		name    string
		wantOps string
	}{
		{"author", `["create","read","update","publish","archive"]`},
		{"editor", `["create","read","update","publish","archive","delete","manage"]`},
		{"admin", `["create","read","update","publish","archive","delete","manage","administer","review","approve","define-type","define-flow","define-relation-kind"]`},
	}
	for _, c := range cases {
		var ops string
		if err := db.QueryRowContext(ctx,
			`SELECT operations FROM smeldr_roles WHERE name = ?`, c.name,
		).Scan(&ops); err != nil {
			t.Errorf("role %q not found: %v", c.name, err)
			continue
		}
		if ops != c.wantOps {
			t.Errorf("role %q operations: want %q, got %q", c.name, c.wantOps, ops)
		}
	}
}

// TestMigrateGovernance_DefaultRolesScopeMode verifies all default roles use
// global scope and trust_level 0.
func TestMigrateGovernance_DefaultRolesScopeMode(t *testing.T) {
	db := newSQLiteDB(t)
	setupTokensTable(t, db)
	ctx := context.Background()

	if err := migrateGovernance(ctx, db); err != nil {
		t.Fatalf("migrateGovernance: %v", err)
	}

	for _, name := range []string{"author", "editor", "admin"} {
		var scopeMode string
		var trustLevel int
		if err := db.QueryRowContext(ctx,
			`SELECT scope_mode, trust_level FROM smeldr_roles WHERE name = ?`, name,
		).Scan(&scopeMode, &trustLevel); err != nil {
			t.Errorf("role %q: %v", name, err)
			continue
		}
		if scopeMode != "global" {
			t.Errorf("role %q scope_mode: want global, got %q", name, scopeMode)
		}
		if trustLevel != 0 {
			t.Errorf("role %q trust_level: want 0, got %d", name, trustLevel)
		}
	}
}

// TestMigrateGovernance_ToolPoliciesSeed verifies key tool policies are seeded
// with the correct required_op values.
func TestMigrateGovernance_ToolPoliciesSeed(t *testing.T) {
	db := newSQLiteDB(t)
	setupTokensTable(t, db)
	ctx := context.Background()

	if err := migrateGovernance(ctx, db); err != nil {
		t.Fatalf("migrateGovernance: %v", err)
	}

	cases := []struct {
		tool   string
		wantOp string
	}{
		{"create_preview_url", "publish"},
		{"define_content_type", "define-type"},
		{"define_state_flow", "define-flow"},
		{"upsert_relation_kind", "define-relation-kind"},
		{"create_token", "administer"},
		{"list_tokens", "administer"},
		{"create_webhook", "administer"},
		{"set_page_meta", "administer"},
		{"add_section", "manage"},
		{"transition_item", "manage"},
		{"create_nav_item", "manage"},
		{"create_redirect", "manage"},
		{"list_posts", "read"},
		{"delete_post", "delete"},
	}
	for _, c := range cases {
		var op string
		if err := db.QueryRowContext(ctx,
			`SELECT required_op FROM smeldr_tool_policies WHERE tool_name = ?`, c.tool,
		).Scan(&op); err != nil {
			t.Errorf("policy for %q not found: %v", c.tool, err)
			continue
		}
		if op != c.wantOp {
			t.Errorf("policy for %q: want %q, got %q", c.tool, c.wantOp, op)
		}
	}
}

// TestMigrateGovernance_ToolPoliciesIdempotent verifies repeated calls produce
// no duplicate tool_policies rows.
func TestMigrateGovernance_ToolPoliciesIdempotent(t *testing.T) {
	db := newSQLiteDB(t)
	setupTokensTable(t, db)
	ctx := context.Background()

	var firstCount int
	if err := migrateGovernance(ctx, db); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM smeldr_tool_policies`).Scan(&firstCount); err != nil {
		t.Fatalf("count: %v", err)
	}

	if err := migrateGovernance(ctx, db); err != nil {
		t.Fatalf("second run: %v", err)
	}
	var secondCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM smeldr_tool_policies`).Scan(&secondCount); err != nil {
		t.Fatalf("count: %v", err)
	}
	if firstCount != secondCount {
		t.Errorf("tool policy count changed on second run: %d → %d", firstCount, secondCount)
	}
}

// TestMigrateGovernance_NilDB verifies that migrateGovernance returns an error
// immediately when db is nil.
func TestMigrateGovernance_NilDB(t *testing.T) {
	ctx := context.Background()
	if err := migrateGovernance(ctx, nil); err == nil {
		t.Fatal("expected error for nil DB, got nil")
	}
}

// TestMigrateTokenGrants_GlobalScopeGrant verifies that a token with a known
// role gets a global-scope grant row after migration.
func TestMigrateTokenGrants_GlobalScopeGrant(t *testing.T) {
	db := newSQLiteDB(t)
	setupTokensTable(t, db)
	ctx := context.Background()

	if err := migrateGovernance(ctx, db); err != nil {
		t.Fatalf("migrateGovernance: %v", err)
	}

	// Insert a token with role="editor".
	tokenID := NewID()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO smeldr_tokens (id, role) VALUES (?, 'editor')`, tokenID,
	); err != nil {
		t.Fatalf("insert token: %v", err)
	}

	if err := migrateTokenGrants(ctx, db); err != nil {
		t.Fatalf("migrateTokenGrants: %v", err)
	}

	// A grant row must exist for this token with the editor role.
	var grantCount int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM smeldr_role_grants g
			JOIN smeldr_roles r ON r.id = g.role_id
			WHERE g.token_id = ? AND r.name = 'editor' AND g.scope_anchor_id IS NULL`,
		tokenID,
	).Scan(&grantCount); err != nil {
		t.Fatalf("query grant: %v", err)
	}
	if grantCount != 1 {
		t.Errorf("grant count for editor token: want 1, got %d", grantCount)
	}
}

// TestMigrateTokenGrants_UnknownRoleSkipped verifies that a token with an
// unrecognised role produces no grant row and no error.
func TestMigrateTokenGrants_UnknownRoleSkipped(t *testing.T) {
	db := newSQLiteDB(t)
	setupTokensTable(t, db)
	ctx := context.Background()

	if err := migrateGovernance(ctx, db); err != nil {
		t.Fatalf("migrateGovernance: %v", err)
	}

	tokenID := NewID()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO smeldr_tokens (id, role) VALUES (?, 'superuser')`, tokenID,
	); err != nil {
		t.Fatalf("insert token: %v", err)
	}

	if err := migrateTokenGrants(ctx, db); err != nil {
		t.Fatalf("migrateTokenGrants returned error for unknown role: %v", err)
	}

	var grantCount int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM smeldr_role_grants WHERE token_id = ?`, tokenID,
	).Scan(&grantCount); err != nil {
		t.Fatalf("count grants: %v", err)
	}
	if grantCount != 0 {
		t.Errorf("expected no grant for unknown role, got %d", grantCount)
	}
}

// TestMigrateTokenGrants_Idempotent verifies that calling migrateTokenGrants
// twice produces exactly one grant per token.
func TestMigrateTokenGrants_Idempotent(t *testing.T) {
	db := newSQLiteDB(t)
	setupTokensTable(t, db)
	ctx := context.Background()

	if err := migrateGovernance(ctx, db); err != nil {
		t.Fatalf("migrateGovernance: %v", err)
	}

	tokenID := NewID()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO smeldr_tokens (id, role) VALUES (?, 'admin')`, tokenID,
	); err != nil {
		t.Fatalf("insert token: %v", err)
	}

	for i := range 2 {
		if err := migrateTokenGrants(ctx, db); err != nil {
			t.Fatalf("run %d: migrateTokenGrants: %v", i+1, err)
		}
	}

	var grantCount int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM smeldr_role_grants WHERE token_id = ?`, tokenID,
	).Scan(&grantCount); err != nil {
		t.Fatalf("count grants: %v", err)
	}
	if grantCount != 1 {
		t.Errorf("grant count after two runs: want 1, got %d", grantCount)
	}
}

// TestMigrateTokenGrants_NoTokensTable verifies that migrateTokenGrants returns
// an error when smeldr_tokens does not exist. This error is caught by
// migrateGovernance and handled as a fail-open warning.
func TestMigrateTokenGrants_NoTokensTable(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()

	// Create governance tables but NOT smeldr_tokens.
	stmts := []string{
		`CREATE TABLE smeldr_roles (id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE, operations TEXT NOT NULL, scope_mode TEXT NOT NULL DEFAULT 'global', scope_relation_kind TEXT, scope_direction TEXT, trust_level INTEGER NOT NULL DEFAULT 0, allow_self_approval INTEGER NOT NULL DEFAULT 0, created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL)`,
		`CREATE TABLE smeldr_role_grants (id TEXT PRIMARY KEY, token_id TEXT NOT NULL, role_id TEXT NOT NULL, scope_static TEXT NOT NULL DEFAULT '[]', scope_anchor_id TEXT, created_at DATETIME NOT NULL)`,
	}
	for _, s := range stmts {
		if _, err := db.ExecContext(ctx, s); err != nil {
			t.Fatalf("setup: %v", err)
		}
	}

	if err := migrateTokenGrants(ctx, db); err == nil {
		t.Fatal("expected error when smeldr_tokens is absent, got nil")
	}
}

// TestMigrateGovernance_FailOpenOnMissingTokensTable verifies that
// migrateGovernance succeeds (fail-open) when smeldr_tokens does not exist,
// logging a warning instead of returning an error.
func TestMigrateGovernance_FailOpenOnMissingTokensTable(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()
	// Do NOT call setupTokensTable — simulate a deployment without token management.

	if err := migrateGovernance(ctx, db); err != nil {
		t.Fatalf("migrateGovernance should succeed even without smeldr_tokens: %v", err)
	}

	// Tables and seed data must still be present.
	var roleCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM smeldr_roles`).Scan(&roleCount); err != nil {
		t.Fatalf("smeldr_roles missing: %v", err)
	}
	if roleCount != 3 {
		t.Errorf("role count: want 3, got %d", roleCount)
	}
}

// TestMigrateGovernance_DDLError verifies that migrateGovernance returns an
// error when the CREATE TABLE statement fails.
func TestMigrateGovernance_DDLError(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()

	wrapped := &execFailDB{DB: db, failOn: "smeldr_roles"}
	if err := migrateGovernance(ctx, wrapped); err == nil {
		t.Fatal("expected error when DDL fails, got nil")
	}
}

// TestMigrateGovernance_SeedRolesError verifies that migrateGovernance
// propagates an error from seedDefaultRoles (tables exist but INSERT fails).
func TestMigrateGovernance_SeedRolesError(t *testing.T) {
	db := newSQLiteDB(t)
	setupTokensTable(t, db)
	ctx := context.Background()

	// First call succeeds — tables are created and seed data is inserted.
	if err := migrateGovernance(ctx, db); err != nil {
		t.Fatalf("first migrateGovernance: %v", err)
	}

	// Drop roles so INSERT is not an ignore but wrapping makes ExecContext fail.
	// Use a wrapper that fails only INSERT (not CREATE TABLE IF NOT EXISTS which is a no-op now).
	wrapped := &execFailDB{DB: db, failOn: "INSERT OR IGNORE INTO smeldr_roles"}
	if err := migrateGovernance(ctx, wrapped); err == nil {
		t.Fatal("expected error when seedDefaultRoles INSERT fails, got nil")
	}
}

// TestMigrateGovernance_SeedToolPoliciesError verifies that migrateGovernance
// propagates an error from seedToolPolicies.
func TestMigrateGovernance_SeedToolPoliciesError(t *testing.T) {
	db := newSQLiteDB(t)
	setupTokensTable(t, db)
	ctx := context.Background()

	// Tables exist from first run.
	if err := migrateGovernance(ctx, db); err != nil {
		t.Fatalf("first migrateGovernance: %v", err)
	}

	wrapped := &execFailDB{DB: db, failOn: "INSERT OR IGNORE INTO smeldr_tool_policies"}
	if err := migrateGovernance(ctx, wrapped); err == nil {
		t.Fatal("expected error when seedToolPolicies INSERT fails, got nil")
	}
}

// TestSeedDefaultRoles_ExecError verifies that seedDefaultRoles propagates an
// error from ExecContext.
func TestSeedDefaultRoles_ExecError(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()

	// Create tables so the INSERT is the failing call.
	if _, err := db.ExecContext(ctx,
		`CREATE TABLE smeldr_roles (id TEXT NOT NULL PRIMARY KEY, name TEXT NOT NULL UNIQUE, operations TEXT NOT NULL, scope_mode TEXT NOT NULL DEFAULT 'global', scope_relation_kind TEXT, scope_direction TEXT, trust_level INTEGER NOT NULL DEFAULT 0, allow_self_approval INTEGER NOT NULL DEFAULT 0, created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL)`,
	); err != nil {
		t.Fatalf("setup: %v", err)
	}

	wrapped := &execFailDB{DB: db, failOn: "INSERT OR IGNORE INTO smeldr_roles"}
	if err := seedDefaultRoles(ctx, wrapped); err == nil {
		t.Fatal("expected error from seedDefaultRoles when ExecContext fails, got nil")
	}
}

// TestSeedToolPolicies_ExecError verifies that seedToolPolicies propagates an
// error from ExecContext.
func TestSeedToolPolicies_ExecError(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()

	if _, err := db.ExecContext(ctx,
		`CREATE TABLE smeldr_tool_policies (id TEXT NOT NULL PRIMARY KEY, tool_name TEXT NOT NULL UNIQUE, required_op TEXT NOT NULL, created_at DATETIME NOT NULL)`,
	); err != nil {
		t.Fatalf("setup: %v", err)
	}

	wrapped := &execFailDB{DB: db, failOn: "smeldr_tool_policies"}
	if err := seedToolPolicies(ctx, wrapped); err == nil {
		t.Fatal("expected error from seedToolPolicies when ExecContext fails, got nil")
	}
}

// TestMigrateTokenGrants_QueryError verifies that migrateTokenGrants returns an
// error when the initial SELECT on smeldr_tokens fails.
func TestMigrateTokenGrants_QueryError(t *testing.T) {
	db := newSQLiteDB(t)
	setupTokensTable(t, db)
	ctx := context.Background()

	if err := migrateGovernance(ctx, db); err != nil {
		t.Fatalf("migrateGovernance: %v", err)
	}

	wrapped := &govQueryFailDB{DB: db, failOn: "FROM smeldr_tokens"}
	if err := migrateTokenGrants(ctx, wrapped); err == nil {
		t.Fatal("expected error when token query fails, got nil")
	}
}

// TestMigrateTokenGrants_RoleLookupError verifies that migrateTokenGrants
// returns an error when the role lookup fails (not a no-rows miss).
func TestMigrateTokenGrants_RoleLookupError(t *testing.T) {
	db := newSQLiteDB(t)
	setupTokensTable(t, db)
	ctx := context.Background()

	if err := migrateGovernance(ctx, db); err != nil {
		t.Fatalf("migrateGovernance: %v", err)
	}

	tokenID := NewID()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO smeldr_tokens (id, role) VALUES (?, 'admin')`, tokenID,
	); err != nil {
		t.Fatalf("insert token: %v", err)
	}

	wrapped := &govQueryRowFailDB{DB: db, failOn: "FROM smeldr_roles"}
	if err := migrateTokenGrants(ctx, wrapped); err == nil {
		t.Fatal("expected error when role lookup fails, got nil")
	}
}

// TestMigrateTokenGrants_InsertError verifies that migrateTokenGrants returns
// an error when the grant INSERT fails.
func TestMigrateTokenGrants_InsertError(t *testing.T) {
	db := newSQLiteDB(t)
	setupTokensTable(t, db)
	ctx := context.Background()

	if err := migrateGovernance(ctx, db); err != nil {
		t.Fatalf("migrateGovernance: %v", err)
	}

	tokenID := NewID()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO smeldr_tokens (id, role) VALUES (?, 'editor')`, tokenID,
	); err != nil {
		t.Fatalf("insert token: %v", err)
	}

	wrapped := &execFailDB{DB: db, failOn: "smeldr_role_grants"}
	if err := migrateTokenGrants(ctx, wrapped); err == nil {
		t.Fatal("expected error when grant INSERT fails, got nil")
	}
}
