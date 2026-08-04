package jinn

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

func sanitizeExecutablePath(raw string) []string {
	seen := make(map[string]struct{})
	paths := make([]string, 0)
	for _, entry := range filepath.SplitList(raw) {
		if entry == "" || !filepath.IsAbs(entry) {
			continue
		}
		clean := filepath.Clean(entry)
		if _, ok := seen[clean]; ok {
			continue
		}
		info, err := os.Stat(clean)
		if err != nil || !info.IsDir() {
			continue
		}
		seen[clean] = struct{}{}
		paths = append(paths, clean)
	}
	return paths
}

func findExecutableInPath(paths []string, name string) (string, error) {
	if filepath.Base(name) != name {
		return "", errors.New("executable name must not contain a path")
	}
	for _, dir := range paths {
		path := filepath.Join(dir, name)
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0 {
			return path, nil
		}
	}
	return "", fmt.Errorf("%s not found in launch-time PATH", name)
}

// findSystemExecutable is a test-only convenience for probes that intentionally
// inspect the current process PATH; production dispatch must use Engine.execPath.
func findSystemExecutable(name string) (string, error) {
	return findExecutableInPath(sanitizeExecutablePath(os.Getenv("PATH")), name)
}

// ShellMode controls whether and how commands may execute.
type ShellMode string

const (
	// ShellModeDisabled forbids shell execution.
	ShellModeDisabled ShellMode = "disabled"
	// ShellModeSandboxed runs shell commands inside the platform sandbox.
	ShellModeSandboxed ShellMode = "sandboxed"
	// ShellModeUnsafe permits direct host shell execution.
	ShellModeUnsafe ShellMode = "unsafe"
)

// EngineConfig is the explicit security policy for an Engine.
type EngineConfig struct {
	Version   string
	ShellMode ShellMode
	// UnsafeAllowMutationWithoutPreconditions is an explicit compatibility
	// escape hatch for trusted embedders and tests. Production callers should
	// leave it false so every mutation supplies a current assertion.
	UnsafeAllowMutationWithoutPreconditions bool
}

// ParseShellMode validates a shell-mode flag value.
func ParseShellMode(value string) (ShellMode, error) {
	mode := ShellMode(value)
	switch mode {
	case ShellModeDisabled, ShellModeSandboxed, ShellModeUnsafe:
		return mode, nil
	default:
		return "", fmt.Errorf("invalid shell mode %q: use disabled, sandboxed, or unsafe", value)
	}
}

func validateShellMode(mode ShellMode) (string, error) {
	if mode == "" {
		mode = ShellModeDisabled
	}
	switch mode {
	case ShellModeDisabled, ShellModeUnsafe:
		return "", nil
	case ShellModeSandboxed:
		switch runtime.GOOS {
		case "darwin":
			const path = "/usr/bin/sandbox-exec"
			if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
				return "", fmt.Errorf("sandboxed shell unavailable: %s is not installed", path)
			}
			return path, nil
		case "linux":
			for _, path := range []string{"/usr/bin/bwrap", "/bin/bwrap"} {
				if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
					return path, nil
				}
			}
			return "", errors.New("sandboxed shell unavailable: bwrap is not installed in a system path")
		default:
			return "", fmt.Errorf("sandboxed shell is unsupported on %s", runtime.GOOS)
		}
	default:
		return "", fmt.Errorf("invalid shell mode %q", mode)
	}
}

// ShellMode reports the engine's configured shell execution policy.
func (e *Engine) ShellMode() ShellMode { return e.shellMode }
