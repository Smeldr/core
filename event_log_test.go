package smeldr

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// loggedBroadcaster is a broadcaster with a running event log on a fresh
// SQLite database.
func loggedBroadcaster(t *testing.T) (*eventBroadcaster, *sql.DB) {
	t.Helper()
	db := newSQLiteDB(t)
	if err := CreateEventLogTable(db); err != nil {
		t.Fatal(err)
	}
	b := newEventBroadcaster()
	l, err := newEventLog(db, 0, b.fanout)
	if err != nil {
		t.Fatal(err)
	}
	b.log = l
	go l.run()
	t.Cleanup(l.shutdown)
	return b, db
}

// testEnvelope is a minimal envelope with id and event.
func testEnvelope(id, event string) []byte {
	b, _ := json.Marshal(WebhookEventPayload{ID: id, Event: event, Timestamp: time.Now().UTC(), Data: json.RawMessage(`{}`)})
	return b
}

// waitLogged waits until the log has written seq n.
func waitLogged(t *testing.T, l *eventLog, n int64) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for l.last.Load() < n {
		if time.Now().After(deadline) {
			t.Fatalf("log reached seq %d, want %d", l.last.Load(), n)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// recvID waits for one line on ch and returns its envelope id.
func recvID(t *testing.T, ch chan []byte) string {
	t.Helper()
	select {
	case p := <-ch:
		return envelopeID(p)
	case <-time.After(3 * time.Second):
		t.Fatal("no event delivered")
		return ""
	}
}

// Events from concurrent publishers are logged in one order, before they are
// delivered, and delivered in that order.
func TestEventLog_OrderAndPersistBeforePublish(t *testing.T) {
	b, db := loggedBroadcaster(t)
	ch, err := b.subscribe("u1", eventStreamChannelAll)
	if err != nil {
		t.Fatal(err)
	}
	defer b.unsubscribe(ch)
	const n = 60
	var wg sync.WaitGroup
	for g := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range n / 4 {
				b.publishEvent(eventMeta{id: fmt.Sprintf("e-%d-%02d", g, i), event: "task.created"}, testEnvelope(fmt.Sprintf("e-%d-%02d", g, i), "task.created"))
			}
		}()
	}
	wg.Wait()
	var prev int64
	for range n {
		id := recvID(t, ch)
		var seq int64
		if err := db.QueryRow(`SELECT seq FROM smeldr_events WHERE id = ?`, id).Scan(&seq); err != nil {
			t.Fatalf("%s delivered before it was logged: %v", id, err)
		}
		if seq <= prev {
			t.Fatalf("delivery order: seq %d after %d", seq, prev)
		}
		prev = seq
	}
	if prev != n {
		t.Errorf("last seq = %d, want %d", prev, n)
	}
}

