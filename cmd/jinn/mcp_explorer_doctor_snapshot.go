package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/dotcommander/jinn/internal/mcpexplore"
	"github.com/dotcommander/jinn/internal/mcpsnapshot"
	"github.com/voocel/mcp-sdk-go/protocol"
)

type mcpDoctorSnapshotOutcome struct {
	warnings      []string
	approvalDrift bool
	failed        bool
}

func doctorSnapshotWarnings(alias string, identity *protocol.Implementation, tools []*protocol.Tool) mcpDoctorSnapshotOutcome {
	registry, _, err := mcpexplore.LoadRegistry()
	if err != nil {
		return mcpDoctorSnapshotOutcome{warnings: []string{"snapshot_registry_unavailable"}, failed: true}
	}
	server, exists := registry.Servers[alias]
	if !exists {
		return mcpDoctorSnapshotOutcome{warnings: []string{"snapshot_alias_missing"}, failed: true}
	}
	current, warnings, err := mcpsnapshot.Build(alias, server, identity, tools, time.Now())
	if err != nil {
		return mcpDoctorSnapshotOutcome{warnings: []string{"snapshot_capture_invalid"}, failed: true}
	}
	output := make([]string, 0, len(warnings)+1)
	for _, warning := range warnings {
		output = append(output, "metadata_"+warning.Code)
	}
	approved, exists, err := mcpsnapshot.Load(alias)
	if err != nil {
		if exists {
			return mcpDoctorSnapshotOutcome{warnings: append(output, "snapshot_corrupt"), approvalDrift: true}
		}
		return mcpDoctorSnapshotOutcome{warnings: append(output, "snapshot_unavailable"), failed: true}
	}
	if !exists {
		return mcpDoctorSnapshotOutcome{warnings: append(output, "snapshot_missing"), approvalDrift: true}
	}
	changes, err := mcpsnapshot.Diff(approved, current)
	if err != nil {
		return mcpDoctorSnapshotOutcome{warnings: append(output, "snapshot_compare_failed"), failed: true}
	}
	if len(changes) != 0 {
		return mcpDoctorSnapshotOutcome{warnings: append(output, "snapshot_drift"), approvalDrift: true}
	}
	return mcpDoctorSnapshotOutcome{warnings: output}
}

//nolint:gosec // Paths come only from validated aliases under the resolved Jinn config directory.
func mcpSnapshotPermissionWarnings(aliases []string) ([]string, error) {
	if len(aliases) == 0 {
		return nil, nil
	}
	path, err := mcpsnapshot.Path(aliases[0])
	if err != nil {
		return nil, err
	}
	warnings := make([]string, 0, 1+len(aliases)*3)
	info, err := os.Stat(filepath.Dir(path))
	if err == nil && info.Mode().Perm() != 0o700 {
		warnings = append(warnings, fmt.Sprintf("%s permissions are more permissive than required", filepath.Base(filepath.Dir(path))))
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	for _, alias := range aliases {
		aliasPath, err := mcpsnapshot.Path(alias)
		if err != nil {
			return nil, err
		}
		for _, item := range []string{aliasPath, aliasPath + ".bak", aliasPath + ".lock"} {
			info, err := os.Stat(item)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, err
			}
			if info.Mode().Perm() != 0o600 {
				warnings = append(warnings, fmt.Sprintf("%s permissions are more permissive than required", filepath.Base(item)))
			}
		}
	}
	return warnings, nil
}
