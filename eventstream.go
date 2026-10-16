package smeldr

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

// eventStreamSubscriberBuffer is the per-subscriber channel capacity used by
// [eventBroadcaster.subscribe].
const eventStreamSubscriberBuffer = 256

// eventStreamMaxSubscribersPerToken bounds how many concurrent
// GET /_events/stream connections a single token may hold open at once.
// The design's own listener model is one long-lived connection per
// machine per token (design/agent-event-signaling.md) — steady-state is
// exactly 1. Set to 4, not 1, to tolerate a listener's own reconnect
// overlap (old connection still tearing down while a new one opens)
// without rejecting normal operation; a token past this is treated as a
// runaway reconnect loop or a compromised token, not legitimate use (T271).
const eventStreamMaxSubscribersPerToken = 4

// eventStreamChannelAll is the reserved channel value meaning "subscribe to
// (or, if ever used as a publish target, reach) every channel" — the
// wildcard for [eventBroadcaster.subscribe]'s "everything" mode, not a
// channel name of its own. Chosen as a query-string-safe literal ("all"
// rather than "*") so it reads cleanly in a curl command or log line
// (A302).
const eventStreamChannelAll = "all"

// Topic channels: events with no owning role are published on a named topic,
// not broadcast, so they reach that topic's subscribers and every
// [eventStreamChannelAll] subscriber but never wake a role's own channel. A
// consumer can subscribe to one topic alone (?channel=relations).
const (
	// eventStreamChannelRelations carries relation.asserted and
	// relation.ended.
	eventStreamChannelRelations = "relations"
	// eventStreamChannelAmendments carries every Amendment event.
	eventStreamChannelAmendments = "amendments"
	// eventStreamChannelSignals carries Signal state changes
	// (signal.transitioned) and the expiry sweep's summary
	// (signal.expiry_swept). signal.created still routes on the receiver: it
	// is the wake-up for new work.
	eventStreamChannelSignals = "signals"
	// eventStreamChannelTypes carries content_type.redefined: a
	// runtime-defined type's schema changed.
	eventStreamChannelTypes = "types"
)

// eventStreamTypeTopicPrefix starts the topic channel of a runtime-defined
// type that routes its events (a schema field with role "channel"): every
// event of such a type also goes to "type:<type name>", so a reviewer of the
// type can follow every band's items without hearing other types. The prefix
// keeps a type topic from ever colliding with a role or band channel.
const eventStreamTypeTopicPrefix = "type:"

// typeTopic is the topic channel of the runtime-defined type typeName.
func typeTopic(typeName string) string { return eventStreamTypeTopicPrefix + typeName }

// parseChannels splits a ?channel= value into its channels: comma-separated,
// whitespace trimmed, empty entries dropped. An empty result, or "all" among
// the entries, means every channel ([eventStreamChannelAll]).
func parseChannels(q string) []string {
	var out []string
	for _, c := range strings.Split(q, ",") {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if c == eventStreamChannelAll {
			return []string{eventStreamChannelAll}
		}
		out = append(out, c)
	}
	if len(out) == 0 {
		return []string{eventStreamChannelAll}
	}
	return out
}

// eventStreamSub is one subscriber's registration: which token owns the
// connection (for the per-token concurrency cap) and which channel it
// asked to receive — either a real channel name or [eventStreamChannelAll]
// for the unfiltered firehose.
type eventStreamSub struct {
	tokenID string
	// channels are the channels the connection asked for ([parseChannels]):
	// one or more channel names, or just [eventStreamChannelAll].
	channels []string
	// includeOwn opts this connection back in to events its own token caused
	// (GET /_events/stream?include_own=true). The default, false, drops them:
	// see [eventBroadcaster.broadcastFrom].
	includeOwn bool
	// scope is the owner's read scope, loaded at connect: an event about an
	// item it may not read is not delivered ([eventReadable]). Nil reads all.
	scope *ReadScope
	// subs, when set, are the owner's compiled subscriptions (D116): they
	// replace the channel and include_own rules. recompile is signalled when
	// the owner's subscriptions change.
	subs      *connSubscriptions
	recompile chan struct{}
	// fingerprint is the stored fingerprint of the connection's token, so a
	// revocation closes it.
	fingerprint string
}

