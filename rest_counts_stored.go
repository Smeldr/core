// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// MaxStoredRestCountItems is the most items one [App.StoredRestCounts] call
// reads.
const MaxStoredRestCountItems = 200

// ItemRef names one item by type and ID.
type ItemRef struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// StoredRestCounts are an item's rest counts as the last sweep stored them,
// summed over the places the reader may read (D150 point 4).
type StoredRestCounts struct {
	ItemRef
	RestCounts
	// CountedAt is when the sweep that stored them finished; zero when no
	// sweep has counted the item.
	CountedAt time.Time `json:"counted_at,omitzero"`
}

// RestCountRun is one run of [App.SweepRestCounts].
type RestCountRun struct {
	ID        string    `json:"id"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`
	// Items is how many items have rest edges and were walked.
	Items int `json:"items"`
	// Buckets is how many count rows the run stored.
	Buckets int `json:"buckets"`
	// Capped is how many walks (item and direction) stopped at
	// [RestCountLimit].
	Capped int `json:"capped"`
}

// EnsureRestCountTable creates the tables of the stored rest counts
// (smeldr_rest_counts) and of their runs (smeldr_rest_count_runs) when they
// do not exist. Idempotent; call it at boot.
func EnsureRestCountTable(ctx context.Context, db DB) error {
	for _, ddl := range []string{
		`CREATE TABLE IF NOT EXISTS smeldr_rest_counts (
			item_type     TEXT NOT NULL,
			item_id       TEXT NOT NULL,
			direction     TEXT NOT NULL,
			bucket_type   TEXT NOT NULL,
			bucket_id     TEXT NOT NULL,
			bucket_domain TEXT NOT NULL DEFAULT '',
			open_count    INTEGER NOT NULL,
			closed_count  INTEGER NOT NULL,
			capped        BOOLEAN NOT NULL DEFAULT FALSE,
			counted_at    TIMESTAMPTZ NOT NULL,
			PRIMARY KEY (item_type, item_id, direction, bucket_type, bucket_id)
		)`,
		`CREATE TABLE IF NOT EXISTS smeldr_rest_count_runs (
			id         TEXT PRIMARY KEY,
			started_at TIMESTAMPTZ NOT NULL,
			ended_at   TIMESTAMPTZ NOT NULL,
			items      INTEGER NOT NULL,
			buckets    INTEGER NOT NULL,
			capped     INTEGER NOT NULL
		)`,
	} {
		if _, err := db.ExecContext(ctx, ddl); err != nil {
			return fmt.Errorf("smeldr: EnsureRestCountTable: %w", err)
		}
	}
	return nil
}

// Directions of a stored rest count.
const (
	restRestsOnThis = "rests_on_this"
	restThisRestsOn = "this_rests_on"
)

// restPlace is a bucket: the Organization (both empty), a Domain (domain
// only) or an Area (area, with its Domain when the Area has one).
type restPlace struct{ domain, area string }

func (p restPlace) depth() int {
	switch {
	case p.area != "":
		return 2
	case p.domain != "":
		return 1
	}
	return 0
}

// lowestCommonPlace is the lowest place both p and q lie in: readers of it
// read both, because a place's readers read every place below it.
func lowestCommonPlace(p, q restPlace) restPlace {
	switch {
	case p == q:
		return p
	case p.domain != "" && p.domain == q.domain:
		return restPlace{domain: p.domain}
	}
	return restPlace{}
}

