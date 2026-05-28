package smeldr

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/rand"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// Clock is a minimal time abstraction used by [workerPool]. Tests substitute
// a fake clock to control timing without real sleeps.
type Clock interface {
	// Now returns the current time.
	Now() time.Time
	// After waits for the duration to elapse and then sends the current time
	// on the returned channel (equivalent to time.After).
	After(d time.Duration) <-chan time.Time
}

// realClock is the production Clock implementation backed by the standard
// time package.
type realClock struct{}

func (realClock) Now() time.Time                         { return time.Now() }
func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// OutboundDelivery is implemented by any engine that can queue outbound HTTP
// requests for retry-backed delivery. It is the shared interface for
// forge-social, audit trail, and other modules that need durable outbound HTTP
// without coupling to [WebhookStore] directly.
//
// The unexported [workerPool] satisfies OutboundDelivery.
// [App.WebhookPool] returns the pool as a [WebhookJobQueue] for forge-mcp.
type OutboundDelivery interface {
	Enqueue(ctx context.Context, job OutboundJob) error
}

// OutboundJob is a persisted outbound delivery attempt. One job is created
// per endpoint per triggering event. The worker pool polls for due jobs and
// retries them with exponential backoff until success or expiry.
//
// Database DDL (must be executed before [App.Webhooks] is called):
//
//	CREATE TABLE forge_outbound_jobs (
//	    id            TEXT    PRIMARY KEY,
//	    endpoint_id   TEXT    NOT NULL,
//	    target_url    TEXT    NOT NULL,
//	    secret_enc    TEXT    NOT NULL,
//	    payload       BYTEA   NOT NULL,
//	    event         TEXT    NOT NULL,
//	    attempts      INTEGER NOT NULL DEFAULT 0,
//	    next_retry_at TIMESTAMPTZ NOT NULL,
//	    created_at    TIMESTAMPTZ NOT NULL,
//	    expires_at    TIMESTAMPTZ NOT NULL,
//	    status        TEXT    NOT NULL DEFAULT 'pending'
//	);
type OutboundJob struct {
	ID          string
	EndpointID  string
	TargetURL   string
	SecretEnc   string
	Payload     []byte
	Event       string
	Attempts    int
	NextRetryAt time.Time
	CreatedAt   time.Time
	ExpiresAt   time.Time
	Status      string
}

// DeliveryLog is a record of one delivery attempt for an [OutboundJob].
//
// Database DDL (must be executed before [App.Webhooks] is called):
//
//	CREATE TABLE forge_delivery_logs (
//	    id           TEXT    PRIMARY KEY,
//	    job_id       TEXT    NOT NULL,
//	    attempted_at TIMESTAMPTZ NOT NULL,
//	    status_code  INTEGER NOT NULL DEFAULT 0,
//	    duration_ms  INTEGER NOT NULL DEFAULT 0,
//	    error        TEXT    NOT NULL DEFAULT ''
//	);
type DeliveryLog struct {
	ID          string
	JobID       string
	AttemptedAt time.Time
	StatusCode  int
	DurationMS  int64
	Error       string
}

// circuitState tracks failure counts and open timing for one endpoint.
type circuitState struct {
	failures  int
	openUntil time.Time
}

const (
	circuitOpenThreshold = 5
	circuitOpenDuration  = 5 * time.Minute
)

// workerPool polls forge_outbound_jobs for due deliveries, signs payloads with
// HMAC-SHA256, and POSTs them to webhook endpoints. It enforces per-endpoint
// circuit breakers and writes delivery logs.
type workerPool struct {
	db           DB
	webhookStore *WebhookStore
	clock        Clock
	deliver      func(context.Context, OutboundJob, []byte) error
	workers      int

	stop chan struct{}
	wg   sync.WaitGroup

	mu       sync.Mutex
	circuits map[string]*circuitState
}

// newWorkerPool creates a [workerPool] wired to db and store. workers controls
// concurrency; pass 0 to use 10. The production HTTP deliverer is used unless
// overridden in tests via p.deliver.
func newWorkerPool(db DB, store *WebhookStore, clock Clock, workers int) *workerPool {
	if workers <= 0 {
		workers = 10
	}
	p := &workerPool{
		db:           db,
		webhookStore: store,
		clock:        clock,
		workers:      workers,
		stop:         make(chan struct{}),
		circuits:     make(map[string]*circuitState),
	}
	p.deliver = func(ctx context.Context, job OutboundJob, secret []byte) error {
		return httpDeliver(ctx, job, secret)
	}
	return p
}

