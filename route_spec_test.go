package smeldr

import (
	"context"
	"testing"
)

// mockListable is a test-only Listable that returns a fixed set of items.
type mockListable struct {
	items []map[string]any
	err   error
}

func (m *mockListable) ListPublished(_ context.Context, _ ListOptions) ([]map[string]any, error) {
	return m.items, m.err
}

func newModuleWithRepo(opts ...Option) *Module[*testPost] {
	return newTestModule(NewMemoryRepo[*testPost](), opts...)
}

func TestServes_TypeName(t *testing.T) {
	m := newModuleWithRepo(At("/posts"))
	spec := Serves[*testPost](m)
	if spec.typeName != "testPost" {
		t.Errorf("typeName = %q; want testPost", spec.typeName)
	}
	if spec.listable == nil {
		t.Error("listable is nil")
	}
}

func TestServesSpec_ListAndShow(t *testing.T) {
	m := newModuleWithRepo(At("/posts"))
	s := Serves[*testPost](m)

	list := s.List()
	if list.view != "list" {
		t.Errorf("List().view = %q; want list", list.view)
	}
	if len(list.specs) != 1 {
		t.Fatalf("List().specs length = %d; want 1", len(list.specs))
	}
	if list.IsAggregate() {
		t.Error("single-type List() should not be aggregate")
	}

	show := s.Show()
	if show.view != "item" {
		t.Errorf("Show().view = %q; want item", show.view)
	}
	if show.IsAggregate() {
		t.Error("single-type Show() should not be aggregate")
	}
}

func TestAggregate_TwoSpecs(t *testing.T) {
	m1 := newModuleWithRepo(At("/posts"))
	m2 := newModuleWithRepo(At("/articles"))

	agg := Aggregate(Serves[*testPost](m1), Serves[*testPost](m2))
	list := agg.List()

	if list.view != "aggregate-list" {
		t.Errorf("view = %q; want aggregate-list", list.view)
	}
	if len(list.specs) != 2 {
		t.Fatalf("specs length = %d; want 2", len(list.specs))
	}
	if !list.IsAggregate() {
		t.Error("two-type spec should be aggregate")
	}

	show := agg.Show()
	if show.view != "aggregate-item" {
		t.Errorf("Show().view = %q; want aggregate-item", show.view)
	}
}

func TestAppRoute_SingleTypePopulatesRegistry(t *testing.T) {
	app := newTestApp()
	m := newModuleWithRepo(At("/posts"))
	app.Content(m)
	app.Route("/posts", Serves[*testPost](m).List())

	if len(app.routeReg) == 0 {
		t.Fatal("routeReg is empty after app.Route")
	}
	found := false
	for _, e := range app.routeReg {
		if e.pattern == "/posts" && e.spec.view == "list" {
			found = true
		}
	}
	if !found {
		t.Error("/posts list entry not found in routeReg")
	}
}

func TestAppRoute_AggregateRegistersHandler(t *testing.T) {
	app := newTestApp()
	m1 := newModuleWithRepo(At("/posts"))
	m2 := newModuleWithRepo(At("/articles"))
	app.Content(m1)
	app.Content(m2)

	app.Route("/all", Aggregate(Serves[*testPost](m1), Serves[*testPost](m2)).List())

	found := false
	for _, e := range app.routeReg {
		if e.pattern == "/all" && e.spec.view == "aggregate-list" {
			found = true
		}
	}
	if !found {
		t.Error("/all aggregate-list not in routeReg")
	}
}

func TestAppContent_PopulatesRouteRegFromAt(t *testing.T) {
	app := newTestApp()
	m := newModuleWithRepo(At("/posts"))
	app.Content(m)

	// app.Content should auto-populate routeReg with list + item entries.
	var listFound, itemFound bool
	for _, e := range app.routeReg {
		if e.pattern == "/posts" && e.spec.view == "list" {
			listFound = true
		}
		if e.pattern == "/posts/{slug}" && e.spec.view == "item" {
			itemFound = true
		}
	}
	if !listFound {
		t.Error("/posts list entry not auto-populated in routeReg")
	}
	if !itemFound {
		t.Error("/posts/{slug} item entry not auto-populated in routeReg")
	}
}

func TestListable_ModuleImplements(t *testing.T) {
	m := newModuleWithRepo(At("/posts"))
	var _ Listable = m // compile-time check
}
