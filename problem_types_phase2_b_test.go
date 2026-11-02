package smeldr

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

// T1061 phase 2: the typed refusal sites of places, state, orchestration,
// decision sources, plan time and approvers carry their type and values, and
// keep their old sentinel and message.

// typedCaseB is one real refusal and what it must carry.
type typedCaseB struct {
	name     string
	err      error
	typ      ProblemType
	values   map[string]string
	sentinel error  // errors.Is target; nil for a ValidationError
	field    string // the ValidationError's field, when sentinel is nil
	msg      string // the exact Error() text, or
	sub      string // a stable substring of it
}

// wantTypedB checks one typedCaseB.
func wantTypedB(t *testing.T, c typedCaseB) {
	t.Helper()
	if c.err == nil {
		t.Fatalf("no refusal")
	}
	if got := ProblemTypeOf(c.err); got != c.typ {
		t.Errorf("ProblemTypeOf = %q, want %q (err %v)", got, c.typ, c.err)
	}
	if got := ProblemValuesOf(c.err); !reflect.DeepEqual(got, c.values) {
		t.Errorf("ProblemValuesOf = %#v, want %#v", got, c.values)
	}
	if c.sentinel != nil {
		if !errors.Is(c.err, c.sentinel) {
			t.Errorf("err = %v, want errors.Is %v", c.err, c.sentinel)
		}
	} else {
		var ve *ValidationError
		if !errors.As(c.err, &ve) {
			t.Fatalf("err = %v (%T), want a ValidationError", c.err, c.err)
		}
		if fs := ve.Fields(); len(fs) != 1 || fs[0].Field != c.field {
			t.Errorf("fields = %+v, want one on %q", fs, c.field)
		}
	}
	if c.msg != "" && c.err.Error() != c.msg {
		t.Errorf("Error() = %q\nwant      %q", c.err.Error(), c.msg)
	}
	if c.sub != "" && !strings.Contains(c.err.Error(), c.sub) {
		t.Errorf("Error() = %q, want it to contain %q", c.err.Error(), c.sub)
	}
}

// msgB is the text fmt.Errorf gives a sentinel-wrapping message.
func msgB(format string, args ...any) string { return fmt.Errorf(format, args...).Error() }

