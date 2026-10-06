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
)

// standingCountDB counts the queries a call issues against the standing table
// and against the tagged-state lookup.
type standingCountDB struct {
	DB
	standing, tagged atomic.Int32
}

func (d *standingCountDB) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	switch {
	case strings.Contains(q, "FROM smeldr_standing"):
		d.standing.Add(1)
	case strings.Contains(q, "FROM smeldr_states s JOIN smeldr_state_flows"):
		d.tagged.Add(1)
	}
	return d.DB.QueryContext(ctx, q, args...)
}

func TestTypeHasStanding(t *testing.T) {
	db := newMigratedDB(t)
	standingFlow(t, db, "HasT", "live")
	ctx := context.Background()
	if !TypeHasStanding(ctx, db, "HasT") {
		t.Error("a type whose flow tags a state has standing")
	}
	if TypeHasStanding(ctx, db, "NoSuchType") {
		t.Error("an unregistered type has no standing")
	}
	if TypeHasStanding(ctx, &queryFailDB{}, "HasT") {
		t.Error("a failed lookup reports no standing (fail-open)")
	}
}

func TestItemStandings_Values(t *testing.T) {
	db := newMigratedDB(t)
	standingFlow(t, db, "BatchT", "live")
	ctx := context.Background()
	writeStanding(ctx, db, nil, stateChange{typeName: "BatchT", id: "a", from: "draft", to: "live"})
	writeStanding(ctx, db, nil, stateChange{typeName: "BatchT", id: "b", from: "draft", to: "live"})
	writeStanding(ctx, db, nil, stateChange{typeName: "BatchT", id: "b", from: "live", to: "closed"})
	// "c" has no row.
	got, err := ItemStandings(ctx, db, "BatchT", []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("ItemStandings: %v", err)
	}
	want := map[string]Standing{"a": StandingHolds, "b": StandingCeased, "c": StandingNone}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("standing of %s = %q, want %q", id, got[id], w)
		}
	}
	// A row of another type with the same id is not read.
	standingFlow(t, db, "OtherT", "live")
	writeStanding(ctx, db, nil, stateChange{typeName: "OtherT", id: "c", from: "draft", to: "live"})
	got, _ = ItemStandings(ctx, db, "BatchT", []string{"c"})
	if got["c"] != StandingNone {
		t.Errorf("another type's row leaked: %q", got["c"])
	}
}

func TestItemStandings_NoStandingTypeAndEmptyIDs(t *testing.T) {
	db := newMigratedDB(t)
	standingFlow(t, db, "HasT2", "live")
	if err := (&App{cfg: Config{DB: db}}).RegisterFlow(StateFlow{
		Name: "plain", TypeName: "PlainT", States: []State{{Name: "open", IsInitial: true}},
	}); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
	cdb := &standingCountDB{DB: db}
	got, err := ItemStandings(context.Background(), cdb, "PlainT", []string{"x", "y"})
	if err != nil || len(got) != 0 {
		t.Errorf("a type without standing = %v, %v, want an empty map", got, err)
	}
	if n := cdb.standing.Load(); n != 0 {
		t.Errorf("%d standing queries for a type without standing, want none", n)
	}
	got, err = ItemStandings(context.Background(), cdb, "HasT2", nil)
	if err != nil || len(got) != 0 {
		t.Errorf("no ids = %v, %v, want an empty map", got, err)
	}
	if n := cdb.standing.Load(); n != 0 {
		t.Errorf("%d standing queries for no ids, want none", n)
	}
}

// TestItemStandings_Chunks: more ids than one query carries are split, every id
// answered, and the page costs ceil(n/400) standing queries plus one tagged-state
// lookup, not one per id.
func TestItemStandings_Chunks(t *testing.T) {
	db := newMigratedDB(t)
	standingFlow(t, db, "ChunkT", "live")
	ctx := context.Background()
	const n = 850
	ids := make([]string, n)
	for i := range ids {
		ids[i] = fmt.Sprintf("id-%d", i)
	}
	for _, i := range []int{0, 399, 400, 849} {
		writeStanding(ctx, db, nil, stateChange{typeName: "ChunkT", id: ids[i], from: "draft", to: "live"})
	}
	cdb := &standingCountDB{DB: db}
	got, err := ItemStandings(ctx, cdb, "ChunkT", ids)
	if err != nil {
		t.Fatalf("ItemStandings: %v", err)
	}
	if len(got) != n {
		t.Fatalf("answered %d ids, want %d", len(got), n)
	}
	for i, id := range ids {
		want := StandingNone
		if i == 0 || i == 399 || i == 400 || i == 849 {
			want = StandingHolds
		}
		if got[id] != want {
			t.Errorf("%s = %q, want %q", id, got[id], want)
		}
	}
	if s, tg := cdb.standing.Load(), cdb.tagged.Load(); s != 3 || tg != 1 {
		t.Errorf("%d standing queries and %d tagged-state lookups, want 3 and 1", s, tg)
	}
}

func TestItemStandings_ReadError(t *testing.T) {
	db := newMigratedDB(t)
	standingFlow(t, db, "ErrBatchT", "live")
	_, err := ItemStandings(context.Background(), &standingReadFailDB2{DB: db}, "ErrBatchT", []string{"a"})
	if !errors.Is(err, ErrInternal) {
		t.Errorf("a failed read = %v, want ErrInternal", err)
	}
	if strings.Contains(fmt.Sprint(err), "ItemStandings") == false {
		t.Errorf("the error names the function: %v", err)
	}
}
