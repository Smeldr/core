package smeldr

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeClock is a controllable Clock for tests.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock(t time.Time) *fakeClock { return &fakeClock{now: t} }
func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}
func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	ch <- c.Now().Add(d)
	return ch
}

// outboundTestDB creates an in-memory SQLite DB with both outbound tables.
func outboundTestDB(t *testing.T) (*workerPool, *WebhookStore) {
	t.Helper()
	db := newSQLiteDB(t)
	ctx := context.Background()
	_, err := db.ExecContext(ctx, `
		CREATE TABLE smeldr_outbound_jobs (
			id TEXT PRIMARY KEY, endpoint_id TEXT NOT NULL, target_url TEXT NOT NULL,
			secret_enc TEXT NOT NULL, payload BLOB NOT NULL, event TEXT NOT NULL,
			attempts INTEGER NOT NULL DEFAULT 0, next_retry_at TIMESTAMPTZ NOT NULL,
			created_at TIMESTAMPTZ NOT NULL, expires_at TIMESTAMPTZ NOT NULL,
			status TEXT NOT NULL DEFAULT 'pending'
		)`)
	if err != nil {
		t.Fatalf("create smeldr_outbound_jobs: %v", err)
	}
	_, err = db.ExecContext(ctx, `
		CREATE TABLE smeldr_delivery_logs (
			id TEXT PRIMARY KEY, job_id TEXT NOT NULL, attempted_at TIMESTAMPTZ NOT NULL,
			status_code INTEGER NOT NULL DEFAULT 0, duration_ms INTEGER NOT NULL DEFAULT 0,
			error TEXT NOT NULL DEFAULT ''
		)`)
	if err != nil {
		t.Fatalf("create smeldr_delivery_logs: %v", err)
	}
	store := NewWebhookStore(db, []byte("app-secret"))
	pool := newWorkerPool(db, store, realClock{}, 2)
	return pool, store
}

// TestSignPayload verifies HMAC signing format.
func TestSignPayload(t *testing.T) {
	secret := []byte("my-secret")
	body := []byte(`{"event":"post.published"}`)
	sig := signPayload(secret, 1700000000, body)
	if len(sig) != len("sha256=")+64 {
		t.Errorf("unexpected sig length: %q", sig)
	}
	if sig[:7] != "sha256=" {
		t.Errorf("signature prefix: got %q", sig[:7])
	}
	// Same input → same signature
	sig2 := signPayload(secret, 1700000000, body)
	if sig != sig2 {
		t.Error("signPayload is not deterministic for same inputs")
	}
	// Different timestamp → different signature
	sig3 := signPayload(secret, 1700000001, body)
	if sig == sig3 {
		t.Error("different timestamp should produce different signature")
	}
}

// TestBackoffDelay checks exponential growth and cap.
func TestBackoffDelay(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	prev := time.Duration(0)
	for attempt := 1; attempt <= 10; attempt++ {
		d := backoffDelay(attempt, rng)
		if d < time.Second {
			t.Errorf("attempt %d: delay %v is below 1s minimum", attempt, d)
		}
		if d > time.Hour {
			t.Errorf("attempt %d: delay %v exceeds 1h cap", attempt, d)
		}
		if attempt <= 6 && d < prev {
			t.Errorf("attempt %d: delay %v decreased from previous %v", attempt, d, prev)
		}
		prev = d
	}
	// High attempt should be capped at 1h.
	d := backoffDelay(20, rng)
	if d > time.Hour {
		t.Errorf("high attempt delay %v exceeds 1h cap", d)
	}
}

