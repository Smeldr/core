// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Audit after merge (D143, T918 part 3): an Area in audit-after-merge never
// blocks a change. When a change touching it reaches the default branch
// without an approval for it, a merge-unapproved condition opens to
// approve-change, subject the Check, placed in the Area. The approver resolves
// it with [App.AnswerChangeCheck]: approve after the fact, or not approved
// with a reason. The Check's own state is not changed by an audit answer.

// ApprovalModeAuditAfterMerge: a change touching the Area is never blocked;
// one merged without its approval opens an audit condition (D143). Brand's
// screen word may rename it (D141).
const ApprovalModeAuditAfterMerge = "audit-after-merge"

// AreaVerdictAwaitingAudit: the change reached the default branch without
// the Area's approval; its merge-unapproved condition is open.
const AreaVerdictAwaitingAudit = "awaiting-audit"

// changeCheckMergeUnapproved is the condition an audit Area opens at merge.
const changeCheckMergeUnapproved = "merge-unapproved"

// MergeInput says a change reached the default branch, as the host's adapter
// reports it.
type MergeInput struct {
	// Repository is owner/name.
	Repository string
	// Number is the change's number on the host.
	Number int
	// HeadCommit is the change's head commit that was merged.
	HeadCommit string
	// MergeCommit is the commit on the default branch.
	MergeCommit string
}

// MergeResult is what [App.RecordMerge] did.
type MergeResult struct {
	// Covered is false when the change has no live Check (nothing it touched
	// was covered): nothing was done.
	Covered bool `json:"covered"`
	// Check is the change's live Check.
	Check *ChangeCheck `json:"check,omitempty"`
	// HeadMismatch is true when the live Check checked another commit than
	// the one merged: the conditions open against it and say so.
	HeadMismatch bool `json:"head_mismatch"`
	// Opened are the addresses of the Areas whose condition this call opened.
	Opened []string `json:"opened"`
}

// RecordMerge records that a change reached the default branch (D143). For
// the change's live Check, each touched Area in audit-after-merge without an
// approval opens a merge-unapproved condition: receiver approve-change,
// subject the Check, placed in that Area, its message naming that Area alone.
// It never blocks, and an Area whose condition is open, or which approved, is
// left as it is, so a second call opens nothing. A change with no live Check
// is a no-op. When the live Check checked another head commit than the one
// merged, the conditions still open against it and their message names both
// commits: the merged code was not the checked code, which an auditor must
// see. The caller needs the check_change capability.
func (a *App) RecordMerge(ctx context.Context, in MergeInput) (MergeResult, error) {
	norm, err := normalizeChangeInput(ChangeInput{Repository: in.Repository, Number: in.Number, HeadCommit: in.HeadCommit})
	if err != nil {
		return MergeResult{}, err
	}
	merge := strings.ToLower(strings.TrimSpace(in.MergeCommit))
	if !changeCheckCommitRe.MatchString(merge) {
		return MergeResult{}, Err("merge_commit", "must be a full 40-character commit SHA")
	}
	if err := a.authorizeCheckChange(ctx); err != nil {
		return MergeResult{}, err
	}
	if a.cfg.DB == nil {
		return MergeResult{}, fmt.Errorf("%w: RecordMerge needs a database", ErrInternal)
	}
	db := a.cfg.DB
	sys := WithReadScope(ctx, AllReadScope())
	c, err := QueryOne[*ChangeCheck](sys, db,
		`SELECT * FROM smeldr_change_checks WHERE repository = $1 AND number = $2 AND status <> $3 ORDER BY address_no DESC LIMIT 1`,
		norm.Repository, norm.Number, ChangeCheckSuperseded)
	switch {
	case errors.Is(err, ErrNotFound):
		return MergeResult{Covered: false, Opened: []string{}}, nil
	case err != nil:
		return MergeResult{}, fmt.Errorf("%w: the live Check of %s#%d: %v", ErrInternal, norm.Repository, norm.Number, err)
	}
	out := MergeResult{Covered: true, Check: c, HeadMismatch: c.HeadCommit != norm.HeadCommit, Opened: []string{}}
	rows, err := db.QueryContext(sys,
		`SELECT c.area_id, a.address, c.verdict FROM smeldr_change_check_areas c JOIN smeldr_areas a ON a.id = c.area_id
		  WHERE c.check_id = $1 AND c.approval_mode = $2 AND c.verdict NOT IN ($3, $4)`,
		c.ID, ApprovalModeAuditAfterMerge, AreaVerdictApproved, AreaVerdictAwaitingAudit)
	if err != nil {
		return MergeResult{}, fmt.Errorf("%w: the Areas of %s: %v", ErrInternal, c.Address, err)
	}
	type pending struct{ id, address, verdict string }
	var areas []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.id, &p.address, &p.verdict); err != nil {
			rows.Close()
			return MergeResult{}, fmt.Errorf("%w: the Areas of %s: %v", ErrInternal, c.Address, err)
		}
		areas = append(areas, p)
	}
	rows.Close()
	note := ""
	if out.HeadMismatch {
		note = fmt.Sprintf(" The merged head %s is not the checked head %s.", norm.HeadCommit[:12], c.HeadCommit[:12])
	}
	resolver := a.signalResolver()
	job := "change-check"
	for _, area := range areas {
		s := &Signal{
			Sender: "system", Receiver: OperationApproveChange, SignalType: changeCheckMergeUnapproved,
			Message: fmt.Sprintf("Check %s of %s#%d reached the default branch at %s without approval for Area %s: approve it after the fact, or record not approved with a reason, with answer_change_check.%s",
				c.Address, c.Repository, c.Number, merge[:12], area.address, note),
			SubjectType: "ChangeCheck", SubjectID: c.ID,
		}
		var edge RelationEdge
		err := runInTx(sys, db, func(exec DB) error {
			res, err := exec.ExecContext(sys,
				`UPDATE smeldr_change_check_areas SET verdict = $1 WHERE check_id = $2 AND area_id = $3 AND verdict = $4`,
				AreaVerdictAwaitingAudit, c.ID, area.id, area.verdict)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return errRequestTaken // another call opened it, or it was answered
			}
			if err := InsertSignal(withSignalSystem(sys), exec, s); err != nil {
				return err
			}
			if _, err := exec.ExecContext(sys, `UPDATE smeldr_change_check_areas SET signal_id = $1 WHERE check_id = $2 AND area_id = $3`, s.ID, c.ID, area.id); err != nil {
				return err
			}
			edge, _, err = writeEdgeRow(sys, exec, RelationEdge{SourceType: "Signal", SourceID: s.ID, TargetType: "Area", TargetID: area.id,
				RelationKind: "belongs_to_area", EdgeClass: "asserted", CreatedByJob: &job, Attributes: []byte("{}")})
			return err
		})
		if errors.Is(err, errRequestTaken) {
			continue
		}
		if err != nil {
			return MergeResult{}, fmt.Errorf("%w: open the audit of %s for %s: %v", ErrInternal, c.Address, area.address, err)
		}
		if a.relationStore != nil {
			a.relationStore.announceEdge(sys, edge, true)
		}
		resolver.announceCreated(sys, s)
		out.Opened = append(out.Opened, area.address)
	}
	return out, nil
}

