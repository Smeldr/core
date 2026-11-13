// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// RestRelationKinds are the relation kinds an item rests on (D150 point 6):
// a Decision grounded in another, an item that addresses another, an
// Amendment that amends a Decision. The live and the stored rest counts walk
// these kinds and no others.
var RestRelationKinds = []string{"grounded_in", "addresses", "amends"}

// RestCountLimit is the most items one rest count walks in one direction
// (D35 guard 2). A walk that reaches more stops there and reports
// [RestCount.AtLeast].
const RestCountLimit = 1000

// restChunk is how many nodes one rest-count query carries: well under every
// supported driver's bind-variable limit.
const restChunk = 400

// RestCount is how many items one direction of a rest walk reached, split
// into open and closed (D109: an item is open when its state awaits action;
// an item of a type with no flow is closed).
type RestCount struct {
	Open   int `json:"open"`
	Closed int `json:"closed"`
	// AtLeast is set when the walk reached its limit ([RestCountLimit], or the
	// caller's [RestCountOptions.Limit]) and stopped: the true count is at
	// least Open+Closed.
	AtLeast bool `json:"at_least,omitempty"`
}

// RestCountDetail is what [App.RestCountsWith] adds to one direction's
// [RestCount] (T1183). It is a type of its own so RestCount stays a
// comparable value.
type RestCountDetail struct {
	// Ratified is how many of the count's Closed items are in the state
	// ratified; the other closed items are Closed minus Ratified.
	Ratified int `json:"ratified"`
	// Addresses is a page of the reached items' addresses, nearest first, set
	// only when [RestCountOptions.Addresses] asks for it. An item without an
	// address is listed as type:id. Every one is readable by the caller.
	Addresses []string `json:"addresses,omitempty"`
	// AddressesTotal is how many items the direction reached in all
	// (Open+Closed); set with Addresses.
	AddressesTotal int `json:"addresses_total,omitempty"`
}

// RestCountsDetail is [App.RestCountsWith]'s answer: the two counts, and
// what the options add to each.
type RestCountsDetail struct {
	RestCounts
	RestsOnThisDetail RestCountDetail `json:"rests_on_this_detail"`
	ThisRestsOnDetail RestCountDetail `json:"this_rests_on_detail"`
}

// restRatifiedState is the state the ratified part of a closed count is in.
const restRatifiedState = "ratified"

// Rest count address pages (T1183).
const (
	// RestCountDefaultAddressLimit is the page size when
	// [RestCountOptions.AddressLimit] is zero.
	RestCountDefaultAddressLimit = 50
	// RestCountMaxAddressLimit caps [RestCountOptions.AddressLimit].
	RestCountMaxAddressLimit = 200
)

// RestCountOptions are what a caller may set on a live rest count (T1183).
// The zero value is [App.RestCounts]: the fixed limit and no addresses.
type RestCountOptions struct {
	// Limit is the most items one direction walks: 0 is [RestCountLimit], and
	// 1 to [RestCountLimit] is the caller's own. Above it, or negative, is
	// refused.
	Limit int
	// Addresses asks for a page of the reached items' addresses in each
	// direction.
	Addresses bool
	// AddressOffset and AddressLimit page the addresses: AddressLimit 0 is
	// [RestCountDefaultAddressLimit], at most [RestCountMaxAddressLimit].
	AddressOffset, AddressLimit int
}

// RestCounts are the two rest counts of one item (D150): what rests on it,
// and what it rests on, transitively over [RestRelationKinds].
type RestCounts struct {
	// RestsOnThis counts the items that rest on this one: reached by
	// following rest edges backwards, from their target to their source.
	RestsOnThis RestCount `json:"rests_on_this"`
	// ThisRestsOn counts the items this one rests on: reached by following
	// rest edges from source to target.
	ThisRestsOn RestCount `json:"this_rests_on"`
}

// RestCounts counts, live, what rests on the item (typeName, id) and what it
// rests on (D150): every item reached over live edges of
// [RestRelationKinds], transitively, each counted once, the item itself
// never. It applies the caller's read rules the way the reachability walk
// does: an item the caller may not read is neither counted nor walked
// through, so a count never reveals a hidden item. Each direction walks at
// most [RestCountLimit] items, nearest first, and reports
// [RestCount.AtLeast] when it stopped there.
//
// The walk costs one edge query and one state query per type for each ring
// (per 400 items), not one per item. An anchor that does not exist or that
// the caller may not read is [ErrNotFound], alike. A failed read is
// [ErrInternal].
func (a *App) RestCounts(ctx context.Context, typeName, id string) (RestCounts, error) {
	d, err := a.RestCountsWith(ctx, typeName, id, RestCountOptions{})
	return d.RestCounts, err
}

