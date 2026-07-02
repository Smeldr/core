package smeldr

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"
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

// govQueryNullRowsDB wraps a real DB and replaces a matching QueryContext with
// a two-column NULL row. This triggers scan failures: scanning NULL into a
// non-nullable string or scanning 6 destinations from a 2-column result.
type govQueryNullRowsDB struct {
	DB
	nullOn string
}

func (d *govQueryNullRowsDB) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	if strings.Contains(q, d.nullOn) {
		return d.DB.QueryContext(ctx, "SELECT NULL, NULL")
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

// TestMigrateTokenGrants_ScanError verifies that migrateTokenGrants returns an
// error when scanning the token row fails (NULL into non-nullable string).
func TestMigrateTokenGrants_ScanError(t *testing.T) {
	db := newSQLiteDB(t)
	setupTokensTable(t, db)
	ctx := context.Background()

	if err := migrateGovernance(ctx, db); err != nil {
		t.Fatalf("migrateGovernance: %v", err)
	}

	// govQueryNullRowsDB replaces the SELECT on smeldr_tokens with a 2-column
	// NULL row; scanning NULL into id string triggers a conversion error.
	wrapped := &govQueryNullRowsDB{DB: db, nullOn: "SELECT id, role FROM smeldr_tokens"}
	if err := migrateTokenGrants(ctx, wrapped); err == nil {
		t.Fatal("expected scan error, got nil")
	}
}

// setupGovernanceDB creates an in-memory SQLite DB with all governance tables
// and seeded default roles. Helper for RoleStore tests.
func setupGovernanceDB(t *testing.T) *sql.DB {
	t.Helper()
	db := newSQLiteDB(t)
	setupTokensTable(t, db)
	if err := migrateGovernance(context.Background(), db); err != nil {
		t.Fatalf("setup migrateGovernance: %v", err)
	}
	return db
}

// setupRelationsTable creates the smeldr_relations table for dynamic-scope tests.
func setupRelationsTable(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(),
		`CREATE TABLE IF NOT EXISTS smeldr_relations (
			id            TEXT NOT NULL PRIMARY KEY,
			source_type   TEXT NOT NULL DEFAULT '',
			source_id     TEXT NOT NULL,
			target_type   TEXT NOT NULL DEFAULT '',
			target_id     TEXT NOT NULL,
			relation_kind TEXT NOT NULL,
			edge_class    TEXT NOT NULL DEFAULT '',
			confidence    REAL,
			valid_at      DATETIME,
			invalid_at    DATETIME,
			created_by_job TEXT,
			attributes    TEXT NOT NULL DEFAULT '{}',
			created_at    DATETIME NOT NULL,
			updated_at    DATETIME NOT NULL
		)`,
	); err != nil {
		t.Fatalf("setup smeldr_relations: %v", err)
	}
}

// --- DefineRole tests ---

func TestDefineRole_New(t *testing.T) {
	db := setupGovernanceDB(t)
	store := NewRoleStore(db)
	ctx := context.Background()

	if err := store.DefineRole(ctx, RoleDefinition{
		Name:       "reviewer",
		Operations: []string{"review"},
		ScopeMode:  ScopeGlobal,
	}); err != nil {
		t.Fatalf("DefineRole: %v", err)
	}

	var ops string
	if err := db.QueryRowContext(ctx,
		`SELECT operations FROM smeldr_roles WHERE name='reviewer'`,
	).Scan(&ops); err != nil {
		t.Fatalf("query: %v", err)
	}
	if ops != `["review"]` {
		t.Errorf("operations: got %q, want %q", ops, `["review"]`)
	}
}

func TestDefineRole_UpdateExisting(t *testing.T) {
	db := setupGovernanceDB(t)
	store := NewRoleStore(db)
	ctx := context.Background()

	if err := store.DefineRole(ctx, RoleDefinition{Name: "reviewer", Operations: []string{"review"}}); err != nil {
		t.Fatalf("first DefineRole: %v", err)
	}
	if err := store.DefineRole(ctx, RoleDefinition{Name: "reviewer", Operations: []string{"review", "approve"}, TrustLevel: 2}); err != nil {
		t.Fatalf("second DefineRole: %v", err)
	}

	var ops string
	var trust int
	if err := db.QueryRowContext(ctx,
		`SELECT operations, trust_level FROM smeldr_roles WHERE name='reviewer'`,
	).Scan(&ops, &trust); err != nil {
		t.Fatalf("query: %v", err)
	}
	if ops != `["review","approve"]` {
		t.Errorf("operations: got %q", ops)
	}
	if trust != 2 {
		t.Errorf("trust_level: got %d, want 2", trust)
	}
}

func TestDefineRole_EmptyName(t *testing.T) {
	store := NewRoleStore(newSQLiteDB(t))
	if err := store.DefineRole(context.Background(), RoleDefinition{}); err == nil {
		t.Fatal("expected error for empty name, got nil")
	}
}

func TestDefineRole_InsertError(t *testing.T) {
	db := setupGovernanceDB(t)
	ctx := context.Background()
	wrapped := &execFailDB{DB: db, failOn: "INSERT OR IGNORE INTO smeldr_roles"}
	store := NewRoleStore(wrapped)
	if err := store.DefineRole(ctx, RoleDefinition{Name: "x", Operations: []string{"read"}}); err == nil {
		t.Fatal("expected error from failing INSERT, got nil")
	}
}

func TestDefineRole_UpdateError(t *testing.T) {
	db := setupGovernanceDB(t)
	ctx := context.Background()
	wrapped := &execFailDB{DB: db, failOn: "UPDATE smeldr_roles"}
	store := NewRoleStore(wrapped)
	if err := store.DefineRole(ctx, RoleDefinition{Name: "x", Operations: []string{"read"}}); err == nil {
		t.Fatal("expected error from failing UPDATE, got nil")
	}
}

// --- Grant tests ---

func TestGrant_Success(t *testing.T) {
	db := setupGovernanceDB(t)
	store := NewRoleStore(db)
	ctx := context.Background()
	tokenID := NewID()

	id, err := store.Grant(ctx, RoleGrant{TokenID: tokenID, RoleName: "author"})
	if err != nil {
		t.Fatalf("Grant: %v", err)
	}
	if id == "" {
		t.Error("expected non-empty grant ID")
	}
}

func TestGrant_EmptyTokenID(t *testing.T) {
	store := NewRoleStore(newSQLiteDB(t))
	_, err := store.Grant(context.Background(), RoleGrant{RoleName: "author"})
	if err == nil {
		t.Fatal("expected error for empty TokenID")
	}
}

func TestGrant_EmptyRoleName(t *testing.T) {
	store := NewRoleStore(newSQLiteDB(t))
	_, err := store.Grant(context.Background(), RoleGrant{TokenID: NewID()})
	if err == nil {
		t.Fatal("expected error for empty RoleName")
	}
}

func TestGrant_RoleNotFound(t *testing.T) {
	db := setupGovernanceDB(t)
	store := NewRoleStore(db)
	_, err := store.Grant(context.Background(), RoleGrant{TokenID: NewID(), RoleName: "no-such-role"})
	if err == nil {
		t.Fatal("expected ErrNotFound for unknown role")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound in chain, got: %v", err)
	}
}

func TestGrant_RoleLookupError(t *testing.T) {
	db := setupGovernanceDB(t)
	ctx := context.Background()
	wrapped := &govQueryRowFailDB{DB: db, failOn: "FROM smeldr_roles WHERE name"}
	store := NewRoleStore(wrapped)
	_, err := store.Grant(ctx, RoleGrant{TokenID: NewID(), RoleName: "author"})
	if err == nil {
		t.Fatal("expected error from role lookup failure")
	}
}

