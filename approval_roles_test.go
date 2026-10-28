package smeldr

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// T884: the approval roles of D142, their migration and the agent rule.

// approvalFixture is an instance with governance, places, relations and a
// tokens table holding one token of each class.
type approvalFixture struct {
	app          *App
	db           *sql.DB
	rs           *RoleStore
	domain, area string
}

func newApprovalFixture(t *testing.T) approvalFixture {
	t.Helper()
	ctx := context.Background()
	app, db, rels := placesApp(t)
	if _, err := db.Exec(`CREATE TABLE smeldr_tokens (id TEXT PRIMARY KEY, name TEXT, role TEXT, expires_at TEXT, revoked_at TEXT, created_at TEXT, user_id TEXT, actor_class TEXT)`); err != nil {
		t.Fatal(err)
	}
	for _, tok := range []struct{ user, class string }{{"agent-1", "agent"}, {"job-1", "job"}, {"human-1", "human"}, {"human-2", "human"}, {"plain-1", ""}} {
		if _, err := db.Exec(`INSERT INTO smeldr_tokens (id, name, role, expires_at, created_at, user_id, actor_class) VALUES ($1, $1, 'editor', '2099-01-01T00:00:00Z', '2026-01-01T00:00:00Z', $2, $3)`, "fp-"+tok.user, tok.user, tok.class); err != nil {
			t.Fatal(err)
		}
	}
	rs := NewRoleStore(db)
	if err := app.Governance(rs); err != nil {
		t.Fatal(err)
	}
	for _, reg := range []func(context.Context, *RoleStore) error{RegisterApproverRole, RegisterDomainApproverRole, RegisterAreaApproverRole, RegisterItemApproverRole} {
		if err := reg(ctx, rs); err != nil {
			t.Fatal(err)
		}
	}
	f := approvalFixture{app: app, db: db, rs: rs, domain: "dom-1", area: "are-1"}
	for _, row := range []struct{ table, id string }{{"smeldr_domains", f.domain}, {"smeldr_areas", f.area}} {
		if _, err := db.Exec(`INSERT INTO `+row.table+` (id, slug, created_at, updated_at, name) VALUES ($1, $1, datetime('now'), datetime('now'), $1)`, row.id); err != nil {
			t.Fatal(err)
		}
	}
	if err := rels.Assert(ctx, RelationEdge{SourceType: "Area", SourceID: f.area, TargetType: "Domain", TargetID: f.domain,
		RelationKind: "belongs_to_domain", EdgeClass: "asserted", Attributes: json.RawMessage("{}")}); err != nil {
		t.Fatal(err)
	}
	return f
}

// as is a context whose actor is userID with the class roles of a token.
func as(userID string, roles ...Role) context.Context {
	return NewTestContext(User{ID: userID, Roles: append([]Role{Editor}, roles...)})
}

func TestApprovalRoles_Definitions(t *testing.T) {
	f := newApprovalFixture(t)
	for name, want := range map[string]string{
		RoleApprover:       `["ratify","approve-change","review"]`,
		RoleDomainApprover: `["ratify","approve-change"]`,
		RoleAreaApprover:   `["approve-change"]`,
		"item-approver":    `["ratify","approve-change","read"]`,
	} {
		var ops string
		if err := f.db.QueryRow(`SELECT operations FROM smeldr_roles WHERE name = $1`, name).Scan(&ops); err != nil || ops != want {
			t.Errorf("%s operations = %q (%v), want %s", name, ops, err, want)
		}
	}
}

