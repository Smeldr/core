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
	"time"
)

// T883: a Decision is ratified only with a place, and holds exactly one.

// stewardContext is a caller that may ratify (admin plus decision-steward).
func stewardContext(t *testing.T, db *sql.DB, rs *RoleStore) context.Context {
	t.Helper()
	tokenID := setupTokenWithRole(t, db, rs, "admin")
	if err := RegisterDecisionStewardRole(context.Background(), rs); err != nil {
		t.Fatalf("RegisterDecisionStewardRole: %v", err)
	}
	if _, err := rs.Grant(context.Background(), RoleGrant{TokenID: tokenID, RoleName: "decision-steward"}); err != nil {
		t.Fatalf("Grant decision-steward: %v", err)
	}
	return TrustedRead(NewTestContext(User{ID: tokenID}))
}

// placeEdge inserts a place edge from the Decision id.
func placeEdge(t *testing.T, db DB, id, targetType, targetID, kind, class string, invalidAt any) {
	t.Helper()
	if err := CreateRelationTables(db); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO smeldr_relations (id, source_type, source_id, target_type, target_id, relation_kind, edge_class, invalid_at, attributes, created_at, updated_at)
		 VALUES ($1, 'Decision', $2, $3, $4, $5, $6, $7, '{}', $8, $8)`,
		NewID(), id, targetType, targetID, kind, class, invalidAt, now); err != nil {
		t.Fatalf("insert edge: %v", err)
	}
}

func TestRatify_RefusedWithoutPlace(t *testing.T) {
	for _, from := range []string{"proposed", "pending-re-evaluation"} {
		t.Run(from, func(t *testing.T) {
			app, db, rs := setupTransitionItemApp(t)
			ctx := stewardContext(t, db, rs)
			insertDecision(t, db, "dec-u", "dec-u-slug", from)
			if _, err := db.Exec(`UPDATE smeldr_decisions SET address = 'D7' WHERE id = 'dec-u'`); err != nil {
				t.Fatal(err)
			}
			if err := CreateRelationTables(db); err != nil {
				t.Fatal(err)
			}
			_, err := app.TransitionItemVia(ctx, "mcp", "Decision", "dec-u-slug", "ratified", "")
			if !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "Decision D7 has no place") || !strings.Contains(err.Error(), "assert_relation belongs_to_domain") {
				t.Fatalf("err = %v; want a conflict naming D7 and saying how to place it", err)
			}
			var status string
			if err := db.QueryRow(`SELECT status FROM smeldr_decisions WHERE id = 'dec-u'`).Scan(&status); err != nil || status != from {
				t.Errorf("status = %q (%v); want %q unchanged", status, err, from)
			}
		})
	}
}

// The refusal comes before the authorization check: a caller without approve
// is told about the place first (Q2).
func TestRatify_PlaceBeforeAuthorization(t *testing.T) {
	app, db, _ := setupTransitionItemApp(t)
	insertDecision(t, db, "dec-a", "dec-a-slug", "proposed")
	if err := CreateRelationTables(db); err != nil {
		t.Fatal(err)
	}
	_, err := app.TransitionItem(TrustedRead(NewTestContext(User{ID: "author-1"})), "Decision", "dec-a-slug", "ratified")
	if !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "Decision dec-a has no place") {
		t.Errorf("err = %v; want the place conflict, labelled by ID when there is no address", err)
	}
}

func TestRatify_PlacedPasses(t *testing.T) {
	for _, tc := range []struct{ targetType, kind string }{
		{"Root", "belongs_to_root"},
		{"Domain", "belongs_to_domain"},
		{"Area", "belongs_to_area"},
		{"domain", "belongs_to_domain"}, // before migrate-places
	} {
		t.Run(tc.targetType, func(t *testing.T) {
			app, db, rs := setupTransitionItemApp(t)
			ctx := stewardContext(t, db, rs)
			insertDecision(t, db, "dec-p", "dec-p-slug", "proposed")
			placeEdge(t, db, "dec-p", tc.targetType, "place-1", tc.kind, "asserted", nil)
			if _, err := app.TransitionItemVia(ctx, "mcp", "Decision", "dec-p-slug", "ratified", ""); err != nil {
				t.Fatalf("ratify placed in %s: %v", tc.targetType, err)
			}
		})
	}
}

// An ended edge, a proposed edge and an edge of another kind are no place.
func TestRatify_EdgesThatAreNoPlace(t *testing.T) {
	past := time.Now().UTC().Add(-time.Hour)
	for _, tc := range []struct {
		name, kind, class string
		invalid           any
	}{
		{"ended", "belongs_to_domain", "asserted", past},
		{"proposed edge", "belongs_to_domain", "proposed", nil},
		{"other kind", "relates_to", "asserted", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, db, rs := setupTransitionItemApp(t)
			ctx := stewardContext(t, db, rs)
			insertDecision(t, db, "dec-n", "dec-n-slug", "proposed")
			placeEdge(t, db, "dec-n", "Domain", "dom-1", tc.kind, tc.class, tc.invalid)
			if _, err := app.TransitionItemVia(ctx, "mcp", "Decision", "dec-n-slug", "ratified", ""); !errors.Is(err, ErrConflict) {
				t.Errorf("err = %v; want the place conflict", err)
			}
		})
	}
}

// No relations table: the place cannot be read, and the ratify fails closed.
func TestRatify_PlaceQueryErrorFailsClosed(t *testing.T) {
	app, db, rs := setupTransitionItemApp(t)
	ctx := stewardContext(t, db, rs)
	insertDecision(t, db, "dec-e", "dec-e-slug", "proposed")
	_, err := app.TransitionItemVia(ctx, "mcp", "Decision", "dec-e-slug", "ratified", "")
	if !errors.Is(err, ErrInternal) {
		t.Errorf("err = %v; want internal", err)
	}
}

// Only the doors into ratified are gated.
func TestRatify_GateOnlyOnRatify(t *testing.T) {
	app, db, rs := setupTransitionItemApp(t)
	ctx := stewardContext(t, db, rs)
	if err := CreateRelationTables(db); err != nil {
		t.Fatal(err)
	}
	insertDecision(t, db, "dec-g", "dec-g-slug", "proposed")
	if _, err := app.TransitionItemVia(ctx, "mcp", "Decision", "dec-g-slug", "archived", ""); err != nil {
		t.Errorf("archive an unplaced proposed Decision: %v", err)
	}
	insertDecision(t, db, "dec-r", "dec-r-slug", "ratified")
	if _, err := app.TransitionItemVia(ctx, "mcp", "Decision", "dec-r-slug", "pending-re-evaluation", ""); err != nil {
		t.Errorf("re-evaluate an unplaced ratified Decision: %v", err)
	}
	if _, err := app.TransitionItemVia(ctx, "mcp", "Decision", "dec-r-slug", "superseded", ""); err != nil {
		t.Errorf("supersede an unplaced Decision: %v", err)
	}
}

// The HTTP update handler goes through the same gate.
func TestModule_updateHandler_decisionRatify_unplaced(t *testing.T) {
	m, store, d := decisionModuleAtStatus(t, "proposed")
	if _, err := m.db.ExecContext(context.Background(), `UPDATE smeldr_relations SET invalid_at = $1 WHERE source_id = $2`, time.Now().UTC().Add(-time.Hour), d.ID); err != nil {
		t.Fatal(err)
	}
	const uid = "tok-admin"
	govGrant(t, store, uid, "admin")
	govGrantDecisionSteward(t, store, uid)
	body, _ := json.Marshal(map[string]any{"Status": "ratified"})
	w := httptest.NewRecorder()
	r := withUser(httptest.NewRequest(http.MethodPut, "/decisions/"+d.Slug, bytes.NewReader(body)),
		User{ID: uid, Name: "Admin", Roles: []Role{Admin}})
	r.SetPathValue("slug", d.Slug)
	m.updateHandler(w, r)
	// HTTP answers a conflict with the generic public message (ERROR_HANDLING.md).
	if w.Code != http.StatusConflict {
		t.Fatalf("ratify unplaced: status = %d, want 409\nbody: %s", w.Code, w.Body.String())
	}
}

func TestDecisionPlace_ExactlyOne(t *testing.T) {
	ctx := context.Background()
	edge := func(src, srcType, tgtType, tgt, kind string) RelationEdge {
		return RelationEdge{SourceType: srcType, SourceID: src, TargetType: tgtType, TargetID: tgt,
			RelationKind: kind, EdgeClass: "asserted", Attributes: json.RawMessage("{}")}
	}
	_, db, rs := placesApp(t)
	if _, err := db.Exec(`INSERT INTO smeldr_domains (id, slug, created_at, updated_at, name, address) VALUES ('dom-a', 'dom-a', datetime('now'), datetime('now'), 'A', 'DOM3')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO smeldr_decisions (id, slug, status, created_at, updated_at, address) VALUES ('dec-1', 'dec-1', 'proposed', datetime('now'), datetime('now'), 'D9')`); err != nil {
		t.Fatal(err)
	}
	if err := rs.Assert(ctx, edge("dec-1", "Decision", "Domain", "dom-a", "belongs_to_domain")); err != nil {
		t.Fatalf("first place: %v", err)
	}
	if err := rs.Assert(ctx, edge("dec-1", "Decision", "Domain", "dom-a", "belongs_to_domain")); err != nil {
		t.Errorf("the same place again: %v", err)
	}
	for _, e := range []RelationEdge{
		edge("dec-1", "Decision", "Domain", "dom-b", "belongs_to_domain"),
		edge("dec-1", "Decision", "Area", "are-1", "belongs_to_area"),
		edge("dec-1", "Decision", "Root", "root-1", "belongs_to_root"),
	} {
		err := rs.Assert(ctx, e)
		if !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "Decision D9 already has a place (belongs_to_domain DOM3)") {
			t.Errorf("second place %s %s: %v; want a conflict naming D9 and DOM3", e.RelationKind, e.TargetID, err)
		}
	}
	// Other records keep any number of places.
	for _, tgt := range []string{"dom-a", "dom-b"} {
		if err := rs.Assert(ctx, edge("task-1", "Task", "Domain", tgt, "belongs_to_domain")); err != nil {
			t.Errorf("a Task in a second Domain: %v", err)
		}
	}
	// A proposed place edge is not a place, and is not limited.
	prop := edge("dec-1", "Decision", "Domain", "dom-c", "belongs_to_domain")
	prop.EdgeClass = "proposed"
	if _, err := rs.insertEdge(ctx, prop); err != nil {
		t.Errorf("a proposed place edge: %v", err)
	}
	// Once the first place ends, another may be given.
	if _, err := db.Exec(`UPDATE smeldr_relations SET invalid_at = datetime('now', '-1 hour') WHERE source_id = 'dec-1' AND edge_class = 'asserted'`); err != nil {
		t.Fatal(err)
	}
	if err := rs.Assert(ctx, edge("dec-1", "Decision", "Area", "are-1", "belongs_to_area")); err != nil {
		t.Errorf("a place after the first ended: %v", err)
	}
}

// A legacy place target has no compiled table: the refusal names its ID.
func TestDecisionPlace_LegacyTargetLabel(t *testing.T) {
	_, db, rs := placesApp(t)
	placeEdge(t, db, "dec-l", "domain", "legacy-dom", "belongs_to_domain", "asserted", nil)
	err := rs.Assert(context.Background(), RelationEdge{SourceType: "Decision", SourceID: "dec-l", TargetType: "Root", TargetID: "root-1",
		RelationKind: "belongs_to_root", EdgeClass: "asserted", Attributes: json.RawMessage("{}")})
	if !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "(belongs_to_domain legacy-dom)") {
		t.Errorf("err = %v; want the legacy target named by ID", err)
	}
}

// A place that cannot be read refuses the edge.
func TestDecisionPlace_QueryError(t *testing.T) {
	_, db, rs := placesApp(t)
	if _, err := db.Exec(`DROP TABLE smeldr_relations`); err != nil {
		t.Fatal(err)
	}
	err := rs.Assert(context.Background(), RelationEdge{SourceType: "Decision", SourceID: "dec-q", TargetType: "Root", TargetID: "root-1",
		RelationKind: "belongs_to_root", EdgeClass: "asserted", Attributes: json.RawMessage("{}")})
	if !errors.Is(err, ErrInternal) {
		t.Errorf("err = %v; want internal", err)
	}
}

// Unplaced proposed Decisions are listed by search, under the read rules.
func TestSearch_UnplacedProposedDecisions(t *testing.T) {
	f := newSearchFixture(t)
	q := SearchQuery{Types: []string{"Decision"}, States: []string{"proposed"}, Place: SearchPlaceNone}
	res, err := f.app.Search(f.global, q)
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 1 || !hasHit(res, f.loose.ID) || hasHit(res, f.placed.ID) {
		t.Errorf("hits %v; want only the unplaced proposed Decision", hitIDs(res))
	}
	if res, err := f.app.Search(f.reader, q); err != nil || res.Total != 0 {
		t.Errorf("reader: hits %v (%v); want the unplaced Decision hidden", hitIDs(res), err)
	}
}
