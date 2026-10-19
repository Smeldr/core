//go:build integration

package pgx

import (
	"context"
	"testing"

	smeldr "smeldr.dev/core"
)

// T886 on Postgres: a coverage rule on an Area, the resolution from paths to
// the Decisions in force, and the coverage-matches-nothing condition.
func TestPG_CoverageRules(t *testing.T) {
	db, _ := isolatedDB(t)
	ctx := context.Background()
	app, store := newPGApp(t, db)
	for _, create := range []func(smeldr.DB) error{smeldr.CreateGovernanceTables, smeldr.CreateRelationTables} {
		if err := create(db); err != nil {
			t.Fatal(err)
		}
	}
	smeldr.RegisterGovernanceTypes(app, db)
	rels, err := smeldr.NewRelationStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := smeldr.RegisterGovernanceRelationKinds(ctx, rels); err != nil {
		t.Fatal(err)
	}
	app.Relations(rels)
	_ = app.Handler()
	if _, err := app.EnsureOrganization(ctx, "Acme"); err != nil {
		t.Fatal(err)
	}
	var domainID string
	if err := db.QueryRowContext(ctx, `SELECT id FROM smeldr_domains WHERE slug = 'general'`).Scan(&domainID); err != nil {
		t.Fatal(err)
	}
	module := func(name string) smeldr.MCPModule {
		for _, m := range app.MCPModules() {
			if m.MCPMeta().TypeName == name {
				return m
			}
		}
		t.Fatalf("no module %s", name)
		return nil
	}
	seed := smeldr.TrustedRead(smeldr.NewTestContext(smeldr.User{ID: "seed", Roles: []smeldr.Role{smeldr.Editor}})).(smeldr.Context)
	out, err := module("Area").MCPCreate(seed, map[string]any{"name": "Billing"})
	if err != nil {
		t.Fatal(err)
	}
	area := out.(*smeldr.Area)
	if err := rels.Assert(ctx, smeldr.RelationEdge{SourceType: "Area", SourceID: area.ID, TargetType: "Domain", TargetID: domainID,
		RelationKind: "belongs_to_domain", EdgeClass: "asserted", Attributes: []byte("{}")}); err != nil {
		t.Fatal(err)
	}
	for _, pattern := range []string{"internal/billing/**", "legacy/**"} {
		if _, err := module("CoverageRule").MCPCreate(seed, map[string]any{"area": area.Address, "repository": "Acme/App", "pattern": pattern}); err != nil {
			t.Fatalf("rule %s: %v", pattern, err)
		}
	}

	// A Decision placed in the Area and ratified by an approver.
	decisions := smeldr.NewSQLRepo[*smeldr.Decision](db, smeldr.Table("smeldr_decisions"))
	if err := decisions.Save(ctx, &smeldr.Decision{Node: smeldr.Node{ID: "d1", Slug: "d1", Status: "proposed"}, Title: "Money in cents"}); err != nil {
		t.Fatal(err)
	}
	if err := rels.Assert(ctx, smeldr.RelationEdge{SourceType: "Decision", SourceID: "d1", TargetType: "Area", TargetID: area.ID,
		RelationKind: "belongs_to_area", EdgeClass: "asserted", Attributes: []byte("{}")}); err != nil {
		t.Fatal(err)
	}
	if err := smeldr.RegisterApproverRole(ctx, store); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Grant(ctx, smeldr.RoleGrant{TokenID: "approver-1", RoleName: smeldr.RoleApprover}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.TransitionItem(smeldr.TrustedRead(smeldr.NewTestContext(smeldr.User{ID: "approver-1"})), "Decision", "d1", "ratified"); err != nil {
		t.Fatalf("ratify: %v", err)
	}

	res, err := app.DecisionsForPaths(seed, smeldr.DecisionsForPathsQuery{Repository: "acme/app", Paths: []string{"internal/billing/x.go", "docs/a.md"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Areas) != 1 || res.Areas[0].ID != area.ID || len(res.Decisions) != 1 || res.Decisions[0].ID != "d1" ||
		res.Decisions[0].Standing != smeldr.StandingHolds || len(res.Uncovered) != 1 {
		t.Errorf("resolution = %+v", res)
	}

	for i := 0; i < 2; i++ {
		check, err := app.CheckCoverage(seed, smeldr.CheckCoverageQuery{Repository: "acme/app", Paths: []string{"internal/billing/x.go", "README.md"}})
		if err != nil {
			t.Fatal(err)
		}
		if len(check.Rules) != 2 || check.Rules[0].Matches != 1 || len(check.MatchingNothing) != 1 {
			t.Errorf("check = %+v", check)
		}
	}
	var open int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM smeldr_signals WHERE signal_type = 'coverage-matches-nothing' AND status = 'open'`).Scan(&open); err != nil || open != 1 {
		t.Errorf("open conditions = %d (%v), want 1", open, err)
	}
	if _, err := app.CheckCoverage(seed, smeldr.CheckCoverageQuery{Repository: "acme/app", Paths: []string{"legacy/old.go"}}); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM smeldr_signals WHERE signal_type = 'coverage-matches-nothing' AND status = 'open'`).Scan(&open); err != nil || open != 1 {
		// legacy matches now (resolved), billing matches nothing (opened).
		t.Errorf("open conditions after the second tree = %d (%v), want 1", open, err)
	}

	// T901: a changed pattern clears the count and the checked time.
	var slug string
	if err := db.QueryRowContext(ctx, `SELECT slug FROM smeldr_coverage_rules WHERE pattern = 'legacy/**'`).Scan(&slug); err != nil {
		t.Fatal(err)
	}
	if _, err := module("CoverageRule").MCPUpdate(seed, slug, map[string]any{"pattern": "legacy/*.go"}); err != nil {
		t.Fatal(err)
	}
	var count int
	var checked *string
	if err := db.QueryRowContext(ctx, `SELECT match_count, checked_at::text FROM smeldr_coverage_rules WHERE slug = $1`, slug).Scan(&count, &checked); err != nil || count != 0 || checked != nil {
		t.Errorf("after a pattern change: count %d, checked %v (%v); want 0 and not checked", count, checked, err)
	}
}
