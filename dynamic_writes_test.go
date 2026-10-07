package smeldr_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	smeldr "smeldr.dev/core"
)

// newDynWritesApp is an App with provenance wired and the recipe type defined.
func newDynWritesApp(t *testing.T) (*smeldr.App, smeldr.ProvenanceStore, *smeldr.DynamicTypeRepo) {
	t.Helper()
	db := openDynDB(t)
	app := smeldr.New(smeldr.MustConfig(smeldr.Config{BaseURL: "https://example.com", Secret: []byte(dynTestSecret), DB: db}))
	if err := smeldr.CreateProvenanceTable(db); err != nil {
		t.Fatalf("CreateProvenanceTable: %v", err)
	}
	store := smeldr.NewProvenanceStore(db)
	app.Provenance(store)
	if _, err := app.DefineContentType(t.Context(), recipeSchema()); err != nil {
		t.Fatalf("DefineContentType: %v", err)
	}
	repo, err := app.DynamicContentRepo("recipe")
	if err != nil {
		t.Fatalf("DynamicContentRepo: %v", err)
	}
	return app, store, repo
}

func recordsOf(t *testing.T, store smeldr.ProvenanceStore, id, verb string) []smeldr.ProvenanceRecord {
	t.Helper()
	all, err := store.List(context.Background(), smeldr.ProvenanceFilter{SubjectType: "recipe", SubjectID: id})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var out []smeldr.ProvenanceRecord
	for _, r := range all {
		if r.Verb == verb {
			out = append(out, r)
		}
	}
	return out
}

func lastActorOf(t *testing.T, repo *smeldr.DynamicTypeRepo, id string) string {
	t.Helper()
	n, err := repo.GetByID(context.Background(), id)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	return n.LastActor
}

// TestDynamicWrites_RecordActor: every door that creates or updates a dynamic
// item records one create or update entry with the caller, its kind and the
// surface, and sets last_actor to the caller.
func TestDynamicWrites_RecordActor(t *testing.T) {
	agent := smeldr.NewTestContext(smeldr.User{ID: "agent-7", Roles: []smeldr.Role{smeldr.Editor, smeldr.Agent}})

	t.Run("mcp create and update", func(t *testing.T) {
		_, store, repo := newDynWritesApp(t)
		node, err := repo.CreateDraftVia(agent, "mcp", map[string]any{"Title": "Soup"})
		if err != nil {
			t.Fatalf("CreateDraftVia: %v", err)
		}
		if err := repo.UpdateFieldsVia(agent, "mcp", node.ID, map[string]any{"Body": "Boil"}); err != nil {
			t.Fatalf("UpdateFieldsVia: %v", err)
		}
		for _, verb := range []string{"create", "update"} {
			recs := recordsOf(t, store, node.ID, verb)
			if len(recs) != 1 || recs[0].ActorID != "agent-7" || recs[0].ActorKind != "agent" || recs[0].Surface != "mcp" {
				t.Errorf("%s records = %+v; want one by agent-7 (agent) via mcp", verb, recs)
			}
		}
		up := recordsOf(t, store, node.ID, "update")
		if len(up) == 1 && (up[0].FromState != "draft" || up[0].ToState != "draft") {
			t.Errorf("update record states = %s -> %s; want draft -> draft", up[0].FromState, up[0].ToState)
		}
		if got := lastActorOf(t, repo, node.ID); got != "agent-7" {
			t.Errorf("last_actor = %q; want agent-7", got)
		}
	})

	t.Run("REST create and update", func(t *testing.T) {
		app, store, repo := newDynWritesApp(t)
		handler := dynHandler(t, app)
		w := dynPost(handler, "/_content/recipe", map[string]any{"Title": "Stew"}, bearerToken(t, smeldr.Editor))
		if w.Code != http.StatusCreated {
			t.Fatalf("create = %d: %s", w.Code, w.Body.String())
		}
		items, _ := repo.List(context.Background(), smeldr.ListOptions{})
		if len(items) != 1 {
			t.Fatalf("items = %d", len(items))
		}
		id := items[0]["ID"].(string)
		w = dynPatch(handler, fmt.Sprintf("/_content/recipe/%s", id), map[string]any{"Body": "Simmer"}, bearerToken(t, smeldr.Editor))
		if w.Code != http.StatusOK {
			t.Fatalf("update = %d: %s", w.Code, w.Body.String())
		}
		for _, verb := range []string{"create", "update"} {
			recs := recordsOf(t, store, id, verb)
			if len(recs) != 1 || recs[0].ActorID != "u1" || recs[0].Surface != "http" {
				t.Errorf("%s records = %+v; want one by u1 via http", verb, recs)
			}
		}
		if got := lastActorOf(t, repo, id); got != "u1" {
			t.Errorf("last_actor = %q; want u1", got)
		}
	})

	t.Run("an update by someone else moves last_actor", func(t *testing.T) {
		_, store, repo := newDynWritesApp(t)
		node, err := repo.CreateDraftVia(agent, "mcp", map[string]any{"Title": "Cake"})
		if err != nil {
			t.Fatal(err)
		}
		bob := smeldr.NewTestContext(smeldr.User{ID: "bob", Roles: []smeldr.Role{smeldr.Editor}})
		if err := repo.UpdateFieldsVia(bob, "mcp", node.ID, map[string]any{"Body": "Bake"}); err != nil {
			t.Fatal(err)
		}
		if got := lastActorOf(t, repo, node.ID); got != "bob" {
			t.Errorf("last_actor after bob's edit = %q; want bob", got)
		}
		if recs := recordsOf(t, store, node.ID, "update"); len(recs) != 1 || recs[0].ActorID != "bob" {
			t.Errorf("update records = %+v; want one by bob", recs)
		}
	})

	t.Run("the old forms record with no surface", func(t *testing.T) {
		_, store, repo := newDynWritesApp(t)
		node, err := repo.CreateDraft(agent, map[string]any{"Title": "Toast"})
		if err != nil {
			t.Fatal(err)
		}
		if err := repo.UpdateFields(agent, node.ID, map[string]any{"Body": "x"}); err != nil {
			t.Fatal(err)
		}
		if recs := recordsOf(t, store, node.ID, "update"); len(recs) != 1 || recs[0].Surface != "" || recs[0].ActorID != "agent-7" {
			t.Errorf("update records = %+v", recs)
		}
	})
}