// SweepRestCounts counts, for every item with a live rest edge, what rests on
// it and what it rests on (D150), and stores the counts per place bucket,
// replacing the previous run's in one transaction. An item reached by a path
// is stored in the bucket of the lowest place every item on that path (after
// the walked one, the reached one included) lies in, and in the deepest such
// bucket any path gave it: every reader of that bucket reads the whole path,
// so no hidden item, and no item reached only through a hidden one, is ever
// counted for them. A reader of another path's places only may get a lower
// count than they could see; never a higher one.
//
// A Signal is neither counted nor walked through (its own read rule, D113,
// is not a place's). Each walk stops at [RestCountLimit] items. The run is
// recorded (smeldr_rest_count_runs); its end is every stored row's
// counted_at. A failed read or write is [ErrInternal] and keeps the previous
// counts.
func (a *App) SweepRestCounts(ctx context.Context) (RestCountRun, error) {
	db := a.cfg.DB
	if db == nil {
		return RestCountRun{}, fmt.Errorf("%w: SweepRestCounts needs a database", ErrInternal)
	}
	run := RestCountRun{ID: NewID(), StartedAt: time.Now().UTC()}
	g, err := loadRestGraph(ctx, db)
	if err != nil {
		return RestCountRun{}, err
	}
	rows := g.count()
	run.EndedAt = time.Now().UTC()
	for _, r := range rows {
		if r.capped && r.first {
			run.Capped++
		}
	}
	run.Items = g.walked
	run.Buckets = len(rows)
	err = runInTx(ctx, db, func(exec DB) error {
		if _, err := exec.ExecContext(ctx, `DELETE FROM smeldr_rest_counts`); err != nil {
			return err
		}
		if err := insertRestCounts(ctx, exec, g, rows, run.EndedAt); err != nil {
			return err
		}
		_, err := exec.ExecContext(ctx, `INSERT INTO smeldr_rest_count_runs (id, started_at, ended_at, items, buckets, capped) VALUES ($1, $2, $3, $4, $5, $6)`,
			run.ID, run.StartedAt, run.EndedAt, run.Items, run.Buckets, run.Capped)
		return err
	})
	if err != nil {
		return RestCountRun{}, fmt.Errorf("%w: SweepRestCounts: store: %v", ErrInternal, err)
	}
	return run, nil
}

// restInsertBatch is how many rows one insert carries (10 values each).
const restInsertBatch = 50

// insertRestCounts writes rows in batches.
func insertRestCounts(ctx context.Context, exec DB, g *restGraph, rows []restRow, countedAt time.Time) error {
	for start := 0; start < len(rows); start += restInsertBatch {
		batch := rows[start:min(start+restInsertBatch, len(rows))]
		var b strings.Builder
		b.WriteString(`INSERT INTO smeldr_rest_counts (item_type, item_id, direction, bucket_type, bucket_id, bucket_domain, open_count, closed_count, capped, counted_at) VALUES `)
		args := make([]any, 0, len(batch)*10)
		for i, r := range batch {
			if i > 0 {
				b.WriteString(", ")
			}
			n := len(args)
			fmt.Fprintf(&b, "($%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d)", n+1, n+2, n+3, n+4, n+5, n+6, n+7, n+8, n+9, n+10)
			node := g.nodes[r.item]
			bucketType, bucketID := "Organization", ""
			switch {
			case r.place.area != "":
				bucketType, bucketID = "Area", r.place.area
			case r.place.domain != "":
				bucketType, bucketID = "Domain", r.place.domain
			}
			args = append(args, node.typ, node.id, r.direction, bucketType, bucketID, r.place.domain, r.open, r.closed, r.capped, countedAt)
		}
		if _, err := exec.ExecContext(ctx, b.String(), args...); err != nil {
			return err
		}
	}
	return nil
}

// restGraph is the rest graph in memory, for one sweep.
type restGraph struct {
	nodes  []reachabilityNode
	open   []bool
	place  []restPlace
	out    [][]int32 // rest edges from source to target
	in     [][]int32 // the same edges, target to source
	walked int
}

// restRow is one stored count: an item, a direction and a bucket.
type restRow struct {
	item         int32
	direction    string
	place        restPlace
	open, closed int
	capped       bool
	first        bool // the walk's first row, so a capped walk counts once
}

