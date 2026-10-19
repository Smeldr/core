// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
)

// D110: every record gets a system-assigned, unique and immutable address, a
// type prefix plus a running number (D110, T412, INV27). This file holds the
// prefix registry, the counter and allocation; see design/item-addresses-v1.md.

// coreAddressPrefixes are the prefixes core reserves. A single letter can only
// be registered for the type this map names it for; every other single letter
// stays free for a future core type.
var coreAddressPrefixes = map[string]string{
	"Decision":  "D",
	"Amendment": "A",
	"Task":      "T",
	"Goal":      "G",
	"Signal":    "S",
	"Run":       "R",
	"task_plan": "P",
	// Authority records (authority.go): R and S are taken, so rUle and stuB.
	"Rule":          "U",
	"AuthorityStub": "B",
	// Subscriptions (subscription.go, D116): S is Signal, so N for notify.
	"Subscription": "N",
	// Places (places.go, D142). DOM and ARE are the prefixes the runtime
	// domain and area types had, so their addresses stay.
	"Organization": "ORG",
	"Domain":       "DOM",
	"Area":         "ARE",
	"Set":          "SET",
	// Coverage rules of Areas (coverage.go, T886).
	"CoverageRule": "COV",
}

var addressPrefixRe = regexp.MustCompile(`^[A-Z]{1,4}$`)

// errNoAddressPrefix marks an allocation for a type that has no registered
// prefix.
var errNoAddressPrefix = errors.New("no address prefix is registered")

// createAddressCountersSQL creates the one table that is both the prefix
// registry and the counter. prefix is UNIQUE so a prefix names one type on an
// instance; last_no only ever goes up, so a number is never reused.
const createAddressCountersSQL = `CREATE TABLE IF NOT EXISTS smeldr_address_counters (
	type_name   TEXT PRIMARY KEY,
	prefix      TEXT NOT NULL UNIQUE,
	last_no     BIGINT NOT NULL DEFAULT 0,
	reserved_no BIGINT NOT NULL DEFAULT 0
)`

// addressColumns are the three columns every addressed table carries.
var addressColumns = [][2]string{
	{"address", "TEXT NOT NULL DEFAULT ''"},
	{"address_no", "BIGINT NOT NULL DEFAULT 0"},
	{"external_ref", "TEXT NOT NULL DEFAULT ''"},
}

// coreAddressTables maps each compiled core record type to its table and the
// legacy human-ID column the counter is seeded from ("" when it has none): the
// six orchestration types and the two authority types. A table that does not
// exist (the authority tables without ENABLE_AUTHORITY) is skipped by every
// reader of this list.
var coreAddressTables = []struct{ typeName, table, legacy string }{
	{"Signal", "smeldr_signals", ""},
	{"Task", "smeldr_tasks", "task_id"},
	{"Decision", "smeldr_decisions", "decision_number"},
	{"Amendment", "smeldr_amendments", "amendment_number"},
	{"Goal", "smeldr_goals", "goal_id"},
	{"Run", "smeldr_runs", ""},
	{"Rule", "smeldr_rules", ""},
	{"AuthorityStub", "smeldr_authority_stubs", ""},
	{"Subscription", "smeldr_subscriptions", ""},
	{"Organization", "smeldr_organizations", ""},
	{"Domain", "smeldr_domains", ""},
	{"Area", "smeldr_areas", ""},
	{"Set", "smeldr_sets", ""},
	{"CoverageRule", "smeldr_coverage_rules", ""},
}

// governanceAddressTypes and orchestrationAddOnAddressTypes are the types
// [CreateGovernanceTables] and [CreateOrchestrationAddOnTables] give address
// storage; [CreateAuthorityTables] does the same for authorityAddressTypes.
var (
	governanceAddressTypes         = []string{"Signal", "Decision", "Amendment", "Organization", "Domain", "Area", "Set", "CoverageRule"}
	orchestrationAddOnAddressTypes = []string{"Task", "Goal", "Run"}
	authorityAddressTypes          = []string{"Rule", "AuthorityStub"}
	subscriptionAddressTypes       = []string{"Subscription"}
)

