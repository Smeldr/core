// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"encoding/json"
	"testing"
)

// streamEnv is an App with relations, provenance and the event stream wired,
// and four subscribers: the relations topic, "all", a role channel, and the
// acting user's own token on "all".
type streamEnv struct {
	app                    *App
	rs                     *RelationStore
	topic, all, role, self chan []byte
}

func newStreamEnv(t *testing.T) *streamEnv {
	t.Helper()
	rs, db := newHistoryStore(t)
	app := New(MustConfig(Config{BaseURL: "https://example.com", Secret: []byte(transitionItemTestSecret), DB: db}))
	app.Relations(rs)
	app.EventStream()
	app.Handler()
	env := &streamEnv{app: app, rs: rs}
	sub := func(token, channel string) chan []byte {
		ch, err := app.eventBroadcaster.subscribe(token, channel)
		if err != nil {
			t.Fatalf("subscribe %s: %v", channel, err)
		}
		t.Cleanup(func() { app.eventBroadcaster.unsubscribe(ch) })
		return ch
	}
	env.topic = sub("watcher-1", eventStreamChannelRelations)
	env.all = sub("watcher-2", eventStreamChannelAll)
	env.role = sub("watcher-3", "core")
	env.self = sub("alice", eventStreamChannelAll)
	return env
}

// drain returns every event waiting on ch, decoded.
func drain(t *testing.T, ch chan []byte) []WebhookEventPayload {
	t.Helper()
	var out []WebhookEventPayload
	for {
		select {
		case raw := <-ch:
			var p WebhookEventPayload
			if err := json.Unmarshal(raw, &p); err != nil {
				t.Fatalf("unmarshal %q: %v", raw, err)
			}
			out = append(out, p)
		default:
			return out
		}
	}
}

func relData(t *testing.T, p WebhookEventPayload) relationEventData {
	t.Helper()
	var d relationEventData
	if err := json.Unmarshal(p.Data, &d); err != nil {
		t.Fatalf("data: %v", err)
	}
	return d
}

// expectOne checks that topic and all each got exactly one event named name
// with the data check passing, the role channel nothing, and the actor's own
// connection the event only when wantSelf.
func (e *streamEnv) expectOne(t *testing.T, name string, wantSelf bool, check func(relationEventData)) {
	t.Helper()
	for label, ch := range map[string]chan []byte{"relations topic": e.topic, "all": e.all} {
		got := drain(t, ch)
		if len(got) != 1 || got[0].Event != name {
			t.Errorf("%s got %+v; want one %s", label, got, name)
			continue
		}
		check(relData(t, got[0]))
	}
	if got := drain(t, e.role); len(got) != 0 {
		t.Errorf("a role channel got %+v; want nothing", got)
	}
	if got := drain(t, e.self); (len(got) == 1) != wantSelf {
		t.Errorf("the actor's own connection got %d events; want self delivery %v", len(got), wantSelf)
	}
}

func TestStreamEvents_RelationAssertedAndEnded(t *testing.T) {
	e := newStreamEnv(t)
	alice := histCtx("alice")

	if err := e.rs.Assert(alice, tagEdge()); err != nil {
		t.Fatal(err)
	}
	var id string
	e.expectOne(t, eventRelationAsserted, false, func(d relationEventData) {
		id = d.ID
		if d.NewRow == nil || !*d.NewRow || d.RelationKind != "tagged" || d.SourceID != "art-1" || d.TargetID != "tag-1" || d.ActorID != "alice" {
			t.Errorf("asserted = %+v; want a new row by alice with both ends", d)
		}
	})

	if err := e.rs.Assert(alice, tagEdge()); err != nil {
		t.Fatal(err)
	}
	e.expectOne(t, eventRelationAsserted, false, func(d relationEventData) {
		if d.NewRow == nil || *d.NewRow || d.ID != id {
			t.Errorf("re-assert = %+v; want new_row false on the same id", d)
		}
	})

	if err := e.rs.Withdraw(alice, id, "wrong tag"); err != nil {
		t.Fatal(err)
	}
	e.expectOne(t, eventRelationEnded, false, func(d relationEventData) {
		if d.Cause != EdgeEndWithdrawn || d.Reason != "wrong tag" || d.ActorID != "alice" || d.NewRow != nil || d.TargetType != "tag" {
			t.Errorf("ended = %+v; want alice's withdrawal with both ends", d)
		}
	})

	// The sweep's end is a job's: it skips no connection.
	if err := e.rs.Assert(alice, tagEdge()); err != nil {
		t.Fatal(err)
	}
	drain(t, e.topic)
	drain(t, e.all)
	dead := func(_ context.Context, typ, _ string) (bool, error) { return typ != "tag", nil }
	if _, _, _, err := e.rs.SweepStructural(context.Background(), dead, func(context.Context, RelationEdge) {}); err != nil {
		t.Fatal(err)
	}
	e.expectOne(t, eventRelationEnded, true, func(d relationEventData) {
		if d.Cause != EdgeEndSwept || d.ActorKind != "job" || d.ActorID != sweepStructuralJob {
			t.Errorf("swept = %+v", d)
		}
	})
}

func TestStreamEvents_RecomputeAndPurge(t *testing.T) {
	e := newStreamEnv(t)
	alice := histCtx("alice")
	in := []RelationEdge{{TargetType: "tag", TargetID: "tag-9", RelationKind: "tagged"}}
	if err := e.rs.RecomputeAsserted(alice, "article", "art-9", in); err != nil {
		t.Fatal(err)
	}
	e.expectOne(t, eventRelationAsserted, false, func(d relationEventData) {
		if d.NewRow == nil || !*d.NewRow || d.SourceID != "art-9" || d.ActorID != "alice" {
			t.Errorf("recompute insert = %+v", d)
		}
	})
	if err := e.rs.RecomputeAsserted(alice, "article", "art-9", nil); err != nil {
		t.Fatal(err)
	}
	var id string
	e.expectOne(t, eventRelationEnded, false, func(d relationEventData) {
		id = d.ID
		if d.Cause != EdgeEndRecomputed || d.ActorID != "alice" {
			t.Errorf("recompute end = %+v", d)
		}
	})
	if err := e.rs.Delete(alice, id); err != nil {
		t.Fatal(err)
	}
	e.expectOne(t, eventRelationEnded, false, func(d relationEventData) {
		if d.Cause != EdgeEndPurged || d.ID != id || d.RelationKind != "tagged" {
			t.Errorf("purge = %+v", d)
		}
	})
}

// No sink wired: relation writes emit nothing and do not fail.
func TestStreamEvents_NoSinkNoEvent(t *testing.T) {
	store, _ := newHistoryStore(t)
	if err := store.Assert(histCtx("alice"), tagEdge()); err != nil {
		t.Fatalf("assert without sinks: %v", err)
	}
}