func TestGrant_InsertError(t *testing.T) {
	db := setupGovernanceDB(t)
	ctx := context.Background()
	wrapped := &execFailDB{DB: db, failOn: "INSERT INTO smeldr_role_grants"}
	store := NewRoleStore(wrapped)
	_, err := store.Grant(ctx, RoleGrant{TokenID: NewID(), RoleName: "author"})
	if err == nil {
		t.Fatal("expected error from failing INSERT")
	}
}

func TestGrant_Idempotent(t *testing.T) {
	db := setupGovernanceDB(t)
	store := NewRoleStore(db)
	ctx := context.Background()
	tokenID := NewID()

	id1, err := store.Grant(ctx, RoleGrant{TokenID: tokenID, RoleName: "editor"})
	if err != nil {
		t.Fatalf("first Grant: %v", err)
	}
	id2, err := store.Grant(ctx, RoleGrant{TokenID: tokenID, RoleName: "editor"})
	if err != nil {
		t.Fatalf("second Grant: %v", err)
	}
	if id1 != id2 {
		t.Errorf("idempotent grant IDs differ: %q vs %q", id1, id2)
	}

	var count int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM smeldr_role_grants WHERE token_id=?`, tokenID,
	).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 grant row, got %d", count)
	}
}

// --- Revoke tests ---

func TestRevoke_Success(t *testing.T) {
	db := setupGovernanceDB(t)
	store := NewRoleStore(db)
	ctx := context.Background()
	tokenID := NewID()

	grantID, err := store.Grant(ctx, RoleGrant{TokenID: tokenID, RoleName: "author"})
	if err != nil {
		t.Fatalf("Grant: %v", err)
	}
	if err := store.Revoke(ctx, grantID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	var count int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM smeldr_role_grants WHERE id=?`, grantID,
	).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Errorf("expected grant to be deleted, got count=%d", count)
	}
}

func TestRevoke_EmptyID(t *testing.T) {
	store := NewRoleStore(newSQLiteDB(t))
	if err := store.Revoke(context.Background(), ""); err == nil {
		t.Fatal("expected error for empty grantID")
	}
}

func TestRevoke_ExecError(t *testing.T) {
	db := setupGovernanceDB(t)
	ctx := context.Background()
	wrapped := &execFailDB{DB: db, failOn: "DELETE FROM smeldr_role_grants"}
	store := NewRoleStore(wrapped)
	if err := store.Revoke(ctx, "fake-id"); err == nil {
		t.Fatal("expected error from failing DELETE")
	}
}

// --- ListGrants tests ---

func TestListGrants_ByToken(t *testing.T) {
	db := setupGovernanceDB(t)
	store := NewRoleStore(db)
	ctx := context.Background()
	tokenID := NewID()

	if _, err := store.Grant(ctx, RoleGrant{TokenID: tokenID, RoleName: "author"}); err != nil {
		t.Fatalf("Grant: %v", err)
	}
	if _, err := store.Grant(ctx, RoleGrant{TokenID: tokenID, RoleName: "editor"}); err != nil {
		t.Fatalf("Grant: %v", err)
	}

	grants, err := store.ListGrants(ctx, tokenID)
	if err != nil {
		t.Fatalf("ListGrants: %v", err)
	}
	if len(grants) != 2 {
		t.Errorf("want 2 grants, got %d", len(grants))
	}
	for _, g := range grants {
		if g.TokenID != tokenID {
			t.Errorf("unexpected tokenID %q", g.TokenID)
		}
	}
}

func TestListGrants_All(t *testing.T) {
	db := setupGovernanceDB(t)
	store := NewRoleStore(db)
	ctx := context.Background()

	for range 3 {
		if _, err := store.Grant(ctx, RoleGrant{TokenID: NewID(), RoleName: "author"}); err != nil {
			t.Fatalf("Grant: %v", err)
		}
	}

	// Empty tokenID returns all grants (plus any from migrateGovernance seed)
	grants, err := store.ListGrants(ctx, "")
	if err != nil {
		t.Fatalf("ListGrants all: %v", err)
	}
	if len(grants) < 3 {
		t.Errorf("expected at least 3 grants, got %d", len(grants))
	}
}

func TestListGrants_Empty(t *testing.T) {
	db := setupGovernanceDB(t)
	store := NewRoleStore(db)
	ctx := context.Background()

	grants, err := store.ListGrants(ctx, NewID()) // token with no grants
	if err != nil {
		t.Fatalf("ListGrants: %v", err)
	}
	if len(grants) != 0 {
		t.Errorf("expected 0 grants, got %d", len(grants))
	}
}

func TestListGrants_QueryError(t *testing.T) {
	db := setupGovernanceDB(t)
	ctx := context.Background()
	wrapped := &govQueryFailDB{DB: db, failOn: "FROM smeldr_role_grants g"}
	store := NewRoleStore(wrapped)
	if _, err := store.ListGrants(ctx, "tok"); err == nil {
		t.Fatal("expected error from failing query")
	}
}

func TestListGrants_ScanError(t *testing.T) {
	db := setupGovernanceDB(t)
	store := NewRoleStore(db)
	ctx := context.Background()
	tokenID := NewID()
	if _, err := store.Grant(ctx, RoleGrant{TokenID: tokenID, RoleName: "author"}); err != nil {
		t.Fatalf("Grant: %v", err)
	}
	// Inject corrupt scope_static so scan succeeds but unmarshal fails.
	if _, err := db.ExecContext(ctx,
		`UPDATE smeldr_role_grants SET scope_static='not-json' WHERE token_id=?`, tokenID,
	); err != nil {
		t.Fatalf("corrupt scope_static: %v", err)
	}
	if _, err := store.ListGrants(ctx, tokenID); err == nil {
		t.Fatal("expected unmarshal error from corrupt scope_static")
	}
}

// TestListGrants_UnmarshalError is covered by TestListGrants_ScanError (corrupt JSON
// after successful scan triggers the unmarshal path).

func TestListGrants_RowsError(t *testing.T) {
	// Use a DB wrapper that returns a valid first row but then injects rows.Err().
	// Simplest coverage: query a non-existent table to force an immediate error.
	db := newSQLiteDB(t)
	ctx := context.Background()
	store := NewRoleStore(db)
	// governance tables not set up — query will fail with "no such table".
	if _, err := store.ListGrants(ctx, "tok"); err == nil {
		t.Fatal("expected error when governance tables absent")
	}
}

// --- Authorized tests ---

func setupTokenWithRole(t *testing.T, db *sql.DB, store *RoleStore, roleName string) string {
	t.Helper()
	tokenID := NewID()
	if _, err := store.Grant(context.Background(), RoleGrant{TokenID: tokenID, RoleName: roleName}); err != nil {
		t.Fatalf("Grant %s: %v", roleName, err)
	}
	return tokenID
}

func TestAuthorized_Global_Match(t *testing.T) {
	db := setupGovernanceDB(t)
	store := NewRoleStore(db)
	tokenID := setupTokenWithRole(t, db, store, "editor")
	ok, err := store.Authorized(context.Background(), tokenID, "delete", AuthTarget{})
	if err != nil {
		t.Fatalf("Authorized: %v", err)
	}
	if !ok {
		t.Error("expected true for global-scope editor with delete op")
	}
}

