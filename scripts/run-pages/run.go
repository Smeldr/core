// AGPL-3.0-or-later

package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Item is one step of a sequence: a published page's bash blocks (From and To
// limit them to a range, 1-based and inclusive; 0 means the page's first or
// last block), or a setup file that is not a page block.
type Item struct {
	Slug     string
	From, To int
	Setup    string
}

// Expectation says that one block of a page is meant to be refused: it must
// exit with Exit and print Contains.
type Expectation struct {
	Slug     string
	Block    int
	Exit     int
	Contains string
}

// Config is one run.
type Config struct {
	Site         string        // the site the pages are fetched from, such as https://smeldr.dev
	Drafts       string        // instead of Site: a directory of drafts, <slug>.md, read before they are published
	OSArch       string        // the release file suffix the reader's system uses
	Port         int           // the server's port; 8080 is the pages' own
	Work         string        // the directory the run writes to
	Bash         string        // the bash to run the blocks with
	BlockTimeout time.Duration // how long one block may take
	RunTimeout   time.Duration // how long the whole run may take
	ReadyTimeout time.Duration // how long the server may take to print its ready line (default 30s)
	Seq          []Item
	Setups       fs.FS
	Expect       []Expectation
	Out          io.Writer
}

// OSArchs are the release file suffixes core and the cli publish.
var OSArchs = []string{"linux-amd64", "linux-arm64", "darwin-amd64", "darwin-arm64", "windows-amd64"}

const readyLine = "smeldr-server: listening on"
const bootstrapLine = "bootstrap admin token"

// The token variables a page's blocks set. Their values are masked in what the
// runner prints, and a block that prints one fails the run.
var tokenVars = []string{"SMELDR_TOKEN", "AGENT_TOKEN", "PERSON_TOKEN"}

var expectLine = regexp.MustCompile(`^(\S+)\s+(\d+)\s+exit=(\d+)\s+contains="([^"]*)"\s*$`)

// ParseExpect reads expect.txt: one line per refused block, blank lines and
// lines that start with # ignored. A line it cannot read is an error that names
// the line.
func ParseExpect(text string) ([]Expectation, error) {
	var out []Expectation
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		m := expectLine.FindStringSubmatch(line)
		if m == nil {
			return nil, fmt.Errorf("expect.txt line %d: want <slug> <block> exit=<status> contains=\"<text>\", got %q", i+1, line)
		}
		block, _ := strconv.Atoi(m[2])
		exit, _ := strconv.Atoi(m[3])
		if block < 1 {
			return nil, fmt.Errorf("expect.txt line %d: blocks are numbered from 1", i+1)
		}
		out = append(out, Expectation{Slug: m[1], Block: block, Exit: exit, Contains: m[4]})
	}
	return out, nil
}

// Fetch gets a published page's body as Markdown, without credentials.
func Fetch(ctx context.Context, site, slug string) (string, error) {
	url := strings.TrimRight(site, "/") + "/docs/" + slug
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "text/markdown")
	c := &http.Client{Timeout: 30 * time.Second}
	resp, err := c.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetch %s: status %d, want 200", url, resp.StatusCode)
	}
	mt, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if mt != "text/markdown" {
		return "", fmt.Errorf("fetch %s: content type %q, want text/markdown", url, resp.Header.Get("Content-Type"))
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", fmt.Errorf("fetch %s: %w", url, err)
	}
	return string(b), nil
}

// draftSlug is what a page name may be, so a draft is always a file directly
// inside the drafts directory.
var draftSlug = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// ReadDraft reads a page's body from dir/<slug>.md, as the publishing scripts
// take it: the text after the line that ends the header comment ("-->"),
// with the blank lines before it removed. It reads no file outside dir.
func ReadDraft(dir, slug string) (string, error) {
	if !draftSlug.MatchString(slug) {
		return "", fmt.Errorf("draft %q: not a page name", slug)
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return "", fmt.Errorf("-drafts %s: not a directory", dir)
	}
	b, err := os.ReadFile(filepath.Join(dir, slug+".md"))
	if err != nil {
		return "", fmt.Errorf("draft %s: %w", slug, err)
	}
	text := strings.ReplaceAll(string(b), "\r\n", "\n")
	i := strings.Index(text, "-->\n")
	if i < 0 {
		return "", fmt.Errorf("draft %s: no header comment ending in -->, so no body", slug)
	}
	return strings.TrimLeft(text[i+len("-->\n"):], "\n"), nil
}

