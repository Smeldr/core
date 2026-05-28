package smeldr

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestSentinels verifies every sentinel error has the correct HTTP status,
// machine-readable code, public message, and Error() string.
func TestSentinels(t *testing.T) {
	tests := []struct {
		name   string
		err    Error
		status int
		code   string
		public string
	}{
		{"ErrNotFound", ErrNotFound, 404, "not_found", "Not found"},
		{"ErrGone", ErrGone, 410, "gone", "This content has been removed"},
		{"ErrForbidden", ErrForbidden, 403, "forbidden", "Forbidden"},
		{"ErrUnauth", ErrUnauth, 401, "unauthorized", "Unauthorized"},
		{"ErrConflict", ErrConflict, 409, "conflict", "Conflict"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.err.HTTPStatus(); got != tc.status {
				t.Errorf("HTTPStatus() = %d, want %d", got, tc.status)
			}
			if got := tc.err.Code(); got != tc.code {
				t.Errorf("Code() = %q, want %q", got, tc.code)
			}
			if got := tc.err.Public(); got != tc.public {
				t.Errorf("Public() = %q, want %q", got, tc.public)
			}
			if got := tc.err.Error(); got != tc.public {
				t.Errorf("Error() = %q, want %q", got, tc.public)
			}
		})
	}
}

// TestErr verifies that smeldr.Err produces a ValidationError with the correct
// field, message, status, code, and Error() string.
func TestErr(t *testing.T) {
	ve := Err("title", "required")

	if ve == nil {
		t.Fatal("Err returned nil")
	}
	if ve.HTTPStatus() != 422 {
		t.Errorf("HTTPStatus() = %d, want 422", ve.HTTPStatus())
	}
	if ve.Code() != "validation_failed" {
		t.Errorf("Code() = %q, want \"validation_failed\"", ve.Code())
	}
	if len(ve.fields) != 1 {
		t.Fatalf("len(fields) = %d, want 1", len(ve.fields))
	}
	if ve.fields[0].field != "title" {
		t.Errorf("field = %q, want \"title\"", ve.fields[0].field)
	}
	if ve.fields[0].message != "required" {
		t.Errorf("message = %q, want \"required\"", ve.fields[0].message)
	}
	if !strings.Contains(ve.Error(), "title") || !strings.Contains(ve.Error(), "required") {
		t.Errorf("Error() = %q, want it to contain field and message", ve.Error())
	}
}

// TestRequire verifies the Require helper correctly collects, skips, and
// handles edge cases.
func TestRequire(t *testing.T) {
	t.Run("all_nil_returns_nil", func(t *testing.T) {
		if got := Require(nil, nil, nil); got != nil {
			t.Errorf("expected nil, got %v", got)
		}
	})

	t.Run("nil_with_no_args_returns_nil", func(t *testing.T) {
		if got := Require(); got != nil {
			t.Errorf("expected nil, got %v", got)
		}
	})

	t.Run("skips_nils_and_collects_both_errors", func(t *testing.T) {
		got := Require(nil, Err("x", "y"), nil, Err("a", "b"))
		if got == nil {
			t.Fatal("expected non-nil error")
		}
		ve, ok := got.(*ValidationError)
		if !ok {
			t.Fatalf("expected *ValidationError, got %T", got)
		}
		if len(ve.fields) != 2 {
			t.Errorf("len(fields) = %d, want 2", len(ve.fields))
		}
		if ve.fields[0].field != "x" || ve.fields[0].message != "y" {
			t.Errorf("fields[0] = {%q, %q}, want {\"x\", \"y\"}", ve.fields[0].field, ve.fields[0].message)
		}
		if ve.fields[1].field != "a" || ve.fields[1].message != "b" {
			t.Errorf("fields[1] = {%q, %q}, want {\"a\", \"b\"}", ve.fields[1].field, ve.fields[1].message)
		}
	})

	t.Run("single_validation_error", func(t *testing.T) {
		got := Require(Err("email", "invalid"))
		ve, ok := got.(*ValidationError)
		if !ok {
			t.Fatalf("expected *ValidationError, got %T", got)
		}
		if len(ve.fields) != 1 {
			t.Errorf("len(fields) = %d, want 1", len(ve.fields))
		}
	})

	t.Run("non_validation_error_returned_unchanged", func(t *testing.T) {
		sentinel := fmt.Errorf("unexpected")
		got := Require(nil, sentinel)
		if got != sentinel {
			t.Errorf("expected sentinel error, got %v", got)
		}
	})
}

