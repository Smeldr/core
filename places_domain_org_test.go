package smeldr

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// T1321: a Domain is created in the Organization, and Domains made before
// that are placed at boot, so the approver reads them (D142, D191).

// orgEdges counts the belongs_to_organization rows of Domain id: live, and all.
func orgEdges(t *testing.T, db DB, id string) (live, all int) {
	t.Helper()
	if err := db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FILTER (WHERE invalid_at IS NULL), COUNT(*) FROM smeldr_relations
		 WHERE source_type = 'Domain' AND source_id = $1 AND relation_kind = 'belongs_to_organization'`, id).Scan(&live, &all); err != nil {
		t.Fatal(err)
	}
	return live, all
}

func TestDomainCreate_InOrganization(t *testing.T) {
	app, db, _ := placesApp(t)
	ctx := context.Background()
	mod := findModule[*Domain](t, app)
	h := app.Handler()

	// No Organization yet: the create succeeds and nothing places it.
	out, err := mod.MCPCreate(authorCtx(), map[string]any{"name": "Early"})
	if err != nil {
		t.Fatal(err)
	}
	early := asItem[*Domain](out)
	if live, _ := orgEdges(t, db, early.ID); live != 0 {
		t.Errorf("a Domain created with no Organization is placed: %d", live)
	}

	org, err := app.EnsureOrganization(ctx, "Acme")
	if err != nil {
		t.Fatal(err)
	}
	// MCP create.
	out, err = mod.MCPCreate(authorCtx(), map[string]any{"name": "Billing"})
	if err != nil {
		t.Fatal(err)
	}
	billing := asItem[*Domain](out)
	if live, _ := orgEdges(t, db, billing.ID); live != 1 {
		t.Errorf("MCP create: %d live edges to the Organization, want 1", live)
	}
	var target, actor string
	if err := db.QueryRow(`SELECT target_id, COALESCE(created_by_job, '') FROM smeldr_relations WHERE source_id = $1 AND relation_kind = 'belongs_to_organization'`, billing.ID).Scan(&target, &actor); err != nil || target != org.ID || actor != placesSetupActor {
		t.Errorf("edge target %q actor %q (%v), want %q by %q", target, actor, err, org.ID, placesSetupActor)
	}
	// An update writes nothing more.
	if _, err := mod.MCPUpdate(authorCtx(), billing.Address, map[string]any{"name": "Billing and invoices"}); err != nil {
		t.Fatal(err)
	}
	if live, all := orgEdges(t, db, billing.ID); live != 1 || all != 1 {
		t.Errorf("after an update: %d live, %d rows; want 1, 1", live, all)
	}
	// HTTP POST.
	tok, err := SignToken(User{ID: "u-editor", Roles: []Role{Author, Editor, Admin}}, transitionItemTestSecret, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/domains", strings.NewReader(`{"name":"Payroll"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var id string
	if err := db.QueryRow(`SELECT id FROM smeldr_domains WHERE name = 'Payroll'`).Scan(&id); err != nil {
		t.Fatalf("HTTP POST: %d %s (%v)", w.Code, w.Body.String(), err)
	}
	if live, _ := orgEdges(t, db, id); live != 1 {
		t.Errorf("HTTP create: %d live edges, want 1", live)
	}

	// Without relations wired, nothing is written and the create succeeds.
	rs := app.relationStore
	app.relationStore = nil
	out, err = mod.MCPCreate(authorCtx(), map[string]any{"name": "Loose"})
	app.relationStore = rs
	if err != nil {
		t.Fatal(err)
	}
	if live, _ := orgEdges(t, db, asItem[*Domain](out).ID); live != 0 {
		t.Errorf("without relations: placed")
	}

	// A store failure on the Organization read fails the create, and nothing is stored.
	mod.db = failAddressDB{DB: db, match: "SELECT id FROM smeldr_organizations"}
	if _, err := mod.MCPCreate(authorCtx(), map[string]any{"name": "Broken"}); err == nil {
		t.Error("a create whose Organization read fails succeeded")
	}
	mod.db = db
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM smeldr_domains WHERE name = 'Broken'`).Scan(&n); err != nil || n != 0 {
		t.Errorf("a failed create left %d rows (%v)", n, err)
	}
}

// At boot, a Domain never placed is placed, once; one whose edge was ended on
// purpose is left alone; one Info line names them.
func TestEnsureOrganization_PlacesDomainsNeverPlaced(t *testing.T) {
	app, db, _ := placesApp(t)
	ctx := context.Background()
	org, err := app.EnsureOrganization(ctx, "Acme")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, d := range [][2]string{{"dom-a", "DOM7"}, {"dom-b", "DOM8"}, {"dom-ended", "DOM9"}} {
		if _, err := db.Exec(`INSERT INTO smeldr_domains (id, slug, status, created_at, updated_at, name, address) VALUES ($1, $1, 'active', $2, $2, $1, $3)`, d[0], now, d[1]); err != nil {
			t.Fatal(err)
		}
	}
	edge, _, err := writeEdgeRow(ctx, db, domainOrganizationEdge("dom-ended", org.ID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE smeldr_relations SET invalid_at = $1 WHERE id = $2`, now, edge.ID); err != nil {
		t.Fatal(err)
	}

	var logged bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, nil)))
	defer slog.SetDefault(prev)
	for i := 0; i < 2; i++ {
		if _, err := app.EnsureOrganization(ctx, "Acme"); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"dom-a", "dom-b"} {
		if live, all := orgEdges(t, db, id); live != 1 || all != 1 {
			t.Errorf("%s: %d live, %d rows; want 1, 1", id, live, all)
		}
	}
	if live, all := orgEdges(t, db, "dom-ended"); live != 0 || all != 1 {
		t.Errorf("the ended Domain: %d live, %d rows; want 0, 1 (left alone)", live, all)
	}
	if got := strings.Count(logged.String(), "Domains placed in the Organization"); got != 1 {
		t.Errorf("Info lines = %d, want 1 (the second boot places nothing): %s", got, logged.String())
	}
	if !strings.Contains(logged.String(), "count=2") || !strings.Contains(logged.String(), "DOM7") || !strings.Contains(logged.String(), "DOM8") {
		t.Errorf("Info line = %s", logged.String())
	}

	// A failed repair is a warning, and the boot goes on.
	logged.Reset()
	if _, err := db.Exec(`INSERT INTO smeldr_domains (id, slug, status, created_at, updated_at, name) VALUES ('dom-c', 'dom-c', 'active', $1, $1, 'c')`, now); err != nil {
		t.Fatal(err)
	}
	app.cfg.DB = failAddressDB{DB: db, match: "INSERT INTO smeldr_relations"}
	if _, err := app.EnsureOrganization(ctx, "Acme"); err != nil {
		t.Fatalf("a failed repair stopped the boot: %v", err)
	}
	app.cfg.DB = db
	if !strings.Contains(logged.String(), "Domains not placed in the Organization") {
		t.Errorf("no warning: %s", logged.String())
	}
}

