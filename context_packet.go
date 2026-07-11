// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	packetVersion    = "1.0"
	packetPerTypeCap = 25
)

// ContextPacket is the v1 bounded operational context envelope.
// Built by [BuildContextPacket] and served by [App.ContextPacketHandler].
type ContextPacket struct {
	PacketVersion string           `json:"packet_version"`
	GeneratedAt   time.Time        `json:"generated_at"`
	Source        PacketSource     `json:"source"`
	Anchor        PacketAnchor     `json:"anchor"`
	Boundary      PacketBoundary   `json:"boundary"`
	Items         []PacketItem     `json:"items"`
	Relations     []PacketRelation `json:"relations"`
}

// PacketSource identifies the Smeldr instance that produced the packet.
type PacketSource struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// PacketAnchor is the focal item the packet was built around.
// Carries the same content fields as [PacketItem]; the anchor is never
// duplicated into Items.
type PacketAnchor struct {
	Type   string         `json:"type"`
	ID     string         `json:"id"`
	Slug   string         `json:"slug"`
	Status string         `json:"status"`
	Rev    int            `json:"rev"`
	URL    string         `json:"url"`
	Fields map[string]any `json:"fields"`
}

// PacketBoundary declares how the packet was assembled.
type PacketBoundary struct {
	Method  string                     `json:"method"`
	Depth   int                        `json:"depth"`
	Omitted map[string]*PacketOmission `json:"omitted,omitempty"`
}

// PacketOmission records included and capped-out item counts for one type.
type PacketOmission struct {
	Included int `json:"included"`
	Omitted  int `json:"omitted"`
}

// PacketItem is one linked content node in the packet, not the anchor.
type PacketItem struct {
	Type   string         `json:"type"`
	ID     string         `json:"id"`
	Slug   string         `json:"slug"`
	Status string         `json:"status"`
	Rev    int            `json:"rev"`
	URL    string         `json:"url"`
	Fields map[string]any `json:"fields"`
}

// PacketRelation is one edge from the relation graph included in the packet.
// Only emitted when both endpoints are present as the Anchor or in Items.
type PacketRelation struct {
	SourceType string `json:"source_type"`
	SourceID   string `json:"source_id"`
	TargetType string `json:"target_type"`
	TargetID   string `json:"target_id"`
	Kind       string `json:"kind"`
}

type anchorTypeEntry struct {
	table        string
	path         string
	relationType string // capitalized Go type name used in RelationStore edges
}

var anchorTypeTable = map[string]anchorTypeEntry{
	"goal":      {"smeldr_goals", "/goals", "Goal"},
	"decision":  {"smeldr_decisions", "/decisions", "Decision"},
	"amendment": {"smeldr_amendments", "/amendments", "Amendment"},
	"task":      {"smeldr_tasks", "/tasks", "Task"},
	"signal":    {"smeldr_signals", "/signals", "Signal"},
}

func packetItemURL(baseURL, typePath, slug string) string {
	return baseURL + typePath + "/" + slug
}

// packetFieldsFromItem extracts the canonical ID and type-specific fields map
// from one of the five orchestration struct types.
func packetFieldsFromItem(item any) (canonicalID string, fields map[string]any) {
	switch v := item.(type) {
	case *Goal:
		return v.GoalID, map[string]any{
			"goal_id":     v.GoalID,
			"priority":    v.Priority,
			"band":        v.Band,
			"size":        v.Size,
			"description": v.Description,
		}
	case *Decision:
		return v.DecisionNumber, map[string]any{
			"decision_number": v.DecisionNumber,
			"scope":           v.Scope,
			"body":            v.Body,
			"next_eval_at":    v.NextEvalAt,
			"eval_note":       v.EvalNote,
		}
	case *Amendment:
		return v.AmendmentNumber, map[string]any{
			"amendment_number": v.AmendmentNumber,
			"amendment_type":   v.AmendmentType,
			"version":          v.Version,
			"commit_hash":      v.CommitHash,
			"pilot":            v.Pilot,
			"summary":          v.Summary,
		}
	case *Task:
		return v.TaskID, map[string]any{
			"task_id":     v.TaskID,
			"priority":    v.Priority,
			"band":        v.Band,
			"size":        v.Size,
			"description": v.Description,
			"note_ref":    v.NoteRef,
		}
	case *Signal:
		return v.Slug, map[string]any{
			"sender":      v.Sender,
			"receiver":    v.Receiver,
			"signal_type": v.SignalType,
			"message":     v.Message,
			"task_ref":    v.TaskRef,
			"sequence":    v.Sequence,
		}
	default:
		return "", nil
	}
}