// The deprecated wrappers define the new roles and never the old names.
func TestApprovalRoles_DeprecatedWrappers(t *testing.T) {
	db := setupGovernanceDB(t)
	rs := NewRoleStore(db)
	ctx := context.Background()
	if err := RegisterDecisionStewardRole(ctx, rs); err != nil {
		t.Fatal(err)
	}
	if err := RegisterDecisionDomainAdminRole(ctx, rs); err != nil {
		t.Fatal(err)
	}
	var names []string
	rows, _ := db.Query(`SELECT name FROM smeldr_roles WHERE name IN ('approver', 'domain-approver', 'decision-steward', 'decision-domain-admin') ORDER BY name`)
	for rows.Next() {
		var n string
		_ = rows.Scan(&n)
		names = append(names, n)
	}
	rows.Close()
	if strings.Join(names, ",") != "approver,domain-approver" {
		t.Errorf("roles = %v; want the new names only", names)
	}
}

func TestRatify_ApproverRoles(t *testing.T) {
	f := newApprovalFixture(t)
	ctx := context.Background()
	if _, err := f.db.Exec(`INSERT INTO smeldr_decisions (id, slug, status, created_at, updated_at) VALUES ('dec-in-area', 'dec-in-area', 'proposed', datetime('now'), datetime('now'))`); err != nil {
		t.Fatal(err)
	}
	placeEdge(t, f.db, "dec-in-area", "Area", f.area, "belongs_to_area", "asserted", nil)
	for _, g := range []RoleGrant{
		{TokenID: "approver", RoleName: RoleApprover},
		{TokenID: "dom", RoleName: RoleDomainApprover, ScopeAnchorID: f.domain},
		{TokenID: "area", RoleName: RoleAreaApprover, ScopeAnchorID: f.area},
		{TokenID: "item", RoleName: "item-approver", ScopeStatic: []string{"Decision:dec-in-area"}},
	} {
		if _, err := f.rs.Grant(ctx, g); err != nil {
			t.Fatalf("grant %s: %v", g.RoleName, err)
		}
	}
	target := AuthTarget{TypeName: "Decision", ID: "dec-in-area"}
	for tok, want := range map[string]bool{"approver": true, "dom": true, "area": false, "item": true} {
		if got, err := f.rs.Authorized(ctx, tok, OperationRatify, target); err != nil || got != want {
			t.Errorf("%s ratify = %v (%v), want %v", tok, got, err, want)
		}
		if got, _ := f.rs.Authorized(ctx, tok, OperationApproveChange, target); !got {
			t.Errorf("%s approve-change = false, want true", tok)
		}
	}
}

// An agent or job token never ratifies, even through a grant made before the
// rule existed; a human or unclassified one may.
func TestRatify_AgentNeverRatifies(t *testing.T) {
	for _, tc := range []struct {
		name  string
		roles []Role
		want  error
	}{
		{"agent", []Role{Agent}, ErrForbidden},
		{"job", []Role{Job}, ErrForbidden},
		{"human", []Role{Human}, nil},
		{"unclassified", nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, db, rs := setupTransitionItemApp(t)
			insertDecision(t, db, "dec-a", "dec-a-slug", "proposed")
			placeDecision(t, db, "dec-a")
			if err := RegisterApproverRole(context.Background(), rs); err != nil {
				t.Fatal(err)
			}
			// The grant is written directly, as one made before the rule.
			var roleID string
			if err := db.QueryRow(`SELECT id FROM smeldr_roles WHERE name = 'approver'`).Scan(&roleID); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`INSERT INTO smeldr_role_grants (id, token_id, role_id, scope_static, created_at) VALUES ('g1', 'tok', $1, '[]', datetime('now'))`, roleID); err != nil {
				t.Fatal(err)
			}
			_, err := app.TransitionItemVia(TrustedRead(as("tok", tc.roles...)), "mcp", "Decision", "dec-a-slug", "ratified", "")
			if tc.want == nil && err != nil || tc.want != nil && (!errors.Is(err, tc.want) || !strings.Contains(err.Error(), "never ratifies")) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
			// The D142 refusal carries its reason (T971).
			var re *RefusalError
			if tc.want != nil && (!errors.As(err, &re) || !strings.Contains(re.Reason, "never ratifies, supersedes or amends a Decision (D142)")) {
				t.Errorf("err = %v, want a RefusalError with the D142 reason", err)
			}
		})
	}
}

