// AGPL-3.0-or-later

package smeldr

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

// edgeTestApp is an App with orchestration tables, a RelationStore wired
// through App.Relations, and the built-in Signal flow replaced by a
// ConflictSupersede flow over the given active state, so the generic transition
// paths exercise the supersede side effect on a real compiled type.
func edgeTestApp(t *testing.T, activeState string, states []State, transitions []Transition) (*App, *sql.DB, *RelationStore) {
	t.Helper()
	app, db, _ := setupTransitionItemApp(t)
	if err := CreateRelationTables(db); err != nil {
		t.Fatalf("CreateRelationTables: %v", err)
	}
	store, err := NewRelationStore(db)
	if err != nil {
		t.Fatalf("NewRelationStore: %v", err)
	}
	app.Relations(store)
	if err := app.RegisterFlow(StateFlow{
		Name: "signal-protocol", TypeName: "Signal",
		ActiveState: activeState, ConflictPolicy: ConflictSupersede,
		States: states, Transitions: transitions,
	}); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
	return app, db, store
}

var signalSupersedeStates = []State{
	{Name: "pending", IsInitial: true}, {Name: "read"}, {Name: "superseded"},
}

var signalSupersedeTransitions = []Transition{
	{From: "pending", To: "read"}, {From: "read", To: "superseded"},
}

func edgesFrom(t *testing.T, store *RelationStore, typeName, id string) []RelationEdge {
	t.Helper()
	edges, err := store.GetBySource(context.Background(), typeName, id, "supersedes")
	if err != nil {
		t.Fatalf("GetBySource: %v", err)
	}
	return edges
}

// TestTransitionItemVia_Supersede_AssertsEdge is the point of the change: a
// winning transition under ConflictSupersede now leaves a supersedes edge from
// the winner to each loser, by raw ID, with the triggering actor as created_by,
// and TraceLineage from the loser finds the replacement.
func TestTransitionItemVia_Supersede_AssertsEdge(t *testing.T) {
	app, db, store := edgeTestApp(t, "read", signalSupersedeStates, signalSupersedeTransitions)
	upsertTestKind(t, store, "supersedes", "Signal", "Signal")
	insertSignal(t, db, "loser-1", "loser-1-slug", "read")
	insertSignal(t, db, "loser-2", "loser-2-slug", "read")
	insertSignal(t, db, "winner", "winner-slug", "pending")
	ctx := NewTestContext(User{ID: "u-trigger", Roles: []Role{Editor}})

	if _, err := app.TransitionItemVia(ctx, "mcp", "Signal", "winner-slug", "read", ""); err != nil {
		t.Fatalf("TransitionItemVia: %v", err)
	}

	edges := edgesFrom(t, store, "Signal", "winner")
	if len(edges) != 2 {
		t.Fatalf("got %d edges from the winner, want 2: %+v", len(edges), edges)
	}
	targets := map[string]bool{}
	for _, e := range edges {
		targets[e.TargetID] = true
		if e.SourceID != "winner" || e.TargetType != "Signal" || e.EdgeClass != "asserted" {
			t.Errorf("edge = %+v, want winner -> Signal asserted (raw IDs)", e)
		}
		if e.CreatedBy == nil || *e.CreatedBy != "u-trigger" {
			t.Errorf("created_by = %v, want the triggering actor u-trigger", e.CreatedBy)
		}
	}
	if !targets["loser-1"] || !targets["loser-2"] {
		t.Errorf("edge targets = %v, want both losers by ID", targets)
	}

	trace, err := store.TraceLineage(context.Background(), "Signal", "loser-1", 3)
	if err != nil {
		t.Fatalf("TraceLineage: %v", err)
	}
	found := false
	for _, n := range trace.Nodes {
		if n.ID == "winner" && n.RelationKind == "supersedes" {
			found = true
		}
	}
	if !found {
		t.Errorf("TraceLineage from the superseded item does not reach its replacement: %+v", trace.Nodes)
	}
}

