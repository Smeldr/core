// Command pgxcheck runs core's Postgres integration tests for real and prints
// one line a commit review request or a release can quote (T890):
//
//	pgx integration: 312 passed, 0 failed, 0 skipped against localhost:5432/forgetest
//
// It sets a temporary replace of smeldr.dev/core to the local checkout in
// pgx/go.mod, runs `go test -tags integration -count=1 -json ./...` in pgx,
// and restores go.mod and go.sum whatever happens. It never starts a database.
//
// Usage, from scripts/pgxcheck:
//
//	DATABASE_URL=postgres://forge:forge@localhost:5432/forgetest?sslmode=disable go run . [../../pgx]
//
// It exits non-zero when DATABASE_URL is unset, when no test ran, or when a
// test or package failed or a test was skipped: none of those is a real run.
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
)

// childOnlyTest is the one integration test that is a helper process, not a
// test of its own: run directly it skips.
const childOnlyTest = "TestPG_RaceChild"

// counts is the outcome of one go test -json run.
type counts struct {
	passed, failed, skipped int
	// failures are the failed tests ("pkg TestName") and packages ("pkg"),
	// skips the skipped tests.
	failures, skips []string
}

// testEvent is the part of a go test -json line pgxcheck reads.
type testEvent struct {
	Action  string `json:"Action"`
	Package string `json:"Package"`
	Test    string `json:"Test"`
}

// summarize counts the test-level outcomes of a go test -json stream. A
// package that fails without a failing test (a build failure) is a failure
// too. Lines that are not JSON are ignored.
func summarize(r io.Reader) (counts, error) {
	var c counts
	failedTests := map[string]bool{}
	var failedPkgs []string
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for sc.Scan() {
		var ev testEvent
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		if ev.Test == "" {
			if ev.Action == "fail" {
				failedPkgs = append(failedPkgs, ev.Package)
			}
			continue
		}
		switch ev.Action {
		case "pass":
			c.passed++
		case "fail":
			c.failed++
			failedTests[ev.Package] = true
			c.failures = append(c.failures, ev.Package+" "+ev.Test)
		case "skip":
			c.skipped++
			c.skips = append(c.skips, ev.Package+" "+ev.Test)
		}
	}
	for _, p := range failedPkgs {
		if !failedTests[p] {
			c.failed++
			c.failures = append(c.failures, p+" (package)")
		}
	}
	sort.Strings(c.failures)
	sort.Strings(c.skips)
	return c, sc.Err()
}

// verdict is the error a run's counts give, or nil for a real, green run.
func verdict(c counts) error {
	switch {
	case c.failed > 0:
		return fmt.Errorf("%d failed", c.failed)
	case c.skipped > 0:
		return fmt.Errorf("%d skipped: a skipped integration test is not a run", c.skipped)
	case c.passed == 0:
		return errors.New("no test ran")
	}
	return nil
}

// dsnLabel is the host and database of dsn, without credentials.
func dsnLabel(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil || u.Host == "" {
		return "an unparsed DATABASE_URL"
	}
	return u.Host + u.Path
}

// line is the summary line.
func line(c counts, dsn string) string {
	return fmt.Sprintf("pgx integration: %d passed, %d failed, %d skipped against %s", c.passed, c.failed, c.skipped, dsnLabel(dsn))
}

func main() {
	os.Exit(run(os.Args[1:], os.Getenv("DATABASE_URL"), os.Stdout, os.Stderr))
}

// run is main without the process exit, for tests.
func run(args []string, dsn string, stdout, stderr io.Writer) int {
	if dsn == "" {
		fmt.Fprintln(stderr, "pgxcheck: DATABASE_URL is not set; no database, no run (start the smeldr-pgtest container, then set it)")
		return 2
	}
	dir := "../../pgx"
	if len(args) > 0 {
		dir = args[0]
	}
	c, err := runSuite(dir, dsn, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "pgxcheck: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, line(c, dsn))
	for _, f := range c.failures {
		fmt.Fprintln(stdout, "  failed: "+f)
	}
	for _, s := range c.skips {
		fmt.Fprintln(stdout, "  skipped: "+s)
	}
	if err := verdict(c); err != nil {
		fmt.Fprintf(stderr, "pgxcheck: %v\n", err)
		return 1
	}
	return 0
}

// runSuite replaces smeldr.dev/core with the checkout above dir, runs the
// tagged suite in dir and restores go.mod and go.sum.
func runSuite(dir, dsn string, stderr io.Writer) (counts, error) {
	restore, err := keepFiles(filepath.Join(dir, "go.mod"), filepath.Join(dir, "go.sum"))
	if err != nil {
		return counts{}, err
	}
	defer restore()
	edit := exec.Command("go", "mod", "edit", "-replace", "smeldr.dev/core=../")
	edit.Dir, edit.Stderr = dir, stderr
	if err := edit.Run(); err != nil {
		return counts{}, fmt.Errorf("replace smeldr.dev/core: %w", err)
	}
	// TestPG_RaceChild only runs as the child process of
	// TestPG_ConflictPolicy_TwoProcesses and skips otherwise; it is left out
	// here so that every skip that remains is a real one.
	test := exec.Command("go", "test", "-tags", "integration", "-count=1", "-json", "-skip", "^"+childOnlyTest+"$", "./...")
	test.Dir, test.Stderr = dir, stderr
	test.Env = append(os.Environ(), "DATABASE_URL="+dsn, "GOWORK=off")
	out, err := test.StdoutPipe()
	if err != nil {
		return counts{}, err
	}
	if err := test.Start(); err != nil {
		return counts{}, fmt.Errorf("go test: %w", err)
	}
	c, sumErr := summarize(out)
	_ = test.Wait() // a failing suite exits non-zero; the counts say why
	return c, sumErr
}

// keepFiles reads paths and returns a func that writes them back as they
// were (a file that did not exist is removed).
func keepFiles(paths ...string) (func(), error) {
	saved := map[string][]byte{}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if errors.Is(err, os.ErrNotExist) {
			saved[p] = nil
			continue
		}
		if err != nil {
			return nil, err
		}
		saved[p] = b
	}
	return func() {
		for p, b := range saved {
			if b == nil {
				_ = os.Remove(p)
				continue
			}
			_ = os.WriteFile(p, b, 0o644)
		}
	}, nil
}
