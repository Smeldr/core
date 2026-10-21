package smeldr

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// T910 (D146): loosening an Area's approval gate (its approval mode, its
// being active, its coverage rules) needs ratify over the Area; adding a rule
// needs approve-change over it.

type areaGateFixture struct {
	placeAuthorityFixture
	areas *Module[*Area]
	rules *Module[*CoverageRule]
	are2  *Area
}

// actorCtx is a module context whose actor is userID with class roles.
func actorCtx(userID string, roles ...Role) Context {
	return NewTestContext(User{ID: userID, Roles: append([]Role{Editor}, roles...)})
}

func newAreaGateFixture(t *testing.T) areaGateFixture {
	t.Helper()
	f := areaGateFixture{placeAuthorityFixture: newPlaceAuthorityFixture(t)}
	f.areas, f.rules = findModule[*Area](t, f.app), findModule[*CoverageRule](t, f.app)
	ctx := context.Background()
	// A second Area, in the other Domain.
	if _, err := f.db.Exec(`INSERT INTO smeldr_areas (id, slug, created_at, updated_at, name, address) VALUES ('are-2', 'are-2', $1, $1, 'Elsewhere', 'ARE2')`, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE smeldr_areas SET created_at = $1, updated_at = $1 WHERE id = 'are-1'`, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	f.are2 = &Area{Node: Node{ID: "are-2", Slug: "are-2"}, RecordAddress: RecordAddress{Address: "ARE2"}}
	if err := f.rels.Assert(ctx, RelationEdge{SourceType: "Area", SourceID: f.are2.ID, TargetType: "Domain", TargetID: f.dom2,
		RelationKind: "belongs_to_domain", EdgeClass: "asserted", Attributes: json.RawMessage("{}")}); err != nil {
		t.Fatal(err)
	}
	// An agent area-approver on are-1, delegated by a human approver.
	if _, err := f.db.Exec(`INSERT INTO smeldr_tokens (id, name, role, expires_at, created_at, user_id, actor_class) VALUES ('fp-agent-x', 'agent-x', 'editor', '2099-01-01T00:00:00Z', '2026-01-01T00:00:00Z', 'agent-x', 'agent')`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.rs.Grant(as("human-1", Human), RoleGrant{TokenID: "agent-x", RoleName: RoleAreaApprover, ScopeAnchorID: f.area}); err != nil {
		t.Fatal(err)
	}
	for _, reader := range []string{"agent-x", "agent-1"} {
		if _, err := f.rs.Grant(ctx, RoleGrant{TokenID: reader, RoleName: "editor"}); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func wantGateRefusal(t *testing.T, err error, msg string) {
	t.Helper()
	if !errors.Is(err, ErrForbidden) || !strings.Contains(err.Error(), msg) {
		t.Errorf("err = %v; want forbidden with %q", err, msg)
	}
}

func TestAreaGate_ApprovalMode(t *testing.T) {
	f := newAreaGateFixture(t)
	set := func(ctx Context, slug, mode string) error {
		_, err := f.areas.MCPUpdate(ctx, slug, map[string]any{"approval_mode": mode})
		return err
	}
	// Unchanged fields and the same mode pass at the Author tier.
	if _, err := f.areas.MCPUpdate(actorCtx("plain-1"), f.area, map[string]any{"name": "Billing", "approval_mode": ApprovalModeCommentOnly}); err != nil {
		t.Errorf("name and same mode: %v", err)
	}
	wantGateRefusal(t, set(actorCtx("plain-1"), f.area, ApprovalModeRequiresApproval), "approval mode needs ratify over ARE1")
	wantGateRefusal(t, set(actorCtx("agent-1", Agent), f.area, ApprovalModeRequiresApproval), "an agent token never ratifies")
	wantGateRefusal(t, set(actorCtx("agent-x", Agent), f.area, ApprovalModeRequiresApproval), "never ratifies")
	if err := set(actorCtx("human-2", Human), f.area, ApprovalModeRequiresApproval); err != nil {
		t.Errorf("domain-approver in its Domain: %v", err)
	}
	// Loosening it again is gated the same way.
	wantGateRefusal(t, set(actorCtx("plain-1"), f.area, ApprovalModeCommentOnly), "needs ratify over ARE1")
	wantGateRefusal(t, set(actorCtx("human-2", Human), f.are2.Slug, ApprovalModeRequiresApproval), "needs ratify over "+f.are2.Address)
	if err := set(actorCtx("human-1", Human), f.are2.Slug, ApprovalModeRequiresApproval); err != nil {
		t.Errorf("approver: %v", err)
	}
}

func TestAreaGate_ArchiveAndDelete(t *testing.T) {
	f := newAreaGateFixture(t)
	move := func(ctx Context, slug, to string) error {
		_, err := f.app.TransitionItem(ctx, "Area", slug, to)
		return err
	}
	if err := move(actorCtx("plain-1"), f.area, "archived"); !errors.Is(err, ErrForbidden) {
		t.Errorf("Author archives: %v", err)
	}
	if err := move(actorCtx("agent-1", Agent), f.area, "archived"); !errors.Is(err, ErrForbidden) {
		t.Errorf("agent archives: %v", err)
	}
	if err := move(actorCtx("human-2", Human), f.are2.Slug, "archived"); !errors.Is(err, ErrForbidden) {
		t.Errorf("domain-approver archives another Domain's Area: %v", err)
	}
	if err := move(actorCtx("human-2", Human), f.area, "archived"); err != nil {
		t.Errorf("domain-approver archives its Area: %v", err)
	}
	if err := move(actorCtx("plain-1"), f.area, "active"); !errors.Is(err, ErrForbidden) {
		t.Errorf("Author reactivates: %v", err)
	}
	if err := move(actorCtx("human-2", Human), f.area, "active"); err != nil {
		t.Errorf("domain-approver reactivates: %v", err)
	}
	wantGateRefusal(t, f.areas.MCPDelete(actorCtx("plain-1"), f.are2.Slug), "deleting "+f.are2.Address+" needs ratify over "+f.are2.Address)
	if err := f.areas.MCPDelete(actorCtx("human-1", Human), f.are2.Slug); err != nil {
		t.Errorf("approver deletes: %v", err)
	}
}

func TestAreaGate_CoverageRules(t *testing.T) {
	f := newAreaGateFixture(t)
	create := func(ctx Context, area string) (*CoverageRule, error) {
		out, err := f.rules.MCPCreate(ctx, map[string]any{"area": area, "repository": "acme/app", "pattern": "a/**"})
		if err != nil {
			return nil, err
		}
		return asItem[*CoverageRule](out), nil
	}
	// The agent area-approver adds a rule to its Area, not to another.
	rule, err := create(actorCtx("agent-x", Agent), "ARE1")
	if err != nil {
		t.Fatalf("agent area-approver adds a rule: %v", err)
	}
	if _, err := create(actorCtx("agent-x", Agent), f.are2.Address); !errors.Is(err, ErrForbidden) || !strings.Contains(err.Error(), "needs approve-change over "+f.are2.Address) {
		t.Errorf("agent in another Area: %v", err)
	}
	if _, err := create(actorCtx("plain-1"), "ARE1"); !errors.Is(err, ErrForbidden) {
		t.Errorf("Author adds a rule: %v", err)
	}
	// Changing what it covers needs ratify: the agent is refused, the
	// domain-approver allowed; a change of nothing passes.
	change := func(ctx Context, fields map[string]any) error {
		_, err := f.rules.MCPUpdate(ctx, rule.Slug, fields)
		return err
	}
	if err := change(actorCtx("agent-x", Agent), map[string]any{"pattern": "a/**"}); err != nil {
		t.Errorf("unchanged pattern: %v", err)
	}
	wantGateRefusal(t, change(actorCtx("agent-x", Agent), map[string]any{"pattern": "b/**"}), "never ratifies")
	wantGateRefusal(t, change(actorCtx("plain-1"), map[string]any{"repository": "acme/other"}), "pattern, repository or kind needs ratify over ARE1")
	if err := change(actorCtx("human-2", Human), map[string]any{"pattern": "b/**"}); err != nil {
		t.Errorf("domain-approver changes: %v", err)
	}
	// Archive and reactivate through the flow gate.
	if _, err := f.app.TransitionItem(actorCtx("agent-x", Agent), "CoverageRule", rule.Slug, "archived"); !errors.Is(err, ErrForbidden) {
		t.Errorf("agent archives a rule: %v", err)
	}
	if _, err := f.app.TransitionItem(actorCtx("human-2", Human), "CoverageRule", rule.Slug, "archived"); err != nil {
		t.Errorf("domain-approver archives: %v", err)
	}
	if _, err := f.app.TransitionItem(actorCtx("plain-1"), "CoverageRule", rule.Slug, "active"); !errors.Is(err, ErrForbidden) {
		t.Errorf("Author reactivates: %v", err)
	}
	if _, err := f.app.TransitionItem(actorCtx("human-2", Human), "CoverageRule", rule.Slug, "active"); err != nil {
		t.Errorf("domain-approver reactivates: %v", err)
	}
	// Delete.
	wantGateRefusal(t, f.rules.MCPDelete(actorCtx("agent-x", Agent), rule.Slug), "never ratifies")
	if err := f.rules.MCPDelete(actorCtx("human-1", Human), rule.Slug); err != nil {
		t.Errorf("approver deletes: %v", err)
	}
}

// The shared check: no governance skips, no actor is refused, a failed read
// is internal.
func TestRequireOperationOver(t *testing.T) {
	if err := requireOperationOver(context.Background(), nil, OperationRatify, AuthTarget{}, "x", "y", "D146"); err != nil {
		t.Errorf("no governance: %v", err)
	}
	f := newAreaGateFixture(t)
	wantGateRefusal(t, requireOperationOver(context.Background(), f.rs, OperationRatify, AuthTarget{TypeName: "Area", ID: f.area}, "x", "ARE1", "D146"), "no actor is known")
	if _, err := f.db.Exec(`ALTER TABLE smeldr_role_grants RENAME TO grants_gone`); err != nil {
		t.Fatal(err)
	}
	if err := requireOperationOver(actorCtx("human-1", Human), f.rs, OperationRatify, AuthTarget{TypeName: "Area", ID: f.area}, "x", "ARE1", "D146"); !errors.Is(err, ErrInternal) {
		t.Errorf("grant read fails: %v", err)
	}
}

// The labels fall back to the ID; an invalid mode is refused before the
// gate; an old empty mode reads as comment-only, so setting it is no change.
func TestAreaGate_Helpers(t *testing.T) {
	if got := areaLabel(&Area{Node: Node{ID: "are-x"}}); got != "are-x" {
		t.Errorf("areaLabel = %q", got)
	}
	if got := ruleLabel(&CoverageRule{Node: Node{ID: "cov-x"}}); got != "cov-x" {
		t.Errorf("ruleLabel = %q", got)
	}
	f := newAreaGateFixture(t)
	if err := f.app.prepareArea(actorCtx("plain-1"), nil, &Area{ApprovalMode: "sometimes"}); err == nil {
		t.Error("invalid mode accepted")
	}
	if err := f.app.prepareArea(actorCtx("plain-1"), &Area{Node: Node{ID: f.area}}, &Area{ApprovalMode: ApprovalModeCommentOnly}); err != nil {
		t.Errorf("empty to comment-only: %v", err)
	}
}
