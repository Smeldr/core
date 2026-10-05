// AGPL-3.0-or-later

package smeldr

import "context"

// applyConflictPolicy is the plan, the losers' writes and the effects on one
// handle, in one call, for tests that exercise the policy on its own with no
// winning write in between. Production code does not use it: it calls
// [planConflict] before the winner's own write and the plan's methods after it.
func applyConflictPolicy(ctx context.Context, db DB, rs *RelationStore, prov ProvenanceStore, typeName, toState, newItemID, surface string) error {
	plan, err := planConflict(ctx, db, typeName, toState, newItemID)
	if err != nil {
		return err
	}
	plan.run(ctx, db, rs, prov, surface)
	return nil
}

// conflictSupersede builds the plan for an explicit active state and table, and
// runs it, for tests that start from the supersede step.
func conflictSupersede(ctx context.Context, db DB, rs *RelationStore, prov ProvenanceStore, typeName, activeState, newItemID, surface, table string, isDynamic bool) error {
	ids, err := conflictIDs(ctx, db, typeName, activeState, table, isDynamic)
	if err != nil {
		return nil // fail-open
	}
	var losers []string
	for _, id := range ids {
		if id != newItemID {
			losers = append(losers, id)
		}
	}
	if len(losers) == 0 {
		return nil
	}
	plan := &conflictPlan{typeName: typeName, activeState: activeState, newItemID: newItemID, table: table, isDynamic: isDynamic, losers: losers}
	plan.run(ctx, db, rs, prov, surface)
	return nil
}
