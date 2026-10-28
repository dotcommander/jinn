package jinn

import (
	"os"

	"golang.org/x/sys/windows"
)

func spillFileIdentity(path string, info os.FileInfo) (spillIdentity, bool) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return spillIdentity{}, false
	}
	handle, err := windows.CreateFile(name, windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return spillIdentity{}, false
	}
	file := os.NewFile(uintptr(handle), path)
	defer func() { _ = file.Close() }()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return spillIdentity{}, false
	}
	var data windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &data); err != nil {
		return spillIdentity{}, false
	}
	if data.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return spillIdentity{}, false
	}
	return spillIdentity{dev: int64(data.VolumeSerialNumber), ino: uint64(data.FileIndexHigh)<<32 | uint64(data.FileIndexLow)}, true
}
