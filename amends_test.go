// AGPL-3.0-or-later

package smeldr

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const amendsTestSecret = "amends-test-secret-32-bytes-xxxxxx"

type amendsEnv struct {
	app  *App
	m    *Module[*Amendment]
	db   *sql.DB
	rs   *RelationStore
	prov *fakeProvenanceStore
	h    http.Handler
}

// newAmendsEnv wires what a core instance with orchestration, relations and
// provenance has, with the Amendment module carrying the same hooks
// RegisterOrchestrationTypes gives it.
func newAmendsEnv(t *testing.T, withRelations bool) *amendsEnv {
	t.Helper()
	db := newSQLiteDB(t)
	if err := CreateOrchestrationTables(db); err != nil {
		t.Fatalf("CreateOrchestrationTables: %v", err)
	}
	app := New(MustConfig(Config{BaseURL: "https://example.com", Secret: []byte(amendsTestSecret), DB: db}))
	env := &amendsEnv{app: app, db: db, prov: &fakeProvenanceStore{}}
	if withRelations {
		if err := CreateRelationTables(db); err != nil {
			t.Fatalf("CreateRelationTables: %v", err)
		}
		rs, err := NewRelationStore(db)
		if err != nil {
			t.Fatalf("NewRelationStore: %v", err)
		}
		if err := RegisterOrchestrationRelationKinds(context.Background(), rs); err != nil {
			t.Fatalf("RegisterOrchestrationRelationKinds: %v", err)
		}
		app.Relations(rs)
		env.rs = rs
	}
	if err := app.RegisterFlow(orchAmendmentFlow()); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
	app.Provenance(env.prov)
	env.m = NewModule[*Amendment]((*Amendment)(nil),
		At("/amendments"), Repo(NewSQLRepo[*Amendment](db, Table("smeldr_amendments"))), MCP(MCPRead, MCPWrite),
		amendsSaveHooks(),
	)
	app.Content(env.m)
	env.h = app.Handler()
	return env
}

func (e *amendsEnv) decision(t *testing.T, id, number string) {
	t.Helper()
	repo := NewSQLRepo[*Decision](e.db, Table("smeldr_decisions"))
	if err := repo.Save(context.Background(), &Decision{Node: Node{ID: id, Slug: id + "-slug", Status: "ratified"}, DecisionNumber: number}); err != nil {
		t.Fatalf("seed Decision %s: %v", number, err)
	}
}

func amCtx() Context { return NewTestContext(User{ID: "recorder-1", Roles: []Role{Editor, Agent}}) }

func (e *amendsEnv) create(t *testing.T, fields map[string]any) (*Amendment, error) {
	t.Helper()
	out, err := e.m.MCPCreate(amCtx(), fields)
	if err != nil {
		return nil, err
	}
	return out.(*Amendment), nil
}

func (e *amendsEnv) liveAmends(t *testing.T, amendmentID string) []RelationEdge {
	t.Helper()
	edges, err := e.rs.GetLiveBySource(context.Background(), "Amendment", amendmentID, "amends")
	if err != nil {
		t.Fatalf("GetLiveBySource: %v", err)
	}
	return edges
}

func TestAmends_KindIsRegisteredWithOnlyThePair(t *testing.T) {
	e := newAmendsEnv(t, true)
	ctx := context.Background()
	if err := RegisterOrchestrationRelationKinds(ctx, e.rs); err != nil {
		t.Fatalf("second registration: %v", err)
	}
	kinds := e.rs.MCPListRelationKinds()
	found := false
	for _, k := range kinds {
		if k.TypeName == "amends" {
			found = true
			if string(k.TypePairs) != `[{"source_type":"Amendment","target_type":"Decision"}]` || !k.Directional || k.ReverseLabel != "Amended By" {
				t.Errorf("amends kind = %+v", k)
			}
		}
	}
	if !found {
		t.Fatal("amends is not registered")
	}
	// Another pair is refused.
	if err := e.rs.Assert(ctx, RelationEdge{SourceType: "Task", SourceID: "t", TargetType: "Decision", TargetID: "d", RelationKind: "amends", EdgeClass: "asserted"}); err == nil {
		t.Error("an amends edge from a Task was accepted")
	}
}