// TestDynamicWrites_RefusedWriteRecordsNothing: a refused write leaves no
// record and does not move last_actor.
func TestDynamicWrites_RefusedWriteRecordsNothing(t *testing.T) {
	app, store, repo := newDynWritesApp(t)
	alice := smeldr.NewTestContext(smeldr.User{ID: "alice", Roles: []smeldr.Role{smeldr.Editor}})
	bob := smeldr.NewTestContext(smeldr.User{ID: "bob", Roles: []smeldr.Role{smeldr.Editor}})
	node, err := repo.CreateDraftVia(alice, "mcp", map[string]any{"Title": "Pie"})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := repo.CreateDraftVia(bob, "mcp", map[string]any{"Body": "no title"}); err == nil {
		t.Error("a create without the required Title was accepted")
	}
	if err := repo.UpdateFieldsVia(bob, "mcp", node.ID, map[string]any{"Nope": "x"}); err == nil {
		t.Error("an update with an unknown field was accepted")
	}
	if err := repo.UpdateFieldsVia(bob, "mcp", "no-such-id", map[string]any{"Body": "x"}); !errors.Is(err, smeldr.ErrNotFound) {
		t.Errorf("update of an unknown id = %v; want ErrNotFound", err)
	}
	if err := app.RegisterFlow(smeldr.StateFlow{
		Name: "recipe-locked", TypeName: "recipe",
		States:      []smeldr.State{{Name: "draft", IsInitial: true, Locked: true}, {Name: "published"}},
		Transitions: []smeldr.Transition{{From: "draft", To: "published"}},
	}); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
	if err := repo.UpdateFieldsVia(bob, "mcp", node.ID, map[string]any{"Body": "x"}); !errors.Is(err, smeldr.ErrConflict) {
		t.Errorf("update in a locked state = %v; want ErrConflict", err)
	}

	if recs := recordsOf(t, store, node.ID, "update"); len(recs) != 0 {
		t.Errorf("refused updates recorded %+v", recs)
	}
	all, _ := store.List(context.Background(), smeldr.ProvenanceFilter{SubjectType: "recipe"})
	if len(all) != 1 {
		t.Errorf("records = %+v; want only alice's create", all)
	}
	if got := lastActorOf(t, repo, node.ID); got != "alice" {
		t.Errorf("last_actor = %q; want alice unchanged", got)
	}
}

