// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// — MCPReachability (thin wrapper) ———————————————————————————————————————————

func TestMCPReachability_ThinWrapper(t *testing.T) {
	store := setupRelationStore(t)
	ctx := context.Background()
	if err := store.UpsertKind(ctx, RelationKindDef{TypeName: "links", Mode: "asserted"}); err != nil {
		t.Fatalf("UpsertKind: %v", err)
	}
	if err := store.Assert(ctx, RelationEdge{
		ID: NewID(), SourceType: "Task", SourceID: "t1",
		TargetType: "Goal", TargetID: "g1",
		RelationKind: "links", EdgeClass: "asserted",
	}); err != nil {
		t.Fatalf("Assert: %v", err)
	}

	got, err := store.MCPReachability(ctx, "Task", "t1", "", "outgoing", 1)
	if err != nil {
		t.Fatalf("MCPReachability: %v", err)
	}
	want, err := store.Reachability(ctx, "Task", "t1", "", "outgoing", 1)
	if err != nil {
		t.Fatalf("Reachability: %v", err)
	}
	if got.AnchorType != want.AnchorType || got.AnchorID != want.AnchorID ||
		len(got.Rings) != len(want.Rings) {
		t.Errorf("MCPReachability = %+v, want passthrough of Reachability = %+v", got, want)
	}
}

// — HTTP handler ——————————————————————————————————————————————————————————————

const reachabilityTestSecret = "testsecret16chars"

func TestReachabilityHandler_200(t *testing.T) {
	db, rs := setupPacketDB(t)
	ctx := context.Background()
	if err := rs.UpsertKind(ctx, RelationKindDef{TypeName: "links", Mode: "asserted"}); err != nil {
		t.Fatalf("UpsertKind: %v", err)
	}
	if err := rs.Assert(ctx, RelationEdge{
		ID: NewID(), SourceType: "Task", SourceID: "t1",
		TargetType: "Goal", TargetID: "g1",
		RelationKind: "links", EdgeClass: "asserted",
	}); err != nil {
		t.Fatalf("Assert: %v", err)
	}

	app := New(Config{
		BaseURL: "http://localhost",
		Secret:  []byte(reachabilityTestSecret),
		DB:      db,
	})
	app.ReachabilityHandler(rs)

	tok, err := SignToken(User{ID: "u1", Roles: []Role{Author}}, reachabilityTestSecret, 0)
	if err != nil {
		t.Fatalf("SignToken: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/reachability/Task/t1?direction=outgoing&depth=1", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	app.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", w.Code, w.Body.String())
	}
	var got Reachability
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.AnchorType != "Task" || got.AnchorID != "t1" {
		t.Errorf("anchor = %s/%s, want Task/t1", got.AnchorType, got.AnchorID)
	}
	if len(got.Rings) != 1 || len(got.Rings[0].Items) != 1 || got.Rings[0].Items[0].ID != "g1" {
		t.Errorf("rings = %+v, want one ring containing g1", got.Rings)
	}
}

func TestReachabilityHandler_400_invalidDirection(t *testing.T) {
	db, rs := setupPacketDB(t)
	app := New(Config{
		BaseURL: "http://localhost",
		Secret:  []byte(reachabilityTestSecret),
		DB:      db,
	})
	app.ReachabilityHandler(rs)

	tok, err := SignToken(User{ID: "u1", Roles: []Role{Author}}, reachabilityTestSecret, 0)
	if err != nil {
		t.Fatalf("SignToken: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/reachability/Task/t1?direction=sideways", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	app.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestReachabilityHandler_400_invalidDepth(t *testing.T) {
	db, rs := setupPacketDB(t)
	app := New(Config{
		BaseURL: "http://localhost",
		Secret:  []byte(reachabilityTestSecret),
		DB:      db,
	})
	app.ReachabilityHandler(rs)

	tok, err := SignToken(User{ID: "u1", Roles: []Role{Author}}, reachabilityTestSecret, 0)
	if err != nil {
		t.Fatalf("SignToken: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/reachability/Task/t1?depth=notanumber", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	app.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestReachabilityHandler_Unauthorized(t *testing.T) {
	db, rs := setupPacketDB(t)
	app := New(Config{
		BaseURL: "http://localhost",
		Secret:  []byte(reachabilityTestSecret),
		DB:      db,
	})
	app.ReachabilityHandler(rs)

	req := httptest.NewRequest(http.MethodGet, "/reachability/Task/t1", nil)
	w := httptest.NewRecorder()
	app.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestReachabilityHandler_ForbiddenBelowAuthor(t *testing.T) {
	db, rs := setupPacketDB(t)
	app := New(Config{
		BaseURL: "http://localhost",
		Secret:  []byte(reachabilityTestSecret),
		DB:      db,
	})
	app.ReachabilityHandler(rs)

	tok, err := SignToken(User{ID: "u1", Roles: []Role{Guest}}, reachabilityTestSecret, 0)
	if err != nil {
		t.Fatalf("SignToken: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/reachability/Task/t1", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	app.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
}
