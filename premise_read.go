package smeldr

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// PremiseFlag is one premise-flagged pair of a resting Decision, as
// [App.PremiseFlags] returns it (D149, T1183): the condition that is open
// about it, the premise it rests on when the caller may read it, and the last
// confirmation that it still holds.
type PremiseFlag struct {
	// ConditionOpen reports whether a premise-flagged condition is open on
	// the pair. It is false once the pair was confirmed or its condition
	// resolved.
	ConditionOpen bool `json:"condition_open"`
	// Signal is the address of that open condition, set only when the caller
	// may read the Signal itself (D181 point 3): a reader of the resting
	// Decision does not read its Signals by that alone.
	Signal string `json:"signal,omitempty"`
	// Premise is the Decision this one rests on, set only when the caller may
	// read it: an unreadable premise is never named (D149).
	Premise *PremiseRef `json:"premise,omitempty"`
	// Confirmation is the last confirm_premise on the pair, or nil when it
	// was never confirmed.
	Confirmation *PremiseConfirmation `json:"confirmation,omitempty"`
}

// PremiseRef is the premise of a [PremiseFlag]: its address, its state, and
// whether it has a place (a live belongs_to_* edge).
type PremiseRef struct {
	Address string `json:"address"`
	State   string `json:"state"`
	Placed  bool   `json:"placed"`
}

// PremiseConfirmation is who confirmed a premise-flagged pair, when, why,
// and the premise's state at that moment. By and Class name the confirming
// identity as [App.ApproversOf] names a holder; both are empty when it has
// no live token left to name it by.
type PremiseConfirmation struct {
	By           string    `json:"by"`
	Class        string    `json:"class"`
	At           time.Time `json:"at"`
	Reason       string    `json:"reason"`
	PremiseState string    `json:"premise_state"`
}

// PremiseFlags returns the premise-flagged pairs of the resting Decision
// decision, by address, slug or ID (D149, T1183), oldest premise first. It
// answers only when the caller may read the Decision: an unknown or
// unreadable one is [ErrNotFound] alike. A pair whose premise the caller may
// not read is still listed, without its premise, so its open condition is
// not hidden but the premise is never named; likewise its open condition is
// reported, and named by address only to a caller who may read that Signal.
// An empty decision is
// [ErrBadRequest]; a failed read is [ErrInternal].
func (a *App) PremiseFlags(ctx context.Context, decision string) ([]PremiseFlag, error) {
	decision = strings.TrimSpace(decision)
	if decision == "" {
		return nil, problem(problemPremiseDecisionRequired, fmt.Errorf("%w: name the resting Decision by address, slug or ID", ErrBadRequest), nil)
	}
	if a.cfg.DB == nil {
		return nil, fmt.Errorf("%w: PremiseFlags needs a database", ErrInternal)
	}
	db := a.cfg.DB
	scope, err := scopeFor(ctx, a.governance)
	if err != nil {
		return nil, err
	}
	_, decisionID, _, _, err := a.lookupCompiledRecord(ctx, "Decision", decision, scope)
	if err != nil {
		return nil, err
	}
	out := []PremiseFlag{}
	if ok, err := tableExists(ctx, db, "smeldr_premise_flags"); err != nil {
		return nil, fmt.Errorf("%w: the premise flags of %s: %v", ErrInternal, decision, err)
	} else if !ok {
		return out, nil
	}
	type pairRow struct {
		premiseID, signalID, by, reason, state string
		at                                     any
	}
	rows, err := db.QueryContext(ctx,
		`SELECT premise_id, signal_id, confirmed_by, confirmed_at, confirmed_reason, confirmed_premise_state
		   FROM smeldr_premise_flags WHERE decision_id = $1 ORDER BY premise_id`, decisionID)
	if err != nil {
		return nil, fmt.Errorf("%w: the premise flags of %s: %v", ErrInternal, decision, err)
	}
	var pairs []pairRow
	for rows.Next() {
		var p pairRow
		if err := rows.Scan(&p.premiseID, &p.signalID, &p.by, &p.at, &p.reason, &p.state); err != nil {
			rows.Close()
			return nil, fmt.Errorf("%w: the premise flags of %s: %v", ErrInternal, decision, err)
		}
		pairs = append(pairs, p)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("%w: the premise flags of %s: %v", ErrInternal, decision, err)
	}
	rows.Close()
	var confirmers []string
	for _, p := range pairs {
		if p.at != nil && p.by != "" {
			confirmers = append(confirmers, p.by)
		}
	}
	names := map[string]liveToken{}
	if len(confirmers) > 0 {
		if names, err = a.liveTokens(ctx, confirmers); err != nil {
			return nil, err
		}
	}
	r := a.signalResolver()
	for _, p := range pairs {
		f := PremiseFlag{}
		if p.signalID != "" {
			open, err := r.selectOpen(ctx, "id = $1", p.signalID)
			if err != nil {
				return nil, fmt.Errorf("%w: the premise condition of %s: %v", ErrInternal, decision, err)
			}
			if len(open) > 0 {
				f.ConditionOpen = true
				if scope.Readable("Signal", p.signalID) {
					f.Signal = open[0].address
				}
			}
		}
		if scope.Readable("Decision", p.premiseID) {
			ref, err := premiseRefOf(ctx, db, p.premiseID)
			if err != nil {
				return nil, fmt.Errorf("%w: the premise of %s: %v", ErrInternal, decision, err)
			}
			f.Premise = ref
		}
		if p.at != nil {
			at, err := ParseTimestamp(p.at)
			if err != nil {
				return nil, fmt.Errorf("%w: the confirmation time of %s: %v", ErrInternal, decision, err)
			}
			tok := names[p.by]
			f.Confirmation = &PremiseConfirmation{By: tok.name, Class: tok.class, At: at, Reason: p.reason, PremiseState: p.state}
		}
		out = append(out, f)
	}
	return out, nil
}

// premiseRefOf reads a premise's address and state and whether it has a
// live place edge. A premise that no longer exists is nil.
func premiseRefOf(ctx context.Context, db DB, id string) (*PremiseRef, error) {
	d, err := readPremiseDecision(ctx, db, id)
	if err != nil || !d.found {
		return nil, err
	}
	var n int
	err = db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM smeldr_relations
		  WHERE source_type = 'Decision' AND source_id = $1
		    AND relation_kind IN ('belongs_to_organization', 'belongs_to_domain', 'belongs_to_area')
		    AND (invalid_at IS NULL OR invalid_at > $2)`, id, time.Now().UTC()).Scan(&n)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	return &PremiseRef{Address: d.address, State: d.state, Placed: n > 0}, nil
}
