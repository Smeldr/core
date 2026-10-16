package smeldr

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// DefaultEventLogRetention is how long the event log keeps an event when
// [EventLogOptions.Retention] is zero: 30 days (D117).
const DefaultEventLogRetention = 30 * 24 * time.Hour

// eventLogQueue is the capacity of the writer's queue. A publish that finds it
// full is delivered live without the log and counted ([App.UnloggedEvents]).
const eventLogQueue = 1024

// eventLogBatch is the most events the writer inserts in one transaction.
const eventLogBatch = 100

// eventLogPruneEvery is how often the writer deletes events past retention.
var eventLogPruneEvery = time.Hour

// eventLogDrainTimeout bounds how long shutdown waits for queued events to be
// written before the stream closes.
var eventLogDrainTimeout = 2 * time.Second

// EventLogOptions configures [App.EventLog].
type EventLogOptions struct {
	// Retention is how long an event is kept. Zero or less means
	// [DefaultEventLogRetention].
	Retention time.Duration
}

// LoggedEvent is one event of the durable event log (D117): the envelope that
// was delivered, and what it was about.
type LoggedEvent struct {
	// Seq is the event's place in the log: delivery order.
	Seq int64 `json:"seq"`
	// ID is the envelope ID, the cursor a client resumes after.
	ID string `json:"id"`
	// Event is the event name, such as "task.transitioned".
	Event string `json:"event"`
	// Channels are the stream channels the event was published on; empty
	// means a broadcast.
	Channels []string `json:"channels"`
	// ActorID is the token that caused the event, empty for the system.
	ActorID string `json:"actor_id"`
	// SubjectType and SubjectID name the item the event is about.
	SubjectType string `json:"subject_type"`
	SubjectID   string `json:"subject_id"`
	// OtherType and OtherID name a second item the event reveals: the other
	// end of a relation event, or a Signal's subject.
	OtherType string `json:"other_type"`
	OtherID   string `json:"other_id"`
	// CreatedAt is when the event was logged.
	CreatedAt time.Time `json:"created_at"`
	// FromState, ToState, SignalType and Fields are what subscription filters
	// match (D116): a transition's states, a Signal's type, and the routing
	// values (band, scope, receiver, sender, priority, a dynamic type's channel
	// field).
	FromState  string            `json:"from_state,omitempty"`
	ToState    string            `json:"to_state,omitempty"`
	SignalType string            `json:"signal_type,omitempty"`
	Fields     map[string]string `json:"fields,omitempty"`
	// Payload is the envelope exactly as delivered.
	Payload json.RawMessage `json:"payload"`
}

// EventPage is one page of [App.EventsAfter].
type EventPage struct {
	// Events are the events after the cursor, in order.
	Events []LoggedEvent `json:"events"`
	// Skipped is true when the cursor was not in the log (older than the
	// retention, or never logged): the page starts at the oldest kept event.
	Skipped bool `json:"skipped"`
	// Oldest is the ID of the oldest kept event, empty when the log is empty.
	Oldest string `json:"oldest"`
	// Next is the cursor to pass as afterID for the following page: the ID of
	// the last event returned, or, when the read ended on events the caller
	// may not read, an opaque continuation that names no event. Empty only
	// when the page read nothing at all.
	Next string `json:"next"`
	// More is true when the log may hold events after Next; false when the
	// read reached the end of the log.
	More bool `json:"more"`
}

// eventMeta is what the stream and the log know about one event besides its
// envelope: where it is published and which items it reveals.
type eventMeta struct {
	id, event   string
	channels    []string
	actorID     string
	subjectType string
	subjectID   string
	otherType   string
	otherID     string
	// fromState and toState are a transition's states (toState also the
	// state a created item starts in); signalType a Signal's type; fields its
	// routing values (band, scope, receiver, sender, priority, a dynamic
	// type's channel field), which subscription filters match (D116).
	fromState, toState string
	signalType         string
	fields             map[string]string
}

// streamEvent is one event on its way to the log and the connections.
type streamEvent struct {
	seq     int64
	payload []byte
	meta    eventMeta
	at      time.Time
}

