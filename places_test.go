// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// placesApp is an instance with core governance, relations and provenance.
func placesApp(t *testing.T) (*App, *sql.DB, *RelationStore) {
	t.Helper()
	db := newSQLiteDB(t)
	for _, f := range []func(DB) error{CreateGovernanceTables, CreateRelationTables, CreateProvenanceTable} {
		if err := f(db); err != nil {
			t.Fatal(err)
		}
	}
	app := New(MustConfig(Config{BaseURL: "https://example.com", Secret: []byte(transitionItemTestSecret), DB: db}))
	RegisterGovernanceTypes(app, db)
	rs, err := NewRelationStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := RegisterGovernanceRelationKinds(context.Background(), rs); err != nil {
		t.Fatal(err)
	}
	app.Relations(rs)
	app.Provenance(NewProvenanceStore(db))
	return app, db, rs
}

// The four place types are core governance types with their prefixes and
// flows; an instance can hold one Organization only.
func TestPlaces_Registration(t *testing.T) {
	app, db, _ := placesApp(t)
	for typeName, prefix := range map[string]string{"Organization": "ORG", "Domain": "DOM", "Area": "ARE", "Set": "SET"} {
		if app.typeRegistry.Lookup(typeName) == nil {
			t.Errorf("%s not registered", typeName)
		}
		var got string
		if err := db.QueryRow(`SELECT prefix FROM smeldr_address_counters WHERE type_name = $1`, typeName).Scan(&got); err != nil || got != prefix {
			t.Errorf("%s prefix = %q, %v; want %s", typeName, got, err, prefix)
		}
		var flows int
		if err := db.QueryRow(`SELECT COUNT(*) FROM smeldr_state_flows WHERE type_name = $1`, typeName).Scan(&flows); err != nil || flows != 1 {
			t.Errorf("%s flows = %d, %v; want 1", typeName, flows, err)
		}
	}
	now := time.Now().UTC()
	if _, err := db.Exec(`INSERT INTO smeldr_organizations (id, slug, created_at, updated_at) VALUES ('r1', 'r1', $1, $1)`, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO smeldr_organizations (id, slug, created_at, updated_at) VALUES ('r2', 'r2', $1, $1)`, now); err == nil {
		t.Error("a second Organization was stored")
	}
}

// EnsureOrganization creates Organization, the default Domain and the edge between them once,
// with provenance; later calls return the same Organization.
func TestEnsureOrganization(t *testing.T) {
	app, db, rs := placesApp(t)
	ctx := context.Background()
	root, err := app.EnsureOrganization(ctx, "Acme")
	if err != nil {
		t.Fatal(err)
	}
	if root.Name != "Acme" || root.Address != "ORG1" {
		t.Errorf("root = %+v", root)
	}
	var domainID, domainAddr, name string
	if err := db.QueryRow(`SELECT id, address, name FROM smeldr_domains WHERE slug = 'general'`).Scan(&domainID, &domainAddr, &name); err != nil || domainAddr != "DOM1" || name != "General" {
		t.Fatalf("default Domain = %s %s %s, %v", domainID, domainAddr, name, err)
	}
	edges, err := rs.GetLiveBySource(ctx, "Domain", domainID, "belongs_to_organization")
	if err != nil || len(edges) != 1 || edges[0].TargetID != root.ID {
		t.Errorf("belongs_to_organization = %+v, %v", edges, err)
	}
	var prov int
	_ = db.QueryRow(`SELECT COUNT(*) FROM smeldr_provenance WHERE actor_id = 'setup' AND verb = 'create'`).Scan(&prov)
	if prov != 2 {
		t.Errorf("provenance records = %d, want 2", prov)
	}
	again, err := app.EnsureOrganization(ctx, "Other")
	if err != nil || again.ID != root.ID || again.Name != "Acme" {
		t.Errorf("second call = %+v, %v; want the same Organization", again, err)
	}
	var domains int
	_ = db.QueryRow(`SELECT COUNT(*) FROM smeldr_domains`).Scan(&domains)
	if domains != 1 {
		t.Errorf("domains = %d, want 1", domains)
	}
}

func TestEnsureOrganization_Errors(t *testing.T) {
	ctx := context.Background()
	app, db, _ := placesApp(t)
	if _, err := app.EnsureOrganization(ctx, " "); !errors.As(err, new(*ValidationError)) {
		t.Errorf("empty name = %v", err)
	}
	if _, err := (&App{}).EnsureOrganization(ctx, "x"); !errors.Is(err, ErrInternal) {
		t.Errorf("no DB = %v", err)
	}
	_ = db
	// Each failure on a fresh instance (the failing wrapper runs without a
	// transaction, so a failure part-way would leave the earlier rows).
	for _, match := range []string{"FROM smeldr_organizations LIMIT 1", "INSERT INTO smeldr_organizations", "INSERT INTO smeldr_domains", "INSERT INTO smeldr_relations", "smeldr_address_counters WHERE prefix"} {
		fresh, freshDB, _ := placesApp(t)
		fresh.cfg.DB = failAddressDB{freshDB, match}
		if _, err := fresh.EnsureOrganization(ctx, "x"); err == nil {
			t.Errorf("%s failing: no error", match)
		}
	}
	// Without relation tables Organization is created without the edge.
	bare := newSQLiteDB(t)
	if err := CreateGovernanceTables(bare); err != nil {
		t.Fatal(err)
	}
	app2 := New(MustConfig(Config{BaseURL: "https://example.com", Secret: []byte(transitionItemTestSecret), DB: bare}))
	if root, err := app2.EnsureOrganization(ctx, "Bare"); err != nil || root.Name != "Bare" {
		t.Errorf("without relations = %+v, %v", root, err)
	}
}

// The places accept any record type as the source; the target must be the
// place type.
func TestPlaces_AnySourceType(t *testing.T) {
	app, db, rs := placesApp(t)
	ctx := context.Background()
	root, err := app.EnsureOrganization(ctx, "Acme")
	if err != nil {
		t.Fatal(err)
	}
	var domainID string
	_ = db.QueryRow(`SELECT id FROM smeldr_domains`).Scan(&domainID)
	for _, src := range []string{"Decision", "Amendment", "Signal", "Area", "anything"} {
		edge := RelationEdge{SourceType: src, SourceID: "x-" + src, TargetType: "Domain", TargetID: domainID, RelationKind: "belongs_to_domain", EdgeClass: "asserted"}
		if err := validateTypePairs(mustKind(t, rs, "belongs_to_domain"), edge); err != nil {
			t.Errorf("%s -> Domain refused: %v", src, err)
		}
	}
	if err := validateTypePairs(mustKind(t, rs, "belongs_to_domain"), RelationEdge{SourceType: "Decision", TargetType: "Organization", TargetID: root.ID, RelationKind: "belongs_to_domain"}); err == nil {
		t.Error("belongs_to_domain to a Organization passed")
	}
	if err := validateTypePairs(mustKind(t, rs, "belongs_to_organization"), RelationEdge{SourceType: "Decision", TargetType: "Organization", RelationKind: "belongs_to_organization"}); err != nil {
		t.Errorf("Decision -> Organization refused: %v", err)
	}
}

func mustKind(t *testing.T, rs *RelationStore, name string) RelationKindDef {
	t.Helper()
	k, ok := rs.GetKind(name)
	if !ok {
		t.Fatalf("kind %s missing", name)
	}
	return k
}

// The place readers read through the hierarchy: a domain-reader its Domain,
// the Areas in it and what is placed in either; an area-reader its Area and
// what is placed in it. The per-item check agrees, and approve does not reach
// the place record itself.
func TestPlaces_HierarchyReading(t *testing.T) {
	db := setupGovernanceDB(t)
	setupRelationsTable(t, db)
	ctx := context.Background()
	edge := func(srcType, src, tgtType, tgt, kind string) {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO smeldr_relations (id, source_type, source_id, target_type, target_id, relation_kind, edge_class, attributes, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, 'asserted', '{}', datetime('now'), datetime('now'))`,
			NewID(), srcType, src, tgtType, tgt, kind); err != nil {
			t.Fatal(err)
		}
	}
	edge("Domain", "domA", "Organization", "root", "belongs_to_organization")
	edge("Domain", "domB", "Organization", "root", "belongs_to_organization")
	edge("Area", "areX", "Domain", "domA", "belongs_to_domain")
	edge("Decision", "d1", "Domain", "domA", "belongs_to_domain")
	edge("Decision", "d2", "Area", "areX", "belongs_to_area")
	edge("Decision", "d3", "Domain", "domB", "belongs_to_domain")
	edge("Decision", "d4", "Domain", "domA", "contains") // another kind: not followed
	rs := NewRoleStore(db)
	for _, f := range []func(context.Context, *RoleStore) error{RegisterDomainReaderRole, RegisterAreaReaderRole, RegisterDomainApproverRole} {
		if err := f(ctx, rs); err != nil {
			t.Fatal(err)
		}
	}
	grant := func(tok, role, anchor string) {
		if _, err := rs.Grant(ctx, RoleGrant{TokenID: tok, RoleName: role, ScopeAnchorID: anchor}); err != nil {
			t.Fatal(err)
		}
	}
	grant("dr", "domain-reader", "domA")
	grant("ar", "area-reader", "areX")
	grant("da", "domain-approver", "domA")
	cases := []struct {
		tok      string
		readable map[string]bool
	}{
		{"dr", map[string]bool{"Domain:domA": true, "Area:areX": true, "Decision:d1": true, "Decision:d2": true, "Decision:d3": false, "Domain:domB": false, "Decision:d4": false}},
		{"ar", map[string]bool{"Area:areX": true, "Decision:d2": true, "Decision:d1": false, "Domain:domA": false}},
	}
	for _, c := range cases {
		s, err := LoadReadScope(ctx, rs, User{ID: c.tok})
		if err != nil {
			t.Fatal(err)
		}
		for item, want := range c.readable {
			typ, id, _ := strings.Cut(item, ":")
			if got := s.Readable(typ, id); got != want {
				t.Errorf("%s scope: %s readable = %v, want %v", c.tok, item, got, want)
			}
			if got, err := rs.Authorized(ctx, c.tok, "read", AuthTarget{TypeName: typ, ID: id}); err != nil || got != want {
				t.Errorf("%s Authorized read %s = %v, %v; want %v", c.tok, item, got, err, want)
			}
		}
	}
	// ratify follows the hierarchy too (a Decision in an Area of the Domain)
	// but never the place record itself.
	for target, want := range map[string]bool{"d1": true, "d2": true, "d3": false, "domA": false, "areX": true} {
		got, err := rs.Authorized(ctx, "da", "ratify", AuthTarget{TypeName: "Decision", ID: target})
		if err != nil || got != want {
			t.Errorf("ratify %s = %v, %v; want %v", target, got, err, want)
		}
	}
}

