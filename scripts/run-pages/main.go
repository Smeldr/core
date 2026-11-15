// AGPL-3.0-or-later

// Command run-pages runs the bash blocks of published /docs pages as written,
// against the release binaries, the way a reader's terminal would (T1251).
//
// It fetches each page of a named sequence once from the site, as Markdown,
// and prints its hash; runs the pages' bash blocks in one bash session in a
// fresh directory, each with stdin closed and a time limit; and stops at the
// first block that is not as expected. A block is as expected when it exits 0
// and answers no JSON-RPC error, or, when expect.txt lists it as a refusal the
// page means to show, when it exits as listed and prints the text listed.
//
// The only change it makes to a block is the one the pages tell a reader to
// make: linux-amd64 in the file names becomes -os-arch. Its departures from a
// reader, printed in the log: the server line runs in the background, the
// bootstrap token is copied from the server's log into bootstrap.txt, and a
// -port other than 8080 moves the port. It uses no credentials, masks every
// token it sees, fails a block that prints one, and writes only under -work.
//
// With -drafts <dir> it reads each page from <dir>/<slug>.md instead, the body
// after the draft's header comment, so a changed page is run before it is
// published (T1272).
//
//	go run . -seq getting-started -os-arch darwin-arm64
//	go run . -seq connect-an-agent -drafts ../../drafts
package main

import (
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"time"
)

// The setups a sequence may name (setup/README.md says what they are for).
// The whole directory is embedded, so it compiles with no setup in it.
//
//go:embed setup
var setupFS embed.FS

//go:embed expect.txt
var expectText string

func main() {
	seq := flag.String("seq", "", "the sequence to run: "+strings.Join(sequenceNames(), ", "))
	osArch := flag.String("os-arch", "linux-amd64", "the release file suffix for this system: "+strings.Join(OSArchs, ", "))
	site := flag.String("site", "https://smeldr.dev", "the site the pages are fetched from")
	drafts := flag.String("drafts", "", "instead of the site, read each page from <dir>/<slug>.md, a draft before it is published")
	port := flag.Int("port", 8080, "the server's port")
	work := flag.String("work", "", "the directory to run in (default: a new temporary one)")
	bash := flag.String("bash", "bash", "the bash to run the blocks with")
	blockTimeout := flag.Duration("block-timeout", 2*time.Minute, "how long one block may take")
	runTimeout := flag.Duration("run-timeout", 20*time.Minute, "how long the whole run may take")
	flag.Parse()
	// -drafts replaces the site's default; given together with -site, Run
	// refuses the pair.
	if *drafts != "" {
		siteSet := false
		flag.Visit(func(f *flag.Flag) { siteSet = siteSet || f.Name == "site" })
		if !siteSet {
			*site = ""
		}
	}

	items, err := lookupSequence(*seq)
	if err != nil {
		fail(err)
	}
	expect, err := ParseExpect(expectText)
	if err != nil {
		fail(err)
	}
	if *work == "" {
		if *work, err = os.MkdirTemp("", "run-pages-"); err != nil {
			fail(err)
		}
	}
	setups, _ := fs.Sub(setupFS, "setup")
	err = Run(Config{Site: *site, Drafts: *drafts, OSArch: *osArch, Port: *port, Work: *work, Bash: *bash,
		BlockTimeout: *blockTimeout, RunTimeout: *runTimeout, Seq: items, Setups: setups,
		Expect: expect, Out: os.Stdout})
	if err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "run-pages:", err)
	os.Exit(1)
}