// ensureAddressStorage adds the address columns to table (idempotent) and the
// partial unique index over its addresses. scopeByType makes the index
// (type_name, address), for the shared smeldr_dynamic_content table. A
// duplicate address refuses the index and is returned as an error: unlike a
// duplicate slug, it means the data is wrong, so boot must fail loudly.
func ensureAddressStorage(ctx context.Context, db DB, table string, scopeByType bool) error {
	for _, c := range addressColumns {
		if err := EnsureColumn(ctx, db, table, c[0], c[1]); err != nil {
			return fmt.Errorf("smeldr: address columns on %s: %w", table, err)
		}
	}
	cols, groupBy := "address", "address"
	if scopeByType {
		cols, groupBy = "type_name, address", "type_name, address"
	}
	var dup string
	err := db.QueryRowContext(ctx,
		`SELECT address FROM `+table+` WHERE address <> '' GROUP BY `+groupBy+` HAVING COUNT(*) > 1 LIMIT 1`).Scan(&dup)
	if err == nil {
		return fmt.Errorf("%w: smeldr: %s has more than one row with address %q; addresses must be unique", ErrInternal, table, dup)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("smeldr: address index on %s: %w", table, err)
	}
	if _, err := db.ExecContext(ctx,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_`+table+`_address ON `+table+` (`+cols+`) WHERE address <> ''`); err != nil {
		// Unlike the slug index, this one is not optional: D110 promises the
		// address is unique by a database constraint, so boot stops without it.
		return fmt.Errorf("%w: smeldr: the unique address index on %s was not created: %s", ErrInternal, table, err)
	}
	return nil
}

// checkAddressPrefix applies the prefix rules that need no database: the shape
// (1 to 4 ASCII capitals), a single letter only for the core type it is
// reserved for, and a core type only under its own reserved prefix.
func checkAddressPrefix(typeName, prefix string) error {
	if !addressPrefixRe.MatchString(prefix) {
		return Err("prefix", fmt.Sprintf("%q must be 1 to 4 capital letters A-Z", prefix))
	}
	if want, core := coreAddressPrefixes[typeName]; core && prefix != want {
		return Err("prefix", fmt.Sprintf("%s's address prefix is reserved as %q and cannot be changed", typeName, want))
	}
	if len(prefix) == 1 && coreAddressPrefixes[typeName] != prefix {
		return Err("prefix", fmt.Sprintf("single-letter prefixes are reserved for core types; %q is not available for %s (use 2 to 4 letters)", prefix, typeName))
	}
	return nil
}

// registerAddressPrefix records prefix as typeName's address prefix. It is
// idempotent for the same pair. A new row is seeded from seed (the existing
// rows' numbers, so a new address never takes a number an existing record will
// keep). A different prefix replaces the old one only while the type has no
// address yet; a prefix another type holds is refused.
func registerAddressPrefix(ctx context.Context, db DB, typeName, prefix string, seed func() (int64, error)) error {
	return registerAddressPrefixWith(ctx, db, typeName, prefix, seed, false)
}

// registerReservingAddressPrefix is [registerAddressPrefix] for a type with no
// legacy human ID (Signal, Run, a dynamic record type): its seed is the count of
// rows that have no address yet, and the new row also records that count as
// reserved_no, the numbers 1..reserved_no the migration gives those rows
// ([MigrateAddresses]). No allocation ever lands in that range, since the
// counter starts above it.
func registerReservingAddressPrefix(ctx context.Context, db DB, typeName, prefix string, seed func() (int64, error)) error {
	return registerAddressPrefixWith(ctx, db, typeName, prefix, seed, true)
}

func registerAddressPrefixWith(ctx context.Context, db DB, typeName, prefix string, seed func() (int64, error), reserve bool) error {
	if err := checkAddressPrefix(typeName, prefix); err != nil {
		return err
	}
	var current string
	var lastNo int64
	err := db.QueryRowContext(ctx,
		`SELECT prefix, last_no FROM smeldr_address_counters WHERE type_name = $1`, typeName).Scan(&current, &lastNo)
	switch {
	case err == nil && current == prefix:
		return nil
	case err == nil && lastNo > 0:
		return Err("prefix", fmt.Sprintf("%s's address prefix is %q and is fixed: the type already has addresses", typeName, current))
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("%w: read address prefix of %s: %s", ErrInternal, typeName, err)
	}
	var holder string
	herr := db.QueryRowContext(ctx,
		`SELECT type_name FROM smeldr_address_counters WHERE prefix = $1`, prefix).Scan(&holder)
	if herr == nil {
		return Err("prefix", fmt.Sprintf("%q is already the address prefix of %s", prefix, holder))
	}
	if !errors.Is(herr, sql.ErrNoRows) {
		return fmt.Errorf("%w: read address prefixes: %s", ErrInternal, herr)
	}
	if err == nil {
		// An existing row with no address yet: the prefix may still change.
		if _, err := db.ExecContext(ctx,
			`UPDATE smeldr_address_counters SET prefix = $1 WHERE type_name = $2 AND last_no = 0`, prefix, typeName); err != nil {
			return fmt.Errorf("%w: change address prefix of %s: %s", ErrInternal, typeName, err)
		}
		return nil
	}
	var start int64
	if seed != nil {
		if start, err = seed(); err != nil {
			return fmt.Errorf("%w: seed address counter of %s: %s", ErrInternal, typeName, err)
		}
	}
	reserved := int64(0)
	if reserve {
		reserved = start
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO smeldr_address_counters (type_name, prefix, last_no, reserved_no) VALUES ($1, $2, $3, $4) ON CONFLICT (type_name) DO NOTHING`,
		typeName, prefix, start, reserved); err != nil {
		return fmt.Errorf("%w: register address prefix of %s: %s", ErrInternal, typeName, err)
	}
	return nil
}

