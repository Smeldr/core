package forgemedia

// os_helpers.go contains thin wrappers around os and encoding functions.
// These are package-level variables so tests can substitute stubs without
// touching the filesystem.

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"io"
	"os"
)

// ensureDir creates dir and all parents if they do not exist.
var ensureDir = func(dir string) error {
	return os.MkdirAll(dir, 0o755)
}

// writeFile writes data to path, creating or truncating the file.
var writeFile = func(path string, data []byte) error {
	return os.WriteFile(path, data, 0o644)
}

// removeFile deletes path. Returns nil if the file does not exist.
var removeFile = func(path string) error {
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// randRead fills b with cryptographically random bytes.
var randRead = rand.Read

// isNoRows reports whether err is sql.ErrNoRows.
func isNoRows(err error) bool {
	return err == sql.ErrNoRows
}

// encodeJSON encodes v as JSON to w, discarding any encoding error (callers
// cannot recover from a broken response writer at this point).
var encodeJSON = func(w io.Writer, v any) {
	_ = json.NewEncoder(w).Encode(v)
}
