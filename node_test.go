package forge

import (
	"strings"
	"testing"
)

// TestNewID verifies UUID v7 format: 36 chars, correct hyphen positions,
// version nibble = '7', variant bits in [89ab], and no duplicates.
func TestNewID(t *testing.T) {
	const count = 1000
	seen := make(map[string]struct{}, count)

	for i := 0; i < count; i++ {
		id := NewID()

		if len(id) != 36 {
			t.Fatalf("NewID() len = %d, want 36", len(id))
		}
		for _, pos := range []int{8, 13, 18, 23} {
			if id[pos] != '-' {
				t.Fatalf("NewID()[%d] = %q, want '-'", pos, id[pos])
			}
		}
		// Version nibble must be '7' (position 14 in string).
		if id[14] != '7' {
			t.Fatalf("NewID() version = %q, want '7'", id[14])
		}
		// Variant bits: first hex digit of 4th group must be 8, 9, a, or b.
		v := id[19]
		if v != '8' && v != '9' && v != 'a' && v != 'b' {
			t.Fatalf("NewID() variant = %q, want [89ab]", v)
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("NewID() duplicate: %s", id)
		}
		seen[id] = struct{}{}
	}
}

// TestGenerateSlug verifies slug normalisation: lowercase, whitelist, max
// length, hyphen collapsing, empty input fallback.
func TestGenerateSlug(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Hello World", "hello-world"},
		{"Go 1.22!", "go-122"},
		{"  --leading", "leading"},
		{"trailing--  ", "trailing"},
		{"a/b/c", "abc"},
		{strings.Repeat("a", 250), strings.Repeat("a", 200)},
		{"", "untitled"},
		{"   ", "untitled"},
		{"café", "caf"},
		{"multiple   spaces", "multiple-spaces"},
		{"--hyphen--", "hyphen"},
		{"UPPER CASE", "upper-case"},
		{"slug-already-good", "slug-already-good"},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got := GenerateSlug(tc.input)
			if got != tc.want {
				t.Errorf("GenerateSlug(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// TestUniqueSlug verifies suffix appending when the base slug is taken.
func TestUniqueSlug(t *testing.T) {
	tests := []struct {
		name  string
		taken map[string]bool
		base  string
		want  string
	}{
		{
			name:  "no collision",
			taken: map[string]bool{},
			base:  "post",
			want:  "post",
		},
		{
			name:  "one collision",
			taken: map[string]bool{"post": true},
			base:  "post",
			want:  "post-2",
		},
		{
			name:  "five collisions",
			taken: map[string]bool{"post": true, "post-2": true, "post-3": true, "post-4": true, "post-5": true},
			base:  "post",
			want:  "post-6",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := UniqueSlug(tc.base, func(s string) bool { return tc.taken[s] })
			if got != tc.want {
				t.Errorf("UniqueSlug(%q) = %q, want %q", tc.base, got, tc.want)
			}
		})
	}
}

// ── validation constraint tests ─────────────────────────────────────────────

// TestRunValidationRequired verifies the required constraint.
func TestRunValidationRequired(t *testing.T) {
	type S struct {
		Title string `forge:"required"`
	}
	if err := RunValidation(&S{Title: "hello"}); err != nil {
		t.Errorf("expected nil for non-empty, got %v", err)
	}
	err := RunValidation(&S{})
	if err == nil {
		t.Fatal("expected error for empty Title")
	}
	var ve *ValidationError
	if !isValidationError(err, &ve) {
		t.Fatalf("expected *ValidationError, got %T", err)
	}
	if len(ve.fields) != 1 || ve.fields[0].field != "Title" {
		t.Errorf("unexpected fields: %v", ve.fields)
	}
}

// TestRunValidationMin verifies the min= constraint on strings and ints.
func TestRunValidationMin(t *testing.T) {
	type S struct {
		Body string `forge:"min=10"`
	}
	if err := RunValidation(&S{Body: "hello world"}); err != nil {
		t.Errorf("expected nil for long body, got %v", err)
	}
	if err := RunValidation(&S{Body: "hi"}); err == nil {
		t.Error("expected error for too-short body")
	}
	// empty string also fails min — use required+min together
	if err := RunValidation(&S{Body: ""}); err == nil {
		t.Error("empty string should fail min=10")
	}
}

// TestRunValidationMax verifies the max= constraint on strings.
func TestRunValidationMax(t *testing.T) {
	type S struct {
		Name string `forge:"max=5"`
	}
	if err := RunValidation(&S{Name: "hi"}); err != nil {
		t.Errorf("expected nil for short name, got %v", err)
	}
	if err := RunValidation(&S{Name: "toolongname"}); err == nil {
		t.Error("expected error for too-long name")
	}
}

// TestRunValidationEmail verifies the email constraint.
func TestRunValidationEmail(t *testing.T) {
	type S struct {
		Email string `forge:"email"`
	}
	tests := []struct {
		value   string
		wantErr bool
	}{
		{"user@example.com", false},
		{"a@b.co", false},
		{"", false}, // empty passes; use required+email to forbid
		{"notanemail", true},
		{"@nodomain", true}, // no local part
		{"noat", true},
		{"double@@at.com", true},
	}
	for _, tc := range tests {
		t.Run(tc.value, func(t *testing.T) {
			err := RunValidation(&S{Email: tc.value})
			if (err != nil) != tc.wantErr {
				t.Errorf("email=%q: err=%v, wantErr=%v", tc.value, err, tc.wantErr)
			}
		})
	}
}

// TestRunValidationURL verifies the url constraint.
func TestRunValidationURL(t *testing.T) {
	type S struct {
		Link string `forge:"url"`
	}
	tests := []struct {
		value   string
		wantErr bool
	}{
		{"https://example.com", false},
		{"http://localhost:8080/path", false},
		{"", false}, // empty passes; use required+url to forbid
		{"not-a-url", true},
		{"//no-scheme.com", true},
		{"ftp://files.example.com", false},
	}
	for _, tc := range tests {
		t.Run(tc.value, func(t *testing.T) {
			err := RunValidation(&S{Link: tc.value})
			if (err != nil) != tc.wantErr {
				t.Errorf("url=%q: err=%v, wantErr=%v", tc.value, err, tc.wantErr)
			}
		})
	}
}

// TestRunValidationSlug verifies the slug constraint.
func TestRunValidationSlug(t *testing.T) {
	type S struct {
		Slug string `forge:"slug"`
	}
	if err := RunValidation(&S{Slug: "valid-slug-123"}); err != nil {
		t.Errorf("expected nil, got %v", err)
	}
	if err := RunValidation(&S{Slug: ""}); err == nil {
		t.Error("empty slug should fail")
	}
	for _, bad := range []string{"Has Spaces", "UPPER", "with/slash"} {
		if err := RunValidation(&S{Slug: bad}); err == nil {
			t.Errorf("expected error for slug %q", bad)
		}
	}
}

// TestRunValidationOneOf verifies the oneof= constraint using | separator.
func TestRunValidationOneOf(t *testing.T) {
	type S struct {
		Status string `forge:"oneof=draft|published|archived"`
	}
	for _, good := range []string{"draft", "published", "archived"} {
		if err := RunValidation(&S{Status: good}); err != nil {
			t.Errorf("expected nil for %q, got %v", good, err)
		}
	}
	if err := RunValidation(&S{Status: "unknown"}); err == nil {
		t.Error("expected error for invalid status")
	}
}

// TestRunValidationMultiConstraint verifies multiple constraints on one field
// and multiple errors collected without short-circuit.
func TestRunValidationMultiConstraint(t *testing.T) {
	type S struct {
		Title string `forge:"required,min=3,max=50"`
		Body  string `forge:"required,min=10"`
	}
	// Both fields empty — expect two errors.
	err := RunValidation(&S{})
	var ve *ValidationError
	if !isValidationError(err, &ve) {
		t.Fatalf("expected *ValidationError, got %T: %v", err, err)
	}
	if len(ve.fields) < 2 {
		t.Errorf("expected at least 2 field errors, got %d: %v", len(ve.fields), ve.fields)
	}
}

// TestRunValidationUnknownTagPanics verifies that an unrecognised forge tag
// key causes a panic on first use — fail-fast at startup.
func TestRunValidationUnknownTagPanics(t *testing.T) {
	type Bad struct {
		X string `forge:"doesnotexist"`
	}
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for unknown tag, got none")
		}
	}()
	_ = RunValidation(&Bad{})
}

