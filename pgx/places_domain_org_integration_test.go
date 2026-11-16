//go:build integration

package pgx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	smeldr "smeldr.dev/core"
)

// TestPG_DomainInOrganization (T1321): on PostgreSQL a Domain created over
// HTTP is written with its belongs_to_organization edge in the same
// transaction, and the boot repair places a Domain that never had one, once,
// leaving one whose edge was ended.
func TestPG_DomainInOrganization(t *testing.T) {
	db, _ := isolatedDB(t)
	ctx := context.Background()
	app, store := newPGApp(t, db)
	for _, create := range []func(smeldr.DB) error{smeldr.CreateGovernanceTables, smeldr.CreateRelationTables} {
		if err := create(db); err != nil {
			t.Fatal(err)
		}
	}
	smeldr.RegisterGovernanceTypes(app, db)
	rels, err := smeldr.NewRelationStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := smeldr.RegisterGovernanceRelationKinds(ctx, rels); err != nil {
		t.Fatal(err)
	}
	app.Relations(rels)
	h := app.Handler()
	org, err := app.EnsureOrganization(ctx, "Acme")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Grant(ctx, smeldr.RoleGrant{TokenID: "pg-admin", RoleName: "admin"}); err != nil {
		t.Fatal(err)
	}
	tok, err := smeldr.SignToken(smeldr.User{ID: "pg-admin", Roles: []smeldr.Role{smeldr.Admin}}, pgTestSecret, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/domains", strings.NewReader(`{"name":"Billing"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	edges := func(name string) (live, all int) {
		t.Helper()
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FILTER (WHERE r.invalid_at IS NULL), COUNT(r.id) FROM smeldr_domains d
			LEFT JOIN smeldr_relations r ON r.source_id = d.id AND r.relation_kind = 'belongs_to_organization' AND r.target_id = $2
			WHERE d.name = $1`, name, org.ID).Scan(&live, &all); err != nil {
			t.Fatal(err)
		}
		return live, all
	}
	if live, _ := edges("Billing"); live != 1 {
		t.Fatalf("HTTP create: %d live edges (%d %s), want 1", live, w.Code, w.Body.String())
	}

	now := time.Now().UTC()
	for _, d := range []string{"never", "ended"} {
		if _, err := db.ExecContext(ctx, `INSERT INTO smeldr_domains (id, slug, status, created_at, updated_at, name) VALUES ($1, $1, 'active', $2, $2, $1)`, "dom-"+d, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO smeldr_relations (id, source_type, source_id, target_type, target_id, relation_kind, edge_class, attributes, created_at, updated_at, valid_at, invalid_at)
		VALUES ('e-ended', 'Domain', 'dom-ended', 'Organization', $1, 'belongs_to_organization', 'asserted', '{}', $2, $2, $2, $2)`, org.ID, now); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := app.EnsureOrganization(ctx, "Acme"); err != nil {
			t.Fatal(err)
		}
	}
	if live, all := edges("dom-never"); live != 1 || all != 1 {
		t.Errorf("never placed: %d live, %d rows; want 1, 1", live, all)
	}
	if live, all := edges("dom-ended"); live != 0 || all != 1 {
		t.Errorf("ended on purpose: %d live, %d rows; want 0, 1", live, all)
	}
}
