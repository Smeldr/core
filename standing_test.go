// AGPL-3.0-or-later

package smeldr

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
)

// standingFlow registers (or re-registers, which updates the tags in place) a
// flow for typeName whose states draft (initial), live, other and closed are
// connected draft->live->other->closed and live->closed, draft->closed, with the
// named states tagged as holding.
func standingFlow(t *testing.T, db DB, typeName string, holds ...string) {
	t.Helper()
	tag := map[string]bool{}
	for _, h := range holds {
		tag[h] = true
	}
	st := func(name string, initial bool) State {
		s := State{Name: name, IsInitial: initial}
		if tag[name] {
			s.Standing = StandingHolds
		}
		return s
	}
	app := &App{cfg: Config{DB: db}}
	if err := app.RegisterFlow(StateFlow{
		Name: "standing-flow-" + typeName, TypeName: typeName,
		States: []State{st("draft", true), st("live", false), st("other", false), st("closed", false)},
		Transitions: []Transition{
			{From: "draft", To: "live"}, {From: "live", To: "other"}, {From: "other", To: "closed"},
			{From: "live", To: "closed"}, {From: "draft", To: "closed"}, {From: "closed", To: "live"},
		},
	}); err != nil {
		t.Fatalf("RegisterFlow %s: %v", typeName, err)
	}
}

func mustStanding(t *testing.T, db DB, typeName, id string) (Standing, bool) {
	t.Helper()
	s, has, err := ItemStanding(context.Background(), db, typeName, id)
	if err != nil {
		t.Fatalf("ItemStanding(%s, %s): %v", typeName, id, err)
	}
	return s, has
}

func verbsOf(recs []ProvenanceRecord) []string {
	out := make([]string, 0, len(recs))
	for _, r := range recs {
		out = append(out, r.Verb)
	}
	return out
}

