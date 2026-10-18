package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSummarize(t *testing.T) {
	in := strings.Join([]string{
		`{"Action":"run","Package":"p","Test":"TestA"}`,
		`{"Action":"pass","Package":"p","Test":"TestA"}`,
		`{"Action":"fail","Package":"p","Test":"TestB"}`,
		`{"Action":"skip","Package":"p","Test":"TestC"}`,
		`{"Action":"fail","Package":"p"}`,
		`{"Action":"fail","Package":"q"}`,
		`not json`,
	}, "\n")
	c, err := summarize(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if c.passed != 1 || c.failed != 2 || c.skipped != 1 || strings.Join(c.failures, "|") != "p TestB|q (package)" || strings.Join(c.skips, "|") != "p TestC" {
		t.Errorf("counts = %+v", c)
	}
}

func TestVerdict(t *testing.T) {
	for _, tc := range []struct {
		c    counts
		want string
	}{
		{counts{passed: 3}, ""},
		{counts{passed: 3, failed: 1}, "1 failed"},
		{counts{passed: 3, skipped: 2}, "2 skipped"},
		{counts{}, "no test ran"},
	} {
		err := verdict(tc.c)
		if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
			t.Errorf("verdict(%+v) = %v, want %q", tc.c, err, tc.want)
		}
	}
}

func TestLineAndDSN(t *testing.T) {
	got := line(counts{passed: 5}, "postgres://u:secret@db:5432/test?sslmode=disable")
	if got != "pgx integration: 5 passed, 0 failed, 0 skipped against db:5432/test" || strings.Contains(got, "secret") {
		t.Errorf("line = %q", got)
	}
	if dsnLabel("::") != "an unparsed DATABASE_URL" {
		t.Error("an unparsable DSN was labelled")
	}
}

// No DATABASE_URL is exit 2 and runs nothing.
func TestRun_NoDSN(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run(nil, "", &out, &errOut); code != 2 || !strings.Contains(errOut.String(), "DATABASE_URL") {
		t.Errorf("exit %d, stderr %q", code, errOut.String())
	}
}

// keepFiles puts files back as they were, and removes one that did not exist.
func TestKeepFiles(t *testing.T) {
	dir := t.TempDir()
	mod := filepath.Join(dir, "go.mod")
	if err := os.WriteFile(mod, []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sum := filepath.Join(dir, "go.sum")
	restore, err := keepFiles(mod, sum)
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(mod, []byte("changed\n"), 0o644)
	_ = os.WriteFile(sum, []byte("new\n"), 0o644)
	restore()
	if b, _ := os.ReadFile(mod); string(b) != "module x\n" {
		t.Errorf("go.mod = %q", b)
	}
	if _, err := os.Stat(sum); !os.IsNotExist(err) {
		t.Error("a file that did not exist was kept")
	}
}

// A run in a directory that is not a module fails without a count.
func TestRun_BadDir(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{t.TempDir()}, "postgres://x@h/d", &out, &errOut); code != 1 {
		t.Errorf("exit %d, stderr %q", code, errOut.String())
	}
}
