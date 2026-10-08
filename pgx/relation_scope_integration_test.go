//go:build integration

package pgx

import (
	"context"
	"testing"
	"time"

	smeldr "smeldr.dev/core"
)

// A relation-scoped grant on Postgres: an edge whose end is still ahead
// grants, an edge that has ended does not.
func TestPG_RelationScopeEnd(t *testing.T) {
	db, _ := isolatedDB(t)
	ctx := context.Background()
	if err := smeldr.CreateRelationTables(db); err != nil {
		t.Fatalf("CreateRelationTables: %v", err)
	}
	rs, err := smeldr.NewRelationStore(db)
	if err != nil {
		t.Fatalf("NewRelationStore: %v", err)
	}
	if err := rs.UpsertKind(ctx, smeldr.RelationKindDef{TypeName: "tagged", Mode: "asserted", Directional: true}); err != nil {
		t.Fatalf("UpsertKind: %v", err)
	}
	app := smeldr.New(smeldr.Config{BaseURL: "http://localhost", Secret: []byte(pgTestSecret), DB: db})
	store := smeldr.NewRoleStore(db)
	if err := app.Governance(store); err != nil {
		t.Fatalf("Governance: %v", err)
	}
	if err := store.DefineRole(ctx, smeldr.RoleDefinition{
		Name: "tag-scope", Operations: []string{"update"},
		ScopeMode: smeldr.ScopeDynamic, ScopeRelationKind: "tagged", ScopeDirection: "incoming",
	}); err != nil {
		t.Fatalf("DefineRole: %v", err)
	}
	alice := smeldr.NewContextWithUser(smeldr.User{ID: "alice", Roles: []smeldr.Role{smeldr.Editor}})

	cases := []struct {
		name string
		end  time.Duration
		want bool
	}{
		{"end later today", time.Minute, true},
		{"ended", -time.Minute, false},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			anchor := "t" + string(rune('1'+i))
			end := time.Now().UTC().Add(tc.end)
			if err := rs.Assert(alice, smeldr.RelationEdge{
				SourceType: "article", SourceID: "a1", TargetType: "tag", TargetID: anchor,
				RelationKind: "tagged", EdgeClass: "asserted", InvalidAt: &end,
			}); err != nil {
				t.Fatalf("Assert: %v", err)
			}
			tokenID := "pg-scope-" + smeldr.NewID()
			if _, err := store.Grant(ctx, smeldr.RoleGrant{TokenID: tokenID, RoleName: "tag-scope", ScopeAnchorID: anchor}); err != nil {
				t.Fatalf("Grant: %v", err)
			}
			ok, err := store.Authorized(ctx, tokenID, "update", smeldr.AuthTarget{ID: "a1"})
			if err != nil || ok != tc.want {
				t.Errorf("Authorized = %v, %v; want %v", ok, err, tc.want)
			}
			ok, err = store.RoleGranted(ctx, tokenID, "tag-scope", smeldr.AuthTarget{ID: "a1"})
			if err != nil || ok != tc.want {
				t.Errorf("RoleGranted = %v, %v; want %v", ok, err, tc.want)
			}
		})
	}
}
