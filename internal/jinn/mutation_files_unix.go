//go:build !windows

package jinn

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func unlinkRootedFile(_ *os.Root, parent *os.File, relative string) error {
	return unix.Unlinkat(int(parent.Fd()), filepath.Base(relative), 0)
}

func syncMutationDirectory(parent *os.File) error { return parent.Sync() }
