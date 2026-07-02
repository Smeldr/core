package smeldr

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"
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
	// ScopeStatic restricts the grant to an explicit list of "type:id" patterns.
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

// RoleDefinition describes a named set of operations and scope shape that may be
// granted to a token via [RoleStore.Grant]. Roles are upserted by name via
// [RoleStore.DefineRole]; the three default roles (author/editor/admin) are
// seeded by [migrateGovernance] and may be redefined by operators.
type RoleDefinition struct {
	// Name is the unique role identifier, e.g. "author", "reviewer", "ai-agent".
	// Required — DefineRole returns an error when empty.
	Name string
	// Operations is the list of full-word operation words this role permits,
	// e.g. ["create", "read", "manage"]. Stored as a JSON array in smeldr_roles.
	Operations []string
	// ScopeMode controls how grants of this role are scoped. Defaults to
	// [ScopeGlobal] when the zero value is passed.
	ScopeMode ScopeMode
	// ScopeRelationKind is the relation kind used for one-hop scope resolution
	// when ScopeMode is [ScopeDynamic]. Empty for global/static modes.
	ScopeRelationKind string
	// ScopeDirection is "outgoing", "incoming", or "both" when ScopeMode is
	// [ScopeDynamic]. Empty for global/static modes.
	ScopeDirection string
	// TrustLevel is 0 (direct execute), 1 (auto-verify), or 2 (plan required).
	// Default 0.
	TrustLevel int
	// AllowSelfApproval permits the grant's proposer to also review/approve their
	// own Plans. Only meaningful when TrustLevel is 2. Default false.
	AllowSelfApproval bool
}

// RoleGrant records a token's binding to a role with concrete scope data.
// The ID field is empty when passed to [RoleStore.Grant]; it is populated when
// returned by [RoleStore.ListGrants].
type RoleGrant struct {
	// ID is the grant's primary key. Empty on input to Grant.
	ID string
	// TokenID is the token this grant is bound to. Required.
	TokenID string
	// RoleName is the role's unique name. Resolved to role_id internally. Required.
	RoleName string
	// ScopeStatic is the list of "type:id" or "type:*" patterns for static scope.
	// Matched against target.TypeName+":"+target.ID. Only used when the role's
	// ScopeMode is [ScopeStatic].
	ScopeStatic []string
	// ScopeAnchorID is the anchor item's ID for dynamic scope.
	// Only used when the role's ScopeMode is [ScopeDynamic].
	ScopeAnchorID string
	// CreatedAt is the RFC3339 creation timestamp. Populated by ListGrants.
	CreatedAt string
}

// AuthTarget identifies the content item being acted on in an [RoleStore.Authorized]
// check. TypeName and ID are used for static-scope pattern matching ("type:id" or
// "type:*"); ID is also used for dynamic-scope relation queries. All fields are
// optional — pass zero values for operations that have no specific target (e.g.
// "administer"). Slug is retained on the struct for display and logging only — it
// is not used in any authorization comparison (slugs are not stable identity).
type AuthTarget struct {
	// TypeName is the content type name, e.g. "post", "essay".
	TypeName string
	// Slug is for display and logging only. Not used in authorization comparisons.
	Slug string
	// ID is the item's primary key. Used for static-scope pattern matching
	// ("type:id" / "type:*") and for dynamic-scope relation queries.
	ID string
}

// GovernanceAuditRecord captures a single governance mutation — a role
// definition or grant change — with the actor, before/after state, and
// timestamp. Produced by [RoleStore.WithAudit] and written via
// [GovernanceAuditStore.Append].
type GovernanceAuditRecord struct {
	// ID is a UUID uniquely identifying this audit record.
	ID string
	// ActorTokenID is the token that initiated the mutation.
	ActorTokenID string
	// Action is the mutation kind: "define_role", "grant", or "revoke".
	Action string
	// TargetKind is the kind of object affected: "role" or "grant".
	TargetKind string
	// TargetID is the role ID or grant ID that was affected.
	TargetID string
	// Before is a JSON object representing the state before the mutation;
	// "{}" when the object did not previously exist.
	Before string
	// After is a JSON object representing the state after the mutation;
	// "{}" when the object was deleted.
	After string
	// CreatedAt is when this record was written.
	CreatedAt time.Time
}

