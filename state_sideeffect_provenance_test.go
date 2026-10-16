// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"database/sql"
	"testing"
)

// createConflictItemTable creates the typed table registerConflictFlow's
// "ConflictType" resolves to ("conflict_types"), optionally with the last_actor
// column, and registers the supersede flow against it. Must run before any call
// that would create the table without last_actor (CREATE IF NOT EXISTS).
func createConflictItemTable(t *testing.T, db *sql.DB, withLastActor bool, policy ConflictPolicy) {
	t.Helper()
	ddl := `CREATE TABLE conflict_types (id TEXT PRIMARY KEY, status TEXT NOT NULL, updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP`
	if withLastActor {
		ddl += `, last_actor TEXT NOT NULL DEFAULT ''`
	}
	ddl += `)`
	if _, err := db.ExecContext(context.Background(), ddl); err != nil {
		t.Fatalf("create conflict_types: %v", err)
	}
	registerConflictFlow(t, db, policy)
}

func conflictItemStatus(t *testing.T, db *sql.DB, id string) string {
	t.Helper()
	var s string
	if err := db.QueryRowContext(context.Background(),
		`SELECT status FROM conflict_types WHERE id = ?`, id).Scan(&s); err != nil {
		t.Fatalf("status of %s: %v", id, err)
	}
	return s
}

func conflictItemLastActor(t *testing.T, db *sql.DB, id string) string {
	t.Helper()
	var s string
	if err := db.QueryRowContext(context.Background(),
		`SELECT last_actor FROM conflict_types WHERE id = ?`, id).Scan(&s); err != nil {
		t.Fatalf("last_actor of %s: %v", id, err)
	}
	return s
}

// TestApplyConflictPolicy_Supersede_RecordsProvenance covers the side effect
// that used to leave no trace: every item superseded by a winning transition
// gets last_actor stamped and one record naming the triggering actor, the
// winning transition's surface and the winner.
func TestApplyConflictPolicy_Supersede_RecordsProvenance(t *testing.T) {
	tests := []struct {
		name          string
		ctx           context.Context
		surface       string
		wantActor     string
		wantKind      string
		withLastActor bool
	}{
		{"untagged via mcp", NewTestContext(User{ID: "u-human", Roles: []Role{Editor}}), "mcp", "u-human", "unclassified", true},
		{"agent via http", NewTestContext(User{ID: "u-agent", Roles: []Role{Editor, Agent}}), "http", "u-agent", "agent", true},
		{"plain context, unattributable", context.Background(), "", "", "", true},
		{"table without last_actor still recorded", NewTestContext(User{ID: "u-human", Roles: []Role{Editor}}), "mcp", "u-human", "unclassified", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := newMigratedDB(t)
			createConflictItemTable(t, db, tc.withLastActor, ConflictSupersede)
			insertConflictItem(t, db, "old-1", "published")
			insertConflictItem(t, db, "old-2", "published")
			insertConflictItem(t, db, "untouched", "draft")
			store := &fakeProvenanceStore{}

			if err := applyConflictPolicy(tc.ctx, db, nil, store, "ConflictType", "published", "new-1", tc.surface); err != nil {
				t.Fatalf("applyConflictPolicy: %v", err)
			}

			got := store.Appended()
			if len(got) != 2 {
				t.Fatalf("got %d records, want one per superseded item (2): %+v", len(got), got)
			}
			seen := map[string]bool{}
			for _, r := range got {
				seen[r.SubjectID] = true
				if r.SubjectType != "ConflictType" || r.Verb != "transition" ||
					r.FromState != "published" || r.ToState != "superseded" {
					t.Errorf("unexpected subject/verb/states: %+v", r)
				}
				if r.ActorID != tc.wantActor || r.ActorKind != tc.wantKind {
					t.Errorf("actor = %q (%q), want %q (%q)", r.ActorID, r.ActorKind, tc.wantActor, tc.wantKind)
				}
				if r.Surface != tc.surface {
					t.Errorf("surface = %q, want %q", r.Surface, tc.surface)
				}
				if r.Reason != "superseded by ConflictType new-1" {
					t.Errorf("reason = %q, want the winner named", r.Reason)
				}
			}
			if !seen["old-1"] || !seen["old-2"] {
				t.Errorf("records cover %v, want old-1 and old-2", seen)
			}
			for _, id := range []string{"old-1", "old-2"} {
				if s := conflictItemStatus(t, db, id); s != "superseded" {
					t.Errorf("%s status = %q, want superseded", id, s)
				}
				if tc.withLastActor {
					if la := conflictItemLastActor(t, db, id); la != tc.wantActor {
						t.Errorf("%s last_actor = %q, want %q", id, la, tc.wantActor)
					}
				}
			}
			if s := conflictItemStatus(t, db, "untouched"); s != "draft" {
				t.Errorf("an item not in the active state was touched: %q", s)
			}
		})
	}
}