// eventBroadcaster fans a payload out to connected subscribers — either to
// every one of them ([eventBroadcaster.broadcast], "reaches every channel")
// or to only those subscribed to one channel plus any wildcard
// ([eventStreamChannelAll]) subscriber ([eventBroadcaster.publish]).
// Without a log it is in memory only and at most once; with [App.EventLog]
// every event is written before it is fanned out, so a connection can resume
// with a cursor (D117).
type eventBroadcaster struct {
	mu      sync.Mutex
	subs    map[chan []byte]eventStreamSub
	byToken map[string]int // token ID -> concurrent subscriber count

	closing     chan struct{} // closed by shutdown; every stream handler returns on it
	closingOnce sync.Once

	// log, when set by [App.EventLog], makes delivery durable: every event is
	// written before it is fanned out ([eventLog]).
	log *eventLog
	// canon maps an event's lower-case type name to its registered name, for
	// the read check. Nil keeps the name as given.
	canon func(string) string
}

// newEventBroadcaster returns an empty [eventBroadcaster].
func newEventBroadcaster() *eventBroadcaster {
	return &eventBroadcaster{
		subs:    make(map[chan []byte]eventStreamSub),
		byToken: make(map[string]int),
		closing: make(chan struct{}),
	}
}

// shutdown releases every connected stream handler at once, so a graceful
// server shutdown does not wait for stream clients to disconnect. Safe to
// call more than once.
func (b *eventBroadcaster) shutdown() {
	if b.log != nil {
		b.log.shutdown()
	}
	b.closingOnce.Do(func() { close(b.closing) })
}

// subscribe registers a new subscriber channel for tokenID, listening on
// channel (a real channel name, or [eventStreamChannelAll] for every
// channel), and returns it. Returns [ErrTooManyRequests] without
// subscribing when tokenID already holds eventStreamMaxSubscribersPerToken
// concurrent connections (T271). The caller must eventually call
// unsubscribe with the same channel on success.
func (b *eventBroadcaster) subscribe(tokenID, channel string) (chan []byte, error) {
	return b.subscribeOpts(tokenID, channel, false)
}

// subscribeOpts is [eventBroadcaster.subscribe] with the includeOwn option:
// when true the connection also receives events its own token caused, which
// the default drops (see [eventBroadcaster.broadcastFrom]).
func (b *eventBroadcaster) subscribeOpts(tokenID, channel string, includeOwn bool) (chan []byte, error) {
	return b.subscribeScoped(tokenID, channel, includeOwn, nil)
}

// subscribeScoped is [eventBroadcaster.subscribeOpts] with the owner's read
// scope, checked on every delivery (nil reads all).
func (b *eventBroadcaster) subscribeScoped(tokenID, channel string, includeOwn bool, scope *ReadScope) (chan []byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.byToken[tokenID] >= eventStreamMaxSubscribersPerToken {
		return nil, ErrTooManyRequests
	}
	ch := make(chan []byte, eventStreamSubscriberBuffer)
	b.subs[ch] = eventStreamSub{tokenID: tokenID, channels: parseChannels(channel), includeOwn: includeOwn, scope: scope}
	b.byToken[tokenID]++
	return ch, nil
}

// unsubscribe deregisters ch, closes it, and frees its slot in the owning
// token's concurrent-connection count. Safe to call on an
// already-unsubscribed channel (no-op) — defensive, though the only real
// caller (newEventStreamHandler's defer) never double-calls it.
func (b *eventBroadcaster) unsubscribe(ch chan []byte) {
	b.mu.Lock()
	if sub, ok := b.subs[ch]; ok {
		delete(b.subs, ch)
		b.byToken[sub.tokenID]--
		if b.byToken[sub.tokenID] <= 0 {
			delete(b.byToken, sub.tokenID)
		}
		close(ch)
	}
	b.mu.Unlock()
}