// addressPrefixOf returns typeName's registered prefix, or "" when it has none.
func addressPrefixOf(ctx context.Context, db DB, typeName string) (string, error) {
	var prefix string
	err := db.QueryRowContext(ctx, `SELECT prefix FROM smeldr_address_counters WHERE type_name = $1`, typeName).Scan(&prefix)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("%w: read address prefix of %s: %s", ErrInternal, typeName, err)
	}
	return prefix, nil
}

// proposeAddressPrefix proposes a prefix for typeName from its ASCII letters:
// the first 3, then the first 4, then the first 2 followed by each later letter
// in turn. The first candidate no type holds wins. A name with fewer than 2
// letters, or none free, is an error asking for an explicit prefix: there is no
// arbitrary fallback.
func proposeAddressPrefix(ctx context.Context, db DB, typeName string) (string, error) {
	var b strings.Builder
	for _, r := range typeName {
		if r >= 'a' && r <= 'z' {
			r -= 'a' - 'A'
		}
		if r >= 'A' && r <= 'Z' {
			b.WriteRune(r)
		}
	}
	letters := b.String()
	if len(letters) < 2 {
		return "", Err("prefix", fmt.Sprintf("no address prefix can be proposed for %q (it has fewer than 2 letters): give one of 2 to 4 capital letters", typeName))
	}
	var candidates []string
	if len(letters) >= 3 {
		candidates = append(candidates, letters[:3])
	}
	if len(letters) >= 4 {
		candidates = append(candidates, letters[:4])
	}
	if len(letters) == 2 {
		candidates = append(candidates, letters)
	}
	for i := 2; i < len(letters); i++ {
		candidates = append(candidates, letters[:2]+letters[i:i+1])
	}
	seen := map[string]bool{}
	for _, c := range candidates {
		if seen[c] {
			continue
		}
		seen[c] = true
		var holder string
		err := db.QueryRowContext(ctx, `SELECT type_name FROM smeldr_address_counters WHERE prefix = $1`, c).Scan(&holder)
		if errors.Is(err, sql.ErrNoRows) {
			return c, nil
		}
		if err != nil {
			return "", fmt.Errorf("%w: read address prefixes: %s", ErrInternal, err)
		}
	}
	return "", Err("prefix", fmt.Sprintf("every proposed address prefix for %q is taken: give one of 2 to 4 capital letters", typeName))
}

