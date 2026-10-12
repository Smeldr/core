package smeldr

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// migrationFixture is a database as it would be on the day the migration
// runs: rows written before addresses existed (address ”), the counters
// seeded at the first boot after deploy, then records created after deploy,
// which already have addresses, and one raw Signal written after deploy
// without one (as mcp's create_signal does until mcp Phase 2).
func migrationFixture(t *testing.T) (*sql.DB, *App) {
	t.Helper()
	ctx := context.Background()
	db := newSQLiteDB(t)
	for _, f := range []func(DB) error{CreateOrchestrationTables, CreateRelationTables, CreateBlockTables, CreateSchemaTable} {
		if err := f(db); err != nil {
			t.Fatal(err)
		}
	}
	// Before addresses: no counters yet.
	mustExecMig(t, db, `DELETE FROM smeldr_address_counters`)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	at := func(i int) time.Time { return base.Add(time.Duration(i) * time.Minute) }
	for i, d := range []struct{ id, number string }{
		{"d-a", "D3"}, {"d-b", "D5"}, {"d-c", "D5"}, {"d-d", "D74-DECLINE"}, {"d-e", ""},
	} {
		mustExecMig(t, db, `INSERT INTO smeldr_decisions (id, slug, status, created_at, updated_at, decision_number) VALUES ($1, $1, 'ratified', $2, $2, $3)`, d.id, at(i), d.number)
	}
	mustExecMig(t, db, `INSERT INTO smeldr_goals (id, slug, created_at, updated_at, goal_id) VALUES ('g-1', 'g-1', $1, $1, 'T114')`, at(10))
	mustExecMig(t, db, `INSERT INTO smeldr_tasks (id, slug, created_at, updated_at, task_id, note_ref) VALUES ('t-1', 't-1', $1, $1, '', 'T114')`, at(11))
	mustExecMig(t, db, `INSERT INTO smeldr_tasks (id, slug, created_at, updated_at, task_id) VALUES ('t-2', 't-2', $1, $1, 'T7')`, at(12))
	mustExecMig(t, db, `INSERT INTO smeldr_amendments (id, slug, created_at, updated_at, amendment_number, amends) VALUES ('a-1', 'a-1', $1, $1, 'A1', 'D5')`, at(13))
	mustExecMig(t, db, `INSERT INTO smeldr_amendments (id, slug, created_at, updated_at, amendment_number, amends) VALUES ('a-2', 'a-2', $1, $1, 'A2', 'D3')`, at(14))
	mustExecMig(t, db, `INSERT INTO smeldr_relations (id, source_type, source_id, target_type, target_id, relation_kind, edge_class, created_at, updated_at)
		VALUES ('e-1', 'Amendment', 'a-1', 'Decision', 'd-b', 'amends', 'asserted', $1, $1)`, at(13))
	mustExecMig(t, db, `INSERT INTO smeldr_signals (id, slug, created_at, updated_at, task_ref) VALUES ('s-1', 's-1', $1, $1, 'D74-DECLINE')`, at(15))
	mustExecMig(t, db, `INSERT INTO smeldr_signals (id, slug, created_at, updated_at, task_ref) VALUES ('s-2', 's-2', $1, $1, 'D5')`, at(16))
	mustExecMig(t, db, `INSERT INTO smeldr_content_type_schemas (id, type_name, label, kind, url_prefix, fields, created_at, updated_at)
		VALUES ('sch', 'domain', 'Domain', 'content', '', '[{"Name":"Title","Type":"string","Role":"title"}]', $1, $1)`, at(0))
	for i := range 2 {
		mustExecMig(t, db, `INSERT INTO smeldr_dynamic_content (id, slug, type_name, created_at, updated_at) VALUES ($1, $1, 'domain', $2, $2)`,
			fmt.Sprintf("dom-%d", i), at(20+i))
	}

	// The first boot after deploy: counters seeded, record types registered.
	if err := CreateOrchestrationTables(db); err != nil {
		t.Fatal(err)
	}
	app := New(Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16"), DB: db})
	app.ServeDynamicContent()
	RegisterOrchestrationTypes(app, db)
	_ = app.Handler()

	// After deploy: records with addresses, and one raw Signal without.
	dec := findModule[*Decision](t, app)
	if out, err := dec.MCPCreate(authorCtx(), map[string]any{"Title": "after deploy"}); err != nil || out.(*Decision).Address != "D6" {
		t.Fatalf("post-deploy Decision: %v %v, want D6 (above the highest legacy D5)", out, err)
	}
	goal := findModule[*Goal](t, app)
	if out, err := goal.MCPCreate(authorCtx(), map[string]any{"Description": "after deploy"}); err != nil || out.(*Goal).Address != "G1" {
		t.Fatalf("post-deploy Goal: %v %v, want G1", out, err)
	}
	s := &Signal{Sender: "core", SignalType: "plan-ready"}
	if err := InsertSignal(ctx, db, s); err != nil || s.Address != "S3" {
		t.Fatalf("post-deploy Signal: %q %v, want S3 (2 old Signals reserved)", s.Address, err)
	}
	mustExecMig(t, db, `INSERT INTO smeldr_signals (id, slug, created_at, updated_at) VALUES ('s-raw', 's-raw', $1, $1)`, at(100))
	repo, err := app.DynamicContentRepo("domain")
	if err != nil {
		t.Fatal(err)
	}
	if n, err := repo.CreateDraft(ctx, map[string]any{"Title": "new"}); err != nil || n.Address != "DOM3" {
		t.Fatalf("post-deploy domain: %v %v, want DOM3", n, err)
	}
	return db, app
}

func mustExecMig(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func addressOfRow(t *testing.T, db *sql.DB, table, id string) (address, legacy, externalRef string) {
	t.Helper()
	legacyCol := map[string]string{"smeldr_decisions": "decision_number", "smeldr_goals": "goal_id", "smeldr_tasks": "task_id", "smeldr_amendments": "amendment_number"}[table]
	if legacyCol == "" {
		legacyCol = "''"
	}
	if err := db.QueryRow(`SELECT address, `+legacyCol+`, external_ref FROM `+table+` WHERE id = $1`, id).Scan(&address, &legacy, &externalRef); err != nil {
		t.Fatalf("read %s %s: %v", table, id, err)
	}
	return
}

func typeReport(rep *AddressMigrationReport, name string) TypeAddressReport {
	for _, t := range rep.Types {
		if t.TypeName == name {
			return t
		}
	}
	return TypeAddressReport{}
}

// Records created after deploy keep their addresses; every renumbered row is
// numbered through the counter, above them; every address stays unique.
func TestMigrateAddresses_AfterSeedingKeepsUniqueness(t *testing.T) {
	db, app := migrationFixture(t)
	rep, err := app.MigrateAddresses(context.Background(), AddressMigrationOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"d-a": "D3",  // conforming and unique: kept
		"d-b": "D7",  // duplicated D5: renumbered, above the post-deploy D6
		"d-c": "D8",  // its duplicate: renumbered too
		"d-d": "D9",  // free text
		"d-e": "D10", // empty
	}
	for id, addr := range want {
		got, legacy, _ := addressOfRow(t, db, "smeldr_decisions", id)
		if got != addr || legacy != addr {
			t.Errorf("%s: address %q legacy %q, want both %s", id, got, legacy, addr)
		}
	}
	if _, _, ref := addressOfRow(t, db, "smeldr_decisions", "d-d"); ref != "D74-DECLINE" {
		t.Errorf("d-d external_ref = %q, want the old value", ref)
	}
	if _, _, ref := addressOfRow(t, db, "smeldr_decisions", "d-a"); ref != "" {
		t.Errorf("a kept row's external_ref = %q, want empty", ref)
	}
	for table, want := range map[string]int{"smeldr_decisions": 6, "smeldr_goals": 2, "smeldr_tasks": 2, "smeldr_signals": 4, "smeldr_amendments": 2} {
		var n, distinct int
		_ = db.QueryRow(`SELECT COUNT(*), COUNT(DISTINCT address) FROM `+table+` WHERE address <> ''`).Scan(&n, &distinct)
		if n != want || distinct != want {
			t.Errorf("%s: %d addresses (%d distinct), want %d unique", table, n, distinct, want)
		}
	}
	if g, _, ref := addressOfRow(t, db, "smeldr_goals", "g-1"); g != "G2" || ref != "T114" {
		t.Errorf("goal with T114: %q (ref %q), want G2 above the post-deploy G1", g, ref)
	}
	if a, _, _ := addressOfRow(t, db, "smeldr_tasks", "t-2"); a != "T7" {
		t.Errorf("task T7 = %q, want kept", a)
	}
	if a, _, _ := addressOfRow(t, db, "smeldr_tasks", "t-1"); a != "T8" {
		t.Errorf("task without id = %q, want T8", a)
	}
	for id, addr := range map[string]string{"s-1": "S1", "s-2": "S2", "s-raw": "S4"} {
		if got, _, _ := addressOfRow(t, db, "smeldr_signals", id); got != addr {
			t.Errorf("signal %s = %q, want %s", id, got, addr)
		}
	}
	var dom []string
	rows, _ := db.Query(`SELECT address FROM smeldr_dynamic_content WHERE type_name = 'domain' ORDER BY created_at`)
	for rows.Next() {
		var a string
		_ = rows.Scan(&a)
		dom = append(dom, a)
	}
	rows.Close()
	if strings.Join(dom, ",") != "DOM1,DOM2,DOM3" {
		t.Errorf("domain addresses = %v, want DOM1,DOM2,DOM3", dom)
	}

	dr := typeReport(rep, "Decision")
	if dr.Kept != 1 || dr.Renumbered != 4 || dr.Before.WithAddress != 1 || dr.Before.WithoutAddress != 5 || dr.After.WithAddress != 6 || dr.After.WithoutAddress != 0 {
		t.Errorf("Decision report = %+v", dr)
	}
	if sr := typeReport(rep, "Signal"); sr.NewlyNumbered != 3 || sr.Prefix != "S" {
		t.Errorf("Signal report = %+v", sr)
	}
	var reserved int64
	_ = db.QueryRow(`SELECT reserved_no FROM smeldr_address_counters WHERE type_name = 'Signal'`).Scan(&reserved)
	if reserved != 0 {
		t.Errorf("reserved_no after the migration = %d, want 0", reserved)
	}
	var last int64
	_ = db.QueryRow(`SELECT last_no FROM smeldr_address_counters WHERE type_name = 'Decision'`).Scan(&last)
	if last != 10 {
		t.Errorf("Decision counter = %d, want 10", last)
	}
}

// References to renumbered records are rewritten where they name exactly one;
// the rest are listed.
func TestMigrateAddresses_References(t *testing.T) {
	db, app := migrationFixture(t)
	rep, err := app.MigrateAddresses(context.Background(), AddressMigrationOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var amends string
	_ = db.QueryRow(`SELECT amends FROM smeldr_amendments WHERE id = 'a-1'`).Scan(&amends)
	if amends != "D7" {
		t.Errorf("a-1 amends = %q, want D7 (its edge names d-b, renumbered)", amends)
	}
	var noteRef, taskRef1, taskRef2 string
	_ = db.QueryRow(`SELECT note_ref FROM smeldr_tasks WHERE id = 't-1'`).Scan(&noteRef)
	_ = db.QueryRow(`SELECT task_ref FROM smeldr_signals WHERE id = 's-1'`).Scan(&taskRef1)
	_ = db.QueryRow(`SELECT task_ref FROM smeldr_signals WHERE id = 's-2'`).Scan(&taskRef2)
	if noteRef != "G2" || taskRef1 != "D9" {
		t.Errorf("note_ref %q task_ref %q, want G2 and D9", noteRef, taskRef1)
	}
	if taskRef2 != "D5" {
		t.Errorf("an ambiguous task_ref was rewritten to %q", taskRef2)
	}
	var sawAmbiguous, sawNoEdge bool
	for _, n := range rep.LeftAlone {
		if n.ID == "s-2" && strings.Contains(n.Reason, "2 renumbered") {
			sawAmbiguous = true
		}
		if n.ID == "a-2" && strings.Contains(n.Reason, "no live amends edge") {
			sawNoEdge = true
		}
	}
	if !sawAmbiguous || !sawNoEdge {
		t.Errorf("left alone = %+v, want the ambiguous task_ref and the amends without an edge", rep.LeftAlone)
	}
	if len(rep.Rewritten) != 3 {
		t.Errorf("rewritten = %+v, want 3 (amends, note_ref, task_ref)", rep.Rewritten)
	}
}

// A dry run writes nothing and reports exactly what the apply then does; a
// second apply changes nothing.
func TestMigrateAddresses_DryRunAndIdempotence(t *testing.T) {
	db, app := migrationFixture(t)
	ctx := context.Background()
	snapshot := func() string {
		var b strings.Builder
		for _, q := range []string{
			`SELECT id || address || external_ref || decision_number FROM smeldr_decisions ORDER BY id`,
			`SELECT id || address || task_ref FROM smeldr_signals ORDER BY id`,
			`SELECT type_name || last_no || reserved_no FROM smeldr_address_counters ORDER BY type_name`,
			`SELECT id || amends FROM smeldr_amendments ORDER BY id`,
		} {
			rows, err := db.Query(q)
			if err != nil {
				t.Fatal(err)
			}
			for rows.Next() {
				var s string
				_ = rows.Scan(&s)
				b.WriteString(s + ";")
			}
			rows.Close()
		}
		return b.String()
	}
	before := snapshot()
	dry, err := app.MigrateAddresses(ctx, AddressMigrationOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot() != before {
		t.Fatal("the dry run wrote to the database")
	}
	applied, err := app.MigrateAddresses(ctx, AddressMigrationOptions{})
	if err != nil {
		t.Fatal(err)
	}
	strip := func(r *AddressMigrationReport) string {
		c := *r
		c.DryRun, c.DurationMS = false, 0
		b, _ := json.Marshal(c)
		return string(b)
	}
	if strip(dry) != strip(applied) {
		t.Errorf("dry run report differs from the apply:\n dry   %s\n apply %s", strip(dry), strip(applied))
	}
	again, err := app.MigrateAddresses(ctx, AddressMigrationOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, tr := range again.Types {
		if tr.Kept+tr.Renumbered+tr.NewlyNumbered != 0 || tr.Before.WithoutAddress != 0 {
			t.Errorf("second run changed %+v", tr)
		}
	}
	if len(again.Rewritten) != 0 {
		t.Errorf("second run rewrote %+v", again.Rewritten)
	}
}

// A pre-deploy row deleted after seeding frees a reserved slot, which a later
// unaddressed row (a post-deploy raw insert) then takes: still unique, and
// nothing allocated by the counter is ever touched.
func TestMigrateAddresses_DeletedPreDeployRowLetsRawRowTakeReservedSlot(t *testing.T) {
	ctx := context.Background()
	db := newSQLiteDB(t)
	if err := CreateOrchestrationTables(db); err != nil {
		t.Fatal(err)
	}
	mustExecMig(t, db, `DELETE FROM smeldr_address_counters`)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range 2 {
		mustExecMig(t, db, `INSERT INTO smeldr_signals (id, slug, created_at, updated_at) VALUES ($1, $1, $2, $2)`, fmt.Sprintf("old-%d", i), base.Add(time.Duration(i)*time.Minute))
	}
	if err := CreateOrchestrationTables(db); err != nil {
		t.Fatal(err)
	}
	mustExecMig(t, db, `DELETE FROM smeldr_signals WHERE id = 'old-1'`)
	s := &Signal{Sender: "core"}
	if err := InsertSignal(ctx, db, s); err != nil || s.Address != "S3" {
		t.Fatalf("post-deploy Signal = %q %v, want S3", s.Address, err)
	}
	mustExecMig(t, db, `INSERT INTO smeldr_signals (id, slug, created_at, updated_at) VALUES ('raw', 'raw', $1, $1)`, base.Add(time.Hour))
	if _, err := MigrateAddresses(ctx, db, AddressMigrationOptions{}); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	rows, _ := db.Query(`SELECT id, address FROM smeldr_signals`)
	for rows.Next() {
		var id, a string
		_ = rows.Scan(&id, &a)
		got[id] = a
	}
	rows.Close()
	if got["old-0"] != "S1" || got["raw"] != "S2" || got[s.ID] != "S3" {
		t.Errorf("addresses = %v, want old-0 S1, the raw row the freed S2, the post-deploy S3", got)
	}
}

// A record type with no prefix is reported and skipped; no database is a bad
// request.
func TestMigrateAddresses_Errors(t *testing.T) {
	ctx := context.Background()
	if _, err := MigrateAddresses(ctx, nil, AddressMigrationOptions{}); err == nil {
		t.Error("no database: want an error")
	}
	db := newSQLiteDB(t)
	for _, f := range []func(DB) error{CreateOrchestrationTables, CreateBlockTables, CreateSchemaTable} {
		if err := f(db); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC()
	mustExecMig(t, db, `INSERT INTO smeldr_content_type_schemas (id, type_name, label, kind, url_prefix, fields, created_at, updated_at)
		VALUES ('s', 'orphan', 'Orphan', 'content', '', '[]', $1, $1)`, now)
	rep, err := MigrateAddresses(ctx, db, AddressMigrationOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if tr := typeReport(rep, "orphan"); !strings.Contains(tr.Error, "no address prefix") {
		t.Errorf("orphan report = %+v, want the type error", tr)
	}
	if _, err := MigrateAddresses(ctx, failAddressDB{db, "UPDATE smeldr_address_counters SET reserved_no"}, AddressMigrationOptions{}); err == nil {
		// Only reached when some type had a reserve; this database has none,
		// so the failing statement is never run.
		t.Log("no reserve to release")
	}
	if _, err := MigrateAddresses(ctx, failAddressDB{db, "SELECT prefix, reserved_no"}, AddressMigrationOptions{}); err == nil {
		t.Error("a failing prefix read: want an error")
	}
}

// A failing read or write anywhere in the migration is an ErrInternal naming
// the step; nothing is reported as done.
func TestMigrateAddresses_StepErrors(t *testing.T) {
	steps := []string{
		"SELECT prefix, reserved_no",
		"SELECT COUNT(*) FROM smeldr_decisions",
		"SELECT address FROM smeldr_decisions",
		"SELECT decision_number FROM smeldr_decisions",
		"ORDER BY created_at, id",
		"UPDATE smeldr_decisions SET address",
		"UPDATE smeldr_address_counters SET last_no",
		"UPDATE smeldr_address_counters SET reserved_no",
		"SELECT id, amends FROM smeldr_amendments",
		"FROM smeldr_relations",
		"UPDATE smeldr_amendments SET amends",
		"SELECT id, task_ref FROM smeldr_signals",
		"UPDATE smeldr_signals SET task_ref",
		"SELECT type_name FROM smeldr_content_type_schemas",
	}
	for _, step := range steps {
		t.Run(step, func(t *testing.T) {
			db, _ := migrationFixture(t)
			if _, err := MigrateAddresses(context.Background(), failAddressDB{db, step}, AddressMigrationOptions{}); !errors.Is(err, ErrInternal) {
				t.Errorf("err = %v, want ErrInternal when %q fails", err, step)
			}
		})
	}
}
