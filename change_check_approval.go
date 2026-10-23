// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Approval per Area (D144, T918 part 2): each touched Area that requires
// approval gets its own request, placed in that Area and addressed to
// approve-change; an answer counts when the answerer holds approve-change
// over that Area (its area-approver, its domain-approver, an approver: the
// away case) or the one-item role for that Check. The Check is approved when
// every such Area approved it, and not approved on the first "not approved".

// RoleItemChangeApprover is the static role that approves one ChangeCheck for
// every Area it touches (Peter's yes 2026-10-19 point 4, S3058): operation
// approve-change (and read) on the one Check its grant names. A human who
// holds approve-change over that Check delegates it, to an agent too, with
// delegate_item.
const RoleItemChangeApprover = "item-change-approver"

// RegisterItemChangeApproverRole defines (or updates) the
// [RoleItemChangeApprover] role: operations approve-change and read, static
// scope, so a grant authorizes only the Check its ScopeStatic names.
// Idempotent, safe to call on every boot.
func RegisterItemChangeApproverRole(ctx context.Context, rs *RoleStore) error {
	return rs.DefineRole(ctx, RoleDefinition{
		Name:       RoleItemChangeApprover,
		Operations: []string{OperationApproveChange, "read"},
		ScopeMode:  ScopeStatic,
	})
}

// The verdicts an answer sets on an Area of a Check.
const (
	// AreaVerdictApproved: the Area's approver approved the change.
	AreaVerdictApproved = "approved"
	// AreaVerdictNotApproved: the Area's approver did not approve it.
	AreaVerdictNotApproved = "not-approved"
)

// changeCheckSignalType is the request each Area that requires approval gets.
const changeCheckSignalType = "review-requested"

// errRequestTaken rolls back a request another call opened first.
var errRequestTaken = errors.New("smeldr: the Area's request is already open")

// checkItemChangeApproverGrant applies the rule of the one-item role: its
// grant names exactly one ChangeCheck, and its grantor is a human who holds
// approve-change over every Area of that Check that requires approval. An agent grantee is then allowed
// (the agent rule's exception for this role).
func (s *RoleStore) checkItemChangeApproverGrant(ctx context.Context, grant RoleGrant) error {
	if len(grant.ScopeStatic) != 1 || !strings.HasPrefix(grant.ScopeStatic[0], "ChangeCheck:") {
		return fmt.Errorf("%w: Grant: %s names exactly one ChangeCheck (ChangeCheck:<id>)", ErrForbidden, RoleItemChangeApprover)
	}
	// The scope is normalized before this check, which refuses a pattern
	// naming no ChangeCheck record.
	checkID := strings.TrimPrefix(grant.ScopeStatic[0], "ChangeCheck:")
	grantor, _ := actorFromContext(ctx)
	if grantor == "" {
		grantor = s.actorTokenID
	}
	if grantor == "" {
		return fmt.Errorf("%w: Grant: %s needs a human grantor, and none is known", ErrForbidden, RoleItemChangeApprover)
	}
	class, err := actorClass(ctx, s.db, grantor)
	if err != nil {
		return fmt.Errorf("%w: Grant: the class of the grantor %s cannot be read: %s", ErrForbidden, grantor, err)
	}
	if class != string(Human) {
		return fmt.Errorf("%w: Grant: %s must come from a human token; the grantor is %s", ErrForbidden, RoleItemChangeApprover, classLabel(class))
	}
	// The role counts for every Area of the Check that requires approval, so
	// its grantor must hold approve-change over each of them (S3154): an
	// approver of one Area cannot hand out another's.
	rows, err := s.db.QueryContext(ctx, `SELECT c.area_id, a.address FROM smeldr_change_check_areas c JOIN smeldr_areas a ON a.id = c.area_id
		WHERE c.check_id = $1 AND c.approval_mode = $2`, checkID, ApprovalModeRequiresApproval)
	if err != nil {
		return fmt.Errorf("%w: Grant: the Areas of the Check cannot be read: %s", ErrForbidden, err)
	}
	type area struct{ id, address string }
	var areas []area
	for rows.Next() {
		var x area
		if err := rows.Scan(&x.id, &x.address); err != nil {
			rows.Close()
			return fmt.Errorf("%w: Grant: the Areas of the Check cannot be read: %s", ErrForbidden, err)
		}
		areas = append(areas, x)
	}
	rows.Close()
	if len(areas) == 0 {
		return fmt.Errorf("%w: Grant: the Check has no Area that requires approval; there is nothing to delegate", ErrForbidden)
	}
	for _, x := range areas {
		ok, err := s.authorizedOver(ctx, grantor, OperationApproveChange, AuthTarget{TypeName: "Area", ID: x.id}, true)
		if err != nil {
			return fmt.Errorf("%w: Grant: the grantor's authority over Area %s cannot be read: %s", ErrForbidden, x.address, err)
		}
		if !ok {
			return fmt.Errorf("%w: Grant: the grantor holds no approve-change over Area %s of the Check, so cannot delegate approval of it", ErrForbidden, x.address)
		}
	}
	return nil
}

