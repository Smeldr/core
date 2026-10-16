// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"encoding/json"
	"testing"
)

// relationEvents drains ch and returns the relation.asserted events of kind.
func relationEvents(t *testing.T, ch chan []byte, kind string) []relationEventData {
	t.Helper()
	var out []relationEventData
	for {
		select {
		case payload := <-ch:
			var got WebhookEventPayload
			if err := json.Unmarshal(payload, &got); err != nil {
				t.Fatal(err)
			}
			if got.Event != eventRelationAsserted {
				continue
			}
			var d relationEventData
			if err := json.Unmarshal(got.Data, &d); err != nil {
				t.Fatal(err)
			}
			if d.RelationKind == kind {
				out = append(out, d)
			}
		default:
			return out
		}
	}
}

// refEventFixture is a refFixture whose relation store streams its events.
func refEventFixture(t *testing.T) (refFixture, chan []byte) {
	t.Helper()
	f := newRefFixture(t)
	f.app.EventStream()
	_ = f.app.Handler() // wires the stream into the relation store
	ch, err := f.app.eventBroadcaster.subscribe("watcher", eventStreamChannelRelations)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.app.eventBroadcaster.unsubscribe(ch) })
	return f, ch
}

// A Signal inserted raw (mcp's create_signal path) and then notified
// announces its about edges once, as new rows with both addresses (T875).
func TestSignalCreate_AnnouncesAboutEdges(t *testing.T) {
	f, ch := refEventFixture(t)
	ctx := context.Background()
	task := f.task(t, map[string]any{"title": "the subject"})
	s := &Signal{Sender: "core", Receiver: "architect", SignalType: "notice", Message: "m", TaskRef: task.ID}
	if err := InsertSignal(f.global, f.db, s); err != nil {
		t.Fatal(err)
	}
	relationEvents(t, ch, "about") // nothing yet: the insert announces nothing
	f.app.NotifySignalCreated(ctx, s.ID, s.Slug)
	got := relationEvents(t, ch, "about")
	if len(got) != 1 {
		t.Fatalf("about events = %d, want 1: %+v", len(got), got)
	}
	e := got[0]
	if e.SourceID != s.ID || e.TargetID != task.ID || e.SourceAddress != s.Address || e.TargetAddress != task.Address || e.NewRow == nil || !*e.NewRow {
		t.Errorf("event = %+v", e)
	}
	// Without a relation store nothing is announced and nothing fails.
	announceSignalReferenceEdges(ctx, nil, s.ID)
	announceSignalReferenceEdges(ctx, f.rels, "")
}

// A condition core emits announces its about edge (T875).
func TestConditionEmit_AnnouncesAboutEdge(t *testing.T) {
	f, ch := refEventFixture(t)
	task := f.task(t, map[string]any{"title": "gated"})
	relationEvents(t, ch, "about")
	if err := recordAuthorizationRequiredSignalVia(context.Background(), f.app.signalResolver(), "Task", task.ID, "backlog", "active", "approve"); err != nil {
		t.Fatal(err)
	}
	got := relationEvents(t, ch, "about")
	if len(got) != 1 || got[0].TargetID != task.ID || got[0].TargetAddress != task.Address {
		t.Errorf("about events = %+v", got)
	}
}

// The other reference paths already announce at create: Task implements and
// Amendment task_ref (pins the table in plan P211).
func TestReferenceCreate_AnnouncesOnOtherPaths(t *testing.T) {
	f, ch := refEventFixture(t)
	dec := f.decision(t, "a decision")
	task := f.task(t, map[string]any{"title": "implements it", "implements": dec.ID})
	if got := relationEvents(t, ch, "implements"); len(got) != 1 || got[0].SourceID != task.ID || got[0].TargetAddress != dec.Address {
		t.Errorf("implements events = %+v", got)
	}
	out, err := f.amends.MCPCreate(f.global, map[string]any{"summary": "s", "amendment_type": "fix", "task_ref": task.ID})
	if err != nil {
		t.Fatal(err)
	}
	a := asItem[*Amendment](out)
	if got := relationEvents(t, ch, "ships_as"); len(got) != 1 || got[0].SourceID != task.ID || got[0].TargetID != a.ID {
		t.Errorf("ships_as events = %+v", got)
	}
}

// A failing edge read is logged and announces nothing.
func TestAnnounceSignalReferenceEdges_FailsOpen(t *testing.T) {
	f := newRefFixture(t)
	f.rels.db = failAddressDB{f.db, "FROM smeldr_relations WHERE source_type = 'Signal'"}
	announceSignalReferenceEdges(context.Background(), f.rels, "x")
}

// A runtime-defined reference field (task_plan's task_ref) announces its
// refers_to edge at create (T875; liveEdge used to fail on SQLite times).
func TestReferenceCreate_AnnouncesDynamic(t *testing.T) {
	f, ch := refEventFixture(t)
	ctx := context.Background()
	task := f.task(t, map[string]any{"slug": "planned"})
	plan := &ContentTypeSchema{TypeName: "plan", Fields: []byte(`[{"name":"title","type":"string","role":"title"},{"name":"task_ref","type":"string","references":"Task"}]`)}
	if _, err := f.app.DefineContentType(ctx, plan); err != nil {
		t.Fatal(err)
	}
	repo, err := f.app.DynamicContentRepo("plan")
	if err != nil {
		t.Fatal(err)
	}
	relationEvents(t, ch, "refers_to")
	node, _, err := repo.CreateDraftWithWarnings(f.global, "mcp", map[string]any{"title": "P", "task_ref": "planned"})
	if err != nil {
		t.Fatal(err)
	}
	if got := relationEvents(t, ch, "refers_to"); len(got) != 1 || got[0].SourceID != node.ID || got[0].TargetAddress != task.Address {
		t.Errorf("refers_to events = %+v", got)
	}
}