// RestCountsWith is [App.RestCounts] with the caller's options (T1183): its
// own limit at or below [RestCountLimit], how many of each direction's closed
// items are ratified, and a page of the addresses each direction reached. A
// limit or page out of bounds is [ErrBadRequest] (problem
// rest_counts.limit_invalid); the rest is as [App.RestCounts].
func (a *App) RestCountsWith(ctx context.Context, typeName, id string, opt RestCountOptions) (RestCountsDetail, error) {
	limit, err := opt.limits()
	if err != nil {
		return RestCountsDetail{}, err
	}
	if typeName == "" || id == "" {
		return RestCountsDetail{}, problem(problemRequestInvalidParam, ErrBadRequest, nil)
	}
	if a.cfg.DB == nil {
		return RestCountsDetail{}, fmt.Errorf("%w: RestCounts needs a database", ErrInternal)
	}
	db := a.cfg.DB
	scope, err := packageScope(ctx, db)
	if err != nil {
		return RestCountsDetail{}, err
	}
	w := &restWalk{ctx: ctx, db: db, ends: newEndReader(ctx, db, scope), states: newRestStates(ctx, db), limit: limit}
	anchor := reachabilityNode{typeName, id}
	if ok, err := w.ends.readable(typeName, id); err != nil {
		return RestCountsDetail{}, fmt.Errorf("%w: rest counts of %s %s: %v", ErrInternal, typeName, id, err)
	} else if !ok {
		return RestCountsDetail{}, problem(problemItemNotFound, ErrNotFound, nil)
	}
	status, err := w.states.read([]reachabilityNode{anchor})
	if err != nil {
		return RestCountsDetail{}, err
	}
	if _, ok := status[anchor]; !ok {
		return RestCountsDetail{}, problem(problemItemNotFound, ErrNotFound, nil)
	}
	var out RestCountsDetail
	var outReached, inReached []reachabilityNode
	if out.ThisRestsOn, out.ThisRestsOnDetail.Ratified, outReached, err = w.count(anchor, true); err != nil {
		return RestCountsDetail{}, err
	}
	if out.RestsOnThis, out.RestsOnThisDetail.Ratified, inReached, err = w.count(anchor, false); err != nil {
		return RestCountsDetail{}, err
	}
	if opt.Addresses {
		if err := w.addressPage(&out.ThisRestsOnDetail, outReached, opt); err != nil {
			return RestCountsDetail{}, err
		}
		if err := w.addressPage(&out.RestsOnThisDetail, inReached, opt); err != nil {
			return RestCountsDetail{}, err
		}
	}
	return out, nil
}

// limits checks the options and returns the walk limit to use.
func (o RestCountOptions) limits() (int, error) {
	bad := func(field string, max int) error {
		return problem(problemRestCountsLimitInvalid, fmt.Errorf("%w: %s must be between 0 and %d", ErrBadRequest, field, max),
			problemValues{"field": field, "max": strconv.Itoa(max)})
	}
	switch {
	case o.Limit < 0 || o.Limit > RestCountLimit:
		return 0, bad("limit", RestCountLimit)
	case o.AddressLimit < 0 || o.AddressLimit > RestCountMaxAddressLimit:
		return 0, bad("address_limit", RestCountMaxAddressLimit)
	case o.AddressOffset < 0 || o.AddressOffset > RestCountLimit:
		return 0, bad("address_offset", RestCountLimit)
	}
	if o.Limit == 0 {
		return RestCountLimit, nil
	}
	return o.Limit, nil
}

// addressPage sets c's page of addresses from reached, in walk order.
func (w *restWalk) addressPage(c *RestCountDetail, reached []reachabilityNode, opt RestCountOptions) error {
	c.AddressesTotal = len(reached)
	total := len(reached)
	size := opt.AddressLimit
	if size == 0 {
		size = RestCountDefaultAddressLimit
	}
	start := min(opt.AddressOffset, total)
	page := reached[start:min(start+size, total)]
	addrs, err := w.states.addresses(page)
	if err != nil {
		return err
	}
	c.Addresses = make([]string, len(page))
	for i, n := range page {
		if a := addrs[n]; a != "" {
			c.Addresses[i] = a
		} else {
			c.Addresses[i] = n.typ + ":" + n.id
		}
	}
	return nil
}