// legacyPlacesDB is an instance as process.smeldr.dev is before D142: a
// runtime domain type with two records (DOM1, DOM2), a Decision placed in one,
// a scoped-reader grant naming it, provenance and an event about it.
func legacyPlacesDB(t *testing.T) (*sql.DB, []string) {
	t.Helper()
	ctx := context.Background()
	db := newSQLiteDB(t)
	setupTokensTable(t, db)
	for _, f := range []func(DB) error{CreateBlockTables, CreateSchemaTable, CreateRelationTables, CreateProvenanceTable, CreateEventLogTable} {
		if err := f(db); err != nil {
			t.Fatal(err)
		}
	}
	if err := migrateGovernance(ctx, db); err != nil {
		t.Fatal(err)
	}
	pre := New(MustConfig(Config{BaseURL: "https://example.com", Secret: []byte(transitionItemTestSecret), DB: db}))
	pre.ServeDynamicContent()
	if _, err := pre.DefineContentType(ctx, &ContentTypeSchema{TypeName: "domain", Label: "Domain", Kind: "content",
		Fields: []byte(`[{"name":"name","type":"string","role":"title"}]`)}); err != nil {
		t.Fatal(err)
	}
	repo, err := pre.DynamicContentRepo("domain")
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, n := range []string{"core", "cloud"} {
		node, err := repo.CreateDraft(ctx, map[string]any{"name": n})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, node.ID)
	}
	if err := CreateGovernanceTables(db); err != nil {
		t.Fatalf("boot with the runtime domain type: %v", err)
	}
	now := time.Now().UTC()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO smeldr_relations (id, source_type, source_id, target_type, target_id, relation_kind, edge_class, attributes, created_at, updated_at)
		   VALUES ('e1', 'Decision', 'd1', 'domain', $1, 'belongs_to_domain', 'asserted', '{}', $2, $2)`, []any{ids[0], now}},
		{`INSERT INTO smeldr_provenance (id, timestamp, subject_type, subject_id, verb, from_state, to_state, actor_kind, actor_id, surface, reason)
		   VALUES ('p1', $1, 'domain', $2, 'create', '', '', 'human', 'u', '', '')`, []any{now, ids[0]}},
		{`INSERT INTO smeldr_events (seq, id, event, subject_type, subject_id, created_at, payload) VALUES (1, 'ev1', 'domain.created', 'domain', $1, $2, '{}')`, []any{ids[0], now}},
	} {
		if _, err := db.Exec(q.sql, q.args...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	rs := NewRoleStore(db)
	if err := RegisterScopedReaderRole(ctx, rs); err != nil {
		t.Fatal(err)
	}
	if _, err := rs.Grant(ctx, RoleGrant{TokenID: "reader", RoleName: "scoped-reader", ScopeStatic: []string{"domain:" + ids[0]}}); err != nil {
		t.Fatal(err)
	}
	return db, ids
}

// migratedApp boots the compiled place types on db, as the new binary does.
func migratedApp(t *testing.T, db *sql.DB) *App {
	t.Helper()
	app := New(MustConfig(Config{BaseURL: "https://example.com", Secret: []byte(transitionItemTestSecret), DB: db}))
	app.ServeDynamicContent()
	RegisterGovernanceTypes(app, db)
	rs, err := NewRelationStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := RegisterGovernanceRelationKinds(context.Background(), rs); err != nil {
		t.Fatal(err)
	}
	app.Relations(rs)
	app.Provenance(NewProvenanceStore(db))
	_ = app.Handler()
	return app
}

// MigratePlaces moves the runtime Domains with their addresses, edges,
// provenance, events, grants and counter; it retires the runtime schema,
// creates Organization and relates every Domain to it. A dry run writes nothing; a
// second run moves nothing; the next Domain is DOM3.
func TestMigratePlaces(t *testing.T) {
	db, ids := legacyPlacesDB(t)
	app := migratedApp(t, db)
	ctx := context.Background()
	if _, err := app.EnsureOrganization(ctx, "Acme"); !errors.Is(err, ErrConflict) {
		t.Fatalf("EnsureOrganization before the migration = %v, want ErrConflict", err)
	}
	dry, err := app.MigratePlaces(ctx, PlaceMigrationOptions{DryRun: true, OrganizationName: "Acme"})
	if err != nil || !dry.DryRun || len(dry.Moved) != 2 || dry.Rewritten["smeldr_relations"] != 1 {
		t.Fatalf("dry run = %+v, %v", dry, err)
	}
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM smeldr_domains`).Scan(&n)
	if n != 0 {
		t.Fatalf("the dry run wrote %d Domains", n)
	}
	rep, err := app.MigratePlaces(ctx, PlaceMigrationOptions{OrganizationName: "Acme"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Moved) != 2 || rep.Moved[0].Address != "DOM1" || rep.Organization != "ORG1" || rep.OrganizationEdges != 2 ||
		rep.Rewritten["smeldr_provenance"] != 1 || rep.Rewritten["smeldr_events"] != 1 || rep.Rewritten["smeldr_role_grants"] != 1 ||
		rep.Rewritten["smeldr_address_counters"] != 1 || strings.Join(rep.RetiredSchemas, ",") != "domain" {
		t.Errorf("report = %+v", rep)
	}
	var name, addr string
	if err := db.QueryRow(`SELECT name, address FROM smeldr_domains WHERE id = $1`, ids[0]).Scan(&name, &addr); err != nil || name != "core" || addr != "DOM1" {
		t.Errorf("moved Domain = %s %s, %v", name, addr, err)
	}
	var tgtType string
	_ = db.QueryRow(`SELECT target_type FROM smeldr_relations WHERE id = 'e1'`).Scan(&tgtType)
	var static string
	_ = db.QueryRow(`SELECT scope_static FROM smeldr_role_grants`).Scan(&static)
	var kind string
	_ = db.QueryRow(`SELECT kind FROM smeldr_content_type_schemas WHERE type_name = 'domain'`).Scan(&kind)
	if tgtType != "Domain" || !strings.Contains(static, `"Domain:`+ids[0]) || kind != "retired" {
		t.Errorf("edge %q, grant %q, schema kind %q", tgtType, static, kind)
	}
	var roots, rootEdges int
	_ = db.QueryRow(`SELECT COUNT(*) FROM smeldr_organizations`).Scan(&roots)
	_ = db.QueryRow(`SELECT COUNT(*) FROM smeldr_relations WHERE relation_kind = 'belongs_to_organization'`).Scan(&rootEdges)
	if roots != 1 || rootEdges != 3 {
		t.Errorf("roots %d, belongs_to_organization edges %d; want 1 and 3 (two moved, one default)", roots, rootEdges)
	}
	again, err := app.MigratePlaces(ctx, PlaceMigrationOptions{OrganizationName: "Acme"})
	if err != nil || len(again.Moved) != 0 || again.OrganizationEdges != 0 || len(again.RetiredSchemas) != 0 || again.Rewritten["smeldr_role_grants"] != 0 {
		t.Errorf("second run = %+v, %v; want nothing", again, err)
	}
	// The next Domain continues the numbering.
	out, err := findModule[*Domain](t, app).MCPCreate(authorCtx(), map[string]any{"name": "brand"})
	if err != nil || asItem[*Domain](out).Address != "DOM4" {
		t.Errorf("next Domain = %+v, %v; want DOM4 (DOM3 is the default Domain)", out, err)
	}
	b, _ := json.Marshal(rep)
	if !strings.Contains(string(b), `"moved"`) {
		t.Errorf("report JSON = %s", b)
	}
}

