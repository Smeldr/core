package smeldr

import (
	"context"
	"database/sql"
	"testing"
)

func TestCreateSiteConfigTable_idempotent(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateSiteConfigTable(db); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if err := CreateSiteConfigTable(db); err != nil {
		t.Fatalf("second call (idempotent): %v", err)
	}
}

func TestNewSiteConfigModule_construct(t *testing.T) {
	db := newSQLiteDB(t)
	m := NewSiteConfigModule(db)
	if m == nil {
		t.Error("NewSiteConfigModule returned nil")
	}
}

// TestCreateSiteConfigTable_SaveSucceeds is the regression pin for T246's
// live-reproduced bug: SQLRepo.Save's dbFields reflection unconditionally
// expects scheduled_at/rev columns (declared on the embedded Node), which
// CreateSiteConfigTable's own CREATE TABLE text did not declare — Save
// failed on a freshly created table, not only a pre-existing one.
func TestCreateSiteConfigTable_SaveSucceeds(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateSiteConfigTable(db); err != nil {
		t.Fatalf("CreateSiteConfigTable: %v", err)
	}
	repo := NewSQLRepo[SiteConfig](db, Table("smeldr_site_configs"))
	sc := SiteConfig{Node: Node{ID: NewID(), Slug: "site-config", Status: Draft}}
	if err := repo.Save(context.Background(), sc); err != nil {
		t.Fatalf("Save: %v", err)
	}
}

// TestCreateSiteConfigTable_CreateFails covers the CREATE TABLE ExecContext
// error branch — the 1st ExecContext call.
func TestCreateSiteConfigTable_CreateFails(t *testing.T) {
	db := &nthExecFailDB{DB: newSQLiteDB(t), fail: 1}
	if err := CreateSiteConfigTable(db); err == nil {
		t.Error("expected error when CREATE TABLE fails, got nil")
	}
}

// TestCreateSiteConfigTable_MigratesPreexistingTable simulates an instance
// that already ran the pre-T246 DDL (scheduled_at/rev absent) and confirms
// calling the fixed CreateSiteConfigTable again upgrades it in place, not
// only a fresh install.
func TestCreateSiteConfigTable_MigratesPreexistingTable(t *testing.T) {
	db := newSQLiteDB(t)
	legacySiteConfigsTable(t, db)

	if err := CreateSiteConfigTable(db); err != nil {
		t.Fatalf("CreateSiteConfigTable (migrate pre-existing): %v", err)
	}

	repo := NewSQLRepo[SiteConfig](db, Table("smeldr_site_configs"))
	sc := SiteConfig{Node: Node{ID: NewID(), Slug: "site-config", Status: Draft}}
	if err := repo.Save(context.Background(), sc); err != nil {
		t.Fatalf("Save after migration: %v", err)
	}
}

// legacySiteConfigsTable creates smeldr_site_configs in its pre-T246 shape
// (scheduled_at/rev absent) on a real SQLite DB — the exact shape of the
// "already caused hand-patching twice" incident the Task cites. Against a
// fresh table (this fix's own new CREATE TABLE text), EnsureColumn's PRAGMA
// probe finds both columns already present and never calls ExecContext for
// the ALTER at all, so exercising EnsureColumn's own ALTER path — whether
// to prove the migration or to fail it — requires a column genuinely
// missing beforehand.
func legacySiteConfigsTable(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), `
CREATE TABLE smeldr_site_configs (
	id               TEXT NOT NULL PRIMARY KEY,
	slug             TEXT NOT NULL DEFAULT 'site-config',
	status           TEXT NOT NULL DEFAULT 'draft',
	created_at       DATETIME NOT NULL,
	updated_at       DATETIME NOT NULL,
	published_at     DATETIME,
	site_name        TEXT NOT NULL DEFAULT '',
	title_separator  TEXT NOT NULL DEFAULT '',
	og_image         TEXT NOT NULL DEFAULT '',
	x_handle         TEXT NOT NULL DEFAULT '',
	head_script      TEXT NOT NULL DEFAULT ''
)`); err != nil {
		t.Fatalf("create pre-existing (pre-T246) table: %v", err)
	}
}

// TestCreateSiteConfigTable_ScheduledAtMigrationFails covers EnsureColumn's
// error branch for scheduled_at. Reached only when the column is genuinely
// missing beforehand (see legacySiteConfigsTable) — the 2nd ExecContext
// call, following the CREATE TABLE IF NOT EXISTS no-op.
func TestCreateSiteConfigTable_ScheduledAtMigrationFails(t *testing.T) {
	real := newSQLiteDB(t)
	legacySiteConfigsTable(t, real)
	db := &nthExecFailDB{DB: real, fail: 2}
	if err := CreateSiteConfigTable(db); err == nil {
		t.Error("expected error when the scheduled_at migration fails, got nil")
	}
}

// TestCreateSiteConfigTable_RevMigrationFails covers EnsureColumn's error
// branch for rev — the 3rd ExecContext call (the 2nd ALTER).
func TestCreateSiteConfigTable_RevMigrationFails(t *testing.T) {
	real := newSQLiteDB(t)
	legacySiteConfigsTable(t, real)
	db := &nthExecFailDB{DB: real, fail: 3}
	if err := CreateSiteConfigTable(db); err == nil {
		t.Error("expected error when the rev migration fails, got nil")
	}
}
