// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

// TestAuthorityTypes_embedNode verifies at compile time that both Authority
// types embed [Node] and are pointer-receiverable by the generic content
// module infrastructure.
func TestAuthorityTypes_embedNode(t *testing.T) {
	t.Run("Rule", func(t *testing.T) {
		var r Rule
		_ = r.Node
		_ = r.Slug
		_ = r.Surface
		_ = r.RuleType
		_ = r.Body
		_ = r.SourceRef
	})
	t.Run("AuthorityStub", func(t *testing.T) {
		var s AuthorityStub
		_ = s.Node
		_ = s.Slug
		_ = s.SourceRef
		_ = s.RuleType
		_ = s.Surface
		_ = s.SourceHash
	})
}

// TestRuleFlow_definition verifies the authority-rule flow has the expected
// states and transitions without requiring a database.
func TestRuleFlow_definition(t *testing.T) {
	f := ruleFlow()
	if f.Name != "authority-rule" {
		t.Errorf("Name = %q, want %q", f.Name, "authority-rule")
	}
	if f.TypeName != "Rule" {
		t.Errorf("TypeName = %q, want %q", f.TypeName, "Rule")
	}
	wantStates := []string{"draft", "active", "retired"}
	if got := stateNames(f); got != join(wantStates) {
		t.Errorf("states = %s, want %s", got, join(wantStates))
	}
	if got := initialState(f); got != "draft" {
		t.Errorf("initial = %q, want %q", got, "draft")
	}
	wantTerminals := []string{"retired"}
	if got := terminalStates(f); got != join(wantTerminals) {
		t.Errorf("terminals = %s, want %s", got, join(wantTerminals))
	}
	if len(f.Transitions) != 2 {
		t.Errorf("transitions count = %d, want 2", len(f.Transitions))
	}
}

// TestAuthorityStubFlow_definition verifies the authority-stub flow has the
// expected states and transitions without requiring a database.
func TestAuthorityStubFlow_definition(t *testing.T) {
	f := authorityStubFlow()
	if f.Name != "authority-stub" {
		t.Errorf("Name = %q, want %q", f.Name, "authority-stub")
	}
	if f.TypeName != "AuthorityStub" {
		t.Errorf("TypeName = %q, want %q", f.TypeName, "AuthorityStub")
	}
	wantStates := []string{"stub", "converted", "retired"}
	if got := stateNames(f); got != join(wantStates) {
		t.Errorf("states = %s, want %s", got, join(wantStates))
	}
	if got := initialState(f); got != "stub" {
		t.Errorf("initial = %q, want %q", got, "stub")
	}
	wantTerminals := []string{"converted", "retired"}
	if got := terminalStates(f); got != join(wantTerminals) {
		t.Errorf("terminals = %s, want %s", got, join(wantTerminals))
	}
	if len(f.Transitions) != 2 {
		t.Errorf("transitions count = %d, want 2", len(f.Transitions))
	}
}

// TestCreateAuthorityTables verifies both Authority tables are created and
// queryable.
func TestCreateAuthorityTables(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateAuthorityTables(db); err != nil {
		t.Fatalf("CreateAuthorityTables: %v", err)
	}
	ctx := context.Background()
	for _, table := range []string{"smeldr_rules", "smeldr_authority_stubs"} {
		row := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table)
		var n int
		if err := row.Scan(&n); err != nil {
			t.Errorf("table %s not queryable: %v", table, err)
		}
	}
}