// allocateAddress takes typeName's next number in one statement on exec (the
// create's own transaction), so two concurrent creates never get the same
// number and a create that rolls back takes none. A type with no registered
// prefix is an error naming it.
func allocateAddress(ctx context.Context, exec DB, typeName string) (string, int64, error) {
	var prefix string
	var no int64
	err := exec.QueryRowContext(ctx,
		`UPDATE smeldr_address_counters SET last_no = last_no + 1 WHERE type_name = $1 RETURNING prefix, last_no`,
		typeName).Scan(&prefix, &no)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, fmt.Errorf("%w: %w for type %q", ErrInternal, errNoAddressPrefix, typeName)
	}
	if err != nil {
		return "", 0, fmt.Errorf("%w: allocate an address for %s: %s", ErrInternal, typeName, err)
	}
	return prefix + strconv.FormatInt(no, 10), no, nil
}

// legacyAddressSeed returns the counter start for a type whose existing rows
// predate addresses. For a type with a legacy human-ID column it is the highest
// number that parses with the prefix, unique or not, so the counter stays above
// every number the migration may keep. For a type without one it is the number
// of rows that have no address yet, which the migration numbers 1 to N. where
// narrows the rows (the dynamic table's type), with its arguments.
func legacyAddressSeed(ctx context.Context, db DB, table, legacy, prefix, where string, args ...any) (int64, error) {
	exists, err := tableExists(ctx, db, table)
	if err != nil || !exists {
		return 0, err
	}
	cond := "address = ''"
	if where != "" {
		cond += " AND " + where
	}
	if legacy == "" {
		var n int64
		err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table+` WHERE `+cond, args...).Scan(&n)
		return n, err
	}
	rows, err := db.QueryContext(ctx, `SELECT `+legacy+` FROM `+table, args...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var top int64
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return 0, err
		}
		v = strings.TrimSpace(v)
		if len(v) <= len(prefix) || !strings.EqualFold(v[:len(prefix)], prefix) {
			continue
		}
		if n, err := strconv.ParseInt(v[len(prefix):], 10, 64); err == nil && n > top {
			top = n
		}
	}
	return top, rows.Err()
}

// ensureAddressCounters creates smeldr_address_counters, or adds reserved_no
// to one created before it existed (idempotent).
func ensureAddressCounters(ctx context.Context, db DB) error {
	if _, err := db.ExecContext(ctx, createAddressCountersSQL); err != nil {
		return fmt.Errorf("smeldr: create smeldr_address_counters: %w", err)
	}
	if err := EnsureColumn(ctx, db, "smeldr_address_counters", "reserved_no", "BIGINT NOT NULL DEFAULT 0"); err != nil {
		return fmt.Errorf("smeldr: smeldr_address_counters.reserved_no: %w", err)
	}
	return nil
}

// registerCoreAddresses creates the counter table, the address columns and
// indexes on the tables of the named core record types ([coreAddressTables]),
// and registers their core prefixes, seeded from their existing rows. The
// tables must exist.
func registerCoreAddresses(ctx context.Context, db DB, types ...string) error {
	if err := ensureAddressCounters(ctx, db); err != nil {
		return err
	}
	for _, t := range coreAddressTables {
		if !slices.Contains(types, t.typeName) {
			continue
		}
		if err := ensureAddressStorage(ctx, db, t.table, false); err != nil {
			return err
		}
		prefix := coreAddressPrefixes[t.typeName]
		if held, err := heldByLegacyPlaceType(ctx, db, t.typeName, prefix); err != nil {
			return err
		} else if held {
			// The instance still keeps the runtime type this place type
			// replaces (D142): its counter keeps the prefix until
			// MigratePlaces moves it, so boot does not fail before the
			// migration can run.
			slog.WarnContext(ctx, "smeldr: address prefix still held by the runtime type the place type replaces; run migrate-places",
				"type", t.typeName, "prefix", prefix, "holder", legacyPlaceTypes[t.typeName])
			continue
		}
		table, legacy := t.table, t.legacy
		register := registerAddressPrefix
		if legacy == "" {
			register = registerReservingAddressPrefix
		}
		if err := register(ctx, db, t.typeName, prefix, func() (int64, error) {
			return legacyAddressSeed(ctx, db, table, legacy, prefix, "")
		}); err != nil {
			return err
		}
	}
	return nil
}

