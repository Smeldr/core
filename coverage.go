// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Coverage rules (D142 points 7 and 8, T886): an Area covers repository paths,
// and one resolution answers which Decisions apply to a list of paths. Core
// has no repository access: the caller supplies the paths.

// CoverageRule is a rule by which an Area covers repository paths. Only Areas
// have coverage rules; one Area may cover paths in several repositories. The
// first kind is a path pattern; line rules are declined (D142) and a symbol
// kind may come later.
type CoverageRule struct {
	Node
	// RecordAddress is the rule's address (D110, prefix COV).
	RecordAddress
	// Area is the Area the rule belongs to, by address, slug or ID; stored as
	// its address. Write-once: moving a rule is archiving it and creating
	// another. The system relates the rule to the Area by belongs_to_area.
	Area string `json:"area" db:"area" smeldr:"required" smeldr_description:"The Area this rule covers paths for: its address (ARE3), slug or ID. Write-once."`
	// Kind is the rule's kind; "path" is the only one so far.
	Kind string `json:"kind" db:"kind" smeldr_enum:"path" smeldr_description:"The rule's kind: path (a repository path pattern). Default path."`
	// Repository is the repository the pattern applies in, "owner/name",
	// stored in lower case (GitHub compares them without case).
	Repository string `json:"repository" db:"repository" smeldr:"required" smeldr_description:"The repository, owner/name."`
	// Pattern is a repository path pattern: * matches within one path segment,
	// ? one character, ** any number of segments. A pattern without wildcards
	// covers one file.
	Pattern string `json:"pattern" db:"pattern" smeldr:"required" smeldr_description:"A path pattern in the repository: * within one segment, ? one character, ** any number of segments, for example internal/billing/** or cmd/*/main.go. No leading slash, no .. segments."`
	// MatchCount is how many paths the pattern matched at the last
	// [App.CheckCoverage]; read-only.
	MatchCount int `json:"match_count" db:"match_count" smeldr:"readonly"`
	// CheckedAt is when [App.CheckCoverage] last counted the rule; nil before
	// the first check. Read-only.
	CheckedAt *time.Time `json:"checked_at,omitempty" db:"checked_at" smeldr:"readonly"`
}

// The kinds of coverage rule.
const (
	// CoverageKindPath is a repository path pattern.
	CoverageKindPath = "path"
)

// Approval modes of an Area (D142 point 8), read by the pull request check.
const (
	// ApprovalModeCommentOnly: a change touching the Area gets a comment.
	ApprovalModeCommentOnly = "comment-only"
	// ApprovalModeRequiresApproval: such a change needs approve-change from
	// the Area's approver.
	ApprovalModeRequiresApproval = "requires-approval"
)

// Limits of one call.
const (
	// DecisionsForPathsMaxPaths is the most paths [App.DecisionsForPaths]
	// takes in one call: GitHub's own cap on a pull request's file list.
	DecisionsForPathsMaxPaths = 3000
	// CheckCoverageMaxPaths is the most paths [App.CheckCoverage] takes: the
	// size where GitHub's tree API truncates a repository's file list.
	CheckCoverageMaxPaths = 100000
	// coveragePatternMaxLen bounds a pattern.
	coveragePatternMaxLen = 512
)

// coverageMatchesNothing is the condition a rule that matches no path opens.
const coverageMatchesNothing = "coverage-matches-nothing"

// coverageRuleTableStmts create the table of [CoverageRule] and its lookup
// index (by repository, the key of every resolution).
var coverageRuleTableStmts = []string{
	`CREATE TABLE IF NOT EXISTS smeldr_coverage_rules (
		id           TEXT PRIMARY KEY,
		slug         TEXT NOT NULL UNIQUE,
		status       TEXT NOT NULL DEFAULT 'active',
		published_at TIMESTAMPTZ,
		scheduled_at TIMESTAMPTZ,
		created_at   TIMESTAMPTZ NOT NULL,
		updated_at   TIMESTAMPTZ NOT NULL,
		rev          INTEGER NOT NULL DEFAULT 0,
		area         TEXT NOT NULL DEFAULT '',
		kind         TEXT NOT NULL DEFAULT 'path',
		repository   TEXT NOT NULL DEFAULT '',
		pattern      TEXT NOT NULL DEFAULT '',
		match_count  INTEGER NOT NULL DEFAULT 0,
		checked_at   TIMESTAMPTZ,
		last_actor   TEXT NOT NULL DEFAULT ''
	)`,
	`CREATE INDEX IF NOT EXISTS idx_smeldr_coverage_rules_repository ON smeldr_coverage_rules (repository, status)`,
}

