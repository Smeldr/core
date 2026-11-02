package smeldr

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// untypedSentinels are the 4xx sentinels a refusal site must give a type
// (T1039). ErrRevConflict and ErrLastAdmin are not here: each means one type,
// which ProblemTypeOf gives them.
var untypedSentinels = map[string]bool{
	"ErrForbidden": true, "ErrConflict": true, "ErrNotFound": true, "ErrGone": true, "ErrUnauth": true,
	"ErrBadRequest": true, "ErrNotAcceptable": true, "ErrRequestTooLarge": true, "ErrTooManyRequests": true,
}

// typedConstructors are the calls that name a refusal type as their first
// argument.
var typedConstructors = map[string]bool{"problem": true, "refuse": true, "fieldErr": true}

// refusalSite is one finding of the site check.
type refusalSite struct {
	pos  token.Position
	what string
}

// scanRefusalSites parses the package's non-test files and returns, per
// file, the refusal sites that name no type, and the type arguments that are
// not a constant from the closed list. It also returns how often each list
// constant is used outside problem_types.go's own table.
func scanRefusalSites(t *testing.T) (untyped map[string][]refusalSite, badTypes []refusalSite, used map[string]int) {
	t.Helper()
	fset := token.NewFileSet()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	consts := problemTypeConstNames()
	untyped = map[string][]refusalSite{}
	used = map[string]int{}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		var stack []ast.Node
		ast.Inspect(f, func(n ast.Node) bool {
			if n == nil {
				stack = stack[:len(stack)-1]
				return false
			}
			// The table's own ID: entries are not uses. Inspect skips the
			// children and sends no nil for a node it is told to skip.
			if kv, ok := n.(*ast.KeyValueExpr); ok && name == "problem_types.go" {
				if k, ok := kv.Key.(*ast.Ident); ok && k.Name == "ID" {
					return false
				}
			}
			defer func() { stack = append(stack, n) }()
			switch n := n.(type) {
			case *ast.Ident:
				if consts[n.Name] && !isConstDecl(stack) {
					used[n.Name]++
				}
				if name == "errors.go" || name == "problem_types.go" || !untypedSentinels[n.Name] {
					return true
				}
				if !sentinelAllowed(stack) {
					untyped[name] = append(untyped[name], refusalSite{fset.Position(n.Pos()), n.Name})
				}
			case *ast.CompositeLit:
				// A fieldError built by hand (node.go's struct-tag checks)
				// must set its type.
				if id, ok := n.Type.(*ast.Ident); ok && id.Name == "fieldError" && name != "errors.go" && name != "problem_types.go" {
					if !hasKey(n, "typ") {
						untyped[name] = append(untyped[name], refusalSite{fset.Position(n.Pos()), "fieldError{"})
					} else if msg := checkFieldErrorValues(n); msg != "" {
						badTypes = append(badTypes, refusalSite{fset.Position(n.Pos()), "fieldError: " + msg})
					}
				}
			case *ast.CallExpr:
				fn, ok := n.Fun.(*ast.Ident)
				if !ok {
					return true
				}
				if (fn.Name == "Refused" || fn.Name == "Err") && name != "errors.go" && name != "problem_types.go" {
					untyped[name] = append(untyped[name], refusalSite{fset.Position(n.Pos()), fn.Name + "("})
				}
				if typedConstructors[fn.Name] && name != "problem_types.go" {
					var arg *ast.Ident
					if len(n.Args) > 0 {
						arg, _ = n.Args[0].(*ast.Ident)
					}
					if arg == nil || !consts[arg.Name] {
						badTypes = append(badTypes, refusalSite{fset.Position(n.Pos()), fn.Name + " without a listed type constant"})
					} else if msg := checkValuesArg(arg.Name, n.Args[len(n.Args)-1]); msg != "" {
						badTypes = append(badTypes, refusalSite{fset.Position(n.Pos()), fn.Name + ": " + msg})
					}
				}
			}
			return true
		})
	}
	return untyped, badTypes, used
}

// sentinelAllowed reports whether a sentinel reference, whose ancestors are
// stack (nearest last), is not a refusal site: inside a typed constructor's
// arguments, a comparison, an errors.Is, a case list, or a method call on the
// sentinel itself (its text, not a refusal).
func sentinelAllowed(stack []ast.Node) bool {
	parent := stack[len(stack)-1]
	switch p := parent.(type) {
	case *ast.BinaryExpr:
		if p.Op == token.EQL || p.Op == token.NEQ {
			return true
		}
	case *ast.SelectorExpr:
		return true
	case *ast.CaseClause:
		return true
	}
	for i := len(stack) - 1; i >= 0; i-- {
		call, ok := stack[i].(*ast.CallExpr)
		if !ok {
			continue
		}
		if fn, ok := call.Fun.(*ast.Ident); ok && typedConstructors[fn.Name] {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
			if x, ok := sel.X.(*ast.Ident); ok && x.Name == "errors" && sel.Sel.Name == "Is" {
				return true
			}
		}
	}
	return false
}

