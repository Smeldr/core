package smeldr

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// subStreamApp is an App with orchestration, the stream, the log and
// subscriptions, served over HTTP.
func subStreamApp(t *testing.T, subscriptions bool) (*App, *httptest.Server) {
	t.Helper()
	app, _ := signalTestApp(t)
	app.EventStream().EventLog(EventLogOptions{})
	if subscriptions {
		app.Subscriptions()
	}
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)
	t.Cleanup(app.eventBroadcaster.log.shutdown)
	return app, srv
}

// streamAs opens the stream as user id with query q.
func streamAs(t *testing.T, srv *httptest.Server, id, q string) *http.Response {
	t.Helper()
	tok, err := SignToken(User{ID: id, Roles: []Role{Author}}, transitionItemTestSecret, 0)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/_events/stream?"+q, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// envelope is the part of a streamed line the tests read.
type envelope struct {
	ID    string `json:"id"`
	Event string `json:"event"`
	Type  string `json:"type"`
	Data  struct {
		ID string `json:"id"`
	} `json:"data"`
}

// lines reads streamed lines into a channel.
func lines(resp *http.Response) chan envelope {
	out := make(chan envelope, 256)
	go func() {
		defer close(out)
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			var e envelope
			if json.Unmarshal(sc.Bytes(), &e) == nil && e.Type != "ping" {
				out <- e
			}
		}
	}()
	return out
}

// next returns the next line, or fails after a timeout.
func next(t *testing.T, ch chan envelope) envelope {
	t.Helper()
	select {
	case e, ok := <-ch:
		if !ok {
			t.Fatal("the stream closed")
		}
		return e
	case <-time.After(3 * time.Second):
		t.Fatal("no line")
		return envelope{}
	}
}

