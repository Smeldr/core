package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// envReaders are the calls whose first argument names an environment
// variable the server reads.
var envReaders = map[string]bool{"Getenv": true, "LookupEnv": true, "envOr": true, "requireEnv": true, "getenv": true}

// readEnvNames parses the server's non-test Go files and returns every
// variable name read through envReaders, and every call whose name is not a
// string literal (outside envOr and requireEnv, which pass their key on).
func readEnvNames(t *testing.T) (names map[string]bool, computed []string, doc string) {
	t.Helper()
	fset := token.NewFileSet()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	names = map[string]bool{}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		if path == "main.go" && f.Doc != nil {
			doc = f.Doc.Text()
		}
		for _, decl := range f.Decls {
			fn, _ := decl.(*ast.FuncDecl)
			passesKeyOn := fn != nil && (fn.Name.Name == "envOr" || fn.Name.Name == "requireEnv")
			ast.Inspect(decl, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || len(call.Args) == 0 {
					return true
				}
				var name string
				switch f := call.Fun.(type) {
				case *ast.Ident:
					name = f.Name
				case *ast.SelectorExpr:
					if x, ok := f.X.(*ast.Ident); ok && x.Name == "os" {
						name = f.Sel.Name
					}
				}
				if !envReaders[name] {
					return true
				}
				lit, ok := call.Args[0].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					if !passesKeyOn {
						computed = append(computed, fset.Position(call.Pos()).String())
					}
					return true
				}
				v, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatal(err)
				}
				names[v] = true
				return true
			})
		}
	}
	return names, computed, doc
}

// Every environment variable the server reads is listed in main.go's doc
// comment, docs/SELF_HOSTING.md and AGENTS.md (T1310).
func TestEnvironmentVariablesListed(t *testing.T) {
	names, computed, doc := readEnvNames(t)
	if len(computed) > 0 {
		t.Errorf("an environment variable read by a computed name cannot be checked; use a literal: %v", computed)
	}
	if len(names) < 50 {
		t.Fatalf("found %d environment variables, want at least 50: the reads moved where this test does not look", len(names))
	}
	read := func(rel string) string {
		b, err := os.ReadFile(filepath.FromSlash(rel))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	places := []struct {
		name, text, form string
	}{
		{"main.go's doc comment", doc, "%s"},
		{"docs/SELF_HOSTING.md", read("../../docs/SELF_HOSTING.md"), "%s"},
		{"AGENTS.md", read("../../AGENTS.md"), "`%s`"},
	}
	sorted := make([]string, 0, len(names))
	for n := range names {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)
	var missing []string
	for _, p := range places {
		for _, n := range sorted {
			want := strings.Replace(p.form, "%s", n, 1)
			if !containsWord(p.text, want) {
				missing = append(missing, n+" (missing from "+p.name+")")
			}
		}
	}
	if len(missing) > 0 {
		t.Errorf("%d environment variables are read but not listed:\n  %s", len(missing), strings.Join(missing, "\n  "))
	}
}

// containsWord reports whether s holds w not as part of a longer name, so
// ENABLE_CHECK is not found inside ENABLE_CHECKS.
func containsWord(s, w string) bool {
	nameChar := func(b byte) bool { return b == '_' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' }
	for i := 0; ; {
		j := strings.Index(s[i:], w)
		if j < 0 {
			return false
		}
		j += i
		end := j + len(w)
		if (j == 0 || !nameChar(s[j-1])) && (end == len(s) || !nameChar(s[end])) {
			return true
		}
		i = j + 1
	}
}
