// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
)

// D113: references between records are resolved on write under the writer's
// read scope, stored canonically, write-once, and related by the system in the
// write's own transaction.

type refFixture struct {
	app     *App
	db      *sql.DB
	rels    *RelationStore
	rs      *RoleStore
	tasks   *Module[*Task]
	amends  *Module[*Amendment]
	signals *Module[*Signal]
	decs    *Module[*Decision]
	goals   *Module[*Goal]
	// global reads everything; reader reads only what a test grants.
	global, reader Context
}

func newRefFixture(t *testing.T) refFixture {
	t.Helper()
	ctx := context.Background()
	db := addressDB(t)
	setupTokensTable(t, db)
	if err := CreateBlockTables(db); err != nil {
		t.Fatal(err)
	}
	if err := CreateSchemaTable(db); err != nil {
		t.Fatal(err)
	}
	if err := CreateRelationTables(db); err != nil {
		t.Fatal(err)
	}
	app := New(Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16"), DB: db})
	app.ServeDynamicContent()
	RegisterOrchestrationTypes(app, db)
	rels, err := NewRelationStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := RegisterOrchestrationRelationKinds(ctx, rels); err != nil {
		t.Fatal(err)
	}
	app.Relations(rels)
	rs := NewRoleStore(db)
	if err := app.Governance(rs); err != nil {
		t.Fatal(err)
	}
	if err := RegisterScopedReaderRole(ctx, rs); err != nil {
		t.Fatal(err)
	}
	govGrant(t, rs, "global", "admin")
	_ = app.Handler()
	return refFixture{
		app: app, db: db, rels: rels, rs: rs,
		tasks: findModule[*Task](t, app), amends: findModule[*Amendment](t, app), signals: findModule[*Signal](t, app),
		decs: findModule[*Decision](t, app), goals: findModule[*Goal](t, app),
		global: NewTestContext(User{ID: "global"}), reader: NewTestContext(User{ID: "reader"}),
	}
}

func (f refFixture) grant(t *testing.T, patterns ...string) {
	t.Helper()
	if _, err := f.rs.Grant(context.Background(), RoleGrant{TokenID: "reader", RoleName: "scoped-reader", ScopeStatic: patterns}); err != nil {
		t.Fatal(err)
	}
}

func (f refFixture) decision(t *testing.T, title string) *Decision {
	t.Helper()
	out, err := f.decs.MCPCreate(f.global, map[string]any{"title": title, "body": "why"})
	if err != nil {
		t.Fatal(err)
	}
	return asItem[*Decision](out)
}

func (f refFixture) task(t *testing.T, fields map[string]any) *Task {
	t.Helper()
	if fields["title"] == nil {
		fields["title"] = "a task"
	}
	out, err := f.tasks.MCPCreate(f.global, fields)
	if err != nil {
		t.Fatalf("create Task: %v", err)
	}
	return asItem[*Task](out)
}

// liveEdges returns the live edges of kind from (sourceType, sourceID).
func (f refFixture) liveEdges(t *testing.T, sourceType, sourceID, kind string) []RelationEdge {
	t.Helper()
	edges, err := f.rels.GetLiveBySource(context.Background(), sourceType, sourceID, kind)
	if err != nil {
		t.Fatal(err)
	}
	return edges
}

func wantFieldError(t *testing.T, err error, field, text string) {
	t.Helper()
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("err = %v; want a ValidationError on %s", err, field)
	}
	if !strings.Contains(ve.Error(), field) || !strings.Contains(ve.Error(), text) {
		t.Errorf("err = %q; want field %s and %q", ve.Error(), field, text)
	}
}