// restWalk is one caller's live rest count.
type restWalk struct {
	ctx    context.Context
	db     DB
	ends   *endReader
	states *restStates
	limit  int // the most items one direction walks
}

// count walks from anchor over live rest edges, outgoing (source to target)
// when outgoing is true, and counts what it reaches, ring by ring. It also
// returns how many of the closed items are ratified, and the items it
// counted, nearest first, each ring by type and id.
func (w *restWalk) count(anchor reachabilityNode, outgoing bool) (RestCount, int, []reachabilityNode, error) {
	var c RestCount
	ratified := 0
	var reached []reachabilityNode
	visited := map[reachabilityNode]bool{anchor: true}
	frontier := []reachabilityNode{anchor}
	total := 0
	for len(frontier) > 0 {
		edges, err := restEdges(w.ctx, w.db, outgoing, frontier)
		if err != nil {
			return RestCount{}, 0, nil, err
		}
		var ring []reachabilityNode
		for _, e := range edges {
			n := reachabilityNode{e.TargetType, e.TargetID}
			if !outgoing {
				n = reachabilityNode{e.SourceType, e.SourceID}
			}
			if visited[n] {
				continue
			}
			visited[n] = true
			ok, err := w.ends.readable(n.typ, n.id)
			if err != nil {
				return RestCount{}, 0, nil, fmt.Errorf("%w: rest counts: %v", ErrInternal, err)
			}
			if ok {
				ring = append(ring, n)
			}
		}
		status, err := w.states.read(ring)
		if err != nil {
			return RestCount{}, 0, nil, err
		}
		// An end with no row (a dangling edge) is not an item to count.
		kept := ring[:0]
		for _, n := range ring {
			if _, ok := status[n]; ok {
				kept = append(kept, n)
			}
		}
		// Each ring in a stable order, so a page of addresses is stable too.
		sortNodes(kept)
		if total+len(kept) > w.limit {
			// The cap lands in this ring.
			kept = kept[:w.limit-total]
			c.AtLeast = true
		}
		for _, n := range kept {
			open, err := w.states.open(n.typ, status[n])
			if err != nil {
				return RestCount{}, 0, nil, err
			}
			switch {
			case open:
				c.Open++
			case status[n] == restRatifiedState:
				c.Closed++
				ratified++
			default:
				c.Closed++
			}
		}
		reached = append(reached, kept...)
		total += len(kept)
		if c.AtLeast {
			break
		}
		frontier = kept
	}
	return c, ratified, reached, nil
}

// sortNodes orders nodes by type, then id.
func sortNodes(nodes []reachabilityNode) {
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].typ != nodes[j].typ {
			return nodes[i].typ < nodes[j].typ
		}
		return nodes[i].id < nodes[j].id
	})
}

// restEdges returns the live rest edges at nodes: those whose source is one of
// nodes when outgoing, else those whose target is, in queries of at most
// restChunk nodes per type.
func restEdges(ctx context.Context, db DB, outgoing bool, nodes []reachabilityNode) ([]RelationEdge, error) {
	rs := &RelationStore{db: db}
	now := time.Now().UTC()
	var out []RelationEdge
	for start := 0; start < len(nodes); start += restChunk {
		edges, err := rs.batchEdgesByTypeAndIDs(ctx, outgoing, nodes[start:min(start+restChunk, len(nodes))], RestRelationKinds)
		if err != nil {
			return nil, fmt.Errorf("%w: rest counts: read edges: %v", ErrInternal, err)
		}
		for _, e := range edges {
			if e.InvalidAt == nil || e.InvalidAt.After(now) {
				out = append(out, e)
			}
		}
	}
	return out, nil
}

// restStates reads items' states and whether a state is open, caching each
// type's table and open states for one count.
type restStates struct {
	ctx    context.Context
	db     DB
	tables map[string]string
	opens  map[string]map[string]bool // type to its open states; nil: no flow
}

func newRestStates(ctx context.Context, db DB) *restStates {
	return &restStates{ctx: ctx, db: db, tables: map[string]string{}, opens: map[string]map[string]bool{}}
}

