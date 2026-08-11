//go:build !windows

package mcpsnapshot

import (
	"os"
	"syscall"
)

func withLock(path string, fn func() error) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	if err := file.Chmod(0o600); err != nil {
		return err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer func() { _ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN) }()
	return fn()
}
