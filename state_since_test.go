// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type sinceCountDB struct {
	DB
	queries atomic.Int32
}

func (d *sinceCountDB) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	if strings.Contains(q, "FROM smeldr_provenance") {
		d.queries.Add(1)
	}
	return d.DB.QueryContext(ctx, q, args...)
}

func sinceApp(t *testing.T) (*App, DB, ProvenanceStore) {
	t.Helper()
	db := newSQLiteDB(t)
	if err := CreateProvenanceTable(db); err != nil {
		t.Fatalf("CreateProvenanceTable: %v", err)
	}
	store := NewProvenanceStore(db)
	return &App{cfg: Config{DB: db}, provenanceStore: store}, db, store
}

func appendRec(t *testing.T, store ProvenanceStore, r ProvenanceRecord) {
	t.Helper()
	if err := store.Append(context.Background(), r); err != nil {
		t.Fatalf("Append: %v", err)
	}
}

func TestItemsStateSince_LatestTransitionWins(t *testing.T) {
	app, _, store := sinceApp(t)
	t0 := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	appendRec(t, store, ProvenanceRecord{ID: "r1", Timestamp: t0, SubjectType: "Task", SubjectID: "a", Verb: "transition", FromState: "backlog", ToState: "active"})
	appendRec(t, store, ProvenanceRecord{ID: "r2", Timestamp: t0.Add(time.Hour), SubjectType: "Task", SubjectID: "a", Verb: "transition", FromState: "active", ToState: "blocked", Reason: "waiting on X"})
	got, err := app.ItemsStateSince(context.Background(), "Task", map[string]string{"a": "blocked"})
	if err != nil {
		t.Fatalf("ItemsStateSince: %v", err)
	}
	s, ok := got["a"]
	if !ok || !s.Since.Equal(t0.Add(time.Hour)) || s.Reason != "waiting on X" {
		t.Errorf("got %+v, want the later transition with its reason", got)
	}
}

func TestItemsStateSince_IgnoresOtherVerbsAndTypes(t *testing.T) {
	app, _, store := sinceApp(t)
	t0 := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	appendRec(t, store, ProvenanceRecord{ID: "r1", Timestamp: t0, SubjectType: "Task", SubjectID: "a", Verb: "transition", ToState: "active", Reason: "real"})
	// A later update and a later record of another type must not shadow it.
	appendRec(t, store, ProvenanceRecord{ID: "r2", Timestamp: t0.Add(time.Hour), SubjectType: "Task", SubjectID: "a", Verb: "update", ToState: "active", Reason: "edit"})
	appendRec(t, store, ProvenanceRecord{ID: "r3", Timestamp: t0.Add(2 * time.Hour), SubjectType: "Goal", SubjectID: "a", Verb: "transition", ToState: "active", Reason: "other type"})
	got, _ := app.ItemsStateSince(context.Background(), "Task", map[string]string{"a": "active"})
	if got["a"].Reason != "real" || !got["a"].Since.Equal(t0) {
		t.Errorf("got %+v, want the transition record only", got["a"])
	}
}

func TestItemsStateSince_ToStateMismatchAndNoRecordAreAbsent(t *testing.T) {
	app, _, store := sinceApp(t)
	appendRec(t, store, ProvenanceRecord{ID: "r1", Timestamp: time.Now().UTC(), SubjectType: "Task", SubjectID: "a", Verb: "transition", ToState: "active"})
	got, err := app.ItemsStateSince(context.Background(), "Task", map[string]string{"a": "done", "b": "active"})
	if err != nil {
		t.Fatalf("ItemsStateSince: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %+v, want empty: a's latest record is not into its current state, b has none", got)
	}
}

func TestItemsStateSince_SameSecondOrderedByID(t *testing.T) {
	app, _, store := sinceApp(t)
	t0 := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	appendRec(t, store, ProvenanceRecord{ID: "01a", Timestamp: t0, SubjectType: "Task", SubjectID: "a", Verb: "transition", ToState: "x", Reason: "first"})
	appendRec(t, store, ProvenanceRecord{ID: "01b", Timestamp: t0, SubjectType: "Task", SubjectID: "a", Verb: "transition", ToState: "x", Reason: "second"})
	got, _ := app.ItemsStateSince(context.Background(), "Task", map[string]string{"a": "x"})
	if got["a"].Reason != "second" {
		t.Errorf("reason = %q, want the greater id to win a same-second tie", got["a"].Reason)
	}
}

func TestItemsStateSince_NilStoreAndEmptyInput(t *testing.T) {
	ctx := context.Background()
	got, err := (&App{}).ItemsStateSince(ctx, "Task", map[string]string{"a": "x"})
	if err != nil || len(got) != 0 {
		t.Errorf("nil store: got %v, %v", got, err)
	}
	app, _, _ := sinceApp(t)
	got, err = app.ItemsStateSince(ctx, "Task", nil)
	if err != nil || got == nil || len(got) != 0 {
		t.Errorf("empty input: got %v, %v", got, err)
	}
}

