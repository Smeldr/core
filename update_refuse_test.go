// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

// TestUpdate_RefusesLifecycleKeys: an update that asks to change the status,
// the slug or the ID is refused with ErrBadRequest naming the field, and
// nothing is saved; the same value as stored, or no such key, is accepted.
func TestUpdate_RefusesLifecycleKeys(t *testing.T) {
	cases := []struct {
		name   string
		fields func(p *testPost) map[string]any
		want   string // "" = accepted
	}{
		{"status", func(*testPost) map[string]any { return map[string]any{"status": "published"} }, "cannot be changed by an update; use transition_item"},
		{"Status", func(*testPost) map[string]any { return map[string]any{"Status": "published"} }, "cannot be changed by an update; use transition_item"},
		{"STATUS", func(*testPost) map[string]any { return map[string]any{"STATUS": "archived"} }, "cannot be changed by an update; use transition_item"},
		{"status not a string", func(*testPost) map[string]any { return map[string]any{"status": 7} }, "cannot be changed by an update; use transition_item"},
		{"slug", func(*testPost) map[string]any { return map[string]any{"slug": "other"} }, "slug"},
		{"id", func(*testPost) map[string]any { return map[string]any{"ID": "other"} }, "id"},
		{"same status", func(p *testPost) map[string]any {
			return map[string]any{"Status": string(p.Status), "Title": "New"}
		}, ""},
		{"same slug and id", func(p *testPost) map[string]any {
			return map[string]any{"slug": p.Slug, "id": p.ID, "Title": "New"}
		}, ""},
		{"content only", func(*testPost) map[string]any { return map[string]any{"Title": "New"} }, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			repo := NewMemoryRepo[*testPost]()
			m := newTestModule(repo)
			seed := seedPost(t, repo, "Item", Draft)
			_, err := m.MCPUpdate(hookCtx(), seed.Slug, c.fields(seed))
			stored, _ := repo.FindByID(context.Background(), seed.ID)
			if c.want == "" {
				if err != nil {
					t.Fatalf("update refused: %v", err)
				}
				if stored.Title != "New" {
					t.Errorf("title = %q; want New", stored.Title)
				}
				return
			}
			var ve *ValidationError
			if !errors.As(err, &ve) || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v; want a ValidationError with %q", err, c.want)
			}
			if stored.Status != Draft || stored.Slug != seed.Slug || stored.Title != "Item" {
				t.Errorf("stored item changed: %+v", stored)
			}
		})
	}
}

// TestUpdate_PatchRefusesStatus: PATCH over HTTP gives 400 for a status
// change and still accepts a content edit.
func TestUpdate_PatchRefusesStatus(t *testing.T) {
	repo := NewMemoryRepo[*testPost]()
	m := newTestModule(repo)
	seed := seedPost(t, repo, "Item", Draft)
	err := serveWrite(m.patchHandler, http.MethodPatch, "/testposts/"+seed.Slug, seed.Slug, map[string]any{"status": "published"})
	var he errHTTPStatus
	if !errors.As(err, &he) || he.code != http.StatusUnprocessableEntity {
		t.Fatalf("PATCH status = %v; want 422", err)
	}
	if err := serveWrite(m.patchHandler, http.MethodPatch, "/testposts/"+seed.Slug, seed.Slug, map[string]any{"Title": "New", "slug": seed.Slug}); err != nil {
		t.Fatalf("PATCH content: %v", err)
	}
}

// TestUpdate_ByHumanIDIsNotASlugChange: an update addressed by a Task's human
// ID (the identifier is not a body key) succeeds, over MCP and over PATCH;
// the same for a Signal, the two types the defect was found on.
func TestUpdate_ByHumanIDIsNotASlugChange(t *testing.T) {
	_, db, _ := setupTransitionItemApp(t)
	tasks := NewModule((*Task)(nil), At("/tasks"), Repo(NewSQLRepo[*Task](db, Table("smeldr_tasks"))), MCP(MCPRead, MCPWrite))
	tasks.setDB(db)
	task := &Task{Node: Node{ID: NewID(), Slug: "task-slug", Status: "backlog"}, TaskID: "T23", Band: "core"}
	if err := tasks.repo.Save(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.MCPUpdate(hookCtx(), "T23", map[string]any{"band": "cloud"}); err != nil {
		t.Fatalf("MCPUpdate by human ID: %v", err)
	}
	if err := serveWrite(tasks.patchHandler, http.MethodPatch, "/tasks/T23", "T23", map[string]any{"band": "site", "slug": "task-slug"}); err != nil {
		t.Fatalf("PATCH by human ID: %v", err)
	}
	stored, _ := tasks.repo.FindByID(context.Background(), task.ID)
	if stored.Band != "site" || stored.Status != "backlog" {
		t.Errorf("stored = band %q status %q; want site, backlog", stored.Band, stored.Status)
	}
	if _, err := tasks.MCPUpdate(hookCtx(), "T23", map[string]any{"status": "resolved"}); !isValidationErr(err) {
		t.Errorf("update_task status = %v; want a ValidationError", err)
	}

	signals := NewModule((*Signal)(nil), At("/signals"), Repo(NewSQLRepo[*Signal](db, Table("smeldr_signals"))), MCP(MCPRead, MCPWrite))
	signals.setDB(db)
	insertSignal(t, db, "sg", "sg-slug", "pending")
	if _, err := signals.MCPUpdate(hookCtx(), "sg-slug", map[string]any{"status": "read"}); !isValidationErr(err) {
		t.Errorf("update_signal status = %v; want a ValidationError", err)
	}
}

// TestUpdate_DynamicContentRefusesStatus pins the existing behaviour of a
// runtime-defined type: status is not a field, so an update naming it fails.
func TestUpdate_DynamicContentRefusesStatus(t *testing.T) {
	app, db, _, _ := setupProvenanceTransitionApp(t)
	typeName, slug := defineProvenanceDynamicType(t, app, db, "upd")
	repo, err := app.DynamicContentRepo(typeName)
	if err != nil {
		t.Fatal(err)
	}
	node, err := repo.GetBySlug(context.Background(), slug)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateFields(hookCtx(), node.ID, map[string]any{"status": "published"}); err == nil {
		t.Error("a dynamic update with status was accepted")
	}
}

func isValidationErr(err error) bool {
	var ve *ValidationError
	return errors.As(err, &ve)
}
