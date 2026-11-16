package smeldr

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// T1299: the bootstrap admin token never enters the log capture and is never
// served at /_logs, while standard error gets the line exactly as before.

// adminAuth authenticates every request as an Admin.
type adminAuth struct{}

func (adminAuth) authenticate(*http.Request) (User, bool) {
	return User{ID: "admin", Roles: []Role{Admin}}, true
}

// noTime drops the time attribute so two outputs compare byte for byte.
func noTime(groups []string, a slog.Attr) slog.Attr {
	if len(groups) == 0 && a.Key == slog.TimeKey {
		return slog.Attr{}
	}
	return a
}

func TestCaptureLogs_BootstrapTokenNotCaptured(t *testing.T) {
	for _, tc := range []struct {
		name          string
		captureBefore bool
		level         slog.Level
	}{
		{"capture before, default level", true, slog.LevelWarn},
		{"capture before, debug level", true, slog.LevelDebug},
		{"capture after", false, slog.LevelDebug},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prev := slog.Default()
			t.Cleanup(func() { restoreDefaultLogging(prev) })
			var stderr bytes.Buffer
			slog.SetDefault(slog.New(slog.NewTextHandler(&stderr, &slog.HandlerOptions{Level: slog.LevelDebug, ReplaceAttr: noTime})))
			app := &App{}
			if tc.captureBefore {
				app.CaptureLogs(WithLogLevel(tc.level))
			}
			ts := NewTokenStore(newTestTokensDBWithUserID(t), testSecret)
			if _, created := ts.ensureBootstrap(context.Background()); !created {
				t.Fatal("no bootstrap token")
			}
			if !tc.captureBefore {
				app.CaptureLogs(WithLogLevel(tc.level))
			}
			out := stderr.String()
			_, after, ok := strings.Cut(out, `(copy now, shown once):\n\t`)
			if !ok {
				t.Fatalf("no bootstrap line on standard error: %q", out)
			}
			raw, _, _ := strings.Cut(after, `"`)
			if len(raw) < 20 {
				t.Fatalf("no token in the line: %q", out)
			}
			// Byte for byte what the line was before (T1299: the Getting started
			// pages copy the token from it).
			var want bytes.Buffer
			slog.New(slog.NewTextHandler(&want, &slog.HandlerOptions{Level: slog.LevelDebug, ReplaceAttr: noTime})).
				Warn("smeldr: smeldr_tokens is empty: bootstrap admin token created (copy now, shown once):\n\t" + raw)
			if !strings.Contains(out, want.String()) {
				t.Errorf("standard error changed:\n got %q\nwant %q", out, want.String())
			}
			entries, _, _ := app.logRing.snapshot()
			for _, e := range entries {
				if strings.Contains(e.Msg, raw) {
					t.Errorf("the ring holds the token: %+v", e)
				}
			}
			rec := httptest.NewRecorder()
			newLogsHandler(adminAuth{}, app.logRing).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/_logs", nil))
			if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), raw) {
				t.Errorf("/_logs: %d, holds the token: %v", rec.Code, strings.Contains(rec.Body.String(), raw))
			}
		})
	}
}

// A marked record reaches the inner handler and not the ring, also through a
// handler derived from the capture; an unmarked one reaches both.
func TestTeeHandler_WithoutCapture(t *testing.T) {
	prev := slog.Default()
	t.Cleanup(func() { restoreDefaultLogging(prev) })
	var inner bytes.Buffer
	slog.SetDefault(slog.New(slog.NewTextHandler(&inner, &slog.HandlerOptions{Level: slog.LevelDebug})))
	app := (&App{}).CaptureLogs(WithLogLevel(slog.LevelDebug))
	marked := withoutCapture(context.Background())
	slog.ErrorContext(marked, "marked-plain")
	slog.With("k", "v").WithGroup("g").ErrorContext(marked, "marked-derived")
	slog.ErrorContext(context.Background(), "unmarked")
	for _, msg := range []string{"marked-plain", "marked-derived", "unmarked"} {
		if !strings.Contains(inner.String(), msg) {
			t.Errorf("inner handler lacks %s", msg)
		}
	}
	entries, _, _ := app.logRing.snapshot()
	if len(entries) != 1 || entries[0].Msg != "unmarked" {
		t.Errorf("ring = %+v, want only the unmarked record", entries)
	}
}
