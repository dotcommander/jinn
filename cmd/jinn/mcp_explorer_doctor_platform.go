package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

func doctorPOSIXModeMismatch(mode, expected os.FileMode) bool {
	// Windows FileMode does not describe ACL security.
	return doctorModeMismatch(runtime.GOOS, mode, expected)
}

func doctorModeMismatch(platform string, mode, expected os.FileMode) bool {
	return platform != "windows" && mode.Perm() != expected
}

func doctorCommandExecutable(command string) error {
	if !strings.ContainsAny(command, `/\`) {
		if _, err := exec.LookPath(command); err != nil {
			return fmt.Errorf("registered command lookup failed: %w", err)
		}
		return nil
	}
	info, err := os.Stat(command)
	if err != nil {
		return fmt.Errorf("registered command unavailable: %w", err)
	}
	if info.IsDir() || (runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0) {
		return fmt.Errorf("registered command is not executable: %s", command)
	}
	return nil
}