// coverageRuleFlow is the flow of [CoverageRule]: active, archived.
func coverageRuleFlow() StateFlow {
	return StateFlow{
		Name:     "coverage-rule",
		TypeName: "CoverageRule",
		States:   []State{{Name: "active", IsInitial: true}, {Name: "archived"}},
		// D146 (T910): archiving a rule, or bringing it back, changes its
		// Area's gate: ratify over the rule (its Area's domain-approver or an
		// approver, through the place walk). Not strict, as the place flows.
		Transitions: []Transition{
			{From: "active", To: "archived", RequiredOperation: OperationRatify},
			{From: "archived", To: "active", RequiredOperation: OperationRatify},
		},
	}
}

// coverageRuleReferences is the one reference field of [CoverageRule].
func coverageRuleReferences() []referenceField {
	return []referenceField{{
		name:    "area",
		value:   func(item any) *string { return &item.(*CoverageRule).Area },
		targets: func(any) []string { return []string{"Area"} },
		edge: func(item any, _, targetID string) RelationEdge {
			return RelationEdge{SourceType: "CoverageRule", SourceID: item.(*CoverageRule).ID, TargetType: "Area", TargetID: targetID, RelationKind: "belongs_to_area"}
		},
		refusal: func(v string) string {
			return fmt.Sprintf("no Area %q you can read; only an Area has coverage rules: name it by address (ARE3), slug or ID", v)
		},
	}}
}

// registerCoverageRule registers [CoverageRule] with its flow, its reference
// field, its field checks and the hooks that resolve its condition. Called by
// [RegisterGovernanceTypes].
func registerCoverageRule(app *App, db DB) {
	registerFlows(app, coverageRuleFlow())
	refs := app.referenceSaveHooks(coverageRuleReferences(), nil)
	prepareRefs := refs.prepare
	refs.prepare = func(ctx Context, existing, item any) error {
		r := item.(*CoverageRule)
		if err := normalizeCoverageRule(r); err != nil {
			return err
		}
		old, isUpdate := existing.(*CoverageRule)
		if isUpdate && coverageRuleChanged(old, r) {
			// D146 (T910): changing what a rule covers loosens or moves its
			// Area's gate: ratify over the rule's Area.
			if err := requireOperationOver(ctx, app.governance, OperationRatify, AuthTarget{TypeName: "CoverageRule", ID: old.ID},
				fmt.Sprintf("changing %s's pattern, repository or kind", ruleLabel(old)), old.Area, "D146"); err != nil {
				return err
			}
			// A changed rule goes back to not checked (T901): its count and
			// time were for the old pattern. Same write as the change.
			r.MatchCount, r.CheckedAt = 0, nil
		}
		if err := prepareRefs(ctx, existing, item); err != nil {
			return err
		}
		if isUpdate {
			return nil
		}
		// D146: a new rule needs approve-change over its Area (an area-approver
		// on it, its domain-approver, an approver).
		var areaID string
		if err := db.QueryRowContext(ctx, `SELECT id FROM smeldr_areas WHERE address = $1 OR id = $1`, r.Area).Scan(&areaID); err != nil {
			return fmt.Errorf("%w: the Area %s of the new rule cannot be read: %s", ErrInternal, r.Area, err)
		}
		return requireOperationOver(ctx, app.governance, OperationApproveChange, AuthTarget{TypeName: "Area", ID: areaID},
			"adding a coverage rule to "+r.Area, r.Area, "D146")
	}
	afterRefs := refs.after
	refs.after = func(ctx Context, db DB, rs *RelationStore, existing, item any) {
		afterRefs(ctx, db, rs, existing, item)
		if existing != nil {
			app.resolveCoverageCondition(ctx, item.(*CoverageRule).ID, "the rule changed")
		}
	}
	refs.statusChanged = func(ctx Context, _ DB, _ *RelationStore, item any, _, to string) {
		if to == "archived" {
			app.resolveCoverageCondition(ctx, item.(*CoverageRule).ID, "the rule was archived")
		}
	}
	app.Content(NewModule[*CoverageRule]((*CoverageRule)(nil),
		At("/coverage-rules"), Repo(NewSQLRepo[*CoverageRule](db, Table("smeldr_coverage_rules"))), MCP(MCPRead, MCPWrite), withAddress(),
		refs,
		On[*CoverageRule](BeforeDelete, func(ctx Context, r *CoverageRule) error {
			return requireOperationOver(ctx, app.governance, OperationRatify, AuthTarget{TypeName: "CoverageRule", ID: r.ID},
				"deleting "+ruleLabel(r), r.Area, "D146")
		}),
	))
}

