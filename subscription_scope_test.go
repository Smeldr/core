package smeldr

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The scope cache: loaded once, dropped on a grant change (the RoleStore
// hook), on a relation change at an anchor, on a revocation, and after the TTL.
func TestScopeCache(t *testing.T) {
	app, db, rs := setupTransitionItemApp(t)
	app.EventStream()
	app.wireScopeHooks()
	ctx := context.Background()
	reader := "u-reader"
	if ok, err := app.CanRead(ctx, User{ID: reader}, "Task", "t1"); err != nil || ok {
		t.Fatalf("before a grant: %v, %v", ok, err)
	}
	if _, err := rs.Grant(ctx, RoleGrant{TokenID: reader, RoleName: "author"}); err != nil {
		t.Fatal(err)
	}
	if ok, _ := app.CanRead(ctx, User{ID: reader}, "Task", "t1"); !ok {
		t.Error("the grant did not take effect at once")
	}
	if ok, _ := app.CanRead(ctx, User{ID: reader}, "Task", "t1"); !ok {
		t.Error("the cached scope did not read")
	}
	if _, err := db.Exec(`UPDATE smeldr_role_grants SET scope_anchor_id = 'anchor-x' WHERE token_id = ?`, reader); err != nil {
		t.Fatal(err)
	}
	if anchors, err := grantAnchors(ctx, db, reader); err != nil || !anchors["anchor-x"] {
		t.Errorf("anchors = %v, %v", anchors, err)
	}
	app.scopes.mu.Lock()
	app.scopes.entries[reader].anchors = map[string]bool{"anchor-1": true}
	app.scopes.mu.Unlock()
	app.relationChanged("other", "anchor-1")
	app.scopes.mu.Lock()
	_, kept := app.scopes.entries[reader]
	app.scopes.mu.Unlock()
	if kept {
		t.Error("a relation change at an anchor kept the scope")
	}
	if _, err := app.cachedScope(ctx, reader); err != nil {
		t.Fatal(err)
	}
	app.tokenRevoked("fp")
	if app.scopes.entries != nil {
		t.Error("a revocation kept the cache")
	}
	if _, err := app.cachedScope(ctx, reader); err != nil {
		t.Fatal(err)
	}
	app.scopes.mu.Lock()
	first := app.scopes.entries[reader]
	first.loaded = time.Now().Add(-2 * scopeCacheTTL) // older than the TTL
	app.scopes.mu.Unlock()
	_, _ = app.cachedScope(ctx, reader)
	app.scopes.mu.Lock()
	second := app.scopes.entries[reader]
	app.scopes.mu.Unlock()
	if first == second {
		t.Error("a stale entry was reused")
	}
	if ok, _ := app.CanRead(ctx, User{}, "Task", "t1"); ok {
		t.Error("no identity read")
	}
	if s, err := app.cachedScope(ctx, legacyAdminOwner); s != nil || err != nil {
		t.Errorf("legacy-admin = %v, %v; want everything", s, err)
	}
	if _, err := grantAnchors(ctx, failAddressDB{db, "scope_anchor_id FROM smeldr_role_grants"}, reader); !errors.Is(err, ErrInternal) {
		t.Errorf("anchor query failure: %v", err)
	}
	plain := &App{}
	if ok, _ := plain.CanRead(ctx, User{Roles: []Role{Author}}, "Task", "t"); !ok {
		t.Error("without governance an Author reads")
	}
	if ok, _ := plain.CanRead(ctx, User{}, "Task", "t"); ok {
		t.Error("without governance a guest reads")
	}
	plain.scopesChanged("x")
	plain.relationChanged("a", "b")
	plain.tokenRevoked("x")
}

// A stream connection's scope reloads when its token is granted read, and the
// next event is delivered.
func TestScopeReload_GrantReachesOpenConnection(t *testing.T) {
	app, db, rs := setupTransitionItemApp(t)
	_ = db
	app.EventStream()
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)
	reader := NewID()
	tok, err := SignToken(User{ID: reader, Roles: []Role{Author}}, transitionItemTestSecret, 0)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/_events/stream?channel=all", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	ch := lines(resp)
	waitConnected(t, app, 1)
	publish := func(id string) {
		dispatchEventFrom(context.Background(), nil, nil, app.eventBroadcaster, "", "", "task.transitioned",
			transitionWebhookData{Type: "task", ID: id, typeName: "Task"})
	}
	publish("hidden")
	if _, err := rs.Grant(context.Background(), RoleGrant{TokenID: reader, RoleName: "author"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond) // the connection reloads its scope
	publish("seen")
	if e := next(t, ch); e.Data.ID != "seen" {
		t.Errorf("delivered %+v, want only the event after the grant", e)
	}
}

// A revoked token's connection closes: at once through the App, and on the
// backstop when the revocation came another way.
func TestScopeReload_RevokedTokenCloses(t *testing.T) {
	db := newTestTokensDB(t)
	ts := NewTokenStore(db, transitionItemTestSecret)
	raw, err := ts.Create(context.Background(), "agent", "editor", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	b := newEventBroadcaster()
	srv := httptest.NewServer(newEventStreamHandlerWith(BearerHMAC(transitionItemTestSecret), b, nil, nil, ts.IsRevoked))
	t.Cleanup(srv.Close)
	open := func() chan envelope {
		req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
		req.Header.Set("Authorization", "Bearer "+raw)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { resp.Body.Close() })
		return lines(resp)
	}
	fp := bearerFingerprint(&http.Request{Header: http.Header{"Authorization": {"Bearer " + raw}}})
	ch := open()
	waitBroadcaster(t, b, 1)
	b.closeFingerprint(fp)
	expectClosed(t, ch)

	prev := scopeCacheTTL
	scopeCacheTTL = 20 * time.Millisecond
	defer func() { scopeCacheTTL = prev }()
	ch2 := open()
	waitBroadcaster(t, b, 1)
	if _, err := db.Exec(`UPDATE smeldr_tokens SET revoked_at = ? WHERE id = ?`, time.Now().UTC().Format(time.RFC3339), fp); err != nil {
		t.Fatal(err)
	}
	expectClosed(t, ch2)

	// The store tells the App on its own revocation.
	var told string
	ts.onRevoke = func(f string) { told = f }
	raw2, _ := ts.Create(context.Background(), "two", "editor", time.Hour)
	fp2 := bearerFingerprint(&http.Request{Header: http.Header{"Authorization": {"Bearer " + raw2}}})
	if err := ts.Revoke(context.Background(), fp2); err != nil {
		t.Fatal(err)
	}
	if told != fp2 {
		t.Errorf("onRevoke got %q, want %q", told, fp2)
	}
	if gone, err := ts.IsRevoked(context.Background(), "unknown"); gone || err != nil {
		t.Errorf("unknown fingerprint = %v, %v", gone, err)
	}
	bad := NewTokenStore(failAddressDB{db, "SELECT revoked_at"}, transitionItemTestSecret)
	if _, err := bad.IsRevoked(context.Background(), fp); !errors.Is(err, ErrInternal) {
		t.Errorf("query failure: %v", err)
	}
	if bearerFingerprint(&http.Request{Header: http.Header{}}) != "" {
		t.Error("a fingerprint without a bearer")
	}
	b.closeFingerprint("")
}

// waitBroadcaster waits for n connections on b.
func waitBroadcaster(t *testing.T, b *eventBroadcaster, n int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for b.count() < n {
		if time.Now().After(deadline) {
			t.Fatal("no connection")
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// expectClosed waits for the stream to close.
func expectClosed(t *testing.T, ch chan envelope) {
	t.Helper()
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
		case <-time.After(3 * time.Second):
			t.Fatal("the connection stayed open")
		}
	}
}

// webhookApp is an App with subscriptions and webhooks on SQLite.
func webhookApp(t *testing.T) (*App, *sql.DB, *WebhookStore) {
	t.Helper()
	app, db := subscriptionApp(t)
	createWebhookEndpointsTable(t, db)
	store := NewWebhookStore(db, []byte("app-secret"))
	app.Webhooks(store).Subscriptions()
	_ = app.Handler()
	return app, db, store
}

// Webhook endpoints deliver through their subscriptions: a new endpoint gets
// one owned by legacy-admin; its filter and pause apply; an owner's scope
// bounds it; a deleted endpoint loses it.
func TestWebhookSubscriptions(t *testing.T) {
	app, db, store := webhookApp(t)
	ctx := context.Background()
	ep, _, err := store.Create(ctx, "https://8.8.8.8/x", []string{"task.transitioned"})
	if err != nil {
		t.Fatal(err)
	}
	var owner, delivery string
	if err := db.QueryRow(`SELECT owner, delivery FROM smeldr_subscriptions WHERE delivery = ?`, "webhook:"+ep.ID).Scan(&owner, &delivery); err != nil || owner != legacyAdminOwner {
		t.Fatalf("subscription = %q %q, %v", owner, delivery, err)
	}
	task := eventMeta{event: "task.transitioned", subjectType: "task", subjectID: "t1"}
	if !app.webhookDeliverable(ctx, ep.ID, task) {
		t.Error("a matching event was not delivered")
	}
	if app.webhookDeliverable(ctx, ep.ID, eventMeta{event: "task.created"}) {
		t.Error("an unnamed event was delivered")
	}
	if !app.webhookDeliverable(ctx, "no-subscription", task) {
		t.Error("an endpoint without a subscription stopped delivering")
	}
	if _, err := db.Exec(`UPDATE smeldr_subscriptions SET paused = TRUE WHERE delivery = ?`, "webhook:"+ep.ID); err != nil {
		t.Fatal(err)
	}
	app.subscriptionsChanged(legacyAdminOwner)
	if app.webhookDeliverable(ctx, ep.ID, task) {
		t.Error("a paused subscription delivered")
	}
	if err := store.Delete(ctx, ep.ID); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM smeldr_subscriptions WHERE delivery = ?`, "webhook:"+ep.ID).Scan(&n)
	if n != 0 {
		t.Errorf("subscriptions after delete = %d", n)
	}
	app.cfg.DB = failAddressDB{db, "WHERE delivery LIKE 'webhook:%'"}
	app.subscriptionsChanged("")
	if app.webhookDeliverable(ctx, ep.ID, task) {
		t.Error("a read failure delivered")
	}
	if _, err := app.cfg.DB.ExecContext(ctx, `SELECT 1`); err != nil {
		t.Fatal(err)
	}
	app.cfg.DB = db
	app.webhookCreated(ctx, WebhookEndpoint{ID: "dup", TargetURL: "https://x.example/" + strings.Repeat("p", 200)})
	app.cfg.DB = failAddressDB{db, "DELETE FROM smeldr_subscriptions WHERE delivery"}
	app.webhookDeleted(ctx, "dup")
	app.cfg.DB = failAddressDB{db, "INSERT INTO smeldr_subscriptions"}
	app.webhookCreated(ctx, WebhookEndpoint{ID: "dup2"})
}

// Under governance a webhook subscription owned by a scoped token is not
// delivered an event about an item that token cannot read.
func TestWebhookSubscriptions_OwnerScope(t *testing.T) {
	app, db, _ := setupTransitionItemApp(t)
	if err := CreateSubscriptionTables(db); err != nil {
		t.Fatal(err)
	}
	app.subscriptionsOn = true
	ctx := context.Background()
	s := &Subscription{Name: "scoped hook", Owner: "u-none", Delivery: "webhook:e1", Filter: SubscriptionFilter{EventNames: []string{"task.transitioned"}}}
	if _, err := insertSubscription(ctx, db, s); err != nil {
		t.Fatal(err)
	}
	if app.webhookDeliverable(ctx, "e1", eventMeta{event: "task.transitioned", subjectType: "Task", subjectID: "t1"}) {
		t.Error("a hidden subject was delivered to a scoped owner")
	}
	app.cfg.DB = failAddressDB{db, "scope_anchor_id"}
	app.scopesChanged("")
	app.subscriptionsChanged("")
	if _, err := app.CanRead(ctx, User{ID: "u-none"}, "Task", "t1"); !errors.Is(err, ErrInternal) {
		t.Errorf("CanRead on a scope failure: %v", err)
	}
	if app.webhookDeliverable(ctx, "e1", eventMeta{event: "task.transitioned", subjectType: "Task", subjectID: "t1"}) {
		t.Error("a scope failure delivered")
	}
}

func TestMigrateWebhookSubscriptions(t *testing.T) {
	app, db := subscriptionApp(t)
	_ = app
	createWebhookEndpointsTable(t, db)
	ctx := context.Background()
	events, _ := json.Marshal([]string{"task.transitioned"})
	for _, id := range []string{"e1", "e2"} {
		if _, err := db.Exec(`INSERT INTO smeldr_webhook_endpoints (id, events, target_url, secret_enc, created_at) VALUES (?, ?, 'https://x.example', 's', CURRENT_TIMESTAMP)`, id, string(events)); err != nil {
			t.Fatal(err)
		}
	}
	dry, err := MigrateWebhookSubscriptions(ctx, db, WebhookMigrationOptions{})
	if err != nil || dry.Applied || len(dry.Created) != 2 {
		t.Fatalf("dry run = %+v, %v", dry, err)
	}
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM smeldr_subscriptions`).Scan(&n)
	if n != 0 {
		t.Fatalf("a dry run wrote %d", n)
	}
	rep, err := MigrateWebhookSubscriptions(ctx, db, WebhookMigrationOptions{Apply: true})
	if err != nil || len(rep.Created) != 2 || rep.Created[0].Events[0] != "task.transitioned" {
		t.Fatalf("apply = %+v, %v", rep, err)
	}
	again, err := MigrateWebhookSubscriptions(ctx, db, WebhookMigrationOptions{Apply: true})
	if err != nil || len(again.Created) != 0 {
		t.Errorf("second run = %+v, %v", again, err)
	}
	if _, err := MigrateWebhookSubscriptions(ctx, nil, WebhookMigrationOptions{}); !errors.Is(err, ErrBadRequest) {
		t.Errorf("nil db: %v", err)
	}
	if rep, err := MigrateWebhookSubscriptions(ctx, newSQLiteDB(t), WebhookMigrationOptions{}); err != nil || len(rep.Created) != 0 {
		t.Errorf("no table: %+v, %v", rep, err)
	}
	if _, err := MigrateWebhookSubscriptions(ctx, failAddressDB{db, "FROM smeldr_webhook_endpoints e"}, WebhookMigrationOptions{}); !errors.Is(err, ErrInternal) {
		t.Errorf("query failure: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO smeldr_webhook_endpoints (id, events, target_url, secret_enc, created_at) VALUES ('e3', '[]', 'https://x.example', 's', CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	if _, err := MigrateWebhookSubscriptions(ctx, failAddressDB{db, "INSERT INTO smeldr_subscriptions"}, WebhookMigrationOptions{}); !errors.Is(err, ErrInternal) {
		t.Errorf("insert failure: %v", err)
	}
}

// fakeStreamSubs compiles a fixed connection subscription, failing on demand.
type fakeStreamSubs struct {
	owner string
	fail  atomic.Bool
}

func (f *fakeStreamSubs) compile(context.Context, User, string) (*connSubscriptions, *Subscription, error) {
	if f.fail.Load() {
		return nil, nil, ErrInternal
	}
	return &connSubscriptions{owner: f.owner}, nil, nil
}

func (f *fakeStreamSubs) saveCursor(context.Context, string, string) error { return nil }

// A connection fails closed when its scope or its subscriptions cannot be
// loaded again, or its revocation cannot be checked; a subscription owned by
// another token is delivered under that owner's scope.
func TestScopeReload_FailsClosed(t *testing.T) {
	prev := scopeCacheTTL
	scopeCacheTTL = 20 * time.Millisecond
	t.Cleanup(func() { scopeCacheTTL = prev })
	tok, err := SignToken(User{ID: "reader", Roles: []Role{Author}}, transitionItemTestSecret, 0)
	if err != nil {
		t.Fatal(err)
	}
	var scopeFails, revokeFails atomic.Bool
	var asked []string
	var mu sync.Mutex
	loadScope := func(_ context.Context, u User) (*ReadScope, error) {
		mu.Lock()
		asked = append(asked, u.ID)
		mu.Unlock()
		if scopeFails.Load() {
			return nil, ErrInternal
		}
		return &ReadScope{}, nil
	}
	revoked := func(context.Context, string) (bool, error) {
		if revokeFails.Load() {
			return false, ErrInternal
		}
		return false, nil
	}
	src := &fakeStreamSubs{owner: "owner"}
	b := newEventBroadcaster()
	srv := httptest.NewServer(newEventStreamHandlerWith(BearerHMAC(transitionItemTestSecret), b, loadScope, src, revoked))
	t.Cleanup(srv.Close)
	open := func() (*http.Response, chan envelope) {
		req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { resp.Body.Close() })
		return resp, lines(resp)
	}

	// The owner's scope is loaded for the connection.
	_, ch := open()
	waitBroadcaster(t, b, 1)
	mu.Lock()
	sawOwner := false
	for _, id := range asked {
		sawOwner = sawOwner || id == "owner"
	}
	mu.Unlock()
	if !sawOwner {
		t.Errorf("scopes loaded for %v, want the owner's", asked)
	}
	b.reloadToken("someone-else") // not this connection
	b.reloadToken("owner")
	src.fail.Store(true)
	expectClosed(t, ch)
	src.fail.Store(false)

	_, ch = open()
	waitBroadcaster(t, b, 1)
	scopeFails.Store(true)
	expectClosed(t, ch)

	scopeFails.Store(false)
	src.owner = "reader"
	_, ch = open()
	waitBroadcaster(t, b, 1)
	revokeFails.Store(true)
	expectClosed(t, ch)
	revokeFails.Store(false)

	src.owner = "owner"
	calls := 0
	failOwner := func(_ context.Context, u User) (*ReadScope, error) {
		calls++
		if u.ID == "owner" {
			return nil, ErrInternal
		}
		return &ReadScope{}, nil
	}
	// The owner's scope fails at connect: no stream.
	srv2 := httptest.NewServer(newEventStreamHandlerWith(BearerHMAC(transitionItemTestSecret), newEventBroadcaster(), failOwner, src, nil))
	t.Cleanup(srv2.Close)
	req, _ := http.NewRequest(http.MethodGet, srv2.URL, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError || calls != 2 {
		t.Errorf("owner scope failure = %d after %d loads, want 500 after 2", resp.StatusCode, calls)
	}
}

// The webhook paths fail closed or log, and the migration reports its
// database failures.
func TestWebhookSubscriptions_Failures(t *testing.T) {
	app, db, store := webhookApp(t)
	ctx := context.Background()
	if _, err := insertSubscription(ctx, db, &Subscription{Name: "r", Owner: "o", Delivery: "webhook:r",
		Filter: SubscriptionFilter{RelatedTo: &SubscriptionAnchor{Anchor: "a1", Kind: "contains"}}}); err != nil {
		t.Fatal(err)
	}
	app.subscriptionsChanged("")
	app.cfg.DB = failAddressDB{db, "smeldr_relations"}
	if app.webhookDeliverable(ctx, "r", eventMeta{event: "task.created"}) {
		t.Error("a related-set failure delivered")
	}
	app.cfg.DB = db
	app.subscriptionsChanged("")
	_ = app.webhookDeliverable(ctx, "r", eventMeta{event: "task.created", otherType: "task"})

	// Without a subscriptions table the store keeps delivering by event name.
	plain := &App{cfg: Config{DB: failAddressDB{db, "smeldr_subscriptions"}}, webhookStore: store, subscriptionsOn: true}
	store.deliverable = nil
	plain.wireWebhookSubscriptions()
	if store.deliverable != nil {
		t.Error("hooks wired without a table")
	}

	opts := WebhookMigrationOptions{Apply: true}
	if _, err := MigrateWebhookSubscriptions(ctx, failAddressDB{db, "WHERE 1=0"}, opts); !errors.Is(err, ErrInternal) {
		t.Errorf("table check failure: %v", err)
	}
}