// broadcast sends payload to every current subscriber regardless of which
// channel it asked for — mode 4, "publish as a broadcast reaching every
// channel" (A302). Used for events with no single owning channel (e.g. an
// Amendment, or a generic content [Module] lifecycle event, which have no
// band/receiver-shaped field to key on). Non-blocking — a subscriber whose
// buffer is full is skipped (dropped, not blocked) rather than stalling
// every other subscriber or the caller (which may be a hot state-transition
// path). Logged at Warn so a persistently wedged listener is visible, not
// silent.
func (b *eventBroadcaster) broadcast(payload []byte) {
	b.broadcastFrom("", payload)
}

// broadcastFrom is [eventBroadcaster.broadcast] for an event caused by the
// token origin: a subscriber holding that same token is skipped, unless it
// asked for its own events with include_own. An empty origin (a system actor,
// or an unauthenticated caller) skips nobody. The match is on the token's
// User.ID, the same value [eventBroadcaster.subscribe] is keyed on and the
// request context's User().ID carries.
func (b *eventBroadcaster) broadcastFrom(origin string, payload []byte) {
	b.publishEvent(eventMeta{id: envelopeID(payload), actorID: origin}, payload)
}

// publish sends payload to every subscriber listening on channel, plus
// every subscriber listening on [eventStreamChannelAll] — mode 3, "publish
// to one channel" (A302). Same non-blocking drop-and-warn semantics as
// broadcast for a full subscriber buffer.
func (b *eventBroadcaster) publish(channel string, payload []byte) {
	b.publishFrom("", channel, payload)
}

// publishFrom is [eventBroadcaster.publish] for an event caused by the token
// origin, with the same self-skip rule as [eventBroadcaster.broadcastFrom].
func (b *eventBroadcaster) publishFrom(origin, channel string, payload []byte) {
	b.publishToFrom(origin, []string{channel}, payload)
}

// publishToFrom sends payload, caused by the token origin, to every subscriber
// listening on any of channels or on [eventStreamChannelAll]: once per
// subscriber, however many of its channels match. No channels at all is a
// broadcast ([eventBroadcaster.broadcastFrom]).
func (b *eventBroadcaster) publishToFrom(origin string, channels []string, payload []byte) {
	b.publishEvent(eventMeta{id: envelopeID(payload), actorID: origin, channels: channels}, payload)
}

// publishEvent hands one event to the log, which writes it and then fans it
// out, or, without a log, fans it out at once ([eventBroadcaster.fanout]).
func (b *eventBroadcaster) publishEvent(meta eventMeta, payload []byte) {
	if b.canon != nil {
		if meta.subjectType != "" {
			meta.subjectType = b.canon(meta.subjectType)
		}
		if meta.otherType != "" {
			meta.otherType = b.canon(meta.otherType)
		}
	}
	if meta.event == "" {
		meta.event = envelopeEvent(payload)
	}
	if meta.signalType == "" && meta.fields != nil {
		meta.signalType = meta.fields["signal_type"]
	}
	ev := streamEvent{payload: payload, meta: meta, at: time.Now().UTC()}
	if b.log != nil {
		b.log.enqueue(ev)
		return
	}
	b.fanout(ev)
}

// fanout delivers ev to every connection it is deliverable to
// ([eventStreamSub.deliverable]). It never blocks: a connection whose buffer
// is full is closed, not thinned, so its client reconnects (with ?after=, when
// the log is wired) and loses nothing the log kept.
func (b *eventBroadcaster) fanout(ev streamEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch, sub := range b.subs {
		if !sub.deliverable(ev.meta) {
			continue
		}
		select {
		case ch <- ev.payload:
		default:
			slog.Warn("smeldr: event stream: a connection's buffer is full; it is closed so its client reconnects", "token", sub.tokenID)
			delete(b.subs, ch)
			b.byToken[sub.tokenID]--
			if b.byToken[sub.tokenID] <= 0 {
				delete(b.byToken, sub.tokenID)
			}
			close(ch)
		}
	}
}

