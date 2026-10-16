// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"errors"
	"testing"
)

// The store-failure paths of D113's reference checks, edges, walks and
// backfill: each is an internal error (or, for a walk, the walk's error), and
// nothing half-written is left behind.

func TestReferences_MigrateFailures(t *testing.T) {
	ctx := context.Background()
	f := newRefFixture(t)
	seedOldReferences(t, f)
	opts := ReferenceMigrationOptions{RefFields: map[string]string{"plan.task_ref": "Task"}}
	for _, c := range []struct {
		name string
		hook queryHookDB
	}{
		{"relations probe", queryHookDB{match: `FROM "smeldr_relations" WHERE 1=0`, fail: true}},
		{"schemas probe", queryHookDB{match: `FROM "smeldr_content_type_schemas" WHERE 1=0`, fail: true}},
		{"schemas read", queryHookDB{match: `FROM smeldr_content_type_schemas WHERE kind`, fail: true}},
		{"signals probe", queryHookDB{match: `FROM "smeldr_signals" WHERE 1=0`, fail: true}},
		{"task table facts", queryHookDB{match: `"smeldr_tasks"."address"`, fail: true}},
		{"subject types scan", queryHookDB{match: "SELECT DISTINCT subject_type", replace: "SELECT NULL"}},
		{"note_ref scan", queryHookDB{match: "note_ref FROM smeldr_tasks", replace: "SELECT 1, 2"}},
		{"signal scan", queryHookDB{match: "FROM smeldr_signals WHERE TRIM(subject_id)", replace: "SELECT 1, 2"}},
		{"dynamic scan", queryHookDB{match: "FROM smeldr_dynamic_content WHERE type_name", replace: "SELECT 1, 2"}},
		{"lookup", queryHookDB{match: `FROM "smeldr_decisions" WHERE "address"`, fail: true}},
	} {
		t.Run(c.name, func(t *testing.T) {
			hook := c.hook
			hook.DB = f.db
			if _, err := MigrateReferences(ctx, &hook, opts); err == nil {
				t.Error("returned nil")
			}
		})
	}
	// A note_ref matching two records by their old number is reported, not
	// guessed.
	for _, id := range []string{"d-x1", "d-x2"} {
		if err := NewSQLRepo[*Decision](f.db, Table("smeldr_decisions")).Save(ctx, &Decision{Node: Node{ID: id, Slug: id, Status: "proposed"}, DecisionNumber: "D-OLD"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := NewSQLRepo[*Task](f.db, Table("smeldr_tasks")).Save(ctx, &Task{Node: Node{ID: "t-amb", Slug: "t-amb", Status: "backlog"}, NoteRef: "D-OLD"}); err != nil {
		t.Fatal(err)
	}
	rep, err := MigrateReferences(ctx, f.db, ReferenceMigrationOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, n := range rep.Unresolved {
		found = found || (n.ID == "t-amb" && n.Field == "note_ref")
	}
	if !found {
		t.Errorf("an ambiguous note_ref is not reported: %+v", rep.Unresolved)
	}
	// A rewrite that fails rolls back (a fresh database: the hook cases above
	// ran without a transaction).
	f = newRefFixture(t)
	seedOldReferences(t, f)
	if _, err := f.db.Exec(`CREATE TRIGGER refuse_rewrite BEFORE UPDATE ON smeldr_signals BEGIN SELECT RAISE(ABORT, 'refused'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := MigrateReferences(ctx, f.db, ReferenceMigrationOptions{}); !errors.Is(err, ErrInternal) {
		t.Errorf("failing rewrite: %v; want ErrInternal", err)
	}
}

func TestReferences_InsertSignalFailures(t *testing.T) {
	ctx := context.Background()
	f := newRefFixture(t)
	task := f.task(t, map[string]any{"slug": "x"})
	sig := func() *Signal {
		return &Signal{Sender: "core", Receiver: "architect", SignalType: "question", SubjectType: "Task", SubjectID: task.ID}
	}
	for _, c := range []struct {
		name string
		hook queryHookDB
	}{
		{"relations probe", queryHookDB{match: `FROM "smeldr_relations" WHERE 1=0`, fail: true}},
		{"task table facts", queryHookDB{match: `"smeldr_tasks"."address"`, fail: true}},
	} {
		t.Run(c.name, func(t *testing.T) {
			hook := c.hook
			hook.DB = f.db
			if err := InsertSignal(ctx, &hook, sig()); err == nil {
				t.Error("returned nil")
			}
		})
	}
	// An edge that cannot be written fails the insert.
	if _, err := f.db.Exec(`CREATE TRIGGER refuse_edge BEFORE INSERT ON smeldr_relations BEGIN SELECT RAISE(ABORT, 'refused'); END`); err != nil {
		t.Fatal(err)
	}
	if err := InsertSignal(ctx, f.db, sig()); !errors.Is(err, ErrInternal) {
		t.Errorf("a refused edge: %v; want ErrInternal", err)
	}
	if _, err := f.db.Exec(`DROP TRIGGER refuse_edge`); err != nil {
		t.Fatal(err)
	}
	// An attached read scope is used; one that cannot read the subject hides it.
	scoped := WithReadScope(ctx, &ReadScope{})
	if err := InsertSignal(scoped, f.db, sig()); err == nil {
		t.Error("a scope that reads nothing let the subject through")
	}
	// The scope of an identified caller that cannot be loaded is an error.
	broken := queryHookDB{DB: f.db, match: "smeldr_role_grants", fail: true}
	if err := InsertSignal(f.reader, &broken, sig()); err == nil {
		t.Error("a failing scope load returned nil")
	}
}

func TestReferences_DynamicFailures(t *testing.T) {
	ctx := context.Background()
	f := newRefFixture(t)
	f.task(t, map[string]any{"slug": "y"})
	plan := &ContentTypeSchema{TypeName: "fplan", Fields: []byte(`[{"name":"title","type":"string","role":"title"},{"name":"task_ref","type":"string","references":"Task"}]`)}
	if _, err := f.app.DefineContentType(ctx, plan); err != nil {
		t.Fatal(err)
	}
	repo, err := f.app.DynamicContentRepo("fplan")
	if err != nil {
		t.Fatal(err)
	}
	// An edge that cannot be written rolls the item back.
	if _, err := f.db.Exec(`CREATE TRIGGER refuse_edge BEFORE INSERT ON smeldr_relations BEGIN SELECT RAISE(ABORT, 'refused'); END`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.CreateDraftWithWarnings(f.global, "mcp", map[string]any{"title": "F", "task_ref": "y"}); err == nil {
		t.Error("a refused edge did not fail the create")
	}
	empty, _, err := repo.CreateDraftWithWarnings(f.global, "mcp", map[string]any{"title": "E"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.UpdateFieldsWithWarnings(f.global, "mcp", empty.ID, map[string]any{"task_ref": "y"}); err == nil {
		t.Error("a refused edge did not fail the update")
	}
	if _, err := f.db.Exec(`DROP TRIGGER refuse_edge`); err != nil {
		t.Fatal(err)
	}
	// A lookup failure inside the write is internal.
	repo.db = &queryHookDB{DB: f.db, match: `FROM "smeldr_tasks" WHERE "address"`, fail: true}
	if _, err := repo.UpdateFieldsWithWarnings(f.global, "mcp", empty.ID, map[string]any{"task_ref": "T1"}); err == nil {
		t.Error("a failing lookup returned nil")
	}
	// Without a relation store the reference is checked and stored, with no edge.
	f.app.relationStore = nil
	repo.db = f.db
	if _, _, err := repo.CreateDraftWithWarnings(f.global, "mcp", map[string]any{"title": "N", "task_ref": "y"}); err != nil {
		t.Errorf("no relation store: %v", err)
	}
	// A repository without an App stores references as given.
	bare := NewDynamicTypeRepo(f.db, "fplan", repo.schema)
	if refs, err := bare.prepareReferences(ctx, nil, map[string]any{"task_ref": "anything"}); err != nil || refs != nil {
		t.Errorf("bare repo = %v %v", refs, err)
	}
	// checkSchemaReferences leaves bad JSON to ValidateSchemaDef.
	if err := f.app.checkSchemaReferences(&ContentTypeSchema{TypeName: "x", Fields: []byte(`{`)}); err != nil {
		t.Errorf("bad JSON: %v", err)
	}
}

func TestReferences_WalkFailures(t *testing.T) {
	ctx := context.Background()
	f := newRefFixture(t)
	task := f.task(t, map[string]any{"slug": "w"})
	gOut, err := f.goals.MCPCreate(f.global, map[string]any{"title": "G"})
	if err != nil {
		t.Fatal(err)
	}
	g := asItem[*Goal](gOut)
	s := &Signal{Sender: "core", Receiver: "architect", SignalType: "question", SubjectType: "Task", SubjectID: task.ID}
	if err := InsertSignal(ctx, f.db, s); err != nil {
		t.Fatal(err)
	}
	if err := f.rels.Assert(ctx, RelationEdge{SourceType: "Goal", SourceID: g.ID, TargetType: "Signal", TargetID: s.ID, RelationKind: "contains", EdgeClass: "asserted"}); err != nil {
		t.Fatal(err)
	}
	f.grant(t, "Goal:*", "Signal:*", "Task:*")
	f.grant(t, "Goal:*", "Signal:"+s.ID)
	scope, err := f.app.ReadScope(f.reader)
	if err != nil {
		t.Fatal(err)
	}
	failing := &queryHookDB{DB: f.db, match: "SELECT id, subject_type, subject_id FROM smeldr_signals WHERE id IN", fail: true}
	edges, _ := f.rels.GetLiveByTarget(ctx, "Signal", s.ID, "")
	if _, err := readableEdgesOn(ctx, failing, scope, edges); err == nil {
		t.Error("readableEdgesOn: a failing subject read returned nil")
	}
	ends := newEndReader(ctx, failing, scope)
	if _, err := ends.readable("Signal", s.ID); err == nil {
		t.Error("endReader: a failing subject read returned nil")
	}
	saved := f.rels.db
	f.rels.db = failing
	defer func() { f.rels.db = saved }()
	if _, err := f.rels.Reachability(f.reader, "Goal", g.ID, "contains", "outgoing", 1); err == nil {
		t.Error("reachability: a failing subject read returned nil")
	}
	if _, err := f.rels.Reachability(f.reader, "Signal", s.ID, "about", "outgoing", 1); err == nil {
		t.Error("reachability from a Signal: a failing subject read returned nil")
	}
}

func TestReferences_TableFactsFailures(t *testing.T) {
	ctx := context.Background()
	f := newRefFixture(t)
	if _, err := f.db.Exec(`CREATE TABLE dupt (id TEXT, slug TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`INSERT INTO dupt VALUES ('a', 'same'), ('b', 'same')`); err != nil {
		t.Fatal(err)
	}
	dup := recordTable{typeName: "Dup", name: "dupt"}
	var ve *ValidationError
	if _, _, err := dup.lookup(ctx, f.db, "same"); !errors.As(err, &ve) {
		t.Errorf("a slug held twice: %v; want a ValidationError", err)
	}
	if _, _, err := dup.lookup(ctx, &queryHookDB{DB: f.db, match: "FROM \"dupt\"", replace: "SELECT NULL, NULL"}, "same"); err == nil {
		t.Error("a scan failure returned nil")
	}
	// A database without the dynamic table has no table for an unknown type.
	bare := newSQLiteDB(t)
	if rt, err := recordTableOf(ctx, bare, "Unknown"); err != nil || rt.name != "" {
		t.Errorf("no dynamic table = %+v %v", rt, err)
	}
	if _, err := recordTableOf(ctx, &queryHookDB{DB: bare, match: `FROM "smeldr_dynamic_content" WHERE 1=0`, fail: true}, "Unknown"); err == nil {
		t.Error("a failing dynamic table probe returned nil")
	}
	// The App's kept table facts: a failing read is returned and not kept.
	app := newRefFixture(t).app
	app.cfg.DB = &queryHookDB{DB: f.db, match: `"smeldr_goals"."address"`, fail: true}
	if _, err := app.referenceTable(ctx, "Goal"); err == nil {
		t.Error("referenceTable: a failing read returned nil")
	}
	if _, err := app.resolveReference(ctx, []string{"Nope"}, "x"); !errors.Is(err, ErrBadRequest) {
		t.Errorf("resolveReference of an unregistered type: %v", err)
	}
	if err := EnsureReferenceColumns(ctx, &queryHookDB{DB: f.db, match: `"smeldr_tasks"."implements"`, fail: true}); err == nil {
		t.Error("a failing column check returned nil")
	}
}

func TestReferences_SmallPaths(t *testing.T) {
	ctx := context.Background()
	if rt, err := recordTableOf(ctx, nil, "Task"); err != nil || rt.name != "" {
		t.Errorf("no database = %+v %v", rt, err)
	}
	f := newRefFixture(t)
	q := &Signal{Sender: "core", Receiver: "architect", SignalType: "question"}
	if err := InsertSignal(ctx, f.db, q); err != nil {
		t.Fatal(err)
	}
	var ve *ValidationError
	if err := f.app.AnswerSignal(f.global, q.ID, "done", "Nope:x"); !errors.As(err, &ve) {
		t.Errorf("answer_ref of an unregistered type: %v; want a ValidationError", err)
	}
}

func TestReferences_ErrorArms(t *testing.T) {
	ctx := context.Background()
	f := newRefFixture(t)
	task := f.task(t, map[string]any{"slug": "arms"})
	other := errors.New("disk")
	// refuse passes an error it does not own through.
	if err := (referenceField{name: "x"}).refuse("v", []string{"Task"}, other); !errors.Is(err, other) {
		t.Errorf("refuse = %v; want the store error", err)
	}
	// liveEdge of an edge that is not there.
	if _, err := liveEdge(ctx, f.db, RelationEdge{SourceType: "Task", SourceID: "none", TargetType: "Task", TargetID: "none", RelationKind: "depends_on", EdgeClass: "asserted"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("liveEdge = %v; want ErrNotFound", err)
	}
	// A lookup failure inside the write is returned as it is.
	rt, err := recordTableOf(ctx, f.db, "Task")
	if err != nil {
		t.Fatal(err)
	}
	failing := &queryHookDB{DB: f.db, match: `FROM "smeldr_tasks"`, fail: true}
	item := &Amendment{Node: Node{ID: "a"}, TaskRef: task.Address}
	if _, err := referenceEdgeOn(ctx, failing, referenceTables{"Task": rt}, amendmentReferences()[0], item); !errors.Is(err, ErrInternal) {
		t.Errorf("referenceEdgeOn = %v; want ErrInternal", err)
	}
	// A Signal insert whose target is gone inside the transaction is refused.
	s := &Signal{Sender: "core", SignalType: "question", TaskRef: "T999"}
	var ve *ValidationError
	if err := writeSignalReferenceEdges(ctx, f.db, signalReferencePlan{tables: referenceTables{"Task": rt}, relations: true}, s); !errors.As(err, &ve) {
		t.Errorf("writeSignalReferenceEdges = %v; want the refusal", err)
	}
	// The dynamic edge lookup: a store failure is returned as it is.
	plan := &ContentTypeSchema{TypeName: "aplan", Fields: []byte(`[{"name":"title","type":"string","role":"title"},{"name":"task_ref","type":"string","references":"Task"}]`)}
	if _, err := f.app.DefineContentType(ctx, plan); err != nil {
		t.Fatal(err)
	}
	repo, err := f.app.DynamicContentRepo("aplan")
	if err != nil {
		t.Fatal(err)
	}
	node, _, err := repo.CreateDraftWithWarnings(f.global, "mcp", map[string]any{"title": "A", "task_ref": "arms"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.referenceEdgeOn(ctx, failing, node.ID, dynamicReference{field: "task_ref", target: "Task", stored: task.Address}); !errors.Is(err, ErrInternal) {
		t.Errorf("dynamic referenceEdgeOn = %v; want ErrInternal", err)
	}
	// An update that sends the stored value again changes nothing.
	if _, err := repo.UpdateFieldsWithWarnings(f.global, "mcp", node.ID, map[string]any{"task_ref": task.Address}); err != nil {
		t.Errorf("same stored value: %v", err)
	}
	// MigrateReferences: a RefFields target whose table facts cannot be read.
	hook := &queryHookDB{DB: f.db, match: `"smeldr_goals"."address"`, fail: true}
	if _, err := MigrateReferences(ctx, hook, ReferenceMigrationOptions{}); err == nil {
		t.Error("a failing table read returned nil")
	}
}