// waitConnected waits until n connections are subscribed.
func waitConnected(t *testing.T, app *App, n int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for app.eventBroadcaster.count() < n {
		if time.Now().After(deadline) {
			t.Fatal("the stream did not subscribe")
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// signalTo inserts and announces a question addressed to receiver.
func signalTo(t *testing.T, app *App, receiver string) string {
	t.Helper()
	s := &Signal{Sender: "architect", Receiver: receiver, SignalType: "question", Message: "?"}
	if err := InsertSignal(context.Background(), app.cfg.DB, s); err != nil {
		t.Fatal(err)
	}
	app.NotifySignalCreated(context.Background(), s.ID, s.Slug)
	return s.ID
}

// A connection with no ?channel= delivers its owner's subscriptions: the
// universal default delivers a request addressed to it, and nothing else.
func TestSubscriptionStream_Defaults(t *testing.T) {
	app, srv := subStreamApp(t, true)
	ch := lines(streamAs(t, srv, "u1", ""))
	waitConnected(t, app, 1)
	signalTo(t, app, "someone-else")
	mine := signalTo(t, app, "u1")
	if e := next(t, ch); e.Event != "signal.created" || e.Data.ID != mine {
		t.Errorf("delivered %+v, want the request to u1", e)
	}
}

// ?subscription= delivers that subscription, saves its cursor, and resumes
// from it.
func TestSubscriptionStream_NamedWithCursor(t *testing.T) {
	// Set before the server starts and restored after it closes (cleanups run
	// last in, first out), so no handler reads it while it changes.
	prev := subscriptionCursorEvery
	subscriptionCursorEvery = 20 * time.Millisecond
	t.Cleanup(func() { subscriptionCursorEvery = prev })
	app, srv := subStreamApp(t, true)
	u1 := asUser("u1", Author)
	sub, err := app.CreateSubscription(u1, SubscriptionSpec{Name: "core tasks", Filter: SubscriptionFilter{
		Types: []string{"Task"}, Events: []string{"transitioned"}, Fields: map[string][]string{"band": {"core"}}}})
	if err != nil {
		t.Fatal(err)
	}
	resp := streamAs(t, srv, "u1", "subscription="+sub.Address)
	ch := lines(resp)
	waitConnected(t, app, 1)
	task := mustTask(t, app)
	if _, err := app.TransitionItemVia(context.Background(), "mcp", "Task", task, "active", ""); err != nil {
		t.Fatal(err)
	}
	first := next(t, ch)
	if first.Event != "task.transitioned" {
		t.Fatalf("delivered %+v", first)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		got, _ := app.GetSubscription(u1, sub.ID)
		if got.Cursor == first.ID {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("cursor = %q, want %q", got.Cursor, first.ID)
		}
		time.Sleep(10 * time.Millisecond)
	}
	resp.Body.Close()
	waitDisconnected(t, app)

	// Missed while away: replayed from the cursor on reconnect.
	if _, err := app.TransitionItemVia(context.Background(), "mcp", "Task", task, "waiting-plan", ""); err != nil {
		t.Fatal(err)
	}
	waitLogged(t, app.eventBroadcaster.log, app.eventBroadcaster.log.last.Load())
	time.Sleep(50 * time.Millisecond)
	ch2 := lines(streamAs(t, srv, "u1", "subscription="+sub.Address))
	if e := next(t, ch2); e.Event != "task.transitioned" || e.ID == first.ID {
		t.Errorf("replayed %+v, want the missed transition", e)
	}
	if e := next(t, ch2); e.Type != "caught_up" {
		t.Errorf("then %+v, want caught_up", e)
	}
}

// waitDisconnected waits until no connection is subscribed.
func waitDisconnected(t *testing.T, app *App) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for app.eventBroadcaster.count() > 0 {
		if time.Now().After(deadline) {
			t.Fatal("the stream did not unsubscribe")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestSubscriptionStream_Errors(t *testing.T) {
	app, srv := subStreamApp(t, true)
	theirs, err := app.CreateSubscription(asUser("u2", Author), SubscriptionSpec{Name: "theirs"})
	if err != nil {
		t.Fatal(err)
	}
	if r := streamAs(t, srv, "u1", "subscription="+theirs.Address+"&channel=core"); r.StatusCode != http.StatusBadRequest {
		t.Errorf("with channel: %d", r.StatusCode)
	}
	if r := streamAs(t, srv, "u1", "subscription="+theirs.Address); r.StatusCode != http.StatusNotFound {
		t.Errorf("another's: %d, want 404", r.StatusCode)
	}
	if _, err := app.cfg.DB.ExecContext(context.Background(), `UPDATE smeldr_subscriptions SET delivery = 'webhook:e' WHERE id = ?`, theirs.ID); err != nil {
		t.Fatal(err)
	}
	if r := streamAs(t, srv, "u2", "subscription="+theirs.Address); r.StatusCode != http.StatusBadRequest {
		t.Errorf("a webhook subscription: %d, want 400", r.StatusCode)
	}
	_, plain := subStreamApp(t, false)
	if r := streamAs(t, plain, "u1", "subscription=N1"); r.StatusCode != http.StatusBadRequest {
		t.Errorf("no subscriptions: %d, want 400", r.StatusCode)
	}
}

// ?channel= keeps today's routing, and a change to the owner's subscriptions
// recompiles an open connection.
func TestSubscriptionStream_ChannelAndRecompile(t *testing.T) {
	app, srv := subStreamApp(t, true)
	legacy := lines(streamAs(t, srv, "u1", "channel=core"))
	all := lines(streamAs(t, srv, "u1", ""))
	waitConnected(t, app, 2)
	task := mustTask(t, app)
	if e := next(t, legacy); !strings.HasPrefix(e.Event, "task.") {
		t.Errorf("channel=core got %+v", e)
	}
	if _, err := app.CreateSubscription(asUser("u1", Author), SubscriptionSpec{Name: "tasks", Filter: SubscriptionFilter{Types: []string{"Task"}}}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond) // the connection recompiles
	if _, err := app.TransitionItemVia(context.Background(), "mcp", "Task", task, "active", ""); err != nil {
		t.Fatal(err)
	}
	if e := next(t, all); e.Event != "task.transitioned" {
		t.Errorf("after the new subscription: %+v", e)
	}
}
