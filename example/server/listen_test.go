package main

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
)

// The ready line is logged only after the bind (T1000): a free port logs it
// with the bound address, a taken port returns the error and logs nothing.
func TestListenAndAnnounce(t *testing.T) {
	var logged []string
	logf := func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) }

	ln, err := listenAndAnnounce("127.0.0.1:0", logf)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if len(logged) != 1 || logged[0] != "smeldr-server: listening on "+ln.Addr().String() {
		t.Fatalf("logged = %q, want the ready line with the bound address", logged)
	}

	logged = nil
	busy, err := listenAndAnnounce(ln.Addr().String(), logf)
	if err == nil {
		busy.Close()
		t.Fatal("a taken port: want an error")
	}
	if len(logged) != 0 {
		t.Errorf("a taken port logged %q, want nothing", logged)
	}
	if !strings.Contains(err.Error(), "listen "+ln.Addr().String()) || !strings.Contains(err.Error(), "set PORT or ADDR") {
		t.Errorf("err = %v, want the address and the PORT hint", err)
	}
	var opErr *net.OpError
	if !errors.As(err, &opErr) {
		t.Errorf("err = %v, want it to wrap the *net.OpError", err)
	}
}
