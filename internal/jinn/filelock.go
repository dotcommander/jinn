package jinn

import (
	"os"
	"path/filepath"
	"syscall"
)

// withFileLock serializes cross-process critical sections on an advisory
// flock at lockPath (created 0o600 if absent; parent dir created 0o700).
// Blocking LOCK_EX: holders keep it only for microsecond-scale local JSON
// read-modify-writes, so blocking beats a retry protocol.
// flock is per open-file-description, so it also serializes goroutines in
// one process (each call opens its own fd).
// IMPORTANT: lockPath must never be deleted while in use — callers keep it
// OUTSIDE any directory they RemoveAll (unlink-while-locked lets two
// processes hold the lock on different inodes).
func withFileLock(lockPath string, fn func() error) error {
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }()
	return fn()
}
