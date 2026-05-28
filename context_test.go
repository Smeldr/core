package smeldr

import (
	"net/http/httptest"
	"testing"
)

// TestContextFromGeneratesRequestID verifies that ContextFrom generates a
// non-empty X-Request-ID when none is present on request or response.
func TestContextFromGeneratesRequestID(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()

	ctx := ContextFrom(rec, req)

	if ctx.RequestID() == "" {
		t.Error("RequestID() should not be empty")
	}
	if got := rec.Header().Get("X-Request-ID"); got == "" {
		t.Error("X-Request-ID response header should be set")
	}
	if got := rec.Header().Get("X-Request-ID"); got != ctx.RequestID() {
		t.Errorf("response header %q != RequestID() %q", got, ctx.RequestID())
	}
}

// TestContextFromPreservesRequestID verifies that an existing X-Request-ID on
// the inbound request is echoed unchanged.
func TestContextFromPreservesRequestID(t *testing.T) {
	const existing = "test-request-id-123"
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Request-ID", existing)
	rec := httptest.NewRecorder()

	ctx := ContextFrom(rec, req)

	if ctx.RequestID() != existing {
		t.Errorf("RequestID() = %q, want %q", ctx.RequestID(), existing)
	}
	if got := rec.Header().Get("X-Request-ID"); got != existing {
		t.Errorf("response X-Request-ID = %q, want %q", got, existing)
	}
}

// TestContextFromLocale verifies Locale always returns "en" in v1.
func TestContextFromLocale(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	ctx := ContextFrom(rec, req)
	if ctx.Locale() != "en" {
		t.Errorf("Locale() = %q, want \"en\"", ctx.Locale())
	}
}

// TestContextFromGuestUser verifies that an unauthenticated request yields
// the zero User (GuestUser).
func TestContextFromGuestUser(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	ctx := ContextFrom(rec, req)
	u := ctx.User()
	if u.ID != "" || u.Name != "" || len(u.Roles) != 0 {
		t.Errorf("expected GuestUser, got %+v", u)
	}
}

// TestContextFromRequestAndResponse verifies that Request() and Response()
// return the same values passed in.
func TestContextFromRequestAndResponse(t *testing.T) {
	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()
	ctx := ContextFrom(rec, req)

	if ctx.Request() != req {
		t.Error("Request() should return the original *http.Request")
	}
	if ctx.Response() != rec {
		t.Error("Response() should return the original http.ResponseWriter")
	}
}

// TestNewTestContext verifies that NewTestContext returns a fully-populated
// Context with no HTTP server required.
func TestNewTestContext(t *testing.T) {
	user := User{ID: "u1", Name: "Alice", Roles: []Role{Editor}}
	ctx := NewTestContext(user)

	if ctx == nil {
		t.Fatal("NewTestContext() returned nil")
	}
	if got := ctx.User(); got.ID != user.ID || got.Name != user.Name {
		t.Errorf("User() = %+v, want %+v", got, user)
	}
	if ctx.Locale() != "en" {
		t.Errorf("Locale() = %q, want \"en\"", ctx.Locale())
	}
	if ctx.RequestID() == "" {
		t.Error("RequestID() should not be empty")
	}
	if ctx.Request() == nil {
		t.Error("Request() should not be nil")
	}
	if ctx.Response() == nil {
		t.Error("Response() should not be nil")
	}
}

// TestNewTestContextGuest verifies that NewTestContext(User{}) is equivalent
// to an unauthenticated guest context.
func TestNewTestContextGuest(t *testing.T) {
	ctx := NewTestContext(User{})
	u := ctx.User()
	if u.ID != "" || u.Name != "" || len(u.Roles) != 0 {
		t.Errorf("expected GuestUser, got %+v", u)
	}
}

// TestContextImplementsContextContext verifies that smeldr.Context is usable
// anywhere context.Context is accepted (compile-time interface satisfaction).
func TestContextImplementsContextContext(t *testing.T) {
	ctx := NewTestContext(GuestUser)
	// If this compiles, smeldr.Context embeds context.Context correctly.
	_ = context_deadline_helper(ctx)
}

func context_deadline_helper(ctx interface{ Done() <-chan struct{} }) <-chan struct{} {
	return ctx.Done()
}
