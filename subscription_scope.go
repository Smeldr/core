package smeldr

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// scopeCacheTTL is how long a cached read scope is used before it is loaded
// again: the backstop for a grant or an edge that expired without an event
// (binding addition E).
var scopeCacheTTL = 60 * time.Second

// scopeEntry is one owner's cached read scope.
type scopeEntry struct {
	scope   *ReadScope
	anchors map[string]bool // the anchor IDs of the owner's dynamic grants
	loaded  time.Time
}

// scopeCache holds the read scope of every owner the stream or a webhook
// delivers to, so a delivery does not load a scope per event. It is dropped
// on a grant change, on a relation change at an anchor, and when an entry is
// older than scopeCacheTTL.
type scopeCache struct {
	mu      sync.Mutex
	entries map[string]*scopeEntry
}

// get returns owner's scope, loading it when missing or stale. Without
// governance it is nil: everything is readable, as [scopeFor] has it.
func (a *App) cachedScope(ctx context.Context, owner string) (*ReadScope, error) {
	if a.governance == nil || owner == legacyAdminOwner {
		return nil, nil
	}
	c := &a.scopes
	c.mu.Lock()
	e := c.entries[owner]
	c.mu.Unlock()
	if e != nil && time.Since(e.loaded) < scopeCacheTTL {
		return e.scope, nil
	}
	scope, err := LoadReadScope(ctx, a.governance, User{ID: owner})
	if err != nil {
		return nil, err
	}
	anchors, err := grantAnchors(ctx, a.cfg.DB, owner)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	if c.entries == nil {
		c.entries = map[string]*scopeEntry{}
	}
	c.entries[owner] = &scopeEntry{scope: scope, anchors: anchors, loaded: time.Now()}
	c.mu.Unlock()
	return scope, nil
}

// grantAnchors are the anchor IDs of owner's dynamic grants.
func grantAnchors(ctx context.Context, db DB, owner string) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT scope_anchor_id FROM smeldr_role_grants WHERE token_id = $1 AND scope_anchor_id IS NOT NULL AND scope_anchor_id <> ''`, owner)
	if err != nil {
		return nil, fmt.Errorf("%w: grant anchors: %s", ErrInternal, err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			out[id] = true
		}
	}
	return out, rows.Err()
}

// scopesChanged drops the cached scope of owner (every owner when empty) and
// tells their stream connections to reload.
func (a *App) scopesChanged(owner string) {
	a.scopes.mu.Lock()
	if owner == "" {
		a.scopes.entries = nil
	} else {
		delete(a.scopes.entries, owner)
	}
	a.scopes.mu.Unlock()
	if a.eventBroadcaster != nil {
		a.eventBroadcaster.reloadToken(owner)
	}
}

// relationChanged drops the cached scope of every owner with a dynamic grant
// anchored at either end of a relation that began or ended.
func (a *App) relationChanged(sourceID, targetID string) {
	a.scopes.mu.Lock()
	var owners []string
	for owner, e := range a.scopes.entries {
		if e.anchors[sourceID] || e.anchors[targetID] {
			owners = append(owners, owner)
		}
	}
	a.scopes.mu.Unlock()
	for _, o := range owners {
		a.scopesChanged(o)
	}
}

// tokenRevoked closes the stream connections of a revoked token and drops
// every cached scope.
func (a *App) tokenRevoked(fingerprint string) {
	a.scopesChanged("")
	if a.eventBroadcaster != nil {
		a.eventBroadcaster.closeFingerprint(fingerprint)
	}
}

// CanRead reports whether user may read the item typeName id, by the same
// read rules as every read surface, through the App's scope cache: a grant
// change takes effect at once, and an expired grant within a minute. Without
// governance a legacy Author or higher reads everything. [ErrInternal] when
// the scope cannot be loaded.
func (a *App) CanRead(ctx context.Context, user User, typeName, id string) (bool, error) {
	if a.governance == nil {
		return user.HasRole(Author), nil
	}
	if user.ID == "" {
		return false, nil
	}
	scope, err := a.cachedScope(ctx, user.ID)
	if err != nil {
		return false, err
	}
	return scope.Readable(typeName, id), nil
}

// wireScopeHooks connects the grant, revocation and relation hooks to the
// scope cache (App.Handler).
func (a *App) wireScopeHooks() {
	if a.governance != nil {
		a.governance.onGrantChange = a.scopesChanged
	}
	if a.tokenStore != nil {
		a.tokenStore.onRevoke = a.tokenRevoked
	}
	if a.relationStore != nil {
		a.relationStore.onRelationChange = a.relationChanged
	}
}

// bearerFingerprint is the stored fingerprint of the request's bearer token,
// the ID a revocation names; empty without one.
func bearerFingerprint(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return ""
	}
	sum := sha256.Sum256([]byte(strings.TrimPrefix(h, "Bearer ")))
	return hex.EncodeToString(sum[:])
}

// IsRevoked reports whether the token with the stored fingerprint (its
// [TokenRecord] ID) is revoked. A fingerprint the store does not hold is not
// revoked: a token without a store row is checked by its signature alone.
func (ts *TokenStore) IsRevoked(ctx context.Context, fingerprint string) (bool, error) {
	var revoked any
	err := ts.db.QueryRowContext(ctx, `SELECT revoked_at FROM smeldr_tokens WHERE id = $1`, fingerprint).Scan(&revoked)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("%w: token revocation: %s", ErrInternal, err)
	}
	return revoked != nil, nil
}

// legacyAdminOwner owns the subscription of a webhook endpoint created
// before subscriptions, or by a caller with no identity: it reads everything
// (read rules answer 5).
const legacyAdminOwner = "legacy-admin"