// heldByLegacyPlaceType reports whether prefix is still the address prefix of
// the lowercase runtime type typeName replaces (domain for Domain, D142).
func heldByLegacyPlaceType(ctx context.Context, db DB, typeName, prefix string) (bool, error) {
	legacy, ok := legacyPlaceTypes[typeName]
	if !ok {
		return false, nil
	}
	var holder string
	err := db.QueryRowContext(ctx, `SELECT type_name FROM smeldr_address_counters WHERE prefix = $1`, prefix).Scan(&holder)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("%w: read address prefix %s: %s", ErrInternal, prefix, err)
	}
	return holder == legacy, nil
}

// RecordAddress is a record's system-assigned address (D110), embedded in the
// six orchestration types and in [DynamicNode]. The server assigns Address and
// AddressNo when the record is created, in the same transaction, and they never
// change: create and update input never sets them (the readonly tag), and every
// update path keeps the stored value. ExternalRef holds a customer's own
// numbering as free text and is writable.
type RecordAddress struct {
	// Address is the full address, the type prefix plus the number ("D110").
	// Empty on a record created before addresses, until the migration runs.
	Address string `json:"address" db:"address" smeldr:"readonly"`
	// AddressNo is the number alone (110), for numeric order.
	AddressNo int64 `json:"address_no" db:"address_no" smeldr:"readonly"`
	// ExternalRef is the caller's own reference for the record, free text.
	ExternalRef string `json:"external_ref" db:"external_ref" smeldr_description:"Your own reference or numbering for this record, free text. The address (for example D110) is assigned by the server and returned in the result; do not send one."`
}

// assignAddress sets the address the server allocated.
func (a *RecordAddress) assignAddress(address string, no int64) {
	a.Address, a.AddressNo = address, no
}

// itemAddress returns the record's address, "" when it has none.
func (a *RecordAddress) itemAddress() string { return a.Address }

// keepAddress copies the stored address from existing onto a, so no update
// can change it.
func (a *RecordAddress) keepAddress(existing any) {
	if e, ok := existing.(addressAssignable); ok {
		a.Address, a.AddressNo = e.itemAddress(), e.itemAddressNo()
	}
}

// itemExternalRef returns the record's external reference.
func (a *RecordAddress) itemExternalRef() string { return a.ExternalRef }

// itemAddressNo returns the record's address number.
func (a *RecordAddress) itemAddressNo() int64 { return a.AddressNo }

// addressAssignable is implemented by every type that embeds [RecordAddress].
type addressAssignable interface {
	assignAddress(address string, no int64)
	itemAddress() string
	itemAddressNo() int64
	keepAddress(existing any)
}

var recordAddressType = reflect.TypeOf(RecordAddress{})

// addressOption is the internal [Option] that makes a Module allocate an
// address for every item it creates. [RegisterOrchestrationTypes] sets it for
// the six orchestration types; web-content modules never have it.
type addressOption struct{}

func (addressOption) isOption() {}

// withAddress returns the [addressOption].
func withAddress() Option { return addressOption{} }

