package smeldr

import (
	"cmp"
	"context"
	"database/sql"
	"fmt"
	"iter"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
)

// DB is the database interface accepted by [NewSQLRepo], [NewTokenStore],
// [NewAuditStore], and [NewWebhookStore]. It is satisfied by *sql.DB, *sql.Tx,
// and forgepgx.Wrap(pool). Pass a concrete implementation — do not implement
// DB directly.
type DB interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// dbField maps a SQL column name to the index path of the matching struct
// field. The index slice is used with reflect.Value.FieldByIndex and supports
// promoted fields from embedded anonymous structs.
type dbField struct {
	index []int  // field index path for reflect.Value.FieldByIndex
	name  string // column name: db tag value, or lowercased field name
}

// dbFieldCache stores []dbField slices keyed by reflect.Type (struct, not pointer).
// Entries are written once per type and never modified.
var dbFieldCache sync.Map

// dbFields returns the cached column→field mapping for struct type t.
// On cache miss, collectDBFields recurses into anonymous (embedded) struct
// fields so that promoted fields such as smeldr.Node are flattened into the
// result.
func dbFields(t reflect.Type) []dbField {
	if v, ok := dbFieldCache.Load(t); ok {
		return v.([]dbField)
	}
	fields := collectDBFields(t, nil)
	dbFieldCache.Store(t, fields)
	return fields
}

// collectDBFields recurses into t, building a flat list of dbField values.
// prefix is the index path to the current struct within the root type.
// Anonymous (embedded) struct fields are traversed rather than included
// directly — their promoted fields are mapped with the full index path so
// that FieldByIndex resolves them correctly.
func collectDBFields(t reflect.Type, prefix []int) []dbField {
	result := make([]dbField, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		idx := make([]int, len(prefix)+1)
		copy(idx, prefix)
		idx[len(prefix)] = i
		ft := f.Type
		if ft.Kind() == reflect.Ptr {
			ft = ft.Elem()
		}
		if f.Anonymous && ft.Kind() == reflect.Struct {
			result = append(result, collectDBFields(ft, idx)...)
			continue
		}
		name := f.Tag.Get("db")
		if name == "-" {
			continue
		}
		if name == "" {
			name = strings.ToLower(f.Name)
		}
		result = append(result, dbField{index: idx, name: name})
	}
	return result
}

// goFieldPathKey is the cache key for [goFieldPath].
type goFieldPathKey struct {
	t    reflect.Type
	name string
}

// goFieldPathCache stores field index paths ([]int) keyed by goFieldPathKey.
// A []int path is used to access promoted fields in embedded structs via
// reflect.Value.FieldByIndex.
var goFieldPathCache sync.Map

// goFieldPath returns the reflect index path (suitable for FieldByIndex) for
// the named field in struct type t, traversing embedded structs. Returns nil
// if the field is not found.
func goFieldPath(t reflect.Type, name string) []int {
	key := goFieldPathKey{t: t, name: name}
	if v, ok := goFieldPathCache.Load(key); ok {
		return v.([]int)
	}
	sf, ok := t.FieldByName(name)
	if !ok {
		goFieldPathCache.Store(key, ([]int)(nil))
		return nil
	}
	path := sf.Index
	goFieldPathCache.Store(key, path)
	return path
}

// structTypeOf returns the underlying struct reflect.Type for T and whether T
// is a pointer type. Panics if T resolves to neither a struct nor a pointer
// to a struct — Query[T] is only valid for struct targets.
func structTypeOf[T any]() (reflect.Type, bool) {
	t := reflect.TypeOf((*T)(nil)).Elem()
	if t.Kind() == reflect.Ptr {
		return t.Elem(), true
	}
	return t, false
}