func TestAuthorized_Global_NoOp(t *testing.T) {
	db := setupGovernanceDB(t)
	store := NewRoleStore(db)
	tokenID := setupTokenWithRole(t, db, store, "author")
	ok, err := store.Authorized(context.Background(), tokenID, "delete", AuthTarget{})
	if err != nil {
		t.Fatalf("Authorized: %v", err)
	}
	if ok {
		t.Error("expected false: author does not have delete op")
	}
}

func TestAuthorized_Static_ExactMatch(t *testing.T) {
	db := setupGovernanceDB(t)
	store := NewRoleStore(db)
	ctx := context.Background()
	itemID := NewID()

	// Define a static-scope role.
	if err := store.DefineRole(ctx, RoleDefinition{
		Name: "post-author", Operations: []string{"update"}, ScopeMode: ScopeStatic,
	}); err != nil {
		t.Fatalf("DefineRole: %v", err)
	}
	tokenID := NewID()
	if _, err := store.Grant(ctx, RoleGrant{
		TokenID: tokenID, RoleName: "post-author",
		ScopeStatic: []string{"post:" + itemID},
	}); err != nil {
		t.Fatalf("Grant: %v", err)
	}

	ok, err := store.Authorized(ctx, tokenID, "update", AuthTarget{TypeName: "post", ID: itemID})
	if err != nil {
		t.Fatalf("Authorized: %v", err)
	}
	if !ok {
		t.Error("expected true for exact static match")
	}
}

func TestAuthorized_Static_Wildcard(t *testing.T) {
	db := setupGovernanceDB(t)
	store := NewRoleStore(db)
	ctx := context.Background()

	if err := store.DefineRole(ctx, RoleDefinition{
		Name: "essay-editor", Operations: []string{"update"}, ScopeMode: ScopeStatic,
	}); err != nil {
		t.Fatalf("DefineRole: %v", err)
	}
	tokenID := NewID()
	if _, err := store.Grant(ctx, RoleGrant{
		TokenID: tokenID, RoleName: "essay-editor",
		ScopeStatic: []string{"essay:*"},
	}); err != nil {
		t.Fatalf("Grant: %v", err)
	}

	ok, err := store.Authorized(ctx, tokenID, "update", AuthTarget{TypeName: "essay", ID: NewID()})
	if err != nil {
		t.Fatalf("Authorized: %v", err)
	}
	if !ok {
		t.Error("expected true for wildcard static match")
	}
	// Different type — should not match.
	ok2, err := store.Authorized(ctx, tokenID, "update", AuthTarget{TypeName: "post", ID: NewID()})
	if err != nil {
		t.Fatalf("Authorized: %v", err)
	}
	if ok2 {
		t.Error("expected false: wildcard type mismatch")
	}
}

func TestAuthorized_Static_NoMatch(t *testing.T) {
	db := setupGovernanceDB(t)
	store := NewRoleStore(db)
	ctx := context.Background()
	allowedID := NewID()

	if err := store.DefineRole(ctx, RoleDefinition{
		Name: "limited", Operations: []string{"update"}, ScopeMode: ScopeStatic,
	}); err != nil {
		t.Fatalf("DefineRole: %v", err)
	}
	tokenID := NewID()
	if _, err := store.Grant(ctx, RoleGrant{
		TokenID: tokenID, RoleName: "limited",
		ScopeStatic: []string{"post:" + allowedID},
	}); err != nil {
		t.Fatalf("Grant: %v", err)
	}

	ok, err := store.Authorized(ctx, tokenID, "update", AuthTarget{TypeName: "post", ID: NewID()})
	if err != nil {
		t.Fatalf("Authorized: %v", err)
	}
	if ok {
		t.Error("expected false: static pattern does not match")
	}
}

func TestAuthorized_Dynamic_Incoming_Match(t *testing.T) {
	db := setupGovernanceDB(t)
	setupRelationsTable(t, db)
	store := NewRoleStore(db)
	ctx := context.Background()

	anchorID := NewID()
	itemID := NewID()
	// Insert asserted relation: item → anchor ("part-of")
	if _, err := db.ExecContext(ctx,
		`INSERT INTO smeldr_relations
			(id, source_id, target_id, relation_kind, edge_class, attributes, created_at, updated_at)
			VALUES (?, ?, ?, 'part-of', 'asserted', '{}', datetime('now'), datetime('now'))`,
		NewID(), itemID, anchorID,
	); err != nil {
		t.Fatalf("insert relation: %v", err)
	}

	if err := store.DefineRole(ctx, RoleDefinition{
		Name: "product-owner", Operations: []string{"update"},
		ScopeMode: ScopeDynamic, ScopeRelationKind: "part-of", ScopeDirection: "incoming",
	}); err != nil {
		t.Fatalf("DefineRole: %v", err)
	}
	tokenID := NewID()
	if _, err := store.Grant(ctx, RoleGrant{
		TokenID: tokenID, RoleName: "product-owner", ScopeAnchorID: anchorID,
	}); err != nil {
		t.Fatalf("Grant: %v", err)
	}

	ok, err := store.Authorized(ctx, tokenID, "update", AuthTarget{ID: itemID})
	if err != nil {
		t.Fatalf("Authorized: %v", err)
	}
	if !ok {
		t.Error("expected true: item has incoming part-of to anchor")
	}
}

func TestAuthorized_Dynamic_Outgoing_Match(t *testing.T) {
	db := setupGovernanceDB(t)
	setupRelationsTable(t, db)
	store := NewRoleStore(db)
	ctx := context.Background()

	anchorID := NewID()
	itemID := NewID()
	// Insert asserted relation: anchor → item ("contains")
	if _, err := db.ExecContext(ctx,
		`INSERT INTO smeldr_relations
			(id, source_id, target_id, relation_kind, edge_class, attributes, created_at, updated_at)
			VALUES (?, ?, ?, 'contains', 'asserted', '{}', datetime('now'), datetime('now'))`,
		NewID(), anchorID, itemID,
	); err != nil {
		t.Fatalf("insert relation: %v", err)
	}

	if err := store.DefineRole(ctx, RoleDefinition{
		Name: "container-owner", Operations: []string{"update"},
		ScopeMode: ScopeDynamic, ScopeRelationKind: "contains", ScopeDirection: "outgoing",
	}); err != nil {
		t.Fatalf("DefineRole: %v", err)
	}
	tokenID := NewID()
	if _, err := store.Grant(ctx, RoleGrant{
		TokenID: tokenID, RoleName: "container-owner", ScopeAnchorID: anchorID,
	}); err != nil {
		t.Fatalf("Grant: %v", err)
	}

	ok, err := store.Authorized(ctx, tokenID, "update", AuthTarget{ID: itemID})
	if err != nil {
		t.Fatalf("Authorized: %v", err)
	}
	if !ok {
		t.Error("expected true: anchor has outgoing contains to item")
	}
}

