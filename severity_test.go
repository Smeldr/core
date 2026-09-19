package smeldr

import (
	"context"
	"testing"
)

func TestFanoutSeverity_Buckets(t *testing.T) {
	tests := []struct {
		name  string
		items int
		want  Severity
	}{
		{"zero", 0, SeverityLow},
		{"one", 1, SeverityMedium},
		{"four", 4, SeverityMedium},
		{"five", 5, SeverityHigh},
		{"nineteen", 19, SeverityHigh},
		{"twenty", 20, SeverityCritical},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			items := make([]ReachabilityItem, tt.items)
			r := &Reachability{Rings: []ReachabilityRing{{Depth: 1, Items: items}}}
			got := fanoutSeverity(r)
			if got != tt.want {
				t.Errorf("fanoutSeverity(%d items) = %q, want %q", tt.items, got, tt.want)
			}
		})
	}
}

func TestFanoutSeverity_MultipleRingsSummed(t *testing.T) {
	r := &Reachability{Rings: []ReachabilityRing{
		{Depth: 1, Items: make([]ReachabilityItem, 3)},
		{Depth: 2, Items: make([]ReachabilityItem, 3)},
	}}
	// 6 total items, across two rings — must sum, not use only one ring.
	if got := fanoutSeverity(r); got != SeverityHigh {
		t.Errorf("fanoutSeverity = %q, want %q (6 items summed across rings)", got, SeverityHigh)
	}
}

func setupRuleTypeRankDB(t *testing.T) DB {
	t.Helper()
	db := newSQLiteDB(t)
	if err := CreateRuleTypeRankTable(db); err != nil {
		t.Fatalf("CreateRuleTypeRankTable: %v", err)
	}
	return db
}

func TestRuleTypeFloorSeverity_EmptyRuleType_Low(t *testing.T) {
	db := setupRuleTypeRankDB(t)
	got, err := ruleTypeFloorSeverity(context.Background(), db, "")
	if err != nil {
		t.Fatalf("ruleTypeFloorSeverity: %v", err)
	}
	if got != SeverityLow {
		t.Errorf("got %q, want %q", got, SeverityLow)
	}
}

func TestRuleTypeFloorSeverity_UnregisteredRuleType_Low(t *testing.T) {
	db := setupRuleTypeRankDB(t)
	if err := SetRuleTypeOrder(context.Background(), db, []string{"process-precedent", "constitution"}); err != nil {
		t.Fatalf("SetRuleTypeOrder: %v", err)
	}
	got, err := ruleTypeFloorSeverity(context.Background(), db, "never-registered")
	if err != nil {
		t.Fatalf("ruleTypeFloorSeverity: %v", err)
	}
	if got != SeverityLow {
		t.Errorf("got %q, want %q — an unregistered RuleType is never silently treated as a real floor", got, SeverityLow)
	}
}

func TestRuleTypeFloorSeverity_SingleRegistered_Critical(t *testing.T) {
	db := setupRuleTypeRankDB(t)
	if err := SetRuleTypeOrder(context.Background(), db, []string{"constitution"}); err != nil {
		t.Fatalf("SetRuleTypeOrder: %v", err)
	}
	got, err := ruleTypeFloorSeverity(context.Background(), db, "constitution")
	if err != nil {
		t.Fatalf("ruleTypeFloorSeverity: %v", err)
	}
	if got != SeverityCritical {
		t.Errorf("got %q, want %q — the only registered rank is the strongest by definition", got, SeverityCritical)
	}
}

func TestRuleTypeFloorSeverity_RelativePosition(t *testing.T) {
	db := setupRuleTypeRankDB(t)
	// Weakest to strongest, matching SetRuleTypeOrder's own convention.
	names := []string{"process-precedent", "design-system", "architecture", "constitution"}
	if err := SetRuleTypeOrder(context.Background(), db, names); err != nil {
		t.Fatalf("SetRuleTypeOrder: %v", err)
	}
	tests := []struct {
		ruleType string
		want     Severity
	}{
		{"process-precedent", SeverityLow}, // position 0/3 = 0.0
		{"design-system", SeverityMedium},  // position 1/3 = 0.33
		{"architecture", SeverityHigh},     // position 2/3 = 0.67
		{"constitution", SeverityCritical}, // position 3/3 = 1.0
	}
	for _, tt := range tests {
		t.Run(tt.ruleType, func(t *testing.T) {
			got, err := ruleTypeFloorSeverity(context.Background(), db, tt.ruleType)
			if err != nil {
				t.Fatalf("ruleTypeFloorSeverity: %v", err)
			}
			if got != tt.want {
				t.Errorf("ruleTypeFloorSeverity(%q) = %q, want %q", tt.ruleType, got, tt.want)
			}
		})
	}
}