// requestChangeCheckApprovals opens one request per touched Area that
// requires approval and has none yet: receiver approve-change, subject the
// Check, placed in that Area only (its message names that Area alone,
// condition 5).
func (a *App) requestChangeCheckApprovals(ctx context.Context, c *ChangeCheck) error {
	db := a.cfg.DB
	rows, err := db.QueryContext(ctx,
		`SELECT c.area_id, a.address FROM smeldr_change_check_areas c JOIN smeldr_areas a ON a.id = c.area_id
		  WHERE c.check_id = $1 AND c.approval_mode = $2 AND c.signal_id = ''`, c.ID, ApprovalModeRequiresApproval)
	if err != nil {
		return fmt.Errorf("%w: the Areas of %s: %v", ErrInternal, c.Address, err)
	}
	type pending struct{ id, address string }
	var areas []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.id, &p.address); err != nil {
			rows.Close()
			return fmt.Errorf("%w: the Areas of %s: %v", ErrInternal, c.Address, err)
		}
		areas = append(areas, p)
	}
	rows.Close()
	resolver := a.signalResolver()
	job := "change-check"
	for _, area := range areas {
		s := &Signal{
			Sender: "system", Receiver: OperationApproveChange, SignalType: changeCheckSignalType,
			Message: fmt.Sprintf("Check %s of %s#%d at %s needs approval for Area %s: answer it with answer_change_check.",
				c.Address, c.Repository, c.Number, c.HeadCommit[:12], area.address),
			SubjectType: "ChangeCheck", SubjectID: c.ID,
		}
		// The request, its place and its link from the Area row are one
		// write; it is announced only after the commit, so no reader sees it
		// unplaced and a failure leaves nothing to open twice.
		var edge RelationEdge
		err := runInTx(ctx, db, func(exec DB) error {
			if err := InsertSignal(withSignalSystem(ctx), exec, s); err != nil {
				return err
			}
			res, err := exec.ExecContext(ctx, `UPDATE smeldr_change_check_areas SET signal_id = $1 WHERE check_id = $2 AND area_id = $3 AND signal_id = ''`, s.ID, c.ID, area.id)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return errRequestTaken
			}
			edge, _, err = writeEdgeRow(ctx, exec, RelationEdge{SourceType: "Signal", SourceID: s.ID, TargetType: "Area", TargetID: area.id,
				RelationKind: "belongs_to_area", EdgeClass: "asserted", CreatedByJob: &job, Attributes: json.RawMessage("{}")})
			return err
		})
		if errors.Is(err, errRequestTaken) {
			continue // another call opened it at the same moment
		}
		if err != nil {
			return fmt.Errorf("%w: request approval of %s for %s: %v", ErrInternal, c.Address, area.address, err)
		}
		if a.relationStore != nil {
			a.relationStore.announceEdge(ctx, edge, true)
		}
		resolver.announceCreated(ctx, s)
	}
	return nil
}

// closeChangeCheckRequest moves the open request id of a Check to to
// (answered or withdrawn), with text as the answer or the reason, recording
// the change by the actor in ctx.
func (a *App) closeChangeCheckRequest(ctx context.Context, id, to, text, ref string) error {
	now := time.Now().UTC()
	actor, kind := actorFromContext(ctx)
	res, err := a.cfg.DB.ExecContext(ctx,
		`UPDATE smeldr_signals SET status = $1, answer = $2, answer_ref = $3, updated_at = $4, last_actor = $5
		  WHERE id = $6 AND status = 'open'`, to, text, ref, now, actor, id)
	if err != nil {
		return fmt.Errorf("%w: close the request %s: %v", ErrInternal, id, err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return nil
	}
	applyStateChange(ctx, a.cfg.DB, a.provenanceStore, stateChange{
		typeName: "Signal", id: id, from: signalOpen, to: to, reason: text, actorKind: kind, actorID: actor,
	})
	dispatchTransitionWebhook(ctx, a.webhookStore, a.webhookPool, a.eventBroadcaster, OperationApproveChange, eventSignalTransitioned,
		transitionWebhookData{Type: "signal", ID: id, FromState: signalOpen, ToState: to, Reason: text, ActorID: actor, ActorKind: kind})
	return nil
}

// withdrawChangeCheckRequests withdraws the open requests of the Check id
// with reason: a superseded Check's answers would count for nothing.
func (a *App) withdrawChangeCheckRequests(ctx context.Context, id, reason string) error {
	rows, err := a.cfg.DB.QueryContext(ctx, `SELECT signal_id FROM smeldr_change_check_areas WHERE check_id = $1 AND signal_id <> ''`, id)
	if err != nil {
		return fmt.Errorf("%w: the requests of %s: %v", ErrInternal, id, err)
	}
	var ids []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			rows.Close()
			return fmt.Errorf("%w: the requests of %s: %v", ErrInternal, id, err)
		}
		ids = append(ids, s)
	}
	rows.Close()
	for _, s := range ids {
		if err := a.closeChangeCheckRequest(ctx, s, signalWithdrawn, reason, ""); err != nil {
			return err
		}
	}
	return nil
}

