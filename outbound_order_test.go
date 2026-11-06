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