// GovernanceAuditStore receives governance mutation audit records. The
// standard SQL-backed implementation is returned by [NewGovernanceAuditStore]
// after calling [CreateGovernanceAuditTable]. Applications may supply their
// own implementation (e.g. writing to a remote log service).
type GovernanceAuditStore interface {
	Append(ctx context.Context, r GovernanceAuditRecord) error
}

type sqlGovernanceAuditStore struct {
	db DB
}

// CreateGovernanceAuditTable creates the smeldr_governance_audit table and
// its actor index in db. The function is idempotent — safe to call on every
// startup. Call it before [NewGovernanceAuditStore].
func CreateGovernanceAuditTable(db DB) error {
	if _, err := db.ExecContext(context.Background(),
		`CREATE TABLE IF NOT EXISTS smeldr_governance_audit (
			id             TEXT NOT NULL PRIMARY KEY,
			actor_token_id TEXT NOT NULL,
			action         TEXT NOT NULL,
			target_kind    TEXT NOT NULL,
			target_id      TEXT NOT NULL,
			before_json    TEXT NOT NULL DEFAULT '{}',
			after_json     TEXT NOT NULL DEFAULT '{}',
			created_at     DATETIME NOT NULL
		)`,
	); err != nil {
		return fmt.Errorf("smeldr: CreateGovernanceAuditTable: %w", err)
	}
	if _, err := db.ExecContext(context.Background(),
		`CREATE INDEX IF NOT EXISTS idx_governance_audit_actor
			ON smeldr_governance_audit(actor_token_id)`,
	); err != nil {
		return fmt.Errorf("smeldr: CreateGovernanceAuditTable: index: %w", err)
	}
	return nil
}

// NewGovernanceAuditStore returns a [GovernanceAuditStore] backed by db.
// Call [CreateGovernanceAuditTable] before using the returned store.
func NewGovernanceAuditStore(db DB) GovernanceAuditStore {
	return &sqlGovernanceAuditStore{db: db}
}

func (s *sqlGovernanceAuditStore) Append(ctx context.Context, r GovernanceAuditRecord) error {
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO smeldr_governance_audit
			(id, actor_token_id, action, target_kind, target_id, before_json, after_json, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.ActorTokenID, r.Action, r.TargetKind, r.TargetID, r.Before, r.After,
		r.CreatedAt.UTC().Format(time.RFC3339),
	); err != nil {
		return fmt.Errorf("smeldr: GovernanceAuditStore.Append: %w", err)
	}
	return nil
}

// RoleStore provides role management and authorization checks backed by the
// smeldr_roles, smeldr_role_grants, and smeldr_tool_policies tables from
// [migrateGovernance]. Obtain one via [NewRoleStore] and wire it into the
// application with [App.Governance].
type RoleStore struct {
	db           DB
	actorTokenID string               // "" unless WithAudit called
	auditStore   GovernanceAuditStore // nil unless WithAudit called
}

// NewRoleStore returns a new [RoleStore] backed by db.
func NewRoleStore(db DB) *RoleStore {
	return &RoleStore{db: db}
}