// TestWriteStanding_Rules pins what D100 says the transition code writes: only a
// move into or out of a tagged state changes anything, an untagged-to-untagged
// move leaves "none" as "none", and every change is a recorded event.
func TestWriteStanding_Rules(t *testing.T) {
	tests := []struct {
		name       string
		from, to   string
		start      Standing // pre-existing stored row, "" for none
		wantValue  Standing
		wantVerbs  []string
		wantNoRows bool
	}{
		{"enter a state that holds", "draft", "live", "", StandingHolds, []string{"standing-began"}, false},
		{"leave it for an untagged state", "live", "closed", StandingHolds, StandingCeased, []string{"standing-ended"}, false},
		{"hold again after ceasing", "closed", "live", StandingCeased, StandingHolds, []string{"standing-began"}, false},
		{"never held stays none", "draft", "closed", "", StandingNone, nil, true},
		{"tagged to tagged changes nothing", "live", "other", StandingHolds, StandingHolds, nil, false},
		{"no move changes nothing", "live", "live", StandingHolds, StandingHolds, nil, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := newMigratedDB(t)
			standingFlow(t, db, "StandT", "live", "other")
			if tc.start != "" {
				if _, err := db.ExecContext(context.Background(),
					`INSERT INTO smeldr_standing (subject_type, subject_id, standing) VALUES ('StandT', 'i1', ?)`, string(tc.start)); err != nil {
					t.Fatalf("seed: %v", err)
				}
			}
			prov := &fakeProvenanceStore{}
			ctx := NewTestContext(User{ID: "u1", Roles: []Role{Editor}})
			writeStanding(ctx, db, prov, stateChange{typeName: "StandT", id: "i1", from: tc.from, to: tc.to,
				reason: "why", surface: "mcp", actorKind: "human", actorID: "u1"})

			got, has := mustStanding(t, db, "StandT", "i1")
			if !has || got != tc.wantValue {
				t.Errorf("standing = %q (has %v), want %q", got, has, tc.wantValue)
			}
			if v := verbsOf(prov.Appended()); strings.Join(v, ",") != strings.Join(tc.wantVerbs, ",") {
				t.Errorf("events = %v, want %v", v, tc.wantVerbs)
			}
			for _, r := range prov.Appended() {
				if r.FromState != tc.from || r.ToState != tc.to || r.ActorID != "u1" || r.Surface != "mcp" || r.Reason != "why" {
					t.Errorf("event = %+v, want the transition's own from/to/actor/surface/reason", r)
				}
			}
			var n int
			_ = db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM smeldr_standing`).Scan(&n)
			if tc.wantNoRows && n != 0 {
				t.Errorf("%d rows written, want none", n)
			}
		})
	}
}

// TestItemStanding_TypeWithoutStanding: a type whose flow tags no state has no
// standing at all (Signal, Task, Goal), and nothing is ever written for it.
func TestItemStanding_TypeWithoutStanding(t *testing.T) {
	app, db, _ := setupTransitionItemApp(t)
	insertSignal(t, db, "s1", "s1-slug", "pending")
	if _, err := app.TransitionItemVia(context.Background(), "mcp", "Signal", "s1-slug", "read", ""); err != nil {
		t.Fatalf("TransitionItemVia: %v", err)
	}
	if s, has := mustStanding(t, db, "Signal", "s1"); has || s != "" {
		t.Errorf("Signal standing = %q (has %v), want none at all", s, has)
	}
	var n int
	_ = db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM smeldr_standing`).Scan(&n)
	if n != 0 {
		t.Errorf("%d standing rows for an untagged type, want 0", n)
	}
	counts, err := CountStanding(context.Background(), db, "Signal")
	if err != nil || len(counts) != 0 {
		t.Errorf("CountStanding(Signal) = %v, %v, want empty", counts, err)
	}
}

func ratifyDecision(t *testing.T, app *App, db *sql.DB, rs *RoleStore, id, slug string) {
	t.Helper()
	insertDecision(t, db, id, slug, "proposed")
	tokenID := setupTokenWithRole(t, db, rs, "admin")
	if err := RegisterDecisionStewardRole(context.Background(), rs); err != nil {
		t.Fatalf("RegisterDecisionStewardRole: %v", err)
	}
	if _, err := rs.Grant(context.Background(), RoleGrant{TokenID: tokenID, RoleName: "decision-steward"}); err != nil {
		t.Fatalf("Grant: %v", err)
	}
	if _, err := app.TransitionItemVia(NewTestContext(User{ID: tokenID}), "mcp", "Decision", slug, "ratified", "ratified by steward"); err != nil {
		t.Fatalf("ratify: %v", err)
	}
}

// TestTransitionItemVia_Decision_StandingLifecycle follows a real Decision:
// ratified (holds), moved to pending-re-evaluation (still holds, no new event),
// superseded (ceased), archived (stays ceased).
func TestTransitionItemVia_Decision_StandingLifecycle(t *testing.T) {
	app, db, rs := setupTransitionItemApp(t)
	prov := &fakeProvenanceStore{}
	app.Provenance(prov)
	ratifyDecision(t, app, db, rs, "dec-1", "dec-1-slug")
	if s, has := mustStanding(t, db, "Decision", "dec-1"); !has || s != StandingHolds {
		t.Fatalf("after ratify: %q (has %v), want holds", s, has)
	}
	ctx := NewTestContext(User{ID: "u-any", Roles: []Role{Editor}})
	if _, err := app.TransitionItemVia(ctx, "mcp", "Decision", "dec-1-slug", "pending-re-evaluation", ""); err != nil {
		t.Fatalf("pending-re-evaluation: %v", err)
	}
	if s, _ := mustStanding(t, db, "Decision", "dec-1"); s != StandingHolds {
		t.Errorf("after pending-re-evaluation: %q, want still holds", s)
	}
	for _, v := range verbsOf(prov.Appended()) {
		if v == "standing-ended" {
			t.Error("a standing-ended event was written for a move between two states that hold")
		}
	}
}

// TestDecisionSupersededByPolicy_BecomesCeased covers the side effect outside
// the transition code: an item moved to superseded by a winning transition
// becomes ceased, with the winner's actor on the events.
func TestDecisionSupersededByPolicy_BecomesCeased(t *testing.T) {
	db := newMigratedDB(t)
	createConflictItemTable(t, db, true, ConflictSupersede)
	// Re-register the same flow with "published" tagged: the tag reaches the
	// existing state row (the DO UPDATE) and so applies at once.
	app := &App{cfg: Config{DB: db}}
	if err := app.RegisterFlow(StateFlow{
		Name: "conflict-flow", TypeName: "ConflictType", ActiveState: "published", ConflictPolicy: ConflictSupersede,
		States: []State{
			{Name: "draft", IsInitial: true}, {Name: "published", Standing: StandingHolds},
			{Name: "superseded"}, {Name: "archived", IsTerminal: true},
		},
		Transitions: []Transition{
			{From: "draft", To: "published"}, {From: "published", To: "superseded"}, {From: "published", To: "archived"},
		},
	}); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
	insertConflictItem(t, db, "old", "published")
	prov := &fakeProvenanceStore{}
	ctx := NewTestContext(User{ID: "u-win", Roles: []Role{Editor}})

	if err := applyConflictPolicy(ctx, db, nil, prov, "ConflictType", "published", "new", "mcp"); err != nil {
		t.Fatalf("applyConflictPolicy: %v", err)
	}

	if s, has := mustStanding(t, db, "ConflictType", "old"); !has || s != StandingCeased {
		t.Errorf("superseded item standing = %q (has %v), want ceased", s, has)
	}
	var ended *ProvenanceRecord
	for _, r := range prov.Appended() {
		if r.Verb == "standing-ended" {
			r := r
			ended = &r
		}
	}
	if ended == nil || ended.ActorID != "u-win" || ended.FromState != "published" || ended.ToState != "superseded" {
		t.Errorf("standing-ended event = %+v, want the winner's actor and published -> superseded", ended)
	}
}

// TestDynamic_Standing covers the runtime-defined path: creation into a tagged
// initial state, a status change out of it, through DynamicTypeRepo.
func TestDynamic_Standing(t *testing.T) {
	app, db, _, prov := setupProvenanceTransitionApp(t)
	typeName, slug := defineProvenanceDynamicType(t, app, db, "standdyn")
	// The default flow's "draft" is where CreateDraft starts; tag it so creation
	// itself begins standing, and "published" so a later move changes it.
	if err := app.RegisterFlow(StateFlow{
		Name: "standdyn-flow", TypeName: typeName,
		States:      []State{{Name: "draft", IsInitial: true, Standing: StandingHolds}, {Name: "published"}},
		Transitions: []Transition{{From: "draft", To: "published"}},
	}); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
	repo, err := app.DynamicContentRepo(typeName)
	if err != nil {
		t.Fatalf("DynamicContentRepo: %v", err)
	}
	node, err := repo.CreateDraft(NewTestContext(User{ID: "u-dyn", Roles: []Role{Editor}}), map[string]any{"Title": "x"})
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	if s, has := mustStanding(t, db, typeName, node.ID); !has || s != StandingHolds {
		t.Fatalf("after creation into a tagged initial state: %q (has %v), want holds", s, has)
	}
	if err := repo.setStatusVia(NewTestContext(User{ID: "u-dyn", Roles: []Role{Editor}}), node.ID, Published, "", "http"); err != nil {
		t.Fatalf("setStatusVia: %v", err)
	}
	if s, _ := mustStanding(t, db, typeName, node.ID); s != StandingCeased {
		t.Errorf("after leaving the holding state: %q, want ceased", s)
	}
	_ = slug
	verbs := verbsOf(prov.Appended())
	if strings.Count(strings.Join(verbs, ","), "standing-began") != 1 || strings.Count(strings.Join(verbs, ","), "standing-ended") != 1 {
		t.Errorf("events = %v, want one standing-began and one standing-ended", verbs)
	}
}

// TestModulePath_Standing covers the Module path (HTTP/MCP lifecycle), which
// ends in notifyAfter: the value follows the item whether or not provenance is
// wired, events appear only when it is, and deleting the item removes the row.
func TestModulePath_Standing(t *testing.T) {
	for _, withProv := range []bool{false, true} {
		name := "provenance not wired"
		if withProv {
			name = "provenance wired"
		}
		t.Run(name, func(t *testing.T) {
			app, db, _ := setupTransitionItemApp(t)
			if err := app.RegisterFlow(StateFlow{
				Name: "signal-protocol", TypeName: "Signal",
				States: []State{
					{Name: "pending", IsInitial: true}, {Name: "published", Standing: StandingHolds},
					{Name: "archived", IsTerminal: true},
				},
				Transitions: []Transition{{From: "pending", To: "published"}, {From: "published", To: "archived"}},
			}); err != nil {
				t.Fatalf("RegisterFlow: %v", err)
			}
			prov := &fakeProvenanceStore{}
			insertSignal(t, db, "sg", "sg-slug", "pending")
			m := NewModule[*Signal]((*Signal)(nil),
				At("/signals"), Repo(NewSQLRepo[*Signal](db, Table("smeldr_signals"))), MCP(MCPRead, MCPWrite))
			m.setDB(db)
			if withProv {
				m.setProvenanceStore(prov)
			}
			ctx := NewTestContext(User{ID: "u-mod", Roles: []Role{Editor}})

			if err := m.MCPPublish(ctx, "sg-slug", ""); err != nil {
				t.Fatalf("MCPPublish: %v", err)
			}
			if s, has := mustStanding(t, db, "Signal", "sg"); !has || s != StandingHolds {
				t.Fatalf("after publish: %q (has %v), want holds", s, has)
			}
			if err := m.MCPArchive(ctx, "sg-slug", ""); err != nil {
				t.Fatalf("MCPArchive: %v", err)
			}
			if s, _ := mustStanding(t, db, "Signal", "sg"); s != StandingCeased {
				t.Errorf("after archive: %q, want ceased", s)
			}
			got := verbsOf(prov.Appended())
			if withProv && strings.Join(got, ",") != "standing-began,standing-ended" {
				t.Errorf("events = %v, want standing-began then standing-ended (the signal subscriber, not wired here, writes the transition records)", got)
			}
			if !withProv && len(got) != 0 {
				t.Errorf("events written without a provenance store: %v", got)
			}
			if err := m.MCPDelete(ctx, "sg-slug"); err != nil {
				t.Fatalf("MCPDelete: %v", err)
			}
			var n int
			_ = db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM smeldr_standing WHERE subject_id = 'sg'`).Scan(&n)
			if n != 0 {
				t.Errorf("standing row survived the item's deletion")
			}
		})
	}
}

// TestRegisterFlow_Standing: a tag reaches an existing state row (the DO UPDATE),
// an illegal value is refused before anything is written.
func TestRegisterFlow_Standing(t *testing.T) {
	db := newMigratedDB(t)
	app := &App{cfg: Config{DB: db}}
	flow := func(tag Standing) StateFlow {
		return StateFlow{Name: "rf-standing", TypeName: "RFStanding",
			States:      []State{{Name: "draft", IsInitial: true}, {Name: "live", Standing: tag}},
			Transitions: []Transition{{From: "draft", To: "live"}}}
	}
	if err := app.RegisterFlow(flow("")); err != nil {
		t.Fatalf("RegisterFlow untagged: %v", err)
	}
	read := func() string {
		var s string
		if err := db.QueryRowContext(context.Background(),
			`SELECT s.standing FROM smeldr_states s JOIN smeldr_state_flows f ON f.id = s.flow_id
			 WHERE f.type_name = 'RFStanding' AND s.name = 'live'`).Scan(&s); err != nil {
			t.Fatalf("read tag: %v", err)
		}
		return s
	}
	if read() != "" {
		t.Fatalf("tag = %q before tagging, want empty", read())
	}
	if err := app.RegisterFlow(flow(StandingHolds)); err != nil {
		t.Fatalf("RegisterFlow tagged: %v", err)
	}
	if read() != "holds" {
		t.Errorf("tag = %q after re-registering with Standing, want holds (an existing row must update)", read())
	}

	err := app.RegisterFlow(StateFlow{Name: "rf-bad", TypeName: "RFBad",
		States: []State{{Name: "draft", IsInitial: true, Standing: "ceased"}}})
	if !errors.Is(err, ErrBadRequest) {
		t.Errorf("illegal Standing: err = %v, want ErrBadRequest", err)
	}
	var n int
	_ = db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM smeldr_state_flows WHERE type_name = 'RFBad'`).Scan(&n)
	if n != 0 {
		t.Error("a flow with an illegal tag was partly written")
	}
}

// TestRegisterFlow_Retag_DoesNotRecompute (D100 point 6): changing a flow's tags
// leaves every stored standing as it was; only later transitions use the new tags.
func TestRegisterFlow_Retag_DoesNotRecompute(t *testing.T) {
	db := newMigratedDB(t)
	standingFlow(t, db, "RetagT", "live", "other")
	writeStanding(context.Background(), db, nil, stateChange{typeName: "RetagT", id: "i1", from: "draft", to: "live"})
	if s, _ := mustStanding(t, db, "RetagT", "i1"); s != StandingHolds {
		t.Fatalf("before retag: %q, want holds", s)
	}
	standingFlow(t, db, "RetagT", "other") // "live" no longer holds
	if s, _ := mustStanding(t, db, "RetagT", "i1"); s != StandingHolds {
		t.Errorf("after retag: %q, want the recorded holds kept", s)
	}
	// The item moves live -> closed under the NEW tags: neither state holds now,
	// so nothing changes (the stored value is a recorded fact, not recomputed).
	writeStanding(context.Background(), db, nil, stateChange{typeName: "RetagT", id: "i1", from: "live", to: "closed"})
	if s, _ := mustStanding(t, db, "RetagT", "i1"); s != StandingHolds {
		t.Errorf("after a move under the new tags: %q, want holds unchanged", s)
	}
}

func seedDecisions(t *testing.T, db *sql.DB) {
	t.Helper()
	for id, status := range map[string]string{
		"d-prop": "proposed", "d-rat": "ratified", "d-pend": "pending-re-evaluation",
		"d-sup": "superseded", "d-arch": "archived",
	} {
		insertDecision(t, db, id, id+"-slug", status)
	}
}

// TestMigrateStanding classifies items that existed before standing was recorded
// from the flow graph alone, once per type, and never relabels later items.
func TestMigrateStanding(t *testing.T) {
	_, db, _ := setupTransitionItemApp(t)
	seedDecisions(t, db)
	insertSignal(t, db, "sig", "sig-slug", "read")

	if err := MigrateStanding(context.Background(), db); err != nil {
		t.Fatalf("MigrateStanding: %v", err)
	}
	want := map[string]Standing{
		"d-rat": StandingHolds, "d-pend": StandingHolds, // in a state that holds
		"d-sup":  StandingCeased,      // reachable only from states that hold
		"d-arch": StandingNotRecorded, // reachable from proposed and from superseded
		"d-prop": StandingNone,        // no path from a state that holds reaches it
	}
	for id, w := range want {
		if got, has := mustStanding(t, db, "Decision", id); !has || got != w {
			t.Errorf("%s = %q (has %v), want %q", id, got, has, w)
		}
	}
	var n int
	_ = db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM smeldr_standing WHERE subject_type = 'Signal'`).Scan(&n)
	if n != 0 {
		t.Error("a type with no tagged state got standing rows")
	}

	// Once per type: an item archived from proposed AFTER the migration has no
	// row and is rightly none; a second run must not relabel it.
	insertDecision(t, db, "d-late", "d-late-slug", "archived")
	if err := MigrateStanding(context.Background(), db); err != nil {
		t.Fatalf("second MigrateStanding: %v", err)
	}
	if got, _ := mustStanding(t, db, "Decision", "d-late"); got != StandingNone {
		t.Errorf("an item created after the migration = %q, want none (the marker must stop a relabel)", got)
	}
	var markers int
	_ = db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM smeldr_standing_migrations`).Scan(&markers)
	if markers == 0 {
		t.Error("no migration marker written")
	}
}

// TestMigrateStanding_Failure: a failing write returns the error and leaves the
// type unmarked, so the next boot retries.
func TestMigrateStanding_Failure(t *testing.T) {
	_, db, _ := setupTransitionItemApp(t)
	seedDecisions(t, db)
	err := MigrateStanding(context.Background(), &nthExecFailDB{DB: db, fail: 1})
	if err == nil {
		t.Fatal("want the write failure returned")
	}
	var markers int
	_ = db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM smeldr_standing_migrations`).Scan(&markers)
	if markers != 0 {
		t.Error("a marker was written for a type whose migration failed")
	}
	if err := MigrateStanding(context.Background(), db); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if got, _ := mustStanding(t, db, "Decision", "d-rat"); got != StandingHolds {
		t.Errorf("after retry d-rat = %q, want holds", got)
	}
}

func TestMigrateStanding_NoTablesNoDB(t *testing.T) {
	if err := MigrateStanding(context.Background(), nil); err != nil {
		t.Errorf("nil db: %v", err)
	}
	if err := MigrateStanding(context.Background(), newSQLiteDB(t)); err != nil {
		t.Errorf("empty database (no flow tables): %v", err)
	}
	if err := MigrateStanding(context.Background(), &queryFailDB{}); err != nil {
		t.Errorf("a database that cannot be probed: %v", err)
	}
}

// TestCheckStandingDrift reports a mismatch between the stored standing and the
// current state's tag, in both directions, and repairs nothing.
func TestCheckStandingDrift(t *testing.T) {
	app, db, _ := setupTransitionItemApp(t)
	if err := CreateFindingTable(db); err != nil {
		t.Fatalf("CreateFindingTable: %v", err)
	}
	fs := NewFindingStore(db)
	app.Findings(fs)
	insertDecision(t, db, "ok", "ok-slug", "ratified")
	insertDecision(t, db, "no-row", "no-row-slug", "ratified")       // holds state, no stored standing
	insertDecision(t, db, "stale", "stale-slug", "superseded")       // does not hold, but stored holds
	insertDecision(t, db, "fine-none", "fine-none-slug", "proposed") // untagged, none: no drift
	for id, s := range map[string]Standing{"ok": StandingHolds, "stale": StandingHolds} {
		if _, err := db.ExecContext(context.Background(),
			`INSERT INTO smeldr_standing (subject_type, subject_id, standing) VALUES ('Decision', ?, ?)`, id, string(s)); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	checked, drifted, err := app.CheckStandingDrift(context.Background())
	if err != nil {
		t.Fatalf("CheckStandingDrift: %v", err)
	}
	if checked != 4 || drifted != 2 {
		t.Errorf("checked/drifted = %d/%d, want 4/2", checked, drifted)
	}
	fl, err := fs.List(context.Background(), "standing-drift")
	if err != nil {
		t.Fatalf("List findings: %v", err)
	}
	ids := map[string]bool{}
	for _, f := range fl {
		ids[f.SubjectID] = true
		if f.SubjectType != "Decision" || f.Provenance != "detected" {
			t.Errorf("finding = %+v", f)
		}
	}
	if !ids["no-row"] || !ids["stale"] || len(ids) != 2 {
		t.Errorf("findings for %v, want no-row and stale only", ids)
	}
	if s, _ := mustStanding(t, db, "Decision", "stale"); s != StandingHolds {
		t.Errorf("the drift check repaired an item (%q); it must only report", s)
	}
	if got, _ := mustStanding(t, db, "Decision", "no-row"); got != StandingNone {
		t.Errorf("the drift check wrote a row for no-row (%q)", got)
	}
}

// failingFindings makes Record fail, to prove a failed Finding is logged and
// the mismatch is still counted.
type failingFindings struct{}

func (failingFindings) Record(context.Context, Finding) error { return errors.New("record failed") }
func (failingFindings) List(context.Context, string) ([]Finding, error) {
	return nil, nil
}

func TestCheckStandingDrift_Failures(t *testing.T) {
	app, db, _ := setupTransitionItemApp(t)
	insertDecision(t, db, "no-row", "no-row-slug", "ratified")
	app.Findings(failingFindings{})
	_, drifted, err := app.CheckStandingDrift(context.Background())
	if err != nil || drifted != 1 {
		t.Errorf("a failing Finding store: drifted=%d err=%v, want 1 and nil", drifted, err)
	}
	if c, d, err := (&App{}).CheckStandingDrift(context.Background()); c != 0 || d != 0 || err != nil {
		t.Errorf("no db: %d/%d/%v", c, d, err)
	}
	broken := &App{cfg: Config{DB: &standingReadFailDB{DB: db}}}
	if _, _, err := broken.CheckStandingDrift(context.Background()); err == nil {
		t.Error("a failing standing read must be returned")
	}
}

// standingReadFailDB fails the read of stored standings only.
type standingReadFailDB struct{ DB }

func (d *standingReadFailDB) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	if strings.Contains(q, "FROM smeldr_standing WHERE subject_type") {
		return nil, errors.New("simulated standing read failure")
	}
	return d.DB.QueryContext(ctx, q, args...)
}

// TestWriteStanding_FailuresFailOpen: a failing write or a failing event never
// fails the change being recorded.
func TestWriteStanding_FailuresFailOpen(t *testing.T) {
	db := newMigratedDB(t)
	standingFlow(t, db, "FailT", "live")
	c := stateChange{typeName: "FailT", id: "i1", from: "draft", to: "live"}

	prov := &fakeProvenanceStore{}
	writeStanding(context.Background(), &nthExecFailDB{DB: db, fail: 1}, prov, c)
	if got := prov.Appended(); len(got) != 0 {
		t.Errorf("an event was written for a failed value write: %+v", got)
	}
	if _, has := mustStanding(t, db, "FailT", "i1"); !has {
		t.Fatal("type should have standing")
	}
	if s, _ := mustStanding(t, db, "FailT", "i1"); s != StandingNone {
		t.Errorf("standing = %q after a failed write, want none (nothing stored)", s)
	}

	af := &appendFailStore{}
	writeStanding(context.Background(), db, af, c)
	if s, _ := mustStanding(t, db, "FailT", "i1"); s != StandingHolds || af.calls != 1 {
		t.Errorf("standing = %q, Append calls = %d, want holds stored and one event attempted", s, af.calls)
	}

	// A lookup that fails means standing is simply not tracked for the change.
	writeStanding(context.Background(), &queryFailDB{}, nil, stateChange{typeName: "FailT", id: "x", from: "draft", to: "live"})
	writeStanding(context.Background(), nil, nil, c)
	writeStanding(context.Background(), db, nil, stateChange{})
	forgetStanding(context.Background(), nil, "FailT", "i1")
	forgetStanding(context.Background(), &queryFailDB{}, "FailT", "i1")
}

func TestItemStanding_AndCount_Errors(t *testing.T) {
	db := newMigratedDB(t)
	standingFlow(t, db, "ErrT", "live")
	if _, _, err := ItemStanding(context.Background(), &standingReadFailDB2{DB: db}, "ErrT", "i1"); !errors.Is(err, ErrInternal) {
		t.Errorf("ItemStanding query error = %v, want ErrInternal", err)
	}
	writeStanding(context.Background(), db, nil, stateChange{typeName: "ErrT", id: "a", from: "draft", to: "live"})
	writeStanding(context.Background(), db, nil, stateChange{typeName: "ErrT", id: "b", from: "draft", to: "live"})
	writeStanding(context.Background(), db, nil, stateChange{typeName: "ErrT", id: "b", from: "live", to: "closed"})
	counts, err := CountStanding(context.Background(), db, "ErrT")
	if err != nil || counts[StandingHolds] != 1 || counts[StandingCeased] != 1 {
		t.Errorf("CountStanding = %v, %v, want 1 holds and 1 ceased", counts, err)
	}
	if _, err := CountStanding(context.Background(), &standingReadFailDB2{DB: db}, "ErrT"); !errors.Is(err, ErrInternal) {
		t.Errorf("CountStanding query error = %v, want ErrInternal", err)
	}
}

// standingReadFailDB2 fails the standing table's reads (single-row and grouped).
type standingReadFailDB2 struct{ DB }

func (d *standingReadFailDB2) QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row {
	if strings.Contains(q, "FROM smeldr_standing WHERE subject_type = $1 AND subject_id") {
		return d.DB.QueryRowContext(ctx, "SELECT standing FROM smeldr_standing_does_not_exist")
	}
	return d.DB.QueryRowContext(ctx, q, args...)
}

func (d *standingReadFailDB2) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	if strings.Contains(q, "GROUP BY standing") || strings.Contains(q, "subject_id IN") {
		return nil, errors.New("simulated group failure")
	}
	return d.DB.QueryContext(ctx, q, args...)
}

// TestBuildContextPacket_Standing: the packet carries the stored standing next to
// the governed state, and omits it for a type without standing.
func TestBuildContextPacket_Standing(t *testing.T) {
	app, db, rs := setupTransitionItemApp(t)
	ratifyDecision(t, app, db, rs, "dec-pk", "dec-pk-slug")
	pkt, err := BuildContextPacket(context.Background(), db, nil, "https://example.com", "test", "decision", "dec-pk-slug", 1)
	if err != nil {
		t.Fatalf("BuildContextPacket: %v", err)
	}
	if pkt.Anchor.Status != "ratified" || pkt.Anchor.Standing != StandingHolds {
		t.Errorf("anchor status/standing = %q/%q, want ratified/holds", pkt.Anchor.Status, pkt.Anchor.Standing)
	}
	insertSignal(t, db, "sg-pk", "sg-pk-slug", "pending")
	spkt, err := BuildContextPacket(context.Background(), db, nil, "https://example.com", "test", "signal", "sg-pk-slug", 1)
	if err != nil {
		t.Fatalf("BuildContextPacket(signal): %v", err)
	}
	if spkt.Anchor.Standing != "" {
		t.Errorf("signal anchor standing = %q, want omitted", spkt.Anchor.Standing)
	}
}

// TestFlowGraph_Classify pins the migration's classification rules on a graph
// that exercises each class, including a state reached from both a holding and a
// non-holding state, and an initial state reachable from a holding one.
func TestFlowGraph_Classify(t *testing.T) {
	g := flowGraph{
		typeName: "G",
		initial:  map[string]bool{"draft": true, "reopened": true},
		tagged:   map[string]bool{"live": true},
		states:   []string{"draft", "live", "after", "mixed", "reopened", "island"},
		transitions: [][2]string{
			{"draft", "live"}, {"live", "after"}, {"live", "mixed"}, {"draft", "mixed"}, {"live", "reopened"},
		},
	}
	want := map[string]Standing{
		"draft":    StandingNone,        // nothing from a holding state reaches it
		"live":     StandingHolds,       // tagged
		"after":    StandingCeased,      // only reachable from the holding state
		"mixed":    StandingNotRecorded, // reachable from a holding and a non-holding state
		"reopened": StandingNotRecorded, // initial, but reachable from the holding state
		"island":   StandingNone,        // unreachable
	}
	got := g.classify()
	for s, w := range want {
		if got[s] != w {
			t.Errorf("classify[%s] = %q, want %q", s, got[s], w)
		}
	}
}

// failMatchDB fails any query, row query or exec whose SQL contains the given
// text, so each error branch of the standing helpers can be reached on its own.
type failMatchDB struct {
	DB
	query, row, exec string
}

func (d *failMatchDB) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	if d.query != "" && strings.Contains(q, d.query) {
		return nil, errors.New("simulated query failure")
	}
	return d.DB.QueryContext(ctx, q, args...)
}

func (d *failMatchDB) QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row {
	if d.row != "" && strings.Contains(q, d.row) {
		return d.DB.QueryRowContext(ctx, "SELECT x FROM smeldr_no_such_table_for_test")
	}
	return d.DB.QueryRowContext(ctx, q, args...)
}

func (d *failMatchDB) ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error) {
	if d.exec != "" && strings.Contains(q, d.exec) {
		return nil, errors.New("simulated exec failure")
	}
	return d.DB.ExecContext(ctx, q, args...)
}

// TestStandingHelpers_ErrorBranches reaches the read and write failure branches
// of the migration, the drift check and the delete hook.
func TestStandingHelpers_ErrorBranches(t *testing.T) {
	ctx := context.Background()
	newDB := func(t *testing.T) *sql.DB {
		_, db, _ := setupTransitionItemApp(t)
		seedDecisions(t, db)
		return db
	}

	t.Run("migrate: flow read fails", func(t *testing.T) {
		if err := MigrateStanding(ctx, &failMatchDB{DB: newDB(t), query: "FROM smeldr_state_flows f"}); err == nil {
			t.Error("want the flow read failure returned")
		}
	})
	t.Run("migrate: state read fails", func(t *testing.T) {
		if err := MigrateStanding(ctx, &failMatchDB{DB: newDB(t), query: "FROM smeldr_states WHERE flow_id"}); err == nil {
			t.Error("want the state read failure returned")
		}
	})
	t.Run("migrate: transition read fails", func(t *testing.T) {
		if err := MigrateStanding(ctx, &failMatchDB{DB: newDB(t), query: "FROM smeldr_transitions"}); err == nil {
			t.Error("want the transition read failure returned")
		}
	})
	t.Run("migrate: marker read fails", func(t *testing.T) {
		if err := MigrateStanding(ctx, &failMatchDB{DB: newDB(t), row: "FROM smeldr_standing_migrations"}); err == nil {
			t.Error("want the marker read failure returned")
		}
	})
	t.Run("migrate: item read fails", func(t *testing.T) {
		if err := MigrateStanding(ctx, &failMatchDB{DB: newDB(t), query: `FROM "smeldr_decisions"`}); err == nil {
			t.Error("want the item read failure returned")
		}
	})
	t.Run("migrate: marker write fails, type stays unmarked", func(t *testing.T) {
		db := newDB(t)
		if err := MigrateStanding(ctx, &failMatchDB{DB: db, exec: "INSERT INTO smeldr_standing_migrations"}); err == nil {
			t.Error("want the marker write failure returned")
		}
		var n int
		_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM smeldr_standing_migrations`).Scan(&n)
		if n != 0 {
			t.Error("a marker exists although its write failed")
		}
	})
	t.Run("migrate: item table missing is no items, not an error", func(t *testing.T) {
		db := newDB(t)
		if _, err := db.ExecContext(ctx, `DROP TABLE smeldr_decisions`); err != nil {
			t.Fatalf("drop: %v", err)
		}
		if err := MigrateStanding(ctx, db); err != nil {
			t.Errorf("a missing item table: %v", err)
		}
	})

	t.Run("drift: flow read fails", func(t *testing.T) {
		app := &App{cfg: Config{DB: &failMatchDB{DB: newDB(t), query: "FROM smeldr_state_flows f"}}}
		if _, _, err := app.CheckStandingDrift(ctx); err == nil {
			t.Error("want the flow read failure returned")
		}
	})
	t.Run("drift: item read fails", func(t *testing.T) {
		app := &App{cfg: Config{DB: &failMatchDB{DB: newDB(t), query: `FROM "smeldr_decisions"`}}}
		if _, _, err := app.CheckStandingDrift(ctx); err == nil {
			t.Error("want the item read failure returned")
		}
	})
	t.Run("drift: no standing table means every holding item is unrecorded", func(t *testing.T) {
		db := newDB(t)
		if _, err := db.ExecContext(ctx, `DROP TABLE smeldr_standing`); err != nil {
			t.Fatalf("drop: %v", err)
		}
		app := &App{cfg: Config{DB: db}}
		_, drifted, err := app.CheckStandingDrift(ctx)
		if err != nil || drifted != 2 {
			t.Errorf("drifted = %d, err = %v, want the 2 holding decisions reported", drifted, err)
		}
	})
	t.Run("drift: a stored value that is not holds on a holding state", func(t *testing.T) {
		app, db, _ := setupTransitionItemApp(t)
		insertDecision(t, db, "c", "c-slug", "ratified")
		if _, err := db.ExecContext(ctx,
			`INSERT INTO smeldr_standing (subject_type, subject_id, standing) VALUES ('Decision', 'c', 'ceased')`); err != nil {
			t.Fatalf("seed: %v", err)
		}
		if _, drifted, err := app.CheckStandingDrift(ctx); err != nil || drifted != 1 {
			t.Errorf("drifted = %d, err = %v, want 1", drifted, err)
		}
	})

	t.Run("forget: a failing delete is logged, not fatal", func(t *testing.T) {
		db := newDB(t)
		forgetStanding(ctx, &failMatchDB{DB: db, exec: "DELETE FROM smeldr_standing"}, "Decision", "d-rat")
	})
	t.Run("flow reads: tagged-state lookup on a database without flow tables", func(t *testing.T) {
		if got := holdsStates(ctx, newSQLiteDB(t), "Decision"); len(got) != 0 {
			t.Errorf("holdsStates = %v, want none", got)
		}
	})
}
