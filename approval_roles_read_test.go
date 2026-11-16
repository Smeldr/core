package smeldr

import (
	"context"
	"testing"
)

// T1247, D191: an approval role reads the place it approves in.

// With only area-approver on the second Area (no reader grant, no editor),
// the holder reads the Check and answers it for its Area.
func TestApprovalRoleRead_AreaApproverAnswersCheck(t *testing.T) {
	f, c := newApprovalCheck(t, second)
	if _, err := f.rs.Grant(context.Background(), RoleGrant{TokenID: "aa-only", RoleName: RoleAreaApprover, ScopeAnchorID: f.a2.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.ChangeCheckOf(human("aa-only"), c.Address); err != nil {
		t.Errorf("the Check is not readable with area-approver alone: %v", err)
	}
	if err := f.app.AnswerChangeCheck(human("aa-only"), c.Address, f.a2.Address, true, ""); err != nil {
		t.Errorf("area-approver alone cannot answer for its Area: %v", err)
	}
}