// loadRestGraph reads the live rest edges, the places and the states once.
func loadRestGraph(ctx context.Context, db DB) (*restGraph, error) {
	now := time.Now().UTC()
	fail := func(what string, err error) error {
		return fmt.Errorf("%w: SweepRestCounts: read %s: %v", ErrInternal, what, err)
	}
	args := []any{now}
	ph := make([]string, len(RestRelationKinds))
	for i, k := range RestRelationKinds {
		args = append(args, k)
		ph[i] = fmt.Sprintf("$%d", len(args))
	}
	edgeRows, err := db.QueryContext(ctx, `SELECT source_type, source_id, target_type, target_id FROM smeldr_relations
		WHERE (invalid_at IS NULL OR invalid_at > $1) AND relation_kind IN (`+strings.Join(ph, ", ")+`)
		ORDER BY source_type, source_id, target_type, target_id`, args...)
	if err != nil {
		return nil, fail("rest edges", err)
	}
	var edges [][2]reachabilityNode
	for edgeRows.Next() {
		var s, t reachabilityNode
		if err := edgeRows.Scan(&s.typ, &s.id, &t.typ, &t.id); err != nil {
			edgeRows.Close()
			return nil, fail("rest edges", err)
		}
		if s.typ != "Signal" && t.typ != "Signal" && s != t {
			edges = append(edges, [2]reachabilityNode{s, t})
		}
	}
	err = edgeRows.Err()
	edgeRows.Close()
	if err != nil {
		return nil, fail("rest edges", err)
	}

	g := &restGraph{}
	index := map[reachabilityNode]int32{}
	var ends []reachabilityNode
	for _, e := range edges {
		for _, n := range e {
			if _, ok := index[n]; !ok {
				index[n] = -1
				ends = append(ends, n)
			}
		}
	}
	states := newRestStates(ctx, db)
	status, err := states.read(ends)
	if err != nil {
		return nil, err
	}
	// Only ends with a row are items.
	for _, n := range ends {
		st, ok := status[n]
		if !ok {
			delete(index, n)
			continue
		}
		open, err := states.open(n.typ, st)
		if err != nil {
			return nil, err
		}
		index[n] = int32(len(g.nodes))
		g.nodes = append(g.nodes, n)
		g.open = append(g.open, open)
	}
	g.out = make([][]int32, len(g.nodes))
	g.in = make([][]int32, len(g.nodes))
	for _, e := range edges {
		s, okS := index[e[0]]
		t, okT := index[e[1]]
		if okS && okT {
			g.out[s] = append(g.out[s], t)
			g.in[t] = append(g.in[t], s)
		}
	}
	places, err := loadRestPlaces(ctx, db, now)
	if err != nil {
		return nil, fail("places", err)
	}
	g.place = make([]restPlace, len(g.nodes))
	for i, n := range g.nodes {
		ps := places[n]
		if len(ps) == 0 {
			continue // unplaced: the Organization
		}
		p := ps[0]
		for _, q := range ps[1:] {
			p = lowestCommonPlace(p, q)
		}
		g.place[i] = p
	}
	return g, nil
}

