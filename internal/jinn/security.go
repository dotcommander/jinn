package jinn

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

var sensitiveSegments = []string{"/.git/", "/.ssh/", "/.aws/", "/.gnupg/"}
var sensitiveDirs = []string{".git", ".ssh", ".aws", ".gnupg"}

// sensitivePathErr builds the standard "blocked path" error with
// a single canonical suggestion string shared by all sensitive-path guards.
func sensitivePathErr(p string) error {
	return &ErrWithSuggestion{
		Err:        fmt.Errorf("sensitive path: %s", p),
		Suggestion: "this path is blocked for security; request the specific field or artifact from the user instead",
		Code:       ErrCodePathOutsideSandbox,
	}
}

func (e *Engine) rootRelative(resolved string) (string, error) {
	rel, err := filepath.Rel(e.workDir, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path is outside workspace root: %s", resolved)
	}
	if rel == "" {
		rel = "."
	}
	return rel, nil
}

func (e *Engine) rootedStat(resolved string) (os.FileInfo, error) {
	rel, err := e.rootRelative(resolved)
	if err != nil {
		return nil, err
	}
	return e.root.Stat(rel)
}

func (e *Engine) rootedReadFile(resolved string, maxBytes int64) ([]byte, os.FileInfo, error) {
	file, info, err := e.openRegularFile(resolved, maxBytes, false)
	if err != nil {
		return nil, info, err
	}
	defer func() { _ = file.Close() }()
	reader := io.Reader(file)
	if maxBytes >= 0 {
		reader = io.LimitReader(file, maxBytes+1)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, info, err
	}
	if maxBytes >= 0 && int64(len(data)) > maxBytes {
		return nil, info, &ErrWithSuggestion{Err: fmt.Errorf("file exceeded %d bytes while reading", maxBytes), Suggestion: "use a smaller regular file", Code: ErrCodeFileTooLarge}
	}
	return data, info, nil
}

func (e *Engine) readRegularFile(resolved string, maxBytes int64) ([]byte, os.FileInfo, error) {
	file, info, err := e.openRegularFile(resolved, maxBytes, true)
	if err != nil {
		return nil, info, err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return nil, info, err
	}
	if int64(len(data)) > maxBytes {
		return nil, info, &ErrWithSuggestion{Err: fmt.Errorf("file exceeded %d bytes while reading", maxBytes), Suggestion: "use a smaller regular file", Code: ErrCodeFileTooLarge}
	}
	return data, info, nil
}

func (e *Engine) openRegularFile(resolved string, maxBytes int64, allowSpill bool) (*os.File, os.FileInfo, error) {
	var (
		file   *os.File
		err    error
		inRoot bool
	)
	if rel, relErr := e.rootRelative(resolved); relErr == nil {
		inRoot = true
		file, err = e.root.OpenFile(rel, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	} else if allowSpill {
		file, err = os.OpenFile(resolved, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	} else {
		return nil, nil, relErr
	}
	if err != nil {
		return nil, nil, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, info, fmt.Errorf("not a regular file: %s", resolved)
	}
	if !inRoot && !isRegisteredShellSpillFile(filepath.Clean(resolved), info) {
		_ = file.Close()
		return nil, info, unregisteredSpillErr(resolved)
	}
	if maxBytes >= 0 && info.Size() > maxBytes {
		_ = file.Close()
		return nil, info, &ErrWithSuggestion{Err: fmt.Errorf("file too large: %d bytes (max %d)", info.Size(), maxBytes), Suggestion: "use a smaller regular file", Code: ErrCodeFileTooLarge}
	}
	return file, info, nil
}

func (e *Engine) readRegularPrefix(resolved string, maxBytes int) ([]byte, os.FileInfo, error) {
	file, info, err := e.openRegularFile(resolved, -1, true)
	if err != nil {
		return nil, info, err
	}
	defer func() { _ = file.Close() }()
	buf := make([]byte, maxBytes)
	n, err := file.Read(buf)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, info, err
	}
	return buf[:n], info, nil
}

// checkPathForMutation rejects every symlink component, including in-root
// symlinks that are intentionally allowed by explicit read operations.
func (e *Engine) checkPathForMutation(path string) (string, error) {
	// Reject lexical symlinks before resolving the path. Resolving first would
	// erase an in-workspace alias and let a write target the alias destination.
	lexical, err := e.resolvePath(path)
	if err != nil {
		return "", err
	}
	if _, relErr := e.rootRelative(lexical); relErr != nil {
		return "", &ErrWithSuggestion{Err: fmt.Errorf("%s is outside working directory", path), Suggestion: "path resolves outside the sandbox root; supply a path inside the workdir", Code: ErrCodePathOutsideSandbox}
	}
	if _, relErr := e.rootRelative(lexical); relErr == nil {
		if symlinkErr := e.rejectMutationSymlinkComponents(lexical, path); symlinkErr != nil {
			return "", symlinkErr
		}
	}
	resolved, err := e.checkPath(path)
	if err != nil {
		return "", err
	}
	if err := e.rejectMutationSymlinkComponents(resolved, path); err != nil {
		return "", err
	}
	return resolved, nil
}

func (e *Engine) rejectMutationSymlinkComponents(path, display string) error {
	rel, err := e.rootRelative(path)
	if err != nil {
		return err
	}
	current := ""
	parts := strings.Split(filepath.Clean(rel), string(filepath.Separator))
	for _, part := range parts {
		if part == "." || part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, statErr := e.root.Lstat(current)
		if errors.Is(statErr, os.ErrNotExist) {
			continue
		}
		if statErr != nil {
			return statErr
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return &ErrWithSuggestion{Err: fmt.Errorf("mutation path contains symlink component: %s", display), Suggestion: "target a real path within the workspace", Code: ErrCodePathOutsideSandbox}
		}
	}
	return nil
}

// permissionDeniedErr builds the standard "permission denied" error with the
// single canonical suggestion string shared by all readable-file guards.
func permissionDeniedErr(path string) error {
	return &ErrWithSuggestion{
		Err:        fmt.Errorf("permission denied: %s", path),
		Suggestion: "file is not readable by the sandbox; check ownership or choose a different file",
		Code:       ErrCodePermissionDenied,
	}
}

func hasSensitivePathSegment(p string) bool {
	clean := filepath.Clean(p)
	for _, seg := range sensitiveSegments {
		if strings.Contains(clean, seg) {
			return true
		}
	}
	base := filepath.Base(clean)
	for _, dir := range sensitiveDirs {
		if base == dir {
			return true
		}
	}
	return base == ".env" || strings.HasPrefix(base, ".env.")
}

func (e *Engine) resolvePath(p string) (string, error) {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("expand ~: %w", err)
		}
		p = home + p[1:]
	}
	if !strings.HasPrefix(p, "/") {
		p = filepath.Join(e.workDir, p)
	}
	return filepath.Clean(p), nil
}