// TestTransitionItemVia_Supersede_PlainContext_EdgeWithoutCreatedBy covers a
// system-initiated transition: the edge is still written, with no creator.
func TestTransitionItemVia_Supersede_PlainContext_EdgeWithoutCreatedBy(t *testing.T) {
	app, db, store := edgeTestApp(t, "read", signalSupersedeStates, signalSupersedeTransitions)
	upsertTestKind(t, store, "supersedes", "Signal", "Signal")
	insertSignal(t, db, "loser", "loser-slug", "read")
	insertSignal(t, db, "winner", "winner-slug", "pending")

	if _, err := app.TransitionItemVia(context.Background(), "", "Signal", "winner-slug", "read", ""); err != nil {
		t.Fatalf("TransitionItemVia: %v", err)
	}
	edges := edgesFrom(t, store, "Signal", "winner")
	if len(edges) != 1 || edges[0].CreatedBy != nil {
		t.Errorf("edges = %+v, want one edge with nil created_by", edges)
	}
}

// TestTransitionItemVia_Supersede_SecondSupersedeNoDuplicate pins the existing
// dedup key: the same winner/loser pair asserted again leaves one row.
func TestTransitionItemVia_Supersede_SameEdgeNotDuplicated(t *testing.T) {
	_, db, store := edgeTestApp(t, "read", signalSupersedeStates, signalSupersedeTransitions)
	upsertTestKind(t, store, "supersedes", "Signal", "Signal")
	insertSignal(t, db, "loser", "loser-slug", "read")
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if _, err := db.ExecContext(ctx, `UPDATE smeldr_signals SET status = 'read' WHERE id = 'loser'`); err != nil {
			t.Fatalf("reset loser: %v", err)
		}
		if err := conflictSupersede(ctx, db, store, nil, "Signal", "read", "winner", "", "smeldr_signals", false); err != nil {
			t.Fatalf("conflictSupersede: %v", err)
		}
	}
	if edges := edgesFrom(t, store, "Signal", "winner"); len(edges) != 1 {
		t.Errorf("got %d edges, want 1 (dedup)", len(edges))
	}
}

// TestDynamicSetStatus_Supersede_AssertsEdge covers the runtime-defined type
// path (DynamicTypeRepo.setStatusVia), wired from App.Relations.
func TestDynamicSetStatus_Supersede_AssertsEdge(t *testing.T) {
	app, db, _, _ := setupProvenanceTransitionApp(t)
	if err := CreateRelationTables(db); err != nil {
		t.Fatalf("CreateRelationTables: %v", err)
	}
	store, err := NewRelationStore(db)
	if err != nil {
		t.Fatalf("NewRelationStore: %v", err)
	}
	app.Relations(store)
	typeName, slugA := defineProvenanceDynamicType(t, app, db, "edgedyn")
	upsertTestKind(t, store, "supersedes", typeName, typeName)
	repo, err := app.DynamicContentRepo(typeName)
	if err != nil {
		t.Fatalf("DynamicContentRepo: %v", err)
	}
	b, err := repo.CreateDraft(context.Background(), map[string]any{"Title": "second"})
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	a, err := repo.GetBySlug(context.Background(), slugA)
	if err != nil {
		t.Fatalf("GetBySlug: %v", err)
	}
	if err := app.RegisterFlow(StateFlow{
		Name: "edgedyn-flow", TypeName: typeName, ActiveState: "published", ConflictPolicy: ConflictSupersede,
		States:      []State{{Name: "draft", IsInitial: true}, {Name: "published"}, {Name: "superseded"}},
		Transitions: []Transition{{From: "draft", To: "published"}, {From: "published", To: "superseded"}},
	}); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
	ctx := NewTestContext(User{ID: "u-dyn", Roles: []Role{Editor}})
	if err := repo.setStatusVia(ctx, a.ID, Published, "", "mcp"); err != nil {
		t.Fatalf("publish A: %v", err)
	}
	if err := repo.setStatusVia(ctx, b.ID, Published, "", "mcp"); err != nil {
		t.Fatalf("publish B: %v", err)
	}
	edges := edgesFrom(t, store, typeName, b.ID)
	if len(edges) != 1 || edges[0].TargetID != a.ID || edges[0].CreatedBy == nil || *edges[0].CreatedBy != "u-dyn" {
		t.Errorf("edges = %+v, want one B -> A edge created by u-dyn", edges)
	}
}

