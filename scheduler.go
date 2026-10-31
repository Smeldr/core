package smeldr

import (
	"context"
	"log/slog"
	"time"
)

// schedulableModule is the unexported interface satisfied by every [Module[T]].
// The [Scheduler] calls processScheduled on each registered module once per tick.
type schedulableModule interface {
	processScheduled(ctx Context, now time.Time) (published int, next *time.Time, err error)
}

// Scheduler drives the Scheduled→Published transition for all content modules
// registered with [App.Content]. A single background goroutine runs with an
// adaptive timer: after each tick the timer is reset to fire at the soonest
// remaining ScheduledAt across all modules, but never more than
// [schedulerMaxWait] (60 seconds) away, so an item written by another process
// is picked up within a minute. A module that schedules or reschedules an item
// wakes it at once (T1003), so an item due before the armed timer is published
// at its own time.
//
// The Scheduler is created and started by [App.Run] and stopped as part of
// graceful shutdown. Applications do not create Schedulers directly.
type Scheduler struct {
	modules []schedulableModule
	bgCtx   Context
	done    chan struct{}
	// wake, when set, makes run tick at once and re-arm its timer (T1003).
	wake <-chan struct{}
	// maxWait is [schedulerMaxWait] as it was when the Scheduler was made.
	maxWait time.Duration
}

// newScheduler returns a Scheduler that will process scheduled items for each
// of the provided modules, using bgCtx for storage calls and signal dispatch.
func newScheduler(modules []schedulableModule, bgCtx Context) *Scheduler {
	return &Scheduler{
		modules: modules,
		bgCtx:   bgCtx,
		done:    make(chan struct{}),
		maxWait: schedulerMaxWait,
	}
}

// Start spawns the scheduler goroutine. The goroutine exits when ctx is
// cancelled. Call [Scheduler.Wait] after cancellation to block until the
// goroutine has fully exited.
func (s *Scheduler) Start(ctx context.Context) {
	go s.run(ctx)
}

// Wait blocks until the goroutine started by [Scheduler.Start] has exited.
// It should be called after cancelling the context passed to Start to ensure
// clean shutdown.
func (s *Scheduler) Wait() {
	<-s.done
}

// tick processes all overdue Scheduled items across every registered module
// and returns the soonest remaining ScheduledAt across all modules (nil if no
// scheduled items remain after the pass).
func (s *Scheduler) tick() *time.Time {
	now := time.Now().UTC()
	var next *time.Time
	for _, m := range s.modules {
		_, n, err := m.processScheduled(s.bgCtx, now)
		if err != nil {
			slog.Warn("smeldr: scheduler tick error", "err", err)
		}
		if n != nil && (next == nil || n.Before(*next)) {
			next = n
		}
	}
	return next
}

// run is the body of the scheduler goroutine.
func (s *Scheduler) run(ctx context.Context) {
	defer close(s.done)

	// Process any items that became due while the server was offline.
	next := s.tick()

	timer := time.NewTimer(nextDurMax(next, s.maxWait))
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			next = s.tick()
			timer.Reset(nextDurMax(next, s.maxWait))
		case <-s.wake:
			// An item was scheduled or rescheduled: tick now, so one due
			// already is published, and re-arm from the new soonest time.
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			next = s.tick()
			timer.Reset(nextDurMax(next, s.maxWait))
		}
	}
}

// schedulerMaxWait is the longest the Scheduler waits between ticks: it covers
// items written outside this process (another instance, a direct write), which
// send no wake (T1003). A variable so tests can shorten it.
var schedulerMaxWait = 60 * time.Second

// nextDur returns the duration to wait before the next scheduler tick: until
// next, but at most [schedulerMaxWait], and [schedulerMaxWait] when next is nil
// (no scheduled items remain). The returned duration is never less than 1
// millisecond.
func nextDur(next *time.Time) time.Duration { return nextDurMax(next, schedulerMaxWait) }

// nextDurMax is [nextDur] with the cap given: the Scheduler passes the cap it
// read when it was made, so its goroutine never reads schedulerMaxWait.
func nextDurMax(next *time.Time, maxWait time.Duration) time.Duration {
	if next == nil {
		return maxWait
	}
	d := time.Until(*next)
	if d < time.Millisecond {
		return time.Millisecond
	}
	if d > maxWait {
		return maxWait
	}
	return d
}
