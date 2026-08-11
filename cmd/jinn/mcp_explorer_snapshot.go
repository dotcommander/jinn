package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/dotcommander/jinn/internal/mcpexplore"
	"github.com/dotcommander/jinn/internal/mcpsnapshot"
)

const mcpSnapshotOptionAccept = "--accept"

type mcpSchemaDiffOutput struct {
	SchemaVersion int                      `json:"schema_version"`
	Alias         string                   `json:"alias"`
	Status        string                   `json:"status"`
	Snapshot      *mcpsnapshot.Snapshot    `json:"snapshot,omitempty"`
	Warnings      []mcpsnapshot.Warning    `json:"warnings,omitempty"`
	Changes       []mcpsnapshot.DiffChange `json:"changes,omitempty"`
}

type mcpSchemaDiffReportedError struct{}

func (mcpSchemaDiffReportedError) Error() string         { return "MCP schema drift detected" }
func (mcpSchemaDiffReportedError) ExitStatus() int       { return 2 }
func (mcpSchemaDiffReportedError) AlreadyReported() bool { return true }

func runMCPSnapshot(ctx context.Context, args []string) error {
	alias, err := parseMCPSnapshotArgs(args)
	if err != nil {
		return err
	}
	current, warnings, err := captureMCPSnapshot(ctx, alias)
	if err != nil {
		return err
	}
	if err := mcpsnapshot.Save(current); err != nil {
		return fmt.Errorf("save MCP snapshot: %w", err)
	}
	return json.NewEncoder(os.Stdout).Encode(mcpSchemaDiffOutput{SchemaVersion: 1, Alias: alias, Status: "accepted", Snapshot: &current, Warnings: warnings})
}

//nolint:nestif // Missing, corrupt, clean, and drift outcomes share one stable report envelope.
func runMCPSchemaDiff(ctx context.Context, args []string) error {
	format, args, err := collectMCPExplorerJSONOrHumanFormat(args)
	if err != nil {
		return err
	}
	alias, err := parseMCPSchemaDiffArgs(args)
	if err != nil {
		return err
	}
	approved, exists, err := mcpsnapshot.Load(alias)
	if err != nil {
		if exists {
			output := mcpSchemaDiffOutput{SchemaVersion: 1, Alias: alias, Status: "snapshot_corrupt", Changes: []mcpsnapshot.DiffChange{{Kind: "snapshot_corrupt", Message: "approval snapshot is corrupt"}}}
			if writeErr := writeMCPExplorerOutput(os.Stdout, format, output); writeErr != nil {
				return writeErr
			}
			return mcpSchemaDiffReportedError{}
		}
		return err
	}
	current, warnings, err := captureMCPSnapshot(ctx, alias)
	if err != nil {
		return err
	}
	output := mcpSchemaDiffOutput{SchemaVersion: 1, Alias: alias, Warnings: warnings}
	if !exists {
		output.Status = "missing_approval"
		output.Changes = []mcpsnapshot.DiffChange{{Kind: "snapshot_missing", Message: "approval snapshot is missing"}}
	} else {
		output.Changes, err = mcpsnapshot.Diff(approved, current)
		if err != nil {
			return fmt.Errorf("compare MCP snapshot: %w", err)
		}
		if len(output.Changes) == 0 {
			output.Status = "clean"
		} else {
			output.Status = "drift"
		}
	}
	if err := writeMCPExplorerOutput(os.Stdout, format, output); err != nil {
		return err
	}
	if output.Status != "clean" {
		return mcpSchemaDiffReportedError{}
	}
	return nil
}

func parseMCPSnapshotArgs(args []string) (string, error) {
	if len(args) != 2 || args[1] != mcpSnapshotOptionAccept {
		return "", errors.New("usage: jinn mcp snapshot @NAME --accept")
	}
	return parseMCPSnapshotAlias(args[0])
}

func parseMCPSchemaDiffArgs(args []string) (string, error) {
	if len(args) != 1 {
		return "", errors.New("usage: jinn mcp schema-diff @NAME")
	}
	return parseMCPSnapshotAlias(args[0])
}

func parseMCPSnapshotAlias(value string) (string, error) {
	if !strings.HasPrefix(value, "@") || len(value) == 1 {
		return "", errors.New("MCP snapshot commands require a registered @NAME target")
	}
	return strings.TrimPrefix(value, "@"), nil
}

func captureMCPSnapshot(parent context.Context, alias string) (mcpsnapshot.Snapshot, []mcpsnapshot.Warning, error) {
	registry, _, err := mcpexplore.LoadRegistry()
	if err != nil {
		return mcpsnapshot.Snapshot{}, nil, err
	}
	server, exists := registry.Servers[alias]
	if !exists {
		return mcpsnapshot.Snapshot{}, nil, fmt.Errorf("MCP server alias @%s not found", alias)
	}
	target, err := mcpexplore.TargetForServer(server, os.Environ())
	if err != nil {
		return mcpsnapshot.Snapshot{}, nil, err
	}
	ctx, cancel := context.WithTimeout(parent, mcpExplorerDefaultTimeout)
	defer cancel()
	client, err := mcpexplore.New(ctx, target)
	if err != nil {
		return mcpsnapshot.Snapshot{}, nil, fmt.Errorf("connect MCP server: %w", err)
	}
	defer func() { _ = client.Close() }()
	discovery, tools, err := discoverMCPExplorerTools(ctx, client)
	if err != nil {
		return mcpsnapshot.Snapshot{}, nil, err
	}
	return mcpsnapshot.Build(alias, server, discovery.Meta.ServerInfo, tools, time.Now())
}