func TestReferences_TaskImplements(t *testing.T) {
	f := newRefFixture(t)
	d := f.decision(t, "Lists return rows")
	gOut, err := f.goals.MCPCreate(f.global, map[string]any{"title": "Grow", "slug": "grow"})
	if err != nil {
		t.Fatal(err)
	}
	g := asItem[*Goal](gOut)

	byDecision := f.task(t, map[string]any{"implements": d.ID})
	if byDecision.Implements != d.Address {
		t.Errorf("implements stored as %q; want the address %q", byDecision.Implements, d.Address)
	}
	edges := f.liveEdges(t, "Task", byDecision.ID, "implements")
	if len(edges) != 1 || edges[0].TargetID != d.ID || edges[0].CreatedByJob == nil || *edges[0].CreatedByJob != referencesJob {
		t.Errorf("implements edges = %+v", edges)
	}
	if edges[0].CreatedBy == nil || *edges[0].CreatedBy != "global" {
		t.Errorf("the writer is not recorded on the edge: %+v", edges[0].CreatedBy)
	}

	byGoal := f.task(t, map[string]any{"implements": "grow"})
	if byGoal.Implements != g.Address || len(f.liveEdges(t, "Task", byGoal.ID, "derives_from")) != 1 {
		t.Errorf("a Goal by slug: implements %q, derives_from %v", byGoal.Implements, f.liveEdges(t, "Task", byGoal.ID, "derives_from"))
	}

	_, err = f.tasks.MCPCreate(f.global, map[string]any{"title": "x", "implements": "D999"})
	wantFieldError(t, err, "implements", `no Decision or Goal "D999" you can read`)

	// Write-once: the same target by another name is kept, a different one or
	// an empty value is refused.
	if _, err := f.tasks.MCPUpdate(f.global, byDecision.Slug, map[string]any{"implements": d.ID}); err != nil {
		t.Errorf("same target by ID: %v", err)
	}
	other := f.decision(t, "Another")
	_, err = f.tasks.MCPUpdate(f.global, byDecision.Slug, map[string]any{"implements": other.Address})
	wantFieldError(t, err, "implements", "write-once")
	_, err = f.tasks.MCPUpdate(f.global, byDecision.Slug, map[string]any{"implements": ""})
	wantFieldError(t, err, "implements", "cannot be cleared")

	// Set once on an update of a Task that had none.
	plain := f.task(t, map[string]any{})
	if _, err := f.tasks.MCPUpdate(f.global, plain.Slug, map[string]any{"implements": other.Address}); err != nil {
		t.Fatalf("set on update: %v", err)
	}
	if len(f.liveEdges(t, "Task", plain.ID, "implements")) != 1 {
		t.Error("an update that sets implements wrote no edge")
	}
}

func TestReferences_UnreadableIsMissing(t *testing.T) {
	f := newRefFixture(t)
	hidden := f.decision(t, "Hidden")
	f.grant(t, "Task:*")
	_, hiddenErr := f.tasks.MCPCreate(f.reader, map[string]any{"title": "x", "implements": hidden.Address})
	_, missingErr := f.tasks.MCPCreate(f.reader, map[string]any{"title": "x", "implements": "D999"})
	var hv, mv *ValidationError
	if !errors.As(hiddenErr, &hv) || !errors.As(missingErr, &mv) {
		t.Fatalf("hidden %v, missing %v; want ValidationErrors", hiddenErr, missingErr)
	}
	if strings.ReplaceAll(hv.Error(), `"`+hidden.Address+`"`, "X") != strings.ReplaceAll(mv.Error(), `"D999"`, "X") {
		t.Errorf("hidden %q and missing %q answer differently", hv.Error(), mv.Error())
	}
}

func TestReferences_AmendmentShipsAs(t *testing.T) {
	f := newRefFixture(t)
	task := f.task(t, map[string]any{"slug": "ship-me"})
	out, err := f.amends.MCPCreate(f.global, map[string]any{"summary": "s", "amendment_type": "fix", "task_ref": "ship-me"})
	if err != nil {
		t.Fatal(err)
	}
	a := asItem[*Amendment](out)
	if a.TaskRef != task.Address {
		t.Errorf("task_ref stored as %q; want %q", a.TaskRef, task.Address)
	}
	edges := f.liveEdges(t, "Task", task.ID, "ships_as")
	if len(edges) != 1 || edges[0].TargetID != a.ID {
		t.Errorf("ships_as edges = %+v", edges)
	}
	_, err = f.amends.MCPCreate(f.global, map[string]any{"summary": "s", "amendment_type": "fix", "task_ref": "T999"})
	wantFieldError(t, err, "task_ref", `no Task "T999" you can read`)
}