func TestAuthorized_Dynamic_Both_Match(t *testing.T) {
	db := setupGovernanceDB(t)
	setupRelationsTable(t, db)
	store := NewRoleStore(db)
	ctx := context.Background()

	anchorID := NewID()
	itemID := NewID()
	// Insert asserted relation: anchor → item (found via "outgoing" in "both" check)
	if _, err := db.ExecContext(ctx,
		`INSERT INTO smeldr_relations
			(id, source_id, target_id, relation_kind, edge_class, attributes, created_at, updated_at)
			VALUES (?, ?, ?, 'linked', 'asserted', '{}', datetime('now'), datetime('now'))`,
		NewID(), anchorID, itemID,
	); err != nil {
		t.Fatalf("insert relation: %v", err)
	}

	if err := store.DefineRole(ctx, RoleDefinition{
		Name: "linked-owner", Operations: []string{"update"},
		ScopeMode: ScopeDynamic, ScopeRelationKind: "linked", ScopeDirection: "both",
	}); err != nil {
		t.Fatalf("DefineRole: %v", err)
	}
	tokenID := NewID()
	if _, err := store.Grant(ctx, RoleGrant{
		TokenID: tokenID, RoleName: "linked-owner", ScopeAnchorID: anchorID,
	}); err != nil {
		t.Fatalf("Grant: %v", err)
	}

	ok, err := store.Authorized(ctx, tokenID, "update", AuthTarget{ID: itemID})
	if err != nil {
		t.Fatalf("Authorized: %v", err)
	}
	if !ok {
		t.Error("expected true: relation found in both-direction check")
	}
}

func TestAuthorized_Dynamic_NoMatch(t *testing.T) {
	db := setupGovernanceDB(t)
	setupRelationsTable(t, db)
	store := NewRoleStore(db)
	ctx := context.Background()

	anchorID := NewID()

	if err := store.DefineRole(ctx, RoleDefinition{
		Name: "no-rel-role", Operations: []string{"update"},
		ScopeMode: ScopeDynamic, ScopeRelationKind: "part-of", ScopeDirection: "incoming",
	}); err != nil {
		t.Fatalf("DefineRole: %v", err)
	}
	tokenID := NewID()
	if _, err := store.Grant(ctx, RoleGrant{
		TokenID: tokenID, RoleName: "no-rel-role", ScopeAnchorID: anchorID,
	}); err != nil {
		t.Fatalf("Grant: %v", err)
	}

	ok, err := store.Authorized(ctx, tokenID, "update", AuthTarget{ID: NewID()})
	if err != nil {
		t.Fatalf("Authorized: %v", err)
	}
	if ok {
		t.Error("expected false: no relation in DB")
	}
}

func TestAuthorized_Dynamic_EmptyID(t *testing.T) {
	db := setupGovernanceDB(t)
	setupRelationsTable(t, db)
	store := NewRoleStore(db)
	ctx := context.Background()

	anchorID := NewID()
	if err := store.DefineRole(ctx, RoleDefinition{
		Name: "dyn-role", Operations: []string{"update"},
		ScopeMode: ScopeDynamic, ScopeRelationKind: "part-of", ScopeDirection: "incoming",
	}); err != nil {
		t.Fatalf("DefineRole: %v", err)
	}
	tokenID := NewID()
	if _, err := store.Grant(ctx, RoleGrant{
		TokenID: tokenID, RoleName: "dyn-role", ScopeAnchorID: anchorID,
	}); err != nil {
		t.Fatalf("Grant: %v", err)
	}

	// Empty target.ID — dynamic scope cannot resolve, should be false, nil.
	ok, err := store.Authorized(ctx, tokenID, "update", AuthTarget{})
	if err != nil {
		t.Fatalf("Authorized: %v", err)
	}
	if ok {
		t.Error("expected false when target.ID is empty")
	}
}

func TestAuthorized_NoGrants(t *testing.T) {
	db := setupGovernanceDB(t)
	store := NewRoleStore(db)
	ok, err := store.Authorized(context.Background(), NewID(), "read", AuthTarget{})
	if err != nil {
		t.Fatalf("Authorized: %v", err)
	}
	if ok {
		t.Error("expected false for token with no grants")
	}
}

func TestAuthorized_QueryError(t *testing.T) {
	db := setupGovernanceDB(t)
	ctx := context.Background()
	wrapped := &govQueryFailDB{DB: db, failOn: "FROM smeldr_role_grants g"}
	store := NewRoleStore(wrapped)
	if _, err := store.Authorized(ctx, NewID(), "read", AuthTarget{}); err == nil {
		t.Fatal("expected error from failing grants query")
	}
}

