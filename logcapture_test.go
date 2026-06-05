package smeldr

import (
	"bytes"
	"context"
	"log"
	"log/slog"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// builtinDefaultHandler is slog's zero-config handler, captured at package load
// before any test mutates the global default. It is the handler that bridges to
// the log package and must never be wrapped + reinstalled.
var builtinDefaultHandler = slog.Default().Handler()

// restoreDefaultLogging returns the process to its pristine logging state:
// slog default = prev, and the log package writing directly to os.Stderr (not
// routed back through slog). Restoring a built-in default without resetting the
// log output would re-arm the slog/log re-entrancy cycle for later tests.
func restoreDefaultLogging(prev *slog.Logger) {
	slog.SetDefault(prev)
	log.SetOutput(os.Stderr)
}

// ---------------------------------------------------------------------------
// logRing — append, ordering, eviction
// ---------------------------------------------------------------------------

func TestLogRing_AppendAssignsMonotonicSeq(t *testing.T) {
	r := newLogRing(8)
	for i := 0; i < 3; i++ {
		r.append(LogEntry{Msg: "m"})
	}
	entries, capacity, dropped := r.snapshot()
	if capacity != 8 {
		t.Fatalf("capacity = %d, want 8", capacity)
	}
	if dropped != 0 {
		t.Fatalf("dropped = %d, want 0", dropped)
	}
	if len(entries) != 3 {
		t.Fatalf("len(entries) = %d, want 3", len(entries))
	}
	// Newest first: seq 3, 2, 1.
	wantSeq := []uint64{3, 2, 1}
	for i, e := range entries {
		if e.Seq != wantSeq[i] {
			t.Errorf("entries[%d].Seq = %d, want %d", i, e.Seq, wantSeq[i])
		}
	}
}

func TestLogRing_EvictionAndDropped(t *testing.T) {
	r := newLogRing(3)
	for i := 0; i < 5; i++ {
		r.append(LogEntry{Msg: "m"})
	}
	entries, capacity, dropped := r.snapshot()
	if capacity != 3 {
		t.Fatalf("capacity = %d, want 3", capacity)
	}
	if dropped != 2 {
		t.Fatalf("dropped = %d, want 2", dropped)
	}
	if len(entries) != 3 {
		t.Fatalf("len(entries) = %d, want 3", len(entries))
	}
	// Newest first: seq 5, 4, 3 (1 and 2 were overwritten).
	wantSeq := []uint64{5, 4, 3}
	for i, e := range entries {
		if e.Seq != wantSeq[i] {
			t.Errorf("entries[%d].Seq = %d, want %d", i, e.Seq, wantSeq[i])
		}
	}
}

func TestLogRing_DefaultCapacity(t *testing.T) {
	if got := newLogRing(0); len(got.buf) != defaultLogCapacity {
		t.Errorf("newLogRing(0) capacity = %d, want %d", len(got.buf), defaultLogCapacity)
	}
	if got := newLogRing(-5); len(got.buf) != defaultLogCapacity {
		t.Errorf("newLogRing(-5) capacity = %d, want %d", len(got.buf), defaultLogCapacity)
	}
}

func TestLogRing_Concurrency(t *testing.T) {
	const goroutines = 50
	const perG = 100
	r := newLogRing(100)

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				r.append(LogEntry{Msg: "x"})
			}
		}()
	}
	wg.Wait()

	entries, capacity, dropped := r.snapshot()
	total := uint64(goroutines * perG)
	if capacity != 100 {
		t.Fatalf("capacity = %d, want 100", capacity)
	}
	if len(entries) != 100 {
		t.Fatalf("len(entries) = %d, want 100", len(entries))
	}
	if r.seq != total {
		t.Errorf("seq = %d, want %d", r.seq, total)
	}
	if dropped != total-100 {
		t.Errorf("dropped = %d, want %d", dropped, total-100)
	}
}

// ---------------------------------------------------------------------------
// teeHandler — stderr preservation, level gating, Enabled OR rule
// ---------------------------------------------------------------------------