// TestApplyConflictPolicy_Supersede_Dynamic proves the dynamic-content branch
// (smeldr_dynamic_content, keyed by type_name) records and stamps the same way.
func TestApplyConflictPolicy_Supersede_Dynamic(t *testing.T) {
	db := newMigratedDB(t)
	ctx := NewTestContext(User{ID: "u-dyn", Roles: []Role{Editor}})
	if _, err := db.ExecContext(context.Background(),
		`CREATE TABLE IF NOT EXISTS smeldr_dynamic_content (id TEXT PRIMARY KEY, type_name TEXT NOT NULL, status TEXT NOT NULL, published_at DATETIME, updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, slug TEXT, last_actor TEXT NOT NULL DEFAULT '')`,
	); err != nil {
		t.Fatalf("create smeldr_dynamic_content: %v", err)
	}
	app := &App{cfg: Config{DB: db}}
	if err := app.RegisterFlow(StateFlow{
		Name: "dyn-prov-flow", TypeName: "DynProvType", ActiveState: "published", ConflictPolicy: ConflictSupersede,
		States:      []State{{Name: "draft", IsInitial: true}, {Name: "published"}, {Name: "superseded"}},
		Transitions: []Transition{{From: "draft", To: "published"}, {From: "published", To: "superseded"}},
	}); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO smeldr_dynamic_content (id, type_name, status, slug) VALUES ('dyn-old', 'DynProvType', 'published', 'dyn-old')`,
	); err != nil {
		t.Fatalf("insert: %v", err)
	}
	store := &fakeProvenanceStore{}

	if err := applyConflictPolicy(ctx, db, nil, store, "DynProvType", "published", "dyn-new", "http"); err != nil {
		t.Fatalf("applyConflictPolicy: %v", err)
	}

	got := store.Appended()
	if len(got) != 1 {
		t.Fatalf("got %d records, want 1: %+v", len(got), got)
	}
	r := got[0]
	if r.SubjectType != "DynProvType" || r.SubjectID != "dyn-old" || r.ToState != "superseded" ||
		r.ActorID != "u-dyn" || r.Surface != "http" || r.Reason != "superseded by DynProvType dyn-new" {
		t.Errorf("unexpected record: %+v", r)
	}
	var lastActor string
	if err := db.QueryRowContext(context.Background(),
		`SELECT last_actor FROM smeldr_dynamic_content WHERE id = 'dyn-old'`).Scan(&lastActor); err != nil {
		t.Fatalf("read last_actor: %v", err)
	}
	if lastActor != "u-dyn" {
		t.Errorf("last_actor = %q, want u-dyn", lastActor)
	}
}

// TestApplyConflictPolicy_NoSupersede_RecordsNothing pins every path that must
// not write a record: no store, reject policy, a transition into another
// state, no conflicting item, and a supersede UPDATE that fails.
func TestApplyConflictPolicy_NoSupersede_RecordsNothing(t *testing.T) {
	t.Run("reject policy conflict", func(t *testing.T) {
		db := newMigratedDB(t)
		createConflictItemTable(t, db, true, ConflictReject)
		insertConflictItem(t, db, "old-1", "published")
		store := &fakeProvenanceStore{}
		if err := applyConflictPolicy(context.Background(), db, nil, store, "ConflictType", "published", "new", "mcp"); err == nil {
			t.Fatal("reject policy: want ErrConflict, got nil")
		}
		if got := store.Appended(); len(got) != 0 {
			t.Errorf("records = %+v, want none", got)
		}
	})
	t.Run("transition into another state", func(t *testing.T) {
		db := newMigratedDB(t)
		createConflictItemTable(t, db, true, ConflictSupersede)
		insertConflictItem(t, db, "old-1", "published")
		store := &fakeProvenanceStore{}
		if err := applyConflictPolicy(context.Background(), db, nil, store, "ConflictType", "archived", "new", "mcp"); err != nil {
			t.Fatalf("applyConflictPolicy: %v", err)
		}
		if got := store.Appended(); len(got) != 0 {
			t.Errorf("records = %+v, want none", got)
		}
	})
	t.Run("no conflicting item", func(t *testing.T) {
		db := newMigratedDB(t)
		createConflictItemTable(t, db, true, ConflictSupersede)
		store := &fakeProvenanceStore{}
		if err := applyConflictPolicy(context.Background(), db, nil, store, "ConflictType", "published", "new", "mcp"); err != nil {
			t.Fatalf("applyConflictPolicy: %v", err)
		}
		if got := store.Appended(); len(got) != 0 {
			t.Errorf("records = %+v, want none", got)
		}
	})
	t.Run("supersede UPDATE fails", func(t *testing.T) {
		db := newMigratedDB(t)
		createConflictItemTable(t, db, true, ConflictSupersede)
		insertConflictItem(t, db, "old-1", "published")
		store := &fakeProvenanceStore{}
		// The first ExecContext call is the supersede UPDATE (a real
		// failure, not a missing column), so no fallback and no record.
		failing := &nthExecFailDB{DB: db, fail: 1}
		if err := applyConflictPolicy(context.Background(), failing, nil, store, "ConflictType", "published", "new", "mcp"); err != nil {
			t.Fatalf("applyConflictPolicy: want fail-open nil, got %v", err)
		}
		if got := store.Appended(); len(got) != 0 {
			t.Errorf("records = %+v, want none for an item whose UPDATE failed", got)
		}
		if s := conflictItemStatus(t, db, "old-1"); s != "published" {
			t.Errorf("status = %q, want unchanged", s)
		}
	})
	t.Run("nil store", func(t *testing.T) {
		db := newMigratedDB(t)
		createConflictItemTable(t, db, true, ConflictSupersede)
		insertConflictItem(t, db, "old-1", "published")
		if err := applyConflictPolicy(context.Background(), db, nil, nil, "ConflictType", "published", "new", "mcp"); err != nil {
			t.Fatalf("applyConflictPolicy: %v", err)
		}
		if s := conflictItemStatus(t, db, "old-1"); s != "superseded" {
			t.Errorf("status = %q, want superseded without a store", s)
		}
	})
}

// TestApplyConflictPolicy_Supersede_AppendFailureFailsOpen proves a provenance
// write failure neither blocks the supersede nor fails the winning transition.
func TestApplyConflictPolicy_Supersede_AppendFailureFailsOpen(t *testing.T) {
	db := newMigratedDB(t)
	createConflictItemTable(t, db, true, ConflictSupersede)
	insertConflictItem(t, db, "old-1", "published")
	store := &appendFailStore{}
	if err := applyConflictPolicy(context.Background(), db, nil, store, "ConflictType", "published", "new", "mcp"); err != nil {
		t.Fatalf("applyConflictPolicy: want nil despite Append failure, got %v", err)
	}
	if store.calls != 1 {
		t.Errorf("Append calls = %d, want 1", store.calls)
	}
	if s := conflictItemStatus(t, db, "old-1"); s != "superseded" {
		t.Errorf("status = %q, want superseded", s)
	}
}

// TestRepoSetStatus_Supersede_RecordsLoserAndWinnerOnce is the end-to-end
// shape: publishing a second item under a supersede flow, through the dynamic
// repo a real HTTP or MCP request uses, yields the winner's own transition
// record exactly once and the loser's superseded record with the surface.
func TestRepoSetStatus_Supersede_RecordsLoserAndWinnerOnce(t *testing.T) {
	app, db, _, store := setupProvenanceTransitionApp(t)
	typeName, slugA := defineProvenanceDynamicType(t, app, db, "supprov")
	repo, err := app.DynamicContentRepo(typeName)
	if err != nil {
		t.Fatalf("DynamicContentRepo: %v", err)
	}
	b, err := repo.WithProvenance(nil).CreateDraft(context.Background(), map[string]any{"Title": "second"})
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	a, err := repo.GetBySlug(context.Background(), slugA)
	if err != nil {
		t.Fatalf("GetBySlug: %v", err)
	}
	if err := app.RegisterFlow(StateFlow{
		Name: "supprov-flow", TypeName: typeName, ActiveState: "published", ConflictPolicy: ConflictSupersede,
		States: []State{{Name: "draft", IsInitial: true}, {Name: "published"}, {Name: "superseded"}},
		Transitions: []Transition{
			{From: "draft", To: "published"}, {From: "published", To: "superseded"},
		},
	}); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
	ctx := NewTestContext(User{ID: "u-pub", Roles: []Role{Editor}})

	if err := repo.setStatusVia(ctx, a.ID, Published, "", "mcp"); err != nil {
		t.Fatalf("publish A: %v", err)
	}
	if err := repo.setStatusVia(ctx, b.ID, Published, "", "http"); err != nil {
		t.Fatalf("publish B: %v", err)
	}

	var winnerB, loserA []ProvenanceRecord
	for _, r := range store.Appended() {
		switch {
		case r.SubjectID == b.ID:
			winnerB = append(winnerB, r)
		case r.SubjectID == a.ID && r.ToState == "superseded":
			loserA = append(loserA, r)
		}
	}
	if len(winnerB) != 1 || winnerB[0].ToState != "published" || winnerB[0].Surface != "http" {
		t.Errorf("winner records = %+v, want exactly one draft->published via http", winnerB)
	}
	if len(loserA) != 1 {
		t.Fatalf("loser superseded records = %+v, want exactly one", loserA)
	}
	if l := loserA[0]; l.FromState != "published" || l.Surface != "http" || l.ActorID != "u-pub" ||
		l.Reason != "superseded by "+typeName+" "+b.ID {
		t.Errorf("loser record = %+v", l)
	}
}

// TestRecordStateChange_VerbAndNilStore pins the shared writer's two small
// rules: an empty verb means provenanceVerbFor's update/transition choice, an
// explicit verb wins, and a nil store is a no-op.
func TestRecordStateChange_VerbAndNilStore(t *testing.T) {
	recordStateChange(context.Background(), nil, stateChange{typeName: "X", id: "1", from: "a", to: "b"})

	store := &fakeProvenanceStore{}
	recordStateChange(context.Background(), store, stateChange{typeName: "X", id: "1", from: "a", to: "b"})
	recordStateChange(context.Background(), store, stateChange{typeName: "X", id: "1", from: "a", to: "a"})
	recordStateChange(context.Background(), store, stateChange{typeName: "X", id: "1", from: "", to: "draft", verb: "create"})
	got := store.Appended()
	if len(got) != 3 {
		t.Fatalf("got %d records, want 3", len(got))
	}
	if got[0].Verb != "transition" || got[1].Verb != "update" || got[2].Verb != "create" {
		t.Errorf("verbs = %q/%q/%q, want transition/update/create", got[0].Verb, got[1].Verb, got[2].Verb)
	}
}
