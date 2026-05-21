//go:build !windows

package jinn

import (
	"os/exec"
	"syscall"
)

type processTree struct{}

func configureProcessGroup(c *exec.Cmd) processTree {
	// Always use a process group so SIGKILL reaches background processes too.
	// exec.CommandContext only kills the direct child; our timer kills -pgid.
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return processTree{}
}

func (processTree) afterStart(_ *exec.Cmd) {}

func (processTree) cleanup() {}

func (processTree) kill(c *exec.Cmd) {
	if c.Process == nil {
		return
	}
	pgid := c.Process.Pid // bash is the group leader (Setpgid=true)
	// Negative pgid targets the whole process group.
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
}
