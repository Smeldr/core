package smeldr

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// webhookSubscription is one endpoint's subscription, compiled for delivery.
type webhookSubscription struct {
	owner  string
	paused bool
	sub    compiledSubscription
	names  set
}

// webhookSubCache holds the compiled subscription of each webhook endpoint,
// loaded on first delivery and dropped whenever a subscription changes.
type webhookSubCache struct {
	mu     sync.Mutex
	loaded bool
	byEP   map[string]*webhookSubscription
}

// reset drops the cache.
func (c *webhookSubCache) reset() {
	c.mu.Lock()
	c.loaded, c.byEP = false, nil
	c.mu.Unlock()
}

// webhookSubscriptions returns the compiled subscription per endpoint ID.
func (a *App) webhookSubscriptions(ctx context.Context) (map[string]*webhookSubscription, error) {
	c := &a.webhookSubs
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.loaded {
		return c.byEP, nil
	}
	rows, err := a.cfg.DB.QueryContext(ctx, `SELECT `+subscriptionColumns+` FROM smeldr_subscriptions WHERE delivery LIKE 'webhook:%'`)
	if err != nil {
		return nil, fmt.Errorf("%w: webhook subscriptions: %s", ErrInternal, err)
	}
	var subs []Subscription
	for rows.Next() {
		s, err := scanSubscription(rows.Scan)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("%w: webhook subscriptions: %s", ErrInternal, err)
		}
		subs = append(subs, *s)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, fmt.Errorf("%w: webhook subscriptions: %s", ErrInternal, err)
	}
	byEP := map[string]*webhookSubscription{}
	for _, s := range subs {
		names, err := a.routingNames(ctx, s.Owner, []Subscription{s})
		if err != nil {
			return nil, err
		}
		related, err := a.relatedSet(ctx, s.Filter.RelatedTo)
		if err != nil {
			return nil, err
		}
		byEP[strings.TrimPrefix(s.Delivery, deliveryWebhookPrefix)] = &webhookSubscription{
			owner: s.Owner, paused: s.Paused, sub: compileSubscription(s, related), names: names}
	}
	c.byEP, c.loaded = byEP, true
	return byEP, nil
}

// webhookDeliverable reports whether an event goes to the endpoint: its
// subscription is active, its filter matches, and its owner may read what the
// event reveals. An endpoint without a subscription (not yet migrated) is
// delivered every event it names, as before. A failure skips the delivery
// (fail closed) and is logged.
func (a *App) webhookDeliverable(ctx context.Context, endpointID string, meta eventMeta) bool {
	subs, err := a.webhookSubscriptions(ctx)
	if err != nil {
		slog.WarnContext(ctx, "smeldr: webhook subscriptions not read; delivery skipped", "endpoint", endpointID, "error", err)
		return false
	}
	ws, ok := subs[endpointID]
	if !ok {
		return true
	}
	if ws.paused {
		return false
	}
	meta.subjectType = a.canonicalTypeName(meta.subjectType)
	if meta.otherType != "" {
		meta.otherType = a.canonicalTypeName(meta.otherType)
	}
	if !ws.sub.matches(meta, ws.names) {
		return false
	}
	scope, err := a.cachedScope(ctx, ws.owner)
	if err != nil {
		slog.WarnContext(ctx, "smeldr: webhook owner's read scope not loaded; delivery skipped", "endpoint", endpointID, "error", err)
		return false
	}
	return eventReadable(scope, meta)
}

// webhookSubscriptionFor is the subscription a new or migrated endpoint gets:
// owned by legacy-admin, delivering the event names it was created for.
func webhookSubscriptionFor(ep WebhookEndpoint) *Subscription {
	name := "Webhook " + ep.TargetURL
	if r := []rune(name); len(r) > TitleMaxLength {
		name = string(r[:TitleMaxLength])
	}
	return &Subscription{Name: name, Owner: legacyAdminOwner, Delivery: deliveryWebhookPrefix + ep.ID,
		Filter: SubscriptionFilter{EventNames: ep.Events}, LastActor: legacyAdminOwner}
}

// webhookCreated gives a new endpoint its subscription. Fail-open: the
// endpoint is created either way, and an endpoint without a subscription is
// delivered as before.
func (a *App) webhookCreated(ctx context.Context, ep WebhookEndpoint) {
	if _, err := insertSubscription(ctx, a.cfg.DB, webhookSubscriptionFor(ep)); err != nil {
		slog.WarnContext(ctx, "smeldr: webhook subscription not created", "endpoint", ep.ID, "error", err)
	}
	a.subscriptionsChanged(legacyAdminOwner)
}

