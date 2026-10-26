package smeldr

import (
	"context"
	"math/rand"
	"strings"
	"testing"
	"time"
)

// webhookObjects counts the webhook tables and indexes present.
func webhookObjects(t *testing.T, db DB) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM sqlite_master WHERE name IN ('smeldr_webhook_endpoints', 'smeldr_outbound_jobs', 'smeldr_delivery_logs',
			'idx_smeldr_outbound_jobs_due', 'idx_smeldr_delivery_logs_job')`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestCreateWebhookTables (T947): a fresh database gets the three tables and
// two indexes; a second call keeps the rows; a job table from before D117
// gains event_id.
func TestCreateWebhookTables(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()
	if err := CreateWebhookTables(db); err != nil {
		t.Fatal(err)
	}
	if n := webhookObjects(t, db); n != 5 {
		t.Fatalf("objects after the first call = %d, want 5", n)
	}
	now := time.Now().UTC()
	for _, stmt := range []string{
		`INSERT INTO smeldr_webhook_endpoints (id, events, target_url, secret_enc, created_at) VALUES ('ep', '[]', 'https://x', 's', $1)`,
		`INSERT INTO smeldr_outbound_jobs (id, endpoint_id, target_url, secret_enc, payload, event, next_retry_at, created_at, expires_at) VALUES ('j', 'ep', 'https://x', 's', 'p', 'e', $1, $1, $1)`,
		`INSERT INTO smeldr_delivery_logs (id, job_id, attempted_at) VALUES ('l', 'j', $1)`,
	} {
		if _, err := db.ExecContext(ctx, stmt, now); err != nil {
			t.Fatal(err)
		}
	}
	if err := CreateWebhookTables(db); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"smeldr_webhook_endpoints", "smeldr_outbound_jobs", "smeldr_delivery_logs"} {
		var n int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(&n); err != nil || n != 1 {
			t.Errorf("%s after a second call: %d rows, %v", table, n, err)
		}
	}

	// A job table made by hand before D117, without event_id.
	old := newSQLiteDB(t)
	if _, err := old.ExecContext(ctx, `CREATE TABLE smeldr_outbound_jobs (id TEXT PRIMARY KEY, endpoint_id TEXT NOT NULL, target_url TEXT NOT NULL,
		secret_enc TEXT NOT NULL, payload BLOB NOT NULL, event TEXT NOT NULL, attempts INTEGER NOT NULL DEFAULT 0,
		next_retry_at TIMESTAMPTZ NOT NULL, created_at TIMESTAMPTZ NOT NULL, expires_at TIMESTAMPTZ NOT NULL, status TEXT NOT NULL DEFAULT 'pending')`); err != nil {
		t.Fatal(err)
	}
	if err := CreateWebhookTables(old); err != nil {
		t.Fatal(err)
	}
	if _, err := old.ExecContext(ctx, `SELECT event_id FROM smeldr_outbound_jobs`); err != nil {
		t.Errorf("event_id was not added: %v", err)
	}
}

// TestCreateWebhookTables_Errors: a failing statement names its table.
func TestCreateWebhookTables_Errors(t *testing.T) {
	for _, table := range []string{"smeldr_webhook_endpoints", "smeldr_outbound_jobs", "smeldr_delivery_logs"} {
		err := CreateWebhookTables(&execFailDB{DB: newSQLiteDB(t), failOn: table})
		if err == nil || !strings.Contains(err.Error(), table) {
			t.Errorf("%s fails: %v", table, err)
		}
	}
}

// TestWebhookDelivery_FreshDatabase (T947): on a database where only
// CreateWebhookTables ran, an event reaches its endpoint's job, the worker
// delivers it, and the delivery is logged.
func TestWebhookDelivery_FreshDatabase(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()
	if err := CreateWebhookTables(db); err != nil {
		t.Fatal(err)
	}
	store := NewWebhookStore(db, []byte("app-secret"))
	app := New(Config{BaseURL: "https://example.com", DB: db, Secret: []byte("test-secret-that-is-at-least-32-bytes")})
	app.Webhooks(store)
	if _, _, err := store.Create(ctx, "https://8.8.8.8/hook", []string{"task.transitioned"}); err != nil {
		t.Fatal(err)
	}
	dispatchTransitionWebhook(ctx, store, app.webhookPool, nil, "", "task.transitioned", transitionWebhookData{Type: "task", ID: "1", ToState: "active"})
	jobs, err := app.webhookPool.fetchDueJobs(ctx)
	if err != nil || len(jobs) != 1 || jobs[0].Event != "task.transitioned" {
		t.Fatalf("due jobs = %+v %v", jobs, err)
	}
	var got []byte
	app.webhookPool.deliver = func(_ context.Context, _ OutboundJob, secret []byte) error {
		got = secret
		return nil
	}
	if err := app.webhookPool.processJob(ctx, jobs[0], rand.New(rand.NewSource(1))); err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		t.Error("the worker delivered without the endpoint's secret")
	}
	logs, err := app.webhookPool.ListDeliveryLogs(ctx, jobs[0].ID)
	if err != nil || len(logs) != 1 || logs[0].StatusCode != 200 {
		t.Errorf("delivery logs = %+v %v", logs, err)
	}
}
