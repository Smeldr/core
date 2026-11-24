package smeldr

import (
	"strings"
	"testing"
	"time"
)

// drainEvents reads what the subscription got until it is quiet.
func drainEvents(ch chan []byte) []string {
	var out []string
	for {
		select {
		case b := <-ch:
			out = append(out, string(b))
		case <-time.After(300 * time.Millisecond):
			return out
		}
	}
}

// T1324: two head commits of one pull request checked at the same moment
// leave one live Check. The hook lands call B (head B) between call A's read
// of Check X's state and its move of X to commented, the order that made two
// live Checks: B supersedes X, and A's move must not undo that.
func TestCheckChange_TwoHeadsRace_Deterministic(t *testing.T) {
	f := newChangeCheckFixture(t)
	f.app.EventStream()
	ch, err := f.app.eventBroadcaster.subscribeScoped("ops-1", eventStreamChannelAll, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.app.eventBroadcaster.unsubscribe(ch)
	in := ChangeInput{Repository: "acme/app", Number: 30, HeadCommit: headA, Paths: []string{"internal/billing/a.go"}}
	var x string
	var errB error
	ran := false
	transitionBeforeWrite = func(typeName, id string) {
		if typeName != "ChangeCheck" || ran {
			return
		}
		ran = true
		x = id
		next := in
		next.HeadCommit = headB
		_, errB = f.app.CheckChange(f.ops, next)
	}
	t.Cleanup(func() { transitionBeforeWrite = nil })
	if _, err := f.app.CheckChange(f.ops, in); err != nil {
		t.Fatalf("call A: %v", err)
	}
	if errB != nil {
		t.Fatalf("call B: %v", errB)
	}
	if !ran {
		t.Fatal("the hook never ran")
	}
	if n := f.liveChecks(t, 30); n != 1 {
		t.Errorf("live = %d, want 1", n)
	}
	var xStatus, y, yStatus string
	if err := f.db.QueryRow(`SELECT status FROM smeldr_change_checks WHERE id = $1`, x).Scan(&xStatus); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow(`SELECT id, status FROM smeldr_change_checks WHERE number = 30 AND head_commit = $1`, headB).Scan(&y, &yStatus); err != nil {
		t.Fatal(err)
	}
	if xStatus != ChangeCheckSuperseded || yStatus != ChangeCheckCommented {
		t.Errorf("X %s, Y %s; want superseded, commented", xStatus, yStatus)
	}
	var edges int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM smeldr_relations WHERE source_id = $1 AND target_id = $2 AND relation_kind = 'supersedes' AND invalid_at IS NULL`, y, x).Scan(&edges); err != nil || edges != 1 {
		t.Errorf("supersedes edges from Y to X: %d, %v", edges, err)
	}
	// The refused move of X to commented left no trace.
	var prov int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM smeldr_provenance WHERE subject_id = $1 AND to_state = 'commented'`, x).Scan(&prov); err != nil || prov != 0 {
		t.Errorf("provenance of X to commented: %d, %v", prov, err)
	}
	for _, e := range drainEvents(ch) {
		if strings.Contains(e, `"id":"`+x+`"`) && strings.Contains(e, `"to_state":"commented"`) {
			t.Errorf("an event for the refused move: %s", e)
		}
	}
}