func TestAuthorized_ScanError(t *testing.T) {
	db := setupGovernanceDB(t)
	store := NewRoleStore(db)
	ctx := context.Background()

	tokenID := NewID()
	if _, err := store.Grant(ctx, RoleGrant{TokenID: tokenID, RoleName: "author"}); err != nil {
		t.Fatalf("Grant: %v", err)
	}
	// Corrupt the operations JSON so scan succeeds but the scan itself of a different
	// column type would fail — instead verify via corrupt scope_static that JSON parse
	// errors in operations are skipped (continue path).
	// Note: scan itself can't easily be forced to fail without breaking the DB wrapper.
	// The corrupt-operations case exercises the continue path (malformed ops → skip).
	if _, err := db.ExecContext(ctx,
		`UPDATE smeldr_role_grants SET scope_static=']bad' WHERE token_id=?`, tokenID,
	); err != nil {
		t.Fatalf("corrupt scope_static: %v", err)
	}
	// Corrupt operations so the row is skipped (continue path).
	if _, err := db.ExecContext(ctx,
		`UPDATE smeldr_roles SET operations=']bad' WHERE name='author'`,
	); err != nil {
		t.Fatalf("corrupt operations: %v", err)
	}
	// No grants can now authorize — result should be false, nil (skip-on-corrupt path).
	ok, err := store.Authorized(ctx, tokenID, "create", AuthTarget{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("expected false with corrupt operations row skipped")
	}
}

func TestAuthorized_RowsError(t *testing.T) {
	// Force rows.Err() by querying without setting up governance tables.
	db := newSQLiteDB(t)
	store := NewRoleStore(db)
	// Tables don't exist — query will fail at the query level, not rows.Err().
	// Cover rows.Err() differently: set up tables, add a grant, then drop the
	// roles table mid-iteration to trigger rows.Err() on Close.
	// Simplest approach: use govQueryFailDB to fail the join query.
	ctx := context.Background()
	if _, err := store.Authorized(ctx, "tok", "read", AuthTarget{}); err == nil {
		t.Fatal("expected error when governance tables absent")
	}
}

func TestAuthorized_Dynamic_QueryError(t *testing.T) {
	// Only errored dynamic-scope grant; no other grant authorizes → false, err.
	db := setupGovernanceDB(t)
	setupRelationsTable(t, db)
	store := NewRoleStore(db)
	ctx := context.Background()

	anchorID := NewID()
	if err := store.DefineRole(ctx, RoleDefinition{
		Name: "dyn-err-role", Operations: []string{"update"},
		ScopeMode: ScopeDynamic, ScopeRelationKind: "part-of", ScopeDirection: "incoming",
	}); err != nil {
		t.Fatalf("DefineRole: %v", err)
	}
	tokenID := NewID()
	if _, err := store.Grant(ctx, RoleGrant{
		TokenID: tokenID, RoleName: "dyn-err-role", ScopeAnchorID: anchorID,
	}); err != nil {
		t.Fatalf("Grant: %v", err)
	}

	// Wrap the DB so the smeldr_relations query fails.
	wrapped := &govQueryRowFailDB{DB: db, failOn: "FROM smeldr_relations"}
	storeWrapped := NewRoleStore(wrapped)

	ok, err := storeWrapped.Authorized(ctx, tokenID, "update", AuthTarget{ID: NewID()})
	if err == nil {
		t.Fatal("expected error surfaced when only errored dynamic grant exists")
	}
	if ok {
		t.Error("expected false")
	}
}

func TestAuthorized_Dynamic_QueryError_OtherGrantWins(t *testing.T) {
	// Two grants on same token: dynamic-scope errors, global-scope matches.
	// Result must be true, nil — error is swallowed because another grant authorized.
	db := setupGovernanceDB(t)
	setupRelationsTable(t, db)
	store := NewRoleStore(db)
	ctx := context.Background()

	anchorID := NewID()
	if err := store.DefineRole(ctx, RoleDefinition{
		Name: "dyn-err2", Operations: []string{"update"},
		ScopeMode: ScopeDynamic, ScopeRelationKind: "part-of", ScopeDirection: "incoming",
	}); err != nil {
		t.Fatalf("DefineRole dyn: %v", err)
	}

	tokenID := NewID()
	// Dynamic-scope grant (will error on relation query).
	if _, err := store.Grant(ctx, RoleGrant{
		TokenID: tokenID, RoleName: "dyn-err2", ScopeAnchorID: anchorID,
	}); err != nil {
		t.Fatalf("Grant dyn: %v", err)
	}
	// Global-scope admin grant (should authorize regardless of the dyn error).
	if _, err := store.Grant(ctx, RoleGrant{
		TokenID: tokenID, RoleName: "admin",
	}); err != nil {
		t.Fatalf("Grant admin: %v", err)
	}

	// Wrap DB so smeldr_relations fails — but admin global grant still authorizes.
	wrapped := &govQueryRowFailDB{DB: db, failOn: "FROM smeldr_relations"}
	storeWrapped := NewRoleStore(wrapped)

	ok, err := storeWrapped.Authorized(ctx, tokenID, "update", AuthTarget{ID: NewID()})
	if err != nil {
		t.Fatalf("unexpected error — global grant should have authorized: %v", err)
	}
	if !ok {
		t.Error("expected true: admin global grant authorized even though dyn-scope errored")
	}
}

// --- App.Governance and App.RoleStore tests ---

func TestAppGovernance_NilStore(t *testing.T) {
	app := New(Config{BaseURL: "https://example.com", Secret: make([]byte, 16)})
	if err := app.Governance(nil); err == nil {
		t.Fatal("expected error for nil store")
	}
}

func TestAppGovernance_NilDB(t *testing.T) {
	// App with no DB — migrateGovernance will fail.
	app := New(Config{BaseURL: "https://example.com", Secret: make([]byte, 16)})
	store := NewRoleStore(nil)
	if err := app.Governance(store); err == nil {
		t.Fatal("expected error when DB is nil")
	}
}

func TestAppGovernance_Success(t *testing.T) {
	db := newSQLiteDB(t)
	setupTokensTable(t, db)
	app := New(Config{BaseURL: "https://example.com", Secret: make([]byte, 16), DB: db})
	store := NewRoleStore(db)
	if err := app.Governance(store); err != nil {
		t.Fatalf("Governance: %v", err)
	}
	if app.RoleStore() == nil {
		t.Error("expected RoleStore to be non-nil after Governance()")
	}
}

func TestAppRoleStore_Nil(t *testing.T) {
	app := New(Config{BaseURL: "https://example.com", Secret: make([]byte, 16)})
	if app.RoleStore() != nil {
		t.Error("expected nil RoleStore before Governance() is called")
	}
}

func TestAppRoleStore_Set(t *testing.T) {
	db := newSQLiteDB(t)
	setupTokensTable(t, db)
	app := New(Config{BaseURL: "https://example.com", Secret: make([]byte, 16), DB: db})
	store := NewRoleStore(db)
	if err := app.Governance(store); err != nil {
		t.Fatalf("Governance: %v", err)
	}
	if got := app.RoleStore(); got != store {
		t.Error("RoleStore() returned wrong store instance")
	}
}

func TestAppGovernance_DBMismatch(t *testing.T) {
	db1 := newSQLiteDB(t)
	setupTokensTable(t, db1)
	db2 := newSQLiteDB(t) // different DB instance
	app := New(Config{BaseURL: "https://example.com", Secret: make([]byte, 16), DB: db1})
	store := NewRoleStore(db2) // store backed by a different DB
	if err := app.Governance(store); err == nil {
		t.Fatal("expected error when store.db != app.cfg.DB")
	}
}

func TestDefineRole_NilOperations(t *testing.T) {
	db := setupGovernanceDB(t)
	store := NewRoleStore(db)
	ctx := context.Background()
	// nil Operations must be stored as "[]", not null.
	if err := store.DefineRole(ctx, RoleDefinition{Name: "empty-role"}); err != nil {
		t.Fatalf("DefineRole: %v", err)
	}
	var ops string
	if err := db.QueryRowContext(ctx,
		`SELECT operations FROM smeldr_roles WHERE name='empty-role'`,
	).Scan(&ops); err != nil {
		t.Fatalf("query: %v", err)
	}
	if ops != `[]` {
		t.Errorf("operations: got %q, want %q", ops, `[]`)
	}
}

func TestDefineRole_AllowSelfApproval(t *testing.T) {
	db := setupGovernanceDB(t)
	store := NewRoleStore(db)
	ctx := context.Background()
	if err := store.DefineRole(ctx, RoleDefinition{
		Name: "plan-role", Operations: []string{"approve"},
		TrustLevel: 2, AllowSelfApproval: true,
	}); err != nil {
		t.Fatalf("DefineRole: %v", err)
	}
	var selfApproval int
	if err := db.QueryRowContext(ctx,
		`SELECT allow_self_approval FROM smeldr_roles WHERE name='plan-role'`,
	).Scan(&selfApproval); err != nil {
		t.Fatalf("query: %v", err)
	}
	if selfApproval != 1 {
		t.Errorf("allow_self_approval: got %d, want 1", selfApproval)
	}
}

func TestGrant_ResolveIDError(t *testing.T) {
	// Covers the resolve-grant-id error path (null anchor): after a successful
	// INSERT (or 0-row no-op), the SELECT to find the canonical grant ID fails.
	db := setupGovernanceDB(t)
	ctx := context.Background()
	// Fail specifically the SELECT that looks up the grant by token+role+null-anchor.
	wrapped := &govQueryRowFailDB{DB: db, failOn: "scope_anchor_id IS NULL"}
	store := NewRoleStore(wrapped)
	_, err := store.Grant(ctx, RoleGrant{TokenID: NewID(), RoleName: "author"})
	if err == nil {
		t.Fatal("expected error from failing resolve-grant-id query")
	}
}

func TestGrant_ResolveIDError_WithAnchor(t *testing.T) {
	// Covers the resolve-grant-id error path when anchorID is non-nil.
	db := setupGovernanceDB(t)
	ctx := context.Background()
	if err := NewRoleStore(db).DefineRole(ctx, RoleDefinition{
		Name: "dyn-role2", Operations: []string{"update"},
		ScopeMode: ScopeDynamic, ScopeRelationKind: "part-of", ScopeDirection: "incoming",
	}); err != nil {
		t.Fatalf("DefineRole: %v", err)
	}
	// Fail the SELECT that resolves the grant id by anchor (scope_anchor_id=?).
	wrapped := &govQueryRowFailDB{DB: db, failOn: "scope_anchor_id=?"}
	store := NewRoleStore(wrapped)
	_, err := store.Grant(ctx, RoleGrant{
		TokenID: NewID(), RoleName: "dyn-role2", ScopeAnchorID: NewID(),
	})
	if err == nil {
		t.Fatal("expected error from failing resolve-grant-id query (with anchor)")
	}
}

func TestListGrants_WithAnchor(t *testing.T) {
	// Covers the anchorID.Valid branch in the scan loop.
	db := setupGovernanceDB(t)
	store := NewRoleStore(db)
	ctx := context.Background()

	anchorID := NewID()
	if err := store.DefineRole(ctx, RoleDefinition{
		Name: "anchor-role", Operations: []string{"update"}, ScopeMode: ScopeDynamic,
		ScopeRelationKind: "part-of", ScopeDirection: "incoming",
	}); err != nil {
		t.Fatalf("DefineRole: %v", err)
	}
	tokenID := NewID()
	if _, err := store.Grant(ctx, RoleGrant{
		TokenID: tokenID, RoleName: "anchor-role", ScopeAnchorID: anchorID,
	}); err != nil {
		t.Fatalf("Grant: %v", err)
	}

	grants, err := store.ListGrants(ctx, tokenID)
	if err != nil {
		t.Fatalf("ListGrants: %v", err)
	}
	if len(grants) != 1 {
		t.Fatalf("expected 1 grant, got %d", len(grants))
	}
	if grants[0].ScopeAnchorID != anchorID {
		t.Errorf("ScopeAnchorID: got %q, want %q", grants[0].ScopeAnchorID, anchorID)
	}
}

// TestListGrants_ScanNullError verifies that ListGrants returns an error when
// the query returns rows with fewer columns than expected (scan mismatch).
func TestListGrants_ScanNullError(t *testing.T) {
	db := setupGovernanceDB(t)
	// Replace FROM smeldr_role_grants with a 2-column NULL row; scanning into
	// 6 destinations causes a column-count mismatch error.
	wrapped := &govQueryNullRowsDB{DB: db, nullOn: "FROM smeldr_role_grants g"}
	store := NewRoleStore(wrapped)
	if _, err := store.ListGrants(context.Background(), "any"); err == nil {
		t.Fatal("expected scan error from wrong-arity NULL rows, got nil")
	}
}

func TestAuthorized_Static_CorruptStaticJSON(t *testing.T) {
	// Covers the corrupt-scope_static continue path in Authorized: valid ops
	// (so the op check passes), static scope mode, but scope_static JSON is corrupt.
	db := setupGovernanceDB(t)
	store := NewRoleStore(db)
	ctx := context.Background()

	if err := store.DefineRole(ctx, RoleDefinition{
		Name: "static-role", Operations: []string{"update"}, ScopeMode: ScopeStatic,
	}); err != nil {
		t.Fatalf("DefineRole: %v", err)
	}
	tokenID := NewID()
	if _, err := store.Grant(ctx, RoleGrant{
		TokenID: tokenID, RoleName: "static-role", ScopeStatic: []string{"post:x"},
	}); err != nil {
		t.Fatalf("Grant: %v", err)
	}
	// Corrupt scope_static so the static JSON unmarshal path is hit.
	if _, err := db.ExecContext(ctx,
		`UPDATE smeldr_role_grants SET scope_static=']bad' WHERE token_id=?`, tokenID,
	); err != nil {
		t.Fatalf("corrupt scope_static: %v", err)
	}
	// Result must be false, nil (row is skipped).
	ok, err := store.Authorized(ctx, tokenID, "update", AuthTarget{TypeName: "post", ID: "x"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("expected false when scope_static JSON is corrupt")
	}
}

func TestAuthorized_Dynamic_OutgoingQueryError(t *testing.T) {
	// Covers the outgoing-direction error path in relationExists.
	// Uses direction="outgoing" so only the outgoing query is made; when it errors,
	// pendingErr is set and the result is false, err.
	db := setupGovernanceDB(t)
	setupRelationsTable(t, db)
	store := NewRoleStore(db)
	ctx := context.Background()

	anchorID := NewID()
	if err := store.DefineRole(ctx, RoleDefinition{
		Name: "out-err-role", Operations: []string{"update"},
		ScopeMode: ScopeDynamic, ScopeRelationKind: "part-of", ScopeDirection: "outgoing",
	}); err != nil {
		t.Fatalf("DefineRole: %v", err)
	}
	tokenID := NewID()
	if _, err := store.Grant(ctx, RoleGrant{
		TokenID: tokenID, RoleName: "out-err-role", ScopeAnchorID: anchorID,
	}); err != nil {
		t.Fatalf("Grant: %v", err)
	}

	wrapped := &govQueryRowFailDB{DB: db, failOn: "FROM smeldr_relations"}
	storeWrapped := NewRoleStore(wrapped)

	ok, err := storeWrapped.Authorized(ctx, tokenID, "update", AuthTarget{ID: NewID()})
	if err == nil {
		t.Fatal("expected error from failing outgoing relation query")
	}
	if ok {
		t.Error("expected false")
	}
}

func TestDefineRole_TrustLevel1Rejected(t *testing.T) {
	db := setupGovernanceDB(t)
	store := NewRoleStore(db)
	if err := store.DefineRole(context.Background(), RoleDefinition{
		Name: "bad-role", Operations: []string{"read"}, TrustLevel: 1,
	}); err == nil {
		t.Fatal("expected error for trust_level=1 (not yet defined)")
	}
}

func TestAuthorized_Dynamic_AssertedOnly(t *testing.T) {
	// An inferred (edge_class != 'asserted') relation must NOT grant scope.
	db := setupGovernanceDB(t)
	setupRelationsTable(t, db)
	store := NewRoleStore(db)
	ctx := context.Background()

	anchorID := NewID()
	itemID := NewID()
	// Insert inferred relation — should be ignored by the active-edge predicate.
	if _, err := db.ExecContext(ctx,
		`INSERT INTO smeldr_relations
			(id, source_id, target_id, relation_kind, edge_class, attributes, created_at, updated_at)
			VALUES (?, ?, ?, 'part-of', 'inferred', '{}', datetime('now'), datetime('now'))`,
		NewID(), itemID, anchorID,
	); err != nil {
		t.Fatalf("insert inferred relation: %v", err)
	}

	if err := store.DefineRole(ctx, RoleDefinition{
		Name: "inferred-scope", Operations: []string{"update"},
		ScopeMode: ScopeDynamic, ScopeRelationKind: "part-of", ScopeDirection: "incoming",
	}); err != nil {
		t.Fatalf("DefineRole: %v", err)
	}
	tokenID := NewID()
	if _, err := store.Grant(ctx, RoleGrant{
		TokenID: tokenID, RoleName: "inferred-scope", ScopeAnchorID: anchorID,
	}); err != nil {
		t.Fatalf("Grant: %v", err)
	}

	ok, err := store.Authorized(ctx, tokenID, "update", AuthTarget{ID: itemID})
	if err != nil {
		t.Fatalf("Authorized: %v", err)
	}
	if ok {
		t.Error("expected false: inferred relation must not satisfy dynamic scope")
	}
}

// --- GovernanceAuditStore and WithAudit tests ---

// failAppendAuditStore is a GovernanceAuditStore whose Append always fails.
// Used to exercise the fail-closed audit error paths in DefineRole/Grant/Revoke.
type failAppendAuditStore struct{}

func (f *failAppendAuditStore) Append(_ context.Context, _ GovernanceAuditRecord) error {
	return errors.New("simulated audit append failure")
}

// setupGovernanceAuditDB creates a DB with governance tables + audit table and
// returns the DB, an unwired RoleStore, and a wired GovernanceAuditStore.
func setupGovernanceAuditDB(t *testing.T) (*sql.DB, *RoleStore, GovernanceAuditStore) {
	t.Helper()
	db := setupGovernanceDB(t)
	if err := CreateGovernanceAuditTable(db); err != nil {
		t.Fatalf("CreateGovernanceAuditTable: %v", err)
	}
	return db, NewRoleStore(db), NewGovernanceAuditStore(db)
}

func TestCreateGovernanceAuditTable_Idempotent(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()
	for i := range 2 {
		if err := CreateGovernanceAuditTable(db); err != nil {
			t.Fatalf("run %d: CreateGovernanceAuditTable: %v", i+1, err)
		}
	}
	var n int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='smeldr_governance_audit'`,
	).Scan(&n); err != nil || n == 0 {
		t.Error("table smeldr_governance_audit not found after CreateGovernanceAuditTable")
	}
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_governance_audit_actor'`,
	).Scan(&n); err != nil || n == 0 {
		t.Error("index idx_governance_audit_actor not found after CreateGovernanceAuditTable")
	}
}

func TestCreateGovernanceAuditTable_ExecError(t *testing.T) {
	t.Run("create_table", func(t *testing.T) {
		wrapped := &execFailDB{DB: newSQLiteDB(t), failOn: "smeldr_governance_audit"}
		if err := CreateGovernanceAuditTable(wrapped); err == nil {
			t.Fatal("expected error from CREATE TABLE failure, got nil")
		}
	})
	t.Run("create_index", func(t *testing.T) {
		wrapped := &execFailDB{DB: newSQLiteDB(t), failOn: "idx_governance_audit_actor"}
		if err := CreateGovernanceAuditTable(wrapped); err == nil {
			t.Fatal("expected error from CREATE INDEX failure, got nil")
		}
	})
}

func TestGovernanceAuditStore_Append(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateGovernanceAuditTable(db); err != nil {
		t.Fatalf("CreateGovernanceAuditTable: %v", err)
	}
	store := NewGovernanceAuditStore(db)
	ctx := context.Background()
	rec := GovernanceAuditRecord{
		ID:           NewID(),
		ActorTokenID: "tok-1",
		Action:       "define_role",
		TargetKind:   "role",
		TargetID:     "role-id-1",
		Before:       "{}",
		After:        `{"name":"reviewer"}`,
		CreatedAt:    time.Now().UTC(),
	}
	if err := store.Append(ctx, rec); err != nil {
		t.Fatalf("Append: %v", err)
	}
	var count int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM smeldr_governance_audit WHERE id=?`, rec.ID,
	).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 audit row, got %d", count)
	}
}

func TestGovernanceAuditStore_AppendError(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateGovernanceAuditTable(db); err != nil {
		t.Fatalf("CreateGovernanceAuditTable: %v", err)
	}
	wrapped := &execFailDB{DB: db, failOn: "INSERT INTO smeldr_governance_audit"}
	store := NewGovernanceAuditStore(wrapped)
	err := store.Append(context.Background(), GovernanceAuditRecord{
		ID: NewID(), ActorTokenID: "tok", Action: "grant",
		TargetKind: "grant", TargetID: "g1", CreatedAt: time.Now(),
	})
	if err == nil {
		t.Fatal("expected error from INSERT failure, got nil")
	}
}

func TestWithAudit_DefineRole_NewRole(t *testing.T) {
	db, store, auditStore := setupGovernanceAuditDB(t)
	actor := "tok-actor"
	audited := store.WithAudit(actor, auditStore)
	ctx := context.Background()

	if err := audited.DefineRole(ctx, RoleDefinition{
		Name: "reviewer", Operations: []string{"review"}, ScopeMode: ScopeGlobal,
	}); err != nil {
		t.Fatalf("DefineRole: %v", err)
	}

	var action, targetKind, before, actorID string
	if err := db.QueryRowContext(ctx,
		`SELECT action, target_kind, before_json, actor_token_id
		   FROM smeldr_governance_audit ORDER BY created_at DESC LIMIT 1`,
	).Scan(&action, &targetKind, &before, &actorID); err != nil {
		t.Fatalf("query audit: %v", err)
	}
	if action != "define_role" {
		t.Errorf("action: want define_role, got %q", action)
	}
	if targetKind != "role" {
		t.Errorf("target_kind: want role, got %q", targetKind)
	}
	if before != "{}" {
		t.Errorf("before: want {}, got %q", before)
	}
	if actorID != actor {
		t.Errorf("actor_token_id: want %q, got %q", actor, actorID)
	}
}

func TestWithAudit_DefineRole_UpdateRole(t *testing.T) {
	db, store, auditStore := setupGovernanceAuditDB(t)
	actor := "tok-actor"
	audited := store.WithAudit(actor, auditStore)
	ctx := context.Background()

	if err := audited.DefineRole(ctx, RoleDefinition{
		Name: "reviewer", Operations: []string{"review"}, ScopeMode: ScopeGlobal,
	}); err != nil {
		t.Fatalf("first DefineRole: %v", err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM smeldr_governance_audit`); err != nil {
		t.Fatalf("truncate audit: %v", err)
	}
	if err := audited.DefineRole(ctx, RoleDefinition{
		Name: "reviewer", Operations: []string{"review", "approve"}, ScopeMode: ScopeGlobal,
	}); err != nil {
		t.Fatalf("second DefineRole: %v", err)
	}

	var before string
	if err := db.QueryRowContext(ctx,
		`SELECT before_json FROM smeldr_governance_audit LIMIT 1`,
	).Scan(&before); err != nil {
		t.Fatalf("query audit: %v", err)
	}
	if before == "{}" {
		t.Error("before_json for update should contain existing role data, not {}")
	}
	if !strings.Contains(before, "reviewer") {
		t.Errorf("before_json should contain role name, got %q", before)
	}
}

