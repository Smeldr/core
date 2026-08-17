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

// eventStreamMaxSubscribersPerToken bounds how many concurrent
// GET /_events/stream connections a single token may hold open at once.
// The design's own listener model is one long-lived connection per
// machine per token (design/agent-event-signaling.md) — steady-state is
// exactly 1. Set to 4, not 1, to tolerate a listener's own reconnect
// overlap (old connection still tearing down while a new one opens)
// without rejecting normal operation; a token past this is treated as a
// runaway reconnect loop or a compromised token, not legitimate use (T271).
const eventStreamMaxSubscribersPerToken = 4

// eventBroadcaster fans a broadcast payload out to every currently-connected
// subscriber channel. In-memory only — no persistence, no replay; a
// subscriber that connects after a broadcast simply never sees it
// (at-most-once, per design/agent-event-signaling.md's own lean).
type eventBroadcaster struct {
	mu      sync.Mutex
	subs    map[chan []byte]string // channel -> owning token ID
	byToken map[string]int         // token ID -> concurrent subscriber count
}

// newEventBroadcaster returns an empty [eventBroadcaster].
func newEventBroadcaster() *eventBroadcaster {
	return &eventBroadcaster{
		subs:    make(map[chan []byte]string),
		byToken: make(map[string]int),
	}
}

// subscribe registers a new subscriber channel for tokenID and returns it.
// Returns [ErrTooManyRequests] without subscribing when tokenID already
// holds eventStreamMaxSubscribersPerToken concurrent connections (T271).
// The caller must eventually call unsubscribe with the same channel on
// success.
func (b *eventBroadcaster) subscribe(tokenID string) (chan []byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.byToken[tokenID] >= eventStreamMaxSubscribersPerToken {
		return nil, ErrTooManyRequests
	}
	ch := make(chan []byte, eventStreamSubscriberBuffer)
	b.subs[ch] = tokenID
	b.byToken[tokenID]++
	return ch, nil
}

// unsubscribe deregisters ch, closes it, and frees its slot in the owning
// token's concurrent-connection count. Safe to call on an
// already-unsubscribed channel (no-op) — defensive, though the only real
// caller (newEventStreamHandler's defer) never double-calls it.
func (b *eventBroadcaster) unsubscribe(ch chan []byte) {
	b.mu.Lock()
	if tokenID, ok := b.subs[ch]; ok {
		delete(b.subs, ch)
		b.byToken[tokenID]--
		if b.byToken[tokenID] <= 0 {
			delete(b.byToken, tokenID)
		}
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

// tokenCount reports the current subscriber count for tokenID. Test-only
// introspection — mirrors count()'s own pattern.
func (b *eventBroadcaster) tokenCount(tokenID string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.byToken[tokenID]
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
// A malformed/missing token yields 401; wrong role 403; a token already
// holding eventStreamMaxSubscribersPerToken concurrent connections 429
// (T271); a ResponseWriter that does not implement http.Flusher (never true
// for a real HTTP/1.1+ connection) yields 500.
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
		rc := http.NewResponseController(w)

		// subscribe before any header is written — headers cannot be
		// unwritten, so a 429 rejection (T271) has to happen before the
		// status line commits to 200.
		ch, err := b.subscribe(user.ID)
		if err != nil {
			WriteError(w, r, err)
			return
		}
		defer b.unsubscribe(ch)

		w.Header().Set("Content-Type", "application/x-ndjson")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no") // nginx: disable proxy buffering on this route
		w.WriteHeader(http.StatusOK)
		fl.Flush()

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
				// Config.WriteTimeout is a fixed deadline set once when this
				// connection's headers were read, never reset by an
				// intermediate Flush() — wrong for a deliberately long-lived
				// stream. Refreshing it here (rather than disabling it once)
				// still bounds a genuinely wedged write, matching T271's own
				// bounded-resource reasoning applied to connection duration
				// instead of connection count.
				if err := rc.SetWriteDeadline(time.Now().Add(2 * eventStreamHeartbeat)); err != nil {
					slog.WarnContext(r.Context(), "smeldr: event stream: SetWriteDeadline unsupported", "error", err)
				}
				if _, err := w.Write(append(payload, '\n')); err != nil {
					return
				}
				fl.Flush()
			case <-ticker.C:
				if err := rc.SetWriteDeadline(time.Now().Add(2 * eventStreamHeartbeat)); err != nil {
					slog.WarnContext(r.Context(), "smeldr: event stream: SetWriteDeadline unsupported", "error", err)
				}
				if _, err := w.Write([]byte(`{"type":"ping"}` + "\n")); err != nil {
					return
				}
				fl.Flush()
			}
		}
	})
}