// WithAudit returns a shallow copy of s configured to record every governance
// mutation (DefineRole, Grant, Revoke) to log, attributed to actorTokenID.
//
// When audit is wired, each mutation method reads the previous state, performs
// the mutation, then calls log.Append with a [GovernanceAuditRecord]. If
// Append returns an error, the mutation method surfaces that error — but the
// underlying DB operations are not transactional: the mutation (INSERT, UPDATE,
// or DELETE) may have already taken effect before Append was called. An error
// return means "the mutation may have already taken effect; the audit record
// failed to write — verify current state before assuming the operation was
// rolled back." Callers can safely retry DefineRole and Grant (both are
// idempotent) and Revoke (idempotent by nature).
//
// Call sites that do not use WithAudit receive a store with nil auditStore
// and see zero behaviour change.
func (s *RoleStore) WithAudit(actorTokenID string, log GovernanceAuditStore) *RoleStore {
	cp := *s
	cp.actorTokenID = actorTokenID
	cp.auditStore = log
	return &cp
}

// DefineRole creates or updates a role template by name. If a role with the
// given name already exists its mutable fields (operations, scope, trust level,
// and self-approval flag) are updated; the row's ID and created_at remain
// unchanged. Idempotent for concurrent calls on distinct names.
//
// Returns an error when role.Name is empty or any DB statement fails.
func (s *RoleStore) DefineRole(ctx context.Context, role RoleDefinition) error {
	if role.Name == "" {
		return fmt.Errorf("smeldr: DefineRole: role.Name must not be empty")
	}
	if role.TrustLevel == 1 {
		return fmt.Errorf("smeldr: DefineRole: trust_level 1 is not yet defined — use 0 (direct) or 2 (plan required)")
	}
	ops := role.Operations
	if ops == nil {
		ops = []string{}
	}
	opsJSON, _ := json.Marshal(ops) // []string marshal never fails
	scopeMode := role.ScopeMode
	if scopeMode == "" {
		scopeMode = ScopeGlobal
	}
	selfApproval := 0
	if role.AllowSelfApproval {
		selfApproval = 1
	}
	now := time.Now().UTC().Format(time.RFC3339)

	var (
		beforeJSON = "{}"
		targetID   string
	)
	if s.auditStore != nil {
		var (
			existingID           string
			existingName         string
			existingOps          string
			existingScope        string
			existingTrust        int
			existingSelfApproval int
		)
		err := s.db.QueryRowContext(ctx,
			`SELECT id, name, operations, scope_mode, trust_level, allow_self_approval
			   FROM smeldr_roles WHERE name = ?`, role.Name,
		).Scan(&existingID, &existingName, &existingOps, &existingScope, &existingTrust, &existingSelfApproval)
		if err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("smeldr: DefineRole: audit before-state: %w", err)
		}
		if err == nil {
			b, _ := json.Marshal(map[string]any{
				"id": existingID, "name": existingName,
				"operations":          json.RawMessage([]byte(existingOps)),
				"scope_mode":          existingScope,
				"trust_level":         existingTrust,
				"allow_self_approval": existingSelfApproval,
			})
			beforeJSON = string(b)
			targetID = existingID
		}
	}

	if _, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO smeldr_roles
			(id, name, operations, scope_mode, scope_relation_kind, scope_direction,
			 trust_level, allow_self_approval, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		NewID(), role.Name, string(opsJSON), string(scopeMode),
		role.ScopeRelationKind, role.ScopeDirection,
		role.TrustLevel, selfApproval, now, now,
	); err != nil {
		return fmt.Errorf("smeldr: DefineRole: insert %q: %w", role.Name, err)
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE smeldr_roles
			SET operations=?, scope_mode=?, scope_relation_kind=?, scope_direction=?,
			    trust_level=?, allow_self_approval=?, updated_at=?
			WHERE name=?`,
		string(opsJSON), string(scopeMode), role.ScopeRelationKind, role.ScopeDirection,
		role.TrustLevel, selfApproval, now, role.Name,
	); err != nil {
		return fmt.Errorf("smeldr: DefineRole: update %q: %w", role.Name, err)
	}
	if s.auditStore != nil {
		if targetID == "" {
			if err := s.db.QueryRowContext(ctx,
				`SELECT id FROM smeldr_roles WHERE name = ?`, role.Name,
			).Scan(&targetID); err != nil {
				return fmt.Errorf("smeldr: DefineRole: audit resolve id: %w", err)
			}
		}
		afterJSON, _ := json.Marshal(map[string]any{
			"id": targetID, "name": role.Name,
			"operations":          json.RawMessage([]byte(string(opsJSON))),
			"scope_mode":          string(scopeMode),
			"trust_level":         role.TrustLevel,
			"allow_self_approval": selfApproval,
		})
		rec := GovernanceAuditRecord{
			ID:           NewID(),
			ActorTokenID: s.actorTokenID,
			Action:       "define_role",
			TargetKind:   "role",
			TargetID:     targetID,
			Before:       beforeJSON,
			After:        string(afterJSON),
			CreatedAt:    time.Now().UTC(),
		}
		if err := s.auditStore.Append(ctx, rec); err != nil {
			return fmt.Errorf("smeldr: DefineRole: audit: %w", err)
		}
	}
	return nil
}

// Grant binds a token to a role with concrete scope data and returns the grant ID.
// If an identical grant already exists (same token, role, and anchor) the existing
// grant ID is returned without inserting a duplicate. The WHERE NOT EXISTS guard
// is required because SQLite allows multiple NULL values in a UNIQUE constraint,
// making INSERT OR IGNORE unreliable for global-scope (null anchor) grants.
//
// Returns an error when grant.TokenID or grant.RoleName is empty, the named role
// does not exist, or any DB operation fails.
func (s *RoleStore) Grant(ctx context.Context, grant RoleGrant) (string, error) {
	if grant.TokenID == "" {
		return "", fmt.Errorf("smeldr: Grant: TokenID must not be empty")
	}
	if grant.RoleName == "" {
		return "", fmt.Errorf("smeldr: Grant: RoleName must not be empty")
	}
	var roleID string
	if err := s.db.QueryRowContext(ctx,
		`SELECT id FROM smeldr_roles WHERE name = ?`, grant.RoleName,
	).Scan(&roleID); err == sql.ErrNoRows {
		return "", fmt.Errorf("smeldr: Grant: role %q: %w", grant.RoleName, ErrNotFound)
	} else if err != nil {
		return "", fmt.Errorf("smeldr: Grant: lookup role %q: %w", grant.RoleName, err)
	}

	scopeStatic := grant.ScopeStatic
	if scopeStatic == nil {
		scopeStatic = []string{}
	}
	staticJSON, _ := json.Marshal(scopeStatic) // []string marshal never fails
	var anchorID *string
	if grant.ScopeAnchorID != "" {
		anchorID = &grant.ScopeAnchorID
	}
	newID := NewID()
	now := time.Now().UTC().Format(time.RFC3339)

	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO smeldr_role_grants (id, token_id, role_id, scope_static, scope_anchor_id, created_at)
			SELECT ?, ?, ?, ?, ?, ?
			WHERE NOT EXISTS (
				SELECT 1 FROM smeldr_role_grants
				WHERE token_id = ? AND role_id = ?
				  AND (scope_anchor_id IS ? OR (scope_anchor_id IS NULL AND ? IS NULL))
			)`,
		newID, grant.TokenID, roleID, string(staticJSON), anchorID, now,
		grant.TokenID, roleID, anchorID, anchorID,
	); err != nil {
		return "", fmt.Errorf("smeldr: Grant: insert: %w", err)
	}

	// Re-query to find the canonical grant ID (new or pre-existing).
	var grantID string
	if anchorID != nil {
		if err := s.db.QueryRowContext(ctx,
			`SELECT id FROM smeldr_role_grants WHERE token_id=? AND role_id=? AND scope_anchor_id=?`,
			grant.TokenID, roleID, *anchorID,
		).Scan(&grantID); err != nil {
			return "", fmt.Errorf("smeldr: Grant: resolve grant id: %w", err)
		}
	} else {
		if err := s.db.QueryRowContext(ctx,
			`SELECT id FROM smeldr_role_grants WHERE token_id=? AND role_id=? AND scope_anchor_id IS NULL`,
			grant.TokenID, roleID,
		).Scan(&grantID); err != nil {
			return "", fmt.Errorf("smeldr: Grant: resolve grant id: %w", err)
		}
	}
	if s.auditStore != nil {
		var anchorVal any
		if anchorID != nil {
			anchorVal = *anchorID
		}
		afterJSON, _ := json.Marshal(map[string]any{
			"id":              grantID,
			"token_id":        grant.TokenID,
			"role_id":         roleID,
			"scope_static":    json.RawMessage([]byte(string(staticJSON))),
			"scope_anchor_id": anchorVal,
		})
		rec := GovernanceAuditRecord{
			ID:           NewID(),
			ActorTokenID: s.actorTokenID,
			Action:       "grant",
			TargetKind:   "grant",
			TargetID:     grantID,
			Before:       "{}",
			After:        string(afterJSON),
			CreatedAt:    time.Now().UTC(),
		}
		if err := s.auditStore.Append(ctx, rec); err != nil {
			return "", fmt.Errorf("smeldr: Grant: audit: %w", err)
		}
	}
	return grantID, nil
}