// TestCreateAuthorityTables_DBError verifies CreateAuthorityTables returns
// an error when the database rejects a DDL statement — covering both
// statements' own error-return path (smeldr_rules first, then
// smeldr_authority_stubs once the first succeeds).
func TestCreateAuthorityTables_DBError(t *testing.T) {
	t.Run("smeldr_rules", func(t *testing.T) {
		db := newSQLiteDB(t)
		failing := &execFailDB{DB: db, failOn: "smeldr_rules"}
		if err := CreateAuthorityTables(failing); err == nil {
			t.Error("expected error from failing DB, got nil")
		}
	})
	t.Run("smeldr_authority_stubs", func(t *testing.T) {
		db := newSQLiteDB(t)
		failing := &execFailDB{DB: db, failOn: "smeldr_authority_stubs"}
		if err := CreateAuthorityTables(failing); err == nil {
			t.Error("expected error from failing DB, got nil")
		}
	})
}

// TestRegisterAuthorityTypes_nilDB verifies that RegisterAuthorityTypes
// tolerates an App with nil DB by logging and continuing (fail-open).
func TestRegisterAuthorityTypes_nilDB(t *testing.T) {
	app := New(Config{BaseURL: "http://localhost", Secret: []byte("test-secret-key!!")})
	// Must not panic; errors are logged internally.
	RegisterAuthorityTypes(app, nil)
}

// TestRegisterAuthorityTypes_flows verifies that with a real SQLite DB both
// Authority flows are persisted via RegisterFlow.
func TestRegisterAuthorityTypes_flows(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()
	if err := migrateStateFlows(ctx, db); err != nil {
		t.Fatalf("migrateStateFlows: %v", err)
	}
	if err := CreateAuthorityTables(db); err != nil {
		t.Fatalf("CreateAuthorityTables: %v", err)
	}
	app := New(Config{
		BaseURL: "http://localhost",
		Secret:  []byte("test-secret-key!!"),
		DB:      db,
	})
	// Must not panic; should register both flows without logging errors.
	RegisterAuthorityTypes(app, db)

	row := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM smeldr_state_flows WHERE type_name IN ('Rule', 'AuthorityStub')")
	var n int
	if err := row.Scan(&n); err != nil {
		t.Fatalf("count state flows: %v", err)
	}
	if n != 2 {
		t.Errorf("registered flow count = %d, want 2", n)
	}
}

// TestRegisterAuthorityRelationKinds_RoundTrip verifies the materializes
// kind is registered with the expected fields and that a second call is
// idempotent.
func TestRegisterAuthorityRelationKinds_RoundTrip(t *testing.T) {
	store := setupRelationStore(t)
	ctx := context.Background()

	if err := RegisterAuthorityRelationKinds(ctx, store); err != nil {
		t.Fatalf("RegisterAuthorityRelationKinds: %v", err)
	}

	kinds := store.ListKinds()
	if len(kinds) != 1 {
		t.Fatalf("ListKinds: got %d kinds, want 1: %+v", len(kinds), kinds)
	}
	k := kinds[0]
	if k.TypeName != "materializes" {
		t.Errorf("TypeName = %q, want %q", k.TypeName, "materializes")
	}
	if k.Label != "Materializes" {
		t.Errorf("Label = %q, want %q", k.Label, "Materializes")
	}
	if k.ReverseLabel != "Materialized From" {
		t.Errorf("ReverseLabel = %q, want %q", k.ReverseLabel, "Materialized From")
	}
	if k.Mode != "asserted" {
		t.Errorf("Mode = %q, want %q", k.Mode, "asserted")
	}
	if !k.Directional {
		t.Error("Directional = false, want true")
	}
	if k.Weighted {
		t.Error("Weighted = true, want false")
	}
	wantPairs := `[{"source_type":"AuthorityStub","target_type":"Rule"}]`
	if string(k.TypePairs) != wantPairs {
		t.Errorf("TypePairs = %s, want %s", k.TypePairs, wantPairs)
	}

	// Idempotent — a second call must not error or duplicate the kind.
	if err := RegisterAuthorityRelationKinds(ctx, store); err != nil {
		t.Fatalf("second RegisterAuthorityRelationKinds call: %v", err)
	}
	if got := len(store.ListKinds()); got != 1 {
		t.Errorf("after second call: got %d kinds, want 1", got)
	}
}

