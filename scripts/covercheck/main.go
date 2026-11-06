// Command covercheck is core's coverage gate (T1160). It reads a coverage
// profile written by go test -coverprofile, merges each block's repeated
// lines the way go tool cover does (a block counts as covered when any of
// its lines has a count above zero), and prints the exact ratio:
//
//	coverage: 18336/19098 = 96.0100 %
//
// It exits 1 when the ratio is below -min (default 96.0). The total: line of
// go tool cover -func rounds to one decimal, so 95.9996 % reads as 96.0 %
// there; this command does not round before it compares.
//
// Usage, from the root of smeldr/core:
//
//	go test -coverprofile=coverage.out ./...
//	go -C scripts/covercheck run . ../../coverage.out
//
// It is its own module, so go test ./... at the root does not count it.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

func main() {
	min := flag.Float64("min", 96.0, "lowest passing coverage, in percent")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: covercheck [-min 96.0] <coverage profile>")
		os.Exit(2)
	}
	f, err := os.Open(flag.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, "covercheck:", err)
		os.Exit(2)
	}
	defer f.Close()
	covered, total, err := measure(f)
	if err != nil {
		fmt.Fprintln(os.Stderr, "covercheck:", err)
		os.Exit(2)
	}
	pct, ok := verdict(covered, total, *min)
	fmt.Printf("coverage: %d/%d = %.4f %%\n", covered, total, pct)
	if !ok {
		fmt.Printf("covercheck: below the %.1f %% gate\n", *min)
		os.Exit(1)
	}
}

// measure returns the covered and total statement counts of a coverage
// profile. A block that appears on several lines (one per test binary that
// ran it) counts once, covered when any of its lines is.
func measure(r io.Reader) (covered, total int, err error) {
	type block struct {
		stmts   int
		covered bool
	}
	blocks := map[string]*block{}
	var order []string
	sc := bufio.NewScanner(r)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "mode:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 3 {
			return 0, 0, fmt.Errorf("line %d: want \"file:block stmts count\", got %q", n, line)
		}
		stmts, err1 := strconv.Atoi(fields[1])
		count, err2 := strconv.Atoi(fields[2])
		if err1 != nil || err2 != nil {
			return 0, 0, fmt.Errorf("line %d: bad numbers in %q", n, line)
		}
		b, ok := blocks[fields[0]]
		if !ok {
			b = &block{stmts: stmts}
			blocks[fields[0]] = b
			order = append(order, fields[0])
		}
		if count > 0 {
			b.covered = true
		}
	}
	if err := sc.Err(); err != nil {
		return 0, 0, err
	}
	for _, k := range order {
		total += blocks[k].stmts
		if blocks[k].covered {
			covered += blocks[k].stmts
		}
	}
	return covered, total, nil
}

// verdict is the exact percentage and whether it reaches min. It compares
// covered*100 with min*total, so no rounding decides a gate at the edge. An
// empty profile does not pass.
func verdict(covered, total int, min float64) (float64, bool) {
	if total == 0 {
		return 0, false
	}
	pct := 100 * float64(covered) / float64(total)
	return pct, float64(covered)*100 >= min*float64(total)
}