// A runtime record whose address a compiled row already holds is refused.
func TestMigratePlaces_Collision(t *testing.T) {
	db, _ := legacyPlacesDB(t)
	app := migratedApp(t, db)
	now := time.Now().UTC()
	if _, err := db.Exec(`INSERT INTO smeldr_domains (id, slug, created_at, updated_at, name, address, address_no) VALUES ('other', 'other', $1, $1, 'x', 'DOM1', 1)`, now); err != nil {
		t.Fatal(err)
	}
	if _, err := app.MigratePlaces(context.Background(), PlaceMigrationOptions{OrganizationName: "Acme"}); !errors.As(err, new(*ValidationError)) {
		t.Errorf("collision = %v, want a validation error", err)
	}
}

// Every store failure is returned and leaves nothing half-done.
func TestMigratePlaces_Errors(t *testing.T) {
	ctx := context.Background()
	if _, err := (&App{}).MigratePlaces(ctx, PlaceMigrationOptions{}); !errors.Is(err, ErrInternal) {
		t.Errorf("no DB = %v", err)
	}
	for _, match := range []string{
		"FROM smeldr_dynamic_content WHERE type_name",
		"FROM smeldr_domains WHERE id",
		"INSERT INTO smeldr_domains",
		"UPDATE smeldr_relations SET target_type",
		"UPDATE smeldr_provenance",
		"UPDATE smeldr_events SET subject_type",
		"UPDATE smeldr_role_grants",
		"SELECT last_no FROM smeldr_address_counters",
		"UPDATE smeldr_address_counters SET type_name",
		"UPDATE smeldr_content_type_schemas",
		"SELECT d.id FROM smeldr_domains",
	} {
		db, _ := legacyPlacesDB(t)
		app := migratedApp(t, db)
		app.cfg.DB = failAddressDB{db, match}
		if _, err := app.MigratePlaces(ctx, PlaceMigrationOptions{OrganizationName: "Acme"}); err == nil {
			t.Errorf("%s failing: no error", match)
		}
	}
	// Without a name for the Organization it must create, the migration fails after
	// moving (the move stands; a rerun with a name finishes it).
	db, _ := legacyPlacesDB(t)
	app := migratedApp(t, db)
	if _, err := app.MigratePlaces(ctx, PlaceMigrationOptions{}); err == nil {
		t.Error("no Organization name passed")
	}
	if rep, err := app.MigratePlaces(ctx, PlaceMigrationOptions{OrganizationName: "Acme"}); err != nil || rep.Organization != "ORG1" {
		t.Errorf("rerun with a name = %+v, %v", rep, err)
	}
}

