// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"testing"
	"time"
)

// wantTypedD checks err's refusal type, its exact values, and its message
// (T1061 phase 2, group D).
func wantTypedD(t *testing.T, err error, typ ProblemType, values map[string]string, msg string) {
	t.Helper()
	if err == nil {
		t.Fatalf("err = nil, want %s", typ)
	}
	if got := ProblemTypeOf(err); got != typ {
		t.Errorf("ProblemTypeOf = %q, want %q (err %v)", got, typ, err)
	}
	if got := ProblemValuesOf(err); !reflect.DeepEqual(got, values) {
		t.Errorf("ProblemValuesOf = %#v, want %#v", got, values)
	}
	if err.Error() != msg {
		t.Errorf("Error() = %q, want %q", err.Error(), msg)
	}
}

// wantFieldD checks err is a ValidationError on field.
func wantFieldD(t *testing.T, err error, field string) {
	t.Helper()
	var ve *ValidationError
	if !errors.As(err, &ve) || len(ve.Fields()) != 1 || ve.Fields()[0].Field != field {
		t.Errorf("err = %v, want a ValidationError on %s", err, field)
	}
}

// wantIsD checks errors.Is(err, sentinel).
func wantIsD(t *testing.T, err, sentinel error) {
	t.Helper()
	if !errors.Is(err, sentinel) {
		t.Errorf("err = %v, want %v", err, sentinel)
	}
}

func TestPhaseTwoD_Subscription(t *testing.T) {
	app, db := subscriptionApp(t)
	u1, adm := asUser("u1", Author), asUser("adm", Admin)

	t.Run("not_owner", func(t *testing.T) {
		_, err := app.CreateSubscription(u1, SubscriptionSpec{Owner: "u2", Name: "x"})
		wantTypedD(t, err, problemSubscriptionNotOwner, nil,
			fmt.Errorf("%w: a subscription for another token needs administer", ErrForbidden).Error())
		wantIsD(t, err, ErrForbidden)
	})
	t.Run("webhook_needs_admin", func(t *testing.T) {
		_, err := app.CreateSubscription(u1, SubscriptionSpec{Name: "n", Delivery: "webhook:e1"})
		wantTypedD(t, err, problemSubscriptionWebhookNeedsAdmin, nil,
			fmt.Errorf("%w: a webhook delivery needs administer", ErrForbidden).Error())
		wantIsD(t, err, ErrForbidden)
	})
	t.Run("item_limit", func(t *testing.T) {
		_, err := app.CreateSubscription(u1, SubscriptionSpec{Name: "n", Filter: SubscriptionFilter{Items: make([]string, MaxSubscriptionItems+1)}})
		wantTypedD(t, err, problemSubscriptionItemLimit, map[string]string{"limit": strconv.Itoa(MaxSubscriptionItems)},
			fmt.Errorf("%w: a subscription names at most %d items", ErrBadRequest, MaxSubscriptionItems).Error())
		wantIsD(t, err, ErrBadRequest)
	})
	t.Run("defaults", func(t *testing.T) {
		list, err := app.ListSubscriptions(u1, "")
		if err != nil {
			t.Fatal(err)
		}
		var def string
		for _, l := range list {
			if l.DefaultKind != "" {
				def = l.ID
			}
		}
		_, err = app.UpdateSubscription(u1, def, SubscriptionUpdate{Filter: &SubscriptionFilter{}})
		wantTypedD(t, err, problemSubscriptionDefaultFixed, nil,
			fmt.Errorf("%w: a default subscription's filter is fixed; pause it instead", ErrConflict).Error())
		wantIsD(t, err, ErrConflict)
		err = app.DeleteSubscription(u1, def)
		wantTypedD(t, err, problemSubscriptionDefaultUndeletable, nil,
			fmt.Errorf("%w: a default subscription cannot be deleted; pause it instead", ErrConflict).Error())
		wantIsD(t, err, ErrConflict)
	})
	t.Run("seed", func(t *testing.T) {
		_, err := app.SeedRoleSubscriptionsReport(adm, "x", RoleDefaults{})
		wantTypedD(t, err, problemSubscriptionRoleDefaultsNeedBand, nil,
			fmt.Errorf("%w: role defaults need a band or Architect", ErrBadRequest).Error())
		wantIsD(t, err, ErrBadRequest)
		_, err = app.SeedRoleSubscriptionsReport(u1, "x", RoleDefaults{Band: "core"})
		wantTypedD(t, err, problemSubscriptionSeedNeedsAdmin, nil,
			fmt.Errorf("%w: seeding role subscriptions needs administer", ErrForbidden).Error())
		wantIsD(t, err, ErrForbidden)
	})
	t.Run("owner_limit", func(t *testing.T) {
		for i := range MaxSubscriptionsPerOwner {
			if _, err := db.Exec(`INSERT INTO smeldr_subscriptions (id, slug, created_at, updated_at, name, owner) VALUES (?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, 'n', 'full')`,
				fmt.Sprint("full", i), fmt.Sprint("full", i)); err != nil {
				t.Fatal(err)
			}
		}
		_, err := app.CreateSubscription(asUser("full", Author), SubscriptionSpec{Name: "one more"})
		wantTypedD(t, err, problemSubscriptionOwnerLimit, map[string]string{"limit": strconv.Itoa(MaxSubscriptionsPerOwner)},
			fmt.Errorf("%w: an owner holds at most %d subscriptions", ErrBadRequest, MaxSubscriptionsPerOwner).Error())
		wantIsD(t, err, ErrBadRequest)
	})
}

