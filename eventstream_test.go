package smeldr

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// — eventBroadcaster — ————————————————————————————————————————————————————

func TestEventBroadcaster_SubscribeReceivesBroadcast(t *testing.T) {
	b := newEventBroadcaster()
	ch, err := b.subscribe("u1")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer b.unsubscribe(ch)

	b.broadcast([]byte(`{"event":"x"}`))

	select {
	case got := <-ch:
		if string(got) != `{"event":"x"}` {
			t.Errorf("got %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for broadcast")
	}
}

func TestEventBroadcaster_MultipleSubscribersAllReceive(t *testing.T) {
	b := newEventBroadcaster()
	var chans []chan []byte
	for i := 0; i < 3; i++ {
		// distinct token per subscriber — this test exercises fan-out
		// across subscribers, not the per-token cap.
		ch, err := b.subscribe(fmt.Sprintf("u%d", i))
		if err != nil {
			t.Fatalf("subscribe: %v", err)
		}
		chans = append(chans, ch)
		defer b.unsubscribe(ch)
	}

	b.broadcast([]byte("event"))

	for i, ch := range chans {
		select {
		case <-ch:
		case <-time.After(time.Second):
			t.Fatalf("subscriber %d: timed out waiting for broadcast", i)
		}
	}
}

func TestEventBroadcaster_UnsubscribeStopsDelivery(t *testing.T) {
	b := newEventBroadcaster()
	ch, err := b.subscribe("u1")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	b.unsubscribe(ch)

	if got := b.count(); got != 0 {
		t.Fatalf("count = %d, want 0", got)
	}
	if _, ok := <-ch; ok {
		t.Fatal("expected channel closed after unsubscribe")
	}
	// Broadcasting after every subscriber has unsubscribed must not panic.
	b.broadcast([]byte("x"))
}

func TestEventBroadcaster_UnsubscribeUnknownChannel(t *testing.T) {
	b := newEventBroadcaster()
	ch := make(chan []byte, 1)
	// Never subscribed — must be a defensive no-op, not a panic.
	b.unsubscribe(ch)
}

func TestEventBroadcaster_BroadcastNoSubscribers(t *testing.T) {
	b := newEventBroadcaster()
	b.broadcast([]byte("x")) // no-op, must not panic
}

func TestEventBroadcaster_FullBufferDropsWithoutBlocking(t *testing.T) {
	// Deliberately single-goroutine and lockstep, not a background drainer
	// racing the broadcast loop: an earlier version drained `fast` from a
	// separate goroutine, which is exactly the kind of race that passes on
	// a fast local machine and flakes on a loaded CI runner — `fast`'s own
	// buffer could fill before the drainer got scheduled, undermining the
	// very thing this test exists to prove. Draining `fast` synchronously,
	// once per broadcast, in the same goroutine that calls broadcast,
	// removes the race entirely while still proving the same two things:
	// broadcast doesn't block on `slow`'s full buffer, and `fast` — a
	// healthy sibling — is unaffected by it.
	b := newEventBroadcaster()

	slow, err := b.subscribe("u-slow") // deliberately never drained
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer b.unsubscribe(slow)

	fast, err := b.subscribe("u-fast")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer b.unsubscribe(fast)

	const n = eventStreamSubscriberBuffer + 5 // deliberately overflows slow's buffer

	start := time.Now()
	for i := 0; i < n; i++ {
		b.broadcast([]byte("x"))
		select {
		case <-fast:
		case <-time.After(time.Second):
			t.Fatalf("fast subscriber did not receive broadcast %d/%d — a wedged sibling must not affect it", i+1, n)
		}
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("broadcast took %v for %d calls despite a full subscriber buffer — looks blocked", elapsed, n)
	}

	if got := len(slow); got != eventStreamSubscriberBuffer {
		t.Errorf("slow subscriber buffer len = %d, want %d (full, overflow dropped)", got, eventStreamSubscriberBuffer)
	}
}

func TestEventBroadcaster_ConcurrentSubscribeBroadcastUnsubscribe(t *testing.T) {
	b := newEventBroadcaster()
	var wg sync.WaitGroup
	stop := make(chan struct{})

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					b.broadcast([]byte("x"))
				}
			}
		}()
	}

	for i := 0; i < 4; i++ {
		// Distinct token per goroutine — this test's own purpose is proving
		// no deadlock/panic under concurrent subscribe/broadcast/unsubscribe,
		// not exercising the per-token cap (covered separately); a shared
		// token here would make occasional ErrTooManyRequests rejections a
		// race in the test itself, not a signal about the code under test.
		tokenID := fmt.Sprintf("concurrent-%d", i)
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					ch, err := b.subscribe(tokenID)
					if err != nil {
						continue
					}
					for j := 0; j < 3; j++ {
						select {
						case <-ch:
						case <-time.After(10 * time.Millisecond):
						}
					}
					b.unsubscribe(ch)
				}
			}
		}()
	}

	time.Sleep(200 * time.Millisecond)
	close(stop)

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("goroutines did not finish — possible deadlock")
	}
}