// checkPathForRead is like checkPath but also allows reading jinn's own
// shell spill files. Spill files live in os.TempDir() and are named with
// spillFilePrefix — they are jinn's own output, equivalent to what run_shell
// can already return inline. Write and exec paths must use checkPath directly.
func (e *Engine) checkPathForRead(p string) (string, error) {
	resolved, err := e.resolvePath(p)
	if err != nil {
		return "", err
	}
	// Spill-file exemption: read-only access to jinn's own registered tmp
	// output. Prefix alone is not proof: callers can create temp symlinks or
	// regular files with this name. The registry binds a path to the inode that
	// run_shell created before read_file accepts it outside the workdir.
	if filepath.Dir(resolved) == filepath.Clean(os.TempDir()) &&
		strings.HasPrefix(filepath.Base(resolved), spillFilePrefix) {
		if isRegisteredShellSpill(resolved) {
			return resolved, nil
		}
		return "", unregisteredSpillErr(p)
	}
	return e.checkPath(p)
}

func (e *Engine) checkPath(p string) (string, error) {
	resolved, err := e.resolvePath(p)
	if err != nil {
		return "", err
	}

	real, err := resolveExistingPrefix(resolved)
	if err != nil {
		return "", &ErrWithSuggestion{
			Code:       ErrCodePathOutsideSandbox,
			Err:        fmt.Errorf("symlink target is outside the sandbox: %s", p),
			Suggestion: "symlink target is outside the sandbox; follow the symlink manually via its absolute path if authorized",
		}
	}

	// Check sensitive segments on the resolved path.
	if hasSensitivePathSegment(real) {
		return "", sensitivePathErr(p)
	}

	// Check workdir boundary on the resolved path.
	if !strings.HasPrefix(real, e.workDir+"/") && real != e.workDir {
		return "", &ErrWithSuggestion{
			Err:        fmt.Errorf("%s is outside working directory", p),
			Suggestion: "path resolves outside the sandbox root; supply a path inside the workdir",
			Code:       ErrCodePathOutsideSandbox,
		}
	}
	return real, nil
}

func resolveExistingPrefix(path string) (string, error) {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return real, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}

	var missing []string
	current := path
	for {
		parent := filepath.Dir(current)
		if parent == current {
			return filepath.Clean(path), nil
		}
		missing = append(missing, filepath.Base(current))
		current = parent
		real, err := filepath.EvalSymlinks(current)
		if err == nil {
			for i := len(missing) - 1; i >= 0; i-- {
				real = filepath.Join(real, missing[i])
			}
			return real, nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
	}
}
