package smeldr

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

// T911 (D147): who approves a place, for anyone who reads it.

func newApproversFixture(t *testing.T) areaGateFixture {
	t.Helper()
	f := newAreaGateFixture(t)
	ctx := context.Background()
	for _, tok := range []struct{ id, user, class, created, expires, revoked string }{
		{"fp-human-3", "human-3", "human", "2026-01-01T00:00:00Z", "2099-01-01T00:00:00Z", ""},
		{"fp-old-1", "old-1", "human", "2026-01-01T00:00:00Z", "2099-01-01T00:00:00Z", ""},
		{"fp-gone-1", "gone-1", "human", "2026-01-01T00:00:00Z", "2099-01-01T00:00:00Z", "2026-02-01T00:00:00Z"},
		{"fp-late-1", "late-1", "human", "2026-01-01T00:00:00Z", "2020-01-01T00:00:00Z", ""},
		{"two-old", "two-1", "human", "2026-01-01T00:00:00Z", "2099-01-01T00:00:00Z", ""},
		{"two-new", "two-1", "human", "2026-03-01T00:00:00Z", "2099-01-01T00:00:00Z", ""},
		{"fp-reader-1", "reader-1", "human", "2026-01-01T00:00:00Z", "2099-01-01T00:00:00Z", ""},
	} {
		var revoked any
		if tok.revoked != "" {
			revoked = tok.revoked
		}
		if _, err := f.db.Exec(`INSERT INTO smeldr_tokens (id, name, role, expires_at, revoked_at, created_at, user_id, actor_class) VALUES ($1, $1, 'editor', $2, $3, $4, $5, $6)`,
			tok.id, tok.expires, revoked, tok.created, tok.user, tok.class); err != nil {
			t.Fatal(err)
		}
	}
	if err := RegisterDomainReaderRole(ctx, f.rs); err != nil {
		t.Fatal(err)
	}
	past := time.Now().UTC().Add(-time.Hour)
	for _, g := range []RoleGrant{
		{TokenID: "human-3", RoleName: RoleDomainApprover, ScopeAnchorID: f.dom2},
		{TokenID: "old-1", RoleName: RoleDomainApprover, ScopeAnchorID: f.domain, ExpiresAt: &past},
		{TokenID: "gone-1", RoleName: RoleDomainApprover, ScopeAnchorID: f.domain},
		{TokenID: "late-1", RoleName: RoleDomainApprover, ScopeAnchorID: f.domain},
		{TokenID: "two-1", RoleName: RoleDomainApprover, ScopeAnchorID: f.domain},
		{TokenID: "reader-1", RoleName: "domain-reader", ScopeAnchorID: f.domain},
	} {
		if _, err := f.rs.Grant(ctx, g); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func TestApproversOf(t *testing.T) {
	f := newApproversFixture(t)
	reader := as("reader-1")
	admin := as("human-1", Human)
	approvers := func(anchor string) []PlaceApprover {
		return []PlaceApprover{
			{Name: "fp-agent-1", Role: RoleApprover, Class: "agent", AnchorAddress: anchor},
			{Name: "fp-human-1", Role: RoleApprover, Class: "human", AnchorAddress: anchor},
		}
	}
	dom1 := []PlaceApprover{
		{Name: "fp-human-2", Role: RoleDomainApprover, Class: "human", AnchorAddress: "DOM1"},
		{Name: "two-new", Role: RoleDomainApprover, Class: "human", AnchorAddress: "DOM1"},
	}
	are1 := append(append([]PlaceApprover{}, dom1...), PlaceApprover{Name: "agent-x", Role: RoleAreaApprover, Class: "agent", AnchorAddress: "ARE1"})
	for _, tc := range []struct {
		name  string
		ctx   context.Context
		place string
		want  []PlaceApprover
	}{
		// A domain-reader reads its Domain and its Areas, not the
		// Organization: the approvers' anchor stays empty.
		{"reader on its Domain", reader, "DOM1", append(approvers(""), dom1...)},
		{"reader on its Domain by slug", reader, "dom-1", append(approvers(""), dom1...)},
		{"reader on its Area", reader, "ARE1", append(approvers(""), are1...)},
		{"admin on the Domain", admin, "dom-1", append(approvers("ORG1"), dom1...)},
		{"admin on the Organization", admin, "ORG1", approvers("ORG1")},
		{"admin on the other Domain", admin, "DOM2", append(approvers("ORG1"), PlaceApprover{Name: "fp-human-3", Role: RoleDomainApprover, Class: "human", AnchorAddress: "DOM2"})},
		{"admin on an Area of the other Domain", admin, "are-2", append(approvers("ORG1"), PlaceApprover{Name: "fp-human-3", Role: RoleDomainApprover, Class: "human", AnchorAddress: "DOM2"})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := f.app.ApproversOf(tc.ctx, tc.place)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got  %+v\nwant %+v", got, tc.want)
			}
		})
	}
	// Unreadable and unknown answer alike.
	var msgs []string
	for _, place := range []string{"DOM2", "ARE2", "ORG1", "NOPE9"} {
		_, err := f.app.ApproversOf(reader, place)
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("%s: %v, want not found", place, err)
			continue
		}
		msgs = append(msgs, strings.Replace(err.Error(), place, "X", 1))
	}
	for _, m := range msgs[1:] {
		if m != msgs[0] {
			t.Errorf("refusals differ: %q and %q", m, msgs[0])
		}
	}
	if _, err := f.app.ApproversOf(reader, "  "); !errors.Is(err, ErrBadRequest) {
		t.Errorf("empty place: %v", err)
	}
	// Exactly the four keys.
	got, err := f.app.ApproversOf(admin, "ARE1")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(got[0])
	var keys map[string]any
	if err := json.Unmarshal(raw, &keys); err != nil || len(keys) != 4 {
		t.Errorf("keys: %s", raw)
	}
}

