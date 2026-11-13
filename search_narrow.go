package smeldr

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// The narrow form of search (T1183): [SearchQuery.Narrow] matches a record's
// address and its external reference only, from one character, as index
// reads. It never reads a text field.

// SearchNarrowCountCeiling is the most candidate records one narrow search
// reads per type and phase, and so the most it counts (D183): above it
// [SearchResult.AtLeast] is set, [SearchResult.Total] is what was read, and
// [SearchResult.Next] continues after it.
const SearchNarrowCountCeiling = 200

// Match reasons of a narrow hit ([SearchHit.MatchReason]).
const (
	// SearchMatchAddress: the text is the record's whole address.
	SearchMatchAddress = "address"
	// SearchMatchAddressPrefix: the text is the start of the address.
	SearchMatchAddressPrefix = "address-prefix"
	// SearchMatchReference: the text is the start of the record's
	// external_ref, case-insensitively.
	SearchMatchReference = "reference"
)

// narrowPlan is a narrow search's text, parsed once.
type narrowPlan struct {
	upper   string // the text upper-cased: the address form
	letters string // the leading ASCII letters, upper-cased
	digits  string // the digits after them
	byAddr  bool   // the text is letters then digits: it can be an address
	ref     string // the text with ASCII letters lowered: the reference prefix
	after   narrowKey
	pg      bool                      // the database is PostgreSQL
	counter map[string]addressCounter // type name to its prefix and last number
}

// addressCounter is one row of the address counter registry.
type addressCounter struct {
	prefix string
	lastNo int64
}

// narrowKey orders narrow candidates: every address match (phase 'a', by
// type, then number, then id), then every reference match (phase 'r', by
// the lowered reference, then type, then id). The zero key is before all.
type narrowKey struct {
	Phase byte   `json:"p"`
	Type  string `json:"t"`
	No    int64  `json:"n,omitempty"`
	Ref   string `json:"r,omitempty"`
	ID    string `json:"i"`
}

func (k narrowKey) less(o narrowKey) bool {
	if k.Phase != o.Phase {
		return k.Phase < o.Phase
	}
	if k.Phase == 'r' && k.Ref != o.Ref {
		return k.Ref < o.Ref
	}
	if k.Type != o.Type {
		return k.Type < o.Type
	}
	if k.Phase == 'a' && k.No != o.No {
		return k.No < o.No
	}
	return k.ID < o.ID
}

// encode is the key as an opaque cursor.
func (k narrowKey) encode() string {
	b, _ := json.Marshal(k)
	return base64.RawURLEncoding.EncodeToString(b)
}

// decodeNarrowKey reads a cursor; a malformed one is [ErrBadRequest].
func decodeNarrowKey(s string) (narrowKey, error) {
	var k narrowKey
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err == nil {
		err = json.Unmarshal(b, &k)
	}
	if err != nil || (k.Phase != 'a' && k.Phase != 'r') {
		return narrowKey{}, problem(problemSearchQueryInvalid, fmt.Errorf("%w: the search cursor is not one this search returned", ErrBadRequest), nil)
	}
	return k, nil
}

// narrowCandidate is one record a narrow phase read: its hit and its key.
type narrowCandidate struct {
	key     narrowKey
	hit     SearchHit
	visible bool
}

