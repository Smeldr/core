// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"fmt"
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

		result.Rings = append(result.Rings, ring)
		frontier = nextFrontier
	}

	return result, nil
}

// reachabilityNeighbors returns the distinct nodes directly connected to node via
// kind (empty = all kinds), honoring direction, paired with the EdgeClass/Confidence
// of the edge that connects them. Mirrors the direction vocabulary already used by
// [RelationStore.MCPGetRelations]: "outgoing" walks edges where node is the source,
// "incoming" walks edges where node is the target, "both" unions both directions.
func (s *RelationStore) reachabilityNeighbors(ctx context.Context, node reachabilityNode, kind, direction string) ([]reachabilityCandidate, error) {
	var out []reachabilityCandidate

	if direction == "outgoing" || direction == "both" {
		edges, err := s.GetBySource(ctx, node.typ, node.id, kind)
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
		edges, err := s.GetByTarget(ctx, node.typ, node.id, kind)
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
