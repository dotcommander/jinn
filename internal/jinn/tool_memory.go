package jinn

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"
)

const (
	memoryMaxValueBytes = 16384   // 16 KiB per value
	memoryMaxFileBytes  = 1048576 // 1 MiB total file
)

var validKeyRe = regexp.MustCompile(`^[a-zA-Z0-9_.\\-]{1,128}$`)

type memoryEntry struct {
	Value   string `json:"value"`
	Updated string `json:"updated"` // RFC3339
}

type memoryFile struct {
	Version int                    `json:"version"`
	Entries map[string]memoryEntry `json:"entries"`
}

// memoryPath resolves the memory file path.
// Checks JINN_CONFIG_DIR first for test isolation; falls back to os.UserConfigDir().
func memoryPath() (string, error) {
	base := os.Getenv("JINN_CONFIG_DIR")
	if base == "" {
		dir, err := os.UserConfigDir()
		if err != nil {
			return "", fmt.Errorf("memory: resolve config dir: %w", err)
		}
		base = dir
	}
	return filepath.Join(base, "jinn", "memory.json"), nil
}

// loadMemory reads and unmarshals the memory file.
// Returns an empty struct (no error) when the file does not exist.
func loadMemory() (memoryFile, error) {
	path, err := memoryPath()
	if err != nil {
		return memoryFile{}, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return memoryFile{Version: 1, Entries: map[string]memoryEntry{}}, nil
	}
	if err != nil {
		return memoryFile{}, fmt.Errorf("memory: read: %w", err)
	}
	var m memoryFile
	if err := json.Unmarshal(data, &m); err != nil {
		return memoryFile{}, fmt.Errorf("memory: unmarshal: %w", err)
	}
	if m.Entries == nil {
		m.Entries = map[string]memoryEntry{}
	}
	return m, nil
}

// saveMemory atomically writes the memory file via temp+fsync+rename.
func saveMemory(m memoryFile) error {
	path, err := memoryPath()
	if err != nil {
		return err
	}
	if err := atomicWriteJSON(path, m, 0o600); err != nil {
		return fmt.Errorf("memory: %w", err)
	}
	return nil
}

// validateKey checks key charset and length.
func validateKey(key string) error {
	if key == "" || !validKeyRe.MatchString(key) {
		return &ErrWithSuggestion{
			Err:        fmt.Errorf("invalid key: %q — only [a-zA-Z0-9_.-] allowed, 1-128 chars", key),
			Suggestion: `use only letters, digits, underscores, dots, and hyphens in key names`,
		}
	}
	return nil
}

// memoryTool implements the memory tool: save/recall/list/forget.
func (e *Engine) memoryTool(args map[string]interface{}) (string, error) {
	action, _ := args["action"].(string)
	switch action {
	case "save":
		return e.memorySave(args)
	case "recall":
		return e.memoryRecall(args)
	case "list":
		return e.memoryList()
	case "forget":
		return e.memoryForget(args)
	default:
		return "", &ErrWithSuggestion{
			Err:        fmt.Errorf("unknown action: %q", action),
			Suggestion: `use action="save", "recall", "list", or "forget"`,
		}
	}
}

func (e *Engine) memorySave(args map[string]interface{}) (string, error) {
	key, _ := args["key"].(string)
	if err := validateKey(key); err != nil {
		return "", err
	}
	value, _ := args["value"].(string)
	if len(value) > memoryMaxValueBytes {
		return "", &ErrWithSuggestion{
			Err:        fmt.Errorf("value exceeds 16 KiB limit (%d bytes)", len(value)),
			Suggestion: "trim the value or split it across multiple keys",
		}
	}

	e.memMu.Lock()
	defer e.memMu.Unlock()

	m, err := loadMemory()
	if err != nil {
		return "", err
	}
	m.Entries[key] = memoryEntry{Value: value, Updated: time.Now().UTC().Format(time.RFC3339)}
	m.Version = 1

	// Check total file size before committing.
	preview, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return "", fmt.Errorf("memory: size check marshal: %w", err)
	}
	if len(preview) > memoryMaxFileBytes {
		return "", &ErrWithSuggestion{
			Err:        fmt.Errorf("memory file would exceed 1 MiB limit"),
			Suggestion: `use action="forget" on old keys to free space`,
		}
	}

	if err := saveMemory(m); err != nil {
		return "", err
	}
	return "saved: " + key, nil
}

func (e *Engine) memoryRecall(args map[string]interface{}) (string, error) {
	key, _ := args["key"].(string)
	if err := validateKey(key); err != nil {
		return "", err
	}

	e.memMu.Lock()
	defer e.memMu.Unlock()

	m, err := loadMemory()
	if err != nil {
		return "", err
	}
	entry, ok := m.Entries[key]
	if !ok {
		return "", &ErrWithSuggestion{
			Err:        fmt.Errorf("key not found: %s", key),
			Suggestion: `use action="list" to see available keys`,
		}
	}
	return entry.Value, nil
}

func (e *Engine) memoryList() (string, error) {
	e.memMu.Lock()
	defer e.memMu.Unlock()

	m, err := loadMemory()
	if err != nil {
		return "", err
	}
	keys := make([]string, 0, len(m.Entries))
	for k := range m.Entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	result := struct {
		Keys  []string `json:"keys"`
		Count int      `json:"count"`
	}{Keys: keys, Count: len(keys)}
	if result.Keys == nil {
		result.Keys = []string{}
	}

	data, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("memory: list marshal: %w", err)
	}
	return string(data), nil
}

func (e *Engine) memoryForget(args map[string]interface{}) (string, error) {
	key, _ := args["key"].(string)
	if err := validateKey(key); err != nil {
		return "", err
	}

	e.memMu.Lock()
	defer e.memMu.Unlock()

	m, err := loadMemory()
	if err != nil {
		return "", err
	}
	// Idempotent: not found is success.
	if _, exists := m.Entries[key]; !exists {
		return "forgotten: " + key, nil
	}
	delete(m.Entries, key)
	if err := saveMemory(m); err != nil {
		return "", err
	}
	return "forgotten: " + key, nil
}