func TestGrant_AgentRule(t *testing.T) {
	f := newApprovalFixture(t)
	human := as("human-1", Human)
	if _, err := f.rs.Grant(context.Background(), RoleGrant{TokenID: "human-1", RoleName: RoleDomainApprover, ScopeAnchorID: f.domain}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.rs.Grant(context.Background(), RoleGrant{TokenID: "human-2", RoleName: "editor"}); err != nil {
		t.Fatal(err)
	}
	// No existence oracle (T971): a missing Area and an existing Area the
	// grantor holds nothing over are refused with the same text.
	reason := func(anchor string) string {
		_, err := f.rs.Grant(as("human-2", Human), RoleGrant{TokenID: "job-1", RoleName: RoleAreaApprover, ScopeAnchorID: anchor})
		var re *RefusalError
		if !errors.As(err, &re) {
			t.Fatalf("%s: err = %v, want a RefusalError", anchor, err)
		}
		return strings.ReplaceAll(re.Reason, anchor, "<anchor>")
	}
	if missing, unreadable := reason("no-such-area"), reason(f.area); missing != unreadable {
		t.Errorf("an existence oracle:\nmissing:    %s\nunreadable: %s", missing, unreadable)
	}
	for _, tc := range []struct {
		name  string
		ctx   context.Context
		grant RoleGrant
		want  string // "" passes
	}{
		{"agent approver", human, RoleGrant{TokenID: "agent-1", RoleName: RoleApprover}, "never holds ratify"},
		{"job domain-approver", human, RoleGrant{TokenID: "job-1", RoleName: RoleDomainApprover, ScopeAnchorID: f.domain}, "never holds ratify"},
		{"agent item-approver", human, RoleGrant{TokenID: "agent-1", RoleName: "item-approver", ScopeStatic: []string{"Decision:x"}}, "never holds ratify"},
		{"agent area-approver without anchor", human, RoleGrant{TokenID: "agent-1", RoleName: RoleAreaApprover}, "only within one Area"},
		{"agent area-approver on a Domain", human, RoleGrant{TokenID: "agent-1", RoleName: RoleAreaApprover, ScopeAnchorID: f.domain}, "holds no approve-change over Area"},
		{"no grantor", context.Background(), RoleGrant{TokenID: "agent-1", RoleName: RoleAreaApprover, ScopeAnchorID: f.area}, "needs a human grantor"},
		{"agent grantor", as("agent-1", Agent), RoleGrant{TokenID: "job-1", RoleName: RoleAreaApprover, ScopeAnchorID: f.area}, "an agent token never grants a role that ratifies or approves changes"},
		{"job grantor of an Area grant", as("job-1", Job), RoleGrant{TokenID: "agent-1", RoleName: RoleAreaApprover, ScopeAnchorID: f.area}, "must come from a human token; the grantor is job"},
		{"unclassified grantor", as("plain-1"), RoleGrant{TokenID: "agent-1", RoleName: RoleAreaApprover, ScopeAnchorID: f.area}, "the grantor is unclassified"},
		{"human grantor without authority", as("human-2", Human), RoleGrant{TokenID: "agent-1", RoleName: RoleAreaApprover, ScopeAnchorID: f.area}, "holds no approve-change"},
		{"human domain-approver delegates the Area", human, RoleGrant{TokenID: "agent-1", RoleName: RoleAreaApprover, ScopeAnchorID: f.area}, ""},
		{"human gets approver", human, RoleGrant{TokenID: "human-2", RoleName: RoleApprover}, ""},
		{"unclassified gets approver", human, RoleGrant{TokenID: "plain-1", RoleName: RoleApprover}, ""},
		{"no token row gets approver", human, RoleGrant{TokenID: "oauth-user", RoleName: RoleApprover}, ""},
		{"agent gets a role without approval", human, RoleGrant{TokenID: "agent-1", RoleName: "editor"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.rs.Grant(tc.ctx, tc.grant)
			if tc.want == "" && err != nil || tc.want != "" && (!errors.Is(err, ErrForbidden) || !strings.Contains(err.Error(), tc.want)) {
				t.Errorf("err = %v, want %q", err, tc.want)
			}
			// Each refusal of the agent rule carries its reason (T971).
			var re *RefusalError
			if tc.want != "" && (!errors.As(err, &re) || !strings.Contains(re.Reason, tc.want)) {
				t.Errorf("err = %v, want a RefusalError whose reason has %q", err, tc.want)
			}
		})
	}
	// The store's audit actor is a grantor too.
	if _, err := f.rs.WithAudit("human-1", nil).Grant(context.Background(), RoleGrant{TokenID: "job-1", RoleName: RoleAreaApprover, ScopeAnchorID: f.area}); err != nil {
		t.Errorf("audit actor as grantor: %v", err)
	}
}

// A class that cannot be read refuses the grant, fail closed; a tokens table
// without the class columns reads as unclassified.
func TestGrant_AgentRuleClassErrors(t *testing.T) {
	f := newApprovalFixture(t)
	if _, err := f.db.Exec(`ALTER TABLE smeldr_tokens RENAME TO smeldr_tokens_old`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`CREATE TABLE smeldr_tokens (id TEXT PRIMARY KEY, role TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.rs.Grant(context.Background(), RoleGrant{TokenID: "agent-1", RoleName: RoleApprover}); err != nil {
		t.Errorf("no class columns: %v; want unclassified, granted", err)
	}
	if _, err := f.db.Exec(`DROP TABLE smeldr_tokens`); err != nil {
		t.Fatal(err)
	}
	// Reading the class fails at run time (an integer overflow in SQLite).
	if _, err := f.db.Exec(`CREATE VIEW smeldr_tokens AS SELECT 'agent-1' AS user_id, 'agent' AS actor_class UNION ALL SELECT 'x', abs(-9223372036854775808)`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.rs.Grant(context.Background(), RoleGrant{TokenID: "x", RoleName: RoleApprover}); !errors.Is(err, ErrForbidden) || !strings.Contains(err.Error(), "cannot be read") {
		t.Errorf("unreadable class: %v; want forbidden", err)
	}
	// The same for the grantor of an Area grant.
	if _, err := f.rs.Grant(as("x"), RoleGrant{TokenID: "agent-1", RoleName: RoleAreaApprover, ScopeAnchorID: f.area}); err == nil {
		t.Error("unreadable grantor class: want an error")
	}
}

func TestGrant_AgentRuleRoleAndAreaErrors(t *testing.T) {
	f := newApprovalFixture(t)
	if _, err := f.db.Exec(`UPDATE smeldr_roles SET operations = 'not json' WHERE name = 'editor'`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.rs.Grant(as("human-1", Human), RoleGrant{TokenID: "agent-1", RoleName: "editor"}); err != nil {
		t.Errorf("malformed operations: %v; want no approval operation, granted", err)
	}
	if _, err := f.db.Exec(`DROP TABLE smeldr_areas`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.rs.Grant(as("human-1", Human), RoleGrant{TokenID: "agent-1", RoleName: RoleAreaApprover, ScopeAnchorID: f.area}); !errors.Is(err, ErrForbidden) {
		t.Errorf("no Areas table: %v; want forbidden", err)
	}
}

func TestMigrateApprovalRoles(t *testing.T) {
	ctx := context.Background()
	db := setupGovernanceDB(t)
	if err := CreateGovernanceAuditTable(db); err != nil {
		t.Fatal(err)
	}
	if err := CreateOrchestrationTables(db); err != nil {
		t.Fatal(err)
	}
	role := func(name, ops string) string {
		id := NewID()
		if _, err := db.Exec(`INSERT INTO smeldr_roles (id, name, operations, scope_mode, created_at, updated_at) VALUES ($1, $2, $3, 'global', datetime('now'), datetime('now'))`, id, name, ops); err != nil {
			t.Fatal(err)
		}
		return id
	}
	grant := func(id, token, roleID, anchor string) {
		var a any
		if anchor != "" {
			a = anchor
		}
		if _, err := db.Exec(`INSERT INTO smeldr_role_grants (id, token_id, role_id, scope_static, scope_anchor_id, created_at) VALUES ($1, $2, $3, '[]', $4, datetime('now'))`, id, token, roleID, a); err != nil {
			t.Fatal(err)
		}
	}
	steward := role("decision-steward", `["review","approve"]`)
	grant("g-peter", "peter", steward, "")
	domAdmin := role("decision-domain-admin", `["approve"]`)
	newDom := role("domain-approver", `["ratify","approve-change"]`)
	grant("g-move", "alice", domAdmin, "dom-1")
	grant("g-dup-old", "bob", domAdmin, "dom-1")
	grant("g-dup-new", "bob", newDom, "dom-1")
	role("custom", `["read","approve","ratify"]`)
	if _, err := db.Exec(`UPDATE smeldr_roles SET operations = '["read","ratify","approve-change","approve"]' WHERE name = 'admin'`); err != nil {
		t.Fatal(err)
	}
	for i, s := range []struct{ status, receiver, required string }{{"open", "approve", "approve"}, {"open", "core", "approve"}, {"resolved", "approve", "approve"}} {
		if _, err := db.Exec(`INSERT INTO smeldr_signals (id, slug, status, created_at, updated_at, receiver, required_role) VALUES ($1, $1, $2, datetime('now'), datetime('now'), $3, $4)`, "s"+string(rune('0'+i)), s.status, s.receiver, s.required); err != nil {
			t.Fatal(err)
		}
	}
	for run := 0; run < 2; run++ {
		if err := migrateGovernance(ctx, db); err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
	}
	ops := func(name string) string {
		var o string
		_ = db.QueryRow(`SELECT operations FROM smeldr_roles WHERE name = $1`, name).Scan(&o)
		return o
	}
	if ops("decision-steward") != "" || ops("decision-domain-admin") != "" {
		t.Error("an old role name survived")
	}
	if got := ops(RoleApprover); got != `["review","approve","ratify","approve-change"]` {
		t.Errorf("approver ops = %s", got)
	}
	if got := ops("custom"); got != `["read","approve","approve-change","ratify"]` {
		t.Errorf("custom ops = %s", got)
	}
	if got := ops("admin"); got != `["read"]` {
		t.Errorf("admin ops = %s; want no approval operation", got)
	}
	grantRole := func(id string) string {
		var n string
		_ = db.QueryRow(`SELECT r.name FROM smeldr_role_grants g JOIN smeldr_roles r ON r.id = g.role_id WHERE g.id = $1`, id).Scan(&n)
		return n
	}
	if grantRole("g-peter") != RoleApprover || grantRole("g-move") != RoleDomainApprover || grantRole("g-dup-new") != RoleDomainApprover || grantRole("g-dup-old") != "" {
		t.Errorf("grants: peter %q, move %q, dup-new %q, dup-old %q", grantRole("g-peter"), grantRole("g-move"), grantRole("g-dup-new"), grantRole("g-dup-old"))
	}
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM smeldr_governance_audit WHERE action = 'rename' AND target_kind = 'role'`).Scan(&n)
	if n != 2 {
		t.Errorf("rename audit rows = %d, want 2 (one per role, none on the second run)", n)
	}
	sig := func(id string) string {
		var r, q string
		_ = db.QueryRow(`SELECT receiver, required_role FROM smeldr_signals WHERE id = $1`, id).Scan(&r, &q)
		return r + "/" + q
	}
	if sig("s0") != "ratify/ratify" || sig("s1") != "core/ratify" || sig("s2") != "approve/approve" {
		t.Errorf("signals: %s %s %s; want open ones rewritten, the resolved one kept", sig("s0"), sig("s1"), sig("s2"))
	}
}

func TestAddApprovalOperations(t *testing.T) {
	for _, tc := range []struct{ in, want []string }{
		{[]string{"approve"}, []string{"approve", "ratify", "approve-change"}},
		{[]string{"read", "approve", "approve", "ratify"}, []string{"read", "approve", "approve-change", "ratify"}},
	} {
		got, changed := addApprovalOperations(tc.in)
		if !changed || strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("addApprovalOperations(%v) = %v %v, want %v", tc.in, got, changed, tc.want)
		}
	}
	for _, in := range [][]string{{"read"}, {"approve", "ratify", "approve-change"}} {
		if _, changed := addApprovalOperations(in); changed {
			t.Errorf("%v: changed, want unchanged", in)
		}
	}
}

