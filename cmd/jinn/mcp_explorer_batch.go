package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/dotcommander/jinn/internal/mcpbatch"
	"github.com/dotcommander/jinn/internal/mcpexplore"
	"github.com/dotcommander/jinn/internal/mcpsnapshot"
	"github.com/voocel/mcp-sdk-go/protocol"
)

const (
	mcpBatchOptionCallTimeout = "--call-timeout"
	mcpBatchOptionDryRun      = "--dry-run"
	mcpBatchOptionFile        = "--file"
)

type mcpBatchConfig struct {
	alias       string
	file        string
	timeout     time.Duration
	callTimeout time.Duration
	dryRun      bool
	format      string
}

type mcpBatchOutput struct {
	Version             int               `json:"version"`
	Alias               string            `json:"alias"`
	ManifestFingerprint string            `json:"manifest_fingerprint"`
	DryRun              bool              `json:"dry_run,omitempty"`
	Results             []mcpbatch.Result `json:"results"`
}

type mcpBatchManifestCapture struct {
	discovery *protocol.DiscoverResult
	tools     []*protocol.Tool
	snapshot  mcpsnapshot.Snapshot
}

type mcpBatchReportedError struct{}

func (mcpBatchReportedError) Error() string         { return "MCP batch completed with failures" }
func (mcpBatchReportedError) ExitStatus() int       { return 2 }
func (mcpBatchReportedError) AlreadyReported() bool { return true }

//nolint:gocyclo // Preflight gates remain explicit and sequential before any call starts.
func runMCPBatch(parent context.Context, args []string) error {
	config, err := parseMCPBatch(args)
	if err != nil {
		return err
	}
	input, err := readMCPBatchInput(config.file)
	if err != nil {
		return err
	}
	input.CallTimeout = config.callTimeout
	approved, exists, err := mcpsnapshot.Load(config.alias)
	if err != nil {
		return fmt.Errorf("load MCP batch approval snapshot: %w", err)
	}
	if !exists {
		return fmt.Errorf("MCP batch @%s requires an approved snapshot; run jinn mcp snapshot @%s --accept", config.alias, config.alias)
	}
	registry, _, err := mcpexplore.LoadRegistry()
	if err != nil {
		return err
	}
	server, exists := registry.Servers[config.alias]
	if !exists {
		return fmt.Errorf("MCP server alias @%s not found", config.alias)
	}
	target, err := mcpexplore.TargetForServer(server, os.Environ())
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(parent, config.timeout)
	defer cancel()
	client, err := mcpexplore.New(ctx, target)
	if err != nil {
		return fmt.Errorf("connect MCP server: %w", err)
	}
	defer func() { _ = client.Close() }()
	currentCapture, err := captureMCPBatchManifest(ctx, config.alias, server, client)
	if err != nil {
		return err
	}
	if approvalErr := requireMCPBatchApproval(approved, currentCapture.snapshot); approvalErr != nil {
		return approvalErr
	}
	approvedTools, err := mcpBatchSnapshotTools(approved)
	if err != nil {
		return err
	}
	if err := mcpbatch.Validate(input, approvedTools, currentCapture.tools); err != nil {
		return fmt.Errorf("validate MCP batch: %w", err)
	}
	if err := checkMCPBatchFinalManifest(ctx, config.alias, approved, currentCapture.snapshot, client); err != nil {
		return err
	}
	output := mcpBatchOutput{Version: 1, Alias: config.alias, ManifestFingerprint: approved.ManifestFingerprint, Results: []mcpbatch.Result{}}
	if config.dryRun {
		output.DryRun = true
		return writeMCPExplorerOutput(os.Stdout, config.format, output)
	}
	output.Results = mcpbatch.Execute(ctx, client, input, projectMCPBatchResult)
	if err := writeMCPExplorerOutput(os.Stdout, config.format, output); err != nil {
		return err
	}
	if !mcpbatch.AllOK(output.Results) {
		return mcpBatchReportedError{}
	}
	return nil
}