func TestEventBroadcaster_SubscribePerTokenCapRejectsPastLimit(t *testing.T) {
	b := newEventBroadcaster()
	var chans []chan []byte
	for i := 0; i < eventStreamMaxSubscribersPerToken; i++ {
		ch, err := b.subscribe("u1")
		if err != nil {
			t.Fatalf("subscribe %d/%d: %v", i+1, eventStreamMaxSubscribersPerToken, err)
		}
		chans = append(chans, ch)
	}
	defer func() {
		for _, ch := range chans {
			b.unsubscribe(ch)
		}
	}()

	if _, err := b.subscribe("u1"); !errors.Is(err, ErrTooManyRequests) {
		t.Fatalf("subscribe past cap: err = %v, want ErrTooManyRequests", err)
	}
	if got := b.tokenCount("u1"); got != eventStreamMaxSubscribersPerToken {
		t.Errorf("tokenCount(u1) = %d, want %d (rejected attempt must not register)", got, eventStreamMaxSubscribersPerToken)
	}
}

func TestEventBroadcaster_SubscribeDifferentTokensIndependentCaps(t *testing.T) {
	b := newEventBroadcaster()
	// u1 fills its own cap entirely.
	var u1chans []chan []byte
	for i := 0; i < eventStreamMaxSubscribersPerToken; i++ {
		ch, err := b.subscribe("u1")
		if err != nil {
			t.Fatalf("subscribe u1 %d/%d: %v", i+1, eventStreamMaxSubscribersPerToken, err)
		}
		u1chans = append(u1chans, ch)
	}
	defer func() {
		for _, ch := range u1chans {
			b.unsubscribe(ch)
		}
	}()

	// u2 is unaffected by u1 being at its own cap.
	u2ch, err := b.subscribe("u2")
	if err != nil {
		t.Fatalf("subscribe u2: %v — a different token must not be affected by u1's own cap", err)
	}
	defer b.unsubscribe(u2ch)
}

func TestEventBroadcaster_UnsubscribeFreesTokenSlot(t *testing.T) {
	b := newEventBroadcaster()
	var chans []chan []byte
	for i := 0; i < eventStreamMaxSubscribersPerToken; i++ {
		ch, err := b.subscribe("u1")
		if err != nil {
			t.Fatalf("subscribe %d/%d: %v", i+1, eventStreamMaxSubscribersPerToken, err)
		}
		chans = append(chans, ch)
	}
	if _, err := b.subscribe("u1"); !errors.Is(err, ErrTooManyRequests) {
		t.Fatalf("subscribe at cap: err = %v, want ErrTooManyRequests", err)
	}

	b.unsubscribe(chans[0])
	chans = chans[1:]

	freed, err := b.subscribe("u1")
	if err != nil {
		t.Fatalf("subscribe after freeing a slot: %v", err)
	}
	chans = append(chans, freed)

	for _, ch := range chans {
		b.unsubscribe(ch)
	}
}

