package jinn

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// migrateLegacyMemory imports a pre-existing memory.json (flat key→value, no
// scopes) into the global scope, then renames it to memory.json.migrated. The
// rename happens only after all inserts succeed, so a mid-import failure leaves
// memory.json in place for a clean retry. A missing file is a no-op (also the
// idempotent re-run path once the rename has happened).
//
// The 16 KiB per-value cap is NOT enforced during migration: legacy data is
// grandfathered in regardless of size.
func (e *Engine) migrateLegacyMemory(ctx context.Context, db *sql.DB) error {
	base := os.Getenv("JINN_CONFIG_DIR")
	if base == "" {
		dir, err := os.UserConfigDir()
		if err != nil {
			return fmt.Errorf("memory: migrate resolve config dir: %w", err)
		}
		base = dir
	}
	legacyPath := filepath.Join(base, "jinn", "memory.json")

	data, err := os.ReadFile(legacyPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("memory: migrate read: %w", err)
	}

	type legacyEntry struct {
		Value   string `json:"value"`
		Updated string `json:"updated"`
	}
	type legacyFile struct {
		Version int                    `json:"version"`
		Entries map[string]legacyEntry `json:"entries"`
	}

	var m legacyFile
	if err := json.Unmarshal(data, &m); err != nil {
		return fmt.Errorf("memory: migrate unmarshal: %w", err)
	}

	for k, v := range m.Entries {
		updated := v.Updated
		if updated == "" {
			updated = time.Now().UTC().Format(time.RFC3339)
		}
		if _, err := db.ExecContext(ctx, "INSERT OR IGNORE INTO memory(scope,key,value,updated) VALUES(?,?,?,?)", globalScope, k, v.Value, updated); err != nil {
			return fmt.Errorf("memory: migrate insert: %w", err)
		}
	}

	if err := os.Rename(legacyPath, legacyPath+".migrated"); err != nil {
		return fmt.Errorf("memory: migrate rename: %w", err)
	}
	return nil
}
