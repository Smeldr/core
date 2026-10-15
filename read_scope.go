package smeldr

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"
)

// ReadScope is what one caller may read, per item (design item-eecbbe69). It is
// loaded once per request, or once per event-stream connection, by
// [App.ReadScope] or [LoadReadScope], and then answers every per-item check in
// memory. A nil *ReadScope reads nothing.
//
// Its answers equal [RoleStore.Authorized] for the operation "read": a global
// grant reads everything, a static grant reads its "type:id" and "type:*"
// patterns, and a dynamic grant reads the items one live, asserted hop from its
// anchor (not the anchor itself).
type ReadScope struct {
	all      bool
	items    map[string]map[string]bool // type -> id: the readable items
	anyOfTyp map[string]bool            // "type" from a "type:*" pattern
}

// All reports whether the scope reads every item: a legacy Author or higher
// (no governance wired), or a caller holding a global grant with "read".
func (s *ReadScope) All() bool { return s != nil && s.all }

// Readable reports whether the scope may read the item of typeName with this
// ID. It is an in-memory lookup.
func (s *ReadScope) Readable(typeName, id string) bool {
	if s == nil {
		return false
	}
	return s.all || s.anyOfTyp[typeName] || s.items[typeName][id]
}

// TypeReadable reports whether the scope may read at least one item of
// typeName: the floor a tool or route checks before filtering per item.
func (s *ReadScope) TypeReadable(typeName string) bool {
	if s == nil {
		return false
	}
	return s.all || s.anyOfTyp[typeName] || len(s.items[typeName]) > 0
}

// ReadScope loads the read scope of the caller in ctx through the App's
// RoleStore, or the legacy role check when governance is not wired. See
// [LoadReadScope].
func (a *App) ReadScope(ctx Context) (*ReadScope, error) {
	return LoadReadScope(ctx, a.governance, ctx.User())
}

// LoadReadScope loads what user may read, for a caller that holds a
// [RoleStore]; [App.ReadScope] is the usual entry point.
//
//   - rs nil (governance not wired): All is user.HasRole(Author), the legacy
//     draft-reading rule; otherwise the scope is empty.
//   - user without an ID: an empty scope.
//   - otherwise one query loads the user's unexpired grants whose role holds
//     "read". A global grant stops there. Each dynamic grant costs one query per
//     direction, and loads its anchor's whole one-hop item set, so its cost
//     grows with the anchor's size.
//
// Any query failure returns an [ErrInternal] error and no scope: a half-loaded
// scope would hide records silently. The caller answers an internal error,
// never not-found.
func LoadReadScope(ctx context.Context, rs *RoleStore, user User) (*ReadScope, error) {
	s := &ReadScope{items: map[string]map[string]bool{}, anyOfTyp: map[string]bool{}}
	if rs == nil {
		s.all = user.HasRole(Author)
		return s, nil
	}
	if user.ID == "" {
		return s, nil
	}
	if err := s.load(ctx, rs, user.ID); err != nil {
		slog.ErrorContext(ctx, "smeldr: read scope could not be loaded", "token", user.ID, "error", err)
		return nil, fmt.Errorf("%w: read scope: %s", ErrInternal, err)
	}
	return s, nil
}

// load fills s from tokenID's grants, mirroring [RoleStore.Authorized] for
// "read".
func (s *ReadScope) load(ctx context.Context, rs *RoleStore, tokenID string) error {
	rows, err := rs.db.QueryContext(ctx,
		`SELECT g.scope_static, g.scope_anchor_id, r.operations,
		        r.scope_mode, r.scope_relation_kind, r.scope_direction
		   FROM smeldr_role_grants g
		   JOIN smeldr_roles r ON r.id = g.role_id
		  WHERE g.token_id = $1 AND (g.expires_at IS NULL OR g.expires_at > $2)`,
		tokenID, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("query grants: %w", err)
	}
	// Collect before any further query: SQLite allows one active statement.
	var grants []authorizedGrant
	for rows.Next() {
		var g authorizedGrant
		if err := rows.Scan(&g.staticJSON, &g.anchorID, &g.opsJSON, &g.scopeMode, &g.relKind, &g.relDir); err != nil {
			rows.Close()
			return fmt.Errorf("scan grant: %w", err)
		}
		grants = append(grants, g)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("grant rows: %w", err)
	}
	rows.Close()

	for _, g := range grants {
		var ops []string
		if json.Unmarshal([]byte(g.opsJSON), &ops) != nil || !slices.Contains(ops, "read") {
			continue // malformed or no read: skipped, as Authorized does
		}
		switch ScopeMode(g.scopeMode) {
		case ScopeGlobal:
			s.all = true
			return nil
		case ScopeStatic:
			var patterns []string
			if json.Unmarshal([]byte(g.staticJSON), &patterns) != nil {
				continue
			}
			for _, p := range patterns {
				typeName, id, ok := cutPattern(p)
				if !ok {
					continue
				}
				if id == "*" {
					s.anyOfTyp[typeName] = true
				} else {
					s.add(typeName, id)
				}
			}
		case ScopeDynamic:
			if !g.anchorID.Valid || g.anchorID.String == "" {
				continue
			}
			if err := s.loadAnchor(ctx, rs.db, g.relKind.String, g.relDir.String, g.anchorID.String); err != nil {
				return err
			}
		}
	}
	return nil
}

// loadAnchor adds the items one live, asserted hop from anchorID, with the
// same predicate as [RoleStore] relationExists: "incoming" means the item points
// to the anchor, "outgoing" that the anchor points to the item.
func (s *ReadScope) loadAnchor(ctx context.Context, db DB, kind, direction, anchorID string) error {
	now := time.Now().UTC()
	const live = ` AND relation_kind = $2 AND edge_class = 'asserted' AND (invalid_at IS NULL OR invalid_at > $3)`
	if direction == "incoming" || direction == "both" {
		if err := s.addRows(ctx, db, `SELECT source_type, source_id FROM smeldr_relations WHERE target_id = $1`+live, anchorID, kind, now); err != nil {
			return fmt.Errorf("anchor %s incoming: %w", anchorID, err)
		}
	}
	if direction == "outgoing" || direction == "both" {
		if err := s.addRows(ctx, db, `SELECT target_type, target_id FROM smeldr_relations WHERE source_id = $1`+live, anchorID, kind, now); err != nil {
			return fmt.Errorf("anchor %s outgoing: %w", anchorID, err)
		}
	}
	return nil
}

// addRows adds every (type, id) row the query returns.
func (s *ReadScope) addRows(ctx context.Context, db DB, query string, args ...any) error {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var typeName, id sql.NullString
		if err := rows.Scan(&typeName, &id); err != nil {
			return err
		}
		s.add(typeName.String, id.String)
	}
	return rows.Err()
}

func (s *ReadScope) add(typeName, id string) {
	ids := s.items[typeName]
	if ids == nil {
		ids = map[string]bool{}
		s.items[typeName] = ids
	}
	ids[id] = true
}

// cutPattern splits a static scope pattern "type:id" (or "type:*") at its first colon.
func cutPattern(p string) (typeName, id string, ok bool) {
	typeName, id, ok = strings.Cut(p, ":")
	return typeName, id, ok && typeName != "" && id != ""
}
