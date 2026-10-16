package smeldr

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// nextEventData reads one event from ch and decodes its data into v.
func nextEventData(t *testing.T, ch chan []byte, event string, v any) {
	t.Helper()
	for {
		select {
		case payload := <-ch:
			var got WebhookEventPayload
			if err := json.Unmarshal(payload, &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if got.Event != event {
				continue
			}
			if err := json.Unmarshal(got.Data, v); err != nil {
				t.Fatalf("unmarshal data: %v", err)
			}
			return
		default:
			t.Fatalf("no %s event", event)
		}
	}
}

// signal.created from NotifySignalCreated (mcp's create_signal) carries the
// Signal's address, read in the same lookup as its receiver (T872).
func TestNotifySignalCreated_CarriesAddress(t *testing.T) {
	app, db := signalTestApp(t)
	app.EventStream()
	ctx := context.Background()
	s := &Signal{Sender: "core", Receiver: "architect", SignalType: "notice", Message: "m"}
	if err := InsertSignal(ctx, db, s); err != nil {
		t.Fatal(err)
	}
	ch, err := app.eventBroadcaster.subscribe("u1", "architect")
	if err != nil {
		t.Fatal(err)
	}
	defer app.eventBroadcaster.unsubscribe(ch)
	app.NotifySignalCreated(ctx, s.ID, s.Slug)
	var data transitionWebhookData
	nextEventData(t, ch, "signal.created", &data)
	if data.Address == "" || data.Address != s.Address || !strings.HasPrefix(data.Address, "S") {
		t.Errorf("address = %q, want %q", data.Address, s.Address)
	}
	raw, _ := json.Marshal(data)
	if !strings.Contains(string(raw), `"address":"`+s.Address+`"`) {
		t.Errorf("payload %s has no address", raw)
	}
}

// A condition core resolves announces signal.transitioned with the Signal's
// address (T872).
func TestResolve_TransitionCarriesAddress(t *testing.T) {
	app, db := signalTestApp(t)
	store := wireWebhooksForTest(t, app, db)
	ctx := context.Background()
	if _, _, err := store.Create(ctx, "https://8.8.8.8/hook", []string{eventSignalTransitioned}); err != nil {
		t.Fatal(err)
	}
	out, err := findModule[*Task](t, app).MCPCreate(authorCtx(), map[string]any{"title": "a task"})
	if err != nil {
		t.Fatal(err)
	}
	task := asItem[*Task](out)
	if err := recordAuthorizationRequiredSignal(ctx, db, nil, nil, nil, "Task", task.ID, "backlog", "active", "approve"); err != nil {
		t.Fatal(err)
	}
	var address string
	if err := db.QueryRow(`SELECT address FROM smeldr_signals WHERE signal_type = 'authorization-required'`).Scan(&address); err != nil || address == "" {
		t.Fatalf("address = %q, %v", address, err)
	}
	r := app.signalResolver()
	open, err := r.openFor(ctx, "Task", task.ID)
	if err != nil || len(open) != 1 || open[0].address != address {
		t.Fatalf("open = %+v, %v", open, err)
	}
	r.resolve(ctx, open, "moved")
	endpoints, err := store.EndpointsForEvent(ctx, eventSignalTransitioned)
	if err != nil || len(endpoints) != 1 {
		t.Fatalf("endpoints = %v, %v", endpoints, err)
	}
	jobs, err := app.webhookPool.ListJobsForEndpoint(ctx, endpoints[0].ID)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("jobs = %d, %v", len(jobs), err)
	}
	if !strings.Contains(string(jobs[0].Payload), `"address":"`+address+`"`) {
		t.Errorf("payload %s has no address %s", jobs[0].Payload, address)
	}
}

// Relation events carry both ends' addresses next to their IDs; an end with
// no address is omitted (T872).
func TestRelationEvent_CarriesEndAddresses(t *testing.T) {
	app, db := signalTestApp(t)
	ctx := context.Background()
	if err := CreateRelationTables(db); err != nil {
		t.Fatal(err)
	}
	rs, err := NewRelationStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := RegisterOrchestrationRelationKinds(ctx, rs); err != nil {
		t.Fatal(err)
	}
	b := newEventBroadcaster()
	rs.setSignalDeps(nil, nil, b)
	ch, err := b.subscribe("u1", eventStreamChannelRelations)
	if err != nil {
		t.Fatal(err)
	}
	defer b.unsubscribe(ch)
	taskOut, err := findModule[*Task](t, app).MCPCreate(authorCtx(), map[string]any{"title": "a task"})
	if err != nil {
		t.Fatal(err)
	}
	goalOut, err := findModule[*Goal](t, app).MCPCreate(authorCtx(), map[string]any{"title": "a goal"})
	if err != nil {
		t.Fatal(err)
	}
	task, goal := asItem[*Task](taskOut), asItem[*Goal](goalOut)
	if err := rs.Assert(ctx, RelationEdge{RelationKind: "derives_from", EdgeClass: "asserted", SourceType: "Task", SourceID: task.ID, TargetType: "Goal", TargetID: goal.ID}); err != nil {
		t.Fatal(err)
	}
	var data relationEventData
	nextEventData(t, ch, "relation.asserted", &data)
	if data.SourceAddress != task.Address || data.TargetAddress != goal.Address || data.SourceAddress == "" || data.TargetAddress == "" {
		t.Errorf("addresses = %q, %q; want %q, %q", data.SourceAddress, data.TargetAddress, task.Address, goal.Address)
	}
	if data.SourceID != task.ID || data.TargetID != goal.ID {
		t.Errorf("ids changed: %+v", data)
	}
	// An end with no address is left out of the payload.
	if a := lookupAddressByID(ctx, db, "Post", "nope", true); a != "" {
		t.Errorf("unknown end = %q, want empty", a)
	}
	raw, _ := json.Marshal(relationEventData{Type: "relation", SourceID: "x"})
	if strings.Contains(string(raw), "source_address") || strings.Contains(string(raw), "target_address") {
		t.Errorf("empty addresses not omitted: %s", raw)
	}
}
