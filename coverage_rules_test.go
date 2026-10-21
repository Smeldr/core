package smeldr

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// T886: coverage rules, the resolution from paths to Decisions, and the
// coverage-matches-nothing condition.

func TestCoveragePattern(t *testing.T) {
	for _, tc := range []struct {
		pattern, path string
		want          bool
	}{
		{"README.md", "README.md", true},
		{"README.md", "docs/README.md", false},
		{"*.md", "README.md", true},
		{"*.md", "docs/README.md", false},
		{"docs/*.md", "docs/a.md", true},
		{"docs/?.md", "docs/a.md", true},
		{"docs/?.md", "docs/ab.md", false},
		{"internal/billing/**", "internal/billing/a.go", true},
		{"internal/billing/**", "internal/billing/x/y/z.go", true},
		{"internal/billing/**", "internal/billing", true},
		{"internal/billing/**", "internal/billings/a.go", false},
		{"**/main.go", "main.go", true},
		{"**/main.go", "cmd/a/b/main.go", true},
		{"cmd/*/main.go", "cmd/a/main.go", true},
		{"cmd/*/main.go", "cmd/a/b/main.go", false},
		{"a/**/b/**/c", "a/b/c", true},
		{"a/**/b/**/c", "a/x/b/y/z/c", true},
		{"a/**/b/**/c", "a/x/y/c", false},
		{"*_test.go", "x_test.go", true},
		{"*a*b", "xaxxb", true},
		{"*a*b", "xaxxbc", false},
	} {
		c := compileCoveragePattern(tc.pattern)
		if got := c.match(tc.path, strings.Split(tc.path, "/")); got != tc.want {
			t.Errorf("%q against %q = %v, want %v", tc.pattern, tc.path, got, tc.want)
		}
	}
}

func TestCoveragePattern_Checks(t *testing.T) {
	for _, p := range []string{"", "/abs", `a\b`, "a//b", "a/", "../x", "a/../b", "a/x**/b", strings.Repeat("a", 513)} {
		if err := checkCoveragePattern(p); err == nil {
			t.Errorf("pattern %q accepted", p)
		}
	}
	for _, p := range []string{"a", "a/**", "**", "a/*.go"} {
		if err := checkCoveragePattern(p); err != nil {
			t.Errorf("pattern %q: %v", p, err)
		}
	}
	if r, err := normalizeRepository(" Acme/Billing "); err != nil || r != "acme/billing" {
		t.Errorf("repository = %q, %v", r, err)
	}
	for _, r := range []string{"", "acme", "acme/billing/x", "acme billing/x"} {
		if _, err := normalizeRepository(r); err == nil {
			t.Errorf("repository %q accepted", r)
		}
	}
	if got := normalizePaths([]string{" ./a ", "/a", "", "b", "a"}); strings.Join(got, ",") != "a,b" {
		t.Errorf("normalizePaths = %v", got)
	}
}

// coverageFixture is an Organization with the General Domain, two Areas in
// it, rules in acme/app and Decisions placed at every level.
type coverageFixture struct {
	app                     *App
	db                      *sql.DB
	rs                      *RoleStore
	rules                   *Module[*CoverageRule]
	areas                   *Module[*Area]
	domain, org             string
	a1, a2                  *Area
	dArea, dDomain, dOrg    string
	ruleBilling, ruleMain   *CoverageRule
	ruleReadme, ruleNothing *CoverageRule
}

