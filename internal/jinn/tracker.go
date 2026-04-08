package jinn

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type fileTracker struct {
	times map[string]time.Time
}

func newFileTracker() *fileTracker {
	return &fileTracker{times: make(map[string]time.Time)}
}

func (ft *fileTracker) record(path string, t time.Time) {
	ft.times[path] = t
}

func (ft *fileTracker) checkStale(resolved string) error {
	readTime, tracked := ft.times[resolved]
	if !tracked {
		return nil // new file or never read — allow
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return nil // file removed since read — allow write to recreate
	}
	if info.ModTime().After(readTime) {
		return fmt.Errorf("file modified since last read (mtime changed). Re-read before writing: %s",
			filepath.Base(resolved))
	}
	return nil
}
