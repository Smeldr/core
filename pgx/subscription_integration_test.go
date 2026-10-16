//go:build integration

package pgx

import (
	"context"
	"sync"
	"testing"

	smeldr "smeldr.dev/core"
)

// D116 on Postgres: the subscription table, N addresses, concurrent first
// uses giving exactly three defaults, and the owner rule.
func TestPG_Subscriptions(t *testing.T) {
	db, _ := isolatedDB(t)
	if err := smeldr.CreateOrchestrationTables(db); err != nil {
		t.Fatal(err)
	}
	if err := smeldr.CreateSubscriptionTables(db); err != nil {
		t.Fatal(err)
	}
	if err := smeldr.CreateSubscriptionTables(db); err != nil {
		t.Fatalf("second create: %v", err)
	}
	app := smeldr.New(smeldr.Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16"), DB: db})
	smeldr.RegisterOrchestrationTypes(app, db)
	_ = app.Handler()

	u1 := smeldr.NewContextWithUser(smeldr.User{ID: "u1", Roles: []smeldr.Role{smeldr.Author}})
	var wg sync.WaitGroup
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := app.ListSubscriptions(u1, ""); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	list, err := app.ListSubscriptions(u1, "")
	if err != nil || len(list) != 3 {
		t.Fatalf("defaults = %d, %v; want 3", len(list), err)
	}
	s, err := app.CreateSubscription(u1, smeldr.SubscriptionSpec{Name: "tasks", Filter: smeldr.SubscriptionFilter{Types: []string{"Task"}}})
	if err != nil || len(s.Address) < 2 || s.Address[0] != 'N' {
		t.Fatalf("create = %+v, %v", s, err)
	}
	u2 := smeldr.NewContextWithUser(smeldr.User{ID: "u2", Roles: []smeldr.Role{smeldr.Author}})
	if _, err := app.GetSubscription(u2, s.Address); err == nil {
		t.Error("another token read it")
	}
	if got, err := app.GetSubscription(u1, s.Address); err != nil || got.ID != s.ID {
		t.Errorf("get by address = %v, %v", got, err)
	}
}

// D116 on Postgres: the webhook migration gives each endpoint one
// subscription, owned by legacy-admin, and runs idempotently.
func TestPG_MigrateWebhookSubscriptions(t *testing.T) {
	db, _ := isolatedDB(t)
	ctx := context.Background()
	if err := smeldr.CreateOrchestrationTables(db); err != nil {
		t.Fatal(err)
	}
	if err := smeldr.CreateSubscriptionTables(db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE smeldr_webhook_endpoints (
		id TEXT PRIMARY KEY, events TEXT NOT NULL, target_url TEXT NOT NULL,
		secret_enc TEXT NOT NULL, active BOOLEAN NOT NULL DEFAULT TRUE, created_at TIMESTAMPTZ NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"e1", "e2"} {
		if _, err := db.ExecContext(ctx, `INSERT INTO smeldr_webhook_endpoints (id, events, target_url, secret_enc, created_at) VALUES ($1, '["task.transitioned"]', 'https://x.example', 's', now())`, id); err != nil {
			t.Fatal(err)
		}
	}
	dry, err := smeldr.MigrateWebhookSubscriptions(ctx, db, smeldr.WebhookMigrationOptions{})
	if err != nil || len(dry.Created) != 2 {
		t.Fatalf("dry run = %+v, %v", dry, err)
	}
	count := func() int {
		var n int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM smeldr_subscriptions WHERE owner = 'legacy-admin'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count(); n != 0 {
		t.Fatalf("a dry run wrote %d", n)
	}
	rep, err := smeldr.MigrateWebhookSubscriptions(ctx, db, smeldr.WebhookMigrationOptions{Apply: true})
	if err != nil || len(rep.Created) != 2 || rep.Created[0].Subscription == "" {
		t.Fatalf("apply = %+v, %v", rep, err)
	}
	if n := count(); n != 2 {
		t.Errorf("subscriptions = %d, want 2", n)
	}
	again, err := smeldr.MigrateWebhookSubscriptions(ctx, db, smeldr.WebhookMigrationOptions{Apply: true})
	if err != nil || len(again.Created) != 0 {
		t.Errorf("second run = %+v, %v", again, err)
	}
}