func TestItemsStateSince_Chunks(t *testing.T) {
	app, db, store := sinceApp(t)
	cdb := &sinceCountDB{DB: db}
	app.provenanceStore = NewProvenanceStore(cdb)
	current := map[string]string{}
	for i := 0; i < 850; i++ {
		id := fmt.Sprintf("id%04d", i)
		current[id] = "active"
	}
	appendRec(t, store, ProvenanceRecord{ID: "r1", Timestamp: time.Now().UTC(), SubjectType: "Task", SubjectID: "id0849", Verb: "transition", ToState: "active"})
	got, err := app.ItemsStateSince(context.Background(), "Task", current)
	if err != nil {
		t.Fatalf("ItemsStateSince: %v", err)
	}
	if _, ok := got["id0849"]; !ok || len(got) != 1 {
		t.Errorf("got %d entries, want only id0849", len(got))
	}
	if n := cdb.queries.Load(); n != 3 {
		t.Errorf("%d queries for 850 ids, want 3 (400 per chunk)", n)
	}
}

// listOnlyStore has no batch read, so ItemsStateSince falls back to List.
type listOnlyStore struct {
	recs  []ProvenanceRecord
	err   error
	lists atomic.Int32
}

func (s *listOnlyStore) Append(context.Context, ProvenanceRecord) error { return nil }
func (s *listOnlyStore) List(_ context.Context, f ProvenanceFilter) ([]ProvenanceRecord, error) {
	s.lists.Add(1)
	if s.err != nil {
		return nil, s.err
	}
	var out []ProvenanceRecord
	for _, r := range s.recs {
		if r.SubjectType == f.SubjectType && r.SubjectID == f.SubjectID {
			out = append(out, r)
		}
	}
	return out, nil
}

func TestItemsStateSince_ListFallback(t *testing.T) {
	t0 := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	st := &listOnlyStore{recs: []ProvenanceRecord{
		{ID: "r1", Timestamp: t0, SubjectType: "Task", SubjectID: "a", Verb: "transition", ToState: "active", Reason: "old"},
		{ID: "r2", Timestamp: t0.Add(time.Hour), SubjectType: "Task", SubjectID: "a", Verb: "transition", ToState: "blocked", Reason: "new"},
		{ID: "r3", Timestamp: t0.Add(2 * time.Hour), SubjectType: "Task", SubjectID: "a", Verb: "update", ToState: "blocked"},
	}}
	app := &App{provenanceStore: st}
	got, err := app.ItemsStateSince(context.Background(), "Task", map[string]string{"a": "blocked", "b": "x"})
	if err != nil {
		t.Fatalf("ItemsStateSince: %v", err)
	}
	if got["a"].Reason != "new" || len(got) != 1 {
		t.Errorf("got %+v", got)
	}
	if st.lists.Load() != 2 {
		t.Errorf("%d List calls, want one per id", st.lists.Load())
	}
}

func TestItemsStateSince_FailedReadIsErrInternal(t *testing.T) {
	app := &App{provenanceStore: &listOnlyStore{err: errors.New("boom")}}
	if _, err := app.ItemsStateSince(context.Background(), "Task", map[string]string{"a": "x"}); !errors.Is(err, ErrInternal) {
		t.Errorf("fallback error = %v, want ErrInternal", err)
	}
	db := newSQLiteDB(t) // no provenance table
	app = &App{provenanceStore: NewProvenanceStore(db)}
	if _, err := app.ItemsStateSince(context.Background(), "Task", map[string]string{"a": "x"}); !errors.Is(err, ErrInternal) {
		t.Errorf("sql error = %v, want ErrInternal", err)
	}
}

// A timestamp that does not parse leaves the subject unknown: no zero date, and
// no older record standing in for the newest one.
func TestItemsStateSince_UnparseableTimestampIsUnknown(t *testing.T) {
	app, db, store := sinceApp(t)
	t0 := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	appendRec(t, store, ProvenanceRecord{ID: "r1", Timestamp: t0, SubjectType: "Task", SubjectID: "a", Verb: "transition", ToState: "active", Reason: "older"})
	appendRec(t, store, ProvenanceRecord{ID: "r2", Timestamp: t0, SubjectType: "Task", SubjectID: "b", Verb: "transition", ToState: "active", Reason: "fine"})
	// "z" sorts after every RFC3339 text, so this is the newest row of a.
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO smeldr_provenance (id, timestamp, subject_type, subject_id, verb, from_state, to_state, actor_kind, actor_id, surface, reason)
		 VALUES ('r3', 'z-not-a-time', 'Task', 'a', 'transition', '', 'active', '', '', '', 'newest')`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	got, err := app.ItemsStateSince(context.Background(), "Task", map[string]string{"a": "active", "b": "active"})
	if err != nil {
		t.Fatalf("ItemsStateSince: %v", err)
	}
	if _, ok := got["a"]; ok {
		t.Errorf("a = %+v, want absent: its newest record has no readable timestamp", got["a"])
	}
	if got["b"].Reason != "fine" || got["b"].Since.IsZero() {
		t.Errorf("b = %+v, want the readable record", got["b"])
	}
}
