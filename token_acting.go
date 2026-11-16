// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ActingUser returns the user a request carrying the token whose stored
// fingerprint is fp acts as, the same user [VerifyTokenString] returns for the
// raw token (its ID, its role, its class from its identity, TokenID fp), and
// the token's expiry. ok is false when no such token is stored, or it is
// revoked or expired, or its identity is not active.
//
// It is how a credential derived from a Smeldr token, such as an OAuth access
// token (D195), is checked against the token behind it at each request,
// without holding that token's value: a revoked token, or a lowered role,
// takes effect at once. A read that fails is an [ErrInternal] error, never a
// pass.
func (ts *TokenStore) ActingUser(ctx context.Context, fp string) (User, time.Time, bool, error) {
	var role, expires string
	var revoked, userID, class, identClass, identStatus sql.NullString
	err := ts.db.QueryRowContext(ctx,
		`SELECT t.role, t.expires_at, t.revoked_at, t.user_id, t.actor_class, i.class, i.status
		   FROM smeldr_tokens t LEFT JOIN smeldr_identities i ON i.id = t.identity_id
		  WHERE t.id = $1`, fp).Scan(&role, &expires, &revoked, &userID, &class, &identClass, &identStatus)
	if isNoSuchTable(err) || isNoSuchColumn(err, "identity_id") {
		// A store without identities (D165): the token's own class.
		err = ts.db.QueryRowContext(ctx,
			`SELECT role, expires_at, revoked_at, user_id, actor_class FROM smeldr_tokens WHERE id = $1`, fp).
			Scan(&role, &expires, &revoked, &userID, &class)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return GuestUser, time.Time{}, false, nil
	}
	if err != nil {
		return GuestUser, time.Time{}, false, fmt.Errorf("%w: ActingUser: %s", ErrInternal, err)
	}
	exp, perr := time.Parse(time.RFC3339, expires)
	switch {
	case perr != nil, !exp.After(time.Now()), revoked.Valid, !userID.Valid || userID.String == "",
		identStatus.Valid && identStatus.String != "" && identStatus.String != IdentityActive:
		return GuestUser, exp, false, nil
	}
	roles := []Role{Role(role)}
	if class.String != "" {
		roles = append(roles, Role(class.String))
	}
	return User{ID: userID.String, Roles: withIdentityClass(roles, identClass.String), TokenID: fp}, exp, true, nil
}