// ── RunValidation tests ───────────────────────────────────────────────────────

// validateSpy is a Validatable that records whether Validate() was called.
type validateSpy struct {
	Title     string `forge:"required"`
	called    bool
	returnErr error
}

func (s *validateSpy) Validate() error {
	s.called = true
	return s.returnErr
}

// TestRunValidation covers the three paths defined in Decision 10.
func TestRunValidation(t *testing.T) {
	t.Run("tags fail — Validate not called", func(t *testing.T) {
		spy := &validateSpy{} // Title empty → required fails
		err := RunValidation(spy)
		if err == nil {
			t.Fatal("expected error")
		}
		if spy.called {
			t.Error("Validate() should not be called when tags fail")
		}
	})

	t.Run("tags pass — Validate error returned", func(t *testing.T) {
		spy := &validateSpy{Title: "ok", returnErr: Err("body", "required")}
		err := RunValidation(spy)
		if err == nil {
			t.Fatal("expected error from Validate()")
		}
		if !spy.called {
			t.Error("Validate() should be called when tags pass")
		}
	})

	t.Run("tags pass — Validate nil — RunValidation nil", func(t *testing.T) {
		spy := &validateSpy{Title: "ok"}
		if err := RunValidation(spy); err != nil {
			t.Errorf("expected nil, got %v", err)
		}
		if !spy.called {
			t.Error("Validate() should be called when tags pass")
		}
	})
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// isValidationError uses errors.As to check and unwrap a *ValidationError.
func isValidationError(err error, target **ValidationError) bool {
	if err == nil {
		return false
	}
	vErr, ok := err.(*ValidationError)
	if ok && target != nil {
		*target = vErr
	}
	return ok
}

// ── Benchmarks ────────────────────────────────────────────────────────────────

type benchStruct struct {
	Title string `forge:"required,min=3,max=100"`
	Body  string `forge:"required,min=50"`
	Email string `forge:"email"`
	Slug  string `forge:"slug"`
}

// BenchmarkValidateStructCached measures the cached reflection path (all runs
// after the first). The first run populates the cache; subsequent runs are
// pure slice iteration — no reflection.
func BenchmarkValidateStructCached(b *testing.B) {
	v := &benchStruct{
		Title: "Hello World",
		Body:  strings.Repeat("x", 60),
		Email: "user@example.com",
		Slug:  "hello-world",
	}
	// Warm the cache.
	_ = validateStruct(v)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = validateStruct(v)
	}
}
