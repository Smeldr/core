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

// T1211: a message a caller receives (an error, a refusal, a warning) names
// no record by its address. "D142" is a record on smeldr.dev's own instance;
// on a self-hoster's instance it is another record or none. The design record
// behind a refusal is ProblemTypeInfo.Rule, documented as such; the message
// says what is refused and what to do instead.

// recordAddress matches an address of the kinds a message used to cite:
// D12, T884, A398, G12, S3893, P401.
var recordAddress = regexp.MustCompile(`\b[DTAGSP][0-9]{2,4}\b`)

// messageCalls are the calls whose string arguments become a message a
// caller receives.
var messageCalls = map[string]bool{
	"fmt.Errorf": true, "fmt.Sprintf": true, "errors.New": true,
	"refuse": true, "Refused": true, "fieldErr": true, "problem": true,
}

// messageAddressAllowed lists literals the pattern matches that are not an
// address. Keep it short: a new entry needs a reason next to it.
var messageAddressAllowed = map[string]string{}

func TestMessagesNameNoRecordAddresses(t *testing.T) {
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	seen := map[token.Pos]bool{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		check := func(n ast.Node) {
			ast.Inspect(n, func(m ast.Node) bool {
				lit, ok := m.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING || seen[lit.Pos()] {
					return true
				}
				seen[lit.Pos()] = true
				s, err := strconv.Unquote(lit.Value)
				if err != nil {
					return true
				}
				checked++
				if a := recordAddress.FindString(s); a != "" {
					if _, ok := messageAddressAllowed[s]; !ok {
						t.Errorf("%s: a message names %s: %q", fset.Position(lit.Pos()), a, s)
					}
				}
				return true
			})
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.CallExpr:
				if messageCalls[callName(x.Fun)] {
					for _, arg := range x.Args {
						check(arg)
					}
				}
			case *ast.KeyValueExpr:
				if k, ok := x.Key.(*ast.Ident); ok && (k.Name == "Message" || k.Name == "Reason") {
					if _, isProvenance := provenanceReasonParent(f, x); !isProvenance {
						check(x.Value)
					}
				}
			}
			return true
		})
	}
	if checked < 100 {
		t.Fatalf("checked only %d message literals; the scan is not reading the calls", checked)
	}
}

// callName is "pkg.Func" or "Func" for a call's function expression.
func callName(fun ast.Expr) string {
	switch f := fun.(type) {
	case *ast.Ident:
		return f.Name
	case *ast.SelectorExpr:
		if x, ok := f.X.(*ast.Ident); ok {
			return x.Name + "." + f.Sel.Name
		}
	}
	return ""
}

// provenanceReasonParent reports whether kv is the Reason of a
// ProvenanceRecord literal: a provenance reason is history, not a message,
// and is outside T1211 (the follow-up Task covers it).
func provenanceReasonParent(f *ast.File, kv *ast.KeyValueExpr) (*ast.CompositeLit, bool) {
	var found *ast.CompositeLit
	ast.Inspect(f, func(n ast.Node) bool {
		cl, ok := n.(*ast.CompositeLit)
		if !ok || found != nil {
			return found == nil
		}
		for _, el := range cl.Elts {
			if el == kv {
				found = cl
				return false
			}
		}
		return true
	})
	if found == nil {
		return nil, false
	}
	id, ok := found.Type.(*ast.Ident)
	return found, ok && id.Name == "ProvenanceRecord"
}