func TestPhaseTwoD_SubscriptionNoCaller(t *testing.T) {
	app, db, _ := setupTransitionItemApp(t)
	if err := CreateSubscriptionTables(db); err != nil {
		t.Fatal(err)
	}
	_, err := app.CreateSubscription(context.Background(), SubscriptionSpec{Owner: "x", Name: "n"})
	wantTypedD(t, err, problemSubscriptionNoCaller, nil, ErrForbidden.Error())
	wantIsD(t, err, ErrForbidden)
}

func TestPhaseTwoD_RelationAssert(t *testing.T) {
	store := setupMCPRelations(t)
	upsertTestKind(t, store, "tagged", "article", "tag")
	ctx := context.Background()

	t.Run("kind_unknown", func(t *testing.T) {
		_, err := store.MCPAssertRelation(ctx, "article", "a", "tag", "t", "nope", nil, nil, nil, nil)
		wantTypedD(t, err, problemRelationKindUnknown, nil, ErrNotFound.Error())
		wantIsD(t, err, ErrNotFound)
	})
	t.Run("kind_unknown_assert", func(t *testing.T) {
		err := store.Assert(ctx, RelationEdge{SourceType: "article", SourceID: "a", TargetType: "tag", TargetID: "t",
			RelationKind: "nope", EdgeClass: "asserted"})
		wantTypedD(t, err, problemRelationKindUnknownAssert, map[string]string{"kind": "nope"},
			`validation failed: relation_kind: unknown relation kind "nope"`)
		wantFieldD(t, err, "relation_kind")
	})
	t.Run("edge_class_not_asserted", func(t *testing.T) {
		err := store.Assert(ctx, RelationEdge{SourceType: "article", SourceID: "a", TargetType: "tag", TargetID: "t",
			RelationKind: "tagged", EdgeClass: "inferred"})
		wantTypedD(t, err, problemRelationEdgeClassNotAsserted, map[string]string{"allowed": "asserted"},
			"validation failed: edge_class: Assert only accepts edge_class=asserted")
		wantFieldD(t, err, "edge_class")
	})
	t.Run("pair_not_permitted", func(t *testing.T) {
		err := store.Assert(ctx, RelationEdge{SourceType: "post", SourceID: "p", TargetType: "tag", TargetID: "t",
			RelationKind: "tagged", EdgeClass: "asserted"})
		wantTypedD(t, err, problemRelationPairNotPermitted, map[string]string{"kind": "tagged", "source": "post", "target": "tag"},
			`validation failed: type_pairs: relation kind "tagged" does not permit post→tag`)
		wantFieldD(t, err, "type_pairs")
	})
}

