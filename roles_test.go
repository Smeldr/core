package smeldr

import (
	"errors"
	"testing"
)

// TestRoleLevel verifies built-in roles have the correct levels.
func TestRoleLevel(t *testing.T) {
	tests := []struct {
		role  Role
		level int
	}{
		{Guest, 10},
		{Author, 20},
		{Editor, 30},
		{Admin, 40},
	}
	for _, tc := range tests {
		t.Run(string(tc.role), func(t *testing.T) {
			if got := levelOf(tc.role); got != tc.level {
				t.Errorf("levelOf(%q) = %d, want %d", tc.role, got, tc.level)
			}
		})
	}
}

// TestHasRole verifies the hierarchical permission check.
func TestHasRole(t *testing.T) {
	tests := []struct {
		name     string
		roles    []Role
		required Role
		want     bool
	}{
		// Admin satisfies every level
		{"admin satisfies Admin", []Role{Admin}, Admin, true},
		{"admin satisfies Editor", []Role{Admin}, Editor, true},
		{"admin satisfies Author", []Role{Admin}, Author, true},
		{"admin satisfies Guest", []Role{Admin}, Guest, true},
		// Editor does not reach Admin
		{"editor satisfies Editor", []Role{Editor}, Editor, true},
		{"editor satisfies Author", []Role{Editor}, Author, true},
		{"editor satisfies Guest", []Role{Editor}, Guest, true},
		{"editor does not satisfy Admin", []Role{Editor}, Admin, false},
		// Author does not reach Editor or Admin
		{"author satisfies Author", []Role{Author}, Author, true},
		{"author satisfies Guest", []Role{Author}, Guest, true},
		{"author does not satisfy Editor", []Role{Author}, Editor, false},
		{"author does not satisfy Admin", []Role{Author}, Admin, false},
		// Guest satisfies only Guest
		{"guest satisfies Guest", []Role{Guest}, Guest, true},
		{"guest does not satisfy Author", []Role{Guest}, Author, false},
		// Unknown role satisfies nothing
		{"unknown satisfies nothing", []Role{Role("unknown")}, Guest, false},
		// Multiple roles in slice — highest wins
		{"author+editor satisfies Admin", []Role{Author, Editor}, Admin, false},
		{"author+admin satisfies Admin", []Role{Author, Admin}, Admin, true},
		// Empty slice
		{"empty roles", []Role{}, Editor, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := HasRole(tc.roles, tc.required); got != tc.want {
				t.Errorf("HasRole(%v, %q) = %v, want %v", tc.roles, tc.required, got, tc.want)
			}
		})
	}
}

// TestIsRole verifies the exact-match role check.
func TestIsRole(t *testing.T) {
	tests := []struct {
		name     string
		roles    []Role
		required Role
		want     bool
	}{
		{"exact match Admin", []Role{Admin}, Admin, true},
		{"exact match Editor", []Role{Editor}, Editor, true},
		{"Admin does not satisfy Editor", []Role{Admin}, Editor, false},
		{"Editor does not satisfy Admin", []Role{Editor}, Admin, false},
		{"match in multi-role slice", []Role{Guest, Editor}, Editor, true},
		{"no match in multi-role slice", []Role{Guest, Author}, Editor, false},
		{"empty slice", []Role{}, Admin, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsRole(tc.roles, tc.required); got != tc.want {
				t.Errorf("IsRole(%v, %q) = %v, want %v", tc.roles, tc.required, got, tc.want)
			}
		})
	}
}

// TestNewRole verifies that a custom role is positioned correctly in the
// hierarchy via Above/Below and committed via Register.
func TestNewRole(t *testing.T) {
	// Register a "publisher" role between Author (2) and Editor (3).
	pub, err := NewRole("publisher").Above(Author).Below(Editor).Register()
	if err != nil {
		t.Fatalf("Register() error: %v", err)
	}
	if pub != Role("publisher") {
		t.Errorf("role name = %q, want \"publisher\"", pub)
	}

	got := levelOf(pub)
	if got <= levelOf(Author) {
		t.Errorf("publisher level %d should be > Author level %d", got, levelOf(Author))
	}
	if got >= levelOf(Editor) {
		t.Errorf("publisher level %d should be < Editor level %d", got, levelOf(Editor))
	}

	// HasRole: publisher-level user satisfies Author but not Editor.
	if !HasRole([]Role{pub}, Author) {
		t.Error("publisher should satisfy Author")
	}
	if HasRole([]Role{pub}, Editor) {
		t.Error("publisher should not satisfy Editor")
	}

	// Clean up so other tests are not affected.
	roleMu.Lock()
	delete(roleLevels, pub)
	roleMu.Unlock()
}

// TestRegisterIdempotent verifies that registering the same name+level twice
// is a no-op and does not return an error.
func TestRegisterIdempotent(t *testing.T) {
	const name = "idempotent_test_role"
	r1, err := NewRole(name).Above(Guest).Below(Author).Register()
	if err != nil {
		t.Fatalf("first Register() error: %v", err)
	}
	r2, err := NewRole(name).Above(Guest).Below(Author).Register()
	if err != nil {
		t.Fatalf("second Register() error: %v", err)
	}
	if r1 != r2 {
		t.Errorf("roles differ: %q vs %q", r1, r2)
	}

	// Clean up.
	roleMu.Lock()
	delete(roleLevels, r1)
	roleMu.Unlock()
}

// TestRegisterConflict verifies that registering the same name with a different
// level returns a smeldr.Error (ValidationError).
func TestRegisterConflict(t *testing.T) {
	const name = "conflict_test_role"
	_, err := NewRole(name).Above(Guest).Register()
	if err != nil {
		t.Fatalf("first Register() error: %v", err)
	}

	_, err = NewRole(name).Above(Author).Register()
	if err == nil {
		t.Fatal("expected error registering same name with different level, got nil")
	}

	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Errorf("expected *ValidationError, got %T: %v", err, err)
	}

	// Clean up.
	roleMu.Lock()
	delete(roleLevels, Role(name))
	roleMu.Unlock()
}

// TestModuleOptionStubs verifies Read, Write, and Delete return non-nil Options
// with the correct signal string and role.
func TestModuleOptionStubs(t *testing.T) {
	tests := []struct {
		name       string
		opt        Option
		wantSignal string
		wantRole   Role
	}{
		{"Read", Read(Editor), "read", Editor},
		{"Write", Write(Editor), "write", Editor},
		{"Delete", Delete(Admin), "delete", Admin},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.opt == nil {
				t.Fatal("Option is nil")
			}
			ro, ok := tc.opt.(roleOption)
			if !ok {
				t.Fatalf("expected roleOption, got %T", tc.opt)
			}
			if ro.signal != tc.wantSignal {
				t.Errorf("signal = %q, want %q", ro.signal, tc.wantSignal)
			}
			if ro.role != tc.wantRole {
				t.Errorf("role = %q, want %q", ro.role, tc.wantRole)
			}
		})
	}
}
