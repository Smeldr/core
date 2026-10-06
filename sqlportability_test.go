// AGPL-3.0-or-later

package smeldr

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// This test exists because the same defect was found three times, each time by
// running core on a real Postgres by hand: SQL that only SQLite understands, in code
// that was meant to run on both (the state machine probes, A409; the dynamic content
// seed, A412; the page meta store, A413). It reads every non-test Go file of the core
// module and looks at the string literals only, so comments and prose cannot trip it,
// and a multi-line SQL statement is one literal.

// sqliteOnlyPattern is one thing that is valid SQL on SQLite and not on Postgres,
// with the portable way to say it.
type sqliteOnlyPattern struct {
	name        string
	re          *regexp.Regexp
	alternative string
}

var sqliteOnlyPatterns = []sqliteOnlyPattern{
	{"INSERT OR REPLACE / IGNORE", regexp.MustCompile(`(?i)\binsert\s+or\s+(replace|ignore|abort|fail|rollback)\b`),
		"INSERT ... ON CONFLICT (key) DO UPDATE SET ... or DO NOTHING (valid on SQLite 3.24+ and Postgres)"},
	{"REPLACE INTO", regexp.MustCompile(`(?i)(?:^|[;(])\s*replace\s+into\b`),
		"INSERT ... ON CONFLICT (key) DO UPDATE SET ..."},
	{"sqlite_master", regexp.MustCompile(`(?i)\bsqlite_master\b`),
		"tableExists / columnExists / flowTablesPresent in dbprobe.go (they select no rows from the table, valid everywhere)"},
	{"PRAGMA", regexp.MustCompile(`(?i)^\s*pragma\s+[a-z_]+\s*(?:\(|=|;|$)`),
		"columnExists in dbprobe.go, or EnsureColumn for a migration"},
	{"datetime(", regexp.MustCompile(`(?i)\bdatetime\s*\(`),
		"pass a time.Time as a parameter, or compare TIMESTAMP columns directly"},
	{"strftime(", regexp.MustCompile(`(?i)\bstrftime\s*\(`),
		"format the time in Go and pass it as a parameter"},
	{"IFNULL(", regexp.MustCompile(`(?i)\bifnull\s*\(`),
		"COALESCE(a, b), valid on both"},
	{"? placeholder", regexp.MustCompile(`(?is)\b(select|insert|update|delete)\b.*(\(\s*\?|=\s*\?|,\s*\?|\?\s*\)|\bin\s*\(\s*\?|\blimit\s+\?|\boffset\s+\?)`),
		"$1, $2, ... numbered placeholders (pgx does not understand ?, SQLite accepts both)"},
	// SQL built from fragments (" WHERE status = ?", " AND receiver = ?", " ORDER BY ...
	// LIMIT ?" appended to a statement) has no statement keyword in the literal that
	// holds the placeholder, so the rule above cannot see it. A literal that starts like
	// a clause and holds a placeholder in an operator position is SQL. The shapes are
	// deliberately operator-bound, so prose that merely ends in a question mark is not.
	{"? placeholder (SQL fragment)", regexp.MustCompile(`(?is)^\s*(?:(?:limit|offset)\s+\?|(?:where|and|or|set|limit|offset|values|order\s+by|group\s+by|having)\b.*(?:=\s*\?|<\s*\?|>\s*\?|\blike\s+\?|\bin\s*\(\s*\?|\(\s*\?|,\s*\?|\blimit\s+\?|\boffset\s+\?))`),
		"$1, $2, ... numbered placeholders, numbered from the count of arguments so far (pgx does not understand ?, SQLite accepts both)"},
}

// sqliteOnlyAllowed lists the literals that may stay, each with a reason a reviewer
// can check. A new entry needs one. It is empty: nothing in the core module needs to
// be SQLite only.
var sqliteOnlyAllowed = []struct{ file, text, reason string }{}

type sqliteOnlyHit struct {
	file    string
	line    int
	pattern sqliteOnlyPattern
	literal string
}