// refuseChangeCheckRequest refuses the generic answer and withdraw on a
// Check's approval request: an answer must count for its Area (D144).
func refuseChangeCheckRequest(ctx context.Context, db DB, id string) error {
	var subjectType, signalType string
	err := db.QueryRowContext(ctx, `SELECT subject_type, signal_type FROM smeldr_signals WHERE id = $1`, id).Scan(&subjectType, &signalType)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: read the request %s: %v", ErrInternal, id, err)
	}
	if subjectType == "ChangeCheck" && signalType == changeCheckSignalType {
		return fmt.Errorf("%w: this request is a Check's approval for one Area: answer it with answer_change_check (App.AnswerChangeCheck), which checks the answerer's authority over that Area", ErrConflict)
	}
	return nil
}

// changeCheckAreaRow is one Area of a Check as an answer reads it.
type changeCheckAreaRow struct {
	areaID, address, mode, verdict, signalID string
}

// AnswerChangeCheck records the answer for one Area of a Check (D144): approve,
// or not approve with a reason. check is the Check (address, slug or ID); area
// the Area (address, slug or ID). The answerer must hold approve-change over
// that Area (its area-approver, its domain-approver or an approver) or the
// [RoleItemChangeApprover] role on that Check; an agent or job token counts
// only through an area-approver grant on that Area or that role. It answers
// the Area's request and moves the Check: not-approved on the first not
// approved, approved when every Area that requires approval approved.
//
// Refusals: a Check or Area not found ([ErrNotFound]); a reason missing for
// not approved ([ValidationError]); an Area the Check does not touch, one
// that only comments, or one already answered, or a Check not awaiting
// approval ([ErrConflict]); an answerer without the authority
// ([ErrForbidden]).
func (a *App) AnswerChangeCheck(ctx context.Context, check, area string, approve bool, reason string) error {
	reason = strings.TrimSpace(reason)
	if !approve && reason == "" {
		return Err("reason", "is required when the change is not approved: say why")
	}
	if a.cfg.DB == nil {
		return fmt.Errorf("%w: AnswerChangeCheck needs a database", ErrInternal)
	}
	db := a.cfg.DB
	scope, err := scopeFor(ctx, a.governance)
	if err != nil {
		return err
	}
	_, checkID, _, status, err := a.lookupCompiledRecord(ctx, "ChangeCheck", check, scope)
	if err != nil {
		return err
	}
	// The Area is found and must be readable before anything about it is
	// said, and the authority is checked before its mode or verdict is
	// (S3154): an Area the caller may not read is not found.
	var row changeCheckAreaRow
	err = db.QueryRowContext(ctx, `SELECT id, address FROM smeldr_areas WHERE address = $1 OR slug = $1 OR id = $1 LIMIT 1`, area).Scan(&row.areaID, &row.address)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("%w: no Area %q you can read", ErrNotFound, area)
	case err != nil:
		return fmt.Errorf("%w: read the Area %q: %v", ErrInternal, area, err)
	}
	if !scope.Readable("Area", row.areaID) {
		return fmt.Errorf("%w: no Area %q you can read", ErrNotFound, area)
	}
	if err := a.authorizeChangeCheckAnswer(ctx, checkID, row.areaID, row.address); err != nil {
		return err
	}
	err = db.QueryRowContext(ctx, `SELECT approval_mode, verdict, signal_id FROM smeldr_change_check_areas WHERE check_id = $1 AND area_id = $2`,
		checkID, row.areaID).Scan(&row.mode, &row.verdict, &row.signalID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: read the Area %s of the Check: %v", ErrInternal, row.address, err)
	}
	label := recordLabel(ctx, db, "smeldr_change_checks", checkID)
	switch {
	case row.mode == "":
		return fmt.Errorf("%w: %s does not touch Area %s", ErrConflict, label, row.address)
	case row.mode != ApprovalModeRequiresApproval:
		return fmt.Errorf("%w: Area %s only comments on %s (%s); there is nothing to approve", ErrConflict, row.address, label, row.mode)
	case status != ChangeCheckAwaitingApproval:
		return fmt.Errorf("%w: %s is %s, not awaiting approval", ErrConflict, label, status)
	case row.verdict != AreaVerdictAwaiting:
		return fmt.Errorf("%w: Area %s already answered %s (%s)", ErrConflict, row.address, label, row.verdict)
	}
	verdict, text := AreaVerdictApproved, "approve"
	if !approve {
		verdict, text = AreaVerdictNotApproved, "not approved: "+reason
	}
	actor, _ := actorFromContext(ctx)
	res, err := db.ExecContext(ctx,
		`UPDATE smeldr_change_check_areas SET verdict = $1, answered_by = $2, answered_at = $3, reason = $4
		  WHERE check_id = $5 AND area_id = $6 AND verdict = $7`,
		verdict, actor, time.Now().UTC(), reason, checkID, row.areaID, AreaVerdictAwaiting)
	if err != nil {
		return fmt.Errorf("%w: record the answer for %s: %v", ErrInternal, row.address, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: Area %s of %s was answered at the same moment", ErrConflict, row.address, label)
	}
	if row.signalID != "" {
		if err := a.closeChangeCheckRequest(ctx, row.signalID, signalAnswered, text, "ChangeCheck:"+checkID); err != nil {
			return err
		}
	}
	sys := WithReadScope(ctx, AllReadScope())
	if !approve {
		_, err := a.TransitionItemVia(sys, "", "ChangeCheck", checkID, ChangeCheckNotApproved, fmt.Sprintf("not approved for %s: %s", row.address, reason))
		return err
	}
	var waiting int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM smeldr_change_check_areas WHERE check_id = $1 AND approval_mode = $2 AND verdict <> $3`,
		checkID, ApprovalModeRequiresApproval, AreaVerdictApproved).Scan(&waiting); err != nil {
		return fmt.Errorf("%w: count the answers of %s: %v", ErrInternal, label, err)
	}
	if waiting == 0 {
		_, err := a.TransitionItemVia(sys, "", "ChangeCheck", checkID, ChangeCheckApproved, "every Area that requires approval approved it")
		return err
	}
	return nil
}

// authorizeChangeCheckAnswer requires the actor in ctx to hold approve-change
// over the Area (anchor counting), or the one-item role on the Check. An
// agent or job token counts only through an area-approver grant on that Area
// or the one-item role (T884). No governance skips the check.
func (a *App) authorizeChangeCheckAnswer(ctx context.Context, checkID, areaID, areaAddress string) error {
	rs := a.governance
	if rs == nil {
		return nil
	}
	actor, kind := actorFromContext(ctx)
	if actor == "" {
		return fmt.Errorf("%w: answering for Area %s needs approve-change over it, and no actor is known", ErrForbidden, areaAddress)
	}
	areaTarget := AuthTarget{TypeName: "Area", ID: areaID}
	checkTarget := AuthTarget{TypeName: "ChangeCheck", ID: checkID}
	var ok bool
	var err error
	if automatedClass(kind) {
		ok, err = rs.holdsAnchoredGrant(ctx, actor, RoleAreaApprover, areaID)
		if err == nil && !ok {
			ok, err = rs.RoleGranted(ctx, actor, RoleItemChangeApprover, checkTarget)
		}
	} else {
		ok, err = rs.authorizedOver(ctx, actor, OperationApproveChange, areaTarget, true)
		if err == nil && !ok {
			ok, err = rs.authorizedOver(ctx, actor, OperationApproveChange, checkTarget, false)
		}
	}
	if err != nil {
		return fmt.Errorf("%w: the authority over Area %s cannot be read: %s", ErrInternal, areaAddress, err)
	}
	if !ok {
		return fmt.Errorf("%w: answering for Area %s needs approve-change over it (its area-approver, its domain-approver or an approver) or %s on the Check (D144)", ErrForbidden, areaAddress, RoleItemChangeApprover)
	}
	return nil
}

// holdsAnchoredGrant reports whether tokenID holds an unexpired grant of
// roleName anchored on anchorID itself.
func (s *RoleStore) holdsAnchoredGrant(ctx context.Context, tokenID, roleName, anchorID string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM smeldr_role_grants g JOIN smeldr_roles r ON r.id = g.role_id
		  WHERE g.token_id = $1 AND r.name = $2 AND g.scope_anchor_id = $3 AND (g.expires_at IS NULL OR g.expires_at > $4)`,
		tokenID, roleName, anchorID, time.Now().UTC()).Scan(&n)
	return n > 0, err
}