// The migration fails on a broken roles table, and a malformed operations
// list is left as it is.
func TestMigrateApprovalRoles_Errors(t *testing.T) {
	ctx := context.Background()
	db := setupGovernanceDB(t)
	if _, err := db.Exec(`INSERT INTO smeldr_roles (id, name, operations, scope_mode, created_at, updated_at) VALUES ('bad', 'bad', 'not json', 'global', datetime('now'), datetime('now'))`); err != nil {
		t.Fatal(err)
	}
	if err := migrateApprovalRoles(ctx, db); err != nil {
		t.Errorf("malformed operations: %v", err)
	}
	if _, err := db.Exec(`DROP TABLE smeldr_role_grants`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO smeldr_roles (id, name, operations, scope_mode, created_at, updated_at) VALUES ('o', 'decision-steward', '[]', 'global', datetime('now'), datetime('now')), ('n', 'approver', '[]', 'global', datetime('now'), datetime('now'))`); err != nil {
		t.Fatal(err)
	}
	if err := migrateApprovalRoles(ctx, db); err == nil {
		t.Error("no grants table: want an error")
	}
	if _, err := db.Exec(`DROP TABLE smeldr_roles`); err != nil {
		t.Fatal(err)
	}
	if err := migrateApprovalRoles(ctx, db); err == nil {
		t.Error("no roles table: want an error")
	}
}

// The ratification condition is addressed to ratify, so the tokens that hold
// it are the ones it reaches.
func TestRatificationRequired_RoutesToRatify(t *testing.T) {
	s := ratificationSignal("dec-1")
	if s.Receiver != OperationRatify || s.RequiredOperation != OperationRatify {
		t.Errorf("receiver %q, required %q; want ratify", s.Receiver, s.RequiredOperation)
	}
}

func TestGrantFounder(t *testing.T) {
	f := newApprovalFixture(t)
	f.rs.setProvenanceStore(NewProvenanceStore(f.db))
	ctx := as("human-1", Human)
	if err := f.app.GrantFounder(ctx, ""); err == nil {
		t.Error("empty user ID: want an error")
	}
	for i := 0; i < 2; i++ {
		if err := f.app.GrantFounder(ctx, "founder"); err != nil {
			t.Fatalf("GrantFounder run %d: %v", i, err)
		}
	}
	grants, err := f.rs.ListGrants(context.Background(), "founder")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, g := range grants {
		names = append(names, g.RoleName)
		if g.Reason != founderReason {
			t.Errorf("%s reason = %q", g.RoleName, g.Reason)
		}
	}
	if strings.Join(names, ",") != "admin,approver" && strings.Join(names, ",") != "approver,admin" {
		t.Errorf("founder grants = %v; want admin and approver once each", names)
	}
	if err := (&App{}).GrantFounder(ctx, "x"); !errors.Is(err, ErrInternal) {
		t.Errorf("no governance: %v", err)
	}
	if _, err := f.db.Exec(`DROP TABLE smeldr_role_grants`); err != nil {
		t.Fatal(err)
	}
	if err := f.app.GrantFounder(ctx, "y"); err == nil {
		t.Error("broken grants table: want an error")
	}
}

// actorClass prefers the class signed into the token in ctx.
func TestActorClass(t *testing.T) {
	f := newApprovalFixture(t)
	for _, tc := range []struct {
		ctx  context.Context
		id   string
		want string
	}{
		{as("agent-1", Agent), "agent-1", "agent"},
		{as("x"), "human-1", "human"},
		{context.Background(), "plain-1", ""},
		{context.Background(), "nobody", ""},
	} {
		if got, err := actorClass(tc.ctx, f.db, tc.id); err != nil || got != tc.want {
			t.Errorf("actorClass(%s) = %q (%v), want %q", tc.id, got, err, tc.want)
		}
	}
}

// Every statement of the migration that fails fails the migration.
func TestMigrateApprovalRoles_DatabaseErrors(t *testing.T) {
	seed := func(t *testing.T) *sql.DB {
		db := setupGovernanceDB(t)
		if err := CreateGovernanceAuditTable(db); err != nil {
			t.Fatal(err)
		}
		if err := CreateOrchestrationTables(db); err != nil {
			t.Fatal(err)
		}
		for _, r := range []struct{ id, name, ops string }{
			{"r-steward", "decision-steward", `["approve"]`},
			{"r-approver", "approver", `["ratify"]`},
			{"r-domadmin", "decision-domain-admin", `["approve"]`},
		} {
			if _, err := db.Exec(`INSERT INTO smeldr_roles (id, name, operations, scope_mode, created_at, updated_at) VALUES ($1, $2, $3, 'global', datetime('now'), datetime('now'))`, r.id, r.name, r.ops); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := db.Exec(`INSERT INTO smeldr_role_grants (id, token_id, role_id, scope_static, created_at) VALUES ('g', 'tok', 'r-steward', '[]', datetime('now'))`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO smeldr_signals (id, slug, status, created_at, updated_at, receiver) VALUES ('s', 's', 'open', datetime('now'), datetime('now'), 'approve')`); err != nil {
			t.Fatal(err)
		}
		return db
	}
	for _, match := range []string{
		`"smeldr_governance_audit" WHERE 1=0`,
		`"smeldr_signals" WHERE 1=0`,
		"SELECT id FROM smeldr_roles WHERE name",
		"UPDATE smeldr_roles SET name",
		"UPDATE smeldr_role_grants SET role_id",
		"DELETE FROM smeldr_role_grants",
		"DELETE FROM smeldr_roles",
		"INSERT INTO smeldr_governance_audit",
		"SELECT id, operations FROM smeldr_roles",
		"UPDATE smeldr_roles SET operations",
		"UPDATE smeldr_signals",
	} {
		t.Run(match, func(t *testing.T) {
			db := seed(t)
			if err := migrateApprovalRoles(context.Background(), failAddressDB{DB: db, match: match}); err == nil {
				t.Errorf("failing %q: want an error", match)
			}
		})
	}
}

