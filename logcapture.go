package smeldr

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"
)

// defaultLogCapacity is the number of entries retained by the in-memory log
// ring when [WithLogCapacity] is not supplied.
const defaultLogCapacity = 500

// LogEntry is one captured log record held in the in-memory ring installed by
// [App.CaptureLogs]. The HTTP endpoint mounted in a later step serialises these
// to JSON; the field tags define that wire shape.
type LogEntry struct {
	Time  time.Time      `json:"time"`            // event time, normalised to UTC
	Level string         `json:"level"`           // slog level string: "DEBUG", "INFO", "WARN", "ERROR"
	Msg   string         `json:"msg"`             // log message
	Attrs map[string]any `json:"attrs,omitempty"` // record attributes; open groups nest as sub-objects
	Seq   uint64         `json:"seq"`             // monotonic capture sequence number, 1-based
}

// logRing is a bounded, concurrency-safe circular buffer of [LogEntry] values.
// When the buffer is full the oldest entry is overwritten and the dropped
// counter is incremented, so callers can tell that older entries were evicted.
type logRing struct {
	mu      sync.Mutex
	buf     []LogEntry
	head    int    // index of the oldest entry
	count   int    // number of valid entries currently stored
	seq     uint64 // total entries ever appended; also the last assigned Seq
	dropped uint64 // entries evicted by overwrite since start
}

// newLogRing returns a logRing retaining at most capacity entries.
// A capacity <= 0 falls back to [defaultLogCapacity].
func newLogRing(capacity int) *logRing {
	if capacity <= 0 {
		capacity = defaultLogCapacity
	}
	return &logRing{buf: make([]LogEntry, capacity)}
}

// append stores e, assigning it the next monotonic Seq. When the ring is full
// the oldest entry is overwritten and dropped is incremented.
func (r *logRing) append(e LogEntry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seq++
	e.Seq = r.seq
	capN := len(r.buf)
	if r.count < capN {
		r.buf[(r.head+r.count)%capN] = e
		r.count++
		return
	}
	// Buffer full: overwrite the oldest entry and advance head.
	r.buf[r.head] = e
	r.head = (r.head + 1) % capN
	r.dropped++
}

// snapshot returns a copy of the buffered entries, newest first, together with
// the configured capacity and the number of entries dropped since start.
// The returned slice is owned by the caller. Safe for concurrent use.
func (r *logRing) snapshot() (entries []LogEntry, capacity int, dropped uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	capN := len(r.buf)
	out := make([]LogEntry, r.count)
	for i := 0; i < r.count; i++ {
		// Oldest entry is at head; walk backwards for newest-first order.
		idx := (r.head + r.count - 1 - i) % capN
		out[i] = r.buf[idx]
	}
	return out, capN, r.dropped
}

// groupOrAttrs records one [slog.Handler.WithGroup] or [slog.Handler.WithAttrs]
// call so the captured ring entry can reconstruct the full attribute tree.
// Exactly one of group / attrs is set per value.
type groupOrAttrs struct {
	group string      // group name, when non-empty
	attrs []slog.Attr // attrs, when non-empty
}

// teeHandler is a [slog.Handler] that forwards every record to an inner handler
// (typically the process's existing stderr handler) and, for records at or above
// minLevel, also captures them into ring. It never narrows what the inner
// handler receives: see [teeHandler.Enabled].
type teeHandler struct {
	inner    slog.Handler
	ring     *logRing
	minLevel slog.Level
	goas     []groupOrAttrs // accumulated WithGroup / WithAttrs state
}

// Enabled reports whether either destination wants a record at level.
// The OR is deliberate: returning only the ring threshold could suppress a level
// the inner handler still emits, silently dropping existing stderr output.
func (h *teeHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level) || level >= h.minLevel
}

// Handle forwards rec to the inner handler (honouring the inner handler's own
// level), and independently captures it into the ring when rec.Level >= minLevel.
func (h *teeHandler) Handle(ctx context.Context, rec slog.Record) error {
	var ierr error
	if h.inner.Enabled(ctx, rec.Level) {
		ierr = h.inner.Handle(ctx, rec)
	}
	if rec.Level >= h.minLevel {
		h.ring.append(h.entry(rec))
	}
	return ierr
}

// WithAttrs returns a handler that adds attrs to both the inner handler and the
// captured ring entries.
func (h *teeHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	return h.with(groupOrAttrs{attrs: attrs}, h.inner.WithAttrs(attrs))
}