// failingProv refuses every Append.
type failingProv struct{}

func (failingProv) Append(context.Context, smeldr.ProvenanceRecord) error {
	return errors.New("provenance down")
}
func (failingProv) List(context.Context, smeldr.ProvenanceFilter) ([]smeldr.ProvenanceRecord, error) {
	return nil, nil
}

// TestDynamicWrites_ProvenanceFailOpen: a provenance store that refuses every
// record does not fail the write.
func TestDynamicWrites_ProvenanceFailOpen(t *testing.T) {
	_, _, repo := newDynWritesApp(t)
	r := repo.WithProvenance(failingProv{})
	ctx := smeldr.NewTestContext(smeldr.User{ID: "alice", Roles: []smeldr.Role{smeldr.Editor}})
	node, err := r.CreateDraftVia(ctx, "mcp", map[string]any{"Title": "Bread"})
	if err != nil {
		t.Fatalf("create with provenance down: %v", err)
	}
	if err := r.UpdateFieldsVia(ctx, "mcp", node.ID, map[string]any{"Body": "Knead"}); err != nil {
		t.Fatalf("update with provenance down: %v", err)
	}
	if got := lastActorOf(t, repo, node.ID); got != "alice" {
		t.Errorf("last_actor = %q; want alice", got)
	}
}

// TestDynamicWrites_SystemCallerNoActor: a plain context records the write
// with no actor, as transitions do.
func TestDynamicWrites_SystemCallerNoActor(t *testing.T) {
	_, store, repo := newDynWritesApp(t)
	node, err := repo.CreateDraftVia(context.Background(), "", map[string]any{"Title": "Jam"})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateFieldsVia(context.Background(), "", node.ID, map[string]any{"Body": "x"}); err != nil {
		t.Fatal(err)
	}
	for _, verb := range []string{"create", "update"} {
		if recs := recordsOf(t, store, node.ID, verb); len(recs) != 1 || recs[0].ActorID != "" || recs[0].ActorKind != "" {
			t.Errorf("%s records = %+v; want one with no actor", verb, recs)
		}
	}
	if got := lastActorOf(t, repo, node.ID); got != "" {
		t.Errorf("last_actor = %q; want empty", got)
	}
}

// TestDynamicWrites_ItemHistory: create, update and a transition read back as
// three history entries, newest first, each with its actor.
func TestDynamicWrites_ItemHistory(t *testing.T) {
	app, _, repo := newDynWritesApp(t)
	ctx := smeldr.NewTestContext(smeldr.User{ID: "architect-1", Roles: []smeldr.Role{smeldr.Editor, smeldr.Agent}})
	node, err := repo.CreateDraftVia(ctx, "mcp", map[string]any{"Title": "Plan"})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateFieldsVia(ctx, "mcp", node.ID, map[string]any{"Body": "approved"}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.TransitionItemVia(ctx, "mcp", "recipe", node.Slug, "published", ""); err != nil {
		t.Fatalf("transition: %v", err)
	}
	page, err := app.ItemProvenance(ctx, "recipe", node.ID, smeldr.ProvenanceMembers, 0, 0)
	if err != nil {
		t.Fatalf("ItemProvenance: %v", err)
	}
	verbs := map[string]bool{}
	for _, e := range page.Entries {
		verbs[e.Verb] = true
		if e.ActorID != "architect-1" {
			t.Errorf("entry %+v; want architect-1 as the actor", e)
		}
	}
	if page.Total != 3 || !verbs["create"] || !verbs["update"] || !verbs["transition"] {
		t.Errorf("history = %+v; want create, update and transition", page)
	}
}