func newCoverageFixture(t testing.TB) coverageFixture {
	t.Helper()
	ctx := context.Background()
	app, db, rels := placesApp(t)
	rs := NewRoleStore(db)
	if err := app.Governance(rs); err != nil {
		t.Fatal(err)
	}
	if err := RegisterScopedReaderRole(ctx, rs); err != nil {
		t.Fatal(err)
	}
	// The seed author is an approver: rules are added and changed under
	// approve-change and ratify over their Area (D146).
	if err := RegisterApproverRole(ctx, rs); err != nil {
		t.Fatal(err)
	}
	if _, err := rs.Grant(ctx, RoleGrant{TokenID: "u-author", RoleName: RoleApprover}); err != nil {
		t.Fatal(err)
	}
	_ = app.Handler()
	org, err := app.EnsureOrganization(ctx, "Acme")
	if err != nil {
		t.Fatal(err)
	}
	f := coverageFixture{app: app, db: db, rs: rs, org: org.ID, rules: findModule[*CoverageRule](t, app), areas: findModule[*Area](t, app)}
	if err := db.QueryRow(`SELECT id FROM smeldr_domains WHERE slug = 'general'`).Scan(&f.domain); err != nil {
		t.Fatal(err)
	}
	seed := TrustedRead(authorCtx()).(Context)
	area := func(name string) *Area {
		out, err := f.areas.MCPCreate(seed, map[string]any{"name": name})
		if err != nil {
			t.Fatal(err)
		}
		a := asItem[*Area](out)
		if err := rels.Assert(ctx, RelationEdge{SourceType: "Area", SourceID: a.ID, TargetType: "Domain", TargetID: f.domain,
			RelationKind: "belongs_to_domain", EdgeClass: "asserted", Attributes: []byte("{}")}); err != nil {
			t.Fatal(err)
		}
		return a
	}
	f.a1, f.a2 = area("Billing"), area("Commands")
	rule := func(a *Area, repo, pattern string) *CoverageRule {
		out, err := f.rules.MCPCreate(seed, map[string]any{"area": a.Address, "repository": repo, "pattern": pattern})
		if err != nil {
			t.Fatalf("create rule %s: %v", pattern, err)
		}
		return asItem[*CoverageRule](out)
	}
	f.ruleBilling = rule(f.a1, "Acme/App", "internal/billing/**")
	f.ruleReadme = rule(f.a1, "acme/app", "README.md")
	f.ruleMain = rule(f.a2, "acme/app", "cmd/*/main.go")
	f.ruleNothing = rule(f.a2, "acme/app", "legacy/**")
	rule(f.a1, "acme/other", "**") // another repository
	no := 0
	decision := func(id, status, standing, kind, targetType, target string) string {
		no++
		if _, err := db.Exec(`INSERT INTO smeldr_decisions (id, slug, status, title, created_at, updated_at, address, address_no)
			VALUES ($1, $1, $2, $3, datetime('now'), datetime('now'), $4, $5)`, id, status, "Decision "+id, fmt.Sprintf("D%d", 900+no), 900+no); err != nil {
			t.Fatal(err)
		}
		if standing != "" {
			if _, err := db.Exec(`INSERT INTO smeldr_standing (subject_type, subject_id, standing) VALUES ('Decision', $1, $2)`, id, standing); err != nil {
				t.Fatal(err)
			}
		}
		placeEdge(t, db, id, targetType, target, kind, "asserted", nil)
		return id
	}
	f.dArea = decision("dec-area", "ratified", "holds", "belongs_to_area", "Area", f.a1.ID)
	f.dDomain = decision("dec-domain", "pending-re-evaluation", "holds", "belongs_to_domain", "Domain", f.domain)
	f.dOrg = decision("dec-org", "ratified", "holds", "belongs_to_organization", "Organization", f.org)
	decision("dec-proposed", "proposed", "", "belongs_to_area", "Area", f.a1.ID)
	decision("dec-superseded", "superseded", "ceased", "belongs_to_area", "Area", f.a2.ID)
	return f
}

func decisionIDs(ds []AppliedDecision) string {
	var ids []string
	for _, d := range ds {
		ids = append(ids, d.ID+"@"+d.Place+":"+strings.Join(d.Areas, "+"))
	}
	return strings.Join(ids, ",")
}

