package smeldr

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// TestWorkerPool_StopBetweenJobs: a worker that sees stop between two fetched
// jobs returns without delivering the second, every run (T1160).
func TestWorkerPool_StopBetweenJobs(t *testing.T) {
	pool, store := outboundTestDB(t)
	pool.workers = 1
	started := make(chan struct{}, 2)
	var delivered int32
	pool.deliver = func(context.Context, OutboundJob, []byte) error {
		atomic.AddInt32(&delivered, 1)
		started <- struct{}{}
		<-pool.stop // hold the first job until Stop has closed stop
		return nil
	}
	enqueueDueJob(t, pool, store)
	enqueueDueJob(t, pool, store)
	pool.Start(context.Background())
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("no delivery started")
	}
	pool.Stop()
	if n := atomic.LoadInt32(&delivered); n != 1 {
		t.Errorf("delivered %d jobs, want 1: the worker went on after stop", n)
	}
}

// TestFetchDueJobs_ScanError: a due row that does not scan is an error from
// fetchDueJobs, every run, not only when a teardown races a fetch (T1160).
func TestFetchDueJobs_ScanError(t *testing.T) {
	pool, store := outboundTestDB(t)
	job := enqueueDueJob(t, pool, store)
	if _, err := pool.db.ExecContext(context.Background(),
		`UPDATE smeldr_outbound_jobs SET attempts = 'not a number' WHERE id = $1`, job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.fetchDueJobs(context.Background()); err == nil {
		t.Error("fetchDueJobs scanned a row whose attempts is not a number")
	}
}

// TestWorkerPool_StoppedWorkerReturnsAtOnce: a worker whose pool is already
// stopped returns at the top of its loop without fetching, every run
// (T1160).
func TestWorkerPool_StoppedWorkerReturnsAtOnce(t *testing.T) {
	pool, _ := outboundTestDB(t)
	close(pool.stop)
	done := make(chan struct{})
	go func() {
		pool.runWorker(context.Background())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a stopped worker did not return")
	}
}

// TestWorkerPool_CircuitBoundWithWorkers keeps circuitAllows's documented
// bound under test: with n workers and a failing endpoint, deliveries reach
// at least circuitOpenThreshold and at most circuitOpenThreshold+n-1 before
// the circuit skips the endpoint (T1170). No exact count, so no flake.
func TestWorkerPool_CircuitBoundWithWorkers(t *testing.T) {
	pool, store := outboundTestDB(t)
	clock := newFakeClock(time.Now().UTC()) // never advanced: the circuit stays open
	pool.clock = clock
	pool.workers = 2
	var n int32
	pool.deliver = func(context.Context, OutboundJob, []byte) error {
		atomic.AddInt32(&n, 1)
		return &webhookHTTPError{statusCode: 503}
	}
	for i := 0; i < 2*circuitOpenThreshold; i++ {
		enqueueDueJob(t, pool, store)
	}
	pool.Start(context.Background())
	deadline := time.Now().Add(5 * time.Second)
	for atomic.LoadInt32(&n) < circuitOpenThreshold && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond) // room for any delivery that was in flight
	pool.Stop()
	got, max := int(atomic.LoadInt32(&n)), circuitOpenThreshold+pool.workers-1
	if got < circuitOpenThreshold || got > max {
		t.Errorf("deliveries = %d; want %d to %d with %d workers", got, circuitOpenThreshold, max, pool.workers)
	}
}