// TestWorkerPool_EnqueueAndDeliver verifies end-to-end job delivery via a
// fake deliver func and fake clock.
func TestWorkerPool_EnqueueAndDeliver(t *testing.T) {
	pool, store := outboundTestDB(t)
	ctx := context.Background()
	clk := newFakeClock(time.Now().UTC())
	pool.clock = clk

	var delivered int32
	pool.deliver = func(_ context.Context, _ OutboundJob, _ []byte) error {
		atomic.AddInt32(&delivered, 1)
		return nil
	}

	enc, _ := store.encryptSecret([]byte("secret"))
	now := clk.Now()
	job := OutboundJob{
		ID:          NewID(),
		EndpointID:  "ep-1",
		TargetURL:   "https://example.com/hook",
		SecretEnc:   enc,
		Payload:     []byte(`{"event":"post.published"}`),
		Event:       "post.published",
		Attempts:    0,
		NextRetryAt: now,
		CreatedAt:   now,
		ExpiresAt:   now.Add(24 * time.Hour),
		Status:      "pending",
	}
	if err := pool.Enqueue(ctx, job); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	jobs, err := pool.fetchDueJobs(ctx)
	if err != nil {
		t.Fatalf("fetchDueJobs: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("want 1 due job, got %d", len(jobs))
	}

	rng := rand.New(rand.NewSource(1))
	if err := pool.processJob(ctx, jobs[0], rng); err != nil {
		t.Fatalf("processJob: %v", err)
	}
	if atomic.LoadInt32(&delivered) != 1 {
		t.Fatal("deliver func was not called")
	}

	// After success, job should be marked delivered.
	// Close rows explicitly before next query to avoid holding the single connection.
	func() {
		rows, _ := pool.db.QueryContext(ctx, `SELECT status FROM smeldr_outbound_jobs WHERE id = $1`, job.ID)
		defer rows.Close()
		var status string
		rows.Next()
		rows.Scan(&status)
		if status != "delivered" {
			t.Errorf("job status: got %q want delivered", status)
		}
	}()

	// Delivery log should exist.
	logs, err := pool.ListDeliveryLogs(ctx, job.ID)
	if err != nil {
		t.Fatalf("ListDeliveryLogs: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("want 1 delivery log, got %d", len(logs))
	}
	if logs[0].StatusCode != 200 {
		t.Errorf("log status code: got %d want 200", logs[0].StatusCode)
	}
}

// TestWorkerPool_RetryOnFailure verifies that a job is retried on transient error.
func TestWorkerPool_RetryOnFailure(t *testing.T) {
	pool, store := outboundTestDB(t)
	ctx := context.Background()
	clk := newFakeClock(time.Now().UTC())
	pool.clock = clk

	callCount := 0
	pool.deliver = func(_ context.Context, _ OutboundJob, _ []byte) error {
		callCount++
		return &webhookHTTPError{statusCode: 503}
	}

	enc, _ := store.encryptSecret([]byte("secret"))
	now := clk.Now()
	job := OutboundJob{
		ID:          NewID(),
		EndpointID:  "ep-retry",
		TargetURL:   "https://example.com/hook",
		SecretEnc:   enc,
		Payload:     []byte(`{}`),
		Event:       "post.created",
		NextRetryAt: now,
		CreatedAt:   now,
		ExpiresAt:   now.Add(24 * time.Hour),
		Status:      "pending",
	}
	if err := pool.Enqueue(ctx, job); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	rng := rand.New(rand.NewSource(1))
	_ = pool.processJob(ctx, job, rng)

	// Job should still be pending (not dead) after first failure.
	rows, _ := pool.db.QueryContext(ctx, `SELECT status, attempts FROM smeldr_outbound_jobs WHERE id = $1`, job.ID)
	defer rows.Close()
	var status string
	var attempts int
	rows.Next()
	rows.Scan(&status, &attempts)
	if status != "pending" {
		t.Errorf("after first failure: status %q want pending", status)
	}
	if attempts != 1 {
		t.Errorf("after first failure: attempts %d want 1", attempts)
	}
}

// TestWorkerPool_DeadLetter verifies that a job exceeding maxAttempts is dead-lettered.
func TestWorkerPool_DeadLetter(t *testing.T) {
	pool, store := outboundTestDB(t)
	ctx := context.Background()
	pool.deliver = func(_ context.Context, _ OutboundJob, _ []byte) error {
		return &webhookHTTPError{statusCode: 500}
	}

	enc, _ := store.encryptSecret([]byte("s"))
	now := time.Now().UTC()
	rng := rand.New(rand.NewSource(1))

	// Simulate a job already at maxAttempts-1 (6), so one more attempt → dead.
	job := OutboundJob{
		ID:          NewID(),
		EndpointID:  "ep-dead",
		TargetURL:   "https://example.com/hook",
		SecretEnc:   enc,
		Payload:     []byte(`{}`),
		Event:       "post.created",
		Attempts:    6, // one more → 7 = maxAttempts
		NextRetryAt: now,
		CreatedAt:   now,
		ExpiresAt:   now.Add(24 * time.Hour),
		Status:      "pending",
	}
	if err := pool.Enqueue(ctx, job); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	_ = pool.processJob(ctx, job, rng)

	rows, _ := pool.db.QueryContext(ctx, `SELECT status FROM smeldr_outbound_jobs WHERE id = $1`, job.ID)
	defer rows.Close()
	var status string
	rows.Next()
	rows.Scan(&status)
	if status != "dead" {
		t.Errorf("after maxAttempts: status %q want dead", status)
	}
}

// TestWorkerPool_CircuitBreaker verifies that consecutive failures open the circuit.
func TestWorkerPool_CircuitBreaker(t *testing.T) {
	clk := newFakeClock(time.Now().UTC())
	pool := &workerPool{
		clock:    clk,
		circuits: make(map[string]*circuitState),
	}
	const ep = "ep-circuit"

	// Record 5 consecutive failures to open the circuit.
	for i := 0; i < 5; i++ {
		pool.recordCircuitResult(ep, false)
	}
	if pool.circuitAllows(ep) {
		t.Fatal("circuit should be open after 5 failures")
	}

	// Advance past openUntil — half-open probe should be allowed.
	clk.Advance(6 * time.Minute)
	if !pool.circuitAllows(ep) {
		t.Fatal("circuit should allow probe after openUntil elapses")
	}

	// Successful probe should close the circuit.
	pool.recordCircuitResult(ep, true)
	if !pool.circuitAllows(ep) {
		t.Fatal("circuit should be closed after successful probe")
	}
}

// RetryDead queues a dead or pending job for delivery now, and refuses a
// delivered job and an unknown id without changing anything (T1266).
func TestWorkerPool_RetryDead(t *testing.T) {
	ctx := context.Background()
	type row struct {
		status   string
		attempts int
		next     time.Time
		expires  time.Time
	}
	read := func(t *testing.T, pool *workerPool, id string) row {
		t.Helper()
		var r row
		if err := pool.db.QueryRowContext(ctx, `SELECT status, attempts, next_retry_at, expires_at FROM smeldr_outbound_jobs WHERE id = $1`, id).
			Scan(&r.status, &r.attempts, scanDest(&r.next), scanDest(&r.expires)); err != nil {
			t.Fatal(err)
		}
		return r
	}
	now := time.Now().UTC().Truncate(time.Second)
	tests := []struct {
		name     string
		status   string
		attempts int
		next     time.Time
		expires  time.Time
		queued   bool // re-queued and due now
	}{
		{"dead", "dead", 7, now.Add(-time.Hour), now.Add(time.Hour), true},
		{"pending in its backoff", "pending", 3, now.Add(time.Hour), now.Add(2 * time.Hour), true},
		{"pending past its window", "pending", 5, now.Add(-time.Hour), now.Add(-time.Minute), true},
		{"delivered", "delivered", 1, now.Add(-time.Hour), now.Add(time.Hour), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pool, store := outboundTestDB(t)
			pool.clock = newFakeClock(now)
			enc, _ := store.encryptSecret([]byte("s"))
			id := NewID()
			if _, err := pool.db.ExecContext(ctx,
				`INSERT INTO smeldr_outbound_jobs VALUES ($1,'ep','https://x.com',$2,'{}','ev',$3,$4,$5,$6,$7)`,
				id, enc, tc.attempts, tc.next, now.Add(-2*time.Hour), tc.expires, tc.status); err != nil {
				t.Fatal(err)
			}
			before := read(t, pool, id)
			err := pool.RetryDead(ctx, id)
			after := read(t, pool, id)
			due, ferr := pool.fetchDueJobs(ctx)
			if ferr != nil {
				t.Fatal(ferr)
			}
			if !tc.queued {
				wantTypedG(t, err, problemWebhookJobDelivered, map[string]string{"job": id, "status": "delivered"},
					"Conflict: webhook job "+id+" was already delivered, so it is not sent again; nothing changed")
				if !errors.Is(err, ErrConflict) {
					t.Errorf("errors.Is(err, ErrConflict) = false")
				}
				if after != before {
					t.Errorf("row changed: %+v, was %+v", after, before)
				}
				if len(due) != 0 {
					t.Errorf("a delivered job is due again: %+v", due)
				}
				return
			}
			if err != nil {
				t.Fatalf("RetryDead: %v", err)
			}
			if after.status != "pending" || after.attempts != 0 || !after.next.Equal(now) || !after.expires.Equal(now.Add(24*time.Hour)) {
				t.Errorf("after RetryDead: %+v", after)
			}
			if len(due) != 1 || due[0].ID != id {
				t.Errorf("due after RetryDead: %+v", due)
			}
		})
	}

	t.Run("unknown id", func(t *testing.T) {
		pool, _ := outboundTestDB(t)
		err := pool.RetryDead(ctx, "no-such-job")
		wantTypedG(t, err, problemWebhookJobNotFound, map[string]string{"job": "no-such-job"},
			"Not found: webhook job no-such-job does not exist")
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("errors.Is(err, ErrNotFound) = false")
		}
	})

	t.Run("a status retry does not know", func(t *testing.T) {
		pool, store := outboundTestDB(t)
		enc, _ := store.encryptSecret([]byte("s"))
		if _, err := pool.db.ExecContext(ctx,
			`INSERT INTO smeldr_outbound_jobs VALUES ('odd','ep','https://x.com',$1,'{}','ev',0,$2,$2,$2,'sending')`, enc, now); err != nil {
			t.Fatal(err)
		}
		err := pool.RetryDead(ctx, "odd")
		if !errors.Is(err, ErrInternal) || ProblemTypeOf(err) != "" {
			t.Errorf("err = %v (type %q); want an untyped ErrInternal", err, ProblemTypeOf(err))
		}
	})

	t.Run("the database fails", func(t *testing.T) {
		for _, match := range []string{"status IN ('dead', 'pending')", "SELECT status FROM smeldr_outbound_jobs"} {
			pool, _ := outboundTestDB(t)
			pool.db = failAddressDB{DB: pool.db, match: match}
			err := pool.RetryDead(ctx, "j1")
			if err == nil || ProblemTypeOf(err) != "" || !strings.Contains(err.Error(), "smeldr: webhook: retry job j1") {
				t.Errorf("%s fails: err = %v; want a wrapped, untyped error", match, err)
			}
		}
		pool, _ := outboundTestDB(t)
		pool.db = &rowsAffectedFailDB{DB: pool.db, failOn: "status IN ('dead', 'pending')"}
		if err := pool.RetryDead(ctx, "j1"); err == nil || !strings.Contains(err.Error(), "simulated rows affected error") {
			t.Errorf("RowsAffected fails: err = %v", err)
		}
	})
}

