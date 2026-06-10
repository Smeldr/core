package smeldr

import (
	"context"
	"testing"
)

// testBlockParent is a minimal ContentParentProvider for block_host_test.go.
type testBlockParent struct {
	typeName string
	ids      map[string]bool
}

func (p *testBlockParent) BlockParentTypeName() string { return p.typeName }
func (p *testBlockParent) HasBlockParent(_ context.Context, id string) (bool, error) {
	return p.ids[id], nil
}

func TestBlockHost_OptionSetsFlag(t *testing.T) {
	m := NewModule((*testPost)(nil), Repo(NewMemoryRepo[*testPost]()), BlockHost())
	if !m.blockHost {
		t.Error("BlockHost() option did not set blockHost = true")
	}
}

func TestBlockHost_ModuleImplementsContentParentProvider(t *testing.T) {
	m := NewModule((*testPost)(nil), Repo(NewMemoryRepo[*testPost]()), BlockHost())
	var _ ContentParentProvider = m
}

func TestBlockHost_BlockParentTypeName(t *testing.T) {
	m := NewModule((*testPost)(nil), Repo(NewMemoryRepo[*testPost]()), BlockHost())
	got := m.BlockParentTypeName()
	if got != "testpost" {
		t.Errorf("BlockParentTypeName() = %q, want %q", got, "testpost")
	}
}

func TestBlockHost_HasBlockParent_NotFound(t *testing.T) {
	m := NewModule((*testPost)(nil), Repo(NewMemoryRepo[*testPost]()))
	ok, err := m.HasBlockParent(context.Background(), "does-not-exist")
	if err != nil {
		t.Fatalf("HasBlockParent returned unexpected error: %v", err)
	}
	if ok {
		t.Error("HasBlockParent returned true for non-existent ID")
	}
}

func TestApp_RegisterBlockParent_And_BlockParents(t *testing.T) {
	app := New(Config{BaseURL: "http://localhost", Secret: []byte(testSecret)})
	p := &testBlockParent{typeName: "page"}
	app.RegisterBlockParent(p)
	parents := app.BlockParents()
	if len(parents) != 1 {
		t.Fatalf("BlockParents() len = %d, want 1", len(parents))
	}
	if parents[0].BlockParentTypeName() != "page" {
		t.Errorf("BlockParentTypeName() = %q, want %q", parents[0].BlockParentTypeName(), "page")
	}
}

func TestApp_Content_AutoRegisters_BlockHostModule(t *testing.T) {
	app := New(Config{BaseURL: "http://localhost", Secret: []byte(testSecret)})
	m := NewModule((*testPost)(nil), Repo(NewMemoryRepo[*testPost]()), BlockHost())
	app.Content(m)
	parents := app.BlockParents()
	if len(parents) != 1 {
		t.Fatalf("BlockParents() len = %d, want 1 after Content() with BlockHost", len(parents))
	}
}

func TestApp_Content_NonBlockHost_NotRegistered(t *testing.T) {
	app := New(Config{BaseURL: "http://localhost", Secret: []byte(testSecret)})
	m := NewModule((*testPost)(nil), Repo(NewMemoryRepo[*testPost]()))
	app.Content(m)
	parents := app.BlockParents()
	if len(parents) != 0 {
		t.Fatalf("BlockParents() len = %d, want 0 for non-BlockHost module", len(parents))
	}
}
