// AGPL-3.0-or-later

package smeldr

import "context"

// Severity is a small, closed ordinal scale — not an open numeric score —
// matching this project's own established precedent for a governance-
// facing value meant to be compared and bucketed consistently everywhere
// it is read (the same reasoning [Reversibility] and [RuleTypeRank]'s own
// ordinal comparison already follow): a numeric score would leave every
// consumer (a cloud UI severity badge is the named one) to invent its own
// bucketing, risking a different cutoff in different places.
type Severity string

const (
	SeverityLow      Severity = "low"
	SeverityMedium   Severity = "medium"
	SeverityHigh     Severity = "high"
	SeverityCritical Severity = "critical"
)

// severityRank orders Severity values for comparison — lowest first.
// Unknown values (including "") rank lowest, never highest, matching
// this project's fail-closed-on-the-unknown posture (§7).
func severityRank(s Severity) int {
	switch s {
	case SeverityLow:
		return 0
	case SeverityMedium:
		return 1
	case SeverityHigh:
		return 2
	case SeverityCritical:
		return 3
	default:
		return 0
	}
}

// higherSeverity returns whichever of a, b ranks higher.
func higherSeverity(a, b Severity) Severity {
	if severityRank(b) > severityRank(a) {
		return b
	}
	return a
}

// fanoutSeverity buckets a [Reachability] result's own total item count
// (summed across every ring) into a [Severity]. Thresholds are a first
// cut, not derived from real usage data — this project has none yet for
// this specific signal (decision-governance-model's own §13 names the
// whole model as validated against a single-operator organization only).
// Adjust here, in one place, once real data justifies different cutoffs;
// callers never see or choose the thresholds themselves.
func fanoutSeverity(r *Reachability) Severity {
	total := 0
	for _, ring := range r.Rings {
		total += len(ring.Items)
	}
	switch {
	case total == 0:
		return SeverityLow
	case total < 5:
		return SeverityMedium
	case total < 20:
		return SeverityHigh
	default:
		return SeverityCritical
	}
}

// ruleTypeFloorSeverity buckets ruleType's own [RuleTypeRank] position,
// relative to the full currently-registered ordering, into a [Severity]
// floor — never the source of truth on its own (fanoutSeverity is), only
// a floor for exactly the case §3/§6 name: explicit relation edges are
// structurally sparse near the top of the rule-type hierarchy (nothing
// draws an edge to "the constitution" for every decision that implicitly
// depends on it), so pure fan-out systematically undercounts exposure
// exactly where the stakes are highest.
//
// Returns SeverityLow, true when ruleType is empty or unregistered (no
// floor to apply — never silently treated as the highest tier). ok is
// false only on a real query error.
func ruleTypeFloorSeverity(ctx context.Context, db DB, ruleType string) (Severity, error) {
	if ruleType == "" {
		return SeverityLow, nil
	}
	rank, ok, err := RuleTypeRank(ctx, db, ruleType)
	if err != nil {
		return SeverityLow, err
	}
	if !ok {
		return SeverityLow, nil
	}
	total, err := ruleTypeRankCount(ctx, db)
	if err != nil {
		return SeverityLow, err
	}
	if total <= 1 {
		// A single registered rule type has nothing to be relatively
		// weaker than — treat it as the strongest tier registered.
		return SeverityCritical, nil
	}
	// Relative position within the currently-registered ordering:
	// 0.0 = weakest registered rank, 1.0 = strongest. The ordering's own
	// vocabulary and depth are organization-defined (§3), so an absolute
	// rank number carries no fixed meaning across organizations — only
	// its position relative to the full registered set does.
	position := float64(rank) / float64(total-1)
	switch {
	case position >= 0.75:
		return SeverityCritical, nil
	case position >= 0.5:
		return SeverityHigh, nil
	case position >= 0.25:
		return SeverityMedium, nil
	default:
		return SeverityLow, nil
	}
}

// ruleTypeRankCount returns the number of currently-registered RuleType
// ranks ([SetRuleTypeOrder]'s own full-replace ordering).
func ruleTypeRankCount(ctx context.Context, db DB) (int, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM smeldr_rule_type_ranks`).Scan(&n)
	return n, err
}

// SeverityOf computes a [Severity] for anchorType/anchorID: the higher of
// (a) [RelationStore.Reachability]'s own real graph fan-out, bucketed via
// fanoutSeverity, and (b) ruleType's own [RuleTypeRank] position, used
// only as a floor — never a substitute for (a) — matching decision-
// governance §3/§6's own explicit reasoning (a locally-scoped decision
// can carry enormous real consequence; a foundational one can have
// negligible current fan-out simply because nothing has been built
// against it yet).
//
// ruleType is the anchor's own RuleType value (e.g. a [Decision]'s or
// [Rule]'s RuleType field) — SeverityOf does not look it up itself, since
// not every anchor type carries one; pass "" when the anchor has none or
// its own RuleType is unset.
func SeverityOf(ctx context.Context, db DB, rs *RelationStore, anchorType, anchorID, ruleType, kind, direction string, maxDepth int) (Severity, error) {
	reach, err := rs.Reachability(ctx, anchorType, anchorID, kind, direction, maxDepth)
	if err != nil {
		return "", err
	}
	fanout := fanoutSeverity(reach)

	floor, err := ruleTypeFloorSeverity(ctx, db, ruleType)
	if err != nil {
		return "", err
	}

	return higherSeverity(fanout, floor), nil
}