// deliverable reports whether an event with meta goes to this connection: on
// one of its channels (an event with none is a broadcast), not caused by its
// own token unless it asked for its own events, and readable by its scope.
func (s eventStreamSub) deliverable(meta eventMeta) bool {
	if s.subs != nil {
		return s.subs.match(meta) && eventReadable(s.scope, meta)
	}
	if len(meta.channels) > 0 && !s.wants(meta.channels) {
		return false
	}
	if meta.actorID != "" && !s.includeOwn && s.tokenID == meta.actorID {
		return false
	}
	return eventReadable(s.scope, meta)
}

// wants reports whether the subscriber listens on any of channels, or on
// every channel.
func (s eventStreamSub) wants(channels []string) bool {
	for _, mine := range s.channels {
		if mine == eventStreamChannelAll {
			return true
		}
		for _, c := range channels {
			if mine == c {
				return true
			}
		}
	}
	return false
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
// Each connection is held open and receives every event delivered to its
// requested channel as one NDJSON line (a single-line JSON object,
// "\n"-terminated), flushed immediately — plus every true broadcast
// (A302, mode 4), regardless of channel. The channel is chosen via the
// "channel" query parameter (e.g. "?channel=core"); an absent or empty
// value defaults to [eventStreamChannelAll] ("?channel=all" also works
// explicitly), the unfiltered firehose every subscriber received
// unconditionally before A302 — this keeps every existing caller working
// unmodified until it opts into a specific channel. No role gate is added
// for [eventStreamChannelAll]: this is the same access every Author-role
// token already had, not a new grant. A periodic {"type":"ping"} line is
// sent on eventStreamHeartbeat to keep the connection alive through
// idle-timing reverse proxies. Without the event log delivery is
// at most once: a dropped connection misses whatever fired while it was gone.
//
// A malformed/missing token yields 401; wrong role 403; a token already
// holding eventStreamMaxSubscribersPerToken concurrent connections 429
// (T271); a ResponseWriter that does not implement http.Flusher (never true
// for a real HTTP/1.1+ connection) yields 500. The channel query parameter
// itself has no error path. Any non-empty value is a comma-separated list of
// channel names, each used verbatim (free-form, matching Band/Scope/Receiver's
// own unrestricted-string convention elsewhere): the connection receives an
// event published to any of them, once ([parseChannels]). An absent or empty
// value is not an error; it is the documented default (A302).
//
// The include_own query parameter has no error path either: exactly "true"
// opts the connection back in to events its own token caused, anything else
// (or nothing) keeps the default of not delivering them. The connection is
// keyed on the authenticated user's ID, the same value a request context
// carries as User().ID, which is what the self-skip in
// [eventBroadcaster.broadcastFrom] compares.
//
// With the event log wired ([App.EventLog]), ?after=<event id> resumes a
// connection: the events after that envelope ID are written first, in log
// order, then live events, with no event lost between the two (an event may
// come twice: deduplicate by id). A {"type":"skipped","after":...,
// "resumed_from":...} line says the cursor was no longer kept and the replay
// starts at the oldest kept event; one {"type":"caught_up","cursor":...} line
// marks the start of live delivery. ?after= without the log is 400; a failure
// reading the first page is 500. A connection whose buffer fills is closed:
// reconnect with ?after= the last id seen.
func newEventStreamHandler(auth AuthFunc, b *eventBroadcaster) http.Handler {
	return newEventStreamHandlerScoped(auth, b, nil)
}

// streamScopeLoader loads the read scope of a stream connection's owner; nil
// (no governance) reads everything.
type streamScopeLoader func(ctx context.Context, user User) (*ReadScope, error)

// eventStreamReplayPage is how many logged events a resuming connection reads
// at a time.
const eventStreamReplayPage = 500

// newEventStreamHandlerScoped is [newEventStreamHandler] with the owner's read
// scope, loaded at connect by loadScope and checked on every delivery, live
// and replayed.
func newEventStreamHandlerScoped(auth AuthFunc, b *eventBroadcaster, loadScope streamScopeLoader) http.Handler {
	return newEventStreamHandlerWith(auth, b, loadScope, nil, nil)
}

// subscriptionCursorEvery is how often a connection on a named subscription
// saves its cursor while events flow; it is also saved when the connection
// ends.
var subscriptionCursorEvery = 10 * time.Second

// newEventStreamHandlerWith is [newEventStreamHandlerScoped] with the App's
// subscriptions (D116). With src set:
//   - ?subscription=<address> delivers that subscription (the caller's, or any
//     with administer; a missing or hidden one is 404) and resumes from its
//     cursor when no ?after= is given;
//   - a connection with neither ?subscription= nor ?channel= delivers every
//     active subscription of its owner, the universal defaults included;
//   - ?channel= keeps today's routing (D97), under the read ceiling.
//
// ?subscription= with ?channel= is 400. Without src, ?subscription= is 400.
//
// revoked, when set, reports whether the connection's token was revoked: it is
// asked every scopeCacheTTL, and a revoked token's connection is closed (a
// revocation through the App closes it at once). The connection's read scope
// is reloaded on the same tick and whenever its owner's grants change.
func newEventStreamHandlerWith(auth AuthFunc, b *eventBroadcaster, loadScope streamScopeLoader, src streamSubscriptions, revoked func(context.Context, string) (bool, error)) http.Handler {
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

		q := r.URL.Query()
		channel := q.Get("channel")
		if channel == "" {
			channel = eventStreamChannelAll
		}
		includeOwn := q.Get("include_own") == "true"
		ident := q.Get("subscription")
		if ident != "" && q.Has("channel") {
			WriteError(w, r, fmt.Errorf("%w: ?subscription= and ?channel= cannot be combined", ErrBadRequest))
			return
		}
		if ident != "" && src == nil {
			WriteError(w, r, fmt.Errorf("%w: this instance keeps no subscriptions", ErrBadRequest))
			return
		}
		after, resume := q.Get("after"), q.Has("after")
		if resume && b.log == nil {
			WriteError(w, r, fmt.Errorf("%w: ?after= needs the event log, which this instance does not keep", ErrBadRequest))
			return
		}
		var scope *ReadScope
		if loadScope != nil {
			s, err := loadScope(r.Context(), user)
			if err != nil {
				WriteError(w, r, err)
				return
			}
			scope = s
		}
		var subs *connSubscriptions
		var named *Subscription
		if src != nil && (ident != "" || !q.Has("channel")) {
			cs, n, err := src.compile(r.Context(), user, ident)
			if err != nil {
				WriteError(w, r, err)
				return
			}
			subs, named = cs, n
			if named != nil && !resume && named.Cursor != "" && b.log != nil {
				after, resume = named.Cursor, true
			}
		}

		// subscribe before any header is written — headers cannot be
		// unwritten, so a 429 rejection (T271) has to happen before the
		// status line commits to 200. On a resume it also comes before the
		// replay, so an event published meanwhile is buffered, not lost.
		// A subscription owned by another token (read with administer) is
		// delivered under its owner's read scope.
		scopeUser := user
		if subs != nil && subs.owner != user.ID && loadScope != nil {
			scopeUser = User{ID: subs.owner}
			s, err := loadScope(r.Context(), scopeUser)
			if err != nil {
				WriteError(w, r, err)
				return
			}
			scope = s
		}
		fingerprint := bearerFingerprint(r)
		sub := eventStreamSub{tokenID: user.ID, channels: parseChannels(channel), includeOwn: includeOwn, scope: scope, subs: subs}
		ch, recompile, err := b.subscribeConn(user.ID, channel, includeOwn, scope, subs, fingerprint)
		if err != nil {
			WriteError(w, r, err)
			return
		}
		defer b.unsubscribe(ch)

		var first EventPage
		var since int64
		if resume {
			since = b.log.last.Load()
			first, err = b.log.page(r.Context(), after, eventStreamReplayPage)
			if err != nil {
				WriteError(w, r, err)
				return
			}
		}

		w.Header().Set("Content-Type", "application/x-ndjson")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no") // nginx: disable proxy buffering on this route
		w.WriteHeader(http.StatusOK)
		fl.Flush()

		// The cursor of a named subscription: the last envelope delivered,
		// saved every subscriptionCursorEvery and when the connection ends.
		last, saved := "", ""
		saveCursor := func() {
			if named == nil || last == "" || last == saved {
				return
			}
			if err := src.saveCursor(context.WithoutCancel(r.Context()), named.ID, last); err != nil {
				slog.WarnContext(r.Context(), "smeldr: event stream: subscription cursor not saved", "subscription", named.Address, "error", err)
				return
			}
			saved = last
		}
		defer saveCursor()

		write := func(line []byte) bool {
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
			// line is shared with every connection that gets the event:
			// never append to it, which would write into its backing array.
			if _, err := w.Write(line); err != nil {
				return false
			}
			if _, err := w.Write(newline); err != nil {
				return false
			}
			fl.Flush()
			if id := envelopeID(line); id != "" {
				last = id
			}
			return true
		}

		// seen holds the replayed events that may also arrive live: those
		// logged from just before this connection subscribed. A live event in
		// it is skipped once.
		var seen map[string]bool
		if resume {
			var ok bool
			seen, ok = replayStream(r.Context(), b, first, since, after, sub, write)
			if !ok {
				return
			}
		}

		ticker := time.NewTicker(eventStreamHeartbeat)
		defer ticker.Stop()
		cursorTick := time.NewTicker(subscriptionCursorEvery)
		defer cursorTick.Stop()
		backstop := time.NewTicker(scopeCacheTTL)
		defer backstop.Stop()
		// reload loads the read scope again and recompiles the subscriptions;
		// false closes the connection (fail closed), and the client reconnects.
		reload := func() bool {
			if loadScope != nil {
				s, err := loadScope(r.Context(), scopeUser)
				if err != nil {
					slog.WarnContext(r.Context(), "smeldr: event stream: read scope not reloaded; closing so the client reconnects", "error", err)
					return false
				}
				b.setScope(ch, s)
			}
			if src != nil && subs != nil {
				cs, _, err := src.compile(r.Context(), user, ident)
				if err != nil {
					slog.WarnContext(r.Context(), "smeldr: event stream: subscriptions not recompiled; closing so the client reconnects", "error", err)
					return false
				}
				b.setSubs(ch, cs)
			}
			return true
		}

		for {
			select {
			case <-r.Context().Done():
				return
			case <-b.closing:
				return
			case <-recompile:
				if !reload() {
					return
				}
			case <-backstop.C:
				if revoked != nil && fingerprint != "" {
					gone, err := revoked(r.Context(), fingerprint)
					if err != nil || gone {
						return
					}
				}
				if !reload() {
					return
				}
			case <-cursorTick.C:
				saveCursor()
			case payload, ok := <-ch:
				if !ok {
					return
				}
				if len(seen) > 0 {
					if id := envelopeID(payload); seen[id] {
						delete(seen, id)
						continue
					}
				}
				if !write(payload) {
					return
				}
			case <-ticker.C:
				if !write([]byte(`{"type":"ping"}`)) {
					return
				}
			}
		}
	})
}

