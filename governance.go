package smeldr

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"
)

// ScopeMode controls how a role grant's scope is resolved.
//
//   - [ScopeGlobal]: no restriction — the grant applies to every item.
//   - [ScopeStatic]: an explicit list of "type:slug" or "type:*" patterns.
//   - [ScopeDynamic]: one hop from an anchor item via a named relation kind.
type ScopeMode string

const (
	// ScopeGlobal applies the grant to every item regardless of type or slug.
	ScopeGlobal ScopeMode = "global"
	// ScopeStatic restricts the grant to an explicit list of "type:slug" patterns.
	ScopeStatic ScopeMode = "static"
	// ScopeDynamic restricts the grant to items reachable in one hop from an
	// anchor item via a named relation kind and direction.
	ScopeDynamic ScopeMode = "dynamic"
)

// migrateGovernance creates the three governance tables, seeds default roles and
// tool policies, and migrates existing token role strings into smeldr_role_grants.
//
// The function is not called from [New] automatically — it is opt-in. [App.Governance]
// (added in T49 Step 2) is the entry point that calls it at startup.
//
// All DDL statements use CREATE TABLE IF NOT EXISTS and INSERT OR IGNORE so the
// function is safe to call on every boot (idempotent).
func migrateGovernance(ctx context.Context, db DB) error {
	if db == nil {
		return fmt.Errorf("smeldr: migrateGovernance: db is nil")
	}
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS smeldr_roles (
			id                  TEXT NOT NULL PRIMARY KEY,
			name                TEXT NOT NULL UNIQUE,
			operations          TEXT NOT NULL,
			scope_mode          TEXT NOT NULL DEFAULT 'global',
			scope_relation_kind TEXT,
			scope_direction     TEXT,
			trust_level         INTEGER NOT NULL DEFAULT 0,
			allow_self_approval INTEGER NOT NULL DEFAULT 0,
			created_at          DATETIME NOT NULL,
			updated_at          DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS smeldr_role_grants (
			id              TEXT NOT NULL PRIMARY KEY,
			token_id        TEXT NOT NULL REFERENCES smeldr_tokens(id),
			role_id         TEXT NOT NULL REFERENCES smeldr_roles(id),
			scope_static    TEXT NOT NULL DEFAULT '[]',
			scope_anchor_id TEXT,
			created_at      DATETIME NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_role_grants_token
			ON smeldr_role_grants(token_id)`,
		`CREATE INDEX IF NOT EXISTS idx_role_grants_role_anchor
			ON smeldr_role_grants(role_id, scope_anchor_id)`,
		`CREATE TABLE IF NOT EXISTS smeldr_tool_policies (
			id          TEXT NOT NULL PRIMARY KEY,
			tool_name   TEXT NOT NULL UNIQUE,
			required_op TEXT NOT NULL,
			created_at  DATETIME NOT NULL
		)`,
	}
	for _, s := range stmts {
		if _, err := db.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("smeldr: migrateGovernance: %w", err)
		}
	}

	if err := seedDefaultRoles(ctx, db); err != nil {
		return err
	}
	if err := seedToolPolicies(ctx, db); err != nil {
		return err
	}

	// Token migration: fail-open. smeldr_tokens may not exist in every
	// deployment (apps that never call WithTokenStore). Log and continue.
	if err := migrateTokenGrants(ctx, db); err != nil {
		slog.Warn("smeldr: migrateGovernance: token grant migration skipped", "err", err)
	}
	return nil
}

// seedDefaultRoles inserts the three built-in role templates. Idempotent via
// INSERT OR IGNORE on the UNIQUE name column.
func seedDefaultRoles(ctx context.Context, db DB) error {
	now := time.Now().UTC().Format(time.RFC3339)
	roles := []struct {
		name       string
		operations string
	}{
		{
			"author",
			`["create","read","update","publish","archive"]`,
		},
		{
			"editor",
			`["create","read","update","publish","archive","delete","manage"]`,
		},
		{
			"admin",
			`["create","read","update","publish","archive","delete","manage","administer","review","approve","define-type","define-flow","define-relation-kind"]`,
		},
	}
	for _, r := range roles {
		if _, err := db.ExecContext(ctx,
			`INSERT OR IGNORE INTO smeldr_roles
				(id, name, operations, scope_mode, trust_level, allow_self_approval, created_at, updated_at)
				VALUES (?, ?, ?, 'global', 0, 0, ?, ?)`,
			NewID(), r.name, r.operations, now, now,
		); err != nil {
			return fmt.Errorf("smeldr: seedDefaultRoles: %s: %w", r.name, err)
		}
	}
	return nil
}

// seedToolPolicies inserts one row per built-in MCP tool, mapping each tool to
// the operation word that controls access. Idempotent via INSERT OR IGNORE on
// the UNIQUE tool_name column.
//
// The required_op values mirror today's hardcoded role gates — zero behaviour
// change on day one. Operators may alter rows later to regrant access.
func seedToolPolicies(ctx context.Context, db DB) error {
	now := time.Now().UTC().Format(time.RFC3339)

	// Operation word semantics (governance-model.md §4):
	//   "read"                 — Author, Editor, Admin
	//   "create"               — Author, Editor, Admin
	//   "update"               — Author, Editor, Admin
	//   "publish"              — Author, Editor, Admin
	//   "archive"              — Author, Editor, Admin
	//   "delete"               — Editor, Admin (Author lacks delete; for CRUDAP destructive ops only)
	//   "manage"               — Editor, Admin (operational tools that are not CRUDAP verbs:
	//                            composition, transition_item, preview_impact, nav CRUD, redirect CRUD,
	//                            dynamic-content Editor tools)
	//   "administer"           — Admin only (instance-infrastructure: tokens, webhooks, page-meta)
	//   "define-type"          — Admin only (framework schema tools)
	//   "define-flow"          — Admin only (state flow definition)
	//   "define-relation-kind" — Admin only (relation kind definition)
	//
	// "approve" and "review" are reserved for the Plan governance loop (§6) — they must NOT
	// be used as generic Admin-tier gates.
	policies := []struct {
		tool string
		op   string
	}{
		// Content lifecycle — module-generated tools (per-type but listed for common built-ins)
		{"create_post", "create"},
		{"get_post", "read"},
		{"update_post", "update"},
		{"publish_post", "publish"},
		{"schedule_post", "publish"},
		{"archive_post", "archive"},
		{"delete_post", "delete"},
		{"list_posts", "read"},
		{"create_story", "create"},
		{"get_story", "read"},
		{"update_story", "update"},
		{"publish_story", "publish"},
		{"schedule_story", "publish"},
		{"archive_story", "archive"},
		{"delete_story", "delete"},
		{"list_stories", "read"},
		{"create_essay", "create"},
		{"get_essay", "read"},
		{"update_essay", "update"},
		{"publish_essay", "publish"},
		{"schedule_essay", "publish"},
		{"archive_essay", "archive"},
		{"delete_essay", "delete"},
		{"list_essays", "read"},
		{"create_doc_page", "create"},
		{"get_doc_page", "read"},
		{"update_doc_page", "update"},
		{"publish_doc_page", "publish"},
		{"schedule_doc_page", "publish"},
		{"archive_doc_page", "archive"},
		{"delete_doc_page", "delete"},
		{"list_doc_pages", "read"},
		// Node tools (Author gate for read, Author gate for write — nodes are public by default)
		{"create_node", "create"},
		{"get_node", "read"},
		{"update_node", "update"},
		{"publish_node", "publish"},
		{"archive_node", "archive"},
		{"list_nodes", "read"},
		// Dynamic content tools (gate per roleFor in mcp/rolefor.go)
		{"define_content_type", "define-type"},
		{"create_content", "manage"}, // Editor-gated operational tool
		{"get_content", "read"},
		{"list_content", "read"},
		{"update_content", "manage"},     // Editor-gated operational tool
		{"set_content_status", "manage"}, // Editor-gated operational tool
		// Schema read tools (Author+)
		{"get_content_type_schema", "read"},
		{"list_content_type_schemas", "read"},
		// Edge / composition tools (Editor gate — operational, not CRUDAP)
		{"add_section", "manage"},
		{"reorder_sections", "manage"},
		{"remove_section", "manage"},
		{"add_item", "manage"},
		{"reorder_items", "manage"},
		{"remove_item", "manage"},
		// Preview / upload (Editor and Author respectively)
		{"create_preview_url", "publish"}, // Editor gate per A92/A143
		{"create_upload_token", "create"}, // Author gate
		// Redirect tools (Editor gate — operational, not CRUDAP)
		{"create_redirect", "manage"},
		{"list_redirects", "read"},
		{"delete_redirect", "manage"},
		// Page meta tools (Admin gate — instance infrastructure)
		{"set_page_meta", "administer"},
		{"get_page_meta", "administer"},
		{"delete_page_meta", "administer"},
		{"list_page_meta", "administer"},
		// Token management (Admin gate — instance infrastructure)
		{"create_token", "administer"},
		{"list_tokens", "administer"},
		{"revoke_token", "administer"},
		// Nav tools (Editor gate for write — operational, not CRUDAP)
		{"list_nav_items", "read"},
		{"create_nav_item", "manage"},
		{"update_nav_item", "manage"},
		{"delete_nav_item", "manage"},
		// Webhook tools (Admin gate — instance infrastructure)
		{"create_webhook", "administer"},
		{"list_webhooks", "administer"},
		{"delete_webhook", "administer"},
		{"list_webhook_deliveries", "administer"},
		{"retry_webhook", "administer"},
		// State flow tools
		{"define_state_flow", "define-flow"},
		{"transition_item", "manage"}, // Editor gate — operational state change
		{"get_valid_transitions", "read"},
		{"list_items_by_state", "read"},
		// Relation tools
		{"assert_relation", "create"},
		{"propose_relation", "create"},
		{"get_relations", "read"},
		{"preview_impact", "manage"}, // Editor gate — operational read with side-effect preview
		{"upsert_relation_kind", "define-relation-kind"},
		{"list_relation_kinds", "read"},
		// Signal tools (Author+)
		{"create_signal", "create"},
		{"list_signals", "read"},
	}

	for _, p := range policies {
		if _, err := db.ExecContext(ctx,
			`INSERT OR IGNORE INTO smeldr_tool_policies (id, tool_name, required_op, created_at)
				VALUES (?, ?, ?, ?)`,
			NewID(), p.tool, p.op, now,
		); err != nil {
			return fmt.Errorf("smeldr: seedToolPolicies: %s: %w", p.tool, err)
		}
	}
	return nil
}

// migrateTokenGrants inserts a global-scope smeldr_role_grants row for every
// smeldr_tokens row whose role matches a built-in role name. Idempotent: the
// insert uses a WHERE NOT EXISTS guard because SQLite treats NULLs as distinct
// in UNIQUE constraints, making INSERT OR IGNORE unreliable for the
// (token_id, role_id, NULL) triple.
//
// Returns an error if smeldr_tokens cannot be queried. The caller
// ([migrateGovernance]) handles this as a fail-open warning.
func migrateTokenGrants(ctx context.Context, db DB) error {
	rows, err := db.QueryContext(ctx, `SELECT id, role FROM smeldr_tokens`)
	if err != nil {
		return fmt.Errorf("smeldr: migrateTokenGrants: query tokens: %w", err)
	}
	defer rows.Close()

	type tokenRow struct {
		id   string
		role string
	}
	var tokens []tokenRow
	for rows.Next() {
		var t tokenRow
		if err := rows.Scan(&t.id, &t.role); err != nil {
			return fmt.Errorf("smeldr: migrateTokenGrants: scan: %w", err)
		}
		tokens = append(tokens, t)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("smeldr: migrateTokenGrants: rows: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	for _, t := range tokens {
		var roleID string
		err := db.QueryRowContext(ctx,
			`SELECT id FROM smeldr_roles WHERE name = ?`, t.role,
		).Scan(&roleID)
		if err == sql.ErrNoRows {
			continue // unknown role — no grant
		}
		if err != nil {
			return fmt.Errorf("smeldr: migrateTokenGrants: lookup role %q: %w", t.role, err)
		}

		// WHERE NOT EXISTS guard: SQLite's UNIQUE constraint allows multiple NULLs,
		// so INSERT OR IGNORE would not prevent duplicate (token_id, role_id, NULL) rows.
		if _, err := db.ExecContext(ctx,
			`INSERT INTO smeldr_role_grants (id, token_id, role_id, scope_static, scope_anchor_id, created_at)
				SELECT ?, ?, ?, '[]', NULL, ?
				WHERE NOT EXISTS (
					SELECT 1 FROM smeldr_role_grants
					WHERE token_id = ? AND role_id = ? AND scope_anchor_id IS NULL
				)`,
			NewID(), t.id, roleID, now, t.id, roleID,
		); err != nil {
			return fmt.Errorf("smeldr: migrateTokenGrants: insert grant for token %q: %w", t.id, err)
		}
	}
	return nil
}
