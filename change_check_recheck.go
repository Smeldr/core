// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"
)

// The reopen of open Checks (D196, T1294): when the Decisions in force for a
// touched Area change, an open Check is checked again against the Decisions
// in force now. The reopen is a new Check of the same commit (the next
// round) that supersedes the old one, so the earlier verdict stays readable
// as history and no locked verdict is rewritten. A change is detected by
// comparing, never by tracking causes: the Decisions in force for the
// Check's Areas against its live checked_against targets.

// recheckInlineLimit bounds how many open Checks one Decision change
// re-checks in its own call; the rest wait for [App.RecheckChangeChecks]. A
// variable only so a test can lower it.
var recheckInlineLimit = 200

// The reasons of a reopen, readable by every reader of the Check: they name
// no Decision and no Area (D196 point 7).
const (
	reopenReasonSuperseded = "checked again as %s: the Decisions in force for a touched Area changed"
	reopenReasonCreated    = "check again of %s: the Decisions in force for a touched Area changed"
	// A touched Area deleted or archived: the new Check leaves it out.
	reopenReasonAreaGoneSuperseded = "checked again as %s: a touched Area is gone or archived"
	reopenReasonAreaGoneCreated    = "check again of %s: a touched Area is gone or archived"
)

// RecheckResult is what [App.RecheckChangeChecks] did.
type RecheckResult struct {
	// Checked is how many open Checks were compared.
	Checked int `json:"checked"`
	// Reopened is how many were checked again as a new Check.
	Reopened int `json:"reopened"`
	// Failed is how many could not be re-checked; each is logged, and the
	// next run tries again.
	Failed int `json:"failed"`
}

// RecheckChangeChecks checks every open Check again (D196, T1294): the
// newest Check of a change not recorded as ended ([App.RecordMerge],
// [App.RecordClose]), made since the reopen rule exists. A Check whose
// Decisions in force changed is checked again as a new Check of the same
// commit, which supersedes it, waits for approval again where an Area
// requires it, and tells the host through its transition. A Check whose
// Decisions did not change is not written to.
//
// A Decision change re-checks its own Checks at once, in the call that made
// it, up to 200. This sweep is the guarantee for the rest and for any step
// that failed: call it every few minutes when you use the change check
// (example/server runs it on CHANGE_CHECK_RECHECK_SCHEDULE). Until it runs,
// a Check whose inline re-check failed still reads as before. One Check that
// fails is counted in Failed and logged, and the run goes on; the error is
// only for a run that could not read the open Checks.
func (a *App) RecheckChangeChecks(ctx context.Context) (RecheckResult, error) {
	if a.cfg.DB == nil {
		return RecheckResult{}, fmt.Errorf("%w: RecheckChangeChecks needs a database", ErrInternal)
	}
	sys := WithReadScope(ctx, AllReadScope())
	checks, err := a.openChangeChecks(sys, "", nil, 0)
	if isNoSuchTable(err) {
		return RecheckResult{}, nil
	}
	if err != nil {
		return RecheckResult{}, err
	}
	return a.recheckChecks(sys, checks), nil
}

// openChangeChecks returns the open Checks (D196 point 1), with an extra
// condition on c when where is set, at most limit when limit is above 0.
func (a *App) openChangeChecks(ctx context.Context, where string, args []any, limit int) ([]*ChangeCheck, error) {
	q := `SELECT c.* FROM smeldr_change_checks c
	       WHERE c.status NOT IN ($1, $2) AND c.reopenable
	         AND NOT EXISTS (SELECT 1 FROM smeldr_change_ends e WHERE e.repository = c.repository AND e.number = c.number)
	         AND NOT EXISTS (SELECT 1 FROM smeldr_change_checks n WHERE n.repository = c.repository AND n.number = c.number AND n.address_no > c.address_no)`
	all := append([]any{ChangeCheckSuperseded, ChangeCheckChecking}, args...)
	if where != "" {
		q += " AND (" + where + ")"
	}
	q += " ORDER BY c.address_no"
	if limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", limit)
	}
	checks, err := Query[*ChangeCheck](ctx, a.cfg.DB, q, all...)
	if err != nil {
		if isNoSuchTable(err) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: the open Checks: %v", ErrInternal, err)
	}
	return checks, nil
}