func TestReferences_SignalModule(t *testing.T) {
	f := newRefFixture(t)
	task := f.task(t, map[string]any{"slug": "about-me"})
	out, err := f.signals.MCPCreate(f.global, map[string]any{
		"sender": "core", "receiver": "architect", "signal_type": "question",
		"subject_type": "Task", "subject_id": "about-me", "task_ref": task.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	s := asItem[*Signal](out)
	if s.SubjectID != task.ID || s.TaskRef != task.Address {
		t.Errorf("stored subject %q task_ref %q; want the ID and the address", s.SubjectID, s.TaskRef)
	}
	if edges := f.liveEdges(t, "Signal", s.ID, "about"); len(edges) != 1 {
		t.Errorf("about edges = %+v; want one (subject and task_ref name the same Task)", edges)
	}

	// The binding (task_plan item-c087cdf5): the refusal says what to send.
	_, err = f.signals.MCPCreate(f.global, map[string]any{"sender": "core", "receiver": "architect", "signal_type": "question", "task_ref": "A185"})
	wantFieldError(t, err, "task_ref", `no Task "A185" you can read; send a Task's address (T273), slug or ID in task_ref, and name any other record in subject_type and subject_id`)

	_, err = f.signals.MCPCreate(f.global, map[string]any{"sender": "core", "receiver": "architect", "signal_type": "question", "subject_type": "Task"})
	wantFieldError(t, err, "subject_id", "is required with subject_type")
	_, err = f.signals.MCPCreate(f.global, map[string]any{"sender": "core", "receiver": "architect", "signal_type": "question", "subject_id": task.ID})
	wantFieldError(t, err, "subject_type", "is required with subject_id")
	_, err = f.signals.MCPCreate(f.global, map[string]any{"sender": "core", "receiver": "architect", "signal_type": "question", "subject_type": "Nope", "subject_id": "x"})
	wantFieldError(t, err, "subject_type", "names no registered content type")
}

func TestReferences_InsertSignal(t *testing.T) {
	f := newRefFixture(t)
	task := f.task(t, map[string]any{"slug": "insert-me"})
	hidden := f.task(t, map[string]any{"slug": "hidden"})

	// A plain internal context: names must resolve, no read scope applies.
	s := &Signal{Sender: "core", Receiver: "architect", SignalType: "question", SubjectType: "Task", SubjectID: "insert-me", TaskRef: "insert-me"}
	if err := InsertSignal(context.Background(), f.db, s); err != nil {
		t.Fatal(err)
	}
	if s.SubjectID != task.ID || s.TaskRef != task.Address || len(f.liveEdges(t, "Signal", s.ID, "about")) != 1 {
		t.Errorf("internal insert: subject %q task_ref %q edges %v", s.SubjectID, s.TaskRef, f.liveEdges(t, "Signal", s.ID, "about"))
	}
	err := InsertSignal(context.Background(), f.db, &Signal{Sender: "core", Receiver: "architect", SignalType: "question", TaskRef: "A185"})
	wantFieldError(t, err, "task_ref", "send a Task's address (T273)")

	// A caller with an identity is held to its read scope: hidden is missing.
	f.grant(t, "Task:"+task.ID, "Signal:*")
	errHidden := InsertSignal(f.reader, f.db, &Signal{Sender: "core", Receiver: "architect", SignalType: "question", SubjectType: "Task", SubjectID: hidden.ID})
	errMissing := InsertSignal(f.reader, f.db, &Signal{Sender: "core", Receiver: "architect", SignalType: "question", SubjectType: "Task", SubjectID: "nope"})
	if errHidden == nil || errMissing == nil ||
		strings.ReplaceAll(errHidden.Error(), hidden.ID, "X") != strings.ReplaceAll(errMissing.Error(), "nope", "X") {
		t.Errorf("hidden %v, missing %v; want the same refusal", errHidden, errMissing)
	}
	if err := InsertSignal(f.reader, f.db, &Signal{Sender: "core", Receiver: "architect", SignalType: "question", SubjectType: "Task", SubjectID: task.ID}); err != nil {
		t.Errorf("a readable subject: %v", err)
	}

	// Core's own Signals keep their subject as given, of a type with no table
	// too, and still get the edge.
	sys := &Signal{Sender: "system", Receiver: "approve", SignalType: "authorization-required", SubjectType: "GateItem", SubjectID: "g-1"}
	if err := InsertSignal(withSignalSystem(context.Background()), f.db, sys); err != nil {
		t.Fatal(err)
	}
	if edges := f.liveEdges(t, "Signal", sys.ID, "about"); len(edges) != 1 || edges[0].TargetID != "g-1" {
		t.Errorf("system edges = %+v", edges)
	}

	// Inside a caller's transaction: core's own Signal is kept, anyone else's
	// references are refused.
	tx, err := f.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := InsertSignal(withSignalSystem(context.Background()), tx, &Signal{Sender: "system", Receiver: "approve", SignalType: "authorization-required", SubjectType: "Task", SubjectID: task.ID}); err != nil {
		t.Errorf("system insert in a transaction: %v", err)
	}
	if err := InsertSignal(context.Background(), tx, &Signal{Sender: "core", Receiver: "architect", SignalType: "question", TaskRef: task.ID}); !errors.Is(err, ErrBadRequest) {
		t.Errorf("non-system references in a transaction: %v; want ErrBadRequest", err)
	}
}

func TestReferences_Dynamic(t *testing.T) {
	f := newRefFixture(t)
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
	node, _, err := repo.CreateDraftWithWarnings(f.global, "mcp", map[string]any{"title": "P", "task_ref": "planned"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(node.Fields), task.Address) {
		t.Errorf("fields = %s; want task_ref stored as %s", node.Fields, task.Address)
	}
	edges := f.liveEdges(t, "plan", node.ID, "refers_to")
	if len(edges) != 1 || edges[0].TargetID != task.ID || string(edges[0].Attributes) != `{"field":"task_ref"}` {
		t.Errorf("refers_to edges = %+v", edges)
	}
	_, _, err = repo.CreateDraftWithWarnings(f.global, "mcp", map[string]any{"title": "P", "task_ref": "T999"})
	wantFieldError(t, err, "task_ref", `no Task "T999" you can read`)

	// Write-once on update; set once on an item that had none.
	_, err = repo.UpdateFieldsWithWarnings(f.global, "mcp", node.ID, map[string]any{"task_ref": f.task(t, map[string]any{}).Address})
	wantFieldError(t, err, "task_ref", "write-once")
	_, err = repo.UpdateFieldsWithWarnings(f.global, "mcp", node.ID, map[string]any{"task_ref": ""})
	wantFieldError(t, err, "task_ref", "cannot be cleared")
	if _, err := repo.UpdateFieldsWithWarnings(f.global, "mcp", node.ID, map[string]any{"task_ref": task.ID, "title": "Q"}); err != nil {
		t.Errorf("same target by ID: %v", err)
	}
	empty, _, err := repo.CreateDraftWithWarnings(f.global, "mcp", map[string]any{"title": "E"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.UpdateFieldsWithWarnings(f.global, "mcp", empty.ID, map[string]any{"task_ref": "planned"}); err != nil {
		t.Fatal(err)
	}
	if len(f.liveEdges(t, "plan", empty.ID, "refers_to")) != 1 {
		t.Error("an update that sets the reference wrote no edge")
	}

	// Definition: a reference is a string field naming a registered type.
	bad := &ContentTypeSchema{TypeName: "bad1", Fields: []byte(`[{"name":"n","type":"integer","references":"Task"}]`)}
	if _, err := f.app.DefineContentType(ctx, bad); err == nil || !strings.Contains(err.Error(), "a reference is a string field") {
		t.Errorf("integer reference: %v", err)
	}
	bad = &ContentTypeSchema{TypeName: "bad2", Fields: []byte(`[{"name":"n","type":"string","references":"Nope"}]`)}
	if _, err := f.app.DefineContentType(ctx, bad); err == nil || !strings.Contains(err.Error(), "not a registered content type") {
		t.Errorf("unknown type: %v", err)
	}
	self := &ContentTypeSchema{TypeName: "node2", Fields: []byte(`[{"name":"title","type":"string","role":"title"},{"name":"parent","type":"string","references":"node2"}]`)}
	if _, err := f.app.DefineContentType(ctx, self); err != nil {
		t.Errorf("a self reference: %v", err)
	}
	// A redefinition may add a reference to an existing field (task_plan).
	next := &ContentTypeSchema{TypeName: "node2", Fields: []byte(`[{"name":"title","type":"string","role":"title"},{"name":"parent","type":"string","references":"Nope"}]`)}
	if _, err := f.app.RedefineContentType(ctx, next, "x"); err == nil || !strings.Contains(err.Error(), "not a registered content type") {
		t.Errorf("redefine to an unknown type: %v", err)
	}
}

func TestReferences_Transaction(t *testing.T) {
	f := newRefFixture(t)
	d := f.decision(t, "D")
	// An edge that cannot be written rolls the record back.
	if _, err := f.db.Exec(`CREATE TRIGGER refuse_edge BEFORE INSERT ON smeldr_relations BEGIN SELECT RAISE(ABORT, 'refused'); END`); err != nil {
		t.Fatal(err)
	}
	_, err := f.tasks.MCPCreate(f.global, map[string]any{"title": "rolled back", "slug": "rolled-back", "implements": d.Address})
	if !errors.Is(err, ErrInternal) {
		t.Errorf("edge failure: %v; want ErrInternal", err)
	}
	var n int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM smeldr_tasks WHERE slug = 'rolled-back'`).Scan(&n); err != nil || n != 0 {
		t.Errorf("tasks after a refused edge = %d %v; want none", n, err)
	}
	if _, err := f.db.Exec(`DROP TRIGGER refuse_edge`); err != nil {
		t.Fatal(err)
	}

	// A target gone between the check and the transaction refuses the write.
	if _, err := f.app.referenceTable(context.Background(), "Decision"); err != nil {
		t.Fatal(err)
	}
	gone := &Task{Node: Node{ID: "t-gone"}, Implements: "D999"}
	err = f.app.writeReferenceEdges(context.Background(), f.db, taskReferences(), nil, gone)
	wantFieldError(t, err, "implements", "D999")
}

func TestReferences_WithoutRelationStore(t *testing.T) {
	app, db := signalTestApp(t)
	tasks := findModule[*Task](t, app)
	decs := findModule[*Decision](t, app)
	out, err := decs.MCPCreate(NewTestContext(User{ID: "a", Roles: []Role{Admin}}), map[string]any{"title": "D"})
	if err != nil {
		t.Fatal(err)
	}
	d := asItem[*Decision](out)
	tOut, err := tasks.MCPCreate(NewTestContext(User{ID: "a", Roles: []Role{Admin}}), map[string]any{"title": "T", "implements": d.ID})
	if err != nil {
		t.Fatalf("checked without a relation store: %v", err)
	}
	if asItem[*Task](tOut).Implements != d.Address {
		t.Errorf("stored %q; want %q", asItem[*Task](tOut).Implements, d.Address)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name = 'smeldr_relations'`).Scan(&n); err == nil && n > 0 {
		if err := db.QueryRow(`SELECT COUNT(*) FROM smeldr_relations`).Scan(&n); err != nil || n != 0 {
			t.Errorf("edges = %d %v; want none without a relation store", n, err)
		}
	}
}

func TestReferences_SignalEndsInWalks(t *testing.T) {
	f := newRefFixture(t)
	ctx := context.Background()
	seen := f.task(t, map[string]any{"slug": "seen"})
	hidden := f.task(t, map[string]any{"slug": "hidden"})
	gOut, err := f.goals.MCPCreate(f.global, map[string]any{"title": "G"})
	if err != nil {
		t.Fatal(err)
	}
	g := asItem[*Goal](gOut)
	aboutSeen := &Signal{Sender: "core", Receiver: "architect", SignalType: "question", SubjectType: "Task", SubjectID: seen.ID}
	aboutHidden := &Signal{Sender: "core", Receiver: "architect", SignalType: "question", SubjectType: "Task", SubjectID: hidden.ID}
	for _, s := range []*Signal{aboutSeen, aboutHidden} {
		if err := InsertSignal(ctx, f.db, s); err != nil {
			t.Fatal(err)
		}
		if err := f.rels.Assert(ctx, RelationEdge{SourceType: "Goal", SourceID: g.ID, TargetType: "Signal", TargetID: s.ID, RelationKind: "contains", EdgeClass: "asserted"}); err != nil {
			t.Fatal(err)
		}
	}
	f.grant(t, "Goal:*", "Signal:*", "Task:"+seen.ID)

	edges, err := f.rels.MCPGetRelations(f.reader, "Goal", g.ID, "source", "contains")
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 1 || edges[0].TargetID != aboutSeen.ID {
		t.Errorf("relations = %v; want only the Signal whose subject is readable", edgePairs(edges))
	}
	reach, err := f.rels.Reachability(f.reader, "Goal", g.ID, "contains", "outgoing", 2)
	if err != nil {
		t.Fatal(err)
	}
	var reached []string
	for _, ring := range reach.Rings {
		for _, n := range ring.Items {
			reached = append(reached, n.ID)
		}
	}
	if strings.Contains(strings.Join(reached, ","), aboutHidden.ID) || !strings.Contains(strings.Join(reached, ","), aboutSeen.ID) {
		t.Errorf("reachability = %v; want the readable-subject Signal only", reached)
	}
	// A Signal anchor whose subject is hidden reaches nothing.
	if r, err := f.rels.Reachability(f.reader, "Signal", aboutHidden.ID, "about", "outgoing", 1); err != nil || len(r.Rings) > 0 && len(r.Rings[0].Items) > 0 {
		t.Errorf("hidden Signal anchor = %+v %v", r, err)
	}
	if all, err := f.rels.MCPGetRelations(f.global, "Goal", g.ID, "source", "contains"); err != nil || len(all) != 2 {
		t.Errorf("global relations = %d %v; want both", len(all), err)
	}
}

func TestReferences_AnswerRef(t *testing.T) {
	f := newRefFixture(t)
	ctx := context.Background()
	task := f.task(t, map[string]any{"slug": "the-change"})
	q := &Signal{Sender: "core", Receiver: "architect", SignalType: "question"}
	if err := InsertSignal(ctx, f.db, q); err != nil {
		t.Fatal(err)
	}
	if err := f.app.AnswerSignal(f.global, q.ID, "done", "Task:the-change"); err != nil {
		t.Fatal(err)
	}
	edges := f.liveEdges(t, "Signal", q.ID, "about")
	if len(edges) != 1 || edges[0].TargetID != task.ID || string(edges[0].Attributes) != `{"field":"answer_ref"}` {
		t.Errorf("answer_ref edges = %+v", edges)
	}
	q2 := &Signal{Sender: "core", Receiver: "architect", SignalType: "question"}
	if err := InsertSignal(ctx, f.db, q2); err != nil {
		t.Fatal(err)
	}
	err := f.app.AnswerSignal(f.global, q2.ID, "done", "Task:nope")
	wantFieldError(t, err, "answer_ref", `no Task "nope" you can read; name the change as Type:address`)
}

func TestReferences_LookupAndTables(t *testing.T) {
	f := newRefFixture(t)
	ctx := context.Background()
	if _, err := f.tasks.MCPCreate(f.global, map[string]any{"title": "a", "slug": "dup"}); err != nil {
		t.Fatal(err)
	}
	rt, err := recordTableOf(ctx, f.db, "Task")
	if err != nil || rt.name != "smeldr_tasks" || !rt.address {
		t.Fatalf("recordTableOf(Task) = %+v %v", rt, err)
	}
	// A type with no table misses every lookup.
	none, err := recordTableOf(ctx, f.db, "Nothing")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := none.lookup(ctx, f.db, "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("no table: %v", err)
	}
	if _, _, err := rt.lookup(ctx, f.db, "  "); !errors.Is(err, ErrNotFound) {
		t.Errorf("blank ident: %v", err)
	}
	// The legacy number is the last step.
	legacy := f.task(t, map[string]any{"task_id": "T-OLD-1"})
	if id, _, err := rt.lookup(ctx, f.db, "T-OLD-1"); err != nil || id != legacy.ID {
		t.Errorf("legacy lookup = %q %v; want %q", id, err, legacy.ID)
	}
	// A store failure is internal.
	failing := queryHookDB{DB: f.db, match: `FROM "smeldr_tasks" WHERE "slug"`, fail: true}
	if _, _, err := rt.lookup(ctx, &failing, "dup"); !errors.Is(err, ErrInternal) {
		t.Errorf("failing lookup: %v", err)
	}
}

func TestEnsureReferenceColumns(t *testing.T) {
	ctx := context.Background()
	db := newSQLiteDB(t)
	if err := EnsureReferenceColumns(ctx, db); err != nil {
		t.Fatalf("no tables: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE smeldr_tasks (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE smeldr_amendments (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := EnsureReferenceColumns(ctx, db); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range [][2]string{{"smeldr_tasks", "implements"}, {"smeldr_amendments", "task_ref"}} {
		if ok, err := columnExists(ctx, db, c[0], c[1]); err != nil || !ok {
			t.Errorf("%s.%s missing: %v", c[0], c[1], err)
		}
	}
	failing := queryHookDB{DB: db, match: `"smeldr_tasks"`, fail: true}
	if err := EnsureReferenceColumns(ctx, &failing); err == nil {
		t.Error("a failing table check returned nil")
	}
}