// replayStream writes the logged events after the cursor, page by page,
// that sub would have received, then the caught_up line. since is the log's
// last seq when the connection subscribed: a replayed event logged from one
// batch before it may also arrive live, so its ID is returned in seen. It
// reports false when the connection must close (a write or a later page
// failed).
func replayStream(ctx context.Context, b *eventBroadcaster, page EventPage, since int64, after string, sub eventStreamSub, write func([]byte) bool) (map[string]bool, bool) {
	seen := map[string]bool{}
	if page.Skipped {
		line, _ := json.Marshal(map[string]string{"type": "skipped", "after": after, "resumed_from": page.Oldest})
		if !write(line) {
			return nil, false
		}
	}
	cursor := after
	for {
		var lastSeq int64
		for _, e := range page.Events {
			lastSeq = e.Seq
			if !sub.deliverable(e.meta()) {
				continue
			}
			if !write(e.Payload) {
				return nil, false
			}
			cursor = e.ID
			if e.Seq > since-eventLogBatch {
				seen[e.ID] = true
			}
		}
		if len(page.Events) < eventStreamReplayPage {
			break
		}
		events, err := b.log.after(ctx, lastSeq, eventStreamReplayPage)
		if err != nil {
			slog.WarnContext(ctx, "smeldr: event stream: replay failed; closing so the client resumes", "error", err)
			return nil, false
		}
		page = EventPage{Events: events}
	}
	line, _ := json.Marshal(map[string]string{"type": "caught_up", "cursor": cursor})
	if !write(line) {
		return nil, false
	}
	return seen, true
}

