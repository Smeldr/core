package smeldr

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAggregateListHandler_MergesItems(t *testing.T) {
	s1 := &mockListable{items: []map[string]any{
		{"slug": "alpha", "published_at": "2026-01-02T00:00:00Z"},
	}}
	s2 := &mockListable{items: []map[string]any{
		{"slug": "beta", "published_at": "2026-01-03T00:00:00Z"},
	}}
	spec := RouteSpec{
		view: "aggregate-list",
		specs: []*ServesSpec{
			{typeName: "a", listable: s1},
			{typeName: "b", listable: s2},
		},
	}
	h := aggregateRouteHandler(spec)
	req := httptest.NewRequest(http.MethodGet, "/all", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `"alpha"`) || !strings.Contains(body, `"beta"`) {
		t.Errorf("missing items in response: %s", body)
	}
}

func TestAggregateListHandler_SortsByPublishedAtDesc(t *testing.T) {
	s1 := &mockListable{items: []map[string]any{
		{"slug": "older", "published_at": "2026-01-01T00:00:00Z"},
	}}
	s2 := &mockListable{items: []map[string]any{
		{"slug": "newer", "published_at": "2026-06-01T00:00:00Z"},
	}}
	spec := RouteSpec{
		view:  "aggregate-list",
		specs: []*ServesSpec{{typeName: "a", listable: s1}, {typeName: "b", listable: s2}},
	}
	h := aggregateRouteHandler(spec)
	req := httptest.NewRequest(http.MethodGet, "/all", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	body := w.Body.String()
	newerPos := strings.Index(body, "newer")
	olderPos := strings.Index(body, "older")
	if newerPos > olderPos {
		t.Errorf("newer item should appear before older in sorted response; body=%s", body)
	}
}

func TestAggregateListHandler_UpstreamError(t *testing.T) {
	s1 := &mockListable{err: errors.New("db down")}
	spec := RouteSpec{
		view:  "aggregate-list",
		specs: []*ServesSpec{{typeName: "a", listable: s1}},
	}
	h := aggregateRouteHandler(spec)
	req := httptest.NewRequest(http.MethodGet, "/all", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code == http.StatusOK {
		t.Error("expected non-200 on upstream error")
	}
}

func TestAggregateShowHandler_FindsBySlug(t *testing.T) {
	s1 := &mockListable{items: []map[string]any{
		{"slug": "foo", "title": "Foo"},
		{"slug": "bar", "title": "Bar"},
	}}
	spec := RouteSpec{
		view:  "aggregate-item",
		specs: []*ServesSpec{{typeName: "a", listable: s1}},
	}
	h := aggregateRouteHandler(spec)
	req := httptest.NewRequest(http.MethodGet, "/all/foo", nil)
	// Simulate PathValue for {slug}.
	req.SetPathValue("slug", "foo")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `"Foo"`) {
		t.Errorf("expected Foo in response, got: %s", body)
	}
}

func TestAggregateShowHandler_NotFound(t *testing.T) {
	s1 := &mockListable{items: []map[string]any{
		{"slug": "foo", "title": "Foo"},
	}}
	spec := RouteSpec{
		view:  "aggregate-item",
		specs: []*ServesSpec{{typeName: "a", listable: s1}},
	}
	h := aggregateRouteHandler(spec)
	req := httptest.NewRequest(http.MethodGet, "/all/missing", nil)
	req.SetPathValue("slug", "missing")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code == http.StatusOK {
		t.Error("expected non-200 for missing slug")
	}
}

func TestAggregateShowHandler_EmptySlug(t *testing.T) {
	s1 := &mockListable{items: []map[string]any{{"slug": "foo"}}}
	spec := RouteSpec{
		view:  "aggregate-item",
		specs: []*ServesSpec{{typeName: "a", listable: s1}},
	}
	h := aggregateRouteHandler(spec)
	req := httptest.NewRequest(http.MethodGet, "/all/", nil)
	// No PathValue set — slug is empty string.
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code == http.StatusOK {
		t.Error("expected non-200 for empty slug")
	}
}
