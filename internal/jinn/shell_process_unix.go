//go:build !windows

package jinn

import (
	"os/exec"
	"syscall"
)

type shellProcessGroup struct{ command *exec.Cmd }

func prepareShellProcessGroup(command *exec.Cmd) (*shellProcessGroup, error) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return &shellProcessGroup{command: command}, nil
}

func (group *shellProcessGroup) start() error { return group.command.Start() }
func (group *shellProcessGroup) close()       {}
func (group *shellProcessGroup) kill() {
	if process := group.command.Process; process != nil {
		_ = syscall.Kill(-process.Pid, syscall.SIGKILL)
		_ = process.Kill()
	}
}