// findSQLiteOnlySQL returns the string literals of one Go source file that contain
// SQLite-only SQL and are not allowed.
func findSQLiteOnlySQL(filename string, src any) ([]sqliteOnlyHit, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filename, src, 0)
	if err != nil {
		return nil, err
	}
	var hits []sqliteOnlyHit
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		s, err := strconv.Unquote(lit.Value)
		if err != nil {
			return true
		}
	patterns:
		for _, p := range sqliteOnlyPatterns {
			if !p.re.MatchString(s) {
				continue
			}
			for _, a := range sqliteOnlyAllowed {
				if filepath.ToSlash(filename) == a.file && strings.Contains(s, a.text) {
					continue patterns
				}
			}
			hits = append(hits, sqliteOnlyHit{file: filename, line: fset.Position(lit.Pos()).Line, pattern: p, literal: s})
		}
		return true
	})
	return hits, nil
}

func (h sqliteOnlyHit) String() string {
	lit := strings.Join(strings.Fields(h.literal), " ")
	if len(lit) > 90 {
		lit = lit[:90] + "..."
	}
	return fmt.Sprintf("%s:%d: SQLite-only SQL (%s) in %q\n\tuse instead: %s\n\tcore runs on Postgres through core/pgx too; if this one really must stay, add an entry with a reason to sqliteOnlyAllowed in sqlportability_test.go",
		filepath.ToSlash(h.file), h.line, h.pattern.name, lit, h.pattern.alternative)
}

