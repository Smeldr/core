//go:build integration

package pgx

import (
	"context"
	"fmt"
	"testing"

	smeldr "smeldr.dev/core"
)

// T919 part 2 (D150) on Postgres: the sweep stores buckets in one
// transaction (batched inserts, booleans, timestamps), and a domain-reader
// gets only the buckets of its Domain.
func TestPG_StoredRestCounts(t *testing.T) {
	db, _ := isolatedDB(t)
	ctx := context.Background()
	app, store := newPGApp(t, db)
	for _, create := range []func(smeldr.DB) error{smeldr.CreateGovernanceTables, smeldr.CreateRelationTables} {
		if err := create(db); err != nil {
			t.Fatal(err)
		}
	}
	if err := smeldr.EnsureRestCountTable(ctx, db); err != nil {
		t.Fatal(err)
	}
	createItemsTable(t, db, "rc_decs")
	registerType(app, "RcDec")
	if err := app.RegisterFlow(smeldr.StateFlow{Name: "rc", TypeName: "RcDec", States: []smeldr.State{
		{Name: "proposed", IsInitial: true, AwaitsAction: true}, {Name: "ratified"},
	}}); err != nil {
		t.Fatal(err)
	}
	edge := func(id, srcType, src, kind, tgtType, tgt string) {
		t.Helper()
		if _, err := db.ExecContext(ctx, `INSERT INTO smeldr_relations (id, source_type, source_id, target_type, target_id, relation_kind, edge_class, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, 'asserted', now(), now())`, id, srcType, src, tgtType, tgt, kind); err != nil {
			t.Fatal(err)
		}
	}
	edge("pa1", "Area", "A1", "belongs_to_domain", "Domain", "D1")
	edge("pb1", "Area", "B1", "belongs_to_domain", "Domain", "D2")
	insertItem(t, db, "rc_decs", "a", "ratified")
	edge("pa", "RcDec", "a", "belongs_to_area", "Area", "A1")
	insertItem(t, db, "rc_decs", "far", "ratified")
	edge("pfar", "RcDec", "far", "belongs_to_area", "Area", "B1")
	insertItem(t, db, "rc_decs", "behind", "proposed")
	edge("pbehind", "RcDec", "behind", "belongs_to_area", "Area", "A1")
	edge("e-far", "RcDec", "a", "grounded_in", "RcDec", "far")
	edge("e-behind", "RcDec", "far", "grounded_in", "RcDec", "behind")
	// 120 items in A1 rest on a: more than two insert batches of rows.
	for i := range 120 {
		id := fmt.Sprintf("n%03d", i)
		insertItem(t, db, "rc_decs", id, "proposed")
		edge("p"+id, "RcDec", id, "belongs_to_area", "Area", "A1")
		edge("e"+id, "RcDec", id, "grounded_in", "RcDec", "a")
	}
	run, err := app.SweepRestCounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if run.Items != 123 || run.Buckets < 120 {
		t.Errorf("run = %+v", run)
	}
	if _, err := app.SweepRestCounts(ctx); err != nil {
		t.Fatalf("second sweep: %v", err)
	}

	if err := smeldr.RegisterDomainReaderRole(ctx, store); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Grant(ctx, smeldr.RoleGrant{TokenID: "reader", RoleName: "domain-reader", ScopeAnchorID: "D1"}); err != nil {
		t.Fatal(err)
	}
	reader := smeldr.NewTestContext(smeldr.User{ID: "reader", Roles: []smeldr.Role{smeldr.Editor}})
	got, err := app.StoredRestCounts(reader, []smeldr.ItemRef{{Type: "RcDec", ID: "a"}})
	if err != nil || len(got) != 1 {
		t.Fatalf("StoredRestCounts = %+v %v", got, err)
	}
	// behind is in A1 but reached only through far in D2: not counted.
	if got[0].ThisRestsOn != (smeldr.RestCount{}) || got[0].RestsOnThis != (smeldr.RestCount{Open: 120}) || got[0].CountedAt.IsZero() {
		t.Errorf("domain-reader of D1 = %+v", got[0])
	}
	all, err := app.StoredRestCounts(smeldr.WithReadScope(ctx, smeldr.AllReadScope()), []smeldr.ItemRef{{Type: "RcDec", ID: "a"}})
	if err != nil || all[0].ThisRestsOn != (smeldr.RestCount{Open: 1, Closed: 1}) {
		t.Errorf("reads everything = %+v %v", all, err)
	}
}
