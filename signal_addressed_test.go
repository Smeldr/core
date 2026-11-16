package smeldr

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

// T1308: App.SignalsAddressedTo lists the Signals whose receiver is one of
// the caller's routing names, under its read scope.

// saveAddressedSignal saves a Signal with receiver and status, created at the given
// minute so the order is known.
func saveAddressedSignal(t *testing.T, db DB, id, receiver, signalType, status string, minute int) {
	t.Helper()
	s := &Signal{Node: Node{ID: id, Slug: id, Status: Status(status), CreatedAt: time.Date(2026, 11, 16, 10, minute, 0, 0, time.UTC)},
		Sender: "architect", Receiver: receiver, SignalType: signalType, Message: id}
	if err := NewSQLRepo[*Signal](db, Table("smeldr_signals")).Save(context.Background(), s); err != nil {
		t.Fatal(err)
	}
}

// addressedIDs returns the IDs SignalsAddressedTo lists for ctx, and the total.
func addressedIDs(t *testing.T, app *App, ctx Context, state string, limit, offset int) ([]string, int) {
	t.Helper()
	got, total, err := app.SignalsAddressedTo(ctx, state, limit, offset)
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, s := range got {
		ids = append(ids, s.ID)
	}
	return ids, total
}

func TestSignalsAddressedTo_Governed(t *testing.T) {
	f := newApprovalReadFixture(t)
	ctx := context.Background()
	if err := CreateSubscriptionTables(f.db); err != nil {
		t.Fatal(err)
	}
	for _, g := range []RoleGrant{
		{TokenID: "dom-appr", RoleName: RoleDomainApprover, ScopeAnchorID: "dom-1"},
		{TokenID: "other-appr", RoleName: RoleDomainApprover, ScopeAnchorID: "dom-2"},
		{TokenID: "reader", RoleName: "editor"},
	} {
		if _, err := f.rs.Grant(ctx, g); err != nil {
			t.Fatal(err)
		}
	}
	// A ratification condition in each Domain: d-a is in are-1 (dom-1), d-x in
	// are-2 (dom-2).
	f.app.emitRatificationRequired(ctx, "d-a")
	f.app.emitRatificationRequired(ctx, "d-x")
	inDom1, inDom2 := f.ratificationOf(t, "d-a"), f.ratificationOf(t, "d-x")

	// A holder of ratify over one Domain sees that Domain's condition, and not
	// the other's: the read scope applies to what is addressed to an operation.
	for token, want := range map[string]string{"dom-appr": inDom1, "other-appr": inDom2} {
		ids, total := addressedIDs(t, f.app, NewTestContext(User{ID: token}), "", 0, 0)
		if !slices.Equal(ids, []string{want}) || total != 1 {
			t.Errorf("%s: %v (total %d), want [%s]", token, ids, total, want)
		}
	}

	// reader reads everything and holds no ratify; a subscription of its own
	// names the band core.
	reader := NewTestContext(User{ID: "reader"})
	if _, err := f.app.CreateSubscription(reader, SubscriptionSpec{Name: "band",
		Filter: SubscriptionFilter{Fields: map[string][]string{"receiver": {"core"}}}}); err != nil {
		t.Fatal(err)
	}
	saveAddressedSignal(t, f.db, "to-me", "reader", "question", "open", 1)
	saveAddressedSignal(t, f.db, "to-band", "core", "review-requested", "open", 2)
	saveAddressedSignal(t, f.db, "to-band-history", "core", "notice", "pending", 3)
	saveAddressedSignal(t, f.db, "to-other-band", "site", "question", "open", 4)
	saveAddressedSignal(t, f.db, "answered", "reader", "question", "answered", 5)
	ids, total := addressedIDs(t, f.app, reader, "", 0, 0)
	if want := []string{"to-band-history", "to-band", "to-me"}; !slices.Equal(ids, want) || total != 3 {
		t.Errorf("reader: %v (total %d), want %v", ids, total, want)
	}
	// Paging cuts after the read scope: the total stays the same.
	if ids, total := addressedIDs(t, f.app, reader, "", 2, 0); !slices.Equal(ids, []string{"to-band-history", "to-band"}) || total != 3 {
		t.Errorf("first page: %v (total %d)", ids, total)
	}
	if ids, total := addressedIDs(t, f.app, reader, "", 2, 2); !slices.Equal(ids, []string{"to-me"}) || total != 3 {
		t.Errorf("second page: %v (total %d)", ids, total)
	}
	if ids, _ := addressedIDs(t, f.app, reader, "", 2, -5); !slices.Equal(ids, []string{"to-band-history", "to-band"}) {
		t.Errorf("negative offset: %v", ids)
	}
	if ids, total := addressedIDs(t, f.app, reader, "answered", 1000, 0); !slices.Equal(ids, []string{"answered"}) || total != 1 {
		t.Errorf("state answered: %v (total %d)", ids, total)
	}

	// A caller with no identity has nothing addressed to it.
	if _, _, err := f.app.SignalsAddressedTo(NewTestContext(User{}), "", 0, 0); !errors.Is(err, ErrForbidden) {
		t.Errorf("no identity: %v", err)
	}
}

