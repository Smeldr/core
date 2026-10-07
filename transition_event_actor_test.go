// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// transitionEventData reads the data of the one event on ch and decodes it.
func transitionEventData(t *testing.T, ch chan []byte) (transitionWebhookData, map[string]any) {
	t.Helper()
	select {
	case raw := <-ch:
		var p WebhookEventPayload
		if err := json.Unmarshal(raw, &p); err != nil {
			t.Fatalf("unmarshal event: %v", err)
		}
		var d transitionWebhookData
		if err := json.Unmarshal(p.Data, &d); err != nil {
			t.Fatalf("unmarshal data: %v", err)
		}
		var generic map[string]any
		_ = json.Unmarshal(p.Data, &generic)
		return d, generic
	case <-time.After(2 * time.Second):
		t.Fatal("no event delivered")
		return transitionWebhookData{}, nil
	}
}

// The transition event carries the actor of the same transition on both sinks,
// with the kind provenance records for it, and the two agree.
func TestTransitionEvent_CarriesActor(t *testing.T) {
	for _, tt := range []struct {
		name     string
		roles    []Role
		wantKind string
	}{
		{"untagged editor", []Role{Editor}, "unclassified"},
		{"agent", []Role{Editor, Agent}, "agent"},
		{"job", []Role{Editor, Job}, "job"},
		{"human", []Role{Editor, Human}, "human"},
		{"hand-signed human and agent", []Role{Editor, Human, Agent}, "agent"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			app, db, rs := setupTransitionItemApp(t)
			prov := &fakeProvenanceStore{}
			app.Provenance(prov)
			ratifyDecision(t, app, db, rs, "dec-e", "dec-e-slug")
			store := wireWebhooksForTest(t, app, db)
			app.EventStream()
			watcher, err := app.eventBroadcaster.subscribe("watcher", eventStreamChannelAll)
			if err != nil {
				t.Fatalf("subscribe: %v", err)
			}
			ctx := context.Background()
			if _, _, err := store.Create(ctx, "https://8.8.8.8/hook", []string{"decision.transitioned"}); err != nil {
				t.Fatalf("Create endpoint: %v", err)
			}
			before := len(prov.Appended())

			actor := NewTestContext(User{ID: "actor-1", Roles: tt.roles})
			if _, err := app.TransitionItemVia(actor, "mcp", "Decision", "dec-e-slug", "pending-re-evaluation", ""); err != nil {
				t.Fatalf("TransitionItemVia: %v", err)
			}

			streamed, generic := transitionEventData(t, watcher)
			if streamed.ActorID != "actor-1" || streamed.ActorKind != tt.wantKind {
				t.Errorf("stream actor = %q/%q, want actor-1/%s", streamed.ActorID, streamed.ActorKind, tt.wantKind)
			}
			if generic["actor_id"] != "actor-1" || generic["actor_kind"] != tt.wantKind {
				t.Errorf("stream JSON keys = %v", generic)
			}

			endpoints, _ := store.EndpointsForEvent(ctx, "decision.transitioned")
			jobs, err := app.WebhookPool().ListJobsForEndpoint(ctx, endpoints[0].ID)
			if err != nil || len(jobs) != 1 {
				t.Fatalf("webhook jobs = %d, %v, want 1", len(jobs), err)
			}
			var p WebhookEventPayload
			_ = json.Unmarshal(jobs[0].Payload, &p)
			var hooked transitionWebhookData
			_ = json.Unmarshal(p.Data, &hooked)
			if hooked.ActorID != streamed.ActorID || hooked.ActorKind != streamed.ActorKind {
				t.Errorf("webhook actor = %q/%q, stream %q/%q: both sinks must carry the same", hooked.ActorID, hooked.ActorKind, streamed.ActorID, streamed.ActorKind)
			}

			// The event names the actor the provenance record of the same transition names.
			var rec *ProvenanceRecord
			for _, r := range prov.Appended()[before:] {
				if r.Verb == "transition" {
					r := r
					rec = &r
				}
			}
			if rec == nil || rec.ActorID != streamed.ActorID || rec.ActorKind != streamed.ActorKind {
				t.Errorf("provenance record %+v does not match the event actor %q/%q", rec, streamed.ActorID, streamed.ActorKind)
			}
		})
	}
}

// With no authenticated actor the keys are absent, not empty strings.
func TestTransitionEvent_NoActorOmitsKeys(t *testing.T) {
	app, db, _ := setupTransitionItemApp(t)
	insertSignal(t, db, "sig-na", "sig-na-slug", "pending")
	store := wireWebhooksForTest(t, app, db)
	ctx := context.Background()
	if _, _, err := store.Create(ctx, "https://8.8.8.8/hook", []string{"signal.transitioned"}); err != nil {
		t.Fatalf("Create endpoint: %v", err)
	}
	if _, err := app.TransitionItemWithReason(ctx, "Signal", "sig-na-slug", "read", ""); err != nil {
		t.Fatalf("TransitionItemWithReason: %v", err)
	}
	endpoints, _ := store.EndpointsForEvent(ctx, "signal.transitioned")
	jobs, _ := app.WebhookPool().ListJobsForEndpoint(ctx, endpoints[0].ID)
	if len(jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(jobs))
	}
	if s := string(jobs[0].Payload); strings.Contains(s, "actor_id") || strings.Contains(s, "actor_kind") {
		t.Errorf("payload %s carries an actor key with no actor known", s)
	}
}
