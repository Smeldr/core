package smeldr

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// T918 part 3: audit after merge (D143).

const mergeSHA = "cccccccccccccccccccccccccccccccccccccccc"

// newAuditCheck is a Check of both fixture Areas with the first in
// audit-after-merge and the second as given ("" leaves it comment-only).
func newAuditCheck(t *testing.T, second string) (changeCheckFixture, *ChangeCheck) {
	t.Helper()
	return newApprovalCheck(t, func(f changeCheckFixture) []*Area {
		if _, err := f.db.Exec(`UPDATE smeldr_areas SET approval_mode = 'audit-after-merge' WHERE id = $1`, f.a1.ID); err != nil {
			t.Fatal(err)
		}
		if second != "" {
			if _, err := f.db.Exec(`UPDATE smeldr_areas SET approval_mode = $1 WHERE id = $2`, second, f.a2.ID); err != nil {
				t.Fatal(err)
			}
		}
		return nil
	})
}

func (f changeCheckFixture) merge(t *testing.T, head string) MergeResult {
	t.Helper()
	res, err := f.app.RecordMerge(f.ops, MergeInput{Repository: "acme/app", Number: 30, HeadCommit: head, MergeCommit: mergeSHA})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// audits are the merge-unapproved conditions of a Check: id, status, message.
func (f changeCheckFixture) audits(t *testing.T, checkID string) [][3]string {
	t.Helper()
	rows, err := f.db.Query(`SELECT id, status, message FROM smeldr_signals WHERE subject_id = $1 AND signal_type = 'merge-unapproved'`, checkID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out [][3]string
	for rows.Next() {
		var v [3]string
		if err := rows.Scan(&v[0], &v[1], &v[2]); err != nil {
			t.Fatal(err)
		}
		out = append(out, v)
	}
	return out
}

func TestRecordMerge_OpensAudit(t *testing.T) {
	f, c := newAuditCheck(t, "")
	if string(c.Status) != ChangeCheckCommented || c.Setting != ApprovalModeAuditAfterMerge {
		t.Fatalf("check = %s, setting %s", c.Status, c.Setting)
	}
	res := f.merge(t, headA)
	if !res.Covered || res.HeadMismatch || strings.Join(res.Opened, ",") != f.a1.Address {
		t.Fatalf("merge = %+v", res)
	}
	audits := f.audits(t, c.ID)
	if len(audits) != 1 || audits[0][1] != signalOpen || !strings.Contains(audits[0][2], f.a1.Address) || strings.Contains(audits[0][2], f.a2.Address) {
		t.Fatalf("audits = %v", audits)
	}
	var placed int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM smeldr_relations WHERE source_id = $1 AND relation_kind = 'belongs_to_area' AND target_id = $2`, audits[0][0], f.a1.ID).Scan(&placed); err != nil || placed != 1 {
		t.Errorf("audit placed %d (%v)", placed, err)
	}
	// A second merge report opens nothing more.
	if again := f.merge(t, headA); len(again.Opened) != 0 || len(f.audits(t, c.ID)) != 1 {
		t.Errorf("second merge: %+v", again)
	}
	// The approver approves after the fact: the condition is resolved, the
	// Check keeps its state.
	if err := f.app.AnswerChangeCheck(human("u-author"), c.Address, f.a1.Address, true, ""); err != nil {
		t.Fatal(err)
	}
	if got := f.audits(t, c.ID)[0][1]; got != signalResolved {
		t.Errorf("audit after the answer: %s", got)
	}
	if f.status(t, c.ID) != ChangeCheckCommented {
		t.Errorf("Check moved to %s", f.status(t, c.ID))
	}
	// Answered: a second answer is refused.
	if err := f.app.AnswerChangeCheck(human("u-author"), c.Address, f.a1.Address, false, "late"); !errors.Is(err, ErrConflict) {
		t.Errorf("answer after resolved: %v", err)
	}
}

func TestRecordMerge_NotApproved(t *testing.T) {
	f, c := newAuditCheck(t, "")
	f.merge(t, headA)
	if err := f.app.AnswerChangeCheck(human("u-author"), c.Address, f.a1.Address, false, ""); err == nil || !strings.Contains(err.Error(), "reason") {
		t.Errorf("no reason: %v", err)
	}
	if err := f.app.AnswerChangeCheck(human("u-author"), c.Address, f.a1.Address, false, "the money rule is broken"); err != nil {
		t.Fatal(err)
	}
	var resolution string
	if err := f.db.QueryRow(`SELECT resolution FROM smeldr_signals WHERE subject_id = $1 AND signal_type = 'merge-unapproved'`, c.ID).Scan(&resolution); err != nil || !strings.Contains(resolution, "not approved: the money rule is broken") {
		t.Errorf("resolution = %q, %v", resolution, err)
	}
}

func TestRecordMerge_ApprovedBeforeMerge(t *testing.T) {
	f, c := newAuditCheck(t, "")
	if err := f.app.AnswerChangeCheck(human("u-author"), c.Address, f.a1.Address, true, ""); err != nil {
		t.Fatal(err)
	}
	if res := f.merge(t, headA); len(res.Opened) != 0 || len(f.audits(t, c.ID)) != 0 {
		t.Errorf("an approved Area opened an audit: %+v", res)
	}
}

func TestRecordMerge_Edges(t *testing.T) {
	f, c := newAuditCheck(t, ApprovalModeRequiresApproval)
	if c.Setting != ApprovalModeRequiresApproval {
		t.Errorf("setting = %s, want the strictest", c.Setting)
	}
	// Another head was merged than the one checked: the audit opens and says so.
	res := f.merge(t, headB)
	if !res.HeadMismatch || len(res.Opened) != 1 || !strings.Contains(f.audits(t, c.ID)[0][2], "is not the checked head") {
		t.Errorf("mismatch: %+v %v", res, f.audits(t, c.ID))
	}
	// No live Check: nothing.
	none, err := f.app.RecordMerge(f.ops, MergeInput{Repository: "acme/app", Number: 99, HeadCommit: headA, MergeCommit: mergeSHA})
	if err != nil || none.Covered || len(none.Opened) != 0 {
		t.Errorf("no Check: %+v, %v", none, err)
	}
	for _, tc := range []struct {
		name string
		in   MergeInput
		want string
	}{
		{"merge commit", MergeInput{Repository: "acme/app", Number: 30, HeadCommit: headA, MergeCommit: "main"}, "merge_commit"},
		{"head commit", MergeInput{Repository: "acme/app", Number: 30, HeadCommit: "x", MergeCommit: mergeSHA}, "head_commit"},
		{"repository", MergeInput{Repository: "acme", Number: 30, HeadCommit: headA, MergeCommit: mergeSHA}, "repository"},
	} {
		if _, err := f.app.RecordMerge(f.ops, tc.in); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v", tc.name, err)
		}
	}
	if _, err := f.app.RecordMerge(f.reader, MergeInput{Repository: "acme/app", Number: 30, HeadCommit: headA, MergeCommit: mergeSHA}); !errors.Is(err, ErrForbidden) {
		t.Errorf("reader: %v", err)
	}
	if _, err := New(Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16")}).RecordMerge(context.Background(), MergeInput{Repository: "acme/app", Number: 1, HeadCommit: headA, MergeCommit: mergeSHA}); !errors.Is(err, ErrInternal) {
		t.Errorf("no database: %v", err)
	}
	// An audit answer on a superseded Check is refused.
	if _, err := f.app.CheckChange(f.ops, ChangeInput{Repository: "acme/app", Number: 30, HeadCommit: headB, Paths: approvalPaths}); err != nil {
		t.Fatal(err)
	}
	if err := f.app.AnswerChangeCheck(human("u-author"), c.Address, f.a1.Address, true, ""); !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "superseded") {
		t.Errorf("superseded: %v", err)
	}
}

func TestRecordMerge_Failures(t *testing.T) {
	for _, tc := range []struct{ name, sql string }{
		{"condition", `CREATE TRIGGER t BEFORE INSERT ON smeldr_signals BEGIN SELECT RAISE(ABORT, 'no'); END`},
		{"Areas", `ALTER TABLE smeldr_change_check_areas RENAME TO gone`},
		{"Checks", `ALTER TABLE smeldr_change_checks RENAME TO gone`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, c := newAuditCheck(t, "")
			if _, err := f.db.Exec(tc.sql); err != nil {
				t.Fatal(err)
			}
			if _, err := f.app.RecordMerge(f.ops, MergeInput{Repository: "acme/app", Number: 30, HeadCommit: headA, MergeCommit: mergeSHA}); !errors.Is(err, ErrInternal) {
				t.Errorf("%v, want internal", err)
			}
			if tc.name == "condition" && len(f.audits(t, c.ID)) != 0 {
				t.Error("a refused condition left one")
			}
		})
	}
	// The answer's write and the condition read fail.
	f, c := newAuditCheck(t, "")
	f.merge(t, headA)
	if _, err := f.db.Exec(`CREATE TRIGGER t BEFORE UPDATE ON smeldr_change_check_areas BEGIN SELECT RAISE(ABORT, 'no'); END`); err != nil {
		t.Fatal(err)
	}
	if err := f.app.AnswerChangeCheck(human("u-author"), c.Address, f.a1.Address, true, ""); !errors.Is(err, ErrInternal) {
		t.Errorf("answer refused: %v", err)
	}
}

func TestAreaApprovalMode_Audit(t *testing.T) {
	a := &Area{ApprovalMode: " audit-after-merge "}
	if err := normalizeApprovalMode(a); err != nil || a.ApprovalMode != ApprovalModeAuditAfterMerge {
		t.Errorf("audit mode: %q, %v", a.ApprovalMode, err)
	}
	if err := normalizeApprovalMode(&Area{ApprovalMode: "sometimes"}); err == nil || !strings.Contains(err.Error(), ApprovalModeAuditAfterMerge) {
		t.Errorf("refusal names the modes: %v", err)
	}
}

// The races and failures inside the audit's writes, made by triggers.
func TestRecordMerge_Races(t *testing.T) {
	merge := MergeInput{Repository: "acme/app", Number: 30, HeadCommit: headA, MergeCommit: mergeSHA}
	for _, tc := range []struct {
		name, sql string
		wantErr   bool
	}{
		{"taken by another call", `CREATE TRIGGER t BEFORE UPDATE ON smeldr_change_check_areas WHEN NEW.verdict = 'awaiting-audit' BEGIN SELECT RAISE(IGNORE); END`, false},
		{"verdict refused", `CREATE TRIGGER t BEFORE UPDATE ON smeldr_change_check_areas WHEN NEW.verdict = 'awaiting-audit' BEGIN SELECT RAISE(ABORT, 'no'); END`, true},
		{"link refused", `CREATE TRIGGER t BEFORE UPDATE ON smeldr_change_check_areas WHEN NEW.signal_id <> '' BEGIN SELECT RAISE(ABORT, 'no'); END`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, c := newAuditCheck(t, "")
			if _, err := f.db.Exec(tc.sql); err != nil {
				t.Fatal(err)
			}
			res, err := f.app.RecordMerge(f.ops, merge)
			if tc.wantErr != (err != nil) {
				t.Fatalf("err = %v", err)
			}
			if err == nil && len(res.Opened) != 0 {
				t.Errorf("opened %v", res.Opened)
			}
			if len(f.audits(t, c.ID)) != 0 {
				t.Error("a condition was left")
			}
		})
	}
	// The answer loses its race, or cannot read the condition.
	f, c := newAuditCheck(t, "")
	f.merge(t, headA)
	if _, err := f.db.Exec(`CREATE TRIGGER t BEFORE UPDATE ON smeldr_change_check_areas WHEN NEW.verdict = 'approved' BEGIN SELECT RAISE(IGNORE); END`); err != nil {
		t.Fatal(err)
	}
	if err := f.app.AnswerChangeCheck(human("u-author"), c.Address, f.a1.Address, true, ""); !errors.Is(err, ErrConflict) {
		t.Errorf("answered at the same moment: %v", err)
	}
	if _, err := f.db.Exec(`DROP TRIGGER t`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`ALTER TABLE smeldr_signals RENAME TO gone`); err != nil {
		t.Fatal(err)
	}
	if err := f.app.AnswerChangeCheck(human("u-author"), c.Address, f.a1.Address, true, ""); !errors.Is(err, ErrInternal) {
		t.Errorf("condition unreadable: %v", err)
	}
}
