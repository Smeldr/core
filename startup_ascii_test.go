// AGPL-3.0-or-later

package smeldr

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// logCall matches the start of a log or startup-output call: the lines a
// person reads in a terminal, where a non-ASCII character can show as
// mojibake on a console with a legacy code page (T1000).
var logCall = regexp.MustCompile(`slog\.(Info|Warn|Error|Debug)\(|log\.(Printf|Println|Print|Fatalf|Fatal|Fatalln)\(|Fprint(f|ln)?\(os\.Std(err|out)`)

// Startup and log lines in core and example/server are plain ASCII (T1000).
// The check is per line: a call whose message continues on the next line is
// not covered.
func TestStartupLogLinesAreASCII(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	server, err := filepath.Glob(filepath.Join("example", "server", "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range append(files, server...) {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			if !logCall.MatchString(line) {
				continue
			}
			for _, r := range line {
				if r > 127 {
					t.Errorf("%s:%d: non-ASCII %q in a log line: %s", f, i+1, r, strings.TrimSpace(line))
					break
				}
			}
		}
	}
}
