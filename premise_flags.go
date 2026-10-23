// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// premiseFlagged is the condition of a Decision in force that rests on a
// Decision that is not (D149).
const premiseFlagged = "premise-flagged"

// isPremiseKind reports whether kind is one by which a Decision rests on
// another for D149: grounded in it, or addressing it. amends is left out (Q4).
func isPremiseKind(kind string) bool {
	return kind == "grounded_in" || kind == "addresses"
}

// premiseFlagTableStmts create the table of the premise pairs: one row per
// Decision and premise that ever opened the condition, with its Signal and
// the last confirmation.
var premiseFlagTableStmts = []string{
	`CREATE TABLE IF NOT EXISTS smeldr_premise_flags (
		decision_id             TEXT NOT NULL,
		premise_id              TEXT NOT NULL,
		signal_id               TEXT NOT NULL DEFAULT '',
		confirmed_by            TEXT NOT NULL DEFAULT '',
		confirmed_at            TIMESTAMPTZ,
		confirmed_reason        TEXT NOT NULL DEFAULT '',
		confirmed_premise_state TEXT NOT NULL DEFAULT '',
		PRIMARY KEY (decision_id, premise_id)
	)`,
}

// EnsurePremiseFlagTable creates the table of the premise-flagged pairs
// (D149) on a database made before it. Fresh installs get it from
// [CreateGovernanceTables]. Idempotent, safe to call on every boot.
func EnsurePremiseFlagTable(ctx context.Context, db DB) error {
	for _, stmt := range premiseFlagTableStmts {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("smeldr: EnsurePremiseFlagTable: %w", err)
		}
	}
	return nil
}

// premiseMessage is the condition's message. It names the Decision only,
// never the premise: a reader of the Signal who may not read the premise
// must not learn it from here (condition 3); the premise is found through the
// relations the reader may read.
func premiseMessage(decisionLabel string) string {
	return decisionLabel + " rests on a Decision that is not in force"
}

// premiseDecision is what the rule needs to know of one Decision.
type premiseDecision struct {
	found          bool
	address, state string
}

// readPremiseDecision reads a Decision's address and state.
func readPremiseDecision(ctx context.Context, db DB, id string) (premiseDecision, error) {
	var d premiseDecision
	err := db.QueryRowContext(ctx, `SELECT address, status FROM smeldr_decisions WHERE id = $1`, id).Scan(&d.address, &d.state)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return premiseDecision{}, nil
	case err != nil:
		return premiseDecision{}, err
	}
	d.found = true
	return d, nil
}

// label is how the message names the Decision: its address, or its ID.
func (d premiseDecision) label(id string) string {
	if d.address != "" {
		return d.address
	}
	return "Decision " + id
}