// WithGroup returns a handler that opens group name on both the inner handler and
// the captured ring entries.
func (h *teeHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	return h.with(groupOrAttrs{group: name}, h.inner.WithGroup(name))
}

// with returns a shallow copy of h with goa appended and the inner handler
// replaced by the already-derived inner.
func (h *teeHandler) with(goa groupOrAttrs, inner slog.Handler) *teeHandler {
	nh := *h
	nh.inner = inner
	nh.goas = make([]groupOrAttrs, len(h.goas)+1)
	copy(nh.goas, h.goas)
	nh.goas[len(nh.goas)-1] = goa
	return &nh
}

// entry builds a [LogEntry] from rec, applying the accumulated group/attr state
// so the captured attributes mirror what the inner handler emits.
func (h *teeHandler) entry(rec slog.Record) LogEntry {
	root := map[string]any{}
	cur := root
	for _, goa := range h.goas {
		if goa.group != "" {
			sub := map[string]any{}
			cur[goa.group] = sub
			cur = sub
			continue
		}
		for _, a := range goa.attrs {
			addAttr(cur, a)
		}
	}
	rec.Attrs(func(a slog.Attr) bool {
		addAttr(cur, a)
		return true
	})

	var attrs map[string]any
	if len(root) > 0 {
		attrs = root
	}
	return LogEntry{
		Time:  rec.Time.UTC(),
		Level: rec.Level.String(),
		Msg:   rec.Message,
		Attrs: attrs,
	}
}

// addAttr resolves a and stores it into m, recursing into group values so nested
// groups become nested maps. Empty attrs and empty keys are skipped, matching
// slog's own handling.
func addAttr(m map[string]any, a slog.Attr) {
	a.Value = a.Value.Resolve()
	if a.Equal(slog.Attr{}) {
		return
	}
	if a.Value.Kind() == slog.KindGroup {
		group := a.Value.Group()
		if len(group) == 0 {
			return
		}
		if a.Key == "" {
			// An empty key inlines the group's attrs into the parent.
			for _, ga := range group {
				addAttr(m, ga)
			}
			return
		}
		sub := map[string]any{}
		for _, ga := range group {
			addAttr(sub, ga)
		}
		if len(sub) > 0 {
			m[a.Key] = sub
		}
		return
	}
	if a.Key == "" {
		return
	}
	m[a.Key] = a.Value.Any()
}

// LogCaptureOption configures [App.CaptureLogs].
type LogCaptureOption func(*logCaptureConfig)

// logCaptureConfig holds the resolved options for [App.CaptureLogs].
type logCaptureConfig struct {
	capacity int
	minLevel slog.Level
}

// WithLogCapacity sets the maximum number of entries retained in the in-memory
// log ring. A value <= 0 keeps the default of 500.
func WithLogCapacity(n int) LogCaptureOption {
	return func(c *logCaptureConfig) { c.capacity = n }
}

// WithLogLevel sets the minimum level captured into the ring. Records below this
// level still reach the existing destination unchanged — the ring threshold
// never narrows what stderr receives. Defaults to [slog.LevelWarn].
func WithLogLevel(level slog.Level) LogCaptureOption {
	return func(c *logCaptureConfig) { c.minLevel = level }
}

// CaptureLogs installs a teeing [slog.Handler] that mirrors every log record to
// the in-memory ring buffer surfaced (in a later step) at GET /_logs, while still
// forwarding every record to the existing default handler (typically stderr).
// Capture is opt-in and additive: without this call nothing changes.
//
// By default the ring retains the most recent 500 records at [slog.LevelWarn] and
// above; configure with [WithLogCapacity] and [WithLogLevel].
//
// CaptureLogs wraps slog.Default().Handler() at the moment it is called and then
// calls [slog.SetDefault], so it must be invoked AFTER any application-side
// slog.SetDefault of its own — otherwise a later SetDefault replaces the tee and
// capture stops.
//
// Special case: when no handler has been configured (the zero-config default,
// slog's built-in handler, which writes through the standard log package),
// CaptureLogs forwards to a text handler on os.Stderr instead of wrapping it.
// Wrapping the built-in handler would be fatal: slog.SetDefault also repoints the
// log package through the new handler, so a wrapped built-in handler would route
// the log package back into itself — an infinite re-entrant loop. Apps that
// configure their own handler (the recommended path) keep that handler unchanged.
//
// CaptureLogs returns the App for chaining.
func (a *App) CaptureLogs(opts ...LogCaptureOption) *App {
	tee := newLogTee(slog.Default().Handler(), opts...)
	a.logRing = tee.ring
	slog.SetDefault(slog.New(tee))
	return a
}

