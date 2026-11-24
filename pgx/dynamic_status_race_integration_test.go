//go:build integration

package pgx

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	smeldr "smeldr.dev/core"
)

// T1330 on Postgres: two moves of one runtime-defined item from draft at the
// same moment (to published and to archived) never both write over each
// other. Thirty rounds on real parallel connections: every refusal is
// transition.state_moved, and the item ends in the state of a move that was
// not refused. Core's deterministic test proves the order; this one runs the
// same SQL under load.
func TestPG_DynamicSetStatus_ConcurrentMoves(t *testing.T) {
	db, _ := isolatedDB(t)
	ctx := context.Background()
	if err := smeldr.CreateBlockTables(db); err != nil {
		t.Fatal(err)
	}
	if err := smeldr.CreateSchemaTable(db); err != nil {
		t.Fatal(err)
	}
	app := smeldr.New(smeldr.Config{BaseURL: "https://example.com", Secret: []byte("supersecretkey16"), DB: db})
	app.ServeDynamicContent()
	if _, err := app.DefineContentType(ctx, &smeldr.ContentTypeSchema{TypeName: "race_item",
		Fields: json.RawMessage(`[{"name":"title","type":"string","role":"title"}]`)}); err != nil {
		t.Fatal(err)
	}
	repo, err := app.DynamicContentRepo("race_item")
	if err != nil {
		t.Fatal(err)
	}
	targets := []smeldr.Status{smeldr.Published, smeldr.Archived}
	refused := 0
	for round := 0; round < 30; round++ {
		node, err := repo.CreateDraft(ctx, map[string]any{"title": fmt.Sprintf("race %d", round)})
		if err != nil {
			t.Fatal(err)
		}
		errs := make([]error, len(targets))
		var wg sync.WaitGroup
		for i, to := range targets {
			wg.Add(1)
			go func(i int, to smeldr.Status) {
				defer wg.Done()
				errs[i] = repo.SetStatus(ctx, node.ID, to)
			}(i, to)
		}
		wg.Wait()
		var status string
		if err := db.QueryRowContext(ctx, `SELECT status FROM smeldr_dynamic_content WHERE id = $1`, node.ID).Scan(&status); err != nil {
			t.Fatal(err)
		}
		var won []string
		for i, err := range errs {
			switch {
			case err == nil:
				won = append(won, string(targets[i]))
			case smeldr.ProblemTypeOf(err) == "transition.state_moved":
				refused++
			default:
				t.Fatalf("round %d, move to %s: %v", round, targets[i], err)
			}
		}
		// One refused: the item is in the other's state. None refused: the
		// two ran one after the other, and the item is in one of them.
		if len(won) == 1 && status != won[0] || len(won) == 0 {
			t.Fatalf("round %d: status %s, moves that went through %v", round, status, won)
		}
	}
	t.Logf("%d of 30 rounds had a move refused with transition.state_moved", refused)
}
