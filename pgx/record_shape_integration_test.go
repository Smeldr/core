//go:build integration

package pgx

import (
	"context"
	"strings"
	"testing"

	smeldr "smeldr.dev/core"
)

// D112 on Postgres: the title columns ensure, and MigrateRecordShape titles,
// cuts and maps in one transaction, idempotently.
func TestPG_RecordShape(t *testing.T) {
	db, _ := isolatedDB(t)
	ctx := context.Background()
	if err := smeldr.CreateOrchestrationTables(db); err != nil {
		t.Fatal(err)
	}
	if err := smeldr.EnsureTaskGoalTitleColumns(ctx, db); err != nil {
		t.Fatal(err)
	}
	app := smeldr.New(smeldr.Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16"), DB: db})
	smeldr.RegisterOrchestrationTypes(app, db)
	_ = app.Handler()

	if err := smeldr.NewSQLRepo[*smeldr.Goal](db, smeldr.Table("smeldr_goals")).Save(ctx,
		&smeldr.Goal{Node: smeldr.Node{ID: smeldr.NewID(), Slug: "g-old", Status: "open"}, Description: "# Grow the backlog view\n\nmore"}); err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("word ", 40)
	if err := smeldr.NewSQLRepo[*smeldr.Amendment](db, smeldr.Table("smeldr_amendments")).Save(ctx,
		&smeldr.Amendment{Node: smeldr.Node{ID: smeldr.NewID(), Slug: "a-old", Status: "merged"}, AmendmentType: "feat", Summary: long, Body: "why"}); err != nil {
		t.Fatal(err)
	}

	rep, err := app.MigrateRecordShape(ctx, smeldr.RecordShapeMigrationOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Titles) != 2 || len(rep.AmendmentTypes) != 1 {
		t.Errorf("report = %+v", rep)
	}
	var title, summary, body, kind string
	if err := db.QueryRowContext(ctx, `SELECT title FROM smeldr_goals WHERE slug = 'g-old'`).Scan(&title); err != nil || title != "Grow the backlog view" {
		t.Errorf("goal title = %q %v", title, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT summary, body, amendment_type FROM smeldr_amendments WHERE slug = 'a-old'`).Scan(&summary, &body, &kind); err != nil {
		t.Fatal(err)
	}
	if len([]rune(summary)) > smeldr.TitleMaxLength || !strings.HasPrefix(body, strings.TrimSpace(long)) || kind != "feature" {
		t.Errorf("amendment = %q / %q / %q", summary, body, kind)
	}

	// A second run changes nothing.
	again, err := smeldr.MigrateRecordShape(ctx, db, smeldr.RecordShapeMigrationOptions{})
	if err != nil || len(again.Titles) != 0 || len(again.AmendmentTypes) != 0 {
		t.Errorf("second run = %+v %v", again, err)
	}
}
