package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	smeldr "smeldr.dev/core"
)

// D196, T1294: with governance records on, the change check re-check runs at
// start; when that run fails, the server still starts and says so in the log.
func TestChangeCheckRecheck_StartFailureLogged(t *testing.T) {
	old := recheckChangeChecks
	recheckChangeChecks = func(*smeldr.App, context.Context) (smeldr.RecheckResult, error) {
		return smeldr.RecheckResult{}, errors.New("injected")
	}
	defer func() { recheckChangeChecks = old }()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	cfg := baseConfig()
	cfg.EnableRelations, cfg.EnableGovernanceRecords = true, true
	ts := buildTestServer(t, cfg)
	slog.SetDefault(prev)
	if getStatus(t, ts.URL, "", "/_health") != http.StatusOK {
		t.Error("the server did not start")
	}
	if !strings.Contains(buf.String(), "change check re-check at start failed") {
		t.Errorf("not logged: %s", buf.String())
	}
}
