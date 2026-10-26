//go:build integration

package pgx

import (
	"context"
	"testing"

	smeldr "smeldr.dev/core"
)

// T953 on Postgres: ListFindings reads the findings newest first with the
// detector and type filters and the limit, on a scope that reads
// everything (an admin grant).
func TestPG_ListFindings(t *testing.T) {
	db, _ := isolatedDB(t)
	ctx := context.Background()
	app, roles := newPGApp(t, db)
	if _, err := roles.Grant(ctx, smeldr.RoleGrant{TokenID: "admin", RoleName: "admin"}); err != nil {
		t.Fatal(err)
	}
	if err := smeldr.CreateFindingTable(db); err != nil {
		t.Fatal(err)
	}
	store := smeldr.NewFindingStore(db)
	app.Findings(store)
	for _, f := range []smeldr.Finding{
		{Detector: "standing-drift", SubjectType: "Decision", SubjectID: "d1", Provenance: "detected", Message: "one"},
		{Detector: "standing-drift", SubjectType: "Decision", SubjectID: "d2", Provenance: "detected", Message: "two"},
		{Detector: "structural", SubjectType: "RelationEdge", SubjectID: "e1", Provenance: "detected", Message: "edge"},
	} {
		if err := store.Record(ctx, f); err != nil {
			t.Fatal(err)
		}
	}
	reader := smeldr.NewTestContext(smeldr.User{ID: "admin", Roles: []smeldr.Role{smeldr.Admin}})
	all, err := app.ListFindings(reader, smeldr.FindingQuery{})
	if err != nil || len(all) != 3 || all[0].SubjectID != "e1" {
		t.Fatalf("all = %+v %v", all, err)
	}
	if got, err := app.ListFindings(reader, smeldr.FindingQuery{Detector: "standing-drift", SubjectType: "Decision", Limit: 1}); err != nil || len(got) != 1 || got[0].SubjectID != "d2" {
		t.Errorf("filtered = %+v %v", got, err)
	}
}