// Place edges, ratify without a place, sources and plan-time edges.
func TestPhaseTwoB_Places(t *testing.T) {
	f := newPlaceAuthorityFixture(t)
	author := as("plain-1")
	f.decision(t, "rat", "ratified")
	f.decision(t, "prop", "proposed")
	if err := f.place(author, "Decision", "prop", "Domain", f.domain, "asserted"); err != nil {
		t.Fatal(err)
	}
	ratified := Decision{Node: Node{ID: "rat", Status: "ratified"}}
	unplaced := Decision{Node: Node{ID: "unplaced", Status: "ratified"},
		Sources: DecisionSources{{Repository: "acme/app", Commit: testSHA, Path: "a.md", Role: "source"}}}
	if err := f.place(as("human-1", Human), "Decision", "rat", "Domain", f.domain, "asserted"); err != nil {
		t.Fatal(err)
	}
	ratified.Sources = unplaced.Sources
	changeSources := func(old Decision) error {
		next := old
		next.Sources = DecisionSources{{Repository: "acme/app", Commit: testSHA, Path: "b.md", Role: "source"}}
		return f.app.prepareDecision(actorCtx("plain-1"), &old, &next)
	}
	f.decision(t, "r1", "ratified")
	f.decision(t, "r2", "ratified")
	_, _, planEdgeErr := f.rels.MCPAssertRelationAt(author, planType, "plan-1", "Area", f.area, "belongs_to_area", nil, nil, nil, nil, nil)

	for _, c := range []typedCaseB{
		{name: "authority required, act place", err: f.place(author, "Decision", "r1", "Domain", f.domain, "asserted"),
			typ: problemPlaceAuthorityRequired, values: map[string]string{"act": "place", "operation": OperationRatify, "place": "DOM1"},
			sentinel: ErrForbidden, msg: msgB("%w: placing D-r1 in DOM1 needs ratify over DOM1 (D145)", ErrForbidden)},
		{name: "authority required, act change_sources", err: changeSources(ratified),
			typ: problemPlaceAuthorityRequired, values: map[string]string{"act": "change_sources", "operation": OperationRatify, "place": "DOM1"},
			sentinel: ErrForbidden, msg: msgB("%w: changing D-rat's sources needs ratify over DOM1 (D148)", ErrForbidden)},
		{name: "no actor", err: f.place(context.Background(), "Decision", "r2", "Domain", f.domain, "asserted"),
			typ: problemPlaceNoActor, values: map[string]string{"act": "place", "operation": OperationRatify, "place": "DOM1"},
			sentinel: ErrForbidden, msg: msgB("%w: placing D-r2 in DOM1 needs ratify over DOM1, and no actor is known", ErrForbidden)},
		{name: "place edge from a missing Decision", err: f.place(author, "Decision", "ghost", "Domain", f.domain, "asserted"),
			typ: problemPlaceNotFound, values: map[string]string{"decision": "ghost"},
			sentinel: ErrNotFound, msg: msgB("%w: no Decision with ID %q; a place edge names its Decision by ID", ErrNotFound, "ghost")},
		{name: "second place", err: f.place(author, "Decision", "prop", "Domain", f.dom2, "asserted"),
			typ: problemDecisionPlaceExists, values: map[string]string{"decision": "D-prop", "kind": "belongs_to_domain", "place": "DOM1"},
			sentinel: ErrConflict, msg: msgB("%w: Decision D-prop already has a place (belongs_to_domain DOM1): a Decision has exactly one place; withdraw that relation first", ErrConflict)},
		{name: "sources need a place", err: changeSources(unplaced),
			typ: problemDecisionSourcesNeedPlace, values: map[string]string{"decision": "unplaced"},
			sentinel: ErrForbidden, msg: msgB("%w: changing unplaced's sources needs ratify over its place, and it has none (D148)", ErrForbidden)},
		{name: "plan-time edge by hand", err: planEdgeErr,
			typ: problemPlanRelationsCoreOwned, values: nil, sentinel: ErrForbidden,
			msg: "forbidden: a plan's belongs_to_area and checked_against relations are recorded by Smeldr at submit and approve, from the plan's repository and paths; change those instead"},
		{name: "approvers of no place", err: errOnlyB(f.app.ApproversOf(author, " ")),
			typ: problemApproversPlaceRequired, values: nil, sentinel: ErrBadRequest,
			msg: msgB("%w: name a place: an Organization, Domain or Area by address, slug or ID", ErrBadRequest)},
		{name: "approvers of an unknown place", err: errOnlyB(f.app.ApproversOf(author, "NOWHERE")),
			typ: problemPlaceNotFound, values: map[string]string{"place": "NOWHERE"}, sentinel: ErrNotFound,
			msg: msgB("%w: no place %q you can read (an Organization, Domain or Area by address, slug or ID)", ErrNotFound, "NOWHERE")},
	} {
		t.Run(c.name, func(t *testing.T) { wantTypedB(t, c) })
	}
}

// errOnlyB drops a result and keeps the error.
func errOnlyB[T any](_ T, err error) error { return err }

