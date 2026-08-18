// Command checkpins reports smeldr.dev/* direct module pins in the given
// directories' go.mod files that are behind the latest version published
// on the Go module proxy. A module covered by an active replace directive
// is skipped — its require line is inert placeholder text, not a real pin.
// An indirect require is also skipped — T217's own motivating incidents
// were all about a direct dependency missing a capability a caller
// actually reaches (a tool, a symbol); an indirect pin's staleness is a
// different, lower-signal risk class not worth this check's own noise,
// and `go mod tidy` already manages indirect versions on its own.
//
// Usage:
//
//	go run ./scripts/checkpins <dir>...
//
// Exits non-zero and prints every stale pin found; exits 0 silently when
// every pin is current.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"golang.org/x/mod/modfile"
)

// stalePin describes one smeldr.dev/* dependency whose pinned version is
// behind the latest version published on the module proxy.
type stalePin struct {
	dir    string
	module string
	pinned string
	latest string
}

// versionLookup returns the latest published version of module, or an
// error if it cannot be determined. Injected so checkModule's own parsing
// and comparison logic can be tested without a live network call.
type versionLookup func(module string) (string, error)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: checkpins <dir>...")
		os.Exit(2)
	}

	var stale []stalePin
	for _, dir := range os.Args[1:] {
		pins, err := checkDir(dir, goListLatestVersion)
		if err != nil {
			fmt.Fprintf(os.Stderr, "checkpins: %s: %v\n", dir, err)
			os.Exit(1)
		}
		stale = append(stale, pins...)
	}

	if len(stale) == 0 {
		fmt.Println("checkpins: all smeldr.dev/* pins current")
		return
	}

	fmt.Fprintln(os.Stderr, "checkpins: stale pin(s) found:")
	for _, s := range stale {
		fmt.Fprintf(os.Stderr, "  %s: %s pinned at %s, latest is %s\n", s.dir, s.module, s.pinned, s.latest)
	}
	os.Exit(1)
}

// checkDir reads and parses dir's own go.mod, then delegates to checkModule.
func checkDir(dir string, latest versionLookup) ([]stalePin, error) {
	path := dir + "/go.mod"
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return checkModule(dir, data, latest)
}

// checkModule is checkDir's own testable core — parsing and comparison
// only, no filesystem or network access itself.
func checkModule(dir string, data []byte, latest versionLookup) ([]stalePin, error) {
	f, err := modfile.Parse(dir+"/go.mod", data, nil)
	if err != nil {
		return nil, fmt.Errorf("parse go.mod: %w", err)
	}

	replaced := map[string]bool{}
	for _, r := range f.Replace {
		replaced[r.Old.Path] = true
	}

	var stale []stalePin
	for _, req := range f.Require {
		mod := req.Mod.Path
		if !strings.HasPrefix(mod, "smeldr.dev/") || replaced[mod] || req.Indirect {
			continue
		}
		latestVer, err := latest(mod)
		if err != nil {
			return nil, fmt.Errorf("look up %s: %w", mod, err)
		}
		if latestVer != "" && latestVer != req.Mod.Version {
			stale = append(stale, stalePin{dir: dir, module: mod, pinned: req.Mod.Version, latest: latestVer})
		}
	}
	return stale, nil
}

// goListLatestVersion is the real versionLookup, backed by `go list -m
// -versions` against the live module proxy. `go list -m -versions` prints
// versions in ascending semver order — the last field is the latest.
func goListLatestVersion(module string) (string, error) {
	out, err := exec.Command("go", "list", "-m", "-versions", module).Output()
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(out))
	if len(fields) < 2 {
		return "", fmt.Errorf("no published versions found for %s", module)
	}
	return fields[len(fields)-1], nil
}