// TestHTTPDeliver_Success verifies header sending via httptest server.
func TestHTTPDeliver_Success(t *testing.T) {
	// Replace the SSRF-safe client with a plain one so the test server on
	// 127.0.0.1 is reachable. Restored after the test.
	orig := outboundClient
	outboundClient = &http.Client{Timeout: 5 * time.Second}
	defer func() { outboundClient = orig }()

	var gotHeaders http.Header
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header.Clone()
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	secret := []byte("test-secret")
	payload := []byte(`{"event":"post.published"}`)
	job := OutboundJob{
		ID:        "job-1",
		TargetURL: srv.URL,
		Payload:   payload,
		Event:     "post.published",
	}

	if err := httpDeliver(context.Background(), job, secret); err != nil {
		t.Fatalf("httpDeliver: %v", err)
	}
	if gotHeaders.Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type: got %q", gotHeaders.Get("Content-Type"))
	}
	if gotHeaders.Get("X-Smeldr-Event") != "post.published" {
		t.Errorf("X-Smeldr-Event: got %q", gotHeaders.Get("X-Smeldr-Event"))
	}
	if gotHeaders.Get("X-Smeldr-Delivery") != "job-1" {
		t.Errorf("X-Smeldr-Delivery: got %q", gotHeaders.Get("X-Smeldr-Delivery"))
	}
	if sig := gotHeaders.Get("X-Smeldr-Signature"); len(sig) < 8 || sig[:7] != "sha256=" {
		t.Errorf("X-Smeldr-Signature: got %q", sig)
	}
	if gotHeaders.Get("X-Smeldr-Timestamp") == "" {
		t.Error("X-Smeldr-Timestamp missing")
	}
	if string(gotBody) != string(payload) {
		t.Errorf("body: got %q want %q", gotBody, payload)
	}
}

