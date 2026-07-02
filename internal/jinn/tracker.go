package jinn

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// fileStamp is the recorded identity of a file at read/write time. Size is
// tracked alongside mtime because mtime has filesystem-tick granularity: a
// rewrite landing in the same tick as the recorded read is invisible to the
// mtime check alone.
type fileStamp struct {
	mtime time.Time
	size  int64
}

type fileTracker struct {
	stamps map[string]fileStamp
}

func newFileTracker() *fileTracker {
	return &fileTracker{stamps: make(map[string]fileStamp)}
}

func (ft *fileTracker) record(path string, mtime time.Time, size int64) {
	ft.stamps[path] = fileStamp{mtime: mtime, size: size}
}

func (ft *fileTracker) checkStale(resolved string) error {
	rec, tracked := ft.stamps[resolved]
	if !tracked {
		return nil // new file or never read — allow
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return nil //nolint:nilerr // file removed since read — allow write to recreate
	}
	if info.ModTime().After(rec.mtime) || info.Size() != rec.size {
		return fmt.Errorf("file modified since last read (mtime or size changed). Re-read before writing: %s",
			filepath.Base(resolved))
	}
	return nil
}