func TestDecisionsForPaths(t *testing.T) {
	f := newCoverageFixture(t)
	ctx := TrustedRead(authorCtx())
	paths := []string{"internal/billing/a.go", "README.md", "cmd/x/main.go", "docs/x.md", "./README.md"}
	res, err := f.app.DecisionsForPaths(ctx, DecisionsForPathsQuery{Repository: "ACME/app", Paths: paths})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Areas) != 2 || res.Areas[0].ID != f.a1.ID || strings.Join(res.Areas[0].Paths, ",") != "internal/billing/a.go,README.md" ||
		strings.Join(res.Areas[0].Rules, ",") != f.ruleBilling.Address+","+f.ruleReadme.Address || res.Areas[0].ApprovalMode != ApprovalModeCommentOnly {
		t.Errorf("areas = %+v", res.Areas)
	}
	if res.Areas[1].ID != f.a2.ID || res.Areas[1].Domain == "" || strings.Join(res.Areas[1].Paths, ",") != "cmd/x/main.go" {
		t.Errorf("second area = %+v", res.Areas[1])
	}
	want := fmt.Sprintf("dec-area@area:%s,dec-domain@domain:%s+%s,dec-org@organization:", f.a1.Address, f.a1.Address, f.a2.Address)
	if got := decisionIDs(res.Decisions); got != want {
		t.Errorf("decisions = %s\nwant        %s", got, want)
	}
	if strings.Join(res.Uncovered, ",") != "docs/x.md" {
		t.Errorf("uncovered = %v", res.Uncovered)
	}
	if res.Decisions[0].Standing != StandingHolds || res.Decisions[0].PlaceAddress != f.a1.Address || res.Decisions[2].PlaceAddress == "" {
		t.Errorf("decision fields = %+v", res.Decisions)
	}
	res, _ = f.app.DecisionsForPaths(ctx, DecisionsForPathsQuery{Repository: "acme/app", Paths: paths, ExcludeOrganization: true})
	if strings.Contains(decisionIDs(res.Decisions), "dec-org") {
		t.Error("ExcludeOrganization kept the Organization's Decision")
	}
	// Nothing covered: no Areas, no Decisions, every path uncovered.
	res, err = f.app.DecisionsForPaths(ctx, DecisionsForPathsQuery{Repository: "acme/app", Paths: []string{"docs/x.md"}})
	if err != nil || len(res.Areas) != 0 || len(res.Decisions) != 0 || len(res.Uncovered) != 1 {
		t.Errorf("uncovered only = %+v, %v", res, err)
	}
	// An archived Area's rules do not cover.
	if _, err := f.db.Exec(`UPDATE smeldr_areas SET status = 'archived' WHERE id = $1`, f.a2.ID); err != nil {
		t.Fatal(err)
	}
	res, _ = f.app.DecisionsForPaths(ctx, DecisionsForPathsQuery{Repository: "acme/app", Paths: []string{"cmd/x/main.go"}})
	if len(res.Areas) != 0 {
		t.Errorf("archived Area covered: %+v", res.Areas)
	}
}