// recheckChecks re-checks each of checks, reading the Decisions in force for
// one set of Areas once.
func (a *App) recheckChecks(ctx context.Context, checks []*ChangeCheck) RecheckResult {
	var out RecheckResult
	cache := map[string][]AppliedDecision{}
	for _, c := range checks {
		out.Checked++
		reopened, err := a.recheckChangeCheck(ctx, c, cache)
		switch {
		case err != nil:
			out.Failed++
			slog.WarnContext(ctx, "smeldr: Check not re-checked; the next sweep tries again", "check", c.Address, "error", err)
		case reopened:
			out.Reopened++
		}
	}
	return out
}

// recheckChangeCheck compares c's checked_against with the Decisions in force
// now for its Areas and, when they differ, checks it again as a new Check.
// It reports whether it did. cache, when set, holds the Decisions in force
// per set of Areas for one run.
//
// checked_against is read with its ended edges too. A Check never ends its
// own edges (an old Check keeps them as history), so an ended one means its
// Decision is gone: the structural sweep ends edges to a deleted record, and
// if it ran before this re-check, the live edges alone would look equal to
// the Decisions in force and the Check would never reopen. A touched Area
// that is gone or archived is a change too: the new Check leaves it out.
func (a *App) recheckChangeCheck(ctx context.Context, c *ChangeCheck, cache map[string][]AppliedDecision) (bool, error) {
	db := a.cfg.DB
	areas, err := queryStrings(ctx, db, `SELECT area_id FROM smeldr_change_check_areas WHERE check_id = $1 ORDER BY area_id`, c.ID)
	if err != nil {
		return false, fmt.Errorf("%w: the Areas of %s: %v", ErrInternal, c.Address, err)
	}
	if len(areas) == 0 {
		return false, nil
	}
	args := make([]any, len(areas))
	for i, id := range areas {
		args[i] = id
	}
	live, err := queryStrings(ctx, db, `SELECT id FROM smeldr_areas WHERE status = 'active' AND id IN (`+inMarks(1, len(areas))+`) ORDER BY id`, args...)
	if err != nil {
		return false, fmt.Errorf("%w: the Areas of %s: %v", ErrInternal, c.Address, err)
	}
	against, err := queryStrings(ctx, db,
		`SELECT target_id FROM smeldr_relations WHERE source_type = 'ChangeCheck' AND source_id = $1 AND relation_kind = 'checked_against'
		    AND edge_class = 'asserted'`, c.ID)
	if err != nil {
		return false, fmt.Errorf("%w: what %s was checked against: %v", ErrInternal, c.Address, err)
	}
	var decisions []AppliedDecision
	if len(live) > 0 {
		key := strings.Join(live, ",")
		var ok bool
		if decisions, ok = cache[key]; !ok {
			// The same scope and Organization flag as CheckChange: the system
			// read scope and the Organization included.
			_, decisions, err = a.inForceForAreas(ctx, AllReadScope(), live, true)
			if err != nil {
				return false, err
			}
			if cache != nil {
				cache[key] = decisions
			}
		}
	}
	areaGone := len(live) != len(areas)
	if !areaGone && sameDecisions(against, decisions) {
		return false, nil
	}
	return a.reopenChangeCheck(ctx, c, live, decisions, areaGone)
}

// sameDecisions reports whether ids and decisions name the same Decisions.
func sameDecisions(ids []string, decisions []AppliedDecision) bool {
	want := make(map[string]bool, len(decisions))
	for _, d := range decisions {
		want[d.ID] = true
	}
	got := make(map[string]bool, len(ids))
	for _, id := range ids {
		got[id] = true
	}
	if len(got) != len(want) {
		return false
	}
	for id := range got {
		if !want[id] {
			return false
		}
	}
	return true
}