// A failed insert is retried once after re-reading the highest seq (another
// writer took it); a second failure delivers live, unlogged and counted.
func TestEventLog_InsertFailures(t *testing.T) {
	b, db := loggedBroadcaster(t)
	ch, _ := b.subscribe("u1", eventStreamChannelAll)
	defer b.unsubscribe(ch)
	// Another writer took seq 1.
	if _, err := db.Exec(`INSERT INTO smeldr_events (seq, id, created_at, payload) VALUES (1, 'other', ?, '{}')`, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	b.publishEvent(eventMeta{id: "a"}, testEnvelope("a", "x.created"))
	if got := recvID(t, ch); got != "a" {
		t.Fatalf("delivered %q", got)
	}
	var seq int64
	if err := db.QueryRow(`SELECT seq FROM smeldr_events WHERE id = 'a'`).Scan(&seq); err != nil || seq != 2 {
		t.Errorf("retried insert: seq %d, %v; want 2", seq, err)
	}
	if _, err := db.Exec(`CREATE TRIGGER ev_refuse BEFORE INSERT ON smeldr_events BEGIN SELECT RAISE(ABORT, 'no'); END`); err != nil {
		t.Fatal(err)
	}
	b.publishEvent(eventMeta{id: "b"}, testEnvelope("b", "x.created"))
	if got := recvID(t, ch); got != "b" {
		t.Fatalf("delivered %q", got)
	}
	app := &App{eventBroadcaster: b}
	if got := app.UnloggedEvents(); got != 1 {
		t.Errorf("UnloggedEvents = %d, want 1", got)
	}
	if (&App{}).UnloggedEvents() != 0 {
		t.Error("an App without the log counts unlogged events")
	}
}

// A full writer queue delivers live and counts the event.
func TestEventLog_FullQueue(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateEventLogTable(db); err != nil {
		t.Fatal(err)
	}
	var delivered []string
	l, err := newEventLog(db, time.Hour, func(ev streamEvent) { delivered = append(delivered, ev.meta.id) })
	if err != nil {
		t.Fatal(err)
	}
	l.queue = make(chan streamEvent, 1) // no writer running
	l.enqueue(streamEvent{meta: eventMeta{id: "q"}})
	l.enqueue(streamEvent{meta: eventMeta{id: "over"}})
	if len(delivered) != 1 || delivered[0] != "over" || l.unlogged.Load() != 1 {
		t.Errorf("delivered %v unlogged %d; want the overflow live and counted", delivered, l.unlogged.Load())
	}
	l.notLogged(1, "again") // within the minute: counted, not warned again
	if l.unlogged.Load() != 2 {
		t.Errorf("unlogged = %d", l.unlogged.Load())
	}
}

// Events past retention are pruned; younger ones stay.
func TestEventLog_Prune(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateEventLogTable(db); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for i, at := range []time.Time{now.Add(-48 * time.Hour), now} {
		if _, err := db.Exec(`INSERT INTO smeldr_events (seq, id, created_at, payload) VALUES (?, ?, ?, '{}')`, i+1, fmt.Sprint("p", i), at); err != nil {
			t.Fatal(err)
		}
	}
	l, err := newEventLog(db, 24*time.Hour, func(streamEvent) {})
	if err != nil || l.next != 2 {
		t.Fatalf("seeded at %d, %v", l.next, err)
	}
	l.prune(context.Background())
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM smeldr_events`).Scan(&n)
	if n != 1 {
		t.Errorf("rows after prune = %d, want 1", n)
	}
	l.db = failAddressDB{db, "DELETE FROM smeldr_events"}
	l.prune(context.Background()) // logged, not fatal
	if _, err := newEventLog(failAddressDB{db, "MAX(seq)"}, 0, nil); err == nil {
		t.Error("a failed seed did not fail the log")
	}
}

// The writer prunes on its ticker, and shutdown writes what is queued.
func TestEventLog_TickerAndShutdownDrain(t *testing.T) {
	prev := eventLogPruneEvery
	eventLogPruneEvery = 5 * time.Millisecond
	defer func() { eventLogPruneEvery = prev }()
	db := newSQLiteDB(t)
	if err := CreateEventLogTable(db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO smeldr_events (seq, id, created_at, payload) VALUES (1, 'old', ?, '{}')`, time.Now().UTC().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	l, err := newEventLog(db, time.Minute, func(streamEvent) {})
	if err != nil {
		t.Fatal(err)
	}
	go l.run()
	deadline := time.Now().Add(2 * time.Second)
	for {
		var n int
		_ = db.QueryRow(`SELECT COUNT(*) FROM smeldr_events WHERE id = 'old'`).Scan(&n)
		if n == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the ticker never pruned")
		}
		time.Sleep(5 * time.Millisecond)
	}
	// Queue events and stop at once: the drain writes them.
	for i := range 5 {
		l.queue <- streamEvent{meta: eventMeta{id: fmt.Sprint("d", i)}, payload: []byte(`{}`), at: time.Now().UTC()}
	}
	l.shutdown()
	l.shutdown() // twice is safe
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM smeldr_events`).Scan(&n)
	if n != 5 {
		t.Errorf("rows after shutdown = %d, want the 5 queued", n)
	}
}

// eventLogApp is an App with the stream, the log and the orchestration types.
func eventLogApp(t *testing.T) (*App, *sql.DB) {
	t.Helper()
	db := newSQLiteDB(t)
	if err := CreateOrchestrationTables(db); err != nil {
		t.Fatal(err)
	}
	app := New(MustConfig(Config{BaseURL: "https://example.com", Secret: []byte(transitionItemTestSecret), DB: db}))
	RegisterOrchestrationTypes(app, db)
	app.EventStream().EventLog(EventLogOptions{})
	_ = app.Handler()
	t.Cleanup(app.eventBroadcaster.log.shutdown)
	return app, db
}

func TestApp_EventsAfter(t *testing.T) {
	app, _ := eventLogApp(t)
	ctx := context.Background()
	b := app.eventBroadcaster
	var ids []string
	for i := range 3 {
		ids = append(ids, fmt.Sprint("ev", i))
		dispatchEventFrom(ctx, nil, nil, b, "", "core", "task.transitioned",
			transitionWebhookData{Type: "task", ID: fmt.Sprint("t", i), typeName: "Task", ToState: "active"})
	}
	waitLogged(t, b.log, 3)
	page, err := app.EventsAfter(ctx, "", 0)
	if err != nil || len(page.Events) != 3 || page.Skipped || page.Oldest != page.Events[0].ID {
		t.Fatalf("from the start = %+v, %v", page, err)
	}
	if e := page.Events[0]; e.SubjectType != "Task" || e.SubjectID != "t0" || e.Event != "task.transitioned" || len(e.Channels) != 1 || e.CreatedAt.IsZero() {
		t.Errorf("logged event = %+v", e)
	}
	after, _ := app.EventsAfter(ctx, page.Events[0].ID, 1)
	if len(after.Events) != 1 || after.Events[0].ID != page.Events[1].ID {
		t.Errorf("after the first, limit 1 = %+v", after)
	}
	skipped, _ := app.EventsAfter(ctx, "gone", 5000)
	if !skipped.Skipped || len(skipped.Events) != 3 {
		t.Errorf("unknown cursor = %+v, want skipped from the oldest", skipped)
	}
	// A scope that reads nothing gets none of these events.
	hidden, err := app.EventsAfter(WithReadScope(ctx, &ReadScope{}), "", 0)
	if err != nil || len(hidden.Events) != 0 {
		t.Errorf("scoped = %+v, %v; want no events", hidden, err)
	}
	if _, err := (&App{}).EventsAfter(ctx, "", 0); !errors.Is(err, ErrBadRequest) {
		t.Errorf("no log: %v, want ErrBadRequest", err)
	}
	real := b.log.db
	for _, step := range []string{"ORDER BY seq ASC LIMIT 1", "WHERE id = $1", "WHERE seq > $1"} {
		b.log.db = failAddressDB{real, step}
		if _, err := app.EventsAfter(ctx, ids[0], 0); !errors.Is(err, ErrInternal) {
			t.Errorf("%s fails: %v, want ErrInternal", step, err)
		}
	}
	b.log.db = real
}

// The read check: an item event the scope cannot read is not delivered; an
// event about no item is; a relation event needs both ends; a Signal needs
// its subject.
func TestEventReadable(t *testing.T) {
	none := &ReadScope{}
	if !eventReadable(nil, eventMeta{subjectType: "Task", subjectID: "t"}) || !eventReadable(AllReadScope(), eventMeta{subjectType: "Task", subjectID: "t"}) {
		t.Error("an unscoped reader was refused")
	}
	if eventReadable(none, eventMeta{subjectType: "Task", subjectID: "t"}) {
		t.Error("a hidden subject was readable")
	}
	if !eventReadable(none, eventMeta{event: "content_type.redefined"}) {
		t.Error("an event about no item was refused")
	}
	tasks := &ReadScope{anyOfTyp: map[string]bool{"Task": true}}
	if eventReadable(tasks, eventMeta{subjectType: "Task", subjectID: "t", otherType: "Decision", otherID: "d"}) {
		t.Error("a relation with a hidden end was readable")
	}
	if !eventReadable(tasks, eventMeta{subjectType: "Task", subjectID: "t"}) {
		t.Error("a readable subject was refused")
	}
}

// logStreamServer serves the stream over b with the given scope loader.
func logStreamServer(t *testing.T, b *eventBroadcaster, load streamScopeLoader) (*httptest.Server, string) {
	t.Helper()
	srv := httptest.NewServer(newEventStreamHandlerScoped(BearerHMAC(eventStreamTestSecret), b, load))
	t.Cleanup(srv.Close)
	tok, err := SignToken(User{ID: "u1", Roles: []Role{Author}}, eventStreamTestSecret, 0)
	if err != nil {
		t.Fatal(err)
	}
	return srv, tok
}

// streamGet opens the stream with query q and returns the response.
func streamGet(t *testing.T, srv *httptest.Server, tok, q string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"?"+q, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// lineReader returns a function reading the next streamed line.
func lineReader(t *testing.T, resp *http.Response) func() string {
	lines := make(chan string, 64)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	return func() string {
		t.Helper()
		select {
		case l, ok := <-lines:
			if !ok {
				return "<closed>"
			}
			return l
		case <-time.After(3 * time.Second):
			t.Fatal("no line")
			return ""
		}
	}
}

func TestEventStream_ResumeAfter(t *testing.T) {
	b, _ := loggedBroadcaster(t)
	for i := range 3 {
		b.publishEvent(eventMeta{id: fmt.Sprint("r", i), event: "task.created"}, testEnvelope(fmt.Sprint("r", i), "task.created"))
	}
	waitLogged(t, b.log, 3)
	srv, tok := logStreamServer(t, b, nil)

	resp := streamGet(t, srv, tok, "after=r0")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	next := lineReader(t, resp)
	if a, c := envelopeID([]byte(next())), envelopeID([]byte(next())); a != "r1" || c != "r2" {
		t.Fatalf("replayed %q %q, want r1 r2", a, c)
	}
	if got := next(); got != `{"cursor":"r2","type":"caught_up"}` {
		t.Fatalf("caught_up line = %s", got)
	}
	// A live copy of a replayed event is skipped once; a new event comes.
	b.fanout(streamEvent{payload: testEnvelope("r2", "task.created"), meta: eventMeta{id: "r2"}})
	b.publishEvent(eventMeta{id: "r3"}, testEnvelope("r3", "task.created"))
	if got := envelopeID([]byte(next())); got != "r3" {
		t.Fatalf("live = %q, want r3 (the duplicate r2 skipped)", got)
	}

	// An unknown cursor: skipped from the oldest.
	resp2 := streamGet(t, srv, tok, "after=nope")
	next2 := lineReader(t, resp2)
	if got := next2(); !strings.Contains(got, `"type":"skipped"`) || !strings.Contains(got, `"resumed_from":"r0"`) {
		t.Fatalf("skipped line = %s", got)
	}
	if got := envelopeID([]byte(next2())); got != "r0" {
		t.Errorf("first after skipped = %q, want r0", got)
	}
}

// Replay applies the connection's channels, self-skip and scope, and reads
// more than one page.
func TestEventStream_ReplayFiltersAndPages(t *testing.T) {
	b, _ := loggedBroadcaster(t)
	b.publishEvent(eventMeta{id: "own", actorID: "u1"}, testEnvelope("own", "x"))
	b.publishEvent(eventMeta{id: "other-band", channels: []string{"site"}}, testEnvelope("other-band", "x"))
	b.publishEvent(eventMeta{id: "hidden", subjectType: "Task", subjectID: "t"}, testEnvelope("hidden", "x"))
	for i := range eventStreamReplayPage + 2 {
		id := fmt.Sprint("p", i)
		b.publishEvent(eventMeta{id: id, channels: []string{"core"}}, testEnvelope(id, "x"))
	}
	waitLogged(t, b.log, int64(eventStreamReplayPage+5))
	srv, tok := logStreamServer(t, b, func(context.Context, User) (*ReadScope, error) {
		return &ReadScope{anyOfTyp: map[string]bool{"Decision": true}}, nil
	})
	next := lineReader(t, streamGet(t, srv, tok, "after=&channel=core"))
	for i := range eventStreamReplayPage + 2 {
		if got := envelopeID([]byte(next())); got != fmt.Sprint("p", i) {
			t.Fatalf("replayed %q at %d", got, i)
		}
	}
	if got := next(); !strings.Contains(got, "caught_up") {
		t.Fatalf("after the replay: %s", got)
	}
}

func TestEventStream_ResumeErrors(t *testing.T) {
	// No log: 400.
	srv, tok := logStreamServer(t, newEventBroadcaster(), nil)
	if resp := streamGet(t, srv, tok, "after=x"); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("no log: %d, want 400", resp.StatusCode)
	}
	// The scope cannot be loaded: its error.
	bad, tok2 := logStreamServer(t, newEventBroadcaster(), func(context.Context, User) (*ReadScope, error) {
		return nil, ErrInternal
	})
	if resp := streamGet(t, bad, tok2, ""); resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("scope failure: %d, want 500", resp.StatusCode)
	}
	// The first page fails: 500, and the connection slot is freed.
	b, db := loggedBroadcaster(t)
	b.log.db = failAddressDB{db, "ORDER BY seq ASC LIMIT 1"}
	srv3, tok3 := logStreamServer(t, b, nil)
	if resp := streamGet(t, srv3, tok3, "after=x"); resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("first page failure: %d, want 500", resp.StatusCode)
	}
	if b.tokenCount("u1") != 0 {
		t.Error("the failed resume kept its connection slot")
	}
}

// A later replay page that fails closes the connection.
func TestReplayStream_LaterPageFails(t *testing.T) {
	b, db := loggedBroadcaster(t)
	page := EventPage{}
	for i := range eventStreamReplayPage {
		page.Events = append(page.Events, LoggedEvent{Seq: int64(i + 1), ID: fmt.Sprint("x", i), Payload: []byte(`{}`)})
	}
	b.log.db = failAddressDB{db, "WHERE seq > $1"}
	if _, ok := replayStream(context.Background(), b, page, 0, "", eventStreamSub{channels: []string{eventStreamChannelAll}}, func([]byte) bool { return true }); ok {
		t.Error("a failed page did not close")
	}
	fail := func([]byte) bool { return false }
	if _, ok := replayStream(context.Background(), b, EventPage{Skipped: true}, 0, "", eventStreamSub{}, fail); ok {
		t.Error("a failed skipped line did not close")
	}
	if _, ok := replayStream(context.Background(), b, EventPage{Events: page.Events[:1]}, 0, "", eventStreamSub{channels: []string{eventStreamChannelAll}}, fail); ok {
		t.Error("a failed event write did not close")
	}
	if _, ok := replayStream(context.Background(), b, EventPage{}, 0, "", eventStreamSub{}, fail); ok {
		t.Error("a failed caught_up line did not close")
	}
}

// Live delivery applies the connection's scope.
func TestEventStream_LiveScope(t *testing.T) {
	b := newEventBroadcaster()
	srv, tok := logStreamServer(t, b, func(context.Context, User) (*ReadScope, error) { return &ReadScope{}, nil })
	resp := streamGet(t, srv, tok, "")
	next := lineReader(t, resp)
	deadline := time.Now().Add(2 * time.Second)
	for b.count() == 0 && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	b.publishEvent(eventMeta{id: "h", subjectType: "Task", subjectID: "t"}, testEnvelope("h", "task.created"))
	b.publishEvent(eventMeta{id: "v"}, testEnvelope("v", "content_type.redefined"))
	if got := envelopeID([]byte(next())); got != "v" {
		t.Errorf("delivered %q, want only the event about no item", got)
	}
}

// EventLog wires nothing without a DB or a stream, and once only.
func TestApp_EventLogWiring(t *testing.T) {
	a := New(MustConfig(Config{BaseURL: "https://example.com", Secret: []byte(transitionItemTestSecret)}))
	a.EventStream().EventLog(EventLogOptions{})
	_ = a.Handler()
	if a.eventBroadcaster.log != nil {
		t.Error("a log started without a DB")
	}
	app, _ := eventLogApp(t)
	l := app.eventBroadcaster.log
	app.startEventLog()
	if app.eventBroadcaster.log != l {
		t.Error("a second start replaced the log")
	}
	bad := New(MustConfig(Config{BaseURL: "https://example.com", Secret: []byte(transitionItemTestSecret), DB: failAddressDB{newSQLiteDB(t), "CREATE TABLE IF NOT EXISTS smeldr_events"}}))
	bad.EventStream().EventLog(EventLogOptions{})
	bad.startEventLog()
	if bad.eventBroadcaster.log != nil {
		t.Error("a log started without its table")
	}
	seedFail := New(MustConfig(Config{BaseURL: "https://example.com", Secret: []byte(transitionItemTestSecret), DB: failAddressDB{newSQLiteDB(t), "MAX(seq)"}}))
	seedFail.EventStream().EventLog(EventLogOptions{})
	seedFail.startEventLog()
	if seedFail.eventBroadcaster.log != nil {
		t.Error("a log started after a failed seed")
	}
}

// canonicalTypeName maps payload names to registered ones.
func TestApp_CanonicalTypeName(t *testing.T) {
	app, _ := eventLogApp(t)
	if got := app.canonicalTypeName("task"); got != "Task" {
		t.Errorf("task -> %q", got)
	}
	if got := app.canonicalTypeName("Task"); got != "Task" {
		t.Errorf("Task -> %q", got)
	}
	if got := app.canonicalTypeName("nothing"); got != "nothing" {
		t.Errorf("unknown -> %q", got)
	}
	if got := (&App{}).canonicalTypeName("x"); got != "x" {
		t.Errorf("no registry -> %q", got)
	}
}

// A lifecycle event has one envelope: every sink gets the same bytes, so the
// same ID.
func TestSignalEvent_OneEnvelope(t *testing.T) {
	ev := buildSignalEvent(hookCtx(), AfterCreate, afterHookMeta{TypeName: "Post", Prefix: "/posts"}, &testPost{Node: Node{ID: "p1", Slug: "s"}}, "https://example.com")
	a, err := ev.envelopeFor(AfterCreate)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := ev.envelopeFor(AfterCreate)
	if string(a) != string(b) || envelopeID(a) == "" {
		t.Errorf("two envelopes: %s vs %s", a, b)
	}
	bare := SignalEvent{Type: "Post", raw: &testPost{Node: Node{ID: "p1"}}}
	if p, err := bare.envelopeFor(AfterCreate); err != nil || envelopeID(p) == "" {
		t.Errorf("an event without a shared envelope: %s, %v", p, err)
	}
}

// The outbox records the event ID when the table has the column, and still
// takes a job without it.
func TestOutbox_EventID(t *testing.T) {
	pool, _ := outboundTestDB(t)
	ctx := context.Background()
	job := OutboundJob{ID: "j1", EndpointID: "e", TargetURL: "https://x.example", Payload: []byte(`{}`), Event: "x", EventID: "ev1",
		NextRetryAt: time.Now(), CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour), Status: "pending"}
	if err := pool.Enqueue(ctx, job); err != nil {
		t.Fatalf("Enqueue without the column: %v", err)
	}
	if err := EnsureOutboundJobEventIDColumn(ctx, pool.db); err != nil {
		t.Fatal(err)
	}
	if err := EnsureOutboundJobEventIDColumn(ctx, pool.db); err != nil {
		t.Fatalf("second ensure: %v", err)
	}
	job.ID = "j2"
	if err := pool.Enqueue(ctx, job); err != nil {
		t.Fatal(err)
	}
	var got string
	if err := pool.db.QueryRowContext(ctx, `SELECT event_id FROM smeldr_outbound_jobs WHERE id = 'j2'`).Scan(&got); err != nil || got != "ev1" {
		t.Errorf("event_id = %q, %v", got, err)
	}
	if err := EnsureOutboundJobEventIDColumn(ctx, newSQLiteDB(t)); err != nil {
		t.Errorf("no table: %v", err)
	}
}

func TestEnvelopeHelpers(t *testing.T) {
	if envelopeID([]byte(`{"event":"x"}`)) != "" || envelopeID([]byte(`{"id":"unterminated`)) != "" {
		t.Error("envelopeID read a non-envelope")
	}
	if envelopeEvent([]byte(`not json`)) != "" || envelopeEvent(testEnvelope("i", "a.b")) != "a.b" {
		t.Error("envelopeEvent")
	}
	if s, i, o, oi := eventSubject(42); s+i+o+oi != "" {
		t.Error("eventSubject of an unknown data")
	}
	if s, _, o, _ := eventSubject(relationEventData{SourceType: "Task", TargetType: "Goal"}); s != "Task" || o != "Goal" {
		t.Error("eventSubject of a relation")
	}
	if s, _, _, _ := eventSubject(transitionWebhookData{Type: "task"}); s != "task" {
		t.Error("eventSubject falls back to Type")
	}
}