func TestWithAudit_DefineRole_BeforeSelectError(t *testing.T) {
	db := setupGovernanceDB(t)
	ctx := context.Background()
	if err := CreateGovernanceAuditTable(db); err != nil {
		t.Fatalf("CreateGovernanceAuditTable: %v", err)
	}
	wrapped := &govQueryRowFailDB{DB: db, failOn: "FROM smeldr_roles WHERE name"}
	store := NewRoleStore(wrapped).WithAudit("actor", NewGovernanceAuditStore(db))
	if err := store.DefineRole(ctx, RoleDefinition{
		Name: "x", Operations: []string{"read"},
	}); err == nil {
		t.Fatal("expected error from before-state SELECT failure, got nil")
	}
}

func TestWithAudit_DefineRole_AppendError(t *testing.T) {
	db := setupGovernanceDB(t)
	ctx := context.Background()
	store := NewRoleStore(db).WithAudit("actor", &failAppendAuditStore{})

	err := store.DefineRole(ctx, RoleDefinition{Name: "reviewer", Operations: []string{"review"}})
	if err == nil {
		t.Fatal("expected error from Append failure, got nil")
	}
	// The mutation must be persisted despite the Append failure (non-atomic audit).
	var count int
	if err2 := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM smeldr_roles WHERE name='reviewer'`,
	).Scan(&count); err2 != nil {
		t.Fatalf("query: %v", err2)
	}
	if count != 1 {
		t.Error("role row must be persisted even though DefineRole returned an error (non-atomic audit)")
	}
}

func TestWithAudit_DefineRole_NoAudit(t *testing.T) {
	db := setupGovernanceDB(t)
	store := NewRoleStore(db) // nil auditStore — no audit table needed
	ctx := context.Background()
	if err := store.DefineRole(ctx, RoleDefinition{
		Name: "reviewer", Operations: []string{"review"},
	}); err != nil {
		t.Fatalf("DefineRole without audit: %v", err)
	}
	var count int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM smeldr_roles WHERE name='reviewer'`,
	).Scan(&count); err != nil {
		t.Fatalf("query: %v", err)
	}
	if count != 1 {
		t.Error("expected role to be defined")
	}
}