// coverageRuleChanged reports whether r differs from old in what decides its
// matches: the kind, the repository or the pattern, compared normalized (a
// repository differing only in case is the same).
func coverageRuleChanged(old, r *CoverageRule) bool {
	return old.Kind != r.Kind || old.Repository != r.Repository || old.Pattern != r.Pattern
}

// coverageRepositoryRe is the shape of a repository: owner/name.
var coverageRepositoryRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// normalizeCoverageRule checks and normalizes a rule's kind, repository and
// pattern before it is written.
func normalizeCoverageRule(r *CoverageRule) error {
	r.Kind = strings.TrimSpace(r.Kind)
	if r.Kind == "" {
		r.Kind = CoverageKindPath
	}
	if r.Kind != CoverageKindPath {
		return Err("kind", fmt.Sprintf("must be %q: the only kind of coverage rule so far", CoverageKindPath))
	}
	repo, err := normalizeRepository(r.Repository)
	if err != nil {
		return err
	}
	r.Repository = repo
	r.Pattern = strings.TrimSpace(r.Pattern)
	if err := checkCoveragePattern(r.Pattern); err != nil {
		return err
	}
	return nil
}

// normalizeRepository checks repo is owner/name and returns it in lower case.
func normalizeRepository(repo string) (string, error) {
	repo = strings.TrimSpace(repo)
	if !coverageRepositoryRe.MatchString(repo) {
		return "", Err("repository", fmt.Sprintf("%q is not owner/name, for example acme/billing", repo))
	}
	return strings.ToLower(repo), nil
}