// Ratifying a Decision with no place, through TransitionItemVia.
func TestPhaseTwoB_RatifyWithoutPlace(t *testing.T) {
	app, db, rs := setupTransitionItemApp(t)
	ctx := stewardContext(t, db, rs)
	insertDecision(t, db, "dec-u", "dec-u-slug", "proposed")
	if _, err := db.Exec(`UPDATE smeldr_decisions SET address = 'D7' WHERE id = 'dec-u'`); err != nil {
		t.Fatal(err)
	}
	if err := CreateRelationTables(db); err != nil {
		t.Fatal(err)
	}
	_, err := app.TransitionItemVia(ctx, "mcp", "Decision", "dec-u-slug", "ratified", "")
	_, unreg := app.TransitionItem(ctx, "NoSuchType", "x", "done")
	for _, c := range []typedCaseB{
		{name: "place missing", err: err, typ: problemDecisionPlaceMissing, values: map[string]string{"decision": "D7"}, sentinel: ErrConflict,
			msg: msgB("%w: Decision D7 has no place: place it before ratifying, with assert_relation belongs_to_domain (a Domain), belongs_to_area (an Area) or belongs_to_organization (Organization, for a Decision that spans Domains)", ErrConflict)},
		{name: "type not registered", err: unreg, typ: problemTypeNotRegistered, values: map[string]string{"type": "NoSuchType"}, sentinel: ErrBadRequest,
			msg: msgB("%w: content type %q not registered", ErrBadRequest, "NoSuchType")},
	} {
		t.Run(c.name, func(t *testing.T) { wantTypedB(t, c) })
	}
}

// The transition checks of validateTransition, a flow's initial state, a
// singleton state, RegisterFlow's standing tag and a Decision's scope role.
func TestPhaseTwoB_Transitions(t *testing.T) {
	app, db, rs := setupTransitionItemApp(t)
	ctx := context.Background()
	if err := app.RegisterFlow(StateFlow{
		Name:     "phase-two-b",
		TypeName: "PhaseB",
		States:   []State{{Name: "a", IsInitial: true}, {Name: "b"}, {Name: "c"}, {Name: "z"}},
		Transitions: []Transition{
			{From: "a", To: "b", RequiredOperation: "approve", Strict: true},
			{From: "a", To: "c", RequiredReason: true},
		},
	}); err != nil {
		t.Fatal(err)
	}
	vt := func(rs *RoleStore, actor, from, to string) error {
		return validateTransition(ctx, db, rs, nil, actor, "item-1", "PhaseB", from, to, "")
	}
	badStanding := app.RegisterFlow(StateFlow{Name: "bad-standing", TypeName: "PhaseBad",
		States: []State{{Name: "s", IsInitial: true, Standing: "maybe"}}})
	if _, err := db.Exec(`CREATE TABLE phase_b_items (id TEXT, status TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO phase_b_items (id, status) VALUES ('i1', 'active')`); err != nil {
		t.Fatal(err)
	}
	scopeErr := authorizeDecisionScope(ctx, rs, "nobody", &Decision{Scope: "security"}, map[string]string{"security": "admin"})

	for _, c := range []typedCaseB{
		{name: "unknown target", err: vt(nil, "", "a", "nowhere"), typ: problemTransitionUnknownTarget,
			values: map[string]string{"type": "PhaseB", "to": "nowhere"}, sentinel: ErrConflict,
			msg: msgB("%w: %q is not a valid target state for type %q", ErrConflict, "nowhere", "PhaseB")},
		{name: "not permitted", err: vt(nil, "", "a", "z"), typ: problemTransitionNotPermitted,
			values: map[string]string{"type": "PhaseB", "from": "a", "to": "z"}, sentinel: ErrConflict,
			msg: msgB("%w: transition a→z is not permitted for type %q", ErrConflict, "PhaseB")},
		{name: "reason required", err: vt(nil, "", "a", "c"), typ: problemTransitionReasonRequired,
			values: map[string]string{"from": "a", "to": "c"}, sentinel: ErrBadRequest,
			msg: msgB("%w: transition a→c requires a reason", ErrBadRequest)},
		{name: "gate unwired", err: vt(nil, "u-1", "a", "b"), typ: problemTransitionGateUnwired,
			values: map[string]string{"from": "a", "to": "b", "operation": "approve"}, sentinel: ErrForbidden,
			msg: msgB("%w: transition a→b requires operation %q but governance is not wired", ErrForbidden, "approve")},
		{name: "no actor", err: vt(rs, "", "a", "b"), typ: problemTransitionNoActor,
			values: map[string]string{"from": "a", "to": "b", "operation": "approve"}, sentinel: ErrForbidden,
			msg: msgB("%w: transition a→b requires operation %q but no actor is present", ErrForbidden, "approve")},
		{name: "gate", err: vt(rs, "nobody", "a", "b"), typ: problemTransitionGate,
			values: map[string]string{"type": "PhaseB", "from": "a", "to": "b", "operation": "approve"}, sentinel: ErrForbidden,
			msg: msgB("%w: transition a→b requires operation %q", ErrForbidden, "approve")},
		{name: "unknown initial state", err: validateInitialState(ctx, db, "PhaseB", "bogus"), typ: problemStateUnknownInitial,
			values: map[string]string{"type": "PhaseB", "state": "bogus"}, sentinel: ErrConflict,
			msg: msgB("%w: %q is not a valid state in the registered flow for type %q", ErrConflict, "bogus", "PhaseB")},
		{name: "singleton state", err: conflictRejectCheck(ctx, db, "PhaseB", "active", "phase_b_items", false), typ: problemTransitionSingletonState,
			values: map[string]string{"type": "PhaseB", "state": "active"}, sentinel: ErrConflict,
			msg: msgB("%w: type %q already has an item in state %q", ErrConflict, "PhaseB", "active")},
		{name: "standing tag", err: badStanding, typ: problemFlowStandingInvalid,
			values: map[string]string{"flow": "bad-standing", "state": "s", "value": "maybe", "allowed": string(StandingHolds)}, sentinel: ErrBadRequest,
			msg: msgB("%w: smeldr: RegisterFlow %q: state %q: Standing %q is not a legal tag (only %q or empty)", ErrBadRequest, "bad-standing", "s", "maybe", StandingHolds)},
		{name: "decision scope role", err: scopeErr, typ: problemDecisionScopeRole,
			values: map[string]string{"scope": "security", "role": "admin"}, sentinel: ErrForbidden,
			msg: msgB("%w: scope %q requires role %q", ErrForbidden, "security", "admin")},
	} {
		t.Run(c.name, func(t *testing.T) { wantTypedB(t, c) })
	}
}