func TestPhaseTwoD_RelationHistory(t *testing.T) {
	store, _ := newHistoryStore(t)
	ctx := histCtx("alice")

	t.Run("not_found", func(t *testing.T) {
		err := store.Withdraw(ctx, "no-such-edge", "r")
		wantTypedD(t, err, problemRelationNotFound, map[string]string{"relation": "no-such-edge"},
			fmt.Errorf("%w: relation no-such-edge", ErrNotFound).Error())
		wantIsD(t, err, ErrNotFound)
	})

	if err := store.Assert(ctx, tagEdge()); err != nil {
		t.Fatal(err)
	}
	live, _ := store.GetLiveBySource(ctx, "article", "art-1", "")
	id := live[0].ID
	if err := store.Withdraw(ctx, id, "done"); err != nil {
		t.Fatal(err)
	}
	rows, _ := store.GetBySource(ctx, "article", "art-1", "")
	if len(rows) != 1 || rows[0].InvalidAt == nil {
		t.Fatalf("rows = %+v, want one ended row", rows)
	}
	endedAt := rows[0].InvalidAt.UTC().Format(time.RFC3339)

	t.Run("already_withdrawn", func(t *testing.T) {
		err := store.Withdraw(ctx, id, "again")
		wantTypedD(t, err, problemRelationAlreadyWithdrawn, map[string]string{"relation": id, "ended_at": endedAt},
			fmt.Errorf("%w: relation %s already ended at %s", ErrConflict, id, endedAt).Error())
		wantIsD(t, err, ErrConflict)
	})
	t.Run("ended", func(t *testing.T) {
		e := tagEdge()
		e.ID = id
		err := store.Assert(ctx, e)
		wantTypedD(t, err, problemRelationEnded, map[string]string{"relation": id, "ended_at": endedAt},
			fmt.Errorf("%w: relation %s ended at %s; assert it again without an id to start a new row", ErrConflict, id, endedAt).Error())
		wantIsD(t, err, ErrConflict)
	})
}

func TestPhaseTwoD_Amends(t *testing.T) {
	t.Run("no_database", func(t *testing.T) {
		err := amendsBeforeSave(amCtx(), nil, nil, &Amendment{Amends: "D1", AmendmentType: "fix"})
		wantTypedD(t, err, problemAmendsNoDatabase, nil, "validation failed: amends: cannot be checked: no database is configured")
		wantFieldD(t, err, "amends")
	})
	t.Run("write_once", func(t *testing.T) {
		e := newAmendsEnv(t, true)
		e.decision(t, "dec-1", "D1")
		e.decision(t, "dec-2", "D2")
		a, err := e.create(t, map[string]any{"amendment_number": "A903", "amends": "D1"})
		if err != nil {
			t.Fatal(err)
		}
		_, err = e.m.MCPUpdate(amCtx(), a.Slug, map[string]any{"amends": "D2"})
		wantTypedD(t, err, problemReferenceWriteOnce, map[string]string{"record": "D1"},
			"validation failed: amends: is write-once: this Amendment already amends D1; the link is changed through the relation once withdraw_relation exists")
		wantFieldD(t, err, "amends")
	})
	t.Run("ident_ambiguous", func(t *testing.T) {
		e := newAmendsEnv(t, true)
		e.decision(t, "dec-a", "D5")
		e.decision(t, "dec-b", "5")
		_, err := e.create(t, map[string]any{"amendment_number": "A-amb", "amends": "D5"})
		wantTypedD(t, err, problemIdentAmbiguous, map[string]string{"value": "D5", "count": "2", "type": "Decision"},
			"validation failed: amends: D5 matches 2 Decisions: it must name one")
		wantFieldD(t, err, "amends")
	})
	t.Run("migrate_precondition", func(t *testing.T) {
		e := newAmendsEnv(t, false)
		_, err := e.app.BackfillAmendsEdges(context.Background(), nil, true)
		wantTypedD(t, err, problemMigratePrecondition, nil,
			fmt.Errorf("%w: BackfillAmendsEdges needs a database and the relation store (App.Relations)", ErrBadRequest).Error())
		wantIsD(t, err, ErrBadRequest)
	})
}
