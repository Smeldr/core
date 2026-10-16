package smeldr

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"
)

// SubscriptionAnchor is the related_to dimension of a [SubscriptionFilter]:
// the items one live, asserted hop from Anchor by relation Kind, in Direction
// ("incoming": the item points to the anchor; "outgoing": the anchor points
// to the item; "both", the default).
type SubscriptionAnchor struct {
	// Anchor is the anchor item as "Type:ident" when given, its ID once saved.
	Anchor string `json:"anchor"`
	// AnchorType is the anchor's type, set when the subscription is saved.
	AnchorType string `json:"anchor_type,omitempty"`
	// Kind is the relation kind, such as belongs_to_domain.
	Kind string `json:"kind"`
	// Direction is incoming, outgoing or both (empty: both).
	Direction string `json:"direction,omitempty"`
}

// SubscriptionFilter says which events a [Subscription] delivers (D116). It is
// a conjunction: each dimension narrows, and an empty one means any.
type SubscriptionFilter struct {
	// Types are registered content type names. A relation event matches when
	// either end is of one of them.
	Types []string `json:"types,omitempty"`
	// Events are event kinds: created, updated, transitioned, published,
	// unpublished, scheduled, archived, deleted, relation.asserted,
	// relation.ended.
	Events []string `json:"events,omitempty"`
	// EventNames are full event names, such as "task.transitioned": the
	// events a webhook endpoint was created for.
	EventNames []string `json:"event_names,omitempty"`
	// FromStates and ToStates are the states of a transition (ToStates also
	// the state a created item starts in).
	FromStates []string `json:"from_states,omitempty"`
	ToStates   []string `json:"to_states,omitempty"`
	// Items are "Type:ident" pairs (an address, slug or ID), saved as
	// "Type:ID" under the owner's read scope. A relation event matches when
	// either end is one of them.
	Items []string `json:"items,omitempty"`
	// RelatedTo narrows to the items one hop from an anchor.
	RelatedTo *SubscriptionAnchor `json:"related_to,omitempty"`
	// Fields narrows on routing values: band (Task, Goal), scope (Decision),
	// receiver and sender (Signal), priority (Task, as a decimal string), and
	// the channel field of a runtime-defined type.
	Fields map[string][]string `json:"fields,omitempty"`
	// SignalKinds narrows Signal events to conditions or requests (a history
	// type matches neither).
	SignalKinds []SignalKind `json:"signal_kinds,omitempty"`
	// AddressedToMe narrows to Signals whose receiver is one of the owner's
	// routing names: its User.ID, the operations it holds, and the receiver
	// values its subscriptions name.
	AddressedToMe bool `json:"addressed_to_me,omitempty"`
	// SentByMe narrows to Signals whose sender is one of the owner's routing
	// names (the universal "outcomes of my requests" default).
	SentByMe bool `json:"sent_by_me,omitempty"`
}

// Subscription is an owner's standing request for events (D116): a filter,
// a delivery (the event stream, or a webhook endpoint) and a cursor. It is a
// core record with a D110 address (prefix N). It is read and changed only by
// its owner or a holder of administer, through [App.CreateSubscription] and
// its siblings; it has no generic module surface.
type Subscription struct {
	ID          string             `json:"id"`
	Address     string             `json:"address"`
	AddressNo   int64              `json:"address_no"`
	Name        string             `json:"name"`
	Owner       string             `json:"owner"`
	Filter      SubscriptionFilter `json:"filter"`
	Delivery    string             `json:"delivery"`
	IncludeOwn  bool               `json:"include_own"`
	Paused      bool               `json:"paused"`
	Cursor      string             `json:"cursor"`
	DefaultKind string             `json:"default_kind,omitempty"`
	LastActor   string             `json:"last_actor,omitempty"`
	CreatedAt   time.Time          `json:"created_at"`
	UpdatedAt   time.Time          `json:"updated_at"`
}

// SubscriptionSpec is what [App.CreateSubscription] takes.
type SubscriptionSpec struct {
	// Owner is the token User.ID the subscription delivers to. Empty means
	// the caller; another token needs administer.
	Owner string
	// Name is the owner's own label, at most [TitleMaxLength] characters.
	Name   string
	Filter SubscriptionFilter
	// Delivery is "stream" (the default) or "webhook:<endpoint id>", which
	// needs administer.
	Delivery   string
	IncludeOwn bool
	Paused     bool
}