func TestAmends_CreateAssertsOneEdgeAndRecordsWho(t *testing.T) {
	e := newAmendsEnv(t, true)
	e.decision(t, "dec-7", "D7")
	a, err := e.create(t, map[string]any{"amendment_number": "A900", "summary": "x", "amends": "D7"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if a.Amends != "D7" {
		t.Errorf("stored amends = %q, want D7", a.Amends)
	}
	edges := e.liveAmends(t, a.ID)
	if len(edges) != 1 || edges[0].TargetType != "Decision" || edges[0].TargetID != "dec-7" || edges[0].EdgeClass != "asserted" {
		t.Fatalf("edges = %+v, want one asserted edge to dec-7", edges)
	}
	var rec *ProvenanceRecord
	for _, r := range e.prov.Appended() {
		if r.Verb == "assert" && r.SubjectType == "RelationEdge" {
			r := r
			rec = &r
		}
	}
	if rec == nil || rec.ActorID != "recorder-1" || rec.ActorKind != "agent" {
		t.Errorf("assert record = %+v, want the recorder (agent) as the actor", rec)
	}
}

func TestAmends_BothSpellingsResolve(t *testing.T) {
	e := newAmendsEnv(t, true)
	e.decision(t, "dec-old", "9") // an older row carries the bare number
	for _, in := range []string{"D9", "d9", "9"} {
		a, err := e.create(t, map[string]any{"amendment_number": "A-" + in, "amends": in})
		if err != nil {
			t.Fatalf("amends %q: %v", in, err)
		}
		if a.Amends != "D9" || len(e.liveAmends(t, a.ID)) != 1 {
			t.Errorf("amends %q stored %q with %d edges, want D9 and one edge", in, a.Amends, len(e.liveAmends(t, a.ID)))
		}
	}
}

func TestAmends_UnknownAmbiguousAndMalformedRefuseTheCreation(t *testing.T) {
	e := newAmendsEnv(t, true)
	e.decision(t, "dec-a", "D5")
	e.decision(t, "dec-b", "5") // the same number, two spellings: ambiguous
	for name, in := range map[string]string{"unknown": "D999", "ambiguous": "D5", "malformed": "decision five"} {
		_, err := e.create(t, map[string]any{"amendment_number": "A-" + name, "amends": in})
		var ve *ValidationError
		if !errors.As(err, &ve) {
			t.Errorf("%s: err = %v, want a ValidationError", name, err)
		}
	}
	var n int
	if err := e.db.QueryRow(`SELECT COUNT(*) FROM smeldr_amendments`).Scan(&n); err != nil || n != 0 {
		t.Errorf("%d Amendments stored after refused creates (%v), want 0", n, err)
	}
}

func TestAmends_NoAmendsNoEdge(t *testing.T) {
	e := newAmendsEnv(t, true)
	a, err := e.create(t, map[string]any{"amendment_number": "A901", "summary": "fixes code"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if a.Amends != "" || len(e.liveAmends(t, a.ID)) != 0 {
		t.Errorf("amends %q with %d edges, want none", a.Amends, len(e.liveAmends(t, a.ID)))
	}
}

func TestAmends_NoRelationStoreStillCreates(t *testing.T) {
	e := newAmendsEnv(t, false)
	e.decision(t, "dec-1", "D1")
	a, err := e.create(t, map[string]any{"amendment_number": "A902", "amends": "D1"})
	if err != nil || a.Amends != "D1" {
		t.Fatalf("create without relations = %+v, %v, want it created with the field kept", a, err)
	}
}

// Write-once: set at creation, or later only while empty; a different value is
// refused; an empty value never clears; the same value is accepted; the edge is
// asserted once, when the field becomes set, and not again.
func TestAmends_WriteOnce(t *testing.T) {
	e := newAmendsEnv(t, true)
	e.decision(t, "dec-1", "D1")
	e.decision(t, "dec-2", "D2")

	a, _ := e.create(t, map[string]any{"amendment_number": "A903", "amends": "D1"})
	if _, err := e.m.MCPUpdate(amCtx(), a.Slug, map[string]any{"amends": "D2"}); err == nil || !strings.Contains(err.Error(), "write-once") {
		t.Errorf("changing amends = %v, want a write-once refusal", err)
	}
	if _, err := e.m.MCPUpdate(amCtx(), a.Slug, map[string]any{"amends": ""}); err != nil {
		t.Errorf("an empty amends must be accepted and ignored: %v", err)
	}
	if _, err := e.m.MCPUpdate(amCtx(), a.Slug, map[string]any{"amends": "1", "summary": "again"}); err != nil {
		t.Errorf("the same Decision spelled 1 must be accepted: %v", err)
	}
	got, _ := e.m.MCPGet(amCtx(), a.Slug)
	if got.(*Amendment).Amends != "D1" {
		t.Errorf("amends after the attempts = %q, want D1 unchanged", got.(*Amendment).Amends)
	}
	if n := len(e.liveAmends(t, a.ID)); n != 1 {
		t.Errorf("%d live edges after the attempts, want exactly 1", n)
	}

	// Set later, from empty: allowed, and the edge appears.
	b, _ := e.create(t, map[string]any{"amendment_number": "A904"})
	if _, err := e.m.MCPUpdate(amCtx(), b.Slug, map[string]any{"amends": "D2"}); err != nil {
		t.Fatalf("setting amends from empty: %v", err)
	}
	edges := e.liveAmends(t, b.ID)
	if len(edges) != 1 || edges[0].TargetID != "dec-2" {
		t.Errorf("edges = %+v, want one to dec-2", edges)
	}
}

func TestAmends_AssertIsIdempotent(t *testing.T) {
	e := newAmendsEnv(t, true)
	e.decision(t, "dec-1", "D1")
	a, _ := e.create(t, map[string]any{"amendment_number": "A905", "amends": "D1"})
	for i := 0; i < 2; i++ {
		asserted, err := assertAmendsEdge(context.Background(), e.db, e.rs, a.ID, "D1")
		if err != nil || asserted {
			t.Errorf("repeat %d = %v, %v, want no new edge", i, asserted, err)
		}
	}
	if n := len(e.liveAmends(t, a.ID)); n != 1 {
		t.Errorf("%d live edges, want 1", n)
	}
}

// A rejected Amendment's "amends" edge ends (D102), on every transition path:
// its row stays, ended, with an "invalidate" record naming the rejecting
// caller and the cause amendment-rejected. Rejecting it a second time, or any
// other transition, ends nothing more.
func TestAmends_RejectedAmendmentEndsItsEdge(t *testing.T) {
	rejecter := NewTestContext(User{ID: "rejecter-9", Roles: []Role{Editor}})
	cases := map[string]func(t *testing.T, e *amendsEnv, a *Amendment){
		"transition_item": func(t *testing.T, e *amendsEnv, a *Amendment) {
			if _, err := e.app.TransitionItem(rejecter, "Amendment", a.Slug, "rejected"); err != nil {
				t.Fatalf("reject: %v", err)
			}
		},
		"PUT": func(t *testing.T, e *amendsEnv, a *Amendment) {
			got, err := e.m.MCPGet(amCtx(), a.Slug)
			if err != nil {
				t.Fatalf("get: %v", err)
			}
			body := *got.(*Amendment)
			body.Status = "rejected"
			b, _ := json.Marshal(body)
			r := httptest.NewRequest(http.MethodPut, "/amendments/"+a.Slug, bytes.NewReader(b))
			r = withUser(r, rejecter.User())
			r.SetPathValue("slug", a.Slug)
			w := httptest.NewRecorder()
			e.m.updateHandler(w, r)
			if w.Code != http.StatusOK {
				t.Fatalf("PUT reject = %d: %s", w.Code, w.Body.String())
			}
		},
	}
	for name, reject := range cases {
		t.Run(name, func(t *testing.T) {
			e := newAmendsEnv(t, true)
			e.decision(t, "dec-1", "D1")
			a, _ := e.create(t, map[string]any{"amendment_number": "A906", "amends": "D1"})
			if _, err := e.app.TransitionItem(amCtx(), "Amendment", a.Slug, "in-progress"); err != nil {
				t.Fatalf("in-progress: %v", err)
			}
			reject(t, e, a)
			if n := len(e.liveAmends(t, a.ID)); n != 0 {
				t.Errorf("%d live edges after rejection, want 0", n)
			}
			all, err := e.rs.GetBySource(context.Background(), "Amendment", a.ID, "amends")
			if err != nil || len(all) != 1 || all[0].InvalidAt == nil {
				t.Fatalf("rows = %+v, %v, want the one row kept, ended", all, err)
			}
			var ends []ProvenanceRecord
			for _, r := range e.prov.Appended() {
				if r.SubjectType == "RelationEdge" && r.SubjectID == all[0].ID && r.Verb == "invalidate" {
					ends = append(ends, r)
				}
			}
			if len(ends) != 1 || ends[0].ToState != EdgeEndAmendmentRejected || ends[0].ActorID != "rejecter-9" || ends[0].FromState != "live" {
				t.Errorf("invalidate records = %+v, want one: live -> amendment-rejected by rejecter-9", ends)
			}
		})
	}
}

func TestEnsureAmendmentAmendsColumn(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()
	if _, err := db.Exec(`CREATE TABLE smeldr_amendments (id TEXT PRIMARY KEY, slug TEXT NOT NULL UNIQUE)`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := EnsureAmendmentAmendsColumn(ctx, db); err != nil {
			t.Fatalf("call %d: %v", i+1, err)
		}
	}
	if ok, err := columnExists(ctx, db, "smeldr_amendments", "amends"); err != nil || !ok {
		t.Errorf("column exists = %v, %v", ok, err)
	}
	if err := EnsureAmendmentAmendsColumn(ctx, &queryFailDB{}); err == nil {
		t.Error("a failing database must surface an error")
	}
}

func TestBackfillAmendsEdges(t *testing.T) {
	e := newAmendsEnv(t, true)
	e.decision(t, "dec-1", "D1")
	e.decision(t, "dec-2", "D2")
	plain, _ := e.create(t, map[string]any{"amendment_number": "A910", "summary": "mentions D1 in passing", "body": "see D2 too"})
	e.create(t, map[string]any{"amendment_number": "A911", "amends": "D1"})
	ctx := NewTestContext(User{ID: "admin-9", Roles: []Role{Admin}})
	links := []AmendsLink{
		{"A910", "D1"},   // to assert
		{"A911", "D1"},   // already present
		{"A912", "D1"},   // no such Amendment
		{"A910", "D404"}, // no such Decision
	}

	dry, err := e.app.BackfillAmendsEdges(ctx, links, true)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if !dry.DryRun || dry.Asserted != 1 || dry.AlreadyPresent != 1 || dry.Unresolved != 2 || len(e.liveAmends(t, plain.ID)) != 0 {
		t.Errorf("dry run = %+v, edges %d, want 1 would assert, 1 present, 2 unresolved and nothing written", dry, len(e.liveAmends(t, plain.ID)))
	}

	rep, err := e.app.BackfillAmendsEdges(ctx, links, false)
	if err != nil {
		t.Fatalf("real run: %v", err)
	}
	if rep.Asserted != 1 || rep.AlreadyPresent != 1 || rep.Unresolved != 2 {
		t.Errorf("report = %+v", rep)
	}
	edges := e.liveAmends(t, plain.ID)
	if len(edges) != 1 || edges[0].TargetID != "dec-1" {
		t.Fatalf("edges = %+v, want one to dec-1", edges)
	}
	got, _ := e.m.MCPGet(ctx, plain.Slug)
	if got.(*Amendment).Amends != "D1" {
		t.Errorf("the Amendment's amends = %q, want it filled in", got.(*Amendment).Amends)
	}
	var actor string
	for _, r := range e.prov.Appended() {
		if r.Verb == "assert" && r.ActorID == "admin-9" {
			actor = r.ActorID
		}
	}
	if actor != "admin-9" {
		t.Error("the backfill's assert record does not name the caller")
	}
	again, _ := e.app.BackfillAmendsEdges(ctx, links, false)
	if again.Asserted != 0 || again.AlreadyPresent != 2 {
		t.Errorf("second run = %+v, want nothing new and two present", again)
	}
}

func TestBackfillAmendsEdges_NeedsRelations(t *testing.T) {
	e := newAmendsEnv(t, false)
	if _, err := e.app.BackfillAmendsEdges(context.Background(), nil, true); !errors.Is(err, ErrBadRequest) {
		t.Errorf("err = %v, want ErrBadRequest", err)
	}
}

// The candidates list reads mentions and invents no link.
func TestAmendsBackfillCandidates(t *testing.T) {
	e := newAmendsEnv(t, true)
	e.decision(t, "dec-1", "D1")
	e.decision(t, "dec-3", "3")
	e.create(t, map[string]any{"amendment_number": "A920", "summary": "changes D1", "body": "and D3, and D77 which does not exist"})
	e.create(t, map[string]any{"amendment_number": "A921", "summary": "mentions nothing"})
	e.create(t, map[string]any{"amendment_number": "A922", "amends": "D1", "summary": "already linked, mentions D3"})
	got, err := e.app.AmendsBackfillCandidates(context.Background())
	if err != nil {
		t.Fatalf("AmendsBackfillCandidates: %v", err)
	}
	if len(got) != 1 || got[0].AmendmentNumber != "A920" || strings.Join(got[0].Mentions, ",") != "D1,D3" {
		t.Errorf("candidates = %+v, want only A920 with D1 and D3", got)
	}
}

// The HTTP create and PUT paths run the same check and step as MCP.
func TestAmends_HTTPPaths(t *testing.T) {
	e := newAmendsEnv(t, true)
	e.decision(t, "dec-1", "D1")
	e.decision(t, "dec-2", "D2")
	tok, err := SignToken(User{ID: "http-1", Roles: []Role{Editor}}, amendsTestSecret, 0)
	if err != nil {
		t.Fatal(err)
	}
	do := func(method, path string, body any) *httptest.ResponseRecorder {
		b, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(b))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+tok)
		w := httptest.NewRecorder()
		e.h.ServeHTTP(w, r)
		return w
	}

	if w := do(http.MethodPost, "/amendments", map[string]any{"amendment_number": "A930", "slug": "a930", "amends": "D404"}); w.Code < 400 {
		t.Errorf("create naming a missing Decision = %d, want a refusal", w.Code)
	}
	w := do(http.MethodPost, "/amendments", map[string]any{"amendment_number": "A931", "slug": "a931", "amends": "D1"})
	if w.Code >= 300 {
		t.Fatalf("create = %d %s", w.Code, w.Body.String())
	}
	var created Amendment
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil || created.Amends != "D1" {
		t.Fatalf("created = %+v, %v", created, err)
	}
	if n := len(e.liveAmends(t, created.ID)); n != 1 {
		t.Errorf("%d edges after the HTTP create, want 1", n)
	}
	// PUT is a full replace and needs the current rev: read it back first.
	var cur Amendment
	gw := do(http.MethodGet, "/amendments/a931", nil)
	if err := json.Unmarshal(gw.Body.Bytes(), &cur); err != nil {
		t.Fatalf("GET = %d %s: %v", gw.Code, gw.Body.String(), err)
	}
	created.Rev = cur.Rev
	w = do(http.MethodPut, "/amendments/a931", map[string]any{"amendment_number": "A931", "amends": "D2", "rev": created.Rev, "status": string(cur.Status)})
	if w.Code < 400 || !strings.Contains(w.Body.String(), "write-once") {
		t.Errorf("PUT changing amends = %d %s, want a write-once refusal", w.Code, w.Body.String())
	}
	if w := do(http.MethodPut, "/amendments/a931", map[string]any{"amendment_number": "A931", "summary": "edited", "rev": created.Rev, "status": string(cur.Status)}); w.Code >= 300 {
		t.Fatalf("PUT omitting amends = %d %s", w.Code, w.Body.String())
	}
	got, _ := e.m.MCPGet(amCtx(), "a931")
	if got.(*Amendment).Amends != "D1" {
		t.Errorf("amends after a PUT that omitted it = %q, want D1 kept", got.(*Amendment).Amends)
	}
}
