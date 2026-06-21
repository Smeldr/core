package smeldr

import (
	"cmp"
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
)

// aggregateRouteHandler returns an http.Handler that serves an aggregate route.
// For list patterns (no {slug} segment): calls ListPublished on all specs in
// parallel, merges results sorted by "published_at" descending, and writes JSON.
// For show patterns ({slug} present): finds the first item whose "slug" field
// matches the request's slug value across all specs, and writes that item as JSON.
func aggregateRouteHandler(spec RouteSpec) http.Handler {
	isShow := strings.Contains(spec.view, "item")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if isShow {
			aggregateShowHandler(ctx, w, r, spec.specs)
		} else {
			aggregateListHandler(ctx, w, r, spec.specs)
		}
	})
}

func aggregateListHandler(ctx context.Context, w http.ResponseWriter, r *http.Request, specs []*ServesSpec) {
	type result struct {
		items []map[string]any
		err   error
	}
	ch := make(chan result, len(specs))
	for _, s := range specs {
		s := s
		go func() {
			items, err := s.listable.ListPublished(ctx, ListOptions{Status: []Status{Published}})
			ch <- result{items, err}
		}()
	}

	var merged []map[string]any
	for range specs {
		res := <-ch
		if res.err != nil {
			WriteError(w, r, res.err)
			return
		}
		merged = append(merged, res.items...)
	}

	// Sort by published_at descending; items without the field sort last.
	slices.SortStableFunc(merged, func(a, b map[string]any) int {
		pa, _ := a["published_at"].(string)
		pb, _ := b["published_at"].(string)
		return cmp.Compare(pb, pa) // descending: b before a
	})

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]any{"items": merged}) //nolint:errcheck
}

func aggregateShowHandler(ctx context.Context, w http.ResponseWriter, r *http.Request, specs []*ServesSpec) {
	slug := r.PathValue("slug")
	if slug == "" {
		WriteError(w, r, ErrNotFound)
		return
	}

	type result struct {
		items []map[string]any
		err   error
	}
	ch := make(chan result, len(specs))
	for _, s := range specs {
		s := s
		go func() {
			items, err := s.listable.ListPublished(ctx, ListOptions{Status: []Status{Published}})
			ch <- result{items, err}
		}()
	}

	for range specs {
		res := <-ch
		if res.err != nil {
			continue
		}
		for _, item := range res.items {
			if s, _ := item["slug"].(string); s == slug {
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				json.NewEncoder(w).Encode(item) //nolint:errcheck
				return
			}
		}
	}
	WriteError(w, r, ErrNotFound)
}