// Extract returns a page's fenced bash blocks in page order, also those
// indented in a list item, with the indent taken off. Every other fence
// (powershell, json, output) is skipped.
func Extract(body string) []string {
	var blocks []string
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	for i := 0; i < len(lines); i++ {
		t := strings.TrimLeft(lines[i], " \t")
		if !strings.HasPrefix(t, "```") {
			continue
		}
		indent := lines[i][:len(lines[i])-len(t)]
		lang := strings.TrimSpace(strings.TrimPrefix(t, "```"))
		var b []string
		j := i + 1
		for ; j < len(lines); j++ {
			if strings.TrimSpace(lines[j]) == "```" {
				break
			}
			b = append(b, strings.TrimPrefix(lines[j], indent))
		}
		if lang == "bash" {
			blocks = append(blocks, strings.Join(b, "\n")+"\n")
		}
		i = j
	}
	return blocks
}

// substitute makes the one change a page tells its reader to make, the file
// names for their system, and moves the port when the run uses another.
func substitute(block, osArch string, port int) string {
	name := osArch
	if strings.HasPrefix(osArch, "windows-") {
		name += ".exe" // the release files for Windows are named with it, as the pages say
	}
	block = strings.ReplaceAll(block, "linux-amd64", name)
	if port != 8080 {
		block = strings.ReplaceAll(block, "localhost:8080", "localhost:"+strconv.Itoa(port))
	}
	return block
}

// RPCError reports whether output carries a JSON-RPC answer that is an error
// or a tool result marked isError. It reads each line that is a JSON object
// (an SSE "data:" line or a bare one) as JSON, so a result whose text holds
// the word "error" is not one.
func RPCError(output string) bool {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "data:"))
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var m map[string]json.RawMessage
		if json.Unmarshal([]byte(line), &m) != nil {
			continue
		}
		if _, rpc := m["jsonrpc"]; !rpc {
			continue
		}
		if e, ok := m["error"]; ok && string(e) != "null" {
			return true
		}
		var r struct {
			IsError bool `json:"isError"`
		}
		if res, ok := m["result"]; ok && json.Unmarshal(res, &r) == nil && r.IsError {
			return true
		}
	}
	return false
}

var longValue = regexp.MustCompile(`[A-Za-z0-9_.+/=-]{40,}`)

// mask hides every known token, and any other long opaque value, in text the
// runner prints.
func mask(text string, tokens []string) string {
	for _, t := range tokens {
		if t != "" {
			text = strings.ReplaceAll(text, t, "<token>")
		}
	}
	return longValue.ReplaceAllString(text, "<long value>")
}

// run is one bash session that runs the blocks as one terminal would.
type session struct {
	cfg     Config
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	dir     string // the work directory, as bash spells it
	n       int
	tokens  []string
	started bool          // a server was started
	ended   chan struct{} // closed when bash has ended, for whatever reason
}

// q quotes a path for bash. It is used only for paths the runner made itself
// under -work, which Run checks holds no single quote; nothing read from a
// page or a log ever goes through it.
func q(p string) string { return "'" + filepath.ToSlash(p) + "'" }

var digits = regexp.MustCompile(`^[0-9]+$`)

func (s *session) path(parts ...string) string {
	return filepath.Join(append([]string{s.cfg.Work}, parts...)...)
}