func TestApproversOf_Edges(t *testing.T) {
	// No governance: the place is still checked, the list is empty.
	app, db, _ := placesApp(t)
	if _, err := db.Exec(`INSERT INTO smeldr_domains (id, slug, created_at, updated_at, name, address) VALUES ('d', 'd', datetime('now'), datetime('now'), 'D', 'DOM7')`); err != nil {
		t.Fatal(err)
	}
	if got, err := app.ApproversOf(context.Background(), "DOM7"); err != nil || len(got) != 0 {
		t.Errorf("no governance: %v %v", got, err)
	}
	if _, err := app.ApproversOf(context.Background(), "DOM8"); !errors.Is(err, ErrNotFound) {
		t.Errorf("no governance, unknown: %v", err)
	}
	// No database.
	if _, err := New(Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16")}).ApproversOf(context.Background(), "DOM7"); !errors.Is(err, ErrInternal) {
		t.Errorf("no database: %v", err)
	}

	// A place with no approver grants at all.
	f := newApproversFixture(t)
	admin := as("human-1", Human)
	if _, err := f.db.Exec(`DELETE FROM smeldr_role_grants WHERE role_id IN (SELECT id FROM smeldr_roles WHERE name IN ('approver', 'domain-approver', 'area-approver'))`); err != nil {
		t.Fatal(err)
	}
	if got, err := f.app.ApproversOf(admin, "DOM1"); err != nil || len(got) != 0 {
		t.Errorf("no grants: %v %v", got, err)
	}

	// No Organization row: the approvers' anchor is empty. An Area with no
	// Domain lists approvers and its area-approvers only.
	o := newApproversFixture(t)
	if _, err := o.db.Exec(`DELETE FROM smeldr_organizations`); err != nil {
		t.Fatal(err)
	}
	if _, err := o.db.Exec(`UPDATE smeldr_relations SET invalid_at = '2000-01-01T00:00:00Z' WHERE source_id = 'are-1' AND relation_kind = 'belongs_to_domain'`); err != nil {
		t.Fatal(err)
	}
	got, err := o.app.ApproversOf(WithReadScope(admin, AllReadScope()), "ARE1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].AnchorAddress != "" || got[2].Role != RoleAreaApprover {
		t.Errorf("no Organization, no Domain: %+v", got)
	}

	// An old tokens table without actor_class reads as unclassified.
	g := newApproversFixture(t)
	if _, err := g.db.Exec(`ALTER TABLE smeldr_tokens DROP COLUMN actor_class`); err != nil {
		t.Fatal(err)
	}
	got, err = g.app.ApproversOf(as("human-1"), "DOM2")
	if err != nil || len(got) == 0 || got[0].Class != "unclassified" {
		t.Errorf("no actor_class: %+v %v", got, err)
	}

	// Read failures are internal.
	for _, tc := range []struct{ name, stmt, place string }{
		{"tokens", `ALTER TABLE smeldr_tokens RENAME TO tokens_gone`, "DOM1"},
		{"area domains", `ALTER TABLE smeldr_relations RENAME TO relations_gone`, "ARE1"},
		{"organization", `ALTER TABLE smeldr_organizations RENAME TO orgs_gone`, "DOM1"},
		{"places", `ALTER TABLE smeldr_areas RENAME TO areas_gone`, "DOM1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newApproversFixture(t)
			// The admin reads everything without a grant read of its own.
			ctx := WithReadScope(as("human-1", Human), AllReadScope())
			if _, err := h.db.Exec(tc.stmt); err != nil {
				t.Fatal(err)
			}
			if _, err := h.app.ApproversOf(ctx, tc.place); !errors.Is(err, ErrInternal) {
				t.Errorf("%v, want internal", err)
			}
		})
	}
	h := newApproversFixture(t)
	ctx := WithReadScope(as("human-1", Human), AllReadScope())
	if _, err := h.db.Exec(`ALTER TABLE smeldr_role_grants RENAME TO grants_gone`); err != nil {
		t.Fatal(err)
	}
	if _, err := h.app.ApproversOf(ctx, "DOM1"); !errors.Is(err, ErrInternal) {
		t.Errorf("grants: %v, want internal", err)
	}
}
