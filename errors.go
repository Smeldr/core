package smeldr

import (
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"strings"
	"sync"
)

// Error is implemented by all Forge errors. Callers should use errors.As to
// inspect the concrete type — never type-assert directly against a sentinel.
type Error interface {
	error
	// Code returns a machine-readable error identifier (e.g. "not_found").
	Code() string
	// HTTPStatus returns the HTTP status code that should be sent to the client.
	HTTPStatus() int
	// Public returns a message that is safe to expose to API clients.
	Public() string
}

// sentinelError is the unexported concrete type backing the package-level
// sentinel error variables (ErrNotFound, ErrGone, etc.).
type sentinelError struct {
	code   string
	status int
	public string
}

func (e *sentinelError) Error() string   { return e.public }
func (e *sentinelError) Code() string    { return e.code }
func (e *sentinelError) HTTPStatus() int { return e.status }
func (e *sentinelError) Public() string  { return e.public }

// newSentinel creates a package-level sentinel error. Unexported — callers
// reference the pre-declared vars (ErrNotFound, ErrGone, …).
func newSentinel(status int, code, public string) Error {
	return &sentinelError{code: code, status: status, public: public}
}

// Sentinel errors for well-known HTTP failure conditions.
var (
	// ErrNotFound indicates the requested resource does not exist. → 404
	ErrNotFound = newSentinel(http.StatusNotFound, "not_found", "Not found")

	// ErrGone indicates the resource existed but has been permanently removed. → 410
	ErrGone = newSentinel(http.StatusGone, "gone", "This content has been removed")

	// ErrForbidden indicates the authenticated user lacks permission. → 403
	ErrForbidden = newSentinel(http.StatusForbidden, "forbidden", "Forbidden")

	// ErrUnauth indicates the request requires authentication. → 401
	ErrUnauth = newSentinel(http.StatusUnauthorized, "unauthorized", "Unauthorized")

	// ErrConflict indicates a state conflict (e.g. duplicate slug). → 409
	ErrConflict = newSentinel(http.StatusConflict, "conflict", "Conflict")

	// ErrLastAdmin is returned by [TokenStore.Revoke] when the token being
	// revoked is the last active (non-revoked, non-expired) admin token. → 409
	ErrLastAdmin = newSentinel(http.StatusConflict, "last_admin", "Cannot revoke the last active admin token")

	// ErrBadRequest indicates the request is malformed or unparseable. → 400
	ErrBadRequest = newSentinel(http.StatusBadRequest, "bad_request", "Bad request")

	// ErrNotAcceptable indicates the requested content type is not supported. → 406
	ErrNotAcceptable = newSentinel(http.StatusNotAcceptable, "not_acceptable", "Not acceptable")

	// ErrRequestTooLarge indicates the request body exceeds the allowed size. → 413
	// Returned when a handler reads a body that has been limited by [MaxBodySize]
	// and the limit is exceeded. Users wrapping their own body reads should detect
	// *http.MaxBytesError via errors.As and return this sentinel.
	ErrRequestTooLarge = newSentinel(http.StatusRequestEntityTooLarge, "request_too_large", "Request too large")

	// ErrTooManyRequests indicates the client has exceeded the rate limit. → 429
	ErrTooManyRequests = newSentinel(http.StatusTooManyRequests, "too_many_requests", "Too many requests")

	// ErrInternal indicates an unexpected server-side error. → 500
	// The public message is intentionally generic; details are logged, not exposed.
	ErrInternal = newSentinel(http.StatusInternalServerError, "internal_server_error", "Internal server error")
)

// fieldError holds a single field-level validation failure. Unexported — only
// used as input to the JSON encoder and within ValidationError.
type fieldError struct {
	field   string
	message string
}

// ValidationError is returned when one or more fields fail validation.
// It implements smeldr.Error with HTTP status 422.
//
// Create with [Err] for a single field, or [Require] to collect several.
type ValidationError struct {
	fields []fieldError
}

func (e *ValidationError) HTTPStatus() int { return http.StatusUnprocessableEntity }
func (e *ValidationError) Code() string    { return "validation_failed" }
func (e *ValidationError) Public() string  { return "Validation failed" }

// Error returns a human-readable summary of all validation failures.
func (e *ValidationError) Error() string {
	if len(e.fields) == 1 {
		return fmt.Sprintf("validation failed: %s: %s", e.fields[0].field, e.fields[0].message)
	}
	parts := make([]string, len(e.fields))
	for i, f := range e.fields {
		parts[i] = f.field + ": " + f.message
	}
	return "validation failed: " + strings.Join(parts, ", ")
}

// Err returns a [ValidationError] for a single field. The returned error
// implements smeldr.Error and will produce a 422 response with field details.
//
//	return smeldr.Err("title", "required")
func Err(field, message string) *ValidationError {
	return &ValidationError{fields: []fieldError{{field: field, message: message}}}
}

// Require collects [ValidationError] values from errs into a single
// ValidationError. Nil values are silently skipped. Returns nil if every
// input is nil. Returns the first non-nil non-ValidationError error unchanged.
//
//	return smeldr.Require(
//	    smeldr.Err("title", "required"),
//	    smeldr.Err("body",  "minimum 50 characters"),
//	)
func Require(errs ...error) error {
	var collected []fieldError
	for _, err := range errs {
		if err == nil {
			continue
		}
		var ve *ValidationError
		if errors.As(err, &ve) {
			collected = append(collected, ve.fields...)
		} else {
			// Unexpected non-ValidationError — return it unchanged.
			return err
		}
	}
	if len(collected) == 0 {
		return nil
	}
	return &ValidationError{fields: collected}
}

