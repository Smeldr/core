package smeldr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

// Search limits (T885).
const (
	// SearchTextMinLength is the fewest runes a search text may have, so a
	// one-letter search does not scan everything by accident. A search with no
	// text and at least one filter is allowed.
	SearchTextMinLength = 2
	// SearchTextMaxLength is the most runes a search text may have.
	SearchTextMaxLength = 200
	// SearchDefaultLimit is the page size when [SearchQuery.Limit] is zero.
	SearchDefaultLimit = 50
	// SearchMaxLimit caps [SearchQuery.Limit].
	SearchMaxLimit = 500
	// SearchPlaceNone is the [SearchQuery.Place] value that selects records
	// without a place: no live asserted edge of a place relation kind.
	SearchPlaceNone = "none"
)

// searchSnippetRunes is the length of a hit's snippet, and searchSnippetLead
// how many runes of it come before the match.
const (
	searchSnippetRunes = 160
	searchSnippetLead  = 60
)

// placeRelationKinds are the relation kinds that give a record its place
// (D142): a live asserted edge of one of them from a record is its place. A
// Root relation kind joins this list when Root becomes a core type (T882).
var placeRelationKinds = []string{"belongs_to_domain", "belongs_to_area"}

// placeTypes are the record types a named place may be, tried in order; a
// type that is not registered is skipped. The capitalized names are the core
// types T882 introduces, the lower-case ones the instance-defined types before
// it.
var placeTypes = []string{"Domain", "Area", "domain", "area"}

// searchRoles are the schema field roles a runtime-defined type searches when
// none of its fields sets [SchemaField.Search].
var searchRoles = map[string]bool{"title": true, "description": true, "body": true, "summary": true}

// SearchQuery is one search over every record type the caller may read
// ([App.Search]). Text is matched as a plain, case-insensitive substring of
// each type's searchable fields: a compiled type's fields tagged
// smeldr_search:"text", a runtime-defined type's fields with
// [SchemaField.Search] (or, when none sets it, its title, description, body
// and summary roles and its markdown fields).
type SearchQuery struct {
	// Text is the text to find, at least [SearchTextMinLength] and at most
	// [SearchTextMaxLength] runes. It may be empty when a filter is set.
	Text string
	// Types limits the search to these record types ("Decision", "task_plan").
	// Empty searches every type.
	Types []string
	// States limits the search to records in these states. Empty is any state.
	States []string
	// Place is "" for any place, [SearchPlaceNone] for records without a
	// place, or a Domain or Area named by its address, slug or ID.
	Place string
	// Limit is the page size: zero is [SearchDefaultLimit], and it is capped
	// at [SearchMaxLimit]. Offset skips that many hits.
	Limit, Offset int
}

