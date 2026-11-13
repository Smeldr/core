package smeldr

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"testing"
)

// T1183: the narrow form of search, on address and external_ref.

// narrowDecisions creates n Decisions and returns them in address order.
func (f searchFixture) narrowDecisions(t testing.TB, n int) []*Decision {
	t.Helper()
	seed := TrustedRead(authorCtx()).(Context)
	out := make([]*Decision, 0, n)
	for i := range n {
		item, err := f.decisions.MCPCreate(seed, map[string]any{"Title": "Bulk " + strconv.Itoa(i), "Scope": "core"})
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, asItem[*Decision](item))
	}
	return out
}

func narrow(t *testing.T, f searchFixture, ctx Context, q SearchQuery) SearchResult {
	t.Helper()
	q.Narrow = true
	res, err := f.app.Search(ctx, q)
	if err != nil {
		t.Fatalf("Search(%+v): %v", q, err)
	}
	return res
}

// TestSearchNarrow_AddressAndReference: one character finds by address
// prefix and by reference, each with its reason; a whole address says so.
func TestSearchNarrow_AddressAndReference(t *testing.T) {
	f := newSearchFixture(t)
	if _, err := f.db.Exec(`UPDATE smeldr_decisions SET external_ref = 'C26 imported' WHERE id = $1`, f.loose.ID); err != nil {
		t.Fatal(err)
	}

	// "d" starts the Decisions' prefix D and the domain type's prefix DOMA.
	res := narrow(t, f, f.global, SearchQuery{Text: "d"})
	if res.Total != 3 || res.AtLeast || res.Next != "" {
		t.Fatalf("d: %+v", res)
	}
	for _, h := range res.Hits {
		if h.MatchReason != SearchMatchAddressPrefix || h.Title == "" {
			t.Errorf("d hit = %+v", h)
		}
	}
	if res.Hits[0].Address != f.placed.Address || res.Hits[1].Address != f.loose.Address || res.Hits[2].Address != f.domain.Address {
		t.Errorf("order: by type, then number: %v", hitIDs(res))
	}
	// With digits, the letters are the whole prefix: D1 is not DOMA1.
	if res := narrow(t, f, f.global, SearchQuery{Text: "D1"}); len(res.Hits) != 1 || res.Hits[0].ID != f.placed.ID {
		t.Errorf("D1: %+v", res.Hits)
	}
	res = narrow(t, f, f.global, SearchQuery{Text: f.loose.Address})
	if len(res.Hits) != 1 || res.Hits[0].MatchReason != SearchMatchAddress || res.Hits[0].ExternalRef != "C26 imported" {
		t.Errorf("whole address: %+v", res.Hits)
	}
	// "c" can be no address of these types (no type has the prefix C), so
	// it is the reference, case-insensitively.
	res = narrow(t, f, f.global, SearchQuery{Text: "c"})
	if len(res.Hits) != 1 || res.Hits[0].ID != f.loose.ID || res.Hits[0].MatchReason != SearchMatchReference {
		t.Errorf("c: %+v", res.Hits)
	}
	if res := narrow(t, f, f.global, SearchQuery{Text: "C26 I"}); len(res.Hits) != 1 {
		t.Errorf("C26 I: %+v", res.Hits)
	}
	// A reference that starts like an address of the same record is listed
	// once, as the address.
	if _, err := f.db.Exec(`UPDATE smeldr_decisions SET external_ref = $1 WHERE id = $2`, f.placed.Address+"-old", f.placed.ID); err != nil {
		t.Fatal(err)
	}
	res = narrow(t, f, f.global, SearchQuery{Text: f.placed.Address})
	if len(res.Hits) != 1 || res.Hits[0].MatchReason != SearchMatchAddress {
		t.Errorf("address and reference: %+v", res.Hits)
	}
	// A leading zero is no address number; nothing else matches.
	if res := narrow(t, f, f.global, SearchQuery{Text: "D0"}); len(res.Hits) != 0 {
		t.Errorf("D0: %+v", res.Hits)
	}
	// Wildcards in a reference are literal.
	if res := narrow(t, f, f.global, SearchQuery{Text: "%"}); len(res.Hits) != 0 {
		t.Errorf("%%: %+v", res.Hits)
	}
}

// TestSearchNarrow_ReadRules: the reader sees only what it reads.
func TestSearchNarrow_ReadRules(t *testing.T) {
	f := newSearchFixture(t)
	res := narrow(t, f, f.reader, SearchQuery{Text: "t"})
	if res.Total != 1 || len(res.Hits) != 1 || res.Hits[0].ID != f.seen.ID {
		t.Errorf("reader: %+v", res)
	}
	if res := narrow(t, f, f.reader, SearchQuery{Text: f.hidden.Address}); res.Total != 0 {
		t.Errorf("hidden by address: %+v", res)
	}
}

