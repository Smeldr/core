// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

// wantTypedG holds err to its refusal type, its exact values (nil for none)
// and its exact Error text.
func wantTypedG(t *testing.T, err error, typ ProblemType, values map[string]string, msg string) {
	t.Helper()
	if err == nil {
		t.Fatalf("err = nil; want %s", typ)
	}
	if got := ProblemTypeOf(err); got != typ {
		t.Errorf("ProblemTypeOf = %q; want %q", got, typ)
	}
	if got := ProblemValuesOf(err); !reflect.DeepEqual(got, values) {
		t.Errorf("ProblemValuesOf = %#v; want %#v", got, values)
	}
	if got := err.Error(); got != msg {
		t.Errorf("Error() = %q; want %q", got, msg)
	}
}

// wantTypedGField holds err to a single-field ValidationError on field.
func wantTypedGField(t *testing.T, err error, field string) {
	t.Helper()
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("err = %v; want a ValidationError", err)
	}
	if fs := ve.Fields(); len(fs) != 1 || fs[0].Field != field {
		t.Errorf("fields = %+v; want one on %s", fs, field)
	}
}

func TestPhaseThreeG_AddressPrefixReserved(t *testing.T) {
	err := checkAddressPrefix("Decision", "DEC")
	wantTypedG(t, err, problemAddressPrefixReserved, map[string]string{"type": "Decision", "prefix": "D"},
		`validation failed: prefix: Decision's address prefix is reserved as "D" and cannot be changed`)
	wantTypedGField(t, err, "prefix")
}

func TestPhaseThreeG_AddressPrefixSingleLetter(t *testing.T) {
	err := checkAddressPrefix("invoice", "Q")
	wantTypedG(t, err, problemAddressPrefixSingleLetter, map[string]string{"prefix": "Q", "type": "invoice"},
		`validation failed: prefix: single-letter prefixes are reserved for core types; "Q" is not available for invoice (use 2 to 4 letters)`)
	wantTypedGField(t, err, "prefix")
}

func TestPhaseThreeG_AddressPrefixFixedAndTaken(t *testing.T) {
	ctx := context.Background()
	db := addressDB(t)
	if err := registerAddressPrefix(ctx, db, "invoice", "INV", nil); err != nil {
		t.Fatal(err)
	}
	err := registerAddressPrefix(ctx, db, "ticket", "INV", nil)
	wantTypedG(t, err, problemAddressPrefixTaken, map[string]string{"prefix": "INV", "type": "invoice"},
		`validation failed: prefix: "INV" is already the address prefix of invoice`)
	wantTypedGField(t, err, "prefix")

	if _, _, err := allocateAddress(ctx, db, "invoice"); err != nil {
		t.Fatal(err)
	}
	err = registerAddressPrefix(ctx, db, "invoice", "IVC", nil)
	wantTypedG(t, err, problemAddressPrefixFixed, map[string]string{"type": "invoice", "prefix": "INV"},
		`validation failed: prefix: invoice's address prefix is "INV" and is fixed: the type already has addresses`)
	wantTypedGField(t, err, "prefix")
}

func TestPhaseThreeG_AddressPrefixUnavailable(t *testing.T) {
	_, err := proposeAddressPrefix(context.Background(), addressDB(t), "q1")
	wantTypedG(t, err, problemAddressPrefixUnavailable, map[string]string{"type": "q1"},
		`validation failed: prefix: no address prefix can be proposed for "q1" (it has fewer than 2 letters): give one of 2 to 4 capital letters`)
	wantTypedGField(t, err, "prefix")
}

func TestPhaseThreeG_AddressPrefixWebContent(t *testing.T) {
	s := &ContentTypeSchema{TypeName: "article", URLPrefix: "/articles"}
	_, _, err := settleDynamicPrefix(context.Background(), addressDB(t), s, "ART", 0)
	wantTypedG(t, err, problemAddressPrefixWebContent, nil,
		"validation failed: prefix: a type with a url_prefix is web content and has no address prefix")
	wantTypedGField(t, err, "prefix")
}

