package smeldr

import (
	"io/fs"
	"log/slog"
	"net/http"
	"os"
)

// Static mounts a static file handler at prefix.
//
// In production ([Config.Dev] == false) it serves from the embedded prod FS
// with a one-year Cache-Control: public, max-age=31536000, immutable header so
// browsers and CDNs cache assets aggressively.
//
// In development ([Config.Dev] == true) it serves from devDir on disk so
// changes are visible without rebuilding. A startup log line is emitted via
// [log/slog] to make the active mode visible:
//
//	static: serving from disk dir=static
//
// Static panics at startup if Config.Dev is true and devDir does not exist,
// because this always indicates a misconfiguration rather than a runtime error.
//
// Example:
//
//	//go:embed static
//	var staticFiles embed.FS
//
//	staticFS, _ := fs.Sub(staticFiles, "static")
//	app.Static("/static/", staticFS, "static")
func (a *App) Static(prefix string, prod fs.FS, devDir string) {
	var h http.Handler
	if a.cfg.Dev {
		if _, err := os.Stat(devDir); err != nil {
			panic("forge: Static: devDir " + devDir + " does not exist: " + err.Error())
		}
		slog.Info("static: serving from disk", "dir", devDir)
		h = http.StripPrefix(prefix, http.FileServer(http.Dir(devDir)))
	} else {
		h = http.StripPrefix(prefix, withImmutableCache(http.FileServerFS(prod)))
	}
	a.mux.Handle("GET "+prefix, h)
}

// withImmutableCache wraps h to set Cache-Control: public, max-age=31536000,
// immutable on every response. Used by [App.Static] in production mode.
func withImmutableCache(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		h.ServeHTTP(w, r)
	})
}
