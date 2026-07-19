// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"fmt"
)

// MaxReachabilityDepth is the hard ceiling on [RelationStore.Reachability]'s maxDepth
// parameter — bounds worst-case query fanout on pathological graphs.
const MaxReachabilityDepth = 10

// ReachabilityItem identifies one item found during a reachability traversal.
type ReachabilityItem struct {
	Type string `json:"type"`
	ID   string `json:"id"`
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
}

// reachabilityNode identifies one graph node by (type, id) during traversal.
type reachabilityNode struct {
	typ string
	id  string
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

	for depth := 1; depth <= maxDepth; depth++ {
		var nextFrontier []reachabilityNode
		ring := ReachabilityRing{Depth: depth, Items: []ReachabilityItem{}}

		for _, node := range frontier {
			neighbors, err := s.reachabilityNeighbors(ctx, node, kind, direction)
			if err != nil {
				return nil, fmt.Errorf("smeldr: Reachability: %w", err)
			}
			for _, nb := range neighbors {
				if seenNodes[nb] {
					continue
				}
				seenNodes[nb] = true
				ring.Items = append(ring.Items, ReachabilityItem{Type: nb.typ, ID: nb.id})
				nextFrontier = append(nextFrontier, nb)
			}
		}

		result.Rings = append(result.Rings, ring)
		frontier = nextFrontier
	}

	return result, nil
}

// reachabilityNeighbors returns the distinct nodes directly connected to node via
// kind (empty = all kinds), honoring direction. Mirrors the direction vocabulary
// already used by [RelationStore.MCPGetRelations]: "outgoing" walks edges where
// node is the source, "incoming" walks edges where node is the target, "both"
// unions both directions.
func (s *RelationStore) reachabilityNeighbors(ctx context.Context, node reachabilityNode, kind, direction string) ([]reachabilityNode, error) {
	var out []reachabilityNode

	if direction == "outgoing" || direction == "both" {
		edges, err := s.GetBySource(ctx, node.typ, node.id, kind)
		if err != nil {
			return nil, err
		}
		for _, e := range edges {
			out = append(out, reachabilityNode{e.TargetType, e.TargetID})
		}
	}
	if direction == "incoming" || direction == "both" {
		edges, err := s.GetByTarget(ctx, node.typ, node.id, kind)
		if err != nil {
			return nil, err
		}
		for _, e := range edges {
			out = append(out, reachabilityNode{e.SourceType, e.SourceID})
		}
	}
	return out, nil
}