// TestWriteError covers the full dispatch chain in WriteError.
func TestWriteError(t *testing.T) {
	type body struct {
		Error struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			RequestID string `json:"request_id"`
			Fields    []struct {
				Field   string `json:"field"`
				Message string `json:"message"`
			} `json:"fields"`
		} `json:"error"`
	}

	parse := func(t *testing.T, rec *httptest.ResponseRecorder) body {
		t.Helper()
		var b body
		if err := json.NewDecoder(rec.Body).Decode(&b); err != nil {
			t.Fatalf("failed to decode JSON response: %v", err)
		}
		return b
	}

	tests := []struct {
		name           string
		err            error
		wantStatus     int
		wantCode       string
		wantFields     int    // expected number of field errors in response
		wantNoInternal bool   // body must not expose the raw error message
		wantNoMessage  string // body message must not contain this substring
	}{
		{
			name:       "ErrNotFound",
			err:        ErrNotFound,
			wantStatus: 404,
			wantCode:   "not_found",
		},
		{
			name:       "ErrGone",
			err:        ErrGone,
			wantStatus: 410,
			wantCode:   "gone",
		},
		{
			name:       "ErrForbidden",
			err:        ErrForbidden,
			wantStatus: 403,
			wantCode:   "forbidden",
		},
		{
			name:       "ErrUnauth",
			err:        ErrUnauth,
			wantStatus: 401,
			wantCode:   "unauthorized",
		},
		{
			name:       "ErrConflict",
			err:        ErrConflict,
			wantStatus: 409,
			wantCode:   "conflict",
		},
		{
			name:       "ValidationError_single_field",
			err:        Err("title", "required"),
			wantStatus: 422,
			wantCode:   "validation_failed",
			wantFields: 1,
		},
		{
			name:       "ValidationError_multi_field",
			err:        Require(Err("title", "required"), Err("body", "min 50")),
			wantStatus: 422,
			wantCode:   "validation_failed",
			wantFields: 2,
		},
		{
			name:           "unknown_error_returns_500",
			err:            fmt.Errorf("database connection refused"),
			wantStatus:     500,
			wantCode:       "internal_error",
			wantNoInternal: true,
		},
		{
			name:       "wrapped_sentinel_errors_as_unwraps",
			err:        fmt.Errorf("storage: %w", ErrNotFound),
			wantStatus: 404,
			wantCode:   "not_found",
		},
		{
			name:          "5xx_forge_error_suppresses_public_message",
			err:           newSentinel(500, "custom_5xx", "custom five-hundred detail"),
			wantStatus:    500,
			wantCode:      "internal_error",
			wantNoMessage: "custom five-hundred detail",
		},
		{
			name:       "wrapped_validation_error_includes_fields",
			err:        fmt.Errorf("hook: %w", Err("email", "invalid format")),
			wantStatus: 422,
			wantCode:   "validation_failed",
			wantFields: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			rec := httptest.NewRecorder()

			WriteError(rec, req, tc.err)

			if rec.Code != tc.wantStatus {
				t.Errorf("HTTP status = %d, want %d", rec.Code, tc.wantStatus)
			}

			ct := rec.Header().Get("Content-Type")
			if !strings.HasPrefix(ct, "application/json") {
				t.Errorf("Content-Type = %q, want application/json", ct)
			}

			b := parse(t, rec)

			if b.Error.Code != tc.wantCode {
				t.Errorf("code = %q, want %q", b.Error.Code, tc.wantCode)
			}
			if got := len(b.Error.Fields); got != tc.wantFields {
				t.Errorf("len(fields) = %d, want %d", got, tc.wantFields)
			}
			if tc.wantNoInternal && strings.Contains(b.Error.Message, "database") {
				t.Errorf("internal error detail leaked in message: %q", b.Error.Message)
			}
			if tc.wantNoMessage != "" && strings.Contains(b.Error.Message, tc.wantNoMessage) {
				t.Errorf("suppressed message leaked in response: %q", b.Error.Message)
			}
		})
	}
}

