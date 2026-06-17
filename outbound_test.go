package smeldr

import (
	"context"
	"encoding/json"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/http/httptest"
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
			attempts INTEGER NOT NULL DEFAULT 0, next_retry_at DATETIME NOT NULL,
			created_at DATETIME NOT NULL, expires_at DATETIME NOT NULL,
			status TEXT NOT NULL DEFAULT 'pending'
		)`)
	if err != nil {
		t.Fatalf("create smeldr_outbound_jobs: %v", err)
	}
	_, err = db.ExecContext(ctx, `
		CREATE TABLE smeldr_delivery_logs (
			id TEXT PRIMARY KEY, job_id TEXT NOT NULL, attempted_at DATETIME NOT NULL,
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
	clk := newFakeClock(time.Now())
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
	clk := newFakeClock(time.Now())
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
	now := time.Now()
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
	clk := newFakeClock(time.Now())
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

// TestWorkerPool_RetryDead verifies that RetryDead resets a dead job to pending.
func TestWorkerPool_RetryDead(t *testing.T) {
	pool, store := outboundTestDB(t)
	ctx := context.Background()

	enc, _ := store.encryptSecret([]byte("s"))
	now := time.Now()
	jobID := NewID()
	_, _ = pool.db.ExecContext(ctx,
		`INSERT INTO smeldr_outbound_jobs VALUES ($1,'ep','https://x.com',$2,'{}','ev',7,$3,$3,$4,'dead')`,
		jobID, enc, now, now.Add(time.Hour),
	)

	if err := pool.RetryDead(ctx, jobID); err != nil {
		t.Fatalf("RetryDead: %v", err)
	}
	rows, _ := pool.db.QueryContext(ctx, `SELECT status, attempts FROM smeldr_outbound_jobs WHERE id = $1`, jobID)
	defer rows.Close()
	var status string
	var attempts int
	rows.Next()
	rows.Scan(&status, &attempts)
	if status != "pending" {
		t.Errorf("after RetryDead: status %q want pending", status)
	}
	if attempts != 0 {
		t.Errorf("after RetryDead: attempts %d want 0", attempts)
	}
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
	// Legacy X-Forge-* headers — must still be present during deprecation window (T86).
	if gotHeaders.Get("X-Forge-Event") != "post.published" {
		t.Errorf("X-Forge-Event: got %q", gotHeaders.Get("X-Forge-Event"))
	}
	if gotHeaders.Get("X-Forge-Delivery") != "job-1" {
		t.Errorf("X-Forge-Delivery: got %q", gotHeaders.Get("X-Forge-Delivery"))
	}
	if sig := gotHeaders.Get("X-Forge-Signature"); len(sig) < 8 || sig[:7] != "sha256=" {
		t.Errorf("X-Forge-Signature: got %q", sig)
	}
	ts := gotHeaders.Get("X-Forge-Timestamp")
	if ts == "" {
		t.Error("X-Forge-Timestamp missing")
	}
	// New X-Smeldr-* headers — preferred, same values as X-Forge-* (T86).
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
	if gotHeaders.Get("X-Smeldr-Signature") != gotHeaders.Get("X-Forge-Signature") {
		t.Error("X-Smeldr-Signature and X-Forge-Signature must be identical")
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
		NextRetryAt: time.Now(),
		CreatedAt:   time.Now(),
		ExpiresAt:   time.Now().Add(time.Hour),
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

