# Forge — Error Handling Strategy

This document is the authoritative reference for how errors are produced,
propagated, and rendered in Forge. It supplements Decision 16 in `DECISIONS.md`.

Read this document before writing any code that: handles or returns errors,
calls `WriteError`, adds a new sentinel, uses `errors.As`/`errors.Is`,
or writes an HTTP response in an error path.

---

## The single pipeline rule

Every error-to-HTTP translation in Forge goes through one function:

```go
WriteError(w http.ResponseWriter, r *http.Request, err error)
```

**No handler, middleware, or helper may call `http.Error`, `w.WriteHeader` +
`w.Write`, or `fmt.Fprintf` in an error path.** If you are returning an error
response, you must call `WriteError`. No exceptions.

This rule exists so that:
- `X-Request-ID` is always echoed on error responses
- Internal details are never leaked to clients
- Error responses are always in the format the client negotiated (JSON or HTML)
- All error events are logged consistently via `slog.Error`

---

## Error tiers

### Tier 1 — Sentinel errors (4xx domain conditions)

Use a sentinel when the condition is a named, well-understood HTTP failure.

```go
WriteError(w, r, smeldr.ErrNotFound)          // 404
WriteError(w, r, smeldr.ErrGone)              // 410
WriteError(w, r, smeldr.ErrForbidden)         // 403
WriteError(w, r, smeldr.ErrUnauth)            // 401
WriteError(w, r, smeldr.ErrConflict)          // 409
WriteError(w, r, smeldr.ErrBadRequest)        // 400
WriteError(w, r, smeldr.ErrNotAcceptable)     // 406
WriteError(w, r, smeldr.ErrTooManyRequests)   // 429
WriteError(w, r, smeldr.ErrRequestTooLarge)   // 413
```

Sentinels are `smeldr.Error` values. They produce a JSON body with `code`,
`message`, and `request_id`. They produce an HTML page when the request
carries `Accept: text/html`.

#### When to add a new sentinel

Add a sentinel when all three hold:
1. The condition has a named HTTP status code
2. The condition is produced by framework code (not only application code)
3. The public message is generic enough to be safe for all clients

Never add a sentinel that exposes application-specific detail. That belongs
in a `ValidationError` field message.

### Tier 2 — Validation errors (422)

Use a validation error when user-supplied input fails a named constraint.

```go
// Single field
return smeldr.Err("title", "required")

// Multiple fields — collect, skip nils, return combined or nil
return smeldr.Require(
    smeldr.Err("title", "required"),
    smeldr.Err("body",  "minimum 50 characters"),
)
```

`WriteError` detects `*ValidationError` via `errors.As` and adds a `fields`
array to the response body:

```json
{
  "error": {
    "code": "validation_failed",
    "message": "Validation failed",
    "request_id": "...",
    "fields": [
      { "field": "title", "message": "required" },
      { "field": "body",  "message": "minimum 50 characters" }
    ]
  }
}
```

### Tier 3 — Internal / unknown errors (500)

Any error that is not a `smeldr.Error` or `*ValidationError` is treated as
an internal error. `WriteError` logs the original message with `slog.Error`
(including `request_id`) and sends a generic 500 to the client. The original
message is **never** sent to the client.

```go
// Handler receives a database error, storage error, or unexpected failure:
if err := m.repo.Save(ctx, item); err != nil {
    WriteError(w, r, err)   // logged internally; client sees generic 500
    return
}
```

If you need to wrap an internal error with context, preserve the chain:

```go
return fmt.Errorf("forge: saving post: %w", err)
```

`WriteError` unwraps via `errors.As`, so a wrapped `smeldr.Error` still
produces the correct 4xx response.

### Tier 4 — Panic recovery

`Recoverer()` middleware catches all panics, logs the stack trace, and
calls `WriteError` with a 500 sentinel. The process is never crashed.
Placing `Recoverer()` as the outermost middleware is required — it must
wrap all other handlers to catch panics anywhere in the chain.

---

## `errors.As` — always, no type assertions

When inspecting an error to decide behaviour, always use `errors.As`:

```go
// Correct
var ve *ValidationError
if errors.As(err, &ve) { ... }

var fe smeldr.Error
if errors.As(err, &fe) { ... }

// Wrong — breaks wrapping
if ve, ok := err.(*ValidationError); ok { ... }
if fe, ok := err.(smeldr.Error); ok { ... }
```

The rule applies everywhere in the codebase, including inside `errors.go`.

---

## Sentinel construction

Sentinels are constructed with the unexported `newSentinel` helper. They may
only be constructed in `errors.go`. **Do not call `newSentinel` in any other
file.** Use the package-level `Err*` variables.

If a one-off status is genuinely needed in a single location (e.g. inside
an intermediate helper that cannot call `WriteError` directly), create a new
sentinel variable in `errors.go` and reference it from the call site. Do not
inline `newSentinel(...)` in handler code.

---

## X-Request-ID

Every request receives a `X-Request-ID`. The ID is generated by `ContextFrom`
on the first call and written to the response header. `WriteError` reads the
ID from the response header (preferred) or the request header (fallback) — it
never generates a new one.

Consequences:
- `RequestLogger` must be the outermost middleware so `ContextFrom` runs first
- All error log lines emitted via `slog.Error` must include `"request_id"` as a field
- The request ID appears in all three error surfaces: response header, JSON body, logs

---

## HTML vs JSON error responses

`WriteError` respects `Accept: text/html`:
- If the request prefers HTML and a `templates/errors/{status}.html` exists, it renders it
- If no template is registered, it renders a minimal built-in HTML error page
- All other requests (including JSON-only APIs) receive `application/json`

---

## Checklist for handler authors

Before submitting any handler code, verify:

- [ ] All error paths call `WriteError(w, r, err)` — no bare `http.Error`
- [ ] `WriteError` is always followed immediately by `return`
- [ ] Internal errors (DB, storage, hook) are passed to `WriteError` unwrapped
      — `WriteError` handles logging; do not log before calling it
- [ ] `errors.As` is used for all error type checks — no direct type assertions
- [ ] New named conditions use a `smeldr.Err*` sentinel, not an ad-hoc `errors.New`
- [ ] `WriteError` is never called after `w.WriteHeader` has already been called
      (headers can't be changed after the first write)

---

## Known gaps (fixed in v1.0.1 — Amendments A29–A32)

The following violations existed in v1.0.0 and were repaired in v1.0.1:

| File | Location | Violation | Amendment | Status |
|------|----------|-----------|-----------|--------|
| `errors.go` | `respond` | Direct type assertion `err.(*ValidationError)` instead of `errors.As` | A29 | ✅ Fixed |
| `errors.go` | `errorTemplateLookup` | Bare `var` — data race risk under parallel tests | A29 | ✅ Fixed |
| `errors.go` | sentinel vars | Missing sentinels for 400, 406, 413, 429 | A29 | ✅ Fixed |
| `module.go` | `writeContent` | `http.Error` for 406 — no `r *http.Request` available | A30 | ✅ Fixed |
| `module.go` | `createHandler`, `updateHandler` | `http.Error` for 400 on JSON decode; `*http.MaxBytesError` (413) mapped to 400 | A30 | ✅ Fixed |
| `templates.go` | `renderListHTML`, `renderShowHTML` | `http.Error` for 406 | A31 | ✅ Fixed |
| `middleware.go` | `RateLimit` | `http.Error` for 429 | A32 | ✅ Fixed |
| `middleware.go` | `Recoverer` | 4096-byte stack buffer silently truncated deep stacks | A32 | ✅ Fixed |