// SubscriptionUpdate is what [App.UpdateSubscription] changes: nil leaves a
// field as it is.
type SubscriptionUpdate struct {
	Name       *string
	Filter     *SubscriptionFilter
	IncludeOwn *bool
	Paused     *bool
}

// The universal default subscriptions every token has (design 3.4). They are
// created on first use, cannot be deleted, and can be paused.
const (
	SubscriptionRequestsToMe          = "requests_to_me"
	SubscriptionConditionsICanResolve = "conditions_i_can_resolve"
	SubscriptionOutcomesOfMyRequests  = "outcomes_of_my_requests"
)

// Subscription limits (design 3.6).
const (
	MaxSubscriptionsPerOwner = 100
	MaxSubscriptionItems     = 1000
)

// DeliveryStream is a subscription delivered on the event stream.
const DeliveryStream = "stream"

// deliveryWebhookPrefix starts a webhook delivery: "webhook:<endpoint id>".
const deliveryWebhookPrefix = "webhook:"

// subscriptionEventKinds are the event kinds a filter may name.
var subscriptionEventKinds = map[string]bool{
	"created": true, "updated": true, "transitioned": true, "published": true, "unpublished": true,
	"scheduled": true, "archived": true, "deleted": true, eventRelationAsserted: true, eventRelationEnded: true,
}

// subscriptionFieldNames are the routing fields of the compiled types.
var subscriptionFieldNames = map[string]bool{"band": true, "scope": true, "receiver": true, "sender": true, "priority": true}

