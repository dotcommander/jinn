package jinn

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func waitForProcessExit(t *testing.T, pid int) {
	t.Helper()
	process, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_TERMINATE, false, uint32(pid))
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) || errors.Is(err, windows.ERROR_NOT_FOUND) {
		return
	}
	if err != nil {
		t.Fatalf("open background process %d: %v", pid, err)
	}
	defer func() { _ = windows.CloseHandle(process) }()
	status, err := windows.WaitForSingleObject(process, 3000)
	if err == nil && status == windows.WAIT_OBJECT_0 {
		return
	}
	_ = windows.TerminateProcess(process, 1)
	t.Fatalf("background process %d survived cancellation: wait status %d, error %v", pid, status, err)
}

func TestWindowsShellProcessFixture(t *testing.T) {
	if len(os.Args) > 1 {
		switch os.Args[len(os.Args)-1] {
		case "jinn-shell-child":
			fmt.Println(os.Getpid())
			<-time.After(24 * time.Hour)
			os.Exit(0)
		case "jinn-shell-parent":
			executable, err := os.Executable()
			if err != nil {
				os.Exit(2)
			}
			child := exec.Command(executable, "-test.run=^TestWindowsShellProcessFixture$", "--", "jinn-shell-child")
			child.Stdout = os.Stdout
			if err := child.Run(); err != nil {
				os.Exit(3)
			}
			os.Exit(0)
		}
	}
	t.Parallel()
}

func TestWindowsShellProcessGroupTerminatesDescendants(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("native Windows process integration")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(executable, "-test.run=^TestWindowsShellProcessFixture$", "--", "jinn-shell-parent")
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close(); _ = writer.Close() })
	command.Stdout = writer
	group, err := prepareShellProcessGroup(command)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(group.close)
	if err := group.start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(group.kill)
	if err := reader.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(reader).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatal(err)
	}
	child, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = windows.CloseHandle(child) })
	group.kill()
	_ = command.Wait()
	if status, err := windows.WaitForSingleObject(child, 1000); err != nil || status != windows.WAIT_OBJECT_0 {
		t.Fatalf("descendant status=%d, error=%v", status, err)
	}
}
