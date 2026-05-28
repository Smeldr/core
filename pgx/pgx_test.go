package forgepgx

import "testing"

// TestWrap_compilesAsForgeDB documents the compile-time guarantee that
// poolAdapter satisfies smeldr.DB. The actual enforcement is the package-level
// declaration in pgx.go:
//
//	var _ smeldr.DB = (*poolAdapter)(nil)
//
// This test exists to keep that guarantee visible in test output and ensure the
// file is included in all test builds. No database is required.
func TestWrap_compilesAsForgeDB(t *testing.T) {
	// Compilation of this package is the test.
	// No runtime check needed — the guarantee is enforced at compile time.
}
