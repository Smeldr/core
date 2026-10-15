//go:build integration

package pgx

import (
	"context"
	"errors"
	"testing"

	smeldr "smeldr.dev/core"
)

// Relations require two real ends on Postgres (D113 point 3): an end that does
// not exist is not-found, and an end named by slug is stored by ID.
func TestPG_RelationEndsMustExist(t *testing.T) {
	db, _ := isolatedDB(t)
	ctx := context.Background()
	if err := smeldr.CreateOrchestrationTables(db); err != nil {
		t.Fatal(err)
	}
	if err := smeldr.CreateRelationTables(db); err != nil {
		t.Fatal(err)
	}
	app := smeldr.New(smeldr.Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16"), DB: db})
	smeldr.RegisterOrchestrationTypes(app, db)
	rels, err := smeldr.NewRelationStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := smeldr.RegisterOrchestrationRelationKinds(ctx, rels); err != nil {
		t.Fatal(err)
	}
	app.Relations(rels)
	author := smeldr.TrustedRead(smeldr.NewTestContext(smeldr.User{ID: "u", Roles: []smeldr.Role{smeldr.Author, smeldr.Editor}})).(smeldr.Context)
	var tasks []*smeldr.Task
	for _, d := range []string{"one", "two"} {
		task := &smeldr.Task{Node: smeldr.Node{ID: smeldr.NewID(), Slug: "task-" + d, Status: "backlog"}, Band: "core", Description: d}
		if err := smeldr.NewSQLRepo[*smeldr.Task](db, smeldr.Table("smeldr_tasks")).Save(ctx, task); err != nil {
			t.Fatal(err)
		}
		tasks = append(tasks, task)
	}
	if _, err := rels.MCPAssertRelation(author, "Task", tasks[0].ID, "Task", "no-such-task", "depends_on", nil, nil, nil, nil); !errors.Is(err, smeldr.ErrNotFound) {
		t.Errorf("missing target: %v; want ErrNotFound", err)
	}
	edge, err := rels.MCPAssertRelation(author, "Task", tasks[0].Slug, "Task", tasks[1].Slug, "depends_on", nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if edge.SourceID != tasks[0].ID || edge.TargetID != tasks[1].ID {
		t.Errorf("edge %s -> %s; want IDs %s -> %s", edge.SourceID, edge.TargetID, tasks[0].ID, tasks[1].ID)
	}
}
