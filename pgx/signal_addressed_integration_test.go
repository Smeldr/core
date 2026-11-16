//go:build integration

package pgx

import (
	"context"
	"slices"
	"testing"
	"time"

	smeldr "smeldr.dev/core"
)

// T1308 on Postgres: App.SignalsAddressedTo's one query, the state and the
// receiver names as numbered placeholders, and the order.
func TestPG_SignalsAddressedTo(t *testing.T) {
	db, _ := isolatedDB(t)
	ctx := context.Background()
	if err := smeldr.CreateOrchestrationTables(db); err != nil {
		t.Fatal(err)
	}
	if err := smeldr.EnsureOrchestrationSignalColumns(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := smeldr.CreateSubscriptionTables(db); err != nil {
		t.Fatal(err)
	}
	app := smeldr.New(smeldr.Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16"), DB: db})
	smeldr.RegisterOrchestrationTypes(app, db)
	_ = app.Handler()
	u1 := smeldr.NewTestContext(smeldr.User{ID: "u1", Roles: []smeldr.Role{smeldr.Author}})
	if _, err := app.CreateSubscription(u1, smeldr.SubscriptionSpec{Name: "band",
		Filter: smeldr.SubscriptionFilter{Fields: map[string][]string{"receiver": {"core"}}}}); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	for i, r := range [][3]string{
		{"to-me", "u1", "open"}, {"to-band", "core", "open"}, {"to-band-history", "core", "pending"},
		{"to-other", "site", "open"}, {"answered", "u1", "answered"},
	} {
		if _, err := db.ExecContext(ctx, `INSERT INTO smeldr_signals (id, slug, status, created_at, updated_at, receiver, signal_type) VALUES ($1, $1, $2, $3, $3, $4, 'question')`,
			r[0], r[2], base.Add(time.Duration(i)*time.Minute), r[1]); err != nil {
			t.Fatal(err)
		}
	}
	ids := func(state string, limit, offset int) ([]string, int) {
		got, total, err := app.SignalsAddressedTo(u1, state, limit, offset)
		if err != nil {
			t.Fatalf("SignalsAddressedTo(%q): %v", state, err)
		}
		out := []string{}
		for _, s := range got {
			out = append(out, s.ID)
		}
		return out, total
	}
	if got, total := ids("", 0, 0); !slices.Equal(got, []string{"to-band-history", "to-band", "to-me"}) || total != 3 {
		t.Errorf("awaiting action: %v (total %d)", got, total)
	}
	if got, total := ids("", 1, 1); !slices.Equal(got, []string{"to-band"}) || total != 3 {
		t.Errorf("page: %v (total %d)", got, total)
	}
	if got, total := ids("answered", 0, 0); !slices.Equal(got, []string{"answered"}) || total != 1 {
		t.Errorf("answered: %v (total %d)", got, total)
	}
}