// Revoke removes the grant identified by grantID. It is not an error if the
// grant does not exist — the call is idempotent with respect to absence.
// Returns an error when grantID is empty or the DELETE fails.
func (s *RoleStore) Revoke(ctx context.Context, grantID string) error {
	if grantID == "" {
		return fmt.Errorf("smeldr: Revoke: grantID must not be empty")
	}
	beforeJSON := "{}"
	if s.auditStore != nil {
		var (
			eid     string
			etok    string
			erol    string
			eanch   sql.NullString
			estatic string
		)
		err := s.db.QueryRowContext(ctx,
			`SELECT id, token_id, role_id, scope_anchor_id, scope_static
			   FROM smeldr_role_grants WHERE id = ?`, grantID,
		).Scan(&eid, &etok, &erol, &eanch, &estatic)
		if err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("smeldr: Revoke: audit before-state: %w", err)
		}
		if err == nil {
			var anchorVal any
			if eanch.Valid {
				anchorVal = eanch.String
			}
			b, _ := json.Marshal(map[string]any{
				"id":              eid,
				"token_id":        etok,
				"role_id":         erol,
				"scope_anchor_id": anchorVal,
				"scope_static":    json.RawMessage([]byte(estatic)),
			})
			beforeJSON = string(b)
		}
	}
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM smeldr_role_grants WHERE id = ?`, grantID,
	); err != nil {
		return fmt.Errorf("smeldr: Revoke: %w", err)
	}
	if s.auditStore != nil {
		rec := GovernanceAuditRecord{
			ID:           NewID(),
			ActorTokenID: s.actorTokenID,
			Action:       "revoke",
			TargetKind:   "grant",
			TargetID:     grantID,
			Before:       beforeJSON,
			After:        "{}",
			CreatedAt:    time.Now().UTC(),
		}
		if err := s.auditStore.Append(ctx, rec); err != nil {
			return fmt.Errorf("smeldr: Revoke: audit: %w", err)
		}
	}
	return nil
}

// ListGrants returns the grants bound to the given token. If tokenID is empty,
// all grants in the store are returned. Each [RoleGrant] in the result has its
// ID, TokenID, RoleName, ScopeStatic, ScopeAnchorID, and CreatedAt populated.
func (s *RoleStore) ListGrants(ctx context.Context, tokenID string) ([]RoleGrant, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if tokenID != "" {
		rows, err = s.db.QueryContext(ctx,
			`SELECT g.id, r.name, g.token_id, g.scope_static, g.scope_anchor_id, g.created_at
				FROM smeldr_role_grants g
				JOIN smeldr_roles r ON r.id = g.role_id
				WHERE g.token_id = ?`, tokenID)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT g.id, r.name, g.token_id, g.scope_static, g.scope_anchor_id, g.created_at
				FROM smeldr_role_grants g
				JOIN smeldr_roles r ON r.id = g.role_id`)
	}
	if err != nil {
		return nil, fmt.Errorf("smeldr: ListGrants: query: %w", err)
	}
	defer rows.Close()

	var out []RoleGrant
	for rows.Next() {
		var g RoleGrant
		var staticJSON string
		var anchorID sql.NullString
		if err := rows.Scan(&g.ID, &g.RoleName, &g.TokenID, &staticJSON, &anchorID, &g.CreatedAt); err != nil {
			return nil, fmt.Errorf("smeldr: ListGrants: scan: %w", err)
		}
		if err := json.Unmarshal([]byte(staticJSON), &g.ScopeStatic); err != nil {
			return nil, fmt.Errorf("smeldr: ListGrants: unmarshal scope_static: %w", err)
		}
		if anchorID.Valid {
			g.ScopeAnchorID = anchorID.String
		}
		out = append(out, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("smeldr: ListGrants: rows: %w", err)
	}
	return out, nil
}