// newTestTee builds a teeHandler wrapping a text handler that writes to buf.
func newTestTee(buf *bytes.Buffer, innerLevel, ringLevel slog.Level) (*teeHandler, *logRing) {
	ring := newLogRing(64)
	inner := slog.NewTextHandler(buf, &slog.HandlerOptions{Level: innerLevel})
	return &teeHandler{inner: inner, ring: ring, minLevel: ringLevel}, ring
}

func TestTeeHandler_PreservesStderrAndGatesRing(t *testing.T) {
	var buf bytes.Buffer
	tee, ring := newTestTee(&buf, slog.LevelDebug, slog.LevelWarn)
	log := slog.New(tee)

	log.Debug("dbg-line")
	log.Info("info-line")
	log.Warn("warn-line")
	log.Error("err-line")

	// Inner handler (LevelDebug) must still receive every line.
	for _, want := range []string{"dbg-line", "info-line", "warn-line", "err-line"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("inner output missing %q\n--- got ---\n%s", want, buf.String())
		}
	}

	// Ring (LevelWarn) captures only WARN and ERROR.
	entries, _, _ := ring.snapshot()
	if len(entries) != 2 {
		t.Fatalf("ring captured %d entries, want 2 (WARN+ERROR)", len(entries))
	}
	if entries[0].Level != "ERROR" || entries[1].Level != "WARN" {
		t.Errorf("ring levels = [%s %s], want [ERROR WARN]", entries[0].Level, entries[1].Level)
	}
	if entries[0].Msg != "err-line" || entries[1].Msg != "warn-line" {
		t.Errorf("ring msgs = [%s %s]", entries[0].Msg, entries[1].Msg)
	}
}

func TestTeeHandler_EnabledOrRule(t *testing.T) {
	var buf bytes.Buffer
	ctx := context.Background()

	// Inner at Debug (verbose), ring at Error (strict): stderr threshold must win
	// for the low levels — Enabled stays true so stderr keeps receiving them.
	tee, _ := newTestTee(&buf, slog.LevelDebug, slog.LevelError)
	if !tee.Enabled(ctx, slog.LevelInfo) {
		t.Error("Enabled(Info) = false; inner is Debug, must stay enabled (OR rule)")
	}

	// Inner at Error (strict), ring at Warn (verbose): ring threshold must win for
	// WARN so the record reaches Handle and is captured.
	tee2, _ := newTestTee(&buf, slog.LevelError, slog.LevelWarn)
	if !tee2.Enabled(ctx, slog.LevelWarn) {
		t.Error("Enabled(Warn) = false; ring is Warn, must be enabled (OR rule)")
	}
	if tee2.Enabled(ctx, slog.LevelInfo) {
		t.Error("Enabled(Info) = true; neither inner (Error) nor ring (Warn) wants Info")
	}
}

