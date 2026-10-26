package smeldr

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// Bounds of [FindingQuery.Limit].
const (
	defaultFindingLimit = 100
	maxFindingLimit     = 500
)

// FindingQuery filters [App.ListFindings].
type FindingQuery struct {
	// Detector keeps the findings of one detector (structural, eval-queue,
	// standing-drift, ...); empty keeps every detector.
	Detector string
	// SubjectType keeps the findings about one type; empty keeps every type.
	SubjectType string
	// Limit is how many findings to return: 0 means 100, at most 500.
	Limit int
}

// FindingView is one recorded [Finding] as a reader sees it (T953): its
// detector, its subject, the subject's address when it has one, the message
// and when it was first and last seen. The finding's own ID is internal and
// not shown.
type FindingView struct {
	Detector    string    `json:"detector"`
	SubjectType string    `json:"subject_type"`
	SubjectID   string    `json:"subject_id"`
	Address     string    `json:"address,omitempty"`
	Provenance  string    `json:"provenance"`
	Message     string    `json:"message"`
	FirstSeenAt time.Time `json:"first_seen_at"`
	LastSeenAt  time.Time `json:"last_seen_at"`
}

// ListFindings returns the recorded findings the caller may see, newest first
// by when they were last seen (T953). A finding names a subject and says
// something about it, so it is shown only when the caller may read that
// subject: an item by the read rules, a relation edge (detector structural)
// only when both its ends are readable, and a finding whose subject no
// longer exists only to a scope that reads everything. Filtered rows do not
// count toward the limit: the read goes on until Limit findings are shown or
// none are left.
//
// Without a [FindingStore] wired ([App.Findings]) it is [ErrNotFound] with
// the reason, never an empty list that looks like nothing was found. A limit
// below 0 or above 500 is [ErrBadRequest]; a failed read is [ErrInternal].
func (a *App) ListFindings(ctx Context, q FindingQuery) ([]FindingView, error) {
	if a.findingStore == nil || a.cfg.DB == nil {
		return nil, fmt.Errorf("%w: findings are not recorded on this instance (no finding store is wired)", ErrNotFound)
	}
	switch {
	case q.Limit < 0 || q.Limit > maxFindingLimit:
		return nil, fmt.Errorf("%w: limit must be between 0 and %d", ErrBadRequest, maxFindingLimit)
	case q.Limit == 0:
		q.Limit = defaultFindingLimit
	}
	scope, err := scopeFor(ctx, a.governance)
	if err != nil {
		return nil, err
	}
	db := a.cfg.DB
	query := `SELECT detector, subject_type, subject_id, provenance, message, first_seen_at, last_seen_at FROM smeldr_findings WHERE 1 = 1`
	var args []any
	if q.Detector != "" {
		args = append(args, q.Detector)
		query += fmt.Sprintf(" AND detector = $%d", len(args))
	}
	if q.SubjectType != "" {
		args = append(args, q.SubjectType)
		query += fmt.Sprintf(" AND subject_type = $%d", len(args))
	}
	query += " ORDER BY last_seen_at DESC, id"
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("%w: list findings: %v", ErrInternal, err)
	}
	var all []FindingView
	for rows.Next() {
		var f FindingView
		if err := rows.Scan(&f.Detector, &f.SubjectType, &f.SubjectID, &f.Provenance, &f.Message, scanDest(&f.FirstSeenAt), scanDest(&f.LastSeenAt)); err != nil {
			rows.Close()
			return nil, fmt.Errorf("%w: list findings: %v", ErrInternal, err)
		}
		all = append(all, f)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, fmt.Errorf("%w: list findings: %v", ErrInternal, err)
	}
	out := []FindingView{}
	for _, f := range all {
		if len(out) == q.Limit {
			break
		}
		if !a.findingVisible(ctx, scope, f) {
			continue
		}
		if f.SubjectType != "RelationEdge" {
			f.Address = lookupAddressByID(ctx, db, f.SubjectType, f.SubjectID, true)
		}
		out = append(out, f)
	}
	return out, nil
}

// findingVisible reports whether scope may see the finding: it may read the
// subject, or for a relation edge both its ends. An edge that no longer
// exists, or whose ends cannot be read, is visible only to a scope that reads
// everything.
func (a *App) findingVisible(ctx context.Context, scope *ReadScope, f FindingView) bool {
	if scope.All() {
		return true
	}
	if f.SubjectType != "RelationEdge" {
		return scope.Readable(f.SubjectType, f.SubjectID)
	}
	var srcType, srcID, dstType, dstID string
	err := a.cfg.DB.QueryRowContext(ctx,
		`SELECT source_type, source_id, target_type, target_id FROM smeldr_relations WHERE id = $1`, f.SubjectID).
		Scan(&srcType, &srcID, &dstType, &dstID)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			slog.WarnContext(ctx, "smeldr: ListFindings: the edge of a finding could not be read; the finding is left out",
				"edge_id", f.SubjectID, "error", err)
		}
		return false
	}
	return scope.Readable(srcType, srcID) && scope.Readable(dstType, dstID)
}