// queryStrings returns the one text column of q's rows.
func queryStrings(ctx context.Context, db DB, q string, args ...any) ([]string, error) {
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// reopenChangeCheck checks old again as a new Check of the same commit, the
// next round: its Areas with their approval modes now, checked_against the
// Decisions in force now, fresh Area rows so no earlier answer counts. In
// three short steps, in an order that tells the host first: the new Check
// is made (only while old is still the newest Check of its change), it is
// settled (awaiting-approval with new requests where an Area requires
// approval, else commented; its transition reaches the adapter), and old is
// superseded with the reason. It reports false when another call or a newer
// commit got there first.
func (a *App) reopenChangeCheck(ctx context.Context, old *ChangeCheck, areaIDs []string, decisions []AppliedDecision, areaGone bool) (bool, error) {
	// Core's own write, under the system read scope, as CheckChange makes one.
	ctx = WithReadScope(ctx, AllReadScope())
	db := a.cfg.DB
	modes := map[string]string{}
	if len(areaIDs) > 0 {
		args := make([]any, len(areaIDs))
		for i, id := range areaIDs {
			args[i] = id
		}
		rows, err := db.QueryContext(ctx, `SELECT id, approval_mode FROM smeldr_areas WHERE id IN (`+inMarks(1, len(areaIDs))+`)`, args...)
		if err != nil {
			return false, fmt.Errorf("%w: the Areas of %s: %v", ErrInternal, old.Address, err)
		}
		for rows.Next() {
			var id, mode string
			if err := rows.Scan(&id, &mode); err != nil {
				rows.Close()
				return false, fmt.Errorf("%w: the Areas of %s: %v", ErrInternal, old.Address, err)
			}
			modes[id] = mode
		}
		rows.Close()
	}
	superseded, created := reopenReasonSuperseded, reopenReasonCreated
	if areaGone {
		superseded, created = reopenReasonAreaGoneSuperseded, reopenReasonAreaGoneCreated
	}
	var round int
	if err := db.QueryRowContext(ctx, `SELECT round FROM smeldr_change_checks WHERE id = $1`, old.ID).Scan(&round); err != nil {
		return false, fmt.Errorf("%w: the round of %s: %v", ErrInternal, old.Address, err)
	}
	setting := ApprovalModeCommentOnly
	for _, id := range areaIDs {
		if approvalModeRank[modes[id]] > approvalModeRank[setting] {
			setting = modes[id]
		}
	}
	actor, _ := actorFromContext(ctx)
	now := time.Now().UTC()
	c := &ChangeCheck{
		Node:       Node{ID: NewID(), Status: ChangeCheckChecking, CreatedAt: now, UpdatedAt: now},
		Repository: old.Repository, Number: old.Number, HeadCommit: old.HeadCommit,
		FilesTotal: old.FilesTotal, FilesChecked: old.FilesChecked, CheckedPartly: old.CheckedPartly,
		Setting: setting, LastActor: actor,
	}
	c.Slug = "chk-" + strings.ToLower(c.ID)
	sort.Strings(areaIDs)
	var edges []RelationEdge
	inserted := false
	err := runInTx(ctx, db, func(exec DB) error {
		address, no, err := allocateAddress(ctx, exec, "ChangeCheck")
		if err != nil {
			return err
		}
		c.assignAddress(address, no)
		r, err := exec.ExecContext(ctx,
			`INSERT INTO smeldr_change_checks (id, slug, status, created_at, updated_at, rev, repository, number, head_commit,
				files_total, files_checked, checked_partly, setting, last_actor, address, address_no, external_ref, round, reopenable)
			 SELECT $1, $2, $3, $4, $5, 0, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, '', $16, TRUE
			 WHERE NOT EXISTS (SELECT 1 FROM smeldr_change_checks WHERE repository = $6 AND number = $7 AND address_no > $17 AND id <> $1)`,
			c.ID, c.Slug, c.Status, now, now, c.Repository, c.Number, c.HeadCommit,
			c.FilesTotal, c.FilesChecked, c.CheckedPartly, c.Setting, c.LastActor, address, no, round+1, old.AddressNo)
		if err != nil {
			return err
		}
		if n, _ := r.RowsAffected(); n == 0 {
			return nil // a newer Check of the change exists: nothing to do
		}
		inserted = true
		job := "change-check"
		edge := func(targetType, targetID, kind string) error {
			e, wrote, err := writeEdgeRow(ctx, exec, RelationEdge{SourceType: "ChangeCheck", SourceID: c.ID, TargetType: targetType, TargetID: targetID,
				RelationKind: kind, EdgeClass: "asserted", CreatedByJob: &job, Attributes: json.RawMessage("{}")})
			if err == nil && wrote {
				edges = append(edges, e)
			}
			return err
		}
		for _, id := range areaIDs {
			verdict := AreaVerdictCommented
			if modes[id] == ApprovalModeRequiresApproval {
				verdict = AreaVerdictAwaiting
			}
			if _, err := exec.ExecContext(ctx,
				`INSERT INTO smeldr_change_check_areas (check_id, area_id, approval_mode, verdict) VALUES ($1, $2, $3, $4)`,
				c.ID, id, modes[id], verdict); err != nil {
				return err
			}
			if err := edge("Area", id, "belongs_to_area"); err != nil {
				return err
			}
		}
		for _, d := range decisions {
			if err := edge("Decision", d.ID, "checked_against"); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		// Another reopen of the same Check took the round first: it won.
		if newer, ferr := a.newerChangeCheck(ctx, old); ferr == nil && newer {
			return false, nil
		}
		return false, fmt.Errorf("%w: check %s again: %v", ErrInternal, old.Address, err)
	}
	if !inserted {
		return false, nil
	}
	actorID, actorKind := actorFromContext(ctx)
	recordProvenance(ctx, a.provenanceStore, ProvenanceRecord{
		SubjectType: "ChangeCheck", SubjectID: c.ID, Verb: "create", ActorID: actorID, ActorKind: actorKind,
		Reason: fmt.Sprintf(created, old.Address),
	})
	if a.relationStore != nil {
		for _, e := range edges {
			a.relationStore.announceEdge(ctx, e, true)
		}
	}
	// The host hears the new verdict before the old one is closed.
	if err := a.settleChangeCheck(ctx, c); err != nil {
		return true, err
	}
	if _, err := a.claimSupersedeWith(ctx, old.ID, old.Address, c.ID, c.Address, fmt.Sprintf(superseded, c.Address)); err != nil {
		return true, err
	}
	return true, nil
}

// newerChangeCheck reports whether a Check of old's change newer than old
// exists.
func (a *App) newerChangeCheck(ctx context.Context, old *ChangeCheck) (bool, error) {
	var n int
	err := a.cfg.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM smeldr_change_checks WHERE repository = $1 AND number = $2 AND address_no > $3`,
		old.Repository, old.Number, old.AddressNo).Scan(&n)
	return n > 0, err
}

// recheckForDecision re-checks, in the call that changed the Decision, the
// open Checks it may concern: those checked against it (it left force or its
// place) and those whose Areas its place reaches now (it entered force or
// moved there). At most recheckInlineLimit; the rest wait for the sweep.
// Fail-open for the caller: the Decision's write has committed, so an error
// is logged and the sweep tries again.
func (a *App) recheckForDecision(ctx context.Context, decisionID string) {
	if a.cfg.DB == nil {
		return
	}
	sys := WithReadScope(ctx, AllReadScope())
	where := `EXISTS (SELECT 1 FROM smeldr_relations r WHERE r.source_type = 'ChangeCheck' AND r.source_id = c.id AND r.relation_kind = 'checked_against'
	            AND r.target_id = $3 AND r.invalid_at IS NULL)`
	args := []any{decisionID}
	place, placed, err := decisionPlace(sys, a.cfg.DB, decisionID)
	if err != nil {
		slog.WarnContext(ctx, "smeldr: Checks of a Decision not re-checked; the next sweep tries again", "decision", decisionID, "error", err)
		return
	}
	if placed {
		switch place.targetType {
		case "Organization":
			where = "TRUE"
		case "Domain":
			where += ` OR EXISTS (SELECT 1 FROM smeldr_change_check_areas ca JOIN smeldr_relations d ON d.source_id = ca.area_id
			             AND d.relation_kind = 'belongs_to_domain' AND d.edge_class = 'asserted' AND d.invalid_at IS NULL
			           WHERE ca.check_id = c.id AND d.target_id = $4)`
			args = append(args, place.targetID)
		case "Area":
			where += ` OR EXISTS (SELECT 1 FROM smeldr_change_check_areas ca WHERE ca.check_id = c.id AND ca.area_id = $4)`
			args = append(args, place.targetID)
		}
	}
	if where == "TRUE" {
		where, args = "", nil
	}
	checks, err := a.openChangeChecks(sys, where, args, recheckInlineLimit+1)
	if isNoSuchTable(err) {
		return
	}
	if err != nil {
		slog.WarnContext(ctx, "smeldr: Checks of a Decision not re-checked; the next sweep tries again", "decision", decisionID, "error", err)
		return
	}
	if len(checks) > recheckInlineLimit {
		slog.InfoContext(ctx, "smeldr: more open Checks than one Decision change re-checks; the sweep does the rest", "decision", decisionID, "limit", recheckInlineLimit)
		checks = checks[:recheckInlineLimit]
	}
	a.recheckChecks(sys, checks)
}

// afterChangeReopened handles a change that was closed and is open again:
// checked again when the Decisions in force changed meanwhile, else asked
// again where its requests were withdrawn. It returns the live Check.
func (a *App) afterChangeReopened(ctx context.Context, c *ChangeCheck) (*ChangeCheck, error) {
	if string(c.Status) == ChangeCheckSuperseded || string(c.Status) == ChangeCheckChecking {
		return c, nil
	}
	reopened, err := a.recheckChangeCheck(ctx, c, nil)
	if err != nil {
		return nil, err
	}
	if reopened {
		// The reopen made the commit's newest Check.
		return a.findChangeCheck(ctx, c.Repository, c.Number, c.HeadCommit)
	}
	return c, a.rerequestAfterReopen(ctx, c)
}