// subscribeSubs is [eventBroadcaster.subscribeScoped] for a connection that
// delivers its owner's subscriptions (D116).
func (b *eventBroadcaster) subscribeSubs(tokenID string, subs *connSubscriptions, scope *ReadScope) (chan []byte, chan struct{}, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.byToken[tokenID] >= eventStreamMaxSubscribersPerToken {
		return nil, nil, ErrTooManyRequests
	}
	ch := make(chan []byte, eventStreamSubscriberBuffer)
	recompile := make(chan struct{}, 1)
	b.subs[ch] = eventStreamSub{tokenID: tokenID, scope: scope, subs: subs, recompile: recompile}
	b.byToken[tokenID]++
	return ch, recompile, nil
}

// setSubs replaces the compiled subscriptions of the connection ch.
func (b *eventBroadcaster) setSubs(ch chan []byte, subs *connSubscriptions) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if sub, ok := b.subs[ch]; ok {
		sub.subs = subs
		b.subs[ch] = sub
	}
}

// recompileOwner signals every subscription connection of owner to compile
// its subscriptions again.
func (b *eventBroadcaster) recompileOwner(owner string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, sub := range b.subs {
		if sub.recompile == nil || sub.subs == nil || sub.subs.owner != owner {
			continue
		}
		select {
		case sub.recompile <- struct{}{}:
		default:
		}
	}
}

