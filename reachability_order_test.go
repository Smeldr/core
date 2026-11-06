package smeldr

import (
	"context"
	"testing"
	"time"
)

// TestBetterCandidate covers every branch of the representative-edge choice
// directly, so no branch depends on the order rows come back in (T1160).
func TestBetterCandidate(t *testing.T) {
	low, high := 0.2, 0.9
	c := func(class string, conf *float64) reachabilityCandidate {
		return reachabilityCandidate{edgeClass: class, confidence: conf}
	}
	for _, tc := range []struct {
		name string
		a, b reachabilityCandidate
		want bool
	}{
		{"higher rank wins", c("inferred", nil), c("asserted", nil), true},
		{"lower rank loses", c("observed", &high), c("inferred", &high), false},
		{"same rank, b has none", c("inferred", &low), c("inferred", nil), false},
		{"same rank, only b has one", c("inferred", nil), c("inferred", &low), true},
		{"same rank, b higher", c("inferred", &low), c("inferred", &high), true},
		{"same rank, b lower", c("inferred", &high), c("inferred", &low), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := betterCandidate(tc.a, tc.b); got != tc.want {
				t.Errorf("betterCandidate = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestReachability_StrongerEdgeKeptInEitherOrder: when one node is reached by
// an inferred and an asserted edge, the asserted one is the node's edge
// whichever is met first. The two edges get fixed ids in both orders, so the
// replacement path runs in one case and the keep path in the other, every
// run (T1160).
func TestReachability_StrongerEdgeKeptInEitherOrder(t *testing.T) {
	for _, order := range [][2]string{{"inferred", "asserted"}, {"asserted", "inferred"}} {
		t.Run(order[0]+" first", func(t *testing.T) {
			store := setupRelationStore(t)
			ctx := context.Background()
			upsertTestKind(t, store, "kind_a", "post", "post")
			for i, class := range order {
				if _, err := store.db.ExecContext(ctx,
					`INSERT INTO smeldr_relations (id, source_type, source_id, target_type, target_id, relation_kind, edge_class, attributes, created_at, updated_at)
					 VALUES ($1, 'post', 'p1', 'post', 'shared', 'kind_a', $2, '{}', $3, $3)`,
					[]string{"00000000000000000000000001", "00000000000000000000000002"}[i], class,
					time.Now().UTC().Add(time.Duration(i-2)*time.Second)); err != nil {
					t.Fatalf("insert %s edge: %v", class, err)
				}
			}
			got, err := store.Reachability(ctx, "post", "p1", "", "outgoing", 1)
			if err != nil {
				t.Fatalf("Reachability: %v", err)
			}
			items := got.Rings[0].Items
			if len(items) != 1 || items[0].EdgeClass != "asserted" {
				t.Errorf("items = %+v; want one, asserted", items)
			}
		})
	}
}