// answerAuditArea records an answer for an audit-after-merge Area of a Check:
// before the merge it is an approval in advance, after it the answer resolves
// the Area's merge-unapproved condition (approved after the fact, or not
// approved with the reason). The Check's state is not changed. An Area
// already answered is [ErrConflict], and so is a superseded Check.
func (a *App) answerAuditArea(ctx context.Context, checkID, label, status string, row changeCheckAreaRow, approve bool, reason string) error {
	db := a.cfg.DB
	switch {
	case status == ChangeCheckSuperseded:
		return fmt.Errorf("%w: %s is superseded; answer for the change's live Check", ErrConflict, label)
	case row.verdict != AreaVerdictCommented && row.verdict != AreaVerdictAwaitingAudit:
		return fmt.Errorf("%w: Area %s already answered %s (%s)", ErrConflict, row.address, label, row.verdict)
	}
	verdict, text := AreaVerdictApproved, "approved"
	if !approve {
		verdict, text = AreaVerdictNotApproved, "not approved: "+reason
	}
	actor, _ := actorFromContext(ctx)
	if actor != "" {
		text += " (by " + actor + ")"
	}
	res, err := db.ExecContext(ctx,
		`UPDATE smeldr_change_check_areas SET verdict = $1, answered_by = $2, answered_at = $3, reason = $4
		  WHERE check_id = $5 AND area_id = $6 AND verdict = $7`,
		verdict, actor, time.Now().UTC(), reason, checkID, row.areaID, row.verdict)
	if err != nil {
		return fmt.Errorf("%w: record the answer for %s: %v", ErrInternal, row.address, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: Area %s of %s was answered at the same moment", ErrConflict, row.address, label)
	}
	if row.signalID == "" {
		return nil
	}
	resolver := a.signalResolver()
	open, err := resolver.selectOpen(ctx, "id = $1", row.signalID)
	if err != nil {
		return fmt.Errorf("%w: the audit condition of %s: %v", ErrInternal, row.address, err)
	}
	resolver.resolve(ctx, open, text)
	return nil
}
