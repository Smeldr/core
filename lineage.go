// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"fmt"
	"strings"
)

// lineageWalkKinds are the relation kinds [RelationStore.TraceLineage] walks
// upstream from the traced item toward its premises. supersedesKind is
// walked separately, backward, only ever to find a visited node's
// replacement — never as an outgoing edge of the traced item itself.
var lineageWalkKinds = []string{"depends_on", "derives_from"}

// supersedesKind is the relation kind recorded from a replacement item to
// the item it replaced (see [conflictSupersede] in state.go). TraceLineage
// follows it backward: from a visited node to whatever superseded it.
const supersedesKind = "supersedes"

// MaxLineageDepth bounds [RelationStore.TraceLineage]'s maxDepth parameter —
// same ceiling as [MaxReachabilityDepth], for consistency; lineage chains in
// practice are much shorter, but there is no reason to give this primitive a
// different limit.
const MaxLineageDepth = 10

// LineageNode is one item found while tracing upstream from the anchor in
// [RelationStore.TraceLineage].
type LineageNode struct {
	Type         string   `json:"type"`
	ID           string   `json:"id"`
	Depth        int      `json:"depth"`
	RelationKind string   `json:"relation_kind"` // depends_on | derives_from | supersedes
	EdgeClass    string   `json:"edge_class"`
	Confidence   *float64 `json:"confidence,omitempty"`

	// Invalidated is true when the edge that reached this node currently has
	// InvalidAt set. TraceLineage still follows an invalidated edge rather
	// than stopping at it (D35 guard 3) — this only flags it in the result.
	Invalidated bool `json:"invalidated"`

	// Superseded is true when this node is itself the target of a
	// supersedes edge — its replacement was found and is also present in
	// the trace, at the same Depth (see [RelationStore.TraceLineage]).
	Superseded bool `json:"superseded,omitempty"`
}

// LineageTrace is the result of [RelationStore.TraceLineage].
type LineageTrace struct {
	AnchorType string        `json:"anchor_type"`
	AnchorID   string        `json:"anchor_id"`
	Nodes      []LineageNode `json:"nodes"`

	// Truncated is true when the walk still had unexplored frontier at
	// maxDepth — an explicit signal, never a silent cutoff (D35 guard 2).
	Truncated bool `json:"truncated"`
}

// TraceLineage walks depends_on/derives_from edges upstream from
// (anchorType, anchorID) up to maxDepth hops, reporting every item found
// along the way. Three guards, per D35:
//
//  1. Cycle detection via a visited-set — a node already in the trace is
//     never revisited or double-counted.
//  2. Explicit truncation: Truncated is set when the walk still had
//     unexplored frontier at maxDepth, never a silent cutoff.
//  3. Invalidated edges are followed, not stopped at — flagged via
//     LineageNode.Invalidated instead.
//
// Additionally, when a visited node is itself the target of a supersedes
// edge, TraceLineage follows it to the replacement: the replacement is
// recorded at the same Depth as the superseded node (a lateral step — a
// node's supersede history is revision metadata about its identity, not a
// new premise in the reasoning chain) and its own depends_on/derives_from
// edges are then walked starting at the next depth, same as any other node.
//
// TraceLineage does not verify that the anchor item exists — like
// [RelationStore.Reachability], it reports graph structure only; an anchor
// with no edges returns an empty, non-truncated trace.
//
// maxDepth must be between 1 and [MaxLineageDepth] inclusive.
func (s *RelationStore) TraceLineage(ctx context.Context, anchorType, anchorID string, maxDepth int) (*LineageTrace, error) {
	if anchorType == "" || anchorID == "" {
		return nil, ErrBadRequest
	}
	if maxDepth < 1 || maxDepth > MaxLineageDepth {
		return nil, ErrBadRequest
	}

	anchor := reachabilityNode{anchorType, anchorID}
	trace := &LineageTrace{
		AnchorType: anchorType,
		AnchorID:   anchorID,
		Nodes:      []LineageNode{},
	}

	visited := map[reachabilityNode]bool{anchor: true}

	replacements, err := s.followSupersedes(ctx, []reachabilityNode{anchor}, 0, visited, trace)
	if err != nil {
		return nil, fmt.Errorf("smeldr: TraceLineage: %w", err)
	}
	frontier := append([]reachabilityNode{anchor}, replacements...)

	for depth := 1; depth <= maxDepth; depth++ {
		edges, err := s.batchEdgesBySource(ctx, frontier, lineageWalkKinds)
		if err != nil {
			return nil, fmt.Errorf("smeldr: TraceLineage: %w", err)
		}

		var order []reachabilityNode
		best := make(map[reachabilityNode]reachabilityCandidate)
		bestKind := make(map[reachabilityNode]string)
		bestInvalidated := make(map[reachabilityNode]bool)
		for _, e := range edges {
			target := reachabilityNode{e.TargetType, e.TargetID}
			if visited[target] {
				continue
			}
			cand := reachabilityCandidate{node: target, edgeClass: e.EdgeClass, confidence: e.Confidence}
			existing, ok := best[target]
			if !ok || betterCandidate(existing, cand) {
				if !ok {
					order = append(order, target)
				}
				best[target] = cand
				bestKind[target] = e.RelationKind
				bestInvalidated[target] = e.InvalidAt != nil
			}
		}

		var newNodes []reachabilityNode
		for _, n := range order {
			visited[n] = true
			newNodes = append(newNodes, n)
			c := best[n]
			trace.Nodes = append(trace.Nodes, LineageNode{
				Type: n.typ, ID: n.id, Depth: depth,
				RelationKind: bestKind[n], EdgeClass: c.edgeClass, Confidence: c.confidence,
				Invalidated: bestInvalidated[n],
			})
		}

		replacements, err := s.followSupersedes(ctx, newNodes, depth, visited, trace)
		if err != nil {
			return nil, fmt.Errorf("smeldr: TraceLineage: %w", err)
		}

		nextFrontier := append(newNodes, replacements...)
		if len(nextFrontier) == 0 {
			break
		}
		if depth == maxDepth {
			// The frontier being non-empty here does not by itself mean the
			// walk was truncated — these nodes might simply have no further
			// edges of their own. Peek one level further (without recording
			// anything into trace.Nodes) to answer that honestly, per D35
			// guard 2: Truncated must mean "the chain continues past what
			// you saw," not "there happened to be a frontier at the limit."
			truncated, err := s.hasFurtherLineage(ctx, nextFrontier, visited)
			if err != nil {
				return nil, fmt.Errorf("smeldr: TraceLineage: %w", err)
			}
			trace.Truncated = truncated
		}
		frontier = nextFrontier
	}

	return trace, nil
}

