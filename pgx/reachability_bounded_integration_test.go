//go:build integration

package pgx

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	smeldr "smeldr.dev/core"
)

// RelationStore.ReachabilityBounded on Postgres: below the cap it equals the
// unbounded walk (live edges only, an ended edge in the middle of a path is not
// walked), and a cap inside a ring reports its cut with the true leftover count.
func TestPG_ReachabilityBounded(t *testing.T) {
	db, _ := isolatedDB(t)
	ctx := context.Background()
	if err := smeldr.CreateRelationTables(db); err != nil {
		t.Fatalf("CreateRelationTables: %v", err)
	}
	store, err := smeldr.NewRelationStore(db)
	if err != nil {
		t.Fatalf("NewRelationStore: %v", err)
	}
	pairs, _ := json.Marshal([]map[string]string{{"source_type": "item", "target_type": "item"}})
	if err := store.UpsertKind(ctx, smeldr.RelationKindDef{TypeName: "related_to", Mode: "asserted", Directional: true, TypePairs: json.RawMessage(pairs)}); err != nil {
		t.Fatalf("UpsertKind: %v", err)
	}
	assert := func(src, tgt string) {
		t.Helper()
		if err := store.Assert(ctx, smeldr.RelationEdge{SourceType: "item", SourceID: src, TargetType: "item", TargetID: tgt, RelationKind: "related_to", EdgeClass: "asserted"}); err != nil {
			t.Fatalf("Assert %s->%s: %v", src, tgt, err)
		}
	}
	for i := 0; i < 10; i++ {
		assert("a", fmt.Sprintf("c%02d", i))
		assert(fmt.Sprintf("c%02d", i), fmt.Sprintf("g%02d", i))
	}
	past := time.Now().Add(-time.Hour).UTC()
	if err := store.Assert(ctx, smeldr.RelationEdge{SourceType: "item", SourceID: "g00", TargetType: "item", TargetID: "ended", RelationKind: "related_to", EdgeClass: "asserted", InvalidAt: &past}); err != nil {
		t.Fatalf("Assert ended edge: %v", err)
	}

	full, err := store.Reachability(ctx, "item", "a", "", "outgoing", 4)
	if err != nil {
		t.Fatalf("Reachability: %v", err)
	}
	below, err := store.ReachabilityBounded(ctx, "item", "a", "", "outgoing", 4, 0)
	if err != nil || below.Cut != nil || len(below.Rings) != len(full.Rings) {
		t.Fatalf("below the cap = %+v, %v", below, err)
	}
	for i := range full.Rings {
		if len(full.Rings[i].Items) != len(below.Rings[i].Items) {
			t.Errorf("ring %d: %d vs %d items", i+1, len(full.Rings[i].Items), len(below.Rings[i].Items))
		}
		for _, it := range below.Rings[i].Items {
			if it.ID == "ended" {
				t.Error("an ended edge was walked on Postgres")
			}
		}
	}

	cut, err := store.ReachabilityBounded(ctx, "item", "a", "", "outgoing", 4, 14)
	if err != nil || cut.Cut == nil || cut.Cut.Depth != 2 || cut.Cut.Dropped != 6 || len(cut.Rings) != 2 {
		t.Fatalf("cut = %+v cut %+v, %v, want depth 2, dropped 6, two rings", cut.Rings, cut.Cut, err)
	}
	var keys []string
	for _, it := range cut.Rings[1].Items {
		keys = append(keys, it.ID)
	}
	if strings.Join(keys, ",") != "g00,g01,g02,g03" {
		t.Errorf("kept in the cut ring = %v, want the first four by id", keys)
	}
}