// authorizedGrant is the per-row data scanned from the grants+roles join.
type authorizedGrant struct {
	staticJSON string
	anchorID   sql.NullString
	opsJSON    string
	scopeMode  string
	relKind    sql.NullString
	relDir     sql.NullString
}

// Authorized reports whether the token identified by tokenID holds a grant that
// permits the given operation on target. It checks all three scope modes:
//
//   - [ScopeGlobal]: always matches when the operation is in the role's list.
//   - [ScopeStatic]: matches when target.TypeName+":"+target.ID matches a
//     pattern in the grant's scope_static list ("type:*" is a wildcard).
//   - [ScopeDynamic]: matches when target.ID is one hop from the grant's
//     scope_anchor_id via the role's relation kind and direction. Only asserted,
//     non-invalidated edges are considered.
//
// For operations with no specific target (e.g. "administer"), pass a zero
// AuthTarget — only global-scope grants can authorize those.
//
// A transient dynamic-scope query error does not abort the check: remaining
// grants continue to be evaluated, and the error is surfaced only if no other
// grant authorizes the request.
func (s *RoleStore) Authorized(ctx context.Context, tokenID, op string, target AuthTarget) (bool, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT g.scope_static, g.scope_anchor_id, r.operations,
		        r.scope_mode, r.scope_relation_kind, r.scope_direction
		   FROM smeldr_role_grants g
		   JOIN smeldr_roles r ON r.id = g.role_id
		  WHERE g.token_id = ?`, tokenID)
	if err != nil {
		return false, fmt.Errorf("smeldr: Authorized: query grants: %w", err)
	}

	// Collect all grant rows before closing — SQLite allows only one active
	// statement per connection, so relationExists must not be called while rows
	// is still open.
	var grants []authorizedGrant
	for rows.Next() {
		var g authorizedGrant
		if err := rows.Scan(&g.staticJSON, &g.anchorID, &g.opsJSON, &g.scopeMode, &g.relKind, &g.relDir); err != nil {
			rows.Close()
			return false, fmt.Errorf("smeldr: Authorized: scan: %w", err)
		}
		grants = append(grants, g)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return false, fmt.Errorf("smeldr: Authorized: rows: %w", err)
	}
	rows.Close()

	var pendingErr error
	for _, g := range grants {
		var ops []string
		if err := json.Unmarshal([]byte(g.opsJSON), &ops); err != nil {
			continue // malformed operations — skip, do not block
		}
		if !slices.Contains(ops, op) {
			continue
		}

		switch ScopeMode(g.scopeMode) {
		case ScopeGlobal:
			return true, nil

		case ScopeStatic:
			var patterns []string
			if err := json.Unmarshal([]byte(g.staticJSON), &patterns); err != nil {
				continue // malformed static list — skip
			}
			key := target.TypeName + ":" + target.ID
			for _, p := range patterns {
				if p == key {
					return true, nil
				}
				// "type:*" wildcard — matches any id of that type
				if strings.HasSuffix(p, ":*") {
					prefix := strings.TrimSuffix(p, ":*")
					if prefix == target.TypeName {
						return true, nil
					}
				}
			}

		case ScopeDynamic:
			if !g.anchorID.Valid || g.anchorID.String == "" || target.ID == "" {
				continue // can't resolve without both ends
			}
			matched, err := s.relationExists(ctx, g.relKind.String, g.relDir.String, target.ID, g.anchorID.String)
			if err != nil {
				pendingErr = err
				continue // don't abort — check remaining grants
			}
			if matched {
				return true, nil
			}
		}
	}
	if pendingErr != nil {
		return false, fmt.Errorf("smeldr: Authorized: dynamic scope: %w", pendingErr)
	}
	return false, nil
}

// relationExists checks whether an active, asserted one-hop relation exists
// between itemID and anchorID via the given relation kind and direction.
//
//   - "incoming": itemID has a relation pointing TO anchorID (item is source, anchor is target)
//   - "outgoing": anchorID points TO itemID (anchor is source, item is target)
//   - "both": either direction
//
// Only asserted edges (edge_class='asserted') that have not been invalidated
// (invalid_at IS NULL OR invalid_at > now) are considered — reusing the same
// "active edge" predicate applied by [RelationStore.SweepStructural].
func (s *RoleStore) relationExists(ctx context.Context, kind, direction, itemID, anchorID string) (bool, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	checkIncoming := direction == "incoming" || direction == "both"
	checkOutgoing := direction == "outgoing" || direction == "both"

	if checkIncoming {
		var n int
		if err := s.db.QueryRowContext(ctx,
			`SELECT 1 FROM smeldr_relations
				WHERE source_id=? AND target_id=? AND relation_kind=?
				  AND edge_class='asserted'
				  AND (invalid_at IS NULL OR invalid_at > ?)
				LIMIT 1`,
			itemID, anchorID, kind, now,
		).Scan(&n); err == nil {
			return true, nil
		} else if err != sql.ErrNoRows {
			return false, fmt.Errorf("smeldr: relationExists incoming: %w", err)
		}
	}
	if checkOutgoing {
		var n int
		if err := s.db.QueryRowContext(ctx,
			`SELECT 1 FROM smeldr_relations
				WHERE source_id=? AND target_id=? AND relation_kind=?
				  AND edge_class='asserted'
				  AND (invalid_at IS NULL OR invalid_at > ?)
				LIMIT 1`,
			anchorID, itemID, kind, now,
		).Scan(&n); err == nil {
			return true, nil
		} else if err != sql.ErrNoRows {
			return false, fmt.Errorf("smeldr: relationExists outgoing: %w", err)
		}
	}
	return false, nil
}

// Governance wires store as the role-based authorization store for this
// application. It calls [migrateGovernance] to ensure the three governance
// tables exist, then stores the [RoleStore] for later retrieval via
// [App.RoleStore]. Calling Governance multiple times replaces the store
// (last write wins, consistent with [App.Relations] and [App.Audit]).
//
// An app that never calls Governance sees zero behaviour change — no tables are
// touched and no authorization checks are performed.
func (a *App) Governance(store *RoleStore) error {
	if store == nil {
		return fmt.Errorf("smeldr: Governance: store must not be nil")
	}
	if store.db != a.cfg.DB {
		return fmt.Errorf("smeldr: Governance: store.db does not match the app's DB — pass the same DB instance to NewRoleStore and Config.DB")
	}
	if err := migrateGovernance(context.Background(), a.cfg.DB); err != nil {
		return fmt.Errorf("smeldr: Governance: %w", err)
	}
	a.governance = store
	return nil
}

// RoleStore returns the [RoleStore] wired via [App.Governance], or nil if
// [App.Governance] has not been called.
func (a *App) RoleStore() *RoleStore {
	return a.governance
}