func readMCPBatchInput(path string) (mcpbatch.Input, error) {
	var reader io.Reader = os.Stdin
	var file *os.File
	if path != "-" {
		var err error
		file, err = os.Open(path)
		if err != nil {
			return mcpbatch.Input{}, fmt.Errorf("open batch input: %w", err)
		}
		defer func() { _ = file.Close() }()
		reader = file
	}
	input, err := mcpbatch.Decode(reader)
	if err != nil {
		return mcpbatch.Input{}, err
	}
	return input, nil
}

func captureMCPBatchManifest(ctx context.Context, alias string, server mcpexplore.Server, client *mcpexplore.Client) (mcpBatchManifestCapture, error) {
	discovery, tools, err := discoverMCPExplorerTools(ctx, client)
	if err != nil {
		return mcpBatchManifestCapture{}, err
	}
	snapshot, _, err := mcpsnapshot.Build(alias, server, discovery.Meta.ServerInfo, tools, time.Now())
	if err != nil {
		return mcpBatchManifestCapture{}, fmt.Errorf("build current MCP batch manifest: %w", err)
	}
	return mcpBatchManifestCapture{discovery: discovery, tools: tools, snapshot: snapshot}, nil
}

func requireMCPBatchApproval(approved, current mcpsnapshot.Snapshot) error {
	if approved.TargetFingerprint != current.TargetFingerprint {
		return errors.New("MCP batch target configuration differs from its approved snapshot")
	}
	if approved.ManifestFingerprint != current.ManifestFingerprint {
		return errors.New("MCP batch manifest differs from its approved snapshot")
	}
	return nil
}

func checkMCPBatchFinalManifest(ctx context.Context, alias string, approved, initial mcpsnapshot.Snapshot, client *mcpexplore.Client) error {
	registry, _, err := mcpexplore.LoadRegistry()
	if err != nil {
		return err
	}
	server, exists := registry.Servers[alias]
	if !exists {
		return fmt.Errorf("MCP server alias @%s not found", alias)
	}
	currentCapture, err := captureMCPBatchManifest(ctx, alias, server, client)
	if err != nil {
		return err
	}
	if currentCapture.snapshot.TargetFingerprint != initial.TargetFingerprint || currentCapture.snapshot.ManifestFingerprint != initial.ManifestFingerprint {
		return errors.New("MCP batch manifest changed during preflight")
	}
	if err := requireMCPBatchApprovalCurrent(alias, approved); err != nil {
		return err
	}
	return requireMCPBatchApproval(approved, currentCapture.snapshot)
}

func requireMCPBatchApprovalCurrent(alias string, initial mcpsnapshot.Snapshot) error {
	current, exists, err := mcpsnapshot.Load(alias)
	if err != nil {
		return fmt.Errorf("reload MCP batch approval snapshot: %w", err)
	}
	if !exists {
		return errors.New("MCP batch approval snapshot was removed during preflight")
	}
	if current.Version != initial.Version || current.Alias != initial.Alias || current.TargetFingerprint != initial.TargetFingerprint || current.ManifestFingerprint != initial.ManifestFingerprint {
		return errors.New("MCP batch approval snapshot changed during preflight")
	}
	return nil
}

func mcpBatchSnapshotTools(snapshot mcpsnapshot.Snapshot) ([]*protocol.Tool, error) {
	tools := make([]*protocol.Tool, 0, len(snapshot.Tools))
	for _, raw := range snapshot.Tools {
		decoder := json.NewDecoder(strings.NewReader(string(raw)))
		decoder.UseNumber()
		var tool protocol.Tool
		if err := decoder.Decode(&tool); err != nil {
			return nil, fmt.Errorf("decode approved MCP tool: %w", err)
		}
		tools = append(tools, &tool)
	}
	return tools, nil
}

func projectMCPBatchResult(result *protocol.CallToolResult, call mcpbatch.Call) (any, error) {
	output := mcpExplorerCallOutput{ResultType: protocol.ResultTypeComplete, Content: result.Content, StructuredContent: result.StructuredContent, IsError: result.IsError}
	if result.IsError || call.Select == nil {
		return output, nil
	}
	options := mcpExplorerProjectionOptions{pointer: *call.Select, hasSelect: true}
	if call.Head != nil {
		options.head, options.hasHead = *call.Head, true
	}
	return projectMCPExplorerCallValue(output, options)
}
