package main

import (
	"context"
	"database/sql"
	"testing"

	smeldr "smeldr.dev/core"
)

// TestRuleTypeRankTable (T949): with ENABLE_AUTHORITY the rule type rank
// table exists, so SeverityOf floors a Decision by its rule type instead of
// failing; a second boot keeps the ordering.
func TestRuleTypeRankTable(t *testing.T) {
	cfg := ServerConfig{Secret: "test-secret-that-is-at-least-32-bytes", BaseURL: "https://example.com", InstanceName: "test",
		EnableAuthority: true, EnableGovernanceRecords: true, EnableRelations: true}
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()
	res, err := buildApp(cfg, db)
	if err != nil {
		t.Fatal(err)
	}
	defer res.StopAll()
	ctx := context.Background()
	if err := smeldr.SetRuleTypeOrder(ctx, db, []string{"guideline", "constitution"}); err != nil {
		t.Fatalf("SetRuleTypeOrder: %v", err)
	}
	severity := func(app *smeldr.App) (smeldr.Severity, error) {
		return smeldr.SeverityOf(ctx, db, app.RelationStore(), "Decision", "d1", "constitution", "grounded_in", "outgoing", 1)
	}
	if got, err := severity(res.App); err != nil || got != smeldr.SeverityCritical {
		t.Errorf("severity = %q %v, want the floor %q", got, err, smeldr.SeverityCritical)
	}
	again, err := buildApp(cfg, db)
	if err != nil {
		t.Fatalf("second boot: %v", err)
	}
	defer again.StopAll()
	if got, err := severity(again.App); err != nil || got != smeldr.SeverityCritical {
		t.Errorf("after a second boot: severity = %q %v", got, err)
	}

	// Without ENABLE_AUTHORITY there is no table, and the same call fails:
	// the gap this closes.
	plain, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	plain.SetMaxOpenConns(1)
	defer plain.Close()
	cfg.EnableAuthority = false
	other, err := buildApp(cfg, plain)
	if err != nil {
		t.Fatal(err)
	}
	defer other.StopAll()
	if _, err := smeldr.SeverityOf(ctx, plain, other.App.RelationStore(), "Decision", "d1", "constitution", "grounded_in", "outgoing", 1); err == nil {
		t.Error("SeverityOf without the table did not fail; the test no longer shows the gap")
	}
}
