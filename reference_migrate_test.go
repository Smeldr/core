// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// seedOldReferences writes records the way they were before D113: references
// as text, no edges. It returns the dynamic plan item IDs (good, dangling).
func seedOldReferences(t *testing.T, f refFixture) (string, string) {
	t.Helper()
	ctx := context.Background()
	save := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	save(NewSQLRepo[*Decision](f.db, Table("smeldr_decisions")).Save(ctx, &Decision{Node: Node{ID: "d-1", Slug: "d-1", Status: "proposed"}, RecordAddress: RecordAddress{Address: "D7", AddressNo: 7}}))
	save(NewSQLRepo[*Goal](f.db, Table("smeldr_goals")).Save(ctx, &Goal{Node: Node{ID: "g-1", Slug: "grow", Status: "open"}, RecordAddress: RecordAddress{Address: "G3", AddressNo: 3}}))
	tasks := NewSQLRepo[*Task](f.db, Table("smeldr_tasks"))
	save(tasks.Save(ctx, &Task{Node: Node{ID: "t-1", Slug: "first", Status: "backlog"}, RecordAddress: RecordAddress{Address: "T5", AddressNo: 5}, NoteRef: "D7"}))
	save(tasks.Save(ctx, &Task{Node: Node{ID: "t-2", Slug: "second", Status: "backlog"}, NoteRef: "grow"}))
	save(tasks.Save(ctx, &Task{Node: Node{ID: "t-3", Slug: "third", Status: "backlog"}, NoteRef: "T5"}))
	save(tasks.Save(ctx, &Task{Node: Node{ID: "t-4", Slug: "fourth", Status: "backlog"}, NoteRef: "task_plan item-c480de24 (answers 4 and 5)"}))
	ams := NewSQLRepo[*Amendment](f.db, Table("smeldr_amendments"))
	save(ams.Save(ctx, &Amendment{Node: Node{ID: "a-1", Slug: "a-1", Status: "merged"}, AmendmentType: "fix", Amends: "D7", TaskRef: "T5"}))
	save(ams.Save(ctx, &Amendment{Node: Node{ID: "a-2", Slug: "a-2", Status: "merged"}, AmendmentType: "fix", Amends: "D99"}))
	now := time.Now().UTC()
	for _, s := range [][5]string{
		{"s-1", "Task", "first", "second", ""},
		{"s-2", "", "", "A185", ""},
		{"s-3", "Decision", "d-1", "", ""},
	} {
		if _, err := f.db.Exec(`INSERT INTO smeldr_signals (id, slug, status, created_at, updated_at, sender, receiver, signal_type, subject_type, subject_id, task_ref)
			VALUES ($1, $1, 'pending', $2, $2, 'core', 'architect', 'notice', $3, $4, $5)`, s[0], now, s[1], s[2], s[3]); err != nil {
			t.Fatal(err)
		}
	}
	plan := &ContentTypeSchema{TypeName: "plan", Fields: []byte(`[{"name":"title","type":"string","role":"title"},{"name":"task_ref","type":"string"}]`)}
	if _, err := f.app.DefineContentType(ctx, plan); err != nil {
		t.Fatal(err)
	}
	repo, err := f.app.DynamicContentRepo("plan")
	if err != nil {
		t.Fatal(err)
	}
	good, _, err := repo.CreateDraftWithWarnings(f.global, "mcp", map[string]any{"title": "good", "task_ref": "first"})
	if err != nil {
		t.Fatal(err)
	}
	dangling, _, err := repo.CreateDraftWithWarnings(f.global, "mcp", map[string]any{"title": "dangling", "task_ref": "T404"})
	if err != nil {
		t.Fatal(err)
	}
	return good.ID, dangling.ID
}