// Without governance the names are the caller's ID and its subscriptions'
// receivers; an operation word is not one of them.
func TestSignalsAddressedTo_NoGovernance(t *testing.T) {
	app, db := signalTestApp(t)
	saveAddressedSignal(t, db, "to-me", "u1", "question", "open", 1)
	saveAddressedSignal(t, db, "to-ratify", decisionRatifyOperation, "ratification-required", "open", 2)
	u1 := NewTestContext(User{ID: "u1", Roles: []Role{Author}})
	// No subscriptions table: the ID still holds.
	if ids, total := addressedIDs(t, app, u1, "", 0, 0); !slices.Equal(ids, []string{"to-me"}) || total != 1 {
		t.Errorf("no subscriptions table: %v (total %d)", ids, total)
	}
	if err := CreateSubscriptionTables(db); err != nil {
		t.Fatal(err)
	}
	if _, err := app.CreateSubscription(u1, SubscriptionSpec{Name: "band",
		Filter: SubscriptionFilter{Fields: map[string][]string{"receiver": {"core"}}}}); err != nil {
		t.Fatal(err)
	}
	saveAddressedSignal(t, db, "to-band", "core", "question", "open", 3)
	if ids, total := addressedIDs(t, app, u1, "", 0, 0); !slices.Equal(ids, []string{"to-band", "to-me"}) || total != 2 {
		t.Errorf("with a subscription: %v (total %d)", ids, total)
	}
}

// No database, or no Signals table, is an empty list; a store failure is an
// internal error.
func TestSignalsAddressedTo_Store(t *testing.T) {
	u1 := NewTestContext(User{ID: "u1"})
	off := &App{}
	if got, total, err := off.SignalsAddressedTo(u1, "", 0, 0); err != nil || len(got) != 0 || total != 0 {
		t.Errorf("no DB: %v, %d, %v", got, total, err)
	}
	bare := New(MustConfig(Config{BaseURL: "https://example.com", Secret: []byte(transitionItemTestSecret), DB: newSQLiteDB(t)}))
	if got, total, err := bare.SignalsAddressedTo(u1, "", 0, 0); err != nil || len(got) != 0 || total != 0 {
		t.Errorf("no Signals table: %v, %d, %v", got, total, err)
	}

	app, db := signalTestApp(t)
	if err := CreateSubscriptionTables(db); err != nil {
		t.Fatal(err)
	}
	saveAddressedSignal(t, db, "to-me", "u1", "question", "open", 1)
	for _, match := range []string{"FROM smeldr_subscriptions WHERE owner", "SELECT id, subject_type, subject_id FROM smeldr_signals", "SELECT * FROM smeldr_signals WHERE"} {
		app.cfg.DB = failAddressDB{DB: db, match: match}
		if _, _, err := app.SignalsAddressedTo(u1, "", 0, 0); !errors.Is(err, ErrInternal) {
			t.Errorf("%s: %v, want ErrInternal", match, err)
		}
	}
}

// The taken-URL condition names the item by its address where its type has
// one, and by its ID otherwise.
func TestSlugCondition_NamesTheAddress(t *testing.T) {
	app, db := signalTestApp(t)
	ctx := context.Background()
	if _, err := db.Exec(`INSERT INTO smeldr_decisions (id, slug, status, created_at, updated_at, address) VALUES ('d-1', 'd-1', 'scheduled', datetime('now'), datetime('now'), 'D9')`); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ typeName, id, want string }{
		{"Decision", "d-1", `Decision D9 is scheduled, but its URL "about" is taken`},
		{"Post", "p-1", `Post p-1 is scheduled, but its URL "about" is taken`},
	} {
		if !app.signalSlugBlocked(ctx, c.typeName, c.id, "about") {
			t.Fatalf("%s: not recorded", c.typeName)
		}
		var message string
		if err := db.QueryRow(`SELECT message FROM smeldr_signals WHERE signal_type = 'conflict-detected' AND subject_id = $1`, c.id).Scan(&message); err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(message, c.want) {
			t.Errorf("message = %q, want it to start %q", message, c.want)
		}
	}
}