// TestModuleMCPPublish_Supersede_AssertsEdge covers the Module path
// (MCPPublish), wired through setRelationStore.
func TestModuleMCPPublish_Supersede_AssertsEdge(t *testing.T) {
	app, db, store := edgeTestApp(t, "published",
		[]State{{Name: "pending", IsInitial: true}, {Name: "published"}, {Name: "superseded"}},
		[]Transition{{From: "pending", To: "published"}, {From: "published", To: "superseded"}})
	_ = app
	upsertTestKind(t, store, "supersedes", "Signal", "Signal")
	insertSignal(t, db, "old", "old-slug", "published")
	insertSignal(t, db, "new", "new-slug", "pending")
	m := NewModule[*Signal]((*Signal)(nil),
		At("/signals"), Repo(NewSQLRepo[*Signal](db, Table("smeldr_signals"))), MCP(MCPRead, MCPWrite))
	m.setDB(db)
	m.setRelationStore(store)

	if err := m.MCPPublish(NewTestContext(User{ID: "u-mod", Roles: []Role{Editor}}), "new-slug", ""); err != nil {
		t.Fatalf("MCPPublish: %v", err)
	}
	edges := edgesFrom(t, store, "Signal", "new")
	if len(edges) != 1 || edges[0].TargetID != "old" || edges[0].CreatedBy == nil || *edges[0].CreatedBy != "u-mod" {
		t.Errorf("edges = %+v, want one new -> old edge created by u-mod", edges)
	}
}

// capturedLog runs fn with slog's default logger writing to a buffer.
func capturedLog(fn func()) string {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(prev)
	fn()
	return buf.String()
}

