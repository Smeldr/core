package smeldr

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
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
	// places holds the places ("Domain:<id>", "Area:<id>") a read grant on a
	// place kind anchors: the stored rest counts read the buckets they cover.
	places map[string]bool
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

// readsAny reports whether the scope reads at least one item of any type. A
// nil scope reads nothing.
func (s *ReadScope) readsAny() bool {
	if s == nil {
		return false
	}
	if s.all || len(s.anyOfTyp) > 0 {
		return true
	}
	for _, ids := range s.items {
		if len(ids) > 0 {
			return true
		}
	}
	return false
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
	rows, err := rs.db.QueryContext(ctx, tokenGrantsQuery, tokenID, time.Now().UTC())
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
	if anchorType, place := placeKindTargets[kind]; place && (direction == "incoming" || direction == "both") {
		// A place kind (D142): the place itself and everything in it, down the
		// hierarchy (an Area of a Domain and what is placed in it).
		members, err := placeMembers(ctx, db, anchorType, anchorID)
		if err != nil {
			return fmt.Errorf("anchor %s: %w", anchorID, err)
		}
		for _, m := range members {
			s.add(m.typeName, m.id)
		}
		if s.places == nil {
			s.places = map[string]bool{}
		}
		s.places[anchorType+":"+anchorID] = true
		if direction == "incoming" {
			return nil
		}
		direction = "outgoing" // "both": the outgoing hop below still applies
	}
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

// coversRestBucket reports whether the scope reads every item the rest count
// sweep stored in a bucket ([App.SweepRestCounts]): a scope that reads
// everything covers every bucket; a read grant on a Domain covers that
// Domain's bucket and those of its Areas (bucketDomain); one on an Area
// covers that Area's. The Organization's bucket holds unplaced items and is
// covered only by a scope that reads everything.
func (s *ReadScope) coversRestBucket(bucketType, bucketID, bucketDomain string) bool {
	switch {
	case s == nil:
		return false
	case s.all:
		return true
	case bucketType == "Domain":
		return s.places["Domain:"+bucketID]
	case bucketType == "Area":
		return s.places["Area:"+bucketID] || (bucketDomain != "" && s.places["Domain:"+bucketDomain])
	}
	return false
}

// cutPattern splits a static scope pattern "type:id" (or "type:*") at its first colon.
func cutPattern(p string) (typeName, id string, ok bool) {
	typeName, id, ok = strings.Cut(p, ":")
	return typeName, id, ok && typeName != "" && id != ""
}

// readScopeKey is the context key of an attached [ReadScope].
type readScopeKey struct{}

// scopedContext is a [Context] that carries a [ReadScope].
type scopedContext struct {
	Context
	scope *ReadScope
}

func (c *scopedContext) Value(key any) any {
	if key == (readScopeKey{}) {
		return c.scope
	}
	return c.Context.Value(key)
}

// WithReadScope attaches s to ctx, so every read the request makes uses the
// one scope loaded for it. When ctx is a [Context], the result is one too: it
// keeps the user and the request.
func WithReadScope(ctx context.Context, s *ReadScope) context.Context {
	if c, ok := ctx.(Context); ok {
		return &scopedContext{Context: c, scope: s}
	}
	return context.WithValue(ctx, readScopeKey{}, s)
}

// ReadScopeFrom returns the [ReadScope] attached to ctx by [WithReadScope].
func ReadScopeFrom(ctx context.Context) (*ReadScope, bool) {
	s, ok := ctx.Value(readScopeKey{}).(*ReadScope)
	return s, ok && s != nil
}

// AllReadScope returns a scope that reads everything.
func AllReadScope() *ReadScope { return &ReadScope{all: true} }

// TrustedRead marks ctx as a trusted in-process read that may see every
// record: the deliberate way for Go code (a job, a migration, a test) to read
// past the per-item read rules. Request surfaces never use it.
func TrustedRead(ctx context.Context) context.Context {
	return WithReadScope(ctx, AllReadScope())
}

// scopeFor returns the read scope attached to ctx, or else the caller's own:
// loaded from rs for the user in ctx when it is a [Context] (no user reads
// nothing under governance). Without governance (rs nil) the Go and MCP
// surfaces have no per-item read rules, so the scope reads everything, as
// before; the public HTTP routes keep their own legacy draft rule.
func scopeFor(ctx context.Context, rs *RoleStore) (*ReadScope, error) {
	if s, ok := ReadScopeFrom(ctx); ok {
		return s, nil
	}
	if rs == nil {
		return AllReadScope(), nil
	}
	var u User
	if c, ok := ctx.(interface{ User() User }); ok {
		u = c.User()
	}
	return LoadReadScope(ctx, rs, u)
}

// scopeIDFilter is s as a resolver's visibility rule for typeName: nil when s
// reads everything (the resolver then runs as before), otherwise whether s
// reads the item with an ID.
func scopeIDFilter(s *ReadScope, typeName string) func(id string) bool {
	if s.All() {
		return nil
	}
	return func(id string) bool { return s.Readable(typeName, id) }
}

// subjectHolder is an item that is about another item: a [Signal] with a
// subject. Its visibility also needs the subject's (answer 4 of design
// item-eecbbe69).
type subjectHolder interface {
	readSubject() (subjectType, subjectID string)
}

// signalSubjectVisible reports whether s reads the subject a Signal is about. A
// Signal with no subject has none to read. A subject named by anything other
// than its ID (a slug, an address) or of a type that is not an item matches no
// readable ID, so it is hidden from a caller who cannot read everything:
// fail-closed.
func signalSubjectVisible(s *ReadScope, subjectType, subjectID string) bool {
	return subjectType == "" || s.Readable(subjectType, subjectID)
}

// subjectRule is the Signal subject clause for s as a lookup rule, or nil when
// s is nil or reads everything (the lookup then runs as before).
func subjectRule(s *ReadScope) func(subjectType, subjectID string) bool {
	if s == nil || s.All() {
		return nil
	}
	return func(subjectType, subjectID string) bool { return signalSubjectVisible(s, subjectType, subjectID) }
}

// governanceByDB maps a database handle to the RoleStore that [App.Governance]
// wired on it, or to a nil RoleStore for a handle an App uses without
// governance, so a package function given only the handle (or a
// [RelationStore] on it) knows which read rules apply there.
var governanceByDB sync.Map

// governanceWired is set once any App in the process wires governance. From
// then on a handle that is not in governanceByDB, with no scope attached,
// fails closed instead of reading everything.
var governanceWired atomic.Bool

// registerGovernance records rs as the governance of db, or db as a handle
// without governance when rs is nil. A governed handle stays governed: a later
// registration without governance does not undo it. A handle that cannot be a
// map key is not recorded; under governance its package functions then need an
// attached scope.
func registerGovernance(db DB, rs *RoleStore) {
	if rs != nil {
		governanceWired.Store(true)
	}
	if db == nil || !comparableKey(db) {
		return
	}
	if rs == nil {
		governanceByDB.LoadOrStore(db, (*RoleStore)(nil))
		return
	}
	governanceByDB.Store(db, rs)
}

// governanceFor returns the RoleStore wired on db, and whether db was
// registered at all (a nil RoleStore with known true is a handle without
// governance).
func governanceFor(db DB) (rs *RoleStore, known bool) {
	if db == nil || !comparableKey(db) {
		return nil, false
	}
	v, known := governanceByDB.Load(db)
	rs, _ = v.(*RoleStore)
	return rs, known
}

// comparableKey reports whether v can be used as a map key without a panic.
func comparableKey(v any) (ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	_ = map[any]bool{v: true}
	return true
}

// packageScope is the read scope a package function on db applies:
//   - the one attached to ctx;
//   - else the caller's own under the governance wired on db (no user reads
//     nothing);
//   - else everything, when db is a handle an App uses without governance, or
//     no App in the process wires governance (legacy mode);
//   - else an [ErrInternal] error: governance is wired in the process, but not
//     on this handle (a wrapper, a transaction, an adapter), and no scope is
//     attached. It never reads everything.
func packageScope(ctx context.Context, db DB) (*ReadScope, error) {
	if s, ok := ReadScopeFrom(ctx); ok {
		return s, nil
	}
	rs, known := governanceFor(db)
	switch {
	case rs != nil:
		return scopeFor(ctx, rs)
	case known || !governanceWired.Load():
		return AllReadScope(), nil
	}
	return nil, fmt.Errorf("%w: no read scope attached: governance is wired in this process but not on this database handle; attach the caller's scope with WithReadScope", ErrInternal)
}