// hasFurtherLineage reports whether frontier has any depends_on/derives_from
// edge to a not-yet-visited target, or any incoming supersedes edge from a
// not-yet-visited replacement — i.e. whether TraceLineage's walk was cut
// short by maxDepth rather than having naturally exhausted the graph. Used
// only at the depth boundary; does not record anything into the trace.
func (s *RelationStore) hasFurtherLineage(ctx context.Context, frontier []reachabilityNode, visited map[reachabilityNode]bool) (bool, error) {
	edges, err := s.batchEdgesBySource(ctx, frontier, lineageWalkKinds)
	if err != nil {
		return false, err
	}
	for _, e := range edges {
		if !visited[reachabilityNode{e.TargetType, e.TargetID}] {
			return true, nil
		}
	}

	supersedeEdges, err := s.batchEdgesByTarget(ctx, frontier, []string{supersedesKind})
	if err != nil {
		return false, err
	}
	for _, e := range supersedeEdges {
		if !visited[reachabilityNode{e.SourceType, e.SourceID}] {
			return true, nil
		}
	}

	return false, nil
}

// followSupersedes fully resolves the supersede chain starting from nodes:
// if a node was superseded, its replacement is found; if that replacement
// was itself later superseded, its own replacement is found next, and so on
// until a current (not-further-superseded) node is reached. Each superseded
// node's existing LineageNode entry in trace is marked, and every
// replacement found — at any point in the chain — is recorded at the same
// Depth (see [RelationStore.TraceLineage]) and returned so the caller can
// fold it into the same depth's frontier. nodes may be empty (a no-op). The
// anchor itself (depth 0) is never present in trace.Nodes, so a superseded
// anchor is followed to its replacement but produces no Superseded flag —
// there is no anchor entry to flag.
//
// Chain resolution is capped at [MaxLineageDepth] hops, reusing the same
// ceiling as the main upstream walk: fully resolving a supersede chain is
// what makes the replacement found actually current rather than one
// revision behind (see plan/D35 discussion), but unlike a single hop it is
// not otherwise bounded by maxDepth — a pathologically long revision
// history (a Decision superseded many times over) would run this loop
// unbounded, defeating the bounded-cost guarantee even though the
// visited-set already guarantees it would eventually terminate. Hitting the
// cap sets trace.Truncated, the same explicit signal used for the
// hop-depth limit — the caller is told the same way either way that the
// trace stopped short of the full picture.
func (s *RelationStore) followSupersedes(ctx context.Context, nodes []reachabilityNode, depth int, visited map[reachabilityNode]bool, trace *LineageTrace) ([]reachabilityNode, error) {
	var allReplacements []reachabilityNode
	checking := nodes

	for hop := 0; len(checking) > 0; hop++ {
		if hop >= MaxLineageDepth {
			trace.Truncated = true
			break
		}

		edges, err := s.batchEdgesByTarget(ctx, checking, []string{supersedesKind})
		if err != nil {
			return nil, err
		}
		if len(edges) == 0 {
			break
		}

		supersededTargets := make(map[reachabilityNode]bool)
		var newReplacements []reachabilityNode
		for _, e := range edges {
			target := reachabilityNode{e.TargetType, e.TargetID}
			supersededTargets[target] = true

			replacement := reachabilityNode{e.SourceType, e.SourceID}
			if visited[replacement] {
				continue
			}
			visited[replacement] = true
			newReplacements = append(newReplacements, replacement)
			trace.Nodes = append(trace.Nodes, LineageNode{
				Type: replacement.typ, ID: replacement.id, Depth: depth,
				RelationKind: supersedesKind, EdgeClass: e.EdgeClass, Confidence: e.Confidence,
				Invalidated: e.InvalidAt != nil,
			})
		}

		for i := range trace.Nodes {
			if supersededTargets[reachabilityNode{trace.Nodes[i].Type, trace.Nodes[i].ID}] {
				trace.Nodes[i].Superseded = true
			}
		}

		allReplacements = append(allReplacements, newReplacements...)
		// Chase the newly-found replacements for further supersession —
		// visited already prevents a malformed cyclical chain from looping.
		checking = newReplacements
	}

	return allReplacements, nil
}