// TestNoSQLiteOnlySQLInTheSource walks the core module (every directory without a
// go.mod of its own, so not example/, pgx/, scripts/ or spikes/) and fails on SQLite-only
// SQL in a string literal of a non-test file.
func TestNoSQLiteOnlySQLInTheSource(t *testing.T) {
	var walked = map[string]bool{}
	var problems []string
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != "." {
				if d.Name() == ".git" {
					return filepath.SkipDir
				}
				if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil {
					return filepath.SkipDir // a module of its own
				}
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		walked[filepath.ToSlash(filepath.Dir(path))] = true
		hits, err := findSQLiteOnlySQL(path, nil)
		if err != nil {
			return err
		}
		for _, h := range hits {
			problems = append(problems, h.String())
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	dirs := make([]string, 0, len(walked))
	for d := range walked {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	t.Logf("walked the non-test Go files of: %v", dirs)
	if len(dirs) == 0 {
		t.Fatal("the guard walked no directory: it would pass for the wrong reason")
	}
	for _, p := range problems {
		t.Error(p)
	}
}

// TestSQLiteOnlyGuard_Detects pins what the guard fires on and what it leaves alone,
// so it can neither go blind nor cry wolf.
func TestSQLiteOnlyGuard_Detects(t *testing.T) {
	fires := map[string]string{
		"the page meta store before A413": "package x\nvar q = `INSERT OR REPLACE INTO smeldr_page_meta\n (path, meta_title)\n VALUES (?, ?)`",
		"insert or ignore":                "package x\nvar q = \"INSERT OR IGNORE INTO t (a) VALUES ($1)\"",
		"replace into":                    "package x\nvar q = \"REPLACE INTO t (a) VALUES ($1)\"",
		"sqlite_master":                   "package x\nvar q = \"SELECT COUNT(*) FROM sqlite_master\"",
		"pragma":                          "package x\nvar q = \"PRAGMA table_info(t)\"",
		"datetime":                        "package x\nvar q = \"SELECT datetime('now')\"",
		"strftime":                        "package x\nvar q = \"SELECT strftime('%s', created_at) FROM t\"",
		"ifnull":                          "package x\nvar q = \"SELECT IFNULL(a, 0) FROM t\"",
		"? in a where":                    "package x\nvar q = \"DELETE FROM t WHERE path = ?\"",
		"? in an IN list":                 "package x\nvar q = \"SELECT * FROM t WHERE id IN (?, ?)\"",
		"? after LIMIT in a statement":    "package x\nvar q = \"SELECT * FROM t LIMIT ?\"",
		// Fragments: the shapes smeldr.dev/mcp's list_signals appends to a statement.
		"a WHERE fragment":       "package x\nvar w = ` WHERE status = ?`",
		"an AND fragment":        "package x\nvar w = ` AND receiver = ?`",
		"an ORDER BY LIMIT tail": "package x\nvar w = ` ORDER BY created_at DESC LIMIT ?`",
		"an OFFSET fragment":     "package x\nvar w = \" OFFSET ?\"",
		"a SET fragment":         "package x\nvar w = \"SET a = ?, b = ?\"",
		"a VALUES fragment":      "package x\nvar w = \"VALUES (?, ?, 'pending')\"",
		"an OR fragment":         "package x\nvar w = \" OR sender = ?\"",
	}
	for name, src := range fires {
		hits, err := findSQLiteOnlySQL("x.go", src)
		if err != nil || len(hits) == 0 {
			t.Errorf("%s: no hit (err %v): the guard is blind to it", name, err)
		}
	}
	quiet := map[string]string{
		"numbered placeholders":     "package x\nvar q = \"INSERT INTO t (a, b) VALUES ($1, $2) ON CONFLICT (a) DO NOTHING\"",
		"a question in an error":    "package x\nvar e = \"smeldr: update failed, retry? (see the log)\"",
		"a url with a query":        "package x\nvar u = \"https://example.com/search?q=select\"",
		"a comment about sqlite":    "package x\n// INSERT OR REPLACE and sqlite_master are SQLite only\nvar x = 1",
		"prose that names pragma":   "package x\nvar s = \"a pragma of its own\"",
		"coalesce":                  "package x\nvar q = \"SELECT COALESCE(a, 0) FROM t\"",
		"a format verb with a mark": "package x\nvar s = fmt.Sprintf(\"%s?\", \"x\")",
		// Prose that starts like a clause and ends in a question mark is not SQL.
		"prose starting with or":      "package x\nvar s = \"or maybe the file moved?\"",
		"prose starting with where":   "package x\nvar s = \"where is it?\"",
		"prose starting with set":     "package x\nvar s = \"Set to the default (see the docs)?\"",
		"prose starting with and":     "package x\nvar s = \"and what then? (see the log)\"",
		"prose starting with limit":   "package x\nvar s = \"limit exceeded, try again?\"",
		"numbered fragments are fine": "package x\nvar w = \" WHERE status = $1 AND receiver = $2 ORDER BY created_at DESC LIMIT $3\"",
	}
	for name, src := range quiet {
		hits, err := findSQLiteOnlySQL("x.go", src)
		if err != nil || len(hits) != 0 {
			t.Errorf("%s: hits %v (err %v): the guard cries wolf", name, hits, err)
		}
	}
}

func TestSQLiteOnlyGuard_MessageNamesTheFixAndTheLine(t *testing.T) {
	hits, err := findSQLiteOnlySQL("pagemeta.go", "package x\n\nvar q = \"INSERT OR REPLACE INTO t (a) VALUES ($1)\"")
	if err != nil || len(hits) != 1 {
		t.Fatalf("hits = %v, err = %v", hits, err)
	}
	msg := hits[0].String()
	for _, want := range []string{"pagemeta.go:3", "INSERT OR REPLACE", "ON CONFLICT", "sqliteOnlyAllowed"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the message must contain %q:\n%s", want, msg)
		}
	}
}

func TestSQLiteOnlyGuard_AnAllowedLiteralIsLeftAlone(t *testing.T) {
	prev := sqliteOnlyAllowed
	sqliteOnlyAllowed = append(sqliteOnlyAllowed[:0:0], struct{ file, text, reason string }{"x.go", "sqlite_master", "a test of the allowlist"})
	t.Cleanup(func() { sqliteOnlyAllowed = prev })
	hits, err := findSQLiteOnlySQL("x.go", "package x\nvar q = \"SELECT 1 FROM sqlite_master\"")
	if err != nil || len(hits) != 0 {
		t.Errorf("hits = %v, err = %v, want none for an allowed literal", hits, err)
	}
	hits, _ = findSQLiteOnlySQL("y.go", "package x\nvar q = \"SELECT 1 FROM sqlite_master\"")
	if len(hits) == 0 {
		t.Error("the allowlist entry is for x.go only")
	}
}