// TestRegisterAuthorityRelationKinds_UpsertError verifies the UpsertKind
// failure path returns a wrapped error naming the failing kind.
func TestRegisterAuthorityRelationKinds_UpsertError(t *testing.T) {
	store := mockRelationStore(&errExecDB{})
	err := RegisterAuthorityRelationKinds(context.Background(), store)
	if err == nil {
		t.Fatal("want error when UpsertKind fails, got nil")
	}
	if !strings.Contains(err.Error(), `"materializes"`) {
		t.Errorf("error = %q, want it to name the failing kind %q", err.Error(), "materializes")
	}
}

// — RuleType rank mechanism (decision-governance §3, A304) ————————————————————

func TestCreateRuleTypeRankTable(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateRuleTypeRankTable(db); err != nil {
		t.Fatalf("CreateRuleTypeRankTable: %v", err)
	}
	row := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM smeldr_rule_type_ranks")
	var n int
	if err := row.Scan(&n); err != nil {
		t.Errorf("table not queryable: %v", err)
	}
}

func TestCreateRuleTypeRankTable_DBError(t *testing.T) {
	db := newSQLiteDB(t)
	failing := &execFailDB{DB: db, failOn: "smeldr_rule_type_ranks"}
	if err := CreateRuleTypeRankTable(failing); err == nil {
		t.Error("expected error from failing DB, got nil")
	}
}

func TestSetRuleTypeOrder_RoundTrip(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateRuleTypeRankTable(db); err != nil {
		t.Fatalf("CreateRuleTypeRankTable: %v", err)
	}
	ctx := context.Background()
	names := []string{"precedent", "design-system", "architecture", "constitution"}
	if err := SetRuleTypeOrder(ctx, db, names); err != nil {
		t.Fatalf("SetRuleTypeOrder: %v", err)
	}
	for wantRank, name := range names {
		rank, ok, err := RuleTypeRank(ctx, db, name)
		if err != nil {
			t.Fatalf("RuleTypeRank(%q): %v", name, err)
		}
		if !ok {
			t.Fatalf("RuleTypeRank(%q): ok = false, want true", name)
		}
		if rank != wantRank {
			t.Errorf("RuleTypeRank(%q) = %d, want %d", name, rank, wantRank)
		}
	}
}

func TestSetRuleTypeOrder_ReplaceDropsStale(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateRuleTypeRankTable(db); err != nil {
		t.Fatalf("CreateRuleTypeRankTable: %v", err)
	}
	ctx := context.Background()
	if err := SetRuleTypeOrder(ctx, db, []string{"precedent", "design-system"}); err != nil {
		t.Fatalf("first SetRuleTypeOrder: %v", err)
	}
	if err := SetRuleTypeOrder(ctx, db, []string{"architecture", "constitution"}); err != nil {
		t.Fatalf("second SetRuleTypeOrder: %v", err)
	}
	if _, ok, err := RuleTypeRank(ctx, db, "precedent"); err != nil || ok {
		t.Errorf(`RuleTypeRank("precedent") after replace: ok=%v, err=%v, want ok=false`, ok, err)
	}
	rank, ok, err := RuleTypeRank(ctx, db, "architecture")
	if err != nil || !ok || rank != 0 {
		t.Errorf(`RuleTypeRank("architecture") after replace = (%d, %v, %v), want (0, true, nil)`, rank, ok, err)
	}
}