// eventLog is the durable event log behind the stream: one writer goroutine
// assigns the order, inserts, and only then fans the events out, so a client
// resuming with a cursor never has seen an event the log lacks.
type eventLog struct {
	db        DB
	retention time.Duration
	queue     chan streamEvent
	next      int64 // the last seq assigned; owned by the writer
	last      atomic.Int64
	unlogged  atomic.Int64
	warnedAt  atomic.Int64 // unix seconds of the last "not logged" warning
	stop      chan struct{}
	done      chan struct{}
	stopOnce  sync.Once
	fanout    func(streamEvent)
}

// CreateEventLogTable creates smeldr_events, the durable event log (D117), and
// its index on created_at, if they do not exist. Idempotent.
func CreateEventLogTable(db DB) error {
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS smeldr_events (
			seq          BIGINT PRIMARY KEY,
			id           TEXT NOT NULL UNIQUE,
			event        TEXT NOT NULL DEFAULT '',
			channels     TEXT NOT NULL DEFAULT '',
			actor_id     TEXT NOT NULL DEFAULT '',
			subject_type TEXT NOT NULL DEFAULT '',
			subject_id   TEXT NOT NULL DEFAULT '',
			other_type   TEXT NOT NULL DEFAULT '',
			other_id     TEXT NOT NULL DEFAULT '',
			created_at   TIMESTAMPTZ NOT NULL,
			payload      TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_smeldr_events_created_at ON smeldr_events (created_at)`,
	} {
		if _, err := db.ExecContext(context.Background(), stmt); err != nil {
			return fmt.Errorf("smeldr: CreateEventLogTable: %w", err)
		}
	}
	// D116: what subscription filters match, so a replay filters as live
	// delivery does. Added to a table created by v1.160.0.
	for _, c := range [][2]string{
		{"from_state", "TEXT NOT NULL DEFAULT ''"},
		{"to_state", "TEXT NOT NULL DEFAULT ''"},
		{"signal_type", "TEXT NOT NULL DEFAULT ''"},
		{"fields", "TEXT NOT NULL DEFAULT ''"},
	} {
		if err := EnsureColumn(context.Background(), db, "smeldr_events", c[0], c[1]); err != nil {
			return fmt.Errorf("smeldr: CreateEventLogTable: %w", err)
		}
	}
	return nil
}

// newEventLog returns a log on db, seeded from the highest seq stored.
func newEventLog(db DB, retention time.Duration, fanout func(streamEvent)) (*eventLog, error) {
	if retention <= 0 {
		retention = DefaultEventLogRetention
	}
	l := &eventLog{db: db, retention: retention, queue: make(chan streamEvent, eventLogQueue),
		stop: make(chan struct{}), done: make(chan struct{}), fanout: fanout}
	if err := l.seed(context.Background()); err != nil {
		return nil, err
	}
	return l, nil
}

// seed sets the counter to the highest stored seq.
func (l *eventLog) seed(ctx context.Context) error {
	var maxSeq sql.NullInt64
	if err := l.db.QueryRowContext(ctx, `SELECT MAX(seq) FROM smeldr_events`).Scan(&maxSeq); err != nil {
		return fmt.Errorf("smeldr: event log: %w", err)
	}
	l.next = maxSeq.Int64
	l.last.Store(maxSeq.Int64)
	return nil
}

// enqueue hands ev to the writer. A full queue delivers it live without the
// log, and counts it.
func (l *eventLog) enqueue(ev streamEvent) {
	select {
	case l.queue <- ev:
	default:
		l.notLogged(1, "the writer queue is full")
		l.fanout(ev)
	}
}

// notLogged counts n events delivered without the log, with a Warn at most
// once a minute.
func (l *eventLog) notLogged(n int, why string) {
	total := l.unlogged.Add(int64(n))
	now := time.Now().Unix()
	if prev := l.warnedAt.Load(); now-prev >= 60 && l.warnedAt.CompareAndSwap(prev, now) {
		slog.Warn("smeldr: event log: events delivered live but not logged, so they cannot be replayed",
			"reason", why, "unlogged_total", total)
	}
}

// run is the writer loop: batches from the queue are inserted in order, then
// fanned out; events past retention are pruned on a ticker. It returns when
// stop is closed, after draining the queue.
func (l *eventLog) run() {
	defer close(l.done)
	prune := time.NewTicker(eventLogPruneEvery)
	defer prune.Stop()
	for {
		select {
		case ev := <-l.queue:
			l.writeBatch(l.collect(ev))
		case <-prune.C:
			l.prune(context.Background())
		case <-l.stop:
			l.drain()
			return
		}
	}
}

// collect returns first plus whatever else is queued, up to eventLogBatch.
func (l *eventLog) collect(first streamEvent) []streamEvent {
	batch := []streamEvent{first}
	for len(batch) < eventLogBatch {
		select {
		case ev := <-l.queue:
			batch = append(batch, ev)
		default:
			return batch
		}
	}
	return batch
}

// drain writes what is still queued, bounded by eventLogDrainTimeout.
func (l *eventLog) drain() {
	deadline := time.After(eventLogDrainTimeout)
	for {
		select {
		case ev := <-l.queue:
			l.writeBatch(l.collect(ev))
		case <-deadline:
			return
		default:
			return
		}
	}
}

// writeBatch assigns seqs, inserts the batch in one transaction and fans it
// out. A failed insert is retried once after re-reading the highest seq (a
// second writer on the same database); a second failure delivers the batch
// live without the log and counts it.
func (l *eventLog) writeBatch(batch []streamEvent) {
	ctx := context.Background()
	err := l.insert(ctx, batch)
	if err != nil {
		if serr := l.seed(ctx); serr == nil {
			err = l.insert(ctx, batch)
		}
	}
	if err != nil {
		slog.Warn("smeldr: event log: insert failed; the events are delivered live only", "events", len(batch), "error", err)
		l.notLogged(len(batch), "the insert failed")
		for _, ev := range batch {
			ev.seq = 0
			l.fanout(ev)
		}
		return
	}
	l.last.Store(batch[len(batch)-1].seq)
	for _, ev := range batch {
		l.fanout(ev)
	}
}

// insert writes batch with consecutive seqs after l.next. On failure l.next is
// left as it was and the seqs are cleared.
func (l *eventLog) insert(ctx context.Context, batch []streamEvent) error {
	exec, commit, rollback, err := conflictTx(ctx, l.db)
	if err != nil {
		return err
	}
	defer rollback()
	next := l.next
	for i := range batch {
		next++
		batch[i].seq = next
		ev := batch[i]
		if _, err := exec.ExecContext(ctx,
			`INSERT INTO smeldr_events (seq, id, event, channels, actor_id, subject_type, subject_id, other_type, other_id, created_at, payload,
			  from_state, to_state, signal_type, fields)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)`,
			ev.seq, ev.meta.id, ev.meta.event, strings.Join(ev.meta.channels, ","), ev.meta.actorID,
			ev.meta.subjectType, ev.meta.subjectID, ev.meta.otherType, ev.meta.otherID, ev.at, string(ev.payload),
			ev.meta.fromState, ev.meta.toState, ev.meta.signalType, encodeEventFields(ev.meta.fields)); err != nil {
			for j := range batch {
				batch[j].seq = 0
			}
			return err
		}
	}
	if err := commit(); err != nil {
		for j := range batch {
			batch[j].seq = 0
		}
		return err
	}
	l.next = next
	return nil
}

// prune deletes events older than the retention.
func (l *eventLog) prune(ctx context.Context) {
	res, err := l.db.ExecContext(ctx, `DELETE FROM smeldr_events WHERE created_at < $1`, time.Now().UTC().Add(-l.retention))
	if err != nil {
		slog.Warn("smeldr: event log: prune failed", "error", err)
		return
	}
	if n, err := res.RowsAffected(); err == nil && n > 0 {
		slog.Debug("smeldr: event log: pruned events past retention", "events", n, "retention", l.retention.String())
	}
}

// shutdown stops the writer after it has drained, waiting at most
// eventLogDrainTimeout plus a margin.
func (l *eventLog) shutdown() {
	l.stopOnce.Do(func() { close(l.stop) })
	select {
	case <-l.done:
	case <-time.After(eventLogDrainTimeout + time.Second):
	}
}

// page reads up to limit events after the cursor afterID, in order.
func (l *eventLog) page(ctx context.Context, afterID string, limit int) (EventPage, error) {
	page, after, err := l.cursor(ctx, afterID)
	if err != nil {
		return page, err
	}
	events, err := l.after(ctx, after, limit)
	if err != nil {
		return page, err
	}
	page.Events = events
	return page, nil
}

// cursor resolves the envelope ID afterID to the seq to read after, with the
// page's Oldest and Skipped set: an empty afterID is the start of the log, one
// the log does not hold is the start with Skipped.
func (l *eventLog) cursor(ctx context.Context, afterID string) (EventPage, int64, error) {
	var page EventPage
	if err := l.db.QueryRowContext(ctx, `SELECT id FROM smeldr_events ORDER BY seq ASC LIMIT 1`).Scan(&page.Oldest); err != nil && err != sql.ErrNoRows {
		return page, 0, fmt.Errorf("%w: event log: %s", ErrInternal, err)
	}
	var after int64
	if afterID != "" {
		err := l.db.QueryRowContext(ctx, `SELECT seq FROM smeldr_events WHERE id = $1`, afterID).Scan(&after)
		switch {
		case err == sql.ErrNoRows:
			page.Skipped = true
		case err != nil:
			return page, 0, fmt.Errorf("%w: event log: %s", ErrInternal, err)
		}
	}
	return page, after, nil
}

// after reads up to limit events with seq above after, in order.
func (l *eventLog) after(ctx context.Context, after int64, limit int) ([]LoggedEvent, error) {
	rows, err := l.db.QueryContext(ctx,
		`SELECT seq, id, event, channels, actor_id, subject_type, subject_id, other_type, other_id, created_at, payload,
		  from_state, to_state, signal_type, fields
		 FROM smeldr_events WHERE seq > $1 ORDER BY seq ASC LIMIT $2`, after, limit)
	if err != nil {
		return nil, fmt.Errorf("%w: event log: %s", ErrInternal, err)
	}
	defer rows.Close()
	var out []LoggedEvent
	for rows.Next() {
		var e LoggedEvent
		var channels, payload, fields string
		var at any
		if err := rows.Scan(&e.Seq, &e.ID, &e.Event, &channels, &e.ActorID, &e.SubjectType, &e.SubjectID,
			&e.OtherType, &e.OtherID, &at, &payload, &e.FromState, &e.ToState, &e.SignalType, &fields); err != nil {
			return nil, fmt.Errorf("%w: event log: %s", ErrInternal, err)
		}
		e.Fields = decodeEventFields(fields)
		if t, err := ParseTimestamp(at); err == nil {
			e.CreatedAt = t
		}
		if channels != "" {
			e.Channels = strings.Split(channels, ",")
		}
		e.Payload = json.RawMessage(payload)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%w: event log: %s", ErrInternal, err)
	}
	return out, nil
}

// meta returns the event's routing and subject, for a delivery check.
func (e LoggedEvent) meta() eventMeta {
	return eventMeta{id: e.ID, event: e.Event, channels: e.Channels, actorID: e.ActorID,
		subjectType: e.SubjectType, subjectID: e.SubjectID, otherType: e.OtherType, otherID: e.OtherID,
		fromState: e.FromState, toState: e.ToState, signalType: e.SignalType, fields: e.Fields}
}

// eventReadable reports whether scope reads every item meta reveals. A nil
// scope, or one that reads everything, reads all; an event about no item
// (a type schema change) is readable.
func eventReadable(scope *ReadScope, meta eventMeta) bool {
	if scope == nil || scope.All() {
		return true
	}
	if meta.subjectType != "" && !scope.Readable(meta.subjectType, meta.subjectID) {
		return false
	}
	if meta.otherType != "" && !scope.Readable(meta.otherType, meta.otherID) {
		return false
	}
	return true
}

// EventLog makes the event stream durable (D117): every event is written to
// smeldr_events, in one order, before it is delivered, and kept for
// opts.Retention (30 days by default). A stream connection that gives
// ?after=<event id> receives what it missed, in order, before live events, and
// [App.EventsAfter] reads the log from Go. Delivery becomes at least once:
// deduplicate by the envelope ID.
//
// It needs [Config.DB] and [App.EventStream]; the table is created and the one
// writer started by [App.Handler]. One process writes the log: a second
// process on the same database is not supported. An event that cannot be
// logged (the insert failed, or the writer queue was full) is still delivered
// live and counted by [App.UnloggedEvents]. Without this call the stream is in
// memory and live only, as before.
func (a *App) EventLog(opts EventLogOptions) *App {
	a.eventLogOpts = &opts
	return a
}

// startEventLog creates the table and starts the writer, once, when
// [App.EventLog] and [App.EventStream] are both wired and the App has a DB.
func (a *App) startEventLog() {
	if a.eventLogOpts == nil || a.eventBroadcaster == nil || a.cfg.DB == nil || a.eventBroadcaster.log != nil {
		return
	}
	if err := CreateEventLogTable(a.cfg.DB); err != nil {
		slog.Error("smeldr: event log not started: the stream stays live only", "error", err)
		return
	}
	l, err := newEventLog(a.cfg.DB, a.eventLogOpts.Retention, a.eventBroadcaster.fanout)
	if err != nil {
		slog.Error("smeldr: event log not started: the stream stays live only", "error", err)
		return
	}
	a.eventBroadcaster.log = l
	go l.run()
}

// UnloggedEvents is how many events were delivered live but not written to
// the event log since the process started: their insert failed, or the
// writer's queue was full. Such events cannot be replayed with a cursor, so a
// non-zero count is a gap in the at-least-once promise. Zero without
// [App.EventLog].
func (a *App) UnloggedEvents() int64 {
	if a.eventBroadcaster == nil || a.eventBroadcaster.log == nil {
		return 0
	}
	return a.eventBroadcaster.log.unlogged.Load()
}

// eventsAfterScanFactor bounds how many log rows one [App.EventsAfter] call
// reads, as a multiple of its limit, so a caller whose scope reads few events
// costs a bounded read per call.
const eventsAfterScanFactor = 10

// EventsAfter reads the event log after the cursor afterID, in order, up to
// limit events (0 or less means 500, more than 1000 is 1000). afterID is an
// envelope ID or a page's Next. An empty afterID starts at the oldest kept
// event; an envelope ID the log does not hold (older than the retention, or
// unknown) also starts there, with Skipped set.
//
// Under governance the page holds only events whose items the caller in ctx
// may read: the read scope attached to ctx, or the caller's own, by the same
// rule the stream applies at delivery. Hidden events are skipped without a
// trace: the read goes on past them until it has limit readable events, the
// log ends, or it has read 10 x limit rows. Page through with Next until More
// is false. Next never names a hidden event: when the read ended on hidden
// events it is an opaque continuation, so a page can be empty with More true
// when the bound was reached first. Pass Next back as it is.
//
// [ErrBadRequest] when the log is not wired ([App.EventLog]) or afterID is a
// continuation this instance did not issue; [ErrInternal] on a query failure.
func (a *App) EventsAfter(ctx context.Context, afterID string, limit int) (EventPage, error) {
	if a.eventBroadcaster == nil || a.eventBroadcaster.log == nil {
		return EventPage{}, fmt.Errorf("%w: the event log is not enabled", ErrBadRequest)
	}
	switch {
	case limit <= 0:
		limit = 500
	case limit > 1000:
		limit = 1000
	}
	scope, err := scopeFor(ctx, a.governance)
	if err != nil {
		return EventPage{}, err
	}
	l := a.eventBroadcaster.log
	var page EventPage
	var after int64
	if seq, ok, cerr := a.openEventContinuation(afterID); cerr != nil {
		return EventPage{}, cerr
	} else if ok {
		page, _, err = l.cursor(ctx, "")
		after = seq
	} else {
		page, after, err = l.cursor(ctx, afterID)
	}
	if err != nil {
		return EventPage{}, err
	}
	page.Events = []LoggedEvent{}
	bound := eventsAfterScanFactor * limit
	scanned, ended, lastHidden := 0, false, false
	for len(page.Events) < limit && scanned < bound {
		want := min(limit, bound-scanned)
		rows, err := l.after(ctx, after, want)
		if err != nil {
			return EventPage{}, err
		}
		consumed := 0
		for _, e := range rows {
			consumed++
			scanned++
			after = e.Seq
			if !eventReadable(scope, e.meta()) {
				lastHidden = true
				continue
			}
			lastHidden = false
			page.Events = append(page.Events, e)
			if len(page.Events) == limit {
				break
			}
		}
		// A short read that was read to its end is the end of the log.
		if len(rows) < want && consumed == len(rows) {
			ended = true
			break
		}
	}
	page.More = !ended
	switch {
	case scanned == 0:
	case lastHidden:
		page.Next = a.sealEventContinuation(after)
	default:
		page.Next = page.Events[len(page.Events)-1].ID
	}
	return page, nil
}

// envelopeID reads the "id" of an envelope written by [WebhookEventPayload]'s
// marshalling (its first field) without decoding the rest. Empty when payload
// does not start that way.
func envelopeID(payload []byte) string {
	const prefix = `{"id":"`
	if !bytes.HasPrefix(payload, []byte(prefix)) {
		return ""
	}
	rest := payload[len(prefix):]
	if i := bytes.IndexByte(rest, '"'); i >= 0 {
		return string(rest[:i])
	}
	return ""
}

// envelopeEvent reads the "event" of an envelope. Empty when payload is not
// one.
func envelopeEvent(payload []byte) string {
	var env struct {
		Event string `json:"event"`
	}
	if json.Unmarshal(payload, &env) != nil {
		return ""
	}
	return env.Event
}

// streamScope is the read scope of a stream connection's owner: nil (all)
// without governance, else the user's loaded scope.
func (a *App) streamScope(ctx context.Context, user User) (*ReadScope, error) {
	if a.governance == nil {
		return nil, nil
	}
	return a.cachedScope(ctx, user.ID)
}

// canonicalTypeName maps an event's type name, which payloads carry in lower
// case, to the registered name the read rules use ("task" to "Task"). A name
// the registry does not know is returned as given.
func (a *App) canonicalTypeName(name string) string {
	if a.typeRegistry == nil || a.typeRegistry.Lookup(name) != nil {
		return name
	}
	for _, d := range a.typeRegistry.All() {
		if strings.EqualFold(d.Name, name) {
			return d.Name
		}
	}
	return name
}

// eventSubject returns the items an event's data reveals: its subject, and a
// second item (the other end of a relation, or a Signal's subject).
func eventSubject(data any) (subjectType, subjectID, otherType, otherID string) {
	switch d := data.(type) {
	case transitionWebhookData:
		subjectType = d.typeName
		if subjectType == "" {
			subjectType = d.Type
		}
		return subjectType, d.ID, d.aboutType, d.aboutID
	case relationEventData:
		return d.SourceType, d.SourceID, d.TargetType, d.TargetID
	}
	return "", "", "", ""
}

// eventContinuationPrefix starts a sealed continuation cursor, which an
// envelope ID (hex with dashes) never does.
const eventContinuationPrefix = "~"

// eventCursorKey is the AES-256 key of continuation cursors, derived from the
// App's secret.
func (a *App) eventCursorKey() []byte {
	sum := sha256.Sum256(append([]byte("smeldr event cursor\x00"), a.cfg.Secret...))
	return sum[:]
}

// sealEventContinuation returns an opaque cursor for the log position seq:
// sealed with AES-GCM, so it names no event and reveals no count.
func (a *App) sealEventContinuation(seq int64) string {
	block, _ := aes.NewCipher(a.eventCursorKey())
	gcm, _ := cipher.NewGCM(block)
	nonce := make([]byte, gcm.NonceSize())
	_, _ = io.ReadFull(rand.Reader, nonce)
	var plain [8]byte
	binary.BigEndian.PutUint64(plain[:], uint64(seq))
	return eventContinuationPrefix + base64.RawURLEncoding.EncodeToString(gcm.Seal(nonce, nonce, plain[:], nil))
}

// openEventContinuation reads a cursor made by [App.sealEventContinuation]:
// ok is false for an envelope ID (anything without the prefix), and a
// continuation that does not open is [ErrBadRequest].
func (a *App) openEventContinuation(cursor string) (seq int64, ok bool, err error) {
	if !strings.HasPrefix(cursor, eventContinuationPrefix) {
		return 0, false, nil
	}
	bad := fmt.Errorf("%w: the cursor is not a continuation this instance issued", ErrBadRequest)
	raw, derr := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(cursor, eventContinuationPrefix))
	block, _ := aes.NewCipher(a.eventCursorKey())
	gcm, _ := cipher.NewGCM(block)
	if derr != nil || len(raw) < gcm.NonceSize() {
		return 0, false, bad
	}
	plain, oerr := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	if oerr != nil || len(plain) != 8 {
		return 0, false, bad
	}
	return int64(binary.BigEndian.Uint64(plain)), true, nil
}

// encodeEventFields stores routing values as JSON (” for none).
func encodeEventFields(f map[string]string) string {
	if len(f) == 0 {
		return ""
	}
	b, _ := json.Marshal(f)
	return string(b)
}

// decodeEventFields reads what encodeEventFields stored.
func decodeEventFields(s string) map[string]string {
	if s == "" {
		return nil
	}
	var f map[string]string
	_ = json.Unmarshal([]byte(s), &f)
	return f
}