// createSubscriptionTableSQL is the subscription table and its indexes.
var createSubscriptionTableSQL = []string{
	`CREATE TABLE IF NOT EXISTS smeldr_subscriptions (
		id           TEXT PRIMARY KEY,
		slug         TEXT NOT NULL UNIQUE,
		status       TEXT NOT NULL DEFAULT 'active',
		created_at   TIMESTAMPTZ NOT NULL,
		updated_at   TIMESTAMPTZ NOT NULL,
		name         TEXT NOT NULL DEFAULT '',
		owner        TEXT NOT NULL,
		filter       TEXT NOT NULL DEFAULT '{}',
		delivery     TEXT NOT NULL DEFAULT 'stream',
		include_own  BOOLEAN NOT NULL DEFAULT FALSE,
		paused       BOOLEAN NOT NULL DEFAULT FALSE,
		cursor       TEXT NOT NULL DEFAULT '',
		default_kind TEXT NOT NULL DEFAULT '',
		last_actor   TEXT NOT NULL DEFAULT ''
	)`,
	`CREATE INDEX IF NOT EXISTS idx_smeldr_subscriptions_owner ON smeldr_subscriptions (owner)`,
	// One of each universal default per owner, however many first connects
	// race to create them.
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_smeldr_subscriptions_default ON smeldr_subscriptions (owner, default_kind) WHERE default_kind <> ''`,
}

// CreateSubscriptionTables creates smeldr_subscriptions (D116) with its D110
// address storage (prefix N). Idempotent; call at startup with the other
// Create* functions.
func CreateSubscriptionTables(db DB) error {
	for _, stmt := range createSubscriptionTableSQL {
		if _, err := db.ExecContext(context.Background(), stmt); err != nil {
			return fmt.Errorf("smeldr: CreateSubscriptionTables: %w", err)
		}
	}
	return registerCoreAddresses(context.Background(), db, subscriptionAddressTypes...)
}

// subscriptionColumns is the column list every subscription read uses.
const subscriptionColumns = `id, COALESCE(address, ''), COALESCE(address_no, 0), name, owner, filter, delivery, include_own, paused, cursor, default_kind, last_actor, created_at, updated_at`

// scanSubscription reads one row of subscriptionColumns.
func scanSubscription(scan func(...any) error) (*Subscription, error) {
	var s Subscription
	var filter string
	var created, updated any
	if err := scan(&s.ID, &s.Address, &s.AddressNo, &s.Name, &s.Owner, &filter, &s.Delivery, &s.IncludeOwn, &s.Paused,
		&s.Cursor, &s.DefaultKind, &s.LastActor, &created, &updated); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(filter), &s.Filter); err != nil {
		return nil, fmt.Errorf("smeldr: subscription %s: filter: %w", s.ID, err)
	}
	s.CreatedAt, _ = ParseTimestamp(created)
	s.UpdatedAt, _ = ParseTimestamp(updated)
	return &s, nil
}

// subscriptionCaller is who calls a subscription API: the user in ctx, and
// whether it holds administer. Without governance a caller with no Context is
// trusted (the plain Go surface) and counts as administering.
func (a *App) subscriptionCaller(ctx context.Context) (User, bool, error) {
	if v, _ := ctx.Value(trustedOperatorKey{}).(bool); v {
		return User{}, true, nil
	}
	c, ok := ctx.(Context)
	if !ok {
		if a.governance != nil {
			return User{}, false, ErrForbidden
		}
		return User{}, true, nil
	}
	u := c.User()
	if a.governance == nil {
		return u, u.HasRole(Admin), nil
	}
	if u.ID == "" {
		return u, false, ErrForbidden
	}
	ok, err := a.governance.Authorized(ctx, u.ID, "administer", AuthTarget{})
	if err != nil {
		return u, false, fmt.Errorf("%w: subscription: %s", ErrInternal, err)
	}
	return u, ok, nil
}

// subscriptionDB is the App's database, or an error without one.
func (a *App) subscriptionDB() (DB, error) {
	if a.cfg.DB == nil {
		return nil, fmt.Errorf("%w: subscriptions need Config.DB", ErrInternal)
	}
	return a.cfg.DB, nil
}

// CreateSubscription saves a subscription for spec.Owner (the caller when
// empty). It validates the filter and resolves its items and anchor under the
// owner's read scope: an item the owner may not read is [ErrNotFound], the same
// as a missing one.
//
// [ValidationError] for a bad name, filter or delivery; [ErrForbidden] for
// another owner, or a webhook delivery, without administer; [ErrBadRequest]
// past [MaxSubscriptionsPerOwner] or [MaxSubscriptionItems]; [ErrInternal] on
// a store failure.
func (a *App) CreateSubscription(ctx context.Context, spec SubscriptionSpec) (*Subscription, error) {
	db, err := a.subscriptionDB()
	if err != nil {
		return nil, err
	}
	caller, admin, err := a.subscriptionCaller(ctx)
	if err != nil {
		return nil, err
	}
	owner := spec.Owner
	if owner == "" {
		owner = caller.ID
	}
	if owner == "" {
		return nil, Err("owner", "is required when the caller has no identity")
	}
	if owner != caller.ID && !admin {
		return nil, fmt.Errorf("%w: a subscription for another token needs administer", ErrForbidden)
	}
	if err := checkSubscriptionName(spec.Name); err != nil {
		return nil, err
	}
	delivery, err := a.checkDelivery(ctx, spec.Delivery, admin)
	if err != nil {
		return nil, err
	}
	filter, err := a.resolveFilter(ctx, owner, spec.Filter)
	if err != nil {
		return nil, err
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM smeldr_subscriptions WHERE owner = $1`, owner).Scan(&n); err != nil {
		return nil, fmt.Errorf("%w: subscription count: %s", ErrInternal, err)
	}
	if n >= MaxSubscriptionsPerOwner {
		return nil, fmt.Errorf("%w: an owner holds at most %d subscriptions", ErrBadRequest, MaxSubscriptionsPerOwner)
	}
	s := &Subscription{Name: spec.Name, Owner: owner, Filter: filter, Delivery: delivery,
		IncludeOwn: spec.IncludeOwn, Paused: spec.Paused, LastActor: caller.ID}
	if _, err := insertSubscription(ctx, db, s); err != nil {
		return nil, err
	}
	a.subscriptionsChanged(owner)
	return s, nil
}

// insertSubscription writes s with a new ID and address in one transaction.
// A row the default index refuses (a concurrent create of the same universal
// default) is no insert: ok is false and nothing is allocated.
func insertSubscription(ctx context.Context, db DB, s *Subscription) (ok bool, err error) {
	filter, err := json.Marshal(s.Filter)
	if err != nil {
		return false, fmt.Errorf("%w: subscription filter: %s", ErrInternal, err)
	}
	now := time.Now().UTC()
	id := NewID()
	errExists := errors.New("default exists")
	err = runInTx(ctx, db, func(exec DB) error {
		address, no, err := allocateAddress(ctx, exec, "Subscription")
		if err != nil {
			return err
		}
		res, err := exec.ExecContext(ctx,
			`INSERT INTO smeldr_subscriptions (id, slug, created_at, updated_at, name, owner, filter, delivery, include_own, paused, default_kind, last_actor, address, address_no)
			 VALUES ($1, $1, $2, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
			 ON CONFLICT DO NOTHING`,
			id, now, s.Name, s.Owner, string(filter), s.Delivery, s.IncludeOwn, s.Paused, s.DefaultKind, s.LastActor, address, no)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err == nil && n == 0 {
			return errExists
		}
		s.ID, s.Address, s.AddressNo, s.CreatedAt, s.UpdatedAt = id, address, no, now, now
		return nil
	})
	if errors.Is(err, errExists) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("%w: subscription insert: %s", ErrInternal, err)
	}
	return true, nil
}

