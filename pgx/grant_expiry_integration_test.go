//go:build integration

package pgx

import (
	"context"
	"testing"
	"time"

	smeldr "smeldr.dev/core"
)

// T916 on Postgres: expires_at is a timestamptz, so a grant given in any zone
// expires at its moment, and the boot normalization leaves it alone.
func TestPG_GrantExpiryZones(t *testing.T) {
	db, _ := isolatedDB(t)
	ctx := context.Background()
	app, store := newPGApp(t, db)
	_ = app
	if err := smeldr.RegisterApproverRole(ctx, store); err != nil {
		t.Fatal(err)
	}
	east := time.FixedZone("CEST", 2*3600)
	expired := time.Now().In(east).Add(-time.Hour)
	live := time.Now().In(east).Add(time.Hour)
	for token, at := range map[string]*time.Time{"expired": &expired, "live": &live} {
		if _, err := store.Grant(ctx, smeldr.RoleGrant{TokenID: token, RoleName: smeldr.RoleApprover, ExpiresAt: at}); err != nil {
			t.Fatal(err)
		}
	}
	check := func(when string) {
		for token, want := range map[string]bool{"expired": false, "live": true} {
			if ok, err := store.Authorized(ctx, token, smeldr.OperationRatify, smeldr.AuthTarget{}); err != nil || ok != want {
				t.Errorf("%s, %s: Authorized = %v, %v; want %v", when, token, ok, err, want)
			}
		}
	}
	check("after grant")
	// A second governance boot runs the normalization again.
	again := smeldr.New(smeldr.Config{BaseURL: "http://localhost", Secret: []byte(pgTestSecret), DB: db})
	if err := again.Governance(smeldr.NewRoleStore(db)); err != nil {
		t.Fatal(err)
	}
	check("after a second boot")
}
