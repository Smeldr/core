// AGPL-3.0-or-later

//go:build windows

package main

import (
	"os/exec"
	"strconv"
)

// setGroup has nothing to set on Windows; killTree ends the tree instead.
func setGroup(c *exec.Cmd) {}

// killTree ends bash and every process it started.
func killTree(c *exec.Cmd) {
	if c.Process != nil {
		exec.Command("taskkill", "/PID", strconv.Itoa(c.Process.Pid), "/T", "/F").Run()
	}
}