func TestWithAudit_DefineRole_ResolveIDError(t *testing.T) {
	// Covers the resolve-id error path: before-state returns ErrNoRows (new role),
	// INSERT/UPDATE succeed, then the post-insert SELECT id FROM smeldr_roles fails.
	db := setupGovernanceDB(t)
	ctx := context.Background()
	if err := CreateGovernanceAuditTable(db); err != nil {
		t.Fatalf("CreateGovernanceAuditTable: %v", err)
	}
	// The before-state query selects many columns; the resolve-ID query selects
	// only "id FROM smeldr_roles". The failOn string matches only the latter.
	wrapped := &govQueryRowFailDB{DB: db, failOn: "SELECT id FROM smeldr_roles WHERE name"}
	store := NewRoleStore(wrapped).WithAudit("actor", NewGovernanceAuditStore(db))
	if err := store.DefineRole(ctx, RoleDefinition{
		Name: "reviewer", Operations: []string{"review"},
	}); err == nil {
		t.Fatal("expected error from resolve-ID query failure, got nil")
	}
}

func TestWithAudit_Grant_Recorded(t *testing.T) {
	db, store, auditStore := setupGovernanceAuditDB(t)
	actor := "tok-actor"
	audited := store.WithAudit(actor, auditStore)
	ctx := context.Background()
	tokenID := NewID()

	grantID, err := audited.Grant(ctx, RoleGrant{TokenID: tokenID, RoleName: "author"})
	if err != nil {
		t.Fatalf("Grant: %v", err)
	}

	var action, targetKind, targetID, before string
	if err := db.QueryRowContext(ctx,
		`SELECT action, target_kind, target_id, before_json
		   FROM smeldr_governance_audit LIMIT 1`,
	).Scan(&action, &targetKind, &targetID, &before); err != nil {
		t.Fatalf("query audit: %v", err)
	}
	if action != "grant" {
		t.Errorf("action: want grant, got %q", action)
	}
	if targetKind != "grant" {
		t.Errorf("target_kind: want grant, got %q", targetKind)
	}
	if targetID != grantID {
		t.Errorf("target_id: want %q, got %q", grantID, targetID)
	}
	if before != "{}" {
		t.Errorf("before: want {}, got %q", before)
	}
}

