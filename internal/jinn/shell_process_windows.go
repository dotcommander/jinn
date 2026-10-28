package jinn

import (
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

type shellProcessGroup struct {
	command *exec.Cmd
	job     windows.Handle
}

func prepareShellProcessGroup(command *exec.Cmd) (*shellProcessGroup, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		_ = windows.CloseHandle(job)
		return nil, err
	}
	// Start suspended so no descendant can escape before job assignment.
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED | windows.CREATE_NEW_PROCESS_GROUP}
	return &shellProcessGroup{command: command, job: job}, nil
}

func (group *shellProcessGroup) start() error {
	if err := group.command.Start(); err != nil {
		return err
	}
	pid := uint32(group.command.Process.Pid)
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, pid)
	if err == nil {
		err = windows.AssignProcessToJobObject(group.job, process)
		_ = windows.CloseHandle(process)
	}
	if err == nil {
		err = resumeShellProcess(pid)
	}
	if err != nil {
		group.kill()
		_ = group.command.Wait()
		return fmt.Errorf("attach shell process tree: %w", err)
	}
	return nil
}

func (group *shellProcessGroup) kill() {
	_ = windows.TerminateJobObject(group.job, 1)
	if group.command.Process != nil {
		_ = group.command.Process.Kill()
	}
}

func (group *shellProcessGroup) close() { _ = windows.CloseHandle(group.job) }

func resumeShellProcess(pid uint32) error {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return err
	}
	defer func() { _ = windows.CloseHandle(snapshot) }()
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	for err = windows.Thread32First(snapshot, &entry); err == nil; err = windows.Thread32Next(snapshot, &entry) {
		if entry.OwnerProcessID != pid {
			continue
		}
		thread, openErr := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
		if openErr != nil {
			return openErr
		}
		_, resumeErr := windows.ResumeThread(thread)
		_ = windows.CloseHandle(thread)
		return resumeErr
	}
	if err != nil && !errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return err
	}
	return errors.New("suspended shell thread not found")
}