// declaredValues maps each list constant's name to the value names its type
// declares.
var declaredValues = func() map[string][]string {
	m := map[string][]string{}
	for _, p := range problemTypeList {
		m[constNameOf(p.ID)] = p.Values
	}
	return m
}()

// checkValuesArg checks a typed call's values argument against the names
// constName's type declares: nil, or a problemValues literal whose keys are
// string literals, each one the type declares and none twice. A type's names
// are the union over its sites, so a site leaves out a name its message does
// not state. It returns what is wrong, or "".
func checkValuesArg(constName string, arg ast.Expr) string {
	want := declaredValues[constName]
	if id, ok := arg.(*ast.Ident); ok && id.Name == "nil" {
		return ""
	}
	lit, ok := arg.(*ast.CompositeLit)
	if !ok {
		return "values must be nil or a problemValues literal"
	}
	if id, ok := lit.Type.(*ast.Ident); !ok || id.Name != "problemValues" {
		return "values must be nil or a problemValues literal"
	}
	got := map[string]bool{}
	for _, e := range lit.Elts {
		kv, ok := e.(*ast.KeyValueExpr)
		if !ok {
			return "values entry is not key: value"
		}
		k, ok := kv.Key.(*ast.BasicLit)
		if !ok || k.Kind != token.STRING {
			return "a values key must be a string literal"
		}
		name, err := strconv.Unquote(k.Value)
		if err != nil {
			return "a values key must be a string literal"
		}
		if got[name] {
			return fmt.Sprintf("values key %q twice", name)
		}
		got[name] = true
	}
	for _, w := range want {
		delete(got, w)
	}
	for k := range got {
		return fmt.Sprintf("values key %q is not one the type declares (%v)", k, want)
	}
	return ""
}

// checkFieldErrorValues checks a typed fieldError literal's values key the
// way checkValuesArg checks a call's; an absent key counts as nil.
func checkFieldErrorValues(lit *ast.CompositeLit) string {
	var typ string
	var values ast.Expr = ast.NewIdent("nil")
	for _, e := range lit.Elts {
		kv, ok := e.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		k, _ := kv.Key.(*ast.Ident)
		switch {
		case k == nil:
		case k.Name == "typ":
			if id, ok := kv.Value.(*ast.Ident); ok {
				typ = id.Name
			}
		case k.Name == "values":
			values = kv.Value
		}
	}
	if _, ok := declaredValues[typ]; !ok {
		return "typ is not a listed type constant"
	}
	return checkValuesArg(typ, values)
}

// hasKey reports whether the composite literal sets the field key.
func hasKey(lit *ast.CompositeLit, key string) bool {
	for _, e := range lit.Elts {
		if kv, ok := e.(*ast.KeyValueExpr); ok {
			if k, ok := kv.Key.(*ast.Ident); ok && k.Name == key {
				return true
			}
		}
	}
	return false
}

// isConstDecl reports whether the identifier on top of stack is being
// declared (the const block), not used.
func isConstDecl(stack []ast.Node) bool {
	_, ok := stack[len(stack)-1].(*ast.ValueSpec)
	return ok
}

// problemTypeConstNames is the set of the closed list's constant names, read
// from problem_types.go's const block.
func problemTypeConstNames() map[string]bool {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "problem_types.go", nil, 0)
	if err != nil {
		panic(err)
	}
	names := map[string]bool{}
	for _, d := range f.Decls {
		g, ok := d.(*ast.GenDecl)
		if !ok || g.Tok != token.CONST {
			continue
		}
		for _, s := range g.Specs {
			vs := s.(*ast.ValueSpec)
			if id, ok := vs.Type.(*ast.Ident); ok && id.Name == "ProblemType" {
				for _, n := range vs.Names {
					names[n.Name] = true
				}
			}
		}
	}
	return names
}

// Every refusal site names a type from the closed list (T1039, T1061): a
// Refused, Err, bare 4xx sentinel or untyped fieldError anywhere in the
// package fails, with its line.
func TestProblemTypeSites(t *testing.T) {
	untyped, badTypes, _ := scanRefusalSites(t)
	for _, b := range badTypes {
		t.Errorf("%s: %s", b.pos, b.what)
	}
	var files []string
	for f := range untyped {
		files = append(files, f)
	}
	sort.Strings(files)
	for _, f := range files {
		for _, s := range untyped[f] {
			t.Errorf("%s: an untyped refusal (%s): give it a type with problem, refuse or fieldErr", s.pos, s.what)
		}
	}
}

