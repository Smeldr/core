//go:build integration

package pgx

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	smeldr "smeldr.dev/core"
)

// T947 on Postgres: CreateWebhookTables makes the three tables and both
// indexes, a second call keeps the rows, and a job and its delivery log are
// written and read back the way the worker does.
func TestPG_CreateWebhookTables(t *testing.T) {
	db, _ := isolatedDB(t)
	ctx := context.Background()
	if err := smeldr.CreateWebhookTables(db); err != nil {
		t.Fatal(err)
	}
	store := smeldr.NewWebhookStore(db, []byte("app-secret"))
	ep, _, err := store.Create(ctx, "https://8.8.8.8/hook", []string{"changecheck.transitioned"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := db.ExecContext(ctx, `INSERT INTO smeldr_outbound_jobs (id, endpoint_id, target_url, secret_enc, payload, event, attempts, next_retry_at, created_at, expires_at, status, event_id)
		VALUES ('j1', $1, $2, 'enc', $3, 'changecheck.transitioned', 0, $4, $4, $5, 'pending', 'ev1')`,
		ep.ID, ep.TargetURL, []byte(`{"event":"changecheck.transitioned"}`), now, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO smeldr_delivery_logs (id, job_id, attempted_at, status_code, duration_ms, error) VALUES ('l1', 'j1', $1, 200, 12, '')`, now); err != nil {
		t.Fatal(err)
	}
	if err := smeldr.CreateWebhookTables(db); err != nil {
		t.Fatalf("second call: %v", err)
	}
	var due int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM smeldr_outbound_jobs WHERE status = 'pending' AND next_retry_at <= $1 AND expires_at > $1`, now.Add(time.Second)).Scan(&due); err != nil || due != 1 {
		t.Errorf("due jobs = %d %v", due, err)
	}
	var logs int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM smeldr_delivery_logs WHERE job_id = 'j1' AND status_code = 200`).Scan(&logs); err != nil || logs != 1 {
		t.Errorf("delivery logs = %d %v", logs, err)
	}
	var idx int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pg_indexes WHERE indexname IN ('idx_smeldr_outbound_jobs_due', 'idx_smeldr_delivery_logs_job')`).Scan(&idx); err != nil || idx != 2 {
		t.Errorf("indexes = %d %v", idx, err)
	}
}

// T1146 on Postgres: of ten workers that run core's claim statement on the
// same due job at once, exactly one gets it; a worker whose fetch saw other
// attempts gets nothing; after the lease the job is due again.
func TestPG_OutboundClaimOnce(t *testing.T) {
	db, _ := isolatedDB(t)
	ctx := context.Background()
	if err := smeldr.CreateWebhookTables(db); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := db.ExecContext(ctx, `INSERT INTO smeldr_outbound_jobs (id, endpoint_id, target_url, secret_enc, payload, event, attempts, next_retry_at, created_at, expires_at, status)
		VALUES ('j1', 'ep1', 'https://8.8.8.8/hook', 'enc', $1, 'post.created', 0, $2, $2, $3, 'pending')`,
		[]byte(`{}`), now, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	lease := now.Add(2 * time.Minute)
	if res, err := db.ExecContext(ctx, smeldr.OutboundClaimSQL, lease, "j1", now, 1); err != nil {
		t.Fatal(err)
	} else if n, _ := res.RowsAffected(); n != 0 {
		t.Fatalf("a stale claim (attempts 1) got %d rows", n)
	}
	var won atomic.Int64
	var wg sync.WaitGroup
	errs := make(chan error, 10)
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := db.ExecContext(ctx, smeldr.OutboundClaimSQL, lease, "j1", now, 0)
			if err != nil {
				errs <- err
				return
			}
			n, _ := res.RowsAffected()
			won.Add(n)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if won.Load() != 1 {
		t.Fatalf("%d workers claimed the job, want 1", won.Load())
	}
	var due int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM smeldr_outbound_jobs WHERE status = 'pending' AND next_retry_at <= $1`, now.Add(time.Minute)).Scan(&due); err != nil || due != 0 {
		t.Errorf("due during the lease = %d %v", due, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM smeldr_outbound_jobs WHERE status = 'pending' AND next_retry_at <= $1 AND attempts = 0`, lease.Add(time.Second)).Scan(&due); err != nil || due != 1 {
		t.Errorf("due after the lease = %d %v", due, err)
	}
}

// T1304 on Postgres: of ten workers that claim the same due job with its
// holder, one wins and its claim ID is on the row; a retry of the held job
// only leaves a request, so the job is not due; after the lease a retry
// resets it.
func TestPG_OutboundClaimHolder(t *testing.T) {
	db, _ := isolatedDB(t)
	ctx := context.Background()
	if err := smeldr.CreateWebhookTables(db); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := db.ExecContext(ctx, `INSERT INTO smeldr_outbound_jobs (id, endpoint_id, target_url, secret_enc, payload, event, attempts, next_retry_at, created_at, expires_at, status)
		VALUES ('j1', 'ep1', 'https://8.8.8.8/hook', 'enc', $1, 'post.created', 0, $2, $2, $3, 'pending')`,
		[]byte(`{}`), now.Add(-time.Second), now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	lease := now.Add(2 * time.Minute)
	var won atomic.Int64
	var winner atomic.Value
	var wg sync.WaitGroup
	errs := make(chan error, 10)
	for i := range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := "claim-" + string(rune('a'+i))
			res, err := db.ExecContext(ctx, smeldr.OutboundClaimHolderSQL, lease, "j1", now, 0, id)
			if err != nil {
				errs <- err
				return
			}
			if n, _ := res.RowsAffected(); n == 1 {
				won.Add(1)
				winner.Store(id)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if won.Load() != 1 {
		t.Fatalf("%d workers claimed the job, want 1", won.Load())
	}
	var claimID string
	if err := db.QueryRowContext(ctx, `SELECT claim_id FROM smeldr_outbound_jobs WHERE id = 'j1'`).Scan(&claimID); err != nil || claimID != winner.Load() {
		t.Errorf("claim_id = %q %v, want %v", claimID, err, winner.Load())
	}

	app := smeldr.New(smeldr.Config{BaseURL: "http://localhost", Secret: []byte("test-secret-32-bytes-xxxxxxxxxxxx"), DB: db})
	app.Webhooks(smeldr.NewWebhookStore(db, []byte("app-secret")))
	if err := app.WebhookPool().RetryDead(ctx, "j1"); err != nil {
		t.Fatal(err)
	}
	var requested bool
	var next time.Time
	if err := db.QueryRowContext(ctx, `SELECT retry_requested, next_retry_at FROM smeldr_outbound_jobs WHERE id = 'j1'`).Scan(&requested, &next); err != nil {
		t.Fatal(err)
	}
	if !requested || !next.Equal(lease.Truncate(time.Microsecond)) {
		t.Errorf("held job after a retry: requested %v, next %v; want the request and the lease kept", requested, next)
	}

	if _, err := db.ExecContext(ctx, `UPDATE smeldr_outbound_jobs SET claimed_until = $1 WHERE id = 'j1'`, now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := app.WebhookPool().RetryDead(ctx, "j1"); err != nil {
		t.Fatal(err)
	}
	var cid string
	if err := db.QueryRowContext(ctx, `SELECT retry_requested, claim_id FROM smeldr_outbound_jobs WHERE id = 'j1'`).Scan(&requested, &cid); err != nil || requested || cid != "" {
		t.Errorf("after the lease: requested %v, claim %q, %v; want reset", requested, cid, err)
	}
}

// T1266 on Postgres: webhook retry re-queues a dead job and a pending one in
// its backoff, refuses a delivered job without changing it, and refuses an id
// that names no job. The App's pool is used without App.Run, so no worker
// delivers anything.
func TestPG_WebhookRetry(t *testing.T) {
	db, _ := isolatedDB(t)
	ctx := context.Background()
	if err := smeldr.CreateWebhookTables(db); err != nil {
		t.Fatal(err)
	}
	app := smeldr.New(smeldr.Config{BaseURL: "http://localhost", Secret: []byte("test-secret-32-bytes-xxxxxxxxxxxx"), DB: db})
	app.Webhooks(smeldr.NewWebhookStore(db, []byte("app-secret")))
	pool := app.WebhookPool()
	now := time.Now().UTC()
	for _, j := range []struct {
		id, status string
		attempts   int
		next       time.Time
	}{
		{"dead", "dead", 7, now.Add(-time.Hour)},
		{"backoff", "pending", 3, now.Add(time.Hour)},
		{"delivered", "delivered", 1, now.Add(-time.Hour)},
	} {
		if _, err := db.ExecContext(ctx, `INSERT INTO smeldr_outbound_jobs (id, endpoint_id, target_url, secret_enc, payload, event, attempts, next_retry_at, created_at, expires_at, status)
			VALUES ($1, 'ep1', 'https://8.8.8.8/hook', 'enc', $2, 'post.created', $3, $4, $5, $6, $7)`,
			j.id, []byte(`{}`), j.attempts, j.next, now.Add(-2*time.Hour), now.Add(time.Hour), j.status); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"dead", "backoff"} {
		if err := pool.RetryDead(ctx, id); err != nil {
			t.Fatalf("retry %s: %v", id, err)
		}
		var status string
		var attempts int
		var next time.Time
		if err := db.QueryRowContext(ctx, `SELECT status, attempts, next_retry_at FROM smeldr_outbound_jobs WHERE id = $1`, id).Scan(&status, &attempts, &next); err != nil {
			t.Fatal(err)
		}
		if status != "pending" || attempts != 0 || next.After(time.Now().Add(time.Second)) {
			t.Errorf("%s after retry: %s, attempts %d, next %v", id, status, attempts, next)
		}
	}
	err := pool.RetryDead(ctx, "delivered")
	if smeldr.ProblemTypeOf(err) != "webhook.job_delivered" || smeldr.ProblemValuesOf(err)["status"] != "delivered" {
		t.Errorf("retry delivered: %v (type %q)", err, smeldr.ProblemTypeOf(err))
	}
	var status string
	var attempts int
	if err := db.QueryRowContext(ctx, `SELECT status, attempts FROM smeldr_outbound_jobs WHERE id = 'delivered'`).Scan(&status, &attempts); err != nil || status != "delivered" || attempts != 1 {
		t.Errorf("the delivered job changed: %s, attempts %d, %v", status, attempts, err)
	}
	if err := pool.RetryDead(ctx, "no-such-job"); smeldr.ProblemTypeOf(err) != "webhook.job_not_found" {
		t.Errorf("retry unknown: %v (type %q)", err, smeldr.ProblemTypeOf(err))
	}
}
