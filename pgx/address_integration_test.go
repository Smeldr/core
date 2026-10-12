//go:build integration

package pgx

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	smeldr "smeldr.dev/core"
)

// N concurrent Signals on Postgres get N distinct numbers 1..N (the counter's
// row lock serializes the allocation), and a refused insert takes no number.
func TestPG_Address_ConcurrentAndNoGap(t *testing.T) {
	db, _ := isolatedDB(t)
	ctx := context.Background()
	if err := smeldr.CreateOrchestrationTables(db); err != nil {
		t.Fatal(err)
	}
	const n = 16
	var wg sync.WaitGroup
	nums := make(chan int, n)
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s := &smeldr.Signal{Sender: "core", SignalType: "plan-ready"}
			if err := smeldr.InsertSignal(ctx, db, s); err != nil {
				t.Errorf("InsertSignal: %v", err)
				return
			}
			no, _ := strconv.Atoi(strings.TrimPrefix(s.Address, "S"))
			nums <- no
		}()
	}
	wg.Wait()
	close(nums)
	var got []int
	for v := range nums {
		got = append(got, v)
	}
	sort.Ints(got)
	for i, v := range got {
		if v != i+1 {
			t.Fatalf("numbers = %v, want 1..%d with no repeat or gap", got, n)
		}
	}

	refuseInsertsOf(t, db, "smeldr_signals", "signal_type", "refused")
	if err := smeldr.InsertSignal(ctx, db, &smeldr.Signal{Sender: "core", SignalType: "refused"}); err == nil {
		t.Fatal("want the refused insert to fail")
	}
	next := &smeldr.Signal{Sender: "core", SignalType: "plan-ready"}
	if err := smeldr.InsertSignal(ctx, db, next); err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprintf("S%d", n+1); next.Address != want {
		t.Errorf("after a refused insert: %s, want %s (no gap)", next.Address, want)
	}
}

// A dynamic record type on Postgres: concurrent creates get distinct addresses,
// and the slug retry in a new transaction does not skip a number.
func TestPG_Address_DynamicRecords(t *testing.T) {
	db, _ := isolatedDB(t)
	app, _ := dynamicApp(t, db, nil)
	ctx := context.Background()
	if _, err := app.DefineContentType(ctx, &smeldr.ContentTypeSchema{TypeName: "invoice",
		Fields: json.RawMessage(`[{"Name":"Title","Type":"string","Role":"title"}]`)}); err != nil {
		t.Fatal(err)
	}
	repo, err := app.DynamicContentRepo("invoice")
	if err != nil {
		t.Fatal(err)
	}
	const n = 8
	var wg sync.WaitGroup
	addrs := make(chan string, n)
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			node, err := repo.CreateDraft(ctx, map[string]any{"Title": "same title"})
			if err != nil {
				t.Errorf("CreateDraft: %v", err)
				return
			}
			addrs <- node.Address
		}()
	}
	wg.Wait()
	close(addrs)
	seen := map[string]bool{}
	for a := range addrs {
		if seen[a] {
			t.Errorf("address %s given twice", a)
		}
		seen[a] = true
	}
	for i := 1; i <= n; i++ {
		if !seen[fmt.Sprintf("INV%d", i)] {
			t.Errorf("INV%d missing: got %v", i, seen)
		}
	}
}
