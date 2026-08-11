//go:build windows

package main

import (
	"os"

	"golang.org/x/sys/windows"
)

func withMCPLogLock(lockPath string, fn func() error) error {
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	overlapped := new(windows.Overlapped)
	handle := windows.Handle(file.Fd())
	if err := windows.LockFileEx(handle, windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, overlapped); err != nil {
		return err
	}
	defer func() { _ = windows.UnlockFileEx(handle, 0, 1, 0, overlapped) }()
	return fn()
}