// Query executes a SQL query and scans the result rows into a slice of T.
// T may be a struct type or a pointer to a struct (e.g. *BlogPost).
// Columns are matched to fields by db struct tag first, then by lowercased
// field name. Unrecognised columns are discarded without error.
// Returns an empty (non-nil) slice when no rows match.
func Query[T any](ctx context.Context, db DB, query string, args ...any) ([]T, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	elemType, isPtr := structTypeOf[T]()
	fields := dbFields(elemType)

	// Build column-name → field-index lookup from the cached field list.
	colIdx := make(map[string][]int, len(fields))
	for _, f := range fields {
		colIdx[f.name] = f.index
	}

	result := make([]T, 0)
	for rows.Next() {
		ptr := reflect.New(elemType)
		elem := ptr.Elem()

		targets := make([]any, len(cols))
		for i, col := range cols {
			if idx, ok := colIdx[col]; ok {
				targets[i] = elem.FieldByIndex(idx).Addr().Interface()
			} else {
				var discard any
				targets[i] = &discard
			}
		}

		if err := rows.Scan(targets...); err != nil {
			return nil, err
		}

		var item T
		if isPtr {
			item = ptr.Interface().(T)
		} else {
			item = elem.Interface().(T)
		}
		result = append(result, item)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

// QueryOne executes a SQL query and returns the first scanned row as T.
// Returns ErrNotFound when no rows match.
func QueryOne[T any](ctx context.Context, db DB, query string, args ...any) (T, error) {
	rows, err := Query[T](ctx, db, query, args...)
	if err != nil {
		var zero T
		return zero, err
	}
	if len(rows) == 0 {
		var zero T
		return zero, ErrNotFound
	}
	return rows[0], nil
}

// ListOptions controls pagination and ordering for FindAll queries.
type ListOptions struct {
	// Page is one-based. Values ≤ 0 are treated as page 1.
	Page int
	// PerPage is the maximum number of items per page.
	// A value of 0 means return all items.
	PerPage int
	// OrderBy is the Go field name to sort by (e.g. "Title").
	// Sorting applies only to exported string fields; other types are ignored.
	OrderBy string
	// Desc reverses the sort order when true.
	Desc bool
	// Status restricts results to items whose Status field matches one of the
	// given values. An empty or nil slice means return all statuses.
	Status []Status
}

// Offset returns the zero-based row offset for the page described by o.
func (o ListOptions) Offset() int {
	p := o.Page
	if p <= 0 {
		p = 1
	}
	off := (p - 1) * o.PerPage
	if off < 0 {
		return 0
	}
	return off
}

// Repository is the storage interface for a content type.
// Implement it to provide a custom storage backend.
// Use [NewMemoryRepo] for in-process testing and prototyping.
type Repository[T any] interface {
	FindByID(ctx context.Context, id string) (T, error)
	FindBySlug(ctx context.Context, slug string) (T, error)
	FindAll(ctx context.Context, opts ListOptions) ([]T, error)
	Save(ctx context.Context, node T) error
	Delete(ctx context.Context, id string) error
}

// SeqRepository is an optional extension of [Repository] that supports lazy
// iteration over content items without loading the full result set into memory.
// Both [MemoryRepo] and [SQLRepo] implement this interface.
//
// Callers type-assert their [Repository] to [SeqRepository] to use it:
//
//	if sr, ok := repo.(smeldr.SeqRepository[*Post]); ok {
//		for item, err := range sr.Seq(ctx, opts) {
//			if err != nil { ... }
//		}
//	}
//
// Note: Seq does not apply sorting or pagination — it streams all items
// matching the status filter in storage order. Use [Repository.FindAll] when
// ordering or pagination is required.
type SeqRepository[T any] interface {
	Seq(ctx context.Context, opts ListOptions) iter.Seq2[T, error]
}

// MemoryRepo is a thread-safe in-memory implementation of [Repository].
// It is intended for unit tests and prototyping — not production use.
// Fields named ID and Slug are located via cached reflection on first use.
type MemoryRepo[T any] struct {
	mu    sync.RWMutex
	items map[string]T
	order []string // ID insertion order
}

// NewMemoryRepo returns an empty MemoryRepo[T] ready for use.
func NewMemoryRepo[T any]() *MemoryRepo[T] {
	return &MemoryRepo[T]{items: make(map[string]T)}
}

// Save upserts node into the repository keyed by its ID field.
// On insert the ID is appended to the internal order list and Rev is unchanged (stays 0).
// On update the existing position in the order list is preserved and Rev is incremented
// on pointer T (so the caller's Rev stays in sync for subsequent saves).
func (r *MemoryRepo[T]) Save(_ context.Context, node T) error {
	id := stringField(node, "ID")
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.items[id]; !exists {
		r.order = append(r.order, id)
	} else {
		incrementRevField(node)
	}
	r.items[id] = node
	return nil
}

// incrementRevField increments the Rev field of item when item is a non-nil pointer
// to a struct that has a Rev int field (directly or via embedded [Node]).
// Value types are not modified — they cannot be updated through the interface.
func incrementRevField[T any](item T) {
	rv := reflect.ValueOf(item)
	if rv.Kind() != reflect.Ptr || rv.IsNil() {
		return
	}
	rv = rv.Elem()
	path := goFieldPath(rv.Type(), "Rev")
	if path == nil {
		return
	}
	f := rv.FieldByIndex(path)
	if f.Kind() == reflect.Int && f.CanSet() {
		f.SetInt(f.Int() + 1)
	}
}

// FindByID returns the item with the given ID, or [ErrNotFound].
func (r *MemoryRepo[T]) FindByID(_ context.Context, id string) (T, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	item, ok := r.items[id]
	if !ok {
		var zero T
		return zero, ErrNotFound
	}
	return item, nil
}

// FindBySlug returns the first item whose Slug field matches slug, or [ErrNotFound].
func (r *MemoryRepo[T]) FindBySlug(_ context.Context, slug string) (T, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, id := range r.order {
		item := r.items[id]
		if stringField(item, "Slug") == slug {
			return item, nil
		}
	}
	var zero T
	return zero, ErrNotFound
}

// statusMatch reports whether the Status field of item matches any of the
// given statuses. Returns true when statuses is empty (no filter applied).
func statusMatch[T any](item T, statuses []Status) bool {
	if len(statuses) == 0 {
		return true
	}
	s := Status(stringField(item, "Status"))
	for _, allowed := range statuses {
		if s == allowed {
			return true
		}
	}
	return false
}

// FindAll returns items in insertion order, with optional sorting and
// pagination from opts. When opts.PerPage is 0, all items are returned.
func (r *MemoryRepo[T]) FindAll(_ context.Context, opts ListOptions) ([]T, error) {
	r.mu.RLock()
	all := make([]T, 0, len(r.order))
	for _, id := range r.order {
		item := r.items[id]
		if statusMatch(item, opts.Status) {
			all = append(all, item)
		}
	}
	r.mu.RUnlock()

	if opts.OrderBy != "" {
		sortItems(all, opts.OrderBy, opts.Desc)
	}

	if opts.PerPage <= 0 {
		return all, nil
	}

	off := opts.Offset()
	if off >= len(all) {
		return []T{}, nil
	}
	end := off + opts.PerPage
	if end > len(all) {
		end = len(all)
	}
	return all[off:end], nil
}

// Seq returns a lazy iterator over items in insertion order, yielding each
// item that matches the status filter in opts (or all items when opts.Status
// is empty). Iteration stops early if the caller returns false from yield.
//
// Seq does not apply sorting or pagination. Use [MemoryRepo.FindAll] when
// ordering or pagination is required.
func (r *MemoryRepo[T]) Seq(_ context.Context, opts ListOptions) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		r.mu.RLock()
		ids := make([]string, len(r.order))
		copy(ids, r.order)
		items := make(map[string]T, len(r.items))
		for k, v := range r.items {
			items[k] = v
		}
		r.mu.RUnlock()

		for _, id := range ids {
			item := items[id]
			if !statusMatch(item, opts.Status) {
				continue
			}
			if !yield(item, nil) {
				return
			}
		}
	}
}

