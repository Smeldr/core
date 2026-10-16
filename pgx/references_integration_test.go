//go:build integration

package pgx

import (
	"context"
	"strings"
	"testing"
	"time"

	smeldr "smeldr.dev/core"
)

// D113 on Postgres: references resolve on write and their edges commit in the
// write's transaction (InsertSignal, a runtime-defined reference field), a
// refused reference writes nothing, and MigrateReferences backfills
// idempotently.
func TestPG_References(t *testing.T) {
	db, _ := isolatedDB(t)
	ctx := context.Background()
	for _, create := range []func(smeldr.DB) error{smeldr.CreateOrchestrationTables, smeldr.CreateRelationTables, smeldr.CreateBlockTables, smeldr.CreateSchemaTable} {
		if err := create(db); err != nil {
			t.Fatal(err)
		}
	}
	if err := smeldr.EnsureReferenceColumns(ctx, db); err != nil {
		t.Fatal(err)
	}
	app := smeldr.New(smeldr.Config{BaseURL: "https://example.com", Secret: []byte(pgTestSecret), DB: db})
	app.ServeDynamicContent()
	smeldr.RegisterOrchestrationTypes(app, db)
	rels, err := smeldr.NewRelationStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := smeldr.RegisterOrchestrationRelationKinds(ctx, rels); err != nil {
		t.Fatal(err)
	}
	app.Relations(rels)
	_ = app.Handler()

	taskID := smeldr.NewID()
	if err := smeldr.NewSQLRepo[*smeldr.Task](db, smeldr.Table("smeldr_tasks")).Save(ctx,
		&smeldr.Task{Node: smeldr.Node{ID: taskID, Slug: "pg-task", Status: "backlog"}, RecordAddress: smeldr.RecordAddress{Address: "T9", AddressNo: 9}}); err != nil {
		t.Fatal(err)
	}

	// InsertSignal: subject by slug stored as the ID, task_ref as the address,
	// one about edge (both name the Task).
	s := &smeldr.Signal{Sender: "core", Receiver: "architect", SignalType: "question", SubjectType: "Task", SubjectID: "pg-task", TaskRef: "T9"}
	if err := smeldr.InsertSignal(ctx, db, s); err != nil {
		t.Fatal(err)
	}
	if s.SubjectID != taskID || s.TaskRef != "T9" {
		t.Errorf("stored subject %q task_ref %q", s.SubjectID, s.TaskRef)
	}
	if edges, err := rels.GetLiveBySource(ctx, "Signal", s.ID, "about"); err != nil || len(edges) != 1 || edges[0].TargetID != taskID {
		t.Errorf("about edges = %+v %v", edges, err)
	}
	bad := &smeldr.Signal{Sender: "core", Receiver: "architect", SignalType: "question", TaskRef: "A185"}
	if err := smeldr.InsertSignal(ctx, db, bad); err == nil || !strings.Contains(err.Error(), "send a Task's address") {
		t.Errorf("unresolved task_ref = %v; want the guided refusal", err)
	}

	// A runtime-defined reference field.
	plan := &smeldr.ContentTypeSchema{TypeName: "pg_plan", Fields: []byte(`[{"name":"title","type":"string","role":"title"},{"name":"task_ref","type":"string","references":"Task"}]`)}
	if _, err := app.DefineContentType(ctx, plan); err != nil {
		t.Fatal(err)
	}
	repo, err := app.DynamicContentRepo("pg_plan")
	if err != nil {
		t.Fatal(err)
	}
	node, _, err := repo.CreateDraftWithWarnings(ctx, "mcp", map[string]any{"title": "P", "task_ref": "pg-task"})
	if err != nil {
		t.Fatal(err)
	}
	if edges, err := rels.GetLiveBySource(ctx, "pg_plan", node.ID, "refers_to"); err != nil || len(edges) != 1 {
		t.Errorf("refers_to edges = %+v %v", edges, err)
	}
	if _, _, err := repo.CreateDraftWithWarnings(ctx, "mcp", map[string]any{"title": "Q", "task_ref": "T404"}); err == nil {
		t.Error("a dangling reference was written")
	}

	// The backfill: an old Signal and an old Task note_ref, then idempotent.
	decisionID := smeldr.NewID()
	if err := smeldr.NewSQLRepo[*smeldr.Decision](db, smeldr.Table("smeldr_decisions")).Save(ctx,
		&smeldr.Decision{Node: smeldr.Node{ID: decisionID, Slug: "pg-d", Status: "proposed"}, RecordAddress: smeldr.RecordAddress{Address: "D4", AddressNo: 4}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE smeldr_tasks SET note_ref = 'D4' WHERE id = $1`, taskID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO smeldr_signals (id, slug, status, created_at, updated_at, sender, receiver, signal_type, subject_type, subject_id)
		VALUES ('pg-old', 'pg-old', 'pending', $1, $1, 'core', 'architect', 'notice', 'Task', 'pg-task')`, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	rep, err := app.MigrateReferences(ctx, smeldr.ReferenceMigrationOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Created) != 2 || len(rep.Rewritten) != 1 {
		t.Errorf("backfill = created %+v rewritten %+v", rep.Created, rep.Rewritten)
	}
	again, err := smeldr.MigrateReferences(ctx, db, smeldr.ReferenceMigrationOptions{})
	if err != nil || len(again.Created) != 0 || len(again.Rewritten) != 0 {
		t.Errorf("second run = %+v %v", again, err)
	}
}
