package smeldr

import (
	"crypto/rand"
	"fmt"
	"io"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Status is the content lifecycle state. All content types embed [Node] and
// therefore always carry a Status. Forge enforces lifecycle rules on all public
// endpoints — non-Published content is never publicly visible.
type Status string

const (
	// Draft is the default state for newly created content. Not publicly visible.
	Draft Status = "draft"

	// Published content is publicly visible and included in sitemaps, feeds,
	// and AI indexes.
	Published Status = "published"

	// Scheduled content will be automatically transitioned to Published at
	// [Node.ScheduledAt]. Not publicly visible until the transition fires.
	Scheduled Status = "scheduled"

	// Archived content has been retired. Not publicly visible. Does not appear
	// in sitemaps or feeds. Returns 410 Gone from public endpoints.
	Archived Status = "archived"
)

// Node is the base type embedded by every Forge content type. It carries the
// stable UUID identity, the URL slug, and the full content lifecycle.
//
// Content types must embed Node as a value (not a pointer):
//
//	type BlogPost struct {
//	    smeldr.Node
//	    Title string `smeldr:"required"`
//	    Body  string `smeldr:"required,min=50"`
//	}
//
// Never store a Node by pointer inside your content type — the storage and
// validation layers require a contiguous struct layout.
type Node struct {
	// ID is the UUID v7 primary key. Set by the storage layer on insert;
	// immutable thereafter. See [NewID] and Amendment S1.
	ID string

	// Slug is the URL-safe identifier used in all public URLs. Unique within
	// a module. Auto-generated from the first required string field if not
	// set explicitly. May be changed; the old URL should redirect.
	Slug string

	// Status is the lifecycle state. Forge enforces this on every public
	// endpoint. See Decision 14.
	Status Status

	// PublishedAt is the time the content was first published. Zero until
	// the first transition to Published.
	PublishedAt time.Time `db:"published_at"`

	// ScheduledAt is the time at which a Scheduled item will be published.
	// Nil for all other lifecycle states.
	ScheduledAt *time.Time `db:"scheduled_at"`

	// CreatedAt is set by the storage layer on insert and never updated.
	CreatedAt time.Time `db:"created_at"`

	// UpdatedAt is set by the storage layer on every Save.
	UpdatedAt time.Time `db:"updated_at"`
}

// GetSlug returns the URL slug for this node. Satisfies the [SitemapNode]
// constraint, enabling generic sitemap generation without reflection.
func (n *Node) GetSlug() string { return n.Slug }

// GetPublishedAt returns the time this node was first published.
// The zero time indicates the node has never been published.
func (n *Node) GetPublishedAt() time.Time { return n.PublishedAt }

// GetStatus returns the lifecycle status of this node.
func (n *Node) GetStatus() Status { return n.Status }

// NewID returns a new UUID v7 string. UUID v7 is time-ordered (48-bit
// millisecond timestamp) with 74 bits of cryptographic randomness, which
// keeps B-tree indexes compact while providing the same collision resistance
// as UUID v4. See Amendment S1.
//
// Panics if [crypto/rand] is unavailable — this indicates an unrecoverable
// platform error and should never occur in practice.
func NewID() string {
	var b [16]byte

	ms := uint64(time.Now().UnixMilli())
	b[0] = byte(ms >> 40)
	b[1] = byte(ms >> 32)
	b[2] = byte(ms >> 24)
	b[3] = byte(ms >> 16)
	b[4] = byte(ms >> 8)
	b[5] = byte(ms)

	if _, err := io.ReadFull(rand.Reader, b[6:]); err != nil {
		panic("smeldr: crypto/rand unavailable: " + err.Error())
	}

	// Version 7: high nibble of byte 6.
	b[6] = (b[6] & 0x0f) | 0x70
	// Variant 10xxxxxx: high two bits of byte 8.
	b[8] = (b[8] & 0x3f) | 0x80

	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// GenerateSlug converts input into a URL-safe slug. The algorithm:
//  1. Lowercase (Unicode-aware)
//  2. Spaces, hyphens, and underscores become hyphens
//  3. All other non-[a-z0-9] bytes are dropped
//  4. Consecutive hyphens are collapsed to one
//  5. Leading and trailing hyphens are trimmed
//  6. Result is truncated to 200 bytes
//
// Returns "untitled" if the result would be empty.
//
// The implementation uses a byte loop — no regexp — to avoid allocations on the
// hot path.
func GenerateSlug(input string) string {
	s := strings.ToLower(input)
	buf := make([]byte, 0, min(len(s), 200))
	prevHyphen := true // suppress leading hyphens

	for i := 0; i < len(s) && len(buf) < 200; i++ {
		c := s[i]
		switch {
		case (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9'):
			buf = append(buf, c)
			prevHyphen = false
		case c == ' ' || c == '-' || c == '_':
			if !prevHyphen {
				buf = append(buf, '-')
				prevHyphen = true
			}
		}
	}

	// Trim trailing hyphen.
	for len(buf) > 0 && buf[len(buf)-1] == '-' {
		buf = buf[:len(buf)-1]
	}

	if len(buf) == 0 {
		return "untitled"
	}
	return string(buf)
}

// UniqueSlug returns base if exists(base) is false, otherwise tries base-2,
// base-3, … until exists returns false. Callers must ensure the namespace is
// finite; this function has no upper bound.
func UniqueSlug(base string, exists func(string) bool) string {
	if !exists(base) {
		return base
	}
	for i := 2; ; i++ {
		candidate := base + "-" + strconv.Itoa(i)
		if !exists(candidate) {
			return candidate
		}
	}
}

// ── Reflection-cached tag validation ─────────────────────────────────────────

// fieldConstraint holds the parsed validation rules for one struct field.
// Built once per type and stored in typeCache.
type fieldConstraint struct {
	index    int
	name     string
	checkers []func(reflect.Value) *fieldError
}

// typeCache stores []fieldConstraint keyed by reflect.Type.
// Populated on first call to validateStruct for each type.
var typeCache sync.Map

// parseConstraints inspects all fields of t for `smeldr:"..."` tags and returns
// the compiled constraint slice. Panics on unrecognised tag keys so that
// misconfigured types are caught at startup, not at runtime.
func parseConstraints(t reflect.Type) []fieldConstraint {
	var out []fieldConstraint
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("smeldr")
		if tag == "" {
			continue
		}
		fc := fieldConstraint{index: i, name: f.Name}
		for _, part := range strings.Split(tag, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			switch {
			case part == "required":
				fc.checkers = append(fc.checkers, makeRequired(f.Name))
			case strings.HasPrefix(part, "min="):
				n, err := strconv.Atoi(strings.TrimPrefix(part, "min="))
				if err != nil {
					panic("smeldr: invalid min= tag on " + t.Name() + "." + f.Name)
				}
				fc.checkers = append(fc.checkers, makeMin(f.Name, n))
			case strings.HasPrefix(part, "max="):
				n, err := strconv.Atoi(strings.TrimPrefix(part, "max="))
				if err != nil {
					panic("smeldr: invalid max= tag on " + t.Name() + "." + f.Name)
				}
				fc.checkers = append(fc.checkers, makeMax(f.Name, n))
			case part == "email":
				fc.checkers = append(fc.checkers, makeEmail(f.Name))
			case part == "url":
				fc.checkers = append(fc.checkers, makeURL(f.Name))
			case part == "slug":
				fc.checkers = append(fc.checkers, makeSlugCheck(f.Name))
			case strings.HasPrefix(part, "oneof="):
				opts := strings.Split(strings.TrimPrefix(part, "oneof="), "|")
				fc.checkers = append(fc.checkers, makeOneOf(f.Name, opts))
			default:
				panic("smeldr: unrecognised tag constraint " + strconv.Quote(part) +
					" on " + t.Name() + "." + f.Name)
			}
		}
		if len(fc.checkers) > 0 {
			out = append(out, fc)
		}
	}
	return out
}

func makeRequired(name string) func(reflect.Value) *fieldError {
	return func(v reflect.Value) *fieldError {
		if v.IsZero() {
			return &fieldError{field: name, message: "required"}
		}
		return nil
	}
}

func makeMin(name string, n int) func(reflect.Value) *fieldError {
	return func(v reflect.Value) *fieldError {
		switch v.Kind() { //nolint:exhaustive
		case reflect.String:
			if len(v.String()) < n {
				return &fieldError{field: name, message: "too short (min " + strconv.Itoa(n) + ")"}
			}
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			if v.Int() < int64(n) {
				return &fieldError{field: name, message: "too small (min " + strconv.Itoa(n) + ")"}
			}
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			if v.Uint() < uint64(n) {
				return &fieldError{field: name, message: "too small (min " + strconv.Itoa(n) + ")"}
			}
		case reflect.Float32, reflect.Float64:
			if v.Float() < float64(n) {
				return &fieldError{field: name, message: "too small (min " + strconv.Itoa(n) + ")"}
			}
		}
		return nil
	}
}

func makeMax(name string, n int) func(reflect.Value) *fieldError {
	return func(v reflect.Value) *fieldError {
		switch v.Kind() { //nolint:exhaustive
		case reflect.String:
			if len(v.String()) > n {
				return &fieldError{field: name, message: "too long (max " + strconv.Itoa(n) + ")"}
			}
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			if v.Int() > int64(n) {
				return &fieldError{field: name, message: "too large (max " + strconv.Itoa(n) + ")"}
			}
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			if v.Uint() > uint64(n) {
				return &fieldError{field: name, message: "too large (max " + strconv.Itoa(n) + ")"}
			}
		case reflect.Float32, reflect.Float64:
			if v.Float() > float64(n) {
				return &fieldError{field: name, message: "too large (max " + strconv.Itoa(n) + ")"}
			}
		}
		return nil
	}
}

func makeEmail(name string) func(reflect.Value) *fieldError {
	return func(v reflect.Value) *fieldError {
		s := v.String()
		if s == "" {
			return nil // let required handle empty
		}
		at := strings.IndexByte(s, '@')
		if at <= 0 || at == len(s)-1 {
			return &fieldError{field: name, message: "invalid email address"}
		}
		domain := s[at+1:]
		if strings.Contains(domain, "@") || !strings.Contains(domain, ".") {
			return &fieldError{field: name, message: "invalid email address"}
		}
		return nil
	}
}

func makeURL(name string) func(reflect.Value) *fieldError {
	return func(v reflect.Value) *fieldError {
		s := v.String()
		if s == "" {
			return nil // let required handle empty
		}
		u, err := url.Parse(s)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return &fieldError{field: name, message: "invalid URL"}
		}
		return nil
	}
}

func makeSlugCheck(name string) func(reflect.Value) *fieldError {
	return func(v reflect.Value) *fieldError {
		s := v.String()
		if s == "" {
			return &fieldError{field: name, message: "required"}
		}
		for i := 0; i < len(s); i++ {
			c := s[i]
			if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-') {
				return &fieldError{field: name, message: "invalid slug (use a-z, 0-9, -)"}
			}
		}
		return nil
	}
}