func TestSetRuleTypeOrder_DBError(t *testing.T) {
	t.Run("delete fails", func(t *testing.T) {
		db := newSQLiteDB(t)
		if err := CreateRuleTypeRankTable(db); err != nil {
			t.Fatalf("CreateRuleTypeRankTable: %v", err)
		}
		failing := &execFailDB{DB: db, failOn: "DELETE FROM smeldr_rule_type_ranks"}
		if err := SetRuleTypeOrder(context.Background(), failing, []string{"a"}); err == nil {
			t.Error("expected error when DELETE fails, got nil")
		}
	})
	t.Run("insert fails", func(t *testing.T) {
		db := newSQLiteDB(t)
		if err := CreateRuleTypeRankTable(db); err != nil {
			t.Fatalf("CreateRuleTypeRankTable: %v", err)
		}
		failing := &execFailDB{DB: db, failOn: "INSERT INTO smeldr_rule_type_ranks"}
		if err := SetRuleTypeOrder(context.Background(), failing, []string{"a"}); err == nil {
			t.Error("expected error when INSERT fails, got nil")
		}
	})
}

func TestRuleTypeRank_Unregistered(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateRuleTypeRankTable(db); err != nil {
		t.Fatalf("CreateRuleTypeRankTable: %v", err)
	}
	rank, ok, err := RuleTypeRank(context.Background(), db, "never-set")
	if err != nil {
		t.Fatalf("RuleTypeRank: %v", err)
	}
	if ok {
		t.Error("ok = true, want false for an unregistered name")
	}
	if rank != 0 {
		t.Errorf("rank = %d, want 0 (zero value) alongside ok=false", rank)
	}
}

// rankQueryErrDB makes QueryRowContext.Scan fail with a non-ErrNoRows error.
type rankQueryErrDB struct{}

func (r *rankQueryErrDB) ExecContext(_ context.Context, _ string, _ ...any) (sql.Result, error) {
	return nil, nil
}
func (r *rankQueryErrDB) QueryContext(_ context.Context, _ string, _ ...any) (*sql.Rows, error) {
	return nil, nil
}
func (r *rankQueryErrDB) QueryRowContext(ctx context.Context, _ string, _ ...any) *sql.Row {
	conn := &errRowConn{}
	return sql.OpenDB(conn).QueryRowContext(ctx, "SELECT v")
}

func TestRuleTypeRank_DBError(t *testing.T) {
	_, ok, err := RuleTypeRank(context.Background(), &rankQueryErrDB{}, "anything")
	if err == nil {
		t.Fatal("want error from a scan failure, got nil")
	}
	if ok {
		t.Error("ok = true, want false alongside an error")
	}
}

// — Reversibility (decision-governance §3/§7, A304) ————————————————————————————

func TestInferReversibility_AllowlistedClasses(t *testing.T) {
	cases := []DestructiveOperationClass{ClassDelete, ClassExternalCommunication, ClassFundsTransfer}
	for _, class := range cases {
		t.Run(string(class), func(t *testing.T) {
			value, ok := InferReversibility(class)
			if !ok {
				t.Fatalf("InferReversibility(%q): ok = false, want true", class)
			}
			if value != Irreversible {
				t.Errorf("InferReversibility(%q) = %q, want %q", class, value, Irreversible)
			}
		})
	}
}

func TestInferReversibility_UnknownClass(t *testing.T) {
	value, ok := InferReversibility(DestructiveOperationClass("some-unlisted-class"))
	if ok {
		t.Error("ok = true, want false for an unlisted class")
	}
	if value != "" {
		t.Errorf("value = %q, want empty alongside ok=false", value)
	}
}

func TestResolveReversibility(t *testing.T) {
	tests := []struct {
		name       string
		inferred   Reversibility
		inferredOK bool
		declared   Reversibility
		want       Reversibility
	}{
		{"agree", Irreversible, true, Irreversible, Irreversible},
		{"disagree", Irreversible, true, Reversible, ReversibilityDisputed},
		{"declared only", "", false, ConditionallyReversible, ConditionallyReversible},
		{"inferred only", Irreversible, true, "", Irreversible},
		{"neither", "", false, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveReversibility(tt.inferred, tt.inferredOK, tt.declared)
			if got != tt.want {
				t.Errorf("ResolveReversibility(%q, %v, %q) = %q, want %q",
					tt.inferred, tt.inferredOK, tt.declared, got, tt.want)
			}
		})
	}
}
