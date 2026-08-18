package main

import (
	"errors"
	"testing"
)

func TestCheckModule_SkipsReplaced(t *testing.T) {
	data := []byte(`module example/x

go 1.26.5

require smeldr.dev/core v1.0.0

replace smeldr.dev/core => ../..
`)
	calls := 0
	latest := func(_ string) (string, error) {
		calls++
		return "v2.0.0", nil
	}
	stale, err := checkModule("example/x", data, latest)
	if err != nil {
		t.Fatalf("checkModule: %v", err)
	}
	if len(stale) != 0 {
		t.Errorf("want 0 stale pins for a replaced module, got %v", stale)
	}
	if calls != 0 {
		t.Errorf("want latest() never called for a replaced module, got %d calls", calls)
	}
}

func TestCheckModule_ReportsStalePin(t *testing.T) {
	data := []byte(`module example/x

go 1.26.5

require smeldr.dev/mcp v1.30.0
`)
	latest := func(module string) (string, error) {
		if module != "smeldr.dev/mcp" {
			t.Fatalf("unexpected module: %s", module)
		}
		return "v1.31.1", nil
	}
	stale, err := checkModule("example/x", data, latest)
	if err != nil {
		t.Fatalf("checkModule: %v", err)
	}
	if len(stale) != 1 {
		t.Fatalf("want 1 stale pin, got %d: %v", len(stale), stale)
	}
	s := stale[0]
	if s.module != "smeldr.dev/mcp" || s.pinned != "v1.30.0" || s.latest != "v1.31.1" || s.dir != "example/x" {
		t.Errorf("got %+v, want dir=example/x module=smeldr.dev/mcp pinned=v1.30.0 latest=v1.31.1", s)
	}
}

func TestCheckModule_CurrentPinNotReported(t *testing.T) {
	data := []byte(`module example/x

go 1.26.5

require smeldr.dev/mcp v1.31.1
`)
	latest := func(_ string) (string, error) { return "v1.31.1", nil }
	stale, err := checkModule("example/x", data, latest)
	if err != nil {
		t.Fatalf("checkModule: %v", err)
	}
	if len(stale) != 0 {
		t.Errorf("want 0 stale pins for a current pin, got %v", stale)
	}
}

func TestCheckModule_SkipsIndirect(t *testing.T) {
	data := []byte(`module example/x

go 1.26.5

require smeldr.dev/oauth v0.2.0 // indirect
`)
	calls := 0
	latest := func(_ string) (string, error) {
		calls++
		return "v0.4.0", nil
	}
	stale, err := checkModule("example/x", data, latest)
	if err != nil {
		t.Fatalf("checkModule: %v", err)
	}
	if len(stale) != 0 {
		t.Errorf("want 0 stale pins for an indirect dependency, got %v", stale)
	}
	if calls != 0 {
		t.Errorf("want latest() never called for an indirect dependency, got %d calls", calls)
	}
}

func TestCheckModule_IgnoresNonSmeldrModules(t *testing.T) {
	data := []byte(`module example/x

go 1.26.5

require github.com/foo/bar v1.0.0
`)
	calls := 0
	latest := func(_ string) (string, error) {
		calls++
		return "v2.0.0", nil
	}
	stale, err := checkModule("example/x", data, latest)
	if err != nil {
		t.Fatalf("checkModule: %v", err)
	}
	if len(stale) != 0 || calls != 0 {
		t.Errorf("want a non-smeldr.dev module skipped entirely, got stale=%v calls=%d", stale, calls)
	}
}

func TestCheckModule_MultiplePinsSomeStale(t *testing.T) {
	data := []byte(`module example/x

go 1.26.5

require (
	smeldr.dev/core v1.75.1
	smeldr.dev/mcp v1.30.0
	smeldr.dev/agent v0.7.1
)

replace smeldr.dev/core => ../..
`)
	latest := func(module string) (string, error) {
		switch module {
		case "smeldr.dev/mcp":
			return "v1.31.1", nil
		case "smeldr.dev/agent":
			return "v0.8.0", nil
		}
		t.Fatalf("unexpected module lookup: %s (core is replaced, should never be looked up)", module)
		return "", nil
	}
	stale, err := checkModule("example/x", data, latest)
	if err != nil {
		t.Fatalf("checkModule: %v", err)
	}
	if len(stale) != 2 {
		t.Fatalf("want 2 stale pins (mcp, agent), got %d: %v", len(stale), stale)
	}
}

func TestCheckModule_LatestLookupError(t *testing.T) {
	data := []byte(`module example/x

go 1.26.5

require smeldr.dev/mcp v1.30.0
`)
	wantErr := errors.New("network error")
	latest := func(_ string) (string, error) { return "", wantErr }
	if _, err := checkModule("example/x", data, latest); err == nil {
		t.Fatal("want error from a failing version lookup, got nil")
	}
}

func TestCheckModule_MalformedGoMod(t *testing.T) {
	data := []byte(`this is not a valid go.mod file {{{`)
	if _, err := checkModule("example/x", data, func(string) (string, error) { return "", nil }); err == nil {
		t.Fatal("want error for a malformed go.mod, got nil")
	}
}

func TestCheckDir_ReadError(t *testing.T) {
	if _, err := checkDir("no-such-directory-xyz", func(string) (string, error) { return "", nil }); err == nil {
		t.Fatal("want error for a missing go.mod, got nil")
	}
}
