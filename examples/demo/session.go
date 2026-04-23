package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func saveSession(cfg *config, messages []message) error {
	if cfg.sessionID == "" {
		return nil
	}
	if err := os.MkdirAll(cfg.sessionDir, 0o755); err != nil {
		return fmt.Errorf("mkdir session dir: %w", err)
	}
	path := filepath.Join(cfg.sessionDir, cfg.sessionID+".json")
	data, err := json.MarshalIndent(messages, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(path, data)
}

func loadSession(cfg *config) ([]message, error) {
	if cfg.sessionID == "" {
		latest, err := findLatestSession(cfg.sessionDir)
		if err != nil {
			return nil, err
		}
		cfg.sessionID = latest
		fmt.Fprintf(os.Stderr, "resuming latest session: %s\n", cfg.sessionID)
	}
	path := filepath.Join(cfg.sessionDir, cfg.sessionID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var msgs []message
	if err := json.Unmarshal(data, &msgs); err != nil {
		return nil, fmt.Errorf("decode session %s: %w", path, err)
	}
	return msgs, nil
}

func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".session-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func findLatestSession(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", errors.New("no sessions found (session directory does not exist)")
		}
		return "", err
	}

	var latest string
	var latestTime time.Time
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".json") {
			continue
		}
		info, err := ent.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(latestTime) {
			latestTime = info.ModTime()
			latest = strings.TrimSuffix(ent.Name(), ".json")
		}
	}

	if latest == "" {
		return "", errors.New("no sessions found")
	}
	return latest, nil
}