// WriteError writes the correct HTTP error response for err. It should be the
// only error-to-HTTP translation in handler code — call it and return.
//
// Behaviour by error type:
//   - [*ValidationError]    → 422 with a JSON fields array
//   - [smeldr.Error] 4xx     → the error's own status, code, and public message
//   - [smeldr.Error] 5xx     → logged internally; generic 500 sent to client
//   - any other error       → logged internally; generic 500 sent to client
//
// The X-Request-ID header is echoed from the response (if already set by
// upstream middleware) or from the incoming request. A new ID is never
// generated here — that is [ContextFrom]'s responsibility.
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	// Resolve the request ID from the response headers first (set by
	// ContextFrom), then fall back to the inbound request header.
	requestID := w.Header().Get("X-Request-ID")
	if requestID == "" {
		requestID = r.Header.Get("X-Request-ID")
	}
	if requestID != "" {
		w.Header().Set("X-Request-ID", requestID)
	}

	wantsHTML := strings.Contains(r.Header.Get("Accept"), "text/html")

	var ve *ValidationError
	var fe Error

	switch {
	case errors.As(err, &ve):
		respond(w, r, http.StatusUnprocessableEntity, requestID, ve, wantsHTML)

	case errors.As(err, &fe):
		if fe.HTTPStatus() >= 500 {
			slog.Error("smeldr: internal error",
				"error", err.Error(),
				"request_id", requestID,
			)
			generic500 := newSentinel(http.StatusInternalServerError, "internal_error", "Internal server error")
			respond(w, r, http.StatusInternalServerError, requestID, generic500, wantsHTML)
		} else {
			respond(w, r, fe.HTTPStatus(), requestID, fe, wantsHTML)
		}

	default:
		slog.Error("smeldr: unhandled error",
			"error", err.Error(),
			"request_id", requestID,
		)
		generic500 := newSentinel(http.StatusInternalServerError, "internal_error", "Internal server error")
		respond(w, r, http.StatusInternalServerError, requestID, generic500, wantsHTML)
	}
}

// errorTemplateLookup is set by [bindErrorTemplates] when modules with
// template directories are registered. It searches for errors/{status}.html
// in each registered module's template directory and returns the first match.
// When nil or returning nil, respond falls back to [htmlErrorPage].
//
// errorTemplateLookupMu guards concurrent reads (request goroutines) against
// the single write (App.Handler start-up). Use setErrorTemplateLookup to
// write and runErrorTemplateLookup to read.
var (
	errorTemplateLookupMu sync.RWMutex
	errorTemplateLookup   func(status int) *template.Template
)

// setErrorTemplateLookup stores fn under the write lock. Called once by
// [bindErrorTemplates] during [App.Handler] start-up.
func setErrorTemplateLookup(fn func(int) *template.Template) {
	errorTemplateLookupMu.Lock()
	errorTemplateLookup = fn
	errorTemplateLookupMu.Unlock()
}

// runErrorTemplateLookup executes the lookup function (if set) under the
// read lock and returns the matching template, or nil.
func runErrorTemplateLookup(status int) *template.Template {
	errorTemplateLookupMu.RLock()
	fn := errorTemplateLookup
	errorTemplateLookupMu.RUnlock()
	if fn == nil {
		return nil
	}
	return fn(status)
}

// respond writes a JSON or minimal HTML error response.
func respond(w http.ResponseWriter, _ *http.Request, status int, requestID string, err Error, wantsHTML bool) {
	if wantsHTML {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
		if tpl := runErrorTemplateLookup(status); tpl != nil {
			_ = tpl.Execute(w, struct {
				Status    int
				Message   string
				RequestID string
			}{status, err.Public(), requestID})
			return
		}
		fmt.Fprintf(w, htmlErrorPage, status, err.Public(), requestID)
		return
	}

	body := errorResponse{
		Error: errorBody{
			Code:      err.Code(),
			Message:   err.Public(),
			RequestID: requestID,
		},
	}

	var veFields *ValidationError
	if errors.As(err, &veFields) {
		body.Error.Fields = make([]fieldJSON, len(veFields.fields))
		for i, f := range veFields.fields {
			body.Error.Fields[i] = fieldJSON{Field: f.field, Message: f.message}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// errorResponse is the top-level JSON envelope for all error responses.
type errorResponse struct {
	Error errorBody `json:"error"`
}

// errorBody holds the fields that appear inside the "error" key.
type errorBody struct {
	Code      string      `json:"code"`
	Message   string      `json:"message"`
	RequestID string      `json:"request_id"`
	Fields    []fieldJSON `json:"fields,omitempty"`
}

// fieldJSON is the JSON representation of a single validation field error.
type fieldJSON struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// htmlErrorPage is a minimal fallback HTML template used when Accept: text/html
// and no template directory has been registered (Milestone 3).
// Placeholders: %d = status code, %s = public message, %s = request_id.
const htmlErrorPage = `<!DOCTYPE html>
<html lang="en">
<head><meta charset="utf-8"><title>%d</title></head>
<body><h1>%s</h1><p>Request ID: %s</p></body>
</html>
`
