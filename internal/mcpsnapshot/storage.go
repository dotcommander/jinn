package mcpsnapshot

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/dotcommander/jinn/internal/mcpexplore"
)

// Path returns the approval snapshot path without creating any directory.
func Path(alias string) (string, error) {
	registryPath, err := mcpexplore.RegistryPath()
	if err != nil {
		return "", err
	}
	if err := mcpexplore.ValidateAlias(alias); err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(registryPath), "snapshots", alias+".json"), nil
}

// Load reads one validated approval snapshot. exists is false only when absent.
func Load(alias string) (Snapshot, bool, error) {
	path, err := Path(alias)
	if err != nil {
		return Snapshot{}, false, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Snapshot{}, false, nil
	}
	if err != nil {
		return Snapshot{}, false, fmt.Errorf("read MCP snapshot: %w", err)
	}
	var snapshot Snapshot
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&snapshot); err != nil {
		return Snapshot{}, true, fmt.Errorf("invalid MCP snapshot: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Snapshot{}, true, errors.New("invalid MCP snapshot: must contain exactly one JSON object")
	}
	if err := snapshot.Validate(); err != nil {
		return Snapshot{}, true, fmt.Errorf("invalid MCP snapshot: %w", err)
	}
	if snapshot.Alias != alias {
		return Snapshot{}, true, fmt.Errorf("invalid MCP snapshot: alias %q does not match requested alias %q", snapshot.Alias, alias)
	}
	return snapshot, true, nil
}

// Save replaces a valid snapshot atomically and leaves the last valid contents in .bak.
func Save(snapshot Snapshot) error {
	if err := snapshot.Validate(); err != nil {
		return err
	}
	path, err := Path(snapshot.Alias)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create MCP snapshot directory: %w", err)
	}
	if err := os.Chmod(filepath.Dir(path), 0o700); err != nil { //nolint:gosec // Snapshot directories must be owner-only and traversable.
		return err
	}
	return withLock(path+".lock", func() error {
		previous, exists, err := Load(snapshot.Alias)
		if err != nil {
			return fmt.Errorf("refusing to overwrite corrupt MCP snapshot: %w", err)
		}
		if exists {
			oldData, canonicalErr := CanonicalJSON(previous)
			if canonicalErr != nil {
				return canonicalErr
			}
			if backupErr := durableWrite(path+".bak", append(oldData, '\n')); backupErr != nil {
				return fmt.Errorf("backup MCP snapshot: %w", backupErr)
			}
		}
		data, err := CanonicalJSON(snapshot)
		if err != nil {
			return err
		}
		return durableWrite(path, append(data, '\n'))
	})
}

func durableWrite(path string, data []byte) error {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".snapshot-")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()
	if chmodErr := temporary.Chmod(0o600); chmodErr != nil {
		_ = temporary.Close()
		return chmodErr
	}
	if _, writeErr := temporary.Write(data); writeErr != nil {
		_ = temporary.Close()
		return writeErr
	}
	if syncErr := temporary.Sync(); syncErr != nil {
		_ = temporary.Close()
		return syncErr
	}
	if closeErr := temporary.Close(); closeErr != nil {
		return closeErr
	}
	if renameErr := os.Rename(temporaryPath, path); renameErr != nil {
		return renameErr
	}
	directoryHandle, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer func() { _ = directoryHandle.Close() }()
	if err := directoryHandle.Sync(); err != nil {
		return err
	}
	committed = true
	return nil
}
