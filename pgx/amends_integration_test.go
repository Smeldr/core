//go:build integration

package pgx

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	smeldr "smeldr.dev/core"
)

// The amends relation on Postgres: the column added to an Amendment table that
// predates it, a creation that names a Decision (refused when it names none, an
// edge asserted when it does), write-once on update, and the backfill.
func TestPG_AmendsRelation(t *testing.T) {
	db, _ := isolatedDB(t)
	ctx := context.Background()

	// An Amendment table as it was before the amends column existed.
	if _, err := db.ExecContext(ctx, `CREATE TABLE smeldr_amendments (
		id TEXT PRIMARY KEY, slug TEXT NOT NULL UNIQUE, status TEXT NOT NULL DEFAULT 'draft',
		published_at TIMESTAMPTZ, scheduled_at TIMESTAMPTZ, created_at TIMESTAMPTZ NOT NULL,
		updated_at TIMESTAMPTZ NOT NULL, rev INTEGER NOT NULL DEFAULT 0,
		amendment_number TEXT NOT NULL DEFAULT '', amendment_type TEXT NOT NULL DEFAULT '',
		version TEXT NOT NULL DEFAULT '', commit_hash TEXT NOT NULL DEFAULT '', pilot TEXT NOT NULL DEFAULT '',
		summary TEXT NOT NULL DEFAULT '', body TEXT NOT NULL DEFAULT '', last_actor TEXT NOT NULL DEFAULT '')`); err != nil {
		t.Fatalf("legacy table: %v", err)
	}
	if err := smeldr.CreateOrchestrationTables(db); err != nil {
		t.Fatalf("CreateOrchestrationTables: %v", err)
	}
	if err := smeldr.CreateRelationTables(db); err != nil {
		t.Fatalf("CreateRelationTables: %v", err)
	}
	if err := smeldr.EnsureAmendmentAmendsColumn(ctx, db); err != nil {
		t.Fatalf("EnsureAmendmentAmendsColumn: %v", err)
	}
	if err := smeldr.EnsureAmendmentAmendsColumn(ctx, db); err != nil {
		t.Fatalf("EnsureAmendmentAmendsColumn (second call): %v", err)
	}
	rs, err := smeldr.NewRelationStore(db)
	if err != nil {
		t.Fatalf("NewRelationStore: %v", err)
	}
	if err := smeldr.RegisterOrchestrationRelationKinds(ctx, rs); err != nil {
		t.Fatalf("RegisterOrchestrationRelationKinds: %v", err)
	}
	app := smeldr.New(smeldr.Config{BaseURL: "http://localhost", Secret: []byte(pgTestSecret), DB: db})
	app.Relations(rs)
	smeldr.RegisterOrchestrationTypes(app, db)
	h := app.Handler()

	decisions := smeldr.NewSQLRepo[*smeldr.Decision](db, smeldr.Table("smeldr_decisions"))
	for id, number := range map[string]string{"dec-1": "D1", "dec-2": "2"} {
		if err := decisions.Save(ctx, &smeldr.Decision{Node: smeldr.Node{ID: id, Slug: id, Status: "ratified"}, DecisionNumber: number}); err != nil {
			t.Fatalf("seed Decision: %v", err)
		}
	}
	tok, err := smeldr.SignToken(smeldr.User{ID: "pg-user", Roles: []smeldr.Role{smeldr.Editor}}, pgTestSecret, 0)
	if err != nil {
		t.Fatal(err)
	}
	do := func(method, path string, body any) *httptest.ResponseRecorder {
		b, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(b))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+tok)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}

	if w := do(http.MethodPost, "/amendments", map[string]any{"amendment_number": "A1", "slug": "a1", "amends": "D404"}); w.Code < 400 {
		t.Errorf("create naming a missing Decision = %d, want a refusal", w.Code)
	}
	w := do(http.MethodPost, "/amendments", map[string]any{"amendment_number": "A2", "slug": "a2", "amends": "2"})
	if w.Code >= 300 {
		t.Fatalf("create = %d %s", w.Code, w.Body.String())
	}
	var created smeldr.Amendment
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil || created.Amends != "D2" {
		t.Fatalf("created = %+v, %v, want amends canonicalised to D2", created, err)
	}
	edges, err := rs.GetLiveBySource(ctx, "Amendment", created.ID, "amends")
	if err != nil || len(edges) != 1 || edges[0].TargetID != "dec-2" {
		t.Fatalf("edges = %+v, %v, want one to dec-2", edges, err)
	}

	var cur smeldr.Amendment
	if err := json.Unmarshal(do(http.MethodGet, "/amendments/a2", nil).Body.Bytes(), &cur); err != nil {
		t.Fatal(err)
	}
	w = do(http.MethodPut, "/amendments/a2", map[string]any{"amendment_number": "A2", "amends": "D1", "rev": cur.Rev, "status": string(cur.Status)})
	if w.Code < 400 || !strings.Contains(w.Body.String(), "write-once") {
		t.Errorf("PUT changing amends = %d %s, want a write-once refusal", w.Code, w.Body.String())
	}

	// Backfill an Amendment created without amends.
	if w := do(http.MethodPost, "/amendments", map[string]any{"amendment_number": "A3", "slug": "a3"}); w.Code >= 300 {
		t.Fatalf("create plain = %d %s", w.Code, w.Body.String())
	}
	admin := smeldr.NewTestContext(smeldr.User{ID: "pg-admin", Roles: []smeldr.Role{smeldr.Admin}})
	rep, err := app.BackfillAmendsEdges(admin, []smeldr.AmendsLink{{AmendmentNumber: "A3", DecisionNumber: "D1"}, {AmendmentNumber: "A2", DecisionNumber: "D2"}}, false)
	if err != nil || rep.Asserted != 1 || rep.AlreadyPresent != 1 {
		t.Errorf("backfill = %+v, %v, want one asserted and one already present", rep, err)
	}
	cands, err := app.AmendsBackfillCandidates(ctx)
	if err != nil {
		t.Fatalf("AmendsBackfillCandidates: %v", err)
	}
	for _, c := range cands {
		if c.AmendmentNumber == "A2" || c.AmendmentNumber == "A3" {
			t.Errorf("candidate %+v listed although it is linked", c)
		}
	}
}
