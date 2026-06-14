package smeldr

import (
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
