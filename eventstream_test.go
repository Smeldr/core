package smeldr

import (
	"bufio"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// — eventBroadcaster — ————————————————————————————————————————————————————

func TestEventBroadcaster_SubscribeReceivesBroadcast(t *testing.T) {
	b := newEventBroadcaster()
	ch := b.subscribe()
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
		ch := b.subscribe()
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
	ch := b.subscribe()
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

	slow := b.subscribe() // deliberately never drained
	defer b.unsubscribe(slow)

	fast := b.subscribe()
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
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					ch := b.subscribe()
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
