// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"
)

// The approval roles before D142 (T884), and the roles they became.
var renamedApprovalRoles = []struct{ from, to string }{
	{"decision-steward", RoleApprover},
	{"decision-domain-admin", RoleDomainApprover},
}

// retiredApproveOperation is the operation "ratify" and "approve-change"
// replaced (T884). No core gate requires it any more; [migrateApprovalRoles]
// gives every role that holds it the two new operations as well.
const retiredApproveOperation = "approve"

// approvalMigration counts what [migrateApprovalRoles] changed.
type approvalMigration struct {
	rolesRenamed, grantsMoved, grantsDropped, rolesRewritten, signalsRewritten int64
}

// migrateApprovalRoles moves an instance to the approval roles of D142 (T884),
// in one transaction where the handle supports one. It is idempotent: a
// second run changes nothing.
//
//  1. decision-steward is renamed approver, decision-domain-admin
//     domain-approver; its grants follow (they key on the role's ID). When
//     the new role already exists, the old role's grants move to it, a grant
//     the new role already has is dropped, and the old role is deleted.
//     Every grant that moves keeps its ID, scope, expiry and audit trail.
//  2. Every stored role holding "approve" also holds "ratify" and
//     "approve-change", custom roles included. "approve" stays, so a custom
//     flow that requires it keeps its approvers; the core roles drop it when
//     they are redefined at boot ([RegisterApproverRole] and the others).
//  3. Open Signals addressed to "approve", or naming it as the required
//     operation, name "ratify", so the conditions keep reaching the tokens
//     that may resolve them.
//
// Each renamed role gets a governance audit record by actor "system" when the
// audit table exists.
func migrateApprovalRoles(ctx context.Context, db DB) error {
	// Table checks run on the base handle, before the transaction.
	audit, err := tableExists(ctx, db, "smeldr_governance_audit")
	if err != nil {
		return fmt.Errorf("smeldr: migrateApprovalRoles: %w", err)
	}
	signals, err := tableExists(ctx, db, "smeldr_signals")
	if err != nil {
		return fmt.Errorf("smeldr: migrateApprovalRoles: %w", err)
	}
	exec, commit, rollback, err := conflictTx(ctx, db)
	if err != nil {
		return fmt.Errorf("smeldr: migrateApprovalRoles: begin: %w", err)
	}
	defer rollback()
	var m approvalMigration
	for _, r := range renamedApprovalRoles {
		if err := renameApprovalRole(ctx, exec, r.from, r.to, audit, &m); err != nil {
			return fmt.Errorf("smeldr: migrateApprovalRoles: %s: %w", r.from, err)
		}
	}
	if err := rewriteApproveOperation(ctx, exec, &m); err != nil {
		return fmt.Errorf("smeldr: migrateApprovalRoles: %w", err)
	}
	if signals {
		if err := rewriteApproveSignals(ctx, exec, &m); err != nil {
			return fmt.Errorf("smeldr: migrateApprovalRoles: %w", err)
		}
	}
	if err := commit(); err != nil {
		return fmt.Errorf("smeldr: migrateApprovalRoles: commit: %w", err)
	}
	if m != (approvalMigration{}) {
		slog.Info("smeldr: migrateApprovalRoles: approval roles moved to D142",
			"roles_renamed", m.rolesRenamed, "grants_moved", m.grantsMoved, "grants_dropped", m.grantsDropped,
			"roles_rewritten", m.rolesRewritten, "signals_rewritten", m.signalsRewritten)
	}
	return nil
}

// roleIDByName is the ID of the role named name; found=false when there is
// none.
func roleIDByName(ctx context.Context, db DB, name string) (id string, found bool, err error) {
	err = db.QueryRowContext(ctx, `SELECT id FROM smeldr_roles WHERE name = $1`, name).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return id, err == nil, err
}