func makeOneOf(name string, opts []string) func(reflect.Value) *fieldError {
	return func(v reflect.Value) *fieldError {
		s := v.String()
		for _, o := range opts {
			if s == o {
				return nil
			}
		}
		return &fieldError{field: name, message: "must be one of: " + strings.Join(opts, ", ")}
	}
}

// ── Public validation API ─────────────────────────────────────────────────────

// validateStruct runs struct-tag validation on v. v must be a struct or a
// pointer to a struct. Field constraints are parsed once per type and cached.
//
// Returns a [*ValidationError] if any constraint fails, otherwise nil.
// Returns all field errors — does not short-circuit on the first failure.
func validateStruct(v any) error {
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Pointer {
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		panic("smeldr: RunValidation requires a struct or pointer to struct")
	}
	rt := rv.Type()

	val, ok := typeCache.Load(rt)
	if !ok {
		parsed := parseConstraints(rt)
		val, _ = typeCache.LoadOrStore(rt, parsed)
	}
	constraints := val.([]fieldConstraint) //nolint:forcetypeassert

	var ve *ValidationError
	for _, fc := range constraints {
		fv := rv.Field(fc.index)
		for _, checker := range fc.checkers {
			if fe := checker(fv); fe != nil {
				if ve == nil {
					ve = &ValidationError{}
				}
				ve.fields = append(ve.fields, *fe)
			}
		}
	}
	if ve != nil {
		return ve
	}
	return nil
}

// Validatable is implemented by content types that have business-rule validation
// beyond struct-tag constraints. [RunValidation] calls Validate() after tag
// validation passes — if tags fail, Validate() is not called.
//
//	func (p *BlogPost) Validate() error {
//	    if p.Status == smeldr.Published && len(p.Tags) == 0 {
//	        return smeldr.Err("tags", "required when publishing")
//	    }
//	    return nil
//	}
type Validatable interface {
	Validate() error
}

// RunValidation runs the full validation pipeline on v:
//  1. Struct-tag constraints (required, min, max, email, …)
//  2. If tags pass and v implements [Validatable], calls v.Validate()
//
// If step 1 fails, step 2 is skipped — the caller receives only the tag errors.
// This matches Decision 10: "Tag validation runs before Validate(); if tags
// fail, Validate() is not called."
func RunValidation(v any) error {
	if err := validateStruct(v); err != nil {
		return err
	}
	if val, ok := v.(Validatable); ok {
		return val.Validate()
	}
	return nil
}