// premiseEdgeLive reports whether a live grounded_in or addresses edge runs
// from the Decision to the premise.
func premiseEdgeLive(ctx context.Context, db DB, decisionID, premiseID string) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM smeldr_relations
		  WHERE source_type = 'Decision' AND source_id = $1 AND target_type = 'Decision' AND target_id = $2
		    AND relation_kind IN ('grounded_in', 'addresses') AND (invalid_at IS NULL OR invalid_at > $3)`,
		decisionID, premiseID, time.Now().UTC()).Scan(&n)
	return n > 0, err
}

// premiseRow is a pair's row: its Signal and last confirmation.
type premiseRow struct {
	found          bool
	signalID       string
	confirmed      bool
	confirmedState string
}

func readPremiseRow(ctx context.Context, db DB, decisionID, premiseID string) (premiseRow, error) {
	var r premiseRow
	var confirmedAt sql.NullString
	err := db.QueryRowContext(ctx,
		`SELECT signal_id, confirmed_premise_state, CAST(confirmed_at AS TEXT) FROM smeldr_premise_flags WHERE decision_id = $1 AND premise_id = $2`,
		decisionID, premiseID).Scan(&r.signalID, &r.confirmedState, &confirmedAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return premiseRow{}, nil
	case err != nil:
		return premiseRow{}, err
	}
	r.found, r.confirmed = true, confirmedAt.Valid
	return r, nil
}

// evaluatePremise brings the premise-flagged condition of the pair
// (decisionID, premiseID) in line with D149: open when the Decision is in
// force, a live grounded_in or addresses edge runs to the premise, and the
// premise is not in force; resolved, with the reason saying which, when that
// no longer holds. In force is a standing that holds (Q2). A pair whose
// condition was confirmed opens again only when the premise's state differs
// from the one it was confirmed at (condition 4). One open condition per
// pair. Fail-open: an error is logged, never returned to the change that
// caused the evaluation.
func (r signalResolver) evaluatePremise(ctx context.Context, decisionID, premiseID string) {
	if _, err := r.evaluatePremiseErr(ctx, decisionID, premiseID); err != nil && !isNoSuchTable(err) {
		slog.WarnContext(ctx, "smeldr: premise-flagged not evaluated", "decision", decisionID, "premise", premiseID, "error", err)
	}
}

// evaluatePremiseErr is evaluatePremise, reporting what it did ("opened",
// "resolved" or "") and its error.
func (r signalResolver) evaluatePremiseErr(ctx context.Context, decisionID, premiseID string) (string, error) {
	db := r.db
	d, err := readPremiseDecision(ctx, db, decisionID)
	if err != nil {
		return "", err
	}
	p, err := readPremiseDecision(ctx, db, premiseID)
	if err != nil {
		return "", err
	}
	live, err := premiseEdgeLive(ctx, db, decisionID, premiseID)
	if err != nil {
		return "", err
	}
	row, err := readPremiseRow(ctx, db, decisionID, premiseID)
	if err != nil {
		return "", err
	}
	var open []openSignal
	if row.signalID != "" {
		if open, err = r.selectOpen(ctx, "id = $1", row.signalID); err != nil {
			return "", err
		}
	}
	holds := holdsStates(ctx, db, "Decision")
	label := d.label(decisionID)
	var ended string
	switch {
	case !d.found:
		ended = label + " no longer exists"
	case !holds[d.state]:
		ended = label + " is no longer in force (" + d.state + ")"
	case !live:
		ended = "the relation from " + label + " to its premise ended"
	case !p.found:
		ended = "the premise of " + label + " no longer exists"
	case holds[p.state]:
		ended = "the premise of " + label + " is in force again"
	}
	if ended != "" {
		if len(open) == 0 {
			return "", nil
		}
		r.resolve(ctx, open, ended)
		return "resolved", nil
	}
	if len(open) > 0 || (row.confirmed && row.confirmedState == p.state) {
		return "", nil
	}
	return r.openPremise(ctx, decisionID, premiseID, label, row)
}

// openPremise opens the condition of a pair: the Signal, its row and its
// place edge in one transaction, announced after the commit (condition 3).
func (r signalResolver) openPremise(ctx context.Context, decisionID, premiseID, label string, row premiseRow) (string, error) {
	s := &Signal{
		Sender: "system", Receiver: decisionRatifyOperation, SignalType: premiseFlagged,
		Message:     premiseMessage(label),
		SubjectType: "Decision", SubjectID: decisionID, RequiredOperation: decisionRatifyOperation,
	}
	// Claim the pair: a concurrent evaluation that opened it first wins, and
	// this transaction rolls its Signal back.
	return r.openDecisionCondition(ctx, s, decisionID, "premise-flagged", func(exec DB, signalID string) (sql.Result, error) {
		if row.found {
			return exec.ExecContext(ctx,
				`UPDATE smeldr_premise_flags SET signal_id = $1, confirmed_by = '', confirmed_at = NULL, confirmed_reason = '', confirmed_premise_state = ''
				  WHERE decision_id = $2 AND premise_id = $3 AND signal_id = $4`,
				signalID, decisionID, premiseID, row.signalID)
		}
		return exec.ExecContext(ctx,
			`INSERT INTO smeldr_premise_flags (decision_id, premise_id, signal_id) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`,
			decisionID, premiseID, signalID)
	})
}

// premisePairsOf returns the pairs a Decision is a side of: its live premise
// edges both ways between Decisions, and every pair row naming it.
func premisePairsOf(ctx context.Context, db DB, decisionID string) ([][2]string, error) {
	return premisePairs(ctx, db,
		`SELECT source_id, target_id FROM smeldr_relations
		  WHERE source_type = 'Decision' AND target_type = 'Decision' AND relation_kind IN ('grounded_in', 'addresses')
		    AND (invalid_at IS NULL OR invalid_at > $2) AND (source_id = $1 OR target_id = $1)
		 UNION
		 SELECT decision_id, premise_id FROM smeldr_premise_flags WHERE decision_id = $1 OR premise_id = $1`,
		decisionID, time.Now().UTC())
}

// premisePairs runs a query of (decision, premise) rows.
func premisePairs(ctx context.Context, db DB, query string, args ...any) ([][2]string, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out [][2]string
	for rows.Next() {
		var p [2]string
		if err := rows.Scan(&p[0], &p[1]); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// evaluatePremisesOf evaluates every pair a Decision is a side of, after its
// state changed. Fail-open.
func (r signalResolver) evaluatePremisesOf(ctx context.Context, decisionID string) {
	pairs, err := premisePairsOf(ctx, r.db, decisionID)
	if err != nil {
		if !isNoSuchTable(err) {
			slog.WarnContext(ctx, "smeldr: premise-flagged pairs not read", "decision", decisionID, "error", err)
		}
		return
	}
	for _, p := range pairs {
		r.evaluatePremise(ctx, p[0], p[1])
	}
}

// premiseEdgeChanged evaluates the pair of an asserted or ended edge, when it
// is a premise edge between Decisions.
func (r signalResolver) premiseEdgeChanged(ctx context.Context, edge RelationEdge) {
	switch {
	case edge.SourceType != "Decision":
	case isPremiseKind(edge.RelationKind) && edge.TargetType == "Decision":
		r.evaluatePremise(ctx, edge.SourceID, edge.TargetID)
	case placeKindTargets[edge.RelationKind] != "":
		r.syncDecisionConditionPlaces(ctx, edge.SourceID)
	}
}

// syncDecisionConditionPlaces keeps every open premise-flagged and
// contradiction (conflict-detected) condition about a Decision placed where
// the Decision is placed (T925, T928): a place edge of the
// condition that is not the Decision's current place is ended (cause
// [EdgeEndMoved]) and the current one written if missing, in one
// transaction per condition, announced after the commit. An unplaced
// Decision leaves its conditions unplaced. Fail-open: an error is logged
// and the condition stays where it was until the next sync or sweep.
func (r signalResolver) syncDecisionConditionPlaces(ctx context.Context, decisionID string) {
	if err := r.syncDecisionConditionPlacesErr(ctx, decisionID); err != nil && !isNoSuchTable(err) {
		slog.WarnContext(ctx, "smeldr: premise-flagged place not synced", "decision", decisionID, "error", err)
	}
}

func (r signalResolver) syncDecisionConditionPlacesErr(ctx context.Context, decisionID string) error {
	open, err := r.selectOpen(ctx, "signal_type IN ('premise-flagged', 'conflict-detected') AND subject_type = 'Decision' AND subject_id = $1", decisionID)
	if err != nil || len(open) == 0 {
		return err
	}
	place, placed, err := decisionPlace(ctx, r.db, decisionID)
	if err != nil {
		return err
	}
	job := "premise-flagged"
	for _, s := range open {
		edges, err := signalPlaceEdges(ctx, r.db, s.id)
		if err != nil {
			return err
		}
		var stale []string
		kept := false
		for _, e := range edges {
			if placed && !kept && e.RelationKind == place.kind && e.TargetID == place.targetID {
				kept = true
				continue
			}
			stale = append(stale, e.ID)
		}
		write := placed && !kept
		if len(stale) == 0 && !write {
			continue
		}
		var ended []RelationEdge
		var added RelationEdge
		err = runInTx(ctx, r.db, func(exec DB) error {
			now := time.Now().UTC()
			for _, id := range stale {
				e, ok, err := endEdgeRow(ctx, exec, id, now)
				if err != nil {
					return err
				}
				if ok {
					ended = append(ended, e)
				}
			}
			if !write {
				return nil
			}
			added, _, err = writeEdgeRow(ctx, exec, RelationEdge{SourceType: "Signal", SourceID: s.id, TargetType: place.targetType, TargetID: place.targetID,
				RelationKind: place.kind, EdgeClass: "asserted", CreatedByJob: &job, Attributes: []byte("{}")})
			return err
		})
		if err != nil {
			return err
		}
		if r.relations != nil {
			for _, e := range ended {
				r.relations.recordEdgeEnd(ctx, e, edgeEnding{cause: EdgeEndMoved, reason: "the Decision moved", actorKind: "job", actorID: job})
			}
			if write {
				r.relations.announceEdge(ctx, added, true)
			}
		}
	}
	return nil
}

// signalPlaceEdges returns a Signal's live place edges, oldest first.
func signalPlaceEdges(ctx context.Context, db DB, signalID string) ([]RelationEdge, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, relation_kind, target_id FROM smeldr_relations
		  WHERE source_type = 'Signal' AND source_id = $1
		    AND relation_kind IN ('belongs_to_organization', 'belongs_to_domain', 'belongs_to_area')
		    AND (invalid_at IS NULL OR invalid_at > $2)
		  ORDER BY created_at, id`, signalID, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RelationEdge
	for rows.Next() {
		var e RelationEdge
		if err := rows.Scan(&e.ID, &e.RelationKind, &e.TargetID); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// PremiseSweep is what one [App.SweepPremiseFlags] run did.
type PremiseSweep struct {
	Pairs    int `json:"pairs"`
	Opened   int `json:"opened"`
	Resolved int `json:"resolved"`
	Failed   int `json:"failed"`
}

// SweepPremiseFlags evaluates every premise pair (D149): each live
// grounded_in or addresses edge between Decisions, and each pair that ever
// opened the condition. It is the backstop for a change the event-driven
// evaluation missed; the structural sweep's schedule runs it. A pair that
// fails is counted and logged, and the sweep goes on. Reading the pairs
// fails as [ErrInternal].
func (a *App) SweepPremiseFlags(ctx context.Context) (PremiseSweep, error) {
	if a.cfg.DB == nil {
		return PremiseSweep{}, fmt.Errorf("%w: SweepPremiseFlags needs a database", ErrInternal)
	}
	pairs, err := premisePairs(ctx, a.cfg.DB,
		`SELECT source_id, target_id FROM smeldr_relations
		  WHERE source_type = 'Decision' AND target_type = 'Decision' AND relation_kind IN ('grounded_in', 'addresses')
		    AND (invalid_at IS NULL OR invalid_at > $1)
		 UNION
		 SELECT decision_id, premise_id FROM smeldr_premise_flags`, time.Now().UTC())
	if err != nil {
		return PremiseSweep{}, fmt.Errorf("%w: SweepPremiseFlags: %v", ErrInternal, err)
	}
	r := a.signalResolver()
	out := PremiseSweep{Pairs: len(pairs)}
	for _, p := range pairs {
		did, err := r.evaluatePremiseErr(ctx, p[0], p[1])
		switch {
		case err != nil:
			out.Failed++
			slog.WarnContext(ctx, "smeldr: SweepPremiseFlags: pair not evaluated", "decision", p[0], "premise", p[1], "error", err)
		case did == "opened":
			out.Opened++
		case did == "resolved":
			out.Resolved++
		}
	}
	// The open conditions follow their Decision's place (T925).
	decisions, err := premisePairs(ctx, a.cfg.DB,
		`SELECT DISTINCT subject_id, '' FROM smeldr_signals WHERE signal_type = $1 AND status = 'open'`, premiseFlagged)
	if err != nil {
		slog.WarnContext(ctx, "smeldr: SweepPremiseFlags: open conditions not read", "error", err)
		return out, nil
	}
	for _, d := range decisions {
		r.syncDecisionConditionPlaces(ctx, d[0])
	}
	return out, nil
}

// ConfirmPremise records that a Decision still holds although a premise it
// rests on is not in force (D149): it resolves the open premise-flagged
// condition of the pair with the reason, and the pair stays closed until the
// premise's state changes. It needs ratify over the Decision's place, so an
// agent or job token never confirms. Both Decisions are named by address,
// slug or ID and must be readable (else [ErrNotFound]); a missing reason is a
// validation error; a pair with no open condition is [ErrConflict].
func (a *App) ConfirmPremise(ctx context.Context, decision, premise, reason string) error {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return Err("reason", "is required: say why the Decision still holds")
	}
	if a.cfg.DB == nil {
		return fmt.Errorf("%w: ConfirmPremise needs a database", ErrInternal)
	}
	db := a.cfg.DB
	scope, err := scopeFor(ctx, a.governance)
	if err != nil {
		return err
	}
	_, decisionID, _, _, err := a.lookupCompiledRecord(ctx, "Decision", decision, scope)
	if err != nil {
		return err
	}
	_, premiseID, _, premiseState, err := a.lookupCompiledRecord(ctx, "Decision", premise, scope)
	if err != nil {
		return err
	}
	d, err := readPremiseDecision(ctx, db, decisionID)
	if err != nil {
		return fmt.Errorf("%w: read %s: %v", ErrInternal, decision, err)
	}
	label := d.label(decisionID)
	if err := requireOperationOver(ctx, a.governance, OperationRatify, AuthTarget{TypeName: "Decision", ID: decisionID},
		"confirming the premise of "+label, label, "D149"); err != nil {
		return err
	}
	row, err := readPremiseRow(ctx, db, decisionID, premiseID)
	if err != nil && !isNoSuchTable(err) {
		return fmt.Errorf("%w: the premise of %s: %v", ErrInternal, label, err)
	}
	r := a.signalResolver()
	var open []openSignal
	if row.signalID != "" {
		if open, err = r.selectOpen(ctx, "id = $1", row.signalID); err != nil {
			return fmt.Errorf("%w: the premise condition of %s: %v", ErrInternal, label, err)
		}
	}
	if len(open) == 0 {
		return fmt.Errorf("%w: %s has no open premise-flagged condition for that premise", ErrConflict, label)
	}
	return a.confirmPremisePair(ctx, decisionID, premiseID, premiseState, label, open, reason)
}

