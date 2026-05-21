//go:build windows

package jinn

import (
	"os/exec"
	"sync"
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
	// mu serializes kill() (TerminateJobObject) against cleanup()
	// (CloseHandle + zero of job): the timer goroutine and the main
	// goroutine can otherwise touch the same handle concurrently.
	mu  sync.Mutex
	job syscall.Handle
}

var (
	kernel32                  = syscall.NewLazyDLL("kernel32.dll")
	procCreateJobObjectW      = kernel32.NewProc("CreateJobObjectW")
	procSetInformationJobObj  = kernel32.NewProc("SetInformationJobObject")
	procAssignProcessToJobObj = kernel32.NewProc("AssignProcessToJobObject")
	procTerminateJobObject    = kernel32.NewProc("TerminateJobObject")
)

func configureProcessGroup(_ *exec.Cmd) *processTree {
	job, _, _ := procCreateJobObjectW.Call(0, 0)
	if job == 0 {
		return &processTree{}
	}

	info := jobObjectExtendedLimitInformation{}
	info.BasicLimitInformation.LimitFlags = jobObjectLimitKillOnJob
	ret, _, _ := procSetInformationJobObj.Call(
		job,
		uintptr(jobObjectExtendedLimit),
		uintptr(unsafe.Pointer(&info)),
		unsafe.Sizeof(info),
	)
	if ret == 0 {
		_ = syscall.CloseHandle(syscall.Handle(job))
		return &processTree{}
	}

	return &processTree{job: syscall.Handle(job)}
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
	// The child (bash) is assigned to the job only AFTER c.Start(); any
	// grandchildren spawned in the Start()→AssignProcessToJobObject window
	// are not retroactively captured. The Unix path has no such gap because
	// Setpgid is set before Start(). The proper fix (CREATE_SUSPENDED +
	// assign + resume) is not reachable through Go's os/exec.
	ret, _, _ := procAssignProcessToJobObj.Call(uintptr(p.job), uintptr(proc))
	if ret == 0 {
		// Fall back to direct process kill on timeout when assignment fails.
		p.mu.Lock()
		if p.job != 0 {
			_ = syscall.CloseHandle(p.job)
			p.job = 0
		}
		p.mu.Unlock()
	}
}

func (p *processTree) cleanup() {
	// Closing the last job handle terminates surviving children too, because
	// JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE is set — so on normal (non-timeout)
	// completion this also reaps backgrounded children, unlike the Unix path
	// which leaves backgrounded survivors alone on normal exit. Intentional.
	p.mu.Lock()
	if p.job != 0 {
		_ = syscall.CloseHandle(p.job)
		p.job = 0
	}
	p.mu.Unlock()
}

func (p *processTree) kill(c *exec.Cmd) {
	// Hold mu across TerminateJobObject so cleanup() cannot CloseHandle the
	// job mid-syscall (handle values are recycled — a use-after-close could
	// terminate an unrelated job). A no-op job (cleanup already ran, or
	// assignment failed) falls through to the process-kill fallback below.
	p.mu.Lock()
	if p.job != 0 {
		ret, _, _ := procTerminateJobObject.Call(uintptr(p.job), 1)
		if ret != 0 {
			p.mu.Unlock()
			return
		}
	}
	p.mu.Unlock()
	if c.Process != nil {
		_ = c.Process.Kill()
	}
}