// TestHTTPDeliver_Non2xx verifies that a 500 response returns webhookHTTPError.
func TestHTTPDeliver_Non2xx(t *testing.T) {
	orig := outboundClient
	outboundClient = &http.Client{Timeout: 5 * time.Second}
	defer func() { outboundClient = orig }()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	job := OutboundJob{ID: "j", TargetURL: srv.URL, Payload: []byte(`{}`)}
	err := httpDeliver(context.Background(), job, []byte("s"))
	if err == nil {
		t.Fatal("want error for 500 response, got nil")
	}
	httpErr, ok := err.(*webhookHTTPError)
	if !ok {
		t.Fatalf("expected *webhookHTTPError, got %T: %v", err, err)
	}
	if httpErr.statusCode != 500 {
		t.Errorf("status code: got %d want 500", httpErr.statusCode)
	}
}

// TestBuildWebhookPayloadJSON verifies that payload JSON is well-formed.
func TestBuildWebhookPayloadJSON(t *testing.T) {
	item := &testTitledPost{Node: Node{ID: "x", Slug: "my-post"}, Title: "My Post"}
	data, err := buildWebhookPayload("Article", item, AfterSchedule)
	if err != nil {
		t.Fatalf("buildWebhookPayload: %v", err)
	}
	var p WebhookEventPayload
	if err := json.Unmarshal(data, &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if p.Event != "article.scheduled" {
		t.Errorf("Event: got %q want article.scheduled", p.Event)
	}
}

func TestWorkerPool_DeliveryStats_empty(t *testing.T) {
	pool, _ := outboundTestDB(t)
	ctx := context.Background()

	total, success, failed, last, err := pool.DeliveryStats(ctx, "no-such-endpoint")
	if err != nil {
		t.Fatalf("DeliveryStats: %v", err)
	}
	if total != 0 || success != 0 || failed != 0 {
		t.Errorf("empty: got total=%d success=%d failed=%d, want all 0", total, success, failed)
	}
	if last != nil {
		t.Errorf("lastAttempt should be nil for endpoint with no logs, got %v", last)
	}
}

// TestWorkerPool_DeliveryStats_realAttempt is a regression test for the
// TIMESTAMPTZ scan fix: it exercises DeliveryStats's MAX(dl.attempted_at)
// nullable-string parse against a real delivery log written through the
// actual Enqueue/processJob path (not a hand-crafted string), proving the
// round trip works end-to-end rather than assuming it.
func TestWorkerPool_DeliveryStats_realAttempt(t *testing.T) {
	pool, store := outboundTestDB(t)
	ctx := context.Background()
	clk := newFakeClock(time.Now().UTC())
	pool.clock = clk
	pool.deliver = func(_ context.Context, _ OutboundJob, _ []byte) error { return nil }

	enc, _ := store.encryptSecret([]byte("secret"))
	now := clk.Now()
	job := OutboundJob{
		ID:          NewID(),
		EndpointID:  "ep-stats",
		TargetURL:   "https://example.com/hook",
		SecretEnc:   enc,
		Payload:     []byte(`{"event":"post.published"}`),
		Event:       "post.published",
		NextRetryAt: now,
		CreatedAt:   now,
		ExpiresAt:   now.Add(24 * time.Hour),
		Status:      "pending",
	}
	if err := pool.Enqueue(ctx, job); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	jobs, err := pool.fetchDueJobs(ctx)
	if err != nil {
		t.Fatalf("fetchDueJobs: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("want 1 due job, got %d", len(jobs))
	}
	rng := rand.New(rand.NewSource(1))
	if err := pool.processJob(ctx, jobs[0], rng); err != nil {
		t.Fatalf("processJob: %v", err)
	}

	total, success, failed, last, err := pool.DeliveryStats(ctx, "ep-stats")
	if err != nil {
		t.Fatalf("DeliveryStats: %v", err)
	}
	if total != 1 || success != 1 || failed != 0 {
		t.Errorf("got total=%d success=%d failed=%d, want 1/1/0", total, success, failed)
	}
	if last == nil {
		t.Fatal("lastAttempt: want a parsed timestamp, got nil")
	}
	if diff := last.Sub(now); diff < -time.Minute || diff > time.Minute {
		t.Errorf("lastAttempt: got %v, want within a minute of %v", last, now)
	}
}

func TestNewWorkerPool_zeroWorkers_defaultsTen(t *testing.T) {
	store := NewWebhookStore(nil, []byte("k"))
	pool := newWorkerPool(nil, store, realClock{}, 0)
	if pool.workers != 10 {
		t.Errorf("workers: got %d want 10 when 0 passed", pool.workers)
	}
}

func TestSSRFProtection(t *testing.T) {
	dial := ssrfSafeDialContext()
	ctx := context.Background()

	blocked := []string{
		"127.0.0.1",
		"10.0.0.1",
		"192.168.1.1",
		"169.254.169.254",
		"::1",
		"fe80::1",
		"100.64.0.1",
		"fc00::1",
	}
	for _, ip := range blocked {
		addr := net.JoinHostPort(ip, "443")
		_, err := dial(ctx, "tcp", addr)
		if err == nil {
			t.Errorf("ssrfSafeDialContext: expected error for %s, got nil", ip)
		}
	}
}

func TestEnqueueHTTPSValidation(t *testing.T) {
	pool, _ := outboundTestDB(t)
	ctx := context.Background()

	job := OutboundJob{
		ID:          NewID(),
		EndpointID:  "ep1",
		TargetURL:   "http://example.com/hook",
		SecretEnc:   "s",
		Payload:     []byte("{}"),
		Event:       "test",
		Attempts:    0,
		NextRetryAt: time.Now().UTC(),
		CreatedAt:   time.Now().UTC(),
		ExpiresAt:   time.Now().UTC().Add(time.Hour),
		Status:      "pending",
	}
	if err := pool.Enqueue(ctx, job); err == nil {
		t.Error("Enqueue: expected error for http:// target, got nil")
	}

	job.TargetURL = "https://example.com/hook"
	if err := pool.Enqueue(ctx, job); err != nil {
		t.Errorf("Enqueue: unexpected error for https:// target: %v", err)
	}
}

// enqueueDueJob stores one pending job due at the pool clock's now and
// returns it as a worker would fetch it.
func enqueueDueJob(t *testing.T, pool *workerPool, store *WebhookStore) OutboundJob {
	t.Helper()
	enc, _ := store.encryptSecret([]byte("secret"))
	now := pool.clock.Now()
	job := OutboundJob{
		ID: NewID(), EndpointID: "ep-claim", TargetURL: "https://example.com/hook", SecretEnc: enc,
		Payload: []byte(`{}`), Event: "post.created", NextRetryAt: now, CreatedAt: now,
		ExpiresAt: now.Add(24 * time.Hour), Status: "pending",
	}
	if err := pool.Enqueue(context.Background(), job); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	return job
}

// jobRow reads a job's attempts and delivery log count.
func jobRow(t *testing.T, pool *workerPool, id string) (attempts, logs int) {
	t.Helper()
	ctx := context.Background()
	if err := pool.db.QueryRowContext(ctx, `SELECT attempts FROM smeldr_outbound_jobs WHERE id = $1`, id).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if err := pool.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM smeldr_delivery_logs WHERE job_id = $1`, id).Scan(&logs); err != nil {
		t.Fatal(err)
	}
	return attempts, logs
}

// TestWorkerPool_OneWorkerPerJob: ten workers and one due job give one
// delivery, one delivery log and one attempt, for a receiver that succeeds
// and one that fails (T1146). Before the claim, every worker delivered it.
func TestWorkerPool_OneWorkerPerJob(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{{"success", nil}, {"failure", &webhookHTTPError{statusCode: 503}}} {
		t.Run(tc.name, func(t *testing.T) {
			pool, store := outboundTestDB(t)
			pool.workers = 10
			var delivered int32
			pool.deliver = func(context.Context, OutboundJob, []byte) error {
				atomic.AddInt32(&delivered, 1)
				time.Sleep(50 * time.Millisecond) // every worker has fetched by now
				return tc.err
			}
			job := enqueueDueJob(t, pool, store)
			pool.Start(context.Background())
			deadline := time.Now().Add(2 * time.Second)
			for atomic.LoadInt32(&delivered) == 0 && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
			time.Sleep(300 * time.Millisecond) // room for any duplicate
			pool.Stop()
			if n := atomic.LoadInt32(&delivered); n != 1 {
				t.Errorf("delivered %d times, want 1", n)
			}
			if attempts, logs := jobRow(t, pool, job.ID); attempts != 1 || logs != 1 {
				t.Errorf("attempts %d, logs %d, want 1 and 1", attempts, logs)
			}
		})
	}
}

// TestWorkerPool_ClaimOnce: of two claims of the same fetched job, the first
// gets it and the second gets nothing.
func TestWorkerPool_ClaimOnce(t *testing.T) {
	pool, store := outboundTestDB(t)
	pool.clock = newFakeClock(time.Now().UTC())
	job := enqueueDueJob(t, pool, store)
	ctx := context.Background()
	if ok, err := pool.claimJob(ctx, job); err != nil || !ok {
		t.Fatalf("first claim: %v %v", ok, err)
	}
	if ok, err := pool.claimJob(ctx, job); err != nil || ok {
		t.Fatalf("second claim: %v %v, want false", ok, err)
	}
}

// TestWorkerPool_ClaimLeaseExpires: a claimed job whose worker never finishes
// is not due during the lease and is due again after it, attempts unchanged.
func TestWorkerPool_ClaimLeaseExpires(t *testing.T) {
	pool, store := outboundTestDB(t)
	clk := newFakeClock(time.Now().UTC())
	pool.clock = clk
	job := enqueueDueJob(t, pool, store)
	ctx := context.Background()
	if ok, err := pool.claimJob(ctx, job); err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	if due, err := pool.fetchDueJobs(ctx); err != nil || len(due) != 0 {
		t.Fatalf("during the lease: %d due, %v", len(due), err)
	}
	clk.Advance(outboundLease + time.Second)
	due, err := pool.fetchDueJobs(ctx)
	if err != nil || len(due) != 1 || due[0].Attempts != 0 {
		t.Fatalf("after the lease: %v %v", due, err)
	}
}

// TestWorkerPool_StaleSnapshotNotClaimed: a worker whose fetch is older than
// an attempt cannot claim the job when it is due again; the next fetch, with
// the current attempts, can.
func TestWorkerPool_StaleSnapshotNotClaimed(t *testing.T) {
	pool, store := outboundTestDB(t)
	clk := newFakeClock(time.Now().UTC())
	pool.clock = clk
	pool.deliver = func(context.Context, OutboundJob, []byte) error { return &webhookHTTPError{statusCode: 503} }
	stale := enqueueDueJob(t, pool, store)
	ctx := context.Background()
	if err := pool.processJob(ctx, stale, rand.New(rand.NewSource(1))); err != nil {
		t.Fatalf("first attempt: %v", err)
	}
	clk.Advance(time.Hour) // past the retry delay: due again
	if ok, err := pool.claimJob(ctx, stale); err != nil || ok {
		t.Fatalf("stale claim: %v %v, want false", ok, err)
	}
	due, err := pool.fetchDueJobs(ctx)
	if err != nil || len(due) != 1 || due[0].Attempts != 1 {
		t.Fatalf("fresh fetch: %v %v", due, err)
	}
	if ok, err := pool.claimJob(ctx, due[0]); err != nil || !ok {
		t.Fatalf("fresh claim: %v %v", ok, err)
	}
}

// TestWorkerPool_CircuitOpenDoesNotClaim: with the endpoint's circuit open,
// the job is not claimed and stays due.
func TestWorkerPool_CircuitOpenDoesNotClaim(t *testing.T) {
	pool, store := outboundTestDB(t)
	pool.clock = newFakeClock(time.Now().UTC())
	job := enqueueDueJob(t, pool, store)
	for i := 0; i < 5; i++ {
		pool.recordCircuitResult(job.EndpointID, false)
	}
	ctx := context.Background()
	if err := pool.processJob(ctx, job, rand.New(rand.NewSource(1))); err != nil {
		t.Fatal(err)
	}
	if due, err := pool.fetchDueJobs(ctx); err != nil || len(due) != 1 {
		t.Fatalf("after an open circuit: %d due, %v", len(due), err)
	}
}

// TestWorkerPool_ClaimError: a claim that cannot run is an error, and nothing
// is delivered.
func TestWorkerPool_ClaimError(t *testing.T) {
	pool, store := outboundTestDB(t)
	job := enqueueDueJob(t, pool, store)
	var delivered int32
	pool.deliver = func(context.Context, OutboundJob, []byte) error { atomic.AddInt32(&delivered, 1); return nil }
	if _, err := pool.db.ExecContext(context.Background(), `DROP TABLE smeldr_outbound_jobs`); err != nil {
		t.Fatal(err)
	}
	if err := pool.processJob(context.Background(), job, rand.New(rand.NewSource(1))); err == nil {
		t.Fatal("processJob with no jobs table returned nil")
	}
	if atomic.LoadInt32(&delivered) != 0 {
		t.Error("a job that could not be claimed was delivered")
	}
}