// TestSearchNarrow_Ceiling (D183): one letter over more than the ceiling
// reads the ceiling, says at_least, and continues after Next to the end.
func TestSearchNarrow_Ceiling(t *testing.T) {
	f := newSearchFixture(t)
	f.narrowDecisions(t, SearchNarrowCountCeiling+50)
	want := SearchNarrowCountCeiling + 53 // and the fixture's two Decisions and its domain (DOMA1)

	res := narrow(t, f, f.global, SearchQuery{Text: "D", Limit: 500})
	if res.Total != SearchNarrowCountCeiling || !res.AtLeast || res.Next == "" || len(res.Hits) != SearchNarrowCountCeiling {
		t.Fatalf("first: total %d at_least %v next %q hits %d", res.Total, res.AtLeast, res.Next, len(res.Hits))
	}
	seen := map[string]bool{}
	for _, h := range res.Hits {
		seen[h.ID] = true
	}
	res = narrow(t, f, f.global, SearchQuery{Text: "D", Limit: 500, After: res.Next})
	if res.AtLeast || res.Next != "" || res.Total != want-SearchNarrowCountCeiling {
		t.Fatalf("second: total %d at_least %v next %q", res.Total, res.AtLeast, res.Next)
	}
	for _, h := range res.Hits {
		if seen[h.ID] {
			t.Errorf("%s twice", h.Address)
		}
		seen[h.ID] = true
	}
	if len(seen) != want {
		t.Errorf("found %d, want %d", len(seen), want)
	}
}

// TestSearchNarrow_ShortPagesContinue: a reader who reads every third
// Decision gets short pages; each one with a Next is not the end, and
// following Next finds exactly what the reader may read.
func TestSearchNarrow_ShortPagesContinue(t *testing.T) {
	f := newSearchFixture(t)
	all := f.narrowDecisions(t, 2*SearchNarrowCountCeiling+10)
	var static []string
	want := map[string]bool{}
	for i, d := range all {
		if i%3 == 0 {
			static = append(static, "Decision:"+d.ID)
			want[d.ID] = true
		}
	}
	if _, err := f.rs.Grant(context.Background(), RoleGrant{TokenID: "thirds", RoleName: "scoped-reader", ScopeStatic: static}); err != nil {
		t.Fatal(err)
	}
	ctx := NewTestContext(User{ID: "thirds"})
	got := map[string]bool{}
	after, calls := "", 0
	for {
		res := narrow(t, f, ctx, SearchQuery{Text: "D", Limit: 500, After: after})
		calls++
		if res.Next != "" && (!res.AtLeast || len(res.Hits) >= SearchNarrowCountCeiling) {
			t.Errorf("call %d: a cut page is short and says at_least: %+v", calls, res.Total)
		}
		for _, h := range res.Hits {
			got[h.ID] = true
		}
		if res.Next == "" {
			break
		}
		after = res.Next
	}
	if len(got) != len(want) || calls < 3 {
		t.Fatalf("got %d in %d calls, want %d", len(got), calls, len(want))
	}
	for id := range want {
		if !got[id] {
			t.Errorf("missed %s", id)
		}
	}
}

