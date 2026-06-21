package smeldr

import "context"

// Listable is implemented by any content module that can supply a paginated
// list of published items for inclusion in an aggregate route.
// [Module][T] implements Listable automatically via [Module.ListPublished].
type Listable interface {
	ListPublished(ctx context.Context, opts ListOptions) ([]map[string]any, error)
}

// RouteSpec describes what is served at a route: one or more content types
// and a view ("list", "item", "aggregate-list", "aggregate-item").
// Build specs with [Serves] and [Aggregate].
type RouteSpec struct {
	view  string        // "list" | "item" | "aggregate-list" | "aggregate-item"
	specs []*ServesSpec // one entry per content type
}

// IsAggregate reports whether the spec covers more than one content type.
func (r RouteSpec) IsAggregate() bool { return len(r.specs) > 1 }

// routeEntry is an entry in the App's in-memory route registry.
type routeEntry struct {
	pattern string
	spec    RouteSpec
}

// ServesSpec is a type-erased route spec for a single content type.
// Create via [Serves].
type ServesSpec struct {
	typeName string
	listable Listable
}

// Serves returns a route spec builder for the single content type T.
// The module m must have been registered via [App.Content] before the returned
// spec is passed to [App.Route].
//
//	recipes := smeldr.NewModule[Recipe](Recipe{}, smeldr.At("/recipes"))
//	app.Content(recipes)
//	app.Route("/recipes",        smeldr.Serves[Recipe](recipes).List())
//	app.Route("/recipes/{slug}", smeldr.Serves[Recipe](recipes).Show())
func Serves[T any](m *Module[T]) *ServesSpec {
	return &ServesSpec{typeName: m.contentTypeName, listable: m}
}

// List returns a [RouteSpec] for the list view of this type.
func (s *ServesSpec) List() RouteSpec {
	return RouteSpec{view: "list", specs: []*ServesSpec{s}}
}

// Show returns a [RouteSpec] for the item (show) view of this type.
func (s *ServesSpec) Show() RouteSpec {
	return RouteSpec{view: "item", specs: []*ServesSpec{s}}
}

// AggregateSpec is a multi-type route spec builder. Create via [Aggregate].
type AggregateSpec struct{ specs []*ServesSpec }

// Aggregate returns a route spec builder that merges published items from two
// or more content types at a single URL.
//
//	app.Route("/products",        smeldr.Aggregate(smeldr.Serves[Electronics](el), smeldr.Serves[Clothing](cl)).List())
//	app.Route("/products/{slug}", smeldr.Aggregate(smeldr.Serves[Electronics](el), smeldr.Serves[Clothing](cl)).Show())
func Aggregate(specs ...*ServesSpec) *AggregateSpec {
	return &AggregateSpec{specs: specs}
}

// List returns a [RouteSpec] for the aggregate list view.
func (a *AggregateSpec) List() RouteSpec {
	return RouteSpec{view: "aggregate-list", specs: a.specs}
}

// Show returns a [RouteSpec] for the aggregate item (show) view.
func (a *AggregateSpec) Show() RouteSpec {
	return RouteSpec{view: "aggregate-item", specs: a.specs}
}