// newline ends each streamed line.
var newline = []byte{'\n'}

// subscribeConn registers a stream connection with everything it is
// delivered by: its channels or subscriptions, its read scope and its token
// fingerprint (for closing it on revocation). reload is signalled when its
// owner's grants or subscriptions change.
func (b *eventBroadcaster) subscribeConn(tokenID, channel string, includeOwn bool, scope *ReadScope, subs *connSubscriptions, fingerprint string) (chan []byte, chan struct{}, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.byToken[tokenID] >= eventStreamMaxSubscribersPerToken {
		return nil, nil, ErrTooManyRequests
	}
	ch := make(chan []byte, eventStreamSubscriberBuffer)
	reload := make(chan struct{}, 1)
	b.subs[ch] = eventStreamSub{tokenID: tokenID, channels: parseChannels(channel), includeOwn: includeOwn,
		scope: scope, subs: subs, recompile: reload, fingerprint: fingerprint}
	b.byToken[tokenID]++
	return ch, reload, nil
}

// setScope replaces the read scope of the connection ch.
func (b *eventBroadcaster) setScope(ch chan []byte, scope *ReadScope) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if sub, ok := b.subs[ch]; ok {
		sub.scope = scope
		b.subs[ch] = sub
	}
}

// reloadToken signals the connections of token (every connection when empty)
// to reload their scope and subscriptions.
func (b *eventBroadcaster) reloadToken(token string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, sub := range b.subs {
		if sub.recompile == nil {
			continue
		}
		if token != "" && sub.tokenID != token && (sub.subs == nil || sub.subs.owner != token) {
			continue
		}
		select {
		case sub.recompile <- struct{}{}:
		default:
		}
	}
}

// closeFingerprint closes every connection opened with the token whose
// stored fingerprint is fp: a revoked token stops receiving at once.
func (b *eventBroadcaster) closeFingerprint(fp string) {
	if fp == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch, sub := range b.subs {
		if sub.fingerprint != fp {
			continue
		}
		delete(b.subs, ch)
		b.byToken[sub.tokenID]--
		if b.byToken[sub.tokenID] <= 0 {
			delete(b.byToken, sub.tokenID)
		}
		close(ch)
	}
}
