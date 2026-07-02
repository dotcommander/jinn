package jinn

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

const (
	spillRegistryMaxEntries = 128
	spillRegistryMaxAge     = 24 * time.Hour
)

type spillRegistryRecord struct {
	Path      string `json:"path"`
	Dev       int64  `json:"dev"`
	Ino       uint64 `json:"ino"`
	Size      int64  `json:"size"`
	MTimeNano int64  `json:"mtime_nano"`
	CreatedAt int64  `json:"created_at"`
}

type spillRegistryFile struct {
	Records []spillRegistryRecord `json:"records"`
}

func spillRegistryPath() (string, error) {
	base := os.Getenv("JINN_CONFIG_DIR")
	if base == "" {
		dir, err := os.UserConfigDir()
		if err != nil {
			return "", fmt.Errorf("spill registry: resolve config dir: %w", err)
		}
		base = dir
	}
	return filepath.Join(base, "jinn", "spills.json"), nil
}

func registerShellSpill(path string) {
	if path == "" {
		return
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return
	}
	ident, ok := spillFileIdentity(info)
	if !ok {
		return
	}
	regPath, err := spillRegistryPath()
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(regPath), 0o700); err != nil {
		return
	}

	// Cross-process lock: concurrent run_shell spills must not drop each
	// other's registrations (read-modify-write below). Reads stay lock-free —
	// atomicWriteJSON renames atomically, so readers always see a consistent
	// snapshot. Best-effort, like the rest of this function.
	_ = withFileLock(regPath+".lock", func() error {
		reg := readSpillRegistry(regPath)
		now := time.Now().Unix()
		rec := spillRegistryRecord{
			Path:      filepath.Clean(path),
			Dev:       ident.dev,
			Ino:       ident.ino,
			Size:      info.Size(),
			MTimeNano: info.ModTime().UnixNano(),
			CreatedAt: now,
		}

		kept := make([]spillRegistryRecord, 0, len(reg.Records)+1)
		for _, existing := range reg.Records {
			if existing.Path == rec.Path || now-existing.CreatedAt > int64(spillRegistryMaxAge.Seconds()) {
				continue
			}
			kept = append(kept, existing)
		}
		kept = append(kept, rec)
		if len(kept) > spillRegistryMaxEntries {
			kept = kept[len(kept)-spillRegistryMaxEntries:]
		}
		reg.Records = kept
		return atomicWriteJSON(regPath, reg)
	})
}

func readSpillRegistry(path string) spillRegistryFile {
	var reg spillRegistryFile
	data, err := os.ReadFile(path)
	if err != nil {
		return reg
	}
	if err := json.Unmarshal(data, &reg); err != nil {
		return spillRegistryFile{}
	}
	return reg
}

func isRegisteredShellSpill(path string) bool {
	clean := filepath.Clean(path)
	if filepath.Dir(clean) != filepath.Clean(os.TempDir()) || !hasShellSpillName(clean) {
		return false
	}
	info, err := os.Lstat(clean)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	ident, ok := spillFileIdentity(info)
	if !ok {
		return false
	}
	regPath, err := spillRegistryPath()
	if err != nil {
		return false
	}
	reg := readSpillRegistry(regPath)
	now := time.Now().Unix()
	for _, rec := range reg.Records {
		if rec.Path != clean {
			continue
		}
		if now-rec.CreatedAt > int64(spillRegistryMaxAge.Seconds()) {
			return false
		}
		return rec.Dev == ident.dev &&
			rec.Ino == ident.ino &&
			rec.Size == info.Size() &&
			rec.MTimeNano == info.ModTime().UnixNano()
	}
	return false
}

func hasShellSpillName(path string) bool {
	return filepath.Base(path) != "" && len(filepath.Base(path)) >= len(spillFilePrefix) &&
		filepath.Base(path)[:len(spillFilePrefix)] == spillFilePrefix
}

type spillIdentity struct {
	dev int64
	ino uint64
}

func spillFileIdentity(info os.FileInfo) (spillIdentity, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st == nil {
		return spillIdentity{}, false
	}
	return spillIdentity{dev: int64(st.Dev), ino: st.Ino}, true
}

func unregisteredSpillErr(path string) error {
	return &ErrWithSuggestion{
		Err:        errors.New("unregistered shell spill file: " + path),
		Suggestion: "read only the exact Full output path returned by run_shell, or rerun the command",
		Code:       ErrCodePathOutsideSandbox,
	}
}