// Start launches p.workers goroutines that poll for due jobs. Call Stop to
// drain them.
func (p *workerPool) Start(ctx context.Context) {
	for i := 0; i < p.workers; i++ {
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			p.runWorker(ctx)
		}()
	}
}

// Stop signals all workers to stop and waits for them to drain.
func (p *workerPool) Stop() {
	close(p.stop)
	p.wg.Wait()
}

// Enqueue persists job into forge_outbound_jobs so the worker pool will pick
// it up. Callers should set job.ID (via NewID) before calling.
func (p *workerPool) Enqueue(ctx context.Context, job OutboundJob) error {
	_, err := p.db.ExecContext(ctx, `
		INSERT INTO forge_outbound_jobs
			(id, endpoint_id, target_url, secret_enc, payload, event, attempts, next_retry_at, created_at, expires_at, status)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		job.ID, job.EndpointID, job.TargetURL, job.SecretEnc,
		job.Payload, job.Event, job.Attempts,
		job.NextRetryAt, job.CreatedAt, job.ExpiresAt, job.Status,
	)
	return err
}

// runWorker is the main loop for one worker goroutine.
func (p *workerPool) runWorker(ctx context.Context) {
	rng := rand.New(rand.NewSource(p.clock.Now().UnixNano()))
	for {
		select {
		case <-p.stop:
			return
		default:
		}

		jobs, err := p.fetchDueJobs(ctx)
		if err != nil || len(jobs) == 0 {
			select {
			case <-p.stop:
				return
			case <-p.clock.After(2 * time.Second):
			}
			continue
		}

		for _, job := range jobs {
			select {
			case <-p.stop:
				return
			default:
			}
			if err := p.processJob(ctx, job, rng); err != nil {
				// Errors are logged inside processJob; continue to next job.
			}
		}
	}
}

// fetchDueJobs selects up to 10 pending jobs whose next_retry_at is in the
// past and which have not yet expired.
func (p *workerPool) fetchDueJobs(ctx context.Context) ([]OutboundJob, error) {
	now := p.clock.Now()
	rows, err := p.db.QueryContext(ctx, `
		SELECT id, endpoint_id, target_url, secret_enc, payload, event, attempts, next_retry_at, created_at, expires_at, status
		FROM forge_outbound_jobs
		WHERE status = 'pending'
		  AND next_retry_at <= $1
		  AND expires_at > $1
		ORDER BY next_retry_at
		LIMIT 10`,
		now,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OutboundJob
	for rows.Next() {
		var j OutboundJob
		if err := rows.Scan(
			&j.ID, &j.EndpointID, &j.TargetURL, &j.SecretEnc,
			&j.Payload, &j.Event, &j.Attempts,
			&j.NextRetryAt, &j.CreatedAt, &j.ExpiresAt, &j.Status,
		); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// processJob decrypts the secret, checks the circuit breaker, delivers the
// payload, logs the result, and updates the job status.
func (p *workerPool) processJob(ctx context.Context, job OutboundJob, rng *rand.Rand) error {
	if !p.circuitAllows(job.EndpointID) {
		return nil // circuit open — skip silently; job retains pending status
	}

	secret, err := p.webhookStore.decryptSecret(job.SecretEnc)
	if err != nil {
		// Unrecoverable — dead-letter immediately.
		_ = p.moveToDeadLetter(ctx, job.ID)
		_ = p.logDelivery(ctx, DeliveryLog{
			ID:          NewID(),
			JobID:       job.ID,
			AttemptedAt: p.clock.Now(),
			Error:       "secret decrypt failed: " + err.Error(),
		})
		return err
	}

	start := p.clock.Now()
	deliverErr := p.deliver(ctx, job, secret)
	durationMS := p.clock.Now().Sub(start).Milliseconds()

	job.Attempts++
	dl := DeliveryLog{
		ID:          NewID(),
		JobID:       job.ID,
		AttemptedAt: start,
		DurationMS:  durationMS,
	}

	if deliverErr != nil {
		if e, ok := deliverErr.(*webhookHTTPError); ok {
			dl.StatusCode = e.statusCode
		}
		dl.Error = deliverErr.Error()
		p.recordCircuitResult(job.EndpointID, false)

		const maxAttempts = 7
		if job.Attempts >= maxAttempts {
			_ = p.moveToDeadLetter(ctx, job.ID)
		} else {
			delay := backoffDelay(job.Attempts, rng)
			_ = p.updateJobAfterAttempt(ctx, job, false, delay)
		}
	} else {
		dl.StatusCode = 200
		p.recordCircuitResult(job.EndpointID, true)
		_ = p.updateJobAfterAttempt(ctx, job, true, 0)
	}
	return p.logDelivery(ctx, dl)
}

// webhookHTTPError carries the HTTP status code from a non-2xx delivery response.
type webhookHTTPError struct {
	statusCode int
}

func (e *webhookHTTPError) Error() string {
	return fmt.Sprintf("webhook: upstream returned %d", e.statusCode)
}

// logDelivery inserts a DeliveryLog row.
func (p *workerPool) logDelivery(ctx context.Context, dl DeliveryLog) error {
	_, err := p.db.ExecContext(ctx, `
		INSERT INTO forge_delivery_logs (id, job_id, attempted_at, status_code, duration_ms, error)
		VALUES ($1,$2,$3,$4,$5,$6)`,
		dl.ID, dl.JobID, dl.AttemptedAt, dl.StatusCode, dl.DurationMS, dl.Error,
	)
	return err
}

// updateJobAfterAttempt updates attempts, next_retry_at, and status.
func (p *workerPool) updateJobAfterAttempt(ctx context.Context, job OutboundJob, success bool, delay time.Duration) error {
	status := "pending"
	if success {
		status = "delivered"
	}
	nextRetry := p.clock.Now().Add(delay)
	_, err := p.db.ExecContext(ctx, `
		UPDATE forge_outbound_jobs
		SET attempts = $1, next_retry_at = $2, status = $3
		WHERE id = $4`,
		job.Attempts, nextRetry, status, job.ID,
	)
	return err
}

// moveToDeadLetter marks a job as dead so it is no longer retried.
func (p *workerPool) moveToDeadLetter(ctx context.Context, jobID string) error {
	_, err := p.db.ExecContext(ctx,
		`UPDATE forge_outbound_jobs SET status = 'dead' WHERE id = $1`, jobID,
	)
	return err
}

// RetryDead resets a dead job to pending so the worker pool will retry it.
func (p *workerPool) RetryDead(ctx context.Context, jobID string) error {
	_, err := p.db.ExecContext(ctx, `
		UPDATE forge_outbound_jobs
		SET status = 'pending', attempts = 0, next_retry_at = $1,
		    expires_at = $2
		WHERE id = $3`,
		p.clock.Now(), p.clock.Now().Add(24*time.Hour), jobID,
	)
	return err
}

// ListDeliveryLogs returns all delivery logs for a job, newest first.
func (p *workerPool) ListDeliveryLogs(ctx context.Context, jobID string) ([]DeliveryLog, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT id, job_id, attempted_at, status_code, duration_ms, error
		FROM forge_delivery_logs
		WHERE job_id = $1
		ORDER BY attempted_at DESC`,
		jobID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DeliveryLog
	for rows.Next() {
		var dl DeliveryLog
		if err := rows.Scan(&dl.ID, &dl.JobID, &dl.AttemptedAt, &dl.StatusCode, &dl.DurationMS, &dl.Error); err != nil {
			return nil, err
		}
		out = append(out, dl)
	}
	return out, rows.Err()
}

// ListJobsForEndpoint returns all outbound jobs for an endpoint, newest first.
func (p *workerPool) ListJobsForEndpoint(ctx context.Context, endpointID string) ([]OutboundJob, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT id, endpoint_id, target_url, secret_enc, payload, event, attempts, next_retry_at, created_at, expires_at, status
		FROM forge_outbound_jobs
		WHERE endpoint_id = $1
		ORDER BY created_at DESC`,
		endpointID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OutboundJob
	for rows.Next() {
		var j OutboundJob
		if err := rows.Scan(
			&j.ID, &j.EndpointID, &j.TargetURL, &j.SecretEnc,
			&j.Payload, &j.Event, &j.Attempts,
			&j.NextRetryAt, &j.CreatedAt, &j.ExpiresAt, &j.Status,
		); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// DeliveryStats returns aggregate delivery statistics for an endpoint:
// total jobs attempted, successful deliveries, failed deliveries, and the
// timestamp of the most recent attempt. Returns nil for lastAttempt when no
// delivery logs exist for the endpoint's jobs.
func (p *workerPool) DeliveryStats(ctx context.Context, endpointID string) (total, success, failed int, lastAttempt *time.Time, err error) {
	row := p.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*),
			COALESCE(SUM(CASE WHEN dl.status_code >= 200 AND dl.status_code < 300 THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN dl.status_code = 0 OR dl.status_code >= 300 THEN 1 ELSE 0 END), 0),
			MAX(dl.attempted_at)
		FROM forge_outbound_jobs j
		LEFT JOIN forge_delivery_logs dl ON dl.job_id = j.id
		WHERE j.endpoint_id = $1`,
		endpointID,
	)
	var maxAt *time.Time
	if err = row.Scan(&total, &success, &failed, &maxAt); err != nil {
		return 0, 0, 0, nil, err
	}
	return total, success, failed, maxAt, nil
}