// The copy takes the name from name, then title, then the slug; an archived
// record stays archived. A failing place walk and an unknown place type are
// handled.
func TestPlaces_Helpers(t *testing.T) {
	_, db, _ := placesApp(t)
	ctx := context.Background()
	now := time.Now().UTC()
	for i, c := range []struct {
		fields, status, wantName, wantStatus string
	}{
		{`{"name":"n"}`, "published", "n", "active"},
		{`{"title":"t"}`, "draft", "t", "active"},
		{`{}`, "archived", "slug-2", "archived"},
	} {
		r := legacyPlaceRow{id: NewID(), slug: "slug-" + string(rune('0'+i)), status: c.status, fields: c.fields, createdAt: now, updatedAt: now}
		if err := copyLegacyPlace(ctx, db, "Area", r); err != nil {
			t.Fatal(err)
		}
		var name, status string
		_ = db.QueryRow(`SELECT name, status FROM smeldr_areas WHERE id = $1`, r.id).Scan(&name, &status)
		if name != c.wantName || status != c.wantStatus {
			t.Errorf("case %d: %s %s; want %s %s", i, name, status, c.wantName, c.wantStatus)
		}
	}
	if err := copyLegacyPlace(ctx, db, "Area", legacyPlaceRow{id: NewID(), slug: "slug-0", createdAt: now, updatedAt: now}); err == nil {
		t.Error("a duplicate slug was copied")
	}
	if placeTable("Nope") != "" {
		t.Error("an unknown type has a place table")
	}
	if _, err := placeMembers(ctx, failAddressDB{db, "FROM smeldr_relations"}, "Domain", "x"); err == nil {
		t.Error("a failing walk passed")
	}
	app, db2, _ := placesApp(t)
	if _, err := app.relateDomainsToOrganization(ctx, failAddressDB{db2, "SELECT d.id FROM smeldr_domains"}, "r"); err == nil {
		t.Error("a failing Domain read passed")
	}
	if _, err := db2.Exec(`INSERT INTO smeldr_domains (id, slug, created_at, updated_at, name) VALUES ('d1', 'd1', $1, $1, 'd')`, now); err != nil {
		t.Fatal(err)
	}
	if _, err := app.relateDomainsToOrganization(ctx, failAddressDB{db2, "INSERT INTO smeldr_relations"}, "r"); err == nil {
		t.Error("a failing edge write passed")
	}
	if err := insertPlace(ctx, db2, "Nope", "smeldr_domains", &Node{}, &RecordAddress{}, "x"); err == nil {
		t.Error("an address for an unknown type was allocated")
	}
}