// packetFetchItem fetches one orchestration item from table by col=val.
// typeName is lowercase ("goal", "decision", etc.).
// Returns (canonicalID, Node, fields, error).
func packetFetchItem(ctx context.Context, db DB, typeName, table, col, val string) (string, Node, map[string]any, error) {
	q := "SELECT * FROM " + table + " WHERE " + col + " = $1"
	switch typeName {
	case "goal":
		it, err := QueryOne[*Goal](ctx, db, q, val)
		if err != nil {
			return "", Node{}, nil, err
		}
		cid, f := packetFieldsFromItem(it)
		return cid, it.Node, f, nil
	case "decision":
		it, err := QueryOne[*Decision](ctx, db, q, val)
		if err != nil {
			return "", Node{}, nil, err
		}
		cid, f := packetFieldsFromItem(it)
		return cid, it.Node, f, nil
	case "amendment":
		it, err := QueryOne[*Amendment](ctx, db, q, val)
		if err != nil {
			return "", Node{}, nil, err
		}
		cid, f := packetFieldsFromItem(it)
		return cid, it.Node, f, nil
	case "task":
		it, err := QueryOne[*Task](ctx, db, q, val)
		if err != nil {
			return "", Node{}, nil, err
		}
		cid, f := packetFieldsFromItem(it)
		return cid, it.Node, f, nil
	case "signal":
		it, err := QueryOne[*Signal](ctx, db, q, val)
		if err != nil {
			return "", Node{}, nil, err
		}
		cid, f := packetFieldsFromItem(it)
		return cid, it.Node, f, nil
	default:
		return "", Node{}, nil, ErrBadRequest
	}
}