// assignNewAddress allocates the item's address on exec (the create's
// transaction) and sets it on item. Without a database it numbers from the
// module's in-memory counter under the type's core prefix. A module without
// [withAddress], or an item that cannot carry one, gets none.
func (m *Module[T]) assignNewAddress(ctx context.Context, exec DB, item T) error {
	if !m.addressed {
		return nil
	}
	a, ok := any(item).(addressAssignable)
	if !ok {
		return nil
	}
	var address string
	var no int64
	if exec == nil {
		no = m.memAddress.next()
		address = coreAddressPrefixes[m.contentTypeName] + strconv.FormatInt(no, 10)
	} else {
		var err error
		if address, no, err = allocateAddress(ctx, exec, m.contentTypeName); err != nil {
			return err
		}
	}
	a.assignAddress(address, no)
	// The transition period (D110 7.1): a legacy human-ID field mirrors the
	// address, and a different value sent there moves to external_ref.
	mirrorLegacyHumanID(any(item), address)
	return nil
}

// isAddressedSchema reports whether a runtime-defined type's items are records
// that carry an address: every dynamic type is "content", so the line between
// a record and web content is the public prefix. A type with a URLPrefix is
// web content and has no address.
func isAddressedSchema(s *ContentTypeSchema) bool {
	return s != nil && s.URLPrefix == ""
}

// dynamicAddressSeed seeds a dynamic record type's counter from its items that
// have no address yet, which the migration numbers 1 to N.
//
// Call it on the base handle, never inside a transaction. It is 0 for a type
// that is not a record.
func dynamicAddressSeed(ctx context.Context, db DB, s *ContentTypeSchema) (int64, error) {
	if !isAddressedSchema(s) {
		return 0, nil
	}
	return legacyAddressSeed(ctx, db, "smeldr_dynamic_content", "", "", "type_name = $1 AND slug <> ''", s.TypeName)
}

// settleDynamicPrefix decides and registers the address prefix of a
// runtime-defined record type. A core type (task_plan) takes its reserved
// prefix, and any other value is refused; otherwise asked is used, or, when it
// is empty, one is proposed. Web content (a URLPrefix) takes none, and a prefix
// asked for it is refused. It returns the prefix set ("" for web content) and
// whether it was proposed. db is where it writes, a transaction or the base
// handle. seed is the counter start for a new registry row, read beforehand
// on the base handle ([dynamicAddressSeed]): a table probe never runs inside a
// transaction, and a read on the base handle inside one waits forever on a
// one-connection SQLite.
func settleDynamicPrefix(ctx context.Context, db DB, s *ContentTypeSchema, asked string, seed int64) (prefix string, proposed bool, err error) {
	if !isAddressedSchema(s) {
		if asked != "" {
			return "", false, Err("prefix", "a type with a url_prefix is web content and has no address prefix")
		}
		return "", false, nil
	}
	prefix = asked
	if want, core := coreAddressPrefixes[s.TypeName]; core {
		if prefix != "" && prefix != want {
			return "", false, Err("prefix", fmt.Sprintf("%s's address prefix is reserved as %q", s.TypeName, want))
		}
		prefix = want
	}
	if prefix == "" {
		if prefix, err = proposeAddressPrefix(ctx, db, s.TypeName); err != nil {
			return "", false, err
		}
		proposed = true
	}
	if err := registerReservingAddressPrefix(ctx, db, s.TypeName, prefix, func() (int64, error) { return seed, nil }); err != nil {
		return "", false, err
	}
	return prefix, proposed, nil
}

// memoryAddressCounter numbers a Module's items when it has no database, with
// the same rules in one process.
type memoryAddressCounter struct {
	mu   sync.Mutex
	last int64
}

func (c *memoryAddressCounter) next() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.last++
	return c.last
}

// keepStoredAddress sets the stored address of existing back on the decoded
// update pv, so no update path can change it.
func keepStoredAddress(pv reflect.Value, existing any) {
	if a, ok := pv.Interface().(addressAssignable); ok {
		a.keepAddress(existing)
	}
}

// addressOf returns item's address, or "" when it has none.
func addressOf(item any) string {
	if a, ok := item.(addressAssignable); ok {
		return a.itemAddress()
	}
	return ""
}