// TestSupersede_EdgeSkipped covers the two expected configurations in which no
// edge is written: the kind is unregistered, and the kind is the orchestration
// one (Decision -> Decision only) while the type is not Decision. The item is
// still superseded and recorded, and the operator gets one Info line per type,
// no Warn, however many items are superseded or how often it happens.
func TestSupersede_EdgeSkipped(t *testing.T) {
	tests := []struct {
		name     string
		register func(t *testing.T, store *RelationStore)
	}{
		{"kind unregistered", func(t *testing.T, store *RelationStore) {}},
		{"orchestration kinds, type not permitted", func(t *testing.T, store *RelationStore) {
			if err := RegisterOrchestrationRelationKinds(context.Background(), store); err != nil {
				t.Fatalf("RegisterOrchestrationRelationKinds: %v", err)
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			supersedeEdgeSkipLogged.Delete("Signal")
			defer supersedeEdgeSkipLogged.Delete("Signal")
			app, db, store := edgeTestApp(t, "read", signalSupersedeStates, signalSupersedeTransitions)
			tc.register(t, store)
			prov := &fakeProvenanceStore{}
			app.Provenance(prov)
			insertSignal(t, db, "l1", "l1-slug", "read")
			insertSignal(t, db, "l2", "l2-slug", "read")
			insertSignal(t, db, "w1", "w1-slug", "pending")
			insertSignal(t, db, "w2", "w2-slug", "pending")

			out := capturedLog(func() {
				if _, err := app.TransitionItemVia(context.Background(), "mcp", "Signal", "w1-slug", "read", ""); err != nil {
					t.Fatalf("first transition: %v", err)
				}
				if _, err := app.TransitionItemVia(context.Background(), "mcp", "Signal", "w2-slug", "read", ""); err != nil {
					t.Fatalf("second transition: %v", err)
				}
			})

			if n := strings.Count(out, "no supersedes relation is written"); n != 1 {
				t.Errorf("Info skip line logged %d times, want exactly once per type\n%s", n, out)
			}
			if strings.Contains(out, "level=WARN") {
				t.Errorf("an expected configuration logged a Warn:\n%s", out)
			}
			var cnt int
			if err := db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM smeldr_relations`).Scan(&cnt); err != nil {
				t.Fatalf("count relations: %v", err)
			}
			if cnt != 0 {
				t.Errorf("%d relation rows written, want none", cnt)
			}
			for _, id := range []string{"l1", "l2"} {
				var s string
				if err := db.QueryRowContext(context.Background(), `SELECT status FROM smeldr_signals WHERE id = ?`, id).Scan(&s); err != nil {
					t.Fatalf("status of %s: %v", id, err)
				}
				if s != "superseded" && s != "read" {
					t.Errorf("%s status = %q", id, s)
				}
			}
			superseded := 0
			for _, r := range prov.Appended() {
				if r.ToState == "superseded" {
					superseded++
				}
			}
			if superseded == 0 {
				t.Error("no superseded record written: the supersede itself must proceed without the edge")
			}
		})
	}
}

// TestSupersede_NilRelationStore_NoEdgeNoLog pins the unchanged default: with
// App.Relations never called nothing is attempted and nothing is logged.
func TestSupersede_NilRelationStore_NoEdgeNoLog(t *testing.T) {
	supersedeEdgeSkipLogged.Delete("Signal")
	app, db, _ := setupTransitionItemApp(t)
	if err := app.RegisterFlow(StateFlow{
		Name: "signal-protocol", TypeName: "Signal", ActiveState: "read", ConflictPolicy: ConflictSupersede,
		States: signalSupersedeStates, Transitions: signalSupersedeTransitions,
	}); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
	insertSignal(t, db, "l1", "l1-slug", "read")
	insertSignal(t, db, "w1", "w1-slug", "pending")
	out := capturedLog(func() {
		if _, err := app.TransitionItemVia(context.Background(), "", "Signal", "w1-slug", "read", ""); err != nil {
			t.Fatalf("TransitionItemVia: %v", err)
		}
	})
	if strings.Contains(out, "no supersedes relation") {
		t.Errorf("nil relation store logged a skip line:\n%s", out)
	}
}

// failingInsertDB makes the relation INSERT fail, to prove an Assert error for
// any other reason still logs a Warn and never fails the transition.
type failingInsertDB struct{ DB }

func (d failingInsertDB) ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error) {
	if strings.Contains(q, "INSERT INTO smeldr_relations") {
		return nil, sql.ErrConnDone
	}
	return d.DB.ExecContext(ctx, q, args...)
}

func TestSupersede_AssertFailsOtherwise_WarnsAndStillSupersedes(t *testing.T) {
	_, db, _ := edgeTestApp(t, "read", signalSupersedeStates, signalSupersedeTransitions)
	store, err := NewRelationStore(failingInsertDB{db})
	if err != nil {
		t.Fatalf("NewRelationStore: %v", err)
	}
	upsertKindPermissive(t, store)
	insertSignal(t, db, "l1", "l1-slug", "read")
	out := capturedLog(func() {
		if err := conflictSupersede(context.Background(), db, store, nil, "Signal", "read", "w", "", "smeldr_signals", false); err != nil {
			t.Fatalf("conflictSupersede: %v", err)
		}
	})
	if !strings.Contains(out, "level=WARN") || !strings.Contains(out, "supersedes relation failed") {
		t.Errorf("an unexpected Assert failure must still log a Warn:\n%s", out)
	}
	var s string
	if err := db.QueryRowContext(context.Background(), `SELECT status FROM smeldr_signals WHERE id = 'l1'`).Scan(&s); err != nil || s != "superseded" {
		t.Errorf("status = %q (%v), want superseded", s, err)
	}
}

// upsertKindPermissive registers an unconstrained supersedes kind.
func upsertKindPermissive(t *testing.T, store *RelationStore) {
	t.Helper()
	if err := store.UpsertKind(context.Background(), RelationKindDef{
		TypeName: "supersedes", Mode: "asserted", Directional: true, TypePairs: json.RawMessage(`[]`),
	}); err != nil {
		t.Fatalf("UpsertKind: %v", err)
	}
}

// TestSupersedeEdgeAllowed pins the decision table directly, including the
// unconstrained kind (empty TypePairs permits every pair).
func TestSupersedeEdgeAllowed(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateRelationTables(db); err != nil {
		t.Fatalf("CreateRelationTables: %v", err)
	}
	store, err := NewRelationStore(db)
	if err != nil {
		t.Fatalf("NewRelationStore: %v", err)
	}
	ctx := context.Background()
	for _, ty := range []string{"AllowNil", "AllowNoWinner", "AllowNoKind", "AllowPair", "AllowOther", "AllowAny"} {
		supersedeEdgeSkipLogged.Delete(ty)
	}
	if supersedeEdgeAllowed(ctx, nil, "AllowNil", "w") {
		t.Error("nil store: want false")
	}
	if supersedeEdgeAllowed(ctx, store, "AllowNoWinner", "") {
		t.Error("no winner ID: want false")
	}
	if supersedeEdgeAllowed(ctx, store, "AllowNoKind", "w") {
		t.Error("kind unregistered: want false")
	}
	upsertTestKind(t, store, "supersedes", "AllowPair", "AllowPair")
	if !supersedeEdgeAllowed(ctx, store, "AllowPair", "w") {
		t.Error("kind permitting the pair: want true")
	}
	if supersedeEdgeAllowed(ctx, store, "AllowOther", "w") {
		t.Error("kind not permitting the pair: want false")
	}
	upsertKindPermissive(t, store)
	if !supersedeEdgeAllowed(ctx, store, "AllowAny", "w") {
		t.Error("unconstrained kind: want true")
	}
}
