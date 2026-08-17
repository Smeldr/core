package smeldr

import (
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// eventStreamSubscriberBuffer is the per-subscriber channel capacity used by
// [eventBroadcaster.subscribe].
const eventStreamSubscriberBuffer = 32

// eventBroadcaster fans a broadcast payload out to every currently-connected
// subscriber channel. In-memory only — no persistence, no replay; a
// subscriber that connects after a broadcast simply never sees it
// (at-most-once, per design/agent-event-signaling.md's own lean).
type eventBroadcaster struct {
	mu   sync.Mutex
	subs map[chan []byte]struct{}
}

// newEventBroadcaster returns an empty [eventBroadcaster].
func newEventBroadcaster() *eventBroadcaster {
	return &eventBroadcaster{subs: make(map[chan []byte]struct{})}
}

// subscribe registers a new subscriber channel and returns it. The caller
// must eventually call unsubscribe with the same channel.
func (b *eventBroadcaster) subscribe() chan []byte {
	ch := make(chan []byte, eventStreamSubscriberBuffer)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

// unsubscribe deregisters ch and closes it. Safe to call on an
// already-unsubscribed channel (no-op) — defensive, though the only real
// caller (newEventStreamHandler's defer) never double-calls it.
func (b *eventBroadcaster) unsubscribe(ch chan []byte) {
	b.mu.Lock()
	if _, ok := b.subs[ch]; ok {
		delete(b.subs, ch)
		close(ch)
	}
	b.mu.Unlock()
}

// broadcast sends payload to every current subscriber, non-blocking — a
// subscriber whose buffer is full is skipped (dropped, not blocked) rather
// than stalling every other subscriber or the caller (which may be a hot
// state-transition path). Logged at Warn so a persistently wedged listener
// is visible, not silent.
func (b *eventBroadcaster) broadcast(payload []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- payload:
		default:
			slog.Warn("smeldr: event stream subscriber buffer full, dropping event")
		}
	}
}

// count reports the current subscriber count. Test-only introspection.
func (b *eventBroadcaster) count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs)
}

// eventStreamHeartbeat is the interval between keepalive ping lines sent to
// an idle stream connection — a var, not a const, so tests can shrink it
// rather than waiting out a real interval (same injectable-timing idiom
// already used for the webhook worker pool's realClock{}).
var eventStreamHeartbeat = 25 * time.Second

// newEventStreamHandler returns the http.Handler mounted at GET
// /_events/stream by [App.Handler] when [App.EventStream] has been called.
// It requires the Author role and serves plain HTTP + bearer auth so it
// works even when MCP is unavailable — same contract as [newLogsHandler].
//
// Each connection is held open and receives every broadcast event as one
// NDJSON line (a single-line JSON object, "\n"-terminated), flushed
// immediately. A periodic {"type":"ping"} line is sent on
// eventStreamHeartbeat to keep the connection alive through idle-timing
// reverse proxies. Delivery is at-most-once: a dropped connection misses
// whatever fired while it was gone, and reconnecting starts fresh — no
// replay/backfill in this first cut (design/agent-event-signaling.md, open
// question 1).
//
// A malformed/missing token yields 401; wrong role 403; a ResponseWriter
// that does not implement http.Flusher (never true for a real HTTP/1.1+
// connection) yields 500.
func newEventStreamHandler(auth AuthFunc, b *eventBroadcaster) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.authenticate(r)
		if !ok {
			WriteError(w, r, ErrUnauth)
			return
		}
		if !user.HasRole(Author) {
			WriteError(w, r, ErrForbidden)
			return
		}
		fl, ok := w.(http.Flusher)
		if !ok {
			WriteError(w, r, ErrInternal)
			return
		}

		w.Header().Set("Content-Type", "application/x-ndjson")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no") // nginx: disable proxy buffering on this route
		w.WriteHeader(http.StatusOK)
		fl.Flush()

		ch := b.subscribe()
		defer b.unsubscribe(ch)

		ticker := time.NewTicker(eventStreamHeartbeat)
		defer ticker.Stop()

		for {
			select {
			case <-r.Context().Done():
				return
			case payload, ok := <-ch:
				if !ok {
					return
				}
				if _, err := w.Write(append(payload, '\n')); err != nil {
					return
				}
				fl.Flush()
			case <-ticker.C:
				if _, err := w.Write([]byte(`{"type":"ping"}` + "\n")); err != nil {
					return
				}
				fl.Flush()
			}
		}
	})
}
