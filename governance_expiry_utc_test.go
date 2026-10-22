package smeldr

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// T916: a grant's expiry holds whatever zone it was given in, and stored
// expiries in other forms are rewritten to UTC at boot.

func TestGrantExpiry_Zones(t *testing.T) {
	db := setupGovernanceDB(t)
	store := NewRoleStore(db)
	ctx := context.Background()
	if err := store.DefineRole(ctx, RoleDefinition{Name: "z-steward", Operations: []string{"steward", "ratify"}, ScopeMode: ScopeStatic}); err != nil {
		t.Fatal(err)
	}
	east, west := time.FixedZone("CEST", 2*3600), time.FixedZone("EST", -5*3600)
	for _, tc := range []struct {
		name    string
		expires time.Time
		want    bool
	}{
		{"east, expired an hour ago", time.Now().In(east).Add(-time.Hour), false},
		{"east, an hour left", time.Now().In(east).Add(time.Hour), true},
		{"west, half an hour left", time.Now().In(west).Add(30 * time.Minute), true},
		{"west, expired a minute ago", time.Now().In(west).Add(-time.Minute), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			token := NewID()
			given := tc.expires
			if _, err := store.Grant(ctx, RoleGrant{TokenID: token, RoleName: "z-steward", ScopeStatic: []string{"RuleType:money"}, ExpiresAt: &given}); err != nil {
				t.Fatal(err)
			}
			if !given.Equal(tc.expires) || given.Location() != tc.expires.Location() {
				t.Errorf("Grant changed the caller's ExpiresAt: %v", given)
			}
			target := AuthTarget{TypeName: "RuleType", ID: "money"}
			if ok, err := store.Authorized(ctx, token, OperationRatify, target); err != nil || ok != tc.want {
				t.Errorf("Authorized = %v, %v; want %v", ok, err, tc.want)
			}
			if ok, err := store.RoleGranted(ctx, token, "z-steward", target); err != nil || ok != tc.want {
				t.Errorf("RoleGranted = %v, %v; want %v", ok, err, tc.want)
			}
			types, err := store.StewardedRuleTypes(ctx, token)
			if err != nil || (len(types) == 1) != tc.want {
				t.Errorf("StewardedRuleTypes = %v, %v; want held %v", types, err, tc.want)
			}
		})
	}
}

func TestGrantExpiry_Normalize(t *testing.T) {
	db := setupGovernanceDB(t)
	store := NewRoleStore(db)
	ctx := context.Background()
	if err := store.DefineRole(ctx, RoleDefinition{Name: "z-role", Operations: []string{"ratify"}, ScopeMode: ScopeGlobal}); err != nil {
		t.Fatal(err)
	}
	var roleID string
	if err := db.QueryRow(`SELECT id FROM smeldr_roles WHERE name = 'z-role'`).Scan(&roleID); err != nil {
		t.Fatal(err)
	}
	past := time.Now().UTC().Add(-time.Hour).Round(0) // no monotonic reading in String
	future := time.Now().UTC().Add(time.Hour).Round(0)
	east := time.FixedZone("CEST", 2*3600)
	for _, row := range []struct {
		id      string
		expires any
	}{
		{"g-cest", past.In(east).String()},
		{"g-rfc", past.Format(time.RFC3339)},
		{"g-future", future.In(east).Format(time.RFC3339)},
		{"g-bad", "next tuesday"},
		{"g-blob", []byte(past.In(east).String())},
		{"g-null", nil},
	} {
		if _, err := db.Exec(`INSERT INTO smeldr_role_grants (id, token_id, role_id, scope_static, expires_at, created_at) VALUES ($1, $1, $2, '[]', $3, $4)`,
			row.id, roleID, row.expires, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	// Before: the CEST row compares as unexpired.
	if ok, _ := store.Authorized(ctx, "g-cest", OperationRatify, AuthTarget{}); !ok {
		t.Fatal("setup: the CEST row should read as unexpired before the fix")
	}
	if err := normalizeRoleGrantExpiry(ctx, db); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]bool{"g-cest": false, "g-rfc": false, "g-future": true, "g-bad": false, "g-blob": false, "g-null": true} {
		if ok, err := store.Authorized(ctx, id, OperationRatify, AuthTarget{}); err != nil || ok != want {
			t.Errorf("%s: Authorized = %v, %v; want %v", id, ok, err, want)
		}
	}
	// The CEST row is now its own time in UTC, not the epoch.
	var cest string
	if err := db.QueryRow(`SELECT expires_at FROM smeldr_role_grants WHERE id = 'g-cest'`).Scan(&cest); err != nil {
		t.Fatal(err)
	}
	if got, err := ParseTimestamp(cest); err != nil || !got.Equal(past) || !strings.HasSuffix(cest, roleGrantExpiryUTCSuffix) {
		t.Errorf("g-cest stored %q (%v), want %v in UTC", cest, err, past)
	}
	var before, after string
	if err := db.QueryRow(`SELECT expires_at FROM smeldr_role_grants WHERE id = 'g-future'`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := normalizeRoleGrantExpiry(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT expires_at FROM smeldr_role_grants WHERE id = 'g-future'`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Errorf("second run changed %q to %q", before, after)
	}
	var null any
	if err := db.QueryRow(`SELECT expires_at FROM smeldr_role_grants WHERE id = 'g-null'`).Scan(&null); err != nil || null != nil {
		t.Errorf("NULL expiry became %v (%v)", null, err)
	}
	// A rewrite that fails stops the boot step, and migrateGovernance with it.
	if _, err := db.Exec(`INSERT INTO smeldr_role_grants (id, token_id, role_id, scope_static, expires_at, created_at) VALUES ('g-new', 'g-new', $1, '[]', '2030-01-01T00:00:00Z', $2)`, roleID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER no_rewrite BEFORE UPDATE ON smeldr_role_grants BEGIN SELECT RAISE(ABORT, 'read only'); END`); err != nil {
		t.Fatal(err)
	}
	if err := normalizeRoleGrantExpiry(ctx, db); err == nil {
		t.Error("failed rewrite: no error")
	}
	if err := migrateGovernance(ctx, db); err == nil {
		t.Error("migrateGovernance: no error from a failed rewrite")
	}
	if _, err := db.Exec(`DROP TRIGGER no_rewrite`); err != nil {
		t.Fatal(err)
	}
	// A broken table fails the boot step.
	if _, err := db.Exec(`ALTER TABLE smeldr_role_grants RENAME TO grants_gone`); err != nil {
		t.Fatal(err)
	}
	if err := normalizeRoleGrantExpiry(ctx, db); err == nil {
		t.Error("missing table: no error")
	}
}

// Two boot helpers with no test of their own.
func TestGrantExpiry_Neighbours(t *testing.T) {
	db := setupGovernanceDB(t)
	if _, err := db.Exec(`CREATE TABLE smeldr_decisions (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := EnsureDecisionTensionColumns(context.Background(), db); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`SELECT tension_rule_id, tension_reason FROM smeldr_decisions`); err != nil {
		t.Errorf("columns: %v", err)
	}
	if err := EnsureDecisionTensionColumns(context.Background(), failingDB{DB: db, match: "smeldr_decisions", err: errors.New("down")}); err == nil {
		t.Error("failing database: no error")
	}
	app := New(Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16")})
	prefix, slug, id, err := decodePreviewToken(app.GeneratePreviewTokenForID("/posts", "draft", "id-7"), []byte("supersecretkey16"))
	if err != nil || prefix != "/posts" || slug != "draft" || id != "id-7" {
		t.Errorf("preview token: %q %q %q %v", prefix, slug, id, err)
	}
}