// read returns the status of each of nodes that has a row; a node with none
// is absent from the result.
func (s *restStates) read(nodes []reachabilityNode) (map[reachabilityNode]string, error) {
	out := map[reachabilityNode]string{}
	byType := map[string][]string{}
	var types []string
	for _, n := range nodes {
		if _, ok := byType[n.typ]; !ok {
			types = append(types, n.typ)
		}
		byType[n.typ] = append(byType[n.typ], n.id)
	}
	for _, typ := range types {
		table, ok := s.tables[typ]
		if !ok {
			table = resolveItemTable(s.ctx, s.db, typ)
			s.tables[typ] = table
		}
		ids := byType[typ]
		for start := 0; start < len(ids); start += restChunk {
			chunk := ids[start:min(start+restChunk, len(ids))]
			args := make([]any, 0, len(chunk)+1)
			query := "SELECT id, status FROM " + quoteIdent(table) + " WHERE "
			if table == "smeldr_dynamic_content" {
				args = append(args, typ)
				query += "type_name=$1 AND "
			}
			ph := make([]string, len(chunk))
			for i, id := range chunk {
				args = append(args, id)
				ph[i] = fmt.Sprintf("$%d", len(args))
			}
			query += "id IN (" + strings.Join(ph, ", ") + ")"
			if err := s.scan(query, args, typ, out); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// addresses returns the address of each of nodes that has one, one query per
// type per restChunk (T1183). A type whose table has no address column has
// none, so its items are left out and the caller names them by type and id.
func (s *restStates) addresses(nodes []reachabilityNode) (map[reachabilityNode]string, error) {
	out := map[reachabilityNode]string{}
	byType := map[string][]string{}
	var types []string
	for _, n := range nodes {
		if _, ok := byType[n.typ]; !ok {
			types = append(types, n.typ)
		}
		byType[n.typ] = append(byType[n.typ], n.id)
	}
	for _, typ := range types {
		table, ok := s.tables[typ]
		if !ok {
			table = resolveItemTable(s.ctx, s.db, typ)
			s.tables[typ] = table
		}
		ids := byType[typ]
		for start := 0; start < len(ids); start += restChunk {
			chunk := ids[start:min(start+restChunk, len(ids))]
			args := make([]any, 0, len(chunk)+1)
			query := "SELECT id, address FROM " + quoteIdent(table) + " WHERE "
			if table == "smeldr_dynamic_content" {
				args = append(args, typ)
				query += "type_name=$1 AND "
			}
			ph := make([]string, len(chunk))
			for i, id := range chunk {
				args = append(args, id)
				ph[i] = fmt.Sprintf("$%d", len(args))
			}
			query += "id IN (" + strings.Join(ph, ", ") + ")"
			if err := s.scanAddresses(query, args, typ, out); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// scanAddresses runs one address query and adds its rows to out. A table
// without the address column has no addresses: not an error.
func (s *restStates) scanAddresses(query string, args []any, typ string, out map[reachabilityNode]string) error {
	rows, err := s.db.QueryContext(s.ctx, query, args...)
	if err != nil {
		if isNoSuchColumn(err, "address") {
			return nil
		}
		return fmt.Errorf("%w: rest counts: read the addresses of %s: %v", ErrInternal, typ, err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, address string
		if err := rows.Scan(&id, &address); err != nil {
			return fmt.Errorf("%w: rest counts: read the addresses of %s: %v", ErrInternal, typ, err)
		}
		out[reachabilityNode{typ, id}] = address
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("%w: rest counts: read the addresses of %s: %v", ErrInternal, typ, err)
	}
	return nil
}

// scan runs one state query and adds its rows to out.
func (s *restStates) scan(query string, args []any, typ string, out map[reachabilityNode]string) error {
	rows, err := s.db.QueryContext(s.ctx, query, args...)
	if err != nil {
		return fmt.Errorf("%w: rest counts: read the states of %s: %v", ErrInternal, typ, err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, status string
		if err := rows.Scan(&id, &status); err != nil {
			return fmt.Errorf("%w: rest counts: read the states of %s: %v", ErrInternal, typ, err)
		}
		out[reachabilityNode{typ, id}] = status
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("%w: rest counts: read the states of %s: %v", ErrInternal, typ, err)
	}
	return nil
}

// open reports whether status is an open state of typ (D109). A type with no
// flow has no open state.
func (s *restStates) open(typ, status string) (bool, error) {
	set, ok := s.opens[typ]
	if !ok {
		states, found, err := StatesAwaitingAction(s.ctx, s.db, typ)
		if err != nil {
			return false, fmt.Errorf("%w: rest counts: %v", ErrInternal, err)
		}
		if found {
			set = states
		}
		s.opens[typ] = set
	}
	return set[status], nil
}
