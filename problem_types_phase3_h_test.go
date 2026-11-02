package smeldr

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// T1061 phase 3: the event log, event stream, findings, provenance, rest
// counts, search and subscription stream refusals, each driven through a real
// site.

// wantTypedH checks err's refusal type, exactly its values, the sentinel it
// wraps and its exact Error() text.
func wantTypedH(t *testing.T, err error, typ ProblemType, values map[string]string, sentinel error, msg string) {
	t.Helper()
	if err == nil {
		t.Fatalf("err = nil; want %s", typ)
	}
	if got := ProblemTypeOf(err); got != typ {
		t.Errorf("ProblemTypeOf = %q; want %q (err %v)", got, typ, err)
	}
	if got := ProblemValuesOf(err); !maps.Equal(got, values) || (got == nil) != (values == nil) {
		t.Errorf("ProblemValuesOf = %#v; want %#v", got, values)
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("errors.Is(%v, %v) = false", err, sentinel)
	}
	if err.Error() != msg {
		t.Errorf("Error() = %q\nwant      %q", err.Error(), msg)
	}
}

// wantEnvelopeH decodes a handler's JSON error envelope and checks the
// status, code, message, type id and exactly its values.
func wantEnvelopeH(t *testing.T, resp *http.Response, status int, code, message string, typ ProblemType, values map[string]string) {
	t.Helper()
	if resp.StatusCode != status {
		t.Errorf("status = %d; want %d", resp.StatusCode, status)
	}
	var env struct {
		Error struct {
			Code    string            `json:"code"`
			Message string            `json:"message"`
			ID      ProblemType       `json:"id"`
			Class   string            `json:"class"`
			Values  map[string]string `json:"values"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	e := env.Error
	if e.Code != code || e.Message != message {
		t.Errorf("code, message = %q, %q; want %q, %q", e.Code, e.Message, code, message)
	}
	if e.ID != typ {
		t.Errorf("id = %q; want %q", e.ID, typ)
	}
	if e.Class != problemClass(typ) {
		t.Errorf("class = %q; want %q", e.Class, problemClass(typ))
	}
	if !maps.Equal(e.Values, values) || (e.Values == nil) != (values == nil) {
		t.Errorf("values = %#v; want %#v", e.Values, values)
	}
}

func TestPhaseThreeH_EventsAfter(t *testing.T) {
	_, err := (&App{}).EventsAfter(context.Background(), "", 0)
	wantTypedH(t, err, problemEventLogNotEnabled, nil, ErrBadRequest, "Bad request: the event log is not enabled")

	app := pagedLogApp(t, 3)
	_, err = app.EventsAfter(context.Background(), "~!!", 10)
	wantTypedH(t, err, problemEventLogCursorInvalid, nil, ErrBadRequest, "Bad request: the cursor is not a continuation this instance issued")
}

func TestPhaseThreeH_StreamAfterWithoutLog(t *testing.T) {
	srv, tok := logStreamServer(t, newEventBroadcaster(), nil)
	resp := streamGet(t, srv, tok, "after=x")
	wantEnvelopeH(t, resp, http.StatusBadRequest, "bad_request", "Bad request", problemEventLogNotEnabled, nil)
}

func TestPhaseThreeH_StreamSubscriptionRefusals(t *testing.T) {
	app, srv := subStreamApp(t, true)
	theirs, err := app.CreateSubscription(asUser("u2", Author), SubscriptionSpec{Name: "theirs"})
	if err != nil {
		t.Fatal(err)
	}
	resp := streamAs(t, srv, "u1", "subscription="+theirs.Address+"&channel=core")
	wantEnvelopeH(t, resp, http.StatusBadRequest, "bad_request", "Bad request", problemEventstreamParamsConflict, nil)

	if _, err := app.cfg.DB.ExecContext(context.Background(), `UPDATE smeldr_subscriptions SET delivery = 'webhook:e' WHERE id = ?`, theirs.ID); err != nil {
		t.Fatal(err)
	}
	resp = streamAs(t, srv, "u2", "subscription="+theirs.Address)
	wantEnvelopeH(t, resp, http.StatusBadRequest, "bad_request", "Bad request", problemSubscriptionWebhookNotStream,
		map[string]string{"record": theirs.Address})
	_, _, err = app.compileForConnection(asUser("u2", Author), "u2", theirs.Address)
	wantTypedH(t, err, problemSubscriptionWebhookNotStream, map[string]string{"record": theirs.Address}, ErrBadRequest,
		"Bad request: subscription "+theirs.Address+" delivers to a webhook, not the stream")

	_, plain := subStreamApp(t, false)
	resp = streamAs(t, plain, "u1", "subscription=N1")
	wantEnvelopeH(t, resp, http.StatusBadRequest, "bad_request", "Bad request", problemEventstreamNoSubscriptions, nil)
}

func TestPhaseThreeH_TooManyStreams(t *testing.T) {
	b := newEventBroadcaster()
	for i := 0; i < eventStreamMaxSubscribersPerToken; i++ {
		ch, err := b.subscribe("u1", eventStreamChannelAll)
		if err != nil {
			t.Fatalf("subscribe %d: %v", i, err)
		}
		t.Cleanup(func() { b.unsubscribe(ch) })
	}
	_, err := b.subscribe("u1", eventStreamChannelAll)
	wantTypedH(t, err, problemEventstreamTooManyStreams, nil, ErrTooManyRequests, ErrTooManyRequests.Error())
}

func TestPhaseThreeH_Findings(t *testing.T) {
	f := newFindingsFixture(t)
	reader := as("reader-1").(Context)
	_, err := f.app.ListFindings(reader, FindingQuery{Limit: 501})
	wantTypedH(t, err, problemLimitFindings, map[string]string{"max": "500"}, ErrBadRequest,
		"Bad request: limit must be between 0 and 500")

	_, err = (&App{cfg: Config{DB: f.db}}).ListFindings(reader, FindingQuery{})
	wantTypedH(t, err, problemFeatureNotEnabled, map[string]string{"feature": "findings"}, ErrNotFound,
		"Not found: findings are not recorded on this instance (no finding store is wired)")
}

func TestPhaseThreeH_ProvenanceNotEnabled(t *testing.T) {
	_, err := (&App{}).ItemProvenance(context.Background(), "Task", "t", ProvenanceMembers, 0, 0)
	wantTypedH(t, err, problemFeatureNotEnabled, map[string]string{"feature": "provenance"}, ErrNotFound,
		"Not found: provenance is not enabled on this instance")
}

func TestPhaseThreeH_StoredRestCountsLimit(t *testing.T) {
	f := newStoredRestFixture(t)
	_, err := f.app.StoredRestCounts(allReader(), make([]ItemRef, MaxStoredRestCountItems+1))
	wantTypedH(t, err, problemLimitRestCountItems, map[string]string{"limit": strconv.Itoa(MaxStoredRestCountItems)}, ErrBadRequest,
		"Bad request: at most 200 items per call")
}

func TestPhaseThreeH_SearchQueryInvalid(t *testing.T) {
	f := newSearchFixture(t)
	_, err := f.app.Search(f.global, SearchQuery{})
	wantTypedH(t, err, problemSearchQueryInvalid, nil, ErrBadRequest,
		"Bad request: search needs text or at least one filter (types, states, place)")

	_, err = f.app.Search(f.global, SearchQuery{Text: "a"})
	wantTypedH(t, err, problemSearchQueryInvalid, map[string]string{"min": "2", "max": "200", "length": "1"}, ErrBadRequest,
		"Bad request: search text must be 2 to 200 characters, got 1")

	_, err = f.app.Search(f.global, SearchQuery{Text: strings.Repeat("x", 3), Limit: -1})
	wantTypedH(t, err, problemSearchQueryInvalid, nil, ErrBadRequest,
		"Bad request: search limit and offset must not be negative")

	_, err = f.app.Search(f.global, SearchQuery{Text: "apple", Types: []string{"Nope"}})
	wantTypedH(t, err, problemSearchQueryInvalid, map[string]string{"type": "Nope"}, ErrBadRequest,
		`Bad request: "Nope" is not a record type this instance can search`)
}