// circuitAllows returns true when the circuit for endpointID permits a delivery
// attempt. A circuit is opened after circuitOpenThreshold consecutive failures
// and transitions to half-open after circuitOpenDuration.
func (p *workerPool) circuitAllows(endpointID string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	s, ok := p.circuits[endpointID]
	if !ok {
		return true
	}
	now := p.clock.Now()
	if now.Before(s.openUntil) {
		return false // Open — still blocking
	}
	// Past openUntil → half-open; allow one probe
	return true
}

// recordCircuitResult updates the circuit breaker state for endpointID based
// on whether the last delivery attempt succeeded.
func (p *workerPool) recordCircuitResult(endpointID string, success bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	s, ok := p.circuits[endpointID]
	if !ok {
		s = &circuitState{}
		p.circuits[endpointID] = s
	}
	if success {
		s.failures = 0
		s.openUntil = time.Time{}
	} else {
		s.failures++
		if s.failures >= circuitOpenThreshold {
			s.openUntil = p.clock.Now().Add(circuitOpenDuration)
		}
	}
}

// backoffDelay returns the retry delay for attempt number attempt (1-based)
// with ±20% random jitter. The delay grows exponentially (4^attempt seconds)
// and is capped at 1 hour.
func backoffDelay(attempt int, rng *rand.Rand) time.Duration {
	const cap = time.Hour
	if attempt <= 0 {
		return time.Second
	}
	base := time.Second
	for i := 0; i < attempt && base < cap; i++ {
		base *= 4
		if base > cap {
			base = cap
		}
	}
	// ±20% jitter
	jitter := float64(base) * 0.4 * (rng.Float64() - 0.5)
	d := time.Duration(float64(base) + jitter)
	if d < time.Second {
		d = time.Second
	}
	if d > cap {
		d = cap
	}
	return d
}