// newLogTee builds the capturing [teeHandler] for current, applying opts.
// If current is the standard library's built-in default handler, a fresh text
// handler on os.Stderr is used as the forwarding target instead — see
// [App.CaptureLogs] for why wrapping the built-in handler would deadlock.
func newLogTee(current slog.Handler, opts ...LogCaptureOption) *teeHandler {
	cfg := logCaptureConfig{capacity: defaultLogCapacity, minLevel: slog.LevelWarn}
	for _, opt := range opts {
		opt(&cfg)
	}
	inner := current
	if bridgesToLog(current) {
		inner = slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{})
	}
	return &teeHandler{
		inner:    inner,
		ring:     newLogRing(cfg.capacity),
		minLevel: cfg.minLevel,
	}
}

// logsResponse is the JSON envelope returned by GET /_logs. The capacity and
// dropped fields tell the operator whether older entries were evicted from the
// ring, context a bare array could not convey.
type logsResponse struct {
	Capacity int        `json:"capacity"` // ring capacity (max entries retained)
	Count    int        `json:"count"`    // number of entries in this response
	Dropped  uint64     `json:"dropped"`  // entries evicted by overwrite since start
	Entries  []LogEntry `json:"entries"`  // newest-first; never null
}

// newLogsHandler returns the http.Handler mounted at GET /_logs by [App.Run] /
// [App.Handler] when [App.CaptureLogs] has been called. It requires the Admin role
// and serves plain HTTP + bearer auth so it works even when MCP is unavailable.
//
// Optional query parameters:
//   - level: minimum level to return (debug|info|warn|error), inclusive
//   - limit: return at most the N most recent matching entries
//   - since: RFC3339 timestamp; return only entries with Time strictly after it
//
// A malformed query parameter yields 400; no/invalid token 401; wrong role 403.
func newLogsHandler(auth AuthFunc, ring *logRing) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.authenticate(r)
		if !ok {
			WriteError(w, r, ErrUnauth)
			return
		}
		if !user.HasRole(Admin) {
			WriteError(w, r, ErrForbidden)
			return
		}

		q := r.URL.Query()

		var minLevel slog.Level
		hasLevel := false
		if s := q.Get("level"); s != "" {
			if err := minLevel.UnmarshalText([]byte(strings.ToUpper(s))); err != nil {
				WriteError(w, r, ErrBadRequest)
				return
			}
			hasLevel = true
		}

		var since time.Time
		if s := q.Get("since"); s != "" {
			t, err := time.Parse(time.RFC3339, s)
			if err != nil {
				WriteError(w, r, ErrBadRequest)
				return
			}
			since = t
		}

		limit := 0
		if s := q.Get("limit"); s != "" {
			n, err := strconv.Atoi(s)
			if err != nil || n < 0 {
				WriteError(w, r, ErrBadRequest)
				return
			}
			limit = n
		}

		all, capacity, dropped := ring.snapshot() // newest-first
		entries := make([]LogEntry, 0, len(all))
		for _, e := range all {
			if !since.IsZero() && !e.Time.After(since) {
				continue
			}
			if hasLevel && !levelAtLeast(e.Level, minLevel) {
				continue
			}
			entries = append(entries, e)
			if limit > 0 && len(entries) >= limit {
				break
			}
		}

		resp := logsResponse{
			Capacity: capacity,
			Count:    len(entries),
			Dropped:  dropped,
			Entries:  entries,
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			slog.WarnContext(r.Context(), "smeldr: logs encode failed", "error", err)
		}
	})
}

// levelAtLeast reports whether the captured level string is at or above min.
// An unparseable level is never filtered out (returns true).
func levelAtLeast(levelStr string, min slog.Level) bool {
	var l slog.Level
	if err := l.UnmarshalText([]byte(levelStr)); err != nil {
		return true
	}
	return l >= min
}

// bridgesToLog reports whether h is slog's built-in zero-config handler, which
// emits through the standard log package. That handler must never be wrapped and
// reinstalled via slog.SetDefault (see [App.CaptureLogs]). slog exposes no public
// type for it, so it is identified by its concrete type name — stable since the
// log/slog package was introduced in Go 1.21.
func bridgesToLog(h slog.Handler) bool {
	return h != nil && reflect.TypeOf(h).String() == "*slog.defaultHandler"
}
