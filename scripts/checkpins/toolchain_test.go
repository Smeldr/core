package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeMod writes a go.mod with an optional toolchain line into a new dir.
func writeMod(t *testing.T, toolchain string) string {
	t.Helper()
	dir := t.TempDir()
	mod := "module example/x\n\ngo 1.26.5\n"
	if toolchain != "" {
		mod += "\ntoolchain " + toolchain + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(mod), 0o644); err != nil {
		t.Fatal(err)
	}
	return filepath.ToSlash(dir)
}

func TestToolchainProblem(t *testing.T) {
	for _, tc := range []struct {
		got, want, problem string
	}{
		{"", "go1.26.10", "no toolchain line; the root pins go1.26.10"},
		{"go1.26.9", "go1.26.10", "toolchain go1.26.9 is older than the root's go1.26.10"},
		{"go1.26.10", "go1.26.10", ""},
		{"go1.27.0", "go1.26.10", ""},
	} {
		got := toolchainProblem("example/server", tc.got, tc.want)
		if tc.problem == "" && got != "" || tc.problem != "" && !strings.HasSuffix(got, tc.problem) {
			t.Errorf("toolchainProblem(%q, %q) = %q; want %q", tc.got, tc.want, got, tc.problem)
		}
		if tc.problem != "" && !strings.HasPrefix(got, "example/server: ") {
			t.Errorf("the problem does not name the dir: %q", got)
		}
	}
}

func TestCheckToolchains(t *testing.T) {
	root := writeMod(t, "go1.26.10")
	ok, missing, old := writeMod(t, "go1.26.10"), writeMod(t, ""), writeMod(t, "go1.26.9")
	problems, err := checkToolchains(root, []string{ok, missing, old})
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 2 || !strings.HasPrefix(problems[0], missing) || !strings.HasPrefix(problems[1], old) {
		t.Errorf("problems = %q", problems)
	}
	if _, err := checkToolchains(writeMod(t, ""), []string{ok}); err == nil {
		t.Error("a root with no toolchain line must be an error")
	}
	if _, err := checkToolchains("no-such-root", []string{ok}); err == nil {
		t.Error("a missing root must be an error")
	}
	if _, err := checkToolchains(root, []string{"no-such-dir"}); err == nil {
		t.Error("a missing dir must be an error")
	}
	if _, err := toolchainOf("go.mod", []byte("not a go.mod {{{")); err == nil {
		t.Error("a malformed go.mod must be an error")
	}
}

// The real tree: every example module pins a toolchain at least as new as
// the root's.
func TestExamplesMatchTheRoot(t *testing.T) {
	problems, err := checkToolchains("../..", []string{"../../example/api", "../../example/blog", "../../example/docs", "../../example/server"})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range problems {
		t.Error(p)
	}
}