// BuildContextPacket assembles a bounded [ContextPacket] for any of the five
// orchestration anchor types (goal, decision, amendment, task, signal).
//
// depth controls breadth-first traversal of the relation graph (minimum 1,
// maximum 2). Each type is capped at [packetPerTypeCap] items per packet.
// Relations are only emitted when both endpoints are present as the Anchor or
// in Items.
//
// [QueryGoalContext] remains unchanged for backward compatibility; this
// function is the generalized successor for all five anchor types.
func BuildContextPacket(
	ctx context.Context,
	db DB,
	rs *RelationStore,
	baseURL, sourceName, anchorType, anchorSlug string,
	depth int,
) (*ContextPacket, error) {
	if db == nil {
		return nil, ErrInternal
	}
	entry, ok := anchorTypeTable[anchorType]
	if !ok || anchorSlug == "" {
		return nil, ErrBadRequest
	}
	if depth < 1 || depth > 2 {
		return nil, ErrBadRequest
	}

	anchorCID, anchorNode, anchorFields, err := packetFetchItem(ctx, db, anchorType, entry.table, "slug", anchorSlug)
	if err != nil {
		return nil, err
	}
	if anchorNode.Status != Published {
		return nil, ErrNotFound
	}

	pkt := &ContextPacket{
		PacketVersion: packetVersion,
		GeneratedAt:   time.Now().UTC(),
		Source:        PacketSource{Name: sourceName, URL: baseURL},
		Anchor: PacketAnchor{
			Type:   anchorType,
			ID:     anchorCID,
			Slug:   anchorNode.Slug,
			Status: string(anchorNode.Status),
			Rev:    anchorNode.Rev,
			URL:    packetItemURL(baseURL, entry.path, anchorNode.Slug),
			Fields: anchorFields,
		},
		Boundary:  PacketBoundary{Method: "relations", Depth: depth},
		Items:     []PacketItem{},
		Relations: []PacketRelation{},
	}

	if rs == nil {
		return pkt, nil
	}

	type frontierItem struct {
		relType string
		nodeID  string
	}

	frontier := []frontierItem{{entry.relationType, anchorNode.ID}}
	seenEdges := map[string]bool{}
	seenNodes := map[string]bool{entry.relationType + anchorNode.ID: true}

	// typeRefs accumulates linked items to fetch, keyed by lowercase type name.
	typeRefs := map[string][]frontierItem{}
	var includedEdges []RelationEdge

	for d := 1; d <= depth; d++ {
		var nextFrontier []frontierItem
		for _, fi := range frontier {
			srcEdges, err := rs.GetBySource(ctx, fi.relType, fi.nodeID, "")
			if err != nil {
				return nil, err
			}
			tgtEdges, err := rs.GetByTarget(ctx, fi.relType, fi.nodeID, "")
			if err != nil {
				return nil, err
			}
			for _, edge := range append(srcEdges, tgtEdges...) {
				if seenEdges[edge.ID] {
					continue
				}
				seenEdges[edge.ID] = true
				includedEdges = append(includedEdges, edge)

				var refRelType, refNodeID string
				if edge.SourceType == fi.relType && edge.SourceID == fi.nodeID {
					refRelType, refNodeID = edge.TargetType, edge.TargetID
				} else {
					refRelType, refNodeID = edge.SourceType, edge.SourceID
				}

				nodeKey := refRelType + refNodeID
				if seenNodes[nodeKey] {
					continue
				}
				seenNodes[nodeKey] = true

				lowerType := strings.ToLower(refRelType)
				typeRefs[lowerType] = append(typeRefs[lowerType], frontierItem{refRelType, refNodeID})
				nextFrontier = append(nextFrontier, frontierItem{refRelType, refNodeID})
			}
		}
		frontier = nextFrontier
	}

	// resolvedItems maps relType+nodeID → canonicalID for relation boundary checks.
	resolvedItems := map[string]string{entry.relationType + anchorNode.ID: anchorCID}

	omitted := map[string]*PacketOmission{}

	for lowerType, refs := range typeRefs {
		linkedEntry, ok := anchorTypeTable[lowerType]
		if !ok {
			continue
		}
		total := len(refs)
		limit := total
		if limit > packetPerTypeCap {
			limit = packetPerTypeCap
		}

		included := 0
		for _, ref := range refs[:limit] {
			cid, nd, f, fetchErr := packetFetchItem(ctx, db, lowerType, linkedEntry.table, "id", ref.nodeID)
			if fetchErr != nil {
				slog.WarnContext(ctx, "smeldr: BuildContextPacket: skipping linked item",
					"type", lowerType, "id", ref.nodeID, "error", fetchErr)
				continue
			}
			if nd.Status != Published {
				continue
			}
			pkt.Items = append(pkt.Items, PacketItem{
				Type:   lowerType,
				ID:     cid,
				Slug:   nd.Slug,
				Status: string(nd.Status),
				Rev:    nd.Rev,
				URL:    packetItemURL(baseURL, linkedEntry.path, nd.Slug),
				Fields: f,
			})
			resolvedItems[ref.relType+ref.nodeID] = cid
			included++
		}
		if total > limit {
			omitted[lowerType] = &PacketOmission{
				Included: included,
				Omitted:  total - limit,
			}
		}
	}
	if len(omitted) > 0 {
		pkt.Boundary.Omitted = omitted
	}

	for _, edge := range includedEdges {
		srcCID, srcOK := resolvedItems[edge.SourceType+edge.SourceID]
		tgtCID, tgtOK := resolvedItems[edge.TargetType+edge.TargetID]
		if !srcOK || !tgtOK {
			continue
		}
		pkt.Relations = append(pkt.Relations, PacketRelation{
			SourceType: strings.ToLower(edge.SourceType),
			SourceID:   srcCID,
			TargetType: strings.ToLower(edge.TargetType),
			TargetID:   tgtCID,
			Kind:       edge.RelationKind,
		})
	}

	return pkt, nil
}

// ContextPacketHandler registers GET /packet/{type}/{slug} on the app's mux.
// The optional ?depth= query parameter controls traversal depth (1 or 2;
// defaults to 1). sourceName identifies the instance in the packet's Source field.
func (a *App) ContextPacketHandler(rs *RelationStore, sourceName string) {
	a.mux.Handle("GET /packet/{type}/{slug}", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		anchorType := r.PathValue("type")
		anchorSlug := r.PathValue("slug")
		depth := 1
		if d := r.URL.Query().Get("depth"); d != "" {
			v, err := strconv.Atoi(d)
			if err != nil || v < 1 || v > 2 {
				WriteError(w, r, ErrBadRequest)
				return
			}
			depth = v
		}
		pkt, err := BuildContextPacket(r.Context(), a.cfg.DB, rs,
			a.cfg.BaseURL, sourceName, anchorType, anchorSlug, depth)
		if err != nil {
			WriteError(w, r, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if encErr := json.NewEncoder(w).Encode(pkt); encErr != nil {
			slog.ErrorContext(r.Context(), "smeldr: ContextPacketHandler: encode", "error", encErr)
		}
	}))
}