// Every non-retired type is used by some site; a retired type is used by
// none.
func TestProblemTypesUsed(t *testing.T) {
	_, _, used := scanRefusalSites(t)
	byID := map[ProblemType]int{}
	for name, n := range used {
		for _, p := range problemTypeList {
			if constNameOf(p.ID) == name {
				byID[p.ID] += n
			}
		}
	}
	for _, p := range problemTypeList {
		n := byID[p.ID]
		switch {
		case p.Retired != "" && n > 0:
			t.Errorf("%s is retired in %s but still used", p.ID, p.Retired)
		case p.Retired == "" && n == 0:
			t.Errorf("%s is used by no site; use it or retire it", p.ID)
		}
	}
}

// builtInValues are the types whose values ProblemValuesOf takes from an
// error's own fields rather than from a site's literal, and the names it
// always sets.
var builtInValues = map[string][]string{
	"problemRevConflict":           {"type", "record", "rev_sent", "rev_current"},
	"problemRelationAlreadyExists": {"relation"},
}

// alwaysValuesExceptions are value names written at every site of a type
// but possibly empty at run time, so not sent every time. None today: every
// act over a place carries its code (requireActOver), and a site that names a
// record by an address read from its column falls back to the record's ID
// (addressOrID).
var alwaysValuesExceptions = map[string][]string{}

// computedAlwaysValues is, per list constant, the value names every typed
// site of the type writes in its literal (a site with nil values writes
// none), minus alwaysValuesExceptions.
func computedAlwaysValues(t *testing.T) map[string][]string {
	t.Helper()
	fset := token.NewFileSet()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	sites := map[string][]map[string]bool{}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") || name == "problem_types.go" {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.CallExpr:
				if id, ok := n.Fun.(*ast.Ident); ok && typedConstructors[id.Name] && len(n.Args) > 1 {
					if c, ok := n.Args[0].(*ast.Ident); ok {
						sites[c.Name] = append(sites[c.Name], literalKeys(n.Args[len(n.Args)-1]))
					}
				}
			case *ast.CompositeLit:
				if id, ok := n.Type.(*ast.Ident); ok && id.Name == "fieldError" {
					var typ string
					var values ast.Expr
					for _, e := range n.Elts {
						kv, ok := e.(*ast.KeyValueExpr)
						if !ok {
							continue
						}
						switch k, _ := kv.Key.(*ast.Ident); {
						case k == nil:
						case k.Name == "typ":
							if v, ok := kv.Value.(*ast.Ident); ok {
								typ = v.Name
							}
						case k.Name == "values":
							values = kv.Value
						}
					}
					if typ != "" {
						sites[typ] = append(sites[typ], literalKeys(values))
					}
				}
			}
			return true
		})
	}
	out := map[string][]string{}
	for name, ss := range sites {
		var common []string
		for _, v := range declaredValues[name] {
			all := true
			for _, s := range ss {
				all = all && s[v]
			}
			if all && !slices.Contains(alwaysValuesExceptions[name], v) {
				common = append(common, v)
			}
		}
		out[name] = common
	}
	for name, v := range builtInValues {
		out[name] = v
	}
	return out
}

// literalKeys is the set of string keys of a problemValues literal; nil or
// any other expression has none.
func literalKeys(e ast.Expr) map[string]bool {
	keys := map[string]bool{}
	lit, ok := e.(*ast.CompositeLit)
	if !ok {
		return keys
	}
	for _, el := range lit.Elts {
		if kv, ok := el.(*ast.KeyValueExpr); ok {
			if b, ok := kv.Key.(*ast.BasicLit); ok {
				if k, err := strconv.Unquote(b.Value); err == nil {
					keys[k] = true
				}
			}
		}
	}
	return keys
}

// Each type's AlwaysValues is exactly the computed set, in its Values order:
// the bold names in docs/PROBLEM_TYPES.md and the data a product's catalogue
// test reads are a promise the server keeps.
func TestProblemTypesAlwaysValues(t *testing.T) {
	computed := computedAlwaysValues(t)
	for _, p := range problemTypeList {
		want := computed[constNameOf(p.ID)]
		if fmt.Sprint(p.AlwaysValues) != fmt.Sprint(want) && !(len(p.AlwaysValues) == 0 && len(want) == 0) {
			t.Errorf("%s: AlwaysValues %v, computed %v", p.ID, p.AlwaysValues, want)
		}
	}
	if os.Getenv("SMELDR_PRINT_ALWAYS_VALUES") != "" {
		for _, p := range problemTypeList {
			fmt.Printf("ALWAYS %s %s\n", p.ID, strings.Join(computed[constNameOf(p.ID)], ","))
		}
	}
}

// constNameOf is the constant name the list uses for id: "problem" and the
// id's words, capitalized.
func constNameOf(id ProblemType) string {
	var b strings.Builder
	b.WriteString("problem")
	for _, w := range strings.FieldsFunc(string(id), func(r rune) bool { return r == '.' || r == '_' }) {
		b.WriteString(strings.ToUpper(w[:1]) + w[1:])
	}
	return b.String()
}
