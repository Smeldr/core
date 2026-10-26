//go:build integration

package pgx

import (
	"context"
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
