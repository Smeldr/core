package smeldr

// Error-path coverage for dynamic.go (DynamicTypeRepo methods).

import (
	"context"
	"database/sql"
	"errors"
	"net/http/httptest"
	"testing"
	"time"
)

// wrapExecErrDB delegates to an embedded DB for queries but always fails
// ExecContext. Used to test save-error paths without triggering nil-Rows panics
// on QueryContext calls.
type wrapExecErrDB struct {
	DB
}

func (w *wrapExecErrDB) ExecContext(_ context.Context, _ string, _ ...any) (sql.Result, error) {
	return nil, errors.New("exec error")
}

// — CreateDraft error paths ————————————————————————————————————————————————

// Covers dynamic.go:36-38 — json.Marshal fails on non-serializable value.
func TestDynamicTypeRepo_CreateDraft_MarshalError(t *testing.T) {
	repo := &DynamicTypeRepo{db: &errQueryDB{}, typeName: "article"}
	_, err := repo.CreateDraft(context.Background(), map[string]any{"bad": make(chan int)})
	if err == nil {
		t.Error("want error for non-serializable field, got nil")
	}
}

// Covers dynamic.go:54-56 — repo.Save fails when ExecContext returns error.
// Uses a no-table SQLite DB: slugExists gets a query error (silently ignored),
// then Save's ExecContext returns our error.
func TestDynamicTypeRepo_CreateDraft_SaveError(t *testing.T) {
	inner := newSQLiteDB(t) // no tables → slugExists query fails (ignored by slugExists)
	repo := &DynamicTypeRepo{db: &wrapExecErrDB{inner}, typeName: "article"}
	_, err := repo.CreateDraft(context.Background(), map[string]any{"title": "hello"})
	if err == nil {
		t.Error("want error when Save fails, got nil")
	}
}

// — GetBySlug / GetByID query error paths ——————————————————————————————————

func TestDynamicTypeRepo_GetBySlug_QueryError(t *testing.T) {
	repo := &DynamicTypeRepo{db: &errQueryDB{}, typeName: "article"}
	_, err := repo.GetBySlug(context.Background(), "any-slug")
	if err == nil {
		t.Error("want query error from GetBySlug, got nil")
	}
}

func TestDynamicTypeRepo_GetByID_QueryError(t *testing.T) {
	repo := &DynamicTypeRepo{db: &errQueryDB{}, typeName: "article"}
	_, err := repo.GetByID(context.Background(), "any-id")
	if err == nil {
		t.Error("want query error from GetByID, got nil")
	}
}

// — List: invalid JSON node → continue —————————————————————————————————————

// Covers dynamic.go:136-138 — the continue path when node.Fields is bad JSON.
func TestDynamicTypeRepo_List_InvalidJSONNode(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateBlockTables(db); err != nil {
		t.Fatalf("CreateBlockTables: %v", err)
	}
	now := time.Now().UTC()
	zeroTime := time.Time{}
	_, err := db.ExecContext(context.Background(), `
		INSERT INTO smeldr_dynamic_content
			(id, type_name, slug, status, fields, created_at, updated_at, scheduled_at, published_at, rev)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NULL, $8, 0)`,
		// []byte so the driver stores it as BLOB → scans back into json.RawMessage;
		// a Go string would cause a driver Scan error before reaching json.Unmarshal.
		"bad-json-id", "article", "bad-slug", "draft", []byte(`not{valid}json`), now, now, zeroTime,
	)
	if err != nil {
		t.Fatalf("insert bad-JSON node: %v", err)
	}
	repo := &DynamicTypeRepo{db: db, typeName: "article"}
	items, err := repo.List(context.Background(), ListOptions{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("want 0 items (bad-JSON node skipped), got %d", len(items))
	}
}

// — UpdateFields error paths ————————————————————————————————————————————————

// Covers dynamic.go:162-164 — GetByID fails → UpdateFields returns early.
func TestDynamicTypeRepo_UpdateFields_GetByIDError(t *testing.T) {
	repo := &DynamicTypeRepo{db: &errQueryDB{}, typeName: "article"}
	err := repo.UpdateFields(context.Background(), "id-1", map[string]any{"title": "new"})
	if err == nil {
		t.Error("want error when GetByID fails in UpdateFields, got nil")
	}
}

// Covers dynamic.go:170-172 — node has invalid JSON fields → Unmarshal fails.
func TestDynamicTypeRepo_UpdateFields_BadExistingJSON(t *testing.T) {
	db := newSQLiteDB(t)
	if err := CreateBlockTables(db); err != nil {
		t.Fatalf("CreateBlockTables: %v", err)
	}
	now := time.Now().UTC()
	nodeID := "node-bad-json"
	zeroTime := time.Time{}
	_, err := db.ExecContext(context.Background(), `
		INSERT INTO smeldr_dynamic_content
			(id, type_name, slug, status, fields, created_at, updated_at, scheduled_at, published_at, rev)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NULL, $8, 0)`,
		nodeID, "article", "slug-1", "draft", []byte(`{bad json}`), now, now, zeroTime,
	)
	if err != nil {
		t.Fatalf("insert bad-JSON node: %v", err)
	}
	repo := &DynamicTypeRepo{db: db, typeName: "article"}
	err = repo.UpdateFields(context.Background(), nodeID, map[string]any{"title": "updated"})
	if err == nil {
		t.Error("want unmarshal error for bad existing JSON, got nil")
	}
}

// — writeDynamicJSON: encode error path ————————————————————————————————————

// Covers dynamic.go:444-446 — json.Encode fails on non-serializable value.
func TestWriteDynamicJSON_EncodeError(t *testing.T) {
	w := httptest.NewRecorder()
	writeDynamicJSON(w, map[string]any{"bad": make(chan int)})
	// No assertion needed — function logs the error; test passes if no panic.
}
