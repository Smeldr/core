// AGPL-3.0-or-later

//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

// setGroup puts bash and everything it starts in a process group of its own,
// so a run that is cut off can end all of it.
func setGroup(c *exec.Cmd) { c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

// killTree ends bash's process group.
func killTree(c *exec.Cmd) {
	if c.Process != nil {
		syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
	}
}