// pagedLogApp is an App whose log holds n Task events t-0..t-(n-1), written
// directly, with no writer running.
func pagedLogApp(t *testing.T, n int) *App {
	t.Helper()
	db := newSQLiteDB(t)
	if err := CreateEventLogTable(db); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	tx, _ := db.Begin()
	for i := range n {
		if _, err := tx.Exec(`INSERT INTO smeldr_events (seq, id, event, subject_type, subject_id, created_at, payload) VALUES (?, ?, 'task.created', 'Task', ?, ?, '{}')`,
			i+1, fmt.Sprint("e-", i), fmt.Sprint("t-", i), now); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	b := newEventBroadcaster()
	l, err := newEventLog(db, 0, b.fanout)
	if err != nil {
		t.Fatal(err)
	}
	b.log = l
	return &App{cfg: Config{DB: db, Secret: []byte(transitionItemTestSecret)}, eventBroadcaster: b}
}

// readsTasks is a scope reading the Tasks with the given IDs.
func readsTasks(ids ...string) *ReadScope {
	m := map[string]bool{}
	for _, id := range ids {
		m[id] = true
	}
	return &ReadScope{items: map[string]map[string]bool{"Task": m}}
}

// pageAll follows Next until More is false, returning every event and the
// number of calls.
func pageAll(t *testing.T, app *App, ctx context.Context, limit int) ([]string, int) {
	t.Helper()
	var got []string
	cursor := ""
	for calls := 1; ; calls++ {
		page, err := app.EventsAfter(ctx, cursor, limit)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range page.Events {
			got = append(got, e.ID)
		}
		if !page.More {
			return got, calls
		}
		if page.Next == "" || page.Next == cursor {
			t.Fatalf("call %d: More with no progress (next %q)", calls, page.Next)
		}
		cursor = page.Next
		if calls > 100 {
			t.Fatal("paging does not end")
		}
	}
}

// A scope that reads one event in 1,200 reaches it, paging with limit 500,
// and with a small limit that hits the scan bound; no Next names a hidden
// event.
func TestEventsAfter_PagesPastHiddenEvents(t *testing.T) {
	app := pagedLogApp(t, 1200)
	ctx := WithReadScope(context.Background(), readsTasks("t-700"))
	if got, calls := pageAll(t, app, ctx, 500); len(got) != 1 || got[0] != "e-700" || calls != 1 {
		t.Errorf("limit 500: %v in %d calls, want e-700 in one call (the bound is 5,000 rows)", got, calls)
	}
	got, calls := pageAll(t, app, ctx, 50)
	if len(got) != 1 || got[0] != "e-700" || calls != 3 {
		t.Errorf("limit 50: %v in %d calls, want e-700 in three (500 rows per call)", got, calls)
	}
	first, _ := app.EventsAfter(ctx, "", 50)
	if len(first.Events) != 0 || !first.More || !strings.HasPrefix(first.Next, eventContinuationPrefix) {
		t.Errorf("first page = %+v, want empty, more, and a continuation", first)
	}
	if strings.Contains(first.Next, "e-") {
		t.Errorf("the continuation names an event: %q", first.Next)
	}
}

// An all-hidden log returns an empty page that says the log ended; a short
// readable page in the middle does not claim the end.
func TestEventsAfter_EndAndShortPages(t *testing.T) {
	app := pagedLogApp(t, 1200)
	ctx := context.Background()
	none, err := app.EventsAfter(WithReadScope(ctx, &ReadScope{}), "", 500)
	if err != nil || len(none.Events) != 0 || none.More {
		t.Errorf("all hidden = %d events, more %v, %v; want an empty page at the end", len(none.Events), none.More, err)
	}
	var some []string
	for i := 0; i < 1200; i += 100 {
		some = append(some, fmt.Sprint("t-", i))
	}
	short, _ := app.EventsAfter(WithReadScope(ctx, readsTasks(some...)), "", 50)
	if len(short.Events) != 5 || !short.More {
		t.Errorf("short page = %d events, more %v; want 5 and more (the bound, not the end)", len(short.Events), short.More)
	}
	all, _ := app.EventsAfter(ctx, "", 1000)
	if len(all.Events) != 1000 || !all.More || all.Next != "e-999" {
		t.Errorf("unscoped = %d events, more %v, next %q", len(all.Events), all.More, all.Next)
	}
	rest, _ := app.EventsAfter(ctx, all.Next, 1000)
	if len(rest.Events) != 200 || rest.More || rest.Next != "e-1199" {
		t.Errorf("rest = %d events, more %v, next %q", len(rest.Events), rest.More, rest.Next)
	}
	empty, _ := app.EventsAfter(ctx, rest.Next, 10)
	if len(empty.Events) != 0 || empty.More || empty.Next != "" {
		t.Errorf("past the end = %+v", empty)
	}
}

// A continuation is opaque and checked: a forged or truncated one is refused.
func TestEventsAfter_Continuation(t *testing.T) {
	app := pagedLogApp(t, 3)
	c := app.sealEventContinuation(2)
	if seq, ok, err := app.openEventContinuation(c); !ok || err != nil || seq != 2 {
		t.Fatalf("open = %d %v %v", seq, ok, err)
	}
	if c == app.sealEventContinuation(2) {
		t.Error("two continuations for one position are equal")
	}
	page, err := app.EventsAfter(context.Background(), c, 10)
	if err != nil || len(page.Events) != 1 || page.Events[0].ID != "e-2" {
		t.Errorf("after a continuation = %+v, %v", page, err)
	}
	for _, bad := range []string{"~", "~!!", "~" + strings.Repeat("A", 40), c[:len(c)-2]} {
		if _, err := app.EventsAfter(context.Background(), bad, 10); !errors.Is(err, ErrBadRequest) {
			t.Errorf("%q: %v, want ErrBadRequest", bad, err)
		}
	}
	other := &App{cfg: Config{Secret: []byte("another secret, another key!!!!")}}
	if _, _, err := other.openEventContinuation(c); !errors.Is(err, ErrBadRequest) {
		t.Errorf("another instance opened it: %v", err)
	}
}