// The reproduction: a token whose only grant is approver reads a Domain made
// after setup, its Area and the Decision placed there, and lists Decisions; an
// unplaced Decision stays unread, as D191 is written.
func TestApprover_ReadsDomainMadeAfterSetup(t *testing.T) {
	f := newApprovalFixture(t)
	ctx := context.Background()
	if _, err := f.db.Exec(`DELETE FROM smeldr_relations; DELETE FROM smeldr_areas; DELETE FROM smeldr_domains`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.EnsureOrganization(ctx, "Acme"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.rs.Grant(ctx, RoleGrant{TokenID: "appr-only", RoleName: RoleApprover}); err != nil {
		t.Fatal(err)
	}
	dom, err := findModule[*Domain](t, f.app).MCPCreate(authorCtx(), map[string]any{"name": "Billing"})
	if err != nil {
		t.Fatal(err)
	}
	area, err := findModule[*Area](t, f.app).MCPCreate(authorCtx(), map[string]any{"name": "Invoices"})
	if err != nil {
		t.Fatal(err)
	}
	d, a := asItem[*Domain](dom), asItem[*Area](area)
	for _, e := range [][4]string{{"Area", a.ID, "Domain", d.ID}, {"Decision", "d-placed", "Area", a.ID}} {
		if _, _, err := writeEdgeRow(ctx, f.db, RelationEdge{SourceType: e[0], SourceID: e[1], TargetType: e[2], TargetID: e[3],
			RelationKind: placeKindOf[e[2]], EdgeClass: "asserted", Attributes: []byte("{}")}); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"d-placed", "d-unplaced"} {
		if _, err := f.db.Exec(`INSERT INTO smeldr_decisions (id, slug, status, created_at, updated_at) VALUES ($1, $1, 'proposed', $2, $2)`, id, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	scope, err := LoadReadScope(ctx, f.rs, User{ID: "appr-only"})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		typeName, id string
		want         bool
	}{{"Domain", d.ID, true}, {"Area", a.ID, true}, {"Decision", "d-placed", true}, {"Decision", "d-unplaced", false}} {
		if got := scope.Readable(c.typeName, c.id); got != c.want {
			t.Errorf("reads %s %s = %v, want %v", c.typeName, c.id, got, c.want)
		}
		ok, err := f.rs.Authorized(ctx, "appr-only", "read", AuthTarget{TypeName: c.typeName, ID: c.id})
		if err != nil || ok != c.want {
			t.Errorf("Authorized read %s %s = %v (%v), want %v", c.typeName, c.id, ok, err, c.want)
		}
	}
	// The list path: the Decision type passes its floor, and the list holds
	// the placed Decision only.
	if !scope.TypeReadable("Decision") {
		t.Error("the Decision type is not readable to the approver")
	}
	// The MCP server attaches the caller's read scope to each request.
	actx := WithReadScope(NewTestContext(User{ID: "appr-only", Roles: []Role{Author}}), scope).(Context)
	got, err := findModule[*Decision](t, f.app).MCPList(actx)
	if err != nil {
		t.Fatalf("list as the approver: %v", err)
	}
	var ids []string
	for _, v := range got {
		ids = append(ids, asItem[*Decision](v).ID)
	}
	if s := strings.Join(ids, ","); s != "d-placed" {
		t.Errorf("list as the approver = %q, want d-placed", s)
	}
}