// QueryGoalContext's own refusals: no goal named, and no Goal table.
func TestPhaseTwoB_GoalContext(t *testing.T) {
	db := newSQLiteDB(t)
	ctx := context.Background()
	_, empty := QueryGoalContext(ctx, db, nil, "")
	_, noTable := QueryGoalContext(ctx, db, nil, "G1")
	for _, c := range []typedCaseB{
		{name: "no goal named", err: empty, typ: problemRequestInvalidParam, values: nil, sentinel: ErrBadRequest, msg: ErrBadRequest.Error()},
		{name: "add-on not run", err: noTable, typ: problemAddonNotRun, values: map[string]string{"type": "Goal"}, sentinel: ErrBadRequest,
			msg: msgB("%w: Goals belong to the Orchestration add-on, which this instance does not run", ErrBadRequest)},
	} {
		t.Run(c.name, func(t *testing.T) { wantTypedB(t, c) })
	}
}

// The field rules of a Decision's sources and a plan's paths.
func TestPhaseTwoB_Fields(t *testing.T) {
	ok := DecisionSource{Repository: "acme/app", Commit: testSHA, Path: "a.md", Role: "source"}
	with := func(f func(*DecisionSource)) DecisionSources { s := ok; f(&s); return DecisionSources{s} }
	many := DecisionSources{}
	for i := 0; i <= DecisionSourcesMax; i++ {
		s := ok
		s.Anchor = strconv.Itoa(i)
		many = append(many, s)
	}
	norm := func(in DecisionSources) error { _, err := normalizeDecisionSources(in); return err }
	plan := func(fields string, cutover time.Time) error {
		_, err := planPathsRule(&DynamicNode{Fields: []byte(fields)}, cutover)
		return err
	}
	paths := make([]string, DecisionsForPathsMaxPaths+1)
	for i := range paths {
		paths[i] = fmt.Sprintf("%q", "p"+strconv.Itoa(i))
	}
	tooMany := `{"repository":"acme/app","paths":[` + strings.Join(paths, ",") + `]}`
	required := "repository and paths are required to submit a plan whose work ends in a commit: the Areas and Decisions it rests on are recorded from them; set no_commit for work that ends in none"

	for _, c := range []typedCaseB{
		{name: "too many sources", err: norm(many), typ: problemDecisionSourcesTooMany,
			values: map[string]string{"count": strconv.Itoa(DecisionSourcesMax + 1), "limit": strconv.Itoa(DecisionSourcesMax)}, field: "sources",
			msg: fmt.Sprintf("validation failed: sources: has %d sources; a Decision carries at most %d", DecisionSourcesMax+1, DecisionSourcesMax)},
		{name: "duplicate source", err: norm(DecisionSources{ok, ok}), typ: problemDecisionSourceDuplicate,
			values: map[string]string{"index": "0"}, field: "sources[1].path",
			msg: "validation failed: sources[1].path: is the same source as sources[0]: the same repository, commit, path and anchor"},
		{name: "anchor too long", err: norm(with(func(s *DecisionSource) { s.Anchor = strings.Repeat("a", decisionSourceAnchorMax+1) })), typ: problemFieldTooLong,
			values: map[string]string{"limit": strconv.Itoa(decisionSourceAnchorMax)}, field: "sources[0].anchor",
			msg: fmt.Sprintf("validation failed: sources[0].anchor: is longer than %d characters", decisionSourceAnchorMax)},
		{name: "anchor on two lines", err: norm(with(func(s *DecisionSource) { s.Anchor = "a\nb" })), typ: problemFieldOneLine,
			values: nil, field: "sources[0].anchor",
			msg: "validation failed: sources[0].anchor: must be one line: a heading or a line range"},
		{name: "too many paths", err: plan(tooMany, time.Time{}), typ: problemFieldTooMany,
			values: map[string]string{"limit": strconv.Itoa(DecisionsForPathsMaxPaths)}, field: "paths",
			msg: fmt.Sprintf("validation failed: paths: at most %d patterns", DecisionsForPathsMaxPaths)},
		{name: "repository and paths after cutover", err: plan(`{}`, time.Now().Add(-time.Hour)), typ: problemPlanRepositoryPathsRequired,
			values: nil, field: "repository", msg: "validation failed: repository: " + required},
	} {
		t.Run(c.name, func(t *testing.T) { wantTypedB(t, c) })
	}
}

