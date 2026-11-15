// AGPL-3.0-or-later

package main

import (
	"bytes"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// page is one page the test site serves.
type page struct {
	status int
	ctype  string
	body   string
}

func md(body string) page { return page{200, "text/markdown; charset=utf-8", body} }

func site(t *testing.T, pages map[string]page) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := pages[strings.TrimPrefix(r.URL.Path, "/docs/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Accept") != "text/markdown" {
			t.Errorf("Accept = %q, want text/markdown", r.Header.Get("Accept"))
		}
		w.Header().Set("Content-Type", p.ctype)
		w.WriteHeader(p.status)
		w.Write([]byte(p.body))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// bashPath is a real bash, or the test is skipped. On Windows it must be Git
// Bash, not the WSL launcher in System32.
func bashPath(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("bash")
	if err != nil || strings.Contains(strings.ToLower(p), `\windows\system32`) {
		t.Skip("no bash to run blocks with")
	}
	return p
}

func fence(code string) string { return "```bash\n" + code + "\n```\n" }

// fakeServer is a block that writes a stand-in smeldr-server: it prints the
// lines asked for, writes its pid, and stays up.
func fakeServer(ready, boot bool) string {
	s := `printf '#!/bin/sh\n`
	if boot {
		s += `echo "WARN smeldr: smeldr_tokens is empty: bootstrap admin token created (copy now, shown once):"\necho "\tboottokenvalue1234567890"\n`
	}
	if ready {
		s += `echo "smeldr-server: listening on 127.0.0.1:8080"\n`
	}
	s += `echo $$ > srv.pid\nexec sleep 60\n' > smeldr-server; chmod +x smeldr-server`
	return s
}

type result struct {
	err error
	out string
	cfg Config
}

func run(t *testing.T, pages map[string]page, seq []Item, setups fstest.MapFS, expect []Expectation, tune func(*Config)) result {
	t.Helper()
	var buf bytes.Buffer
	cfg := Config{Site: site(t, pages), OSArch: "linux-amd64", Port: 8080, Work: t.TempDir(), Bash: bashPath(t),
		BlockTimeout: 20 * time.Second, RunTimeout: time.Minute, ReadyTimeout: 5 * time.Second,
		Seq: seq, Setups: setups, Expect: expect, Out: &buf}
	if setups == nil {
		cfg.Setups = fstest.MapFS{}
	}
	if tune != nil {
		tune(&cfg)
	}
	err := Run(cfg)
	return result{err, buf.String(), cfg}
}

func wantErr(t *testing.T, r result, part string) {
	t.Helper()
	if r.err == nil || !strings.Contains(r.err.Error(), part) {
		t.Fatalf("error = %v, want one containing %q\n%s", r.err, part, r.out)
	}
}

func TestFetch_NotOK(t *testing.T) {
	r := run(t, map[string]page{}, []Item{{Slug: "gone"}}, nil, nil, nil)
	wantErr(t, r, "status 404, want 200")
}

func TestFetch_NotMarkdown(t *testing.T) {
	r := run(t, map[string]page{"p": {200, "text/html; charset=utf-8", "<html></html>"}}, []Item{{Slug: "p"}}, nil, nil, nil)
	wantErr(t, r, "want text/markdown")
}

func TestExtract_NoBlocks(t *testing.T) {
	r := run(t, map[string]page{"p": md("Text.\n\n```json\n{}\n```\n")}, []Item{{Slug: "p"}}, nil, nil, nil)
	wantErr(t, r, "page p has no bash block")
}

func TestExtract_IndentedAndOthersSkipped(t *testing.T) {
	body := "```powershell\nWrite-Host no\n```\n\n- A list item:\n\n  ```bash\n  echo one\n  ```\n\n```\noutput\n```\n" + fence("echo two")
	got := Extract(body)
	if len(got) != 2 || got[0] != "echo one\n" || got[1] != "echo two\n" {
		t.Fatalf("Extract = %q", got)
	}
}

func TestSubstitute(t *testing.T) {
	b := "curl -LO x/smeldr-cli-linux-amd64\nmv smeldr-cli-linux-amd64 smeldr-cli\ncurl http://localhost:8080/mcp\n"
	if got := substitute(b, "darwin-arm64", 8080); got != strings.ReplaceAll(b, "linux-amd64", "darwin-arm64") {
		t.Errorf("darwin: %q", got)
	}
	got := substitute(b, "windows-amd64", 8090)
	if !strings.Contains(got, "mv smeldr-cli-windows-amd64.exe smeldr-cli") || !strings.Contains(got, "localhost:8090") {
		t.Errorf("windows: %q", got)
	}
}

func TestSequence_Unknown(t *testing.T) {
	if _, err := lookupSequence("nope"); err == nil || !strings.Contains(err.Error(), "is no sequence") {
		t.Fatalf("err = %v", err)
	}
	if _, err := lookupSequence("getting-started"); err != nil {
		t.Fatal(err)
	}
}

func TestSequence_SetupMissing(t *testing.T) {
	r := run(t, map[string]page{"p": md(fence("true"))}, []Item{{Setup: "missing.sh"}, {Slug: "p"}}, nil, nil, nil)
	wantErr(t, r, "setup missing.sh: not found")
}

func TestArgs_BadOSArch(t *testing.T) {
	r := run(t, map[string]page{"p": md(fence("true"))}, []Item{{Slug: "p"}}, nil, nil, func(c *Config) { c.OSArch = "plan9-amd64" })
	wantErr(t, r, `-os-arch "plan9-amd64" is none of`)
}

func TestServer_NoReadyLine(t *testing.T) {
	p := md(fence(fakeServer(false, true)) + fence("./smeldr-server"))
	r := run(t, map[string]page{"p": p}, []Item{{Slug: "p"}}, nil, nil, func(c *Config) { c.ReadyTimeout = time.Second })
	wantErr(t, r, "no ready line")
}

func TestServer_NoBootstrap(t *testing.T) {
	p := md(fence(fakeServer(true, false)) + fence("./smeldr-server"))
	r := run(t, map[string]page{"p": p}, []Item{{Slug: "p"}}, nil, nil, nil)
	wantErr(t, r, "no bootstrap token line")
}

func TestServer_StoppedAfterFailure(t *testing.T) {
	p := md(fence(fakeServer(true, true)) + fence("./smeldr-server") + fence("false"))
	r := run(t, map[string]page{"p": p}, []Item{{Slug: "p"}}, nil, nil, nil)
	wantErr(t, r, "p block 3: exit 1")
	pid, err := os.ReadFile(filepath.Join(r.cfg.Work, "run", "srv.pid"))
	if err != nil {
		t.Fatalf("the stand-in server never ran: %v", err)
	}
	alive := exec.Command(r.cfg.Bash, "--noprofile", "--norc", "-c", "kill -0 "+strings.TrimSpace(string(pid)))
	if alive.Run() == nil {
		t.Fatalf("the server (pid %s) is still running after the run", strings.TrimSpace(string(pid)))
	}
}

func TestBlock_UnexpectedExit(t *testing.T) {
	r := run(t, map[string]page{"p": md(fence("echo fine") + fence("exit_code() { return 3; }; exit_code"))}, []Item{{Slug: "p"}}, nil, nil, nil)
	wantErr(t, r, "p block 2: exit 3")
}

func TestBlock_UnexpectedRPCError(t *testing.T) {
	b := `echo 'data: {"jsonrpc":"2.0","id":1,"error":{"code":-32001,"message":"forbidden"}}'`
	r := run(t, map[string]page{"p": md(fence(b))}, []Item{{Slug: "p"}}, nil, nil, nil)
	wantErr(t, r, "p block 1: the answer is a JSON-RPC error")
}

func TestRPCError_ResultWithTheWord(t *testing.T) {
	ok := `data: {"jsonrpc":"2.0","id":1,"result":{"content":[{"text":"no error here","type":"text"}],"isError":false}}`
	if RPCError(ok) {
		t.Fatal("a result whose text says error is not an error")
	}
	if !RPCError(`data: {"jsonrpc":"2.0","id":1,"result":{"content":[],"isError":true}}`) {
		t.Fatal("isError true is an error")
	}
	if RPCError(`{"error": "not JSON-RPC"}` + "\nerror: plain text") {
		t.Fatal("only JSON-RPC answers count")
	}
}

func TestBlock_ExpectedRefusalMissing(t *testing.T) {
	exp := []Expectation{{Slug: "p", Block: 1, Exit: 1, Contains: "forbidden"}}
	r := run(t, map[string]page{"p": md(fence("echo allowed"))}, []Item{{Slug: "p"}}, nil, exp, nil)
	wantErr(t, r, "is meant to be refused")
}

func TestBlock_TokenPrinted(t *testing.T) {
	r := run(t, map[string]page{"p": md(fence("AGENT_TOKEN=agenttokenvalue123456789\necho \"$AGENT_TOKEN\""))}, []Item{{Slug: "p"}}, nil, nil, nil)
	wantErr(t, r, "p block 1 printed a token")
	if strings.Contains(r.out, "----- output\nagenttokenvalue123456789") {
		t.Fatalf("the printed token reached the log unmasked:\n%s", r.out)
	}
}

func TestBlock_Timeout(t *testing.T) {
	r := run(t, map[string]page{"p": md(fence("sleep 30"))}, []Item{{Slug: "p"}}, nil, nil, func(c *Config) { c.BlockTimeout = time.Second })
	wantErr(t, r, "p block 1: did not finish within 1s")
}

func TestBlock_StdinClosed(t *testing.T) {
	start := time.Now()
	r := run(t, map[string]page{"p": md(fence(`read -r answer; echo "read returned $?"`))}, []Item{{Slug: "p"}}, nil, nil, nil)
	if r.err != nil {
		t.Fatalf("err = %v\n%s", r.err, r.out)
	}
	if !strings.Contains(r.out, "read returned 1") || time.Since(start) > 15*time.Second {
		t.Fatalf("a block that reads stdin must see it closed at once:\n%s", r.out)
	}
}

func TestRun_Timeout(t *testing.T) {
	r := run(t, map[string]page{"p": md(fence("sleep 30"))}, []Item{{Slug: "p"}}, nil, nil, func(c *Config) { c.RunTimeout = time.Second })
	wantErr(t, r, "the run did not finish within 1s")
}

func TestExpect_BlockOutOfRange(t *testing.T) {
	exp := []Expectation{{Slug: "p", Block: 4, Exit: 1, Contains: "x"}}
	r := run(t, map[string]page{"p": md(fence("true"))}, []Item{{Slug: "p"}}, nil, exp, nil)
	wantErr(t, r, "names p block 4, but the page has 1 bash blocks")
}

func TestExpect_Malformed(t *testing.T) {
	if _, err := ParseExpect("getting-started 10 exit=0 item.locked_state\n"); err == nil || !strings.Contains(err.Error(), "line 1") {
		t.Fatalf("err = %v", err)
	}
	got, err := ParseExpect("# comment\n\ngetting-started 10 exit=0 contains=\"item.locked_state\"\n")
	if err != nil || len(got) != 1 || got[0].Block != 10 || got[0].Contains != "item.locked_state" {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestExpectFile_Parses(t *testing.T) {
	if _, err := ParseExpect(expectText); err != nil {
		t.Fatal(err)
	}
}

// TestRun_OK runs a page the way the real ones are built: state carried from
// block to block, a server started and its bootstrap token copied, a setup,
// a refusal the page means, the file name the reader substitutes.
func TestRun_OK(t *testing.T) {
	p1 := md("Intro.\n\n" + fence("mkdir start && cd start\nNAME=smeldr-cli-linux-amd64\n"+fakeServer(true, true)) +
		"```powershell\nnot run\n```\n" + fence("./smeldr-server") +
		fence(`echo "name $NAME in $(basename "$PWD")"; test -s bootstrap.txt && echo "bootstrap copied"`))
	p2 := md(fence(`echo 'data: {"jsonrpc":"2.0","id":2,"error":{"code":-32001,"message":"forbidden: transition.gate"}}'`) + fence("echo $FROM_SETUP"))
	setups := fstest.MapFS{"s.sh": {Data: []byte("FROM_SETUP=set\n")}}
	exp := []Expectation{{Slug: "p2", Block: 1, Exit: 0, Contains: "transition.gate"}}
	r := run(t, map[string]page{"p1": md(p1.body), "p2": p2}, []Item{{Slug: "p1"}, {Setup: "s.sh"}, {Slug: "p2"}}, setups, exp,
		func(c *Config) { c.OSArch = "darwin-arm64" })
	if r.err != nil {
		t.Fatalf("err = %v\n%s", r.err, r.out)
	}
	for _, want := range []string{"name smeldr-cli-darwin-arm64 in start", "bootstrap copied", "refused, as the page means", "set\n", "all blocks as expected", "sha256 "} {
		if !strings.Contains(r.out, want) {
			t.Errorf("log lacks %q:\n%s", want, r.out)
		}
	}
	// The stand-in's source names the value once; the server's log must show it masked.
	if strings.Count(r.out, "boottokenvalue1234567890") != 1 || !strings.Contains(r.out, "\t<token>") {
		t.Errorf("the bootstrap token is not masked in the server's log:\n%s", r.out)
	}
}

func TestBlock_ShellEnded(t *testing.T) {
	start := time.Now()
	r := run(t, map[string]page{"p": md(fence("echo before") + fence("exit 3"))}, []Item{{Slug: "p"}}, nil, nil, nil)
	wantErr(t, r, "p block 2: bash ended before the block did")
	if time.Since(start) > 10*time.Second {
		t.Fatalf("the end of bash was seen only after %s", time.Since(start))
	}
}

func TestServer_BootstrapWrittenFromGo(t *testing.T) {
	// A value with a quote and a dollar sign: it must reach bootstrap.txt as
	// it is, never through a shell line.
	srv := `printf '#!/bin/sh\necho "bootstrap admin token created (copy now, shown once):"\necho "\tboot'"'"'tok\$HOME1234567890"\necho "smeldr-server: listening on 127.0.0.1:8080"\nexec sleep 60\n' > smeldr-server; chmod +x smeldr-server`
	r := run(t, map[string]page{"p": md(fence(srv) + fence("./smeldr-server") + fence("cat bootstrap.txt > copy.txt"))}, []Item{{Slug: "p"}}, nil, nil, nil)
	if r.err != nil {
		t.Fatalf("err = %v\n%s", r.err, r.out)
	}
	b, err := os.ReadFile(filepath.Join(r.cfg.Work, "run", "bootstrap.txt"))
	if err != nil || string(b) != "boot'tok$HOME1234567890\n" {
		t.Fatalf("bootstrap.txt = %q, %v", b, err)
	}
}

func TestArgs_WorkWithQuote(t *testing.T) {
	r := run(t, map[string]page{"p": md(fence("true"))}, []Item{{Slug: "p"}}, nil, nil, func(c *Config) { c.Work = filepath.Join(c.Work, "it's") })
	wantErr(t, r, "holds a single quote")
}

// draftFile writes dir/<slug>.md with a header comment and the body after it.
func draftFile(t *testing.T, dir, slug, text string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, slug+".md"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fromDrafts reads the pages from dir instead of the test site.
func fromDrafts(dir string) func(*Config) {
	return func(c *Config) { c.Site = ""; c.Drafts = dir }
}

func TestDrafts_DirMissing(t *testing.T) {
	r := run(t, nil, []Item{{Slug: "p"}}, nil, nil, fromDrafts(filepath.Join(t.TempDir(), "none")))
	wantErr(t, r, "not a directory")
}

func TestDrafts_PageMissing(t *testing.T) {
	r := run(t, nil, []Item{{Slug: "p"}}, nil, nil, fromDrafts(t.TempDir()))
	wantErr(t, r, "draft p:")
}

func TestDrafts_NoBody(t *testing.T) {
	dir := t.TempDir()
	draftFile(t, dir, "p", "slug: p\n\n"+fence("true"))
	r := run(t, nil, []Item{{Slug: "p"}}, nil, nil, fromDrafts(dir))
	wantErr(t, r, "no header comment ending in -->")
}

func TestDrafts_WithSite(t *testing.T) {
	dir := t.TempDir()
	draftFile(t, dir, "p", "<!-- header -->\n"+fence("true"))
	r := run(t, map[string]page{"p": md(fence("true"))}, []Item{{Slug: "p"}}, nil, nil,
		func(c *Config) { c.Drafts = dir })
	wantErr(t, r, "-drafts and -site both given")
}

func TestDrafts_SlugOutsideDir(t *testing.T) {
	_, err := ReadDraft(t.TempDir(), "../p")
	if err == nil || !strings.Contains(err.Error(), "not a page name") {
		t.Fatalf("error = %v, want one saying the name is not a page name", err)
	}
}

// A draft is read as the publishing scripts take it: the header comment and
// the blank lines after it are not part of the page, and a CRLF file reads the
// same as an LF one.
func TestDrafts_Body(t *testing.T) {
	dir := t.TempDir()
	draftFile(t, dir, "p", "slug: p\r\n\r\n<!-- the header,\r\nover lines -->\r\n\r\nText.\r\n"+strings.ReplaceAll(fence("echo from-the-draft"), "\n", "\r\n"))
	body, err := ReadDraft(dir, "p")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(body, "Text.\n```bash\n") {
		t.Fatalf("body = %q, want it to start after the header comment", body)
	}
	r := run(t, nil, []Item{{Slug: "p"}}, nil, nil, fromDrafts(dir))
	if r.err != nil {
		t.Fatalf("run: %v\n%s", r.err, r.out)
	}
	if !strings.Contains(r.out, "page p (draft):") || !strings.Contains(r.out, "from-the-draft") {
		t.Fatalf("the draft was not run:\n%s", r.out)
	}
}

// Every setup a sequence names is embedded, so a sequence never fails at its
// first setup for a file that is gone.
func TestSequences_SetupExists(t *testing.T) {
	setups, _ := fs.Sub(setupFS, "setup")
	for name, items := range sequences {
		for _, it := range items {
			if it.Setup == "" {
				continue
			}
			if _, err := fs.Stat(setups, it.Setup); err != nil {
				t.Errorf("sequence %s names setup %s, which is not embedded", name, it.Setup)
			}
		}
	}
}
