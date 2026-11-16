package smeldr

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

// T1287, D195: the user a stored token acts as, read by its fingerprint.
func TestTokenStore_ActingUser(t *testing.T) {
	ts, _, _ := reasonTokens(t)
	ctx := context.Background()
	admin := as("admin-1", Admin, Human)
	raw, uid, err := ts.CreateClassified(admin, "agent token", "author", Agent, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	fp := fingerprint(raw)
	u, exp, ok, err := ts.ActingUser(ctx, fp)
	if err != nil || !ok {
		t.Fatalf("ActingUser = %v %v", ok, err)
	}
	if u.ID != uid || u.TokenID != fp || !slices.Contains(u.Roles, Author) || !slices.Contains(u.Roles, Agent) {
		t.Errorf("user = %+v; want %s, author, agent, TokenID", u, uid)
	}
	if d := time.Until(exp); d < 59*time.Minute || d > time.Hour+time.Minute {
		t.Errorf("expiry in %v; want about an hour", d)
	}

	if _, _, ok, err := ts.ActingUser(ctx, "no-such-token"); ok || err != nil {
		t.Errorf("unknown token: %v %v; want false, nil", ok, err)
	}
	if _, err := ts.db.ExecContext(ctx, `UPDATE smeldr_tokens SET expires_at = $1 WHERE id = $2`, time.Now().Add(-time.Minute).UTC().Format(time.RFC3339), fp); err != nil {
		t.Fatal(err)
	}
	if _, _, ok, _ := ts.ActingUser(ctx, fp); ok {
		t.Error("an expired token acts")
	}
	if _, err := ts.db.ExecContext(ctx, `UPDATE smeldr_tokens SET expires_at = $1 WHERE id = $2`, time.Now().Add(time.Hour).UTC().Format(time.RFC3339), fp); err != nil {
		t.Fatal(err)
	}
	if err := ts.Revoke(ctx, fp); err != nil {
		t.Fatal(err)
	}
	if _, _, ok, _ := ts.ActingUser(ctx, fp); ok {
		t.Error("a revoked token acts")
	}
	broken := &TokenStore{db: faultDB{DB: ts.db, match: "FROM smeldr_tokens t"}}
	if _, _, ok, err := broken.ActingUser(ctx, fp); ok || !errors.Is(err, ErrInternal) {
		t.Errorf("failed read: %v %v; want false, ErrInternal", ok, err)
	}
}