// planNarrow parses text for a narrow search and loads the counter registry.
func (a *App) planNarrow(ctx context.Context, text, after string) (*narrowPlan, error) {
	n := &narrowPlan{upper: strings.ToUpper(text), ref: asciiLower(text), counter: map[string]addressCounter{}}
	i := 0
	for i < len(text) && isASCIILetter(text[i]) {
		i++
	}
	j := i
	for j < len(text) && text[j] >= '0' && text[j] <= '9' {
		j++
	}
	n.letters, n.digits = strings.ToUpper(text[:i]), text[i:j]
	n.byAddr = j == len(text) && (n.digits == "" || n.digits[0] != '0')
	if after != "" {
		k, err := decodeNarrowKey(after)
		if err != nil {
			return nil, err
		}
		n.after = k
	}
	db := a.cfg.DB
	if db == nil {
		return n, nil
	}
	pg, err := dbIsPostgres(ctx, db)
	if err != nil {
		return nil, fmt.Errorf("%w: search: the database kind: %v", ErrInternal, err)
	}
	n.pg = pg
	rows, err := db.QueryContext(ctx, `SELECT type_name, prefix, last_no FROM smeldr_address_counters`)
	if err != nil {
		if isNoSuchTable(err) {
			return n, nil
		}
		return nil, fmt.Errorf("%w: search: the address registry: %v", ErrInternal, err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var c addressCounter
		if err := rows.Scan(&name, &c.prefix, &c.lastNo); err != nil {
			return nil, fmt.Errorf("%w: search: the address registry: %v", ErrInternal, err)
		}
		n.counter[name] = c
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%w: search: the address registry: %v", ErrInternal, err)
	}
	return n, nil
}

func isASCIILetter(b byte) bool { return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') }

// addressRanges returns the address_no ranges typ's addresses may match, or
// nil when none can: the prefix must be the letters (or start with them when
// no digits follow), and the digits are the start of the number.
func (n *narrowPlan) addressRanges(typ string) [][2]int64 {
	c, ok := n.counter[typ]
	if !ok || !n.byAddr || c.lastNo < 1 {
		return nil
	}
	if n.digits == "" {
		if !strings.HasPrefix(c.prefix, n.letters) {
			return nil
		}
		return [][2]int64{{1, c.lastNo}}
	}
	if c.prefix != n.letters {
		return nil
	}
	base, err := strconv.ParseInt(n.digits, 10, 64)
	if err != nil {
		return nil
	}
	var out [][2]int64
	for lo, hi := base, base; lo <= c.lastNo; lo, hi = lo*10, hi*10+9 {
		out = append(out, [2]int64{lo, min(hi, c.lastNo)})
		if lo > c.lastNo/10 {
			break
		}
	}
	return out
}

// matchesAddress reports whether address matches the text as an address.
func (n *narrowPlan) matchesAddress(address string) bool {
	return n.byAddr && address != "" && strings.HasPrefix(address, n.upper)
}

// reason is a hit's match reason for its address.
func (n *narrowPlan) reason(address string) string {
	if address == n.upper {
		return SearchMatchAddress
	}
	return SearchMatchAddressPrefix
}

// narrowWhere builds the WHERE clause and ORDER BY of one phase on one type,
// numbering placeholders after args. dynamic adds the type_name column.
// ok is false when the phase cannot match the type.
func (n *narrowPlan) narrowWhere(phase byte, typ string, dynamic bool, args []any, states []Status) (clause string, out []any, ok bool) {
	if _, addressed := n.counter[typ]; !addressed {
		return "", nil, false
	}
	if n.after.Phase > phase || (n.after.Phase == phase && phase == 'a' && n.after.Type > typ) {
		return "", nil, false // the cursor is past this phase or type
	}
	arg := func(v any) string {
		args = append(args, v)
		return "$" + strconv.Itoa(len(args))
	}
	var conds []string
	if dynamic {
		conds = append(conds, "type_name = "+arg(typ))
	}
	var order string
	switch phase {
	case 'a':
		ranges := n.addressRanges(typ)
		if len(ranges) == 0 {
			return "", nil, false
		}
		ors := make([]string, len(ranges))
		for i, r := range ranges {
			ors[i] = "address_no BETWEEN " + arg(r[0]) + " AND " + arg(r[1])
		}
		conds = append(conds, "address <> ''", "("+strings.Join(ors, " OR ")+")")
		if n.after.Phase == 'a' && n.after.Type == typ {
			no, id := arg(n.after.No), arg(n.after.ID)
			conds = append(conds, "(address_no > "+no+" OR (address_no = "+no+" AND id > "+id+"))")
		}
		order = " ORDER BY address_no, id"
	case 'r':
		// One order on both databases, the one the merge uses: bytes. The
		// reference with its ASCII letters lowered, and the id, compared in
		// byte order: SQLite's lower() folds ASCII only and compares BINARY;
		// on PostgreSQL the C collation does both, under any database
		// collation, and the index on the same expressions serves the
		// filter, the cursor and the order (narrowRefExpr).
		ref, id := narrowRefExpr(n.pg), narrowIDExpr(n.pg)
		conds = append(conds, "external_ref <> ''", ref+" >= "+arg(n.ref), ref+" < "+arg(prefixUpperBound(n.ref)))
		if n.after.Phase == 'r' {
			after := arg(n.after.Ref)
			switch {
			case typ < n.after.Type:
				conds = append(conds, ref+" > "+after)
			case typ == n.after.Type:
				conds = append(conds, "("+ref+", "+id+") > ("+after+", "+arg(n.after.ID)+")")
			default:
				conds = append(conds, ref+" >= "+after)
			}
		}
		order = " ORDER BY " + ref + ", " + id
	}
	clause = " WHERE " + strings.Join(conds, " AND ")
	clause, args = withStatusIn(clause, args, states)
	return clause + order + " LIMIT " + strconv.Itoa(SearchNarrowCountCeiling+1), args, true
}

// prefixUpperBound is the smallest string greater than every string with
// prefix s in byte order: s with its last rune raised by one (past the
// surrogate range, which UTF-8 cannot hold).
func prefixUpperBound(s string) string {
	r, size := utf8.DecodeLastRuneInString(s)
	r++
	if r >= 0xD800 && r <= 0xDFFF {
		r = 0xE000
	}
	return s[:len(s)-size] + string(r)
}

// asciiLower lowers the ASCII letters of s and leaves every other byte: the
// fold SQLite's lower() and PostgreSQL's lower() under the C collation both
// apply, so a reference has one key in Go and in either database.
func asciiLower(s string) string {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c >= 'A' && c <= 'Z' {
			b := []byte(s)
			for j := i; j < len(b); j++ {
				if b[j] >= 'A' && b[j] <= 'Z' {
					b[j] += 'a' - 'A'
				}
			}
			return string(b)
		}
	}
	return s
}

// narrowRefExpr is the reference key the reference phase filters, orders and
// is indexed by: external_ref with its ASCII letters lowered, in byte order.
// PostgreSQL needs the C collation for both; SQLite has them by default.
func narrowRefExpr(pg bool) string {
	if pg {
		return `lower(external_ref COLLATE "C")`
	}
	return "lower(external_ref)"
}

// narrowIDExpr is the id in byte order, the reference phase's tie-break.
func narrowIDExpr(pg bool) string {
	if pg {
		return `(id COLLATE "C")`
	}
	return "id"
}

// narrowRecords implements the narrow read of one compiled type and phase:
// the rows the phase's index reads, at most [SearchNarrowCountCeiling]+1,
// each with whether the caller may see it.
func (m *Module[T]) narrowRecords(ctx context.Context, p *searchPlan, phase byte) ([]narrowCandidate, error) {
	sr, ok := m.repo.(*SQLRepo[T])
	if !ok {
		return nil, nil
	}
	where, args, ok := p.narrow.narrowWhere(phase, m.contentTypeName, false, nil, p.states)
	if !ok {
		return nil, nil
	}
	items, err := Query[T](ctx, sr.db, "SELECT * FROM "+sr.table+where, args...)
	if err != nil {
		return nil, err
	}
	elem := m.proto
	for elem.Kind() == reflect.Ptr {
		elem = elem.Elem()
	}
	fields := compiledSearchFields(elem)
	updatedAt := goFieldPath(elem, "UpdatedAt")
	out := make([]narrowCandidate, 0, len(items))
	for _, item := range items {
		id := nodeIDOf(item)
		rv := elemValue(item)
		hit := SearchHit{Type: m.contentTypeName, ID: id, Slug: nodeSlugOf(item), Address: addressOf(item), State: string(nodeStatusOf(item))}
		if updatedAt != nil {
			hit.UpdatedAt, _ = rv.FieldByIndex(updatedAt).Interface().(time.Time)
		}
		title := ""
		if t, ok := any(item).(Titled); ok {
			title = t.ContentTitle()
		} else if len(fields) > 0 {
			title = rv.FieldByIndex(fields[0].index).String()
		}
		hit.Title = hitTitle(title, hit)
		var no int64
		if a, ok := any(item).(interface {
			itemAddressNo() int64
			itemExternalRef() string
		}); ok {
			no, hit.ExternalRef = a.itemAddressNo(), a.itemExternalRef()
		}
		c := narrowCandidate{hit: hit, visible: m.visibleTo(p.scope, item) && (p.keepID == nil || p.keepID(id))}
		c.key = p.narrow.keyOf(phase, hit, no)
		out = append(out, c)
	}
	return out, nil
}

// keyOf is a candidate's key, and sets its match reason.
func (n *narrowPlan) keyOf(phase byte, hit SearchHit, no int64) narrowKey {
	if phase == 'a' {
		return narrowKey{Phase: 'a', Type: hit.Type, No: no, ID: hit.ID}
	}
	return narrowKey{Phase: 'r', Type: hit.Type, Ref: asciiLower(hit.ExternalRef), ID: hit.ID}
}

// narrowDynamic is the narrow read of one runtime-defined type and phase.
func (a *App) narrowDynamic(ctx context.Context, desc *TypeDescriptor, p *searchPlan, phase byte) ([]narrowCandidate, error) {
	repo, err := a.DynamicContentRepo(desc.Name)
	if err != nil {
		return nil, err
	}
	where, args, ok := p.narrow.narrowWhere(phase, desc.Name, true, nil, p.states)
	if !ok {
		return nil, nil
	}
	visible, err := repo.readVisible(WithReadScope(ctx, p.scope))
	if err != nil {
		return nil, err
	}
	nodes, err := Query[*DynamicNode](ctx, repo.db, "SELECT * FROM smeldr_dynamic_content"+where, args...)
	if err != nil {
		return nil, err
	}
	_, titleField := dynamicSearchFields(desc.Schema)
	out := make([]narrowCandidate, 0, len(nodes))
	for _, node := range nodes {
		values := map[string]any{}
		if len(node.Fields) > 0 {
			if err := json.Unmarshal(node.Fields, &values); err != nil {
				slog.Warn("smeldr: search: a record with unreadable fields", "type", desc.Name, "id", node.ID, "err", err)
			}
		}
		hit := SearchHit{Type: desc.Name, ID: node.ID, Slug: node.Slug, Address: node.Address, State: string(node.Status), UpdatedAt: node.UpdatedAt, ExternalRef: node.ExternalRef}
		title, _ := values[titleField].(string)
		hit.Title = hitTitle(title, hit)
		c := narrowCandidate{hit: hit, visible: (visible == nil || visible(node)) && (p.keepID == nil || p.keepID(node.ID))}
		c.key = p.narrow.keyOf(phase, hit, node.AddressNo)
		out = append(out, c)
	}
	return out, nil
}

// searchNarrow runs a narrow search: both phases over every target type, the
// candidates merged in key order and cut at the ceiling, then the caller's
// read rules. Rows read: at most [SearchNarrowCountCeiling]+1 per type and
// phase, whatever the size of the model.
func (a *App) searchNarrow(ctx context.Context, q SearchQuery, p *searchPlan, compiled []searchableModule, dynamic []*TypeDescriptor) (SearchResult, error) {
	var cands []narrowCandidate
	// A source that returned the full ceiling plus one may hold more after
	// its last row: nothing past the smallest such last key is certain.
	var bound *narrowKey
	take := func(found []narrowCandidate) {
		if len(found) > SearchNarrowCountCeiling {
			last := found[len(found)-1].key
			if bound == nil || last.less(*bound) {
				bound = &last
			}
		}
		cands = append(cands, found...)
	}
	for _, phase := range []byte{'a', 'r'} {
		for _, m := range compiled {
			nm, ok := m.(interface {
				narrowRecords(context.Context, *searchPlan, byte) ([]narrowCandidate, error)
			})
			if !ok {
				continue
			}
			found, err := nm.narrowRecords(ctx, p, phase)
			if err != nil {
				return SearchResult{}, fmt.Errorf("%w: search %s: %v", ErrInternal, m.searchTypeName(), err)
			}
			take(found)
		}
		for _, desc := range dynamic {
			found, err := a.narrowDynamic(ctx, desc, p, phase)
			if err != nil {
				return SearchResult{}, fmt.Errorf("%w: search %s: %v", ErrInternal, desc.Name, err)
			}
			take(found)
		}
	}
	// A reference match whose address also matches is already an address
	// match: it is listed once, as that.
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].key.less(cands[j].key) })
	res := SearchResult{Hits: []SearchHit{}}
	cut := false
	if bound != nil {
		// Keep what is certain: up to and including the bound.
		n := sort.Search(len(cands), func(i int) bool { return bound.less(cands[i].key) })
		cut, cands = true, cands[:n]
	}
	if len(cands) > SearchNarrowCountCeiling {
		cut, cands = true, cands[:SearchNarrowCountCeiling]
	}
	if cut {
		res.AtLeast = true
		var next narrowKey
		if len(cands) > 0 {
			next = cands[len(cands)-1].key
		} else {
			next = *bound // cut by the bound only, which is set
		}
		res.Next = next.encode()
	}
	kept := cands[:0]
	for _, c := range cands {
		if c.key.Phase == 'r' && p.narrow.matchesAddress(c.hit.Address) {
			continue
		}
		kept = append(kept, c)
	}
	cands = kept
	var hits []SearchHit
	for _, c := range cands {
		if !c.visible {
			continue
		}
		h := c.hit
		if c.key.Phase == 'a' {
			h.MatchReason = p.narrow.reason(h.Address)
		} else {
			h.MatchReason = SearchMatchReference
		}
		hits = append(hits, h)
	}
	res.Total = len(hits)
	start := min(q.Offset, len(hits))
	if page := hits[start:min(start+p.limit(q), len(hits))]; len(page) > 0 {
		res.Hits = page
	}
	return res, nil
}

// postgresCache remembers, per database handle, whether it is PostgreSQL.
var postgresCache sync.Map

// dbIsPostgres reports whether db is PostgreSQL, probed outside any
// transaction: current_setting exists there and not on SQLite. The one place
// core asks (T1183): the external_ref index and its query. An answer is
// cached per handle only when the probe settled it (it succeeded, or the
// database says the function does not exist); any other error, such as a
// cancelled context or a dropped connection, is returned and the next call
// probes again.
func dbIsPostgres(ctx context.Context, db DB) (bool, error) {
	if v, ok := postgresCache.Load(db); ok {
		return v.(bool), nil
	}
	var v string
	err := db.QueryRowContext(ctx, `SELECT current_setting('server_version_num')`).Scan(&v)
	switch {
	case err == nil:
		postgresCache.Store(db, true)
		return true, nil
	case strings.Contains(err.Error(), "no such function"):
		postgresCache.Store(db, false)
		return false, nil
	default:
		return false, err
	}
}