// send writes one command line to bash and waits for the status file it
// names, within the block time limit and the run's context.
func (s *session) send(ctx context.Context, line, status string) (int, error) {
	if _, err := io.WriteString(s.stdin, line+"\n"); err != nil {
		return 0, fmt.Errorf("bash ended: %w", err)
	}
	deadline := time.Now().Add(s.cfg.BlockTimeout)
	for {
		if b, err := os.ReadFile(status); err == nil && strings.HasSuffix(string(b), "\n") {
			return strconv.Atoi(strings.TrimSpace(string(b)))
		}
		if ctx.Err() != nil {
			return 0, fmt.Errorf("the run did not finish within %s", s.cfg.RunTimeout)
		}
		if time.Now().After(deadline) {
			return 0, fmt.Errorf("did not finish within %s (stdin is closed, so a block that waits for an answer ends here)", s.cfg.BlockTimeout)
		}
		select {
		case <-s.ended:
			return 0, errors.New("bash ended before the block did (the block ran exit, or the shell died)")
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// block runs one block in the session's shell, stdin closed, and returns its
// exit status, its own output, and, for a block whose last line starts the
// server, the server's log (which shows the bootstrap token to the reader, as
// the page says). That line runs in the background, and the block waits for
// the server to be ready.
func (s *session) block(ctx context.Context, code string) (int, string, string, error) {
	s.n++
	n := strconv.Itoa(s.n)
	file, out, status, tok := s.path("blocks", n+".sh"), s.path("out", n+".txt"), s.path("out", n+".status"), s.path("out", n+".tok")
	lines := strings.Split(strings.TrimRight(code, "\n"), "\n")
	server := strings.TrimSpace(lines[len(lines)-1]) == "./smeldr-server"
	if server {
		code = strings.Join(lines[:len(lines)-1], "\n") + "\n"
	}
	if err := os.WriteFile(file, []byte(code), 0o644); err != nil {
		return 0, "", "", err
	}
	dump := `printf '%s\n'`
	for _, v := range tokenVars {
		dump += ` "${` + v + `:-}"`
	}
	line := "{ . " + q(file) + " ; } </dev/null >" + q(out) + " 2>&1; __s=$?; "
	if server {
		// The reader's directory, so the bootstrap token can be written there
		// from Go (Git Bash spells it as Windows does with -W).
		line += "{ pwd -W 2>/dev/null || pwd; } >" + q(s.path("out", "cwd")) + "; "
		line += "if [ $__s = 0 ]; then ./smeldr-server </dev/null >" + q(s.path("server.log")) + " 2>&1 & echo $! >" + q(s.path("server.pid")) + "; fi; "
	}
	line += dump + " >" + q(tok) + "; echo $__s >" + q(status)
	code2, err := s.send(ctx, line, status)
	output, _ := os.ReadFile(out)
	if b, rerr := os.ReadFile(tok); rerr == nil {
		for _, t := range strings.Split(string(b), "\n") {
			if t = strings.TrimSpace(t); t != "" && !contains(s.tokens, t) {
				s.tokens = append(s.tokens, t)
			}
		}
	}
	os.Remove(tok)
	if err != nil || !server || code2 != 0 {
		return code2, string(output), "", err
	}
	s.started = true
	log, err := s.waitReady(ctx)
	return code2, string(output), log, err
}

// waitReady waits for the server's ready line and copies the bootstrap token
// into bootstrap.txt, as the page has the reader do by hand.
func (s *session) waitReady(ctx context.Context) (string, error) {
	logFile := s.path("server.log")
	wait := s.cfg.ReadyTimeout
	if wait == 0 {
		wait = 30 * time.Second
	}
	deadline := time.Now().Add(wait)
	var text string
	for {
		b, _ := os.ReadFile(logFile)
		text = string(b)
		if strings.Contains(text, readyLine) {
			break
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return text, fmt.Errorf("the server printed no ready line within %s", wait)
		}
		time.Sleep(100 * time.Millisecond)
	}
	ls := strings.Split(text, "\n")
	boot := ""
	for i, l := range ls {
		if strings.Contains(l, bootstrapLine) && i+1 < len(ls) {
			boot = strings.TrimSpace(ls[i+1])
		}
	}
	if boot == "" {
		return text, errors.New("the server's log has no bootstrap token line")
	}
	s.tokens = append(s.tokens, boot)
	// Written from Go into the reader's directory: nothing read from the log
	// is ever put into a shell line.
	cwd, err := os.ReadFile(s.path("out", "cwd"))
	if err != nil {
		return text, fmt.Errorf("the reader's directory is not known: %w", err)
	}
	if err := os.WriteFile(filepath.Join(filepath.FromSlash(strings.TrimSpace(string(cwd))), "bootstrap.txt"), []byte(boot+"\n"), 0o600); err != nil {
		return text, fmt.Errorf("write bootstrap.txt: %w", err)
	}
	return text, nil
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// stop ends the server by its process id, whatever ended the run, and then
// bash.
func (s *session) stop() {
	if b, err := os.ReadFile(s.path("server.pid")); err == nil {
		if pid := strings.TrimSpace(string(b)); digits.MatchString(pid) {
			c := exec.Command(s.cfg.Bash, "--noprofile", "--norc", "-c", "kill "+pid+" 2>/dev/null; sleep 1; kill -9 "+pid+" 2>/dev/null; true")
			c.Run()
		}
	}
	if s.stdin != nil {
		s.stdin.Close()
	}
	if s.ended != nil {
		select {
		case <-s.ended:
		case <-time.After(3 * time.Second):
			killTree(s.cmd)
			<-s.ended
		}
	}
}

// Run fetches every page of the sequence once, then runs its blocks in one
// bash session and checks each one, stopping at the first that is not as
// expected.
func Run(cfg Config) (err error) {
	out := cfg.Out
	if !contains(OSArchs, cfg.OSArch) {
		return fmt.Errorf("-os-arch %q is none of %s", cfg.OSArch, strings.Join(OSArchs, ", "))
	}
	if len(cfg.Seq) == 0 {
		return errors.New("the sequence is empty")
	}
	if strings.Contains(cfg.Work, "'") {
		return fmt.Errorf("-work %q holds a single quote, which the runner does not quote", cfg.Work)
	}
	if cfg.Drafts != "" && cfg.Site != "" {
		return errors.New("-drafts and -site both given: the pages come from one of them")
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.RunTimeout)
	defer cancel()
	for _, d := range []string{"pages", "blocks", "out", "run"} {
		if err := os.MkdirAll(filepath.Join(cfg.Work, d), 0o755); err != nil {
			return err
		}
	}
	fmt.Fprintf(out, "work directory: %s\n", cfg.Work)
	defer func() {
		fmt.Fprintf(out, "work directory, kept: %s (pages/ as fetched, blocks/ as run, out/ each block's output, run/ the reader's directory, server.log)\n", cfg.Work)
	}()

	// Every setup exists, before anything runs.
	for _, it := range cfg.Seq {
		if it.Setup != "" {
			if _, err := fs.Stat(cfg.Setups, it.Setup); err != nil {
				return fmt.Errorf("setup %s: not found", it.Setup)
			}
		}
	}

	// Fetch once: every block of the run comes from this one snapshot.
	blocks := map[string][]string{}
	for _, it := range cfg.Seq {
		if it.Slug == "" || blocks[it.Slug] != nil {
			continue
		}
		var body, from string
		var err error
		if cfg.Drafts != "" {
			body, err = ReadDraft(cfg.Drafts, it.Slug)
			from = " (draft)"
		} else {
			body, err = Fetch(ctx, cfg.Site, it.Slug)
		}
		if err != nil {
			return err
		}
		os.WriteFile(filepath.Join(cfg.Work, "pages", it.Slug+".md"), []byte(body), 0o644)
		fmt.Fprintf(out, "page %s%s: %d bytes, sha256 %x\n", it.Slug, from, len(body), sha256.Sum256([]byte(body)))
		b := Extract(body)
		if len(b) == 0 {
			return fmt.Errorf("page %s has no bash block", it.Slug)
		}
		blocks[it.Slug] = b
	}

	// Every expectation for a page in this run names a block the page has.
	exp := map[string]map[int]Expectation{}
	for _, e := range cfg.Expect {
		b, ok := blocks[e.Slug]
		if !ok {
			continue
		}
		if e.Block > len(b) {
			return fmt.Errorf("expect.txt names %s block %d, but the page has %d bash blocks: the page changed, update expect.txt", e.Slug, e.Block, len(b))
		}
		if exp[e.Slug] == nil {
			exp[e.Slug] = map[int]Expectation{}
		}
		exp[e.Slug][e.Block] = e
	}

	s := &session{cfg: cfg}
	s.cmd = exec.Command(cfg.Bash, "--noprofile", "--norc", "-s")
	s.cmd.Dir = filepath.Join(cfg.Work, "run")
	logf, err := os.Create(filepath.Join(cfg.Work, "bash.log"))
	if err != nil {
		return err
	}
	defer logf.Close()
	s.cmd.Stdout, s.cmd.Stderr = logf, logf
	setGroup(s.cmd)
	if s.stdin, err = s.cmd.StdinPipe(); err != nil {
		return err
	}
	if err := s.cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", cfg.Bash, err)
	}
	s.ended = make(chan struct{})
	go func() { s.cmd.Wait(); close(s.ended) }()
	defer s.stop()
	if cfg.Port != 8080 {
		io.WriteString(s.stdin, "export PORT="+strconv.Itoa(cfg.Port)+"\n")
	}

	for _, it := range cfg.Seq {
		if it.Setup != "" {
			code, _ := fs.ReadFile(cfg.Setups, it.Setup)
			fmt.Fprintf(out, "\n===== setup %s (not a page block)\n", it.Setup)
			st, output, _, err := s.block(ctx, substitute(string(code), cfg.OSArch, cfg.Port))
			fmt.Fprint(out, mask(output, s.tokens))
			if err != nil {
				return fmt.Errorf("setup %s: %w", it.Setup, err)
			}
			if st != 0 {
				return fmt.Errorf("setup %s: exit %d", it.Setup, st)
			}
			continue
		}
		b := blocks[it.Slug]
		from, to := it.From, it.To
		if from == 0 {
			from = 1
		}
		if to == 0 || to > len(b) {
			to = len(b)
		}
		for i := from; i <= to; i++ {
			code := substitute(b[i-1], cfg.OSArch, cfg.Port)
			fmt.Fprintf(out, "\n===== %s block %d\n", it.Slug, i)
			for _, l := range strings.Split(strings.TrimRight(code, "\n"), "\n") {
				fmt.Fprintf(out, "  | %s\n", l)
			}
			fmt.Fprintln(out, "----- output")
			st, output, serverLog, err := s.block(ctx, code)
			fmt.Fprint(out, mask(output, s.tokens))
			if serverLog != "" {
				fmt.Fprintf(out, "----- the server's log (the server runs in the background)\n%s", mask(serverLog, s.tokens))
			}
			where := fmt.Sprintf("%s block %d", it.Slug, i)
			if err != nil {
				return fmt.Errorf("%s: %w", where, err)
			}
			for _, t := range s.tokens {
				if strings.Contains(output, t) {
					return fmt.Errorf("%s printed a token", where)
				}
			}
			if e, ok := exp[it.Slug][i]; ok {
				if st != e.Exit || !strings.Contains(output, e.Contains) {
					return fmt.Errorf("%s is meant to be refused (exit %d, %q), got exit %d: the refusal is gone or changed", where, e.Exit, e.Contains, st)
				}
				fmt.Fprintf(out, "----- refused, as the page means (exit %d, %q)\n", st, e.Contains)
				continue
			}
			if st != 0 {
				return fmt.Errorf("%s: exit %d", where, st)
			}
			if RPCError(output) {
				return fmt.Errorf("%s: the answer is a JSON-RPC error", where)
			}
		}
	}
	fmt.Fprintln(out, "\nall blocks as expected")
	return nil
}

// sequences are the runs, named. A page that shows the example places runs
// after Getting started and Set up the example places, as a reader does
// (T1272). decisions-before-an-edit stops before the two blocks that need the
// server stopped (blocks 3 and 4), which the runner does not do, and runs the
// block after them.
var sequences = map[string][]Item{
	"getting-started": {{Slug: "getting-started"}, {Slug: "an-agent-is-refused"}},
	"deploy":          {{Slug: "deploy-and-first-start"}},
	"connect-an-agent": {{Slug: "getting-started"}, {Slug: "example-places"},
		{Slug: "connect-an-agent"}},
	"which-decisions-apply": {{Slug: "getting-started"}, {Slug: "example-places"},
		{Slug: "which-decisions-apply"}},
	"decisions-before-an-edit": {{Slug: "getting-started"}, {Slug: "example-places"},
		{Slug: "decisions-before-an-edit", To: 2}, {Slug: "decisions-before-an-edit", From: 5}},
}

// lookupSequence returns the named sequence.
func lookupSequence(name string) ([]Item, error) {
	items, ok := sequences[name]
	if !ok {
		return nil, fmt.Errorf("-seq %q is no sequence; the sequences are %s", name, strings.Join(sequenceNames(), ", "))
	}
	return items, nil
}

func sequenceNames() []string {
	var n []string
	for k := range sequences {
		n = append(n, k)
	}
	sort.Strings(n)
	return n
}
