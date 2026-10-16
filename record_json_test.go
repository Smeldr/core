package smeldr

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Fields per type (D114 point 2): the seven flow-bound record types leave out
// PublishedAt and ScheduledAt on every JSON surface.

// publishFieldSamples holds one value of every compiled type the orchestration
// and authority registrations add, with both fields set so a dropped key is
// really dropped, not merely zero. A type registered there and missing here
// fails TestOrchestrationJSON_EveryFlowWithoutPublish.
func publishFieldSamples() map[string]any {
	at := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	n := Node{ID: "id-1", Slug: "slug-1", Status: "draft", PublishedAt: at, ScheduledAt: &at, CreatedAt: at, UpdatedAt: at, Rev: 3}
	return map[string]any{
		"Goal":          &Goal{Node: n},
		"Signal":        &Signal{Node: n},
		"Task":          &Task{Node: n},
		"Decision":      &Decision{Node: n},
		"Amendment":     &Amendment{Node: n},
		"Run":           &Run{Node: n},
		"Rule":          &Rule{Node: n},
		"AuthorityStub": &AuthorityStub{Node: n},
	}
}

func jsonKeys(t *testing.T, v any) map[string]any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal %T: %v", v, err)
	}
	out := map[string]any{}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal %T: %v", v, err)
	}
	return out
}

func TestOrchestrationJSON_NoPublishFields(t *testing.T) {
	for name, v := range publishFieldSamples() {
		if name == "Run" {
			continue
		}
		keys := jsonKeys(t, v)
		for _, dropped := range []string{"PublishedAt", "ScheduledAt"} {
			if _, ok := keys[dropped]; ok {
				t.Errorf("%s JSON has %s", name, dropped)
			}
		}
		for _, kept := range []string{"ID", "Slug", "Status", "CreatedAt", "UpdatedAt", "Rev"} {
			if _, ok := keys[kept]; !ok {
				t.Errorf("%s JSON lost %s: %v", name, kept, keys)
			}
		}
		if _, ok := keys["address"]; !ok {
			t.Errorf("%s JSON lost address: %v", name, keys)
		}
	}
	// A value and a pointer encode the same way.
	task := Task{Node: Node{ID: "t"}, Band: "core"}
	byValue, _ := json.Marshal(task)
	byPointer, _ := json.Marshal(&task)
	if string(byValue) != string(byPointer) || !strings.Contains(string(byValue), `"band":"core"`) {
		t.Errorf("value %s, pointer %s", byValue, byPointer)
	}
}

func TestOrchestrationJSON_RunAndContentKeepFields(t *testing.T) {
	at := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	for name, v := range map[string]any{
		"Run":      publishFieldSamples()["Run"],
		"testPost": &testPost{Node: Node{ID: "p", PublishedAt: at, ScheduledAt: &at}, Title: "x"},
	} {
		keys := jsonKeys(t, v)
		for _, kept := range []string{"PublishedAt", "ScheduledAt"} {
			if _, ok := keys[kept]; !ok {
				t.Errorf("%s JSON lost %s", name, kept)
			}
		}
	}
}

// TestOrchestrationJSON_EveryFlowWithoutPublish: a registered compiled type
// whose flow has neither a published nor a scheduled state drops both keys;
// one with no flow (Run) or with such a state keeps them.
func TestOrchestrationJSON_EveryFlowWithoutPublish(t *testing.T) {
	db := addressDB(t)
	if err := CreateAuthorityTables(db); err != nil {
		t.Fatal(err)
	}
	app := New(Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16"), DB: db})
	RegisterOrchestrationTypes(app, db)
	RegisterAuthorityTypes(app, db)
	samples := publishFieldSamples()
	ctx := context.Background()
	for _, d := range app.TypeRegistry().All() {
		if d.Kind != "compiled" {
			continue
		}
		v, ok := samples[d.Name]
		if !ok {
			t.Errorf("compiled type %s has no sample: add it to publishFieldSamples and decide its JSON fields", d.Name)
			continue
		}
		var flows, publishStates int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM smeldr_state_flows WHERE type_name = $1`, d.Name).Scan(&flows); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM smeldr_states s JOIN smeldr_state_flows f ON f.id = s.flow_id
			  WHERE f.type_name = $1 AND s.name IN ('published', 'scheduled')`, d.Name).Scan(&publishStates); err != nil {
			t.Fatal(err)
		}
		_, has := jsonKeys(t, v)["PublishedAt"]
		if wantDrop := flows > 0 && publishStates == 0; has == wantDrop {
			t.Errorf("%s: flows %d, publish states %d, PublishedAt emitted %v", d.Name, flows, publishStates, has)
		}
	}
}

