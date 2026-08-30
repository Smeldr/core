// AGPL-3.0-or-later

package smeldr

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
)

// ReachabilityHandler registers GET /reachability/{type}/{id} on the app's
// mux — a real remote-exposure path for [RelationStore.Reachability]
// (A293), unblocking a cross-instance "what already exists in domain X"
// query that [ContextPacket] cannot serve: its depth cap (2) and per-type
// cap (25) silently undercount a real, densely-connected domain, the same
// limitation that already ruled it out for Pulse's Tension computation.
//
// Optional query parameters: kind (relation kind filter, empty = all
// kinds), direction ("incoming"/"outgoing"/"both", default "both"), depth
// (1 to [MaxReachabilityDepth], default 1). No depth cap tighter than
// [MaxReachabilityDepth] is imposed here — that would recreate the exact
// gap this handler exists to close.
//
// Requires Author role via bearer auth — same contract as [App.Audit]'s
// own GET /_audit and the other raw, non-Module[T] admin/operational
// routes. Reachability exposes graph structure only (type/id/edge_class/
// confidence, no item content), a materially lower bar than
// [App.ContextPacketHandler]'s own Editor requirement — matching
// [RelationStore.MCPGetRelations]'s own existing Author+ bar for a
// structurally identical graph read.
func (a *App) ReachabilityHandler(rs *RelationStore) {
	auth := a.cfg.Auth
	if auth == nil {
		auth = BearerHMAC(string(a.cfg.Secret))
	}
	a.mux.Handle("GET /reachability/{type}/{id}", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.authenticate(r)
		if !ok {
			WriteError(w, r, ErrUnauth)
			return
		}
		if !user.HasRole(Author) {
			WriteError(w, r, ErrForbidden)
			return
		}
		anchorType := r.PathValue("type")
		anchorID := r.PathValue("id")
		kind := r.URL.Query().Get("kind")
		direction := r.URL.Query().Get("direction")
		if direction == "" {
			direction = "both"
		}
		depth := 1
		if d := r.URL.Query().Get("depth"); d != "" {
			v, err := strconv.Atoi(d)
			if err != nil {
				WriteError(w, r, ErrBadRequest)
				return
			}
			depth = v
		}
		result, err := rs.Reachability(r.Context(), anchorType, anchorID, kind, direction, depth)
		if err != nil {
			WriteError(w, r, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if encErr := json.NewEncoder(w).Encode(result); encErr != nil {
			slog.ErrorContext(r.Context(), "smeldr: ReachabilityHandler: encode", "error", encErr)
		}
	}))
}