func TestWithAudit_Grant_AppendError(t *testing.T) {
	db := setupGovernanceDB(t)
	ctx := context.Background()
	store := NewRoleStore(db).WithAudit("actor", &failAppendAuditStore{})
	if _, err := store.Grant(ctx, RoleGrant{TokenID: NewID(), RoleName: "author"}); err == nil {
		t.Fatal("expected error from Append failure, got nil")
	}
}

// TestWithAudit_Revoke_Recorded verifies the full before/after audit record for
// Revoke. Uses a grant with ScopeAnchorID to cover the eanch.Valid branch in
// the before-state scan, and the anchorID != nil branch in Grant's audit.
func TestWithAudit_Revoke_Recorded(t *testing.T) {
	db, store, auditStore := setupGovernanceAuditDB(t)
	actor := "tok-actor"
	audited := store.WithAudit(actor, auditStore)
	ctx := context.Background()
	tokenID := NewID()
	anchorID := NewID()

	grantID, err := audited.Grant(ctx, RoleGrant{
		TokenID: tokenID, RoleName: "author", ScopeAnchorID: anchorID,
	})
	if err != nil {
		t.Fatalf("Grant: %v", err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM smeldr_governance_audit`); err != nil {
		t.Fatalf("truncate audit: %v", err)
	}

	if err := audited.Revoke(ctx, grantID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	var action, before, after string
	if err := db.QueryRowContext(ctx,
		`SELECT action, before_json, after_json FROM smeldr_governance_audit LIMIT 1`,
	).Scan(&action, &before, &after); err != nil {
		t.Fatalf("query audit: %v", err)
	}
	if action != "revoke" {
		t.Errorf("action: want revoke, got %q", action)
	}
	if before == "{}" {
		t.Error("before_json for revoke should contain grant data, not {}")
	}
	if !strings.Contains(before, anchorID) {
		t.Errorf("before_json should contain anchor ID %q, got %q", anchorID, before)
	}
	if after != "{}" {
		t.Errorf("after: want {}, got %q", after)
	}
}

func TestWithAudit_Revoke_BeforeSelectError(t *testing.T) {
	db := setupGovernanceDB(t)
	ctx := context.Background()
	if err := CreateGovernanceAuditTable(db); err != nil {
		t.Fatalf("CreateGovernanceAuditTable: %v", err)
	}
	// Create a real grant using an unwrapped store.
	grantID, err := NewRoleStore(db).Grant(ctx, RoleGrant{TokenID: NewID(), RoleName: "author"})
	if err != nil {
		t.Fatalf("Grant: %v", err)
	}
	// Fail the before-state SELECT on smeldr_role_grants.
	wrapped := &govQueryRowFailDB{DB: db, failOn: "FROM smeldr_role_grants WHERE id"}
	store := NewRoleStore(wrapped).WithAudit("actor", NewGovernanceAuditStore(db))
	if err := store.Revoke(ctx, grantID); err == nil {
		t.Fatal("expected error from before-state SELECT failure, got nil")
	}
}

func TestWithAudit_Revoke_AppendError(t *testing.T) {
	db := setupGovernanceDB(t)
	ctx := context.Background()
	grantID, err := NewRoleStore(db).Grant(ctx, RoleGrant{TokenID: NewID(), RoleName: "author"})
	if err != nil {
		t.Fatalf("Grant: %v", err)
	}
	store := NewRoleStore(db).WithAudit("actor", &failAppendAuditStore{})
	if err := store.Revoke(ctx, grantID); err == nil {
		t.Fatal("expected error from Append failure, got nil")
	}
}

// --- ToolPolicy tests ---

func TestRoleStore_ToolPolicy_Hit(t *testing.T) {
	db := setupGovernanceDB(t)
	store := NewRoleStore(db)
	op, found, err := store.ToolPolicy(context.Background(), "create_post")
	if err != nil {
		t.Fatalf("ToolPolicy: %v", err)
	}
	if !found {
		t.Fatal("expected found=true for seeded tool create_post")
	}
	if op == "" {
		t.Error("expected non-empty requiredOp for create_post")
	}
}

func TestRoleStore_ToolPolicy_NotFound(t *testing.T) {
	db := setupGovernanceDB(t)
	store := NewRoleStore(db)
	op, found, err := store.ToolPolicy(context.Background(), "no_such_tool_xyz")
	if err != nil {
		t.Fatalf("ToolPolicy: %v", err)
	}
	if found {
		t.Errorf("expected found=false for unknown tool, got op=%q", op)
	}
}

func TestRoleStore_ToolPolicy_QueryError(t *testing.T) {
	db := setupGovernanceDB(t)
	wrapped := &govQueryRowFailDB{DB: db, failOn: "FROM smeldr_tool_policies"}
	store := NewRoleStore(wrapped)
	_, _, err := store.ToolPolicy(context.Background(), "create_post")
	if err == nil {
		t.Fatal("expected error from QueryRowContext failure, got nil")
	}
}