// renameApprovalRole renames the role from to, or merges it into to when
// both exist.
func renameApprovalRole(ctx context.Context, db DB, from, to string, audit bool, m *approvalMigration) error {
	oldID, found, err := roleIDByName(ctx, db, from)
	if err != nil || !found {
		return err
	}
	newID, exists, err := roleIDByName(ctx, db, to)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if !exists {
		if _, err := db.ExecContext(ctx, `UPDATE smeldr_roles SET name = $1, updated_at = $2 WHERE id = $3`, to, now, oldID); err != nil {
			return err
		}
		m.rolesRenamed++
		return auditApprovalRename(ctx, db, audit, oldID, from, to)
	}
	moved, err := db.ExecContext(ctx,
		`UPDATE smeldr_role_grants SET role_id = $1
		  WHERE role_id = $2 AND NOT EXISTS (
		    SELECT 1 FROM smeldr_role_grants g
		     WHERE g.role_id = $1 AND g.token_id = smeldr_role_grants.token_id
		       AND g.scope_anchor_id IS NOT DISTINCT FROM smeldr_role_grants.scope_anchor_id
		       AND g.scope_static = smeldr_role_grants.scope_static)`,
		newID, oldID)
	if err != nil {
		return err
	}
	n, _ := moved.RowsAffected()
	m.grantsMoved += n
	dropped, err := db.ExecContext(ctx, `DELETE FROM smeldr_role_grants WHERE role_id = $1`, oldID)
	if err != nil {
		return err
	}
	n, _ = dropped.RowsAffected()
	m.grantsDropped += n
	if _, err := db.ExecContext(ctx, `DELETE FROM smeldr_roles WHERE id = $1`, oldID); err != nil {
		return err
	}
	m.rolesRenamed++
	return auditApprovalRename(ctx, db, audit, newID, from, to)
}

// auditApprovalRename records a role rename by actor "system", when the
// governance audit table exists (audit).
func auditApprovalRename(ctx context.Context, db DB, audit bool, roleID, from, to string) error {
	if !audit {
		return nil
	}
	before, _ := json.Marshal(map[string]string{"name": from})
	after, _ := json.Marshal(map[string]string{"name": to, "reason": "D142 approval roles (T884)"})
	return appendAuditRecord(ctx, db, GovernanceAuditRecord{
		ID: NewID(), ActorTokenID: "system", Action: "rename", TargetKind: "role", TargetID: roleID,
		Before: string(before), After: string(after), CreatedAt: time.Now().UTC(),
	})
}

// addApprovalOperations returns ops with "ratify" and "approve-change" added
// right after "approve", without duplicates; changed=false when ops holds no
// "approve" or already holds both. "approve" itself stays: a custom flow of
// the instance may still require it, and the core roles that held it are
// redefined at boot without it.
func addApprovalOperations(ops []string) (out []string, changed bool) {
	if !slices.Contains(ops, retiredApproveOperation) {
		return ops, false
	}
	for _, op := range ops {
		if slices.Contains(out, op) {
			continue
		}
		out = append(out, op)
		if op != retiredApproveOperation {
			continue
		}
		for _, n := range []string{OperationRatify, OperationApproveChange} {
			if !slices.Contains(ops, n) {
				out = append(out, n)
				changed = true
			}
		}
	}
	return out, changed
}