// checkSubscriptionName requires a one-line name of at most TitleMaxLength
// characters.
func checkSubscriptionName(name string) error {
	switch {
	case strings.TrimSpace(name) == "":
		return Err("name", "is required: a short label for the subscription")
	case utf8.RuneCountInString(name) > TitleMaxLength:
		return Err("name", fmt.Sprintf("is longer than %d characters", TitleMaxLength))
	case strings.ContainsAny(name, "\r\n"):
		return Err("name", "is one line")
	}
	return nil
}

// checkDelivery normalizes a delivery: stream (the default), or a webhook
// endpoint that exists, which needs administer.
func (a *App) checkDelivery(ctx context.Context, delivery string, admin bool) (string, error) {
	switch {
	case delivery == "" || delivery == DeliveryStream:
		return DeliveryStream, nil
	case strings.HasPrefix(delivery, deliveryWebhookPrefix):
		if !admin {
			return "", fmt.Errorf("%w: a webhook delivery needs administer", ErrForbidden)
		}
		id := strings.TrimPrefix(delivery, deliveryWebhookPrefix)
		var n int
		if err := a.cfg.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM smeldr_webhook_endpoints WHERE id = $1`, id).Scan(&n); err != nil || n == 0 {
			return "", Err("delivery", fmt.Sprintf("names no webhook endpoint %q", id))
		}
		return delivery, nil
	}
	return "", Err("delivery", `is "stream" or "webhook:<endpoint id>"`)
}

// resolveFilter validates f and resolves its items and anchor to IDs under
// the owner's read scope.
func (a *App) resolveFilter(ctx context.Context, owner string, f SubscriptionFilter) (SubscriptionFilter, error) {
	for _, t := range f.Types {
		if a.typeRegistry.Lookup(t) == nil {
			return f, Err("filter", fmt.Sprintf("types: %q is not a registered type", t))
		}
	}
	for _, e := range f.Events {
		if !subscriptionEventKinds[e] {
			return f, Err("filter", fmt.Sprintf("events: %q is not an event kind", e))
		}
	}
	for _, n := range f.EventNames {
		if !strings.Contains(n, ".") {
			return f, Err("filter", fmt.Sprintf("event_names: %q is not an event name", n))
		}
	}
	for _, k := range f.SignalKinds {
		if k != SignalCondition && k != SignalRequest {
			return f, Err("filter", fmt.Sprintf("signal_kinds: %q is not condition or request", k))
		}
	}
	for name := range f.Fields {
		if !subscriptionFieldNames[name] && !a.isChannelField(name) {
			return f, Err("filter", fmt.Sprintf("fields: %q is not a routing field", name))
		}
	}
	if len(f.Items) > MaxSubscriptionItems {
		return f, fmt.Errorf("%w: a subscription names at most %d items", ErrBadRequest, MaxSubscriptionItems)
	}
	if len(f.Items) == 0 && f.RelatedTo == nil {
		return f, nil
	}
	sctx, err := a.ownerScopeContext(ctx, owner)
	if err != nil {
		return f, err
	}
	items := make([]string, 0, len(f.Items))
	for _, it := range f.Items {
		typ, id, err := a.resolveTypedItem(sctx, it)
		if err != nil {
			return f, err
		}
		items = append(items, typ+":"+id)
	}
	f.Items = items
	if f.RelatedTo != nil {
		anchor := *f.RelatedTo
		switch anchor.Direction {
		case "", "both", "incoming", "outgoing":
		default:
			return f, Err("filter", "related_to: direction is incoming, outgoing or both")
		}
		if strings.TrimSpace(anchor.Kind) == "" {
			return f, Err("filter", "related_to: kind is required")
		}
		typ, id, err := a.resolveTypedItem(sctx, anchor.Anchor)
		if err != nil {
			return f, err
		}
		anchor.Anchor, anchor.AnchorType = id, typ
		f.RelatedTo = &anchor
	}
	return f, nil
}

// isChannelField reports whether name is the channel field of a
// runtime-defined type.
func (a *App) isChannelField(name string) bool {
	if a.typeRegistry == nil {
		return false
	}
	for _, d := range a.typeRegistry.All() {
		if routeField(d.Schema) == name {
			return true
		}
	}
	return false
}

// resolveTypedItem resolves "Type:ident" to the type and the item's ID under
// the read scope in ctx. A malformed pair is a ValidationError; a missing or
// hidden item is ErrNotFound.
func (a *App) resolveTypedItem(ctx context.Context, pair string) (string, string, error) {
	typ, ident, ok := strings.Cut(pair, ":")
	if !ok || typ == "" || ident == "" {
		return "", "", Err("filter", fmt.Sprintf("%q is not Type:ident", pair))
	}
	if a.typeRegistry.Lookup(typ) == nil {
		return "", "", Err("filter", fmt.Sprintf("%q is not a registered type", typ))
	}
	id, err := a.ResolveReadableID(ctx, typ, ident)
	if err != nil {
		return "", "", err
	}
	return typ, id, nil
}

// ownerScopeContext is ctx with the owner's read scope attached: items are
// resolved as the owner would see them, whoever saves the subscription.
func (a *App) ownerScopeContext(ctx context.Context, owner string) (context.Context, error) {
	if a.governance == nil {
		return ctx, nil
	}
	scope, err := LoadReadScope(ctx, a.governance, User{ID: owner})
	if err != nil {
		return nil, err
	}
	return WithReadScope(ctx, scope), nil
}

// loadSubscription reads one subscription by ID, address or slug, and applies
// the owner rule: one the caller does not own, without administer, is
// ErrNotFound.
func (a *App) loadSubscription(ctx context.Context, ident string) (*Subscription, User, bool, error) {
	db, err := a.subscriptionDB()
	if err != nil {
		return nil, User{}, false, err
	}
	caller, admin, err := a.subscriptionCaller(ctx)
	if err != nil {
		return nil, User{}, false, err
	}
	row := db.QueryRowContext(ctx, `SELECT `+subscriptionColumns+` FROM smeldr_subscriptions WHERE id = $1 OR slug = $1 OR address = $2`,
		ident, strings.ToUpper(ident))
	s, err := scanSubscription(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, caller, admin, ErrNotFound
	}
	if err != nil {
		return nil, caller, admin, fmt.Errorf("%w: subscription: %s", ErrInternal, err)
	}
	if s.Owner != caller.ID && !admin {
		return nil, caller, admin, ErrNotFound
	}
	return s, caller, admin, nil
}

// GetSubscription returns the subscription ident names, if the caller owns it
// or holds administer; [ErrNotFound] otherwise, the same as a missing one.
func (a *App) GetSubscription(ctx context.Context, ident string) (*Subscription, error) {
	s, _, _, err := a.loadSubscription(ctx, ident)
	return s, err
}

// ListSubscriptions returns owner's subscriptions (the caller's when owner is
// empty), creating its universal defaults first. Another owner's need
// administer: without it the list is [ErrForbidden].
func (a *App) ListSubscriptions(ctx context.Context, owner string) ([]Subscription, error) {
	db, err := a.subscriptionDB()
	if err != nil {
		return nil, err
	}
	caller, admin, err := a.subscriptionCaller(ctx)
	if err != nil {
		return nil, err
	}
	if owner == "" {
		owner = caller.ID
	}
	if owner != caller.ID && !admin {
		return nil, ErrForbidden
	}
	if err := a.ensureDefaultSubscriptions(ctx, owner); err != nil {
		return nil, err
	}
	return listSubscriptions(ctx, db, owner)
}

// listSubscriptions reads owner's subscriptions, oldest first.
func listSubscriptions(ctx context.Context, db DB, owner string) ([]Subscription, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+subscriptionColumns+` FROM smeldr_subscriptions WHERE owner = $1 ORDER BY created_at, id`, owner)
	if err != nil {
		return nil, fmt.Errorf("%w: subscriptions: %s", ErrInternal, err)
	}
	defer rows.Close()
	out := []Subscription{}
	for rows.Next() {
		s, err := scanSubscription(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("%w: subscriptions: %s", ErrInternal, err)
		}
		out = append(out, *s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%w: subscriptions: %s", ErrInternal, err)
	}
	return out, nil
}

// UpdateSubscription changes the fields u sets on the subscription ident
// names, under the same rules as [App.CreateSubscription]. A universal
// default may be paused and renamed, but its filter is fixed: changing it is
// [ErrConflict].
func (a *App) UpdateSubscription(ctx context.Context, ident string, u SubscriptionUpdate) (*Subscription, error) {
	s, caller, _, err := a.loadSubscription(ctx, ident)
	if err != nil {
		return nil, err
	}
	if u.Name != nil {
		if err := checkSubscriptionName(*u.Name); err != nil {
			return nil, err
		}
		s.Name = *u.Name
	}
	if u.Filter != nil {
		if s.DefaultKind != "" {
			return nil, fmt.Errorf("%w: a default subscription's filter is fixed; pause it instead", ErrConflict)
		}
		f, err := a.resolveFilter(ctx, s.Owner, *u.Filter)
		if err != nil {
			return nil, err
		}
		s.Filter = f
	}
	if u.IncludeOwn != nil {
		s.IncludeOwn = *u.IncludeOwn
	}
	if u.Paused != nil {
		s.Paused = *u.Paused
	}
	filter, _ := json.Marshal(s.Filter)
	s.UpdatedAt, s.LastActor = time.Now().UTC(), caller.ID
	if _, err := a.cfg.DB.ExecContext(ctx,
		`UPDATE smeldr_subscriptions SET name = $1, filter = $2, include_own = $3, paused = $4, updated_at = $5, last_actor = $6 WHERE id = $7`,
		s.Name, string(filter), s.IncludeOwn, s.Paused, s.UpdatedAt, s.LastActor, s.ID); err != nil {
		return nil, fmt.Errorf("%w: subscription update: %s", ErrInternal, err)
	}
	a.subscriptionsChanged(s.Owner)
	return s, nil
}

// DeleteSubscription removes the subscription ident names. A universal default
// cannot be deleted ([ErrConflict]): pause it.
func (a *App) DeleteSubscription(ctx context.Context, ident string) error {
	s, _, _, err := a.loadSubscription(ctx, ident)
	if err != nil {
		return err
	}
	if s.DefaultKind != "" {
		return fmt.Errorf("%w: a default subscription cannot be deleted; pause it instead", ErrConflict)
	}
	if _, err := a.cfg.DB.ExecContext(ctx, `DELETE FROM smeldr_subscriptions WHERE id = $1`, s.ID); err != nil {
		return fmt.Errorf("%w: subscription delete: %s", ErrInternal, err)
	}
	a.subscriptionsChanged(s.Owner)
	return nil
}

// saveSubscriptionCursor records the last envelope ID delivered on a
// subscription. Fail-open: a failure is logged by the caller.
func saveSubscriptionCursor(ctx context.Context, db DB, id, cursor string) error {
	_, err := db.ExecContext(ctx, `UPDATE smeldr_subscriptions SET cursor = $1 WHERE id = $2`, cursor, id)
	return err
}

// universalDefaults are the three default subscriptions every token has.
var universalDefaults = []struct {
	kind, name string
	filter     SubscriptionFilter
}{
	{SubscriptionRequestsToMe, "Requests addressed to me",
		SubscriptionFilter{Types: []string{"Signal"}, Events: []string{"created"}, SignalKinds: []SignalKind{SignalRequest}, AddressedToMe: true}},
	{SubscriptionConditionsICanResolve, "Conditions I can resolve",
		SubscriptionFilter{Types: []string{"Signal"}, Events: []string{"created"}, SignalKinds: []SignalKind{SignalCondition}, AddressedToMe: true}},
	{SubscriptionOutcomesOfMyRequests, "Outcomes of my requests",
		SubscriptionFilter{Types: []string{"Signal"}, Events: []string{"transitioned"}, ToStates: []string{signalAnswered, signalWithdrawn}, SentByMe: true}},
}

// ensureDefaultSubscriptions creates owner's universal defaults that are
// missing. Concurrent first uses cannot duplicate them: the default index
// refuses a second row, and that insert allocates nothing.
func (a *App) ensureDefaultSubscriptions(ctx context.Context, owner string) error {
	if owner == "" {
		return nil
	}
	rows, err := a.cfg.DB.QueryContext(ctx, `SELECT default_kind FROM smeldr_subscriptions WHERE owner = $1 AND default_kind <> ''`, owner)
	if err != nil {
		return fmt.Errorf("%w: default subscriptions: %s", ErrInternal, err)
	}
	have := map[string]bool{}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err == nil {
			have[k] = true
		}
	}
	rows.Close()
	for _, d := range universalDefaults {
		if have[d.kind] {
			continue
		}
		s := &Subscription{Name: d.name, Owner: owner, Filter: d.filter, Delivery: DeliveryStream, DefaultKind: d.kind}
		if _, err := insertSubscription(ctx, a.cfg.DB, s); err != nil {
			return err
		}
	}
	return nil
}

// RoleDefaults names the role subscriptions [App.SeedRoleSubscriptions]
// creates (design 3.4).
type RoleDefaults struct {
	// Band is the role's band (core, site, cloud, devops, brand): its Tasks,
	// its task_plans' reviews and the Signals addressed to it.
	Band string
	// Architect adds the architect's reviews: plans submitted, Tasks in plan
	// or commit review, Tasks done, blocked or resolved, Decisions proposed or
	// ratified, and Tasks created with priority 0, in every band.
	Architect bool
}

// roleSubscriptions returns the role subscriptions for d.
func roleSubscriptions(d RoleDefaults) []SubscriptionSpec {
	var out []SubscriptionSpec
	if d.Band != "" {
		band := []string{d.Band}
		out = append(out,
			SubscriptionSpec{Name: "Tasks in band " + d.Band, Filter: SubscriptionFilter{Types: []string{"Task"}, Events: []string{"created", "updated", "transitioned"}, Fields: map[string][]string{"band": band}}},
			SubscriptionSpec{Name: "Plan reviews in band " + d.Band, Filter: SubscriptionFilter{Types: []string{"task_plan"}, Events: []string{"transitioned"}, ToStates: []string{"approved", "changes_requested"}, Fields: map[string][]string{"band": band}}},
			SubscriptionSpec{Name: "Signals to " + d.Band, Filter: SubscriptionFilter{Types: []string{"Signal"}, Events: []string{"created"}, Fields: map[string][]string{"receiver": band}}},
		)
	}
	if d.Architect {
		out = append(out,
			SubscriptionSpec{Name: "Plans submitted", Filter: SubscriptionFilter{Types: []string{"task_plan"}, Events: []string{"transitioned"}, ToStates: []string{"submitted"}}},
			SubscriptionSpec{Name: "Tasks in review", Filter: SubscriptionFilter{Types: []string{"Task"}, Events: []string{"transitioned"}, ToStates: []string{"plan-reviewing", "commit-reviewing"}}},
			SubscriptionSpec{Name: "Tasks done, blocked or resolved", Filter: SubscriptionFilter{Types: []string{"Task"}, Events: []string{"transitioned"}, ToStates: []string{"done", "blocked", "resolved"}}},
			SubscriptionSpec{Name: "Decisions proposed or ratified", Filter: SubscriptionFilter{Types: []string{"Decision"}, Events: []string{"created", "transitioned"}, ToStates: []string{"proposed", "ratified"}}},
			SubscriptionSpec{Name: "Tasks created at priority 0", Filter: SubscriptionFilter{Types: []string{"Task"}, Events: []string{"created"}, Fields: map[string][]string{"priority": {"0"}}}},
			SubscriptionSpec{Name: "Signals to architect", Filter: SubscriptionFilter{Types: []string{"Signal"}, Events: []string{"created"}, Fields: map[string][]string{"receiver": {"architect"}}}},
		)
	}
	return out
}

// SeedRoleSubscriptions gives owner its role subscriptions (design 3.4) and
// its universal defaults. It is idempotent by name: a role subscription whose
// name the owner already has is left as it is, even when its filter is older
// than the one seeded now; an operator changes an existing one with
// [App.UpdateSubscription] (MCP update_subscription). It returns the subscriptions it
// created; [App.SeedRoleSubscriptionsReport] also names the ones it left out.
// The caller needs administer ([ErrForbidden]); a [RoleDefaults] with no band
// and not Architect is [ErrBadRequest].
func (a *App) SeedRoleSubscriptions(ctx context.Context, owner string, d RoleDefaults) ([]Subscription, error) {
	rep, err := a.SeedRoleSubscriptionsReport(ctx, owner, d)
	if rep == nil {
		return nil, err
	}
	return rep.Created, err
}

// RoleSeedReport is what seeding a role's subscriptions did.
type RoleSeedReport struct {
	// Created are the subscriptions this run created.
	Created []Subscription `json:"created"`
	// LeftOut are the role subscriptions this run did not create because
	// their type is not defined on this instance.
	LeftOut []RoleSubscriptionSkip `json:"left_out"`
}

// RoleSubscriptionSkip is a role subscription that was not created because
// its type is not defined on this instance.
type RoleSubscriptionSkip struct {
	// Name is the role subscription's name.
	Name string `json:"name"`
	// Type is the type its filter names that is not defined here.
	Type string `json:"type"`
}

// SeedRoleSubscriptionsReport is [App.SeedRoleSubscriptions] returning a
// [RoleSeedReport]: the subscriptions it created and the role subscriptions
// it left out because their type (task_plan is runtime-defined) is not
// defined here. A role subscription the owner already has is in neither list.
// On a create failure the report holds what was created before it.
func (a *App) SeedRoleSubscriptionsReport(ctx context.Context, owner string, d RoleDefaults) (*RoleSeedReport, error) {
	if d.Band == "" && !d.Architect {
		return nil, fmt.Errorf("%w: role defaults need a band or Architect", ErrBadRequest)
	}
	if strings.TrimSpace(owner) == "" {
		return nil, Err("owner", "is required")
	}
	_, admin, err := a.subscriptionCaller(ctx)
	if err != nil {
		return nil, err
	}
	if !admin {
		return nil, fmt.Errorf("%w: seeding role subscriptions needs administer", ErrForbidden)
	}
	if _, err := a.subscriptionDB(); err != nil {
		return nil, err
	}
	if err := a.ensureDefaultSubscriptions(ctx, owner); err != nil {
		return nil, err
	}
	existing, err := listSubscriptions(ctx, a.cfg.DB, owner)
	if err != nil {
		return nil, err
	}
	have := map[string]bool{}
	for _, s := range existing {
		have[s.Name] = true
	}
	rep := &RoleSeedReport{Created: []Subscription{}, LeftOut: []RoleSubscriptionSkip{}}
	for _, spec := range roleSubscriptions(d) {
		if have[spec.Name] {
			continue
		}
		if missing := a.unregisteredType(spec.Filter.Types); missing != "" {
			slog.WarnContext(ctx, "smeldr: role subscription left out: its type is not defined here", "subscription", spec.Name, "type", missing)
			rep.LeftOut = append(rep.LeftOut, RoleSubscriptionSkip{Name: spec.Name, Type: missing})
			continue
		}
		spec.Owner = owner
		s, err := a.CreateSubscription(ctx, spec)
		if err != nil {
			return rep, err
		}
		rep.Created = append(rep.Created, *s)
	}
	return rep, nil
}

// unregisteredType returns the first of types the registry does not know.
func (a *App) unregisteredType(types []string) string {
	for _, t := range types {
		if a.typeRegistry.Lookup(t) == nil {
			return t
		}
	}
	return ""
}

// trustedOperatorKey marks the operator's own tooling (a server subcommand),
// which acts with administer on any owner.
type trustedOperatorKey struct{}

// SeedRoleSubscriptionsTrusted is [App.SeedRoleSubscriptions] for the
// operator's own tooling on the server (smeldr-server
// seed-role-subscriptions): it acts with administer without a token. Never
// call it from a request surface.
func (a *App) SeedRoleSubscriptionsTrusted(ctx context.Context, owner string, d RoleDefaults) ([]Subscription, error) {
	return a.SeedRoleSubscriptions(context.WithValue(ctx, trustedOperatorKey{}, true), owner, d)
}

// SeedRoleSubscriptionsReportTrusted is [App.SeedRoleSubscriptionsReport] for
// the operator's own tooling on the server, acting with administer without a
// token, like [App.SeedRoleSubscriptionsTrusted]. Never call it from a
// request surface.
func (a *App) SeedRoleSubscriptionsReportTrusted(ctx context.Context, owner string, d RoleDefaults) (*RoleSeedReport, error) {
	return a.SeedRoleSubscriptionsReport(context.WithValue(ctx, trustedOperatorKey{}, true), owner, d)
}