func TestSeverityOf_FanoutWins(t *testing.T) {
	rs := setupRelationStore(t)
	ctx := context.Background()
	if err := rs.UpsertKind(ctx, RelationKindDef{TypeName: "depends_on", Mode: "asserted"}); err != nil {
		t.Fatalf("UpsertKind: %v", err)
	}
	// Large real fan-out, no RuleType — fan-out alone must drive severity.
	for i := 0; i < 20; i++ {
		if err := rs.Assert(ctx, RelationEdge{
			SourceType: "Decision", SourceID: "anchor",
			TargetType: "Task", TargetID: idFor(i),
			RelationKind: "depends_on", EdgeClass: "asserted",
		}); err != nil {
			t.Fatalf("Assert %d: %v", i, err)
		}
	}
	got, err := SeverityOf(ctx, rs.db, rs, "Decision", "anchor", "", "depends_on", "outgoing", 1)
	if err != nil {
		t.Fatalf("SeverityOf: %v", err)
	}
	if got != SeverityCritical {
		t.Errorf("SeverityOf = %q, want %q (fan-out of 20)", got, SeverityCritical)
	}
}

func TestSeverityOf_RuleTypeFloorWins(t *testing.T) {
	rs := setupRelationStore(t)
	ctx := context.Background()
	if err := CreateRuleTypeRankTable(rs.db); err != nil {
		t.Fatalf("CreateRuleTypeRankTable: %v", err)
	}
	if err := SetRuleTypeOrder(ctx, rs.db, []string{"process-precedent", "constitution"}); err != nil {
		t.Fatalf("SetRuleTypeOrder: %v", err)
	}
	if err := rs.UpsertKind(ctx, RelationKindDef{TypeName: "depends_on", Mode: "asserted"}); err != nil {
		t.Fatalf("UpsertKind: %v", err)
	}
	// Zero fan-out (nothing built against it yet) but a foundational
	// RuleType — the exact §3/§6 scenario: the floor must win.
	got, err := SeverityOf(ctx, rs.db, rs, "Decision", "anchor", "constitution", "depends_on", "outgoing", 1)
	if err != nil {
		t.Fatalf("SeverityOf: %v", err)
	}
	if got != SeverityCritical {
		t.Errorf("SeverityOf = %q, want %q (rule-type floor, zero fan-out)", got, SeverityCritical)
	}
}

func TestSeverityOf_BothAgree(t *testing.T) {
	rs := setupRelationStore(t)
	ctx := context.Background()
	if err := CreateRuleTypeRankTable(rs.db); err != nil {
		t.Fatalf("CreateRuleTypeRankTable: %v", err)
	}
	got, err := SeverityOf(ctx, rs.db, rs, "Decision", "anchor", "", "depends_on", "outgoing", 1)
	if err != nil {
		t.Fatalf("SeverityOf: %v", err)
	}
	if got != SeverityLow {
		t.Errorf("SeverityOf = %q, want %q (zero fan-out, no RuleType)", got, SeverityLow)
	}
}

func TestSeverityRank_UnknownValue(t *testing.T) {
	if got := severityRank(Severity("bogus")); got != 0 {
		t.Errorf("severityRank(unknown) = %d, want 0 (never ranks above a known value)", got)
	}
}

func TestSeverityOf_ReachabilityError(t *testing.T) {
	rs := setupRelationStore(t)
	// maxDepth 0 is invalid — Reachability returns ErrBadRequest before
	// SeverityOf ever reaches the rule-type floor lookup.
	if _, err := SeverityOf(context.Background(), rs.db, rs, "Decision", "anchor", "", "depends_on", "outgoing", 0); err == nil {
		t.Error("expected an error from an invalid maxDepth, got nil")
	}
}

func idFor(i int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz"
	return "t-" + string(letters[i%len(letters)]) + string(rune('0'+i/len(letters)))
}