// placeDecision gives the Decision id a place, a belongs_to_organization edge to a
// test Organization, so a test can ratify it (T883). It creates the relation tables
// when the test has none.
func placeDecision(t *testing.T, db DB, id string) {
	t.Helper()
	if err := CreateRelationTables(db); err != nil {
		t.Fatalf("placeDecision: relation tables: %v", err)
	}
	now := time.Now().UTC()
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO smeldr_relations (id, source_type, source_id, target_type, target_id, relation_kind, edge_class, attributes, created_at, updated_at)
		 VALUES ($1, 'Decision', $2, 'Organization', 'root-test', 'belongs_to_organization', 'asserted', '{}', $3, $3)`,
		NewID(), id, now); err != nil {
		t.Fatalf("placeDecision %s: %v", id, err)
	}
}

// TypeName names a module's type apart from its Go struct (T895): the
// default prefix and the MCP type name follow it, At still wins, and a name
// that is not a PascalCase identifier panics at startup.
func TestTypeNameOption(t *testing.T) {
	m := NewModule((*testPost)(nil), Repo(NewMemoryRepo[*testPost]()), TypeName("Article"))
	if m.contentTypeName != "Article" || m.prefix != "/articles" || m.MCPMeta().TypeName != "Article" {
		t.Errorf("type %q, prefix %q, MCP %q", m.contentTypeName, m.prefix, m.MCPMeta().TypeName)
	}
	m = NewModule((*testPost)(nil), Repo(NewMemoryRepo[*testPost]()), At("/stories"), TypeName("Article"))
	if m.prefix != "/stories" {
		t.Errorf("At before TypeName: prefix %q, want /stories", m.prefix)
	}
	if m := NewModule((*testPost)(nil), Repo(NewMemoryRepo[*testPost]())); m.MCPMeta().TypeName != "testPost" {
		t.Errorf("without TypeName: %q", m.MCPMeta().TypeName)
	}
	for _, bad := range []string{"", "article", "Art icle", "Art-icle"} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("TypeName(%q) did not panic", bad)
				}
			}()
			NewModule((*testPost)(nil), Repo(NewMemoryRepo[*testPost]()), TypeName(bad))
		}()
	}
}

// The organization is registered as Organization (T895): prefix ORG, REST
// /organizations, relation kind belongs_to_organization from any source.
func TestPlaces_OrganizationNames(t *testing.T) {
	app, _, rs := placesApp(t)
	desc := app.TypeRegistry().Lookup("Organization")
	if desc == nil {
		t.Fatal("Organization not registered")
	}
	if app.TypeRegistry().Lookup("OrganizationRecord") != nil || app.TypeRegistry().Lookup("Root") != nil {
		t.Error("the Go struct name or the old name is registered")
	}
	if coreAddressPrefixes["Organization"] != "ORG" {
		t.Errorf("prefix = %q", coreAddressPrefixes["Organization"])
	}
	k := mustKind(t, rs, "belongs_to_organization")
	if !strings.Contains(string(k.TypePairs), `"source_type":"*","target_type":"Organization"`) {
		t.Errorf("type pairs = %s", k.TypePairs)
	}
	if prefix := findModule[*OrganizationRecord](t, app).MCPMeta().Prefix; prefix != "/organizations" {
		t.Errorf("REST prefix = %q", prefix)
	}
}
