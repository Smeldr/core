package main

import (
	"strings"
	"testing"
)

func TestMeasure(t *testing.T) {
	profile := `mode: set
a.go:1.1,2.2 3 1
a.go:3.1,4.2 2 0
a.go:3.1,4.2 2 1
b.go:1.1,2.2 5 0

`
	covered, total, err := measure(strings.NewReader(profile))
	if err != nil || covered != 5 || total != 10 {
		t.Errorf("measure = %d/%d, %v; want 5/10", covered, total, err)
	}
	for _, bad := range []string{"a.go:1.1,2.2 3\n", "a.go:1.1,2.2 x 1\n"} {
		if _, _, err := measure(strings.NewReader(bad)); err == nil {
			t.Errorf("measure(%q): no error", bad)
		}
	}
}

func TestVerdict(t *testing.T) {
	for _, tc := range []struct {
		covered, total int
		want           bool
	}{
		{18335, 19098, true},  // 96.0048 %
		{18334, 19098, false}, // 95.9996 %, which go tool cover prints as 96.0 %
		{96, 100, true},
		{0, 0, false},
	} {
		if _, ok := verdict(tc.covered, tc.total, 96.0); ok != tc.want {
			t.Errorf("verdict(%d, %d) = %v, want %v", tc.covered, tc.total, ok, tc.want)
		}
	}
}
