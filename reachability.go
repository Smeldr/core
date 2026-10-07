// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"fmt"
	"sort"
)

// MaxReachabilityDepth is the hard ceiling on [RelationStore.Reachability]'s maxDepth
// parameter — bounds worst-case query fanout on pathological graphs.
const MaxReachabilityDepth = 10

// ReachabilityItem identifies one item found during a reachability traversal,
// along with the EdgeClass/Confidence of the edge that reached it. When a node is
// reachable via more than one edge at the same hop distance, the most-trusted edge
// wins: "asserted" > "observed" > "inferred", then higher Confidence (nil treated
// as lowest) breaks a tie within the same EdgeClass.
type ReachabilityItem struct {
	Type       string   `json:"type"`
	ID         string   `json:"id"`
	EdgeClass  string   `json:"edge_class"`
	Confidence *float64 `json:"confidence,omitempty"`
}

// Bounds of [RelationStore.ReachabilityBounded]: the default and the ceiling on how
// many items one walk may return (and so on how many nodes it expands).
const (
	DefaultReachabilityItems = 500
	MaxReachabilityItems     = 2000
)

// ReachabilityCut says a bounded walk stopped before it was complete. Depth is
// the ring the cap landed in and Dropped the exact number of nodes found in that
// ring that were not returned. The ring at Depth is itself partial even when it
// is empty (the cap landed at a ring boundary and the next ring had more), so the
// rule that an empty ring is a genuine absence does not apply to it. Rings deeper
// than Depth were not walked and are not in the result (not empty rings: an empty
// ring means "nothing at that distance", and after a cut that is not known).
type ReachabilityCut struct {
	Depth   int `json:"depth"`
	Dropped int `json:"dropped"`
}

// ReachabilityRing is one hop-distance layer of a bounded reachability traversal:
// the items found at that distance from the anchor. A ring with zero Items is a
// genuine absence at that distance — not an error, not a missing ring.
type ReachabilityRing struct {
	Depth int                `json:"depth"`
	Items []ReachabilityItem `json:"items"`
}

// Reachability is the bounded transitive-closure result of [RelationStore.Reachability]:
// one ring per hop distance from 1 to the requested max depth, outward from an anchor.
type Reachability struct {
	AnchorType string             `json:"anchor_type"`
	AnchorID   string             `json:"anchor_id"`
	Kind       string             `json:"kind"`
	Direction  string             `json:"direction"`
	Rings      []ReachabilityRing `json:"rings"`
	// Cut is set only by [RelationStore.ReachabilityBounded], and only when its cap
	// stopped the walk. Nil means the walk is complete.
	Cut *ReachabilityCut `json:"cut,omitempty"`
}

// reachabilityNode identifies one graph node by (type, id) during traversal.
type reachabilityNode struct {
	typ string
	id  string
}

// reachabilityCandidate pairs a discovered node with the EdgeClass/Confidence of
// the edge that reached it, so the BFS can pick the most-trusted edge when a node
// is reachable via more than one edge at the same hop distance.
type reachabilityCandidate struct {
	node       reachabilityNode
	edgeClass  string
	confidence *float64
}

// edgeClassRank orders EdgeClass values by trust, highest first: asserted (a
// human's direct claim) > observed (a system directly witnessed it) > inferred
// (an agent's deduction). Unknown values rank lowest, alongside inferred.
func edgeClassRank(class string) int {
	switch class {
	case "asserted":
		return 2
	case "observed":
		return 1
	default:
		return 0
	}
}

// betterCandidate reports whether b should replace a as the representative edge
// for a node: higher EdgeClass rank wins outright; within the same rank, higher
// Confidence wins (nil is treated as lower than any set value).
func betterCandidate(a, b reachabilityCandidate) bool {
	ar, br := edgeClassRank(a.edgeClass), edgeClassRank(b.edgeClass)
	if br != ar {
		return br > ar
	}
	if b.confidence == nil {
		return false
	}
	if a.confidence == nil {
		return true
	}
	return *b.confidence > *a.confidence
}

// Reachability performs a bounded breadth-first traversal of the relation graph
// outward from (anchorType, anchorID), reporting which items are found at each hop
// distance from 1 to maxDepth. kind filters by relation kind (empty = all kinds);
// direction is "incoming", "outgoing", or "both" (same vocabulary as
// [RoleDefinition.ScopeDirection] and [RelationStore.MCPGetRelations]).
//
// A ring is returned for every depth from 1 to maxDepth, even after the frontier is
// exhausted — a ring with zero items is a genuine, reportable absence at that
// distance. This function only reports graph structure; interpreting what an
// absence means is the caller's concern, not this primitive's.
//
// Each node is placed in exactly one ring, at its shortest hop distance from the
// anchor (standard BFS visited-once semantics) — cycles and diamonds in the graph
// never cause a node to be revisited or double-counted.
//
// maxDepth must be between 1 and [MaxReachabilityDepth] inclusive.
func (s *RelationStore) Reachability(ctx context.Context, anchorType, anchorID, kind, direction string, maxDepth int) (*Reachability, error) {
	return s.reachability(ctx, anchorType, anchorID, kind, direction, maxDepth, 0)
}