func TestEventBroadcaster_UnsubscribeCleansByTokenEntry(t *testing.T) {
	b := newEventBroadcaster()
	ch, err := b.subscribe("u1")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if got := b.tokenCount("u1"); got != 1 {
		t.Fatalf("tokenCount(u1) = %d, want 1", got)
	}

	b.unsubscribe(ch)

	if got := b.tokenCount("u1"); got != 0 {
		t.Errorf("tokenCount(u1) = %d, want 0 — stale zero-entry left behind", got)
	}
	if _, ok := b.byToken["u1"]; ok {
		t.Error("byToken still holds a key for u1 after its last subscriber unsubscribed")
	}
}

// — newEventStreamHandler — ——————————————————————————————————————————————

const eventStreamTestSecret = "event-stream-test-secret-123456"

func TestEventStreamHandler_Unauthenticated401(t *testing.T) {
	b := newEventBroadcaster()
	auth := BearerHMAC(eventStreamTestSecret)
	h := newEventStreamHandler(auth, b)

	req := httptest.NewRequest(http.MethodGet, "/_events/stream", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestEventStreamHandler_WrongRole403(t *testing.T) {
	b := newEventBroadcaster()
	auth := BearerHMAC(eventStreamTestSecret)
	h := newEventStreamHandler(auth, b)

	tok, err := SignToken(User{ID: "u1", Roles: []Role{Guest}}, eventStreamTestSecret, 0)
	if err != nil {
		t.Fatalf("SignToken: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/_events/stream", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

// nonFlushingWriter satisfies http.ResponseWriter without also satisfying
// http.Flusher — a plain struct (not embedding httptest.ResponseRecorder,
// which does implement Flush) so the handler's own capability check has a
// real negative case to exercise.
type nonFlushingWriter struct {
	header http.Header
	code   int
}

func (w *nonFlushingWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}
func (w *nonFlushingWriter) Write(p []byte) (int, error) { return len(p), nil }
func (w *nonFlushingWriter) WriteHeader(code int)        { w.code = code }

func TestEventStreamHandler_FlusherUnsupported500(t *testing.T) {
	b := newEventBroadcaster()
	auth := BearerHMAC(eventStreamTestSecret)
	h := newEventStreamHandler(auth, b)

	tok, err := SignToken(User{ID: "u1", Roles: []Role{Author}}, eventStreamTestSecret, 0)
	if err != nil {
		t.Fatalf("SignToken: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/_events/stream", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := &nonFlushingWriter{}
	h.ServeHTTP(w, req)

	if w.code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", w.code)
	}
}

// failWriteWriter satisfies http.ResponseWriter + http.Flusher; every Write
// after headers fails, simulating a torn connection mid-stream.
type failWriteWriter struct {
	header http.Header
}

func (w *failWriteWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}
func (w *failWriteWriter) Write(p []byte) (int, error) { return 0, errors.New("write failed") }
func (w *failWriteWriter) WriteHeader(int)             {}
func (w *failWriteWriter) Flush()                      {}

func TestEventStreamHandler_WriteErrorReturns(t *testing.T) {
	orig := eventStreamHeartbeat
	eventStreamHeartbeat = 5 * time.Millisecond
	defer func() { eventStreamHeartbeat = orig }()

	b := newEventBroadcaster()
	auth := BearerHMAC(eventStreamTestSecret)
	h := newEventStreamHandler(auth, b)

	tok, err := SignToken(User{ID: "u1", Roles: []Role{Author}}, eventStreamTestSecret, 0)
	if err != nil {
		t.Fatalf("SignToken: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/_events/stream", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := &failWriteWriter{}

	done := make(chan struct{})
	go func() {
		h.ServeHTTP(w, req)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not return after a write error")
	}
	if got := b.count(); got != 0 {
		t.Errorf("subscriber not cleaned up after write error, count = %d", got)
	}
}

func TestEventStreamHandler_WriteErrorOnEventReturns(t *testing.T) {
	// Distinct from TestEventStreamHandler_WriteErrorReturns: that test hits
	// the write error on the periodic heartbeat ticker branch, this one hits
	// it on the broadcast-event branch — two separate select cases, two
	// separate Write call sites.
	b := newEventBroadcaster()
	auth := BearerHMAC(eventStreamTestSecret)
	h := newEventStreamHandler(auth, b)

	tok, err := SignToken(User{ID: "u1", Roles: []Role{Author}}, eventStreamTestSecret, 0)
	if err != nil {
		t.Fatalf("SignToken: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/_events/stream", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := &failWriteWriter{}

	done := make(chan struct{})
	go func() {
		h.ServeHTTP(w, req)
		close(done)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for b.count() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if b.count() == 0 {
		t.Fatal("handler never subscribed to the broadcaster")
	}
	b.broadcast([]byte(`{"event":"x"}`))

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not return after a write error on the event branch")
	}
	if got := b.count(); got != 0 {
		t.Errorf("subscriber not cleaned up after write error, count = %d", got)
	}
}

func TestEventStreamHandler_StreamsBroadcastEvent(t *testing.T) {
	b := newEventBroadcaster()
	auth := BearerHMAC(eventStreamTestSecret)
	srv := httptest.NewServer(newEventStreamHandler(auth, b))
	defer srv.Close()

	tok, err := SignToken(User{ID: "u1", Roles: []Role{Author}}, eventStreamTestSecret, 0)
	if err != nil {
		t.Fatalf("SignToken: %v", err)
	}
	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	deadline := time.Now().Add(2 * time.Second)
	for b.count() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if b.count() == 0 {
		t.Fatal("handler never subscribed to the broadcaster")
	}

	b.broadcast([]byte(`{"event":"task.transitioned"}`))

	scanner := bufio.NewScanner(resp.Body)
	lineCh := make(chan string, 1)
	go func() {
		if scanner.Scan() {
			lineCh <- scanner.Text()
		}
	}()

	select {
	case line := <-lineCh:
		if line != `{"event":"task.transitioned"}` {
			t.Errorf("streamed line = %q", line)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the streamed event")
	}
}

func TestEventStreamHandler_HeartbeatPing(t *testing.T) {
	orig := eventStreamHeartbeat
	eventStreamHeartbeat = 20 * time.Millisecond
	defer func() { eventStreamHeartbeat = orig }()

	b := newEventBroadcaster()
	auth := BearerHMAC(eventStreamTestSecret)
	srv := httptest.NewServer(newEventStreamHandler(auth, b))
	defer srv.Close()

	tok, err := SignToken(User{ID: "u1", Roles: []Role{Author}}, eventStreamTestSecret, 0)
	if err != nil {
		t.Fatalf("SignToken: %v", err)
	}
	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	lineCh := make(chan string, 1)
	go func() {
		if scanner.Scan() {
			lineCh <- scanner.Text()
		}
	}()

	select {
	case line := <-lineCh:
		if line != `{"type":"ping"}` {
			t.Errorf("line = %q, want heartbeat ping", line)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for heartbeat ping")
	}
}

func TestEventStreamHandler_ClientDisconnectUnregisters(t *testing.T) {
	b := newEventBroadcaster()
	auth := BearerHMAC(eventStreamTestSecret)
	srv := httptest.NewServer(newEventStreamHandler(auth, b))
	defer srv.Close()

	tok, err := SignToken(User{ID: "u1", Roles: []Role{Author}}, eventStreamTestSecret, 0)
	if err != nil {
		t.Fatalf("SignToken: %v", err)
	}
	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for b.count() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := b.count(); got != 1 {
		t.Fatalf("count = %d, want 1 before disconnect", got)
	}

	resp.Body.Close() // client-initiated disconnect

	deadline = time.Now().Add(2 * time.Second)
	for b.count() != 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := b.count(); got != 0 {
		t.Errorf("count = %d, want 0 after client disconnect", got)
	}
}

// openEventStream opens a real GET /_events/stream connection against srv
// for the given token and waits until the broadcaster has actually
// registered it (not just until the HTTP round trip returns), so a caller
// can rely on b.count()/b.tokenCount() reflecting this connection
// immediately after openEventStream returns. Returns the response for the
// caller to close (releasing the connection's slot) when done.
func openEventStream(t *testing.T, srv *httptest.Server, b *eventBroadcaster, tok string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	return resp
}

func TestEventStreamHandler_TooManyConnectionsPerToken429(t *testing.T) {
	b := newEventBroadcaster()
	auth := BearerHMAC(eventStreamTestSecret)
	srv := httptest.NewServer(newEventStreamHandler(auth, b))
	defer srv.Close()

	tok, err := SignToken(User{ID: "u1", Roles: []Role{Author}}, eventStreamTestSecret, 0)
	if err != nil {
		t.Fatalf("SignToken: %v", err)
	}

	var conns []*http.Response
	for i := 0; i < eventStreamMaxSubscribersPerToken; i++ {
		conns = append(conns, openEventStream(t, srv, b, tok))
	}
	defer func() {
		for _, c := range conns {
			c.Body.Close()
		}
	}()

	deadline := time.Now().Add(2 * time.Second)
	for b.tokenCount("u1") != eventStreamMaxSubscribersPerToken && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := b.tokenCount("u1"); got != eventStreamMaxSubscribersPerToken {
		t.Fatalf("tokenCount(u1) = %d, want %d before the rejected attempt", got, eventStreamMaxSubscribersPerToken)
	}

	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode error body: %v — expected the standard smeldr error envelope, not a half-written stream", err)
	}
}

func TestEventStreamHandler_DifferentTokensNotCapped(t *testing.T) {
	b := newEventBroadcaster()
	auth := BearerHMAC(eventStreamTestSecret)
	srv := httptest.NewServer(newEventStreamHandler(auth, b))
	defer srv.Close()

	tok1, err := SignToken(User{ID: "u1", Roles: []Role{Author}}, eventStreamTestSecret, 0)
	if err != nil {
		t.Fatalf("SignToken u1: %v", err)
	}
	tok2, err := SignToken(User{ID: "u2", Roles: []Role{Author}}, eventStreamTestSecret, 0)
	if err != nil {
		t.Fatalf("SignToken u2: %v", err)
	}

	var conns []*http.Response
	for i := 0; i < eventStreamMaxSubscribersPerToken; i++ {
		conns = append(conns, openEventStream(t, srv, b, tok1))
	}
	defer func() {
		for _, c := range conns {
			c.Body.Close()
		}
	}()

	// u1 is now at its own cap; u2 must be entirely unaffected.
	u2conn := openEventStream(t, srv, b, tok2)
	defer u2conn.Body.Close()
}

func TestEventStreamHandler_CapReleasedOnDisconnect(t *testing.T) {
	b := newEventBroadcaster()
	auth := BearerHMAC(eventStreamTestSecret)
	srv := httptest.NewServer(newEventStreamHandler(auth, b))
	defer srv.Close()

	tok, err := SignToken(User{ID: "u1", Roles: []Role{Author}}, eventStreamTestSecret, 0)
	if err != nil {
		t.Fatalf("SignToken: %v", err)
	}

	var conns []*http.Response
	for i := 0; i < eventStreamMaxSubscribersPerToken; i++ {
		conns = append(conns, openEventStream(t, srv, b, tok))
	}

	deadline := time.Now().Add(2 * time.Second)
	for b.tokenCount("u1") != eventStreamMaxSubscribersPerToken && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}

	// Release one connection client-side and wait for the broadcaster to
	// notice — proves the release path end-to-end through the real
	// handler, not just eventBroadcaster.unsubscribe in isolation.
	conns[0].Body.Close()
	deadline = time.Now().Add(2 * time.Second)
	for b.tokenCount("u1") == eventStreamMaxSubscribersPerToken && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := b.tokenCount("u1"); got != eventStreamMaxSubscribersPerToken-1 {
		t.Fatalf("tokenCount(u1) = %d, want %d after releasing one connection", got, eventStreamMaxSubscribersPerToken-1)
	}

	newConn := openEventStream(t, srv, b, tok)
	defer newConn.Body.Close()
	for _, c := range conns[1:] {
		defer c.Body.Close()
	}
}

// TestEventStreamHandler_SurvivesWriteTimeout is a regression test for a
// real production bug: Config.WriteTimeout is an absolute deadline set once
// when the connection's headers are read, never reset by an intermediate
// Flush() — wrong for this deliberately long-lived stream. Without the fix,
// the first write attempted after the deadline (the first heartbeat, since
// nothing else is written until then) force-closes the connection. Uses a
// real httptest.NewUnstartedServer so Config.WriteTimeout is actually wired
// into a real net/http.Server, reproducing the exact mechanism — httptest.
// NewServer's default Config has no WriteTimeout set, which would not catch
// this bug.
func TestEventStreamHandler_SurvivesWriteTimeout(t *testing.T) {
	orig := eventStreamHeartbeat
	eventStreamHeartbeat = 30 * time.Millisecond
	defer func() { eventStreamHeartbeat = orig }()

	b := newEventBroadcaster()
	auth := BearerHMAC(eventStreamTestSecret)
	srv := httptest.NewUnstartedServer(newEventStreamHandler(auth, b))
	srv.Config.WriteTimeout = 100 * time.Millisecond
	srv.Start()
	defer srv.Close()

	tok, err := SignToken(User{ID: "u1", Roles: []Role{Author}}, eventStreamTestSecret, 0)
	if err != nil {
		t.Fatalf("SignToken: %v", err)
	}
	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	scanner := bufio.NewScanner(resp.Body)
	const wantLines = 5 // spans well past the 100ms WriteTimeout at a 30ms heartbeat
	for i := 0; i < wantLines; i++ {
		if !scanner.Scan() {
			t.Fatalf("connection closed prematurely after %d/%d lines (write-timeout bug): %v", i, wantLines, scanner.Err())
		}
	}
}

// deadlineUnsupportedWriter satisfies http.ResponseWriter + http.Flusher but
// not the unexported shape http.ResponseController.SetWriteDeadline looks
// for — proves newEventStreamHandler degrades gracefully (logs, still
// writes) rather than aborting when the underlying ResponseWriter can't
// support a write deadline at all.
type deadlineUnsupportedWriter struct {
	header  http.Header
	mu      sync.Mutex
	written []byte
}

func (w *deadlineUnsupportedWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}
func (w *deadlineUnsupportedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.written = append(w.written, p...)
	return len(p), nil
}
func (w *deadlineUnsupportedWriter) WriteHeader(int) {}
func (w *deadlineUnsupportedWriter) Flush()          {}

func (w *deadlineUnsupportedWriter) snapshot() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]byte, len(w.written))
	copy(out, w.written)
	return out
}

func TestEventStreamHandler_SetWriteDeadlineUnsupported_StillWrites(t *testing.T) {
	b := newEventBroadcaster()
	auth := BearerHMAC(eventStreamTestSecret)
	h := newEventStreamHandler(auth, b)

	tok, err := SignToken(User{ID: "u1", Roles: []Role{Author}}, eventStreamTestSecret, 0)
	if err != nil {
		t.Fatalf("SignToken: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/_events/stream", nil).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := &deadlineUnsupportedWriter{}

	done := make(chan struct{})
	go func() {
		h.ServeHTTP(w, req)
		close(done)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for b.count() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if b.count() == 0 {
		t.Fatal("handler never subscribed to the broadcaster")
	}
	b.broadcast([]byte(`{"event":"x"}`))

	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if bytes.Contains(w.snapshot(), []byte(`{"event":"x"}`)) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !bytes.Contains(w.snapshot(), []byte(`{"event":"x"}`)) {
		t.Fatal("event was not written despite SetWriteDeadline being unsupported — handler should degrade gracefully, not abort")
	}

	cancel() // end the handler goroutine
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not return after context cancellation")
	}
}