// SearchHit is one record a search found.
type SearchHit struct {
	Type    string `json:"type"`
	ID      string `json:"id"`
	Slug    string `json:"slug"`
	Address string `json:"address,omitempty"`
	Title   string `json:"title"`
	State   string `json:"state"`
	// Field is the searchable field the text matched first; empty for a
	// search with no text.
	Field string `json:"field,omitempty"`
	// Snippet is the text around the match, at most 160 runes.
	Snippet   string    `json:"snippet,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// SearchResult is a page of hits and the total number of hits the caller may
// see across every page.
type SearchResult struct {
	Hits  []SearchHit `json:"hits"`
	Total int         `json:"total"`
}

// searchPlan is a validated [SearchQuery] as each type's search runs it.
type searchPlan struct {
	needle []rune            // the lowered text; nil for a search with no text
	ascii  string            // the longest ASCII run of the lowered text, for the SQL prefilter
	states []Status          // empty is any state
	keepID func(string) bool // the place filter; nil is any place
	scope  *ReadScope
}

// searchableModule is a compiled module [App.Search] can search: every
// [Module] is one.
type searchableModule interface {
	searchTypeName() string
	searchRecords(ctx context.Context, p *searchPlan) ([]SearchHit, error)
}

// Search finds the records the caller in ctx may read that match q, newest
// first (by UpdatedAt, then type, then ID). The read rules apply before
// anything is returned or counted: a record the caller may not read is not a
// hit and does not change the total.
//
// Errors: [ErrBadRequest] for a query with no text and no filter, a text
// outside the length limits, a negative limit or offset, or a type that is not
// a registered record type; [ErrNotFound] for a place that does not exist or
// that the caller may not read (the same answer for both); [ErrInternal] when
// the read scope or any query fails, never a partial result.
func (a *App) Search(ctx Context, q SearchQuery) (SearchResult, error) {
	p, err := a.planSearch(ctx, q)
	if err != nil {
		return SearchResult{}, err
	}
	compiled, dynamic, err := a.searchTargets(q.Types)
	if err != nil {
		return SearchResult{}, err
	}
	var hits []SearchHit
	for _, m := range compiled {
		// Every compiled type is searched: published web content is public
		// to a caller with no grant on its type ([Module.visibleTo]).
		found, err := m.searchRecords(ctx, p)
		if err != nil {
			return SearchResult{}, fmt.Errorf("%w: search %s: %v", ErrInternal, m.searchTypeName(), err)
		}
		hits = append(hits, found...)
	}
	for _, desc := range dynamic {
		found, err := a.searchDynamic(ctx, desc, p)
		if err != nil {
			return SearchResult{}, fmt.Errorf("%w: search %s: %v", ErrInternal, desc.Name, err)
		}
		hits = append(hits, found...)
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if !hits[i].UpdatedAt.Equal(hits[j].UpdatedAt) {
			return hits[i].UpdatedAt.After(hits[j].UpdatedAt)
		}
		if hits[i].Type != hits[j].Type {
			return hits[i].Type < hits[j].Type
		}
		return hits[i].ID < hits[j].ID
	})
	res := SearchResult{Total: len(hits)}
	start := min(q.Offset, len(hits))
	res.Hits = hits[start:min(start+p.limit(q), len(hits))]
	if res.Hits == nil {
		res.Hits = []SearchHit{}
	}
	return res, nil
}

// limit is q's page size after the default and the cap.
func (p *searchPlan) limit(q SearchQuery) int {
	switch {
	case q.Limit == 0:
		return SearchDefaultLimit
	case q.Limit > SearchMaxLimit:
		return SearchMaxLimit
	}
	return q.Limit
}

// planSearch validates q and loads what every type's search shares: the
// caller's read scope and the place filter.
func (a *App) planSearch(ctx Context, q SearchQuery) (*searchPlan, error) {
	text := strings.TrimSpace(q.Text)
	if text == "" && len(q.Types) == 0 && len(q.States) == 0 && q.Place == "" {
		return nil, fmt.Errorf("%w: search needs text or at least one filter (types, states, place)", ErrBadRequest)
	}
	if n := utf8.RuneCountInString(text); text != "" && (n < SearchTextMinLength || n > SearchTextMaxLength) {
		return nil, fmt.Errorf("%w: search text must be %d to %d characters, got %d", ErrBadRequest, SearchTextMinLength, SearchTextMaxLength, n)
	}
	if q.Limit < 0 || q.Offset < 0 {
		return nil, fmt.Errorf("%w: search limit and offset must not be negative", ErrBadRequest)
	}
	scope, err := scopeFor(ctx, a.governance)
	if err != nil {
		return nil, err
	}
	p := &searchPlan{scope: scope}
	if text != "" {
		p.needle = lowerRunes(text)
		p.ascii = longestASCIIRun(p.needle)
	}
	for _, s := range q.States {
		if s = strings.TrimSpace(s); s != "" {
			p.states = append(p.states, Status(s))
		}
	}
	if q.Place != "" {
		keep, err := a.placeFilter(WithReadScope(ctx, scope), strings.TrimSpace(q.Place))
		if err != nil {
			return nil, err
		}
		p.keepID = keep
	}
	return p, nil
}

// searchTargets returns the compiled modules and the runtime-defined types a
// search covers: those named in types, or every one. A name that is not a
// registered record type is [ErrBadRequest].
func (a *App) searchTargets(types []string) ([]searchableModule, []*TypeDescriptor, error) {
	if len(types) == 0 {
		var dynamic []*TypeDescriptor
		for _, desc := range a.typeRegistry.All() {
			if desc.Kind == "content" {
				dynamic = append(dynamic, desc)
			}
		}
		return a.searchModules, dynamic, nil
	}
	var compiled []searchableModule
	var dynamic []*TypeDescriptor
	seen := map[string]bool{}
	for _, name := range types {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		if m := a.searchModule(name); m != nil {
			compiled = append(compiled, m)
			continue
		}
		desc := a.typeRegistry.Lookup(name)
		if desc == nil || desc.Kind != "content" {
			return nil, nil, fmt.Errorf("%w: %q is not a record type this instance can search", ErrBadRequest, name)
		}
		dynamic = append(dynamic, desc)
	}
	return compiled, dynamic, nil
}

// searchModule returns the compiled module of typeName, or nil.
func (a *App) searchModule(typeName string) searchableModule {
	for _, m := range a.searchModules {
		if m.searchTypeName() == typeName {
			return m
		}
	}
	return nil
}

// addSearchModule records m for [App.Search]; the first module of a type
// name wins, as in the type registry.
func (a *App) addSearchModule(m searchableModule) {
	if m.searchTypeName() == "" || a.searchModule(m.searchTypeName()) != nil {
		return
	}
	a.searchModules = append(a.searchModules, m)
}

// placeFilter is the ID filter of a place: for [SearchPlaceNone], the records
// with no live asserted edge of a [placeRelationKinds] kind; else the records
// with one to the named Domain or Area. A place the caller may not read is
// [ErrNotFound], like a missing one. Without a relations table no record has
// a place.
func (a *App) placeFilter(ctx context.Context, place string) (func(string) bool, error) {
	targetID := ""
	if place != SearchPlaceNone {
		id, err := a.resolvePlace(ctx, place)
		if err != nil {
			return nil, err
		}
		targetID = id
	}
	placed, err := a.placedSources(ctx, targetID)
	if err != nil {
		return nil, err
	}
	if targetID == "" {
		return func(id string) bool { return !placed[id] }, nil
	}
	return func(id string) bool { return placed[id] }, nil
}

// resolvePlace returns the ID of the Domain or Area that ident names, under
// the read scope attached to ctx.
func (a *App) resolvePlace(ctx context.Context, ident string) (string, error) {
	for _, typeName := range placeTypes {
		if a.typeRegistry.Lookup(typeName) == nil {
			continue
		}
		id, err := a.ResolveReadableID(ctx, typeName, ident)
		if err == nil {
			return id, nil
		}
		if !isNotFound(err) {
			return "", err
		}
	}
	return "", fmt.Errorf("%w: no Domain or Area %q", ErrNotFound, ident)
}

// isNotFound reports whether err is [ErrNotFound].
func isNotFound(err error) bool {
	return errors.Is(err, ErrNotFound)
}

// placedSources returns the source IDs of the live asserted place edges: to
// targetID, or of any target when targetID is empty.
func (a *App) placedSources(ctx context.Context, targetID string) (map[string]bool, error) {
	out := map[string]bool{}
	if a.cfg.DB == nil {
		return out, nil
	}
	args := make([]any, 0, len(placeRelationKinds)+2)
	marks := make([]string, len(placeRelationKinds))
	for i, k := range placeRelationKinds {
		args = append(args, k)
		marks[i] = fmt.Sprintf("$%d", i+1)
	}
	args = append(args, time.Now().UTC())
	query := "SELECT source_id FROM smeldr_relations WHERE relation_kind IN (" + strings.Join(marks, ", ") +
		fmt.Sprintf(") AND edge_class = 'asserted' AND (invalid_at IS NULL OR invalid_at > $%d)", len(args))
	if targetID != "" {
		args = append(args, targetID)
		query += fmt.Sprintf(" AND target_id = $%d", len(args))
	}
	rows, err := a.cfg.DB.QueryContext(ctx, query, args...)
	if err != nil {
		if isNoSuchTable(err) {
			return out, nil
		}
		return nil, fmt.Errorf("%w: place edges: %v", ErrInternal, err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("%w: place edges: %v", ErrInternal, err)
		}
		out[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%w: place edges: %v", ErrInternal, err)
	}
	return out, nil
}

// searchDynamic searches the runtime-defined type desc: the SQL prefilter
// matches the JSON text of every field, then each row is decoded and only its
// searchable fields are matched.
func (a *App) searchDynamic(ctx context.Context, desc *TypeDescriptor, p *searchPlan) ([]SearchHit, error) {
	repo, err := a.DynamicContentRepo(desc.Name)
	if err != nil {
		return nil, err
	}
	fields, titleField := dynamicSearchFields(desc.Schema)
	if p.needle != nil && len(fields) == 0 {
		return nil, nil
	}
	nodes, err := repo.searchNodes(WithReadScope(ctx, p.scope), p)
	if err != nil {
		return nil, err
	}
	var hits []SearchHit
	for _, n := range nodes {
		if p.keepID != nil && !p.keepID(n.ID) {
			continue
		}
		values := map[string]any{}
		if len(n.Fields) > 0 {
			if err := json.Unmarshal(n.Fields, &values); err != nil {
				slog.Warn("smeldr: search: skipping a record with unreadable fields", "type", desc.Name, "id", n.ID, "err", err)
				continue
			}
		}
		hit := SearchHit{Type: desc.Name, ID: n.ID, Slug: n.Slug, Address: n.Address, State: string(n.Status), UpdatedAt: n.UpdatedAt}
		if p.needle != nil {
			field, snippet, ok := matchFields(p.needle, fields, func(name string) string {
				s, _ := values[name].(string)
				return s
			})
			if !ok {
				continue
			}
			hit.Field, hit.Snippet = field, snippet
		}
		title, _ := values[titleField].(string)
		hit.Title = hitTitle(title, hit)
		hits = append(hits, hit)
	}
	return hits, nil
}

// dynamicSearchFields returns the names of schema's searchable fields and its
// title field: the fields with [SchemaField.Search], or, when none sets it,
// the string fields with a title, description, body or summary role or the
// markdown format.
func dynamicSearchFields(schema *ContentTypeSchema) (fields []string, titleField string) {
	if schema == nil {
		return nil, ""
	}
	parsed, err := schema.ParseFields()
	if err != nil {
		return nil, ""
	}
	var flagged, byRole []string
	for _, f := range parsed {
		if f.Role == "title" {
			titleField = f.Name
		}
		if f.Search {
			flagged = append(flagged, f.Name)
		}
		if f.Type == "string" && (searchRoles[f.Role] || f.Format == "markdown") {
			byRole = append(byRole, f.Name)
		}
	}
	if len(flagged) > 0 {
		return flagged, titleField
	}
	return byRole, titleField
}

// searchNodes returns the nodes of r's type that pass p's text prefilter and
// states and that the caller may read.
func (r *DynamicTypeRepo) searchNodes(ctx context.Context, p *searchPlan) ([]*DynamicNode, error) {
	visible, err := r.readVisible(ctx)
	if err != nil {
		return nil, err
	}
	args := []any{r.typeName}
	query := "SELECT * FROM smeldr_dynamic_content WHERE type_name = $1"
	if p.ascii != "" {
		// The fields column holds JSON as encoding/json writes it, so the
		// needle is matched in its encoded form.
		enc, _ := json.Marshal(p.ascii)
		args = append(args, likeContains(strings.ToLower(string(enc[1:len(enc)-1]))))
		query += fmt.Sprintf(" AND LOWER(fields) LIKE $%d ESCAPE '\\'", len(args))
	}
	query, args = withStatusIn(query, args, p.states)
	nodes, err := Query[*DynamicNode](ctx, r.db, query, args...)
	if err != nil {
		return nil, err
	}
	if visible == nil {
		return nodes, nil
	}
	kept := nodes[:0]
	for _, n := range nodes {
		if visible(n) {
			kept = append(kept, n)
		}
	}
	return kept, nil
}

// searchTypeName implements [searchableModule].
func (m *Module[T]) searchTypeName() string { return m.contentTypeName }

// searchRecords implements [searchableModule]: the records of m's type that
// match p and that p's scope may see ([Module.visibleTo]). A SQL repository
// prefilters the text in SQL; any other repository is read whole and matched
// in Go.
func (m *Module[T]) searchRecords(ctx context.Context, p *searchPlan) ([]SearchHit, error) {
	elem := m.proto
	for elem.Kind() == reflect.Ptr {
		elem = elem.Elem()
	}
	fields := compiledSearchFields(elem)
	if p.needle != nil && len(fields) == 0 {
		return nil, nil
	}
	var items []T
	var err error
	if sr, ok := m.repo.(*SQLRepo[T]); ok {
		items, err = sr.searchRows(ctx, fields, p)
	} else {
		items, err = m.repo.FindAll(ctx, ListOptions{Status: p.states})
	}
	if err != nil {
		return nil, err
	}
	updatedAt := goFieldPath(elem, "UpdatedAt")
	var hits []SearchHit
	for _, item := range items {
		if !m.visibleTo(p.scope, item) {
			continue
		}
		id := nodeIDOf(item)
		if p.keepID != nil && !p.keepID(id) {
			continue
		}
		rv := elemValue(item)
		hit := SearchHit{Type: m.contentTypeName, ID: id, Slug: nodeSlugOf(item), Address: addressOf(item), State: string(nodeStatusOf(item))}
		if updatedAt != nil {
			hit.UpdatedAt, _ = rv.FieldByIndex(updatedAt).Interface().(time.Time)
		}
		if p.needle != nil {
			field, snippet, ok := matchFields(p.needle, searchFieldNames(fields), func(name string) string {
				for _, f := range fields {
					if f.name == name {
						return rv.FieldByIndex(f.index).String()
					}
				}
				return ""
			})
			if !ok {
				continue
			}
			hit.Field, hit.Snippet = field, snippet
		}
		title := ""
		if t, ok := any(item).(Titled); ok {
			title = t.ContentTitle()
		} else if len(fields) > 0 {
			title = rv.FieldByIndex(fields[0].index).String()
		}
		hit.Title = hitTitle(title, hit)
		hits = append(hits, hit)
	}
	return hits, nil
}

// searchRows reads the rows of r's table whose searchable columns contain p's
// prefilter text, in the states of p.
func (r *SQLRepo[T]) searchRows(ctx context.Context, fields []searchField, p *searchPlan) ([]T, error) {
	query := "SELECT * FROM " + r.table + " WHERE 1=1"
	var args []any
	if p.ascii != "" && len(fields) > 0 {
		args = append(args, likeContains(p.ascii))
		ors := make([]string, len(fields))
		for i, f := range fields {
			ors[i] = "LOWER(" + quoteIdent(f.column) + ") LIKE $1 ESCAPE '\\'"
		}
		query += " AND (" + strings.Join(ors, " OR ") + ")"
	}
	query, args = withStatusIn(query, args, p.states)
	return Query[T](ctx, r.db, query, args...)
}

// withStatusIn adds a status IN clause for states to query, numbering its
// placeholders after args.
func withStatusIn(query string, args []any, states []Status) (string, []any) {
	if len(states) == 0 {
		return query, args
	}
	marks := make([]string, len(states))
	for i, s := range states {
		args = append(args, string(s))
		marks[i] = fmt.Sprintf("$%d", len(args))
	}
	return query + " AND status IN (" + strings.Join(marks, ", ") + ")", args
}

// searchField is a compiled type's searchable field: its index path, its JSON
// name (what a hit reports) and its column.
type searchField struct {
	index  []int
	name   string
	column string
}

// searchFieldCache holds each struct type's []searchField.
var searchFieldCache sync.Map

// compiledSearchFields returns the string fields of struct type t tagged
// smeldr_search:"text", in declaration order. The result is cached per type.
func compiledSearchFields(t reflect.Type) []searchField {
	if v, ok := searchFieldCache.Load(t); ok {
		return v.([]searchField)
	}
	var out []searchField
	for _, f := range reflect.VisibleFields(t) {
		if f.Tag.Get("smeldr_search") != "text" || f.Type.Kind() != reflect.String {
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "" {
			name = f.Name
		}
		column := f.Tag.Get("db")
		if column == "" {
			column = strings.ToLower(f.Name)
		}
		out = append(out, searchField{index: f.Index, name: name, column: column})
	}
	searchFieldCache.Store(t, out)
	return out
}

// searchFieldNames returns the JSON names of fields.
func searchFieldNames(fields []searchField) []string {
	names := make([]string, len(fields))
	for i, f := range fields {
		names[i] = f.name
	}
	return names
}

// matchFields returns the first of fields whose value contains needle
// (case-insensitively), and a snippet around the match.
func matchFields(needle []rune, fields []string, value func(string) string) (field, snippet string, ok bool) {
	for _, name := range fields {
		text := []rune(value(name))
		if i := runeIndex(lowerRuneSlice(text), needle); i >= 0 {
			return name, snippetAround(text, i, len(needle)), true
		}
	}
	return "", "", false
}

// lowerRunes returns s lowered rune by rune, so a match index in it is an
// index in s.
func lowerRunes(s string) []rune { return lowerRuneSlice([]rune(s)) }

// lowerRuneSlice lowers each rune of rs into a new slice.
func lowerRuneSlice(rs []rune) []rune {
	out := make([]rune, len(rs))
	for i, r := range rs {
		out[i] = unicode.ToLower(r)
	}
	return out
}

// runeIndex returns the index of the first needle in hay, or -1.
func runeIndex(hay, needle []rune) int {
	if len(needle) == 0 {
		return 0
	}
outer:
	for i := 0; i+len(needle) <= len(hay); i++ {
		for j, r := range needle {
			if hay[i+j] != r {
				continue outer
			}
		}
		return i
	}
	return -1
}

// snippetAround returns about [searchSnippetRunes] runes of text around the
// match at i of length n, with whitespace collapsed and "..." where it is cut.
func snippetAround(text []rune, i, n int) string {
	start := max(0, i-searchSnippetLead)
	end := min(len(text), max(start+searchSnippetRunes, i+n))
	s := strings.Join(strings.Fields(string(text[start:end])), " ")
	if start > 0 {
		s = "..." + s
	}
	if end < len(text) {
		s += "..."
	}
	return s
}

// longestASCIIRun returns the longest run of ASCII runes in rs, the part of a
// lowered needle that SQL's LOWER folds the same way on SQLite (ASCII only)
// and Postgres, so the prefilter never drops a true hit.
func longestASCIIRun(rs []rune) string {
	best, cur := "", strings.Builder{}
	flush := func() {
		if cur.Len() > len(best) {
			best = cur.String()
		}
		cur.Reset()
	}
	for _, r := range rs {
		if r < utf8.RuneSelf {
			cur.WriteRune(r)
			continue
		}
		flush()
	}
	flush()
	return best
}

// likeContains is a LIKE pattern for a value containing s, with LIKE's
// wildcards and the escape character in s matched literally (ESCAPE '\').
func likeContains(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(s) + "%"
}

// hitTitle is a hit's title: title's first line, cut to [TitleMaxLength]
// runes, else the address, else the slug.
func hitTitle(title string, hit SearchHit) string {
	title, _, _ = strings.Cut(strings.TrimSpace(title), "\n")
	title = strings.TrimSpace(title)
	if rs := []rune(title); len(rs) > TitleMaxLength {
		title = string(rs[:TitleMaxLength])
	}
	switch {
	case title != "":
		return title
	case hit.Address != "":
		return hit.Address
	}
	return hit.Slug
}
