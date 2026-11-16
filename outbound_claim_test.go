package smeldr

import (
	"context"
	"database/sql"
	"math/rand"
	"strings"
	"testing"
	"time"
)

// T1304: a claim names its holder, so a retry of a job a worker holds never
// makes a second worker deliver it.

// claimTestDB is outboundTestDB with the claim columns added, as
// EnsureOutboundJobClaimColumns adds them to a table from before T1304.
func claimTestDB(t *testing.T) (*workerPool, *WebhookStore, *fakeClock) {
	t.Helper()
	pool, store := outboundTestDB(t)
	if err := EnsureOutboundJobClaimColumns(context.Background(), pool.db); err != nil {
		t.Fatal(err)
	}
	clk := newFakeClock(time.Now().UTC().Truncate(time.Second))
	pool.clock = clk
	return pool, store, clk
}

type claimRow struct {
	status       string
	attempts     int
	next         time.Time
	expires      time.Time
	claimID      string
	claimedUntil sql.NullString
	retry        bool
}

func readClaimRow(t *testing.T, pool *workerPool, id string) claimRow {
	t.Helper()
	var r claimRow
	if err := pool.db.QueryRowContext(context.Background(),
		`SELECT status, attempts, next_retry_at, expires_at, claim_id, claimed_until, retry_requested FROM smeldr_outbound_jobs WHERE id = $1`, id).
		Scan(&r.status, &r.attempts, scanDest(&r.next), scanDest(&r.expires), &r.claimID, &r.claimedUntil, &r.retry); err != nil {
		t.Fatal(err)
	}
	return r
}