// rewriteApproveOperation adds the approval operations to every stored role
// that holds "approve".
func rewriteApproveOperation(ctx context.Context, db DB, m *approvalMigration) error {
	rows, err := db.QueryContext(ctx, `SELECT id, operations FROM smeldr_roles`)
	if err != nil {
		return err
	}
	type role struct{ id, ops string }
	var roles []role
	for rows.Next() {
		var r role
		if err := rows.Scan(&r.id, &r.ops); err != nil {
			rows.Close()
			return err
		}
		roles = append(roles, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for _, r := range roles {
		var ops []string
		if json.Unmarshal([]byte(r.ops), &ops) != nil {
			continue // a malformed list is skipped, as Authorized skips it
		}
		out, changed := addApprovalOperations(ops)
		if !changed {
			continue
		}
		b, _ := json.Marshal(out)
		if _, err := db.ExecContext(ctx, `UPDATE smeldr_roles SET operations = $1, updated_at = $2 WHERE id = $3`, string(b), now, r.id); err != nil {
			return err
		}
		m.rolesRewritten++
	}
	return nil
}

// rewriteApproveSignals points the open Signals that name "approve" at
// "ratify".
func rewriteApproveSignals(ctx context.Context, db DB, m *approvalMigration) error {
	res, err := db.ExecContext(ctx,
		`UPDATE smeldr_signals
		    SET receiver = CASE WHEN receiver = $1 THEN $2 ELSE receiver END,
		        required_role = CASE WHEN required_role = $1 THEN $2 ELSE required_role END
		  WHERE status = 'open' AND (receiver = $1 OR required_role = $1)`,
		retiredApproveOperation, OperationRatify)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	m.signalsRewritten += n
	return nil
}

// automatedClass reports whether class is a token class the agent rule
// refuses (D142 point 5, D105): agent or job.
func automatedClass(class string) bool { return class == string(Agent) || class == string(Job) }

// actorClass is the D105 class of the actor userID: the class signed into the
// token in ctx when ctx is that actor, else the class stored on its token row
// (smeldr_tokens.actor_class). "" is unclassified, also for an actor with no
// token row or an instance without the tokens table or the column.
func actorClass(ctx context.Context, db DB, userID string) (string, error) {
	if sc, ok := ctx.(interface{ User() User }); ok {
		if u := sc.User(); u.ID == userID {
			if kind := actorKindFor(u.ID, u.Roles); kind != "unclassified" {
				return kind, nil
			}
		}
	}
	ok, err := tableExists(ctx, db, "smeldr_tokens")
	if err != nil || !ok {
		return "", err
	}
	var class sql.NullString
	err = db.QueryRowContext(ctx, `SELECT actor_class FROM smeldr_tokens WHERE user_id = $1 LIMIT 1`, userID).Scan(&class)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", nil
	case err != nil && (isNoSuchColumn(err, "actor_class") || isNoSuchColumn(err, "user_id")):
		return "", nil
	case err != nil:
		return "", err
	}
	return class.String, nil
}

// grantRole is the part of a role the agent rule reads.
type grantRole struct {
	ops          []string
	scopeMode    string
	relationKind string
}

// checkAgentGrant applies the agent rule to a grant (D142 point 5, T884), fail
// closed. For a grantee of class agent or job:
//   - a role that holds ratify is refused;
//   - a role that holds approve-change is refused unless it is scoped over
//     belongs_to_area and the grant is anchored on an existing Area;
//   - such an Area grant needs a grantor (the actor in ctx, or the store's
//     audit actor) of class human who holds approve-change over that Area.
//
// Any other grantee, and any role without an approval operation, passes.
func (s *RoleStore) checkAgentGrant(ctx context.Context, grant RoleGrant, roleID string) error {
	var r grantRole
	var opsJSON string
	var relKind sql.NullString
	if err := s.db.QueryRowContext(ctx,
		`SELECT operations, scope_mode, scope_relation_kind FROM smeldr_roles WHERE id = $1`, roleID,
	).Scan(&opsJSON, &r.scopeMode, &relKind); err != nil {
		return fmt.Errorf("%w: Grant: read role %q: %s", ErrInternal, grant.RoleName, err)
	}
	r.relationKind = relKind.String
	_ = json.Unmarshal([]byte(opsJSON), &r.ops) // malformed: no approval operation to check
	ratify, change := slices.Contains(r.ops, OperationRatify), slices.Contains(r.ops, OperationApproveChange)
	if !ratify && !change {
		return nil
	}
	class, err := actorClass(ctx, s.db, grant.TokenID)
	if err != nil {
		return fmt.Errorf("%w: Grant: the class of %s cannot be read: %s", ErrForbidden, grant.TokenID, err)
	}
	if !automatedClass(class) {
		return nil
	}
	// The refusals below name only what the caller sent (the role, the
	// anchor) and the classes involved, so they carry their reason
	// (RefusalError, T971).
	if ratify {
		return fmt.Errorf("Grant: %w", Refused(fmt.Sprintf("an %s token never holds ratify (D142): role %q holds it", class, grant.RoleName)))
	}
	if ScopeMode(r.scopeMode) != ScopeDynamic || r.relationKind != "belongs_to_area" || grant.ScopeAnchorID == "" {
		return fmt.Errorf("Grant: %w", Refused(fmt.Sprintf("an %s token holds approve-change only within one Area: role %q is not an Area role anchored on an Area (use %s)", class, grant.RoleName, RoleAreaApprover)))
	}
	grantor, _ := actorFromContext(ctx)
	if grantor == "" {
		grantor = s.actorTokenID
	}
	if grantor == "" {
		return fmt.Errorf("Grant: %w", Refused(fmt.Sprintf("an %s token's %s grant needs a human grantor, and none is known", class, grant.RoleName)))
	}
	grantorClass, err := actorClass(ctx, s.db, grantor)
	if err != nil {
		return fmt.Errorf("%w: Grant: the class of the grantor %s cannot be read: %s", ErrForbidden, grantor, err)
	}
	if grantorClass != string(Human) {
		return fmt.Errorf("Grant: %w", Refused(fmt.Sprintf("an %s token's %s grant must come from a human token; the grantor is %s", class, grant.RoleName, classLabel(grantorClass))))
	}
	// A missing Area and an Area the grantor holds no approve-change over
	// are refused with one text, so the refusal is no existence oracle
	// (T971): the grantor learns only that it cannot delegate this anchor.
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM smeldr_areas WHERE id = $1`, grant.ScopeAnchorID).Scan(&n); err != nil {
		return fmt.Errorf("%w: Grant: the anchor of an %s token's %s grant cannot be read: %s", ErrForbidden, class, grant.RoleName, err)
	}
	ok := false
	if n > 0 {
		if ok, err = s.Authorized(ctx, grantor, OperationApproveChange, AuthTarget{TypeName: "Area", ID: grant.ScopeAnchorID}); err != nil {
			return fmt.Errorf("%w: Grant: the grantor's authority over the Area cannot be read: %s", ErrForbidden, err)
		}
	}
	if !ok {
		return fmt.Errorf("Grant: %w", Refused(fmt.Sprintf("the grantor holds no approve-change over Area %s, so cannot delegate it to an %s token", grant.ScopeAnchorID, class)))
	}
	return nil
}

// classLabel names a class in a message.
func classLabel(class string) string {
	if class == "" {
		return "unclassified"
	}
	return class
}

// refuseAutomatedRatify refuses a transition that requires ratify when the
// actor in ctx is an agent or job token (D142 point 5): whatever its grants
// say, it never ratifies.
func refuseAutomatedRatify(ctx context.Context, requiredOperation string) error {
	if requiredOperation != OperationRatify {
		return nil
	}
	if _, kind := actorFromContext(ctx); automatedClass(kind) {
		return Refused(fmt.Sprintf("an %s token never ratifies, supersedes or amends a Decision (D142); a human approver does", kind))
	}
	return nil
}

// founderReason is the reason recorded on the founder's grants.
const founderReason = "founder of the organization (D142)"

// GrantFounder gives userID, the person who created the organization, the
// admin and approver roles (D142 point 6), each grant recorded with the reason
// "founder of the organization (D142)" in its provenance, by the actor in ctx.
// It defines the approver role when the instance has not yet. Idempotent: a
// grant the user already holds is not repeated. Call it once the Organization exists
// ([App.EnsureOrganization]). Needs [App.Governance]; an empty userID is a
// [*ValidationError].
func (a *App) GrantFounder(ctx context.Context, userID string) error {
	if userID == "" {
		return Err("user_id", "is required: the founder's user ID")
	}
	rs := a.governance
	if rs == nil {
		return fmt.Errorf("%w: GrantFounder needs App.Governance", ErrInternal)
	}
	if err := RegisterApproverRole(ctx, rs); err != nil {
		return err
	}
	for _, role := range []string{string(Admin), RoleApprover} {
		if _, err := rs.Grant(ctx, RoleGrant{TokenID: userID, RoleName: role, Reason: founderReason}); err != nil {
			return fmt.Errorf("smeldr: GrantFounder: %s: %w", role, err)
		}
	}
	return nil
}
