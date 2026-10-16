package smeldr

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// subscriptionApp is an App with the orchestration types and the subscription
// table, without governance: a Context's legacy role decides administer.
func subscriptionApp(t *testing.T) (*App, *sql.DB) {
	t.Helper()
	app, db := signalTestApp(t)
	if err := CreateSubscriptionTables(db); err != nil {
		t.Fatal(err)
	}
	return app, db
}

// asUser is a Context for user id with role.
func asUser(id string, role Role) Context { return NewTestContext(User{ID: id, Roles: []Role{role}}) }

func TestSubscription_OwnerRule(t *testing.T) {
	app, _ := subscriptionApp(t)
	u1, u2, adm := asUser("u1", Author), asUser("u2", Author), asUser("adm", Admin)
	s, err := app.CreateSubscription(u1, SubscriptionSpec{Name: "my tasks", Filter: SubscriptionFilter{Types: []string{"Task"}}})
	if err != nil {
		t.Fatal(err)
	}
	if s.Owner != "u1" || !strings.HasPrefix(s.Address, "N") || s.Delivery != DeliveryStream {
		t.Errorf("created %+v", s)
	}
	if got, err := app.GetSubscription(u1, s.Address); err != nil || got.ID != s.ID {
		t.Errorf("own by address: %v %v", got, err)
	}
	if _, err := app.GetSubscription(u2, s.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("another token: %v, want ErrNotFound", err)
	}
	if _, err := app.GetSubscription(adm, s.ID); err != nil {
		t.Errorf("administer: %v", err)
	}
	if _, err := app.CreateSubscription(u1, SubscriptionSpec{Owner: "u2", Name: "x"}); !errors.Is(err, ErrForbidden) {
		t.Errorf("for another owner: %v, want ErrForbidden", err)
	}
	if got, err := app.CreateSubscription(adm, SubscriptionSpec{Owner: "u2", Name: "for u2"}); err != nil || got.Owner != "u2" {
		t.Errorf("admin for another owner: %v %v", got, err)
	}
	if _, err := app.ListSubscriptions(u1, "u2"); !errors.Is(err, ErrForbidden) {
		t.Errorf("list another's: %v", err)
	}
	list, err := app.ListSubscriptions(u1, "")
	if err != nil || len(list) != 4 {
		t.Fatalf("list = %d, %v; want 3 defaults and 1", len(list), err)
	}
	name, paused := "renamed", true
	upd, err := app.UpdateSubscription(u1, s.ID, SubscriptionUpdate{Name: &name, Paused: &paused, Filter: &SubscriptionFilter{Events: []string{"created"}}})
	if err != nil || upd.Name != "renamed" || !upd.Paused || upd.Filter.Events[0] != "created" {
		t.Errorf("update = %+v, %v", upd, err)
	}
	if _, err := app.UpdateSubscription(u2, s.ID, SubscriptionUpdate{Name: &name}); !errors.Is(err, ErrNotFound) {
		t.Errorf("update another's: %v", err)
	}
	var def string
	for _, l := range list {
		if l.DefaultKind != "" {
			def = l.ID
		}
	}
	if err := app.DeleteSubscription(u1, def); !errors.Is(err, ErrConflict) {
		t.Errorf("delete a default: %v, want ErrConflict", err)
	}
	if _, err := app.UpdateSubscription(u1, def, SubscriptionUpdate{Filter: &SubscriptionFilter{}}); !errors.Is(err, ErrConflict) {
		t.Errorf("refilter a default: %v, want ErrConflict", err)
	}
	if _, err := app.UpdateSubscription(u1, def, SubscriptionUpdate{Paused: &paused}); err != nil {
		t.Errorf("pause a default: %v", err)
	}
	if err := app.DeleteSubscription(u1, s.ID); err != nil {
		t.Fatal(err)
	}
	if err := app.DeleteSubscription(u1, s.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleted twice: %v", err)
	}
	// The plain Go surface without governance is trusted.
	if _, err := app.CreateSubscription(context.Background(), SubscriptionSpec{Owner: "svc", Name: "trusted"}); err != nil {
		t.Errorf("trusted: %v", err)
	}
	if _, err := app.CreateSubscription(context.Background(), SubscriptionSpec{Name: "no owner"}); err == nil {
		t.Error("a trusted create without an owner passed")
	}
	if _, err := (&App{}).CreateSubscription(u1, SubscriptionSpec{Name: "x"}); !errors.Is(err, ErrInternal) {
		t.Errorf("no DB: %v", err)
	}
}

