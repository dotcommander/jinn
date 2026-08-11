package mcpexplore

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// RegistryPath returns the user-only registry path without creating it.
func RegistryPath() (string, error) {
	base := os.Getenv("JINN_CONFIG_DIR")
	if base == "" {
		var err error
		base, err = os.UserConfigDir()
		if err != nil {
			return "", fmt.Errorf("resolve MCP config directory: %w", err)
		}
	}
	return filepath.Join(base, "jinn", "mcp", "servers.json"), nil
}

// LoadRegistry reads and validates the registry. exists is false for a missing file.
func LoadRegistry() (registry Registry, exists bool, err error) {
	path, err := RegistryPath()
	if err != nil {
		return Registry{}, false, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Registry{Version: registryVersion, Servers: map[string]Server{}}, false, nil
	}
	if err != nil {
		return Registry{}, false, fmt.Errorf("read MCP server registry: %w", err)
	}
	registry, err = decodeRegistry(data)
	if err != nil {
		return Registry{}, true, fmt.Errorf("invalid MCP server registry: %w", err)
	}
	return registry, true, nil
}

// SaveRegistry replaces a valid registry through a durable lock, backup, and rename.
func SaveRegistry(registry Registry) error {
	if err := validateRegistry(registry); err != nil {
		return err
	}
	return mutateRegistry(func(current *Registry) error {
		*current = registry
		return nil
	})
}

// UpdateRegistry serializes a read-modify-write change. It never replaces a corrupt current registry.
func UpdateRegistry(update func(*Registry) error) error { return mutateRegistry(update) }

func mutateRegistry(update func(*Registry) error) error {
	path, err := RegistryPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create MCP registry directory: %w", err)
	}
	if err := os.Chmod(filepath.Dir(path), 0o700); err != nil { //nolint:gosec // Registry directories must be owner-only and traversable.
		return fmt.Errorf("set MCP registry directory permissions: %w", err)
	}
	return withRegistryLock(path+".lock", func() error {
		current, err := os.ReadFile(path)
		registry := Registry{Version: registryVersion, Servers: map[string]Server{}}
		if err == nil {
			registry, err = decodeRegistry(current)
			if err != nil {
				return fmt.Errorf("refusing to overwrite corrupt MCP server registry: %w", err)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("read MCP server registry before write: %w", err)
		}
		if updateErr := update(&registry); updateErr != nil {
			return updateErr
		}
		if validateErr := validateRegistry(registry); validateErr != nil {
			return validateErr
		}
		if len(current) != 0 {
			if writeErr := durableWrite(path+".bak", current); writeErr != nil {
				return fmt.Errorf("backup MCP server registry: %w", writeErr)
			}
		}
		data, err := json.MarshalIndent(registry, "", "  ")
		if err != nil {
			return fmt.Errorf("encode MCP server registry: %w", err)
		}
		return durableWrite(path, append(data, '\n'))
	})
}

func durableWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	temporary, err := os.CreateTemp(dir, ".servers-")
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
	directory, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer func() { _ = directory.Close() }()
	if err := directory.Sync(); err != nil {
		return err
	}
	committed = true
	return nil
}