// batchEdgesBySource returns every RelationEdge whose (source_type,
// source_id) matches one of nodes and whose relation_kind is in kinds, in
// one query per distinct source type present in nodes — the batched IN(...)
// technique named in D35, applied per BFS depth level instead of per node.
func (s *RelationStore) batchEdgesBySource(ctx context.Context, nodes []reachabilityNode, kinds []string) ([]RelationEdge, error) {
	return s.batchEdgesByTypeAndIDs(ctx, true, nodes, kinds)
}

// batchEdgesByTarget is the target-side mirror of batchEdgesBySource, used
// to find supersedes edges pointing at newly-discovered nodes.
func (s *RelationStore) batchEdgesByTarget(ctx context.Context, nodes []reachabilityNode, kinds []string) ([]RelationEdge, error) {
	return s.batchEdgesByTypeAndIDs(ctx, false, nodes, kinds)
}

// batchEdgesByTypeAndIDs groups nodes by type and issues one query per group
// (bySource selects source_type/source_id vs target_type/target_id),
// restricted to relation_kind IN kinds. Grouping by type keeps the query a
// plain single-column IN(...) list rather than a composite (type, id)
// row-value IN(...), which is not verified portable across this package's
// SQLite and pgx backends.
func (s *RelationStore) batchEdgesByTypeAndIDs(ctx context.Context, bySource bool, nodes []reachabilityNode, kinds []string) ([]RelationEdge, error) {
	// kinds is always a fixed, non-empty literal at every call site
	// (lineageWalkKinds or []string{supersedesKind}) — this guard exists to
	// avoid ever generating "relation_kind IN ()", invalid SQL, not to
	// handle a real caller. nodes emptiness needs no such guard: the loop
	// below already returns (nil, nil) for it without one.
	if len(kinds) == 0 {
		return nil, nil
	}

	typeCol, idCol := "target_type", "target_id"
	if bySource {
		typeCol, idCol = "source_type", "source_id"
	}

	byType := make(map[string][]string)
	var typeOrder []string
	for _, n := range nodes {
		if _, ok := byType[n.typ]; !ok {
			typeOrder = append(typeOrder, n.typ)
		}
		byType[n.typ] = append(byType[n.typ], n.id)
	}

	var out []RelationEdge
	for _, typ := range typeOrder {
		ids := byType[typ]
		args := make([]any, 0, len(ids)+len(kinds)+1)
		args = append(args, typ)

		idPH := make([]string, len(ids))
		for i, id := range ids {
			args = append(args, id)
			idPH[i] = fmt.Sprintf("$%d", len(args))
		}
		kindPH := make([]string, len(kinds))
		for i, k := range kinds {
			args = append(args, k)
			kindPH[i] = fmt.Sprintf("$%d", len(args))
		}

		query := "SELECT " + relationColumns + " FROM smeldr_relations WHERE " +
			typeCol + "=$1 AND " + idCol + " IN (" + strings.Join(idPH, ", ") + ")" +
			" AND relation_kind IN (" + strings.Join(kindPH, ", ") + ")"

		rows, err := s.db.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, err
		}
		edges, err := collectEdges(rows)
		rows.Close()
		if err != nil {
			return nil, err
		}
		out = append(out, edges...)
	}
	return out, nil
}
