package jinn

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type configFile struct {
	RelatedContext relatedContextConfig `json:"related_context"`
}

type relatedContextConfig struct {
	Paths []string `json:"paths"`
}

func jinnConfigDir() (string, error) {
	base := os.Getenv("JINN_CONFIG_DIR")
	if base == "" {
		dir, err := os.UserConfigDir()
		if err != nil {
			return "", fmt.Errorf("resolve config dir: %w", err)
		}
		base = dir
	}
	return filepath.Join(base, "jinn"), nil
}

func jinnConfigPath() (string, error) {
	dir, err := jinnConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

func loadConfigFile() (configFile, string, error) {
	path, err := jinnConfigPath()
	if err != nil {
		return configFile{}, "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return configFile{}, path, nil
		}
		return configFile{}, path, err
	}
	var cfg configFile
	if err := json.Unmarshal(data, &cfg); err != nil {
		return configFile{}, path, err
	}
	return cfg, path, nil
}

func expandConfigPath(raw, configPath string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("empty path")
	}
	if raw == "~" || strings.HasPrefix(raw, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("expand ~: %w", err)
		}
		raw = home + raw[1:]
	}
	if !filepath.IsAbs(raw) {
		raw = filepath.Join(filepath.Dir(configPath), raw)
	}
	return filepath.Clean(raw), nil
}
