// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// defineRoutedType registers a runtime-defined type whose "band" field has
// role "channel" (routed) or no role (unrouted), and returns its name.
func defineRoutedType(t *testing.T, e *d107Env, typeName string, routed bool) string {
	t.Helper()
	if err := CreateBlockTables(e.db); err != nil {
		t.Fatal(err)
	}
	if err := CreateSchemaTable(e.db); err != nil {
		t.Fatal(err)
	}
	band := SchemaField{Name: "band", Type: "string"}
	if routed {
		band.Role = "channel"
	}
	fields, _ := json.Marshal([]SchemaField{{Name: "Title", Type: "string", Required: true, Role: "title"}, band})
	desc, err := e.app.DefineContentType(context.Background(), &ContentTypeSchema{TypeName: typeName, Fields: fields})
	if err != nil {
		t.Fatalf("DefineContentType: %v", err)
	}
	return desc.Name
}

// routeSub is one stream connection and the events it received, by
// "<event>:<id>".
type routeSub struct {
	ch  chan []byte
	got map[string]int
}

func (s *routeSub) drain(t *testing.T) {
	t.Helper()
	for {
		select {
		case raw := <-s.ch:
			var p WebhookEventPayload
			if err := json.Unmarshal(raw, &p); err != nil {
				t.Fatal(err)
			}
			var d struct {
				ID string `json:"id"`
			}
			_ = json.Unmarshal(p.Data, &d)
			s.got[p.Event+":"+d.ID]++
		default:
			return
		}
	}
}

// publishDynamic creates an item of typeName with band and publishes it
// through transition_item, then returns its id once the async events had time
// to arrive.
func publishDynamic(t *testing.T, e *d107Env, typeName, band string) string {
	t.Helper()
	repo, err := e.app.DynamicContentRepo(typeName)
	if err != nil {
		t.Fatal(err)
	}
	node, err := repo.WithProvenance(nil).CreateDraft(context.Background(), map[string]any{"Title": "Item " + NewID(), "band": band})
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	if _, err := e.app.TransitionItemVia(hookCtx(), "mcp", typeName, node.Slug, string(Published), ""); err != nil {
		t.Fatalf("TransitionItemVia: %v", err)
	}
	e.settle(t, map[string]int{"listen " + string(AfterTransition) + ":" + node.ID: 1})
	return node.ID
}

// TestDynamicRouting_BandAndTypeTopic: a routed type's transitioned and status
// events reach the band, the type topic and "all", once per connection however
// many of its channels match, and no other band.
func TestDynamicRouting_BandAndTypeTopic(t *testing.T) {
	e := newD107Env(t)
	typeName := defineRoutedType(t, e, "plan", true)
	subs := map[string]*routeSub{}
	for _, c := range []string{"core", "cloud", "type:" + typeName, "all", "core,type:" + typeName, "architect, type:" + typeName} {
		ch, err := e.app.eventBroadcaster.subscribe("w-"+c, c)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { e.app.eventBroadcaster.unsubscribe(ch) })
		subs[c] = &routeSub{ch: ch, got: map[string]int{}}
	}
	id := publishDynamic(t, e, typeName, "core")
	want := map[string]int{"core": 1, "cloud": 0, "type:" + typeName: 1, "all": 1, "core,type:" + typeName: 1, "architect, type:" + typeName: 1}
	for c, s := range subs {
		s.drain(t)
		for _, ev := range []string{typeName + ".transitioned", typeName + ".published"} {
			if got := s.got[ev+":"+id]; got != want[c] {
				t.Errorf("%q got %s %d times; want %d", c, ev, got, want[c])
			}
		}
	}
}

// TestDynamicRouting_EmptyValueGoesToTheTopicOnly: an item with no band wakes
// no role.
func TestDynamicRouting_EmptyValueGoesToTheTopicOnly(t *testing.T) {
	e := newD107Env(t)
	typeName := defineRoutedType(t, e, "plan", true)
	core, _ := e.app.eventBroadcaster.subscribe("w-core", "core")
	topic, _ := e.app.eventBroadcaster.subscribe("w-topic", "type:"+typeName)
	t.Cleanup(func() { e.app.eventBroadcaster.unsubscribe(core); e.app.eventBroadcaster.unsubscribe(topic) })
	id := publishDynamic(t, e, typeName, "")
	c, tp := &routeSub{ch: core, got: map[string]int{}}, &routeSub{ch: topic, got: map[string]int{}}
	c.drain(t)
	tp.drain(t)
	if c.got[typeName+".transitioned:"+id] != 0 || tp.got[typeName+".transitioned:"+id] != 1 {
		t.Errorf("core %v, topic %v; want the topic only", c.got, tp.got)
	}
}