func TestSubscription_Validation(t *testing.T) {
	app, db := subscriptionApp(t)
	u1, adm := asUser("u1", Author), asUser("adm", Admin)
	task := mustTask(t, app)
	long := strings.Repeat("x", TitleMaxLength+1)
	cases := []struct {
		name string
		spec SubscriptionSpec
		want string // "validation:<field>", "forbidden", "notfound", "bad"
		as   Context
	}{
		{"empty name", SubscriptionSpec{}, "validation:name", u1},
		{"long name", SubscriptionSpec{Name: long}, "validation:name", u1},
		{"two lines", SubscriptionSpec{Name: "a\nb"}, "validation:name", u1},
		{"bad type", SubscriptionSpec{Name: "n", Filter: SubscriptionFilter{Types: []string{"Nope"}}}, "validation:filter", u1},
		{"bad event", SubscriptionSpec{Name: "n", Filter: SubscriptionFilter{Events: []string{"exploded"}}}, "validation:filter", u1},
		{"bad kind", SubscriptionSpec{Name: "n", Filter: SubscriptionFilter{SignalKinds: []SignalKind{"history"}}}, "validation:filter", u1},
		{"bad field", SubscriptionSpec{Name: "n", Filter: SubscriptionFilter{Fields: map[string][]string{"colour": {"red"}}}}, "validation:filter", u1},
		{"bad delivery", SubscriptionSpec{Name: "n", Delivery: "email"}, "validation:delivery", u1},
		{"webhook not admin", SubscriptionSpec{Name: "n", Delivery: "webhook:e1"}, "forbidden", u1},
		{"webhook unknown", SubscriptionSpec{Name: "n", Delivery: "webhook:e1"}, "validation:delivery", adm},
		{"item not a pair", SubscriptionSpec{Name: "n", Filter: SubscriptionFilter{Items: []string{"T1"}}}, "validation:filter", u1},
		{"item bad type", SubscriptionSpec{Name: "n", Filter: SubscriptionFilter{Items: []string{"Nope:1"}}}, "validation:filter", u1},
		{"item missing", SubscriptionSpec{Name: "n", Filter: SubscriptionFilter{Items: []string{"Task:missing"}}}, "notfound", u1},
		{"too many items", SubscriptionSpec{Name: "n", Filter: SubscriptionFilter{Items: make([]string, MaxSubscriptionItems+1)}}, "bad", u1},
		{"anchor no kind", SubscriptionSpec{Name: "n", Filter: SubscriptionFilter{RelatedTo: &SubscriptionAnchor{Anchor: "Task:" + task}}}, "validation:filter", u1},
		{"anchor bad direction", SubscriptionSpec{Name: "n", Filter: SubscriptionFilter{RelatedTo: &SubscriptionAnchor{Anchor: "Task:" + task, Kind: "k", Direction: "up"}}}, "validation:filter", u1},
		{"anchor missing", SubscriptionSpec{Name: "n", Filter: SubscriptionFilter{RelatedTo: &SubscriptionAnchor{Anchor: "Task:missing", Kind: "k"}}}, "notfound", u1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := app.CreateSubscription(tc.as, tc.spec)
			switch {
			case strings.HasPrefix(tc.want, "validation:"):
				wantValidation(t, err, strings.TrimPrefix(tc.want, "validation:"))
			case tc.want == "forbidden" && !errors.Is(err, ErrForbidden),
				tc.want == "notfound" && !errors.Is(err, ErrNotFound),
				tc.want == "bad" && !errors.Is(err, ErrBadRequest):
				t.Errorf("err = %v, want %s", err, tc.want)
			}
		})
	}
	s, err := app.CreateSubscription(u1, SubscriptionSpec{Name: "resolved", Filter: SubscriptionFilter{
		Items: []string{"Task:" + task}, RelatedTo: &SubscriptionAnchor{Anchor: "Task:" + task, Kind: "depends_on"},
		Fields: map[string][]string{"band": {"core"}}, SignalKinds: []SignalKind{SignalRequest}, Events: []string{"relation.asserted"}}})
	if err != nil || s.Filter.Items[0] != "Task:"+task || s.Filter.RelatedTo.AnchorType != "Task" {
		t.Fatalf("resolved = %+v, %v", s, err)
	}
	if _, err := db.Exec(`CREATE TABLE smeldr_webhook_endpoints (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO smeldr_webhook_endpoints (id) VALUES ('e1')`); err != nil {
		t.Fatal(err)
	}
	if s, err := app.CreateSubscription(adm, SubscriptionSpec{Name: "hook", Delivery: "webhook:e1"}); err != nil || s.Delivery != "webhook:e1" {
		t.Errorf("webhook delivery: %v %v", s, err)
	}
}