// confirmPremisePair records a confirmation of the pair and resolves its
// open condition, for both confirm forms. Nothing it writes or returns names
// the premise.
func (a *App) confirmPremisePair(ctx context.Context, decisionID, premiseID, premiseState, label string, open []openSignal, reason string) error {
	actor, _ := actorFromContext(ctx)
	if _, err := a.cfg.DB.ExecContext(ctx,
		`UPDATE smeldr_premise_flags SET confirmed_by = $1, confirmed_at = $2, confirmed_reason = $3, confirmed_premise_state = $4
		  WHERE decision_id = $5 AND premise_id = $6`,
		actor, time.Now().UTC(), reason, premiseState, decisionID, premiseID); err != nil {
		return fmt.Errorf("%w: record the confirmation of %s: %v", ErrInternal, label, err)
	}
	a.signalResolver().resolve(ctx, open, "confirmed by "+actor+": "+reason)
	return nil
}

// ConfirmPremiseCondition is [App.ConfirmPremise] by the condition: the
// caller names the open premise-flagged Signal (address, slug or ID) and
// never the premise, which it may not be able to read (T925). The Signal
// must be readable (it is, with its Decision, D113), else [ErrNotFound];
// a resolved one is [ErrConflict]. It needs ratify over the resting
// Decision's place and a reason, as ConfirmPremise does. No error, result
// or record it writes names the premise.
func (a *App) ConfirmPremiseCondition(ctx context.Context, condition, reason string) error {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return Err("reason", "is required: say why the Decision still holds")
	}
	if a.cfg.DB == nil {
		return fmt.Errorf("%w: ConfirmPremiseCondition needs a database", ErrInternal)
	}
	db := a.cfg.DB
	scope, err := scopeFor(ctx, a.governance)
	if err != nil {
		return err
	}
	notFound := fmt.Errorf("%w: no premise-flagged condition %q you can read", ErrNotFound, condition)
	_, signalID, _, status, err := a.lookupCompiledRecord(ctx, "Signal", condition, scope)
	if errors.Is(err, ErrNotFound) {
		return notFound
	}
	if err != nil {
		return err
	}
	if ok, err := newEndReader(ctx, db, scope).readable("Signal", signalID); err != nil {
		return fmt.Errorf("%w: read the condition %q: %v", ErrInternal, condition, err)
	} else if !ok {
		return notFound
	}
	var decisionID, premiseID string
	err = db.QueryRowContext(ctx, `SELECT decision_id, premise_id FROM smeldr_premise_flags WHERE signal_id = $1`, signalID).Scan(&decisionID, &premiseID)
	switch {
	case errors.Is(err, sql.ErrNoRows) || isNoSuchTable(err):
		return notFound
	case err != nil:
		return fmt.Errorf("%w: read the condition %q: %v", ErrInternal, condition, err)
	}
	d, err := readPremiseDecision(ctx, db, decisionID)
	if err != nil {
		return fmt.Errorf("%w: read the condition %q: %v", ErrInternal, condition, err)
	}
	label := d.label(decisionID)
	if err := requireOperationOver(ctx, a.governance, OperationRatify, AuthTarget{TypeName: "Decision", ID: decisionID},
		"confirming the premise of "+label, label, "D149"); err != nil {
		return err
	}
	if status != signalOpen {
		return fmt.Errorf("%w: the condition %q is %s, not open", ErrConflict, condition, status)
	}
	p, err := readPremiseDecision(ctx, db, premiseID)
	if err != nil {
		return fmt.Errorf("%w: read the condition %q: %v", ErrInternal, condition, err)
	}
	open, err := a.signalResolver().selectOpen(ctx, "id = $1", signalID)
	if err != nil {
		return fmt.Errorf("%w: read the condition %q: %v", ErrInternal, condition, err)
	}
	if len(open) == 0 {
		return fmt.Errorf("%w: the condition %q is no longer open", ErrConflict, condition)
	}
	return a.confirmPremisePair(ctx, decisionID, premiseID, p.state, label, open, reason)
}
