// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"fmt"
	"log/slog"
)

// DefaultTensionThreshold is the number of ratified Decisions declaring
// tension against the same Rule (via [Decision.TensionRuleID]) that
// triggers a "declared-tension" [Finding] (decision-governance §6). A
// package-level var, not a DB-configurable per-rule-type table: this
// project has no real usage data yet to justify per-rule-type tuning
// (decision-governance-model's own §13 names the whole model as
// validated against a single-operator organization only) — override in
// Go if the default proves wrong in practice, matching how
// [Decision.RuleType]/[Decision.Reversibility] themselves shipped as
// fields-only first, with enforcement/configurability added later once a
// real need showed up (A304).
var DefaultTensionThreshold = 3

// recordDeclaredTension counts ratified Decisions sharing tensionRuleID
// and, at or past [DefaultTensionThreshold], records a "declared-tension"
// Finding for the Rule (decision-governance §6) — the same detector-owned,
// deduplicated-on-(Detector,SubjectType,SubjectID) primitive
// [RelationStore.SweepStructural]'s own onStale callback already uses
// (D51), applied one level up: from noticing one item's relation gone
// stale, to noticing that many ratified decisions consistently contradict
// the same rule. A no-op when store is nil — same "no store configured,
// no behaviour change" posture as SweepStructural's own callback.
//
// Only ratified decisions count — "only ratified, explicitly reasoned
// deviations ever count as evidence" (§6); this function is called only
// from the proposed→ratified transition, never on save or on a silent
// working draft, so no separate ratified-only filter is needed beyond the
// COUNT query's own WHERE clause (a decision that is later superseded
// still counted as evidence at the moment it was ratified — reversing
// that is a future task's own scope, not assumed here).
func recordDeclaredTension(ctx context.Context, db DB, store FindingStore, tensionRuleID string) {
	if store == nil || tensionRuleID == "" {
		return
	}
	var count int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM smeldr_decisions WHERE tension_rule_id = $1 AND status = 'ratified'`,
		tensionRuleID,
	).Scan(&count); err != nil {
		slog.WarnContext(ctx, "smeldr: recordDeclaredTension: count query failed",
			"tension_rule_id", tensionRuleID, "error", err)
		return
	}
	if count < DefaultTensionThreshold {
		return
	}
	if err := store.Record(ctx, Finding{
		Detector:    "declared-tension",
		SubjectType: "Rule",
		SubjectID:   tensionRuleID,
		Provenance:  "asserted",
		Message: fmt.Sprintf("%d ratified decisions declare tension against this rule",
			count),
	}); err != nil {
		slog.WarnContext(ctx, "smeldr: recordDeclaredTension: Finding record failed",
			"tension_rule_id", tensionRuleID, "error", err)
	}
}

// runDeclaredTensionAggregation is [Module.updateHandler]'s own wiring of
// §6's Propagate mechanism: a no-op unless item is a *Decision
// transitioning proposed→ratified. Mirrors runDecisionAuthorityCheck's
// exact dual-wiring shape (check.go) — deliberately both paths, same
// reasoning: TransitionItem (state.go) and updateHandler (module.go) are
// two separate write paths to the same transition.
func runDeclaredTensionAggregation(ctx context.Context, db DB, store FindingStore, item any, fromStatus, toStatus string) {
	if store == nil || fromStatus != "proposed" || toStatus != "ratified" {
		return
	}
	d, ok := item.(*Decision)
	if !ok {
		return
	}
	recordDeclaredTension(ctx, db, store, d.TensionRuleID)
}

// runDeclaredTensionAggregationByID is [App.TransitionItem]'s own wiring
// of §6, mirroring runDecisionAuthorityCheckByID's exact shape
// (check.go): TransitionItem never decodes a full Decision struct, so
// TensionRuleID is fetched directly by id.
func runDeclaredTensionAggregationByID(ctx context.Context, db DB, store FindingStore, typeName, itemID, fromStatus, toStatus string) {
	if store == nil || typeName != "Decision" || fromStatus != "proposed" || toStatus != "ratified" {
		return
	}
	var tensionRuleID string
	if err := db.QueryRowContext(ctx,
		`SELECT tension_rule_id FROM smeldr_decisions WHERE id = $1`, itemID,
	).Scan(&tensionRuleID); err != nil {
		slog.WarnContext(ctx, "smeldr: runDeclaredTensionAggregationByID: read tension_rule_id failed",
			"subject_id", itemID, "error", err)
		return
	}
	recordDeclaredTension(ctx, db, store, tensionRuleID)
}
