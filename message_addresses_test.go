package smeldr

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// T1211, widened by T1222: no text this package writes names a record by its
// address. "D142" is a record on smeldr.dev's own instance; on a
// self-hoster's instance it is another record or none. T1211 read the
// messages a caller receives; T1222 reads every string literal in the
// package's non-test files: messages, field and flow descriptions, Signal
// kind descriptions, provenance reasons and problem type titles. The design
// record behind a refusal is ProblemTypeInfo.Rule, documented as such.

// recordAddress matches an address of the kinds a text used to cite:
// D12, T884, A398, G12, S3893, P401.
var recordAddress = regexp.MustCompile(`\b[DTAGSP][0-9]{2,4}\b`)

// textAddressAllowed lists the literals that keep an address, each with its
// reason. Keep it short: a new entry needs a reason and is named in review.
var textAddressAllowed = map[string]string{
	"D110": "the deprecated-field rule id on the wire, decided in P413",
	"smeldr: a deprecated input was sent (D110); it is removed at the cut":                                               "a log line: an operator reads our logs",
	"smeldr: migrateApprovalRoles: approval roles moved to D142":                                                         "a log line: an operator reads our logs",
	"smeldr: a record was written without a valid title (D112); refused after the title cutover":                         "a log line: an operator reads our logs",
	"smeldr: ExpireSignals is retired (D115): nothing expires; use App.OpenSignalsOlderThan to surface old open Signals": "a log line: an operator reads our logs",
}

func TestTextsNameNoRecordAddresses(t *testing.T) {
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	used := map[string]bool{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			// A problem type's Rule is Smeldr's own design record, kept on
			// the wire and in the docs as such (Peter, 13 November 2026).
			if kv, ok := n.(*ast.KeyValueExpr); ok {
				if k, ok := kv.Key.(*ast.Ident); ok && k.Name == "Rule" {
					return false
				}
			}
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			s, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			checked++
			if a := recordAddress.FindString(s); a != "" {
				if _, ok := textAddressAllowed[s]; ok {
					used[s] = true
				} else {
					t.Errorf("%s: a text names %s: %q", fset.Position(lit.Pos()), a, s)
				}
			}
			return true
		})
	}
	if checked < 1000 {
		t.Fatalf("checked only %d string literals; the scan is not reading the files", checked)
	}
	for s := range textAddressAllowed {
		if !used[s] {
			t.Errorf("allow-list entry no longer found, remove it: %q", s)
		}
	}
}