// signPayload computes the HMAC-SHA256 signature for a webhook payload.
// The signed string is "<timestamp>.<body>" where timestamp is Unix seconds.
// Returns "sha256=<hex>" — the value of the X-Forge-Signature header.
func signPayload(secret []byte, timestamp int64, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(strconv.FormatInt(timestamp, 10)))
	mac.Write([]byte("."))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// outboundClient is the HTTP client used for all webhook deliveries.
// It enforces a 30-second timeout and blocks all redirects: webhook targets
// must not redirect, and following a redirect to a private IP would bypass
// the SSRF validation performed at endpoint creation time.
var outboundClient = &http.Client{
	Timeout: 30 * time.Second,
	CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// httpDeliver performs a single HTTP POST delivery for job. It sets all
// required X-Forge-* headers, signs the payload, and returns a
// *webhookHTTPError for non-2xx responses.
func httpDeliver(ctx context.Context, job OutboundJob, secret []byte) error {
	ts := time.Now().Unix()
	sig := signPayload(secret, ts, job.Payload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, job.TargetURL, bytes.NewReader(job.Payload))
	if err != nil {
		return fmt.Errorf("forge: webhook: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forge-Signature", sig)
	req.Header.Set("X-Forge-Timestamp", strconv.FormatInt(ts, 10))
	req.Header.Set("X-Forge-Event", job.Event)
	req.Header.Set("X-Forge-Delivery", job.ID)

	resp, err := outboundClient.Do(req)
	if err != nil {
		return fmt.Errorf("forge: webhook: http: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &webhookHTTPError{statusCode: resp.StatusCode}
	}
	return nil
}
