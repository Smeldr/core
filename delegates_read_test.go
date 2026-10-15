package smeldr

import (
	"context"
	"testing"
)

// A delegated approver or reviewer reads the one item it was delegated, and a
// grant stored under the old, approve-only role picks that up at the next
// boot's Register*Role call, with no migration
// (core-delegated-approvers-read-and-transition).
func TestDelegationRoles_ReadTheirItemAfterBoot(t *testing.T) {
	for _, c := range []struct {
		role     string
		oldOp    string
		register func(context.Context, *RoleStore) error
	}{
		{"item-approver", "approve", RegisterItemApproverRole},
		{"item-reviewer", "review", RegisterItemReviewerRole},
	} {
		t.Run(c.role, func(t *testing.T) {
			f := newScopedFixture(t)
			ctx := context.Background()
			trusted := TrustedRead(authorCtx()).(Context)
			mine, _ := f.tasks.MCPCreate(trusted, map[string]any{"Description": "delegated"})
			other, _ := f.tasks.MCPCreate(trusted, map[string]any{"Description": "other"})
			mineID, otherID := asItem[*Task](mine).ID, asItem[*Task](other).ID

			// The role as it was before: the delegated operation only.
			if err := f.rs.DefineRole(ctx, RoleDefinition{Name: c.role, Operations: []string{c.oldOp}, ScopeMode: ScopeStatic}); err != nil {
				t.Fatal(err)
			}
			if _, err := f.rs.Grant(ctx, RoleGrant{TokenID: "delegate", RoleName: c.role, ScopeStatic: []string{"Task:" + mineID}}); err != nil {
				t.Fatal(err)
			}
			delegate := NewTestContext(User{ID: "delegate"})
			before, err := f.app.ReadScope(delegate)
			if err != nil {
				t.Fatal(err)
			}
			if before.Readable("Task", mineID) {
				t.Fatal("the old role already reads its item")
			}

			// The boot path: the real Register*Role call, no migration.
			if err := c.register(ctx, f.rs); err != nil {
				t.Fatal(err)
			}
			after, err := f.app.ReadScope(delegate)
			if err != nil {
				t.Fatal(err)
			}
			if !after.Readable("Task", mineID) || after.Readable("Task", otherID) || after.All() {
				t.Errorf("after boot: reads its item %v, the other %v, all %v; want only its own item",
					after.Readable("Task", mineID), after.Readable("Task", otherID), after.All())
			}
			if ok, err := f.rs.Authorized(ctx, "delegate", c.oldOp, AuthTarget{TypeName: "Task", ID: mineID}); err != nil || !ok {
				t.Errorf("%s on its item = %v %v; want true", c.oldOp, ok, err)
			}
			if ok, err := f.rs.Authorized(ctx, "delegate", "manage", AuthTarget{TypeName: "Task", ID: mineID}); err != nil || ok {
				t.Errorf("manage on its item = %v %v; want false (nothing else widens)", ok, err)
			}
			if _, err := f.tasks.MCPGet(delegate, asItem[*Task](mine).Slug); err != nil {
				t.Errorf("the delegate cannot read its item: %v", err)
			}
		})
	}
}