// TestDynamicRouting_UnroutedTypeIsABroadcast: a type without a channel field
// reaches every connection, as before.
func TestDynamicRouting_UnroutedTypeIsABroadcast(t *testing.T) {
	e := newD107Env(t)
	typeName := defineRoutedType(t, e, "memo", false)
	cloud, _ := e.app.eventBroadcaster.subscribe("w-cloud", "cloud")
	t.Cleanup(func() { e.app.eventBroadcaster.unsubscribe(cloud) })
	id := publishDynamic(t, e, typeName, "core")
	s := &routeSub{ch: cloud, got: map[string]int{}}
	s.drain(t)
	if s.got[typeName+".transitioned:"+id] != 1 {
		t.Errorf("cloud got %v; want the broadcast", s.got)
	}
}

// TestDynamicChannels covers the lookup edge cases directly.
func TestDynamicChannels(t *testing.T) {
	e := newD107Env(t)
	typeName := defineRoutedType(t, e, "plan", true)
	node := func(fields string) *DynamicNode { return &DynamicNode{Fields: json.RawMessage(fields)} }
	cases := []struct {
		name string
		app  *App
		typ  string
		node *DynamicNode
		want []string
	}{
		{"band", e.app, typeName, node(`{"band":"core"}`), []string{"core", "type:" + typeName}},
		{"blank band", e.app, typeName, node(`{"band":"  "}`), []string{"type:" + typeName}},
		{"band not a string", e.app, typeName, node(`{"band":7}`), []string{"type:" + typeName}},
		{"unreadable fields", e.app, typeName, node(`not json`), []string{"type:" + typeName}},
		{"unknown type", e.app, "nope", node(`{"band":"core"}`), nil},
		{"no registry", &App{}, typeName, node(`{"band":"core"}`), nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.app.eventChannels(c.typ, c.node); !reflect.DeepEqual(got, c.want) {
				t.Errorf("channels = %v; want %v", got, c.want)
			}
		})
	}
	if got := routeField(nil); got != "" {
		t.Errorf("routeField(nil) = %q", got)
	}
	if got := routeField(&ContentTypeSchema{Fields: json.RawMessage(`bad`)}); got != "" {
		t.Errorf("routeField(bad) = %q", got)
	}
}

// TestValidateSchemaDef_ChannelRole: the role is a string field, once.
func TestValidateSchemaDef_ChannelRole(t *testing.T) {
	def := func(fields ...SchemaField) *ContentTypeSchema {
		b, _ := json.Marshal(fields)
		return &ContentTypeSchema{TypeName: "x", Fields: b}
	}
	if err := ValidateSchemaDef(def(SchemaField{Name: "band", Type: "string", Role: "channel"})); err != nil {
		t.Errorf("string channel field refused: %v", err)
	}
	if err := ValidateSchemaDef(def(SchemaField{Name: "n", Type: "integer", Role: "channel"})); err == nil || !strings.Contains(err.Error(), "must be a string") {
		t.Errorf("integer channel field: %v", err)
	}
	if err := ValidateSchemaDef(def(SchemaField{Name: "a", Type: "string", Role: "channel"}, SchemaField{Name: "b", Type: "string", Role: "channel"})); err == nil || !strings.Contains(err.Error(), "second field") {
		t.Errorf("two channel fields: %v", err)
	}
	if err := ValidateSchemaDef(def(SchemaField{Name: "t", Type: "string", Role: "nope"})); err == nil || !strings.Contains(err.Error(), "channel)") {
		t.Errorf("unknown role message: %v", err)
	}
}

// TestParseChannels pins the ?channel= list.
func TestParseChannels(t *testing.T) {
	cases := map[string][]string{
		"":                   {"all"},
		"core":               {"core"},
		"core,type:plan":     {"core", "type:plan"},
		" core , ,type:plan": {"core", "type:plan"},
		",,":                 {"all"},
		"core,all":           {"all"},
	}
	for in, want := range cases {
		if got := parseChannels(in); !reflect.DeepEqual(got, want) {
			t.Errorf("parseChannels(%q) = %v; want %v", in, got, want)
		}
	}
}

// TestPublishToFrom: once per subscriber, the self-skip still applies, and no
// channels is a broadcast.
func TestPublishToFrom(t *testing.T) {
	b := newEventBroadcaster()
	both, _ := b.subscribe("u-both", "a,b")
	other, _ := b.subscribe("u-other", "c")
	own, _ := b.subscribe("u-own", "a")
	b.publishToFrom("u-own", []string{"a", "b"}, []byte(`{"event":"x"}`))
	if len(both) != 1 || len(other) != 0 || len(own) != 0 {
		t.Errorf("deliveries: both %d (want 1), other %d (want 0), own %d (want 0, self-skip)", len(both), len(other), len(own))
	}
	b.publishToFrom("", nil, []byte(`{"event":"y"}`))
	if len(other) != 1 {
		t.Errorf("no channels did not broadcast: other %d", len(other))
	}
}
