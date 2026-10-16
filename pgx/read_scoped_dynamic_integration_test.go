//go:build integration

package pgx

import (
	"context"
	"errors"
	"fmt"
	"testing"

	smeldr "smeldr.dev/core"
)

// TestPG_DynamicListPageAndSignalSubject: on Postgres, a scoped reader's
// dynamic pages and total cover only what it may read, and a Signal whose
// subject is hidden resolves as missing.
func TestPG_DynamicListPageAndSignalSubject(t *testing.T) {
	db, _ := isolatedDB(t)
	app, _ := dynamicApp(t, db, nil)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS smeldr_tokens (id TEXT NOT NULL PRIMARY KEY, role TEXT NOT NULL DEFAULT '')`); err != nil {
		t.Fatal(err)
	}
	if err := smeldr.CreateOrchestrationTables(db); err != nil {
		t.Fatal(err)
	}
	smeldr.RegisterOrchestrationTypes(app, db)
	if _, err := app.DefineContentType(ctx, &smeldr.ContentTypeSchema{TypeName: "invoice",
		Fields: []byte(`[{"Name":"Title","Type":"string","Role":"title"}]`)}); err != nil {
		t.Fatal(err)
	}
	repo, err := app.DynamicContentRepo("invoice")
	if err != nil {
		t.Fatal(err)
	}
	trusted := smeldr.TrustedRead(ctx)
	var ids []string
	for i := 0; i < 5; i++ {
		n, err := repo.CreateDraft(trusted, map[string]any{"Title": fmt.Sprintf("inv %d", i)})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, n.ID)
	}
	rs := app.RoleStore()
	if err := smeldr.RegisterScopedReaderRole(ctx, rs); err != nil {
		t.Fatal(err)
	}
	if _, err := rs.Grant(ctx, smeldr.RoleGrant{TokenID: "tok", RoleName: "scoped-reader",
		ScopeStatic: []string{"invoice:" + ids[1], "invoice:" + ids[3], "Signal:*"}}); err != nil {
		t.Fatal(err)
	}
	reader := smeldr.NewTestContext(smeldr.User{ID: "tok"})

	seen := map[string]bool{}
	for page := 1; page <= 2; page++ {
		items, total, err := repo.ListPage(reader, smeldr.ListOptions{Page: page, PerPage: 1, OrderBy: "created_at"})
		if err != nil || total != 2 || len(items) != 1 {
			t.Fatalf("page %d = %d items, total %d, %v; want 1 of 2", page, len(items), total, err)
		}
		seen[items[0]["ID"].(string)] = true
	}
	if !seen[ids[1]] || !seen[ids[3]] {
		t.Errorf("pages returned %v; want the two readable invoices", seen)
	}
	if _, total, err := repo.ListPage(trusted, smeldr.ListOptions{PerPage: 2}); err != nil || total != 5 {
		t.Errorf("trusted total = %d %v; want 5", total, err)
	}

	// D113: the subject must exist; this Task is one the reader cannot read.
	hiddenTask := smeldr.NewID()
	if err := smeldr.NewSQLRepo[*smeldr.Task](db, smeldr.Table("smeldr_tasks")).Save(ctx, &smeldr.Task{Node: smeldr.Node{ID: hiddenTask, Slug: hiddenTask, Status: "backlog"}}); err != nil {
		t.Fatal(err)
	}
	about := &smeldr.Signal{Sender: "core", SignalType: "note", SubjectType: "Task", SubjectID: hiddenTask}
	plain := &smeldr.Signal{Sender: "core", SignalType: "note"}
	for _, s := range []*smeldr.Signal{about, plain} {
		if err := smeldr.InsertSignal(ctx, db, s); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := app.ResolveReadableID(reader, "Signal", about.Slug); !errors.Is(err, smeldr.ErrNotFound) {
		t.Errorf("a Signal about a hidden Task: %v; want not found", err)
	}
	if id, err := app.ResolveReadableID(reader, "Signal", plain.Slug); err != nil || id != plain.ID {
		t.Errorf("a Signal with no subject = %q %v", id, err)
	}
}
