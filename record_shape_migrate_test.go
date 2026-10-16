package smeldr

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// MigrateRecordShape (D112): titles from the record's text, long Amendment
// summaries cut with the full text kept in the body, amendment_type mapped.

func seedRecordShape(t *testing.T, db DB) {
	t.Helper()
	ctx := context.Background()
	long := "Ship the capability registry so every surface can name its acts once and the stream, the tools and the CLI share them all"
	tasks := NewSQLRepo[*Task](db, Table("smeldr_tasks"))
	for _, task := range []*Task{
		{Node: Node{ID: "t-1", Slug: "t-1", Status: "backlog"}, Description: "## " + long + "\n\nmore text"},
		{Node: Node{ID: "t-2", Slug: "t-2", Status: "backlog"}, Description: "\n\n- **Short** first line\nbody"},
		{Node: Node{ID: "t-3", Slug: "t-3", Status: "backlog"}, Title: "already named", Description: "x"},
		{Node: Node{ID: "t-4", Slug: "t-4", Status: "backlog"}, Description: "---\n\n"},
	} {
		if err := tasks.Save(ctx, task); err != nil {
			t.Fatal(err)
		}
	}
	if err := NewSQLRepo[*Goal](db, Table("smeldr_goals")).Save(ctx, &Goal{Node: Node{ID: "g-1", Slug: "g-1", Status: "open"}, Description: "Grow the backlog view"}); err != nil {
		t.Fatal(err)
	}
	if err := NewSQLRepo[*Decision](db, Table("smeldr_decisions")).Save(ctx, &Decision{Node: Node{ID: "d-1", Slug: "d-1", Status: "proposed"}, Body: "# Lists return rows\n\nWhy"}); err != nil {
		t.Fatal(err)
	}
	ams := NewSQLRepo[*Amendment](db, Table("smeldr_amendments"))
	for _, a := range []*Amendment{
		{Node: Node{ID: "a-1", Slug: "a-1", Status: "merged"}, AmendmentType: "feat", Summary: long + " and more words after the limit", Body: "rationale"},
		{Node: Node{ID: "a-2", Slug: "a-2", Status: "merged"}, AmendmentType: "Level 2", Summary: "short"},
		{Node: Node{ID: "a-3", Slug: "a-3", Status: "merged"}, AmendmentType: "fix", Summary: long + " tail", Body: "pre " + long + " tail post"},
		{Node: Node{ID: "a-4", Slug: "a-4", Status: "merged"}, AmendmentType: "bugfix", Summary: "s"},
		{Node: Node{ID: "a-5", Slug: "a-5", Status: "merged"}, AmendmentType: "deploy", Summary: "s"},
	} {
		if err := ams.Save(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
}

func recordShapeColumn(t *testing.T, db DB, table, column, id string) string {
	t.Helper()
	var v string
	if err := db.QueryRowContext(context.Background(), "SELECT "+column+" FROM "+table+" WHERE id = $1", id).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestMigrateRecordShape_DryRun(t *testing.T) {
	db := addressDB(t)
	seedRecordShape(t, db)
	rep, err := MigrateRecordShape(context.Background(), db, RecordShapeMigrationOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.DryRun || len(rep.Titles) == 0 || len(rep.AmendmentTypes) != 3 || len(rep.UnmappedAmendmentTypes) != 1 {
		t.Errorf("report = %+v", rep)
	}
	if got := recordShapeColumn(t, db, "smeldr_tasks", "title", "t-1"); got != "" {
		t.Errorf("a dry run wrote a title: %q", got)
	}
	if got := recordShapeColumn(t, db, "smeldr_amendments", "amendment_type", "a-1"); got != "feat" {
		t.Errorf("a dry run wrote a type: %q", got)
	}
}

func TestMigrateRecordShape_ApplyIdempotent(t *testing.T) {
	db := addressDB(t)
	seedRecordShape(t, db)
	app := New(Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16"), DB: db})
	rep, err := app.MigrateRecordShape(context.Background(), RecordShapeMigrationOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t1 := recordShapeColumn(t, db, "smeldr_tasks", "title", "t-1")
	if n := len([]rune(t1)); n > TitleMaxLength || n < TitleMaxLength/2 || strings.HasPrefix(t1, "#") || strings.HasSuffix(t1, " ") {
		t.Errorf("t-1 title = %q (%d); want the first line cut at a word boundary", t1, n)
	}
	if !strings.HasPrefix("Ship the capability registry so every surface can name its acts once and the stream, the tools and the CLI share them all", t1) {
		t.Errorf("t-1 title = %q; want a prefix of the first line", t1)
	}
	if got := recordShapeColumn(t, db, "smeldr_tasks", "title", "t-2"); got != "Short first line" {
		t.Errorf("t-2 title = %q; want list and emphasis marks removed", got)
	}
	if got := recordShapeColumn(t, db, "smeldr_tasks", "title", "t-3"); got != "already named" {
		t.Errorf("t-3 title changed: %q", got)
	}
	if got := recordShapeColumn(t, db, "smeldr_goals", "title", "g-1"); got != "Grow the backlog view" {
		t.Errorf("g-1 title = %q", got)
	}
	if got := recordShapeColumn(t, db, "smeldr_decisions", "title", "d-1"); got != "Lists return rows" {
		t.Errorf("d-1 title = %q", got)
	}
	if len(rep.Untitled) != 1 || rep.Untitled[0].ID != "t-4" {
		t.Errorf("untitled = %+v; want t-4", rep.Untitled)
	}
	// A long summary is cut, and the full text leads the body.
	s1 := recordShapeColumn(t, db, "smeldr_amendments", "summary", "a-1")
	b1 := recordShapeColumn(t, db, "smeldr_amendments", "body", "a-1")
	if len([]rune(s1)) > TitleMaxLength || !strings.HasPrefix(b1, "Ship the capability registry") || !strings.HasSuffix(b1, "rationale") {
		t.Errorf("a-1 summary %q, body %q", s1, b1)
	}
	// A body that already holds the full summary is left as it is.
	if b3 := recordShapeColumn(t, db, "smeldr_amendments", "body", "a-3"); !strings.HasPrefix(b3, "pre ") {
		t.Errorf("a-3 body = %q; want unchanged", b3)
	}
	kept := map[string]bool{}
	for _, c := range rep.Titles {
		if c.Field == "summary" {
			kept[c.ID] = c.BodyKept
		}
	}
	if !kept["a-1"] || kept["a-3"] {
		t.Errorf("body_kept = %v; want a-1 true, a-3 false", kept)
	}
	for id, want := range map[string]string{"a-1": "feature", "a-2": "Level 2", "a-4": "fix", "a-5": "release"} {
		if got := recordShapeColumn(t, db, "smeldr_amendments", "amendment_type", id); got != want {
			t.Errorf("%s amendment_type = %q; want %q", id, got, want)
		}
	}
	if len(rep.UnmappedAmendmentTypes) != 1 || rep.UnmappedAmendmentTypes[0].Value != "Level 2" {
		t.Errorf("unmapped = %+v", rep.UnmappedAmendmentTypes)
	}

	again, err := MigrateRecordShape(context.Background(), db, RecordShapeMigrationOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Titles) != 0 || len(again.AmendmentTypes) != 0 || len(again.Untitled) != 1 || len(again.UnmappedAmendmentTypes) != 1 {
		t.Errorf("second run = %+v; want only the standing notes", again)
	}
}

func TestMigrateRecordShape_Preconditions(t *testing.T) {
	if _, err := MigrateRecordShape(context.Background(), nil, RecordShapeMigrationOptions{}); !errors.Is(err, ErrBadRequest) {
		t.Errorf("nil db: %v", err)
	}
	// A database without the orchestration tables has nothing to do.
	rep, err := MigrateRecordShape(context.Background(), newSQLiteDB(t), RecordShapeMigrationOptions{})
	if err != nil || len(rep.Titles) != 0 {
		t.Errorf("empty database: %+v %v", rep, err)
	}
	// A tasks table without the title column needs the ensure first.
	db := newSQLiteDB(t)
	if _, err := db.Exec(`CREATE TABLE smeldr_tasks (id TEXT PRIMARY KEY, description TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := MigrateRecordShape(context.Background(), db, RecordShapeMigrationOptions{}); !errors.Is(err, ErrBadRequest) || !strings.Contains(err.Error(), "EnsureTaskGoalTitleColumns") {
		t.Errorf("missing title column: %v", err)
	}
	if err := EnsureTaskGoalTitleColumns(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO smeldr_tasks (id, description) VALUES ('t', 'named by text')`); err != nil {
		t.Fatal(err)
	}
	rep, err = MigrateRecordShape(context.Background(), db, RecordShapeMigrationOptions{})
	if err != nil || len(rep.Titles) != 1 || rep.Titles[0].Address != "" {
		t.Errorf("after ensure, no address column: %+v %v", rep, err)
	}
}

func TestMigrateRecordShape_Failure(t *testing.T) {
	// A failing read is an internal error.
	for _, match := range []string{"FROM smeldr_tasks", "FROM smeldr_amendments"} {
		db := addressDB(t)
		seedRecordShape(t, db)
		hook := &queryHookDB{DB: db, match: match, fail: true}
		if _, err := MigrateRecordShape(context.Background(), hook, RecordShapeMigrationOptions{}); !errors.Is(err, ErrInternal) {
			t.Errorf("%s failing: %v; want ErrInternal", match, err)
		}
	}
	// A write that fails late rolls back every earlier one: one transaction.
	db := addressDB(t)
	seedRecordShape(t, db)
	if _, err := db.Exec(`CREATE TRIGGER refuse_amendments BEFORE UPDATE ON smeldr_amendments BEGIN SELECT RAISE(ABORT, 'refused'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := MigrateRecordShape(context.Background(), db, RecordShapeMigrationOptions{}); !errors.Is(err, ErrInternal) {
		t.Errorf("failing amendment update: %v; want ErrInternal", err)
	}
	if got := recordShapeColumn(t, db, "smeldr_goals", "title", "g-1"); got != "" {
		t.Errorf("a title survived the rollback: %q", got)
	}
}

func TestCutAtWordAndFirstLine(t *testing.T) {
	for in, want := range map[string]string{
		"short":                  "short",
		strings.Repeat("x", 130): strings.Repeat("x", 120),
		"> quoted `code` line":   "quoted code line",
		"\n\n***\n\nfirst words": "first words",
	} {
		got := cutAtWord(firstTextLine(in), TitleMaxLength)
		if got != want {
			t.Errorf("%q -> %q; want %q", in, got, want)
		}
	}
	if got := cutAtWord("aaaa bbbb, cccc", 11); got != "aaaa bbbb" {
		t.Errorf("trailing comma kept: %q", got)
	}
}

// TestMigrateRecordShape_ProbeAndScanFailures: every probe, read and scan
// failure is an internal error, never a partial migration.
func TestMigrateRecordShape_ProbeAndScanFailures(t *testing.T) {
	cases := []struct {
		name string
		hook queryHookDB
	}{
		{"title column probe", queryHookDB{match: `"smeldr_tasks"."title"`, fail: true}},
		{"amendments table probe", queryHookDB{match: `SELECT 1 FROM "smeldr_amendments"`, fail: true}},
		{"address column probe", queryHookDB{match: `"smeldr_tasks"."address"`, fail: true}},
		{"title scan", queryHookDB{match: "FROM smeldr_tasks WHERE TRIM(title)", replace: "SELECT 1"}},
		{"amendment scan", queryHookDB{match: "FROM smeldr_amendments ORDER BY", replace: "SELECT 1"}},
		{"amendment read", queryHookDB{match: "FROM smeldr_amendments ORDER BY", fail: true}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			db := addressDB(t)
			seedRecordShape(t, db)
			hook := c.hook
			hook.DB = db
			if _, err := MigrateRecordShape(context.Background(), &hook, RecordShapeMigrationOptions{}); !errors.Is(err, ErrInternal) {
				t.Errorf("%v; want ErrInternal", err)
			}
		})
	}
	// A failing write is an internal error too.
	db := addressDB(t)
	seedRecordShape(t, db)
	if _, err := db.Exec(`CREATE TRIGGER refuse_tasks BEFORE UPDATE ON smeldr_tasks BEGIN SELECT RAISE(ABORT, 'refused'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := MigrateRecordShape(context.Background(), db, RecordShapeMigrationOptions{}); !errors.Is(err, ErrInternal) {
		t.Errorf("failing title update: %v", err)
	}
	if _, err := db.Exec(`DROP TRIGGER refuse_tasks`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE smeldr_tasks SET title = 'x'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE smeldr_goals SET title = 'x'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE smeldr_decisions SET title = 'x'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE smeldr_amendments SET summary = 's'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER refuse_type BEFORE UPDATE ON smeldr_amendments BEGIN SELECT RAISE(ABORT, 'refused'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := MigrateRecordShape(context.Background(), db, RecordShapeMigrationOptions{}); !errors.Is(err, ErrInternal) {
		t.Errorf("failing type update: %v", err)
	}
}

func TestEnsureTaskGoalTitleColumns_Errors(t *testing.T) {
	db := newSQLiteDB(t)
	if err := EnsureTaskGoalTitleColumns(context.Background(), db); err != nil {
		t.Errorf("no tables: %v", err)
	}
	hook := &queryHookDB{DB: db, match: `SELECT 1 FROM "smeldr_tasks"`, fail: true}
	if err := EnsureTaskGoalTitleColumns(context.Background(), hook); err == nil {
		t.Error("a failing table probe: want an error")
	}
	if _, err := db.Exec(`CREATE TABLE smeldr_goals (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	hook = &queryHookDB{DB: db, match: `"smeldr_goals"."title"`, fail: true}
	if err := EnsureTaskGoalTitleColumns(context.Background(), hook); err == nil {
		t.Error("a failing column probe: want an error")
	}
	if err := EnsureTaskGoalTitleColumns(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if err := EnsureTaskGoalTitleColumns(context.Background(), db); err != nil {
		t.Errorf("second run: %v", err)
	}
}

// addressRecordShape seeds the record-shape rows and gives the Amendments
// addresses, plus one "Level 1" row, as on a D110 database.
func addressRecordShape(t *testing.T, db DB) {
	t.Helper()
	seedRecordShape(t, db)
	if err := NewSQLRepo[*Amendment](db, Table("smeldr_amendments")).Save(context.Background(),
		&Amendment{Node: Node{ID: "a-6", Slug: "a-6", Status: "merged"}, AmendmentType: "Level 1", Summary: "s"}); err != nil {
		t.Fatal(err)
	}
	for id, address := range map[string]string{"a-1": "A491", "a-2": "A490", "a-3": "A492", "a-4": "A493", "a-5": "A494", "a-6": "A497"} {
		if _, err := db.ExecContext(context.Background(), `UPDATE smeldr_amendments SET address = $1 WHERE id = $2`, address, id); err != nil {
			t.Fatal(err)
		}
	}
}

// AmendmentTypeMap maps the rows the rules cannot, by address or number, and
// wins over a rule; the report says which rows the map decided.
func TestMigrateRecordShape_TypeMap(t *testing.T) {
	db := addressDB(t)
	addressRecordShape(t, db)
	typeMap := map[string]string{"A490": "feature", "497": "fix", "A491": "refactor"}
	rep, err := MigrateRecordShape(context.Background(), db, RecordShapeMigrationOptions{AmendmentTypeMap: typeMap})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.UnmappedAmendmentTypes) != 0 {
		t.Errorf("unmapped = %+v", rep.UnmappedAmendmentTypes)
	}
	sources := map[string]string{}
	for _, c := range rep.AmendmentTypes {
		sources[c.Address] = c.New + "/" + c.Source
	}
	want := map[string]string{"A490": "feature/type_map", "A497": "fix/type_map", "A491": "refactor/type_map", "A493": "fix/rule", "A494": "release/rule"}
	for address, w := range want {
		if sources[address] != w {
			t.Errorf("%s = %q; want %q (all %v)", address, sources[address], w, sources)
		}
	}
	for id, w := range map[string]string{"a-1": "refactor", "a-2": "feature", "a-6": "fix", "a-4": "fix"} {
		if got := recordShapeColumn(t, db, "smeldr_amendments", "amendment_type", id); got != w {
			t.Errorf("%s type = %q; want %q", id, got, w)
		}
	}
	// A second run without the map changes no type; with it, it is refused.
	if rep, err := MigrateRecordShape(context.Background(), db, RecordShapeMigrationOptions{}); err != nil || len(rep.AmendmentTypes) != 0 {
		t.Errorf("second run: %+v %v", rep, err)
	}
	if _, err := MigrateRecordShape(context.Background(), db, RecordShapeMigrationOptions{AmendmentTypeMap: typeMap}); !errors.Is(err, ErrBadRequest) {
		t.Errorf("second run with the map: %v; want ErrBadRequest", err)
	}
}

func TestMigrateRecordShape_TypeMapDryRun(t *testing.T) {
	db := addressDB(t)
	addressRecordShape(t, db)
	rep, err := MigrateRecordShape(context.Background(), db, RecordShapeMigrationOptions{DryRun: true, AmendmentTypeMap: map[string]string{"A490": "chore"}})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range rep.AmendmentTypes {
		found = found || (c.Address == "A490" && c.New == "chore" && c.Source == "type_map")
	}
	if !found || len(rep.UnmappedAmendmentTypes) != 1 {
		t.Errorf("dry-run report = %+v", rep)
	}
	if got := recordShapeColumn(t, db, "smeldr_amendments", "amendment_type", "a-2"); got != "Level 2" {
		t.Errorf("a dry run wrote %q", got)
	}
}

// One bad entry refuses the whole run before anything is written, and every
// problem is named.
func TestMigrateRecordShape_TypeMapRefused(t *testing.T) {
	cases := []struct {
		name    string
		typeMap map[string]string
		want    []string
	}{
		{"value not a type", map[string]string{"A490": "bug"}, []string{`A490: "bug" is not a type (feature, fix`}},
		{"no such Amendment", map[string]string{"A999": "fix"}, []string{"A999: no such Amendment"}},
		{"not an address", map[string]string{"x1": "fix", "A": "fix", "": "fix"}, []string{"x1: not an Amendment address or number", "A: not an Amendment"}},
		{"already valid", map[string]string{"A492": "feature"}, []string{"A492: already has type fix"}},
		{"named twice", map[string]string{"A490": "fix", "490": "fix"}, []string{"A490: names A490 twice"}},
		{"several problems", map[string]string{"A490": "bug", "A999": "fix", "A497": "fix"}, []string{`A490: "bug"`, "A999: no such Amendment"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			db := addressDB(t)
			addressRecordShape(t, db)
			_, err := MigrateRecordShape(context.Background(), db, RecordShapeMigrationOptions{AmendmentTypeMap: c.typeMap})
			if !errors.Is(err, ErrBadRequest) {
				t.Fatalf("%v; want ErrBadRequest", err)
			}
			for _, w := range c.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("%v; want it to name %q", err, w)
				}
			}
			// Nothing written: no title, no summary cut, no type mapped.
			if got := recordShapeColumn(t, db, "smeldr_tasks", "title", "t-1"); got != "" {
				t.Errorf("title written: %q", got)
			}
			if got := recordShapeColumn(t, db, "smeldr_amendments", "amendment_type", "a-1"); got != "feat" {
				t.Errorf("type written: %q", got)
			}
			if got := recordShapeColumn(t, db, "smeldr_amendments", "amendment_type", "a-6"); got != "Level 1" {
				t.Errorf("type written: %q", got)
			}
		})
	}
	// A map with no Amendments table to apply it to is refused too.
	if _, err := MigrateRecordShape(context.Background(), newSQLiteDB(t), RecordShapeMigrationOptions{AmendmentTypeMap: map[string]string{"A1": "fix"}}); !errors.Is(err, ErrBadRequest) || !strings.Contains(err.Error(), "no smeldr_amendments table") {
		t.Errorf("no amendments table: %v", err)
	}
}

// A database from before addresses (D110) matches a key on amendment_number.
func TestMigrateRecordShape_TypeMapWithoutAddress(t *testing.T) {
	db := newSQLiteDB(t)
	if _, err := db.Exec(`CREATE TABLE smeldr_amendments (id TEXT PRIMARY KEY, amendment_number TEXT NOT NULL DEFAULT '', amendment_type TEXT NOT NULL DEFAULT '', summary TEXT NOT NULL DEFAULT '', body TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO smeldr_amendments (id, amendment_number, amendment_type) VALUES ('x', 'A12', 'Level 2'), ('y', 'A13', 'Level 1'), ('z', 'A13', 'Level 1')`); err != nil {
		t.Fatal(err)
	}
	if _, err := MigrateRecordShape(context.Background(), db, RecordShapeMigrationOptions{AmendmentTypeMap: map[string]string{"A13": "fix"}}); !errors.Is(err, ErrBadRequest) || !strings.Contains(err.Error(), "A13: matches 2 Amendments") {
		t.Errorf("shared number: %v", err)
	}
	rep, err := MigrateRecordShape(context.Background(), db, RecordShapeMigrationOptions{AmendmentTypeMap: map[string]string{"12": "docs"}})
	if err != nil || len(rep.AmendmentTypes) != 1 || rep.AmendmentTypes[0].Source != "type_map" {
		t.Fatalf("by number: %+v %v", rep, err)
	}
	if got := recordShapeColumn(t, db, "smeldr_amendments", "amendment_type", "x"); got != "docs" {
		t.Errorf("type = %q", got)
	}
}
