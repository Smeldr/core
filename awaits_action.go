package smeldr

import (
	"context"
	"fmt"
)

// StatesAwaitingAction returns the names of typeName's flow states that declare
// [State.AwaitsAction] (D109: "open", waiting for someone to act), resolved the
// same way a transition is: the type's own registered flow, else the default
// flow. found reports whether any flow resolved; when it is false the type's
// items have no answer, and a reader must show "unknown", never "closed". With
// a flow found, a state missing from the returned set is closed, including a
// state the flow does not name.
//
// An instance whose smeldr_states predates the awaits_action column (not yet
// booted on this version) answers found with an empty set: every state closed,
// which is D109's rule for an undeclared state. Missing flow tables answer
// found false. Any other database error is returned.
//
// It is one query per call; callers that read a page of items call it once per
// type and derive each item's answer from its status.
func StatesAwaitingAction(ctx context.Context, db DB, typeName string) (states map[string]bool, found bool, err error) {
	if db == nil {
		return nil, false, nil
	}
	flowID, found, err := resolveFlowID(ctx, db, typeName)
	if err != nil {
		if isNoSuchTable(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("smeldr: StatesAwaitingAction: resolve flow: %w", err)
	}
	if !found {
		return nil, false, nil
	}
	rows, err := db.QueryContext(ctx,
		`SELECT name FROM smeldr_states WHERE flow_id = $1 AND awaits_action = TRUE`, flowID)
	if err != nil {
		if isNoSuchColumn(err, "awaits_action") {
			return map[string]bool{}, true, nil
		}
		return nil, false, fmt.Errorf("smeldr: StatesAwaitingAction: %w", err)
	}
	defer rows.Close()
	states = map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, false, fmt.Errorf("smeldr: StatesAwaitingAction: scan: %w", err)
		}
		states[name] = true
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("smeldr: StatesAwaitingAction: %w", err)
	}
	return states, true, nil
}