// ReachabilityBounded is [RelationStore.Reachability] with a cap on how many items
// the walk returns: maxItems 0 means [DefaultReachabilityItems], a larger value is
// capped at [MaxReachabilityItems], a negative one is [ErrBadRequest]. Below the
// cap the result is exactly Reachability's. When the cap lands inside ring D the
// result holds the first items of that ring (sorted by type and id, so the choice
// is the same on every call), [Reachability.Cut] says how many were dropped, and no
// deeper ring is returned. A cap equal to the size of the walked component is not a
// cut. Cost: the previous ring is expanded in full before cutting and it holds at
// most maxItems nodes, so a walk costs at most maxItems expansions, one query per
// direction each. Use it where the result is sent to a caller; the unbounded
// Reachability is for in-process computations that need the whole walk.
func (s *RelationStore) ReachabilityBounded(ctx context.Context, anchorType, anchorID, kind, direction string, maxDepth, maxItems int) (*Reachability, error) {
	if maxItems < 0 {
		return nil, ErrBadRequest
	}
	if maxItems == 0 {
		maxItems = DefaultReachabilityItems
	}
	return s.reachability(ctx, anchorType, anchorID, kind, direction, maxDepth, min(maxItems, MaxReachabilityItems))
}

// reachability is the one walker behind both entry points. maxItems 0 means
// unbounded.
func (s *RelationStore) reachability(ctx context.Context, anchorType, anchorID, kind, direction string, maxDepth, maxItems int) (*Reachability, error) {
	if anchorType == "" || anchorID == "" {
		return nil, ErrBadRequest
	}
	if maxDepth < 1 || maxDepth > MaxReachabilityDepth {
		return nil, ErrBadRequest
	}
	switch direction {
	case "incoming", "outgoing", "both":
	default:
		return nil, ErrBadRequest
	}

	anchor := reachabilityNode{anchorType, anchorID}
	result := &Reachability{
		AnchorType: anchorType,
		AnchorID:   anchorID,
		Kind:       kind,
		Direction:  direction,
		Rings:      make([]ReachabilityRing, 0, maxDepth),
	}

	frontier := []reachabilityNode{anchor}
	seenNodes := map[reachabilityNode]bool{anchor: true}
	returned := 0

	for depth := 1; depth <= maxDepth; depth++ {
		var nextFrontier []reachabilityNode
		var order []reachabilityNode
		best := make(map[reachabilityNode]reachabilityCandidate)

		for _, node := range frontier {
			neighbors, err := s.reachabilityNeighbors(ctx, node, kind, direction)
			if err != nil {
				return nil, fmt.Errorf("smeldr: Reachability: %w", err)
			}
			for _, nb := range neighbors {
				if seenNodes[nb.node] {
					continue
				}
				existing, ok := best[nb.node]
				if !ok {
					order = append(order, nb.node)
					best[nb.node] = nb
					continue
				}
				if betterCandidate(existing, nb) {
					best[nb.node] = nb
				}
			}
		}

		cut := 0
		if maxItems > 0 && returned+len(order) > maxItems {
			// The cap lands in this ring: keep a stable choice, count the rest.
			sort.Slice(order, func(i, j int) bool {
				if order[i].typ != order[j].typ {
					return order[i].typ < order[j].typ
				}
				return order[i].id < order[j].id
			})
			keep := maxItems - returned
			cut = len(order) - keep
			order = order[:keep]
		}
		ring := ReachabilityRing{Depth: depth, Items: []ReachabilityItem{}}
		for _, n := range order {
			seenNodes[n] = true
			c := best[n]
			ring.Items = append(ring.Items, ReachabilityItem{
				Type: n.typ, ID: n.id,
				EdgeClass: c.edgeClass, Confidence: c.confidence,
			})
			nextFrontier = append(nextFrontier, n)
		}
		returned += len(ring.Items)

		result.Rings = append(result.Rings, ring)
		if cut > 0 {
			result.Cut = &ReachabilityCut{Depth: depth, Dropped: cut}
			break
		}
		frontier = nextFrontier
	}

	return result, nil
}

// MCPReachability is a thin passthrough to [RelationStore.Reachability], kept for
// mcp's uniform MCPXxx naming. The MCP tool get_reachability uses
// [RelationStore.ReachabilityBounded], not this: a result sent to a caller is capped.
func (s *RelationStore) MCPReachability(ctx context.Context, anchorType, anchorID, kind, direction string, maxDepth int) (*Reachability, error) {
	return s.Reachability(ctx, anchorType, anchorID, kind, direction, maxDepth)
}

// reachabilityNeighbors returns the distinct nodes directly connected to node via
// kind (empty = all kinds), honoring direction, paired with the EdgeClass/Confidence
// of the edge that connects them. Mirrors the direction vocabulary already used by
// [RelationStore.MCPGetRelations]: "outgoing" walks edges where node is the source,
// "incoming" walks edges where node is the target, "both" unions both directions.
func (s *RelationStore) reachabilityNeighbors(ctx context.Context, node reachabilityNode, kind, direction string) ([]reachabilityCandidate, error) {
	var out []reachabilityCandidate

	if direction == "outgoing" || direction == "both" {
		edges, err := s.GetLiveBySource(ctx, node.typ, node.id, kind)
		if err != nil {
			return nil, err
		}
		for _, e := range edges {
			out = append(out, reachabilityCandidate{
				node:       reachabilityNode{e.TargetType, e.TargetID},
				edgeClass:  e.EdgeClass,
				confidence: e.Confidence,
			})
		}
	}
	if direction == "incoming" || direction == "both" {
		edges, err := s.GetLiveByTarget(ctx, node.typ, node.id, kind)
		if err != nil {
			return nil, err
		}
		for _, e := range edges {
			out = append(out, reachabilityCandidate{
				node:       reachabilityNode{e.SourceType, e.SourceID},
				edgeClass:  e.EdgeClass,
				confidence: e.Confidence,
			})
		}
	}
	return out, nil
}
