package smeldr

import "sync"

// Role is a named permission level. The four built-in roles cover most
// applications; custom roles can be registered via [NewRole].
//
// Roles are stored as plain strings in tokens and sessions. The numeric level
// is derived at runtime via a registry lookup, not stored with the role name.
type Role string

// Built-in role constants in ascending permission order.
const (
	// Guest is the implicit role for unauthenticated requests (level 1).
	Guest Role = "guest"
	// Author can create and manage their own content (level 2).
	Author Role = "author"
	// Editor can manage all content (level 3).
	Editor Role = "editor"
	// Admin has full access including app configuration (level 4).
	Admin Role = "admin"
)

// roleMu protects roleLevels for concurrent custom role registration.
var roleMu sync.RWMutex

// roleLevels is the single source of truth for role → level mappings.
// Built-in roles are populated here; custom roles are added via Register.
// Built-in levels are spaced by 10 so custom roles can be inserted between
// them without renumbering. The absolute values carry no meaning; only the
// relative order matters.
var roleLevels = map[Role]int{
	Guest:  10,
	Author: 20,
	Editor: 30,
	Admin:  40,
}

// levelOf returns the numeric level for r, or 0 if the role is not registered.
// Level 0 never satisfies any permission check.
func levelOf(r Role) int {
	roleMu.RLock()
	l := roleLevels[r]
	roleMu.RUnlock()
	return l
}

// HasRole reports whether any role in userRoles has a level greater than or
// equal to the level of required. This is a hierarchical check: an Admin
// satisfies a check for Editor, Author, or Guest.
//
// Unknown roles (not registered) have level 0 and never satisfy any check.
func HasRole(userRoles []Role, required Role) bool {
	req := levelOf(required)
	if req == 0 {
		return false
	}
	for _, r := range userRoles {
		if levelOf(r) >= req {
			return true
		}
	}
	return false
}

// IsRole reports whether any role in userRoles exactly matches required.
// Unlike [HasRole], this is not hierarchical — Admin does not satisfy Editor.
func IsRole(userRoles []Role, required Role) bool {
	for _, r := range userRoles {
		if r == required {
			return true
		}
	}
	return false
}

// roleBuilder is returned by [NewRole] and provides a fluent API for
// positioning a custom role relative to the built-in hierarchy.
type roleBuilder struct {
	name  string
	level int
}

// NewRole begins the registration of a custom role. Call [roleBuilder.Above]
// or [roleBuilder.Below] to position it, then [roleBuilder.Register] to
// commit it to the role registry.
//
//	r, err := smeldr.NewRole("publisher").Above(smeldr.Author).Below(smeldr.Editor).Register()
func NewRole(name string) roleBuilder {
	return roleBuilder{name: name}
}

// Above positions the new role one level above r in the hierarchy.
// The returned builder is not yet registered — call [roleBuilder.Register].
func (rb roleBuilder) Above(r Role) roleBuilder {
	rb.level = levelOf(r) + 1
	return rb
}

// Below positions the new role one level below r in the hierarchy.
// The level is clamped to a minimum of 1 (may not be below Guest).
// The returned builder is not yet registered — call [roleBuilder.Register].
func (rb roleBuilder) Below(r Role) roleBuilder {
	l := levelOf(r) - 1
	if l < 1 {
		l = 1
	}
	rb.level = l
	return rb
}

// Register commits the custom role to the global role registry and returns the
// new [Role] value. Registration is idempotent: calling Register again with the
// same name and the same level is a no-op. Registering the same name with a
// different level returns a [*ValidationError].
func (rb roleBuilder) Register() (Role, error) {
	r := Role(rb.name)
	roleMu.Lock()
	defer roleMu.Unlock()
	if existing, ok := roleLevels[r]; ok {
		if existing != rb.level {
			return "", Err("role", "already registered with a different level")
		}
		return r, nil
	}
	roleLevels[r] = rb.level
	return r, nil
}

// Option configures a [Module] or App at registration time. Option values are
// created by functions such as [Read], [Write], [Delete], [At], [Cache], and
// [smeldr.On]. They are consumed during module or app setup and have no effect
// after [App.Run] is called.
type Option interface{ isOption() }

// roleOption is the concrete Option returned by [Read], [Write], and [Delete].
type roleOption struct {
	signal string
	role   Role
}

func (roleOption) isOption() {}

// Read returns an [Option] that restricts read (list + show) access to users
// whose role satisfies the required role. Wired in Step 10 (module.go).
func Read(r Role) Option { return roleOption{signal: "read", role: r} }

// Write returns an [Option] that restricts write (create + update) access to
// users whose role satisfies the required role. Wired in Step 10 (module.go).
func Write(r Role) Option { return roleOption{signal: "write", role: r} }

// Delete returns an [Option] that restricts delete access to users whose role
// satisfies the required role. Wired in Step 10 (module.go).
func Delete(r Role) Option { return roleOption{signal: "delete", role: r} }