// TestWriteError_RequestID verifies that the X-Request-ID from the incoming
// request is echoed in the response header.
func TestWriteError_RequestID(t *testing.T) {
	const id = "test-request-id-123"

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-ID", id)
	rec := httptest.NewRecorder()

	WriteError(rec, req, ErrNotFound)

	if got := rec.Header().Get("X-Request-ID"); got != id {
		t.Errorf("X-Request-ID = %q, want %q", got, id)
	}

	// The request ID must also appear in the JSON response body.
	var b struct {
		Error struct {
			RequestID string `json:"request_id"`
		} `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&b); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if b.Error.RequestID != id {
		t.Errorf("body request_id = %q, want %q", b.Error.RequestID, id)
	}
}

// TestWriteError_HTML verifies that when Accept: text/html is set, the
// response is HTML and not JSON.
func TestWriteError_HTML(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept", "text/html")
	rec := httptest.NewRecorder()

	WriteError(rec, req, ErrNotFound)

	if rec.Code != 404 {
		t.Errorf("status = %d, want 404", rec.Code)
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	if !strings.Contains(rec.Body.String(), "Not found") {
		t.Errorf("HTML body missing public message: %q", rec.Body.String())
	}
}

// TestWriteError_RequestID_responseHeader verifies that the X-Request-ID
// already present on the response header takes priority over the value
// in the request header. This mirrors the RequestLogger+ContextFrom path
// where the ID is set on the response before any handler runs.
func TestWriteError_RequestID_responseHeader(t *testing.T) {
	const responseID = "from-response-header"
	const requestID = "from-request-header"

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-ID", requestID)
	rec := httptest.NewRecorder()

	// Simulate what ContextFrom/RequestLogger sets on the response writer.
	rec.Header().Set("X-Request-ID", responseID)

	WriteError(rec, req, ErrNotFound)

	if got := rec.Header().Get("X-Request-ID"); got != responseID {
		t.Errorf("X-Request-ID = %q, want response-header value %q", got, responseID)
	}

	var b struct {
		Error struct {
			RequestID string `json:"request_id"`
		} `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&b); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if b.Error.RequestID != responseID {
		t.Errorf("body request_id = %q, want %q", b.Error.RequestID, responseID)
	}
}

// TestSentinels_new verifies the four sentinels added in Amendment A29.
func TestSentinels_new(t *testing.T) {
	tests := []struct {
		name   string
		err    Error
		status int
		code   string
	}{
		{"ErrBadRequest", ErrBadRequest, 400, "bad_request"},
		{"ErrNotAcceptable", ErrNotAcceptable, 406, "not_acceptable"},
		{"ErrRequestTooLarge", ErrRequestTooLarge, 413, "request_too_large"},
		{"ErrTooManyRequests", ErrTooManyRequests, 429, "too_many_requests"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.err.HTTPStatus(); got != tc.status {
				t.Errorf("HTTPStatus() = %d, want %d", got, tc.status)
			}
			if got := tc.err.Code(); got != tc.code {
				t.Errorf("Code() = %q, want %q", got, tc.code)
			}
			if tc.err.Public() == "" {
				t.Error("Public() must not be empty")
			}
		})
	}
}
