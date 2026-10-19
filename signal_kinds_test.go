package smeldr

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// signalTestApp is an App with the orchestration types and no governance:
// the plain Go surface, where a caller without a [Context] is trusted.
func signalTestApp(t *testing.T) (*App, *sql.DB) {
	t.Helper()
	db := newSQLiteDB(t)
	if err := CreateOrchestrationTables(db); err != nil {
		t.Fatalf("CreateOrchestrationTables: %v", err)
	}
	app := New(MustConfig(Config{BaseURL: "https://example.com", Secret: []byte(transitionItemTestSecret), DB: db}))
	RegisterOrchestrationTypes(app, db)
	return app, db
}

// signalRow reads the columns of one Signal the tests assert on.
func signalRow(t *testing.T, db *sql.DB, id string) (status, signalType, resolution, answer, answerRef string) {
	t.Helper()
	if err := db.QueryRow(`SELECT status, signal_type, resolution, answer, answer_ref FROM smeldr_signals WHERE id = ?`, id).
		Scan(&status, &signalType, &resolution, &answer, &answerRef); err != nil {
		t.Fatalf("signalRow(%s): %v", id, err)
	}
	return
}

// countSignals counts the Signals of one type in one status.
func countSignals(t *testing.T, db *sql.DB, signalType, status string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM smeldr_signals WHERE signal_type = ? AND status = ?`, signalType, status).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// wantValidation fails unless err is a ValidationError naming field.
func wantValidation(t *testing.T, err error, field string) {
	t.Helper()
	var ve *ValidationError
	if !errors.As(err, &ve) || !strings.Contains(err.Error(), field) {
		t.Fatalf("err = %v; want a validation error on %s", err, field)
	}
}

func TestSignalTypes_Catalog(t *testing.T) {
	types := SignalTypes()
	if len(types) != 9 {
		t.Fatalf("catalog has %d types, want 9", len(types))
	}
	kinds := map[SignalKind]int{}
	for _, info := range types {
		if info.Resolution == "" {
			t.Errorf("%s has no resolution rule", info.Type)
		}
		kinds[info.Kind]++
		if got, ok := SignalTypeOf(info.Type); !ok || got != info {
			t.Errorf("SignalTypeOf(%s) = %+v, %v", info.Type, got, ok)
		}
	}
	if kinds[SignalCondition] != 7 || kinds[SignalRequest] != 2 {
		t.Errorf("kinds = %v, want 7 conditions and 2 requests", kinds)
	}
	types[0].Type = "changed"
	if SignalTypes()[0].Type == "changed" {
		t.Error("SignalTypes returned the registry itself, not a copy")
	}
	for _, history := range []string{"plan-ready", "notice", "implementation-question", ""} {
		if _, ok := SignalTypeOf(history); ok {
			t.Errorf("%q is in the catalog, want history", history)
		}
	}
}

func TestCheckSignal(t *testing.T) {
	past := time.Now().Add(-time.Hour)
	future := time.Now().Add(time.Hour)
	long := strings.Repeat("é", SignalMessageMaxLength+1)
	system := withSignalSystem(context.Background())
	cases := []struct {
		name     string
		ctx      context.Context
		s        Signal
		c        SignalCutovers
		warnings []string
		err      string // "validation:<field>", "forbidden" or ""
	}{
		{"empty type", context.Background(), Signal{}, SignalCutovers{}, nil, "validation:signal_type"},
		{"request", context.Background(), Signal{SignalType: "question", Message: "why?"}, SignalCutovers{}, nil, ""},
		{"condition by a caller", context.Background(), Signal{SignalType: "conflict-detected"}, SignalCutovers{}, nil, "forbidden"},
		{"condition by the system", system, Signal{SignalType: "authorization-required"}, SignalCutovers{}, nil, ""},
		{"history before the cutover", context.Background(), Signal{SignalType: "plan-ready"}, SignalCutovers{Event: future}, []string{"deprecated_signal_type"}, ""},
		{"history after the cutover", context.Background(), Signal{SignalType: "plan-ready"}, SignalCutovers{Event: past}, nil, "validation:signal_type"},
		{"long message before the cutover", context.Background(), Signal{SignalType: "question", Message: long}, SignalCutovers{}, []string{"message_too_long"}, ""},
		{"long message after the cutover", context.Background(), Signal{SignalType: "question", Message: long}, SignalCutovers{Message: past}, nil, "validation:message"},
		{"history and long", context.Background(), Signal{SignalType: "notice", Message: long}, SignalCutovers{}, []string{"deprecated_signal_type", "message_too_long"}, ""},
		{"message at the limit", context.Background(), Signal{SignalType: "question", Message: long[:len(long)-2]}, SignalCutovers{Message: past}, nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ws, err := CheckSignal(tc.ctx, &tc.s, tc.c)
			switch {
			case tc.err == "forbidden":
				if !errors.Is(err, ErrForbidden) {
					t.Fatalf("err = %v, want ErrForbidden", err)
				}
			case strings.HasPrefix(tc.err, "validation:"):
				wantValidation(t, err, strings.TrimPrefix(tc.err, "validation:"))
			case err != nil:
				t.Fatalf("err = %v", err)
			}
			var codes []string
			for _, w := range ws {
				codes = append(codes, w.Code)
			}
			if strings.Join(codes, ",") != strings.Join(tc.warnings, ",") {
				t.Errorf("warnings = %v, want %v", codes, tc.warnings)
			}
		})
	}
}

func TestCheckSignal_ImplementationQuestionNamesQuestion(t *testing.T) {
	ws, err := CheckSignal(context.Background(), &Signal{SignalType: "implementation-question"}, SignalCutovers{})
	if err != nil || len(ws) != 1 || !strings.Contains(ws[0].Message, `"question"`) {
		t.Fatalf("warnings = %+v, %v; want one naming question as the replacement", ws, err)
	}
}

func TestApp_SignalCutovers(t *testing.T) {
	if (*App)(nil).SignalCutovers() != (SignalCutovers{}) {
		t.Error("a nil App has cutovers")
	}
	m, e := time.Now(), time.Now().Add(time.Hour)
	app := New(MustConfig(Config{BaseURL: "https://example.com", Secret: []byte(transitionItemTestSecret), SignalMessageCutover: m, SignalEventCutover: e}))
	if got := app.SignalCutovers(); !got.Message.Equal(m) || !got.Event.Equal(e) {
		t.Errorf("SignalCutovers = %+v", got)
	}
}

func TestInsertSignalWithWarnings(t *testing.T) {
	ctx := context.Background()
	_, db := signalTestApp(t)
	q := &Signal{SignalType: "question", Node: Node{Status: "resolved"}}
	if ws, err := InsertSignalWithWarnings(ctx, db, q, SignalCutovers{}); err != nil || len(ws) != 0 || q.Status != "open" {
		t.Errorf("request: %v %v status %q, want open with no warnings", ws, err, q.Status)
	}
	h := &Signal{SignalType: "plan-ready"}
	if ws, err := InsertSignalWithWarnings(ctx, db, h, SignalCutovers{}); err != nil || len(ws) != 1 || h.Status != "pending" {
		t.Errorf("history: %v %v status %q, want pending with one warning", ws, err, h.Status)
	}
	if _, err := InsertSignalWithWarnings(ctx, db, &Signal{SignalType: "notice"}, SignalCutovers{Event: time.Now().Add(-time.Minute)}); err == nil {
		t.Error("history after the cutover was written")
	}
	if err := InsertSignal(ctx, db, &Signal{SignalType: "ratification-required"}); !errors.Is(err, ErrForbidden) {
		t.Errorf("a condition from a caller: %v, want ErrForbidden", err)
	}
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM smeldr_signals`).Scan(&n)
	if n != 2 {
		t.Errorf("rows = %d, want 2 (the refused ones are not written)", n)
	}
}