// A reader sees only what it may read; hidden records change nothing.
func TestDecisionsForPaths_ReadRules(t *testing.T) {
	f := newCoverageFixture(t)
	ctx := context.Background()
	if _, err := f.rs.Grant(ctx, RoleGrant{TokenID: "reader", RoleName: "scoped-reader",
		ScopeStatic: []string{"Area:" + f.a1.ID, "CoverageRule:*", "Decision:dec-area"}}); err != nil {
		t.Fatal(err)
	}
	res, err := f.app.DecisionsForPaths(NewTestContext(User{ID: "reader"}), DecisionsForPathsQuery{Repository: "acme/app",
		Paths: []string{"internal/billing/a.go", "cmd/x/main.go"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Areas) != 1 || res.Areas[0].ID != f.a1.ID || res.Areas[0].Domain != "" {
		t.Errorf("areas = %+v; want only the readable Area, its hidden Domain left out", res.Areas)
	}
	if got := decisionIDs(res.Decisions); got != "dec-area@area:"+f.a1.Address {
		t.Errorf("decisions = %s", got)
	}
	if strings.Join(res.Uncovered, ",") != "cmd/x/main.go" {
		t.Errorf("uncovered = %v; a path only a hidden Area covers reads as uncovered", res.Uncovered)
	}
}

func TestDecisionsForPaths_Errors(t *testing.T) {
	f := newCoverageFixture(t)
	ctx := TrustedRead(authorCtx())
	if _, err := f.app.DecisionsForPaths(ctx, DecisionsForPathsQuery{Repository: "nope"}); err == nil {
		t.Error("bad repository accepted")
	}
	many := make([]string, DecisionsForPathsMaxPaths+1)
	if _, err := f.app.DecisionsForPaths(ctx, DecisionsForPathsQuery{Repository: "acme/app", Paths: many}); !errors.Is(err, ErrBadRequest) {
		t.Errorf("too many paths: %v", err)
	}
	if _, err := (&App{}).DecisionsForPaths(ctx, DecisionsForPathsQuery{Repository: "acme/app"}); !errors.Is(err, ErrInternal) {
		t.Errorf("no database: %v", err)
	}
	paths := []string{"internal/billing/a.go"}
	for _, match := range []string{"FROM smeldr_coverage_rules r JOIN", "JOIN smeldr_domains d", "JOIN smeldr_decisions d", "FROM smeldr_standing", "UNION ALL"} {
		f.app.cfg.DB = failAddressDB{DB: f.db, match: match}
		_, err := f.app.DecisionsForPaths(ctx, DecisionsForPathsQuery{Repository: "acme/app", Paths: paths})
		f.app.cfg.DB = f.db
		if err == nil {
			t.Errorf("failing %q: want an error", match)
		}
	}
}

// Rules are checked and normalized on every write; the Area is write-once and
// must be an Area.
func TestCoverageRule_Writes(t *testing.T) {
	f := newCoverageFixture(t)
	seed := TrustedRead(authorCtx()).(Context)
	if f.ruleBilling.Repository != "acme/app" || f.ruleBilling.Kind != CoverageKindPath || f.ruleBilling.Area != f.a1.Address {
		t.Errorf("stored rule = %+v", f.ruleBilling)
	}
	for _, fields := range []map[string]any{
		{"area": f.domain, "repository": "acme/app", "pattern": "a"},
		{"area": "ARE999", "repository": "acme/app", "pattern": "a"},
		{"area": f.a1.Address, "repository": "acme", "pattern": "a"},
		{"area": f.a1.Address, "repository": "acme/app", "pattern": "/a"},
		{"area": f.a1.Address, "repository": "acme/app", "pattern": "a", "kind": "symbol"},
	} {
		if _, err := f.rules.MCPCreate(seed, fields); err == nil {
			t.Errorf("create %v accepted", fields)
		}
	}
	if _, err := f.rules.MCPUpdate(seed, f.ruleBilling.Slug, map[string]any{"area": f.a2.Address}); err == nil {
		t.Error("the Area of a rule changed")
	}
	var placed int
	_ = f.db.QueryRow(`SELECT COUNT(*) FROM smeldr_relations WHERE source_id = $1 AND relation_kind = 'belongs_to_area' AND target_id = $2`, f.ruleBilling.ID, f.a1.ID).Scan(&placed)
	if placed != 1 {
		t.Errorf("rule placed in its Area: %d edges", placed)
	}
}

func TestArea_ApprovalMode(t *testing.T) {
	f := newCoverageFixture(t)
	seed := TrustedRead(authorCtx()).(Context)
	if f.a1.ApprovalMode != ApprovalModeCommentOnly {
		t.Errorf("default = %q", f.a1.ApprovalMode)
	}
	out, err := f.areas.MCPUpdate(seed, f.a1.Slug, map[string]any{"approval_mode": ApprovalModeRequiresApproval})
	if err != nil || asItem[*Area](out).ApprovalMode != ApprovalModeRequiresApproval {
		t.Errorf("set requires-approval: %v", err)
	}
	if _, err := f.areas.MCPUpdate(seed, f.a1.Slug, map[string]any{"approval_mode": "always"}); err == nil {
		t.Error("unknown mode accepted")
	}
}

// openCoverage returns the open coverage-matches-nothing conditions of id.
func openCoverage(t *testing.T, db *sql.DB, id string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM smeldr_signals WHERE subject_id = $1 AND signal_type = $2 AND status = 'open'`, id, coverageMatchesNothing).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestCheckCoverage(t *testing.T) {
	f := newCoverageFixture(t)
	ctx := TrustedRead(authorCtx())
	tree := []string{"internal/billing/a.go", "internal/billing/b.go", "README.md", "cmd/x/main.go", "docs/x.md"}
	for run := 0; run < 2; run++ {
		res, err := f.app.CheckCoverage(ctx, CheckCoverageQuery{Repository: "acme/app", Paths: tree})
		if err != nil {
			t.Fatal(err)
		}
		if res.Paths != 5 || len(res.Rules) != 4 || res.Rules[0].Matches != 2 || strings.Join(res.MatchingNothing, ",") != f.ruleNothing.Address {
			t.Errorf("run %d: %+v", run, res)
		}
	}
	if n := openCoverage(t, f.db, f.ruleNothing.ID); n != 1 {
		t.Errorf("open conditions = %d, want 1 after two checks", n)
	}
	var receiver string
	_ = f.db.QueryRow(`SELECT receiver FROM smeldr_signals WHERE subject_id = $1`, f.ruleNothing.ID).Scan(&receiver)
	if receiver != OperationApproveChange {
		t.Errorf("receiver = %q", receiver)
	}
	var count int
	var checked sql.NullString
	_ = f.db.QueryRow(`SELECT match_count, checked_at FROM smeldr_coverage_rules WHERE id = $1`, f.ruleBilling.ID).Scan(&count, &checked)
	if count != 2 || !checked.Valid {
		t.Errorf("stored count = %d, checked %v", count, checked)
	}
	// It matches again: resolved.
	if _, err := f.app.CheckCoverage(ctx, CheckCoverageQuery{Repository: "acme/app", Paths: append(tree, "legacy/x.go")}); err != nil {
		t.Fatal(err)
	}
	if n := openCoverage(t, f.db, f.ruleNothing.ID); n != 0 {
		t.Errorf("open after a match = %d", n)
	}
	// Opened again, then the rule changes: resolved.
	_, _ = f.app.CheckCoverage(ctx, CheckCoverageQuery{Repository: "acme/app", Paths: tree})
	seed := TrustedRead(authorCtx()).(Context)
	if _, err := f.rules.MCPUpdate(seed, f.ruleNothing.Slug, map[string]any{"pattern": "docs/**"}); err != nil {
		t.Fatal(err)
	}
	if n := openCoverage(t, f.db, f.ruleNothing.ID); n != 0 {
		t.Errorf("open after a change = %d", n)
	}
	// Opened again (a rule that matches nothing), then archived: resolved,
	// and an archived rule is not counted.
	if _, err := f.rules.MCPUpdate(seed, f.ruleNothing.Slug, map[string]any{"pattern": "gone/**"}); err != nil {
		t.Fatal(err)
	}
	_, _ = f.app.CheckCoverage(ctx, CheckCoverageQuery{Repository: "acme/app", Paths: tree})
	if n := openCoverage(t, f.db, f.ruleNothing.ID); n != 1 {
		t.Fatalf("not reopened: %d", n)
	}
	if _, err := f.app.TransitionItem(ctx, "CoverageRule", f.ruleNothing.Slug, "archived"); err != nil {
		t.Fatal(err)
	}
	if n := openCoverage(t, f.db, f.ruleNothing.ID); n != 0 {
		t.Errorf("open after archive = %d", n)
	}
	res, _ := f.app.CheckCoverage(ctx, CheckCoverageQuery{Repository: "acme/app", Paths: tree})
	if len(res.Rules) != 3 {
		t.Errorf("archived rule counted: %+v", res.Rules)
	}
}

func TestCheckCoverage_Errors(t *testing.T) {
	f := newCoverageFixture(t)
	ctx := TrustedRead(authorCtx())
	if _, err := f.app.CheckCoverage(ctx, CheckCoverageQuery{Repository: ""}); err == nil {
		t.Error("bad repository accepted")
	}
	if _, err := f.app.CheckCoverage(ctx, CheckCoverageQuery{Repository: "acme/app", Paths: make([]string, CheckCoverageMaxPaths+1)}); !errors.Is(err, ErrBadRequest) {
		t.Errorf("too many paths: %v", err)
	}
	if _, err := (&App{}).CheckCoverage(ctx, CheckCoverageQuery{Repository: "acme/app"}); !errors.Is(err, ErrInternal) {
		t.Errorf("no database: %v", err)
	}
	for _, match := range []string{"FROM smeldr_coverage_rules r JOIN", "UPDATE smeldr_coverage_rules", "FROM smeldr_signals", "INSERT INTO smeldr_signals"} {
		f.app.cfg.DB = failAddressDB{DB: f.db, match: match}
		_, err := f.app.CheckCoverage(ctx, CheckCoverageQuery{Repository: "acme/app", Paths: []string{"README.md"}})
		f.app.cfg.DB = f.db
		if err == nil {
			t.Errorf("failing %q: want an error", match)
		}
	}
	(&App{}).resolveCoverageCondition(ctx, "x", "no database: nothing to do")
	f.app.cfg.DB = failAddressDB{DB: f.db, match: "FROM smeldr_signals"}
	f.app.resolveCoverageCondition(ctx, f.ruleNothing.ID, "a failed read leaves it for the next check")
	f.app.cfg.DB = f.db
}

// BenchmarkDecisionsForPaths: 3,000 paths against 300 rules in one repository.
func BenchmarkDecisionsForPaths(b *testing.B) {
	f := newCoverageFixture(b)
	seed := TrustedRead(authorCtx()).(Context)
	for i := 0; i < 296; i++ {
		pattern := fmt.Sprintf("pkg%03d/**", i)
		if i%3 == 0 {
			pattern = fmt.Sprintf("pkg%03d/*/file_*.go", i)
		} else if i%3 == 1 {
			pattern = fmt.Sprintf("pkg%03d/exact.go", i)
		}
		if _, err := f.rules.MCPCreate(seed, map[string]any{"area": f.a1.Address, "repository": "acme/app", "pattern": pattern}); err != nil {
			b.Fatal(err)
		}
	}
	paths := make([]string, 3000)
	for i := range paths {
		paths[i] = fmt.Sprintf("pkg%03d/sub%d/file_%d.go", i%400, i%7, i)
	}
	ctx := TrustedRead(authorCtx())
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := f.app.DecisionsForPaths(ctx, DecisionsForPathsQuery{Repository: "acme/app", Paths: paths}); err != nil {
			b.Fatal(err)
		}
	}
}

// T901: an update that changes the pattern, repository or kind clears the
// rule's count and checked time; one that changes none keeps them.
func TestCoverageRule_ChangeClearsCount(t *testing.T) {
	f := newCoverageFixture(t)
	ctx := TrustedRead(authorCtx())
	seed := TrustedRead(authorCtx()).(Context)
	tree := []string{"internal/billing/a.go", "internal/billing/b.go", "README.md", "cmd/x/main.go", "docs/x.md"}
	state := func(id string) (int, bool) {
		var n int
		var at sql.NullString
		if err := f.db.QueryRow(`SELECT match_count, checked_at FROM smeldr_coverage_rules WHERE id = $1`, id).Scan(&n, &at); err != nil {
			t.Fatal(err)
		}
		return n, at.Valid
	}
	for _, tc := range []struct {
		name    string
		fields  map[string]any
		cleared bool
	}{
		{"same pattern again", map[string]any{"pattern": "internal/billing/**"}, false},
		{"repository differing in case only", map[string]any{"repository": "ACME/App"}, false},
		{"same kind", map[string]any{"kind": "path"}, false},
		{"pattern changed", map[string]any{"pattern": "internal/billing/*.go"}, true},
		{"repository changed", map[string]any{"repository": "acme/other-app"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Reset the rule to its original values, then count.
			if _, err := f.db.Exec(`UPDATE smeldr_coverage_rules SET pattern = 'internal/billing/**', repository = 'acme/app' WHERE id = $1`, f.ruleBilling.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := f.app.CheckCoverage(ctx, CheckCoverageQuery{Repository: "acme/app", Paths: tree}); err != nil {
				t.Fatal(err)
			}
			if n, checked := state(f.ruleBilling.ID); n != 2 || !checked {
				t.Fatalf("before: count %d, checked %v", n, checked)
			}
			if _, err := f.rules.MCPUpdate(seed, f.ruleBilling.Slug, tc.fields); err != nil {
				t.Fatal(err)
			}
			n, checked := state(f.ruleBilling.ID)
			if tc.cleared && (n != 0 || checked) || !tc.cleared && (n != 2 || !checked) {
				t.Errorf("after: count %d, checked %v; want cleared %v", n, checked, tc.cleared)
			}
		})
	}
}

func TestCoverageRuleChanged(t *testing.T) {
	base := CoverageRule{Kind: "path", Repository: "acme/app", Pattern: "a/**"}
	for _, tc := range []struct {
		change func(*CoverageRule)
		want   bool
	}{
		{func(*CoverageRule) {}, false},
		{func(r *CoverageRule) { r.Pattern = "b/**" }, true},
		{func(r *CoverageRule) { r.Repository = "acme/b" }, true},
		{func(r *CoverageRule) { r.Kind = "symbol" }, true},
		{func(r *CoverageRule) { r.Area = "ARE9"; r.MatchCount = 7 }, false},
	} {
		r := base
		tc.change(&r)
		if got := coverageRuleChanged(&base, &r); got != tc.want {
			t.Errorf("%+v: changed = %v, want %v", r, got, tc.want)
		}
	}
}