// TestSearchNarrow_Refusals: narrow needs text; after needs narrow; a cursor
// this search did not return is refused.
func TestSearchNarrow_Refusals(t *testing.T) {
	f := newSearchFixture(t)
	for name, q := range map[string]SearchQuery{
		"no text":         {Narrow: true, Types: []string{"Decision"}},
		"after alone":     {Text: "apple", After: "x"},
		"a foreign after": {Text: "D", Narrow: true, After: "not-a-cursor"},
		"a wrong phase":   {Text: "D", Narrow: true, After: narrowKey{Phase: 'x'}.encode()},
		"too long":        {Text: string(make([]byte, SearchTextMaxLength+1)), Narrow: true},
	} {
		if _, err := f.app.Search(f.global, q); !errors.Is(err, ErrBadRequest) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// TestNarrowPlan_Ranges: the number ranges a digit prefix covers.
func TestNarrowPlan_Ranges(t *testing.T) {
	n := &narrowPlan{byAddr: true, counter: map[string]addressCounter{"Decision": {"D", 1450}, "Task": {"T", 9}}}
	for _, tc := range []struct {
		letters, digits, typ string
		want                 string
	}{
		{"D", "14", "Decision", "[[14 14] [140 149] [1400 1450]]"},
		{"D", "", "Decision", "[[1 1450]]"},
		{"", "", "Task", "[[1 9]]"},
		{"D", "2", "Task", "[]"},
		{"D", "9999", "Decision", "[]"},
		{"T", "9", "Task", "[[9 9]]"},
		{"X", "1", "Nope", "[]"},
	} {
		n.letters, n.digits = tc.letters, tc.digits
		got := n.addressRanges(tc.typ)
		s := "["
		for i, r := range got {
			if i > 0 {
				s += " "
			}
			s += "[" + strconv.FormatInt(r[0], 10) + " " + strconv.FormatInt(r[1], 10) + "]"
		}
		if s += "]"; s != tc.want {
			t.Errorf("%s%s on %s = %s, want %s", tc.letters, tc.digits, tc.typ, s, tc.want)
		}
	}
	if prefixUpperBound("c2") != "c3" || prefixUpperBound("a퟿") != "a" || asciiLower("C26-Éa_X") != "c26-Éa_x" || asciiLower("ok") != "ok" {
		t.Error("prefix helpers")
	}
	keys := []narrowKey{{Phase: 'r', Ref: "a", Type: "T", ID: "1"}, {Phase: 'a', Type: "T", No: 2, ID: "x"}, {Phase: 'a', Type: "D", No: 9, ID: "y"}, {Phase: 'a', Type: "T", No: 2, ID: "a"}}
	sort.Slice(keys, func(i, j int) bool { return keys[i].less(keys[j]) })
	if keys[0].Type != "D" || keys[1].ID != "a" || keys[3].Phase != 'r' {
		t.Errorf("order = %+v", keys)
	}
}

// BenchmarkSearchNarrow: one letter over 300 Decisions, on SQLite.
func BenchmarkSearchNarrow(b *testing.B) {
	t := &testing.T{}
	f := newSearchFixture(t)
	f.narrowDecisions(b, 300)
	q := SearchQuery{Text: "D", Narrow: true}
	b.ResetTimer()
	for b.Loop() {
		if _, err := f.app.Search(f.global, q); err != nil {
			b.Fatal(err)
		}
	}
}

// TestSearchNarrow_ReferencesFollowNext: more reference matches than the
// ceiling, over two types, with references that differ in case and hold
// "_", "-", a space and an upper-case non-ASCII letter: following Next
// returns every match exactly once, in one byte order (T1183 review,
// change 1). Only ASCII letters fold: "xÉ" finds the É references, "xé"
// does not.
func TestSearchNarrow_ReferencesFollowNext(t *testing.T) {
	f := newSearchFixture(t)
	decisions := f.narrowDecisions(t, 130)
	seed := TrustedRead(authorCtx()).(Context)
	variants := []string{"X-%d", "x_%d", "x %d", "XÉ-%d", "xa%d", "Xa%d"}
	want := map[string]bool{}
	set := func(table, id, ref string) {
		if _, err := f.db.Exec(`UPDATE `+table+` SET external_ref = $1 WHERE id = $2`, ref, id); err != nil {
			t.Fatal(err)
		}
	}
	for i, d := range decisions {
		set("smeldr_decisions", d.ID, fmt.Sprintf(variants[i%len(variants)], i/len(variants)))
		want[d.ID] = true
	}
	for i := range 130 {
		item, err := f.tasks.MCPCreate(seed, map[string]any{"Title": "Ref task " + strconv.Itoa(i), "Band": "core"})
		if err != nil {
			t.Fatal(err)
		}
		task := asItem[*Task](item)
		// The same references as the Decisions: ties across the two types.
		set("smeldr_tasks", task.ID, fmt.Sprintf(variants[i%len(variants)], i/len(variants)))
		want[task.ID] = true
	}
	set("smeldr_decisions", f.loose.ID, "y-not") // no match
	got := map[string]int{}
	after, calls := "", 0
	var last narrowKey
	for {
		res := narrow(t, f, f.global, SearchQuery{Text: "x", Limit: 500, After: after})
		calls++
		for _, h := range res.Hits {
			if h.MatchReason != SearchMatchReference {
				t.Errorf("%s: reason %q", h.ExternalRef, h.MatchReason)
			}
			k := narrowKey{Phase: 'r', Type: h.Type, Ref: asciiLower(h.ExternalRef), ID: h.ID}
			if k.less(last) {
				t.Errorf("%s/%s out of order after %+v", h.Type, h.ExternalRef, last)
			}
			last = k
			got[h.ID]++
		}
		if res.Next == "" {
			break
		}
		if !res.AtLeast {
			t.Error("a page with next is not marked at_least")
		}
		if calls > 5 {
			t.Fatal("next does not end")
		}
		after = res.Next
	}
	if calls < 2 {
		t.Errorf("%d calls: the matches did not pass the ceiling", calls)
	}
	for id := range want {
		if got[id] != 1 {
			t.Errorf("%s found %d times", id, got[id])
		}
	}
	if len(got) != len(want) {
		t.Errorf("found %d, want %d", len(got), len(want))
	}
	upper := narrow(t, f, f.global, SearchQuery{Text: "xÉ", Limit: 500})
	if upper.Total != 2*22 || upper.AtLeast { // i%6 == 3 for 22 of 130, per type
		t.Errorf("xÉ: total %d at_least %v", upper.Total, upper.AtLeast)
	}
	if lower := narrow(t, f, f.global, SearchQuery{Text: "xé", Limit: 500}); lower.Total != 0 {
		t.Errorf("xé: total %d, want 0 (only ASCII letters fold)", lower.Total)
	}
}

// TestSearchNarrow_ReadFails: a narrow read that fails on one type's table
// is ErrInternal, not a short answer.
func TestSearchNarrow_ReadFails(t *testing.T) {
	f := newSearchFixture(t)
	if _, err := f.db.Exec(`DROP TABLE smeldr_tasks`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.Search(f.global, SearchQuery{Text: "x", Narrow: true}); !errors.Is(err, ErrInternal) {
		t.Errorf("dropped table: %v, want ErrInternal", err)
	}
}
