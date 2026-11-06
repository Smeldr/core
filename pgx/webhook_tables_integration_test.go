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
