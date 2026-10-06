// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"encoding/json"
	"testing"
)

func TestRawMessageScanner(t *testing.T) {
	t.Run("a string, the shape Postgres returns for TEXT", func(t *testing.T) {
		var dst json.RawMessage
		if err := (rawMessageScanner{&dst}).Scan(`{"a":1}`); err != nil || string(dst) != `{"a":1}` {
			t.Errorf("dst = %q, err = %v", dst, err)
		}
	})
	t.Run("bytes, the shape SQLite returns, are copied", func(t *testing.T) {
		src := []byte(`{"a":1}`)
		var dst json.RawMessage
		if err := (rawMessageScanner{&dst}).Scan(src); err != nil {
			t.Fatal(err)
		}
		src[2] = 'z' // a driver may reuse its buffer after Scan returns
		if string(dst) != `{"a":1}` {
			t.Errorf("dst = %q: it aliases the driver's buffer", dst)
		}
	})
	t.Run("NULL is a nil message", func(t *testing.T) {
		dst := json.RawMessage(`stale`)
		if err := (rawMessageScanner{&dst}).Scan(nil); err != nil || dst != nil {
			t.Errorf("dst = %q (nil %v), err = %v", dst, dst == nil, err)
		}
	})
	t.Run("another type is an error and the destination is untouched", func(t *testing.T) {
		dst := json.RawMessage(`keep`)
		if err := (rawMessageScanner{&dst}).Scan(int64(7)); err == nil || string(dst) != "keep" {
			t.Errorf("dst = %q, err = %v, want an error and the old value", dst, err)
		}
	})
}

type rawItem struct {
	ID     string          `db:"id"`
	Slug   string          `db:"slug"`
	Status string          `db:"status"`
	Body   json.RawMessage `db:"body"`
}

func rawItemsDB(t *testing.T) DB {
	t.Helper()
	db := newSQLiteDB(t)
	if _, err := db.Exec(`CREATE TABLE raw_items (id TEXT PRIMARY KEY, slug TEXT NOT NULL, status TEXT NOT NULL, body TEXT)`); err != nil {
		t.Fatal(err)
	}
	return db
}

// TestQuery_RawMessageFromAStringColumn: a TEXT column inserted as text is returned by
// the driver as a Go string, which is also what Postgres returns for every TEXT
// column. Before the scanner it failed with "unsupported Scan, storing driver.Value
// type string into type *json.RawMessage".
func TestQuery_RawMessageFromAStringColumn(t *testing.T) {
	ctx := context.Background()
	db := rawItemsDB(t)
	if _, err := db.ExecContext(ctx, `INSERT INTO raw_items VALUES ('a', 'a', 's', '{"k":"välue"}')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO raw_items VALUES ('b', 'b', 's', $1)`, []byte(`{"k":2}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO raw_items VALUES ('c', 'c', 's', NULL)`); err != nil {
		t.Fatal(err)
	}
	rows, err := Query[rawItem](ctx, db, `SELECT * FROM raw_items ORDER BY id`)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(rows) != 3 || string(rows[0].Body) != `{"k":"välue"}` || string(rows[1].Body) != `{"k":2}` || rows[2].Body != nil {
		t.Errorf("rows = %+v", rows)
	}
}

// TestRawMessage_SameValuesAsDatabaseSQLWhereItWorked: for bytes, the one source
// database/sql could scan into a json.RawMessage, the new scanner gives the same
// value. For NULL it does not: database/sql returned an error (on SQLite too, so no
// reader got a value from a NULL before), the scanner gives a nil message.
func TestRawMessage_SameValuesAsDatabaseSQLWhereItWorked(t *testing.T) {
	db := newSQLiteDB(t)
	var before json.RawMessage
	if err := db.QueryRow(`SELECT NULL`).Scan(&before); err == nil {
		t.Fatal("database/sql used to refuse a NULL into a json.RawMessage; the premise of this test is gone")
	}
	after := json.RawMessage(`stale`)
	if err := db.QueryRow(`SELECT NULL`).Scan(scanDest(&after)); err != nil || after != nil {
		t.Errorf("scanDest NULL = %q, %v, want a nil message", after, err)
	}
	var plain json.RawMessage
	if err := db.QueryRow(`SELECT CAST('{"a":1}' AS BLOB)`).Scan(&plain); err != nil {
		t.Fatal(err)
	}
	var viaScanDest json.RawMessage
	if err := db.QueryRow(`SELECT CAST('{"a":1}' AS BLOB)`).Scan(scanDest(&viaScanDest)); err != nil {
		t.Fatal(err)
	}
	if string(plain) != string(viaScanDest) {
		t.Errorf("bytes: database/sql gave %q, scanDest gives %q", plain, viaScanDest)
	}
}

func TestSQLRepo_RawMessageRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := rawItemsDB(t)
	repo := NewSQLRepo[rawItem](db, Table("raw_items"))
	if err := repo.Save(ctx, rawItem{ID: "x", Slug: "x", Status: "s", Body: json.RawMessage(`{"n":[1,2]}`)}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := repo.FindByID(ctx, "x")
	if err != nil || string(got.Body) != `{"n":[1,2]}` {
		t.Errorf("FindByID = %+v, %v", got, err)
	}
}

// TestSeedBlockTypeSchemas_KeepsACustomisedRow: the seed's ON CONFLICT DO NOTHING keeps
// a row that already exists, as INSERT OR IGNORE did.
func TestSeedBlockTypeSchemas_KeepsACustomisedRow(t *testing.T) {
	ctx := context.Background()
	db := newSQLiteDB(t)
	if err := CreateSchemaTable(db); err != nil {
		t.Fatal(err)
	}
	if err := SeedBlockTypeSchemas(db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE smeldr_content_type_schemas SET label = 'Customised' WHERE type_name = 'content_block'`); err != nil {
		t.Fatal(err)
	}
	if err := SeedBlockTypeSchemas(db); err != nil {
		t.Fatal(err)
	}
	var label string
	var n int
	if err := db.QueryRowContext(ctx, `SELECT label FROM smeldr_content_type_schemas WHERE type_name = 'content_block'`).Scan(&label); err != nil || label != "Customised" {
		t.Errorf("label = %q, %v, want the customisation kept", label, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM smeldr_content_type_schemas`).Scan(&n); err != nil || n != 16 {
		t.Errorf("rows = %d, %v, want 16", n, err)
	}
}