func TestMigrateReferences(t *testing.T) {
	f := newRefFixture(t)
	good, dangling := seedOldReferences(t, f)
	ctx := context.Background()
	opts := ReferenceMigrationOptions{DryRun: true, RefFields: map[string]string{"plan.task_ref": "Task"}}

	dry, err := MigrateReferences(ctx, f.db, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !dry.DryRun || len(dry.Created) == 0 {
		t.Fatalf("dry run = %+v", dry)
	}
	if n := len(f.liveEdges(t, "Task", "t-1", "implements")); n != 0 {
		t.Errorf("a dry run wrote %d edges", n)
	}

	opts.DryRun = false
	rep, err := f.app.MigrateReferences(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	created := map[string]bool{}
	for _, c := range rep.Created {
		created[c.SourceID+"/"+c.Field+"/"+c.Kind+"/"+c.TargetID] = true
	}
	for _, want := range []string{
		"s-1/subject_id/about/t-1", "s-1/task_ref/about/t-2", "s-3/subject_id/about/d-1",
		"t-1/note_ref/implements/d-1", "t-2/note_ref/derives_from/g-1",
		"a-1/amends/amends/d-1", "a-1/task_ref/ships_as/t-1",
		good + "/task_ref/refers_to/t-1",
	} {
		if !created[want] {
			t.Errorf("created lacks %s: %v", want, created)
		}
	}
	if edges := f.liveEdges(t, "Task", "t-1", "ships_as"); len(edges) != 1 || edges[0].TargetID != "a-1" {
		t.Errorf("ships_as runs Task to Amendment: %+v", edges)
	}
	if len(rep.Rewritten) != 1 || rep.Rewritten[0].ID != "s-1" || rep.Rewritten[0].New != "t-1" {
		t.Errorf("rewritten = %+v", rep.Rewritten)
	}
	var subject string
	if err := f.db.QueryRow(`SELECT subject_id FROM smeldr_signals WHERE id = 's-1'`).Scan(&subject); err != nil || subject != "t-1" {
		t.Errorf("s-1 subject_id = %q %v", subject, err)
	}
	unresolved := map[string]string{}
	for _, n := range rep.Unresolved {
		unresolved[n.ID+"/"+n.Field] = n.Reason
	}
	for key, want := range map[string]string{
		"s-2/task_ref":         "names no Task",
		"t-3/note_ref":         "not a relation this field makes",
		"t-4/note_ref":         "names no Decision or Goal or Task",
		"a-2/amends":           "names no Decision",
		dangling + "/task_ref": "names no Task",
	} {
		if !strings.Contains(unresolved[key], want) {
			t.Errorf("unresolved[%s] = %q; want %q", key, unresolved[key], want)
		}
	}

	// Idempotent: a second apply creates nothing and finds every edge present.
	again, err := MigrateReferences(ctx, f.db, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Created) != 0 || len(again.Present) != len(rep.Created) || len(again.Rewritten) != 0 {
		t.Errorf("second apply: created %d present %d (want %d) rewritten %d", len(again.Created), len(again.Present), len(rep.Created), len(again.Rewritten))
	}
}

func TestMigrateReferences_SchemaDeclaredField(t *testing.T) {
	f := newRefFixture(t)
	ctx := context.Background()
	task := f.task(t, map[string]any{"slug": "declared"})
	plan := &ContentTypeSchema{TypeName: "plan2", Fields: []byte(`[{"name":"title","type":"string","role":"title"},{"name":"task_ref","type":"string"}]`)}
	if _, err := f.app.DefineContentType(ctx, plan); err != nil {
		t.Fatal(err)
	}
	repo, _ := f.app.DynamicContentRepo("plan2")
	node, _, err := repo.CreateDraftWithWarnings(f.global, "mcp", map[string]any{"title": "p", "task_ref": "declared"})
	if err != nil {
		t.Fatal(err)
	}
	// Redefined with the reference: the backfill reads it from the schema.
	next := &ContentTypeSchema{TypeName: "plan2", Fields: []byte(`[{"name":"title","type":"string","role":"title"},{"name":"task_ref","type":"string","references":"Task"}]`)}
	if _, err := f.app.RedefineContentType(ctx, next, "task_ref is a reference"); err != nil {
		t.Fatal(err)
	}
	rep, err := MigrateReferences(ctx, f.db, ReferenceMigrationOptions{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range rep.Created {
		found = found || (c.SourceID == node.ID && c.Kind == "refers_to" && c.TargetID == task.ID)
	}
	if !found {
		t.Errorf("created = %+v; want the declared field's edge", rep.Created)
	}
}

func TestMigrateReferences_Refused(t *testing.T) {
	ctx := context.Background()
	if _, err := MigrateReferences(ctx, nil, ReferenceMigrationOptions{}); !errors.Is(err, ErrBadRequest) {
		t.Errorf("nil db: %v", err)
	}
	if _, err := MigrateReferences(ctx, newSQLiteDB(t), ReferenceMigrationOptions{}); !errors.Is(err, ErrBadRequest) || !strings.Contains(err.Error(), "smeldr_relations") {
		t.Errorf("no relations table: %v", err)
	}
	f := newRefFixture(t)
	for _, bad := range []map[string]string{{"nodot": "Task"}, {"plan.": "Task"}, {"plan.f": " "}} {
		if _, err := MigrateReferences(ctx, f.db, ReferenceMigrationOptions{RefFields: bad}); !errors.Is(err, ErrBadRequest) {
			t.Errorf("RefFields %v: %v; want ErrBadRequest", bad, err)
		}
	}
	seedOldReferences(t, f)
	for _, match := range []string{
		"FROM smeldr_signals WHERE TRIM(subject_id)",
		"FROM smeldr_tasks WHERE TRIM(note_ref)",
		"amends FROM smeldr_amendments",
		"FROM smeldr_dynamic_content WHERE type_name",
		"SELECT DISTINCT subject_type",
	} {
		hook := queryHookDB{DB: f.db, match: match, fail: true}
		if _, err := MigrateReferences(ctx, &hook, ReferenceMigrationOptions{RefFields: map[string]string{"plan.task_ref": "Task"}}); !errors.Is(err, ErrInternal) {
			t.Errorf("failing %q: %v; want ErrInternal", match, err)
		}
	}
	// A failing edge write rolls everything back.
	if _, err := f.db.Exec(`CREATE TRIGGER refuse_edge BEFORE INSERT ON smeldr_relations BEGIN SELECT RAISE(ABORT, 'refused'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := MigrateReferences(ctx, f.db, ReferenceMigrationOptions{}); !errors.Is(err, ErrInternal) {
		t.Errorf("failing edge: %v; want ErrInternal", err)
	}
	var subject string
	if err := f.db.QueryRow(`SELECT subject_id FROM smeldr_signals WHERE id = 's-1'`).Scan(&subject); err != nil || subject != "first" {
		t.Errorf("s-1 after a rollback = %q %v; want unchanged", subject, err)
	}
}
