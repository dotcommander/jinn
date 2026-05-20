//go:build windows

package jinn

import (
	"os/exec"
	"syscall"
	"unsafe"
)

const (
	processTerminate        = 0x0001
	processSetQuota         = 0x0100
	jobObjectLimitKillOnJob = 0x00002000
	jobObjectExtendedLimit  = 9
)

type jobObjectExtendedLimitInformation struct {
	BasicLimitInformation struct {
		PerProcessUserTimeLimit int64
		PerJobUserTimeLimit     int64
		LimitFlags              uint32
		MinimumWorkingSetSize   uintptr
		MaximumWorkingSetSize   uintptr
		ActiveProcessLimit      uint32
		Affinity                uintptr
		PriorityClass           uint32
		SchedulingClass         uint32
	}
	IoInfo struct {
		ReadOperationCount  uint64
		WriteOperationCount uint64
		OtherOperationCount uint64
		ReadTransferCount   uint64
		WriteTransferCount  uint64
		OtherTransferCount  uint64
	}
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}

type processTree struct {
	job syscall.Handle
}

var (
	kernel32                  = syscall.NewLazyDLL("kernel32.dll")
	procCreateJobObjectW      = kernel32.NewProc("CreateJobObjectW")
	procSetInformationJobObj  = kernel32.NewProc("SetInformationJobObject")
	procAssignProcessToJobObj = kernel32.NewProc("AssignProcessToJobObject")
	procTerminateJobObject    = kernel32.NewProc("TerminateJobObject")
)

func configureProcessGroup(_ *exec.Cmd) processTree {
	job, _, err := procCreateJobObjectW.Call(0, 0)
	if job == 0 {
		_ = err
		return processTree{}
	}

	info := jobObjectExtendedLimitInformation{}
	info.BasicLimitInformation.LimitFlags = jobObjectLimitKillOnJob
	ret, _, err := procSetInformationJobObj.Call(
		job,
		uintptr(jobObjectExtendedLimit),
		uintptr(unsafe.Pointer(&info)),
		unsafe.Sizeof(info),
	)
	if ret == 0 {
		_ = err
		_ = syscall.CloseHandle(syscall.Handle(job))
		return processTree{}
	}

	return processTree{job: syscall.Handle(job)}
}

func (p *processTree) afterStart(c *exec.Cmd) {
	if p.job == 0 || c.Process == nil {
		return
	}
	proc, err := syscall.OpenProcess(processTerminate|processSetQuota, false, uint32(c.Process.Pid))
	if err != nil {
		return
	}
	defer syscall.CloseHandle(proc)
	ret, _, _ := procAssignProcessToJobObj.Call(uintptr(p.job), uintptr(proc))
	if ret == 0 {
		// Fall back to direct process kill on timeout when assignment fails.
		_ = syscall.CloseHandle(p.job)
		p.job = 0
	}
}

func (p *processTree) cleanup() {
	if p.job != 0 {
		_ = syscall.CloseHandle(p.job)
	}
}

func (p *processTree) kill(c *exec.Cmd) {
	if p.job != 0 {
		ret, _, _ := procTerminateJobObject.Call(uintptr(p.job), 1)
		if ret != 0 {
			return
		}
	}
	if c.Process != nil {
		_ = c.Process.Kill()
	}
}