// checkCoveragePattern refuses a pattern the matcher would read wrongly.
func checkCoveragePattern(p string) error {
	switch {
	case p == "":
		return Err("pattern", "is required: a path in the repository, for example internal/billing/**")
	case len(p) > coveragePatternMaxLen:
		return Err("pattern", fmt.Sprintf("is longer than %d characters", coveragePatternMaxLen))
	case strings.HasPrefix(p, "/"):
		return Err("pattern", "starts with /: write it relative to the repository root, for example internal/billing/**")
	case strings.Contains(p, `\`):
		return Err("pattern", `contains a backslash: separate segments with /`)
	}
	for _, seg := range strings.Split(p, "/") {
		switch {
		case seg == "":
			return Err("pattern", "has an empty segment (//, or a trailing /)")
		case seg == "..":
			return Err("pattern", "has a .. segment: a pattern stays inside the repository")
		case seg != "**" && strings.Contains(seg, "**"):
			return Err("pattern", "has ** inside a segment: ** stands alone between slashes")
		}
	}
	return nil
}

// coveragePattern is a compiled pattern: a literal path, a literal directory
// followed by ** (the common case, matched by prefix), or its segments.
type coveragePattern struct {
	literal string
	dir     string // "a/b" for "a/b/**"
	segs    []string
}

// compileCoveragePattern compiles p, which [checkCoveragePattern] accepted.
func compileCoveragePattern(p string) coveragePattern {
	if !strings.ContainsAny(p, "*?") {
		return coveragePattern{literal: p}
	}
	if dir, ok := strings.CutSuffix(p, "/**"); ok && !strings.ContainsAny(dir, "*?") {
		return coveragePattern{dir: dir}
	}
	return coveragePattern{segs: strings.Split(p, "/")}
}

// match reports whether the path split into segs matches the pattern.
func (c coveragePattern) match(path string, segs []string) bool {
	var buf matchBuffers
	return c.matchWith(path, segs, &buf)
}

// matchBuffers are the two rows the segment matcher reuses between calls.
type matchBuffers struct{ a, b []bool }

// matchWith is match with reusable buffers. Dynamic programming over
// (pattern segment, path segment), so several ** never backtrack
// exponentially.
func (c coveragePattern) matchWith(path string, segs []string, buf *matchBuffers) bool {
	switch {
	case c.dir != "":
		return path == c.dir || strings.HasPrefix(path, c.dir) && len(path) > len(c.dir) && path[len(c.dir)] == '/'
	case c.segs == nil:
		return c.literal == path
	}
	n, m := len(c.segs), len(segs)
	if cap(buf.a) < m+1 {
		buf.a, buf.b = make([]bool, m+1), make([]bool, m+1)
	}
	// ok[j] is whether c.segs[i:] matches segs[j:], for the current i.
	ok, next := buf.a[:m+1], buf.b[:m+1]
	clear(ok)
	ok[m] = true
	for i := n - 1; i >= 0; i-- {
		ok, next = next, ok
		clear(ok)
		if c.segs[i] == "**" {
			// ** matches zero segments (next[j]) or one more (ok[j+1]).
			ok[m] = next[m]
			for j := m - 1; j >= 0; j-- {
				ok[j] = next[j] || ok[j+1]
			}
			continue
		}
		for j := m - 1; j >= 0; j-- {
			ok[j] = next[j+1] && matchSegment(c.segs[i], segs[j])
		}
	}
	return ok[0]
}

// matchSegment matches one path segment against a pattern segment with * and
// ?, without regexp: the usual linear wildcard match with one backtrack point.
func matchSegment(p, s string) bool {
	pi, si, star, mark := 0, 0, -1, 0
	for si < len(s) {
		switch {
		case pi < len(p) && (p[pi] == '?' || p[pi] == s[si]):
			pi++
			si++
		case pi < len(p) && p[pi] == '*':
			star, mark = pi, si
			pi++
		case star >= 0:
			pi = star + 1
			mark++
			si = mark
		default:
			return false
		}
	}
	for pi < len(p) && p[pi] == '*' {
		pi++
	}
	return pi == len(p)
}

// normalizePaths trims the given paths, drops empty ones and a leading ./ or
// /, and keeps the first of duplicates.
func normalizePaths(paths []string) []string {
	out := make([]string, 0, len(paths))
	seen := make(map[string]bool, len(paths))
	for _, p := range paths {
		p = strings.TrimSpace(p)
		p = strings.TrimPrefix(strings.TrimPrefix(p, "./"), "/")
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// coverageRuleRow is one active rule of a repository with its Area.
type coverageRuleRow struct {
	id, address, pattern                    string
	areaID, areaAddress, areaName, areaMode string
	compiled                                coveragePattern
}

// activeCoverageRules reads the active rules of repo whose Area is active,
// with their Areas, in one query.
func (a *App) activeCoverageRules(ctx context.Context, repo string) ([]coverageRuleRow, error) {
	rows, err := a.cfg.DB.QueryContext(ctx,
		`SELECT r.id, r.address, r.pattern, a.id, a.address, a.name, a.approval_mode
		   FROM smeldr_coverage_rules r JOIN smeldr_areas a ON a.address = r.area
		  WHERE r.repository = $1 AND r.status = 'active' AND a.status = 'active'
		  ORDER BY r.address_no`, repo)
	if err != nil {
		return nil, fmt.Errorf("%w: coverage rules: %v", ErrInternal, err)
	}
	defer rows.Close()
	var out []coverageRuleRow
	for rows.Next() {
		var r coverageRuleRow
		if err := rows.Scan(&r.id, &r.address, &r.pattern, &r.areaID, &r.areaAddress, &r.areaName, &r.areaMode); err != nil {
			return nil, fmt.Errorf("%w: coverage rules: %v", ErrInternal, err)
		}
		r.compiled = compileCoveragePattern(r.pattern)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%w: coverage rules: %v", ErrInternal, err)
	}
	return out, nil
}

// coverageMatcher matches paths against a repository's rules: literal
// patterns by map lookup, the others segment by segment. Not safe for
// concurrent use (it reuses its buffers).
type coverageMatcher struct {
	literal  map[string][]int // path -> rule indexes
	wildcard []int            // indexes of the wildcard rules
	rules    []coverageRuleRow
	buf      *matchBuffers
}

func newCoverageMatcher(rules []coverageRuleRow) coverageMatcher {
	m := coverageMatcher{literal: map[string][]int{}, rules: rules, buf: &matchBuffers{}}
	for i, r := range rules {
		if r.compiled.segs == nil && r.compiled.dir == "" {
			m.literal[r.compiled.literal] = append(m.literal[r.compiled.literal], i)
		} else {
			m.wildcard = append(m.wildcard, i)
		}
	}
	return m
}

// match calls hit with the index of every rule path matches.
func (m coverageMatcher) match(path string, hit func(i int)) {
	for _, i := range m.literal[path] {
		hit(i)
	}
	if len(m.wildcard) == 0 {
		return
	}
	var segs []string
	for _, i := range m.wildcard {
		if segs == nil && m.rules[i].compiled.segs != nil {
			segs = strings.Split(path, "/")
		}
		if m.rules[i].compiled.matchWith(path, segs, m.buf) {
			hit(i)
		}
	}
}

// DecisionsForPathsQuery asks which Decisions apply to paths in a repository.
type DecisionsForPathsQuery struct {
	// Repository is owner/name.
	Repository string
	// Paths are repository paths, at most [DecisionsForPathsMaxPaths].
	Paths []string
	// ExcludeOrganization leaves out the Decisions placed on the
	// Organization; by default they are included, since they are in force
	// everywhere.
	ExcludeOrganization bool
}

// CoveredArea is an Area some of the paths fall in.
type CoveredArea struct {
	ID           string   `json:"id"`
	Address      string   `json:"address"`
	Name         string   `json:"name"`
	ApprovalMode string   `json:"approval_mode"`
	Domain       string   `json:"domain,omitempty"` // the Domain's address
	Paths        []string `json:"paths"`
	Rules        []string `json:"rules"` // the matching rules' addresses
}

// AppliedDecision is a Decision in force that applies to some of the paths.
type AppliedDecision struct {
	ID       string   `json:"id"`
	Address  string   `json:"address"`
	Title    string   `json:"title"`
	State    string   `json:"state"`
	Standing Standing `json:"standing"`
	// Place is "area", "domain" or "organization": where the Decision is
	// placed; PlaceAddress names that place.
	Place        string `json:"place"`
	PlaceAddress string `json:"place_address"`
	// Areas are the addresses of the covered Areas it applies through; empty
	// for an Organization-wide Decision.
	Areas []string `json:"areas,omitempty"`
}

// DecisionsForPathsResult is the answer of [App.DecisionsForPaths].
type DecisionsForPathsResult struct {
	Repository string            `json:"repository"`
	Areas      []CoveredArea     `json:"areas"`
	Decisions  []AppliedDecision `json:"decisions"`
	Uncovered  []string          `json:"uncovered"`
}

// DecisionsForPaths resolves repository paths to the Areas they fall in and
// the Decisions in force placed in those Areas, in their Domains and, unless
// excluded, on the Organization (D142 point 8, T886). It is the one
// resolution the pull request check and the MCP tool share. Everything is
// read under the caller's read rules: an Area, rule or Decision the caller may
// not read is left out and never changes a count. A Decision is in force when
// its standing holds (ratified, or pending re-evaluation).
//
// At most [DecisionsForPathsMaxPaths] paths per call; more is
// [ErrBadRequest]. The answer is per path, so the results of calls over parts
// of a longer list add up.
func (a *App) DecisionsForPaths(ctx context.Context, q DecisionsForPathsQuery) (DecisionsForPathsResult, error) {
	repo, err := normalizeRepository(q.Repository)
	if err != nil {
		return DecisionsForPathsResult{}, err
	}
	if len(q.Paths) > DecisionsForPathsMaxPaths {
		return DecisionsForPathsResult{}, fmt.Errorf("%w: at most %d paths per call (GitHub's cap on a pull request); split the list, the results of separate calls add up", ErrBadRequest, DecisionsForPathsMaxPaths)
	}
	if a.cfg.DB == nil {
		return DecisionsForPathsResult{}, fmt.Errorf("%w: DecisionsForPaths needs a database", ErrInternal)
	}
	scope, err := scopeFor(ctx, a.governance)
	if err != nil {
		return DecisionsForPathsResult{}, err
	}
	all, err := a.activeCoverageRules(ctx, repo)
	if err != nil {
		return DecisionsForPathsResult{}, err
	}
	var rules []coverageRuleRow
	for _, r := range all {
		if scope.Readable("CoverageRule", r.id) && scope.Readable("Area", r.areaID) {
			rules = append(rules, r)
		}
	}
	res := DecisionsForPathsResult{Repository: repo, Areas: []CoveredArea{}, Decisions: []AppliedDecision{}, Uncovered: []string{}}
	matcher := newCoverageMatcher(rules)
	byArea := map[string]*CoveredArea{}
	var order []string
	for _, p := range normalizePaths(q.Paths) {
		covered := false
		seenArea := map[string]bool{}
		matcher.match(p, func(i int) {
			covered = true
			r := rules[i]
			ca := byArea[r.areaID]
			if ca == nil {
				ca = &CoveredArea{ID: r.areaID, Address: r.areaAddress, Name: r.areaName, ApprovalMode: r.areaMode, Paths: []string{}, Rules: []string{}}
				byArea[r.areaID] = ca
				order = append(order, r.areaID)
			}
			if !seenArea[r.areaID] {
				seenArea[r.areaID] = true
				ca.Paths = append(ca.Paths, p)
			}
			if !containsString(ca.Rules, r.address) {
				ca.Rules = append(ca.Rules, r.address)
			}
		})
		if !covered {
			res.Uncovered = append(res.Uncovered, p)
		}
	}
	if len(order) == 0 {
		// Nothing is covered: no Area applies, and an Organization-wide
		// Decision applies only through a covered path.
		return res, nil
	}
	domains, err := a.areaDomains(ctx, order)
	if err != nil {
		return DecisionsForPathsResult{}, err
	}
	for _, id := range order {
		ca := byArea[id]
		if d, ok := domains[id]; ok && scope.Readable("Domain", d.id) {
			ca.Domain = d.address
		}
		res.Areas = append(res.Areas, *ca)
	}
	decisions, err := a.decisionsInPlaces(ctx, scope, order, domains, !q.ExcludeOrganization)
	if err != nil {
		return DecisionsForPathsResult{}, err
	}
	res.Decisions = decisions
	return res, nil
}

// containsString reports whether list holds s.
func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// placeRef is a place's ID and address.
type placeRef struct{ id, address string }

// inMarks returns "$from, $from+1, ..." for n values.
func inMarks(from, n int) string {
	marks := make([]string, n)
	for i := range marks {
		marks[i] = fmt.Sprintf("$%d", from+i)
	}
	return strings.Join(marks, ", ")
}

// areaDomains returns the Domain each of the Areas belongs to.
func (a *App) areaDomains(ctx context.Context, areaIDs []string) (map[string]placeRef, error) {
	args := []any{time.Now().UTC()}
	for _, id := range areaIDs {
		args = append(args, id)
	}
	rows, err := a.cfg.DB.QueryContext(ctx,
		`SELECT r.source_id, d.id, d.address FROM smeldr_relations r JOIN smeldr_domains d ON d.id = r.target_id
		  WHERE r.relation_kind = 'belongs_to_domain' AND r.source_type = 'Area' AND r.edge_class = 'asserted'
		    AND (r.invalid_at IS NULL OR r.invalid_at > $1) AND r.source_id IN (`+inMarks(2, len(areaIDs))+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("%w: Domains of the Areas: %v", ErrInternal, err)
	}
	defer rows.Close()
	out := map[string]placeRef{}
	for rows.Next() {
		var area string
		var d placeRef
		if err := rows.Scan(&area, &d.id, &d.address); err != nil {
			return nil, fmt.Errorf("%w: Domains of the Areas: %v", ErrInternal, err)
		}
		out[area] = d
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%w: Domains of the Areas: %v", ErrInternal, err)
	}
	return out, nil
}

// decisionsInPlaces returns the readable Decisions in force placed in the
// Areas, in their Domains and, when organization, on the Organization.
func (a *App) decisionsInPlaces(ctx context.Context, scope *ReadScope, areaIDs []string, domains map[string]placeRef, organization bool) ([]AppliedDecision, error) {
	// Which covered Areas each place stands for.
	viaArea := map[string][]string{} // place ID -> covered Area IDs
	for _, id := range areaIDs {
		viaArea[id] = append(viaArea[id], id)
		if d, ok := domains[id]; ok {
			viaArea[d.id] = append(viaArea[d.id], id)
		}
	}
	targets := make([]string, 0, len(viaArea))
	for id := range viaArea {
		targets = append(targets, id)
	}
	sort.Strings(targets)
	args := []any{time.Now().UTC()}
	for _, id := range targets {
		args = append(args, id)
	}
	where := `(r.relation_kind IN ('belongs_to_area', 'belongs_to_domain') AND r.target_id IN (` + inMarks(2, len(targets)) + `))`
	if organization {
		where = `(` + where + ` OR r.relation_kind = 'belongs_to_organization')`
	}
	rows, err := a.cfg.DB.QueryContext(ctx,
		`SELECT d.id, d.address, d.title, d.status, r.relation_kind, r.target_id
		   FROM smeldr_relations r JOIN smeldr_decisions d ON d.id = r.source_id
		  WHERE r.source_type = 'Decision' AND r.edge_class = 'asserted'
		    AND (r.invalid_at IS NULL OR r.invalid_at > $1) AND `+where+`
		  ORDER BY d.address_no`, args...)
	if err != nil {
		return nil, fmt.Errorf("%w: Decisions of the places: %v", ErrInternal, err)
	}
	type row struct {
		AppliedDecision
		target string
	}
	var found []row
	for rows.Next() {
		var r row
		var kind string
		if err := rows.Scan(&r.ID, &r.Address, &r.Title, &r.State, &kind, &r.target); err != nil {
			rows.Close()
			return nil, fmt.Errorf("%w: Decisions of the places: %v", ErrInternal, err)
		}
		r.Place = strings.TrimPrefix(kind, "belongs_to_")
		found = append(found, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, fmt.Errorf("%w: Decisions of the places: %v", ErrInternal, err)
	}
	ids := make([]string, 0, len(found))
	for _, r := range found {
		ids = append(ids, r.ID)
	}
	standing, err := ItemStandings(ctx, a.cfg.DB, "Decision", ids)
	if err != nil {
		return nil, fmt.Errorf("%w: standing of the Decisions: %v", ErrInternal, err)
	}
	var places []string
	seen := map[string]bool{}
	for _, r := range found {
		if !seen[r.target] {
			seen[r.target] = true
			places = append(places, r.target)
		}
	}
	for _, id := range areaIDs {
		if !seen[id] {
			seen[id] = true
			places = append(places, id)
		}
	}
	addresses, err := a.placeAddresses(ctx, places)
	if err != nil {
		return nil, err
	}
	areaAddress := map[string]string{}
	for _, id := range areaIDs {
		areaAddress[id] = addresses[id]
	}
	out := []AppliedDecision{}
	for _, r := range found {
		if standing[r.ID] != StandingHolds || !scope.Readable("Decision", r.ID) {
			continue
		}
		d := r.AppliedDecision
		d.Standing = StandingHolds
		d.PlaceAddress = addresses[r.target]
		for _, area := range viaArea[r.target] {
			d.Areas = append(d.Areas, areaAddress[area])
		}
		out = append(out, d)
	}
	return out, nil
}

// placeAddresses returns the address of each place ID, from the Area, Domain
// and Organization tables.
func (a *App) placeAddresses(ctx context.Context, ids []string) (map[string]string, error) {
	out := map[string]string{}
	if len(ids) == 0 {
		return out, nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	marks := inMarks(1, len(ids))
	rows, err := a.cfg.DB.QueryContext(ctx,
		`SELECT id, address FROM smeldr_areas WHERE id IN (`+marks+`)
		 UNION ALL SELECT id, address FROM smeldr_domains WHERE id IN (`+marks+`)
		 UNION ALL SELECT id, address FROM smeldr_organizations WHERE id IN (`+marks+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("%w: place addresses: %v", ErrInternal, err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, addr string
		if err := rows.Scan(&id, &addr); err != nil {
			return nil, fmt.Errorf("%w: place addresses: %v", ErrInternal, err)
		}
		out[id] = addr
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%w: place addresses: %v", ErrInternal, err)
	}
	return out, nil
}

// CheckCoverageQuery is a repository's full file list, for [App.CheckCoverage].
type CheckCoverageQuery struct {
	// Repository is owner/name.
	Repository string
	// Paths are every file path of the repository, at most
	// [CheckCoverageMaxPaths].
	Paths []string
}

// RuleCoverage is one rule's count in a [CheckCoverageResult].
type RuleCoverage struct {
	Rule    string `json:"rule"` // the rule's address
	Area    string `json:"area"` // the Area's address
	Pattern string `json:"pattern"`
	Matches int    `json:"matches"`
}

// CheckCoverageResult is the answer of [App.CheckCoverage].
type CheckCoverageResult struct {
	Repository string         `json:"repository"`
	Paths      int            `json:"paths"`
	Rules      []RuleCoverage `json:"rules"`
	// MatchingNothing are the addresses of the rules that match no path; each
	// has an open coverage-matches-nothing condition.
	MatchingNothing []string `json:"matching_nothing"`
}

// CheckCoverage counts, for every active rule of a repository, how many of
// its paths the rule matches, given the repository's full file list (T886).
// It stores each count on the rule (match_count, checked_at), opens the
// condition coverage-matches-nothing for a rule that matches nothing (one
// per rule) and resolves it for a rule that matches again. Every active rule
// of the repository is counted, readable to the caller or not: the check
// keeps the rules true, the caller supplies only the file list. At most
// [CheckCoverageMaxPaths] paths; more is [ErrBadRequest].
func (a *App) CheckCoverage(ctx context.Context, q CheckCoverageQuery) (CheckCoverageResult, error) {
	repo, err := normalizeRepository(q.Repository)
	if err != nil {
		return CheckCoverageResult{}, err
	}
	if len(q.Paths) > CheckCoverageMaxPaths {
		return CheckCoverageResult{}, fmt.Errorf("%w: at most %d paths (the size where GitHub's tree API truncates a repository's file list)", ErrBadRequest, CheckCoverageMaxPaths)
	}
	if a.cfg.DB == nil {
		return CheckCoverageResult{}, fmt.Errorf("%w: CheckCoverage needs a database", ErrInternal)
	}
	rules, err := a.activeCoverageRules(ctx, repo)
	if err != nil {
		return CheckCoverageResult{}, err
	}
	paths := normalizePaths(q.Paths)
	counts := make([]int, len(rules))
	matcher := newCoverageMatcher(rules)
	for _, p := range paths {
		matcher.match(p, func(i int) { counts[i]++ })
	}
	now := time.Now().UTC()
	res := CheckCoverageResult{Repository: repo, Paths: len(paths), Rules: []RuleCoverage{}, MatchingNothing: []string{}}
	for i, r := range rules {
		if _, err := a.cfg.DB.ExecContext(ctx,
			`UPDATE smeldr_coverage_rules SET match_count = $1, checked_at = $2 WHERE id = $3`, counts[i], now, r.id); err != nil {
			return CheckCoverageResult{}, fmt.Errorf("%w: store the count of %s: %v", ErrInternal, r.address, err)
		}
		res.Rules = append(res.Rules, RuleCoverage{Rule: r.address, Area: r.areaAddress, Pattern: r.pattern, Matches: counts[i]})
		if counts[i] > 0 {
			a.resolveCoverageCondition(ctx, r.id, "the rule matches again")
			continue
		}
		res.MatchingNothing = append(res.MatchingNothing, r.address)
		if err := a.openCoverageCondition(ctx, r, repo); err != nil {
			return CheckCoverageResult{}, err
		}
	}
	return res, nil
}

// openCoverageCondition opens coverage-matches-nothing for rule r unless one
// is already open.
func (a *App) openCoverageCondition(ctx context.Context, r coverageRuleRow, repo string) error {
	resolver := a.signalResolver()
	open, err := resolver.openFor(ctx, "CoverageRule", r.id)
	if err != nil {
		return fmt.Errorf("%w: open conditions of %s: %v", ErrInternal, r.address, err)
	}
	for _, s := range open {
		if s.signalType == coverageMatchesNothing {
			return nil
		}
	}
	if err := resolver.emit(ctx, &Signal{
		Sender:      "system",
		Receiver:    OperationApproveChange,
		SignalType:  coverageMatchesNothing,
		Message:     fmt.Sprintf("Coverage rule %s of Area %s matches nothing in %s: %s", r.address, r.areaAddress, repo, r.pattern),
		SubjectType: "CoverageRule",
		SubjectID:   r.id,
	}); err != nil {
		return fmt.Errorf("%w: open coverage-matches-nothing for %s: %v", ErrInternal, r.address, err)
	}
	return nil
}

// resolveCoverageCondition resolves the open coverage-matches-nothing of the
// rule id, if any. Fail-open: a failed read is logged by the resolver's
// callers' pattern and leaves the condition for the next check.
func (a *App) resolveCoverageCondition(ctx context.Context, id, resolution string) {
	if a.cfg.DB == nil {
		return
	}
	resolver := a.signalResolver()
	open, err := resolver.openFor(ctx, "CoverageRule", id)
	if err != nil {
		return
	}
	var sigs []openSignal
	for _, s := range open {
		if s.signalType == coverageMatchesNothing {
			sigs = append(sigs, s)
		}
	}
	resolver.resolve(ctx, sigs, resolution)
}

// ruleLabel is a rule's address, or its ID before it has one.
func ruleLabel(r *CoverageRule) string {
	if r.Address != "" {
		return r.Address
	}
	return r.ID
}