// mustTask creates a Task and returns its ID.
func mustTask(t *testing.T, app *App) string {
	t.Helper()
	out, err := findModule[*Task](t, app).MCPCreate(authorCtx(), map[string]any{"title": "a task", "band": "core"})
	if err != nil {
		t.Fatal(err)
	}
	return asItem[*Task](out).ID
}

func TestSubscription_Cap(t *testing.T) {
	app, db := subscriptionApp(t)
	u1 := asUser("u1", Author)
	for i := range MaxSubscriptionsPerOwner {
		if _, err := db.Exec(`INSERT INTO smeldr_subscriptions (id, slug, created_at, updated_at, name, owner) VALUES (?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, 'n', 'u1')`,
			fmt.Sprint("s", i), fmt.Sprint("s", i)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := app.CreateSubscription(u1, SubscriptionSpec{Name: "one more"}); !errors.Is(err, ErrBadRequest) {
		t.Errorf("past the cap: %v, want ErrBadRequest", err)
	}
}

// The binding: concurrent first uses give exactly three defaults.
func TestSubscription_DefaultsDoNotDuplicate(t *testing.T) {
	app, db := subscriptionApp(t)
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- app.ensureDefaultSubscriptions(context.Background(), "u1")
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM smeldr_subscriptions WHERE owner = 'u1' AND default_kind <> ''`).Scan(&n)
	if n != 3 {
		t.Errorf("defaults = %d, want 3", n)
	}
	// A duplicate default row is refused by the index and allocates nothing.
	s := &Subscription{Name: "dup", Owner: "u1", DefaultKind: SubscriptionRequestsToMe, Delivery: DeliveryStream}
	if ok, err := insertSubscription(context.Background(), db, s); ok || err != nil {
		t.Errorf("duplicate default: inserted %v, %v", ok, err)
	}
	if err := app.ensureDefaultSubscriptions(context.Background(), ""); err != nil {
		t.Errorf("no owner: %v", err)
	}
}

func TestSubscription_SeedRoleDefaults(t *testing.T) {
	app, _ := subscriptionApp(t)
	adm := asUser("adm", Admin)
	core, err := app.SeedRoleSubscriptions(adm, "core-token", RoleDefaults{Band: "core"})
	if err != nil || len(core) != 2 {
		t.Fatalf("band seed = %d, %v; want 2 (task_plan is not defined here)", len(core), err)
	}
	again, err := app.SeedRoleSubscriptions(adm, "core-token", RoleDefaults{Band: "core"})
	if err != nil || len(again) != 0 {
		t.Errorf("second seed = %d, %v; want none (idempotent)", len(again), err)
	}
	arch, err := app.SeedRoleSubscriptions(adm, "arch-token", RoleDefaults{Architect: true})
	if err != nil || len(arch) != 4 {
		t.Errorf("architect seed = %d, %v", len(arch), err)
	}
	if _, err := app.SeedRoleSubscriptions(asUser("u1", Author), "x", RoleDefaults{Band: "core"}); !errors.Is(err, ErrForbidden) {
		t.Errorf("not admin: %v", err)
	}
	if _, err := app.SeedRoleSubscriptions(adm, "x", RoleDefaults{}); !errors.Is(err, ErrBadRequest) {
		t.Errorf("empty defaults: %v", err)
	}
	if _, err := app.SeedRoleSubscriptions(adm, " ", RoleDefaults{Band: "core"}); err == nil {
		t.Error("no owner passed")
	}
}

func TestCompiledSubscription_Matches(t *testing.T) {
	names := set{"u1": true, "core": true, "approve": true}
	task := eventMeta{event: "task.transitioned", subjectType: "Task", subjectID: "t1", fromState: "backlog", toState: "active",
		fields: map[string]string{"band": "core", "priority": "0"}}
	req := eventMeta{event: "signal.created", subjectType: "Signal", subjectID: "s1", signalType: "question",
		fields: map[string]string{"receiver": "core", "sender": "architect"}}
	cond := eventMeta{event: "signal.created", subjectType: "Signal", subjectID: "s2", signalType: "conflict-detected",
		fields: map[string]string{"receiver": "approve", "sender": "system"}}
	rel := eventMeta{event: "relation.asserted", subjectType: "Task", subjectID: "t1", otherType: "Goal", otherID: "g1"}
	cases := []struct {
		name string
		f    SubscriptionFilter
		meta eventMeta
		want bool
	}{
		{"empty matches all", SubscriptionFilter{}, task, true},
		{"type", SubscriptionFilter{Types: []string{"Task"}}, task, true},
		{"type miss", SubscriptionFilter{Types: []string{"Goal"}}, task, false},
		{"relation other end type", SubscriptionFilter{Types: []string{"Goal"}}, rel, true},
		{"event kind", SubscriptionFilter{Events: []string{"transitioned"}}, task, true},
		{"relation kind", SubscriptionFilter{Events: []string{"relation.asserted"}}, rel, true},
		{"event miss", SubscriptionFilter{Events: []string{"created"}}, task, false},
		{"from", SubscriptionFilter{FromStates: []string{"backlog"}}, task, true},
		{"to miss", SubscriptionFilter{ToStates: []string{"done"}}, task, false},
		{"item", SubscriptionFilter{Items: []string{"Task:t1"}}, task, true},
		{"item other end", SubscriptionFilter{Items: []string{"Goal:g1"}}, rel, true},
		{"item miss", SubscriptionFilter{Items: []string{"Task:t9"}}, task, false},
		{"field", SubscriptionFilter{Fields: map[string][]string{"priority": {"0"}}}, task, true},
		{"field miss", SubscriptionFilter{Fields: map[string][]string{"band": {"site"}}}, task, false},
		{"request kind", SubscriptionFilter{SignalKinds: []SignalKind{SignalRequest}}, req, true},
		{"condition kind miss", SubscriptionFilter{SignalKinds: []SignalKind{SignalCondition}}, req, false},
		{"kind on a non-Signal", SubscriptionFilter{SignalKinds: []SignalKind{SignalRequest}}, task, false},
		{"addressed to me by band", SubscriptionFilter{AddressedToMe: true}, req, true},
		{"addressed to me by operation", SubscriptionFilter{AddressedToMe: true}, cond, true},
		{"not addressed to me", SubscriptionFilter{AddressedToMe: true}, eventMeta{fields: map[string]string{"receiver": "site"}}, false},
		{"sent by me miss", SubscriptionFilter{SentByMe: true}, req, false},
		{"conjunction", SubscriptionFilter{Types: []string{"Task"}, ToStates: []string{"active"}, Fields: map[string][]string{"band": {"core"}}}, task, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := compileSubscription(Subscription{Filter: tc.f}, nil).matches(tc.meta, names); got != tc.want {
				t.Errorf("matches = %v, want %v", got, tc.want)
			}
		})
	}
	related := compileSubscription(Subscription{Filter: SubscriptionFilter{RelatedTo: &SubscriptionAnchor{Anchor: "g1", Kind: "k"}}}, set{"Task:t1": true})
	if !related.matches(task, names) || related.matches(eventMeta{subjectType: "Task", subjectID: "t2"}, names) {
		t.Error("related_to")
	}
	if eventKind("plain") != "plain" {
		t.Error("eventKind without a dot")
	}
	// include_own is per subscription.
	cs := &connSubscriptions{owner: "u1", names: names, subs: []compiledSubscription{compileSubscription(Subscription{}, nil)}}
	if cs.match(eventMeta{actorID: "u1"}) || !cs.match(eventMeta{actorID: "u2"}) {
		t.Error("include_own off")
	}
	cs.subs[0].includeOwn = true
	if !cs.match(eventMeta{actorID: "u1"}) {
		t.Error("include_own on")
	}
}

func TestApp_EventFields(t *testing.T) {
	app, _ := subscriptionApp(t)
	if f := app.eventFields("Task", &Task{Band: "core", Priority: 0}); f["band"] != "core" || f["priority"] != "0" {
		t.Errorf("task %v", f)
	}
	if f := app.eventFields("Goal", &Goal{Band: "b"}); f["band"] != "b" {
		t.Errorf("goal %v", f)
	}
	if f := app.eventFields("Decision", &Decision{Scope: "s"}); f["scope"] != "s" {
		t.Errorf("decision %v", f)
	}
	if f := app.eventFields("Signal", &Signal{Receiver: "r", Sender: "x", SignalType: "question"}); f["receiver"] != "r" || f["signal_type"] != "question" {
		t.Errorf("signal %v", f)
	}
	if f := app.eventFields("Run", &Run{}); f != nil {
		t.Errorf("run %v", f)
	}
	if f := app.eventFields("nothing", &DynamicNode{}); f != nil {
		t.Errorf("unregistered dynamic %v", f)
	}
	if f := (&App{}).eventFields("x", &DynamicNode{}); f != nil {
		t.Errorf("no registry %v", f)
	}
}

// Under governance: a caller without a Context, or without an ID, is
// forbidden; administer comes from a grant; routing names include the held
// operations.
func TestSubscription_Governance(t *testing.T) {
	app, db, rs := setupTransitionItemApp(t)
	if err := CreateSubscriptionTables(db); err != nil {
		t.Fatal(err)
	}
	admin := setupTokenWithRole(t, db, rs, "admin")
	author := setupTokenWithRole(t, db, rs, "author")
	if _, err := app.CreateSubscription(context.Background(), SubscriptionSpec{Owner: "x", Name: "n"}); !errors.Is(err, ErrForbidden) {
		t.Errorf("no Context: %v", err)
	}
	if _, err := app.CreateSubscription(NewTestContext(User{}), SubscriptionSpec{Owner: "x", Name: "n"}); !errors.Is(err, ErrForbidden) {
		t.Errorf("no ID: %v", err)
	}
	if _, err := app.CreateSubscription(NewTestContext(User{ID: author}), SubscriptionSpec{Owner: admin, Name: "n"}); !errors.Is(err, ErrForbidden) {
		t.Errorf("author for another: %v", err)
	}
	if _, err := app.CreateSubscription(NewTestContext(User{ID: admin}), SubscriptionSpec{Owner: author, Name: "for author"}); err != nil {
		t.Errorf("admin for another: %v", err)
	}
	names, err := app.routingNames(context.Background(), admin, []Subscription{{Filter: SubscriptionFilter{Fields: map[string][]string{"receiver": {"core"}}}}})
	if err != nil || !names[admin] || !names["administer"] || !names["core"] {
		t.Errorf("routing names = %v, %v", names, err)
	}
	cs, named, err := app.compileForConnection(NewContextWithUser(User{ID: author}), author, "")
	if err != nil || named != nil || len(cs.subs) != 4 {
		t.Errorf("compiled %d subscriptions, %v", len(cs.subs), err)
	}
}

// Store failures are internal errors.
func TestSubscription_StoreFailures(t *testing.T) {
	app, db := subscriptionApp(t)
	u1 := asUser("u1", Author)
	s, err := app.CreateSubscription(u1, SubscriptionSpec{Name: "n"})
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range []string{"SELECT COUNT(*) FROM smeldr_subscriptions", "INSERT INTO smeldr_subscriptions", "UPDATE smeldr_subscriptions SET name",
		"DELETE FROM smeldr_subscriptions", "WHERE owner = $1 ORDER BY", "WHERE owner = $1 AND default_kind"} {
		app.cfg.DB = failAddressDB{db, step}
		name := "x"
		var err error
		switch {
		case strings.HasPrefix(step, "UPDATE"):
			_, err = app.UpdateSubscription(u1, s.ID, SubscriptionUpdate{Name: &name})
		case strings.HasPrefix(step, "DELETE"):
			err = app.DeleteSubscription(u1, s.ID)
		case strings.Contains(step, "ORDER BY"), strings.Contains(step, "default_kind"):
			_, err = app.ListSubscriptions(u1, "")
		default:
			_, err = app.CreateSubscription(u1, SubscriptionSpec{Name: "n"})
		}
		if !errors.Is(err, ErrInternal) {
			t.Errorf("%s fails: %v, want ErrInternal", step, err)
		}
	}
	app.cfg.DB = failAddressDB{db, "SELECT id, COALESCE(address"}
	if _, err := app.GetSubscription(u1, s.ID); !errors.Is(err, ErrInternal) {
		t.Errorf("get failure: %v", err)
	}
	app.cfg.DB = db
	if err := CreateSubscriptionTables(failAddressDB{db, "CREATE TABLE IF NOT EXISTS smeldr_subscriptions"}); err == nil {
		t.Error("a failed create passed")
	}
	if (&App{subscriptionsOn: true}).subscriptionSource() != nil {
		t.Error("a source without a DB")
	}
	bad := &App{subscriptionsOn: true, cfg: Config{DB: failAddressDB{db, "CREATE INDEX IF NOT EXISTS idx_smeldr_subscriptions_owner"}}}
	if bad.subscriptionSource() != nil {
		t.Error("a source without its table")
	}
}

// A runtime-defined type's channel field is a routing field; its value and a
// band field reach the event meta.
func TestSubscription_ChannelField(t *testing.T) {
	app, db := dynamicAddressApp(t)
	if err := CreateSubscriptionTables(db); err != nil {
		t.Fatal(err)
	}
	schema := &ContentTypeSchema{TypeName: "plan", Fields: json.RawMessage(
		`[{"Name":"Title","Type":"string","Role":"title"},{"Name":"lane","Type":"string","Role":"channel"},{"Name":"band","Type":"string"}]`)}
	if _, err := app.DefineContentType(context.Background(), schema); err != nil {
		t.Fatal(err)
	}
	if !app.isChannelField("lane") || app.isChannelField("colour") || (&App{}).isChannelField("lane") {
		t.Error("isChannelField")
	}
	if _, err := app.CreateSubscription(asUser("u1", Author), SubscriptionSpec{Name: "lane x", Filter: SubscriptionFilter{Fields: map[string][]string{"lane": {"x"}}}}); err != nil {
		t.Errorf("a channel field: %v", err)
	}
	f := app.eventFields("plan", &DynamicNode{Fields: json.RawMessage(`{"lane":"x","band":"b"}`)})
	if f["lane"] != "x" || f["band"] != "b" {
		t.Errorf("fields = %v", f)
	}
	if f := app.eventFields("plan", &DynamicNode{Fields: json.RawMessage(`{}`)}); f != nil {
		t.Errorf("no channel value: %v", f)
	}
}

// related_to is the one-hop set of a live asserted edge; no relation table is
// an empty set, and a query failure is internal.
func TestSubscription_RelatedSet(t *testing.T) {
	store := setupRelationStoreWithSignals(t)
	if err := store.UpsertKind(context.Background(), RelationKindDef{TypeName: "belongs_to", Mode: "asserted", Directional: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.MCPAssertRelation(context.Background(), "Task", "t1", "Goal", "g1", "belongs_to", nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	app := &App{cfg: Config{DB: store.db}}
	got, err := app.relatedSet(context.Background(), &SubscriptionAnchor{Anchor: "g1", Kind: "belongs_to"})
	if err != nil || !got["Task:t1"] {
		t.Errorf("related = %v, %v", got, err)
	}
	if got, err := app.relatedSet(context.Background(), nil); got != nil || err != nil {
		t.Errorf("no anchor = %v, %v", got, err)
	}
	if got, err := (&App{cfg: Config{DB: newSQLiteDB(t)}}).relatedSet(context.Background(), &SubscriptionAnchor{Anchor: "g", Kind: "k"}); err != nil || len(got) != 0 {
		t.Errorf("no table = %v, %v", got, err)
	}
	bad := &App{cfg: Config{DB: failAddressDB{store.db, "FROM smeldr_relations"}}}
	if _, err := bad.relatedSet(context.Background(), &SubscriptionAnchor{Anchor: "g1", Kind: "belongs_to", Direction: "outgoing"}); !errors.Is(err, ErrInternal) {
		t.Errorf("query failure: %v", err)
	}
}

// Under governance an item is resolved as the owner sees it, and the
// operator's own tooling seeds without a token.
func TestSubscription_OwnerScopeAndTrustedSeed(t *testing.T) {
	app, db, rs := setupTransitionItemApp(t)
	if err := CreateSubscriptionTables(db); err != nil {
		t.Fatal(err)
	}
	admin := setupTokenWithRole(t, db, rs, "admin")
	task := mustTask(t, app)
	if _, err := app.CreateSubscription(NewTestContext(User{ID: admin}), SubscriptionSpec{Owner: "nobody", Name: "n",
		Filter: SubscriptionFilter{Items: []string{"Task:" + task}}}); !errors.Is(err, ErrNotFound) {
		t.Errorf("an item the owner cannot read: %v, want ErrNotFound", err)
	}
	if _, err := app.SeedRoleSubscriptions(context.Background(), "core-token", RoleDefaults{Band: "core"}); !errors.Is(err, ErrForbidden) {
		t.Errorf("untrusted plain context: %v", err)
	}
	created, err := app.SeedRoleSubscriptionsTrusted(context.Background(), "core-token", RoleDefaults{Band: "core"})
	if err != nil || len(created) == 0 {
		t.Errorf("trusted seed = %d, %v", len(created), err)
	}
}

// The remaining error paths of the subscription API.
func TestSubscription_MoreErrors(t *testing.T) {
	app, db := subscriptionApp(t)
	u1 := asUser("u1", Author)
	s, err := app.CreateSubscription(u1, SubscriptionSpec{Name: "n"})
	if err != nil {
		t.Fatal(err)
	}
	empty, long := "", strings.Repeat("y", TitleMaxLength+1)
	if _, err := app.UpdateSubscription(u1, s.ID, SubscriptionUpdate{Name: &empty}); err == nil {
		t.Error("an empty name passed")
	}
	if _, err := app.UpdateSubscription(u1, s.ID, SubscriptionUpdate{Name: &long}); err == nil {
		t.Error("a long name passed")
	}
	if _, err := app.UpdateSubscription(u1, s.ID, SubscriptionUpdate{Filter: &SubscriptionFilter{Types: []string{"Nope"}}}); err == nil {
		t.Error("a bad filter passed")
	}
	yes := true
	if got, err := app.UpdateSubscription(u1, s.ID, SubscriptionUpdate{IncludeOwn: &yes}); err != nil || !got.IncludeOwn {
		t.Errorf("include_own: %v %v", got, err)
	}
	noDB := &App{}
	if _, err := noDB.ListSubscriptions(u1, ""); !errors.Is(err, ErrInternal) {
		t.Errorf("list no DB: %v", err)
	}
	if _, err := noDB.GetSubscription(u1, "x"); !errors.Is(err, ErrInternal) {
		t.Errorf("get no DB: %v", err)
	}
	if _, err := noDB.SeedRoleSubscriptions(asUser("a", Admin), "o", RoleDefaults{Band: "b"}); !errors.Is(err, ErrInternal) {
		t.Errorf("seed no DB: %v", err)
	}
	gov, _, _ := setupTransitionItemApp(t)
	if _, err := gov.ListSubscriptions(context.Background(), ""); !errors.Is(err, ErrForbidden) {
		t.Errorf("list without a caller under governance: %v", err)
	}
	if _, err := gov.GetSubscription(context.Background(), "x"); !errors.Is(err, ErrForbidden) {
		t.Errorf("get without a caller under governance: %v", err)
	}
	if _, err := gov.SeedRoleSubscriptions(context.Background(), "o", RoleDefaults{Band: "b"}); !errors.Is(err, ErrForbidden) {
		t.Errorf("seed without a caller: %v", err)
	}
	// Compiling: a paused subscription delivers nothing; a store failure is
	// returned.
	paused := true
	if _, err := app.UpdateSubscription(u1, s.ID, SubscriptionUpdate{Paused: &paused}); err != nil {
		t.Fatal(err)
	}
	cs, _, err := app.compileForConnection(u1, "u1", "")
	if err != nil || len(cs.subs) != 3 {
		t.Errorf("compiled %d, %v; want the 3 defaults (n is paused)", len(cs.subs), err)
	}
	if _, _, err := app.compileForConnection(u1, "u1", "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("a missing named subscription: %v", err)
	}
	app.cfg.DB = failAddressDB{db, "WHERE owner = $1 ORDER BY"}
	if _, _, err := app.compileForConnection(u1, "u1", ""); !errors.Is(err, ErrInternal) {
		t.Errorf("list failure: %v", err)
	}
	if _, _, err := app.compileForConnection(u1, "u1", s.ID); !errors.Is(err, ErrInternal) {
		t.Errorf("list failure, named: %v", err)
	}
	app.cfg.DB = failAddressDB{db, "WHERE owner = $1 AND default_kind"}
	if _, _, err := app.compileForConnection(u1, "u2", ""); !errors.Is(err, ErrInternal) {
		t.Errorf("defaults failure: %v", err)
	}
	app.cfg.DB = failAddressDB{db, "INSERT INTO smeldr_subscriptions"}
	if err := app.ensureDefaultSubscriptions(context.Background(), "fresh"); !errors.Is(err, ErrInternal) {
		t.Errorf("default insert failure: %v", err)
	}
	app.cfg.DB = db
	bad, err := app.CreateSubscription(u1, SubscriptionSpec{Name: "anchor", Filter: SubscriptionFilter{RelatedTo: &SubscriptionAnchor{Anchor: "Task:" + mustTask(t, app), Kind: "k"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE smeldr_relations (x TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := app.compileForConnection(u1, "u1", bad.ID); !errors.Is(err, ErrInternal) {
		t.Errorf("related set failure: %v", err)
	}
	if _, err := db.Exec(`UPDATE smeldr_subscriptions SET filter = 'not json' WHERE id = ?`, bad.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := app.GetSubscription(u1, bad.ID); !errors.Is(err, ErrInternal) {
		t.Errorf("a broken filter: %v", err)
	}
	if _, err := listSubscriptions(context.Background(), db, "u1"); !errors.Is(err, ErrInternal) {
		t.Errorf("list with a broken filter: %v", err)
	}
}

// The stream's scope loader reads nothing without governance and the user's
// scope with it; a connection over the token cap is refused.
func TestSubscriptionStream_ScopeAndCap(t *testing.T) {
	if s, err := (&App{}).streamScope(context.Background(), User{ID: "u"}); s != nil || err != nil {
		t.Errorf("no governance = %v, %v", s, err)
	}
	gov, _, _ := setupTransitionItemApp(t)
	if s, err := gov.streamScope(context.Background(), User{ID: "u"}); err != nil || s == nil || s.All() {
		t.Errorf("governance = %v, %v", s, err)
	}
	b := newEventBroadcaster()
	cs := &connSubscriptions{owner: "u"}
	for range eventStreamMaxSubscribersPerToken {
		if _, _, err := b.subscribeSubs("u", cs, nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := b.subscribeSubs("u", cs, nil); !errors.Is(err, ErrTooManyRequests) {
		t.Errorf("past the cap: %v", err)
	}
	b.recompileOwner("u")
	b.recompileOwner("u") // a pending signal is not doubled
	b.setSubs(make(chan []byte), cs)
}