// Setting up places before the migration, and a migration whose address is held.
func TestPhaseTwoB_PlacesMigration(t *testing.T) {
	ctx := context.Background()
	db, _ := legacyPlacesDB(t)
	app := migratedApp(t, db)
	_, prefixErr := app.EnsureOrganization(ctx, "Acme")
	if _, err := db.Exec(`INSERT INTO smeldr_domains (id, slug, created_at, updated_at, name, address, address_no) VALUES ('other', 'other', $1, $1, 'x', 'DOM1', 1)`, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	_, heldErr := app.MigratePlaces(ctx, PlaceMigrationOptions{OrganizationName: "Acme"})
	for _, c := range []typedCaseB{
		{name: "domain prefix held", err: prefixErr, typ: problemPlacesDomainPrefixHeld, values: map[string]string{"prefix": "DOM"}, sentinel: ErrConflict,
			msg: msgB("%w: the runtime domain type still holds the DOM prefix; run migrate-places, which creates Organization", ErrConflict)},
		{name: "address held", err: heldErr, typ: problemPlacesMigrateAddressHeld,
			values: map[string]string{"type": "domain", "record": "DOM1", "holder_type": "Domain", "holder": "other"}, field: "address",
			msg: "validation failed: address: domain DOM1 is already held by Domain other: resolve it before migrating"},
	} {
		t.Run(c.name, func(t *testing.T) { wantTypedB(t, c) })
	}
}
