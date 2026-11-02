package smeldr

import (
	"fmt"
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

// untypedSitesAllowed is how many untyped refusal sites each file may still
// hold while T1039's phases type them. It only shrinks: a new untyped site, or
// a count that rises, fails TestProblemTypeSites; a count that falls must be
// lowered here in the same commit. Phase 3 ends with it empty.
var untypedSitesAllowed = map[string]int{
	"address.go":               9,
	"address_migrate.go":       1,
	"address_resolve.go":       3,
	"aggregate.go":             2,
	"audit.go":                 4,
	"content_type_redefine.go": 9,
	"context_packet.go":        10,
	"cookiemanifest.go":        1,
	"coverage.go":              12,
	"dynamic.go":               30,
	"dynamic_title.go":         1,
	"edges.go":                 2,
	"event_log.go":             2,
	"eventstream.go":           8,
	"finding_list.go":          2,
	"identity_migrate.go":      1,
	"lineage.go":               2,
	"logcapture.go":            5,
	"middleware.go":            3,
	"migrate.go":               3,
	"module.go":                34,
	"nav.go":                   2,
	"node.go":                  15,
	"provenance.go":            1,
	"provenance_read.go":       4,
	"reachability.go":          4,
	"reachability_handler.go":  4,
	"record_shape_migrate.go":  4,
	"record_title.go":          1,
	"redirectmanifest.go":      1,
	"redirects.go":             2,
	"reference_migrate.go":     3,
	"references.go":            21,
	"relations_list.go":        2,
	"rest_counts.go":           3,
	"rest_counts_stored.go":    1,
	"roles.go":                 1,
	"scheduler.go":             1,
	"schemas.go":               18,
	"search.go":                5,
	"signal_migrate.go":        1,
	"sitemap.go":               1,
	"smeldr.go":                3,
	"stats.go":                 2,
	"storage.go":               6,
	"subscription_match.go":    1,
	"subscription_webhook.go":  1,
	"templates.go":             2,
	"webhook.go":               6,
}

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

// Every refusal site names a type from the closed list, except the untyped
// sites T1039's later phases have yet to type (untypedSitesAllowed), whose
// count per file only falls.
func TestProblemTypeSites(t *testing.T) {
	untyped, badTypes, _ := scanRefusalSites(t)
	for _, b := range badTypes {
		t.Errorf("%s: %s", b.pos, b.what)
	}
	if os.Getenv("SMELDR_PRINT_UNTYPED_SITES") != "" {
		var files []string
		for f := range untyped {
			files = append(files, f)
		}
		sort.Strings(files)
		for _, f := range files {
			fmt.Printf("\t%q: %d,\n", f, len(untyped[f]))
		}
	}
	files := map[string]bool{}
	for f := range untyped {
		files[f] = true
	}
	for f := range untypedSitesAllowed {
		files[f] = true
	}
	for f := range files {
		got, allowed := len(untyped[f]), untypedSitesAllowed[f]
		switch {
		case got > allowed:
			var lines []string
			for _, s := range untyped[f] {
				lines = append(lines, fmt.Sprintf("%d %s", s.pos.Line, s.what))
			}
			t.Errorf("%s: %d untyped refusal sites, %d allowed; give the new one a type with problem, refuse or fieldErr (lines: %s)",
				f, got, allowed, strings.Join(lines, ", "))
		case got < allowed:
			t.Errorf("%s: %d untyped refusal sites, %d allowed; lower untypedSitesAllowed[%q] to %d", f, got, allowed, f, got)
		}
	}
}

// typesNotYetUsed are the listed types no site names yet; it only shrinks,
// and is empty once phase 3 of T1039 is done.
var typesNotYetUsed = map[ProblemType]bool{
	"role.insufficient":                  true,
	"scope.type_not_readable":            true,
	"write.not_permitted":                true,
	"csrf.invalid":                       true,
	"auth.unauthenticated":               true,
	"item.locked_state":                  true,
	"slug.published_immutable":           true,
	"slug.taken":                         true,
	"slug.ambiguous":                     true,
	"migrate.duplicate_published_slugs":  true,
	"route.not_found":                    true,
	"content_type.not_found":             true,
	"feature.not_enabled":                true,
	"redirect.gone":                      true,
	"request.malformed_body":             true,
	"content_type.define_failed":         true,
	"transition.status_required":         true,
	"content_type.not_runtime":           true,
	"slug.record_type_fixed":             true,
	"limit.rest_count_items":             true,
	"limit.findings":                     true,
	"subscription.webhook_not_stream":    true,
	"search.query_invalid":               true,
	"eventstream.params_conflict":        true,
	"eventstream.no_subscriptions":       true,
	"event_log.not_enabled":              true,
	"event_log.cursor_invalid":           true,
	"references.signal_in_tx":            true,
	"content.not_acceptable":             true,
	"request.too_large":                  true,
	"eventstream.too_many_streams":       true,
	"rate.limited":                       true,
	"title.cutover":                      true,
	"signal.history_type_refused":        true,
	"signal.message_too_long":            true,
	"amendment.type_not_in_list":         true,
	"schedule.needs_time":                true,
	"reference.addon_not_run":            true,
	"reference.subject_type_unknown":     true,
	"field.immutable":                    true,
	"content_type.redefine_incompatible": true,
	"address.prefix_reserved":            true,
	"address.prefix_single_letter":       true,
	"address.prefix_fixed":               true,
	"address.prefix_taken":               true,
	"address.prefix_unavailable":         true,
	"address.prefix_web_content":         true,
	"webhook.url_not_allowed":            true,
	"config.invalid":                     true,
	"role.level_mismatch":                true,
	"field.too_short":                    true,
	"field.type_mismatch":                true,
	"field.unknown":                      true,
}

// Every non-retired type is used by some site, or is still waiting for its
// phase (typesNotYetUsed); a retired type is used by none.
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
		case p.Retired == "" && n == 0 && !typesNotYetUsed[p.ID]:
			t.Errorf("%s is used by no site; use it or retire it", p.ID)
		case n > 0 && typesNotYetUsed[p.ID]:
			t.Errorf("%s is used now; remove it from typesNotYetUsed", p.ID)
		}
	}
	if os.Getenv("SMELDR_PRINT_UNTYPED_SITES") != "" {
		for _, p := range problemTypeList {
			if byID[p.ID] == 0 && p.Retired == "" {
				fmt.Printf("\t%q: true,\n", p.ID)
			}
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