// Delete removes the item with the given ID. Returns [ErrNotFound] if absent.
func (r *MemoryRepo[T]) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.items[id]; !ok {
		return ErrNotFound
	}
	delete(r.items, id)
	for i, oid := range r.order {
		if oid == id {
			r.order = append(r.order[:i], r.order[i+1:]...)
			break
		}
	}
	return nil
}

// countByStatus implements [statusCounter] for [MemoryRepo]. It iterates the
// in-memory items and counts them by Status field value.
func (r *MemoryRepo[T]) countByStatus(_ context.Context) (map[Status]int, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	counts := make(map[Status]int)
	for _, id := range r.order {
		item := r.items[id]
		if s := stringField(item, "Status"); s != "" {
			counts[Status(s)]++
		}
	}
	return counts, nil
}

// stringField returns the string value of the named exported field in v.
// Traverses one pointer indirection and handles embedded struct fields.
// Returns "" if the field does not exist, is not a string, or v is nil.
// Uses goFieldPathCache to avoid repeated reflection.
func stringField[T any](v T, name string) string {
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Ptr {
		if rv.IsNil() {
			return ""
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return ""
	}
	path := goFieldPath(rv.Type(), name)
	if path == nil {
		return ""
	}
	f := rv.FieldByIndex(path)
	if f.Kind() != reflect.String {
		return ""
	}
	return f.String()
}

// sortPair bundles an item with its pre-extracted sort key to avoid
// repeated reflection calls inside the sort comparator.
type sortPair[T any] struct {
	item T
	key  string
}

// sortItems sorts items in-place by the named string field.
// Non-string fields and missing fields sort to the end.
// Uses a stable sort so equal elements preserve insertion order.
// Each item's sort key is extracted once before sorting, avoiding O(N log N)
// reflection calls in the comparator.
func sortItems[T any](items []T, field string, desc bool) {
	if len(items) == 0 {
		return
	}
	pairs := make([]sortPair[T], len(items))
	for i, item := range items {
		pairs[i] = sortPair[T]{item: item, key: stringField(item, field)}
	}
	slices.SortStableFunc(pairs, func(a, b sortPair[T]) int {
		if desc {
			return cmp.Compare(b.key, a.key)
		}
		return cmp.Compare(a.key, b.key)
	})
	for i, p := range pairs {
		items[i] = p.item
	}
}

// ---------------------------------------------------------------------------
// SQLRepo[T] — production Repository[T] backed by smeldr.DB
// ---------------------------------------------------------------------------

// quoteIdent wraps a SQL identifier in double quotes, making it safe to use
// reserved keywords (e.g. "order", "group") as column names. ANSI SQL
// double-quoting is supported by both SQLite and PostgreSQL.
func quoteIdent(name string) string { return `"` + name + `"` }

// SQLRepoOption configures a [SQLRepo]. Obtain values via [Table].
type SQLRepoOption interface{ isSQLRepoOption() }

// tableOption overrides the automatically derived SQL table name.
type tableOption struct{ name string }

func (tableOption) isSQLRepoOption() {}

// Table returns a [SQLRepoOption] that overrides the automatically derived
// table name for a [SQLRepo]. Use it when the default snake_case plural
// derivation does not produce the correct name — for example, types whose
// plural is not formed by appending "s" (Story → "storys", not "stories").
//
//	repo := smeldr.NewSQLRepo[*Story](db, smeldr.Table("stories"))
//	repo := smeldr.NewSQLRepo[*BlogPost](db, smeldr.Table("posts"))
func Table(name string) SQLRepoOption { return tableOption{name: name} }

// camelToSnake converts a CamelCase identifier to snake_case.
// "BlogPost" → "blog_post". "Article" → "article".
func camelToSnake(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 4)
	for i, r := range s {
		if unicode.IsUpper(r) && i > 0 {
			b.WriteByte('_')
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

// tableName derives the SQL table name for T from the registered [Table]
// option, or falls back to the snake_case plural of the type name.
// Pointer types are unwrapped: *BlogPost → "blog_posts".
func tableName[T any](opts []SQLRepoOption) string {
	for _, o := range opts {
		if to, ok := o.(tableOption); ok {
			return to.name
		}
	}
	t := reflect.TypeOf((*T)(nil)).Elem()
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	return camelToSnake(t.Name()) + "s"
}

// SQLRepo is a production [Repository][T] backed by [smeldr.DB].
// T must embed [smeldr.Node] — its fields are mapped to SQL columns via `db`
// struct tags, falling back to lowercase field names (the same rules as
// [Query] and [QueryOne]).
//
// All queries use $N positional placeholders (PostgreSQL / pgx compatible).
// Use the [Table] option to override the automatically derived table name.
type SQLRepo[T any] struct {
	db       DB
	table    string
	elemType reflect.Type // underlying struct type (never a pointer)
}

// NewSQLRepo returns a [SQLRepo][T] ready for use. The table name is derived
// automatically from T (e.g. BlogPost → "blog_posts"); pass [Table] to
// override.
//
// T must be a pointer type and must match the proto passed to [NewModule]:
//
//	repo := smeldr.NewSQLRepo[*Post](db)
//	m := smeldr.NewModule((*Post)(nil), smeldr.Repo(repo))
//
// Using a value type (NewSQLRepo[Post]) will compile but will not satisfy
// Repository[*Post] — the type parameters must match throughout.
func NewSQLRepo[T any](db DB, opts ...SQLRepoOption) *SQLRepo[T] {
	t := reflect.TypeOf((*T)(nil)).Elem()
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	return &SQLRepo[T]{
		db:       db,
		table:    tableName[T](opts),
		elemType: t,
	}
}

// columnForField returns the SQL column name for the given Go field name.
// Returns ("", false) if the field is unknown or excluded from mapping.
func (r *SQLRepo[T]) columnForField(goName string) (string, bool) {
	for _, f := range dbFields(r.elemType) {
		if r.elemType.FieldByIndex(f.index).Name == goName {
			return f.name, true
		}
	}
	return "", false
}

// FindByID returns the item with the given id, or [ErrNotFound].
func (r *SQLRepo[T]) FindByID(ctx context.Context, id string) (T, error) {
	return QueryOne[T](ctx, r.db,
		"SELECT * FROM "+r.table+" WHERE "+quoteIdent("id")+" = $1", id)
}

// FindBySlug returns the item with the given slug, or [ErrNotFound].
func (r *SQLRepo[T]) FindBySlug(ctx context.Context, slug string) (T, error) {
	return QueryOne[T](ctx, r.db,
		"SELECT * FROM "+r.table+" WHERE "+quoteIdent("slug")+" = $1", slug)
}

// FindAll returns items matching opts. Status filter, ordering, and
// pagination are translated to SQL WHERE / ORDER BY / LIMIT OFFSET clauses.
func (r *SQLRepo[T]) FindAll(ctx context.Context, opts ListOptions) ([]T, error) {
	query := "SELECT * FROM " + r.table
	args := make([]any, 0, len(opts.Status)+2)
	n := 1

	if len(opts.Status) > 0 {
		placeholders := make([]string, len(opts.Status))
		for i, s := range opts.Status {
			placeholders[i] = fmt.Sprintf("$%d", n)
			args = append(args, string(s))
			n++
		}
		query += " WHERE " + quoteIdent("status") + " IN (" + strings.Join(placeholders, ", ") + ")"
	}

	if opts.OrderBy != "" {
		if col, ok := r.columnForField(opts.OrderBy); ok {
			query += " ORDER BY " + quoteIdent(col)
			if opts.Desc {
				query += " DESC"
			}
		}
	}

	if opts.PerPage > 0 {
		query += fmt.Sprintf(" LIMIT $%d OFFSET $%d", n, n+1)
		args = append(args, opts.PerPage, opts.Offset())
	}

	return Query[T](ctx, r.db, query, args...)
}

// Save upserts item into the table. UpdatedAt is set to the current UTC time.
// CreatedAt is set only when its current value is the zero time.
// The upsert key is the "id" column.
func (r *SQLRepo[T]) Save(ctx context.Context, item T) error {
	// Create an addressable copy so we can write timestamps.
	cp := reflect.New(r.elemType)
	src := reflect.ValueOf(item)
	for src.Kind() == reflect.Ptr {
		src = src.Elem()
	}
	cp.Elem().Set(src)
	rv := cp.Elem()

	now := time.Now().UTC()
	if path := goFieldPath(r.elemType, "UpdatedAt"); path != nil {
		if f := rv.FieldByIndex(path); f.CanSet() {
			f.Set(reflect.ValueOf(now))
		}
	}
	if path := goFieldPath(r.elemType, "CreatedAt"); path != nil {
		if f := rv.FieldByIndex(path); f.CanSet() {
			if ts, ok := f.Interface().(time.Time); ok && ts.IsZero() {
				f.Set(reflect.ValueOf(now))
			}
		}
	}

	fields := dbFields(r.elemType)
	cols := make([]string, len(fields))
	placeholders := make([]string, len(fields))
	vals := make([]any, len(fields))
	setParts := make([]string, 0, len(fields))
	var revPH string // placeholder for the rev column in the INSERT list (e.g. "$3")

	for i, f := range fields {
		cols[i] = quoteIdent(f.name)
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		vals[i] = rv.FieldByIndex(f.index).Interface()
		switch f.name {
		case "id":
			// excluded from SET — id is the conflict key
		case "rev":
			revPH = fmt.Sprintf("$%d", i+1)
			// SET handled below: rev = table.rev + 1
		default:
			setParts = append(setParts, quoteIdent(f.name)+"=EXCLUDED."+quoteIdent(f.name))
		}
	}

	if revPH != "" {
		// Increment the stored rev rather than using EXCLUDED.rev, so the DB
		// owns the counter. The CAS WHERE guard below prevents overwriting a
		// concurrently incremented rev.
		setParts = append(setParts, quoteIdent("rev")+"="+r.table+"."+quoteIdent("rev")+"+1")
	}

	query := fmt.Sprintf(
		"INSERT INTO %s (%s) VALUES (%s) ON CONFLICT (%s) DO UPDATE SET %s",
		r.table,
		strings.Join(cols, ", "),
		strings.Join(placeholders, ", "),
		quoteIdent("id"),
		strings.Join(setParts, ", "),
	)
	if revPH != "" {
		// CAS guard: only apply the update if the stored rev matches what the
		// caller had when they read the item. RowsAffected = 0 means conflict.
		query += " WHERE " + r.table + "." + quoteIdent("rev") + " = " + revPH
	}

	result, err := r.db.ExecContext(ctx, query, vals...)
	if err != nil {
		return err
	}
	if revPH != "" {
		n, _ := result.RowsAffected()
		if n == 0 {
			return ErrRevConflict
		}
	}
	return nil
}

// Delete removes the item with the given id. Returns [ErrNotFound] if no row
// was deleted.
func (r *SQLRepo[T]) Delete(ctx context.Context, id string) error {
	result, err := r.db.ExecContext(ctx,
		"DELETE FROM "+r.table+" WHERE "+quoteIdent("id")+" = $1", id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// MigrateNodeRevColumn adds the rev column to table when it is absent.
// Safe to call on every boot; no-op when the column already exists or
// when db does not support PRAGMA table_info (non-SQLite).
//
// Call once per content table at startup before any Save call:
//
//	smeldr.MigrateNodeRevColumn(db, "posts")
//	smeldr.MigrateNodeRevColumn(db, "stories")
func MigrateNodeRevColumn(db DB, table string) error {
	ctx := context.Background()
	rows, err := db.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return nil // non-SQLite; assume schema is current
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, pk int
		var name, colType string
		var dflt *string
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil {
			continue
		}
		if name == "rev" {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = db.ExecContext(ctx,
		"ALTER TABLE "+quoteIdent(table)+" ADD COLUMN rev INTEGER NOT NULL DEFAULT 0")
	return err
}

// Seq returns a lazy iterator that streams rows from the database one at a
// time, yielding each item that matches the status filter in opts (or all
// items when opts.Status is empty). Iteration stops early if the caller
// returns false from yield.
//
// Seq does not apply pagination. Use [SQLRepo.FindAll] when ordering or
// pagination is required.
func (r *SQLRepo[T]) Seq(ctx context.Context, opts ListOptions) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		query := "SELECT * FROM " + r.table
		args := make([]any, 0, len(opts.Status))
		n := 1

		if len(opts.Status) > 0 {
			placeholders := make([]string, len(opts.Status))
			for i, s := range opts.Status {
				placeholders[i] = fmt.Sprintf("$%d", n)
				args = append(args, string(s))
				n++
			}
			query += " WHERE " + quoteIdent("status") + " IN (" + strings.Join(placeholders, ", ") + ")"
		}

		rows, err := r.db.QueryContext(ctx, query, args...)
		if err != nil {
			var zero T
			yield(zero, err)
			return
		}
		defer rows.Close()

		fields := dbFields(r.elemType)
		for rows.Next() {
			cp := reflect.New(r.elemType)
			dests := make([]any, len(fields))
			for i, f := range fields {
				dests[i] = cp.Elem().FieldByIndex(f.index).Addr().Interface()
			}
			if err := rows.Scan(dests...); err != nil {
				var zero T
				yield(zero, err)
				return
			}
			item, ok := cp.Interface().(T)
			if !ok {
				item = cp.Elem().Interface().(T)
			}
			if !yield(item, nil) {
				return
			}
		}
		if err := rows.Err(); err != nil {
			var zero T
			yield(zero, err)
		}
	}
}

// countByStatus implements [statusCounter] for [SQLRepo]. It executes a single
// GROUP BY query to count items by their status column.
func (r *SQLRepo[T]) countByStatus(ctx context.Context) (map[Status]int, error) {
	rows, err := r.db.QueryContext(ctx,
		"SELECT "+quoteIdent("status")+", COUNT(*) FROM "+r.table+
			" GROUP BY "+quoteIdent("status"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := make(map[Status]int)
	for rows.Next() {
		var s string
		var n int
		if err := rows.Scan(&s, &n); err != nil {
			return nil, err
		}
		counts[Status(s)] = n
	}
	return counts, rows.Err()
}
