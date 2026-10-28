//go:build !windows

package jinn

import (
	"os"
	"syscall"
)

func spillFileIdentity(_ string, info os.FileInfo) (spillIdentity, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st == nil {
		return spillIdentity{}, false
	}
	return spillIdentity{dev: int64(st.Dev), ino: st.Ino}, true
}