// The race of T1266, shown fixed: a retry while the holder delivers leaves
// the job held, so no other worker can take it. If the attempt succeeds the
// job is delivered once; if it fails the job is due now and the next worker
// sends it, one attempt after the other.
func TestClaim_RetryWhileHeld(t *testing.T) {
	for _, tc := range []struct {
		name    string
		failing bool
	}{{"the attempt succeeds", false}, {"the attempt fails", true}} {
		t.Run(tc.name, func(t *testing.T) {
			pool, store, clk := claimTestDB(t)
			ctx := context.Background()
			job := enqueueDueJob(t, pool, store)
			deliveries := 0
			pool.deliver = func(context.Context, OutboundJob, []byte) error {
				deliveries++
				if deliveries == 1 {
					before := readClaimRow(t, pool, job.ID)
					if err := pool.RetryDead(ctx, job.ID); err != nil {
						t.Fatalf("retry while held: %v", err)
					}
					held := readClaimRow(t, pool, job.ID)
					if !held.retry || held.attempts != before.attempts || !held.next.Equal(before.next) || !held.expires.Equal(before.expires) || held.claimID != before.claimID {
						t.Errorf("a held job's retry changed more than the request: %+v, was %+v", held, before)
					}
					if due, err := pool.fetchDueJobs(ctx); err != nil || len(due) != 0 {
						t.Errorf("another worker could take the held job: %v %v", due, err)
					}
				}
				if tc.failing {
					return &webhookHTTPError{statusCode: 503}
				}
				return nil
			}
			if err := pool.processJob(ctx, job, rand.New(rand.NewSource(1))); err != nil {
				t.Fatal(err)
			}
			after := readClaimRow(t, pool, job.ID)
			if after.claimID != "" || after.claimedUntil.Valid || after.retry {
				t.Errorf("the claim or the request was left on the row: %+v", after)
			}
			due, err := pool.fetchDueJobs(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if !tc.failing {
				if after.status != "delivered" || len(due) != 0 || deliveries != 1 {
					t.Errorf("delivered once: status %s, due %d, deliveries %d", after.status, len(due), deliveries)
				}
				return
			}
			if after.status != "pending" || after.attempts != 0 || !after.next.Equal(clk.Now()) || !after.expires.Equal(clk.Now().Add(outboundRetryWindow)) || len(due) != 1 {
				t.Fatalf("after a failed attempt with a retry asked for: %+v, due %d", after, len(due))
			}
			if err := pool.processJob(ctx, due[0], rand.New(rand.NewSource(1))); err != nil {
				t.Fatal(err)
			}
			if deliveries != 2 {
				t.Errorf("deliveries = %d; want 2, one after the other", deliveries)
			}
		})
	}
}

// "Held" is the lease's own time, not next_retry_at: a claim whose lease ran
// out, or a claim_id left behind by a writer that does not know the columns
// with next_retry_at far ahead, is reset by a retry, not flagged (P488).
func TestClaim_RetryAfterTheLease(t *testing.T) {
	pool, store, clk := claimTestDB(t)
	ctx := context.Background()
	job := enqueueDueJob(t, pool, store)
	if ok, _, err := pool.claimJob(ctx, job); err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	clk.Advance(outboundLease + time.Second)
	if err := pool.RetryDead(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	r := readClaimRow(t, pool, job.ID)
	if r.retry || r.claimID != "" || r.claimedUntil.Valid || r.attempts != 0 || !r.next.Equal(clk.Now()) {
		t.Errorf("after the lease: %+v", r)
	}

	old := enqueueDueJob(t, pool, store)
	if _, err := pool.db.ExecContext(ctx, `UPDATE smeldr_outbound_jobs SET claim_id = 'left-behind', claimed_until = $1, next_retry_at = $2, attempts = 2 WHERE id = $3`,
		clk.Now().Add(-time.Minute), clk.Now().Add(time.Hour), old.ID); err != nil {
		t.Fatal(err)
	}
	if err := pool.RetryDead(ctx, old.ID); err != nil {
		t.Fatal(err)
	}
	r = readClaimRow(t, pool, old.ID)
	if r.retry || r.claimID != "" || r.attempts != 0 || !r.next.Equal(clk.Now()) {
		t.Errorf("a claim left behind with next_retry_at an hour ahead: %+v; want reset", r)
	}
}

// A holder that dies with the request set: after the lease the next worker
// claims the job, and the request is honoured after its attempt (P488).
func TestClaim_HolderDiesWithRequest(t *testing.T) {
	pool, store, clk := claimTestDB(t)
	ctx := context.Background()
	job := enqueueDueJob(t, pool, store)
	if ok, _, err := pool.claimJob(ctx, job); err != nil || !ok {
		t.Fatalf("A's claim: %v %v", ok, err)
	}
	if err := pool.RetryDead(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	clk.Advance(outboundLease + time.Second) // A died
	due, err := pool.fetchDueJobs(ctx)
	if err != nil || len(due) != 1 {
		t.Fatalf("due after the lease: %v %v", due, err)
	}
	pool.deliver = func(context.Context, OutboundJob, []byte) error { return &webhookHTTPError{statusCode: 503} }
	if err := pool.processJob(ctx, due[0], rand.New(rand.NewSource(1))); err != nil {
		t.Fatal(err)
	}
	r := readClaimRow(t, pool, job.ID)
	if r.status != "pending" || r.attempts != 0 || !r.next.Equal(clk.Now()) || r.retry {
		t.Errorf("B's failed attempt did not honour the request: %+v", r)
	}
}

// A holder whose claim was taken over changes nothing; the new holder's row
// stands, and the old holder's delivery log is still written.
func TestClaim_TakenOver(t *testing.T) {
	pool, store, clk := claimTestDB(t)
	ctx := context.Background()
	job := enqueueDueJob(t, pool, store)
	_, claimA, err := pool.claimJob(ctx, job)
	if err != nil || claimA == "" {
		t.Fatalf("A's claim: %q %v", claimA, err)
	}
	clk.Advance(outboundLease + time.Second)
	due, _ := pool.fetchDueJobs(ctx)
	ok, claimB, err := pool.claimJob(ctx, due[0])
	if err != nil || !ok || claimB == claimA {
		t.Fatalf("B's claim: %v %q %v", ok, claimB, err)
	}
	a := job
	a.Attempts = 1
	if err := pool.updateJobAfterAttempt(ctx, a, claimA, true, 0); err != nil {
		t.Fatal(err)
	}
	if err := pool.moveToDeadLetter(ctx, a, claimA); err != nil {
		t.Fatal(err)
	}
	r := readClaimRow(t, pool, job.ID)
	if r.status != "pending" || r.claimID != claimB || r.attempts != 0 {
		t.Errorf("A wrote over B's claim: %+v", r)
	}
}

// Dead-lettering a job with a request set leaves it pending and due now;
// without one it is dead, and the claim is cleared either way.
func TestClaim_DeadLetter(t *testing.T) {
	for _, requested := range []bool{false, true} {
		pool, store, clk := claimTestDB(t)
		ctx := context.Background()
		job := enqueueDueJob(t, pool, store)
		_, claimID, err := pool.claimJob(ctx, job)
		if err != nil {
			t.Fatal(err)
		}
		if requested {
			if err := pool.RetryDead(ctx, job.ID); err != nil {
				t.Fatal(err)
			}
		}
		if err := pool.moveToDeadLetter(ctx, job, claimID); err != nil {
			t.Fatal(err)
		}
		r := readClaimRow(t, pool, job.ID)
		want := "dead"
		if requested {
			want = "pending"
		}
		if r.status != want || r.claimID != "" || r.retry || (requested && !r.next.Equal(clk.Now())) {
			t.Errorf("requested %v: %+v; want %s", requested, r, want)
		}
	}
}

// With the columns the refusals of T1266 stand.
func TestClaim_RetryRefusals(t *testing.T) {
	pool, store, _ := claimTestDB(t)
	ctx := context.Background()
	job := enqueueDueJob(t, pool, store)
	if _, err := pool.db.ExecContext(ctx, `UPDATE smeldr_outbound_jobs SET status = 'delivered' WHERE id = $1`, job.ID); err != nil {
		t.Fatal(err)
	}
	if err := pool.RetryDead(ctx, job.ID); ProblemTypeOf(err) != problemWebhookJobDelivered {
		t.Errorf("delivered: %v", err)
	}
	if err := pool.RetryDead(ctx, "nothing"); ProblemTypeOf(err) != problemWebhookJobNotFound {
		t.Errorf("unknown: %v", err)
	}
}

// The holder's writes fail: the error is returned; RowsAffected failing too.
func TestClaim_HolderWriteFails(t *testing.T) {
	pool, store, _ := claimTestDB(t)
	ctx := context.Background()
	job := enqueueDueJob(t, pool, store)
	_, claimID, _ := pool.claimJob(ctx, job)
	db := pool.db
	pool.db = failAddressDB{DB: db, match: "claim_id = $"}
	if err := pool.updateJobAfterAttempt(ctx, job, claimID, true, 0); err == nil {
		t.Error("a failed update returned nil")
	}
	pool.db = &rowsAffectedFailDB{DB: db, failOn: "claim_id = $"}
	if err := pool.moveToDeadLetter(ctx, job, claimID); err == nil || !strings.Contains(err.Error(), "simulated rows affected error") {
		t.Errorf("RowsAffected failing: %v", err)
	}
	pool.db = failAddressDB{DB: db, match: "claimed_until = $1"}
	if _, _, err := pool.claimJob(ctx, job); err == nil {
		t.Error("a failed claim returned nil")
	}
}

// EnsureOutboundJobClaimColumns is idempotent, leaves a database without the
// table alone, and CreateWebhookTables makes the columns.
func TestEnsureOutboundJobClaimColumns(t *testing.T) {
	ctx := context.Background()
	pool, _, _ := claimTestDB(t)
	if err := EnsureOutboundJobClaimColumns(ctx, pool.db); err != nil {
		t.Fatalf("second call: %v", err)
	}
	empty := newSQLiteDB(t)
	if err := EnsureOutboundJobClaimColumns(ctx, empty); err != nil {
		t.Fatalf("no table: %v", err)
	}
	fresh := newSQLiteDB(t)
	if err := CreateWebhookTables(fresh); err != nil {
		t.Fatal(err)
	}
	for _, c := range outboundClaimColumns {
		if ok, err := columnExists(ctx, fresh, "smeldr_outbound_jobs", c); err != nil || !ok {
			t.Errorf("CreateWebhookTables: column %s: %v %v", c, ok, err)
		}
	}
	broken := failAddressDB{DB: pool.db, match: "ALTER TABLE"}
	other := newSQLiteDB(t)
	if _, err := other.ExecContext(ctx, `CREATE TABLE smeldr_outbound_jobs (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	broken.DB = other
	if err := EnsureOutboundJobClaimColumns(ctx, broken); err == nil {
		t.Error("a failing ALTER returned nil")
	}
}
