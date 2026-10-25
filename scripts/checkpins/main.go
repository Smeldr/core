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
//	go run ./scripts/checkpins [-root <dir>] [-pins=false] <dir>...
//
// Exits non-zero and prints every stale pin found; exits 0 silently when
// every pin is current.
//
// With -root (the directory of the module the dirs belong to), it also
// checks each dir's toolchain line (T930): a dir that builds a binary must
// pin a toolchain at least as new as the root's, or a plain go build there
// on an older installed Go uses the older one. -pins=false skips the
// network pin lookup, so CI can run the toolchain check on every push.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/semver"
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
	root := flag.String("root", "", "the root module's directory: also check each dir's toolchain line against it")
	pins := flag.Bool("pins", true, "check smeldr.dev/* pins against the module proxy")
	flag.Parse()
	if flag.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: checkpins [-root <dir>] [-pins=false] <dir>...")
		os.Exit(2)
	}

	if *root != "" {
		problems, err := checkToolchains(*root, flag.Args())
		if err != nil {
			fmt.Fprintf(os.Stderr, "checkpins: %v\n", err)
			os.Exit(1)
		}
		if len(problems) > 0 {
			fmt.Fprintln(os.Stderr, "checkpins: toolchain line(s) behind the root:")
			for _, p := range problems {
				fmt.Fprintln(os.Stderr, "  "+p)
			}
			os.Exit(1)
		}
		fmt.Println("checkpins: every toolchain line matches the root")
	}
	if !*pins {
		return
	}

	var stale []stalePin
	for _, dir := range flag.Args() {
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

// readToolchain returns the toolchain line of dir's go.mod ("go1.26.10"),
// or "" when it has none.
func readToolchain(dir string) (string, error) {
	path := dir + "/go.mod"
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	return toolchainOf(path, data)
}

// toolchainOf parses a go.mod and returns its toolchain line, or "".
func toolchainOf(path string, data []byte) (string, error) {
	f, err := modfile.Parse(path, data, nil)
	if err != nil {
		return "", fmt.Errorf("parse %s: %w", path, err)
	}
	if f.Toolchain == nil {
		return "", nil
	}
	return f.Toolchain.Name, nil
}

// checkToolchains compares each dir's toolchain line with the root's and
// returns one problem per dir whose line is missing or older, naming the
// dir and both versions. The root must have a toolchain line.
func checkToolchains(root string, dirs []string) ([]string, error) {
	want, err := readToolchain(root)
	if err != nil {
		return nil, err
	}
	if want == "" {
		return nil, fmt.Errorf("%s/go.mod has no toolchain line to compare against", root)
	}
	var problems []string
	for _, dir := range dirs {
		got, err := readToolchain(dir)
		if err != nil {
			return nil, err
		}
		if p := toolchainProblem(dir, got, want); p != "" {
			problems = append(problems, p)
		}
	}
	return problems, nil
}

// toolchainProblem says what is wrong with dir's toolchain got against the
// root's want, or "" when got is at least as new.
func toolchainProblem(dir, got, want string) string {
	if got == "" {
		return fmt.Sprintf("%s: no toolchain line; the root pins %s", dir, want)
	}
	if semver.Compare(toSemver(got), toSemver(want)) < 0 {
		return fmt.Sprintf("%s: toolchain %s is older than the root's %s", dir, got, want)
	}
	return ""
}

// toSemver turns a toolchain name ("go1.26.10") into a semver ("v1.26.10").
func toSemver(toolchain string) string {
	return "v" + strings.TrimPrefix(toolchain, "go")
}
