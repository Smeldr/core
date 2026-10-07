package smeldr

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
)

// failUpdateDB fails every UPDATE and passes everything else through.
type failUpdateDB struct{ DB }

func (f failUpdateDB) ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error) {
	if strings.HasPrefix(strings.TrimSpace(q), "UPDATE") {
		return nil, errors.New("update refused")
	}
	return f.DB.ExecContext(ctx, q, args...)
}

// The UPDATE of UpdateFieldsVia failing returns the error and records nothing.
func TestUpdateFieldsVia_ExecErrorRecordsNothing(t *testing.T) {
	app, db, _, store := setupProvenanceTransitionApp(t)
	typeName, slug := defineProvenanceDynamicType(t, app, db, "dynexec")
	repo, err := app.DynamicContentRepo(typeName)
	if err != nil {
		t.Fatal(err)
	}
	node, err := repo.GetBySlug(context.Background(), slug)
	if err != nil {
		t.Fatal(err)
	}
	broken := *repo
	broken.db = failUpdateDB{db}
	ctx := NewTestContext(User{ID: "alice", Roles: []Role{Editor}})
	if err := broken.UpdateFieldsVia(ctx, "mcp", node.ID, map[string]any{"Title": "x"}); err == nil {
		t.Fatal("want the UPDATE error")
	}
	if got := store.Appended(); len(got) != 0 {
		t.Errorf("records after a failed update: %+v", got)
	}
}