// loadRestPlaces returns each placed record's places, with an Area's Domain.
func loadRestPlaces(ctx context.Context, db DB, now time.Time) (map[reachabilityNode][]restPlace, error) {
	rows, err := db.QueryContext(ctx, `SELECT source_type, source_id, relation_kind, target_id FROM smeldr_relations
		WHERE relation_kind IN ('belongs_to_organization', 'belongs_to_domain', 'belongs_to_area')
		  AND edge_class = 'asserted' AND (invalid_at IS NULL OR invalid_at > $1)
		ORDER BY source_type, source_id, relation_kind, target_id`, now)
	if err != nil {
		return nil, err
	}
	type placeEdge struct {
		node         reachabilityNode
		kind, target string
	}
	var placeEdges []placeEdge
	areaDomain := map[string]string{}
	for rows.Next() {
		var e placeEdge
		if err := rows.Scan(&e.node.typ, &e.node.id, &e.kind, &e.target); err != nil {
			rows.Close()
			return nil, err
		}
		placeEdges = append(placeEdges, e)
		if e.node.typ == "Area" && e.kind == "belongs_to_domain" {
			areaDomain[e.node.id] = e.target
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	out := map[reachabilityNode][]restPlace{}
	for _, e := range placeEdges {
		var p restPlace
		switch e.kind {
		case "belongs_to_domain":
			p = restPlace{domain: e.target}
		case "belongs_to_area":
			p = restPlace{domain: areaDomain[e.target], area: e.target}
		}
		out[e.node] = append(out[e.node], p)
	}
	return out, nil
}

// count walks every item both ways and returns the stored rows, in a stable
// order (item, direction, then bucket as found).
func (g *restGraph) count() []restRow {
	n := len(g.nodes)
	label := make([]restPlace, n)
	stamp := make([]int32, n)
	var rows []restRow
	gen := int32(0)
	for item := range n {
		if len(g.out[item]) == 0 && len(g.in[item]) == 0 {
			continue
		}
		g.walked++
		for _, dir := range []struct {
			name  string
			edges [][]int32
		}{{restRestsOnThis, g.in}, {restThisRestsOn, g.out}} {
			gen++
			reached, capped := g.walk(int32(item), dir.edges, label, stamp, gen)
			rows = append(rows, g.buckets(int32(item), dir.name, reached, label, capped)...)
		}
	}
	return rows
}

// walk is the label-correcting walk from anchor: an item is entered again
// only when a path reaches it with a deeper bucket (at most three times). It
// returns the items reached, in the order first reached, at most
// RestCountLimit of them.
func (g *restGraph) walk(anchor int32, edges [][]int32, label []restPlace, stamp []int32, gen int32) (reached []int32, capped bool) {
	type step struct {
		node  int32
		label restPlace
		first bool // the anchor: its own place is not on the path
	}
	queue := []step{{node: anchor, first: true}}
	stamp[anchor] = gen // the anchor is never counted
	for head := 0; head < len(queue); head++ {
		s := queue[head]
		if !s.first && label[s.node] != s.label {
			continue // a deeper label has superseded this entry
		}
		for _, nb := range edges[s.node] {
			if nb == anchor {
				continue
			}
			nl := g.place[nb]
			if !s.first {
				nl = lowestCommonPlace(s.label, nl)
			}
			if stamp[nb] == gen {
				if label[nb].depth() >= nl.depth() {
					continue
				}
			} else {
				if len(reached) >= RestCountLimit {
					capped = true
					continue
				}
				stamp[nb] = gen
				reached = append(reached, nb)
			}
			label[nb] = nl
			queue = append(queue, step{node: nb, label: nl})
		}
	}
	return reached, capped
}

// buckets sums what one walk reached per bucket, in the order the buckets
// were first met.
func (g *restGraph) buckets(item int32, direction string, reached []int32, label []restPlace, capped bool) []restRow {
	var rows []restRow
	at := map[restPlace]int{}
	for _, r := range reached {
		p := label[r]
		i, ok := at[p]
		if !ok {
			i = len(rows)
			at[p] = i
			rows = append(rows, restRow{item: item, direction: direction, place: p, capped: capped, first: i == 0})
		}
		if g.open[r] {
			rows[i].open++
		} else {
			rows[i].closed++
		}
	}
	// A capped walk reached RestCountLimit items, so it has rows.
	return rows
}

// StoredRestCounts returns the stored rest counts of items (at most
// [MaxStoredRestCountItems]), for each item the caller may read, in the
// order asked; an item the caller may not read is left out. Each count is
// the sum over the buckets the caller's place grants cover (D150 point 4): a
// caller who reads everything gets every bucket; a read grant on a Domain
// covers that Domain's bucket and its Areas'; one on an Area covers that
// Area's. Any other way of reading (an item grant, a static grant, a grant
// on the Organization) covers no bucket, so a count is never higher than
// what the caller could walk, only lower. CountedAt is the end of the last
// sweep; an item no sweep has counted has zero counts and no CountedAt.
// More items than allowed is [ErrBadRequest]; a failed read [ErrInternal].
func (a *App) StoredRestCounts(ctx context.Context, items []ItemRef) ([]StoredRestCounts, error) {
	if len(items) > MaxStoredRestCountItems {
		return nil, fmt.Errorf("%w: at most %d items per call", ErrBadRequest, MaxStoredRestCountItems)
	}
	db := a.cfg.DB
	if db == nil {
		return nil, fmt.Errorf("%w: StoredRestCounts needs a database", ErrInternal)
	}
	scope, err := packageScope(ctx, db)
	if err != nil {
		return nil, err
	}
	var out []StoredRestCounts
	at := map[ItemRef]int{}
	var nodes []reachabilityNode
	for _, it := range items {
		if _, dup := at[it]; dup || it.Type == "" || it.ID == "" || it.Type == "Signal" || !scope.Readable(it.Type, it.ID) {
			continue
		}
		at[it] = len(out)
		out = append(out, StoredRestCounts{ItemRef: it})
		nodes = append(nodes, reachabilityNode{it.Type, it.ID})
	}
	if len(out) == 0 {
		return []StoredRestCounts{}, nil
	}
	countedAt, swept, err := lastRestCountRun(ctx, db)
	if err != nil || !swept {
		return out, err
	}
	byType := map[string][]string{}
	var types []string
	for _, n := range nodes {
		if _, ok := byType[n.typ]; !ok {
			types = append(types, n.typ)
		}
		byType[n.typ] = append(byType[n.typ], n.id)
	}
	for _, typ := range types {
		ids := byType[typ]
		for start := 0; start < len(ids); start += restChunk {
			chunk := ids[start:min(start+restChunk, len(ids))]
			args := []any{typ}
			ph := make([]string, len(chunk))
			for i, id := range chunk {
				args = append(args, id)
				ph[i] = fmt.Sprintf("$%d", len(args))
			}
			if err := sumStoredRestCounts(ctx, db, scope, typ, `SELECT item_id, direction, bucket_type, bucket_id, bucket_domain, open_count, closed_count, capped FROM smeldr_rest_counts
				WHERE item_type = $1 AND item_id IN (`+strings.Join(ph, ", ")+`)`, args, at, out); err != nil {
				return nil, err
			}
		}
	}
	for i := range out {
		out[i].CountedAt = countedAt
	}
	return out, nil
}

// sumStoredRestCounts adds the covered rows of one query to out.
func sumStoredRestCounts(ctx context.Context, db DB, scope *ReadScope, typ, query string, args []any, at map[ItemRef]int, out []StoredRestCounts) error {
	fail := func(err error) error { return fmt.Errorf("%w: StoredRestCounts: %v", ErrInternal, err) }
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return fail(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, direction, bucketType, bucketID, bucketDomain string
		var open, closed int
		var capped bool
		if err := rows.Scan(&id, &direction, &bucketType, &bucketID, &bucketDomain, &open, &closed, &capped); err != nil {
			return fail(err)
		}
		if !scope.coversRestBucket(bucketType, bucketID, bucketDomain) {
			continue
		}
		c := &out[at[ItemRef{typ, id}]].RestsOnThis
		if direction == restThisRestsOn {
			c = &out[at[ItemRef{typ, id}]].ThisRestsOn
		}
		c.Open += open
		c.Closed += closed
		c.AtLeast = c.AtLeast || capped
	}
	if err := rows.Err(); err != nil {
		return fail(err)
	}
	return nil
}

// lastRestCountRun returns the end of the last sweep, and whether there was
// one. Without the tables there was none.
func lastRestCountRun(ctx context.Context, db DB) (time.Time, bool, error) {
	rows, err := db.QueryContext(ctx, `SELECT ended_at FROM smeldr_rest_count_runs ORDER BY ended_at DESC LIMIT 1`)
	if err != nil {
		if isNoSuchTable(err) {
			return time.Time{}, false, nil
		}
		return time.Time{}, false, fmt.Errorf("%w: StoredRestCounts: last run: %v", ErrInternal, err)
	}
	defer rows.Close()
	if !rows.Next() {
		return time.Time{}, false, rows.Err()
	}
	var ended time.Time
	if err := rows.Scan(timeScanner{dst: &ended}); err != nil {
		return time.Time{}, false, fmt.Errorf("%w: StoredRestCounts: last run: %v", ErrInternal, err)
	}
	return ended.UTC(), true, nil
}