// The module create path (create_content, POST) applies the same rules: the
// state is the server's, history warns, and a caller cannot create a
// condition.
func TestSignalModuleCreate(t *testing.T) {
	app, db := signalTestApp(t)
	m := findModule[*Signal](t, app)
	out, err := m.MCPCreate(authorCtx(), map[string]any{"signal_type": "question", "message": "m", "status": "resolved"})
	if err != nil || asItem[*Signal](out).Status != "open" {
		t.Fatalf("question: %v %v, want open", out, err)
	}
	out, err = m.MCPCreate(authorCtx(), map[string]any{"signal_type": "notice"})
	if err != nil || asItem[*Signal](out).Status != "pending" {
		t.Fatalf("notice: %v %v, want pending", out, err)
	}
	if b, _ := json.Marshal(out); !strings.Contains(string(b), "deprecated_signal_type") {
		t.Errorf("history create = %s, want a deprecated_signal_type warning", b)
	}
	if _, err := m.MCPCreate(authorCtx(), map[string]any{"signal_type": "conflict-detected"}); !errors.Is(err, ErrForbidden) {
		t.Errorf("condition: %v, want ErrForbidden", err)
	}
	if _, err := m.MCPCreate(authorCtx(), map[string]any{"message": "no type"}); err == nil {
		t.Error("a Signal without a type was created")
	}
	if n := countSignals(t, db, "question", "open"); n != 1 {
		t.Errorf("open questions = %d, want 1", n)
	}
}