func TestPhaseThreeG_MigrateDuplicatePublishedSlugs(t *testing.T) {
	db := newSQLiteDB(t)
	if _, err := db.Exec(`CREATE TABLE posts (id TEXT PRIMARY KEY, slug TEXT NOT NULL, status TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for _, r := range [][3]string{{"1", "a", "published"}, {"2", "a", "published"}, {"3", "b", "published"}, {"4", "b", "published"}, {"5", "c", "draft"}, {"6", "c", "draft"}} {
		if _, err := db.Exec(`INSERT INTO posts VALUES (?, ?, ?)`, r[0], r[1], r[2]); err != nil {
			t.Fatal(err)
		}
	}
	err := EnsurePublishedSlugIndex(context.Background(), db, "posts")
	wantTypedG(t, err, problemMigrateDuplicatePublishedSlugs, map[string]string{"table": "posts", "slugs": "a, b"},
		"Conflict: EnsurePublishedSlugIndex: posts publishes these slugs more than once: a, b; give all but one of each a new slug")
	if !errors.Is(err, ErrConflict) {
		t.Errorf("err = %v; want ErrConflict", err)
	}
}

func TestPhaseThreeG_ReferenceAddonNotRun(t *testing.T) {
	app, db, _ := governanceOnlyApp(t)
	const msg = "validation failed: task_ref: names a Task, and Tasks belong to the Orchestration add-on, which this instance does not run; leave it empty"

	// The module path (prepareReferences).
	_, err := findModule[*Amendment](t, app).MCPCreate(authorCtx(), map[string]any{"summary": "s", "amendment_type": "fix", "task_ref": "T1"})
	wantTypedG(t, err, problemReferenceAddonNotRun, map[string]string{"type": "Task"}, msg)
	wantTypedGField(t, err, "task_ref")

	// The raw insert path (prepareSignalReferences).
	err = InsertSignal(context.Background(), db, &Signal{Sender: "core", Receiver: "architect", SignalType: "notice", Message: "m", TaskRef: "T1"})
	wantTypedG(t, err, problemReferenceAddonNotRun, map[string]string{"type": "Task"}, msg)
	wantTypedGField(t, err, "task_ref")
}

func TestPhaseThreeG_ReferenceSubjectTypeUnknown(t *testing.T) {
	f := newRefFixture(t)
	_, err := f.signals.MCPCreate(f.global, map[string]any{"sender": "core", "receiver": "architect", "signal_type": "question", "subject_type": "Nope", "subject_id": "x"})
	wantTypedG(t, err, problemReferenceSubjectTypeUnknown, nil,
		"validation failed: subject_type: names no registered content type")
	wantTypedGField(t, err, "subject_type")
}

func TestPhaseThreeG_ReferencesSignalInTx(t *testing.T) {
	f := newRefFixture(t)
	task := f.task(t, map[string]any{"slug": "in-tx"})
	tx, err := f.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	err = InsertSignal(context.Background(), tx, &Signal{Sender: "core", Receiver: "architect", SignalType: "question", TaskRef: task.ID})
	wantTypedG(t, err, problemReferencesSignalInTx, nil,
		"Bad request: InsertSignal cannot check a Signal's subject or task_ref inside a caller's transaction; insert it on the base handle")
	if !errors.Is(err, ErrBadRequest) {
		t.Errorf("err = %v; want ErrBadRequest", err)
	}
}

func TestPhaseThreeG_WebhookURLNotAllowed(t *testing.T) {
	store := NewWebhookStore(nil, []byte("test-key-32bytes-xxxxxxxxxxxx!!!"))
	cases := []struct{ name, url, msg string }{
		{"http", "http://8.8.8.8/deliver", "validation failed: url: must use HTTPS"},
		{"localhost", "https://localhost/hook", "validation failed: url: must not target localhost or .local hostnames"},
		{"dotlocal", "https://myserver.local/hook", "validation failed: url: must not target localhost or .local hostnames"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, err := store.Create(context.Background(), c.url, []string{"post.published"})
			wantTypedG(t, err, problemWebhookUrlNotAllowed, nil, c.msg)
			wantTypedGField(t, err, "url")
		})
	}
}

// An Area whose address column is empty (a record from before the address
// migration, D110) is named by its ID, so the always-sent area value is
// still sent (T1061 phase 3 review).
func TestPhaseThreeG_AddressColumnEmptyFallsBackToID(t *testing.T) {
	f, c := newApprovalCheck(t, both)
	other, err := f.areas.MCPCreate(TrustedRead(authorCtx()).(Context), map[string]any{"name": "Unaddressed"})
	if err != nil {
		t.Fatal(err)
	}
	area := asItem[*Area](other)
	if _, err := f.db.Exec(`UPDATE smeldr_areas SET address = '' WHERE id = $1`, area.ID); err != nil {
		t.Fatal(err)
	}
	err = f.app.AnswerChangeCheck(human("u-author"), c.Address, area.ID, true, "")
	if ProblemTypeOf(err) != problemChangeCheckAreaNotTouched {
		t.Fatalf("answer for an unaddressed Area: %v; want change_check.area_not_touched", err)
	}
	if got := ProblemValuesOf(err)["area"]; got != area.ID {
		t.Errorf("area value %q; want the Area's ID %q", got, area.ID)
	}
	if addressOrID("", "id-1") != "id-1" || addressOrID("ARE1", "id-1") != "ARE1" {
		t.Error("addressOrID: an address wins, an empty one falls back to the ID")
	}
}
