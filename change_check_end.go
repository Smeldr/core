// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// How a change ended, in smeldr_change_ends (D196, T1294). A change with a
// row there is not open: its Check is never reopened.
const (
	// changeEndMerged: the change reached the default branch. Final.
	changeEndMerged = "merged"
	// changeEndClosed: the change was closed without a merge. Opening it
	// again removes the row.
	changeEndClosed = "closed"
)

// changeEndsTableStmt is the table of ended changes: one row per change, how
// it ended and when.
const changeEndsTableStmt = `CREATE TABLE IF NOT EXISTS smeldr_change_ends (
		repository   TEXT NOT NULL,
		number       INTEGER NOT NULL,
		head_commit  TEXT NOT NULL DEFAULT '',
		how          TEXT NOT NULL,
		merge_commit TEXT NOT NULL DEFAULT '',
		ended_at     TIMESTAMPTZ NOT NULL,
		PRIMARY KEY (repository, number)
	)`

// recordChangeEnd records that the change ended. A merge is final and
// replaces a closed row; a close never replaces a merge.
func recordChangeEnd(ctx context.Context, db DB, repo string, number int, head, how, merge string) error {
	q := `INSERT INTO smeldr_change_ends (repository, number, head_commit, how, merge_commit, ended_at) VALUES ($1, $2, $3, $4, $5, $6)
	      ON CONFLICT (repository, number) DO NOTHING`
	if how == changeEndMerged {
		q = `INSERT INTO smeldr_change_ends (repository, number, head_commit, how, merge_commit, ended_at) VALUES ($1, $2, $3, $4, $5, $6)
		     ON CONFLICT (repository, number) DO UPDATE SET head_commit = excluded.head_commit, how = excluded.how,
		       merge_commit = excluded.merge_commit, ended_at = excluded.ended_at
		     WHERE smeldr_change_ends.how <> 'merged'`
	}
	if _, err := db.ExecContext(ctx, q, repo, number, head, how, merge, time.Now().UTC()); err != nil {
		return fmt.Errorf("%w: record that %s#%d was %s: %v", ErrInternal, repo, number, how, err)
	}
	return nil
}

// reopenClosedChange removes the closed row of a change opened again, and
// reports whether there was one. A merged row stays: a merge is final.
func reopenClosedChange(ctx context.Context, db DB, repo string, number int) (bool, error) {
	res, err := db.ExecContext(ctx, `DELETE FROM smeldr_change_ends WHERE repository = $1 AND number = $2 AND how = $3`, repo, number, changeEndClosed)
	if err != nil {
		return false, fmt.Errorf("%w: open %s#%d again: %v", ErrInternal, repo, number, err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// CloseInput says a change was closed without a merge, as the host's adapter
// reports it.
type CloseInput struct {
	// Repository is owner/name.
	Repository string
	// Number is the change's number on the host.
	Number int
	// HeadCommit is the change's head commit when it was closed.
	HeadCommit string
}

// CloseResult is what [App.RecordClose] did.
type CloseResult struct {
	// Covered is false when the change has no live Check (nothing it touched
	// was covered): the close is still recorded.
	Covered bool `json:"covered"`
	// Check is the change's live Check, whose open requests were withdrawn.
	Check *ChangeCheck `json:"check,omitempty"`
}

// RecordClose records that a change was closed without a merge (D196, T1294).
// The change is no longer open: its live Check is never reopened when the
// Decisions for its Areas change, and the Check's open approval requests are
// withdrawn, since nobody needs to answer them. The Check's own state does
// not move. Opening the change again ([App.CheckChange] after the host's
// reopen event) makes it open again. Idempotent. The caller needs the
// check_change capability.
func (a *App) RecordClose(ctx context.Context, in CloseInput) (CloseResult, error) {
	norm, err := normalizeChangeInput(ChangeInput{Repository: in.Repository, Number: in.Number, HeadCommit: in.HeadCommit})
	if err != nil {
		return CloseResult{}, err
	}
	if err := a.authorizeCheckChange(ctx); err != nil {
		return CloseResult{}, err
	}
	if a.cfg.DB == nil {
		return CloseResult{}, fmt.Errorf("%w: RecordClose needs a database", ErrInternal)
	}
	sys := WithReadScope(ctx, AllReadScope())
	if err := recordChangeEnd(sys, a.cfg.DB, norm.Repository, norm.Number, norm.HeadCommit, changeEndClosed, ""); err != nil {
		return CloseResult{}, err
	}
	c, err := a.liveChangeCheck(sys, norm.Repository, norm.Number)
	if err != nil || c == nil {
		return CloseResult{Covered: false}, err
	}
	if err := a.withdrawChangeCheckRequests(sys, c.ID, "the change was closed without a merge"); err != nil {
		return CloseResult{}, err
	}
	return CloseResult{Covered: true, Check: c}, nil
}

// liveChangeCheck is the newest Check of a change that is not superseded, or
// nil.
func (a *App) liveChangeCheck(ctx context.Context, repo string, number int) (*ChangeCheck, error) {
	c, err := QueryOne[*ChangeCheck](ctx, a.cfg.DB,
		`SELECT * FROM smeldr_change_checks WHERE repository = $1 AND number = $2 AND status <> $3 ORDER BY address_no DESC LIMIT 1`,
		repo, number, ChangeCheckSuperseded)
	switch {
	case errors.Is(err, ErrNotFound):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("%w: the live Check of %s#%d: %v", ErrInternal, repo, number, err)
	}
	return c, nil
}

// rerequestAfterReopen gives each requires-approval Area of c that still
// awaits an answer, and whose request was withdrawn (a close), a new request.
// An Area that answered keeps its answer.
func (a *App) rerequestAfterReopen(ctx context.Context, c *ChangeCheck) error {
	if _, err := a.cfg.DB.ExecContext(ctx,
		`UPDATE smeldr_change_check_areas SET signal_id = '' WHERE check_id = $1 AND approval_mode = $2 AND verdict = $3
		   AND signal_id IN (SELECT id FROM smeldr_signals WHERE status = $4)`,
		c.ID, ApprovalModeRequiresApproval, AreaVerdictAwaiting, signalWithdrawn); err != nil {
		return fmt.Errorf("%w: the withdrawn requests of %s: %v", ErrInternal, c.Address, err)
	}
	return a.requestChangeCheckApprovals(ctx, c)
}
