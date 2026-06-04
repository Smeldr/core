package smeldr

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

// ---------------------------------------------------------------------------
// Public types
// ---------------------------------------------------------------------------

// ContentTypeStats holds item counts for one registered content type,
// broken down by lifecycle status.
type ContentTypeStats struct {
	TypeName string         `json:"type"`
	Prefix   string         `json:"prefix"`
	Counts   map[Status]int `json:"counts"`
}

// SiteStats is the aggregate returned by [App.Stats] and [App.StatsHandler].
// The External field carries additional statistics from modules registered via
// [App.RegisterStatsProvider]; it is omitted from JSON when no providers are registered.
type SiteStats struct {
	Content     []ContentTypeStats `json:"content"`
	External    map[string]any     `json:"external,omitempty"`
	GeneratedAt string             `json:"generated_at"`
}

// StatsExtProvider is an optional interface that external modules implement to
// contribute additional statistics to [App.Stats] without creating an import
// cycle between core and the external module. Register with
// [App.RegisterStatsProvider]:
//
//	app.RegisterStatsProvider(mediaSrv.StatsProvider())
//
// The value returned by [ProvideStats] is placed in [SiteStats.External] under
// the key returned by [StatsKey]. A non-nil error is logged at Warn level and
// causes that provider's contribution to be omitted — it never prevents the
// remaining stats from being returned.
type StatsExtProvider interface {
	StatsKey() string
	ProvideStats(ctx context.Context) (map[string]any, error)
}

// ---------------------------------------------------------------------------
// Private interfaces
// ---------------------------------------------------------------------------

// statusCounter is implemented by repositories that support efficient
// status-grouped counting. Type-asserted at stats-collection time.
// Repositories that do not implement it degrade gracefully (empty counts).
type statusCounter interface {
	countByStatus(ctx context.Context) (map[Status]int, error)
}

// statsCollector is implemented by content modules (Module[T]) that can report
// item counts. Collected by App.Content() and called by App.Stats().
type statsCollector interface {
	collectStats(ctx context.Context) ContentTypeStats
}

// ---------------------------------------------------------------------------
// App methods
// ---------------------------------------------------------------------------

// RegisterStatsProvider adds p to the list of external statistics providers
// consulted by [App.Stats] and [App.StatsHandler].
// Call before [App.Handler] or [App.Run].
func (a *App) RegisterStatsProvider(p StatsExtProvider) {
	a.statsExtProviders = append(a.statsExtProviders, p)
}

// Stats aggregates content counts across all registered modules and any external
// providers. Each content module reports its item counts per [Status]; external
// providers contribute their own JSON-serialisable values via [StatsExtProvider].
//
// Stats never returns a partial error — individual provider failures are logged
// at Warn and omitted from the result.
func (a *App) Stats(ctx context.Context) (SiteStats, error) {
	content := make([]ContentTypeStats, 0, len(a.statsCollectors))
	for _, sc := range a.statsCollectors {
		content = append(content, sc.collectStats(ctx))
	}

	var external map[string]any
	for _, p := range a.statsExtProviders {
		data, err := p.ProvideStats(ctx)
		if err != nil {
			slog.WarnContext(ctx, "smeldr: stats provider failed",
				"provider", p.StatsKey(), "error", err)
			continue
		}
		if external == nil {
			external = make(map[string]any)
		}
		external[p.StatsKey()] = data
	}

	return SiteStats{
		Content:     content,
		External:    external,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
	}, nil
}

// StatsHandler mounts GET /_stats on the App's mux.
// Requires Admin role — /_stats exposes site-internal metadata (content volumes
// and, when a StatsExtProvider is registered, media disk usage) that must not
// be publicly visible. Call before [App.Handler] or [App.Run].
//
//	app.StatsHandler()
//	// GET /_stats (Admin bearer token) → {"content":[...],"generated_at":"..."}
func (a *App) StatsHandler() {
	auth := a.cfg.Auth
	if auth == nil {
		auth = BearerHMAC(string(a.cfg.Secret))
	}
	a.mux.Handle("GET /_stats", newStatsHandler(auth, a))
}

// newStatsHandler returns the http.Handler mounted by [App.StatsHandler].
func newStatsHandler(auth AuthFunc, app *App) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.authenticate(r)
		if !ok {
			WriteError(w, r, ErrUnauth)
			return
		}
		if !user.HasRole(Admin) {
			WriteError(w, r, ErrForbidden)
			return
		}

		stats, err := app.Stats(r.Context())
		if err != nil {
			slog.ErrorContext(r.Context(), "smeldr: stats failed", "error", err)
			WriteError(w, r, ErrInternal)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(stats); err != nil {
			slog.WarnContext(r.Context(), "smeldr: stats encode failed", "error", err)
		}
	})
}
