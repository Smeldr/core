//go:build integration

package pgx

import (
	"context"
	"fmt"
	"testing"

	smeldr "smeldr.dev/core"
)

// T919 (D150) on Postgres: the live rest counts walk a ring of more than one
// chunk (500 items resting on a hub), read states per type, split open and
// closed, leave out ended edges, and stop at RestCountLimit.
func TestPG_RestCounts(t *testing.T) {
	db, _ := isolatedDB(t)
	ctx := context.Background()
	app, _ := newPGApp(t, db)
	if err := smeldr.CreateRelationTables(db); err != nil {
		t.Fatal(err)
	}
	createItemsTable(t, db, "rc_decs")
	registerType(app, "RcDec")
	if err := app.RegisterFlow(smeldr.StateFlow{Name: "rc", TypeName: "RcDec", States: []smeldr.State{
		{Name: "proposed", IsInitial: true, AwaitsAction: true}, {Name: "ratified"},
	}}); err != nil {
		t.Fatal(err)
	}
	insertItem(t, db, "rc_decs", "hub", "ratified")
	insertItem(t, db, "rc_decs", "base", "ratified")
	edge := func(id, src, tgt, ended string) {
		t.Helper()
		if _, err := db.ExecContext(ctx, `INSERT INTO smeldr_relations (id, source_type, source_id, target_type, target_id, relation_kind, edge_class, invalid_at, created_at, updated_at)
			VALUES ($1, 'RcDec', $2, 'RcDec', $3, 'grounded_in', 'asserted', `+ended+`, now(), now())`, id, src, tgt); err != nil {
			t.Fatal(err)
		}
	}
	edge("e-base", "hub", "base", "NULL")
	edge("e-ended", "base", "hub", "now() - interval '1 hour'")
	for i := range 500 {
		id := fmt.Sprintf("n%03d", i)
		status := "ratified"
		if i%5 == 0 {
			status = "proposed"
		}
		insertItem(t, db, "rc_decs", id, status)
		edge("e-"+id, id, "hub", "NULL")
	}
	all := smeldr.WithReadScope(ctx, smeldr.AllReadScope())
	got, err := app.RestCounts(all, "RcDec", "hub")
	if err != nil {
		t.Fatal(err)
	}
	want := smeldr.RestCounts{RestsOnThis: smeldr.RestCount{Open: 100, Closed: 400}, ThisRestsOn: smeldr.RestCount{Closed: 1}}
	if got != want {
		t.Errorf("RestCounts(hub) = %+v, want %+v", got, want)
	}
	// base: the hub and, through it, the 500 rest on it.
	if got, err := app.RestCounts(all, "RcDec", "base"); err != nil || got.RestsOnThis != (smeldr.RestCount{Open: 100, Closed: 401}) {
		t.Errorf("RestCounts(base) = %+v %v", got, err)
	}
	for i := 500; i < smeldr.RestCountLimit+10; i++ {
		id := fmt.Sprintf("m%04d", i)
		insertItem(t, db, "rc_decs", id, "ratified")
		edge("e-"+id, id, "hub", "NULL")
	}
	if got, err := app.RestCounts(all, "RcDec", "hub"); err != nil || !got.RestsOnThis.AtLeast || got.RestsOnThis.Open+got.RestsOnThis.Closed != smeldr.RestCountLimit {
		t.Errorf("capped = %+v %v", got.RestsOnThis, err)
	}
}