// A role or grant lookup that fails refuses the grant.
func TestGrant_AgentRuleDatabaseErrors(t *testing.T) {
	f := newApprovalFixture(t)
	if _, err := f.rs.Grant(context.Background(), RoleGrant{TokenID: "human-1", RoleName: RoleDomainApprover, ScopeAnchorID: f.domain}); err != nil {
		t.Fatal(err)
	}
	for _, match := range []string{"SELECT operations, scope_mode, scope_relation_kind", "FROM smeldr_role_grants g"} {
		rs := &RoleStore{db: failAddressDB{DB: f.db, match: match}}
		_, err := rs.Grant(as("human-1", Human), RoleGrant{TokenID: "agent-1", RoleName: RoleAreaApprover, ScopeAnchorID: f.area})
		if err == nil {
			t.Errorf("failing %q: want an error", match)
		}
	}
}

// GrantFounder fails when the approver role cannot be defined.
func TestGrantFounder_DefineFails(t *testing.T) {
	f := newApprovalFixture(t)
	f.app.governance = &RoleStore{db: failAddressDB{DB: f.db, match: "smeldr_roles"}}
	if err := f.app.GrantFounder(context.Background(), "x"); err == nil {
		t.Error("want an error")
	}
}

// An agent grantor never grants a role that ratifies or approves changes (D142,
// T984), whatever the grantee's class: otherwise an agent with admin hands
// approver to a human token and ratifies through it. Roles without an approval
// operation stay grantable. A job grantor is pinned as it is today, pending
// the job question T984 leaves open (Cloud's job token carries a member's
// approver over on reissue).
func TestGrant_AgentGrantor(t *testing.T) {
	f := newApprovalFixture(t)
	agent := as("agent-1", Agent)
	for _, tc := range []struct {
		name  string
		ctx   context.Context
		grant RoleGrant
		want  string // "" passes
	}{
		{"approver to a human", agent, RoleGrant{TokenID: "human-2", RoleName: RoleApprover}, `role "approver" holds ratify`},
		{"approver to a human, carried over by reissue", agent, RoleGrant{TokenID: "human-2", RoleName: RoleApprover, Reason: "carried over by reissue from human-1"}, `role "approver" holds ratify`},
		{"approver to an unclassified token", agent, RoleGrant{TokenID: "plain-1", RoleName: RoleApprover}, `role "approver" holds ratify`},
		{"approver to an agent", agent, RoleGrant{TokenID: "agent-1", RoleName: RoleApprover}, `role "approver" holds ratify`},
		{"domain-approver to a human", agent, RoleGrant{TokenID: "human-2", RoleName: RoleDomainApprover, ScopeAnchorID: f.domain}, "holds ratify"},
		{"area-approver to a human", agent, RoleGrant{TokenID: "human-2", RoleName: RoleAreaApprover, ScopeAnchorID: f.area}, `role "area-approver" holds approve-change`},
		{"editor to a human", agent, RoleGrant{TokenID: "human-2", RoleName: "editor"}, ""},
		{"admin to a human", agent, RoleGrant{TokenID: "human-2", RoleName: "admin"}, ""},
		{"job grants approver to a human (today's behaviour, pending the job question)", as("job-1", Job), RoleGrant{TokenID: "human-2", RoleName: RoleApprover, Reason: "carried over by reissue from human-1"}, ""},
		{"human grants approver to a human", as("human-1", Human), RoleGrant{TokenID: "human-2", RoleName: RoleApprover}, ""},
		{"unclassified grants approver to a human", as("plain-1"), RoleGrant{TokenID: "human-2", RoleName: RoleApprover}, ""},
		{"no grantor grants approver", context.Background(), RoleGrant{TokenID: "human-2", RoleName: RoleApprover}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.rs.Grant(tc.ctx, tc.grant)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("err = %v; want granted", err)
				}
				return
			}
			var re *RefusalError
			if !errors.Is(err, ErrForbidden) || !errors.As(err, &re) || !strings.Contains(re.Reason, "an agent token never grants a role that ratifies or approves changes (D142)") || !strings.Contains(re.Reason, tc.want) {
				t.Fatalf("err = %v; want a RefusalError with %q", err, tc.want)
			}
		})
	}
	// The store's audit actor is a grantor too.
	if _, err := f.rs.WithAudit("agent-1", nil).Grant(context.Background(), RoleGrant{TokenID: "human-2", RoleName: RoleApprover}); !errors.Is(err, ErrForbidden) {
		t.Errorf("agent audit actor as grantor = %v; want forbidden", err)
	}
	// A token's role field is not a grant (D43): an agent mints an agent token
	// with role admin, and the new token holds no grant.
	ts := NewTokenStore(f.db, testSecret)
	_, id, err := ts.CreateClassified(agent, "minted", "admin", Agent, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if grants, err := f.rs.ListGrants(context.Background(), id); err != nil || len(grants) != 0 {
		t.Errorf("grants of the minted token = %v (%v); want none", grants, err)
	}
}