// webhookDeleted removes a deleted endpoint's subscription.
func (a *App) webhookDeleted(ctx context.Context, endpointID string) {
	if _, err := a.cfg.DB.ExecContext(ctx, `DELETE FROM smeldr_subscriptions WHERE delivery = $1`, deliveryWebhookPrefix+endpointID); err != nil {
		slog.WarnContext(ctx, "smeldr: webhook subscription not removed", "endpoint", endpointID, "error", err)
	}
	a.subscriptionsChanged(legacyAdminOwner)
}

// wireWebhookSubscriptions connects the webhook store to subscriptions when
// both are on (App.Handler).
func (a *App) wireWebhookSubscriptions() {
	if !a.subscriptionsOn || a.webhookStore == nil || a.cfg.DB == nil {
		return
	}
	if err := CreateSubscriptionTables(a.cfg.DB); err != nil {
		slog.Error("smeldr: webhook subscriptions not started: endpoints deliver by event name only", "error", err)
		return
	}
	a.webhookStore.deliverable = a.webhookDeliverable
	a.webhookStore.onCreate = a.webhookCreated
	a.webhookStore.onDelete = a.webhookDeleted
}

// WebhookMigrationOptions configures [MigrateWebhookSubscriptions].
type WebhookMigrationOptions struct {
	// Apply commits; without it the migration is a dry run that rolls back.
	Apply bool
}

// WebhookMigrationReport is what [MigrateWebhookSubscriptions] did, or would
// do.
type WebhookMigrationReport struct {
	Applied    bool  `json:"applied"`
	DurationMS int64 `json:"duration_ms"`
	// Created lists the subscription given to each endpoint that had none.
	Created []WebhookMigrationChange `json:"created"`
}

// WebhookMigrationChange is one endpoint and the subscription it got.
type WebhookMigrationChange struct {
	EndpointID   string   `json:"endpoint_id"`
	TargetURL    string   `json:"target_url"`
	Events       []string `json:"events"`
	Subscription string   `json:"subscription"`
}

// MigrateWebhookSubscriptions gives every webhook endpoint that has no
// subscription its own (D116, design section 4): owned by legacy-admin, which
// reads everything (read rules answer 5), delivering the event names the
// endpoint was created for. One transaction, idempotent, a dry run unless
// opts.Apply. It needs smeldr_webhook_endpoints and smeldr_subscriptions;
// without the endpoints table there is nothing to migrate.
func MigrateWebhookSubscriptions(ctx context.Context, db DB, opts WebhookMigrationOptions) (*WebhookMigrationReport, error) {
	if db == nil {
		return nil, fmt.Errorf("%w: MigrateWebhookSubscriptions needs a database", ErrBadRequest)
	}
	rep := &WebhookMigrationReport{Applied: opts.Apply, Created: []WebhookMigrationChange{}}
	if ok, err := tableExists(ctx, db, "smeldr_webhook_endpoints"); err != nil {
		return nil, fmt.Errorf("%w: MigrateWebhookSubscriptions: %s", ErrInternal, err)
	} else if !ok {
		return rep, nil
	}
	start := time.Now()
	exec, commit, rollback, err := conflictTx(ctx, db)
	if err != nil {
		return nil, err
	}
	defer rollback()
	rows, err := exec.QueryContext(ctx,
		`SELECT e.id, e.target_url, e.events FROM smeldr_webhook_endpoints e
		 WHERE NOT EXISTS (SELECT 1 FROM smeldr_subscriptions s WHERE s.delivery = 'webhook:' || e.id) ORDER BY e.created_at, e.id`)
	if err != nil {
		return nil, fmt.Errorf("%w: MigrateWebhookSubscriptions: %s", ErrInternal, err)
	}
	var eps []WebhookEndpoint
	for rows.Next() {
		var ep WebhookEndpoint
		var events string
		if err := rows.Scan(&ep.ID, &ep.TargetURL, &events); err != nil {
			rows.Close()
			return nil, fmt.Errorf("%w: MigrateWebhookSubscriptions: %s", ErrInternal, err)
		}
		_ = json.Unmarshal([]byte(events), &ep.Events)
		eps = append(eps, ep)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, fmt.Errorf("%w: MigrateWebhookSubscriptions: %s", ErrInternal, err)
	}
	for _, ep := range eps {
		s := webhookSubscriptionFor(ep)
		if _, err := insertSubscription(ctx, exec, s); err != nil {
			return nil, err
		}
		rep.Created = append(rep.Created, WebhookMigrationChange{EndpointID: ep.ID, TargetURL: ep.TargetURL, Events: ep.Events, Subscription: s.Address})
	}
	if !opts.Apply {
		rollback()
	} else if err := commit(); err != nil {
		return nil, fmt.Errorf("%w: MigrateWebhookSubscriptions commit: %s", ErrInternal, err)
	}
	rep.DurationMS = time.Since(start).Milliseconds()
	return rep, nil
}