func TestOrchestrationJSON_UnmarshalOldShape(t *testing.T) {
	var task Task
	old := `{"ID":"t1","Slug":"t1","Status":"backlog","PublishedAt":"0001-01-01T00:00:00Z","ScheduledAt":null,"Band":"core"}`
	if err := json.Unmarshal([]byte(old), &task); err != nil || task.ID != "t1" || task.Band != "core" {
		t.Errorf("old shape: %+v %v", task, err)
	}
}

// TestOrchestrationJSON_Surfaces checks the surfaces that build their own
// output around a Task: MCP get and list, a create result with warnings, a
// lifecycle webhook body, and a context packet.
func TestOrchestrationJSON_Surfaces(t *testing.T) {
	f := newReadSurfaceFixture(t)
	noPublish := func(where string, v any) {
		t.Helper()
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("%s: %v", where, err)
		}
		if s := string(b); strings.Contains(s, `"PublishedAt"`) || strings.Contains(s, `"ScheduledAt"`) {
			t.Errorf("%s emits a publish field: %s", where, s)
		}
		if !strings.Contains(string(b), f.seen.Slug) {
			t.Errorf("%s lost the Task: %s", where, b)
		}
	}
	got, err := f.tasks.MCPGet(f.global, f.seen.Slug)
	if err != nil {
		t.Fatal(err)
	}
	noPublish("MCPGet", got)
	list, err := f.tasks.MCPList(f.global)
	if err != nil {
		t.Fatal(err)
	}
	noPublish("MCPList", list)
	noPublish("create warnings", withCreateWarnings(f.seen, []CreateWarning{{Code: "title_missing"}}))
	payload, err := buildWebhookPayload("Task", f.seen, AfterUpdate)
	if err != nil {
		t.Fatal(err)
	}
	noPublish("webhook payload", json.RawMessage(payload))

	if err := CreateRelationTables(f.db); err != nil {
		t.Fatal(err)
	}
	rels, err := NewRelationStore(f.db)
	if err != nil {
		t.Fatal(err)
	}
	packet, err := BuildContextPacket(TrustedRead(context.Background()), f.db, rels, "https://example.com", "test", "task", f.seen.Slug, 1)
	if err != nil {
		t.Fatal(err)
	}
	noPublish("context packet", packet)
}

func TestParseTimestamp(t *testing.T) {
	want := time.Date(2026, 10, 15, 20, 23, 8, 72349009, time.UTC)
	cases := []struct {
		name string
		in   any
		want time.Time
	}{
		{"RFC3339Nano", "2026-10-15T20:23:08.072349009Z", want},
		{"RFC3339 offset", "2026-10-15T22:23:08.072349009+02:00", want},
		{"String form", "2026-10-15 20:23:08.072349009 +0000 UTC", want},
		{"String form, no fraction", "2026-10-15 20:23:08 +0000 UTC", want.Truncate(time.Second)},
		{"bytes", []byte("2026-10-15T20:23:08.072349009Z"), want},
		{"time in another zone", want.In(time.FixedZone("x", 3600)), want},
		{"unix seconds", int64(want.Unix()), want.Truncate(time.Second)},
		{"nil", nil, time.Time{}},
	}
	for _, c := range cases {
		got, err := ParseTimestamp(c.in)
		if err != nil || !got.Equal(c.want) || (!got.IsZero() && got.Location() != time.UTC) {
			t.Errorf("%s: %v %v; want %v in UTC", c.name, got, err, c.want)
		}
	}
	for _, bad := range []any{"yesterday", 3.5} {
		if got, err := ParseTimestamp(bad); err == nil || !got.IsZero() {
			t.Errorf("%v: %v %v; want an error and the zero time", bad, got, err)
		}
	}
}

func BenchmarkTaskMarshalJSON(b *testing.B) {
	task := publishFieldSamples()["Task"]
	b.ReportAllocs()
	for b.Loop() {
		if _, err := json.Marshal(task); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkRunMarshalJSON is the baseline for BenchmarkTaskMarshalJSON: Run
// embeds the same Node and RecordAddress and has no MarshalJSON.
func BenchmarkRunMarshalJSON(b *testing.B) {
	run := publishFieldSamples()["Run"]
	b.ReportAllocs()
	for b.Loop() {
		if _, err := json.Marshal(run); err != nil {
			b.Fatal(err)
		}
	}
}