func TestTeeHandler_AttrsAndGroupFidelity(t *testing.T) {
	var buf bytes.Buffer
	tee, ring := newTestTee(&buf, slog.LevelDebug, slog.LevelWarn)

	log := slog.New(tee).With("service", "core").WithGroup("req")
	log.Error("boom", "status", 500, slog.Group("user", "id", "u1"))

	entries, _, _ := ring.snapshot()
	if len(entries) != 1 {
		t.Fatalf("captured %d entries, want 1", len(entries))
	}
	got := entries[0].Attrs
	want := map[string]any{
		"service": "core",
		"req": map[string]any{
			"status": int64(500),
			"user":   map[string]any{"id": "u1"},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("attrs mismatch\n got: %#v\nwant: %#v", got, want)
	}
}

func TestTeeHandler_NoAttrsOmitsMap(t *testing.T) {
	var buf bytes.Buffer
	tee, ring := newTestTee(&buf, slog.LevelDebug, slog.LevelWarn)
	slog.New(tee).Warn("plain")

	entries, _, _ := ring.snapshot()
	if len(entries) != 1 {
		t.Fatalf("captured %d entries, want 1", len(entries))
	}
	if entries[0].Attrs != nil {
		t.Errorf("Attrs = %#v, want nil for an attr-less record", entries[0].Attrs)
	}
	if entries[0].Time.Location() != nil && entries[0].Time.Location().String() != "UTC" {
		t.Errorf("Time not normalised to UTC: %v", entries[0].Time.Location())
	}
}

// ---------------------------------------------------------------------------
// newLogTee — option defaults, custom-handler preservation, default-handler
// substitution (the slog/log re-entrancy guard). No global state is mutated.
// ---------------------------------------------------------------------------

func TestNewLogTee_Defaults(t *testing.T) {
	var buf bytes.Buffer
	custom := slog.NewTextHandler(&buf, nil)

	tee := newLogTee(custom)
	if tee.minLevel != slog.LevelWarn {
		t.Errorf("default minLevel = %v, want Warn", tee.minLevel)
	}
	if len(tee.ring.buf) != defaultLogCapacity {
		t.Errorf("default capacity = %d, want %d", len(tee.ring.buf), defaultLogCapacity)
	}
}

func TestNewLogTee_AppliesOptions(t *testing.T) {
	var buf bytes.Buffer
	tee := newLogTee(slog.NewTextHandler(&buf, nil), WithLogCapacity(16), WithLogLevel(slog.LevelInfo))
	if tee.minLevel != slog.LevelInfo {
		t.Errorf("minLevel = %v, want Info", tee.minLevel)
	}
	if len(tee.ring.buf) != 16 {
		t.Errorf("ring capacity = %d, want 16", len(tee.ring.buf))
	}
}

func TestNewLogTee_PreservesCustomHandler(t *testing.T) {
	var buf bytes.Buffer
	custom := slog.NewTextHandler(&buf, nil)
	tee := newLogTee(custom)
	if tee.inner != custom {
		t.Errorf("inner = %T, want the supplied custom handler to be wrapped unchanged", tee.inner)
	}
}

func TestNewLogTee_SubstitutesBuiltinDefault(t *testing.T) {
	if !bridgesToLog(builtinDefaultHandler) {
		t.Fatalf("bridgesToLog(builtin) = false; reflect type was %s",
			reflect.TypeOf(builtinDefaultHandler).String())
	}
	tee := newLogTee(builtinDefaultHandler)
	if tee.inner == builtinDefaultHandler {
		t.Fatal("newLogTee wrapped the built-in default handler — this would deadlock via the log bridge")
	}
	if bridgesToLog(tee.inner) {
		t.Errorf("substituted inner still bridges to log: %T", tee.inner)
	}
}

func TestBridgesToLog(t *testing.T) {
	var buf bytes.Buffer
	if bridgesToLog(slog.NewTextHandler(&buf, nil)) {
		t.Error("a TextHandler must not be reported as the log-bridging default")
	}
	if bridgesToLog(nil) {
		t.Error("nil handler must not be reported as the log-bridging default")
	}
}

// ---------------------------------------------------------------------------
// CaptureLogs — installs a global tee over a custom handler, preserves it,
// and restores pristine logging on cleanup (no cyclic state leaks).
// ---------------------------------------------------------------------------

func TestCaptureLogs_InstallsGlobalTee(t *testing.T) {
	prev := slog.Default()
	t.Cleanup(func() { restoreDefaultLogging(prev) })

	var buf bytes.Buffer
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))

	app := &App{}
	if got := app.CaptureLogs(WithLogCapacity(16), WithLogLevel(slog.LevelInfo)); got != app {
		t.Fatal("CaptureLogs did not return the App for chaining")
	}

	tee, ok := slog.Default().Handler().(*teeHandler)
	if !ok {
		t.Fatalf("default handler is %T, want *teeHandler", slog.Default().Handler())
	}

	slog.Info("captured-info")
	slog.Debug("below-threshold")

	// Inner (the custom handler at Debug) still gets both lines.
	if !strings.Contains(buf.String(), "captured-info") || !strings.Contains(buf.String(), "below-threshold") {
		t.Errorf("inner handler did not receive both lines:\n%s", buf.String())
	}

	// Ring captures Info+ only.
	entries, _, _ := tee.ring.snapshot()
	if len(entries) != 1 {
		t.Fatalf("ring captured %d entries, want 1", len(entries))
	}
	if entries[0].Msg != "captured-info" {
		t.Errorf("captured msg = %q, want captured-info", entries[0].Msg)
	}
}
